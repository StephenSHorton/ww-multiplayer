package gui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
)

// TestMasker: the window masks every IP it draws by default (owner
// decision 2026-10-16, streamer-safe) with internal/redact's fixed
// shapes; the "Show IP addresses" switch passes everything through.
func TestMasker(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"192.168.1.42", "•••.•••.•••.42"},
		{"10.0.0.7:25570", "•••.•••.•••.7:25570"},
		{"[fd7a:115c:a1e0::53]:25570", "[••••:…:53]:25570"},
		{"localhost:25570", "localhost:25570"},
		{"", ""},
	} {
		if got := hideIPs.addr(c.in); got != c.want {
			t.Errorf("hideIPs.addr(%q) = %q, want %q", c.in, got, c.want)
		}
		if got := (masker{show: true}).addr(c.in); got != c.in {
			t.Errorf("shown addr(%q) = %q", c.in, got)
		}
	}
	line := "dial tcp [fd7a::53]:25565 then 192.168.1.42:25565 at 12:34:56"
	if got, want := hideIPs.text(line), "dial tcp [••••:…:53]:25565 then •••.•••.•••.42:25565 at 12:34:56"; got != want {
		t.Errorf("hideIPs.text = %q, want %q", got, want)
	}
	if got := (masker{show: true}).text(line); got != line {
		t.Errorf("shown text = %q", got)
	}
}

// TestAddrLabel: a recent host saved without a label is shown by its
// address kind (LAN, Radmin VPN, ...) next to its masked address.
func TestAddrLabel(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"192.168.1.42", "LAN"}, {"26.1.2.3:25570", "Radmin VPN"}, {"100.64.0.9", "Tailscale"},
		{"203.0.113.9", "Other"}, {"myhost.example", ""},
	} {
		if got := addrLabel(c.in); got != c.want {
			t.Errorf("addrLabel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestEventTextMasked: log and chat lines reach the window with their IP
// addresses masked, IPv6 included; with the switch on they come whole,
// with the masked text beside them for when it is turned off again.
func TestEventTextMasked(t *testing.T) {
	v := eventView(session.Event{Kind: session.EventLog, Text: "Joining the lobby at 192.168.1.42:25565 as \"Medli\"."}, hideIPs)
	if v.Text != "Joining the lobby at •••.•••.•••.42:25565 as \"Medli\"." || v.TextMasked != "" {
		t.Fatalf("log line = %+v", v)
	}
	v = eventView(session.Event{Kind: session.EventChat, From: "Link", Text: "try 26.1.2.3 or [fd00::1a2b]:25565"}, hideIPs)
	if v.Text != "try •••.•••.•••.3 or [••••:…:1a2b]:25565" || v.From != "Link" {
		t.Fatalf("chat line = %+v", v)
	}
	shown := masker{show: true}
	v = eventView(session.Event{Kind: session.EventLog, Text: "Host IPs: 192.168.1.42, fd00::7"}, shown)
	if v.Text != "Host IPs: 192.168.1.42, fd00::7" || v.TextMasked != "Host IPs: •••.•••.•••.42, ••••:…:7" {
		t.Fatalf("shown log line = %+v", v)
	}
	v = eventView(session.Event{Kind: session.EventLog, Text: "Ready at 12:34:56"}, shown)
	if v.Text != "Ready at 12:34:56" || v.TextMasked != "" {
		t.Fatalf("shown line without an IP = %+v", v)
	}
}

// TestBridgeShowIPs: the header's switch is off by default, saved in the
// config, tells every page to draw itself again, and turns off the
// masking everywhere the window shows an address: recent hosts, the
// share card, the header, dialogs, log and chat.
func TestBridgeShowIPs(t *testing.T) {
	b, e, path := newTestBridge(t)
	got := make(chan EventView, 64)
	ctx, cancel := context.WithCancel(context.Background())
	done := b.pump(ctx, func(ev EventView) { got <- ev })
	defer func() { cancel(); <-done }()
	waitKind := func(kind string, ok func(EventView) bool) EventView {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case v := <-got:
				if v.Kind == kind && (ok == nil || ok(v)) {
					return v
				}
			case <-deadline:
				t.Fatalf("no %q event", kind)
			}
		}
	}

	if v := b.Info(); v.ShowIPs {
		t.Fatal("Show IP addresses is on by default")
	}
	b.hostIPs = func() []string { return []string{"192.168.1.42", "fd7a:115c::53"} }
	if err := b.Host(HostRequest{Name: "Link", Port: 25570}); err != nil {
		t.Fatal(err)
	}
	out := e.out(t)
	share := b.ShareAddrs().Addrs
	if share[0].Masked != "•••.•••.•••.42:25570" || share[1].Masked != "[••••:…:53]:25570" || b.State().ShowIPs {
		t.Fatalf("masked share card = %+v", share)
	}
	out.Log(0, "listening on 192.168.1.42:25570")
	if v := waitKind("log", nil); v.Text != "listening on •••.•••.•••.42:25570" {
		t.Fatalf("masked log line = %+v", v)
	}
	if v, err := b.SendChat("I'm at 192.168.1.42"); err != nil || v.Text != "I'm at •••.•••.•••.42" || v.From != "Link" || v.Kind != "chat" {
		t.Fatalf("SendChat echo = %+v, %v", v, err)
	}

	b.SetShowIPs(true)
	waitKind("settings", nil)
	if !loadCfg(t, path).UI.ShowIPs || !b.Info().ShowIPs || !b.State().ShowIPs {
		t.Fatal("SetShowIPs(true) is not saved or not reported")
	}
	// Masked stays masked with the switch on: the page picks (a late
	// reply must not carry a real address in a field it may draw).
	share = b.ShareAddrs().Addrs
	if share[0].Masked != "•••.•••.•••.42:25570" || share[1].Masked != "[••••:…:53]:25570" || share[0].Addr != "192.168.1.42:25570" {
		t.Fatalf("shown share card = %+v", share)
	}
	out.Chat("Tetra", "mine is 10.0.0.9")
	if v := waitKind("chat", nil); v.Text != "mine is 10.0.0.9" || v.TextMasked != "mine is •••.•••.•••.9" {
		t.Fatalf("shown chat line = %+v", v)
	}
	if v, _ := b.SendChat("10.0.0.5"); v.Text != "10.0.0.5" || v.TextMasked != "•••.•••.•••.5" {
		t.Fatalf("shown SendChat echo = %+v", v)
	}
	b.Leave()

	// A joiner's header and its "Can't reach the host" dialog.
	e.unreachable = true
	if err := b.Join(JoinRequest{Addr: "10.0.0.2:25570", Name: "Medli"}); err != nil {
		t.Fatal(err)
	}
	e.out(t)
	st := b.State()
	if st.AddrMasked != "•••.•••.•••.2:25570" || st.Addr != "10.0.0.2:25570" || st.Dialog == nil || !strings.Contains(st.Dialog.Text, "10.0.0.2:25570") {
		t.Fatalf("shown join state = %+v / %+v", st, st.Dialog)
	}
	b.SetShowIPs(false)
	st = b.State()
	if st.AddrMasked != "•••.•••.•••.2:25570" || strings.Contains(fmt.Sprint(st.Dialog), "10.0.0.2") || loadCfg(t, path).UI.ShowIPs {
		t.Fatalf("masked again: %+v / %+v", st, st.Dialog)
	}
	b.Leave()

	// The saved recent hosts follow the switch too.
	b.mu.Lock()
	b.cfg.AddRecentHost("26.1.2.3", "", time.Now())
	b.mu.Unlock()
	if h := b.Info().RecentHosts[0]; h.Masked != "•••.•••.•••.3" {
		t.Fatalf("masked recent host = %+v", h)
	}
	b.SetShowIPs(true)
	if h := b.Info().RecentHosts[0]; h.Masked != "•••.•••.•••.3" || h.Addr != "26.1.2.3" {
		t.Fatalf("shown recent host = %+v", h)
	}
}

// TestBrowserShowIPs: a browser page can flip the switch.
func TestBrowserShowIPs(t *testing.T) {
	s, _ := startBrowser(t)
	if code, body := api(t, s, "SetShowIPs", `[true]`, nil); code != 200 || body["error"] != nil {
		t.Fatalf("SetShowIPs: %d %v", code, body)
	}
	if code, body := api(t, s, "Info", `[]`, nil); code != 200 || fmt.Sprint(body["result"].(map[string]any)["showIPs"]) != "true" {
		t.Fatalf("Info after SetShowIPs: %d %v", code, body)
	}
}
