package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata/stagetest"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/keys"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/tablestest"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/internal/report"
)

// tablesLog records log lines with their levels.
type tablesLog struct{ lines []string }

func (l *tablesLog) Log(level report.Level, msg string) {
	l.lines = append(l.lines, fmt.Sprintf("%d %s", level, msg))
}

// useTablesDir points coopTablesCacheDir at dir for the test (an error
// for dir "").
func useTablesDir(t *testing.T, dir string) {
	t.Helper()
	saved := coopTablesCacheDir
	t.Cleanup(func() { coopTablesCacheDir = saved })
	coopTablesCacheDir = func() (string, error) {
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
		iso, rest, err := takeCoopISO(c.args)
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
// says what to do.
func TestNoCoopStageTables(t *testing.T) {
	var none *coopStageTables
	if none.hash() != 0 || none.syncedSwitch(keys.TableDRC, 0x20) || none.switchVerdict() != nil {
		t.Error("a session without tables has a hash or a switch")
	}
	if p := none.policy(); *p != *world.DefaultPolicy() {
		t.Error("the policy without tables is not world.DefaultPolicy")
	}
	useTablesDir(t, t.TempDir())
	st, err := loadCoopStageTables("")
	if st != nil || !errors.Is(err, errNoCoopTables) {
		t.Fatalf("loadCoopStageTables with nothing recorded = %v, %v", st, err)
	}
	var l tablesLog
	logCoopStageTables(&l, st, err)
	if len(l.lines) != 1 || !strings.HasPrefix(l.lines[0], fmt.Sprint(report.Warn)) ||
		!strings.Contains(l.lines[0], "Run patch on your vanilla ISO again") || !strings.Contains(l.lines[0], "--coop-iso") ||
		!strings.Contains(l.lines[0], "Memory switches, room puzzles and kills are not synced") {
		t.Errorf("log %q", l.lines)
	}

	useTablesDir(t, "")
	if _, err := loadCoopStageTables(""); err == nil || !strings.Contains(err.Error(), "pass --coop-iso") {
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

	st, err := loadCoopStageTables(iso)
	if err != nil {
		t.Fatal(err)
	}
	if st.hash() != want || !strings.Contains(st.from, "built from the disc") || strings.Contains(st.from, "not cached") {
		t.Errorf("first load: %016x %q", st.hash(), st.from)
	}
	if !st.syncedSwitch(keys.TableDRC, 0x20) || st.syncedSwitch(keys.TableDRC, 0x30) {
		t.Error("the crystal is not synced, or the push block's path is")
	}
	sw := switchByte(t, keys.TableDRC, 0x20)
	if p := st.policy(); p[sw.Off]&sw.Val == 0 || p[switchByte(t, keys.TableDRC, 0x30).Off]&switchByte(t, keys.TableDRC, 0x30).Val != 0 {
		t.Error("the policy doesn't follow the verdict")
	}
	if v := st.switchVerdict(); v == nil || !v(keys.TableDRC, 0x20) {
		t.Error("switchVerdict lost the crystal")
	}
	var l tablesLog
	logCoopStageTables(&l, st, nil)
	n, ts := st.syncedCount()
	if len(l.lines) != 1 || !strings.HasPrefix(l.lines[0], fmt.Sprint(report.OK)) ||
		!strings.Contains(l.lines[0], fmt.Sprintf("stage tables %016x, %s", want, iso)) ||
		!strings.Contains(l.lines[0], fmt.Sprintf("%d latching memory switch(es) in %d save table(s)", n, ts)) || n == 0 {
		t.Errorf("log %q (%d switches in %d tables)", l.lines, n, ts)
	}

	again, err := loadCoopStageTables(iso)
	if err != nil || again.hash() != want || !strings.HasSuffix(again.from, "from the cache") {
		t.Errorf("second load: %v %+v", err, again)
	}

	useTablesDir(t, "")
	uncached, err := loadCoopStageTables(iso)
	if err != nil || uncached.hash() != want || !strings.Contains(uncached.from, "not cached: no config directory") {
		t.Errorf("load without a config directory: %v %+v", err, uncached)
	}

	useTablesDir(t, dir)
	if _, err := loadCoopStageTables(filepath.Join(t.TempDir(), "missing.iso")); err == nil || !strings.Contains(err.Error(), "missing.iso") {
		t.Errorf("a missing image: %v", err)
	}
	empty := writeImage(t, "empty.iso", tablestest.Disc(nil).ISO(tablestest.DOL()))
	if _, err := loadCoopStageTables(empty); err == nil || !strings.Contains(err.Error(), "no stages") {
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
// written and records it, so a session without --coop-iso finds them; a
// disc it can't read is one warning line and no record.
func TestCacheCoopTablesForPatch(t *testing.T) {
	dir := t.TempDir()
	useTablesDir(t, dir)
	out := writeImage(t, "ww-multiplayer.iso", tablestest.PatchableISO(tablestest.Vanilla()))

	var w bytes.Buffer
	cacheCoopTablesForPatch(&w, out)
	if got := w.String(); !strings.HasPrefix(got, "  co-op : stage tables built and cached in ") || !strings.HasSuffix(got, " s, for --coop sessions\n") {
		t.Errorf("first patch says %q", got)
	}
	rec, err := tables.ReadLastPatched(dir)
	if err != nil || rec.Image != out {
		t.Fatalf("record %+v, %v", rec, err)
	}
	st, err := loadCoopStageTables("")
	if err != nil || st.hash() != fixtureIdentity(t) || !strings.Contains(st.from, out+" (the disc last patched, ") || !strings.HasSuffix(st.from, "from the cache") {
		t.Fatalf("session after patch: %v %+v", err, st)
	}

	w.Reset()
	cacheCoopTablesForPatch(&w, out)
	if got := w.String(); !strings.HasPrefix(got, "  co-op : stage tables already cached in ") {
		t.Errorf("second patch says %q", got)
	}

	// The cache is gone (another build): the session rebuilds from the image.
	if err := os.Remove(tables.CacheFile(dir, rec.DOL)); err != nil {
		t.Fatal(err)
	}
	st, err = loadCoopStageTables("")
	if err != nil || !strings.Contains(st.from, "built from the disc") {
		t.Fatalf("session without the cache: %v %+v", err, st)
	}

	for _, c := range []struct {
		name, image, dir, want string
	}{
		{"no stages", writeImage(t, "empty.iso", tablestest.Disc(nil).ISO(tablestest.PatchableDOL())), t.TempDir(), "the disc has no stages"},
		{"no image", filepath.Join(t.TempDir(), "gone.iso"), t.TempDir(), "gone.iso"},
		{"no config directory", out, "", "no config directory"},
	} {
		t.Run(c.name, func(t *testing.T) {
			useTablesDir(t, c.dir)
			var w bytes.Buffer
			cacheCoopTablesForPatch(&w, c.image)
			got := w.String()
			if !strings.HasPrefix(got, "  co-op : WARNING: no co-op stage tables for this disc (") || !strings.Contains(got, c.want) ||
				!strings.Contains(got, "The patched ISO is fine") || strings.Count(got, "\n") != 1 {
				t.Errorf("says %q, want one warning about %q", got, c.want)
			}
			if c.dir != "" {
				if _, err := tables.ReadLastPatched(c.dir); err == nil {
					t.Error("a failed build was recorded")
				}
			}
		})
	}
}

// TestCacheCoopTablesForPatchPanics: a panic building the tables is a
// warning, not the patch's failure.
func TestCacheCoopTablesForPatchPanics(t *testing.T) {
	saved := coopTablesCacheDir
	t.Cleanup(func() { coopTablesCacheDir = saved })
	coopTablesCacheDir = func() (string, error) { panic("boom") }
	var w bytes.Buffer
	cacheCoopTablesForPatch(&w, "x.iso")
	if got := w.String(); !strings.HasPrefix(got, "  co-op : WARNING: building the co-op stage tables failed (a bug; please report it: boom)") {
		t.Errorf("says %q", got)
	}
}

// TestPatchHelper's input image, output image and tables directory.
const (
	patchHelperIn  = "WW_TEST_PATCH_IN"
	patchHelperOut = "WW_TEST_PATCH_OUT"
	patchHelperDir = "WW_TEST_PATCH_TABLES"
)

// TestPatchHelper is not a real test: runPatchCmd re-executes the test
// binary with it selected and its variables set, and it runs `patch` as
// main() does, so the tests see its output and exit code.
func TestPatchHelper(t *testing.T) {
	in, ok := os.LookupEnv(patchHelperIn)
	if !ok {
		return
	}
	dir := os.Getenv(patchHelperDir)
	coopTablesCacheDir = func() (string, error) { return dir, nil }
	os.Args = []string{"ww-multiplayer", "patch", in, os.Getenv(patchHelperOut)}
	main()
	os.Exit(0) // keep the testing package's "PASS" off stdout
}

// runPatchCmd runs `ww-multiplayer patch in out` with the co-op tables
// kept in dir, and returns its stdout and exit code.
func runPatchCmd(t *testing.T, in, out, dir string) (string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestPatchHelper$")
	cmd.Env = append(os.Environ(), patchHelperIn+"="+in, patchHelperOut+"="+out, patchHelperDir+"="+dir,
		"GORACE="+strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
	stdout, err := cmd.Output()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return string(stdout), 0
	case errors.As(err, &exitErr):
		return string(stdout), exitErr.ExitCode()
	}
	t.Fatalf("running patch: %v", err)
	return "", 0
}

// TestPatchCoopTables runs `patch` end to end on synthetic discs: the
// image it writes is byte for byte what the patcher makes of the input,
// whether or not the co-op tables can be built from it, its output is the
// old output plus one co-op line, and it exits 0 either way. Patching the
// patched image again records that image.
func TestPatchCoopTables(t *testing.T) {
	for _, c := range []struct {
		name   string
		img    []byte
		tables bool
	}{
		{"a disc with stages", tablestest.PatchableISO(tablestest.Vanilla()), true},
		{"a disc without stages", tablestest.PatchableISO(nil), false},
		{"another disc", tablestest.PatchableISO([]stagetest.Stage{tablestest.Dungeon("M_NewD2", keys.TableDRC)}), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			in := writeImage(t, "ww.iso", c.img)
			out := filepath.Join(t.TempDir(), "ww-multiplayer.iso")
			want, err := inject.PatchISOInMemory(c.img)
			if err != nil {
				t.Fatal(err)
			}
			stdout, code := runPatchCmd(t, in, out, dir)
			if code != 0 {
				t.Fatalf("exit %d:\n%s", code, stdout)
			}
			got, err := os.ReadFile(out)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("the patched image differs from PatchISOInMemory's (%d bytes, want %d; %v)", len(got), len(want), err)
			}
			lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
			co := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "  co-op : ") })
			if co < 0 || slices.ContainsFunc(lines[co+1:], func(l string) bool { return strings.HasPrefix(l, "  co-op : ") }) {
				t.Fatalf("not one co-op line:\n%s", stdout)
			}
			old := slices.Delete(slices.Clone(lines), co, co+1)
			wantOld := []string{"Wind Waker Multiplayer patcher", "  input : " + in, "  output: " + out}
			if res := old[3]; strings.HasPrefix(res, "  FST   : moved ") {
				wantOld = append(wantOld, res)
			}
			wantOld = append(wantOld, "", "Done. Boot "+out+" in Dolphin to play multiplayer.")
			if !slices.Equal(old, wantOld) || co != len(wantOld)-2 {
				t.Errorf("output without the co-op line:\n%s\nwant\n%s", strings.Join(old, "\n"), strings.Join(wantOld, "\n"))
			}
			rec, recErr := tables.ReadLastPatched(dir)
			if c.tables {
				if !strings.Contains(lines[co], "stage tables built and cached") || recErr != nil || rec.Image != out {
					t.Errorf("co-op line %q, record %+v, %v", lines[co], rec, recErr)
				}
			} else if !strings.Contains(lines[co], "WARNING") || recErr == nil {
				t.Errorf("co-op line %q, record %+v, %v", lines[co], rec, recErr)
			}
			if !c.tables {
				return
			}

			// Patching the patched image: nothing to do, exit 0, and the
			// record now names that image.
			again := filepath.Join(t.TempDir(), "again.iso")
			stdout, code = runPatchCmd(t, out, again, dir)
			if code != 0 || !strings.Contains(stdout, "already contains this version") || !strings.Contains(stdout, "  co-op : stage tables already cached") {
				t.Fatalf("patching the patched image: exit %d\n%s", code, stdout)
			}
			if _, err := os.Stat(again); err == nil {
				t.Error("patching the patched image wrote an image")
			}
			if rec, err := tables.ReadLastPatched(dir); err != nil || rec.Image != out {
				t.Errorf("record after patching the patched image: %+v, %v", rec, err)
			}
		})
	}

	// A disc patched by another version still fails, without a co-op line.
	patched, err := inject.PatchISOInMemory(tablestest.PatchableISO(tablestest.Vanilla()))
	if err != nil {
		t.Fatal(err)
	}
	t2 := bytes.Index(patched, inject.T2Bytes)
	if t2 < 0 {
		t.Fatal("no T2 section in the patched image")
	}
	patched[t2+len(inject.T2Bytes)/2] ^= 0xFF
	stdout, code := runPatchCmd(t, writeImage(t, "other.iso", patched), filepath.Join(t.TempDir(), "o.iso"), t.TempDir())
	if code != 1 || !strings.Contains(stdout, "patched by a different ww-multiplayer version") || strings.Contains(stdout, "co-op") {
		t.Errorf("another version's image: exit %d\n%s", code, stdout)
	}
}

// The hub takes the session's verdict: hub.SwitchPolicy over it is the
// replica's policy too, so both sides agree on every memory switch.
func TestCoopStageTablesPolicyIsHubs(t *testing.T) {
	useTablesDir(t, t.TempDir())
	st, err := loadCoopStageTables(writeImage(t, "ww.iso", tablestest.ISO(tablestest.Vanilla())))
	if err != nil {
		t.Fatal(err)
	}
	if *st.policy() != *hub.SwitchPolicy(world.DefaultPolicy(), st.switchVerdict()) {
		t.Error("the replica's policy is not the hub's")
	}
}

// FuzzTakeCoopISO: on any command line takeCoopISO takes every --coop-iso
// before "--" and nothing else, in order, or fails.
func FuzzTakeCoopISO(f *testing.F) {
	for _, s := range []string{"Alice\x00--coop-iso\x00ww.iso", "--coop-iso=a\x00--\x00--coop-iso=b", "--coop-iso", "--coop-iso=\x00x", "a\x00b"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		args := strings.Split(s, "\x00")
		iso, rest, err := takeCoopISO(args)
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
			if a == coopISOFlag {
				if args[i+1] != iso {
					t.Fatalf("%q: took %q, not %q", args, iso, args[i+1])
				}
				args[i+1] = "\x00" // its value, taken with it
				continue
			}
			if v, ok := strings.CutPrefix(a, coopISOFlag+"="); ok {
				if v != iso {
					t.Fatalf("%q: took %q, not %q", args, iso, v)
				}
				continue
			}
			if a != "\x00" {
				kept = append(kept, a)
			}
		}
		if !slices.Equal(rest, kept) || slices.Contains(rest[:end], coopISOFlag) {
			t.Fatalf("%q: rest %q, want %q", strings.Split(s, "\x00"), rest, kept)
		}
	})
}
