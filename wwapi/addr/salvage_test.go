package addr

import "testing"

// TestSalvageLayout pins the salvage table's layout to the decomp
// (d_salvage.h:17-79) and the GZLE01 main.dol's getters (salvage.go).
func TestSalvageLayout(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want uint32
	}{
		{"mTagData_p", SalvageTagDataPtr, 0x803F6B44},            // symbols.txt:18744; r13 0x803FE0E0 - 30108
		{"mInfo", SalvageOffInfo, 0x04},                          // getters add 4 to every field
		{"dSalvage_info_c", SalvageInfoSize, 0x38},               // mulli r0,r4,0x38
		{"MAX_TOTAL", SalvageInfoCount, 160},                     // init's loop count
		{"sizeof", SalvageSize, 0x2304},                          // d_salvage.h:79
		{"mRoomNo", SalvageOffInfo + SalvageInfoOffRoomNo, 0x30}, // getRoomNo lbz +0x30
		{"mSaveNo", SalvageOffInfo + SalvageInfoOffSaveNo, 0x31}, // getSaveNo lbz +0x31
		{"mKind", SalvageOffInfo + SalvageInfoOffKind, 0x33},     // getKind lbz +0x33
		{"mFlag", SalvageOffInfo + SalvageInfoOffFlag, 0x35},     // checkUsed / setFlag +0x35
		{"proc", uint32(ProcSalvage), 0x194},                     // fpcNm_Salvage_e
		{"mSalvageId", SalvageIDAddr, 0x803FE0E0 - 30100},        // symbols.txt:18746; phase_4 stw r0,-30100(r13)
		{"mR", SalvageOffInfo + SalvageInfoOffR, 0x1C},           // getR lfs +0x1C
		{"mDistance", SalvageOffInfo + SalvageInfoOffDist, 0x2C}, // getDistance lfs +0x2C
		{"mInfo[159].mFlag", SalvageInfoAddr(0x80C00000, 159) + SalvageInfoOffFlag, 0x80C00000 + 4 + 159*0x38 + 0x31},
	} {
		if c.got != c.want {
			t.Errorf("%s = 0x%X, want 0x%X", c.name, c.got, c.want)
		}
	}
	if SalvageOffInfo+SalvageInfoCount*SalvageInfoSize != SalvageSize {
		t.Error("the table does not end at its last entry")
	}
}
