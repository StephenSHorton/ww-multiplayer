// Package session is the UI-free owner of a multiplayer session. The CLI's
// host and join, the TUI (and later the GUI) drive it the same way:
// commands in (Host, Join, Leave, SendChat, and for co-op Answer, Command,
// CommandLine), state snapshots and events out (Snapshot, Subscribe,
// Wait).
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

	"github.com/StephenSHorton/ww-multiplayer/internal/playercolor"
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
// start at once and wait for Dolphin themselves. A lobby session (HostLobby,
// JoinLobby) goes Idle -> Connecting -> InLobby -> Starting -> InGame ->
// Ended, following the host's lobby; a join that falls back to the direct
// path (a pre-lobby host) goes from Connecting to InGame. The values of the
// first three are those they always had.
type Phase int

const (
	Idle       Phase = iota // no session has run yet
	InGame                  // the session's pipelines are running (or the lobby is in game)
	Ended                   // the session returned; State.EndReason says why
	Connecting              // a lobby session is reaching the host's lobby
	InLobby                 // in the lobby: colors, ready, co-op settings, Start
	Starting                // the host pressed Start; waiting for its GO
)

func (p Phase) String() string {
	switch p {
	case Idle:
		return "idle"
	case InGame:
		return "in game"
	case Ended:
		return "ended"
	case Connecting:
		return "connecting"
	case InLobby:
		return "lobby"
	case Starting:
		return "starting"
	}
	return "unknown"
}

// Player is one other human in the running session, network-free. X/Y/Z are
// the remote's world coords, all 0 while unknown.
type Player struct {
	ID      byte
	Name    string
	X, Y, Z float32
	// Color is the player's tunic color (docs/09-player-colors.md), set
	// when HasColor is. A UI shows it as a swatch by the name.
	Color    playercolor.Color
	HasColor bool
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

	// CoopStatus is each co-op pipeline's status, as /status reports it; nil
	// without co-op.
	CoopStatus []CoopStatus
	// CoopHub is the host's hub; nil on a joiner and without co-op.
	CoopHub *CoopHubStatus
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
	// Failed is set once Phase is Ended when the session ended on an
	// error (the CLI exits 1 for it), not by Leave or a cancel.
	Failed bool
	// Err is the error the session ended on, once Phase is Ended and
	// Failed, for a UI that tells the causes apart (a version mismatch,
	// a taken name, a kick: errors.As on the network package's
	// RejectError and LobbyNoticeError); nil otherwise.
	Err error
	// Coop is the session's co-op choices; Coop.On says the co-op
	// commands and questions apply.
	Coop CoopOptions
	// Questions are the session's open co-op questions, oldest first.
	Questions []Question
	// AutoYes answers every co-op question yes at once (SetAutoYes): the
	// app's per-session "Answer yes to co-op questions", never saved.
	AutoYes bool
	// Advice is the session's co-op advice not dismissed yet, oldest
	// first, the newest of each kind per player.
	Advice []Advice
	// Requests are the joiners' requests to the host (merge) not
	// answered yet, oldest first; host only.
	Requests []Request
	// CoopBackup is the folder this session's memory-card backup went
	// to ("" before co-op started, or without a backup).
	CoopBackup string
	// Founding is the host's founding of a new world from its loaded
	// file (a lobby session's New world); nil otherwise.
	Founding *Founding
	// Lobby is the lobby of a HostLobby or JoinLobby session (its
	// members, colors, phase); nil for a direct Host or Join.
	Lobby *Lobby
	// Dolphin is this player's Dolphin once a lobby session's host
	// pressed Start (launch.go); its State is "" before.
	Dolphin DolphinInfo
	Live    // zero unless Phase is InGame (a lobby session: Starting too)
}

// HostOptions configures Controller.Host.
type HostOptions struct {
	Name string
	// Color is the tunic color to ask for: the lobby's (HostLobby), or
	// the server's for the player connections (Host), where it overrides
	// the process-wide --color / WW_COLOR. Nil is no preference:
	// --color's, else classic green. Players may share a color.
	Color *playercolor.Color
	// Port is the port the server listens on; 0 is the default (25565).
	Port int
	// ListenHost is the address a lobby host's server listens on
	// (HostLobby only); "" is every interface, which LAN and VPN
	// friends need. Tests and local checks set "127.0.0.1".
	ListenHost string
	Coop       CoopOptions
	// Launch is how the lobby's Start gets this player's Dolphin going
	// (HostLobby, JoinLobby). LaunchFn, when set, is asked instead when
	// Start comes, so a setting changed in the lobby ("I'll start
	// Dolphin myself") counts.
	Launch   LaunchOptions
	LaunchFn func() LaunchOptions
	// CoopFn, when set, is this player's own co-op choices in a lobby
	// session (the disc for the co-op tables, a joiner's own dry run),
	// read when co-op starts; the host's lobby settings say the rest.
	CoopFn func() CoopOptions
}

// LaunchOptions returns o's launch options as Start finds them.
func (o HostOptions) LaunchOptions() LaunchOptions { return launchOf(o.Launch, o.LaunchFn) }

// LaunchOptions returns o's launch options as Start finds them.
func (o JoinOptions) LaunchOptions() LaunchOptions { return launchOf(o.Launch, o.LaunchFn) }

func launchOf(o LaunchOptions, f func() LaunchOptions) LaunchOptions {
	if f != nil {
		return f()
	}
	return o
}

// JoinOptions configures Controller.Join.
type JoinOptions struct {
	Addr string
	Name string
	// Color is the tunic color to ask for (JoinLobby, Join, and
	// JoinLobby's direct join of a pre-lobby host); see HostOptions.Color.
	Color *playercolor.Color
	Coop  CoopOptions
	// Launch and LaunchFn: see HostOptions.Launch.
	Launch   LaunchOptions
	LaunchFn func() LaunchOptions
	// CoopFn: see HostOptions.CoopFn.
	CoopFn func() CoopOptions
}

// Output is what a session writes to while it runs: its log lines, and the
// chat lines it receives. The Controller gives every run its own Output, so
// lines are tagged with the run they came from.
type Output interface {
	report.Reporter
	// Chat delivers one incoming chat line. It must not block: it is
	// called from the network client's read loop.
	Chat(from, text string)
	// Ask puts a co-op question to the UI (EventQuestion) and waits for
	// Controller.Answer; ctx ending, the session's, is a no.
	Ask(ctx context.Context, q Question) bool
	// Advise passes co-op advice to the UI (EventAdvice). It never
	// blocks.
	Advise(a Advice)
}

// Engine runs the session work. package main implements it over
// runHostSession / runJoinSession and its liveState status bridge.
type Engine interface {
	// Host runs the host flow until ctx is cancelled or a pipeline fails,
	// and returns the error to report as the session's end: nil on a
	// normal cancel, and one wrapping ErrReported when the session
	// already logged it. cancel cancels ctx;
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
