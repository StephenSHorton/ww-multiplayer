package enemysync

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/mailboxgen"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// wwenemyKindsHeader is the C copy of the kind table, which the REL
// builds EM_KINDS from (inject/include/wwenemy_kinds.h).
const wwenemyKindsHeader = "../../inject/include/wwenemy_kinds.h"

func kindsHeader(t *testing.T) ([]byte, map[string]int64) {
	t.Helper()
	src, err := os.ReadFile(filepath.FromSlash(wwenemyKindsHeader))
	if err != nil {
		t.Fatal(err)
	}
	ds, err := mailboxgen.ParseDefines("wwenemy_kinds.h", src)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, d := range ds {
		out[d.Name] = d.Value
	}
	return src, out
}

// emkRowFields are the fields every row (MO2, BK, CC, KI, AM, AM2)
// defines: the em_kind struct's (inject/rel/enemy_mirror.h), one
// EMK_<KIND>_<FIELD> each, 0 for "none".
var emkRowFields = []string{"ID", "PROC", "CLASS_SIZE", "BCK_FIRST", "BCK_LAST", "BCK_WAIT", "MORF", "GATE", "DR", "DR_MODE", "DR_ACTION",
	"TILT", "CO_CYL", "TG_CYL", "HEAD_SPH", "DEF_SPH", "HEAD_CENTER", "DEF_CENTER", "CO_FORWARD", "TG_HIT_UP", "INVULN", "INVULN_WAIT",
	"ICE", "WEAPON", "BARE", "BLUR_ON", "BLUR_SHAPE", "BLUR_SCALE", "ANM_GATE", "PLAY_SKIP", "SPH_SRC", "CAPS", "MTX", "COLL", "ATTACH", "EYE", "ATTN_F2"}

// TestKindTablePins: wwenemy_kinds.h and Go agree. Every row has
// every em_kind field; each row's proc, archive, BCK range, morf, ice and
// gate equal the Go Kinds row; the class offsets equal wwapi/addr's; the
// per-kind flags2 bits equal the Go samplers'; the generic SF2 bits equal
// the block's; and the PKey test vector is the one TestPKey checks.
func TestKindTablePins(t *testing.T) {
	src, defs := kindsHeader(t)
	prefix := map[KindID]string{KindMO2: "EMK_MO2_", KindBK: "EMK_BK_", KindCC: "EMK_CC_", KindKI: "EMK_KI_", KindAM: "EMK_AM_", KindAM2: "EMK_AM2_"}
	for id, p := range prefix {
		for _, f := range emkRowFields {
			if _, ok := defs[p+f]; !ok {
				t.Errorf("wwenemy_kinds.h has no %s%s", p, f)
			}
		}
		k := id.Row()
		for name, want := range map[string]int64{
			"ID": int64(k.ID), "PROC": int64(k.Proc), "BCK_FIRST": int64(k.BckFirst), "BCK_LAST": int64(k.BckLast), "BCK_WAIT": int64(k.BckWait),
			"MORF": int64(k.MorfOff), "ICE": int64(k.IceOff), "GATE": int64(k.GateOff),
		} {
			if got, ok := defs[p+name]; ok && got != want {
				t.Errorf("%s%s = 0x%X, the Go Kinds row has 0x%X", p, name, got, want)
			}
		}
		m := regexp.MustCompile(`(?m)^#define\s+` + p + `ARC\s+"(\w+)"`).FindSubmatch(src)
		if m == nil || string(m[1]) != k.Arc {
			t.Errorf("%sARC is %q, the Go row's archive %q", p, m, k.Arc)
		}
	}
	pins := map[string]int64{
		// kind ids and the masks they make
		"EMK_KIND_MO2": int64(KindMO2), "EMK_KIND_BK": int64(KindBK), "EMK_KIND_CC": int64(KindCC), "EMK_KIND_NONE": int64(KindNone),
		"EMK_KIND_KI": int64(KindKI), "EMK_KIND_AM": int64(KindAM), "EMK_KIND_AM2": int64(KindAM2),
		// the hit collider codes
		"EMK_HIT_HEAD": int64(HitHead), "EMK_HIT_TG": int64(HitTg), "EMK_HIT_SPECIAL": int64(HitSpecial),
		"EMK_KIND_COUNT": int64(len(Kinds)),
		// the placement key
		"EMK_PKEY_LEN": PKeyLen, "EMK_PKEY_FNV_OFFSET": int64(fnvOffset), "EMK_PKEY_FNV_PRIME": int64(fnvPrime),
		"EMK_PKEY_TV_PROC": int64(pkeyTVProc), "EMK_PKEY_TV_SETID": int64(pkeyTVSetID), "EMK_PKEY_TV_PARAMS": int64(pkeyTVParams),
		"EMK_PKEY_TV_ROOM": int64(pkeyTVRoom), "EMK_PKEY_TV_HOME_X": int64(pkeyTVHome[0]), "EMK_PKEY_TV_HOME_Y": int64(pkeyTVHome[1]),
		"EMK_PKEY_TV_HOME_Z": int64(pkeyTVHome[2]), "EMK_PKEY_TV_OUT": int64(pkeyTVOut),
		"EMK_PKEY_TV2_PROC": int64(pkeyTV2Proc), "EMK_PKEY_TV2_SETID": int64(pkeyTV2SetID), "EMK_PKEY_TV2_PARAMS": int64(pkeyTV2Params),
		"EMK_PKEY_TV2_ROOM": int64(pkeyTV2Room), "EMK_PKEY_TV2_OUT": int64(pkeyTV2Out),
		// generic fields
		"EMK_AC_PARAMS": addr.ActorOffParameters, "EMK_AC_HOME_POS": addr.ActorOffHomePos, "EMK_AC_HOME_ROOM": addr.ActorOffHomeRoomNo, "EMK_ICE_SHRINK": addr.EnemyIceOffTimer, "EMK_ICE_FREEZE": addr.EnemyIceOffFreezeTimer,
		"EMK_AC_STATUS": addr.ActorOffStatus, "EMK_AC_STATUS_CARRY": addr.ActorStatusCarry, "EMK_AC_ANGLE_Y": addr.ActorOffCurAngleY,
		"EMK_AC_SHAPE_X": addr.ActorOffShapeAngle, "EMK_AC_SHAPE_Y": addr.ActorOffShapeAngleY, "EMK_AC_SHAPE_Z": addr.ActorOffShapeAngle + 4,
		"EMK_AC_ATTN_FLAGS": addr.ActorOffAttnFlags, "EMK_ATTN_LOCKON_BATTLE": addr.AttnLockonBattle, "EMK_AC_HEALTH": addr.ActorOffHealth,
		"EMK_AC_EYE_POS": addr.ActorOffEyePos,
		// MO2: the legacy Moblin offsets
		"EMK_MO2_DR": addr.Mo2OffDR, "EMK_MO2_DR_MODE": addr.Mo2OffDRMode, "EMK_MO2_DR_ACTION": addr.Mo2OffDRAction, "EMK_MO2_TILT": addr.Mo2OffTilt,
		"EMK_MO2_CO_CYL": addr.Mo2OffCoCyl, "EMK_MO2_TG_CYL": addr.Mo2OffTgCyl, "EMK_MO2_HEAD_SPH": addr.Mo2OffHeadSph, "EMK_MO2_DEF_SPH": addr.Mo2OffDefenseSph,
		"EMK_MO2_HEAD_CENTER": addr.Mo2OffHeadCenter, "EMK_MO2_DEF_CENTER": addr.Mo2OffShieldCenter, "EMK_MO2_INVULN": addr.Mo2OffInvuln,
		"EMK_MO2_WEAPON": addr.Mo2OffHasWeapon, "EMK_MO2_BARE": addr.Mo2OffBareHanded, "EMK_MO2_BLUR_ON": addr.Mo2OffBlurOn,
		"EMK_MO2_BLUR_SHAPE": addr.Mo2OffBlurShape, "EMK_MO2_BLUR_SCALE": addr.Mo2OffBlurScale, "EMK_MO2_ANM_GATE": addr.Mo2OffAnmGate,
		"EMK_MO2_PLAY_SKIP": addr.Mo2OffPlaySkip, "EMK_MO2_SPH_SRC": addr.Mo2OffHeadSph,
		// BK
		"EMK_BK_CLASS_SIZE": addr.BkClassSize, "EMK_BK_DR": addr.BkOffDR, "EMK_BK_DR_MODE": addr.BkOffDRMode, "EMK_BK_DR_ACTION": addr.BkOffDRAction,
		"EMK_BK_TILT": addr.BkOffTilt, "EMK_BK_TILT_Z": addr.BkOffTiltZ, "EMK_BK_CO_CYL": addr.BkOffCoCyl, "EMK_BK_TG_CYL": addr.BkOffTgCyl,
		"EMK_BK_HEAD_SPH": addr.BkOffHeadSph, "EMK_BK_DEF_SPH": addr.BkOffShieldSph, "EMK_BK_HEAD_CENTER": addr.BkOffHeadCenter,
		"EMK_BK_DEF_CENTER": addr.BkOffShieldCenter, "EMK_BK_INVULN": addr.BkOffInvuln, "EMK_BK_WEAPON": addr.BkOffWeaponHold,
		"EMK_BK_BLUR_ON": addr.BkOffBlurOn, "EMK_BK_BLUR_SHAPE": addr.BkOffBlurShape, "EMK_BK_BLUR_SCALE": addr.BkOffBlurScale,
		"EMK_BK_PLAY_SKIP": addr.BkOffPlaySkip, "EMK_BK_SPH_SRC": addr.BkOffHeadSph, "EMK_BK_DRAW_GATE": addr.BkOffDrawGate,
		"EMK_BK_SHIELD_MODEL": addr.BkOffShieldModel, "EMK_BK_SHIELD_GATE": addr.BkOffShieldGate, "EMK_BK_BOUEN_MODEL": addr.BkOffBouenModel,
		"EMK_BK_BOUEN_GATE": addr.BkOffBouenGate, "EMK_BK_HIDE2": addr.BkOffHide2, "EMK_BK_JAW": addr.BkOffJaw, "EMK_BK_BOKO_PID": addr.BkOffBokoPID,
		"EMK_BK_BOKO_ATTACH": addr.BkOffBokoAttach, "EMK_BK_DELETE_REQ": addr.BkOffDeleteReq, "EMK_BK_DEMO_GATE": addr.BkOffDemoGate,
		"EMK_BK_F2_HAS_WEAPON": int64(BkF2HasWeapon), "EMK_BK_F2_PLAY_SKIP": int64(BkF2PlaySkip), "EMK_BK_F2_BLUR_ON": int64(BkF2BlurOn),
		"EMK_BK_F2_ATTN": int64(BkF2Attn), "EMK_BOKO_PROC": addr.ProcBoko,
		// CC
		"EMK_CC_CLASS_SIZE": addr.CcClassSize, "EMK_CC_TILT": addr.CcOffGroundTilt, "EMK_CC_TILT_Z": addr.CcOffGroundTiltZ,
		"EMK_CC_CO_CYL": addr.CcOffCyl, "EMK_CC_TG_CYL": addr.CcOffCyl, "EMK_CC_BLUR_SCALE": addr.CcOffYOffset, "EMK_CC_COLOR": addr.CcOffColor,
		"EMK_CC_ACTION": addr.CcOffAction, "EMK_CC_SUBSTATE": addr.CcOffSubstate, "EMK_CC_NO_SHADOW": addr.CcOffNoShadowBG,
		"EMK_CC_STONE": addr.CcOffStone, "EMK_CC_SPLIT": addr.CcOffSplit, "EMK_CC_INSTA_KILL": addr.CcOffInstaKill, "EMK_CC_WOBBLE": addr.CcOffWobble,
		"EMK_CC_SQUASH_PHASE": addr.CcOffSquashPhase, "EMK_CC_BCK_INDEX": addr.CcOffBckIndex, "EMK_CC_SCALE": addr.CcOffScale,
		"EMK_CC_CYL_AT_SPRM": addr.CcCylOffAtSPrm, "EMK_CC_CYL_TG_SPRM": addr.CcCylOffTgSPrm, "EMK_CC_CYL_CO_SPRM": addr.CcCylOffCoSPrm,
		"EMK_CC_F2_TG": int64(CcF2Tg), "EMK_CC_F2_CO": int64(CcF2Co), "EMK_CC_F2_SQUASH": int64(CcF2Squash), "EMK_CC_F2_LOW_EYE": int64(CcF2LowEye),
		"EMK_CC_F2_NO_SHADOW": int64(CcF2NoShadow), "EMK_CC_F2_ATTN": int64(CcF2Attn),
		// the attention bit of each row (D4)
		"EMK_MO2_ATTN_F2": 0, "EMK_BK_ATTN_F2": int64(BkF2Attn), "EMK_CC_ATTN_F2": int64(CcF2Attn),
		"EMK_KI_ATTN_F2": int64(KiF2Attn), "EMK_AM_ATTN_F2": int64(AmF2Attn), "EMK_AM2_ATTN_F2": int64(Am2F2Attn),
		// KI (allow design type_table KI; wwapi/addr/ki.go)
		"EMK_KI_CLASS_SIZE": addr.KiClassSize, "EMK_KI_CO_CYL": addr.KiOffCoSph, "EMK_KI_TG_CYL": addr.KiOffTgSph, "EMK_KI_SPH_SRC": addr.KiOffTgSph,
		"EMK_KI_INVULN": addr.KiOffInvuln, "EMK_KI_AT_SPH": addr.KiOffAtSph, "EMK_KI_PARAMS": addr.KiOffParams, "EMK_KI_TYPE_FLY": KiTypeFly,
		"EMK_KI_BEHAVIOR": addr.KiOffBehavior, "EMK_KI_DRAW_GATE": addr.KiOffDrawGate, "EMK_KI_SHRINK_DELETE": addr.KiOffShrinkDelete,
		"EMK_KI_HEAD_BANK": addr.KiOffHeadBank, "EMK_KI_AUX2": addr.KiOffAux1, "EMK_KI_AUX3": addr.KiOffAux2, "EMK_KI_M338": addr.KiOffM338,
		"EMK_KI_SHADOW": addr.KiOffShadow, "EMK_KI_DMG_TYPE": addr.KiOffDmgType, "EMK_KI_BTK": addr.KiOffBtk,
		"EMK_KI_ACT_WAIT": addr.KiActWait, "EMK_KI_ACT_FIRE_SET": addr.KiActFireSet, "EMK_KI_ACT_ATTACK": addr.KiActAttack,
		"EMK_KI_ACT_ATTACK_INDEX": addr.KiActAttackIndex, "EMK_KI_ACT_FAIL": addr.KiActFail, "EMK_KI_BEHAVIOR_LUNGE": addr.KiBehaviorLunge,
		"EMK_KI_EYE_UP": addr.KiEyeUp, "EMK_KI_ATTN_UP": addr.KiAttnUp, "EMK_KI_F2_ATTN": int64(KiF2Attn), "EMK_KI_F2_LUNGE": int64(KiF2Lunge),
		// AM (type_table AM; wwapi/addr/am.go)
		"EMK_AM_CLASS_SIZE": addr.AmClassSize, "EMK_AM_CO_CYL": addr.AmOffBodyCyl, "EMK_AM_TG_CYL": addr.AmOffBodyCyl, "EMK_AM_HEAD_SPH": addr.AmOffEyeSph,
		"EMK_AM_HEAD_CENTER": addr.AmOffEyeballPos, "EMK_AM_TG_HIT_UP": addr.AmTgHitUp, "EMK_AM_PLAY_SKIP": addr.AmOffPlaySkip, "EMK_AM_SPH_SRC": addr.AmOffEyeSph,
		"EMK_AM_EYE_SPH": addr.AmOffEyeSph, "EMK_AM_MOUTH_SPH": addr.AmOffMouthSph, "EMK_AM_BODY_CYL": addr.AmOffBodyCyl, "EMK_AM_NEEDLE_CYL": addr.AmOffNeedleCyl,
		"EMK_AM_MODE": addr.AmOffMode, "EMK_AM_HUGE_KB": addr.AmOffHugeKnockback, "EMK_AM_BODY_HIT": addr.AmOffBodyHit, "EMK_AM_TIMERS": addr.AmOffCountDown,
		"EMK_AM_TARGET_ANGLE": addr.AmOffTargetAngleY, "EMK_AM_BCK_INDEX": addr.AmOffCurrBckIdx, "EMK_AM_SWALLOW_PID": addr.AmOffSwallowedPID,
		"EMK_AM_MOUTH_POS": addr.AmOffMouthPos, "EMK_AM_EYE_ROT": addr.AmOffEyeRot, "EMK_AM_EYE_ROT_Y": addr.AmOffEyeRotY,
		"EMK_AM_ACT_DOUSA": addr.AmActDousa, "EMK_AM_ACT_ITAI": addr.AmActItai, "EMK_AM_MODE_SWALLOW": addr.AmModeBombSwallow,
		"EMK_AM_MODE_THRASH": addr.AmModeBombThrash, "EMK_AM_MODE_DEAD": addr.AmModeDead, "EMK_AM_EYE_UP": addr.AmEyeUp, "EMK_AM_ATTN_UP": addr.AmAttnUp,
		"EMK_AM_F2_ATTN": int64(AmF2Attn), "EMK_AM_F2_PLAY_SKIP": int64(AmF2PlaySkip),
		// AM2 (type_table AM2; wwapi/addr/am2.go)
		"EMK_AM2_CLASS_SIZE": addr.Am2ClassSize, "EMK_AM2_CO_CYL": addr.Am2OffBodyCyl, "EMK_AM2_TG_CYL": addr.Am2OffBodyCyl, "EMK_AM2_HEAD_SPH": addr.Am2OffWeakSph,
		"EMK_AM2_HEAD_CENTER": addr.Am2OffWeakPos, "EMK_AM2_TG_HIT_UP": addr.Am2TgHitUp, "EMK_AM2_SPH_SRC": addr.Am2OffWeakSph,
		"EMK_AM2_EYE_NODE": addr.Am2OffEyeballPos, "EMK_AM2_EYE_DOWN": addr.Am2EyeDown, "EMK_AM2_ATTN_UP": addr.Am2AttnUp,
		"EMK_AM2_BTK": addr.Am2OffBtk, "EMK_AM2_BRK": addr.Am2OffBrk, "EMK_AM2_STARTS_INACTIVE": addr.Am2OffStartsInactive, "EMK_AM2_SWITCH": addr.Am2OffSwitch,
		"EMK_AM2_MODE": addr.Am2OffMode, "EMK_AM2_M2CE": addr.Am2OffM2CE, "EMK_AM2_WEAK_HIT": addr.Am2OffWeakHit, "EMK_AM2_ABYSS": addr.Am2OffAbyssTimer,
		"EMK_AM2_STUN_TIMER": addr.Am2OffStunTimer, "EMK_AM2_BCK_INDEX": addr.Am2OffCurrBckIdx, "EMK_AM2_NEEDLE_CYL": addr.Am2OffNeedleCyl,
		"EMK_AM2_EYE_SPH": addr.Am2OffEyeSph, "EMK_AM2_ACT_DOUSA": addr.Am2ActDousa, "EMK_AM2_ACT_MAHI": addr.Am2ActMahi, "EMK_AM2_ACT_ITAI": addr.Am2ActItai,
		"EMK_AM2_ACT_MODORU": addr.Am2ActModoru, "EMK_AM2_MODE_DANCE": addr.Am2ModeDance,
		"EMK_AM2_F2_ATTN": int64(Am2F2Attn), "EMK_AM2_F2_WEAK_TG": int64(Am2F2WeakTg), "EMK_AM2_F2_BODY_CO": int64(Am2F2BodyCo), "EMK_AM2_F2_CARRIED": int64(Am2F2Carried),
		"EMK_PROC_BOMB": addr.ProcBomb, "EMK_PROC_BOMB2": addr.ProcBomb2,
	}
	for name, want := range pins {
		got, ok := defs[name]
		if !ok {
			t.Errorf("wwenemy_kinds.h has no %s", name)
			continue
		}
		if got != want {
			t.Errorf("%s = 0x%X in wwenemy_kinds.h, Go has 0x%X", name, got, want)
		}
	}
	// The generic flags2 bits are the block's, and no per-kind bit uses
	// them.
	if int64(KindF2Light) != int64(mailbox.EnemySF2Light) || mailbox.EnemySF2Light|mailbox.EnemySF2Quiet != 0xC0 {
		t.Errorf("KindF2Light 0x%X, block SF2_LIGHT 0x%X", KindF2Light, mailbox.EnemySF2Light)
	}
	for _, b := range []uint8{BkF2HasWeapon, BkF2PlaySkip, BkF2BlurOn, BkF2Attn, CcF2Tg, CcF2Co, CcF2Squash, CcF2LowEye, CcF2NoShadow, CcF2Attn,
		KiF2Attn, KiF2Lunge, AmF2Attn, AmF2PlaySkip, Am2F2Attn, Am2F2WeakTg, Am2F2BodyCo, Am2F2Carried} {
		if b&0xC0 != 0 {
			t.Errorf("per-kind flags2 bit 0x%02X overlaps SF2_LIGHT/QUIET", b)
		}
	}
	// The caps the rows carry are the documented ones.
	if defs["EMK_MO2_CAPS"] != defs["EMK_CAP_DR"]|defs["EMK_CAP_MODEL_EQ"]|defs["EMK_CAP_FOPEN"]|defs["EMK_CAP_WAIT_MODE1"]|defs["EMK_CAP_ADDR_SUCCESS"] ||
		defs["EMK_BK_CAPS"] != defs["EMK_CAP_DR"]|defs["EMK_CAP_MODEL_EQ"]|defs["EMK_CAP_FOPEN"] || defs["EMK_CC_CAPS"] != defs["EMK_CAP_FOPEN"] ||
		defs["EMK_KI_CAPS"] != defs["EMK_CAP_FOPEN"]|defs["EMK_CAP_PKEY_NO_POS"]|defs["EMK_CAP_SPH_COLL"] || defs["EMK_AM_CAPS"] != defs["EMK_CAP_FOPEN"] ||
		defs["EMK_AM2_CAPS"] != defs["EMK_CAP_FOPEN"]|defs["EMK_CAP_MODEL_EQ"] {
		t.Error("a row's caps are not the type table's")
	}
	if defs["EMK_MO2_MTX"] != defs["EMK_MTX_DR"] || defs["EMK_BK_MTX"] != defs["EMK_MTX_DR"] || defs["EMK_CC_MTX"] != defs["EMK_MTX_CC"] ||
		defs["EMK_BK_ATTACH"] != defs["EMK_ATTACH_BK"] || defs["EMK_CC_EYE"] != defs["EMK_EYE_CC"] || defs["EMK_CC_COLL"] != defs["EMK_COLL_CC"] {
		t.Error("a row's hooks are not the type table's")
	}
	// The allow rows (type_table KI, AM, AM2): MTX_PLAIN, COLL_STD, the
	// fixed eye, no attach; the caps bit a row's no-pos pkey reads is the
	// Go row's PKeyNoPos; a PLAIN row is an fopEn_enemy_c with a morf (R-2).
	for _, id := range []KindID{KindKI, KindAM, KindAM2} {
		p := prefix[id]
		if defs[p+"MTX"] != defs["EMK_MTX_PLAIN"] || defs[p+"COLL"] != defs["EMK_COLL_STD"] || defs[p+"EYE"] != defs["EMK_EYE_FIXED"] ||
			defs[p+"ATTACH"] != defs["EMK_ATTACH_NONE"] || defs[p+"CAPS"]&defs["EMK_CAP_FOPEN"] == 0 || defs[p+"MORF"] == 0 {
			t.Errorf("%s row's hooks are not the allow type table's", p)
		}
		if (defs[p+"CAPS"]&defs["EMK_CAP_PKEY_NO_POS"] != 0) != id.Row().PKeyNoPos {
			t.Errorf("%sCAPS PKEY_NO_POS disagrees with the Go row's PKeyNoPos", p)
		}
		if _, ok := defs[p+"QUIET_FRAMES"]; !ok {
			t.Errorf("wwenemy_kinds.h has no %sQUIET_FRAMES", p)
		}
	}
	if defs["EMK_AM_QUIET_FRAMES"] != 40 {
		t.Errorf("EMK_AM_QUIET_FRAMES = %d, A0 measured the DEAD bck at 40 frames", defs["EMK_AM_QUIET_FRAMES"])
	}
	// The mirrored rows' quiet frames are the header's, the AM swallow's
	// bcks are the archive's names.
	for name, want := range map[string]string{"EMK_AM_BCK_DAMAGE": "DAMAGE", "EMK_AM_BCK_DAMAGE_LOOP": "DAMAGE_LOOP", "EMK_AM_BCK_OPEN": "OPEN",
		"EMK_AM_BCK_OPEN_LOOP": "OPEN_LOOP", "EMK_AM_BCK_WAIT": "SLEEP_LOOP"} {
		if got := BckName(KindAM, uint8(defs[name])); got != want {
			t.Errorf("%s 0x%X is %s, want %s", name, defs[name], got, want)
		}
	}
	// The annex overrides cover the first rows by kind id; the hook bytes
	// (HKDX, allow design D3) every row past them.
	if mailbox.EnemyAnnexKinds > len(Kinds) || mailbox.EnemyAnnexKinds+mailbox.EnemyAnnexHKDXKinds < len(Kinds) || mailbox.EnemyAnnexKinds != int(KindKI) {
		t.Errorf("the annex has %d override rows and %d hook bytes for %d kinds", mailbox.EnemyAnnexKinds, mailbox.EnemyAnnexHKDXKinds, len(Kinds))
	}
}

// TestKindHash pins the table's hash: a change here is a wire-visible
// table change (hosts and clients of different builds refuse each other)
// and must be deliberate.
func TestKindHash(t *testing.T) {
	const want = 0x97EDB0F9 // allow batch A1: the KI, AM and AM2 rows (was 0x73A3A496)
	if got := KindHash(); got != want {
		t.Errorf("KindHash() = 0x%08X, pinned 0x%08X: the Kinds table changed", got, want)
	}
}
