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

// The Bokoblin's and ChuChu's archives as the table stores them, and
// their BCK ranges (design "animation": ArcRes per kind).
const (
	// BkArc is bk_class's archive (dComIfG_resLoad "Bk", d_a_bk.cpp:4747).
	BkArc = "Bk"
	// Bk.arc's BCK indices are contiguous (assets/GZLE01/res/Object/
	// Bk.h:43-85): dRes_INDEX_BK_BCK_BK_AOMUKE_e 0x29 through
	// dRes_INDEX_BK_BCK_BK_WALK2_e 0x53, 43 of them.
	BkBckFirst = 0x29
	BkBckLast  = 0x53
	BkBckCount = BkBckLast - BkBckFirst + 1
	BkBckWait  = 0x51 // dRes_INDEX_BK_BCK_BK_WAIT_e (Bk.h:83)
	// CcArc is cc_class's archive (dComIfG_resLoad "CC", d_a_cc.cpp:2558).
	CcArc = "CC"
	// Cc.arc's BCK indices (assets/GZLE01/res/Object/Cc.h:22-36):
	// dRes_INDEX_CC_BCK_ATACK01_e 0x14 through dRes_INDEX_CC_BCK_TSTART02_e
	// 0x22, 15 of them (0x17 CC_BETA and 0x18 CC_PTCL drive other morfs;
	// their pointers are still distinct).
	CcBckFirst = 0x14
	CcBckLast  = 0x22
	CcBckCount = CcBckLast - CcBckFirst + 1
)

// The Keese's, Armos Knight's and Armos's archives and BCK ranges (allow
// batch "type_table" KI, AM, AM2 ARC and BCK_*).
const (
	// KiArc is ki_class's archive (dComIfG_resLoad "Ki", d_a_ki.cpp:1370).
	KiArc = "Ki"
	// Ki.arc's BCK indices (assets/GZLE01/res/Object/Ki.h:18-23):
	// dRes_INDEX_KI_BCK_ATTACK1_e 0x0F through dRes_INDEX_KI_BCK_WAIT1_e
	// 0x14, 6 of them; WAIT1 is the morf's create row (d_a_ki.cpp:1211).
	KiBckFirst = 0x0F
	KiBckLast  = 0x14
	KiBckCount = KiBckLast - KiBckFirst + 1
	KiBckWait  = 0x14
	// AmArc is am_class's archive (dComIfG_resLoad "AM", d_a_am.cpp:1171).
	AmArc = "AM"
	// Am.arc's BCK indices (assets/GZLE01/res/Object/Am.h:25-36):
	// dRes_INDEX_AM_BCK_BOM_NOMI_e 0x14 through dRes_INDEX_AM_BCK_SLEEP_LOOP_e
	// 0x1F, 12 of them; SLEEP_LOOP is the create row (d_a_am.cpp:498,
	// 1128), DEAD (0x1A) the death's (d_a_am.cpp:911, 1006).
	AmBckFirst = 0x14
	AmBckLast  = 0x1F
	AmBckCount = AmBckLast - AmBckFirst + 1
	AmBckWait  = 0x1F
	AmBckDead  = 0x1A
	// Am2Arc is am2_class's archive (dComIfG_resLoad "AM2",
	// d_a_am2.cpp:1294).
	Am2Arc = "AM2"
	// Am2.arc's BCK indices (assets/GZLE01/res/Object/Am2.h:11-20):
	// dRes_INDEX_AM2_BCK_BURUBURU_e 0x06 through dRes_INDEX_AM2_BCK_WAIT_e
	// 0x0F, 10 of them; WAIT is the create row (d_a_am2.cpp:1227).
	Am2BckFirst = 0x06
	Am2BckLast  = 0x0F
	Am2BckCount = Am2BckLast - Am2BckFirst + 1
	Am2BckWait  = 0x0F
)

// The Darknut's archive and body BCK range (TN design D7, rel_changes R-1
// ARC and BCK_*).
const (
	// TnArc is tn_class's archive (dComIfG_getObjectRes "Tn",
	// d_a_tn.cpp:87-113).
	TnArc = "Tn"
	// Tn.arc's body BCK indices (assets/GZLE01/res/Object/Tn.h:44-89):
	// dRes_INDEX_TN_BCK_AAT_KAITEN1_e 0x28 through
	// dRes_INDEX_TN_BCK_S_DEMO_M2_e 0x55, 46 of them. 0x56..0x67 are the
	// Y* armor BCKs (Tn.h:90-107), which only the armor morf plays. AWAIT1
	// 0x3D is the wait row (d_a_tn.cpp:2205); YKIHON1 0x64 the armor morf's
	// (design R-5); ATATE_ON1 0x3C the shield's (R-6).
	TnBckFirst      = 0x28
	TnBckLast       = 0x55
	TnBckCount      = TnBckLast - TnBckFirst + 1
	TnBckWait       = 0x3D
	TnBckTateOn     = 0x3C
	TnBckArmorKihon = 0x64
)

// The Kargaroc's and Magtail's archives and BCK ranges (enemy mirror
// batch C, enemy-mirror-batch-c.json "bb".row and "mt".row ARC and BCK_*).
const (
	// BbArc is bb_class's archive (dComIfG_getObjectRes "Bb",
	// d_a_bb.cpp:314-355).
	BbArc = "Bb"
	// Bb.arc's BCK indices (assets/GZLE01/res/Object/Bb.h:25-38):
	// dRes_INDEX_BB_BCK_ATACK01_e 0x15 through WALK 0x22. FLY01 0x18 is
	// the handback loop (WAIT 0x21 is the perched idle, batch C
	// judge_errors_resolved).
	BbBckFirst = 0x15
	BbBckLast  = 0x22
	BbBckCount = BbBckLast - BbBckFirst + 1
	BbBckWait  = 0x18
	// MtArc is mt_class's archive (res/Object/Mt.h).
	MtArc = "Mt"
	// Mt.arc's BCK indices (Mt.h:11-14): ATTACK 7, SUKI 8, TUKKOMI 9,
	// WAIT1 10; only the head morf plays them.
	MtBckFirst = 0x07
	MtBckLast  = 0x0A
	MtBckCount = MtBckLast - MtBckFirst + 1
	MtBckWait  = 0x0A
)
