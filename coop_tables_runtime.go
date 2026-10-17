package main

// Co-op stage tables at runtime (#23; docs/08-coop-design.md §4.4, §6.2-6.3,
// §9 M3): which M1 tables (wwapi/tables) a --coop session runs
// with, and how they get there.
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
//	st, err := loadCoopStageTables(iso)
//	logCoopStageTables(rep, st, err)
//	hub.Config{..., StageTblHash: st.hash(), SyncedSwitch: st.switchVerdict()}
//	replica.Config{..., StageTblHash: st.hash(), Policy: hub.SessionPolicy(st.switchVerdict())}
//
// The replica's policy is the session's (coopSession.policy), which the
// world is founded with too. st is nil after an error, and every method
// is nil-safe.

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/synctables"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/internal/report"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
)

// coopTablesCacheDir is the directory of the tables' cache and of the
// last-patched record. Tests point it at a temporary directory.
var coopTablesCacheDir = tables.CacheDir

// coopISOFlag names the disc image whose tables a --coop session uses.
const coopISOFlag = "--coop-iso"

// errNoCoopTables is loadCoopStageTables's error when nothing names the
// disc: no --coop-iso, and no last-patched record (tables.ErrNoRecord).
// patch records the disc it writes whether or not its tables build, so
// this build has not patched on this machine, or patch could not open or
// record the disc it wrote (and said so).
var errNoCoopTables = errors.New("no co-op stage tables for your disc: patch has recorded no disc on this machine. " +
	"Run patch on your vanilla ISO with this build (it records the disc it writes), or pass " + coopISOFlag + " <the ISO Dolphin runs>")

// coopStageTables are the M1 tables a co-op session runs with. A nil
// *coopStageTables is a session without tables: identity 0, no memory
// switch synced.
type coopStageTables struct {
	t    *tables.Tables
	id   uint64 // t.Identity()
	from string // where they came from, for the log
}

// hash is the tables' identity, CO_HELLO's stageTblHash; 0 without tables.
func (st *coopStageTables) hash() uint64 {
	if st == nil {
		return 0
	}
	return st.id
}

// syncedSwitch is the M1 verdict on memory switch no of save table t
// (synctables.SyncedMemorySwitch); false without tables.
func (st *coopStageTables) syncedSwitch(t, no int) bool {
	return st != nil && synctables.SyncedMemorySwitch(st.t, t, no)
}

// switchVerdict is syncedSwitch as hub.Config.SyncedSwitch takes it: nil
// without tables, so the hub keeps every memory switch out.
func (st *coopStageTables) switchVerdict() func(t, no int) bool {
	if st == nil {
		return nil
	}
	return st.syncedSwitch
}

// policy is M3's policy: world.DefaultPolicy with the memory switches of
// the verdict open (hub.SwitchPolicy), and no story bit. Without tables
// no memory switch is open. A --coop session no longer uses it: its world
// and replicas take hub.SessionPolicy, which opens the SyncMask's story
// bits too (coopSession.policy). save-merge --iso starts from it and adds
// the story bits (story.Policy), and the M3 session test plans with it.
func (st *coopStageTables) policy() *world.Policy {
	return hub.SwitchPolicy(nil, st.switchVerdict())
}

// syncedCount is how many memory switches the verdict opens, and in how
// many save tables, and how many of them sync only while no Game Boy
// Advance is linked (synctables.GBAMemorySwitch).
func (st *coopStageTables) syncedCount() (switches, saveTables, gba int) {
	for t := range addr.TableCount {
		n := 0
		for no := range addr.MemBitSwitchCount {
			if st.syncedSwitch(t, no) {
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

// loadCoopStageTables finds the tables of the disc the session's Dolphin
// runs: iso's (--coop-iso) when given, from the cache or built from the
// image (and cached), otherwise those of the disc patch last wrote. It
// fails when neither gives tables, and the error says why and what to do:
// errNoCoopTables when nothing names a disc, a build that fails on the
// disc (a *tables.BuildError, which patching that disc again doesn't
// change), or an image or record that can't be read. The session then
// runs without switch sync (logCoopStageTables). A rebuild reads the
// image's stage archives, a few seconds on a real disc, so a process
// loads them once.
func loadCoopStageTables(iso string) (*coopStageTables, error) {
	dir, dirErr := coopTablesCacheDir()
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
		from = fmt.Sprintf("%s %s", iso, coopTablesHow(st))
	} else {
		if dirErr != nil {
			return nil, fmt.Errorf("co-op stage tables: %w, so patch's record of the disc it wrote can't be read; pass %s <the ISO Dolphin runs>", dirErr, coopISOFlag)
		}
		var ps tables.PatchedStats
		var err error
		t, ps, err = tables.GetPatched(dir)
		var be *tables.BuildError
		switch {
		case errors.Is(err, tables.ErrNoRecord):
			return nil, errNoCoopTables
		case errors.As(err, &be):
			// The record names the disc patch last wrote, which need not
			// be the one Dolphin runs.
			return nil, fmt.Errorf("co-op stage tables: this build can't make them from %s (the disc last patched, %s): %w. Patching that disc again won't change that; "+
				"if Dolphin runs another disc, run patch on it or pass %s <the ISO Dolphin runs>; "+
				"if patch made this one from your unmodified Wind Waker (GZLE01) image, please report this line",
				ps.Record.Image, ps.Record.At.Local().Format(time.DateTime), be, coopISOFlag)
		case err != nil:
			return nil, fmt.Errorf("co-op stage tables: %w; run patch on your vanilla ISO again, or pass %s <the ISO Dolphin runs>", err, coopISOFlag)
		}
		from = fmt.Sprintf("%s (the disc last patched, %s) %s", ps.Record.Image, ps.Record.At.Local().Format(time.DateTime), coopTablesHow(ps.Stats))
		if ps.Changed {
			from += "; the image has changed since then, and the record now names what it holds"
		}
	}
	id, err := t.Identity()
	if err != nil {
		return nil, fmt.Errorf("co-op stage tables: %w", err)
	}
	return &coopStageTables{t: t, id: id, from: from}, nil
}

// coopTablesHow says how tables.Get came by tables: the cache, or a build.
func coopTablesHow(st tables.Stats) string {
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

// logCoopStageTables says which tables a session runs with, or, after
// loadCoopStageTables failed, that memory switches don't sync and how to
// fix it. The rest of co-op runs either way.
func logCoopStageTables(rep report.Reporter, st *coopStageTables, err error) {
	if err != nil || st == nil {
		if err == nil {
			err = errNoCoopTables
		}
		report.Logf(rep, report.Warn, "co-op: %s. Memory switches, room puzzles and kills are not synced this session; chests, items, maps and the rest are.", oneLine(err))
		return
	}
	n, ts, gba := st.syncedCount()
	report.Logf(rep, report.OK, "co-op: stage tables %016x, %s: %d latching memory switch(es) in %d save table(s) sync (%d of them, shared with Tingle Tuner switches, only while no Game Boy Advance is linked).", st.id, st.from, n, ts, gba)
}

// takeCoopISO takes --coop-iso <path> or --coop-iso=<path> out of args
// (up to a bare "--", which ends the options) and returns the path and the
// rest, in order. Call it only once --coop is on: a command line without
// --coop keeps --coop-iso in its arguments, which then parse as they
// always have.
func takeCoopISO(args []string) (iso string, rest []string, err error) {
	rest = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return iso, append(rest, args[i:]...), nil
		}
		v, ok := strings.CutPrefix(a, coopISOFlag+"=")
		switch {
		case a == coopISOFlag:
			if i+1 >= len(args) || args[i+1] == "--" {
				return "", nil, fmt.Errorf("%s needs the path of the disc image Dolphin runs", coopISOFlag)
			}
			i++
			v = args[i]
		case !ok:
			rest = append(rest, a)
			continue
		}
		if v == "" {
			return "", nil, fmt.Errorf("%s needs the path of the disc image Dolphin runs", coopISOFlag)
		}
		if iso != "" && iso != v {
			return "", nil, fmt.Errorf("%s %s and %s %s: give one disc image", coopISOFlag, iso, coopISOFlag, v)
		}
		iso = v
	}
	return iso, rest, nil
}

// cacheCoopTablesForPatch builds and caches the co-op stage tables of
// image, the disc patch just wrote (or found already patched), and records
// it as the disc last patched, for --coop sessions, even when they can't
// be built. It reads image and writes only under coopTablesCacheDir, and
// it never fails the patch: it prints one line on w, and a failure, a
// panic included, is a warning that says what it means for co-op.
func cacheCoopTablesForPatch(w io.Writer, image string) {
	start := time.Now()
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(w, "  co-op : WARNING: building the co-op stage tables failed (a bug; please report it: %v). The patched ISO is fine, but --coop sessions may run without memory switch sync.\n", r)
		}
	}()
	note, err := coopTablesForPatch(image)
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
			"If you patched your unmodified Wind Waker (GZLE01) image, please report this line.\n", oneLine(be), oneLine(nr.err), coopISOFlag, coopISOFlag)
	case errors.As(err, &be):
		fmt.Fprintf(w, "  co-op : WARNING: this build can't make the co-op stage tables of this disc (%s). "+
			"The patched ISO is fine, but --coop sessions run without memory switch sync, and patching again or %s won't change that. "+
			"The disc is recorded, so a later build that can read it needs no new patch for its tables. "+
			"If you patched your unmodified Wind Waker (GZLE01) image, please report this line.\n", oneLine(err), coopISOFlag)
	case err != nil:
		fmt.Fprintf(w, "  co-op : WARNING: no co-op stage tables for this disc (%s). The patched ISO is fine; --coop sessions will need %s to sync memory switches.\n", oneLine(err), coopISOFlag)
	default:
		fmt.Fprintf(w, "  co-op : stage tables %s in %.1f s, for --coop sessions\n", note, time.Since(start).Seconds())
	}
}

// oneLine puts err's text on one line: a failed stage-table build names
// each archive that failed on a line of its own (stage.Load).
func oneLine(err error) string { return strings.ReplaceAll(err.Error(), "\n", "; ") }

// coopTablesForPatch does cacheCoopTablesForPatch's work and says what it
// did. It records image even when its tables can't be built (Get's
// *tables.BuildError, returned once the disc is recorded, or with a
// *notRecordedError when it can't be) or cached: a session then reads the
// image again, and says why there are no tables rather than that no disc
// is recorded. Recording also replaces the record of a disc patched
// before, whose tables a session would otherwise take while Dolphin runs
// this one.
func coopTablesForPatch(image string) (string, error) {
	dir, err := coopTablesCacheDir()
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

// notRecordedError is RecordPatched's error in coopTablesForPatch's, when
// the disc's tables can't be built and the disc can't be recorded either.
type notRecordedError struct{ err error }

func (e *notRecordedError) Error() string { return "the disc can't be recorded: " + e.err.Error() }

func (e *notRecordedError) Unwrap() error { return e.err }
