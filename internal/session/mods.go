package session

import (
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/events"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// The mods, as the Controller adapts them (docs/api/00-design.md decision
// 15): the mod runtime's event bus (Options.Bus) is the source of truth,
// and the Controller turns what the UIs need from it into EventMod. This
// package imports wwapi/events only: wwapi imports this package (for
// NetProvider), so the statuses come in as ModStatus, which package main
// fills from wwapi's ModInfo.

// ModStatus is one mod as the UIs show it.
type ModStatus struct {
	ID      string
	Name    string
	Version string
	// State is LOADED, DISABLED, FAILED, QUARANTINED or NEEDS_REPATCH
	// (wwapi.State).
	State string
	// Reason says why, for every state but LOADED.
	Reason string
	// Missing are the features a NEEDS_REPATCH mod lacks.
	Missing []string
}

// Loaded is the state of a mod that loaded (wwapi.Loaded).
const Loaded = "LOADED"

// String is the status as `mods` prints it: LOADED, DISABLED,
// FAILED(reason), QUARANTINED(reason) or NEEDS_REPATCH(a, b).
func (m ModStatus) String() string {
	switch {
	case m.State == Loaded || m.State == "DISABLED":
		return m.State
	case m.State == "NEEDS_REPATCH":
		return m.State + "(" + strings.Join(m.Missing, ", ") + ")"
	}
	return m.State + "(" + m.Reason + ")"
}

func (m ModStatus) same(o ModStatus) bool {
	return m.ID == o.ID && m.Name == o.Name && m.Version == o.Version && m.State == o.State &&
		m.Reason == o.Reason && strings.Join(m.Missing, "\x00") == strings.Join(o.Missing, "\x00")
}

// Mods returns the mods with their current status (Options.Mods), or
// none.
func (c *Controller) Mods() []ModStatus {
	if c.opts.Mods == nil {
		return nil
	}
	return c.opts.Mods()
}

// ModLog is the Reporter the mod runtime logs to (its own lines and every
// mod's): each line is an EventLog of the running session, or of none
// (Session 0) between sessions, and goes to Options.Log too.
func (c *Controller) ModLog() log.Reporter { return modLog{c} }

type modLog struct{ c *Controller }

func (l modLog) Log(level log.Level, msg string) {
	c := l.c
	c.mu.Lock()
	var id ID
	if c.cur != nil {
		id = c.cur.id
	}
	c.mu.Unlock()
	c.publish(Event{Kind: EventLog, Session: id, Level: level, Text: msg})
	if c.opts.Log != nil {
		c.opts.Log.Log(level, msg)
	}
}

// adaptBus subscribes to Options.Bus, if any, from New. The subscription
// ends when the bus closes (the runtime stopped).
func (c *Controller) adaptBus() {
	if c.opts.Bus == nil {
		return
	}
	c.modSeen = map[string]ModStatus{}
	for _, m := range c.Mods() {
		c.modSeen[m.ID] = m
	}
	events.On(c.opts.Bus, c.onBus)
}

// modPrefix and modStatusPrefix are the event names the Controller
// adapts: a mod's own events, and the runtime's core.mod_* status events
// (wwapi.ModQuarantined, core.mod_quarantined).
const (
	modPrefix       = events.ModNamespace + "."
	modStatusPrefix = events.CoreNamespace + ".mod_"
)

// onBus is the adapter: one bus event in, EventMods out. Other events
// (the game's core.* ones) are not session events and are left to the
// mods.
func (c *Controller) onBus(e events.Event) {
	name := e.EventName()
	switch {
	case strings.HasPrefix(name, modPrefix):
		id, _, _ := strings.Cut(strings.TrimPrefix(name, modPrefix), ".")
		m := ModStatus{ID: id}
		c.modMu.Lock()
		if s, ok := c.modSeen[id]; ok {
			m = s
		}
		c.modMu.Unlock()
		c.publish(Event{Kind: EventMod, Mod: m, Payload: e, Text: name})
	case strings.HasPrefix(name, modStatusPrefix):
		c.modStatuses()
	}
}

// modStatuses publishes an EventMod for each mod whose status changed
// since the last one published (or since New).
func (c *Controller) modStatuses() {
	var changed []ModStatus
	c.modMu.Lock()
	for _, m := range c.Mods() {
		if old, ok := c.modSeen[m.ID]; !ok || !old.same(m) {
			c.modSeen[m.ID] = m
			changed = append(changed, m)
		}
	}
	c.modMu.Unlock()
	for _, m := range changed {
		c.publish(Event{Kind: EventMod, Mod: m, Text: m.String()})
	}
}
