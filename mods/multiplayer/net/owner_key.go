package network

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"
)

// The lobby owner key (docs/10-lobby.md §3 "Owner key"; docs/11-self-hosting.md).
// A dedicated `server` has no member on its own machine, so without a key
// its lobby has no host (START, GO, SETTINGS and KICK get not_host). With
// one (`server --owner-key`, WW_OWNER_KEY) a member whose HELLO carries
// the same key in ownerKey can be host, wherever it connects from, and a
// connection from this machine no longer can: behind a local TCP proxy
// (a tunnel, `tailscale serve`, socat) every connection is loopback.
// Without a key nothing changes.
//
// The check is the idea of datstarkey/ww-online's OwnerKeyCheck
// (WWOnline.Server/Hubs/OwnerKeyCheck.cs, MIT), written again here: SHA-256
// of both sides compared in fixed time, so neither the key's content nor
// its length shows in the timing, and an over-long key refused before it
// is hashed. Their attempt cap is per connection and resets on reconnect;
// ours counts wrong keys per source address (an IPv4 address, or an IPv6
// /64) and survives reconnects. The role it grants is held under the
// member's token like any host slot, so a reclaim keeps it without the
// key being checked (or counted) again.

// Owner key limits.
const (
	// MaxOwnerKeyLen caps an owner key in bytes, on the command line and
	// in a HELLO (refused before hashing).
	MaxOwnerKeyLen = 256
	// MinOwnerKeyLen is the shortest key `server` takes: the per-address
	// limit slows guessing, it doesn't make a short key safe.
	MinOwnerKeyLen = 8
	// ownerKeyMaxFails wrong keys from one address within
	// ownerKeyLockout of each other lock it out: its keys are ignored,
	// not compared, until ownerKeyLockout after the last wrong one.
	ownerKeyMaxFails = 5
	ownerKeyLockout  = 10 * time.Minute
	// ownerKeyMaxAddrs caps the addresses the limiter remembers; past it
	// the stale ones go, then the oldest.
	ownerKeyMaxAddrs = 1024
)

// NoticeOwnerKey answers a HELLO whose ownerKey was not taken (wrong,
// locked out, or the server has none). It is not fatal: the member joined
// without the host role.
const NoticeOwnerKey = "owner_key"

// ownerKeyVerdict is what the hub made of a HELLO's ownerKey.
type ownerKeyVerdict int

const (
	keyNone      ownerKeyVerdict = iota // no ownerKey in the HELLO, or a reclaim
	keyAccepted                         // the server's key
	keyWrong                            // another key (or one over MaxOwnerKeyLen)
	keyLocked                           // this address is locked out; not compared
	keyLockedNow                        // wrong, and that locked this address out
	keyUnset                            // the server has no owner key
)

// ValidateOwnerKey checks a key for `server --owner-key` / WW_OWNER_KEY.
func ValidateOwnerKey(key string) error {
	switch {
	case len(key) < MinOwnerKeyLen:
		return fmt.Errorf("the owner key is %d bytes; use at least %d (a long random one, e.g. openssl rand -hex 16)", len(key), MinOwnerKeyLen)
	case len(key) > MaxOwnerKeyLen:
		return fmt.Errorf("the owner key is %d bytes; at most %d", len(key), MaxOwnerKeyLen)
	}
	return nil
}

// ownerKeyMatches compares key with the hash of the server's key in fixed
// time. A key that is empty or over MaxOwnerKeyLen never matches and is
// not hashed.
func ownerKeyMatches(want *[sha256.Size]byte, key string) bool {
	if want == nil || key == "" || len(key) > MaxOwnerKeyLen {
		return false
	}
	got := sha256.Sum256([]byte(key))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}

// keyFails is one address's run of wrong keys.
type keyFails struct {
	n    int
	last time.Time
}

// ownerKeyLimiter counts wrong owner keys per source prefix. Pure (the
// caller passes the clock); not safe for concurrent use: LobbyHub holds
// its mu around it.
type ownerKeyLimiter struct {
	fails map[netip.Prefix]*keyFails
}

func newOwnerKeyLimiter() *ownerKeyLimiter {
	return &ownerKeyLimiter{fails: map[netip.Prefix]*keyFails{}}
}

// ownerKeyPrefix is the unit the limiter counts by: the IPv4 address, or
// the IPv6 /64 (one host is usually given a whole /64). An address that
// doesn't parse counts as the zero prefix: one bucket for all of those.
func ownerKeyPrefix(ip netip.Addr) netip.Prefix {
	ip = ip.Unmap()
	bits := 32
	if ip.Is6() {
		bits = 64
	}
	if !ip.IsValid() {
		return netip.Prefix{}
	}
	p, _ := ip.Prefix(bits)
	return p
}

// check judges key from ip at now against want (nil: the server has no
// key) and records a wrong one.
func (l *ownerKeyLimiter) check(want *[sha256.Size]byte, ip netip.Addr, key string, now time.Time) ownerKeyVerdict {
	if key == "" {
		return keyNone
	}
	if want == nil {
		// Nothing to guess: not counted.
		return keyUnset
	}
	p := ownerKeyPrefix(ip)
	f := l.fails[p]
	if f != nil && now.Sub(f.last) >= ownerKeyLockout {
		delete(l.fails, p)
		f = nil
	}
	if f != nil && f.n >= ownerKeyMaxFails {
		return keyLocked
	}
	if ownerKeyMatches(want, key) {
		delete(l.fails, p)
		return keyAccepted
	}
	if f == nil {
		l.makeRoom(now)
		f = &keyFails{}
		l.fails[p] = f
	}
	f.n++
	f.last = now
	if f.n >= ownerKeyMaxFails {
		return keyLockedNow
	}
	return keyWrong
}

// makeRoom drops the stale entries, then the oldest, while the map is at
// ownerKeyMaxAddrs.
func (l *ownerKeyLimiter) makeRoom(now time.Time) {
	if len(l.fails) < ownerKeyMaxAddrs {
		return
	}
	for p, f := range l.fails {
		if now.Sub(f.last) >= ownerKeyLockout {
			delete(l.fails, p)
		}
	}
	for len(l.fails) >= ownerKeyMaxAddrs {
		var oldest netip.Prefix
		var at time.Time
		first := true
		for p, f := range l.fails {
			if first || f.last.Before(at) {
				oldest, at, first = p, f.last, false
			}
		}
		delete(l.fails, oldest)
	}
}

// remoteIP is a connection's source address for the limiter.
func remoteIP(addr net.Addr) netip.Addr {
	if ta, ok := addr.(*net.TCPAddr); ok {
		if ap := ta.AddrPort(); ap.IsValid() {
			return ap.Addr().Unmap()
		}
	}
	if addr != nil {
		if ap, err := netip.ParseAddrPort(addr.String()); err == nil {
			return ap.Addr().Unmap()
		}
	}
	return netip.Addr{}
}

// ownerKeyNoticeText is the notice for a verdict that didn't grant the
// role ("" for one that needs none).
func ownerKeyNoticeText(v ownerKeyVerdict) string {
	switch v {
	case keyWrong:
		return "the owner key was not accepted; you joined as a player"
	case keyLocked, keyLockedNow:
		return fmt.Sprintf("too many wrong owner keys from your address; you joined as a player (try again in %d minutes)", int(ownerKeyLockout/time.Minute))
	case keyUnset:
		return "this server has no owner key; you joined as a player"
	}
	return ""
}

// errOwnerKeyAfterStart refuses SetOwnerKey once a lobby connection exists.
var errOwnerKeyAfterStart = errors.New("the owner key must be set before the lobby has members")

// SetOwnerKey sets the lobby's owner key (docs/10 §3 "Owner key"): a
// member whose HELLO carries it can be host, and a member from this
// machine no longer can. "" clears it (the loopback rule again). The key
// itself is not kept, only its SHA-256. Set it before Start, like
// SetMods; with lobby members already in it is refused.
func (s *Server) SetOwnerKey(key string) error {
	if key != "" {
		if err := ValidateOwnerKey(key); err != nil {
			return err
		}
	}
	h := s.lobby
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.m.members) > 0 || len(h.conns) > 0 {
		return errOwnerKeyAfterStart
	}
	if key == "" {
		h.ownerKey = nil
	} else {
		sum := sha256.Sum256([]byte(key))
		h.ownerKey = &sum
	}
	h.m.keyed = h.ownerKey != nil
	h.keyFails = newOwnerKeyLimiter()
	return nil
}

// HasOwnerKey reports whether the lobby has an owner key.
func (s *Server) HasOwnerKey() bool {
	h := s.lobby
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ownerKey != nil
}

// checkOwnerKeyLocked judges a HELLO's ownerKey for the connection from
// addr named name. A reclaim of a held slot (its token and name) is not
// checked: the token is the member's proof, and the app sends its HELLO,
// key and all, again on every reconnect, so a mistyped key would
// otherwise cost an attempt per blip. The caller holds h.mu.
func (h *LobbyHub) checkOwnerKeyLocked(addr net.Addr, name string, hb LobbyHelloBody) ownerKeyVerdict {
	if hb.OwnerKey == "" || h.m.isReclaim(name, hb.Token) {
		return keyNone
	}
	return h.keyFails.check(h.ownerKey, h.remoteIP(addr), hb.OwnerKey, h.now())
}

// DenyCoop makes the lobby refuse co-op in SETTINGS (NoticeNoCoop) with
// reason: a dedicated `server` has no co-op hub, which only the hosting
// app's own server process can set (SetCoop), so a host's co-op START
// would never get its GO. "" allows co-op again.
func (s *Server) DenyCoop(reason string) {
	h := s.lobby
	h.mu.Lock()
	h.m.noCoop = reason
	h.mu.Unlock()
}
