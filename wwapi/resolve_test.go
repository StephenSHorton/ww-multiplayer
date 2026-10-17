package wwapi_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	fixtures "github.com/StephenSHorton/ww-multiplayer/mods/_fixtures"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
)

func TestResolveEmpty(t *testing.T) {
	p := wwapi.Resolve(nil, wwapi.ResolveOptions{})
	if len(p.Entries) != 0 || len(p.Order) != 0 {
		t.Fatalf("%+v", p)
	}
	b, err := json.Marshal(p.Infos())
	if err != nil || string(b) != "[]" {
		t.Fatalf("Infos JSON = %s, %v; want []", b, err)
	}
}

// TestResolveFixtures: every broken fixture fails with its reason, and
// the rest load in dependency order.
func TestResolveFixtures(t *testing.T) {
	p := wwapi.Resolve(fixtures.All(nil), wwapi.ResolveOptions{})
	infos := p.Infos()
	got := statuses(infos)
	want := map[string]string{
		"_api_mismatch":  "FAILED(needs wwapi ^9.0; this build has 0.1.0)",
		"_bad_range":     "",
		"_cycle_a":       "FAILED(dependency cycle: _cycle_a -> _cycle_b -> _cycle_a)",
		"_cycle_b":       "FAILED(dependency cycle: _cycle_a -> _cycle_b -> _cycle_a)",
		"_missing_dep":   "FAILED(requires _not_installed ^1.0, which is not installed)",
		"_bad_manifest":  "",
		"_handler_panic": "LOADED", "_init_error": "LOADED", "_ok_base": "LOADED",
		"_ok_dependent": "LOADED", "_run_panic": "LOADED",
	}
	for id, w := range want {
		g := got[id]
		switch id {
		case "_bad_manifest":
			if !strings.HasPrefix(g, "FAILED(wwapi: bad mod.json: does not match the schema: /version:") || !strings.Contains(g, "/colour: unknown property") {
				t.Errorf("%s = %s", id, g)
			}
		case "_bad_range":
			if !strings.HasPrefix(g, "FAILED(wwapi: bad mod.json: _bad_range: dependencies: _ok_base: semver: bad range") {
				t.Errorf("%s = %s", id, g)
			}
		default:
			if g != w {
				t.Errorf("%s = %s, want %s", id, g, w)
			}
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d mods: %v", len(got), got)
	}
	wantOrder := []string{"_handler_panic", "_init_error", "_ok_base", "_ok_dependent", "_run_panic",
		"_api_mismatch", "_bad_manifest", "_bad_range", "_cycle_a", "_cycle_b", "_missing_dep"}
	if g := ids(infos); !reflect.DeepEqual(g, wantOrder) {
		t.Errorf("Infos order = %v\nwant %v", g, wantOrder)
	}
	for k, mi := range infos {
		if wl := map[bool]int{true: k, false: -1}[k < 5]; mi.LoadIndex != wl {
			t.Errorf("%s LoadIndex = %d, want %d", mi.ID, mi.LoadIndex, wl)
		}
	}
}

func TestResolveDependencies(t *testing.T) {
	base := mk(t, "base", "1.4.0", "")
	off := mk(t, "off", "1.0.0", `"enabled_by_default":false`)
	mods := []wwapi.Mod{
		base, off,
		mk(t, "wants_new", "1.0.0", `"dependencies":{"base":"^2.0"}`),
		mk(t, "wants_off", "1.0.0", `"dependencies":{"off":"*"}`),
		mk(t, "chain_c", "1.0.0", `"dependencies":{"wants_new":"*"}`),
		mk(t, "chain_d", "1.0.0", `"dependencies":{"chain_c":"*"}`),
		mk(t, "opt_ok", "1.0.0", `"optional_dependencies":{"absent":"^1","off":"^9","base":"^1.2"}`),
		mk(t, "opt_bad", "1.0.0", `"optional_dependencies":{"base":"~1.3"}`),
		mk(t, "user", "1.0.0", `"dependencies":{"base":">=1.0.0 <2.0.0"}`),
	}
	p := wwapi.Resolve(mods, wwapi.ResolveOptions{})
	got := statuses(p.Infos())
	for id, want := range map[string]string{
		"base":      "LOADED",
		"off":       "DISABLED",
		"wants_new": "FAILED(requires base ^2.0, but base 1.4.0 is installed)",
		"wants_off": "FAILED(requires off, which is disabled)",
		"chain_c":   "FAILED(requires wants_new, which did not load)",
		"chain_d":   "FAILED(requires chain_c, which did not load)",
		"opt_ok":    "LOADED",
		"opt_bad":   "FAILED(works with base ~1.3, but base 1.4.0 is installed)",
		"user":      "LOADED",
	} {
		if got[id] != want {
			t.Errorf("%s = %s, want %s", id, got[id], want)
		}
	}
	if g := ids(p.Infos())[:3]; !reflect.DeepEqual(g, []string{"base", "opt_ok", "user"}) {
		t.Errorf("load order %v", g)
	}
}

// TestResolveOrder: dependencies (optional ones too) come first; ties go
// by load_order, then id.
func TestResolveOrder(t *testing.T) {
	mods := []wwapi.Mod{
		mk(t, "zeta", "1.0.0", `"load_order":-10,"dependencies":{"omega":"*"}`),
		mk(t, "alpha", "1.0.0", `"load_order":5`),
		mk(t, "omega", "1.0.0", `"load_order":100`),
		mk(t, "beta", "1.0.0", ""),
		mk(t, "gamma", "1.0.0", `"optional_dependencies":{"alpha":"*"}`),
		mk(t, "delta", "1.0.0", ""),
	}
	p := wwapi.Resolve(mods, wwapi.ResolveOptions{})
	want := []string{"beta", "delta", "alpha", "gamma", "omega", "zeta"}
	if g := ids(p.Infos()); !reflect.DeepEqual(g, want) {
		t.Fatalf("order %v, want %v", g, want)
	}
	for k, i := range p.Order {
		if p.Entries[i].LoadIndex != k || p.Entries[i].Manifest.ID != want[k] {
			t.Errorf("Order[%d] = %d (%s, LoadIndex %d)", k, i, p.Entries[i].Manifest.ID, p.Entries[i].LoadIndex)
		}
	}
}

func TestResolveCycles(t *testing.T) {
	mods := []wwapi.Mod{
		mk(t, "c", "1.0.0", `"dependencies":{"a":"*"}`),
		mk(t, "a", "1.0.0", `"dependencies":{"b":"*"}`),
		mk(t, "b", "1.0.0", `"dependencies":{"c":"*"}`),
		mk(t, "tail", "1.0.0", `"dependencies":{"a":"*"}`),
		mk(t, "x", "1.0.0", `"optional_dependencies":{"y":"*"}`),
		mk(t, "y", "1.0.0", `"dependencies":{"x":"*"}`),
		mk(t, "free", "1.0.0", ""),
	}
	got := statuses(wwapi.Resolve(mods, wwapi.ResolveOptions{}).Infos())
	for id, want := range map[string]string{
		"a":    "FAILED(dependency cycle: a -> b -> c -> a)",
		"b":    "FAILED(dependency cycle: a -> b -> c -> a)",
		"c":    "FAILED(dependency cycle: a -> b -> c -> a)",
		"tail": "FAILED(requires a, which did not load)",
		"x":    "FAILED(dependency cycle: x -> y -> x)",
		"y":    "FAILED(dependency cycle: x -> y -> x)",
		"free": "LOADED",
	} {
		if got[id] != want {
			t.Errorf("%s = %s, want %s", id, got[id], want)
		}
	}
}

func TestResolveEnableFilter(t *testing.T) {
	mods := []wwapi.Mod{
		mk(t, "plain", "1.0.0", ""),
		mk(t, "devmod", "1.0.0", `"dev":true`),
		mk(t, "offmod", "1.0.0", `"enabled_by_default":false`),
		mk(t, "forced_off", "1.0.0", ""),
		mk(t, "forced_on", "1.0.0", `"enabled_by_default":false`),
	}
	enabled := func(id string) (bool, bool) {
		switch id {
		case "forced_off":
			return false, true
		case "forced_on":
			return true, true
		}
		return false, false
	}
	p := wwapi.Resolve(mods, wwapi.ResolveOptions{Enabled: enabled})
	reasons := map[string]string{}
	for _, mi := range p.Infos() {
		reasons[mi.ID] = mi.StatusString() + " " + mi.Reason
	}
	for id, want := range map[string]string{
		"plain":      "LOADED ",
		"devmod":     "DISABLED a dev mod (shown with --dev)",
		"offmod":     "DISABLED off by default",
		"forced_off": "DISABLED turned off in settings or by a flag",
		"forced_on":  "LOADED ",
	} {
		if reasons[id] != want {
			t.Errorf("%s = %q, want %q", id, reasons[id], want)
		}
	}
	got := statuses(wwapi.Resolve(mods, wwapi.ResolveOptions{Dev: true}).Infos())
	if got["devmod"] != "LOADED" || got["offmod"] != "DISABLED" {
		t.Errorf("with Dev: %v", got)
	}
}

func TestResolveAPI(t *testing.T) {
	mods := []wwapi.Mod{mk(t, "a", "1.0.0", "")}
	if got := statuses(wwapi.Resolve(mods, wwapi.ResolveOptions{API: "0.2.0"}).Infos()); got["a"] != "FAILED(needs wwapi ^0.1; this build has 0.2.0)" {
		t.Errorf("got %v", got)
	}
	if got := statuses(wwapi.Resolve(mods, wwapi.ResolveOptions{API: "0.1.7"}).Infos()); got["a"] != "LOADED" {
		t.Errorf("got %v", got)
	}
}

func TestResolveDuplicateIDs(t *testing.T) {
	mods := []wwapi.Mod{mk(t, "a", "1.0.0", ""), mk(t, "a", "2.0.0", ""), mk(t, "b", "1.0.0", `"dependencies":{"a":"*"}`)}
	p := wwapi.Resolve(mods, wwapi.ResolveOptions{})
	for _, e := range p.Entries {
		if e.Status.State != wwapi.Failed {
			t.Errorf("%s: %v", e.Manifest.ID, e.Status)
		}
	}
	if r := p.Entries[0].Status.Reason; r != "more than one compiled-in mod has the id a" {
		t.Errorf("reason %q", r)
	}
	if r := p.Entries[2].Status.Reason; r != "requires a, which did not load" {
		t.Errorf("reason %q", r)
	}
}

func TestResolveNetProvider(t *testing.T) {
	one := netMod{mk(t, "mp", "1.0.0", `"provides":["net"]`)}
	two := netMod{mk(t, "mp2", "1.0.0", `"provides":["net"]`)}
	coop := mk(t, "coop", "1.0.0", `"dependencies":{"mp":"^1.0"}`)

	got := statuses(wwapi.Resolve([]wwapi.Mod{one, coop}, wwapi.ResolveOptions{}).Infos())
	if got["mp"] != "LOADED" || got["coop"] != "LOADED" {
		t.Errorf("one provider: %v", got)
	}
	got = statuses(wwapi.Resolve([]wwapi.Mod{two, one, coop}, wwapi.ResolveOptions{}).Infos())
	for id, want := range map[string]string{
		"mp":   "FAILED(net is provided by more than one mod (mp, mp2); turn all but one off)",
		"mp2":  "FAILED(net is provided by more than one mod (mp, mp2); turn all but one off)",
		"coop": "FAILED(requires mp, which did not load)",
	} {
		if got[id] != want {
			t.Errorf("two providers: %s = %s, want %s", id, got[id], want)
		}
	}
	// A second provider that is off does not count.
	off := netMod{mk(t, "mp2", "1.0.0", `"provides":["net"],"enabled_by_default":false`)}
	if got := statuses(wwapi.Resolve([]wwapi.Mod{one, off}, wwapi.ResolveOptions{}).Infos()); got["mp"] != "LOADED" {
		t.Errorf("disabled second provider: %v", got)
	}
	// A broken second provider does not count either.
	broken := netMod{mk(t, "mp3", "1.0.0", `"provides":["net"],"dependencies":{"nope":"*"}`)}
	if got := statuses(wwapi.Resolve([]wwapi.Mod{one, broken}, wwapi.ResolveOptions{}).Infos()); got["mp"] != "LOADED" || !strings.Contains(got["mp3"], "not installed") {
		t.Errorf("broken second provider: %v", got)
	}
	got = statuses(wwapi.Resolve([]wwapi.Mod{
		netMod{mk(t, "quiet", "1.0.0", "")},
		mk(t, "liar", "1.0.0", `"provides":["net"]`),
	}, wwapi.ResolveOptions{}).Infos())
	if got["quiet"] != "FAILED(implements NetProvider, but its mod.json does not provide net)" ||
		got["liar"] != "FAILED(its mod.json provides net, but it does not implement NetProvider)" {
		t.Errorf("mismatch: %v", got)
	}
}

func TestResolveNeeds(t *testing.T) {
	mods := []wwapi.Mod{
		mk(t, "mp", "1.0.0", `"needs":["avatar","face","tunic"]`),
		mk(t, "coop", "1.0.0", `"dependencies":{"mp":"*"}`),
		mk(t, "plain", "1.0.0", `"needs":["face"]`),
	}
	p := wwapi.Resolve(mods, wwapi.ResolveOptions{Features: wwapi.KnownFeatures("face")})
	infos := p.Infos()
	got := statuses(infos)
	if got["mp"] != "NEEDS_REPATCH(avatar, tunic)" || got["coop"] != "FAILED(requires mp, which did not load)" || got["plain"] != "LOADED" {
		t.Errorf("got %v", got)
	}
	for _, mi := range infos {
		if mi.ID == "mp" && mi.Reason != "the patched ISO lacks avatar, tunic; re-patch your ISO" {
			t.Errorf("reason %q", mi.Reason)
		}
	}
	if got := statuses(wwapi.Resolve(mods, wwapi.ResolveOptions{}).Infos()); got["mp"] != "LOADED" {
		t.Errorf("unknown features must not block: %v", got)
	}
}

func TestResolveWrites(t *testing.T) {
	w := func(s, e string) string { return `"writes":[{"start":"` + s + `","end":"` + e + `"}]` }
	mods := []wwapi.Mod{
		mk(t, "first", "1.0.0", `"load_order":-1,`+w("0x80410000", "0x80410100")),
		mk(t, "second", "1.0.0", w("0x804100F0", "0x80410200")),
		mk(t, "needs_second", "1.0.0", `"dependencies":{"second":"*"}`),
		mk(t, "touching", "1.0.0", w("0x80410100", "0x80410110")),
	}
	got := statuses(wwapi.Resolve(mods, wwapi.ResolveOptions{}).Infos())
	for id, want := range map[string]string{
		"first":        "LOADED",
		"second":       "FAILED(its write range 0x804100F0-0x80410200 overlaps first's 0x80410000-0x80410100)",
		"needs_second": "FAILED(requires second, which did not load)",
		"touching":     "LOADED",
	} {
		if got[id] != want {
			t.Errorf("%s = %s, want %s", id, got[id], want)
		}
	}
}

func TestResolveBadManifestAndInfoJSON(t *testing.T) {
	bad := &tmod{m: wwapi.ManifestFrom([]byte(`{"name":"no id"}`))}
	ok := mk(t, "ok", "1.2.3", `"dependencies":{"x":"^1"},"optional_dependencies":{},"provides":[],"multiplayer":"host_only","dev":true`)
	p := wwapi.Resolve([]wwapi.Mod{bad, ok}, wwapi.ResolveOptions{Dev: true})
	b, err := json.Marshal(p.Infos())
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"id":"ok","name":"OK","version":"1.2.3","status":"FAILED","reason":"requires x ^1, which is not installed","multiplayer":"host_only","dev":true,"dependencies":{"x":"^1"},"load_index":-1},` +
		`{"id":"","name":"no id","version":"","status":"FAILED","reason":"wwapi: bad mod.json: does not match the schema: /: missing required property \"id\"; /: missing required property \"version\"; /: missing required property \"api\"","load_index":-1}]`
	if string(b) != want {
		t.Errorf("JSON:\n got %s\nwant %s", b, want)
	}
}
