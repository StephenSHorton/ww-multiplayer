package main

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// TestMailboxHelpers checks package main's per-slot mailbox helpers and
// its mailbox-derived counts against the generated layout
// (wwapi/mailbox/layout_gen.go, from inject/include/mailbox.h; its
// freshness test is wwapi/mailbox's TestMailboxLayout).
func TestMailboxHelpers(t *testing.T) {
	for slot := 0; slot < mailbox.MailboxPoseSlotCap; slot++ {
		for _, c := range []struct {
			name      string
			got, want uint32
		}{
			{"mailboxPoseBufPtr", mailboxPoseBufPtr(slot), mailbox.OffPoseBufPtrs + 4*uint32(slot)},
			{"mailboxPoseJointCount", mailboxPoseJointCount(slot), mailbox.OffPoseJointCounts + 2*uint32(slot)},
			{"mailboxPoseBufState", mailboxPoseBufState(slot), mailbox.OffPoseBufStates + uint32(slot)},
			{"mailboxPoseSeq", mailboxPoseSeq(slot), mailbox.OffPoseSeqs + uint32(slot)},
			{"mailboxFaceState", mailboxFaceState(slot), mailbox.OffFaceState + mailbox.FaceStateSize*uint32(slot)},
			{"mailboxFaceSeq", mailboxFaceSeq(slot), mailbox.OffFaceSeqs + uint32(slot)},
		} {
			if c.got != c.want {
				t.Errorf("%s(%d) = +0x%X, want +0x%X", c.name, slot, c.got, c.want)
			}
		}
	}
	for slot := 0; slot < tunicSlots; slot++ {
		if got, want := mailboxTunicBuf(slot), mailbox.OffTunicBufs+4*uint32(slot); got != want {
			t.Errorf("mailboxTunicBuf(%d) = +0x%X, want +0x%X", slot, got, want)
		}
	}
	if mailboxTunicBuf(tunicSlots) != mailbox.OffTunicBufSize {
		t.Errorf("tunic_bufs holds %d slots, Go uses %d", (mailbox.OffTunicBufSize-mailbox.OffTunicBufs)/4, tunicSlots)
	}
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
	if maxRemoteLinks > mailbox.MailboxPoseSlotCap {
		t.Errorf("maxRemoteLinks %d > MAILBOX_POSE_SLOT_CAP %d", maxRemoteLinks, mailbox.MailboxPoseSlotCap)
	}
}
