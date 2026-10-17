package main

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/engine"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
)

// The app's mods (D4): the faces run the compiled-in mods under a
// wwapi.Runtime over the saved settings and the mod flags, and take the
// session engine from the multiplayer mod.

func TestParseModFlags(t *testing.T) {
	for _, c := range []struct {
		args []string
		want modFlags
		rest []string
		err  string
	}{
		{nil, modFlags{}, nil, ""},
		{[]string{"--browser", "--port", "5"}, modFlags{}, []string{"--browser", "--port", "5"}, ""},
		{[]string{"--dev", "--mods=a, b", "--no-mod", "c,d", "--no-mod=e"},
			modFlags{only: []string{"a", "b"}, onlySet: true, off: []string{"c", "d", "e"}, dev: true}, nil, ""},
		{[]string{"--mods="}, modFlags{onlySet: true}, nil, ""},
		{[]string{"--mods", "a", "--browser", "--mods=b"}, modFlags{only: []string{"a", "b"}, onlySet: true}, []string{"--browser"}, ""},
		{[]string{"--mods"}, modFlags{}, nil, "--mods needs a list of mod ids"},
		{[]string{"--no-mod="}, modFlags{}, nil, "--no-mod needs a mod id"},
		{[]string{"--dev=1"}, modFlags{}, nil, "--dev takes no value"},
	} {
		got, rest, err := parseModFlags(c.args)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: err %v, want %q", c.args, err, c.err)
			}
			continue
		}
		if err != nil || !slices.Equal(got.only, c.want.only) || got.onlySet != c.want.onlySet ||
			!slices.Equal(got.off, c.want.off) || got.dev != c.want.dev || !slices.Equal(rest, c.rest) {
			t.Errorf("%q: %+v rest %q err %v; want %+v rest %q", c.args, got, rest, err, c.want, c.rest)
		}
	}
}

// TestFaceModFlags runs the real main: the faces take the mod flags (a
// good one still reaches the TUI, which without a terminal says so) and
// refuse a bad one or a stray argument with the face's usage.
func TestFaceModFlags(t *testing.T) {
	if out, code := runFaceMain(t, "tui", "--dev", "--no-mod=coop"); code != 1 || !strings.Contains(out, "no interactive terminal detected on stdin") {
		t.Errorf("tui --dev --no-mod=coop: exit %d\n%s", code, out)
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"tui", "--mods=nope"}, "ERROR: no mod \"nope\" in this version (it has multiplayer, coop)\nUsage: " + exeCmd + " tui [--dev] [--mods=a,b] [--no-mod=x]\n"},
		{[]string{"tui", "--port", "1"}, "ERROR: unknown argument \"--port\"\nUsage: " + exeCmd + " tui [--dev] [--mods=a,b] [--no-mod=x]\n"},
		{[]string{"ui", "--browser", "--no-mod=example"}, "ERROR: example is a dev mod: add --dev\nUsage: " + exeCmd + " ui [--browser [--port N] [--no-open]] [--dev] [--mods=a,b] [--no-mod=x]\n"},
		{[]string{"ui", "--browser", "--bogus", "--dev"}, "ERROR: unknown argument \"--bogus\"\nUsage: " + exeCmd + " ui [--browser [--port N] [--no-open]] [--dev] [--mods=a,b] [--no-mod=x]\n"},
	} {
		if out, code := runFaceMain(t, c.args...); code != 1 || out != c.want {
			t.Errorf("%q: exit %d\n%s\nwant exit 1\n%s", c.args, code, out, c.want)
		}
	}
}

// savedMods is a saved config's mods section (what gui.ModConfig reads).
type savedMods map[string]bool

func (s savedMods) ModEnabled(id string) (on, set bool) { on, set = s[id]; return }
func (s savedMods) ModSettings(id string) json.RawMessage {
	if id == "multiplayer" {
		return json.RawMessage(`{"x":1}`)
	}
	return nil
}

// TestAppModConfig: the flags override the saved settings, which
// override the defaults (unset).
func TestAppModConfig(t *testing.T) {
	saved := savedMods{"coop": false, "multiplayer": true}
	for _, c := range []struct {
		flags   modFlags
		id      string
		on, set bool
	}{
		{modFlags{}, "coop", false, true},
		{modFlags{}, "example", false, false},
		{modFlags{off: []string{"multiplayer"}}, "multiplayer", false, true},
		{modFlags{onlySet: true, only: []string{"coop"}}, "coop", true, true},
		{modFlags{onlySet: true, only: []string{"coop"}}, "multiplayer", false, true},
		{modFlags{onlySet: true, only: []string{"coop"}, off: []string{"coop"}}, "coop", false, true},
	} {
		on, set := appModConfig{saved: saved, flags: c.flags}.ModEnabled(c.id)
		if on != c.on || set != c.set {
			t.Errorf("%+v %s: %v %v, want %v %v", c.flags, c.id, on, set, c.on, c.set)
		}
	}
	if on, set := (appModConfig{}).ModEnabled("coop"); on || set {
		t.Error("no settings, no flags: set")
	}
	if s := (appModConfig{saved: saved}).ModSettings("multiplayer"); string(s) != `{"x":1}` {
		t.Errorf("settings %s", s)
	}
	if s := (appModConfig{}).ModSettings("multiplayer"); s != nil {
		t.Errorf("no settings: %s", s)
	}
}

// TestModFlagsCheck: a flag naming a mod this version lacks is refused,
// and a dev mod needs --dev.
func TestModFlagsCheck(t *testing.T) {
	all := modList(true, nil)
	if err := (modFlags{off: []string{"coop"}}).check(all); err != nil {
		t.Error(err)
	}
	if err := (modFlags{dev: true, only: []string{"example"}, onlySet: true}).check(all); err != nil {
		t.Error(err)
	}
	if err := (modFlags{only: []string{"example"}, onlySet: true}).check(all); err == nil || err.Error() != "example is a dev mod: add --dev" {
		t.Errorf("example without --dev: %v", err)
	}
	if err := (modFlags{off: []string{"nope"}}).check(all); err == nil || !strings.Contains(err.Error(), `no mod "nope"`) {
		t.Errorf("nope: %v", err)
	}
}

func appStates(a *app) map[string]string {
	out := map[string]string{}
	for _, m := range a.ctrl.Mods() {
		out[m.ID] = m.State
	}
	return out
}

// TestAppDefaults: with nothing saved and no flags the app runs what it
// ran before mods: multiplayer and co-op LOADED, the example hidden, the
// session engine the multiplayer mod's, with co-op in it (co-op play
// stays off until a player picks it: that is the session's CoopOptions).
func TestAppDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	a := newApp(modFlags{}, nil)
	defer a.stop()
	if got := appStates(a); len(got) != 2 || got["multiplayer"] != session.Loaded || got["coop"] != session.Loaded {
		t.Fatalf("mods = %v", got)
	}
	if _, err := a.ctrl.CoopWorlds(); errors.Is(err, session.ErrNoCoop) {
		t.Fatalf("the engine has no co-op: %v", err)
	}
	if steps := patchSteps(runtimeMods(a.rt)); len(steps) != 1 {
		t.Fatalf("%d post-patch steps, want co-op's", len(steps))
	}
	a.stop() // twice is fine
}

// TestAppModsOff: --no-mod=coop gives an engine without co-op;
// multiplayer off gives no engine (Host says so) and fails co-op; a saved
// setting does the same as a flag; --dev runs the example.
func TestAppModsOff(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	a := newApp(modFlags{off: []string{"coop"}}, nil)
	if got := appStates(a); got["coop"] != "DISABLED" || got["multiplayer"] != session.Loaded {
		t.Fatalf("--no-mod=coop: %v", got)
	}
	if _, err := a.ctrl.CoopWorlds(); !errors.Is(err, session.ErrNoCoop) {
		t.Fatalf("--no-mod=coop: CoopWorlds err %v, want ErrNoCoop", err)
	}
	if steps := patchSteps(runtimeMods(a.rt)); len(steps) != 0 {
		t.Fatalf("--no-mod=coop: %d post-patch steps", len(steps))
	}
	a.stop()

	a = newApp(modFlags{}, savedMods{"multiplayer": false})
	if got := appStates(a); got["multiplayer"] != "DISABLED" || got["coop"] != "FAILED" {
		t.Fatalf("multiplayer off in settings: %v", got)
	}
	if _, err := a.ctrl.Host(t.Context(), session.HostOptions{Name: "Link"}); !errors.Is(err, session.ErrNoEngine) {
		t.Fatalf("Host without multiplayer: %v", err)
	}
	a.stop()

	a = newApp(modFlags{dev: true}, nil)
	defer a.stop()
	if got := appStates(a); got["example"] != session.Loaded {
		t.Fatalf("--dev: %v", got)
	}
	names := a.rt.Commands().Names()
	if !slices.Contains(names, "mod.example.mark") {
		t.Fatalf("--dev: commands %v", names)
	}
	// No Game in the app yet: the example's commands say so.
	if _, err := a.rt.Commands().Execute(t.Context(), "mod.example.mark", nil); err == nil || !strings.Contains(err.Error(), "no game") {
		t.Fatalf("mark: %v", err)
	}
	if s, _ := a.rt.Status("example"); s.State != wwapi.Loaded {
		t.Fatalf("example: %v", s)
	}
}

// TestAppModsHash (D5): the engines carry the client_required mods that
// load: the CLI's at the manifest defaults, the app's as its runtime
// resolved them (a dev mod is local_only and never counts).
func TestAppModsHash(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	both := []string{"coop@1.0.0", "multiplayer@1.0.0"}
	if m := newCLIEngine().Mods(); !m.Known || !slices.Equal(m.List, both) || m.Hash != wwapi.ModsIDOf(both).Hash {
		t.Fatalf("CLI engine mods = %+v", m)
	}
	for _, c := range []struct {
		flags modFlags
		want  []string
	}{
		{modFlags{}, both},
		{modFlags{dev: true}, both},
		{modFlags{off: []string{"coop"}}, []string{"multiplayer@1.0.0"}},
	} {
		a := newApp(c.flags, nil)
		e, ok := netEngine(a.rt).(*engine.Engine)
		if !ok {
			a.stop()
			t.Fatalf("%+v: engine %T", c.flags, netEngine(a.rt))
		}
		if m := e.Mods(); !m.Known || !slices.Equal(m.List, c.want) || m.Hash != wwapi.ModsIDOf(c.want).Hash {
			t.Errorf("%+v: app engine mods = %+v, want %v", c.flags, m, c.want)
		}
		a.stop()
	}
}
