package stagetables

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage/stagetest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/keys"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/tablestest"
)

// tablesLog records log lines with their levels.
type tablesLog struct{ lines []string }

func (l *tablesLog) Log(level log.Level, msg string) {
	l.lines = append(l.lines, fmt.Sprintf("%d %s", level, msg))
}

// useTablesDir points CacheDir at dir for the test (an error
// for dir "").
func useTablesDir(t *testing.T, dir string) {
	t.Helper()
	saved := CacheDir
	t.Cleanup(func() { CacheDir = saved })
	CacheDir = func() (string, error) {
		if dir == "" {
			return "", errors.New("tables: no config directory for the cache: $HOME is not defined")
		}
		return dir, nil
	}
}

// writeImage writes img to a new file and returns its path.
func writeImage(t *testing.T, name string, img []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, img, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// fixtureIdentity is the identity of the vanilla fixture's tables, which
// every disc with its stages has.
func fixtureIdentity(t *testing.T) uint64 {
	t.Helper()
	d, err := inject.NewDisc(bytes.NewReader(tablestest.ISO(tablestest.Vanilla())), int64(len(tablestest.ISO(tablestest.Vanilla()))))
	if err != nil {
		t.Fatal(err)
	}
	tb, err := tables.Build(d)
	if err != nil {
		t.Fatal(err)
	}
	id, err := tb.Identity()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestTakeCoopISO(t *testing.T) {
	for _, c := range []struct {
		args []string
		iso  string
		rest []string
		err  string
	}{
		{args: []string{"Alice", "--port", "7000"}, rest: []string{"Alice", "--port", "7000"}},
		{args: []string{"--coop-iso", "ww.iso", "Alice"}, iso: "ww.iso", rest: []string{"Alice"}},
		{args: []string{"Alice", "--coop-iso=C:\\Roms\\ww.iso"}, iso: "C:\\Roms\\ww.iso", rest: []string{"Alice"}},
		{args: []string{"--coop-iso=a.iso", "--coop-iso", "a.iso"}, iso: "a.iso", rest: []string{}},
		{args: []string{"--", "--coop-iso", "x.iso"}, rest: []string{"--", "--coop-iso", "x.iso"}},
		{args: []string{"A", "--coop-iso=a.iso", "--", "--coop-iso=b.iso"}, iso: "a.iso", rest: []string{"A", "--", "--coop-iso=b.iso"}},
		{args: []string{"--coop-isolate"}, rest: []string{"--coop-isolate"}},
		{args: []string{"--coop-iso"}, err: "--coop-iso needs the path"},
		{args: []string{"--coop-iso", "--"}, err: "--coop-iso needs the path"},
		{args: []string{"--coop-iso="}, err: "--coop-iso needs the path"},
		{args: []string{"--coop-iso=a.iso", "--coop-iso=b.iso"}, err: "give one disc image"},
	} {
		iso, rest, err := TakeISO(c.args)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: err %v, want %q", c.args, err, c.err)
			}
			continue
		}
		if err != nil || iso != c.iso || !slices.Equal(rest, c.rest) {
			t.Errorf("%q = %q, %q, %v; want %q, %q", c.args, iso, rest, err, c.iso, c.rest)
		}
	}
}

// TestNoCoopStageTables: without tables a session has identity 0, no
// switch verdict and a policy that opens no memory switch, and the log
// says what to do. (TestCoopStageTablesWhy has the other causes.)
func TestNoCoopStageTables(t *testing.T) {
	var none *Tables
	if none.Hash() != 0 || none.SyncedSwitch(keys.TableDRC, 0x20) || none.SwitchVerdict() != nil {
		t.Error("a session without tables has a hash or a switch")
	}
	if p := none.Policy(); *p != *world.DefaultPolicy() {
		t.Error("the policy without tables is not world.DefaultPolicy")
	}
	useTablesDir(t, t.TempDir())
	st, err := Load("")
	if st != nil || !errors.Is(err, ErrNoTables) {
		t.Fatalf("Load with nothing recorded = %v, %v", st, err)
	}
	var l tablesLog
	Log(&l, st, err)
	if len(l.lines) != 1 || !strings.HasPrefix(l.lines[0], fmt.Sprint(log.Warn)) ||
		!strings.Contains(l.lines[0], "patch has recorded no disc") || !strings.Contains(l.lines[0], "Run patch on your vanilla ISO with this build") || !strings.Contains(l.lines[0], "--coop-iso") ||
		!strings.Contains(l.lines[0], "Memory switches, room puzzles and kills are not synced") {
		t.Errorf("log %q", l.lines)
	}

	useTablesDir(t, "")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "pass --coop-iso") {
		t.Errorf("without a config directory: %v", err)
	}
}

// TestCoopISOTables: --coop-iso builds the image's tables and caches them;
// the next session reads the cache; without a config directory they are
// built uncached; a missing image or one without stages is an error.
func TestCoopISOTables(t *testing.T) {
	dir := t.TempDir()
	useTablesDir(t, dir)
	iso := writeImage(t, "ww.iso", tablestest.ISO(tablestest.Vanilla()))
	want := fixtureIdentity(t)

	st, err := Load(iso)
	if err != nil {
		t.Fatal(err)
	}
	if st.Hash() != want || !strings.Contains(st.from, "built from the disc") || strings.Contains(st.from, "not cached") {
		t.Errorf("first load: %016x %q", st.Hash(), st.from)
	}
	if !st.SyncedSwitch(keys.TableDRC, 0x20) || st.SyncedSwitch(keys.TableDRC, 0x30) {
		t.Error("the crystal is not synced, or the push block's path is")
	}
	sw := switchByte(t, keys.TableDRC, 0x20)
	if p := st.Policy(); p[sw.Off]&sw.Val == 0 || p[switchByte(t, keys.TableDRC, 0x30).Off]&switchByte(t, keys.TableDRC, 0x30).Val != 0 {
		t.Error("the policy doesn't follow the verdict")
	}
	if v := st.SwitchVerdict(); v == nil || !v(keys.TableDRC, 0x20) {
		t.Error("switchVerdict lost the crystal")
	}
	var l tablesLog
	Log(&l, st, nil)
	n, ts, gba := st.SyncedCount()
	if len(l.lines) != 1 || !strings.HasPrefix(l.lines[0], fmt.Sprint(log.OK)) ||
		!strings.Contains(l.lines[0], fmt.Sprintf("stage tables %016x, %s", want, iso)) ||
		!strings.Contains(l.lines[0], fmt.Sprintf("%d latching memory switch(es) in %d save table(s) sync (%d of them, shared with Tingle Tuner switches, only while no Game Boy Advance is linked)", n, ts, gba)) || n == 0 || gba != 0 {
		t.Errorf("log %q (%d switches in %d tables, %d GBA-dependent)", l.lines, n, ts, gba)
	}

	again, err := Load(iso)
	if err != nil || again.Hash() != want || !strings.HasSuffix(again.from, "from the cache") {
		t.Errorf("second load: %v %+v", err, again)
	}

	useTablesDir(t, "")
	uncached, err := Load(iso)
	if err != nil || uncached.Hash() != want || !strings.Contains(uncached.from, "not cached: no config directory") {
		t.Errorf("load without a config directory: %v %+v", err, uncached)
	}

	useTablesDir(t, dir)
	if _, err := Load(filepath.Join(t.TempDir(), "missing.iso")); err == nil || !strings.Contains(err.Error(), "missing.iso") {
		t.Errorf("a missing image: %v", err)
	}
	empty := writeImage(t, "empty.iso", tablestest.Disc(nil).ISO(tablestest.DOL()))
	if _, err := Load(empty); err == nil || !strings.Contains(err.Error(), "no stages") {
		t.Errorf("an image without stages: %v", err)
	}
}

// switchByte is the image byte and bit of memory switch no in save table
// table (d_save.cpp:1084-1087).
func switchByte(t *testing.T, table, no int) world.Gain {
	t.Helper()
	_, b, err := addr.SwitchBit(no)
	if err != nil {
		t.Fatal(err)
	}
	return world.Gain{Off: uint16(addr.OffMemBit(table) + b.Off), Val: b.Mask}
}

// TestCacheCoopTablesForPatch: the patch step caches the tables of the disc
// written and records it, so a session without --coop-iso finds them. A
// disc it can't open is one warning line and no record. A disc whose
// tables can't be built is one warning line too, which says that patching
// again won't help, and it is recorded, so the session says the same.
func TestCacheCoopTablesForPatch(t *testing.T) {
	dir := t.TempDir()
	useTablesDir(t, dir)
	out := writeImage(t, "ww-multiplayer.iso", tablestest.PatchableISO(tablestest.Vanilla()))

	var w bytes.Buffer
	CacheForPatch(&w, out)
	if got := w.String(); !strings.HasPrefix(got, "  co-op : stage tables built and cached in ") || !strings.HasSuffix(got, " s, for --coop sessions\n") {
		t.Errorf("first patch says %q", got)
	}
	rec, err := tables.ReadLastPatched(dir)
	if err != nil || rec.Image != out {
		t.Fatalf("record %+v, %v", rec, err)
	}
	st, err := Load("")
	if err != nil || st.Hash() != fixtureIdentity(t) || !strings.Contains(st.from, out+" (the disc last patched, ") || !strings.HasSuffix(st.from, "from the cache") {
		t.Fatalf("session after patch: %v %+v", err, st)
	}

	w.Reset()
	CacheForPatch(&w, out)
	if got := w.String(); !strings.HasPrefix(got, "  co-op : stage tables already cached in ") {
		t.Errorf("second patch says %q", got)
	}

	// The cache is gone (another build): the session rebuilds from the image.
	if err := os.Remove(tables.CacheFile(dir, rec.DOL)); err != nil {
		t.Fatal(err)
	}
	st, err = Load("")
	if err != nil || !strings.Contains(st.from, "built from the disc") {
		t.Fatalf("session without the cache: %v %+v", err, st)
	}

	for _, c := range []struct {
		name, image, dir, want string
		built                  bool // the disc was read, and its tables can't be built
	}{
		{"no stages", writeImage(t, "empty.iso", tablestest.Disc(nil).ISO(tablestest.PatchableDOL())), t.TempDir(), "the disc has no stages", true},
		{"no image", filepath.Join(t.TempDir(), "gone.iso"), t.TempDir(), "gone.iso", false},
		// Two broken stages: the error names both, on the one line.
		{"two broken archives", writeImage(t, "broken.iso", tablestest.Disc([]stagetest.Stage{{Name: "a", DZS: []byte{1}}, {Name: "b", DZS: []byte{1}}}).ISO(tablestest.PatchableDOL())),
			t.TempDir(), "res/Stage/a/Stage.arc: dzs/stage.dzs: dzx: ", true},
		{"two broken archives, the second", writeImage(t, "broken2.iso", tablestest.Disc([]stagetest.Stage{{Name: "a", DZS: []byte{1}}, {Name: "b", DZS: []byte{1}}}).ISO(tablestest.PatchableDOL())),
			t.TempDir(), "; stagedata: res/Stage/b/Stage.arc: dzs/stage.dzs: dzx: ", true},
		{"no config directory", out, "", "no config directory", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			useTablesDir(t, c.dir)
			var w bytes.Buffer
			CacheForPatch(&w, c.image)
			got := w.String()
			prefix, advice := "  co-op : WARNING: no co-op stage tables for this disc (", "--coop sessions will need --coop-iso"
			if c.built {
				prefix, advice = "  co-op : WARNING: this build can't make the co-op stage tables of this disc (", "patching again or --coop-iso won't change that"
			}
			if !strings.HasPrefix(got, prefix) || !strings.Contains(got, c.want) || !strings.Contains(got, advice) ||
				!strings.Contains(got, "The patched ISO is fine") || strings.Count(got, "\n") != 1 {
				t.Errorf("says %q, want one warning about %q", got, c.want)
			}
			if c.built && (strings.Contains(got, "will need --coop-iso") || !strings.Contains(got, "The disc is recorded, so a later build that can read it needs no new patch for its tables") ||
				!strings.Contains(got, "please report this line")) {
				t.Errorf("says %q, which doesn't say a new patch won't help", got)
			}
			if c.dir == "" {
				return
			}
			rec, err := tables.ReadLastPatched(c.dir)
			switch {
			case !c.built && err == nil:
				t.Error("a disc patch couldn't open was recorded")
			case c.built && (err != nil || rec.Image != c.image):
				t.Fatalf("the disc was not recorded: %+v, %v", rec, err)
			case c.built:
				// The session reads the recorded image and says the same.
				if _, err := Load(""); err == nil || errors.Is(err, ErrNoTables) || !strings.Contains(oneLine(err), c.want) || !strings.Contains(err.Error(), "please report this line") {
					t.Errorf("the session after this patch: %v", err)
				}
			}
		})
	}
}

// TestCacheCoopTablesForPatchUncached: tables the patch step built but
// can't cache are a note on its line, not a warning, and the disc is
// recorded, so a session builds them again.
func TestCacheCoopTablesForPatchUncached(t *testing.T) {
	dir := t.TempDir()
	useTablesDir(t, dir)
	img := writeImage(t, "ww-multiplayer.iso", tablestest.PatchableISO(tablestest.Vanilla()))
	d, err := inject.OpenDisc(img)
	if err != nil {
		t.Fatal(err)
	}
	id, err := tables.Identify(d)
	d.Close()
	if err != nil {
		t.Fatal(err)
	}
	// A directory where the cache file goes: Save can't rename over it.
	if err := os.Mkdir(tables.CacheFile(dir, id.DOL), 0o755); err != nil {
		t.Fatal(err)
	}
	var w bytes.Buffer
	CacheForPatch(&w, img)
	if got := w.String(); !strings.HasPrefix(got, "  co-op : stage tables built (not cached: ") || !strings.Contains(got, "; each --coop session builds them again) in ") ||
		!strings.HasSuffix(got, " s, for --coop sessions\n") || strings.Count(got, "\n") != 1 {
		t.Errorf("patch says %q", got)
	}
	if rec, err := tables.ReadLastPatched(dir); err != nil || rec.Image != img || rec.ID != id {
		t.Fatalf("record %+v, %v; want %s", rec, err, img)
	}
	st, err := Load("")
	if err != nil || st.Hash() != fixtureIdentity(t) || !strings.Contains(st.from, "built from the disc") || !strings.Contains(st.from, "not cached") {
		t.Errorf("the session after this patch: %v %+v", err, st)
	}
}

// TestCacheCoopTablesForPatchNoRecord: when the disc can't be recorded
// either, the patch step's warning names both errors. Tables built but not
// cached send sessions to --coop-iso, the one way left to name the disc. A
// disc whose tables can't be built says that sessions can't take its
// tables and that patching again or --coop-iso, which run the same build,
// won't change that, but not that sessions run without switch sync: the
// record of a disc patched before, if any, stays, and a session takes its
// tables.
func TestCacheCoopTablesForPatchNoRecord(t *testing.T) {
	for _, c := range []struct {
		name, prefix string
		img          []byte
		noCache      bool     // a directory where the cache file goes, too
		want, not    []string // in the warning, and not in it
	}{
		{"a disc whose tables can't be built", "  co-op : WARNING: this build can't make the co-op stage tables of this disc (tables: stagedata: res/Stage/a/Stage.arc: ",
			tablestest.Disc([]stagetest.Stage{{Name: "a", DZS: []byte{1}}}).ISO(tablestest.PatchableDOL()), false,
			[]string{"), and the disc can't be recorded either (tables: ", "The patched ISO is fine", "--coop sessions can't take this disc's tables", "patching again or --coop-iso won't change that",
				"A session without --coop-iso may still take the tables of a disc recorded before; its first lines name that disc", "please report this line"},
			[]string{"will need --coop-iso", "The disc is recorded", "run without memory switch sync"}},
		{"tables that can't be cached", "  co-op : WARNING: no co-op stage tables for this disc (", tablestest.PatchableISO(tablestest.Vanilla()), true,
			[]string{"built, but not cached (", "The patched ISO is fine", "--coop sessions will need --coop-iso"}, []string{"The disc is recorded"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			useTablesDir(t, dir)
			img := writeImage(t, "ww-multiplayer.iso", c.img)
			// A directory where the record goes: RecordPatched can't
			// rename over it.
			if err := os.Mkdir(tables.LastPatchedFile(dir), 0o755); err != nil {
				t.Fatal(err)
			}
			if c.noCache {
				d, err := inject.OpenDisc(img)
				if err != nil {
					t.Fatal(err)
				}
				id, err := tables.Identify(d)
				d.Close()
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(tables.CacheFile(dir, id.DOL), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			var w bytes.Buffer
			CacheForPatch(&w, img)
			got := w.String()
			if !strings.HasPrefix(got, c.prefix) || strings.Count(got, "\n") != 1 {
				t.Errorf("patch says %q, want one line starting %q", got, c.prefix)
			}
			for _, s := range c.want {
				if !strings.Contains(got, s) {
					t.Errorf("patch says %q, which lacks %q", got, s)
				}
			}
			for _, s := range c.not {
				if strings.Contains(got, s) {
					t.Errorf("patch says %q, which has %q", got, s)
				}
			}
		})
	}
}

// TestCoopStageTablesWhy: without tables, the session's one warning says
// why, with advice that fits the cause. No disc recorded: patch with this
// build. The recorded image or the record can't be read: patch again, or
// --coop-iso. The disc can't give tables: a report, since neither helps.
func TestCoopStageTablesWhy(t *testing.T) {
	broken := func(t *testing.T) string {
		return writeImage(t, "broken.iso", tablestest.Disc([]stagetest.Stage{{Name: "a", DZS: []byte{1}}}).ISO(tablestest.PatchableDOL()))
	}
	const stageErr = "stagedata: res/Stage/a/Stage.arc: dzs/stage.dzs: dzx: "
	writeRecord := func(t *testing.T, dir, data string) {
		t.Helper()
		if err := os.WriteFile(tables.LastPatchedFile(dir), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	noPatch := []string{"run patch", "Run patch", "pass --coop-iso"}
	for _, c := range []struct {
		name string
		// setup prepares the tables directory and returns the session's
		// --coop-iso, "" for none.
		setup  func(t *testing.T, dir string) string
		noDisc bool     // ErrNoTables
		want   []string // in the warning
		not    []string // not in it
	}{
		{"no record", func(*testing.T, string) string { return "" }, true,
			[]string{"patch has recorded no disc on this machine", "Run patch on your vanilla ISO with this build", "pass --coop-iso"}, []string{"please report"}},
		{"the image and its cache gone", func(t *testing.T, dir string) string {
			img := writeImage(t, "ww-multiplayer.iso", tablestest.PatchableISO(tablestest.Vanilla()))
			CacheForPatch(io.Discard, img)
			rec, err := tables.ReadLastPatched(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range []string{tables.CacheFile(dir, rec.DOL), img} {
				if err := os.Remove(f); err != nil {
					t.Fatal(err)
				}
			}
			return ""
		}, false, []string{"ww-multiplayer.iso, the disc last patched, are not cached, and the image can't be read", "run patch on your vanilla ISO again, or pass --coop-iso"},
			[]string{"recorded no disc", "please report"}},
		{"a corrupt record", func(t *testing.T, dir string) string { writeRecord(t, dir, "not json"); return "" }, false,
			[]string{"the last-patched record: invalid character", "run patch on your vanilla ISO again, or pass --coop-iso"}, []string{"recorded no disc", "please report"}},
		{"a record of another format", func(t *testing.T, dir string) string { writeRecord(t, dir, `{"schema": 2}`); return "" }, false,
			[]string{"the last-patched record has format 2", "run patch on your vanilla ISO again, or pass --coop-iso"}, []string{"recorded no disc", "please report"}},
		{"a recorded disc whose tables can't be built", func(t *testing.T, dir string) string {
			img := broken(t)
			d, err := inject.OpenDisc(img)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			id, err := tables.Identify(d)
			if err != nil {
				t.Fatal(err)
			}
			if err := tables.RecordPatched(dir, img, id, time.Now()); err != nil {
				t.Fatal(err)
			}
			return ""
		}, false, []string{"this build can't make them from ", "broken.iso (the disc last patched, ", stageErr,
			"Patching that disc again won't change that; if Dolphin runs another disc, run patch on it or pass --coop-iso <the ISO Dolphin runs>; " +
				"if patch made this one from your unmodified Wind Waker (GZLE01) image, please report this line"},
			[]string{"Run patch", "run patch on your vanilla ISO", "Patching again or --coop-iso", "recorded no disc"}},
		{"--coop-iso on a disc whose tables can't be built", func(t *testing.T, _ string) string { return broken(t) }, false,
			[]string{"this build can't make them from ", "broken.iso: tables: " + stageErr, "Patching again won't change that",
				"if it is your unmodified Wind Waker (GZLE01) image, or patch made it from one, please report this line"},
			append([]string{"the disc last patched"}, noPatch...)},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			useTablesDir(t, dir)
			st, err := Load(c.setup(t, dir))
			if st != nil || err == nil || errors.Is(err, ErrNoTables) != c.noDisc {
				t.Fatalf("Load = %+v, %v; want ErrNoTables %v", st, err, c.noDisc)
			}
			var l tablesLog
			Log(&l, st, err)
			if len(l.lines) != 1 || !strings.HasPrefix(l.lines[0], fmt.Sprint(log.Warn)) || strings.Contains(l.lines[0], "\n") ||
				!strings.Contains(l.lines[0], "Memory switches, room puzzles and kills are not synced") {
				t.Fatalf("log %q", l.lines)
			}
			for _, s := range c.want {
				if !strings.Contains(l.lines[0], s) {
					t.Errorf("the warning %q lacks %q", l.lines[0], s)
				}
			}
			for _, s := range c.not {
				if strings.Contains(l.lines[0], s) {
					t.Errorf("the warning %q has %q", l.lines[0], s)
				}
			}
		})
	}
}

// TestCoopTablesAfterAFailedPatch: patch records a disc whose tables can't
// be built. When the image later holds a disc they build from (patched
// again by other means, say), the session takes its tables with no new
// patch, and the record follows the image.
func TestCoopTablesAfterAFailedPatch(t *testing.T) {
	dir := t.TempDir()
	useTablesDir(t, dir)
	img := writeImage(t, "ww-multiplayer.iso", tablestest.Disc([]stagetest.Stage{{Name: "a", DZS: []byte{1}}}).ISO(tablestest.PatchableDOL()))
	var w bytes.Buffer
	CacheForPatch(&w, img)
	if got := w.String(); !strings.HasPrefix(got, "  co-op : WARNING: this build can't make the co-op stage tables of this disc (") {
		t.Fatalf("patch says %q", got)
	}
	if err := os.WriteFile(img, tablestest.PatchableISO(tablestest.Vanilla()), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := Load("")
	if err != nil || st.Hash() != fixtureIdentity(t) || !strings.Contains(st.from, img+" (the disc last patched, ") ||
		!strings.Contains(st.from, "built from the disc") || !strings.Contains(st.from, "the image has changed since then") {
		t.Fatalf("the session after the image changed: %v %+v", err, st)
	}
	rec, err := tables.ReadLastPatched(dir)
	if err != nil || rec.Image != img {
		t.Fatalf("record %+v, %v", rec, err)
	}
	if _, err := tables.Load(dir, rec.ID); err != nil {
		t.Errorf("the record doesn't name the new disc's cached tables: %v", err)
	}
}

// TestCoopTablesAfterAnotherDiscFails: patching a disc whose tables can't
// be built replaces the record of a disc patched before, so the session
// doesn't take the earlier disc's tables while Dolphin runs this one. It
// says why there are none, and its advice holds when Dolphin runs the
// earlier disc after all: --coop-iso, or patching that disc again, gives
// its tables.
func TestCoopTablesAfterAnotherDiscFails(t *testing.T) {
	dir := t.TempDir()
	useTablesDir(t, dir)
	good := writeImage(t, "ww-multiplayer.iso", tablestest.PatchableISO(tablestest.Vanilla()))
	CacheForPatch(io.Discard, good)
	broken := writeImage(t, "broken.iso", tablestest.Disc([]stagetest.Stage{{Name: "a", DZS: []byte{1}}}).ISO(tablestest.PatchableDOL()))
	var w bytes.Buffer
	CacheForPatch(&w, broken)
	if got := w.String(); !strings.HasPrefix(got, "  co-op : WARNING: this build can't make the co-op stage tables of this disc (") {
		t.Fatalf("patch says %q", got)
	}
	st, err := Load("")
	if st != nil || err == nil || !strings.Contains(err.Error(), broken+" (the disc last patched, ") || strings.Contains(err.Error(), good) {
		t.Fatalf("the session after patching %s, then %s: %+v, %v; want %s's build failure", good, broken, st, err, broken)
	}

	want := fixtureIdentity(t)
	if st, err := Load(good); err != nil || st.Hash() != want || !strings.HasSuffix(st.from, "from the cache") {
		t.Errorf("--coop-iso %s: %v %+v", good, err, st)
	}
	CacheForPatch(io.Discard, good)
	if st, err := Load(""); err != nil || st.Hash() != want || !strings.Contains(st.from, good+" (the disc last patched, ") {
		t.Errorf("the session after patching %s again: %v %+v", good, err, st)
	}
}

// TestCacheCoopTablesForPatchPanics: a panic building the tables is a
// warning, not the patch's failure.
func TestCacheCoopTablesForPatchPanics(t *testing.T) {
	saved := CacheDir
	t.Cleanup(func() { CacheDir = saved })
	CacheDir = func() (string, error) { panic("boom") }
	var w bytes.Buffer
	CacheForPatch(&w, "x.iso")
	if got := w.String(); !strings.HasPrefix(got, "  co-op : WARNING: building the co-op stage tables failed (a bug; please report it: boom)") || strings.Contains(got, "--coop-iso") {
		t.Errorf("says %q", got)
	}
}

// The hub takes the session's verdict: hub.SwitchPolicy over it is the
// replica's policy too, so both sides agree on every memory switch.
func TestCoopStageTablesPolicyIsHubs(t *testing.T) {
	useTablesDir(t, t.TempDir())
	st, err := Load(writeImage(t, "ww.iso", tablestest.ISO(tablestest.Vanilla())))
	if err != nil {
		t.Fatal(err)
	}
	if *st.Policy() != *hub.SwitchPolicy(world.DefaultPolicy(), st.SwitchVerdict()) {
		t.Error("the replica's policy is not the hub's")
	}
}

// FuzzTakeCoopISO: on any command line TakeISO takes every --coop-iso
// before "--" and nothing else, in order, or fails.
func FuzzTakeCoopISO(f *testing.F) {
	for _, s := range []string{"Alice\x00--coop-iso\x00ww.iso", "--coop-iso=a\x00--\x00--coop-iso=b", "--coop-iso", "--coop-iso=\x00x", "a\x00b"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		args := strings.Split(s, "\x00")
		iso, rest, err := TakeISO(args)
		if err != nil {
			if iso != "" || rest != nil {
				t.Fatalf("%q: %q, %q with error %v", args, iso, rest, err)
			}
			return
		}
		end := slices.Index(rest, "--")
		if end < 0 {
			end = len(rest)
		}
		var kept []string
		for i, a := range args {
			if a == "--" {
				kept = append(kept, args[i:]...)
				break
			}
			if a == ISOFlag {
				if args[i+1] != iso {
					t.Fatalf("%q: took %q, not %q", args, iso, args[i+1])
				}
				args[i+1] = "\x00" // its value, taken with it
				continue
			}
			if v, ok := strings.CutPrefix(a, ISOFlag+"="); ok {
				if v != iso {
					t.Fatalf("%q: took %q, not %q", args, iso, v)
				}
				continue
			}
			if a != "\x00" {
				kept = append(kept, a)
			}
		}
		if !slices.Equal(rest, kept) || slices.Contains(rest[:end], ISOFlag) {
			t.Fatalf("%q: rest %q, want %q", strings.Split(s, "\x00"), rest, kept)
		}
	})
}
