package inject

import (
	"os"
	"path/filepath"
	"strings"
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

// TestConvertStepsMac: Dolphin for macOS ships no DolphinTool, so the Mac
// advice says so and gives the Mac's steps; elsewhere it is unchanged.
func TestConvertStepsMac(t *testing.T) {
	old := hostOS
	t.Cleanup(func() { hostOS = old })
	for _, c := range []struct {
		goos      string
		want, not []string
	}{
		{"darwin", []string{"comes without DolphinTool", "Control-click", "Convert File... > Format: ISO", "under Paths", "patch the .iso it writes"}, nil},
		{"windows", []string{"right-click the game > Convert File... > Format: ISO", "patch the .iso it writes"}, []string{"macOS", "Control-click"}},
		{"linux", []string{"right-click the game"}, []string{"macOS"}},
	} {
		hostOS = c.goos
		got := (&UnsupportedFormatError{Format: "RVZ"}).Error()
		if !strings.HasPrefix(got, "iso: this is a compressed RVZ image, not a plain ISO. ") || got != "iso: this is a compressed RVZ image, not a plain ISO. "+ConvertSteps(c.goos) {
			t.Errorf("%s: %s", c.goos, got)
		}
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: no %q in %s", c.goos, w, got)
			}
		}
		for _, w := range c.not {
			if strings.Contains(got, w) {
				t.Errorf("%s: %q in %s", c.goos, w, got)
			}
		}
	}
	hostOS = "darwin"
	if got := (&UnsupportedFormatError{Format: "NKit"}).Error(); strings.Contains(got, "DolphinTool") {
		t.Errorf("NKit takes the NKit advice on a Mac too: %s", got)
	}
}
