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

// emkRowFields are the fields every live row (MO2, BK, CC) defines: the
// em_kind struct's (inject/rel/enemy_mirror.h), one EMK_<KIND>_<FIELD>
// each, 0 for "none".
var emkRowFields = []string{"ID", "PROC", "CLASS_SIZE", "BCK_FIRST", "BCK_LAST", "BCK_WAIT", "MORF", "GATE", "DR", "DR_MODE", "DR_ACTION",
	"TILT", "CO_CYL", "TG_CYL", "HEAD_SPH", "DEF_SPH", "HEAD_CENTER", "DEF_CENTER", "CO_FORWARD", "TG_HIT_UP", "INVULN", "INVULN_WAIT",
	"ICE", "WEAPON", "BARE", "BLUR_ON", "BLUR_SHAPE", "BLUR_SCALE", "ANM_GATE", "PLAY_SKIP", "SPH_SRC", "CAPS", "MTX", "COLL", "ATTACH", "EYE"}

// TestKindTablePins: wwenemy_kinds.h and Go agree. Every live row has
// every em_kind field; each row's proc, archive, BCK range, morf, ice and
// gate equal the Go Kinds row; the class offsets equal wwapi/addr's; the
// per-kind flags2 bits equal the Go samplers'; the generic SF2 bits equal
// the block's; and the PKey test vector is the one TestPKey checks.
func TestKindTablePins(t *testing.T) {
	src, defs := kindsHeader(t)
	prefix := map[KindID]string{KindMO2: "EMK_MO2_", KindBK: "EMK_BK_", KindCC: "EMK_CC_"}
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
		"EMK_KIND_COUNT": int64(len(Kinds)),
		// the placement key
		"EMK_PKEY_LEN": PKeyLen, "EMK_PKEY_FNV_OFFSET": int64(fnvOffset), "EMK_PKEY_FNV_PRIME": int64(fnvPrime),
		"EMK_PKEY_TV_PROC": int64(pkeyTVProc), "EMK_PKEY_TV_SETID": int64(pkeyTVSetID), "EMK_PKEY_TV_PARAMS": int64(pkeyTVParams),
		"EMK_PKEY_TV_ROOM": int64(pkeyTVRoom), "EMK_PKEY_TV_HOME_X": int64(pkeyTVHome[0]), "EMK_PKEY_TV_HOME_Y": int64(pkeyTVHome[1]),
		"EMK_PKEY_TV_HOME_Z": int64(pkeyTVHome[2]), "EMK_PKEY_TV_OUT": int64(pkeyTVOut),
		// generic fields
		"EMK_AC_PARAMS": addr.ActorOffParameters, "EMK_AC_HOME_POS": addr.ActorOffHomePos, "EMK_AC_HOME_ROOM": addr.ActorOffHomeRoomNo, "EMK_ICE_SHRINK": addr.EnemyIceOffTimer, "EMK_ICE_FREEZE": addr.EnemyIceOffFreezeTimer,
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
	for _, b := range []uint8{BkF2HasWeapon, BkF2PlaySkip, BkF2BlurOn, BkF2Attn, CcF2Tg, CcF2Co, CcF2Squash, CcF2LowEye, CcF2NoShadow, CcF2Attn} {
		if b&0xC0 != 0 {
			t.Errorf("per-kind flags2 bit 0x%02X overlaps SF2_LIGHT/QUIET", b)
		}
	}
	// The caps the rows carry are the documented ones.
	if defs["EMK_MO2_CAPS"] != defs["EMK_CAP_DR"]|defs["EMK_CAP_MODEL_EQ"]|defs["EMK_CAP_FOPEN"]|defs["EMK_CAP_WAIT_MODE1"]|defs["EMK_CAP_ADDR_SUCCESS"] ||
		defs["EMK_BK_CAPS"] != defs["EMK_CAP_DR"]|defs["EMK_CAP_MODEL_EQ"]|defs["EMK_CAP_FOPEN"] || defs["EMK_CC_CAPS"] != defs["EMK_CAP_FOPEN"] {
		t.Error("a row's caps are not the type table's")
	}
	if defs["EMK_MO2_MTX"] != defs["EMK_MTX_DR"] || defs["EMK_BK_MTX"] != defs["EMK_MTX_DR"] || defs["EMK_CC_MTX"] != defs["EMK_MTX_CC"] ||
		defs["EMK_BK_ATTACH"] != defs["EMK_ATTACH_BK"] || defs["EMK_CC_EYE"] != defs["EMK_EYE_CC"] || defs["EMK_CC_COLL"] != defs["EMK_COLL_CC"] {
		t.Error("a row's hooks are not the type table's")
	}
	// The annex overrides cover the live rows, by kind id.
	if mailbox.EnemyAnnexKinds != len(Kinds) {
		t.Errorf("the annex has %d override rows for %d kinds", mailbox.EnemyAnnexKinds, len(Kinds))
	}
}

// TestKindHash pins the table's hash: a change here is a wire-visible
// table change (hosts and clients of different builds refuse each other)
// and must be deliberate.
func TestKindHash(t *testing.T) {
	const want = 0x73A3A496
	if got := KindHash(); got != want {
		t.Errorf("KindHash() = 0x%08X, pinned 0x%08X: the Kinds table changed", got, want)
	}
}
