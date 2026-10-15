package backup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// A fake Dolphin user directory, built in a temp dir: what the tests put
// in it, and where.
type userDir struct {
	t    *testing.T
	root string
}

func newUserDir(t *testing.T) *userDir {
	t.Helper()
	return &userDir{t: t, root: filepath.Join(t.TempDir(), "Dolphin Emulator")}
}

func (u *userDir) path(rel string) string { return filepath.Join(u.root, filepath.FromSlash(rel)) }

func (u *userDir) write(rel string, data []byte) string {
	u.t.Helper()
	p := u.path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		u.t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		u.t.Fatal(err)
	}
	return p
}

// gci is a GCI file for game code code: the 0x40-byte header, then data.
func gci(code string, data string) []byte {
	h := make([]byte, 0x40)
	copy(h, code+"01")
	copy(h[8:], "gczelda")
	return append(h, data...)
}

// ini writes Config/Dolphin.ini with the given [Core] lines.
func (u *userDir) ini(core ...string) {
	u.t.Helper()
	u.write("Config/Dolphin.ini", []byte("[General]\nISOPath0 = C:\\Roms\n[Core]\n"+strings.Join(core, "\n")+"\n[DSP]\nVolume = 100\n"))
}

func read(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// clock is a fake Options.Now: each call is one second after the last.
func clock(start time.Time) func() time.Time {
	t := start.Add(-time.Second)
	return func() time.Time { t = t.Add(time.Second); return t }
}

var day0 = time.Date(2026, 10, 14, 15, 30, 12, 0, time.UTC)

func TestDefaultUserDirs(t *testing.T) {
	cases := []struct{ goos, first, second string }{
		{"windows", filepath.Join("cfg", "Dolphin Emulator"), filepath.Join("cfg", "Dolphin Emulator 2")},
		{"darwin", filepath.Join("cfg", "Dolphin"), filepath.Join("cfg", "Dolphin 2")},
		{"linux", filepath.Join("cfg", "dolphin-emu"), filepath.Join("cfg", "dolphin-emu-2")},
	}
	for _, c := range cases {
		if a, b := defaultUserDirs(c.goos, "cfg"); a != c.first || b != c.second {
			t.Errorf("%s: %q %q, want %q %q", c.goos, a, b, c.first, c.second)
		}
	}
	t.Setenv("USER_DIR_1", "/one")
	t.Setenv("USER_DIR_2", "")
	a, b := UserDirs()
	if _, def2 := DefaultUserDirs(); a != "/one" || b != def2 {
		t.Errorf("UserDirs = %q %q with USER_DIR_1 set", a, b)
	}
}

func TestParseIni(t *testing.T) {
	src := "\ufeff# comment\n[Core]\n  SlotA = 8 \nMemcardAPath=\"C:\\Cards\\A.raw\"\n; another\n[core]\nGCIFolderBPath = /x/y\nnot a setting\n[Other]\nSlotA = 1\n"
	got := parseIni(strings.NewReader(src))
	want := map[string]string{"slota": "8", "memcardapath": `C:\Cards\A.raw`, "gcifolderbpath": "/x/y"}
	if fmt.Sprint(got["core"]) != fmt.Sprint(want) || got["other"]["slota"] != "1" {
		t.Errorf("parseIni = %v", got)
	}
}

// TestParseDevice: SlotA / SlotB are read as Dolphin's TryParse reads
// them, with strtoll and base 0 over the whole value. Each case gives
// what TryParse's code gives when built with g++ against glibc 2.39.
func TestParseDevice(t *testing.T) {
	for _, c := range []struct {
		v  string
		n  int
		ok bool
	}{
		{"8", 8, true}, {"0x8", 8, true}, {"0X8", 8, true}, {"010", 8, true},
		{"0b1000", 8, true}, {"-0b1000", -8, true}, {"+8", 8, true}, {"-1", -1, true},
		{"-0x8", -8, true}, {"  8", 8, true}, {"\t8", 8, true},
		{"", 0, true}, {"0", 0, true}, {"-0", 0, true},
		{"2147483647", 2147483647, true}, {"-2147483648", -2147483648, true},
		{"08", 0, false}, {"0x", 0, false}, {"0x_8", 0, false}, {" ", 0, false},
		{"8 ", 0, false}, {"+", 0, false}, {"+-8", 0, false}, {"0o10", 0, false},
		{"-0o10", 0, false}, {"1_0", 0, false}, {"2147483648", 0, false},
		{"99999999999999999999", 0, false}, {"junk", 0, false}, {"8 cards", 0, false},
	} {
		if n, ok := parseDevice(c.v); ok != c.ok || ok && n != c.n {
			t.Errorf("parseDevice(%q) = %d, %v, want %d, %v", c.v, n, ok, c.n, c.ok)
		}
	}
}

// TestLocate: the default raw and GCI cards, the ones Dolphin.ini
// configures, and which the slots select, Dolphin.ini's SlotA / SlotB or
// Dolphin's defaults, and Dolphin.ini's paths over the default ones.
func TestLocate(t *testing.T) {
	u := newUserDir(t)
	custom := filepath.Join(t.TempDir(), "cards")
	rawA := u.write("GC/MemoryCardA.USA.raw", []byte("card A"))
	rawA2 := u.write("GC/MemoryCardA.EUR.raw", []byte("card A, PAL"))
	u.write("GC/MemoryCardA.USA.raw.bak", []byte("not a card"))
	u.write("GC/USA/Card A/01-GZLE-gczelda.gci", gci("GZLE", "link"))
	customRaw := filepath.Join(custom, "MyCard.USA.251.raw")
	writeFile(t, customRaw, "card B")
	customGCI := filepath.Join(custom, "gci", "USA")
	writeFile(t, filepath.Join(customGCI, "01-GZLE-gczelda.gci"), string(gci("GZLE", "tetra")))
	u.ini("SlotA = 8", "SlotB = 1",
		"MemcardBPath = "+filepath.Join(custom, "MyCard.EUR.raw"),
		"GCIFolderBPathOverride = "+filepath.Join(custom, "gci"))

	located := func() []string {
		t.Helper()
		cards, err := Locate(u.root)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, c := range cards {
			got = append(got, fmt.Sprintf("%s %s %s %s %q", c.Slot, c.Kind, c.Path, c.InUse, c.From))
		}
		return got
	}
	got := located()
	want := []string{
		fmt.Sprintf("A raw card %s no %q", rawA2, "default"),
		fmt.Sprintf("A raw card %s no %q", rawA, "default"),
		fmt.Sprintf("A GCI folder %s yes %q", u.path("GC/USA/Card A"), "default"),
		fmt.Sprintf("B raw card %s yes %q", customRaw, "Dolphin.ini [Core] MemcardBPath"),
		// The configured folder as given, and the region folder Dolphin
		// appends, which holds the save.
		fmt.Sprintf("B GCI folder %s no %q", filepath.Join(custom, "gci"), "Dolphin.ini [Core] GCIFolderBPathOverride"),
		fmt.Sprintf("B GCI folder %s no %q", customGCI, "Dolphin.ini [Core] GCIFolderBPathOverride"),
	}
	if !slices.Equal(got, want) {
		t.Errorf("Locate:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// A slot Dolphin.ini doesn't set, or sets to something that doesn't
	// parse, takes Dolphin's default: a GCI folder in slot A, nothing in
	// slot B. A number is read with the base its prefix gives, as Dolphin
	// does. A missing dir: nothing.
	u.write("GC/MemoryCardB.USA.raw", []byte("card B"))
	u.write("GC/USA/Card B/01-GZLE-gczelda.gci", gci("GZLE", "tetra"))
	uses := func() string {
		t.Helper()
		cards, err := Locate(u.root)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, c := range cards {
			got = append(got, fmt.Sprintf("%s %s %s", c.Slot, c.Kind, c.InUse))
		}
		return strings.Join(got, ", ")
	}
	const byDefault = "A raw card no, A raw card no, A GCI folder yes, B raw card no, B GCI folder no"
	os.Remove(u.path("Config/Dolphin.ini"))
	for _, c := range []struct {
		name string
		core []string // nil: no Dolphin.ini
		want string
	}{
		{"without Dolphin.ini", nil, byDefault},
		{"without SlotA / SlotB", []string{}, byDefault},
		{"SlotA / SlotB that don't parse", []string{"SlotA = junk", "SlotB = 8 cards"}, byDefault},
		{"SlotA = 1", []string{"SlotA = 1"}, "A raw card yes, A raw card yes, A GCI folder no, B raw card no, B GCI folder no"},
		{"SlotA = 0x1, SlotB = 0x8", []string{"SlotA = 0x1", "SlotB = 0x8"}, "A raw card yes, A raw card yes, A GCI folder no, B raw card no, B GCI folder yes"},
		{"SlotA = 255", []string{"SlotA = 255", "SlotB = 1"}, "A raw card no, A raw card no, A GCI folder no, B raw card yes, B GCI folder no"},
		// An empty value is 0, the Dummy device: no card.
		{"SlotA =", []string{"SlotA ="}, "A raw card no, A raw card no, A GCI folder no, B raw card no, B GCI folder no"},
		{`SlotA = ""`, []string{`SlotA = ""`}, "A raw card no, A raw card no, A GCI folder no, B raw card no, B GCI folder no"},
	} {
		if c.core != nil {
			u.ini(c.core...)
		}
		if got := uses(); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}

	// Dolphin uses a path Dolphin.ini configures in place of the default
	// card of its kind, so that default is not in use unless the path
	// comes to it. Of a GCI folder override and path, Dolphin takes the
	// override, as given.
	other := filepath.Join(t.TempDir(), "other", "USA")
	writeFile(t, filepath.Join(other, "01-GZLE-gczelda.gci"), string(gci("GZLE", "not used")))
	cardA := u.path("GC/USA/Card A")
	defRawA := []string{
		fmt.Sprintf("A raw card %s no %q", rawA2, "default"),
		fmt.Sprintf("A raw card %s no %q", rawA, "default"),
	}
	defB := []string{
		fmt.Sprintf("B raw card %s no %q", u.path("GC/MemoryCardB.USA.raw"), "default"),
		fmt.Sprintf("B GCI folder %s no %q", u.path("GC/USA/Card B"), "default"),
	}
	for _, c := range []struct {
		name string
		core []string
		want []string // slot A's cards; slot B's are its defaults, not in use
	}{
		{"MemcardAPath", []string{"SlotA = 1", "MemcardAPath = " + filepath.Join(custom, "MyCard.raw")}, append(slices.Clone(defRawA),
			fmt.Sprintf("A raw card %s yes %q", customRaw, "Dolphin.ini [Core] MemcardAPath"),
			fmt.Sprintf("A GCI folder %s no %q", cardA, "default"))},
		{"MemcardAPath naming the default card", []string{"SlotA = 1", "MemcardAPath = " + filepath.ToSlash(rawA)}, []string{
			fmt.Sprintf("A raw card %s yes %q", rawA2, "default"),
			fmt.Sprintf("A raw card %s yes %q", rawA, "default"),
			fmt.Sprintf("A GCI folder %s no %q", cardA, "default")}},
		{"GCIFolderAPath", []string{"GCIFolderAPath = " + filepath.Join(custom, "gci")}, append(slices.Clone(defRawA),
			fmt.Sprintf("A GCI folder %s no %q", cardA, "default"),
			fmt.Sprintf("A GCI folder %s yes %q", filepath.Join(custom, "gci"), "Dolphin.ini [Core] GCIFolderAPath"),
			fmt.Sprintf("A GCI folder %s yes %q", customGCI, "Dolphin.ini [Core] GCIFolderAPath"))},
		// Dolphin puts the region directory at the end of the path, so
		// this one is Card A/USA, not Card A.
		{"GCIFolderAPath naming the default folder", []string{"GCIFolderAPath = " + cardA}, append(slices.Clone(defRawA),
			fmt.Sprintf("A GCI folder %s no %q", cardA, "default"))},
		{"GCIFolderAPathOverride naming the default folder", []string{"GCIFolderAPathOverride = " + cardA}, append(slices.Clone(defRawA),
			fmt.Sprintf("A GCI folder %s yes %q", cardA, "default"))},
		{"GCIFolderAPath and GCIFolderAPathOverride", []string{"GCIFolderAPath = " + other, "GCIFolderAPathOverride = " + customGCI}, append(slices.Clone(defRawA),
			fmt.Sprintf("A GCI folder %s no %q", cardA, "default"),
			fmt.Sprintf("A GCI folder %s yes %q", customGCI, "Dolphin.ini [Core] GCIFolderAPathOverride"))},
	} {
		u.ini(c.core...)
		if got, want := located(), append(c.want, defB...); !slices.Equal(got, want) {
			t.Errorf("%s:\n%s\nwant\n%s", c.name, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}
	if cards, err := Locate(filepath.Join(t.TempDir(), "nope")); err != nil || len(cards) != 0 {
		t.Errorf("a missing user dir: %v, %v", cards, err)
	}
}

func writeFile(t *testing.T, p, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCreate: a backup copies the raw cards whole and only Wind Waker's
// GCI files, read-only, leaves the sources as they were, and records
// enough to verify and restore them.
func TestCreate(t *testing.T) {
	u := newUserDir(t)
	raw := u.write("GC/MemoryCardA.USA.raw", bytes.Repeat([]byte{0xC4}, 0x80000))
	ww := u.write("GC/USA/Card A/01-GZLE-gczelda.gci", gci("GZLE", "link's save"))
	u.write("GC/USA/Card A/01-GALE-SuperSmashBros.gci", gci("GALE", "not ours"))
	u.write("GC/USA/Card A/notes.txt", []byte("GZLE but not a .gci"))
	srcInfo, _ := os.Stat(raw)
	root := t.TempDir()

	// The same user dir twice (USER_DIR_2 left equal to USER_DIR_1) is
	// backed up once.
	b, err := Create([]string{u.root, "", u.root + string(filepath.Separator)}, Options{Root: root, Label: "session", Now: clock(day0)})
	if err != nil {
		t.Fatal(err)
	}
	if b.ID != "20261014T153012Z" || b.Dir != filepath.Join(root, b.ID) || b.Label != "session" {
		t.Errorf("backup %s at %s, label %q", b.ID, b.Dir, b.Label)
	}
	if len(b.Files) != 2 || b.Files[0].Source != raw || b.Files[1].Source != ww || len(b.UserDirs) != 1 {
		t.Fatalf("files: %+v", b.Files)
	}
	for _, f := range b.Files {
		stored := filepath.Join(b.Dir, filepath.FromSlash(f.Stored))
		if !bytes.Equal(read(t, stored), read(t, f.Source)) {
			t.Errorf("%s differs from %s", f.Stored, f.Source)
		}
		if fi, _ := os.Stat(stored); runtime.GOOS != "windows" && fi.Mode().Perm()&0o222 != 0 {
			t.Errorf("%s is writable: %v", f.Stored, fi.Mode())
		}
	}
	if fi, _ := os.Stat(raw); !fi.ModTime().Equal(srcInfo.ModTime()) || fi.Mode() != srcInfo.Mode() {
		t.Error("the source card was touched")
	}
	if err := b.Verify(); err != nil {
		t.Error(err)
	}
	if got := storedName(`01-GZLE-a:b\c/é.gci`); got != "01-GZLE-a_b_c__.gci" {
		t.Errorf("storedName = %q", got)
	}
	again, err := Open(root, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(again.Files) != fmt.Sprint(b.Files) || again.Created != b.Created {
		t.Errorf("Open = %+v\nwant %+v", again, b)
	}
	if list, err := List(root); err != nil || len(list) != 1 || list[0].ID != b.ID {
		t.Errorf("List = %v, %v", list, err)
	}

	// Nothing to back up: ErrNoCards, and no backup directory.
	empty := newUserDir(t)
	empty.write("GC/USA/Card A/01-GALE-SuperSmashBros.gci", gci("GALE", "x"))
	if _, err := Create([]string{empty.root}, Options{Root: root, Now: clock(day0.Add(time.Hour))}); !errors.Is(err, ErrNoCards) {
		t.Errorf("Create with no cards: %v", err)
	}
	if ents, _ := os.ReadDir(root); len(ents) != 1 {
		t.Errorf("%d entries in the root after a failed Create", len(ents))
	}
}

// TestRotation: Create keeps the newest Keep backups, numbers clashes
// within a second, sweeps old unfinished backups, and leaves anything
// else in the root alone.
func TestRotation(t *testing.T) {
	u := newUserDir(t)
	u.write("GC/MemoryCardA.USA.raw", []byte("card"))
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "my notes", "keep.txt"), "user's")
	unfinishedOld := filepath.Join(root, "20200101T000000Z")
	unfinishedNew := filepath.Join(root, "20261014T000000Z")
	for _, d := range []string{unfinishedOld, unfinishedNew} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := day0.Add(-2 * time.Hour)
	os.Chtimes(unfinishedOld, old, old)

	now := clock(day0)
	var ids []string
	for range 12 {
		b, err := Create([]string{u.root}, Options{Root: root, Now: now})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, b.ID)
	}
	list, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, b := range list {
		got = append(got, b.ID)
	}
	want := slices.Clone(ids[2:])
	slices.Reverse(want)
	if !slices.Equal(got, want) {
		t.Errorf("kept %v, want %v", got, want)
	}
	if _, err := os.Stat(filepath.Join(root, "my notes", "keep.txt")); err != nil {
		t.Error("pruning removed a user's directory")
	}
	if _, err := os.Stat(unfinishedOld); !errors.Is(err, fs.ErrNotExist) {
		t.Error("an hour-old unfinished backup was kept")
	}
	if _, err := os.Stat(unfinishedNew); err != nil {
		t.Error("a fresh unfinished backup (another session's, maybe) was removed")
	}

	// Two backups in one second.
	fixed := func() time.Time { return day0.Add(time.Minute) }
	a, err1 := Create([]string{u.root}, Options{Root: root, Now: fixed, Keep: 3})
	b, err2 := Create([]string{u.root}, Options{Root: root, Now: fixed, Keep: 3})
	if err1 != nil || err2 != nil || a.ID != "20261014T153112Z" || b.ID != "20261014T153112Z-2" {
		t.Fatalf("same-second ids %v %v (%v, %v)", a, b, err1, err2)
	}
	list, _ = List(root)
	if len(list) != 3 || list[0].ID != b.ID || list[1].ID != a.ID {
		t.Errorf("after Keep 3: %v", list)
	}
}

// TestOpenRejects: a manifest that would aim a restore outside the
// backup, or is otherwise not one Create wrote, doesn't open, and List
// skips it.
func TestOpenRejects(t *testing.T) {
	u := newUserDir(t)
	u.write("GC/MemoryCardA.USA.raw", []byte("card"))
	root := t.TempDir()
	b, err := Create([]string{u.root}, Options{Root: root, Now: clock(day0)})
	if err != nil {
		t.Fatal(err)
	}
	good := string(read(t, filepath.Join(b.Dir, manifestName)))
	src := b.Files[0].Source
	cases := map[string]string{
		"stored above the backup":  strings.Replace(good, `"files/00-`, `"../00-`, 1),
		"stored in a subdirectory": strings.Replace(good, `"files/00-`, `"files/x/00-`, 1),
		"relative source":          strings.ReplaceAll(good, jsonString(src), `"GC/MemoryCardA.USA.raw"`),
		"newer manifest":           strings.Replace(good, `"version": 1`, `"version": 2`, 1),
		"no such card":             strings.Replace(good, `"card": 0`, `"card": 5`, 1),
		"not JSON":                 good[:len(good)/2],
	}
	for name, m := range cases {
		if m == good {
			t.Fatalf("%s: the mutation didn't apply", name)
		}
		if _, err := decodeManifest([]byte(m)); err == nil {
			t.Errorf("%s: decoded", name)
		}
	}
	for _, id := range []string{"..", "../x", "latest", "20261014T153012Z/../.."} {
		if _, err := Open(root, id); err == nil {
			t.Errorf("Open(%q) succeeded", id)
		}
	}
	os.Chmod(filepath.Join(b.Dir, manifestName), 0o644)
	writeFile(t, filepath.Join(b.Dir, manifestName), cases["stored above the backup"])
	if list, _ := List(root); len(list) != 0 {
		t.Errorf("List kept a bad backup: %v", list)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
