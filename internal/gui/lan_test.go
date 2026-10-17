package gui

import (
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/config"
	"github.com/StephenSHorton/ww-multiplayer/internal/lan"
	"github.com/StephenSHorton/ww-multiplayer/internal/netutil"
	"github.com/StephenSHorton/ww-multiplayer/internal/network"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
)

// TestMain keeps every test's LAN discovery on 127.0.0.1 (lanTestHost):
// a test that hosts sends its beacon there, never to the network.
func TestMain(m *testing.M) {
	lanTestHost = "127.0.0.1"
	os.Exit(m.Run())
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

// lanBridge is a Bridge over a fake engine whose discovery runs on
// 127.0.0.1:port.
func lanBridge(t *testing.T, port int, env map[string]string) (*Bridge, *fakeEngine) {
	t.Helper()
	e := newFakeEngine()
	b := NewBridge(Options{Version: "v9.9.9", Controller: session.New(e, session.Options{}), ConfigPath: t.TempDir() + "/c.json",
		Getenv: func(k string) string {
			switch k {
			case EnvListenHost:
				return "127.0.0.1"
			case EnvLanPort:
				return strconv.Itoa(port)
			}
			return env[k]
		}})
	t.Cleanup(b.close)
	return b, e
}

func entry(host string, addr string, proto uint16) lan.Entry {
	return lan.Entry{Beacon: lan.Beacon{Format: 1, Protocol: proto, Port: netip.MustParseAddrPort(addr).Port(), Players: 2, Max: 24,
		Phase: lan.PhaseInGame, Flags: lan.FlagCoop, Instance: [8]byte{0xab, 1}, Version: "v0.4.0", Host: host},
		Addrs: []netip.AddrPort{netip.MustParseAddrPort(addr)}}
}

// TestLanSessionViewMasked: a listed session's address is masked
// whatever the switch says in Masked (the page picks), and its host's
// name is masked as free text unless the switch is on.
func TestLanSessionViewMasked(t *testing.T) {
	e := entry("192.168.0.5", "26.12.34.56:25570", network.ProtocolVersion)
	v := lanSessionView(e, hideIPs)
	if v.Addr != "26.12.34.56:25570" || v.Masked != "•••.•••.•••.56:25570" || v.Host != "•••.•••.•••.5" ||
		v.Kind != "radmin" || v.Label != "Radmin VPN" || v.Players != 2 || v.Max != 24 || v.Phase != "in game" ||
		!v.Coop || v.Version != "v0.4.0" || !v.Compatible || !strings.HasPrefix(v.ID, "ab01000000000000-") || v.ID != e.Key() {
		t.Fatalf("masked view = %+v", v)
	}
	v = lanSessionView(e, masker{show: true})
	if v.Host != "192.168.0.5" || v.Masked != "•••.•••.•••.56:25570" {
		t.Fatalf("shown view = %+v", v)
	}
	if v := lanSessionView(entry("Tetra", "127.0.0.1:25601", network.ProtocolVersion), hideIPs); v.Kind != "local" || v.Label != "This computer" || v.Masked != "•••.•••.•••.1:25601" {
		t.Fatalf("a session on this machine = %+v", v)
	}
	// On the default port the row joins the bare IP, as the share card
	// shows it and as one would type it.
	if v := lanSessionView(entry("Tetra", "192.168.1.5:25565", network.ProtocolVersion), hideIPs); v.Addr != "192.168.1.5" || v.Masked != "•••.•••.•••.5" {
		t.Fatalf("default port = %+v", v)
	}
	if v := lanSessionView(entry("Tetra", "192.168.1.5:25565", network.ProtocolVersion+1), hideIPs); v.Compatible || v.Host != "Tetra" {
		t.Fatalf("another protocol = %+v", v)
	}
}

func TestLanSessionsOff(t *testing.T) {
	b, _ := lanBridge(t, freeUDPPort(t), map[string]string{EnvLan: "off"})
	v := b.LanSessions()
	if v.On || !strings.Contains(v.Note, "turned off") || v.Sessions == nil {
		t.Fatalf("off = %+v", v)
	}
	b.lan.mu.Lock()
	defer b.lan.mu.Unlock()
	if b.lan.ln != nil {
		t.Fatal("listening although off")
	}
}

// TestLanHostSeenByJoiner: a hosting Bridge's beacon reaches another
// Bridge on Home (over 127.0.0.1), which lists the host by name with its
// lobby's port, and joins it; the joiner stops listening once it joined.
func TestLanHostSeenByJoiner(t *testing.T) {
	port := freeUDPPort(t)
	host, _ := lanBridge(t, port, nil)
	joiner, je := lanBridge(t, port, nil)
	if v := joiner.LanSessions(); !v.On || len(v.Sessions) != 0 {
		t.Fatalf("before hosting: %+v", v)
	}
	if err := host.Host(HostRequest{Name: "Tetra", Port: 25601}); err != nil {
		t.Fatal(err)
	}
	// Host returns with the session's placeholder lobby (no members yet)
	// and sends the first beacon at once, while the engine publishes its
	// lobby concurrently: the first beacon heard may say 0 players, and
	// the next one (lan.BeaconInterval later) says 2. So wait, with a
	// deadline, for a beacon that says 2 rather than taking the first.
	var s LanSessionView
	heard := func(s LanSessionView) bool {
		return s.Host == "Tetra" && s.Addr == "127.0.0.1:25601" && s.Players == 2 && s.Phase == "lobby" && s.Compatible && s.Version == "v9.9.9"
	}
	deadline := time.Now().Add(3*lan.BeaconInterval + 5*time.Second)
	for {
		v := joiner.LanSessions() // also keeps the listener's lease
		if len(v.Sessions) > 1 {
			t.Fatalf("heard more than the host: %+v", v.Sessions)
		}
		if len(v.Sessions) == 1 {
			s = v.Sessions[0]
			if heard(s) {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("heard %+v", s)
		}
		time.Sleep(50 * time.Millisecond)
	}
	// The host doesn't list itself (it isn't on Home, and its own
	// beacons are skipped).
	if v := host.LanSessions(); len(v.Sessions) != 0 {
		t.Fatalf("the host lists %+v", v.Sessions)
	}
	if err := joiner.Join(JoinRequest{Addr: s.Addr, Name: "Medli"}); err != nil {
		t.Fatal(err)
	}
	je.out(t)
	if je.join[0].Addr != "127.0.0.1:25601" {
		t.Fatalf("joined %+v", je.join)
	}
	joiner.lan.mu.Lock()
	ln := joiner.lan.ln
	joiner.lan.mu.Unlock()
	if ln != nil {
		t.Fatal("the joiner still listens in a session")
	}
	// Leave stops the beacon.
	host.Leave()
	host.lan.mu.Lock()
	stopped := host.lan.beacon == nil
	host.lan.mu.Unlock()
	if !stopped {
		t.Fatal("the beacon runs after Leave")
	}
}

// TestBeaconFollowsSession: the beacon carries the lobby's members (not
// the away ones), phase and co-op choice, and stops with the session.
func TestBeaconFollowsSession(t *testing.T) {
	b, e := lanBridge(t, freeUDPPort(t), nil)
	b.stopBeacon()
	if err := b.Host(HostRequest{Name: "Tetra", Port: 25570}); err != nil {
		t.Fatal(err)
	}
	out := e.out(t)
	id := b.ctrl.Snapshot().Session
	waitFor(t, func() bool { bc, ok := b.beacon(id, 25570); return ok && bc.Players == 2 })
	out.LobbyState(session.Lobby{Member: 1, Host: true, Phase: session.LobbyPhaseInGame, Settings: session.LobbySettings{Coop: "new"},
		Members: []session.LobbyMember{{ID: 1, Name: "Tetra", Host: true}, {ID: 2, Name: "Medli", Away: true}, {ID: 3, Name: "Makar"}}})
	waitFor(t, func() bool { bc, _ := b.beacon(id, 25570); return bc.Phase == lan.PhaseInGame })
	bc, ok := b.beacon(id, 25570)
	if !ok || bc.Players != 2 || !bc.Coop() || bc.Port != 25570 || bc.Host != "Tetra" || bc.Version != "v9.9.9" ||
		bc.Protocol != network.ProtocolVersion || bc.Max != network.MaxLobbyMembers || bc.Instance != b.lan.self {
		t.Fatalf("beacon = %+v, %v", bc, ok)
	}
	if _, err := lan.Encode(bc); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.beacon(id+1, 25570); ok {
		t.Fatal("a beacon for another session")
	}
	b.Leave()
	if _, ok := b.beacon(id, 25570); ok {
		t.Fatal("a beacon after Leave")
	}
}

// TestLanListenerLease: the listener closes once the page stopped asking.
func TestLanListenerLease(t *testing.T) {
	b, _ := lanBridge(t, freeUDPPort(t), nil)
	now := time.Now()
	b.now = func() time.Time { return now }
	if v := b.LanSessions(); !v.On {
		t.Fatalf("not listening: %+v", v)
	}
	b.lan.mu.Lock()
	now = now.Add(lanLease + time.Second)
	b.lan.mu.Unlock()
	waitFor(t, func() bool {
		b.lan.mu.Lock()
		defer b.lan.mu.Unlock()
		return b.lan.ln == nil
	})
}

// TestBridgeJoinCode: the Join box takes a join code (any case, without
// dashes) and joins the address it holds; one with a typo is refused
// without echoing it, and the address is never in the error.
func TestBridgeJoinCode(t *testing.T) {
	b, e, _ := newTestBridge(t)
	code, _ := netutil.JoinCode(netip.MustParseAddrPort("192.168.1.42:25570"))
	typo := code[:len(code)-1] + map[bool]string{true: "1", false: "0"}[code[len(code)-1] == '0']
	err := b.Join(JoinRequest{Addr: typo, Name: "Medli"})
	if err == nil || !strings.Contains(err.Error(), "typo") || strings.Contains(err.Error(), typo[3:7]) {
		t.Fatalf("typo: %v", err)
	}
	if err := b.Join(JoinRequest{Addr: strings.ToLower(strings.ReplaceAll(code, "-", "")), Name: "Medli"}); err != nil {
		t.Fatal(err)
	}
	e.out(t)
	if e.join[0].Addr != "192.168.1.42:25570" {
		t.Fatalf("joined %+v", e.join)
	}
	if st := b.State(); st.AddrMasked != "•••.•••.•••.42:25570" {
		t.Fatalf("state addr = %q / %q", st.Addr, st.AddrMasked)
	}
}

// TestCodesMaskedInText: a join code in a log or chat line, or in an
// error, is masked like an address unless the switch is on.
func TestCodesMaskedInText(t *testing.T) {
	code, _ := netutil.JoinCode(netip.MustParseAddrPort("10.0.0.7:25565"))
	ev := session.Event{Kind: session.EventChat, From: "Tetra", Text: "my code is " + code}
	if v := eventView(ev, hideIPs); v.Text != "my code is WW-••••-••••" {
		t.Fatalf("masked = %q", v.Text)
	}
	if v := eventView(ev, masker{show: true}); v.Text != ev.Text || v.TextMasked != "my code is WW-••••-••••" {
		t.Fatalf("shown = %+v", v)
	}
	if hideIPs.addr(code) != "WW-••••-••••" || (masker{show: true}).addr(code) != code {
		t.Fatal("masker.addr on a code")
	}
}

// TestLanHost: WW_LISTEN_HOST keeps discovery where the user put the
// session; a name or address discovery can't use turns it off instead of
// falling back to every network.
func TestLanHost(t *testing.T) {
	for v, want := range map[string]string{
		"": "", "0.0.0.0": "", "::": "", "127.0.0.1": "127.0.0.1", " localhost ": "127.0.0.1", "LocalHost": "127.0.0.1",
		"::1": "127.0.0.1", "[::1]": "127.0.0.1", "192.168.1.5": "192.168.1.5", "::ffff:10.0.0.2": "10.0.0.2",
	} {
		if got, ok := lanHost(v); !ok || got != want {
			t.Errorf("lanHost(%q) = %q, %v; want %q", v, got, ok, want)
		}
	}
	for _, v := range []string{"myhost", "fe80::1", "2001:db8::1", "fe80::1%eth0", "host.example"} {
		if got, ok := lanHost(v); ok {
			t.Errorf("lanHost(%q) = %q, on", v, got)
		}
	}
	for _, v := range []string{"localhost", "::1"} {
		b := NewBridge(Options{Version: "v9.9.9", Controller: session.New(newFakeEngine(), session.Options{}), ConfigPath: t.TempDir() + "/c.json",
			Getenv: func(k string) string {
				if k == EnvListenHost {
					return v
				}
				return ""
			}})
		if o, off := b.lanOptions(); off != "" || o.Host != "127.0.0.1" {
			t.Errorf("%s: options %+v, %q", v, o, off)
		}
		b.close()
	}
	b := NewBridge(Options{Version: "v9.9.9", Controller: session.New(newFakeEngine(), session.Options{}), ConfigPath: t.TempDir() + "/c.json",
		Getenv: func(k string) string {
			if k == EnvListenHost {
				return "myhost"
			}
			return ""
		}})
	defer b.close()
	v := b.LanSessions()
	if v.On || !strings.Contains(v.Note, EnvListenHost) {
		t.Fatalf("a host name = %+v", v)
	}
	b.lan.mu.Lock()
	defer b.lan.mu.Unlock()
	if b.lan.ln != nil {
		t.Fatal("listening on a host name")
	}
}

// TestLanReaperEndsWhenReplaced: a listener closed by a Host or Join that
// then failed (Home goes on polling and opens a new one) leaves one
// reaper running, not one per attempt.
func TestLanReaperEndsWhenReplaced(t *testing.T) {
	b, _ := lanBridge(t, freeUDPPort(t), nil)
	for i := 0; i < 3; i++ {
		if v := b.LanSessions(); !v.On {
			t.Fatalf("not listening: %+v", v)
		}
		b.stopLanListen() // as Host and Join do before a start that may fail
	}
	if v := b.LanSessions(); !v.On {
		t.Fatalf("not listening: %+v", v)
	}
	deadline := time.Now().Add(5 * time.Second)
	for b.lan.reapers.Load() != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("%d reapers", b.lan.reapers.Load())
		}
		b.LanSessions() // Home keeps polling
		time.Sleep(50 * time.Millisecond)
	}
	// And the one left still owns the open listener.
	b.lan.mu.Lock()
	ln := b.lan.ln
	b.lan.mu.Unlock()
	if ln == nil {
		t.Fatal("the listener was closed")
	}
}

// TestJoinCodeDefaultPortOneRecentHost: a code for the default port joins
// the bare IP, so joining by code and then by the typed IP leaves one
// recent host.
func TestJoinCodeDefaultPortOneRecentHost(t *testing.T) {
	b, e, path := newTestBridge(t)
	code, _ := netutil.JoinCode(netip.MustParseAddrPort("192.168.1.42:25565"))
	if err := b.Join(JoinRequest{Addr: code, Name: "Medli"}); err != nil {
		t.Fatal(err)
	}
	e.out(t)
	b.State()
	if e.join[0].Addr != "192.168.1.42" {
		t.Fatalf("joined %+v", e.join)
	}
	b.Leave()
	b.State()
	if err := b.Join(JoinRequest{Addr: "192.168.1.42", Name: "Medli"}); err != nil {
		t.Fatal(err)
	}
	e.out(t)
	b.State()
	if c, _ := config.Load(path); len(c.RecentHosts) != 1 || c.RecentHosts[0].Addr != "192.168.1.42" {
		t.Fatalf("recent hosts = %+v", c.RecentHosts)
	}
}
