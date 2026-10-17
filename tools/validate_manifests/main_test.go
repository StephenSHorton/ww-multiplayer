package main

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

// TestRepoManifests: the repo's own mods/ tree passes (the co-op, example and
// multiplayer mods included), and every fixture with an invalid.txt is
// refused for the expected reason.
func TestRepoManifests(t *testing.T) {
	var out strings.Builder
	if err := run(os.DirFS("../../mods"), "mods", &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	for _, want := range []string{
		"ok   mods/_fixtures/_ok_base/mod.json\n",
		"ok   mods/_fixtures/_bad_range/mod.json (refused, as invalid.txt expects)\n",
		"ok   mods/_fixtures/_bad_manifest/mod.json (refused, as invalid.txt expects)\n",
		"ok   mods/coop/mod.json\n",
		"ok   mods/example/mod.json\n",
		"ok   mods/multiplayer/mod.json\n",
		" 2 refused on purpose, 0 problems\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func file(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

const okJSON = `{"id":"good","name":"Good","version":"1.0.0","api":"^0.1"}`

func TestProblems(t *testing.T) {
	for _, c := range []struct {
		name string
		fs   fstest.MapFS
		want string
	}{
		{"empty tree", fstest.MapFS{"x.txt": file("")}, "no mod.json found"},
		{"bad json", fstest.MapFS{"bad/mod.json": file(`{`)}, "FAIL m/bad/mod.json: wwapi: bad mod.json"},
		{"folder name", fstest.MapFS{"other/mod.json": file(okJSON)}, `id "good" does not equal its folder name "other"`},
		{"root manifest", fstest.MapFS{"mod.json": file(okJSON)}, "must sit in a folder"},
		{"fixture outside", fstest.MapFS{"_x/mod.json": file(`{"id":"_x","name":"X","version":"1.0.0","api":"^0.1"}`)}, "belong under _fixtures"},
		{"plain id in fixtures", fstest.MapFS{"_fixtures/good/mod.json": file(okJSON)}, "belong under _fixtures"},
		{"unexpected success", fstest.MapFS{
			"_fixtures/_y/mod.json":    file(`{"id":"_y","name":"Y","version":"1.0.0","api":"^0.1"}`),
			"_fixtures/_y/invalid.txt": file("anything"),
		}, "was accepted, but invalid.txt expects"},
		{"wrong error", fstest.MapFS{
			"_fixtures/_y/mod.json":    file(`{"id":"_y","name":"Y","version":"1.0","api":"^0.1"}`),
			"_fixtures/_y/invalid.txt": file("cycle"),
		}, `does not contain "cycle"`},
		{"empty invalid.txt", fstest.MapFS{
			"_fixtures/_y/mod.json":    file(`{`),
			"_fixtures/_y/invalid.txt": file(" \n"),
		}, "invalid.txt is empty"},
		{"invalid.txt outside fixtures", fstest.MapFS{
			"good/mod.json":    file(`{`),
			"good/invalid.txt": file("x"),
		}, "only for fixtures"},
		{"refused fixture with the wrong id", fstest.MapFS{
			"_fixtures/_y/mod.json":    file(`{"id":"_z","name":"Y","version":"1.0","api":"^0.1"}`),
			"_fixtures/_y/invalid.txt": file("does not match the schema"),
		}, `id "_z" does not equal its folder name "_y"`},
	} {
		var out strings.Builder
		err := run(c.fs, "m", &out)
		if err == nil {
			t.Errorf("%s: accepted:\n%s", c.name, out.String())
			continue
		}
		if !strings.Contains(out.String()+err.Error(), c.want) {
			t.Errorf("%s: want %q in\n%s%v", c.name, c.want, out.String(), err)
		}
	}
	var out strings.Builder
	if err := run(fstest.MapFS{"good/mod.json": file(okJSON)}, "m", &out); err != nil || out.String() != "ok   m/good/mod.json\n1 manifests, 0 refused on purpose, 0 problems\n" {
		t.Errorf("good tree: %v\n%s", err, out.String())
	}
}
