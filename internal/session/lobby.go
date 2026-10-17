package session

// The pre-game lobby through the controller (docs/10-lobby.md; App PR 4).
// HostLobby and JoinLobby run a session that starts in the lobby: the
// engine holds the lobby connection (and, for a host, the server) and
// reports the lobby's state; the UI changes it with SetColor, SetReady,
// SetCoopSettings, Start, Go, Kick and RequestMerge. A pre-lobby host is
// joined directly instead, automatically (EventLegacyHost).
//
// What Start launches is App PR 8's (launch.go): every app gets its
// player's Dolphin going and runs the pose pipelines on it (co-op is PR
// 9), reporting the Dolphin through LobbyOutput.Dolphin.

import (
	"context"
	"errors"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/playercolor"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
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
	// Link is the lobby connection's health (the engine's LobbyLink).
	Link LobbyLink
}

// LinkState says how the lobby connection is doing.
type LinkState string

const (
	// LinkConnecting: not welcomed by the host yet.
	LinkConnecting LinkState = "connecting"
	// LinkUp: connected.
	LinkUp LinkState = "up"
	// LinkReconnecting: the connection dropped after it was up; the
	// engine reconnects with its backoff, and the host holds our slot
	// (color, ready) for network.LobbyHoldTimeout meanwhile.
	LinkReconnecting LinkState = "reconnecting"
)

// LobbyLink is the lobby connection's health, for the UI's connecting
// dialog and reconnect banner.
type LobbyLink struct {
	State LinkState
	// Attempts counts the failed connects since the connection was last
	// up (or since the start).
	Attempts int
	// Err is the last failed attempt's error text.
	Err string
	// Since is when the current outage (or the first attempt) began.
	Since time.Time
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
	// LobbyLink passes the lobby connection's health when it changes
	// (an attempt failed, it connected, it dropped).
	LobbyLink(k LobbyLink)
	// Dolphin passes this player's Dolphin after Start (State.Dolphin)
	// whenever it changes.
	Dolphin(d DolphinInfo)
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
	// LaunchDolphin launches this player's Dolphin again after Start
	// (it was closed, or the first launch failed); ErrWrongPhase before
	// Start, and an error while a Dolphin runs.
	LaunchDolphin() error
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

// dolphin records run id's Dolphin.
func (c *Controller) dolphin(id ID, d DolphinInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.Session != id || c.cur == nil || c.cur.id != id {
		return
	}
	if c.state.Dolphin == d {
		return
	}
	c.state.Dolphin = d
	c.publish(Event{Kind: EventState, Session: id})
}

func (o *output) Dolphin(d DolphinInfo) { o.c.dolphin(o.id, d) }

// legacyHostLine is logged when a lobby join falls back to a direct one.
const legacyHostLine = "This host runs an older ww-multiplayer without a lobby; joining directly."

// HostLobby starts a host session that begins in the lobby. It returns
// ErrNoLobby when the engine has no lobby, ErrBusy while a session runs.
func (c *Controller) HostLobby(ctx context.Context, o HostOptions) (ID, error) {
	le, ok := c.engine.(LobbyEngine)
	if !ok {
		return 0, ErrNoLobby
	}
	st := State{Role: RoleHost, Name: o.Name, Coop: o.Coop, Phase: Connecting, Lobby: newLobby(time.Now())}
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
	st := State{Role: RoleJoin, Name: o.Name, Addr: o.Addr, Coop: o.Coop, Phase: Connecting, Lobby: newLobby(time.Now())}
	return c.start(ctx, st, func(ctx context.Context, cancel context.CancelFunc, out Output) error {
		o2 := out.(*output)
		err := le.JoinLobby(ctx, cancel, o, o2)
		if !errors.Is(err, ErrLegacyHost) || ctx.Err() != nil {
			return err
		}
		c.legacy(o2.id)
		o2.Log(log.Info, legacyHostLine)
		return c.engine.Join(ctx, cancel, o, out)
	})
}

// newLobby is a lobby session's lobby before the host has answered.
func newLobby(now time.Time) *Lobby {
	return &Lobby{Phase: LobbyPhaseLobby, Link: LobbyLink{State: LinkConnecting, Since: now}}
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
	var link LobbyLink // kept: the engine's STATE doesn't carry it
	if c.state.Lobby != nil {
		link = c.state.Lobby.Link
	}
	c.state.Lobby = l.clone()
	c.state.Lobby.Link = link
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

func (o *output) LobbyLink(k LobbyLink) { o.c.lobbyLink(o.id, k) }

// lobbyLink records run id's lobby connection health.
func (c *Controller) lobbyLink(id ID, k LobbyLink) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.Session != id || c.cur == nil || c.cur.id != id || c.state.Lobby == nil {
		return
	}
	l := c.state.Lobby.clone()
	l.Link = k
	c.state.Lobby = l
	c.publish(Event{Kind: EventState, Session: id})
}

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
	o.c.request(o.id, kind, from)
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

// Start starts the session (host): every member gets EventLobbyStart, and
// every app gets its player's Dolphin going (launch.go). It returns
// ErrNotReady while a member isn't ready (Lobby.NotReady): StartAnyway
// starts without them.
func (c *Controller) Start() error { return c.start1(false) }

// StartAnyway is Start without waiting for everyone to be ready (the
// host's "Start anyway").
func (c *Controller) StartAnyway() error { return c.start1(true) }

func (c *Controller) start1(anyway bool) error {
	return c.lobbyCmd(LobbyCommand{Kind: LobbyStart}, true, func(l Lobby) error {
		if l.Phase != LobbyPhaseLobby {
			return ErrWrongPhase
		}
		if !anyway && len(l.NotReady()) > 0 {
			return ErrNotReady
		}
		return nil
	})
}

// LaunchDolphin launches this player's Dolphin again after Start: the
// "Dolphin closed" banner's Launch. ErrWrongPhase before Start.
func (c *Controller) LaunchDolphin() error {
	le, l, err := c.lobbyEngine()
	if err != nil {
		return err
	}
	if l.Phase == LobbyPhaseLobby {
		return ErrWrongPhase
	}
	return le.LaunchDolphin()
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
