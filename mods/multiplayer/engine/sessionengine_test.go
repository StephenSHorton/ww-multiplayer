package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// TestSessionErr: a fatal connect error was already logged by the
// session's ConnectLog, so the controller must not log it again, but the
// session still failed (the CLI exits 1); every other error is the
// session's end reason, unchanged.
func TestSessionErr(t *testing.T) {
	if err := sessionErr(nil); err != nil {
		t.Errorf("sessionErr(nil) = %v", err)
	}
	if err := sessionErr(fmt.Errorf("puppet-sync: %w", network.ErrFatal)); !errors.Is(err, session.ErrReported) || !errors.Is(err, network.ErrFatal) {
		t.Errorf("sessionErr(fatal) = %v, want it marked ErrReported", err)
	}
	other := errors.New("server start: bind")
	if err := sessionErr(other); err != other {
		t.Errorf("sessionErr(other) = %v, want it unchanged", err)
	}
}

// TestSessionEngine_LiveIdle: with no session registered the engine reports
// zero live values, as the old hook accessors did.
func TestSessionEngine_LiveIdle(t *testing.T) {
	e := newTUIEngine()
	if got := e.Live(); got.PlayerCount != 0 || got.Players != nil || got.HostAddrs != nil || got.Latency != 0 || got.LocalOK {
		t.Errorf("idle Live() = %+v, want zero", got)
	}
	if err := e.SendChat("hi"); err != nil {
		t.Errorf("SendChat with no client = %v, want nil (dropped)", err)
	}
	var _ session.Engine = e
	var _ session.CoopEngine = e
	if cli := newCLIEngine(); cli.Live().PlayerCount != 0 || cli.SendChat("hi") != nil {
		t.Error("the CLI engine has live values or a chat")
	}
}

// frontOutput is a session.Output that records the questions put to it
// (answering ans) and the advice passed to it.
type frontOutput struct {
	log.Discard
	ans    bool
	mu     sync.Mutex
	asked  []session.Question
	advice []session.Advice
}

func (o *frontOutput) Chat(string, string) {}

func (o *frontOutput) Ask(_ context.Context, q session.Question) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.asked = append(o.asked, q)
	return o.ans
}

func (o *frontOutput) Advise(a session.Advice) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.advice = append(o.advice, a)
}

// TestSessionEngine_Front: each engine wires its own front end into the
// co-op side its sessions start. The CLI's asks on stdin (the prompter,
// no advice sink); the TUI's asks the controller, carrying --yes, and
// passes advice on; both attach the session so the console commands and
// the status reach it, and detach it when the session returns.
func TestSessionEngine_Front(t *testing.T) {
	start := func(e *sessionEngine, out session.Output, o session.CoopOptions) *coopSession {
		t.Helper()
		return startCoopSession(log.Discard{}, coopFlagsOf(o), coopEnv{
			backups:  t.TempDir(),
			userDirs: []string{coUserDir(t)},
			input:    strings.NewReader(""), // the CLI's prompter reads this, not the test's stdin
			front:    e.front(out, o),
		})
	}

	cli := newCLIEngine()
	out := &frontOutput{}
	if f := cli.front(out, session.CoopOptions{On: true}); f.ask != nil || f.advise != nil || f.attach == nil {
		t.Errorf("the CLI's front: ask %v, advise set %v, attach set %v; want the stdin prompter, no advice sink, attach", f.ask, f.advise != nil, f.attach != nil)
	}
	cs := start(cli, out, session.CoopOptions{On: true, Yes: true})
	if p, ok := cs.ask.(*coopPrompter); !ok || !p.yes {
		t.Errorf("the CLI's asker %T (%+v), want a *coopPrompter with --yes", cs.ask, cs.ask)
	}
	if cs.advise != nil || cs.console.cmds == nil {
		t.Errorf("the CLI: advise set %v, console reads stdin %v", cs.advise != nil, cs.console.cmds != nil)
	}
	if cli.coopSide() != cs {
		t.Error("the CLI engine didn't attach the session")
	}
	cli.begin(out)() // the session returns
	if cli.coopSide() != nil {
		t.Error("the CLI engine kept the ended session")
	}

	for _, yes := range []bool{false, true} {
		tui := newTUIEngine()
		out := &frontOutput{ans: true}
		cs := start(tui, out, session.CoopOptions{On: true, Yes: yes})
		a, ok := cs.ask.(outputAsker)
		if !ok || a.yes != yes || a.out != session.Output(out) {
			t.Fatalf("yes=%v: the TUI's asker %T (%+v), want an outputAsker on the session's Output", yes, cs.ask, cs.ask)
		}
		if cs.console.cmds != nil {
			t.Errorf("yes=%v: the TUI's console reads stdin", yes)
		}
		if cs.advise == nil {
			t.Fatalf("yes=%v: the TUI's advice goes nowhere", yes)
		}
		cs.advise(session.Advice{Kind: session.AdviceFarBehind, Player: "Link"})
		if len(out.advice) != 1 || out.advice[0].Player != "Link" {
			t.Errorf("yes=%v: advice reached the Output as %+v", yes, out.advice)
		}
		if tui.coopSide() != cs {
			t.Errorf("yes=%v: the TUI engine didn't attach the session", yes)
		}
		tui.begin(out)()
		if tui.coopSide() != nil {
			t.Errorf("yes=%v: the TUI engine kept the ended session", yes)
		}
	}
}

// TestOutputAsker: the TUI's asker logs the question as the CLI prints
// it, then the answer; --yes answers yes without asking the UI.
func TestOutputAsker(t *testing.T) {
	q := session.Question{ID: "q1", Kind: session.QuestionBind, Player: "Link", Body: "Bind this?\n  File 2\nBind it?"}
	for _, c := range []struct {
		yes, ans bool
		want     string
		asked    int
	}{
		{yes: true, ans: false, want: "  yes (--yes)", asked: 0},
		{ans: true, want: "  yes", asked: 1},
		{ans: false, want: "  no", asked: 1},
	} {
		out := &frontOutput{ans: c.ans}
		log := &goldenLog{}
		got := outputAsker{out: out, yes: c.yes}.question(context.Background(), log, q)
		if got != (c.yes || c.ans) {
			t.Errorf("%+v: answered %v", c, got)
		}
		want := []string{"INFO Bind this?", "INFO   File 2", "INFO Bind it?", "INFO " + c.want}
		if lines := log.snapshot(); !slices.Equal(lines, want) {
			t.Errorf("%+v: logged %q, want %q", c, lines, want)
		}
		if len(out.asked) != c.asked || c.asked == 1 && out.asked[0].ID != "q1" {
			t.Errorf("%+v: the UI was asked %+v", c, out.asked)
		}
		if a := (outputAsker{out: out, yes: c.yes}); a.answersAtOnce() != c.yes {
			t.Errorf("%+v: answersAtOnce %v", c, a.answersAtOnce())
		}
	}
}
