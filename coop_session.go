package main

// Co-op sessions (#23; docs/08-coop-design.md §4.1-4.2, §5, §9 M2): what
// --coop adds to host, join and mp-local.
//
//   - host and mp-local run the CoopHub (internal/coop/hub) on their
//     server. It serves one world: the only one on disk, the one
//     --coop=<id> names, or, with --coop=new, one founded from the host's
//     loaded, idle file (§5.3);
//   - every side backs up its memory cards, read-only, when the session
//     starts (internal/coop/backup, §5.4). Without that backup no tag is
//     written this session;
//   - each Dolphin gets a coop-sync pipeline beside broadcast-pose and
//     puppet-sync, with its own Dolphin handle by PID and its own role-1
//     connection (network.RunCoop). Its replica (internal/coop/replica)
//     reads a snapshot ten times a second, runs the gates and the differ,
//     sends GAINS, and only logs what its planner would write: M2 is a
//     dry run (§5.1 principle 5);
//   - the replica's two tag writes, a bind and a leaked tag's scrub, are
//     asked on the command line (y/N on stdin; --yes answers yes) and made
//     only after the yes, behind the gates (internal/coop/bind).
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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/backup"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/mem"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/snap"
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
}

// coopWorldNew is --coop=new: found a world from the host's loaded file.
const coopWorldNew = "new"

// coopFlagsUsage is the co-op part of host's and mp-local's usage lines;
// join takes only --coop and --yes.
const coopFlagsUsage = "[--coop[=new|<world-id>]] [--coop-merge] [--yes]"

// extractCoopFlags takes the co-op options out of args (in any position
// before a bare "--", which ends the options) and returns the rest, in
// order, for the command's own parser. When args hold no --coop at all,
// they are returned untouched and co-op is off, so a command line without
// co-op parses exactly as it did before: --coop-merge or --yes alone stay
// in rest, where the command's parser treats them as it always has.
func extractCoopFlags(args []string) (coopFlags, []string, error) {
	var f coopFlags
	if !slices.ContainsFunc(coopOptionArgs(args), func(a string) bool {
		return a == "--coop" || strings.HasPrefix(a, "--coop=")
	}) {
		return f, args, nil
	}
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

// forJoin checks f for join: a joiner serves no world, so the world and
// --coop-merge are the host's to choose.
func (f coopFlags) forJoin() error {
	switch {
	case f.world != "":
		return fmt.Errorf("--coop=%s: the host picks the world; join with --coop", f.world)
	case f.merge:
		return errors.New("--coop-merge is the host's choice, not the joiner's")
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
	// input, if set, replaces stdin as the source of y/N answers.
	input io.Reader
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
		tick: coopTick,
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
}

// startCoopSession begins a joiner's co-op side, and is the first step of
// a host's: it backs up the memory cards, and without a backup it turns
// tag writes off for the session (§5.4: the card is backed up before a
// file is bound).
func startCoopSession(rep report.Reporter, f coopFlags, env coopEnv) *coopSession {
	cs := &coopSession{flags: f, rep: rep, prompt: newCoopPrompter(f.yes, env.input)}
	b, err := backup.Create(env.userDirs, backup.Options{Root: env.backups, Label: "co-op session start"})
	if err != nil {
		cs.noTags = err
		report.Logf(rep, report.Warn, "co-op: could not back up the memory cards: %v. No co-op tag is written this session (set USER_DIR_1 if Dolphin keeps its user folder elsewhere).", err)
		return cs
	}
	report.Logf(rep, report.OK, "co-op: backed up %d memory-card file(s) to %s (`coop-world restore %s --yes`, with Dolphin closed, puts them back). Check that these are your cards:", len(b.Files), b.Dir, b.ID)
	logBackupCards(rep, b)
	return cs
}

// logBackupCards lists a backup's cards, each with the files copied from
// it, and warns when Dolphin.ini selects none of them: the card locations
// are Dolphin's (backup.Locate), so the owner checks them (§8.2).
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
		report.Logf(rep, report.Warn, "co-op: Dolphin.ini marks none of these cards as in use. If your Dolphin keeps its user folder elsewhere (a portable User folder, say), set USER_DIR_1 (and USER_DIR_2) to it, or this backup misses your card.")
	}
}

// startCoopHost begins a host's co-op side (host and mp-local): the
// backup, the world (selectCoopWorld, or a founding) and the hub, which
// the caller sets as its server's Coop before Start. srvLog gets the
// hub's lines, beside the server's own. Stop it with stop.
func startCoopHost(ctx context.Context, rep report.Reporter, srvLog func(string), f coopFlags, env coopEnv) (*coopSession, error) {
	cs := startCoopSession(rep, f, env)
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
		w, err := foundCoopWorld(ctx, rep, m, env.tick)
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
	h, err := hub.New(hub.Config{
		Store:    st,
		Features: hub.DefaultFeatures,
		Merge:    f.merge,
		Logf:     func(format string, args ...any) { srvLog(fmt.Sprintf(format, args...)) },
	})
	if err != nil {
		st.Stop()
		return nil, err
	}
	cs.hub, cs.store, cs.world, cs.dir = h, st, bind.WorldID(st.ID()), st.Dir()
	if prev, err := writeCoopMarker(cs.dir); err != nil {
		report.Logf(rep, report.Warn, "co-op: %v", err)
	} else if prev != "" {
		report.Logf(rep, report.Warn, "co-op: world %s was marked in use (%s): its last session did not end cleanly, or another host runs it, which would lose changes", cs.world, prev)
	}
	merge := ""
	if f.merge {
		merge = "; --coop-merge: files with progress the world lacks may bind"
	}
	report.Logf(rep, report.OK, "Co-op world %s at seq %d (dry run: planned writes are logged, not made; only a binding tag is ever written, after a yes%s).", cs.world, st.Seq(), merge)
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
	tombs   int    // tombstoned bits
	from    string // the file it loads from, when not world.wwco
	err     error  // why it can't be loaded
	inUse   string // its session marker, if any
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
	if w.inUse != "" {
		s += ", in use: " + w.inUse
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
		info.inUse, _ = readCoopMarker(world.Path(root, id))
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
func foundCoopWorld(ctx context.Context, rep report.Reporter, m mem.Mem, tick func()) (*world.World, error) {
	var told time.Time
	for {
		s, st, _, err := gateSnapshots(m, tick)
		if err != nil {
			return nil, fmt.Errorf("co-op: founding a world: %w", err)
		}
		if st.CanWriteIdle() {
			return foundFrom(rep, s)
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

// foundFrom founds a world from snapshot s's file.
func foundFrom(rep report.Reporter, s *snap.Snapshot) (*world.World, error) {
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
	img := world.Image(s.Image())
	r := w.Merge(&img)
	report.Logf(rep, report.OK, "co-op: founded world %s from %s (%d shared bytes).", id, bind.Describe(f), len(r.Delta))
	return w, nil
}

// The session marker: a host writes it into its world's directory while
// its hub runs, so coop-world block, which edits the world file, can tell
// the file is in use (the hub's next write would undo the edit).
const coopMarkerName = "session.lock"

// writeCoopMarker marks the world in dir as in use by this process, and
// returns the marker it replaced ("" for none).
func writeCoopMarker(dir string) (prev string, err error) {
	prev, _ = readCoopMarker(dir)
	line := fmt.Sprintf("pid %d since %s", os.Getpid(), time.Now().Format(time.DateTime))
	if err := os.WriteFile(filepath.Join(dir, coopMarkerName), []byte(line+"\n"), 0o644); err != nil {
		return prev, fmt.Errorf("marking the world in use: %w", err)
	}
	return prev, nil
}

// readCoopMarker returns the marker of the world in dir, if it has one.
func readCoopMarker(dir string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(dir, coopMarkerName))
	if err != nil {
		return "", false
	}
	s := strings.TrimSpace(string(b))
	if len(s) > 80 {
		s = s[:80]
	}
	return strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7F {
			return ' '
		}
		return r
	}, s), true
}

func removeCoopMarker(dir string) { os.Remove(filepath.Join(dir, coopMarkerName)) }

// coopPrompter asks the replicas' yes/no questions on the command line,
// one at a time, so mp-local's two Dolphins never ask at once.
type coopPrompter struct {
	yes   bool
	mu    sync.Mutex        // held for a whole question
	lines <-chan promptLine // the lines read, oldest first; closed at EOF; nil with --yes
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

// newCoopPrompter returns a session's prompter. Unless yes answers every
// question, it starts reading in (stdin when nil) at once, when the session
// starts: a line counts only if it was read after its question was asked,
// so whatever was typed before, even a line already waiting in the
// terminal when the session started, never answers one.
func newCoopPrompter(yes bool, in io.Reader) *coopPrompter {
	p := &coopPrompter{yes: yes, now: time.Now}
	if !yes {
		if in == nil {
			in = os.Stdin
		}
		p.in = &promptInput{r: in, start: time.Now()}
		p.lines = readPromptLines(p.in, p.now)
	}
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
// line the moment it is read. It never waits for ask to take a line, so a
// stamp is never later than the read: a full queue drops the line.
func readPromptLines(in *promptInput, now func() time.Time) <-chan promptLine {
	ch := make(chan promptLine, promptQueue)
	go func() {
		defer close(ch)
		defer in.eof.Store(true)
		sc := bufio.NewScanner(in)
		for sc.Scan() {
			select {
			case ch <- promptLine{text: sc.Text(), at: now()}:
			default:
			}
		}
	}()
	return ch
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
	rep    report.Reporter
	m      mem.Mem
	r      *replica.Replica
	client *network.Client

	mu    sync.Mutex
	asked map[coopFileKey]bool // files a bind was asked for this session
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

// newSync builds a coop-sync pipeline over m. Its offers are asked with
// ctx, the session's.
func (cs *coopSession) newSync(ctx context.Context, name string, m mem.Mem, rep report.Reporter) (*coopSync, error) {
	s := &coopSync{cs: cs, rep: rep, m: m, asked: map[coopFileKey]bool{}}
	logf := func(format string, args ...any) {
		rep.Log(report.Info, strings.TrimRight(fmt.Sprintf(format, args...), "\n")) // a preflight summary ends in one
	}
	r, err := replica.New(replica.Config{
		Mem:         m,
		World:       cs.world,
		Logf:        logf,
		OnOffer:     func(o replica.Offer) { s.offer(ctx, o) },
		OnPreflight: s.preflight,
	})
	if err != nil {
		return nil, err
	}
	s.r = r
	s.client = network.NewCoopClient(name, 0)
	s.client.OnLog = func(msg string) { rep.Log(report.Net, msg) }
	s.client.OnCoop = r.Handle
	return s, nil
}

// run ticks the replica and keeps its connection up until ctx ends. A
// host that refuses co-op (it runs without --coop) or another fatal
// connect error turns co-op off for the session, said once; the pipeline
// then waits for the session to end, as pose sync goes on without it.
// Reachability is the pose pipelines' to report: they dial the same host,
// so the co-op connection's retries are not logged twice.
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
	report.Logf(s.rep, report.Warn, "co-op is off for this session: %v", err)
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
