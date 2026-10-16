package session

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/report"
)

// coopFake is a fakeEngine with a console: it records the commands and
// lines it is given, and lists worlds.
type coopFake struct {
	*fakeEngine
	mu    sync.Mutex
	cmds  []Command
	lines []string
}

func (e *coopFake) Command(c Command) error {
	e.mu.Lock()
	e.cmds = append(e.cmds, c)
	e.mu.Unlock()
	return nil
}

func (e *coopFake) CommandLine(line string) error {
	e.mu.Lock()
	e.lines = append(e.lines, line)
	e.mu.Unlock()
	return nil
}

func (e *coopFake) CoopWorlds() ([]CoopWorld, error) {
	return []CoopWorld{{ID: "0123456789abcdef", Seq: 7}}, nil
}

// TestQuestions: a question is open, in the snapshot and as an event,
// until Answer; a session's end closes the questions still open with a
// no, and an answer then is refused.
func TestQuestions(t *testing.T) {
	e := newFake()
	c := New(e, Options{})
	events, unsub := c.Subscribe()
	defer unsub()
	id, err := c.Host(context.Background(), HostOptions{Coop: CoopOptions{On: true}})
	if err != nil {
		t.Fatal(err)
	}
	s := waitStarted(t, e)
	ctx := e.lastCtx()

	answers := make(chan bool, 2)
	go func() { answers <- s.out.Ask(ctx, Question{Kind: QuestionBind, Title: "Bind it?"}) }()
	ev := collect(t, events, func(ev Event) bool { return ev.Kind == EventQuestion })
	q := ev[len(ev)-1].Question
	if q.ID != "q1" || q.Title != "Bind it?" || ev[len(ev)-1].Session != id {
		t.Fatalf("question event %+v", ev[len(ev)-1])
	}
	if st := c.Snapshot(); len(st.Questions) != 1 || st.Questions[0].ID != q.ID || !st.Coop.On {
		t.Fatalf("snapshot %+v", st)
	}
	if err := c.Answer("q9", true); !errors.Is(err, ErrNoQuestion) {
		t.Errorf("Answer of no question = %v", err)
	}
	if err := c.Answer(q.ID, true); err != nil {
		t.Fatal(err)
	}
	if !<-answers {
		t.Error("answered yes, asked no")
	}
	ev = collect(t, events, func(ev Event) bool { return ev.Kind == EventQuestionClosed })
	if cl := ev[len(ev)-1]; cl.Question.ID != q.ID || !cl.Yes {
		t.Errorf("closed %+v", cl)
	}
	if st := c.Snapshot(); len(st.Questions) != 0 {
		t.Errorf("still open: %+v", st.Questions)
	}

	go func() { answers <- s.out.Ask(ctx, Question{Kind: QuestionScrub}) }()
	ev = collect(t, events, func(ev Event) bool { return ev.Kind == EventQuestion })
	q2 := ev[len(ev)-1].Question
	if q2.ID != "q2" {
		t.Errorf("second question %q", q2.ID)
	}
	c.Leave()
	if <-answers {
		t.Error("the session's end answered yes")
	}
	if err := c.Answer(q2.ID, true); !errors.Is(err, ErrNoQuestion) {
		t.Errorf("Answer after the end = %v", err)
	}
	s.out.Advise(Advice{Kind: AdviceNoBackup, Text: "x"})
	ev = collect(t, events, func(ev Event) bool { return ev.Kind == EventAdvice })
	if a := ev[len(ev)-1].Advice; a.Kind != AdviceNoBackup {
		t.Errorf("advice %+v", a)
	}
}

// TestWaitAndFailed: Wait returns once the session ended, with Failed
// set for an engine error, logged unless it wraps ErrReported.
func TestWaitAndFailed(t *testing.T) {
	for _, c := range []struct {
		name   string
		err    error
		failed bool
		logged bool
	}{
		{"cancelled", nil, false, false},
		{"failed", errors.New("puppet-sync: boom"), true, true},
		{"reported", fmt.Errorf("%w: %w", ErrReported, errors.New("fatal")), true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newFake()
			e.fail = c.err
			var log []string
			var mu sync.Mutex
			ctl := New(e, Options{Log: reporterFunc(func(msg string) { mu.Lock(); log = append(log, msg); mu.Unlock() })})
			id, err := ctl.Join(context.Background(), JoinOptions{})
			if err != nil {
				t.Fatal(err)
			}
			s := waitStarted(t, e)
			if c.err == nil {
				s.cancel()
			}
			st := ctl.Wait(id)
			if st.Phase != Ended || st.Failed != c.failed {
				t.Errorf("state %+v, want failed %v", st, c.failed)
			}
			mu.Lock()
			logged := len(log) > 0
			mu.Unlock()
			if logged != c.logged {
				t.Errorf("logged %q, want logged %v", log, c.logged)
			}
			if again := ctl.Wait(id); again.Session != id || again.Phase != Ended {
				t.Errorf("Wait after the end = %+v", again)
			}
		})
	}
}

type reporterFunc func(msg string)

func (f reporterFunc) Log(_ report.Level, msg string) { f(msg) }

// TestCommands: the console commands reach a CoopEngine's running
// session with co-op on, and nothing else.
func TestCommands(t *testing.T) {
	plain := New(newFake(), Options{})
	if err := plain.Command(Command{Verb: VerbWarp}); !errors.Is(err, ErrNoSession) {
		t.Errorf("no session: %v", err)
	}
	if _, err := plain.CoopWorlds(); !errors.Is(err, ErrNoCoop) {
		t.Errorf("worlds without a CoopEngine: %v", err)
	}

	e := &coopFake{fakeEngine: newFake()}
	c := New(e, Options{})
	if ws, err := c.CoopWorlds(); err != nil || len(ws) != 1 || ws[0].Seq != 7 {
		t.Errorf("worlds %+v, %v", ws, err)
	}
	if _, err := c.Host(context.Background(), HostOptions{}); err != nil {
		t.Fatal(err)
	}
	waitStarted(t, e.fakeEngine)
	if err := c.Command(Command{Verb: VerbWarp}); !errors.Is(err, ErrNoCoop) {
		t.Errorf("co-op off: %v", err)
	}
	c.Leave()

	if _, err := c.Join(context.Background(), JoinOptions{Coop: CoopOptions{On: true}}); err != nil {
		t.Fatal(err)
	}
	waitStarted(t, e.fakeEngine)
	defer c.Leave()
	if err := c.Command(Command{Verb: VerbCoopOff, Player: "Link"}); err != nil {
		t.Fatal(err)
	}
	if err := c.CommandLine("/status"); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !slices.Equal(e.cmds, []Command{{Verb: VerbCoopOff, Player: "Link"}}) || !slices.Equal(e.lines, []string{"/status"}) {
		t.Errorf("commands %+v, lines %q", e.cmds, e.lines)
	}
}

// TestAskAfterRunEnded: a question asked by a session the user has
// already left (its ctx is done) is a no at once, and never shows in a
// later session's snapshot.
func TestAskAfterRunEnded(t *testing.T) {
	e := newFake()
	c := New(e, Options{})
	if _, err := c.Host(context.Background(), HostOptions{}); err != nil {
		t.Fatal(err)
	}
	s := waitStarted(t, e)
	ctx := e.lastCtx()
	c.Leave()
	if _, err := c.Host(context.Background(), HostOptions{}); err != nil {
		t.Fatal(err)
	}
	waitStarted(t, e)
	defer c.Leave()
	done := make(chan bool)
	go func() { done <- s.out.Ask(ctx, Question{}) }()
	select {
	case yes := <-done:
		if yes {
			t.Error("yes")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a question of an ended session waited")
	}
	if st := c.Snapshot(); len(st.Questions) != 0 {
		t.Errorf("the old session's question shows: %+v", st.Questions)
	}
}
