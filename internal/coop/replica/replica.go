// Package replica is the client side of co-op (§4.2): the coop-sync
// pipeline that runs beside broadcast-pose and puppet-sync, one per
// Dolphin, with its own co-op connection to the host's hub. Every tick
// (10 Hz) it:
//
//  1. reads one snap.Snapshot through mem.Mem and evaluates the gates
//     (gate.Tracker);
//  2. classifies the loaded file's binding tag (bind.Classify, rules
//     V1-V6 against the session's world);
//  3. builds the local image, the saved dSv_save_c with the current
//     table's live memBit folded in (snap.Snapshot.Image), and runs the
//     Differ over it: rising edges only, never a clear;
//  4. sends what the hub needs: CO_HELLO when the file changes, PRESENCE
//     on change and at 1 Hz, and while the file is bound and armed its
//     WORLD once (again after the Differ re-baselines) and then GAINS
//     (SCOPED too, when those features are granted);
//  5. runs the Planner against its view of the world (WORLD, GAINS from
//     the hub) and, with Config.Apply (M3), the reconciler: the writes of
//     the plan whose time has come go through a FrameWriter and its
//     WriteGuard right after the next heartbeat, and are verified on the
//     next tick (reconcile.go). Without Apply (M2, --coop-dry-run) it only
//     logs what it would write ("dry-run: would set ...").
//
// M3 writes the stage progress bits, the memory switches the hub grants,
// the ocean, the charts and the sea map, and nothing else: every byte is
// checked by the WriteGuard (M3Targets, the gates, never a clear), and
// too many faults turn co-op off by itself (Breaker). TurnOff is "coop
// off". At the user's word it also makes the escape hatches (§6.7,
// Escape: refresh area and warp to partner), a stage-change or warp
// request through the same WriteGuard (TargetEscape), when IDLE. The
// other game-RAM writes the replica makes are the binding
// tag's (bind.Bind) and a leaked tag's scrub (bind.Scrub): each is
// offered to the user (Config.OnOffer), made only after Confirm, behind
// the gates, and verified by reading back. A dry run makes only those, as
// M2 did (§5.1 principle 5).
//
// The session follows internal/coop/hub (whose Feature bits it uses):
//
//	connect    -> CO_HELLO on the first tick with Link in a stage
//	CO_WELCOME -> ok: send WORLD (the local image), then GAINS as they rise
//	              unbound: the hub sends its WORLD; the replica runs the
//	              join preflight (Preflight, Config.OnPreflight)
//	              refused: nothing more this connection (pose sync goes on)
//	file change, bind, scrub -> CO_HELLO again on the same connection
//	disconnect -> armed offline (§5.5): keep detecting, queue the gains;
//	              on reconnect CO_HELLO, WORLD both ways, then the queue
//
// A bind (RequestBind, §5.3) is: the preflight, the user's yes to the
// plan's prompt, the file's WORLD (which the hub never merges for an
// unbound file) and a CO_HELLO asking to bind (hub.AskBind, with
// hub.AskUnsaved for a file never saved and loaded back, whose nonce the
// hub records as a pending fresh bind), then, on a CO_WELCOME granting
// hub.FeatureBind, the tag write and a bound CO_HELLO.
//
// Citations follow internal/coop/addr: file:line is the zeldaret/tww
// decomp at 4d78c994, §N a section of docs/08-coop-design.md.
package replica

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"math/bits"
	"slices"
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/derive"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/gate"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/mem"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/snap"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
)

const (
	// TickEvery is the snapshot rate (§4.2: 10 Hz).
	TickEvery = 100 * time.Millisecond
	// PresenceEvery is how often PRESENCE is repeated when nothing
	// changed (§4.4: on change and at 1 Hz).
	PresenceEvery = time.Second
	// maxPlanLines caps the dry-run lines one tick logs; the rest are
	// counted.
	maxPlanLines = 40
	// maxScopedIn caps the scoped stores the replica keeps from the hub.
	// An honest hub sends a replica only its stage's stores (the DAN
	// store, every room's KILL store, the ZONE store of each room it
	// enters: at most 1 + 2*addr.RoomCount), and pruneScoped drops any
	// other stage's every tick, so twice that leaves room for the
	// relays of a stage just left. Only a hostile host reaches it.
	maxScopedIn = 2 * (1 + 2*addr.RoomCount)
)

// Transport is the co-op connection as the replica uses it: Send writes
// one 'O' payload (network.Client.SendCoop).
type Transport interface {
	Send(payload []byte) error
}

// SendFunc adapts a function to Transport.
type SendFunc func(payload []byte) error

// Send calls f.
func (f SendFunc) Send(payload []byte) error { return f(payload) }

// Config sets up a Replica.
type Config struct {
	// Mem is the Dolphin (or FakeMem) the replica reads. Required.
	Mem mem.Mem
	// World is the session's world when this side knows it (the host,
	// whose own hub serves it). Zero for a joiner, which learns it from
	// CO_WELCOME.
	World bind.WorldID
	// StageTblHash is the identity of this build's M1 tables, sent in
	// CO_HELLO (the hub refuses a replica whose tables differ).
	StageTblHash uint64
	// Want is the session features to ask for; 0 asks for what the
	// replica uses: hub.FeatureImage, and with Apply hub.FeatureSwitches,
	// hub.FeatureLiveSwitches, hub.M4Features and hub.FeaturePuzzle too
	// (the host decides whether to allow the live-switch opt-in,
	// --coop-live-switches). A dry run asks for what M2 did,
	// hub.FeatureImage. Bits outside hub.SessionFeatures are ignored.
	Want hub.Feature
	// Policy limits what the replica proposes, plans and writes to the
	// bits that sync; nil is world.DefaultPolicy (no memory switch or
	// story bit), which is also the hub's default. A session with M1
	// tables passes hub.SwitchPolicy over their switch verdict, the
	// host's policy.
	Policy *world.Policy
	// Apply makes the replica write the world's progress into the game
	// (M3, M4) and a co-present partner's room puzzles (M5): the
	// reconcilers hand their writes to a FrameWriter through a WriteGuard
	// of M3Targets | M4Targets | M5Targets. False is M2's dry run
	// (--coop-dry-run): the plan is logged, never written.
	Apply bool
	// Tables are the M1 tables of the user's own disc, which the derived
	// small keys and max life are counted from (derive.Config.Tables);
	// nil leaves both underived. Max magic needs none.
	Tables *tables.Tables
	// Breaker is the writes' off switch (auto-disable, §4.2); nil makes
	// one with the default thresholds. Unused without Apply.
	Breaker *Breaker
	// BeatTimeout is how long a tick's writes wait for the heartbeat; 0
	// is BeatTimeout.
	BeatTimeout time.Duration
	// Label names an image bit (bit -1: the byte) for the dry-run log;
	// nil is DefaultLabel. coop-watch's saveLabel in package main fits
	// behind a two-line adapter: image offsets are dSv_info_c offsets,
	// as mSavedata is at +0 (addr.InfoOffSavedata).
	Label func(off, bit int) string
	// Logf gets the replica's log lines; nil discards them.
	Logf func(format string, args ...any)
	// Now is the clock for the PRESENCE repeat; nil is time.Now.
	Now func() time.Time
	// OnOffer is called, from Tick and never under the replica's lock,
	// when a tag write waits for the user: the scrub of a leaked tag, or
	// a bind RequestBind asked for. Show Offer.Plan.Prompt and call
	// Confirm or Decline with Offer.Plan. Nil leaves offers waiting in
	// Status.
	OnOffer func(Offer)
	// OnPreflight is called, likewise, with each new join preflight of
	// an unbound file against the hub's world.
	OnPreflight func(PreflightReport)
	// PartnerPos returns the partner's position, the latest pose sync
	// has, for a warp to the partner on the same stage (Escape). It is
	// called under the replica's lock, so it must not call back into
	// the replica. Nil leaves only the warps that change stage.
	PartnerPos func() (Pos, bool)
}

// Offer is a tag write waiting for the user's yes.
type Offer struct {
	Plan *bind.Plan
	// Preflight is a bind's preflight against the world (nil for a
	// scrub).
	Preflight *PreflightReport
}

// State is where the replica's session is.
type State uint8

const (
	StateOff     State = iota // not connected, and not armed before
	StateHello                // connected: waiting for a file in game, or for CO_WELCOME
	StateUnbound              // the hub's welcome says the file is not bound to its world
	StateArmed                // the file is bound and the session runs: detecting, proposing, planning (dry run)
	StateOffline              // armed, but disconnected (§5.5 ARMED_OFFLINE): detecting and queueing
	StateRefused              // the hub refused co-op for this connection
)

func (s State) String() string {
	switch s {
	case StateOff:
		return "off"
	case StateHello:
		return "hello"
	case StateUnbound:
		return "unbound"
	case StateArmed:
		return "armed"
	case StateOffline:
		return "armed offline"
	case StateRefused:
		return "refused"
	}
	return fmt.Sprintf("State(%d)", uint8(s))
}

// Report is what one tick did.
type Report struct {
	Err     error // the snapshot could not be read
	Gates   gate.Status
	Verdict bind.Verdict
	State   State
	Why     string // why the replica is not armed, or ""
	Edges   Edges

	Hello      bool // a CO_HELLO went out
	World      bool // the file's WORLD went out
	Gains      int  // gain bytes sent in GAINS
	Queued     int  // gain bytes queued while offline
	Scoped     int  // SCOPED messages sent
	Presence   bool // a PRESENCE went out
	Planned    int  // byte writes planned (dry run: none is made)
	Held       int  // world bits the holds keep back
	TagWritten bool // a confirmed bind or scrub wrote the tag this tick

	// With Config.Apply:
	Wrote    int          // bytes the reconciler wrote this tick
	Waiting  int          // planned M3 writes not due this tick (§6.3's room change, the room hold, a closed gate)
	WriteErr error        // why this tick's writes stopped or were not made, nil if they all went
	Verify   VerifyReport // the last tick's writes, checked against this tick's snapshot
	// RoomChange: this tick is the first STABLE and IDLE one after a room
	// or stage change, when the writes §6.3 defers are made.
	RoomChange bool
	// SwitchWindow: this tick is the one the loaded table's memory
	// switches §6.3 defers are made in: the first STABLE and IDLE tick
	// after a room or stage change on which every shown room's actors
	// have settled (the room hold, reconcile_m5_rooms.go). It is
	// RoomChange's tick or a later one.
	SwitchWindow bool
	// Escape is the escape (§6.7) this tick tried to write, 0 for none,
	// and EscapeErr why it was not written.
	Escape    EscapeKind
	EscapeErr error
}

// Status is the replica's state for the TUI and tests.
type Status struct {
	State    State
	Why      string
	World    bind.WorldID // the session's world, zero until known
	Seq      uint64       // the world seq the replica's view is at
	Features hub.Feature  // granted by the last CO_WELCOME or CTRL
	Verdict  bind.Verdict
	Planned  []Write
	Held     []HeldBit
	Queued   int
	// DryRun: the replica writes no game data (no Config.Apply).
	DryRun bool
	// WritesOff is why the replica's writes are off ("coop off", or an
	// auto-disable), "" while they run.
	WritesOff string
	// Writes counts what the FrameWriter did.
	Writes WriterStats
	Offer  *Offer // waiting for Confirm or Decline
	// Preflight is the last join preflight, while the file is unbound.
	Preflight *PreflightReport
	// Partner is the last PRESENCE the hub relayed.
	Partner *wire.Presence
	// Derive is how the derived small keys and max life stand (the
	// Deriver's tables, and each key table's last tick).
	Derive derive.Status
	// Puzzle is how the room puzzles stand (M5): where Link is, and the
	// partner's dan, zone and room switches written and refused.
	Puzzle PuzzleStatus
}

// helloKey is what a CO_HELLO describes: when it changes, the replica
// says hello again.
type helloKey struct {
	file    fileKey
	tag     [bind.TagSize]byte
	state   bind.State
	askBind bind.Nonce
}

// Replica is one Dolphin's co-op client. Tick runs on one goroutine (Run),
// Handle on the network client's read loop, and every method is safe for
// concurrent use.
type Replica struct {
	cfg    Config
	m      mem.Mem
	policy *world.Policy
	label  func(off, bit int) string
	logf   func(format string, args ...any)
	now    func() time.Time
	guard  *Guard
	writer *FrameWriter

	sendMu sync.Mutex // held across a flush, taken before mu is released, so sends keep mu's order
	mu     sync.Mutex

	gates   gate.Tracker
	differ  *Differ
	planner *Planner
	last    *obs // the last tick's, nil if its read failed

	// The connection and its session.
	tr       Transport
	hello    *wire.CoHello
	helloKey helloKey
	welcome  *wire.CoWelcome // the answer to hello; nil while waiting
	features hub.Feature
	worldUp  bool // the file's WORLD went up since the session armed
	armedNow bool // the last tick was armed
	presence *wire.Presence
	presAt   time.Time
	partner  *wire.Presence
	bad      int // malformed payloads dropped
	// early is a WORLD that came before the CO_WELCOME naming the
	// session's world, which a joiner learns there (onWorld): kept for
	// the welcome. The hub sends the welcome first (hub.onUnboundHello),
	// so only a reordering transport brings one.
	early *wire.World

	// What outlives a connection.
	sessionWorld bind.WorldID
	fresh        map[bind.Nonce]bool // what welcomes said of nonces as pending fresh binds
	everArmed    bind.WorldID        // the world the file was last armed in: offline queueing
	toldMissing  hub.Feature         // the M4 classes a welcome was last said not to grant (sayUngranted)
	batch        uint32
	queued       world.Image // gains detected while armed offline, joined
	queuedN      int

	// The world as the hub told it.
	view      world.Image
	viewSeq   uint64
	viewWorld bind.WorldID
	haveView  bool
	tomb      world.BitSet
	scopedIn  map[scopeKey][]uint32
	scopedOut int // SCOPED stores refused at maxScopedIn

	// The reconciler.
	room    roomWatch
	toWrite []Write // this tick's writes, made once the lock is released
	window  windows // the deferred writes' windows they are made in (§6.3)

	// The M4 receiver (reconcile_m4.go).
	m4 m4
	// The M5 receiver (reconcile_m5.go).
	m5 m5

	// The escape hatches (escape.go).
	esc     *escapeReq // asked for, waiting for a tick
	escNow  *escapeReq // this tick's, made after the reconciler's writes
	follow  *followWarp
	stopped bool              // Run has returned: no tick comes to make an escape
	entries map[entryKey]int8 // entrances this game came in by, and their rooms
	heard   map[entryKey]int8 // the partner's, from its PRESENCE (inferred)
	track   partnerTrack

	// The plan, and what of it was logged.
	planned    []Write
	held       []HeldBit
	loggedOR   world.BitSet
	loggedMax  world.Image
	loggedHeld world.BitSet
	loggedSc   map[scopeKey][]uint32

	// Tag writes.
	offer        *Offer
	confirmed    *bind.Plan
	declined     map[helloKey]bool // scrubs the user said no to
	bindReq      bool
	bindAsk      *bind.Plan // asked the hub; waiting for its welcome
	bindGranted  bool
	preflight    *PreflightReport
	preflightDue bool
}

// scopeKey names a scoped store: kind, stage, table, room (-1 for DAN).
type scopeKey struct {
	kind  wire.ScopeKind
	stage wire.Stage
	table int8
	room  int8
}

// New returns a replica over cfg.Mem.
func New(cfg Config) (*Replica, error) {
	if cfg.Mem == nil {
		return nil, errors.New("replica: no memory to read")
	}
	r := &Replica{
		cfg:      cfg,
		m:        cfg.Mem,
		policy:   cfg.Policy,
		label:    cfg.Label,
		logf:     cfg.Logf,
		now:      cfg.Now,
		guard:    NewGuard(),
		fresh:    map[bind.Nonce]bool{},
		scopedIn: map[scopeKey][]uint32{},
		loggedSc: map[scopeKey][]uint32{},
		declined: map[helloKey]bool{},
		entries:  map[entryKey]int8{},
		heard:    map[entryKey]int8{},
	}
	if r.policy == nil {
		r.policy = world.DefaultPolicy()
	} else {
		p := *r.policy
		r.policy = &p
	}
	if r.label == nil {
		r.label = DefaultLabel
	}
	if r.logf == nil {
		r.logf = func(string, ...any) {}
	}
	if r.now == nil {
		r.now = time.Now
	}
	w, err := NewFrameWriter(WriterConfig{Mem: r.m, DryRun: true})
	if cfg.Apply {
		wg := NewWriteGuard(GuardConfig{Targets: M3Targets | M4Targets | M5Targets | TargetEscape, Policy: r.policy, Breaker: cfg.Breaker, Logf: r.logf, Label: r.label})
		w, err = NewFrameWriter(WriterConfig{Mem: r.m, Guard: wg, BeatTimeout: cfg.BeatTimeout, Logf: r.logf, Label: r.label})
	}
	if err != nil {
		return nil, err
	}
	r.writer = w
	r.m4 = newM4(cfg.Tables)
	r.m5 = newM5()
	r.differ = NewDiffer(r.policy)
	if cfg.Apply {
		// M4's sender pace (differ_pace.go). A dry run is M2's replica,
		// which sent every edge at once.
		r.differ.Pace(nil)
		// M5's scoped sender (differ_scoped.go): the latching switches of
		// this side's M1 verdict, which is the host's when the hub grants
		// FeaturePuzzle; nil tables propose none.
		r.differ.ShareScopes(hub.ScopeFilter(cfg.Tables.SyncedScopedSwitch))
	}
	r.planner = NewPlanner(r.policy)
	r.planner.writes = !r.writer.DryRun()
	r.sessionWorld = cfg.World
	return r, nil
}

// Guard returns the WriteGuard the replica checks its plan against.
func (r *Replica) Guard() *Guard { return r.guard }

// want is the session features the replica asks for.
func (r *Replica) want() hub.Feature {
	if r.cfg.Want == 0 {
		if r.cfg.Apply {
			return hub.FeatureImage | hub.FeatureSwitches | hub.FeatureLiveSwitches | hub.M4Features | hub.FeaturePuzzle
		}
		return hub.FeatureImage
	}
	return r.cfg.Want & hub.SessionFeatures
}

// Run ticks every TickEvery until ctx ends. Once it returns, an escape
// waiting for a tick, and one asked for until Run is called again, is
// answered ErrStopped.
func (r *Replica) Run(ctx context.Context) {
	r.mu.Lock()
	r.stopped = false
	r.mu.Unlock()
	defer r.stop()
	t := time.NewTicker(TickEvery)
	defer t.Stop()
	for {
		r.tickContext(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// stop is Run's end: the escape waiting for a tick is answered, and a
// warp's second step dropped.
func (r *Replica) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped, r.follow = true, nil
	if q := r.esc; q != nil {
		r.esc = nil
		q.done <- EscapeResult{Kind: q.kind, Err: ErrStopped}
	}
}

// Session is the function network.RunCoop runs for each live connection:
// the replica uses send until the connection ends. Point the client's
// OnCoop at Handle.
func (r *Replica) Session(ctx context.Context, send func(payload []byte) error) error {
	r.Connect(SendFunc(send))
	defer r.Disconnect()
	<-ctx.Done()
	return nil
}

// Connect starts a session on a new connection: the next tick with Link
// in a stage says hello.
func (r *Replica) Connect(t Transport) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resetSession()
	r.tr = t
}

// Disconnect ends the session. A file armed in it goes armed offline:
// its gains are queued until the next session.
func (r *Replica) Disconnect() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.armedNow {
		r.logf("co-op: disconnected: armed offline, queueing gains until the host is back")
	}
	r.resetSession()
}

func (r *Replica) resetSession() {
	r.tr, r.hello, r.welcome = nil, nil, nil
	r.helloKey = helloKey{}
	r.features, r.worldUp, r.armedNow = 0, false, false
	r.presence, r.partner, r.early = nil, nil, nil
	r.track = partnerTrack{}
	r.bindAsk, r.bindGranted = nil, false
	// The next session's hub sends the stores again as the first PRESENCE
	// enters their scopes, and an old one's are no store it holds.
	clear(r.scopedIn)
	clear(r.loggedSc)
}

// RequestBind asks for the loaded file to be bound to the session's
// world (§5.3). The next ticks check that the hub's welcome says the file
// is unbound, run the preflight, plan the bind and offer it
// (Config.OnOffer); after Confirm the hub is asked, and on its yes the
// tag is written. A file the preflight says needs --coop-merge binds
// only if the hub's welcome grants hub.FeatureMerge (founding a world from
// the host's file is such a bind: the empty world is behind it).
func (r *Replica) RequestBind() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bindReq = true
}

// Confirm records the user's yes to an offered plan. The write happens on
// a later tick with the gates open (and, for a bind, the hub's yes).
func (r *Replica) Confirm(p *bind.Plan) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.offer == nil || r.offer.Plan != p {
		return
	}
	p.Confirm()
	r.confirmed, r.offer = p, nil
}

// Decline records the user's no: the plan is dropped, and a scrub is not
// offered again for this file.
func (r *Replica) Decline(p *bind.Plan) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.offer == nil || r.offer.Plan != p {
		return
	}
	if p.Kind != bind.KindBind {
		r.declined[declineKey(p)] = true
	}
	r.offer = nil
	r.logf("co-op: %s declined", p.Kind)
}

// declineKey is the file and tag a declined plan was for.
func declineKey(p *bind.Plan) helloKey {
	return helloKey{file: keyOf(p.File), tag: p.File.Tag, state: p.Verdict.State}
}

// Status returns the replica's state.
func (r *Replica) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := Status{
		World:    r.sessionWorld,
		Seq:      r.viewSeq,
		Features: r.features,
		Planned:  append([]Write(nil), r.planned...),
		Held:     append([]HeldBit(nil), r.held...),
		Queued:   r.queuedN,
		DryRun:   r.writer.DryRun(),
		Writes:   r.writer.Stats(),
		Offer:    r.offer,
		Partner:  r.partner,
		Derive:   r.m4.deriver.Status(),
		Puzzle:   r.puzzleStatus(),
	}
	_, st.WritesOff = r.writer.Disabled()
	if r.preflight != nil {
		pf := *r.preflight
		st.Preflight = &pf
	}
	if r.last != nil {
		st.Verdict = r.classify(r.last)
		st.State, st.Why = r.state(r.last, st.Verdict)
	} else {
		st.State, st.Why = r.state(nil, bind.Verdict{})
	}
	return st
}

// outbox collects what a locked section sends, and the callbacks it runs
// once unlocked.
type outbox struct {
	msgs  []wire.Msg
	calls []func()
}

func (o *outbox) send(m wire.Msg) { o.msgs = append(o.msgs, m) }

// unlockAndFlush releases mu and sends o in order, holding sendMu across
// the handover so concurrent flushes keep the order their locked sections
// ran in, then runs o's callbacks.
func (r *Replica) unlockAndFlush(o *outbox) {
	tr := r.tr
	r.sendMu.Lock()
	r.mu.Unlock()
	for _, m := range o.msgs {
		if tr == nil {
			break
		}
		b, err := wire.Encode(m)
		if err != nil {
			r.logf("co-op: bug: encoding %s: %v", m.Sub(), err)
			continue
		}
		if err := tr.Send(b); err != nil {
			r.logf("co-op: sending %s: %v", m.Sub(), err)
			break
		}
	}
	r.sendMu.Unlock()
	for _, f := range o.calls {
		f()
	}
}

// Tick takes one snapshot and runs one tick: the session's part under the
// replica's lock, then, with Config.Apply, the tick's writes once it is
// released, as they wait for the heartbeat.
func (r *Replica) Tick() Report { return r.tickContext(context.Background()) }

func (r *Replica) tickContext(ctx context.Context) Report {
	s, err := snap.Read(r.m)
	r.mu.Lock()
	var out outbox
	rep := r.tick(s, err, &out)
	ws, window, q := r.toWrite, r.window, r.escNow
	ds := r.m4.take()
	sc := r.m5.take()
	r.toWrite, r.window, r.escNow = nil, windows{}, nil
	r.unlockAndFlush(&out)
	var werr error
	if len(ws) > 0 {
		res, err := r.writer.ApplyContext(ctx, ws)
		werr = err
		r.mu.Lock()
		r.applied(res, err, window, &rep)
		r.mu.Unlock()
	}
	r.applyDerived(ctx, ds, werr, &rep)
	r.applyScoped(ctx, sc, &rep)
	if q != nil {
		// After the plan's writes: an escape changes stage or moves Link,
		// and the plan's bytes were judged against this tick's snapshot.
		res, err := r.writer.ApplyFields(ctx, q.fields)
		r.mu.Lock()
		r.escaped(q, res, err, &rep)
		r.mu.Unlock()
		if q.done != nil {
			q.done <- EscapeResult{Kind: q.kind, Did: q.did, Err: err}
		}
	}
	return rep
}

func (r *Replica) tick(s *snap.Snapshot, err error, out *outbox) Report {
	r.toWrite, r.window, r.escNow = nil, windows{}, nil
	if err != nil {
		st := r.gates.Observe(nil)
		r.writer.Observe(nil, st, false)
		r.differ.Drop()
		r.m4Reset()
		r.m5.leave()
		r.last = nil
		r.armedNow = false
		r.failEscape(err)
		return Report{Err: err, Gates: st, State: StateOff, Why: "the game state could not be read: " + err.Error()}
	}
	st := r.gates.Observe(s)
	o := observe(r.m, s, st)
	r.last = o
	if r.queuedN > 0 && r.differ.Has() && r.differ.file != o.key && o.screen == screenGame {
		r.dropQueue("another file was loaded")
	}

	v := r.classify(o)
	rep := Report{Gates: st, Verdict: v}

	r.tagWrites(o, v, out, &rep)
	if rep.TagWritten {
		v = r.classify(o)
		rep.Verdict = v
	}
	r.sayHello(o, v, out, &rep)

	armed := r.armed(o, v)
	rep.Verify = r.writer.observe(s, st, armed, o.file, o.boss)
	r.room.observe(o)
	if armed && !r.armedNow {
		if r.writer.DryRun() {
			r.logf("co-op: armed in world %s (dry run: planned writes are logged, not made)", r.sessionWorld)
		} else {
			r.logf("co-op: armed in world %s: written to this game: %s", r.sessionWorld, writtenClasses(r.features))
		}
		r.everArmed = r.sessionWorld
	}
	r.armedNow = armed
	r.sendPresence(o, armed, out, &rep)

	if armed && !r.worldUp && o.consistent() {
		m := r.worldMsg(o)
		// What the pace holds back stays out of the WORLD too, to follow
		// as gains in its time. An edge of this very tick is not yet the
		// pace's (the differ observes below), so it goes in the WORLD: a
		// story bit then reaches the world unbatched, which the receiver's
		// own gates (story bits on its stage transitions, the inventory
		// when IDLE) make harmless.
		r.differ.Withhold((*world.Image)(&m.Image))
		out.send(m)
		r.worldUp, rep.World = true, true
		if r.queuedN > 0 {
			if gs := r.news(world.Diff(&world.Image{}, &r.queued)); len(gs) > 0 {
				out.send(r.gainsMsg(gs))
				rep.Gains += len(gs)
			}
			r.dropQueue("")
		}
	}

	e := r.differ.observe(o)
	r.planner.observe(o)
	rep.Edges = e
	if e.Rebased && !rep.World {
		// What rose while the differ had no baseline (a save state, a
		// failed read, a trip off the game) is in the new baseline, so no
		// GAINS carries it: the next consistent armed tick sends the
		// whole WORLD instead. The hub's merge only adds, and news()
		// still drops what the view has.
		r.worldUp = false
	}
	if gs := r.news(e.Gains); len(gs) > 0 {
		switch {
		case armed && r.worldUp:
			out.send(r.gainsMsg(gs))
			rep.Gains += len(gs)
		case r.offlineBound(v):
			r.queued.Apply(gs)
			r.queuedN = len(world.Diff(&world.Image{}, &r.queued))
			rep.Queued = len(gs)
		}
	}
	if armed && r.worldUp {
		for i := range e.Scoped {
			sc := &e.Scoped[i]
			if r.features&scopeFeature(sc.Kind) != 0 {
				out.send(sc)
				rep.Scoped++
			}
		}
	}

	r.pruneScoped(o)
	r.plan(o, armed)
	rep.Planned, rep.Held = len(r.planned), len(r.held)
	r.m5Observe(o) // the rooms' clocks, which M3's memory switches wait on too
	r.reconcile(o, armed, &rep)
	r.reconcileM4(o, armed, &rep)
	r.reconcileM5(o, armed)
	r.escapeTick(o, armed)
	r.runPreflight(o, v, out)
	rep.State, rep.Why = r.state(o, v)
	return rep
}

// scopeFeature is the feature a SCOPED kind needs (as the hub has it).
func scopeFeature(k wire.ScopeKind) hub.Feature {
	if k == wire.ScopeKill {
		return hub.FeatureCombat
	}
	return hub.FeaturePuzzle
}

// classify applies V1-V6 to o's file against the session's world and
// what welcomes said of pending fresh binds.
func (r *Replica) classify(o *obs) bind.Verdict {
	return bind.Classify(o.file, bind.Expect{World: r.sessionWorld, Fresh: r.isFresh})
}

func (r *Replica) isFresh(n bind.Nonce) bool {
	return r.fresh[n]
}

// armed reports whether the session runs for o's file: a welcome that
// says so, the image granted, and the file bound to the welcome's world.
func (r *Replica) armed(o *obs, v bind.Verdict) bool {
	return r.tr != nil && r.welcome != nil && r.welcome.Status == wire.StatusOK &&
		r.features&hub.FeatureImage != 0 && v.State == bind.Bound && v.WorldChecked &&
		o.screen != screenOut
}

// offlineBound reports whether gains of a file in verdict v are queued:
// it is bound to the world it was last armed in, and no session runs.
func (r *Replica) offlineBound(v bind.Verdict) bool {
	return !r.armedNow && !r.everArmed.IsZero() && v.State == bind.Bound && v.WorldChecked &&
		v.Tag.World == r.everArmed
}

func (r *Replica) dropQueue(why string) {
	if why != "" && r.queuedN > 0 {
		r.logf("co-op: dropped %d queued gain bytes: %s", r.queuedN, why)
	}
	r.queued, r.queuedN = world.Image{}, 0
}

// state says where the session is, and why it is not armed.
func (r *Replica) state(o *obs, v bind.Verdict) (State, string) {
	switch {
	case o == nil:
		return StateOff, "the game state could not be read"
	case r.tr == nil:
		if r.offlineBound(v) {
			return StateOffline, "disconnected: gains are queued until the host is back"
		}
		return StateOff, "not connected"
	case r.welcome == nil:
		if o.screen != screenGame {
			return StateHello, "waiting for a file to be loaded in game"
		}
		return StateHello, "waiting for the host's welcome"
	case r.welcome.Status == wire.StatusRefused:
		return StateRefused, fmt.Sprintf("the host refused co-op (%s)", r.welcome.Reason)
	case r.armedNow:
		return StateArmed, ""
	case r.welcome.Status == wire.StatusUnbound:
		return StateUnbound, "the file is not bound to world " + r.sessionWorld.String() + ": " + v.Why
	case r.features&hub.FeatureImage == 0:
		return StateUnbound, "the host turned image sync off"
	}
	return StateUnbound, v.Why
}

// --- CO_HELLO --------------------------------------------------------------

// sayHello sends CO_HELLO when the session has none for the file in game:
// on a new connection, and whenever the file, its tag or a bind ask
// changes.
func (r *Replica) sayHello(o *obs, v bind.Verdict, out *outbox, rep *Report) {
	if r.tr == nil || o.screen != screenGame {
		return
	}
	k := helloKey{file: o.key, tag: o.file.Tag, state: v.State}
	if r.bindAsk != nil {
		// Kept while the granted tag waits for the gates, so no hello
		// goes out between the hub's yes and the write.
		k.askBind = r.bindAsk.Tag.Nonce
	}
	ask := r.bindAsk != nil && !r.bindGranted
	if r.hello != nil && k == r.helloKey {
		return
	}
	if o.file.Slot >= wire.CardSlots {
		return // mDataNum garbage: no file to describe
	}
	h := &wire.CoHello{
		Schema:       wire.Schema,
		ClassHash:    wire.ClassHash(),
		StageTblHash: r.cfg.StageTblHash,
		Slot:         o.file.Slot,
		ClearCount:   o.file.ClearCount,
		NameHash:     bind.NameHash(o.file.Name[:]),
		FileState:    wire.FileState(v.State),
		Want:         uint32(r.want()),
	}
	if v.HasTag {
		h.WorldID, h.BindNonce = wire.ID(v.Tag.World), wire.ID(v.Tag.Nonce)
	}
	if o.file.DateIPL == 0 {
		h.Want |= uint32(hub.AskUnsaved)
	}
	if ask {
		// The file's WORLD first: the hub checks the bind against it.
		out.send(r.candidateMsg(o))
		h.WorldID, h.BindNonce = wire.ID(r.sessionWorld), wire.ID(r.bindAsk.Tag.Nonce)
		h.Want |= uint32(hub.AskBind)
	}
	out.send(h)
	r.hello, r.helloKey, r.welcome = h, k, nil
	r.worldUp = false
	rep.Hello = true
}

// --- inbound ----------------------------------------------------------------

// Handle takes one 'O' payload from the hub (network.Client.OnCoop).
func (r *Replica) Handle(payload []byte) {
	m, err := wire.Decode(payload, wire.Down)
	r.mu.Lock()
	var out outbox
	if r.tr == nil {
		r.mu.Unlock()
		return
	}
	if err != nil {
		r.bad++
		if r.bad == 1 || r.bad%100 == 0 {
			r.logf("co-op: dropped malformed message %d from the host: %v", r.bad, err)
		}
	} else {
		r.handle(m, &out)
	}
	r.unlockAndFlush(&out)
}

func (r *Replica) handle(m wire.Msg, out *outbox) {
	switch m := m.(type) {
	case *wire.CoWelcome:
		r.onWelcome(m)
	case *wire.World:
		r.onWorld(m)
	case *wire.GainsDown:
		if r.viewOf(r.sessionWorld) {
			gs := make([]world.Gain, len(m.Gains))
			for i, g := range m.Gains {
				gs[i] = world.Gain(g)
			}
			r.view.Apply(gs)
			r.viewSeq = max(r.viewSeq, m.Seq)
		}
	case *wire.Scoped:
		r.onScoped(m)
	case *wire.Presence:
		p := *m
		r.notePartnerRest(&p)
		r.learnPartner(&p)
		r.partner = &p
	case *wire.Notice:
		r.logf("co-op: the host says (%s): %s", m.Code, m.Text)
	case *wire.Ctrl:
		r.onCtrl(m, out)
	}
}

func (r *Replica) onWelcome(m *wire.CoWelcome) {
	if r.hello == nil {
		return // nothing asked
	}
	w := bind.WorldID(m.WorldID)
	if !r.cfg.World.IsZero() && w != r.cfg.World && m.Status != wire.StatusRefused {
		r.logf("co-op: the host serves world %s, not this session's %s: co-op is off for this connection", w, r.cfg.World)
		m = &wire.CoWelcome{Status: wire.StatusRefused, Reason: wire.NoticeWorldMismatch, WorldID: m.WorldID}
	}
	r.welcome = m
	r.features = hub.Feature(m.Features)
	r.sayUngranted(m)
	if m.Status != wire.StatusRefused && !w.IsZero() {
		if r.sessionWorld != w && !r.sessionWorld.IsZero() {
			r.logf("co-op: the host now serves world %s (was %s)", w, r.sessionWorld)
		}
		r.sessionWorld = w
	}
	if e := r.early; e != nil {
		r.early = nil
		if m.Status != wire.StatusRefused && bind.WorldID(e.WorldID) == r.sessionWorld {
			r.onWorld(e)
		}
	}
	if n := bind.Nonce(r.hello.BindNonce); m.Status != wire.StatusRefused && !n.IsZero() && bind.WorldID(r.hello.WorldID) == w {
		// A refusal (Hub.refuse) says nothing of fresh binds: its
		// FreshBind is always false, and taking it would make a real
		// pending fresh bind's V5-only failure look like a leak.
		r.fresh[n] = m.FreshBind
	}
	if r.bindAsk != nil && hub.Feature(r.hello.Want)&hub.AskBind != 0 {
		if r.features&hub.FeatureBind != 0 && m.Status == wire.StatusUnbound {
			r.bindGranted = true
			if r.bindAsk.Fresh {
				r.fresh[r.bindAsk.Tag.Nonce] = m.FreshBind
			}
		} else {
			r.logf("co-op: the host did not accept the bind (%s); nothing was written", m.Reason)
			r.bindAsk, r.confirmed = nil, nil
		}
	}
	switch m.Status {
	case wire.StatusOK:
		r.logf("co-op: welcome to world %s (seq %d; %s)", w, m.Seq, r.features)
	case wire.StatusUnbound:
		switch {
		case r.bindAsk != nil:
		case m.FreshBind && r.hello.FileState == wire.FileLeaked && r.last != nil && r.classify(r.last).State == bind.Bound:
			// The leak was V5 failing before the replica knew the nonce
			// (r.fresh starts empty in each process). With r.fresh set
			// above, the file is bound, and the next tick says hello
			// again, bound.
			r.logf("co-op: the host has nonce %s as a pending fresh bind: the file is bound", bind.Nonce(r.hello.BindNonce))
		default:
			r.logf("co-op: the host serves world %s; this file is not bound to it (%s)", w, m.Reason)
		}
	case wire.StatusRefused:
		r.logf("co-op: the host refused co-op for this session (%s); pose sync is unaffected", m.Reason)
	}
}

// viewOf reports whether the view is of world w, resetting it if it was
// of another.
func (r *Replica) viewOf(w bind.WorldID) bool {
	if w.IsZero() {
		return false
	}
	if r.viewWorld != w {
		r.view, r.viewSeq, r.viewWorld, r.haveView = world.Image{}, 0, w, false
		r.tomb = world.BitSet{}
		r.scopedIn, r.loggedSc = map[scopeKey][]uint32{}, map[scopeKey][]uint32{}
	}
	return true
}

func (r *Replica) onWorld(m *wire.World) {
	w := bind.WorldID(m.WorldID)
	if r.sessionWorld.IsZero() && r.welcome == nil && !w.IsZero() {
		c := *m
		r.early = &c // the welcome says whether it is the session's
		return
	}
	if w != r.sessionWorld || !r.viewOf(w) {
		r.logf("co-op: ignored a WORLD of world %s, not this session's %s", w, r.sessionWorld)
		return
	}
	img := world.Image(m.Image)
	img = world.Shared(&img)
	// GAINS relayed before the first WORLD are in the view (and viewSeq)
	// already: a WORLD older than them, the hub's answer to this
	// replica's WORLD delivered after them, is joined like any late one.
	if m.Seq >= r.viewSeq {
		r.view = img // the hub's image at its seq is the world
	} else {
		r.view = world.Join(&r.view, &img) // a late one takes nothing back
	}
	r.viewSeq = max(r.viewSeq, m.Seq)
	r.haveView = true
	r.preflightDue = true
}

// onScoped keeps a scoped store the hub sent, or, for a room edge, hands
// it to the M5 receiver for the room Link is in (m5Edge).
// The hub sends them only to a bound peer granted the kind's feature
// (hub.onScoped, sendEntries), so anything else is dropped: a host can't
// make a replica it never welcomed as bound keep stores. The stores kept
// are bounded by pruneScoped and maxScopedIn.
func (r *Replica) onScoped(m *wire.Scoped) {
	if r.welcome == nil || r.welcome.Status != wire.StatusOK || r.features&scopeFeature(m.Kind) == 0 ||
		!r.viewOf(r.sessionWorld) {
		return
	}
	k := scopeKey{kind: m.Kind, stage: m.Stage, table: m.Table, room: m.Room}
	if m.Kind == wire.ScopeRoomEdge {
		// Relayed live, never kept (§6.3): only the room it is for, at
		// this moment, could apply it.
		r.m5Edge(m)
		return
	}
	if k.kind == wire.ScopeDan {
		k.room = -1
	}
	ws := r.scopedIn[k]
	if ws == nil {
		if len(r.scopedIn) >= maxScopedIn {
			r.scopedOut++
			if r.scopedOut == 1 || r.scopedOut%1000 == 0 {
				r.logf("co-op: the host sent more scoped stores than one stage has (%d refused); keeping %d", r.scopedOut, maxScopedIn)
			}
			return
		}
		n := 2 // DAN: two u32 words; ZONE: mSwitch[0..1]
		if k.kind == wire.ScopeKill {
			n = killWords
		}
		ws = make([]uint32, n)
		r.scopedIn[k] = ws
	}
	for _, w := range m.Words {
		if int(w.Word) < len(ws) {
			ws[w.Word] |= w.Bits
		}
	}
}

func (r *Replica) onCtrl(m *wire.Ctrl, out *outbox) {
	switch m.Op {
	case wire.CtrlResync:
		// The hub asks for a fresh WORLD: the next consistent tick sends
		// it.
		r.worldUp = false
	case wire.CtrlCoopOff:
		f := hub.Feature(m.Arg)
		if f != r.features {
			r.logf("co-op: the host's features for this session are now %s", f)
		}
		r.features = f
	case wire.CtrlTombstone:
		off, mask, _ := m.Tombstone()
		if r.viewOf(r.sessionWorld) {
			r.tomb.Add(int(off), mask)
			r.view[off] &^= mask
		}
		r.logf("co-op: the host blocked %s: it is no longer shared", r.label(int(off), -1))
	}
}

// --- outbound ---------------------------------------------------------------

// worldMsg is the file's WORLD: its shared bits (and the DERIVED bytes the
// hub's preflight reads) within the policy, without tombstoned bits.
func (r *Replica) worldMsg(o *obs) *wire.World {
	img := wire.Image(o.raw)
	wire.StripLocal(&img)
	for off := range img {
		if world.SharedMask(off) == 0 {
			continue
		}
		v := img[off] & r.policy[off]
		if class.Of(off).Class == class.Max && (img[off]&^r.policy[off] != 0 || v&r.tomb[off] != 0) {
			v = 0
		}
		img[off] = v &^ r.tomb[off]
	}
	m := &wire.World{WorldID: wire.ID(r.sessionWorld), Image: img}
	if r.viewWorld == r.sessionWorld {
		m.Seq = r.viewSeq
	}
	return m
}

// candidateMsg is an unbound file's WORLD for a bind ask.
func (r *Replica) candidateMsg(o *obs) *wire.World { return r.worldMsg(o) }

func (r *Replica) gainsMsg(gs []world.Gain) *wire.GainsUp {
	r.batch++
	m := &wire.GainsUp{Batch: r.batch, Gains: make([]wire.Gain, len(gs))}
	for i, g := range gs {
		m.Gains[i] = wire.Gain(g)
	}
	return m
}

// news drops from gs what the world already has (the view) and what the
// host tombstoned. gs is reused.
func (r *Replica) news(gs []world.Gain) []world.Gain {
	out := gs[:0]
	have := r.haveView && r.viewWorld == r.sessionWorld
	for _, g := range gs {
		off := int(g.Off)
		if class.Of(off).Class == class.Max {
			if g.Val&r.tomb[off] != 0 || have && g.Val <= r.view[off] {
				continue
			}
		} else {
			g.Val &^= r.tomb[off]
			if have {
				g.Val &^= r.view[off]
			}
			if g.Val == 0 {
				continue
			}
		}
		out = append(out, g)
	}
	return out
}

// sendPresence sends PRESENCE when it changed, and every PresenceEvery.
func (r *Replica) sendPresence(o *obs, armed bool, out *outbox, rep *Report) {
	if r.tr == nil || r.welcome == nil || r.welcome.Status == wire.StatusRefused {
		return
	}
	p := presenceOf(o, armed)
	now := r.now()
	if r.presence != nil && *r.presence == *p && now.Sub(r.presAt) < PresenceEvery {
		return
	}
	out.send(p)
	r.presence, r.presAt = p, now
	rep.Presence = true
}

// presenceOf is where o says Link is.
func presenceOf(o *obs, armed bool) *wire.Presence {
	s := o.s
	p := &wire.Presence{Table: -1, Room: -1, Layer: -1, Point: s.StagePoint}
	if st, err := wire.MakeStage(s.StageName); err == nil {
		p.Stage = st
	}
	if addr.ValidTable(int(s.Table)) {
		p.Table = s.Table
	}
	if s.StayNo >= 0 && int(s.StayNo) < addr.RoomCount {
		p.Room = s.StayNo
	}
	if s.StageLayer >= -1 && s.StageLayer <= wire.MaxLayer {
		p.Layer = s.StageLayer
	}
	if o.screen == screenGame && !p.Stage.IsEmpty() {
		p.Flags |= wire.PresenceInGame
	}
	if s.EvtMode != 0 || s.EvtOrderCount != 0 {
		p.Flags |= wire.PresenceInEvent
	}
	if s.NextEnable != 0 {
		p.Flags |= wire.PresenceTransitioning
	}
	if s.MenuPause != 0 || !o.st.Heartbeat {
		p.Flags |= wire.PresencePaused
	}
	if armed {
		p.Flags |= wire.PresenceArmed
	}
	return p
}

// --- the planner ---------------------------------------------------------------

// plan runs the planner on a consistent armed tick and, in a dry run,
// logs what is new (the reconciler logs the rest).
func (r *Replica) plan(o *obs, armed bool) {
	if !armed || !r.haveView || r.viewWorld != r.sessionWorld {
		r.planned, r.held = nil, nil
		r.loggedOR, r.loggedMax, r.loggedHeld = world.BitSet{}, world.Image{}, world.BitSet{}
		return
	}
	if !o.consistent() {
		return // keep the last plan: this tick's image can't be trusted
	}
	view := r.view
	if !r.writer.DryRun() {
		view = r.desired()
	}
	ws, held := r.planner.Plan(&view, &o.raw, &o.local, int(o.s.Table))
	for _, w := range ws {
		for _, a := range w.Addrs() {
			if !r.guard.Allows(a, 1) || r.guard.Mask(a)&w.Bits != w.Bits && !w.Max {
				r.logf("co-op: bug: planned write of %s at 0x%08X is outside the WriteGuard; planning stopped", r.label(int(w.Off), -1), a)
				r.planned, r.held = nil, nil
				return
			}
		}
	}
	r.planned, r.held = ws, held
	if r.writer.DryRun() {
		r.logPlan(ws, held)
	}
	r.planScoped(o)
}

// desired is the view without what the host tombstoned (CTRL TOMBSTONE
// took the bits out of the view already; a GAINS relayed after it must not
// bring them back): a MAX value with a tombstoned bit is dropped whole, as
// the world refuses it.
func (r *Replica) desired() world.Image {
	v := r.view
	for off, t := range r.tomb {
		switch {
		case t == 0:
		case class.Of(off).Class == class.Max && v[off]&t != 0:
			v[off] = 0
		default:
			v[off] &^= t
		}
	}
	return v
}

// logPlan logs the planned writes and holds not logged before, and forgets
// those no longer planned, so each is logged once while it stays planned.
func (r *Replica) logPlan(ws []Write, held []HeldBit) {
	var lines []string
	var nowOR, nowHeld world.BitSet
	var nowMax world.Image
	// Holds first: they are few, and say why the plan is short of the
	// world.
	for _, h := range held {
		nowHeld[h.Off] |= h.Mask
		if fresh := h.Mask &^ r.loggedHeld[h.Off]; fresh != 0 {
			lines = append(lines, fmt.Sprintf("dry-run: holding %s back: %s", r.label(int(h.Off), bits.TrailingZeros8(fresh)), h.Hold.Why))
		}
	}
	for _, w := range ws {
		off := int(w.Off)
		live := ""
		if w.Live != 0 {
			live = fmt.Sprintf(", and its live copy 0x%08X", w.Live)
		}
		if w.Max {
			nowMax[off] = w.Bits
			if r.loggedMax[off] != w.Bits {
				lines = append(lines, fmt.Sprintf("dry-run: would raise %s to %d (0x%08X%s; %s)", r.label(off, -1), w.Bits, w.Saved, live, w.When))
			}
			continue
		}
		nowOR[off] = w.Bits
		for b := range 8 {
			m := byte(1) << b
			if w.Bits&m != 0 && r.loggedOR[off]&m == 0 {
				lines = append(lines, fmt.Sprintf("dry-run: would set %s (0x%08X |= 0x%02X%s; %s)", r.label(off, b), w.Saved, m, live, w.When))
			}
		}
	}
	r.loggedOR, r.loggedMax, r.loggedHeld = nowOR, nowMax, nowHeld
	if len(lines) == 0 {
		return
	}
	r.logf("co-op: dry-run: %d byte writes planned, %d holds; nothing is written in this version", len(ws), len(held))
	for i, l := range lines {
		if i == maxPlanLines {
			r.logf("co-op: dry-run: ... and %d more", len(lines)-maxPlanLines)
			break
		}
		r.logf("co-op: %s", l)
	}
}

// pruneScoped drops the stores of a scope o does not show Link in, and
// what was logged of them, so the receiver keeps a store no longer than
// the hub sends it one (world.Scopes): a stage or table other than Link's,
// a ZONE store of another room than mStayNo, and off the game every DAN
// and ZONE store. The PRESENCE this very tick sends says the same place
// (presenceOf), and the hub sends a scope's store again when a PRESENCE
// enters it (hub.onPresence, sendEntries), if anyone is still there; a
// relay that crosses the PRESENCE goes on the next tick. It runs on every
// tick, armed or not, so what a host sends can't pile up.
func (r *Replica) pruneScoped(o *obs) {
	in := o.screen == screenGame
	stage, _ := wire.MakeStage(o.s.StageName) // an invalid name matches no store
	for _, m := range []map[scopeKey][]uint32{r.scopedIn, r.loggedSc} {
		for k := range m {
			switch {
			case k.kind == wire.ScopeDan || k.kind == wire.ScopeZone:
				if !in || k.stage != stage || k.table != o.s.Table || k.kind == wire.ScopeZone && k.room != o.s.StayNo {
					delete(m, k)
				}
			case in && (k.stage != stage || k.table != o.s.Table):
				delete(m, k)
			}
		}
	}
}

// planScoped logs the kill bits the hub sent that the receiver's scopes
// lack, with §7.2's apply rule (M6). The dan and zone stores are the M5
// receiver's (reconcileM5). pruneScoped has dropped this tick the stores
// of a stage or table the receiver left.
func (r *Replica) planScoped(o *obs) {
	s := o.s
	keys := make([]scopeKey, 0, len(r.scopedIn))
	for k := range r.scopedIn {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b scopeKey) int {
		return cmp.Or(cmp.Compare(a.kind, b.kind), cmp.Compare(a.room, b.room),
			bytes.Compare(a.stage[:], b.stage[:]), cmp.Compare(a.table, b.table))
	})
	for _, k := range keys {
		ws := r.scopedIn[k]
		var local []uint32
		var when string
		switch k.kind {
		case wire.ScopeKill:
			z, ok := s.ZoneFor(int(k.room))
			if !ok {
				continue
			}
			kw := killWordsOf(s, z)
			local, when = kw[:], "into the room's zone, and light-shrink a live enemy, M6"
		default:
			continue
		}
		logged := r.loggedSc[k]
		if logged == nil {
			logged = make([]uint32, len(ws))
			r.loggedSc[k] = logged
		}
		for i := range ws {
			miss := ws[i] &^ local[i]
			for b := range 32 {
				if m := uint32(1) << b; miss&m != 0 && logged[i]&m == 0 {
					where := ""
					if k.room >= 0 {
						where = fmt.Sprintf(" in room %d", k.room)
					}
					r.logf("co-op: dry-run: would set %s%s (%s)", scopedLabel(k.kind, i, b), where, when)
				}
			}
			logged[i] = miss
		}
	}
}

// --- tag writes -------------------------------------------------------------

// tagWrites carries out a confirmed plan whose time has come, and makes
// the offers: a scrub for a tag that has leaked, a bind RequestBind asked
// for.
func (r *Replica) tagWrites(o *obs, v bind.Verdict, out *outbox, rep *Report) {
	if r.offer != nil && r.offer.Plan.File != o.file && o.screen == screenGame {
		r.logf("co-op: the loaded file changed: the %s offer is withdrawn", r.offer.Plan.Kind)
		r.offer = nil
	}
	if p := r.confirmed; p != nil {
		if p.File != o.file && o.screen == screenGame {
			r.logf("co-op: the loaded file changed: the confirmed %s is dropped; nothing was written", p.Kind)
			r.confirmed, r.bindAsk, r.bindGranted = nil, nil, false
		} else if p.Kind == bind.KindBind {
			r.carryBind(o, p, rep)
		} else {
			r.carryScrub(o, p, rep)
		}
	}
	if r.offer == nil && r.confirmed == nil && !rep.TagWritten {
		r.makeOffer(o, v, out) // a tick that wrote the tag offers on the next, with its new verdict
	}
}

func (r *Replica) carryScrub(o *obs, p *bind.Plan, rep *Report) {
	err := bind.Scrub(r.m, p, o.s, o.st)
	switch {
	case errors.Is(err, bind.ErrGate):
		return // a later tick
	case err != nil:
		r.logf("co-op: scrubbing the tag: %v", err)
	default:
		r.logf("co-op: zeroed the leaked co-op tag (0x%X bytes at 0x%08X)", bind.TagSize, addr.Reserve)
		rep.TagWritten = true
		r.rereadTag(o)
	}
	r.confirmed = nil
}

// carryBind asks the hub (the next hello carries the ask) and, once it
// says yes, writes the tag.
func (r *Replica) carryBind(o *obs, p *bind.Plan, rep *Report) {
	switch {
	case r.bindAsk == nil && r.tr != nil && r.welcome != nil && r.welcome.Status == wire.StatusUnbound:
		if !o.consistent() {
			return // the WORLD the ask carries must be trustworthy
		}
		r.bindAsk = p // sayHello sends the ask this tick
		return
	case r.bindAsk == nil:
		if r.tr == nil {
			r.logf("co-op: the bind needs the host; it is dropped, nothing was written")
			r.confirmed = nil
		}
		return
	case !r.bindGranted:
		return // waiting for the welcome
	}
	if _, err := bind.Bind(r.m, p, o.s, o.st); err != nil {
		if errors.Is(err, bind.ErrGate) {
			return
		}
		r.logf("co-op: binding the file: %v", err)
	} else {
		r.logf("co-op: bound the file to world %s (nonce %s): save in-game so the card keeps it", p.Tag.World, p.Tag.Nonce)
		rep.TagWritten = true
		r.rereadTag(o)
	}
	r.confirmed, r.bindAsk, r.bindGranted = nil, nil, false
}

// rereadTag puts the tag just written into o, so this tick's hello and
// verdict see it.
func (r *Replica) rereadTag(o *obs) {
	if f, err := bind.ReadFile(r.m); err == nil && keyOf(f) == o.key {
		o.file.Tag = f.Tag
	}
}

func (r *Replica) makeOffer(o *obs, v bind.Verdict, out *outbox) {
	if o.screen != screenGame || !o.st.CanWriteIdle() {
		return
	}
	switch {
	case r.bindReq:
		r.offerBind(o, v, out)
	case v.State == bind.Leaked && r.definiteLeak(v):
		if r.declined[helloKey{file: o.key, tag: o.file.Tag, state: v.State}] {
			return
		}
		p, err := bind.PlanScrub(o.s, o.st, bind.Expect{World: r.sessionWorld, Fresh: r.isFresh})
		if err != nil {
			return
		}
		r.offer = &Offer{Plan: p}
		r.logf("co-op: the loaded file carries a leaked co-op tag (%s); asking to scrub it", v.Why)
		r.queueOffer(out)
	}
}

// definiteLeak reports whether a Leaked verdict can be acted on: a rule
// other than V5 failed, or V5 failed for a nonce a welcome said is no
// pending fresh bind (a joiner learns that only from the hub).
func (r *Replica) definiteLeak(v bind.Verdict) bool {
	for _, rule := range v.Failed() {
		if rule != bind.V5 {
			return true
		}
	}
	known, ok := r.fresh[v.Tag.Nonce]
	return ok && !known
}

func (r *Replica) offerBind(o *obs, v bind.Verdict, out *outbox) {
	refuse := func(format string, a ...any) {
		r.logf("co-op: can't bind: "+format, a...)
		r.bindReq = false
	}
	switch {
	case r.tr == nil || r.welcome == nil:
		return // wait for a session
	case r.welcome.Status == wire.StatusRefused:
		refuse("the host refused co-op for this session")
		return
	case r.welcome.Status == wire.StatusOK || v.State == bind.Bound:
		refuse("the file is already bound to world %s", r.sessionWorld)
		return
	case !r.haveView || r.viewWorld != r.sessionWorld:
		return // the hub's WORLD follows its welcome
	case !o.consistent():
		return
	}
	pf := Preflight(&o.raw, &r.view, PreflightOptions{Policy: r.policy, Label: r.label})
	switch {
	case pf.Verdict == PreflightRefused:
		refuse("%s", pf.Why)
		return
	case pf.NeedsMerge && r.features&hub.FeatureMerge == 0:
		refuse("%s", pf.Why)
		return
	}
	p, err := bind.PlanBind(o.s, o.st, bind.BindRequest{World: r.sessionWorld, WorldSeq: r.viewSeq, Fresh: r.isFresh})
	if err != nil {
		if !errors.Is(err, bind.ErrGate) {
			refuse("%v", err)
		}
		return
	}
	r.bindReq = false
	r.offer = &Offer{Plan: p, Preflight: &pf}
	r.queueOffer(out)
}

func (r *Replica) queueOffer(out *outbox) {
	if f := r.cfg.OnOffer; f != nil {
		o := *r.offer
		out.calls = append(out.calls, func() { f(o) })
	}
}

// runPreflight recomputes an unbound file's preflight after a WORLD.
func (r *Replica) runPreflight(o *obs, v bind.Verdict, out *outbox) {
	if r.armedNow || v.State == bind.Bound {
		r.preflight = nil
		return
	}
	if !r.preflightDue || !r.haveView || !o.consistent() {
		return
	}
	r.preflightDue = false
	pf := Preflight(&o.raw, &r.view, PreflightOptions{Policy: r.policy, Label: r.label})
	r.preflight = &pf
	r.logf("co-op: %s", pf.Summary())
	if f := r.cfg.OnPreflight; f != nil {
		out.calls = append(out.calls, func() { f(pf) })
	}
}
