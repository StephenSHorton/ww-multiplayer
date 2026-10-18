package mailbox

import (
	"encoding/binary"
	"fmt"
)

// The REL module's words in the mailbox slack (inject/rel/rel_main.c
// WWREL_*, inject/rel/README.md): the Mailbox struct ends at
// Mailbox+MailboxSize and the patched arena starts at PatchedArenaLo, so
// the words between belong to nobody, and the mod's REL module (shipped
// in RELS.arc by the patcher, loaded by the game's own dynamic linker)
// uses the first three. Its _prolog writes RELMagic at +OffRELMagic and
// its build id at +OffRELBuildID and zeroes +OffRELFrame, which its
// actor's execute then bumps every frame; its _epilog clears the magic.
// So the magic says the module is linked and the frame counter moving
// says its actor runs. These duplicate internal/inject's RELMagic,
// RELMagicOff, RELBuildIDOff and RELFrameOff (blob.go, generated from
// rel_main.c), which a test there pins equal; wwapi/mailbox imports no
// generated blob.
const (
	RELMagic      uint32 = 0x5757524C // 'WWRL'
	OffRELMagic   uint32 = 0x1B0
	OffRELBuildID uint32 = 0x1B4
	OffRELFrame   uint32 = 0x1B8
	// relWordsLen is the three words' span.
	relWordsLen = OffRELFrame + 4 - OffRELMagic
)

// REL is what the slack words say about the REL module.
type REL struct {
	Linked  bool   // the magic is there: a module with these words is linked
	Magic   uint32 // the word read, whatever it is
	BuildID uint32 // the linked module's build id; 0 when !Linked
	Frames  uint32 // its actor's frame counter; 0 when !Linked
}

// ReadREL reads the REL module's words from the mailbox slack.
func ReadREL(m Reader) (REL, error) {
	b, err := m.Read(Mailbox+OffRELMagic, int(relWordsLen))
	if err != nil {
		return REL{}, fmt.Errorf("mailbox: REL words: %w", err)
	}
	if len(b) != int(relWordsLen) {
		return REL{}, fmt.Errorf("mailbox: REL words: %d bytes, want %d", len(b), relWordsLen)
	}
	r := REL{Magic: binary.BigEndian.Uint32(b)}
	if r.Magic == RELMagic {
		r.Linked = true
		r.BuildID = binary.BigEndian.Uint32(b[OffRELBuildID-OffRELMagic:])
		r.Frames = binary.BigEndian.Uint32(b[OffRELFrame-OffRELMagic:])
	}
	return r, nil
}

// WriteREL writes r's words the way the module's _prolog does (the
// magic when Linked, else zero, which is what _epilog leaves). It is for
// fakes and tests; nothing writes a live game's words but the REL.
func WriteREL(m Writer, r REL) error {
	b := make([]byte, relWordsLen)
	if r.Linked {
		binary.BigEndian.PutUint32(b, RELMagic)
	}
	binary.BigEndian.PutUint32(b[OffRELBuildID-OffRELMagic:], r.BuildID)
	binary.BigEndian.PutUint32(b[OffRELFrame-OffRELMagic:], r.Frames)
	if err := m.Write(Mailbox+OffRELMagic, b); err != nil {
		return fmt.Errorf("mailbox: REL words: %w", err)
	}
	return nil
}
