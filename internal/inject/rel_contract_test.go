package inject

import (
	"testing"

	mbox "github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// TestRELContract: the REL module's constants scripts/extract_blob.py
// writes into blob.go from inject/rel/rel_main.c agree with
// wwapi/mailbox's copies (which wwapi reads the slack with), the slack
// words lie past the Mailbox struct and below the arena, and a blob
// with a module has its id, profile and ELF, one without has none.
func TestRELContract(t *testing.T) {
	if RELMagic != mbox.RELMagic || RELMagicOff != mbox.OffRELMagic || RELBuildIDOff != mbox.OffRELBuildID || RELFrameOff != mbox.OffRELFrame {
		t.Errorf("blob.go REL words (magic 0x%08X at +0x%X, build id +0x%X, frame +0x%X) differ from wwapi/mailbox (0x%08X, +0x%X, +0x%X, +0x%X)",
			RELMagic, RELMagicOff, RELBuildIDOff, RELFrameOff, mbox.RELMagic, mbox.OffRELMagic, mbox.OffRELBuildID, mbox.OffRELFrame)
	}
	if RELMagicOff < mbox.MailboxSize || MailboxAddr+RELFrameOff+4 > ArenaLo {
		t.Errorf("REL words at +0x%X..+0x%X are not in the slack between the Mailbox struct (0x%X bytes) and the arena 0x%08X",
			RELMagicOff, RELFrameOff+4, mbox.MailboxSize, ArenaLo)
	}
	for _, off := range []uint32{RELMagicOff, RELBuildIDOff, RELFrameOff} {
		if off%4 != 0 {
			t.Errorf("REL word at +0x%X is not 4-aligned", off)
		}
	}
	if RELModuleID != 0x58 || RELProcID != 0xB5 || RELProfileSymbol != "g_profile_WWREL" {
		t.Errorf("module 0x%X proc 0x%X profile %q; want 0x58, 0xB5, g_profile_WWREL (inject/rel/README.md)", RELModuleID, RELProcID, RELProfileSymbol)
	}
	if len(RELELF) == 0 {
		if RELBuildID != 0 {
			t.Errorf("RELELF is empty but RELBuildID is 0x%08X", RELBuildID)
		}
		return
	}
	if RELBuildID == 0 {
		t.Error("RELELF is there but RELBuildID is 0")
	}
	if string(RELELF[:4]) != "\x7fELF" {
		t.Errorf("RELELF doesn't start with the ELF magic: % x", RELELF[:4])
	}
}
