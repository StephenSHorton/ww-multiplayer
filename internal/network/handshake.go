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

// ProtocolVersion is the wire protocol this build speaks. Bump it on any
// change an older build can't handle: client and server must match.
const ProtocolVersion uint16 = 1

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
	RejectServerFull byte = 3 // no free player ID
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

// writeJoin sends the client's half of the handshake, MsgJoin then MsgHello,
// in a single write so the server gets the hello right behind the join. A
// name the server would refuse fails here as a *RejectError instead, since
// reconnecting can't fix it.
func writeJoin(w io.Writer, name string) error {
	if err := ValidatePlayerName(name); err != nil {
		return &RejectError{Code: RejectBadName, Reason: err.Error()}
	}
	var buf bytes.Buffer
	if err := WriteMessage(&buf, MsgJoin, []byte(name)); err != nil {
		return err
	}
	if err := WriteMessage(&buf, MsgHello, HelloMessage(ProtocolVersion)); err != nil {
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
