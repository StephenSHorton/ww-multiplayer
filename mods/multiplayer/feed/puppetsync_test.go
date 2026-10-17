package feed

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// TestSlotAddr checks the per-slot actor-puppet mailbox helper against the
// generated layout (wwapi/mailbox/layout_gen.go, from
// inject/include/mailbox.h; its freshness test is wwapi/mailbox's
// TestMailboxLayout). The pose-feed and tunic slot offsets are
// wwapi/avatar's, checked by its TestSlotOffsets.
func TestSlotAddr(t *testing.T) {
	for i := 0; i < MaxPuppets; i++ {
		if got, want := SlotAddr(i, mailbox.PuppetOffActive), mailbox.Mailbox+0x10+0x20*uint32(i)+4; got != want {
			t.Errorf("SlotAddr(%d, active) = 0x%08X, want 0x%08X", i, got, want)
		}
	}
}
