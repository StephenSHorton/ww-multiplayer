package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/report"
)

// fakeEngine is a scripted Engine. Each session logs lines at Info, signals
// started, then either returns fail at once or runs until its ctx is
// cancelled.
type fakeEngine struct {
	lines []string
	fail  error
	live  Live

	started chan started

	mu   sync.Mutex
	sent []string
	ctxs []context.Context
}

type started struct {
	out    Output
	cancel context.CancelFunc
}

func newFake() *fakeEngine { return &fakeEngine{started: make(chan started, 64)} }

func (e *fakeEngine) run(ctx context.Context, cancel context.CancelFunc, out Output) error {
	e.mu.Lock()
	e.ctxs = append(e.ctxs, ctx)
	e.mu.Unlock()
	for _, l := range e.lines {
		out.Log(report.Info, l)
	}
	e.started <- started{out, cancel}
	if e.fail != nil {
		return e.fail
	}
	<-ctx.Done()
	return nil
}

func (e *fakeEngine) Host(ctx context.Context, cancel context.CancelFunc, _ HostOptions, out Output) error {
	return e.run(ctx, cancel, out)
}

func (e *fakeEngine) Join(ctx context.Context, cancel context.CancelFunc, _ JoinOptions, out Output) error {
	return e.run(ctx, cancel, out)
}

func (e *fakeEngine) Live() Live { return e.live }

func (e *fakeEngine) SendChat(text string) error {
	e.mu.Lock()
	if len(e.sent) < 100 { // TestConcurrentUse sends without end
		e.sent = append(e.sent, text)
	}
	e.mu.Unlock()
	return nil
}

func (e *fakeEngine) lastCtx() context.Context {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ctxs[len(e.ctxs)-1]
}

// waitStarted returns the next session's Output and cancel, failing instead
// of hanging.
func waitStarted(t *testing.T, e *fakeEngine) started {
	t.Helper()
	select {
	case s := <-e.started:
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("the engine was never started")
		return started{}
	}
}

// collect reads events until one matches stop (inclusive), failing instead of
// hanging.
func collect(t *testing.T, ch <-chan Event, stop func(Event) bool) []Event {
	t.Helper()
	var out []Event
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("subscription closed after %d events", len(out))
			}
			out = append(out, ev)
			if stop(ev) {
				return out
			}
		case <-timeout:
			t.Fatalf("no stop event after %d events: %+v", len(out), out)
		}
	}
}

// endedState matches the EventState published after a run's EventEnded:
// the last event a run publishes.
func endedState(c *Controller, id ID) func(Event) bool {
	seenEnd := false
	return func(ev Event) bool {
		if ev.Session == id && ev.Kind == EventEnded {
			seenEnd = true
			return false
		}
		return seenEnd && ev.Session == id && ev.Kind == EventState
	}
}

// waitPhase polls Snapshot until it reports p, failing instead of hanging.
func waitPhase(t *testing.T, c *Controller, p Phase) State {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := c.Snapshot()
		if st.Phase == p {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("phase = %v, want %v", st.Phase, p)
		}
		time.Sleep(time.Millisecond)
	}
}

// describe renders events compactly for comparisons.
func describe(evs []Event) string {
	var parts []string
	for _, ev := range evs {
		switch ev.Kind {
		case EventLog:
			parts = append(parts, fmt.Sprintf("log%d:%d:%s", ev.Session, ev.Level, ev.Text))
		case EventChat:
			parts = append(parts, fmt.Sprintf("chat%d:%s:%s", ev.Session, ev.From, ev.Text))
		case EventState:
			parts = append(parts, fmt.Sprintf("state%d", ev.Session))
		case EventEnded:
			parts = append(parts, fmt.Sprintf("ended%d:%s", ev.Session, ev.Text))
		}
	}
	return strings.Join(parts, " ")
}

// TestHost_LifecycleAndLeave: Host goes Idle -> InGame at once; Leave
// cancels the engine's ctx and returns only once the run has ended; the run
// publishes state, its lines, ended (with the neutral reason), state.
func TestHost_LifecycleAndLeave(t *testing.T) {
	e := newFake()
	e.lines = []string{"hosting"}
	c := New(e, Options{})
	if st := c.Snapshot(); st.Phase != Idle || st.Session != 0 {
		t.Fatalf("new controller snapshot = %+v, want Idle with no session", st)
	}
	ch, unsub := c.Subscribe()
	defer unsub()

	id, err := c.Host(context.Background(), HostOptions{Name: "Link"})
	if err != nil || id != 1 {
		t.Fatalf("Host = (%d, %v), want (1, nil)", id, err)
	}
	st := c.Snapshot()
	if st.Session != id || st.Phase != InGame || st.Role != RoleHost || st.Name != "Link" || st.Addr != "" {
		t.Errorf("snapshot after Host = %+v", st)
	}
	waitStarted(t, e)

	c.Leave()
	if e.lastCtx().Err() == nil {
		t.Error("Leave returned but the engine's ctx is not cancelled")
	}
	if st := c.Snapshot(); st.Phase != Ended || st.EndReason != "no error was reported" || st.Session != id {
		t.Errorf("snapshot after Leave = %+v, want Ended with the neutral reason", st)
	}
	got := describe(collect(t, ch, endedState(c, id)))
	if want := "state1 log1:0:hosting ended1:no error was reported state1"; got != want {
		t.Errorf("events = %q, want %q", got, want)
	}
}

// TestJoin_FailureEnds: an engine error ends the run on its own; the error
// is logged at Err and is the end reason, the way the TUI showed a failed
// HostSession.
func TestJoin_FailureEnds(t *testing.T) {
	e := newFake()
	e.fail = errors.New("server start: bind: address already in use")
	c := New(e, Options{})
	ch, unsub := c.Subscribe()
	defer unsub()

	id, err := c.Join(context.Background(), JoinOptions{Addr: "10.0.0.5", Name: "Zelda"})
	if err != nil {
		t.Fatal(err)
	}
	got := describe(collect(t, ch, endedState(c, id)))
	want := "state1 log1:3:server start: bind: address already in use ended1:server start: bind: address already in use state1"
	if got != want {
		t.Errorf("events = %q, want %q", got, want)
	}
	st := waitPhase(t, c, Ended)
	if st.Role != RoleJoin || st.Addr != "10.0.0.5" || st.EndReason != e.fail.Error() {
		t.Errorf("snapshot = %+v", st)
	}
	if e.lastCtx().Err() == nil {
		t.Error("the run's ctx was left uncancelled after it ended")
	}
	c.Leave() // a no-op once ended
}

// TestEndReason_NewestErrLine: the reason is the newest Err-level line, which
// is how runMultiplayerGoroutines reports a pipeline failure, even when the
// engine returns nil.
func TestEndReason_NewestErrLine(t *testing.T) {
	e := newFake()
	c := New(e, Options{})
	if _, err := c.Join(context.Background(), JoinOptions{Name: "Link"}); err != nil {
		t.Fatal(err)
	}
	s := waitStarted(t, e)
	s.out.Log(report.Err, "broadcast-pose: first failure")
	s.out.Log(report.Err, "puppet-sync: dolphin not running")
	s.out.Log(report.Info, "cleanup done")
	c.Leave()
	if got := c.Snapshot().EndReason; got != "puppet-sync: dolphin not running" {
		t.Errorf("EndReason = %q, want the newest Err line", got)
	}
}

// TestEngineCancel_Ends: the engine's own cancel (runPipelines cancelling
// when one pipeline exits) ends the run without Leave, and so does the
// caller's ctx. The second run starts clean: nothing of the first run's
// Join (its Addr, its end reason) carries into the new Host's State.
func TestEngineCancel_Ends(t *testing.T) {
	e := newFake()
	c := New(e, Options{})
	if _, err := c.Join(context.Background(), JoinOptions{Addr: "10.0.0.5", Name: "Zelda"}); err != nil {
		t.Fatal(err)
	}
	s := waitStarted(t, e)
	s.out.Log(report.Err, "puppet-sync: dolphin not running")
	s.cancel()
	if st := waitPhase(t, c, Ended); st.EndReason != "puppet-sync: dolphin not running" {
		t.Fatalf("first run's EndReason = %q", st.EndReason)
	}

	parent, cancel := context.WithCancel(context.Background())
	id, err := c.Host(parent, HostOptions{Name: "Link"})
	if err != nil {
		t.Fatalf("Host after the first run ended: %v", err)
	}
	st := c.Snapshot()
	if id != 2 || st.Session != 2 || st.Phase != InGame || st.Role != RoleHost || st.Name != "Link" || st.Addr != "" || st.EndReason != "" {
		t.Errorf("snapshot after the second Host = %+v (id %d), want a fresh InGame host", st, id)
	}
	waitStarted(t, e)
	cancel()
	waitPhase(t, c, Ended)
}

// TestBusy: one session at a time; after Leave the next run gets a new ID.
func TestBusy(t *testing.T) {
	e := newFake()
	c := New(e, Options{})
	id1, err := c.Host(context.Background(), HostOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Join(context.Background(), JoinOptions{}); !errors.Is(err, ErrBusy) {
		t.Errorf("Join while hosting: err = %v, want ErrBusy", err)
	}
	if _, err := c.Host(context.Background(), HostOptions{}); !errors.Is(err, ErrBusy) {
		t.Errorf("Host while hosting: err = %v, want ErrBusy", err)
	}
	c.Leave()
	id2, err := c.Join(context.Background(), JoinOptions{})
	if err != nil {
		t.Fatalf("Join after Leave: %v", err)
	}
	if id2 == id1 {
		t.Errorf("second run reused ID %d", id1)
	}
	c.Leave()
}

// TestNoEngine: a Controller without an engine refuses to start, and every
// other call is a safe no-op.
func TestNoEngine(t *testing.T) {
	c := New(nil, Options{})
	if _, err := c.Host(context.Background(), HostOptions{}); !errors.Is(err, ErrNoEngine) {
		t.Errorf("Host err = %v, want ErrNoEngine", err)
	}
	if _, err := c.Join(context.Background(), JoinOptions{}); !errors.Is(err, ErrNoEngine) {
		t.Errorf("Join err = %v, want ErrNoEngine", err)
	}
	c.Leave()
	if err := c.SendChat("hi"); !errors.Is(err, ErrNoSession) {
		t.Errorf("SendChat err = %v, want ErrNoSession", err)
	}
	if st := c.Snapshot(); st.Phase != Idle {
		t.Errorf("snapshot = %+v, want Idle", st)
	}
}

// TestChat: SendChat reaches the engine only while a run is live, and the
// run's incoming chat comes out as events tagged with its ID.
func TestChat(t *testing.T) {
	e := newFake()
	c := New(e, Options{})
	if err := c.SendChat("early"); !errors.Is(err, ErrNoSession) {
		t.Errorf("SendChat before a session: err = %v, want ErrNoSession", err)
	}
	ch, unsub := c.Subscribe()
	defer unsub()
	id, _ := c.Join(context.Background(), JoinOptions{Name: "Link"})
	s := waitStarted(t, e)
	if err := c.SendChat("hello"); err != nil {
		t.Fatal(err)
	}
	s.out.Chat("Zelda", "hi there")
	c.Leave()
	if err := c.SendChat("late"); !errors.Is(err, ErrNoSession) {
		t.Errorf("SendChat after Leave: err = %v, want ErrNoSession", err)
	}
	e.mu.Lock()
	sent := strings.Join(e.sent, "|")
	e.mu.Unlock()
	if sent != "hello" {
		t.Errorf("engine got %q, want only the line sent while live", sent)
	}
	got := describe(collect(t, ch, endedState(c, id)))
	if !strings.Contains(got, "chat1:Zelda:hi there") {
		t.Errorf("events %q lack the incoming chat line", got)
	}
}

// TestSnapshot_LiveOnlyWhileRunning: live values come from the engine during
// a run and are zero before and after it, as liveState's reset made them.
func TestSnapshot_LiveOnlyWhileRunning(t *testing.T) {
	e := newFake()
	e.live = Live{PlayerCount: 3, HostAddrs: []string{"10.0.0.2"}, LocalOK: true}
	c := New(e, Options{})
	if c.Snapshot().PlayerCount != 0 {
		t.Error("idle snapshot carries live values")
	}
	c.Host(context.Background(), HostOptions{})
	waitStarted(t, e)
	if st := c.Snapshot(); st.PlayerCount != 3 || len(st.HostAddrs) != 1 || !st.LocalOK {
		t.Errorf("running snapshot = %+v, want the engine's live values", st)
	}
	c.Leave()
	if st := c.Snapshot(); st.PlayerCount != 0 || st.HostAddrs != nil || st.LocalOK {
		t.Errorf("ended snapshot = %+v, want zero live values", st)
	}
}

// TestSubscribe_DropsOldest: a reader that falls behind loses the oldest
// events, never the newest, and never stalls the session.
func TestSubscribe_DropsOldest(t *testing.T) {
	e := newFake()
	for i := 0; i < 10; i++ {
		e.lines = append(e.lines, fmt.Sprint(i))
	}
	c := New(e, Options{Buffer: 4})
	ch, unsub := c.Subscribe()
	defer unsub()
	c.Host(context.Background(), HostOptions{})
	waitStarted(t, e) // all ten lines logged with nobody reading
	var got []string
	for i := 0; i < 4; i++ {
		got = append(got, (<-ch).Text)
	}
	if strings.Join(got, ",") != "6,7,8,9" {
		t.Errorf("buffered events = %v, want the newest four lines", got)
	}
	c.Leave()
}

// TestSubscribe_EndSurvivesFullBuffer: a run's last events (ended, then
// state) always get in, however far behind the reader is.
func TestSubscribe_EndSurvivesFullBuffer(t *testing.T) {
	e := newFake()
	for i := 0; i < 50; i++ {
		e.lines = append(e.lines, fmt.Sprint(i))
	}
	e.fail = errors.New("boom")
	c := New(e, Options{Buffer: 2})
	ch, unsub := c.Subscribe()
	defer unsub()
	id, _ := c.Join(context.Background(), JoinOptions{})
	waitPhase(t, c, Ended)
	got := describe(collect(t, ch, endedState(c, id)))
	if got != "ended1:boom state1" {
		t.Errorf("events = %q, want only the run's last two", got)
	}
}

// TestSubscribe_BufferFloor: Options.Buffer 1 is raised to MinBuffer, so
// even then EventEnded is not pushed out by the EventState after it.
func TestSubscribe_BufferFloor(t *testing.T) {
	e := newFake()
	e.lines = []string{"a", "b", "c"}
	e.fail = errors.New("boom")
	c := New(e, Options{Buffer: 1})
	ch, unsub := c.Subscribe()
	defer unsub()
	if n := cap(ch); n != MinBuffer {
		t.Errorf("buffer = %d, want MinBuffer (%d)", n, MinBuffer)
	}
	id, _ := c.Host(context.Background(), HostOptions{})
	waitPhase(t, c, Ended)
	if got := describe(collect(t, ch, endedState(c, id))); got != "ended1:boom state1" {
		t.Errorf("events = %q, want the run's last two", got)
	}
}

// TestSubscribe_FanOutAndUnsubscribe: every subscriber gets every event;
// unsubscribe closes the channel, is idempotent, and later events skip it.
func TestSubscribe_FanOutAndUnsubscribe(t *testing.T) {
	e := newFake()
	e.lines = []string{"x"}
	c := New(e, Options{})
	a, unsubA := c.Subscribe()
	b, unsubB := c.Subscribe()
	defer unsubB()

	id, _ := c.Host(context.Background(), HostOptions{})
	waitStarted(t, e)
	unsubA()
	unsubA()
	c.Leave()

	gotB := describe(collect(t, b, endedState(c, id)))
	if gotB != "state1 log1:0:x ended1:no error was reported state1" {
		t.Errorf("subscriber b got %q", gotB)
	}
	var gotA []Event
	for ev := range a { // closed by unsubA, so this ends
		gotA = append(gotA, ev)
	}
	if describe(gotA) != "state1 log1:0:x" {
		t.Errorf("subscriber a got %q, want only the events before it left", describe(gotA))
	}
}

// TestOptionsLog: Options.Log gets every session line too (report.Multi),
// and the subscribers still do.
func TestOptionsLog(t *testing.T) {
	e := newFake()
	e.lines = []string{"one", "two"}
	var tee recorder
	c := New(e, Options{Log: &tee})
	c.Host(context.Background(), HostOptions{})
	waitStarted(t, e)
	c.Leave()
	if got := tee.String(); got != "0:one 0:two" {
		t.Errorf("Options.Log got %q", got)
	}
}

type recorder struct {
	mu    sync.Mutex
	lines []string
}

func (r *recorder) Log(level report.Level, msg string) {
	r.mu.Lock()
	r.lines = append(r.lines, fmt.Sprintf("%d:%s", level, msg))
	r.mu.Unlock()
}

func (r *recorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.lines, " ")
}

// TestConcurrentUse: commands, snapshots, subscriptions and session output
// from many goroutines at once. Meant for `go test -race` (the Linux CI
// job): it checks for no race, deadlock or panic (such as a send on a
// closed subscription), not for particular results.
func TestConcurrentUse(t *testing.T) {
	e := newFake()
	e.lines = []string{"up"}
	e.live = Live{PlayerCount: 2}
	c := New(e, Options{Buffer: 8})

	stop := make(chan struct{})
	var wg sync.WaitGroup
	loop := func(f func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					f()
				}
			}
		}()
	}
	loop(func() { c.Snapshot() })
	loop(func() { c.SendChat("hi") })
	loop(func() {
		ch, unsub := c.Subscribe()
		select {
		case <-ch:
		default:
		}
		unsub()
	})
	// A slow reader that never keeps up.
	slow, unsubSlow := c.Subscribe()
	loop(func() {
		select {
		case <-slow:
		case <-time.After(time.Millisecond):
		}
	})
	// Session output from outside the engine goroutine, as the network
	// client's read loop and the pipelines do.
	outs := make(chan Output, 64)
	loop(func() {
		select {
		case o := <-outs:
			o.Log(report.Net, "late line")
			o.Chat("Zelda", "late chat")
		default:
		}
	})

	for i := 0; i < 50; i++ {
		var err error
		if i%2 == 0 {
			_, err = c.Host(context.Background(), HostOptions{})
		} else {
			_, err = c.Join(context.Background(), JoinOptions{})
		}
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		s := waitStarted(t, e)
		select {
		case outs <- s.out:
		default:
		}
		if i%3 == 0 {
			s.cancel() // the engine ends it
			waitPhase(t, c, Ended)
		}
		var lw sync.WaitGroup
		for j := 0; j < 3; j++ { // concurrent Leaves all return
			lw.Add(1)
			go func() { defer lw.Done(); c.Leave() }()
		}
		lw.Wait()
		if st := c.Snapshot(); st.Phase != Ended {
			t.Fatalf("run %d: phase after Leave = %v", i, st.Phase)
		}
	}
	close(stop)
	wg.Wait()
	unsubSlow()
}

// TestSessionLog: each session gets its own SessionLog reporter, opened
// with the session's starting state, with only that session's lines
// (its end reason included), and closed before Wait returns. A nil
// reporter is skipped.
func TestSessionLog(t *testing.T) {
	e := newFake()
	e.lines = []string{"one"}
	type opened struct {
		st     State
		rec    *recorder
		closed bool
	}
	var mu sync.Mutex
	var logs []*opened
	c := New(e, Options{SessionLog: func(st State) (report.Reporter, func()) {
		o := &opened{st: st, rec: &recorder{}}
		mu.Lock()
		logs = append(logs, o)
		mu.Unlock()
		return o.rec, func() { mu.Lock(); o.closed = true; mu.Unlock() }
	}})
	id1, _ := c.Host(context.Background(), HostOptions{Name: "Ann"})
	waitStarted(t, e)
	c.Leave()
	c.Wait(id1)
	e.fail = errors.New("no route")
	id2, _ := c.Join(context.Background(), JoinOptions{Name: "Bea", Addr: "10.0.0.2"})
	waitStarted(t, e)
	c.Wait(id2)
	mu.Lock()
	defer mu.Unlock()
	if len(logs) != 2 {
		t.Fatalf("%d logs opened", len(logs))
	}
	if st := logs[0].st; st.Session != id1 || st.Role != RoleHost || st.Name != "Ann" || st.Phase != InGame {
		t.Errorf("first log opened with %+v", st)
	}
	if st := logs[1].st; st.Session != id2 || st.Role != RoleJoin || st.Addr != "10.0.0.2" {
		t.Errorf("second log opened with %+v", st)
	}
	if got := logs[0].rec.String(); got != "0:one" || !logs[0].closed {
		t.Errorf("first log %q, closed %v", got, logs[0].closed)
	}
	if got := logs[1].rec.String(); got != "0:one 3:no route" || !logs[1].closed {
		t.Errorf("second log %q, closed %v", got, logs[1].closed)
	}

	// A log that can't be opened: the session runs without one.
	c2 := New(e, Options{SessionLog: func(State) (report.Reporter, func()) { return nil, func() { t.Error("closed a nil log") } }})
	e.fail = nil
	id3, err := c2.Host(context.Background(), HostOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitStarted(t, e)
	c2.Leave()
	if st := c2.Wait(id3); st.Phase != Ended {
		t.Errorf("phase %v", st.Phase)
	}
}
