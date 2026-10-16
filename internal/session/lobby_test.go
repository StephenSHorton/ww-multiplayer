package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/playercolor"
	"github.com/StephenSHorton/ww-multiplayer/internal/report"
)

// fakeLobby is a fakeEngine with the lobby: HostLobby and JoinLobby hand
// their LobbyOutput to the test and run until cancelled (or fail with
// joinErr at once); Lobby records the commands.
type fakeLobby struct {
	*fakeEngine
	joinErr error
	outs    chan LobbyOutput

	mu        sync.Mutex
	cmds      []LobbyCommand
	joined    int                // direct Join calls
	joinColor *playercolor.Color // the Color the last direct Join got
}

func newFakeLobby() *fakeLobby {
	return &fakeLobby{fakeEngine: newFake(), outs: make(chan LobbyOutput, 8)}
}

func (e *fakeLobby) HostLobby(ctx context.Context, cancel context.CancelFunc, _ HostOptions, out LobbyOutput) error {
	e.outs <- out
	<-ctx.Done()
	return nil
}

func (e *fakeLobby) JoinLobby(ctx context.Context, cancel context.CancelFunc, _ JoinOptions, out LobbyOutput) error {
	if e.joinErr != nil {
		return e.joinErr
	}
	e.outs <- out
	<-ctx.Done()
	return nil
}

func (e *fakeLobby) Join(ctx context.Context, cancel context.CancelFunc, o JoinOptions, out Output) error {
	e.mu.Lock()
	e.joined++
	e.joinColor = o.Color
	e.mu.Unlock()
	return e.fakeEngine.Join(ctx, cancel, o, out)
}

func (e *fakeLobby) Lobby(cmd LobbyCommand) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cmds = append(e.cmds, cmd)
	return nil
}

func (e *fakeLobby) commands() []LobbyCommand {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]LobbyCommand(nil), e.cmds...)
}

func (e *fakeLobby) out(t *testing.T) LobbyOutput {
	t.Helper()
	select {
	case o := <-e.outs:
		return o
	case <-time.After(5 * time.Second):
		t.Fatal("the lobby engine was never started")
		return nil
	}
}

func lobbyView(phase LobbyPhase, host bool) Lobby {
	return Lobby{Member: 1, Host: host, Phase: phase, Rev: 1, Settings: LobbySettings{Coop: "off"},
		Members: []LobbyMember{{ID: 1, Name: "Me", Color: playercolor.Vanilla(), Host: host, Via: ViaApp},
			{ID: 65538, Name: "Cli", Color: playercolor.Palette()[1].Color, Ready: true, Via: ViaCLI}}}
}

// TestLobbyHostFlow: a lobby host goes Connecting -> InLobby -> Starting ->
// InGame as its lobby does, with State.Lobby the lobby's view; the
// commands reach the engine, and the controller refuses the ones the
// phase rules out before they go on the wire.
func TestLobbyHostFlow(t *testing.T) {
	e := newFakeLobby()
	c := New(e, Options{})
	ch, unsub := c.Subscribe()
	defer unsub()
	red := playercolor.Palette()[1].Color

	id, err := c.HostLobby(context.Background(), HostOptions{Name: "Me", Color: &red})
	if err != nil {
		t.Fatal(err)
	}
	if st := c.Snapshot(); st.Phase != Connecting || st.Lobby == nil || st.Role != RoleHost {
		t.Fatalf("after HostLobby: %+v", st)
	}
	out := e.out(t)
	out.LobbyState(lobbyView(LobbyPhaseLobby, true))
	st := c.Snapshot()
	if st.Phase != InLobby || !st.Lobby.Host || len(st.Lobby.Members) != 2 || st.Phase.String() != "lobby" {
		t.Fatalf("in lobby: %+v", st)
	}
	if self, ok := st.Lobby.Self(); !ok || self.Name != "Me" {
		t.Fatalf("Self = %+v, %v", self, ok)
	}
	// Snapshot is a copy.
	st.Lobby.Members[0].Name = "changed"
	if c.Snapshot().Lobby.Members[0].Name != "Me" {
		t.Fatal("Snapshot shares the lobby's members")
	}

	for _, f := range []func() error{
		func() error { return c.SetColor(red) },
		func() error { return c.SetReady(true) },
		func() error { return c.SetDolphin("title") },
		func() error { return c.SetFile("File 2") },
		func() error { return c.SetCoopSettings(LobbySettings{Coop: "new"}) },
		func() error { return c.Kick(2) },
		func() error { return c.RequestMerge() },
		c.Start,
	} {
		if err := f(); err != nil {
			t.Fatalf("lobby command: %v", err)
		}
	}
	if err := c.Go(""); !errors.Is(err, ErrWrongPhase) {
		t.Fatalf("Go before the lobby says starting = %v", err)
	}
	var kinds []string
	for _, cmd := range e.commands() {
		kinds = append(kinds, string(cmd.Kind))
	}
	if got, want := strings.Join(kinds, " "), "color ready dolphin file settings kick request-merge start"; got != want {
		t.Fatalf("commands %q, want %q", got, want)
	}

	out.LobbyColor(ColorReply{Wanted: red, ClashWith: "Cli", Suggest: playercolor.Palette()[4].Color})
	out.LobbyNotice(Notice{Code: "not_host", Text: "nope"})
	out.LobbyRequest("merge", "Jo")
	out.LobbyStart(LobbySettings{Coop: "new"})
	out.LobbyState(lobbyView(LobbyPhaseStarting, true))
	if st := c.Snapshot(); st.Phase != Starting {
		t.Fatalf("phase %v after START", st.Phase)
	}
	if err := c.SetColor(red); !errors.Is(err, ErrColorsLocked) {
		t.Fatalf("SetColor after Start = %v", err)
	}
	if err := c.Start(); !errors.Is(err, ErrWrongPhase) {
		t.Fatalf("second Start = %v", err)
	}
	if err := c.Go("0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	out.LobbyGo("0123456789abcdef")
	out.LobbyState(lobbyView(LobbyPhaseInGame, true))
	if st := c.Snapshot(); st.Phase != InGame {
		t.Fatalf("phase %v after GO", st.Phase)
	}

	evs := collect(t, ch, func(ev Event) bool { return ev.Kind == EventLobbyGo })
	var got []string
	for _, ev := range evs {
		switch ev.Kind {
		case EventLobbyColor:
			got = append(got, "color:"+ev.Color.ClashWith)
		case EventLobbyNotice:
			got = append(got, "notice:"+ev.Notice.Code)
		case EventLobbyRequest:
			got = append(got, "request:"+ev.Text+":"+ev.From)
		case EventLobbyStart:
			got = append(got, "start:"+ev.Settings.Coop)
		case EventLobbyGo:
			got = append(got, "go:"+ev.Text)
		}
		if ev.Session != id {
			t.Fatalf("event from session %d", ev.Session)
		}
	}
	if s, want := strings.Join(got, " "), "color:Cli notice:not_host request:merge:Jo start:new go:0123456789abcdef"; s != want {
		t.Fatalf("lobby events %q, want %q", s, want)
	}
	c.Leave()
	if st := c.Snapshot(); st.Phase != Ended || st.Failed {
		t.Fatalf("after Leave: %+v", st)
	}
	if err := c.SetReady(true); !errors.Is(err, ErrNoLobby) {
		t.Fatalf("SetReady after Leave = %v", err)
	}
}

// TestLobbyJoinerHostOnlyCommands: a joiner's host-only commands are
// refused by the controller.
func TestLobbyJoinerHostOnlyCommands(t *testing.T) {
	e := newFakeLobby()
	c := New(e, Options{})
	if _, err := c.JoinLobby(context.Background(), JoinOptions{Name: "Me", Addr: "1.2.3.4"}); err != nil {
		t.Fatal(err)
	}
	e.out(t).LobbyState(lobbyView(LobbyPhaseLobby, false))
	for name, f := range map[string]func() error{
		"settings": func() error { return c.SetCoopSettings(LobbySettings{}) },
		"start":    c.Start,
		"go":       func() error { return c.Go("") },
		"kick":     func() error { return c.Kick(1) },
	} {
		if err := f(); !errors.Is(err, ErrNotHost) {
			t.Errorf("joiner %s = %v, want ErrNotHost", name, err)
		}
	}
	if err := c.SetReady(true); err != nil {
		t.Fatal(err)
	}
	c.Leave()
}

// TestLobbyLegacyHostFallback: a host without a lobby is joined directly:
// EventLegacyHost, an Info line saying so, Phase InGame with
// State.Lobby.Legacy, the engine's direct Join, and no lobby commands.
// The fallback is not a failure.
func TestLobbyLegacyHostFallback(t *testing.T) {
	e := newFakeLobby()
	e.joinErr = fmt.Errorf("%w: host refused the connection: unknown connection role 2", ErrLegacyHost)
	c := New(e, Options{})
	ch, unsub := c.Subscribe()
	defer unsub()

	pink := playercolor.Palette()[6].Color
	id, err := c.JoinLobby(context.Background(), JoinOptions{Name: "Me", Addr: "old-host", Color: &pink})
	if err != nil {
		t.Fatal(err)
	}
	waitStarted(t, e.fakeEngine)
	st := waitPhase(t, c, InGame)
	if st.Lobby == nil || !st.Lobby.Legacy {
		t.Fatalf("after the fallback: %+v", st)
	}
	if err := c.SetReady(true); !errors.Is(err, ErrNoLobby) {
		t.Fatalf("lobby command after the fallback = %v", err)
	}
	if err := c.SendChat("hi"); err != nil {
		t.Fatal(err)
	}
	c.Leave()
	evs := collect(t, ch, endedState(c, id))
	sawLegacy, sawLine := false, false
	for _, ev := range evs {
		sawLegacy = sawLegacy || ev.Kind == EventLegacyHost
		sawLine = sawLine || (ev.Kind == EventLog && ev.Level == report.Info && ev.Text == legacyHostLine)
	}
	if !sawLegacy || !sawLine {
		t.Fatalf("events %+v: legacy %v, line %v", evs, sawLegacy, sawLine)
	}
	if st := c.Snapshot(); st.Failed || st.Phase != Ended || st.EndReason != "no error was reported" {
		t.Fatalf("ended: %+v", st)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.joined != 1 {
		t.Fatalf("direct joins %d, want 1", e.joined)
	}
	// The color chosen for the lobby goes to the direct join too.
	if e.joinColor == nil || *e.joinColor != pink {
		t.Fatalf("direct join's Color %v, want %v", e.joinColor, pink)
	}
}

// TestLobbyJoinFailure: a lobby join that fails for another reason (a
// taken name) ends the session as a failure, without a direct join.
func TestLobbyJoinFailure(t *testing.T) {
	e := newFakeLobby()
	e.joinErr = errors.New("lobby: the name \"Me\" is taken in this lobby; pick another")
	c := New(e, Options{})
	id, _ := c.JoinLobby(context.Background(), JoinOptions{Name: "Me", Addr: "h"})
	st := c.Wait(id)
	if !st.Failed || !strings.Contains(st.EndReason, "taken") {
		t.Fatalf("ended %+v", st)
	}
	if e.joined != 0 {
		t.Fatal("fell back to a direct join on a non-legacy error")
	}
}

// TestLobbyNeedsLobbyEngine: an engine without the lobby refuses the lobby
// sessions and commands; the direct Host and Join are unchanged.
func TestLobbyNeedsLobbyEngine(t *testing.T) {
	e := newFake()
	c := New(e, Options{})
	if _, err := c.HostLobby(context.Background(), HostOptions{}); !errors.Is(err, ErrNoLobby) {
		t.Fatalf("HostLobby = %v", err)
	}
	if _, err := c.JoinLobby(context.Background(), JoinOptions{}); !errors.Is(err, ErrNoLobby) {
		t.Fatalf("JoinLobby = %v", err)
	}
	if err := c.SetReady(true); !errors.Is(err, ErrNoLobby) {
		t.Fatalf("SetReady with no session = %v", err)
	}
	id, _ := c.Host(context.Background(), HostOptions{Name: "Link"})
	waitStarted(t, e)
	if st := c.Snapshot(); st.Phase != InGame || st.Lobby != nil {
		t.Fatalf("direct Host: %+v", st)
	}
	if err := c.Start(); !errors.Is(err, ErrNoLobby) {
		t.Fatalf("Start in a direct session = %v", err)
	}
	c.Leave()
	c.Wait(id)
}
