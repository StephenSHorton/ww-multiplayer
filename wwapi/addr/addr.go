// Package addr is the cited table of GZLE01 (Wind Waker NTSC-U)
// addresses and struct offsets the co-op code reads and writes. Nothing
// here touches memory: it is constants plus the bit math the game itself
// uses, and addr_test.go checks every absolute address against the struct
// offsets it is built from, so a typo fails CI instead of landing a write
// in the wrong byte of somebody's save.
//
// # v0 UNSTABLE
//
// This package is part of wwapi v0. Its API may change in any 0.x minor
// version.
//
// Citations: file:line is the zeldaret/tww decomp at commit 4d78c994
// (include/ and src/; the file names are unique, so directories are left
// out). sym:N is line N of its config/GZLE01/symbols.txt. §N is a section
// of the co-op design (issue #23). "Inferred" marks a value read from code
// but not yet observed on a live Dolphin; the M0 probes exist to confirm
// those.
//
// Naming: a plain name is an absolute address (uint32). An Off* name is a
// byte offset into the 0x778-byte dSv_save_c image, the canonical image the
// class table and the co-op wire format index. A <Struct>Off* name is an
// offset inside one struct.
package addr

// MEM1 is the GameCube's 24 MiB main RAM at its cached mirror. Every
// address in this package lies inside it, and wwapi/mem refuses
// any access that doesn't.
const (
	MEM1Base uint32 = 0x80000000
	MEM1Size        = 0x01800000 // 24 MiB: the region size dolphin.scanForRAM looks for (internal/dolphin/memory.go:221)
	MEM1End  uint32 = MEM1Base + MEM1Size
)

// g_dComIfG_gameInfo and its first member, dSv_info_c save.
const (
	// GameInfo is g_dComIfG_gameInfo (dComIfG_inf_c, sym:17472). save is
	// at +0, play at +0x12A0 (d_com_inf_game.h:914-915).
	GameInfo     uint32 = 0x803C4C08
	GameInfoSize        = 0x1D1C8 // sym:17472; STATIC_ASSERT at d_com_inf_game.h:927

	// SaveInfo is gameInfo.save, a dSv_info_c (d_save.h:991-1001).
	SaveInfo     uint32 = 0x803C4C08
	SaveInfoSize        = 0x12A0 // STATIC_ASSERT at d_save.h:1005

	// SaveImage is save.mSavedata, the dSv_save_c that memory_to_card
	// writes to the card (d_save.h:908-913, 991). It is the image every
	// Off* constant indexes.
	SaveImage     uint32 = 0x803C4C08
	SaveImageSize        = 0x778 // STATIC_ASSERT at d_save.h:916

	// Play is gameInfo.play, a dComIfG_play_c (d_com_inf_game.h:915).
	Play     uint32 = 0x803C5EA8
	PlaySize        = 0x4A7C // gameInfo +0x5D1C (drawlist, d_com_inf_game.h:916) minus +0x12A0
)

// dSv_info_c members (d_save.h:991-1001), as offsets from SaveInfo.
const (
	InfoOffSavedata    = 0x0000 // dSv_save_c mSavedata
	InfoOffMemory      = 0x0778 // dSv_memory_c mMemory: the live memBit of the current table
	InfoOffDan         = 0x079C // dSv_danBit_c mDan
	InfoOffZone        = 0x07A8 // dSv_zone_c mZone[32]
	InfoOffRestart     = 0x1128 // dSv_restart_c mRestart
	InfoOffTmp         = 0x1158 // dSv_event_c mTmp
	InfoOffTurnRestart = 0x1258 // dSv_turnRestart_c mTurnRestart
	InfoOffDataNum     = 0x1290 // u8 mDataNum: the file slot loaded
)

// dSv_save_c members (d_save.h:908-913), as Off* image offsets.
const (
	OffPlayer   = 0x000 // dSv_player_c mPlayer, 0x380 bytes
	OffMemory   = 0x380 // dSv_memory_c mMemory[16], saved memBit per table
	OffOcean    = 0x5C0 // dSv_ocean_c mOcean, 0x64 bytes
	OffEvent    = 0x624 // dSv_event_c mEvent, 0x100 bytes
	OffReserve  = 0x724 // dSv_reserve_c mReserve, 0x50 bytes: the co-op binding tag (§5.2)
	OffImagePad = 0x774 // 4 bytes of trailing padding (d_save.h:913)

	PlayerSize  = 0x380 // STATIC_ASSERT at d_save.h:608
	OceanSize   = 0x64  // STATIC_ASSERT at d_save.h:703
	EventSize   = 0x100 // STATIC_ASSERT at d_save.h:719
	ReserveSize = 0x50  // d_save.h:725
)

// dSv_player_c members (d_save.h:586-605), as Off* image offsets (mPlayer
// is at image +0, so these are also offsets into dSv_player_c).
const (
	OffStatusA       = 0x000 // dSv_player_status_a_c, 0x18 (d_save.h:40-52)
	OffStatusB       = 0x018 // dSv_player_status_b_c, 0x18 (d_save.h:72-77)
	OffReturnPlace   = 0x030 // dSv_player_return_place_c, 0xC (d_save.h:92-96)
	OffItems         = 0x03C // dSv_player_item_c mItems[21] (d_save.h:117)
	OffGetItem       = 0x051 // dSv_player_get_item_c mItemFlags[21] (d_save.h:131)
	OffItemRecord    = 0x066 // dSv_player_item_record_c, 0x8 (d_save.h:164-166)
	OffItemMax       = 0x06E // dSv_player_item_max_c, 0x8 (d_save.h:194-195)
	OffBagItem       = 0x076 // dSv_player_bag_item_c, 0x18 (d_save.h:228-230)
	OffGetBagItem    = 0x090 // dSv_player_get_bag_item_c, 0xC (d_save.h:246-249); 0x8E-0x8F is padding
	OffBagItemRecord = 0x09C // dSv_player_bag_item_record_c, 0x18 (d_save.h:265-267)
	OffCollect       = 0x0B4 // dSv_player_collect_c, 0xD (d_save.h:291-296)
	OffMap           = 0x0C4 // dSv_player_map_c, 0x84 (d_save.h:432-436); 0xC1-0xC3 is padding
	OffInfo          = 0x148 // dSv_player_info_c, 0x5C (d_save.h:460-469)
	OffConfig        = 0x1A4 // dSv_player_config_c, 0x5 (d_save.h:491-495)
	OffPriest        = 0x1AC // dSv_player_priest_c, 0x10 (d_save.h:510-513); 0x1A9-0x1AB is padding
	OffStatusC       = 0x1BC // dSv_player_status_c_c mStatusC[4], 4 x 0x70 (d_save.h:522-529,604)
	OffPlayerPad     = 0x37C // 4 bytes of padding (d_save.h:605)

	StatusASize       = 0x18
	StatusBSize       = 0x18
	ReturnPlaceSize   = 0xC
	ItemsSize         = 21
	GetItemSize       = 21
	ItemRecordSize    = 0x8
	ItemMaxSize       = 0x8
	BagItemSize       = 0x18
	GetBagItemSize    = 0xC
	BagItemRecordSize = 0x18
	CollectSize       = 0xD
	MapSize           = 0x84 // STATIC_ASSERT at d_save.h:439
	InfoSize          = 0x5C // STATIC_ASSERT at d_save.h:472
	ConfigSize        = 0x5
	PriestSize        = 0x10
	StatusCSize       = 0x70 // STATIC_ASSERT at d_save.h:531
	StatusCCount      = 4    // PLAYER_STATUS_C_COUNT, d_save.h:584
)

// Fields inside the player structs, as Off* image offsets.
const (
	// dSv_player_status_a_c (d_save.h:40-52). Life and magic counts are
	// per player and never written: mLife = 0 kills Link (§5.1).
	OffMaxLife     = OffStatusA + 0x00 // u16, quarter hearts
	OffLife        = OffStatusA + 0x02 // u16, quarter hearts
	OffRupee       = OffStatusA + 0x04 // u16
	OffSelectItem  = OffStatusA + 0x09 // u8[5]: X, Y, Z button inventory slots
	OffSelectEquip = OffStatusA + 0x0E // u8[4]: sword, shield, bracelet, ?
	OffWalletSize  = OffStatusA + 0x12 // u8: 0, 1, 2 = 200, 1000, 5000 (d_save.h:20-30)
	OffMaxMagic    = OffStatusA + 0x13 // u8
	OffMagic       = OffStatusA + 0x14 // u8
	OffStatusAPad  = OffStatusA + 0x17 // 1 byte of padding (d_save.h:52)

	// dSv_player_status_b_c (d_save.h:72-77).
	OffDateIPL    = OffStatusB + 0x00 // u64: zeroed by init (d_save.cpp:56), stamped only into the card buffer (d_save.cpp:1717)
	OffStatusBPad = OffStatusB + 0x16 // 2 bytes of padding (d_save.h:78)

	// dSv_player_item_max_c (d_save.h:179-195).
	OffArrowMax = OffItemMax + 0x01 // u8 mArrowNum
	OffBombMax  = OffItemMax + 0x02 // u8 mBombNum

	// dSv_player_get_bag_item_c (d_save.h:246-249).
	OffReserveFlags = OffGetBagItem + 0x0 // u32: delivery-bag flags
	OffBeastFlags   = OffGetBagItem + 0x4 // u8
	OffBaitFlags    = OffGetBagItem + 0x5 // u8

	// dSv_player_collect_c (d_save.h:291-296). mCollect[0..4] are sword,
	// shield, bracelet and the two charms (dComIfGs_setSelectEquip,
	// d_com_inf_game.cpp:1786-1822; restored as a set at :1541-1545).
	OffCollectFlags = OffCollect + 0x0 // u8[8]
	OffTact         = OffCollect + 0x9 // u8: songs
	OffTriforce     = OffCollect + 0xA // u8: Triforce shards
	OffSymbol       = OffCollect + 0xB // u8: pearls

	// dSv_player_map_c (d_save.h:432-436). field_0x0 is u32[4][4]: row 0
	// is only zeroed (d_save.cpp:828-833), rows 1-3 are the get, open and
	// complete chart bits (d_save.cpp:852-897).
	OffMapRow0     = OffMap + 0x00 // u32[4], unused
	OffMapGet      = OffMap + 0x10 // u32[4]: charts owned
	OffMapOpen     = OffMap + 0x20 // u32[4]: charts deciphered
	OffMapComplete = OffMap + 0x30 // u32[4]: charts salvaged
	OffFmapBits    = OffMap + 0x40 // u8[49]: sea-map bits per island (d_save.cpp:930-933)
	OffMap71       = OffMap + 0x71 // u8[16], only zeroed (d_save.cpp:846-848)
	OffMapTriforce = OffMap + 0x81 // u8: Triforce charts (d_save.cpp:900-903)
	OffMapPad      = OffMap + 0x82 // u8[2] (d_save.h:436)

	// dSv_player_info_c (d_save.h:460-469).
	OffPlayerName = OffInfo + 0x14 // char[17]
	OffClearCount = OffInfo + 0x58 // u8: second-quest count; tag rule V3 (§5.2)

	OffConfigPad = OffConfig + ConfigSize // 3 bytes of padding before mPriest
)

// Inventory slots of mItems (d_com_inf_game.h:1072-1095).
const (
	SlotBow         = 12
	SlotBottle0     = 14 // bottles are slots 14-17
	SlotBottleCount = 4
	SlotDeliveryBag = 18
	SlotCount       = 21
)

// Absolute addresses of image fields (SaveImage + Off*).
const (
	MaxLife     uint32 = 0x803C4C08
	Life        uint32 = 0x803C4C0A
	Rupee       uint32 = 0x803C4C0C
	SelectItem  uint32 = 0x803C4C11
	SelectEquip uint32 = 0x803C4C16
	WalletSize  uint32 = 0x803C4C1A
	MaxMagic    uint32 = 0x803C4C1B
	Magic       uint32 = 0x803C4C1C
	DateIPL     uint32 = 0x803C4C20
	ReturnPlace uint32 = 0x803C4C38
	Items       uint32 = 0x803C4C44
	GetItem     uint32 = 0x803C4C59
	ItemMax     uint32 = 0x803C4C76
	ArrowMax    uint32 = 0x803C4C77
	BombMax     uint32 = 0x803C4C78
	GetBagItem  uint32 = 0x803C4C98
	BeastFlags  uint32 = 0x803C4C9C
	BaitFlags   uint32 = 0x803C4C9D
	Collect     uint32 = 0x803C4CBC
	Tact        uint32 = 0x803C4CC5
	Triforce    uint32 = 0x803C4CC6
	Symbol      uint32 = 0x803C4CC7
	Map         uint32 = 0x803C4CCC
	MapGet      uint32 = 0x803C4CDC
	MapOpen     uint32 = 0x803C4CEC
	MapComplete uint32 = 0x803C4CFC
	FmapBits    uint32 = 0x803C4D0C
	MapTriforce uint32 = 0x803C4D4D
	PlayerName  uint32 = 0x803C4D64
	ClearCount  uint32 = 0x803C4DA8
	SavedMemBit uint32 = 0x803C4F88 // mMemory[0]; table t is at +0x24*t (SavedMemBitAddr)
	Ocean       uint32 = 0x803C51C8
	Event       uint32 = 0x803C522C
	Reserve     uint32 = 0x803C532C
)

// dSv_info_c fields past the image (SaveInfo + InfoOff*).
const (
	// LiveMemBit is save.mMemory: the current table's memBit. Stage load
	// copies mMemory[t] in (getSave, d_save.cpp:1506-1509, called from
	// d_stage.cpp:1675) and dStage_Delete copies it back (putSave,
	// d_save.cpp:1511-1515; d_stage.cpp:2256), so writes to the current
	// table go here as well as to the saved slot (§3.1).
	LiveMemBit      uint32 = 0x803C5380
	LiveKeyNum      uint32 = 0x803C53A0 // LiveMemBit + MemBitOffKeyNum
	LiveDungeonItem uint32 = 0x803C53A1 // LiveMemBit + MemBitOffDungeonItem

	// Dan is save.mDan (dSv_danBit_c, d_save.h:752-754).
	Dan        uint32 = 0x803C53A4
	DanStageNo uint32 = 0x803C53A4 // s8: the table initDan last ran for; -1 after dSv_info_c::init (d_save.cpp:1392)
	DanSwitch  uint32 = 0x803C53A8 // u32[2]: switches 0x80-0xBF

	// Zones is save.mZone[0] (dSv_zone_c, d_save.h:795-797); zone z is at
	// +0x4C*z (ZoneAddr).
	Zones uint32 = 0x803C53B0

	Restart     uint32 = 0x803C5D30 // dSv_restart_c, 0x30 (d_save.h:826-837)
	Tmp         uint32 = 0x803C5D60 // dSv_event_c mTmp, 0x100
	TurnRestart uint32 = 0x803C5E60 // dSv_turnRestart_c, 0x38 (d_save.h:860-870)
	DataNum     uint32 = 0x803C5E98 // u8 file slot; tag rule V2 (§5.2)

	RestartSize     = 0x30
	TmpSize         = 0x100
	TurnRestartSize = 0x38
)

// Save tables: the index t of mMemory[t] (dSv_save_c::SaveStageTbl,
// d_save.h:888-906). A stage's table is (stagInfo.mProp >> 1) & 0x7F
// (dStage_stagInfo_GetSaveTbl, d_stage.h:1027-1029).
const (
	TableSea        = 0x00
	TableSea2       = 0x01
	TableFF         = 0x02 // Forsaken Fortress
	TableDRC        = 0x03 // Dragon Roost Cavern
	TableFW         = 0x04 // Forbidden Woods
	TableTotG       = 0x05 // Tower of the Gods
	TableET         = 0x06 // Earth Temple
	TableWT         = 0x07 // Wind Temple
	TableGT         = 0x08 // Ganon's Tower
	TableHyrule     = 0x09
	TableShip       = 0x0A
	TableMisc       = 0x0B // mostly interiors
	TableSubdungeon = 0x0C
	TableSubNew     = 0x0D
	TableBlueChu    = 0x0E
	TableTest       = 0x0F
	TableCount      = 0x10 // STAGE_MAX; getSave asserts 0 <= t < 16 (d_save.cpp:1507)
)

// dSv_memBit_c (d_save.h:684-689), one per table, saved and live.
const (
	MemBitSize           = 0x24 // STATIC_ASSERT at d_save.h:692
	MemBitOffTbox        = 0x00 // u32: chests opened, bits 0-31
	MemBitOffSwitch      = 0x04 // u32[4]: memory switches 0x00-0x7F
	MemBitOffItem        = 0x14 // u32[1]: item pickups (see MemBitItemBit)
	MemBitOffVisited     = 0x18 // u32[2]: visited rooms 0-63
	MemBitOffKeyNum      = 0x20 // u8: small keys
	MemBitOffDungeonItem = 0x21 // u8: DungeonItem* bits
	MemBitOffPad         = 0x22 // 2 bytes of padding

	MemBitSwitchCount = 0x80 // onSwitch asserts 0 <= no < 128 (d_save.cpp:1085)
	MemBitTboxCount   = 32   // d_save.cpp:1073
	MemBitItemCount   = 64   // d_save.cpp:1113
	MemBitRoomCount   = 64   // d_save.cpp:1125
)

// mDungeonItem bits (dSv_memBit_c enum, d_save.h:639-646; onDungeonItem
// asserts bit < 6, d_save.cpp:1137).
const (
	DungeonItemMap            = 0
	DungeonItemCompass        = 1
	DungeonItemBossKey        = 2
	DungeonItemStageBossEnemy = 3 // boss defeated
	DungeonItemStageLife      = 4 // heart container taken
	DungeonItemStageBossDemo  = 5
	DungeonItemCount          = 6
	DungeonItemMask           = 1<<DungeonItemCount - 1
)

// dSv_danBit_c (d_save.h:752-754).
const (
	DanSize           = 0xC
	DanOffStageNo     = 0x0
	DanOffGbaRupee    = 0x1
	DanOffSwitch      = 0x4 // u32[2]
	DanSwitchCount    = 0x40
	DanStageNoInvalid = 0xFF // s8 -1 as stored
)

// dSv_zone_c (d_save.h:795-797), 32 of them per stage. Zones are allocated
// first-free on each machine (createZone, d_save.cpp:1525-1537), so a zone
// slot means nothing across players; key zone state by (stage, room).
const (
	ZoneSize        = 0x4C
	ZoneCount       = 0x20 // ZONE_MAX, d_save.h:989
	ZoneOffRoomNo   = 0x00 // s8: -1 when free (d_save.h:787,793)
	ZoneOffSwitch   = 0x02 // u16[3]: switches 0xC0-0xEF (dSv_zoneBit_c, d_save.h:770)
	ZoneOffItem     = 0x08 // u16 (d_save.h:771)
	ZoneOffActor    = 0x0C // u32[16]: dead actor setIDs (dSv_zoneActor_c, d_save.h:782)
	ZoneSwitchCount = 0x30 // SWITCH_MAX, d_save.h:768
	ZoneActorCount  = 512  // ACTOR_MAX, d_save.h:780
)

// dSv_ocean_c (d_save.h:700): u16 per sea grid; onOceanSvBit asserts grid
// <= 0x31 and bit < 16 (d_save.cpp:1164-1167).
const (
	OceanGridCount = 50
)

// dSv_event_c (d_save.h:716). An event bit id 0xHHLL is byte HH, mask LL
// (onEventBit, d_save.cpp:1187-1189). A register id 0xHHMM is byte HH,
// field mask MM, written mask-and-set (setEventReg, d_save.cpp:1202-1205).
const (
	EventBitBytes   = 0x42 // bytes 0x00-0x41 hold the 487 named bits (d_save_event_flag.inc:6-494)
	EventUnnamedLo  = 0x42 // bytes 0x42-0x78 have no named bit or register
	EventRegFirst   = 0x79 // bytes 0x79-0xFF hold the 131 named registers (d_save_event_flag.inc:497-629)
	EventNamedBits  = 487
	EventNamedRegs  = 131
	ReserveTagBytes = 0x30 // the binding tag uses 0x30 of mReserve's 0x50 bytes (§5.2)
)
