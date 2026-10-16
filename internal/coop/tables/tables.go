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
const SchemaVersion = 5

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
	Stages   int                 `json:"stages"`             // stage directories read and built from
	Rooms    int                 `json:"rooms"`              // their room archives
	Skipped  []stagedata.Skipped `json:"skipped,omitempty"`  // stages the play scene can't load, left out (stagedata.Load)
	Excluded []stagedata.Skipped `json:"excluded,omitempty"` // stages on save table 15 (STAGE_TEST, d_save.h:904), left out (playable)
	Keys     *keys.Result        `json:"keys,omitempty"`
	Hearts   *hearts.Result      `json:"hearts,omitempty"`
	Switches *switches.Table     `json:"switches,omitempty"`
	Enemies  *enemies.Table      `json:"enemies,omitempty"`
}

// Only returns a copy of t holding section s alone (the other pointers
// nil), for printing one section's JSON. The section's table is shared.
func (t *Tables) Only(s Section) *Tables {
	o := &Tables{Schema: t.Schema, Builder: t.Builder, ID: t.ID, Stages: t.Stages, Rooms: t.Rooms, Skipped: t.Skipped, Excluded: t.Excluded}
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
	// ID is the disc's, set once main.dol is read: also when the build
	// then fails (a *BuildError), so a caller can still name the disc.
	ID ID
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
// stages at all outside save table 15 (playable); what a builder can't
// account for is a problem in its table instead.
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
	stages, skipped, err := stagedata.Load(cs)
	st.Read, st.Files, st.Bytes = time.Since(start), int(cs.files.Load()), cs.bytes.Load()
	if err != nil {
		return nil, fmt.Errorf("tables: %w", err)
	}
	if len(stages) == 0 {
		if len(skipped) > 0 {
			return nil, fmt.Errorf("tables: the disc has no stages the play scene can load (%d left out, %s first: %s)", len(skipped), skipped[0].Name, skipped[0].Why)
		}
		return nil, errors.New("tables: the disc has no stages (no " + stagedata.StageRoot + "/<stage>/Stage.arc)")
	}
	stages, excluded := playable(stages)
	if len(stages) == 0 {
		return nil, fmt.Errorf("tables: the disc has no stages the play scene can load outside save table %d (%d left out, %d on STAGE_TEST)",
			tableTest, len(skipped), len(excluded))
	}

	// The builders only read the stages and names, so they run at once.
	start = time.Now()
	t := &Tables{Schema: SchemaVersion, Builder: builderID(), ID: id, Stages: len(stages), Skipped: skipped, Excluded: excluded}
	for _, s := range stages {
		t.Rooms += len(s.Rooms)
	}
	errs := make([]error, len(builders))
	var wg sync.WaitGroup
	for i, b := range builders {
		wg.Go(func() { errs[i] = b.run(t, stages, names) })
	}
	wg.Wait()
	st.Build = time.Since(start)
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return t, nil
}

// tableTest is save table 15, dSv_save_c::STAGE_TEST (d_save.h:904).
const tableTest = 0xF

// whyTest is why playable leaves a stage out (Tables.Excluded's Why).
const whyTest = "save table 15 (STAGE_TEST): a developer test stage"

// playable returns stages without those on save table 15, STAGE_TEST,
// and those as excluded, in order. A stage's table is its STAG's
// (dStage_stagInfo_GetSaveTbl, d_stage.h:1027; d_stage.cpp:1674, :2255).
// The stages on it are the developer's test stages, which no retail exit
// leads to; the debug map select lists most of them (res/Menu/Menu1.dat,
// loaded at d_s_menu.cpp:314). On the owner's disc no stage on another
// table has an SCLS exit into one except A_umikz's leftover entries 0 and
// 1 to I_TestM (A_umikz holds no door or scene-change actor), and the
// only test stage the decomp's code names, K_Testb, is only compared with
// the next stage (d_a_npc_mn.cpp:209-220, :1619-1629). Their pieces,
// containers, keys, switches and enemies would only add to the tables
// what no player meets (docs/08-coop-design.md Appendix C item 137).
//
// This is not stagedata.Load's Skipped: the play scene can load these
// stages (the map select does), so they are left out here, by rule. A
// nil stage passes through, as the builders skip it.
func playable(stages []*stagedata.Stage) (kept []*stagedata.Stage, excluded []stagedata.Skipped) {
	for _, s := range stages {
		if s != nil && s.SaveTable == tableTest {
			excluded = append(excluded, stagedata.Skipped{Name: s.Name, Why: whyTest})
			continue
		}
		kept = append(kept, s)
	}
	return kept, excluded
}

// builder is one section's builder.
type builder struct {
	section Section
	build   func(t *Tables, stages []*stagedata.Stage, names *stagedata.ObjectNames)
}

// builders are the four, in Sections order. A variable so a test can make
// one fail.
var builders = []builder{
	{SectionKeys, func(t *Tables, s []*stagedata.Stage, n *stagedata.ObjectNames) { t.Keys = keys.Build(s, n) }},
	{SectionHearts, func(t *Tables, s []*stagedata.Stage, n *stagedata.ObjectNames) { t.Hearts = hearts.Build(s, n) }},
	{SectionSwitches, func(t *Tables, s []*stagedata.Stage, n *stagedata.ObjectNames) { t.Switches = switches.Build(s, n) }},
	{SectionEnemies, func(t *Tables, s []*stagedata.Stage, n *stagedata.ObjectNames) { t.Enemies = enemies.Build(s, n) }},
}

// run runs b, turning a panic into an error: the builders run on their own
// goroutines, where a bug tripped by an unusual disc would otherwise end
// the process (patch builds the tables after writing its image, and must
// not fail for them).
func (b builder) run(t *Tables, stages []*stagedata.Stage, names *stagedata.ObjectNames) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("tables: the %s builder failed on this disc (a bug; please report it): %v", b.section, r)
		}
	}()
	b.build(t, stages, names)
	return nil
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
