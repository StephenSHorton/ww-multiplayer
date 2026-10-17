package gui

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/playercolor"
)

// deadLink is what a write on a dropped lobby connection returns: a
// net.OpError naming both ends, wrapped as the network package does.
func deadLink() error {
	op := &net.OpError{Op: "write", Net: "tcp",
		Source: &net.TCPAddr{IP: net.ParseIP("192.168.1.5"), Port: 51234},
		Addr:   &net.TCPAddr{IP: net.ParseIP("192.168.1.42"), Port: 25565},
		Err:    errors.New("wsasend: An existing connection was forcibly closed by the remote host.")}
	return fmt.Errorf("write data: %w", op)
}

// TestBridgeErrorsMasked: an error a Bridge method returns reaches the
// page (a toast) with its IPs masked, unless the switch is on; errors.Is
// and errors.As still see through it.
func TestBridgeErrorsMasked(t *testing.T) {
	b, e, _ := newTestBridge(t)
	if err := b.Host(HostRequest{Name: "Link"}); err != nil {
		t.Fatal(err)
	}
	e.out(t)
	e.mu.Lock()
	e.err = deadLink()
	e.mu.Unlock()
	calls := map[string]func() error{
		"SendChat": func() error { _, err := b.SendChat("hi"); return err },
		"SetReady": func() error { return b.SetReady(true) },
		"SetColor": func() error { return b.SetColor(playercolor.Palette()[2].Color.Hex()) },
		"Kick":     func() error { return b.Kick(2) },
	}
	for name, call := range calls {
		err := call()
		var op *net.OpError
		if err == nil || !errors.As(err, &op) {
			t.Fatalf("%s: err = %v, want the OpError under it", name, err)
		}
		if msg := err.Error(); strings.Contains(msg, "192.168.1") || !strings.Contains(msg, "•••.•••.•••.5:51234->•••.•••.•••.42:25565") {
			t.Errorf("%s: error shown as %q", name, msg)
		}
	}
	b.SetShowIPs(true)
	if err := b.SetReady(true); err == nil || !strings.Contains(err.Error(), "192.168.1.5:51234->192.168.1.42:25565") {
		t.Errorf("with the switch on: %v", err)
	}
}

// TestBrowserErrorsMasked: the same through ui --browser's /api/call.
func TestBrowserErrorsMasked(t *testing.T) {
	s, e := startBrowser(t)
	if code, body := api(t, s, "Host", `[{"name":"Link"}]`, nil); code != 200 || body["error"] != nil {
		t.Fatalf("Host: %d %v", code, body)
	}
	e.out(t)
	e.mu.Lock()
	e.err = deadLink()
	e.mu.Unlock()
	code, body := api(t, s, "SendChat", `["hi"]`, nil)
	if msg := fmt.Sprint(body["error"]); code != 200 || strings.Contains(msg, "192.168") || !strings.Contains(msg, "•••.•••.•••.42:25565") {
		t.Fatalf("SendChat: %d %q", code, msg)
	}
}

// TestBridgeMethodsMaskErrors: every exported Bridge method that returns
// an error masks it first (maskErr), so a new one can't forget to.
func TestBridgeMethodsMaskErrors(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	seen := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, f, src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !fn.Name.IsExported() || fn.Type.Results == nil {
				continue
			}
			star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			if id, ok := star.X.(*ast.Ident); !ok || id.Name != "Bridge" {
				continue
			}
			res := fn.Type.Results.List
			if id, ok := res[len(res)-1].Type.(*ast.Ident); !ok || id.Name != "error" {
				continue
			}
			seen++
			first := ""
			if len(fn.Body.List) > 0 {
				first = string(src[fset.Position(fn.Body.List[0].Pos()).Offset:fset.Position(fn.Body.List[0].End()).Offset])
			}
			if first != "defer b.maskErr(&err)" {
				t.Errorf("%s: Bridge.%s returns an error but doesn't start with defer b.maskErr(&err)", f, fn.Name.Name)
			}
		}
	}
	if seen < 10 {
		t.Fatalf("found only %d Bridge methods that return an error", seen)
	}
}

// TestBridgeJoinValidates: Join refuses what no host can be, as the TUI
// does, before it reaches the header, the log or a dialog; its error
// masks what was typed.
func TestBridgeJoinValidates(t *testing.T) {
	b, e, _ := newTestBridge(t)
	for _, addr := range []string{"26.12.34.566", "26.12.34.566:25565", "192.168.1.42;25565", "26.12.34"} {
		err := b.Join(JoinRequest{Addr: addr, Name: "Medli"})
		if err == nil {
			t.Fatalf("Join(%q) was accepted", addr)
		}
		if msg := err.Error(); strings.Contains(msg, "26.12") || strings.Contains(msg, "192.168") || !strings.Contains(msg, "•••") {
			t.Errorf("Join(%q) error = %q", addr, msg)
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.join) != 0 {
		t.Fatalf("a bad address reached the engine: %+v", e.join)
	}
}

// TestPeerNamesMasked: a peer may call itself "192.168.0.5": its lobby
// row, its player row and its chat lines' sender are masked unless the
// switch is on. This player's own name isn't.
func TestPeerNamesMasked(t *testing.T) {
	st := session.State{Session: 1, Phase: session.InLobby, Role: session.RoleHost, Name: "10.0.0.1",
		Live: session.Live{Players: []session.Player{{ID: 3, Name: "26.12.34.56"}}},
		Lobby: &session.Lobby{Member: 1, Host: true, Phase: session.LobbyPhaseLobby, Members: []session.LobbyMember{
			{ID: 1, Name: "10.0.0.1", Host: true, Via: session.ViaApp},
			{ID: 2, Name: "192.168.0.5", Via: session.ViaApp},
		}}}
	v := stateView(st, true, "", time.Now(), hideIPs)
	if m := v.Lobby.Members; m[0].Name != "10.0.0.1" || m[1].Name != "•••.•••.•••.5" || v.Players[0].Name != "•••.•••.•••.56" || v.Name != "10.0.0.1" {
		t.Fatalf("masked names: %+v / %+v", m, v.Players)
	}
	v = stateView(st, true, "", time.Now(), masker{show: true})
	if v.Lobby.Members[1].Name != "192.168.0.5" || v.Players[0].Name != "26.12.34.56" {
		t.Fatalf("shown names: %+v / %+v", v.Lobby.Members, v.Players)
	}
	ev := session.Event{Kind: session.EventChat, From: "192.168.0.5", Text: "host:10.1.2.3:25565"}
	if c := eventView(ev, hideIPs); c.From != "•••.•••.•••.5" || c.FromMasked != "" || c.Text != "host:•••.•••.•••.3:25565" {
		t.Fatalf("masked chat line = %+v", c)
	}
	if c := eventView(ev, masker{show: true}); c.From != "192.168.0.5" || c.FromMasked != "•••.•••.•••.5" || c.TextMasked != "host:•••.•••.•••.3:25565" {
		t.Fatalf("shown chat line = %+v", c)
	}
}

// TestMaskedFieldsAlwaysMasked: an address's masked field is masked
// whatever the switch says (the page picks which to draw), so a reply
// from before the switch was turned off can't draw a real address.
func TestMaskedFieldsAlwaysMasked(t *testing.T) {
	st := session.State{Session: 1, Phase: session.InLobby, Role: session.RoleJoin, Addr: "26.12.34.56.nip.io:25570"}
	for _, mk := range []masker{hideIPs, {show: true}} {
		if v := stateView(st, true, "", time.Now(), mk); v.AddrMasked != "•••.•••.•••.56.nip.io:25570" || v.Addr != st.Addr {
			t.Errorf("show %v: addr %q masked %q", mk.show, v.Addr, v.AddrMasked)
		}
	}
	b, _, _ := newTestBridge(t)
	b.SetShowIPs(true)
	b.mu.Lock()
	b.cfg.AddRecentHost("x.26.12.34.56.nip.io", "", time.Now())
	b.mu.Unlock()
	if h := b.Info().RecentHosts[0]; h.Masked != "x.•••.•••.•••.56.nip.io" || h.Addr != "x.26.12.34.56.nip.io" {
		t.Fatalf("recent host = %+v", h)
	}
}
