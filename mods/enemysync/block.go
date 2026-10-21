package enemysync

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"slices"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// What the host sampler and the client matcher share: the private MO2
// walk layout, the scope reads (stage, room, in play), the room's
// dead-actor mask and one Moblin's per-tick sample.

// ProcMO2 is the Moblin's proc (fpcNm_MO2_e, f_pc_name.h:198).
const ProcMO2 = uint16(stage.ProcMO2)

// Layouts is the private walk layout: a Moblin's enemyice and its u8
// mMode. combat.Layouts() is shared and untouched.
var Layouts = actors.Layouts{int16(stage.ProcMO2): {Ice: addr.Mo2OffIce, Mode: addr.Mo2OffMode, ModeU8: true}}

// Scope is where a game is: its stage and room, and whether it is in play
// (feed.inPlay's rule: Link loaded, not the title or file select, no
// stage change pending).
type Scope struct {
	Stage  string
	Room   int8
	InPlay bool
	// Pending is mNextStage.mEnable: a stage change is under way.
	Pending bool
}

// ReadScope reads the scope from m: PlayerPtr, CurStageName..NextStageEnable
// in one read, and mStayNo.
func ReadScope(m mem.Reader) (Scope, error) {
	var s Scope
	link, err := mem.ReadU32(m, addr.PlayerPtr)
	if err != nil {
		return s, err
	}
	n := int(addr.NextStageEnable-addr.CurStageName) + 1
	b, err := m.Read(addr.CurStageName, n)
	if err != nil {
		return s, err
	}
	if len(b) != n {
		return s, fmt.Errorf("enemysync: stage read %d bytes, want %d", len(b), n)
	}
	name := b[:addr.StageNameLen]
	if i := bytes.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	s.Stage = string(name)
	s.Pending = b[n-1] != 0
	room, err := mem.ReadS8(m, addr.StayNo)
	if err != nil {
		return s, err
	}
	s.Room = room
	s.InPlay = addr.InMEM1(link) && !s.Pending && s.Stage != "" && s.Stage != addr.StageTitle && s.Stage != addr.StageFileSelect
	return s, nil
}

// ReadDeadWords reads the room's dead-actor words 0..DeadWords-1 (setIDs
// 0..127; bit n of word w = setID 32*w+n), the way the co-op kill path
// addresses them: mStatus[room].mZoneNo names the zone, whose
// dSv_zoneActor_c words sit at ZoneOffActor (dSv_zoneActor_c::on,
// d_save.cpp:1326-1329). One read of the four words. A room without a
// zone has no deaths recorded: all zero. The STATE carries them (State.
// SetDead): the highest keyed ALLOW setID measured is 81 (design
// death_and_drops.signals), which DeadMask's eight bits never covered.
func ReadDeadWords(m mem.Reader, room int8) ([DeadWords]uint32, error) {
	var w [DeadWords]uint32
	zn, err := addr.ZoneNoAddr(int(room))
	if err != nil {
		return w, nil
	}
	z, err := mem.ReadS8(m, zn)
	if err != nil {
		return w, err
	}
	za, err := addr.ZoneAddr(int(z))
	if err != nil {
		return w, nil // -1: no zone
	}
	b, err := m.Read(za+addr.ZoneOffActor, 4*DeadWords)
	if err != nil {
		return w, err
	}
	if len(b) != 4*DeadWords {
		return w, fmt.Errorf("enemysync: zone %d dead-actor words: %d bytes read", z, len(b))
	}
	for i := range w {
		w[i] = binary.BigEndian.Uint32(b[4*i:])
	}
	return w, nil
}

// DeadMask is the low byte of ReadDeadWords' word 0: the dead-actor bits of
// keyed setIDs 0..7 (bit n = setID n), the Moblin prototype's dead_mask.
func DeadMask(m mem.Reader, room int8) (uint8, error) {
	w, err := ReadDeadWords(m, room)
	return uint8(w[0]), err
}

// Moblin is a keyed, executing Moblin a walk found, with an mMode the
// mirror takes (MirrorsMode).
type Moblin struct {
	Addr, PID uint32
	SetID     uint16
	HomeRoom  int8
	Mode      uint8 // mo2 mMode (addr.Mo2OffMode)
}

// MirrorsMode reports whether a Moblin with mo2 mMode mode is one the
// mirror takes: 0 (patrol) and 1 (the lantern variant: M_Dai room 1's
// setIDs 0 and 1 carry params FF00FF01 and FF01FF01, so create's low
// byte makes them mode 1, d_a_mo2.cpp:4080, and they behave as normal
// Moblins once the player has a pearl, d_a_mo2.cpp:4134-4146). A carried
// one (addr.Mo2ModeCarried) and a statue (addr.Mo2ModeStatue) are
// refused, as the REL refuses them (WWENEMY_ERR_MODE).
func MirrorsMode(mode int32) bool { return mode == 0 || mode == 1 }

// KeyedMoblins walks the actors with Layouts and returns the keyed
// (setID < 0x200, home room 0..63), executing (initState 2) Moblins of
// a mode the mirror takes (MirrorsMode) whose home room is room, sorted
// by setID, at most MaxSlots of them (the lowest setIDs).
func KeyedMoblins(m mem.Reader, room int8) ([]Moblin, error) {
	w, err := actors.ReadWith(m, 0, Layouts)
	if err != nil {
		return nil, err
	}
	var out []Moblin
	for i := range w.Actors {
		a := &w.Actors[i]
		if a.Proc == int16(stage.ProcMO2) && a.Keyed() && a.InitState == 2 && a.ModeOK && MirrorsMode(a.Mode) && a.HomeRoom == room {
			out = append(out, Moblin{Addr: a.Addr, PID: a.PID, SetID: a.SetID, HomeRoom: a.HomeRoom, Mode: uint8(a.Mode)})
		}
	}
	slices.SortFunc(out, func(a, b Moblin) int { return int(a.SetID) - int(b.SetID) })
	if len(out) > MaxSlots {
		out = out[:MaxSlots]
	}
	return out, nil
}

// Sample is one Moblin's fields as a tick read them: the wire Slot plus
// what the host keeps for itself. The Slot's Bck, AnmSeq and Morf are
// not read but derived (Tracker.Derive) from the raw morf fields here.
type Sample struct {
	Slot
	IceTimer int8
	Hidden   bool
	// The raw morf fields the detector works from (ReadSample's R5 span):
	// MorfOK is false when mpMorf was unreadable, and then AnmAttr is
	// AnmAttrUnknown.
	MorfOK    bool
	Anm       uint32  // mpAnm (mpMorf+0x54)
	MorfState uint8   // J3DFrameCtrl mState
	CurMorf   float32 // mCurMorf (+0x74)
	MorfStep  float32 // mMorfStep (+0x7C)
	// MorfEnd is J3DFrameCtrl mEnd, the animation's last frame: the kind
	// samplers' readMorfAnim fills it (the probes print it; A0 measures
	// the Armos Knight's DEAD length with it), the Moblin's ReadSample
	// leaves it 0.
	MorfEnd int16
}

// The per-tick reads of one Moblin (design "host_side", SAMPLER): six
// Reads, R1 the actor (addr.ActorSize at +0), R2 the span below from the
// blur scale through mbHasInnateWeapon, R3 the attack type and phase, R4
// mpMorf, R5 the morf's animation span (addr.McaMorfAnimSpanOff..Len) and
// R6 the tail from m2943 through the ice timer.
const (
	readMidOff  = addr.Mo2OffBlurScale                             // f32 m0590 .. s8 mbHasInnateWeapon (+0xDD4)
	readMidLen  = addr.Mo2OffHasWeapon + 1 - addr.Mo2OffBlurScale  // 0x845
	readTailOff = addr.Mo2OffBareHanded                            // u8 m2943 .. s8 m2A1C .. ice timer
	readTailLen = addr.Mo2OffIceShrink + 1 - addr.Mo2OffBareHanded // 0x118
	// ReadsPerSample is how many Reads ReadSample makes.
	ReadsPerSample = 6
)

// ReadSample reads mo's fields for one tick. It checks the actor is
// still mo (process ID, proc, initState 2, setID); a mismatch is
// actors.ErrChanged and the caller drops the entry and walks again.
// Bck, AnmSeq and Morf are left for Tracker.Derive.
func ReadSample(m mem.Reader, mo *Moblin) (Sample, error) {
	var s Sample
	b, err := m.Read(mo.Addr, addr.ActorSize)
	if err != nil {
		return s, err
	}
	if len(b) != addr.ActorSize {
		return s, fmt.Errorf("enemysync: actor 0x%08X: %d bytes read", mo.Addr, len(b))
	}
	be := binary.BigEndian
	if be.Uint32(b[addr.ActorOffBsPcID:]) != mo.PID || be.Uint16(b[addr.ActorOffProcName:]) != ProcMO2 ||
		int8(b[addr.ActorOffInitState]) != 2 || be.Uint16(b[addr.ActorOffSetID:]) != mo.SetID || int8(b[addr.ActorOffHomeRoomNo]) != mo.HomeRoom {
		return s, fmt.Errorf("%w: Moblin setID %d pid %d at 0x%08X", actors.ErrChanged, mo.SetID, mo.PID, mo.Addr)
	}
	s.SetID, s.Proc = mo.SetID, ProcMO2
	for i := range 3 {
		s.Pos[i] = math.Float32frombits(be.Uint32(b[addr.ActorOffCurPos+4*i:]))
	}
	s.AngleY = int16(be.Uint16(b[addr.ActorOffCurAngle+2:]))
	s.Health = int8(b[addr.ActorOffHealth])

	d, err := m.Read(mo.Addr+readMidOff, readMidLen)
	if err != nil {
		return s, err
	}
	if len(d) != readMidLen {
		return s, fmt.Errorf("enemysync: actor 0x%08X: short blur/damagereaction read", mo.Addr)
	}
	mid := func(off uint32) []byte { return d[off-readMidOff:] }
	s.BlurScale = math.Float32frombits(be.Uint32(mid(addr.Mo2OffBlurScale)))
	if !finiteOK(s.BlurScale) {
		s.BlurScale = 1
	}
	blurOn := be.Uint32(mid(addr.Mo2OffBlurOn)) != 0
	s.BlurShape = uint8(be.Uint32(mid(addr.Mo2OffBlurShape)))
	anmGate := be.Uint16(mid(addr.Mo2OffAnmGate)) != 0
	playSkip := be.Uint16(mid(addr.Mo2OffPlaySkip)) != 0
	invuln := int16(be.Uint16(mid(addr.Mo2OffInvuln)))
	s.Mode = int16(be.Uint16(mid(addr.Mo2OffDRMode)))
	s.Action = int16(be.Uint16(mid(addr.Mo2OffDRAction)))
	s.Tilt = int16(be.Uint16(mid(addr.Mo2OffTilt)))
	hasWeapon := mid(addr.Mo2OffHasWeapon)[0] != 0
	if hasWeapon {
		s.Flags2 |= SlotF2HasWeapon
	}
	if playSkip {
		s.Flags2 |= SlotF2PlaySkip
	}
	if blurOn {
		s.Flags2 |= SlotF2BlurOn
	}
	if anmGate {
		s.Flags2 |= SlotF2AnmGate
	}
	atk, err := m.Read(mo.Addr+addr.Mo2OffAttackType, 8)
	if err != nil {
		return s, err
	}
	if len(atk) == 8 {
		s.AttackType, s.AttackPhase = atk[3], atk[7]
	}
	morf, err := mem.ReadU32(m, mo.Addr+addr.Mo2OffMorf)
	if err != nil {
		return s, err
	}
	s.Bck, s.AnmAttr = addr.Mo2BckNone, AnmAttrUnknown
	if at := morf + addr.McaMorfAnimSpanOff; addr.InMEM1(morf) && mem.CheckRange(at, addr.McaMorfAnimSpanLen) == nil {
		fc, err := m.Read(at, addr.McaMorfAnimSpanLen)
		if err != nil {
			return s, err
		}
		if len(fc) == addr.McaMorfAnimSpanLen {
			f := func(off uint32) []byte { return fc[off-addr.McaMorfAnimSpanOff:] }
			s.MorfOK = true
			s.Anm = be.Uint32(f(addr.McaMorfOffAnm))
			s.AnmAttr = f(addr.McaMorfOffAttr)[0]
			s.MorfState = f(addr.McaMorfOffState)[0]
			s.MorfRate = math.Float32frombits(be.Uint32(f(addr.McaMorfOffRate)))
			s.MorfFrame = math.Float32frombits(be.Uint32(f(addr.McaMorfOffFrame)))
			s.CurMorf = math.Float32frombits(be.Uint32(f(addr.McaMorfOffCurMorf)))
			s.MorfStep = math.Float32frombits(be.Uint32(f(addr.McaMorfOffMorfStep)))
		}
	}
	tail, err := m.Read(mo.Addr+readTailOff, readTailLen)
	if err != nil {
		return s, err
	}
	if len(tail) != readTailLen {
		return s, fmt.Errorf("enemysync: actor 0x%08X: short tail read", mo.Addr)
	}
	bare := tail[0] != 0
	s.Hidden = tail[addr.Mo2OffHide-readTailOff] != 0
	s.IceTimer = int8(tail[addr.Mo2OffIceShrink-readTailOff])

	if s.Health <= 0 || s.Action == 20 || s.Action == 21 || s.IceTimer != 0 {
		s.Flags |= SlotDying
	}
	if invuln != 0 {
		s.Flags |= SlotInvuln
	}
	if s.Action == 10 {
		s.Flags |= SlotDefence
	}
	if s.Mode <= -100 {
		s.Flags |= SlotRagdoll
	}
	if bare {
		s.Flags |= SlotBareHanded
	}
	if s.Hidden {
		s.Flags |= SlotHidden
	}
	if !finiteOK(s.Pos[0], s.Pos[1], s.Pos[2], s.MorfFrame, s.MorfRate) {
		return s, fmt.Errorf("enemysync: Moblin setID %d: NaN or Inf read at 0x%08X", mo.SetID, mo.Addr)
	}
	return s, nil
}

func finiteOK(fs ...float32) bool { return finite(fs...) == nil }

// The generalised samplers (design "flags".host_reads BK and CC): the
// Bokoblin and the ChuChu each in five Reads, the Moblin through
// ReadSample's six, unchanged. The host samples every candidate through
// ReadKindSample; the probes (enemy-watch --kind) too.

// KindSample is one enemy's tick: the Moblin-shaped Sample (Slot fields
// per kind as the design's slot table maps them) plus what only the new
// kinds carry and what the probes print.
type KindSample struct {
	Sample
	Kind  KindID
	Ident Ident
	// TiltZ is the BK dr m48C.z (0x800) and the CC m316.z (0x31A).
	TiltZ int16
	// Aux is the annex's s16[4] (design block_layout annex): BK {m48C.z,
	// jaw m11F4, 0, 0}; CC {shape_angle.x, m310.x, m310.y, m310.z}.
	Aux    [4]int16
	Attn   uint32 // attention_info.flags (+0x280)
	Status uint32 // actor_status (+0x1C4)
	// The Bokoblin's: live mType, the BOKO pid m1200 and its attach flag
	// m1214, the demo gate m1234, dr mInvincibleTimer and the ice freeze
	// timer.
	BkType       uint8
	BokoPID      uint32
	BokoAttach   int8
	DemoGate     int8
	DRInvincible int16
	IceFreeze    int16
	// The ChuChu's: behaviour, colour, mCurrAction (also Action), m2F5
	// (also Mode), m2F7, m320 (the BCK index it last set, a cross-check of
	// Bck) and mCyl's At/Tg/Co mSPrm words.
	CcBehavior, CcColor, CcAction, CcSubstate, CcElectric uint8
	CcBck                                                 int32
	CcAtSPrm, CcTgSPrm, CcCoSPrm                          uint32
	// St is an allow kind's (KI, AM, AM2; allow design type_table samplers).
	St AllowFields
	// Tn is the staged Darknut's (ReadSampleTN, staged.go; St carries its
	// angles and colliders).
	Tn TnFields
}

// AllowFields is what ReadSampleKI, ReadSampleAM and ReadSampleAM2 read
// beyond the Slot fields: what batch A0 verified live (design
// implementation_batches A0), the host's dormant rule and what the
// enemy-watch probe prints.
type AllowFields struct {
	// CurAngle is current.angle (+0x204) and ShapeAngle shape_angle
	// (+0x20C), x/y/z: the samplers stream shape.y as AngleY (the design's
	// AngleOff 0x20E), and A0 checks how far current.y drifts from it.
	CurAngle, ShapeAngle [3]int16
	// HomePos is home.pos's raw bits: the Keese moves its home
	// (d_a_ki.cpp:681-682), which a keyed Keese's pkey must not hash (D9).
	HomePos [3]uint32
	Scale   float32 // scale.x (+0x214)
	// The Keese's: mBehaviorType (also Mode), m2D4 (the switch it waits
	// on, HIDDEN), m2DC (the shrink-delete), mParameters, mDamageType
	// (nonzero: the fire Keese), m314 (INVULN).
	Behavior                                int8
	DrawGate, ShrinkDelete, Params, DmgType uint8
	Invuln                                  int16
	// BckIdx is the Armos Knight's or the Armos's mCurrBckIdx (anm_init's
	// index, a cross-check of Bck); -1 for the Keese, which keeps none.
	BckIdx int32
	// The Armos Knight's: mCountDownTimers[1] (PLAY_SKIP), the swallowed
	// actor's pid, mbIsBodyBeingHit.
	PlaySkip   int16
	SwallowPID uint32
	BodyHit    uint8
	// The Armos's: mbIsWeakBeingHit, mbNotInHomeRoom, mInAbyssTimer,
	// mCountDownTimers[2] (the MAHI stun).
	WeakHit, NotHome, Abyss uint8
	Stun                    int16
	// Dormant is the WFlagDormant rule the design's wireSlot applies
	// (type_table sampler).
	Dormant bool
	// Colliders is every collider of the class in header order.
	Colliders []ColliderSample
}

// ColliderSample is one dCcD_Sph or dCcD_Cyl: its At/Tg/Co mSPrm words
// (bit 0 Set, c_cc_d.h:33,47), its centre, radius and (a cylinder's)
// height.
type ColliderSample struct {
	Name                   string
	Off                    uint32
	Cyl                    bool
	AtSPrm, TgSPrm, CoSPrm uint32
	Center                 [3]float32
	R, H                   float32
}

// TgSet and CoSet report the Tg and Co mSPrm Set bits.
func (c ColliderSample) TgSet() bool { return c.TgSPrm&addr.CcSPrmSet != 0 }
func (c ColliderSample) CoSet() bool { return c.CoSPrm&addr.CcSPrmSet != 0 }

// The Bokoblin's and ChuChu's spans.
const (
	bkReadMidOff  = addr.BkOffType                              // u8 mType .. dr mInvincibleTimer
	bkReadMidLen  = addr.BkOffDRInvincible + 2 - addr.BkOffType // 0x58C: through +0x840
	bkReadHoldOff = addr.BkOffWeaponHold                        // int m0B30
	bkReadHoldLen = 4
	bkReadTailOff = addr.BkOffJaw                           // s16 m11F4 .. ice freeze timer
	bkReadTailLen = addr.BkOffIceFreeze + 2 - addr.BkOffJaw // 0x80: through +0x1274
	ccReadMidOff  = addr.CcOffMorf                          // m2B4 .. m34E[7]
	ccReadMidLen  = addr.CcOffTimersEnd - addr.CcOffMorf    // 0xA8: through +0x35C
	ccReadCylOff  = addr.CcOffCyl                           // mCyl At/Tg/Co mSPrm
	ccReadCylLen  = addr.CcOffCylSPrmEnd - addr.CcOffCyl    // 0x30
	ccReadIceOff  = addr.CcOffIce                           // enemyice
	ccReadIceLen  = addr.EnemyIceSize                       // 0x10
	// KindReadsPerSample is how many Reads ReadSampleBK and ReadSampleCC
	// make (four when the morf is not in MEM1).
	KindReadsPerSample = 5
)

// readKindActor is R1 for e: the actor, checked to still be e (process
// ID, proc, initState 2, setID, home room and the pkey of its params and
// home, under the kind's rule: PKeyFor, so a keyed Keese, which moves its
// home.pos (d_a_ki.cpp:681-682), is rechecked without it, allow design
// D9). A mismatch is actors.ErrChanged.
func readKindActor(m mem.Reader, e *Enemy, proc uint16) ([]byte, error) {
	b, err := m.Read(e.Addr, addr.ActorSize)
	if err != nil {
		return nil, err
	}
	if len(b) != addr.ActorSize {
		return nil, fmt.Errorf("enemysync: actor 0x%08X: %d bytes read", e.Addr, len(b))
	}
	be := binary.BigEndian
	var home [3]uint32
	for i := range home {
		home[i] = be.Uint32(b[addr.ActorOffHomePos+4*i:])
	}
	setID := be.Uint16(b[addr.ActorOffSetID:])
	room := int8(b[addr.ActorOffHomeRoomNo])
	if be.Uint32(b[addr.ActorOffBsPcID:]) != e.PID || be.Uint16(b[addr.ActorOffProcName:]) != proc || int8(b[addr.ActorOffInitState]) != 2 ||
		setID != e.Ident.SetID || room != e.HomeRoom || PKeyFor(KindOf(proc).Row(), proc, setID, be.Uint32(b[addr.ActorOffParameters:]), room, home) != e.Ident.PKey {
		return nil, fmt.Errorf("%w: %s pid %d at 0x%08X", actors.ErrChanged, e.Ident, e.PID, e.Addr)
	}
	return b, nil
}

// kindActorFields fills s's actor-level fields from R1: AngleY is the
// row's AngleOff (allow design D13: current.angle.y for MO2, BK and CC,
// shape_angle.y for KI, AM and AM2).
func kindActorFields(s *KindSample, b []byte) {
	be := binary.BigEndian
	for i := range 3 {
		s.Pos[i] = math.Float32frombits(be.Uint32(b[addr.ActorOffCurPos+4*i:]))
	}
	angleOff := uint32(addr.ActorOffCurAngleY)
	if k := s.Kind.Row(); k != nil {
		angleOff = k.AngleOff
	}
	s.AngleY = int16(be.Uint16(b[angleOff:]))
	s.Health = int8(b[addr.ActorOffHealth])
	s.Attn = be.Uint32(b[addr.ActorOffAttnFlags:])
	s.Status = be.Uint32(b[addr.ActorOffStatus:])
}

// readMorfAnim is R5: the morf's animation span, when morf is in MEM1.
func readMorfAnim(m mem.Reader, morf uint32, s *Sample) error {
	s.Bck, s.AnmAttr = addr.Mo2BckNone, AnmAttrUnknown
	at := morf + addr.McaMorfAnimSpanOff
	if !addr.InMEM1(morf) || mem.CheckRange(at, addr.McaMorfAnimSpanLen) != nil {
		return nil
	}
	fc, err := m.Read(at, addr.McaMorfAnimSpanLen)
	if err != nil {
		return err
	}
	if len(fc) != addr.McaMorfAnimSpanLen {
		return nil
	}
	be := binary.BigEndian
	f := func(off uint32) []byte { return fc[off-addr.McaMorfAnimSpanOff:] }
	s.MorfOK = true
	s.Anm = be.Uint32(f(addr.McaMorfOffAnm))
	s.AnmAttr = f(addr.McaMorfOffAttr)[0]
	s.MorfState = f(addr.McaMorfOffState)[0]
	s.MorfRate = math.Float32frombits(be.Uint32(f(addr.McaMorfOffRate)))
	s.MorfFrame = math.Float32frombits(be.Uint32(f(addr.McaMorfOffFrame)))
	s.CurMorf = math.Float32frombits(be.Uint32(f(addr.McaMorfOffCurMorf)))
	s.MorfStep = math.Float32frombits(be.Uint32(f(addr.McaMorfOffMorfStep)))
	s.MorfEnd = int16(be.Uint16(f(addr.McaMorfOffEnd)))
	return nil
}

// ReadSampleBK reads a Bokoblin's tick in five Reads (design host_reads
// BK): R1 the actor, R2 +0x2B4..+0x840 (mType .. dr mInvincibleTimer),
// R3 m0B30, R4 +0x11F4..+0x1274 (jaw .. ice freeze timer), R5 the morf's
// animation span. Flags (design "flags"): DYING dr.mAction in {20, 21,
// 22} (fail, yogan_fail, water_fail, d_a_bk.cpp:3424-3432) or the ice
// shrink timer runs, never health alone; INVULN m030E; DEFENCE action 10;
// RAGDOLL dr.mMode <= -100; HIDDEN m02BA, m02DE or m121C
// (d_a_bk.cpp:526).
func ReadSampleBK(m mem.Reader, e *Enemy) (KindSample, error) {
	s := KindSample{Kind: KindBK, Ident: e.Ident}
	b, err := readKindActor(m, e, addr.ProcBK)
	if err != nil {
		return s, err
	}
	s.SetID, s.Proc = e.Ident.SetID, addr.ProcBK
	kindActorFields(&s, b)
	be := binary.BigEndian

	d, err := m.Read(e.Addr+bkReadMidOff, bkReadMidLen)
	if err != nil {
		return s, err
	}
	if len(d) != bkReadMidLen {
		return s, fmt.Errorf("enemysync: Bokoblin 0x%08X: short mType..dr read", e.Addr)
	}
	mid := func(off uint32) []byte { return d[off-bkReadMidOff:] }
	s.BkType = mid(addr.BkOffType)[0]
	hidden := mid(addr.BkOffDrawGate)[0] != 0 || int8(mid(addr.BkOffHide2)[0]) != 0
	morf := be.Uint32(mid(addr.BkOffMorf))
	s.BlurScale = math.Float32frombits(be.Uint32(mid(addr.BkOffBlurScale)))
	if !finiteOK(s.BlurScale) {
		s.BlurScale = 1
	}
	blurOn := be.Uint32(mid(addr.BkOffBlurOn)) != 0
	s.BlurShape = uint8(be.Uint32(mid(addr.BkOffBlurShape)))
	playSkip := be.Uint16(mid(addr.BkOffPlaySkip)) != 0
	invuln := be.Uint16(mid(addr.BkOffInvuln)) != 0
	s.Mode = int16(be.Uint16(mid(addr.BkOffDRMode)))
	s.Action = int16(be.Uint16(mid(addr.BkOffDRAction)))
	s.Tilt = int16(be.Uint16(mid(addr.BkOffTilt)))
	s.TiltZ = int16(be.Uint16(mid(addr.BkOffTiltZ)))
	s.DRInvincible = int16(be.Uint16(mid(addr.BkOffDRInvincible)))

	h, err := m.Read(e.Addr+bkReadHoldOff, bkReadHoldLen)
	if err != nil {
		return s, err
	}
	if len(h) != bkReadHoldLen {
		return s, fmt.Errorf("enemysync: Bokoblin 0x%08X: short m0B30 read", e.Addr)
	}
	hasWeapon := be.Uint32(h) != 0

	tail, err := m.Read(e.Addr+bkReadTailOff, bkReadTailLen)
	if err != nil {
		return s, err
	}
	if len(tail) != bkReadTailLen {
		return s, fmt.Errorf("enemysync: Bokoblin 0x%08X: short jaw..ice read", e.Addr)
	}
	t := func(off uint32) []byte { return tail[off-bkReadTailOff:] }
	jaw := int16(be.Uint16(t(addr.BkOffJaw)))
	s.BokoPID = be.Uint32(t(addr.BkOffBokoPID))
	s.BokoAttach = int8(t(addr.BkOffBokoAttach)[0])
	hidden = hidden || int8(t(addr.BkOffDeleteReq)[0]) != 0
	s.DemoGate = int8(t(addr.BkOffDemoGate)[0])
	s.IceTimer = int8(t(addr.BkOffIceShrink)[0])
	s.IceFreeze = int16(be.Uint16(t(addr.BkOffIceFreeze)))
	s.Hidden = hidden
	s.Aux = [4]int16{s.TiltZ, jaw, 0, 0}

	if err := readMorfAnim(m, morf, &s.Sample); err != nil {
		return s, err
	}

	if s.Action == 20 || s.Action == 21 || s.Action == 22 || s.IceTimer != 0 {
		s.Flags |= SlotDying
	}
	if invuln {
		s.Flags |= SlotInvuln
	}
	if s.Action == 10 {
		s.Flags |= SlotDefence
	}
	if s.Mode <= -100 {
		s.Flags |= SlotRagdoll
	}
	if hidden {
		s.Flags |= SlotHidden
	}
	if hasWeapon {
		s.Flags2 |= BkF2HasWeapon
	}
	if playSkip {
		s.Flags2 |= BkF2PlaySkip
	}
	if blurOn {
		s.Flags2 |= BkF2BlurOn
	}
	if s.Attn != 0 {
		s.Flags2 |= BkF2Attn
	}
	if s.Flags&SlotDying != 0 && s.IceTimer != 0 {
		s.Flags2 |= KindF2Light
	}
	if !finiteOK(s.Pos[0], s.Pos[1], s.Pos[2], s.MorfFrame, s.MorfRate) {
		return s, fmt.Errorf("enemysync: Bokoblin %s: NaN or Inf read at 0x%08X", e.Ident, e.Addr)
	}
	return s, nil
}

// ReadSampleCC reads a ChuChu's tick in five Reads (design host_reads
// CC): R1 the actor, R2 +0x2B4..+0x35C (m2B4 .. m34E[7]), R3 mCyl's
// At/Tg/Co mSPrm words (+0x6BC..+0x6EC), R4 the enemyice (+0x81C, 0x10),
// R5 the morf's animation span. Flags: DYING action 4, the ice shrink
// timer, or m2F5 0x35; INVULN the Tg Set bit clear; DEFENCE, RAGDOLL,
// HIDDEN and BARE never (m2F9 is a shadow/BG skip, d_a_cc.cpp:168-174).
// flags2: Tg and Co Set, the squash (d_a_cc.cpp:88), the low eye (m2F5 in
// [6, 11) or action 1), m2F9, attention.
func ReadSampleCC(m mem.Reader, e *Enemy) (KindSample, error) {
	s := KindSample{Kind: KindCC, Ident: e.Ident}
	b, err := readKindActor(m, e, addr.ProcCC)
	if err != nil {
		return s, err
	}
	s.SetID, s.Proc = e.Ident.SetID, addr.ProcCC
	kindActorFields(&s, b)
	be := binary.BigEndian
	shapeX := int16(be.Uint16(b[addr.ActorOffShapeAngle:]))

	d, err := m.Read(e.Addr+ccReadMidOff, ccReadMidLen)
	if err != nil {
		return s, err
	}
	if len(d) != ccReadMidLen {
		return s, fmt.Errorf("enemysync: ChuChu 0x%08X: short m2B4..m34E read", e.Addr)
	}
	mid := func(off uint32) []byte { return d[off-ccReadMidOff:] }
	morf := be.Uint32(mid(addr.CcOffMorf))
	s.CcBehavior = mid(addr.CcOffBehavior)[0]
	s.CcColor = mid(addr.CcOffColor)[0]
	s.CcAction = mid(addr.CcOffAction)[0]
	s.CcSubstate = mid(addr.CcOffSubstate)[0]
	s.CcElectric = mid(addr.CcOffElectric)[0]
	noShadow := mid(addr.CcOffNoShadowBG)[0] != 0
	split := mid(addr.CcOffSplit)[0]
	s.Action, s.Mode = int16(s.CcAction), int16(s.CcSubstate)
	s.Aux = [4]int16{shapeX,
		int16(be.Uint16(mid(addr.CcOffWobble))), int16(be.Uint16(mid(addr.CcOffWobble + 2))), int16(be.Uint16(mid(addr.CcOffWobble + 4)))}
	s.Tilt = int16(be.Uint16(mid(addr.CcOffGroundTilt)))
	s.TiltZ = int16(be.Uint16(mid(addr.CcOffGroundTiltZ)))
	s.CcBck = int32(be.Uint32(mid(addr.CcOffBckIndex)))
	s.BlurScale = math.Float32frombits(be.Uint32(mid(addr.CcOffYOffset)))
	if !finiteOK(s.BlurScale) {
		s.BlurScale = 0
	}
	stun := be.Uint16(mid(addr.CcOffStunTimer))

	cyl, err := m.Read(e.Addr+ccReadCylOff, ccReadCylLen)
	if err != nil {
		return s, err
	}
	if len(cyl) != ccReadCylLen {
		return s, fmt.Errorf("enemysync: ChuChu 0x%08X: short mCyl read", e.Addr)
	}
	s.CcAtSPrm = be.Uint32(cyl[addr.CcCylOffAtSPrm:])
	s.CcTgSPrm = be.Uint32(cyl[addr.CcCylOffTgSPrm:])
	s.CcCoSPrm = be.Uint32(cyl[addr.CcCylOffCoSPrm:])

	ice, err := m.Read(e.Addr+ccReadIceOff, ccReadIceLen)
	if err != nil {
		return s, err
	}
	if len(ice) != ccReadIceLen {
		return s, fmt.Errorf("enemysync: ChuChu 0x%08X: short enemyice read", e.Addr)
	}
	s.IceTimer = int8(ice[addr.EnemyIceOffTimer])
	s.IceFreeze = int16(be.Uint16(ice[addr.EnemyIceOffFreezeTimer:]))

	if err := readMorfAnim(m, morf, &s.Sample); err != nil {
		return s, err
	}

	if s.CcAction == addr.CcActionDying || s.IceTimer != 0 || s.CcSubstate == addr.CcSubstateDying {
		s.Flags |= SlotDying
	}
	tg := s.CcTgSPrm&addr.CcSPrmSet != 0
	if tg {
		s.Flags2 |= CcF2Tg
	} else {
		s.Flags |= SlotInvuln
	}
	if s.CcCoSPrm&addr.CcSPrmSet != 0 {
		s.Flags2 |= CcF2Co
	}
	if s.CcAction != addr.CcActionNoSquash1 && s.CcAction != addr.CcActionNoSquash2 && s.CcSubstate != addr.CcSubstateDying && split == 0 && stun == 0 {
		s.Flags2 |= CcF2Squash
	}
	if (s.CcSubstate >= 6 && s.CcSubstate < 11) || s.CcAction == addr.CcActionLowEye {
		s.Flags2 |= CcF2LowEye
	}
	if noShadow {
		s.Flags2 |= CcF2NoShadow
	}
	if s.Attn != 0 {
		s.Flags2 |= CcF2Attn
	}
	if s.Flags&SlotDying != 0 && s.IceTimer != 0 {
		s.Flags2 |= KindF2Light
	}
	if !finiteOK(s.Pos[0], s.Pos[1], s.Pos[2], s.MorfFrame, s.MorfRate) {
		return s, fmt.Errorf("enemysync: ChuChu %s: NaN or Inf read at 0x%08X", e.Ident, e.Addr)
	}
	return s, nil
}

// ReadKindSample samples e by its kind: a Moblin through ReadSample (its
// six Reads, byte-identical to the host's), a Bokoblin through
// ReadSampleBK, a ChuChu through ReadSampleCC, a Keese, an Armos Knight
// and an Armos through ReadSampleKI, ReadSampleAM and ReadSampleAM2.
func ReadKindSample(m mem.Reader, e *Enemy) (KindSample, error) {
	switch e.Kind {
	case KindMO2:
		mo := Moblin{Addr: e.Addr, PID: e.PID, SetID: e.Ident.SetID, HomeRoom: e.HomeRoom, Mode: e.Live.Mode}
		s, err := ReadSample(m, &mo)
		return KindSample{Sample: s, Kind: KindMO2, Ident: e.Ident, Attn: 0}, err
	case KindBK:
		return ReadSampleBK(m, e)
	case KindCC:
		return ReadSampleCC(m, e)
	case KindKI:
		return ReadSampleKI(m, e)
	case KindAM:
		return ReadSampleAM(m, e)
	case KindAM2:
		return ReadSampleAM2(m, e)
	}
	return KindSample{}, fmt.Errorf("enemysync: no sampler for kind %s (proc 0x%04X)", e.Kind, e.Ident.Proc)
}

// The allow kinds' samplers (enemy-mirror-allow.json "type_table" KI,
// AM, AM2 sampler; batch A0, rows of Kinds since A1, ReadKindSample's).
// Each makes five Reads: R1 the actor, R2
// the class's mid span, R3 its colliders in one span, R4 the enemyice,
// R5 the morf's animation span (the Keese one more, its u8 mDamageType).
// AngleY is shape.y (the design's AngleOff 0x20E); St carries
// current.angle beside it.

// The allow kinds' spans.
const (
	kiReadMidOff   = addr.KiOffMorf                          // mpMorf .. m339
	kiReadMidLen   = addr.KiOffAcchCir - addr.KiOffMorf      // 0x88: through +0x33C
	kiReadCollOff  = addr.KiOffAtSph                         // m580, m6AC, mDamageSphere
	kiReadCollLen  = addr.KiOffSphsEnd - addr.KiOffAtSph     // 0x384
	amReadMidOff   = addr.AmOffMorf                          // mpMorf .. mTargetEyeRot
	amReadMidLen   = addr.AmOffEmitter - addr.AmOffMorf      // 0x84: through +0x33C
	amReadCollOff  = addr.AmOffEyeSph                        // mEyeSph .. mNeedleCyl
	amReadCollLen  = addr.AmOffIce - addr.AmOffEyeSph        // 0x4B8
	am2ReadMidOff  = addr.Am2OffMorf                         // mpMorf .. mAcchRadius
	am2ReadMidLen  = addr.Am2OffAcchCir - addr.Am2OffMorf    // 0xA8: through +0x360
	am2ReadCollOff = addr.Am2OffBodyCyl                      // mBodyCyl .. mWeakSph
	am2ReadCollLen = addr.Am2OffSmokeCb - addr.Am2OffBodyCyl // 0x4B8
)

// colliderSpec names one collider of an allow class.
type colliderSpec struct {
	name string
	off  uint32
	cyl  bool
}

// Each allow class's colliders in header order (d_a_ki.h:77-79,
// d_a_am.h:51-54, d_a_am2.h:51-54).
var (
	kiColliders = []colliderSpec{{"at", addr.KiOffAtSph, false}, {"co", addr.KiOffCoSph, false}, {"tg", addr.KiOffTgSph, false}}
	amColliders = []colliderSpec{{"eye", addr.AmOffEyeSph, false}, {"mouth", addr.AmOffMouthSph, false},
		{"body", addr.AmOffBodyCyl, true}, {"needle", addr.AmOffNeedleCyl, true}}
	am2Colliders = []colliderSpec{{"body", addr.Am2OffBodyCyl, true}, {"needle", addr.Am2OffNeedleCyl, true},
		{"eye", addr.Am2OffEyeSph, false}, {"weak", addr.Am2OffWeakSph, false}}
)

// allowActorFields fills s's actor-level fields from R1 as
// kindActorFields does (AngleY the row's AngleOff, shape.y), then St's
// angles, home and scale.
func allowActorFields(s *KindSample, b []byte) {
	kindActorFields(s, b)
	be := binary.BigEndian
	for i := range 3 {
		s.St.CurAngle[i] = int16(be.Uint16(b[addr.ActorOffCurAngle+2*i:]))
		s.St.ShapeAngle[i] = int16(be.Uint16(b[addr.ActorOffShapeAngle+2*i:]))
		s.St.HomePos[i] = be.Uint32(b[addr.ActorOffHomePos+4*i:])
	}
	s.St.Scale = math.Float32frombits(be.Uint32(b[addr.ActorOffScale:]))
	s.St.BckIdx = -1
}

// readExact is one Read of n bytes at at; a short read is an error naming
// what.
func readExact(m mem.Reader, at uint32, n int, what string) ([]byte, error) {
	d, err := m.Read(at, n)
	if err != nil {
		return nil, err
	}
	if len(d) != n {
		return nil, fmt.Errorf("enemysync: 0x%08X: short %s read", at, what)
	}
	return d, nil
}

// readColliders is R3: the n bytes at base+off that hold specs, parsed:
// each collider's At/Tg/Co mSPrm (+0x00/+0x18/+0x2C, c_cc_d.h:505-510),
// centre (+0x118), radius (+0x124) and a cylinder's height (+0x128).
func readColliders(m mem.Reader, base, off uint32, n int, specs []colliderSpec) ([]ColliderSample, error) {
	d, err := readExact(m, base+off, n, "collider")
	if err != nil {
		return nil, err
	}
	be := binary.BigEndian
	f := func(o uint32) float32 { return math.Float32frombits(be.Uint32(d[o:])) }
	out := make([]ColliderSample, len(specs))
	for i, sp := range specs {
		o := sp.off - off
		c := ColliderSample{Name: sp.name, Off: sp.off, Cyl: sp.cyl,
			AtSPrm: be.Uint32(d[o+addr.CcCylOffAtSPrm:]), TgSPrm: be.Uint32(d[o+addr.CcCylOffTgSPrm:]), CoSPrm: be.Uint32(d[o+addr.CcCylOffCoSPrm:]),
			R: f(o + addr.CcShapeOffR)}
		for k := range uint32(3) {
			c.Center[k] = f(o + addr.Mo2CcCenterOff + 4*k)
		}
		if sp.cyl {
			c.H = f(o + addr.CcShapeOffH)
		}
		out[i] = c
	}
	return out, nil
}

// readAllowIce is R4: the shrink and freeze timers of the enemyice at
// base+off.
func readAllowIce(m mem.Reader, base, off uint32, s *KindSample) error {
	ice, err := readExact(m, base+off, addr.EnemyIceSize, "enemyice")
	if err != nil {
		return err
	}
	s.IceTimer = int8(ice[addr.EnemyIceOffTimer])
	s.IceFreeze = int16(binary.BigEndian.Uint16(ice[addr.EnemyIceOffFreezeTimer:]))
	return nil
}

// allowFinish sets the generic LIGHT bit and checks the floats.
func allowFinish(s *KindSample, e *Enemy, what string) error {
	if s.Flags&SlotDying != 0 && s.IceTimer != 0 {
		s.Flags2 |= KindF2Light
	}
	if !finiteOK(s.Pos[0], s.Pos[1], s.Pos[2], s.MorfFrame, s.MorfRate) {
		return fmt.Errorf("enemysync: %s %s: NaN or Inf read at 0x%08X", what, e.Ident, e.Addr)
	}
	return nil
}

// ReadSampleKI reads a Keese's tick (design type_table KI sampler): R1
// the actor (a keyed one's pkey rechecked without home.pos, which the
// Keese moves: PKeyFor, D9), R2 +0x2B4..+0x33C (mpMorf .. m339), R3 the three spheres
// (+0x580..+0x904), u8 mDamageType (+0x905), R4 the enemyice (+0x924), R5
// the morf. Action is mAction, Mode mBehaviorType; Aux {shape.x, shape.z,
// m326, m328}. Flags: DYING health <= 0, mAction 30 (ACT_FAIL_MOVE) or the
// ice shrink timer; INVULN m314; HIDDEN m2D4. flags2: ATTN, LUNGE (mAction
// 3 or 10 with mBehaviorType 3). Dormant: mAction 0 with mBehaviorType 0
// or 1, or m2D4 (a Keese keeps LOCKON_BATTLE from create,
// d_a_ki.cpp:1453, so attention is no test).
func ReadSampleKI(m mem.Reader, e *Enemy) (KindSample, error) {
	s := KindSample{Kind: KindKI, Ident: e.Ident}
	b, err := readKindActor(m, e, addr.ProcKI)
	if err != nil {
		return s, err
	}
	s.SetID, s.Proc = e.Ident.SetID, addr.ProcKI
	allowActorFields(&s, b)
	be := binary.BigEndian

	d, err := readExact(m, e.Addr+kiReadMidOff, kiReadMidLen, "Keese mpMorf..m339")
	if err != nil {
		return s, err
	}
	mid := func(off uint32) []byte { return d[off-kiReadMidOff:] }
	morf := be.Uint32(mid(addr.KiOffMorf))
	st := &s.St
	st.Params = mid(addr.KiOffParams)[0]
	action := mid(addr.KiOffAction)[0]
	st.Behavior = int8(mid(addr.KiOffBehavior)[0])
	st.DrawGate = mid(addr.KiOffDrawGate)[0]
	st.ShrinkDelete = mid(addr.KiOffShrinkDelete)[0]
	st.Invuln = int16(be.Uint16(mid(addr.KiOffInvuln)))
	s.Action, s.Mode = int16(action), int16(st.Behavior)
	s.Aux = [4]int16{st.ShapeAngle[0], st.ShapeAngle[2], int16(be.Uint16(mid(addr.KiOffAux1))), int16(be.Uint16(mid(addr.KiOffAux2)))}

	if st.Colliders, err = readColliders(m, e.Addr, kiReadCollOff, kiReadCollLen, kiColliders); err != nil {
		return s, err
	}
	if st.DmgType, err = mem.ReadU8(m, e.Addr+addr.KiOffDmgType); err != nil {
		return s, err
	}
	if err := readAllowIce(m, e.Addr, addr.KiOffIce, &s); err != nil {
		return s, err
	}
	if err := readMorfAnim(m, morf, &s.Sample); err != nil {
		return s, err
	}

	if s.Health <= 0 || action == addr.KiActFail || s.IceTimer != 0 {
		s.Flags |= SlotDying
	}
	if st.Invuln != 0 {
		s.Flags |= SlotInvuln
	}
	if st.DrawGate != 0 {
		s.Flags |= SlotHidden
		s.Hidden = true
	}
	if s.Attn != 0 {
		s.Flags2 |= KiF2Attn
	}
	if (action == addr.KiActAttack || action == addr.KiActAttackIndex) && st.Behavior == addr.KiBehaviorLunge {
		s.Flags2 |= KiF2Lunge
	}
	st.Dormant = (action == addr.KiActWait && (st.Behavior == 0 || st.Behavior == 1)) || st.DrawGate != 0
	return s, allowFinish(&s, e, "Keese")
}

// ReadSampleAM reads an Armos Knight's tick in five Reads (design
// type_table AM sampler): R1 the actor, R2 +0x2B8..+0x33C (mpMorf ..
// mTargetEyeRot), R3 the four colliders (+0x604..+0xABC), R4 the enemyice
// (+0xABC), R5 the morf. Action is mAction, Mode mMode; Aux {mEyeRot.x,
// mEyeRot.y, 0, 0}. Flags: DYING mAction 3 (ITAI) with mMode 47 (DEAD,
// d_a_am.cpp:929) or the ice shrink timer; INVULN mAction 3 with mMode
// 44..46 (the bomb swallowed, d_a_am.cpp:849-928). flags2: ATTN,
// PLAY_SKIP. Dormant: mAction 0, mMode <= 1 and mCurrBckIdx SLEEP_LOOP.
func ReadSampleAM(m mem.Reader, e *Enemy) (KindSample, error) {
	s := KindSample{Kind: KindAM, Ident: e.Ident}
	b, err := readKindActor(m, e, addr.ProcAM)
	if err != nil {
		return s, err
	}
	s.SetID, s.Proc = e.Ident.SetID, addr.ProcAM
	allowActorFields(&s, b)
	be := binary.BigEndian

	d, err := readExact(m, e.Addr+amReadMidOff, amReadMidLen, "Armos Knight mpMorf..mTargetEyeRot")
	if err != nil {
		return s, err
	}
	mid := func(off uint32) []byte { return d[off-amReadMidOff:] }
	morf := be.Uint32(mid(addr.AmOffMorf))
	st := &s.St
	action, mode := mid(addr.AmOffAction)[0], mid(addr.AmOffMode)[0]
	s.Action, s.Mode = int16(action), int16(mode)
	st.BodyHit = mid(addr.AmOffBodyHit)[0]
	st.PlaySkip = int16(be.Uint16(mid(addr.AmOffPlaySkip)))
	st.BckIdx = int32(be.Uint32(mid(addr.AmOffCurrBckIdx)))
	st.SwallowPID = be.Uint32(mid(addr.AmOffSwallowedPID))
	s.Aux = [4]int16{int16(be.Uint16(mid(addr.AmOffEyeRot))), int16(be.Uint16(mid(addr.AmOffEyeRotY))), 0, 0}

	if st.Colliders, err = readColliders(m, e.Addr, amReadCollOff, amReadCollLen, amColliders); err != nil {
		return s, err
	}
	if err := readAllowIce(m, e.Addr, addr.AmOffIce, &s); err != nil {
		return s, err
	}
	if err := readMorfAnim(m, morf, &s.Sample); err != nil {
		return s, err
	}

	itai := action == addr.AmActItai
	if (itai && mode == addr.AmModeDead) || s.IceTimer != 0 {
		s.Flags |= SlotDying
	}
	if itai && mode >= addr.AmModeBombSwallow && mode <= addr.AmModeBombThrash {
		s.Flags |= SlotInvuln
	}
	if s.Attn != 0 {
		s.Flags2 |= AmF2Attn
	}
	if st.PlaySkip != 0 {
		s.Flags2 |= AmF2PlaySkip
	}
	st.Dormant = action == addr.AmActDousa && mode <= 1 && st.BckIdx == addr.AmBckWait
	return s, allowFinish(&s, e, "Armos Knight")
}

// ReadSampleAM2 reads an Armos's tick in five Reads (design type_table
// AM2 sampler): R1 the actor (scale, status and attention with it), R2
// +0x2B8..+0x360 (mpMorf .. mAcchRadius), R3 the four colliders
// (+0x5A0..+0xA58), R4 the enemyice (+0xA8C), R5 the morf. Action is
// mAction, Mode mMode. Flags: DYING the ice shrink timer only (the ITAI
// dance is mirrored; the death is the host's mode-25 delete,
// d_a_am2.cpp:959-971). flags2: ATTN, WEAK_TG (mWeakSph Tg Set), BODY_CO
// (mBodyCyl Co Set), CARRIED (actor_status CARRY). Dormant: attention
// without LOCKON_BATTLE.
func ReadSampleAM2(m mem.Reader, e *Enemy) (KindSample, error) {
	s := KindSample{Kind: KindAM2, Ident: e.Ident}
	b, err := readKindActor(m, e, addr.ProcAM2)
	if err != nil {
		return s, err
	}
	s.SetID, s.Proc = e.Ident.SetID, addr.ProcAM2
	allowActorFields(&s, b)
	be := binary.BigEndian

	d, err := readExact(m, e.Addr+am2ReadMidOff, am2ReadMidLen, "Armos mpMorf..mAcchRadius")
	if err != nil {
		return s, err
	}
	mid := func(off uint32) []byte { return d[off-am2ReadMidOff:] }
	morf := be.Uint32(mid(addr.Am2OffMorf))
	st := &s.St
	s.Action, s.Mode = int16(mid(addr.Am2OffAction)[0]), int16(mid(addr.Am2OffMode)[0])
	st.WeakHit = mid(addr.Am2OffWeakHit)[0]
	st.NotHome = mid(addr.Am2OffNotInHomeRoom)[0]
	st.Abyss = mid(addr.Am2OffAbyssTimer)[0]
	st.Stun = int16(be.Uint16(mid(addr.Am2OffStunTimer)))
	st.BckIdx = int32(be.Uint32(mid(addr.Am2OffCurrBckIdx)))

	if st.Colliders, err = readColliders(m, e.Addr, am2ReadCollOff, am2ReadCollLen, am2Colliders); err != nil {
		return s, err
	}
	if err := readAllowIce(m, e.Addr, addr.Am2OffIce, &s); err != nil {
		return s, err
	}
	if err := readMorfAnim(m, morf, &s.Sample); err != nil {
		return s, err
	}

	if s.IceTimer != 0 {
		s.Flags |= SlotDying
	}
	if s.Attn != 0 {
		s.Flags2 |= Am2F2Attn
	}
	if st.Colliders[3].TgSet() {
		s.Flags2 |= Am2F2WeakTg
	}
	if st.Colliders[0].CoSet() {
		s.Flags2 |= Am2F2BodyCo
	}
	if s.Status&addr.ActorStatusCarry != 0 {
		s.Flags2 |= Am2F2Carried
	}
	st.Dormant = s.Attn&addr.AttnLockonBattle == 0
	return s, allowFinish(&s, e, "Armos")
}
