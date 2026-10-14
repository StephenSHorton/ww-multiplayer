package hearts

import "github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/keys/itemsrc"

// RewardKind is how a catalogue reward is handed over.
type RewardKind string

const (
	NPC      RewardKind = "npc"
	Minigame RewardKind = "minigame"
	Letter   RewardKind = "letter"
	Shop     RewardKind = "shop"
	Auction  RewardKind = "auction"
)

// Reward is a heart piece that no stage file places: an NPC, a minigame,
// a letter, a shop or the auction hands it over in code or event data.
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
// plus three minigames whose grant code is not decompiled (ItemSeen
// false), which Build counts separately.
//
// Looked at and left out: no other decompiled file names HEART_PIECE,
// and the only literal 7s given as items are Carlov's and Old Man Ho
// Ho's (d_a_npc_mt.cpp:818-833, d_a_npc_ah.cpp:545-559), set from bits
// (m749, field_0x748) that nothing in the decomp sets, only clears. The
// mail sorting game (d_a_npc_bmsw.cpp, d_a_npc_btsw.cpp, both
// decompiled) pays only rupees (d_a_npc_bmsw.cpp:743). Not decompiled,
// so unchecked beyond the three minigames below: d_a_npc_ds1,
// d_a_npc_gp1, d_a_npc_kf1, d_a_npc_p2, d_a_npc_uk and d_a_npc_sarace
// (the boating course's own items are race items and barrels, which
// Build reads from the stage files).
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
		ID: "traveling-merchant", Giver: "the traveling merchants", Where: "event data", Kind: NPC, Item: itemsrc.ItemHeartPiece,
		Site:     "l_get_item_no[0], d_a_npc_roten.cpp:1373-1375; daNpcRoten_c::eventGetItemInit :2359-2381 (event integer \"ItemNo\" :2362-2365)",
		ItemSeen: true,
		Note:     "the event data says which cut hands it over; no record in code",
	},
	{
		ID: "windfall-people-0", Giver: "a Windfall resident (NPC_PEOPLE)", Where: "Windfall Island", Kind: NPC, Item: itemsrc.ItemHeartPiece,
		Site:     "l_get_item_no[0], d_a_npc_people.cpp:3855-3866; daNpcPeople_c::eventGetItemInit :6043-6056",
		ItemSeen: true,
		Note:     "an orange rupee in the Japanese release (:3856-3860); the event data names the resident and its record",
	},
	{
		ID: "windfall-people-3", Giver: "a Windfall resident (NPC_PEOPLE)", Where: "Windfall Island", Kind: NPC, Item: itemsrc.ItemHeartPiece,
		Site:     "l_get_item_no[3], d_a_npc_people.cpp:3863; daNpcPeople_c::eventGetItemInit :6043-6056",
		ItemSeen: true,
		Note:     "the event data names the resident and its record",
	},
	{
		ID: "windfall-people-4", Giver: "a Windfall resident (NPC_PEOPLE)", Where: "Windfall Island", Kind: NPC, Item: itemsrc.ItemHeartPiece,
		Site:     "l_get_item_no[4], d_a_npc_people.cpp:3864; daNpcPeople_c::eventGetItemInit :6043-6056",
		ItemSeen: true,
		Note:     "the event data names the resident and its record",
	},
	{
		ID: "sploosh-kaboom", Giver: "Salvatore (Sploosh Kaboom)", Where: "Windfall Island", Kind: Minigame, Item: itemsrc.ItemHeartPiece,
		Site: "daNpc_Kg1_c, d_a_npc_kg1.cpp (25 of 27 functions not decompiled); board d_a_mgameboard.cpp",
		Note: "no grant site in the decomp",
	},
	{
		ID: "barrel-shooting", Giver: "Salvatore (cannon minigame)", Where: "Spectacle Island", Kind: Minigame, Item: itemsrc.ItemHeartPiece,
		Site: "daNpc_Kg2_c::evn_createItem_init, d_a_npc_kg2.cpp:167-170 (not decompiled)",
	},
	{
		ID: "bird-man-contest", Giver: "Willi and Obli (Bird-Man Contest)", Where: "Flight Control Platform", Kind: Minigame, Item: itemsrc.ItemHeartPiece,
		Site: "daNpcBmcon_c::eventGetItemInit, d_a_npc_bmcon1.cpp:159-162 (not decompiled)",
	},
}
