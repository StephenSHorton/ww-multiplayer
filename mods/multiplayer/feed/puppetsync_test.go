package feed

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/vissync"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// TestSlotAddr checks the per-slot actor-puppet mailbox helper against the
// generated layout (wwapi/mailbox/layout_gen.go, from
// inject/include/mailbox_core.h; its freshness test is wwapi/mailbox's
// TestMailboxLayout). The pose-feed and tunic slot offsets are
// wwapi/avatar's, checked by its TestSlotOffsets.
func TestSlotAddr(t *testing.T) {
	for i := 0; i < MaxPuppets; i++ {
		if got, want := SlotAddr(i, mailbox.PuppetOffActive), mailbox.Mailbox+0x10+0x20*uint32(i)+4; got != want {
			t.Errorf("SlotAddr(%d, active) = 0x%08X, want 0x%08X", i, got, want)
		}
	}
}

// TestVisDroppedOnReconnect: a dropped session turns the visuals block off
// (no frozen name tags, equip or sea chart markers during the reconnect
// wait), the next session's first Write turns it back on even though the
// block is unchanged, and a refused game is never written.
func TestVisDroppedOnReconnect(t *testing.T) {
	m := mem.NewFakeMem()
	m.Record(true)
	var vis vissync.Sync
	b := mailbox.VisBlock{
		Flags: mailbox.VisFlagNames | mailbox.VisFlagSea,
		Names: [mailbox.VisSlots]string{"Tetra"},
		Sea:   []mailbox.VisSea{{X: 1000, Z: -2000, R: 0xFF}},
	}
	magic := func() uint32 {
		t.Helper()
		v, err := mem.ReadU32(m, mailbox.VisAddr+mailbox.VisMagicOff)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if err := vis.Write(m, b); err != nil || magic() != mailbox.VisMagic {
		t.Fatalf("first session: err %v, magic %#x", err, magic())
	}
	visDropped(m, &vis, true)
	if magic() != 0 {
		t.Fatalf("magic %#x after the drop, want 0 (the REL draws none of it)", magic())
	}
	if err := vis.Write(m, b); err != nil || magic() != mailbox.VisMagic {
		t.Fatalf("reconnected session: err %v, magic %#x", err, magic())
	}
	n := len(m.Writes())
	visDropped(m, &vis, false)
	if len(m.Writes()) != n {
		t.Fatal("visDropped wrote into a game the contract check refuses")
	}
}
