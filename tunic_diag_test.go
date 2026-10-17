package main

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar/avatartest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gfx/texture"
)

func TestReadNameTab_DerefsJUTNameTab(t *testing.T) {
	f := avatartest.NewRAM()
	const obj, res = 0x80D00000, 0x80D10000
	names := []string{"linktexS3TC", "eye", "linktexS3TC"}
	f.Put32(obj, 0x803A0000) // vtable
	f.Put32(obj+4, res)
	f.Put32(obj+8, res+4+4*uint32(len(names)))
	f.Write(obj+0xC, []byte{0, byte(len(names))})
	f.Write(res, []byte{0, byte(len(names)), 0xFF, 0xFF})
	off := 4 + 4*len(names)
	for i, n := range names {
		var e [4]byte
		binary.BigEndian.PutUint16(e[2:], uint16(off))
		f.Write(res+4+4*uint32(i), e[:])
		f.Write(res+uint32(off), append([]byte(n), 0))
		off += len(n) + 1
	}
	got := readNameTab(f, obj, len(names))
	if fmt.Sprint(got) != fmt.Sprint(names) {
		t.Fatalf("names = %q, want %q", got, names)
	}
	if spare, used, ok := texture.FindLinktex(got); !ok || spare != 0 || used != 2 {
		t.Errorf("FindLinktex = %d %d %v", spare, used, ok)
	}
	// The old bug: reading the object itself as the ResNTAB.
	if bad := readNameTab(f, res, len(names)); bad[0] != "" {
		t.Errorf("a ResNTAB passed as a JUTNameTab gave %q", bad)
	}
}
