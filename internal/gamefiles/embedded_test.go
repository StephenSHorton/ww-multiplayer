package gamefiles

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func put32(b []byte, off int, v uint32) { binary.BigEndian.PutUint32(b[off:], v) }

// rarc is the start of a real-shaped RARC (SArcHeader + SArcDataInfo,
// JKRArchive.h:10-31): one node, the data after the info block.
func rarc() []byte {
	b := make([]byte, 0x80)
	copy(b, "RARC")
	put32(b, 4, 0x80)    // file_length
	put32(b, 8, 0x20)    // header_length
	put32(b, 0xC, 0x40)  // file_data_offset
	put32(b, 0x10, 0x20) // file_data_length
	put32(b, 0x20, 1)    // num_nodes
	put32(b, 0x24, 0x20) // node_offset
	put32(b, 0x28, 2)    // num_file_entries
	return b
}

func yaz0() []byte {
	b := make([]byte, 0x20)
	copy(b, "Yaz0")
	put32(b, 4, 0x1234)
	b[0x10] = 0xFF
	return b
}

func yay0() []byte {
	b := make([]byte, 0x30)
	copy(b, "Yay0")
	put32(b, 4, 0x2000)
	put32(b, 8, 0x14)
	put32(b, 0xC, 0x18)
	return b
}

func bdl() []byte {
	b := make([]byte, 0x40)
	copy(b, "J3D2bdl4")
	put32(b, 8, 0x4000)
	put32(b, 0xC, 8)
	return b
}

func bmg() []byte {
	b := make([]byte, 0x20)
	copy(b, "MESGbmg1")
	put32(b, 8, 0x800)
	put32(b, 0xC, 4)
	return b
}

func disc() []byte {
	b := make([]byte, 0x440)
	copy(b, "GZLE01")
	put32(b, 0x1C, 0xC2339F3D)
	copy(b[0x20:], "THE LEGEND OF ZELDA The Wind Waker")
	return b
}

func gci() []byte {
	b := make([]byte, 0x40)
	copy(b, "GZLE01\xFF\x02gczelda")
	return b
}

func ciso() []byte {
	b := make([]byte, 0x8000)
	copy(b, "CISO")
	binary.LittleEndian.PutUint32(b[4:], 0x200000)
	b[8], b[9], b[11] = 1, 1, 1
	return b
}

func rvz() []byte {
	b := make([]byte, 0x48)
	copy(b, "RVZ\x01")
	put32(b, 4, 0x00030000)
	put32(b, 8, 0x00030000)
	put32(b, 0xC, 0xDC)
	binary.BigEndian.PutUint64(b[0x24:], 1459978240)
	return b
}

func gcz() []byte {
	b := make([]byte, 0x20)
	binary.LittleEndian.PutUint32(b, gczMagic)
	size := uint64(1459978240)
	binary.LittleEndian.PutUint64(b[0x10:], size)
	binary.LittleEndian.PutUint32(b[0x18:], 0x4000)
	binary.LittleEndian.PutUint32(b[0x1C:], uint32((size+0x3FFF)/0x4000))
	return b
}

// fakeExe is an "MZ" binary of n bytes with blob at off, the rest
// filler that holds every mark as a bare string, the way our exe holds
// them (this package's table, the patcher's format checks).
func fakeExe(n, off int, blob []byte) []byte {
	b := make([]byte, n)
	copy(b, "MZ\x90\x00")
	marks := []byte("RARC\"Yaz0\"Yay0\"J3D2bdl4\"MESGbmg1\"GZLE01\"CISO\"RVZ\x01\"WIA\x01\"\x01\xC0\x0B\xB1\x00\x00\xC2\x33\x9F\x3D")
	for i := 0x100; i+len(marks) < n; i += 0x3000 {
		copy(b[i:], marks)
	}
	copy(b[off:], blob)
	return b
}

// TestEmbedded: each format, inside an exe-like file, at the start of a
// read, in its middle and across the 1 MiB read boundary, is found; the
// bare marks alone are not.
func TestEmbedded(t *testing.T) {
	const size = 3 << 20
	for _, c := range []struct {
		name string
		blob []byte
	}{
		{"rarc", rarc()}, {"yaz0", yaz0()}, {"yay0", yay0()}, {"bdl", bdl()}, {"bmg", bmg()},
		{"disc", disc()}, {"gci", gci()}, {"ciso", ciso()}, {"rvz", rvz()}, {"gcz", gcz()},
	} {
		for _, off := range []int{0x10, 0x123457, 1<<20 - 9, 1<<20 - 0x1E, 2<<20 - 3, size - len(c.blob)} {
			w, err := Embedded(bytes.NewReader(fakeExe(size, off, c.blob)))
			if err != nil {
				t.Fatal(err)
			}
			if w == "" {
				t.Errorf("%s at 0x%X: not found", c.name, off)
			}
		}
	}
	if w, err := Embedded(bytes.NewReader(fakeExe(size, 0, nil))); w != "" || err != nil {
		t.Errorf("the bare marks: %q, %v; want none", w, err)
	}
	// Our own sources hold every mark as text (this package's tables).
	for _, f := range []string{"gamefiles.go", "embedded.go", "embedded_test.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if w, _ := Embedded(bytes.NewReader(b)); w != "" {
			t.Errorf("%s: %s", f, w)
		}
	}
}

// TestEmbeddedTruncated: a mark whose header the file cuts short is no
// finding.
func TestEmbeddedTruncated(t *testing.T) {
	b := rarc()[:0x20]
	if w, err := Embedded(bytes.NewReader(append([]byte("MZ"), b...))); w != "" || err != nil {
		t.Errorf("got %q, %v", w, err)
	}
}

// TestExeWithEmbeddedRARC is the release check on the exe
// (check_game_files ww-multiplayer.exe): a name and an "MZ" start that
// pass, and a RARC deep in its data, fail it; the same exe without it
// passes. Inside the macOS zip the same holds for the app's binary.
func TestExeWithEmbeddedRARC(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "ww-multiplayer.exe")
	os.WriteFile(bad, fakeExe(5<<20, 0x3A0001, rarc()), 0o644)
	var r Result
	if err := r.File(bad, "ww-multiplayer.exe"); err != nil {
		t.Fatal(err)
	}
	if len(r.Findings) != 1 || !strings.Contains(r.Findings[0].Reason, "RARC") || !strings.Contains(r.Findings[0].Reason, "0x3A0001") {
		t.Errorf("findings %v, want the RARC at 0x3A0001", r.Findings)
	}

	good := filepath.Join(dir, "clean.exe")
	os.WriteFile(good, fakeExe(5<<20, 0, nil), 0o644)
	r = Result{}
	if err := r.File(good, "clean.exe"); err != nil || len(r.Findings) != 0 {
		t.Errorf("clean exe: %v, %v", r.Findings, err)
	}

	z := filepath.Join(dir, "ww-multiplayer-macos.zip")
	os.WriteFile(z, writeZip(t, map[string][]byte{
		"WW Multiplayer/WW Multiplayer.app/Contents/MacOS/ww-multiplayer": fakeExe(2<<20, 0x1FFFF0, yaz0()),
		"WW Multiplayer/LICENSE": []byte("MIT"),
	}), 0o644)
	r = Result{}
	if err := r.File(z, "ww-multiplayer-macos.zip"); err != nil {
		t.Fatal(err)
	}
	if got := paths(r.Findings); got != "ww-multiplayer-macos.zip!WW Multiplayer/WW Multiplayer.app/Contents/MacOS/ww-multiplayer" {
		t.Errorf("findings %q", got)
	}
}

// TestBigNestedZip: a nested zip past the in-memory size is still opened
// (from a temporary file), so an .iso in it is found.
func TestBigNestedZip(t *testing.T) {
	old := memNested
	memNested = 1 << 10
	defer func() { memNested = old }()

	var ib bytes.Buffer
	zw := zip.NewWriter(&ib)
	w, _ := zw.CreateHeader(&zip.FileHeader{Name: "pad.bin", Method: zip.Store})
	w.Write(bytes.Repeat([]byte{0x55}, 8<<10))
	w, _ = zw.Create("Roms/game.iso")
	w.Write([]byte("x"))
	zw.Close()

	p := filepath.Join(t.TempDir(), "outer.zip")
	os.WriteFile(p, writeZip(t, map[string][]byte{"inner.zip": ib.Bytes()}), 0o644)
	var r Result
	if err := r.File(p, "outer.zip"); err != nil {
		t.Fatal(err)
	}
	if got := paths(r.Findings); got != "outer.zip!inner.zip!Roms/game.iso" {
		t.Errorf("findings %q, want the nested .iso", got)
	}
}
