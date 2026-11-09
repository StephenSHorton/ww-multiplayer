package widescreen

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGeckoEnabled(t *testing.T) {
	write := func(t *testing.T, dir, rel, body string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const ticked = "[Gecko_Enabled]\r\n$16:9 Widescreen\r\n$Other\r\n"
	const cheatsOn = "[General]\nX = 1\n[Core]\nEnableCheats = True\n"
	for _, c := range []struct {
		name  string
		files map[string]string
		args  []string
		want  bool
	}{
		{"nothing", nil, nil, false},
		{"ticked, cheats on", map[string]string{"GameSettings/GZLE01.ini": ticked, "Config/Dolphin.ini": cheatsOn}, nil, true},
		{"ticked in GZL.ini", map[string]string{"GameSettings/GZL.ini": ticked, "Config/Dolphin.ini": cheatsOn}, nil, true},
		{"ticked in r0", map[string]string{"GameSettings/GZLE01r0.ini": ticked, "Config/Dolphin.ini": cheatsOn}, nil, true},
		{"ticked, cheats off", map[string]string{"GameSettings/GZLE01.ini": ticked, "Config/Dolphin.ini": "[Core]\nEnableCheats = False\n"}, nil, false},
		{"ticked, no Dolphin.ini", map[string]string{"GameSettings/GZLE01.ini": ticked}, nil, false},
		{"ticked, cheats from -C", map[string]string{"GameSettings/GZLE01.ini": ticked}, []string{"-u", "x", "-C", "Dolphin.Core.EnableCheats=True"}, true},
		{"ticked, --config= off over the ini", map[string]string{"GameSettings/GZLE01.ini": ticked, "Config/Dolphin.ini": cheatsOn}, []string{"--config=Dolphin.Core.EnableCheats=False"}, false},
		{"ticked, game ini turns cheats on", map[string]string{"GameSettings/GZLE01.ini": "[Core]\nEnableCheats = True\n" + ticked}, nil, true},
		{"cheats on, other code only", map[string]string{"GameSettings/GZLE01.ini": "[Gecko_Enabled]\n$Other\n", "Config/Dolphin.ini": cheatsOn}, nil, false},
		{"under [Gecko] only", map[string]string{"GameSettings/GZLE01.ini": "[Gecko]\n$16:9 Widescreen\n", "Config/Dolphin.ini": cheatsOn}, nil, false},
		{"disabled later", map[string]string{"GameSettings/GZL.ini": ticked, "GameSettings/GZLE01.ini": "[Gecko_Disabled]\n$16:9 Widescreen\n", "Config/Dolphin.ini": cheatsOn}, nil, false},
	} {
		dir := t.TempDir()
		for rel, body := range c.files {
			write(t, dir, rel, body)
		}
		if got := GeckoEnabled(dir, c.args); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	if GeckoEnabled("", nil) {
		t.Error("no user dir")
	}
}
