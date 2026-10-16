package dolphinenv

import (
	"path/filepath"
	"testing"
)

// TestUserDirArg: the user folder a Dolphin command line names.
func TestUserDirArg(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
		ok   bool
	}{
		{nil, "", false},
		{[]string{"-e", "ww.iso"}, "", false},
		{[]string{"-u", `C:\Users\p\AppData\Roaming\Dolphin Emulator 2`, "-e", "ww.iso"}, `C:\Users\p\AppData\Roaming\Dolphin Emulator 2`, true},
		{[]string{"--user", "/home/p/d"}, "/home/p/d", true},
		{[]string{"--user=/home/p/d"}, "/home/p/d", true},
		{[]string{"-u/home/p/d"}, "/home/p/d", true},
		{[]string{"-u", "a", "-u", "b"}, "b", true},
		{[]string{"-u"}, "", false}, // no value
		{[]string{"--batch", "-e", "x"}, "", false},
	} {
		if got, ok := UserDirArg(c.args); got != c.want || ok != c.ok {
			t.Errorf("UserDirArg(%q) = %q, %v; want %q, %v", c.args, got, ok, c.want, c.ok)
		}
	}
}

// TestProcessUserDir: a running Dolphin's folder is its -u, else its
// program's default (portable or not); unknown without its command line.
func TestProcessUserDir(t *testing.T) {
	cfg := filepath.Join("cfg")
	exe := filepath.Join("dolphin", "Dolphin.exe")
	portable := map[string]bool{filepath.Join("dolphin", "portable.txt"): true}
	e := Env{GOOS: "windows", ConfigDir: cfg, Exists: func(p string) bool { return portable[p] }}
	if d, ok := ProcessUserDir(e, Process{Exe: exe}); ok || d != "" {
		t.Fatalf("args unknown: %q, %v", d, ok)
	}
	if d, ok := ProcessUserDir(e, Process{Exe: exe, ArgsKnown: true, Args: []string{"-u", "mine"}}); !ok || d != "mine" {
		t.Fatalf("-u: %q, %v", d, ok)
	}
	if d, ok := ProcessUserDir(e, Process{Exe: exe, ArgsKnown: true}); !ok || d != filepath.Join("dolphin", "User") {
		t.Fatalf("portable: %q, %v", d, ok)
	}
	e.Exists = func(string) bool { return false }
	if d, ok := ProcessUserDir(e, Process{Exe: exe, ArgsKnown: true}); !ok || d != filepath.Join(cfg, "Dolphin Emulator") {
		t.Fatalf("default: %q, %v", d, ok)
	}
}
