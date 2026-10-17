package replica

import (
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// Stages the tests load. The names are only labels; the tables are the
// ones the real stages use.
var (
	outset  = fakegame.Stage{Name: "sea", Room: 44, Table: addr.TableSea}
	drc     = fakegame.Stage{Name: "M_NewD2", Table: addr.TableDRC, KeyHUD: true}
	drcBoss = fakegame.Stage{Name: "M_DragB", Table: addr.TableDRC, KeyHUD: true}
	woods   = fakegame.Stage{Name: "kindan", Table: addr.TableFW, KeyHUD: true}
)

var (
	testWorld = bind.WorldID{0xC0, 0x0B, 1, 2, 3, 4, 5, 6}
	someWorld = bind.WorldID{0xAA, 0xBB, 1, 2, 3, 4, 5, 6}
	testNonce = bind.Nonce{0x40, 0x41, 0x42, 0x43, 0x44, 0x45, 0x46, 0x47}
)

// rig is one emulated Dolphin: a FakeGame over a FakeMem with a file
// loaded in a stage. Its Store writes are the game's; only the code under
// test writes with Mem.Write, which the recorder sees.
type rig struct {
	t *testing.T
	m *mem.FakeMem
	g *fakegame.FakeGame
}

// newRig starts a New Game named name in slot 0 and loads Outset.
func newRig(t *testing.T, name string) *rig {
	t.Helper()
	m := mem.NewFakeMem()
	r := &rig{t: t, m: m, g: fakegame.New(m)}
	if err := r.g.NewGame(0, name); err != nil {
		t.Fatal(err)
	}
	r.load(outset)
	return r
}

func (r *rig) load(s fakegame.Stage) {
	r.t.Helper()
	if err := r.g.LoadStage(s); err != nil {
		r.t.Fatalf("LoadStage(%s): %v", s.Name, err)
	}
}

func (r *rig) enter(room int, neighbours ...int) {
	r.t.Helper()
	if err := r.g.EnterRoom(room, neighbours...); err != nil {
		r.t.Fatalf("EnterRoom(%d): %v", room, err)
	}
}

func (r *rig) onSwitch(no, room int) {
	r.t.Helper()
	if err := r.g.OnSwitch(no, room); err != nil {
		r.t.Fatalf("OnSwitch(0x%X, %d): %v", no, room, err)
	}
}

func (r *rig) read(a uint32, n int) []byte {
	r.t.Helper()
	b, err := r.m.Read(a, n)
	if err != nil {
		r.t.Fatal(err)
	}
	return b
}

func (r *rig) store(a uint32, b []byte) {
	r.t.Helper()
	if err := r.m.Store(a, b); err != nil {
		r.t.Fatal(err)
	}
}

// table is the save table the game has loaded (mDan.mStageNo).
func (r *rig) table() int { return int(int8(r.read(addr.DanStageNo, 1)[0])) }

// byteAddr is where the game keeps image byte off: the live memBit for the
// loaded table's slot (its flag setters write mMemBit, d_save.cpp:
// 1072-1139), the saved image otherwise.
func (r *rig) byteAddr(off int) uint32 {
	if t, rel, ok := memBitTable(off); ok && t == r.table() {
		return addr.LiveMemBit + uint32(rel)
	}
	return addr.SaveImage + uint32(off)
}

// set ORs bits into image byte off as the game would.
func (r *rig) set(off int, bits byte) {
	a := r.byteAddr(off)
	b := r.read(a, 1)
	b[0] |= bits
	r.store(a, b)
}

// clear clears bits of image byte off as the game would.
func (r *rig) clear(off int, bits byte) {
	a := r.byteAddr(off)
	b := r.read(a, 1)
	b[0] &^= bits
	r.store(a, b)
}

// put sets image byte off to v as the game would (a MAX field).
func (r *rig) put(off int, v byte) { r.store(r.byteAddr(off), []byte{v}) }

// image is the canonical image of the game now.
func (r *rig) image() world.Image {
	r.t.Helper()
	s, err := state.Read(r.m, state.Options{})
	if err != nil {
		r.t.Fatal(err)
	}
	return world.Image(s.Image())
}

// tag writes t over the tag bytes as card_to_memory would, and gives the
// file an mDateIPL, as a file loaded from the card has (V5).
func (r *rig) tag(t bind.Tag) {
	b := t.Encode()
	r.store(addr.Reserve, b[:])
	r.saved()
}

// saved stamps mDateIPL, as loading the file back from the card does.
func (r *rig) saved() {
	var d [8]byte
	binary.BigEndian.PutUint64(d[:], 0x1234_5678)
	r.store(addr.DateIPL, d[:])
}

// tagFor is a valid tag of world w for the loaded file.
func (r *rig) tagFor(w bind.WorldID, n bind.Nonce) bind.Tag {
	f, err := bind.ReadFile(r.m)
	if err != nil {
		r.t.Fatal(err)
	}
	return bind.Tag{Layout: bind.LayoutV1, Slot: f.Slot, ClearCount: f.ClearCount, World: w, Nonce: n, NameHash: bind.NameHash(f.Name[:])}
}

// differRig runs a Differ (and the gates) over a rig, one tick per frame.
type differRig struct {
	*rig
	d  *Differ
	gt gate.Tracker
}

func newDifferRig(t *testing.T, policy *world.Policy) *differRig {
	return &differRig{rig: newRig(t, "Link"), d: NewDiffer(policy)}
}

// tick runs a frame, then one snapshot through the differ.
func (dr *differRig) tick() Edges {
	dr.t.Helper()
	dr.g.Frame()
	return dr.tickNoFrame()
}

func (dr *differRig) tickNoFrame() Edges {
	dr.t.Helper()
	s, err := state.Read(dr.m, state.Options{})
	if err != nil {
		dr.t.Fatal(err)
	}
	st := dr.gt.Observe(s)
	return dr.d.observe(observe(dr.m, s, st))
}

// ticks runs n ticks and returns every edge they found.
func (dr *differRig) ticks(n int) Edges {
	dr.t.Helper()
	var all Edges
	for range n {
		e := dr.tick()
		all.Gains = append(all.Gains, e.Gains...)
		all.Scoped = append(all.Scoped, e.Scoped...)
	}
	return all
}

// fmtGains is gs as "off=val" pairs for failure messages.
func fmtGains(gs []world.Gain) string {
	var parts []string
	for _, g := range gs {
		parts = append(parts, fmt.Sprintf("%s=0x%02X", DefaultLabel(int(g.Off), -1), g.Val))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// hubEnd is the hub's end of a replica's connection in the unit tests: it
// decodes and keeps what the replica sends.
type hubEnd struct {
	t    *testing.T
	mu   sync.Mutex
	got  []wire.Msg
	fail error // Send returns it when set
}

func (h *hubEnd) Send(b []byte) error {
	m, err := wire.Decode(b, wire.Up)
	if err != nil {
		h.t.Errorf("the replica sent a payload the hub can't decode: %v (%x)", err, b)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.fail != nil {
		return h.fail
	}
	h.got = append(h.got, m)
	return nil
}

// take returns what was sent since the last take.
func (h *hubEnd) take() []wire.Msg {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := h.got
	h.got = nil
	return out
}

// say hands the replica messages as the hub would send them.
func say(t *testing.T, r *Replica, ms ...wire.Msg) {
	t.Helper()
	for _, m := range ms {
		b, err := wire.Encode(m)
		if err != nil {
			t.Fatalf("encoding %s: %v", m.Sub(), err)
		}
		r.Handle(b)
	}
}

// subs lists the sub-types of ms, for failure messages and order checks.
func subs(ms []wire.Msg) string {
	var parts []string
	for _, m := range ms {
		parts = append(parts, m.Sub().String())
	}
	return strings.Join(parts, " ")
}

// find returns the first message of type M in ms.
func find[M wire.Msg](ms []wire.Msg) (M, bool) {
	for _, m := range ms {
		if x, ok := m.(M); ok {
			return x, true
		}
	}
	var zero M
	return zero, false
}

// all returns every message of type M in ms.
func all[M wire.Msg](ms []wire.Msg) []M {
	var out []M
	for _, m := range ms {
		if x, ok := m.(M); ok {
			out = append(out, x)
		}
	}
	return out
}

// logBuf collects a replica's log lines.
type logBuf struct {
	mu    sync.Mutex
	lines []string
}

func (l *logBuf) logf(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func (l *logBuf) has(sub string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range l.lines {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func (l *logBuf) count(sub string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, s := range l.lines {
		if strings.Contains(s, sub) {
			n++
		}
	}
	return n
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// replicaRig is a Replica over a rig, connected to a hubEnd.
type replicaRig struct {
	*rig
	r      *Replica
	hub    *hubEnd
	log    *logBuf
	offers []Offer
	pfs    []PreflightReport
}

func newReplicaRig(t *testing.T, cfg Config) *replicaRig {
	t.Helper()
	rr := &replicaRig{rig: newRig(t, "Link"), hub: &hubEnd{t: t}, log: &logBuf{}}
	cfg.Mem = rr.m
	cfg.Logf = rr.log.logf
	cfg.OnOffer = func(o Offer) { rr.offers = append(rr.offers, o) }
	cfg.OnPreflight = func(p PreflightReport) { rr.pfs = append(rr.pfs, p) }
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rr.r = r
	return rr
}

// tick runs a frame, then one replica tick.
func (rr *replicaRig) tick() Report {
	rr.g.Frame()
	return rr.r.Tick()
}

// settle runs ticks until the gates open (STABLE needs StableTicks
// unchanged ones), returning what the replica sent meanwhile.
func (rr *replicaRig) settle() []wire.Msg {
	rr.t.Helper()
	var sent []wire.Msg
	for range gate.StableTicks + 2 {
		rr.tick()
		sent = append(sent, rr.hub.take()...)
	}
	return sent
}

// welcome is the CO_WELCOME the hub of testWorld sends.
func welcome(st wire.Status, f uint32, fresh bool) *wire.CoWelcome {
	m := &wire.CoWelcome{Status: st, Features: f, WorldID: wire.ID(testWorld), Seq: 1, FreshBind: fresh}
	if st != wire.StatusOK {
		m.Reason = wire.NoticeNotBound
	}
	return m
}

// worldOf is a WORLD of testWorld with img.
func worldOf(seq uint64, img world.Image) *wire.World {
	m := &wire.World{WorldID: wire.ID(testWorld), Seq: seq, Image: wire.Image(img)}
	wire.StripLocal(&m.Image)
	return m
}

// breakerClock is a clock tests move by hand.
type breakerClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *breakerClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *breakerClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}
