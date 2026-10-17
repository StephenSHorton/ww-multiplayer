package coopsession

// The co-op questions and console through the session controller (App PR
// 2): the same transcripts as the CLI's (golden_test.go), and the
// CLI's host and join driver against the direct calls it replaced.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/engine"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// coopCtrlEngine is the TUI's engine.Engine whose sessions run no
// pipelines: Host attaches the co-op side the test made, then waits for
// the session to end. The console and the questions are the real ones.
type coopCtrlEngine struct {
	*engine.Engine
	out chan session.Output
	// ctx is the running session's, set before its Output is sent.
	ctx context.Context
}

func (e *coopCtrlEngine) Host(ctx context.Context, _ context.CancelFunc, _ session.HostOptions, out session.Output) error {
	e.ctx = ctx
	e.out <- out
	<-ctx.Done()
	e.Attach(nil)
	return nil
}

// coopCtrl runs a co-op session on a coopCtrlEngine, ended with the test,
// and returns its controller, engine and Output.
func coopCtrl(t *testing.T) (*session.Controller, *coopCtrlEngine, session.Output) {
	t.Helper()
	eng := &coopCtrlEngine{Engine: newTUIEngine(), out: make(chan session.Output, 1)}
	ctrl := session.New(eng, session.Options{})
	if _, err := ctrl.Host(context.Background(), session.HostOptions{Coop: session.CoopOptions{On: true}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ctrl.Leave)
	return ctrl, eng, <-eng.out
}

// TestGoldenConsoleController: every console transcript is the same when
// the lines are given to the controller, as typed (CommandLine) or as
// commands (Command), as when they are typed on stdin. A line returns
// nil whatever came of it (its outcome is logged); a command also
// returns the refusal that kept it from every pipeline, the same text
// as its WARN line.
func TestGoldenConsoleController(t *testing.T) {
	for _, mode := range []string{"line", "command"} {
		t.Run(mode, func(t *testing.T) {
			ctrl, eng, _ := coopCtrl(t)
			var refused []string
			do := func(cs *Session, line string) {
				eng.Attach(cs)
				cmd, err := parseCoopCommand(line)
				if mode == "line" || err != nil {
					if err := ctrl.CommandLine(line); err != nil {
						t.Fatalf("%s: %v", line, err)
					}
					return
				}
				if err := ctrl.Command(session.Command{Verb: session.Verb(cmd.verb), Player: cmd.who}); err != nil {
					refused = append(refused, err.Error())
				}
			}
			for _, g := range []struct {
				name    string
				run     func(*testing.T, func(*Session, string)) []string
				refused []string
			}{
				{"console-dry-run", goldenConsoleDryRun, []string{"/warp Tetra: no player Tetra here (Link)"}},
				{"console-writing", goldenConsoleWriting, nil},
				{"console-two", goldenConsoleTwo, []string{"/warp: say which player: /warp Link or /warp Tetra", "/refresh Aryll: no player Aryll here (Link, Tetra)"}},
			} {
				refused = nil
				lines := g.run(t, do)
				checkGolden(t, g.name, lines)
				if mode == "line" {
					g.refused = nil
				}
				if !slices.Equal(refused, g.refused) {
					t.Errorf("%s: Command refused %q, want %q", g.name, refused, g.refused)
				}
				for _, r := range refused {
					if !slices.Contains(lines, "WARN co-op: "+r) {
						t.Errorf("%s: refusal %q is not logged", g.name, r)
					}
				}
			}
		})
	}
}

// TestControllerCommandErrors: the commands need a running session with
// co-op on, and a well-formed command.
func TestControllerCommandErrors(t *testing.T) {
	eng := &coopCtrlEngine{Engine: newTUIEngine(), out: make(chan session.Output, 1)}
	ctrl := session.New(eng, session.Options{})
	if err := ctrl.Command(session.Command{Verb: session.VerbWarp}); !errors.Is(err, session.ErrNoSession) {
		t.Errorf("no session: %v", err)
	}
	if _, err := ctrl.Host(context.Background(), session.HostOptions{}); err != nil {
		t.Fatal(err)
	}
	<-eng.out
	if err := ctrl.CommandLine("/warp"); !errors.Is(err, session.ErrNoCoop) {
		t.Errorf("co-op off: %v", err)
	}
	ctrl.Leave()

	ctrl, eng2, _ := coopCtrl(t)
	if err := ctrl.Command(session.Command{Verb: session.VerbStatus}); !errors.Is(err, session.ErrNoCoop) {
		t.Errorf("co-op side not started yet: %v", err)
	}
	eng2.Attach(goldenSession(Flags{on: true}, strings.NewReader("")))
	eng2.CoopSide().(*Session).console = newCoopConsole(log.Discard{}, nil)
	for _, c := range []session.Command{{Verb: "jump"}, {Verb: session.VerbHelp, Player: "Link"}, {Verb: session.VerbWarp, Player: "two words"}, {Verb: session.VerbWarp, Player: strings.Repeat("x", 40)}} {
		if err := ctrl.Command(c); err == nil {
			t.Errorf("%+v was taken", c)
		}
	}
	// Well formed, but no Dolphin runs co-op yet: refused, and logged.
	if err := ctrl.Command(session.Command{Verb: session.VerbCoopOff, Player: "Link"}); err == nil || err.Error() != "/coop off: no Dolphin runs co-op yet" {
		t.Errorf("/coop off Link with no pipeline: %v", err)
	}
	if err := ctrl.CommandLine("/coop off Link"); err != nil {
		t.Errorf("/coop off Link as a line: %v (its outcome is only logged)", err)
	}
	if err := ctrl.Command(session.Command{Verb: session.VerbHelp}); err != nil {
		t.Errorf("/help: %v", err)
	}
}

// TestControllerQuestions: through the controller, a question is the
// offer's (kind, player, title, the whole prompt, a bind's preflight),
// open in the snapshot until answered; the log says it as the CLI does,
// with the answer, and without stdin's "Type y" line. A session ending
// answers no.
func TestControllerQuestions(t *testing.T) {
	ctrl, eng, out := coopCtrl(t)
	ctx := eng.ctx // the session's: the pipelines ask under it
	events, unsub := ctrl.Subscribe()
	defer unsub()
	log := &goldenLog{}
	cs := goldenSession(Flags{on: true}, strings.NewReader(""))
	cs.ask, cs.advise = outputAsker{out: out}, out.Advise
	s := goldenSync(t, cs, log)

	next := func(kind session.EventKind) session.Event {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case ev := <-events:
				if ev.Kind == kind {
					return ev
				}
			case <-deadline:
				t.Fatalf("no event %d", kind)
			}
		}
	}

	pf := &replica.PreflightReport{Verdict: replica.PreflightSubset, Why: "the file is a subset of the world"}
	pf.WorldByCategory[replica.CatStory] = 2
	s.offer(ctx, replica.Offer{Plan: &bind.Plan{Kind: bind.KindBind, Prompt: goldenBindPrompt}, Preflight: pf})
	ev := next(session.EventQuestion)
	q := ev.Question
	wantDetails := []string{"preflight: subset: the file is a subset of the world", "the file adds: nothing", "the world adds: story events 2"}
	if q.ID == "" || q.Kind != session.QuestionBind || q.Player != "Link" || q.Title != "Bind this save file to co-op world 0123456789abcdef?" ||
		q.Body != goldenBindPrompt || !slices.Equal(q.Details, wantDetails) {
		t.Errorf("question %+v, want details %q", q, wantDetails)
	}
	if st := ctrl.Snapshot(); len(st.Questions) != 1 || st.Questions[0].ID != q.ID {
		t.Errorf("open questions %+v", st.Questions)
	}
	if err := ctrl.Answer(q.ID, true); err != nil {
		t.Fatal(err)
	}
	if ev := next(session.EventQuestionClosed); ev.Question.ID != q.ID || !ev.Yes {
		t.Errorf("closed %+v", ev)
	}
	if err := ctrl.Answer(q.ID, false); !errors.Is(err, session.ErrNoQuestion) {
		t.Errorf("a second answer: %v", err)
	}
	log.wait(t, "  yes")

	s.offer(ctx, replica.Offer{Plan: &bind.Plan{Kind: bind.KindScrub, Prompt: goldenScrubPrompt}})
	q = next(session.EventQuestion).Question
	if q.Kind != session.QuestionScrub || q.Title != "This file carries a co-op tag for world fedcba9876543210 that it was never bound with:" || q.Details != nil {
		t.Errorf("scrub question %+v", q)
	}
	ctrl.Answer(q.ID, false)
	next(session.EventQuestionClosed)
	log.wait(t, "  no")

	s.preflight(replica.PreflightReport{Verdict: replica.PreflightFarBehind, NeedsMerge: true})
	if a := next(session.EventAdvice).Advice; a.Kind != session.AdviceFarBehind || a.Player != "Link" || !strings.HasPrefix(a.Text, "co-op: the world is far ahead of this file") {
		t.Errorf("advice %+v", a)
	}
	s.preflight(replica.PreflightReport{Verdict: replica.PreflightDivergent, NeedsMerge: true})
	if a := next(session.EventAdvice).Advice; a.Kind != session.AdviceNeedsMerge {
		t.Errorf("advice %+v", a)
	}

	// The session ends with a question open: a no.
	s.offer(ctx, replica.Offer{Plan: &bind.Plan{Kind: bind.KindBind, Prompt: goldenBindPrompt}})
	q = next(session.EventQuestion).Question
	ctrl.Leave()
	if ev := next(session.EventQuestionClosed); ev.Question.ID != q.ID || ev.Yes {
		t.Errorf("closed at the end %+v", ev)
	}
	log.wait(t, "INFO   no")
	if st := ctrl.Snapshot(); len(st.Questions) != 0 {
		t.Errorf("questions left open: %+v", st.Questions)
	}
	checkGolden(t, "offers-controller", log.snapshot())
}

// TestControllerLiveCoop: the snapshot carries each pipeline's status,
// with /status's lines.
func TestControllerLiveCoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctrl, eng, _ := coopCtrl(t)
	cs, ss, _, _ := goldenConsoleSession(t, ctx, Flags{on: true, yes: true, dryRun: true}, strings.NewReader(""), "Link", "Tetra")
	eng.Attach(cs)
	got := ctrl.Snapshot().CoopStatus
	if len(got) != 2 || got[0].Player != "Link" || got[1].Player != "Tetra" {
		t.Fatalf("coop status %+v", got)
	}
	want := formatStatus("Link", ss[0].r.Status())
	if g := got[0]; !slices.Equal(g.Lines, want) || g.Writes != "a dry run (--coop-dry-run)" || !strings.HasPrefix(g.State, "off") {
		t.Errorf("Link: %+v, want lines %q", g, want)
	}
}

// cliEngine is a session.Engine whose session logs lines and returns err:
// the outcomes engine.RunHostSession and engine.RunJoinSession have.
type cliEngine struct{ err error }

// lines logs what the session prints; the engine then returns e.err,
// marked as the CLI's engine marks it (engine.SessionErr).
func (e cliEngine) lines(out log.Reporter) {
	out.Log(log.OK, `Hosting as "Link" on :25565.`)
	out.Log(log.Info, "Ctrl+C to stop.")
	if errors.Is(e.err, network.ErrFatal) {
		out.Log(log.Err, "Can't join localhost:25565: the server is full")
	}
}

func (e cliEngine) run(out session.Output) error {
	e.lines(out)
	return engine.SessionErr(e.err)
}

func (e cliEngine) Host(_ context.Context, _ context.CancelFunc, _ session.HostOptions, out session.Output) error {
	return e.run(out)
}

func (e cliEngine) Join(_ context.Context, _ context.CancelFunc, _ session.JoinOptions, out session.Output) error {
	return e.run(out)
}

func (cliEngine) Live() session.Live         { return session.Live{} }
func (cliEngine) SendChat(text string) error { return nil }

// TestRunCLISession: the CLI's host and join through the controller print
// the lines, and exit with the code, of the direct call they replaced
// (engine.RunHostSession, then engine.LogSessionErr and os.Exit(1) on an error), for
// each way a session ends.
func TestRunCLISession(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
	}{
		{"cancelled", nil},
		{"a pipeline failed", errors.New("puppet-sync: dolphin: GZLE01 not found")},
		{"a fatal connect error", fmt.Errorf("broadcast-pose: %w", network.ErrFatal)},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Before: the session's lines to stdout, then, on an error,
			// engine.LogSessionErr and exit 1 (runHost and runJoin).
			old := &goldenLog{}
			oldCode := 0
			(cliEngine{c.err}).lines(old)
			if c.err != nil {
				engine.LogSessionErr(old, c.err)
				oldCode = 1
			}
			// After: the controller, logging to the same kind of reporter.
			now := &goldenLog{}
			ctrl := session.New(cliEngine{c.err}, session.Options{Log: now})
			code := engine.RunCLISession(ctrl, now, func() (session.ID, error) {
				return ctrl.Host(context.Background(), session.HostOptions{Name: "Link"})
			})
			if code != oldCode || !slices.Equal(now.snapshot(), old.snapshot()) {
				t.Errorf("exit %d with\n%s\nwant exit %d with\n%s", code, strings.Join(now.snapshot(), "\n"), oldCode, strings.Join(old.snapshot(), "\n"))
			}
		})
	}
	t.Run("can't start", func(t *testing.T) {
		log := &goldenLog{}
		ctrl := session.New(nil, session.Options{Log: log})
		if code := engine.RunCLISession(ctrl, log, func() (session.ID, error) { return ctrl.Host(context.Background(), session.HostOptions{}) }); code != 1 || !log.has("ERR sessions aren't available") {
			t.Errorf("exit %d, log %q", code, log.snapshot())
		}
	})
}
