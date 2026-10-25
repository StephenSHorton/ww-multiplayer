package network

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
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
// ours throttles per source address (an IPv4 address, or an IPv6 /64)
// across reconnects and never refuses the right key: the real owner may
// share that address with whoever is guessing. The key's entropy is the
// defense (MinOwnerKeyLen; `server` makes a 128-bit one by default). The role it grants is held under the
// member's token like any host slot, so a reclaim keeps it without the
// key being checked (or counted) again.

// Owner key limits.
const (
	// MaxOwnerKeyLen caps an owner key in bytes, on the command line and
	// in a HELLO (refused before hashing).
	MaxOwnerKeyLen = 256
	// MinOwnerKeyLen is the shortest key `server` takes. The key's
	// entropy is the defense; the per-address throttle below only slows
	// guessing. `server` with no key makes a random one
	// (GenerateOwnerKey, 128 bits).
	MinOwnerKeyLen = 16
	// generatedOwnerKeyBytes is GenerateOwnerKey's randomness: 128 bits,
	// 32 hex digits.
	generatedOwnerKeyBytes = 16
	// A wrong key slows the next ones from its address: after n wrong
	// keys, a HELLO with a key waits until ownerKeyDelay(n) after the
	// last wrong one before its key is compared. The wait never refuses
	// a key and is capped at ownerKeyDelayMax (well under the client's
	// 15 s HELLO deadline, clientReadTimeout), so the real owner behind
	// an address shared with whoever is guessing (a NAT, a Docker
	// gateway, a local proxy) still gets in. An address with no wrong
	// key for ownerKeyForget starts over.
	ownerKeyDelayBase = 250 * time.Millisecond
	ownerKeyDelayMax  = 8 * time.Second
	ownerKeyForget    = 10 * time.Minute
	// ownerKeyMaxAddrs caps the addresses the limiter remembers; past it
	// the forgotten ones go, then the one with the fewest wrong keys
	// (the oldest of those), so a spray of fresh addresses doesn't push
	// out a throttle that is doing its job.
	ownerKeyMaxAddrs = 1024
)

// NoticeOwnerKey answers a HELLO whose ownerKey was not taken (wrong, or
// the server has none). It is not fatal: the member joined without the
// host role.
const NoticeOwnerKey = "owner_key"

// ownerKeyVerdict is what the hub made of a HELLO's ownerKey.
type ownerKeyVerdict int

const (
	keyNone     ownerKeyVerdict = iota // no ownerKey in the HELLO, or a reclaim
	keyAccepted                        // the server's key
	keyWrong                           // another key (or one over MaxOwnerKeyLen)
	keyUnset                           // the server has no owner key
)

// ValidateOwnerKey checks a key for `server --owner-key` / WW_OWNER_KEY.
func ValidateOwnerKey(key string) error {
	switch {
	case len(key) < MinOwnerKeyLen:
		return fmt.Errorf("the owner key is %d bytes; use at least %d (a long random one, e.g. openssl rand -hex 16, or set none and the server makes one)", len(key), MinOwnerKeyLen)
	case len(key) > MaxOwnerKeyLen:
		return fmt.Errorf("the owner key is %d bytes; at most %d", len(key), MaxOwnerKeyLen)
	}
	return nil
}

// GenerateOwnerKey returns a random owner key: 128 bits from crypto/rand
// as 32 hex digits. `server` uses it when no key is configured.
func GenerateOwnerKey() (string, error) {
	b := make([]byte, generatedOwnerKeyBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("making an owner key: %w", err)
	}
	return hex.EncodeToString(b), nil
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

// judgeOwnerKey is the verdict on key against want (nil: the server has
// no key). It records nothing: the hub records it (ownerKeyLimiter.record)
// only once the HELLO is admitted.
func judgeOwnerKey(want *[sha256.Size]byte, key string) ownerKeyVerdict {
	switch {
	case key == "":
		return keyNone
	case want == nil:
		return keyUnset
	case ownerKeyMatches(want, key):
		return keyAccepted
	}
	return keyWrong
}

// ownerKeyDelay is the throttle after n wrong keys: 0 for none, then
// ownerKeyDelayBase doubling per wrong key, capped at ownerKeyDelayMax.
func ownerKeyDelay(n int) time.Duration {
	if n <= 0 {
		return 0
	}
	d := ownerKeyDelayBase
	for i := 1; i < n && d < ownerKeyDelayMax; i++ {
		d *= 2
	}
	return min(d, ownerKeyDelayMax)
}

// keyFails is one address's run of wrong keys.
type keyFails struct {
	n    int
	last time.Time
}

// ownerKeyLimiter throttles owner keys per source prefix. Pure (the
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

// entry is ip's live run of wrong keys at now (nil: none, or forgotten).
func (l *ownerKeyLimiter) entry(ip netip.Addr, now time.Time) *keyFails {
	p := ownerKeyPrefix(ip)
	f := l.fails[p]
	if f != nil && now.Sub(f.last) >= ownerKeyForget {
		delete(l.fails, p)
		return nil
	}
	return f
}

// wait is how long a key from ip arriving at now waits before it is
// compared: until ownerKeyDelay(n) after the address's last wrong key.
// It is never more than ownerKeyDelayMax and never a refusal.
func (l *ownerKeyLimiter) wait(ip netip.Addr, now time.Time) time.Duration {
	f := l.entry(ip, now)
	if f == nil {
		return 0
	}
	return min(ownerKeyDelayMax, max(0, f.last.Add(ownerKeyDelay(f.n)).Sub(now)))
}

// record notes the verdict on an admitted HELLO's key from ip: a wrong
// key counts, the right one clears the address. Others count for
// nothing (no key, a reclaim, a server with none: nothing to guess).
func (l *ownerKeyLimiter) record(ip netip.Addr, v ownerKeyVerdict, now time.Time) {
	switch v {
	case keyAccepted:
		delete(l.fails, ownerKeyPrefix(ip))
	case keyWrong:
		f := l.entry(ip, now)
		if f == nil {
			l.makeRoom(now)
			f = &keyFails{}
			l.fails[ownerKeyPrefix(ip)] = f
		}
		f.n++
		f.last = now
	}
}

// makeRoom drops the forgotten entries, then the ones with the fewest
// wrong keys (the oldest first among those), while the map is at
// ownerKeyMaxAddrs.
func (l *ownerKeyLimiter) makeRoom(now time.Time) {
	if len(l.fails) < ownerKeyMaxAddrs {
		return
	}
	for p, f := range l.fails {
		if now.Sub(f.last) >= ownerKeyForget {
			delete(l.fails, p)
		}
	}
	for len(l.fails) >= ownerKeyMaxAddrs {
		var victim netip.Prefix
		var vf *keyFails
		for p, f := range l.fails {
			if vf == nil || f.n < vf.n || (f.n == vf.n && f.last.Before(vf.last)) {
				victim, vf = p, f
			}
		}
		delete(l.fails, victim)
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

// ownerKeyWaitLocked is how long the HELLO hb under name from addr waits
// before its key is judged (ownerKeyLimiter.wait). A HELLO with no key,
// a lobby with none, and a reclaim of a held slot (its token and name)
// don't wait. The caller holds h.mu.
func (h *LobbyHub) ownerKeyWaitLocked(addr net.Addr, name string, hb LobbyHelloBody) time.Duration {
	if hb.OwnerKey == "" || h.ownerKey == nil || h.m.isReclaim(name, hb.Token) {
		return 0
	}
	return h.keyFails.wait(h.remoteIP(addr), h.now())
}

// judgeOwnerKeyLocked judges a HELLO's ownerKey for the connection named
// name, recording nothing (recordOwnerKeyLocked does, once helloAs has
// admitted it). A reclaim of a held slot (its token and name) is not
// judged: the token is the member's proof, and the app sends its HELLO,
// key and all, again on every reconnect, so a mistyped key would
// otherwise count per blip. The caller holds h.mu.
func (h *LobbyHub) judgeOwnerKeyLocked(name string, hb LobbyHelloBody) ownerKeyVerdict {
	if hb.OwnerKey == "" || h.m.isReclaim(name, hb.Token) {
		return keyNone
	}
	return judgeOwnerKey(h.ownerKey, hb.OwnerKey)
}

// recordOwnerKeyLocked records the verdict on an admitted HELLO's key
// from addr. A refused HELLO (name_taken, full, kicked, mods_differ) is
// never recorded, so the app's name_taken retry, which sends the same
// key again, counts once. The caller holds h.mu.
func (h *LobbyHub) recordOwnerKeyLocked(addr net.Addr, v ownerKeyVerdict) {
	h.keyFails.record(h.remoteIP(addr), v, h.now())
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
