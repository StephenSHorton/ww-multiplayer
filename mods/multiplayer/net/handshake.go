package network

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/playercolor"
)

// Protocol version handshake.
//
// v0.2.x had no version field anywhere, so a mixed pair failed in confusing
// ways: a v0.2.x client never answers heartbeat pings, so a newer server
// reaped it after the read timeout, and a newer client flapped against a
// v0.2.x server. The handshake adds a version without changing any frame an
// older peer already parses:
//
//	client -> server  MsgJoin    [name]            unchanged: old servers read the whole payload as the name
//	client -> server  MsgHello   [version:2 BE]    old servers ignore unknown types
//	              or  MsgHello   [version:2 BE][role:1][caps:2 BE]   (protocol 2+, a co-op connection)
//	server -> client  MsgWelcome [id][epoch:8][version:2 BE]
//	              or  MsgReject  [code:1][reason], then close
//
// The hello goes AFTER the join because a v0.2.x server closes any
// connection whose first frame isn't a join. Sent first, it would hide the
// old server's welcome, and that welcome (too short to carry a version) is
// how a current client recognizes an old server.
//
// A current server waits up to helloTimeout after the join for the hello. A
// client that sends none predates the handshake. It gets a welcome it
// accepts (ID 0, which is never assigned), then a plain-text MsgChat that it
// prints as a log line telling the player to upgrade, and is disconnected.
//
// Connection roles (protocol 2). Every connection so far was a player's: it
// streams position and pose and gets the relays and the roster. The co-op
// pipeline (docs/08-coop-design.md §4.4) opens a connection of its own with
// role RoleCoop in its hello. That connection never becomes a player: it is
// not in the roster, the player count or the 255-player limit, gets no
// relay, and carries MsgCoop frames to and from the server's CoopHandler
// (coop.go). A player's hello without a color or a mods hash stays the
// 2-byte form, so such a player connection sends the same frames as under
// protocol 1 apart from the version number; a hello with no role byte is a
// player's. Bytes after caps are ignored, so a later client can append
// fields.
//
// The desktop app's lobby (docs/10-lobby.md) is a third role, RoleLobby,
// with LobbyCapJSON in caps: a member of the pre-game lobby, never a
// player either. A server from before the lobby refuses role 2 with
// RejectRole, which the app takes as "join directly", so the lobby did not
// bump ProtocolVersion (it was 2 then).
//
// Tunic colors (docs/09-player-colors.md). A player with a color preference
// (--color / WW_COLOR) sends the role form with role RolePlayer and caps 0,
// then [hasColor:1 = 1][r:1][g:1][b:1]. A server from before colors reads
// it as a plain player hello (it ignores bytes after caps), so the protocol
// version does not change. A player with no preference and no mods hash
// sends the 2-byte hello as before.
//
// Mods (docs/api/02-mods.md, "mods_hash"). A player whose build knows its
// mods sets CapModsHash in a player hello's caps and appends the 32-byte
// SHA-256 of its client_required mods after the color block:
// [version:2][role 0][caps:2][hasColor:1][r][g][b][hash:32], with hasColor
// 0 (and rgb 0) when it has no color preference. A server from before
// mods ignores the caps and the bytes after the color block; a current
// server whose own hash differs refuses with RejectMods and a reason that
// lists the host's mods (the client adds its own). A hello without the
// bit, from an older client, is accepted as before. Every CLI and app
// build since D5 has a hash, so its player hello is always this form; the
// 2-byte and caps-0 color forms are what older clients (and writeJoin
// without a hash) send. The mods hash did not bump ProtocolVersion
// (docs/api/00-design.md decision 13).
//
// Enemy sync (enemy.go) is roles 3 and 4 and MsgEnemy. It did bump
// ProtocolVersion, to 3: an older server refuses role 4 with RejectRole,
// which is fatal and would end a joiner's whole session, and an older
// joiner would leave a newer host's enemies silently unshared. Under 3 a
// mixed pair is refused at the handshake with the version reject instead.
//
// Enemy mirror batch C (enemy-mirror-batch-c.json D13) bumped it to 4: its
// kind table (the Kargaroc; its Magtail row was cut after P0-MT) changes
// the enemy KindHash, so a v0.5.0 peer would pass a protocol-3 hello and
// then lose every default-on enemy to the client's KindHash disarm, with
// only a log line. Under 4 that
// mixed pair is refused at the hello and shown incompatible in the LAN
// list (internal/gui/lan.go) instead.
//
// Batch D (enemy-mirror-batch-d.json G-D6, "magtail") bumped it to 5 for
// the same reason: the restored Magtail row changes the KindHash again
// (0x587E10C2 to 0xB826567A), and a batch C build (protocol 4, main
// 56bfb22, unreleased) would pass a protocol-4 hello and then lose every
// enemy to the KindHash disarm.

// ProtocolVersion is the wire protocol this build speaks. Bump it on any
// change an older build can't handle: client and server must match.
//
//	1  v0.3.0: the version handshake
//	2  co-op: the hello's role and caps, MsgCoop
//	3  enemy sync: RoleEnemyHost/RoleEnemyClient, MsgEnemy (WireVersion 4);
//	   enemy layer and remote bodies on by default
//	4  enemy mirror batch C: the BB kind row (a new KindHash; MT cut),
//	   the u16 host kind mask (WireVersion stays 4)
//	5  enemy mirror batch D: the MT kind row back (a new KindHash; off by
//	   default, mt+u), Gohdan's boss_kill and the actor_refresh request
//	   (WireVersion stays 4)
const ProtocolVersion uint16 = 5

// Connection roles, the hello's role byte.
const (
	RolePlayer byte = 0 // a player: pose, position, chat and the roster (also a hello with no role byte)
	RoleCoop   byte = 1 // the co-op pipeline: MsgCoop only, routed to the server's CoopHandler
	RoleLobby  byte = 2 // the app's lobby connection: MsgLobby, chat and the heartbeat (lobby.go)
	// Enemy sync (enemy.go): the host's sampler dials with RoleEnemyHost
	// (one per server) and every joiner's mirror with RoleEnemyClient.
	// Neither is a player; both carry MsgEnemy and the heartbeat alone.
	RoleEnemyHost   byte = 3
	RoleEnemyClient byte = 4
)

// helloTimeout is how long a server waits after a join for the client's
// MsgHello before treating it as a pre-handshake (v0.2.x) client. A current
// client writes both frames in one write, so this only has to cover network
// jitter. A package var so tests can shorten it; NewServer captures it.
var helloTimeout = 3 * time.Second

// lingerTimeout bounds how long lingerClose drains a refused connection.
const lingerTimeout = 2 * time.Second

// MsgReject codes.
const (
	RejectVersion    byte = 1 // client and server protocol versions differ
	RejectBadName    byte = 2 // empty, too long, not UTF-8, or has control characters
	RejectServerFull byte = 3 // no free player ID (or, for a co-op connection, co-op ID)
	RejectRole       byte = 4 // the server doesn't take the hello's role: co-op is off, or the role is unknown
	RejectMods       byte = 5 // the player's client_required mods differ from the host's (CapModsHash)
)

// ErrFatal is matched (errors.Is) by connect errors that retrying can't fix:
// the host refused us with MsgReject, or the two sides run incompatible
// versions. ReconnectLoop stops and returns such an error instead of backing
// off and trying again.
var ErrFatal = errors.New("fatal connect error")

// RejectError is a connect failure that retrying can't fix, so
// errors.Is(err, ErrFatal) holds for it. Code is a Reject* value
// (RejectVersion for a host too old to send MsgReject).
type RejectError struct {
	Code   byte
	Reason string
}

func (e *RejectError) Error() string { return e.Reason }

// Is reports whether target is ErrFatal, so errors.Is(err, ErrFatal) matches
// any *RejectError in err's chain.
func (e *RejectError) Is(target error) bool { return target == ErrFatal }

// legacyUpgradeNotice is the line a pre-handshake client is sent before it is
// disconnected. v0.2.x passes a MsgChat payload verbatim to its log callback
// ("[net] server: ..."), so this is plain text, not a chat relay.
const legacyUpgradeNotice = "server: this host runs a newer ww-multiplayer; please upgrade to the host's version (both players need the same version)"

// HelloMessage builds a MsgHello payload: [version:2 BE].
func HelloMessage(version uint16) []byte {
	return binary.BigEndian.AppendUint16(nil, version)
}

// ParseHello extracts the protocol version from a MsgHello payload. Bytes
// after the version are ignored, so a later client can append fields.
func ParseHello(data []byte) (version uint16, err error) {
	if len(data) < 2 {
		return 0, fmt.Errorf("hello: %d-byte payload is shorter than its 2-byte version", len(data))
	}
	return binary.BigEndian.Uint16(data[0:2]), nil
}

// helloRoleLen is the length of a hello that carries a role:
// [version:2][role:1][caps:2].
const helloRoleLen = 5

// HelloRoleMessage builds a protocol-2 MsgHello payload that carries a
// connection role: [version:2 BE][role:1][caps:2 BE]. caps is passed to the
// server's CoopHandler as is; the network layer gives it no meaning.
func HelloRoleMessage(version uint16, role byte, caps uint16) []byte {
	b := binary.BigEndian.AppendUint16(nil, version)
	b = append(b, role)
	return binary.BigEndian.AppendUint16(b, caps)
}

// ParseHelloRole decodes a MsgHello payload with its optional role and
// caps. A 2-byte hello (protocol 1, or a protocol-2 player) is RolePlayer
// with caps 0. A hello cut off inside role and caps (3 or 4 bytes) is an
// error, and so is a player hello with CapModsHash that cuts off its hash.
// Other bytes after caps are ignored, as ParseHello ignores bytes after
// the version.
func ParseHelloRole(data []byte) (version uint16, role byte, caps uint16, err error) {
	version, err = ParseHello(data)
	switch {
	case err != nil:
		return 0, 0, 0, err
	case len(data) == 2:
		return version, RolePlayer, 0, nil
	case len(data) < helloRoleLen:
		return 0, 0, 0, fmt.Errorf("hello: %d-byte payload cuts off its role and caps (want 2 or at least %d bytes)", len(data), helloRoleLen)
	}
	role, caps = data[2], binary.BigEndian.Uint16(data[3:5])
	if role == RolePlayer && caps&CapModsHash != 0 && len(data) < helloModsLen {
		return 0, 0, 0, fmt.Errorf("hello: %d-byte player hello cuts off its mods hash (want at least %d bytes)", len(data), helloModsLen)
	}
	return version, role, caps, nil
}

// helloColorLen is the length of a player hello that carries a color
// preference: the role form plus [hasColor:1][r:1][g:1][b:1].
const helloColorLen = helloRoleLen + 4

// HelloPlayerMessage builds a player's MsgHello: the 2-byte form with no
// color preference, else the role form (RolePlayer, caps 0) plus the
// preference.
func HelloPlayerMessage(version uint16, pref *playercolor.Color) []byte {
	return HelloPlayerModsMessage(version, pref, nil)
}

// helloModsLen is the length of a player hello with CapModsHash: the
// color block (hasColor 0 without a preference) plus the hash.
const helloModsLen = helloColorLen + ModsHashLen

// HelloPlayerModsMessage is HelloPlayerMessage with this build's mods
// hash. With a hash it is always the role form: caps CapModsHash, the
// color block (hasColor 0 and rgb 0 without a preference), then the hash.
func HelloPlayerModsMessage(version uint16, pref *playercolor.Color, hash *[ModsHashLen]byte) []byte {
	if pref == nil && hash == nil {
		return HelloMessage(version)
	}
	var caps uint16
	if hash != nil {
		caps = CapModsHash
	}
	b := HelloRoleMessage(version, RolePlayer, caps)
	if pref != nil {
		b = append(b, 1, pref.R, pref.G, pref.B)
	} else {
		b = append(b, 0, 0, 0, 0)
	}
	if hash != nil {
		b = append(b, hash[:]...)
	}
	return b
}

// ParseHelloModsHash returns the mods hash a player's MsgHello carries;
// ok is false for a hello without one (no CapModsHash, another role, or
// too short).
func ParseHelloModsHash(data []byte) (hash [ModsHashLen]byte, ok bool) {
	if len(data) < helloModsLen || data[2] != RolePlayer || binary.BigEndian.Uint16(data[3:5])&CapModsHash == 0 {
		return hash, false
	}
	copy(hash[:], data[helloColorLen:helloModsLen])
	return hash, true
}

// ParseHelloColor returns the color preference a player's MsgHello carries,
// or nil for a hello without one.
func ParseHelloColor(data []byte) *playercolor.Color {
	if len(data) < helloColorLen || data[helloRoleLen] != 1 {
		return nil
	}
	c := playercolor.Color{R: data[helloRoleLen+1], G: data[helloRoleLen+2], B: data[helloRoleLen+3]}
	return &c
}

// RejectMessage builds a MsgReject payload: [code:1][reason]. The reason is
// cut to fit a frame.
func RejectMessage(code byte, reason string) []byte {
	if len(reason) > MaxPayload-1 {
		reason = reason[:MaxPayload-1]
	}
	return append([]byte{code}, reason...)
}

// ParseReject extracts the code and reason from a MsgReject payload.
func ParseReject(data []byte) (code byte, reason string, err error) {
	if len(data) < 1 {
		return 0, "", errors.New("reject: empty payload")
	}
	return data[0], string(data[1:]), nil
}

// ValidatePlayerName reports why the server won't admit a player name, or
// nil. Names are compared to filter a player's own echo and are printed in
// logs, the TUI and other players' chat, so the server rejects (rather than
// cleans up) a name that is empty, over MaxNameLen bytes, not valid UTF-8,
// or that contains control characters such as newlines or terminal escapes.
func ValidatePlayerName(name string) error {
	switch {
	case name == "":
		return errors.New("player name is empty")
	case len(name) > MaxNameLen:
		return fmt.Errorf("player name is %d bytes long (max %d)", len(name), MaxNameLen)
	case !utf8.ValidString(name):
		return errors.New("player name is not valid UTF-8")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return fmt.Errorf("player name contains control character %U", r)
		}
	}
	return nil
}

// writeJoin sends a player's half of the handshake, MsgJoin then MsgHello,
// in a single write so the server gets the hello right behind the join. A
// name the server would refuse fails here as a *RejectError instead, since
// reconnecting can't fix it.
func writeJoin(w io.Writer, name string) error {
	return writeJoinRole(w, name, RolePlayer, 0, nil, nil)
}

// writeJoinRole is writeJoin for a connection of any role. A player's hello
// with a hash is the role form with CapModsHash, the color block (hasColor
// 0 without pref) and the hash; without one it is the color form when pref
// is set, else the 2-byte form. Any other role's carries role and caps.
func writeJoinRole(w io.Writer, name string, role byte, caps uint16, pref *playercolor.Color, hash *[ModsHashLen]byte) error {
	if err := ValidatePlayerName(name); err != nil {
		return &RejectError{Code: RejectBadName, Reason: err.Error()}
	}
	hello := HelloPlayerModsMessage(ProtocolVersion, pref, hash)
	if role != RolePlayer {
		hello = HelloRoleMessage(ProtocolVersion, role, caps)
	}
	var buf bytes.Buffer
	if err := WriteMessage(&buf, MsgJoin, []byte(name)); err != nil {
		return err
	}
	if err := WriteMessage(&buf, MsgHello, hello); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

// legacyHostReason is the version error for a host that predates the
// handshake (v0.2.x or earlier).
const legacyHostReason = "host runs an older ww-multiplayer (v0.2.x or earlier); both players need the same version"

// checkWelcome vets the server's first frame after our join. A *RejectError
// (errors.Is ErrFatal) means retrying can't help: the server refused us with
// MsgReject, or it speaks a different protocol version (including none at
// all, a v0.2.x server).
func checkWelcome(msg *Message) error {
	switch msg.Type {
	case MsgWelcome:
	case MsgReject:
		code, reason, err := ParseReject(msg.Data)
		if err != nil || reason == "" {
			reason = "no reason given"
		}
		return &RejectError{Code: code, Reason: "host refused the connection: " + reason}
	case MsgPosition, MsgPose, MsgPlayerList, MsgLeave, MsgChat:
		// A current server writes the welcome before any relay can reach
		// a new player (server.go holds SendMu across it). A v0.2.x server
		// adds the player to its broadcast list first, so another player's
		// position or pose can arrive ahead of the welcome; that is the
		// same old host as a short welcome, so say so instead of retrying.
		return &RejectError{Code: RejectVersion, Reason: legacyHostReason}
	default:
		return fmt.Errorf("expected welcome, got message type %q (%d bytes)", msg.Type, len(msg.Data))
	}
	if len(msg.Data) < 1 {
		return errors.New("expected welcome, got an empty one")
	}
	version, ok := WelcomeProtocolVersion(msg.Data)
	if !ok {
		return &RejectError{Code: RejectVersion, Reason: legacyHostReason}
	}
	if version != ProtocolVersion {
		return &RejectError{Code: RejectVersion,
			Reason: fmt.Sprintf("host speaks network protocol %d and this build speaks %d; both players need the same ww-multiplayer version", version, ProtocolVersion)}
	}
	if msg.Data[0] == 0 {
		// ID 0 is never assigned. It is the welcome a current server sends
		// a client it took for a pre-handshake one, meaning our hello
		// arrived after helloTimeout. That's transient, so retry.
		return errors.New("host did not receive our protocol hello in time")
	}
	return nil
}

// versionMismatchReason is the MsgReject reason for a client whose hello
// carries protocol version client.
func versionMismatchReason(client uint16) string {
	if client < ProtocolVersion {
		return fmt.Sprintf("this host runs a newer ww-multiplayer (network protocol %d, yours is %d); please upgrade to the host's version",
			ProtocolVersion, client)
	}
	return fmt.Sprintf("this host runs an older ww-multiplayer (network protocol %d, yours is %d); both players need the same version",
		ProtocolVersion, client)
}

// refuse answers a current client's join with MsgReject and closes the
// connection. conn is not in s.players, so nothing else writes to it.
func (s *Server) refuse(conn net.Conn, name string, code byte, reason string) {
	s.log(fmt.Sprintf("refused %.40q: %s", name, reason))
	s.writeConn(conn, MsgReject, RejectMessage(code, reason))
	lingerClose(conn)
}

// refuseLegacy turns away a client that predates the version handshake, in
// the only form it can show the player: a welcome it accepts (ID 0, never
// assigned to a real player), then legacyUpgradeNotice as a chat line, then
// a close.
func (s *Server) refuseLegacy(conn net.Conn, name string) {
	s.log(fmt.Sprintf("refused %.40q: its ww-multiplayer predates the version handshake (v0.2.x or earlier); told it to upgrade", name))
	s.writeConn(conn, MsgWelcome, WelcomeMessage(0, s.epoch))
	s.writeConn(conn, MsgChat, []byte(legacyUpgradeNotice))
	lingerClose(conn)
}

// writeConn writes one frame to a connection that isn't (yet) a registered
// player, bounded by the server's write deadline.
func (s *Server) writeConn(conn net.Conn, msgType byte, data []byte) error {
	if s.writeTimeout > 0 {
		conn.SetWriteDeadline(time.Now().Add(s.writeTimeout))
	}
	return WriteMessage(conn, msgType, data)
}

// lingerClose closes a refused connection so the frames just written reach
// the peer. Closing a socket that still has unread input makes the OS send a
// reset, which can throw away data the peer hasn't read yet (a v0.2.x client
// starts streaming poses as soon as it has its welcome). So send FIN first,
// drain the peer's input until it closes or lingerTimeout passes, then close.
func lingerClose(conn net.Conn) {
	if cw, ok := conn.(interface{ CloseWrite() error }); ok {
		cw.CloseWrite()
	}
	conn.SetReadDeadline(time.Now().Add(lingerTimeout))
	io.Copy(io.Discard, conn)
	conn.Close()
}

// isTimeout reports whether err is a deadline expiry.
func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
