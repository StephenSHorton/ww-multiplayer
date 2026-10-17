package tables

import "github.com/StephenSHorton/ww-multiplayer/wwapi/addr"

// What M5's live room puzzles (§6.3, §9 M5) read of the switch table: the
// verdict on the switches SCOPED carries, the dan switches 0x80-0xBF, the
// zone switches 0xC0-0xDF and the one-shot room switches 0xE0-0xEF. The
// host's hub keeps and relays only the bits this grants
// (hub.Config.SyncedScoped), and a writing replica's sender proposes only
// those (hub.ScopeFilter), as SyncedMemorySwitch does for the memory
// switches the image carries.

// SyncedScopedSwitch reports whether co-op may sync stage switch no, as an
// actor of save table table, stage stage and room room writes it:
// SyncedSwitch, for the three stores SCOPED carries and nothing else.
//
//   - A dan switch (0x80-0xBF) is the table's (dSv_danBit_c, kept per save
//     table, d_save.cpp:1225-1235; Appendix C item 25), so stage and room
//     are not asked.
//   - A zone or room switch (0xC0-0xEF) is the zone of room in stage
//     (dSv_info_c::onSwitch, d_save.cpp:1552-1559), so it needs a room of
//     0..63: any other is the JUT_ASSERT at d_save.cpp:1553, which halts
//     the game (JUTAssert.h:9, OSPanic at m_Do_printf.cpp:208-230).
//
// False for a memory switch (SyncedMemorySwitch's), for -1 and 0xFF (no
// switch, d_save.cpp:1544-1546), for 0xF0-0xFE and anything past them
// (the assert at d_save.cpp:1541-1542), for a save table outside 0..15
// (getSave's assert, d_save.cpp:1507), for a switch no placed actor
// writes or one with a writer that doesn't latch (§6.2), and without a
// switch table.
func (t *Tables) SyncedScopedSwitch(table int, stage string, room, no int) bool {
	if !addr.ValidTable(table) {
		return false
	}
	switch addr.ClassifySwitch(no) {
	case addr.SwitchDan:
		return t.SyncedSwitch(table, "", -1, no)
	case addr.SwitchZone, addr.SwitchRoom:
		return room >= 0 && room < addr.RoomCount && stage != "" && t.SyncedSwitch(table, stage, room, no)
	}
	return false
}

// GBAScopedSwitch is GBASwitch for the switches SyncedScopedSwitch opens:
// a dan, zone or room switch a Tingle Tuner writer shares, which a
// replica neither proposes nor writes while a GBA may be linked.
func (t *Tables) GBAScopedSwitch(table int, stage string, room, no int) bool {
	if !t.SyncedScopedSwitch(table, stage, room, no) {
		return false
	}
	if addr.ClassifySwitch(no) == addr.SwitchDan {
		stage, room = "", -1
	}
	return t.GBASwitch(table, stage, room, no)
}
