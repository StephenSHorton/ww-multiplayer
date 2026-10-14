package replica

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/snap"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
)

// chest is chest no of table t as an image byte and mask.
func chest(t, no int) (int, byte) {
	b, err := addr.TboxBit(no)
	if err != nil {
		panic(err)
	}
	return addr.OffMemBit(t) + b.Off, b.Mask
}

// visited is room's visited bit of table t.
func visited(t, room int) (int, byte) {
	b, err := addr.VisitedRoomBit(room)
	if err != nil {
		panic(err)
	}
	return addr.OffMemBit(t) + b.Off, b.Mask
}

// eventBit is event 0xHHLL as an image byte and mask.
func eventBit(id uint16) (int, byte) {
	b := addr.EventByte(id)
	return addr.OffEvent + b.Off, b.Mask
}

func TestDifferRisingEdgesOnly(t *testing.T) {
	seaChest, seaMask := chest(addr.TableSea, 0)
	seaChest2, seaMask2 := chest(addr.TableSea, 9)
	drcChest, drcMask := chest(addr.TableDRC, 31)
	getItem := addr.OffGetItem + 2
	wallet := addr.OffWalletSize

	dr := newDifferRig(t, nil)
	steps := []struct {
		name string
		do   func()
		want []world.Gain
	}{
		{"baseline", nil, nil},
		{"a chest of the loaded table, in the live memBit", func() { dr.set(seaChest, seaMask) },
			[]world.Gain{{Off: uint16(seaChest), Val: seaMask}}},
		{"a chest of another table, in its saved slot", func() { dr.set(drcChest, drcMask) },
			[]world.Gain{{Off: uint16(drcChest), Val: drcMask}}},
		{"an item and a chest in one tick, in image order", func() {
			dr.set(seaChest2, seaMask2)
			dr.set(getItem, 0x10)
		}, sortGains([]world.Gain{{Off: uint16(getItem), Val: 0x10}, {Off: uint16(seaChest2), Val: seaMask2}})},
		{"the game clears a chest bit", func() { dr.clear(seaChest, seaMask) }, nil},
		{"nothing changes", nil, nil},
		{"the cleared bit is set again: a new rising edge", func() { dr.set(seaChest, seaMask) },
			[]world.Gain{{Off: uint16(seaChest), Val: seaMask}}},
		{"the wallet grows (MAX)", func() { dr.put(wallet, 2) }, []world.Gain{{Off: uint16(wallet), Val: 2}}},
		{"the wallet shrinks", func() { dr.put(wallet, 1) }, nil},
		{"every bit of a byte cleared at once", func() { dr.clear(getItem, 0xFF) }, nil},
		{"a memory switch: the default policy keeps it out", func() { dr.onSwitch(0x05, int(outset.Room)) }, nil},
		{"a story bit: the default policy keeps it out", func() { dr.g.OnEventBit(0x0F80) }, nil},
	}
	for _, s := range steps {
		if s.do != nil {
			s.do()
		}
		e := dr.tick()
		if !slices.Equal(e.Gains, s.want) {
			t.Errorf("%s: gains %s, want %s (note %q)", s.name, fmtGains(e.Gains), fmtGains(s.want), e.Note)
		}
	}
}

func sortGains(gs []world.Gain) []world.Gain {
	slices.SortFunc(gs, func(a, b world.Gain) int { return int(a.Off) - int(b.Off) })
	return gs
}

func TestDifferPolicy(t *testing.T) {
	sw, swMask := func() (int, byte) {
		_, b, err := addr.SwitchBit(0x05)
		if err != nil {
			t.Fatal(err)
		}
		return addr.OffMemBit(addr.TableSea) + b.Off, b.Mask
	}()
	ev, evMask := eventBit(0x0F80)

	dr := newDifferRig(t, world.FullPolicy())
	dr.tick()
	dr.onSwitch(0x05, int(outset.Room))
	dr.g.OnEventBit(0x0F80)
	want := sortGains([]world.Gain{{Off: uint16(sw), Val: swMask}, {Off: uint16(ev), Val: evMask}})
	if e := dr.tick(); !slices.Equal(e.Gains, want) {
		t.Errorf("full policy: gains %s, want %s", fmtGains(e.Gains), fmtGains(want))
	}

	// A policy that opens one bit of a byte passes that bit alone.
	p := world.DefaultPolicy()
	p.Grant(ev, evMask)
	dr = newDifferRig(t, p)
	dr.tick()
	dr.g.OnEventBit(0x0F80)
	dr.g.OnEventBit(0x0F40) // same byte, not granted
	if e := dr.tick(); !slices.Equal(e.Gains, []world.Gain{{Off: uint16(ev), Val: evMask}}) {
		t.Errorf("one granted bit: gains %s", fmtGains(e.Gains))
	}
}

// richState sets flags of every kind the resets touch, so a reset that
// leaked into an edge would show.
func richState(dr *differRig) {
	room := int(outset.Room)
	for _, no := range []int{0, 7, 31} {
		off, m := chest(addr.TableSea, no)
		dr.set(off, m)
		off, m = chest(addr.TableDRC, no)
		dr.set(off, m)
	}
	dr.set(addr.OffGetItem, 0xFF)
	dr.onSwitch(0x05, room) // memory switch, live memBit
	dr.onSwitch(0xC0, room) // zone switch
	dr.onSwitch(0xD3, room)
	dr.onSwitch(0xE0, room) // room switch
	dr.onSwitch(0xEF, room)
	if err := dr.g.OnActor(3, room); err != nil {
		dr.t.Fatal(err)
	}
	if err := dr.g.OnActor(200, room); err != nil {
		dr.t.Fatal(err)
	}
}

// TestDifferTransitionStorms runs the game's bulk resets (getSave and
// putSave on a stage change, initDan, clearRoomSwitch, removeZone and the
// zone wipe, card_to_memory) over a game full of flags and checks that
// none of them produces an edge: the differ never sends a clear, and
// nothing a reset brings back reads as a gain.
func TestDifferTransitionStorms(t *testing.T) {
	room := int(outset.Room)
	cases := []struct {
		name  string
		setup func(dr *differRig)
		storm []func(dr *differRig)
	}{
		{"getSave and putSave across tables", richState, []func(*differRig){
			func(dr *differRig) { dr.load(drc) },
			func(dr *differRig) { dr.load(outset) },
			func(dr *differRig) { dr.load(drcBoss) },
			func(dr *differRig) { dr.load(woods) },
			func(dr *differRig) { dr.load(drc) },
			func(dr *differRig) { dr.load(outset) },
		}},
		{"initDan", func(dr *differRig) {
			dr.load(drc)
			dr.onSwitch(0x80, 0)
			dr.onSwitch(0xBF, 0)
		}, []func(*differRig){
			func(dr *differRig) { dr.load(drcBoss) }, // same table: kept
			func(dr *differRig) { dr.load(outset) },  // another table: zeroed
			func(dr *differRig) { dr.load(drc) },     // and zeroed again
			func(dr *differRig) { dr.load(woods) },
		}},
		{"clearRoomSwitch", richState, []func(*differRig){
			func(dr *differRig) { dr.enter(room+1, room) },
			func(dr *differRig) { dr.enter(room, room+1) },
			func(dr *differRig) { dr.enter(room + 2) },
			func(dr *differRig) { dr.enter(room) },
		}},
		{"removeZone and a reused slot", richState, []func(*differRig){
			func(dr *differRig) { dr.enter(1) },
			func(dr *differRig) { dr.enter(2) },
			func(dr *differRig) { dr.enter(3) }, // room 44's zone is removed
			func(dr *differRig) { dr.enter(4) }, // and its slot reused
			func(dr *differRig) { dr.enter(5) },
			func(dr *differRig) { dr.enter(room) }, // a new zone for room 44
		}},
		{"a stage entry wipes every zone", richState, []func(*differRig){
			func(dr *differRig) { dr.load(outset) },
		}},
		{"a negative start point keeps zones, clears room switches", richState, []func(*differRig){
			func(dr *differRig) {
				dr.load(fakegame.Stage{Name: "sea", Point: -1, Room: outset.Room, Table: addr.TableSea})
			},
		}},
		{"a stage load half done, with Link gone", richState, []func(*differRig){
			func(dr *differRig) {
				if err := dr.g.SetNextStage(drc); err != nil {
					dr.t.Fatal(err)
				}
			},
			func(dr *differRig) {
				if err := dr.g.ChangeStage(); err != nil {
					dr.t.Fatal(err)
				}
			},
		}},
		{"save, file select and the same file back", richState, []func(*differRig){
			func(dr *differRig) {
				if err := dr.g.Save(); err != nil {
					dr.t.Fatal(err)
				}
			},
			func(dr *differRig) { dr.g.EnterFileSelect() },
			func(dr *differRig) {
				if err := dr.g.LoadFile(0); err != nil {
					dr.t.Fatal(err)
				}
			},
			func(dr *differRig) { dr.load(outset) },
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dr := newDifferRig(t, world.FullPolicy())
			c.setup(dr)
			dr.ticks(3) // baseline
			for i, step := range c.storm {
				step(dr)
				if e := dr.ticks(3); len(e.Gains) > 0 || len(e.Scoped) > 0 {
					t.Fatalf("step %d: gains %s, scoped %v", i, fmtGains(e.Gains), e.Scoped)
				}
			}
		})
	}
}

// TestDifferRandomStorm walks the game through random transitions, with
// no flag ever set, and checks that not one edge comes out.
func TestDifferRandomStorm(t *testing.T) {
	rng := rand.New(rand.NewPCG(23, 2))
	dr := newDifferRig(t, world.FullPolicy())
	richState(dr)
	dr.ticks(3)
	stages := []fakegame.Stage{outset, drc, drcBoss, woods}
	for i := range 400 {
		switch k := rng.IntN(10); {
		case k < 3:
			s := stages[rng.IntN(len(stages))]
			if rng.IntN(4) == 0 {
				s.Point = -1
			}
			dr.load(s)
		case k < 8:
			room := rng.IntN(addr.RoomCount)
			dr.enter(room, rng.IntN(addr.RoomCount))
		case k == 8:
			if err := dr.g.Save(); err != nil {
				t.Fatal(err)
			}
			dr.g.EnterFileSelect()
			if err := dr.g.LoadFile(0); err != nil {
				t.Fatal(err)
			}
			dr.load(stages[rng.IntN(len(stages))])
		default:
			dr.g.Frames(rng.IntN(5))
		}
		if e := dr.ticks(1 + rng.IntN(2)); len(e.Gains) > 0 || len(e.Scoped) > 0 {
			t.Fatalf("step %d: gains %s, scoped %v", i, fmtGains(e.Gains), e.Scoped)
		}
	}
}

// TestDifferRoomChangeKeepsImageBaseline checks the decomp-driven rule
// that the image is not re-baselined on a room change: the visited bit
// dMap_c::mapMoveAll sets as Link enters a room of a dungeon-type stage
// (d_map.cpp:1662-1665) is a gain like any other.
func TestDifferRoomChangeKeepsImageBaseline(t *testing.T) {
	dr := newDifferRig(t, nil)
	dr.load(drc)
	dr.ticks(2)
	dr.enter(3, 0)
	off, m := visited(addr.TableDRC, 3)
	dr.set(off, m)
	if e := dr.tick(); !slices.Equal(e.Gains, []world.Gain{{Off: uint16(off), Val: m}}) {
		t.Errorf("visited bit on entering a room: gains %s", fmtGains(e.Gains))
	}

	// The same on a stage change: the first Forbidden Woods room.
	dr.load(woods)
	off, m = visited(addr.TableFW, 0)
	dr.set(off, m)
	e := dr.tick()
	if !slices.Equal(e.Gains, []world.Gain{{Off: uint16(off), Val: m}}) {
		t.Errorf("visited bit on entering a stage: gains %s (note %q)", fmtGains(e.Gains), e.Note)
	}
}

// TestDifferFoldHazard puts the game in the middle of
// dStage_stagInfoInit: the new STAG is loaded and getSave has copied the
// new table's memBit into the live copy, but initDan has not yet moved
// mDan.mStageNo (d_stage.cpp:1670-1677). The fold would put the new
// table's bits in the old table's slot. Link's pointer is left set, as a
// stale one would be, so only the STAG check stands between the differ
// and those bogus gains.
func TestDifferFoldHazard(t *testing.T) {
	dr := newDifferRig(t, nil)
	off, m := chest(addr.TableSea, 2)
	dr.set(off, m)
	for _, no := range []int{0, 1, 5, 30} {
		o, mk := chest(addr.TableDRC, no)
		dr.set(o, mk)
	}
	dr.ticks(3)

	// putSave of sea, then the new STAG and getSave of DRC.
	savedSea, _ := addr.SavedMemBitAddr(addr.TableSea)
	savedDRC, _ := addr.SavedMemBitAddr(addr.TableDRC)
	dr.store(savedSea, dr.read(addr.LiveMemBit, addr.MemBitSize))
	dr.store(fakegame.StagChunk+addr.StagOffProp, []byte{byte(addr.TableDRC << 1)})
	dr.store(addr.LiveMemBit, dr.read(savedDRC, addr.MemBitSize))
	dr.g.Frame()

	// Without the check, this tick's image has DRC's chests in sea's
	// slot: the hazard is real.
	before := dr.d.base
	if o := dr.observeNow(); world.Diff(&before, &o.local) == nil || o.consistent() {
		t.Fatal("the half-loaded stage shows no fold hazard; the test is not testing anything")
	}
	if e := dr.tickNoFrame(); len(e.Gains) > 0 || !strings.Contains(e.Note, "stage load in progress") {
		t.Fatalf("half-loaded stage: gains %s, note %q", fmtGains(e.Gains), e.Note)
	}

	// initDan finishes the load: DRC's bits are in DRC's slot, and the
	// image is what it was.
	dr.store(addr.DanStageNo, []byte{addr.TableDRC})
	dr.store(addr.DanSwitch, make([]byte, 8))
	if e := dr.ticks(3); len(e.Gains) > 0 {
		t.Fatalf("after initDan: gains %s", fmtGains(e.Gains))
	}
}

// observeNow decodes a snapshot without running the differ.
func (dr *differRig) observeNow() *obs {
	dr.t.Helper()
	s, err := snap.Read(dr.m)
	if err != nil {
		dr.t.Fatal(err)
	}
	st := dr.gt.Observe(s)
	return observe(dr.m, s, st)
}

func TestDifferRebaseline(t *testing.T) {
	off, m := chest(addr.TableSea, 4)
	cases := []struct {
		name string
		do   func(dr *differRig)
		note string // what the first tick after says
	}{
		{"another file's name, without leaving the game", func(dr *differRig) {
			dr.store(addr.PlayerName, []byte("Tetra\x00"))
			dr.set(off, m)
		}, "new baseline"},
		{"file select", func(dr *differRig) { dr.g.EnterFileSelect() }, "not in game"},
		{"file select, then a card copy of this file with other progress", func(dr *differRig) {
			// A memory-card copy keeps slot, name and mDateIPL: only
			// leaving the game tells the files apart.
			dr.g.EnterFileSelect()
			b := dr.read(addr.SaveImage+uint32(off), 1) // card_to_memory writes the saved image
			dr.store(addr.SaveImage+uint32(off), []byte{b[0] | m})
		}, "not in game"},
		{"a save state (the heartbeat goes back)", func(dr *differRig) {
			dr.store(addr.SpawnTrigger, []byte{0, 0, 0, 1})
			dr.set(off, m)
		}, "heartbeat went backwards"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dr := newDifferRig(t, nil)
			dr.ticks(3)
			c.do(dr)
			e := dr.tickNoFrame()
			if len(e.Gains) > 0 || !strings.Contains(e.Note, c.note) {
				t.Fatalf("gains %s, note %q, want %q", fmtGains(e.Gains), e.Note, c.note)
			}
			if strings.HasPrefix(c.name, "file select") {
				dr.load(outset)
			}
			// Exactly one tick takes the new baseline, and says so: the
			// replica follows it with a WORLD.
			rebased := 0
			if e.Rebased {
				rebased++
			}
			for range 3 {
				e := dr.tick()
				if len(e.Gains) > 0 {
					t.Fatalf("after the re-baseline: gains %s", fmtGains(e.Gains))
				}
				if e.Rebased {
					rebased++
				}
			}
			if rebased != 1 {
				t.Fatalf("%d ticks took a new baseline, want 1", rebased)
			}
			// And the next real gain still comes through.
			o2, m2 := chest(addr.TableSea, 20)
			dr.set(o2, m2)
			if e := dr.tick(); !slices.Equal(e.Gains, []world.Gain{{Off: uint16(o2), Val: m2}}) {
				t.Fatalf("a gain after the re-baseline: %s", fmtGains(e.Gains))
			}
		})
	}
}

func TestDifferScoped(t *testing.T) {
	dr := newDifferRig(t, nil)
	dr.load(drc)
	dr.ticks(2)
	stage, _ := wire.MakeStage(drc.Name)
	tbl := int8(addr.TableDRC)
	scoped := func(k wire.ScopeKind, room int8, w uint8, bits uint32) wire.Scoped {
		return wire.Scoped{Kind: k, Stage: stage, Table: tbl, Room: room, Words: []wire.ScopedWord{{Word: w, Bits: bits}}}
	}
	steps := []struct {
		name string
		do   func()
		want []wire.Scoped
	}{
		{"dan switch 0x80", func() { dr.onSwitch(0x80, 0) }, []wire.Scoped{scoped(wire.ScopeDan, -1, 0, 1)}},
		{"dan switch 0xA1", func() { dr.onSwitch(0xA1, 0) }, []wire.Scoped{scoped(wire.ScopeDan, -1, 1, 1<<1)}},
		{"zone switch 0xC2", func() { dr.onSwitch(0xC2, 0) }, []wire.Scoped{scoped(wire.ScopeZone, 0, 0, 1<<2)}},
		{"zone switch 0xD0", func() { dr.onSwitch(0xD0, 0) }, []wire.Scoped{scoped(wire.ScopeZone, 0, 1, 1)}},
		{"room switch 0xE4", func() { dr.onSwitch(0xE4, 0) }, []wire.Scoped{scoped(wire.ScopeRoomEdge, 0, 2, 1<<4)}},
		{"actor 37 dies", func() {
			if err := dr.g.OnActor(37, 0); err != nil {
				t.Fatal(err)
			}
		}, []wire.Scoped{scoped(wire.ScopeKill, 0, 1, 1<<5)}},
		{"a room change clears the room switches: nothing", func() { dr.enter(1, 0) }, nil},
		{"a kill in room 0, now a neighbour", func() {
			if err := dr.g.OnActor(1, 0); err != nil {
				t.Fatal(err)
			}
		}, []wire.Scoped{scoped(wire.ScopeKill, 0, 0, 1<<1)}},
		{"the same table's next stage keeps the dan bits: nothing", func() { dr.load(drcBoss) }, nil},
	}
	for _, s := range steps {
		s.do()
		e := dr.tick()
		if !scopedEqual(e.Scoped, s.want) {
			t.Errorf("%s: scoped %v, want %v", s.name, e.Scoped, s.want)
		}
	}
}

func scopedEqual(a, b []wire.Scoped) bool {
	return slices.EqualFunc(a, b, func(x, y wire.Scoped) bool {
		return x.Kind == y.Kind && x.Stage == y.Stage && x.Table == y.Table && x.Room == y.Room && slices.Equal(x.Words, y.Words)
	})
}
