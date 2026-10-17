package main

// Golden transcripts of the co-op questions and console (App PR 2,
// docs/08-coop-design.md Appendix C item 162). Each test drives one flow
// and compares every line it logged, with its level, to a file under
// testdata/coop-golden. The files were written by the stdin prompter and
// console as they were before the Asker and the controller commands, so
// a diff here is a change in what a CLI user sees. Regenerate one only for
// an intended change: go test -run TestGolden -update-coop-golden.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/internal/report"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

var updateCoopGolden = flag.Bool("update-coop-golden", false, "rewrite testdata/coop-golden from this run")

// goldenLog records each line with its level, the way report.Stdout
// would tell them apart, and calls on with it.
type goldenLog struct {
	mu    sync.Mutex
	lines []string
	on    func(msg string)
}

func (l *goldenLog) Log(level report.Level, msg string) {
	tag := map[report.Level]string{report.Info: "INFO", report.Net: "NET", report.Warn: "WARN", report.Err: "ERR", report.OK: "OK"}[level]
	l.mu.Lock()
	l.lines = append(l.lines, tag+" "+msg)
	on := l.on
	l.mu.Unlock()
	if on != nil {
		on(msg)
	}
}

func (l *goldenLog) has(sub string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range l.lines {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func (l *goldenLog) reset() {
	l.mu.Lock()
	l.lines = nil
	l.mu.Unlock()
}

func (l *goldenLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

// wait waits for a line holding sub, which a goroutine logs.
func (l *goldenLog) wait(t *testing.T, sub string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !l.has(sub) {
		if time.Now().After(deadline) {
			t.Fatalf("no %q in:\n%s", sub, strings.Join(l.snapshot(), "\n"))
		}
		time.Sleep(time.Millisecond)
	}
}

// waitFrom waits for a line exactly line (its level included) at or
// after index from.
func (l *goldenLog) waitFrom(t *testing.T, from int, line string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		l.mu.Lock()
		found := false
		for _, s := range l.lines[min(from, len(l.lines)):] {
			found = found || s == line
		}
		l.mu.Unlock()
		if found {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %q after line %d in:\n%s", line, from, strings.Join(l.snapshot(), "\n"))
		}
		time.Sleep(time.Millisecond)
	}
}

func (l *goldenLog) len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.lines)
}

// answering makes l type answer(question) into w when a question waits
// for one, as coAnswering does.
func (l *goldenLog) answering(w io.Writer, answer func(q string) string) *goldenLog {
	var mu sync.Mutex
	var q string
	l.on = func(msg string) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.HasSuffix(msg, "?"):
			q = msg
		case strings.Contains(msg, "Type y and press Enter"):
			io.WriteString(w, answer(q)+"\n")
		}
	}
	return l
}

// goldenIDs are world IDs and other 16-hex-digit tokens, which differ run
// to run.
var goldenIDs = regexp.MustCompile(`\b[0-9a-f]{16}\b`)

// checkGolden compares lines with testdata/coop-golden/name.txt.
func checkGolden(t *testing.T, name string, lines []string) {
	t.Helper()
	got := goldenIDs.ReplaceAllString(strings.Join(lines, "\n")+"\n", "<id>")
	path := filepath.Join("testdata", "coop-golden", name+".txt")
	if *updateCoopGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update-coop-golden to write it)", err)
	}
	if want := strings.ReplaceAll(string(b), "\r\n", "\n"); got != want {
		t.Errorf("%s changed:\n--- got\n%s--- want\n%s", path, got, want)
	}
}

// The prompts used below: a bind's and a scrub's, as bind.Plan.Prompt
// words them (several lines, the question last).
const (
	goldenBindPrompt = "Bind this save file to co-op world 0123456789abcdef?\n" +
		"  File 2 \"Link\" (first quest)\n" +
		"  ww-multiplayer writes a 0x2C-byte tag at 0x803C532C (dSv_reserve_c, which the game\n" +
		"  saves with the file and never reads). It lands on the memory card at your next\n" +
		"  in-game save. Nothing else in the file is written.\n" +
		"Bind it?"
	goldenScrubPrompt = "This file carries a co-op tag for world fedcba9876543210 that it was never bound with:\n" +
		"  File 1 \"Tetra\" (first quest)\n" +
		"  V3 failed: the name hash differs\n" +
		"  Nothing else is written.\n" +
		"Scrub it?"
)

func TestGoldenPrompter(t *testing.T) {
	ctx := context.Background()
	log := &goldenLog{}
	note := func(s string) { log.Log(report.Info, "## "+s) }

	note("--yes, with a no and a command waiting in stdin")
	p := newCoopPrompter(true, strings.NewReader("n\n/help\n"))
	note(fmt.Sprint("answer: ", goldenAsk(ctx, p, log, goldenBindPrompt)))

	for _, typed := range []string{"y", "yes", " Yes ", "n", "", "yes please"} {
		note(fmt.Sprintf("typed %q", typed))
		r, w := coInput(t)
		log.answering(w, func(string) string { return typed })
		note(fmt.Sprint("answer: ", goldenAsk(ctx, newCoopPrompter(false, r), log, goldenScrubPrompt)))
		log.on = nil
	}

	note("a command, then n")
	r, w := coInput(t)
	p = newCoopPrompter(false, r)
	log.answering(w, func(string) string { return "/warp\nn" })
	note(fmt.Sprint("answer: ", goldenAsk(ctx, p, log, goldenBindPrompt)))
	log.on = nil

	note("stdin closed")
	note(fmt.Sprint("answer: ", goldenAsk(ctx, newCoopPrompter(false, strings.NewReader("")), log, goldenBindPrompt)))

	note("the session ends")
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	r, _ = coInput(t)
	note(fmt.Sprint("answer: ", goldenAsk(cctx, newCoopPrompter(false, r), log, goldenScrubPrompt)))

	checkGolden(t, "prompter", log.snapshot())
}

// goldenSync is a coop-sync pipeline over a fake replica, for the offer
// and preflight paths: Confirm and Decline of a plan it did not offer do
// nothing, so only the session's own lines are logged.
func goldenSync(t *testing.T, cs *coopSession, log report.Reporter) *coopSync {
	t.Helper()
	r, err := replica.New(replica.Config{Mem: mem.NewFakeMem()})
	if err != nil {
		t.Fatal(err)
	}
	cs.rep = log
	return &coopSync{cs: cs, name: "Link", rep: log, r: r, asked: map[coopFileKey]bool{}}
}

func TestGoldenOffers(t *testing.T) {
	ctx := context.Background()
	log := &goldenLog{}
	note := func(s string) { log.Log(report.Info, "## "+s) }

	note("--yes: a bind")
	s := goldenSync(t, goldenSession(coopFlags{on: true, yes: true}, strings.NewReader("")), log)
	s.offer(ctx, replica.Offer{Plan: &bind.Plan{Kind: bind.KindBind, Prompt: goldenBindPrompt}})

	note("a scrub answered y on stdin")
	r, w := coInput(t)
	s = goldenSync(t, goldenSession(coopFlags{on: true}, r), log)
	log.answering(w, func(string) string { return "y" })
	mark := log.len()
	s.offer(ctx, replica.Offer{Plan: &bind.Plan{Kind: bind.KindScrub, Prompt: goldenScrubPrompt}})
	log.waitFrom(t, mark, "INFO   yes")
	log.on = nil

	note("a bind answered n on stdin")
	r, w = coInput(t)
	s = goldenSync(t, goldenSession(coopFlags{on: true}, r), log)
	log.answering(w, func(string) string { return "n" })
	mark = log.len()
	s.offer(ctx, replica.Offer{Plan: &bind.Plan{Kind: bind.KindBind, Prompt: goldenBindPrompt}})
	log.waitFrom(t, mark, "INFO   no")
	log.on = nil

	note("no memory-card backup")
	cs := goldenSession(coopFlags{on: true, yes: true}, strings.NewReader(""))
	cs.noTags = fmt.Errorf("no Dolphin user folder")
	s = goldenSync(t, cs, log)
	s.offer(ctx, replica.Offer{Plan: &bind.Plan{Kind: bind.KindBind, Prompt: goldenBindPrompt}})

	note("preflight: far behind")
	s = goldenSync(t, goldenSession(coopFlags{on: true}, strings.NewReader("")), log)
	s.preflight(replica.PreflightReport{Verdict: replica.PreflightFarBehind, NeedsMerge: true})
	note("preflight: needs --coop-merge")
	s.preflight(replica.PreflightReport{Verdict: replica.PreflightDivergent, NeedsMerge: true})
	note("preflight: refused")
	s.preflight(replica.PreflightReport{Verdict: replica.PreflightRefused})
	note("preflight: could bind, no backup")
	s.cs.noTags = fmt.Errorf("no Dolphin user folder")
	s.preflight(replica.PreflightReport{Verdict: replica.PreflightSubset})

	checkGolden(t, "offers", log.snapshot())
}

// goldenSession is a joiner's co-op side without the session-start
// backup and tables, reading in.
func goldenSession(f coopFlags, in io.Reader) *coopSession {
	return newGoldenSession(f, in)
}

// goldenConsoleSession is a joiner's co-op session with one pipeline per
// name, each over its own fake game in the sea, not connected; the lines
// of its start are not recorded.
func goldenConsoleSession(t *testing.T, ctx context.Context, f coopFlags, in io.Reader, names ...string) (*coopSession, []*coopSync, []*coGame, *goldenLog) {
	t.Helper()
	log := &goldenLog{}
	cs := startCoopSession(log, f, coopEnv{backups: t.TempDir(), userDirs: []string{coUserDir(t)}, input: in})
	var ss []*coopSync
	var gs []*coGame
	for _, n := range names {
		cg := newCoGame(t, n, coMine{life: 12})
		s, err := cs.newSync(ctx, n, cg.m, log)
		if err != nil {
			t.Fatal(err)
		}
		ss, gs = append(ss, s), append(gs, cg)
	}
	log.reset()
	return cs, ss, gs, log
}

// goldenConsoleDryRun is the console's dry-run transcript; do runs one
// command line.
func goldenConsoleDryRun(t *testing.T, do func(cs *coopSession, line string)) []string {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cs, _, _, log := goldenConsoleSession(t, ctx, coopFlags{on: true, yes: true, dryRun: true}, strings.NewReader(""), "Link")
	do(cs, "/warp")
	log.wait(t, "co-op: /warp for Link: --coop-dry-run writes no game data")
	do(cs, "/refresh Link")
	log.wait(t, "co-op: /refresh for Link: --coop-dry-run writes no game data")
	do(cs, "/coop off")
	do(cs, "/coop on")
	do(cs, "/status")
	do(cs, "/help")
	for _, bad := range []string{"/jump", "/", "/coop", "/coop maybe", "/help me", "/warp Tetra Link", "/warp Tetra"} {
		do(cs, bad)
	}
	return log.snapshot()
}

// goldenConsoleWriting is the console's transcript with writes on.
func goldenConsoleWriting(t *testing.T, do func(cs *coopSession, line string)) []string {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cs, ss, gs, log := goldenConsoleSession(t, ctx, coopFlags{on: true, yes: true}, strings.NewReader(""), "Link")
	s, cg := ss[0], gs[0]
	do(cs, "/refresh")
	cg.g.Frame()
	s.r.Tick()
	log.wait(t, "; try again in a moment")
	do(cs, "/coop on")
	do(cs, "/coop off")
	log.wait(t, "type /coop on to write the world again.")
	do(cs, "/warp")
	log.wait(t, "co-op: /warp for Link: co-op writes are off")
	do(cs, "/coop off")
	do(cs, "/status")
	do(cs, "/coop on")
	do(cs, "/status link")
	return log.snapshot()
}

// goldenConsoleTwo is the console's transcript with mp-local's two
// pipelines.
func goldenConsoleTwo(t *testing.T, do func(cs *coopSession, line string)) []string {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cs, _, _, log := goldenConsoleSession(t, ctx, coopFlags{on: true, yes: true, dryRun: true}, strings.NewReader(""), "Link", "Tetra")
	do(cs, "/warp")
	do(cs, "/refresh Aryll")
	do(cs, "/warp tetra")
	log.wait(t, "co-op: /warp for Tetra: --coop-dry-run writes no game data")
	do(cs, "/coop off")
	do(cs, "/status")
	do(cs, "/status Link")
	return log.snapshot()
}

func TestGoldenConsole(t *testing.T) {
	checkGolden(t, "console-dry-run", goldenConsoleDryRun(t, goldenDo))
	checkGolden(t, "console-writing", goldenConsoleWriting(t, goldenDo))
	checkGolden(t, "console-two", goldenConsoleTwo(t, goldenDo))
}

// TestGoldenConsoleStdin types the commands into the session's input, as
// a CLI user does, and checks no command is taken for an answer.
func TestGoldenConsoleStdin(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, w := coInput(t)
	_, _, _, log := goldenConsoleSession(t, ctx, coopFlags{on: true, yes: true, dryRun: true}, r, "Link")
	for _, l := range []string{"/help", "/coop off", "/jump", "/status"} {
		io.WriteString(w, l+"\n")
	}
	log.wait(t, "0 verify failures")
	io.WriteString(w, "y\n/warp\n")
	log.wait(t, "co-op: /warp for Link: --coop-dry-run writes no game data")
	checkGolden(t, "console-stdin", log.snapshot())
}

// TestGoldenConsoleHost: a host's /status adds its hub's lines, and runs
// before any pipeline does.
func TestGoldenConsoleHost(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ga := newCoGame(t, "Link", coMine{life: 12})
	h := &coHost{t: t, ctx: ctx, rep: &coLog{}, env: coopEnv{
		worlds:   t.TempDir(),
		backups:  t.TempDir(),
		userDirs: []string{coUserDir(t)},
		openMem:  func() (mem.Mem, func(), error) { return ga.m, func() {}, nil },
		tick:     func() { ga.g.Frames(3) },
		input:    strings.NewReader(""),
	}}
	h.start(coopFlags{on: true, world: coopWorldNew})
	defer h.stop()
	log := &goldenLog{}
	h.cs.rep, h.cs.console.rep = log, log
	goldenDo(h.cs, "/status")
	goldenDo(h.cs, "/warp")
	checkGolden(t, "console-host", log.snapshot())

	// As commands (the controller's): a host's /status answers with its
	// hub's, so it is no refusal; /warp is refused.
	for line, want := range map[string]string{"/status": "", "/warp": "/warp: no Dolphin runs co-op yet"} {
		cmd, err := parseCoopCommand(line)
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		if err := h.cs.console.exec(context.Background(), cmd); err != nil {
			got = err.Error()
		}
		if got != want {
			t.Errorf("%s returned %q, want %q", line, got, want)
		}
	}
}
