package main

import (
	"context"
	"errors"

	"net"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/network"
	"github.com/StephenSHorton/ww-multiplayer/internal/playercolor"
	"github.com/StephenSHorton/ww-multiplayer/internal/report"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
)

// waitSnap polls the controller until cond holds.
func waitSnap(t *testing.T, c *session.Controller, what string, cond func(session.State) bool) session.State {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := c.Snapshot()
		if cond(st) {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: state %+v (lobby %+v)", what, st, st.Lobby)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestSessionEngine_JoinLobby drives the app's engine through the
// controller against a real server: the controller joins the lobby
// (Connecting -> InLobby), its color request and ready flag reach the host's
// view, and the host's START and GO move it to Starting and InGame. Chat
// goes over the lobby connection. No Dolphin is involved: START launches
// nothing yet.
func TestSessionEngine_JoinLobby(t *testing.T) {
	srv := network.NewServer(0) // loopback only
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	addr := srv.Addr().String()

	// The host's app: the first lobby member from this machine.
	host := network.NewLobbyClient("Host", network.LobbyHelloBody{Color: "red"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hostDone := make(chan error, 1)
	go func() { hostDone <- host.Run(ctx, addr, nil) }()
	defer func() { cancel(); <-hostDone }()
	var hostChat = make(chan string, 4)
	host.Client().OnChat = func(from, text string) { hostChat <- from + ": " + text }
	if !waitForCond(func() bool { st, ok := host.State(); return ok && len(st.Members) == 1 }) {
		t.Fatal("host never joined its lobby")
	}

	e := &sessionEngine{ls: &liveState{}}
	c := session.New(e, session.Options{})
	evs, unsub := c.Subscribe()
	defer unsub()
	red := playercolor.Palette()[1].Color
	id, err := c.JoinLobby(context.Background(), session.JoinOptions{Name: "Joiner", Addr: addr, Color: &red})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Leave()
	st := waitSnap(t, c, "in the lobby", func(st session.State) bool {
		return st.Phase == session.InLobby && st.Lobby != nil && len(st.Lobby.Members) == 2
	})
	self, ok := st.Lobby.Self()
	if !ok || self.Name != "Joiner" || self.Color != red || st.Lobby.Host {
		t.Fatalf("self %+v (host %v); the joiner asked for the host's red and shares it", self, st.Lobby.Host)
	}
	if err := c.Start(); !errors.Is(err, session.ErrNotHost) {
		t.Fatalf("joiner Start = %v", err)
	}
	if err := c.SetReady(true); err != nil {
		t.Fatal(err)
	}
	if !waitForCond(func() bool {
		st, _ := host.State()
		for _, m := range st.Members {
			if m.Name == "Joiner" && m.Ready {
				return true
			}
		}
		return false
	}) {
		t.Fatal("the joiner's ready never reached the host")
	}
	if err := c.SendChat("hello host"); err != nil {
		t.Fatal(err)
	}
	select {
	case line := <-hostChat:
		if line != "Joiner: hello host" {
			t.Fatalf("host chat %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("chat never reached the host")
	}

	host.Start()
	waitSnap(t, c, "starting", func(st session.State) bool { return st.Phase == session.Starting })
	if err := c.SetColor(playercolor.Vanilla()); !errors.Is(err, session.ErrColorsLocked) {
		t.Fatalf("SetColor after Start = %v", err)
	}
	host.Go("")
	waitSnap(t, c, "in game", func(st session.State) bool { return st.Phase == session.InGame })

	sawStart, sawGo := false, false
	timeout := time.After(5 * time.Second)
	for !sawStart || !sawGo {
		select {
		case ev := <-evs:
			if ev.Session == id {
				sawStart = sawStart || ev.Kind == session.EventLobbyStart
				sawGo = sawGo || ev.Kind == session.EventLobbyGo
			}
		case <-timeout:
			t.Fatalf("events: start %v go %v", sawStart, sawGo)
		}
	}
	c.Leave()
	if st := c.Snapshot(); st.Phase != session.Ended || st.Failed {
		t.Fatalf("after Leave: %+v", st)
	}
	if err := c.SetReady(false); !errors.Is(err, session.ErrNoLobby) {
		t.Fatalf("lobby command after Leave = %v", err)
	}
}

// TestSessionEngine_JoinLobbyLegacyHost: a pre-lobby host's RejectRole
// comes back from JoinLobby as session.ErrLegacyHost (the controller then
// joins directly; internal/session tests that step).
func TestSessionEngine_JoinLobbyLegacyHost(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		network.ReadMessage(conn)
		network.ReadMessage(conn)
		network.WriteMessage(conn, network.MsgReject, network.RejectMessage(network.RejectRole,
			"unknown connection role 2; both players need the same ww-multiplayer version"))
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		for {
			if _, err := network.ReadMessage(conn); err != nil {
				return
			}
		}
	}()
	e := &sessionEngine{}
	out := &recOutput{}
	err = e.JoinLobby(context.Background(), func() {}, session.JoinOptions{Name: "App", Addr: ln.Addr().String()}, out)
	if !errors.Is(err, session.ErrLegacyHost) || !errors.Is(err, network.ErrFatal) {
		t.Fatalf("JoinLobby = %v, want ErrLegacyHost", err)
	}
	for _, l := range out.lines {
		if strings.Contains(l, "Can't join") {
			t.Errorf("the legacy refusal was logged as a failure: %q", l)
		}
	}
}

// recOutput is a session.LobbyOutput that records log lines.
type recOutput struct {
	lines []string
}

func (o *recOutput) Log(_ report.Level, msg string)             { o.lines = append(o.lines, msg) }
func (o *recOutput) Chat(from, text string)                     {}
func (o *recOutput) Ask(context.Context, session.Question) bool { return false }
func (o *recOutput) Advise(session.Advice)                      {}
func (o *recOutput) LobbyState(session.Lobby)                   {}
func (o *recOutput) LobbyNotice(session.Notice)                 {}
func (o *recOutput) LobbyStart(session.LobbySettings)           {}
func (o *recOutput) LobbyGo(string)                             {}
func (o *recOutput) LobbyRequest(string, string)                {}

func waitForCond(cond func() bool) bool {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

// TestColorPrefFromSession: a session's own color (JoinOptions.Color, kept
// when a lobby join falls back to a direct one) overrides --color for the
// player connections it opens; without one, --color applies as before.
func TestColorPrefFromSession(t *testing.T) {
	saved := colorPref
	t.Cleanup(func() { colorPref = saved })
	flag := playercolor.Palette()[2].Color
	colorPref = &flag
	if got := colorPrefFor(context.Background()); got == nil || *got != flag {
		t.Fatalf("no session color: %v, want --color %v", got, flag)
	}
	if got := colorPrefFor(withColorPref(context.Background(), nil)); got == nil || *got != flag {
		t.Fatalf("nil session color: %v, want --color %v", got, flag)
	}
	pink := playercolor.Palette()[6].Color
	ctx := withColorPref(context.Background(), &pink)
	pink = flag // withColorPref keeps its own copy
	if got := colorPrefFor(ctx); got == nil || *got != playercolor.Palette()[6].Color {
		t.Fatalf("session color: %v", got)
	}
}

// TestLobbyViewHostFromState: the host flag follows our STATE row, so a
// member the host's slot passes to after its WELCOME becomes host.
func TestLobbyViewHostFromState(t *testing.T) {
	st := network.LobbyStateBody{Phase: network.PhaseLobby, Members: []network.LobbyMember{
		{ID: 2, Name: "Me", Color: "#D83030", Host: true, Via: network.ViaApp}}}
	if l := lobbyView(st, network.LobbyWelcomeBody{Member: 2, Host: false}); !l.Host {
		t.Fatalf("lobbyView %+v: not host", l)
	}
	st.Members[0].Host = false
	if l := lobbyView(st, network.LobbyWelcomeBody{Member: 2, Host: true}); l.Host {
		t.Fatalf("lobbyView %+v: still host", l)
	}
}
