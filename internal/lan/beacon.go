// Package lan finds sessions on the local network (App PR 11, design
// decision 12: no service). A host's app sends a small UDP beacon to the
// broadcast address of each network it is on, every BeaconInterval, while
// it hosts a lobby; an app on Home listens on the same port and lists
// what it hears for EntryTTL ("Sessions on your network").
//
// The beacon names the host (a player name), the session's phase, player
// count, the app and protocol versions and the lobby's TCP port. It holds
// no address (the joiner takes the packet's source), no token and nothing
// else secret. The format is docs/10-lobby.md §10.
package lan

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// DefaultPort is the beacons' UDP port: the session's default TCP port
	// (WW_LAN_PORT changes it).
	DefaultPort = 25565
	// BeaconInterval is how often a host sends its beacon.
	BeaconInterval = 2 * time.Second
	// EntryTTL is how long a joiner lists a session after its last
	// beacon: three missed beacons.
	EntryTTL = 6 * time.Second
	// MaxBeacon caps a beacon's size; a bigger datagram is not one.
	MaxBeacon = 512
	// FormatVersion is the beacon format this build writes.
	FormatVersion = 1

	maxVersionLen = 32
	maxNameLen    = 64
	headerLen     = 4 + 1 + 1 + 2 + 2 + 1 + 1 + 1 + 8
)

// Magic starts every beacon.
var Magic = [4]byte{'W', 'W', 'L', 'B'}

// Phase is a session's phase as the beacon carries it.
type Phase byte

// Beacon phases (session.LobbyPhase).
const (
	PhaseLobby    Phase = 0
	PhaseStarting Phase = 1
	PhaseInGame   Phase = 2
	// PhaseUnknown is a newer format's phase this build doesn't know.
	PhaseUnknown Phase = 255
)

func (p Phase) String() string {
	switch p {
	case PhaseLobby:
		return "lobby"
	case PhaseStarting:
		return "starting"
	case PhaseInGame:
		return "in game"
	}
	return "unknown"
}

// Flags.
const (
	FlagCoop byte = 1 << 0 // the host chose a co-op world
)

// Beacon is one host's announcement.
type Beacon struct {
	// Format is the beacon format (FormatVersion when sent by this build).
	Format byte
	Flags  byte
	// Protocol is the host's network.ProtocolVersion: a joiner whose own
	// differs can't join (the host refuses it with RejectVersion).
	Protocol uint16
	// Port is the lobby's TCP port.
	Port    uint16
	Players byte
	Max     byte
	Phase   Phase
	// Instance identifies the sending app run, so a joiner can skip its
	// own beacons and merge one host heard on two networks. Random, made
	// once per run.
	Instance [8]byte
	// Version is the host's app version ("v0.4.0").
	Version string
	// Host is the host's player name.
	Host string
}

// Coop says the host chose a co-op world.
func (b Beacon) Coop() bool { return b.Flags&FlagCoop != 0 }

// Errors Decode returns. Every malformed beacon is one of these.
var (
	ErrNotBeacon = errors.New("not a ww-multiplayer beacon")
	ErrMalformed = errors.New("malformed beacon")
)

// Encode writes b in the current format. Strings are trimmed to their
// limits (at a rune boundary) and cleaned of control characters.
func Encode(b Beacon) ([]byte, error) {
	if b.Port == 0 {
		return nil, fmt.Errorf("%w: port 0", ErrMalformed)
	}
	if b.Phase > PhaseInGame {
		return nil, fmt.Errorf("%w: phase %d", ErrMalformed, b.Phase)
	}
	ver := clip(clean(b.Version), maxVersionLen)
	host := clip(clean(b.Host), maxNameLen)
	out := make([]byte, 0, headerLen+2+len(ver)+len(host))
	out = append(out, Magic[:]...)
	out = append(out, FormatVersion, b.Flags)
	out = binary.BigEndian.AppendUint16(out, b.Protocol)
	out = binary.BigEndian.AppendUint16(out, b.Port)
	out = append(out, b.Players, b.Max, byte(b.Phase))
	out = append(out, b.Instance[:]...)
	out = append(out, byte(len(ver)))
	out = append(out, ver...)
	out = append(out, byte(len(host)))
	out = append(out, host...)
	return out, nil
}

// Decode reads a beacon. It never panics and refuses anything off the
// format: a wrong magic, a short or oversized datagram, a format older
// than 1, port 0, a string over its limit or not valid printable UTF-8,
// and (format 1) an unknown phase or trailing bytes. A newer format is
// read as far as format 1 goes; what it appends is ignored, and a phase
// this build doesn't know reads as PhaseUnknown.
func Decode(p []byte) (Beacon, error) {
	var b Beacon
	if len(p) < 4 || !bytes.Equal(p[:4], Magic[:]) {
		return b, ErrNotBeacon
	}
	if len(p) > MaxBeacon {
		return b, fmt.Errorf("%w: %d bytes", ErrMalformed, len(p))
	}
	if len(p) < headerLen+2 {
		return b, fmt.Errorf("%w: short", ErrMalformed)
	}
	b.Format = p[4]
	if b.Format < 1 {
		return b, fmt.Errorf("%w: format 0", ErrMalformed)
	}
	b.Flags = p[5]
	b.Protocol = binary.BigEndian.Uint16(p[6:8])
	b.Port = binary.BigEndian.Uint16(p[8:10])
	b.Players, b.Max, b.Phase = p[10], p[11], Phase(p[12])
	copy(b.Instance[:], p[13:21])
	if b.Port == 0 {
		return b, fmt.Errorf("%w: port 0", ErrMalformed)
	}
	if b.Phase > PhaseInGame {
		if b.Format == 1 {
			return b, fmt.Errorf("%w: phase %d", ErrMalformed, b.Phase)
		}
		b.Phase = PhaseUnknown
	}
	rest := p[headerLen:]
	var err error
	if b.Version, rest, err = readString(rest, maxVersionLen); err != nil {
		return Beacon{}, err
	}
	if b.Host, rest, err = readString(rest, maxNameLen); err != nil {
		return Beacon{}, err
	}
	if b.Format == 1 && len(rest) != 0 {
		return Beacon{}, fmt.Errorf("%w: %d trailing bytes", ErrMalformed, len(rest))
	}
	return b, nil
}

// readString reads a length-prefixed string of at most limit bytes.
func readString(p []byte, limit int) (string, []byte, error) {
	if len(p) < 1 {
		return "", nil, fmt.Errorf("%w: short", ErrMalformed)
	}
	n := int(p[0])
	if n > limit || len(p) < 1+n {
		return "", nil, fmt.Errorf("%w: bad string length %d", ErrMalformed, n)
	}
	s := string(p[1 : 1+n])
	if !utf8.ValidString(s) || clean(s) != s {
		return "", nil, fmt.Errorf("%w: unprintable string", ErrMalformed)
	}
	return s, p[1+n:], nil
}

// clean drops control and other unprintable characters.
func clean(s string) string {
	s = strings.ToValidUTF8(s, "")
	return strings.Map(func(r rune) rune {
		if !unicode.IsPrint(r) {
			return -1
		}
		return r
	}, s)
}

// clip cuts s to at most n bytes at a rune boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
