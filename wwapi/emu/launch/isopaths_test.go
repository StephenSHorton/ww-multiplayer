package launch

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestISOArg(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
		ok   bool
	}{
		{LaunchArgs("/u", "/g/ww.iso", ""), "/g/ww.iso", true},
		{[]string{"--exec=/a.iso"}, "/a.iso", true},
		{[]string{"-e/a.iso", "--exec", "/b.iso"}, "/b.iso", true},
		{[]string{"-u", "/u"}, "", false},
		{[]string{"-e"}, "", false},
	} {
		if got, ok := ISOArg(c.args); got != c.want || ok != c.ok {
			t.Errorf("%q: %q %v", c.args, got, ok)
		}
	}
}

func TestHasLaunchArgs(t *testing.T) {
	if !HasLaunchArgs(LaunchArgs("/u", "/g/ww.iso", "/s.sav")) {
		t.Error("Launch's own command line")
	}
	for _, args := range [][]string{
		{"-u", "/u", "-e", "/g/ww.iso"},                        // no MEM1 override: started by hand
		append([]string{"-e", "/g/ww.iso"}, MEM1Override...),   // no -u
		append([]string{"-u", "/u"}, MEM1Override...),          // no disc
		{"-u", "/u", "-e", "/g/ww.iso", "-C", MEM1Override[1]}, // half the override
		nil,
	} {
		if HasLaunchArgs(args) {
			t.Errorf("%q counted as Launch's", args)
		}
	}
}

func TestISOPaths(t *testing.T) {
	u := t.TempDir()
	if d, r := ISOPaths(u); d != nil || r {
		t.Fatalf("no ini: %q %v", d, r)
	}
	os.MkdirAll(filepath.Join(u, "Config"), 0o755)
	ini := "[Core]\nISOPath0 = /not/general\n[General]\nISOPaths = 3\nISOPath1 = Games/GC\nISOPath0 = /home/p/Roms\nISOPath2 =\nRecursiveISOPaths = True\nISOPathX = /bad\n"
	os.WriteFile(filepath.Join(u, "Config", "Dolphin.ini"), []byte(ini), 0o644)
	d, r := ISOPaths(u)
	want := []string{filepath.Clean("/home/p/Roms"), filepath.Join(u, "Games", "GC")}
	if !slices.Equal(d, want) || !r {
		t.Fatalf("got %q %v, want %q true", d, r, want)
	}
}
