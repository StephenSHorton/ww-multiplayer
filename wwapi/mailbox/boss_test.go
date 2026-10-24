package mailbox

import (
	"os"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// wwbossHeader is the C source of the boss_kill constants.
const wwbossHeader = "../../inject/include/wwboss.h"

// bosskillSource is the REL op the header's defines drive.
const bosskillSource = "../../inject/rel/bosskill.c"

// TestBossKillDispatchBdk: bosskill_frame gives Bdk its own arm, under
// WWBOSS_WITH_BDK, ahead of the final else: that else runs bk_bmd (Kalle
// Demos' lethal block) for every proc not named before it, so a 0xF0
// request that only passed bk_proc_ok would get Bmd's writes on Bdk
// memory (relay-helmaroc.md check notes, missing case 1). bk_proc_ok and
// bk_stage_ok take the proc under the same switch, the stage exactly
// M2tower.
func TestBossKillDispatchBdk(t *testing.T) {
	b, err := os.ReadFile(bosskillSource)
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	i := strings.Index(src, "void bosskill_frame(void) {")
	if i < 0 {
		t.Fatal("no bosskill_frame")
	}
	body := src[i:]
	arm := strings.Index(body, "} else if (proc == WWBOSS_PROC_BDK) {")
	call := strings.Index(body, "bk_bdk(seq, a);")
	last := strings.LastIndex(body, "} else {\n        bk_bmd(seq, a);")
	if arm < 0 || call < arm || last < 0 || call > last {
		t.Fatalf("bosskill_frame: the Bdk arm at %d, its bk_bdk at %d, the final bk_bmd else at %d; want arm < call < else", arm, call, last)
	}
	if g := strings.LastIndex(body[:arm], "#if "); g < 0 || !strings.HasPrefix(body[g:], "#if WWBOSS_WITH_BDK\n") {
		t.Error("bosskill_frame's Bdk arm is not under #if WWBOSS_WITH_BDK")
	}
	for _, want := range []string{
		"#if WWBOSS_WITH_BDK\n    if (proc == WWBOSS_PROC_BDK) return 1;\n#endif",
		"#if WWBOSS_WITH_BDK\n    } else if (proc == WWBOSS_PROC_BDK) {\n        want = m2_tower;\n#endif",
		"m2_tower[8] = { 'M', '2', 't', 'o', 'w', 'e', 'r', 0 };",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("bosskill.c lacks %q", want)
		}
	}
}

// TestBossHeaderPins: every Go constant equals its WWBOSS_* define, and
// every define has a Go constant.
func TestBossHeaderPins(t *testing.T) {
	pinHeader(t, wwbossHeader, headerDefines(t, wwbossHeader), map[string]uint64{
		"WWBOSS_OFF":             uint64(BossOff),
		"WWBOSS_SIZE":            uint64(BossSize),
		"WWBOSS_SEQ_OFF":         BossSeqOff,
		"WWBOSS_PROC_OFF":        BossProcOff,
		"WWBOSS_TAG_OFF":         BossTagOff,
		"WWBOSS_ACK_OFF":         BossAckOff,
		"WWBOSS_RESULT_OFF":      BossResultOff,
		"WWBOSS_GO_SPAN":         BossGoSpan,
		"WWBOSS_TAG":             uint64(BossTag),
		"WWBOSS_PROC_BTD":        uint64(BossProcBtd),
		"WWBOSS_R_NONE":          uint64(BossRNone),
		"WWBOSS_R_APPLIED":       uint64(BossRApplied),
		"WWBOSS_R_WAITING":       uint64(BossRWaiting),
		"WWBOSS_R_ALREADY":       uint64(BossRAlready),
		"WWBOSS_R_NO_ACTOR":      uint64(BossRNoActor),
		"WWBOSS_R_GATE":          uint64(BossRGate),
		"WWBOSS_BTD_M02E4_OFF":   BossBtdM02E4Off,
		"WWBOSS_BTD_STATE_OFF":   BossBtdStateOff,
		"WWBOSS_BTD_ACT_INTRO":   BossBtdActIntro,
		"WWBOSS_BTD_ACT_END":     BossBtdActEnd,
		"WWBOSS_BTD_STATE_DEAD":  BossBtdStateDead,
		"WWBOSS_BTD_M02E0_OFF":   BossBtdM02E0Off,
		"WWBOSS_BTD_M02F6_OFF":   BossBtdM02F6Off,
		"WWBOSS_BTD_PHASE_BARE":  BossBtdPhaseBare,
		"WWBOSS_BTD_ROCK_LANDED": BossBtdRockLand,
		"WWBOSS_PROC_BMD":        uint64(BossProcBmd),
		"WWBOSS_BMD_MODE_OFF":    BossBmdModeOff,
		"WWBOSS_BMD_M302_OFF":    BossBmdM302Off,
		"WWBOSS_BMD_M304_OFF":    BossBmdM304Off,
		"WWBOSS_BMD_M306_OFF":    BossBmdM306Off,
		"WWBOSS_BMD_M310_OFF":    BossBmdM310Off,
		"WWBOSS_BMD_MB74_OFF":    BossBmdMB74Off,
		"WWBOSS_BMD_MBDC_OFF":    BossBmdMBDCOff,
		"WWBOSS_BMD_MBE0_OFF":    BossBmdMBE0Off,
		"WWBOSS_BMD_MODE_FALLEN": BossBmdModeFallen,
		"WWBOSS_BMD_STEP_LANDED": BossBmdStepLanded,
		"WWBOSS_BMD_MODE_INTRO":  BossBmdModeIntro,
		"WWBOSS_BMD_MODE_END":    BossBmdModeEnd,
		"WWBOSS_BMD_M306_DEAD":   BossBmdM306Dead,
		"WWBOSS_BMD_MB74_DEATH":  BossBmdMB74Death,
		"WWBOSS_BMD_M310_DEAD":   BossBmdM310Dead,
		"WWBOSS_BMD_MBE0_DEAD":   BossBmdMBE0Dead,
		"WWBOSS_BGM_STOP_FN":     uint64(BossBgmStopFn),
		"WWBOSS_ZEL_BASIC":       uint64(BossZelBasic),
		"WWBOSS_BGM_STOP_FADE":   BossBgmStopFade,
		// Batch D: Gohdan (R-D1).
		"WWBOSS_PROC_BST":         uint64(BossProcBst),
		"WWBOSS_WITH_BST":         BossWithBst,
		"WWBOSS_BST_ANIM":         BossBstAnim,
		"WWBOSS_BST_PART_OFF":     BossBstPartOff,
		"WWBOSS_BST_PART_HEAD":    BossBstPartHead,
		"WWBOSS_BST_PART_HAND1":   BossBstPartHand1,
		"WWBOSS_BST_PART_HAND2":   BossBstPartHand2,
		"WWBOSS_BST_MORF_OFF":     BossBstMorfOff,
		"WWBOSS_BST_ACT_OFF":      BossBstActOff,
		"WWBOSS_BST_DMG_OFF":      BossBstDmgOff,
		"WWBOSS_BST_M2E7C_OFF":    BossBstM2E7COff,
		"WWBOSS_BST_M2E9A_OFF":    BossBstM2E9AOff,
		"WWBOSS_BST_ACT_SLEEP":    BossBstActSleep,
		"WWBOSS_BST_ACT_HEAD_DMG": BossBstActHeadDmg,
		"WWBOSS_BST_ACT_END_DEMO": BossBstActEndDemo,
		"WWBOSS_BST_DMG_MOUTH":    BossBstDmgMouth,
		"WWBOSS_BST_M2E7C_EYES":   BossBstM2E7CEyes,
		"WWBOSS_BST_M2E7C_DONE":   BossBstM2E7CDone,
		"WWBOSS_BST_M2E9A_DEATH":  BossBstM2E9ADeath,
		"WWBOSS_BST_HAND_DMG_END": BossBstHandDmgEnd,
		"WWBOSS_BST_ANM_DEAD":     BossBstAnmDead,
		"WWBOSS_STRM_PREPARE_FN":  BossStrmPrepareFn,
		"WWBOSS_STRM_BST_CLEAR":   BossStrmBstClear,
		"WWBOSS_GETRES_FN":        BossGetResFn,
		"WWBOSS_OBJINFO":          BossObjInfo,
		"WWBOSS_OBJINFO_COUNT":    BossObjInfoCount,
		"WWBOSS_SETANM_FN":        BossSetAnmFn,
		"WWBOSS_ITER_JUDGE_FN":    BossIterJudgeFn,
		// Batch E: Molgera (design/relay-molgera.md).
		"WWBOSS_PROC_BWD":        uint64(BossProcBwd),
		"WWBOSS_WITH_BWD":        BossWithBwd,
		"WWBOSS_BWD_MORF_OFF":    BossBwdMorfOff,
		"WWBOSS_BWD_ACT_OFF":     BossBwdActOff,
		"WWBOSS_BWD_STEP_OFF":    BossBwdStepOff,
		"WWBOSS_BWD_TONGUE_OFF":  BossBwdTongueOff,
		"WWBOSS_BWD_HITS_OFF":    BossBwdHitsOff,
		"WWBOSS_BWD_CRIT_OFF":    BossBwdCritOff,
		"WWBOSS_BWD_DEMO_OFF":    BossBwdDemoOff,
		"WWBOSS_BWD_ACT_INTRO":   BossBwdActIntro,
		"WWBOSS_BWD_ACT_TONGUE":  BossBwdActTongue,
		"WWBOSS_BWD_ACT_SFLY":    BossBwdActSFly,
		"WWBOSS_BWD_ACT_END":     BossBwdActEnd,
		"WWBOSS_BWD_ACT_INERT":   BossBwdActInert,
		"WWBOSS_BWD_STEP_PULLED": BossBwdStepPulled,
		"WWBOSS_BWD_HITS_LETHAL": BossBwdHitsLethal,
		"WWBOSS_BWD_CRIT_PRE":    BossBwdCritPre,
		// Batch E: Jalhalla (design/relay-jalhalla.md).
		"WWBOSS_PROC_BPW":         uint64(BossProcBpw),
		"WWBOSS_WITH_BPW":         BossWithBpw,
		"WWBOSS_BPW_TYPE_OFF":     BossBpwTypeOff,
		"WWBOSS_BPW_TYPE_BODY":    BossBpwTypeBody,
		"WWBOSS_BPW_ACTION_OFF":   BossBpwActionOff,
		"WWBOSS_BPW_STATE_OFF":    BossBpwStateOff,
		"WWBOSS_BPW_HEALTH_OFF":   BossBpwHealthOff,
		"WWBOSS_BPW_ACT_DAMAGE":   BossBpwActDamage,
		"WWBOSS_BPW_ACT_SEPARATE": BossBpwActSeparate,
		"WWBOSS_BPW_ACT_INTRO":    BossBpwActIntro,
		"WWBOSS_BPW_ST_FROZEN_LO": BossBpwStFrozenLo,
		"WWBOSS_BPW_ST_THROWN":    BossBpwStThrown,
		"WWBOSS_BPW_ST_SPIKE":     BossBpwStSpike,
		"WWBOSS_BPW_ST_SPIKE_AIR": BossBpwStSpikeAir,
		"WWBOSS_BPW_ST_DEATH":     BossBpwStDeath,
		// Batch F: Helmaroc King (design/relay-helmaroc.md).
		"WWBOSS_PROC_BDK":          uint64(BossProcBdk),
		"WWBOSS_WITH_BDK":          BossWithBdk,
		"WWBOSS_BDK_MORF_OFF":      BossBdkMorfOff,
		"WWBOSS_BDK_ACT_OFF":       BossBdkActOff,
		"WWBOSS_BDK_MASK_OFF":      BossBdkMaskOff,
		"WWBOSS_BDK_DEMO_OFF":      BossBdkDemoOff,
		"WWBOSS_BDK_HEALTH_OFF":    BossBdkHealthOff,
		"WWBOSS_BDK_ACT_FIGHT_MAX": BossBdkActFightMax,
		"WWBOSS_BDK_ACT_MASK_DEMO": BossBdkActMaskDemo,
		"WWBOSS_BDK_ACT_END":       BossBdkActEnd,
		"WWBOSS_BDK_ACT_START":     BossBdkActStart,
		"WWBOSS_BDK_ACT_T_FLY":     BossBdkActTFly,
		"WWBOSS_BDK_ACT_T_DOWN":    BossBdkActTDown,
		"WWBOSS_BDK_ACT_AFTER":     BossBdkActAfter,
		"WWBOSS_BDK_MASK_BROKEN":   BossBdkMaskBroken,
	})
}

// TestBossRequestPlace: the request fills the gap between the enemy region
// and the remote-Link block, inside RemoteBlockRegion (which the canary
// probe skips) and outside both canary arenas.
func TestBossRequestPlace(t *testing.T) {
	if BossOff != EnemyRegionEnd || BossOff+BossSize != RemoteOff || BossOff < RemoteRegionStart {
		t.Fatalf("boss_kill request +0x%X..+0x%X, want the gap +0x%X..+0x%X", BossOff, BossOff+BossSize, EnemyRegionEnd, RemoteOff)
	}
	r := BossRequestRegion
	if r.Start < RemoteBlockRegion.Start || r.End > RemoteBlockRegion.End {
		t.Fatalf("boss_kill request %#x..%#x is outside %+v", r.Start, r.End, RemoteBlockRegion)
	}
	for _, c := range []struct{ s, e uint32 }{{CanaryArena.Start, CanaryArena.End}, {CanaryArenaHigh.Start, CanaryArenaHigh.End}, {CanaryTRK.Start, CanaryTRK.End}} {
		if r.Start < c.e && c.s < r.End {
			t.Fatalf("boss_kill request %#x..%#x overlaps a canary arena %#x..%#x", r.Start, r.End, c.s, c.e)
		}
	}
	if BossAckOff != BossGoSpan || BossResultOff+4 != int(BossSize) {
		t.Fatal("the Go span must end where the REL's ack starts, and the result end the request")
	}
}

// TestBossReadAndFields: ReadBoss decodes the REL's half; the request
// fields are the proc and tag, then the seq.
func TestBossReadAndFields(t *testing.T) {
	m := mem.NewFakeMem()
	pt, sq := BossRequestFields(BossProcBtd, 7)
	if err := m.Write(BossAddr+BossProcOff, pt); err != nil {
		t.Fatal(err)
	}
	if err := m.Write(BossAddr+BossSeqOff, sq); err != nil {
		t.Fatal(err)
	}
	if err := m.Write(BossAddr+BossAckOff, []byte{0, 0, 0, 7, BossRWaiting, BossBtdActIntro, 0x01, BossBtdPhaseBare}); err != nil {
		t.Fatal(err)
	}
	st, err := ReadBoss(m)
	if err != nil {
		t.Fatal(err)
	}
	if st != (BossStatus{Seq: 7, Ack: 7, Result: BossRWaiting, Act: BossBtdActIntro, State: 1, Phase: BossBtdPhaseBare}) {
		t.Fatalf("ReadBoss = %+v", st)
	}
	if b, _ := m.Read(BossAddr+BossProcOff, 4); b[0] != 0x00 || b[1] != 0xEB || b[2] != 'B' || b[3] != 'K' {
		t.Fatalf("proc and tag bytes % X", b)
	}
}

// TestBossDetailPerProc: the result's bytes after BossR* read as Btd's
// m02E4, mGohmaState and m02E0 for Gohma, as Bmd's mMode, m302 and m304
// for Kalle Demos, as Bwd's m18AE, m18B0 and m1BB5 for Molgera, as Bpw's
// mBodyAction, mActionState and health for Jalhalla and as Bdk's mAction,
// m8F8 and health for Helmaroc King; the request
// carries the proc it was made for.
func TestBossDetailPerProc(t *testing.T) {
	st := BossStatus{Result: BossRApplied, Act: BossBmdModeFallen, State: 3, Phase: 1}
	if got, want := BossDetail(BossProcBmd, st), "mMode 0x3, m302 3, m304 1"; got != want {
		t.Fatalf("Bmd detail %q, want %q", got, want)
	}
	if got, want := BossDetail(BossProcBtd, st), "m02E4 0x3, state 0x3, m02E0 1"; got != want {
		t.Fatalf("Btd detail %q, want %q", got, want)
	}
	if got := BossDetail(BossProcBmd, BossStatus{Act: BossBmdModeIntro, State: 0xFF}); got != "mMode 0xA, m302 -1, m304 0" {
		t.Fatalf("Bmd intro detail %q", got)
	}
	if got, want := BossDetail(BossProcBwd, BossStatus{Act: BossBwdActTongue, State: BossBwdStepPulled, Phase: 2}), "m18AE 0x3, m18B0 2, m1BB5 2"; got != want {
		t.Fatalf("Bwd detail %q, want %q", got, want)
	}
	// The intro-skip create's m18B0 -10 (d_a_bwd.cpp:2545-2549).
	if got, want := BossDetail(BossProcBwd, BossStatus{Act: 1, State: 0xF6}), "m18AE 0x1, m18B0 -10, m1BB5 0"; got != want {
		t.Fatalf("Bwd intro-skip detail %q, want %q", got, want)
	}
	if got, want := BossDetail(BossProcBpw, BossStatus{Act: BossBpwActDamage, State: BossBpwStSpike, Phase: 15}), "mBodyAction 3, mActionState 0x64, health 15"; got != want {
		t.Fatalf("Bpw detail %q, want %q", got, want)
	}
	if got, want := BossDetail(BossProcBdk, BossStatus{Act: BossBdkActTFly, State: BossBdkMaskBroken, Phase: 0}), "mAction 0x64, m8F8 4, health 0"; got != want {
		t.Fatalf("Bdk detail %q, want %q", got, want)
	}
	if BossProcName(BossProcBmd) != "Kalle Demos (Bmd)" || BossProcName(BossProcBtd) != "Gohma (Btd)" || BossProcName(0xED) != "proc 0x00ED" ||
		BossProcName(BossProcBwd) != "Molgera (Bwd)" || BossProcName(BossProcBpw) != "Jalhalla (Bpw)" || BossProcName(BossProcBdk) != "Helmaroc King (Bdk)" {
		t.Fatal("BossProcName")
	}
	m := mem.NewFakeMem()
	pt, _ := BossRequestFields(BossProcBmd, 3)
	if err := m.Write(BossAddr+BossProcOff, pt); err != nil {
		t.Fatal(err)
	}
	if b, _ := m.Read(BossAddr+BossProcOff, 4); b[0] != 0x00 || b[1] != 0xEC || b[2] != 'B' || b[3] != 'K' {
		t.Fatalf("Bmd proc and tag bytes % X", b)
	}
}
