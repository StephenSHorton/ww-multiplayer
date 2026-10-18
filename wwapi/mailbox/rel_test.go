package mailbox

import (
	"encoding/binary"
	"testing"
)

// fakeWords is a Reader/Writer over a few words at the mailbox.
type fakeWords struct{ b map[uint32]byte }

func (f *fakeWords) Read(a uint32, n int) ([]byte, error) {
	out := make([]byte, n)
	for i := range out {
		out[i] = f.b[a+uint32(i)]
	}
	return out, nil
}

func (f *fakeWords) Write(a uint32, b []byte) error {
	if f.b == nil {
		f.b = map[uint32]byte{}
	}
	for i, c := range b {
		f.b[a+uint32(i)] = c
	}
	return nil
}

// TestRELSlack: the REL module's words lie in the slack past the Mailbox
// struct and below the patched arena (the canary region CanaryArena), in
// order, and the magic spells 'WWRL'.
func TestRELSlack(t *testing.T) {
	if OffRELMagic < MailboxSize {
		t.Errorf("OffRELMagic 0x%X is inside the Mailbox struct (0x%X bytes)", OffRELMagic, MailboxSize)
	}
	if OffRELBuildID != OffRELMagic+4 || OffRELFrame != OffRELBuildID+4 {
		t.Errorf("REL words not consecutive: 0x%X 0x%X 0x%X", OffRELMagic, OffRELBuildID, OffRELFrame)
	}
	if end := Mailbox + OffRELFrame + 4; end > PatchedArenaLo || !CanaryArena.Contains(Mailbox+OffRELMagic, int(relWordsLen)) {
		t.Errorf("REL words 0x%08X..0x%08X are not in the slack below the arena 0x%08X", Mailbox+OffRELMagic, end, PatchedArenaLo)
	}
	if RELMagic != 0x5757524C || string(binary.BigEndian.AppendUint32(nil, RELMagic)) != "WWRL" {
		t.Errorf("RELMagic 0x%08X is not 'WWRL'", RELMagic)
	}
}

// TestReadWriteREL: an unlinked module (zero words, as before the prolog
// and after the epilog) reads as not linked with the magic it found;
// written words read back; a cleared magic hides the other words.
func TestReadWriteREL(t *testing.T) {
	m := &fakeWords{}
	r, err := ReadREL(m)
	if err != nil || r != (REL{}) {
		t.Fatalf("zero words: %+v, %v", r, err)
	}
	want := REL{Linked: true, BuildID: 0xAC19DE43, Frames: 1234}
	if err := WriteREL(m, want); err != nil {
		t.Fatal(err)
	}
	if r, err = ReadREL(m); err != nil || r != (REL{Linked: true, Magic: RELMagic, BuildID: 0xAC19DE43, Frames: 1234}) {
		t.Errorf("written words: %+v, %v", r, err)
	}
	if err := WriteREL(m, REL{BuildID: 0xAC19DE43, Frames: 1234}); err != nil {
		t.Fatal(err)
	}
	if r, err = ReadREL(m); err != nil || r.Linked || r.Magic != 0 || r.BuildID != 0 || r.Frames != 0 {
		t.Errorf("cleared magic: %+v, %v", r, err)
	}
	if err := m.Write(Mailbox+OffRELMagic, []byte{0xDE, 0xAD, 0xBE, 0xEF}); err != nil {
		t.Fatal(err)
	}
	if r, err = ReadREL(m); err != nil || r.Linked || r.Magic != 0xDEADBEEF || r.BuildID != 0 {
		t.Errorf("another magic: %+v, %v", r, err)
	}
}
