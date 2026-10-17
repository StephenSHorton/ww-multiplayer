package cliutil

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
)

// TestDolphinOptions: the env knobs map to emu.Options as dolphin.Find
// read them before C10, with the same errors.
func TestDolphinOptions(t *testing.T) {
	for _, c := range []struct {
		index, pid string
		want       emu.Options
		err        string
	}{
		{"", "", emu.Options{GameID: "GZLE01"}, ""},
		{"1", "", emu.Options{Index: 1, GameID: "GZLE01"}, ""},
		{"", "4242", emu.Options{PID: 4242, GameID: "GZLE01"}, ""},
		{"1", "4242", emu.Options{PID: 4242, Index: 1, GameID: "GZLE01"}, ""},
		{"-1", "", emu.Options{}, `bad WW_DOLPHIN_INDEX="-1"`},
		{"x", "4242", emu.Options{}, `bad WW_DOLPHIN_INDEX="x"`},
		{"", "pid", emu.Options{}, `bad WW_DOLPHIN_PID="pid"`},
		{"", "4294967296", emu.Options{}, `bad WW_DOLPHIN_PID="4294967296"`},
	} {
		env := map[string]string{"WW_DOLPHIN_INDEX": c.index, "WW_DOLPHIN_PID": c.pid}
		got, err := DolphinOptions(func(k string) string { return env[k] }, "GZLE01")
		if c.err != "" {
			if err == nil || err.Error() != c.err {
				t.Errorf("index=%q pid=%q: err = %v, want %q", c.index, c.pid, err, c.err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("index=%q pid=%q: got %+v, %v; want %+v", c.index, c.pid, got, err, c.want)
		}
	}
}

// TestExeCmd: the binary as help and usage lines spell it, per platform.
func TestExeCmd(t *testing.T) {
	for _, c := range []struct{ name, goos, want string }{
		{"ww-multiplayer", "windows", `.\ww-multiplayer.exe`},
		{"ww-multiplayer", "darwin", "./ww-multiplayer"},
		{"wwdev", "windows", `.\wwdev.exe`},
		{"wwdev", "linux", "./wwdev"},
	} {
		if got := exeCmd(c.name, c.goos); got != c.want {
			t.Errorf("exeCmd(%q, %q) = %q, want %q", c.name, c.goos, got, c.want)
		}
	}
}
