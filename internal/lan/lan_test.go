package lan

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/netip"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func sample() Beacon {
	return Beacon{Format: FormatVersion, Flags: FlagCoop, Protocol: 2, Port: 25570, Players: 3, Max: 24,
		Phase: PhaseLobby, Instance: [8]byte{1, 2, 3, 4, 5, 6, 7, 8}, Version: "v0.4.0", Host: "Tetra"}
}

func TestBeaconRoundTrip(t *testing.T) {
	for _, b := range []Beacon{
		sample(),
		{Format: 1, Protocol: 2, Port: 1, Phase: PhaseInGame, Host: "", Version: ""},
		{Format: 1, Protocol: 65535, Port: 65535, Players: 255, Max: 255, Phase: PhaseStarting, Host: "Łink ☆ 小", Version: "dev"},
	} {
		p, err := Encode(b)
		if err != nil {
			t.Fatal(err)
		}
		if len(p) > MaxBeacon {
			t.Fatalf("beacon of %d bytes", len(p))
		}
		got, err := Decode(p)
		if err != nil || got != b {
			t.Errorf("Decode(Encode(%+v)) = %+v, %v", b, got, err)
		}
	}
	if !sample().Coop() {
		t.Error("Coop flag")
	}
}

// TestBeaconGolden pins the wire form (docs/10-lobby.md §10).
func TestBeaconGolden(t *testing.T) {
	p, err := Encode(sample())
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{'W', 'W', 'L', 'B', 1, 1, 0, 2, 0x63, 0xE2, 3, 24, 0, 1, 2, 3, 4, 5, 6, 7, 8,
		6, 'v', '0', '.', '4', '.', '0', 5, 'T', 'e', 't', 'r', 'a'}
	if !bytes.Equal(p, want) {
		t.Fatalf("beacon = % x\nwant     % x", p, want)
	}
}

func TestBeaconEncodeCleans(t *testing.T) {
	b := sample()
	b.Host = "Te\x00tra\n" + strings.Repeat("é", 40) // 80 bytes of é
	b.Version = strings.Repeat("v", 40)
	p, err := Encode(b)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.Host, "Tetraé") || len(got.Host) > maxNameLen || len(got.Version) != maxVersionLen {
		t.Errorf("cleaned = %q / %q", got.Host, got.Version)
	}
	if _, err := Encode(Beacon{Port: 0}); err == nil {
		t.Error("port 0 encoded")
	}
	if _, err := Encode(Beacon{Port: 1, Phase: 7}); err == nil {
		t.Error("phase 7 encoded")
	}
}

func TestBeaconDecodeRefuses(t *testing.T) {
	good, _ := Encode(sample())
	mod := func(f func(p []byte) []byte) []byte { return f(append([]byte(nil), good...)) }
	cases := map[string][]byte{
		"empty":       nil,
		"other magic": mod(func(p []byte) []byte { p[0] = 'X'; return p }),
		"short":       good[:headerLen],
		"format 0":    mod(func(p []byte) []byte { p[4] = 0; return p }),
		"port 0":      mod(func(p []byte) []byte { p[8], p[9] = 0, 0; return p }),
		"phase 3":     mod(func(p []byte) []byte { p[12] = 3; return p }),
		"trailing":    append(append([]byte(nil), good...), 0),
		"long string": mod(func(p []byte) []byte { p[headerLen] = 200; return p }),
		"cut string":  good[:len(good)-1],
		"bad utf8":    mod(func(p []byte) []byte { p[len(p)-1] = 0xff; return p }),
		"control":     mod(func(p []byte) []byte { p[len(p)-1] = '\n'; return p }),
		"oversize":    append(append([]byte(nil), good...), make([]byte, MaxBeacon)...),
	}
	for name, p := range cases {
		if b, err := Decode(p); err == nil {
			t.Errorf("%s: decoded %+v", name, b)
		} else if !errors.Is(err, ErrMalformed) && !errors.Is(err, ErrNotBeacon) {
			t.Errorf("%s: error %v", name, err)
		}
	}
}

// TestBeaconNewerFormat: a newer format is read as far as format 1 goes.
func TestBeaconNewerFormat(t *testing.T) {
	p, _ := Encode(sample())
	p[4], p[12] = 2, 9
	p = append(p, 1, 2, 3)
	b, err := Decode(p)
	if err != nil {
		t.Fatal(err)
	}
	if b.Format != 2 || b.Phase != PhaseUnknown || b.Host != "Tetra" || b.Phase.String() != "unknown" {
		t.Fatalf("newer = %+v", b)
	}
}

func TestTableExpiryAndOwnFilter(t *testing.T) {
	self := [8]byte{9}
	tb := NewTable(self)
	now := time.Unix(1000, 0)
	b := sample()
	own := b
	own.Instance = self
	if tb.Add(netip.MustParseAddr("192.168.1.5"), own, now) {
		t.Error("own beacon added")
	}
	if !tb.Add(netip.MustParseAddr("192.168.1.5"), b, now) {
		t.Fatal("beacon not added")
	}
	if l := tb.List(now); len(l) != 1 || l[0].Addr() != netip.MustParseAddrPort("192.168.1.5:25570") || l[0].Host != "Tetra" {
		t.Fatalf("list = %+v", l)
	}
	if l := tb.List(now.Add(EntryTTL)); len(l) != 1 {
		t.Fatalf("expired early: %+v", l)
	}
	if l := tb.List(now.Add(EntryTTL + time.Millisecond)); len(l) != 0 {
		t.Fatalf("not expired: %+v", l)
	}
	// A beacon refreshes it, and changes what it says.
	tb.Add(netip.MustParseAddr("192.168.1.5"), b, now)
	b.Players = 4
	tb.Add(netip.MustParseAddr("192.168.1.5"), b, now.Add(5*time.Second))
	if l := tb.List(now.Add(10 * time.Second)); len(l) != 1 || l[0].Players != 4 {
		t.Fatalf("refreshed = %+v", l)
	}
}

// TestTableMergesAddrs: one host heard on two networks is one entry, its
// addresses ranked; an address that went quiet drops out.
func TestTableMergesAddrs(t *testing.T) {
	tb := NewTable([8]byte{})
	tb.Rank = func(a netip.Addr) int {
		if a.Is4() && a.As4()[0] == 26 {
			return 1
		}
		return 0
	}
	now := time.Unix(1000, 0)
	b := sample()
	tb.Add(netip.MustParseAddr("26.1.2.3"), b, now)
	tb.Add(netip.MustParseAddr("::ffff:192.168.1.5"), b, now.Add(time.Second))
	l := tb.List(now.Add(time.Second))
	if len(l) != 1 || len(l[0].Addrs) != 2 || l[0].Addr().Addr() != netip.MustParseAddr("192.168.1.5") {
		t.Fatalf("merged = %+v", l)
	}
	l = tb.List(now.Add(EntryTTL + 500*time.Millisecond))
	if len(l) != 1 || len(l[0].Addrs) != 1 || l[0].Addr().Addr() != netip.MustParseAddr("192.168.1.5") {
		t.Fatalf("after the Radmin one went quiet = %+v", l)
	}
}

// TestTableBounded: a flood of hosts (or of addresses for one) can't grow
// the table without end.
func TestTableBounded(t *testing.T) {
	tb := NewTable([8]byte{})
	now := time.Unix(1000, 0)
	b := sample()
	for i := 0; i < 1000; i++ {
		b.Instance = [8]byte{byte(i), byte(i >> 8), 1}
		tb.Add(netip.AddrFrom4([4]byte{10, 0, byte(i >> 8), byte(i)}), b, now)
	}
	if n := len(tb.List(now)); n != maxEntries {
		t.Fatalf("%d entries", n)
	}
	// Once they expire, new hosts fit again.
	b.Instance = [8]byte{0xff, 0xff}
	if !tb.Add(netip.MustParseAddr("10.9.9.9"), b, now.Add(EntryTTL+time.Second)) {
		t.Fatal("a new host after the flood expired was refused")
	}
	tb2 := NewTable([8]byte{})
	b = sample()
	for i := 0; i < 100; i++ {
		tb2.Add(netip.AddrFrom4([4]byte{10, 0, 0, byte(i)}), b, now)
	}
	if l := tb2.List(now); len(l) != 1 || len(l[0].Addrs) != maxAddrsPerHost {
		t.Fatalf("addrs = %d", len(l[0].Addrs))
	}
}

// TestTablePerSource: one machine sending beacons for many made-up
// hosts can't fill the table; a real host on another address is still
// listed.
func TestTablePerSource(t *testing.T) {
	tb := NewTable([8]byte{})
	now := time.Unix(1000, 0)
	flood := netip.MustParseAddr("192.168.1.66")
	b := sample()
	for i := 0; i < 500; i++ {
		b.Instance = [8]byte{byte(i), byte(i >> 8), 7}
		tb.Add(flood, b, now)
	}
	if n := len(tb.List(now)); n != maxPerSource {
		t.Fatalf("%d entries from one source", n)
	}
	good := sample()
	good.Host = "Medli"
	if !tb.Add(netip.MustParseAddr("192.168.1.5"), good, now) {
		t.Fatal("a real host was refused after a flood from another source")
	}
	found := false
	for _, e := range tb.List(now) {
		found = found || e.Host == "Medli"
	}
	if !found {
		t.Fatal("the real host isn't listed")
	}
	// Once the flood's entries expire, its source can list again.
	b.Instance = [8]byte{0xee}
	if !tb.Add(flood, b, now.Add(EntryTTL+time.Second)) {
		t.Fatal("the source was refused after its entries expired")
	}
}

// TestTableCopyDoesNotRedirect: a beacon that copies a host's Instance but
// names another port (or name) is its own entry and never changes where
// the real one's Join goes; an exact copy from a lower address merges but
// the address heard first stays the Join.
func TestTableCopyDoesNotRedirect(t *testing.T) {
	tb := NewTable([8]byte{})
	now := time.Unix(1000, 0)
	realSrc, evil := netip.MustParseAddr("192.168.1.50"), netip.MustParseAddr("192.168.1.2")
	b := sample()
	tb.Add(realSrc, b, now)
	fake := b
	fake.Port = 4444
	tb.Add(evil, fake, now.Add(time.Second))
	tb.Add(realSrc, b, now.Add(2*time.Second))
	l := tb.List(now.Add(2 * time.Second))
	if len(l) != 2 || l[0].Key() == l[1].Key() {
		t.Fatalf("copy merged: %+v", l)
	}
	for _, e := range l {
		switch e.Port {
		case b.Port:
			if len(e.Addrs) != 1 || e.Addr() != netip.AddrPortFrom(realSrc, b.Port) {
				t.Fatalf("real entry = %+v", e)
			}
		case 4444:
			if len(e.Addrs) != 1 || e.Addr().Addr() != evil {
				t.Fatalf("copy entry = %+v", e)
			}
		default:
			t.Fatalf("entry %+v", e)
		}
	}
	tb2 := NewTable([8]byte{})
	tb2.Add(realSrc, b, now)
	tb2.Add(evil, b, now.Add(time.Second))
	if l := tb2.List(now.Add(time.Second)); len(l) != 1 || len(l[0].Addrs) != 2 || l[0].Addr() != netip.AddrPortFrom(realSrc, b.Port) {
		t.Fatalf("exact copy = %+v", l)
	}
}

func pfx(s string) netip.Prefix { return netip.MustParsePrefix(s) }

func TestBroadcastAddrs(t *testing.T) {
	ifs := []Iface{
		{Name: "Ethernet", Up: true, Broadcast: true, Addrs: []netip.Prefix{pfx("192.168.1.5/24"), pfx("fe80::1/64")}},
		{Name: "Radmin VPN", Up: true, Broadcast: true, Addrs: []netip.Prefix{pfx("26.12.34.56/8")}},
		{Name: "Wi-Fi (down)", Up: false, Broadcast: true, Addrs: []netip.Prefix{pfx("10.0.0.9/16")}},
		{Name: "Loopback", Up: true, Broadcast: true, Loopback: true, Addrs: []netip.Prefix{pfx("127.0.0.1/8")}},
		{Name: "Tailscale", Up: true, Broadcast: false, Addrs: []netip.Prefix{pfx("100.64.1.2/32")}},
		{Name: "Hotspot", Up: true, Broadcast: true, Addrs: []netip.Prefix{pfx("172.20.10.2/28"), pfx("172.20.10.3/28"), pfx("10.1.1.1/32"), pfx("10.2.2.2/31")}},
	}
	got := BroadcastAddrs(ifs, netip.Addr{})
	want := []string{"192.168.1.255", "26.255.255.255", "172.20.10.15"}
	if len(got) != len(want) {
		t.Fatalf("broadcasts = %v", got)
	}
	for i, w := range want {
		if got[i].String() != w {
			t.Errorf("broadcast %d = %v, want %s", i, got[i], w)
		}
	}
	if got := BroadcastAddrs(ifs, netip.MustParseAddr("26.12.34.56")); len(got) != 1 || got[0].String() != "26.255.255.255" {
		t.Errorf("within Radmin = %v", got)
	}
}

func TestSenderTargets(t *testing.T) {
	fake := func() ([]Iface, error) {
		return []Iface{{Up: true, Broadcast: true, Addrs: []netip.Prefix{pfx("192.168.1.5/24"), pfx("26.1.2.3/8")}}}, nil
	}
	for host, want := range map[string]string{
		"127.0.0.1":    "[127.255.255.255:25565 127.0.0.1:25565]",
		"":             "[192.168.1.255:25565 26.255.255.255:25565]",
		"0.0.0.0":      "[192.168.1.255:25565 26.255.255.255:25565]",
		"192.168.1.5":  "[192.168.1.255:25565]",
		"not-an-addr!": "[192.168.1.255:25565 26.255.255.255:25565]",
	} {
		s := &Sender{o: Options{Host: host}, ifaces: fake}
		if got := fmtAddrs(s.Targets()); got != want {
			t.Errorf("host %q: targets %s, want %s", host, got, want)
		}
	}
	s := &Sender{o: Options{Host: "127.0.0.1", Port: 40000}, ifaces: fake}
	if got := fmtAddrs(s.Targets()); got != "[127.255.255.255:40000 127.0.0.1:40000]" {
		t.Errorf("port: %s", got)
	}
}

func fmtAddrs(a []netip.AddrPort) string {
	var s []string
	for _, x := range a {
		s = append(s, x.String())
	}
	return "[" + strings.Join(s, " ") + "]"
}

func TestInterfacesRuns(t *testing.T) {
	ifs, err := Interfaces()
	if err != nil {
		t.Skip("no interfaces here:", err)
	}
	for _, f := range ifs {
		for _, p := range f.Addrs {
			if !p.Addr().Is4() || p.Bits() < 0 || p.Bits() > 32 {
				t.Errorf("%s: %v", f.Name, p)
			}
		}
	}
}

// freeUDPPort is a UDP port free on 127.0.0.1 a moment ago.
func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

// TestLoopbackDiscovery: a sender and two listeners on 127.0.0.1 (nothing
// reaches the network): both listeners share the port and both hear the
// beacon (the loopback broadcast; a unicast datagram would reach only one
// on Windows), and a listener skips the beacons of its own app.
func TestLoopbackDiscovery(t *testing.T) {
	o := Options{Host: "127.0.0.1", Port: freeUDPPort(t)}
	tb := NewTable([8]byte{0xAA})
	l, err := Listen(o, tb, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	tb2 := NewTable([8]byte{0xAA})
	l2, err := Listen(o, tb2, time.Now)
	if err != nil {
		t.Fatalf("a second listener on the port: %v", err)
	}
	defer l2.Close()
	s, err := NewSender(o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if la := s.conn.LocalAddr().(*net.UDPAddr); !la.IP.IsLoopback() {
		t.Fatalf("sender bound %v", la)
	}
	own := sample()
	own.Instance = [8]byte{0xAA}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var n atomic.Int64
	go s.Run(ctx, 20*time.Millisecond, func() (Beacon, bool) {
		if n.Add(1)%2 == 0 {
			return own, true
		}
		return sample(), true
	})
	// Junk on the port is ignored.
	junk, _ := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: o.Port})
	if junk != nil {
		junk.Write([]byte("WWLB garbage"))
		junk.Write(make([]byte, 4000))
		junk.Close()
	}
	// Windows delivers the loopback broadcast to every listener; other
	// systems may deliver only the unicast, to one of them.
	need := 1
	if runtime.GOOS == "windows" {
		need = 2
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		heard := 0
		for _, tbl := range []*Table{tb, tb2} {
			if l := tbl.List(time.Now()); len(l) == 1 {
				if l[0].Host != "Tetra" || l[0].Addr() != netip.MustParseAddrPort("127.0.0.1:25570") {
					t.Fatalf("heard %+v", l[0])
				}
				heard++
			} else if len(l) > 1 {
				t.Fatalf("heard %+v", l)
			}
		}
		if heard >= need {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the beacon wasn't heard by %d listener(s) on loopback", need)
}

func FuzzDecodeBeacon(f *testing.F) {
	good, _ := Encode(sample())
	f.Add(good)
	f.Add([]byte("WWLB"))
	f.Add(append(append([]byte(nil), good[:headerLen]...), 255, 1))
	newer := append([]byte(nil), good...)
	newer[4] = 7
	f.Add(append(newer, 0xde, 0xad))
	f.Fuzz(func(t *testing.T, p []byte) {
		b, err := Decode(p)
		if err != nil {
			return
		}
		if b.Port == 0 || b.Format < 1 || len(b.Host) > maxNameLen || len(b.Version) > maxVersionLen {
			t.Fatalf("decoded off-format %+v", b)
		}
		if clean(b.Host) != b.Host || clean(b.Version) != b.Version {
			t.Fatalf("unclean strings %q %q", b.Host, b.Version)
		}
		if b.Phase == PhaseUnknown {
			return // a newer format's phase can't be written back
		}
		q, err := Encode(b)
		if err != nil {
			t.Fatalf("decoded %+v doesn't encode: %v", b, err)
		}
		c, err := Decode(q)
		b.Format = FormatVersion
		if err != nil || c != b {
			t.Fatalf("re-decode %+v, %v; want %+v", c, err, b)
		}
	})
}
