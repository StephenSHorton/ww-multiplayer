package addr

import (
	"testing"
)

// rel is one arithmetic relation: an absolute address written as a literal
// must equal the base plus the struct offsets it is made of.
type rel struct {
	name      string
	got, want uint32
}

func checkRels(t *testing.T, rels []rel) {
	t.Helper()
	for _, r := range rels {
		if r.got != r.want {
			t.Errorf("%s = 0x%08X, want 0x%08X", r.name, r.got, r.want)
		}
	}
}

// TestGameInfoLayout ties the gameInfo, save and play bases together, and
// to the relations the co-op design states (§3, §4.2).
func TestGameInfoLayout(t *testing.T) {
	checkRels(t, []rel{
		{"SaveInfo", SaveInfo, GameInfo},
		{"SaveImage", SaveImage, SaveInfo + InfoOffSavedata},
		{"Play", Play, GameInfo + SaveInfoSize},
		{"Play (design: gameInfo + 0x12A0)", Play, GameInfo + 0x12A0},
		{"LiveMemBit", LiveMemBit, SaveInfo + InfoOffMemory},
		{"LiveMemBit (design: gameInfo + 0x778)", LiveMemBit, GameInfo + 0x778},
		{"Reserve (design: gameInfo + 0x724)", Reserve, GameInfo + 0x724},
		{"CurStage (design: play + 0x3E94)", CurStage, Play + 0x3E94},
		{"Dan", Dan, SaveInfo + InfoOffDan},
		{"Zones", Zones, SaveInfo + InfoOffZone},
		{"Restart", Restart, SaveInfo + InfoOffRestart},
		{"Tmp", Tmp, SaveInfo + InfoOffTmp},
		{"TurnRestart", TurnRestart, SaveInfo + InfoOffTurnRestart},
		{"DataNum", DataNum, SaveInfo + InfoOffDataNum},
	})
	// play ends where drawlist starts (d_com_inf_game.h:916).
	if SaveInfoSize+PlaySize != 0x5D1C || SaveInfoSize+PlaySize > GameInfoSize {
		t.Errorf("save + play = 0x%X, want drawlist at +0x5D1C", SaveInfoSize+PlaySize)
	}
}

// TestImageFields checks every absolute image field against SaveImage +
// its Off* constant.
func TestImageFields(t *testing.T) {
	img := func(off int) uint32 { return SaveImage + uint32(off) }
	checkRels(t, []rel{
		{"MaxLife", MaxLife, img(OffMaxLife)},
		{"Life", Life, img(OffLife)},
		{"Rupee", Rupee, img(OffRupee)},
		{"SelectItem", SelectItem, img(OffSelectItem)},
		{"SelectEquip", SelectEquip, img(OffSelectEquip)},
		{"WalletSize", WalletSize, img(OffWalletSize)},
		{"MaxMagic", MaxMagic, img(OffMaxMagic)},
		{"Magic", Magic, img(OffMagic)},
		{"DateIPL", DateIPL, img(OffDateIPL)},
		{"ReturnPlace", ReturnPlace, img(OffReturnPlace)},
		{"Items", Items, img(OffItems)},
		{"GetItem", GetItem, img(OffGetItem)},
		{"ItemMax", ItemMax, img(OffItemMax)},
		{"ArrowMax", ArrowMax, img(OffArrowMax)},
		{"BombMax", BombMax, img(OffBombMax)},
		{"GetBagItem", GetBagItem, img(OffGetBagItem)},
		{"BeastFlags", BeastFlags, img(OffBeastFlags)},
		{"BaitFlags", BaitFlags, img(OffBaitFlags)},
		{"Collect", Collect, img(OffCollect)},
		{"Tact", Tact, img(OffTact)},
		{"Triforce", Triforce, img(OffTriforce)},
		{"Symbol", Symbol, img(OffSymbol)},
		{"Map", Map, img(OffMap)},
		{"MapGet", MapGet, img(OffMapGet)},
		{"MapOpen", MapOpen, img(OffMapOpen)},
		{"MapComplete", MapComplete, img(OffMapComplete)},
		{"FmapBits", FmapBits, img(OffFmapBits)},
		{"MapTriforce", MapTriforce, img(OffMapTriforce)},
		{"PlayerName", PlayerName, img(OffPlayerName)},
		{"ClearCount", ClearCount, img(OffClearCount)},
		{"SavedMemBit", SavedMemBit, img(OffMemory)},
		{"Ocean", Ocean, img(OffOcean)},
		{"Event", Event, img(OffEvent)},
		{"Reserve", Reserve, img(OffReserve)},
		{"LiveKeyNum", LiveKeyNum, LiveMemBit + MemBitOffKeyNum},
		{"LiveDungeonItem", LiveDungeonItem, LiveMemBit + MemBitOffDungeonItem},
		{"DanStageNo", DanStageNo, Dan + DanOffStageNo},
		{"DanSwitch", DanSwitch, Dan + DanOffSwitch},
	})
}

// TestPlayFields checks the play-side gate and counter addresses against
// Play + member offsets.
func TestPlayFields(t *testing.T) {
	checkRels(t, []rel{
		{"CurStage", CurStage, Play + PlayOffCurStage},
		{"CurStageName", CurStageName, CurStage + StageOffName},
		{"CurStagePoint", CurStagePoint, CurStage + StageOffPoint},
		{"CurStageRoomNo", CurStageRoomNo, CurStage + StageOffRoomNo},
		{"CurStageLayer", CurStageLayer, CurStage + StageOffLayer},
		{"NextStage", NextStage, Play + PlayOffNextStage},
		{"NextStageEnable", NextStageEnable, NextStage + NextOffEnable},
		{"StagInfoPtr", StagInfoPtr, Play + PlayOffStageData + StageDtOffStag},
		{"EvtOrderCount", EvtOrderCount, Play + PlayOffEvtCtrl + EvtOffOrderCnt},
		{"EvtMode", EvtMode, Play + PlayOffEvtCtrl + EvtOffMode},
		{"PlayerPtr", PlayerPtr, Play + PlayOffPlayerPtr},
		{"ItemLifeCount", ItemLifeCount, Play + PlayOffItemLifeCount},
		{"ItemRupeeCount", ItemRupeeCount, Play + PlayOffItemRupeeCount},
		{"ItemKeyNumCount", ItemKeyNumCount, Play + PlayOffItemKeyNumCount},
		{"ItemMaxLifeCount", ItemMaxLifeCount, Play + PlayOffItemMaxLifeCount},
		{"ItemMaxMagicCount", ItemMaxMagicCount, Play + PlayOffItemMaxMagicCount},
		{"PlaySelectItem", PlaySelectItem, Play + PlayOffSelectItem},
		{"PlaySelectEquip", PlaySelectEquip, Play + PlayOffSelectEquip},
		{"PlayerInfoBufStageNo", PlayerInfoBufStageNo, Play + PlayOffPlayerInfoBufStageNo},
		{"MiniGameType", MiniGameType, Play + PlayOffMiniGameType},
	})
	for _, off := range []int{PlayOffCurStage, PlayOffNextStage, PlayOffStageData, PlayOffEvtCtrl,
		PlayOffMiniGameType} {
		if off >= PlaySize {
			t.Errorf("play offset 0x%X is past PlaySize 0x%X", off, PlaySize)
		}
	}
	// mpPlayerPtr is 3 pointers, then f32 mItemScopeWipeScale, then
	// mItemLifeCount (d_com_inf_game.h:791-793).
	if PlayOffItemLifeCount != PlayOffPlayerPtr+3*4+4 {
		t.Errorf("mItemLifeCount at +0x%X, want mpPlayerPtr[3] + f32", PlayOffItemLifeCount)
	}
	// mItemKeyNumCount .. mItemMaxMagicCount are consecutive s16s
	// (d_com_inf_game.h:799-803).
	if PlayOffItemMaxLifeCount != PlayOffItemKeyNumCount+2 || PlayOffItemMaxMagicCount != PlayOffItemKeyNumCount+8 {
		t.Error("pending s16 counters are not consecutive")
	}
	if PlayOffSelectEquip != PlayOffSelectItem+4 {
		t.Error("play mSelectEquip does not follow mSelectItem[4]")
	}
}

// TestStructChains walks each struct member by member: every member must
// start where the previous one (plus its documented padding) ends, and the
// last must end at the STATIC_ASSERTed size.
func TestStructChains(t *testing.T) {
	type link struct {
		name     string
		off, end int // member start; where it ends (next start minus padding)
	}
	chains := map[string]struct {
		links []link
		size  int
	}{
		"dSv_player_c": {[]link{
			{"status_a", OffStatusA, OffStatusA + StatusASize},
			{"status_b", OffStatusB, OffStatusB + StatusBSize},
			{"return_place", OffReturnPlace, OffReturnPlace + ReturnPlaceSize},
			{"item", OffItems, OffItems + ItemsSize},
			{"get_item", OffGetItem, OffGetItem + GetItemSize},
			{"item_record", OffItemRecord, OffItemRecord + ItemRecordSize},
			{"item_max", OffItemMax, OffItemMax + ItemMaxSize},
			{"bag_item + 2 pad", OffBagItem, OffBagItem + BagItemSize + 2},
			{"get_bag_item", OffGetBagItem, OffGetBagItem + GetBagItemSize},
			{"bag_item_record", OffBagItemRecord, OffBagItemRecord + BagItemRecordSize},
			{"collect + 3 pad", OffCollect, OffCollect + CollectSize + 3},
			{"map", OffMap, OffMap + MapSize},
			{"info", OffInfo, OffInfo + InfoSize},
			{"config + 3 pad", OffConfig, OffConfigPad + 3},
			{"priest", OffPriest, OffPriest + PriestSize},
			{"status_c[4]", OffStatusC, OffStatusC + StatusCCount*StatusCSize},
			{"pad", OffPlayerPad, OffPlayerPad + 4},
		}, PlayerSize},
		"dSv_save_c": {[]link{
			{"player", OffPlayer, OffPlayer + PlayerSize},
			{"memory[16]", OffMemory, OffMemory + TableCount*MemBitSize},
			{"ocean", OffOcean, OffOcean + OceanSize},
			{"event", OffEvent, OffEvent + EventSize},
			{"reserve", OffReserve, OffReserve + ReserveSize},
			{"pad", OffImagePad, OffImagePad + 4},
		}, SaveImageSize},
		"dSv_info_c": {[]link{
			{"savedata", InfoOffSavedata, InfoOffSavedata + SaveImageSize},
			{"memory", InfoOffMemory, InfoOffMemory + MemBitSize},
			{"dan", InfoOffDan, InfoOffDan + DanSize},
			{"zone[32]", InfoOffZone, InfoOffZone + ZoneCount*ZoneSize},
			{"restart", InfoOffRestart, InfoOffRestart + RestartSize},
			{"tmp", InfoOffTmp, InfoOffTmp + TmpSize},
			{"turn_restart", InfoOffTurnRestart, InfoOffTurnRestart + TurnRestartSize},
			// mDataNum, mNewFile, mNoFile, pad, u64 mMemCardCheckID at 0x1298.
			{"data_num .. check_id", InfoOffDataNum, 0x1298 + 8},
		}, SaveInfoSize},
		"dSv_memBit_c": {[]link{
			{"tbox", MemBitOffTbox, MemBitOffTbox + 4},
			{"switch[4]", MemBitOffSwitch, MemBitOffSwitch + 16},
			{"item[1]", MemBitOffItem, MemBitOffItem + 4},
			{"visited[2]", MemBitOffVisited, MemBitOffVisited + 8},
			{"key_num", MemBitOffKeyNum, MemBitOffKeyNum + 1},
			{"dungeon_item", MemBitOffDungeonItem, MemBitOffDungeonItem + 1},
			{"pad", MemBitOffPad, MemBitOffPad + 2},
		}, MemBitSize},
		"dSv_zone_c": {[]link{
			{"room_no + 1 pad", ZoneOffRoomNo, ZoneOffRoomNo + 2},
			{"switch[3]", ZoneOffSwitch, ZoneOffSwitch + 6},
			{"item + 2 pad", ZoneOffItem, ZoneOffItem + 4},
			{"actor[16]", ZoneOffActor, ZoneOffActor + ZoneActorCount/8},
		}, ZoneSize},
		"dSv_danBit_c": {[]link{
			{"stage_no", DanOffStageNo, DanOffStageNo + 1},
			{"gba_rupee + 2 pad", DanOffGbaRupee, DanOffGbaRupee + 3},
			{"switch[2]", DanOffSwitch, DanOffSwitch + DanSwitchCount/8},
		}, DanSize},
		"dSv_ocean_c": {[]link{{"grids", 0, 2 * OceanGridCount}}, OceanSize},
	}
	for name, c := range chains {
		at := 0
		for _, l := range c.links {
			if l.off != at {
				t.Errorf("%s.%s starts at 0x%X, want 0x%X", name, l.name, l.off, at)
			}
			at = l.end
		}
		if at != c.size {
			t.Errorf("%s ends at 0x%X, want size 0x%X", name, at, c.size)
		}
	}

	// Play members that sit back to back (d_com_inf_game.h:723-726).
	if PlayOffNextStage != PlayOffCurStage+StageSize || PlayOffStageData != PlayOffNextStage+NextStageSize+2 {
		t.Error("mCurStage / mNextStage / mStageData offsets don't chain")
	}
	if RoomStatusSize != RoomCount*RoomStatusStride {
		t.Errorf("mStatus is 0x%X bytes, want 64 x 0x114", RoomStatusSize)
	}
	if SwitchEnd != MemBitSwitchCount+DanSwitchCount+ZoneSwitchCount {
		t.Errorf("SwitchEnd 0x%X != memory + dan + zone switch counts", SwitchEnd)
	}
}

// TestDesignTableAddresses spot-checks the per-table and per-room formulas
// the design writes out by hand (§3, §6.4, Appendix A).
func TestDesignTableAddresses(t *testing.T) {
	for tb := 0; tb < TableCount; tb++ {
		a, err := SavedMemBitAddr(tb)
		if err != nil {
			t.Fatal(err)
		}
		if a != 0x803C4F88+0x24*uint32(tb) {
			t.Errorf("SavedMemBitAddr(%d) = 0x%08X", tb, a)
		}
		// §6.4: the saved key byte is 0x803C4FA8 + 0x24*t.
		if a+MemBitOffKeyNum != 0x803C4FA8+0x24*uint32(tb) {
			t.Errorf("saved keyNum for table %d = 0x%08X", tb, a+MemBitOffKeyNum)
		}
		if SaveImage+uint32(OffMemBit(tb)) != a {
			t.Errorf("OffMemBit(%d) disagrees with SavedMemBitAddr", tb)
		}
	}
	for z := 0; z < ZoneCount; z++ {
		a, err := ZoneAddr(z)
		if err != nil {
			t.Fatal(err)
		}
		// §3: zone switches at 0x803C53B0+0x4C*z+2, actor bits at 0x803C53BC+0x4C*z.
		if a+ZoneOffSwitch != 0x803C53B0+0x4C*uint32(z)+2 || a+ZoneOffActor != 0x803C53BC+0x4C*uint32(z) {
			t.Errorf("zone %d fields at 0x%08X", z, a)
		}
	}
	for room := 0; room < RoomCount; room++ {
		a, err := ZoneNoAddr(room)
		if err != nil {
			t.Fatal(err)
		}
		if a != 0x803BDC88+0x114*uint32(room)+0x107 {
			t.Errorf("ZoneNoAddr(%d) = 0x%08X", room, a)
		}
	}
	if Zones+ZoneCount*ZoneSize != Restart {
		t.Errorf("mZone[32] ends at 0x%08X, want mRestart 0x%08X", Zones+ZoneCount*ZoneSize, Restart)
	}
	// §3: event registers span 0x803C52A5..0x803C532B and the unnamed
	// bytes 0x803C526E..0x803C52A4.
	if Event+EventRegFirst != 0x803C52A5 || Event+EventSize-1 != 0x803C532B {
		t.Error("event register span disagrees with the design")
	}
	if Event+EventUnnamedLo != 0x803C526E || Event+EventRegFirst-1 != 0x803C52A4 {
		t.Error("unnamed event span disagrees with the design")
	}
	if EventUnnamedLo != EventBitBytes {
		t.Error("the unnamed event bytes must start right after the bit bytes")
	}
}

// TestCheckedHelpersRefuseBadArguments: every helper a write address comes
// from rejects what the game would assert on.
func TestCheckedHelpersRefuseBadArguments(t *testing.T) {
	if _, err := SavedMemBitAddr(-1); err == nil {
		t.Error("SavedMemBitAddr(-1) accepted")
	}
	if _, err := SavedMemBitAddr(TableCount); err == nil {
		t.Error("SavedMemBitAddr(16) accepted")
	}
	if _, err := ZoneAddr(ZoneCount); err == nil {
		t.Error("ZoneAddr(32) accepted")
	}
	if _, err := ZoneAddr(-1); err == nil {
		t.Error("ZoneAddr(-1) accepted")
	}
	if _, err := RoomStatusAddr(RoomCount); err == nil {
		t.Error("RoomStatusAddr(64) accepted")
	}
	if _, err := ZoneNoAddr(-1); err == nil {
		t.Error("ZoneNoAddr(-1) accepted")
	}
}

// TestEverythingInMEM1 guards against an address that slipped a digit.
func TestEverythingInMEM1(t *testing.T) {
	all := map[string]uint32{
		"GameInfo": GameInfo, "Play": Play, "LiveMemBit": LiveMemBit, "Zones": Zones,
		"CurStage": CurStage, "NextStageEnable": NextStageEnable, "StagInfoPtr": StagInfoPtr,
		"EvtMode": EvtMode, "PlayerPtr": PlayerPtr, "ItemMaxMagicCount": ItemMaxMagicCount,
		"MiniGameType": MiniGameType, "RoomStatus": RoomStatus, "StayNo": StayNo,
		"MenuPause": MenuPause, "ActorQueue": ActorQueue, "FopAcType": FopAcType,
		"GameHeapPtr": GameHeapPtr, "ZeldaHeapPtr": ZeldaHeapPtr, "ArchiveHeapPtr": ArchiveHeapPtr,
		"GameHeapErrors": GameHeapErrors, "ZeldaHeapErrors": ZeldaHeapErrors,
		"ArchiveHeapErrors": ArchiveHeapErrors, "OSArenaLo": OSArenaLo,
		"JKRHeapCodeEnd": JKRHeapCodeEnd, "DMC": DMC, "GbaCom": GbaCom,
	}
	for name, a := range all {
		if a < MEM1Base || a >= MEM1End {
			t.Errorf("%s = 0x%08X is outside MEM1", name, a)
		}
	}
	if GameInfo+GameInfoSize > MEM1End || RoomStatus+RoomStatusSize > GameInfo {
		t.Error("gameInfo or mStatus overruns its neighbours")
	}
}

// TestGbaComLayout checks g_mDoGaC_gbaCom's fields against its size and
// neighbours: the struct ends where TestDataManager starts (sym:17367-
// 17368), before DMC, and the fields mDoGaC_GbaLink reads sit at their
// header offsets (m_Do_gba_com.h:67-110) within one read of
// GbaComLinkReadLen bytes.
func TestGbaComLayout(t *testing.T) {
	const testDataManager = 0x803B917C // sym:17368
	if GbaCom+GbaComSize != testDataManager || GbaCom%4 != 0 || GbaCom+GbaComSize > DMC {
		t.Errorf("g_mDoGaC_gbaCom 0x%08X+0x%X does not end at TestDataManager 0x%08X", GbaCom, GbaComSize, testDataManager)
	}
	// The header's own arithmetic: 0x00C + 16 entries of 0x10, then the
	// u8s at 0x10C-0x10E, a pointer at 0x110, three u32 to 0x11C, two more
	// words after the demo, then field_0x128[1] of 8 bytes.
	if 0xC+16*0x10 != 0x10C || 0x10C+2 != GbaComOffRetries || 0x11C+4+4+4 != GbaComOffContext || GbaComOffContext+8 != GbaComSize {
		t.Error("the header's offsets don't add up")
	}
	for name, off := range map[string]int{"mEnable": GbaComOffEnable, "field_0x2": GbaComOffState, "field_0x4": GbaComOffClosed,
		"field_0x10e": GbaComOffRetries, "field_0x128[0].field_0x0": GbaComOffContext} {
		if off < 0 || off >= GbaComLinkReadLen || GbaComLinkReadLen > GbaComSize {
			t.Errorf("%s at +0x%X is outside the link read (0x%X of 0x%X)", name, off, GbaComLinkReadLen, GbaComSize)
		}
	}
	if GbaCom >= RoomStatus && GbaCom < RoomStatus+RoomStatusSize || GbaCom+GbaComSize > RoomStatus && GbaCom < RoomStatus {
		t.Error("g_mDoGaC_gbaCom overlaps mStatus")
	}
}

// TestFoldedLiterals pins every constant that replaced a literal outside
// this package (Phase C2) to the value the old literal had, so the fold
// changed names and nothing else.
func TestFoldedLiterals(t *testing.T) {
	for _, c := range []struct {
		name, was string
		got, want uint32
	}{
		{"PlayerPtr", "dolphin.PlayerPtrAddr; tunic_diag playerPtrArray0", PlayerPtr, 0x803CA754},
		{"ActorOffCurPos", "dolphin.PosOffset", ActorOffCurPos, 0x1F8},
		{"ActorOffCurAngle", "dolphin.RotOffset", ActorOffCurAngle, 0x204},
		{"ActorOffCurRoomNo", "dolphin.RoomOffset", ActorOffCurRoomNo, 0x20A},
		{"PlayerOffCurProc", "dolphin.AnimOffset", PlayerOffCurProc, 0x31D8},
		{"PlayerOffCLModelData", "tunic_diag daPyMpCLModelDataOff", PlayerOffCLModelData, 0x0328},
		{"PlayerOffCurrLinktex", "tunic_diag daPyMpCurrLinktexOff", PlayerOffCurrLinktex, 0x0338},
		{"PlayerOffOtherLinktex", "tunic_diag daPyOtherLinktexOff", PlayerOffOtherLinktex, 0x033C},
		{"PlayerOffNoResetFlg1", "tunic_diag daPyNoResetFlg1Off", PlayerOffNoResetFlg1, 0x02A0},
		{"PlayerFlg1CasualClothes", "tunic_diag daPyFlg1Casual", PlayerFlg1CasualClothes, 0x00000008},
		{"MEM1Base", "inMEM1 / IsInGame / pose-feed lower bound", MEM1Base, 0x80000000},
		{"MEM1End", "inMEM1 / IsInGame / pose-feed upper bound", MEM1End, 0x81800000},
	} {
		if c.got != c.want {
			t.Errorf("%s = 0x%X, want 0x%X (was %s)", c.name, c.got, c.want, c.was)
		}
	}
}

// TestInMEM1 checks InMEM1 against the literal range check it replaced,
// a >= 0x80000000 && a < 0x81800000, at and around both edges.
func TestInMEM1(t *testing.T) {
	old := func(a uint32) bool { return a >= 0x80000000 && a < 0x81800000 }
	for _, a := range []uint32{0, 0x7FFFFFFF, 0x80000000, 0x80000001, 0x803CA754,
		0x817FFFFF, 0x81800000, 0x81800001, 0xC0000000, 0xFFFFFFFF} {
		if got, want := InMEM1(a), old(a); got != want {
			t.Errorf("InMEM1(0x%08X) = %v, want %v", a, got, want)
		}
	}
}

// TestLinkActorOffsets ties the Link fields to their structs:
// current.angle and roomNo follow current.pos inside actor_place
// (f_op_actor.h:212-216), and daPy_lk_c's mpCLModelData, mpCLModel,
// mpKatsuraModel, mpYamuModel, mpCurrLinktex, then the inline 0x20-byte
// ResTIMG mOtherLinktex run back to back (d_a_player_main.h:1992-1998).
func TestLinkActorOffsets(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want int
	}{
		{"ActorOffCurAngle", ActorOffCurAngle, ActorOffCurPos + 0xC},
		{"ActorOffCurRoomNo", ActorOffCurRoomNo, ActorOffCurPos + 0x12},
		{"PlayerOffCurrLinktex", PlayerOffCurrLinktex, PlayerOffCLModelData + 4*4},
		{"PlayerOffOtherLinktex", PlayerOffOtherLinktex, PlayerOffCurrLinktex + 4},
	} {
		if c.got != c.want {
			t.Errorf("%s = 0x%X, want 0x%X", c.name, c.got, c.want)
		}
	}
	if PlayerOffNoResetFlg1 >= PlayerOffCLModelData || PlayerOffDemoType >= PlayerOffCLModelData {
		t.Error("a daPy_py_c field sits past where daPy_lk_c's own members start")
	}
	if PlayerOffCurProc <= PlayerOffOtherLinktex+0x20 {
		t.Error("mCurProc overlaps the clothes textures")
	}
}

// TestM6Offsets pins the actor, tag, enemyice and player offsets M6 reads
// (§7.2) to their structs: each field sits where its neighbours put it.
func TestM6Offsets(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want int
	}{
		// create_tag_class: node_class {prev, data, next} then mpTagData,
		// then s8 mbIsUse (c_tag.h:10-14).
		{"TagOffUse", TagOffUse, TagOffTagData + 4},
		{"TagSize", TagSize, TagOffUse + 4},
		// base_process_class: mProcName s16, mUnk0, mPauseFlag, then
		// mInitState (f_pc_base.h:16-17).
		{"ActorOffInitState", ActorOffInitState, ActorOffProcName + 4},
		// fopAc_ac_c: parentActorID before home, home.angle after
		// home.pos, current two actor_places after home (f_op_actor.h:285-289).
		{"ActorOffParentID", ActorOffParentID, ActorOffHomePos - 4},
		{"ActorOffHomeAngle", ActorOffHomeAngle, ActorOffHomePos + 0xC},
		{"ActorOffCurPos", ActorOffCurPos, ActorOffHomePos + 2*0x14},
		{"ActorOffMaxHealth", ActorOffMaxHealth, ActorOffHealth - 1},
		{"ActorOffAttnFlags", ActorOffAttnFlags, ActorOffMaxHealth - 4},
		{"ActorOffCarryByte", ActorOffCarryByte, ActorOffStatus + 2},
		// enemyice: mpActor, s16 mFreezeDuration, s8 mLightShrinkTimer,
		// pad, f32 mYOffset, s8 m00C, s8 mMode, s16 mFreezeTimer
		// (c_damagereaction.h:12-21).
		{"EnemyIceOffTimer", EnemyIceOffTimer, EnemyIceOffSelf + 4 + 2},
		{"EnemyIceOffMode", EnemyIceOffMode, 0x8 + 4 + 1},
		{"EnemyIceOffFreezeTimer", EnemyIceOffFreezeTimer, EnemyIceOffMode + 1},
		{"EnemyIceSize", EnemyIceSize, EnemyIceOffFreezeTimer + 2},
		// daBoko_c: fopAc_ac_c (0x290), request_of_phase_process_class
		// mPhase (8), J3DModel* (4), mDoExt_brkAnm mBrkAnm (0x18), u32
		// m2B4, then u8 mNowMode (d_a_boko.h:122-128).
		{"BokoOffNowMode", BokoOffNowMode, 0x290 + 8 + 4 + 0x18 + 4},
	} {
		if c.got != c.want {
			t.Errorf("%s = 0x%X, want 0x%X", c.name, c.got, c.want)
		}
	}
	if uint32(ActorCarryByteMask)<<8 != ActorStatusCarry || ActorStatusBoss != 1<<26 {
		t.Errorf("CARRY byte mask 0x%02X, BOSS 0x%08X", ActorCarryByteMask, ActorStatusBoss)
	}
	if ProcessIDCounter < MEM1Base || ProcessIDCounter >= MEM1End || RoomStatusOffFlags >= RoomStatusOffZoneCount {
		t.Error("ProcessIDCounter or mFlags out of place")
	}
}
