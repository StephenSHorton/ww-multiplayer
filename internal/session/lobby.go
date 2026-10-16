package session

// The pre-game lobby through the controller (docs/10-lobby.md; App PR 4).
// HostLobby and JoinLobby run a session that starts in the lobby: the
// engine holds the lobby connection (and, for a host, the server) and
// reports the lobby's state; the UI changes it with SetColor, SetReady,
// SetCoopSettings, Start, Go, Kick and RequestMerge. A pre-lobby host is
// joined directly instead, automatically (EventLegacyHost).
//
// What Start launches (Dolphin, the pose pipelines, co-op) is a later PR;
// this one carries the protocol, the state and the commands.

import (
	"context"
	"errors"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/playercolor"
	"github.com/StephenSHorton/ww-multiplayer/internal/report"
)

// LobbyPhase is the lobby's phase, the server's.
type LobbyPhase string

const (
	LobbyPhaseLobby    LobbyPhase = "lobby"    // members gather, pick colors, get ready
	LobbyPhaseStarting LobbyPhase = "starting" // the host pressed Start; apps launch
	LobbyPhaseInGame   LobbyPhase = "in_game"  // the host sent GO
)

// LobbyWorld is one of the host's co-op worlds, as the lobby lists it.
type LobbyWorld struct {
	ID    string
	Seq   uint64
	InUse bool
}

// LobbySettings are the host's co-op settings, as the lobby carries them.
// Coop is "off", "new" or a world ID.
type LobbySettings struct {
	Coop         string
	DryRun       bool
	Merge        bool
	LiveSwitches bool
	Worlds       []LobbyWorld
}

// Member Via values.
const (
	ViaApp = "app" // in the lobby with the app
	ViaCLI = "cli" // a CLI player (join), listed from its player connections
)

// LobbyMember is one row of the lobby.
type LobbyMember struct {
	ID      int
	Name    string
	Color   playercolor.Color
	Ready   bool
	Host    bool
	Dolphin string // "none", "launching", "title", "in_game" or ""
	File    string // the member's save-file hint
	RTT     time.Duration
	Via     string
	// Away: the member's lobby connection dropped; the host holds its
	// slot for a while.
	Away bool
}

// Lobby is the lobby as this player sees it: State.Lobby.
type Lobby struct {
	// Member is this player's member ID; 0 until the host welcomed us.
	Member int
	// Host says this player is the lobby's host.
	Host     bool
	Phase    LobbyPhase
	Rev      uint64
	Settings LobbySettings
	Members  []LobbyMember
	// Legacy says the host runs a build without a lobby: the session
	// joined directly (the CLI's join) and the lobby commands don't
	// apply.
	Legacy bool
}

// Self returns this player's row.
func (l Lobby) Self() (LobbyMember, bool) {
	for _, m := range l.Members {
		if m.ID == l.Member && m.Via == ViaApp {
			return m, true
		}
	}
	return LobbyMember{}, false
}

func (l Lobby) clone() *Lobby {
	l.Members = append([]LobbyMember(nil), l.Members...)
	l.Settings.Worlds = append([]LobbyWorld(nil), l.Settings.Worlds...)
	return &l
}

// Notice is a lobby notice or refusal from the host (EventLobbyNotice):
// Code is the wire's ("name_taken", "colors_locked", "kicked", ...).
type Notice struct {
	Code, Text string
	// Suggest is a free name, for "name_taken".
	Suggest string
}

// LobbyCommandKind names a lobby command.
type LobbyCommandKind string

const (
	LobbySetColor     LobbyCommandKind = "color"
	LobbySetReady     LobbyCommandKind = "ready"
	LobbySetDolphin   LobbyCommandKind = "dolphin"
	LobbySetFile      LobbyCommandKind = "file"
	LobbySetSettings  LobbyCommandKind = "settings" // host
	LobbyStart        LobbyCommandKind = "start"    // host
	LobbyGo           LobbyCommandKind = "go"       // host
	LobbyKick         LobbyCommandKind = "kick"     // host
	LobbyRequestMerge LobbyCommandKind = "request-merge"
)

// LobbyCommand is one lobby command for the engine; Kind says which field
// applies.
type LobbyCommand struct {
	Kind     LobbyCommandKind
	Color    playercolor.Color
	Ready    bool
	Text     string // Dolphin state, file hint, or GO's world
	Settings LobbySettings
	Member   int
}

// LobbyOutput is the Output of a lobby session: what the engine reports
// about the lobby, besides log lines and chat.
type LobbyOutput interface {
	Output
	// LobbyState passes the lobby's latest state (after every change).
	LobbyState(l Lobby)
	// LobbyNotice, LobbyStart, LobbyGo and LobbyRequest pass the host's
	// messages of those names.
	LobbyNotice(n Notice)
	LobbyStart(s LobbySettings)
	LobbyGo(world string)
	LobbyRequest(kind, from string)
}

// LobbyEngine is what an Engine with the lobby adds. The Controller finds
// it by a type assertion, as CoopEngine.
type LobbyEngine interface {
	// HostLobby starts the server and joins its lobby, and runs until
	// ctx ends or it fails, with Engine.Host's contract.
	HostLobby(ctx context.Context, cancel context.CancelFunc, o HostOptions, out LobbyOutput) error
	// JoinLobby joins the lobby at o.Addr, with Engine.Join's contract.
	// A host without a lobby makes it return an error wrapping
	// ErrLegacyHost at once; the Controller then joins directly.
	JoinLobby(ctx context.Context, cancel context.CancelFunc, o JoinOptions, out LobbyOutput) error
	// Lobby sends one command to the lobby. It fails when the lobby
	// connection is down.
	Lobby(cmd LobbyCommand) error
}

var (
	// ErrLegacyHost wraps JoinLobby's error for a host whose build has
	// no lobby (it refused the lobby connection role).
	ErrLegacyHost = errors.New("the host runs an older build without a lobby")
	// ErrNoLobby is returned by the lobby commands outside a lobby
	// session (none runs, it joined directly, or the engine has no lobby).
	ErrNoLobby = errors.New("not in a lobby")
	// ErrNotHost is returned by the host's commands for a joiner.
	ErrNotHost = errors.New("only the host can do that")
	// ErrWrongPhase is returned by Start outside the lobby phase and by
	// Go outside the starting phase.
	ErrWrongPhase = errors.New("not at this point of the session")
	// ErrColorsLocked is returned by SetColor once the session has
	// started (colors are locked until the in-game recolor can change).
	ErrColorsLocked = errors.New("colors are locked once the session has started")
)

// legacyHostLine is logged when a lobby join falls back to a direct one.
const legacyHostLine = "This host runs an older ww-multiplayer without a lobby; joining directly."

// HostLobby starts a host session that begins in the lobby. It returns
// ErrNoLobby when the engine has no lobby, ErrBusy while a session runs.
func (c *Controller) HostLobby(ctx context.Context, o HostOptions) (ID, error) {
	le, ok := c.engine.(LobbyEngine)
	if !ok {
		return 0, ErrNoLobby
	}
	st := State{Role: RoleHost, Name: o.Name, Coop: o.Coop, Phase: Connecting, Lobby: &Lobby{Phase: LobbyPhaseLobby}}
	return c.start(ctx, st, func(ctx context.Context, cancel context.CancelFunc, out Output) error {
		return le.HostLobby(ctx, cancel, o, out.(*output))
	})
}

// JoinLobby starts a join session that begins in the host's lobby. A host
// without a lobby is joined directly, as Join does: EventLegacyHost, then
// the session runs in game with State.Lobby.Legacy set.
func (c *Controller) JoinLobby(ctx context.Context, o JoinOptions) (ID, error) {
	le, ok := c.engine.(LobbyEngine)
	if !ok {
		return 0, ErrNoLobby
	}
	st := State{Role: RoleJoin, Name: o.Name, Addr: o.Addr, Coop: o.Coop, Phase: Connecting, Lobby: &Lobby{Phase: LobbyPhaseLobby}}
	return c.start(ctx, st, func(ctx context.Context, cancel context.CancelFunc, out Output) error {
		o2 := out.(*output)
		err := le.JoinLobby(ctx, cancel, o, o2)
		if !errors.Is(err, ErrLegacyHost) || ctx.Err() != nil {
			return err
		}
		c.legacy(o2.id)
		o2.Log(report.Info, legacyHostLine)
		return c.engine.Join(ctx, cancel, o, out)
	})
}

// legacy records run id's fallback to a direct join.
func (c *Controller) legacy(id ID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.Session != id || c.state.Lobby == nil {
		return
	}
	l := c.state.Lobby.clone()
	l.Legacy = true
	c.state.Lobby, c.state.Phase = l, InGame
	c.publish(Event{Kind: EventLegacyHost, Session: id})
	c.publish(Event{Kind: EventState, Session: id})
}

// lobbyState records run id's lobby state and the phase it implies.
func (c *Controller) lobbyState(id ID, l Lobby) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.Session != id || c.cur == nil || c.cur.id != id {
		return
	}
	c.state.Lobby = l.clone()
	switch l.Phase {
	case LobbyPhaseStarting:
		c.state.Phase = Starting
	case LobbyPhaseInGame:
		c.state.Phase = InGame
	default:
		c.state.Phase = InLobby
	}
	c.publish(Event{Kind: EventState, Session: id})
}

func (o *output) LobbyState(l Lobby) { o.c.lobbyState(o.id, l) }

func (o *output) LobbyNotice(n Notice) {
	o.c.publish(Event{Kind: EventLobbyNotice, Session: o.id, Notice: n, Text: n.Text})
}

func (o *output) LobbyStart(s LobbySettings) {
	o.c.publish(Event{Kind: EventLobbyStart, Session: o.id, Settings: s})
}

func (o *output) LobbyGo(world string) {
	o.c.publish(Event{Kind: EventLobbyGo, Session: o.id, Text: world})
}

func (o *output) LobbyRequest(kind, from string) {
	o.c.publish(Event{Kind: EventLobbyRequest, Session: o.id, From: from, Text: kind})
}

// lobbyEngine returns the engine's lobby side and the running lobby,
// or ErrNoLobby.
func (c *Controller) lobbyEngine() (LobbyEngine, Lobby, error) {
	c.mu.Lock()
	running, l := c.cur != nil, c.state.Lobby
	c.mu.Unlock()
	le, ok := c.engine.(LobbyEngine)
	if !running || !ok || l == nil || l.Legacy {
		return nil, Lobby{}, ErrNoLobby
	}
	return le, *l, nil
}

func (c *Controller) lobbyCmd(cmd LobbyCommand, host bool, check func(Lobby) error) error {
	le, l, err := c.lobbyEngine()
	if err != nil {
		return err
	}
	if host && !l.Host {
		return ErrNotHost
	}
	if check != nil {
		if err := check(l); err != nil {
			return err
		}
	}
	return le.Lobby(cmd)
}

// SetColor asks the host for a tunic color. It is always granted, even
// when another player wears it; the new color arrives in the lobby state.
func (c *Controller) SetColor(col playercolor.Color) error {
	return c.lobbyCmd(LobbyCommand{Kind: LobbySetColor, Color: col}, false, func(l Lobby) error {
		if l.Phase != LobbyPhaseLobby {
			return ErrColorsLocked
		}
		return nil
	})
}

// SetReady sets this player's ready flag.
func (c *Controller) SetReady(ready bool) error {
	return c.lobbyCmd(LobbyCommand{Kind: LobbySetReady, Ready: ready}, false, nil)
}

// SetDolphin reports this player's Dolphin state to the lobby.
func (c *Controller) SetDolphin(state string) error {
	return c.lobbyCmd(LobbyCommand{Kind: LobbySetDolphin, Text: state}, false, nil)
}

// SetFile reports this player's save-file hint to the lobby.
func (c *Controller) SetFile(hint string) error {
	return c.lobbyCmd(LobbyCommand{Kind: LobbySetFile, Text: hint}, false, nil)
}

// SetCoopSettings sets the session's co-op settings (host). After Start
// only Merge may change.
func (c *Controller) SetCoopSettings(s LobbySettings) error {
	return c.lobbyCmd(LobbyCommand{Kind: LobbySetSettings, Settings: s}, true, nil)
}

// Start starts the session (host): every member gets EventLobbyStart.
func (c *Controller) Start() error {
	return c.lobbyCmd(LobbyCommand{Kind: LobbyStart}, true, func(l Lobby) error {
		if l.Phase != LobbyPhaseLobby {
			return ErrWrongPhase
		}
		return nil
	})
}

// Go tells every member the host's side is ready (host, after Start);
// world is the co-op world it serves, or "".
func (c *Controller) Go(world string) error {
	return c.lobbyCmd(LobbyCommand{Kind: LobbyGo, Text: world}, true, func(l Lobby) error {
		if l.Phase != LobbyPhaseStarting {
			return ErrWrongPhase
		}
		return nil
	})
}

// Kick removes a member, or a CLI player by its row's ID (host). The name
// stays refused until the lobby empties, so a CLI join that reconnects
// stops instead of coming back.
func (c *Controller) Kick(member int) error {
	return c.lobbyCmd(LobbyCommand{Kind: LobbyKick, Member: member}, true, nil)
}

// RequestMerge asks the host to allow merging (EventLobbyRequest on the
// host's side).
func (c *Controller) RequestMerge() error {
	return c.lobbyCmd(LobbyCommand{Kind: LobbyRequestMerge}, false, nil)
}
