package main

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// parseICNS splits an .icns file into its elements (type → data).
func parseICNS(t *testing.T, b []byte) map[string][]byte {
	t.Helper()
	if len(b) < 8 || string(b[:4]) != "icns" || int(binary.BigEndian.Uint32(b[4:])) != len(b) {
		t.Fatalf("bad icns header")
	}
	m := map[string][]byte{}
	for off := 8; off < len(b); {
		if off+8 > len(b) {
			t.Fatalf("truncated element at %d", off)
		}
		n := int(binary.BigEndian.Uint32(b[off+4:]))
		if n < 8 || off+n > len(b) {
			t.Fatalf("bad element length %d at %d", n, off)
		}
		m[string(b[off:off+4])] = b[off+8 : off+n]
		off += n
	}
	return m
}

// TestCommittedICNSMatchesICO: docs/img/icon.icns (the macOS app icon) is
// generated from docs/img/icon.ico. Its full-size element must be the
// .ico's PNG byte for byte, and the smaller sizes real PNGs of the right
// size, so a changed icon.ico without `go generate` fails here.
func TestCommittedICNSMatchesICO(t *testing.T) {
	root := filepath.Join("..", "..", "docs", "img")
	ico, err := os.ReadFile(filepath.Join(root, "icon.ico"))
	if err != nil {
		t.Fatal(err)
	}
	icns, err := os.ReadFile(filepath.Join(root, "icon.icns"))
	if err != nil {
		t.Fatal(err)
	}
	src, w, err := largestPNG(ico)
	if err != nil {
		t.Fatal(err)
	}
	elems := parseICNS(t, icns)
	if !bytes.Equal(elems[icnsType[w]], src) {
		t.Fatalf("icon.icns %s element is not icon.ico's %dx%d PNG; run go generate .", icnsType[w], w, w)
	}
	for _, s := range []int{128, 32, 16} {
		data, ok := elems[icnsType[s]]
		if !ok {
			t.Errorf("icon.icns has no %dx%d (%s) element", s, s, icnsType[s])
			continue
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width != s || cfg.Height != s {
			t.Errorf("%s element: %v, %dx%d; want a %dx%d PNG", icnsType[s], err, cfg.Width, cfg.Height, s, s)
		}
	}
	// build() is deterministic for one Go version: regenerating matches.
	again, err := build(ico)
	if err != nil {
		t.Fatal(err)
	}
	if len(parseICNS(t, again)) != len(elems) {
		t.Errorf("regenerated icns has %d elements, committed %d", len(parseICNS(t, again)), len(elems))
	}
}
