package launch

import (
	"math"
	"os"
	"path/filepath"
	"reflect"
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

// TestReadGFXAspect: GFX.ini, then -C, then the game inis, as Dolphin layers them.
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
	if a := ReadGFXAspect(dir, []string{"-u", dir, "-C", "Graphics.Settings.wideScreenHack=False"}); a.Hack {
		t.Fatalf("-C over GFX.ini: %+v", a)
	}
	if a := ReadGFXAspect(dir, []string{"--config=Graphics.Settings.AspectRatio=2"}); a.Mode != AspectForceWide {
		t.Fatalf("the game ini over -C: %+v", a)
	}
	if a := ReadGFXAspect(dir, []string{"--config=Graphics.Settings.wideScreenHack=False"}); a.Hack {
		t.Fatalf("--config= over GFX.ini: %+v", a)
	}
	writeFile(t, filepath.Join(dir, "GameSettings", "GZLE01r0.ini"), "[Video_Settings]\nAspectRatio = 2\n")
	if a := ReadGFXAspect(dir, nil); a.Mode != AspectForceStandard {
		t.Fatalf("GZLE01r0.ini last: %+v", a)
	}
}

// TestReadGFXAspectDolphinNames: GFX.* is dropped, SuggestedAspectRatio
// replaces Auto, the custom size defaults to 1:1.
func TestReadGFXAspectDolphinNames(t *testing.T) {
	dir := t.TempDir()
	if a := ReadGFXAspect(dir, []string{"-C", "GFX.Settings.wideScreenHack=True"}); a.Hack {
		t.Fatalf("GFX.* must be ignored: %+v", a)
	}
	if a := ReadGFXAspect(dir, []string{"-C", "graphics.settings.wideScreenHack=True"}); !a.Hack {
		t.Fatalf("Graphics.* (any case) must apply: %+v", a)
	}
	if a := ReadGFXAspect(dir, nil); a.CustomW != 1 || a.CustomH != 1 {
		t.Fatalf("custom default: %+v", a)
	}
	writeFile(t, filepath.Join(dir, "Config", "GFX.ini"), "[Settings]\nSuggestedAspectRatio = 1\n")
	if a := ReadGFXAspect(dir, nil); a.Mode != AspectForceWide {
		t.Fatalf("suggested over Auto: %+v", a)
	}
	writeFile(t, filepath.Join(dir, "Config", "GFX.ini"), "[Settings]\nSuggestedAspectRatio = 1\nAspectRatio = 2\n")
	if a := ReadGFXAspect(dir, nil); a.Mode != AspectForceStandard {
		t.Fatalf("explicit mode beats suggested: %+v", a)
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

func TestParseWidescreenSetting(t *testing.T) {
	for _, c := range []struct {
		in      string
		w, h    int
		wantErr bool
	}{
		{"", 0, 0, false}, {"off", 0, 0, false}, {" OFF ", 0, 0, false},
		{"16:9", 16, 9, false}, {"16:10", 16, 10, false}, {"21:9", 21, 9, false},
		{"4:3", 0, 0, false}, {"5:4", 0, 0, false}, {"64:27", 64, 27, false},
		{"32:9", 32, 9, false}, {"5:1", 0, 0, true}, {"2.39", 0, 0, true}, {"a:b", 0, 0, true}, {"16:0", 0, 0, true}, {"-16:9", 0, 0, true},
	} {
		s, err := ParseWidescreenSetting(c.in)
		if (err != nil) != c.wantErr || s.W != c.w || s.H != c.h {
			t.Errorf("%q: %+v, %v", c.in, s, err)
		}
	}
	s, _ := ParseWidescreenSetting("21:9")
	if math.Abs(s.Factor()-1.75) > 1e-9 {
		t.Errorf("21:9 factor %v", s.Factor())
	}
	if (WidescreenSetting{}).Factor() != 1 {
		t.Error("off factor")
	}
}

func TestWidescreenArgs(t *testing.T) {
	s16, _ := ParseWidescreenSetting("16:9")
	want := []string{"-C", "Graphics.Settings.wideScreenHack=False", "-C", "Graphics.Settings.AspectRatio=1"}
	if got := WidescreenArgs(s16); !reflect.DeepEqual(got, want) {
		t.Errorf("16:9: %v", got)
	}
	s, _ := ParseWidescreenSetting("21:9")
	if got := WidescreenArgs(s); len(got) != 8 || got[5] != "Graphics.Settings.CustomAspectRatioWidth=21" || got[7] != "Graphics.Settings.CustomAspectRatioHeight=9" {
		t.Errorf("21:9: %v", got)
	}
	if WidescreenArgs(WidescreenSetting{}) != nil {
		t.Error("off must add nothing")
	}
	// What the args say reads back as the same picture.
	if a := ReadGFXAspect(t.TempDir(), WidescreenArgs(s)); a.Hack || a.Mode != AspectCustom || a.CustomW != 21 {
		t.Errorf("round trip: %+v", a)
	}
}

// TestVIAspect: the recorded VI aspect sits inside the measured range
// (WS-11), the range is 1.2524..1.2543, and the 1x dump agrees.
func TestVIAspect(t *testing.T) {
	if VIAspect < VIAspectMin || VIAspect > VIAspectMax {
		t.Errorf("VIAspect %.4f outside %.4f..%.4f", VIAspect, VIAspectMin, VIAspectMax)
	}
	if math.Abs(VIAspectMin-1.2524) > 0.00005 || math.Abs(VIAspectMax-1.2543) > 0.00005 {
		t.Errorf("range %.5f..%.5f, want 1.2524..1.2543", VIAspectMin, VIAspectMax)
	}
	// 640 wide at 1x: 640/VIAspect lines, rounded up to a multiple of 4, is 512.
	if h := int(math.Ceil(640/VIAspect/4)) * 4; h != 512 {
		t.Errorf("1x dump height %d, want 512", h)
	}
}
