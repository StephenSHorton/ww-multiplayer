package mailbox

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// wwremoteHeader and wwrelHeader are the C sources of these constants (the
// REL module and T2 read the same files).
const (
	wwremoteHeader = "../../inject/include/wwremote.h"
	wwrelHeader    = "../../inject/include/wwrel.h"
)

// pinHeader checks every define of path against pins and every pin
// against the header.
func pinHeader(t *testing.T, path string, defs map[string]uint64, pins map[string]uint64) {
	t.Helper()
	for name, want := range defs {
		got, ok := pins[name]
		if !ok {
			t.Errorf("%s = 0x%X in %s has no Go constant", name, want, path)
			continue
		}
		if got != want {
			t.Errorf("%s: Go 0x%X, %s 0x%X", name, got, path, want)
		}
	}
	for name := range pins {
		if _, ok := defs[name]; !ok {
			t.Errorf("Go pins %s, which %s does not define", name, path)
		}
	}
}

// TestRemoteHeaderPins: every Go constant equals its WWREMOTE_* define,
// and every define has a Go constant.
func TestRemoteHeaderPins(t *testing.T) {
	defs := headerDefines(t, wwremoteHeader)
	if got, ok := defs["WWREMOTE_LAYOUT"]; !ok || got != uint64(RemoteLayout) {
		t.Fatalf("inject/include/wwremote.h is WWREMOTE_LAYOUT %d and this Go mirror is layout %d: a layout change must move both in the same PR", got, RemoteLayout)
	}
	pinHeader(t, wwremoteHeader, defs, map[string]uint64{
		"WWREMOTE_MAGIC":                  uint64(RemoteMagic),
		"WWREMOTE_OFF":                    uint64(RemoteOff),
		"WWREMOTE_SIZE":                   uint64(RemoteSize),
		"WWREMOTE_LAYOUT":                 uint64(RemoteLayout),
		"WWREMOTE_REGION_START":           uint64(RemoteRegionStart),
		"WWREMOTE_REGION_END":             uint64(RemoteRegionEnd),
		"WWREMOTE_MAX_SLOTS":              uint64(RemoteMaxSlots),
		"WWREMOTE_PROC_POLICY_MAX":        uint64(RemoteProcPolicyMax),
		"WWREMOTE_FF_DENY_MAX":            uint64(RemoteFFDenyMax),
		"WWREMOTE_STAGE_LEN":              uint64(RemoteStageLen),
		"WWREMOTE_STALE_FRAMES":           uint64(RemoteStaleFrames),
		"WWREMOTE_POSE_STALE_FRAMES":      uint64(RemotePoseStaleFrames),
		"WWREMOTE_MAGIC_OFF":              uint64(RemoteMagicOff),
		"WWREMOTE_SEQ_HEAD_OFF":           uint64(RemoteSeqHeadOff),
		"WWREMOTE_LAYOUT_OFF":             uint64(RemoteLayoutOff),
		"WWREMOTE_FLAGS_OFF":              uint64(RemoteFlagsOff),
		"WWREMOTE_EPOCH_OFF":              uint64(RemoteEpochOff),
		"WWREMOTE_MARGIN_Q8_OFF":          uint64(RemoteMarginQ8Off),
		"WWREMOTE_MIN_HOLD_OFF":           uint64(RemoteMinHoldOff),
		"WWREMOTE_BODY_IFRAMES_OFF":       uint64(RemoteBodyIFramesOff),
		"WWREMOTE_ROLE_OFF":               uint64(RemoteRoleOff),
		"WWREMOTE_DY_MAX_OFF":             uint64(RemoteDYMaxOff),
		"WWREMOTE_POLICY_COUNT_OFF":       uint64(RemotePolicyCountOff),
		"WWREMOTE_FF_COUNT_OFF":           uint64(RemoteFFCountOff),
		"WWREMOTE_CREATE_COOLDOWN_OFF":    uint64(RemoteCreateCooldownOff),
		"WWREMOTE_RESERVED_OFF":           uint64(RemoteReservedOff),
		"WWREMOTE_SLOTS_OFF":              uint64(RemoteSlotsOff),
		"WWREMOTE_SLOT_SIZE":              uint64(RemoteSlotSize),
		"WWREMOTE_APPLY_OFF":              uint64(RemoteApplyOff),
		"WWREMOTE_APPLY_SIZE":             uint64(RemoteApplySize),
		"WWREMOTE_FF_DENY_OFF":            uint64(RemoteFFDenyOff),
		"WWREMOTE_POLICY_OFF":             uint64(RemotePolicyOff),
		"WWREMOTE_POLICY_SIZE":            uint64(RemotePolicySize),
		"WWREMOTE_SEQ_TAIL_OFF":           uint64(RemoteSeqTailOff),
		"WWREMOTE_GO_SPAN":                uint64(RemoteGoSpan),
		"WWREMOTE_UNUSED_OFF":             uint64(RemoteUnusedOff),
		"WWREMOTE_FLAG_BODIES":            uint64(RemoteFlagBodies),
		"WWREMOTE_FLAG_SWAP":              uint64(RemoteFlagSwap),
		"WWREMOTE_FLAG_HITTABLE":          uint64(RemoteFlagHittable),
		"WWREMOTE_FLAG_DETECT":            uint64(RemoteFlagDetect),
		"WWREMOTE_FLAG_APPLY":             uint64(RemoteFlagApply),
		"WWREMOTE_FLAG_ATTRIB":            uint64(RemoteFlagAttrib),
		"WWREMOTE_FLAG_NAME_A9":           uint64(RemoteFlagNameA9),
		"WWREMOTE_FLAG_GUARD":             uint64(RemoteFlagGuard),
		"WWREMOTE_FLAG_FF_GATE_OFF":       uint64(RemoteFlagFFGateOff),
		"WWREMOTE_FLAG_ROUYA_GATE_OFF":    uint64(RemoteFlagRouyaGateOff),
		"WWREMOTE_FLAG_ATTACKER":          uint64(RemoteFlagAttacker),
		"WWREMOTE_FLAG_GROUP_WIDE":        uint64(RemoteFlagGroupWide),
		"WWREMOTE_FLAG_BOTH_WORDS_ENABLE": uint64(RemoteFlagBothWordsEnable),
		"WWREMOTE_FLAG_IFRAME_STAMP":      uint64(RemoteFlagIFrameStamp),
		"WWREMOTE_ROLE_HOST":              uint64(RemoteRoleHost),
		"WWREMOTE_ROLE_CLIENT":            uint64(RemoteRoleClient),
		"WWREMOTE_DEF_MARGIN_Q8":          uint64(RemoteDefMarginQ8),
		"WWREMOTE_DEF_MIN_HOLD":           uint64(RemoteDefMinHold),
		"WWREMOTE_DEF_BODY_IFRAMES":       uint64(RemoteDefBodyIFrames),
		"WWREMOTE_DEF_DY_MAX":             uint64(RemoteDefDYMax),
		"WWREMOTE_DEF_CREATE_COOLDOWN":    uint64(RemoteDefCreateCooldown),
		"WWREMOTE_DEF_EYE_DY":             uint64(RemoteDefEyeDY),
		"WWREMOTE_DEF_CYL_R":              uint64(RemoteDefCylR),
		"WWREMOTE_DEF_CYL_H":              uint64(RemoteDefCylH),
		"WWREMOTE_SL_ACTIVE_OFF":          uint64(RemoteSLActiveOff),
		"WWREMOTE_SL_SFLAGS_OFF":          uint64(RemoteSLSFlagsOff),
		"WWREMOTE_SL_ROOM_OFF":            uint64(RemoteSLRoomOff),
		"WWREMOTE_SL_CUT_TYPE_OFF":        uint64(RemoteSLCutTypeOff),
		"WWREMOTE_SL_CUT_COUNT_OFF":       uint64(RemoteSLCutCountOff),
		"WWREMOTE_SL_PLAYER_ID_OFF":       uint64(RemoteSLPlayerIDOff),
		"WWREMOTE_SL_IFRAMES_OFF":         uint64(RemoteSLIFramesOff),
		"WWREMOTE_SL_STAGE_OFF":           uint64(RemoteSLStageOff),
		"WWREMOTE_SL_POS_OFF":             uint64(RemoteSLPosOff),
		"WWREMOTE_SL_ANGLE_OFF":           uint64(RemoteSLAngleOff),
		"WWREMOTE_SL_SHIELD_ANGLE_OFF":    uint64(RemoteSLShieldAngleOff),
		"WWREMOTE_SL_EYE_DY_OFF":          uint64(RemoteSLEyeDYOff),
		"WWREMOTE_SL_CYL_R_OFF":           uint64(RemoteSLCylROff),
		"WWREMOTE_SL_CYL_H_OFF":           uint64(RemoteSLCylHOff),
		"WWREMOTE_SL_POSE_SLOT_OFF":       uint64(RemoteSLPoseSlotOff),
		"WWREMOTE_SL_GEN_OFF":             uint64(RemoteSLGenOff),
		"WWREMOTE_SL_PAD_OFF":             uint64(RemoteSLPadOff),
		"WWREMOTE_SF_TARGETABLE":          uint64(RemoteSFTargetable),
		"WWREMOTE_SF_GUARD":               uint64(RemoteSFGuard),
		"WWREMOTE_SF_NO_HIT":              uint64(RemoteSFNoHit),
		"WWREMOTE_SF_CROUCH_DEF":          uint64(RemoteSFCrouchDef),
		"WWREMOTE_SF_DEAD":                uint64(RemoteSFDead),
		"WWREMOTE_SF_IN_EVENT":            uint64(RemoteSFInEvent),
		"WWREMOTE_SF_SWIM":                uint64(RemoteSFSwim),
		"WWREMOTE_SF_ON_SHIP":             uint64(RemoteSFOnShip),
		"WWREMOTE_PS_GUARD":               uint64(RemotePSGuard),
		"WWREMOTE_PS_CROUCH_DEF":          uint64(RemotePSCrouchDef),
		"WWREMOTE_PS_DAMAGE_MODE":         uint64(RemotePSDamageMode),
		"WWREMOTE_PS_DEAD":                uint64(RemotePSDead),
		"WWREMOTE_PS_IN_EVENT":            uint64(RemotePSInEvent),
		"WWREMOTE_PS_SWIM":                uint64(RemotePSSwim),
		"WWREMOTE_PS_ON_SHIP":             uint64(RemotePSOnShip),
		"WWREMOTE_PS_LOADING":             uint64(RemotePSLoading),
		"WWREMOTE_AP_SEQ_OFF":             uint64(RemoteAPSeqOff),
		"WWREMOTE_AP_SETID_OFF":           uint64(RemoteAPSetIDOff),
		"WWREMOTE_AP_ATP_OFF":             uint64(RemoteAPAtpOff),
		"WWREMOTE_AP_SE_OFF":              uint64(RemoteAPSeOff),
		"WWREMOTE_AP_HITMARK_OFF":         uint64(RemoteAPHitMarkOff),
		"WWREMOTE_AP_SPL_OFF":             uint64(RemoteAPSplOff),
		"WWREMOTE_AP_AT_SPRM_OFF":         uint64(RemoteAPAtSPrmOff),
		"WWREMOTE_AP_FLAGS_OFF":           uint64(RemoteAPFlagsOff),
		"WWREMOTE_AP_ATTYPE_OFF":          uint64(RemoteAPAtTypeOff),
		"WWREMOTE_AP_REL_HIT_OFF":         uint64(RemoteAPRelHitOff),
		"WWREMOTE_AP_RVEC_OFF":            uint64(RemoteAPRVecOff),
		"WWREMOTE_APPLY_AT_SPRM":          uint64(RemoteApplyAtSPrm),
		"WWREMOTE_AF_HOST_SHIELD":         uint64(RemoteAFHostShield),
		"WWREMOTE_AF_HOST_GUARD":          uint64(RemoteAFHostGuard),
		"WWREMOTE_PP_PROC_OFF":            uint64(RemotePPProcOff),
		"WWREMOTE_PP_FLAGS_OFF":           uint64(RemotePPFlagsOff),
		"WWREMOTE_PP_PAD_OFF":             uint64(RemotePPPadOff),
		"WWREMOTE_PF_DENY":                uint64(RemotePFDeny),
		"WWREMOTE_PF_ALLOW":               uint64(RemotePFAllow),
		"WWREMOTE_PF_BOTH_WORDS":          uint64(RemotePFBothWords),
		"WWREMOTE_PF_NO_LATCH":            uint64(RemotePFNoLatch),
		"WWREMOTE_PROC_MO2":               uint64(RemoteProcMo2),
		"WWREMOTE_ACK_SEQ_OFF":            uint64(RemoteAckSeqOff),
		"WWREMOTE_ACK_FRAME_OFF":          uint64(RemoteAckFrameOff),
		"WWREMOTE_BODY_STATUS_OFF":        uint64(RemoteBodyStatusOff),
		"WWREMOTE_BODY_STATUS_SIZE":       uint64(RemoteBodyStatusSize),
		"WWREMOTE_COUNTERS_OFF":           uint64(RemoteCountersOff),
		"WWREMOTE_COUNTERS":               uint64(RemoteCounters),
		"WWREMOTE_HIT_RING_HEAD_OFF":      uint64(RemoteHitRingHeadOff),
		"WWREMOTE_HIT_RING_OFF":           uint64(RemoteHitRingOff),
		"WWREMOTE_HIT_RING_LEN":           uint64(RemoteHitRingLen),
		"WWREMOTE_HIT_SIZE":               uint64(RemoteHitSize),
		"WWREMOTE_APPLY_ACK_OFF":          uint64(RemoteApplyAckOff),
		"WWREMOTE_APPLY_RESULT_OFF":       uint64(RemoteApplyResultOff),
		"WWREMOTE_LIFE_BEFORE_OFF":        uint64(RemoteLifeBeforeOff),
		"WWREMOTE_LIFE_AFTER_OFF":         uint64(RemoteLifeAfterOff),
		"WWREMOTE_BS_ACTOR_OFF":           uint64(RemoteBSActorOff),
		"WWREMOTE_BS_PID_OFF":             uint64(RemoteBSPIDOff),
		"WWREMOTE_BS_STATE_OFF":           uint64(RemoteBSStateOff),
		"WWREMOTE_BS_ERR_OFF":             uint64(RemoteBSErrOff),
		"WWREMOTE_BS_HITS_OFF":            uint64(RemoteBSHitsOff),
		"WWREMOTE_BS_LATCHED_BY_OFF":      uint64(RemoteBSLatchedByOff),
		"WWREMOTE_BS_FRAMES_LIVE_OFF":     uint64(RemoteBSFramesLiveOff),
		"WWREMOTE_BS_TRIES_OFF":           uint64(RemoteBSTriesOff),
		"WWREMOTE_BS_PAD_OFF":             uint64(RemoteBSPadOff),
		"WWREMOTE_BODY_EMPTY":             uint64(RemoteBodyEmpty),
		"WWREMOTE_BODY_QUEUED":            uint64(RemoteBodyQueued),
		"WWREMOTE_BODY_LIVE":              uint64(RemoteBodyLive),
		"WWREMOTE_BODY_PARKED":            uint64(RemoteBodyParked),
		"WWREMOTE_BODY_DYING":             uint64(RemoteBodyDying),
		"WWREMOTE_BERR_NONE":              uint64(RemoteBErrNone),
		"WWREMOTE_BERR_SLOT":              uint64(RemoteBErrSlot),
		"WWREMOTE_BERR_BLOCK":             uint64(RemoteBErrBlock),
		"WWREMOTE_BERR_VTABLE":            uint64(RemoteBErrVtable),
		"WWREMOTE_BERR_HOST_LINK":         uint64(RemoteBErrHostLink),
		"WWREMOTE_BERR_CYL":               uint64(RemoteBErrCyl),
		"WWREMOTE_BERR_CREATE":            uint64(RemoteBErrCreate),
		"WWREMOTE_BERR_LOST":              uint64(RemoteBErrLost),
		"WWREMOTE_BERR_TRIES":             uint64(RemoteBErrTries),
		"WWREMOTE_BERR_GONE":              uint64(RemoteBErrGone),
		"WWREMOTE_CTR_HOOK_CALLS":         uint64(RemoteCtrHookCalls),
		"WWREMOTE_CTR_SWAPS":              uint64(RemoteCtrSwaps),
		"WWREMOTE_CTR_SWITCHES":           uint64(RemoteCtrSwitches),
		"WWREMOTE_CTR_ATTACKER_OVERRIDES": uint64(RemoteCtrAttackerOverrides),
		"WWREMOTE_CTR_DENIED":             uint64(RemoteCtrDenied),
		"WWREMOTE_CTR_ROUYA_SKIPS":        uint64(RemoteCtrRouyaSkips),
		"WWREMOTE_CTR_PARK_REWRITES":      uint64(RemoteCtrParkRewrites),
		"WWREMOTE_CTR_BOTH_WORD_SWAPS":    uint64(RemoteCtrBothWordSwaps),
		"WWREMOTE_CTR_VT_REFUSALS":        uint64(RemoteCtrVTRefusals),
		"WWREMOTE_CTR_GATE_SKIPS":         uint64(RemoteCtrGateSkips),
		"WWREMOTE_HE_SEQ_OFF":             uint64(RemoteHESeqOff),
		"WWREMOTE_HE_SLOT_OFF":            uint64(RemoteHESlotOff),
		"WWREMOTE_HE_HFLAGS_OFF":          uint64(RemoteHEHFlagsOff),
		"WWREMOTE_HE_PID_OFF":             uint64(RemoteHEPIDOff),
		"WWREMOTE_HE_SETID_OFF":           uint64(RemoteHESetIDOff),
		"WWREMOTE_HE_PROC_OFF":            uint64(RemoteHEProcOff),
		"WWREMOTE_HE_ATTYPE_OFF":          uint64(RemoteHEAtTypeOff),
		"WWREMOTE_HE_ATP_OFF":             uint64(RemoteHEAtpOff),
		"WWREMOTE_HE_SE_OFF":              uint64(RemoteHESeOff),
		"WWREMOTE_HE_HITMARK_OFF":         uint64(RemoteHEHitMarkOff),
		"WWREMOTE_HE_SPL_OFF":             uint64(RemoteHESplOff),
		"WWREMOTE_HE_REL_HIT_OFF":         uint64(RemoteHERelHitOff),
		"WWREMOTE_HE_RVEC_OFF":            uint64(RemoteHERVecOff),
		"WWREMOTE_HE_FRAME_OFF":           uint64(RemoteHEFrameOff),
		"WWREMOTE_HF_SHIELD":              uint64(RemoteHFShield),
		"WWREMOTE_HF_NOT_MO2":             uint64(RemoteHFNotMo2),
		"WWREMOTE_HF_NOT_KEYED":           uint64(RemoteHFNotKeyed),
		"WWREMOTE_KEYED_SETID_MAX":        uint64(RemoteKeyedSetIDMax),
		"WWREMOTE_AR_NONE":                uint64(RemoteARNone),
		"WWREMOTE_AR_DROPPED":             uint64(RemoteARDropped),
		"WWREMOTE_AR_APPLIED":             uint64(RemoteARApplied),
		"WWREMOTE_AR_GUARDED":             uint64(RemoteARGuarded),
		"WWREMOTE_AR_NO_CROSS":            uint64(RemoteARNoCross),
		"WWREMOTE_AR_NO_MIRROR":           uint64(RemoteARNoMirror),
		"WWREMOTE_AR_LINK_ABSENT":         uint64(RemoteARLinkAbsent),
		"WWREMOTE_AR_EARLY_OUT":           uint64(RemoteAREarlyOut),
		"WWREMOTE_AD_NONE":                uint64(RemoteADNone),
		"WWREMOTE_AD_IFRAMES":             uint64(RemoteADIFrames),
		"WWREMOTE_AD_GO_GATE":             uint64(RemoteADGoGate),
		"WWREMOTE_AD_NOT_IN_PLAY":         uint64(RemoteADNotInPlay),
		"WWREMOTE_AD_STALE_EPOCH":         uint64(RemoteADStaleEpoch),
		"WWREMOTE_AD_ABORTED":             uint64(RemoteADAborted),
		"WWREMOTE_HAF_ATTRIB_BODY":        uint64(RemoteHAFAttribBody),
		"WWREMOTE_HAF_SLOT1":              uint64(RemoteHAFSlot1),
		"WWREMOTE_HD_NO_BODY":             uint64(RemoteHDNoBody),
		"WWREMOTE_ACTOR_SIZE":             uint64(RemoteActorSize),
		"WWREMOTE_KEEPER_SWAP_OFF":        uint64(RemoteKeeperSwapOff),
		"WWREMOTE_BODY_PARAMS_TAG":        uint64(RemoteBodyParamsTag),
		"WWREMOTE_BODY_PARAMS_MASK":       uint64(RemoteBodyParamsMask),
		"WWREMOTE_BODY_MAGIC":             uint64(RemoteBodyMagic),
		"WWREMOTE_BODY_CUT_TYPE_OFF":      uint64(RemoteBodyCutTypeOff),
		"WWREMOTE_BODY_CUT_COUNT_OFF":     uint64(RemoteBodyCutCountOff),
		"WWREMOTE_BODY_DAMAGE_WAIT_OFF":   uint64(RemoteBodyDamageWaitOff),
		"WWREMOTE_BODY_GRAB_WEAR_OFF":     uint64(RemoteBodyGrabWearOff),
		"WWREMOTE_BODY_DEMO_OFF":          uint64(RemoteBodyDemoOff),
		"WWREMOTE_BODY_VPTR_OFF":          uint64(RemoteBodyVPtrOff),
		"WWREMOTE_BODY_VT_OFF":            uint64(RemoteBodyVTOff),
		"WWREMOTE_BODY_VT_SIZE":           uint64(RemoteBodyVTSize),
		"WWREMOTE_BODY_CYL_OFF":           uint64(RemoteBodyCylOff),
		"WWREMOTE_BODY_CYL_SIZE":          uint64(RemoteBodyCylSize),
		"WWREMOTE_BODY_STTS_OFF":          uint64(RemoteBodySttsOff),
		"WWREMOTE_BODY_STTS_SIZE":         uint64(RemoteBodySttsSize),
		"WWREMOTE_BODY_MAGIC_OFF":         uint64(RemoteBodyMagicOff),
		"WWREMOTE_BODY_SLOT_OFF":          uint64(RemoteBodySlotOff),
		"WWREMOTE_BODY_GEN_OFF":           uint64(RemoteBodyGenOff),
		"WWREMOTE_BODY_IFRAMES_LEFT_OFF":  uint64(RemoteBodyIFramesLeftOff),
		"WWREMOTE_BODY_STATE_OFF":         uint64(RemoteBodyStateOff),
		"WWREMOTE_BODY_SHIELD_ANGLE_OFF":  uint64(RemoteBodyShieldAngleOff),
		"WWREMOTE_BODY_HAND_MTX_OFF":      uint64(RemoteBodyHandMtxOff),
		"WWREMOTE_BODY_LAST_POSE_SEQ_OFF": uint64(RemoteBodyLastPoseSeqOff),
		"WWREMOTE_BODY_PARK_SINCE_OFF":    uint64(RemoteBodyParkSinceOff),
		"WWREMOTE_BODY_END":               uint64(RemoteBodyEnd),
		"WWREMOTE_COLLECT_ADDR":           uint64(RemoteCollectAddr),
		"WWREMOTE_COLLECT_JAIL_MASK":      uint64(RemoteCollectJailMask),
	})
}

// TestRELHeaderPins: wwrel.h's five words (the three REL words and the
// two remote-Link words T2's exec_shim and the swap read) equal their Go
// constants, and the header defines nothing else.
func TestRELHeaderPins(t *testing.T) {
	pinHeader(t, wwrelHeader, headerDefines(t, wwrelHeader), map[string]uint64{
		"WWREL_MAGIC":          uint64(RELMagic),
		"WWREL_MAGIC_OFF":      uint64(OffRELMagic),
		"WWREL_BUILD_ID_OFF":   uint64(OffRELBuildID),
		"WWREL_FRAME_OFF":      uint64(OffRELFrame),
		"WWREL_EXEC_HOOK_OFF":  uint64(OffRELExecHook),
		"WWREL_SWAP_STATE_OFF": uint64(OffRELSwapState),
	})
	// The two new words follow the frame counter, in the slack past the
	// Mailbox struct, below the enemy block, outside every canary region;
	// T2's exec_shim loads 0x30BC(0x8041 << 16).
	if OffRELExecHook != OffRELFrame+4 || OffRELSwapState != OffRELExecHook+4 || Mailbox+OffRELExecHook != 0x804130BC {
		t.Errorf("REL hook words at +0x%X/+0x%X (0x%08X)", OffRELExecHook, OffRELSwapState, Mailbox+OffRELExecHook)
	}
	if OffRELExecHook < MailboxSize || OffRELSwapState+4 > EnemyOff || RELHookRegion.Size() != relHookWordsLen {
		t.Error("REL hook words not between the Mailbox struct and the enemy block")
	}
	for _, r := range []addr.Region{CanaryTRK, CanaryArena, CanaryArenaHigh} {
		if r.Contains(Mailbox+OffRELExecHook, 1) || r.Contains(Mailbox+OffRELSwapState+3, 1) {
			t.Errorf("%s covers the REL hook words", r.Name)
		}
	}
}

// TestRemoteBlockPlacement: the block lies in the slack past the enemy
// region and below the patched arena, and its layout adds up: each field
// ends where the next starts, the spans and entries tile, and the magics
// spell 'WWRP' and 'WWRB'.
func TestRemoteBlockPlacement(t *testing.T) {
	if RemoteBlockAddr != 0x80413420 || RemoteBlockAddr+RemoteSize != 0x80413750 {
		t.Errorf("remote-Link block at 0x%08X-0x%08X, the design says 0x80413420-0x80413750", RemoteBlockAddr, RemoteBlockAddr+RemoteSize)
	}
	if RemoteOff < EnemyRegionEnd || RemoteRegionStart != EnemyRegionEnd || RemoteRegionEnd != RemoteOff+RemoteSize ||
		Mailbox+RemoteRegionEnd > PatchedArenaLo || RemoteBlockAddr%0x20 != 0 {
		t.Errorf("block +0x%X..+0x%X (region +0x%X..+0x%X) overlaps the enemy region (to +0x%X) or reaches the arena 0x%08X",
			RemoteOff, RemoteOff+RemoteSize, RemoteRegionStart, RemoteRegionEnd, EnemyRegionEnd, PatchedArenaLo)
	}
	if !RemoteBlockRegion.Contains(RemoteBlockAddr, int(RemoteSize)) || RemoteBlockRegion.Contains(Mailbox+EnemyRegionEnd-1, 1) ||
		RemoteBlockRegion.Contains(PatchedArenaLo-1, 1) && RemoteBlockRegion.End == PatchedArenaLo {
		t.Errorf("RemoteBlockRegion %+v", RemoteBlockRegion)
	}
	if string(binary.BigEndian.AppendUint32(nil, RemoteMagic)) != "WWRP" || string(binary.BigEndian.AppendUint32(nil, RemoteBodyMagic)) != "WWRB" {
		t.Errorf("magics 0x%08X / 0x%08X", RemoteMagic, RemoteBodyMagic)
	}
	for _, c := range []struct {
		name      string
		got, want int
	}{
		{"seq_head after magic", RemoteMagicOff + 4, RemoteSeqHeadOff},
		{"layout after seq_head", RemoteSeqHeadOff + 4, RemoteLayoutOff},
		{"flags after layout", RemoteLayoutOff + 2, RemoteFlagsOff},
		{"epoch after flags", RemoteFlagsOff + 2, RemoteEpochOff},
		{"tunables after epoch", RemoteEpochOff + 4, RemoteMarginQ8Off},
		{"role byte", RemoteBodyIFramesOff + 1, RemoteRoleOff},
		{"dy_max after role", RemoteRoleOff + 1, RemoteDYMaxOff},
		{"counts after dy_max", RemoteDYMaxOff + 4, RemotePolicyCountOff},
		{"cooldown after counts", RemoteFFCountOff + 1, RemoteCreateCooldownOff},
		{"reserved after cooldown", RemoteCreateCooldownOff + 2, RemoteReservedOff},
		{"slots after reserved", RemoteReservedOff + 4, RemoteSlotsOff},
		{"slots end", RemoteSlotsOff + RemoteMaxSlots*RemoteSlotSize, RemoteApplyOff},
		{"apply end", RemoteApplyOff + RemoteApplySize, RemoteFFDenyOff},
		{"FF denylist end", RemoteFFDenyOff + RemoteFFDenyMax*RemoteStageLen, RemotePolicyOff},
		{"PROC_POLICY end", RemotePolicyOff + RemoteProcPolicyMax*RemotePolicySize, RemoteSeqTailOff},
		{"seq_tail end", RemoteSeqTailOff + 4, RemoteGoSpan},
		{"unused word", RemoteUnusedOff + 4, RemoteAckSeqOff},
		{"ack_frame", RemoteAckSeqOff + 4, RemoteAckFrameOff},
		{"body_status start", RemoteAckFrameOff + 4, RemoteBodyStatusOff},
		{"body_status end", RemoteBodyStatusOff + RemoteMaxSlots*RemoteBodyStatusSize, RemoteCountersOff},
		{"counters end", RemoteCountersOff + 4*RemoteCounters, RemoteHitRingHeadOff},
		{"ring after head", RemoteHitRingHeadOff + 4, RemoteHitRingOff},
		{"ring end", RemoteHitRingOff + RemoteHitRingLen*RemoteHitSize, RemoteApplyAckOff},
		{"apply_result", RemoteApplyAckOff + 4, RemoteApplyResultOff},
		{"life_before", RemoteApplyResultOff + 4, RemoteLifeBeforeOff},
		{"life_after", RemoteLifeBeforeOff + 2, RemoteLifeAfterOff},
		{"block end", RemoteLifeAfterOff + 2, int(RemoteSize)},
		// slot
		{"slot stage after iframes", RemoteSLIFramesOff + 2, RemoteSLStageOff},
		{"slot pos after stage", RemoteSLStageOff + RemoteStageLen, RemoteSLPosOff},
		{"slot angle after pos", RemoteSLPosOff + 12, RemoteSLAngleOff},
		{"slot shield after angle", RemoteSLAngleOff + 2, RemoteSLShieldAngleOff},
		{"slot eye_dy", RemoteSLShieldAngleOff + 2, RemoteSLEyeDYOff},
		{"slot cyl_r", RemoteSLEyeDYOff + 4, RemoteSLCylROff},
		{"slot cyl_h", RemoteSLCylROff + 4, RemoteSLCylHOff},
		{"slot pose_slot", RemoteSLCylHOff + 4, RemoteSLPoseSlotOff},
		{"slot gen", RemoteSLPoseSlotOff + 1, RemoteSLGenOff},
		{"slot pad", RemoteSLGenOff + 1, RemoteSLPadOff},
		{"slot end", RemoteSLPadOff + 2, RemoteSlotSize},
		// apply record
		{"apply setid", RemoteAPSeqOff + 4, RemoteAPSetIDOff},
		{"apply atp", RemoteAPSetIDOff + 2, RemoteAPAtpOff},
		{"apply flags", RemoteAPAtSPrmOff + 1, RemoteAPFlagsOff},
		{"apply attype", RemoteAPFlagsOff + 1, RemoteAPAtTypeOff},
		{"apply rel_hit", RemoteAPAtTypeOff + 4, RemoteAPRelHitOff},
		{"apply rvec", RemoteAPRelHitOff + 12, RemoteAPRVecOff},
		{"apply end", RemoteAPRVecOff + 12, RemoteApplySize},
		// PROC_POLICY entry, body status, hit entry
		{"policy flags", RemotePPProcOff + 2, RemotePPFlagsOff},
		{"policy end", RemotePPPadOff + 1, RemotePolicySize},
		{"bs state", RemoteBSPIDOff + 4, RemoteBSStateOff},
		{"bs frames_live", RemoteBSLatchedByOff + 1, RemoteBSFramesLiveOff},
		{"bs tries", RemoteBSFramesLiveOff + 2, RemoteBSTriesOff},
		{"bs end", RemoteBSPadOff + 1, RemoteBodyStatusSize},
		{"hit pid", RemoteHEHFlagsOff + 1, RemoteHEPIDOff},
		{"hit setid", RemoteHEPIDOff + 4, RemoteHESetIDOff},
		{"hit attype", RemoteHEProcOff + 2, RemoteHEAtTypeOff},
		{"hit atp", RemoteHEAtTypeOff + 4, RemoteHEAtpOff},
		{"hit rel_hit", RemoteHESplOff + 1, RemoteHERelHitOff},
		{"hit rvec", RemoteHERelHitOff + 12, RemoteHERVecOff},
		{"hit frame", RemoteHERVecOff + 12, RemoteHEFrameOff},
		{"hit end", RemoteHEFrameOff + 4, RemoteHitSize},
	} {
		if c.got != c.want {
			t.Errorf("%s: 0x%X, want 0x%X", c.name, c.got, c.want)
		}
	}
	// Absolute addresses the REL and the Go host use (design §6.2).
	if RemoteSlotAddr(0, 0) != 0x80413440 || RemoteSlotAddr(3, RemoteSLPadOff)+2 != RemoteBlockAddr+RemoteApplyOff ||
		RemoteBlockAddr+RemoteCountersOff != 0x80413658 || RemoteBlockAddr+RemoteHitRingOff != 0x80413684 {
		t.Errorf("slot 0 0x%08X, counters 0x%08X, ring 0x%08X", RemoteSlotAddr(0, 0), RemoteBlockAddr+RemoteCountersOff, RemoteBlockAddr+RemoteHitRingOff)
	}
}

// TestRemoteContractValues: the values the two sides and the enemy block
// share agree, the ATTRIB extensions do not collide with the enemy
// block's own bits and details, the flags are distinct single bits, and
// the lite body fits the actor.
func TestRemoteContractValues(t *testing.T) {
	if RemoteRoleHost != EnemyRoleHost || RemoteRoleClient != EnemyRoleClient || RemoteStaleFrames != EnemyStaleFrames || RemoteStageLen != EnemyStageLen {
		t.Error("the remote-Link block's role, stale or stage rules differ from the enemy block's")
	}
	if RemoteCollectAddr != addr.Collect || RemoteCollectJailMask != 0x0F {
		t.Errorf("collect byte 0x%08X mask 0x%02X, want addr.Collect 0x%08X & 0x0F", RemoteCollectAddr, RemoteCollectJailMask, addr.Collect)
	}
	if RemoteMaxSlots < MaxRemoteLinks || RemoteMaxSlots < MailboxPoseSlotCap {
		t.Errorf("%d layout slots for %d remote Links / %d pose slots", RemoteMaxSlots, MaxRemoteLinks, MailboxPoseSlotCap)
	}
	if RemoteHAFAttribBody&(EnemyHAFForceV1|EnemyHAFFallbackShrink|EnemyHAFNoHitMark) != 0 ||
		RemoteHAFSlot1&(EnemyHAFForceV1|EnemyHAFFallbackShrink|EnemyHAFNoHitMark|RemoteHAFAttribBody) != 0 || RemoteHDNoBody <= EnemyHDDemo {
		t.Error("the ATTRIB hit_apply bits or HD_NO_BODY collide with the enemy block's")
	}
	var seen uint16
	for _, f := range []uint16{RemoteFlagBodies, RemoteFlagSwap, RemoteFlagHittable, RemoteFlagDetect, RemoteFlagApply, RemoteFlagAttrib,
		RemoteFlagNameA9, RemoteFlagGuard, RemoteFlagFFGateOff, RemoteFlagRouyaGateOff, RemoteFlagAttacker, RemoteFlagGroupWide,
		RemoteFlagBothWordsEnable, RemoteFlagIFrameStamp} {
		if f == 0 || f&(f-1) != 0 || seen&f != 0 {
			t.Errorf("flag 0x%04X is not a fresh single bit", f)
		}
		seen |= f
	}
	if seen != 0x3FFF {
		t.Errorf("flags cover 0x%04X, design §6.3 has 0x0001..0x2000", seen)
	}
	if RemoteKeeperSwapOff != 0x458 || RemoteActorSize != 0x600 || RemoteBodyEnd > RemoteActorSize ||
		RemoteBodyVTOff+RemoteBodyVTSize > RemoteBodyCylOff || RemoteBodyCylOff+RemoteBodyCylSize != RemoteBodySttsOff ||
		RemoteBodySttsOff+RemoteBodySttsSize > RemoteBodyMagicOff || RemoteBodyHandMtxOff+0x30 != RemoteBodyLastPoseSeqOff ||
		RemoteBodyParkSinceOff+4 != RemoteBodyEnd || RemoteBodyVPtrOff+4 != RemoteBodyVTOff || RemoteBodyHandMtxOff%4 != 0 {
		t.Error("the lite body's layout does not fit WWREMOTE_ACTOR_SIZE or its fields overlap")
	}
	if RemoteBodyParamsTag&^RemoteBodyParamsMask != 0 || RemoteBodyParamsTag == 0 {
		t.Error("the body params tag is not in its mask (the keeper is params 0)")
	}
	if RemoteARApplied != 2 || RemoteAREarlyOut != 7 || RemoteADStaleEpoch != 4 || RemoteHFNotKeyed != 4 || RemoteKeyedSetIDMax != 0x200 {
		t.Error("apply result/detail or hit flag values moved (design §7.1, §8.3)")
	}
	if len(RemoteCounterNames) != RemoteCounters || RemoteCounterNames[RemoteCtrHookCalls] != "hook_calls" || RemoteCounterNames[RemoteCtrGateSkips] != "gate_skips" {
		t.Error("RemoteCounterNames does not follow the RemoteCtr* indices")
	}
}

func sampleRemoteBlock() *RemoteBlock {
	b := &RemoteBlock{
		Magic: RemoteMagic, Seq: 0x0A0B0C0D, Layout: RemoteLayout, Flags: RemoteFlagBodies | RemoteFlagSwap | RemoteFlagIFrameStamp,
		Epoch: 7, MarginQ8: RemoteDefMarginQ8, MinHold: RemoteDefMinHold, BodyIFrames: RemoteDefBodyIFrames, Role: RemoteRoleHost,
		DYMax: RemoteDefDYMax, CreateCooldown: RemoteDefCreateCooldown,
		Apply: RemoteApply{Seq: 3, SetID: 0x12, Atp: 2, Se: 3, HitMark: 0x0D, Spl: 6, AtSPrm: RemoteApplyAtSPrm, Flags: RemoteAFHostShield,
			AtType: 0x800, RelHit: [3]float32{1, 50, -2}, RVec: [3]float32{0, 0, -30}},
		FFDeny: []string{"Siren", "sea_T"},
		Policy: []RemotePolicy{{Proc: int16(RemoteProcMo2), Flags: RemotePFAllow}, {Proc: 0x1D5, Flags: RemotePFDeny | RemotePFNoLatch}},
	}
	b.Slots[0] = RemoteSlot{Active: true, SFlags: RemoteSFTargetable | RemoteSFGuard, Room: 1, CutType: 5, CutCount: 2, PlayerID: 2,
		IFrames: 12, Stage: "M_Dai", Pos: [3]float32{-1500.5, 200, 3e3}, AngleY: -0x4000, ShieldAngleY: 0x1234,
		EyeDY: RemoteDefEyeDY, CylR: RemoteDefCylR, CylH: RemoteDefCylH, PoseSlot: 1, Gen: 9}
	b.Slots[2] = RemoteSlot{Room: -1, Stage: "sea", Pos: [3]float32{4, 5, 6}}
	return b
}

// TestRemoteBlockEncode: Encode is RemoteGoSpan bytes with seq_head ==
// seq_tail, every field at its design offset, DecodeRemoteBlock reads it
// back, and the encoder refuses NaN/Inf, a long stage, too many FF names
// or policies.
func TestRemoteBlockEncode(t *testing.T) {
	b := sampleRemoteBlock()
	out, err := b.Encode()
	if err != nil {
		t.Fatal(err)
	}
	be := binary.BigEndian
	// Literal design §6.2 offsets, not the constants.
	if len(out) != 0x1EC || be.Uint32(out[0x000:]) != RemoteMagic || be.Uint32(out[0x004:]) != b.Seq || be.Uint32(out[0x1E8:]) != b.Seq ||
		be.Uint16(out[0x008:]) != 1 || be.Uint16(out[0x00A:]) != 0x2003 || be.Uint32(out[0x00C:]) != 7 ||
		out[0x010] != 164 || out[0x011] != 30 || out[0x012] != 30 || out[0x013] != 1 || math.Float32frombits(be.Uint32(out[0x014:])) != 400 ||
		out[0x018] != 2 || out[0x019] != 2 || be.Uint16(out[0x01A:]) != 30 || be.Uint32(out[0x01C:]) != 0 {
		t.Errorf("header %x", out[:0x20])
	}
	s0 := out[0x020:]
	if s0[0x00] != 1 || s0[0x01] != 0x03 || s0[0x02] != 1 || s0[0x03] != 5 || s0[0x04] != 2 || s0[0x05] != 2 || be.Uint16(s0[0x06:]) != 12 ||
		!bytes.Equal(s0[0x08:0x10], []byte("M_Dai\x00\x00\x00")) || math.Float32frombits(be.Uint32(s0[0x10:])) != -1500.5 ||
		math.Float32frombits(be.Uint32(s0[0x18:])) != 3e3 || int16(be.Uint16(s0[0x1C:])) != -0x4000 || be.Uint16(s0[0x1E:]) != 0x1234 ||
		math.Float32frombits(be.Uint32(s0[0x20:])) != 110 || math.Float32frombits(be.Uint32(s0[0x24:])) != 30 ||
		math.Float32frombits(be.Uint32(s0[0x28:])) != 120 || s0[0x2C] != 1 || s0[0x2D] != 9 || s0[0x2E] != 0 || s0[0x2F] != 0 {
		t.Errorf("slot 0 %x", s0[:0x30])
	}
	if s2 := out[0x020+2*0x30:]; s2[0x00] != 0 || int8(s2[0x02]) != -1 || string(s2[0x08:0x0B]) != "sea" || s2[0x0B] != 0 {
		t.Errorf("slot 2 %x", s2[:0x30])
	}
	ap := out[0x0E0:]
	if be.Uint32(ap[0x00:]) != 3 || be.Uint16(ap[0x04:]) != 0x12 || ap[0x06] != 2 || ap[0x07] != 3 || ap[0x08] != 0x0D || ap[0x09] != 6 ||
		ap[0x0A] != 0x05 || ap[0x0B] != 0x01 || be.Uint32(ap[0x0C:]) != 0x800 || math.Float32frombits(be.Uint32(ap[0x14:])) != 50 ||
		math.Float32frombits(be.Uint32(ap[0x24:])) != -30 {
		t.Errorf("apply %x", ap[:0x28])
	}
	if !bytes.Equal(out[0x108:0x118], []byte("Siren\x00\x00\x00sea_T\x00\x00\x00")) || !bytes.Equal(out[0x118:0x128], make([]byte, 16)) {
		t.Errorf("FF denylist %x", out[0x108:0x128])
	}
	if !bytes.Equal(out[0x128:0x130], []byte{0x00, 0xBD, 0x02, 0x00, 0x01, 0xD5, 0x09, 0x00}) || !bytes.Equal(out[0x130:0x1E8], make([]byte, 0x1E8-0x130)) {
		t.Errorf("PROC_POLICY %x", out[0x128:0x138])
	}
	if got := hex.EncodeToString(out); got != remoteGoSpanGolden {
		t.Errorf("the Go span's bytes changed:\n got %s\nwant %s", got, remoteGoSpanGolden)
	}
	got, tail, err := DecodeRemoteBlock(out)
	if err != nil || tail != b.Seq || !reflect.DeepEqual(&got, b) {
		t.Errorf("decode: tail %08X, %v\n got %+v\nwant %+v", tail, err, got, *b)
	}

	for name, mod := range map[string]func(*RemoteBlock){
		"NaN pos":      func(b *RemoteBlock) { b.Slots[3].Pos[1] = float32(math.NaN()) },
		"Inf cyl":      func(b *RemoteBlock) { b.Slots[0].CylH = float32(math.Inf(-1)) },
		"NaN dy_max":   func(b *RemoteBlock) { b.DYMax = float32(math.NaN()) },
		"Inf rvec":     func(b *RemoteBlock) { b.Apply.RVec[2] = float32(math.Inf(1)) },
		"9-byte stage": func(b *RemoteBlock) { b.Slots[1].Stage = "123456789" },
		"9-byte FF":    func(b *RemoteBlock) { b.FFDeny[1] = "123456789" },
		"5 FF names":   func(b *RemoteBlock) { b.FFDeny = make([]string, 5) },
		"49 policies":  func(b *RemoteBlock) { b.Policy = make([]RemotePolicy, 49) },
	} {
		b := sampleRemoteBlock()
		mod(b)
		_, err := b.Encode()
		if err == nil {
			t.Errorf("%s encoded", name)
		}
		if name[:3] == "NaN" || name[:3] == "Inf" {
			if !errors.Is(err, ErrRemoteNotFinite) {
				t.Errorf("%s: %v", name, err)
			}
		}
	}
	full := sampleRemoteBlock()
	full.FFDeny = []string{"a", "b", "c", "12345678"}
	full.Policy = make([]RemotePolicy, RemoteProcPolicyMax)
	full.Policy[47] = RemotePolicy{Proc: -2, Flags: RemotePFBothWords}
	out, err = full.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if be.Uint16(out[0x1E4:]) != 0xFFFE || out[0x1E6] != RemotePFBothWords || be.Uint32(out[0x1E8:]) != full.Seq {
		t.Errorf("the 48th policy runs into seq_tail: %x", out[0x1E0:])
	}
	if got, _, err := DecodeRemoteBlock(out); err != nil || !reflect.DeepEqual(&got, full) {
		t.Errorf("full decode %+v %v", got, err)
	}
	out[0x018], out[0x019] = 200, 9 // garbled counts clamp
	if got, _, err := DecodeRemoteBlock(out); err != nil || len(got.Policy) != RemoteProcPolicyMax || len(got.FFDeny) != RemoteFFDenyMax {
		t.Errorf("garbled counts: %d policies, %d FF, %v", len(got.Policy), len(got.FFDeny), err)
	}
	if _, _, err := DecodeRemoteBlock(out[:RemoteGoSpan-1]); err == nil {
		t.Error("a short block decoded")
	}
}

func sampleRemoteStatus() *RemoteStatus {
	st := &RemoteStatus{AckSeq: 0x0A0B0C0D, AckFrame: 4242, HitRingHead: 6, ApplyAck: 3,
		ApplyResult: RemoteApplyResult{Result: RemoteARGuarded, Detail: RemoteADNone, Frames: 2, Sets: 1}, LifeBefore: 12, LifeAfter: 12}
	st.Bodies[1] = RemoteBodyStatus{Actor: 0x80B12340, PID: 0x77, State: RemoteBodyLive, Err: RemoteBErrNone, Hits: 3, LatchedBy: 2, FramesLive: 900, Tries: 1}
	st.Bodies[3] = RemoteBodyStatus{State: RemoteBodyEmpty, Err: RemoteBErrVtable, Tries: 3}
	for i := range st.Counters {
		st.Counters[i] = uint32(0x100*i + 1)
	}
	for n := uint32(3); n <= 6; n++ {
		st.Hits[(n-1)%RemoteHitRingLen] = RemoteHit{Seq: uint16(n), Slot: 1, HFlags: RemoteHFShield, PID: 0x50 + n, SetID: 0x10, Proc: int16(RemoteProcMo2),
			AtType: 0x800, Atp: 4, Se: 3, HitMark: 0x0D, Spl: 7, RelHit: [3]float32{0, float32(n), 0}, RVec: [3]float32{-30, 0, 0}, Frame: 1000 + n}
	}
	return st
}

// TestRemoteStatusRoundTrip: the REL span encodes at its design offsets
// (literal), decodes back, and reads through ReadRemoteStatus and
// ReadRemoteBlock; HitsSince follows the ring rule.
func TestRemoteStatusRoundTrip(t *testing.T) {
	st := sampleRemoteStatus()
	b := st.Encode()
	be := binary.BigEndian
	blk := make([]byte, 0x330)
	copy(blk[0x1F0:], b)
	if len(b) != 0x140 || be.Uint32(blk[0x1F0:]) != 0x0A0B0C0D || be.Uint32(blk[0x1F4:]) != 4242 ||
		be.Uint32(blk[0x1F8+0x10:]) != 0x80B12340 || blk[0x1F8+0x10+0x08] != RemoteBodyLive || blk[0x1F8+0x10+0x0B] != 2 ||
		be.Uint16(blk[0x1F8+0x10+0x0C:]) != 900 || blk[0x1F8+0x30+0x09] != RemoteBErrVtable || blk[0x1F8+0x30+0x0E] != 3 ||
		be.Uint32(blk[0x238:]) != 1 || be.Uint32(blk[0x238+4*9:]) != 0x901 || be.Uint32(blk[0x260:]) != 6 ||
		be.Uint32(blk[0x324:]) != 3 || !bytes.Equal(blk[0x328:0x32C], []byte{RemoteARGuarded, 0, 2, 1}) || be.Uint16(blk[0x32C:]) != 12 || be.Uint16(blk[0x32E:]) != 12 {
		t.Errorf("REL span at the wrong offsets: %x", blk[0x1F0:])
	}
	// Push 5 lives in entry (5-1)%4 = 0.
	if e := blk[0x264:]; be.Uint16(e[0x00:]) != 5 || e[0x02] != 1 || e[0x03] != RemoteHFShield || be.Uint32(e[0x04:]) != 0x55 ||
		be.Uint16(e[0x08:]) != 0x10 || be.Uint16(e[0x0A:]) != 0xBD || be.Uint32(e[0x0C:]) != 0x800 || e[0x10] != 4 || e[0x11] != 3 ||
		e[0x12] != 0x0D || e[0x13] != 7 || math.Float32frombits(be.Uint32(e[0x18:])) != 5 || math.Float32frombits(be.Uint32(e[0x20:])) != -30 ||
		be.Uint32(e[0x2C:]) != 1005 {
		t.Errorf("hit entry 0 %x", e[:0x30])
	}
	got, err := DecodeRemoteStatus(b)
	if err != nil || !reflect.DeepEqual(&got, st) {
		t.Errorf("decode %v\n got %+v\nwant %+v", err, got, *st)
	}
	if _, err := DecodeRemoteStatus(b[:len(b)-1]); err == nil {
		t.Error("a short status decoded")
	}

	m := &fakeWords{}
	if err := WriteRemoteBlock(m, sampleRemoteBlock()); err != nil {
		t.Fatal(err)
	}
	if err := m.Write(RemoteBlockAddr+RemoteAckSeqOff, b); err != nil {
		t.Fatal(err)
	}
	if rs, err := ReadRemoteStatus(m); err != nil || !reflect.DeepEqual(&rs, st) {
		t.Errorf("ReadRemoteStatus %+v %v", rs, err)
	}
	rb, tail, rs, err := ReadRemoteBlock(m)
	if err != nil || tail != 0x0A0B0C0D || !reflect.DeepEqual(&rb, sampleRemoteBlock()) || !reflect.DeepEqual(&rs, st) {
		t.Errorf("ReadRemoteBlock %+v tail %08X %+v %v", rb, tail, rs, err)
	}
	if err := ClearRemoteBlock(m); err != nil {
		t.Fatal(err)
	}
	if rb, tail, rs2, err := ReadRemoteBlock(m); err != nil || rb.Magic != 0 || rb.Seq != 0x0A0B0C0D || tail != 0x0A0B0C0D || !reflect.DeepEqual(rs2, rs) {
		t.Errorf("after clear: %+v tail %08X %v", rb, tail, err)
	}
	// The Go write never touches the unused word or the REL span.
	if w := m.b[RemoteBlockAddr+RemoteUnusedOff]; w != 0 {
		t.Errorf("unused word written: %x", w)
	}
}

// TestRemoteHitsSince: pushes after last, oldest first, at most a ring,
// and only entries whose seq matches their push number.
func TestRemoteHitsSince(t *testing.T) {
	st := sampleRemoteStatus() // pushes 3..6 live, head 6
	seqs := func(hs []RemoteHit) (out []uint16) {
		for _, h := range hs {
			out = append(out, h.Seq)
		}
		return out
	}
	for _, c := range []struct {
		last uint32
		want []uint16
	}{
		{6, nil}, {9, nil}, {5, []uint16{6}}, {3, []uint16{4, 5, 6}}, {2, []uint16{3, 4, 5, 6}}, {0, []uint16{3, 4, 5, 6}},
	} {
		if got := seqs(st.HitsSince(c.last)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("HitsSince(%d) = %v, want %v", c.last, got, c.want)
		}
	}
	st.Hits[(5-1)%RemoteHitRingLen].Seq = 9 // overwritten mid-read: dropped, not misattributed
	if got := seqs(st.HitsSince(2)); !reflect.DeepEqual(got, []uint16{3, 4, 6}) {
		t.Errorf("torn ring: %v", got)
	}
	if got := (&RemoteStatus{}).HitsSince(0); got != nil {
		t.Errorf("empty ring: %v", got)
	}
}

// TestReadRELHook: the two REL hook words read back from their addresses.
func TestReadRELHook(t *testing.T) {
	m := &fakeWords{}
	if h, err := ReadRELHook(m); err != nil || h != (RELHook{}) {
		t.Fatalf("zero words: %+v %v", h, err)
	}
	if err := m.Write(0x804130BC, []byte{0x81, 0x23, 0x45, 0x60, 0x80, 0xB0, 0x14, 0x58}); err != nil {
		t.Fatal(err)
	}
	if h, err := ReadRELHook(m); err != nil || h != (RELHook{ExecHook: 0x81234560, SwapState: 0x80B01458}) {
		t.Errorf("hook words %+v %v", h, err)
	}
}

// remoteGoSpanGolden is sampleRemoteBlock's Encode, byte for byte: the
// layout-1 Go span (0x1EC bytes) the REL reads. A change here is a layout
// change and needs WWREMOTE_LAYOUT bumped on both sides.
const remoteGoSpanGolden = "" +
	"575752500a0b0c0d0001200300000007a41e1e0143c800000202001e00000000" +
	"010301050202000c4d5f446169000000c4bb900043480000453b8000c0001234" +
	"42dc000041f0000042f000000109000000000000000000000000000000000000" +
	"0000000000000000000000000000000000000000000000000000000000000000" +
	"0000ff000000000073656100000000004080000040a0000040c0000000000000" +
	"0000000000000000000000000000000000000000000000000000000000000000" +
	"0000000000000000000000000000000000000000000000000000000000000000" +
	"00000003001202030d060501000008003f80000042480000c000000000000000" +
	"00000000c1f00000536972656e0000007365615f540000000000000000000000" +
	"000000000000000000bd020001d5090000000000000000000000000000000000" +
	"0000000000000000000000000000000000000000000000000000000000000000" +
	"0000000000000000000000000000000000000000000000000000000000000000" +
	"0000000000000000000000000000000000000000000000000000000000000000" +
	"0000000000000000000000000000000000000000000000000000000000000000" +
	"0000000000000000000000000000000000000000000000000000000000000000" +
	"00000000000000000a0b0c0d"
