package replica

import (
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/proto"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// salvageBase is where the fixture keeps its dSalvage_control_c: any MEM1
// range clear of the game's statics and the fake's actor heap serves, as
// the owner's solid heap does on the console.
const salvageBase uint32 = 0x80C00000

// salvageTable lays out a dSalvage_control_c holding ps, every other entry
// unregistered (mRoomNo -1, as init leaves it, d_salvage.cpp:20-27).
func salvageTable(ps ...SalvagePoint) []byte {
	b := make([]byte, addr.SalvageSize)
	for i := range addr.SalvageInfoCount {
		b[addr.SalvageOffInfo+i*addr.SalvageInfoSize+addr.SalvageInfoOffRoomNo] = 0xFF
	}
	for _, p := range ps {
		at := addr.SalvageOffInfo + p.Index*addr.SalvageInfoSize
		for k, v := range p.Pos {
			binary.BigEndian.PutUint32(b[at+4*k:], v)
		}
		b[at+addr.SalvageInfoOffRoomNo] = byte(p.Room)
		b[at+addr.SalvageInfoOffSaveNo] = p.Save
		b[at+addr.SalvageInfoOffKind] = p.Kind
		b[at+addr.SalvageInfoOffFlag] = p.Flag
		if p.Index < addr.SalvageSeaMax {
			b[addr.SalvageOffSeaNum]++
		} else {
			b[addr.SalvageOffRoomNum]++
		}
	}
	return b
}

// The fixture's points, in Outset's square (44) and the one beside it:
// a Big Octo's point (kind 2), a chart's (kind 0), a moonlight point (kind
// 6, an event bit), a plain one (kind 3) and one with no save bit (31).
var (
	ptOcto  = SalvagePoint{Index: 0, Pos: [3]uint32{0x45000000, 0, 0x45000000}, Room: 44, Save: 3, Kind: 2}
	ptChart = SalvagePoint{Index: 1, Pos: [3]uint32{0x46000000, 0, 0}, Room: 44, Save: 5, Kind: 0}
	ptMoon  = SalvagePoint{Index: 2, Pos: [3]uint32{0, 0, 0x46000000}, Room: 44, Save: 1, Kind: 6}
	ptPlain = SalvagePoint{Index: 3, Pos: [3]uint32{0x44000000, 0, 0x44000000}, Room: 45, Save: 2, Kind: 3}
	ptNone  = SalvagePoint{Index: 4, Pos: [3]uint32{0x43000000, 0, 0}, Room: 44, Save: addr.SalvageNoSave, Kind: 4}
)

func TestParseSalvage(t *testing.T) {
	ps, why := ParseSalvage(salvageTable(ptOcto, ptChart, ptMoon))
	if why != "" || len(ps) != 3 || ps[0] != ptOcto || ps[1] != ptChart || ps[2] != ptMoon {
		t.Fatalf("ParseSalvage = %+v, %q", ps, why)
	}
	for _, c := range []struct {
		name string
		edit func(b []byte)
	}{
		{"mSeaNum past MAX_SEA", func(b []byte) { b[addr.SalvageOffSeaNum] = addr.SalvageSeaMax + 1 }},
		{"mRoomNum past MAX_ROOM", func(b []byte) { b[addr.SalvageOffRoomNum] = addr.SalvageRoomMax + 1 }},
		{"mRoomNo past 63", func(b []byte) { b[addr.SalvageOffInfo+addr.SalvageInfoOffRoomNo] = 64 }},
		{"mRoomNo below -1", func(b []byte) { b[addr.SalvageOffInfo+addr.SalvageInfoOffRoomNo] = 0xFE }},
		{"mKind past 6", func(b []byte) { b[addr.SalvageOffInfo+addr.SalvageInfoOffKind] = 7 }},
	} {
		b := salvageTable(ptOcto)
		c.edit(b)
		if _, why := ParseSalvage(b); why == "" {
			t.Errorf("%s: taken", c.name)
		}
	}
	if _, why := ParseSalvage(make([]byte, 16)); why == "" {
		t.Error("a short read was taken")
	}
}

// TestSalvageKey: the key of each kind is the one end_salvage writes
// (d_a_salvage.cpp:503-527), and its image byte the one the game's setter
// sets: onOceanSvBit's u16 per grid, onCompleteCollectMap's bit no-1.
func TestSalvageKey(t *testing.T) {
	const tb = addr.TableSea
	for _, c := range []struct {
		p    SalvagePoint
		want LandedBit
		ok   bool
	}{
		{ptOcto, OceanKey(tb, 44, 3), true},
		{ptChart, ChartKey(tb, 5), true},
		{ptPlain, OceanKey(tb, 45, 2), true},
		{SalvagePoint{Room: 7, Save: 15, Kind: 4}, OceanKey(tb, 7, 15), true},
		{ptMoon, LandedBit{}, false},
		{ptNone, LandedBit{}, false},
		{SalvagePoint{Room: 3, Kind: 5}, LandedBit{}, false},
		{SalvagePoint{Room: 50, Save: 1, Kind: 2}, LandedBit{}, false}, // past grid 0x31
		{SalvagePoint{Save: 0, Kind: 0}, LandedBit{}, false},
		{SalvagePoint{Save: 129, Kind: 0}, LandedBit{}, false},
	} {
		got, ok := c.p.Key(tb)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: Key = %v %v, want %v %v", c.p, got, ok, c.want, c.ok)
		}
	}
	for grid := range addr.OceanGridCount {
		for bit := range 16 {
			k := OceanKey(tb, grid, bit)
			off, mask, ok := salvageBitOff(k)
			b, _ := addr.OceanBit(grid, bit)
			if !ok || off != addr.OffOcean+b.Off || mask != b.Mask {
				t.Fatalf("%v: image byte 0x%X mask 0x%02X, OceanBit says 0x%X 0x%02X", k, off, mask, addr.OffOcean+b.Off, b.Mask)
			}
		}
	}
	// Chart 1 is bit 0 of the first word, its last byte (onCompleteMap,
	// d_save.cpp; save/view.go reads it so); chart 33 the second word's.
	for no, want := range map[int]int{1: addr.OffMapComplete + 3, 8: addr.OffMapComplete + 3, 9: addr.OffMapComplete + 2, 33: addr.OffMapComplete + 7, 128: addr.OffMapComplete + 12} {
		if off, _, ok := salvageBitOff(ChartKey(tb, no)); !ok || off != want {
			t.Errorf("chart %d: image byte 0x%X, want 0x%X", no, off, want)
		}
	}
}

// TestLandOcean: an ocean or chart byte the merge path wrote and verified
// lands each bit it set as the key salvageBitOff maps back to that byte;
// another byte lands nothing.
func TestLandOcean(t *testing.T) {
	const tb = addr.TableSea
	now := time.Now()
	for grid := range addr.OceanGridCount {
		for bit := range 16 {
			b, _ := addr.OceanBit(grid, bit)
			w := &FrameWriter{landed: landedSet{ok: true, table: tb}}
			w.landOcean(addr.Ocean+uint32(b.Off), tb, b.Mask, now)
			if _, ok := w.landed.at[OceanKey(tb, grid, bit)]; !ok || len(w.landed.at) != 1 {
				t.Fatalf("grid %d bit %d landed %v", grid, bit, w.landed.at)
			}
		}
	}
	for no := 1; no <= 128; no++ {
		off, mask, _ := salvageBitOff(ChartKey(tb, no))
		w := &FrameWriter{landed: landedSet{ok: true, table: tb}}
		w.landOcean(addr.SaveImage+uint32(off), tb, mask, now)
		if _, ok := w.landed.at[ChartKey(tb, no)]; !ok || len(w.landed.at) != 1 {
			t.Fatalf("chart %d landed %v", no, w.landed.at)
		}
	}
	w := &FrameWriter{landed: landedSet{ok: true, table: tb}}
	w.landOcean(addr.MapOpen, tb, 0xFF, now) // charts deciphered: not salvaged
	w.landOcean(addr.Ocean+addr.OceanSize, tb, 0xFF, now)
	if len(w.landed.at) != 0 {
		t.Errorf("landed %v", w.landed.at)
	}
}

// TestCheckSalvage: the guard takes exactly the planned point's mFlag,
// gaining bit 0x01 alone, with the tick's gates open.
func TestCheckSalvage(t *testing.T) {
	r := newRig(t, "Link")
	s, err := state.Read(r.m, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	wg := NewWriteGuard(GuardConfig{Targets: TargetSalvage})
	wg.SetTick(Tick{Gates: settledGates(*s), Armed: true, Table: s.Table})
	p := ptOcto
	a := p.FlagAddr(salvageBase)
	if err := wg.checkSalvage(salvageBase, p, a, 0, 1); err == nil {
		t.Error("taken with no plan")
	}
	plan := func() {
		wg.SetSalvage(SalvageTick{Base: salvageBase, Points: map[int]SalvagePoint{p.Index: p}})
	}
	plan()
	if err := wg.checkSalvage(salvageBase, p, a, 0x04, 0x05); err != nil {
		t.Errorf("refused: %v", err)
	}
	moved := p
	moved.Pos[0]++
	other := p
	other.Index = 7
	for _, c := range []struct {
		name     string
		base     uint32
		p        SalvagePoint
		a        uint32
		old, val byte
	}{
		{"another byte", salvageBase, p, a - 1, 0, 1},
		{"another table", salvageBase + 0x100, p, p.FlagAddr(salvageBase + 0x100), 0, 1},
		{"a point moved", salvageBase, moved, a, 0, 1},
		{"a point not planned", salvageBase, other, other.FlagAddr(salvageBase), 0, 1},
		{"set already", salvageBase, p, a, 1, 1},
		{"another bit too", salvageBase, p, a, 0, 3},
		{"a clear", salvageBase, p, a, 1, 0},
	} {
		plan()
		if err := wg.checkSalvage(c.base, c.p, c.a, c.old, c.val); err == nil {
			t.Errorf("%s: taken", c.name)
		}
	}
	shut := NewWriteGuard(GuardConfig{Targets: RefreshTargets})
	shut.SetTick(Tick{Gates: settledGates(*s), Armed: true, Table: s.Table})
	shut.SetSalvage(SalvageTick{Base: salvageBase, Points: map[int]SalvagePoint{p.Index: p}})
	if err := shut.checkSalvage(salvageBase, p, a, 0, 1); err == nil {
		t.Error("a guard without TargetSalvage took it")
	}
	unarmed := NewWriteGuard(GuardConfig{Targets: TargetSalvage})
	unarmed.SetTick(Tick{Gates: settledGates(*s), Table: s.Table})
	unarmed.SetSalvage(SalvageTick{Base: salvageBase, Points: map[int]SalvagePoint{p.Index: p}})
	if err := unarmed.checkSalvage(salvageBase, p, a, 0, 1); err == nil {
		t.Error("taken unarmed")
	}
	if AllTargets&TargetSalvage == 0 || !strings.Contains(TargetSalvage.String(), "salvage") {
		t.Errorf("AllTargets %s", AllTargets)
	}
}

// salvageRig is a reconciling replica on the sea in Outset's square (44),
// its daSalvage_c executing when owner is set, its table at salvageBase
// holding ps, with no settle time.
func salvageRig(t *testing.T, owner bool, ps ...SalvagePoint) *recRig {
	t.Helper()
	rr := newRecRig(t, Config{Apply: true, RefreshSettle: -1}, proto.FeatureImage)
	rr.frames = 3
	if owner {
		placed := fakegame.PlaceEnemies(fakegame.Enemy{Stage: outset.Name, Room: -1, SetID: addr.ActorSetIDNone, Proc: stage.Proc(addr.ProcSalvage)})
		rr.bm.setActors(placed)
	}
	rr.load(outset)
	rr.enter(44, 45)
	rr.store(salvageBase, salvageTable(ps...))
	var p [4]byte
	binary.BigEndian.PutUint32(p[:], salvageBase)
	rr.store(addr.SalvageTagDataPtr, p[:])
	if n := rr.ticks(gate.StableTicks + 4); n != 0 {
		t.Fatalf("%d bytes written before any bit came\n%s", n, rr.log)
	}
	return rr
}

// salvageImage is the partner's world: the Big Octo's point (grid 44 bit
// 3) and the plain one (45, 2) salvaged, and chart 5.
func salvageImage() save.Image {
	var img save.Image
	for _, k := range []LandedBit{OceanKey(addr.TableSea, 44, 3), OceanKey(addr.TableSea, 45, 2), ChartKey(addr.TableSea, 5)} {
		off, mask, _ := salvageBitOff(k)
		img[off] |= mask
	}
	return img
}

func (rr *recRig) salvageFlag(p SalvagePoint) byte {
	return rr.read(p.FlagAddr(salvageBase), 1)[0]
}

// TestSalvageCatchUp: the partner's salvage bits land at the receiver's
// room change (OnRoomChange), and the registered points they empty are
// marked unusable on the ticks after; a moonlight point, a point with no
// save bit and one whose bit this game set itself are not touched.
func TestSalvageCatchUp(t *testing.T) {
	local := SalvagePoint{Index: 130, Pos: [3]uint32{1, 2, 3}, Room: 44, Save: 9, Kind: 3}
	rr := salvageRig(t, true, ptOcto, ptChart, ptMoon, ptPlain, ptNone, local)
	// This game's own salvage of local's bit: read set, never landed.
	off, mask, _ := salvageBitOff(OceanKey(addr.TableSea, 44, 9))
	rr.set(off, mask)
	rr.world(salvageImage())
	rr.ticks(2)
	rr.enter(45, 44)
	for range gate.StableTicks + 12 {
		rr.tick()
		if rr.r.Status().Salvage.Marked >= 3 {
			break
		}
	}
	rr.ticks(2) // the verify
	st := rr.r.Status()
	if st.Salvage.Marked != 3 || st.WritesOff != "" || st.Writes.Salvage != 3 {
		t.Fatalf("marked %d (writes %d), writes off %q\n%s", st.Salvage.Marked, st.Writes.Salvage, st.WritesOff, rr.log)
	}
	for _, p := range []SalvagePoint{ptOcto, ptChart, ptPlain} {
		if rr.salvageFlag(p)&addr.SalvageFlagUnusable == 0 {
			t.Errorf("%s not marked\n%s", p, rr.log)
		}
	}
	for _, p := range []SalvagePoint{ptMoon, ptNone, local} {
		if f := rr.salvageFlag(p); f != p.Flag {
			t.Errorf("%s: flag 0x%02X, not left 0x%02X", p, f, p.Flag)
		}
	}
	if !rr.log.has("salvage: marked salvage point 0 (sea grid 44 bit 3, kind 2) emptied") {
		t.Errorf("no log line\n%s", rr.log)
	}
	if n := rr.ticks(4); n != 0 {
		t.Errorf("%d more bytes written", n)
	}
}

// TestSalvageNoOwner: with no daSalvage_c executing, the table may be a
// deleted owner's, and nothing is written.
func TestSalvageNoOwner(t *testing.T) {
	rr := salvageRig(t, false, ptOcto)
	rr.world(salvageImage())
	rr.ticks(2)
	rr.enter(45, 44)
	rr.ticks(gate.StableTicks + 12)
	if f := rr.salvageFlag(ptOcto); f != 0 || rr.r.Status().Salvage.Marked != 0 {
		t.Fatalf("flag 0x%02X\n%s", f, rr.log)
	}
	if !rr.log.has("no daSalvage_c executes") {
		t.Errorf("no waiting line\n%s", rr.log)
	}
}

// TestSalvageBadTable: a null pointer or a table that does not look like
// one is never written.
func TestSalvageBadTable(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(rr *recRig)
		want string
	}{
		{"null", func(rr *recRig) { rr.store(addr.SalvageTagDataPtr, []byte{0, 0, 0, 0}) }, "mTagData_p is null"},
		{"garbage", func(rr *recRig) { rr.store(salvageBase+addr.SalvageOffSeaNum, []byte{0xEE}) }, "past MAX_SEA"},
	} {
		t.Run(c.name, func(t *testing.T) {
			rr := salvageRig(t, true, ptOcto)
			c.edit(rr)
			rr.world(salvageImage())
			rr.ticks(2)
			rr.enter(45, 44)
			rr.ticks(gate.StableTicks + 12)
			if f := rr.salvageFlag(ptOcto); f != 0 {
				t.Fatalf("flag 0x%02X\n%s", f, rr.log)
			}
			if !rr.log.has(c.want) {
				t.Errorf("no %q line\n%s", c.want, rr.log)
			}
		})
	}
}

// TestSalvageVerify: a mark the game stores over on the same point is a
// verify failure, a fault the Breaker counts; the point gone (init_one)
// is not.
func TestSalvageVerify(t *testing.T) {
	for _, gone := range []bool{false, true} {
		rr := salvageRig(t, true, ptOcto)
		rr.world(salvageImage())
		rr.ticks(2)
		rr.enter(45, 44)
		for range gate.StableTicks + 12 {
			if rr.tick(); rr.r.Status().Salvage.Marked == 1 {
				break
			}
		}
		if rr.r.Status().Salvage.Marked != 1 {
			t.Fatalf("not marked\n%s", rr.log)
		}
		at := addr.SalvageInfoAddr(salvageBase, ptOcto.Index)
		if gone {
			rr.store(at+addr.SalvageInfoOffRoomNo, []byte{0xFF})
		} else {
			rr.store(at+addr.SalvageInfoOffFlag, []byte{0})
		}
		rep := rr.tick()
		want := 1
		if gone {
			want = 0
		}
		if rep.Verify.Failed != want || rr.r.writer.WriteGuard().Breaker().Faults() != want {
			t.Errorf("gone %v: verify %+v\n%s", gone, rep.Verify, rr.log)
		}
	}
}
