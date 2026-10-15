// Package backup copies the player's Dolphin memory cards aside before a
// co-op session can touch a save file (§5.4), and puts a copy back on
// request, only while Dolphin is closed.
//
// Co-op never writes a memory-card file (§5.1): the game's own save menu
// does, from RAM the co-op tool may have written to. A backup is the way
// back from a bad merge or a bad bind. Create copies every card Dolphin
// may be using for Wind Waker, read-only, into
// <os.UserConfigDir>/ww-multiplayer/backups/<id>/ and keeps the newest
// Keep (10). Restore copies one back over the cards, after taking a fresh
// backup of what it overwrites, and refuses while any Dolphin process
// runs: Dolphin holds the card in memory and would write its own copy
// over the restored file.
//
// Which cards: Dolphin keeps them in its user directory (the one `-u`
// names; dolphin2 passes USER_DIR_1 and USER_DIR_2, see UserDirs), as a
// raw image per slot (GC/MemoryCardA.USA.raw) or, in GCI-folder mode, one
// .gci file per game save under GC/USA/Card A. Config/Dolphin.ini's
// [Core] section can point either elsewhere, and says which kind each
// slot uses; a slot it doesn't set takes Dolphin's default, a GCI folder
// in slot A and nothing in slot B. Since a wrong guess about which one is
// in use would mean no backup at all, Locate returns every card that
// exists, default and configured, and says which Dolphin's settings
// select; Create copies all of them. Of a GCI folder only Wind Waker's
// own files (game code GZLE) are copied: the co-op can't affect the rest,
// and a restore then can't roll back another game's saves.
//
// Dolphin's names and settings are Dolphin's, not the game's: they are
// from Dolphin's source (Source/Core/Core/Config/MainSettings.cpp and
// HW/EXI/EXI_Device.h) and confirmed on the owner's machine in M2's
// acceptance.
package backup

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// UserDirs returns the Dolphin user directories the tool's Dolphins use,
// as dolphin2 and mp-local pick them: USER_DIR_1 and USER_DIR_2 from the
// environment, else DefaultUserDirs. host and join attach to the first.
func UserDirs() (first, second string) {
	first, second = DefaultUserDirs()
	if v := os.Getenv("USER_DIR_1"); v != "" {
		first = v
	}
	if v := os.Getenv("USER_DIR_2"); v != "" {
		second = v
	}
	return first, second
}

// DefaultUserDirs returns the per-platform defaults of main.go's
// dolphin2Defaults: under os.UserConfigDir, "Dolphin Emulator" and
// "Dolphin Emulator 2" on Windows (%APPDATA%), "Dolphin" and "Dolphin 2"
// on macOS (~/Library/Application Support), "dolphin-emu" and
// "dolphin-emu-2" elsewhere.
func DefaultUserDirs() (first, second string) {
	cfg, _ := os.UserConfigDir()
	return defaultUserDirs(runtime.GOOS, cfg)
}

func defaultUserDirs(goos, cfg string) (first, second string) {
	switch goos {
	case "darwin":
		return filepath.Join(cfg, "Dolphin"), filepath.Join(cfg, "Dolphin 2")
	case "windows":
		return filepath.Join(cfg, "Dolphin Emulator"), filepath.Join(cfg, "Dolphin Emulator 2")
	}
	return filepath.Join(cfg, "dolphin-emu"), filepath.Join(cfg, "dolphin-emu-2")
}

// Kind is how a card is stored.
type Kind uint8

const (
	// Raw is a whole card image in one file, every game's saves in it.
	Raw Kind = 1 + iota
	// GCIFolder is a directory of .gci files, one per game save.
	GCIFolder
)

func (k Kind) String() string {
	switch k {
	case Raw:
		return "raw card"
	case GCIFolder:
		return "GCI folder"
	}
	return fmt.Sprintf("Kind(%d)", uint8(k))
}

// MarshalText writes k as "raw" or "gci" in a manifest.
func (k Kind) MarshalText() ([]byte, error) {
	switch k {
	case Raw:
		return []byte("raw"), nil
	case GCIFolder:
		return []byte("gci"), nil
	}
	return nil, fmt.Errorf("backup: no card kind %d", uint8(k))
}

// UnmarshalText reads what MarshalText writes.
func (k *Kind) UnmarshalText(b []byte) error {
	switch string(b) {
	case "raw":
		*k = Raw
	case "gci":
		*k = GCIFolder
	default:
		return fmt.Errorf("backup: no card kind %q", b)
	}
	return nil
}

// Use says whether Dolphin's settings select a card for its slot: the
// slot's SlotA / SlotB in Dolphin.ini, or Dolphin's default for a slot it
// doesn't set (defaultDevice).
type Use string

const (
	// UseUnknown is in older builds' manifests only: those builds took a
	// slot Dolphin.ini doesn't set for unknown. Locate no longer returns it.
	UseUnknown Use = ""
	UseYes     Use = "yes" // the slot's device is this kind of card
	UseNo      Use = "no"  // the slot holds something else, or nothing
)

// Card is one memory card in a Dolphin user directory.
type Card struct {
	UserDir string `json:"user_dir"`
	Slot    string `json:"slot"` // "A" or "B"
	Kind    Kind   `json:"kind"`
	// Path is the raw image, or the GCI folder.
	Path string `json:"path"`
	// From says where Path came from: "default", or the Dolphin.ini key.
	From  string `json:"from"`
	InUse Use    `json:"in_use"`
}

func (c Card) String() string {
	use := map[Use]string{UseYes: ", in use", UseNo: ", not in use", UseUnknown: ""}[c.InUse]
	return fmt.Sprintf("slot %s %s %s (%s%s)", c.Slot, c.Kind, c.Path, c.From, use)
}

// Dolphin's settings and names.
const (
	// iniPath is Dolphin.ini within a user directory.
	iniPath = "Config/Dolphin.ini"

	// The EXI device numbers SlotA / SlotB hold (EXIDeviceType): a raw
	// card, a GCI folder, and nothing.
	exiMemoryCard       = 1
	exiMemoryCardFolder = 8
	exiNone             = 0xFF

	// regionDir is the GC directory and card-name part for an NTSC-U
	// game such as GZLE01 (Dolphin's GetDirectoryForRegion).
	regionDir = "USA"

	// gameCode is the first four bytes of a Wind Waker GCI file's header:
	// the game code of GZLE01. The save itself is the card file
	// "gczelda" (m_Do_MemCard.cpp:116,134).
	gameCode = "GZLE"
)

var slots = []string{"A", "B"}

// defaultDevice is the device Dolphin gives a slot Dolphin.ini doesn't
// set, or sets to something that doesn't parse (Config::GetUncached): a
// GCI folder in slot A and nothing in slot B (MAIN_SLOT_A and MAIN_SLOT_B
// in MainSettings.cpp, the same on master and in each release checked,
// 2407 to 2609). Dolphin.ini holds only the settings that were set
// (BaseConfigLayerLoader::Save), and Dolphin's GameCube settings set
// SlotA and SlotB as the user changes a slot there
// (GameCubePane::SaveSettings), so a user who never changed one usually
// has neither key.
var defaultDevice = map[string]int{"A": exiMemoryCardFolder, "B": exiNone}

// dolphinIni is what Locate reads from Dolphin.ini's [Core] section. A
// missing file or key leaves the default: Dolphin's default device for a
// slot (defaultDevice), and the default card paths. So does a SlotA /
// SlotB that doesn't parse, as in Dolphin.
type dolphinIni struct {
	device  map[string]int    // slot -> SlotA/SlotB
	memcard map[string]string // slot -> MemcardAPath/MemcardBPath
	gci     map[string]string // slot -> GCIFolderAPath/GCIFolderBPath (or the older ...Override)
	gciKey  map[string]string // slot -> which of those keys gave gci
}

func readDolphinIni(userDir string) (dolphinIni, error) {
	ini := dolphinIni{device: map[string]int{}, memcard: map[string]string{}, gci: map[string]string{}, gciKey: map[string]string{}}
	b, err := os.ReadFile(filepath.Join(userDir, filepath.FromSlash(iniPath)))
	if errors.Is(err, fs.ErrNotExist) {
		return ini, nil
	}
	if err != nil {
		return ini, fmt.Errorf("backup: %w", err)
	}
	core := parseIni(bytes.NewReader(b))["core"]
	resolve := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(userDir, p)
	}
	for _, s := range slots {
		if v, ok := core["slot"+strings.ToLower(s)]; ok {
			// Dolphin reads the number with the base its prefix gives, as
			// strtoll with base 0 does (TryParse, StringUtil.h): 0x8 is 8.
			if n, err := strconv.ParseInt(v, 0, 32); err == nil {
				ini.device[s] = int(n)
			}
		}
		if v := core["memcard"+strings.ToLower(s)+"path"]; v != "" {
			ini.memcard[s] = resolve(v)
		}
		for _, k := range []string{"GCIFolder" + s + "Path", "GCIFolder" + s + "PathOverride"} {
			if v := core[strings.ToLower(k)]; v != "" {
				ini.gci[s], ini.gciKey[s] = resolve(v), k
				break
			}
		}
	}
	return ini, nil
}

// parseIni reads an INI file the way Dolphin's IniFile does: [Section]
// headers, key = value lines with both sides trimmed and a quoted value
// unquoted, and # or ; comments. Section and key names are folded to
// lower case, as Dolphin compares them case-insensitively.
func parseIni(r io.Reader) map[string]map[string]string {
	out := map[string]map[string]string{}
	section := ""
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff"))
		switch {
		case line == "", line[0] == '#', line[0] == ';':
			continue
		case line[0] == '[':
			if end := strings.IndexByte(line, ']'); end > 0 {
				section = strings.ToLower(strings.TrimSpace(line[1:end]))
			}
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
			v = v[1 : len(v)-1]
		}
		if out[section] == nil {
			out[section] = map[string]string{}
		}
		out[section][k] = v
	}
	return out
}

// rawPattern is a family of raw card files: Dolphin puts the region, and
// a block count on cards smaller than 2043 blocks, between the configured
// name and its extension (MemoryCardA.USA.raw, MemoryCardA.USA.251.raw).
// It matches dir/<stem>.*<ext>, and the configured file itself.
type rawPattern struct {
	dir, stem, ext string
	exact          string // the configured path, "" for the default
}

func (p rawPattern) match(path string) bool {
	if p.exact != "" && samePath(path, p.exact) {
		return true
	}
	if !samePath(filepath.Dir(path), p.dir) {
		return false
	}
	base, stem, ext := pathKey(filepath.Base(path)), pathKey(p.stem), pathKey(p.ext)
	return len(base) > len(stem)+1+len(ext) && strings.HasPrefix(base, stem+".") && strings.HasSuffix(base, ext)
}

// newRawPattern is the family of a configured or default raw card path,
// with any region code already in its name taken out.
func newRawPattern(path string, exact bool) rawPattern {
	dir, base := filepath.Split(path)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for _, r := range []string{".USA", ".EUR", ".JAP", ".KOR"} {
		if strings.HasSuffix(strings.ToUpper(stem), r) {
			stem = stem[:len(stem)-len(r)]
			break
		}
	}
	p := rawPattern{dir: filepath.Clean(dir), stem: stem, ext: ext}
	if exact {
		p.exact = filepath.Clean(path)
	}
	return p
}

// candidate is one place a card may be, whether or not anything is there.
type candidate struct {
	Card
	raw rawPattern // Kind Raw
	// guess marks a GCI folder derived from a configured one rather than
	// named: Locate reports it only if it holds a Wind Waker save.
	guess bool
}

// candidates returns every place Dolphin may keep userDir's cards: for
// each slot, the default raw image and GCI folder, and the ones
// Dolphin.ini configures. A configured GCI folder is tried as given, with
// the region directory Dolphin appends, and with the "Card A" folder of
// the default layout.
func candidates(userDir string) ([]candidate, error) {
	userDir = filepath.Clean(userDir)
	ini, err := readDolphinIni(userDir)
	if err != nil {
		return nil, err
	}
	gc := filepath.Join(userDir, "GC")
	var out []candidate
	for _, s := range slots {
		use := func(k Kind) Use {
			d, ok := ini.device[s]
			if !ok {
				d = defaultDevice[s]
			}
			if k == Raw && d == exiMemoryCard || k == GCIFolder && d == exiMemoryCardFolder {
				return UseYes
			}
			return UseNo
		}
		card := func(k Kind, path, from string) Card {
			return Card{UserDir: userDir, Slot: s, Kind: k, Path: filepath.Clean(path), From: from, InUse: use(k)}
		}
		def := filepath.Join(gc, "MemoryCard"+s+"."+regionDir+".raw")
		out = append(out, candidate{Card: card(Raw, def, "default"), raw: newRawPattern(def, false)})
		if p := ini.memcard[s]; p != "" {
			out = append(out, candidate{Card: card(Raw, p, "Dolphin.ini [Core] Memcard"+s+"Path"), raw: newRawPattern(p, true)})
		}
		out = append(out, candidate{Card: card(GCIFolder, filepath.Join(gc, regionDir, "Card "+s), "default")})
		if p := ini.gci[s]; p != "" {
			from := "Dolphin.ini [Core] " + ini.gciKey[s]
			for i, dir := range []string{p, filepath.Join(p, regionDir), filepath.Join(p, regionDir, "Card "+s)} {
				out = append(out, candidate{Card: card(GCIFolder, dir, from), guess: i > 0})
			}
		}
	}
	return out, nil
}

// Locate returns the cards in userDir that exist: raw images that are
// files, and GCI folders that are directories (for a folder derived from
// a configured one, only when it holds a Wind Waker save), each path
// once, slot A first. A missing userDir has none.
func Locate(userDir string) ([]Card, error) {
	cs, err := candidates(userDir)
	if err != nil {
		return nil, err
	}
	var out []Card
	seen := map[string]bool{}
	add := func(c Card) {
		if k := pathKey(filepath.Clean(c.Path)); !seen[k] {
			seen[k] = true
			out = append(out, c)
		}
	}
	for _, c := range cs {
		switch c.Kind {
		case Raw:
			files, err := rawFiles(c.raw)
			if err != nil {
				return nil, err
			}
			for _, f := range files {
				rc := c.Card
				rc.Path = f
				add(rc)
			}
		case GCIFolder:
			fi, err := os.Stat(c.Path)
			if err != nil || !fi.IsDir() {
				continue
			}
			if c.guess {
				if files, err := gciFiles(c.Path); err != nil || len(files) == 0 {
					continue
				}
			}
			add(c.Card)
		}
	}
	return out, nil
}

// rawFiles lists the regular files of p's family, sorted.
func rawFiles(p rawPattern) ([]string, error) {
	var out []string
	ents, err := os.ReadDir(p.dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("backup: %w", err)
	}
	for _, e := range ents {
		path := filepath.Join(p.dir, e.Name())
		if e.Type().IsRegular() && p.match(path) {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out, nil
}

// gciFiles lists the Wind Waker saves in a GCI folder: regular .gci files
// whose header starts with the game code GZLE. Other games' saves are
// left out.
func gciFiles(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("backup: %w", err)
	}
	var out []string
	for _, e := range ents {
		if !e.Type().IsRegular() || !strings.EqualFold(filepath.Ext(e.Name()), ".gci") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if isGameGCI(path) {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out, nil
}

func isGameGCI(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var head [len(gameCode)]byte
	_, err = io.ReadFull(f, head[:])
	return err == nil && string(head[:]) == gameCode
}

// samePath compares two paths as Windows does, ignoring case, and
// exactly elsewhere. (macOS volumes usually ignore case too, but can be
// made not to; there two names that differ only in case are kept apart.)
func samePath(a, b string) bool { return pathKey(filepath.Clean(a)) == pathKey(filepath.Clean(b)) }

// pathKey is p folded the way samePath compares it.
func pathKey(p string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(p)
	}
	return p
}
