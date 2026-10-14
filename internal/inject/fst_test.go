package inject

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestFSTFileExtents(t *testing.T) {
	files := []testFile{
		{name: "a.arc", off: 0x100000, size: 0x2000},
		{name: "b.arc", off: 0x200000, size: 0},
		{name: "c.arc", off: 0xFFFFFFF0, size: 0x20}, // end doesn't fit in 32 bits
	}
	got, err := fstFileExtents(buildFST(files, 0))
	if err != nil {
		t.Fatal(err)
	}
	want := []extent{
		{start: 0x100000, end: 0x102000, what: `file "a.arc"`},
		{start: 0xFFFFFFF0, end: 0x100000010, what: `file "c.arc"`},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("extents = %v, want %v (directories and empty files skipped)", got, want)
	}

	bad := map[string][]byte{
		"too small":          make([]byte, 8),
		"root not directory": buildFST(files, 0),
		"count past end":     buildFST(files, 0),
		"zero entries":       buildFST(files, 0),
	}
	bad["root not directory"][0] = 0
	put32(bad["count past end"], 8, 1000)
	put32(bad["zero entries"], 8, 0)
	for name, fst := range bad {
		if _, err := fstFileExtents(fst); err == nil {
			t.Errorf("%s: parsed without error", name)
		}
	}
}

func TestFSTNameFallback(t *testing.T) {
	fst := buildFST([]testFile{{name: "x", off: 0x1000, size: 1}}, 0)
	put32(fst, 2*fstEntrySize, 0x00FFFFFF) // name offset past the string table
	got, err := fstFileExtents(fst)
	if err != nil || len(got) != 1 || got[0].what != "file #2" {
		t.Fatalf("got %v, %v; want one extent named by index", got, err)
	}
}

func TestFindFreeSlot(t *testing.T) {
	const align = 0x10000
	used := []extent{
		{start: 0x380000, end: 0x390000},
		{start: 0x300000, end: 0x320000}, // unsorted on purpose
		{start: 0x310000, end: 0x345000}, // overlaps the previous one
		{start: 0x500000, end: 0x500000}, // empty: blocks nothing
	}
	cases := []struct {
		name                string
		size, lo, limit, pf uint64
		want                uint64
		ok                  bool
	}{
		{"preferred free", 0x8000, 0x300000, 0x600000, 0x500000, 0x500000, true},
		{"preferred used", 0x8000, 0x300000, 0x600000, 0x385000, 0x350000, true},
		{"preferred below lo", 0x8000, 0x300000, 0x600000, 0x100000, 0x350000, true},
		{"preferred past limit", 0x8000, 0x300000, 0x504000, 0x500000, 0x350000, true},
		{"gap too small", 0x40000, 0x300000, 0x600000, 0x385000, 0x390000, true},
		{"lo inside a used run", 0x8000, 0x318000, 0x600000, 0x0, 0x350000, true},
		{"only after the last extent", 0x100000, 0x300000, 0x490000, 0x0, 0x390000, true},
		{"nothing fits", 0x100000, 0x300000, 0x48FFFF, 0x0, 0, false},
	}
	for _, c := range cases {
		got, ok := findFreeSlot(used, c.size, align, c.lo, c.limit, c.pf)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: got 0x%x, %v; want 0x%x, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestDetectUnsupportedFormat(t *testing.T) {
	nkit := vanillaLike().build(t).img[:0x1000]
	copy(nkit[nkitOffset:], "NKIT")
	cases := map[string][]byte{
		"RVZ":  []byte("RVZ\x01\x00\x00\x00\x00"),
		"WIA":  []byte("WIA\x01\x00\x00\x00\x00"),
		"GCZ":  {0x01, 0xC0, 0x0B, 0xB1, 0, 0, 0, 0},
		"NKit": nkit,
	}
	for format, b := range cases {
		err := detectUnsupportedFormat(b)
		var ufe *UnsupportedFormatError
		if !errors.As(err, &ufe) || ufe.Format != format {
			t.Errorf("%s: err = %v, want UnsupportedFormatError{%s}", format, err, format)
			continue
		}
		// Through the full patch path too, ahead of the game-ID check.
		if _, err := PatchISOInMemory(b); !errors.As(err, &ufe) {
			t.Errorf("%s: PatchISOInMemory err = %v, want UnsupportedFormatError", format, err)
		}
		hint := "Convert File"
		if format == "NKit" {
			hint = "NKit tool"
		}
		if !strings.Contains(err.Error(), hint) {
			t.Errorf("%s: message %q lacks %q", format, err, hint)
		}
	}

	for _, ok := range [][]byte{vanillaLike().build(t).img[:0x440], []byte("CISO"), nil} {
		if err := detectUnsupportedFormat(ok); err != nil {
			t.Errorf("%q...: %v", ok[:min(len(ok), 6)], err)
		}
	}
}
