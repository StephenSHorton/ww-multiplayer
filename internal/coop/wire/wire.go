// Package wire encodes and decodes the co-op sub-messages that travel in a
// MsgCoop ('O') frame of network protocol 2 (§4.4). An 'O' payload is
// [sub:u8][body], every integer big-endian:
//
//	0x01 CO_HELLO    c->s   who I am: schema, table hashes, my file's tag state
//	0x02 CO_WELCOME  s->c   the hub's verdict and the world it serves
//	0x03 WORLD       both   a whole 0x778-byte image (join, reconnect, resync)
//	0x04 GAINS       both   bytes that gained bits (c->s and s->c bodies differ)
//	0x05 PRESENCE    both   where a player is (sent c->s, relayed s->c)
//	0x06 SCOPED      both   dan, zone, room-edge or kill words for one stage or room
//	0x07 NOTICE      s->c   a coded, human-readable reason
//	0x08 CTRL        both   resync, co-op off, tombstone
//
// No raw address travels: offsets index the canonical dSv_save_c image
// (internal/coop/addr Off* offsets) and words index one game array, and
// each receiver derives addresses itself. Decoding is strict, so a peer's
// bug or a hostile frame is refused before any of it reaches a merge or a
// write:
//
//   - every body has its exact length, and counts match it;
//   - GAINS offsets are below 0x778, strictly ascending, of a class that
//     merges (OR, OR_MASKED, MAX in internal/coop/class), with no bit
//     outside the class mask; a WORLD image carries nothing that is not
//     shared (StripLocal);
//   - SCOPED words are in range for their kind and strictly ascending,
//     tables 0..15 and rooms 0..63 as the game's setters assert
//     (d_save.cpp:1540-1701, §3.1);
//   - enums hold known values, stage names are canonical, NOTICE text is
//     valid UTF-8 without control characters.
//
// Encode refuses whatever Decode would, so a sender's bug fails at the
// sender. Every error wraps ErrMalformed, except a CO_HELLO of another
// schema, which wraps ErrSchema so the hub can answer with NOTICE SCHEMA.
//
// Decomp citations are zeldaret/tww at 4d78c994, as in internal/coop/addr.
package wire

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
)

// Schema is the layout version of the sub-messages below. CO_HELLO carries
// it; bump it on any change to a body.
const Schema uint16 = 1

// MaxPayload is the most an 'O' payload can hold: a frame's length field is
// a u16 (network.MaxPayload). Every message this package encodes is far
// below it.
const MaxPayload = 0xFFFF

// Sub is an 'O' sub-message type: the payload's first byte.
type Sub uint8

const (
	SubCoHello   Sub = 0x01
	SubCoWelcome Sub = 0x02
	SubWorld     Sub = 0x03
	SubGains     Sub = 0x04
	SubPresence  Sub = 0x05
	SubScoped    Sub = 0x06
	SubNotice    Sub = 0x07
	SubCtrl      Sub = 0x08
)

func (s Sub) String() string {
	switch s {
	case SubCoHello:
		return "CO_HELLO"
	case SubCoWelcome:
		return "CO_WELCOME"
	case SubWorld:
		return "WORLD"
	case SubGains:
		return "GAINS"
	case SubPresence:
		return "PRESENCE"
	case SubScoped:
		return "SCOPED"
	case SubNotice:
		return "NOTICE"
	case SubCtrl:
		return "CTRL"
	}
	return fmt.Sprintf("Sub(0x%02X)", uint8(s))
}

// Dir is the way a payload travels. It picks the GAINS body, and some subs
// travel one way only.
type Dir uint8

const (
	Up   Dir = 1 // client (a replica) to server (the hub)
	Down Dir = 2 // server to client
)

func (d Dir) String() string {
	switch d {
	case Up:
		return "c->s"
	case Down:
		return "s->c"
	}
	return fmt.Sprintf("Dir(%d)", uint8(d))
}

// travels reports whether sub s may travel in direction d.
func (s Sub) travels(d Dir) bool {
	switch s {
	case SubCoHello:
		return d == Up
	case SubCoWelcome, SubNotice:
		return d == Down
	case SubWorld, SubGains, SubPresence, SubScoped, SubCtrl:
		return d == Up || d == Down
	}
	return false
}

var (
	// ErrMalformed is wrapped by every decode and encode error but a
	// schema mismatch.
	ErrMalformed = errors.New("wire: malformed co-op message")
	// ErrSchema is wrapped by the error for a CO_HELLO whose schema is
	// not Schema. Nothing after the schema field is read.
	ErrSchema = errors.New("wire: co-op schema mismatch")
)

// bad builds an ErrMalformed error.
func bad(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrMalformed}, args...)...)
}

// Msg is one decoded sub-message: *CoHello, *CoWelcome, *World, *GainsUp,
// *GainsDown, *Presence, *Scoped, *Notice or *Ctrl.
type Msg interface {
	Sub() Sub
	// validate reports why the message can't be sent, or nil.
	validate() error
	// appendBody appends the body of a valid message.
	appendBody(b []byte) []byte
}

// Encode validates m and returns its 'O' payload, [sub][body].
func Encode(m Msg) ([]byte, error) {
	if err := m.validate(); err != nil {
		return nil, err
	}
	return m.appendBody([]byte{byte(m.Sub())}), nil
}

// Decode parses one 'O' payload that travelled in direction dir. The
// returned message holds no reference to payload.
func Decode(payload []byte, dir Dir) (Msg, error) {
	if len(payload) < 1 {
		return nil, bad("empty payload")
	}
	sub, body := Sub(payload[0]), payload[1:]
	if dir != Up && dir != Down {
		return nil, bad("%s: unknown direction %d", sub, dir)
	}
	if !sub.travels(dir) {
		if sub.travels(Up) || sub.travels(Down) {
			return nil, bad("%s does not travel %s", sub, dir)
		}
		return nil, bad("unknown sub-message 0x%02X", uint8(sub))
	}
	switch sub {
	case SubCoHello:
		return asMsg(decodeCoHello(body))
	case SubCoWelcome:
		return asMsg(decodeCoWelcome(body))
	case SubWorld:
		return asMsg(decodeWorld(body))
	case SubGains:
		if dir == Up {
			return asMsg(decodeGainsUp(body))
		}
		return asMsg(decodeGainsDown(body))
	case SubPresence:
		return asMsg(decodePresence(body))
	case SubScoped:
		return asMsg(decodeScoped(body))
	case SubNotice:
		return asMsg(decodeNotice(body))
	default: // SubCtrl
		return asMsg(decodeCtrl(body))
	}
}

// asMsg returns a typed decoder's result as a Msg, nil (not a nil pointer
// in a non-nil interface) on error.
func asMsg[M Msg](m M, err error) (Msg, error) {
	if err != nil {
		return nil, err
	}
	return m, nil
}

// validated returns m if it is valid, else nil and why.
func validated[M Msg](m M) (M, error) {
	if err := m.validate(); err != nil {
		var none M
		return none, err
	}
	return m, nil
}

// ID is an 8-byte identifier: a worldID or a bindNonce, minted by Go at a
// bind (§5.3). The zero ID means none (an unbound file has no world).
type ID [8]byte

func (id ID) String() string { return hex.EncodeToString(id[:]) }

// IsZero reports whether id is the zero ID.
func (id ID) IsZero() bool { return id == ID{} }

func getID(b []byte) (id ID) {
	copy(id[:], b)
	return id
}

// Stage is a stage name as the game holds it: char[8] filled by strcpy
// (dStage_startStage_c::set, d_stage.cpp:112; mName, d_stage.h:950), so at
// most 7 characters and a NUL. On the wire every byte after the name is NUL
// (RAM can keep a longer old name's tail past the NUL), and the name is
// printable ASCII without spaces, like every stage name the game uses
// ("sea", "M_NewD2", "sea_T"). The zero Stage is the empty name.
type Stage [addr.StageNameLen]byte

// MaxStageLen is the longest stage name: char[8] with its NUL.
const MaxStageLen = addr.StageNameLen - 1

// MakeStage returns name as a Stage, or an error if it can't be one.
func MakeStage(name string) (Stage, error) {
	var s Stage
	if len(name) > MaxStageLen {
		return s, bad("stage name %q is %d bytes (max %d)", name, len(name), MaxStageLen)
	}
	copy(s[:], name)
	return s, s.check("stage")
}

func (s Stage) String() string {
	for i, c := range s {
		if c == 0 {
			return string(s[:i])
		}
	}
	return string(s[:])
}

// IsEmpty reports whether s is the empty name.
func (s Stage) IsEmpty() bool { return s[0] == 0 }

// check reports why s is not a canonical stage name; what names the
// message for the error.
func (s Stage) check(what string) error {
	n := 0
	for n < len(s) && s[n] != 0 {
		if c := s[n]; c < '!' || c > '~' {
			return bad("%s name %q has byte 0x%02X", what, s.String(), c)
		}
		n++
	}
	if n == len(s) {
		return bad("%s name %q fills all %d bytes with no NUL", what, s.String(), len(s))
	}
	for _, c := range s[n:] {
		if c != 0 {
			return bad("%s name %q has bytes after its NUL", what, s.String())
		}
	}
	return nil
}

// validTable reports whether t is a save table (addr.ValidTable).
func validTable(t int8) bool { return addr.ValidTable(int(t)) }

// validRoom reports whether r is a room the setters accept: 0..63
// (d_save.cpp:1553).
func validRoom(r int8) bool { return r >= 0 && int(r) < addr.RoomCount }

// classHash is ClassHash, computed once.
var classHash = func() uint64 {
	h := fnv.New64a()
	for _, s := range class.Spans() {
		var b [6]byte
		binary.BigEndian.PutUint16(b[0:2], uint16(s.Off))
		binary.BigEndian.PutUint16(b[2:4], uint16(s.Len))
		b[4], b[5] = byte(s.Class), s.Mask
		h.Write(b[:])
	}
	return h.Sum64()
}()

// ClassHash is the FNV-1a 64 of the class table this build validates GAINS
// and WORLD against: per span, in image order, off and len (u16 BE), class
// and mask (field names are left out). CO_HELLO carries it, so a hub can
// tell a peer whose table differs and give it features 0 (§4.4).
func ClassHash() uint64 { return classHash }
