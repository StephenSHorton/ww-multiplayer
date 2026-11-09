package launch

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestReadGFXAspect: GFX.ini, then the game ini, then -C, as Dolphin layers them.
func TestReadGFXAspect(t *testing.T) {
	dir := t.TempDir()
	if a := ReadGFXAspect(dir, nil); a.Hack || a.Mode != AspectAuto {
		t.Fatalf("no files: %+v", a)
	}
	writeFile(t, filepath.Join(dir, "Config", "GFX.ini"), "[Hardware]\nAspectRatio = 9\n[Settings]\nAspectRatio = 3\nInternalResolution = 0\nwideScreenHack = True\n")
	if a := ReadGFXAspect(dir, nil); !a.Hack || a.Mode != AspectStretch {
		t.Fatalf("GFX.ini: %+v", a)
	}
	writeFile(t, filepath.Join(dir, "GameSettings", "GZLE01.ini"), "[Video_Settings]\nAspectRatio = 1\n")
	if a := ReadGFXAspect(dir, nil); !a.Hack || a.Mode != AspectForceWide {
		t.Fatalf("game ini over GFX.ini: %+v", a)
	}
	if a := ReadGFXAspect(dir, []string{"-u", dir, "-C", "GFX.Settings.wideScreenHack=False"}); a.Hack {
		t.Fatalf("-C over the files: %+v", a)
	}
}

func TestGFXAspectXScale(t *testing.T) {
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-6 }
	for _, c := range []struct {
		a    GFXAspect
		w, h int
		want float64
	}{
		{GFXAspect{Hack: false, Mode: AspectForceWide}, 0, 0, 1},
		{GFXAspect{Hack: true, Mode: AspectForceWide}, 0, 0, 0.75},
		{GFXAspect{Hack: true, Mode: AspectAuto}, 3440, 1440, 1},
		{GFXAspect{Hack: true, Mode: AspectForceStandard}, 3440, 1440, 1},
		{GFXAspect{Hack: true, Mode: AspectStretch}, 3440, 1440, (4.0 / 3.0) / (3440.0 / 1440.0)},
		{GFXAspect{Hack: true, Mode: AspectStretch}, 0, 0, 1},
		{GFXAspect{Hack: true, Mode: AspectStretch}, 600, 800, 1},
		{GFXAspect{Hack: true, Mode: AspectCustom, CustomW: 21, CustomH: 9}, 0, 0, (4.0 / 3.0) / (21.0 / 9.0)},
	} {
		if got, why := c.a.XScale(c.w, c.h); !near(got, c.want) || why == "" {
			t.Errorf("%+v %dx%d: %v (%s), want %v", c.a, c.w, c.h, got, why, c.want)
		}
	}
}
