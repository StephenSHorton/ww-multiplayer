package wwapi

import (
	"fmt"
	"slices"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/semver"
)

// ResolveOptions configure Resolve. The zero value resolves against this
// build's Version with every mod at its manifest default.
type ResolveOptions struct {
	// API is the wwapi version to check "api" ranges against; Version
	// when empty. Tests set it. It must be a valid semver version:
	// Resolve panics otherwise.
	API string
	// Dev turns on mods marked "dev" (they are off otherwise, unless
	// Enabled turns one on).
	Dev bool
	// Enabled overrides a mod's default: settings (mods.<id>.enabled) and
	// CLI flags. set is false to keep the default. Nil keeps every default.
	Enabled func(id string) (on, set bool)
	// Features is what the patched blob provides. Unknown (the zero
	// value) skips the needs check.
	Features Features
}

// Entry is one mod in a Plan.
type Entry struct {
	Mod      Mod
	Manifest Manifest
	Status   Status
	// LoadIndex is the entry's place in Plan.Order, or -1.
	LoadIndex int
}

// Plan is the result of Resolve.
type Plan struct {
	// Entries are the mods in the order they were given.
	Entries []Entry
	// Order lists the indexes into Entries of the mods that load, in load
	// order: every mod after the mods it depends on (optional ones
	// included), and otherwise by load_order, then id.
	Order []int
}

// pending is the internal state of an entry that has not failed yet.
const pending State = ""

// Resolve checks mods and orders the ones that can load. It never fails:
// every problem becomes one mod's Status, and a mod whose dependency did
// not load fails too. In order:
//
//  1. the manifest must parse, and ids must be unique;
//  2. the enable filter (Enabled, then enabled_by_default and dev);
//  3. the api range must contain the wwapi version;
//  4. needs against a known Features (NEEDS_REPATCH);
//  5. a NetProvider must provide "net" in its manifest, and the reverse;
//  6. every dependency must be installed, enabled, in range and loading;
//     an optional one that is loading must be in range;
//  7. no dependency cycles;
//  8. at most one loading mod per "provides" role (all claimants fail);
//  9. declared write sets must not overlap (the later mod in load order
//     fails).
func Resolve(mods []Mod, o ResolveOptions) *Plan {
	r := &resolver{o: o, byID: map[string]int{}}
	api := o.API
	if api == "" {
		api = Version
	}
	apiV, err := semver.Parse(api)
	if err != nil {
		panic("wwapi: ResolveOptions.API: " + err.Error())
	}
	r.p.Entries = make([]Entry, len(mods))
	for i, m := range mods {
		e := &r.p.Entries[i]
		e.Mod, e.Manifest, e.LoadIndex = m, m.Manifest(), -1
		if err := e.Manifest.Err(); err != nil {
			r.fail(i, "%v", err)
			continue
		}
		if j, ok := r.byID[e.Manifest.ID]; ok {
			r.fail(j, "more than one compiled-in mod has the id %s", e.Manifest.ID)
			r.fail(i, "more than one compiled-in mod has the id %s", e.Manifest.ID)
			continue
		}
		r.byID[e.Manifest.ID] = i
	}
	for i := range r.p.Entries {
		e := &r.p.Entries[i]
		if e.Status.State != pending {
			continue
		}
		m := e.Manifest
		if !r.enabled(m) {
			e.Status = Status{State: Disabled, Reason: r.disabledReason(m)}
			continue
		}
		if !m.API.Contains(apiV) {
			r.fail(i, "needs wwapi %s; this build has %s", m.API, apiV)
			continue
		}
		if miss := o.Features.Missing(m.Needs); len(miss) > 0 {
			e.Status = Status{State: NeedsRepatch, Missing: miss,
				Reason: "the patched ISO lacks " + strings.Join(miss, ", ") + "; re-patch your ISO"}
			continue
		}
		_, isNet := e.Mod.(NetProvider)
		switch {
		case isNet && !m.Provide(ProvideNet):
			r.fail(i, "implements NetProvider, but its mod.json does not provide net")
		case !isNet && m.Provide(ProvideNet):
			r.fail(i, "its mod.json provides net, but it does not implement NetProvider")
		}
	}
	r.propagate()
	r.cycles()
	r.propagate()
	r.providers()
	r.propagate()
	for !r.order() {
		r.propagate()
	}
	for _, i := range r.p.Order {
		r.p.Entries[i].Status = Status{State: Loaded}
	}
	return &r.p
}

type resolver struct {
	o    ResolveOptions
	p    Plan
	byID map[string]int // unique ids with a valid manifest
}

func (r *resolver) fail(i int, format string, args ...any) {
	r.p.Entries[i].Status = Status{State: Failed, Reason: fmt.Sprintf(format, args...)}
}

func (r *resolver) live(i int) bool { return r.p.Entries[i].Status.State == pending }

func (r *resolver) enabled(m Manifest) bool {
	if r.o.Enabled != nil {
		if on, set := r.o.Enabled(m.ID); set {
			return on
		}
	}
	return m.EnabledByDefault && (!m.Dev || r.o.Dev)
}

func (r *resolver) disabledReason(m Manifest) string {
	if r.o.Enabled != nil {
		if _, set := r.o.Enabled(m.ID); set {
			return "turned off in settings or by a flag"
		}
	}
	if !m.EnabledByDefault {
		return "off by default"
	}
	return "a dev mod (shown with --dev)"
}

// deps returns i's dependencies that are loading (required ones, and
// optional ones that are present and loading), sorted.
func (r *resolver) deps(i int) []int {
	m := r.p.Entries[i].Manifest
	var out []int
	for _, id := range sortedKeys(m.Dependencies) {
		if j, ok := r.byID[id]; ok && r.live(j) {
			out = append(out, j)
		}
	}
	for _, id := range sortedKeys(m.OptionalDependencies) {
		if j, ok := r.byID[id]; ok && r.live(j) {
			out = append(out, j)
		}
	}
	return out
}

// propagate fails every live mod whose dependencies cannot be met, until
// nothing changes.
func (r *resolver) propagate() {
	for changed := true; changed; {
		changed = false
		for i := range r.p.Entries {
			if !r.live(i) {
				continue
			}
			if why := r.depProblem(i); why != "" {
				r.fail(i, "%s", why)
				changed = true
			}
		}
	}
}

func (r *resolver) depProblem(i int) string {
	m := r.p.Entries[i].Manifest
	for _, id := range sortedKeys(m.Dependencies) {
		rng := m.Dependencies[id]
		j, ok := r.byID[id]
		if !ok {
			return fmt.Sprintf("requires %s %s, which is not installed", id, rng)
		}
		dep := r.p.Entries[j]
		if !rng.Contains(dep.Manifest.Version) {
			return fmt.Sprintf("requires %s %s, but %s %s is installed", id, rng, id, dep.Manifest.Version)
		}
		switch dep.Status.State {
		case pending:
		case Disabled:
			return fmt.Sprintf("requires %s, which is disabled", id)
		default:
			return fmt.Sprintf("requires %s, which did not load", id)
		}
	}
	for _, id := range sortedKeys(m.OptionalDependencies) {
		rng := m.OptionalDependencies[id]
		j, ok := r.byID[id]
		if !ok || !r.live(j) {
			continue
		}
		if v := r.p.Entries[j].Manifest.Version; !rng.Contains(v) {
			return fmt.Sprintf("works with %s %s, but %s %s is installed", id, rng, id, v)
		}
	}
	return ""
}

// cycles fails every live mod in a dependency cycle (Tarjan's strongly
// connected components over the live dependency graph).
func (r *resolver) cycles() {
	n := len(r.p.Entries)
	index := make([]int, n)
	low := make([]int, n)
	on := make([]bool, n)
	for i := range index {
		index[i] = -1
	}
	var stack []int
	next := 0
	var comps [][]int
	var visit func(v int)
	visit = func(v int) {
		index[v], low[v] = next, next
		next++
		stack = append(stack, v)
		on[v] = true
		for _, w := range r.deps(v) {
			if index[w] < 0 {
				visit(w)
				low[v] = min(low[v], low[w])
			} else if on[w] {
				low[v] = min(low[v], index[w])
			}
		}
		if low[v] == index[v] {
			var comp []int
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				on[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			if len(comp) > 1 {
				comps = append(comps, comp)
			}
		}
	}
	for _, i := range r.idOrder() {
		if r.live(i) && index[i] < 0 {
			visit(i)
		}
	}
	for _, comp := range comps {
		path := r.cyclePath(comp)
		for _, i := range comp {
			r.fail(i, "dependency cycle: %s", path)
		}
	}
}

// cyclePath walks a strongly connected component from its smallest id,
// always to the smallest-id dependency inside it, until it comes back
// round: "a -> b -> a".
func (r *resolver) cyclePath(comp []int) string {
	in := map[int]bool{}
	for _, i := range comp {
		in[i] = true
	}
	start := slices.MinFunc(comp, func(a, b int) int {
		return strings.Compare(r.p.Entries[a].Manifest.ID, r.p.Entries[b].Manifest.ID)
	})
	seen := map[int]int{}
	var path []int
	for v := start; ; {
		if at, ok := seen[v]; ok {
			path = append(path[at:], v)
			break
		}
		seen[v] = len(path)
		path = append(path, v)
		var nexts []int
		for _, w := range r.deps(v) {
			if in[w] {
				nexts = append(nexts, w)
			}
		}
		v = slices.MinFunc(nexts, func(a, b int) int {
			return strings.Compare(r.p.Entries[a].Manifest.ID, r.p.Entries[b].Manifest.ID)
		})
	}
	ids := make([]string, len(path))
	for k, i := range path {
		ids[k] = r.p.Entries[i].Manifest.ID
	}
	return strings.Join(ids, " -> ")
}

// providers fails every claimant of a role more than one live mod
// provides.
func (r *resolver) providers() {
	claim := map[string][]int{}
	for _, i := range r.idOrder() {
		if !r.live(i) {
			continue
		}
		for _, role := range r.p.Entries[i].Manifest.Provides {
			claim[role] = append(claim[role], i)
		}
	}
	for _, role := range sortedKeys(claim) {
		is := claim[role]
		if len(is) < 2 {
			continue
		}
		ids := make([]string, len(is))
		for k, i := range is {
			ids[k] = r.p.Entries[i].Manifest.ID
		}
		for _, i := range is {
			r.fail(i, "%s is provided by more than one mod (%s); turn all but one off", role, strings.Join(ids, ", "))
		}
	}
}

// idOrder returns the indexes of entries with a valid unique id, by id.
func (r *resolver) idOrder() []int {
	out := make([]int, 0, len(r.byID))
	for _, id := range sortedKeys(r.byID) {
		out = append(out, r.byID[id])
	}
	return out
}

// order computes the load order of the live mods and checks their write
// sets in it. It reports false when a write overlap failed a mod; the
// caller then propagates and orders again.
func (r *resolver) order() bool {
	live := []int{}
	for _, i := range r.idOrder() {
		if r.live(i) {
			live = append(live, i)
		}
	}
	placed := map[int]bool{}
	var order []int
	for len(order) < len(live) {
		best := -1
		for _, i := range live {
			if placed[i] {
				continue
			}
			ready := true
			for _, d := range r.deps(i) {
				if !placed[d] {
					ready = false
					break
				}
			}
			if ready && (best < 0 || r.before(i, best)) {
				best = i
			}
		}
		if best < 0 {
			panic("wwapi: resolve: a cycle survived cycle detection")
		}
		placed[best] = true
		order = append(order, best)
	}
	var claimed []struct {
		id string
		w  AddrRange
	}
	ok := true
	for _, i := range order {
		m := r.p.Entries[i].Manifest
		var clash string
		for _, w := range m.Writes {
			for _, c := range claimed {
				if w.Overlaps(c.w) && clash == "" {
					clash = fmt.Sprintf("its write range %s overlaps %s's %s", w, c.id, c.w)
				}
			}
		}
		if clash != "" {
			r.fail(i, "%s", clash)
			ok = false
			continue
		}
		for _, w := range m.Writes {
			claimed = append(claimed, struct {
				id string
				w  AddrRange
			}{m.ID, w})
		}
	}
	if !ok {
		return false
	}
	r.p.Order = order
	for k, i := range order {
		r.p.Entries[i].LoadIndex = k
	}
	return true
}

// before orders two ready mods: load_order, then id.
func (r *resolver) before(a, b int) bool {
	ma, mb := r.p.Entries[a].Manifest, r.p.Entries[b].Manifest
	if ma.LoadOrder != mb.LoadOrder {
		return ma.LoadOrder < mb.LoadOrder
	}
	return ma.ID < mb.ID
}

// Infos returns the plan's mods for display: the loading ones in load
// order, then the rest by id (invalid manifests last, in input order).
func (p *Plan) Infos() []ModInfo {
	return infos(p.Entries, p.Order, func(i int) Status { return p.Entries[i].Status })
}

func infos(entries []Entry, order []int, status func(int) Status) []ModInfo {
	out := make([]ModInfo, 0, len(entries))
	inOrder := map[int]bool{}
	for _, i := range order {
		inOrder[i] = true
		out = append(out, info(entries[i], status(i)))
	}
	var rest []int
	for i := range entries {
		if !inOrder[i] {
			rest = append(rest, i)
		}
	}
	slices.SortStableFunc(rest, func(a, b int) int {
		ia, ib := entries[a].Manifest.ID, entries[b].Manifest.ID
		switch {
		case ia == "" && ib == "":
			return 0
		case ia == "":
			return 1
		case ib == "":
			return -1
		}
		return strings.Compare(ia, ib)
	})
	for _, i := range rest {
		out = append(out, info(entries[i], status(i)))
	}
	return out
}

func info(e Entry, s Status) ModInfo {
	m := e.Manifest
	mi := ModInfo{
		ID: m.ID, Name: m.Name, Status: s.State, Reason: s.Reason,
		Missing: s.Missing, Multiplayer: m.Multiplayer, Dev: m.Dev,
		Provides: m.Provides, LoadIndex: e.LoadIndex,
	}
	if m.Err() == nil {
		mi.Version = m.Version.String()
	}
	if len(m.Dependencies) > 0 {
		mi.Dependencies = map[string]string{}
		for id, rng := range m.Dependencies {
			mi.Dependencies[id] = rng.String()
		}
	}
	return mi
}
