package class

import (
	"regexp"
	"strconv"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/hearts"
)

// TestUntrackedRewardRegistersStayLocal: a heart piece reward that no save
// record marks (tables.RecordOf is none) is never credited to the partner
// by the life derive, so a register its Catalogue entry names as the
// reward's gate must stay each player's. Shared, the winner's value would
// close the reward on the partner, who could then neither win it nor be
// credited it. Sploosh Kaboom's 0xFE07 and barrel shooting's 0xB703 were
// shared by MAX this way.
func TestUntrackedRewardRegistersStayLocal(t *testing.T) {
	reg := regexp.MustCompile(`register 0x([0-9A-Fa-f]{4})`)
	checked := 0
	for _, r := range hearts.Catalogue {
		if tables.RecordOf(r).Kind != tables.RecordNone {
			continue
		}
		for _, m := range reg.FindAllStringSubmatch(r.Record+" "+r.Note, -1) {
			id, err := strconv.ParseUint(m[1], 16, 16)
			if err != nil {
				t.Fatal(err)
			}
			b := addr.EventByte(uint16(id))
			if c := Of(addr.OffEvent + b.Off).Class; c != PerPlayer {
				t.Errorf("%s: register 0x%04X (event byte 0x%02X) is %v, want per player while the reward is untracked", r.ID, id, b.Off, c)
			}
			checked++
		}
	}
	// Sploosh Kaboom's and barrel shooting's Notes name their registers.
	if checked < 2 {
		t.Errorf("checked %d registers, want at least Sploosh Kaboom's and barrel shooting's", checked)
	}
}
