package addr

// The game's reset request (m_Do_Reset.h). main allocates a 0x10-byte
// mDoRstData from the arena and points mDoRst::mResetData at it
// (m_Do_main.cpp:561-562). A reset press (mDoRst_resetCallBack,
// m_Do_Reset.cpp:86-119), a game over's or the save menu's "quit"
// (d_gameover.cpp:116,132; d_menu_save.cpp:1269) set mReset; the scene
// machinery then tears play down (dComIfG_resetToOpening,
// d_com_inf_game.cpp:510-521) and either reboots (mDoRst_reset ->
// OSResetSystem: the logo scene, d_s_logo.cpp:501-503; a heap check that
// fails, d_s_play.cpp:1197-1198; a DVD error, f_pc_manager.cpp:250-256)
// or clears it at the opening scene (d_s_play.cpp:1444; d_s_logo.cpp:619,
// 950). A restart keeps the block (OSSetSaveRegion, m_Do_Reset.cpp:74;
// main clears it only on reset code 0, m_Do_main.cpp:569-574).
const (
	// ResetDataPtr is mDoRstData* mDoRst::mResetData (sym:18605). In
	// inject/original.dol getResetData (0x80017EF0) is lwz r3,-0x7778(r13)
	// with r13 0x803FE0E0 (__init_registers, 0x80003288-0x8000328C), and
	// main stores OSAllocFromArenaLo(0x10, 4)'s result there (0x800064C0).
	ResetDataPtr uint32 = 0x803F6968
	// ResetDataOffReset is int mReset (m_Do_Reset.h:12): mDoRst_resetCallBack
	// reads it at +0 (0x80017E50) and onReset stores 1 there (0x80017EDC).
	ResetDataOffReset = 0x00
	// ResetDataSize is sizeof(mDoRstData) (m_Do_Reset.h:11-16).
	ResetDataSize = 0x10
)
