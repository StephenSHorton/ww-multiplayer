package mailbox

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// The enemy-mirror block in the mailbox slack (inject/include/wwenemy.h,
// whose WWENEMY_* defines these mirror; enemy_test.go pins each one to
// the header). One 0x198-byte block at Mailbox+EnemyOff: Go writes its
// first EnemyGoSpan bytes in one write per tick (Block.Encode), with
// seq_head at +0x04 equal to seq_tail at +0x120 so the REL module can
// tell a torn read; the REL writes the tail, +0x124..+0x198, which Go
// reads back as a Status. Past the block, still inside the slack, the
// host REL keeps its two static collision objects (EnemySphOff,
// EnemySttsOff), so everything enemy sync owns ends at EnemyRegionEnd.
// Design: ww-multiplayer-handoff/design/host-authoritative-prototype.md
// and moblin-s2-v2-final.txt (layout 2: the animation stream and the v2
// hit path).
//
// The slack is not zeroed at boot, so a block is valid only by its magic,
// its epoch and a moving seq; Go writes magic 0 when it stops.
const (
	EnemyMagic    uint32 = 0x5757454E // 'WWEN'
	EnemyOff      uint32 = 0x200      // block start, from Mailbox
	EnemySize     uint32 = 0x198
	EnemyLayout   uint16 = 2 // u16 at +0x12: bumped on any layout change
	EnemyMaxSlots        = 4

	// The Go-written span: +0x00..+EnemyGoSpan, one write per tick.
	EnemyMagicOff     = 0x00  // u32 EnemyMagic (0 = block off)
	EnemySeqHeadOff   = 0x04  // u32 ++ per tick, never 0
	EnemyHostBeatOff  = 0x08  // u32 host spawn_trigger at sample time
	EnemyGoWriteMSOff = 0x0C  // u32 Go monotonic ms at write (lag probe)
	EnemyCountOff     = 0x10  // u8 slots in use (0..4)
	EnemyFlagsOff     = 0x11  // u8 EnemyFlag*
	EnemyLayoutOff    = 0x12  // u16 EnemyLayout
	EnemyEpochOff     = 0x14  // u32 bumped on arm, stage change, heartbeat regression
	EnemyStageOff     = 0x18  // char[8] NUL-padded stage name
	EnemyRoomOff      = 0x20  // s8 room (mStayNo)
	EnemyRoleOff      = 0x21  // u8 EnemyRole*
	EnemyReservedOff  = 0x24  // u32[3]
	EnemySlotsOff     = 0x30  // slot[4] x EnemySlotSize
	EnemySlotSize     = 0x34  // slots end at +0x100
	EnemyHitApplyOff  = 0x100 // host role: a HIT to apply (EnemyHA*)
	EnemySeqTailOff   = 0x120 // u32 = seq_head, the last Go-written word
	EnemyGoSpan       = 0x124 // bytes Go writes per tick

	EnemyFlagMirror    uint8 = 0x01 // mirror on (clear = release everything)
	EnemyFlagColliders uint8 = 0x02 // re-submit colliders (S3)
	EnemyFlagAnim      uint8 = 0x04 // streamed animation (S2)
	EnemyFlagRaw       uint8 = 0x08 // no REL lerp (lag measurement)

	EnemyRoleHost   uint8 = 1
	EnemyRoleClient uint8 = 2

	// Slot fields, from the slot start.
	EnemySLPIDOff       = 0x00 // u32 actor process id, 0 = empty -> release
	EnemySLPosOff       = 0x04 // f32 x, y, z
	EnemySLAngleOff     = 0x10 // s16 angle.y
	EnemySLTiltOff      = 0x12 // s16 mo2 tilt (+0xAA0)
	EnemySLActionOff    = 0x14 // s16 mDamageReaction.mAction (+0x61A)
	EnemySLModeOff      = 0x16 // s16 mDamageReaction.mMode (+0x618)
	EnemySLAttackOff    = 0x18 // u8 attack type (+0x2060)
	EnemySLPhaseOff     = 0x19 // u8 attack phase (+0x2064)
	EnemySLHealthOff    = 0x1A // s8 health (+0x285)
	EnemySLFlagsOff     = 0x1B // u8 EnemySF*
	EnemySLFrameOff     = 0x1C // f32 morf frame (mpMorf+0x68)
	EnemySLRateOff      = 0x20 // f32 morf rate (mpMorf+0x64)
	EnemySLSampleOff    = 0x24 // u32 host sample seq
	EnemySLBckOff       = 0x28 // u8 Mo2 BCK index of mpAnm (addr.Mo2BckFirst..Last), EnemyBckNone unresolved
	EnemySLAttrOff      = 0x29 // u8 J3DFrameCtrl mAttribute (0 NONE, 1 RESET, 2 LOOP, 3 REVERSE, 4 LOOP_REVERSE), 0xFF unknown
	EnemySLAnmSeqOff    = 0x2A // u8 bumped by the host whenever it must have called setAnm (the restart detector)
	EnemySLMorfOff      = 0x2B // u8 setAnm's morf argument recovered from mMorfStep at the restart (0 = snap)
	EnemySLFlags2Off    = 0x2C // u8 EnemySF2*
	EnemySLBlurShapeOff = 0x2D // u8 m0598 low byte
	EnemySLBlurScaleOff = 0x30 // f32 m0590 (after a u16 pad at +0x2E)

	// EnemyBckNone is the slot's bck for an mpAnm the host could not
	// resolve (addr.Mo2BckNone).
	EnemyBckNone uint8 = 0xFF

	EnemySFSnap       uint8 = 0x01
	EnemySFDying      uint8 = 0x02
	EnemySFInvuln     uint8 = 0x04
	EnemySFDefence    uint8 = 0x08
	EnemySFRagdoll    uint8 = 0x10
	EnemySFBareHanded uint8 = 0x20
	EnemySFHidden     uint8 = 0x40
	EnemySFRelease    uint8 = 0x80

	EnemySF2HasWeapon uint8 = 0x01 // mbHasInnateWeapon != 0 (+0xDD4): the spear shape is drawn
	EnemySF2PlaySkip  uint8 = 0x02 // m05B2 != 0: the host skips play(), frame and blend stand still
	EnemySF2BlurOn    uint8 = 0x04 // m0594 != 0: the sword blur mesh is drawn
	EnemySF2AnmGate   uint8 = 0x08 // m05B0 != 0: anm_init is gated (informational)

	// hit_apply fields (host role), from EnemyHitApplyOff.
	EnemyHASeqOff      = 0x00 // u32 request seq (the REL acks it at EnemyHitApplyAckOff)
	EnemyHASetIDOff    = 0x04 // u16
	EnemyHAColliderOff = 0x06 // u8 1 head sph, 2 tg cyl
	EnemyHAFlagsOff    = 0x07 // u8 EnemyHAF*
	EnemyHAAtTypeOff   = 0x08 // u32 cCcD_ObjAt mType
	EnemyHAAtpOff      = 0x0C // u8
	EnemyHASeOff       = 0x0D // u8 dCcD_GObjAt mSe (0: the REL's default, 1 = sword)
	EnemyHAHitMarkOff  = 0x0E // u8 dCcD_GObjAt mHitMark (0: the REL's default, 0xD normal)
	EnemyHASplOff      = 0x0F // u8 dCcD_GObjAt mSpl (1 = kill blow; the REL clamps > 1 to 1)
	EnemyHAPosOff      = 0x10 // f32 x, y, z
	EnemyHAPIDOff      = 0x1C // u32 the host's pid of that setID's Moblin (Go resolves it; the REL checks setID and room against it)
	EnemyHASize        = 0x20 // ends at +0x120 = EnemySeqTailOff

	EnemyHAFForceV1       uint8 = 0x01 // the REL runs the v1 branch (the light-shrink kill) verbatim
	EnemyHAFFallbackShrink uint8 = 0x02 // after two Sets crossed nothing, write the shrink instead of HD_NO_CROSS
	EnemyHAFNoHitMark     uint8 = 0x04 // the At sphere carries dCcG_AtSPrm_NoHitMark_e

	// The REL-written span: +EnemyGoSpan..+EnemySize.
	EnemyAckSeqOff      = 0x124 // u32 last seq consumed
	EnemyAckFrameOff    = 0x128 // u32 WWREL frame counter at consume
	EnemyAdoptedOff     = 0x12C // u32 bit n = slot n adopted
	EnemyEpochSeenOff   = 0x130 // u32
	EnemyStatusOff      = 0x134 // status[4] x EnemyStatusSize
	EnemyStatusSize     = 0x10
	EnemyHitsOff        = 0x174 // hit[4] x EnemyHitSize: u16 hit_seq, u8 collider, u8 pending
	EnemyHitSize        = 0x04
	EnemyHitApplyAckOff = 0x184 // u32 hit_apply seq acked (its result at EnemyHitResultOff)
	EnemyCanariesOff    = 0x188 // u8 pause_reasserts, u8 anm_unresolved, u8 revalidate_failures, u8 watchdog_releases
	EnemyCanaries2Off   = 0x18C // u8 anm_res_null, u8 anm_range, u8 hit_nocross, u8 hit_aborted
	EnemyHitResultOff   = 0x190 // u32 BE: byte3 EnemyHR* result, byte2 EnemyHD* detail, byte1 REL executes from Set to result, byte0 Sets issued
	EnemyReserved2Off   = 0x194 // u32

	// Status fields, from the status start.
	EnemySTActorOff      = 0x00 // u32 actor address (0 when unresolved)
	EnemySTPIDOff        = 0x04 // u32 pid resolved
	EnemySTStateOff      = 0x08 // u8 EnemyState*
	EnemySTPausePrevOff  = 0x09 // u8 pause byte before adopt
	EnemySTLastErrOff    = 0x0A // u8 EnemyErr*
	EnemySTAnmStateOff   = 0x0B // u8 EnemyAnm*
	EnemySTFramesOff     = 0x0C // u16 frames mirrored
	EnemySTBckOff        = 0x0E // u8 the last BCK index setAnm'd on the copy, 0xFF none
	EnemySTAnmChangesOff = 0x0F // u8 setAnm calls on the copy (saturating)

	EnemyStateUnresolved uint8 = 0
	EnemyStateMirrored   uint8 = 1
	EnemyStateReleased   uint8 = 2
	EnemyStateMissing    uint8 = 3
	EnemyStateDying      uint8 = 4
	EnemyStateRefused    uint8 = 5

	EnemyErrNone  uint8 = 0
	EnemyErrPID   uint8 = 1 // fopAcM_SearchByID found nothing
	EnemyErrProc  uint8 = 2 // proc != 0x00BD
	EnemyErrInit  uint8 = 3 // initState != 2
	EnemyErrSetID uint8 = 4 // setID / home room mismatch
	EnemyErrMorf  uint8 = 5 // mpMorf or model null
	EnemyErrRange uint8 = 6 // NaN or |pos| > 1e6
	EnemyErrMode  uint8 = 7 // mo2 mMode 5 (carried) or 0xF (statue); 0 and 1 are mirrored

	// The copy's animation state (EnemySTAnmStateOff).
	EnemyAnmS1         uint8 = 0 // not streaming: play + calc only
	EnemyAnmSynced     uint8 = 1 // the streamed bck is setAnm'd on the copy
	EnemyAnmUnresolved uint8 = 2 // the host sent EnemyBckNone
	EnemyAnmResNull    uint8 = 3 // getRes gave NULL or a bad pointer
	EnemyAnmRange      uint8 = 4 // index or attribute out of range

	// hit_apply results (byte 3 of EnemyHitResultOff).
	EnemyHRDropped   uint8 = 0
	EnemyHRAppliedAt uint8 = 1 // the At sphere crossed the target's head or Tg; its own damage code ran
	EnemyHRNoActor   uint8 = 2
	EnemyHRShrink    uint8 = 3 // the v1 light-shrink kill was written
	EnemyHRParried   uint8 = 4 // the Moblin's defence sphere took it

	// hit_apply details (byte 2 of EnemyHitResultOff; EnemyHDGoGate on
	// are Go-side drops that never reach the REL).
	EnemyHDNone          uint8 = 0
	EnemyHDNoCross       uint8 = 1  // two Sets, nothing crossed
	EnemyHDInvuln        uint8 = 2  // m05B4 still set after the wait (or v1's pre-drop)
	EnemyHDRagdoll       uint8 = 3  // damagereaction mode <= -100
	EnemyHDDying         uint8 = 4  // health <= 0 or the death action
	EnemyHDNoLink        uint8 = 5  // PlayerInfo0Ptr is not a PLAYER, or differs from PlayerPtr
	EnemyHDNoSphere      uint8 = 6  // the sphere image's vptr sanity failed
	EnemyHDWrongObj      uint8 = 7  // another object's Tg crossed first
	EnemyHDAborted       uint8 = 8  // REL early return, release or delete mid-request
	EnemyHDDefence       uint8 = 9  // v1's pre-drop only
	EnemyHDFallbackShrink uint8 = 10 // HAF_FALLBACK_SHRINK applied the shrink after NO_CROSS
	EnemyHDGoGate        uint8 = 11 // epoch, stage, room, scope, replayed hit_seq or no REL
	EnemyHDRate          uint8 = 12 // within HitRate of the last hit on that Moblin
	EnemyHDBusy          uint8 = 13 // the request queue is full or holds that (client, setID)
	EnemyHDGoFlags       uint8 = 14 // the sample's flags pre-dropped it (ragdoll/dying; the four flags in v1 mode)
	EnemyHDDemo          uint8 = 15 // the host Link is in a demo (mDemo.mDemoType != 0)

	// EnemyStaleFrames is how many REL frames without a new seq release
	// every adopted slot.
	EnemyStaleFrames = 90

	// The host REL's static collision objects past the block, from
	// Mailbox: a dCcD_Sph image (0x12C) and a dCcD_Stts (0x3C), and where
	// everything enemy sync owns ends.
	EnemySphOff    uint32 = 0x3A0
	EnemySttsOff   uint32 = 0x4D0
	EnemyRegionEnd uint32 = 0x510
	EnemySphSize   uint32 = 0x12C // sizeof(dCcD_Sph), d_cc_d.h:529
	EnemySttsSize  uint32 = 0x3C  // sizeof(dCcD_Stts), d_cc_d.h:178-243

	// EnemyStageLen is the stage field's size.
	EnemyStageLen = 8
)

// EnemyBlockAddr is the block's address.
const EnemyBlockAddr = Mailbox + EnemyOff

// EnemySlotAddr is the address of field off (an EnemySL* offset) in slot i.
func EnemySlotAddr(i int, off uint32) uint32 {
	return EnemyBlockAddr + EnemySlotsOff + uint32(i)*EnemySlotSize + off
}

// EnemySlot is one mirrored Moblin as the Go-written block carries it.
type EnemySlot struct {
	PID         uint32 // 0 = empty -> release
	Pos         [3]float32
	AngleY      int16
	Tilt        int16
	Action      int16
	Mode        int16
	AttackType  uint8
	AttackPhase uint8
	Health      int8
	Flags       uint8 // EnemySF*
	MorfFrame   float32
	MorfRate    float32
	SampleSeq   uint32
	Bck         uint8 // Mo2 BCK index, EnemyBckNone unresolved
	AnmAttr     uint8 // J3DFrameCtrl attribute, 0xFF unknown
	AnmSeq      uint8
	Morf        uint8
	Flags2      uint8 // EnemySF2*
	BlurShape   uint8
	BlurScale   float32
}

// EnemyHitApply is the host-role hit_apply request at EnemyHitApplyOff.
type EnemyHitApply struct {
	Seq      uint32
	SetID    uint16
	Collider uint8
	Flags    uint8 // EnemyHAF*
	AtType   uint32
	Atp      uint8
	Se       uint8
	HitMark  uint8
	Spl      uint8
	Pos      [3]float32
	PID      uint32 // the host's Moblin with that setID, from the sampler's cache
}

// EnemyBlock is the Go-written span of the block. Encode lays it out;
// Count is len(Slots), capped at EnemyMaxSlots.
type EnemyBlock struct {
	Magic     uint32 // EnemyMagic, or 0 to turn the block off
	Seq       uint32 // written as both seq_head and seq_tail
	HostBeat  uint32
	GoWriteMS uint32
	Flags     uint8
	Layout    uint16
	Epoch     uint32
	Stage     string
	Room      int8
	Role      uint8
	Slots     []EnemySlot
	HitApply  EnemyHitApply
}

// ErrEnemyNotFinite is Encode's error for a NaN or Inf.
var ErrEnemyNotFinite = errors.New("mailbox: NaN or Inf in the enemy block")

func enemyFinite(fs ...float32) error {
	for _, f := range fs {
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			return ErrEnemyNotFinite
		}
	}
	return nil
}

// Encode lays the block out as the REL reads it: EnemyGoSpan bytes with
// seq_head == seq_tail == b.Seq. It refuses more than EnemyMaxSlots
// slots, a stage name over EnemyStageLen bytes and a NaN or Inf.
func (b *EnemyBlock) Encode() ([]byte, error) {
	if len(b.Slots) > EnemyMaxSlots {
		return nil, fmt.Errorf("mailbox: enemy block with %d slots (max %d)", len(b.Slots), EnemyMaxSlots)
	}
	if len(b.Stage) > EnemyStageLen {
		return nil, fmt.Errorf("mailbox: enemy block stage %q is longer than %d bytes", b.Stage, EnemyStageLen)
	}
	out := make([]byte, EnemyGoSpan)
	be := binary.BigEndian
	be.PutUint32(out[EnemyMagicOff:], b.Magic)
	be.PutUint32(out[EnemySeqHeadOff:], b.Seq)
	be.PutUint32(out[EnemyHostBeatOff:], b.HostBeat)
	be.PutUint32(out[EnemyGoWriteMSOff:], b.GoWriteMS)
	out[EnemyCountOff] = byte(len(b.Slots))
	out[EnemyFlagsOff] = b.Flags
	be.PutUint16(out[EnemyLayoutOff:], b.Layout)
	be.PutUint32(out[EnemyEpochOff:], b.Epoch)
	copy(out[EnemyStageOff:EnemyStageOff+EnemyStageLen], b.Stage)
	out[EnemyRoomOff] = byte(b.Room)
	out[EnemyRoleOff] = b.Role
	for i := range b.Slots {
		s := &b.Slots[i]
		if err := enemyFinite(s.Pos[0], s.Pos[1], s.Pos[2], s.MorfFrame, s.MorfRate, s.BlurScale); err != nil {
			return nil, fmt.Errorf("slot %d: %w", i, err)
		}
		o := out[EnemySlotsOff+i*EnemySlotSize:]
		be.PutUint32(o[EnemySLPIDOff:], s.PID)
		for k := range 3 {
			be.PutUint32(o[EnemySLPosOff+4*k:], math.Float32bits(s.Pos[k]))
		}
		be.PutUint16(o[EnemySLAngleOff:], uint16(s.AngleY))
		be.PutUint16(o[EnemySLTiltOff:], uint16(s.Tilt))
		be.PutUint16(o[EnemySLActionOff:], uint16(s.Action))
		be.PutUint16(o[EnemySLModeOff:], uint16(s.Mode))
		o[EnemySLAttackOff] = s.AttackType
		o[EnemySLPhaseOff] = s.AttackPhase
		o[EnemySLHealthOff] = byte(s.Health)
		o[EnemySLFlagsOff] = s.Flags
		be.PutUint32(o[EnemySLFrameOff:], math.Float32bits(s.MorfFrame))
		be.PutUint32(o[EnemySLRateOff:], math.Float32bits(s.MorfRate))
		be.PutUint32(o[EnemySLSampleOff:], s.SampleSeq)
		o[EnemySLBckOff] = s.Bck
		o[EnemySLAttrOff] = s.AnmAttr
		o[EnemySLAnmSeqOff] = s.AnmSeq
		o[EnemySLMorfOff] = s.Morf
		o[EnemySLFlags2Off] = s.Flags2
		o[EnemySLBlurShapeOff] = s.BlurShape
		be.PutUint32(o[EnemySLBlurScaleOff:], math.Float32bits(s.BlurScale))
	}
	h := &b.HitApply
	if err := enemyFinite(h.Pos[0], h.Pos[1], h.Pos[2]); err != nil {
		return nil, fmt.Errorf("hit_apply: %w", err)
	}
	o := out[EnemyHitApplyOff:]
	be.PutUint32(o[EnemyHASeqOff:], h.Seq)
	be.PutUint16(o[EnemyHASetIDOff:], h.SetID)
	o[EnemyHAColliderOff] = h.Collider
	o[EnemyHAFlagsOff] = h.Flags
	be.PutUint32(o[EnemyHAAtTypeOff:], h.AtType)
	o[EnemyHAAtpOff] = h.Atp
	o[EnemyHASeOff] = h.Se
	o[EnemyHAHitMarkOff] = h.HitMark
	o[EnemyHASplOff] = h.Spl
	for k := range 3 {
		be.PutUint32(o[EnemyHAPosOff+4*k:], math.Float32bits(h.Pos[k]))
	}
	be.PutUint32(o[EnemyHAPIDOff:], h.PID)
	be.PutUint32(out[EnemySeqTailOff:], b.Seq)
	return out, nil
}

// WriteEnemyBlock encodes b and writes it at the block in one write.
func WriteEnemyBlock(m Writer, b *EnemyBlock) error {
	out, err := b.Encode()
	if err != nil {
		return err
	}
	if err := m.Write(EnemyBlockAddr, out); err != nil {
		return fmt.Errorf("mailbox: enemy block: %w", err)
	}
	return nil
}

// ClearEnemyBlock writes magic 0, which releases every mirrored slot the
// next REL frame. It is what a pipeline writes as it stops.
func ClearEnemyBlock(m Writer) error {
	if err := m.Write(EnemyBlockAddr+EnemyMagicOff, []byte{0, 0, 0, 0}); err != nil {
		return fmt.Errorf("mailbox: enemy block magic: %w", err)
	}
	return nil
}

// DecodeEnemyBlock parses the Go-written span back (the enemy-block-dump
// probe; tests). b must be at least EnemyGoSpan bytes. SeqTail is
// returned apart so a torn block shows.
func DecodeEnemyBlock(b []byte) (blk EnemyBlock, seqTail uint32, err error) {
	if len(b) < EnemyGoSpan {
		return blk, 0, fmt.Errorf("mailbox: %d bytes, the enemy block's Go span is %d", len(b), EnemyGoSpan)
	}
	be := binary.BigEndian
	blk.Magic = be.Uint32(b[EnemyMagicOff:])
	blk.Seq = be.Uint32(b[EnemySeqHeadOff:])
	blk.HostBeat = be.Uint32(b[EnemyHostBeatOff:])
	blk.GoWriteMS = be.Uint32(b[EnemyGoWriteMSOff:])
	count := int(b[EnemyCountOff])
	blk.Flags = b[EnemyFlagsOff]
	blk.Layout = be.Uint16(b[EnemyLayoutOff:])
	blk.Epoch = be.Uint32(b[EnemyEpochOff:])
	stage := b[EnemyStageOff : EnemyStageOff+EnemyStageLen]
	n := 0
	for n < EnemyStageLen && stage[n] != 0 {
		n++
	}
	blk.Stage = string(stage[:n])
	blk.Room = int8(b[EnemyRoomOff])
	blk.Role = b[EnemyRoleOff]
	if count > EnemyMaxSlots {
		count = EnemyMaxSlots
	}
	for i := range count {
		o := b[EnemySlotsOff+i*EnemySlotSize:]
		s := EnemySlot{
			PID:         be.Uint32(o[EnemySLPIDOff:]),
			AngleY:      int16(be.Uint16(o[EnemySLAngleOff:])),
			Tilt:        int16(be.Uint16(o[EnemySLTiltOff:])),
			Action:      int16(be.Uint16(o[EnemySLActionOff:])),
			Mode:        int16(be.Uint16(o[EnemySLModeOff:])),
			AttackType:  o[EnemySLAttackOff],
			AttackPhase: o[EnemySLPhaseOff],
			Health:      int8(o[EnemySLHealthOff]),
			Flags:       o[EnemySLFlagsOff],
			MorfFrame:   math.Float32frombits(be.Uint32(o[EnemySLFrameOff:])),
			MorfRate:    math.Float32frombits(be.Uint32(o[EnemySLRateOff:])),
			SampleSeq:   be.Uint32(o[EnemySLSampleOff:]),
			Bck:         o[EnemySLBckOff],
			AnmAttr:     o[EnemySLAttrOff],
			AnmSeq:      o[EnemySLAnmSeqOff],
			Morf:        o[EnemySLMorfOff],
			Flags2:      o[EnemySLFlags2Off],
			BlurShape:   o[EnemySLBlurShapeOff],
			BlurScale:   math.Float32frombits(be.Uint32(o[EnemySLBlurScaleOff:])),
		}
		for k := range 3 {
			s.Pos[k] = math.Float32frombits(be.Uint32(o[EnemySLPosOff+4*k:]))
		}
		blk.Slots = append(blk.Slots, s)
	}
	o := b[EnemyHitApplyOff:]
	blk.HitApply = EnemyHitApply{
		Seq:      be.Uint32(o[EnemyHASeqOff:]),
		SetID:    be.Uint16(o[EnemyHASetIDOff:]),
		Collider: o[EnemyHAColliderOff],
		Flags:    o[EnemyHAFlagsOff],
		AtType:   be.Uint32(o[EnemyHAAtTypeOff:]),
		Atp:      o[EnemyHAAtpOff],
		Se:       o[EnemyHASeOff],
		HitMark:  o[EnemyHAHitMarkOff],
		Spl:      o[EnemyHASplOff],
		PID:      be.Uint32(o[EnemyHAPIDOff:]),
	}
	for k := range 3 {
		blk.HitApply.Pos[k] = math.Float32frombits(be.Uint32(o[EnemyHAPosOff+4*k:]))
	}
	return blk, be.Uint32(b[EnemySeqTailOff:]), nil
}

// EnemySlotStatus is one slot's REL-written status entry.
type EnemySlotStatus struct {
	Actor      uint32 // actor address, 0 when unresolved
	PID        uint32
	State      uint8 // EnemyState*
	PausePrev  uint8 // the pause byte before adopt
	LastErr    uint8 // EnemyErr*
	AnmState   uint8 // EnemyAnm*
	Frames     uint16
	Bck        uint8 // the last BCK index setAnm'd on the copy, 0xFF none
	AnmChanges uint8 // setAnm calls on the copy (saturating)
}

// EnemyHitEntry is one slot's REL-written hit entry (S3).
type EnemyHitEntry struct {
	HitSeq   uint16
	Collider uint8
	Pending  uint8
}

// EnemyCanaries are the REL's packed counters.
type EnemyCanaries struct {
	PauseReasserts     uint8
	AnmUnresolved      uint8
	RevalidateFailures uint8
	WatchdogReleases   uint8
}

// EnemyCanaries2 are the REL's second counter word: the animation stream's
// and the hit path's failures.
type EnemyCanaries2 struct {
	AnmResNull uint8 // getRes gave NULL or a bad pointer
	AnmRange   uint8 // index or attribute out of range
	HitNoCross uint8 // a v2 request whose two Sets crossed nothing
	HitAborted uint8 // a request aborted by an early return, release or delete
}

// EnemyHitResult is the hit_apply result word: what the REL did with the
// request HitApplyAck names.
type EnemyHitResult struct {
	Result uint8 // EnemyHR*
	Detail uint8 // EnemyHD*
	Frames uint8 // REL executes from the first Set to the result
	Sets   uint8 // cCcS::Set calls issued
}

// EnemyStatus is the REL-written span, +EnemyGoSpan..+EnemySize.
type EnemyStatus struct {
	AckSeq      uint32
	AckFrame    uint32
	AdoptedMask uint32
	EpochSeen   uint32
	Status      [EnemyMaxSlots]EnemySlotStatus
	Hits        [EnemyMaxSlots]EnemyHitEntry
	HitApplyAck uint32
	Canaries    EnemyCanaries
	Canaries2   EnemyCanaries2
	HitResult   EnemyHitResult
}

// enemyStatusLen is the REL-written span's size.
const enemyStatusLen = EnemySize - EnemyGoSpan

// DecodeEnemyStatus parses the REL-written span from b, which must hold
// at least enemyStatusLen bytes starting at +EnemyGoSpan.
func DecodeEnemyStatus(b []byte) (EnemyStatus, error) {
	var st EnemyStatus
	if len(b) < int(enemyStatusLen) {
		return st, fmt.Errorf("mailbox: %d bytes, the enemy block's REL span is %d", len(b), enemyStatusLen)
	}
	be := binary.BigEndian
	at := func(off int) []byte { return b[off-EnemyGoSpan:] }
	st.AckSeq = be.Uint32(at(EnemyAckSeqOff))
	st.AckFrame = be.Uint32(at(EnemyAckFrameOff))
	st.AdoptedMask = be.Uint32(at(EnemyAdoptedOff))
	st.EpochSeen = be.Uint32(at(EnemyEpochSeenOff))
	for i := range st.Status {
		o := at(EnemyStatusOff + i*EnemyStatusSize)
		st.Status[i] = EnemySlotStatus{
			Actor:      be.Uint32(o[EnemySTActorOff:]),
			PID:        be.Uint32(o[EnemySTPIDOff:]),
			State:      o[EnemySTStateOff],
			PausePrev:  o[EnemySTPausePrevOff],
			LastErr:    o[EnemySTLastErrOff],
			AnmState:   o[EnemySTAnmStateOff],
			Frames:     be.Uint16(o[EnemySTFramesOff:]),
			Bck:        o[EnemySTBckOff],
			AnmChanges: o[EnemySTAnmChangesOff],
		}
	}
	for i := range st.Hits {
		o := at(EnemyHitsOff + i*EnemyHitSize)
		st.Hits[i] = EnemyHitEntry{HitSeq: be.Uint16(o), Collider: o[2], Pending: o[3]}
	}
	st.HitApplyAck = be.Uint32(at(EnemyHitApplyAckOff))
	c := at(EnemyCanariesOff)
	st.Canaries = EnemyCanaries{PauseReasserts: c[0], AnmUnresolved: c[1], RevalidateFailures: c[2], WatchdogReleases: c[3]}
	c = at(EnemyCanaries2Off)
	st.Canaries2 = EnemyCanaries2{AnmResNull: c[0], AnmRange: c[1], HitNoCross: c[2], HitAborted: c[3]}
	r := at(EnemyHitResultOff)
	st.HitResult = EnemyHitResult{Result: r[0], Detail: r[1], Frames: r[2], Sets: r[3]}
	return st, nil
}

// ReadEnemyStatus reads the REL-written span from the game.
func ReadEnemyStatus(m Reader) (EnemyStatus, error) {
	b, err := m.Read(EnemyBlockAddr+EnemyGoSpan, int(enemyStatusLen))
	if err != nil {
		return EnemyStatus{}, fmt.Errorf("mailbox: enemy status: %w", err)
	}
	if len(b) != int(enemyStatusLen) {
		return EnemyStatus{}, fmt.Errorf("mailbox: enemy status: %d bytes, want %d", len(b), enemyStatusLen)
	}
	return DecodeEnemyStatus(b)
}

// ReadEnemyBlock reads the whole block: the Go span decoded, its tail
// seq, and the REL span.
func ReadEnemyBlock(m Reader) (blk EnemyBlock, seqTail uint32, st EnemyStatus, err error) {
	b, err := m.Read(EnemyBlockAddr, int(EnemySize))
	if err != nil {
		return blk, 0, st, fmt.Errorf("mailbox: enemy block: %w", err)
	}
	if len(b) != int(EnemySize) {
		return blk, 0, st, fmt.Errorf("mailbox: enemy block: %d bytes, want %d", len(b), EnemySize)
	}
	if blk, seqTail, err = DecodeEnemyBlock(b); err != nil {
		return blk, 0, st, err
	}
	st, err = DecodeEnemyStatus(b[EnemyGoSpan:])
	return blk, seqTail, st, err
}

// EnemyBlockRegion is the RAM enemy sync owns: the block and, past it,
// the host REL's static collision objects, up to Mailbox+EnemyRegionEnd.
// The region probe must not arm it while enemy sync runs (mod.go,
// CanaryArena).
var EnemyBlockRegion = addr.Region{Name: "enemy block", Start: EnemyBlockAddr, End: Mailbox + EnemyRegionEnd}
