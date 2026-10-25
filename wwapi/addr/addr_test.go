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
		{"PlayerInfo0Ptr", PlayerInfo0Ptr, Play + PlayOffPlayerInfo},
		{"PlayerInfo0Ptr (d_com_inf_game.h:787-791: mPlayerInfo[1] of 8 before mpPlayerPtr)", PlayerInfo0Ptr, PlayerPtr - 8},
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
		"RoomStayNo": RoomStayNo, "CcS": CcS, "CalcMtxPtr": CalcMtxPtr,
		"MObjectInfo": MObjectInfo, "PlayerInfo0Ptr": PlayerInfo0Ptr,
		"SolidHeapErrors": SolidHeapErrors, "CommandHeapErrors": CommandHeapErrors,
		"UnknownHeapErrors": UnknownHeapErrors, "HeapErrorsTotal": HeapErrorsTotal,
		"HeapCountersAddr": HeapCountersAddr, "HeapCountersEnd-1": HeapCountersAddr + HeapCountersLen - 1,
		"CommandHeapPtr": CommandHeapPtr, "ExpHeapVtable": ExpHeapVtable,
	}
	for name, a := range all {
		if a < MEM1Base || a >= MEM1End {
			t.Errorf("%s = 0x%08X is outside MEM1", name, a)
		}
	}
	// The seven heap counters are one contiguous run, in declaration order.
	for i, a := range []uint32{SolidHeapErrors, GameHeapErrors, ZeldaHeapErrors, CommandHeapErrors,
		ArchiveHeapErrors, UnknownHeapErrors, HeapErrorsTotal} {
		if a != HeapCountersAddr+uint32(4*i) {
			t.Errorf("heap counter %d at 0x%08X, want 0x%08X", i, a, HeapCountersAddr+uint32(4*i))
		}
	}
	if HeapCountersLen != 7*4 {
		t.Errorf("HeapCountersLen 0x%X, want 0x1C", HeapCountersLen)
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
		{"MEM1Base", "inMEM1 / Link.Ready / pose-feed lower bound", MEM1Base, 0x80000000},
		{"MEM1End", "inMEM1 / Link.Ready / pose-feed upper bound", MEM1End, 0x83000000},
	} {
		if c.got != c.want {
			t.Errorf("%s = 0x%X, want 0x%X (was %s)", c.name, c.got, c.want, c.was)
		}
	}
}

// TestInMEM1 checks InMEM1 against the literal range check it replaced,
// a >= 0x80000000 && a < 0x83000000 (48 MB since docs/05 "48 MB MEM1"),
// at and around both edges.
func TestInMEM1(t *testing.T) {
	old := func(a uint32) bool { return a >= 0x80000000 && a < 0x83000000 }
	for _, a := range []uint32{0, 0x7FFFFFFF, 0x80000000, 0x80000001, 0x803CA754,
		0x817FFFFF, 0x81800000, 0x82FFFFFF, 0x83000000, 0x83000001, 0xC0000000, 0xFFFFFFFF} {
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

// TestMirrorActorOffsets pins the fopAc_ac_c and base_process_class
// fields the enemy mirror added (f_op_actor.h:270-321, f_pc_base.h:13-32)
// to their neighbours: mPauseFlag sits between mUnk0 and mInitState, the
// three actor_places are 0x14 apart, shape_angle follows current, speed
// follows scale, and model, speedF, eyePos sit where cullSizeFar, jntHit
// and maxFallSpeed put them.
func TestMirrorActorOffsets(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want int
	}{
		{"ActorOffPauseFlag", ActorOffPauseFlag, ActorOffInitState - 1},
		{"ActorOffPauseFlag (mProcName s16, mUnk0 s8)", ActorOffPauseFlag, ActorOffProcName + 2 + 1},
		{"ActorOffOldPos", ActorOffOldPos, ActorOffHomePos + 0x14},
		{"ActorOffCurPos", ActorOffCurPos, ActorOffOldPos + 0x14},
		{"ActorOffShapeAngle", ActorOffShapeAngle, ActorOffCurPos + 0x14},
		{"ActorOffSpeed (scale cXyz after shape_angle csXyz + pad)", ActorOffSpeed, ActorOffShapeAngle + 8 + 0xC},
		{"ActorOffModel (cullMtx 4, cull 0x18, cullSizeFar 4)", ActorOffModel, ActorOffSpeed + 0xC + 4 + 0x18 + 4},
		{"ActorOffSpeedF (jntHit)", ActorOffSpeedF, ActorOffModel + 4 + 4},
		{"ActorOffEyePos (gravity, maxFallSpeed)", ActorOffEyePos, ActorOffSpeedF + 4 + 4 + 4},
		{"ActorOffAttnFlags (eyePos cXyz, attention_info +0x14)", ActorOffAttnFlags, ActorOffEyePos + 0xC + 0x14},
	} {
		if c.got != c.want {
			t.Errorf("%s = 0x%X, want 0x%X", c.name, c.got, c.want)
		}
	}
	if ActorOffEyePos+0xC > ActorSize || ActorOffShapeAngle+8 > ActorOffSpeed {
		t.Error("a mirror field runs past the struct or its neighbour")
	}
}

// TestMo2Offsets pins daMo2_c's fields (d_a_mo2.h) and mDoExt_McaMorf's
// (m_Do_ext.h, J3DAnimation.h) to the design's numbers and to each
// other: the damagereaction fields hang off mDamageReaction, the
// colliders are a dCcD_Cyl (0x130) and dCcD_Sph (0x12C) apart, the frame
// control's fields follow its vtable, and the statics add up.
func TestMo2Offsets(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want int
	}{
		{"Mo2OffMode", Mo2OffMode, 0x2BC},
		{"Mo2OffMorf", Mo2OffMorf, 0x2C4},
		{"Mo2OffDeathSwitch (d_a_mo2.h:42)", Mo2OffDeathSwitch, 0x2C0},
		{"Mo2OffInvuln", Mo2OffInvuln, 0x5B4},
		// d_a_mo2.h:58-60: f32 m0590, int m0594, int m0598 back to back.
		{"Mo2OffBlurScale", Mo2OffBlurScale, 0x590},
		{"Mo2OffBlurOn", Mo2OffBlurOn, Mo2OffBlurScale + 4},
		{"Mo2OffBlurShape", Mo2OffBlurShape, Mo2OffBlurOn + 4},
		{"Mo2OffBlurOn (d_a_mo2.h:59)", Mo2OffBlurOn, 0x594},
		{"Mo2OffBlurShape (d_a_mo2.h:60)", Mo2OffBlurShape, 0x598},
		// d_a_mo2.h:65-67: s16 m05B0, m05B2, m05B4 back to back.
		{"Mo2OffAnmGate", Mo2OffAnmGate, 0x5B0},
		{"Mo2OffPlaySkip", Mo2OffPlaySkip, Mo2OffAnmGate + 2},
		{"Mo2OffPlaySkip (d_a_mo2.h:66)", Mo2OffPlaySkip, 0x5B2},
		{"Mo2OffInvuln (m05B2 + 2)", Mo2OffInvuln, Mo2OffPlaySkip + 2},
		{"Mo2OffHasWeapon (d_a_mo2.h:90)", Mo2OffHasWeapon, 0xDD4},
		{"Mo2OffDR", Mo2OffDR, 0x614},
		{"Mo2OffDRMode", Mo2OffDRMode, 0x618},
		{"Mo2OffDRAction", Mo2OffDRAction, 0x61A},
		{"Mo2OffTilt", Mo2OffTilt, 0xAA0},
		{"Mo2OffAttackType", Mo2OffAttackType, 0x2060},
		{"Mo2OffAttackPhase", Mo2OffAttackPhase, 0x2064},
		{"Mo2OffCoCyl", Mo2OffCoCyl, 0x208C},
		{"Mo2OffTgCyl (mCoCyl + dCcD_Cyl 0x130)", Mo2OffTgCyl, Mo2OffCoCyl + 0x130},
		{"Mo2OffHeadSph (mTgCyl + dCcD_Cyl 0x130)", Mo2OffHeadSph, Mo2OffTgCyl + 0x130},
		{"Mo2OffDefenseSph (mHeadSph + dCcD_Sph 0x12C)", Mo2OffDefenseSph, Mo2OffHeadSph + 0x12C},
		{"Mo2OffDefenseSph", Mo2OffDefenseSph, 0x2418},
		{"Mo2CcCenterOff (dCcD_GObjInf 0xF8 + cCcD_ShapeAttr 0x20)", Mo2CcCenterOff, 0xF8 + 0x20},
		{"Mo2CcTgHitOff (Tg +0x18, mRPrm +4)", Mo2CcTgHitOff, 0x18 + 4},
		{"Mo2CcTgHitObjOff (Tg +0x18, mHitObj +8)", Mo2CcTgHitObjOff, 0x18 + 8},
		{"Mo2OffHeadCenter", Mo2OffHeadCenter, 0x28C8},
		{"Mo2OffShieldCenter", Mo2OffShieldCenter, 0x2928},
		{"Mo2OffBareHanded", Mo2OffBareHanded, 0x2943},
		{"Mo2OffHide", Mo2OffHide, 0x2A1C},
		{"Mo2OffIce", Mo2OffIce, 0x2A54},
		{"Mo2OffIceShrink", Mo2OffIceShrink, 0x2A5A},
		{"McaMorfOffModel", McaMorfOffModel, 0x50},
		{"J3DModelOffBaseScale (J3DModel.h:148; base TR mtx at 0x24)", J3DModelOffBaseScale, 0x24 - 12},
		{"McaMorfOffAnm", McaMorfOffAnm, 0x54},
		{"McaMorfOffFrameCtrl", McaMorfOffFrameCtrl, 0x58},
		{"McaMorfOffAttr", McaMorfOffAttr, 0x5C},
		{"McaMorfOffEnd", McaMorfOffEnd, 0x60},
		{"McaMorfOffRate", McaMorfOffRate, 0x64},
		{"McaMorfOffFrame", McaMorfOffFrame, 0x68},
		{"McaMorfOffCurMorf (frame ctrl 0x14, mpTransformInfo, mpQuat)", McaMorfOffCurMorf, McaMorfOffFrameCtrl + McaMorfFrameCtrlLen + 4 + 4},
		// m_Do_ext.h:374-377: mCurMorf, mPrevMorf, mMorfStep, mpSound.
		{"McaMorfOffPrevMorf", McaMorfOffPrevMorf, McaMorfOffCurMorf + 4},
		{"McaMorfOffMorfStep", McaMorfOffMorfStep, McaMorfOffPrevMorf + 4},
		{"McaMorfOffSound", McaMorfOffSound, McaMorfOffMorfStep + 4},
		{"McaMorfOffPrevMorf (m_Do_ext.h:375)", McaMorfOffPrevMorf, 0x78},
		{"McaMorfOffMorfStep (m_Do_ext.h:376)", McaMorfOffMorfStep, 0x7C},
		{"McaMorfOffSound (m_Do_ext.h:377)", McaMorfOffSound, 0x80},
		{"McaMorfAnimSpanOff (mpAnm)", McaMorfAnimSpanOff, 0x54},
		{"McaMorfAnimSpanLen (mpAnm .. mMorfStep inclusive)", McaMorfAnimSpanLen, 0x2C},
		{"McaMorfAnimSpanLen covers mMorfStep", McaMorfAnimSpanOff + McaMorfAnimSpanLen, McaMorfOffMorfStep + 4},
		// dCcD_GObjInf: cCcD_Obj 0x40, cCcD_DivideInfo 0x08, then dCcD_GObjAt
		// (d_cc_d.h:305-332; facts at-sphere tables[2]).
		{"CcAtOffSPrm (cCcD_Obj 0x40 + cCcD_DivideInfo 0x08 + 0x08)", CcAtOffSPrm, 0x50},
		{"CcAtOffSe (dCcD_GAtTgCoCommonBase 0x1C)", CcAtOffSe, CcAtOffSPrm + 0x1C},
		{"CcAtOffHitMark (mSe, mMtrl, mHitMark)", CcAtOffHitMark, CcAtOffSe + 2},
		{"CcAtOffSpl", CcAtOffSpl, CcAtOffHitMark + 1},
		{"CcAtOffSe", CcAtOffSe, 0x6C},
		{"CcAtOffHitMark", CcAtOffHitMark, 0x6E},
		{"CcAtOffSpl", CcAtOffSpl, 0x6F},
		// d_resorce.h:40-47.
		{"ResInfoSize", ResInfoSize, 0x24},
		{"ResInfoOffCount (char[14])", ResInfoOffCount, ResInfoOffName + ResInfoNameLen},
		{"ResInfoOffArchive (mCount u16, mpDMCommand)", ResInfoOffArchive, ResInfoOffCount + 2 + 4},
		{"ResInfoOffRes (mpArchive, mpParentHeap, mDataHeap)", ResInfoOffRes, ResInfoOffArchive + 4 + 4 + 4},
		{"ResInfoSize (mRes last)", ResInfoSize, ResInfoOffRes + 4},
		{"ResInfoOffName (char mArchiveName[14])", ResInfoOffName, 0x00},
		{"ResInfoOffCount", ResInfoOffCount, 0x0E},
		{"ResInfoOffArchive", ResInfoOffArchive, 0x14},
		{"ResInfoOffRes", ResInfoOffRes, 0x20},
		{"ObjectInfoCount (mObjectInfo[64])", ObjectInfoCount, 64},
		// Mo2.h:64-119.
		{"Mo2BckFirst", Mo2BckFirst, 0x3E},
		{"Mo2BckLast", Mo2BckLast, 0x75},
		{"Mo2BckCount", Mo2BckCount, 56},
		{"Mo2BckWait", Mo2BckWait, 0x73},
		{"Mo2BckKWait", Mo2BckKWait, 0x5C},
		{"Mo2BckNBWait", Mo2BckNBWait, 0x62},
	} {
		if c.got != c.want {
			t.Errorf("%s = 0x%X, want 0x%X", c.name, c.got, c.want)
		}
	}
	// The fakegame's enemy block must hold every Moblin field read.
	if Mo2OffIce+EnemyIceSize > 0x3000 || Mo2OffShieldCenter+0xC > Mo2OffBareHanded {
		t.Error("a Moblin field is past the fake's actor block or overlaps a neighbour")
	}
	checkRels(t, []rel{
		{"CcS (gameInfo + play + mCcS)", CcS, GameInfo + 0x12A0 + 0x1404},
		{"CcS (Play + mCcS)", CcS, Play + 0x1404},
		{"RoomStayNo", RoomStayNo, StayNo},
		{"RoomStayNo (sym:18667)", RoomStayNo, 0x803F6A78},
		{"CalcMtxPtr (sym:18270)", CalcMtxPtr, 0x803F63A0},
		{"OverlapReq (sym:18169)", OverlapReq, 0x803F6160},
		{"ResetDataPtr (sym:18605; r13 0x803FE0E0 - 0x7778)", ResetDataPtr, 0x803FE0E0 - 0x7778},
		{"OverlapRequest (sym:17412)", OverlapRequest, 0x803B9E30},
		{"MObjectInfo (wwapi.h:266)", MObjectInfo, 0x803E0BC8},
		{"PlayerInfo0Ptr (d_com_inf_game.h:787-791)", PlayerInfo0Ptr, 0x803CA74C},
	})
	if CcS+0x1404 > Play+PlaySize || CcS%4 != 0 || CalcMtxPtr%4 != 0 {
		t.Error("mCcS runs past play, or a pointer is misaligned")
	}
	// The resource table: 64 entries from MObjectInfo (wwapi.h:266) fit
	// before the game heap pointers; every BCK index names one of the 56
	// pointers at mRes + 4*idx; the handback rows are BCK indices.
	if MObjectInfo%4 != 0 || MObjectInfo+ObjectInfoCount*ResInfoSize > GameHeapErrors {
		t.Errorf("mObjectInfo 0x%08X+0x%X is misaligned or runs into the heap statics", MObjectInfo, ObjectInfoCount*ResInfoSize)
	}
	for _, idx := range []int{Mo2BckWait, Mo2BckKWait, Mo2BckNBWait} {
		if idx < Mo2BckFirst || idx > Mo2BckLast {
			t.Errorf("BCK index 0x%X is outside 0x%X..0x%X", idx, Mo2BckFirst, Mo2BckLast)
		}
	}
	if Mo2BckNone <= Mo2BckLast || Mo2BckNone > 0xFF {
		t.Errorf("Mo2BckNone 0x%X collides with an index", Mo2BckNone)
	}
	// The sampler's second span, +0x590..+0xDD4 inclusive, holds every
	// field it is read for.
	for name, off := range map[string]int{"blur scale": Mo2OffBlurScale, "anm gate": Mo2OffAnmGate, "play skip": Mo2OffPlaySkip, "invuln": Mo2OffInvuln,
		"dr mode": Mo2OffDRMode, "dr action": Mo2OffDRAction, "tilt": Mo2OffTilt, "has weapon": Mo2OffHasWeapon} {
		if off < Mo2OffBlurScale || off > Mo2OffHasWeapon {
			t.Errorf("%s at +0x%X is outside the sampler's +0x%X..+0x%X span", name, off, Mo2OffBlurScale, Mo2OffHasWeapon)
		}
	}
}

// TestBkOffsets pins bk_class's fields (d_a_bk.h:13-143) to the design's
// numbers ("type_table" Bokoblin_0xBE) and to each other: the
// damagereaction fields hang off dr, m0B2C follows dr's 0x7BC bytes, the
// colliders are a dCcD_Cyl (0x130) or dCcD_Sph (0x12C) apart, and the
// enemyice fields hang off mEnemyIce.
func TestBkOffsets(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want int
	}{
		{"BkClassSize (d_a_bk.h:143)", BkClassSize, 0x1848},
		{"BkOffType (d_a_bk.h:16)", BkOffType, 0x2B4},
		{"BkOffDeathSwitch (d_a_bk.h:20)", BkOffDeathSwitch, 0x2B8},
		{"BkOffDrawGate (d_a_bk.h:22)", BkOffDrawGate, 0x2BA},
		{"BkOffMorf (d_a_bk.h:24)", BkOffMorf, 0x2BC},
		{"BkOffEyeBtp (d_a_bk.h:26)", BkOffEyeBtp, 0x2C4},
		{"BkOffShieldModel (d_a_bk.h:32)", BkOffShieldModel, 0x2D0},
		{"BkOffShieldGate (d_a_bk.h:33)", BkOffShieldGate, BkOffShieldModel + 4},
		{"BkOffBouenModel (d_a_bk.h:36)", BkOffBouenModel, 0x2D8},
		{"BkOffBouenGate (d_a_bk.h:37)", BkOffBouenGate, BkOffBouenModel + 4},
		{"BkOffHide2 (d_a_bk.h:39)", BkOffHide2, 0x2DE},
		// d_a_bk.h:44-46: f32 m02EC, int m02F0, int m02F4 back to back.
		{"BkOffBlurScale", BkOffBlurScale, 0x2EC},
		{"BkOffBlurOn", BkOffBlurOn, BkOffBlurScale + 4},
		{"BkOffBlurShape", BkOffBlurShape, BkOffBlurOn + 4},
		// d_a_bk.h:51-52: s16 m030C, m030E.
		{"BkOffPlaySkip", BkOffPlaySkip, 0x30C},
		{"BkOffInvuln", BkOffInvuln, BkOffPlaySkip + 2},
		{"BkOffDR (d_a_bk.h:72)", BkOffDR, 0x370},
		{"BkOffDRMode", BkOffDRMode, 0x374},
		{"BkOffDRAction", BkOffDRAction, 0x376},
		{"BkOffTilt", BkOffTilt, 0x7FC},
		{"BkOffTiltZ", BkOffTiltZ, 0x800},
		{"BkOffDRInvincible", BkOffDRInvincible, 0x83E},
		{"BkOffDRM714", BkOffDRM714, 0xA84},
		{"DamageReactionSize (c_damagereaction.h:193)", DamageReactionSize, 0x7BC},
		{"m0B2C follows dr (d_a_bk.h:73)", BkOffDR + DamageReactionSize, 0xB2C},
		{"BkOffWeaponHold (d_a_bk.h:74)", BkOffWeaponHold, 0xB30},
		{"BkOffCoCyl (d_a_bk.h:89)", BkOffCoCyl, 0xB88},
		{"BkOffTgCyl (m0B88 + dCcD_Cyl 0x130)", BkOffTgCyl, BkOffCoCyl + 0x130},
		{"BkOffHeadSph (m0CB8 + dCcD_Cyl 0x130)", BkOffHeadSph, BkOffTgCyl + 0x130},
		{"BkOffShieldSph (m0DE8 + dCcD_Sph 0x12C)", BkOffShieldSph, BkOffHeadSph + 0x12C},
		{"BkOffAtSph (m0F14 + dCcD_Sph 0x12C)", BkOffAtSph, BkOffShieldSph + 0x12C},
		{"BkOffHeadCenter (m1040 + dCcD_Sph 0x12C)", BkOffHeadCenter, BkOffAtSph + 0x12C},
		{"BkOffHeadCenter (d_a_bk.h:94)", BkOffHeadCenter, 0x116C},
		{"BkOffShieldCenter (d_a_bk.h:102)", BkOffShieldCenter, 0x11CC},
		{"BkOffJaw (d_a_bk.h:109)", BkOffJaw, 0x11F4},
		{"BkOffBokoPID (d_a_bk.h:112)", BkOffBokoPID, 0x1200},
		{"BkOffBokoAttach (d_a_bk.h:118)", BkOffBokoAttach, 0x1214},
		{"BkOffDeleteReq (d_a_bk.h:123)", BkOffDeleteReq, 0x121C},
		{"BkOffDemoGate (d_a_bk.h:134)", BkOffDemoGate, 0x1234},
		{"BkOffIce (d_a_bk.h:140)", BkOffIce, 0x1264},
		{"BkOffIceShrink (ice +6)", BkOffIceShrink, 0x126A},
		{"BkOffIceFreeze (ice +0xE)", BkOffIceFreeze, 0x1272},
		{"BkOffJntHit (d_a_bk.h:142)", BkOffJntHit, BkClassSize - 4},
	} {
		if c.got != c.want {
			t.Errorf("%s = 0x%X, want 0x%X", c.name, c.got, c.want)
		}
	}
	if BkBokoNone != 0xFFFFFFFF || BkParamTypeMask != 0xF || BkTypeTorchShield != 0xB {
		t.Error("BK params constants moved")
	}
}

// TestCcOffsets pins cc_class's fields (d_a_cc.h:14-95) to the design's
// numbers ("type_table" ChuChu_0xCF) and to each other.
func TestCcOffsets(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want int
	}{
		{"CcClassSize (d_a_cc.h:95)", CcClassSize, 0xE04},
		{"CcOffMorf (d_a_cc.h:17)", CcOffMorf, 0x2B4},
		{"CcOffBehavior (d_a_cc.h:28)", CcOffBehavior, 0x2F0},
		{"CcOffColor (d_a_cc.h:29)", CcOffColor, CcOffBehavior + 1},
		{"CcOffAction (d_a_cc.h:32)", CcOffAction, 0x2F4},
		{"CcOffSubstate (d_a_cc.h:33)", CcOffSubstate, CcOffAction + 1},
		{"CcOffElectric (d_a_cc.h:35)", CcOffElectric, 0x2F7},
		{"CcOffNoShadowBG (d_a_cc.h:37)", CcOffNoShadowBG, 0x2F9},
		{"CcOffStone (d_a_cc.h:42)", CcOffStone, 0x2FE},
		{"CcOffSplit (d_a_cc.h:45)", CcOffSplit, 0x301},
		{"CcOffInstaKill (d_a_cc.h:48)", CcOffInstaKill, 0x304},
		{"CcOffWobble (d_a_cc.h:52)", CcOffWobble, 0x310},
		{"CcOffGroundTilt (d_a_cc.h:53; m310 csXyz + 6)", CcOffGroundTilt, CcOffWobble + 6},
		{"CcOffGroundTiltZ", CcOffGroundTiltZ, 0x31A},
		{"CcOffSquashPhase (d_a_cc.h:54)", CcOffSquashPhase, 0x31C},
		{"CcOffBckIndex (d_a_cc.h:55)", CcOffBckIndex, 0x320},
		{"CcOffYOffset (d_a_cc.h:57)", CcOffYOffset, 0x328},
		{"CcOffScale (d_a_cc.h:60)", CcOffScale, 0x334},
		{"CcOffTimers (d_a_cc.h:68)", CcOffTimers, 0x34E},
		{"CcOffStunTimer (m34E[4])", CcOffStunTimer, 0x356},
		{"CcOffTimersEnd (m34E[7], d_a_cc.h:69)", CcOffTimersEnd, CcOffTimers + 7*2},
		{"CcOffStts (d_a_cc.h:89)", CcOffStts, 0x680},
		{"CcOffCyl (mStts + dCcD_Stts 0x3C, d_a_cc.h:90)", CcOffCyl, 0x6BC},
		{"CcOffMtx (mCyl + dCcD_Cyl 0x130, d_a_cc.h:91)", CcOffMtx, CcOffCyl + 0x130},
		{"CcCylOffTgSPrm (cCcD_ObjAt 0x18)", CcCylOffTgSPrm, 0x18},
		{"CcCylOffCoSPrm (+ cCcD_ObjTg 0x14)", CcCylOffCoSPrm, CcCylOffTgSPrm + 0x14},
		{"Tg SPrm vs the Moblin's Tg hit word (mRPrm = mSPrm + 4)", CcCylOffTgSPrm + 4, Mo2CcTgHitOff},
		{"CcOffCylSPrmEnd", CcOffCylSPrmEnd, 0x6EC},
		{"CcOffIce (d_a_cc.h:92; m7EC Mtx 0x30)", CcOffIce, CcOffMtx + 0x30},
		{"CcOffIceShrink", CcOffIceShrink, 0x822},
		{"CcOffIceFreeze", CcOffIceFreeze, 0x82A},
	} {
		if c.got != c.want {
			t.Errorf("%s = 0x%X, want 0x%X", c.name, c.got, c.want)
		}
	}
	if CcSPrmSet != 1 || CcColorBlack != 3 || CcSubstateDying != 0x35 || CcActionDying != 4 {
		t.Error("CC flag constants moved")
	}
}

// TestKindBckRanges pins the Bk and Cc BCK ranges (Bk.h:43-85, Cc.h:22-36).
func TestKindBckRanges(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want int
	}{
		{"BkBckFirst (AOMUKE)", BkBckFirst, 0x29},
		{"BkBckLast (WALK2)", BkBckLast, 0x53},
		{"BkBckCount", BkBckCount, 43},
		{"BkBckWait", BkBckWait, 0x51},
		{"CcBckFirst (ATACK01)", CcBckFirst, 0x14},
		{"CcBckLast (TSTART02)", CcBckLast, 0x22},
		{"CcBckCount", CcBckCount, 15},
	} {
		if c.got != c.want {
			t.Errorf("%s = 0x%X, want 0x%X", c.name, c.got, c.want)
		}
	}
	if BkArc != "Bk" || CcArc != "CC" || len(BkArc) >= ResInfoNameLen || len(CcArc) >= ResInfoNameLen {
		t.Errorf("archive names %q %q", BkArc, CcArc)
	}
	if BkBckWait < BkBckFirst || BkBckWait > BkBckLast || Mo2BckNone <= BkBckLast {
		t.Error("BK wait row or the none index out of range")
	}
}
