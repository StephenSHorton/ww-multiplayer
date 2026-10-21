package enemysync

import (
	"errors"
	"math"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// The TN batch's B0 (enemy-mirror-tn.json): the Darknut is a staged row
// the probes read, outside Kinds, so nothing the pipelines see moves.

// TestStagedTN: the Darknut is staged at the design's id 6 (D1) and is
// not a row: KindHash, ParseKinds, KindOf, KindLayouts, the sets, the
// annex, ColliderOff and DeathSwitchOff do not know it, while String,
// BckName, StagedOf, StagedByName and ProbeLayouts do.
func TestStagedTN(t *testing.T) {
	if KindHash() != 0x97EDB0F9 {
		t.Errorf("KindHash 0x%08X moved: B0 must not change the table", KindHash())
	}
	if KindTN.Row() != nil || KindOf(addr.ProcTN) != KindNone || len(Kinds) != 6 {
		t.Fatal("tn is a Kinds row")
	}
	if _, ok := KindLayouts[int16(addr.ProcTN)]; ok {
		t.Error("KindLayouts has tn")
	}
	for _, v := range []string{"tn", "tn+u", "mo2,tn"} {
		if _, err := ParseKinds(v); err == nil {
			t.Errorf("ParseKinds(%q) accepted", v)
		}
	}
	if _, err := ParseAnnex("tn:hooks=attach"); err == nil {
		t.Error("ParseAnnex accepted tn")
	}
	if AllKinds().Has(KindTN) || MaskSet(0xFF).Has(KindTN) || ColliderOff(KindTN, HitHead) != 0 || DeathSwitchOff(KindTN) != 0 {
		t.Error("a pipeline table knows tn")
	}

	k := KindTN.StagedRow()
	if k == nil || k.ID != KindTN || StagedOf(addr.ProcTN) != KindTN || KindTN.String() != "tn" {
		t.Fatalf("staged row %+v", k)
	}
	if r, ok := StagedByName("tn"); !ok || r != k {
		t.Error("StagedByName(tn)")
	}
	if KindID(7).StagedRow() != nil || KindAM2.StagedRow() != nil || StagedOf(addr.ProcBK) != KindNone {
		t.Error("StagedRow beyond the staged rows")
	}
	if k.Proc != addr.ProcTN || k.Arc != "Tn" || k.BckFirst != 0x28 || k.BckLast != 0x55 || k.BckWait != 0x3D ||
		k.MorfOff != 0x2C4 || k.IceOff != 0x1524 || k.GateOff != 0x2BC || k.AngleOff != addr.ActorOffCurAngleY || k.PKeyNoPos {
		t.Errorf("tn row %+v (design R-1, D7, D8)", *k)
	}
	if len(tnBckNames) != k.BckCount() || len(tnBckNames) != 46 {
		t.Errorf("%d tn BCK names for %d rows", len(tnBckNames), k.BckCount())
	}
	if BckName(KindTN, 0x28) != "AAT_KAITEN1" || BckName(KindTN, 0x3D) != "AWAIT1" || BckName(KindTN, 0x3C) != "ATATE_ON1" ||
		BckName(KindTN, 0x55) != "S_DEMO_M2" || BckName(KindTN, 0x56) != "-" || BckName(KindTN, 0x27) != "-" {
		t.Error("tn BCK names (Tn.h:44-89)")
	}
	if l, ok := ProbeLayouts[int16(addr.ProcTN)]; !ok || l.Mode != addr.TnOffBehavior || l.Ice != addr.TnOffIce || !l.ModeU8 {
		t.Errorf("ProbeLayouts[tn] = %+v", l)
	}
	for p, l := range KindLayouts {
		if ProbeLayouts[p] != l {
			t.Errorf("ProbeLayouts[0x%X] differs from KindLayouts", p)
		}
	}
	if TnF2PlaySkip != mailbox.EnemySF2PlaySkip {
		t.Error("TnF2PlaySkip != SF2_PLAY_SKIP (design R-1 Flags2)")
	}
	for _, b := range []uint8{TnF2HasWeapon, TnF2PlaySkip, TnF2Attn} {
		if b&0xC0 != 0 {
			t.Errorf("tn flags2 bit 0x%02X overlaps SF2_LIGHT/QUIET", b)
		}
	}

	var s StagedSet
	if s.Any() || s.String() != "none" || !s.Enable(KindTN, false) || s.Enable(KindBK, true) {
		t.Error("StagedSet Enable")
	}
	if !s.Has(KindTN) || s.TakesUnkeyed(KindTN) || s.String() != "tn" || AllStaged().String() != "tn+u" || s.Has(KindBK) {
		t.Errorf("StagedSet %v", s)
	}
}

// TestTnLiveRefusal: the design's TN LiveOK (go_changes, R-4), one
// source at a time.
func TestTnLiveRefusal(t *testing.T) {
	ok := LiveGate{Mode: 4, DrAction: addr.TnActFight, Equip: 3}
	for _, c := range []struct {
		name string
		set  func(*LiveGate)
		want string
	}{
		{"nothing", func(*LiveGate) {}, ""},
		{"boss", func(g *LiveGate) { g.Boss = true }, "boss"},
		{"behaviour 15", func(g *LiveGate) { g.Mode = 15 }, "frozen"},
		{"D_DOZOU", func(g *LiveGate) { g.DrAction = 23 }, "d_dozou"},
		{"S_DEMO", func(g *LiveGate) { g.DrAction = 25 }, "s_demo"},
		{"fail is not refused", func(g *LiveGate) { g.DrAction = 20 }, ""},
		{"m02C1", func(g *LiveGate) { g.M02C1 = 1 }, "m02c1"},
		{"m1518", func(g *LiveGate) { g.M1518 = 1 }, "m1518"},
		{"m14F8", func(g *LiveGate) { g.M14F8 = 0x33 }, "m14f8"},
		{"m140C", func(g *LiveGate) { g.M140C = 1 }, "m140c"},
		{"cape 4", func(g *LiveGate) { g.Equip = 4 }, "cape"},
		{"cape 5", func(g *LiveGate) { g.Equip = 5 }, "cape"},
	} {
		g := ok
		c.set(&g)
		if got := TnLiveRefusal(g); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// TestClassifyStagedTN: keyed Darknuts are taken, unkeyed ones only with
// "+u", the params gate refuses behaviour nibbles 12..15, and the live
// gate bytes are read but refuse nothing (B0 prints them).
func TestClassifyStagedTN(t *testing.T) {
	m := mem.NewFakeMem()
	p := kindPut{t, m}
	act := func(setID uint16, params uint32, init int8) actors.Actor {
		return actors.Actor{Addr: 0x80500000 + uint32(setID&0xFF)*0x2000, PID: uint32(setID) + 1, Proc: int16(addr.ProcTN), InitState: init, SetID: setID,
			HomeRoom: 1, Params: params, ModeOK: true}
	}
	w := actors.Walk{Actors: []actors.Actor{
		act(0x10, 0xFFFF0004, 2),                // keyed, guards
		act(addr.ActorSetIDNone, 0xFFFF0000, 2), // unkeyed
		act(0x12, 0xFFFF000D, 2),                // miniboss params: refused
		act(0x13, 0xFFFF000F, 2),                // frozen statue params: refused
		act(0x14, 0, 1),                         // not executing
		{Addr: 0x80400000, PID: 77, Proc: int16(addr.ProcBK), InitState: 2, SetID: 0x15, HomeRoom: 1, ModeOK: true},
	}}
	w.Actors[1].PID = 99
	a0 := w.Actors[0].Addr
	w.Actors[0].Status = addr.ActorStatusBoss
	p.b(a0+addr.TnOffM02C1, 2)
	p.b(a0+addr.TnOffEquip, 5)
	p.u16(a0+addr.TnOffDRAction, addr.TnActDDozou)
	p.b(a0+addr.TnOffM140C, 1)
	p.b(a0+addr.TnOffM14F8, 0x32)
	p.b(a0+addr.TnOffM1518, 1)

	es, err := ClassifyStaged(m, &w, 1, AllStaged())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, e := range es {
		got[e.Kind.String()+":"+verdictOf(e)]++
	}
	want := map[string]int{"tn:ok": 2, "tn:" + RefuseParams: 2, "tn:" + RefuseInit: 1}
	if len(got) != len(want) {
		t.Errorf("verdicts %v, want %v", got, want)
	}
	for k, n := range want {
		if got[k] != n {
			t.Errorf("verdicts %v, want %v", got, want)
			break
		}
	}
	var keyed *Enemy
	for i := range es {
		if es[i].Ident.SetID == 0x10 {
			keyed = &es[i]
		}
	}
	if keyed == nil || !keyed.LiveSpanOK {
		t.Fatalf("keyed tn: %+v", keyed)
	}
	g := keyed.Live
	if !g.Boss || g.M02C1 != 2 || g.Equip != 5 || g.DrAction != addr.TnActDDozou || g.M140C != 1 || g.M14F8 != 0x32 || g.M1518 != 1 {
		t.Errorf("gate bytes %+v", g)
	}
	if TnLiveRefusal(g) != "boss" || keyed.Refused != "" {
		t.Errorf("the live gate refuses in B0: %q / %q", TnLiveRefusal(g), keyed.Refused)
	}

	var keyedOnly StagedSet
	keyedOnly.Enable(KindTN, false)
	es, err = ClassifyStaged(m, &w, 1, keyedOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if e.Ident.SetID == addr.ActorSetIDNone && (e.Refused != RefuseUnkeyedOff || e.LiveSpanOK) {
			t.Errorf("tn without +u: unkeyed %q span %v", e.Refused, e.LiveSpanOK)
		}
	}
	if es, _ := ClassifyStaged(m, &w, 1, StagedSet{}); len(es) != 0 {
		t.Errorf("no staged kinds: %+v", es)
	}
	if es, _ := ClassifyStaged(m, &w, 2, AllStaged()); len(es) != 0 {
		t.Errorf("room 2: %+v", es)
	}
}

// tnFixture is a Darknut's fields.
type tnFixture struct {
	Health               int8
	DrMode, DrAction     int16
	Pieces               int8
	Equip                uint8
	PlaySkip, Invuln     int16
	Weapon               int32
	M02C1                uint8
	M140C, M1444, M1518  int8
	Ice                  int8
	Attn, Status, SubMtd uint32
}

// tnActor writes R1's fields for a Darknut (current.angle and
// shape_angle differ, so AngleY shows which one streams) and returns its
// Enemy.
func tnActor(p kindPut, setID uint16, params uint32) Enemy {
	a := blkActor
	home := [3]uint32{math.Float32bits(10), math.Float32bits(-5), math.Float32bits(2000)}
	p.u32(a+addr.ActorOffBsPcID, blkPID)
	p.u16(a+addr.ActorOffProcName, addr.ProcTN)
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
	p.u16(a+addr.ActorOffCurAngle+2, 0xC000)
	p.u16(a+addr.ActorOffShapeAngle+2, 0x4000)
	p.f32(a+addr.ActorOffScale, 1)
	return Enemy{Kind: KindTN, Addr: a, PID: blkPID, Params: params, HomeRoom: 1, HomePos: home,
		Ident: Ident{Proc: addr.ProcTN, SetID: setID, PKey: PKey(addr.ProcTN, setID, params, 1, home)}}
}

func tnSample(t *testing.T, f tnFixture) (KindSample, []readSpan, error) {
	t.Helper()
	m := mem.NewFakeMem()
	p := kindPut{t, m}
	e := tnActor(p, 7, 0xFFFF0004)
	a := blkActor
	p.b(a+addr.ActorOffHealth, byte(f.Health))
	p.u32(a+addr.ActorOffAttnFlags, f.Attn)
	p.u32(a+addr.ActorOffStatus, f.Status)
	p.u32(a+addr.ActorOffSubMtd, f.SubMtd)
	p.b(a+addr.TnOffBehavior, 4)
	p.b(a+addr.TnOffDeathSwitch, 0x86)
	p.b(a+addr.TnOffM02C1, f.M02C1)
	p.u32(a+addr.TnOffMorf, blkMorf)
	p.u32(a+addr.TnOffShieldMorf, 0x80610000)
	p.u32(a+addr.TnOffArmorMorf, 0x80620000)
	p.b(a+addr.TnOffPieces, byte(f.Pieces))
	p.b(a+addr.TnOffParts+addr.TnPartOffM08, 0xFF)
	p.b(a+addr.TnOffParts+2*addr.TnPartStride+addr.TnPartOffM09, 1)
	p.b(a+addr.TnOffArmorColor, 2)
	p.b(a+addr.TnOffEquip, f.Equip)
	p.u16(a+addr.TnOffPlaySkip, uint16(f.PlaySkip))
	p.u16(a+addr.TnOffInvuln, uint16(f.Invuln))
	p.u16(a+addr.TnOffDRMode, uint16(f.DrMode))
	p.u16(a+addr.TnOffDRAction, uint16(f.DrAction))
	p.u16(a+addr.TnOffTilt, 0x111)
	p.u16(a+addr.TnOffTiltZ, 0x222)
	p.u16(a+addr.TnOffDRInvincible, 3)
	p.u32(a+addr.TnOffWeapon, uint32(f.Weapon))
	putCollider(p, a+addr.TnOffTgCyl, 1, 0, [3]float32{100, 17.5, 3000}, 50, 100)
	putCollider(p, a+addr.TnOffDefSph, 0, 0, [3]float32{90, 40, 3010}, 70, 0)
	p.f32(a+addr.TnOffDefCenter, 90)
	p.f32(a+addr.TnOffDefCenter+4, 40)
	p.f32(a+addr.TnOffDefCenter+8, 3010)
	p.u16(a+addr.TnOffM1402, 0x55)
	p.b(a+addr.TnOffM140C, byte(f.M140C))
	p.u32(a+addr.TnOffBokoPID, 0x4321)
	p.b(a+addr.TnOffM1444, byte(f.M1444))
	p.b(a+addr.TnOffM1518, byte(f.M1518))
	p.b(a+addr.TnOffIceShrink, byte(f.Ice))
	p.u16(a+addr.TnOffIceFreeze, 9)
	kindMorf(p)
	r := &readLog{m: m}
	s, err := ReadSampleTN(r, &e)
	return s, r.reads, err
}

func TestReadSampleTN(t *testing.T) {
	s, reads, err := tnSample(t, tnFixture{Health: 8, DrMode: 2, DrAction: addr.TnActFight, Pieces: 0x0F, Equip: 3, Weapon: 1,
		M1444: 1, Attn: 0x4, SubMtd: 0x80388000})
	if err != nil {
		t.Fatal(err)
	}
	checkReads(t, "TN", reads, []readSpan{
		{blkActor, 0x290},          // R1 the actor
		{blkActor + 0x2BC, 0x97C},  // R2 mBehaviorType .. m0C34
		{blkActor + 0xC74, 0x710},  // R3 the six colliders
		{blkActor + 0x1384, 0x1B0}, // R4 m1384 .. mEnemyIce
		{blkMorf + 0x54, 0x2C},     // R5 mpAnm .. mMorfStep
	})
	if len(reads) != TnReadsPerSample {
		t.Errorf("%d Reads, TnReadsPerSample %d", len(reads), TnReadsPerSample)
	}
	if s.Kind != KindTN || s.Proc != addr.ProcTN || s.SetID != 7 || s.AngleY != -0x4000 || s.St.CurAngle[1] != -0x4000 || s.St.ShapeAngle[1] != 0x4000 {
		t.Errorf("actor fields: kind %s angleY 0x%X (D8: current.angle.y) cur %v shape %v", s.Kind, s.AngleY, s.St.CurAngle, s.St.ShapeAngle)
	}
	tn := s.Tn
	if s.Action != addr.TnActFight || s.Mode != 2 || s.Tilt != 0x111 || s.TiltZ != 0x222 || s.Aux != [4]int16{0x222, 0, 0, 0} || s.DRInvincible != 3 {
		t.Errorf("dr fields: action %d mode %d tilt %d/%d aux %v inv %d", s.Action, s.Mode, s.Tilt, s.TiltZ, s.Aux, s.DRInvincible)
	}
	if tn.Behavior != 4 || tn.DeathSwitch != 0x86 || tn.Pieces != 0x0F || tn.Equip != 3 || tn.ArmorColor != 2 || tn.Weapon != 1 || tn.BokoPID != 0x4321 ||
		tn.M1444 != 1 || tn.M1402 != 0x55 || tn.ShieldMorf != 0x80610000 || tn.ArmorMorf != 0x80620000 || tn.SubMtd != 0x80388000 ||
		tn.Parts != [3][2]int8{{-1, 0}, {0, 0}, {0, 1}} || tn.DefCenter != [3]float32{90, 40, 3010} || tn.DefR != 70 {
		t.Errorf("tn fields %+v", tn)
	}
	if s.BlurShape != 0x07 || s.BlurScale != 0 {
		t.Errorf("BlurShape 0x%02X (pieces & 7) BlurScale %v", s.BlurShape, s.BlurScale)
	}
	cs := s.St.Colliders
	if len(cs) != 6 || cs[0].Name != "co" || !cs[1].Cyl || cs[1].H != 100 || !cs[1].TgSet() || cs[3].Name != "def" || cs[3].Off != addr.TnOffDefSph ||
		cs[5].Name != "wepon2" || cs[5].Off != addr.TnOffWepon2Sph {
		t.Errorf("colliders %+v", cs)
	}
	if s.Flags != 0 || s.Flags2 != TnF2HasWeapon|TnF2Attn || s.St.Dormant || !s.MorfOK || s.IceFreeze != 9 {
		t.Errorf("flags 0x%02X flags2 0x%02X dormant %v morf %v freeze %d", s.Flags, s.Flags2, s.St.Dormant, s.MorfOK, s.IceFreeze)
	}
}

// TestReadSampleTNFlags sets one source at a time (design go_changes
// block.go: DYING from dr.mAction 20..22 or the shrink, never health).
func TestReadSampleTNFlags(t *testing.T) {
	base := tnFixture{Health: 8, DrAction: addr.TnActFight, Pieces: 0x0B}
	for _, c := range []struct {
		name   string
		set    func(*tnFixture)
		flags  uint8
		flags2 uint8
	}{
		{"nothing", func(*tnFixture) {}, 0, 0},
		{"health 0 is not dying", func(f *tnFixture) { f.Health = 0 }, 0, 0},
		{"action 20 fail", func(f *tnFixture) { f.DrAction = addr.TnActFail }, SlotDying, 0},
		{"action 21 yogan fail", func(f *tnFixture) { f.DrAction = addr.TnActYoganFail }, SlotDying, 0},
		{"action 22 water fail", func(f *tnFixture) { f.DrAction = addr.TnActWaterFail }, SlotDying, 0},
		{"action 23 is not dying", func(f *tnFixture) { f.DrAction = addr.TnActDDozou }, 0, 0},
		{"ice shrink: dying, light", func(f *tnFixture) { f.Ice = 2 }, SlotDying, KindF2Light},
		{"m03F0 invuln", func(f *tnFixture) { f.Invuln = 5 }, SlotInvuln, 0},
		{"action 10 defence", func(f *tnFixture) { f.DrAction = addr.TnActDefence; f.M1444 = 1 }, SlotDefence, 0},
		{"ragdoll", func(f *tnFixture) { f.DrMode = -100 }, SlotRagdoll, 0},
		{"m02C1 hidden", func(f *tnFixture) { f.M02C1 = 3 }, SlotHidden, 0},
		{"m1518 hidden", func(f *tnFixture) { f.M1518 = 1 }, SlotHidden, 0},
		{"m0C34 weapon", func(f *tnFixture) { f.Weapon = 2 }, 0, TnF2HasWeapon},
		{"m03EE play skip", func(f *tnFixture) { f.PlaySkip = 5 }, 0, TnF2PlaySkip},
		{"attention", func(f *tnFixture) { f.Attn = 4 }, 0, TnF2Attn},
	} {
		f := base
		c.set(&f)
		s, _, err := tnSample(t, f)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if s.Flags != c.flags || s.Flags2 != c.flags2 || s.St.Dormant {
			t.Errorf("%s: Flags 0x%02X Flags2 0x%02X dormant %v, want 0x%02X 0x%02X", c.name, s.Flags, s.Flags2, s.St.Dormant, c.flags, c.flags2)
		}
		if s.BlurShape != 0x03 {
			t.Errorf("%s: BlurShape 0x%02X, want pieces 0x0B & 7", c.name, s.BlurShape)
		}
	}
}

// TestReadSampleTNChanged: a Darknut whose actor is no longer the walk's
// is actors.ErrChanged; ReadStagedSample routes TN and refuses the rows.
func TestReadSampleTNChanged(t *testing.T) {
	m := mem.NewFakeMem()
	p := kindPut{t, m}
	e := tnActor(p, 7, 0xFFFF0004)
	kindMorf(p)
	if _, err := ReadStagedSample(m, &e); err != nil {
		t.Fatalf("ReadStagedSample: %v", err)
	}
	p.u32(blkActor+addr.ActorOffParameters, 0xFFFF0005)
	if _, err := ReadSampleTN(m, &e); !errors.Is(err, actors.ErrChanged) {
		t.Errorf("params changed: %v", err)
	}
	bk := e
	bk.Kind = KindBK
	if _, err := ReadStagedSample(m, &bk); err == nil {
		t.Error("ReadStagedSample sampled a Kinds row")
	}
	if _, err := ReadKindSample(m, &e); err == nil {
		t.Error("ReadKindSample sampled the staged tn")
	}
}
