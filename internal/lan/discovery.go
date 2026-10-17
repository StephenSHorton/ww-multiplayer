package lan

import (
	"context"
	"encoding/hex"
	"errors"
	"hash/fnv"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Options says where beacons go and where they are heard.
type Options struct {
	// Host limits discovery to one address, as WW_LISTEN_HOST does the
	// lobby's server. "" (or 0.0.0.0) is every network: the listener binds
	// every interface and the sender broadcasts on each. A loopback
	// address (127.0.0.1, the local checks) keeps it all on this machine:
	// the listener binds it and the sender sends to it, nothing reaches
	// the network. Any other address binds the listener to it and
	// broadcasts only on the networks that hold it.
	Host string
	// Port is the beacons' UDP port; 0 is DefaultPort.
	Port int
}

func (o Options) port() int {
	if o.Port <= 0 || o.Port > 65535 {
		return DefaultPort
	}
	return o.Port
}

// host is o.Host as an address; ok is false for every interface.
func (o Options) host() (netip.Addr, bool) {
	a, err := netip.ParseAddr(o.Host)
	if err != nil || a.IsUnspecified() {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}

// Iface is one network interface, as BroadcastAddrs reads it.
type Iface struct {
	Name      string
	Up        bool
	Broadcast bool
	Loopback  bool
	// Addrs are its addresses with their prefixes (IPv6 ones are skipped).
	Addrs []netip.Prefix
}

// Interfaces lists this machine's interfaces.
func Interfaces() ([]Iface, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []Iface
	for _, i := range ifs {
		f := Iface{Name: i.Name, Up: i.Flags&net.FlagUp != 0, Broadcast: i.Flags&net.FlagBroadcast != 0, Loopback: i.Flags&net.FlagLoopback != 0}
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			v4 := n.IP.To4()
			if v4 == nil {
				continue
			}
			mask := n.Mask
			if len(mask) == net.IPv6len {
				mask = mask[12:]
			}
			ones, bits := mask.Size()
			if bits != 32 {
				continue
			}
			ip, _ := netip.AddrFromSlice(v4)
			f.Addrs = append(f.Addrs, netip.PrefixFrom(ip, ones))
		}
		out = append(out, f)
	}
	return out, nil
}

// BroadcastAddrs is the directed broadcast address of every IPv4 network
// on an interface that is up, can broadcast and isn't loopback (a LAN,
// Radmin VPN's 26.0.0.0/8, a hotspot), each once. A /31 or /32 has none.
// within, when valid, keeps only the networks that hold it.
func BroadcastAddrs(ifs []Iface, within netip.Addr) []netip.Addr {
	seen := map[netip.Addr]bool{}
	var out []netip.Addr
	for _, f := range ifs {
		if !f.Up || !f.Broadcast || f.Loopback {
			continue
		}
		for _, p := range f.Addrs {
			ip := p.Addr().Unmap()
			if !ip.Is4() || ip.IsLoopback() || p.Bits() < 1 || p.Bits() > 30 {
				continue
			}
			if within.IsValid() && !netip.PrefixFrom(ip, p.Bits()).Masked().Contains(within) {
				continue
			}
			b := ip.As4()
			host := uint32(1)<<(32-p.Bits()) - 1
			v := (uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])) | host
			bc := netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
			if !seen[bc] {
				seen[bc] = true
				out = append(out, bc)
			}
		}
	}
	return out
}

// ---------- the joiner's side ----------

// Entry is one session a joiner heard.
type Entry struct {
	Beacon
	// Addrs are where the host's lobby was heard from (each source
	// address with the beacon's port), best first (Table.Rank, then the
	// one heard first), at least one.
	Addrs []netip.AddrPort
	// Seen is the last beacon's time.
	Seen time.Time
}

// Addr is where to join: the best of Addrs.
func (e Entry) Addr() netip.AddrPort { return e.Addrs[0] }

// Key tells entries apart: the host's Instance and what its beacons say
// about who and where it is. Two beacons with one Instance that disagree
// on those are two entries (one is a copy), so Key is unique in a List.
func (e Entry) Key() string { return keyOf(e.Beacon).String() }

const (
	maxEntries      = 64 // sessions a table keeps; more are dropped until some expire
	maxAddrsPerHost = 8
	// maxPerSource caps the sessions one source address can have listed:
	// a real host sends one (one Instance), so a flood from one machine
	// can't fill the table and hide the others.
	maxPerSource = 4
)

// entryKey is what a beacon merges on: one host's Instance and the
// fields that say who it is and where its lobby listens. A beacon that
// copies an Instance but says something else (another port, name or
// version) is a separate entry, so it can't redirect a real host's row.
type entryKey struct {
	inst     [8]byte
	host     string
	version  string
	port     uint16
	protocol uint16
}

func keyOf(b Beacon) entryKey {
	return entryKey{inst: b.Instance, host: b.Host, version: b.Version, port: b.Port, protocol: b.Protocol}
}

func (k entryKey) String() string {
	h := fnv.New32a()
	h.Write([]byte(k.host))
	h.Write([]byte{0})
	h.Write([]byte(k.version))
	h.Write([]byte{byte(k.port >> 8), byte(k.port), byte(k.protocol >> 8), byte(k.protocol)})
	return hex.EncodeToString(k.inst[:]) + "-" + strconv.FormatUint(uint64(h.Sum32()), 16)
}

// heard is when an address of an entry was first and last heard from.
type heard struct{ first, last time.Time }

type entry struct {
	b     Beacon
	seen  time.Time
	addrs map[netip.Addr]*heard
}

// Table is the sessions a joiner heard, by the hosts' Instance (and what
// their beacons say, entryKey). It is safe for concurrent use.
type Table struct {
	self [8]byte
	// Rank orders a host's addresses (lower first); nil keeps the order
	// they were first heard in.
	Rank func(netip.Addr) int

	mu      sync.Mutex
	entries map[entryKey]*entry
}

// NewTable returns a table that ignores the beacons of self (this app's
// own Instance).
func NewTable(self [8]byte) *Table {
	return &Table{self: self, entries: map[entryKey]*entry{}}
}

// Add records b, heard from src at now. It returns false for one of this
// app's own beacons, for a new host while the table is full, and for a
// source that already has maxPerSource sessions listed.
func (t *Table) Add(src netip.Addr, b Beacon, now time.Time) bool {
	if b.Instance == t.self {
		return false
	}
	src = src.Unmap()
	t.mu.Lock()
	defer t.mu.Unlock()
	k := keyOf(b)
	e := t.entries[k]
	if e == nil {
		t.pruneLocked(now)
		if len(t.entries) >= maxEntries || t.fromLocked(src, now) >= maxPerSource {
			return false
		}
		e = &entry{addrs: map[netip.Addr]*heard{}}
		t.entries[k] = e
	}
	h := e.addrs[src]
	if h == nil || now.Sub(h.last) > EntryTTL {
		for a, at := range e.addrs { // drop the addresses that went quiet
			if now.Sub(at.last) > EntryTTL {
				delete(e.addrs, a)
			}
		}
		if len(e.addrs) >= maxAddrsPerHost || t.fromLocked(src, now) >= maxPerSource {
			if len(e.addrs) == 0 {
				delete(t.entries, k)
			}
			return false
		}
		h = &heard{first: now}
		e.addrs[src] = h
	}
	e.b, e.seen = b, now
	h.last = now
	return true
}

// fromLocked counts the live entries src was heard for.
func (t *Table) fromLocked(src netip.Addr, now time.Time) int {
	n := 0
	for _, e := range t.entries {
		if h := e.addrs[src]; h != nil && now.Sub(h.last) <= EntryTTL {
			n++
		}
	}
	return n
}

func (t *Table) pruneLocked(now time.Time) {
	for k, e := range t.entries {
		if now.Sub(e.seen) > EntryTTL {
			delete(t.entries, k)
		}
	}
}

// List is the sessions heard within EntryTTL of now, by host name.
func (t *Table) List(now time.Time) []Entry {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pruneLocked(now)
	out := make([]Entry, 0, len(t.entries))
	for _, e := range t.entries {
		en := Entry{Beacon: e.b, Seen: e.seen}
		var ips []netip.Addr
		for a, h := range e.addrs {
			if now.Sub(h.last) <= EntryTTL {
				ips = append(ips, a)
			}
		}
		sort.Slice(ips, func(i, j int) bool {
			if t.Rank != nil {
				if ri, rj := t.Rank(ips[i]), t.Rank(ips[j]); ri != rj {
					return ri < rj
				}
			}
			// Within a rank the address heard first wins: a copy that
			// starts later can't take a real host's Join.
			if fi, fj := e.addrs[ips[i]].first, e.addrs[ips[j]].first; !fi.Equal(fj) {
				return fi.Before(fj)
			}
			return ips[i].Less(ips[j])
		})
		for _, a := range ips {
			en.Addrs = append(en.Addrs, netip.AddrPortFrom(a, e.b.Port))
		}
		if len(en.Addrs) > 0 {
			out = append(out, en)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Host != out[j].Host {
			return out[i].Host < out[j].Host
		}
		return out[i].Key() < out[j].Key()
	})
	return out
}

// Listener hears beacons into a Table until closed.
type Listener struct {
	conn *net.UDPConn
	done chan struct{}
}

// Listen binds the beacons' port (shared with other listeners on this
// machine, so two apps can both look) and adds what it hears to t, timed
// by now.
func Listen(o Options, t *Table, now func() time.Time) (*Listener, error) {
	host := ""
	if a, ok := o.host(); ok {
		host = a.String()
	}
	lc := net.ListenConfig{Control: reuseAddr}
	pc, err := lc.ListenPacket(context.Background(), "udp4", net.JoinHostPort(host, strconv.Itoa(o.port())))
	if err != nil {
		return nil, err
	}
	l := &Listener{conn: pc.(*net.UDPConn), done: make(chan struct{})}
	go l.run(t, now)
	return l, nil
}

func (l *Listener) run(t *Table, now func() time.Time) {
	defer close(l.done)
	buf := make([]byte, 2048)
	for {
		n, src, err := l.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			// A Windows ICMP "port unreachable" and the like: go on, but
			// never spin on an error that repeats.
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if n > MaxBeacon {
			continue
		}
		b, err := Decode(buf[:n])
		if err != nil {
			continue
		}
		t.Add(src.Addr(), b, now())
	}
}

// Close stops listening and waits for the reader to end.
func (l *Listener) Close() error {
	err := l.conn.Close()
	<-l.done
	return err
}

// ---------- the host's side ----------

// Sender sends a host's beacons.
type Sender struct {
	o    Options
	conn *net.UDPConn
	// ifaces lists the interfaces (Interfaces; a test's fake).
	ifaces func() ([]Iface, error)
}

// NewSender opens the socket beacons leave from: on o.Host when it names
// one, else an ephemeral port on every interface.
func NewSender(o Options) (*Sender, error) {
	laddr := &net.UDPAddr{}
	if a, ok := o.host(); ok {
		laddr.IP = a.AsSlice()
	}
	conn, err := net.ListenUDP("udp4", laddr)
	if err != nil {
		return nil, err
	}
	return &Sender{o: o, conn: conn, ifaces: Interfaces}, nil
}

// loopbackBroadcast is IPv4 loopback's broadcast address.
var loopbackBroadcast = netip.AddrFrom4([4]byte{127, 255, 255, 255})

// Targets is where a beacon goes now. On a loopback o.Host it is
// 127.255.255.255 and o.Host itself: on Windows a unicast datagram reaches
// only one of the listeners sharing the port, the loopback broadcast
// reaches each; where the broadcast isn't heard by a listener bound to
// o.Host (Linux) or can't be sent, the unicast still is (a listener that
// hears both just refreshes its entry). Else it is the broadcast address
// of each network (Options.Host).
func (s *Sender) Targets() []netip.AddrPort {
	port := uint16(s.o.port())
	h, ok := s.o.host()
	if ok && h.IsLoopback() {
		if !h.Is4() {
			return []netip.AddrPort{netip.AddrPortFrom(h, port)}
		}
		return []netip.AddrPort{netip.AddrPortFrom(loopbackBroadcast, port), netip.AddrPortFrom(h, port)}
	}
	ifs, err := s.ifaces()
	if err != nil {
		return nil
	}
	var within netip.Addr
	if ok {
		within = h
	}
	var out []netip.AddrPort
	for _, a := range BroadcastAddrs(ifs, within) {
		out = append(out, netip.AddrPortFrom(a, port))
	}
	return out
}

// Send sends b to every target and says to how many it went; err is the
// last failure, if none went.
func (s *Sender) Send(b Beacon) (int, error) {
	p, err := Encode(b)
	if err != nil {
		return 0, err
	}
	sent := 0
	var last error
	for _, to := range s.Targets() {
		if _, err := s.conn.WriteToUDPAddrPort(p, to); err != nil {
			last = err
			continue
		}
		sent++
	}
	if sent > 0 {
		last = nil
	}
	return sent, last
}

// Close closes the socket.
func (s *Sender) Close() error { return s.conn.Close() }

// Run sends next's beacon every interval (the first at once) until ctx
// ends or next says to stop; a beacon it can't send is skipped.
func (s *Sender) Run(ctx context.Context, interval time.Duration, next func() (b Beacon, ok bool)) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		b, ok := next()
		if !ok {
			return
		}
		_, _ = s.Send(b)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
