package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/tablestest"
)

// M3 through the session plumbing (§9 M3): host and join --coop with
// writing replicas, the stage tables of --coop-iso, the console's escapes
// (§6.7) and the write audit over every byte the pipelines wrote.

// coBeat is the Mem a writing coop-sync pipeline runs over in these
// tests. The game runs a frame when the FrameWriter waits for the
// heartbeat: on a heartbeat read right after another (snap.Read reads it
// once, between other reads; waitBeat reads it in a loop). Each frame
// ends with the mod's warp handler. Every write is kept for the audit,
// with the bytes it replaced, the save table loaded and its tick.
type coBeat struct {
	*mem.FakeMem
	g *fakegame.FakeGame

	mu     sync.Mutex
	last   uint32 // the address of the last read
	tick   int    // the tick the writes belong to (coPlayer.step)
	writes []coWrite
	landed []replica.Pos // where each warp put Link
	places []coPlace     // where the game was as each tick began, by tick number
	// ages follows how long each room's actors have run, on every frame,
	// for the audit's room hold.
	ages fakegame.RoomAges
	// doors are the key doors of the game's stages, run on every frame
	// after the rooms' ages (TestCoopSessionM4). Set before the first
	// frame.
	doors []*fakegame.KeyDoor
	// enemies, if set, are the game's placed enemies (M6), run on every
	// frame; actErr is the first error a frame's actors returned (an
	// onActor that would halt the game).
	enemies *fakegame.Enemies
	actErr  error
}

// coPlace is where a fake game is, read from its memory as a tick begins:
// the audit's own record of room and stage changes, apart from what the
// replica made of them (Report.RoomChange).
type coPlace struct {
	stage   string
	table   int8 // mDan.mStageNo
	room    int8 // mStayNo
	pending bool // mNextStage.mEnable
	link    bool // mpPlayerPtr[0] != 0
}

// coPlaceOf reads where the game over m is.
func coPlaceOf(m mem.Mem) (coPlace, error) {
	var p coPlace
	name, err := m.Read(addr.CurStageName, addr.StageNameLen)
	if err != nil {
		return p, err
	}
	p.stage = strings.TrimRight(string(name), "\x00")
	b, err := m.Read(addr.DanStageNo, 1)
	if err != nil {
		return p, err
	}
	p.table = int8(b[0])
	if b, err = m.Read(addr.StayNo, 1); err != nil {
		return p, err
	}
	p.room = int8(b[0])
	if b, err = m.Read(addr.NextStageEnable, 1); err != nil {
		return p, err
	}
	p.pending = b[0] != 0
	link, err := mem.ReadU32(m, addr.PlayerPtr)
	p.link = link != 0
	return p, err
}

// coRoomWindows says which ticks may make room-change writes (§6.3), by
// the game's places alone: the first tick since the place last changed
// that is ARMED with STABLE, IDLE and HEARTBEAT open (IDLE since Appendix
// C item 129), or a later such tick when every one before it since the
// change found the heartbeat stalled or the game moved on
// (replica.ErrStall, replica.ErrAborted). The loaded table's
// memory switches wait for the room hold as well, so their tick can come
// later (coSwitchTicks).
func coRoomWindows(reps []replica.Report, places []coPlace) []bool {
	win := make([]bool, len(reps))
	due := false
	for i, rep := range reps {
		if i > 0 && i < len(places) && places[i] != places[i-1] {
			due = true
		}
		if !due || rep.State != replica.StateArmed || !rep.Gates.CanWriteIdle() {
			continue
		}
		win[i] = true
		if !errors.Is(rep.WriteErr, replica.ErrStall) && !errors.Is(rep.WriteErr, replica.ErrAborted) {
			due = false
		}
	}
	return win
}

// coSwitchTicks checks the ticks the loaded table's memory switches §6.3
// defers are written on, by the game's places alone and in write order:
// each comes after a change of place, and one tick since each change
// writes them, or a later one when every tick before it that wrote some
// since the change ended stalled or aborted (replica.ErrStall,
// replica.ErrAborted: its batch is tried again). Which tick that is the
// room hold decides, which the audit checks apart (fakegame.RoomAges).
type coSwitchTicks struct {
	reps    []replica.Report
	changes []int       // by tick, the last tick at or before it whose place changed, or -1
	last    map[int]int // by change, the last tick that wrote a switch
}

func newCoSwitchTicks(reps []replica.Report, places []coPlace) *coSwitchTicks {
	s := &coSwitchTicks{reps: reps, changes: make([]int, len(places)), last: map[int]int{}}
	c := -1
	for i := range places {
		if i > 0 && places[i] != places[i-1] {
			c = i
		}
		s.changes[i] = c
	}
	return s
}

// check returns why a write at tick is out of place, or "".
func (s *coSwitchTicks) check(tick int) string {
	c := s.changes[tick]
	if c < 0 {
		return "before any room change"
	}
	if lt, ok := s.last[c]; ok && lt != tick && !errors.Is(s.reps[lt].WriteErr, replica.ErrStall) && !errors.Is(s.reps[lt].WriteErr, replica.ErrAborted) {
		return fmt.Sprintf("on a second tick since the room change on tick %d (tick %d wrote them)", c, lt)
	}
	s.last[c] = tick
	return ""
}

// coWrite is one write a pipeline made.
type coWrite struct {
	tick     int
	a        uint32
	old, val []byte
	table    int8 // mDan.mStageNo as it was made
	// zone is the stage and the zone table as a write into the dan or
	// zone bytes was made (coScopedRegion); nil for any other.
	zone *coZoneAt
	// young is the room whose actors had run the fewest frames as the
	// write was made (fakegame.RoomAges; -1 for none), and execs how many.
	young, execs int
	// pid is, for a write on the fake's actor heap (M6), the process ID
	// of the actor whose block holds it, as the write was made.
	pid uint32
}

func newCoBeat(cg *coGame) *coBeat { return &coBeat{FakeMem: cg.m, g: cg.g} }

func (b *coBeat) Read(a uint32, n int) ([]byte, error) {
	b.mu.Lock()
	beat := a == mailbox.SpawnTrigger && b.last == mailbox.SpawnTrigger
	b.last = a
	b.mu.Unlock()
	if beat {
		b.frame()
	}
	return b.FakeMem.Read(a, n)
}

func (b *coBeat) Write(a uint32, d []byte) error {
	old, err := b.FakeMem.Read(a, len(d))
	if err != nil {
		return err
	}
	t, err := b.FakeMem.Read(addr.DanStageNo, 1)
	if err != nil {
		return err
	}
	var zone *coZoneAt
	if coScopedRegion(a) {
		z, err := coZoneAtOf(b.FakeMem)
		if err != nil {
			return err
		}
		zone = &z
	}
	if err := b.FakeMem.Write(a, d); err != nil {
		return err
	}
	young, execs := b.ages.Youngest()
	var pid uint32
	if coHeap(a) {
		block := fakegame.EnemyHeap + (a-fakegame.EnemyHeap)/coBlock*coBlock
		pid, _ = mem.ReadU32(b.FakeMem, block+addr.ActorOffBsPcID)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.writes = append(b.writes, coWrite{tick: b.tick, a: a, old: old, val: bytes.Clone(d), table: int8(t[0]), zone: zone, young: young, execs: execs, pid: pid})
	return nil
}

// coBlock is the fake's actor block (fakegame.Enemies).
const coBlock = 0x3000

// coHeap reports whether a is on the fake's actor heap.
func coHeap(a uint32) bool { return a >= fakegame.EnemyHeap && a < fakegame.EnemyHeap+0x1000000 }

// frame runs a game frame, with the rooms' ages, then the warp handler as
// multiplayer_update runs it, right after spawn_trigger++ whenever Link
// exists (multiplayer.c:341-345, 365-421): a warp_seq that differs from
// warp_ack puts Link at warp_x/y/z and acknowledges it.
func (b *coBeat) frame() {
	actors := []fakegame.Actor{&b.ages}
	for _, d := range b.doors {
		actors = append(actors, d)
	}
	if b.enemies != nil {
		actors = append(actors, b.enemies)
	}
	if err := b.g.FrameWith(actors...); err != nil {
		b.mu.Lock()
		if b.actErr == nil {
			b.actErr = err
		}
		b.mu.Unlock()
	}
	link, err := mem.ReadU32(b.FakeMem, addr.PlayerPtr)
	if err != nil || link == 0 {
		return
	}
	w, err := b.FakeMem.Read(mailbox.WarpSeq, 20)
	if err != nil || bytes.Equal(w[:4], w[4:8]) {
		return
	}
	f := func(i int) float32 { return math.Float32frombits(binary.BigEndian.Uint32(w[8+4*i:])) }
	if err := b.FakeMem.Store(replica.WarpAck, w[:4]); err != nil {
		panic(err)
	}
	b.mu.Lock()
	b.landed = append(b.landed, replica.Pos{X: f(0), Y: f(1), Z: f(2)})
	b.mu.Unlock()
}

// setTick starts tick n: its writes are recorded as n's, and where the
// game is now as n's place.
func (b *coBeat) setTick(n int) {
	p, err := coPlaceOf(b.FakeMem)
	if err != nil {
		panic(err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tick = n
	b.places = append(b.places[:min(n, len(b.places))], p)
}

func (b *coBeat) placesSeen() []coPlace {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]coPlace(nil), b.places...)
}

func (b *coBeat) written() []coWrite {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]coWrite(nil), b.writes...)
}

func (b *coBeat) warps() []replica.Pos {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]replica.Pos(nil), b.landed...)
}

// coM3 is what M3 writes of the image within pol: the OR bytes of the
// stage, switch and ocean categories that M3's allowlist covers.
type coM3 struct {
	pol  *world.Policy
	mask world.Image // per byte, the bits M3 converges
	offs []int       // the bytes with a bit in mask
	sw   []int       // of those, the memory switch bytes of the tables the stages use
}

func newCoM3(pol *world.Policy) *coM3 {
	g := replica.NewGuardFor(replica.M3Targets)
	m := &coM3{pol: pol}
	tables := map[int]bool{}
	for _, s := range []fakegame.Stage{coSea, coDRC, coDRCBoss, coWoods} {
		tables[s.Table] = true
	}
	for off := range addr.SaveImageSize {
		switch replica.CategoryOf(off) {
		case replica.CatStage, replica.CatSwitch, replica.CatOcean:
		default:
			continue
		}
		if class.Of(off).Class == class.Max {
			continue
		}
		if m.mask[off] = g.Mask(addr.SaveImage+uint32(off)) & pol[off] & world.SharedMask(off); m.mask[off] == 0 {
			continue
		}
		m.offs = append(m.offs, off)
		if t, ok := coSwitchTable(off); ok && tables[t] {
			m.sw = append(m.sw, off)
		}
	}
	return m
}

// coSwitchTable is the save table whose memory switches image byte off
// holds, if it holds any.
func coSwitchTable(off int) (int, bool) {
	for t := range addr.TableCount {
		if rel := off - addr.OffMemBit(t); rel >= addr.MemBitOffSwitch && rel < addr.MemBitOffItem {
			return t, true
		}
	}
	return 0, false
}

// of is img's M3 bytes, everything else zero.
func (m *coM3) of(img world.Image) world.Image {
	var out world.Image
	for _, off := range m.offs {
		out[off] = img[off] & m.mask[off]
	}
	return out
}

// play does one random thing in p's game: an M3 bit set as the game would
// (often a latching switch), or a room or stage change.
func (m *coM3) play(p *coPlayer, rng *rand.Rand) {
	p.t.Helper()
	pick := func(offs []int) {
		off := offs[rng.IntN(len(offs))]
		for {
			if b := byte(1) << rng.IntN(8); m.mask[off]&b != 0 {
				p.put(off, p.get(off)|b)
				return
			}
		}
	}
	switch k := rng.IntN(20); {
	case k < 3 && len(m.sw) > 0:
		pick(m.sw)
	case k < 12:
		pick(m.offs)
	case k < 16:
		if err := p.g.EnterRoom(rng.IntN(addr.RoomCount), rng.IntN(addr.RoomCount)); err != nil {
			p.t.Fatal(err)
		}
	default:
		stages := []fakegame.Stage{coSea, coDRC, coDRCBoss, coWoods}
		if err := p.g.LoadStage(stages[rng.IntN(len(stages))]); err != nil {
			p.t.Fatal(err)
		}
	}
}

// coAuditM3 is §8.1's write audit over every write p's pipeline made, by
// the tick reports: a binding tag only on a tick that wrote one; an
// escape's request bytes (mNextStage, warp_seq, warp_x/y/z) only on a
// tick whose escape went through, ARMED with STABLE, IDLE and HEARTBEAT;
// and every other write one byte inside M3's allowlist (so no PER_PLAYER,
// NEVER, DERIVED or TAG byte), setting bits and clearing none, ARMED with
// the gates open, no bit set twice at one place, the loaded table's bytes
// only when IDLE and its memory switches (no --coop-live-switches) only on
// the one tick since a room change that writes them (coSwitchTicks) and
// once every room's actors have run fakegame.ShutterFrames frames (the
// room hold, by fakegame.RoomAges), the ocean, charts and sea map only on
// a room-change tick, IDLE as well, which it works out from where the
// game was as each tick began (coRoomWindows, coSwitchTicks), not from the
// reports. From M4 a session's replicas write M4's targets too, which
// coAuditM4Write checks by M4's rules; the writes at the derive targets
// must be exactly those the derive path reports. From M5 they write the
// dan and zone switch bytes too (TargetScoped), which coAuditM5Write
// checks by M5's rules, and no dan or zone switch is set twice in one
// scope. It counts what it saw.
func coAuditM3(t *testing.T, p *coPlayer) (n coAuditCount) {
	t.Helper()
	places := p.beat.placesSeen()
	if len(places) != len(p.reps) {
		t.Fatalf("%s: %d ticks reported, %d places recorded", p.name, len(p.reps), len(places))
	}
	win := coRoomWindows(p.reps, places)
	sw := newCoSwitchTicks(p.reps, places)
	n.cats = map[replica.Category]int{}
	n.m4 = map[replica.Target]int{}
	m3 := replica.NewGuardFor(replica.M3Targets)
	m4 := replica.NewGuardFor(replica.M3Targets | replica.M4Targets)
	m5 := replica.NewGuardFor(replica.M3Targets | replica.M4Targets | replica.M5Targets)
	esc := replica.NewGuardFor(replica.TargetEscape)
	m6 := replica.NewGuardFor(replica.M6Targets)
	type place struct {
		a     uint32
		table int8
	}
	set := map[place]byte{}
	m4set := map[place]byte{} // M4's OR bytes, by address
	m5set := map[coSwitchAt]bool{}
	tb := p.s.cs.m1()
	for _, w := range p.beat.written() {
		bad := func(format string, args ...any) {
			t.Helper()
			t.Errorf("%s: tick %d, a write of % X at 0x%08X over % X (table %d): "+format,
				append([]any{p.name, w.tick, w.val, w.a, w.old, w.table}, args...)...)
		}
		if w.tick >= len(p.reps) {
			bad("made outside a tick")
			continue
		}
		rep, window := p.reps[w.tick], win[w.tick]
		switch {
		case w.a == addr.Reserve && len(w.val) == bind.TagSize:
			n.tags++
			if !rep.TagWritten {
				bad("a tag on a tick that wrote none")
			}
			continue
		case esc.Allows(w.a, len(w.val)):
			n.escapes++
			switch {
			case rep.Escape == 0 || rep.EscapeErr != nil:
				bad("an escape's bytes on a tick without one (%v, %v)", rep.Escape, rep.EscapeErr)
			case rep.State != replica.StateArmed:
				bad("an escape while %s", rep.State)
			case !rep.Gates.CanWriteIdle():
				bad("an escape with a gate closed: %s", rep.Gates.Why())
			}
			continue
		case m5.TargetOf(w.a) == replica.TargetScoped:
			for _, at := range coAuditM5Write(w, rep, tb, bad) {
				n.scoped = append(n.scoped, at)
				if addr.ClassifySwitch(at.no) == addr.SwitchRoom {
					continue // every room change clears a room switch
				}
				k := at
				if k.room < 0 {
					k.stage = "" // one mDan per save table (d_save.cpp:1225-1235)
				}
				if m5set[k] {
					bad("switch 0x%02X set again in its scope", at.no)
				}
				m5set[k] = true
			}
			continue
		case m6.TargetOf(w.a) == replica.TargetKills:
			n.kills = append(n.kills, coAuditM6Kill(w, rep, tb, bad)...)
			continue
		case coHeap(w.a):
			coAuditM6Heap(w, rep, bad)
			n.heap = append(n.heap, w)
			continue
		case m4.TargetOf(w.a)&replica.M4Targets != 0:
			tg := m4.TargetOf(w.a)
			n.m4[tg]++
			if tg&coDeriveTargets != 0 {
				n.derived++
			}
			if set := coAuditM4Write(w, tg, rep, places, bad); set != 0 {
				at := place{a: w.a}
				if again := m4set[at] & set; again != 0 {
					bad("bits 0x%02X set again", again)
				}
				m4set[at] |= set
			}
			continue
		}
		n.progress++
		if len(w.val) != 1 {
			bad("%d bytes: progress is written a byte at a time", len(w.val))
			continue
		}
		old, val := w.old[0], w.val[0]
		switch {
		case m3.Mask(w.a) == 0:
			bad("outside M3's allowlist")
		case old&^val != 0:
			bad("a clear")
		case old == val:
			bad("wrote what was there")
		case rep.State != replica.StateArmed:
			bad("while %s", rep.State)
		case !rep.Gates.CanWrite():
			bad("with a gate closed: %s", rep.Gates.Why())
		}
		if w.a >= addr.SaveImage && w.a < addr.SaveImage+addr.SaveImageSize {
			off := int(w.a - addr.SaveImage)
			n.cats[replica.CategoryOf(off)]++
			switch c := class.Of(off).Class; c {
			case class.PerPlayer, class.Never, class.Derived, class.Tag:
				bad("a %v byte", c)
			}
			if replica.CategoryOf(off) == replica.CatOcean && !window {
				bad("the ocean, the charts or the sea map, off a room change")
			}
		}
		rel, loaded := -1, false
		if w.a >= addr.LiveMemBit && w.a < addr.LiveMemBit+addr.MemBitSize {
			rel, loaded = int(w.a-addr.LiveMemBit), true
		} else if saved, err := addr.SavedMemBitAddr(int(w.table)); err == nil && w.a >= saved && w.a < saved+addr.MemBitSize {
			rel, loaded = int(w.a-saved), true
		}
		if w.a >= addr.LiveMemBit && w.a < addr.LiveMemBit+addr.MemBitSize && addr.ValidTable(int(w.table)) {
			n.cats[replica.CategoryOf(addr.OffMemBit(int(w.table))+rel)]++
		}
		if loaded {
			switch isSwitch := rel >= addr.MemBitOffSwitch && rel < addr.MemBitOffItem; {
			case isSwitch:
				n.deferred++
				if why := sw.check(w.tick); why != "" {
					bad("a memory switch of the loaded table, %s", why)
				}
				if !rep.Gates.Idle {
					bad("a memory switch of the loaded table, not IDLE")
				}
				if w.young >= 0 && w.execs < fakegame.ShutterFrames {
					bad("a memory switch of the loaded table while room %d's actors had run %d frames: a new shutter there would not see it (the room hold)", w.young, w.execs)
				}
			case !rep.Gates.Idle:
				bad("the loaded table's progress, not IDLE")
			}
		}
		at := place{a: w.a}
		if w.a >= addr.LiveMemBit && w.a < addr.LiveMemBit+addr.MemBitSize {
			at.table = w.table
		}
		if again := set[at] & (val &^ old); again != 0 {
			bad("bits 0x%02X set again", again)
		}
		set[at] |= val &^ old
	}
	if d := p.s.r.Status().Writes.Derived; n.derived != d {
		t.Errorf("%s: %d writes at the derive targets, %d made by the derive path", p.name, n.derived, d)
	}
	return n
}

// coAuditCount is what coAuditM3 saw: progress bytes (by category), of
// them the loaded table's memory switch bytes (deferred), escape writes
// and tag writes, M4's writes by target, those at the derive targets
// counted apart, and every switch the scoped path set, with its scope, in
// write order.
type coAuditCount struct {
	progress, deferred, escapes, tags int
	cats                              map[replica.Category]int
	m4                                map[replica.Target]int
	derived                           int
	scoped                            []coSwitchAt
	kills                             []coKillAt // M6's dead-actor bits written, in their scopes
	heap                              []coWrite  // M6's writes on the actor heap
}

// coRoomLoaded gives room of cg's stage collision: mStatus[room].mpBgW
// (d_stage.h:865), which the room's BG actor sets as it creates the
// room's dBgW (d_a_bg.cpp:213). The fake game has no BG actors.
func coRoomLoaded(t *testing.T, cg *coGame, room int) {
	t.Helper()
	a, err := addr.RoomStatusAddr(room)
	if err != nil {
		t.Fatal(err)
	}
	if err := cg.m.Store(a+0x110, u32b(0x8123_4560)); err != nil {
		t.Fatal(err)
	}
}

// coType types line on a console.
func coType(t *testing.T, w io.Writer, line string) {
	t.Helper()
	if _, err := io.WriteString(w, line+"\n"); err != nil {
		t.Fatal(err)
	}
}

// coNextStage is mNextStage in cg's game.
func coNextStage(cg *coGame) (name string, point int16, room, layer, enable int8) {
	b := cg.read(addr.NextStage, addr.NextStageSize)
	name, _, _ = strings.Cut(string(b[:addr.StageNameLen]), "\x00")
	return name, int16(binary.BigEndian.Uint16(b[addr.StageOffPoint:])), int8(b[addr.StageOffRoomNo]), int8(b[addr.StageOffLayer]), int8(b[addr.NextOffEnable])
}

// coPose is a pose-sync client of the session for player name, connected
// to the server at at, as feed.PuppetSync's onClient hands it over
// (withPoseClient).
func coPose(t *testing.T, cs *coopSession, name, at string) *network.Client {
	t.Helper()
	c := network.NewClient(name)
	if err := c.Connect(at); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Disconnect)
	cs.withPoseClient(name, nil)(c)
	return c
}

// TestCoopSessionM3 is M3's acceptance test through the --coop session
// plumbing: the host founds a world from A's file and runs A's pipeline
// in its own session; B joins from a session of its own; both take their
// stage tables from --coop-iso (the vanilla fixture) and write. Both
// games make M3 progress at random, latching switches among it, and move
// between stages and rooms while the connections deliver out of order,
// repeat messages and drop, and the host restarts from its world file.
// Afterwards:
//
//   - both games hold the world's M3 bytes (chests, pickups, rooms,
//     dungeon items, latching switches, ocean and charts), which the
//     world got from the games;
//   - the console's /refresh reloads A's area by its entrance, /warp
//     takes A to B on the same stage and B to A on another (its
//     entrance, then onto it), and an escape with a gate closed is not
//     made;
//   - /coop off stops A's writes within a tick, and /coop on resumes
//     them; a storm of save-state loads turns B's writes off, said once,
//     while its pipeline and its pose sync go on;
//   - the write audit (coAuditM3) holds over every byte either pipeline
//     wrote, and no PER_PLAYER, NEVER or TAG byte crossed the wire.
func TestCoopSessionM3(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a loopback server for a few seconds")
	}
	useTablesDir(t, t.TempDir())
	iso := writeImage(t, "ww.iso", tablestest.ISO(tablestest.Vanilla()))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // before the cleanups, which wait for the clients

	ga := newCoGame(t, "Link", coMine{life: 0x0051, rupee: 0x0123, magic: 0x11})
	gb := newCoGame(t, "Tetra", coMine{life: 0x0062, rupee: 0x0456, magic: 0x22})
	hostIn, hostKeys := coInput(t)
	h := &coHost{t: t, ctx: ctx, rep: &coLog{}, env: coopEnv{
		worlds:   t.TempDir(),
		backups:  t.TempDir(),
		userDirs: []string{coUserDir(t)},
		openMem:  func() (mem.Mem, func(), error) { return ga.m, func() {}, nil },
		tick:     func() { ga.g.Frames(3) },
		input:    hostIn,
		tables:   loadCoopStageTables,
	}}
	h.start(coopFlags{on: true, world: coopWorldNew, yes: true, iso: iso})
	defer h.stop()
	cs := h.cs
	// A's pipeline, and the console that types into it, stay in the
	// first session, as in host; a restarted hub's session reads nothing.
	h.env.input = strings.NewReader("")
	if cs.tables.hash() == 0 || !h.rep.has("stage tables") {
		t.Fatalf("the host has no stage tables:\n%s", h.rep)
	}
	pol := cs.tables.policy()
	m3 := newCoM3(pol)
	if len(m3.sw) == 0 {
		t.Fatal("the fixture's tables latch no memory switch of the tables the stages use: the test would not cover switches")
	}

	joinIn, joinKeys := coInput(t)
	jlog := &coLog{}
	jcs := startCoopSession(jlog, coopFlags{on: true, yes: true, iso: iso}, coopEnv{backups: t.TempDir(), userDirs: []string{coUserDir(t)}, input: joinIn, tables: loadCoopStageTables})
	if jcs.tables.hash() != cs.tables.hash() {
		t.Fatalf("the joiner's tables %016x are not the host's %016x", jcs.tables.hash(), cs.tables.hash())
	}
	for _, l := range []*coLog{h.rep, jlog} {
		if n := l.count("co-op: commands (type one and press Enter)"); n != 1 {
			t.Errorf("the console was explained %d times, want once:\n%s", n, l)
		}
	}
	audit := &coAudit{}
	a := newCoPlayerOver(t, ctx, h, cs, ga, newCoBeat(ga), "Link", &coLog{}, audit, 1)
	b := newCoPlayerOver(t, ctx, h, jcs, gb, newCoBeat(gb), "Tetra", &coLog{}, audit, 2)
	players := []*coPlayer{a, b}
	stepAll := func() {
		for _, p := range players {
			p.step()
		}
	}
	welcomes := func(p *coPlayer) int { return p.rep.count("welcome to world") }
	need := map[*coPlayer]int{}
	drop := func(ps ...*coPlayer) {
		for _, p := range ps {
			need[p] = welcomes(p) + 1
		}
	}
	waitArmed := func(why string) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for {
			stepAll()
			armed := true
			for _, p := range players {
				st := p.s.r.Status()
				armed = armed && st.State == replica.StateArmed && st.Features&hub.FeatureSwitches != 0 && welcomes(p) >= need[p]
			}
			if armed {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: not armed with switch sync: %+v / %+v\nhost:\n%s\nA:\n%s\nB:\n%s", why, a.s.r.Status(), b.s.r.Status(), h.rep, a.rep, b.rep)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitArmed("binding")
	for _, p := range players {
		if st := p.s.r.Status(); st.DryRun || st.Features&hub.FeatureLiveSwitches != 0 {
			t.Errorf("%s: dry run %v, features %s: want writes, and switches at a room change", p.name, st.DryRun, st.Features)
		}
	}

	// Play, with drops and a host restart.
	rng := rand.New(rand.NewPCG(2026, 1015))
	prev := h.world()
	const steps = 300
	for i := range steps {
		for _, p := range players {
			if rng.IntN(3) == 0 {
				m3.play(p, rng)
			}
			p.step()
		}
		switch i {
		case steps / 4:
			drop(a)
			a.s.client.Disconnect()
		case steps / 2:
			drop(a, b)
			h.restart()
		case 3 * steps / 4:
			drop(b)
			b.s.client.Disconnect()
		}
		cur := h.world()
		if why := coMonotone(&prev, &cur); why != "" {
			t.Fatalf("step %d: the world went backwards: %s", i, why)
		}
		prev = cur
		time.Sleep(time.Millisecond)
	}

	// Quiesce: no more play but room changes (the writes §6.3 defers wait
	// for one), until both games hold the world's M3 bytes. Link goes back
	// and forth between rooms 1 and 2, which keep their zones (the room he
	// came from does not count down, d_stage.cpp:250-267), so their actors
	// are not created again and the room hold, which the loaded table's
	// switches wait for, passes.
	converge := func(why string) {
		t.Helper()
		waitArmed(why)
		deadline := time.Now().Add(30 * time.Second)
		for i := 0; ; i++ {
			for _, p := range players {
				if i%20 == 19 {
					r := 1 + (i/20)%2
					if err := p.g.EnterRoom(r, 3-r); err != nil {
						t.Fatal(err)
					}
				}
				p.step()
			}
			w := m3.of(h.world())
			ok := true
			for _, p := range players {
				ok = ok && m3.of(p.image()) == w
			}
			if ok {
				return
			}
			if time.Now().After(deadline) {
				for _, p := range players {
					got := m3.of(p.image())
					t.Errorf("%s: %s's M3 bytes are not the world's: %s (status %+v)", why, p.name, coDiff(&got, &w), p.s.r.Status())
				}
				t.Fatalf("no convergence\nhost:\n%s\nA:\n%s\nB:\n%s", h.rep, a.rep, b.rep)
			}
			time.Sleep(time.Millisecond)
		}
	}
	converge("after the play")
	if w := m3.of(h.world()); w == (world.Image{}) {
		t.Fatal("the world has no M3 progress: the play made none")
	}
	for _, p := range players {
		if n := coAuditM3(t, p); n.progress == 0 {
			t.Errorf("%s wrote no progress: the partner's never reached its game", p.name)
		}
	}

	// The escapes. Both on the dungeon's stage: A in room 0, by the
	// entrance it came in by, B in room 3. B has seen the woods (the fake
	// game learns a stage's STAG data when it is first asked for).
	steady := func(why string, until func() bool) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for !until() {
			if time.Now().After(deadline) {
				t.Fatalf("%s\nA:\n%s\nB:\n%s", why, a.rep, b.rep)
			}
			stepAll()
			time.Sleep(2 * time.Millisecond)
		}
	}
	// escape types cmd on p's console until done, again whenever the
	// console says to try again (a gate was closed for a moment).
	escape := func(p *coPlayer, keys io.Writer, cmd string, done func() bool) {
		t.Helper()
		const again = "try again in a moment"
		tries := p.rep.count(again)
		coType(t, keys, cmd)
		deadline := time.Now().Add(15 * time.Second)
		for !done() {
			if time.Now().After(deadline) {
				t.Fatalf("%s for %s did not happen\nA:\n%s\nB:\n%s", cmd, p.name, a.rep, b.rep)
			}
			if n := p.rep.count(again); n > tries {
				tries = n
				coType(t, keys, cmd)
			}
			stepAll()
			time.Sleep(2 * time.Millisecond)
		}
	}
	partnerAt := func(p *coPlayer, stage string, point int16, room int8) func() bool {
		return func() bool {
			q := p.s.r.Status().Partner
			return q != nil && q.Stage.String() == stage && q.Point == point && q.Room == room
		}
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(b.g.LoadStage(coWoods))
	must(a.g.LoadStage(coDRC))
	must(b.g.LoadStage(coDRC))
	must(b.g.EnterRoom(3))
	steady("the partners never saw each other in the dungeon", func() bool {
		return partnerAt(a, coDRC.Name, 0, 3)() && partnerAt(b, coDRC.Name, 0, 0)()
	})

	// /refresh with the pause menu up is not made, and says to try again.
	a.g.SetPauseMenu(true)
	for range 3 {
		stepAll()
	}
	coType(t, hostKeys, "/refresh")
	steady("a refresh with the pause menu up was not refused", func() bool { return a.rep.has("/refresh for Link: not now") })
	a.g.SetPauseMenu(false)
	if _, _, _, _, enable := coNextStage(a.coGame); enable != 0 {
		t.Fatal("a refresh was written with the pause menu up")
	}

	// /refresh reloads A's area by its entrance.
	escape(a, hostKeys, "/refresh", func() bool { return a.rep.has("co-op: refresh: reloading M_NewD2 by point 0 into room 0, layer 0") })
	if name, point, room, layer, enable := coNextStage(a.coGame); name != coDRC.Name || point != 0 || room != 0 || layer != 0 || enable != 1 {
		t.Fatalf("the refresh left mNextStage %q point %d room %d layer %d enable %d", name, point, room, layer, enable)
	}
	must(a.g.ChangeStage())

	// /warp takes A onto B on the same stage, at the position pose sync
	// has of B, in a room A's game has loaded.
	posA := network.PlayerPosition{PosX: -800, PosY: 0, PosZ: 2500}
	posB := network.PlayerPosition{PosX: 1200, PosY: 30, PosZ: -450}
	pa, pb := coPose(t, cs, "Link", h.addr), coPose(t, jcs, "Tetra", h.addr)
	steady("pose sync never brought the partners' positions", func() bool {
		// Each sends again until the other hears it: the server relays a
		// position only to the clients connected when it comes in.
		if err := errors.Join(pa.SendPosition(&posA), pb.SendPosition(&posB)); err != nil {
			t.Fatal(err)
		}
		_, okA := cs.partnerPos("Link")()
		_, okB := jcs.partnerPos("Tetra")()
		return okA && okB
	})
	coRoomLoaded(t, a.coGame, 3)
	escape(a, hostKeys, "/warp", func() bool { return len(a.beat.warps()) == 1 })
	if w := a.beat.warps()[0]; w != (replica.Pos{X: posB.PosX, Y: posB.PosY, Z: posB.PosZ}) {
		t.Errorf("A warped to %+v, want B's %+v", w, posB)
	}
	if !a.rep.has("co-op: warp: warping to the partner at (1200, 30, -450) in room 3") {
		t.Errorf("the warp was not logged:\n%s", a.rep)
	}

	// /warp takes B to A on another stage: A's entrance, which B learned
	// from A's arrival, then onto A.
	woods := coWoods
	woods.Point, woods.Room = 3, 5
	must(a.g.LoadStage(woods))
	steady("B never saw A in the woods", partnerAt(b, woods.Name, 3, 5))
	escape(b, joinKeys, "/warp", func() bool {
		return b.rep.has("co-op: warp: going to kindan by point 3 into room 5, layer -1, the partner's entrance (as the partner came in by it), then onto the partner")
	})
	if name, point, room, layer, enable := coNextStage(b.coGame); name != woods.Name || point != 3 || room != 5 || layer != -1 || enable != 1 {
		t.Fatalf("B's warp left mNextStage %q point %d room %d layer %d enable %d", name, point, room, layer, enable)
	}
	must(b.g.ChangeStage())
	coRoomLoaded(t, b.coGame, 5)
	steady("B never followed A", func() bool { return len(b.beat.warps()) == 1 })
	if w := b.beat.warps()[0]; w != (replica.Pos{X: posA.PosX, Y: posA.PosY, Z: posA.PosZ}) {
		t.Errorf("B warped to %+v, want A's %+v", w, posA)
	}
	steady("the second step was not logged", func() bool {
		return b.rep.has("co-op: warp: warping to the partner at (-800, 0, 2500) in room 5")
	})

	// /coop off: A writes nothing from the tick it turned off on, while
	// B's progress still reaches the world; /coop on writes it.
	off, bit := -1, byte(0)
	for _, o := range m3.offs {
		if c := replica.CategoryOf(o); c == replica.CatStage && m3.mask[o]&^gb.get(o) != 0 {
			off, bit = o, (m3.mask[o]&^gb.get(o))&-(m3.mask[o]&^gb.get(o))
			break
		}
	}
	if off < 0 {
		t.Fatal("B has every stage byte already")
	}
	coType(t, hostKeys, "/coop off")
	steady("/coop off did not turn A's writes off", func() bool { return a.s.r.Status().WritesOff != "" })
	stepAll() // the tick the writes stopped within
	n := len(a.beat.written())
	b.put(off, b.get(off)|bit)
	steady("B's progress never reached the world", func() bool {
		w := h.world()
		return w[off]&bit != 0 && a.s.r.Status().Seq == h.cs.store.Seq()
	})
	for i := range 40 {
		if i%10 == 9 {
			must(a.g.EnterRoom(i%addr.RoomCount, 0))
		}
		stepAll()
	}
	if got := len(a.beat.written()); got != n {
		t.Fatalf("A made %d writes after /coop off", got-n)
	}
	if a.get(off)&bit != 0 {
		t.Fatal("B's progress reached A's game after /coop off")
	}
	if c := a.rep.count("co-op writes are off for Link: you typed /coop off. Pose sync goes on"); c != 1 {
		t.Errorf("the turn-off was said %d times, want once:\n%s", c, a.rep)
	}
	coType(t, hostKeys, "/coop on")
	converge("after /coop on")
	if a.get(off)&bit == 0 {
		t.Fatal("B's progress never reached A's game after /coop on")
	}

	// A storm of save-state loads turns B's writes off, said once; its
	// pipeline still follows the world, and pose sync goes on.
	for range replica.MaxRegressionsPerMinute + 3 {
		hb := binary.BigEndian.Uint32(gb.read(mailbox.SpawnTrigger, 4))
		if err := gb.m.Store(mailbox.SpawnTrigger, u32b(hb-100)); err != nil {
			t.Fatal(err)
		}
		for range 3 {
			stepAll()
		}
	}
	steady("no auto-disable", func() bool { return b.s.r.Status().WritesOff != "" })
	for range 20 {
		stepAll()
	}
	if c := b.rep.count("save-state loads in"); c != 1 || !b.rep.has("Pose sync goes on; type /coop on to write the world again.") {
		t.Errorf("the auto-disable was said %d times, want once, with what goes on:\n%s", c, b.rep)
	}
	n = len(b.beat.written())
	ga2 := -1
	for _, o := range m3.offs {
		if replica.CategoryOf(o) == replica.CatStage && m3.mask[o]&^ga.get(o) != 0 && o != off {
			ga2 = o
			break
		}
	}
	if ga2 >= 0 {
		v := m3.mask[ga2] &^ ga.get(ga2)
		a.put(ga2, a.get(ga2)|v&-v)
		steady("A's progress never reached B's view after the auto-disable", func() bool {
			return h.world()[ga2]&(v&-v) != 0 && b.s.r.Status().Seq == h.cs.store.Seq()
		})
	}
	for range 20 {
		stepAll()
	}
	if got := len(b.beat.written()); got != n {
		t.Errorf("B made %d writes after the auto-disable", got-n)
	}
	if st := b.s.r.Status(); st.State != replica.StateArmed {
		t.Errorf("B's pipeline stopped following the world: %+v", st)
	}
	if _, ok := jcs.partnerPos("Tetra")(); !ok {
		t.Error("B's pose sync stopped with its writes")
	}

	// The audit, over everything either pipeline wrote.
	cats := map[replica.Category]int{}
	deferred := 0
	for _, p := range players {
		n := coAuditM3(t, p)
		deferred += n.deferred
		if n.tags != 1 {
			t.Errorf("%s: %d tag writes, want the bind", p.name, n.tags)
		}
		if n.escapes == 0 {
			t.Errorf("%s: no escape write", p.name)
		}
		for c, k := range n.cats {
			cats[c] += k
		}
		t.Logf("%s: %d progress bytes %v, %d escape writes, %d tag writes", p.name, n.progress, n.cats, n.escapes, n.tags)
	}
	if cats[replica.CatStage] == 0 || cats[replica.CatSwitch] == 0 || cats[replica.CatOcean] == 0 {
		t.Errorf("the pipelines wrote %v: want stage progress, latching switches and the ocean", cats)
	}
	if deferred == 0 {
		t.Error("no memory switch of a loaded table was written: the room-change window and the room hold were not covered")
	}
	t.Logf("%d memory switch bytes of a loaded table written, after a room change and the room hold", deferred)
	for _, p := range players {
		if n := welcomes(p); n < 2 || !p.rep.has("armed offline") {
			t.Errorf("%s: %d welcomes, offline %v: the reconnects did not happen", p.name, n, p.rep.has("armed offline"))
		}
	}
	audit.mu.Lock()
	defer audit.mu.Unlock()
	for _, s := range audit.bad {
		t.Error(s)
	}
	if audit.worlds == 0 || audit.gains == 0 {
		t.Errorf("the audit saw %d WORLD and %d GAINS entries: the wire was not watched", audit.worlds, audit.gains)
	}
	t.Logf("world seq %d; %d WORLD images and %d gains audited", h.cs.store.Seq(), audit.worlds, audit.gains)
}

// TestCoopSessionFeatures: what a --coop session's replicas are granted,
// by its flags and its stage tables. Writing replicas with the host's
// tables get switch sync (switches at a room change, or IDLE with
// --coop-live-switches); without tables, or with --coop-dry-run (M2's
// replica, which asks for the image only), there is none.
func TestCoopSessionFeatures(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a loopback server")
	}
	useTablesDir(t, t.TempDir())
	iso := writeImage(t, "ww.iso", tablestest.ISO(tablestest.Vanilla()))
	cases := []struct {
		name      string
		f         coopFlags
		noTables  bool
		want, not hub.Feature
		dryRun    bool
	}{
		{"default", coopFlags{iso: iso}, false, hub.FeatureImage | hub.FeatureSwitches, hub.FeatureLiveSwitches, false},
		{"live switches", coopFlags{iso: iso, liveSwitches: true}, false, hub.FeatureSwitches | hub.FeatureLiveSwitches, 0, false},
		{"no tables", coopFlags{}, true, hub.FeatureImage, hub.FeatureSwitches | hub.FeatureLiveSwitches, false},
		{"dry run", coopFlags{iso: iso, dryRun: true, liveSwitches: true}, false, hub.FeatureImage, hub.FeatureSwitches | hub.FeatureLiveSwitches, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cg := newCoGame(t, "Link", coMine{life: 12})
			env := coopEnv{
				worlds:   t.TempDir(),
				backups:  t.TempDir(),
				userDirs: []string{coUserDir(t)},
				openMem:  func() (mem.Mem, func(), error) { return cg.m, func() {}, nil },
				tick:     func() { cg.g.Frames(3) },
				input:    strings.NewReader(""),
			}
			if !c.noTables {
				env.tables = loadCoopStageTables
			}
			h := &coHost{t: t, ctx: ctx, rep: &coLog{}, env: env}
			f := c.f
			f.on, f.world, f.yes = true, coopWorldNew, true
			h.start(f)
			defer h.stop()
			p := newCoPlayerOver(t, ctx, h, h.cs, cg, newCoBeat(cg), "Link", &coLog{}, &coAudit{}, 1)
			deadline := time.Now().Add(10 * time.Second)
			for p.s.r.Status().State != replica.StateArmed {
				if time.Now().After(deadline) {
					t.Fatalf("not armed: %+v\n%s\n%s", p.s.r.Status(), h.rep, p.rep)
				}
				p.step()
				time.Sleep(2 * time.Millisecond)
			}
			st := p.s.r.Status()
			if st.Features&c.want != c.want || st.Features&c.not != 0 || st.DryRun != c.dryRun {
				t.Errorf("features %s, dry run %v; want %s and not %s, dry run %v", st.Features, st.DryRun, c.want, c.not, c.dryRun)
			}
			if live := h.rep.has("--coop-live-switches"); live != (c.f.liveSwitches && !c.dryRun) {
				t.Errorf("the host log mentions --coop-live-switches: %v\n%s", live, h.rep)
			}
			if dry := h.rep.has("dry run: planned writes are logged"); dry != c.dryRun {
				t.Errorf("the host log says dry run: %v\n%s", dry, h.rep)
			}
			if c.noTables && h.rep.has("co-op: stage tables") {
				t.Errorf("a session without tables spoke of them:\n%s", h.rep)
			}
		})
	}
}
