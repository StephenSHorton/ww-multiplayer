package inject

import (
	"os"
	"path/filepath"
	"testing"
)

func TestImageFormat(t *testing.T) {
	dir := t.TempDir()
	nkit := make([]byte, 0x1000)
	copy(nkit[nkitOffset:], "NKIT")
	for name, c := range map[string]struct {
		img         []byte
		format      string
		convertible bool
	}{
		"rvz":   {append([]byte("RVZ\x01"), 0), "RVZ", true},
		"wia":   {[]byte("WIA\x01"), "WIA", true},
		"gcz":   {[]byte{0x01, 0xC0, 0x0B, 0xB1, 0, 0}, "GCZ", true},
		"nkit":  {nkit, "NKit", false},
		"iso":   {vanillaLike().build(t).img[:0x1000], "", false},
		"empty": {nil, "", false},
	} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, c.img, 0o644); err != nil {
			t.Fatal(err)
		}
		u, err := ImageFormat(p)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		switch {
		case c.format == "" && u != nil:
			t.Errorf("%s: %v", name, u)
		case c.format != "" && (u == nil || u.Format != c.format || u.Convertible() != c.convertible):
			t.Errorf("%s: %+v", name, u)
		}
	}
	if _, err := ImageFormat(filepath.Join(dir, "missing")); err == nil {
		t.Error("missing file: no error")
	}
}
