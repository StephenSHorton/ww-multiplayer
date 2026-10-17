package coop

import (
	"slices"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
)

// TestManifest: the embedded mod.json is co-op's, every player needs it,
// it requires multiplayer 1.x, and it needs nothing of the blob beyond
// what multiplayer needs.
func TestManifest(t *testing.T) {
	m := New(coopEnv()).Manifest()
	if err := m.Err(); err != nil {
		t.Fatal(err)
	}
	if m.ID != "coop" || m.Version.String() != "1.0.0" || !m.EnabledByDefault || m.Dev {
		t.Errorf("manifest %+v", m)
	}
	if m.Multiplayer != wwapi.ClientRequired || len(m.Needs) != 0 || len(m.Provides) != 0 {
		t.Errorf("multiplayer %q, needs %v, provides %v", m.Multiplayer, m.Needs, m.Provides)
	}
	if len(m.Dependencies) != 1 || m.Dependencies["multiplayer"].String() != "^1.0" || len(m.OptionalDependencies) != 0 {
		t.Errorf("dependencies %v, optional %v", m.Dependencies, m.OptionalDependencies)
	}
}

// TestResolve: co-op loads after multiplayer, fails without it or with
// it disabled, and the disabled multiplayer is the reason.
func TestResolve(t *testing.T) {
	mods := []wwapi.Mod{New(coopEnv()), multiplayer.New(nil)}
	p := wwapi.Resolve(mods, wwapi.ResolveOptions{})
	got := statuses(p.Infos())
	if got["coop"] != "LOADED" || got["multiplayer"] != "LOADED" {
		t.Fatalf("statuses %v", got)
	}
	if len(p.Order) != 2 || p.Order[0] != 1 || p.Order[1] != 0 {
		t.Errorf("load order %v, want multiplayer then coop", p.Order)
	}

	off := func(id string) (bool, bool) { return false, id == "multiplayer" }
	got = statuses(wwapi.Resolve(mods, wwapi.ResolveOptions{Enabled: off}).Infos())
	if got["multiplayer"] != "DISABLED" || got["coop"] != "FAILED(requires multiplayer, which is disabled)" {
		t.Errorf("multiplayer disabled: %v", got)
	}

	got = statuses(wwapi.Resolve([]wwapi.Mod{New(coopEnv())}, wwapi.ResolveOptions{}).Infos())
	if !strings.HasPrefix(got["coop"], "FAILED(") {
		t.Errorf("without multiplayer: %v", got)
	}

	// Disabling co-op leaves multiplayer loaded.
	offCoop := func(id string) (bool, bool) { return false, id == "coop" }
	got = statuses(wwapi.Resolve(mods, wwapi.ResolveOptions{Enabled: offCoop}).Infos())
	if got["coop"] != "DISABLED" || got["multiplayer"] != "LOADED" {
		t.Errorf("co-op disabled: %v", got)
	}
}

// TestCommands: the CLI capability is co-op's five developer commands,
// hidden, in the order the app's registry has always listed them.
func TestCommands(t *testing.T) {
	var names []string
	for _, c := range New(coopEnv()).Commands() {
		names = append(names, c.Name)
		if !c.Hidden || c.Group != wwapi.GroupDev || c.Usage != "" || c.Summary != "" || len(c.Aliases) != 0 || c.Run == nil {
			t.Errorf("%s: %+v", c.Name, c)
		}
	}
	if want := []string{"coop-tables", "coop-tag", "coop-world", "save-diff", "save-merge"}; !slices.Equal(names, want) {
		t.Errorf("commands %q, want %q", names, want)
	}
}

func statuses(infos []wwapi.ModInfo) map[string]string {
	m := map[string]string{}
	for _, mi := range infos {
		m[mi.ID] = mi.StatusString()
	}
	return m
}
