package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi"
)

// TestCLIGoldenMods: `mods` and `mods --json` list the compiled-in mods
// (multiplayer, LOADED), and a stray argument prints the usage.
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
