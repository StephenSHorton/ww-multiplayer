package tui

import (
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/events"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	tea "github.com/charmbracelet/bubbletea"
)

// quarantinedEv stands for wwapi.ModQuarantined on the runtime's bus.
type quarantinedEv struct{ events.Base }

func (quarantinedEv) EventName() string { return "core.mod_quarantined" }

// TestConnectModLines: the connect screen has one status line per mod
// that is not LOADED (masked like the rest of the screen), and none when
// every mod loaded, so the default screen is as it was.
func TestConnectModLines(t *testing.T) {
	m := newConnect()
	m.nameInput = "Link"
	before := plain(m.view(80))
	m.mods = modsNotLoaded(session.New(nil, session.Options{Bus: nil, Mods: func() []session.ModStatus {
		return []session.ModStatus{{ID: "multiplayer", Version: "1.0.0", State: session.Loaded}}
	}}))
	if len(m.mods) != 0 || plain(m.view(80)) != before {
		t.Fatalf("every mod LOADED: lines %+v", m.mods)
	}
	m.mods = []session.ModStatus{
		{ID: "coop", Version: "1.0.0", State: "DISABLED", Reason: "turned off in settings or by a flag"},
		{ID: "example", Version: "0.1.0", State: "QUARANTINED", Reason: "dial 192.168.1.42:25565: refused"},
	}
	out := plain(m.view(80))
	for _, want := range []string{
		"Mod coop 1.0.0: DISABLED (turned off in settings or by a flag)",
		"Mod example 0.1.0: QUARANTINED (dial •••.•••.•••.42:25565: refused)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "192.168") {
		t.Errorf("an IP shows:\n%s", out)
	}
	m.showIPs = true
	if out := plain(m.view(80)); !strings.Contains(out, "192.168.1.42") {
		t.Errorf("with IPs shown:\n%s", out)
	}
	if modsNotLoaded(nil) != nil {
		t.Error("no controller: lines")
	}
}

// TestConnectCoopModOff: with the coop mod not LOADED (the toggle or
// --no-mod=coop) or an engine without co-op, the co-op row offers only
// off for both roles, as the GUI's SetCoop refuses co-op; with coop
// LOADED and a co-op engine the choices are as they were.
func TestConnectCoopModOff(t *testing.T) {
	mods := func(state string) func() []session.ModStatus {
		return func() []session.ModStatus {
			return []session.ModStatus{
				{ID: "multiplayer", Version: "1.0.0", State: session.Loaded},
				{ID: "coop", Version: "1.0.0", State: state},
			}
		}
	}
	on := session.New(newCoopEngine(), session.Options{Mods: mods(session.Loaded)})
	if coopOff(on) || coopOff(nil) {
		t.Fatal("coop LOADED (or no controller): co-op off")
	}
	for name, ctrl := range map[string]*session.Controller{
		"coop DISABLED":    session.New(newCoopEngine(), session.Options{Mods: mods("DISABLED")}),
		"no co-op engine":  session.New(&fakeEngine{}, session.Options{}),
		"coop QUARANTINED": session.New(newCoopEngine(), session.Options{Mods: mods("QUARANTINED")}),
	} {
		if !coopOff(ctrl) {
			t.Errorf("%s: co-op still offered", name)
		}
	}

	m := newConnect()
	m.nameInput = "Link"
	m.noCoop = true
	m.worlds = []session.CoopWorld{{ID: "0123456789abcdef", Seq: 4}}
	for _, role := range []int{0, 1} {
		m.role = role
		m.focused = 1
		m.hostCoop, m.joinCoop = 1, 1
		cs := m.coopChoices()
		if len(cs) != 1 || cs[0].opts.On {
			t.Fatalf("role %d: choices %+v, want off only", role, cs)
		}
		for _, k := range []tea.KeyType{tea.KeyRight, tea.KeyLeft, tea.KeyEnter} {
			next := send(m, tea.KeyMsg{Type: k})
			if c := next.coopChoices()[next.coopIndex()]; c.opts.On {
				t.Errorf("role %d after %v: co-op %+v", role, k, c.opts)
			}
		}
		if out := plain(m.view(80)); !strings.Contains(out, "Off (Co-op mod is off)") {
			t.Errorf("role %d: row\n%s", role, out)
		}
	}
}

// TestDashboardModStatus: a mod's status change during a session (the
// runtime quarantined it) is a dashboard log line.
func TestDashboardModStatus(t *testing.T) {
	bus := events.New(events.Options{})
	defer bus.Close()
	state := session.Loaded
	ctrl := session.New(nil, session.Options{Bus: bus, Mods: func() []session.ModStatus {
		return []session.ModStatus{{ID: "example", Version: "0.1.0", State: state, Reason: "panic: boom"}}
	}})
	ch, stop := ctrl.Subscribe()
	defer stop()
	d := newDashboard(ctrl, 3, ch, stop, "host", "Link", "", 80)
	state = "QUARANTINED"
	if err := bus.Emit(quarantinedEv{}); err != nil {
		t.Fatal(err)
	}
	ev := <-ch
	d, _ = d.update(eventMsg{ch: ch, ev: ev})
	if n := len(d.logs); n == 0 || d.logs[n-1].msg != "Mod example 0.1.0: QUARANTINED (panic: boom)" || d.logs[n-1].level != log.Err {
		t.Fatalf("logs = %+v", d.logs)
	}
}
