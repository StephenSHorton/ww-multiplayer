package session

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/report"
)

// DefaultBuffer is a subscription's event buffer when Options.Buffer is 0.
// It matches the TUI's old log channel.
const DefaultBuffer = 256

// MinBuffer is the smallest event buffer New allows: a run's last two
// events (EventEnded, then EventState) must both fit, or the second would
// push the end reason out of a full buffer.
const MinBuffer = 2

// Options configures a Controller.
type Options struct {
	// Log, when non-nil, also receives every session log line, besides
	// the subscribers (a log file, for one). It is called on the session's
	// goroutines and must not block.
	Log report.Reporter
	// Buffer is each subscription's event buffer; 0 means DefaultBuffer,
	// and a smaller positive value is raised to MinBuffer.
	Buffer int
}

// Controller owns at most one running session at a time and is the only
// thing a UI talks to. It is safe for concurrent use. One Controller lives
// for the whole app, across Host -> Leave -> Join, like liveState did.
type Controller struct {
	engine Engine
	opts   Options

	mu    sync.Mutex // guards next, cur, state, nextQ, pending; taken before subMu
	next  ID
	cur   *run  // the running session; nil when none
	state State // the controller-owned fields of the latest session
	// pending are the open questions' answer channels, by question ID.
	nextQ   uint64
	pending map[string]chan bool

	subMu sync.Mutex
	subs  map[*subscription]struct{}
}

// run is one Host or Join call in flight.
type run struct {
	id     ID
	cancel context.CancelFunc
	done   chan struct{} // closed once the engine returned and state says Ended
}

// New returns a Controller that runs sessions on e. A nil e gives a
// Controller whose Host and Join return ErrNoEngine.
func New(e Engine, o Options) *Controller {
	if o.Buffer <= 0 {
		o.Buffer = DefaultBuffer
	} else if o.Buffer < MinBuffer {
		o.Buffer = MinBuffer
	}
	return &Controller{engine: e, opts: o, subs: map[*subscription]struct{}{}, pending: map[string]chan bool{}}
}

// Host starts a host session under ctx and returns its ID at once; the
// session runs until Leave, ctx's cancel, or a failure. It returns ErrBusy
// while another session runs.
func (c *Controller) Host(ctx context.Context, o HostOptions) (ID, error) {
	return c.start(ctx, State{Role: RoleHost, Name: o.Name, Coop: o.Coop}, func(ctx context.Context, cancel context.CancelFunc, out Output) error {
		return c.engine.Host(ctx, cancel, o, out)
	})
}

// Join starts a join session under ctx; see Host.
func (c *Controller) Join(ctx context.Context, o JoinOptions) (ID, error) {
	return c.start(ctx, State{Role: RoleJoin, Name: o.Name, Addr: o.Addr, Coop: o.Coop}, func(ctx context.Context, cancel context.CancelFunc, out Output) error {
		return c.engine.Join(ctx, cancel, o, out)
	})
}

func (c *Controller) start(parent context.Context, st State, body func(context.Context, context.CancelFunc, Output) error) (ID, error) {
	if c.engine == nil {
		return 0, ErrNoEngine
	}
	c.mu.Lock()
	if c.cur != nil {
		c.mu.Unlock()
		return 0, ErrBusy
	}
	c.next++
	ctx, cancel := context.WithCancel(parent)
	r := &run{id: c.next, cancel: cancel, done: make(chan struct{})}
	c.cur = r
	st.Session, st.Phase = r.id, InGame
	c.state = st
	// State events are published under mu, so they are queued in the same
	// order as the changes, and anyone who saw a change in Snapshot can
	// rely on its event being queued already.
	c.publish(Event{Kind: EventState, Session: r.id})
	c.mu.Unlock()

	out := newOutput(c, r.id)
	go func() {
		err := body(ctx, cancel, out)
		if err != nil && !errors.Is(err, ErrReported) {
			out.Log(report.Err, err.Error())
		}
		cancel()
		reason := out.endReason()
		c.mu.Lock()
		c.cur = nil
		c.state.Phase, c.state.EndReason, c.state.Failed = Ended, reason, err != nil
		c.publish(Event{Kind: EventEnded, Session: r.id, Text: reason})
		c.publish(Event{Kind: EventState, Session: r.id})
		c.mu.Unlock()
		close(r.done)
	}()
	return r.id, nil
}

// Leave stops the running session and waits for its teardown (pipelines
// stopped, server unbound, mailbox reset) to finish, so a following Host can
// bind the port again. A no-op when no session runs.
func (c *Controller) Leave() {
	c.mu.Lock()
	r := c.cur
	c.mu.Unlock()
	if r == nil {
		return
	}
	r.cancel()
	<-r.done
}

// Wait waits until session id has ended and returns the state it ended
// in. For a session that is not running (it ended already, or id is not
// the current one) it returns at once.
func (c *Controller) Wait(id ID) State {
	c.mu.Lock()
	r := c.cur
	c.mu.Unlock()
	if r != nil && r.id == id {
		<-r.done
	}
	return c.Snapshot()
}

// SendChat sends one chat line on the running session.
func (c *Controller) SendChat(text string) error {
	c.mu.Lock()
	running := c.cur != nil
	c.mu.Unlock()
	if !running {
		return ErrNoSession
	}
	return c.engine.SendChat(text)
}

// Snapshot returns the current session's state. The live values come from
// the engine on each call; they are zero unless the session is in game.
func (c *Controller) Snapshot() State {
	c.mu.Lock()
	st := c.state
	st.Questions = append([]Question(nil), st.Questions...)
	running := c.cur != nil
	c.mu.Unlock()
	if running {
		st.Live = c.engine.Live()
	}
	return st
}

// output is the Output a run writes to: it publishes log and chat events
// tagged with the run's ID, forwards log lines to Options.Log, and keeps the
// newest Err-level line as the session's end reason.
type output struct {
	c   *Controller
	id  ID
	rep report.Reporter

	// mu guards lastErr: the session's goroutines log concurrently.
	mu      sync.Mutex
	lastErr string
}

func newOutput(c *Controller, id ID) *output {
	o := &output{c: c, id: id}
	o.rep = report.Multi(publisher{o}, c.opts.Log)
	return o
}

func (o *output) Log(level report.Level, msg string) {
	if level == report.Err {
		o.mu.Lock()
		o.lastErr = msg
		o.mu.Unlock()
	}
	o.rep.Log(level, msg)
}

func (o *output) Chat(from, text string) {
	o.c.publish(Event{Kind: EventChat, Session: o.id, From: from, Text: text})
}

func (o *output) Ask(ctx context.Context, q Question) bool { return o.c.ask(ctx, o.id, q) }

func (o *output) Advise(a Advice) {
	o.c.publish(Event{Kind: EventAdvice, Session: o.id, Advice: a})
}

// endReason explains why the session ended: the newest Err-level line it
// logged (the engine's returned error is logged at Err too), or a neutral
// fallback when it ended without one.
func (o *output) endReason() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.lastErr != "" {
		return o.lastErr
	}
	return "no error was reported"
}

// publisher is the Reporter half of an output that turns lines into events.
type publisher struct{ o *output }

func (p publisher) Log(level report.Level, msg string) {
	p.o.c.publish(Event{Kind: EventLog, Session: p.o.id, Level: level, Text: msg})
}

// publish stamps ev and hands it to every subscriber without blocking.
func (c *Controller) publish(ev Event) {
	ev.Time = time.Now()
	c.subMu.Lock()
	defer c.subMu.Unlock()
	for s := range c.subs {
		s.send(ev)
	}
}
