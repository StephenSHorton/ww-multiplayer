package tui

import (
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/events"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
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
