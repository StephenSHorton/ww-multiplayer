package session

import (
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/events"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// pinged is a mod event, as the example mod would emit one.
type pinged struct {
	events.Base
	N int
}

func (pinged) EventName() string { return "mod.example.pinged" }

// quarantined stands for wwapi.ModQuarantined (this package can't import
// wwapi): what the Controller looks at is its core.mod_ name.
type quarantined struct{ events.Base }

func (quarantined) EventName() string { return "core.mod_quarantined" }

// stageMoved is a game event: not the Controller's to adapt.
type stageMoved struct{ events.Base }

func (stageMoved) EventName() string { return "core.stage_moved" }

// modList is a runtime's statuses that a test changes.
type modList struct {
	mu   sync.Mutex
	mods []ModStatus
}

func (l *modList) get() []ModStatus {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]ModStatus(nil), l.mods...)
}

func (l *modList) set(i int, m ModStatus) {
	l.mu.Lock()
	l.mods[i] = m
	l.mu.Unlock()
}

func nextMod(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Kind == EventMod {
				return ev
			}
		case <-deadline:
			t.Fatal("no EventMod")
		}
	}
}

// TestControllerAdaptsBus: the Controller is an adapter over the mod
// runtime's bus (decision 15): a mod's mod.<id>.* event and a mod's
// status change become EventMod, game events don't, and the session's
// own events are as before.
func TestControllerAdaptsBus(t *testing.T) {
	bus := events.New(events.Options{})
	defer bus.Close()
	list := &modList{mods: []ModStatus{
		{ID: "multiplayer", Name: "Multiplayer", Version: "1.0.0", State: Loaded},
		{ID: "example", Name: "Example", Version: "0.1.0", State: Loaded},
	}}
	e := newFake()
	c := New(e, Options{Bus: bus, Mods: list.get})
	ch, stop := c.Subscribe()
	defer stop()
	if got := c.Mods(); len(got) != 2 || got[1].ID != "example" {
		t.Fatalf("Mods = %+v", got)
	}

	view, err := bus.Mod("example")
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.Emit(stageMoved{}); err != nil {
		t.Fatal(err)
	}
	if err := view.Emit(pinged{N: 7}); err != nil {
		t.Fatal(err)
	}
	ev := nextMod(t, ch)
	p, ok := ev.Payload.(pinged)
	if ev.Mod.ID != "example" || ev.Mod.State != Loaded || ev.Text != "mod.example.pinged" || !ok || p.N != 7 || ev.Session != 0 {
		t.Fatalf("mod event = %+v", ev)
	}

	list.set(1, ModStatus{ID: "example", Name: "Example", Version: "0.1.0", State: "QUARANTINED", Reason: "panic: boom"})
	if err := bus.Emit(quarantined{}); err != nil {
		t.Fatal(err)
	}
	ev = nextMod(t, ch)
	if ev.Payload != nil || ev.Mod.ID != "example" || ev.Mod.State != "QUARANTINED" || ev.Text != "QUARANTINED(panic: boom)" {
		t.Fatalf("status event = %+v", ev)
	}
	// The same statuses again: nothing changed, so no event; the next
	// mod event says the mod's new status.
	if err := bus.Emit(quarantined{}); err != nil {
		t.Fatal(err)
	}
	if err := view.Emit(pinged{N: 8}); err != nil {
		t.Fatal(err)
	}
	if ev = nextMod(t, ch); ev.Payload == nil || ev.Mod.State != "QUARANTINED" {
		t.Fatalf("after an unchanged status: %+v", ev)
	}

	// The session's events are the Controller's own, as before.
	id, err := c.Host(t.Context(), HostOptions{Name: "Link"})
	if err != nil {
		t.Fatal(err)
	}
	waitStarted(t, e)
	c.ModLog().Log(log.Info, "[example] marked")
	for {
		ev := <-ch
		if ev.Kind == EventLog && ev.Text == "[example] marked" {
			if ev.Session != id {
				t.Fatalf("a mod's line during a session has session %d, want %d", ev.Session, id)
			}
			break
		}
	}
	c.Leave()
}

// TestControllerNoBus: without a bus (the CLI) there are no mods.
func TestControllerNoBus(t *testing.T) {
	c := New(newFake(), Options{})
	if m := c.Mods(); m != nil {
		t.Fatalf("Mods = %v", m)
	}
	ch, stop := c.Subscribe()
	defer stop()
	c.ModLog().Log(log.Warn, "between sessions")
	if ev := <-ch; ev.Kind != EventLog || ev.Session != 0 || ev.Text != "between sessions" {
		t.Fatalf("event = %+v", ev)
	}
}

func TestModStatusString(t *testing.T) {
	for _, c := range []struct {
		m    ModStatus
		want string
	}{
		{ModStatus{State: Loaded}, "LOADED"},
		{ModStatus{State: "DISABLED", Reason: "off by default"}, "DISABLED"},
		{ModStatus{State: "FAILED", Reason: "requires multiplayer, which is disabled"}, "FAILED(requires multiplayer, which is disabled)"},
		{ModStatus{State: "NEEDS_REPATCH", Missing: []string{"warp", "face"}}, "NEEDS_REPATCH(warp, face)"},
	} {
		if got := c.m.String(); got != c.want {
			t.Errorf("%+v: %q, want %q", c.m, got, c.want)
		}
	}
}
