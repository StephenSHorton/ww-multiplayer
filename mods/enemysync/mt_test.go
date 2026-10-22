package enemysync

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// Batch C's Magtail (enemy-mirror-batch-c.json "mt"), the first cut: an
// MT cut (R-20) deletes this file with mt.go.

// TestMtLiveRefusal: em_mt_gate's terms at listing, one at a time; MOVE,
// FIGHT and the pinned mode pass.
func TestMtLiveRefusal(t *testing.T) {
	for _, c := range []struct {
		name string
		g    LiveGate
		want string
	}{
		{"move", LiveGate{Mode: MtModeMove}, ""},
		{"fight", LiveGate{Mode: MtModeFight}, ""},
		{"pinned", LiveGate{Mode: MtModePinned}, ""},
		{"ball", LiveGate{Mode: MtModeBall}, "mode2"},
		{"dead", LiveGate{Mode: MtModeDead}, "mode3"},
		{"appear wait", LiveGate{MtAppear: 0xE4}, "appear"},
		{"death flag", LiveGate{MtDeath: 2}, "death"},
		{"killed", LiveGate{MtKilled: 1}, "killed"},
		{"light shrink", LiveGate{MtIce: 1}, "ice"},
		{"carried", LiveGate{Carried: true}, "carried"},
	} {
		if got := MtLiveRefusal(c.g); got != c.want || KindMT.Row().LiveOK(c.g) != (c.want == "") {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if !liveSticky(KindMT) {
		t.Error("the MT live gate is not adopt-only (BALL is the custody signal)")
	}
}

// TestClassifyWalkMT: an unkeyed Magtail is taken with "mt+u" only; one
// waiting on its appear switch is refused unlisted, and a listed one
// that curls into BALL stays listed (liveSticky).
func TestClassifyWalkMT(t *testing.T) {
	m := mem.NewFakeMem()
	p := kindPut{t, m}
	a := actors.Actor{Addr: 0x80500000, PID: 9, Proc: int16(addr.ProcMT), InitState: 2, SetID: addr.ActorSetIDNone, HomeRoom: 8,
		Params: 0xFF02FF00, ModeOK: true}
	w := actors.Walk{Actors: []actors.Actor{a}}
	mtu, _ := ParseKinds("mt+u")
	mt, _ := ParseKinds("mt")
	if es, _ := ClassifyWalk(m, &w, "M_NewD2", 8, mt); len(es) != 1 || es[0].Refused != RefuseUnkeyedOff {
		t.Errorf("mt without +u: %+v", es)
	}
	p.b(a.Addr+addr.MtOffAppear, 0xE4)
	if es, _ := ClassifyWalk(m, &w, "M_NewD2", 8, mtu); es[0].Refused != RefuseLive || es[0].Live.MtAppear != 0xE4 {
		t.Errorf("appear wait: %+v", es[0])
	}
	p.b(a.Addr+addr.MtOffAppear, 0)
	was, err := classifyWalk(m, &w, "M_NewD2", 8, mtu, nil)
	if err != nil || was[0].Refused != "" {
		t.Fatalf("clean: %+v %v", was, err)
	}
	w.Actors[0].Mode = MtModeBall
	if es, _ := classifyWalk(m, &w, "M_NewD2", 8, mtu, was); es[0].Refused != "" {
		t.Errorf("listed Magtail curled: %q, want kept", es[0].Refused)
	}
	if es, _ := classifyWalk(m, &w, "M_NewD2", 8, mtu, nil); es[0].Refused != RefuseLive {
		t.Errorf("unlisted Magtail curled: %q", es[0].Refused)
	}
	// The client's match (mtWas): its listed Magtail keeps its slot
	// through the custody BALL; a listed non-Magtail is not passed on.
	local := map[Ident]localEnemy{matchKey(was[0].Ident): {Enemy: was[0]}}
	if es, _ := classifyWalk(m, &w, "M_NewD2", 8, mtu, mtWas(local)); es[0].Refused != "" {
		t.Errorf("client's listed Magtail curled: %q, want kept", es[0].Refused)
	}
	ki := was[0]
	ki.Kind, ki.PID = KindKI, 10
	if got := mtWas(map[Ident]localEnemy{{Proc: uint16(addr.ProcKI)}: {Enemy: ki}}); len(got) != 0 {
		t.Errorf("mtWas passed a Keese: %+v", got)
	}
}

// mtFixture is a Magtail's fields.
type mtFixture struct {
	Health            int8
	Mode, Action      uint8
	Phase, Appear     uint8
	ColorA, ColorB    int32
	BallTimer, Dive   int16
	Death             int8
	Ice               int8
	Shield, Attn      uint32
	ShapeX, ShapeZ    int16
	Killed, Respawn   uint8
	Anchor3           [3]float32
	EyeHP, Type, Blur uint8
	Body4Center       [3]float32
	HitCool           int16
	SecondMorf, EyeTg uint32
	EyeCenter         [3]float32
}

func mtSample(t *testing.T, f mtFixture) (KindSample, []readSpan, error) {
	t.Helper()
	m := mem.NewFakeMem()
	p := kindPut{t, m}
	e := kindActor(p, KindMT, addr.ActorSetIDNone, 0xFF02FF00)
	a := blkActor
	p.b(a+addr.ActorOffHealth, byte(f.Health))
	p.u32(a+addr.ActorOffAttnFlags, f.Attn)
	p.u16(a+addr.ActorOffShapeAngle, uint16(f.ShapeX))
	p.u16(a+addr.ActorOffShapeAngle+2, 0x2000)
	p.u16(a+addr.ActorOffShapeAngle+4, uint16(f.ShapeZ))
	for i, v := range f.EyeCenter {
		p.f32(a+addr.ActorOffEyePos+uint32(4*i), v)
	}
	p.b(a+addr.MtOffType, f.Type)
	p.b(a+addr.MtOffRespawn, f.Respawn)
	p.b(a+addr.MtOffAppear, f.Appear)
	p.u32(a+addr.MtOffMorf, blkMorf)
	p.u32(a+addr.MtOffMorf+4, f.SecondMorf)
	p.b(a+addr.MtOffColorPhase, f.Phase)
	p.u32(a+addr.MtOffColorA, uint32(f.ColorA))
	p.u32(a+addr.MtOffColorB, uint32(f.ColorB))
	for i, v := range f.Anchor3 {
		p.f32(a+addr.MtOffAnchor+3*12+uint32(4*i), v)
	}
	p.b(a+addr.MtOffMode, f.Mode)
	p.b(a+addr.MtOffAction, f.Action)
	p.u16(a+addr.MtOffHitCool, uint16(f.HitCool))
	p.u16(a+addr.MtOffBallTimer, uint16(f.BallTimer))
	p.u16(a+addr.MtOffDive, uint16(f.Dive))
	p.u32(a+addr.MtOffBodyShieldSPrm, f.Shield)
	putCollider(p, a+addr.MtOffBodySph+4*addr.MtBodySphStride, 1, 1, f.Body4Center, 25, 0)
	putCollider(p, a+addr.MtOffEyeSph, f.EyeTg, 0, f.EyeCenter, 30, 0)
	p.b(a+addr.MtOffBlur, f.Blur)
	p.b(a+addr.MtOffDeath, byte(f.Death))
	p.b(a+addr.MtOffEyeHP, f.EyeHP)
	p.b(a+addr.MtOffIceShrink, byte(f.Ice))
	p.b(a+addr.MtOffKilled, f.Killed)
	kindMorf(p)
	r := &readLog{m: m}
	s, err := ReadSampleMT(r, &e)
	return s, r.reads, err
}

// TestReadSampleMT: five Reads (batch C "mt".stream), AngleY shape.y,
// Tilt shape.x, the aux words (mode | action << 8, the ball timer, the
// frames' low bytes, shape.z), the 9 spheres and what P0-MT prints.
func TestReadSampleMT(t *testing.T) {
	s, reads, err := mtSample(t, mtFixture{Health: 8, Mode: MtModeFight, Action: 1, Phase: 2, ColorA: 0x82, ColorB: 0x178, BallTimer: 40,
		ShapeX: -0x300, ShapeZ: 0x44, Respawn: 1, Anchor3: [3]float32{5, 6, 7}, EyeHP: 2, Body4Center: [3]float32{9, 8, 7}, HitCool: 3,
		SecondMorf: 0x80610000, EyeTg: 1, EyeCenter: [3]float32{1, 2, 3}, Attn: 4})
	if err != nil {
		t.Fatal(err)
	}
	checkReads(t, "MT", reads, []readSpan{
		{blkActor, 0x290},          // R1 the actor
		{blkActor + 0x2B4, 0x1DC},  // R2 type .. the dive timer
		{blkActor + 0xE48, 0xA8C},  // R3 the 9 spheres
		{blkActor + 0x18D4, 0x3EC}, // R4 the blur .. the class end
		{blkMorf + 0x54, 0x2C},     // R5 the head morf
	})
	if len(reads) != MtReadsPerSample {
		t.Errorf("%d Reads, MtReadsPerSample %d", len(reads), MtReadsPerSample)
	}
	if s.Kind != KindMT || s.Proc != addr.ProcMT || s.AngleY != 0x2000 || s.Tilt != -0x300 || s.Mode != MtModeFight || s.Action != 1 ||
		s.Aux != [4]int16{MtModeFight | 1<<8, 40, 0x82 | 0x78<<8, 0x44} {
		t.Errorf("slot fields: angleY 0x%X tilt %d mode %d action %d aux %v", s.AngleY, s.Tilt, s.Mode, s.Action, s.Aux)
	}
	mt := s.Mt
	if mt.Phase != 2 || mt.Respawn != 1 || mt.EyeHP != 2 || mt.HitCool != 3 || mt.Morfs[0] != blkMorf || mt.Morfs[1] != 0x80610000 ||
		mt.Anchor[3] != [3]float32{5, 6, 7} || mt.EyePos != [3]float32{1, 2, 3} || mt.ColorB != 0x178 {
		t.Errorf("mt fields %+v", mt)
	}
	cs := s.St.Colliders
	if len(cs) != 9 || cs[4].Name != "body4" || cs[4].Center != [3]float32{9, 8, 7} || !cs[4].CoSet() || cs[8].Name != "eye" ||
		cs[8].Off != addr.MtOffEyeSph || cs[8].R != 30 || !cs[8].TgSet() {
		t.Errorf("colliders %+v", cs)
	}
	if s.Flags != 0 || s.Flags2 != 2|MtF2Attn || s.St.Dormant || !s.MorfOK {
		t.Errorf("flags 0x%02X flags2 0x%02X", s.Flags, s.Flags2)
	}
}

// TestReadSampleMTFlags sets one source at a time.
func TestReadSampleMTFlags(t *testing.T) {
	base := mtFixture{Health: 8, Mode: MtModeMove}
	for _, c := range []struct {
		name   string
		set    func(*mtFixture)
		flags  uint8
		flags2 uint8
	}{
		{"nothing", func(*mtFixture) {}, 0, 0},
		{"BALL is not dying (custody)", func(f *mtFixture) { f.Mode = MtModeBall }, 0, 0},
		{"DEAD", func(f *mtFixture) { f.Mode = MtModeDead }, SlotDying, 0},
		{"death flag 1 is not dying", func(f *mtFixture) { f.Death = 1 }, 0, 0},
		{"death flag 2 (bakuha)", func(f *mtFixture) { f.Death = 2 }, SlotDying, 0},
		{"death flag -1 (after bakuha) is not", func(f *mtFixture) { f.Death = -1 }, 0, 0},
		{"light shrink", func(f *mtFixture) { f.Ice = 1 }, SlotDying, KindF2Light},
		{"appear wait", func(f *mtFixture) { f.Appear = 0xE4 }, SlotHidden, 0},
		{"phase 1", func(f *mtFixture) { f.Phase = 1 }, 0, 1},
		{"shield", func(f *mtFixture) { f.Shield = 0x11 }, 0, MtF2Shield},
		{"dive", func(f *mtFixture) { f.Dive = 9 }, 0, MtF2Dive},
		{"attention", func(f *mtFixture) { f.Attn = 4 }, 0, MtF2Attn},
	} {
		f := base
		c.set(&f)
		s, _, err := mtSample(t, f)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if s.Flags != c.flags || s.Flags2 != c.flags2 {
			t.Errorf("%s: Flags 0x%02X Flags2 0x%02X, want 0x%02X 0x%02X", c.name, s.Flags, s.Flags2, c.flags, c.flags2)
		}
	}
}

// TestMtDeathSwitch: a Magtail's quiet death sets its mDeathSwitch
// (+0x2B9) on the client as its own death would; a Kargaroc has none.
func TestMtDeathSwitch(t *testing.T) {
	if DeathSwitchOff(KindMT) != addr.MtOffDeathSwitch || DeathSwitchOff(KindBB) != 0 {
		t.Errorf("DeathSwitchOff mt 0x%X bb 0x%X", DeathSwitchOff(KindMT), DeathSwitchOff(KindBB))
	}
}

// TestHitApplierMtKill: a Magtail's code-4 request carries no remote-Link
// ATTRIB bits (no body swung); its collider-2 hit does.
func TestHitApplierMtKill(t *testing.T) {
	a := newHitApplier(mem.NewFakeMem(), nil, 7)
	a.Attrib = func(*Hit) uint8 { return mailbox.RemoteHAFAttribBody }
	kill := a.hitApply(&pendingHit{kind: KindMT, hit: Hit{Collider: HitMtKill}, seq: 1, pid: 9})
	body := a.hitApply(&pendingHit{kind: KindMT, hit: Hit{Collider: HitTg}, seq: 2, pid: 9})
	if kill.Flags&mailbox.RemoteHAFAttribBody != 0 || kill.Collider != HitMtKill || body.Flags&mailbox.RemoteHAFAttribBody == 0 {
		t.Errorf("code 4 flags 0x%02X, collider 2 flags 0x%02X", kill.Flags, body.Flags)
	}
}

// TestMtHits: code 4 is the custody death only for MT; a collider-2 hit
// names the body sphere whose Tg hit object is set.
func TestMtHits(t *testing.T) {
	if !mtKillHit(KindMT, HitMtKill) || mtKillHit(KindBB, HitMtKill) || mtKillHit(KindMT, HitTg) {
		t.Error("mtKillHit")
	}
	m := mem.NewFakeMem()
	p := kindPut{t, m}
	if got := mtHitCollider(m, blkActor); got != addr.MtOffBodySph {
		t.Errorf("no hit object: 0x%X, want sph[0]", got)
	}
	sph5 := uint32(addr.MtOffBodySph + 5*addr.MtBodySphStride)
	p.u32(blkActor+sph5+addr.Mo2CcTgHitObjOff, 0x80400000)
	if got := mtHitCollider(m, blkActor); got != sph5 {
		t.Errorf("sph[5] hit: 0x%X, want 0x%X", got, sph5)
	}
}
