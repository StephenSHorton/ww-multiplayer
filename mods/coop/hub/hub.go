// Package hub is the server side of co-op: the CoopHub (§4.4), which the
// host's network.Server hands every co-op connection (network.CoopHandler).
//
// One goroutine owns all world state: the world (a world.Store, which
// journals and flushes it to the world file), the scoped stores
// (world.Scopes) and every peer's session. The server's per-connection
// goroutines only queue work for it, so the merge is single-writer and its
// order is the seq's. Per peer, a session runs:
//
//	CO_HELLO        -> classify the replica and its file; CO_WELCOME (and a NOTICE
//	                   saying why, when it is not bound or refused)
//	WORLD           -> bound: merge, answer with the merged image, relay the
//	                   effective delta to the other bound peers as GAINS
//	                   unbound: keep it for the bind check (never merged), answer
//	                   with the world
//	GAINS           -> bound: merge what the class table, the policy and the
//	                   tombstones allow; relay the effective delta (origin, seq)
//	PRESENCE        -> relay to the other peers; for a bound peer, scope the
//	                   stores and send what it newly reaches
//	SCOPED          -> keep the bits the host's M1 verdict grants
//	                   (Config.SyncedScoped), OR them into the store (ROOMEDGE:
//	                   relay only) and relay the new bits to the bound peers
//	                   in that scope
//	CTRL            -> RESYNC: send the world; COOP_OFF: the features the replica
//	                   now wants; TOMBSTONE: the host blocks a bit
//
// A replica's file is BOUND when its tag passes V1-V6 for this world
// (§5.2); only then do its WORLD, GAINS and SCOPED count. An unbound file
// can see the world (for the join preflight) and ask to bind (AskBind,
// §5.3); a leaked tag is unbound until the replica scrubs it; a file
// bound to another world, a second-quest file, and a replica whose class
// table differs from the host's are refused: no co-op for that session,
// while pose sync goes on over the player connection. A replica whose M1
// stage tables are not the host's (or either side has none) runs without
// the features that read them (TableFeatures): its memory switch bytes
// are neither taken nor sent, and only the latching switches of the
// host's table enter the world or the scoped stores at all
// (Config.SyncedSwitch, Config.SyncedScoped). From M4 the story bits of
// the SyncMask enter the world too (DefaultPolicy), from and to the
// players granted FeatureStory, and with them a file can be far behind
// the world (§5.3).
//
// Nothing a peer does can stall the hub or the other peers: a peer's
// inbound frames wait in a bounded share of the hub's queue, and its
// outbound messages in a bounded outbox with a writer of its own; a peer
// that overruns either is disconnected, and its replica resyncs on
// reconnect (CO_HELLO, then WORLD both ways, §5.5). The scoped stores a
// peer can make are bounded by where the players are, and a PRESENCE
// costs only the stores near it (world.Scopes).
//
// Citations follow wwapi/addr: file:line is the zeldaret/tww
// decomp at 4d78c994, §N a section of docs/08-coop-design.md.
package hub

import (
	"errors"
	"fmt"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
)

const (
	// eventQueue is the hub's inbound queue: frames, attaches, detaches
	// and host calls, in arrival order.
	eventQueue = 1024
	// inboxPerPeer is how many of a peer's frames may wait in the queue.
	// A peer that sends faster than the hub merges is disconnected rather
	// than let it crowd out the others.
	inboxPerPeer = 64
	// outboxLen is how many messages may wait for a peer's writer. A peer
	// that reads slower than the hub relays is disconnected.
	outboxLen = 256
	// complainEvery is the least time between two NOTICEs of one code
	// about a peer's frames (a replica with a bug would otherwise get one
	// per frame).
	complainEvery = time.Second
)

// ErrStopped is returned once the hub has stopped.
var ErrStopped = errors.New("co-op hub stopped")

// Conn is one co-op connection as the hub uses it. *network.CoopConn is
// one; tests use their own.
type Conn interface {
	// ID is the connection's co-op ID, 1..255, unique among live
	// connections: the GAINS origin and the scoped stores' player.
	ID() byte
	// Name is the player name the connection joined with.
	Name() string
	// RemoteAddr is the peer's address (nil if unknown).
	RemoteAddr() net.Addr
	// Send queues one 'O' payload. It may block; the hub calls it only
	// from the connection's own writer.
	Send(payload []byte) error
	// Close disconnects the peer. The hub expects a Detach after it.
	Close()
}

// Config sets up a Hub.
type Config struct {
	// Store is the world the hub serves, opened (world.Open) or founded
	// (world.Create) by the caller. The hub owns it from New on, and Stop
	// stops it, which writes what is left.
	Store *world.Store
	// StageTblHash is the identity of the host's M1 tables (CO_HELLO's
	// stageTblHash, tables.Tables.Identity), 0 when the host has none. A
	// replica whose hash differs, or is 0, or any replica of a host
	// without tables, gets no TableFeatures, with a NOTICE saying why;
	// the rest of its session runs. (One whose class table differs from
	// this build's, wire.ClassHash, is refused outright.)
	StageTblHash uint64
	// SyncedSwitch is the host's M1 verdict on memory switch no of save
	// table t (synctables.SyncedMemorySwitch, from the tables
	// StageTblHash names): only these enter the world, everything else in
	// the memory switch bytes is refused (SwitchPolicy). Nil, a host
	// without tables, keeps every memory switch out, as M2 did.
	SyncedSwitch func(t, no int) bool
	// Features is what the host allows (DefaultFeatures, or less);
	// SetFeatures changes it while the hub runs. Bits outside
	// SessionFeatures are ignored.
	Features Feature
	// Merge is --coop-merge (§5.3): a file with progress the world lacks,
	// or one far behind it, may bind.
	Merge bool
	// Policy narrows what the world accepts below the class masks; nil is
	// DefaultPolicy: the story bits of the SyncMask, the registers of the
	// allowlist, and every OR and MAX byte, but no memory switch. With
	// SyncedSwitch set, its memory switch bytes are replaced by
	// SyncedSwitch's grants (SwitchPolicy); without, they are as given
	// (none in DefaultPolicy).
	Policy *world.Policy
	// SyncedScoped is the host's M1 verdict on the switches SCOPED
	// carries (synctables.SyncedScopedSwitch, from the tables
	// StageTblHash names): whether stage switch no, 0x80-0xEF, latches as
	// an actor of save table table, stage stage and room room writes it
	// (room -1 for a dan switch, which is the table's). The scoped stores
	// keep and relay only the dan, zone and room-edge bits it grants
	// (ScopeFilter); every other such bit of a SCOPED is dropped and
	// counted (PeerStatus.ScopedRefused), without a NOTICE, as
	// SwitchPolicy's refusals are. Nil, a host without tables, grants
	// none (and such a host grants no FeaturePuzzle anyway). Kill bits
	// are SyncedKill's.
	SyncedScoped func(table int, stage string, room, no int) bool
	// SyncedKill is the host's M1 verdict on the kill bits SCOPED KILL
	// carries (synctables.SyncedKill): whether a death of setID in
	// room of stage, as a game of save table table records it, syncs. The
	// scoped stores keep and relay only the bits it grants (KillFilter);
	// the rest are dropped and counted in PeerStatus.ScopedRefused, as
	// SyncedScoped's. Nil, a host without tables, keeps none (and such a
	// host grants no FeatureCombat anyway).
	SyncedKill func(table int, stage string, room, setID int) bool
	// IsHost reports whether a connection is the host's own, which alone
	// may tombstone over the wire (CTRL TOMBSTONE). Nil means a
	// connection from a loopback address.
	IsHost func(Conn) bool
	// Logf, if set, gets the hub's log lines.
	Logf func(format string, args ...any)
	// Now is the clock for NOTICE rate limits; nil is time.Now.
	Now func() time.Time
}

// Hub is the CoopHub. It implements network.CoopHandler; set it as the
// server's Coop before Start. Its methods are safe for concurrent use.
type Hub struct {
	cfg    Config
	store  *world.Store
	id     world.ID
	logf   func(format string, args ...any)
	now    func() time.Time
	isHost func(Conn) bool

	events   chan event
	quit     chan struct{} // closed by Stop
	exited   chan struct{} // closed when run returns
	stopOnce sync.Once
	stopErr  error // set before exited closes

	mu    sync.Mutex
	links map[Conn]*link // attached connections, for Frame and Detach

	// direct makes send call Conn.Send from the hub goroutine instead of
	// a writer: tests read what the hub sent right after a barrier.
	direct bool

	// Owned by run's goroutine.
	features Feature
	peers    map[Conn]*peer
	byID     map[world.Player]*peer
	order    []*peer // every peer, by ID: relays go out in a fixed order
	scopes   *world.Scopes
}

var _ network.CoopHandler = (*Hub)(nil)

// New starts a hub serving cfg.Store's world.
func New(cfg Config) (*Hub, error) {
	h, err := newHub(cfg, false)
	if err != nil {
		return nil, err
	}
	go h.run()
	return h, nil
}

// newHub builds a hub without starting its goroutine.
func newHub(cfg Config, direct bool) (*Hub, error) {
	if cfg.Store == nil {
		return nil, errors.New("co-op hub: no world store")
	}
	h := &Hub{
		cfg:      cfg,
		store:    cfg.Store,
		id:       cfg.Store.ID(),
		logf:     cfg.Logf,
		now:      cfg.Now,
		isHost:   cfg.IsHost,
		events:   make(chan event, eventQueue),
		quit:     make(chan struct{}),
		exited:   make(chan struct{}),
		links:    map[Conn]*link{},
		direct:   direct,
		features: cfg.Features & SessionFeatures,
		peers:    map[Conn]*peer{},
		byID:     map[world.Player]*peer{},
		scopes:   world.NewScopes(),
	}
	if h.logf == nil {
		h.logf = func(string, ...any) {}
	}
	if h.now == nil {
		h.now = time.Now
	}
	if h.isHost == nil {
		h.isHost = fromLoopback
	}
	pol := cfg.Policy
	if pol == nil {
		pol = DefaultPolicy()
	}
	if cfg.SyncedSwitch != nil {
		pol = SwitchPolicy(pol, cfg.SyncedSwitch)
	}
	h.store.SetPolicy(pol)
	h.scopes.Filter = Filter(cfg.SyncedScoped, cfg.SyncedKill)
	return h, nil
}

// fromLoopback reports whether c comes from a loopback address: the host's
// own coop-sync, or a command run on the host machine.
func fromLoopback(c Conn) bool {
	a, ok := c.RemoteAddr().(*net.TCPAddr)
	return ok && a.IP.IsLoopback()
}

// World is the ID of the world the hub serves.
func (h *Hub) World() world.ID { return h.id }

// Stop ends the hub: it handles what is already queued, disconnects every
// peer and stops the store, which writes the world if it changed. It
// returns the store's error. Idempotent; later calls return the same.
func (h *Hub) Stop() error {
	h.stopOnce.Do(func() { close(h.quit) })
	<-h.exited
	return h.stopErr
}

// Attach is network.CoopHandler's: a new co-op connection.
func (h *Hub) Attach(c *network.CoopConn) error { return h.attach(c) }

// Frame is network.CoopHandler's: one inbound 'O' payload.
func (h *Hub) Frame(c *network.CoopConn, payload []byte) { h.frame(c, payload) }

// Detach is network.CoopHandler's: the connection ended.
func (h *Hub) Detach(c *network.CoopConn) { h.detach(c) }

// link is the transport side of one attached connection: its share of the
// inbound queue and its outbox.
type link struct {
	conn     Conn
	pending  atomic.Int32  // its frames waiting in h.events
	overflow atomic.Bool   // it overran inboxPerPeer (logged once)
	out      chan []byte   // encoded messages for its writer
	done     chan struct{} // closed by the hub when the peer is gone
}

// writeLoop sends the outbox until the hub drops the peer. A failed send
// closes the connection; its Detach follows.
func (l *link) writeLoop() {
	for {
		select {
		case <-l.done:
			return
		case b := <-l.out:
			if err := l.conn.Send(b); err != nil {
				l.conn.Close()
				return
			}
		}
	}
}

type evKind uint8

const (
	evAttach evKind = iota + 1
	evFrame
	evDetach
	evCall
)

type event struct {
	kind    evKind
	link    *link
	payload []byte
	reply   chan error // evAttach
	call    func()     // evCall
}

// enqueue queues ev for the hub goroutine, waiting while the queue is full.
// It reports false once the hub is stopping.
func (h *Hub) enqueue(ev event) bool {
	select {
	case <-h.quit:
		return false
	default:
	}
	select {
	case h.events <- ev:
		return true
	case <-h.quit:
		return false
	}
}

// do runs f on the hub goroutine and waits for it.
func (h *Hub) do(f func()) error {
	done := make(chan struct{})
	if !h.enqueue(event{kind: evCall, call: func() { f(); close(done) }}) {
		return ErrStopped
	}
	select {
	case <-done:
		return nil
	case <-h.exited:
		select {
		case <-done:
			return nil
		default:
			return ErrStopped
		}
	}
}

func (h *Hub) attach(c Conn) error {
	l := &link{conn: c, out: make(chan []byte, outboxLen), done: make(chan struct{})}
	reply := make(chan error, 1)
	if !h.enqueue(event{kind: evAttach, link: l, reply: reply}) {
		return ErrStopped
	}
	select {
	case err := <-reply:
		return err
	case <-h.exited:
		select {
		case err := <-reply:
			return err
		default:
			return ErrStopped
		}
	}
}

func (h *Hub) frame(c Conn, payload []byte) {
	h.mu.Lock()
	l := h.links[c]
	h.mu.Unlock()
	if l == nil {
		return
	}
	if l.pending.Add(1) > inboxPerPeer {
		l.pending.Add(-1)
		if !l.overflow.Swap(true) {
			h.logf("co-op: %s (co-op %d): more than %d co-op messages waiting for the hub; disconnecting", c.Name(), c.ID(), inboxPerPeer)
		}
		c.Close()
		return
	}
	if !h.enqueue(event{kind: evFrame, link: l, payload: payload}) {
		l.pending.Add(-1)
	}
}

func (h *Hub) detach(c Conn) {
	h.mu.Lock()
	l := h.links[c]
	delete(h.links, c)
	h.mu.Unlock()
	if l != nil {
		h.enqueue(event{kind: evDetach, link: l})
	}
}

// run is the hub goroutine.
func (h *Hub) run() {
	defer close(h.exited)
	for {
		select {
		case ev := <-h.events:
			h.handle(ev)
		case <-h.quit:
			for {
				select {
				case ev := <-h.events:
					h.handle(ev)
				default:
					h.shutdown()
					return
				}
			}
		}
	}
}

// handle runs one event. A bug that panics costs the peer it was handling
// (disconnected), not the hub: the host process also runs the TUI and the
// pose pipeline.
func (h *Hub) handle(ev event) {
	defer func() {
		if r := recover(); r != nil {
			who := "the hub"
			if ev.link != nil {
				who = fmt.Sprintf("%s (co-op %d)", ev.link.conn.Name(), ev.link.conn.ID())
				ev.link.conn.Close()
			}
			h.logf("co-op: internal error in the hub handling %s: %v; disconnected", who, r)
			if ev.reply != nil {
				ev.reply <- fmt.Errorf("internal error: %v", r)
			}
		}
	}()
	switch ev.kind {
	case evAttach:
		ev.reply <- h.onAttach(ev.link)
	case evFrame:
		ev.link.pending.Add(-1)
		if p := h.peers[ev.link.conn]; p != nil && p.link == ev.link {
			h.onFrame(p, ev.payload)
		}
	case evDetach:
		if p := h.peers[ev.link.conn]; p != nil && p.link == ev.link {
			h.onDetach(p)
		}
	case evCall:
		ev.call()
	}
}

func (h *Hub) onAttach(l *link) error {
	c := l.conn
	id := world.Player(c.ID())
	if id == 0 {
		return errors.New("co-op ID 0")
	}
	if _, dup := h.peers[c]; dup {
		return errors.New("connection attached twice")
	}
	if q := h.byID[id]; q != nil {
		return fmt.Errorf("co-op ID %d is still in use by %s", id, q.name)
	}
	p := &peer{
		link:       l,
		id:         id,
		name:       c.Name(),
		host:       h.isHost(c),
		state:      StateHello,
		place:      world.Nowhere,
		complained: map[wire.NoticeCode]time.Time{},
	}
	h.peers[c] = p
	h.byID[id] = p
	i, _ := slices.BinarySearchFunc(h.order, id, func(q *peer, id world.Player) int { return int(q.id) - int(id) })
	h.order = slices.Insert(h.order, i, p)
	h.mu.Lock()
	h.links[c] = l
	h.mu.Unlock()
	if !h.direct {
		go l.writeLoop()
	}
	return nil
}

func (h *Hub) onDetach(p *peer) {
	h.leaveBound(p)
	h.leaveView(p)
	delete(h.peers, p.link.conn)
	delete(h.byID, p.id)
	h.order = slices.DeleteFunc(h.order, func(q *peer) bool { return q == p })
	close(p.link.done)
}

// shutdown disconnects every peer and stops the store.
func (h *Hub) shutdown() {
	h.mu.Lock()
	h.links = map[Conn]*link{}
	h.mu.Unlock()
	for _, p := range h.order {
		close(p.link.done)
		p.link.conn.Close()
	}
	h.peers = map[Conn]*peer{}
	h.byID = map[world.Player]*peer{}
	h.order = nil
	if err := h.store.Stop(); err != nil {
		h.stopErr = fmt.Errorf("co-op hub: %w", err)
		h.logf("co-op: writing world %s on stop: %v", h.id, err)
	}
}

// send encodes m and queues it for p.
func (h *Hub) send(p *peer, m wire.Msg) {
	b, err := wire.Encode(m)
	if err != nil {
		h.logf("co-op: bug: encoding %s for %s: %v", m.Sub(), p, err)
		return
	}
	h.sendRaw(p, b)
}

// sendAll encodes m once and queues it for every peer in ps.
func (h *Hub) sendAll(ps []*peer, m wire.Msg) {
	if len(ps) == 0 {
		return
	}
	b, err := wire.Encode(m)
	if err != nil {
		h.logf("co-op: bug: encoding %s: %v", m.Sub(), err)
		return
	}
	for _, p := range ps {
		h.sendRaw(p, b)
	}
}

func (h *Hub) sendRaw(p *peer, b []byte) {
	if p.dead {
		return
	}
	if h.direct {
		if err := p.link.conn.Send(b); err != nil {
			p.dead = true
			p.link.conn.Close()
		}
		return
	}
	select {
	case p.link.out <- b:
	default:
		p.dead = true
		h.logf("co-op: %s: %d co-op messages waiting to be sent; disconnecting", p, outboxLen)
		p.link.conn.Close()
	}
}

// sorted returns the peers keep accepts, by ID.
func (h *Hub) sorted(keep func(*peer) bool) []*peer {
	var ps []*peer
	for _, p := range h.order {
		if keep(p) {
			ps = append(ps, p)
		}
	}
	return ps
}

// grant is the features p runs with: what the host allows and p wants,
// less TableFeatures unless p's stage tables are the host's, and whether
// the host merges divergent files.
func (h *Hub) grant(p *peer) Feature {
	f := h.features & p.want & SessionFeatures
	if !h.tablesMatch(p) {
		f &^= TableFeatures
	}
	if h.cfg.Merge {
		f |= FeatureMerge
	}
	return f
}

// Tombstone blocks bits mask of image byte off for good (`coop-world
// block`, §4.4): they leave the world, every later proposal of them is
// refused, and every peer is told (CTRL TOMBSTONE, so a replica stops
// proposing them, and a NOTICE). Replicas that already applied them keep
// them.
func (h *Hub) Tombstone(off int, mask byte) error {
	var err error
	if derr := h.do(func() { err = h.tombstone(off, mask, "the host") }); derr != nil {
		return derr
	}
	return err
}

// SetFeatures changes what the host allows (CTRL COOP_OFF, §4.4; 0 turns
// co-op off for everyone). Every peer is told its new features; one that
// gets the image back is asked for a fresh WORLD (CTRL RESYNC), since it
// missed the relays meanwhile.
func (h *Hub) SetFeatures(f Feature) error {
	return h.do(func() {
		h.features = f & SessionFeatures
		h.logf("co-op: the host's features are now %s", h.features)
		for _, p := range h.sorted((*peer).welcomed) {
			h.regrant(p)
		}
	})
}

// SetMerge changes Config.Merge while the hub runs (the app's host
// allowing a joiner's file to merge after Start, app design §2.8): a
// file with progress the world lacks, or one far behind it, may bind
// from now on (or no longer). Every peer is told its features again, so
// a replica learns FeatureMerge and may plan the bind it was refused.
func (h *Hub) SetMerge(on bool) error {
	return h.do(func() {
		if h.cfg.Merge == on {
			return
		}
		h.cfg.Merge = on
		if on {
			h.logf("co-op: the host now merges files with progress the world lacks (as --coop-merge)")
		} else {
			h.logf("co-op: the host no longer merges files with progress the world lacks")
		}
		for _, p := range h.sorted((*peer).welcomed) {
			h.regrant(p)
		}
	})
}

// Merge reports whether files with progress the world lacks may bind
// (Config.Merge, or SetMerge's).
func (h *Hub) Merge() bool {
	var on bool
	_ = h.do(func() { on = h.cfg.Merge })
	return on
}

// PeerState is where a peer's session is.
type PeerState uint8

const (
	StateHello   PeerState = iota + 1 // attached; no CO_HELLO yet
	StateRefused                      // refused: nothing it sends counts
	StateUnbound                      // its file is not bound: it sees the world and may ask to bind
	StateBound                        // its file is bound to the world: it takes part
)

func (s PeerState) String() string {
	switch s {
	case StateHello:
		return "hello"
	case StateRefused:
		return "refused"
	case StateUnbound:
		return "unbound"
	case StateBound:
		return "bound"
	}
	return fmt.Sprintf("PeerState(%d)", uint8(s))
}

// PeerStatus is one peer as Status reports it.
type PeerStatus struct {
	ID    byte
	Name  string
	Host  bool // may tombstone (Config.IsHost)
	State PeerState
	// File is the file state its last CO_HELLO reported.
	File wire.FileState
	// Reason is why it is refused or unbound (0 when bound).
	Reason   wire.NoticeCode
	Features Feature
	Place    world.Place
	// Rejected counts the proposed bytes the world refused (the policy,
	// tombstones); Duplicates the GAINS batches dropped as repeats.
	Rejected   int
	Duplicates int
	// ScopedRefused counts the bits of its SCOPED words the host's M1
	// verdict refused (Config.SyncedScoped).
	ScopedRefused int
}

// Status is a snapshot of the hub, for the TUI and tests.
type Status struct {
	World    world.ID
	Seq      uint64
	Features Feature // what the host allows
	// Merge: files with progress the world lacks may bind (Config.Merge,
	// or SetMerge's).
	Merge  bool
	Peers  []PeerStatus
	Scopes []world.Key // the scoped stores held
	// CombatAllowed: the host allows FeatureCombat and has a kill verdict
	// (Config.SyncedKill), so the kill union runs (§7.2, M6).
	CombatAllowed bool
	// KillStores counts the KILL stores among Scopes.
	KillStores int
}

// Status returns a snapshot of the hub.
func (h *Hub) Status() (Status, error) {
	var st Status
	err := h.do(func() {
		st = Status{World: h.id, Seq: h.store.Seq(), Features: h.features, Merge: h.cfg.Merge, Scopes: h.scopes.Keys()}
		st.CombatAllowed = h.features&FeatureCombat != 0 && h.cfg.SyncedKill != nil
		for _, k := range st.Scopes {
			if k.Kind == world.ScopeKill {
				st.KillStores++
			}
		}
		for _, p := range h.sorted(func(*peer) bool { return true }) {
			ps := PeerStatus{
				ID: byte(p.id), Name: p.name, Host: p.host, State: p.state, Reason: p.reason,
				Features: p.features, Place: p.place, Rejected: p.rejected, Duplicates: p.dups,
				ScopedRefused: p.scopedRefused,
			}
			if p.hello != nil {
				ps.File = p.hello.FileState
			}
			st.Peers = append(st.Peers, ps)
		}
	})
	return st, err
}
