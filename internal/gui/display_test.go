package gui

import (
	"encoding/json"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/config"
)

// TestBridgeSetWidescreen: the Display card's setting is saved as
// mods.widescreen.settings.aspect in its canonical form, keeps the mod's
// other settings and its enabled switch, reaches Start's launch options,
// emits "settings", and an invalid one changes nothing.
func TestBridgeSetWidescreen(t *testing.T) {
	b, _, path := newTestBridge(t)
	if got := b.Widescreen(); got != "off" {
		t.Errorf("unset = %q, want off (R1)", got)
	}
	if got := b.launchOptions().Widescreen; got != "off" {
		t.Errorf("unset launches with %q", got)
	}
	on := true
	b.cfg.Mods = map[string]config.ModConfig{"widescreen": {Enabled: &on, Settings: json.RawMessage(`{"other":1}`)}}
	got := make(chan EventView, 8)
	b.emu.Lock()
	b.emitters[99] = func(ev EventView) { got <- ev }
	b.emu.Unlock()

	for _, tc := range []struct{ in, want string }{
		{"16:9", "16:9"},
		{" 21:9 ", "21:9"},
		{"016:010", "16:10"},
		{"64:27", "64:27"},
		{"4:3", "off"}, // at or below 4:3 counts as off
		{"5:4", "off"},
		{"OFF", "off"},
		{"", "off"},
	} {
		if err := b.SetWidescreen(tc.in); err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if ev := <-got; ev.Kind != "settings" {
			t.Fatalf("%q: event %+v", tc.in, ev)
		}
		if g := b.Widescreen(); g != tc.want {
			t.Errorf("%q: Widescreen = %q, want %q", tc.in, g, tc.want)
		}
		if g := b.launchOptions().Widescreen; g != tc.want {
			t.Errorf("%q: launches with %q, want %q", tc.in, g, tc.want)
		}
	}

	if err := b.SetWidescreen("21:9"); err != nil {
		t.Fatal(err)
	}
	// The mod switched off on the Mods page: Dolphin launches vanilla.
	off := false
	m := b.cfg.Mods["widescreen"]
	m.Enabled = &off
	b.cfg.Mods["widescreen"] = m
	if g := b.launchOptions().Widescreen; g != "off" {
		t.Errorf("mod disabled: launches with %q, want off", g)
	}
	m.Enabled = &on
	b.cfg.Mods["widescreen"] = m
	for _, bad := range []string{"auto", "2.39", "16x9", "5:1", "0:9", "-16:9", "a:b"} {
		if err := b.SetWidescreen(bad); err == nil {
			t.Errorf("%q was saved", bad)
		}
		if g := b.Widescreen(); g != "21:9" {
			t.Errorf("after %q: %q", bad, g)
		}
	}

	c, warns := config.Load(path)
	if len(warns) != 0 {
		t.Fatal(warns)
	}
	var set map[string]any
	if err := json.Unmarshal(c.ModSettings("widescreen"), &set); err != nil {
		t.Fatal(err)
	}
	if set["aspect"] != "21:9" || set["other"] != float64(1) {
		t.Errorf("saved settings %v", set)
	}
	if on, saved := c.ModEnabled("widescreen"); !on || !saved {
		t.Errorf("enabled lost: %v %v", on, saved)
	}
}

// TestWidescreenUnreadable: a hand-edited value Start can't read shows
// as off on the card.
func TestWidescreenUnreadable(t *testing.T) {
	b, _, _ := newTestBridge(t)
	b.cfg.Mods = map[string]config.ModConfig{"widescreen": {Settings: json.RawMessage(`{"aspect":"auto"}`)}}
	if got := b.Widescreen(); got != "off" {
		t.Errorf("Widescreen = %q", got)
	}
}
