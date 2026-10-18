package mailbox

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/mailboxgen"
)

// TestMailboxLayout is the freshness check: layout_gen.go must be what
// tools/gen_mailbox makes from today's inject/include/mailbox.h. The
// generator also refuses a header whose "// +0xNN" comments disagree
// with the struct, so a stale comment fails here too.
func TestMailboxLayout(t *testing.T) {
	src, err := os.ReadFile("../../" + mailboxgen.HeaderPath)
	if err != nil {
		t.Fatal(err)
	}
	want, err := mailboxgen.Generate(src)
	if err != nil {
		t.Fatal(err)
	}
	have, err := os.ReadFile("layout_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	have = bytes.ReplaceAll(have, []byte("\r\n"), []byte("\n"))
	if !bytes.Equal(have, want) {
		t.Errorf("wwapi/mailbox/layout_gen.go is stale against %s: run go generate ./wwapi/mailbox", mailboxgen.HeaderPath)
	}

	l, err := mailboxgen.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	mb := l.Mailbox()
	commented := strings.Count(string(src), "// +0x")
	if commented < 40 {
		t.Errorf("only %d fields carry a +0xNN comment; the parser is missing fields", commented)
	}
	if len(mb.Fields) < 70 {
		t.Errorf("Mailbox has %d fields; the parser is missing fields", len(mb.Fields))
	}
	// The mailbox (0x80412F00) must end below the docs/08 co-op region
	// at +0x200 and below __OSArenaLo (0x80414000).
	if MailboxSize > 0x200 || Mailbox+MailboxSize > PatchedArenaLo {
		t.Errorf("Mailbox is 0x%X bytes at 0x%08X: past +0x200 or the arena", MailboxSize, Mailbox)
	}
	// The tunic block (wwapi/avatar's Tunics reads OffTunicBufs..the end
	// of tunic_swaps as one block) is followed by the WWMB features word
	// and the padding that ends the mailbox at 0x1B0 (Phase E1).
	if OffTunicSwaps+4 != OffFeatures || OffFeatures+4+LenPad13 != MailboxSize || MailboxSize != 0x1B0 {
		t.Errorf("tunic_swaps ends at +0x%X, features at +0x%X, the mailbox at +0x%X; want +0x1A0, +0x1A0, +0x1B0",
			OffTunicSwaps+4, OffFeatures, MailboxSize)
	}
	// The WWMB header sits in the 8 bytes design §3 reserved for it.
	if OffHdrMagic != 0x180 || OffHdrAbi != 0x184 || OffHdrLayout != 0x186 || OffTunicBufs != 0x188 {
		t.Errorf("WWMB header at +0x%X/+0x%X/+0x%X, tunic_bufs at +0x%X", OffHdrMagic, OffHdrAbi, OffHdrLayout, OffTunicBufs)
	}
	if TunicSlotLocal >= LenTunicBufs || LenTunicEnable != LenTunicBufs {
		t.Errorf("TUNIC_SLOT_LOCAL %d outside tunic_bufs[%d] / tunic_enable[%d]", TunicSlotLocal, LenTunicBufs, LenTunicEnable)
	}
	if MaxRemoteLinks > MailboxPoseSlotCap {
		t.Errorf("MAX_REMOTE_LINKS %d > MAILBOX_POSE_SLOT_CAP %d", MaxRemoteLinks, MailboxPoseSlotCap)
	}
}

// TestLayoutMatchesHandWritten pins the generated values to the
// hand-written Go constants they replaced (main.go's mailbox blocks,
// tunic_sync.go's mailboxTunic*, this package's Mailbox, MailboxSize,
// SpawnTrigger and WarpSeq, as of Phase C step C9). It is the
// before/after diff of the switch to generated offsets, and must stay
// empty until a deliberate mailbox.h layout change updates both. Phase
// E1 grew the mailbox from 0x1A0 to 0x1B0 bytes (the WWMB features word
// and its padding after the tunic block).
func TestLayoutMatchesHandWritten(t *testing.T) {
	for _, c := range []struct {
		old       string
		was, have uint64
	}{
		{"mailboxBase", 0x80412F00, uint64(Mailbox)},
		{"mailbox.Mailbox", 0x80412F00, uint64(Mailbox)},
		{"mailbox.MailboxSize", 0x1B0, MailboxSize},
		{"mailbox.SpawnTrigger", 0x80412F00, uint64(SpawnTrigger)},
		{"mailbox.WarpSeq", 0x80412FC8, uint64(WarpSeq)},
		{"maxPuppets", 4, MaxPuppets},
		{"puppetSlotBase", 0x80412F10, uint64(Mailbox + OffPuppets)},
		{"puppetSlotSize", 0x20, PuppetSize},
		{"slotOffActor", 0x00, PuppetOffActorPtr},
		{"slotOffAct", 0x04, PuppetOffActive},
		{"slotOffPosX", 0x08, PuppetOffPosX},
		{"slotOffPosY", 0x0C, PuppetOffPosY},
		{"slotOffPosZ", 0x10, PuppetOffPosZ},
		{"slotOffRotX", 0x14, PuppetOffRotX},
		{"mailboxShadowMode", 0x90, OffShadowMode},
		{"mailboxShadowLatched", 0x91, OffShadowLatched},
		{"mailboxDbgJointNum", 0x9C, OffDbgJointNum},
		{"mailboxDbgNodeMtxPtr", 0xA0, OffDbgNodeMtxPtr},
		{"mailboxEchoDelay", 0xA4, OffEchoDelay},
		{"mailboxEchoRingState", 0xA5, OffEchoRingState},
		{"maxRemoteLinks", 2, MaxRemoteLinks},
		{"mailboxPoseBufPtr(0)", 0xA8, OffPoseBufPtrs},
		{"mailboxPoseJointCount(0)", 0xB0, OffPoseJointCounts},
		{"mailboxPoseBufState(0)", 0xB4, OffPoseBufStates},
		{"mailboxPoseSeq(0)", 0xB6, OffPoseSeqs},
		{"mailboxDbgPoseFirstWord", 0xB8, OffDbgPoseFirstWord},
		{"mailboxDbgNodeMtxFirst", 0xBC, OffDbgNodeMtxFirst},
		{"mailboxPosePublishPtr", 0xC0, OffPosePublishPtr},
		{"mailboxPosePublishJointCount", 0xC4, OffPosePublishJointCount},
		{"mailboxPosePublishState", 0xC6, OffPosePublishState},
		{"mailboxPosePublishSeq", 0xC7, OffPosePublishSeq},
		{"mailboxWarpSeq", 0xC8, OffWarpSeq},
		{"mailboxWarpAck", 0xCC, OffWarpAck},
		{"mailboxWarpX", 0xD0, OffWarpX},
		{"mailboxWarpY", 0xD4, OffWarpY},
		{"mailboxWarpZ", 0xD8, OffWarpZ},
		{"mailboxWarpDbgLinkAddr", 0xDC, OffWarpDbgLinkAddr},
		{"mailboxWarpDbgPostX", 0xE0, OffWarpDbgPostX},
		{"mailboxWarpForce", 0xE4, OffWarpForce},
		{"mailboxEyeFixStep", 0xE8, OffEyeFixStep},
		{"mailboxEyeFixMode", 0x118, OffEyeFixMode},
		{"mailboxDbgMiniLinkDataCached", 0x11C, OffDbgMiniLinkDataCached},
		{"mailboxDbgPreMcLModelData", 0x120, OffDbgPreMclmodeldata},
		{"mailboxDbgPostswapMcLModelData", 0x124, OffDbgPostswapMclmodeldata},
		{"mailboxDbgSafetyCheckCurrent", 0x128, OffDbgSafetyCheckCurrent},
		{"mailboxDbgSafetyFiredCount", 0x12C, OffDbgSafetyFiredCount},
		{"mailboxFaceStateBase", 0x130, OffFaceState},
		{"mailboxFaceStateStride", 16, FaceStateSize},
		{"mailboxFaceSeqsBase", 0x150, OffFaceSeqs},
		{"mailboxFaceHookState", 0x154, OffFaceHookState},
		{"mailboxFaceHookEnable", 0x155, OffFaceHookEnable},
		{"mailboxFaceHookSwaps", 0x158, OffFaceHookSwaps},
		{"mailboxFaceHookMpSize", 0x15C, OffFaceHookMpSize},
		{"mailboxFaceHookPatched", 0x15D, OffFaceHookPatchedCount},
		{"mailboxFaceStateLocal", 0x160, OffFaceStateLocal},
		{"mailboxInputEnable", 0x170, OffInputEnable},
		{"mailboxInputButtons", 0x172, OffInputButtons},
		{"mailboxInputStickX", 0x174, OffInputStickX},
		{"mailboxInputStickY", 0x175, OffInputStickY},
		{"mailboxInputSubstickX", 0x176, OffInputSubstickX},
		{"mailboxInputSubstickY", 0x177, OffInputSubstickY},
		{"mailboxInputTriggerL", 0x178, OffInputTriggerL},
		{"mailboxInputTriggerR", 0x179, OffInputTriggerR},
		{"mailboxInputAnalogA", 0x17A, OffInputAnalogA},
		{"mailboxInputAnalogB", 0x17B, OffInputAnalogB},
		{"mailboxInputOverrides", 0x17C, OffInputOverrides},
		{"mailboxTunicBufs", 0x188, OffTunicBufs},
		{"mailboxTunicBufSize", 0x194, OffTunicBufSize},
		{"mailboxTunicEnable", 0x198, OffTunicEnable},
		{"mailboxTunicState", 0x19B, OffTunicState},
		{"mailboxTunicSwaps", 0x19C, OffTunicSwaps},
		{"mailboxTunicEnd", 0x1A0, OffTunicSwaps + 4},
		{"tunicSlots", 3, LenTunicBufs},
		{"tunicSlotLocal", 2, TunicSlotLocal},
		{"tunicEnableCasual", 2, TunicEnableCasual},
	} {
		if c.have != c.was {
			t.Errorf("%s was 0x%X, the generated value is 0x%X", c.old, c.was, c.have)
		}
	}
}
