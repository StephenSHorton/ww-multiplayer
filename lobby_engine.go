package main

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/netutil"
	"github.com/StephenSHorton/ww-multiplayer/internal/network"
	"github.com/StephenSHorton/ww-multiplayer/internal/playercolor"
	"github.com/StephenSHorton/ww-multiplayer/internal/report"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
)

// sessionEngine's lobby side (session.LobbyEngine, docs/10-lobby.md). A
// lobby session holds one lobby connection (network.LobbyClient) and, for
// a host, runs the server it connects to. The lobby's state, colors,
// notices, START and GO go to the controller; its commands come back
// through Lobby. START starts the game side (app_game.go: this player's
// Dolphin and the pose pipelines on it; an engine without gameDeps, a
// test's, starts nothing), and the host answers its own START with GO at
// once while co-op is off (co-op worlds are App PR 9). No CLI subcommand
// runs a lobby session, so the CLI's output is unchanged.

// HostLobby starts the server and joins its lobby from this machine, which
// makes this player the host.
func (e *sessionEngine) HostLobby(ctx context.Context, cancel context.CancelFunc, o session.HostOptions, out session.LobbyOutput) error {
	defer e.begin(out)()
	name := o.Name
	if name == "" {
		name = "Host"
	}
	port := o.Port
	if port == 0 {
		port = defaultPort
	}
	srv := network.NewServer(port)
	srv.ListenHost = o.ListenHost
	srv.OnLog = func(msg string) {
		if strings.HasPrefix(msg, "[Chat] ") {
			return // the lobby connection delivers it as a chat line
		}
		out.Log(report.Info, fmt.Sprintf("[srv %s] %s", time.Now().Format("15:04:05"), msg))
	}
	if err := srv.Start(); err != nil {
		return fmt.Errorf("server start: %w", err)
	}
	defer srv.Stop()
	report.Logf(out, report.OK, "Hosting a lobby as %q on :%d.", name, port)
	ips := shareAddrs(listHostIPs(), port)
	for _, ip := range ips {
		report.Logf(out, report.Info, "  %s", ip)
	}
	return e.runLobby(ctx, cancel, hostDialAddr(o.ListenHost, port), name, o.Color, o.LaunchOptions, out, func() {
		if e.ls != nil {
			e.ls.begin(string(session.RoleHost), name, ips)
			e.ls.setServer(srv)
		}
	})
}

// hostDialAddr is where the host's own lobby client reaches its server:
// the address the server listens on alone (WW_LISTEN_HOST), else
// localhost when it listens on every interface.
func hostDialAddr(listenHost string, port int) string {
	dial := "localhost"
	if h := strings.Trim(listenHost, "[]"); h != "" && h != "0.0.0.0" && h != "::" {
		dial = h
	}
	return net.JoinHostPort(dial, strconv.Itoa(port))
}

// JoinLobby joins the lobby at o.Addr. A host from before the lobby refuses
// the lobby role; that comes back as session.ErrLegacyHost, and the
// controller joins directly.
func (e *sessionEngine) JoinLobby(ctx context.Context, cancel context.CancelFunc, o session.JoinOptions, out session.LobbyOutput) error {
	defer e.begin(out)()
	name := o.Name
	if name == "" {
		name = "Player"
	}
	addr := netutil.NormalizeAddr(o.Addr, defaultPort)
	report.Logf(out, report.OK, "Joining the lobby at %s as %q.", addr, name)
	err := e.runLobby(ctx, cancel, addr, name, o.Color, o.LaunchOptions, out, func() {
		if e.ls != nil {
			e.ls.begin(string(session.RoleJoin), name, nil)
		}
	})
	if network.IsLegacyHost(err) {
		return fmt.Errorf("%w: %w", session.ErrLegacyHost, err)
	}
	return err
}

// runLobby keeps the lobby connection to addr until ctx ends or a fatal
// error (returned). START starts the game side (gameRunner, with
// beginGame first: the status bridge's start), which plays on addr's
// server too; it is stopped, its pipelines ended and the mailbox reset,
// before runLobby returns, so a host's server stops after it. A fatal
// pipelines error (a version mismatch) ends the session through cancel.
func (e *sessionEngine) runLobby(ctx context.Context, cancel context.CancelFunc, addr, name string, color *playercolor.Color, launch func() session.LaunchOptions, out session.LobbyOutput, beginGame func()) (err error) {
	hello := network.LobbyHelloBody{App: version, OS: runtime.GOOS, Dolphin: network.DolphinNone}
	if color != nil {
		hello.Color = color.Hex()
	}
	lc := network.NewLobbyClient(name, hello)
	lc.Client().OnLog = func(s string) { out.Log(report.Net, s) }
	lc.Client().OnChat = out.Chat
	lc.OnState = func(st network.LobbyStateBody) {
		w, _ := lc.Welcome()
		out.LobbyState(lobbyView(st, w))
	}
	lc.OnNotice = func(b network.LobbyNoticeBody) {
		out.Log(report.Warn, "lobby: "+b.Text)
		out.LobbyNotice(session.Notice{Code: b.Code, Text: b.Text, Suggest: b.Suggest})
	}
	var game *gameRunner
	if e.game != nil {
		gctx := withColorPref(ctx, color)
		game = newGameRunner(e.game, launch, name, addr, out,
			func(state string) { _ = lc.SetDolphin(state) },
			func(error) { cancel() })
		var once sync.Once
		startGame := func() {
			once.Do(func() {
				if beginGame != nil {
					beginGame()
				}
				game.start(gctx)
			})
		}
		e.mu.Lock()
		e.game1 = game
		e.mu.Unlock()
		defer func() {
			if gerr := game.stop(); gerr != nil && err == nil {
				err = sessionErr(gerr)
			}
			if e.ls != nil {
				e.ls.reset()
			}
			e.mu.Lock()
			e.game1 = nil
			e.mu.Unlock()
		}()
		lc.OnStart = func(b network.LobbyStartBody) {
			out.LobbyStart(lobbySettings(b.Settings))
			startGame()
			goIfHost(lc, b.Settings, out)
		}
	} else {
		lc.OnStart = func(b network.LobbyStartBody) { out.LobbyStart(lobbySettings(b.Settings)) }
	}
	lc.OnGo = func(b network.LobbyGoBody) { out.LobbyGo(b.World) }
	lc.OnRequest = func(b network.LobbyRequestBody) { out.LobbyRequest(b.Kind, b.From) }
	lc.OnLink = linkTracker(out.LobbyLink, time.Now)

	e.mu.Lock()
	e.lobby = lc
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.lobby = nil
		e.mu.Unlock()
	}()
	// Failures to reach the host are logged as a join's are; a fatal one
	// is returned instead (the controller logs it, or falls back to a
	// direct join for a pre-lobby host), so the ConnectLog drops it.
	connLog := network.NewConnectLog(addr, func(s string) { out.Log(report.Warn, s) }, func(s string) { out.Log(report.OK, s) })
	connLog.Fatal = func(string) {}
	return lc.Run(ctx, addr, connLog)
}

// linkTracker turns a LobbyClient's OnLink calls into the controller's
// LobbyLink: connecting until the first WELCOME, then up, and
// reconnecting from a drop until the next WELCOME, counting the failed
// attempts on the way.
func linkTracker(report func(session.LobbyLink), now func() time.Time) func(up bool, err error) {
	var mu sync.Mutex
	link := session.LobbyLink{State: session.LinkConnecting, Since: now()}
	return func(up bool, err error) {
		mu.Lock()
		switch {
		case up:
			link = session.LobbyLink{State: session.LinkUp}
		case link.State == session.LinkUp:
			link = session.LobbyLink{State: session.LinkReconnecting, Since: now()}
			if err != nil { // the reconnect's first attempt failed without a drop seen
				link.Attempts, link.Err = 1, err.Error()
			}
		case err != nil:
			link.Attempts++
			link.Err = err.Error()
		}
		k := link
		mu.Unlock()
		report(k)
	}
}

// goIfHost sends GO for the host once its START came back, while co-op
// is off (no world to open first: design §2.6 step 4). A START replayed
// after the lobby is in game sends nothing.
func goIfHost(lc *network.LobbyClient, s network.LobbySettingsBody, out session.LobbyOutput) {
	if s.Coop != "" && s.Coop != "off" {
		return // co-op worlds are App PR 9: the host app opens one before GO
	}
	st, ok := lc.State()
	w, _ := lc.Welcome()
	if !ok || st.Phase != network.PhaseStarting || !lobbyView(st, w).Host {
		return
	}
	if err := lc.Go(""); err != nil {
		report.Logf(out, report.Warn, "Couldn't tell the players to go: %v", err)
	}
}

// LaunchDolphin launches this player's Dolphin again after Start.
func (e *sessionEngine) LaunchDolphin() error {
	e.mu.Lock()
	g := e.game1
	e.mu.Unlock()
	if g == nil {
		return session.ErrWrongPhase
	}
	return g.launchAgain()
}

func (e *sessionEngine) lobbyClient() *network.LobbyClient {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lobby
}

// Lobby sends one lobby command.
func (e *sessionEngine) Lobby(cmd session.LobbyCommand) error {
	lc := e.lobbyClient()
	if lc == nil {
		return session.ErrNoLobby
	}
	switch cmd.Kind {
	case session.LobbySetColor:
		return lc.SetColor(cmd.Color)
	case session.LobbySetReady:
		return lc.SetReady(cmd.Ready)
	case session.LobbySetDolphin:
		return lc.SetDolphin(cmd.Text)
	case session.LobbySetFile:
		return lc.SetFile(cmd.Text)
	case session.LobbySetSettings:
		return lc.SetSettings(wireSettings(cmd.Settings))
	case session.LobbyStart:
		return lc.Start()
	case session.LobbyGo:
		return lc.Go(cmd.Text)
	case session.LobbyKick:
		return lc.Kick(cmd.Member)
	case session.LobbyRequestMerge:
		return lc.RequestMerge()
	}
	return fmt.Errorf("unknown lobby command %q", cmd.Kind)
}

// lobbyView is the controller's view of a STATE, w being our WELCOME.
func lobbyView(st network.LobbyStateBody, w network.LobbyWelcomeBody) session.Lobby {
	l := session.Lobby{Member: w.Member, Host: w.Host, Phase: session.LobbyPhase(st.Phase), Rev: st.Rev, Settings: lobbySettings(st.Settings)}
	for _, m := range st.Members {
		c, _ := playercolor.Parse(m.Color)
		l.Members = append(l.Members, session.LobbyMember{ID: m.ID, Name: m.Name, Color: c, Ready: m.Ready, Host: m.Host,
			Dolphin: m.Dolphin, File: m.File, RTT: time.Duration(m.RTTMs) * time.Millisecond, Via: m.Via, Away: m.Away})
	}
	// Our row says whether we are host now: the host's slot can pass to
	// us after WELCOME (docs/10 §3).
	if self, ok := l.Self(); ok {
		l.Host = self.Host
	}
	return l
}

func lobbySettings(s network.LobbySettingsBody) session.LobbySettings {
	out := session.LobbySettings{Coop: s.Coop, DryRun: s.DryRun, Merge: s.Merge, LiveSwitches: s.LiveSwitches}
	for _, w := range s.Worlds {
		out.Worlds = append(out.Worlds, session.LobbyWorld{ID: w.ID, Seq: w.Seq, InUse: w.InUse})
	}
	return out
}

func wireSettings(s session.LobbySettings) network.LobbySettingsBody {
	out := network.LobbySettingsBody{Coop: s.Coop, DryRun: s.DryRun, Merge: s.Merge, LiveSwitches: s.LiveSwitches}
	for _, w := range s.Worlds {
		out.Worlds = append(out.Worlds, network.LobbyWorld{ID: w.ID, Seq: w.Seq, InUse: w.InUse})
	}
	return out
}
