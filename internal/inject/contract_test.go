package inject

import (
	"encoding/binary"
	"hash/crc32"
	"testing"

	mbox "github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// TestBlobContract: the contract constants scripts/extract_blob.py
// writes into blob.go (Phase E1) agree with T2Bytes, with the C sources
// and with wwapi/mailbox: T2End is where T2 ends, .text lies inside T2,
// BlobHash is the CRC-32 of HashRanges (the read-only sections), and the mailbox address, arena,
// ABI, layout version and features are mailbox_core.h's and build.py's.
func TestBlobContract(t *testing.T) {
	if T2End != T2Address+uint32(len(T2Bytes)) {
		t.Errorf("T2End 0x%08X, T2 is 0x%08X + 0x%X", T2End, T2Address, len(T2Bytes))
	}
	if TextStart < T2Address || TextEnd <= TextStart || TextEnd > T2End || TextStart%4 != 0 || TextEnd%4 != 0 {
		t.Fatalf(".text 0x%08X..0x%08X is not inside T2 0x%08X..0x%08X", TextStart, TextEnd, T2Address, T2End)
	}
	text := T2Bytes[TextStart-T2Address : TextEnd-T2Address]
	// HashRanges: sorted, disjoint, inside T2, and covering .text and
	// the .rodata at T2's start; BlobHash is their CRC-32 in order.
	var h uint32
	coversText, coversStart := false, false
	for i, r := range HashRanges {
		if r[0] < T2Address || r[1] <= r[0] || r[1] > T2End || (i > 0 && r[0] <= HashRanges[i-1][1]) {
			t.Fatalf("HashRanges %08X: range %d is not sorted, disjoint and inside T2 0x%08X..0x%08X", HashRanges, i, T2Address, T2End)
		}
		coversText = coversText || (r[0] <= TextStart && TextEnd <= r[1])
		coversStart = coversStart || r[0] == T2Address
		h = crc32.Update(h, crc32.IEEETable, T2Bytes[r[0]-T2Address:r[1]-T2Address])
	}
	if !coversText || !coversStart {
		t.Errorf("HashRanges %08X: covers .text %v, T2's .rodata %v", HashRanges, coversText, coversStart)
	}
	if h != BlobHash {
		t.Errorf("BlobHash 0x%08X, the CRC-32 of T2Bytes over HashRanges is 0x%08X", BlobHash, h)
	}
	if mailbox := define(t, mailboxH, "MAILBOX_ADDR"); MailboxAddr != mailbox || MailboxAddr != mbox.Mailbox {
		t.Errorf("MailboxAddr 0x%08X, mailbox_core.h 0x%08X, wwapi/mailbox 0x%08X", MailboxAddr, mailbox, mbox.Mailbox)
	}
	_, patches := buildPyFacts(t)
	if arena := patchedArenaLo(t, patches); ArenaLo != arena || ArenaLo != mbox.PatchedArenaLo {
		t.Errorf("ArenaLo 0x%08X, build.py 0x%08X, wwapi/mailbox 0x%08X", ArenaLo, arena, mbox.PatchedArenaLo)
	}
	if uint32(ABI) != define(t, mailboxH, "WWMB_ABI") || ABI != mbox.WwmbAbi || ABI == 0 {
		t.Errorf("ABI %d, mailbox_core.h WWMB_ABI %d", ABI, mbox.WwmbAbi)
	}
	if uint32(LayoutVersion) != define(t, mailboxH, "WWMB_LAYOUT") || LayoutVersion != mbox.WwmbLayout || LayoutVersion == 0 {
		t.Errorf("LayoutVersion %d, mailbox_core.h WWMB_LAYOUT %d", LayoutVersion, mbox.WwmbLayout)
	}
	if Features != define(t, mailboxH, "WW_FEATURES") || Features != mbox.WwFeatures {
		t.Errorf("Features 0x%X, mailbox_core.h WW_FEATURES 0x%X", Features, mbox.WwFeatures)
	}
	// main01_init builds the 'WWMB' magic with lis rN,0x5757 and
	// ori rN,rN,0x4D42: the committed .text must contain both halves.
	var lis, ori bool
	for i := 0; i+4 <= len(text); i += 4 {
		w := binary.BigEndian.Uint32(text[i:])
		switch {
		case w>>26 == 15 && (w>>16)&0x1F == 0 && w&0xFFFF == mbox.HeaderMagic>>16: // lis
			lis = true
		case w>>26 == 24 && w&0xFFFF == mbox.HeaderMagic&0xFFFF: // ori
			ori = true
		}
	}
	if !lis || !ori {
		t.Errorf("blob.go's .text never builds the WWMB magic (lis %v, ori %v): main01_init does not write the header", lis, ori)
	}
}
