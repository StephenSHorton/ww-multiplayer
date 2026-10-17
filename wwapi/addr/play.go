package addr

// dComIfG_play_c members (d_com_inf_game.h:720-900), as offsets from Play.
// The struct has no STATIC_ASSERT of its own; its size follows from
// dComIfG_inf_c's (PlaySize). Members past 0x4824 are commented as the
// USA/PAL layout (d_com_inf_game.h:773).
const (
	PlayOffCurStage             = 0x3E94 // dStage_startStage_c mCurStage (d_com_inf_game.h:723)
	PlayOffNextStage            = 0x3EA0 // dStage_nextStage_c mNextStage (d_com_inf_game.h:724)
	PlayOffStageData            = 0x3EB0 // dStage_stageDt_c mStageData (d_com_inf_game.h:726)
	PlayOffEvtCtrl              = 0x3F38 // dEvt_control_c mEvtCtrl (d_com_inf_game.h:728)
	PlayOffPlayerPtr            = 0x48AC // fopAc_ac_c* mpPlayerPtr[3]: Link, partner, ship (d_com_inf_game.h:791)
	PlayOffItemLifeCount        = 0x48BC // f32 (d_com_inf_game.h:793)
	PlayOffItemRupeeCount       = 0x48C0 // s32 (d_com_inf_game.h:794)
	PlayOffItemKeyNumCount      = 0x48D4 // s16 (d_com_inf_game.h:799)
	PlayOffItemMaxLifeCount     = 0x48D6 // s16 (d_com_inf_game.h:800)
	PlayOffItemMaxMagicCount    = 0x48DC // s16 (d_com_inf_game.h:803)
	PlayOffSelectItem           = 0x4933 // u8[4] (d_com_inf_game.h:830)
	PlayOffSelectEquip          = 0x4937 // u8[4] (d_com_inf_game.h:831)
	PlayOffPlayerInfoBufStageNo = 0x4A20 // u8 (d_com_inf_game.h:882)
	PlayOffMiniGameType         = 0x4A3A // u8 (d_com_inf_game.h:886)
)

// dStage_startStage_c (d_stage.h:949-952); dStage_nextStage_c extends it
// with mEnable and mWipe (d_stage.h:966-968).
const (
	StageOffName   = 0x0 // char[8]
	StageOffPoint  = 0x8 // s16
	StageOffRoomNo = 0xA // s8
	StageOffLayer  = 0xB // s8
	StageNameLen   = 8
	StageSize      = 0xC
	NextOffEnable  = 0xC // s8
	NextOffWipe    = 0xD // s8
	NextStageSize  = 0xE
)

// The loaded stage's STAG chunk (stage_stag_info_class, d_stage.h:66-77).
const (
	StageDtOffStag  = 0x48 // mStageData.mpStagInfo (d_stage.h:586)
	StagOffProp     = 0x09 // u8 mProp (d_stage.h:70)
	StagPropKeyDisp = 0x01 // the small-key HUD is shown (dStage_stagInfo_ChkKeyDisp, d_stage.h:1023-1025)
)

// StagSaveTable is the save table a STAG mProp selects
// (dStage_stagInfo_GetSaveTbl, d_stage.h:1027-1029).
func StagSaveTable(prop byte) int { return int(prop>>1) & 0x7F }

// dEvt_control_c (d_event.h:151-154). mMode is dEvtMode_NONE_e (0) while
// no event runs (runCheck, d_event.h:133-139; enum at d_event.h:25).
const (
	EvtOffOrderCnt = 0xC0 // s8 mOrderCount
	EvtOffMode     = 0xC2 // u8 mMode
)

// Stage and gate fields (Play + PlayOff* + member offset).
const (
	// CurStage is the loaded stage: name[8], point s16, room s8, layer s8.
	CurStage       uint32 = 0x803C9D3C
	CurStageName   uint32 = 0x803C9D3C
	CurStagePoint  uint32 = 0x803C9D44
	CurStageRoomNo uint32 = 0x803C9D46
	CurStageLayer  uint32 = 0x803C9D47

	// NextStage is the pending stage change; mEnable is 1 from the moment
	// a loading zone or dComIfGp_setNextStage fires until the scene
	// changes. §3.2 STABLE needs it 0 (inferred).
	NextStage       uint32 = 0x803C9D48
	NextStageEnable uint32 = 0x803C9D54

	// StagInfoPtr is mStageData.mpStagInfo, a pointer to the loaded
	// stage's STAG chunk.
	StagInfoPtr uint32 = 0x803C9DA0

	// EvtOrderCount and EvtMode are mEvtCtrl.mOrderCount and .mMode; both
	// 0 when no event (cutscene, talk, item get) is running or queued.
	// The offsets are inferred: dEvt_control_c has no STATIC_ASSERT.
	EvtOrderCount uint32 = 0x803C9EA0
	EvtMode       uint32 = 0x803C9EA2

	// PlayerPtr is mpPlayerPtr[0], Link's actor; 0 on the title and file
	// select screens. Same address as dolphin.PlayerPtrAddr.
	PlayerPtr uint32 = 0x803CA754

	// The HUD's pending counters: d_meter applies and animates a nonzero
	// count, then zeroes it (dMeter_keyMove, d_meter.cpp:5242-5257; life,
	// d_meter.cpp:1420-1452). Go writes one only while it reads 0 (§3.2
	// IDLE).
	ItemLifeCount     uint32 = 0x803CA764 // f32
	ItemRupeeCount    uint32 = 0x803CA768 // s32
	ItemKeyNumCount   uint32 = 0x803CA77C // s16
	ItemMaxLifeCount  uint32 = 0x803CA77E // s16, quarter hearts
	ItemMaxMagicCount uint32 = 0x803CA784 // s16

	// PlaySelectItem and PlaySelectEquip are the HUD's copies of the button
	// items, refreshed from the save by dComIfGp_setSelectItem
	// (d_com_inf_game.h:2924-2938).
	PlaySelectItem  uint32 = 0x803CA7DB
	PlaySelectEquip uint32 = 0x803CA7DF

	// PlayerInfoBufStageNo is nonzero while a Ganon's Tower boss-refight
	// snapshot of Link's status is held: set 1-4 when one is taken
	// (d_com_inf_game.cpp:1448-1457), cleared on restore (:1580-1581).
	PlayerInfoBufStageNo uint32 = 0x803CA8C8

	// MiniGameType is nonzero during a minigame.
	MiniGameType uint32 = 0x803CA8E2
)

// Stage names the gates special-case (§3.2 STABLE).
const (
	StageTitle      = "sea_T" // the title screen (dComIfG_changeOpeningScene, d_com_inf_game.cpp:496)
	StageFileSelect = "Name"  // file select and naming (dScnName_c::create, d_s_name.cpp:111)
)

// dStage_roomControl_c statics: per-room status and the current room.
const (
	// RoomStatus is dStage_roomControl_c::mStatus[64] (sym:17429,
	// d_stage.h:926), one dStage_roomStatus_c (STATIC_ASSERT 0x114 at
	// d_stage.h:871) per room.
	RoomStatus             uint32 = 0x803BDC88
	RoomStatusSize                = 0x4500 // sym:17429
	RoomStatusStride              = 0x114
	RoomCount                     = 64    // onSwitch asserts 0 <= room < 64 (d_save.cpp:1553)
	RoomStatusOffZoneCount        = 0x106 // s8 mZoneCount (d_stage.h:861)
	RoomStatusOffZoneNo           = 0x107 // s8 mZoneNo: index into mZone, or -1 (d_stage.h:862)

	// StayNo is dStage_roomControl_c::mStayNo, the room Link is in
	// (sym:18667, d_stage.h:930).
	StayNo uint32 = 0x803F6A78

	// MenuPause is dMenu_pause, nonzero while the pause menu is up
	// (sym:19130).
	MenuPause uint32 = 0x803F7097
)

// The Game Boy Advance link (the Tingle Tuner, §6.2). Every Tingle Tuner
// switch write needs mDoGaC_GbaLink true on the frame it is made
// (internal/coop/tables/switches, AGBSW0), and mDoGaC_GbaLink reads only
// g_mDoGaC_gbaCom (m_Do_gba_com.cpp:258-270):
//
//	(mEnable == 1 && field_0x2 == 3 && field_0x4 == 0) ||
//	(field_0x128[0].field_0x0 == 1 && field_0x10e < 3)
//
// the second clause only in versions after the demo (VERSION > VERSION_DEMO,
// global.h:63), which GZLE01 is.
const (
	// GbaCom is g_mDoGaC_gbaCom, an mDoGaC_agbCom_c (sym:17367; its
	// layout, m_Do_gba_com.h:67-110, with field_0x120 and field_0x124
	// present after the demo).
	GbaCom     uint32 = 0x803B904C
	GbaComSize        = 0x130 // sym:17367; TestDataManager follows at 0x803B917C (sym:17368)

	GbaComOffEnable   = 0x000 // u8 mEnable: 1 from the Tuner's upload (onEnable, d_a_agb.cpp:491) until mDoGaC_GbaReboot (m_Do_gba_com.cpp:162)
	GbaComOffState    = 0x002 // u8 field_0x2: the connect state, 3 once connected (mDoGaC_Connect, m_Do_gba_com.cpp:221-224)
	GbaComOffClosed   = 0x004 // u8 field_0x4: 1 while the link is closed (mDoGaC_ComClose :135), 0 from mDoGaC_ComRestart (:190)
	GbaComOffRetries  = 0x10E // u8 field_0x10e: probes since the link was last up, capped at 3 (:216-218, :236)
	GbaComOffContext  = 0x128 // u8 field_0x128[0].field_0x0: 1 once a GBA's context checked out (mDoGaC_ContextCheck, :518), 0 after mDoGaC_GbaReboot (:168)
	GbaComLinkReadLen = GbaComOffContext + 1
)

// The actor queue. Every fopAc_ac_c is linked into g_fopAcTg_Queue through
// its actor_tag; the walk goes node -> next and recovers the actor from
// the tag's data pointer (cTg_Create, c_tag.cpp:89-92).
const (
	// ActorQueue is g_fopAcTg_Queue, a node_list_class {head, tail, size}
	// (sym:14653; c_list.h:6-10).
	ActorQueue        uint32 = 0x80372028
	ActorQueueOffHead        = 0x0
	ActorQueueOffTail        = 0x4
	ActorQueueOffSize        = 0x8
	ActorQueueSize           = 0xC

	// A node is a create_tag_class: node_class {prev, data, next}
	// (c_node.h:5-9) then mpTagData, the actor (c_tag.h:10-14). A queued
	// node's data is &g_fopAcTg_Queue (cLs_Addition -> cNd_SetObject,
	// c_list.cpp:40; c_node.cpp:98-103); it is NULL only before insertion
	// (cTg_Create, c_tag.cpp:90).
	TagOffPrev    = 0x0
	TagOffData    = 0x4 // &g_fopAcTg_Queue once queued
	TagOffNext    = 0x8
	TagOffTagData = 0xC
	// TagOffUse is s8 mbIsUse, 1 while the tag is in a list
	// (cTg_Addition, c_tag.cpp:74-80; cTg_Create clears it, :89-92).
	TagOffUse = 0x10
	TagSize   = 0x14 // create_tag_class (c_tag.h:10-14)

	// FopAcType is g_fopAc_type; an actor's actor_type equals it
	// (fopAc_IsActor, f_op_actor.cpp:116-119; sym:18627). It is 0 until
	// the first actor's create gives it a value (fpcBs_MakeOfType,
	// f_pc_base.cpp:28-33).
	FopAcType uint32 = 0x803F69D0

	// ProcessIDCounter is fpcBs_MakeOfId's static process_id, the next
	// process ID: it starts at 1 and only counts up
	// (f_pc_base.cpp:36-40; sym:18651).
	ProcessIDCounter uint32 = 0x803F6A24

	// MaxActorNodes is where an actor walk gives up (§7.2: "stop after
	// 1024 nodes").
	MaxActorNodes = 1024
)

// fopAc_ac_c fields (f_op_actor.h:270-319, STATIC_ASSERT 0x290 at :321;
// base_process_class at +0, f_pc_base.h:13-32).
const (
	ActorSize            = 0x290
	ActorOffBsPcID       = 0x004 // fpc_ProcID mBsPcId (f_pc_base.h:15)
	ActorOffProcName     = 0x008 // s16 mProcName (f_pc_base.h:16)
	ActorOffParameters   = 0x0B0 // u32 mParameters (f_pc_base.h:30)
	ActorOffActorType    = 0x0C0 // int actor_type (f_op_actor.h:271)
	ActorOffActorTag     = 0x0C4 // create_tag_class actor_tag (f_op_actor.h:272)
	ActorOffEventCommand = 0x0F8 // u16 eventInfo.mCommand: eventInfo +0xF4 (f_op_actor.h:276), mCommand +4 (f_op_actor.h:202)
	ActorOffSetID        = 0x1BC // u16 setID; 0xFFFF for runtime spawns (f_op_actor.h:278)
	ActorOffGroup        = 0x1BE // u8 group: 2 = enemy (f_op_actor.h:279)
	ActorOffStatus       = 0x1C4 // u32 actor_status (f_op_actor.h:284)
	ActorOffHomePos      = 0x1D0 // cXyz home.pos (f_op_actor.h:287; actor_place, f_op_actor.h:212-216)
	ActorOffHomeRoomNo   = 0x1E2 // s8 home.roomNo
	ActorOffHealth       = 0x285 // s8 health (f_op_actor.h:307)
	ActorSetIDNone       = 0xFFFF

	// ActorOffInitState is s8 mInitState (f_pc_base.h:17): 2 once the
	// process is in the execute queues, the only state fpcEx_Execute
	// runs (f_pc_executor.cpp:31, set at :49); 3 as it leaves them.
	ActorOffInitState  = 0x00C
	ActorOffParentID   = 0x1CC              // fpc_ProcID parentActorID: 0xFFFFFFFF for a placed actor (f_op_actor.h:285; fopAcM_CreateAppend, f_op_actor_mng.cpp:78-91)
	ActorOffHomeAngle  = 0x1DC              // csXyz home.angle (actor_place +0xC, f_op_actor.h:214)
	ActorOffCurPos     = 0x1F8              // cXyz current.pos (f_op_actor.h:289)
	ActorOffAttnFlags  = 0x280              // u32 attention_info.flags: attention_info +0x26C, flags +0x14 (f_op_actor.h:219-223, :305)
	ActorOffMaxHealth  = 0x284              // s8 max_health (f_op_actor.h:306)
	ActorStatusCarry   = 0x00002000         // fopAcStts_CARRY_e (f_op_actor.h:32)
	ActorStatusBoss    = 0x04000000         // fopAcStts_BOSS_e (f_op_actor.h:43)
	ActorOffCarryByte  = ActorOffStatus + 2 // the big-endian byte of actor_status that holds CARRY
	ActorCarryByteMask = byte(ActorStatusCarry >> 8)
)

// enemyice (c_damagereaction.h:12-46), the member every enemy that polls
// enemy_ice (c_damagereaction.cpp:108-410) embeds at a per-class offset.
const (
	EnemyIceOffSelf        = 0x00 // fopAc_ac_c* mpActor: the actor itself once its create ran
	EnemyIceOffTimer       = 0x06 // s8 mLightShrinkTimer (c_damagereaction.h:16): nonzero runs the light-arrow death
	EnemyIceOffMode        = 0x0D // s8 mMode: 0 before enemy_ice's first call, 1 idle, 2-3 frozen (c_damagereaction.h:20)
	EnemyIceOffFreezeTimer = 0x0E // s16 mFreezeTimer (c_damagereaction.h:21)
	EnemyIceSize           = 0x10 // the fields above, read in one go
)

// ProcBoko is fpcNm_BOKO_e (f_pc_name.h:482): the weapon a Bokoblin or a
// Stalfos carries (d_a_bk.cpp:5086; d_a_st.cpp:2898).
const ProcBoko = 0x1D5

// BokoOffNowMode is a BOKO's u8 mNowMode (daBoko_c +0x2B8, d_a_boko.h:128,
// after m2B4, which the demo build lacks, :125-127). Its procWait sets
// Mode_ENEMY_CARRY_e when an enemy's fopAcM_setCarryNow takes it up, but
// leaves Mode_PLAYER_CARRY_e, which Link's grab sets (d_a_boko.cpp:382-389;
// d_a_player_grab.inc:330-339). CARRY alone is no sign that an enemy holds
// it: Link's grab sets CARRY too, and the BOKO sets it on itself while it
// flies (d_a_boko.cpp:406-410, :647-649).
const (
	BokoOffNowMode      = 0x2B8
	BokoModeEnemyCarry  = 2 // Mode_ENEMY_CARRY_e (d_a_boko.h:41)
	BokoModePlayerCarry = 3 // Mode_PLAYER_CARRY_e (d_a_boko.h:42)
)

// PlayerOffDemoType is Link's u16 mDemo.mDemoType (daPy_py_c +0x304,
// d_a_player.h:499; daPy_demo_c +0, :153): 0 (TYPE_NONE) outside a player
// demo. The offset is the decomp's struct layout, not yet read on a live
// game (inferred; docs/08 §8.2 K1).
const PlayerOffDemoType = 0x304

// RoomStatusOffFlags is dStage_roomStatus_c's u8 mFlags (d_stage.h:855-869):
// 0x01 loaded, 0x02 being created, 0x04 being deleted, 0x08 hidden
// (loadRoom, d_stage.cpp:214-240; dScnRoom_Execute, d_s_room.cpp:89-101).
const (
	RoomStatusOffFlags = 0x104
	RoomFlagLoaded     = 0x01
	RoomFlagCreating   = 0x02
	RoomFlagDeleting   = 0x04
	RoomFlagHidden     = 0x08
)

// Heaps (§4.5 gate; the heap-free probe walks the free lists).
const (
	// The JKRExpHeap pointers m_Do_ext.cpp creates (m_Do_ext.cpp:847,878,934).
	GameHeapPtr    uint32 = 0x803F6920 // sym:18588
	ZeldaHeapPtr   uint32 = 0x803F6928 // sym:18590
	ArchiveHeapPtr uint32 = 0x803F6938 // sym:18594

	// The allocation-failure counters mDoMch's error handler bumps
	// (m_Do_machine.cpp:35-40,124-130). Any increase means a heap ran out.
	GameHeapErrors    uint32 = 0x803F68F8 // sym:18577
	ZeldaHeapErrors   uint32 = 0x803F68FC // sym:18578
	ArchiveHeapErrors uint32 = 0x803F6904 // sym:18580

	// OSArenaLo is the __OSArenaLo variable (sym:18462). JKRHeap::initArena
	// moves it to the arena top at boot (JKRHeap.cpp:94), so read
	// JKRHeapCodeEnd for where the arena started: initArena sets it to the
	// arena start rounded up to 0x20 (JKRHeap.cpp:83,88). On the patched
	// ISO that is just above PatchedArenaLo (inferred).
	OSArenaLo      uint32 = 0x803F66C0
	JKRHeapCodeEnd uint32 = 0x803F773C // JKRHeap::mCodeEnd, sym:19563

	// JKRHeap (JKRHeap.h:111-113) and JKRExpHeap (JKRExpHeap.h:103-106).
	HeapOffStart       = 0x30 // u8* mStart
	HeapOffEnd         = 0x34 // u8* mEnd
	HeapOffSize        = 0x38 // u32 mSize
	ExpHeapOffFreeHead = 0x78 // CMemBlock* mHeadFreeList
	ExpHeapOffFreeTail = 0x7C
	ExpHeapOffUsedHead = 0x80
	ExpHeapOffUsedTail = 0x84
	MemBlockSize       = 0x10 // CMemBlock header, then the content (JKRExpHeap.h:28,42)
	MemBlockOffMagic   = 0x0  // u16
	MemBlockOffSize    = 0x4  // u32 content size
	MemBlockOffPrev    = 0x8
	MemBlockOffNext    = 0xC
	MemBlockMagic      = 0x484D // 'HM', set by CMemBlock::initiate (JKRExpHeap.cpp:936)
)

// DMC is the REL module control table, 0x7D8 bytes (sym:17371). Enemy code
// lives in RELs loaded at dynamic addresses (f_pc_load.cpp:29).
const DMC uint32 = 0x803B9218
