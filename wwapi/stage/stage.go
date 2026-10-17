package stage

import (
	"errors"
	"fmt"
	"io/fs"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/disc"
)

// Stages on the disc. A stage is a directory under /res/Stage/ named for
// it (dRes_control_c::setStageRes, d_resorce.cpp:705-710), in which the
// game mounts "<dir><name>.arc" (dRes_info_c::set, d_resorce.cpp:56-60):
// "Stage" for the stage itself (d_s_play.cpp:1284) and "Room%d", the room
// number in decimal, for each room (setArcName, d_s_room.cpp:50-54, used
// at :164). In the archives it reads "stage.dzs" (dStage_Create,
// d_stage.cpp:2225) and "room.dzr" (d_s_room.cpp:201) by name; a room
// archive without a room.dzr places nothing (d_s_room.cpp:203-204). The
// DVD library resolves those paths ASCII case-insensitively (dvdfs.c,
// via inject.Disc), so a listing is matched the same way.
const (
	// StageRoot is the directory the stages are in.
	StageRoot = "res/Stage"

	// MaxRooms bounds a room number: the game keeps rooms 0-63
	// (mStatus[64], d_stage.h:926; dStage_chkPlayerId's assert,
	// d_stage.cpp:1412).
	MaxRooms = 64

	// maxStageName is the longest stage name the game can load. The name
	// is a char mName[8] (dStage_startStage_c, d_stage.h:949), and
	// setStageRes builds "/res/Stage/%s/" in 20 bytes (d_resorce.cpp:
	// 707-708), which cuts the final "/" off a longer one.
	maxStageName = 7

	stageArcFile  = "Stage.arc"
	roomArcPrefix = "Room"
	arcSuffix     = ".arc"
	stageDZSName  = "stage.dzs"
	roomDZRName   = "room.dzr"

	// maxLoadWorkers caps how many archives Load reads and expands at
	// once, which bounds its memory: each worker holds one archive of at
	// most MaxArchiveSize as read and MaxYaz0Size expanded, and what
	// ParseDZx makes of one file in it.
	maxLoadWorkers = 8

	// maxLoadErrors caps how many failed archives a Load error names.
	maxLoadErrors = 10
)

// Why Load leaves a stage out (Skipped.Why).
const (
	SkipNoDZS  = "no stage.dzs"
	SkipNoStag = "stage.dzs has no STAG chunk"
)

// Stage is one stage directory: its stage.dzs and its rooms' room.dzr.
type Stage struct {
	Name      string    // the directory, as spelled on the disc: "M_NewD2"
	Path      string    // Stage.arc's path: "res/Stage/M_NewD2/Stage.arc"
	SaveTable int       // Stag.SaveTable()
	StageType StageType // Stag.StageType()
	KeyHUD    bool      // Stag.KeyHUD()
	Stag      Stag      // stage.dzs's STAG
	DZS       *DZx      // stage.dzs: the stage's own actors, which the game creates with room -1
	Rooms     []Room    // every room archive the game can load, in room-number order
}

// Room is one room of a stage.
type Room struct {
	No   int    // 0-63
	Path string // the archive's path as the game opens it: "res/Stage/M_NewD2/Room3.arc"
	DZR  *DZx   // room.dzr; nil when the archive has none
}

// Skipped is a stage directory Load leaves out, with its rooms: its
// Stage.arc parses, but the play scene can't load the stage (Load), so
// nothing in it is ever placed and it has no save table.
type Skipped struct {
	Name string `json:"name"` // the directory, as spelled on the disc: "Name"
	Why  string `json:"why"`  // SkipNoDZS or SkipNoStag
}

// Placed is an entry with the room whose file places it.
type Placed struct {
	Room int // the room number; -1 for the stage's own (dStage_stageDt_c::getRoomNo, d_stage.h:451)
	Actor
}

// Actors returns the room's entries, every layer (nil without a room.dzr).
func (r Room) Actors() []Actor {
	if r.DZR == nil {
		return nil
	}
	return r.DZR.Actors
}

// OnLayer returns what the room creates on layer layer (0-11).
func (r Room) OnLayer(layer int) []Actor {
	if r.DZR == nil {
		return nil
	}
	return r.DZR.OnLayer(layer)
}

// Room returns room no, if the stage has it.
func (s *Stage) Room(no int) (*Room, bool) {
	i, ok := slices.BinarySearchFunc(s.Rooms, no, func(r Room, no int) int { return r.No - no })
	if !ok {
		return nil, false
	}
	return &s.Rooms[i], true
}

// All returns every entry of the stage and its rooms, every layer: the
// stage's own first, then each room's in room order, each file's in
// creation order.
func (s *Stage) All() []Placed { return s.placed(func(Actor) bool { return true }) }

// OnLayer returns what the stage and all its rooms create on layer layer
// (0-11). The game picks the layer per room (getLayerNo,
// d_com_inf_game.cpp:185-269), but only on the sea do rooms differ (the
// Outset, Windfall and Forsaken Fortress rooms, :192-209); elsewhere
// every room, and the stage itself, gets the same one.
func (s *Stage) OnLayer(layer int) []Placed {
	return s.placed(func(a Actor) bool { return a.OnLayer(layer) })
}

// Layers returns the layers (0-11) that have entries of their own in the
// stage or any room, in order.
func (s *Stage) Layers() []int {
	var l []int
	if s.DZS != nil {
		l = s.DZS.Layers()
	}
	for _, r := range s.Rooms {
		if r.DZR != nil {
			l = append(l, r.DZR.Layers()...)
		}
	}
	slices.Sort(l)
	return slices.Compact(l)
}

func (s *Stage) placed(keep func(Actor) bool) []Placed {
	var out []Placed
	add := func(room int, as []Actor) {
		for _, a := range as {
			if keep(a) {
				out = append(out, Placed{Room: room, Actor: a})
			}
		}
	}
	if s.DZS != nil {
		add(-1, s.DZS.Actors)
	}
	for _, r := range s.Rooms {
		add(r.No, r.Actors())
	}
	return out
}

// Load reads every stage on the disc, in the order the disc lists them.
// A stage is a directory right under StageRoot with a Stage.arc in it;
// its rooms are the Room<N>.arc files beside it, N being 0-63 written as
// "%d" writes it. Other files, directories without a Stage.arc, and
// directories with names longer than the game can load are left out.
//
// Stages the play scene can't load are left out too, with their rooms,
// and returned as skipped, in listing order:
//   - A Stage.arc with no stage.dzs. Only the play scene's loader reads a
//     stage.dzs (dStage_infoCreate and dStage_Create, d_stage.cpp:
//     2214-2226; the opening scenes share it, d_s_play.cpp:1528-1566),
//     and it halts on a missing one, since JUT_ASSERT panics in the
//     retail build too (JUTAssert.h:9). Another scene loads such a
//     stage: the file-select (name) scene loads "Name"'s Stage.arc and
//     reads only a nested archive in it, resource 0x17, for its screens
//     (d_s_name.cpp:71, 125-145).
//   - A stage.dzs with no STAG. The stage info stays NULL (dStage_
//     stageDt_c::init, d_stage.cpp:385); only the STAG sets it, and with
//     it the save table (dStage_stagInfoInit, :1670-1677). The play scene
//     reads it unchecked as the stage starts (d_s_play.cpp:1302) and ends
//     (dStage_Delete, d_stage.cpp:2255).
//
// A Stage.arc or room archive that doesn't parse, an archive the disc
// lists as larger than MaxArchiveSize (refused before it is read), and
// any read error fail the whole load: the tables built from it would be
// silently short. The error names the failures (up to maxLoadErrors):
// each stage's Stage.arc first, in listing order, then the rooms of the
// stages whose Stage.arc loaded. A stage whose Stage.arc failed has its
// rooms left unread.
// A file the listing has but Stat can't find is one the game can't
// see either (another spelling of its directory comes first), and is
// left out; so are the rooms of such a stage. The archives are expanded
// in parallel.
func Load(src disc.FileSource) ([]*Stage, []Skipped, error) {
	return loadStages(src, listStages(src.Files()))
}

// LoadStage reads one stage by name (matched case-insensitively, as the
// game opens it). A name Load would not list is an error matching
// fs.ErrNotExist; one Load would skip is an error saying why.
func LoadStage(src disc.FileSource, name string) (*Stage, error) {
	for _, l := range listStages(src.Files()) {
		if !asciiEqualFold(l.name, name) {
			continue
		}
		st, skipped, err := loadStages(src, []stageListing{l})
		if err != nil {
			return nil, err
		}
		if len(st) == 1 {
			return st[0], nil
		}
		if len(skipped) == 1 {
			return nil, fmt.Errorf("stagedata: %s: %s, so the play scene can't load it", l.stagePath(), skipped[0].Why)
		}
		break
	}
	return nil, fmt.Errorf("stagedata: no stage %q under %s: %w", name, StageRoot, fs.ErrNotExist)
}

// stageListing is one candidate stage directory.
type stageListing struct {
	name  string // as first spelled in the listing
	rooms []int  // ascending
}

func (l stageListing) stagePath() string { return StageRoot + "/" + l.name + "/" + stageArcFile }

func (l stageListing) roomPath(no int) string {
	return StageRoot + "/" + l.name + "/" + roomArcPrefix + strconv.Itoa(no) + arcSuffix
}

// listStages finds the stage directories in files: those right under
// StageRoot that hold a Stage.arc, with their room numbers. Names compare
// ASCII case-insensitively, so two spellings of one directory are one
// candidate, named as first spelled; ReadFile then resolves its paths as
// the game would.
func listStages(files []disc.DiscFile) []stageListing {
	type cand struct {
		stageListing
		hasStage bool
		rooms    [MaxRooms]bool
	}
	var cands []*cand
	byKey := make(map[string]*cand)
	for _, f := range files {
		parts := strings.Split(strings.TrimPrefix(f.Path, "/"), "/")
		if len(parts) != 4 || !asciiEqualFold(parts[0], "res") || !asciiEqualFold(parts[1], "Stage") {
			continue
		}
		dir, file := parts[2], parts[3]
		if dir == "" || len(dir) > maxStageName {
			continue
		}
		key := asciiLowerString(dir)
		c := byKey[key]
		if c == nil {
			c = &cand{stageListing: stageListing{name: dir}}
			byKey[key] = c
			cands = append(cands, c)
		}
		if asciiEqualFold(file, stageArcFile) {
			c.hasStage = true
		} else if no, ok := roomNumber(file); ok {
			c.rooms[no] = true
		}
	}
	var out []stageListing
	for _, c := range cands {
		if !c.hasStage {
			continue
		}
		for no, ok := range c.rooms {
			if ok {
				c.stageListing.rooms = append(c.stageListing.rooms, no)
			}
		}
		out = append(out, c.stageListing)
	}
	return out
}

// roomNumber returns N for a file the game opens as "Room%d.arc" with N
// in 0-63: no sign, no leading zero, any letter case.
func roomNumber(file string) (int, bool) {
	p, s := len(roomArcPrefix), len(arcSuffix)
	if len(file) <= p+s || !asciiEqualFold(file[:p], roomArcPrefix) || !asciiEqualFold(file[len(file)-s:], arcSuffix) {
		return 0, false
	}
	digits := file[p : len(file)-s]
	if len(digits) > 2 || (len(digits) > 1 && digits[0] == '0') {
		return 0, false
	}
	n := 0
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return 0, false
		}
		n = n*10 + int(digits[i]-'0')
	}
	return n, n < MaxRooms
}

// loadJob is one archive to read: a stage's Stage.arc (room < 0) or one
// of its rooms.
type loadJob struct {
	stage, room int
	path        string
}

// loadResult is what a job found.
type loadResult struct {
	found bool   // the archive exists (Stat and ReadFile didn't report fs.ErrNotExist)
	dzx   *DZx   // its stage.dzs or room.dzr; nil for a room without one, and for a skipped stage
	skip  string // why the play scene can't load this Stage.arc's stage (Skipped.Why); "" if it can
	err   error
}

// loadStages reads the listed stages: every Stage.arc first, then the
// rooms of the stages the game can open and play (a room of a stage it
// can't see or skips is never loaded, however broken). The rooms of the
// stages whose Stage.arc loaded are read even when another's failed, so
// the error names every failure.
func loadStages(src disc.FileSource, list []stageListing) ([]*Stage, []Skipped, error) {
	jobs := make([]loadJob, len(list))
	for i, l := range list {
		jobs[i] = loadJob{stage: i, room: -1, path: l.stagePath()}
	}
	var errs []error
	var skipped []Skipped
	stages := make([]*Stage, len(list))
	var rooms []loadJob
	for i, r := range runJobs(src, jobs) {
		switch {
		case r.err != nil:
			errs = append(errs, r.err)
			continue
		case !r.found:
			continue // a spelling another directory shadows: the game can't see it
		case r.skip != "":
			skipped = append(skipped, Skipped{Name: list[i].name, Why: r.skip})
			continue
		}
		s := r.dzx.Stag
		stages[i] = &Stage{
			Name: list[i].name, Path: list[i].stagePath(),
			SaveTable: s.SaveTable(), StageType: s.StageType(), KeyHUD: s.KeyHUD(),
			Stag: *s, DZS: r.dzx,
		}
		for _, no := range list[i].rooms {
			rooms = append(rooms, loadJob{stage: i, room: no, path: list[i].roomPath(no)})
		}
	}
	for k, r := range runJobs(src, rooms) { // in room order within each stage
		j := rooms[k]
		switch {
		case r.err != nil:
			errs = append(errs, r.err)
		case r.found:
			stages[j.stage].Rooms = append(stages[j.stage].Rooms, Room{No: j.room, Path: j.path, DZR: r.dzx})
		}
	}
	if len(errs) > maxLoadErrors {
		errs = append(errs[:maxLoadErrors:maxLoadErrors], fmt.Errorf("stagedata: and %d more archives failed", len(errs)-maxLoadErrors))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, nil, err
	}
	return slices.DeleteFunc(stages, func(s *Stage) bool { return s == nil }), skipped, nil
}

// runJobs runs every job on a few workers and returns their results in
// job order, which is the same from run to run whatever the workers'
// timing.
func runJobs(src disc.FileSource, jobs []loadJob) []loadResult {
	results := make([]loadResult, len(jobs))
	next := make(chan int)
	var wg sync.WaitGroup
	for range min(runtime.GOMAXPROCS(0), maxLoadWorkers, max(len(jobs), 1)) {
		wg.Go(func() {
			for k := range next {
				results[k] = loadArchive(src, jobs[k])
			}
		})
	}
	for k := range jobs {
		next <- k
	}
	close(next)
	wg.Wait()
	return results
}

// loadArchive reads one job's archive and its DZx file. The size the
// disc lists is checked first: ReadFile allocates whatever it says, which
// a crafted image can make gigabytes on a sparse file.
func loadArchive(src disc.FileSource, j loadJob) loadResult {
	f, err := src.Stat(j.path)
	if errors.Is(err, fs.ErrNotExist) {
		return loadResult{}
	}
	if err != nil {
		return loadResult{err: fmt.Errorf("stagedata: %s: %w", j.path, err)}
	}
	if f.Size > MaxArchiveSize {
		return loadResult{err: fmt.Errorf("stagedata: %s: the disc lists %d bytes, more than the %d-byte cap", j.path, f.Size, MaxArchiveSize)}
	}
	b, err := src.ReadFile(j.path)
	if errors.Is(err, fs.ErrNotExist) {
		return loadResult{}
	}
	if err != nil {
		return loadResult{err: fmt.Errorf("stagedata: %s: %w", j.path, err)}
	}
	if len(b) > MaxArchiveSize { // a source whose Stat and ReadFile disagree
		return loadResult{err: fmt.Errorf("stagedata: %s: %d bytes read, more than the %d-byte cap", j.path, len(b), MaxArchiveSize)}
	}
	a, err := ParseArchive(b)
	if err != nil {
		return loadResult{err: fmt.Errorf("stagedata: %s: %w", j.path, err)}
	}
	member := roomDZRName
	if j.room < 0 {
		member = stageDZSName
	}
	m, ok := a.FindName(member)
	if !ok {
		if j.room < 0 {
			return loadResult{found: true, skip: SkipNoDZS}
		}
		return loadResult{found: true}
	}
	data, err := a.Read(m)
	if err != nil {
		return loadResult{err: fmt.Errorf("stagedata: %s: %w", j.path, err)}
	}
	d, err := ParseDZx(data)
	if err != nil {
		return loadResult{err: fmt.Errorf("stagedata: %s: %s: %w", j.path, m.Path, err)}
	}
	if j.room < 0 && d.Stag == nil {
		return loadResult{found: true, skip: SkipNoStag}
	}
	return loadResult{found: true, dzx: d}
}

// asciiLowerString lowercases the ASCII letters of s.
func asciiLowerString(s string) string {
	b := []byte(s)
	for i, c := range b {
		b[i] = asciiLower(c)
	}
	return string(b)
}
