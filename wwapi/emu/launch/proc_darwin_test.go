//go:build darwin

package launch

import (
	"encoding/binary"
	"slices"
	"testing"
)

func procArgs2(exe string, argv, env []string, pad int) []byte {
	b := binary.LittleEndian.AppendUint32(nil, uint32(len(argv)))
	b = append(b, exe...)
	b = append(b, make([]byte, 1+pad)...)
	for _, s := range append(slices.Clone(argv), env...) {
		b = append(append(b, s...), 0)
	}
	return b
}

func TestParseProcArgs2(t *testing.T) {
	exe := "/Users/u/Applications/Dolphin.app/Contents/MacOS/Dolphin"
	argv := []string{exe, "-u", "/Users/u/Library/Application Support/Dolphin 2", "-e", "/r/WW.iso", "-C", "Dolphin.Core.MEM1Size=0x03000000"}
	for _, pad := range []int{0, 3, 7} {
		gotExe, args, ok := parseProcArgs2(procArgs2(exe, argv, []string{"HOME=/Users/u"}, pad))
		if !ok || gotExe != exe || !slices.Equal(args, argv[1:]) {
			t.Fatalf("pad %d: got %q %q %v", pad, gotExe, args, ok)
		}
		if d, ok := UserDirArg(args); !ok || d != argv[2] {
			t.Fatalf("pad %d: user dir %q %v", pad, d, ok)
		}
	}
	for _, bad := range [][]byte{nil, {1, 0, 0}, procArgs2(exe, nil, nil, 0), procArgs2(exe, argv, nil, 0)[:40]} {
		if _, _, ok := parseProcArgs2(bad); ok {
			t.Fatalf("parsed a bad buffer %q", bad)
		}
	}
}
