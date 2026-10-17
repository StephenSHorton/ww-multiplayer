package main

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// TestMailboxHelpers checks package main's per-slot mailbox helpers and
// its mailbox-derived counts against the generated layout
// (wwapi/mailbox/layout_gen.go, from inject/include/mailbox.h; its
// freshness test is wwapi/mailbox's TestMailboxLayout). The pose-feed and
// tunic slot offsets are wwapi/avatar's, checked by its TestSlotOffsets.
func TestMailboxHelpers(t *testing.T) {
	for i := 0; i < maxPuppets; i++ {
		if got, want := slotAddr(i, mailbox.PuppetOffActive), mailbox.Mailbox+0x10+0x20*uint32(i)+4; got != want {
			t.Errorf("slotAddr(%d, active) = 0x%08X, want 0x%08X", i, got, want)
		}
	}
	// faceStateWireBytes is the pose message's per-slot face payload; the
	// receiver copies it straight into face_state[slot].
	if faceStateWireBytes != mailbox.FaceStateSize {
		t.Errorf("faceStateWireBytes = %d, FaceState is %d bytes", faceStateWireBytes, mailbox.FaceStateSize)
	}
}
