package enemysync

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// Batch C's Kargaroc (enemy-mirror-batch-c.json "bb", go_changes): its
// row is TestKindsBatchC's and its header pins TestKindTablePins'.

// TestBbLiveRefusal: the BB sampler's listing gate, one source at a
// time; a perch type with nothing set passes.
func TestBbLiveRefusal(t *testing.T) {
	ok := LiveGate{Mode: addr.BbTypePerch}
	for _, c := range []struct {
		name string
		set  func(*LiveGate)
		want string
	}{
		{"nothing", func(*LiveGate) {}, ""},
		{"perch type 7", func(g *LiveGate) { g.Mode = addr.BbTypePerch2 }, ""},
		{"attack type 3", func(g *LiveGate) { g.Mode = 3 }, ""},
		{"rider 5 (a Moblin)", func(g *LiveGate) { g.Mode = BbTypeRiderMO2 }, "rider"},
		{"rider 6 (a Bokoblin)", func(g *LiveGate) { g.Mode = BbTypeRiderBK }, "rider"},
		{"unk_2DF", func(g *LiveGate) { g.BbRider = 1 }, "m2df"},
		{"knockback", func(g *LiveGate) { g.BbKnock = 45 }, "knock"},
		{"knockback at the limit", func(g *LiveGate) { g.BbKnock = BbKnockMax }, ""},
		{"knockback NaN", func(g *LiveGate) { g.BbKnock = float32(math.NaN()) }, "knock"},
	} {
		g := ok
		c.set(&g)
		if got := BbLiveRefusal(g); got != c.want || KindBB.Row().LiveOK(g) != (c.want == "") {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if !liveSticky(KindBB) {
		t.Error("the BB live gate is not adopt-only")
	}
}

// TestStaticGatesBB (D9): a rider Kargaroc (type 5 or 6) is refused by
// its params keyed and unkeyed; the perches are taken keyed, the unkeyed
// ones only with "+u"; the live gate (readBbGate) refuses unk_2DF and a
// knockback on a Kargaroc the host has not listed yet, and a listed one
// stays through the knockback (liveSticky).
func TestStaticGatesBB(t *testing.T) {
	m := mem.NewFakeMem()
	p := kindPut{t, m}
	act := func(setID uint16, params uint32, i int) actors.Actor {
		return actors.Actor{Addr: 0x80500000 + uint32(i)*0x2000, PID: uint32(i) + 1, Proc: int16(addr.ProcBB), InitState: 2, SetID: setID,
			HomeRoom: 3, Params: params, Mode: int32(params & 0xFF), ModeOK: true}
	}
	w := actors.Walk{Actors: []actors.Actor{
		act(0, 0xFFFFFF04, 0),                   // M_NewD2 r3 set 0, perch 4
		act(2, 0xFFFF0507, 1),                   // set 2, perch 7
		act(5, 0xE000FF05, 2),                   // M_Dra09's rider (type 5): refused
		act(6, 0xFFFFFF06, 3),                   // a Bokoblin rider: refused
		act(addr.ActorSetIDNone, 0xFFFFFF05, 4), // an unkeyed rider: refused with +u too
		act(addr.ActorSetIDNone, 0xFFFFFF03, 5), // an unkeyed sea bird
		act(7, 0xFFFFFF04, 6),                   // unk_2DF set: live gate
		act(8, 0xFFFFFF04, 7),                   // knocked back: live gate
	}}
	p.b(w.Actors[6].Addr+addr.BbOffRider, 1)
	p.f32(w.Actors[7].Addr+addr.BbOffKnock, 30)
	want := map[uint32]string{1: "", 2: "", 3: RefuseParams, 4: RefuseParams, 5: RefuseParams, 6: "", 7: RefuseLive, 8: RefuseLive}
	bbu, _ := ParseKinds("bb+u")
	es, err := ClassifyWalk(m, &w, "M_NewD2", 3, bbu)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if e.Refused != want[e.PID] {
			t.Errorf("pid %d params 0x%08X: %q, want %q", e.PID, e.Params, e.Refused, want[e.PID])
		}
	}
	bb, _ := ParseKinds("bb")
	es, _ = ClassifyWalk(m, &w, "M_NewD2", 3, bb)
	for _, e := range es {
		if e.PID == 6 && e.Refused != RefuseUnkeyedOff {
			t.Errorf("the unkeyed sea bird without +u: %q", e.Refused)
		}
	}
	// Sticky: listed clean, then knocked back and its unk_2DF set: kept.
	p.b(w.Actors[0].Addr+addr.BbOffRider, 0)
	was, err := classifyWalk(m, &w, "M_NewD2", 3, bbu, nil)
	if err != nil {
		t.Fatal(err)
	}
	p.f32(w.Actors[0].Addr+addr.BbOffKnock, 45)
	if es, _ := classifyWalk(m, &w, "M_NewD2", 3, bbu, was); es[0].PID != 1 || es[0].Refused != "" || es[0].Live.BbKnock != 45 {
		t.Errorf("listed perch knocked back: %+v, want kept", es[0])
	}
	if es, _ := classifyWalk(m, &w, "M_NewD2", 3, bbu, nil); es[0].Refused != RefuseLive {
		t.Errorf("unlisted perch knocked back: %q", es[0].Refused)
	}
}

// bbFixture is a Kargaroc's fields.
type bbFixture struct {
	Health          int8
	Type, State     uint8
	DrawGate, Dead  uint8
	Invuln          int16
	Ice             int8
	Knock           float32
	Attn, SubMtd    uint32
	CurX, CurZ      int16
	EyeBtp, Rider   uint8
	AttackTimer     int16
	M366, M368      int16
	HeadTgSPrm      uint32
	BodyCenter      [3]float32
	IceFreezeFrames int16
}

func bbSample(t *testing.T, f bbFixture) (KindSample, []readSpan, error) {
	t.Helper()
	m := mem.NewFakeMem()
	p := kindPut{t, m}
	e := kindActor(p, KindBB, 0, 0xFFFFFF04)
	a := blkActor
	p.b(a+addr.ActorOffHealth, byte(f.Health))
	p.u32(a+addr.ActorOffAttnFlags, f.Attn)
	p.u32(a+addr.ActorOffSubMtd, f.SubMtd)
	p.u16(a+addr.ActorOffCurAngle, uint16(f.CurX))
	p.u16(a+addr.ActorOffCurAngle+4, uint16(f.CurZ))
	p.u32(a+addr.BbOffMorf, blkMorf)
	p.b(a+addr.BbOffEyeBtp, f.EyeBtp)
	p.b(a+addr.BbOffType, f.Type)
	p.b(a+addr.BbOffState, f.State)
	p.b(a+addr.BbOffRider, f.Rider)
	p.b(a+addr.BbOffDrawGate, f.DrawGate)
	p.u16(a+addr.BbOffAttackTimer, uint16(f.AttackTimer))
	p.u16(a+addr.BbOffInvuln, uint16(f.Invuln))
	p.f32(a+addr.BbOffKnock, f.Knock)
	p.u16(a+addr.BbOffM366, uint16(f.M366))
	p.u16(a+addr.BbOffM368, uint16(f.M368))
	putCollider(p, a+addr.BbOffHeadTgSph, f.HeadTgSPrm, 0, [3]float32{100, 30, 3000}, 40, 0)
	putCollider(p, a+addr.BbOffBodyTgSph, 1, 0, f.BodyCenter, 62.5, 0)
	p.b(a+addr.BbOffDead, f.Dead)
	p.b(a+addr.BbOffIceShrink, byte(f.Ice))
	p.u16(a+addr.BbOffIceFreeze, uint16(f.IceFreezeFrames))
	kindMorf(p)
	r := &readLog{m: m}
	s, err := ReadSampleBB(r, &e)
	return s, r.reads, err
}

// TestReadSampleBB: five Reads (batch C go_changes BB sampler), AngleY
// current.angle.y, Tilt current.angle.x and TiltZ current.angle.z, the
// type and state as Mode and Action, the spheres in header order, and
// what P0-BB prints.
func TestReadSampleBB(t *testing.T) {
	s, reads, err := bbSample(t, bbFixture{Health: 3, Type: addr.BbTypePerch, State: 1, CurX: -0x200, CurZ: 0x150, EyeBtp: 2,
		AttackTimer: 77, M366: 5, M368: -6, Attn: 4, SubMtd: 0x80388000, HeadTgSPrm: 1, BodyCenter: [3]float32{1, 2, 3}, IceFreezeFrames: 4})
	if err != nil {
		t.Fatal(err)
	}
	checkReads(t, "BB", reads, []readSpan{
		{blkActor, 0x290},         // R1 the actor
		{blkActor + 0x2B4, 0xB6},  // R2 mpMorf .. unk_368
		{blkActor + 0x5BC, 0x4B0}, // R3 the four spheres
		{blkActor + 0xC7E, 0x12},  // R4 unk_C7E .. the enemyice's timers
		{blkMorf + 0x54, 0x2C},    // R5 mpAnm .. mMorfStep
	})
	if len(reads) != BbReadsPerSample {
		t.Errorf("%d Reads, BbReadsPerSample %d", len(reads), BbReadsPerSample)
	}
	if s.Kind != KindBB || s.Proc != addr.ProcBB || s.AngleY != -0x4000 || s.Tilt != -0x200 || s.TiltZ != 0x150 || s.Aux != [4]int16{} ||
		s.Mode != addr.BbTypePerch || s.Action != 1 || s.Health != 3 || s.IceFreeze != 4 {
		t.Errorf("slot fields: angleY 0x%X tilt %d/%d aux %v mode %d action %d health %d freeze %d", s.AngleY, s.Tilt, s.TiltZ, s.Aux, s.Mode, s.Action, s.Health, s.IceFreeze)
	}
	bb := s.Bb
	if bb.Type != 4 || bb.State != 1 || bb.EyeBtp != 2 || bb.AttackTimer != 77 || bb.M366 != 5 || bb.M368 != -6 || bb.SubMtd != 0x80388000 {
		t.Errorf("bb fields %+v", bb)
	}
	cs := s.St.Colliders
	if len(cs) != 4 || cs[0].Name != "at" || cs[1].Name != "head" || cs[1].Off != addr.BbOffHeadTgSph || !cs[1].TgSet() ||
		cs[2].Name != "tg" || cs[2].Center != [3]float32{1, 2, 3} || cs[2].R != 62.5 || cs[3].Name != "co" {
		t.Errorf("colliders %+v", cs)
	}
	if s.Flags != 0 || s.Flags2 != BbF2Attn || s.St.Dormant || !s.MorfOK {
		t.Errorf("flags 0x%02X flags2 0x%02X dormant %v morf %v", s.Flags, s.Flags2, s.St.Dormant, s.MorfOK)
	}
}

// TestReadSampleBBFlags sets one source at a time: DYING from health <= 0,
// mbDead or the ice shrink (with the LIGHT bit), INVULN unk_340, HIDDEN
// unk_2F2, ATTN; the knockback alone is streamed as nothing.
func TestReadSampleBBFlags(t *testing.T) {
	base := bbFixture{Health: 3, Type: addr.BbTypePerch}
	for _, c := range []struct {
		name   string
		set    func(*bbFixture)
		flags  uint8
		flags2 uint8
	}{
		{"nothing", func(*bbFixture) {}, 0, 0},
		{"health 0", func(f *bbFixture) { f.Health = 0 }, SlotDying, 0},
		{"mbDead", func(f *bbFixture) { f.Dead = 1 }, SlotDying, 0},
		{"ice shrink: dying, light", func(f *bbFixture) { f.Ice = 3 }, SlotDying, KindF2Light},
		{"invuln", func(f *bbFixture) { f.Invuln = 6 }, SlotInvuln, 0},
		{"hidden on its switch", func(f *bbFixture) { f.DrawGate = 0xE6 }, SlotHidden, 0},
		{"knockback", func(f *bbFixture) { f.Knock = 45 }, 0, 0},
		{"attention", func(f *bbFixture) { f.Attn = 4 }, 0, BbF2Attn},
	} {
		f := base
		c.set(&f)
		s, _, err := bbSample(t, f)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if s.Flags != c.flags || s.Flags2 != c.flags2 || s.Hidden != (c.flags&SlotHidden != 0) {
			t.Errorf("%s: Flags 0x%02X Flags2 0x%02X, want 0x%02X 0x%02X", c.name, s.Flags, s.Flags2, c.flags, c.flags2)
		}
	}
}

// TestReadSampleBBChanged: ReadKindSample routes BB to ReadSampleBB, and
// a Kargaroc whose actor is no longer the walk's is actors.ErrChanged.
func TestReadSampleBBChanged(t *testing.T) {
	m := mem.NewFakeMem()
	p := kindPut{t, m}
	e := kindActor(p, KindBB, 0, 0xFFFFFF04)
	p.b(blkActor+addr.BbOffType, addr.BbTypePerch)
	kindMorf(p)
	if s, err := ReadKindSample(m, &e); err != nil || s.Kind != KindBB || s.Bb.Type != addr.BbTypePerch {
		t.Fatalf("ReadKindSample: %+v %v", s.Kind, err)
	}
	p.u32(blkActor+addr.ActorOffParameters, 0xFFFF0507)
	if _, err := ReadKindSample(m, &e); !errors.Is(err, actors.ErrChanged) {
		t.Errorf("params changed: %v", err)
	}
}

// TestHostBBWireSlot: the host puts a Kargaroc's current.angle.z in the
// slot's s16 at +0x18 (Slot.SetCcM316Z; the REL's em_consume_owned BB
// case) and never marks it dormant.
func TestHostBBWireSlot(t *testing.T) {
	h := NewHost(mem.NewFakeMem(), log.Discard{}, 7, time.Unix(1000, 0))
	ks := KindSample{Kind: KindBB, TiltZ: -0x123}
	ks.Health = 3
	s := h.wireSlot(&Enemy{Kind: KindBB}, &ks, time.Unix(1000, 0))
	if s.CcM316Z() != -0x123 || s.WFlags&WFlagDormant != 0 || s.Kind != KindBB {
		t.Errorf("BB wire slot: z %d wflags 0x%02X", s.CcM316Z(), s.WFlags)
	}
}

// TestHostTombstoneBBVanish (batch C go_changes): a Kargaroc that
// vanishes while not dying (a path end with mArg3 6, the forest's andsw0)
// is listed as a tombstone (DYING|RELEASE, WFlagTomb, its handle kept),
// which the client's REL ends quietly; a Moblin that vanishes alive gets
// none (a handback, as before).
func TestHostTombstoneBBVanish(t *testing.T) {
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	if err := g.LoadStage(mDai); err != nil {
		t.Fatal(err)
	}
	if err := g.EnterRoom(1, 2); err != nil {
		t.Fatal(err)
	}
	es := fakegame.PlaceEnemies(
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 0, Proc: stage.ProcBB, Params: 0xFFFFFF04},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 1, Proc: stage.ProcMO2, Mode: 1},
	)
	frames(t, g, es, 4)
	bird, _ := es.Of(0)
	mob, _ := es.Of(1)
	store(t, m, bird.Addr+addr.BbOffType, []byte{addr.BbTypePerch})
	start := time.Unix(1000, 0)
	h := NewHost(m, log.Discard{}, 7, start)
	h.Select = SelectLegacy
	h.Kinds, _ = ParseKinds("mo2,bb")
	st, err := h.Tick(start)
	if err != nil || st == nil || len(st.Slots) != 2 {
		t.Fatalf("first tick: %+v %v", st, err)
	}
	var birdHandle uint16
	for _, s := range st.Slots {
		if s.Kind == KindBB {
			birdHandle = s.Handle
			if s.Flags&SlotDying != 0 {
				t.Fatalf("the bird is dying: %+v", s)
			}
		}
	}
	// Both vanish alive: the bird's block and the Moblin's reused.
	if _, err := es.FreeAndReuse(g, bird.PID, stage.ProcBK); err != nil {
		t.Fatal(err)
	}
	if _, err := es.FreeAndReuse(g, mob.PID, stage.ProcBK); err != nil {
		t.Fatal(err)
	}
	st, err = h.Tick(start.Add(16 * time.Millisecond))
	if err != nil || st == nil {
		t.Fatalf("after the vanish: %v", err)
	}
	if len(st.Slots) != 1 {
		t.Fatalf("after the vanish: %+v, want the bird's tombstone only", st.Slots)
	}
	if s := st.Slots[0]; s.Kind != KindBB || s.WFlags != WFlagTomb || s.Flags&(SlotDying|SlotRelease) != SlotDying|SlotRelease || s.Handle != birdHandle {
		t.Errorf("tombstone: %+v", s)
	}
}
