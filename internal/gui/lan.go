package gui

import (
	"context"
	"crypto/rand"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/config"
	"github.com/StephenSHorton/ww-multiplayer/internal/lan"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
)

// "Sessions on your network" (App PR 11, design §2.2-§2.3, decision 12):
// while this app hosts a lobby it sends a beacon (internal/lan) every
// lan.BeaconInterval; while Home is shown (the page polls LanSessions) it
// listens for other hosts' beacons and lists them. A listed session's
// address is masked like any other (mask.go), and its host's name too, as
// a peer's name always is.

const (
	// EnvLanPort is the beacons' UDP port (default lan.DefaultPort).
	EnvLanPort = "WW_LAN_PORT"
	// EnvLan set to "off" turns LAN discovery off: no beacon, no
	// listening.
	EnvLan = "WW_LAN"
	// lanLease is how long the listener stays open after the page last
	// asked for the list (it asks every second while Home is shown).
	lanLease = 5 * time.Second
	// lanRetry is how long a listener that couldn't open waits before
	// the next try.
	lanRetry = 5 * time.Second
)

// LanSessionView is one session heard on the network.
type LanSessionView struct {
	// ID is the host app's instance, stable while it runs.
	ID string `json:"id"`
	// Host is the host's player name, masked as free text unless the
	// switch is on (a name may be an IP).
	Host   string `json:"host"`
	Addr   string `json:"addr"`   // for Join only: never drawn (mask.go)
	Masked string `json:"masked"` // Addr masked, always
	Kind   string `json:"kind"`   // "lan", "radmin", "tailscale", "other" (the share card's)
	Label  string `json:"label"`
	// Players counts the lobby's members; Max is its limit.
	Players int `json:"players"`
	Max     int `json:"max"`
	// Phase is "lobby", "starting", "in game" or "unknown".
	Phase   string `json:"phase"`
	Coop    bool   `json:"coop"`
	Version string `json:"version"`
	// Compatible: the host speaks this build's network protocol. A
	// session that doesn't is shown greyed, with its version, and can't
	// be joined.
	Compatible bool `json:"compatible"`
}

// LanView is Home's "Sessions on your network".
type LanView struct {
	// On says the app is listening; Note says why not (turned off, or the
	// port is taken), or is empty.
	On       bool             `json:"on"`
	Note     string           `json:"note,omitempty"`
	Sessions []LanSessionView `json:"sessions"`
	// ShowIPs is the switch the view was masked under.
	ShowIPs bool `json:"showIPs"`
}

// lanTestHost, when set, is the discovery address of a Bridge whose
// environment names none: the package's tests set it to 127.0.0.1
// (TestMain), so no test sends a beacon to, or listens on, the network.
var lanTestHost string

// lanState is the Bridge's discovery side.
type lanState struct {
	once  sync.Once
	self  [8]byte
	table *lan.Table

	mu       sync.Mutex
	ln       *lan.Listener
	lease    time.Time
	failedAt time.Time
	failure  string
	// beacon stops the running session's beacon.
	beacon context.CancelFunc
	// reapers counts the running lanReaper goroutines (for tests).
	reapers atomic.Int32
}

func (b *Bridge) lanInit() {
	b.lan.once.Do(func() {
		_, _ = rand.Read(b.lan.self[:])
		b.lan.table = lan.NewTable(b.lan.self)
		b.lan.table.Rank = func(a netip.Addr) int {
			kind, _ := addrKind(a.String())
			return shareOrder[kind]
		}
	})
}

// lanOptions is where discovery runs: WW_LISTEN_HOST (127.0.0.1 keeps it
// on this machine) and WW_LAN_PORT. off is non-empty, and says why, when
// discovery doesn't run: WW_LAN=off, or a WW_LISTEN_HOST it can't keep to.
func (b *Bridge) lanOptions() (o lan.Options, off string) {
	if strings.EqualFold(strings.TrimSpace(b.getenv(EnvLan)), "off") {
		return o, "Looking for sessions on your network is turned off (" + EnvLan + "=off)."
	}
	host, ok := lanHost(b.getenv(EnvListenHost))
	if !ok {
		return o, "Looking for sessions on your network is off: it runs on IPv4 only, and " + EnvListenHost + " names no IPv4 address."
	}
	o.Host = host
	if o.Host == "" {
		o.Host = lanTestHost
	}
	if p, err := strconv.Atoi(strings.TrimSpace(b.getenv(EnvLanPort))); err == nil && p > 0 && p <= 65535 {
		o.Port = p
	}
	return o, ""
}

// lanHost is where discovery runs for WW_LISTEN_HOST v: "" (every
// network) when v is empty or unspecified, an IPv4 address, or 127.0.0.1
// for "localhost" and IPv6 loopback. ok is false for any other name or
// IPv6 address: discovery is IPv4, and must never fall back to every
// network when the user named a host (it may be keeping the session on
// this machine).
func lanHost(v string) (host string, ok bool) {
	v = strings.Trim(strings.TrimSpace(v), "[]")
	if v == "" {
		return "", true
	}
	if strings.EqualFold(v, "localhost") {
		return "127.0.0.1", true
	}
	a, err := netip.ParseAddr(v)
	if err != nil || a.Zone() != "" {
		return "", false
	}
	a = a.Unmap()
	switch {
	case a.IsUnspecified():
		return "", true
	case a.Is4():
		return a.String(), true
	case a.IsLoopback():
		return "127.0.0.1", true
	}
	return "", false
}

// LanSessions lists the sessions heard on the network, and keeps
// listening for lanLease more (Home calls it every second). Nothing is
// listened for while a session runs.
func (b *Bridge) LanSessions() LanView {
	b.lanInit()
	mk := b.masker()
	v := LanView{Sessions: []LanSessionView{}, ShowIPs: mk.show}
	o, off := b.lanOptions()
	if off != "" {
		v.Note = off
		return v
	}
	if b.sessionRunning() {
		b.stopLanListen()
		return v
	}
	now := b.now()
	b.lan.mu.Lock()
	b.lan.lease = now.Add(lanLease)
	if b.lan.ln == nil && (b.lan.failedAt.IsZero() || now.Sub(b.lan.failedAt) >= lanRetry) {
		ln, err := lan.Listen(o, b.lan.table, b.now)
		if err != nil {
			b.lan.failedAt, b.lan.failure = now, err.Error()
		} else {
			b.lan.ln, b.lan.failedAt, b.lan.failure = ln, time.Time{}, ""
			go b.lanReaper(ln)
		}
	}
	v.On = b.lan.ln != nil
	if !v.On {
		v.Note = "Can't look for sessions on your network: " + mk.text(b.lan.failure)
	}
	b.lan.mu.Unlock()
	for _, e := range b.lan.table.List(now) {
		v.Sessions = append(v.Sessions, lanSessionView(e, mk))
	}
	return v
}

// sessionRunning is State().Running without the rest of the view.
func (b *Bridge) sessionRunning() bool {
	st := b.ctrl.Snapshot()
	return st.Session != 0 && st.Phase != session.Ended && st.Phase != session.Idle
}

// lanSessionView is e as Home lists it.
func lanSessionView(e lan.Entry, mk masker) LanSessionView {
	addr := e.Addr()
	ip := addr.Addr().String()
	join := joinAddr(addr)
	kind, label := addrKind(ip)
	return LanSessionView{
		ID: e.Key(), Host: mk.text(e.Host), Addr: join, Masked: hideIPs.addr(join),
		Kind: kind, Label: label, Players: int(e.Players), Max: int(e.Max), Phase: e.Phase.String(),
		Coop: e.Coop(), Version: e.Version, Compatible: e.Protocol == network.ProtocolVersion,
	}
}

// joinAddr is ap as the Join box takes it: the bare IP on the default
// port (as the share card and a typed address show it, so one host is one
// recent host), else ip:port.
func joinAddr(ap netip.AddrPort) string {
	if ap.Port() == config.DefaultPort {
		return ap.Addr().String()
	}
	return ap.String()
}

// lanReaper closes ln once the page stopped asking (its lease ran out)
// or a session started. It ends at once when ln is no longer the
// listener: stopLanListen closed it (a Host or Join that then failed
// leaves Home polling, which opens a new listener with its own reaper).
func (b *Bridge) lanReaper(ln *lan.Listener) {
	b.lan.reapers.Add(1)
	defer b.lan.reapers.Add(-1)
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-b.ctx.Done():
		case <-t.C:
			b.lan.mu.Lock()
			owned := b.lan.ln == ln
			expired := b.now().After(b.lan.lease)
			b.lan.mu.Unlock()
			if !owned {
				return
			}
			if !expired && !b.sessionRunning() {
				continue
			}
		}
		b.lan.mu.Lock()
		if b.lan.ln == ln {
			b.lan.ln = nil
		}
		b.lan.mu.Unlock()
		ln.Close()
		return
	}
}

// stopLanListen closes the listener now (a session starts).
func (b *Bridge) stopLanListen() {
	b.lan.mu.Lock()
	ln := b.lan.ln
	b.lan.ln = nil
	b.lan.mu.Unlock()
	if ln != nil {
		ln.Close()
	}
}

// startBeacon announces the lobby session id, hosted on port, until it
// ends (or stopBeacon). A socket that can't open means no beacon: the
// share card still works.
func (b *Bridge) startBeacon(id session.ID, port int) {
	b.lanInit()
	b.stopBeacon()
	o, off := b.lanOptions()
	if off != "" {
		return
	}
	s, err := lan.NewSender(o)
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(b.ctx)
	b.lan.mu.Lock()
	b.lan.beacon = cancel
	b.lan.mu.Unlock()
	go func() {
		defer s.Close()
		s.Run(ctx, lan.BeaconInterval, func() (lan.Beacon, bool) {
			return b.beacon(id, port)
		})
	}()
}

// stopBeacon stops the running beacon, if any.
func (b *Bridge) stopBeacon() {
	b.lan.mu.Lock()
	c := b.lan.beacon
	b.lan.beacon = nil
	b.lan.mu.Unlock()
	if c != nil {
		c()
	}
}

// beacon is session id's beacon now; false once it has ended.
func (b *Bridge) beacon(id session.ID, port int) (lan.Beacon, bool) {
	st := b.ctrl.Snapshot()
	if st.Session != id || st.Phase == session.Ended || st.Phase == session.Idle {
		return lan.Beacon{}, false
	}
	bc := lan.Beacon{Format: lan.FormatVersion, Protocol: network.ProtocolVersion, Port: uint16(port),
		Max: network.MaxLobbyMembers, Instance: b.lan.self, Version: b.version, Host: st.Name}
	if l := st.Lobby; l != nil {
		n := 0
		for _, m := range l.Members {
			if !m.Away {
				n++
			}
		}
		bc.Players = byte(min(n, 255))
		switch l.Phase {
		case session.LobbyPhaseStarting:
			bc.Phase = lan.PhaseStarting
		case session.LobbyPhaseInGame:
			bc.Phase = lan.PhaseInGame
		}
		if c := l.Settings.Coop; c != "" && c != "off" {
			bc.Flags |= lan.FlagCoop
		}
	}
	return bc, true
}
