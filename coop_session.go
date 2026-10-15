package main

// Co-op sessions (#23; docs/08-coop-design.md §4.1-4.2, §5, §6.7, §9
// M2-M4): what --coop adds to host, join and mp-local.
//
//   - host and mp-local run the CoopHub (internal/coop/hub) on their
//     server. It serves one world: the only one on disk, the one
//     --coop=<id> names, or, with --coop=new, one founded from the host's
//     loaded, idle file (§5.3). It grants memory switch sync by the M1
//     stage tables of the session's disc (coop_tables_runtime.go:
//     --coop-iso, or the disc patch last wrote), and the live-switch
//     opt-in with --coop-live-switches (§6.3);
//   - every side backs up its memory cards, read-only, when the session
//     starts (internal/coop/backup, §5.4). Without that backup no tag is
//     written this session;
//   - each Dolphin gets a coop-sync pipeline beside broadcast-pose and
//     puppet-sync, with its own Dolphin handle by PID and its own role-1
//     connection (network.RunCoop). Its replica (internal/coop/replica)
//     reads a snapshot ten times a second, runs the gates and the differ,
//     sends GAINS and, from M3, writes the world's progress into its game
//     through a FrameWriter and the WriteGuard (§4.2): the stage progress
//     bits, the latching memory switches, the ocean, charts and sea map,
//     and from M4 the story bits and event registers at stage changes,
//     the inventory when idle, and the small keys, max life and max magic
//     its own flags give (derived from the M1 tables, §6.4-6.6). Too many
//     faults turn the writes off by themselves, said once, and pose sync
//     goes on (auto-disable). --coop-dry-run keeps M2's dry run: the plan
//     is only logged (§5.1 principle 5);
//   - the replica's two tag writes, a bind and a leaked tag's scrub, are
//     asked on the command line (y/N on stdin; --yes answers yes) and made
//     only after the yes, behind the gates (internal/coop/bind);
//   - stdin also takes the session's commands (coop_session_console.go):
//     /refresh and /warp, the escape hatches (§6.7), /coop off and on,
//     /status and /help.
//
// Without --coop none of this runs: host, join and mp-local open the same
// connections, send the same frames and print the same lines as before.
// The TUI does not expose co-op yet; M8 adds its panel and prompts, so its
// sessions pass coopFlags{}.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/backup"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/derive"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/mem"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/snap"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/internal/network"
	"github.com/StephenSHorton/ww-multiplayer/internal/report"
)

// coopFlags are the co-op options of host, join and mp-local. The zero
// value is co-op off.
type coopFlags struct {
	on bool
	// world is the world a host serves: "" for the only one on disk,
	// coopWorldNew to found one from the host's file, or a world ID.
	world string
	// merge is --coop-merge (§5.3): a file with progress the world lacks,
	// or one far behind it, may bind, and its progress joins the world.
	merge bool
	// yes is --yes: every bind and scrub question is answered yes.
	yes bool
	// dryRun is --coop-dry-run: M2's dry run. This side's replicas write
	// no game data but a binding tag after a yes; the plan is logged.
	dryRun bool
	// liveSwitches is --coop-live-switches (§6.3), the host's choice: the
	// current table's memory switches apply when IDLE, once the room hold
	// has passed, not at the next room change.
	liveSwitches bool
	// iso is --coop-iso: the disc image whose M1 stage tables the session
	// uses; "" for the disc patch last wrote.
	iso string
}

// coopWorldNew is --coop=new: found a world from the host's loaded file.
const coopWorldNew = "new"

// coopFlagsUsage is the co-op part of host's and mp-local's usage lines;
// coopJoinFlagsUsage is join's, which leaves the world and the host's
// choices out.
const (
	coopFlagsUsage     = "[--coop[=new|<world-id>]] [--coop-merge] [--coop-live-switches] [--coop-dry-run] [--coop-iso <iso>] [--yes]"
	coopJoinFlagsUsage = "[--coop] [--coop-dry-run] [--coop-iso <iso>] [--yes]"
)

// extractCoopFlags takes the co-op options out of args (in any position
// before a bare "--", which ends the options) and returns the rest, in
// order, for the command's own parser. When args hold no --coop at all,
// they are returned untouched and co-op is off, so a command line without
// co-op parses exactly as it did before: --coop-merge, --coop-dry-run,
// --coop-live-switches, --coop-iso or --yes alone stay in rest, where the
// command's parser treats them as it always has.
func extractCoopFlags(args []string) (coopFlags, []string, error) {
	var f coopFlags
	if !slices.ContainsFunc(coopOptionArgs(args), func(a string) bool {
		return a == "--coop" || strings.HasPrefix(a, "--coop=")
	}) {
		return f, args, nil
	}
	iso, args, err := takeCoopISO(args)
	if err != nil {
		return coopFlags{}, nil, err
	}
	f.iso = iso
	rest := make([]string, 0, len(args))
	for i, a := range args {
		switch {
		case a == "--":
			return f, append(rest, args[i:]...), nil
		case a == "--coop":
			f.on = true
		case strings.HasPrefix(a, "--coop="):
			v := strings.ToLower(strings.TrimPrefix(a, "--coop="))
			if v != coopWorldNew {
				if _, err := bind.ParseWorldID(v); err != nil {
					return coopFlags{}, nil, fmt.Errorf("%s: say --coop=new, or a world ID (16 hex digits, as coop-world list prints them)", a)
				}
			}
			if f.world != "" && f.world != v {
				return coopFlags{}, nil, fmt.Errorf("--coop=%s and --coop=%s: give one world", f.world, v)
			}
			f.on, f.world = true, v
		case a == "--coop-merge":
			f.merge = true
		case a == "--coop-dry-run":
			f.dryRun = true
		case a == "--coop-live-switches":
			f.liveSwitches = true
		case a == "--yes":
			f.yes = true
		default:
			rest = append(rest, a)
		}
	}
	return f, rest, nil
}

// coopOptionArgs is args up to a bare "--".
func coopOptionArgs(args []string) []string {
	if i := slices.Index(args, "--"); i >= 0 {
		return args[:i]
	}
	return args
}

// forJoin checks f for join: a joiner serves no world, so the world,
// --coop-merge and --coop-live-switches are the host's to choose.
// --coop-dry-run and --coop-iso are each side's own.
func (f coopFlags) forJoin() error {
	switch {
	case f.world != "":
		return fmt.Errorf("--coop=%s: the host picks the world; join with --coop", f.world)
	case f.merge:
		return errors.New("--coop-merge is the host's choice, not the joiner's")
	case f.liveSwitches:
		return errors.New("--coop-live-switches is the host's choice, not the joiner's")
	}
	return nil
}

// coopEnv is where a co-op session keeps and finds things. Production
// uses defaultCoopEnv; the tests point it at temporary directories and a
// FakeGame.
type coopEnv struct {
	worlds  string // the worlds root; "" is world.Dir()
	backups string // the memory-card backup root; "" is backup.Dir()
	// userDirs are the Dolphin user directories whose cards are backed up
	// at session start.
	userDirs []string
	// openMem opens the Dolphin a host founds its world from, and returns
	// how to close it.
	openMem func() (mem.Mem, func(), error)
	// tick waits between the snapshots of a founding (coopTick).
	tick func()
	// input, if set, replaces stdin as the source of y/N answers and
	// commands.
	input io.Reader
	// tables loads the M1 stage tables of the disc iso names, "" for the
	// one patch last wrote (loadCoopStageTables). Nil runs the session
	// without tables, saying nothing of them: no memory switch syncs.
	tables func(iso string) (*coopStageTables, error)
}

// defaultCoopEnv is the production environment for a session whose host
// Dolphin is pid (0: the one WW_DOLPHIN_INDEX / WW_DOLPHIN_PID select).
// Both of the tool's Dolphin user directories are backed up: a missing one
// has no cards, and a developer running two Dolphins has both in use.
func defaultCoopEnv(pid uint32) coopEnv {
	ud1, ud2 := backup.UserDirs()
	return coopEnv{
		userDirs: []string{ud1, ud2},
		openMem: func() (mem.Mem, func(), error) {
			d, err := openDolphin(pid)
			if err != nil {
				return nil, nil, err
			}
			return mem.NewDolphinMem(d), d.Close, nil
		},
		tick:   coopTick,
		tables: loadCoopStageTables,
	}
}

func (e coopEnv) worldsRoot() (string, error) {
	if e.worlds != "" {
		return e.worlds, nil
	}
	return world.Dir()
}

// coopSession is one host, join or mp-local session's co-op side: the
// hub (host and mp-local), the memory-card backup, the prompts, and what
// the session's coop-sync pipelines share.
type coopSession struct {
	flags coopFlags
	// world is the hub's world, so the host's own replicas check their
	// files against it; zero on a joiner, whose replica learns it from
	// the host's welcome.
	world bind.WorldID
	hub   *hub.Hub
	store *world.Store // the hub's world; the hub is its only writer
	dir   string       // the hub's world directory, which holds the session marker
	// noTags says why no tag may be written this session (the backup
	// failed); nil when tag writes may be offered.
	noTags error
	prompt *coopPrompter
	rep    report.Reporter
	// tables are the session's M1 stage tables, nil without (every
	// method is nil-safe).
	tables *coopStageTables
	// console runs the commands typed on stdin for the session's
	// coop-sync pipelines.
	console *coopConsole
	// poses are the puppet-sync clients of the session's Dolphins, by
	// player name: where a warp finds the partner's position.
	posesMu sync.Mutex
	poses   map[string]*network.Client
}

// startCoopSession begins a joiner's co-op side, and is the first step of
// a host's: it backs up the memory cards, and without a backup it turns
// tag writes off for the session (§5.4: the card is backed up before a
// file is bound).
//
// It also loads the session's M1 stage tables (env.tables): a host's hub
// grants memory switches by them, and a writing replica sends their
// identity in CO_HELLO. A dry-run joiner, which M2 ran without them,
// does not.
func startCoopSession(rep report.Reporter, f coopFlags, env coopEnv) *coopSession {
	cs := startCoopSide(rep, f, env, false)
	cs.console.help()
	return cs
}

// startCoopSide is startCoopSession for a host (host: its hub needs the
// tables even in a dry run) or a joiner.
func startCoopSide(rep report.Reporter, f coopFlags, env coopEnv, host bool) *coopSession {
	cs := &coopSession{flags: f, rep: rep, prompt: newCoopPrompter(f.yes, env.input), poses: map[string]*network.Client{}}
	cs.console = newCoopConsole(rep, cs.prompt.cmds)
	b, err := backup.Create(env.userDirs, backup.Options{Root: env.backups, Label: "co-op session start"})
	if err != nil {
		cs.noTags = err
		report.Logf(rep, report.Warn, "co-op: could not back up the memory cards: %v. No co-op tag is written this session (set USER_DIR_1 if Dolphin keeps its user folder elsewhere).", err)
	} else {
		report.Logf(rep, report.OK, "co-op: backed up %d memory-card file(s) to %s (`coop-world restore %s --yes`, with Dolphin closed, puts them back). Check that these are your cards:", len(b.Files), b.Dir, b.ID)
		logBackupCards(rep, b)
	}
	if env.tables != nil && (host || !f.dryRun) {
		st, err := env.tables(f.iso)
		logCoopStageTables(rep, st, err)
		cs.tables = st
	}
	if !f.dryRun {
		logCoopProgression(rep, cs.m1())
		logCoopPuzzles(rep, cs.m1())
	}
	return cs
}

// m1 is the session's M1 tables, nil without.
func (cs *coopSession) m1() *tables.Tables {
	if cs.tables == nil {
		return nil
	}
	return cs.tables.t
}

// policy is what the session's world takes and its replicas propose,
// plan and write: the hub's (hub.SessionPolicy), with the story bits of
// the SyncMask and the memory switches of the M1 verdict open.
func (cs *coopSession) policy() *world.Policy {
	return hub.SessionPolicy(cs.tables.switchVerdict())
}

// logCoopProgression says what of M4's shared progression (§9 M4) this
// side's replicas write, by its own M1 tables t: the story bits, the
// inventory and max magic need none; the small keys of each dungeon and
// max life are derived from t, and each one t does not allow is named
// with why (no tables, or a table whose M1 invariant fails). What the
// host does not grant the replica says on its welcome.
func logCoopProgression(rep report.Reporter, t *tables.Tables) {
	const synced = "story bits and event registers (written at stage changes), items, songs and capacities (when idle) and max magic"
	if t == nil {
		report.Logf(rep, report.OK, "co-op: shared progression this session: %s; without co-op stage tables no small keys or max hearts are derived.", synced)
		return
	}
	st := derive.New(derive.Config{Tables: t}).Status()
	var on, off []string
	if st.NoKeyTable {
		off = append(off, "small keys are not derived: the stage tables have no key table")
	}
	for _, k := range st.Keys {
		switch {
		case k.On:
			on = append(on, k.Dungeon)
		case k.Dungeon != "":
			off = append(off, fmt.Sprintf("the small keys of %s are not derived: %s", k.Name(), k.Why))
		}
	}
	var derived []string
	if len(on) > 0 {
		derived = append(derived, "the small keys of "+strings.Join(on, ", "))
	}
	if st.Life {
		derived = append(derived, "max hearts")
	} else {
		off = append(off, "max hearts are not derived: "+st.LifeWhy)
	}
	line := synced
	if len(derived) > 0 {
		line += "; derived from the stage tables: " + strings.Join(derived, " and ")
	}
	report.Logf(rep, report.OK, "co-op: shared progression this session: %s.", line)
	for _, o := range off {
		report.Logf(rep, report.Warn, "co-op: %s.", o)
	}
}

// logBackupCards lists a backup's cards, each with the files copied from
// it, and warns when Dolphin's settings (Dolphin.ini, or Dolphin's default
// for a slot it doesn't set) select none of them: the card locations are
// Dolphin's (backup.Locate), so the owner checks them (§8.2).
func logBackupCards(rep report.Reporter, b *backup.Backup) {
	inUse := false
	for i, c := range b.Cards {
		inUse = inUse || c.InUse == backup.UseYes
		var files []backup.File
		for _, f := range b.Files {
			if f.Card == i {
				files = append(files, f)
			}
		}
		switch {
		case len(files) == 1 && files[0].Source == c.Path:
			report.Logf(rep, report.Info, "co-op:   %s, %d bytes", c, files[0].Size)
		case len(files) == 0:
			report.Logf(rep, report.Info, "co-op:   %s: no Wind Waker save in it", c)
		default:
			report.Logf(rep, report.Info, "co-op:   %s:", c)
			for _, f := range files {
				report.Logf(rep, report.Info, "co-op:     %s, %d bytes", f.Source, f.Size)
			}
		}
	}
	if !inUse {
		report.Logf(rep, report.Warn, "co-op: Dolphin's settings (Dolphin.ini, or Dolphin's defaults where it sets none: a GCI folder in slot A) select none of these cards. If your Dolphin keeps its user folder elsewhere (a portable User folder, say), set USER_DIR_1 (and USER_DIR_2) to it, or this backup misses your card.")
	}
}

// startCoopHost begins a host's co-op side (host and mp-local): the
// backup, the world (selectCoopWorld, or a founding) and the hub, which
// the caller sets as its server's Coop before Start. srvLog gets the
// hub's lines, beside the server's own. Stop it with stop.
func startCoopHost(ctx context.Context, rep report.Reporter, srvLog func(string), f coopFlags, env coopEnv) (*coopSession, error) {
	cs := startCoopSide(rep, f, env, true)
	root, err := env.worldsRoot()
	if err != nil {
		return nil, err
	}
	opts := world.Options{Logf: func(format string, args ...any) {
		rep.Log(report.Warn, "co-op: world file: "+fmt.Sprintf(format, args...))
	}}
	var st *world.Store
	if f.world == coopWorldNew {
		m, closeMem, err := env.openMem()
		if err != nil {
			return nil, fmt.Errorf("co-op: founding a world: %w", err)
		}
		w, err := foundCoopWorld(ctx, rep, m, env.tick, cs.policy())
		closeMem()
		if err != nil {
			return nil, err
		}
		if st, err = world.Create(root, w, opts); err != nil {
			return nil, err
		}
	} else {
		id, err := selectCoopWorld(root, f.world)
		if err != nil {
			return nil, err
		}
		var lr world.LoadReport
		if st, lr, err = world.Open(root, id, opts); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("co-op: no world %s in %s (coop-world list shows the worlds)", id, root)
			}
			return nil, fmt.Errorf("co-op: opening world %s: %w", id, err)
		}
		logWorldLoad(rep, lr)
	}
	features := hub.DefaultFeatures
	if f.liveSwitches {
		features |= hub.FeatureLiveSwitches
	}
	h, err := hub.New(hub.Config{
		Store:        st,
		StageTblHash: cs.tables.hash(),
		SyncedSwitch: cs.tables.switchVerdict(),
		SyncedScoped: cs.tables.scopedVerdict(),
		Features:     features,
		Merge:        f.merge,
		Logf:         func(format string, args ...any) { srvLog(fmt.Sprintf(format, args...)) },
	})
	if err != nil {
		st.Stop()
		return nil, err
	}
	cs.hub, cs.store, cs.world, cs.dir = h, st, bind.WorldID(st.ID()), st.Dir()
	cs.console.setHub(h.Status)
	if prev, had, err := writeCoopMarker(cs.dir); err != nil {
		report.Logf(rep, report.Warn, "co-op: %v", err)
	} else if had {
		switch use, why := prev.state(); use {
		case markerStale:
			report.Logf(rep, report.Warn, "co-op: world %s was marked in use (%s), but that process has ended: its last session did not end cleanly", cs.world, prev)
		case markerSelf:
			report.Logf(rep, report.Warn, "co-op: world %s was marked in use by this process (%s): its earlier session here did not stop", cs.world, prev)
		case markerLive:
			if why == "" {
				report.Logf(rep, report.Warn, "co-op: world %s is marked in use by a process that still runs (%s): if that is another host serving it, stop one of them, or one host's changes are lost", cs.world, prev)
			} else {
				report.Logf(rep, report.Warn, "co-op: world %s was marked in use (%s), and a process with pid %d runs, but %s, so it may not be that host: if another host serves it, stop one of them, or one host's changes are lost", cs.world, prev, prev.pid, why)
			}
		default:
			report.Logf(rep, report.Warn, "co-op: world %s was marked in use (%s), and whether that session still runs can't be told (%s): if another host serves it, stop one of them, or one host's changes are lost", cs.world, prev, why)
		}
	}
	merge := ""
	if f.merge {
		merge = "; --coop-merge: files with progress the world lacks may bind"
	}
	if f.dryRun {
		report.Logf(rep, report.OK, "Co-op world %s at seq %d (dry run: planned writes are logged, not made; only a binding tag is ever written, after a yes%s).", cs.world, st.Seq(), merge)
		cs.console.help()
		return cs, nil
	}
	live := ""
	if f.liveSwitches {
		live = "; --coop-live-switches: the current table's switches apply when idle, once every shown room's actors have run 2 s"
	}
	report.Logf(rep, report.OK, "Co-op world %s at seq %d: the world's chests, pickups, rooms, dungeon items, latching switches, ocean, charts, story bits and items are written into each bound game, and its small keys, max hearts and max magic derived (--coop-dry-run only logs them%s%s).", cs.world, st.Seq(), merge, live)
	cs.console.help()
	return cs, nil
}

// stop ends the hub, which writes the world, and removes the session
// marker. Nil-safe, for a session without co-op.
func (cs *coopSession) stop() {
	if cs == nil || cs.hub == nil {
		return
	}
	if err := cs.hub.Stop(); err != nil {
		report.Logf(cs.rep, report.Err, "co-op: %v", err)
	}
	removeCoopMarker(cs.dir)
	cs.hub = nil
}

// logWorldLoad says how a world came off the disk when it took more than
// reading world.wwco.
func logWorldLoad(rep report.Reporter, lr world.LoadReport) {
	if lr.FellBack() {
		report.Logf(rep, report.Warn, "co-op: the world file was missing or damaged; loaded the backup %s instead (%s)", lr.From, errors.Join(lr.Skipped...))
	}
	if lr.Replayed > 0 {
		report.Logf(rep, report.Info, "co-op: replayed %d journal record(s) the world file had not caught up with", lr.Replayed)
	}
	if lr.BadLines > 0 {
		report.Logf(rep, report.Warn, "co-op: skipped %d damaged journal line(s)", lr.BadLines)
	}
	if lr.Stripped > 0 {
		report.Logf(rep, report.Warn, "co-op: dropped %d bit(s) this build's class table does not share", lr.Stripped)
	}
}

// selectCoopWorld picks the world a host serves under root: want, a world
// ID, or, for "", the only world there is.
func selectCoopWorld(root, want string) (world.ID, error) {
	if want != "" {
		id, err := bind.ParseWorldID(want)
		return world.ID(id), err
	}
	ws, err := listCoopWorlds(root)
	if err != nil {
		return world.ID{}, err
	}
	switch len(ws) {
	case 0:
		return world.ID{}, fmt.Errorf("co-op: no world in %s yet: found one from your loaded save file with --coop=new", root)
	case 1:
		return ws[0].id, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "co-op: %d worlds in %s; say which with --coop=<world-id>:", len(ws), root)
	for _, w := range ws {
		fmt.Fprintf(&b, "\n  %s", w)
	}
	return world.ID{}, errors.New(b.String())
}

// coopWorldInfo is one world on disk, as coop-world list and a host's
// world choice show it.
type coopWorldInfo struct {
	id      world.ID
	created time.Time
	seq     uint64
	tombs   int             // tombstoned bits
	from    string          // the file it loads from, when not world.wwco
	err     error           // why it can't be loaded
	marker  *coopMarker     // its session marker, if any
	use     coopMarkerState // what the marker says, judged as the list is made
	useWhy  string          // why use is markerUnknown, or markerLive by pid alone (coopMarker.state)
}

func (w coopWorldInfo) String() string {
	if w.err != nil {
		return fmt.Sprintf("%s  unreadable: %v", w.id, w.err)
	}
	s := fmt.Sprintf("%s  founded %s, seq %d", w.id, w.created.Local().Format(time.DateTime), w.seq)
	if w.tombs > 0 {
		s += fmt.Sprintf(", %d blocked bit(s)", w.tombs)
	}
	if w.from != "" {
		s += ", loads from " + w.from
	}
	if m := w.marker; m != nil {
		switch w.use {
		case markerStale:
			s += fmt.Sprintf(", stale session marker (%s; that process has ended)", m)
		case markerUnknown:
			s += fmt.Sprintf(", in use?: %s (can't tell: %s)", m, w.useWhy)
		default:
			s += fmt.Sprintf(", in use: %s", m)
		}
	}
	return s
}

// listCoopWorlds lists the worlds under root, newest first, reading each
// without changing anything (world.Load). A root that does not exist has
// none.
func listCoopWorlds(root string) ([]coopWorldInfo, error) {
	ents, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("co-op: %w", err)
	}
	var out []coopWorldInfo
	for _, e := range ents {
		id, err := world.ParseID(e.Name())
		if err != nil || !e.IsDir() || id.String() != e.Name() || id.IsZero() {
			continue
		}
		w, lr, err := world.Load(root, id)
		if errors.Is(err, fs.ErrNotExist) {
			continue // a directory without a world file: nothing to serve
		}
		info := coopWorldInfo{id: id, err: err}
		if m, ok := readCoopMarker(world.Path(root, id)); ok {
			info.marker = &m
			info.use, info.useWhy = m.state()
		}
		if w != nil {
			info.created, info.seq = w.Created(), w.Seq()
			tomb := w.Tombstones()
			info.tombs = tomb.Count()
			if lr.FellBack() {
				info.from = lr.From
			}
		}
		out = append(out, info)
	}
	slices.SortStableFunc(out, func(a, b coopWorldInfo) int { return b.created.Compare(a.created) })
	return out, nil
}

// foundCoopWorld founds a world from the file loaded in m (§5.3): it
// waits until the file is in game and idle (gate.Status.CanWriteIdle),
// checks the file may found a world, and returns a new world holding the
// file's shared bits. The replica then binds the file to it through the
// usual prompt: a file and the world founded from it are the same, so the
// bind needs no --coop-merge.
func foundCoopWorld(ctx context.Context, rep report.Reporter, m mem.Mem, tick func(), pol *world.Policy) (*world.World, error) {
	var told time.Time
	for {
		s, st, _, err := gateSnapshots(m, tick)
		if err != nil {
			return nil, fmt.Errorf("co-op: founding a world: %w", err)
		}
		if st.CanWriteIdle() {
			return foundFrom(rep, s, pol)
		}
		if time.Since(told) >= 5*time.Second {
			report.Logf(rep, report.Info, "co-op: waiting for your save file to be loaded in game, out of menus and cutscenes, to found the world from it (%s)", st.Why())
			told = time.Now()
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("co-op: no world was founded: %w", err)
		}
	}
}

// foundFrom founds a world from snapshot s's file, within pol, the
// session's policy: the world the hub then serves keeps the same bits
// (hub.Config.SyncedSwitch over DefaultPolicy), so the file it was
// founded from binds to it as a subset, its story bits and memory
// switches included.
func foundFrom(rep report.Reporter, s *snap.Snapshot, pol *world.Policy) (*world.World, error) {
	f := bind.FileOf(s)
	v := bind.Classify(f, bind.Expect{})
	switch {
	case f.ClearCount != 0:
		return nil, fmt.Errorf("co-op: %s is a second quest, which co-op doesn't take", bind.Describe(f))
	case v.State == bind.Bound:
		return nil, fmt.Errorf("co-op: this file is bound to world %s already: host it with --coop=%s (or unbind it with coop-tag clear first)", v.Tag.World, v.Tag.World)
	case v.State == bind.Foreign:
		return nil, fmt.Errorf("co-op: this file's tag can't be judged by this version (%s); coop-tag clear unbinds it", v.Why)
	case v.Garbage:
		return nil, fmt.Errorf("co-op: dSv_reserve_c holds data that is not a co-op tag (%s); coop-tag clear zeroes it", v.Why)
	}
	id, err := bind.NewWorldID()
	if err != nil {
		return nil, err
	}
	w := world.New(world.ID(id), time.Now())
	w.SetPolicy(pol)
	img := world.Image(s.Image())
	r := w.Merge(&img)
	report.Logf(rep, report.OK, "co-op: founded world %s from %s (%d shared bytes).", id, bind.Describe(f), len(r.Delta))
	return w, nil
}

// The session marker: a host writes it into its world's directory while
// its hub runs, so coop-world block, which edits the world file, can tell
// the file is in use (the hub's next write would undo the edit). Its first
// line, "pid <n> since <time>", is the one every version has written. The
// second, "host=<name> start=<token>", names the machine and when the
// process started (procState), so a marker a killed host left can be told
// from a running host's.
const coopMarkerName = "session.lock"

// coopMarker is a session marker as read back.
type coopMarker struct {
	text  string // the first line, made one short line for the terminal
	pid   int    // from the first line; 0 if it names none
	host  string // the machine that wrote it; "" for unknown, taken as this one
	start string // the start token of its process (procState); "" for unknown
}

func (m coopMarker) String() string {
	if m.text == "" {
		return "an empty marker"
	}
	return m.text
}

// coopMarkerState is what a session marker says of its world.
type coopMarkerState int

const (
	markerLive    coopMarkerState = iota // a process with its pid (and start token, if both have one) runs on this machine
	markerSelf                           // this process wrote it
	markerStale                          // its process has ended: its session did not end cleanly
	markerUnknown                        // it can't be checked, so it is taken as in use
)

// coopProcState is procState; tests replace it.
var coopProcState = procState

// coopHostname is this machine's name as a marker records it, or "" if
// the OS can't say.
func coopHostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// state judges whether m's session may still run. For markerUnknown it
// says why it can't tell, and for markerLive judged by pid alone why the
// start tokens weren't compared ("" when they were). Only positive
// evidence makes a marker stale: no process on this machine has its pid,
// or the one that has it started at another time than the marker records
// (the pid was reused). A marker without a start token (one an older
// version wrote, or one written where procState gives none, as on macOS)
// is judged by its pid alone, as is one whose process's token procState
// can't read. The time on the first line is not used: it is local time
// with no zone, so a clock or zone change could make a running host look
// newer than its marker.
func (m coopMarker) state() (coopMarkerState, string) {
	if m.pid <= 0 {
		return markerUnknown, "it names no process"
	}
	if here := coopHostname(); m.host != "" && !strings.EqualFold(m.host, here) {
		if here == "" {
			return markerUnknown, "this machine's name can't be read"
		}
		return markerUnknown, "it was written on another machine, " + coopMarkerLine(m.host)
	}
	alive, start, err := coopProcState(m.pid)
	if err != nil {
		return markerUnknown, err.Error()
	}
	reused := m.start != "" && start != "" && m.start != start
	switch {
	case m.pid == os.Getpid() && !reused:
		return markerSelf, ""
	case !alive || reused:
		return markerStale, ""
	case m.start == "":
		return markerLive, "this marker has no start time"
	case start == "":
		return markerLive, "its start time can't be read"
	}
	return markerLive, ""
}

// writeCoopMarker marks the world in dir as in use by this process, and
// returns the marker it replaced, if there was one.
func writeCoopMarker(dir string) (prev coopMarker, had bool, err error) {
	prev, had = readCoopMarker(dir)
	_, start, _ := coopProcState(os.Getpid())
	text := fmt.Sprintf("pid %d since %s\nhost=%s start=%s\n", os.Getpid(), time.Now().Format(time.DateTime), coopHostname(), start)
	if err := os.WriteFile(filepath.Join(dir, coopMarkerName), []byte(text), 0o644); err != nil {
		return prev, had, fmt.Errorf("marking the world in use: %w", err)
	}
	return prev, had, nil
}

// readCoopMarker returns the marker of the world in dir, if it has one. A
// marker with one line, as older versions wrote, has no host or start.
func readCoopMarker(dir string) (coopMarker, bool) {
	b, err := os.ReadFile(filepath.Join(dir, coopMarkerName))
	if err != nil {
		return coopMarker{}, false
	}
	first, rest, _ := strings.Cut(string(b), "\n")
	m := coopMarker{text: coopMarkerLine(first)}
	if f := strings.Fields(first); len(f) >= 2 && f[0] == "pid" {
		if pid, err := strconv.ParseInt(f[1], 10, 32); err == nil && pid > 0 {
			m.pid = int(pid)
		}
	}
	second, _, _ := strings.Cut(rest, "\n")
	for _, f := range strings.Fields(second) {
		if v, ok := strings.CutPrefix(f, "host="); ok {
			m.host = v
		} else if v, ok := strings.CutPrefix(f, "start="); ok {
			m.start = v
		}
	}
	return m, true
}

// coopMarkerLine makes s, read from a marker, one line of at most 80 bytes
// for the terminal, with its control characters made spaces.
func coopMarkerLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7F {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(strings.TrimSpace(s), "?"))
	if len(s) > 80 {
		s = strings.ToValidUTF8(s[:80], "") // drops a character cut in two
	}
	return s
}

func removeCoopMarker(dir string) { os.Remove(filepath.Join(dir, coopMarkerName)) }

// coopPrompter asks the replicas' yes/no questions on the command line,
// one at a time, so mp-local's two Dolphins never ask at once. Its reader
// also hands the console the commands typed (a line starting with '/'),
// which are never an answer.
type coopPrompter struct {
	yes   bool
	mu    sync.Mutex        // held for a whole question
	lines <-chan promptLine // the answers read, oldest first; closed at EOF
	cmds  <-chan promptLine // the commands read; closed at EOF
	in    *promptInput
	now   func() time.Time
}

// promptLine is one line of input and when it was read.
type promptLine struct {
	text string
	at   time.Time
}

const (
	// promptQueue is how many unread lines the reader keeps. More are
	// dropped: they are older than any question still to come.
	promptQueue = 16
	// promptSettle is how long the reader must have waited for input,
	// with nothing to read, before a question is asked; promptSettleMax
	// bounds the wait for input that never stops.
	promptSettle    = 50 * time.Millisecond
	promptSettleMax = time.Second
)

// newCoopPrompter returns a session's prompter. It starts reading in
// (stdin when nil) at once, when the session starts, for the commands and
// the answers: a line counts as an answer only if it was read after its
// question was asked, so whatever was typed before, even a line already
// waiting in the terminal when the session started, never answers one.
// With yes every question is answered yes, and only the commands are
// read.
func newCoopPrompter(yes bool, in io.Reader) *coopPrompter {
	p := &coopPrompter{yes: yes, now: time.Now}
	if in == nil {
		in = os.Stdin
	}
	p.in = &promptInput{r: in, start: time.Now()}
	p.lines, p.cmds = readPromptLines(p.in, p.now)
	return p
}

// promptInput is the prompter's input, watched so that ask can tell when
// the reader has caught up with what was typed.
type promptInput struct {
	r     io.Reader
	start time.Time
	// reading is when the Read now running began (time.Since(start)+1),
	// or 0 while none runs; eof is set once the reader is done.
	reading atomic.Int64
	eof     atomic.Bool
}

func (in *promptInput) Read(b []byte) (int, error) {
	in.reading.Store(int64(time.Since(in.start)) + 1)
	n, err := in.r.Read(b)
	in.reading.Store(0)
	return n, err
}

// settle waits, at most promptSettleMax, until the reader has waited
// promptSettle for input with nothing to read. A terminal or pipe answers
// a read at once when a line is there, so every line typed so far has then
// been read and stamped, and none passes for an answer to the question
// asked next.
func (in *promptInput) settle(ctx context.Context) {
	deadline := time.Now().Add(promptSettleMax)
	for !in.eof.Load() && ctx.Err() == nil && time.Now().Before(deadline) {
		if r := in.reading.Load(); r != 0 && time.Since(in.start)-time.Duration(r-1) >= promptSettle {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// readPromptLines reads in line by line on its own goroutine, stamping each
// line the moment it is read, and hands a command (isCoopCommand) to cmds
// and any other line to answers. It never waits for a reader to take a
// line, so a stamp is never later than the read: a full queue drops the
// line.
func readPromptLines(in *promptInput, now func() time.Time) (answers, cmds <-chan promptLine) {
	ach := make(chan promptLine, promptQueue)
	cch := make(chan promptLine, promptQueue)
	go func() {
		defer close(ach)
		defer close(cch)
		defer in.eof.Store(true)
		sc := bufio.NewScanner(in)
		for sc.Scan() {
			ch := ach
			if isCoopCommand(sc.Text()) {
				ch = cch
			}
			select {
			case ch <- promptLine{text: sc.Text(), at: now()}:
			default:
			}
		}
	}()
	return ach, cch
}

// ask logs question and waits for the answer: true only for "y" or "yes".
// With --yes it answers yes at once. A line typed before the question was
// logged is dropped (settle, then the stamps), so an earlier keystroke
// never answers it; stdin at EOF and a session ending are a no.
func (p *coopPrompter) ask(ctx context.Context, rep report.Reporter, question string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, l := range strings.Split(strings.TrimRight(question, "\n"), "\n") {
		rep.Log(report.Info, l)
	}
	if p.yes {
		rep.Log(report.Info, "  yes (--yes)")
		return true
	}
	p.in.settle(ctx)
	asked := p.now()
	rep.Log(report.Info, "  Type y and press Enter for yes; anything else is no.")
	for {
		select {
		case <-ctx.Done():
			return false
		case l, ok := <-p.lines:
			if !ok {
				rep.Log(report.Warn, "no answer: stdin is closed, so that is a no (pass --yes to answer yes)")
				return false
			}
			if l.at.Before(asked) {
				continue // typed before the question
			}
			switch strings.ToLower(strings.TrimSpace(l.text)) {
			case "y", "yes":
				rep.Log(report.Info, "  yes")
				return true
			}
			rep.Log(report.Info, "  no")
			return false
		}
	}
}

// coopSync is one Dolphin's coop-sync pipeline: a replica over its memory
// and a co-op connection to the host's hub.
type coopSync struct {
	cs     *coopSession
	name   string // the player's name: the console's handle on it
	rep    report.Reporter
	m      mem.Mem
	r      *replica.Replica
	client *network.Client

	mu    sync.Mutex
	asked map[coopFileKey]bool // files a bind was asked for this session
	over  string               // why run gave up co-op for the session ("" while it has not)
}

// gaveUp returns why run gave up co-op for the session, or "".
func (s *coopSync) gaveUp() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.over
}

// coopFileKey is a file as the bind questions remember it: one is asked
// for at most once per session, so a no is not asked again.
type coopFileKey struct {
	slot, clearCount uint8
	name             [bind.PlayerNameSize]byte
}

// runCoopSyncCtx is the coop-sync pipeline of the Dolphin dolphinPID
// picks (0: the one WW_DOLPHIN_INDEX / WW_DOLPHIN_PID select), with its own
// handle on it, connecting to the hub at addr as name. It returns the
// error opening Dolphin, or nil when ctx ends.
func runCoopSyncCtx(ctx context.Context, name, addr string, dolphinPID uint32, rep report.Reporter, cs *coopSession) error {
	d, err := openDolphin(dolphinPID)
	if err != nil {
		return err
	}
	defer d.Close()
	s, err := cs.newSync(ctx, name, mem.NewDolphinMem(d), rep)
	if err != nil {
		return err
	}
	return s.run(ctx, addr)
}

// newSync builds a coop-sync pipeline over m and hands it to the
// session's console. Its offers are asked, and its commands run, with ctx,
// the session's.
//
// Unless --coop-dry-run, its replica writes (replica.Config.Apply) with
// the session's stage tables: their identity in CO_HELLO, the session's
// policy (the host hub's own: the story bits of the SyncMask and the
// memory switches of the M1 verdict, hub.SessionPolicy), and the tables
// themselves, which the small keys and max life are derived from. Its
// Breaker says once, as a warning, why the writes turned off; the
// pipeline keeps running, and pose sync with it. A dry run is M2's
// replica, configured as M2 configured it: no tables, M2's policy, the
// image alone asked for.
func (cs *coopSession) newSync(ctx context.Context, name string, m mem.Mem, rep report.Reporter) (*coopSync, error) {
	s := &coopSync{cs: cs, name: name, rep: rep, m: m, asked: map[coopFileKey]bool{}}
	logf := func(format string, args ...any) {
		rep.Log(report.Info, strings.TrimRight(fmt.Sprintf(format, args...), "\n")) // a preflight summary ends in one
	}
	cfg := replica.Config{
		Mem:         m,
		World:       cs.world,
		Logf:        logf,
		OnOffer:     func(o replica.Offer) { s.offer(ctx, o) },
		OnPreflight: s.preflight,
	}
	if !cs.flags.dryRun {
		cfg.Apply = true
		cfg.StageTblHash = cs.tables.hash()
		cfg.Policy = cs.policy()
		cfg.Tables = cs.m1()
		cfg.Breaker = replica.NewBreaker(replica.BreakerConfig{Logf: func(format string, args ...any) {
			if s.gaveUp() != "" {
				return // run said why, and /coop on cannot help
			}
			report.Logf(rep, report.Warn, "%s. Pose sync goes on; type /coop on to write the world again.", fmt.Sprintf(format, args...))
		}})
		cfg.PartnerPos = cs.partnerPos(name)
	}
	r, err := replica.New(cfg)
	if err != nil {
		return nil, err
	}
	s.r = r
	s.client = network.NewCoopClient(name, 0)
	s.client.OnLog = func(msg string) { rep.Log(report.Net, msg) }
	s.client.OnCoop = r.Handle
	cs.console.add(ctx, s)
	return s, nil
}

// withPoseClient returns an onClient for name's puppet-sync pipeline that
// records its client for the warps (partnerPos), then calls next if set.
// A nil session returns next.
func (cs *coopSession) withPoseClient(name string, next func(*network.Client)) func(*network.Client) {
	if cs == nil {
		return next
	}
	return func(c *network.Client) {
		cs.posesMu.Lock()
		cs.poses[name] = c
		cs.posesMu.Unlock()
		if next != nil {
			next(c)
		}
	}
}

// partnerPos is replica.Config.PartnerPos for player name: the position
// of the one other player its puppet-sync client hears (pose sync), not
// itself (mp-local's twins each see the other) and not a remote that has
// sent no position yet. With more than one other player it has none: the
// warp is for the two players v1.0 supports.
func (cs *coopSession) partnerPos(name string) func() (replica.Pos, bool) {
	return func() (replica.Pos, bool) {
		cs.posesMu.Lock()
		c := cs.poses[name]
		cs.posesMu.Unlock()
		if c == nil {
			return replica.Pos{}, false
		}
		var (
			p     replica.Pos
			other string
		)
		for _, rp := range c.GetRemotePlayers() {
			switch {
			case rp.Name == name || rp.Position == nil:
				continue
			case other != "" && rp.Name != other:
				return replica.Pos{}, false
			}
			other = rp.Name
			p = replica.Pos{X: rp.Position.PosX, Y: rp.Position.PosY, Z: rp.Position.PosZ}
		}
		return p, other != ""
	}
}

// run ticks the replica and keeps its connection up until ctx ends. A
// host that refuses co-op (it runs without --coop) or another fatal
// connect error turns co-op off for the session, said once: the replica's
// writes are turned off, its ticks stop (an escape waiting for one is
// answered replica.ErrStopped, as is any asked for later), and /coop on
// refuses; the pipeline then waits for the session to end, as pose sync
// goes on without it. Reachability is the pose pipelines' to report: they
// dial the same host, so the co-op connection's retries are not logged
// twice.
func (s *coopSync) run(ctx context.Context, addr string) error {
	tctx, stopTicks := context.WithCancel(ctx)
	ticks := make(chan struct{})
	go func() {
		defer close(ticks)
		s.r.Run(tctx)
	}()
	defer func() {
		stopTicks()
		<-ticks
	}()
	err := network.RunCoop(ctx, s.client, addr, network.NewConnectLog(addr, nil, nil), s.r.Session)
	if err == nil || ctx.Err() != nil {
		return nil
	}
	why := fmt.Sprintf("co-op is off for this session: %v", err)
	s.mu.Lock()
	s.over = why
	s.mu.Unlock()
	s.r.TurnOff(why)
	report.Logf(s.rep, report.Warn, "%s", why)
	stopTicks()
	<-ctx.Done()
	return nil
}

// offer answers a tag write the replica offers (OnOffer, called from the
// replica's tick, outside its lock): the session's prompt, or a no when
// the session has no backup. A bind's preflight is not repeated: the
// replica logged it just before. A question for the user waits in its own
// goroutine, so the replica keeps ticking meanwhile; the replica drops an
// offer whose file changed before the answer.
func (s *coopSync) offer(ctx context.Context, o replica.Offer) {
	if err := s.cs.noTags; err != nil {
		report.Logf(s.rep, report.Warn, "co-op: the %s is not offered: there is no memory-card backup this session (%v)", o.Plan.Kind, err)
		s.r.Decline(o.Plan)
		return
	}
	answer := func() {
		if s.cs.prompt.ask(ctx, s.rep, o.Plan.Prompt) {
			s.r.Confirm(o.Plan)
		} else {
			s.r.Decline(o.Plan)
		}
	}
	if s.cs.prompt.yes {
		answer()
		return
	}
	go answer()
}

// preflight follows a join preflight of an unbound file (OnPreflight):
// when the file may bind (§5.3: a subset of the world, or with the host's
// --coop-merge), the replica is asked to plan the bind, which comes back
// as an offer for the user. Each file is asked about once per session.
func (s *coopSync) preflight(pf replica.PreflightReport) {
	switch {
	case pf.Verdict == replica.PreflightRefused:
		return
	case pf.NeedsMerge && s.r.Status().Features&hub.FeatureMerge == 0 && pf.Verdict == replica.PreflightFarBehind:
		// §5.3 "far behind": a union would move this file's resume
		// stage, so a copy of the host's file is the better start. The
		// game keeps all three files in one card file (savefile.Slots),
		// so a copy is of all three. A copy loaded here says hello
		// bound, which clears the host's pending fresh bind of that file.
		// The host's own file, if it has not been loaded from the card
		// since its bind, then looks leaked in the host's next process
		// (Appendix C item 133).
		s.rep.Log(report.Info, "co-op: the world is far ahead of this file: copy the host's save in, or the host can run with --coop-merge to union them; pose sync goes on. "+
			"Dolphin's Memory Card Manager exports and imports the whole save, so the copy replaces all three of your files with the host's: export yours first (this session's card backup holds them too). The copied file is the host's own, already bound to the world. "+
			"Before the copy, the host should save in game and load its file from the card: a copy of a file bound fresh and not loaded back since makes the host's own file look leaked when the host next starts")
		return
	case pf.NeedsMerge && s.r.Status().Features&hub.FeatureMerge == 0:
		s.rep.Log(report.Info, "co-op: this file can't bind unless the host runs with --coop-merge; pose sync goes on")
		return
	case s.cs.noTags != nil:
		report.Logf(s.rep, report.Info, "co-op: this file could bind, but there is no memory-card backup this session (%v)", s.cs.noTags)
		return
	}
	f, err := bind.ReadFile(s.m)
	if err != nil {
		return
	}
	k := coopFileKey{slot: f.Slot, clearCount: f.ClearCount, name: f.Name}
	s.mu.Lock()
	asked := s.asked[k]
	s.asked[k] = true
	s.mu.Unlock()
	if !asked {
		s.r.RequestBind()
	}
}
