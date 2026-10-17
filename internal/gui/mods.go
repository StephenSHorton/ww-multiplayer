package gui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/config"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
)

// The Mods page (docs/api/00-design.md §6.5): every mod of the app's mod
// runtime (Options.Mods) with its version, status and reason, a toggle
// saved as config mods.<id>.enabled that takes effect the next time the
// app starts (the runtime resolved this run's mods when it started), a
// Patch button beside NEEDS_REPATCH, and the mod's commands (CallMod).
// A reason is masked like every other line the window draws (mask.go).

// callModTimeout bounds one CallMod.
const callModTimeout = 10 * time.Second

// ModConfig is the saved mod settings (config.Mods) as a
// wwapi.ConfigSource, for package main's mod runtime: the app's faces
// read the settings here, since package main never imports
// internal/config. path "" is config.Path(); nil when there is no config
// directory. A config that can't be read gives the defaults (no settings),
// as Load does; the window shows its warnings.
func ModConfig(path string) wwapi.ConfigSource {
	if path == "" {
		p, err := config.Path()
		if err != nil {
			return nil
		}
		path = p
	}
	c, _ := config.Load(path)
	return c
}

// ModsView is the Mods page.
type ModsView struct {
	Mods []ModView `json:"mods"`
	// Dev says this run lists the dev mods (--dev).
	Dev bool `json:"dev"`
	// ShowIPs is the switch the reasons were masked under.
	ShowIPs bool `json:"showIPs"`
}

// ModView is one mod's row.
type ModView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	// Status is the state (LOADED, DISABLED, FAILED, QUARANTINED,
	// NEEDS_REPATCH), StatusText the way `mods` prints it, Reason why
	// (masked, as every line the window draws).
	Status     string   `json:"status"`
	StatusText string   `json:"statusText"`
	Reason     string   `json:"reason"`
	Missing    []string `json:"missing"`
	Dev        bool     `json:"dev"`
	// Requires are the mods it needs (its dependencies' ids).
	Requires []string `json:"requires"`
	// Net says it is the network session provider: without it, Host and
	// Join are off.
	Net bool `json:"net"`
	// Enabled is the toggle: whether the mod is on the next time the app
	// starts (its saved setting, else its manifest's default).
	Enabled bool `json:"enabled"`
	// Saved says Enabled is a saved choice, not the default.
	Saved bool `json:"saved"`
	// Pending says Enabled differs from this run (a restart applies it).
	Pending bool `json:"pending"`
	// Flag is "on" or "off" when this run's --mods or --no-mod set it,
	// else "".
	Flag string `json:"flag"`
	// Verbs are the mod's commands (mod.<id>.<verb>) CallMod runs.
	Verbs []string `json:"verbs"`
}

// CallModView is a CallMod's outcome.
type CallModView struct {
	OK   bool   `json:"ok"`
	Code string `json:"code"`
}

// Mods lists the app's mods for the Mods page: the loading ones first in
// load order, then the rest by id, as `mods` does.
func (b *Bridge) Mods() ModsView {
	mk := b.masker()
	v := ModsView{Mods: []ModView{}, Dev: b.devMods, ShowIPs: mk.show}
	if b.mods == nil {
		return v
	}
	man := map[string]wwapi.Manifest{}
	for _, e := range b.mods.Plan().Entries {
		if e.Manifest.Err() == nil {
			man[e.Manifest.ID] = e.Manifest
		}
	}
	names := b.mods.Commands().Names()
	b.mu.Lock()
	cfg := b.cfg
	b.mu.Unlock()
	for _, mi := range b.mods.Mods() {
		m := man[mi.ID]
		mv := ModView{
			ID: mi.ID, Name: mi.Name, Version: mi.Version, Description: m.Description,
			Status: string(mi.Status), StatusText: mk.text(mi.StatusString()), Reason: mk.text(mi.Reason),
			Missing: append([]string{}, mi.Missing...), Dev: mi.Dev, Requires: []string{}, Verbs: []string{},
			Net: slices.Contains(mi.Provides, wwapi.ProvideNet),
		}
		for id := range mi.Dependencies {
			mv.Requires = append(mv.Requires, id)
		}
		sort.Strings(mv.Requires)
		on, saved := cfg.ModEnabled(mi.ID)
		if !saved {
			on = m.EnabledByDefault
		}
		mv.Enabled, mv.Saved = on, saved
		// This run tried the mod unless it was off (by its setting, a
		// flag or its default); a restart without flags applies Enabled.
		mv.Pending = on != (mi.Status != wwapi.Disabled)
		if b.modFlag != nil {
			if fon, set := b.modFlag(mi.ID); set {
				mv.Flag = map[bool]string{true: "on", false: "off"}[fon]
			}
		}
		prefix := "mod." + mi.ID + "."
		for _, n := range names {
			if verb, ok := strings.CutPrefix(n, prefix); ok && mi.Status == wwapi.Loaded {
				mv.Verbs = append(mv.Verbs, verb)
			}
		}
		v.Mods = append(v.Mods, mv)
	}
	return v
}

// SetModEnabled saves the Mods page's toggle (config mods.<id>.enabled):
// the mod is on or off from the next time the app starts. Every page
// gets a "mods" event to draw the page again.
func (b *Bridge) SetModEnabled(id string, on bool) (err error) {
	defer b.maskErr(&err)
	if !b.hasMod(id) {
		return fmt.Errorf("there is no mod %q in this app", id)
	}
	b.mu.Lock()
	b.cfg.SetModEnabled(id, on)
	b.mu.Unlock()
	b.save()
	b.emit(EventView{Kind: "mods", Time: b.now().UTC().Format(time.RFC3339Nano)})
	return nil
}

// CallMod runs one of a loaded mod's commands, mod.<id>.<verb>, with args
// (JSON, or nothing). A command that fails returns its error.
func (b *Bridge) CallMod(id, verb string, args json.RawMessage) (_ CallModView, err error) {
	defer b.maskErr(&err)
	if !b.hasMod(id) {
		return CallModView{}, fmt.Errorf("there is no mod %q in this app", id)
	}
	if s, _ := b.mods.Status(id); s.State != wwapi.Loaded {
		return CallModView{}, fmt.Errorf("mod %s is not running (%s)", id, s)
	}
	if verb == "" {
		return CallModView{}, errors.New("say which command to run")
	}
	if len(args) > 0 && strings.TrimSpace(string(args)) == "null" {
		args = nil
	}
	ctx, cancel := context.WithTimeout(b.ctx, callModTimeout)
	defer cancel()
	res, err := b.mods.Commands().Execute(ctx, "mod."+id+"."+verb, args)
	if err != nil {
		return CallModView{Code: res.Code}, err
	}
	return CallModView{OK: res.OK, Code: res.Code}, nil
}

// errNoCoopMod is SetCoop's error for a co-op choice when the co-op mod
// is off this run.
var errNoCoopMod = errors.New("co-op is off: the Co-op mod isn't running (turn it on in Mods, then open the app again)")

// coopMod says whether the co-op mod runs this run: then the Session
// screen offers co-op (coopView). Without a runtime (tests, an app built
// without mods) it does, as before mods.
func (b *Bridge) coopMod() bool {
	if b.mods == nil {
		return true
	}
	s, ok := b.mods.Status("coop")
	return ok && s.State == wwapi.Loaded
}

// hasMod says whether the runtime has mod id (in any state).
func (b *Bridge) hasMod(id string) bool {
	if b.mods == nil || id == "" {
		return false
	}
	_, ok := b.mods.Status(id)
	return ok
}
