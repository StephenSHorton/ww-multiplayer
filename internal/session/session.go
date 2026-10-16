// Package session is the UI-free owner of a multiplayer session. The TUI
// (and later the GUI) drive it the same way: commands in (Host, Join, Leave,
// SendChat), state snapshots and events out (Snapshot, Subscribe).
//
// The session work itself (server, broadcast-pose, puppet-sync, co-op) stays
// in package main and is injected as an Engine, the way tui.Hooks used to
// inject it. That keeps this package free of internal/dolphin and
// internal/network, so it builds and tests on any OS with a fake engine.
package session

import (
	"context"
	"errors"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/report"
)

// ID names one Host or Join run. IDs start at 1 and are never reused by a
// Controller, so an event or a UI message can be matched to the run it came
// from after the user has left it and started another. 0 means no session.
type ID uint64

// Role is the local player's part in a session. The values are the strings
// the TUI has always shown.
type Role string

const (
	RoleHost Role = "Host"
	RoleJoin Role = "Join"
)

// Phase is where a session is in its life.
//
// The direct host/join path has only Idle -> InGame -> Ended: its pipelines
// start at once and wait for Dolphin themselves. The lobby (Connecting,
// Lobby, Starting) adds phases between Idle and InGame when it lands.
type Phase int

const (
	Idle   Phase = iota // no session has run yet
	InGame              // the session's pipelines are running
	Ended               // the session returned; State.EndReason says why
)

func (p Phase) String() string {
	switch p {
	case Idle:
		return "idle"
	case InGame:
		return "in game"
	case Ended:
		return "ended"
	}
	return "unknown"
}

// Player is one other human in the running session, network-free. X/Y/Z are
// the remote's world coords, all 0 while unknown.
type Player struct {
	ID      byte
	Name    string
	X, Y, Z float32
}

// Live is the engine's view of the running session: the values the status
// panel and minimap poll. Every field is its zero value when unknown or when
// no session is live.
type Live struct {
	Players     []Player      // other humans, one entry per person
	PlayerCount int           // humans in the session including self; 0 when unknown
	HostAddrs   []string      // host role: addresses to share; nil otherwise
	Latency     time.Duration // client<->server RTT; 0 until measured

	// LocalX/Y/Z is this player's own world position; LocalOK is false
	// until the first broadcast-pose tick has read it.
	LocalX, LocalY, LocalZ float32
	LocalOK                bool
}

// State is a snapshot of the current (or most recent) session. It is a
// copy: callers may keep and read it without locking.
type State struct {
	Session   ID
	Phase     Phase
	Role      Role
	Name      string // the local player's name
	Addr      string // join role: the address as typed; "" for host
	EndReason string // set once Phase is Ended
	Live             // zero unless Phase is InGame
}

// HostOptions configures Controller.Host.
type HostOptions struct {
	Name string
}

// JoinOptions configures Controller.Join.
type JoinOptions struct {
	Addr string
	Name string
}

// Output is what a session writes to while it runs: its log lines, and the
// chat lines it receives. The Controller gives every run its own Output, so
// lines are tagged with the run they came from.
type Output interface {
	report.Reporter
	// Chat delivers one incoming chat line. It must not block: it is
	// called from the network client's read loop.
	Chat(from, text string)
}

// Engine runs the session work. package main implements it over
// runHostSession / runJoinSession and its liveState status bridge.
type Engine interface {
	// Host runs the host flow until ctx is cancelled or a pipeline fails,
	// and returns the error to report as the session's end (nil when it
	// was already reported, or on a normal cancel). cancel cancels ctx;
	// the engine calls it to stop its other goroutines when one fails.
	Host(ctx context.Context, cancel context.CancelFunc, o HostOptions, out Output) error
	// Join runs the join flow, with the same contract as Host.
	Join(ctx context.Context, cancel context.CancelFunc, o JoinOptions, out Output) error
	// Live returns the running session's status values. It is polled
	// several times a second, so it must be cheap and safe to call from
	// any goroutine, including while no session runs.
	Live() Live
	// SendChat sends one chat line to the running session. It may return
	// nil without sending when the session has not connected yet.
	SendChat(text string) error
}

var (
	// ErrBusy is returned by Host and Join while a session is running.
	ErrBusy = errors.New("a session is already running")
	// ErrNoSession is returned by SendChat when no session is running.
	ErrNoSession = errors.New("no session is running")
	// ErrNoEngine is returned by Host and Join on a Controller built
	// without an Engine.
	ErrNoEngine = errors.New("sessions aren't available in this build (no engine)")
)
