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

// ReadSample against a Moblin built byte by byte in a FakeMem (design
// "tests_and_live_checks", "Unit, block/host"): the six spans R1..R6 at
// their design offsets and lengths, and every field they yield.

// readLog is a mem.Reader that records every Read it passes on.
type readLog struct {
	m     mem.Reader
	reads []readSpan
}

type readSpan struct {
	Addr uint32
	N    int
}

func (r *readLog) Read(a uint32, n int) ([]byte, error) {
	r.reads = append(r.reads, readSpan{a, n})
	return r.m.Read(a, n)
}

const (
	blkActor = uint32(0x80500000)
	blkMorf  = uint32(0x80600000)
	blkAnm   = uint32(0x80700000)
	blkPID   = uint32(0x1234)
)

var blkMoblin = Moblin{Addr: blkActor, PID: blkPID, SetID: 2, HomeRoom: 1, Mode: 0}

// blkFixture is the fields one test Moblin carries; blkPlace writes them
// at their offsets.
type blkFixture struct {
	Pos                       [3]float32
	AngleY                    int16
	Health                    int8
	BlurScale                 float32
	BlurOn, BlurShape         uint32
	AnmGate, PlaySkip, Invuln int16
	DRMode, DRAction, Tilt    int16
	HasWeapon                 int8
	AttackType, AttackPhase   uint32
	Morf                      uint32 // mpMorf; the span below is written only when it is blkMorf
	Anm                       uint32
	Attr, State               uint8
	Rate, Frame               float32
	CurMorf, PrevMorf, Step   float32
	Bare, Hide                uint8
	Ice                       int8
}

func blkBase() blkFixture {
	return blkFixture{
		Pos: [3]float32{100, -20.5, 3000}, AngleY: -0x4000, Health: 3,
		BlurScale: 1.5, BlurOn: 1, BlurShape: 0x0102,
		AnmGate: 7, PlaySkip: 3, Invuln: 5,
		DRMode: 2, DRAction: 10, Tilt: -1234, HasWeapon: 1,
		AttackType: 0x104, AttackPhase: 0x202,
		Morf: blkMorf, Anm: blkAnm, Attr: AnmAttrLoop, State: 1,
		Rate: 1, Frame: 12.5, CurMorf: 0.5, PrevMorf: 0.25, Step: 0.1,
		Bare: 1,
	}
}

func blkPlace(t *testing.T, m *mem.FakeMem, f blkFixture) {
	t.Helper()
	be := binary.BigEndian
	put := func(a uint32, b []byte) {
		t.Helper()
		if err := m.Store(a, b); err != nil {
			t.Fatal(err)
		}
	}
	u16 := func(a uint32, v uint16) { put(a, be.AppendUint16(nil, v)) }
	u32 := func(a uint32, v uint32) { put(a, be.AppendUint32(nil, v)) }
	f32 := func(a uint32, v float32) { u32(a, math.Float32bits(v)) }
	a := blkActor
	u32(a+addr.ActorOffBsPcID, blkPID)
	u16(a+addr.ActorOffProcName, ProcMO2)
	put(a+addr.ActorOffInitState, []byte{2})
	u16(a+addr.ActorOffSetID, blkMoblin.SetID)
	put(a+addr.ActorOffHomeRoomNo, []byte{byte(blkMoblin.HomeRoom)})
	for i, v := range f.Pos {
		f32(a+addr.ActorOffCurPos+uint32(4*i), v)
	}
	u16(a+addr.ActorOffCurAngle+2, uint16(f.AngleY))
	put(a+addr.ActorOffHealth, []byte{byte(f.Health)})

	f32(a+addr.Mo2OffBlurScale, f.BlurScale)
	u32(a+addr.Mo2OffBlurOn, f.BlurOn)
	u32(a+addr.Mo2OffBlurShape, f.BlurShape)
	u16(a+addr.Mo2OffAnmGate, uint16(f.AnmGate))
	u16(a+addr.Mo2OffPlaySkip, uint16(f.PlaySkip))
	u16(a+addr.Mo2OffInvuln, uint16(f.Invuln))
	u16(a+addr.Mo2OffDRMode, uint16(f.DRMode))
	u16(a+addr.Mo2OffDRAction, uint16(f.DRAction))
	u16(a+addr.Mo2OffTilt, uint16(f.Tilt))
	put(a+addr.Mo2OffHasWeapon, []byte{byte(f.HasWeapon)})
	u32(a+addr.Mo2OffAttackType, f.AttackType)
	u32(a+addr.Mo2OffAttackPhase, f.AttackPhase)
	u32(a+addr.Mo2OffMorf, f.Morf)

	mo := blkMorf
	u32(mo+addr.McaMorfOffAnm, f.Anm)
	put(mo+addr.McaMorfOffAttr, []byte{f.Attr, f.State})
	f32(mo+addr.McaMorfOffRate, f.Rate)
	f32(mo+addr.McaMorfOffFrame, f.Frame)
	f32(mo+addr.McaMorfOffCurMorf, f.CurMorf)
	f32(mo+addr.McaMorfOffPrevMorf, f.PrevMorf)
	f32(mo+addr.McaMorfOffMorfStep, f.Step)
	u32(mo+addr.McaMorfOffSound, 0x80123450)

	put(a+addr.Mo2OffBareHanded, []byte{f.Bare})
	put(a+addr.Mo2OffHide, []byte{f.Hide})
	put(a+addr.Mo2OffIceShrink, []byte{byte(f.Ice)})
}

func blkSample(t *testing.T, f blkFixture) (Sample, []readSpan) {
	t.Helper()
	m := mem.NewFakeMem()
	blkPlace(t, m, f)
	r := &readLog{m: m}
	mo := blkMoblin
	s, err := ReadSample(r, &mo)
	if err != nil {
		t.Fatalf("ReadSample: %v", err)
	}
	return s, r.reads
}

// TestReadSampleLayout pins the spans against the design's numbers
// (s2.host_side SAMPLER, layout_changes[3]), not against the addr names
// they are built from.
func TestReadSampleLayout(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want int
	}{
		{"ReadsPerSample", ReadsPerSample, 6},
		{"ActorSize", addr.ActorSize, 0x290},
		{"readMidOff", readMidOff, 0x590},
		{"readMidLen", readMidLen, 0x845},
		{"readTailOff", readTailOff, 0x2943},
		{"readTailLen", readTailLen, 0x118},
		{"Mo2OffBlurScale", addr.Mo2OffBlurScale, 0x590},
		{"Mo2OffBlurOn", addr.Mo2OffBlurOn, 0x594},
		{"Mo2OffBlurShape", addr.Mo2OffBlurShape, 0x598},
		{"Mo2OffAnmGate", addr.Mo2OffAnmGate, 0x5B0},
		{"Mo2OffPlaySkip", addr.Mo2OffPlaySkip, 0x5B2},
		{"Mo2OffInvuln", addr.Mo2OffInvuln, 0x5B4},
		{"Mo2OffDRMode", addr.Mo2OffDRMode, 0x618},
		{"Mo2OffDRAction", addr.Mo2OffDRAction, 0x61A},
		{"Mo2OffTilt", addr.Mo2OffTilt, 0xAA0},
		{"Mo2OffHasWeapon", addr.Mo2OffHasWeapon, 0xDD4},
		{"Mo2OffAttackType", addr.Mo2OffAttackType, 0x2060},
		{"Mo2OffMorf", addr.Mo2OffMorf, 0x2C4},
		{"Mo2OffBareHanded", addr.Mo2OffBareHanded, 0x2943},
		{"Mo2OffHide", addr.Mo2OffHide, 0x2A1C},
		{"Mo2OffIceShrink", addr.Mo2OffIceShrink, 0x2A5A},
		{"McaMorfOffAnm", addr.McaMorfOffAnm, 0x54},
		{"McaMorfOffAttr", addr.McaMorfOffAttr, 0x5C},
		{"McaMorfOffState", addr.McaMorfOffState, 0x5D},
		{"McaMorfOffStart", addr.McaMorfOffStart, 0x5E},
		{"McaMorfOffEnd", addr.McaMorfOffEnd, 0x60},
		{"McaMorfOffLoop", addr.McaMorfOffLoop, 0x62},
		{"McaMorfOffRate", addr.McaMorfOffRate, 0x64},
		{"McaMorfOffFrame", addr.McaMorfOffFrame, 0x68},
		{"McaMorfOffCurMorf", addr.McaMorfOffCurMorf, 0x74},
		{"McaMorfOffPrevMorf", addr.McaMorfOffPrevMorf, 0x78},
		{"McaMorfOffMorfStep", addr.McaMorfOffMorfStep, 0x7C},
		{"McaMorfOffSound", addr.McaMorfOffSound, 0x80},
		{"McaMorfAnimSpanOff", addr.McaMorfAnimSpanOff, 0x54},
		{"McaMorfAnimSpanLen", addr.McaMorfAnimSpanLen, 0x2C},
		{"Mo2BckNone", addr.Mo2BckNone, 0xFF},
	} {
		if c.got != c.want {
			t.Errorf("%s = 0x%X, want 0x%X", c.name, c.got, c.want)
		}
	}
}

func TestReadSample(t *testing.T) {
	s, reads := blkSample(t, blkBase())
	want := []readSpan{
		{blkActor, 0x290},          // R1 the actor
		{blkActor + 0x590, 0x845},  // R2 blur scale .. mbHasInnateWeapon
		{blkActor + 0x2060, 8},     // R3 attack type and phase
		{blkActor + 0x2C4, 4},      // R4 mpMorf
		{blkMorf + 0x54, 0x2C},     // R5 mpAnm .. mMorfStep
		{blkActor + 0x2943, 0x118}, // R6 m2943 .. ice timer
	}
	if len(reads) != ReadsPerSample || len(reads) != len(want) {
		t.Fatalf("%d Reads %v, want %d", len(reads), reads, ReadsPerSample)
	}
	for i := range want {
		if reads[i] != want[i] {
			t.Errorf("Read %d = {0x%08X, 0x%X}, want {0x%08X, 0x%X}", i+1, reads[i].Addr, reads[i].N, want[i].Addr, want[i].N)
		}
	}

	if s.SetID != 2 || s.Proc != ProcMO2 {
		t.Errorf("SetID/Proc = %d/0x%X", s.SetID, s.Proc)
	}
	if s.Pos != [3]float32{100, -20.5, 3000} || s.AngleY != -0x4000 || s.Health != 3 {
		t.Errorf("Pos %v AngleY %d Health %d", s.Pos, s.AngleY, s.Health)
	}
	if s.BlurScale != 1.5 || s.BlurShape != 0x02 {
		t.Errorf("BlurScale %v BlurShape 0x%X, want 1.5 and 0x02 (m0598's low byte)", s.BlurScale, s.BlurShape)
	}
	if want := SlotF2HasWeapon | SlotF2PlaySkip | SlotF2BlurOn | SlotF2AnmGate; s.Flags2 != want {
		t.Errorf("Flags2 = 0x%02X, want 0x%02X", s.Flags2, want)
	}
	if s.Mode != 2 || s.Action != 10 || s.Tilt != -1234 {
		t.Errorf("Mode %d Action %d Tilt %d", s.Mode, s.Action, s.Tilt)
	}
	if s.AttackType != 0x04 || s.AttackPhase != 0x02 {
		t.Errorf("AttackType 0x%X AttackPhase 0x%X, want the low bytes 4 and 2", s.AttackType, s.AttackPhase)
	}
	// Invuln, action 10 (defence) and m2943 set; health above 0, not hidden.
	if want := SlotInvuln | SlotDefence | SlotBareHanded; s.Flags != want {
		t.Errorf("Flags = 0x%02X, want 0x%02X", s.Flags, want)
	}
	if !s.MorfOK || s.Anm != blkAnm || s.AnmAttr != AnmAttrLoop || s.MorfState != 1 {
		t.Errorf("MorfOK %v Anm 0x%08X AnmAttr %d MorfState %d", s.MorfOK, s.Anm, s.AnmAttr, s.MorfState)
	}
	if s.MorfRate != 1 || s.MorfFrame != 12.5 || s.CurMorf != 0.5 || s.MorfStep != 0.1 {
		t.Errorf("MorfRate %v MorfFrame %v CurMorf %v MorfStep %v", s.MorfRate, s.MorfFrame, s.CurMorf, s.MorfStep)
	}
	// Bck is Tracker.Derive's: the read leaves it unresolved.
	if s.Bck != addr.Mo2BckNone {
		t.Errorf("Bck = 0x%X, want 0x%X before Derive", s.Bck, addr.Mo2BckNone)
	}
	if s.Hidden || s.IceTimer != 0 {
		t.Errorf("Hidden %v IceTimer %d", s.Hidden, s.IceTimer)
	}
}

// TestReadSampleFlags2 sets one source field at a time: each SlotF2 bit
// comes from its own offset and from nothing else.
func TestReadSampleFlags2(t *testing.T) {
	off := blkBase()
	off.HasWeapon, off.PlaySkip, off.BlurOn, off.AnmGate = 0, 0, 0, 0
	if s, _ := blkSample(t, off); s.Flags2 != 0 {
		t.Fatalf("all sources 0: Flags2 = 0x%02X", s.Flags2)
	}
	for _, c := range []struct {
		name string
		set  func(*blkFixture)
		want uint8
	}{
		{"HasWeapon +0xDD4", func(f *blkFixture) { f.HasWeapon = 1 }, SlotF2HasWeapon},
		{"PlaySkip +0x5B2", func(f *blkFixture) { f.PlaySkip = -1 }, SlotF2PlaySkip},
		{"BlurOn +0x594", func(f *blkFixture) { f.BlurOn = 0x100 }, SlotF2BlurOn},
		{"AnmGate +0x5B0", func(f *blkFixture) { f.AnmGate = 1 }, SlotF2AnmGate},
	} {
		f := off
		c.set(&f)
		if s, _ := blkSample(t, f); s.Flags2 != c.want {
			t.Errorf("%s: Flags2 = 0x%02X, want 0x%02X", c.name, s.Flags2, c.want)
		}
	}
}

func TestReadSampleBlurScaleNotFinite(t *testing.T) {
	for _, v := range []float32{float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))} {
		f := blkBase()
		f.BlurScale = v
		s, reads := blkSample(t, f)
		if s.BlurScale != 1 {
			t.Errorf("m0590 %v: BlurScale = %v, want 1.0", v, s.BlurScale)
		}
		if len(reads) != ReadsPerSample {
			t.Errorf("m0590 %v: %d Reads", v, len(reads))
		}
	}
}

// TestReadSampleNoMorf: an mpMorf of 0 or outside MEM1 skips R5, so the
// sample takes five Reads, MorfOK is false, AnmAttr is unknown and Bck
// none, and the frame and rate stay 0.
func TestReadSampleNoMorf(t *testing.T) {
	for _, morf := range []uint32{0, 0x7FFFFFF0, 0x82FFFFF0, 0x90000000, 0xFFFFFFF0} {
		f := blkBase()
		f.Morf = morf
		s, reads := blkSample(t, f)
		if len(reads) != 5 {
			t.Errorf("mpMorf 0x%08X: %d Reads %v, want 5", morf, len(reads), reads)
		}
		for _, r := range reads {
			if r.Addr < blkActor || r.Addr >= blkActor+0x3000 {
				t.Errorf("mpMorf 0x%08X: read 0x%08X outside the actor", morf, r.Addr)
			}
		}
		if s.MorfOK || s.AnmAttr != AnmAttrUnknown || s.Bck != addr.Mo2BckNone {
			t.Errorf("mpMorf 0x%08X: MorfOK %v AnmAttr 0x%X Bck 0x%X", morf, s.MorfOK, s.AnmAttr, s.Bck)
		}
		if s.Anm != 0 || s.MorfFrame != 0 || s.MorfRate != 0 || s.CurMorf != 0 || s.MorfStep != 0 {
			t.Errorf("mpMorf 0x%08X: morf fields %+v", morf, s)
		}
		if s.Flags2 == 0 || s.BlurScale != 1.5 {
			t.Errorf("mpMorf 0x%08X: the actor's own fields were lost: Flags2 0x%X BlurScale %v", morf, s.Flags2, s.BlurScale)
		}
	}
}

// TestReadSampleChanged: a different actor at the address is
// actors.ErrChanged after R1 alone.
func TestReadSampleChanged(t *testing.T) {
	m := mem.NewFakeMem()
	blkPlace(t, m, blkBase())
	for _, c := range []struct {
		name string
		mod  func(*Moblin)
	}{
		{"pid", func(mo *Moblin) { mo.PID++ }},
		{"setID", func(mo *Moblin) { mo.SetID = 7 }},
		{"home room", func(mo *Moblin) { mo.HomeRoom = 2 }},
	} {
		mo := blkMoblin
		c.mod(&mo)
		r := &readLog{m: m}
		if _, err := ReadSample(r, &mo); !errors.Is(err, actors.ErrChanged) {
			t.Errorf("%s: err = %v, want actors.ErrChanged", c.name, err)
		}
		if len(r.reads) != 1 {
			t.Errorf("%s: %d Reads, want 1", c.name, len(r.reads))
		}
	}
}

// TestReadSampleDying covers the slot flags the tail and DR span feed.
func TestReadSampleDying(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(*blkFixture)
		want uint8
	}{
		{"health 0", func(f *blkFixture) { f.Health = 0 }, SlotDying},
		{"action 20", func(f *blkFixture) { f.DRAction = 20 }, SlotDying},
		{"ice timer", func(f *blkFixture) { f.Ice = 4 }, SlotDying},
		{"ragdoll", func(f *blkFixture) { f.DRMode = -100 }, SlotRagdoll},
		{"hidden", func(f *blkFixture) { f.Hide = 1 }, SlotHidden},
	} {
		f := blkBase()
		f.Invuln, f.DRAction, f.Bare = 0, 0, 0
		c.set(&f)
		if s, _ := blkSample(t, f); s.Flags != c.want {
			t.Errorf("%s: Flags = 0x%02X, want 0x%02X", c.name, s.Flags, c.want)
		}
	}
}
