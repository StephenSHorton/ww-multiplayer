package schema

import (
	"errors"
	"strings"
	"testing"
)

const goodManifest = `{
  "id": "multiplayer",
  "name": "Multiplayer",
  "version": "1.0.0",
  "api": "^0.1",
  "dependencies": {},
  "optional_dependencies": {"_fixture": ">=1.0.0 <2.0.0 || 3.x"},
  "load_order": 0,
  "enabled_by_default": true,
  "dev": false,
  "game_ids": ["GZLE01"],
  "multiplayer": "client_required",
  "provides": ["net"],
  "needs": ["avatar", "face", "tunic", "input", "coop-ops"],
  "writes": [{"start": "0x803C4C08", "end": "0x803C4C10", "why": "x"}],
  "c": null
}`

func TestModSchemaCompiles(t *testing.T) {
	if _, err := Compile(ModJSON()); err != nil {
		t.Fatal(err)
	}
	if Mod() == nil {
		t.Fatal("Mod() is nil")
	}
}

func TestModSchemaAccepts(t *testing.T) {
	for _, doc := range []string{
		goodManifest,
		`{"id":"x","name":"X","version":"0.1.0-rc.1+b","api":"*"}`,
		`{"$schema":"../../wwapi/schema/mod.schema.json","id":"_fix_1","name":"F","version":"1.2.3","api":"^0.1"}`,
	} {
		if err := Mod().Validate([]byte(doc)); err != nil {
			t.Errorf("rejected %s: %v", doc, err)
		}
	}
}

func TestModSchemaRejects(t *testing.T) {
	base := `"name":"X","version":"1.0.0","api":"^0.1"`
	for _, c := range []struct{ doc, want string }{
		{`{` + base + `}`, `missing required property "id"`},
		{`{"id":"x","version":"1.0.0","api":"^0.1"}`, `missing required property "name"`},
		{`{"id":"core",` + base + `}`, `/id: "core" is not allowed`},
		{`{"id":"Bad",` + base + `}`, `/id: "Bad" does not match`},
		{`{"id":"__x",` + base + `}`, `/id:`},
		{`{"id":"x",` + base + `,"extra":1}`, `/extra: unknown property`},
		{`{"id":"x","name":"","version":"1.0.0","api":"^0.1"}`, `/name: is 0 characters`},
		{`{"id":"x","name":"X","version":"1.0","api":"^0.1"}`, `/version: "1.0" does not match`},
		{`{"id":"x","name":"X","version":"01.0.0","api":"^0.1"}`, `/version:`},
		{`{"id":"x","name":"X","version":1,"api":"^0.1"}`, `/version: is number, want string`},
		{`{"id":"x","name":"X","version":"1.0.0","api":""}`, `/api: is 0 characters`},
		{`{"id":"x","name":"X","version":"1.0.0","api":"^0.1;rm"}`, `/api:`},
		{`{"id":"x",` + base + `,"dependencies":{"Bad":"^1"}}`, `/dependencies/Bad:`},
		{`{"id":"x",` + base + `,"dependencies":{"y":1}}`, `/dependencies/y: is number`},
		{`{"id":"x",` + base + `,"dependencies":[]}`, `/dependencies: is array, want object`},
		{`{"id":"x",` + base + `,"load_order":1.5}`, `/load_order: is number, want integer`},
		{`{"id":"x",` + base + `,"load_order":1001}`, `/load_order: is 1001, want at most 1000`},
		{`{"id":"x",` + base + `,"enabled_by_default":"yes"}`, `/enabled_by_default: is string`},
		{`{"id":"x",` + base + `,"game_ids":["GZLE01","GZLE01"]}`, `/game_ids/1: repeats item 0`},
		{`{"id":"x",` + base + `,"game_ids":["gzle01"]}`, `/game_ids/0:`},
		{`{"id":"x",` + base + `,"multiplayer":"always"}`, `/multiplayer: is "always", want one of`},
		{`{"id":"x",` + base + `,"provides":["audio"]}`, `/provides/0:`},
		{`{"id":"x",` + base + `,"needs":["Face"]}`, `/needs/0:`},
		{`{"id":"x",` + base + `,"writes":[{"start":"0x80000000"}]}`, `/writes/0: missing required property "end"`},
		{`{"id":"x",` + base + `,"writes":[{"start":"80000000","end":"0x80000004"}]}`, `/writes/0/start:`},
		{`{"id":"x",` + base + `,"c":{}}`, `/c: is object, want null`},
		{`[]`, `/: is array, want object`},
	} {
		err := Mod().Validate([]byte(c.doc))
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("%s: err = %v, want a ValidationError", c.doc, err)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s:\n got %v\nwant it to contain %q", c.doc, err, c.want)
		}
	}
}

func TestDecodeErrors(t *testing.T) {
	for _, c := range []struct{ doc, want string }{
		{`{"id":"x","id":"y"}`, `duplicate key "id"`},
		{`{"a":{"b":1,"b":2}}`, `duplicate key "b"`},
		{`{} {}`, `after the JSON document`},
		{``, `unexpected end`},
		{`{"a":`, `unexpected end`},
		{`{"a" 1}`, ``},
		{strings.Repeat("[", 70) + strings.Repeat("]", 70), `nested deeper`},
	} {
		err := Mod().Validate([]byte(c.doc))
		if err == nil {
			t.Errorf("%q: accepted", c.doc)
			continue
		}
		var ve *ValidationError
		if errors.As(err, &ve) {
			t.Errorf("%q: %v is a schema mismatch, want a parse error", c.doc, err)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: %v, want %q", c.doc, err, c.want)
		}
	}
}

func TestCompileRefusesUnsupported(t *testing.T) {
	for _, c := range []struct{ schema, want string }{
		{`{"type":"object","oneOf":[]}`, `oneOf: keyword not supported`},
		{`{"properties":{"a":{"format":"email"}}}`, `/properties/a/format: keyword not supported`},
		{`{"$ref":"other.json"}`, `only #/$defs/<name> refs`},
		{`{"$ref":"#/$defs/missing"}`, `only #/$defs/<name> refs to an existing def`},
		{`{"properties":{"a":{"$defs":{}}}}`, `only at the root`},
		{`{"type":"float"}`, `unknown type`},
		{`{"pattern":"("}`, `/pattern:`},
		{`{"maxLength":-1}`, `non-negative integer`},
		{`{"enum":[]}`, `non-empty array`},
		{`{"minimum":"1"}`, `must be a number`},
		{`3`, `object or a boolean`},
		{`{"a":1,"a":2}`, `duplicate key`},
	} {
		_, err := Compile([]byte(c.schema))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Compile(%s) = %v, want %q", c.schema, err, c.want)
		}
	}
}

func TestValidatorKeywords(t *testing.T) {
	s, err := Compile([]byte(`{
		"$defs": {"loop": {"$ref": "#/$defs/loop"}, "small": {"type": "integer", "minimum": -2, "maximum": 2}},
		"type": "object",
		"properties": {
			"never": false,
			"any": true,
			"n": {"$ref": "#/$defs/small"},
			"num": {"type": ["number", "null"], "minimum": 0.5},
			"k": {"const": {"a": [1, "x"]}},
			"loop": {"$ref": "#/$defs/loop"},
			"u": {"type": "array", "uniqueItems": true, "minItems": 1, "maxItems": 2},
			"o": {"type": "object", "minProperties": 1, "maxProperties": 1},
			"s": {"type": "string", "maxLength": 2}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		doc  string
		want string // "" = valid
	}{
		{`{"any":[1,{"x":null}]}`, ``},
		{`{"never":1}`, `/never: no value is allowed`},
		{`{"n":2}`, ``},
		{`{"n":3}`, `/n: is 3, want at most 2`},
		{`{"n":-3}`, `/n: is -3, want at least -2`},
		{`{"num":null}`, ``},
		{`{"num":0.25}`, `want at least 1/2`},
		{`{"num":"1"}`, `want number or null`},
		{`{"k":{"a":[1.0,"x"]}}`, ``},
		{`{"k":{"a":[1,"y"]}}`, `/k:`},
		{`{"loop":1}`, `nests deeper than`},
		{`{"u":[]}`, `has 0 items, want at least 1`},
		{`{"u":[1,2,3]}`, `has 3 items, want at most 2`},
		{`{"u":[{"a":1,"b":2},{"b":2,"a":1}]}`, `/u/1: repeats item 0`},
		{`{"o":{}}`, `has 0 properties, want at least 1`},
		{`{"o":{"a":1,"b":2}}`, `has 2 properties, want at most 1`},
		{`{"s":"éé"}`, ``},
		{`{"s":"ééé"}`, `is 3 characters, want at most 2`},
	} {
		err := s.Validate([]byte(c.doc))
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.doc, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: %v, want %q", c.doc, err, c.want)
		}
	}
}

// TestProblemsBounded: a document with many faults reports at most
// maxProblems of them.
func TestProblemsBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"id":"x","name":"X","version":"1.0.0","api":"^0.1","needs":[`)
	for i := 0; i < 60; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`"BAD"`)
	}
	b.WriteString(`]}`)
	var ve *ValidationError
	if err := Mod().Validate([]byte(b.String())); !errors.As(err, &ve) || len(ve.Problems) != maxProblems {
		t.Fatalf("got %v", err)
	}
}

func TestPointerEscapes(t *testing.T) {
	if got := pointer("/a", "b/c~d"); got != "/a/b~1c~0d" {
		t.Fatalf("pointer = %q", got)
	}
}

// FuzzValidate: validating any bytes against the mod schema never panics,
// and a document that passes still passes (validation is deterministic).
func FuzzValidate(f *testing.F) {
	f.Add([]byte(goodManifest))
	f.Add([]byte(`{"id":"x","name":"X","version":"1.0.0","api":"^0.1"}`))
	f.Add([]byte(`{"id":"x","id":"x"}`))
	f.Add([]byte(`[[[[1]]]]`))
	f.Fuzz(func(t *testing.T, b []byte) {
		if Mod().Validate(b) == nil && Mod().Validate(b) != nil {
			t.Fatal("not deterministic")
		}
	})
}
