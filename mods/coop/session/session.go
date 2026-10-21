// Package coopsession is co-op sessions (#23; docs/08-coop-design.md
// §4.1-4.2, §5, §6.7, §9 M2-M4): what --coop adds to host, join and
// mp-local, the app's lobby sessions with co-op (app.go), and the seam
// the multiplayer engine meets co-op through (hook.go).
//
//   - host and mp-local run the CoopHub (mods/coop/hub) on their
//     server. It serves one world: the only one on disk, the one
//     --coop=<id> names, or, with --coop=new, one founded from the host's
//     loaded, idle file (§5.3). It grants memory switch sync by the M1
//     stage tables of the session's disc (mods/coop/stagetables:
//     --coop-iso, or the disc patch last wrote), and the live-switch
//     opt-in with --coop-live-switches (§6.3);
//   - every side backs up its memory cards, read-only, when the session
//     starts (wwapi/backup, §5.4). Without that backup no tag is
//     written this session;
//   - each Dolphin gets a coop-sync pipeline beside broadcast-pose and
//     puppet-sync, with its own Dolphin handle by PID and its own role-1
//     connection (network.RunCoop). Its replica (mods/coop/replica)
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
//     asked through the session's coopAsker and made only after the yes,
//     behind the gates (mods/coop/bind). The CLI's asker is the stdin
//     prompter (y/N on stdin; --yes answers yes); the TUI's puts the
//     question to the session controller (outputAsker), whose UI answers
//     it (session.Question, Controller.Answer);
//   - the session's commands (console.go): /refresh and
//     /warp, the escape hatches (§6.7), /coop off and on, /status and
//     /help, typed on stdin in the CLI, or given to the controller
//     (Controller.Command) by a UI.
//
// Without --coop none of this runs: host, join and mp-local open the same
// connections, send the same frames and print the same lines as before.
package coopsession

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

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/combat"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/derive"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/diag"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/procstate"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/stagetables"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/backup"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
)

// Flags are the co-op options of host, join and mp-local. The zero
// value is co-op off.
type Flags struct {
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

// On says whether f turns co-op on (--coop).
func (f Flags) On() bool { return f.on }

// coopWorldNew is --coop=new: found a world from the host's loaded file.
const coopWorldNew = "new"

// FlagsUsage is the co-op part of host's and mp-local's usage lines;
// JoinFlagsUsage is join's, which leaves the world and the host's
// choices out.
const (
	FlagsUsage     = "[--coop[=new|<world-id>]] [--coop-merge] [--coop-live-switches] [--coop-dry-run] [--coop-iso <iso>] [--yes]"
	JoinFlagsUsage = "[--coop] [--coop-dry-run] [--coop-iso <iso>] [--yes]"
)

// ExtractFlags takes the co-op options out of args (in any position
// before a bare "--", which ends the options) and returns the rest, in
// order, for the command's own parser. When args hold no --coop at all,
// they are returned untouched and co-op is off, so a command line without
// co-op parses exactly as it did before: --coop-merge, --coop-dry-run,
// --coop-live-switches, --coop-iso or --yes alone stay in rest, where the
// command's parser treats them as it always has.
func ExtractFlags(args []string) (Flags, []string, error) {
	var f Flags
	if !slices.ContainsFunc(coopOptionArgs(args), func(a string) bool {
		return a == "--coop" || strings.HasPrefix(a, "--coop=")
	}) {
		return f, args, nil
	}
	iso, args, err := stagetables.TakeISO(args)
	if err != nil {
		return Flags{}, nil, err
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
					return Flags{}, nil, fmt.Errorf("%s: say --coop=new, or a world ID (16 hex digits, as coop-world list prints them)", a)
				}
			}
			if f.world != "" && f.world != v {
				return Flags{}, nil, fmt.Errorf("--coop=%s and --coop=%s: give one world", f.world, v)
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

// ForJoin checks f for join: a joiner serves no world, so the world,
// --coop-merge and --coop-live-switches are the host's to choose.
// --coop-dry-run and --coop-iso are each side's own.
func (f Flags) ForJoin() error {
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
	// tick waits between the snapshots of a founding (diag.Tick).
	tick func()
	// input, if set, replaces stdin as the source of y/N answers and
	// commands.
	input io.Reader
	// tables loads the M1 stage tables of the disc iso names, "" for the
	// one patch last wrote (stagetables.Load). Nil runs the session
	// without tables, saying nothing of them: no memory switch syncs.
	tables func(iso string) (*stagetables.Tables, error)
	// front is the session's front end; its zero value is the CLI's.
	front coopFront
}

// coopFront is how a co-op session meets its front end: who answers its
// questions, who hears its advice, and who drives its console. The zero
// value is the CLI: questions and commands on stdin (input), advice in
// the log alone.
type coopFront struct {
	// ask answers the questions; nil is the stdin prompter
	// (coopPrompter), whose reader also takes the console's commands.
	// Any other asker leaves stdin alone: the commands then come only
	// through the session's console (engine.Engine.Command).
	ask coopAsker
	// advise hears each piece of advice, beside its log line; nil for
	// none.
	advise func(session.Advice)
	// attach is handed the session once it exists, so its front end can
	// reach the console and the pipelines' status; nil for none.
	attach func(*Session)
}

// defaultCoopEnv is the production environment for a session whose host
// Dolphin is pid (0: the one WW_DOLPHIN_INDEX / WW_DOLPHIN_PID select),
// which env opens. Both of the tool's Dolphin user directories are backed up: a missing one
// has no cards, and a developer running two Dolphins has both in use.
func defaultCoopEnv(env Env, pid uint32) coopEnv {
	ud1, ud2 := backup.UserDirs()
	return coopEnv{
		userDirs: []string{ud1, ud2},
		openMem: func() (mem.Mem, func(), error) {
			d, err := env.Open(pid)
			if err != nil {
				return nil, nil, err
			}
			return d, d.Close, nil
		},
		tick:   diag.Tick,
		tables: stagetables.Load,
	}
}

func (e coopEnv) worldsRoot() (string, error) {
	if e.worlds != "" {
		return e.worlds, nil
	}
	return world.Dir()
}

// Session is one host, join or mp-local session's co-op side: the
// hub (host and mp-local), the memory-card backup, the prompts, and what
// the session's coop-sync pipelines share.
type Session struct {
	flags Flags
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
	// backupDir is where the session-start backup went ("" without).
	backupDir string
	// ask answers the questions (coopFront.ask, or the stdin prompter).
	ask    coopAsker
	advise func(session.Advice) // nil for none
	rep    log.Reporter
	// tables are the session's M1 stage tables, nil without (every
	// method is nil-safe).
	tables *stagetables.Tables
	// console runs the commands typed on stdin for the session's
	// coop-sync pipelines.
	console *coopConsole
	// poses are the puppet-sync clients of the session's Dolphins, by
	// player name: where a warp finds the partner's position.
	posesMu sync.Mutex
	poses   map[string]*network.Client
	// poseClient, if set, is where a warp finds the partner when poses
	// has no client of the player's: the app's pose pipelines run before
	// co-op starts, so they register with its status bridge instead.
	poseClient func() *network.Client
	// askedMu guards askedBy: per player, the files a bind was asked for
	// this session, kept across the coop-sync runs of one player (the app
	// starts coop-sync again on a Dolphin relaunch), so a no stays a no.
	askedMu sync.Mutex
	askedBy map[string]map[coopFileKey]bool
}

// askedFor is player name's set of files a bind was asked for.
func (cs *Session) askedFor(name string) map[coopFileKey]bool {
	cs.askedMu.Lock()
	defer cs.askedMu.Unlock()
	if cs.askedBy == nil {
		cs.askedBy = map[string]map[coopFileKey]bool{}
	}
	k := strings.ToLower(name)
	if cs.askedBy[k] == nil {
		cs.askedBy[k] = map[coopFileKey]bool{}
	}
	return cs.askedBy[k]
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
func startCoopSession(rep log.Reporter, f Flags, env coopEnv) *Session {
	cs := startCoopSide(rep, f, env, false)
	cs.console.help()
	return cs
}

// startCoopSide is startCoopSession for a host (host: its hub needs the
// tables even in a dry run) or a joiner.
func startCoopSide(rep log.Reporter, f Flags, env coopEnv, host bool) *Session {
	cs := &Session{flags: f, rep: rep, ask: env.front.ask, advise: env.front.advise, poses: map[string]*network.Client{}}
	var cmds <-chan promptLine
	if cs.ask == nil {
		p := newCoopPrompter(f.yes, env.input)
		cs.ask, cmds = p, p.cmds
	}
	cs.console = newCoopConsole(rep, cmds)
	if env.front.attach != nil {
		env.front.attach(cs)
	}
	b, err := backup.Create(env.userDirs, backup.Options{Root: env.backups, Label: "co-op session start"})
	if err != nil {
		cs.noTags = err
		log.Logf(rep, log.Warn, "co-op: could not back up the memory cards: %v. No co-op tag is written this session (set USER_DIR_1 if Dolphin keeps its user folder elsewhere).", err)
	} else {
		cs.backupDir = b.Dir
		log.Logf(rep, log.OK, "co-op: backed up %d memory-card file(s) to %s (`coop-world restore %s --yes`, with Dolphin closed, puts them back). Check that these are your cards:", len(b.Files), b.Dir, b.ID)
		logBackupCards(rep, b)
	}
	if env.tables != nil && (host || !f.dryRun) {
		st, err := env.tables(f.iso)
		stagetables.Log(rep, st, err)
		cs.tables = st
	}
	if !f.dryRun {
		logCoopProgression(rep, cs.m1())
		logCoopPuzzles(rep, cs.m1())
		logCoopCombat(rep, cs.m1())
	}
	return cs
}

// m1 is the session's M1 tables, nil without.
func (cs *Session) m1() *tables.Tables {
	if cs.tables == nil {
		return nil
	}
	return cs.tables.M1()
}

// policy is what the session's world takes and its replicas propose,
// plan and write: the hub's (hub.SessionPolicy), with the story bits of
// the SyncMask and the memory switches of the M1 verdict open.
func (cs *Session) policy() *world.Policy {
	return hub.SessionPolicy(cs.tables.SwitchVerdict())
}

// logCoopProgression says what of M4's shared progression (§9 M4) this
// side's replicas write, by its own M1 tables t: the story bits, the
// inventory and max magic need none; the small keys of each dungeon and
// max life are derived from t, and each one t does not allow is named
// with why (no tables, or a table whose M1 invariant fails). What the
// host does not grant the replica says on its welcome.
func logCoopProgression(rep log.Reporter, t *tables.Tables) {
	const synced = "story bits and event registers (written at stage changes), items, songs and capacities (when idle) and max magic"
	if t == nil {
		log.Logf(rep, log.OK, "co-op: shared progression this session: %s; without co-op stage tables no small keys or max hearts are derived.", synced)
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
	log.Logf(rep, log.OK, "co-op: shared progression this session: %s.", line)
	for _, o := range off {
		log.Logf(rep, log.Warn, "co-op: %s.", o)
	}
}

// logBackupCards lists a backup's cards, each with the files copied from
// it, and warns when Dolphin's settings (Dolphin.ini, or Dolphin's default
// for a slot it doesn't set) select none of them: the card locations are
// Dolphin's (backup.Locate), so the owner checks them (§8.2).
func logBackupCards(rep log.Reporter, b *backup.Backup) {
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
			log.Logf(rep, log.Info, "co-op:   %s, %d bytes", c, files[0].Size)
		case len(files) == 0:
			log.Logf(rep, log.Info, "co-op:   %s: no Wind Waker save in it", c)
		default:
			log.Logf(rep, log.Info, "co-op:   %s:", c)
			for _, f := range files {
				log.Logf(rep, log.Info, "co-op:     %s, %d bytes", f.Source, f.Size)
			}
		}
	}
	if !inUse {
		log.Logf(rep, log.Warn, "co-op: Dolphin's settings (Dolphin.ini, or Dolphin's defaults where it sets none: a GCI folder in slot A) select none of these cards. If your Dolphin keeps its user folder elsewhere (a portable User folder, say), set USER_DIR_1 (and USER_DIR_2) to it, or this backup misses your card.")
	}
}

// StartHost is startCoopHost for package main's mp-local: its host
// Dolphin is pid, opened with env (defaultCoopEnv). A host Dolphin the
// strict contract check refuses (another version's patch or save state)
// starts no co-op: the error says why and what to do.
func StartHost(ctx context.Context, rep log.Reporter, srvLog func(string), f Flags, env Env, pid uint32) (*Session, error) {
	if err := contractErrPID(env, pid); err != nil {
		return nil, err
	}
	return startCoopHost(ctx, rep, srvLog, f, defaultCoopEnv(env, pid))
}

// startCoopHost begins a host's co-op side (host and mp-local): the
// backup, the world (selectCoopWorld, or a founding) and the hub, which
// the caller sets as its server's Coop before Start. srvLog gets the
// hub's lines, beside the server's own. Stop it with Stop.
func startCoopHost(ctx context.Context, rep log.Reporter, srvLog func(string), f Flags, env coopEnv) (*Session, error) {
	cs := startCoopSide(rep, f, env, true)
	st, err := cs.openWorld(ctx, rep, f, env, func(ctx context.Context, pol *world.Policy) (*world.World, error) {
		m, closeMem, err := env.openMem()
		if err != nil {
			return nil, fmt.Errorf("co-op: founding a world: %w", err)
		}
		defer closeMem()
		return foundCoopWorld(ctx, rep, m, env.tick, pol)
	})
	if err != nil {
		return nil, err
	}
	if err := cs.startHub(rep, srvLog, f, st); err != nil {
		return nil, err
	}
	return cs, nil
}

// openWorld opens the world a host serves (selectCoopWorld), or, for
// --coop=new, founds one with found and creates it under the worlds
// root.
func (cs *Session) openWorld(ctx context.Context, rep log.Reporter, f Flags, env coopEnv, found func(context.Context, *world.Policy) (*world.World, error)) (*world.Store, error) {
	root, err := env.worldsRoot()
	if err != nil {
		return nil, err
	}
	opts := world.Options{Logf: func(format string, args ...any) {
		rep.Log(log.Warn, "co-op: world file: "+fmt.Sprintf(format, args...))
	}}
	if f.world == coopWorldNew {
		w, err := found(ctx, cs.policy())
		if err != nil {
			return nil, err
		}
		return world.Create(root, w, opts)
	}
	id, err := selectCoopWorld(root, f.world)
	if err != nil {
		return nil, err
	}
	st, lr, err := world.Open(root, id, opts)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("co-op: no world %s in %s (coop-world list shows the worlds)", id, root)
		}
		return nil, fmt.Errorf("co-op: opening world %s: %w", id, err)
	}
	logWorldLoad(rep, lr)
	return st, nil
}

// startHub starts the hub over st, the session marker, and says what the
// session writes.
func (cs *Session) startHub(rep log.Reporter, srvLog func(string), f Flags, st *world.Store) error {
	features := hub.DefaultFeatures
	if f.liveSwitches {
		features |= hub.FeatureLiveSwitches
	}
	h, err := hub.New(hub.Config{
		Store:        st,
		StageTblHash: cs.tables.Hash(),
		SyncedSwitch: cs.tables.SwitchVerdict(),
		SyncedScoped: cs.tables.ScopedVerdict(),
		SyncedKill:   cs.tables.KillVerdict(),
		Features:     features,
		Merge:        f.merge,
		Logf:         func(format string, args ...any) { srvLog(fmt.Sprintf(format, args...)) },
	})
	if err != nil {
		st.Stop()
		return err
	}
	cs.hub, cs.store, cs.world, cs.dir = h, st, bind.WorldID(st.ID()), st.Dir()
	cs.console.setHub(h.Status)
	if prev, had, err := WriteMarker(cs.dir); err != nil {
		log.Logf(rep, log.Warn, "co-op: %v", err)
	} else if had {
		switch use, why := prev.State(); use {
		case MarkerStale:
			log.Logf(rep, log.Warn, "co-op: world %s was marked in use (%s), but that process has ended: its last session did not end cleanly", cs.world, prev)
		case MarkerSelf:
			log.Logf(rep, log.Warn, "co-op: world %s was marked in use by this process (%s): its earlier session here did not stop", cs.world, prev)
		case MarkerLive:
			if why == "" {
				log.Logf(rep, log.Warn, "co-op: world %s is marked in use by a process that still runs (%s): if that is another host serving it, stop one of them, or one host's changes are lost", cs.world, prev)
			} else {
				log.Logf(rep, log.Warn, "co-op: world %s was marked in use (%s), and a process with pid %d runs, but %s, so it may not be that host: if another host serves it, stop one of them, or one host's changes are lost", cs.world, prev, prev.pid, why)
			}
		default:
			log.Logf(rep, log.Warn, "co-op: world %s was marked in use (%s), and whether that session still runs can't be told (%s): if another host serves it, stop one of them, or one host's changes are lost", cs.world, prev, why)
		}
	}
	merge := ""
	if f.merge {
		merge = "; --coop-merge: files with progress the world lacks may bind"
	}
	if f.dryRun {
		log.Logf(rep, log.OK, "Co-op world %s at seq %d (dry run: planned writes are logged, not made; only a binding tag is ever written, after a yes%s).", cs.world, st.Seq(), merge)
		cs.console.help()
		return nil
	}
	live := ""
	if f.liveSwitches {
		live = "; --coop-live-switches: the current table's switches apply when idle, once every shown room's actors have run 2 s"
	}
	log.Logf(rep, log.OK, "Co-op world %s at seq %d: the world's chests, pickups, rooms, dungeon items, latching switches, ocean, charts, story bits and items are written into each bound game, and its small keys, max hearts and max magic derived (--coop-dry-run only logs them%s%s).", cs.world, st.Seq(), merge, live)
	cs.console.help()
	return nil
}

// Stop ends the hub, which writes the world, and removes the session
// marker. Nil-safe, for a session without co-op.
func (cs *Session) Stop() {
	if cs == nil || cs.hub == nil {
		return
	}
	if err := cs.hub.Stop(); err != nil {
		log.Logf(cs.rep, log.Err, "co-op: %v", err)
	}
	removeCoopMarker(cs.dir)
	cs.hub = nil
}

// Hub is the session's hub, which a host's server runs as its Coop; nil
// on a joiner, and once stopped.
func (cs *Session) Hub() *hub.Hub { return cs.hub }

// logWorldLoad says how a world came off the disk when it took more than
// reading world.wwco.
func logWorldLoad(rep log.Reporter, lr world.LoadReport) {
	if lr.FellBack() {
		log.Logf(rep, log.Warn, "co-op: the world file was missing or damaged; loaded the backup %s instead (%s)", lr.From, errors.Join(lr.Skipped...))
	}
	if lr.Replayed > 0 {
		log.Logf(rep, log.Info, "co-op: replayed %d journal record(s) the world file had not caught up with", lr.Replayed)
	}
	if lr.BadLines > 0 {
		log.Logf(rep, log.Warn, "co-op: skipped %d damaged journal line(s)", lr.BadLines)
	}
	if lr.Stripped > 0 {
		log.Logf(rep, log.Warn, "co-op: dropped %d bit(s) this build's class table does not share", lr.Stripped)
	}
}

// selectCoopWorld picks the world a host serves under root: want, a world
// ID, or, for "", the only world there is.
func selectCoopWorld(root, want string) (world.ID, error) {
	if want != "" {
		id, err := bind.ParseWorldID(want)
		return world.ID(id), err
	}
	ws, err := ListWorlds(root)
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

// WorldInfo is one world on disk, as coop-world list and a host's
// world choice show it.
type WorldInfo struct {
	id      world.ID
	created time.Time
	seq     uint64
	tombs   int         // tombstoned bits
	from    string      // the file it loads from, when not world.wwco
	err     error       // why it can't be loaded
	marker  *Marker     // its session marker, if any
	use     MarkerState // what the marker says, judged as the list is made
	useWhy  string      // why use is MarkerUnknown, or MarkerLive by pid alone (Marker.state)
}

func (w WorldInfo) String() string {
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
		switch {
		case w.use == MarkerStale:
			s += fmt.Sprintf(", stale session marker (%s; that process has ended)", m)
		case w.use == MarkerUnknown:
			s += fmt.Sprintf(", in use?: %s (can't tell: %s)", m, w.useWhy)
		case w.use == MarkerLive && w.useWhy != "":
			s += fmt.Sprintf(", in use: %s (by its pid alone: %s)", m, w.useWhy)
		default:
			s += fmt.Sprintf(", in use: %s", m)
		}
	}
	return s
}

// ListWorlds lists the worlds under root, newest first, reading each
// without changing anything (world.Load). A root that does not exist has
// none.
func ListWorlds(root string) ([]WorldInfo, error) {
	ents, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("co-op: %w", err)
	}
	var out []WorldInfo
	for _, e := range ents {
		id, err := world.ParseID(e.Name())
		if err != nil || !e.IsDir() || id.String() != e.Name() || id.IsZero() {
			continue
		}
		w, lr, err := world.Load(root, id)
		if errors.Is(err, fs.ErrNotExist) {
			continue // a directory without a world file: nothing to serve
		}
		info := WorldInfo{id: id, err: err}
		if m, ok := ReadMarker(world.Path(root, id)); ok {
			info.marker = &m
			info.use, info.useWhy = m.State()
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
	slices.SortStableFunc(out, func(a, b WorldInfo) int { return b.created.Compare(a.created) })
	return out, nil
}

// PrintWorlds prints the worlds under root, newest first: coop-world
// list.
func PrintWorlds(w io.Writer, root string) error {
	ws, err := ListWorlds(root)
	if err != nil {
		return err
	}
	if len(ws) == 0 {
		fmt.Fprintf(w, "No co-op worlds in %s yet: `host --coop=new` founds one from your loaded save file.\n", root)
		return nil
	}
	fmt.Fprintf(w, "Co-op worlds in %s, newest first:\n", root)
	for _, x := range ws {
		fmt.Fprintf(w, "  %s\n", x)
	}
	return nil
}

// foundCoopWorld founds a world from the file loaded in m (§5.3): it
// waits until the file is in game and idle (gate.Status.CanWriteIdle),
// checks the file may found a world, and returns a new world holding the
// file's shared bits. The replica then binds the file to it through the
// usual prompt: a file and the world founded from it are the same, so the
// bind needs no --coop-merge.
func foundCoopWorld(ctx context.Context, rep log.Reporter, m mem.Mem, tick func(), pol *world.Policy) (*world.World, error) {
	var told time.Time
	for {
		s, st, _, err := diag.GateSnapshots(m, tick)
		if err != nil {
			return nil, fmt.Errorf("co-op: founding a world: %w", err)
		}
		if st.CanWriteIdle() {
			return foundFrom(rep, s, pol)
		}
		if time.Since(told) >= 5*time.Second {
			log.Logf(rep, log.Info, "co-op: waiting for your save file to be loaded in game, out of menus and cutscenes, to found the world from it (%s)", st.Why())
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
func foundFrom(rep log.Reporter, s *state.Snapshot, pol *world.Policy) (*world.World, error) {
	f := bind.FileOf(s)
	if _, err := foundCheck(f); err != nil {
		return nil, err
	}
	id, err := bind.NewWorldID()
	if err != nil {
		return nil, err
	}
	w := world.New(world.ID(id), time.Now())
	w.SetPolicy(pol)
	img := save.Image(s.Image())
	r := w.Merge(&img)
	log.Logf(rep, log.OK, "co-op: founded world %s from %s (%d shared bytes).", id, bind.Describe(f), len(r.Delta))
	return w, nil
}

// foundCheck says whether file f may found a world: nil, or why not
// with the kind of blocker (session.Block*).
func foundCheck(f bind.File) (string, error) {
	v := bind.Classify(f, bind.Expect{})
	switch {
	case f.ClearCount != 0:
		return session.BlockSecondQuest, fmt.Errorf("co-op: %s is a second quest, which co-op doesn't take", bind.Describe(f))
	case v.State == bind.Bound:
		return session.BlockBound, fmt.Errorf("co-op: this file is bound to world %s already: host it with --coop=%s (or unbind it with coop-tag clear first)", v.Tag.World, v.Tag.World)
	case v.State == bind.Foreign:
		return session.BlockForeign, fmt.Errorf("co-op: this file's tag can't be judged by this version (%s); coop-tag clear unbinds it", v.Why)
	case v.Garbage:
		return session.BlockGarbage, fmt.Errorf("co-op: dSv_reserve_c holds data that is not a co-op tag (%s); coop-tag clear zeroes it", v.Why)
	}
	return "", nil
}

// The session marker: a host writes it into its world's directory while
// its hub runs, so coop-world block, which edits the world file, can tell
// the file is in use (the hub's next write would undo the edit). Its first
// line, "pid <n> since <time>", is the one every version has written. The
// second, "host=<name> start=<token>", names the machine and when the
// process started (procstate.State), so a marker a killed host left can be told
// from a running host's.
const MarkerName = "session.lock"

// Marker is a session marker as read back.
type Marker struct {
	text  string // the first line, made one short line for the terminal
	pid   int    // from the first line; 0 if it names none
	host  string // the machine that wrote it; "" for unknown, taken as this one
	start string // the start token of its process (procstate.State); "" for unknown
}

// PID is the pid the marker names; 0 or less names no process.
func (m Marker) PID() int { return m.pid }

func (m Marker) String() string {
	if m.text == "" {
		return "an empty marker"
	}
	return m.text
}

// MarkerState is what a session marker says of its world.
type MarkerState int

const (
	MarkerLive    MarkerState = iota // a process with its pid (and start token, if both have one) runs on this machine
	MarkerSelf                       // this process wrote it
	MarkerStale                      // its process has ended: its session did not end cleanly
	MarkerUnknown                    // it can't be checked, so it is taken as in use
)

// ProcState is procstate.State; tests replace it.
var ProcState = procstate.State

// Hostname is this machine's name as a marker records it, or "" if
// the OS can't say.
func Hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// state judges whether m's session may still run. For MarkerUnknown it
// says why it can't tell, and for MarkerLive judged by pid alone why the
// start tokens weren't compared ("" when they were). Only positive
// evidence makes a marker stale: no process on this machine has its pid,
// or the one that has it started at another time than the marker records
// (the pid was reused). A marker without a start token (one an older
// version wrote, or one written where procstate.State gives none, as on macOS)
// is judged by its pid alone, as is one whose process's token procstate.State
// can't read. The time on the first line is not used: it is local time
// with no zone, so a clock or zone change could make a running host look
// newer than its marker.
func (m Marker) State() (MarkerState, string) {
	if m.pid <= 0 {
		return MarkerUnknown, "it names no process"
	}
	if here := Hostname(); m.host != "" && !strings.EqualFold(m.host, here) {
		if here == "" {
			return MarkerUnknown, "this machine's name can't be read"
		}
		return MarkerUnknown, "it was written on another machine, " + coopMarkerLine(m.host)
	}
	alive, start, err := ProcState(m.pid)
	if err != nil {
		return MarkerUnknown, err.Error()
	}
	reused := m.start != "" && start != "" && m.start != start
	switch {
	case m.pid == os.Getpid() && !reused:
		return MarkerSelf, ""
	case !alive || reused:
		return MarkerStale, ""
	case m.start == "":
		return MarkerLive, "this marker has no start time"
	case start == "":
		return MarkerLive, "its start time can't be read"
	}
	return MarkerLive, ""
}

// WriteMarker marks the world in dir as in use by this process, and
// returns the marker it replaced, if there was one.
func WriteMarker(dir string) (prev Marker, had bool, err error) {
	prev, had = ReadMarker(dir)
	_, start, _ := ProcState(os.Getpid())
	text := fmt.Sprintf("pid %d since %s\nhost=%s start=%s\n", os.Getpid(), time.Now().Format(time.DateTime), Hostname(), start)
	if err := os.WriteFile(filepath.Join(dir, MarkerName), []byte(text), 0o644); err != nil {
		return prev, had, fmt.Errorf("marking the world in use: %w", err)
	}
	return prev, had, nil
}

// ReadMarker returns the marker of the world in dir, if it has one. A
// marker with one line, as older versions wrote, has no host or start.
func ReadMarker(dir string) (Marker, bool) {
	b, err := os.ReadFile(filepath.Join(dir, MarkerName))
	if err != nil {
		return Marker{}, false
	}
	first, rest, _ := strings.Cut(string(b), "\n")
	m := Marker{text: coopMarkerLine(first)}
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

func removeCoopMarker(dir string) { os.Remove(filepath.Join(dir, MarkerName)) }

// coopAsker answers a co-op session's yes/no questions (App PR 2's Asker,
// docs/01-architecture.md): question logs q to rep, the asking
// pipeline's reporter, and returns the answer, true for yes; ctx ending
// is a no. answersAtOnce says question never waits for a person (--yes),
// so an offer is answered on the replica's tick rather than on a
// goroutine of its own.
type coopAsker interface {
	question(ctx context.Context, rep log.Reporter, q session.Question) bool
	answersAtOnce() bool
}

// logQuestion logs q's body, one line at a time, as the CLI prints it
// (coopPrompter.ask).
func logQuestion(rep log.Reporter, q session.Question) {
	for _, l := range strings.Split(strings.TrimRight(q.Body, "\n"), "\n") {
		rep.Log(log.Info, l)
	}
}

// coopPrompter is the CLI's coopAsker: it asks the replicas' yes/no
// questions on the command line, one at a time, so mp-local's two
// Dolphins never ask at once. Its reader also hands the console the
// commands typed (a line starting with '/'), which are never an answer.
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

func (p *coopPrompter) answersAtOnce() bool { return p.yes }

// question is ask with q's body: the CLI prints the prompt as it always
// has (a bind's preflight is in the log already: the replica logged it
// just before its offer).
func (p *coopPrompter) question(ctx context.Context, rep log.Reporter, q session.Question) bool {
	return p.ask(ctx, rep, q.Body)
}

// ask logs question and waits for the answer: true only for "y" or "yes".
// With --yes it answers yes at once. A line typed before the question was
// logged is dropped (settle, then the stamps), so an earlier keystroke
// never answers it; stdin at EOF and a session ending are a no.
func (p *coopPrompter) ask(ctx context.Context, rep log.Reporter, question string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, l := range strings.Split(strings.TrimRight(question, "\n"), "\n") {
		rep.Log(log.Info, l)
	}
	if p.yes {
		rep.Log(log.Info, "  yes (--yes)")
		return true
	}
	p.in.settle(ctx)
	asked := p.now()
	rep.Log(log.Info, "  Type y and press Enter for yes; anything else is no.")
	for {
		select {
		case <-ctx.Done():
			return false
		case l, ok := <-p.lines:
			if !ok {
				rep.Log(log.Warn, "no answer: stdin is closed, so that is a no (pass --yes to answer yes)")
				return false
			}
			if l.at.Before(asked) {
				continue // typed before the question
			}
			switch strings.ToLower(strings.TrimSpace(l.text)) {
			case "y", "yes":
				rep.Log(log.Info, "  yes")
				return true
			}
			rep.Log(log.Info, "  no")
			return false
		}
	}
}

// coopSync is one Dolphin's coop-sync pipeline: a replica over its memory
// and a co-op connection to the host's hub.
type coopSync struct {
	cs     *Session
	name   string // the player's name: the console's handle on it
	rep    log.Reporter
	m      mem.Mem
	r      *replica.Replica
	client *network.Client

	mu    sync.Mutex
	asked map[coopFileKey]bool // files a bind was asked for this session (cs.askedFor; under cs.askedMu)
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

// RunSync is the coop-sync pipeline of the Dolphin dolphinPID
// picks (0: the one WW_DOLPHIN_INDEX / WW_DOLPHIN_PID select), with its own
// handle on it, connecting to the hub at addr as name. claimed is that
// game's enemy-mirror claims (the Claimed of the enemysync.ClaimSet its
// own enemy client fills; nil: none), which its K1 never light-shrinks
// (replica.Config.Claimed). It returns the error opening Dolphin, or nil
// when ctx ends.
func RunSync(ctx context.Context, env Env, name, addr string, dolphinPID uint32, rep log.Reporter, cs *Session, claimed combat.ClaimFunc) error {
	d, err := env.Open(dolphinPID)
	if err != nil {
		return err
	}
	defer d.Close()
	// The strict contract check (Phase E1): a game from another patch, or
	// a save state taken on one, gets no co-op writes. Pose sync goes on
	// (its own gate draws nobody there either).
	if err := contractErr(d); err != nil {
		rep.Log(log.Err, err.Error()+" Co-op is off for this Dolphin this session.")
		<-ctx.Done()
		return nil
	}
	s, err := cs.newSyncClaimed(ctx, name, d, rep, claimed)
	if err != nil {
		return err
	}
	gctx, stopGuard := context.WithCancel(ctx)
	defer stopGuard()
	go guardContract(gctx, d, s.r, rep, ContractEvery)
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
func (cs *Session) newSync(ctx context.Context, name string, m mem.Mem, rep log.Reporter) (*coopSync, error) {
	return cs.newSyncClaimed(ctx, name, m, rep, nil)
}

// newSyncClaimed is newSync with m's game's enemy-mirror claims
// (replica.Config.Claimed; nil: none).
func (cs *Session) newSyncClaimed(ctx context.Context, name string, m mem.Mem, rep log.Reporter, claimed combat.ClaimFunc) (*coopSync, error) {
	s := &coopSync{cs: cs, name: name, rep: rep, m: m, asked: cs.askedFor(name)}
	logf := func(format string, args ...any) {
		rep.Log(log.Info, strings.TrimRight(fmt.Sprintf(format, args...), "\n")) // a preflight summary ends in one
	}
	cfg := replica.Config{
		Mem:         m,
		World:       cs.world,
		Logf:        logf,
		OnOffer:     func(o replica.Offer) { s.offer(ctx, o) },
		OnPreflight: s.preflight,
		Claimed:     claimed,
	}
	if !cs.flags.dryRun {
		cfg.Apply = true
		cfg.StageTblHash = cs.tables.Hash()
		cfg.Policy = cs.policy()
		cfg.Tables = cs.m1()
		cfg.Breaker = replica.NewBreaker(replica.BreakerConfig{Logf: func(format string, args ...any) {
			if s.gaveUp() != "" {
				return // run said why, and /coop on cannot help
			}
			log.Logf(rep, log.Warn, "%s. Pose sync goes on; type /coop on to write the world again.", fmt.Sprintf(format, args...))
		}})
		cfg.PartnerPos = cs.partnerPos(name)
	}
	r, err := replica.New(cfg)
	if err != nil {
		return nil, err
	}
	s.r = r
	s.client = network.NewCoopClient(name, 0)
	s.client.OnLog = func(msg string) { rep.Log(log.Net, msg) }
	s.client.OnCoop = r.Handle
	cs.console.add(ctx, s)
	return s, nil
}

// WithPoseClient returns an onClient for name's puppet-sync pipeline that
// records its client for the warps (partnerPos), then calls next if set.
// A nil session returns next.
func (cs *Session) WithPoseClient(name string, next func(*network.Client)) func(*network.Client) {
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
func (cs *Session) partnerPos(name string) func() (replica.Pos, bool) {
	return func() (replica.Pos, bool) {
		cs.posesMu.Lock()
		c := cs.poses[name]
		cs.posesMu.Unlock()
		if c == nil && cs.poseClient != nil {
			c = cs.poseClient()
		}
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
	log.Logf(s.rep, log.Warn, "%s", why)
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
		msg := fmt.Sprintf("co-op: the %s is not offered: there is no memory-card backup this session (%v)", o.Plan.Kind, err)
		s.rep.Log(log.Warn, msg)
		s.advise(session.AdviceNoBackup, msg)
		s.r.Decline(o.Plan)
		return
	}
	q := offerQuestion(s.name, o)
	answer := func() {
		if s.cs.ask.question(ctx, s.rep, q) {
			s.r.Confirm(o.Plan)
		} else {
			s.r.Decline(o.Plan)
		}
	}
	if s.cs.ask.answersAtOnce() {
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
		const msg = "co-op: the world is far ahead of this file: copy the host's save in, or the host can run with --coop-merge to union them; pose sync goes on. " +
			"Dolphin's Memory Card Manager exports and imports the whole save, so the copy replaces all three of your files with the host's: export yours first (this session's card backup holds them too). The copied file is the host's own, already bound to the world. " +
			"Before the copy, the host should save in game and load its file from the card: a copy of a file bound fresh and not loaded back since makes the host's own file look leaked when the host next starts"
		s.rep.Log(log.Info, msg)
		s.advise(session.AdviceFarBehind, msg)
		return
	case pf.NeedsMerge && s.r.Status().Features&hub.FeatureMerge == 0:
		const msg = "co-op: this file can't bind unless the host runs with --coop-merge; pose sync goes on"
		s.rep.Log(log.Info, msg)
		s.advise(session.AdviceNeedsMerge, msg)
		return
	case s.cs.noTags != nil:
		msg := fmt.Sprintf("co-op: this file could bind, but there is no memory-card backup this session (%v)", s.cs.noTags)
		s.rep.Log(log.Info, msg)
		s.advise(session.AdviceNoBackup, msg)
		return
	}
	f, err := bind.ReadFile(s.m)
	if err != nil {
		return
	}
	k := coopFileKey{slot: f.Slot, clearCount: f.ClearCount, name: f.Name}
	s.cs.askedMu.Lock() // s.asked may be the set of an earlier run of this player's
	asked := s.asked[k]
	s.asked[k] = true
	s.cs.askedMu.Unlock()
	if !asked {
		s.r.RequestBind()
	}
}

// advise passes advice of kind, logged as text, to the session's front
// end, if it hears advice.
func (s *coopSync) advise(kind session.AdviceKind, text string) {
	if s.cs.advise != nil {
		s.cs.advise(session.Advice{Kind: kind, Player: s.name, Text: text})
	}
}

// offerQuestion is the question for offer o of player's pipeline: the
// plan's prompt, its first line as the title, and a bind's preflight.
func offerQuestion(player string, o replica.Offer) session.Question {
	q := session.Question{Kind: session.QuestionBind, Player: player, Body: o.Plan.Prompt}
	if o.Plan.Kind == bind.KindScrub {
		q.Kind = session.QuestionScrub
	}
	q.Title, _, _ = strings.Cut(o.Plan.Prompt, "\n")
	p := o.Plan
	q.FileName, q.FileSlot = p.File.PlayerName(), int(p.File.Slot)+1
	if p.Kind == bind.KindBind {
		q.World = p.Tag.World.String()
	}
	if p.Verdict.HasTag {
		q.TagWorld = p.Verdict.Tag.World.String()
	}
	failed := p.Verdict.Failed()
	q.MaybeFresh = p.Verdict.State == bind.Leaked && len(failed) == 1 && failed[0] == bind.V5
	for _, c := range p.Verdict.Checks {
		if c.Ran && !c.Pass {
			q.Failed = append(q.Failed, fmt.Sprintf("%s failed: %s", c.Rule, c.Details))
		}
	}
	if o.Preflight != nil {
		for _, l := range strings.Split(strings.TrimRight(o.Preflight.Summary(), "\n"), "\n") {
			q.Details = append(q.Details, strings.TrimSpace(l))
		}
	}
	return q
}

// outputAsker is a UI's coopAsker: it logs the question as the CLI does,
// then puts it to the session controller (session.Output.Ask), whose UI
// answers it, and logs the answer. yes is --yes.
type outputAsker struct {
	out session.Output
	yes bool
}

func (a outputAsker) answersAtOnce() bool { return a.yes }

func (a outputAsker) question(ctx context.Context, rep log.Reporter, q session.Question) bool {
	logQuestion(rep, q)
	if a.yes {
		rep.Log(log.Info, "  yes (--yes)")
		return true
	}
	if a.out.Ask(ctx, q) {
		rep.Log(log.Info, "  yes")
		return true
	}
	rep.Log(log.Info, "  no")
	return false
}
