package enemysync

import (
	"math"
	"regexp"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// The allow batch's A0 (enemy-mirror-allow.json implementation_batches
// A0): the staged rows stay out of Kinds, agree with wwenemy_kinds.h's
// staged defines and wwapi/addr, and their samplers read the type table's
// spans and derive its flags.

// TestStagedRowsOutsideKinds: A0 changes no behaviour. KindOf, KindByName
// and the walk layout the pipelines use do not know the staged procs;
// the ids are the header's EMK_KIND_KI/AM/AM2; ProbeLayouts adds them.
func TestStagedRowsOutsideKinds(t *testing.T) {
	_, defs := kindsHeader(t)
	for _, k := range StagedKinds {
		if KindOf(k.Proc) != KindNone {
			t.Errorf("%s (proc 0x%04X) is a Kinds row", k.Name, k.Proc)
		}
		if _, ok := KindByName(k.Name); ok {
			t.Errorf("KindByName(%q) found a row", k.Name)
		}
		if _, ok := KindLayouts[int16(k.Proc)]; ok {
			t.Errorf("KindLayouts has %s", k.Name)
		}
		if l, ok := ProbeLayouts[int16(k.Proc)]; !ok || l.Mode != k.GateOff || l.Ice != k.IceOff || !l.ModeU8 {
			t.Errorf("ProbeLayouts[%s] = %+v", k.Name, l)
		}
		if k.ID.Row() != nil || k.ID.StagedRow() != &StagedKinds[k.ID-KindKI] || StagedOf(k.Proc) != k.ID {
			t.Errorf("%s: Row/StagedRow/StagedOf disagree", k.Name)
		}
		if r, ok := StagedByName(k.Name); !ok || r.ID != k.ID || k.ID.String() != k.Name {
			t.Errorf("StagedByName(%q) / String() = %v %q", k.Name, ok, k.ID.String())
		}
	}
	for p, l := range KindLayouts {
		if ProbeLayouts[p] != l {
			t.Errorf("ProbeLayouts[0x%X] differs from KindLayouts", p)
		}
	}
	for name, id := range map[string]KindID{"EMK_KIND_KI": KindKI, "EMK_KIND_AM": KindAM, "EMK_KIND_AM2": KindAM2} {
		if defs[name] != int64(id) {
			t.Errorf("%s = %d, Go %d", name, defs[name], id)
		}
	}
	if KindID(0).StagedRow() != nil || KindNone.StagedRow() != nil || KindNone.String() != "none" || StagedOf(addr.ProcBK) != KindNone {
		t.Error("a live or no row reads as staged")
	}
	if BckName(KindKI, 0x14) != "WAIT1" || BckName(KindAM, 0x1A) != "DEAD" || BckName(KindAM2, 0x06) != "BURUBURU" || BckName(KindAM2, 0x10) != "-" {
		t.Error("staged BCK names")
	}
	for i, k := range StagedKinds {
		if len(stagedBckNames[i]) != k.BckCount() {
			t.Errorf("%s: %d BCK names for %d rows", k.Name, len(stagedBckNames[i]), k.BckCount())
		}
	}
}

// TestStagedHeaderPins: the staged defines wwenemy_kinds.h already
// carries equal the staged rows and wwapi/addr. EMK_AM_CLASS_SIZE and
// EMK_AM2_CLASS_SIZE are 0 there (unknown when staged) and A0 changes no
// C, so each may be 0 or the Go size: A1 fills them.
func TestStagedHeaderPins(t *testing.T) {
	src, defs := kindsHeader(t)
	for p, id := range map[string]KindID{"EMK_KI_": KindKI, "EMK_AM_": KindAM, "EMK_AM2_": KindAM2} {
		k := id.StagedRow()
		for name, want := range map[string]int64{
			"PROC": int64(k.Proc), "BCK_FIRST": int64(k.BckFirst), "BCK_LAST": int64(k.BckLast), "BCK_WAIT": int64(k.BckWait),
			"MORF": int64(k.MorfOff), "ICE": int64(k.IceOff),
		} {
			got, ok := defs[p+name]
			if !ok {
				if p+name != "EMK_AM_BCK_WAIT" { // not staged in the header
					t.Errorf("wwenemy_kinds.h has no %s%s", p, name)
				}
				continue
			}
			if got != want {
				t.Errorf("%s%s = 0x%X, the staged row has 0x%X", p, name, got, want)
			}
		}
		m := regexp.MustCompile(`(?m)^#define\s+` + p + `ARC\s+"(\w+)"`).FindSubmatch(src)
		if m == nil || string(m[1]) != k.Arc {
			t.Errorf("%sARC is %q, the staged row's archive %q", p, m, k.Arc)
		}
	}
	for name, want := range map[string]int64{
		"EMK_KI_CLASS_SIZE": addr.KiClassSize, "EMK_KI_TG_SPH": addr.KiOffTgSph,
		"EMK_AM_EYE_SPH": addr.AmOffEyeSph, "EMK_AM_MOUTH_SPH": addr.AmOffMouthSph, "EMK_AM_BODY_CYL": addr.AmOffBodyCyl, "EMK_AM_NEEDLE_CYL": addr.AmOffNeedleCyl,
		"EMK_AM2_BTK": addr.Am2OffBtk, "EMK_AM2_BRK": addr.Am2OffBrk, "EMK_AM2_BODY_CYL": addr.Am2OffBodyCyl, "EMK_AM2_NEEDLE_CYL": addr.Am2OffNeedleCyl,
		"EMK_AM2_EYE_SPH": addr.Am2OffEyeSph, "EMK_AM2_WEAK_SPH": addr.Am2OffWeakSph,
	} {
		if got, ok := defs[name]; !ok || got != want {
			t.Errorf("%s = 0x%X (defined %v), Go has 0x%X", name, got, ok, want)
		}
	}
	for name, want := range map[string]int64{"EMK_AM_CLASS_SIZE": addr.AmClassSize, "EMK_AM2_CLASS_SIZE": addr.Am2ClassSize} {
		if got := defs[name]; got != 0 && got != want {
			t.Errorf("%s = 0x%X, Go has 0x%X", name, got, want)
		}
	}
	for _, b := range []uint8{KiF2Attn, KiF2Lunge, AmF2Attn, AmF2PlaySkip, Am2F2Attn, Am2F2WeakTg, Am2F2BodyCo, Am2F2Carried} {
		if b&0xC0 != 0 {
			t.Errorf("staged flags2 bit 0x%02X overlaps SF2_LIGHT/QUIET", b)
		}
	}
}

// stagedActor writes R1's fields for a staged kind (kindActor's, with
// shape_angle x/y/z, current.angle x/z and scale.x) and returns its Enemy.
func stagedActor(p kindPut, kind KindID, setID uint16, params uint32) Enemy {
	a := blkActor
	k := kind.StagedRow()
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
		Ident: Ident{Proc: k.Proc, SetID: setID, PKey: PKey(k.Proc, setID, params, 1, home)}}
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
	HomeMoved                   bool
}

func kiSample(t *testing.T, f kiFixture) (KindSample, []readSpan, error) {
	t.Helper()
	m := mem.NewFakeMem()
	p := kindPut{t, m}
	e := stagedActor(p, KindKI, f.SetID, 0xFFFF0001)
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
// (d_a_ki.cpp:681-682) is still itself (D9); an unkeyed one is changed.
func TestReadSampleKIHomeMoved(t *testing.T) {
	f := kiFixture{Health: 1, HomeMoved: true}
	s, _, err := kiSample(t, f)
	if err != nil || s.St.HomePos[1] != math.Float32bits(77) {
		t.Errorf("keyed, home moved: err %v home %v", err, s.St.HomePos)
	}
	f.SetID = addr.ActorSetIDNone
	if _, _, err := kiSample(t, f); err == nil {
		t.Error("unkeyed, home moved: no ErrChanged")
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
	e := stagedActor(p, KindAM, 4, 0xFFFFFFFF)
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
	e := stagedActor(p, KindAM2, addr.ActorSetIDNone, 0xFFFFFF00)
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

// TestClassifyStaged: the walk-level gates, the design's params gate (an
// unkeyed Keese only with params & 0xFF == 1), duplicates, and ids.
func TestClassifyStaged(t *testing.T) {
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
		act(addr.ProcBK, 0x40, 0, 2),  // not staged
	}}
	w.Actors[2].PID, w.Actors[5].PID = 99, 100
	es := ClassifyStaged(&w, 1)
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
	if es := ClassifyStaged(&w, 1, KindAM); len(es) != 1 || es[0].Kind != KindAM {
		t.Errorf("ids KindAM: %+v", es)
	}
	if es := ClassifyStaged(&w, 2); len(es) != 0 {
		t.Errorf("room 2: %+v", es)
	}
}

func verdictOf(e Enemy) string {
	if e.Refused == "" {
		return "ok"
	}
	return e.Refused
}
