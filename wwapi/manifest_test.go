package wwapi_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/semver"
)

func TestParseManifestFull(t *testing.T) {
	m, err := wwapi.ParseManifest([]byte(`{
		"id": "multiplayer", "name": "Multiplayer", "description": "d",
		"version": "1.0.0", "api": "^0.1",
		"dependencies": {"base": "^1.0"}, "optional_dependencies": {"extra": "~2.1"},
		"load_order": -5, "enabled_by_default": false, "dev": true,
		"game_ids": ["GZLE01"], "multiplayer": "client_required",
		"provides": ["net"], "needs": ["avatar", "face"],
		"writes": [{"start": "0x80410010", "end": "0x80410020"}, {"start": "0x80410000", "end": "0x80410010", "why": "a"}],
		"c": null
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "multiplayer" || m.Name != "Multiplayer" || m.Description != "d" || m.Version.String() != "1.0.0" {
		t.Errorf("basics: %+v", m)
	}
	if m.API.String() != "^0.1" || !m.API.Contains(semver.MustParse(wwapi.Version)) {
		t.Errorf("api %s", m.API)
	}
	if m.Dependencies["base"].String() != "^1.0" || m.OptionalDependencies["extra"].String() != "~2.1" {
		t.Errorf("deps %v %v", m.Dependencies, m.OptionalDependencies)
	}
	if m.LoadOrder != -5 || m.EnabledByDefault || !m.Dev || m.Multiplayer != wwapi.ClientRequired {
		t.Errorf("flags %+v", m)
	}
	if !m.Provide(wwapi.ProvideNet) || len(m.Needs) != 2 || m.GameIDs[0] != "GZLE01" {
		t.Errorf("provides/needs %+v", m)
	}
	if len(m.Writes) != 2 || m.Writes[0].Start != 0x80410000 || m.Writes[0].Why != "a" || m.Writes[1].End != 0x80410020 {
		t.Errorf("writes not sorted by start: %v", m.Writes)
	}
	if m.Fixture() || m.Err() != nil {
		t.Errorf("Fixture %v Err %v", m.Fixture(), m.Err())
	}
}

func TestParseManifestDefaults(t *testing.T) {
	m := manifest(t, "_x", "0.0.1", "")
	if !m.EnabledByDefault || m.Dev || m.Multiplayer != wwapi.LocalOnly || m.LoadOrder != 0 {
		t.Errorf("defaults: %+v", m)
	}
	if !m.Fixture() || len(m.Dependencies) != 0 || m.Writes != nil {
		t.Errorf("defaults: %+v", m)
	}
}

func TestParseManifestRefuses(t *testing.T) {
	for _, c := range []struct{ doc, want string }{
		{`{"id":"x","name":"X","version":"1.0.0"}`, `missing required property "api"`},
		{`{"id":"core","name":"X","version":"1.0.0","api":"^0.1"}`, `"core" is not allowed`},
		{manifestJSON("x", "1.0.0-01", ""), `version: semver: bad version`},
		{`{"id":"x","name":"X","version":"1.0.0","api":"^0.x.1"}`, `api: semver: bad range`},
		{manifestJSON("x", "1.0.0", `"dependencies":{"x":"^1"}`), `cannot depend on itself`},
		{manifestJSON("x", "1.0.0", `"optional_dependencies":{"x":"^1"}`), `cannot depend on itself`},
		{manifestJSON("x", "1.0.0", `"dependencies":{"y":"1 - 2"}`), `dependencies: y: semver: bad range`},
		{manifestJSON("x", "1.0.0", `"dependencies":{"y":"^1"},"optional_dependencies":{"y":"^1"}`), `y is in both`},
		{manifestJSON("x", "1.0.0", `"writes":[{"start":"0x80000010","end":"0x80000010"}]`), `want start < end`},
		{manifestJSON("x", "1.0.0", `"writes":[{"start":"0x7FFFFFF0","end":"0x80000010"}]`), `inside MEM1`},
		{manifestJSON("x", "1.0.0", `"writes":[{"start":"0x82FFFFF0","end":"0x83000010"}]`), `inside MEM1`},
		{manifestJSON("x", "1.0.0", `"writes":[{"start":"0x80000000","end":"0x80000010"},{"start":"0x8000000F","end":"0x80000020"}]`), `overlap`},
		{`{"id":"x","name":"X","version":"1.0.0","api":"^0.1","id":"y"}`, `duplicate key`},
		{`{"id":"x"` + strings.Repeat(" ", wwapi.MaxManifestSize) + `}`, `the limit is`},
	} {
		_, err := wwapi.ParseManifest([]byte(c.doc))
		if !errors.Is(err, wwapi.ErrManifest) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%.80s:\n got %v\nwant ErrManifest containing %q", c.doc, err, c.want)
		}
	}
	// A write range may end exactly at the end of MEM1, and ranges may touch.
	manifest(t, "x", "1.0.0", `"writes":[{"start":"0x82FFFFF0","end":"0x83000000"},{"start":"0x80000000","end":"0x80000010"},{"start":"0x80000010","end":"0x80000020"}]`)
}

func TestManifestFrom(t *testing.T) {
	m := wwapi.ManifestFrom([]byte(`{"id":"_bad","name":"Bad","version":"1.0","api":"^0.1"}`))
	if m.Err() == nil || m.ID != "_bad" || m.Name != "Bad" {
		t.Fatalf("got %+v", m)
	}
	for _, doc := range []string{`{"id":5}`, `not json`, `{"id":"` + strings.Repeat("a", 100) + `"}`} {
		if m := wwapi.ManifestFrom([]byte(doc)); m.Err() == nil || m.ID != "" {
			t.Errorf("%.40s: %+v", doc, m)
		}
	}
	if m := wwapi.ManifestFrom([]byte(manifestJSON("ok", "1.0.0", ""))); m.Err() != nil || m.ID != "ok" {
		t.Fatalf("got %+v", m)
	}
}

func TestStatusString(t *testing.T) {
	for _, c := range []struct {
		s    wwapi.Status
		want string
	}{
		{wwapi.Status{State: wwapi.Loaded}, "LOADED"},
		{wwapi.Status{State: wwapi.Disabled, Reason: "off by default"}, "DISABLED"},
		{wwapi.Status{State: wwapi.Failed, Reason: "boom"}, "FAILED(boom)"},
		{wwapi.Status{State: wwapi.Quarantined, Reason: "panic: x"}, "QUARANTINED(panic: x)"},
		{wwapi.Status{State: wwapi.NeedsRepatch, Missing: []string{"face", "tunic"}}, "NEEDS_REPATCH(face, tunic)"},
	} {
		if got := c.s.String(); got != c.want {
			t.Errorf("%+v = %q, want %q", c.s, got, c.want)
		}
	}
}

func TestFeatures(t *testing.T) {
	var unknown wwapi.Features
	if unknown.Known() || unknown.Missing([]string{"a"}) != nil || unknown.String() != "unknown" {
		t.Error("the zero Features must be unknown and miss nothing")
	}
	f := wwapi.KnownFeatures("tunic", "face", "face")
	if !f.Known() || !f.Has("face") || f.Has("avatar") || f.String() != "face,tunic" {
		t.Errorf("got %v", f)
	}
	if got := f.Missing([]string{"tunic", "input", "avatar", "input"}); strings.Join(got, ",") != "avatar,input" {
		t.Errorf("Missing = %v", got)
	}
	if got := wwapi.KnownFeatures().Missing(nil); got != nil {
		t.Errorf("Missing(nil) = %v", got)
	}
}

// FuzzParseManifest: ParseManifest and ManifestFrom never panic, and an
// accepted manifest has a valid id, a version that round-trips, and an
// api range that keeps its text.
func FuzzParseManifest(f *testing.F) {
	f.Add([]byte(manifestJSON("x", "1.0.0", `"dependencies":{"y":"^1 || ~2.1"},"writes":[{"start":"0x80000000","end":"0x80000004"}]`)))
	f.Add([]byte(`{"id":"_bad","name":"Bad","version":"1.0","api":"^0.1"}`))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		m, err := wwapi.ParseManifest(b)
		mf := wwapi.ManifestFrom(b)
		if (err == nil) != (mf.Err() == nil) {
			t.Fatalf("ParseManifest err %v, ManifestFrom err %v", err, mf.Err())
		}
		if err != nil {
			return
		}
		if m.ID == "" || m.ID == "core" || m.Name == "" {
			t.Fatalf("accepted %+v", m)
		}
		if v, err := semver.Parse(m.Version.String()); err != nil || v.Compare(m.Version) != 0 {
			t.Fatalf("version %s does not round-trip: %v", m.Version, err)
		}
		if _, err := semver.ParseRange(m.API.String()); err != nil {
			t.Fatalf("api %q: %v", m.API, err)
		}
	})
}
