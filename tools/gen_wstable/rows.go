package main

// The screen table's rows (docs/design/native-widescreen.md 3.3, the
// table; section 8 stage 2 item 4 and stage 3 item 3). One row per
// screen, in the plan's table order. Names are written without ".blo";
// the generator appends it, because J2DScreen::set receives the name
// with the extension ("main_parts1.blo") and the hash is over exactly
// that string. Tags are J2DPane tags as the .blo stores them (up to 4
// characters); a range "ben1-4" or "ip00-03" expands to each tag.

// The plan's 22 screens whose full-screen backdrop is a childless PIC1
// to widen (GEOM, stage 2). Since the global GEOM rule (plan section 6,
// lever 1) they have no rows: a screen with no row gets GEOM. The list
// stays for TestGeomGlobalRule, which checks it against the disc's GEOM
// panes (testdata/geom_hits.txt).
var geomScreens = []string{
	"menu_collect_01", "menu_item_02", "menu_explanation", "menu_map_d", "option",
	"f_map", "f_hikaku_1", "g_map_01", "t_map_01", "DOC_map_01", "HRT_map_1",
	"HM_map_1", "IR_map_1", "MN_map_1", "SENSUI_map_1", "TERRY_map_1", "TR_map_1",
	"YG_map_1", "YS_map_1", "gameover", "operate_wind", "operate_wind_mask",
}

func tableRows() []row {
	rows := []row{
		{name: "main_parts1", kind: kindByCX, flags: flagEpoch, stage: "EPOCH 2, the rest 3",
			rules: tags(actNone, "ben1-4", "enem", "enm2", "bey1", "bey2", "enek", "ene1-3", "l", "r", "wiy1")},
		{name: "main_parts2", kind: kindAllR, stage: "3"},
		{name: "main_parts3", kind: kindAllL, stage: "3"},
		{name: "time_swim_1", flags: flagScrR, stage: "3"},
		{name: "time_swim_2", kind: kindAllR, stage: "3"},
		{name: "place_name", stage: "3", rules: tags(actL, "pn")},
		{name: "auction1", flags: flagScrL, stage: "3"},
		{name: "auction2", stage: "3", rules: tags(actL, "icon", "ip00-03", "gd00-05")},
		{name: "menu_save", flags: flagGeom, stage: "2", rules: tags(actEdge, "wp04-07")},
		{name: "wipe_00", stage: "2", rules: tags(actEdge, "wp04-07")},
		{name: "wipe_00_2", stage: "2", rules: tags(actEdge, "wp04-07")},
	}
	// The global GEOM rule's one exception (geom_hits.txt): the picto
	// album's paper b1 and strip b2 cover the screen and are drawn
	// uncompressed (ws_screen_draw_full), so they must keep their width.
	rows = append(rows, row{name: "wipe_01_01", stage: "2"})
	rows = append(rows, row{name: "main_parts_fileselect", kind: kindAllR, flags: flagEpoch, stage: "EPOCH 2, ALL_R 3"})
	return rows
}
