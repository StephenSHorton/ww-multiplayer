package mailbox

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// TestMailboxFields checks the mailbox fields against the mailbox base
// (mailbox.h).
func TestMailboxFields(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want uint32
	}{
		{"SpawnTrigger", SpawnTrigger, Mailbox},
		{"WarpSeq", WarpSeq, Mailbox + 0xC8},
	} {
		if c.got != c.want {
			t.Errorf("%s = 0x%08X, want 0x%08X", c.name, c.got, c.want)
		}
	}
}

// TestEverythingInMEM1 guards against an address that slipped a digit.
func TestEverythingInMEM1(t *testing.T) {
	all := map[string]uint32{
		"T2Base": T2Base, "Mailbox": Mailbox, "PatchedArenaLo": PatchedArenaLo,
	}
	for name, a := range all {
		if a < addr.MEM1Base || a >= addr.MEM1End {
			t.Errorf("%s = 0x%08X is outside MEM1", name, a)
		}
	}
}

// TestModRegions pins the mod's RAM layout (§4.5) to the blob it ships
// with: T2 must end at or before the mailbox, the mailbox must end at or
// before the patched arena, and the canary regions must sit in the gaps.
func TestModRegions(t *testing.T) {
	if T2Base != inject.T2Address {
		t.Fatalf("T2Base 0x%08X != inject.T2Address 0x%08X", T2Base, inject.T2Address)
	}
	t2End := T2Base + uint32(len(inject.T2Bytes))
	if t2End > Mailbox {
		t.Errorf("T2 ends at 0x%08X, past the mailbox at 0x%08X", t2End, Mailbox)
	}
	if Mailbox+MailboxSize > PatchedArenaLo {
		t.Errorf("mailbox ends past the arena")
	}
	if MainStack.End != MainStackHeader.Start || MainStackHeader.End != CanaryTRK.Start || MainStack.Size() != 0x10000 {
		t.Errorf("main stack %08X-%08X and its header %08X-%08X do not end where the TRK canary starts",
			MainStack.Start, MainStack.End, MainStackHeader.Start, MainStackHeader.End)
	}
	if BootLRSlot != MainStack.End+4 || !MainStackHeader.Contains(BootLRSlot, 4) || CanaryTRK.Contains(BootLRSlot, 4) {
		t.Errorf("boot LR slot 0x%08X is not 4(_stack_addr) inside the stack header", BootLRSlot)
	}
	if CanaryTRK.End != T2Base || CanaryArena.Start != Mailbox+MailboxSize || CanaryArena.End != PatchedArenaLo {
		t.Error("canary regions do not border T2, the mailbox and the arena")
	}
	if CanaryTRK.Size() != 0x3050 || CanaryArena.Size() != 0xF60 {
		t.Errorf("canary sizes 0x%X / 0x%X, want 0x3050 / 0xF60 (§4.5)", CanaryTRK.Size(), CanaryArena.Size())
	}
	for _, r := range []addr.Region{CanaryTRK, CanaryArena} {
		if r.Contains(SpawnTrigger, 4) || r.Contains(T2Base, 4) {
			t.Errorf("%s overlaps the mod", r.Name)
		}
	}
	if !CanaryArena.Contains(CanaryArena.Start, int(CanaryArena.Size())) || CanaryArena.Contains(CanaryArena.Start, int(CanaryArena.Size())+1) {
		t.Error("Region.Contains bounds are off by one")
	}
}
