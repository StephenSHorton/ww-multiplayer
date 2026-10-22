package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/playercolor"
)

// --- helpers ----------------------------------------------------------------

// lobbyWatch records a lobby client's callbacks.
type lobbyWatch struct {
	mu       sync.Mutex
	notices  []LobbyNoticeBody
	starts   []LobbyStartBody
	gos      []LobbyGoBody
	requests []LobbyRequestBody
	chats    []string
}

func watchLobby(l *LobbyClient) *lobbyWatch {
	w := &lobbyWatch{}
	l.OnNotice = func(b LobbyNoticeBody) { w.mu.Lock(); w.notices = append(w.notices, b); w.mu.Unlock() }
	l.OnStart = func(b LobbyStartBody) { w.mu.Lock(); w.starts = append(w.starts, b); w.mu.Unlock() }
	l.OnGo = func(b LobbyGoBody) { w.mu.Lock(); w.gos = append(w.gos, b); w.mu.Unlock() }
	l.OnRequest = func(b LobbyRequestBody) { w.mu.Lock(); w.requests = append(w.requests, b); w.mu.Unlock() }
	l.Client().OnChat = func(from, text string) { w.mu.Lock(); w.chats = append(w.chats, from+": "+text); w.mu.Unlock() }
	return w
}

func (w *lobbyWatch) lens() (notices, starts, gos, requests, chats int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.notices), len(w.starts), len(w.gos), len(w.requests), len(w.chats)
}

func (w *lobbyWatch) lastNotice() LobbyNoticeBody {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.notices[len(w.notices)-1]
}

func (w *lobbyWatch) hasChat(line string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, c := range w.chats {
		if c == line {
			return true
		}
	}
	return false
}

// lobbyHostName is the member tests treat as on the host's machine: every
// test client is on loopback, so the server's host check goes by name.
const lobbyHostName = "Host"

// listenLobby starts a server whose lobby holds slots for hold, with fast
// heartbeats.
func listenLobby(t *testing.T, hold time.Duration) (*Server, string) {
	t.Helper()
	fastNetTimers(t)
	saved := LobbyHoldTimeout
	LobbyHoldTimeout = hold
	t.Cleanup(func() { LobbyHoldTimeout = saved })
	srv := NewServer(0)
	srv.lobby.local = func(_ net.Addr, name string) bool { return name == lobbyHostName }
	if err := srv.Start(); err != nil {
		t.Fatalf("server start: %v", err)
	}
	t.Cleanup(srv.Stop)
	return srv, serverAddr(t, srv)
}

// lobbyRun is a LobbyClient.Run in the background.
type lobbyRun struct {
	cancel context.CancelFunc
	done   chan error
}

func runLobby(t *testing.T, l *LobbyClient, addr string) *lobbyRun {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &lobbyRun{cancel: cancel, done: make(chan error, 1)}
	go func() { r.done <- l.Run(ctx, addr, nil) }()
	t.Cleanup(func() { r.cancel(); <-r.done })
	return r
}

// result waits for Run to return on its own.
func (r *lobbyRun) result(t *testing.T) error {
	t.Helper()
	select {
	case err := <-r.done:
		r.done <- err // for the cleanup
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("lobby Run did not return")
		return nil
	}
}

// waitState waits until l's STATE satisfies cond.
func waitState(t *testing.T, l *LobbyClient, what string, cond func(LobbyStateBody) bool) LobbyStateBody {
	t.Helper()
	var st LobbyStateBody
	if !waitFor(5*time.Second, func() bool {
		var ok bool
		st, ok = l.State()
		return ok && cond(st)
	}) {
		t.Fatalf("%s: lobby state %+v", what, st)
	}
	return st
}

func rowOf(st LobbyStateBody, name string) (LobbyMember, bool) {
	for _, m := range st.Members {
		if m.Name == name {
			return m, true
		}
	}
	return LobbyMember{}, false
}

func colorOfRow(t *testing.T, st LobbyStateBody, name string) playercolor.Color {
	t.Helper()
	m, ok := rowOf(st, name)
	if !ok {
		t.Fatalf("no row %q in %+v", name, st.Members)
	}
	c, err := playercolor.Parse(m.Color)
	if err != nil {
		t.Fatalf("row %q color %q: %v", name, m.Color, err)
	}
	return c
}

func connectPlayer(t *testing.T, addr, name string) *Client {
	t.Helper()
	c := NewClient(name)
	if err := c.Connect(addr); err != nil {
		t.Fatalf("%s connect: %v", name, err)
	}
	t.Cleanup(c.Disconnect)
	return c
}

var lobbyRed, lobbyBlue = playercolor.Palette()[1].Color, playercolor.Palette()[2].Color

// --- loopback integration ----------------------------------------------------

// TestLobbyLoopbackSession is the PR 4 integration test: two lobby clients
// and a CLI player on one server. They join, pick colors (two share one),
// get ready; the host starts, colors lock, the host sends GO; the app's
// pose connection takes its lobby color; chat crosses between lobby and
// player connections; lobby connections stay out of the roster and count.
func TestLobbyLoopbackSession(t *testing.T) {
	srv, addr := listenLobby(t, time.Minute)

	host := NewLobbyClient(lobbyHostName, LobbyHelloBody{App: "test", OS: "linux", Color: "red"})
	hw := watchLobby(host)
	runLobby(t, host, addr)
	waitState(t, host, "host joined", func(st LobbyStateBody) bool { return len(st.Members) == 1 })
	if w, _ := host.Welcome(); !w.Host || w.Phase != PhaseLobby || w.Token == "" {
		t.Fatalf("host WELCOME %+v", w)
	}

	// The joiner asks for red too: granted, the same color as the host,
	// with no COLOR frame or notice about it.
	joiner := NewLobbyClient("Joiner", LobbyHelloBody{Color: "#D02C2C"})
	jw := watchLobby(joiner)
	runLobby(t, joiner, addr)
	st := waitState(t, joiner, "joiner joined", func(st LobbyStateBody) bool { return len(st.Members) == 2 })
	if w, _ := joiner.Welcome(); w.Host {
		t.Fatal("the joiner is host")
	}
	if h, j := colorOfRow(t, st, lobbyHostName), colorOfRow(t, st, "Joiner"); h != lobbyRed || j != lobbyRed {
		t.Fatalf("colors: host %v joiner %v, want both red", h, j)
	}

	// A CLI player (two connections under one name, as host/join runs
	// them) shows as one "via cli" row, ready. With no preference it wears
	// green.
	cli := connectPlayer(t, addr, "Cli")
	cli2 := connectPlayer(t, addr, "Cli")
	var chats chatRecorder
	cli.OnChat = chats.fn()
	st = waitState(t, host, "CLI row", func(st LobbyStateBody) bool { _, ok := rowOf(st, "Cli"); return ok })
	row, _ := rowOf(st, "Cli")
	if row.Via != ViaCLI || !row.Ready || len(st.Members) != 3 {
		t.Fatalf("CLI row %+v in %+v", row, st.Members)
	}
	cc := colorOfRow(t, st, "Cli")
	if cc != playercolor.Vanilla() {
		t.Fatalf("CLI color %v, want green (no preference)", cc)
	}
	// The CLI player learns its own color from the player list the server
	// sends after its welcome, which can arrive after the lobby lists it.
	if !waitFor(5*time.Second, func() bool { _, ok := cli.MyColor(); return ok }) {
		t.Fatal("the CLI player never got its color from the player list")
	}
	if mc, _ := cli.MyColor(); mc != cc {
		t.Fatalf("CLI player list color %v, lobby says %v", mc, cc)
	}
	// Its first pose marks its Dolphin in game.
	if err := cli2.SendPose(1, make([]byte, 48), nil); err != nil {
		t.Fatal(err)
	}
	waitState(t, host, "CLI in game", func(st LobbyStateBody) bool { r, _ := rowOf(st, "Cli"); return r.Dolphin == DolphinInGame })

	// The joiner asks for the CLI player's color: granted, shared.
	if err := joiner.SetColor(cc); err != nil {
		t.Fatal(err)
	}
	waitState(t, host, "joiner shares the CLI color", func(st LobbyStateBody) bool {
		r, _ := rowOf(st, "Joiner")
		return r.Color == cc.Hex()
	})
	if n, _, _, _, _ := jw.lens(); n != 0 {
		t.Fatalf("the joiner got %d notices for a shared color: %+v", n, jw.lastNotice())
	}
	// And another color, as freely.
	if err := joiner.SetColor(lobbyBlue); err != nil {
		t.Fatal(err)
	}
	waitState(t, host, "joiner's blue", func(st LobbyStateBody) bool {
		r, _ := rowOf(st, "Joiner")
		return r.Color == lobbyBlue.Hex()
	})

	// Ready, then a joiner can't start, the host can.
	joiner.SetReady(true)
	host.SetReady(true)
	waitState(t, host, "both ready", func(st LobbyStateBody) bool {
		a, _ := rowOf(st, "Joiner")
		b, _ := rowOf(st, lobbyHostName)
		return a.Ready && b.Ready
	})
	joiner.Start()
	if !waitFor(2*time.Second, func() bool { n, _, _, _, _ := jw.lens(); return n == 1 }) || jw.lastNotice().Code != NoticeNotHost {
		t.Fatal("a joiner's START was not refused")
	}
	host.SetSettings(LobbySettingsBody{Coop: CoopOff, DryRun: true})
	waitState(t, joiner, "settings", func(st LobbyStateBody) bool { return st.Settings.DryRun })
	if err := host.Start(); err != nil {
		t.Fatal(err)
	}
	waitState(t, joiner, "starting", func(st LobbyStateBody) bool { return st.Phase == PhaseStarting })
	for _, w := range []*lobbyWatch{hw, jw} {
		if !waitFor(2*time.Second, func() bool { _, n, _, _, _ := w.lens(); return n == 1 }) {
			t.Fatal("START not relayed to every member")
		}
		if w.starts[0].Settings.DryRun != true {
			t.Fatalf("START settings %+v", w.starts[0].Settings)
		}
	}

	// Colors are locked now.
	joiner.SetColor(playercolor.Palette()[6].Color)
	if !waitFor(2*time.Second, func() bool { n, _, _, _, _ := jw.lens(); return n == 2 }) || jw.lastNotice().Code != NoticeColorsLocked {
		t.Fatal("a color change after Start was not refused")
	}

	if err := host.Go(""); err != nil {
		t.Fatal(err)
	}
	waitState(t, joiner, "in game", func(st LobbyStateBody) bool { return st.Phase == PhaseInGame })
	if !waitFor(2*time.Second, func() bool { _, _, n, _, _ := jw.lens(); return n == 1 }) {
		t.Fatal("GO not relayed")
	}

	// The joiner's app starts its pose pipeline: that player connection
	// takes the lobby color (rule 0) with no preference in its hello, and
	// the joiner is not listed twice.
	pose := connectPlayer(t, addr, "Joiner")
	if !waitFor(2*time.Second, func() bool { c, ok := pose.MyColor(); return ok && c == lobbyBlue }) {
		c, _ := pose.MyColor()
		t.Fatalf("the joiner's pose connection got %v, want its lobby color %v", c, lobbyBlue)
	}
	time.Sleep(50 * time.Millisecond)
	if st, _ := host.State(); len(st.Members) != 3 {
		t.Fatalf("members %+v, want Host, Joiner, Cli", st.Members)
	}

	// Lobby connections are not players.
	if srv.PlayerCount() != 3 || srv.LobbyCount() != 2 {
		t.Fatalf("PlayerCount %d LobbyCount %d, want 3 and 2", srv.PlayerCount(), srv.LobbyCount())
	}
	for _, rp := range cli.GetRemotePlayers() {
		if rp.Name == lobbyHostName {
			t.Fatalf("the host's lobby connection is in a player's roster: %+v", cli.GetRemotePlayers())
		}
	}

	// Chat both ways.
	if err := joiner.SendChat("hi from the lobby"); err != nil {
		t.Fatal(err)
	}
	if !waitFor(2*time.Second, func() bool { return contains(chats.snapshot(), "Joiner|hi from the lobby") }) {
		t.Fatalf("CLI chats %q", chats.snapshot())
	}
	if !waitFor(2*time.Second, func() bool { return hw.hasChat("Joiner: hi from the lobby") }) {
		t.Fatal("lobby chat did not reach the other lobby member")
	}
	if err := cli.SendChat("hi from the CLI"); err != nil {
		t.Fatal(err)
	}
	if !waitFor(2*time.Second, func() bool { return jw.hasChat("Cli: hi from the CLI") && hw.hasChat("Cli: hi from the CLI") }) {
		t.Fatal("a player's chat did not reach the lobby")
	}
	if jw.hasChat("Joiner: hi from the lobby") {
		t.Error("the sender's own lobby connection got its chat back")
	}

	// A joiner asks the host to allow merging.
	joiner.RequestMerge()
	if !waitFor(2*time.Second, func() bool { _, _, _, n, _ := hw.lens(); return n == 1 }) || hw.requests[0].From != "Joiner" {
		t.Fatal("REQUEST did not reach the host")
	}
}

// TestLobbyWelcomeBeforePlayerListState: a member's HELLO that also picks
// up a player-list change the lobby had not seen yet (a player connection
// that joined a moment before, its playersChanged still to come) gets its
// WELCOME before any STATE. The STATE for the player-list change used to be
// queued to the new member ahead of its WELCOME, with the HELLO's STATE
// merged into it; the client drops its STATE on WELCOME, so it had none
// until the next change. That is how TestLobbyKickSticks and
// TestLobbyKickClosesLobbyAndPlayerConns timed out waiting for three rows
// that the host's lobby client had in fact received.
func TestLobbyWelcomeBeforePlayerListState(t *testing.T) {
	srv, addr := listenLobby(t, time.Minute)
	first := NewLobbyClient("First", LobbyHelloBody{})
	runLobby(t, first, addr)
	waitState(t, first, "first", func(st LobbyStateBody) bool { return len(st.Members) == 1 })

	// A player the lobby has not been told about: in s.players, no
	// playersChanged yet.
	pc, peer := net.Pipe()
	t.Cleanup(func() { pc.Close(); peer.Close() })
	srv.mu.Lock()
	srv.players[200] = &Player{ID: 200, Name: "Unseen", Conn: pc, Color: playercolor.Vanilla()}
	srv.mu.Unlock()

	second := NewLobbyClient("Second", LobbyHelloBody{})
	runLobby(t, second, addr)
	waitState(t, second, "second sees everyone", func(st LobbyStateBody) bool {
		_, ok := rowOf(st, "Unseen")
		return ok && len(st.Members) == 3
	})
}

// TestLobbyReconnectReclaimsSlot: a dropped lobby connection reconnects
// with its token into the held slot (same member, color, ready); a slot not
// reclaimed within the hold is dropped.
func TestLobbyReconnectReclaimsSlot(t *testing.T) {
	_, addr := listenLobby(t, 400*time.Millisecond)
	host := NewLobbyClient(lobbyHostName, LobbyHelloBody{})
	runLobby(t, host, addr)
	joiner := NewLobbyClient("Joiner", LobbyHelloBody{Color: "blue"})
	runLobby(t, joiner, addr)
	waitState(t, host, "two members", func(st LobbyStateBody) bool { return len(st.Members) == 2 })
	joiner.SetReady(true)
	waitState(t, host, "ready", func(st LobbyStateBody) bool { r, _ := rowOf(st, "Joiner"); return r.Ready })
	// The joiner's SET can go out before its own read loop has taken the
	// WELCOME (Run's Connect is still waiting for it), so wait for that.
	if !waitFor(5*time.Second, func() bool { _, ok := joiner.Welcome(); return ok }) {
		t.Fatal("the joiner has no WELCOME")
	}
	w0, _ := joiner.Welcome()

	joiner.Client().Disconnect() // Run reconnects with the token
	waitState(t, host, "back", func(st LobbyStateBody) bool {
		r, _ := rowOf(st, "Joiner")
		return r.Ready && !r.Away && r.Color == lobbyBlue.Hex() && r.ID == w0.Member
	})
	if w1, _ := joiner.Welcome(); w1.Member != w0.Member || w1.Token != w0.Token {
		t.Fatalf("reclaim WELCOME %+v, was %+v", w1, w0)
	}

	// Gone for good: the slot shows away, then expires.
	other := NewLobbyClient("Other", LobbyHelloBody{})
	if err := other.Connect(context.Background(), addr); err != nil {
		t.Fatal(err)
	}
	waitState(t, host, "other", func(st LobbyStateBody) bool { _, ok := rowOf(st, "Other"); return ok })
	other.Disconnect()
	waitState(t, host, "other away", func(st LobbyStateBody) bool { r, _ := rowOf(st, "Other"); return r.Away })
	waitState(t, host, "other expired", func(st LobbyStateBody) bool { _, ok := rowOf(st, "Other"); return !ok })
}

// TestLobbyOnLink: Run reports a failed attempt (down, with the error),
// the connect (up), a dropped connection (down, nil) and the reconnect
// (up), and nothing once its ctx has ended.
func TestLobbyOnLink(t *testing.T) {
	type ev struct {
		up  bool
		err bool
	}
	var mu sync.Mutex
	var evs []ev
	record := func(up bool, err error) { mu.Lock(); evs = append(evs, ev{up, err != nil}); mu.Unlock() }
	snapshot := func() []ev { mu.Lock(); defer mu.Unlock(); return append([]ev(nil), evs...) }

	// Nobody listens yet: the first attempt fails.
	fastNetTimers(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	l := NewLobbyClient("Joiner", LobbyHelloBody{})
	l.OnLink = record
	r := runLobby(t, l, addr)
	if !waitFor(5*time.Second, func() bool { e := snapshot(); return len(e) > 0 }) {
		t.Fatal("no OnLink for a failed attempt")
	}
	if e := snapshot()[0]; e.up || !e.err {
		t.Fatalf("first OnLink = %+v, want down with an error", e)
	}
	r.cancel()
	<-r.done
	r.done <- nil

	_, addr = listenLobby(t, 2*time.Second)
	mu.Lock()
	evs = nil
	mu.Unlock()
	l = NewLobbyClient("Joiner", LobbyHelloBody{})
	l.OnLink = record
	r = runLobby(t, l, addr)
	if !waitFor(5*time.Second, func() bool { e := snapshot(); return len(e) == 1 && e[0].up }) {
		t.Fatalf("OnLink after connecting = %+v, want one up", snapshot())
	}
	l.Client().Disconnect()
	if !waitFor(5*time.Second, func() bool { e := snapshot(); return len(e) >= 3 && e[len(e)-1].up }) {
		t.Fatalf("OnLink after a drop = %+v, want down then up", snapshot())
	}
	if e := snapshot()[1]; e.up || e.err {
		t.Fatalf("OnLink for the drop = %+v, want down without an error", e)
	}
	n := len(snapshot())
	r.cancel()
	<-r.done
	r.done <- nil
	time.Sleep(50 * time.Millisecond)
	if got := len(snapshot()); got != n {
		t.Fatalf("OnLink called after Run's ctx ended: %+v", snapshot())
	}

	// A fatal answer (a pre-lobby host's RejectRole, a protocol-1 host's
	// version mismatch) ends Run and is no link change: the UI must not
	// show "Can't reach the host" for it.
	for _, srv := range []func(net.Conn){preLobbyServer, v030Server} {
		mu.Lock()
		evs = nil
		mu.Unlock()
		l = NewLobbyClient("Joiner", LobbyHelloBody{})
		l.OnLink = record
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := l.Run(ctx, serveOnce(t, srv), nil)
		cancel()
		if !errors.Is(err, ErrFatal) {
			t.Fatalf("Run = %v, want a fatal error", err)
		}
		if e := snapshot(); len(e) != 0 {
			t.Fatalf("OnLink for a fatal answer = %+v, want none", e)
		}
	}
}

// TestLobbyHostRestart: the host's server restarts. Both lobby clients
// reconnect on their own to a fresh lobby (phase lobby, nobody ready), the
// host is host again and each keeps the color it had.
func TestLobbyHostRestart(t *testing.T) {
	fastNetTimers(t)
	saved := LobbyHoldTimeout
	t.Cleanup(func() { LobbyHoldTimeout = saved })
	start := func(port int) *Server {
		srv := NewServer(port)
		srv.ListenHost = "127.0.0.1"
		srv.lobby.local = func(_ net.Addr, name string) bool { return name == lobbyHostName }
		if err := srv.Start(); err != nil {
			t.Fatalf("server start: %v", err)
		}
		return srv
	}
	srv := start(0)
	port := srv.Addr().(*net.TCPAddr).Port
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	host := NewLobbyClient(lobbyHostName, LobbyHelloBody{Color: "purple"})
	runLobby(t, host, addr)
	joiner := NewLobbyClient("Joiner", LobbyHelloBody{Color: "orange"})
	runLobby(t, joiner, addr)
	waitState(t, joiner, "two", func(st LobbyStateBody) bool { return len(st.Members) == 2 })
	joiner.SetReady(true)
	host.Start()
	waitState(t, joiner, "starting", func(st LobbyStateBody) bool { return st.Phase == PhaseStarting })
	if !waitFor(5*time.Second, func() bool { _, ok := host.Welcome(); return ok }) {
		t.Fatal("the host has no WELCOME")
	}
	w0, _ := host.Welcome()

	srv.Stop()
	var srv2 *Server
	if !waitFor(5*time.Second, func() bool {
		srv2 = NewServer(port)
		srv2.ListenHost = "127.0.0.1"
		srv2.lobby.local = func(_ net.Addr, name string) bool { return name == lobbyHostName }
		return srv2.Start() == nil
	}) {
		t.Fatal("could not restart the server on its port")
	}
	t.Cleanup(srv2.Stop)

	st := waitState(t, joiner, "fresh lobby", func(st LobbyStateBody) bool { return len(st.Members) == 2 && st.Phase == PhaseLobby })
	for _, m := range st.Members {
		if m.Ready {
			t.Errorf("%s is ready in the restarted lobby", m.Name)
		}
	}
	if colorOfRow(t, st, lobbyHostName) != playercolor.Palette()[3].Color || colorOfRow(t, st, "Joiner") != playercolor.Palette()[4].Color {
		t.Fatalf("colors after restart: %+v", st.Members)
	}
	waitFor(2*time.Second, func() bool { w, _ := host.Welcome(); return w.Token != w0.Token })
	if w, _ := host.Welcome(); !w.Host || w.Token == w0.Token {
		t.Fatalf("host WELCOME after restart %+v (was %+v)", w, w0)
	}
}

// TestLobbyNameTaken: a second member with the same name in another case
// is refused with a suggestion, fatally.
func TestLobbyNameTaken(t *testing.T) {
	_, addr := listenLobby(t, time.Minute)
	a := NewLobbyClient("Link", LobbyHelloBody{})
	runLobby(t, a, addr)
	waitState(t, a, "joined", func(st LobbyStateBody) bool { return len(st.Members) == 1 })

	b := NewLobbyClient("LINK", LobbyHelloBody{})
	bw := watchLobby(b)
	err := runLobby(t, b, addr).result(t)
	var ne *LobbyNoticeError
	if !errors.As(err, &ne) || ne.Notice.Code != NoticeNameTaken || ne.Notice.Suggest != "LINK (2)" || !errors.Is(err, ErrFatal) {
		t.Fatalf("Run = %v, want a fatal name_taken suggesting %q", err, "LINK (2)")
	}
	// OnNotice ran before Run returned (the controller publishes the
	// notice, with its suggestion, before the session ends).
	if notices, _, _, _, _ := bw.lens(); notices != 1 || bw.lastNotice().Suggest != "LINK (2)" {
		t.Fatalf("OnNotice had not run when Run returned (%d notices)", notices)
	}
	// Player connections keep today's rule: two under one name are fine.
	connectPlayer(t, addr, "Link")
	connectPlayer(t, addr, "Link")
}

// TestLobbyKickClosesLobbyAndPlayerConns: the host's KICK ends the member's
// lobby connection (its Run returns the kick) and its player connections.
func TestLobbyKickClosesLobbyAndPlayerConns(t *testing.T) {
	srv, addr := listenLobby(t, time.Minute)
	host := NewLobbyClient(lobbyHostName, LobbyHelloBody{})
	runLobby(t, host, addr)
	bad := NewLobbyClient("Griefer", LobbyHelloBody{})
	run := runLobby(t, bad, addr)
	p := connectPlayer(t, addr, "Griefer")
	cli := connectPlayer(t, addr, "Lurker")
	// Griefer's player connection can be listed before its lobby member
	// joins, as a CLI row of that name with an ID of its own: wait for the
	// member's row, whose ID the kick below is for.
	st := waitState(t, host, "three", func(st LobbyStateBody) bool {
		g, _ := rowOf(st, "Griefer")
		return len(st.Members) == 3 && g.Via == ViaApp
	})
	gm, _ := rowOf(st, "Griefer")

	host.Kick(gm.ID)
	err := run.result(t)
	var ne *LobbyNoticeError
	if !errors.As(err, &ne) || ne.Notice.Code != NoticeKicked {
		t.Fatalf("kicked Run = %v", err)
	}
	if !waitFor(3*time.Second, func() bool { return !p.IsConnected() }) {
		t.Fatal("the kicked member's player connection is still up")
	}
	// Kicking a CLI row closes its player connections.
	lr, _ := rowOf(waitState(t, host, "lurker", func(st LobbyStateBody) bool { _, ok := rowOf(st, "Lurker"); return ok }), "Lurker")
	host.Kick(lr.ID)
	if !waitFor(3*time.Second, func() bool { return !cli.IsConnected() }) {
		t.Fatal("the kicked CLI player is still connected")
	}
	waitState(t, host, "host alone", func(st LobbyStateBody) bool { return len(st.Members) == 1 })
	if srv.PlayerCount() != 0 {
		t.Fatalf("PlayerCount %d after the kicks", srv.PlayerCount())
	}
}

// TestLobbyKickSticks: a kick is not just a disconnect. The kicked CLI
// player's reconnect loop (as CLI join runs it) ends with a fatal refusal
// instead of coming back as a via:"cli" row, a kicked app's fresh lobby
// client is refused, and so are their player and co-op connections.
func TestLobbyKickSticks(t *testing.T) {
	srv, addr := listenLobby(t, time.Minute)
	srv.SetCoop(newCoopRecorder())
	host := NewLobbyClient(lobbyHostName, LobbyHelloBody{})
	runLobby(t, host, addr)

	cli := NewClient("Lurker")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cliDone := make(chan error, 1)
	go func() {
		live := func(ctx context.Context) error {
			for ctx.Err() == nil && cli.IsConnected() {
				time.Sleep(10 * time.Millisecond)
			}
			return nil
		}
		cliDone <- ReconnectLoop(ctx, func(ctx context.Context) error { return cli.ConnectContext(ctx, addr) },
			func(ctx context.Context) error { return RunSession(ctx, cli.Disconnect, live) }, NewBackoff(), SleepCtx)
	}()
	bad := NewLobbyClient("Griefer", LobbyHelloBody{})
	badRun := runLobby(t, bad, addr)
	st := waitState(t, host, "three", func(st LobbyStateBody) bool { return len(st.Members) == 3 })
	lr, _ := rowOf(st, "Lurker")
	gm, _ := rowOf(st, "Griefer")

	host.Kick(lr.ID)
	select {
	case err := <-cliDone:
		var rej *RejectError
		if !errors.As(err, &rej) || rej.Code != RejectBadName || !errors.Is(err, ErrFatal) || !strings.Contains(err.Error(), "removed you") {
			t.Fatalf("kicked CLI player's reconnect loop = %v, want a fatal refusal", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the kicked CLI player's reconnect loop is still going")
	}
	host.Kick(gm.ID)
	if err := badRun.result(t); !errors.Is(err, ErrFatal) {
		t.Fatalf("kicked member's Run = %v", err)
	}
	waitState(t, host, "host alone", func(st LobbyStateBody) bool { return len(st.Members) == 1 })

	// A fresh app (no token) under the kicked name, any case.
	again := NewLobbyClient("GRIEFER", LobbyHelloBody{})
	err := again.Connect(context.Background(), addr)
	var ne *LobbyNoticeError
	if !errors.As(err, &ne) || ne.Notice.Code != NoticeKicked {
		t.Fatalf("kicked name's new lobby client: %v", err)
	}
	if err := NewClient("griefer").Connect(addr); !errors.Is(err, ErrFatal) {
		t.Fatalf("kicked name's player connection: %v", err)
	}
	if err := NewCoopClient("Lurker", 0).Connect(addr); !errors.Is(err, ErrFatal) || !strings.Contains(err.Error(), "removed you") {
		t.Fatalf("kicked name's co-op connection: %v", err)
	}
	if srv.PlayerCount() != 0 {
		t.Fatalf("PlayerCount %d", srv.PlayerCount())
	}
	// Other names are unaffected.
	connectPlayer(t, addr, "Friend")
}

// TestLobbyReclaimReplaysStartAndGo: a member whose lobby connection drops
// in starting, or in game, and reclaims its slot gets START (and GO with
// the world) again, so its app still launches; a late joiner gets both.
func TestLobbyReclaimReplaysStartAndGo(t *testing.T) {
	_, addr := listenLobby(t, time.Minute)
	host := NewLobbyClient(lobbyHostName, LobbyHelloBody{})
	runLobby(t, host, addr)
	joiner := NewLobbyClient("Joiner", LobbyHelloBody{})
	jw := watchLobby(joiner)
	runLobby(t, joiner, addr)
	waitState(t, host, "two", func(st LobbyStateBody) bool { return len(st.Members) == 2 })
	host.SetSettings(LobbySettingsBody{Coop: CoopNew})
	waitState(t, joiner, "co-op", func(st LobbyStateBody) bool { return st.Settings.Coop == CoopNew })
	host.Start()
	waitFor(5*time.Second, func() bool { _, s, _, _, _ := jw.lens(); return s == 1 })

	w0, _ := joiner.Welcome()
	joiner.Client().Disconnect()
	if !waitFor(5*time.Second, func() bool { _, s, _, _, _ := jw.lens(); return s == 2 }) {
		t.Fatal("no START after reclaiming the slot in starting")
	}
	if w, _ := joiner.Welcome(); w.Member != w0.Member {
		t.Fatalf("not the same slot: %+v, was %+v", w, w0)
	}
	jw.mu.Lock()
	settings := jw.starts[1].Settings
	jw.mu.Unlock()
	if settings.Coop != CoopNew {
		t.Fatalf("replayed START settings %+v", settings)
	}

	host.Go("0123456789abcdef")
	waitFor(5*time.Second, func() bool { _, _, g, _, _ := jw.lens(); return g == 1 })
	joiner.Client().Disconnect()
	if !waitFor(5*time.Second, func() bool { _, s, g, _, _ := jw.lens(); return s == 3 && g == 2 }) {
		_, s, g, _, _ := jw.lens()
		t.Fatalf("after reclaiming in game: %d STARTs, %d GOs", s, g)
	}
	jw.mu.Lock()
	world := jw.gos[1].World
	jw.mu.Unlock()
	if world != "0123456789abcdef" {
		t.Fatalf("replayed GO world %q", world)
	}

	late := NewLobbyClient("Late", LobbyHelloBody{})
	lw := watchLobby(late)
	runLobby(t, late, addr)
	if !waitFor(5*time.Second, func() bool { _, s, g, _, _ := lw.lens(); return s == 1 && g == 1 }) {
		t.Fatal("a late joiner in game did not get START and GO")
	}
	waitState(t, late, "in game", func(st LobbyStateBody) bool { return st.Phase == PhaseInGame })
}

// TestLobbyFloodKeepsSlowMemberConnected: one member flips ready as fast
// as it can. STATE is merged per connection (one waiting at most) and the
// flood is rate-limited, so a member reading slowly stays connected, and
// the flooder hears slow_down.
func TestLobbyFloodKeepsSlowMemberConnected(t *testing.T) {
	srv, addr := listenLobby(t, time.Minute)
	host := NewLobbyClient(lobbyHostName, LobbyHelloBody{})
	runLobby(t, host, addr)

	// The slow member is a pipe: the server's writes wait for its reads.
	cli, sconn := net.Pipe()
	t.Cleanup(func() { cli.Close() })
	go srv.handlePlayer(sconn)
	var wmu sync.Mutex
	send := func(typ byte, data []byte) error {
		wmu.Lock()
		defer wmu.Unlock()
		_, err := cli.Write(frame(typ, data))
		return err
	}
	readErr := make(chan error, 1)
	var frames sync.WaitGroup
	frames.Add(1)
	go func() {
		defer frames.Done()
		for {
			msg, err := ReadMessage(cli)
			if err != nil {
				readErr <- err
				return
			}
			if msg.Type == MsgPing {
				go send(MsgPong, msg.Data)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	hello, _ := EncodeLobby(LobbyHello, LobbyHelloBody{})
	for _, f := range []struct {
		typ  byte
		data []byte
	}{{MsgJoin, []byte("Slow")}, {MsgHello, HelloRoleMessage(ProtocolVersion, RoleLobby, LobbyCapJSON)}, {MsgLobby, hello}} {
		if err := send(f.typ, f.data); err != nil {
			t.Fatal(err)
		}
	}
	waitState(t, host, "slow member", func(st LobbyStateBody) bool { _, ok := rowOf(st, "Slow"); return ok })

	spam := NewLobbyClient("Spam", LobbyHelloBody{})
	sw := watchLobby(spam)
	runLobby(t, spam, addr)
	waitState(t, spam, "spammer", func(st LobbyStateBody) bool { return len(st.Members) == 3 })
	for i := 0; i < 5000; i++ {
		if err := spam.SetReady(i%2 == 0); err != nil {
			t.Fatalf("toggle %d: %v", i, err)
		}
	}
	// The flooder's slow_down is on its way while the flood drains.
	sawSlowDown := waitFor(5*time.Second, func() bool {
		sw.mu.Lock()
		defer sw.mu.Unlock()
		for _, n := range sw.notices {
			if n.Code == NoticeSlowDown {
				return true
			}
		}
		return false
	})
	time.Sleep(time.Second)
	select {
	case err := <-readErr:
		t.Fatalf("the slow member was disconnected: %v", err)
	default:
	}
	st := srv.Lobby().State()
	if r, ok := rowOf(st, "Slow"); !ok || r.Away {
		t.Fatalf("slow member's row %+v (present %v)", r, ok)
	}
	if r, ok := rowOf(st, "Spam"); !ok || r.Away {
		t.Fatalf("the flooder's own row %+v (present %v)", r, ok)
	}
	if !sawSlowDown {
		t.Error("the flooder never got slow_down")
	}
	cli.Close()
	frames.Wait()
}

// TestLobbyMemberAndCLIShareAColor: a lobby member and a CLI player that
// ask for the same color, at the same moment, both get it (owner decision
// 2026-10-16, docs/10 §4).
func TestLobbyMemberAndCLIShareAColor(t *testing.T) {
	for i := 0; i < 5; i++ {
		srv, addr := listenLobby(t, time.Minute)
		app := NewLobbyClient("App", LobbyHelloBody{Color: "red"})
		p := NewClient("Cli")
		p.SetColorPreference(&lobbyRed)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); app.Connect(context.Background(), addr) }()
		go func() { defer wg.Done(); p.Connect(addr) }()
		wg.Wait()
		st := waitState(t, app, "both", func(st LobbyStateBody) bool { _, ok := rowOf(st, "Cli"); return ok && len(st.Members) == 2 })
		a, c := colorOfRow(t, st, "App"), colorOfRow(t, st, "Cli")
		if a != lobbyRed || c != lobbyRed {
			t.Fatalf("round %d: member %v, CLI player %v; both asked for red", i, a, c)
		}
		// The CLI player learns its own color from the player list the
		// server sends after its welcome: Connect returns on the welcome,
		// and the lobby can list the player before that list is written
		// or read.
		waitFor(5*time.Second, func() bool { _, ok := p.MyColor(); return ok })
		if mc, ok := p.MyColor(); !ok || mc != lobbyRed {
			t.Fatalf("round %d: the CLI player's own color %v (%v), want red", i, mc, ok)
		}
		app.Disconnect()
		p.Disconnect()
		srv.Stop()
	}
}

// --- compatibility -----------------------------------------------------------

// preLobbyServer mimics the server of 331e772 (protocol 2, co-op, no
// lobby): it refuses any role but player and co-op with RejectRole,
// "unknown connection role N". Its welcome carries this build's
// ProtocolVersion (3 since enemy sync), so it models that server's role
// handling only: a real protocol-2 host now refuses at the version check.
func preLobbyServer(conn net.Conn) {
	join, err := ReadMessage(conn)
	if err != nil || join.Type != MsgJoin {
		return
	}
	hello, err := ReadMessage(conn)
	if err != nil {
		return
	}
	_, role, _, err := ParseHelloRole(hello.Data)
	if err != nil {
		return
	}
	if role > RoleCoop {
		WriteMessage(conn, MsgReject, RejectMessage(RejectRole,
			fmt.Sprintf("unknown connection role %d; both players need the same ww-multiplayer version", role)))
		drain(conn)
		return
	}
	WriteMessage(conn, MsgWelcome, WelcomeMessage(1, 1))
	drain(conn)
}

// TestLobbyLegacyHostFallback: against a host from before the lobby, the
// lobby client stops at once with RejectRole (IsLegacyHost), which the app
// turns into "join directly"; a protocol-1 host is a version error instead.
func TestLobbyLegacyHostFallback(t *testing.T) {
	l := NewLobbyClient("App", LobbyHelloBody{})
	err := l.Connect(context.Background(), serveOnce(t, preLobbyServer))
	if !IsLegacyHost(err) || !errors.Is(err, ErrFatal) || !strings.Contains(err.Error(), "unknown connection role 2") {
		t.Fatalf("Connect = %v, want a fatal RejectRole", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = NewLobbyClient("App", LobbyHelloBody{}).Run(ctx, serveOnce(t, preLobbyServer), nil)
	if !IsLegacyHost(err) || ctx.Err() != nil {
		t.Fatalf("Run = %v (ctx %v), want RejectRole before the timeout", err, ctx.Err())
	}
	// The same app's player connection works against that host.
	if err := NewClient("App").Connect(serveOnce(t, preLobbyServer)); err != nil {
		t.Fatalf("player connect to a pre-lobby host: %v", err)
	}
	err = NewLobbyClient("App", LobbyHelloBody{}).Connect(context.Background(), serveOnce(t, v030Server))
	if IsLegacyHost(err) || !errors.Is(err, ErrFatal) {
		t.Fatalf("against protocol 1: %v, want a version error, not the lobby fallback", err)
	}
}

// TestLobbyHelloWithoutJSONCapRefused: a lobby hello must say its bodies
// are JSON.
func TestLobbyHelloWithoutJSONCapRefused(t *testing.T) {
	srv, addr := listenLobby(t, time.Minute)
	conn := dialRaw(t, addr)
	conn.Write(append(frame(MsgJoin, []byte("x")), frame(MsgHello, HelloRoleMessage(ProtocolVersion, RoleLobby, 0))...))
	expectReject(t, conn, RejectRole)
	if srv.LobbyCount() != 0 {
		t.Fatal("refused lobby connection counted")
	}
}

// TestLobbyMixedVersions: a player client of the previous release (its
// exact bytes: join plus the 2-byte hello) on a server with a live lobby
// sees only the frames it always did: welcome, player list (with the color
// trailer), relays and pings, never a 'B'. A 'B' it might send on a player
// connection is dropped. The lobby lists it as a CLI player.
func TestLobbyMixedVersions(t *testing.T) {
	srv, addr := listenLobby(t, time.Minute)
	host := NewLobbyClient(lobbyHostName, LobbyHelloBody{Color: "red"})
	runLobby(t, host, addr)
	waitState(t, host, "host", func(st LobbyStateBody) bool { return len(st.Members) == 1 })

	conn := dialRaw(t, addr)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	rawJoin(t, conn, "OldCli", ProtocolVersion)
	if msg := mustRead(t, conn); msg.Type != MsgWelcome || checkWelcome(msg) != nil {
		t.Fatalf("first frame %q", msg.Type)
	}
	msg := mustRead(t, conn)
	entries, err := ParsePlayerList(msg.Data)
	if msg.Type != MsgPlayerList || err != nil || len(entries) != 1 || entries[0].Name != "OldCli" || !entries[0].HasColor {
		t.Fatalf("second frame %q %+v %v", msg.Type, entries, err)
	}
	if entries[0].Color != playercolor.Vanilla() {
		t.Fatalf("the old client (no preference) got %v, want green", entries[0].Color)
	}
	// A lobby-only frame on a player connection: dropped, connection kept.
	conn.Write(frame(MsgLobby, []byte("\x07{}")))
	st := waitState(t, host, "old client listed", func(st LobbyStateBody) bool { _, ok := rowOf(st, "OldCli"); return ok })
	if st.Phase != PhaseLobby {
		t.Fatalf("a 'B' on a player connection changed the phase: %+v", st)
	}
	host.SetReady(true)
	host.SendChat("hello old friend")
	host.Start()
	waitState(t, host, "starting", func(st LobbyStateBody) bool { return st.Phase == PhaseStarting })

	sawChat := false
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		conn.SetReadDeadline(deadline)
		msg, err := ReadMessage(conn)
		if err != nil {
			break
		}
		switch msg.Type {
		case MsgChat:
			from, text, _ := ParseChatRelay(msg.Data)
			sawChat = sawChat || (from == lobbyHostName && text == "hello old friend")
		case MsgPing:
			WriteMessage(conn, MsgPong, msg.Data)
		case MsgPlayerList, MsgPong:
		default:
			t.Fatalf("the old client got a %q frame", msg.Type)
		}
	}
	if !sawChat {
		t.Error("the old client did not get the lobby's chat")
	}
	if srv.PlayerCount() != 1 {
		t.Fatalf("PlayerCount %d", srv.PlayerCount())
	}
}

// --- co-op start order -------------------------------------------------------

// TestCoopEarlyJoinNotRefusedPermanently is the co-op start-order hazard:
// with the lobby's host on co-op but its world not open yet, a CLI
// join --coop used to meet a server with no CoopHandler and get RejectRole,
// which is fatal, so it never had co-op. Now its hello is held until
// SetCoop, or, when the hold runs out, closed without a reject so the
// client retries.
func TestCoopEarlyJoinNotRefusedPermanently(t *testing.T) {
	srv, addr := listenLobby(t, time.Minute)
	srv.coopHold = 150 * time.Millisecond // before any co-op connection

	host := NewLobbyClient(lobbyHostName, LobbyHelloBody{})
	runLobby(t, host, addr)
	waitState(t, host, "joined", func(st LobbyStateBody) bool { return len(st.Members) == 1 })
	host.SetSettings(LobbySettingsBody{Coop: CoopNew})
	if !waitFor(2*time.Second, srv.Lobby().ExpectsCoop) {
		t.Fatal("lobby does not expect co-op")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sessions := make(chan struct{}, 4)
	done := make(chan error, 1)
	go func() {
		done <- RunCoop(ctx, NewCoopClient("Cli", 0), addr, nil, func(ctx context.Context, _ func([]byte) error) error {
			sessions <- struct{}{}
			<-ctx.Done()
			return nil
		})
	}()
	// Several holds run out meanwhile; RunCoop keeps going.
	time.Sleep(600 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("RunCoop gave up before the world opened: %v", err)
	default:
	}
	rec := newCoopRecorder()
	srv.SetCoop(rec)
	select {
	case <-sessions:
	case <-time.After(10 * time.Second):
		t.Fatal("the co-op connection never got through after SetCoop")
	}
	if at, _ := rec.counts(); at != 1 {
		t.Fatalf("attached %d", at)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("RunCoop = %v", err)
	}
}

// TestCoopHelloHeldUntilSetCoop: a hello that arrives while the lobby
// expects co-op waits for SetCoop and then attaches on the same connection.
func TestCoopHelloHeldUntilSetCoop(t *testing.T) {
	srv, addr := listenLobby(t, time.Minute)
	host := NewLobbyClient(lobbyHostName, LobbyHelloBody{})
	runLobby(t, host, addr)
	waitState(t, host, "joined", func(st LobbyStateBody) bool { return len(st.Members) == 1 })
	host.SetSettings(LobbySettingsBody{Coop: CoopNew})
	if !waitFor(5*time.Second, srv.Lobby().ExpectsCoop) {
		t.Fatal("lobby does not expect co-op")
	}

	got := make(chan error, 1)
	go func() { got <- NewCoopClient("Cli", 0).Connect(addr) }()
	time.Sleep(200 * time.Millisecond)
	select {
	case err := <-got:
		t.Fatalf("connect returned during the hold: %v", err)
	default:
	}
	srv.SetCoop(newCoopRecorder())
	if err := <-got; err != nil {
		t.Fatalf("held co-op connect = %v", err)
	}
	if srv.CoopCount() != 1 {
		t.Fatalf("CoopCount %d", srv.CoopCount())
	}
	// SetCoop(nil) turns co-op off again: a new co-op hello outside the
	// lobby's expectation is refused as before.
	srv.SetCoop(nil)
	host.Start()
	host.Go("")
	if !waitFor(5*time.Second, func() bool { return !srv.Lobby().ExpectsCoop() }) {
		t.Fatal("the lobby still expects co-op after GO")
	}
	err := NewCoopClient("Late", 0).Connect(addr)
	if !errors.Is(err, ErrFatal) || !strings.Contains(err.Error(), "not running co-op") {
		t.Fatalf("after SetCoop(nil) and GO: %v", err)
	}
}

// TestCoopHoldReleasedByStop: Stop ends a co-op hello held for SetCoop at
// once, without a reject, so the client retries (against a restarted
// server) instead of waiting out the hold on a dead one.
func TestCoopHoldReleasedByStop(t *testing.T) {
	srv, addr := listenLobby(t, time.Minute)
	srv.coopHold = 30 * time.Second
	host := NewLobbyClient(lobbyHostName, LobbyHelloBody{})
	runLobby(t, host, addr)
	waitState(t, host, "joined", func(st LobbyStateBody) bool { return len(st.Members) == 1 })
	host.SetSettings(LobbySettingsBody{Coop: CoopNew})
	if !waitFor(2*time.Second, srv.Lobby().ExpectsCoop) {
		t.Fatal("lobby does not expect co-op")
	}
	got := make(chan error, 1)
	go func() { got <- NewCoopClient("Cli", 0).Connect(addr) }()
	time.Sleep(200 * time.Millisecond)
	stopped := time.Now()
	srv.Stop()
	select {
	case err := <-got:
		if err == nil || errors.Is(err, ErrFatal) {
			t.Fatalf("held connect after Stop = %v, want a transient error", err)
		}
		if d := time.Since(stopped); d > 2*time.Second {
			t.Fatalf("the held hello ended %v after Stop", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not release the held co-op hello")
	}
}

// TestCoopHoldUnderClientReadTimeout pins the hold under the client's
// default 15 s handshake read deadline: the held client is waiting for its
// welcome that long, so a longer hold would end on the client's own
// timeout instead of the server's close.
func TestCoopHoldUnderClientReadTimeout(t *testing.T) {
	if coopHoldTimeout >= 15*time.Second {
		t.Fatalf("coopHoldTimeout %v is not under the client's default 15 s read deadline", coopHoldTimeout)
	}
}

// --- server path fuzzing -------------------------------------------------------

// FuzzServerHandleLobby runs a lobby connection's whole server path (hello
// with the JSON cap, HELLO, sub-messages, chat, teardown) over an arbitrary
// client stream. It must not panic, and nothing may outlive the stream: no
// lobby connection, no player, and (with no hold) no member.
func FuzzServerHandleLobby(f *testing.F) {
	hello, _ := EncodeLobby(LobbyHello, LobbyHelloBody{Color: "red"})
	set, _ := EncodeLobby(LobbySet, LobbySetBody{Ready: boolp(true), Color: strp("blue")})
	var stream []byte
	stream = append(stream, frame(MsgLobby, hello)...)
	stream = append(stream, frame(MsgLobby, set)...)
	stream = append(stream, frame(MsgChat, []byte("hi"))...)
	stream = append(stream, frame(MsgLobby, []byte("\x07{}"))...)
	stream = append(stream, frame(MsgPing, PingPayload(1))...)
	stream = append(stream, frame(MsgLobby, []byte("\x0a{\"member\":1}"))...)
	f.Add("alice", stream)
	f.Add("bad\nname", stream)
	f.Add("bob", []byte{MsgLobby, 0, 0})
	f.Add("bob", frame(MsgLobby, []byte("\x04{\"color\":\"red\"}")))
	f.Fuzz(func(t *testing.T, name string, stream []byte) {
		if len(name) > MaxPayload {
			return
		}
		in := append(frame(MsgJoin, []byte(name)), frame(MsgHello, HelloRoleMessage(ProtocolVersion, RoleLobby, LobbyCapJSON))...)
		in = append(in, stream...)
		srv := NewServer(0)
		srv.lobby.m.hold = 0
		srv.lobby.local = func(net.Addr, string) bool { return true }
		srv.handlePlayer(newScriptConn(in))
		if srv.PlayerCount() != 0 || srv.CoopCount() != 0 || srv.LobbyCount() != 0 {
			t.Fatalf("PlayerCount=%d CoopCount=%d LobbyCount=%d after the stream", srv.PlayerCount(), srv.CoopCount(), srv.LobbyCount())
		}
		if st := srv.Lobby().State(); len(st.Members) != 0 || st.Phase != PhaseLobby {
			t.Fatalf("lobby after the stream: %+v", st)
		}
	})
}
