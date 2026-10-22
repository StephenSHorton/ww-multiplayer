package mailbox

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// wwenemyHeader is inject/include/wwenemy.h, the source of these
// constants (the REL module reads the same file).
const wwenemyHeader = "../../inject/include/wwenemy.h"

// headerDefines reads every `#define NAME value` of path the way
// scripts/extract_blob.py's source_define does (one per line, a decimal
// or hex number, an optional // comment).
func headerDefines(t *testing.T, path string) map[string]uint64 {
	t.Helper()
	src, err := os.ReadFile(filepath.FromSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^#define\s+(\w+)\s+(0x[0-9A-Fa-f]+|\d+)\s*(?://.*)?$`)
	out := map[string]uint64{}
	for _, m := range re.FindAllSubmatch(src, -1) {
		v, err := strconv.ParseUint(string(m[2]), 0, 32)
		if err != nil {
			t.Fatalf("%s: %s", m[1], err)
		}
		out[string(m[1])] = v
	}
	return out
}

// TestEnemyHeaderPins: every Go constant equals its WWENEMY_* define, and
// every define has a Go constant.
func TestEnemyHeaderPins(t *testing.T) {
	defs := headerDefines(t, wwenemyHeader)
	if got, ok := defs["WWENEMY_LAYOUT"]; !ok || got != uint64(EnemyLayout) {
		t.Fatalf("inject/include/wwenemy.h is WWENEMY_LAYOUT %d and this Go mirror is layout %d: a layout change must move both in the same PR", got, EnemyLayout)
	}
	pins := map[string]uint64{
		"WWENEMY_MAGIC":               uint64(EnemyMagic),
		"WWENEMY_OFF":                 uint64(EnemyOff),
		"WWENEMY_SIZE":                uint64(EnemySize),
		"WWENEMY_LAYOUT":              uint64(EnemyLayout),
		"WWENEMY_MAX_SLOTS":           EnemyMaxSlots,
		"WWENEMY_MAGIC_OFF":           EnemyMagicOff,
		"WWENEMY_SEQ_HEAD_OFF":        EnemySeqHeadOff,
		"WWENEMY_HOST_BEAT_OFF":       EnemyHostBeatOff,
		"WWENEMY_GO_WRITE_MS_OFF":     EnemyGoWriteMSOff,
		"WWENEMY_COUNT_OFF":           EnemyCountOff,
		"WWENEMY_FLAGS_OFF":           EnemyFlagsOff,
		"WWENEMY_LAYOUT_OFF":          EnemyLayoutOff,
		"WWENEMY_EPOCH_OFF":           EnemyEpochOff,
		"WWENEMY_STAGE_OFF":           EnemyStageOff,
		"WWENEMY_ROOM_OFF":            EnemyRoomOff,
		"WWENEMY_ROLE_OFF":            EnemyRoleOff,
		"WWENEMY_KIND_MASK_OFF":       EnemyKindMaskOff,
		"WWENEMY_KNOB_GEN_OFF":        EnemyKnobGenOff,
		"WWENEMY_HKD_HI_OFF":          EnemyHkdHiOff,
		"WWENEMY_HKD_HI_KINDS":        EnemyHkdHiKinds,
		"WWENEMY_SLOTS_OFF":           EnemySlotsOff,
		"WWENEMY_SLOT_SIZE":           EnemySlotSize,
		"WWENEMY_HIT_APPLY_OFF":       EnemyHitApplyOff,
		"WWENEMY_SEQ_TAIL_OFF":        EnemySeqTailOff,
		"WWENEMY_GO_SPAN":             EnemyGoSpan,
		"WWENEMY_FLAG_MIRROR":         uint64(EnemyFlagMirror),
		"WWENEMY_FLAG_COLLIDERS":      uint64(EnemyFlagColliders),
		"WWENEMY_FLAG_ANIM":           uint64(EnemyFlagAnim),
		"WWENEMY_FLAG_RAW":            uint64(EnemyFlagRaw),
		"WWENEMY_ROLE_HOST":           uint64(EnemyRoleHost),
		"WWENEMY_ROLE_CLIENT":         uint64(EnemyRoleClient),
		"WWENEMY_SL_PID_OFF":          EnemySLPIDOff,
		"WWENEMY_SL_POS_OFF":          EnemySLPosOff,
		"WWENEMY_SL_ANGLE_OFF":        EnemySLAngleOff,
		"WWENEMY_SL_TILT_OFF":         EnemySLTiltOff,
		"WWENEMY_SL_PKEY_OFF":         EnemySLPKeyOff,
		"WWENEMY_SL_ATTACK_OFF":       EnemySLAttackOff,
		"WWENEMY_SL_PHASE_OFF":        EnemySLPhaseOff,
		"WWENEMY_SL_CC_M316Z_OFF":     EnemySLCcM316ZOff,
		"WWENEMY_SL_HEALTH_OFF":       EnemySLHealthOff,
		"WWENEMY_SL_FLAGS_OFF":        EnemySLFlagsOff,
		"WWENEMY_SL_FRAME_OFF":        EnemySLFrameOff,
		"WWENEMY_SL_RATE_OFF":         EnemySLRateOff,
		"WWENEMY_SL_SAMPLE_OFF":       EnemySLSampleOff,
		"WWENEMY_SL_BCK_OFF":          EnemySLBckOff,
		"WWENEMY_SL_ATTR_OFF":         EnemySLAttrOff,
		"WWENEMY_SL_ANM_SEQ_OFF":      EnemySLAnmSeqOff,
		"WWENEMY_SL_MORF_OFF":         EnemySLMorfOff,
		"WWENEMY_SL_FLAGS2_OFF":       EnemySLFlags2Off,
		"WWENEMY_SL_BLUR_SHAPE_OFF":   EnemySLBlurShapeOff,
		"WWENEMY_SL_PROC_OFF":         EnemySLProcOff,
		"WWENEMY_SL_BLUR_SCALE_OFF":   EnemySLBlurScaleOff,
		"WWENEMY_BCK_NONE":            uint64(EnemyBckNone),
		"WWENEMY_SF_SNAP":             uint64(EnemySFSnap),
		"WWENEMY_SF_DYING":            uint64(EnemySFDying),
		"WWENEMY_SF_INVULN":           uint64(EnemySFInvuln),
		"WWENEMY_SF_DEFENCE":          uint64(EnemySFDefence),
		"WWENEMY_SF_RAGDOLL":          uint64(EnemySFRagdoll),
		"WWENEMY_SF_BARE_HANDED":      uint64(EnemySFBareHanded),
		"WWENEMY_SF_HIDDEN":           uint64(EnemySFHidden),
		"WWENEMY_SF_RELEASE":          uint64(EnemySFRelease),
		"WWENEMY_SF2_HAS_WEAPON":      uint64(EnemySF2HasWeapon),
		"WWENEMY_SF2_PLAY_SKIP":       uint64(EnemySF2PlaySkip),
		"WWENEMY_SF2_BLUR_ON":         uint64(EnemySF2BlurOn),
		"WWENEMY_SF2_ANM_GATE":        uint64(EnemySF2AnmGate),
		"WWENEMY_SF2_LIGHT":           uint64(EnemySF2Light),
		"WWENEMY_SF2_QUIET":           uint64(EnemySF2Quiet),
		"WWENEMY_HA_SEQ_OFF":          EnemyHASeqOff,
		"WWENEMY_HA_SETID_OFF":        EnemyHASetIDOff,
		"WWENEMY_HA_COLLIDER_OFF":     EnemyHAColliderOff,
		"WWENEMY_HA_FLAGS_OFF":        EnemyHAFlagsOff,
		"WWENEMY_HA_ATTYPE_OFF":       EnemyHAAtTypeOff,
		"WWENEMY_HA_ATP_OFF":          EnemyHAAtpOff,
		"WWENEMY_HA_SE_OFF":           EnemyHASeOff,
		"WWENEMY_HA_HITMARK_OFF":      EnemyHAHitMarkOff,
		"WWENEMY_HA_SPL_OFF":          EnemyHASplOff,
		"WWENEMY_HA_POS_OFF":          EnemyHAPosOff,
		"WWENEMY_HA_PID_OFF":          EnemyHAPIDOff,
		"WWENEMY_HA_SIZE":             EnemyHASize,
		"WWENEMY_HAF_FORCE_V1":        uint64(EnemyHAFForceV1),
		"WWENEMY_HAF_FALLBACK_SHRINK": uint64(EnemyHAFFallbackShrink),
		"WWENEMY_HAF_NO_HITMARK":      uint64(EnemyHAFNoHitMark),
		"WWENEMY_HAF_VERDICT":         uint64(EnemyHAFVerdict),
		"WWENEMY_HAF_BACK":            uint64(EnemyHAFBack),
		"WWENEMY_HAF_CRIT":            uint64(EnemyHAFCrit),
		"WWENEMY_ACK_SEQ_OFF":         EnemyAckSeqOff,
		"WWENEMY_ACK_FRAME_OFF":       EnemyAckFrameOff,
		"WWENEMY_ADOPTED_OFF":         EnemyAdoptedOff,
		"WWENEMY_EPOCH_SEEN_OFF":      EnemyEpochSeenOff,
		"WWENEMY_STATUS_OFF":          EnemyStatusOff,
		"WWENEMY_STATUS_SIZE":         EnemyStatusSize,
		"WWENEMY_HITS_OFF":            EnemyHitsOff,
		"WWENEMY_HIT_SIZE":            EnemyHitSize,
		"WWENEMY_HP_PENDING":          uint64(EnemyHPPending),
		"WWENEMY_HP_VERDICT":          uint64(EnemyHPVerdict),
		"WWENEMY_HP_BACK":             uint64(EnemyHPBack),
		"WWENEMY_HIT_APPLY_ACK_OFF":   EnemyHitApplyAckOff,
		"WWENEMY_CANARIES_OFF":        EnemyCanariesOff,
		"WWENEMY_CANARIES2_OFF":       EnemyCanaries2Off,
		"WWENEMY_HIT_RESULT_OFF":      EnemyHitResultOff,
		"WWENEMY_KNOB_SEEN_OFF":       EnemyKnobSeenOff,
		"WWENEMY_FP_MISS_OFF":         EnemyFpMissOff,
		"WWENEMY_TAIL_SPARE_OFF":      EnemyTailSpareOff,
		"WWENEMY_ST_ACTOR_OFF":        EnemySTActorOff,
		"WWENEMY_ST_PID_OFF":          EnemySTPIDOff,
		"WWENEMY_ST_STATE_OFF":        EnemySTStateOff,
		"WWENEMY_ST_PAUSE_PREV_OFF":   EnemySTPausePrevOff,
		"WWENEMY_ST_LAST_ERR_OFF":     EnemySTLastErrOff,
		"WWENEMY_ST_ANM_STATE_OFF":    EnemySTAnmStateOff,
		"WWENEMY_ST_FRAMES_OFF":       EnemySTFramesOff,
		"WWENEMY_ST_BCK_OFF":          EnemySTBckOff,
		"WWENEMY_ST_ANM_CHANGES_OFF":  EnemySTAnmChangesOff,
		"WWENEMY_STATE_UNRESOLVED":    uint64(EnemyStateUnresolved),
		"WWENEMY_STATE_MIRRORED":      uint64(EnemyStateMirrored),
		"WWENEMY_STATE_RELEASED":      uint64(EnemyStateReleased),
		"WWENEMY_STATE_MISSING":       uint64(EnemyStateMissing),
		"WWENEMY_STATE_DYING":         uint64(EnemyStateDying),
		"WWENEMY_STATE_REFUSED":       uint64(EnemyStateRefused),
		"WWENEMY_STATE_DEAD":          uint64(EnemyStateDead),
		"WWENEMY_ERR_NONE":            uint64(EnemyErrNone),
		"WWENEMY_ERR_PID":             uint64(EnemyErrPID),
		"WWENEMY_ERR_PROC":            uint64(EnemyErrProc),
		"WWENEMY_ERR_INIT":            uint64(EnemyErrInit),
		"WWENEMY_ERR_SETID":           uint64(EnemyErrSetID),
		"WWENEMY_ERR_MORF":            uint64(EnemyErrMorf),
		"WWENEMY_ERR_RANGE":           uint64(EnemyErrRange),
		"WWENEMY_ERR_MODE":            uint64(EnemyErrMode),
		"WWENEMY_ERR_KIND":            uint64(EnemyErrKind),
		"WWENEMY_ERR_GATE":            uint64(EnemyErrGate),
		"WWENEMY_ERR_KEY":             uint64(EnemyErrKey),
		"WWENEMY_ERR_ICE":             uint64(EnemyErrIce),
		"WWENEMY_ANM_S1":              uint64(EnemyAnmS1),
		"WWENEMY_ANM_SYNCED":          uint64(EnemyAnmSynced),
		"WWENEMY_ANM_UNRESOLVED":      uint64(EnemyAnmUnresolved),
		"WWENEMY_ANM_RES_NULL":        uint64(EnemyAnmResNull),
		"WWENEMY_ANM_RANGE":           uint64(EnemyAnmRange),
		"WWENEMY_HR_DROPPED":          uint64(EnemyHRDropped),
		"WWENEMY_HR_APPLIED_AT":       uint64(EnemyHRAppliedAt),
		"WWENEMY_HR_NO_ACTOR":         uint64(EnemyHRNoActor),
		"WWENEMY_HR_SHRINK":           uint64(EnemyHRShrink),
		"WWENEMY_HR_PARRIED":          uint64(EnemyHRParried),
		"WWENEMY_HD_NONE":             uint64(EnemyHDNone),
		"WWENEMY_HD_NO_CROSS":         uint64(EnemyHDNoCross),
		"WWENEMY_HD_INVULN":           uint64(EnemyHDInvuln),
		"WWENEMY_HD_RAGDOLL":          uint64(EnemyHDRagdoll),
		"WWENEMY_HD_DYING":            uint64(EnemyHDDying),
		"WWENEMY_HD_NO_LINK":          uint64(EnemyHDNoLink),
		"WWENEMY_HD_NO_SPHERE":        uint64(EnemyHDNoSphere),
		"WWENEMY_HD_WRONG_OBJ":        uint64(EnemyHDWrongObj),
		"WWENEMY_HD_ABORTED":          uint64(EnemyHDAborted),
		"WWENEMY_HD_DEFENCE":          uint64(EnemyHDDefence),
		"WWENEMY_HD_FALLBACK_SHRINK":  uint64(EnemyHDFallbackShrink),
		"WWENEMY_HD_GO_GATE":          uint64(EnemyHDGoGate),
		"WWENEMY_HD_RATE":             uint64(EnemyHDRate),
		"WWENEMY_HD_BUSY":             uint64(EnemyHDBusy),
		"WWENEMY_HD_GO_FLAGS":         uint64(EnemyHDGoFlags),
		"WWENEMY_HD_DEMO":             uint64(EnemyHDDemo),
		"WWENEMY_HD_KIND":             uint64(EnemyHDKind),
		"WWENEMY_HD_DIRECT":           uint64(EnemyHDDirect),
		"WWENEMY_HD_FRONT":            uint64(EnemyHDFront),
		"WWENEMY_OWNER_SLOT_BASE":     uint64(EnemyOwnerSlotBase),
		"WWENEMY_ANNEX_OFF":           uint64(EnemyAnnexOff),
		"WWENEMY_ANNEX_SIZE":          uint64(EnemyAnnexSize),
		"WWENEMY_ANNEX_AUX_SIZE":      EnemyAnnexAuxSize,
		"WWENEMY_ANNEX_SEQ_OFF":       uint64(EnemyAnnexSeqOff),
		"WWENEMY_ANNEX_OVR_OFF":       uint64(EnemyAnnexOvrOff),
		"WWENEMY_ANNEX_OVR_SIZE":      EnemyAnnexOvrSize,
		"WWENEMY_ANNEX_OVR_HOOKS_OFF": EnemyAnnexOvrHooksOff,
		"WWENEMY_ANNEX_OVR_QUIET_OFF": EnemyAnnexOvrQuietOff,
		"WWENEMY_ANNEX_OVR_TG_UP_OFF": EnemyAnnexOvrTgUpOff,
		"WWENEMY_ANNEX_INVW_OFF":      uint64(EnemyAnnexInvWOff),
		"WWENEMY_ANNEX_KINDS":         EnemyAnnexKinds,
		"WWENEMY_ANNEX_TN_TGUP_OFF":   uint64(EnemyAnnexTnTgUpOff),
		"WWENEMY_HKD_MODEL_EQ":        uint64(EnemyHKDModelEq),
		"WWENEMY_HKD_ATTACH":          uint64(EnemyHKDAttach),
		"WWENEMY_HKD_EYE":             uint64(EnemyHKDEye),
		"WWENEMY_HKD_LEGACY_DEATH":    uint64(EnemyHKDLegacyDeath),
		"WWENEMY_HKD_ATTN":            uint64(EnemyHKDAttn),
		"WWENEMY_HKD_MTX_PLAIN":       uint64(EnemyHKDMtxPlain),
		"WWENEMY_HKD_SPECIAL":         uint64(EnemyHKDSpecial),
		"WWENEMY_HKD_EXTRA":           uint64(EnemyHKDExtra),
		"WWENEMY_ANNEX_HKDX_OFF":      uint64(EnemyAnnexHKDXOff),
		"WWENEMY_ANNEX_HKDX_KINDS":    EnemyAnnexHKDXKinds,
		"WWENEMY_KNOB_OFF":            uint64(EnemyKnobOff),
		"WWENEMY_KNOB_SIZE":           uint64(EnemyKnobSize),
		"WWENEMY_KNOB_KINDS":          EnemyKnobKinds,
		"WWENEMY_KNOB_GEN_HEAD_OFF":   EnemyKnobGenHeadOff,
		"WWENEMY_KNOB_LAYOUT_OFF":     EnemyKnobLayoutOff,
		"WWENEMY_KNOB_COUNT_OFF":      EnemyKnobCountOff,
		"WWENEMY_KNOB_OVR_OFF":        EnemyKnobOvrOff,
		"WWENEMY_KNOB_INVW_OFF":       EnemyKnobInvWOff,
		"WWENEMY_KNOB_GEN_TAIL_OFF":   EnemyKnobGenTailOff,
		"WWENEMY_KNOB_ID_MIN":         EnemyKnobIDMin,
		"WWENEMY_STALE_FRAMES":        EnemyStaleFrames,
		"WWENEMY_SPH_OFF":             uint64(EnemySphOff),
		"WWENEMY_STTS_OFF":            uint64(EnemySttsOff),
		"WWENEMY_REGION_END":          uint64(EnemyRegionEnd),
	}
	for name, want := range defs {
		got, ok := pins[name]
		if !ok {
			t.Errorf("%s = 0x%X in wwenemy.h has no Go constant", name, want)
			continue
		}
		if got != want {
			t.Errorf("%s: Go 0x%X, wwenemy.h 0x%X", name, got, want)
		}
	}
	for name := range pins {
		if _, ok := defs[name]; !ok {
			t.Errorf("Go pins %s, which wwenemy.h does not define", name)
		}
	}
}

// TestEnemyBlockPlacement: the block lies in the slack between the
// Mailbox struct and the patched arena, clear of the REL words, and its
// layout adds up: the REL span starts where the Go span ends, the slots
// and status entries fit, the magic spells 'WWEN', and the canary regions
// are the slack less the block.
func TestEnemyBlockPlacement(t *testing.T) {
	slack := addr.Region{Name: "slack", Start: Mailbox + MailboxSize, End: PatchedArenaLo}
	if !slack.Contains(EnemyBlockAddr, int(EnemySize)) {
		t.Errorf("enemy block 0x%08X+0x%X is not inside the slack 0x%08X-0x%08X", EnemyBlockAddr, EnemySize, slack.Start, slack.End)
	}
	relStart, relEnd := Mailbox+OffRELMagic, Mailbox+OffRELFrame+4
	if EnemyBlockAddr < relEnd && relStart < EnemyBlockAddr+EnemySize {
		t.Errorf("enemy block 0x%08X-0x%08X overlaps the REL words 0x%08X-0x%08X", EnemyBlockAddr, EnemyBlockAddr+EnemySize, relStart, relEnd)
	}
	if EnemyBlockAddr != 0x80413100 || EnemyBlockAddr+EnemySize != 0x80413298 {
		t.Errorf("enemy block at 0x%08X-0x%08X, the design says 0x80413100-0x80413298", EnemyBlockAddr, EnemyBlockAddr+EnemySize)
	}
	// The host REL's static collision objects sit past the block, inside
	// the region, 4-aligned, below the arena (design wire_and_block).
	sph, stts := Mailbox+EnemySphOff, Mailbox+EnemySttsOff
	if sph != 0x804132A0 || stts != 0x804133D0 || Mailbox+EnemyRegionEnd != 0x80413410 {
		t.Errorf("collision objects at 0x%08X / 0x%08X, region end 0x%08X; the design says 0x804132A0 / 0x804133D0 / 0x80413410", sph, stts, Mailbox+EnemyRegionEnd)
	}
	if EnemyBlockAddr+EnemySize > sph || sph+EnemySphSize > stts || stts+EnemySttsSize > Mailbox+EnemyRegionEnd || Mailbox+EnemyRegionEnd > PatchedArenaLo || sph%4 != 0 || stts%4 != 0 {
		t.Error("the collision objects overlap the block, each other, the region end or the arena, or are misaligned")
	}
	if EnemyBlockRegion != (addr.Region{Name: "enemy block", Start: Mailbox + EnemyAnnexOff, End: Mailbox + EnemyRegionEnd}) {
		t.Errorf("EnemyBlockRegion %+v", EnemyBlockRegion)
	}
	if CanaryArena.End > EnemyBlockRegion.Start || CanaryArenaHigh.Start < EnemyBlockRegion.End {
		t.Error("the canary regions reach into the enemy block")
	}
	for _, r := range []addr.Region{CanaryTRK, CanaryArena, CanaryArenaHigh} {
		if r.Start < EnemyKnobRegion.End && EnemyKnobRegion.Start < r.End {
			t.Errorf("%s 0x%08X-0x%08X reaches into the knob table %+v", r.Name, r.Start, r.End, EnemyKnobRegion)
		}
	}
	if !CanaryArena.Contains(relStart, int(relEnd-relStart)) {
		t.Error("the REL words left the low canary region")
	}
	// Layout 3's annex fills the slack between the REL's shanm word and the
	// block exactly, and the region (what canaryForbidden covers) starts
	// at it.
	if EnemyAnnexOff != OffRELShanm+4 || EnemyAnnexOff+EnemyAnnexSize != EnemyOff || EnemyAnnexAddr != 0x804130C8 {
		t.Errorf("annex at Mailbox+0x%X size 0x%X; the design says +0x1C8, right after the shanm word and up to the block", EnemyAnnexOff, EnemyAnnexSize)
	}
	if EnemyAnnexSeqOff != EnemyAnnexOff+EnemyMaxSlots*EnemyAnnexAuxSize || EnemyAnnexOvrOff != EnemyAnnexSeqOff+4 ||
		EnemyAnnexInvWOff != EnemyAnnexOvrOff+EnemyAnnexKinds*EnemyAnnexOvrSize || EnemyAnnexInvWOff+EnemyAnnexKinds+1 > EnemyAnnexHKDXOff ||
		EnemyAnnexHKDXOff+EnemyAnnexHKDXKinds != EnemyOff || EnemyWriteSpan != 0x15C {
		t.Error("the annex's fields overlap, or run past the block")
	}
	if EnemySLCcM316ZOff != EnemySLAttackOff || EnemySLPKeyOff+4 != EnemySLAttackOff || EnemySLProcOff != EnemySLBlurShapeOff+1 ||
		EnemyKindMaskOff+4 != EnemyHkdHiOff || EnemyHkdHiOff+EnemyHkdHiKinds != EnemySlotsOff {
		t.Error("layout 3's slot or header fields moved")
	}
	// Layout 4 (batch C D1, D2, D4) moves nothing: knob_gen fills the pad
	// between ROLE and KIND_MASK, the ids 7..14 hook bytes the old reserved
	// u32[2] (inside the Go span, so a kill switch never tears), the echo and
	// fp_miss the old reserved2 u32; the knob table lies in the free slack
	// past the remote-Link block, outside the enemy region, below the arena.
	if EnemyKnobGenOff != EnemyRoleOff+1 || EnemyKnobGenOff+2 != EnemyKindMaskOff || EnemyHkdHiOff+EnemyHkdHiKinds > EnemyGoSpan ||
		EnemyKnobSeenOff != EnemyHitResultOff+4 || EnemyFpMissOff != EnemyKnobSeenOff+2 || EnemyTailSpareOff+1 != int(EnemySize) ||
		7+EnemyHkdHiKinds < EnemyKnobKinds-1 {
		t.Error("layout 4's block fields moved")
	}
	if EnemyKnobAddr != 0x80413750 || EnemyKnobRegion.End != 0x804137B0 || EnemyKnobRegion.Start < Mailbox+EnemyRegionEnd ||
		EnemyKnobRegion.End > PatchedArenaLo || EnemyKnobOvrOff+EnemyKnobKinds*EnemyAnnexOvrSize != EnemyKnobInvWOff ||
		EnemyKnobInvWOff+EnemyKnobKinds > EnemyKnobGenTailOff || EnemyKnobGenTailOff+4 > int(EnemyKnobSize) || EnemyKnobAddr%4 != 0 {
		t.Errorf("knob table %+v: fields overlap or it left the free slack", EnemyKnobRegion)
	}
	if EnemyHDKind != RemoteHDNoBody+1 || EnemyHDDirect != EnemyHDKind+1 || EnemyHDFront != EnemyHDDirect+1 ||
		EnemyAnnexTnTgUpOff != EnemyAnnexInvWOff+EnemyAnnexKinds || EnemyAnnexTnTgUpOff+1 != EnemyAnnexHKDXOff ||
		(EnemyHAFForceV1|EnemyHAFFallbackShrink|EnemyHAFNoHitMark|RemoteHAFAttribBody|RemoteHAFSlot1)&(EnemyHAFVerdict|EnemyHAFBack|EnemyHAFCrit) != 0 ||
		EnemyStateDead != 6 || EnemyErrKey != 10 {
		t.Error("layout 3's new enum values moved")
	}
	if string(binary.BigEndian.AppendUint32(nil, EnemyMagic)) != "WWEN" {
		t.Errorf("EnemyMagic 0x%08X is not 'WWEN'", EnemyMagic)
	}
	for _, c := range []struct {
		name      string
		got, want int
	}{
		{"slots end", EnemySlotsOff + EnemyMaxSlots*EnemySlotSize, EnemyHitApplyOff},
		{"hit_apply end", EnemyHitApplyOff + EnemyHASize, EnemySeqTailOff},
		{"seq_tail", EnemySeqTailOff + 4, EnemyGoSpan},
		{"REL span start", EnemyAckSeqOff, EnemyGoSpan},
		{"status end", EnemyStatusOff + EnemyMaxSlots*EnemyStatusSize, EnemyHitsOff},
		{"hits end", EnemyHitsOff + EnemyMaxSlots*EnemyHitSize, EnemyHitApplyAckOff},
		{"hit_apply_ack end", EnemyHitApplyAckOff + 4, EnemyCanariesOff},
		{"canaries end", EnemyCanariesOff + 4, EnemyCanaries2Off},
		{"canaries2 end", EnemyCanaries2Off + 4, EnemyHitResultOff},
		{"hit_result end", EnemyHitResultOff + 4, EnemyKnobSeenOff},
		{"tail spare end", EnemyTailSpareOff + 1, int(EnemySize)},
		{"slot sample end", EnemySLSampleOff + 4, EnemySLBckOff},
		{"slot bytes", EnemySLBckOff + 6, EnemySLBlurShapeOff + 1},
		{"slot blur_scale", EnemySLProcOff + 2, EnemySLBlurScaleOff},
		{"slot blur_scale end", EnemySLBlurScaleOff + 4, EnemySlotSize},
		{"slots end (0x100)", EnemySlotsOff + EnemyMaxSlots*EnemySlotSize, 0x100},
		{"hit_apply flags after collider", EnemyHAColliderOff + 1, EnemyHAFlagsOff},
		{"hit_apply se after atp", EnemyHAAtpOff + 1, EnemyHASeOff},
		{"hit_apply spl before pos", EnemyHASplOff + 1, EnemyHAPosOff},
		{"hit_apply pid end", EnemyHAPIDOff + 4, EnemyHASize},
		{"status anm_state after last_err", EnemySTLastErrOff + 1, EnemySTAnmStateOff},
		{"status bck after frames", EnemySTFramesOff + 2, EnemySTBckOff},
		{"status anm_changes end", EnemySTAnmChangesOff + 1, EnemyStatusSize},
	} {
		if c.got != c.want {
			t.Errorf("%s: 0x%X, want 0x%X", c.name, c.got, c.want)
		}
	}
	if EnemyHitApplyOff+EnemyHASize > EnemySeqTailOff {
		t.Error("hit_apply runs into seq_tail")
	}
	if EnemySlotAddr(3, EnemySLBlurScaleOff)+4 != EnemyBlockAddr+EnemyHitApplyOff {
		t.Errorf("EnemySlotAddr(3, blur_scale) = 0x%08X", EnemySlotAddr(3, EnemySLBlurScaleOff))
	}
	// The hit details are one byte each and the Go-side ones follow the
	// REL's; the results fit the byte too.
	if EnemyHDGoGate <= EnemyHDFallbackShrink || EnemyHDDemo != 15 || EnemyHRParried != 4 {
		t.Error("hit result or detail values moved")
	}
}

func sampleEnemyBlock() *EnemyBlock {
	return &EnemyBlock{
		Magic: EnemyMagic, Seq: 0x01020304, HostBeat: 777, GoWriteMS: 123456, Flags: EnemyFlagMirror | EnemyFlagRaw,
		Layout: EnemyLayout, Epoch: 3, Stage: "M_Dai", Room: 1, Role: EnemyRoleClient, KindMask: 0b101,
		Slots: []EnemySlot{
			{PID: 0x1234, Pos: [3]float32{-100.5, 20, 3e4}, AngleY: -0x4000, Tilt: 2, PKey: 0xD0264F5D,
				AttackType: 1, AttackPhase: 2, Health: 3, Flags: EnemySFSnap | EnemySFDying, MorfFrame: 7.5, MorfRate: 1, SampleSeq: 99,
				Bck: 0x6A, AnmAttr: 2, AnmSeq: 17, Morf: 15, Flags2: EnemySF2HasWeapon | EnemySF2BlurOn | EnemySF2Quiet, BlurShape: 3, Proc: 0x00BD, BlurScale: 1.25},
			{PID: 0, Pos: [3]float32{1, 2, 3}, Bck: EnemyBckNone, AnmAttr: 0xFF, Proc: 0x00CF},
		},
		Annex: EnemyAnnex{
			Aux:        [EnemyMaxSlots][4]int16{1: {-5, 6, 7, -8}},
			Override:   [EnemyAnnexKinds]EnemyKindOverride{1: {HookDisable: EnemyHKDAttach, QuietFrames: 30, TgHitUp: 50}},
			InvulnWait: [EnemyAnnexKinds]uint8{0, 12, 0},
			// kind id 4's hook byte (the allow batch's HKDX, D3)
			HookDisableX: [EnemyAnnexHKDXKinds]uint8{1: EnemyHKDSpecial | EnemyHKDExtra},
		},
		HitApply: EnemyHitApply{Seq: 5, SetID: 2, Collider: 1, Flags: EnemyHAFNoHitMark, AtType: 0x80000, Atp: 2, Se: 1, HitMark: 0xD, Spl: 1, Pos: [3]float32{4, 5, 6}, PID: 421},
	}
}

// TestEnemyBlockEncode: Encode is EnemyGoSpan bytes with seq_head ==
// seq_tail, every field at its header offset, and DecodeEnemyBlock reads
// it back; the encoder refuses NaN, too many slots and a long stage.
func TestEnemyBlockEncode(t *testing.T) {
	b := sampleEnemyBlock()
	out, err := b.Encode()
	if err != nil {
		t.Fatal(err)
	}
	be := binary.BigEndian
	if len(out) != EnemyGoSpan || be.Uint32(out[EnemySeqHeadOff:]) != b.Seq || be.Uint32(out[EnemySeqTailOff:]) != b.Seq {
		t.Fatalf("%d bytes, head %08X tail %08X", len(out), be.Uint32(out[EnemySeqHeadOff:]), be.Uint32(out[EnemySeqTailOff:]))
	}
	if be.Uint32(out[EnemyMagicOff:]) != EnemyMagic || out[EnemyCountOff] != 2 || out[EnemyFlagsOff] != b.Flags ||
		be.Uint16(out[EnemyLayoutOff:]) != 4 || be.Uint32(out[EnemyEpochOff:]) != 3 || be.Uint32(out[EnemyKindMaskOff:]) != 0b101 ||
		!bytes.Equal(out[EnemyStageOff:EnemyStageOff+8], []byte("M_Dai\x00\x00\x00")) ||
		out[EnemyRoomOff] != 1 || out[EnemyRoleOff] != EnemyRoleClient {
		t.Errorf("header %x", out[:EnemySlotsOff])
	}
	s0 := out[EnemySlotsOff:]
	if be.Uint32(s0[EnemySLPIDOff:]) != 0x1234 || math.Float32frombits(be.Uint32(s0[EnemySLPosOff+8:])) != 3e4 ||
		int16(be.Uint16(s0[EnemySLAngleOff:])) != -0x4000 || be.Uint32(s0[EnemySLPKeyOff:]) != 0xD0264F5D ||
		s0[EnemySLFlagsOff] != EnemySFSnap|EnemySFDying || be.Uint32(s0[EnemySLSampleOff:]) != 99 ||
		s0[EnemySLBckOff] != 0x6A || s0[EnemySLAttrOff] != 2 || s0[EnemySLAnmSeqOff] != 17 || s0[EnemySLMorfOff] != 15 ||
		s0[EnemySLFlags2Off] != EnemySF2HasWeapon|EnemySF2BlurOn|EnemySF2Quiet || s0[EnemySLBlurShapeOff] != 3 || s0[0x2E] != 0 || s0[0x2F] != 0xBD ||
		math.Float32frombits(be.Uint32(s0[EnemySLBlurScaleOff:])) != 1.25 {
		t.Errorf("slot 0 %x", s0[:EnemySlotSize])
	}
	// The slot is 0x34 bytes: bck..blur_scale at +0x28..+0x34, and the
	// second slot starts at +0x30+0x34.
	if s1 := out[EnemySlotsOff+EnemySlotSize:]; be.Uint32(s1[EnemySLPIDOff:]) != 0 || math.Float32frombits(be.Uint32(s1[EnemySLPosOff+4:])) != 2 || s1[EnemySLBckOff] != 0xFF || s1[EnemySLAttrOff] != 0xFF {
		t.Errorf("slot 1 %x", s1[:EnemySlotSize])
	}
	ha := out[EnemyHitApplyOff:]
	if be.Uint32(ha[EnemyHASeqOff:]) != 5 || be.Uint16(ha[EnemyHASetIDOff:]) != 2 || ha[EnemyHAColliderOff] != 1 || ha[EnemyHAFlagsOff] != EnemyHAFNoHitMark ||
		be.Uint32(ha[EnemyHAAtTypeOff:]) != 0x80000 || ha[EnemyHAAtpOff] != 2 || ha[EnemyHASeOff] != 1 || ha[EnemyHAHitMarkOff] != 0xD || ha[EnemyHASplOff] != 1 ||
		math.Float32frombits(be.Uint32(ha[EnemyHAPosOff+4:])) != 5 || be.Uint32(ha[EnemyHAPIDOff:]) != 421 {
		t.Errorf("hit_apply %x", ha[:EnemyHASize])
	}
	// hit_apply at +0x100 with flags/se/hit_mark/spl at +0x107/+0x10D/
	// +0x10E/+0x10F and seq_tail at +0x120 (design tests_and_live_checks).
	if out[0x107] != EnemyHAFNoHitMark || out[0x10D] != 1 || out[0x10E] != 0xD || out[0x10F] != 1 || be.Uint32(out[0x120:]) != b.Seq || len(out) != 0x124 {
		t.Errorf("hit_apply bytes at their absolute offsets: %x", out[0x100:])
	}
	// Slot 0's layout-3 bytes at their absolute offsets: the pkey at
	// +0x30+0x14; bck, attr, anm_seq, morf, flags2, blur_shape, the u16
	// proc and the f32 blur_scale at +0x30+0x28..+0x30+0x33; kind_mask at
	// +0x24; hit_apply's pid at +0x11C.
	if got := hex.EncodeToString(out[0x30+0x28 : 0x30+0x34]); got != "6a02110f850300bd3fa00000" || be.Uint32(out[0x11C:]) != 421 ||
		hex.EncodeToString(out[0x30+0x14:0x30+0x18]) != "d0264f5d" || hex.EncodeToString(out[0x24:0x28]) != "00000005" {
		t.Errorf("slot 0 bck..blur_scale %s, hit_apply pid %d", got, be.Uint32(out[0x11C:]))
	}
	if got := hex.EncodeToString(out); got != enemyGoSpanGolden {
		t.Errorf("the Go span's bytes changed:\n got %s\nwant %s", got, enemyGoSpanGolden)
	}
	got, tail, err := DecodeEnemyBlock(out)
	got.Annex = b.Annex // the block decode leaves the annex to DecodeEnemyAnnex
	if err != nil || tail != b.Seq || !reflect.DeepEqual(&got, b) {
		t.Errorf("decode: tail %08X, %v\n got %+v\nwant %+v", tail, err, got, *b)
	}
	if c := (EnemySlot{}); func() bool {
		c.SetCcM316Z(-0x1234)
		return c.CcM316Z() != -0x1234 || c.AttackType != 0xED || c.AttackPhase != 0xCC
	}() {
		t.Error("SetCcM316Z does not store m316.z big-endian over attack/phase")
	}

	// The whole write: the annex (annex_seq == the block's seq) then the
	// block, from EnemyAnnexAddr.
	w, err := b.EncodeWrite()
	if err != nil {
		t.Fatal(err)
	}
	ax := w[:EnemyAnnexSize]
	if len(w) != EnemyWriteSpan || !bytes.Equal(w[EnemyAnnexSize:], out) || be.Uint32(ax[EnemyAnnexSeqOff-EnemyAnnexOff:]) != b.Seq ||
		hex.EncodeToString(ax[8:16]) != "fffb00060007fff8" ||
		hex.EncodeToString(ax[EnemyAnnexOvrOff-EnemyAnnexOff+4:EnemyAnnexOvrOff-EnemyAnnexOff+8]) != "021e0032" ||
		hex.EncodeToString(ax[EnemyAnnexInvWOff-EnemyAnnexOff:EnemyAnnexSize]) != "000c0000"+"00c00000" {
		t.Errorf("annex %x", ax)
	}
	gotAx, axSeq, err := DecodeEnemyAnnex(ax)
	if err != nil || axSeq != b.Seq || gotAx != b.Annex {
		t.Errorf("annex decode: seq %08X %v\n got %+v\nwant %+v", axSeq, err, gotAx, b.Annex)
	}
	if _, _, err := DecodeEnemyAnnex(ax[:EnemyAnnexSize-1]); err == nil {
		t.Error("a short annex decoded")
	}

	b.Slots[1].Pos[0] = float32(math.NaN())
	if _, err := b.Encode(); !errors.Is(err, ErrEnemyNotFinite) {
		t.Errorf("NaN: %v", err)
	}
	b = sampleEnemyBlock()
	b.Slots[0].BlurScale = float32(math.Inf(1))
	if _, err := b.Encode(); !errors.Is(err, ErrEnemyNotFinite) {
		t.Errorf("Inf blur scale: %v", err)
	}
	b = sampleEnemyBlock()
	b.Slots = make([]EnemySlot, 5)
	if _, err := b.Encode(); err == nil {
		t.Error("5 slots encoded")
	}
	b = sampleEnemyBlock()
	b.Stage = "123456789"
	if _, err := b.Encode(); err == nil {
		t.Error("a 9-byte stage encoded")
	}
	if _, _, err := DecodeEnemyBlock(out[:EnemyGoSpan-1]); err == nil {
		t.Error("a short block decoded")
	}
}

// TestEnemyBlockL4: layout 4's block fields sit at their header offsets
// inside the Go span (knob_gen u16 at +0x22, ids 7..14 hook bytes at
// +0x28..+0x2F) and round-trip, with every layout-3 byte unchanged.
func TestEnemyBlockL4(t *testing.T) {
	b := sampleEnemyBlock()
	base, err := b.Encode()
	if err != nil {
		t.Fatal(err)
	}
	b.KnobGen = 0xBEEF
	b.HkdHi = [EnemyHkdHiKinds]uint8{0: EnemyHKDAttach, 1: EnemyHKDExtra, 7: 0x5A}
	out, err := b.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(out[0x20:0x30]); got != "0102beef00000005"+"028000000000005a" {
		t.Errorf("header +0x20..+0x30 %s", got)
	}
	for i := range out {
		if (i >= EnemyKnobGenOff && i < EnemyKnobGenOff+2) || (i >= EnemyHkdHiOff && i < EnemyHkdHiOff+EnemyHkdHiKinds) {
			continue
		}
		if out[i] != base[i] {
			t.Errorf("byte +0x%X moved: 0x%02X, layout 3 had 0x%02X", i, out[i], base[i])
		}
	}
	got, _, err := DecodeEnemyBlock(out)
	if err != nil || got.KnobGen != 0xBEEF || got.HkdHi != b.HkdHi {
		t.Errorf("decode: knob_gen %04X hkd_hi %x %v", got.KnobGen, got.HkdHi, err)
	}
}

// TestEnemyKnobsEncode: the knob table's bytes at their header offsets,
// the gen words both ends, and DecodeEnemyKnobs' validity rule (the REL's
// em_knob_validate): gen != 0, head == tail == gen, layout 4, count >= kinds.
func TestEnemyKnobsEncode(t *testing.T) {
	k := &EnemyKnobs{Gen: 0x1234}
	k.Quiet[3], k.TgUp[7], k.Invuln[8], k.Hook[15] = 60, -20, 9, EnemyHKDAttn
	out := k.Encode()
	be := binary.BigEndian
	if len(out) != int(EnemyKnobSize) || be.Uint32(out[0x00:]) != 0x1234 || be.Uint16(out[0x04:]) != 4 || out[0x06] != 16 ||
		out[0x08+3*4+1] != 60 || int16(be.Uint16(out[0x08+7*4+2:])) != -20 || out[0x48+8] != 9 || out[0x08+15*4] != EnemyHKDAttn ||
		be.Uint32(out[0x58:]) != 0x1234 {
		t.Fatalf("knob table %x", out)
	}
	got, ok, err := DecodeEnemyKnobs(out, 0x1234, 9)
	if err != nil || !ok || got != *k {
		t.Errorf("decode ok=%v %v\n got %+v\nwant %+v", ok, err, got, *k)
	}
	for _, c := range []struct {
		name  string
		mod   func(b []byte)
		gen   uint16
		kinds int
	}{
		{"gen 0", func([]byte) {}, 0, 9},
		{"another gen", func([]byte) {}, 0x1235, 9},
		{"torn tail", func(b []byte) { b[0x5B] ^= 1 }, 0x1234, 9},
		{"layout 3", func(b []byte) { b[0x05] = 3 }, 0x1234, 9},
		{"too few rows", func([]byte) {}, 0x1234, 17},
	} {
		b := append([]byte(nil), out...)
		c.mod(b)
		if _, ok, _ := DecodeEnemyKnobs(b, c.gen, c.kinds); ok {
			t.Errorf("%s: the table validated", c.name)
		}
	}
	m := &fakeWords{}
	if err := WriteEnemyKnobs(m, k); err != nil {
		t.Fatal(err)
	}
	if back, ok, err := ReadEnemyKnobs(m, 0x1234, 9); err != nil || !ok || back != *k {
		t.Errorf("read back ok=%v %v %+v", ok, err, back)
	}
	if _, _, err := DecodeEnemyKnobs(out[:EnemyKnobSize-1], 0x1234, 9); err == nil {
		t.Error("a short table decoded")
	}
}

// TestEnemyBlockWriteReadStatus: WriteEnemyBlock is one write of the
// annex and the Go span from the annex; ClearEnemyBlock writes the magic
// word alone;
// ReadEnemyStatus decodes what a REL would leave in the tail.
func TestEnemyBlockWriteReadStatus(t *testing.T) {
	m := &fakeWords{}
	if err := WriteEnemyBlock(m, sampleEnemyBlock()); err != nil {
		t.Fatal(err)
	}
	blk, tail, st, err := ReadEnemyBlock(m)
	if err != nil || blk.Magic != EnemyMagic || tail != 0x01020304 || st != (EnemyStatus{}) || blk.Slots[0].PKey != 0xD0264F5D {
		t.Fatalf("read back: %+v tail %08X status %+v %v", blk, tail, st, err)
	}
	if ax, seq, err := ReadEnemyAnnex(m); err != nil || seq != 0x01020304 || ax != sampleEnemyBlock().Annex {
		t.Fatalf("annex read back: %+v seq %08X %v", ax, seq, err)
	}
	// The REL's tail.
	tailBytes := make([]byte, EnemySize-EnemyGoSpan)
	be := binary.BigEndian
	at := func(off int) []byte { return tailBytes[off-EnemyGoSpan:] }
	be.PutUint32(at(EnemyAckSeqOff), 0x01020304)
	be.PutUint32(at(EnemyAckFrameOff), 4242)
	be.PutUint32(at(EnemyAdoptedOff), 0b0101)
	be.PutUint32(at(EnemyEpochSeenOff), 3)
	st2 := at(EnemyStatusOff + 2*EnemyStatusSize)
	be.PutUint32(st2[EnemySTActorOff:], 0x80B00000)
	be.PutUint32(st2[EnemySTPIDOff:], 0x1234)
	st2[EnemySTStateOff] = EnemyStateMirrored
	st2[EnemySTPausePrevOff] = 0x02
	st2[EnemySTLastErrOff] = EnemyErrNone
	st2[EnemySTAnmStateOff] = EnemyAnmSynced
	be.PutUint16(st2[EnemySTFramesOff:], 300)
	st2[EnemySTBckOff] = 0x73
	st2[EnemySTAnmChangesOff] = 9
	h1 := at(EnemyHitsOff + 1*EnemyHitSize)
	be.PutUint16(h1, 7)
	h1[2], h1[3] = 2, 1
	be.PutUint32(at(EnemyHitApplyAckOff), 5)
	copy(at(EnemyCanariesOff), []byte{1, 2, 3, 4})
	copy(at(EnemyCanaries2Off), []byte{5, 6, 7, 8})
	be.PutUint32(at(EnemyHitResultOff), uint32(EnemyHRAppliedAt)<<24|uint32(EnemyHDNone)<<16|2<<8|1)
	// Layout 4's echo and fp_miss in the old reserved2 word; its last byte
	// is spare and not decoded.
	be.PutUint32(at(EnemyKnobSeenOff), 0xBEEF03EE)
	if err := m.Write(EnemyBlockAddr+EnemyGoSpan, tailBytes); err != nil {
		t.Fatal(err)
	}
	st, err = ReadEnemyStatus(m)
	if err != nil {
		t.Fatal(err)
	}
	want := EnemyStatus{AckSeq: 0x01020304, AckFrame: 4242, AdoptedMask: 5, EpochSeen: 3, HitApplyAck: 5,
		Canaries: EnemyCanaries{1, 2, 3, 4}, Canaries2: EnemyCanaries2{5, 6, 7, 8},
		HitResult:   EnemyHitResult{Result: EnemyHRAppliedAt, Detail: EnemyHDNone, Frames: 2, Sets: 1},
		KnobGenSeen: 0xBEEF, FpMiss: 3}
	want.Status[2] = EnemySlotStatus{Actor: 0x80B00000, PID: 0x1234, State: EnemyStateMirrored, PausePrev: 2, AnmState: EnemyAnmSynced, Frames: 300, Bck: 0x73, AnmChanges: 9}
	want.Hits[1] = EnemyHitEntry{HitSeq: 7, Collider: 2, Pending: 1}
	if st != want {
		t.Errorf("status\n got %+v\nwant %+v", st, want)
	}
	if _, _, st3, err := ReadEnemyBlock(m); err != nil || st3 != want {
		t.Errorf("ReadEnemyBlock status %+v %v", st3, err)
	}

	if err := ClearEnemyBlock(m); err != nil {
		t.Fatal(err)
	}
	blk, tail, _, err = ReadEnemyBlock(m)
	if err != nil || blk.Magic != 0 || blk.Seq != 0x01020304 || tail != 0x01020304 || blk.Role != EnemyRoleClient {
		t.Errorf("after clear: %+v tail %08X %v", blk, tail, err)
	}
	if _, err := DecodeEnemyStatus(tailBytes[:len(tailBytes)-1]); err == nil {
		t.Error("a short status decoded")
	}
}

// TestEnemyStatusOffsets: DecodeEnemyStatus reads each field at the
// layout-2 block offset the REL writes it (literal offsets, not the
// constants): every 0x10-byte status entry from 0x134 with anm_state at
// +0x0B, bck at +0x0E and anm_changes at +0x0F, canaries2 at 0x18C, and
// the HIT_RESULT word at 0x190 whose first byte is the result.
func TestEnemyStatusOffsets(t *testing.T) {
	blk := make([]byte, 0x198)
	for i := range 4 {
		e := blk[0x134+0x10*i:]
		e[0x08] = EnemyStateMirrored
		e[0x0B] = EnemyAnmSynced + uint8(i)
		e[0x0E] = byte(0x3E + i)
		e[0x0F] = byte(10 * (i + 1))
	}
	copy(blk[0x18C:], []byte{0x11, 0x22, 0x33, 0x44})
	copy(blk[0x190:], []byte{EnemyHRParried, EnemyHDDefence, 3, 2})
	st, err := DecodeEnemyStatus(blk[0x124:])
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range st.Status {
		if s.State != EnemyStateMirrored || s.AnmState != EnemyAnmSynced+uint8(i) || s.Bck != 0x3E+uint8(i) || s.AnmChanges != uint8(10*(i+1)) || s.Frames != 0 || s.LastErr != 0 {
			t.Errorf("status %d: %+v", i, s)
		}
	}
	if st.Canaries2 != (EnemyCanaries2{AnmResNull: 0x11, AnmRange: 0x22, HitNoCross: 0x33, HitAborted: 0x44}) || st.Canaries != (EnemyCanaries{}) {
		t.Errorf("canaries %+v, canaries2 %+v", st.Canaries, st.Canaries2)
	}
	if st.HitResult != (EnemyHitResult{Result: EnemyHRParried, Detail: EnemyHDDefence, Frames: 3, Sets: 2}) || st.HitApplyAck != 0 {
		t.Errorf("hit result %+v (ack %d)", st.HitResult, st.HitApplyAck)
	}
}

// enemyGoSpanGolden is sampleEnemyBlock's Encode, byte for byte: the
// layout-4 Go span (0x124 bytes) the REL reads; with KnobGen and HkdHi
// zero it is layout 3's but for the layout word at +0x12 (batch C D1). A
// change here is a layout change and needs WWENEMY_LAYOUT bumped on both
// sides.
const enemyGoSpanGolden = "" +
	"5757454e01020304000003090001e24002090004000000034d5f446169000000" +
	"0102000000000005000000000000000000001234c2c9000041a0000046ea6000" +
	"c0000002d0264f5d0102030340f000003f800000000000636a02110f850300bd" +
	"3fa00000000000003f8000004000000040400000000000000000000000000000" +
	"000000000000000000000000ffff0000000000cf000000000000000000000000" +
	"0000000000000000000000000000000000000000000000000000000000000000" +
	"0000000000000000000000000000000000000000000000000000000000000000" +
	"0000000000000000000000000000000000000000000000000000000000000000" +
	"00000005000201040008000002010d014080000040a0000040c00000000001a5" +
	"01020304"
