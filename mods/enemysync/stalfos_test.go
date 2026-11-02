package enemysync

import (
	"errors"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// wind#27's Stalfos (docs/design/enemy-rows-next.md "Enemy mirror row: ST
// Stalfos"): its header pins are TestKindTablePins'.

// TestKindsST: ST is row 9 with the design's go_row, off by default,
// quiet by default; behaviour 14 is params-refused keyed and unkeyed, BOSS
// is live-refused (and on every walk: stickyRefused), and its colliders
// and death switch are the design's.
func TestKindsST(t *testing.T) {
	st := KindST.Row()
	if st == nil || KindOf(addr.ProcST) != KindST || KindST.String() != "st" || int(KindST) != len(Kinds)-2 {
		t.Fatalf("row %+v", st)
	}
	if st.Arc != "St" || st.BckFirst != 0x04 || st.BckLast != 0x35 || st.BckWait != 0x33 || st.MorfOff != 0x2BC || st.IceOff != 0x1FA8 ||
		st.GateOff != 0x2B4 || st.AngleOff != addr.ActorOffShapeAngleY || st.PKeyNoPos {
		t.Errorf("st row %+v", *st)
	}
	d, _ := ParseKinds("")
	if d.Has(KindST) {
		t.Errorf("default kinds %s enable st", d)
	}
	for _, p := range DefaultPolicy(d) {
		if uint16(p.Proc) == addr.ProcST {
			t.Errorf("DefaultPolicy over the default kinds names the Stalfos: %+v", p)
		}
	}
	if s, err := ParseKinds("st+u"); err != nil || !s.TakesUnkeyed(KindST) || s.Mask() != 1<<9 {
		t.Errorf("st+u = %s (mask 0x%X) %v", s, s.Mask(), err)
	}
	if s, err := ParseKinds("st"); err != nil || !s.Has(KindST) || s.TakesUnkeyed(KindST) {
		t.Errorf("st = %s %v", s, err)
	}
	if q, _ := ParseQuietKinds(""); !q.Has(KindST) {
		t.Errorf("quiet default %s", q)
	}
	for params, ok := range map[uint32]bool{0xFFFFFF03: true, 0xFF46FF01: true, 0xFFFFFFFF: true, 0x0000000E: false, 0xFFFFFF0E: false} {
		if got := st.ParamsOK(params) && st.UnkeyedParamsOK(params); got != ok {
			t.Errorf("st params 0x%08X: %v, want %v", params, got, ok)
		}
	}
	if !st.LiveOK(LiveGate{Mode: 3}) || st.LiveOK(LiveGate{Mode: 3, Boss: true}) || st.LiveOK(LiveGate{Mode: addr.StBehaviorUpper}) {
		t.Error("st LiveOK")
	}
	if !liveSticky(KindST) || !stickyRefused(KindST, LiveGate{Boss: true}) || stickyRefused(KindST, LiveGate{}) {
		t.Error("st sticky rules")
	}
	if ColliderOff(KindST, HitHead) != 0x54C || ColliderOff(KindST, HitTg) != 0x678 || ColliderOff(KindST, HitSpecial) != 0 ||
		DeathSwitchOff(KindST) != 0x2B8 || !vanishTomb(KindST) {
		t.Error("st colliders, death switch or vanish tomb")
	}
	if BckName(KindST, 0x33) != "WAIT" || BckName(KindST, 0x0F) != "HEADB_DAMAGE" {
		t.Error("st BCK names")
	}
}

// TestStaticGatesST: the walk takes keyed Stalfos with st and the unkeyed
// ones only with st+u; a BOSS (the M_DaiMB trio's death switches) and the
// split's upper half (behaviour 14) are refused.
func TestStaticGatesST(t *testing.T) {
	m := mem.NewFakeMem()
	act := func(setID uint16, params uint32, status uint32, i int) actors.Actor {
		return actors.Actor{Addr: 0x80500000 + uint32(i)*0x3000, PID: uint32(i) + 1, Proc: int16(addr.ProcST), InitState: 2, SetID: setID,
			HomeRoom: 19, Params: params, Mode: int32(params & 0xF), ModeOK: true, Status: status}
	}
	w := actors.Walk{Actors: []actors.Actor{
		act(0, 0x93FF0003, 0, 0),                                      // M_Dai r19 coffin, keyed
		act(addr.ActorSetIDNone, 0xFF78FF01, 0, 1),                    // Cave10, unkeyed
		act(addr.ActorSetIDNone, 0xFFFFFF01, addr.ActorStatusBoss, 2), // M_DaiMB: BOSS
		act(addr.ActorSetIDNone, 0xFFFFFF0E, 0, 3),                    // the split's upper half
	}}
	stu, _ := ParseKinds("st+u")
	es, err := ClassifyWalk(m, &w, "M_Dai", 19, stu)
	if err != nil {
		t.Fatal(err)
	}
	want := map[uint32]string{1: "", 2: "", 3: RefuseLive, 4: RefuseParams}
	for _, e := range es {
		if e.Refused != want[e.PID] {
			t.Errorf("pid %d: %q, want %q", e.PID, e.Refused, want[e.PID])
		}
	}
	st, _ := ParseKinds("st")
	es, _ = ClassifyWalk(m, &w, "M_Dai", 19, st)
	for _, e := range es {
		if e.PID == 2 && e.Refused != RefuseUnkeyedOff {
			t.Errorf("the unkeyed Stalfos without +u: %q", e.Refused)
		}
	}
}

// stFixture is a Stalfos's fields (an intact, walking one by default).
type stFixture struct {
	Behavior, DrawGate      uint8
	M02C4, Action, Invuln   int16
	M0304                   int16
	M02FA                   int8
	PartsLeft, HeadState    int8
	Nunchaku, HeadOff, Mace int8
	DeathWait, Ice          int8
	UpperPID, Attn          uint32
}

func stBase() stFixture {
	return stFixture{Behavior: 3, PartsLeft: addr.StPartsWhole, Mace: addr.StMaceCarried, UpperPID: addr.StUpperNone}
}

func stSample(t *testing.T, f stFixture) (KindSample, []readSpan, error) {
	t.Helper()
	m := mem.NewFakeMem()
	p := kindPut{t, m}
	e := kindActor(p, KindST, 0, 0x93FF0003)
	a := blkActor
	p.u32(a+addr.ActorOffAttnFlags, f.Attn)
	p.u16(a+addr.ActorOffShapeAngleY, 0x0456)
	p.b(a+addr.StOffBehavior, f.Behavior)
	p.b(a+addr.StOffDrawGate, f.DrawGate)
	p.u32(a+addr.StOffMorf, blkMorf)
	p.u16(a+addr.StOffM02C4, uint16(f.M02C4))
	p.u16(a+addr.StOffAction, uint16(f.Action))
	p.u16(a+addr.StOffInvuln, uint16(f.Invuln))
	p.b(a+addr.StOffM02FA, byte(f.M02FA))
	p.u16(a+addr.StOffM0304, uint16(f.M0304))
	p.b(a+addr.StOffPartsLeft, byte(f.PartsLeft))
	p.b(a+addr.StOffHeadState, byte(f.HeadState))
	p.b(a+addr.StOffNunchaku, byte(f.Nunchaku))
	p.u32(a+addr.StOffBokoPID, 0x1234)
	p.b(a+addr.StOffMace, byte(f.Mace))
	p.u32(a+addr.StOffUpperPID, f.UpperPID)
	p.b(a+addr.StOffHeadOff, byte(f.HeadOff))
	p.b(a+addr.StOffBodyHP, 16)
	p.b(a+addr.StOffDeathWait, byte(f.DeathWait))
	p.b(a+addr.StOffIceShrink, byte(f.Ice))
	kindMorf(p)
	r := &readLog{m: m}
	s, err := ReadSampleStalfos(r, &e)
	return s, r.reads, err
}

// TestReadSampleStalfos: five Reads (design §3), AngleY shape_angle.y,
// Aux {m0304, 0, 0, 0}, behaviour and action as Mode and Action, an
// intact Stalfos carrying its mace is HAS_WEAPON and nothing else.
func TestReadSampleStalfos(t *testing.T) {
	f := stBase()
	f.M0304, f.Attn = 4000, 4
	s, reads, err := stSample(t, f)
	if err != nil {
		t.Fatal(err)
	}
	checkReads(t, "ST", reads, []readSpan{
		{blkActor, 0x290},          // R1 the actor
		{blkActor + 0x2B4, 0x58},   // R2 mBehaviorType .. m0308
		{blkActor + 0xED0, 0x66C},  // R3 m0ED0 .. the head part's mPartPos
		{blkActor + 0x1DC8, 0x1F0}, // R4 m1DC8 .. the enemyice's timers
		{blkMorf + 0x54, 0x2C},     // R5 the morf
	})
	if len(reads) != StReadsPerSample {
		t.Errorf("%d Reads, StReadsPerSample %d", len(reads), StReadsPerSample)
	}
	if s.Kind != KindST || s.Proc != addr.ProcST || s.AngleY != 0x0456 || s.Aux != [4]int16{4000, 0, 0, 0} || s.Mode != 3 || s.Action != 0 {
		t.Errorf("slot fields: angleY 0x%X aux %v mode %d action %d", s.AngleY, s.Aux, s.Mode, s.Action)
	}
	if st := s.Stalfos; st.BokoPID != 0x1234 || st.BodyHP != 16 || st.Morf != blkMorf {
		t.Errorf("stalfos fields %+v", st)
	}
	if s.Flags != 0 || s.Flags2 != StF2HasWeapon|StF2Attn || s.St.Dormant || !s.MorfOK {
		t.Errorf("flags 0x%02X flags2 0x%02X dormant %v morf %v", s.Flags, s.Flags2, s.St.Dormant, s.MorfOK)
	}
}

// TestReadSampleStalfosFlags sets one source at a time: DYING never from
// health, HIDDEN from every "not intact" term, the ambush wait dormant.
func TestReadSampleStalfosFlags(t *testing.T) {
	for _, c := range []struct {
		name    string
		set     func(*stFixture)
		flags   uint8
		flags2  uint8
		dormant bool
	}{
		{"intact", func(*stFixture) {}, 0, StF2HasWeapon, false},
		{"fight", func(f *stFixture) { f.Action = addr.StActFight }, 0, StF2HasWeapon, false},
		{"head spin 0x1F", func(f *stFixture) { f.Action = addr.StActHeadDamage }, 0, StF2HasWeapon, false},
		{"countdown", func(f *stFixture) { f.DeathWait = 5 }, SlotDying, StF2HasWeapon, false},
		{"dying head", func(f *stFixture) { f.HeadState = addr.StHeadStateDead }, SlotDying, StF2HasWeapon, false},
		{"m02FA", func(f *stFixture) { f.M02FA = 1 }, SlotDying, StF2HasWeapon, false},
		{"light arrow", func(f *stFixture) { f.Ice = 1 }, SlotDying, StF2HasWeapon | KindF2Light, false},
		{"invuln", func(f *stFixture) { f.Invuln = 10 }, SlotInvuln, StF2HasWeapon, false},
		{"ambush wait", func(f *stFixture) { f.DrawGate, f.Action = 0xEB, addr.StActUnderground }, SlotHidden, StF2HasWeapon, true},
		{"coffin opening", func(f *stFixture) { f.DrawGate, f.Action, f.M02C4 = 0xFF, addr.StActCoffin, 1 }, SlotHidden, StF2HasWeapon, false},
		{"break 0x1E", func(f *stFixture) { f.Action = 0x1E }, SlotHidden, StF2HasWeapon, false},
		{"parts off", func(f *stFixture) { f.PartsLeft = 0x10 }, SlotHidden, StF2HasWeapon, false},
		{"nunchaku", func(f *stFixture) { f.Nunchaku = 1 }, SlotHidden, StF2HasWeapon, false},
		{"head off", func(f *stFixture) { f.HeadOff = 2 }, SlotHidden, StF2HasWeapon, false},
		{"split", func(f *stFixture) { f.UpperPID = 0x4321 }, SlotHidden, StF2HasWeapon, false},
		{"mace dropped", func(f *stFixture) { f.Mace = 0 }, SlotHidden, 0, false},
		{"attention", func(f *stFixture) { f.Attn = 4 }, 0, StF2HasWeapon | StF2Attn, false},
		{"attention while hidden", func(f *stFixture) { f.Attn, f.Action = 4, 0x1E }, SlotHidden, StF2HasWeapon, false},
	} {
		f := stBase()
		c.set(&f)
		s, _, err := stSample(t, f)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if s.Flags != c.flags || s.Flags2 != c.flags2 || s.Hidden != (c.flags&SlotHidden != 0) || s.St.Dormant != c.dormant {
			t.Errorf("%s: Flags 0x%02X Flags2 0x%02X dormant %v, want 0x%02X 0x%02X %v", c.name, s.Flags, s.Flags2, s.St.Dormant, c.flags, c.flags2, c.dormant)
		}
		if got := dormant(KindST, &s, s.Flags); got != (c.dormant && c.flags&SlotDying == 0) {
			t.Errorf("%s: dormant() %v", c.name, got)
		}
	}
}

// TestReadSampleStalfosChanged: ReadKindSample routes ST to
// ReadSampleStalfos, and a changed actor is actors.ErrChanged.
func TestReadSampleStalfosChanged(t *testing.T) {
	m := mem.NewFakeMem()
	p := kindPut{t, m}
	e := kindActor(p, KindST, 0, 0x93FF0003)
	p.b(blkActor+addr.StOffBehavior, 3)
	kindMorf(p)
	if s, err := ReadKindSample(m, &e); err != nil || s.Kind != KindST || s.Stalfos.Behavior != 3 {
		t.Fatalf("ReadKindSample: %+v %v", s.Kind, err)
	}
	p.u32(blkActor+addr.ActorOffParameters, 0x94FF0003)
	if _, err := ReadKindSample(m, &e); !errors.Is(err, actors.ErrChanged) {
		t.Errorf("params changed: %v", err)
	}
}

// TestStHitCollider: a collider-2 hit is read from whichever body sphere
// holds the hit object (the REL keeps that one's record), sphere 1 when
// none does.
func TestStHitCollider(t *testing.T) {
	m := mem.NewFakeMem()
	p := kindPut{t, m}
	a := uint32(0x80500000)
	p.u32(a+addr.StOffSph+addr.StSphStride+addr.Mo2CcTgHitObjOff, 0)
	if got := stHitCollider(m, a); got != addr.StOffSph+addr.StSphStride {
		t.Errorf("no hit: 0x%X", got)
	}
	p.u32(a+addr.StOffSph+4*addr.StSphStride+addr.Mo2CcTgHitObjOff, 0x80412345)
	if got := stHitCollider(m, a); got != addr.StOffSph+4*addr.StSphStride {
		t.Errorf("sphere 4's hit: 0x%X", got)
	}
}
