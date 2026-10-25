// Package config holds the desktop app's saved settings:
// <os.UserConfigDir>/ww-multiplayer/config.json, the root co-op already
// uses for worlds/ and backups/ (docs/01-architecture.md, "Config").
//
// The file is schema-versioned JSON. Load is tolerant: a missing file is
// the defaults, and a file that can't be read or parsed, a top-level
// section that doesn't decode (all of "dolphin", say, when one of its
// fields has the wrong type), or a value out of range gives the defaults
// for what is bad plus a warning, never an error that stops the app. Save writes
// atomically (a temp file renamed over the old one), keeps the fields this
// build doesn't know (a newer build's), and never lowers the schema
// version a newer build wrote. A file Load could not parse, or one over
// MaxSize, is kept as config.json.bad when the first Save replaces it; a
// file Load could not open or read gives the defaults and a warning only.
//
// Environment variables override the saved settings (WithEnv: DOLPHIN_EXE,
// ISO_PATH, USER_DIR_1, WW_COLOR), so a developer's setup keeps working in
// the app. The CLI does not read this file at all (owner decision 11):
// scripts and tests stay deterministic, and env vars remain its knobs.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/playercolor"
)

// Version is the schema version this build writes.
const Version = 1

// MaxSize is the largest config file Load reads; a bigger one is treated
// as unreadable.
const MaxSize = 1 << 20

// MaxRecentHosts is how many recent hosts the config keeps.
const MaxRecentHosts = 10

// DefaultPort is the server port of a new config.
const DefaultPort = 25565

// Config is the app's settings. Every field has a JSON name; a field the
// file lacks keeps its default.
type Config struct {
	Version     int          `json:"version"`
	Name        string       `json:"name"`
	Color       string       `json:"color"` // a palette name or "#RRGGBB" (playercolor.Parse); "" for classic green
	RecentHosts []RecentHost `json:"recentHosts"`
	Port        int          `json:"port"`
	Dolphin     Dolphin      `json:"dolphin"`
	ISO         ISO          `json:"iso"`
	Coop        Coop         `json:"coop"`
	UI          UI           `json:"ui"`
	// Update is the update check (internal/update): the "Check for
	// updates" switch and the last answer, so the app asks GitHub at most
	// every update.CheckEvery.
	Update Update `json:"update"`
	// Mods are the mods' settings by mod id (docs/api/00-design.md §6.5):
	// on or off for the next start, and each mod's own settings. Written
	// only when there is one, so a file from before mods had settings
	// saves as it was.
	Mods map[string]ModConfig `json:"mods,omitempty"`

	// extra is the file's whole JSON object as read, so Save keeps the
	// fields this build doesn't know. nil for a new Config.
	extra map[string]json.RawMessage
	// broken says Load could not parse the file, which Save then keeps as
	// config.json.bad.
	broken bool
}

// RecentHost is one host the player joined.
type RecentHost struct {
	Addr     string    `json:"addr"`
	Label    string    `json:"label"`
	LastUsed time.Time `json:"lastUsed"`
}

// Dolphin is where Dolphin is and how the app starts it.
type Dolphin struct {
	Exe           string `json:"exe"`
	UserDir       string `json:"userDir"`
	LaunchOnStart bool   `json:"launchOnStart"`
	InstallCheats bool   `json:"installCheats"`
}

// ISO is the disc images: the patched one Dolphin boots, and the vanilla
// one only so the app can patch again after an update.
type ISO struct {
	Patched string `json:"patched"`
	Vanilla string `json:"vanilla"`
}

// Coop is the host's last co-op choice (the Lobby's co-op panel), and
// this player's own relay choices whichever side it plays.
type Coop struct {
	LastMode  string `json:"lastMode"` // "off", "new" or "continue"
	LastWorld string `json:"lastWorld"`
	DryRun    bool   `json:"dryRun"`
	// Merge, LiveSwitches and ISO are the panel's Advanced choices:
	// --coop-merge, --coop-live-switches and --coop-iso ("" for the disc
	// patch last wrote).
	Merge        bool   `json:"merge,omitempty"`
	LiveSwitches bool   `json:"liveSwitches,omitempty"`
	ISO          string `json:"iso,omitempty"`
	// BossKill and Refresh are the save-file panel's relay choices, each
	// side's own (host or joiner): --coop-boss-kill ("" for its default,
	// gohma,kalle,molgera,jalhalla,helmaroc) and --coop-refresh ("" for off).
	BossKill string `json:"bossKill,omitempty"`
	Refresh  string `json:"refresh,omitempty"`
}

// UI is window and first-run state.
type UI struct {
	Window    Window `json:"window"`
	SetupDone bool   `json:"setupDone"`
	// ShowIPs turns off the window's IP masking (the header's "Show IP
	// addresses" switch). Off by default: the window masks every IP it
	// shows, so a stream never flashes one (owner decision 2026-10-16).
	ShowIPs bool `json:"showIPs"`
}

// Update is the app's update check (owner decision 2026-10-17, one-click
// updates).
type Update struct {
	// Check is the "Check for updates" switch: on by default.
	Check bool `json:"check"`
	// LastCheck is when GitHub was last asked; Latest the newest release
	// it named, NotesURL that release's page.
	LastCheck time.Time `json:"lastCheck"`
	Latest    string    `json:"latest"`
	NotesURL  string    `json:"notesURL"`
	// Installed is set by an update just before it relaunches the app:
	// the version it put in place. The new build clears it at its first
	// start and re-patches the disc if its game code differs.
	Installed string `json:"installed"`
}

// ModConfig is one mod's settings: mods.<id> in the file.
type ModConfig struct {
	// Enabled turns the mod on or off from the next start; nil keeps its
	// manifest default (enabled_by_default).
	Enabled *bool `json:"enabled,omitempty"`
	// Settings is the mod's own settings, a JSON object the mod reads
	// through wwapi's Context.Config(); nil for none.
	Settings json.RawMessage `json:"settings,omitempty"`
}

// ModEnabled is mods.<id>.enabled, and whether it is set. With
// ModSettings, it makes a Config a wwapi.ConfigSource (this package
// imports no wwapi).
func (c Config) ModEnabled(id string) (on, set bool) {
	m, ok := c.Mods[id]
	if !ok || m.Enabled == nil {
		return false, false
	}
	return *m.Enabled, true
}

// ModSettings is mods.<id>.settings, or nil.
func (c Config) ModSettings(id string) json.RawMessage {
	return bytes.Clone(c.Mods[id].Settings)
}

// SetModEnabled sets mods.<id>.enabled, keeping the mod's settings.
func (c *Config) SetModEnabled(id string, on bool) {
	if c.Mods == nil {
		c.Mods = map[string]ModConfig{}
	}
	m := c.Mods[id]
	m.Enabled = &on
	c.Mods[id] = m
}

// Window is the main window's size.
type Window struct {
	W int `json:"w"`
	H int `json:"h"`
}

// Defaults returns a new player's settings.
func Defaults() Config {
	return Config{
		Version: Version,
		Port:    DefaultPort,
		Dolphin: Dolphin{LaunchOnStart: true, InstallCheats: true},
		Coop:    Coop{LastMode: "off"},
		UI:      UI{Window: Window{W: 1100, H: 720}},
		Update:  Update{Check: true},
	}
}

// Dir is <os.UserConfigDir>/ww-multiplayer, the app's root.
func Dir() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: no config directory: %w", err)
	}
	return filepath.Join(cfg, "ww-multiplayer"), nil
}

// Path is the config file under Dir.
func Path() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.json"), nil
}

// Load reads the config at path. A missing file gives Defaults and no
// warning. Anything wrong with the file gives the defaults for what is
// wrong, and one warning line each, for the app to show or log; Load never
// fails.
func Load(path string) (Config, []string) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Defaults(), nil
	}
	if err != nil {
		return Defaults(), []string{fmt.Sprintf("config: can't read %s (%v); using the default settings", path, err)}
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxSize+1))
	if err != nil {
		return Defaults(), []string{fmt.Sprintf("config: can't read %s (%v); using the default settings", path, err)}
	}
	if len(b) > MaxSize {
		c := Defaults()
		c.broken = true
		return c, []string{fmt.Sprintf("config: %s is larger than %d bytes; using the default settings (the file is kept as config.json.bad when settings are saved)", path, MaxSize)}
	}
	c, warns := Parse(b)
	for i, w := range warns {
		warns[i] = "config: " + path + ": " + w
	}
	return c, warns
}

// Parse decodes a config file's bytes, tolerantly (see Load). Each
// warning says what was wrong and what is used instead.
func Parse(b []byte) (Config, []string) {
	c := Defaults()
	var warns []string
	b = bytes.TrimPrefix(b, []byte("\xEF\xBB\xBF")) // a BOM from Notepad
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil || raw == nil {
		if err == nil {
			err = errors.New("not a JSON object")
		}
		c.broken = true
		return c, []string{fmt.Sprintf("not valid settings (%v); using the default settings (the file is kept as config.json.bad when settings are saved)", err)}
	}
	c.extra = raw
	// Each known field on its own, so one bad field keeps the rest. Keys
	// match without case, preferring an exact match, as encoding/json does
	// for the nested sections.
	fields := c.fields()
	for _, k := range sortedKeys(fields) {
		v, ok := lookupFold(raw, k)
		if !ok || string(v) == "null" {
			continue
		}
		def := fields[k].snapshot()
		if err := json.Unmarshal(v, fields[k].ptr); err != nil {
			fields[k].restore(def)
			warns = append(warns, fmt.Sprintf("%q is not valid (%v); using the default", k, shortErr(err)))
		}
	}
	warns = append(warns, c.validate()...)
	if c.Version > Version {
		warns = append(warns, fmt.Sprintf("written by a newer version (settings schema %d, this version reads %d); settings this version doesn't know are kept", c.Version, Version))
	}
	return c, warns
}

// field is one top-level JSON field: where it decodes to, and how to undo
// a decode that failed half-way.
type field struct {
	ptr      any
	snapshot func() any
	restore  func(any)
}

func fieldOf[T any](p *T) field {
	return field{
		ptr:      p,
		snapshot: func() any { return *p },
		restore:  func(v any) { *p = v.(T) },
	}
}

func (c *Config) fields() map[string]field {
	return map[string]field{
		"version":     fieldOf(&c.Version),
		"name":        fieldOf(&c.Name),
		"color":       fieldOf(&c.Color),
		"recentHosts": fieldOf(&c.RecentHosts),
		"port":        fieldOf(&c.Port),
		"dolphin":     fieldOf(&c.Dolphin),
		"iso":         fieldOf(&c.ISO),
		"coop":        fieldOf(&c.Coop),
		"ui":          fieldOf(&c.UI),
		"update":      fieldOf(&c.Update),
		"mods":        fieldOf(&c.Mods),
	}
}

// validate puts each out-of-range value back to its default.
func (c *Config) validate() []string {
	d := Defaults()
	var warns []string
	if c.Version < 1 {
		c.Version = Version
	}
	if c.Port < 1 || c.Port > 65535 {
		warns = append(warns, fmt.Sprintf("port %d is not a TCP port; using %d", c.Port, d.Port))
		c.Port = d.Port
	}
	if c.Color != "" {
		if _, err := playercolor.Parse(c.Color); err != nil {
			warns = append(warns, fmt.Sprintf("color %q is not a tunic color (%v); using classic green", c.Color, err))
			c.Color = ""
		}
	}
	if len(c.Name) > 64 || strings.ContainsAny(c.Name, "\x00\r\n") {
		warns = append(warns, "name is not a player name; it is left empty")
		c.Name = ""
	}
	switch c.Coop.LastMode {
	case "off", "new", "continue":
	default:
		warns = append(warns, fmt.Sprintf("co-op mode %q is not off, new or continue; using off", c.Coop.LastMode))
		c.Coop.LastMode = d.Coop.LastMode
	}
	// The relay choices as the CLI takes them (replica's parsers): one it
	// would refuse would stop co-op at Start.
	if _, err := replica.ParseBossKill(c.Coop.BossKill); err != nil {
		warns = append(warns, fmt.Sprintf("co-op boss-kill rows %q are not valid (%v); using the default", c.Coop.BossKill, err))
		c.Coop.BossKill = d.Coop.BossKill
	}
	if _, err := replica.ParseRefresh(c.Coop.Refresh); err != nil {
		warns = append(warns, fmt.Sprintf("co-op refresh tokens %q are not valid (%v); using none", c.Coop.Refresh, err))
		c.Coop.Refresh = d.Coop.Refresh
	}
	if w := c.UI.Window; w.W < 320 || w.H < 240 || w.W > 16384 || w.H > 16384 {
		c.UI.Window = d.UI.Window // a lost window size is not worth a warning
	}
	// What a release named: a tag and a page, never anything long.
	if u := &c.Update; len(u.Latest) > 64 || strings.ContainsAny(u.Latest, "\x00\r\n") {
		u.Latest, u.NotesURL = "", ""
	}
	if u := &c.Update; len(u.NotesURL) > 512 || !strings.HasPrefix(u.NotesURL, "https://") && !strings.HasPrefix(u.NotesURL, "http://127.0.0.1") {
		u.NotesURL = ""
	}
	if len(c.Update.Installed) > 64 {
		c.Update.Installed = ""
	}
	hosts := c.RecentHosts[:0:0]
	for _, h := range c.RecentHosts {
		if strings.TrimSpace(h.Addr) != "" {
			hosts = append(hosts, h)
		}
	}
	if len(hosts) > MaxRecentHosts {
		hosts = hosts[:MaxRecentHosts]
	}
	c.RecentHosts = hosts
	for _, id := range sortedModIDs(c.Mods) {
		m := c.Mods[id]
		raw := bytes.TrimSpace(m.Settings)
		switch {
		case len(raw) == 0 || string(raw) == "null":
			m.Settings = nil
		case raw[0] != '{':
			// {} rather than nothing, so a save writes it over the bad value
			// instead of keeping that (Marshal keeps what the file had).
			warns = append(warns, fmt.Sprintf("the settings of mod %q are not a JSON object; using none", id))
			m.Settings = json.RawMessage("{}")
		default:
			// The form Marshal writes (keys sorted, compact), so a file
			// reads the same after a save.
			m.Settings = mergeObjects(raw, raw)
		}
		c.Mods[id] = m
	}
	if len(c.Mods) == 0 {
		c.Mods = nil
	}
	return warns
}

func sortedModIDs(m map[string]ModConfig) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Marshal encodes c as Save writes it: the known fields, then the fields
// of the file c was loaded from that this build doesn't know, keys sorted,
// indented. A config a newer build wrote keeps its schema version.
func (c Config) Marshal() ([]byte, error) {
	if c.Version < Version {
		c.Version = Version
	}
	if c.RecentHosts == nil {
		c.RecentHosts = []RecentHost{}
	}
	fields := c.fields()
	out := map[string]json.RawMessage{}
	for k, v := range c.extra {
		// A key that folds to a known field's was read as that field (Parse),
		// so it is written once, under the field's own name, below.
		if _, known := fields[k]; !known && hasFieldFold(fields, k) {
			continue
		}
		out[k] = v
	}
	for k, f := range fields {
		v, err := json.Marshal(f.ptr)
		if err != nil {
			return nil, fmt.Errorf("config: %s: %w", k, err)
		}
		// A known object keeps the unknown fields it had in the file, and
		// every object is written with sorted keys, as a merged one is.
		old, ok := lookupFold(c.extra, k)
		if !ok {
			old = v
		}
		v = mergeObjects(old, v)
		out[k] = v
	}
	if len(c.Mods) == 0 {
		// No mod settings: no "mods" key, as before mods had settings (a
		// "mods" the file had that did not decode is dropped with them).
		delete(out, "mods")
	}
	if _, had := lookupFold(c.extra, "update"); !had && c.Update == Defaults().Update {
		// No update check yet: no "update" key, so a file from before the
		// check saves as it was.
		delete(out, "update")
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return append(b, '\n'), nil
}

// mergeObjects returns cur with the keys of old that cur lacks, when both
// are JSON objects, recursively, every object's keys sorted; otherwise cur.
func mergeObjects(old, cur json.RawMessage) json.RawMessage {
	var o, n map[string]json.RawMessage
	if json.Unmarshal(cur, &n) != nil || n == nil {
		return cur
	}
	if json.Unmarshal(old, &o) != nil {
		o = nil // not an object: nothing to keep from it
	}
	for k, nv := range n {
		ov, ok := o[k]
		if !ok {
			ov = nv // nothing to keep: only sort its keys
		}
		n[k] = mergeObjects(ov, nv)
	}
	for k, ov := range o {
		// encoding/json matches field names without case, so a key that
		// folds to one of cur's would be read as it: it is not kept.
		if !hasKeyFold(n, k) {
			n[k] = ov
		}
	}
	b, err := json.Marshal(n)
	if err != nil {
		return cur
	}
	return b
}

// lookupFold returns m[k], or else the value of a key that folds to k (the
// first such key in sorted order, so the result doesn't depend on map
// order).
func lookupFold(m map[string]json.RawMessage, k string) (json.RawMessage, bool) {
	if v, ok := m[k]; ok {
		return v, true
	}
	best := ""
	found := false
	for mk := range m {
		if strings.EqualFold(mk, k) && (!found || mk < best) {
			best, found = mk, true
		}
	}
	if !found {
		return nil, false
	}
	return m[best], true
}

func hasFieldFold(fields map[string]field, k string) bool {
	for fk := range fields {
		if strings.EqualFold(fk, k) {
			return true
		}
	}
	return false
}

func hasKeyFold(m map[string]json.RawMessage, k string) bool {
	for mk := range m {
		if strings.EqualFold(mk, k) {
			return true
		}
	}
	return false
}

// Save writes c to path atomically: to a temp file in the same directory,
// synced, then renamed over path, so a crash leaves the old file or the
// new one, never half of one. The directory is created if needed. A file
// Load could not parse (or one over MaxSize) is kept as <path>.bad: it is
// moved there only once the new file is written and synced, just before
// the rename, and only by the first Save that succeeds, so saving the same
// Config again never replaces .bad with a good file.
func (c *Config) Save(path string) error {
	b, err := c.Marshal()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name) // gone after the rename; cleans up a failure
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return fmt.Errorf("config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	kept := false
	if c.broken {
		if _, err := os.Stat(path); err == nil {
			if err := rename(path, path+".bad"); err != nil {
				return fmt.Errorf("config: keeping the unreadable file: %w", err)
			}
			kept = true
		}
	}
	if err := rename(name, path); err != nil {
		if kept {
			_ = rename(path+".bad", path) // put the old file back; Load warns again
		}
		return fmt.Errorf("config: %w", err)
	}
	c.broken = false
	return nil
}

// rename is os.Rename; a test replaces it to make Save's last step fail.
var rename = os.Rename

// AddRecentHost puts addr first in RecentHosts (moving it if it is there,
// keeping its label unless label is set) and keeps the newest
// MaxRecentHosts.
func (c *Config) AddRecentHost(addr, label string, now time.Time) {
	h := RecentHost{Addr: addr, Label: label, LastUsed: now}
	rest := []RecentHost{}
	for _, o := range c.RecentHosts {
		if strings.EqualFold(o.Addr, addr) {
			if label == "" {
				h.Label = o.Label
			}
			continue
		}
		rest = append(rest, o)
	}
	c.RecentHosts = append([]RecentHost{h}, rest...)
	if len(c.RecentHosts) > MaxRecentHosts {
		c.RecentHosts = c.RecentHosts[:MaxRecentHosts]
	}
}

// Env names the variables WithEnv applies.
const (
	EnvDolphinExe = "DOLPHIN_EXE"
	EnvISO        = "ISO_PATH"
	EnvUserDir    = "USER_DIR_1"
	EnvColor      = "WW_COLOR"
)

// WithEnv returns c with each setting whose environment variable getenv
// sets to a non-empty value replaced by it, and the names of the variables
// it used. The app runs on the result and saves c, so an override is never
// written to the file.
func (c Config) WithEnv(getenv func(string) string) (Config, []string) {
	var used []string
	for _, o := range []struct {
		key string
		dst *string
	}{
		{EnvDolphinExe, &c.Dolphin.Exe},
		{EnvISO, &c.ISO.Patched},
		{EnvUserDir, &c.Dolphin.UserDir},
		{EnvColor, &c.Color},
	} {
		if v := getenv(o.key); v != "" {
			*o.dst = v
			used = append(used, o.key)
		}
	}
	return c, used
}

func shortErr(err error) string {
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) {
		if te.Field != "" {
			return fmt.Sprintf("%s: %s where %s was expected", te.Field, te.Value, te.Type)
		}
		return fmt.Sprintf("%s where %s was expected", te.Value, te.Type)
	}
	return err.Error()
}

func sortedKeys(m map[string]field) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
