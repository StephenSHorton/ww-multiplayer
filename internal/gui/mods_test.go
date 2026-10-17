package gui

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/config"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/example"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/commands"
	wwevents "github.com/StephenSHorton/ww-multiplayer/wwapi/events"
)

// pingEv is a mod's own event.
type pingEv struct{ wwevents.Base }

func (pingEv) EventName() string { return "mod.example.ping" }

// testMod is a mod for the Mods page's tests: its Init fails with initErr
// when that is set (an error naming an IP), and it registers an "echo"
// command otherwise.
type testMod struct {
	wwapi.NoRun
	manifest string
	initErr  error
}

func (m testMod) Manifest() wwapi.Manifest { return wwapi.ManifestFrom([]byte(m.manifest)) }

func (m testMod) Init(c *wwapi.Context) error {
	if m.initErr != nil {
		return m.initErr
	}
	return c.Commands().Register("mod."+c.ID()+".echo", commands.Spec{}, func(_ context.Context, args json.RawMessage) (commands.Result, error) {
		if string(args) == `"fail"` {
			return commands.Result{Code: "bad"}, errors.New("echo refused 10.1.2.3")
		}
		return commands.Result{OK: true, Code: string(args)}, nil
	})
}

func manifestJSON(id string, deps string, def bool) string {
	b, _ := json.Marshal(def)
	return `{"id":"` + id + `","name":"` + strings.ToUpper(id[:1]) + id[1:] + `","description":"The ` + id + ` test mod.","version":"1.2.3","api":"^0.1",
		"dependencies":{` + deps + `},"enabled_by_default":` + string(b) + `,"multiplayer":"local_only"}`
}

// modsBridge is a Bridge over a runtime with: alpha (LOADED, an echo
// command), beta (FAILED: its Init names an IP), gamma (DISABLED: off by
// default), and the example (NEEDS_REPATCH: the blob lacks warp).
func modsBridge(t *testing.T, flag func(string) (bool, bool)) (*Bridge, string) {
	t.Helper()
	rt := wwapi.New([]wwapi.Mod{
		testMod{manifest: manifestJSON("alpha", "", true)},
		testMod{manifest: manifestJSON("beta", `"alpha":"^1.0"`, true), initErr: errors.New("dial 192.168.1.42:25565: refused")},
		testMod{manifest: manifestJSON("gamma", "", false)},
		example.New(),
	}, wwapi.Options{Resolve: wwapi.ResolveOptions{Dev: true, Features: wwapi.KnownFeatures("avatar")}})
	rt.Init()
	path := filepath.Join(t.TempDir(), "config.json")
	b := NewBridge(Options{Controller: session.New(newFakeEngine(), session.Options{}), ConfigPath: path, Mods: rt, ModFlag: flag, DevMods: true})
	t.Cleanup(b.close)
	return b, path
}

func modByID(t *testing.T, v ModsView, id string) ModView {
	t.Helper()
	for _, m := range v.Mods {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("no mod %s in %+v", id, v.Mods)
	return ModView{}
}

// TestBridgeMods: the Mods page lists every mod with its version, status
// and reason (masked unless the switch is on), its toggle and whether a
// restart changes it, and the loaded mods' commands.
func TestBridgeMods(t *testing.T) {
	b, _ := modsBridge(t, func(id string) (bool, bool) { return false, id == "gamma" })
	v := b.Mods()
	if !v.Dev || len(v.Mods) != 4 || v.Mods[0].ID != "alpha" {
		t.Fatalf("Mods = %+v", v)
	}
	a := modByID(t, v, "alpha")
	if a.Status != "LOADED" || a.Version != "1.2.3" || a.Name != "Alpha" || a.Description != "The alpha test mod." ||
		!a.Enabled || a.Saved || a.Pending || len(a.Verbs) != 1 || a.Verbs[0] != "echo" || a.Flag != "" {
		t.Errorf("alpha = %+v", a)
	}
	be := modByID(t, v, "beta")
	if be.Status != "FAILED" || !strings.Contains(be.Reason, "•••.•••.•••.42:25565") || strings.Contains(be.Reason+be.StatusText, "192.168") ||
		len(be.Requires) != 1 || be.Requires[0] != "alpha" || len(be.Verbs) != 0 || be.Pending {
		t.Errorf("beta = %+v", be)
	}
	g := modByID(t, v, "gamma")
	if g.Status != "DISABLED" || g.Enabled || g.Pending || g.Flag != "off" {
		t.Errorf("gamma = %+v", g)
	}
	ex := modByID(t, v, "example")
	if ex.Status != "NEEDS_REPATCH" || len(ex.Missing) != 1 || ex.Missing[0] != "warp" || !ex.Dev || !ex.Enabled {
		t.Errorf("example = %+v", ex)
	}
	b.SetShowIPs(true)
	if be := modByID(t, b.Mods(), "beta"); !strings.Contains(be.Reason, "192.168.1.42:25565") {
		t.Errorf("with the switch on: %q", be.Reason)
	}
	// No runtime: an empty page.
	nb, _, _ := newTestBridge(t)
	if v := nb.Mods(); len(v.Mods) != 0 || v.Mods == nil {
		t.Errorf("no runtime: %+v", v)
	}
}

// TestBridgeSetModEnabled: the toggle is saved as mods.<id>.enabled,
// keeps the rest of the config, says a restart applies it, emits "mods",
// and refuses a mod the app doesn't have.
func TestBridgeSetModEnabled(t *testing.T) {
	b, path := modsBridge(t, nil)
	got := make(chan EventView, 8)
	b.emu.Lock()
	b.emitters[99] = func(ev EventView) { got <- ev }
	b.emu.Unlock()
	if err := b.SetModEnabled("gamma", true); err != nil {
		t.Fatal(err)
	}
	if ev := <-got; ev.Kind != "mods" {
		t.Fatalf("event %+v", ev)
	}
	if err := b.SetModEnabled("alpha", false); err != nil {
		t.Fatal(err)
	}
	v := b.Mods()
	if g := modByID(t, v, "gamma"); !g.Enabled || !g.Saved || !g.Pending || g.Status != "DISABLED" {
		t.Errorf("gamma after on = %+v", g)
	}
	if a := modByID(t, v, "alpha"); a.Enabled || !a.Saved || !a.Pending || a.Status != "LOADED" {
		t.Errorf("alpha after off = %+v", a)
	}
	c, warns := config.Load(path)
	if len(warns) != 0 {
		t.Fatal(warns)
	}
	if on, set := c.ModEnabled("gamma"); !on || !set {
		t.Errorf("saved gamma: %v %v", on, set)
	}
	if on, set := c.ModEnabled("alpha"); on || !set {
		t.Errorf("saved alpha: %v %v", on, set)
	}
	src := ModConfig(path)
	if on, set := src.ModEnabled("alpha"); on || !set {
		t.Errorf("ModConfig alpha: %v %v", on, set)
	}
	if err := b.SetModEnabled("nope", true); err == nil {
		t.Error("an unknown mod was saved")
	}
	if ModConfig(filepath.Join(t.TempDir(), "none.json")) == nil {
		t.Error("a missing config gives no source")
	}
}

// TestBridgeCallMod: CallMod runs a loaded mod's mod.<id>.<verb>, with
// its error masked, and refuses one that isn't running.
func TestBridgeCallMod(t *testing.T) {
	b, _ := modsBridge(t, nil)
	r, err := b.CallMod("alpha", "echo", json.RawMessage(`{"n":1}`))
	if err != nil || !r.OK || r.Code != `{"n":1}` {
		t.Fatalf("echo: %+v %v", r, err)
	}
	if r, err := b.CallMod("alpha", "echo", json.RawMessage("null")); err != nil || !r.OK || r.Code != "" {
		t.Fatalf("echo null: %+v %v", r, err)
	}
	if _, err := b.CallMod("alpha", "echo", json.RawMessage(`"fail"`)); err == nil || err.Error() != "echo refused •••.•••.•••.3" {
		t.Fatalf("a failing command: %v", err)
	}
	for _, c := range [][2]string{{"alpha", "nope"}, {"alpha", ""}, {"beta", "echo"}, {"example", "mark"}, {"nope", "x"}} {
		if _, err := b.CallMod(c[0], c[1], nil); err == nil {
			t.Errorf("CallMod(%q, %q) ran", c[0], c[1])
		}
	}
}

// TestModEventView: a mod's status change reaches the page as a "mod"
// event with its status; its own events carry no status.
func TestModEventView(t *testing.T) {
	ev := eventView(session.Event{Kind: session.EventMod, Mod: session.ModStatus{ID: "beta", State: "QUARANTINED", Reason: "dial 10.0.0.9:1"},
		Text: "QUARANTINED(dial 10.0.0.9:1)"}, hideIPs)
	if ev.Kind != "mod" || ev.Mod != "beta" || ev.Status != "QUARANTINED" || ev.Text != "QUARANTINED(dial •••.•••.•••.9:1)" {
		t.Fatalf("status event = %+v", ev)
	}
	ev = eventView(session.Event{Kind: session.EventMod, Mod: session.ModStatus{ID: "example", State: "LOADED"}, Payload: pingEv{}, Text: "mod.example.ping"}, hideIPs)
	if ev.Status != "" || ev.Text != "mod.example.ping" || ev.Mod != "example" {
		t.Fatalf("mod's event = %+v", ev)
	}
}

// TestCoopModOff: without the co-op mod running, the Session screen
// offers no co-op and a co-op choice is refused (off is fine); without a
// runtime it is offered, as before mods.
func TestCoopModOff(t *testing.T) {
	b, _ := modsBridge(t, nil) // no "coop" among its mods
	if b.coopMod() {
		t.Fatal("coopMod without a coop mod")
	}
	if err := b.SetCoop(CoopRequest{Mode: "new"}); !errors.Is(err, errNoCoopMod) {
		t.Fatalf("SetCoop new: %v", err)
	}
	if err := b.SetCoop(CoopRequest{Mode: "off"}); err != nil {
		t.Fatalf("SetCoop off: %v", err)
	}
	st := session.State{Session: 1, Phase: session.InLobby, Lobby: &session.Lobby{Host: true, Phase: session.LobbyPhaseLobby}}
	if v := b.coopView(st, true, hideIPs); v != nil {
		t.Fatalf("coopView = %+v", v)
	}
	nb, _, _ := newTestBridge(t)
	if !nb.coopMod() || nb.coopView(st, true, hideIPs) == nil {
		t.Fatal("no runtime: no co-op")
	}
}
