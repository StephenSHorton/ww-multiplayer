package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/synctables"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/tablestest"
)

func TestParseCoopTablesArgs(t *testing.T) {
	for _, c := range []struct {
		args []string
		want coopTablesArgs
		err  string
	}{
		{args: []string{"ww.iso"}, want: coopTablesArgs{path: "ww.iso"}},
		{args: []string{"--json", "ww.ciso", "--no-cache"}, want: coopTablesArgs{path: "ww.ciso", json: true, noCache: true}},
		{args: []string{"ww.iso", "--section", "switches"}, want: coopTablesArgs{path: "ww.iso", section: tables.SectionSwitches}},
		{args: []string{"--section=enemies", "ww.iso"}, want: coopTablesArgs{path: "ww.iso", section: tables.SectionEnemies}},
		{args: []string{"-"}, want: coopTablesArgs{path: "-"}},
		{args: nil, err: "give the path"},
		{args: []string{"--json"}, err: "give the path"},
		{args: []string{"a.iso", "b.iso"}, err: `one disc image, not "a.iso" and "b.iso"`},
		{args: []string{"ww.iso", "--section"}, err: "--section needs"},
		{args: []string{"ww.iso", "--section", "doors"}, err: `unknown section "doors"`},
		{args: []string{"ww.iso", "--section=keys", "--section=hearts"}, err: "--section given twice"},
		{args: []string{"ww.iso", "--json=yes"}, err: "--json takes no value"},
		{args: []string{"ww.iso", "-json"}, err: "unknown flag -json"},
		{args: []string{"ww.iso", "--yes"}, err: "unknown flag --yes"},
	} {
		got, err := parseCoopTablesArgs(c.args)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: err %v, want %q", c.args, err, c.err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%q = %+v, %v; want %+v", c.args, got, err, c.want)
		}
	}
}

// TestCoopTables runs the subcommand end to end on a synthetic disc image
// file: the first run builds and caches, the second reads the cache, and
// both print the report the tables package builds from the same disc.
func TestCoopTables(t *testing.T) {
	iso := filepath.Join(t.TempDir(), "ww.iso")
	img := tablestest.ISO(tablestest.Vanilla())
	if err := os.WriteFile(iso, img, 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := inject.NewDisc(bytes.NewReader(img), int64(len(img)))
	if err != nil {
		t.Fatal(err)
	}
	built, err := tables.Build(d)
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(t.TempDir(), "coop-tables")
	cacheFile := tables.CacheFile(cache, built.DOL)

	// run runs the command with cache as the default cache directory, as
	// tables.CacheDir gives it in production.
	run := func(cache string, args ...string) (out, status string) {
		t.Helper()
		a, err := parseCoopTablesArgs(append([]string{iso}, args...))
		if err != nil {
			t.Fatal(err)
		}
		var o, s bytes.Buffer
		if err := coopTables(&o, &s, a, func() (string, error) { return cache, nil }); err != nil {
			t.Fatal(err)
		}
		return o.String(), s.String()
	}
	// report splits the command's output into its status lines and the
	// tables' report.
	report := func(out string) (status, rep string) {
		i := strings.Index(out, "\nCo-op tables (")
		if i < 0 {
			t.Fatalf("no report in:\n%s", out)
		}
		return out[:i], out[i+1:]
	}

	out, status := run(cache)
	head, rep := report(out)
	if status != "" || rep != synctables.Report(built) {
		t.Errorf("first run: status %q; report\n%s\nwant\n%s", status, rep, synctables.Report(built))
	}
	// 27 archives: the 12 stages' Stage.arc, their 12 rooms, the skipped
	// Name stage's Stage.arc, and the excluded K_Test2's Stage.arc and
	// room.
	for _, s := range []string{"coop-tables: " + iso + "\n", "built from the disc in ", "(M1 target: under 5.00 s)",
		"27 stage archives", "cache: no cache file yet; saved to " + cacheFile} {
		if !strings.Contains(head, s) {
			t.Errorf("first run's status lacks %q:\n%s", s, head)
		}
	}
	if loaded, err := tables.Load(cache, built.ID); err != nil || synctables.Report(loaded) != rep {
		t.Errorf("the saved cache: %v", err)
	}

	out, _ = run(cache)
	head, rep2 := report(out)
	if rep2 != rep || !strings.Contains(head, "read from the cache "+cacheFile) {
		t.Errorf("second run:\n%s", out)
	}

	out, _ = run(cache, "--section", "keys")
	if _, r := report(out); r != synctables.Report(built, tables.SectionKeys) {
		t.Errorf("--section keys:\n%s", r)
	}

	// --json: the tables alone on out, the status on status.
	out, status = run(cache, "--json")
	var got tables.Tables
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--json output doesn't parse: %v\n%.300s", err, out)
	}
	if synctables.Report(&got) != synctables.Report(built) || !strings.Contains(status, "read from the cache") {
		t.Errorf("--json: status %q", status)
	}
	out, _ = run(cache, "--json", "--section=hearts")
	var only map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &only); err != nil {
		t.Fatal(err)
	}
	if _, ok := only["hearts"]; !ok || only["keys"] != nil || only["switches"] != nil || only["enemies"] != nil {
		t.Errorf("--json --section hearts has %d fields", len(only))
	}

	// --no-cache with the cache in place: built again, the cache neither
	// read nor written (its time, set into the past, stays put).
	old := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(cacheFile, old, old); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(cacheFile)
	if err != nil {
		t.Fatal(err)
	}
	unchanged := func(when string) {
		t.Helper()
		fi, err := os.Stat(cacheFile)
		if b, _ := os.ReadFile(cacheFile); err != nil || !fi.ModTime().Equal(old) || !bytes.Equal(b, saved) {
			t.Errorf("%s: the cache file changed: %v", when, err)
		}
	}
	for _, args := range [][]string{{"--no-cache"}, {"--no-cache", "--json"}} {
		out, status = run(cache, args...)
		head := status
		if !slices.Contains(args, "--json") {
			head, _ = report(out)
		}
		if !strings.Contains(head, "built from the disc") || !strings.Contains(head, "cache: off") || strings.Contains(head, "read from the cache") {
			t.Errorf("%q:\n%s", args, head)
		}
		unchanged(strings.Join(args, " "))
	}
	if out, _ = run(cache); !strings.Contains(out, "read from the cache "+cacheFile) {
		t.Errorf("after --no-cache, a plain run:\n%s", out)
	}
	unchanged("a plain run")

	// No cache directory: a warning, then built without one.
	a, err := parseCoopTablesArgs([]string{iso})
	if err != nil {
		t.Fatal(err)
	}
	var o, s bytes.Buffer
	if err := coopTables(&o, &s, a, func() (string, error) { return "", errors.New("no home") }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.String(), "WARNING: no home; building without a cache.") || !strings.Contains(o.String(), "cache: off") {
		t.Errorf("no cache directory: status %q, out\n%s", s.String(), o.String())
	}
}

func TestCoopTablesErrors(t *testing.T) {
	dir := t.TempDir()
	notISO := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notISO, bytes.Repeat([]byte("not a disc "), 200), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dir, "missing.iso"), notISO} {
		err := coopTables(new(bytes.Buffer), new(bytes.Buffer), coopTablesArgs{path: path}, func() (string, error) { return dir, nil })
		if err == nil {
			t.Errorf("%s: no error", path)
		}
	}
}
