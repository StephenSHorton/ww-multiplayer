package tables

import (
	"bytes"
	"crypto/sha256"
	"encoding"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata/stagetest"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/keys"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/switches"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/tablestest"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
)

// openISO opens a disc image built in memory, as OpenDisc opens a file.
func openISO(t testing.TB, img []byte) *inject.Disc {
	t.Helper()
	d, err := inject.NewDisc(bytes.NewReader(img), int64(len(img)))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func vanillaDisc(t testing.TB) *inject.Disc { return openISO(t, tablestest.ISO(tablestest.Vanilla())) }

func buildFixture(t testing.TB, stages []stagetest.Stage) *Tables {
	t.Helper()
	tb, err := Build(openISO(t, tablestest.ISO(stages)))
	if err != nil {
		t.Fatal(err)
	}
	return tb
}

func jsonOf(t testing.TB, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// replace returns stages with the stage named like s swapped for s.
func replace(stages []stagetest.Stage, s stagetest.Stage) []stagetest.Stage {
	out := slices.Clone(stages)
	for i := range out {
		if out[i].Name == s.Name {
			out[i] = s
			return out
		}
	}
	panic("no stage " + s.Name)
}

func TestBuildVanilla(t *testing.T) {
	tb := buildFixture(t, tablestest.Vanilla())
	// main.dol as the disc holds it, without the padding after its last
	// section that ReadDOL leaves out.
	dol, err := vanillaDisc(t).ReadDOL()
	if err != nil || len(dol) < 0x2000 || !bytes.HasPrefix(tablestest.DOL(), dol) {
		t.Fatalf("ReadDOL = %d bytes, %v", len(dol), err)
	}
	sum := sha256.Sum256(dol)
	if tb.Schema != SchemaVersion || tb.Builder != builderID() || tb.DOL != hex.EncodeToString(sum[:]) ||
		!validHash(tb.StageFiles) || tb.Stages != 11 || tb.Rooms != 12 {
		t.Fatalf("header = %d %q %+v, %d stages, %d rooms", tb.Schema, tb.Builder, tb.ID, tb.Stages, tb.Rooms)
	}
	var got []string
	for _, c := range tb.Acceptance() {
		got = append(got, fmt.Sprintf("%s %s %s: %s", c.Section, c.Status, c.Name, c.Detail))
	}
	want := []string{
		"keys PASS keys DRC: 4/4 (sources/doors), vanilla 4/4",
		"keys PASS keys FW: 1/1 (sources/doors), vanilla 1/1",
		"keys PASS keys TotG: 2/2 (sources/doors), vanilla 2/2",
		"keys PASS keys ET: 3/3 (sources/doors), vanilla 3/3",
		"keys PASS keys WT: 2/2 (sources/doors), vanilla 2/2",
		"keys PASS key doors outside tables 3-7: none",
		"hearts PASS heart pieces: 44, vanilla 44: 30 in stage files, 11 rewards, 3 unverified rewards",
		"hearts PASS heart containers: 6, vanilla 6",
		"switches PASS push-block exclusion list: 1 push block(s) storing a path in 2 switch use(s), never synced",
		"enemies INFO enemy setID share: 9 enemy entries: 3 (33.3%) with a setID, 3 (33.3%) keyed (0 of them never dead-checked); K1 classes not BOSS: 3 (75.0%) keyed of 4",
		"enemies INFO duplicate (stage, room, setID) keys: 1: 1 with an enemy, 1 with two enemies on a common layer, 1 colliding in one game, 0 on different layers only",
	}
	if !slices.Equal(got, want) {
		t.Errorf("acceptance\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !tb.Keys.Pass || !tb.Hearts.Pass {
		t.Errorf("keys pass %v, hearts pass %v:\n%s", tb.Keys.Pass, tb.Hearts.Pass, tb.Report())
	}
	if s, ok := tb.Switches.Lookup(swKey(keys.TableDRC, 0x20)); !ok || !s.Synced {
		t.Errorf("DRC's crystal switch 0x20 = %+v, want synced", s)
	}
	for _, no := range []int{0x30, 0x31} {
		if tb.Switches.Synced(swKey(keys.TableDRC, no)) {
			t.Errorf("push-block path switch 0x%02X is synced", no)
		}
	}
	if es := tb.Enemies.Lookup("M_NewD2", 1, 1); len(es) != 2 || !es[0].Shared {
		t.Errorf("M_NewD2 room 1 setID 1 = %+v, want two shared Bokoblins", es)
	}
}

// TestAcceptanceFails breaks each acceptance count in turn.
func TestAcceptanceFails(t *testing.T) {
	v := tablestest.Vanilla()
	doors := func(n int) []stagetest.Actor {
		var d []stagetest.Actor
		for i := range n {
			d = append(d, tablestest.KeyDoor(0x10+uint32(i), i, i+1))
		}
		return d
	}
	var pieces []stagetest.Actor
	for bit := range uint32(29) {
		pieces = append(pieces, tablestest.PieceChest(bit))
	}
	for _, c := range []struct {
		name   string
		stages []stagetest.Stage
		check  string // the check that fails
		detail string
	}{
		{"a DRC key short",
			replace(v, tablestest.Stage("M_NewD2", keys.TableDRC, stagedata.StageDungeon, nil, doors(4), tablestest.Room(1,
				tablestest.KeyChest(1), tablestest.KeyChest(2), tablestest.KeyChest(3), tablestest.PushBlock(1, 0x30, 0x31)))),
			"keys DRC", "3/4 (sources/doors), vanilla 4/4; 2 problem(s) in the keys section"},
		{"a key door on the sea",
			replace(v, tablestest.Stage("sea", 0, stagedata.StageSea, nil, []stagetest.Actor{tablestest.KeyDoor(0x05, 1, 2)},
				tablestest.Room(1, append(slices.Clone(pieces), tablestest.PieceChest(29), tablestest.Bitem(2))...))),
			"key doors outside tables 3-7", "1 (table 0: 1)"},
		{"a heart piece short",
			replace(v, tablestest.Stage("sea", 0, stagedata.StageSea, nil, nil, tablestest.Room(1, append(slices.Clone(pieces), tablestest.Bitem(2))...))),
			"heart pieces", "43, vanilla 44: 29 in stage files, 11 rewards, 3 unverified rewards"},
		{"Molgera missing",
			replace(v, tablestest.Stage("kazeB", keys.TableWT, stagedata.StageDungeon, nil, nil)),
			"heart containers", "5, vanilla 6; 1 problem(s) in the hearts section"},
		{"no push block",
			replace(v, tablestest.Dungeon("M_NewD2", keys.TableDRC)),
			"push-block exclusion list", "0 push block(s) storing a path in 0 switch use(s), never synced"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var failed []string
			for _, ch := range buildFixture(t, c.stages).Acceptance() {
				if ch.Status == Fail {
					failed = append(failed, ch.Name+": "+ch.Detail)
				}
			}
			if want := []string{c.check + ": " + c.detail}; !slices.Equal(failed, want) {
				t.Errorf("failed checks %q, want %q", failed, want)
			}
		})
	}
}

// swKey is memory switch no of save table table.
func swKey(table, no int) switches.Key {
	return switches.Key{Scope: switches.Scope{Store: switches.StoreMemory, Table: table, Room: -1}, No: no}
}

// recordingSource records every path read.
type recordingSource struct {
	Source
	mu    sync.Mutex
	paths []string
}

func (r *recordingSource) ReadFile(path string) ([]byte, error) {
	r.mu.Lock()
	r.paths = append(r.paths, path)
	r.mu.Unlock()
	return r.Source.ReadFile(path)
}

// TestReadsOnlyStages checks that Get reads main.dol and the stage
// archives, and nothing else.
func TestReadsOnlyStages(t *testing.T) {
	d := vanillaDisc(t)
	src := &recordingSource{Source: d}
	tb, st, err := Get(src, "")
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	var size int64
	for _, f := range d.Files() {
		if strings.HasPrefix(f.Path, "res/Stage/") {
			want = append(want, f.Path)
			size += int64(f.Size)
		}
	}
	slices.Sort(src.paths)
	slices.Sort(want)
	if !slices.Equal(src.paths, want) || len(want) != tb.Stages+tb.Rooms {
		t.Errorf("read %q, want the %d stage archives %q", src.paths, tb.Stages+tb.Rooms, want)
	}
	if st.Files != len(want) || st.Bytes != size || st.Cached || st.Path != "" || st.Why != "" || st.SaveErr != nil {
		t.Errorf("stats %+v, want %d files of %d bytes, no cache", st, len(want), size)
	}
}

// ciso packs img as a CISO, the layout inject's ciso.go reads: "CISO", a
// little-endian block size, and from 0x08 one byte per block, 1 for a
// block the file stores (all-zero blocks left out), the stored blocks
// following the 0x8000-byte header in order.
func ciso(img []byte) []byte {
	const header, table, block = 0x8000, 0x08, 0x8000
	out := make([]byte, header)
	copy(out, "CISO")
	binary.LittleEndian.PutUint32(out[4:], block)
	for i := 0; i*block < len(img); i++ {
		b := img[i*block : min((i+1)*block, len(img))]
		if bytes.Count(b, []byte{0}) == len(b) {
			continue
		}
		out[table+i] = 1
		out = append(out, b...)
		out = append(out, make([]byte, block-len(b))...)
	}
	return out
}

// TestCISOMatchesISO checks that a CISO of a disc gives the tables its ISO
// gives, cache key included.
func TestCISOMatchesISO(t *testing.T) {
	img := tablestest.ISO(tablestest.Vanilla())
	fromISO := buildFixture(t, tablestest.Vanilla())
	fromCISO, err := Build(openISO(t, ciso(img)))
	if err != nil {
		t.Fatal(err)
	}
	if jsonOf(t, fromCISO) != jsonOf(t, fromISO) {
		t.Error("the CISO's tables differ from the ISO's")
	}
}

// sparseImage reads as img followed by zeros to size bytes, as a sparse
// file or a CISO's absent blocks do.
type sparseImage struct {
	img  []byte
	size int64
}

func (s sparseImage) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off >= s.size {
		return 0, io.EOF
	}
	end := min(int64(len(p)), s.size-off)
	n := 0
	if off < int64(len(s.img)) {
		n = copy(p[:end], s.img[off:])
	}
	clear(p[n:end])
	if end < int64(len(p)) {
		return int(end), io.EOF
	}
	return int(end), nil
}

// TestBuildRefusesHugeArchives opens a 112 KiB image whose FST lists
// every Stage.arc as running to the end of a 1.4 GB disc. Read as listed,
// each would be allocated whole, several at once; Build must refuse them
// unread.
func TestBuildRefusesHugeArchives(t *testing.T) {
	img := tablestest.ISO(tablestest.Vanilla())
	const discSize = 0x57058000 // a full GameCube disc (inject's gcDiscSize)
	be := binary.BigEndian
	fst := int(be.Uint32(img[0x424:])) // DVDBB2's FST offset (inject's iso.go)
	n := int(be.Uint32(img[fst+8:]))   // the root's next index: the entry count
	strs, patched := fst+n*0xC, 0
	for i := 1; i < n; i++ {
		e := img[fst+i*0xC:]
		name := img[strs+int(be.Uint32(e)&0xFFFFFF):]
		if e[0] == 0 && string(name[:bytes.IndexByte(name, 0)]) == "Stage.arc" {
			be.PutUint32(e[8:], discSize-be.Uint32(e[4:]))
			patched++
		}
	}
	if patched == 0 {
		t.Fatal("no Stage.arc in the FST")
	}
	d, err := inject.NewDisc(sparseImage{img, discSize}, discSize)
	if err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err = Build(d)
	runtime.ReadMemStats(&after)
	if err == nil || !strings.Contains(err.Error(), "Stage.arc: the disc lists") || !strings.Contains(err.Error(), "more than the") {
		t.Fatalf("err = %v, want the Stage.arc refused", err)
	}
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 64<<20 {
		t.Errorf("Build allocated %d MiB before refusing", alloc>>20)
	}
}

func TestBuildErrors(t *testing.T) {
	bad := new(stagetest.Disc)
	bad.Add("res/Stage/bad/Stage.arc", []byte("not an archive"))
	for _, c := range []struct {
		name string
		img  []byte
		want string
	}{
		{"no stages", tablestest.Disc(nil).ISO(tablestest.DOL()), "the disc has no stages"},
		{"not the GZLE01 DOL", tablestest.Disc(tablestest.Vanilla()).ISO(stagetest.DOL(0x80300000, make([]byte, 0x100))), "no l_objectName"},
		{"a broken archive", bad.ISO(tablestest.DOL()), "res/Stage/bad/Stage.arc"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := Build(openISO(t, c.img))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want one containing %q", err, c.want)
			}
			if _, _, err := Get(openISO(t, c.img), t.TempDir()); err == nil {
				t.Fatal("Get succeeded")
			}
		})
	}
}

func TestSaveLoad(t *testing.T) {
	dir := t.TempDir()
	tb := buildFixture(t, tablestest.Vanilla())
	path, err := tb.Save(dir)
	if err != nil {
		t.Fatal(err)
	}
	if path != CacheFile(dir, tb.DOL) {
		t.Errorf("saved to %s, want %s", path, CacheFile(dir, tb.DOL))
	}
	got, err := Load(dir, tb.ID)
	if err != nil {
		t.Fatal(err)
	}
	if a, b := jsonOf(t, got), jsonOf(t, tb); a != b {
		t.Errorf("loaded tables differ:\n%s\nwant\n%s", a, b)
	}
	if a, b := got.Report(), tb.Report(); a != b {
		t.Errorf("loaded report differs:\n%s\nwant\n%s", a, b)
	}
	ents, err := os.ReadDir(dir)
	if err != nil || len(ents) != 1 {
		t.Errorf("cache dir holds %v (%v), want the one file", ents, err)
	}
	if _, err := (&Tables{ID: tb.ID}).Save(dir); err == nil {
		t.Error("saved tables without sections")
	}
	if _, err := (&Tables{ID: ID{DOL: "../escape"}, Keys: tb.Keys, Hearts: tb.Hearts, Switches: tb.Switches, Enemies: tb.Enemies}).Save(dir); err == nil {
		t.Error("saved under a name that isn't a hash")
	}
	for _, id := range []ID{{DOL: "../escape"}, {DOL: strings.ToUpper(tb.DOL)}, {DOL: tb.DOL[:63]}} {
		if _, err := Load(dir, id); err == nil {
			t.Errorf("Load(%q) succeeded", id.DOL)
		}
	}
}

func TestGet(t *testing.T) {
	dir := t.TempDir()
	d := vanillaDisc(t)
	first, st, err := Get(d, dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Cached || st.Why != "no cache file yet" || st.SaveErr != nil || st.Path != CacheFile(dir, first.DOL) || st.Files == 0 {
		t.Errorf("first Get: %+v", st)
	}
	second, st, err := Get(d, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Cached || st.Why != "" || st.Files != 0 || st.Path != CacheFile(dir, first.DOL) {
		t.Errorf("second Get: %+v", st)
	}
	if jsonOf(t, first) != jsonOf(t, second) {
		t.Error("the cached tables differ from the built ones")
	}

	// Another disc with the same DOL: the stage listing differs, so the
	// cache is rebuilt over.
	other := openISO(t, tablestest.ISO(replace(tablestest.Vanilla(), tablestest.Dungeon("M_NewD2", keys.TableDRC))))
	third, st, err := Get(other, dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Cached || !strings.Contains(st.Why, "stage files changed") || third.DOL != first.DOL || len(third.Switches.PushBlocks) != 0 {
		t.Errorf("Get on a changed disc: %+v", st)
	}
	if _, err := Load(dir, first.ID); !errors.Is(err, ErrStale) {
		t.Errorf("Load of the first disc's ID after the rebuild = %v, want ErrStale", err)
	}
}

// TestGetRebuilds damages the cache file every way Load refuses and checks
// that Get rebuilds and rewrites it.
func TestGetRebuilds(t *testing.T) {
	d := vanillaDisc(t)
	good := buildFixture(t, tablestest.Vanilla())
	goodJSON := []byte(jsonOf(t, good))
	edit := func(f func(m map[string]json.RawMessage)) []byte {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(goodJSON, &m); err != nil {
			t.Fatal(err)
		}
		f(m)
		return []byte(jsonOf(t, m))
	}
	other := strings.Repeat("0", 64)
	// padded is the good file with trailing spaces to n bytes: still valid
	// JSON, so only the size cap refuses it.
	padded := func(n int) []byte {
		return append(slices.Clone(goodJSON), bytes.Repeat([]byte(" "), n-len(goodJSON))...)
	}
	for _, c := range []struct {
		name string
		file []byte
		want error
		why  string
	}{
		{"empty", nil, ErrCorrupt, "unexpected end of JSON input"},
		{"garbage", []byte("GZLE01 not json"), ErrCorrupt, "invalid character"},
		{"truncated", goodJSON[:len(goodJSON)/2], ErrCorrupt, "unexpected end"},
		{"an array", []byte("[1,2,3]"), ErrCorrupt, "cannot unmarshal array"},
		{"an old schema", edit(func(m map[string]json.RawMessage) { m["schema"] = json.RawMessage("0") }), ErrStale, fmt.Sprintf("schema 0, this binary writes %d", SchemaVersion)},
		{"another binary", edit(func(m map[string]json.RawMessage) { m["builder"] = json.RawMessage(`"v0.0.1"`) }), ErrStale, `built by "v0.0.1"`},
		{"another DOL", edit(func(m map[string]json.RawMessage) { m["dol_sha256"] = json.RawMessage(`"` + other + `"`) }), ErrStale, "for main.dol 000000000000"},
		{"other stage files", edit(func(m map[string]json.RawMessage) { m["stage_files_sha256"] = json.RawMessage(`"` + other + `"`) }), ErrStale, "stage files changed"},
		{"an unknown field", edit(func(m map[string]json.RawMessage) { m["extra"] = json.RawMessage("1") }), ErrCorrupt, `unknown field "extra"`},
		{"a section missing", edit(func(m map[string]json.RawMessage) { delete(m, "enemies") }), ErrCorrupt, "a section is missing"},
		{"a null section", edit(func(m map[string]json.RawMessage) { m["keys"] = json.RawMessage("null") }), ErrCorrupt, "a section is missing"},
		{"a bad field", edit(func(m map[string]json.RawMessage) { m["rooms"] = json.RawMessage(`"twelve"`) }), ErrCorrupt, "cannot unmarshal string"},
		{"data after it", append(slices.Clone(goodJSON), "{}"...), ErrCorrupt, "after top-level value"},
		{"over the size cap", padded(maxCacheSize + 1), ErrCorrupt, fmt.Sprintf("more than %d bytes", maxCacheSize)},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := CacheFile(dir, good.DOL)
			if err := os.WriteFile(path, c.file, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(dir, good.ID); !errors.Is(err, c.want) || !strings.Contains(err.Error(), c.why) {
				t.Fatalf("Load = %v, want %v containing %q", err, c.want, c.why)
			}
			tb, st, err := Get(d, dir)
			if err != nil {
				t.Fatal(err)
			}
			if st.Cached || !strings.Contains(st.Why, c.why) || st.SaveErr != nil || jsonOf(t, tb) != string(goodJSON) {
				t.Errorf("Get: %+v", st)
			}
			if _, err := Load(dir, good.ID); err != nil {
				t.Errorf("the rebuilt cache doesn't load: %v", err)
			}
		})
	}
	// At the cap, the same padding loads.
	dir := t.TempDir()
	if err := os.WriteFile(CacheFile(dir, good.DOL), padded(maxCacheSize), 0o644); err != nil {
		t.Fatal(err)
	}
	if tb, err := Load(dir, good.ID); err != nil || jsonOf(t, tb) != string(goodJSON) {
		t.Errorf("a file at the size cap: %v", err)
	}
}

// TestGetSaveFails checks that a cache that can't be written leaves the
// tables returned and says why.
func TestGetSaveFails(t *testing.T) {
	d := vanillaDisc(t)
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	tb, st, err := Get(d, file)
	if err != nil || tb == nil || st.SaveErr == nil || st.Cached {
		t.Errorf("Get into a file: %v, %+v", err, st)
	}

	// A directory where the cache file goes: Load can't read it and Save
	// can't rename over it, and neither is left behind.
	dir := t.TempDir()
	if err := os.Mkdir(CacheFile(dir, tb.DOL), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, st, err = Get(d, dir); err != nil || st.Cached || st.SaveErr == nil {
		t.Errorf("Get over a directory: %v, %+v", err, st)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Errorf("cache dir holds %v, want only the directory", ents)
	}
}

// TestGetConcurrent runs Gets at once on one cache: each gets whole
// tables, and the file left is whole.
func TestGetConcurrent(t *testing.T) {
	d := vanillaDisc(t)
	dir := t.TempDir()
	want := jsonOf(t, buildFixture(t, tablestest.Vanilla()))
	var wg sync.WaitGroup
	errs := make([]error, 6)
	for i := range errs {
		wg.Go(func() {
			tb, st, err := Get(d, dir)
			switch {
			case err != nil:
				errs[i] = err
			case st.SaveErr != nil:
				errs[i] = st.SaveErr
			case jsonOf(t, tb) != want:
				errs[i] = errors.New("tables differ")
			}
		})
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	tb, err := Load(dir, buildFixture(t, tablestest.Vanilla()).ID)
	if err != nil || jsonOf(t, tb) != want {
		t.Errorf("the cache left doesn't load: %v", err)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Errorf("cache dir holds %v, want the one file", ents)
	}
}

// fakeRename fails its first n calls with err, then succeeds.
type fakeRename struct {
	n, calls int
	err      error
}

func (f *fakeRename) rename(oldpath, newpath string) error {
	f.calls++
	if f.calls <= f.n {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: f.err}
	}
	return nil
}

// TestRetryRename checks the Windows rename retries Save makes: a sharing
// error is ridden out, retried for about 2 s, and any other error is not.
func TestRetryRename(t *testing.T) {
	for _, c := range []struct {
		name  string
		fails int
		err   syscall.Errno
		calls int
		ok    bool
	}{
		{"a sharing violation passes", 3, winErrorSharingViolation, 4, true},
		{"access stays denied", 1 << 30, winErrorAccessDenied, -1, false},
		{"another error", 1 << 30, syscall.Errno(2), 1, false}, // ERROR_FILE_NOT_FOUND / ENOENT
	} {
		t.Run(c.name, func(t *testing.T) {
			fr := &fakeRename{n: c.fails, err: c.err}
			var sleeps []time.Duration
			err := retryRename(fr.rename, "a.tmp", "a.json", isWindowsSharingError, func(d time.Duration) { sleeps = append(sleeps, d) })
			if c.ok != (err == nil) || (!c.ok && !errors.Is(err, c.err)) {
				t.Fatalf("err = %v", err)
			}
			if (c.calls >= 0 && fr.calls != c.calls) || fr.calls != len(sleeps)+1 {
				t.Errorf("%d calls, %d sleeps", fr.calls, len(sleeps))
			}
			var total time.Duration
			for i, d := range sleeps {
				if want := min(renameRetryFirst<<i, renameRetryMaxStep); d != want {
					t.Errorf("sleep %d = %v, want %v", i, d, want)
				}
				total += d
			}
			if c.calls < 0 && (total > renameRetryBudget || total+renameRetryMaxStep <= renameRetryBudget) {
				t.Errorf("slept %v in all, want just under %v", total, renameRetryBudget)
			}
		})
	}
	for _, c := range []struct {
		err  error
		want bool
	}{
		{&os.LinkError{Op: "rename", Err: winErrorAccessDenied}, true},
		{fmt.Errorf("wrapped: %w", &os.LinkError{Op: "rename", Err: winErrorSharingViolation}), true},
		{&os.LinkError{Op: "rename", Err: syscall.Errno(2)}, false},
		{errors.New("access is denied"), false},
		{nil, false},
	} {
		if got := isWindowsSharingError(c.err); got != c.want {
			t.Errorf("isWindowsSharingError(%v) = %v", c.err, got)
		}
	}
	// Errnos 5 and 32 are EIO and EPIPE on Unix, which aren't retried.
	err := &os.LinkError{Op: "rename", Err: winErrorSharingViolation}
	if got, want := renameErrIsTransient(err), runtime.GOOS == "windows"; got != want {
		t.Errorf("renameErrIsTransient(errno 32) on %s = %v, want %v", runtime.GOOS, got, want)
	}
}

// TestSaveRenameFails: when the rename fails for good, Save succeeds only
// if the file already there is whole tables for the same disc (another
// run's), and leaves no temporary file either way.
func TestSaveRenameFails(t *testing.T) {
	tb := buildFixture(t, tablestest.Vanilla())
	dir := t.TempDir()
	path, err := tb.Save(dir)
	if err != nil {
		t.Fatal(err)
	}
	orig := replaceCache
	t.Cleanup(func() { replaceCache = orig })
	held := &os.LinkError{Op: "rename", Err: winErrorSharingViolation}
	replaceCache = func(string, string) error { return held }
	only := func(when string) {
		t.Helper()
		if ents, _ := os.ReadDir(dir); len(ents) != 1 || ents[0].Name() != filepath.Base(path) {
			t.Errorf("%s: cache dir holds %v, want only %s", when, ents, filepath.Base(path))
		}
	}

	if _, err := tb.Save(dir); err != nil {
		t.Errorf("over another run's whole tables: %v", err)
	}
	only("over whole tables")
	if loaded, err := Load(dir, tb.ID); err != nil || jsonOf(t, loaded) != jsonOf(t, tb) {
		t.Errorf("the tables left: %v", err)
	}

	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tb.Save(dir); !errors.Is(err, held) {
		t.Errorf("over a corrupt file: %v, want the rename's error", err)
	}
	only("over a corrupt file")

	other := filepath.Join(t.TempDir(), "empty")
	if _, err := tb.Save(other); !errors.Is(err, held) {
		t.Errorf("into an empty dir: %v, want the rename's error", err)
	}
	if ents, _ := os.ReadDir(other); len(ents) != 0 {
		t.Errorf("into an empty dir: left %v", ents)
	}
}

func TestStageFilesHash(t *testing.T) {
	base := []inject.DiscFile{
		{Path: "res/Object/Link.arc", Offset: 0x100000, Size: 0x4000},
		{Path: "res/Stage/sea/Stage.arc", Offset: 0x200000, Size: 0x800},
		{Path: "res/Stage/sea/Room1.arc", Offset: 0x300000, Size: 0x900},
	}
	with := func(f func(fs []inject.DiscFile)) []inject.DiscFile {
		c := slices.Clone(base)
		f(c)
		return c
	}
	h := stageFilesHash(base)
	for _, c := range []struct {
		name  string
		files []inject.DiscFile
		same  bool
	}{
		{"moved", with(func(fs []inject.DiscFile) { fs[1].Offset, fs[2].Offset = 0x400000, 0x500000 }), true},
		{"another object", with(func(fs []inject.DiscFile) { fs[0].Size = 1 }), true},
		{"a leading slash", with(func(fs []inject.DiscFile) { fs[1].Path = "/" + fs[1].Path }), true},
		{"a room resized", with(func(fs []inject.DiscFile) { fs[2].Size++ }), false},
		{"a room renamed", with(func(fs []inject.DiscFile) { fs[2].Path = "res/Stage/sea/Room2.arc" }), false},
		{"another spelling", with(func(fs []inject.DiscFile) { fs[2].Path = "RES/STAGE/sea/Room1.arc" }), false},
		{"a room more", append(slices.Clone(base), inject.DiscFile{Path: "res/Stage/sea/Room2.arc", Size: 1}), false},
		{"reordered", []inject.DiscFile{base[0], base[2], base[1]}, false},
		{"no stages", base[:1], false},
	} {
		if got := stageFilesHash(c.files) == h; got != c.same {
			t.Errorf("%s: same hash %v, want %v", c.name, got, c.same)
		}
	}
	if stageFilesHash(base[:1]) != stageFilesHash(nil) {
		t.Error("a listing without stages hashes unlike an empty one")
	}

	// The in-memory disc and its image agree.
	v := tablestest.Vanilla()
	id, err := Identify(openISO(t, tablestest.ISO(v)))
	if err != nil {
		t.Fatal(err)
	}
	if id.StageFiles != stageFilesHash(tablestest.Disc(v).Files()) {
		t.Error("the image's stage listing hashes unlike the disc it was built from")
	}
}

func TestReportSections(t *testing.T) {
	tb := buildFixture(t, tablestest.Vanilla())
	all := tb.Report()
	var at []int
	for _, s := range Sections {
		i := strings.Index(all, "\n== "+string(s)+" ==\n")
		if i < 0 {
			t.Fatalf("report has no %s section:\n%s", s, all)
		}
		at = append(at, i)
	}
	if !slices.IsSorted(at) {
		t.Errorf("sections at %v, want them in Sections order", at)
	}
	if !strings.HasPrefix(all, fmt.Sprintf("Co-op tables (docs/08-coop-design.md §9 M1), schema %d, built by ", SchemaVersion)+builderID()+"\n  main.dol SHA-256 "+tb.DOL+"\n") ||
		!strings.Contains(all, "stage files SHA-256 "+tb.StageFiles+": 11 stages, 12 rooms\n") {
		t.Errorf("report header:\n%s", all[:min(len(all), 400)])
	}
	if want := tb.Report(Sections...); all != want {
		t.Error("Report() differs from Report(Sections...)")
	}

	keysOnly := tb.Report(SectionKeys)
	for _, s := range []string{"== keys ==", "PASS  keys DRC", "PASS  key doors outside tables 3-7", tb.Keys.Report()} {
		if !strings.Contains(keysOnly, s) {
			t.Errorf("Report(keys) lacks %q", s)
		}
	}
	for _, s := range []string{"== hearts ==", "== switches ==", "== enemies ==", "heart pieces", "push-block", "setID share"} {
		if strings.Contains(keysOnly, s) {
			t.Errorf("Report(keys) has %q", s)
		}
	}

	// Only keeps the header and one section; the others are left out of
	// both the report and the JSON.
	h := tb.Only(SectionHearts)
	if h.Hearts != tb.Hearts || h.Keys != nil || h.Switches != nil || h.Enemies != nil || h.ID != tb.ID || h.complete() {
		t.Errorf("Only(hearts) = %+v", h)
	}
	if r := h.Report(); !strings.Contains(r, "== hearts ==") || strings.Contains(r, "== keys ==") || strings.Contains(r, "keys DRC") {
		t.Errorf("Only(hearts).Report():\n%s", r)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(jsonOf(t, h)), &m); err != nil {
		t.Fatal(err)
	}
	var fields []string
	for k := range m {
		fields = append(fields, k)
	}
	slices.Sort(fields)
	if want := []string{"builder", "dol_sha256", "hearts", "rooms", "schema", "stage_files_sha256", "stages"}; !slices.Equal(fields, want) {
		t.Errorf("Only(hearts) JSON fields %q, want %q", fields, want)
	}
}

func TestParseSection(t *testing.T) {
	for _, s := range Sections {
		if got, err := ParseSection(string(s)); err != nil || got != s {
			t.Errorf("ParseSection(%q) = %q, %v", s, got, err)
		}
	}
	for _, s := range []string{"", "Keys", "key", "all"} {
		if _, err := ParseSection(s); err == nil || !strings.Contains(err.Error(), "keys, hearts, switches, enemies") {
			t.Errorf("ParseSection(%q) = %v", s, err)
		}
	}
}

// schemaShapes pins the JSON shape (shape) of each SchemaVersion. A
// change to any table's JSON fails TestSchemaShape until SchemaVersion is
// bumped and the new shape's hash added here; old entries stay, as a
// record.
var schemaShapes = map[int]string{
	1: "956b4fe0f1e015e6d4b7ec823c3131f32cc331f0c9c7f26d88bc91e090b50735",
	// 2: the same shape; grass no longer places items, and salvage and
	// item bits the game halts on are flagged "halt" and "bad".
	2: "956b4fe0f1e015e6d4b7ec823c3131f32cc331f0c9c7f26d88bc91e090b50735",
}

func TestSchemaShape(t *testing.T) {
	sum := sha256.Sum256([]byte(shape(reflect.TypeFor[Tables](), nil)))
	got := hex.EncodeToString(sum[:])
	if want := schemaShapes[SchemaVersion]; got != want {
		t.Errorf("the cache's JSON shape is %s, not schema %d's %q: bump SchemaVersion (tables.go) so old caches are rebuilt, and add\n\t%d: %q,\nto schemaShapes",
			got, SchemaVersion, want, SchemaVersion+1, got)
	}
}

var (
	jsonMarshaler = reflect.TypeFor[json.Marshaler]()
	textMarshaler = reflect.TypeFor[encoding.TextMarshaler]()
)

// shape describes how encoding/json writes a value of type t: each struct
// field's JSON name and options and the shape of its type, and the name
// of every type with its own marshaler, whose encoding the type decides.
func shape(t reflect.Type, seen []reflect.Type) string {
	if t.Implements(jsonMarshaler) || t.Implements(textMarshaler) {
		return t.String() + "(own)"
	}
	switch t.Kind() {
	case reflect.Pointer:
		return "*" + shape(t.Elem(), seen)
	case reflect.Slice:
		return "[]" + shape(t.Elem(), seen)
	case reflect.Array:
		return fmt.Sprintf("[%d]", t.Len()) + shape(t.Elem(), seen)
	case reflect.Map:
		return "map[" + shape(t.Key(), seen) + "]" + shape(t.Elem(), seen)
	case reflect.Struct:
		if slices.Contains(seen, t) {
			return t.String() + "(cycle)"
		}
		seen = append(seen, t)
		var b strings.Builder
		b.WriteString("{")
		for i := range t.NumField() {
			f := t.Field(i)
			tag := f.Tag.Get("json")
			if !f.IsExported() && !f.Anonymous || tag == "-" {
				continue
			}
			name := tag
			if name == "" {
				name = f.Name
				if f.Anonymous {
					name = "(embedded)"
				}
			}
			fmt.Fprintf(&b, "%s:%s;", name, shape(f.Type, seen))
		}
		return b.String() + "}"
	}
	return t.Kind().String()
}
