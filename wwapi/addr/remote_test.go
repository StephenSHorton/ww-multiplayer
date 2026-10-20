package addr

import "testing"

// TestRemoteLinkOffsets pins the remote-Link reads of Link to their
// structs and to the one live read so far: each field sits where its
// neighbours put it (d_a_player.h:479-482; d_a_player_main.h:18-26,
// 2080-2099, 2232, 2302) and the shield angle is where Link's own Tg
// pointed in Step −1.
func TestRemoteLinkOffsets(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want int
	}{
		{"PlayerOffCutType", PlayerOffCutType, 0x290},
		{"PlayerOffCutCount (after mCutType)", PlayerOffCutCount, PlayerOffCutType + 1},
		{"PlayerOffDamageWait (s16 after a u8 pair and a pad)", PlayerOffDamageWait, PlayerOffCutType + 4},
		{"PlayerOffAnmHeapUpper (m_anm_heap_under[2] at 0x2FDC)", PlayerOffAnmHeapUpper, 0x2FDC + 2*PlayerAnmHeapSize},
		{"PlayerOffUpperAnm (UPPER_MOVE2_e's mIdx)", PlayerOffUpperAnm, 0x301C},
		{"PlayerOffUpperAnm before mFrameCtrlUnder (0x302C)", PlayerOffUpperAnm + 0x10, 0x302C},
		{"PlayerOffCurProc after m_tex_scroll_heap (0x31C8)", PlayerOffCurProc, 0x31C8 + PlayerAnmHeapSize},
		{"PlayerOffShieldAngleY (Step -1: 0x80ABD9F2 - Link 0x80ABA4AC)", PlayerOffShieldAngleY, 0x80ABD9F2 - 0x80ABA4AC},
		{"PlayerOffModeFlg", PlayerOffModeFlg, 0x3618},
		{"PlayerOffDemoType past the cut fields", PlayerOffDemoType, 0x304},
	} {
		if c.got != c.want {
			t.Errorf("%s = 0x%X, want 0x%X", c.name, c.got, c.want)
		}
	}
	if PlayerOffDamageWait >= PlayerOffNoResetFlg1 {
		t.Error("mDamageWaitTimer sits past mNoResetFlg1")
	}
	for _, c := range []struct {
		name      string
		got, want int
	}{
		{"PlayerModeDamage", PlayerModeDamage, 1 << 3},
		{"PlayerModeInShip", PlayerModeInShip, 1 << 13},
		{"PlayerModeSwim", PlayerModeSwim, 1 << 18},
		{"PlayerProcCrouchDefense", PlayerProcCrouchDefense, 0x0C},
		{"PlayerProcCrouchDefenseSlip", PlayerProcCrouchDefenseSlip, PlayerProcCrouchDefense + 1},
		{"PlayerProcGuardSlip", PlayerProcGuardSlip, 0x6D},
		{"PlayerProcDemoDead", PlayerProcDemoDead, 0xB2},
		{"PlayerAnmAtng", PlayerAnmAtng, 0x16},
		{"PlayerAnmAtngHam", PlayerAnmAtngHam, 0x1B},
	} {
		if c.got != c.want {
			t.Errorf("%s = 0x%X, want 0x%X", c.name, c.got, c.want)
		}
	}
}

// TestRemoteLinkProcs pins the PROC_POLICY seed's proc names to
// f_pc_name.h's enum values (each line carries its value in a comment,
// remote.go cites the line), neighbours chained where the enum puts them
// back to back, and no two the same.
func TestRemoteLinkProcs(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want int
	}{
		{"ProcPlayer", ProcPlayer, 0xA9},
		{"ProcMo2", ProcMo2, 0xBD},
		{"ProcFM", ProcFM, 0x79},
		{"ProcBK (after MO2)", ProcBK, ProcMo2 + 1},
		{"ProcBPW", ProcBPW, 0xD4},
		{"ProcBWD", ProcBWD, 0xDA},
		{"ProcRD", ProcRD, 0xE1},
		{"ProcDaiocta (after RD)", ProcDaiocta, ProcRD + 1},
		{"ProcBTD", ProcBTD, 0xEB},
		{"ProcBMD (after BTD)", ProcBMD, ProcBTD + 1},
		{"ProcBDK", ProcBDK, 0xF0},
		{"ProcBST", ProcBST, 0xF2},
		{"ProcFganon (after BST)", ProcFganon, ProcBST + 1},
		{"ProcBGN", ProcBGN, 0xF5},
		{"ProcBGN2", ProcBGN2, ProcBGN + 1},
		{"ProcBGN3", ProcBGN3, ProcBGN + 2},
		{"ProcBoko is not the Bokoblin", ProcBoko, 0x1D5},
	} {
		if c.got != c.want {
			t.Errorf("%s = 0x%X, want 0x%X", c.name, c.got, c.want)
		}
	}
	seen := map[int]string{}
	for name, p := range map[string]int{"Player": ProcPlayer, "Mo2": ProcMo2, "FM": ProcFM, "BK": ProcBK, "BPW": ProcBPW,
		"BWD": ProcBWD, "RD": ProcRD, "Daiocta": ProcDaiocta, "BTD": ProcBTD, "BMD": ProcBMD, "BDK": ProcBDK, "BST": ProcBST,
		"Fganon": ProcFganon, "BGN": ProcBGN, "BGN2": ProcBGN2, "BGN3": ProcBGN3, "Boko": ProcBoko} {
		if other, ok := seen[p]; ok {
			t.Errorf("Proc%s and Proc%s are both 0x%X", name, other, p)
		}
		seen[p] = name
	}
	for _, s := range []string{StageFFOutside, StageFFRooms, StageFFRooms2, StageFFTower} {
		if len(s) == 0 || len(s) > StageNameLen {
			t.Errorf("FF stage %q does not fit a stage name", s)
		}
	}
}
