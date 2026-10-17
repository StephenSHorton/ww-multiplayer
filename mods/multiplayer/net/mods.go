package network

import (
	"encoding/hex"
	"strings"
)

// ModsHashLen is the length of a mods hash: a SHA-256.
const ModsHashLen = 32

// Mods is what a build tells a host about its mods (docs/api/02-mods.md,
// "mods_hash"): Hash, the SHA-256 of its client_required mods'
// "id@version" (wwapi.ModsID), and List, those strings sorted, which a
// refusal shows. The zero Mods is a build that sends no hash and checks
// none, as before mods.
type Mods struct {
	Hash [ModsHashLen]byte
	List []string
	// Known says Hash and List are set; false is the zero Mods.
	Known bool
}

// hashPtr is m's hash for a hello, nil when m is unknown.
func (m Mods) hashPtr() *[ModsHashLen]byte {
	if !m.Known {
		return nil
	}
	h := m.Hash
	return &h
}

// ModsHashHex is m's hash in lowercase hex (LobbyHelloBody.ModsHash), ""
// when m is unknown.
func (m Mods) ModsHashHex() string {
	if !m.Known {
		return ""
	}
	return hex.EncodeToString(m.Hash[:])
}

// modsText lists mods in plain words: "coop@1.0.0, multiplayer@1.0.0", or
// "no mods that every player needs".
func modsText(list []string) string {
	if len(list) == 0 {
		return "no mods that every player needs"
	}
	return strings.Join(list, ", ")
}

// modsDifferReason is the host's half of a mods refusal (MsgReject with
// RejectMods, NoticeModsDiffer): the host's list. The refused client adds
// its own (withOwnMods).
func modsDifferReason(host []string) string {
	return "the mods differ: the host runs " + modsText(host)
}

// withOwnMods completes a mods refusal's reason with this build's list,
// so the player sees the host's list against theirs.
func withOwnMods(reason string, own Mods) string {
	if !own.Known {
		return reason
	}
	return reason + "; you run " + modsText(own.List) + ". Both players need the same mods turned on."
}

// modsMatch reports whether a joiner with hash (ok false: none sent, an
// older build) may join a host with mods host.
func modsMatch(host Mods, hash [ModsHashLen]byte, ok bool) bool {
	return !host.Known || !ok || hash == host.Hash
}

// modsMatchHex is modsMatch for a lobby HELLO's ModsHash.
func modsMatchHex(host Mods, hexHash string) bool {
	if !host.Known || hexHash == "" {
		return true
	}
	return strings.EqualFold(hexHash, host.ModsHashHex())
}

// SetMods sets the host's mods: a player whose hello carries another
// hash (CapModsHash), or a lobby member whose HELLO does, is refused with
// the host's list. A player or member that sends no hash, an older
// build, is admitted as before. The zero Mods (the default) checks
// nothing. Set it before Start.
func (s *Server) SetMods(m Mods) {
	m.List = append([]string(nil), m.List...)
	s.mu.Lock()
	s.mods = m
	s.mu.Unlock()
	h := s.lobby
	h.mu.Lock()
	h.m.mods = m
	h.mu.Unlock()
}

// Mods returns the host's mods (SetMods).
func (s *Server) Mods() Mods {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mods
}

// modsAdmit reports whether a player hello's hash (ok false: none) may
// join.
func (s *Server) modsAdmit(hash [ModsHashLen]byte, ok bool) bool {
	return modsMatch(s.Mods(), hash, ok)
}

// SetMods sets this build's mods: a player connection sends their hash
// in its hello (CapModsHash), and a host's RejectMods reason gets this
// list added. The zero Mods sends none, as before mods. Call it before
// Connect.
func (c *Client) SetMods(m Mods) {
	m.List = append([]string(nil), m.List...)
	c.mods = m
}

// SetMods sets this build's mods: HELLO carries their hash (ModsHash),
// and a NoticeModsDiffer gets this list added to its text. Call it
// before Connect.
func (l *LobbyClient) SetMods(m Mods) {
	l.c.SetMods(m)
	l.mu.Lock()
	l.hello.ModsHash = m.ModsHashHex()
	l.mu.Unlock()
}
