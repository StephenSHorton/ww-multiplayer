package inject

import (
	"bytes"
	"testing"

	mbox "github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// relBodyNameWordFileOff is the file offset in the GZLE01 DOL of the name
// pointer at RELBodyNameEntryAddr + 4 (0x8033993C: DOL section 11, data
// section 4, verified from inject/original.dol in the stage-4 plan D4-1).
// Only its low half changes (0x8033A6F3 -> 0x8033AE39), so the diff run
// extract_blob.py emits starts two bytes in.
const relBodyNameWordFileOff = 0x33693C

// TestRELBodyContract: the second profile's constants equal wwapi/mailbox's
// copies (themselves pinned to wwremote.h by TestRemoteHeaderPins), it
// shares the keeper's module and mProcName, and the name pointer is the
// module name the vanilla 0xB5 entry already uses.
func TestRELBodyContract(t *testing.T) {
	if RELBodyProcID != mbox.RemoteFullProc || RELBodyNameEntryAddr != mbox.RemoteFullNameEntry || RELBodyNamePtr != mbox.RemoteFullNamePtr {
		t.Errorf("relbody.go proc 0x%X entry 0x%08X ptr 0x%08X differ from wwremote.h's 0x%X 0x%08X 0x%08X",
			RELBodyProcID, RELBodyNameEntryAddr, RELBodyNamePtr, mbox.RemoteFullProc, mbox.RemoteFullNameEntry, mbox.RemoteFullNamePtr)
	}
	if RELBodyProcID == RELProcID || RELBodyProfileSymbol == RELProfileSymbol || RELBodyProfileSymbol != "g_profile_WWBODY" {
		t.Errorf("body profile proc 0x%X %q collides with the keeper's 0x%X %q", RELBodyProcID, RELBodyProfileSymbol, RELProcID, RELProfileSymbol)
	}
	// 0x80339938 is DOL section 11 at file 0x336938 (address - 0x3000);
	// relBodyNameWordFileOff assumes exactly that entry.
	if RELBodyNameEntryAddr != 0x80339938 || RELBodyNameEntryAddr+4-0x80003000 != relBodyNameWordFileOff {
		t.Errorf("name entry 0x%08X: its pointer word is not at file 0x%X", RELBodyNameEntryAddr, relBodyNameWordFileOff)
	}
}

// TestRELBodyDOLPatch: once the REL exports the body profile, the blob's
// in-DOL patches rewrite the proc-0x27 name pointer's low half to
// RELBodyNamePtr's (the two changed bytes AE 39 at file 0x33693E) and touch
// nothing else of that entry. Skipped until build.py and extract_blob.py
// regenerate blob.go with g_profile_WWBODY (the stage-4 REL work).
func TestRELBodyDOLPatch(t *testing.T) {
	if !bytes.Contains(RELELF, []byte(RELBodyProfileSymbol+"\x00")) {
		t.Skipf("RELELF has no %s yet: the REL rebuild has not landed", RELBodyProfileSymbol)
	}
	want := []byte{byte(RELBodyNamePtr >> 8 & 0xFF), byte(RELBodyNamePtr & 0xFF)}
	found := false
	for _, p := range DOLPatches {
		end := p.FileOff + uint32(len(p.Bytes))
		if end <= relBodyNameWordFileOff-4 || p.FileOff >= relBodyNameWordFileOff+4 {
			continue
		}
		if p.FileOff == relBodyNameWordFileOff+2 && bytes.Equal(p.Bytes, want) {
			found = true
			continue
		}
		t.Errorf("DOLPatch at 0x%X (% X) touches the proc-0x27 name entry other than its pointer's low half", p.FileOff, p.Bytes)
	}
	if !found {
		t.Errorf("no DOLPatch writes % X at 0x%X (name pointer 0x%08X)", want, relBodyNameWordFileOff+2, RELBodyNamePtr)
	}
}
