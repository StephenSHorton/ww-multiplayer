package inject

import "testing"

// mailboxAddr mirrors MAILBOX_ADDR in inject/include/mailbox.h. The
// injected section must end below it: past it, the mod's code and data
// overlap the mailbox Go reads and writes every frame.
const mailboxAddr = 0x80412F00

// TestT2EndsBelowMailbox guards the code-space budget (docs/08 §4.5,
// docs/09 §6): a C change that grows T2 into the mailbox fails here, at
// blob regen time, instead of corrupting the mailbox on a live Dolphin.
func TestT2EndsBelowMailbox(t *testing.T) {
	end := T2Address + uint32(len(T2Bytes))
	if end > mailboxAddr {
		t.Fatalf("T2 ends at 0x%08X, past the mailbox at 0x%08X by 0x%X bytes", end, mailboxAddr, end-mailboxAddr)
	}
	t.Logf("T2 0x%08X..0x%08X, 0x%X bytes free below the mailbox", T2Address, end, mailboxAddr-end)
}
