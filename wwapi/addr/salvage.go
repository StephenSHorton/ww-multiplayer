package addr

// The salvage points of the loaded sea (dSalvage_control_c, d_salvage.h):
// a table the first daSalvage_c of a play scene allocates on its own heap
// (daSalvage_c::CreateHeap, d_a_salvage.cpp:34-39) and every daSalvage_c
// of a room fills as it is created (dSalvage_control_c::entry,
// d_salvage.cpp:46-160), keeping only the points still to salvage. The
// crane reads it every frame; a point whose flag bit 0x01 is set is not
// salvageable (checkUsed, d_salvage.cpp:265-270), and end_salvage takes it
// out (init_one, d_a_salvage.cpp:524-526).
//
// Checked in the GZLE01 main.dol (the decomp's layout, d_salvage.h:8-37,
// and these disassembled):
//
//   - mTagData_p is the .sbss word at 0x803F6B44 (symbols.txt:18744):
//     daSalvage_c::init_room (0x80056760) loads it with lwz r3,-30108(r13),
//     and __init_registers (0x80003278) sets r13 to 0x803FE0E0;
//   - mInfo starts at +0x04 and each dSalvage_info_c is 0x38 bytes: every
//     getter is mulli r0,r4,0x38; add r3,r3,r0; then the field at
//     0x04 + its offset: getRoomNo (0x800CCE28) lbz +0x30, getSaveNo
//     (0x800CCE58) lbz +0x31, getKind (0x800CCE78) lbz +0x33, getItemNo
//     lbz +0x34, getSwitchNo lwz +0x24;
//   - checkUsed (0x800CCEDC) is lbz +0x35 and bit 0, and setFlag
//     (0x800CCF1C) lbz +0x35; or r0,r0,r5; stb +0x35;
//   - init (0x800CC734) writes mRoomNo -1 at +0x30 of 160 entries, 0x38
//     apart, then mSeaNum at +0 and mRoomNum at +1.
//
// The layout was first read by datstarkey/ww-online
// (WWOnline.Client/Data/GameMemoryAddresses.cs:377-391, MIT); the values
// here are this file's own reading of the decomp and the DOL.
const (
	// SalvageTagDataPtr is daSalvage_c::mTagData_p. Only the play scene's
	// create nulls it (daSalvage_c::init from dScnPly_Create,
	// d_s_play.cpp:1362, d_a_salvage.h:85-89); the owner's delete frees
	// the table without (d_a_salvage.cpp:122-146), so it may point at a
	// freed heap until the next scene.
	SalvageTagDataPtr uint32 = 0x803F6B44

	SalvageOffSeaNum  = 0x00 // u8 mSeaNum
	SalvageOffRoomNum = 0x01 // u8 mRoomNum
	SalvageOffInfo    = 0x04 // dSalvage_info_c mInfo[MAX_TOTAL]
	SalvageInfoSize   = 0x38
	SalvageSeaMax     = 128 // MAX_SEA: mInfo[0..127], the sea's own points
	SalvageRoomMax    = 32  // MAX_ROOM: mInfo[128..159], a room's
	SalvageInfoCount  = SalvageSeaMax + SalvageRoomMax
	SalvageSize       = SalvageOffInfo + SalvageInfoCount*SalvageInfoSize // 0x2304, d_salvage.h:79

	// dSalvage_info_c (d_salvage.h:17-36), relative to the entry.
	SalvageInfoOffPos    = 0x00 // cXyz mPos
	SalvageInfoOffRoomNo = 0x2C // s8 mRoomNo: -1 not registered (checkRegist, d_salvage.cpp:259-263)
	SalvageInfoOffSaveNo = 0x2D // u8 mSaveNo
	SalvageInfoOffKind   = 0x2F // u8 mKind
	SalvageInfoOffFlag   = 0x31 // u8 mFlag

	// SalvageFlagUnusable is mFlag's bit 0x01: set, the point is not
	// salvageable now (checkUsed false), the bit proc_salvage_init sets as
	// the crane starts (d_a_salvage.cpp:376-379).
	SalvageFlagUnusable = 0x01

	// SalvageNoSave is the mSaveNo of a point with no save bit: entry
	// skips the ocean check for it (d_salvage.cpp:100-105).
	SalvageNoSave = 31

	// ProcSalvage is daSalvage_c's mProcName (fpcNm_Salvage_e,
	// f_pc_name.h:416).
	ProcSalvage int16 = 0x194
)

// SalvageInfoAddr returns the address of mInfo[i] of the table at base.
func SalvageInfoAddr(base uint32, i int) uint32 {
	return base + SalvageOffInfo + uint32(i)*SalvageInfoSize
}
