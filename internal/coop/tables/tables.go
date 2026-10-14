// Package tables is M1's coop-tables (docs/08-coop-design.md §6.2,
// §6.4, §7.2, §9 M1, §11 item 8): it reads the user's own disc once,
// main.dol for its hash and l_objectName and every stage's Stage.arc and
// Room<N>.arc, runs the four table builders over the stages (keys,
// hearts, switches, enemies) and keeps the result as JSON under the
// user's config directory, named by the DOL's SHA-256, so later runs read
// it back instead of the disc.
//
// Only these are read from the disc: boot.bin and the FST (when the disc
// is opened), main.dol, and the Stage.arc and Room<N>.arc files
// stagedata.Load opens. Expanding those archives' Yaz0 is most of the
// time a build takes; the four builders are a small part (BenchmarkGet).
//
// Citations: file:line is the zeldaret/tww decomp at commit 4d78c994
// (include/ and src/; the file names are unique, so directories are left
// out), sym:N line N of its config/GZLE01/symbols.txt, as in
// internal/coop/addr.
package tables

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/enemies"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/hearts"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/keys"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/switches"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
)

// SchemaVersion is the cache format. Bump it whenever what a builder
// writes changes, in shape or in what it decides, so a cache an older
// binary wrote is rebuilt (the cache also records the binary's version,
// builderID, which catches a release that forgets). TestSchemaShape fails
// when the JSON shape changes and this has not been bumped.
const SchemaVersion = 2

// Source is the disc Build reads: *inject.Disc satisfies it. Build calls
// Stat and ReadFile from several goroutines at once (stagedata.FileSource).
type Source interface {
	stagedata.FileSource
	ReadDOL() ([]byte, error)
}

var _ Source = (*inject.Disc)(nil)

// Section is one of the four tables.
type Section string

const (
	SectionKeys     Section = "keys"     // small-key sources and key doors, per save table (§6.4)
	SectionHearts   Section = "hearts"   // heart pieces and containers (§11 items 8, 11)
	SectionSwitches Section = "switches" // switch classes and push blocks (§6.2)
	SectionEnemies  Section = "enemies"  // enemy entries and the setID report (§7.1-7.2)
)

// Sections lists the sections in report order.
var Sections = []Section{SectionKeys, SectionHearts, SectionSwitches, SectionEnemies}

// ParseSection reads a section name as Sections spells it.
func ParseSection(s string) (Section, error) {
	for _, sec := range Sections {
		if s == string(sec) {
			return sec, nil
		}
	}
	names := make([]string, len(Sections))
	for i, sec := range Sections {
		names[i] = string(sec)
	}
	return "", fmt.Errorf("unknown section %q (want %s)", s, strings.Join(names, ", "))
}

// ID names what a Tables is built from. DOL is the SHA-256 of main.dol
// as Disc.ReadDOL returns it, which names the cache file; StageFiles is
// the SHA-256 of the stage files' listing (stageFilesHash), which catches
// a disc whose stages changed under an unchanged DOL. Both are lowercase
// hex.
type ID struct {
	DOL        string `json:"dol_sha256"`
	StageFiles string `json:"stage_files_sha256"`
}

// Tables is everything coop-tables builds from one disc. Each section is
// its builder's result, unchanged; Only leaves the others nil, which the
// JSON leaves out.
type Tables struct {
	Schema  int    `json:"schema"`
	Builder string `json:"builder"` // the binary that built it (builderID)
	ID
	Stages   int             `json:"stages"` // stage directories read
	Rooms    int             `json:"rooms"`  // room archives read
	Keys     *keys.Result    `json:"keys,omitempty"`
	Hearts   *hearts.Result  `json:"hearts,omitempty"`
	Switches *switches.Table `json:"switches,omitempty"`
	Enemies  *enemies.Table  `json:"enemies,omitempty"`
}

// Only returns a copy of t holding section s alone (the other pointers
// nil), for printing one section's JSON. The section's table is shared.
func (t *Tables) Only(s Section) *Tables {
	o := &Tables{Schema: t.Schema, Builder: t.Builder, ID: t.ID, Stages: t.Stages, Rooms: t.Rooms}
	switch s {
	case SectionKeys:
		o.Keys = t.Keys
	case SectionHearts:
		o.Hearts = t.Hearts
	case SectionSwitches:
		o.Switches = t.Switches
	case SectionEnemies:
		o.Enemies = t.Enemies
	}
	return o
}

// complete reports whether t has every section.
func (t *Tables) complete() bool {
	return t.Keys != nil && t.Hearts != nil && t.Switches != nil && t.Enemies != nil
}

// Stats says how Get came by its tables and where the time went.
type Stats struct {
	// Cached is true when the tables came from the cache file Path.
	Cached bool
	// Path is the cache file read or written; "" without a cache.
	Path string
	// Why is why the cache wasn't used ("" when it was, or when there is
	// no cache): no file yet, another schema or binary, a corrupt file.
	Why string
	// SaveErr is why the built tables could not be saved; they are
	// returned all the same.
	SaveErr error

	Identify time.Duration // read main.dol, hash it and the stage listing
	Read     time.Duration // read the cache file, or read and expand the stage archives
	Build    time.Duration // the four builders
	Save     time.Duration // write the cache file

	Files int   // stage archives read from the disc
	Bytes int64 // their bytes as stored on the disc (most Yaz0-compressed)
}

// Build reads src and builds every table. It fails when main.dol is not
// the GZLE01 DOL (no l_objectName at 0x80372818), when a stage archive
// can't be read or parsed (stagedata.Load), and when the disc has no
// stages at all; what a builder can't account for is a problem in its
// table instead.
func Build(src Source) (*Tables, error) {
	id, dol, err := identify(src)
	if err != nil {
		return nil, err
	}
	return build(src, dol, id, new(Stats))
}

// Identify returns src's ID, reading main.dol and the FST listing only.
func Identify(src Source) (ID, error) {
	id, _, err := identify(src)
	return id, err
}

func identify(src Source) (ID, []byte, error) {
	dol, err := src.ReadDOL()
	if err != nil {
		return ID{}, nil, fmt.Errorf("tables: %w", err)
	}
	sum := sha256.Sum256(dol)
	return ID{DOL: hex.EncodeToString(sum[:]), StageFiles: stageFilesHash(src.Files())}, dol, nil
}

// stageFilesHash hashes the path and size of every file under
// stagedata.StageRoot, in listing order: what stagedata.Load can read.
// Moving a file on the disc doesn't change it (an ISO and a CISO of one
// disc hash the same); editing an archive almost always changes its size.
func stageFilesHash(files []inject.DiscFile) string {
	h := sha256.New()
	prefix := strings.ToLower(stagedata.StageRoot) + "/"
	var size [4]byte
	for _, f := range files {
		p := strings.TrimPrefix(f.Path, "/")
		if len(p) < len(prefix) || strings.ToLower(p[:len(prefix)]) != prefix {
			continue
		}
		h.Write([]byte(p))
		h.Write([]byte{0})
		binary.BigEndian.PutUint32(size[:], f.Size)
		h.Write(size[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// build builds the tables from src, whose main.dol is dol and ID id,
// recording times and sizes in st.
func build(src Source, dol []byte, id ID, st *Stats) (*Tables, error) {
	names, err := stagedata.ReadObjectNames(dol)
	if err != nil {
		return nil, fmt.Errorf("tables: %w", err)
	}
	start := time.Now()
	cs := &countingSource{Source: src}
	stages, err := stagedata.Load(cs)
	st.Read, st.Files, st.Bytes = time.Since(start), int(cs.files.Load()), cs.bytes.Load()
	if err != nil {
		return nil, fmt.Errorf("tables: %w", err)
	}
	if len(stages) == 0 {
		return nil, errors.New("tables: the disc has no stages (no " + stagedata.StageRoot + "/<stage>/Stage.arc)")
	}

	// The builders only read the stages and names, so they run at once.
	start = time.Now()
	t := &Tables{Schema: SchemaVersion, Builder: builderID(), ID: id, Stages: len(stages)}
	for _, s := range stages {
		t.Rooms += len(s.Rooms)
	}
	var wg sync.WaitGroup
	wg.Go(func() { t.Keys = keys.Build(stages, names) })
	wg.Go(func() { t.Hearts = hearts.Build(stages, names) })
	wg.Go(func() { t.Switches = switches.Build(stages, names) })
	wg.Go(func() { t.Enemies = enemies.Build(stages, names) })
	wg.Wait()
	st.Build = time.Since(start)
	return t, nil
}

// countingSource counts the files and bytes stagedata.Load reads.
type countingSource struct {
	Source
	files atomic.Int64
	bytes atomic.Int64
}

func (c *countingSource) ReadFile(path string) ([]byte, error) {
	b, err := c.Source.ReadFile(path)
	if err == nil {
		c.files.Add(1)
		c.bytes.Add(int64(len(b)))
	}
	return b, err
}

// builderID names the binary building the tables: its main module's
// version, which go build (Go 1.24+) takes from the VCS (a tag, or a
// pseudo-version naming the commit, "+dirty" with uncommitted changes),
// and the VCS revision when the binary carries one. A cache another
// binary wrote is rebuilt, since its builders may decide differently.
var builderID = sync.OnceValue(func() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	id := bi.Main.Version
	if id == "" {
		id = "(devel)"
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" && !strings.Contains(id, shortRev(s.Value)) {
			id += " " + s.Value
		}
	}
	return id
})

// shortRev is the 12-digit revision prefix a pseudo-version carries.
func shortRev(rev string) string { return rev[:min(len(rev), 12)] }
