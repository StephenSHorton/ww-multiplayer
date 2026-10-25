package hearts

import "github.com/StephenSHorton/ww-multiplayer/wwapi/tables/keys/itemsrc"

// RewardKind is how a catalogue reward is handed over.
type RewardKind string

const (
	NPC      RewardKind = "npc"
	Minigame RewardKind = "minigame"
	Letter   RewardKind = "letter"
	Shop     RewardKind = "shop"
	Auction  RewardKind = "auction"
	// Drop is an object that drops the piece in code, as a field item
	// with no stage-file placement (the withered trees).
	Drop RewardKind = "drop"
)

// Reward is a heart piece that no stage file places: an NPC, a minigame,
// a letter, a shop, the auction or an object hands it over in code or
// event data.
type Reward struct {
	ID    string     `json:"id"`
	Giver string     `json:"giver"`
	Where string     `json:"where"`
	Kind  RewardKind `json:"kind"`
	Item  uint8      `json:"item"`
	// Site is the decomp function or table that hands the item over.
	Site string `json:"site"`
	// Record is the save bit that marks it given, when the code sets one.
	Record string `json:"record,omitempty"`
	// ItemSeen reports whether the decomp shows the item number at Site.
	// false: the function is a "Nonmatching" stub, so the entry stands on
	// game knowledge alone until the totals or coop-watch confirm it.
	ItemSeen bool   `json:"item_seen"`
	Note     string `json:"note,omitempty"`
}

// Catalogue is, written by hand, every heart piece reward the decomp's
// NPC, letter, shop and auction code hands over, each item number and
// record cited to the file:line that shows it (see the package comment),
// plus five rewards whose grant code is not decompiled (ItemSeen false),
// which Build counts separately: the three minigames, Maggie and the
// withered trees. The last two came from comparing with
// datstarkey/ww-online's catalogue (docs/hearts.md §1, rows 37 and 47,
// MIT; facts, no code) and the Wind Waker Randomizer's item list, which
// both count them; their records there are REL readings, so they stay
// unrecorded here (Appendix C item 168 of docs/08-coop-design.md).
//
// Looked at and left out: no other decompiled file names HEART_PIECE,
// and the only literal 7s given as items are Carlov's and Old Man Ho
// Ho's (d_a_npc_mt.cpp:818-833, d_a_npc_ah.cpp:545-559), set from bits
// (m749, field_0x748) that nothing in the decomp sets, only clears. The
// mail sorting game (d_a_npc_bmsw.cpp, d_a_npc_btsw.cpp, both
// decompiled) pays only rupees (d_a_npc_bmsw.cpp:743). Not decompiled,
// so unchecked beyond the five below: d_a_npc_ds1, d_a_npc_gp1,
// d_a_npc_kf1, d_a_npc_p2, d_a_npc_uk and d_a_npc_sarace (the boating
// course's own items are race items and barrels, which Build reads from
// the stage files).
var Catalogue = []Reward{
	{
		ID: "ivan", Giver: "Ivan (Killer Bees)", Where: "Windfall Island", Kind: NPC, Item: itemsrc.ItemHeartPiece,
		Site:     "daNpc_Mk_c, d_a_npc_mk.cpp:820-825",
		Record:   "event bit 0x1340 (UNK_1340, d_save_event_flag.inc:143)",
		ItemSeen: true,
	},
	{
		ID: "orca", Giver: "Orca", Where: "Outset Island", Kind: NPC, Item: itemsrc.ItemHeartPiece,
		Site:     "daNpc_Ji1_c::createItem, d_a_npc_ji1.cpp:1499-1528 (heart piece :1517-1520)",
		Record:   "event bit 0x0F10 (UNK_0F10, d_save_event_flag.inc:114)",
		ItemSeen: true,
		Note:     "the first time; later wins give a silver rupee (:1514-1516)",
	},
	{
		ID: "auction", Giver: "the auction", Where: "Windfall Island", Kind: Auction, Item: itemsrc.ItemHeartPiece,
		Site:     "l_item_dat[3], d_a_auction.cpp:54; daAuction_c::eventGetItemInit :1194-1213",
		Record:   "event bit 0x1020 (UNK_1020, d_save_event_flag.inc:121; set at d_a_auction.cpp:1210)",
		ItemSeen: true,
	},
	{
		ID: "letter-baitos-mom", Giver: "letter from Baito's mother", Where: "any mailbox", Kind: Letter, Item: itemsrc.ItemHeartPiece,
		Site:     "daObjTpost_c::m_letter[0] and [8], d_a_obj_toripost.cpp:40, :48; cutPresentProc :193-194",
		Record:   "letter register 0xAC03 (LETTER_BAITOS_MOM, d_save_event_flag.inc:549), read at d_a_obj_toripost.cpp:802",
		ItemSeen: true,
		Note:     "two table rows, one letter register: one reward",
	},
	{
		ID: "letter-komalis-father", Giver: "letter from Komali's father", Where: "any mailbox", Kind: Letter, Item: itemsrc.ItemHeartPiece,
		Site:     "daObjTpost_c::m_letter[1], d_a_obj_toripost.cpp:41; cutPresentProc :193-194",
		Record:   "letter register 0xB503 (LETTER_KOMALIS_FATHER, d_save_event_flag.inc:556)",
		ItemSeen: true,
	},
	{
		ID: "letter-hoskits-girlfriend", Giver: "letter from Hoskit's girlfriend", Where: "any mailbox", Kind: Letter, Item: itemsrc.ItemHeartPieceAlt,
		Site:     "daObjTpost_c::m_letter[10], d_a_obj_toripost.cpp:50; cutPresentProc :193-194",
		Record:   "letter register 0xAE03 (LETTER_HOSKITS_GIRLFRIEND, d_save_event_flag.inc:551)",
		ItemSeen: true,
		Note:     "HEART_PIECE_ALT (0x3F), which item_func_kakera_heart also handles",
	},
	{
		ID: "beedle-shop", Giver: "Beedle's shop ship", Where: "the ship whose item set is Item_set_data_bshop_6", Kind: Shop, Item: itemsrc.ItemHeartPiece,
		Site:     "shopItemData_kakera_heart, d_shop.cpp:117-122; Item_set_data_bshop_6 :545-550 (entries 7 and 8 of Item_set_data_tbl, :559-560)",
		Record:   "event bit 0x2010 (UNK_2010, d_save_event_flag.inc:244; set on purchase at d_a_npc_bs1.cpp:903-904)",
		ItemSeen: true,
		Note:     "both table entries share the sold-out bit (d_a_npc_bs1.cpp:1508): one reward",
	},
	{
		ID: "traveling-merchant", Giver: "the Goron traveling merchant (the trading quest)", Where: "the Great Sea", Kind: NPC, Item: itemsrc.ItemHeartPiece,
		Site:     "l_get_item_no[0], d_a_npc_roten.cpp:1373-1375; daNpcRoten_c::eventGetItemInit :2359-2381 (event integer \"ItemNo\" :2362-2365)",
		ItemSeen: true,
		// No Record on purpose: the decomp does not show 0x3E04 gating the
		// piece, and max life is add-only, so a wrong record would raise
		// both players' max life for good once the bit syncs.
		Note: "no record in code. UNVERIFIED candidate: event bit 0x3E04 (UNK_3E04), set at d_a_npc_roten.cpp:2500-2503 in the XY-talk branch as merchant 1 is shown the Shop Guru Statue (isGetMap :2827-2833, message l_msg_try_force); whether that talk's event runs the ItemNo cut that hands over the table's only entry, the piece, is event data",
	},
	// The three NPC_PEOPLE rewards: each record is set as the resident
	// picks the reward talk, the piece comes from the event's ItemNo cut
	// (eventGetItemInit, d_a_npc_people.cpp:6043-6056), and l_get_item_no
	// [0], [3] and [4] are its three heart pieces (:3855-3866). Which index
	// each event names is event data, not checked here: the indexes below
	// are the Wind Waker Randomizer's (logic/item_locations.txt, its
	// d_a_npc_people.rel offsets 0xC54B, 0xC557, 0xC55B). The count does
	// not depend on them. ww-online's records agree on the bits
	// (datstarkey/ww-online docs/hearts.md §1, rows 38, 40 and 42, MIT).
	//
	// GZLE01 only: in the Japanese release (VERSION <= VERSION_JPN)
	// l_get_item_no[0] is an orange rupee, not a heart piece
	// (d_a_npc_people.cpp:3855-3860), so whichever resident uses index 0
	// (Sam, by the Randomizer's pairing) pays no piece there and its record
	// would credit max life wrongly. tables.Build accepts only the GZLE01
	// DOL (its l_objectName check) and the game hook only GZLE01
	// (wwapi.GameID); revisit these three records before any other disc is
	// accepted.
	{
		ID: "kreeb", Giver: "Kreeb (light up the lighthouse)", Where: "Windfall Island", Kind: NPC, Item: itemsrc.ItemHeartPiece,
		Site:     "NPC_UM1, l_msg_um1_get_item, d_a_npc_people.cpp:7061-7063; l_get_item_no[4] :3864 (the index unverified)",
		Record:   "event bit 0x1B20 (UNK_1B20; set with the reward talk at d_a_npc_people.cpp:7061-7063)",
		ItemSeen: true,
	},
	{
		ID: "sam", Giver: "Sam (decorate the town)", Where: "Windfall Island", Kind: NPC, Item: itemsrc.ItemHeartPiece,
		Site:     "NPC_UO1, l_msg_uo1_1st_talk_fdai, d_a_npc_people.cpp:6880-6883; l_get_item_no[0] :3855-3860 (the index unverified)",
		Record:   "event bit 0x1B10 (UNK_1B10; set with the reward talk once every flower stand is done, d_a_npc_people.cpp:6880-6883)",
		ItemSeen: true,
		Note:     "index 0 is an orange rupee in the Japanese release (:3856-3860)",
	},
	{
		ID: "linda-and-anton", Giver: "Linda and Anton", Where: "Windfall Island", Kind: NPC, Item: itemsrc.ItemHeartPiece,
		Site:     "NPC_UW2, l_msg_uw2_1st_talk2, d_a_npc_people.cpp:7033-7035; l_get_item_no[3] :3863 (the index unverified)",
		Record:   "event bit 0x2280 (UNK_2280; set with the reward talk at d_a_npc_people.cpp:7033-7035)",
		ItemSeen: true,
	},
	{
		ID: "maggie", Giver: "Maggie (Moblin's Letter)", Where: "Windfall Island", Kind: NPC, Item: itemsrc.ItemHeartPiece,
		Site: "daNpc_Kp1_c, d_a_npc_kp1.cpp (not decompiled)",
		Note: "UNVERIFIED, from datstarkey/ww-online's reading of the vanilla REL (docs/hearts.md §3.3, MIT): GET_KAKERA_HRT as she takes Moblin's Letter (0x9B); its only mark is mReserveFlags bit 15 with the letter gone from the bag, no save bit RecordOf reads",
	},
	{
		ID: "withered-trees", Giver: "the withered trees", Where: "the Great Sea", Kind: Drop, Item: itemsrc.ItemHeartPiece,
		Site: "daObjFtree::Act_c::search_heart_part, d_a_obj_ftree.cpp (not decompiled)",
		Note: "UNVERIFIED, from datstarkey/ww-online's reading of the vanilla REL (docs/hearts.md §3.3, MIT): the last watered tree drops a field piece with no item bit, and 0x2E20 is set once it is gone; the Randomizer patches it at d_a_obj_ftree.rel 0x0237 and 0x03F3",
	},
	{
		ID: "sploosh-kaboom", Giver: "Salvatore (Sploosh Kaboom)", Where: "Windfall Island", Kind: Minigame, Item: itemsrc.ItemHeartPiece,
		Site: "daNpc_Kg1_c, d_a_npc_kg1.cpp (25 of 27 functions not decompiled); board d_a_mgameboard.cpp",
		Note: "no grant site in the decomp; ww-online reads register 0xFE07 >= 1 as its record (UNVERIFIED, REL only)",
	},
	{
		ID: "barrel-shooting", Giver: "Salvatore (cannon minigame)", Where: "Spectacle Island", Kind: Minigame, Item: itemsrc.ItemHeartPiece,
		Site: "daNpc_Kg2_c::evn_createItem_init, d_a_npc_kg2.cpp:167-170 (not decompiled)",
		Note: "ww-online reads register 0xB703 >= 1 as its record (UNVERIFIED, REL only)",
	},
	{
		ID: "bird-man-contest", Giver: "Willi and Obli (Bird-Man Contest)", Where: "Flight Control Platform", Kind: Minigame, Item: itemsrc.ItemHeartPiece,
		Site: "daNpcBmcon_c::eventGetItemInit, d_a_npc_bmcon1.cpp:159-162 (not decompiled)",
		Note: "ww-online reads event bit 0x2B40 as its record (UNVERIFIED, REL only)",
	},
}
