package replica

import (
	"encoding/binary"
	"math"
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
		binary.BigEndian.PutUint32(b[at+addr.SalvageInfoOffR:], math.Float32bits(p.R))
		binary.BigEndian.PutUint32(b[at+addr.SalvageInfoOffDist:], math.Float32bits(p.Dist))
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

// The fixture's points, as entry lays them out (d_salvage.cpp:53-60):
// Outset's square (room 44) in the room slots from mInfo[128], in the
// order its daSalvage_c actors register: a Big Octo's point (kind 2), a
// chart's (kind 0), a moonlight point (kind 6, an event bit), a plain one
// (kind 3) and one with no save bit (31). ptSea is a sea-level chart
// point (room 0, mInfo[0]). Link is far from all of them (mDistance past
// mR and the margin), as calcDistanceXZ leaves it.
const (
	farDist = 30000
	reach   = 400 // mR of kinds 2-4 at scale 1 (entry, d_salvage.cpp:136)
)

var (
	ptOcto  = SalvagePoint{Index: 128, Pos: [3]uint32{0x45000000, 0, 0x45000000}, R: reach, Dist: farDist, Room: 44, Save: 3, Kind: 2}
	ptChart = SalvagePoint{Index: 129, Pos: [3]uint32{0x46000000, 0, 0}, R: 700, Dist: farDist, Room: 44, Save: 5, Kind: 0}
	ptMoon  = SalvagePoint{Index: 130, Pos: [3]uint32{0, 0, 0x46000000}, R: reach, Dist: farDist, Room: 44, Save: 1, Kind: 6}
	ptPlain = SalvagePoint{Index: 131, Pos: [3]uint32{0x44000000, 0, 0x44000000}, R: reach, Dist: farDist, Room: 44, Save: 2, Kind: 3}
	ptNone  = SalvagePoint{Index: 132, Pos: [3]uint32{0x43000000, 0, 0}, R: reach, Dist: farDist, Room: 44, Save: addr.SalvageNoSave, Kind: 4}
	ptSea   = SalvagePoint{Index: 0, Pos: [3]uint32{0x47000000, 0, 0}, R: 700, Dist: farDist, Room: 0, Save: 7, Kind: 0}
)

func TestParseSalvage(t *testing.T) {
	ps, why := ParseSalvage(salvageTable(ptSea, ptOcto, ptChart, ptMoon))
	if why != "" || len(ps) != 4 || ps[0] != ptSea || ps[1] != ptOcto || ps[2] != ptChart || ps[3] != ptMoon {
		t.Fatalf("ParseSalvage = %+v, %q", ps, why)
	}
	at := func(i, off int) int { return addr.SalvageOffInfo + i*addr.SalvageInfoSize + off }
	for _, c := range []struct {
		name string
		edit func(b []byte)
	}{
		{"mSeaNum past MAX_SEA", func(b []byte) { b[addr.SalvageOffSeaNum] = addr.SalvageSeaMax + 1 }},
		{"mRoomNum past MAX_ROOM", func(b []byte) { b[addr.SalvageOffRoomNum] = addr.SalvageRoomMax + 1 }},
		{"mRoomNo past 63", func(b []byte) { b[at(128, addr.SalvageInfoOffRoomNo)] = 64 }},
		{"mRoomNo below -1", func(b []byte) { b[at(128, addr.SalvageInfoOffRoomNo)] = 0xFE }},
		{"a sea-level slot with a room", func(b []byte) { b[at(0, addr.SalvageInfoOffRoomNo)] = 44 }},
		{"a room slot with room 0", func(b []byte) { b[at(128, addr.SalvageInfoOffRoomNo)] = 0 }},
		{"two rooms in the room slots", func(b []byte) { b[at(131, addr.SalvageInfoOffRoomNo)] = 45 }},
	} {
		b := salvageTable(ptSea, ptOcto, ptPlain)
		c.edit(b)
		if _, why := ParseSalvage(b); why == "" {
			t.Errorf("%s: taken", c.name)
		}
	}
	if _, why := ParseSalvage(make([]byte, 16)); why == "" {
		t.Error("a short read was taken")
	}
	// A kind past 6 (getKind is param >> 28) is that entry left out, not
	// the table.
	odd := ptPlain
	odd.Kind = 9
	ps, skipped, why := parseSalvage(salvageTable(ptOcto, odd))
	if why != "" || skipped != 1 || len(ps) != 1 || ps[0] != ptOcto {
		t.Errorf("a kind 9 entry: %+v, %d skipped, %q", ps, skipped, why)
	}
}

// TestSalvageNear: a point Link is within its reach and the margin of,
// or whose distance is no distance, is near.
func TestSalvageNear(t *testing.T) {
	for _, c := range []struct {
		dist, r float32
		near    bool
	}{
		{farDist, reach, false},
		{reach + SalvageNearMargin, reach, false},
		{reach + SalvageNearMargin - 1, reach, true},
		{0, reach, true},
		{float32(math.NaN()), reach, true},
		{float32(math.Inf(1)), reach, true},
		{-5, reach, true},
		{farDist, float32(math.NaN()), true},
		{farDist, 2e6, true},
	} {
		p := ptOcto
		p.Dist, p.R = c.dist, c.r
		if p.near() != c.near {
			t.Errorf("mDistance %v, mR %v: near %v", c.dist, c.r, p.near())
		}
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
		{ptPlain, OceanKey(tb, 44, 2), true},
		{ptSea, ChartKey(tb, 7), true},
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
	rr.crane(-1)
	if n := rr.ticks(gate.StableTicks + 4); n != 0 {
		t.Fatalf("%d bytes written before any bit came\n%s", n, rr.log)
	}
	return rr
}

// crane stores id as mSalvageId: -1 the crane over no point (the play
// scene's create, phase_4), a point's index the one it is over.
func (rr *recRig) crane(id int32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(id))
	rr.store(addr.SalvageIDAddr, b[:])
}

// salvageImage is the partner's world: the Big Octo's point (grid 44 bit
// 3) and the plain one (44, 2) salvaged, and chart 5.
func salvageImage() save.Image {
	var img save.Image
	for _, k := range []LandedBit{OceanKey(addr.TableSea, 44, 3), OceanKey(addr.TableSea, 44, 2), ChartKey(addr.TableSea, 5)} {
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
	local := SalvagePoint{Index: 133, Pos: [3]uint32{1, 2, 3}, R: reach, Dist: farDist, Room: 44, Save: 9, Kind: 3}
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
	if !rr.log.has("salvage: marked salvage point 128 (sea grid 44 bit 3, kind 2) emptied") {
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

// landSalvage gives rr the partner's salvageImage and lands it with a room
// change, as OnRoomChange does.
func (rr *recRig) landSalvage() {
	rr.t.Helper()
	rr.world(salvageImage())
	rr.ticks(2)
	rr.enter(45, 44)
}

// untilMarked ticks until n points are marked, at most a settle's worth.
func (rr *recRig) untilMarked(n int) bool {
	rr.t.Helper()
	for range gate.StableTicks + 12 {
		if rr.tick(); rr.r.Status().Salvage.Marked >= n {
			return true
		}
	}
	return false
}

// TestSalvageCraneBusy: while mSalvageId is not -1 (the crane over a
// point: the salvage's order may be out and its event not started) no
// point is marked, the one it is over or another; at -1 they are.
func TestSalvageCraneBusy(t *testing.T) {
	for _, id := range []int32{int32(ptOcto.Index), int32(ptPlain.Index)} {
		rr := salvageRig(t, true, ptOcto, ptPlain)
		rr.crane(id)
		rr.landSalvage()
		rr.ticks(gate.StableTicks + 12) // the ocean bits land; no point is marked
		if st := rr.r.Status(); st.Salvage.Marked != 0 || st.Writes.Salvage != 0 || rr.salvageFlag(ptOcto) != 0 || rr.salvageFlag(ptPlain) != 0 {
			t.Fatalf("crane over %d: marked %d\n%s", id, st.Salvage.Marked, rr.log)
		}
		if !rr.log.has("the crane is over salvage point") {
			t.Errorf("crane over %d: no waiting line\n%s", id, rr.log)
		}
		rr.crane(-1)
		if !rr.untilMarked(2) {
			t.Errorf("crane gone from %d: marked %d\n%s", id, rr.r.Status().Salvage.Marked, rr.log)
		}
	}
}

// TestSalvageCraneBusyAtWrite: mSalvageId going from -1 to the planned
// point between the plan (the snapshot's heartbeat) and the write holds
// it: ApplySalvage reads it again after the heartbeat.
func TestSalvageCraneBusyAtWrite(t *testing.T) {
	rr := salvageRig(t, true, ptOcto)
	rr.landSalvage()
	held := false
	for range gate.StableTicks + 12 {
		// -1 at the snapshot (heartbeat read 1), so the plan is made; the
		// crane over the point from the writer's wait on.
		rr.bm.onHeartbeat(func(n int) {
			if n == 1 {
				rr.crane(-1)
			} else {
				rr.crane(int32(ptOcto.Index))
			}
		})
		rr.tick()
		if rr.salvageFlag(ptOcto) != 0 || rr.r.Status().Writes.Salvage != 0 {
			t.Fatalf("marked with the crane over the point\n%s", rr.log)
		}
		if st := rr.r.Status().Salvage; st.Held > 0 && strings.Contains(st.LastHeld, "the crane is over salvage point 128") {
			held = true
			break
		}
	}
	rr.bm.onHeartbeat(nil)
	if !held || rr.salvageFlag(ptOcto) != 0 {
		t.Fatalf("held %v, flag 0x%02X\n%s", held, rr.salvageFlag(ptOcto), rr.log)
	}
}

// f32 is v's big-endian bytes.
func f32(v float32) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], math.Float32bits(v))
	return b[:]
}

// TestSalvageNearHeld: a point Link is near is held, at the plan and when
// he comes near between the plan and the write, and marked once he is
// away.
func TestSalvageNearHeld(t *testing.T) {
	near := ptOcto
	near.Dist = 100
	rr := salvageRig(t, true, near)
	rr.landSalvage()
	rr.ticks(gate.StableTicks + 12) // the ocean bits land; the point is not marked
	if rr.salvageFlag(near) != 0 || rr.r.Status().Writes.Salvage != 0 {
		t.Fatalf("marked with Link beside the point\n%s", rr.log)
	}
	if st := rr.r.Status().Salvage; st.Held != 1 || !strings.Contains(st.LastHeld, "within its reach") {
		t.Errorf("status %+v", st)
	}
	dist := addr.SalvageInfoAddr(salvageBase, near.Index) + addr.SalvageInfoOffDist
	// Away at the plan, back at the write.
	rr.store(dist, f32(farDist))
	rr.bm.onHeartbeat(func(n int) {
		if n == 2 {
			rr.store(dist, f32(50))
		}
	})
	rr.tick()
	rr.bm.onHeartbeat(nil)
	if rr.salvageFlag(near) != 0 || rr.r.Status().Writes.Salvage != 0 {
		t.Fatalf("marked with Link back beside it\n%s", rr.log)
	}
	if st := rr.r.Status().Salvage; !strings.Contains(st.LastHeld, "now, within its reach") {
		t.Errorf("not held at the write: %+v\n%s", st, rr.log)
	}
	rr.store(dist, f32(farDist))
	if !rr.untilMarked(1) || rr.salvageFlag(near)&addr.SalvageFlagUnusable == 0 {
		t.Errorf("not marked once away\n%s", rr.log)
	}
}

// TestSalvageQuiet: once a pass has nothing to mark and nothing held, the
// table is not read again until the landed set changes: a table gone bad
// after the marks is not even seen.
func TestSalvageQuiet(t *testing.T) {
	rr := salvageRig(t, true, ptOcto)
	rr.landSalvage()
	if !rr.untilMarked(1) {
		t.Fatalf("not marked\n%s", rr.log)
	}
	rr.ticks(3) // the verify, and a pass that finds the point marked
	if !rr.r.sv.quiet {
		t.Fatalf("not quiet: %+v\n%s", rr.r.Status().Salvage, rr.log)
	}
	rr.store(salvageBase+addr.SalvageOffSeaNum, []byte{0xEE})
	rr.ticks(6)
	if rr.log.has("past MAX_SEA") {
		t.Errorf("the table was read on a quiet tick\n%s", rr.log)
	}
}

// TestSalvageHeldReset: the held points and reasons are the visit's: a
// stage change forgets them.
func TestSalvageHeldReset(t *testing.T) {
	near := ptOcto
	near.Dist = 100
	rr := salvageRig(t, true, near)
	rr.landSalvage()
	rr.ticks(gate.StableTicks + 12)
	if rr.r.Status().Salvage.Held != 1 {
		t.Fatalf("held %d\n%s", rr.r.Status().Salvage.Held, rr.log)
	}
	rr.changeStage(woods)
	rr.ticks(gate.StableTicks + 4)
	if st := rr.r.Status().Salvage; st.Held != 0 || st.LastHeld != "" {
		t.Errorf("after the stage change: %+v", st)
	}
}
