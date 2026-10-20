package enemysync

import (
	"encoding/binary"
	"errors"
	"math"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// ReadSampleBK and ReadSampleCC against actors built byte by byte (design
// test_plan.unit TestReadSampleBK/CC): the five spans at the design's
// offsets and lengths, and the flags each field derives.

// kindPut writes big-endian values into m.
type kindPut struct {
	t *testing.T
	m *mem.FakeMem
}

func (p kindPut) b(a uint32, v ...byte) {
	p.t.Helper()
	if err := p.m.Store(a, v); err != nil {
		p.t.Fatal(err)
	}
}
func (p kindPut) u16(a uint32, v uint16)  { p.b(a, byte(v>>8), byte(v)) }
func (p kindPut) u32(a uint32, v uint32)  { p.b(a, binary.BigEndian.AppendUint32(nil, v)...) }
func (p kindPut) f32(a uint32, v float32) { p.u32(a, math.Float32bits(v)) }

// kindActor writes the fopAc_ac_c fields R1 checks and reads, and
// returns the Enemy a walk would have made of it.
func kindActor(p kindPut, kind KindID, setID uint16, params uint32) Enemy {
	a := blkActor
	proc := Kinds[kind].Proc
	home := [3]uint32{math.Float32bits(10), math.Float32bits(-5), math.Float32bits(2000)}
	p.u32(a+addr.ActorOffBsPcID, blkPID)
	p.u16(a+addr.ActorOffProcName, proc)
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
	p.u16(a+addr.ActorOffShapeAngle, 0x0123)
	p.b(a+addr.ActorOffHealth, 3)
	return Enemy{Kind: kind, Addr: a, PID: blkPID, Params: params, HomeRoom: 1, HomePos: home,
		Ident: Ident{Proc: proc, SetID: setID, PKey: PKey(proc, setID, params, 1, home)}}
}

// kindMorf writes the morf's animation span at blkMorf.
func kindMorf(p kindPut) {
	mo := blkMorf
	p.u32(mo+addr.McaMorfOffAnm, blkAnm)
	p.b(mo+addr.McaMorfOffAttr, AnmAttrLoop, 1)
	p.f32(mo+addr.McaMorfOffRate, 1)
	p.f32(mo+addr.McaMorfOffFrame, 7.5)
	p.f32(mo+addr.McaMorfOffCurMorf, 0.5)
	p.f32(mo+addr.McaMorfOffMorfStep, 0.25)
}

// bkFixture is a Bokoblin's fields.
type bkFixture struct {
	Type, DrawGate, Hide2       uint8
	Morf                        uint32
	BlurScale                   float32
	BlurOn, BlurShape           uint32
	PlaySkip, Invuln            int16
	DRMode, DRAction            int16
	Tilt, TiltZ, DRInvincible   int16
	Hold                        uint32
	Jaw                         int16
	Boko                        uint32
	BokoAttach, DeleteReq, Demo int8
	Ice                         int8
	Freeze                      int16
	Attn                        uint32
}

func bkBase() bkFixture {
	return bkFixture{Type: 4, Morf: blkMorf, BlurScale: 1.25, BlurOn: 1, BlurShape: 0x0101, DRMode: 2, DRAction: 1,
		Tilt: -300, TiltZ: 40, DRInvincible: 6, Hold: 1, Jaw: 0x200, Boko: 0x5678, BokoAttach: 1, Demo: 0, Freeze: 0}
}

func bkSample(t *testing.T, f bkFixture) (KindSample, []readSpan, error) {
	t.Helper()
	m := mem.NewFakeMem()
	p := kindPut{t, m}
	e := kindActor(p, KindBK, 0, 0xFFFF8784)
	a := blkActor
	p.u32(a+addr.ActorOffAttnFlags, f.Attn)
	p.b(a+addr.BkOffType, f.Type)
	p.b(a+addr.BkOffDrawGate, f.DrawGate)
	p.u32(a+addr.BkOffMorf, f.Morf)
	p.b(a+addr.BkOffHide2, f.Hide2)
	p.f32(a+addr.BkOffBlurScale, f.BlurScale)
	p.u32(a+addr.BkOffBlurOn, f.BlurOn)
	p.u32(a+addr.BkOffBlurShape, f.BlurShape)
	p.u16(a+addr.BkOffPlaySkip, uint16(f.PlaySkip))
	p.u16(a+addr.BkOffInvuln, uint16(f.Invuln))
	p.u16(a+addr.BkOffDRMode, uint16(f.DRMode))
	p.u16(a+addr.BkOffDRAction, uint16(f.DRAction))
	p.u16(a+addr.BkOffTilt, uint16(f.Tilt))
	p.u16(a+addr.BkOffTiltZ, uint16(f.TiltZ))
	p.u16(a+addr.BkOffDRInvincible, uint16(f.DRInvincible))
	p.u32(a+addr.BkOffWeaponHold, f.Hold)
	p.u16(a+addr.BkOffJaw, uint16(f.Jaw))
	p.u32(a+addr.BkOffBokoPID, f.Boko)
	p.b(a+addr.BkOffBokoAttach, byte(f.BokoAttach))
	p.b(a+addr.BkOffDeleteReq, byte(f.DeleteReq))
	p.b(a+addr.BkOffDemoGate, byte(f.Demo))
	p.b(a+addr.BkOffIceShrink, byte(f.Ice))
	p.u16(a+addr.BkOffIceFreeze, uint16(f.Freeze))
	kindMorf(p)
	r := &readLog{m: m}
	s, err := ReadSampleBK(r, &e)
	return s, r.reads, err
}

func TestReadSampleBK(t *testing.T) {
	s, reads, err := bkSample(t, bkBase())
	if err != nil {
		t.Fatal(err)
	}
	want := []readSpan{
		{blkActor, 0x290},         // R1 the actor
		{blkActor + 0x2B4, 0x58C}, // R2 mType .. dr mInvincibleTimer (+0x840)
		{blkActor + 0xB30, 4},     // R3 m0B30
		{blkActor + 0x11F4, 0x80}, // R4 jaw .. ice freeze timer (+0x1274)
		{blkMorf + 0x54, 0x2C},    // R5 mpAnm .. mMorfStep
	}
	if len(reads) != KindReadsPerSample || len(reads) != len(want) {
		t.Fatalf("%d Reads %v, want %d", len(reads), reads, KindReadsPerSample)
	}
	for i := range want {
		if reads[i] != want[i] {
			t.Errorf("Read %d = {0x%08X, 0x%X}, want {0x%08X, 0x%X}", i+1, reads[i].Addr, reads[i].N, want[i].Addr, want[i].N)
		}
	}
	if s.Kind != KindBK || s.Proc != 0xBE || s.SetID != 0 || s.Pos != [3]float32{100, -20.5, 3000} || s.AngleY != -0x4000 || s.Health != 3 {
		t.Errorf("actor fields %+v", s)
	}
	if s.BkType != 4 || s.Mode != 2 || s.Action != 1 || s.Tilt != -300 || s.TiltZ != 40 || s.DRInvincible != 6 {
		t.Errorf("type %d dr %d/%d tilt %d/%d inv %d", s.BkType, s.Mode, s.Action, s.Tilt, s.TiltZ, s.DRInvincible)
	}
	if s.Aux != [4]int16{40, 0x200, 0, 0} {
		t.Errorf("Aux = %v, want {m48C.z, jaw, 0, 0}", s.Aux)
	}
	if s.BokoPID != 0x5678 || s.BokoAttach != 1 || s.BlurShape != 1 || s.BlurScale != 1.25 {
		t.Errorf("boko 0x%X attach %d blur %d %v", s.BokoPID, s.BokoAttach, s.BlurShape, s.BlurScale)
	}
	if s.Flags != 0 {
		t.Errorf("Flags = 0x%02X, want 0", s.Flags)
	}
	if want := BkF2HasWeapon | BkF2BlurOn; s.Flags2 != want {
		t.Errorf("Flags2 = 0x%02X, want 0x%02X", s.Flags2, want)
	}
	if !s.MorfOK || s.Anm != blkAnm || s.MorfFrame != 7.5 || s.Bck != addr.Mo2BckNone {
		t.Errorf("morf %v anm 0x%X frame %v bck 0x%X", s.MorfOK, s.Anm, s.MorfFrame, s.Bck)
	}
}

// TestReadSampleBKFlags sets one source at a time (design flags BK).
func TestReadSampleBKFlags(t *testing.T) {
	off := bkBase()
	off.Hold, off.BlurOn = 0, 0
	for _, c := range []struct {
		name   string
		set    func(*bkFixture)
		flags  uint8
		flags2 uint8
	}{
		{"nothing", func(*bkFixture) {}, 0, 0},
		{"action 20 fail", func(f *bkFixture) { f.DRAction = 20 }, SlotDying, 0},
		{"action 21 lava", func(f *bkFixture) { f.DRAction = 21 }, SlotDying, 0},
		{"action 22 water", func(f *bkFixture) { f.DRAction = 22 }, SlotDying, 0},
		{"action 23 statue is not dying", func(f *bkFixture) { f.DRAction = 23 }, 0, 0},
		{"action 31 carry_drop is not dying", func(f *bkFixture) { f.DRAction = 31 }, 0, 0},
		{"ice shrink: dying, light", func(f *bkFixture) { f.Ice = 3 }, SlotDying, KindF2Light},
		{"m030E invuln", func(f *bkFixture) { f.Invuln = 1 }, SlotInvuln, 0},
		{"action 10 defence", func(f *bkFixture) { f.DRAction = 10 }, SlotDefence, 0},
		{"mMode -100 ragdoll", func(f *bkFixture) { f.DRMode = -100 }, SlotRagdoll, 0},
		{"m02BA hidden", func(f *bkFixture) { f.DrawGate = 1 }, SlotHidden, 0},
		{"m02DE hidden", func(f *bkFixture) { f.Hide2 = -1 & 0xFF }, SlotHidden, 0},
		{"m121C hidden", func(f *bkFixture) { f.DeleteReq = 1 }, SlotHidden, 0},
		{"m0B30 weapon", func(f *bkFixture) { f.Hold = 1 }, 0, BkF2HasWeapon},
		{"m030C play skip", func(f *bkFixture) { f.PlaySkip = 2 }, 0, BkF2PlaySkip},
		{"m02F0 blur", func(f *bkFixture) { f.BlurOn = 1 }, 0, BkF2BlurOn},
		{"attention", func(f *bkFixture) { f.Attn = 0x4 }, 0, BkF2Attn},
	} {
		f := off
		c.set(&f)
		s, _, err := bkSample(t, f)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if s.Flags != c.flags || s.Flags2 != c.flags2 {
			t.Errorf("%s: Flags 0x%02X Flags2 0x%02X, want 0x%02X 0x%02X", c.name, s.Flags, s.Flags2, c.flags, c.flags2)
		}
	}
	// Health 0 without a death action is not dying (the Moblin's rule
	// is not the Bokoblin's).
	m := mem.NewFakeMem()
	p := kindPut{t, m}
	e := kindActor(p, KindBK, 0, 0xFFFF8784)
	p.b(blkActor+addr.ActorOffHealth, 0)
	s, err := ReadSampleBK(m, &e)
	if err != nil || s.Flags&SlotDying != 0 || s.Health != 0 {
		t.Errorf("health 0: Flags 0x%02X err %v", s.Flags, err)
	}
	// No morf: four Reads.
	f := bkBase()
	f.Morf = 0
	if s, reads, err := bkSample(t, f); err != nil || len(reads) != 4 || s.MorfOK || s.AnmAttr != AnmAttrUnknown {
		t.Errorf("no morf: %d reads, MorfOK %v err %v", len(reads), s.MorfOK, err)
	}
}

// TestReadSampleKindChanged: R1 refuses an actor that is no longer the
// Enemy, the pkey included (params or home rewritten).
func TestReadSampleKindChanged(t *testing.T) {
	for _, c := range []struct {
		name string
		mod  func(*Enemy)
	}{
		{"pid", func(e *Enemy) { e.PID++ }},
		{"setID", func(e *Enemy) { e.Ident.SetID = 5 }},
		{"home room", func(e *Enemy) { e.HomeRoom = 2 }},
		{"pkey", func(e *Enemy) { e.Ident.PKey ^= 1 }},
	} {
		for _, kind := range []KindID{KindBK, KindCC} {
			m := mem.NewFakeMem()
			e := kindActor(kindPut{t, m}, kind, 3, 0xFF000100)
			c.mod(&e)
			r := &readLog{m: m}
			if _, err := ReadKindSample(r, &e); !errors.Is(err, actors.ErrChanged) || len(r.reads) != 1 {
				t.Errorf("%s %s: err %v after %d reads, want ErrChanged after 1", kind, c.name, err, len(r.reads))
			}
		}
	}
}

// ccFixture is a ChuChu's fields.
type ccFixture struct {
	Morf                         uint32
	Behavior, Color, Action, Sub uint8
	Electric, NoShadow, Split    uint8
	Wobble                       [3]int16
	TiltX, TiltZ                 int16
	Bck                          int32
	YOff                         float32
	Stun                         int16
	AtSPrm, TgSPrm, CoSPrm       uint32
	Ice                          int8
	Attn                         uint32
}

func ccBase() ccFixture {
	return ccFixture{Morf: blkMorf, Color: 1, Action: 2, Sub: 3, Wobble: [3]int16{1, 2, 3}, TiltX: 100, TiltZ: -100, Bck: 0x1C, YOff: 12.5,
		AtSPrm: 0x05, TgSPrm: 0x0B, CoSPrm: 0x75}
}

func ccSample(t *testing.T, f ccFixture) (KindSample, []readSpan, error) {
	t.Helper()
	m := mem.NewFakeMem()
	p := kindPut{t, m}
	e := kindActor(p, KindCC, 4, 0xFF460100)
	a := blkActor
	p.u32(a+addr.ActorOffAttnFlags, f.Attn)
	p.u32(a+addr.CcOffMorf, f.Morf)
	p.b(a+addr.CcOffBehavior, f.Behavior, f.Color)
	p.b(a+addr.CcOffAction, f.Action, f.Sub)
	p.b(a+addr.CcOffElectric, f.Electric)
	p.b(a+addr.CcOffNoShadowBG, f.NoShadow)
	p.b(a+addr.CcOffSplit, f.Split)
	for i, v := range f.Wobble {
		p.u16(a+addr.CcOffWobble+uint32(2*i), uint16(v))
	}
	p.u16(a+addr.CcOffGroundTilt, uint16(f.TiltX))
	p.u16(a+addr.CcOffGroundTiltZ, uint16(f.TiltZ))
	p.u32(a+addr.CcOffBckIndex, uint32(f.Bck))
	p.f32(a+addr.CcOffYOffset, f.YOff)
	p.u16(a+addr.CcOffStunTimer, uint16(f.Stun))
	p.u32(a+addr.CcOffCyl+addr.CcCylOffAtSPrm, f.AtSPrm)
	p.u32(a+addr.CcOffCyl+addr.CcCylOffTgSPrm, f.TgSPrm)
	p.u32(a+addr.CcOffCyl+addr.CcCylOffCoSPrm, f.CoSPrm)
	p.b(a+addr.CcOffIceShrink, byte(f.Ice))
	kindMorf(p)
	r := &readLog{m: m}
	s, err := ReadSampleCC(r, &e)
	return s, r.reads, err
}

func TestReadSampleCC(t *testing.T) {
	s, reads, err := ccSample(t, ccBase())
	if err != nil {
		t.Fatal(err)
	}
	want := []readSpan{
		{blkActor, 0x290},        // R1 the actor
		{blkActor + 0x2B4, 0xA8}, // R2 m2B4 .. m34E[7] (+0x35C)
		{blkActor + 0x6BC, 0x30}, // R3 mCyl At/Tg/Co mSPrm (+0x6EC)
		{blkActor + 0x81C, 0x10}, // R4 enemyice
		{blkMorf + 0x54, 0x2C},   // R5 mpAnm .. mMorfStep
	}
	if len(reads) != KindReadsPerSample || len(reads) != len(want) {
		t.Fatalf("%d Reads %v, want %d", len(reads), reads, KindReadsPerSample)
	}
	for i := range want {
		if reads[i] != want[i] {
			t.Errorf("Read %d = {0x%08X, 0x%X}, want {0x%08X, 0x%X}", i+1, reads[i].Addr, reads[i].N, want[i].Addr, want[i].N)
		}
	}
	if s.Kind != KindCC || s.Proc != 0xCF || s.SetID != 4 || s.CcColor != 1 || s.Action != 2 || s.Mode != 3 {
		t.Errorf("kind %v proc 0x%X setID %d colour %d action %d sub %d", s.Kind, s.Proc, s.SetID, s.CcColor, s.Action, s.Mode)
	}
	if s.Tilt != 100 || s.TiltZ != -100 || s.Aux != [4]int16{0x0123, 1, 2, 3} || s.CcBck != 0x1C || s.BlurScale != 12.5 || s.BlurShape != 0 {
		t.Errorf("tilt %d/%d aux %v bck %d yoff %v", s.Tilt, s.TiltZ, s.Aux, s.CcBck, s.BlurScale)
	}
	if s.Flags != 0 {
		t.Errorf("Flags = 0x%02X, want 0", s.Flags)
	}
	if want := CcF2Tg | CcF2Co | CcF2Squash; s.Flags2 != want {
		t.Errorf("Flags2 = 0x%02X, want 0x%02X", s.Flags2, want)
	}
}

// TestReadSampleCCFlags sets one source at a time (design flags CC).
func TestReadSampleCCFlags(t *testing.T) {
	sq := CcF2Tg | CcF2Co | CcF2Squash
	for _, c := range []struct {
		name   string
		set    func(*ccFixture)
		flags  uint8
		flags2 uint8
	}{
		{"action 4 dying (no squash change)", func(f *ccFixture) { f.Action = 4 }, SlotDying, sq},
		{"m2F5 0x35 dying, no squash", func(f *ccFixture) { f.Sub = 0x35 }, SlotDying, CcF2Tg | CcF2Co},
		{"ice: dying, light", func(f *ccFixture) { f.Ice = 2 }, SlotDying, sq | KindF2Light},
		{"Tg Set clear: invuln", func(f *ccFixture) { f.TgSPrm = 0x0A }, SlotInvuln, CcF2Co | CcF2Squash},
		{"Co Set clear", func(f *ccFixture) { f.CoSPrm = 0x74 }, 0, CcF2Tg | CcF2Squash},
		{"action 5 no squash", func(f *ccFixture) { f.Action = 5 }, 0, CcF2Tg | CcF2Co},
		{"action 6 no squash", func(f *ccFixture) { f.Action = 6 }, 0, CcF2Tg | CcF2Co},
		{"m301 split no squash", func(f *ccFixture) { f.Split = 1 }, 0, CcF2Tg | CcF2Co},
		{"m34E[4] stun no squash", func(f *ccFixture) { f.Stun = 30 }, 0, CcF2Tg | CcF2Co},
		{"m2F5 6 low eye", func(f *ccFixture) { f.Sub = 6 }, 0, sq | CcF2LowEye},
		{"m2F5 10 low eye", func(f *ccFixture) { f.Sub = 10 }, 0, sq | CcF2LowEye},
		{"m2F5 11 not low eye", func(f *ccFixture) { f.Sub = 11 }, 0, sq},
		{"action 1 low eye", func(f *ccFixture) { f.Action = 1 }, 0, sq | CcF2LowEye},
		{"m2F9 no shadow, not hidden", func(f *ccFixture) { f.NoShadow = 1 }, 0, sq | CcF2NoShadow},
		{"attention", func(f *ccFixture) { f.Attn = 1 }, 0, sq | CcF2Attn},
	} {
		f := ccBase()
		c.set(&f)
		s, _, err := ccSample(t, f)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if s.Flags != c.flags || s.Flags2 != c.flags2 {
			t.Errorf("%s: Flags 0x%02X Flags2 0x%02X, want 0x%02X 0x%02X", c.name, s.Flags, s.Flags2, c.flags, c.flags2)
		}
	}
}

// TestReadKindSampleMO2: the Moblin row samples through ReadSample's six
// Reads, byte-identical to the host's.
func TestReadKindSampleMO2(t *testing.T) {
	m := mem.NewFakeMem()
	blkPlace(t, m, blkBase())
	e := Enemy{Kind: KindMO2, Addr: blkActor, PID: blkPID, HomeRoom: blkMoblin.HomeRoom, Ident: Ident{Proc: ProcMO2, SetID: blkMoblin.SetID}}
	r := &readLog{m: m}
	s, err := ReadKindSample(r, &e)
	if err != nil {
		t.Fatal(err)
	}
	want, wantReads := blkSample(t, blkBase())
	if s.Sample != want || len(r.reads) != len(wantReads) {
		t.Errorf("ReadKindSample(MO2) = %+v (%d reads), ReadSample %+v (%d reads)", s.Sample, len(r.reads), want, len(wantReads))
	}
}
