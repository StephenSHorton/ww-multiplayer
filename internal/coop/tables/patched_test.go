package tables

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/keys"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/tablestest"
)

var patchedAt = time.Date(2026, 10, 14, 12, 0, 0, 123_456_789, time.UTC)

func TestRecordPatched(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "coop-tables")
	tb := buildFixture(t, tablestest.Vanilla())
	if _, err := ReadLastPatched(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ReadLastPatched without a record = %v, want fs.ErrNotExist", err)
	}
	t.Chdir(t.TempDir())
	if err := RecordPatched(dir, "ww-multiplayer.iso", tb.ID, patchedAt); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLastPatched(dir)
	if err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs("ww-multiplayer.iso")
	want := LastPatched{Schema: LastPatchedSchema, ID: tb.ID, Image: abs, At: patchedAt.Truncate(time.Millisecond)}
	if got != want {
		t.Errorf("read %+v, want %+v", got, want)
	}
	if err := RecordPatched(dir, "x.iso", ID{DOL: "../escape", StageFiles: tb.StageFiles}, patchedAt); err == nil {
		t.Error("recorded an ID that is not a hash")
	}
	ents, err := os.ReadDir(dir)
	if err != nil || len(ents) != 1 || ents[0].Name() != lastPatchedName {
		t.Errorf("dir holds %v (%v), want the record alone", ents, err)
	}
}

func TestReadLastPatchedRefuses(t *testing.T) {
	tb := buildFixture(t, tablestest.Vanilla())
	good := map[string]any{
		"schema": LastPatchedSchema, "dol_sha256": tb.DOL, "stage_files_sha256": tb.StageFiles,
		"image": filepath.Join(t.TempDir(), "ww.iso"), "at": patchedAt,
	}
	edit := func(f func(m map[string]any)) []byte {
		m := map[string]any{}
		for k, v := range good {
			m[k] = v
		}
		f(m)
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	for _, c := range []struct {
		name string
		data []byte
		want error
	}{
		{"another format", edit(func(m map[string]any) { m["schema"] = LastPatchedSchema + 1 }), ErrStale},
		{"no format", edit(func(m map[string]any) { delete(m, "schema") }), ErrStale},
		{"not JSON", []byte("not json"), ErrCorrupt},
		{"cut short", edit(func(map[string]any) {})[:40], ErrCorrupt},
		{"an unknown field", edit(func(m map[string]any) { m["extra"] = 1 }), ErrCorrupt},
		{"a bad hash", edit(func(m map[string]any) { m["dol_sha256"] = "../../escape" }), ErrCorrupt},
		{"no stage hash", edit(func(m map[string]any) { delete(m, "stage_files_sha256") }), ErrCorrupt},
		{"a relative image", edit(func(m map[string]any) { m["image"] = "ww.iso" }), ErrCorrupt},
		{"no image", edit(func(m map[string]any) { delete(m, "image") }), ErrCorrupt},
		{"a NUL in the image", edit(func(m map[string]any) { m["image"] = good["image"].(string) + "\x00x" }), ErrCorrupt},
		{"too big", append(edit(func(map[string]any) {}), make([]byte, maxLastPatchedSize)...), ErrCorrupt},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(LastPatchedFile(dir), c.data, 0o644); err != nil {
				t.Fatal(err)
			}
			if rec, err := ReadLastPatched(dir); !errors.Is(err, c.want) {
				t.Errorf("ReadLastPatched = %+v, %v; want %v", rec, err, c.want)
			}
		})
	}
	dir := t.TempDir()
	if err := os.WriteFile(LastPatchedFile(dir), edit(func(map[string]any) {}), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLastPatched(dir); err != nil {
		t.Errorf("the good record: %v", err)
	}
}

// TestGetPatched walks the ways a session finds the disc last patched:
// its cached tables, a rebuild from the image when the cache is gone or
// another build's, the image patched again by other means, and the image
// gone too.
func TestGetPatched(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(t.TempDir(), "ww-multiplayer.iso")
	if err := os.WriteFile(img, tablestest.PatchableISO(tablestest.Vanilla()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := GetPatched(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("GetPatched without a record = %v, want fs.ErrNotExist", err)
	}
	want := buildFixture(t, tablestest.Vanilla())
	built, err := Build(openISO(t, tablestest.PatchableISO(tablestest.Vanilla())))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := built.Save(dir); err != nil {
		t.Fatal(err)
	}
	if err := RecordPatched(dir, img, built.ID, patchedAt); err != nil {
		t.Fatal(err)
	}
	same := func(tb *Tables) bool { return identityOf(t, tb) == identityOf(t, want) }

	tb, ps, err := GetPatched(dir)
	if err != nil || !ps.Cached || ps.Changed || ps.Path != CacheFile(dir, built.DOL) || ps.Record.Image != img || !same(tb) {
		t.Fatalf("GetPatched with the cache = %+v, %v", ps, err)
	}

	// The cache file is gone (or another build's): rebuilt from the image
	// and cached again.
	if err := os.Remove(CacheFile(dir, built.DOL)); err != nil {
		t.Fatal(err)
	}
	tb, ps, err = GetPatched(dir)
	if err != nil || ps.Cached || ps.Changed || ps.Files == 0 || ps.SaveErr != nil || !same(tb) || tb.ID != built.ID {
		t.Fatalf("GetPatched without the cache = %+v, %v", ps, err)
	}
	if _, err := Load(dir, built.ID); err != nil {
		t.Errorf("the rebuilt tables were not cached: %v", err)
	}

	// The image now holds another disc: its tables, and the record follows.
	otherImg := tablestest.ISO(replace(tablestest.Vanilla(), tablestest.Dungeon("M_NewD2", keys.TableDRC)))
	if err := os.WriteFile(img, otherImg, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(CacheFile(dir, built.DOL)); err != nil {
		t.Fatal(err)
	}
	tb, ps, err = GetPatched(dir)
	if err != nil || !ps.Changed || ps.RecordErr != nil || same(tb) || tb.ID == built.ID {
		t.Fatalf("GetPatched after the image changed = %+v, %v", ps, err)
	}
	if rec, err := ReadLastPatched(dir); err != nil || rec.ID != tb.ID || rec.Image != img {
		t.Errorf("record after the image changed = %+v, %v; want ID %+v", rec, err, tb.ID)
	}

	// Neither cached nor readable.
	if err := os.Remove(CacheFile(dir, tb.DOL)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(img); err != nil {
		t.Fatal(err)
	}
	if _, _, err := GetPatched(dir); err == nil || !strings.Contains(err.Error(), "not cached, and the image can't be read") {
		t.Errorf("GetPatched with nothing left = %v", err)
	}

	// An image that isn't a disc.
	if err := os.WriteFile(img, []byte("GZLE01"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := GetPatched(dir); err == nil {
		t.Error("GetPatched read tables from a 6-byte image")
	}
}

// TestBuilderPanics: a builder's panic is Build's error, not the process's.
func TestBuilderPanics(t *testing.T) {
	saved := builders
	t.Cleanup(func() { builders = saved })
	builders = append([]builder(nil), saved...)
	builders[2].build = func(*Tables, []*stagedata.Stage, *stagedata.ObjectNames) { panic("index out of range") }
	dir := t.TempDir()
	_, st, err := Get(vanillaDisc(t), dir)
	if err == nil || !strings.Contains(err.Error(), "the switches builder failed on this disc") || !strings.Contains(err.Error(), "index out of range") {
		t.Fatalf("Build = %v, want the switches builder's panic", err)
	}
	if st.SaveErr != nil || st.Save != 0 {
		t.Errorf("stats %+v after a failed build", st)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("a failed build left %v in the cache", ents)
	}
}
