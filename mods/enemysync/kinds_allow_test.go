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

// The allow batch (enemy-mirror-allow.json implementation_batches A0,
// A1): the KI, AM and AM2 rows are Kinds rows (A1; their header pins are
// TestKindTablePins'), and their samplers read the type table's spans and
// derive its flags (A0).

// TestAllowRows: the allow kinds are rows at their header ids: KindOf,
// KindByName, the walk layout and the BCK names know them, they shoot
// shape_angle.y (D13) and only the Keese drops home.pos from its pkey (D9).
func TestAllowRows(t *testing.T) {
	for _, id := range []KindID{KindKI, KindAM, KindAM2} {
		k := id.Row()
		if k == nil || k.ID != id || KindOf(k.Proc) != id {
			t.Fatalf("kind %d: no row or KindOf disagrees", id)
		}
		if r, ok := KindByName(k.Name); !ok || r != k || id.String() != k.Name {
			t.Errorf("KindByName(%q) / String() = %v %q", k.Name, ok, id.String())
		}
		if l, ok := KindLayouts[int16(k.Proc)]; !ok || l.Mode != k.GateOff || l.Ice != k.IceOff || !l.ModeU8 {
			t.Errorf("KindLayouts[%s] = %+v", k.Name, l)
		}
		if k.AngleOff != addr.ActorOffShapeAngleY || k.PKeyNoPos != (id == KindKI) {
			t.Errorf("%s: AngleOff 0x%X PKeyNoPos %v", k.Name, k.AngleOff, k.PKeyNoPos)
		}
		if len(bckNames[id]) != k.BckCount() {
			t.Errorf("%s: %d BCK names for %d rows", k.Name, len(bckNames[id]), k.BckCount())
		}
	}
	for _, id := range []KindID{KindMO2, KindBK, KindCC} {
		if k := id.Row(); k.AngleOff != addr.ActorOffCurAngleY || k.PKeyNoPos {
			t.Errorf("%s: AngleOff 0x%X PKeyNoPos %v", k.Name, k.AngleOff, k.PKeyNoPos)
		}
	}
	if BckName(KindKI, 0x14) != "WAIT1" || BckName(KindAM, 0x1A) != "DEAD" || BckName(KindAM2, 0x06) != "BURUBURU" || BckName(KindAM2, 0x10) != "-" || BckName(KindNone, 0x10) != "-" {
		t.Error("allow BCK names")
	}
	for _, b := range []uint8{KiF2Attn, KiF2Lunge, AmF2Attn, AmF2PlaySkip, Am2F2Attn, Am2F2WeakTg, Am2F2BodyCo, Am2F2Carried} {
		if b&0xC0 != 0 {
			t.Errorf("allow flags2 bit 0x%02X overlaps SF2_LIGHT/QUIET", b)
		}
	}
}

// allowActor writes R1's fields for an allow kind (kindActor's, with
// shape_angle x/y/z, current.angle x/z and scale.x) and returns its Enemy.
func allowActor(p kindPut, kind KindID, setID uint16, params uint32) Enemy {
	a := blkActor
	k := kind.Row()
	home := [3]uint32{math.Float32bits(10), math.Float32bits(-5), math.Float32bits(2000)}
	p.u32(a+addr.ActorOffBsPcID, blkPID)
	p.u16(a+addr.ActorOffProcName, k.Proc)
	p.b(a+addr.ActorOffInitState, 2)
	p.u32(a+addr.ActorOffParameters, params)
	p.u16(a+addr.ActorOffSetID, setID)
	for i, v := range home {
		p.u32(a+addr.ActorOffHomePos+uint32(4*i), v)
	}
	p.b(a+addr.ActorOffHomeRoomNo, 1)
	for i, v := range []float32{100, -20.5, 3000} {
		p.f32(a+addr.ActorOffCurPos+uint32(4*i), v)
	}
	p.u16(a+addr.ActorOffCurAngle, 0x0010)
	p.u16(a+addr.ActorOffCurAngle+2, 0xC000)
	p.u16(a+addr.ActorOffCurAngle+4, 0x0030)
	p.u16(a+addr.ActorOffShapeAngle, 0x0123)
	p.u16(a+addr.ActorOffShapeAngle+2, 0x4000)
	p.u16(a+addr.ActorOffShapeAngle+4, 0x0456)
	p.f32(a+addr.ActorOffScale, 0.75)
	p.b(a+addr.ActorOffHealth, 3)
	return Enemy{Kind: kind, Addr: a, PID: blkPID, Params: params, HomeRoom: 1, HomePos: home,
		Ident: Ident{Proc: k.Proc, SetID: setID, PKey: PKeyFor(k, k.Proc, setID, params, 1, home)}}
}

// putCollider writes a collider's mSPrm words, centre, radius and height.
func putCollider(p kindPut, at uint32, tg, co uint32, c [3]float32, r, h float32) {
	p.u32(at+addr.CcCylOffTgSPrm, tg)
	p.u32(at+addr.CcCylOffCoSPrm, co)
	for i, v := range c {
		p.f32(at+addr.Mo2CcCenterOff+uint32(4*i), v)
	}
	p.f32(at+addr.CcShapeOffR, r)
	p.f32(at+addr.CcShapeOffH, h)
}

func checkReads(t *testing.T, name string, reads, want []readSpan) {
	t.Helper()
	if len(reads) != len(want) {
		t.Fatalf("%s: %d Reads %v, want %d", name, len(reads), reads, len(want))
	}
	for i := range want {
		if reads[i] != want[i] {
			t.Errorf("%s: Read %d = {0x%08X, 0x%X}, want {0x%08X, 0x%X}", name, i+1, reads[i].Addr, reads[i].N, want[i].Addr, want[i].N)
		}
	}
}

// kiFixture is a Keese's fields.
type kiFixture struct {
	Health                      int8
	Action                      uint8
	Behavior                    int8
	DrawGate, ShrinkDelete, Dmg uint8
	Invuln                      int16
	Ice                         int8
	Attn                        uint32
	SetID                       uint16
	HomeMoved, ParamsMoved      bool
}

func kiSample(t *testing.T, f kiFixture) (KindSample, []readSpan, error) {
	t.Helper()
	m := mem.NewFakeMem()
	p := kindPut{t, m}
	e := allowActor(p, KindKI, f.SetID, 0xFFFF0001)
	a := blkActor
	p.b(a+addr.ActorOffHealth, byte(f.Health))
	p.u32(a+addr.ActorOffAttnFlags, f.Attn)
	p.u32(a+addr.KiOffMorf, blkMorf)
	p.b(a+addr.KiOffParams, 1)
	p.b(a+addr.KiOffAction, f.Action)
	p.b(a+addr.KiOffBehavior, byte(f.Behavior))
	p.b(a+addr.KiOffDrawGate, f.DrawGate)
	p.b(a+addr.KiOffShrinkDelete, f.ShrinkDelete)
	p.u16(a+addr.KiOffInvuln, uint16(f.Invuln))
	p.u16(a+addr.KiOffAux1, 0x111)
	p.u16(a+addr.KiOffAux2, 0x222)
	putCollider(p, a+addr.KiOffAtSph, 0, 0, [3]float32{100, -20.5, 3000}, 0, 0)
	putCollider(p, a+addr.KiOffCoSph, 0, 1, [3]float32{100, -20.5, 3000}, 80, 0)
	putCollider(p, a+addr.KiOffTgSph, 1, 0, [3]float32{100, -20.5, 3000}, 40, 0)
	p.b(a+addr.KiOffDmgType, f.Dmg)
	p.b(a+addr.KiOffIceShrink, byte(f.Ice))
	if f.HomeMoved {
		p.f32(a+addr.ActorOffHomePos+4, 77)
	}
	if f.ParamsMoved {
		p.u32(a+addr.ActorOffParameters, 0xFFFF0101)
	}
	kindMorf(p)
	p.u16(blkMorf+addr.McaMorfOffEnd, 40)
	r := &readLog{m: m}
	s, err := ReadSampleKI(r, &e)
	return s, r.reads, err
}

func TestReadSampleKI(t *testing.T) {
	s, reads, err := kiSample(t, kiFixture{Health: 1, Action: addr.KiActFly, Behavior: 2, Dmg: 1, Attn: 0x4})
	if err != nil {
		t.Fatal(err)
	}
	checkReads(t, "KI", reads, []readSpan{
		{blkActor, 0x290},         // R1 the actor
		{blkActor + 0x2B4, 0x88},  // R2 mpMorf .. m339
		{blkActor + 0x580, 0x384}, // R3 m580, m6AC, mDamageSphere
		{blkActor + 0x905, 1},     // mDamageType
		{blkActor + 0x924, 0x10},  // R4 enemyice
		{blkMorf + 0x54, 0x2C},    // R5 mpAnm .. mMorfStep
	})
	if s.Kind != KindKI || s.Proc != addr.ProcKI || s.AngleY != 0x4000 || s.St.CurAngle != [3]int16{0x10, -0x4000, 0x30} ||
		s.St.ShapeAngle != [3]int16{0x123, 0x4000, 0x456} || s.St.Scale != 0.75 || s.St.BckIdx != -1 {
		t.Errorf("actor fields: angleY 0x%X cur %v shape %v scale %v bckidx %d", s.AngleY, s.St.CurAngle, s.St.ShapeAngle, s.St.Scale, s.St.BckIdx)
	}
	if s.Action != 1 || s.Mode != 2 || s.St.Params != 1 || s.St.DmgType != 1 || s.Aux != [4]int16{0x123, 0x456, 0x111, 0x222} {
		t.Errorf("action %d mode %d params %d dmg %d aux %v", s.Action, s.Mode, s.St.Params, s.St.DmgType, s.Aux)
	}
	if len(s.St.Colliders) != 3 || s.St.Colliders[1].Name != "co" || s.St.Colliders[1].R != 80 || !s.St.Colliders[1].CoSet() ||
		!s.St.Colliders[2].TgSet() || s.St.Colliders[2].Center != [3]float32{100, -20.5, 3000} || s.St.Colliders[2].Off != addr.KiOffTgSph {
		t.Errorf("colliders %+v", s.St.Colliders)
	}
	if s.Flags != 0 || s.Flags2 != KiF2Attn || s.St.Dormant || !s.MorfOK || s.MorfEnd != 40 {
		t.Errorf("flags 0x%02X flags2 0x%02X dormant %v morf %v", s.Flags, s.Flags2, s.St.Dormant, s.MorfOK)
	}
}

// TestReadSampleKIFlags sets one source at a time (design type_table KI).
func TestReadSampleKIFlags(t *testing.T) {
	base := kiFixture{Health: 1, Action: addr.KiActFly, Behavior: 2}
	for _, c := range []struct {
		name    string
		set     func(*kiFixture)
		flags   uint8
		flags2  uint8
		dormant bool
	}{
		{"nothing", func(*kiFixture) {}, 0, 0, false},
		{"health 0", func(f *kiFixture) { f.Health = 0 }, SlotDying, 0, false},
		{"action 30 fail", func(f *kiFixture) { f.Action = addr.KiActFail }, SlotDying, 0, false},
		{"action 20 damage is not dying", func(f *kiFixture) { f.Action = addr.KiActDamage }, 0, 0, false},
		{"ice shrink: dying, light", func(f *kiFixture) { f.Ice = 2 }, SlotDying, KindF2Light, false},
		{"m314 invuln", func(f *kiFixture) { f.Invuln = 5 }, SlotInvuln, 0, false},
		{"m2D4 hidden, dormant", func(f *kiFixture) { f.DrawGate = 3 }, SlotHidden, 0, true},
		{"attack lunge", func(f *kiFixture) { f.Action, f.Behavior = addr.KiActAttack, 3 }, 0, KiF2Lunge, false},
		{"attack index lunge", func(f *kiFixture) { f.Action, f.Behavior = addr.KiActAttackIndex, 3 }, 0, KiF2Lunge, false},
		{"attack, not the lunge", func(f *kiFixture) { f.Action, f.Behavior = addr.KiActAttack, 2 }, 0, 0, false},
		{"wait 0 dormant", func(f *kiFixture) { f.Action, f.Behavior = addr.KiActWait, 0 }, 0, 0, true},
		{"wait 1 dormant", func(f *kiFixture) { f.Action, f.Behavior = addr.KiActWait, 1 }, 0, 0, true},
		{"wait 2 awake", func(f *kiFixture) { f.Action, f.Behavior = addr.KiActWait, 2 }, 0, 0, false},
	} {
		f := base
		c.set(&f)
		s, _, err := kiSample(t, f)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if s.Flags != c.flags || s.Flags2 != c.flags2 || s.St.Dormant != c.dormant {
			t.Errorf("%s: Flags 0x%02X Flags2 0x%02X dormant %v, want 0x%02X 0x%02X %v", c.name, s.Flags, s.Flags2, s.St.Dormant, c.flags, c.flags2, c.dormant)
		}
	}
}

// TestReadSampleKIHomeMoved: a keyed Keese that moved its home.pos
// (d_a_ki.cpp:681-682) is still itself (D9: its pkey is rechecked without
// home.pos, so a changed params is still caught); an unkeyed one is
// changed.
func TestReadSampleKIHomeMoved(t *testing.T) {
	f := kiFixture{Health: 1, HomeMoved: true}
	s, _, err := kiSample(t, f)
	if err != nil || s.St.HomePos[1] != math.Float32bits(77) {
		t.Errorf("keyed, home moved: err %v home %v", err, s.St.HomePos)
	}
	f.ParamsMoved = true
	if _, _, err := kiSample(t, f); !errors.Is(err, actors.ErrChanged) {
		t.Errorf("keyed, params changed: %v, want ErrChanged", err)
	}
	f.ParamsMoved = false
	f.SetID = addr.ActorSetIDNone
	if _, _, err := kiSample(t, f); !errors.Is(err, actors.ErrChanged) {
		t.Errorf("unkeyed, home moved: %v, want ErrChanged", err)
	}
}

// amFixture is an Armos Knight's fields.
type amFixture struct {
	Action, Mode uint8
	PlaySkip     int16
	BckIdx       int32
	Ice          int8
}

func amSample(t *testing.T, f amFixture) (KindSample, []readSpan, error) {
	t.Helper()
	m := mem.NewFakeMem()
	p := kindPut{t, m}
	e := allowActor(p, KindAM, 4, 0xFFFFFFFF)
	a := blkActor
	p.u32(a+addr.AmOffMorf, blkMorf)
	p.b(a+addr.AmOffAction, f.Action, f.Mode)
	p.u16(a+addr.AmOffPlaySkip, uint16(f.PlaySkip))
	p.u32(a+addr.AmOffCurrBckIdx, uint32(f.BckIdx))
	p.u32(a+addr.AmOffSwallowedPID, 0xFFFFFFFF)
	p.u16(a+addr.AmOffEyeRot, 0x0100)
	p.u16(a+addr.AmOffEyeRotY, 0xFF00)
	putCollider(p, a+addr.AmOffEyeSph, 1, 0, [3]float32{1, 2, 3}, 60, 0)
	putCollider(p, a+addr.AmOffBodyCyl, 1, 1, [3]float32{100, -20.5, 3000}, 80, 300)
	p.b(a+addr.AmOffIceShrink, byte(f.Ice))
	kindMorf(p)
	r := &readLog{m: m}
	s, err := ReadSampleAM(r, &e)
	return s, r.reads, err
}

func TestReadSampleAM(t *testing.T) {
	s, reads, err := amSample(t, amFixture{Action: addr.AmActDousa, Mode: 3, BckIdx: 0x1C})
	if err != nil {
		t.Fatal(err)
	}
	checkReads(t, "AM", reads, []readSpan{
		{blkActor, 0x290},         // R1 the actor
		{blkActor + 0x2B8, 0x84},  // R2 mpMorf .. mTargetEyeRot
		{blkActor + 0x604, 0x4B8}, // R3 mEyeSph .. mNeedleCyl
		{blkActor + 0xABC, 0x10},  // R4 enemyice
		{blkMorf + 0x54, 0x2C},    // R5
	})
	body := s.St.Colliders[2]
	if s.Kind != KindAM || s.AngleY != 0x4000 || s.Mode != 3 || s.St.BckIdx != 0x1C || s.St.SwallowPID != 0xFFFFFFFF ||
		s.Aux != [4]int16{0x100, -0x100, 0, 0} || body.Name != "body" || !body.Cyl || body.R != 80 || body.H != 300 || s.St.Colliders[0].R != 60 {
		t.Errorf("AM sample %+v", s)
	}
	if s.Flags != 0 || s.Flags2 != 0 || s.St.Dormant {
		t.Errorf("flags 0x%02X flags2 0x%02X dormant %v", s.Flags, s.Flags2, s.St.Dormant)
	}
	for _, c := range []struct {
		name    string
		f       amFixture
		flags   uint8
		flags2  uint8
		dormant bool
	}{
		{"asleep: dormant", amFixture{Action: addr.AmActDousa, Mode: 1, BckIdx: addr.AmBckWait}, 0, 0, true},
		{"waking: not dormant", amFixture{Action: addr.AmActDousa, Mode: 2, BckIdx: addr.AmBckWait}, 0, 0, false},
		{"ITAI 40 hit", amFixture{Action: addr.AmActItai, Mode: 40}, 0, 0, false},
		{"ITAI 44 bomb swallowed", amFixture{Action: addr.AmActItai, Mode: 44}, SlotInvuln, 0, false},
		{"ITAI 46 thrash", amFixture{Action: addr.AmActItai, Mode: 46}, SlotInvuln, 0, false},
		{"ITAI 47 dead", amFixture{Action: addr.AmActItai, Mode: 47}, SlotDying, 0, false},
		{"mode 47 outside ITAI", amFixture{Action: addr.AmActHandou, Mode: 47}, 0, 0, false},
		{"play skip", amFixture{PlaySkip: 9, Mode: 3}, 0, AmF2PlaySkip, false},
		{"ice shrink", amFixture{Ice: 1, Mode: 3}, SlotDying, KindF2Light, false},
	} {
		s, _, err := amSample(t, c.f)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if s.Flags != c.flags || s.Flags2 != c.flags2 || s.St.Dormant != c.dormant {
			t.Errorf("%s: Flags 0x%02X Flags2 0x%02X dormant %v, want 0x%02X 0x%02X %v", c.name, s.Flags, s.Flags2, s.St.Dormant, c.flags, c.flags2, c.dormant)
		}
	}
}

// am2Fixture is an Armos's fields.
type am2Fixture struct {
	Action, Mode   uint8
	WeakTg, BodyCo uint32
	Status, Attn   uint32
	Ice            int8
}

func am2Sample(t *testing.T, f am2Fixture) (KindSample, []readSpan, error) {
	t.Helper()
	m := mem.NewFakeMem()
	p := kindPut{t, m}
	e := allowActor(p, KindAM2, addr.ActorSetIDNone, 0xFFFFFF00)
	a := blkActor
	p.u32(a+addr.ActorOffStatus, f.Status)
	p.u32(a+addr.ActorOffAttnFlags, f.Attn)
	p.u32(a+addr.Am2OffMorf, blkMorf)
	p.b(a+addr.Am2OffAction, f.Action, f.Mode)
	p.b(a+addr.Am2OffWeakHit, 1, 1, 0, 9)
	p.u16(a+addr.Am2OffStunTimer, 600)
	p.u32(a+addr.Am2OffCurrBckIdx, addr.Am2BckWait)
	putCollider(p, a+addr.Am2OffBodyCyl, 1, f.BodyCo, [3]float32{100, -20.5, 3000}, 35, 150)
	putCollider(p, a+addr.Am2OffWeakSph, f.WeakTg, 0, [3]float32{4, 5, 6}, 30, 0)
	p.b(a+addr.Am2OffIceShrink, byte(f.Ice))
	kindMorf(p)
	r := &readLog{m: m}
	s, err := ReadSampleAM2(r, &e)
	return s, r.reads, err
}

func TestReadSampleAM2(t *testing.T) {
	s, reads, err := am2Sample(t, am2Fixture{Action: addr.Am2ActDousa, Mode: 3, WeakTg: 1, BodyCo: 1, Attn: addr.AttnLockonBattle})
	if err != nil {
		t.Fatal(err)
	}
	checkReads(t, "AM2", reads, []readSpan{
		{blkActor, 0x290},         // R1 the actor
		{blkActor + 0x2B8, 0xA8},  // R2 mpMorf .. mAcchRadius
		{blkActor + 0x5A0, 0x4B8}, // R3 mBodyCyl .. mWeakSph
		{blkActor + 0xA8C, 0x10},  // R4 enemyice
		{blkMorf + 0x54, 0x2C},    // R5
	})
	weak := s.St.Colliders[3]
	if s.Kind != KindAM2 || s.St.BckIdx != addr.Am2BckWait || s.St.WeakHit != 1 || s.St.NotHome != 1 || s.St.Abyss != 9 || s.St.Stun != 600 ||
		weak.Name != "weak" || weak.Center != [3]float32{4, 5, 6} || weak.R != 30 || s.St.Colliders[0].H != 150 || s.St.Scale != 0.75 {
		t.Errorf("AM2 sample %+v", s)
	}
	if want := Am2F2Attn | Am2F2WeakTg | Am2F2BodyCo; s.Flags != 0 || s.Flags2 != want || s.St.Dormant {
		t.Errorf("flags 0x%02X flags2 0x%02X (want 0x%02X) dormant %v", s.Flags, s.Flags2, want, s.St.Dormant)
	}
	s, _, _ = am2Sample(t, am2Fixture{Status: addr.ActorStatusCarry, Ice: 1})
	if s.Flags != SlotDying || s.Flags2 != Am2F2Carried|KindF2Light || !s.St.Dormant {
		t.Errorf("carried, shrinking, no attention: flags 0x%02X flags2 0x%02X dormant %v", s.Flags, s.Flags2, s.St.Dormant)
	}
}

// TestClassifyWalkAllow: the walk-level gates over the allow kinds, the
// design's params gate (an unkeyed Keese only with params & 0xFF == 1,
// D10), duplicates, and the kind set.
func TestClassifyWalkAllow(t *testing.T) {
	act := func(proc uint16, setID uint16, params uint32, init int8) actors.Actor {
		return actors.Actor{Addr: 0x80500000 + uint32(setID), PID: uint32(setID) + 1, Proc: int16(proc), InitState: init, SetID: setID,
			HomeRoom: 1, Params: params, ModeOK: true}
	}
	w := actors.Walk{Actors: []actors.Actor{
		act(addr.ProcKI, 0x10, 0x00, 2),                // keyed: any params
		act(addr.ProcKI, addr.ActorSetIDNone, 0x01, 2), // unkeyed, params 1
		act(addr.ProcKI, addr.ActorSetIDNone, 0x02, 2), // unkeyed, params 2: refused
		act(addr.ProcAM, 0x20, 0, 1),                   // not executing
		act(addr.ProcAM2, 0x30, 0, 2),
		act(addr.ProcAM2, 0x30, 0, 2), // the same Ident twice
		act(addr.ProcBK, 0x40, 0, 2),  // a kind the set leaves out
	}}
	w.Actors[2].PID, w.Actors[5].PID = 99, 100
	set, err := ParseKinds("ki+u,am+u,am2+u")
	if err != nil {
		t.Fatal(err)
	}
	m := mem.NewFakeMem()
	es, err := ClassifyWalk(m, &w, "", 1, set)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, e := range es {
		got[e.Kind.String()+":"+verdictOf(e)]++
	}
	want := map[string]int{"ki:ok": 2, "ki:" + RefuseParams: 1, "am:" + RefuseInit: 1, "am2:" + RefuseDuplicate: 2}
	if len(got) != len(want) {
		t.Errorf("verdicts %v, want %v", got, want)
	}
	for k, n := range want {
		if got[k] != n {
			t.Errorf("verdicts %v, want %v", got, want)
			break
		}
	}
	if es, _ := ClassifyWalk(m, &w, "", 1, kindSetOf(KindAM)); len(es) != 1 || es[0].Kind != KindAM {
		t.Errorf("am only: %+v", es)
	}
	if es, _ := ClassifyWalk(m, &w, "", 2, set); len(es) != 0 {
		t.Errorf("room 2: %+v", es)
	}
}

// kindSetOf is the set with id alone enabled, keyed and unkeyed.
func kindSetOf(id KindID) KindSet {
	var s KindSet
	s.On[id], s.Unkeyed[id] = true, true
	return s
}

func verdictOf(e Enemy) string {
	if e.Refused == "" {
		return "ok"
	}
	return e.Refused
}

// TestClassifyWalkAllowLive: the allow kinds' live gates (LiveOK, the
// REL's em_validate R-8) over the bytes ClassifyWalk reads beyond the
// walk's mAction, and the GanonK stage gate on an unkeyed Keese (D10).
func TestClassifyWalkAllowLive(t *testing.T) {
	m := mem.NewFakeMem()
	p := kindPut{t, m}
	var w actors.Walk
	want := map[uint16]string{}
	add := func(proc, setID uint16, params uint32, mode uint8, status uint32, put func(a uint32), verdict string) {
		a := 0x80500000 + uint32(len(w.Actors))*0x1000
		w.Actors = append(w.Actors, actors.Actor{Addr: a, PID: uint32(len(w.Actors)) + 1, Proc: int16(proc), InitState: 2, SetID: setID,
			HomeRoom: 1, Params: params, Mode: int32(mode), ModeOK: true, Status: status})
		if put != nil {
			put(a)
		}
		want[uint16(len(w.Actors))] = verdict
	}
	b := func(off uint32, v uint8) func(uint32) { return func(a uint32) { p.b(a+off, v) } }
	add(addr.ProcKI, 1, 1, addr.KiActFly, 0, nil, "ok")
	add(addr.ProcKI, 2, 1, addr.KiActFireSet, 0, nil, RefuseLive)
	add(addr.ProcKI, 3, 1, addr.KiActFail, 0, nil, RefuseLive)
	add(addr.ProcKI, 4, 1, addr.KiActFly, 0, b(addr.KiOffShrinkDelete, 1), RefuseLive)
	add(addr.ProcKI, addr.ActorSetIDNone, 0xFFFF0101, addr.KiActFly, 0, nil, "ok")
	add(addr.ProcAM, 5, 0, addr.AmActItai, 0, b(addr.AmOffMode, addr.AmModeBombSwallow-1), "ok")
	add(addr.ProcAM, 6, 0, addr.AmActItai, 0, b(addr.AmOffMode, addr.AmModeBombSwallow), RefuseLive)
	add(addr.ProcAM, 7, 0, addr.AmActItai, 0, b(addr.AmOffMode, addr.AmModeDead), RefuseLive)
	add(addr.ProcAM, 8, 0, addr.AmActDousa, 0, b(addr.AmOffMode, 50), "ok") // mMode 44+ only counts in ITAI
	add(addr.ProcAM2, 9, 0, addr.Am2ActDousa, 0, nil, "ok")
	add(addr.ProcAM2, 10, 0, addr.Am2ActMahi, addr.ActorStatusCarry, nil, RefuseLive)
	add(addr.ProcAM2, 11, 0, addr.Am2ActModoru, 0, nil, RefuseLive)
	add(addr.ProcAM2, 12, 0, addr.Am2ActDousa, 0, b(addr.Am2OffAbyssTimer, 3), RefuseLive)
	add(addr.ProcAM2, 13, 0, addr.Am2ActItai, 0, b(addr.Am2OffMode, addr.Am2ModeDance-1), "ok")
	add(addr.ProcAM2, 14, 0, addr.Am2ActItai, 0, b(addr.Am2OffMode, addr.Am2ModeDance), RefuseLive)
	add(addr.ProcAM2, 15, 0, addr.Am2ActDousa, 0, b(addr.Am2OffMode, 30), "ok")
	set, err := ParseKinds("ki+u,am+u,am2+u")
	if err != nil {
		t.Fatal(err)
	}
	check := func(stage string, override map[uint16]string) {
		es, err := ClassifyWalk(m, &w, stage, 1, set)
		if err != nil {
			t.Fatal(err)
		}
		if len(es) != len(w.Actors) {
			t.Fatalf("%q: %d enemies, want %d", stage, len(es), len(w.Actors))
		}
		for _, e := range es {
			v, ok := override[uint16(e.PID)]
			if !ok {
				v = want[uint16(e.PID)]
			}
			if got := verdictOf(e); got != v {
				t.Errorf("%q: pid %d (%s setID 0x%X live %+v): %s, want %s", stage, e.PID, e.Kind, e.Ident.SetID, e.Live, got, v)
			}
			if e.Refused != RefuseStage && !e.LiveSpanOK {
				t.Errorf("%q: pid %d: the allow gate bytes were not read", stage, e.PID)
			}
		}
	}
	check("", nil)
	check("M_NewD2", nil)
	// GanonK: the unkeyed Keese (pid 5) is refused; keyed ones are not.
	check(StageGanonK, map[uint16]string{5: RefuseStage})
	if k := KindKI.Row(); k.StageOK(StageGanonK, addr.ActorSetIDNone) || !k.StageOK(StageGanonK, 1) || !KindAM2.Row().StageOK(StageGanonK, addr.ActorSetIDNone) {
		t.Error("StageOK")
	}
}

// TestClassifyWalkAllowSticky: LiveOK is the allow kinds' adopt gate
// (liveSticky): an enemy the previous walk took (the host's cache) stays
// listed through the swallow, the dance, the carry and FAIL, which its
// sampler streams; a new one in those states is refused, and so is a
// MO2 carried on every walk.
func TestClassifyWalkAllowSticky(t *testing.T) {
	m := mem.NewFakeMem()
	p := kindPut{t, m}
	var w actors.Walk
	add := func(proc, setID uint16, mode uint8, status uint32, off uint32, v uint8) {
		a := 0x80500000 + uint32(len(w.Actors))*0x1000
		w.Actors = append(w.Actors, actors.Actor{Addr: a, PID: uint32(len(w.Actors)) + 1, Proc: int16(proc), InitState: 2, SetID: setID,
			HomeRoom: 1, Mode: int32(mode), ModeOK: true, Status: status})
		if off != 0 {
			p.b(a+off, v)
		}
	}
	add(addr.ProcAM, 1, addr.AmActItai, 0, addr.AmOffMode, addr.AmModeBombSwallow)
	add(addr.ProcAM2, 2, addr.Am2ActItai, 0, addr.Am2OffMode, addr.Am2ModeDance)
	add(addr.ProcAM2, 3, addr.Am2ActMahi, addr.ActorStatusCarry, 0, 0)
	add(addr.ProcKI, 4, addr.KiActFail, 0, 0, 0)
	add(addr.ProcMo2, 5, 5, 0, 0, 0) // carried (MirrorsMode)
	set, err := ParseKinds("ki,am,am2,mo2")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := classifyWalk(m, &w, "", 1, set, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range fresh {
		if e.Refused != RefuseLive {
			t.Errorf("fresh pid %d (%s): %q, want %s", e.PID, e.Kind, e.Refused, RefuseLive)
		}
	}
	was := make([]Enemy, len(fresh))
	copy(was, fresh)
	es, err := classifyWalk(m, &w, "", 1, set, was)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		want := ""
		if e.Kind == KindMO2 {
			want = RefuseLive
		}
		if e.Refused != want {
			t.Errorf("listed pid %d (%s): %q, want %q", e.PID, e.Kind, e.Refused, want)
		}
	}
	// Another actor at the same address (a new PID) is a new enemy.
	was[0].PID = 99
	if es, _ := classifyWalk(m, &w, "", 1, set, was); es[0].Refused != RefuseLive {
		t.Errorf("new pid at a listed address: %q, want %s", es[0].Refused, RefuseLive)
	}
}

// TestWireSlotDormantAllow: the host's WFlagDormant for the allow kinds is
// the sampler's St.Dormant (allow design type_table), never while the
// enemy is carried or dying, and the ChuChu's rule is unchanged.
func TestWireSlotDormantAllow(t *testing.T) {
	h := NewHost(mem.NewFakeMem(), log.Discard{}, 7, time.Unix(1000, 0))
	now := time.Unix(1000, 0)
	for _, c := range []struct {
		name   string
		kind   KindID
		set    func(*KindSample)
		wflags uint8
	}{
		{"KI asleep on the ceiling", KindKI, func(s *KindSample) { s.St.Dormant = true }, WFlagDormant},
		{"KI flying", KindKI, func(s *KindSample) {}, 0},
		{"KI dormant but dying", KindKI, func(s *KindSample) { s.St.Dormant, s.Flags = true, SlotDying }, 0},
		{"AM asleep", KindAM, func(s *KindSample) { s.St.Dormant = true }, WFlagDormant},
		{"AM awake", KindAM, func(s *KindSample) { s.Attn = addr.AttnLockonBattle }, 0},
		{"AM2 statue", KindAM2, func(s *KindSample) { s.St.Dormant = true }, WFlagDormant},
		{"AM2 statue carried", KindAM2, func(s *KindSample) { s.St.Dormant, s.Status = true, addr.ActorStatusCarry }, 0},
		{"AM2 light-arrow death", KindAM2, func(s *KindSample) { s.St.Dormant, s.Flags, s.IceTimer = true, SlotDying, 1 }, 0},
		{"CC without LOCKON", KindCC, func(s *KindSample) {}, WFlagDormant},
		{"CC carried", KindCC, func(s *KindSample) { s.Status = addr.ActorStatusCarry }, 0},
		{"BK never", KindBK, func(s *KindSample) { s.St.Dormant = true }, 0},
	} {
		k := c.kind.Row()
		e := &Enemy{Kind: c.kind, Ident: Ident{Proc: k.Proc, SetID: 1, PKey: 0x1234}}
		ks := KindSample{Kind: c.kind, Ident: e.Ident}
		c.set(&ks)
		ks.Slot.Flags = ks.Flags
		if s := h.wireSlot(e, &ks, now); s.WFlags != c.wflags {
			t.Errorf("%s: WFlags 0x%02X, want 0x%02X", c.name, s.WFlags, c.wflags)
		}
	}
}

// TestForwardHit: the client's At-type filters (allow design go_changes,
// client hit forwarding).
func TestForwardHit(t *testing.T) {
	const sword, arrow = uint32(1 << 1), uint32(1 << 6)
	for _, c := range []struct {
		kind     KindID
		collider uint8
		atType   uint32
		want     bool
	}{
		{KindAM, HitHead, AtTypeGrapplingHook, false},
		{KindAM, HitHead, arrow, true},
		{KindAM, HitTg, AtTypeGrapplingHook, true},
		{KindAM, HitSpecial, 0, true}, // the bomb swallow: no At object
		{KindAM2, HitHead, AtTypeHookshot, false},
		{KindAM2, HitHead, sword, true},
		{KindAM2, HitTg, AtTypeSkullHammer, true},
		{KindAM2, HitTg, sword, false},
		{KindAM2, HitTg, 0, false},
		{KindKI, HitTg, AtTypeHookshot, true},
		{KindBK, HitHead, AtTypeGrapplingHook, true},
		{KindCC, HitTg, AtTypeHookshot, true},
	} {
		if got := ForwardHit(c.kind, c.collider, c.atType); got != c.want {
			t.Errorf("ForwardHit(%s, %d, 0x%08X) = %v, want %v", c.kind, c.collider, c.atType, got, c.want)
		}
	}
}

// TestHostAllowSticky: the host's walk keeps an Armos Knight it listed
// when it starts the bomb swallow (ITAI mMode 44), so the next STATEs
// stream the swallow (SlotInvuln) instead of dropping its Ident; a host
// that first sees it swallowing does not list it.
func TestHostAllowSticky(t *testing.T) {
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	if err := g.LoadStage(mDai); err != nil {
		t.Fatal(err)
	}
	if err := g.EnterRoom(1, 2); err != nil {
		t.Fatal(err)
	}
	es := fakegame.PlaceEnemies(fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 6, Proc: stage.ProcAM})
	frames(t, g, es, 4)
	am, ok := es.Of(0)
	if !ok {
		t.Fatal("no Armos Knight")
	}
	store(t, m, am.Addr+addr.AmOffAction, []byte{addr.AmActDousa, 0})
	listed := func(h *Host, now time.Time) *Slot {
		t.Helper()
		st, err := h.Tick(now)
		if err != nil {
			t.Fatal(err)
		}
		for i := range st.Slots {
			if st.Slots[i].Proc == addr.ProcAM && st.Slots[i].SetID == 6 {
				return &st.Slots[i]
			}
		}
		return nil
	}
	h := NewHost(m, log.Discard{}, 7, time.Unix(1000, 0))
	h.Kinds, _ = ParseKinds("am")
	now := time.Unix(1000, 0)
	if listed(h, now) == nil {
		t.Fatal("the Armos Knight is not listed")
	}
	store(t, m, am.Addr+addr.AmOffAction, []byte{addr.AmActItai, addr.AmModeBombSwallow})
	now = now.Add(2 * WalkEvery)
	s := listed(h, now)
	if s == nil {
		t.Fatal("the swallowing Armos Knight left the listing")
	}
	if s.Flags&SlotInvuln == 0 {
		t.Errorf("swallow slot %+v: no SlotInvuln", *s)
	}
	h2 := NewHost(m, log.Discard{}, 8, now)
	h2.Kinds = h.Kinds
	if s := listed(h2, now); s != nil {
		t.Errorf("a new host lists a swallowing Armos Knight: %+v", *s)
	}
}
