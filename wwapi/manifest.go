package wwapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/events"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/schema"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/semver"
)

// ErrManifest wraps every reason a mod.json is refused.
var ErrManifest = errors.New("wwapi: bad mod.json")

// Scope is a mod's "multiplayer" field.
type Scope string

const (
	ClientRequired Scope = "client_required" // every player needs it; counts in mods_hash
	HostOnly       Scope = "host_only"       // only the host runs it
	LocalOnly      Scope = "local_only"      // never touches the network (the default)
)

// ProvideNet is the "provides" role of the network session provider.
const ProvideNet = "net"

// MEM1 bounds a declared write range (48 MB since docs/05 "48 MB MEM1";
// addr.MEM1End is the same number).
const (
	MEM1Start = 0x80000000
	MEM1End   = 0x83000000
)

// AddrRange is one declared write range: [Start, End) in MEM1.
type AddrRange struct {
	Start, End uint32
	Why        string
}

// Overlaps reports whether r and o share a byte.
func (r AddrRange) Overlaps(o AddrRange) bool { return r.Start < o.End && o.Start < r.End }

func (r AddrRange) String() string { return fmt.Sprintf("0x%08X-0x%08X", r.Start, r.End) }

// Manifest is a parsed mod.json. Use [ParseManifest] or [ManifestFrom];
// treat the fields as read-only.
type Manifest struct {
	ID          string
	Name        string
	Description string
	Version     semver.Version
	API         semver.Range
	// Dependencies and OptionalDependencies map a mod id to a range.
	Dependencies         map[string]semver.Range
	OptionalDependencies map[string]semver.Range
	LoadOrder            int
	EnabledByDefault     bool
	Dev                  bool
	GameIDs              []string
	Multiplayer          Scope
	Provides             []string
	Needs                []string
	// Writes is the declared write set (a placeholder: nothing enforces
	// it until wwapi/write exists). Sorted by Start.
	Writes []AddrRange

	err error
}

// Err is why the manifest was refused, or nil.
func (m Manifest) Err() error { return m.err }

// Provide reports whether the mod provides role.
func (m Manifest) Provide(role string) bool { return slices.Contains(m.Provides, role) }

// Fixture reports whether the id marks a test fixture (a leading _).
func (m Manifest) Fixture() bool { return len(m.ID) > 0 && m.ID[0] == '_' }

// rawManifest is mod.json as encoding/json sees it, after the schema
// check has vouched for every type.
type rawManifest struct {
	ID                   string            `json:"id"`
	Name                 string            `json:"name"`
	Description          string            `json:"description"`
	Version              string            `json:"version"`
	API                  string            `json:"api"`
	Dependencies         map[string]string `json:"dependencies"`
	OptionalDependencies map[string]string `json:"optional_dependencies"`
	LoadOrder            int               `json:"load_order"`
	EnabledByDefault     *bool             `json:"enabled_by_default"`
	Dev                  bool              `json:"dev"`
	GameIDs              []string          `json:"game_ids"`
	Multiplayer          string            `json:"multiplayer"`
	Provides             []string          `json:"provides"`
	Needs                []string          `json:"needs"`
	Writes               []struct {
		Start string `json:"start"`
		End   string `json:"end"`
		Why   string `json:"why"`
	} `json:"writes"`
}

// MaxManifestSize bounds a mod.json.
const MaxManifestSize = 64 << 10

// ParseManifest checks a mod.json against wwapi/schema/mod.schema.json
// and then the rules a schema cannot express: the id is a valid event
// namespace, version and ranges parse as semver, a mod does not depend on
// itself or list one mod as both required and optional, and write ranges
// sit in MEM1 with start < end and do not overlap each other.
func ParseManifest(data []byte) (Manifest, error) {
	if len(data) > MaxManifestSize {
		return Manifest{}, fmt.Errorf("%w: %d bytes, the limit is %d", ErrManifest, len(data), MaxManifestSize)
	}
	if err := schema.Mod().Validate(data); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrManifest, err)
	}
	var r rawManifest
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&r); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrManifest, err)
	}
	bad := func(format string, args ...any) (Manifest, error) {
		return Manifest{}, fmt.Errorf("%w: %s: %s", ErrManifest, r.ID, fmt.Sprintf(format, args...))
	}
	if !events.ValidModID(r.ID) {
		return bad("id is not a valid mod id")
	}
	m := Manifest{
		ID: r.ID, Name: r.Name, Description: r.Description,
		LoadOrder: r.LoadOrder, EnabledByDefault: true, Dev: r.Dev,
		GameIDs: r.GameIDs, Multiplayer: Scope(r.Multiplayer),
		Provides: r.Provides, Needs: r.Needs,
	}
	if r.EnabledByDefault != nil {
		m.EnabledByDefault = *r.EnabledByDefault
	}
	if m.Multiplayer == "" {
		m.Multiplayer = LocalOnly
	}
	var err error
	if m.Version, err = semver.Parse(r.Version); err != nil {
		return bad("version: %v", err)
	}
	if m.API, err = semver.ParseRange(r.API); err != nil {
		return bad("api: %v", err)
	}
	if m.Dependencies, err = parseDeps(r.ID, "dependencies", r.Dependencies); err != nil {
		return bad("%v", err)
	}
	if m.OptionalDependencies, err = parseDeps(r.ID, "optional_dependencies", r.OptionalDependencies); err != nil {
		return bad("%v", err)
	}
	for id := range m.OptionalDependencies {
		if _, both := m.Dependencies[id]; both {
			return bad("%s is in both dependencies and optional_dependencies", id)
		}
	}
	for i, w := range r.Writes {
		start, err1 := strconv.ParseUint(w.Start[2:], 16, 32)
		end, err2 := strconv.ParseUint(w.End[2:], 16, 32)
		if err1 != nil || err2 != nil {
			return bad("writes[%d]: bad address", i)
		}
		ar := AddrRange{Start: uint32(start), End: uint32(end), Why: w.Why}
		if ar.Start >= ar.End || ar.Start < MEM1Start || ar.End > MEM1End {
			return bad("writes[%d] %s: want start < end, inside MEM1 0x%08X-0x%08X", i, ar, MEM1Start, MEM1End)
		}
		m.Writes = append(m.Writes, ar)
	}
	sort.SliceStable(m.Writes, func(i, j int) bool { return m.Writes[i].Start < m.Writes[j].Start })
	for i := 1; i < len(m.Writes); i++ {
		if m.Writes[i-1].Overlaps(m.Writes[i]) {
			return bad("writes %s and %s overlap", m.Writes[i-1], m.Writes[i])
		}
	}
	return m, nil
}

func parseDeps(self, field string, in map[string]string) (map[string]semver.Range, error) {
	out := make(map[string]semver.Range, len(in))
	for id, rs := range in {
		if id == self {
			return nil, fmt.Errorf("%s: a mod cannot depend on itself", field)
		}
		if !events.ValidModID(id) {
			return nil, fmt.Errorf("%s: %q is not a valid mod id", field, id)
		}
		r, err := semver.ParseRange(rs)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %v", field, id, err)
		}
		out[id] = r
	}
	return out, nil
}

// ManifestFrom is ParseManifest for a mod's Manifest method: it never
// fails. A refused mod.json gives a Manifest whose Err is set and whose ID
// is the "id" string from the JSON when there is one; the runtime then
// lists the mod as FAILED instead of crashing the app.
func ManifestFrom(data []byte) Manifest {
	m, err := ParseManifest(data)
	if err == nil {
		return m
	}
	var peek struct {
		ID   any `json:"id"`
		Name any `json:"name"`
	}
	_ = json.Unmarshal(data, &peek)
	m = Manifest{err: err}
	if s, ok := peek.ID.(string); ok && len(s) <= events.MaxModIDLen {
		m.ID = s
	}
	if s, ok := peek.Name.(string); ok && len(s) <= 64 {
		m.Name = s
	}
	return m
}

// sortedKeys returns m's keys in order.
func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
