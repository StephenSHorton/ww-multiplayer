// Package stagetables is the co-op stage tables at runtime (#23;
// docs/08-coop-design.md §4.4, §6.2-6.3, §9 M3): which M1 tables
// (wwapi/tables) a --coop session runs with, and how they get there.
//
//   - `patch` builds the tables of the disc it writes, after writing it,
//     caches them by the patched main.dol's hash, and records that disc as
//     the one last patched (tables.RecordPatched), even when the tables
//     can't be built from it. That only reads the image, and a failure is
//     one warning line: the patched ISO is the same byte for byte, and
//     patch succeeds all the same.
//   - A session takes the tables of --coop-iso <path> when given (cached,
//     or built and cached), else those of the disc last patched
//     (tables.GetPatched): the one Dolphin most likely runs.
//   - Their identity (tables.Tables.Identity) is CO_HELLO's stageTblHash,
//     and the host's hub grants memory switches by their verdict
//     (hub.Config.SyncedSwitch, hub.SwitchPolicy). With no tables the
//     identity is 0, and the hub turns switch sync off for that player
//     (hub.TableFeatures), as it does when the identities differ.
//
// Wiring a session: load the tables once per process (mp-local's two
// Dolphins run one disc and share them), log the outcome, then
//
//	st, err := Load(iso)
//	Log(rep, st, err)
//	hub.Config{..., StageTblHash: st.Hash(), SyncedSwitch: st.SwitchVerdict()}
//	replica.Config{..., StageTblHash: st.Hash(), Policy: hub.SessionPolicy(st.SwitchVerdict())}
//
// The replica's policy is the session's (mods/coop/session:
// Session.policy), which the world is founded with too. st is nil after
// an error, and every method is nil-safe.
package stagetables

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/synctables"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
)

// CacheDir is the directory of the tables' cache and of the
// last-patched record. Tests point it at a temporary directory.
var CacheDir = tables.CacheDir

// ISOFlag names the disc image whose tables a --coop session uses.
const ISOFlag = "--coop-iso"

// ErrNoTables is Load's error when nothing names the
// disc: no --coop-iso, and no last-patched record (tables.ErrNoRecord).
// patch records the disc it writes whether or not its tables build, so
// this build has not patched on this machine, or patch could not open or
// record the disc it wrote (and said so).
var ErrNoTables = errors.New("no co-op stage tables for your disc: patch has recorded no disc on this machine. " +
	"Run patch on your vanilla ISO with this build (it records the disc it writes), or pass " + ISOFlag + " <the ISO Dolphin runs>")

// Tables are the M1 tables a co-op session runs with. A nil
// *Tables is a session without tables: identity 0, no memory
// switch synced.
type Tables struct {
	t    *tables.Tables
	id   uint64 // t.Identity()
	from string // where they came from, for the log
}

// Hash is the tables' identity, CO_HELLO's stageTblHash; 0 without tables.
func (st *Tables) Hash() uint64 {
	if st == nil {
		return 0
	}
	return st.id
}

// SyncedSwitch is the M1 verdict on memory switch no of save table t
// (synctables.SyncedMemorySwitch); false without tables.
func (st *Tables) SyncedSwitch(t, no int) bool {
	return st != nil && synctables.SyncedMemorySwitch(st.t, t, no)
}

// SwitchVerdict is SyncedSwitch as hub.Config.SyncedSwitch takes it: nil
// without tables, so the hub keeps every memory switch out.
func (st *Tables) SwitchVerdict() func(t, no int) bool {
	if st == nil {
		return nil
	}
	return st.SyncedSwitch
}

// Policy is M3's policy: world.DefaultPolicy with the memory switches of
// the verdict open (hub.SwitchPolicy), and no story bit. Without tables
// no memory switch is open. A --coop session no longer uses it: its world
// and replicas take hub.SessionPolicy, which opens the SyncMask's story
// bits too (mods/coop/session: Session.policy). save-merge --iso starts
// from it and adds the story bits (story.Policy), and the M3 session
// test plans with it.
func (st *Tables) Policy() *world.Policy {
	return hub.SwitchPolicy(nil, st.SwitchVerdict())
}

// ScopedVerdict is the M1 verdict on the switches SCOPED carries
// (synctables.SyncedScopedSwitch), as hub.Config.SyncedScoped takes
// it: nil without tables, so the hub keeps and relays none.
func (st *Tables) ScopedVerdict() func(table int, stage string, room, no int) bool {
	if st == nil || st.t == nil {
		return nil
	}
	t := st.t
	return func(table int, stage string, room, no int) bool {
		return synctables.SyncedScopedSwitch(t, table, stage, room, no)
	}
}

// KillVerdict is the M1 verdict on the kill bits SCOPED KILL carries
// (synctables.SyncedKill), as hub.Config.SyncedKill takes it: nil
// without tables, so the hub keeps and relays none.
func (st *Tables) KillVerdict() func(table int, stage string, room, setID int) bool {
	if st == nil || st.t == nil || st.t.Enemies == nil {
		return nil
	}
	t := st.t
	return func(table int, stage string, room, setID int) bool {
		return synctables.SyncedKill(t, table, stage, room, setID)
	}
}

// M1 is the tables themselves; nil without tables.
func (st *Tables) M1() *tables.Tables {
	if st == nil {
		return nil
	}
	return st.t
}

// From says where the tables came from, as the session's log line does.
func (st *Tables) From() string {
	if st == nil {
		return ""
	}
	return st.from
}

// SyncedCount is how many memory switches the verdict opens, and in how
// many save tables, and how many of them sync only while no Game Boy
// Advance is linked (synctables.GBAMemorySwitch).
func (st *Tables) SyncedCount() (switches, saveTables, gba int) {
	for t := range addr.TableCount {
		n := 0
		for no := range addr.MemBitSwitchCount {
			if st.SyncedSwitch(t, no) {
				n++
				if synctables.GBAMemorySwitch(st.t, t, no) {
					gba++
				}
			}
		}
		if n > 0 {
			switches += n
			saveTables++
		}
	}
	return switches, saveTables, gba
}

// Load finds the tables of the disc the session's Dolphin
// runs: iso's (--coop-iso) when given, from the cache or built from the
// image (and cached), otherwise those of the disc patch last wrote. It
// fails when neither gives tables, and the error says why and what to do:
// ErrNoTables when nothing names a disc, a build that fails on the
// disc (a *tables.BuildError, which patching that disc again doesn't
// change), or an image or record that can't be read. The session then
// runs without switch sync (Log). A rebuild reads the
// image's stage archives, a few seconds on a real disc, so a process
// loads them once.
func Load(iso string) (*Tables, error) {
	dir, dirErr := CacheDir()
	var (
		t    *tables.Tables
		from string
	)
	if iso != "" {
		if dirErr != nil {
			dir = "" // build without a cache
		}
		d, err := inject.OpenDisc(iso)
		if err != nil {
			return nil, fmt.Errorf("co-op stage tables: %s: %w", iso, err)
		}
		defer d.Close()
		var st tables.Stats
		t, st, err = tables.Get(d, dir)
		var be *tables.BuildError
		switch {
		case errors.As(err, &be):
			return nil, fmt.Errorf("co-op stage tables: this build can't make them from %s: %w. Patching again won't change that; "+
				"if it is your unmodified Wind Waker (GZLE01) image, or patch made it from one, please report this line", iso, err)
		case err != nil:
			return nil, fmt.Errorf("co-op stage tables: %s: %w", iso, err)
		}
		from = fmt.Sprintf("%s %s", iso, how(st))
	} else {
		if dirErr != nil {
			return nil, fmt.Errorf("co-op stage tables: %w, so patch's record of the disc it wrote can't be read; pass %s <the ISO Dolphin runs>", dirErr, ISOFlag)
		}
		var ps tables.PatchedStats
		var err error
		t, ps, err = tables.GetPatched(dir)
		var be *tables.BuildError
		switch {
		case errors.Is(err, tables.ErrNoRecord):
			return nil, ErrNoTables
		case errors.As(err, &be):
			// The record names the disc patch last wrote, which need not
			// be the one Dolphin runs.
			return nil, fmt.Errorf("co-op stage tables: this build can't make them from %s (the disc last patched, %s): %w. Patching that disc again won't change that; "+
				"if Dolphin runs another disc, run patch on it or pass %s <the ISO Dolphin runs>; "+
				"if patch made this one from your unmodified Wind Waker (GZLE01) image, please report this line",
				ps.Record.Image, ps.Record.At.Local().Format(time.DateTime), be, ISOFlag)
		case err != nil:
			return nil, fmt.Errorf("co-op stage tables: %w; run patch on your vanilla ISO again, or pass %s <the ISO Dolphin runs>", err, ISOFlag)
		}
		from = fmt.Sprintf("%s (the disc last patched, %s) %s", ps.Record.Image, ps.Record.At.Local().Format(time.DateTime), how(ps.Stats))
		if ps.Changed {
			from += "; the image has changed since then, and the record now names what it holds"
		}
	}
	id, err := t.Identity()
	if err != nil {
		return nil, fmt.Errorf("co-op stage tables: %w", err)
	}
	return &Tables{t: t, id: id, from: from}, nil
}

// how says how tables.Get came by tables: the cache, or a build.
func how(st tables.Stats) string {
	if st.Cached {
		return "from the cache"
	}
	how := fmt.Sprintf("built from the disc in %.1f s", (st.Identify + st.Read + st.Build).Seconds())
	switch {
	case st.Path == "":
		how += " (not cached: no config directory)"
	case st.SaveErr != nil:
		how += fmt.Sprintf(" (not cached: %v)", st.SaveErr)
	}
	return how
}

// Log says which tables a session runs with, or, after
// Load failed, that memory switches don't sync and how to
// fix it. The rest of co-op runs either way.
func Log(rep log.Reporter, st *Tables, err error) {
	if err != nil || st == nil {
		if err == nil {
			err = ErrNoTables
		}
		log.Logf(rep, log.Warn, "co-op: %s. Memory switches, room puzzles and kills are not synced this session; chests, items, maps and the rest are.", oneLine(err))
		return
	}
	n, ts, gba := st.SyncedCount()
	log.Logf(rep, log.OK, "co-op: stage tables %016x, %s: %d latching memory switch(es) in %d save table(s) sync (%d of them, shared with Tingle Tuner switches, only while no Game Boy Advance is linked).", st.id, st.from, n, ts, gba)
}

// TakeISO takes --coop-iso <path> or --coop-iso=<path> out of args
// (up to a bare "--", which ends the options) and returns the path and the
// rest, in order. Call it only once --coop is on: a command line without
// --coop keeps --coop-iso in its arguments, which then parse as they
// always have.
func TakeISO(args []string) (iso string, rest []string, err error) {
	rest = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return iso, append(rest, args[i:]...), nil
		}
		v, ok := strings.CutPrefix(a, ISOFlag+"=")
		switch {
		case a == ISOFlag:
			if i+1 >= len(args) || args[i+1] == "--" {
				return "", nil, fmt.Errorf("%s needs the path of the disc image Dolphin runs", ISOFlag)
			}
			i++
			v = args[i]
		case !ok:
			rest = append(rest, a)
			continue
		}
		if v == "" {
			return "", nil, fmt.Errorf("%s needs the path of the disc image Dolphin runs", ISOFlag)
		}
		if iso != "" && iso != v {
			return "", nil, fmt.Errorf("%s %s and %s %s: give one disc image", ISOFlag, iso, ISOFlag, v)
		}
		iso = v
	}
	return iso, rest, nil
}

// CacheForPatch builds and caches the co-op stage tables of
// image, the disc patch just wrote (or found already patched), and records
// it as the disc last patched, for --coop sessions, even when they can't
// be built. It reads image and writes only under CacheDir, and
// it never fails the patch: it prints one line on w, and a failure, a
// panic included, is a warning that says what it means for co-op.
func CacheForPatch(w io.Writer, image string) {
	start := time.Now()
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(w, "  co-op : WARNING: building the co-op stage tables failed (a bug; please report it: %v). The patched ISO is fine, but --coop sessions may run without memory switch sync.\n", r)
		}
	}()
	note, err := forPatch(image)
	var (
		be *tables.BuildError
		nr *notRecordedError
	)
	// A failed build reads the disc the same way each time, so the
	// session's rebuild and --coop-iso on this disc fail as this one did.
	// A record that can't be written leaves the one before it as it was,
	// and a session without --coop-iso takes that disc's tables.
	switch {
	case errors.As(err, &be) && errors.As(err, &nr):
		fmt.Fprintf(w, "  co-op : WARNING: this build can't make the co-op stage tables of this disc (%s), and the disc can't be recorded either (%s). "+
			"The patched ISO is fine, but --coop sessions can't take this disc's tables, and patching again or %s won't change that. "+
			"A session without %s may still take the tables of a disc recorded before; its first lines name that disc. "+
			"If you patched your unmodified Wind Waker (GZLE01) image, please report this line.\n", oneLine(be), oneLine(nr.err), ISOFlag, ISOFlag)
	case errors.As(err, &be):
		fmt.Fprintf(w, "  co-op : WARNING: this build can't make the co-op stage tables of this disc (%s). "+
			"The patched ISO is fine, but --coop sessions run without memory switch sync, and patching again or %s won't change that. "+
			"The disc is recorded, so a later build that can read it needs no new patch for its tables. "+
			"If you patched your unmodified Wind Waker (GZLE01) image, please report this line.\n", oneLine(err), ISOFlag)
	case err != nil:
		fmt.Fprintf(w, "  co-op : WARNING: no co-op stage tables for this disc (%s). The patched ISO is fine; --coop sessions will need %s to sync memory switches.\n", oneLine(err), ISOFlag)
	default:
		fmt.Fprintf(w, "  co-op : stage tables %s in %.1f s, for --coop sessions\n", note, time.Since(start).Seconds())
	}
}

// oneLine puts err's text on one line: a failed stage-table build names
// each archive that failed on a line of its own (stage.Load).
func oneLine(err error) string { return strings.ReplaceAll(err.Error(), "\n", "; ") }

// forPatch does CacheForPatch's work and says what it
// did. It records image even when its tables can't be built (Get's
// *tables.BuildError, returned once the disc is recorded, or with a
// *notRecordedError when it can't be) or cached: a session then reads the
// image again, and says why there are no tables rather than that no disc
// is recorded. Recording also replaces the record of a disc patched
// before, whose tables a session would otherwise take while Dolphin runs
// this one.
func forPatch(image string) (string, error) {
	dir, err := CacheDir()
	if err != nil {
		return "", err
	}
	d, err := inject.OpenDisc(image)
	if err != nil {
		return "", err
	}
	defer d.Close()
	t, st, err := tables.Get(d, dir)
	var be *tables.BuildError
	if errors.As(err, &be) {
		if rerr := tables.RecordPatched(dir, image, st.ID, time.Now()); rerr != nil {
			return "", fmt.Errorf("%w; and %w", err, &notRecordedError{rerr})
		}
		return "", err
	}
	if err != nil {
		return "", err
	}
	if err := tables.RecordPatched(dir, image, t.ID, time.Now()); err != nil {
		if st.SaveErr != nil {
			return "", fmt.Errorf("built, but not cached (%v) and not recorded: %w", st.SaveErr, err)
		}
		return "", err
	}
	switch {
	case st.SaveErr != nil:
		return fmt.Sprintf("built (not cached: %s; each --coop session builds them again)", oneLine(st.SaveErr)), nil
	case st.Cached:
		return "already cached", nil
	}
	return "built and cached", nil
}

// notRecordedError is RecordPatched's error in forPatch's, when
// the disc's tables can't be built and the disc can't be recorded either.
type notRecordedError struct{ err error }

func (e *notRecordedError) Error() string { return "the disc can't be recorded: " + e.err.Error() }

func (e *notRecordedError) Unwrap() error { return e.err }
