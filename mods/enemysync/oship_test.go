package enemysync

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// TestKindsOSHIP: the warship is row 10 with the design's go_row (no BCK,
// no ice, the model as MorfOff, mModelType the gate), off by default,
// unkeyed-only in practice; the gold ship is live-refused; no colliders
// (option B) and no death switch (own-delete).
func TestKindsOSHIP(t *testing.T) {
	k := KindOSHIP.Row()
	if k == nil || KindOf(addr.ProcOship) != KindOSHIP || KindOSHIP.String() != "oship" || int(KindOSHIP) != len(Kinds)-1 {
		t.Fatalf("row %+v", k)
	}
	if !k.NoAnim || k.Arc != "" || k.MorfOff != 0x4B0 || k.IceOff != 0 || k.GateOff != 0x2B6 || k.AngleOff != addr.ActorOffShapeAngleY {
		t.Errorf("oship row %+v", *k)
	}
	if d, _ := ParseKinds(""); d.Has(KindOSHIP) {
		t.Errorf("default kinds %s enable oship", d)
	}
	if s, err := ParseKinds("oship+u"); err != nil || !s.Has(KindOSHIP) || !s.TakesUnkeyed(KindOSHIP) {
		t.Errorf("oship+u = %+v %v", s, err)
	}
	if !k.ParamsOK(0xFF01FF02) || !k.LiveOK(LiveGate{Mode: 0xFF}) || k.LiveOK(LiveGate{Mode: 0x00}) {
		t.Error("oship gates")
	}
	for c := HitHead; c <= HitSpecial; c++ {
		if ColliderOff(KindOSHIP, c) != 0 {
			t.Errorf("collider %d", c)
		}
	}
	if DeathSwitchOff(KindOSHIP) != 0 {
		t.Error("death switch")
	}
}

type oshipFixture struct {
	Proc     int32
	Model    uint8
	HitTimer int32
	Health   int8
	Attn     uint32
}

func oshipSample(t *testing.T, f oshipFixture) (KindSample, []readSpan, error) {
	t.Helper()
	m := mem.NewFakeMem()
	p := kindPut{t, m}
	e := kindActor(p, KindOSHIP, 0xFFFF, 0xFF01FF02)
	a := blkActor
	p.u32(a+addr.ActorOffAttnFlags, f.Attn)
	p.u16(a+addr.ActorOffShapeAngleY, 0x0456)
	p.u16(a+addr.ActorOffShapeAngle+4, 0xFF80)
	p.b(a+addr.ActorOffHealth, byte(f.Health))
	p.u32(a+addr.OshipOffProc, uint32(f.Proc))
	p.b(a+addr.OshipOffSubMode, 2)
	p.b(a+addr.OshipOffSwitchA, 0x13)
	p.b(a+addr.OshipOffModelType, f.Model)
	p.u32(a+addr.OshipOffModel, blkMorf)
	p.u32(a+addr.OshipOffHitTimer, uint32(f.HitTimer))
	p.u16(a+addr.OshipOffAimY, 0x0300)
	p.u16(a+addr.OshipOffAimX, 0xFE00)
	r := &readLog{m: m}
	s, err := ReadSampleOship(r, &e)
	return s, r.reads, err
}

// TestReadSampleOship: four Reads, AngleY shape.y, Tilt/TiltZ shape.x/z,
// Aux {aimY, aimX}, Mode mModelType, no morf; DYING from mode 3 or health
// 0, INVULN from the hit timer, ATTN from the attention flags.
func TestReadSampleOship(t *testing.T) {
	s, reads, err := oshipSample(t, oshipFixture{Model: 0xFF, Health: 3, Attn: 4})
	if err != nil {
		t.Fatal(err)
	}
	checkReads(t, "OSHIP", reads, []readSpan{
		{blkActor, 0x290},        // R1 the actor
		{blkActor + 0x2AC, 0xC},  // R2 mCurrentProc .. mModelType
		{blkActor + 0xD70, 0x1C}, // R3 mHitTimer .. mAimRotXTarget
		{blkActor + 0x4B0, 4},    // R4 mpModel
	})
	if len(reads) != OshipReadsPerSample {
		t.Errorf("%d Reads, OshipReadsPerSample %d", len(reads), OshipReadsPerSample)
	}
	if s.Kind != KindOSHIP || s.Proc != addr.ProcOship || s.AngleY != 0x0456 || s.Tilt != 0x0123 || s.TiltZ != -0x80 ||
		s.Aux != [4]int16{0x300, -0x200, 0, 0} || s.Mode != 0xFF || s.Action != 2 || s.MorfOK {
		t.Errorf("slot fields: %+v", s.Sample)
	}
	if o := s.Oship; o.SwitchA != 0x13 || o.Model != blkMorf {
		t.Errorf("oship fields %+v", o)
	}
	if s.Flags != 0 || s.Flags2 != OshipF2Attn {
		t.Errorf("flags 0x%02X flags2 0x%02X", s.Flags, s.Flags2)
	}
	for _, c := range []struct {
		f    oshipFixture
		want uint8
	}{
		{oshipFixture{Model: 0xFF, Health: 3, Proc: addr.OshipProcDelete}, SlotDying},
		{oshipFixture{Model: 0xFF, Health: 0}, SlotDying},
		{oshipFixture{Model: 0xFF, Health: 2, HitTimer: 5}, SlotInvuln},
	} {
		s, _, err := oshipSample(t, c.f)
		if err != nil || s.Flags != c.want {
			t.Errorf("%+v: flags 0x%02X want 0x%02X (%v)", c.f, s.Flags, c.want, err)
		}
	}
}
