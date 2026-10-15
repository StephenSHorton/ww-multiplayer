package replica

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/gate"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/mem"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
)

// beatMem is the Mem a reconciling replica runs over in these tests. The
// game runs a frame when the FrameWriter waits for the heartbeat: on a
// heartbeat read right after another (snap.Read reads it once, between
// other reads; waitBeat reads it in a loop). Every write is kept for the
// audit, with the byte it replaced and the save table loaded.
type beatMem struct {
	*mem.FakeMem
	game *fakegame.FakeGame

	mu     sync.Mutex
	last   uint32 // the address of the last read
	stall  bool   // the game does not beat: emulation is paused
	polls  int    // heartbeat reads that found it unmoved while stalled
	tick   int    // the test's tick the writes belong to
	writes []auditWrite
	places []gamePlace // where the game was as each tick began, by tick number
	young  []int       // by tick number, the executes of the youngest room's actors as it began (-1: none)
	hook   func(n int) // run before the n-th heartbeat read since onHeartbeat, from 1
	hbn    int
	actors []fakegame.Actor // run on every frame (FakeGame.FrameWith)
	actErr error            // the first error an actor returned
	// ages follows how long each room's actors have run, on every frame,
	// for the audit's room hold.
	ages fakegame.RoomAges
}

// gamePlace is where a fake game is, read from its memory as a tick
// begins: the audit's own record of the room and stage changes, apart
// from what the replica made of them (Report.RoomChange).
type gamePlace struct {
	stage   string
	table   int8 // mDan.mStageNo
	room    int8 // mStayNo
	pending bool // mNextStage.mEnable
	link    bool // mpPlayerPtr[0] != 0
}

// placeOf reads where the game over m is.
func placeOf(m mem.Mem) (gamePlace, error) {
	var p gamePlace
	name, err := m.Read(addr.CurStageName, addr.StageNameLen)
	if err != nil {
		return p, err
	}
	p.stage = strings.TrimRight(string(name), "\x00")
	for _, f := range []struct {
		a uint32
		v func(b byte)
	}{
		{addr.DanStageNo, func(b byte) { p.table = int8(b) }},
		{addr.StayNo, func(b byte) { p.room = int8(b) }},
		{addr.NextStageEnable, func(b byte) { p.pending = b != 0 }},
	} {
		b, err := mem.ReadU8(m, f.a)
		if err != nil {
			return p, err
		}
		f.v(b)
	}
	link, err := mem.ReadU32(m, addr.PlayerPtr)
	p.link = link != 0
	return p, err
}

// roomWindows says which ticks may make room-change writes (§6.3), by the
// game's places alone: the first tick since the place last changed that
// is ARMED with the gates open (STABLE needs gate.StableTicks unchanged
// ticks, so it comes that many after the change), or a later such tick
// when every one before it since the change found the heartbeat stalled
// or the game moved on (ErrStall, ErrAborted: nothing could be tried).
// The loaded table's memory switches wait for the room hold as well, so
// their tick can come later (switchTicks).
func roomWindows(reps []Report, places []gamePlace) []bool {
	win := make([]bool, len(reps))
	due := false
	for i, rep := range reps {
		if i > 0 && i < len(places) && places[i] != places[i-1] {
			due = true
		}
		if !due || rep.State != StateArmed || !rep.Gates.CanWrite() {
			continue
		}
		win[i] = true
		if !errors.Is(rep.WriteErr, ErrStall) && !errors.Is(rep.WriteErr, ErrAborted) {
			due = false
		}
	}
	return win
}

// changesAt is, for each tick, the last tick at or before it on which the
// game's place differed from the tick before's, or -1 before the first.
func changesAt(n int, changed func(i int) bool) []int {
	at := make([]int, n)
	last := -1
	for i := range at {
		if i > 0 && changed(i) {
			last = i
		}
		at[i] = last
	}
	return at
}

// switchTicks checks the ticks the loaded table's memory switches §6.3
// defers are written on, by the game's places alone (changes, from
// changesAt) and in write order: each comes after a change of place, and
// one tick since each change writes them, or a later one when every tick
// before it that wrote some since the change ended stalled or aborted
// (ErrStall, ErrAborted: its batch is tried again). Which tick that is
// the room hold decides, which the audit checks apart (RoomAges). It
// returns the reason a write at tick is out of place, or "".
type switchTicks struct {
	changes []int
	last    map[int]int // by change, the last tick that wrote a switch
}

func (s *switchTicks) check(tick int, retried func(tick int) bool) string {
	c := s.changes[tick]
	if c < 0 {
		return "before any room change"
	}
	if s.last == nil {
		s.last = map[int]int{}
	}
	if lt, ok := s.last[c]; ok && lt != tick && !retried(lt) {
		return fmt.Sprintf("on a second tick since the room change on tick %d (tick %d wrote them)", c, lt)
	}
	s.last[c] = tick
	return ""
}

// youngWrite reports why a write of a switch that every room's actors read
// came too early for a shutter, or "": the youngest room's actors had run
// fewer than fakegame.ShutterFrames executes as it was made, so a shutter
// created with them would not see it change (§6.1, the room hold).
func youngWrite(room, execs int) string {
	if room < 0 || execs >= fakegame.ShutterFrames {
		return ""
	}
	return fmt.Sprintf("room %d's actors had run %d frames: a new shutter there would not see it (the room hold)", room, execs)
}

// auditWrite is one write the replica made.
type auditWrite struct {
	tick       int
	a          uint32
	n          int
	old, val   byte   // the first byte's
	prev, data []byte // every byte's
	table      int8   // mDan.mStageNo as the write was made
	// young is the room whose actors had run the fewest frames as the
	// write was made (fakegame.RoomAges; -1 for none), and execs how many.
	young, execs int
}

func (b *beatMem) Read(a uint32, n int) ([]byte, error) {
	b.mu.Lock()
	again := a == addr.SpawnTrigger && b.last == addr.SpawnTrigger
	beat := again && !b.stall
	if again && b.stall {
		b.polls++
	}
	b.last = a
	var hook func(int)
	if a == addr.SpawnTrigger && b.hook != nil {
		b.hbn++
		hook = b.hook
	}
	hbn := b.hbn
	b.mu.Unlock()
	if hook != nil {
		hook(hbn)
	}
	if beat {
		b.frame()
	}
	return b.FakeMem.Read(a, n)
}

// frame runs one frame of the game, with the actors set by setActors and
// the rooms' ages.
func (b *beatMem) frame() {
	b.mu.Lock()
	actors := append([]fakegame.Actor{&b.ages}, b.actors...)
	b.mu.Unlock()
	if err := b.game.FrameWith(actors...); err != nil {
		b.mu.Lock()
		if b.actErr == nil {
			b.actErr = err
		}
		b.mu.Unlock()
	}
}

// setActors makes actors run on every frame from now, the test's own and
// the writer's beats alike; the first error one returns is actorErr's.
func (b *beatMem) setActors(actors ...fakegame.Actor) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.actors = actors
}

func (b *beatMem) actorErr() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.actErr
}

// onHeartbeat runs hook before each heartbeat read from now, numbered from
// 1 (snap.Read's is a tick's first); nil stops it.
func (b *beatMem) onHeartbeat(hook func(n int)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.hook, b.hbn = hook, 0
}

func (b *beatMem) Write(a uint32, d []byte) error {
	old, err := b.FakeMem.Read(a, len(d))
	if err != nil {
		return err
	}
	t, err := b.FakeMem.Read(addr.DanStageNo, 1)
	if err != nil {
		return err
	}
	if err := b.FakeMem.Write(a, d); err != nil {
		return err
	}
	young, execs := b.ages.Youngest()
	b.mu.Lock()
	defer b.mu.Unlock()
	w := auditWrite{tick: b.tick, a: a, n: len(d), prev: old, data: bytes.Clone(d), table: int8(t[0]), young: young, execs: execs}
	if len(d) > 0 {
		w.old, w.val = old[0], d[0]
	}
	b.writes = append(b.writes, w)
	return nil
}

// setTick starts tick n: its writes are recorded as n's, and where the
// game is now as n's place.
func (b *beatMem) setTick(n int) {
	p, err := placeOf(b.FakeMem)
	if err != nil {
		panic(err)
	}
	room, execs := b.ages.Youngest()
	if room < 0 {
		execs = -1
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tick = n
	b.places = append(b.places[:min(n, len(b.places))], p)
	b.young = append(b.young[:min(n, len(b.young))], execs)
}

// youngAt is, by tick number, the executes of the youngest room's actors
// as each tick began (-1: no room's actors existed).
func (b *beatMem) youngAt() []int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]int(nil), b.young...)
}

func (b *beatMem) placesSeen() []gamePlace {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]gamePlace(nil), b.places...)
}

func (b *beatMem) setStall(on bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stall, b.polls = on, 0
}

func (b *beatMem) stalledPolls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.polls
}

func (b *beatMem) written() []auditWrite {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]auditWrite(nil), b.writes...)
}

// recRig is a reconciling replica (Config.Apply) over a rig in Outset,
// bound, welcomed with the given features and armed, the hub's WORLD
// empty.
type recRig struct {
	*replicaRig
	bm     *beatMem
	hello  *wire.CoHello
	seq    uint64
	reps   []Report // every tick's, by tick number
	frames int      // the game's frames before each tick; 0 is 1
}

func newRecRig(t *testing.T, cfg Config, f hub.Feature) *recRig {
	t.Helper()
	rr := newRecRigNoWorld(t, cfg, f)
	say(t, rr.r, worldOf(1, world.Image{}))
	rr.tick()
	rr.hub.take()
	return rr
}

// newRecRigNoWorld is newRecRig before the hub's WORLD arrives.
func newRecRigNoWorld(t *testing.T, cfg Config, f hub.Feature) *recRig {
	t.Helper()
	rec := newRecRigNoWelcome(t, cfg)
	rec.welcome(f)
	return rec
}

// welcome is the hub's CO_WELCOME with features f, which arms the replica.
func (rr *recRig) welcome(f hub.Feature) {
	rr.t.Helper()
	say(rr.t, rr.r, welcome(wire.StatusOK, uint32(f), false))
	if rep := rr.tick(); rep.State != StateArmed || !rep.World {
		rr.t.Fatalf("not armed after the welcome: %+v\n%s", rep, rr.log)
	}
	rr.hub.take()
}

// newRecRigNoWelcome is newRecRig once it said CO_HELLO, before the hub's
// answer.
func newRecRigNoWelcome(t *testing.T, cfg Config) *recRig {
	t.Helper()
	rr := &replicaRig{rig: newRig(t, "Link"), hub: &hubEnd{t: t}, log: &logBuf{}}
	bm := &beatMem{FakeMem: rr.m, game: rr.g}
	cfg.Mem, cfg.Logf = bm, rr.log.logf
	if cfg.Policy == nil {
		cfg.Policy = switch5Policy(t)
	}
	if cfg.BeatTimeout == 0 {
		cfg.BeatTimeout = time.Second // the game beats on the writer's second read
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rr.r = r
	rec := &recRig{replicaRig: rr, bm: bm, seq: 1}
	rr.tag(rr.tagFor(testWorld, testNonce))
	r.Connect(rr.hub)
	var sent []wire.Msg
	for range gate.StableTicks + 2 {
		rec.tick()
		sent = append(sent, rr.hub.take()...)
	}
	h, ok := find[*wire.CoHello](sent)
	if !ok {
		t.Fatalf("no CO_HELLO: %s", subs(sent))
	}
	rec.hello = h
	return rec
}

// splitLoad is a stage change to s as the game makes it, tick by tick:
// mNextStage set, then Link gone with the old play scene, then the new
// stage (FakeGame.ChangeStage puts both back at once).
func (rr *recRig) splitLoad(s fakegame.Stage) {
	rr.t.Helper()
	if err := rr.g.SetNextStage(s); err != nil {
		rr.t.Fatal(err)
	}
	if n := rr.ticks(3); n != 0 {
		rr.t.Fatalf("%d bytes written with a stage change pending", n)
	}
	rr.store(addr.PlayerPtr, []byte{0, 0, 0, 0})
	if n := rr.ticks(3); n != 0 {
		rr.t.Fatalf("%d bytes written with no Link", n)
	}
	if err := rr.g.ChangeStage(); err != nil {
		rr.t.Fatal(err)
	}
}

// tick runs a frame (rr.frames of them), then one replica tick (with its
// writes).
func (rr *recRig) tick() Report {
	rr.t.Helper()
	for range max(rr.frames, 1) {
		rr.bm.frame()
	}
	rr.bm.setTick(len(rr.reps))
	rep := rr.r.Tick()
	rr.reps = append(rr.reps, rep)
	return rep
}

// ticks runs n ticks and returns the bytes they wrote.
func (rr *recRig) ticks(n int) int {
	rr.t.Helper()
	wrote := 0
	for range n {
		wrote += rr.tick().Wrote
	}
	return wrote
}

// holdTicks is how many ticks the room hold takes (roomSettle IDLE frames)
// for rooms whose actors were just created, at rr.frames frames a tick,
// with two to spare.
func (rr *recRig) holdTicks() int { return roomSettle/max(rr.frames, 1) + 2 }

// settleRooms runs ticks enough for the room hold of rooms whose actors
// were just created, and returns the bytes they wrote.
func (rr *recRig) settleRooms() int { return rr.ticks(rr.holdTicks()) }

// untilWrite ticks until a tick writes, at most n ticks, and returns its
// number, or -1.
func (rr *recRig) untilWrite(n int) int {
	rr.t.Helper()
	for range n {
		if rr.tick().Wrote > 0 {
			return len(rr.reps) - 1
		}
	}
	return -1
}

// world makes img the world the hub tells the replica.
func (rr *recRig) world(img world.Image) {
	rr.t.Helper()
	rr.seq++
	say(rr.t, rr.r, worldOf(rr.seq, img))
}

// savedByte is image byte off's saved copy (addr.SaveImage + off).
func (r *rig) savedByte(off int) byte { return r.read(addr.SaveImage+uint32(off), 1)[0] }

// liveByte is the live memBit's copy of image byte off, which must be in
// the loaded table's slot.
func (r *rig) liveByte(off int) byte {
	r.t.Helper()
	t, rel, ok := memBitTable(off)
	if !ok || t != r.table() {
		r.t.Fatalf("%s is not in the loaded table", DefaultLabel(off, -1))
	}
	return r.read(addr.LiveMemBit+uint32(rel), 1)[0]
}

// noGainsUp fails if the replica proposed anything: the reconciler's
// writes are the world's, so they must never go back up.
func (rr *recRig) noGainsUp(why string) {
	rr.t.Helper()
	for _, g := range all[*wire.GainsUp](rr.hub.take()) {
		rr.t.Fatalf("%s: the replica re-sent %d gains: %+v", why, len(g.Gains), g.Gains)
	}
}

func (rr *recRig) audit(liveSwitches bool) {
	rr.t.Helper()
	auditWrites(rr.t, rr.bm.written(), rr.reps, rr.bm.placesSeen(), liveSwitches)
}

// loadedSlot reports whether a is a byte of the loaded table t's memBit:
// the live copy, or t's saved slot. rel is its offset in the memBit.
func loadedSlot(a uint32, t int8) (rel int, ok bool) {
	if a >= addr.LiveMemBit && a < addr.LiveMemBit+addr.MemBitSize {
		return int(a - addr.LiveMemBit), true
	}
	if saved, err := addr.SavedMemBitAddr(int(t)); err == nil && a >= saved && a < saved+addr.MemBitSize {
		return int(a - saved), true
	}
	return 0, false
}

// auditWrites is §8.1's write audit over every write a replica made, reps
// being its ticks' reports and places where the game was as each began:
// each write is one byte, inside M3's allowlist (so no PER_PLAYER, NEVER,
// DERIVED or TAG byte), sets bits and clears none, is made on a tick
// whose gates were open, and sets no bit an earlier write set at the same
// place (no re-sends). A byte of the loaded table is written only when
// IDLE. A memory switch of it is written only once every room's actors
// have run fakegame.ShutterFrames frames (the room hold, by
// fakegame.RoomAges), and only on the one tick since a room change that
// writes them, ARMED (switchTicks), or, with live switches, when IDLE.
// The ocean, charts and sea map are written only on a room-change tick.
// Which ticks are room-change ones the audit works out from places
// (roomWindows, switchTicks), not from the replica's reports.
func auditWrites(t *testing.T, ws []auditWrite, reps []Report, places []gamePlace, liveSwitches bool) {
	t.Helper()
	if len(places) != len(reps) {
		t.Fatalf("%d ticks reported, %d places recorded", len(reps), len(places))
	}
	win := roomWindows(reps, places)
	sw := switchTicks{changes: changesAt(len(places), func(i int) bool { return places[i] != places[i-1] })}
	retried := func(tick int) bool {
		return errors.Is(reps[tick].WriteErr, ErrStall) || errors.Is(reps[tick].WriteErr, ErrAborted)
	}
	g := NewGuardFor(M3Targets)
	type place struct {
		a     uint32
		table int8
	}
	set := map[place]byte{}
	for _, w := range ws {
		rep, window := reps[w.tick], win[w.tick]
		at := func(format string, args ...any) {
			t.Helper()
			t.Fatalf("tick %d, write at 0x%08X (0x%02X -> 0x%02X, table %d): "+format,
				append([]any{w.tick, w.a, w.old, w.val, w.table}, args...)...)
		}
		switch {
		case w.n != 1:
			at("%d bytes: writes are bytes", w.n)
		case g.Mask(w.a) == 0:
			at("outside M3's allowlist")
		case w.old&^w.val != 0:
			at("a clear")
		case w.old == w.val:
			at("wrote what was there")
		case !rep.Gates.CanWrite():
			at("the gates were closed: %s", rep.Gates.Why())
		}
		if w.a >= addr.SaveImage && w.a < addr.SaveImage+addr.SaveImageSize {
			off := int(w.a - addr.SaveImage)
			switch c := class.Of(off).Class; c {
			case class.PerPlayer, class.Never, class.Derived, class.Tag:
				at("a %v byte", c)
			}
			if CategoryOf(off) == CatOcean && !window {
				at("the ocean, the charts or the sea map, off a room change")
			}
		}
		if rel, ok := loadedSlot(w.a, w.table); ok {
			isSwitch := rel >= addr.MemBitOffSwitch && rel < addr.MemBitOffItem
			switch {
			case isSwitch && liveSwitches && !rep.Gates.Idle:
				at("a live switch, not IDLE")
			case isSwitch && !liveSwitches && rep.State != StateArmed:
				at("a switch of the loaded table, not armed")
			case isSwitch && !liveSwitches:
				if why := sw.check(w.tick, retried); why != "" {
					at("a switch of the loaded table, %s", why)
				}
			case !isSwitch && !rep.Gates.Idle:
				at("the loaded table's progress, not IDLE")
			}
			if why := youngWrite(w.young, w.execs); isSwitch && why != "" {
				at("a switch of the loaded table while %s", why)
			}
		}
		p := place{a: w.a}
		if w.a >= addr.LiveMemBit && w.a < addr.LiveMemBit+addr.MemBitSize {
			p.table = w.table
		}
		if again := set[p] & (w.val &^ w.old); again != 0 {
			at("bits 0x%02X set again", again)
		}
		set[p] |= w.val &^ w.old
	}
}

func TestReconcileAsksForSwitches(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want hub.Feature
	}{
		{"writing", Config{Apply: true}, hub.FeatureImage | hub.FeatureSwitches | hub.FeatureLiveSwitches | hub.M4Features | hub.FeaturePuzzle},
		{"a dry run asks for what M2 did", Config{}, hub.FeatureImage},
		{"Want wins", Config{Apply: true, Want: hub.FeatureImage | hub.FeaturePuzzle}, hub.FeatureImage | hub.FeaturePuzzle},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := newRecRig(t, c.cfg, hub.FeatureImage)
			if got := hub.Feature(rr.hello.Want); got != c.want {
				t.Fatalf("CO_HELLO wants %s, want %s", got, c.want)
			}
			if st := rr.r.Status(); st.DryRun == c.cfg.Apply {
				t.Fatalf("Status.DryRun = %v with Apply %v", st.DryRun, c.cfg.Apply)
			}
		})
	}
}

// TestReconcileStageProgress: the loaded table's chests, pickups, rooms
// and dungeon items go to the live memBit and the saved slot when IDLE;
// another table's to its saved slot alone, as soon as STABLE. Both survive
// putSave and getSave, and none goes back up as a gain.
func TestReconcileStageProgress(t *testing.T) {
	rr := newRecRig(t, Config{Apply: true}, hub.FeatureImage|hub.FeatureSwitches)
	seaChest, seaM := chest(addr.TableSea, 3)
	drcChest, drcM := chest(addr.TableDRC, 7)
	drcRoom, drcRoomM := visited(addr.TableDRC, 2)
	drcItems := addr.OffMemBit(addr.TableDRC) + addr.MemBitOffDungeonItem
	fwPickup := addr.OffMemBit(addr.TableFW) + addr.MemBitOffItem + 1
	var img world.Image
	img[seaChest], img[drcChest], img[drcRoom], img[drcItems], img[fwPickup] = seaM, drcM, drcRoomM, 0x07, 0x40
	rr.world(img)

	rep := rr.tick()
	if rep.Wrote != 6 || rep.WriteErr != nil || rep.Waiting != 0 {
		t.Fatalf("first tick: %+v\n%s", rep, rr.log)
	}
	if rr.liveByte(seaChest)&seaM == 0 || rr.savedByte(seaChest)&seaM == 0 {
		t.Fatal("the loaded table's chest is not in both copies")
	}
	for _, c := range []struct {
		off  int
		mask byte
	}{{drcChest, drcM}, {drcRoom, drcRoomM}, {drcItems, 0x07}, {fwPickup, 0x40}} {
		if rr.savedByte(c.off)&c.mask != c.mask {
			t.Errorf("%s: saved slot 0x%02X", DefaultLabel(c.off, -1), rr.savedByte(c.off))
		}
		_, rel, _ := memBitTable(c.off)
		if v := rr.read(addr.LiveMemBit+uint32(rel), 1)[0]; v&c.mask != 0 {
			t.Errorf("%s: written to the live memBit, which holds the sea's table", DefaultLabel(c.off, -1))
		}
	}
	if !rr.log.has("wrote 6 bytes") {
		t.Errorf("log lacks the writes:\n%s", rr.log)
	}
	rep = rr.tick()
	if rep.Verify.Verified != 6 || rep.Verify.Failed != 0 || rep.Wrote != 0 || rep.Planned != 0 {
		t.Fatalf("second tick: %+v", rep)
	}
	rr.noGainsUp("after the writes")

	// While an event runs, the loaded table's progress waits; another
	// table's does not.
	seaChest2, seaM2 := chest(addr.TableSea, 4)
	drcChest2, drcM2 := chest(addr.TableDRC, 8)
	img[seaChest2], img[drcChest2] = seaM2, drcM2
	rr.g.SetEvent(1, 1)
	rr.world(img)
	rep = rr.tick()
	if rep.Wrote != 1 || rep.Waiting != 1 || rr.savedByte(drcChest2)&drcM2 == 0 {
		t.Fatalf("during the event: %+v", rep)
	}
	if rr.ticks(5) != 0 || rr.liveByte(seaChest2)&seaM2 != 0 {
		t.Fatal("the loaded table's chest was written during the event")
	}
	rr.g.SetEvent(0, 0)
	if rep := rr.tick(); rep.Wrote != 2 || rr.liveByte(seaChest2)&seaM2 == 0 || rr.savedByte(seaChest2)&seaM2 == 0 {
		t.Fatalf("after the event: %+v", rep)
	}

	// Into the dungeon and back: getSave and putSave carry everything,
	// and nothing is written again.
	before := len(rr.bm.written())
	rr.load(drc)
	rr.ticks(gate.StableTicks + 2)
	for _, c := range []struct {
		off  int
		mask byte
	}{{drcChest, drcM}, {drcChest2, drcM2}, {drcRoom, drcRoomM}, {drcItems, 0x07}} {
		if rr.liveByte(c.off)&c.mask != c.mask {
			t.Errorf("in the dungeon, %s's live copy lacks 0x%02X", DefaultLabel(c.off, -1), c.mask)
		}
	}
	rr.load(outset)
	rr.ticks(gate.StableTicks + 2)
	if got := rr.image(); got[seaChest]&seaM == 0 || got[seaChest2]&seaM2 == 0 {
		t.Fatal("back in Outset, a chest is gone")
	}
	if n := len(rr.bm.written()) - before; n != 0 {
		t.Fatalf("%d writes across the stage changes, want none", n)
	}
	rr.noGainsUp("after the stage changes")
	rr.audit(false)
}

// TestReconcileSwitchWaitsForRoomChange: a memory switch of another table
// goes to its saved slot as soon as STABLE; one of the loaded table waits
// for the receiver's next room change and is written on the first STABLE
// tick after it on which every shown room's actors have had the room
// hold's roomSettle frames, at least gate.StableTicks later. Once the
// receiver's game clears it, it is never written again; a save state
// starts that over.
func TestReconcileSwitchWaitsForRoomChange(t *testing.T) {
	rr := newRecRig(t, Config{Apply: true}, hub.FeatureImage|hub.FeatureSwitches)
	seaSw, m := switchImageByte(addr.TableSea, 0x05)
	drcSw, _ := switchImageByte(addr.TableDRC, 0x05)
	off10, m10 := switchImageByte(addr.TableSea, 0x10) // not latching: outside the policy
	var img world.Image
	img[seaSw], img[drcSw], img[off10] = m, m, img[off10]|m10
	rr.world(img)

	rep := rr.tick()
	if rep.Wrote != 1 || rr.savedByte(drcSw)&m == 0 {
		t.Fatalf("the cold table's switch: %+v", rep)
	}
	if rep.Waiting != 1 || rr.liveByte(seaSw)&m != 0 || rr.savedByte(seaSw)&m != 0 {
		t.Fatalf("the loaded table's switch did not wait: %+v", rep)
	}
	if !rr.log.has("waits for the next room or stage change") {
		t.Errorf("log lacks the wait:\n%s", rr.log)
	}
	if n := rr.ticks(20); n != 0 {
		t.Fatalf("%d bytes written with no room change", n)
	}

	rr.enter(7, 6) // rooms 6 and 7 get zones: their actors are new
	changed := len(rr.reps)
	w := rr.untilWrite(gate.StableTicks + rr.holdTicks())
	young := rr.bm.youngAt()
	switch {
	case w < 0:
		t.Fatalf("not written after the room change:\n%s", rr.log)
	case w-changed < gate.StableTicks:
		t.Fatalf("written %d ticks after the room change, want at least %d", w-changed, gate.StableTicks)
	case !rr.reps[w].SwitchWindow:
		t.Fatalf("the writing tick is not the memory switches' window: %+v", rr.reps[w])
	case young[w] < roomSettle:
		t.Fatalf("written when the newest room's actors had run %d frames, want the room hold's %d", young[w], roomSettle)
	}
	for i := changed; i < w; i++ {
		if rr.reps[i].Gates.CanWrite() && young[i] >= roomSettle {
			t.Fatalf("tick %d was STABLE with every room settled before the write on %d: not the first", i, w)
		}
	}
	if rr.liveByte(seaSw)&m == 0 || rr.savedByte(seaSw)&m == 0 || rr.liveByte(off10)&m10 != 0 {
		t.Fatal("the switch is not in both copies, or the unlatching one was written")
	}
	rr.noGainsUp("after the switch")

	// The game clears it before the next tick reads it (it is ever-had
	// because the reconciler wrote it), and it stays cleared through room
	// and stage changes, though the world still has it. The writer reads
	// the clear as a verify failure: one fault, far below the Breaker's.
	if err := rr.g.OffSwitch(0x05, -1); err != nil {
		t.Fatal(err)
	}
	rr.ticks(3)
	if st := rr.r.Status(); st.Writes.Failed != 1 || st.WritesOff != "" {
		t.Fatalf("after the game's clear: %+v, off %q", st.Writes, st.WritesOff)
	}
	for _, move := range []func(){
		func() { rr.enter(8) },
		func() { rr.enter(9, 10) },
		func() { rr.splitLoad(drc) },
		func() { rr.splitLoad(outset) },
		func() { rr.load(drc) },
		func() { rr.load(outset) },
		func() { rr.enter(2) },
	} {
		move()
		if n := rr.ticks(gate.StableTicks + 3); n != 0 {
			t.Fatalf("%d bytes written after the game cleared the switch\n%s", n, rr.log)
		}
	}
	if rr.liveByte(seaSw)&m != 0 {
		t.Fatal("the cleared switch came back")
	}
	rr.audit(false)

	// A save state: its game never had what came after it, so the world's
	// switch is applied again, at the next room change.
	rr.store(addr.SpawnTrigger, []byte{0, 0, 0, 1})
	rr.ticks(gate.StableTicks + 3)
	if rr.liveByte(seaSw)&m != 0 {
		t.Fatal("written after the save state, before a room change")
	}
	rr.enter(3)
	if w := rr.untilWrite(gate.StableTicks + rr.holdTicks()); w < 0 || rr.liveByte(seaSw)&m == 0 {
		t.Fatalf("not applied again after the save state and a room change:\n%s", rr.log)
	}
}

// A room change seen before the session could write the deferred
// writes, before the hub's WORLD came or before its welcome armed it,
// is not theirs: the loaded table's switch the first WORLD brings waits
// for the next room change, though the room hold of the change seen had
// not passed when the session armed.
func TestReconcileDropsAWindowSeenUnarmed(t *testing.T) {
	for _, c := range []struct {
		name string
		rig  func(t *testing.T) *recRig
	}{
		{"before the WORLD", func(t *testing.T) *recRig {
			rr := newRecRigNoWorld(t, Config{Apply: true}, hub.FeatureImage|hub.FeatureSwitches)
			rr.enter(7, 6)
			rr.ticks(gate.StableTicks + 4)
			return rr
		}},
		{"before the welcome", func(t *testing.T) *recRig {
			rr := newRecRigNoWelcome(t, Config{Apply: true})
			rr.enter(7, 6)
			rr.ticks(gate.StableTicks + 4)
			rr.welcome(hub.FeatureImage | hub.FeatureSwitches)
			return rr
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			rr := c.rig(t)
			sw, m := switchImageByte(addr.TableSea, 0x05)
			var img world.Image
			img[sw] = m
			rr.world(img) // the session's first
			if n := rr.ticks(rr.holdTicks() + 20); n != 0 {
				t.Fatalf("%d bytes written in a room change window seen unarmed\n%s", n, rr.log)
			}
			rr.enter(8)
			if w := rr.untilWrite(gate.StableTicks + rr.holdTicks()); w < 0 || rr.liveByte(sw)&m == 0 {
				t.Fatalf("not written after the next room change:\n%s", rr.log)
			}
			rr.audit(false)
		})
	}
}

// The deferred writes' windows are one tick each: the room change's, in
// which the ocean goes, and the memory switches', the first STABLE tick
// after it that the room hold allows. A switch of the loaded table the
// world gains between the two is written in the second; one it gains
// after that waits for the next room change.
func TestReconcileWindowIsOneTick(t *testing.T) {
	rr := newRecRig(t, Config{Apply: true, Policy: switchPolicy(t, 0x05, 0x06, 0x07)}, hub.FeatureImage|hub.FeatureSwitches)
	var img world.Image
	img[addr.OffOcean+5] = 0x04
	sw, m5 := switchImageByte(addr.TableSea, 0x05)
	_, m6 := switchImageByte(addr.TableSea, 0x06)
	_, m7 := switchImageByte(addr.TableSea, 0x07)
	img[sw] = m5
	rr.world(img)
	rr.enter(7, 6)
	w := rr.untilWrite(gate.StableTicks + 4)
	if w < 0 || !rr.reps[w].RoomChange || rr.reps[w].SwitchWindow || rr.reps[w].Wrote != 1 || rr.liveByte(sw) != 0 {
		t.Fatalf("the room change's tick wrote more than the ocean, or nothing:\n%s", rr.log)
	}
	img[sw] |= m6
	rr.world(img)
	w = rr.untilWrite(rr.holdTicks())
	if w < 0 || !rr.reps[w].SwitchWindow || rr.liveByte(sw) != m5|m6 {
		t.Fatalf("the switches' tick did not write both switches:\n%s", rr.log)
	}
	img[sw] |= m7
	rr.world(img)
	if n := rr.ticks(rr.holdTicks()); n != 0 || rr.liveByte(sw)&m7 != 0 {
		t.Fatalf("%d bytes written after the windows closed\n%s", n, rr.log)
	}
	rr.enter(8)
	if w := rr.untilWrite(gate.StableTicks + rr.holdTicks()); w < 0 || rr.liveByte(sw)&m7 == 0 {
		t.Fatalf("not written at the next room change:\n%s", rr.log)
	}
	rr.audit(false)
}

// switchPolicy is the default policy with switches nos of the sea and DRC
// tables granted, as the M1 table would mark latching ones.
func switchPolicy(t *testing.T, nos ...int) *world.Policy {
	t.Helper()
	p := world.DefaultPolicy()
	for _, tb := range []int{addr.TableSea, addr.TableDRC} {
		for _, no := range nos {
			if err := p.GrantSwitch(tb, no); err != nil {
				t.Fatal(err)
			}
		}
	}
	return p
}

// TestReconcileLiveSwitches: with --coop-live-switches the loaded table's
// switches go when IDLE, with no room change, once the room hold allows:
// after a room change into rooms whose actors are new, they have their
// roomSettle frames first.
func TestReconcileLiveSwitches(t *testing.T) {
	rr := newRecRig(t, Config{Apply: true, Policy: switchPolicy(t, 0x05, 0x06)}, hub.FeatureImage|hub.FeatureSwitches|hub.FeatureLiveSwitches)
	rr.settleRooms()
	seaSw, m := switchImageByte(addr.TableSea, 0x05)
	_, m6 := switchImageByte(addr.TableSea, 0x06)
	var img world.Image
	img[seaSw] = m
	rr.g.SetPauseMenu(true)
	rr.world(img)
	if n := rr.ticks(5); n != 0 {
		t.Fatal("written with the pause menu open")
	}
	rr.g.SetPauseMenu(false)
	if rep := rr.tick(); rep.Wrote != 2 || rep.RoomChange || rr.liveByte(seaSw)&m == 0 {
		t.Fatalf("live switch: %+v", rep)
	}

	rr.enter(7, 6)
	img[seaSw] |= m6
	rr.world(img)
	w := rr.untilWrite(gate.StableTicks + rr.holdTicks())
	if young := rr.bm.youngAt(); w < 0 || young[w] < roomSettle || rr.liveByte(seaSw)&m6 == 0 {
		t.Fatalf("the live switch after the room change: written on tick %d, the newest room's actors at %v frames\n%s", w, young[max(w, 0)], rr.log)
	}
	if !rr.log.has("waits for the room hold") {
		t.Errorf("log lacks the hold:\n%s", rr.log)
	}
	rr.audit(true)
}

// TestReconcileSwitchRoomHold: Link comes through a door into a room
// whose shutter waits on a memory switch of the loaded table, and the
// partner's latching switch lands right after the room change. A shutter
// ignores a switch that changes in its first 30 executes
// (fakegame.Shutter; d_a_shutter.cpp:71-87, 133-142, 233-243), and a
// memory switch reaches it through the live memBit as a dan or zone
// switch does (dSv_info_c::isSwitch, d_save.cpp:1586-1603). The deferred
// write used to come on the first STABLE tick after the change, and the
// live one on the first IDLE tick, both gate.StableTicks ticks on, when
// the shutter had run a few frames: it stayed shut with its switch set.
// Both now wait for the room hold, and the shutter opens. An event or the
// pause menu after the door, whose frames no actor executes, makes the
// write wait longer.
func TestReconcileSwitchRoomHold(t *testing.T) {
	// door is Link coming through a door from room from into room to: the
	// door shows room to (d_door.cpp:282), whose actors are created on
	// the next frame, Link enters it, and the door hides room from behind
	// him (d_door.cpp:305).
	door := func(rr *recRig, from, to int) {
		rr.t.Helper()
		if err := rr.g.ShowRoom(to); err != nil {
			rr.t.Fatal(err)
		}
		rr.enter(to, from)
		if err := rr.g.HideRoom(from); err != nil {
			rr.t.Fatal(err)
		}
	}
	const no = 0x05 // latching in DRC's table (switch5Policy)
	for _, c := range []struct {
		name  string
		f     hub.Feature
		pause func(rr *recRig, on bool) // the game not IDLE right after the door, for 25 ticks
	}{
		{name: "deferred to the room change", f: hub.FeatureImage | hub.FeatureSwitches},
		{name: "live switches", f: hub.FeatureImage | hub.FeatureSwitches | hub.FeatureLiveSwitches},
		{name: "deferred, with an event after the door", f: hub.FeatureImage | hub.FeatureSwitches,
			pause: func(rr *recRig, on bool) {
				if on {
					rr.g.SetEvent(1, 0)
				} else {
					rr.g.SetEvent(0, 0)
				}
			}},
		{name: "live, with the pause menu after the door", f: hub.FeatureImage | hub.FeatureSwitches | hub.FeatureLiveSwitches,
			pause: func(rr *recRig, on bool) { rr.g.SetPauseMenu(on) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			rr := newRecRig(t, Config{Apply: true}, c.f)
			rr.load(drc)
			rr.enter(1, 2)
			sh := &fakegame.Shutter{Stage: drc.Name, Room: 1, No: no}
			rr.bm.setActors(sh)
			door(rr, 1, 2) // into room 2: room 1's shutter is deleted with its actors
			rr.settleRooms()
			door(rr, 2, 1) // and back: a new shutter in room 1
			if c.pause != nil {
				c.pause(rr, true)
			}
			off, m := switchImageByte(addr.TableDRC, no)
			var img world.Image
			img[off] = m
			rr.world(img) // the partner's, right after the room change
			if c.pause != nil {
				if n := rr.ticks(25); n != 0 {
					t.Fatalf("%d bytes written while the game was not idle", n)
				}
				c.pause(rr, false)
			}
			at := rr.untilWrite(gate.StableTicks + rr.holdTicks() + 2)
			if at < 0 || rr.liveByte(off)&m == 0 {
				t.Fatalf("never written:\n%s", rr.log)
			}
			rr.ticks(2)
			if err := rr.bm.actorErr(); err != nil {
				t.Fatal(err)
			}
			if !sh.Open() {
				t.Fatalf("the shutter stayed shut: written after its %d executes\n%s", sh.Executes(), rr.log)
			}
			rr.audit(c.f&hub.FeatureLiveSwitches != 0)
		})
	}
}

// TestReconcileNoSwitchesUngranted: without FeatureSwitches no memory
// switch is written, of any table.
func TestReconcileNoSwitchesUngranted(t *testing.T) {
	rr := newRecRig(t, Config{Apply: true}, hub.FeatureImage)
	seaSw, m := switchImageByte(addr.TableSea, 0x05)
	drcSw, _ := switchImageByte(addr.TableDRC, 0x05)
	var img world.Image
	img[seaSw], img[drcSw] = m, m
	rr.world(img)
	rr.ticks(3)
	rr.enter(5)
	if n := rr.ticks(gate.StableTicks + 3); n != 0 || len(rr.bm.written()) != 0 {
		t.Fatalf("%d bytes written without switch sync", n)
	}
}

// TestReconcileOceanAtRoomChange: the ocean, the charts and the sea map
// are written on the first STABLE tick after a room or stage change.
func TestReconcileOceanAtRoomChange(t *testing.T) {
	for _, move := range []struct {
		name string
		do   func(rr *recRig)
	}{
		{"a room change", func(rr *recRig) { rr.enter(45) }},
		{"a stage change", func(rr *recRig) { rr.load(drc) }},
	} {
		t.Run(move.name, func(t *testing.T) {
			rr := newRecRig(t, Config{Apply: true}, hub.FeatureImage|hub.FeatureSwitches)
			var img world.Image
			img[addr.OffOcean+5] = 0x04
			img[addr.OffMapGet+2] = 0x10
			img[addr.OffMapOpen+2] = 0x10
			img[addr.OffFmapBits+3] = 0x01
			img[addr.OffMapTriforce] = 0x80
			rr.world(img)
			if n := rr.ticks(10); n != 0 {
				t.Fatalf("%d bytes written before a room change", n)
			}
			move.do(rr)
			if w := rr.untilWrite(gate.StableTicks + 4); w < 0 || rr.reps[w].Wrote != 5 || !rr.reps[w].RoomChange {
				t.Fatalf("after %s: %+v\n%s", move.name, rr.reps[len(rr.reps)-1], rr.log)
			}
			got := rr.image()
			for off := range img {
				if got[off]&img[off] != img[off] {
					t.Errorf("%s: 0x%02X, want 0x%02X", DefaultLabel(off, -1), got[off], img[off])
				}
			}
			rr.audit(false)
		})
	}
}

// TestReconcileLaterClasses: the story bits, registers, inventory and
// capacities the planner plans are M4's, written only while the hub
// grants their features: with FeatureImage alone nothing of them is
// written, and the log says why.
func TestReconcileLaterClasses(t *testing.T) {
	rr := newRecRig(t, Config{Apply: true, Policy: world.FullPolicy()}, hub.FeatureImage)
	zelda, zm := eventBit(evZeldaAwakened)
	var img world.Image
	img[zelda] = zm
	img[addr.OffGetItem+2] = 0x01
	img[addr.OffWalletSize] = 2
	rr.world(img)
	rr.ticks(gate.StableTicks + 2)
	rr.enter(4)
	rr.ticks(gate.StableTicks + 2)
	if ws := rr.bm.written(); len(ws) != 0 {
		t.Fatalf("wrote %+v", ws)
	}
	if !rr.log.has("inventory is not written to this game: the session does not grant inventory") {
		t.Fatalf("log lacks the M4 lines:\n%s", rr.log)
	}
}

// TestReconcileTransitionStorm runs the receiver through stage loads
// across tables (putSave, getSave, initDan) and far room changes
// (zoneCountCheck's clearRoomSwitch and removeZone), with a tick or two
// between some and an event in the middle, while the world has M3
// progress in every table. No write clears a bit, lands while a gate is
// closed or sets a bit twice, nothing goes back up, and afterwards the
// game has all of it.
func TestReconcileTransitionStorm(t *testing.T) {
	rr := newRecRig(t, Config{Apply: true}, hub.FeatureImage|hub.FeatureSwitches)
	var img world.Image
	for _, tb := range []int{addr.TableSea, addr.TableDRC, addr.TableFW} {
		for no := range 6 {
			off, m := chest(tb, no*5)
			img[off] |= m
			off, m = visited(tb, no*3)
			img[off] |= m
		}
		img[addr.OffMemBit(tb)+addr.MemBitOffItem+2] |= 0x81
		img[addr.OffMemBit(tb)+addr.MemBitOffDungeonItem] |= 0x23
	}
	for _, tb := range []int{addr.TableSea, addr.TableDRC} {
		off, m := switchImageByte(tb, 0x05)
		img[off] |= m
	}
	img[addr.OffOcean+9] = 0x30
	img[addr.OffMapGet] = 0x81
	img[addr.OffFmapBits+20] = 0x01
	rr.world(img)

	storm := []struct {
		move  func()
		ticks int
	}{
		{func() { rr.load(drc) }, 1},
		{func() { rr.enter(3, 4) }, 2},
		{func() { rr.enter(9) }, gate.StableTicks + 2},
		{func() { rr.enter(12) }, 1},
		{func() { rr.enter(20) }, 1},
		{func() { rr.load(drcBoss) }, gate.StableTicks + 2},
		{func() { rr.g.SetEvent(1, 0) }, gate.StableTicks + 2},
		{func() { rr.load(woods) }, 2},
		{func() { rr.g.SetEvent(0, 0) }, 1},
		{func() { rr.enter(1, 2) }, gate.StableTicks + 2},
		{func() { rr.load(outset) }, 1},
		{func() { rr.load(drc) }, 3},
		{func() { rr.enter(30) }, 1},
		{func() { rr.load(outset) }, gate.StableTicks + 2},
		{func() { rr.enter(45, 44) }, gate.StableTicks + 2},
	}
	for i := 0; i < 3; i++ {
		for _, s := range storm {
			s.move()
			rr.ticks(s.ticks)
			rr.noGainsUp("during the storm")
		}
	}
	rr.ticks(gate.StableTicks + 2)
	now := rr.image()
	got := world.Shared(&now)
	for off := range img {
		if got[off]&img[off] != img[off] {
			t.Errorf("%s: 0x%02X, want 0x%02X", DefaultLabel(off, -1), got[off], img[off])
		}
	}
	if st := rr.r.Status(); st.WritesOff != "" || st.Writes.Failed != 0 || len(st.Planned) != 0 {
		t.Fatalf("after the storm: off %q, %+v, %d planned", st.WritesOff, st.Writes, len(st.Planned))
	}
	rr.audit(false)
	if len(rr.bm.written()) == 0 {
		t.Fatal("nothing was written")
	}
}

// TestReconcileDryRunWritesNothing: under --coop-dry-run the same world
// and storm write nothing, and the plan is logged as M2 did.
func TestReconcileDryRunWritesNothing(t *testing.T) {
	rr := newRecRig(t, Config{}, hub.FeatureImage|hub.FeatureSwitches)
	seaChest, m := chest(addr.TableSea, 3)
	sw, sm := switchImageByte(addr.TableSea, 0x05)
	var img world.Image
	img[seaChest], img[sw], img[addr.OffOcean] = m, sm, 0x01
	rr.world(img)
	for _, move := range []func(){
		func() {},
		func() { rr.enter(5) },
		func() { rr.load(drc) },
		func() { rr.load(outset) },
	} {
		move()
		rr.ticks(gate.StableTicks + 2)
	}
	if ws := rr.bm.written(); len(ws) != 0 {
		t.Fatalf("the dry run wrote %+v", ws)
	}
	for _, rep := range rr.reps {
		if rep.Wrote != 0 || rep.WriteErr != nil {
			t.Fatalf("a dry-run tick reports writes: %+v", rep)
		}
	}
	if !rr.log.has("dry-run: would set") || !rr.log.has("nothing is written") || !rr.log.has("dry run: planned writes are logged") {
		t.Fatalf("log lacks the dry-run lines:\n%s", rr.log)
	}
	if st := rr.r.Status(); !st.DryRun || len(st.Planned) == 0 {
		t.Fatalf("status %+v", st)
	}
}

// TestReconcileTurnOff: "coop off" stops the writes from the next tick,
// including a batch already waiting for the heartbeat, until TurnOn.
func TestReconcileTurnOff(t *testing.T) {
	rr := newRecRig(t, Config{Apply: true, BeatTimeout: 10 * time.Second}, hub.FeatureImage|hub.FeatureSwitches)
	c1, m1 := chest(addr.TableDRC, 1)
	var img world.Image
	img[c1] = m1
	rr.r.TurnOff("the user typed coop off")
	rr.world(img)
	for range 5 {
		if rep := rr.tick(); rep.Wrote != 0 || !errors.Is(rep.WriteErr, ErrOff) {
			t.Fatalf("while off: %+v", rep)
		}
	}
	if st := rr.r.Status(); st.WritesOff != "the user typed coop off" {
		t.Fatalf("WritesOff %q", st.WritesOff)
	}
	if len(rr.bm.written()) != 0 {
		t.Fatal("written while off")
	}
	rr.r.TurnOn()
	if rep := rr.tick(); rep.Wrote != 1 {
		t.Fatalf("after TurnOn: %+v", rep)
	}

	// A batch waiting for the heartbeat (emulation paused) stops at once.
	c2, m2 := chest(addr.TableDRC, 2)
	img[c2] = m2
	rr.world(img)
	rr.bm.setStall(true)
	done := make(chan Report, 1)
	go func() {
		rr.g.Frame()
		done <- rr.r.Tick()
	}()
	deadline := time.Now().Add(5 * time.Second)
	for rr.bm.stalledPolls() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the tick never waited for the heartbeat")
		}
		time.Sleep(time.Millisecond)
	}
	start := time.Now()
	rr.r.TurnOff("coop off")
	select {
	case rep := <-done:
		if rep.Wrote != 0 || !errors.Is(rep.WriteErr, ErrOff) {
			t.Fatalf("the waiting tick: %+v", rep)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("TurnOff did not stop the waiting batch")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("the waiting batch took %v to stop", d)
	}
	rr.bm.setStall(false)
	if rr.savedByte(c2)&m2 != 0 {
		t.Fatal("written after coop off")
	}
}

// TestReconcileStallKeepsTheWindow: a heartbeat stall on a deferred
// window's tick writes nothing and leaves its writes for the next STABLE
// tick, which is still that window's: the room change's, with the ocean,
// and then the memory switches', after the room hold.
func TestReconcileStallKeepsTheWindow(t *testing.T) {
	rr := newRecRig(t, Config{Apply: true, BeatTimeout: testBeat}, hub.FeatureImage|hub.FeatureSwitches)
	seaSw, m := switchImageByte(addr.TableSea, 0x05)
	var img world.Image
	img[seaSw] = m
	img[addr.OffOcean+5] = 0x04
	rr.world(img)
	rr.tick()
	rr.enter(6)
	rr.ticks(gate.StableTicks)
	rr.bm.setStall(true)
	rep := rr.tick()
	rr.bm.setStall(false)
	if !rep.RoomChange || !errors.Is(rep.WriteErr, ErrStall) || rep.Wrote != 0 {
		t.Fatalf("the stalled room-change tick: %+v", rep)
	}
	if rep := rr.tick(); !rep.RoomChange || rep.SwitchWindow || rep.Wrote != 1 {
		t.Fatalf("the next tick: %+v", rep)
	}
	rr.bm.setStall(true)
	for range rr.holdTicks() {
		if rep = rr.tick(); rep.SwitchWindow {
			break
		}
	}
	rr.bm.setStall(false)
	if !rep.SwitchWindow || !errors.Is(rep.WriteErr, ErrStall) || rep.Wrote != 0 {
		t.Fatalf("the stalled switches' tick: %+v", rep)
	}
	if rep := rr.tick(); !rep.SwitchWindow || rep.Wrote != 2 || rr.liveByte(seaSw)&m == 0 {
		t.Fatalf("the next tick: %+v", rep)
	}
	rr.audit(false)
}

// TestReplicaWorldAfterGains: GAINS the hub relayed before the replica's
// first WORLD arrived (an out-of-order delivery the protocol survives) are
// kept when the older WORLD lands: the M3 loopback lost a partner's ocean
// bit this way, so the receiver never wrote it.
func TestReplicaWorldAfterGains(t *testing.T) {
	rr := newRecRigNoWorld(t, Config{Apply: true}, hub.FeatureImage)
	off := addr.OffOcean + 0x51
	say(t, rr.r, &wire.GainsDown{Seq: 5, Origin: 1, Gains: []wire.Gain{{Off: uint16(off), Val: 0x01}}})
	say(t, rr.r, worldOf(4, world.Image{}))
	if v, seq := rr.view(); v[off] != 0x01 || seq != 5 {
		t.Fatalf("view byte 0x%02X at seq %d: the older WORLD replaced the GAINS", v[off], seq)
	}
	var later world.Image
	later[off+1] = 0x02
	say(t, rr.r, worldOf(5, later))
	if v, _ := rr.view(); v[off] != 0 || v[off+1] != 0x02 {
		t.Fatalf("a WORLD at the view's seq is the world: byte 0x%02X, 0x%02X", v[off], v[off+1])
	}
}

// view is the replica's view of the world and its seq.
func (rr *recRig) view() (world.Image, uint64) {
	rr.r.mu.Lock()
	defer rr.r.mu.Unlock()
	return rr.r.view, rr.r.viewSeq
}

// stagType sets the loaded STAG chunk's stage type. The fake keeps one
// STAG chunk for every stage and writes only its mProp, so the type stays
// until it is set again.
func (rr *recRig) stagType(typ byte) {
	rr.t.Helper()
	rr.store(fakegame.StagChunk+stagOffType, []byte{typ})
}

// TestReconcileHoldsBossBitsInABossStage: the partner beats Gohma while
// the receiver is in the boss room itself (M_DragB, a BOSS-type stage of
// DRC's table). Its boss bits are polled there every frame
// (d_meter.cpp:1071, d_a_dr2.cpp:561), so the world's "boss defeated" and
// "demo seen" wait; the map and compass go. Once the receiver leaves for
// the dungeon (the same table, not a boss stage) they are written, live
// and saved; leaving for the sea instead puts them in DRC's saved slot.
func TestReconcileHoldsBossBitsInABossStage(t *testing.T) {
	items := addr.OffMemBit(addr.TableDRC) + addr.MemBitOffDungeonItem
	for _, c := range []struct {
		name  string
		leave fakegame.Stage
		live  bool // the table stays loaded
	}{
		{"into the dungeon", drc, true},
		{"out to the sea", outset, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			rr := newRecRig(t, Config{Apply: true}, hub.FeatureImage)
			rr.load(drcBoss)
			rr.stagType(stageTypeBoss)
			rr.ticks(gate.StableTicks + 2)
			var img world.Image
			img[items] = 0x2B // map, compass, boss defeated, demo seen
			rr.world(img)
			rr.ticks(10)
			if v := rr.liveByte(items); v != 0x03 || rr.savedByte(items) != 0x03 {
				t.Fatalf("in the boss stage: live 0x%02X, saved 0x%02X; want only the map and compass", v, rr.savedByte(items))
			}
			if st := rr.r.Status(); len(st.Held) != 1 || st.Held[0].Mask != 0x28 || !rr.log.has("holding") {
				t.Fatalf("held %+v; log:\n%s", st.Held, rr.log)
			}

			before := len(rr.bm.written())
			rr.load(c.leave)
			rr.stagType(1) // dStageType_DUNGEON_e, or the sea's: no boss stage
			if w := rr.untilWrite(gate.StableTicks + 4); w < 0 {
				t.Fatalf("the boss bits were not written after leaving:\n%s", rr.log)
			}
			if rr.savedByte(items) != 0x2B {
				t.Fatalf("DRC's saved dungeon items 0x%02X, want 0x2B", rr.savedByte(items))
			}
			if c.live && rr.liveByte(items) != 0x2B {
				t.Fatalf("DRC's live dungeon items 0x%02X, want 0x2B", rr.liveByte(items))
			}
			for _, w := range rr.bm.written()[before:] {
				if w.a == addr.LiveMemBit+addr.MemBitOffDungeonItem && !c.live {
					t.Fatalf("a live write of the sea's table: %+v", w)
				}
			}
			rr.audit(false)
		})
	}
}

// TestReconcileSaveStateOfAnotherFile is the save-state race end to end:
// the replica is armed on its bound file in Outset, and the world gains a
// chest of the loaded table (live and saved) and one of DRC's (its saved
// slot). As the tick waits for the beat, a save state of another, never
// bound file standing in Outset is loaded: before the writer's first
// heartbeat read or while it polls, its heartbeat behind, far ahead or
// where the game would be. Nothing is written that tick, nor after: the
// loaded file is not armed.
func TestReconcileSaveStateOfAnotherFile(t *testing.T) {
	const (
		first = 2 // a tick's heartbeat reads: snap.Read's, then the writer's first,
		poll  = 3 // then its polls
	)
	cases := []struct {
		name   string
		at     int
		frames int
		near   bool // the heartbeat put where the game is
	}{
		{"before the first read, behind", first, 1, false},
		{"before the first read, ahead", first, 100000, false},
		{"before the first read, where the game is", first, 1, true},
		{"while polling, behind", poll, 1, false},
		{"while polling, ahead", poll, 100000, false},
		{"while polling, where the game is", poll, 1, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := newRecRig(t, Config{Apply: true}, hub.FeatureImage)
			seaChest, seaM := chest(addr.TableSea, 3)
			drcChest, drcM := chest(addr.TableDRC, 7)
			var img world.Image
			img[seaChest], img[drcChest] = seaM, drcM
			solo := soloState(t, c.frames)
			_, rel, _ := memBitTable(seaChest)
			rr.world(img)
			before := len(rr.bm.written())
			var hb uint32
			rr.bm.onHeartbeat(func(n int) {
				switch n {
				case 1:
					hb, _ = mem.ReadU32(rr.m, addr.SpawnTrigger)
				case c.at:
					loadState(t, rr.m, solo)
					if c.near {
						if err := rr.m.Store(addr.SpawnTrigger, binary.BigEndian.AppendUint32(nil, hb)); err != nil {
							t.Error(err)
						}
					}
				}
			})
			rep := rr.tick()
			rr.bm.onHeartbeat(nil)
			if !errors.Is(rep.WriteErr, ErrAborted) || rep.Wrote != 0 {
				t.Fatalf("the tick: %+v, want ErrAborted and nothing written\n%s", rep, rr.log)
			}
			rr.ticks(gate.StableTicks + 4)
			if ws := rr.bm.written()[before:]; len(ws) != 0 {
				t.Fatalf("%d writes into the solo file: %+v", len(ws), ws)
			}
			if v := rr.read(addr.LiveMemBit+uint32(rel), 1)[0]; v != 0 || rr.savedByte(seaChest) != 0 || rr.savedByte(drcChest) != 0 {
				t.Fatalf("the solo file has the world's chests: live 0x%02X", v)
			}
			if st := rr.r.Status(); st.State == StateArmed {
				t.Fatalf("armed on the solo file: %+v", st)
			}
		})
	}
}
