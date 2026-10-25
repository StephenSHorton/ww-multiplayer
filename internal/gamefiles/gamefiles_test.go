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

func TestName(t *testing.T) {
	for _, c := range []struct {
		path string
		bad  bool
	}{
		{"saves/start.sav", true},
		{"x/GZLE01.s01", true},
		{"x/GZLE01.S10", true},
		{"cards/01-GZLE-gczelda.gci", true},
		{"MemoryCardA.USA.raw", true},
		{"Roms/Wind Waker.ISO", true},
		{"a.gcm", true}, {"a.ciso", true}, {"a.rvz", true}, {"a.wia", true}, {"a.gcz", true},
		{"a.nkit.iso", true}, {"a.nkit.gcz", true}, {"a.nkit", true}, {"a.wbfs", true},
		{"inject/original.dol", true},
		{`files\rels\d_a_npc.rel`, true},
		{"res/Stage/sea/Room0.arc", true}, {"Room0.szs", true},
		{"link.bdl", true}, {"x.bmd", true}, {"x.bti", true}, {"x.bck", true}, {"x.bmg", true},
		{"sys/main.dol", true}, {"files/RELS.arc", true}, {"sys/bi2.bin", true}, {"boot.bin", true},
		{"apploader.img", true}, {"fst.bin", true}, {"opening.bnr", true},

		{"internal/inject/blob.go", false},
		{"cheats/GZLE01.ini", false},
		{"docs/img/icon.ico", false},
		{"ww-multiplayer.exe", false},
		{"ww-multiplayer-macos.zip", false},
		{"README.md", false},
		{"a.s1", false}, {"a.sx1", false}, {"release.bin", false},
		{"inject/build/WWMultiplayer.map", false},
	} {
		if got := Name(c.path); (got != "") != c.bad {
			t.Errorf("Name(%q) = %q, want flagged %v", c.path, got, c.bad)
		}
	}
}

func header(off int, mark []byte) []byte {
	h := make([]byte, HeaderLen)
	copy(h[off:], mark)
	return h
}

func TestHeader(t *testing.T) {
	gcz := make([]byte, 8)
	binary.LittleEndian.PutUint32(gcz, gczMagic)
	for _, c := range []struct {
		name string
		h    []byte
		bad  bool
	}{
		{"RARC", []byte("RARC\x00\x00\x01\x00"), true},
		{"Yaz0", []byte("Yaz0\x00\x00\x10\x00"), true},
		{"Yay0", []byte("Yay0\x00\x00\x10\x00"), true},
		{"J3D bdl", []byte("J3D2bdl4"), true},
		{"J3D bmd1", []byte("J3D1bmd1"), true},
		{"BMG", []byte("MESGbmg1\x00\x00"), true},
		{"disc", append([]byte("GZLE01\x00\x00"), make([]byte, 0x40)...), true},
		{"other disc", header(0x1C, []byte{0xC2, 0x33, 0x9F, 0x3D}), true},
		{"GCI", []byte("GZLE01\xffgczelda"), true},
		{"CISO", []byte("CISO\x00\x00\x20\x00"), true},
		{"RVZ", []byte("RVZ\x01rest"), true},
		{"WIA", []byte("WIA\x01rest"), true},
		{"GCZ", gcz, true},
		{"NKit", header(nkitOffset, []byte("NKIT")), true},

		{"empty", nil, false},
		{"short", []byte("RA"), false},
		{"exe", []byte("MZ\x90\x00"), false},
		{"zip", []byte("PK\x03\x04"), false},
		{"text", []byte("# Curated Gecko codes for GZLE01"), false},
		{"go fuzz corpus", []byte("go test fuzz v1\n[]byte(\"RARC\")"), false},
		{"NKit short", header(0, nil)[:nkitOffset+2], false},
	} {
		if got := Header(c.h); (got != "") != c.bad {
			t.Errorf("Header(%s) = %q, want flagged %v", c.name, got, c.bad)
		}
	}
}

func writeZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, b := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(b)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func paths(fs []Finding) string {
	var s []string
	for _, f := range fs {
		s = append(s, f.Path)
	}
	return strings.Join(s, " ")
}

// TestTree: a renamed RARC and a .sav are caught on disk; inside a zip
// (and a zip in that zip) a .rel and a disc header are caught too; our own
// files are not.
func TestTree(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dist")
	inner := writeZip(t, map[string][]byte{"files/rels/d_a_x.rel": {0, 0, 0, 0x58}, "ok.txt": []byte("hi")})
	files := map[string][]byte{
		"notes.txt":          []byte("the word RARC here is not at offset 0"),
		"ww-multiplayer.exe": []byte("MZ\x90\x00 ... RARC Yaz0 GZLE01 inside the exe"),
		"renamed.png":        []byte("RARC\x00\x00\x01\x00"),
		"saves/start.sav":    []byte("anything"),
		"app.zip": writeZip(t, map[string][]byte{
			"WW Multiplayer/WW Multiplayer.app/Contents/Info.plist": []byte("<plist/>"),
			"WW Multiplayer/game.bin":                               append([]byte("GZLE01"), make([]byte, 0x40)...),
			"WW Multiplayer/inner.zip":                              inner,
		}),
	}
	for name, b := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var r Result
	if err := r.Tree(dir); err != nil {
		t.Fatal(err)
	}
	got := paths(r.Findings)
	for _, want := range []string{
		"dist/renamed.png",
		"dist/saves/start.sav",
		"dist/app.zip!WW Multiplayer/game.bin",
		"dist/app.zip!WW Multiplayer/inner.zip!files/rels/d_a_x.rel",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("findings %q lack %q", got, want)
		}
	}
	for _, not := range []string{"notes.txt", "ww-multiplayer.exe", "Info.plist", "ok.txt", "dist/app.zip:"} {
		if strings.Contains(got+":", not) {
			t.Errorf("findings %q flag %q", got, not)
		}
	}
	if len(r.Findings) != 4 {
		t.Errorf("%d findings, want 4: %v", len(r.Findings), r.Findings)
	}
	// 5 files on disk, 3 entries in app.zip, 2 in inner.zip.
	if r.Checked != 10 {
		t.Errorf("checked %d, want 10", r.Checked)
	}
}

// TestFileGone: a tracked file deleted in the working tree is checked by
// its name.
func TestFileGone(t *testing.T) {
	var r Result
	if err := r.File(filepath.Join(t.TempDir(), "gone.iso"), "gone.iso"); err != nil {
		t.Fatal(err)
	}
	if len(r.Findings) != 1 || r.Checked != 1 {
		t.Errorf("got %v (checked %d), want gone.iso flagged", r.Findings, r.Checked)
	}
}

// TestBrokenZip: a file that starts like a zip and isn't one is an error,
// not a pass.
func TestBrokenZip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.zip")
	os.WriteFile(p, []byte("PK\x03\x04garbage"), 0o644)
	var r Result
	if err := r.File(p, "x.zip"); err == nil {
		t.Error("a broken zip passed")
	}
}
