package wwapi

import (
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/events"
)

// State is where a mod stands.
type State string

const (
	// Loaded: resolved, and (once a Runtime has run Init) initialised.
	// In a plan nobody has run yet, it means "would load".
	Loaded State = "LOADED"
	// Disabled: turned off by settings, flags, enabled_by_default or dev.
	Disabled State = "DISABLED"
	// Failed: refused before it ran (bad manifest, api or dependency
	// range, missing or failed dependency, cycle, Init error).
	Failed State = "FAILED"
	// Quarantined: its Run or one of its event handlers panicked or
	// returned an error; it was stopped and its handlers removed.
	Quarantined State = "QUARANTINED"
	// NeedsRepatch: the patched ISO lacks a feature the mod needs.
	NeedsRepatch State = "NEEDS_REPATCH"
)

// Status is a State with its reason.
type Status struct {
	State   State
	Reason  string   // why, for every state but Loaded
	Missing []string // NeedsRepatch: the missing features
}

// String formats s the way `mods` shows it: LOADED, DISABLED,
// FAILED(reason), QUARANTINED(reason), NEEDS_REPATCH(a, b).
func (s Status) String() string {
	switch s.State {
	case Loaded, Disabled:
		return string(s.State)
	case NeedsRepatch:
		return string(s.State) + "(" + strings.Join(s.Missing, ", ") + ")"
	}
	return string(s.State) + "(" + s.Reason + ")"
}

// ModInfo is one mod's line in `mods --json`, the TUI and the GUI.
type ModInfo struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Status       State             `json:"status"`
	Reason       string            `json:"reason,omitempty"`
	Missing      []string          `json:"missing,omitempty"`
	Multiplayer  Scope             `json:"multiplayer,omitempty"`
	Dev          bool              `json:"dev,omitempty"`
	Provides     []string          `json:"provides,omitempty"`
	Dependencies map[string]string `json:"dependencies,omitempty"`
	// LoadIndex is the place in the load order, or -1 for a mod that is
	// not in it.
	LoadIndex int `json:"load_index"`
}

// StatusString formats the status the way Status.String does.
func (mi ModInfo) StatusString() string {
	return Status{State: mi.Status, Reason: mi.Reason, Missing: mi.Missing}.String()
}

// ModQuarantined is emitted on the core bus when a mod is quarantined.
type ModQuarantined struct {
	events.Base
	ID     string
	Reason string
}

// EventName implements events.Event.
func (ModQuarantined) EventName() string { return "core.mod_quarantined" }
