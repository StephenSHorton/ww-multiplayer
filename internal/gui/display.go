package gui

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/config"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
)

// The Settings page's Display card (docs/design/native-widescreen.md §7):
// the widescreen setting, saved as config mods.widescreen.settings =
// {"aspect": "16:9" | "W:H" | "off"}. It applies at the next Start, which
// launches Dolphin with it (launchOptions). The widescreen mod's
// ParseAppSetting is launch.ParseWidescreenSetting, which validates here
// too (this package imports no mods).

// widescreenMod is the widescreen mod's id: its settings hold the aspect.
const widescreenMod = "widescreen"

// widescreenDefault is an unset setting: off in R1 (stage 1 leaves the
// HUD stretched), 16:9 from the release with stages 2 and 3.
const widescreenDefault = "off"

// widescreenAspect is c's saved aspect as Start passes it on, the default
// when none is saved.
func widescreenAspect(c config.Config) string {
	var ws struct {
		Aspect string `json:"aspect"`
	}
	_ = json.Unmarshal(c.ModSettings(widescreenMod), &ws)
	if ws.Aspect == "" {
		return widescreenDefault
	}
	return ws.Aspect
}

// canonAspect is the saved form of a valid setting: "off", or W:H.
func canonAspect(s launch.WidescreenSetting) string {
	if s.Off() {
		return "off"
	}
	return strconv.Itoa(s.W) + ":" + strconv.Itoa(s.H)
}

// Widescreen is the Display card's setting: "off" or W:H ("16:9",
// "21:9", "64:27"). A saved value Start can't read shows as off.
func (b *Bridge) Widescreen() string {
	b.mu.Lock()
	c := b.cfg
	b.mu.Unlock()
	s, err := launch.ParseWidescreenSetting(widescreenAspect(c))
	if err != nil {
		return "off"
	}
	return canonAspect(s)
}

// SetWidescreen saves the Display card's setting: off, 16:9, 16:10, 21:9
// or a whole-number W:H up to 4:1 (4:3 or narrower is off). It keeps the
// mod's other settings, applies at the next Start, and sends a
// "settings" event so every page draws it again.
func (b *Bridge) SetWidescreen(aspect string) (err error) {
	defer b.maskErr(&err)
	s, err := launch.ParseWidescreenSetting(aspect)
	if err != nil {
		return err
	}
	b.mu.Lock()
	if b.cfg.Mods == nil {
		b.cfg.Mods = map[string]config.ModConfig{}
	}
	m := b.cfg.Mods[widescreenMod]
	set := map[string]json.RawMessage{}
	_ = json.Unmarshal(m.Settings, &set)
	if set == nil {
		set = map[string]json.RawMessage{}
	}
	set["aspect"], _ = json.Marshal(canonAspect(s))
	m.Settings, _ = json.Marshal(set)
	b.cfg.Mods[widescreenMod] = m
	b.mu.Unlock()
	b.save()
	b.emit(EventView{Kind: "settings", Time: b.now().UTC().Format(time.RFC3339Nano)})
	return nil
}
