package main

import (
	"flag"
	"os"
	"sort"
	"strings"
	"testing"
)

var updateSubcommands = flag.Bool("update-subcommands", false, "rewrite testdata/cli-subcommands.txt from the command registry")

const subcommandsFile = "testdata/cli-subcommands.txt"

// TestWWDevSubcommandsUnchanged: wwdev's registry has exactly the
// subcommands in testdata/cli-subcommands.txt (the ones Phase D6 moved
// out of ww-multiplayer's), each once, and each dispatches to its own
// entry. ww-multiplayer's TestCLISubcommandsUnchanged checks that the two
// lists share no name.
func TestWWDevSubcommandsUnchanged(t *testing.T) {
	seen := map[string]bool{}
	var got []string
	for _, c := range commandTable() {
		if seen[c.name] {
			t.Errorf("%q is in the registry twice", c.name)
		}
		seen[c.name] = true
		got = append(got, c.name)
	}
	sort.Strings(got)
	if *updateSubcommands {
		if err := os.WriteFile(subcommandsFile, []byte(strings.Join(got, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(subcommandsFile)
	if err != nil {
		t.Fatal(err)
	}
	if g, w := strings.Join(got, "\n")+"\n", strings.ReplaceAll(string(want), "\r\n", "\n"); g != w {
		t.Fatalf("wwdev's subcommands changed:\ngot:\n%s\nwant:\n%s", g, w)
	}
	for _, name := range got {
		if c, ok := lookupCommand(name); !ok || c.run == nil || c.name != name {
			t.Errorf("%q: lookupCommand = %q, %v, run set %v", name, c.name, ok, c.run != nil)
		}
	}
}

// TestRegistryHelp: the local-testing commands have a usage that starts
// with their name and a summary; the diagnostics have neither.
func TestRegistryHelp(t *testing.T) {
	local := 0
	for _, c := range commandTable() {
		switch c.group {
		case groupLocal:
			local++
			if !strings.HasPrefix(c.usage, c.name) || c.summary == "" {
				t.Errorf("%s: usage %q, summary %q", c.name, c.usage, c.summary)
			}
		case groupDev:
			if c.usage != "" || c.summary != "" {
				t.Errorf("%s: a diagnostic with help text", c.name)
			}
		default:
			t.Errorf("%s: group %d", c.name, c.group)
		}
	}
	if local != 3 {
		t.Errorf("%d local-testing commands, want 3 (dolphin2, mp-local, help)", local)
	}
}
