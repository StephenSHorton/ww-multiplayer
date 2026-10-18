package addr

// The resource table (dRes_control_c, d_resorce.h:40-47 and :60-80), what
// dComIfG_getObjectRes("Mo2", idx) resolves through: getResInfo walks up
// to 64 dRes_info_c entries for the one whose mArchiveName matches with
// mCount != 0, then returns its mRes[idx] (d_resorce.cpp:602-654). The
// host's animation stream reads the Mo2 entry's BCK pointers once per
// walk and streams a Moblin's mpAnm as an index into them, which every
// machine resolves back through its own table.
const (
	// MObjectInfo is g_dComIfG_gameInfo.mResControl.mObjectInfo[0]
	// (d_com_inf_game.h:918; sym:17472; MOBJECT_INFO in inject/include/
	// wwapi.h:266): 64 dRes_info_c of 0x24 bytes.
	MObjectInfo     uint32 = 0x803E0BC8
	ObjectInfoCount        = 64
	ResInfoSize            = 0x24 // d_resorce.h:47
	// dRes_info_c (d_resorce.h:40-47): char mArchiveName[14], u16 mCount,
	// mDoDvdThd_mountArchive_c* +0x10, JKRArchive* mpArchive, two heaps,
	// void** mRes.
	ResInfoOffName    = 0x00
	ResInfoNameLen    = 14
	ResInfoOffCount   = 0x0E
	ResInfoOffArchive = 0x14
	ResInfoOffRes     = 0x20
	// Mo2Arc is the Moblin's archive name as the table stores it.
	Mo2Arc = "Mo2"
	// Mo2.arc's BCK indices are contiguous (assets/GZLE01/res/Object/
	// Mo2.h:64-119): dRes_INDEX_MO2_BCK_ADOTSUKI_e 0x3E through
	// dRes_INDEX_MO2_BCK_WALK_e 0x75, 56 of them; Mo2BckNone is the index the
	// stream carries for an unresolved pointer.
	Mo2BckFirst = 0x3E
	Mo2BckLast  = 0x75
	Mo2BckCount = Mo2BckLast - Mo2BckFirst + 1
	Mo2BckNone  = 0xFF
	// The rows the REL hands a released copy back to (wait_set's own,
	// d_a_mo2.cpp:1059-1072).
	Mo2BckWait   = 0x73 // dRes_INDEX_MO2_BCK_WAIT_e
	Mo2BckKWait  = 0x5C // dRes_INDEX_MO2_BCK_KWAIT_e
	Mo2BckNBWait = 0x62 // dRes_INDEX_MO2_BCK_NBWAIT_e
)
