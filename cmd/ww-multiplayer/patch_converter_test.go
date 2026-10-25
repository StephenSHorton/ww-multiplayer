package main

import (
	"slices"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
)

// TestFoundDolphins: the CLI's DolphinTool search keeps every Dolphin
// launch found, in its order (a running one first), not only the first,
// so a Dolphin without DolphinTool.exe doesn't hide one that has it.
func TestFoundDolphins(t *testing.T) {
	got := foundDolphins([]launch.Candidate{
		{Exe: `C:\Run\Dolphin.exe`, Source: launch.SourceRunning, Found: true},
		{Exe: `C:\Env\Dolphin.exe`, Source: launch.SourceEnv, Found: false},
		{Exe: `C:\Program Files\Dolphin\Dolphin.exe`, Source: launch.SourceInstalled, Found: true},
		{Exe: `C:\Users\u\Desktop\Dolphin-x64\Dolphin.exe`, Source: launch.SourcePortable, Found: true},
	})
	want := []string{`C:\Run\Dolphin.exe`, `C:\Program Files\Dolphin\Dolphin.exe`, `C:\Users\u\Desktop\Dolphin-x64\Dolphin.exe`}
	if !slices.Equal(got, want) {
		t.Errorf("foundDolphins = %q", got)
	}
}
