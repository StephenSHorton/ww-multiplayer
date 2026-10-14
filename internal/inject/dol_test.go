package inject

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestPatchDOLSplicesT2(t *testing.T) {
	vanilla := testDOL()
	out, err := PatchDOL(vanilla)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(vanilla)+len(T2Bytes) {
		t.Fatalf("patched DOL is 0x%x bytes, want 0x%x", len(out), len(vanilla)+len(T2Bytes))
	}
	if off, addr, size := be32(out, 0x08), be32(out, 0x50), be32(out, 0x98); off != uint32(len(vanilla)) ||
		addr != T2Address || size != uint32(len(T2Bytes)) {
		t.Fatalf("slot 2 = off 0x%x addr 0x%08x size 0x%x", off, addr, size)
	}
	if !bytes.Equal(vanilla, testDOL()) {
		t.Fatal("PatchDOL modified its input")
	}
	if _, err := PatchDOL(out); !errors.Is(err, ErrAlreadyPatched) {
		t.Fatalf("re-patch: err = %v, want ErrAlreadyPatched", err)
	}
}

func TestPatchDOLDetectsOtherVersion(t *testing.T) {
	current, err := PatchDOL(testDOL())
	if err != nil {
		t.Fatal(err)
	}
	t2 := len(testDOL())
	cases := map[string]func(d []byte) []byte{
		"different T2 byte": func(d []byte) []byte {
			d[t2+len(T2Bytes)-1] ^= 0x01
			return d
		},
		"shorter T2": func(d []byte) []byte {
			put32(d, 0x98, uint32(len(T2Bytes)-0x10))
			return d[:len(d)-0x10]
		},
		"different hook site": func(d []byte) []byte {
			p := DOLPatches[0]
			d[p.FileOff] ^= 0xFF
			return d
		},
		"T2 section past end of DOL": func(d []byte) []byte {
			put32(d, 0x08, uint32(len(d)))
			return d
		},
	}
	for name, mutate := range cases {
		d := mutate(bytes.Clone(current))
		if _, err := PatchDOL(d); !errors.Is(err, ErrPatchedByOtherVersion) {
			t.Errorf("%s: err = %v, want ErrPatchedByOtherVersion", name, err)
		}
	}
}

func TestExtractDOLBounds(t *testing.T) {
	iso := make([]byte, 0x40000)
	const dolOff = 0x10000
	cases := []struct {
		name      string
		off, size uint32
		wantErr   string
	}{
		{"32-bit wrap", 0x7FFFF800, 0x7FFFF800, "more than fits in GameCube RAM"},
		{"just over 24 MiB", 0x100, dolMaxSize, "more than fits in GameCube RAM"},
		{"past FST", 0x100, 0x20000, "past FST offset"},
	}
	for _, c := range cases {
		clear(iso)
		put32(iso, dolOff+0x18, c.off) // T6
		put32(iso, dolOff+0xA8, c.size)
		_, err := extractDOL(iso, dolOff, 0x30000)
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.wantErr)
		}
	}

	clear(iso)
	put32(iso, dolOff+0x1C+4*10, 0x100) // D10, the last data slot
	put32(iso, dolOff+0xAC+4*10, 0x1F00)
	dol, err := extractDOL(iso, dolOff, 0x30000)
	if err != nil || len(dol) != 0x2000 {
		t.Fatalf("valid DOL: len 0x%x err %v, want len 0x2000", len(dol), err)
	}
}
