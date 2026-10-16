package gui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/config"
	"github.com/StephenSHorton/ww-multiplayer/internal/playercolor"
	"github.com/StephenSHorton/ww-multiplayer/internal/report"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
)

// fakeEngine is a session engine with the lobby. HostLobby and JoinLobby
// record their options, publish a lobby with this player as its only
// member, hand their output to the test and run until cancelled; Lobby
// records the commands; SendChat records the lines.
type fakeEngine struct {
	outs chan session.LobbyOutput
	// unreachable: JoinLobby only fails to connect (no lobby state).
	unreachable bool
	// fail ends the running lobby session with its error.
	fail chan error

	mu    sync.Mutex
	host  []session.HostOptions
	join  []session.JoinOptions
	cmds  []session.LobbyCommand
	chats []string
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{outs: make(chan session.LobbyOutput, 8), fail: make(chan error, 1)}
}

func (e *fakeEngine) Host(ctx context.Context, _ context.CancelFunc, _ session.HostOptions, _ session.Output) error {
	<-ctx.Done()
	return nil
}

func (e *fakeEngine) Join(ctx context.Context, _ context.CancelFunc, _ session.JoinOptions, _ session.Output) error {
	<-ctx.Done()
	return nil
}

func (e *fakeEngine) Live() session.Live { return session.Live{} }

func (e *fakeEngine) SendChat(text string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.chats = append(e.chats, text)
	return nil
}

func (e *fakeEngine) lobby(ctx context.Context, host bool, name string, col *playercolor.Color, out session.LobbyOutput) error {
	if e.unreachable {
		out.LobbyLink(session.LobbyLink{State: session.LinkConnecting, Attempts: 1, Err: "connection refused"})
		e.outs <- out
		<-ctx.Done()
		return nil
	}
	c := playercolor.Vanilla()
	if col != nil {
		c = *col
	}
	out.LobbyState(session.Lobby{Member: 1, Host: host, Phase: session.LobbyPhaseLobby, Members: []session.LobbyMember{
		{ID: 1, Name: name, Color: c, Host: host, Via: session.ViaApp},
		{ID: 2, Name: "Tetra", Color: playercolor.Palette()[1].Color, Ready: true, Via: session.ViaCLI},
	}})
	e.outs <- out
	select {
	case <-ctx.Done():
		return nil
	case err := <-e.fail:
		return err
	}
}

func (e *fakeEngine) HostLobby(ctx context.Context, _ context.CancelFunc, o session.HostOptions, out session.LobbyOutput) error {
	e.mu.Lock()
	e.host = append(e.host, o)
	e.mu.Unlock()
	return e.lobby(ctx, true, o.Name, o.Color, out)
}

func (e *fakeEngine) JoinLobby(ctx context.Context, _ context.CancelFunc, o session.JoinOptions, out session.LobbyOutput) error {
	e.mu.Lock()
	e.join = append(e.join, o)
	e.mu.Unlock()
	return e.lobby(ctx, false, o.Name, o.Color, out)
}

func (e *fakeEngine) Lobby(cmd session.LobbyCommand) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cmds = append(e.cmds, cmd)
	return nil
}

func (e *fakeEngine) out(t *testing.T) session.LobbyOutput {
	t.Helper()
	select {
	case o := <-e.outs:
		return o
	case <-time.After(5 * time.Second):
		t.Fatal("the engine's lobby never started")
		return nil
	}
}

// newTestBridge returns a Bridge over a fake engine with its config in a
// temp dir, closed at the test's end.
func newTestBridge(t *testing.T) (*Bridge, *fakeEngine, string) {
	t.Helper()
	e := newFakeEngine()
	path := filepath.Join(t.TempDir(), "config.json")
	b := NewBridge(Options{Version: "v9.9.9", Controller: session.New(e, session.Options{}), ConfigPath: path})
	t.Cleanup(b.close)
	return b, e, path
}

func TestBridgeInfo(t *testing.T) {
	b, _, path := newTestBridge(t)
	v := b.Info()
	if v.Version != "v9.9.9" || v.Port != config.DefaultPort || v.MaxName != MaxNameLen {
		t.Fatalf("Info = %+v", v)
	}
	if len(v.Palette) != len(playercolor.Palette()) || !v.Palette[0].Classic || v.Palette[0].Hex != playercolor.Vanilla().Hex() {
		t.Fatalf("palette = %+v; want the playercolor palette, green (classic) first", v.Palette)
	}
	if v.Name != "" || v.Color != "" || len(v.RecentHosts) != 0 {
		t.Fatalf("a new player's Info has a name, color or hosts: %+v", v)
	}

	// A saved config fills the forms; WW_COLOR overrides its color.
	c := config.Defaults()
	c.Name, c.Color = "Aryll", "blue"
	c.AddRecentHost("26.1.2.3", "Radmin", time.Now())
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	b2 := NewBridge(Options{Controller: session.New(newFakeEngine(), session.Options{}), ConfigPath: path,
		Getenv: func(k string) string {
			if k == config.EnvColor {
				return "pink"
			}
			return ""
		}})
	defer b2.close()
	v = b2.Info()
	pink, _ := playercolor.Parse("pink")
	if v.Name != "Aryll" || v.Color != pink.Hex() || len(v.RecentHosts) != 1 || v.RecentHosts[0].Addr != "26.1.2.3" ||
		v.RecentHosts[0].Masked != "•••.•••.•••.3" || v.RecentHosts[0].Label != "Radmin" {
		t.Fatalf("Info from config = %+v", v)
	}
}

func TestBridgeHostValidates(t *testing.T) {
	b, e, _ := newTestBridge(t)
	for _, r := range []HostRequest{
		{Name: "  "},
		{Name: strings.Repeat("x", MaxNameLen+1)},
		{Name: "Link\x01"},
		{Name: "Link", Color: "mauve"},
		{Name: "Link", Port: 70000},
		{Name: "Link", Port: -1},
	} {
		if err := b.Host(r); err == nil {
			t.Errorf("Host(%+v) = nil, want an error", r)
		}
	}
	if len(e.host) != 0 {
		t.Fatalf("a refused form reached the engine: %+v", e.host)
	}
	if st := b.State(); st.Running || st.Session != 0 {
		t.Fatalf("State after refused forms = %+v", st)
	}
}

func TestBridgeHostStartsLobbyAndSaves(t *testing.T) {
	b, e, path := newTestBridge(t)
	if err := b.Host(HostRequest{Name: "  Link ", Color: "red", Port: 25570}); err != nil {
		t.Fatal(err)
	}
	e.out(t)
	red, _ := playercolor.Parse("red")
	if len(e.host) != 1 || e.host[0].Name != "Link" || e.host[0].Port != 25570 || e.host[0].Color == nil || *e.host[0].Color != red {
		t.Fatalf("HostLobby got %+v", e.host)
	}
	st := b.State()
	if !st.Running || st.Role != "Host" || st.Phase != "lobby" || st.Lobby == nil || !st.Lobby.Host {
		t.Fatalf("State = %+v", st)
	}
	if len(st.Lobby.Members) != 2 || !st.Lobby.Members[0].Self || st.Lobby.Members[0].Color != red.Hex() ||
		st.Lobby.Members[1].Self || st.Lobby.Members[1].Via != "cli" || !st.Lobby.Members[1].Ready {
		t.Fatalf("members = %+v", st.Lobby.Members)
	}
	if err := b.Host(HostRequest{Name: "Link"}); !errors.Is(err, session.ErrBusy) {
		t.Fatalf("a second Host = %v, want ErrBusy", err)
	}
	c, _ := config.Load(path)
	if c.Name != "Link" || c.Color != red.Hex() || c.Port != 25570 {
		t.Fatalf("saved config = %+v", c)
	}

	b.Leave()
	if st := b.State(); st.Running || st.Phase != "ended" || st.Failed {
		t.Fatalf("State after Leave = %+v", st)
	}
}

func TestBridgeJoin(t *testing.T) {
	b, e, path := newTestBridge(t)
	if err := b.Join(JoinRequest{Addr: " ", Name: "Medli"}); err == nil {
		t.Fatal("Join with no address = nil")
	}
	if err := b.Join(JoinRequest{Addr: "192.168.1.42:25570", Name: "Medli"}); err != nil {
		t.Fatal(err)
	}
	e.out(t)
	if len(e.join) != 1 || e.join[0].Addr != "192.168.1.42:25570" || e.join[0].Name != "Medli" || e.join[0].Color != nil {
		t.Fatalf("JoinLobby got %+v", e.join)
	}
	if st := b.State(); st.Role != "Join" || st.Addr != "192.168.1.42:25570" || st.Lobby == nil || st.Lobby.Host {
		t.Fatalf("State = %+v", st)
	}
	// The host was reached (its lobby state came), so it is a recent
	// host now.
	c, _ := config.Load(path)
	if c.Name != "Medli" || len(c.RecentHosts) != 1 || c.RecentHosts[0].Addr != "192.168.1.42:25570" {
		t.Fatalf("saved config = %+v", c)
	}
}

func TestBridgeLobbyCommands(t *testing.T) {
	b, e, path := newTestBridge(t)
	if err := b.SetReady(true); !errors.Is(err, session.ErrNoLobby) {
		t.Fatalf("SetReady with no session = %v, want ErrNoLobby", err)
	}
	if _, err := b.SendChat("hi"); !errors.Is(err, session.ErrNoSession) {
		t.Fatalf("SendChat with no session = %v, want ErrNoSession", err)
	}
	if err := b.Join(JoinRequest{Addr: "10.0.0.2", Name: "Medli"}); err != nil {
		t.Fatal(err)
	}
	e.out(t)
	if err := b.SetColor("not a color"); err == nil {
		t.Fatal("SetColor(bad) = nil")
	}
	if err := b.SetColor("#2C5CE0"); err != nil {
		t.Fatal(err)
	}
	if err := b.SetReady(true); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SendChat("  ahoy  "); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SendChat("   "); err != nil {
		t.Fatal(err)
	}
	blue, _ := playercolor.Parse("blue")
	e.mu.Lock()
	cmds, chats := e.cmds, e.chats
	e.mu.Unlock()
	if len(cmds) != 2 || cmds[0].Kind != session.LobbySetColor || cmds[0].Color != blue ||
		cmds[1].Kind != session.LobbySetReady || !cmds[1].Ready {
		t.Fatalf("lobby commands = %+v", cmds)
	}
	if len(chats) != 1 || chats[0] != "ahoy" {
		t.Fatalf("chats = %q", chats)
	}
	if c, _ := config.Load(path); c.Color != blue.Hex() {
		t.Fatalf("SetColor didn't save the color: %q", c.Color)
	}
}

func TestBridgePumpEvents(t *testing.T) {
	b, e, _ := newTestBridge(t)
	got := make(chan EventView, 64)
	ctx, cancel := context.WithCancel(context.Background())
	done := b.pump(ctx, func(ev EventView) { got <- ev })
	defer func() { cancel(); <-done }()

	if err := b.Host(HostRequest{Name: "Link"}); err != nil {
		t.Fatal(err)
	}
	out := e.out(t)
	out.Log(report.Warn, "careful")
	out.Chat("Tetra", "hello")
	out.LobbyNotice(session.Notice{Code: "name_taken", Text: "taken", Suggest: "Link (2)"})

	want := map[string]func(EventView) bool{
		"log":          func(v EventView) bool { return v.Level == "warn" && v.Text == "careful" },
		"chat":         func(v EventView) bool { return v.From == "Tetra" && v.Text == "hello" },
		"lobby-notice": func(v EventView) bool { return v.Code == "name_taken" && v.Suggest == "Link (2)" },
	}
	deadline := time.After(5 * time.Second)
	for len(want) > 0 {
		select {
		case v := <-got:
			if v.Session == 0 || v.Time == "" {
				t.Fatalf("event without session or time: %+v", v)
			}
			if ok, has := want[v.Kind]; has && ok(v) {
				delete(want, v.Kind)
			}
		case <-deadline:
			t.Fatalf("events never seen: %v", want)
		}
	}
}

func TestEventKindsCoverController(t *testing.T) {
	// Every controller event kind has a name the frontend knows.
	for k := session.EventLog; k <= session.EventLegacyHost; k++ {
		if eventKinds[k] == "" {
			t.Errorf("EventKind %d has no frontend name", k)
		}
	}
}
