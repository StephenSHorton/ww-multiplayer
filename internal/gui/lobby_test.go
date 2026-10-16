package gui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/config"
	"github.com/StephenSHorton/ww-multiplayer/internal/network"
	"github.com/StephenSHorton/ww-multiplayer/internal/playercolor"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
)

func TestShareAddrs(t *testing.T) {
	ips := []string{"203.0.113.9", "100.101.2.3", "26.12.34.56", "192.168.1.42", "10.0.0.7", "172.20.1.1", "172.32.0.1", "100.128.0.1"}
	got := shareAddrs(ips, 0)
	want := []ShareAddrView{
		{"192.168.1.42", "•••.•••.•••.42", "lan", "LAN"}, {"10.0.0.7", "•••.•••.•••.7", "lan", "LAN"}, {"172.20.1.1", "•••.•••.•••.1", "lan", "LAN"},
		{"26.12.34.56", "•••.•••.•••.56", "radmin", "Radmin VPN"},
		{"100.101.2.3", "•••.•••.•••.3", "tailscale", "Tailscale"},
		{"203.0.113.9", "•••.•••.•••.9", "other", "Other"}, {"172.32.0.1", "•••.•••.•••.1", "other", "Other"}, {"100.128.0.1", "•••.•••.•••.1", "other", "Other"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("shareAddrs =\n%v\nwant\n%v", got, want)
	}
	// The default port is left off, as the CLI's host prints them;
	// another is added.
	if got := shareAddrs([]string{"192.168.1.42"}, 25565); got[0].Addr != "192.168.1.42" {
		t.Fatalf("default port: %+v", got)
	}
	if got := shareAddrs([]string{"26.1.2.3"}, 25570); got[0].Addr != "26.1.2.3:25570" || got[0].Masked != "•••.•••.•••.3:25570" || got[0].Label != "Radmin VPN" {
		t.Fatalf("port 25570: %+v", got)
	}
	if got := shareAddrs(nil, 25565); got == nil || len(got) != 0 {
		t.Fatalf("no IPs = %#v, want an empty list (JSON [])", got)
	}
}

// ended is a session that ended on err.
func ended(role session.Role, err error) session.State {
	return session.State{Session: 7, Phase: session.Ended, Role: role, Name: "Medli", Addr: "192.168.1.42",
		Failed: err != nil, Err: err, EndReason: fmt.Sprint(err), Lobby: &session.Lobby{}}
}

func actionIDs(d *DialogView) string {
	var ids []string
	for _, a := range d.Actions {
		id := a.ID
		if a.Primary {
			id += "*"
		}
		ids = append(ids, id)
	}
	return strings.Join(ids, ",")
}

// TestEndDialogs is design §2.3's table: each cause a join or host ends
// on, the dialog it opens.
func TestEndDialogs(t *testing.T) {
	notice := func(code, suggest string) error {
		return fmt.Errorf("%w: %w", session.ErrReported, &network.LobbyNoticeError{Notice: network.LobbyNoticeBody{Code: code, Text: "the host says " + code, Suggest: suggest}})
	}
	reject := func(code byte) error {
		return &network.RejectError{Code: code, Reason: fmt.Sprintf("host refused the connection: reason %d", code)}
	}
	for _, c := range []struct {
		role    session.Role
		err     error
		kind    string
		actions string
		text    string // in Text
	}{
		{session.RoleJoin, reject(network.RejectVersion), dlgVersion, "downloads*,copy-version,ok", "you run v9.9.9"},
		{session.RoleJoin, notice(network.NoticeNameTaken, "Medli (2)"), dlgNameTaken, "join-suggested*,change-name", "already called “Medli”"},
		{session.RoleJoin, notice(network.NoticeNameTaken, ""), dlgNameTaken, "change-name*", "already called"},
		{session.RoleJoin, reject(network.RejectBadName), dlgBadName, "change-name*,ok", "refused your name"},
		{session.RoleJoin, notice(network.NoticeFull, ""), dlgFull, "ok*", "That session is full."},
		{session.RoleJoin, reject(network.RejectServerFull), dlgFull, "ok*", "That session is full."},
		{session.RoleJoin, notice(network.NoticeKicked, ""), dlgKicked, "ok*", "The host removed you"},
		{session.RoleJoin, notice("replaced", ""), dlgReplaced, "ok*", "another window"},
		{session.RoleHost, errors.New("server start: listen: address in use"), dlgHostFailed, "ok*", "pick another port"},
		{session.RoleJoin, errors.New("something else"), dlgFailed, "ok*", "error"},
	} {
		d := dialogFor(ended(c.role, c.err), false, "v9.9.9", hideIPs)
		if d == nil {
			t.Errorf("%v: no dialog", c.err)
			continue
		}
		if d.Kind != c.kind || actionIDs(d) != c.actions || !strings.Contains(d.Text, c.text) || !d.Home || d.Live || d.Key != "7:"+c.kind {
			t.Errorf("%v: dialog %+v (actions %s); want kind %s, actions %s, text with %q, home", c.err, d, actionIDs(d), c.kind, c.actions, c.text)
		}
		for _, a := range d.Actions {
			if a.Label == "" {
				t.Errorf("%v: action %s has no label", c.err, a.ID)
			}
		}
	}
	if d := dialogFor(ended(session.RoleJoin, notice(network.NoticeNameTaken, "Medli (2)")), false, "", hideIPs); d.Suggest != "Medli (2)" || !strings.Contains(d.Actions[0].Label, "Medli (2)") {
		t.Fatalf("name taken: %+v", d)
	}

	// A Leave or a plain end opens nothing; neither does a running lobby.
	if d := dialogFor(ended(session.RoleJoin, nil), false, "", hideIPs); d != nil {
		t.Fatalf("a Leave opened %+v", d)
	}
	if d := dialogFor(session.State{}, false, "", hideIPs); d != nil {
		t.Fatalf("no session opened %+v", d)
	}
	running := session.State{Session: 3, Phase: session.InLobby, Role: session.RoleJoin, Lobby: &session.Lobby{Link: session.LobbyLink{State: session.LinkUp}}}
	if d := dialogFor(running, true, "", hideIPs); d != nil {
		t.Fatalf("a lobby that is up opened %+v", d)
	}
}

// TestLiveDialogs: a joiner whose first connect keeps failing gets "Can't
// reach the host" (live, with Retry), and a pre-lobby host's direct join
// a notice.
func TestLiveDialogs(t *testing.T) {
	st := session.State{Session: 4, Phase: session.Connecting, Role: session.RoleJoin, Addr: "10.0.0.2:25570",
		Lobby: &session.Lobby{Link: session.LobbyLink{State: session.LinkConnecting}}}
	if d := dialogFor(st, true, "", hideIPs); d != nil {
		t.Fatalf("before any failed attempt: %+v", d)
	}
	st.Lobby.Link.Attempts, st.Lobby.Link.Err = 3, "dial tcp 10.0.0.2:25570: connection refused"
	d := dialogFor(st, true, "", hideIPs)
	if d == nil || d.Kind != dlgUnreachable || !d.Live || d.Home || actionIDs(d) != "retry*,leave" ||
		!strings.Contains(d.Text, "Couldn't reach the host at •••.•••.•••.2:25570") || !strings.Contains(d.Text, "port 25570") ||
		!strings.Contains(d.Detail, "3 failed attempts: dial tcp •••.•••.•••.2:25570: connection refused") {
		t.Fatalf("unreachable: %+v", d)
	}
	// Streamer-safe: the IP is never on screen.
	if strings.Contains(d.Text+d.Detail, "10.0.0.2") {
		t.Fatalf("unreachable dialog shows the IP: %+v", d)
	}
	if v := stateView(st, true, "", time.Now(), hideIPs); v.Addr != "10.0.0.2:25570" || v.AddrMasked != "•••.•••.•••.2:25570" ||
		strings.Contains(v.Lobby.Link.Err, "10.0.0.2") {
		t.Fatalf("stateView addresses: addr %q shown %q link err %q", v.Addr, v.AddrMasked, v.Lobby.Link.Err)
	}
	st.Addr = "10.0.0.2"
	if d := dialogFor(st, true, "", hideIPs); !strings.Contains(d.Text, "port 25565") {
		t.Fatalf("default port: %q", d.Text)
	}
	// The host's own lobby connection never asks.
	st.Role = session.RoleHost
	if d := dialogFor(st, true, "", hideIPs); d != nil {
		t.Fatalf("host: %+v", d)
	}

	legacy := session.State{Session: 5, Phase: session.InGame, Role: session.RoleJoin, Lobby: &session.Lobby{Legacy: true}}
	if d := dialogFor(legacy, true, "", hideIPs); d == nil || d.Kind != dlgLegacy || d.Live || d.Home || actionIDs(d) != "ok*" || d.Key != "5:legacy" {
		t.Fatalf("legacy: %+v", d)
	}
}

func TestReconnectBanner(t *testing.T) {
	now := time.Unix(10000, 0)
	st := session.State{Session: 1, Phase: session.InLobby, Role: session.RoleJoin,
		Lobby: &session.Lobby{Link: session.LobbyLink{State: session.LinkUp}}}
	if b := bannerFor(st, true, now); b != nil {
		t.Fatalf("link up: %+v", b)
	}
	st.Lobby.Link = session.LobbyLink{State: session.LinkReconnecting, Attempts: 2, Since: now.Add(-10 * time.Second)}
	b := bannerFor(st, true, now)
	if b == nil || b.Kind != "reconnecting" || b.Attempts != 2 || !strings.Contains(b.Text, "color and ready stay yours for 60 seconds") {
		t.Fatalf("reconnecting: %+v", b)
	}
	// Past the hold, the slot is gone; the banner says the host may
	// have left.
	st.Lobby.Link.Since = now.Add(-network.LobbyHoldTimeout - time.Second)
	if b := bannerFor(st, true, now); b == nil || !strings.Contains(b.Text, "may have closed") {
		t.Fatalf("past the hold: %+v", b)
	}
	// Not while connecting the first time, after the end, or joined
	// directly.
	st.Lobby.Link.State = session.LinkConnecting
	if b := bannerFor(st, true, now); b != nil {
		t.Fatalf("connecting: %+v", b)
	}
	st.Lobby.Link.State = session.LinkReconnecting
	if b := bannerFor(st, false, now); b != nil {
		t.Fatalf("ended: %+v", b)
	}
	st.Lobby.Legacy = true
	if b := bannerFor(st, true, now); b != nil {
		t.Fatalf("legacy: %+v", b)
	}
}

// TestStateViewLobby: the lobby as the frontend reads it.
func TestStateViewLobby(t *testing.T) {
	green, red := playercolor.Vanilla(), playercolor.Palette()[1].Color
	st := session.State{Session: 2, Phase: session.InLobby, Role: session.RoleHost, Name: "Link",
		Lobby: &session.Lobby{Member: 1, Host: true, Phase: session.LobbyPhaseLobby,
			Link: session.LobbyLink{State: session.LinkUp},
			Members: []session.LobbyMember{
				{ID: 1, Name: "Link", Color: red, Host: true, Ready: true, Via: session.ViaApp, RTT: 3 * time.Millisecond},
				{ID: 2, Name: "Medli", Color: red, Via: session.ViaApp, Away: true, RTT: 42 * time.Millisecond},
				{ID: 65537, Name: "Tetra", Color: green, Ready: true, Via: session.ViaCLI},
			}}}
	v := stateView(st, true, "v1", time.Now(), hideIPs)
	l := v.Lobby
	if v.Phase != "lobby" || l == nil || !l.Host || l.Coop != "off" || l.CanStart || l.Link.State != "up" || len(l.Members) != 3 {
		t.Fatalf("view = %+v, lobby %+v", v, l)
	}
	me, medli, tetra := l.Members[0], l.Members[1], l.Members[2]
	if !me.Self || me.CanKick || !me.Host || !me.Ready || me.Color != red.Hex() || me.ColorName != "red" || me.RTTMs != 3 {
		t.Fatalf("own row = %+v", me)
	}
	// Two players share a color; nothing marks it.
	if medli.Self || !medli.CanKick || !medli.Away || medli.Color != me.Color || medli.RTTMs != 42 || medli.Via != "app" {
		t.Fatalf("Medli = %+v", medli)
	}
	if !tetra.CanKick || tetra.Via != "cli" || tetra.Color != green.Hex() {
		t.Fatalf("Tetra = %+v", tetra)
	}
	if v.Dialog != nil || v.Banner != nil {
		t.Fatalf("a healthy lobby has a dialog or banner: %+v %+v", v.Dialog, v.Banner)
	}

	// A joiner kicks nobody; a running reconnect shows the banner.
	st.Role, st.Lobby.Host, st.Lobby.Member = session.RoleJoin, false, 2
	st.Lobby.Link = session.LobbyLink{State: session.LinkReconnecting, Attempts: 1, Since: time.Now()}
	v = stateView(st, true, "v1", time.Now(), hideIPs)
	for _, m := range v.Lobby.Members {
		if m.CanKick {
			t.Fatalf("a joiner can kick %+v", m)
		}
	}
	if !v.Lobby.Members[1].Self || v.Banner == nil || v.Lobby.Link.State != "reconnecting" || v.Lobby.Link.Attempts != 1 {
		t.Fatalf("joiner view: %+v banner %+v", v.Lobby, v.Banner)
	}
}

func TestBridgeKickRetryShare(t *testing.T) {
	b, e, _ := newTestBridge(t)
	b.hostIPs = func() []string { return []string{"26.5.6.7", "192.168.1.42"} }
	if got := b.ShareAddrs(); len(got.Addrs) != 0 || got.Addrs == nil || got.Port != 0 {
		t.Fatalf("ShareAddrs with no session = %+v", got)
	}
	if err := b.Kick(2); !errors.Is(err, session.ErrNoLobby) {
		t.Fatalf("Kick with no session = %v", err)
	}
	if err := b.Retry(); err == nil {
		t.Fatal("Retry before any Join = nil")
	}

	if err := b.Host(HostRequest{Name: "Link", Port: 25570}); err != nil {
		t.Fatal(err)
	}
	e.out(t)
	// The card's port is the session's (25570, not the saved or default
	// one), so its port-forward hint matches the addresses.
	share := b.ShareAddrs()
	got := share.Addrs
	if share.Port != 25570 || len(got) != 2 || got[0].Addr != "192.168.1.42:25570" || got[0].Masked != "•••.•••.•••.42:25570" || got[0].Label != "LAN" ||
		got[1].Addr != "26.5.6.7:25570" || got[1].Label != "Radmin VPN" {
		t.Fatalf("ShareAddrs = %+v", share)
	}
	if err := b.Kick(65537); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	cmds := append([]session.LobbyCommand(nil), e.cmds...)
	e.mu.Unlock()
	if len(cmds) != 1 || cmds[0].Kind != session.LobbyKick || cmds[0].Member != 65537 {
		t.Fatalf("Kick sent %+v", cmds)
	}
	b.Leave()

	// A joiner's ShareAddrs is empty, Kick refused; Retry joins again.
	if err := b.Join(JoinRequest{Addr: "10.0.0.2", Name: "Medli", Color: "red"}); err != nil {
		t.Fatal(err)
	}
	e.out(t)
	if got := b.ShareAddrs(); len(got.Addrs) != 0 || got.Port != 0 {
		t.Fatalf("a joiner's ShareAddrs = %+v", got)
	}
	if err := b.Kick(1); !errors.Is(err, session.ErrNotHost) {
		t.Fatalf("a joiner's Kick = %v", err)
	}
	first := b.State().Session
	if err := b.Retry(); err != nil {
		t.Fatal(err)
	}
	e.out(t)
	red, _ := playercolor.Parse("red")
	e.mu.Lock()
	joins := append([]session.JoinOptions(nil), e.join...)
	e.mu.Unlock()
	if st := b.State(); !st.Running || st.Session == first || len(joins) != 2 || joins[1].Addr != "10.0.0.2" || joins[1].Name != "Medli" || *joins[1].Color != red {
		t.Fatalf("after Retry: state %+v, joins %+v", st, joins)
	}
}

func TestBridgeHostListenHost(t *testing.T) {
	e := newFakeEngine()
	b := NewBridge(Options{Controller: session.New(e, session.Options{}), ConfigPath: t.TempDir() + "/c.json",
		Getenv: func(k string) string {
			if k == EnvListenHost {
				return " 127.0.0.1 "
			}
			return ""
		}})
	defer b.close()
	if err := b.Host(HostRequest{Name: "Link"}); err != nil {
		t.Fatal(err)
	}
	e.out(t)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.host[0].ListenHost != "127.0.0.1" || e.host[0].Port != 25565 {
		t.Fatalf("HostLobby got %+v", e.host[0])
	}
}

func TestBridgeCopyAndDownloads(t *testing.T) {
	b, _, _ := newTestBridge(t)
	if err := b.Copy("x"); err == nil {
		t.Fatal("Copy without the window's clipboard = nil")
	}
	if err := b.OpenDownloads(); err == nil {
		t.Fatal("OpenDownloads without the window = nil")
	}
	var copied, opened string
	b.setDesktop(func(s string) error { copied = s; return nil }, func(u string) error { opened = u; return nil })
	if err := b.Copy("192.168.1.42"); err != nil || copied != "192.168.1.42" {
		t.Fatalf("Copy = %v, clipboard %q", err, copied)
	}
	if err := b.OpenDownloads(); err != nil || opened != DownloadsURL {
		t.Fatalf("OpenDownloads = %v, opened %q", err, opened)
	}
	if b.Info().DownloadsURL != DownloadsURL {
		t.Fatal("Info has no downloads URL")
	}
}

// TestBridgeKickedDialog: a session the host kicked us from ends on the
// notice's error, and State carries the dialog that sends us Home.
func TestBridgeKickedDialog(t *testing.T) {
	b, e, _ := newTestBridge(t)
	if err := b.Join(JoinRequest{Addr: "10.0.0.2", Name: "Medli"}); err != nil {
		t.Fatal(err)
	}
	e.out(t)
	if st := b.State(); st.Dialog != nil || st.Banner != nil {
		t.Fatalf("a fresh lobby: %+v", st)
	}
	e.fail <- &network.LobbyNoticeError{Notice: network.LobbyNoticeBody{Code: network.NoticeKicked, Text: "the host removed you"}}
	var st StateView
	deadline := time.Now().Add(5 * time.Second)
	for st = b.State(); st.Running && time.Now().Before(deadline); st = b.State() {
		time.Sleep(5 * time.Millisecond)
	}
	if st.Running || !st.Failed || st.Dialog == nil || st.Dialog.Kind != dlgKicked || !st.Dialog.Home {
		t.Fatalf("after the kick: %+v dialog %+v", st, st.Dialog)
	}
}

// TestBrowserLobbyCalls: ui --browser serves the Lobby screen's calls and
// not the window's clipboard or browser.
func TestBrowserLobbyCalls(t *testing.T) {
	s, _ := startBrowser(t)
	for _, m := range []string{"Copy", "OpenDownloads"} {
		if code, body := api(t, s, m, `["x"]`, nil); code != 400 || !strings.Contains(fmt.Sprint(body["error"]), "no method") {
			t.Errorf("%s in a browser: %d %v", m, code, body)
		}
	}
	if code, body := api(t, s, "ShareAddrs", `[]`, nil); code != 200 || body["result"] == nil {
		t.Errorf("ShareAddrs: %d %v", code, body)
	}
	if code, body := api(t, s, "Kick", `[2]`, nil); code != 200 || !strings.Contains(fmt.Sprint(body["error"]), "not in a lobby") {
		t.Errorf("Kick with no session: %d %v", code, body)
	}
	if code, body := api(t, s, "Retry", `[]`, nil); code != 200 || body["error"] == nil {
		t.Errorf("Retry with no join: %d %v", code, body)
	}
}

// TestBridgeRecentHostOnlyOnceReached: a join that never reaches its host
// (a typo, a host that isn't running) doesn't become a recent host.
func TestBridgeRecentHostOnlyOnceReached(t *testing.T) {
	b, e, path := newTestBridge(t)
	e.unreachable = true
	if err := b.Join(JoinRequest{Addr: "10.0.0.99", Name: "Medli"}); err != nil {
		t.Fatal(err)
	}
	e.out(t)
	if st := b.State(); st.Dialog == nil || st.Dialog.Kind != dlgUnreachable {
		t.Fatalf("an unreachable host: %+v", st.Dialog)
	}
	b.Leave()
	b.State()
	if c, _ := config.Load(path); len(c.RecentHosts) != 0 || c.Name != "Medli" {
		t.Fatalf("saved config after an unreachable join = %+v", c)
	}
	e.unreachable = false
	if err := b.Join(JoinRequest{Addr: "10.0.0.2", Name: "Medli"}); err != nil {
		t.Fatal(err)
	}
	e.out(t)
	b.State()
	if c, _ := config.Load(path); len(c.RecentHosts) != 1 || c.RecentHosts[0].Addr != "10.0.0.2" {
		t.Fatalf("recent hosts after reaching one = %+v", c.RecentHosts)
	}
}
