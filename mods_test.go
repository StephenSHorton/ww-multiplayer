package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi"
)

// TestCLIGoldenMods: `mods` and `mods --json` list the compiled-in mods
// (multiplayer and co-op, LOADED), and a stray argument prints the usage.
func TestCLIGoldenMods(t *testing.T) {
	root := t.TempDir()
	env := configEnv(root)
	for _, c := range []struct {
		name string
		args []string
	}{
		{"mods", []string{"mods"}},
		{"mods-json", []string{"mods", "--json"}},
		{"mods-usage", []string{"mods", "--all"}},
	} {
		o, code := runCLI(t, env, c.args...)
		checkCLIGolden(t, c.name, normalizeCLI(o, root), code)
	}
}

// TestCompiledModsResolve: whatever the static list holds resolves with
// every mod LOADED, so a broken compiled-in mod fails this test rather
// than a user's session.
func TestCompiledModsResolve(t *testing.T) {
	for _, mi := range wwapi.Resolve(compiledMods(), wwapi.ResolveOptions{}).Infos() {
		if mi.Status != wwapi.Loaded && mi.Status != wwapi.Disabled {
			t.Errorf("compiled-in mod %s: %s", mi.ID, mi.StatusString())
		}
	}
}

func TestModsText(t *testing.T) {
	infos := []wwapi.ModInfo{
		{ID: "multiplayer", Version: "1.0.0", Status: wwapi.Loaded},
		{ID: "coop", Version: "1.0.0", Status: wwapi.Failed, Reason: "requires multiplayer, which did not load"},
		{Status: wwapi.Failed, Reason: "bad"},
	}
	got := modsText(infos)
	want := "Mods (wwapi " + wwapi.Version + "):\n" +
		"  multiplayer              1.0.0        LOADED\n" +
		"  coop                     1.0.0        FAILED(requires multiplayer, which did not load)\n" +
		"  (no id)                               FAILED(bad)"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if b, _ := json.Marshal(infos[:1]); !strings.Contains(string(b), `"status":"LOADED"`) {
		t.Errorf("json %s", b)
	}
}

// TestCLIModCommands: co-op's commands come from the co-op mod (the CLI
// capability), dispatch from the CLI as they did from package main, and
// print the usage with this platform's binary name.
func TestCLIModCommands(t *testing.T) {
	root := t.TempDir()
	env := configEnv(root)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"coop-tag"}, "ERROR: say show or clear\nUsage: " + exeCmd + " coop-tag show [world-id] | coop-tag clear [--yes]\n"},
		{[]string{"coop-tables"}, "ERROR: give the path of your Wind Waker disc image (.iso or .ciso)\nUsage: " + exeCmd + " coop-tables <iso|ciso> [--json] [--no-cache] [--section keys|hearts|switches|enemies]\n"},
	} {
		if o, code := runCLI(t, env, c.args...); code != 1 || o != c.want {
			t.Errorf("%q: exit %d\n%s\nwant exit 1\n%s", c.args, code, o, c.want)
		}
	}
	got := map[string]bool{}
	for _, c := range modCommands() {
		got[c.name] = true
		if !c.hidden || c.group != groupDev {
			t.Errorf("%s: hidden %v, group %d", c.name, c.hidden, c.group)
		}
	}
	for _, name := range []string{"coop-tables", "coop-tag", "coop-world", "save-diff", "save-merge"} {
		if !got[name] {
			t.Errorf("%s is not a mod command", name)
		}
	}
	if len(got) != 5 {
		t.Errorf("mod commands %v", got)
	}
}

// TestPostPatchSteps: the loaded mods' post-patch steps are co-op's
// stage tables step, once.
func TestPostPatchSteps(t *testing.T) {
	if n := len(postPatchSteps()); n != 1 {
		t.Fatalf("%d post-patch steps, want co-op's", n)
	}
}

func TestModGroup(t *testing.T) {
	for label, want := range map[string]cmdGroup{
		wwapi.GroupPlay: groupPlay, wwapi.GroupPatch: groupPatch, wwapi.GroupLocal: groupLocal,
		wwapi.GroupLower: groupLower, wwapi.GroupDev: groupDev, "": groupLower, "other": groupLower,
	} {
		if got := modGroup(label); got != want {
			t.Errorf("modGroup(%q) = %d, want %d", label, got, want)
		}
	}
}
