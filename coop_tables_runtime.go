package main

// Co-op stage tables at runtime (#23; docs/08-coop-design.md §4.4, §6.2-6.3,
// §9 M3): which M1 tables (internal/coop/tables) a --coop session runs
// with, and how they get there.
//
//   - `patch` builds the tables of the disc it writes, after writing it,
//     caches them by the patched main.dol's hash, and records that disc as
//     the one last patched (tables.RecordPatched). That only reads the
//     image, and a failure is one warning line: the patched ISO is the
//     same byte for byte, and patch succeeds all the same.
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
	"io/fs"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/internal/report"
)

// coopTablesCacheDir is the directory of the tables' cache and of the
// last-patched record. Tests point it at a temporary directory.
var coopTablesCacheDir = tables.CacheDir

// coopISOFlag names the disc image whose tables a --coop session uses.
const coopISOFlag = "--coop-iso"

// errNoCoopTables is loadCoopStageTables's error when nothing names the
// disc: no --coop-iso, and no disc patched since M3.
var errNoCoopTables = errors.New("no co-op stage tables for your disc: patch has not recorded one. " +
	"Run patch on your vanilla ISO again (it records the disc it writes), or pass " + coopISOFlag + " <the ISO Dolphin runs>")

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
// (tables.Tables.SyncedMemorySwitch); false without tables.
func (st *coopStageTables) syncedSwitch(t, no int) bool {
	return st != nil && st.t.SyncedMemorySwitch(t, no)
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
// many save tables.
func (st *coopStageTables) syncedCount() (switches, saveTables int) {
	for t := range addr.TableCount {
		n := 0
		for no := range addr.MemBitSwitchCount {
			if st.syncedSwitch(t, no) {
				n++
			}
		}
		if n > 0 {
			switches += n
			saveTables++
		}
	}
	return switches, saveTables
}

// loadCoopStageTables finds the tables of the disc the session's Dolphin
// runs: iso's (--coop-iso) when given, from the cache or built from the
// image (and cached), otherwise those of the disc patch last wrote. It
// fails when neither gives tables (errNoCoopTables when nothing names a
// disc); the session then runs without switch sync (logCoopStageTables).
// A rebuild reads the image's stage archives, a few seconds on a real
// disc, so a process loads them once.
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
		if err != nil {
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
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil, errNoCoopTables
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
		report.Logf(rep, report.Warn, "co-op: %v. Memory switches, room puzzles and kills are not synced this session; chests, items, maps and the rest are.", err)
		return
	}
	n, ts := st.syncedCount()
	report.Logf(rep, report.OK, "co-op: stage tables %016x, %s: %d latching memory switch(es) in %d save table(s) sync.", st.id, st.from, n, ts)
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
// it as the disc last patched, for --coop sessions. It reads image and
// writes only under coopTablesCacheDir, and it never fails the patch: any
// problem, a panic included, is one warning line on w.
func cacheCoopTablesForPatch(w io.Writer, image string) {
	start := time.Now()
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(w, "  co-op : WARNING: building the co-op stage tables failed (a bug; please report it: %v). The patched ISO is fine; --coop sessions will need %s to sync memory switches.\n", r, coopISOFlag)
		}
	}()
	note, err := coopTablesForPatch(image)
	if err != nil {
		fmt.Fprintf(w, "  co-op : WARNING: no co-op stage tables for this disc (%v). The patched ISO is fine; --coop sessions will need %s to sync memory switches.\n", err, coopISOFlag)
		return
	}
	fmt.Fprintf(w, "  co-op : stage tables %s in %.1f s, for --coop sessions\n", note, time.Since(start).Seconds())
}

// coopTablesForPatch does cacheCoopTablesForPatch's work and says what it
// did.
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
	if err != nil {
		return "", err
	}
	if st.SaveErr != nil {
		return "", fmt.Errorf("built, but not cached: %w", st.SaveErr)
	}
	if err := tables.RecordPatched(dir, image, t.ID, time.Now()); err != nil {
		return "", err
	}
	note := "built and cached"
	if st.Cached {
		note = "already cached"
	}
	return note, nil
}
