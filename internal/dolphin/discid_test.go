package dolphin

import (
	"encoding/binary"
	"testing"
)

// TestDiscID: the game ID at the start of emulated RAM counts only with
// the GameCube disc magic beside it.
func TestDiscID(t *testing.T) {
	hdr := func(id string, magic uint32) []byte {
		b := make([]byte, 0x20)
		copy(b, id)
		binary.BigEndian.PutUint32(b[0x1C:], magic)
		return b
	}
	for _, c := range []struct {
		hdr  []byte
		want string
		ok   bool
	}{
		{hdr("GZLE01", gcDiscMagic), "GZLE01", true},
		{hdr("GALE01", gcDiscMagic), "GALE01", true},
		{hdr("GZLE01", 0), "", false},                 // no magic: not a disc header
		{hdr("gz\x00\x0001", gcDiscMagic), "", false}, // not an ID
		{make([]byte, 0x20), "", false},
		{hdr("GZLE01", gcDiscMagic)[:0x10], "", false},
	} {
		if got, ok := discID(c.hdr); got != c.want || ok != c.ok {
			t.Errorf("discID(%q) = %q, %v; want %q, %v", c.hdr, got, ok, c.want, c.ok)
		}
	}
}
