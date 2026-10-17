package engine

import (
	"context"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// The co-op seam (docs/api/00-design.md §7): co-op is its own mod
// requiring multiplayer, so the engine never names co-op's types. It
// meets co-op through CoopApp (what co-op offers the engine), Coop (a
// host or join session's co-op side, session.go), LobbyCoop (a lobby
// session's) and CoopSide (a running co-op session's console and
// status). mods/coop/session implements them over its Session and
// appCoop.

// CoopApp is co-op as the engine meets it.
type CoopApp interface {
	// Session is the co-op side of a host or join session run with o,
	// whose front end is f; nil when o is off.
	Session(o session.CoopOptions, f Front) Coop
	// Lobby is a lobby session's co-op side, which ends with ctx or Stop;
	// nil when this app plays lobby sessions without co-op.
	Lobby(ctx context.Context, env LobbyEnv) LobbyCoop
	// Worlds lists the co-op worlds (Engine.CoopWorlds).
	Worlds() ([]session.CoopWorld, error)
	// Sync runs coop-sync for co-op side cs on Dolphin pid against the
	// server at addr until ctx ends (GameDeps.CoopSync).
	Sync(ctx context.Context, name, addr string, pid uint32, rep log.Reporter, cs CoopSide) error
}

// CoopSide is a running co-op session as the engine reaches it: its
// console and its status.
type CoopSide interface {
	// CoopLive is the pipelines' status and the host's hub (nil on a
	// joiner), for session.Live.
	CoopLive() ([]session.CoopStatus, *session.CoopHubStatus)
	// CoopCommand runs one console command (Engine.Command).
	CoopCommand(c session.Command) error
	// CoopLine runs one console line as typed (Engine.CommandLine).
	CoopLine(line string)
}

// Front is a co-op session's front end, as the engine hands it to co-op.
type Front struct {
	// Out is the session's output, which the co-op questions are put to
	// and which hears the advice; nil for the CLI, whose questions and
	// console commands come on stdin.
	Out session.Output
	// Yes answers every question yes (--yes).
	Yes bool
	// Attach is handed the co-op side once it exists, so Command,
	// CommandLine and Live reach it.
	Attach func(CoopSide)
}

// LobbyEnv is what a lobby session's co-op side takes from the engine.
type LobbyEnv struct {
	// Out is the lobby session's output.
	Out session.LobbyOutput
	// Name is this player's name.
	Name string
	// CoopFn is this player's own co-op choices (the window's); nil for
	// none.
	CoopFn func() session.CoopOptions
	// Launch is this player's Dolphin launch options (its user folder);
	// nil for none.
	Launch func() session.LaunchOptions
	// SrvLog gets the hub's lines (the host's server log); nil on a
	// joiner.
	SrvLog func(string)
	// SetHub sets the hub on the host's server; nil on a joiner.
	SetHub func(network.CoopHandler)
	// Front is the co-op front end for options o.
	Front func(o session.CoopOptions) Front
	// PoseClient is the puppet-sync client of this player's pose
	// pipelines, where a warp finds the partner; nil before one runs.
	PoseClient func() *network.Client
}

// LobbyEnv is the co-op environment of a lobby session writing to out as
// name (the engine's own front end and status bridge).
func (e *Engine) LobbyEnv(out session.LobbyOutput, name string, coopFn func() session.CoopOptions, launch func() session.LaunchOptions) LobbyEnv {
	return LobbyEnv{Out: out, Name: name, CoopFn: coopFn, Launch: launch,
		Front: func(o session.CoopOptions) Front { return e.Front(out, o) },
		PoseClient: func() *network.Client {
			if e.ls == nil {
				return nil
			}
			return e.ls.PuppetClient()
		}}
}

// LobbyCoop is a lobby session's co-op side: the hooks the lobby
// connection calls (docs/10-lobby.md, design §2.5-§2.8).
type LobbyCoop interface {
	// Bind hands it the session's lobby client and game side.
	Bind(lc *network.LobbyClient, game CoopGame)
	// OnStart is the host's START with co-op on: the world, the hub, then
	// GO.
	OnStart(s network.LobbySettingsBody)
	// OnGo is a joiner's GO naming world ("" for none).
	OnGo(s network.LobbySettingsBody, world string)
	// OnSettings follows the lobby's settings after Start.
	OnSettings(s network.LobbySettingsBody)
	// UnbindFile zeroes the co-op tag of the file loaded in this
	// player's game (session.CoopFileEngine).
	UnbindFile(world string) error
	// Stop ends it, after the game side and the host's server stopped.
	Stop()
}

// CoopGame is a lobby session's game side as its co-op side sees it.
// *GameRunner implements it.
type CoopGame interface {
	// PlayablePID is the Dolphin the game side plays on while it runs the
	// patched game, else 0.
	PlayablePID() uint32
	// SetCoop starts coop-sync for cs beside the pose pipelines.
	SetCoop(cs CoopSide)
}

// noLobbyCoop is the co-op side of a lobby session without co-op.
type noLobbyCoop struct{}

func (noLobbyCoop) Bind(*network.LobbyClient, CoopGame)    {}
func (noLobbyCoop) OnStart(network.LobbySettingsBody)      {}
func (noLobbyCoop) OnGo(network.LobbySettingsBody, string) {}
func (noLobbyCoop) OnSettings(network.LobbySettingsBody)   {}
func (noLobbyCoop) UnbindFile(string) error                { return session.ErrNoCoop }
func (noLobbyCoop) Stop()                                  {}

// lobbyCoop is a lobby session's co-op side, and whether it has one (a
// noLobbyCoop when not).
func (e *Engine) lobbyCoop(ctx context.Context, env LobbyEnv) (LobbyCoop, bool) {
	if e.coopApp != nil {
		if ac := e.coopApp.Lobby(ctx, env); ac != nil {
			return ac, true
		}
	}
	return noLobbyCoop{}, false
}

// UnbindFile zeroes the co-op tag of the file loaded in this player's
// game (session.CoopFileEngine).
func (e *Engine) UnbindFile(world string) error {
	e.mu.Lock()
	ac := e.coop1
	e.mu.Unlock()
	if ac == nil {
		return session.ErrNoCoop
	}
	return ac.UnbindFile(world)
}

// CoopOn says the lobby settings s ask for co-op.
func CoopOn(s network.LobbySettingsBody) bool { return s.Coop != "" && s.Coop != network.CoopOff }

// Begin is what a session does first, for the tests of what plugs into
// the engine (co-op): it hands the status bridge out's chat, and returns
// what the session does last: take the chat back and forget the co-op
// side.
func (e *Engine) Begin(out session.Output) func() { return e.begin(out) }
