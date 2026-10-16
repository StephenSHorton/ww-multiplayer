package hub

import (
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/internal/network"
)

var (
	testWorld  = world.ID{0xC0, 0x0B, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	otherWorld = world.ID{0xBA, 0xD0, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
)

// testTables stands for the host's M1 tables identity.
const testTables uint64 = 0x5747_4D31_7461_626C

// Image bytes the tests merge: two OR bytes, a MAX byte, and a memory
// switch byte (OR_MASKED, which the default policy keeps out).
var (
	offOR     = uint16(addr.OffGetItem + 3)                                    // mGetItem[3], OR 0xFF ([0..2] share bit 0 only)
	offOR2    = uint16(addr.OffTact)                                           // mTact, OR 0xFF
	offMax    = uint16(addr.OffWalletSize)                                     // mWalletSize, MAX 0xFF
	offSwitch = uint16(addr.OffMemBit(3) + 4)                                  // mMemory[3].mSwitch, OR_MASKED
	offTbox   = func(t, i int) uint16 { return uint16(addr.OffMemBit(t) + i) } // mMemory[t].mTbox, OR
)

// --- the fake transport ------------------------------------------------------

// fakeConn is a co-op connection that records what the hub sends it.
type fakeConn struct {
	id   byte
	name string
	addr net.Addr

	block chan struct{} // non-nil: Send waits for it to close (a peer that stopped reading)

	mu     sync.Mutex
	got    [][]byte
	closed bool
}

func newConn(id byte, name string) *fakeConn { return &fakeConn{id: id, name: name} }

// hostConn is a connection from the host machine (loopback).
func hostConn(id byte, name string) *fakeConn {
	c := newConn(id, name)
	c.addr = &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 50000 + int(id)}
	return c
}

func (c *fakeConn) ID() byte             { return c.id }
func (c *fakeConn) Name() string         { return c.name }
func (c *fakeConn) RemoteAddr() net.Addr { return c.addr }

func (c *fakeConn) Send(b []byte) error {
	if c.block != nil {
		<-c.block
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return network.ErrCoopClosed
	}
	c.got = append(c.got, b)
	return nil
}

func (c *fakeConn) Close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
}

func (c *fakeConn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *fakeConn) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.got)
}

// take decodes and forgets what the connection received so far.
func (c *fakeConn) take(t *testing.T) []wire.Msg {
	t.Helper()
	c.mu.Lock()
	got := c.got
	c.got = nil
	c.mu.Unlock()
	ms := make([]wire.Msg, len(got))
	for i, b := range got {
		m, err := wire.Decode(b, wire.Down)
		if err != nil {
			t.Fatalf("%s got an undecodable message %x: %v", c.name, b, err)
		}
		ms[i] = m
	}
	return ms
}

// --- the rig -----------------------------------------------------------------

type logLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *logLines) logf(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func (l *logLines) has(sub string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range l.lines {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

type rig struct {
	t     *testing.T
	root  string
	store *world.Store
	h     *Hub
	log   *logLines
}

// fullCfg allows every default feature, every class-mask bit and every
// scoped switch the game takes.
func fullCfg() Config {
	return Config{Features: DefaultFeatures, Policy: world.FullPolicy(), SyncedScoped: syncAll, SyncedKill: syncAll}
}

// syncAll is an M1 verdict on the scoped switches that grants them all.
func syncAll(int, string, int, int) bool { return true }

// newRig starts a hub over a new world in a temporary directory, sending
// directly (no writers), so a test reads what the hub sent after sync.
func newRig(t *testing.T, cfg Config) *rig {
	return newRigMode(t, cfg, true)
}

func newRigMode(t *testing.T, cfg Config, direct bool) *rig {
	t.Helper()
	if cfg.StageTblHash == 0 {
		cfg.StageTblHash = testTables
	}
	return startRig(t, cfg, direct)
}

// startRig is newRigMode with cfg as given: StageTblHash 0 is a host
// without stage tables.
func startRig(t *testing.T, cfg Config, direct bool) *rig {
	t.Helper()
	root := t.TempDir()
	st, err := world.Create(root, world.New(testWorld, time.Unix(1_800_000_000, 0)), world.Options{})
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, root: root, store: st, log: &logLines{}}
	cfg.Store = st
	cfg.Logf = r.log.logf
	h, err := newHub(cfg, direct)
	if err != nil {
		t.Fatal(err)
	}
	go h.run()
	r.h = h
	t.Cleanup(func() { h.Stop() })
	return r
}

// sync waits until the hub has handled everything queued so far.
func (r *rig) sync() {
	r.t.Helper()
	if err := r.h.do(func() {}); err != nil {
		r.t.Fatalf("hub barrier: %v", err)
	}
}

func (r *rig) attach(c *fakeConn) {
	r.t.Helper()
	if err := r.h.attach(c); err != nil {
		r.t.Fatalf("attach %s: %v", c.name, err)
	}
}

// send hands the hub one message from c and waits for it to be handled.
func (r *rig) send(c *fakeConn, m wire.Msg) {
	r.t.Helper()
	r.sendRaw(c, mustEncode(r.t, m))
}

func (r *rig) sendRaw(c *fakeConn, b []byte) {
	r.t.Helper()
	r.h.frame(c, b)
	r.sync()
}

// exchange sends m from c and returns what c got back.
func (r *rig) exchange(c *fakeConn, m wire.Msg) []wire.Msg {
	r.t.Helper()
	r.send(c, m)
	return c.take(r.t)
}

// join attaches a bound peer and reads its welcome.
func (r *rig) join(c *fakeConn) *fakeConn {
	r.t.Helper()
	r.attach(c)
	w := welcomeOf(r.t, r.exchange(c, hello(wire.FileBound, testWorld, c.id)))
	if w.Status != wire.StatusOK {
		r.t.Fatalf("%s: welcome %+v, want OK", c.name, w)
	}
	return c
}

func (r *rig) status() Status {
	r.t.Helper()
	st, err := r.h.Status()
	if err != nil {
		r.t.Fatal(err)
	}
	return st
}

func (r *rig) peer(id byte) PeerStatus {
	r.t.Helper()
	for _, p := range r.status().Peers {
		if p.ID == id {
			return p
		}
	}
	r.t.Fatalf("no peer %d", id)
	return PeerStatus{}
}

func mustEncode(t *testing.T, m wire.Msg) []byte {
	t.Helper()
	b, err := wire.Encode(m)
	if err != nil {
		t.Fatalf("encoding %s: %v", m.Sub(), err)
	}
	return b
}

// hello is a CO_HELLO of file state fs for world w, with nonce n, wanting
// the default features.
func hello(fs wire.FileState, w world.ID, n byte) *wire.CoHello {
	m := &wire.CoHello{
		Schema: wire.Schema, ClassHash: wire.ClassHash(), StageTblHash: testTables,
		FileState: fs, NameHash: 0x1234_5678, Want: uint32(DefaultFeatures),
	}
	if fs != wire.FileUnbound {
		m.WorldID, m.BindNonce = wire.ID(w), wire.ID{0xAA, n}
	}
	return m
}

func img(gs ...world.Gain) wire.Image {
	var im world.Image
	for _, g := range gs {
		im[g.Off] |= g.Val
	}
	return wire.Image(im)
}

func worldMsg(w world.ID, seq uint64, gs ...world.Gain) *wire.World {
	return &wire.World{WorldID: wire.ID(w), Seq: seq, Image: img(gs...)}
}

func gains(batch uint32, gs ...wire.Gain) *wire.GainsUp {
	return &wire.GainsUp{Batch: batch, Gains: gs}
}

func presence(stage string, table, room int8) *wire.Presence {
	st, err := wire.MakeStage(stage)
	if err != nil {
		panic(err)
	}
	return &wire.Presence{Stage: st, Table: table, Room: room, Layer: -1, Flags: wire.PresenceInGame}
}

func scoped(kind wire.ScopeKind, stage string, table, room int8, ws ...wire.ScopedWord) *wire.Scoped {
	st, err := wire.MakeStage(stage)
	if err != nil {
		panic(err)
	}
	return &wire.Scoped{Kind: kind, Stage: st, Table: table, Room: room, Words: ws}
}

// pick returns the messages of type M in ms.
func pick[M wire.Msg](ms []wire.Msg) []M {
	var out []M
	for _, m := range ms {
		if x, ok := m.(M); ok {
			out = append(out, x)
		}
	}
	return out
}

func welcomeOf(t *testing.T, ms []wire.Msg) *wire.CoWelcome {
	t.Helper()
	ws := pick[*wire.CoWelcome](ms)
	if len(ws) != 1 {
		t.Fatalf("got %d CO_WELCOMEs in %s, want 1", len(ws), subs(ms))
	}
	return ws[0]
}

func subs(ms []wire.Msg) string {
	parts := make([]string, len(ms))
	for i, m := range ms {
		parts[i] = m.Sub().String()
		if n, ok := m.(*wire.Notice); ok {
			parts[i] += "(" + n.Code.String() + ")"
		}
		if c, ok := m.(*wire.Ctrl); ok {
			parts[i] += "(" + c.Op.String() + ")"
		}
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func codes(ms []wire.Msg) []wire.NoticeCode {
	var cs []wire.NoticeCode
	for _, n := range pick[*wire.Notice](ms) {
		cs = append(cs, n.Code)
	}
	return cs
}

// --- tests -------------------------------------------------------------------

// TestHelloClassification: every kind of CO_HELLO gets the welcome §4.4
// and §5.2-5.3 call for.
func TestHelloClassification(t *testing.T) {
	schema2 := mustEncode(t, hello(wire.FileBound, testWorld, 1))
	schema2[1], schema2[2] = 0, 2

	cases := []struct {
		name     string
		hello    func() *wire.CoHello
		raw      []byte
		status   wire.Status
		reason   wire.NoticeCode
		features Feature
		notice   []wire.NoticeCode
		world    bool // a WORLD follows (unbound: the preflight's)
		state    PeerState
	}{
		{name: "bound", hello: func() *wire.CoHello { return hello(wire.FileBound, testWorld, 1) },
			status: wire.StatusOK, features: DefaultFeatures, state: StateBound},
		{name: "bound, wanting less", hello: func() *wire.CoHello {
			m := hello(wire.FileBound, testWorld, 1)
			m.Want = uint32(FeatureImage | FeatureLiveSwitches)
			return m
		}, status: wire.StatusOK, features: FeatureImage, state: StateBound},
		{name: "unbound", hello: func() *wire.CoHello { return hello(wire.FileUnbound, world.ID{}, 0) },
			status: wire.StatusUnbound, reason: wire.NoticeNotBound, features: DefaultFeatures,
			notice: []wire.NoticeCode{wire.NoticeNotBound}, world: true, state: StateUnbound},
		{name: "leaked", hello: func() *wire.CoHello { return hello(wire.FileLeaked, otherWorld, 1) },
			status: wire.StatusUnbound, reason: wire.NoticeNotBound, features: DefaultFeatures,
			notice: []wire.NoticeCode{wire.NoticeNotBound}, world: true, state: StateUnbound},
		{name: "foreign", hello: func() *wire.CoHello { return hello(wire.FileForeign, otherWorld, 1) },
			status: wire.StatusRefused, reason: wire.NoticeWorldMismatch,
			notice: []wire.NoticeCode{wire.NoticeWorldMismatch}, state: StateRefused},
		{name: "bound to another world", hello: func() *wire.CoHello { return hello(wire.FileBound, otherWorld, 1) },
			status: wire.StatusRefused, reason: wire.NoticeWorldMismatch,
			notice: []wire.NoticeCode{wire.NoticeWorldMismatch}, state: StateRefused},
		{name: "another class table", hello: func() *wire.CoHello {
			m := hello(wire.FileBound, testWorld, 1)
			m.ClassHash ^= 1
			return m
		}, status: wire.StatusRefused, reason: wire.NoticeSchema, notice: []wire.NoticeCode{wire.NoticeSchema}, state: StateRefused},
		{name: "other stage tables", hello: func() *wire.CoHello {
			m := hello(wire.FileBound, testWorld, 1)
			m.StageTblHash ^= 1
			return m
		}, status: wire.StatusOK, features: DefaultFeatures &^ TableFeatures, notice: []wire.NoticeCode{wire.NoticeSchema}, state: StateBound},
		{name: "no stage tables", hello: func() *wire.CoHello {
			m := hello(wire.FileBound, testWorld, 1)
			m.StageTblHash = 0
			return m
		}, status: wire.StatusOK, features: DefaultFeatures &^ TableFeatures, notice: []wire.NoticeCode{wire.NoticeSchema}, state: StateBound},
		{name: "no stage tables, unbound", hello: func() *wire.CoHello {
			m := hello(wire.FileUnbound, world.ID{}, 0)
			m.StageTblHash = 0
			return m
		}, status: wire.StatusUnbound, reason: wire.NoticeNotBound, features: DefaultFeatures &^ TableFeatures,
			notice: []wire.NoticeCode{wire.NoticeSchema, wire.NoticeNotBound}, world: true, state: StateUnbound},
		{name: "second quest", hello: func() *wire.CoHello {
			m := hello(wire.FileUnbound, world.ID{}, 0)
			m.ClearCount = 1
			return m
		}, status: wire.StatusRefused, reason: wire.NoticeClearCount, notice: []wire.NoticeCode{wire.NoticeClearCount}, state: StateRefused},
		{name: "another schema", raw: schema2,
			status: wire.StatusRefused, reason: wire.NoticeSchema, notice: []wire.NoticeCode{wire.NoticeSchema}, state: StateRefused},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, fullCfg())
			c := newConn(1, "joiner")
			r.attach(c)
			raw := tc.raw
			if raw == nil {
				raw = mustEncode(t, tc.hello())
			}
			r.sendRaw(c, raw)
			ms := c.take(t)
			w := welcomeOf(t, ms)
			if w.Status != tc.status || w.Reason != tc.reason || Feature(w.Features) != tc.features {
				t.Errorf("welcome status %s reason %s features %s, want %s %s %s",
					w.Status, w.Reason, Feature(w.Features), tc.status, tc.reason, tc.features)
			}
			if world.ID(w.WorldID) != testWorld || w.Seq != r.store.Seq() || w.FreshBind {
				t.Errorf("welcome world %s seq %d fresh %v, want %s %d false", w.WorldID, w.Seq, w.FreshBind, testWorld, r.store.Seq())
			}
			if got := codes(ms); !slices.Equal(got, tc.notice) {
				t.Errorf("notices %v, want %v (got %s)", got, tc.notice, subs(ms))
			}
			if got := len(pick[*wire.World](ms)) == 1; got != tc.world {
				t.Errorf("WORLD sent: %v, want %v (got %s)", got, tc.world, subs(ms))
			}
			if st := r.peer(1).State; st != tc.state {
				t.Errorf("state %s, want %s", st, tc.state)
			}
		})
	}
}

// TestFramesBeforeHello: nothing counts before CO_HELLO, a malformed frame
// is refused, and the NOTICEs about it are rate-limited.
func TestFramesBeforeHello(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cfg := fullCfg()
	cfg.Now = func() time.Time { return now }
	r := newRig(t, cfg)
	c := newConn(1, "early")
	r.attach(c)

	ms := r.exchange(c, gains(1, wire.Gain{Off: offOR, Val: 1}))
	if got := codes(ms); !slices.Equal(got, []wire.NoticeCode{wire.NoticeRejected}) {
		t.Fatalf("GAINS before hello: %s, want one NOTICE REJECTED", subs(ms))
	}
	// The same complaint again within complainEvery: silent.
	r.send(c, worldMsg(testWorld, 0, world.Gain{Off: offOR, Val: 1}))
	r.send(c, presence("sea", 0, 1))
	r.sendRaw(c, []byte{0x04, 0xFF})
	if ms := c.take(t); len(ms) != 0 {
		t.Fatalf("got %s within the rate limit, want nothing", subs(ms))
	}
	now = now.Add(complainEvery)
	r.sendRaw(c, []byte{0x04, 0xFF})
	ms = c.take(t)
	if got := codes(ms); !slices.Equal(got, []wire.NoticeCode{wire.NoticeRejected}) {
		t.Fatalf("malformed frame after the limit: %s, want NOTICE REJECTED", subs(ms))
	}
	if r.store.Seq() != 0 {
		t.Fatalf("world seq %d, want 0: something merged before a hello", r.store.Seq())
	}

	// A refused peer's frames are dropped without a word.
	m := hello(wire.FileBound, otherWorld, 1)
	r.send(c, m)
	c.take(t)
	r.send(c, gains(1, wire.Gain{Off: offOR, Val: 1}))
	r.send(c, worldMsg(otherWorld, 0, world.Gain{Off: offOR, Val: 1}))
	r.send(c, &wire.Ctrl{Op: wire.CtrlResync})
	if ms := c.take(t); len(ms) != 0 || r.store.Seq() != 0 {
		t.Fatalf("refused peer got %s and seq %d, want nothing", subs(ms), r.store.Seq())
	}
}

// TestMergeAndRelay: a bound peer's WORLD is merged and answered with the
// merged image, its effective delta goes to the others as GAINS with the
// origin and seq; GAINS go to every bound peer; repeats and no-ops relay
// nothing.
func TestMergeAndRelay(t *testing.T) {
	r := newRig(t, fullCfg())
	a := r.join(newConn(1, "alice"))
	b := r.join(newConn(2, "bob"))

	ms := r.exchange(a, worldMsg(testWorld, 0, world.Gain{Off: offOR, Val: 0x01}, world.Gain{Off: offMax, Val: 2}))
	ws := pick[*wire.World](ms)
	if len(ws) != 1 || len(ms) != 1 {
		t.Fatalf("alice got %s, want one WORLD", subs(ms))
	}
	if ws[0].Seq != 1 || ws[0].Image[offOR] != 0x01 || ws[0].Image[offMax] != 2 || world.ID(ws[0].WorldID) != testWorld {
		t.Fatalf("WORLD reply seq %d, bytes %02x %02x", ws[0].Seq, ws[0].Image[offOR], ws[0].Image[offMax])
	}
	bm := b.take(t)
	gd := pick[*wire.GainsDown](bm)
	want := []wire.Gain{{Off: offMax, Val: 2}, {Off: offOR, Val: 0x01}}
	if len(gd) != 1 || len(bm) != 1 || gd[0].Seq != 1 || gd[0].Origin != 1 || !slices.Equal(gd[0].Gains, want) {
		t.Fatalf("bob got %s %+v, want GAINS seq 1 origin 1 %v", subs(bm), gd, want)
	}

	// Bob's GAINS: the effective delta (bit 0x02) goes to both.
	r.send(b, gains(7, wire.Gain{Off: offOR, Val: 0x03}))
	for _, c := range []*fakeConn{a, b} {
		gd := pick[*wire.GainsDown](c.take(t))
		if len(gd) != 1 || gd[0].Seq != 2 || gd[0].Origin != 2 || !slices.Equal(gd[0].Gains, []wire.Gain{{Off: offOR, Val: 0x02}}) {
			t.Fatalf("%s got %+v, want GAINS seq 2 origin 2 [0x%03X:02]", c.name, gd, offOR)
		}
	}

	// A repeated batch is dropped, an older one too; a lower MAX value or
	// bits the world has change nothing and relay nothing.
	r.send(b, gains(7, wire.Gain{Off: offOR2, Val: 0x01}))
	r.send(b, gains(6, wire.Gain{Off: offOR2, Val: 0x01}))
	r.send(b, gains(8, wire.Gain{Off: offMax, Val: 1}))
	r.send(b, gains(9, wire.Gain{Off: offOR, Val: 0x01}))
	if ma, mb := a.take(t), b.take(t); len(ma)+len(mb) != 0 {
		t.Fatalf("no-op proposals relayed %s / %s", subs(ma), subs(mb))
	}
	if r.store.Seq() != 2 {
		t.Fatalf("seq %d after no-ops, want 2", r.store.Seq())
	}
	if p := r.peer(2); p.Duplicates != 2 {
		t.Errorf("bob's duplicates %d, want 2", p.Duplicates)
	}
	got := r.store.Image()
	if got[offOR] != 0x03 || got[offMax] != 2 || got[offOR2] != 0 {
		t.Errorf("world bytes %02x %02x %02x, want 03 02 00", got[offOR], got[offMax], got[offOR2])
	}
	// Batches count up in serial-number order, through the wrap.
	r.send(b, gains(0x8000_0000, wire.Gain{Off: offOR2, Val: 0x01}))
	r.send(b, gains(0xFFFF_FFFF, wire.Gain{Off: offOR2, Val: 0x02}))
	r.send(b, gains(0, wire.Gain{Off: offOR2, Val: 0x04}))
	if got := r.store.Image()[offOR2]; got != 0x07 {
		t.Errorf("mTact %02x after batches 0x80000000, 0xFFFFFFFF and 0, want 07", got)
	}
}

// TestPolicyRefusalsAreQuiet: under the default policy a memory switch is
// refused and counted, the rest of the proposal merges, and the replica
// is not told (it can't know the host's policy).
func TestPolicyRefusalsAreQuiet(t *testing.T) {
	r := newRig(t, Config{Features: DefaultFeatures})
	a := r.join(newConn(1, "alice"))
	ms := r.exchange(a, gains(1, wire.Gain{Off: offOR, Val: 1}, wire.Gain{Off: offSwitch, Val: 0x80}))
	gd := pick[*wire.GainsDown](ms)
	if len(ms) != 1 || len(gd) != 1 || !slices.Equal(gd[0].Gains, []wire.Gain{{Off: offOR, Val: 1}}) {
		t.Fatalf("got %s %+v, want one GAINS of mGetItem only", subs(ms), gd)
	}
	if got := r.store.Image()[offSwitch]; got != 0 {
		t.Errorf("memory switch byte %02x merged under the default policy", got)
	}
	if p := r.peer(1); p.Rejected != 1 {
		t.Errorf("rejected %d, want 1", p.Rejected)
	}
}

// TestUnboundPeer: an unbound file sees the world and the others' places,
// but nothing it proposes is merged and it gets no relays.
func TestUnboundPeer(t *testing.T) {
	cfg := fullCfg()
	cfg.Now = func() time.Time { return time.Unix(1_800_000_000, 0) }
	r := newRig(t, cfg)
	a := r.join(newConn(1, "alice"))
	u := newConn(2, "unbound")
	r.attach(u)
	r.send(a, presence("sea", 0, 11))
	a.take(t)
	ms := r.exchange(u, hello(wire.FileUnbound, world.ID{}, 0))
	if ps := pick[*wire.Presence](ms); len(ps) != 1 || ps[0].Room != 11 {
		t.Fatalf("newcomer got %s, want alice's PRESENCE", subs(ms))
	}

	if got := codes(r.exchange(u, gains(1, wire.Gain{Off: offOR, Val: 1}))); !slices.Equal(got, []wire.NoticeCode{wire.NoticeNotBound}) {
		t.Fatalf("unbound GAINS: notices %v, want NOT_BOUND", got)
	}
	r.send(u, presence("sea", 0, 11))
	if ps := pick[*wire.Presence](a.take(t)); len(ps) != 1 {
		t.Fatalf("alice got no relay of the unbound peer's PRESENCE")
	}
	r.send(u, scoped(wire.ScopeKill, "sea", 0, 11, wire.ScopedWord{Word: 0, Bits: 1}))
	if got := codes(u.take(t)); len(got) != 0 {
		t.Errorf("second refusal within the rate limit: %v", got) // NOT_BOUND was just sent
	}

	// A subset WORLD: answered with the world, no DIVERGENT.
	ms = r.exchange(u, worldMsg(world.ID{}, 0))
	if len(pick[*wire.World](ms)) != 1 || len(codes(ms)) != 0 {
		t.Fatalf("subset WORLD: got %s, want just the world", subs(ms))
	}
	// A divergent one: DIVERGENT, nothing merged.
	ms = r.exchange(u, worldMsg(world.ID{}, 0, world.Gain{Off: offOR, Val: 0x40}))
	if got := codes(ms); !slices.Equal(got, []wire.NoticeCode{wire.NoticeDivergent}) {
		t.Fatalf("divergent WORLD: notices %v, want DIVERGENT", got)
	}
	if n := pick[*wire.Notice](ms)[0]; !strings.Contains(n.Text, "--coop-merge") || !strings.Contains(n.Text, "mGetItem") {
		t.Errorf("DIVERGENT text %q names neither the field nor --coop-merge", n.Text)
	}
	// Another world's WORLD is refused.
	if got := codes(r.exchange(u, worldMsg(otherWorld, 0))); !slices.Equal(got, []wire.NoticeCode{wire.NoticeWorldMismatch}) {
		t.Fatalf("other world's WORLD: notices %v, want WORLD_MISMATCH", got)
	}
	if r.store.Seq() != 0 {
		t.Fatalf("seq %d: an unbound peer's proposal was merged", r.store.Seq())
	}

	// Alice's gains are not relayed to the unbound peer; a RESYNC gets
	// it the world.
	r.send(a, gains(1, wire.Gain{Off: offOR2, Val: 1}))
	if ms := u.take(t); len(ms) != 0 {
		t.Fatalf("unbound peer got %s", subs(ms))
	}
	ws := pick[*wire.World](r.exchange(u, &wire.Ctrl{Op: wire.CtrlResync}))
	if len(ws) != 1 || ws[0].Image[offOR2] != 1 || ws[0].Seq != 1 {
		t.Fatalf("RESYNC answer %+v", ws)
	}
}

// TestWorldMismatchFromBoundPeer: a WORLD naming another world is refused
// and merges nothing.
func TestWorldMismatchFromBoundPeer(t *testing.T) {
	r := newRig(t, fullCfg())
	a := r.join(newConn(1, "alice"))
	if got := codes(r.exchange(a, worldMsg(otherWorld, 0, world.Gain{Off: offOR, Val: 1}))); !slices.Equal(got, []wire.NoticeCode{wire.NoticeWorldMismatch}) {
		t.Fatalf("notices %v, want WORLD_MISMATCH", got)
	}
	if r.store.Seq() != 0 {
		t.Fatal("merged a WORLD of another world")
	}
}

// TestReconnectWithSeq: a replica that drops and comes back is welcomed
// with the current seq; its WORLD brings its offline progress in, it gets
// back everything it missed, and the partner gets only what is new.
func TestReconnectWithSeq(t *testing.T) {
	r := newRig(t, fullCfg())
	a := r.join(newConn(1, "alice"))
	b := r.join(newConn(2, "bob"))
	r.send(a, gains(1, wire.Gain{Off: offOR, Val: 0x01}))
	a.take(t)
	b.take(t)

	r.h.detach(a)
	r.send(b, gains(1, wire.Gain{Off: offOR, Val: 0x10}))
	b.take(t)
	if a.count() != 0 {
		t.Fatal("a detached peer got a relay")
	}

	a2 := newConn(1, "alice") // the same co-op ID, a new connection
	r.attach(a2)
	w := welcomeOf(t, r.exchange(a2, hello(wire.FileBound, testWorld, 1)))
	if w.Status != wire.StatusOK || w.Seq != 2 {
		t.Fatalf("rewelcome %+v, want OK at seq 2", w)
	}
	// Offline she set mTact; her WORLD still says seq 1.
	ws := pick[*wire.World](r.exchange(a2, worldMsg(testWorld, 1, world.Gain{Off: offOR, Val: 0x01}, world.Gain{Off: offOR2, Val: 0x04})))
	if len(ws) != 1 || ws[0].Seq != 3 || ws[0].Image[offOR] != 0x11 || ws[0].Image[offOR2] != 0x04 {
		t.Fatalf("WORLD reply %+v, want seq 3 with bob's bit and her offline one", ws)
	}
	gd := pick[*wire.GainsDown](b.take(t))
	if len(gd) != 1 || gd[0].Seq != 3 || gd[0].Origin != 1 || !slices.Equal(gd[0].Gains, []wire.Gain{{Off: offOR2, Val: 0x04}}) {
		t.Fatalf("bob got %+v, want only alice's offline gain", gd)
	}

	// A replica that saw a later seq than the world file holds (commits
	// lost in a host crash) heals the world, and the host says so.
	r.exchange(a2, worldMsg(testWorld, 99, world.Gain{Off: offOR, Val: 0x80}))
	if !r.log.has("commits were lost") {
		t.Error("no log line about lost commits")
	}
}

// TestTombstones: only the host blocks bits; a blocked bit leaves the
// world, every peer is told, and later proposals of it are refused.
func TestTombstones(t *testing.T) {
	r := newRig(t, fullCfg())
	a := r.join(newConn(1, "alice"))
	hc := r.join(hostConn(2, "host"))
	u := newConn(3, "unbound")
	r.attach(u)
	r.send(u, hello(wire.FileUnbound, world.ID{}, 0))
	r.send(a, gains(1, wire.Gain{Off: offOR, Val: 0x03}))
	for _, c := range []*fakeConn{a, hc, u} {
		c.take(t)
	}

	ms := r.exchange(a, &wire.Ctrl{Op: wire.CtrlTombstone, Arg: wire.TombstoneArg(offOR, 0x01)})
	if got := codes(ms); !slices.Equal(got, []wire.NoticeCode{wire.NoticeRejected}) || r.store.Image()[offOR] != 0x03 {
		t.Fatalf("a joiner's TOMBSTONE: %s, world byte %02x", subs(ms), r.store.Image()[offOR])
	}

	r.send(hc, &wire.Ctrl{Op: wire.CtrlTombstone, Arg: wire.TombstoneArg(offOR, 0x01)})
	if got := r.store.Image()[offOR]; got != 0x02 {
		t.Fatalf("world byte %02x after the tombstone, want 02", got)
	}
	for _, c := range []*fakeConn{a, hc, u} {
		ms := c.take(t)
		cs := pick[*wire.Ctrl](ms)
		if len(cs) != 1 || cs[0].Op != wire.CtrlTombstone || cs[0].Arg != wire.TombstoneArg(offOR, 0x01) ||
			!slices.Equal(codes(ms), []wire.NoticeCode{wire.NoticeTombstone}) {
			t.Fatalf("%s got %s, want CTRL TOMBSTONE and NOTICE TOMBSTONE", c.name, subs(ms))
		}
	}

	// Proposing it again: refused, the proposer told, nothing relayed.
	ms = r.exchange(a, gains(2, wire.Gain{Off: offOR, Val: 0x01}))
	if got := codes(ms); !slices.Equal(got, []wire.NoticeCode{wire.NoticeTombstone}) || len(pick[*wire.GainsDown](ms)) != 0 {
		t.Fatalf("tombstoned proposal: %s", subs(ms))
	}
	if hc.count() != 0 {
		t.Fatal("a tombstoned proposal was relayed")
	}
	r.send(a, worldMsg(testWorld, 0, world.Gain{Off: offOR, Val: 0x01}))
	if got := r.store.Image()[offOR]; got != 0x02 {
		t.Fatalf("a WORLD brought the tombstoned bit back: %02x", got)
	}
	a.take(t)

	// The host's own API, and a repeat that changes nothing.
	if err := r.h.Tombstone(int(offOR2), 0x80); err != nil {
		t.Fatal(err)
	}
	if cs := pick[*wire.Ctrl](a.take(t)); len(cs) != 1 {
		t.Fatalf("Hub.Tombstone: alice got %d CTRLs", len(cs))
	}
	if err := r.h.Tombstone(int(offOR2), 0x80); err != nil {
		t.Fatal(err)
	}
	if a.count() != 0 {
		t.Fatal("a repeated tombstone was announced again")
	}
	if err := r.h.Tombstone(addr.OffReserve, 0x01); err == nil {
		t.Fatal("tombstoned a TAG byte")
	}

	// A newcomer learns every tombstone with its welcome.
	c := newConn(4, "carol")
	r.attach(c)
	cs := pick[*wire.Ctrl](r.exchange(c, hello(wire.FileBound, testWorld, 4)))
	want := []uint32{wire.TombstoneArg(offOR, 0x01), wire.TombstoneArg(offOR2, 0x80)}
	var got []uint32
	for _, m := range cs {
		got = append(got, m.Arg)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("newcomer's tombstones %x, want %x", got, want)
	}
}

// TestScopedStores: dan, zone and kill words relay to the bound peers in
// their scope, stores live while someone is there and are dropped when the
// last occupant leaves, a newcomer gets what it newly reaches, and
// room-edge words are relayed but never kept.
func TestScopedStores(t *testing.T) {
	const stage = "M_NewD2" // Dragon Roost Cavern, save table 3
	r := newRig(t, fullCfg())
	a := r.join(newConn(1, "alice"))
	b := r.join(newConn(2, "bob"))
	r.send(a, presence(stage, 3, 1))
	r.send(b, presence(stage, 3, 1))
	if ps := pick[*wire.Presence](a.take(t)); len(ps) != 1 || ps[0].Room != 1 {
		t.Fatalf("alice got %+v, want bob's PRESENCE", ps)
	}
	b.take(t)

	zone := scoped(wire.ScopeZone, stage, 3, 1, wire.ScopedWord{Word: 0, Bits: 0x0001})
	r.send(a, zone)
	if a.count() != 0 {
		t.Fatal("the origin got its own SCOPED back")
	}
	got := pick[*wire.Scoped](b.take(t))
	if len(got) != 1 || got[0].Kind != wire.ScopeZone || got[0].Room != 1 || !slices.Equal(got[0].Words, zone.Words) {
		t.Fatalf("bob got %+v, want the zone word", got)
	}
	r.send(a, zone)
	if b.count() != 0 {
		t.Fatal("a repeated zone word was relayed")
	}

	r.send(a, scoped(wire.ScopeKill, stage, 3, 1, wire.ScopedWord{Word: 3, Bits: 0x10}))
	r.send(a, scoped(wire.ScopeDan, stage, 3, 5, wire.ScopedWord{Word: 1, Bits: 0x4}))
	r.send(a, scoped(wire.ScopeRoomEdge, stage, 3, 1, wire.ScopedWord{Word: 2, Bits: 0x8}))
	got = pick[*wire.Scoped](b.take(t))
	if len(got) != 3 || got[0].Kind != wire.ScopeKill || got[1].Kind != wire.ScopeDan || got[1].Room != -1 || got[2].Kind != wire.ScopeRoomEdge {
		t.Fatalf("bob got %+v, want KILL, DAN (room -1), ROOMEDGE", got)
	}
	wantKeys := []world.Key{
		{Kind: world.ScopeDan, Stage: stage, Table: 3, Room: -1},
		{Kind: world.ScopeZone, Stage: stage, Table: 3, Room: 1},
		{Kind: world.ScopeKill, Stage: stage, Table: 3, Room: 1},
	}
	if keys := r.status().Scopes; !slices.Equal(keys, wantKeys) {
		t.Fatalf("stores %v, want %v (ROOMEDGE never kept)", keys, wantKeys)
	}

	// Reporting for a room she is not in: refused.
	ms := r.exchange(a, scoped(wire.ScopeZone, stage, 3, 2, wire.ScopedWord{Word: 0, Bits: 1}))
	if got := codes(ms); !slices.Equal(got, []wire.NoticeCode{wire.NoticeRejected}) {
		t.Fatalf("zone word for another room: %s, want REJECTED", subs(ms))
	}

	// Bob leaves room 1: the zone store lives on, alice is there. Then
	// alice leaves too: it is dropped, and coming back brings nothing.
	r.send(b, presence(stage, 3, 2))
	r.send(a, presence(stage, 3, 2))
	r.send(a, presence(stage, 3, 1))
	a.take(t)
	b.take(t)
	wantKeys = []world.Key{wantKeys[0], wantKeys[2]}
	if keys := r.status().Scopes; !slices.Equal(keys, wantKeys) {
		t.Fatalf("stores %v after room 1 emptied, want %v", keys, wantKeys)
	}

	// A newcomer entering the stage gets the dan and kill stores.
	c := r.join(newConn(3, "carol"))
	c.take(t)
	r.send(c, presence(stage, 3, 2))
	got = pick[*wire.Scoped](c.take(t))
	if len(got) != 2 || got[0].Kind != wire.ScopeDan || got[1].Kind != wire.ScopeKill || got[1].Room != 1 {
		t.Fatalf("newcomer got %+v, want DAN then KILL room 1", got)
	}

	// Everyone leaves the stage: every store is dropped.
	r.send(a, presence("sea", 0, 11))
	r.send(b, presence("sea", 0, 11))
	r.h.detach(c)
	r.sync()
	if keys := r.status().Scopes; len(keys) != 0 {
		t.Fatalf("stores %v after everyone left, want none", keys)
	}
	if !r.log.has("dropped the KILL") {
		t.Error("no log line for the dropped kill store")
	}
}

// TestScopedFeatures: a peer without puzzle sync neither sends nor gets
// zone words; one without combat gets no kills.
func TestScopedFeatures(t *testing.T) {
	r := newRig(t, fullCfg())
	a := r.join(newConn(1, "alice"))
	b := newConn(2, "bob")
	r.attach(b)
	m := hello(wire.FileBound, testWorld, 2)
	m.Want = uint32(FeatureImage | FeatureCombat)
	r.send(b, m)
	r.send(a, presence("M_NewD2", 3, 1))
	r.send(b, presence("M_NewD2", 3, 1))
	a.take(t)
	b.take(t)

	if got := codes(r.exchange(b, scoped(wire.ScopeZone, "M_NewD2", 3, 1, wire.ScopedWord{Word: 0, Bits: 1}))); !slices.Equal(got, []wire.NoticeCode{wire.NoticeRejected}) {
		t.Fatalf("zone word without puzzle sync: %v", got)
	}
	r.send(a, scoped(wire.ScopeZone, "M_NewD2", 3, 1, wire.ScopedWord{Word: 0, Bits: 1}))
	r.send(a, scoped(wire.ScopeKill, "M_NewD2", 3, 1, wire.ScopedWord{Word: 0, Bits: 1}))
	got := pick[*wire.Scoped](b.take(t))
	if len(got) != 1 || got[0].Kind != wire.ScopeKill {
		t.Fatalf("bob got %+v, want only the kill", got)
	}
}

// TestScopedStageFlood: a bound peer naming stage after stage, with a DAN
// and a ZONE store in each, holds only the stores of where it is: each
// goes as it leaves (world.Scopes), with nothing refused, and the honest
// peers' relays go on.
func TestScopedStageFlood(t *testing.T) {
	r := newRig(t, fullCfg())
	a := r.join(newConn(1, "alice"))
	b := r.join(newConn(2, "bob"))
	m := r.join(newConn(3, "mallory"))
	r.send(a, presence("M_NewD2", 3, 1))
	r.send(b, presence("M_NewD2", 3, 1))
	for i := range 300 {
		st := fmt.Sprintf("Z%06d", i)
		r.send(m, presence(st, 3, 0))
		r.send(m, scoped(wire.ScopeDan, st, 3, -1, wire.ScopedWord{Word: 0, Bits: 1}))
		r.send(m, scoped(wire.ScopeZone, st, 3, 0, wire.ScopedWord{Word: 0, Bits: 1}))
		if n := len(r.status().Scopes); n != 2 {
			t.Fatalf("%d scoped stores held in mallory's stage %d, want her DAN and ZONE", n, i)
		}
	}
	if got := codes(m.take(t)); len(got) != 0 {
		t.Fatalf("mallory got NOTICEs %v for the stores of where she is", got)
	}
	a.take(t)
	b.take(t)
	r.send(a, scoped(wire.ScopeZone, "M_NewD2", 3, 1, wire.ScopedWord{Word: 0, Bits: 1}))
	if got := pick[*wire.Scoped](b.take(t)); len(got) != 1 || got[0].Kind != wire.ScopeZone {
		t.Fatalf("bob got %+v after the flood, want alice's zone word", got)
	}
	for _, c := range []*fakeConn{m, a, b} {
		r.send(c, presence("sea", 0, 11))
	}
	if n := len(r.status().Scopes); n != 0 {
		t.Fatalf("%d scoped stores held after everyone left, want none", n)
	}
}

// TestDetachRelaysNowhere: when a peer goes, the others are told it is
// nowhere.
func TestDetachRelaysNowhere(t *testing.T) {
	r := newRig(t, fullCfg())
	a := r.join(newConn(1, "alice"))
	b := r.join(newConn(2, "bob"))
	r.send(a, presence("sea", 0, 11))
	b.take(t)
	r.h.detach(a)
	r.sync()
	ps := pick[*wire.Presence](b.take(t))
	if len(ps) != 1 || !ps[0].Stage.IsEmpty() || ps[0].Flags != 0 || ps[0].Table != -1 {
		t.Fatalf("bob got %+v, want one PRESENCE of nowhere", ps)
	}
	if st := r.status(); len(st.Peers) != 1 {
		t.Fatalf("%d peers after the detach, want 1", len(st.Peers))
	}
}

// TestBindFlow: an unbound file sends its WORLD, asks to bind with a fresh
// nonce, is told to write the tag with the nonce recorded on disk first,
// and then joins bound; once its file is loaded from the card the fresh
// bind is cleared.
func TestBindFlow(t *testing.T) {
	r := newRig(t, fullCfg())
	u := newConn(1, "joiner")
	r.attach(u)
	r.send(u, hello(wire.FileUnbound, world.ID{}, 0))
	u.take(t)

	nonce := world.ID{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	ask := hello(wire.FileUnbound, world.ID{}, 0)
	ask.WorldID, ask.BindNonce = wire.ID(testWorld), wire.ID(nonce)
	ask.Want |= uint32(AskBind | AskUnsaved)

	// Before its WORLD: refused.
	w := welcomeOf(t, r.exchange(u, ask))
	if w.Status != wire.StatusUnbound || w.Reason != wire.NoticeRejected || Feature(w.Features)&FeatureBind != 0 {
		t.Fatalf("bind before WORLD: %+v", w)
	}

	r.send(u, worldMsg(world.ID{}, 0))
	u.take(t)
	ms := r.exchange(u, ask)
	w = welcomeOf(t, ms)
	if w.Status != wire.StatusUnbound || w.Reason != 0 || Feature(w.Features)&FeatureBind == 0 || !w.FreshBind {
		t.Fatalf("bind: %+v (%s), want Unbound with FeatureBind and FreshBind", w, subs(ms))
	}
	if !r.store.IsFreshBind(nonce) {
		t.Fatal("the fresh bind is not recorded")
	}
	// It is on disk before the replica writes the tag.
	onDisk, _, err := world.Load(r.root, testWorld)
	if err != nil || !onDisk.IsFreshBind(nonce) {
		t.Fatalf("the fresh bind is not in the world file (err %v)", err)
	}

	bound := hello(wire.FileBound, testWorld, 0)
	bound.BindNonce = wire.ID(nonce)
	bound.Want |= uint32(AskUnsaved)
	w = welcomeOf(t, r.exchange(u, bound))
	if w.Status != wire.StatusOK || !w.FreshBind {
		t.Fatalf("bound, still unsaved: %+v", w)
	}
	// A LEAKED hello of that nonce (V5 failed before the replica
	// knew, as on the first hello of every new process) is told the bind
	// is fresh, and not that its tag leaked.
	leak := hello(wire.FileLeaked, testWorld, 0)
	leak.BindNonce = wire.ID(nonce)
	ms = r.exchange(u, leak)
	if w = welcomeOf(t, ms); w.Status != wire.StatusUnbound || !w.FreshBind {
		t.Fatalf("leaked hello of a fresh nonce: %+v", w)
	}
	if ns := pick[*wire.Notice](ms); len(ns) != 0 {
		t.Fatalf("leaked hello of a fresh nonce got %s: %q", subs(ms), ns[0].Text)
	}
	if !r.log.has("joiner (co-op 1) joined with a tag naming pending fresh bind " + nonce.String() + ", as a file bound fresh does") {
		t.Error("the host did not log the fresh bind's first hello as one")
	}
	// A leaked hello of this world whose nonce is no pending fresh bind is
	// a leak.
	stale := hello(wire.FileLeaked, testWorld, 0)
	ms = r.exchange(u, stale)
	if w = welcomeOf(t, ms); w.Status != wire.StatusUnbound || w.FreshBind {
		t.Fatalf("leaked hello of a stale nonce: %+v", w)
	}
	if ns := pick[*wire.Notice](ms); len(ns) != 1 || ns[0].Code != wire.NoticeNotBound || !strings.Contains(ns[0].Text, "leaked from another file") || !strings.Contains(ns[0].Text, "should be scrubbed") {
		t.Fatalf("leaked hello of a stale nonce: %s, want the leak NOTICE", subs(ms))
	}
	// A leaked hello of another world, even with a nonce this world has
	// pending, may be a fresh bind of that world if only V5 fails: only
	// its host can tell, and the NOTICE says so, whole, without calling
	// for a scrub. The hub can't see which rules failed, so the NOTICE
	// leaves that to the replica.
	other := hello(wire.FileLeaked, otherWorld, 0)
	other.BindNonce = wire.ID(nonce)
	ms = r.exchange(u, other)
	if w = welcomeOf(t, ms); w.Status != wire.StatusUnbound || w.FreshBind {
		t.Fatalf("leaked hello of another world: %+v", w)
	}
	ns := pick[*wire.Notice](ms)
	if len(ns) != 1 || ns[0].Code != wire.NoticeNotBound {
		t.Fatalf("leaked hello of another world: %s, want one NOT_BOUND NOTICE", subs(ms))
	}
	if txt := ns[0].Text; !containsAll(txt, "a co-op tag of world "+otherWorld.String(), "It leaked from another file or, if only V5 fails, may be a fresh bind to that world not yet loaded from the card", "only that world's host can tell") ||
		strings.Contains(txt, "scrub") || !strings.HasSuffix(txt, "Nothing is synced until you bind the file") || len(txt) > wire.MaxNoticeLen {
		t.Fatalf("leaked hello of another world: NOTICE %q (%d bytes, max %d)", txt, len(txt), wire.MaxNoticeLen)
	}
	if !r.store.IsFreshBind(nonce) {
		t.Fatal("a leaked hello of another world cleared this world's fresh bind")
	}
	bound.Want &^= uint32(AskUnsaved)
	if w = welcomeOf(t, r.exchange(u, bound)); w.Status != wire.StatusOK || w.FreshBind {
		t.Fatalf("bound after a load from the card: %+v", w)
	}
	if r.store.IsFreshBind(nonce) {
		t.Fatal("the fresh bind is still pending after the file was loaded")
	}
	if st := r.peer(1); st.State != StateBound {
		t.Fatalf("state %s, want bound", st.State)
	}
}

// TestBindRefusals: what keeps a file from binding without --coop-merge,
// and what --coop-merge allows.
func TestBindRefusals(t *testing.T) {
	metKorl := addr.EventByte(0x0F80)
	offKorl := uint16(addr.OffEvent + metKorl.Off)
	cases := []struct {
		name    string
		merge   bool
		world   []wire.Gain // in the world first (via a bound peer)
		file    []world.Gain
		edit    func(m *wire.CoHello)
		reason  wire.NoticeCode
		granted bool
	}{
		{name: "subset", world: []wire.Gain{{Off: offOR, Val: 3}}, file: []world.Gain{{Off: offOR, Val: 1}}, granted: true},
		{name: "divergent", world: []wire.Gain{{Off: offOR, Val: 3}}, file: []world.Gain{{Off: offOR2, Val: 1}}, reason: wire.NoticeDivergent},
		{name: "divergent, merge", merge: true, file: []world.Gain{{Off: offOR2, Val: 1}}, granted: true},
		{name: "far behind", world: []wire.Gain{{Off: offKorl, Val: metKorl.Mask}}, reason: wire.NoticeDivergent},
		{name: "far behind, merge", merge: true, world: []wire.Gain{{Off: offKorl, Val: metKorl.Mask}}, granted: true},
		{name: "another world", edit: func(m *wire.CoHello) { m.WorldID = wire.ID(otherWorld) }, reason: wire.NoticeRejected},
		{name: "no nonce", edit: func(m *wire.CoHello) { m.BindNonce = wire.ID{} }, reason: wire.NoticeRejected},
		{name: "WORLD of another file", edit: func(m *wire.CoHello) { m.NameHash++ }, reason: wire.NoticeRejected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := fullCfg()
			cfg.Merge = tc.merge
			r := newRig(t, cfg)
			if len(tc.world) > 0 {
				a := r.join(newConn(9, "host"))
				r.send(a, gains(1, tc.world...))
			}
			u := newConn(1, "joiner")
			r.attach(u)
			r.send(u, hello(wire.FileUnbound, world.ID{}, 0))
			r.send(u, worldMsg(world.ID{}, 0, tc.file...))
			u.take(t)
			ask := hello(wire.FileUnbound, world.ID{}, 0)
			ask.WorldID, ask.BindNonce = wire.ID(testWorld), wire.ID{0x77}
			ask.Want |= uint32(AskBind)
			if tc.edit != nil {
				tc.edit(ask)
			}
			ms := r.exchange(u, ask)
			w := welcomeOf(t, ms)
			if got := Feature(w.Features)&FeatureBind != 0; got != tc.granted || w.Reason != tc.reason {
				t.Fatalf("bind granted %v reason %s (%s), want %v %s", got, w.Reason, subs(ms), tc.granted, tc.reason)
			}
			if got := Feature(w.Features)&FeatureMerge != 0; got != tc.merge {
				t.Errorf("FeatureMerge %v, want %v", got, tc.merge)
			}
			if w.FreshBind || r.store.IsFreshBind(world.ID{0x77}) {
				t.Error("a bind without AskUnsaved recorded a fresh bind")
			}
		})
	}
}

// TestFeatureMask: the host's mask (COOP_OFF) and the replica's own: what
// is granted, what is refused when off, and a resync when the image comes
// back.
func TestFeatureMask(t *testing.T) {
	r := newRig(t, fullCfg())
	a := newConn(1, "alice")
	r.attach(a)
	m := hello(wire.FileBound, testWorld, 1)
	m.Want = uint32(FeatureImage | FeaturePuzzle | FeatureLiveSwitches)
	if w := welcomeOf(t, r.exchange(a, m)); Feature(w.Features) != FeatureImage|FeaturePuzzle {
		t.Fatalf("granted %s, want image|puzzle (live switches are not allowed)", Feature(w.Features))
	}

	if err := r.h.SetFeatures(0); err != nil {
		t.Fatal(err)
	}
	cs := pick[*wire.Ctrl](a.take(t))
	if len(cs) != 1 || cs[0].Op != wire.CtrlCoopOff || cs[0].Arg != 0 {
		t.Fatalf("after SetFeatures(0) alice got %+v, want COOP_OFF 0", cs)
	}
	if got := codes(r.exchange(a, gains(1, wire.Gain{Off: offOR, Val: 1}))); !slices.Equal(got, []wire.NoticeCode{wire.NoticeRejected}) || r.store.Seq() != 0 {
		t.Fatalf("GAINS with co-op off: notices %v, seq %d", got, r.store.Seq())
	}

	if err := r.h.SetFeatures(DefaultFeatures | FeatureLiveSwitches); err != nil {
		t.Fatal(err)
	}
	cs = pick[*wire.Ctrl](a.take(t))
	if len(cs) != 2 || cs[0].Op != wire.CtrlCoopOff || Feature(cs[0].Arg) != FeatureImage|FeaturePuzzle|FeatureLiveSwitches || cs[1].Op != wire.CtrlResync {
		t.Fatalf("after turning co-op back on alice got %+v, want COOP_OFF then RESYNC", cs)
	}

	// The replica narrows its own.
	cs = pick[*wire.Ctrl](r.exchange(a, &wire.Ctrl{Op: wire.CtrlCoopOff, Arg: uint32(FeatureImage)}))
	if len(cs) != 1 || Feature(cs[0].Arg) != FeatureImage {
		t.Fatalf("own COOP_OFF answered %+v, want COOP_OFF image", cs)
	}
	if got := r.status().Features; got != DefaultFeatures|FeatureLiveSwitches {
		t.Errorf("host features %s", got)
	}
}

// TestStopFlushes: Stop handles what is queued, writes the world, closes
// every peer, and the hub refuses everything after it.
func TestStopFlushes(t *testing.T) {
	r := newRig(t, fullCfg())
	a := r.join(newConn(1, "alice"))
	// Queued but not yet handled when Stop comes.
	for i := range 40 {
		r.h.frame(a, mustEncode(t, gains(uint32(i+1), wire.Gain{Off: offTbox(i%16, i/16), Val: 1 << (i % 8)})))
	}
	if err := r.h.Stop(); err != nil {
		t.Fatal(err)
	}
	w, rep, err := world.Load(r.root, testWorld)
	if err != nil {
		t.Fatal(err)
	}
	if rep.FellBack() || rep.Replayed != 0 || w.Seq() != 40 {
		t.Fatalf("world file at seq %d (from %s, %d journal records replayed), want all 40 written by Stop", w.Seq(), rep.From, rep.Replayed)
	}
	im := w.Image()
	for i := range 40 {
		if im[offTbox(i%16, i/16)]&(1<<(i%8)) == 0 {
			t.Fatalf("gain %d is not in the world file after Stop", i)
		}
	}
	if !a.isClosed() {
		t.Error("Stop left a peer connected")
	}
	if err := r.h.attach(newConn(2, "late")); !errors.Is(err, ErrStopped) {
		t.Errorf("attach after Stop: %v, want ErrStopped", err)
	}
	r.h.frame(a, mustEncode(t, gains(99, wire.Gain{Off: offOR, Val: 1})))
	r.h.detach(a)
	if _, err := r.h.Status(); !errors.Is(err, ErrStopped) {
		t.Errorf("Status after Stop: %v", err)
	}
	if err := r.h.Tombstone(int(offOR), 1); !errors.Is(err, ErrStopped) {
		t.Errorf("Tombstone after Stop: %v", err)
	}
	if err := r.h.Stop(); err != nil {
		t.Errorf("second Stop: %v", err)
	}
}

// TestSlowPeerDoesNotStallHub: a peer that stops reading fills its outbox
// and is disconnected; the hub and the other peers carry on.
func TestSlowPeerDoesNotStallHub(t *testing.T) {
	r := newRigMode(t, fullCfg(), false)
	slow := newConn(1, "slow")
	slow.block = make(chan struct{})
	defer close(slow.block)
	fast := newConn(2, "fast")
	src := newConn(3, "source")
	for _, c := range []*fakeConn{slow, fast, src} {
		r.attach(c)
		r.send(c, hello(wire.FileBound, testWorld, c.id))
	}

	// 400 proposals, each one new bit: 400 relays to every peer. The
	// source waits for the fast peer now and then, so only the slow one
	// falls a whole outbox behind however busy the machine is.
	const n = 400
	for i := range n {
		r.send(src, gains(uint32(i+1), wire.Gain{Off: offTbox(i/32, i/8%4), Val: 1 << (i % 8)}))
		if i%64 == 63 && !waitFor(5*time.Second, func() bool { return fast.count() >= i+1 }) {
			t.Fatalf("the fast peer is stuck at %d messages", fast.count())
		}
	}
	if fast.isClosed() || src.isClosed() {
		t.Fatal("a peer that reads was disconnected")
	}
	if !slow.isClosed() {
		t.Fatal("the peer that stopped reading is still connected")
	}
	if !r.log.has("waiting to be sent; disconnecting") {
		t.Error("no log line for the slow peer")
	}
	r.h.detach(slow) // as the server would
	if !waitFor(5*time.Second, func() bool { return fast.count() >= n+1 }) {
		t.Fatalf("the fast peer got %d messages, want %d", fast.count(), n+1)
	}
	if st := r.status(); st.Seq != n || len(st.Peers) != 2 {
		t.Fatalf("seq %d with %d peers, want %d with 2", st.Seq, len(st.Peers), n)
	}
}

// TestInboundFloodDisconnects: a peer that queues more than its share of
// frames while the hub is busy is disconnected; the others are not.
func TestInboundFloodDisconnects(t *testing.T) {
	r := newRig(t, fullCfg())
	flood := r.join(newConn(1, "flood"))
	calm := r.join(newConn(2, "calm"))

	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock() // a failure below must not leave the hub blocked for Stop
	go r.h.do(func() { close(started); <-release })
	<-started
	b := mustEncode(t, &wire.Ctrl{Op: wire.CtrlResync})
	for range inboxPerPeer {
		r.h.frame(flood, b)
	}
	if flood.isClosed() {
		t.Fatal("disconnected at its share, not past it")
	}
	r.h.frame(flood, b)
	if !flood.isClosed() {
		t.Fatal("a peer past its share of the queue is still connected")
	}
	r.h.frame(calm, mustEncode(t, gains(1, wire.Gain{Off: offOR, Val: 1})))
	unblock()
	r.sync()
	if calm.isClosed() || r.store.Image()[offOR] != 1 {
		t.Fatal("the calm peer's frame was lost")
	}
}

// TestHandlerPanicCostsOnePeer: a panic while handling a frame disconnects
// that peer and leaves the hub serving.
func TestHandlerPanicCostsOnePeer(t *testing.T) {
	cfg := fullCfg()
	cfg.SyncedScoped = func(table int, stage string, room, no int) bool {
		if stage == "Boom" {
			panic("verdict bug")
		}
		return true
	}
	r := newRig(t, cfg)
	a := r.join(newConn(1, "alice"))
	b := r.join(newConn(2, "bob"))
	r.send(a, presence("Boom", 3, 0))
	r.send(a, scoped(wire.ScopeZone, "Boom", 3, 0, wire.ScopedWord{Word: 0, Bits: 1}))
	if !a.isClosed() || !r.log.has("internal error") {
		t.Fatal("the panicking peer was not disconnected and logged")
	}
	r.h.detach(a)
	r.send(b, gains(1, wire.Gain{Off: offOR, Val: 1}))
	if r.store.Image()[offOR] != 1 {
		t.Fatal("the hub stopped serving after a panic")
	}
}

// TestAttachRefusals: a second attach of a live connection or co-op ID,
// and ID 0, are refused.
func TestAttachRefusals(t *testing.T) {
	r := newRig(t, fullCfg())
	r.attach(newConn(1, "alice"))
	for _, c := range []*fakeConn{newConn(1, "twin"), newConn(0, "zero")} {
		if err := r.h.attach(c); err == nil {
			t.Errorf("attach of %s (ID %d) succeeded", c.name, c.id)
		}
	}
}

func TestFromLoopback(t *testing.T) {
	cases := []struct {
		addr net.Addr
		want bool
	}{
		{&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}, true},
		{&net.TCPAddr{IP: net.IPv6loopback}, true},
		{&net.TCPAddr{IP: net.IPv4(192, 168, 1, 20)}, false},
		{nil, false},
	}
	for _, tc := range cases {
		c := newConn(1, "x")
		c.addr = tc.addr
		if got := fromLoopback(c); got != tc.want {
			t.Errorf("fromLoopback(%v) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}

func TestClip(t *testing.T) {
	long := strings.Repeat("é", 200) // 400 bytes
	cases := []string{"plain", "tab\tand\nnewline", "bad \xff utf8", long, strings.Repeat("x", 300)}
	for _, s := range cases {
		got := clip(s)
		if len(got) > wire.MaxNoticeLen || !utf8.ValidString(got) {
			t.Errorf("clip(%.20q) = %d bytes, valid %v", s, len(got), utf8.ValidString(got))
		}
		if _, err := wire.Encode(&wire.Notice{Code: wire.NoticeRejected, Text: got}); err != nil {
			t.Errorf("clip(%.20q) is not a valid NOTICE: %v", s, err)
		}
	}
	if got := clip("tab\tx"); got != "tab x" {
		t.Errorf("clip kept a control character: %q", got)
	}
	if got := clip(long); !strings.HasSuffix(got, "...") {
		t.Errorf("a cut text has no ellipsis: %q", got[len(got)-5:])
	}
}

func TestFeatureString(t *testing.T) {
	cases := []struct {
		f    Feature
		want string
	}{
		{0, "none"},
		{DefaultFeatures, "image|puzzle|combat|switches|story|inventory|keys|life|magic"},
		{TableFeatures, "puzzle|combat|live-switches|switches|keys|life"},
		{FeatureBind | AskBind | 1<<20, "bind|ask-bind|0x100000"},
	}
	for _, tc := range cases {
		if got := tc.f.String(); got != tc.want {
			t.Errorf("%#x.String() = %q, want %q", uint32(tc.f), got, tc.want)
		}
	}
}

func waitFor(d time.Duration, ok func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if ok() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return ok()
}
