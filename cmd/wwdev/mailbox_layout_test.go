package main

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// TestMailboxHelpers checks package main's mailbox-derived sizes against
// the generated layout (wwapi/mailbox/layout_gen.go, from
// inject/include/mailbox.h; its freshness test is wwapi/mailbox's
// TestMailboxLayout). The actor-puppet slot helper is feed's, checked by
// its TestSlotAddr; the pose-feed and tunic slot offsets are wwapi/avatar's,
// checked by its TestSlotOffsets.
func TestMailboxHelpers(t *testing.T) {
	// faceStateWireBytes is the pose message's per-slot face payload; the
	// receiver copies it straight into face_state[slot].
	if faceStateWireBytes != mailbox.FaceStateSize {
		t.Errorf("faceStateWireBytes = %d, FaceState is %d bytes", faceStateWireBytes, mailbox.FaceStateSize)
	}
}
