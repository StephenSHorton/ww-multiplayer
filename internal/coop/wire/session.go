package wire

import (
	"encoding/binary"
	"fmt"
	"unicode"
	"unicode/utf8"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// FileState is how a replica classified the loaded file's binding tag
// against the world it asks for (§5.2): CO_HELLO's fileState.
type FileState uint8

const (
	FileUnbound FileState = 0 // no tag, or a scrubbed one
	FileBound   FileState = 1 // the tag passes V1-V6
	FileLeaked  FileState = 2 // a tag that fails V2-V5 (carried in by New Game, a second quest, another slot)
	FileForeign FileState = 3 // a valid tag of another world (fails V6)
)

func (f FileState) String() string {
	switch f {
	case FileUnbound:
		return "unbound"
	case FileBound:
		return "bound"
	case FileLeaked:
		return "leaked"
	case FileForeign:
		return "foreign"
	}
	return fmt.Sprintf("FileState(%d)", uint8(f))
}

// CardSlots is how many files a memory card holds: mDataNum is 0..2
// (the file-select cursor, d_file_select.cpp:458-471; card_savedata's
// gamedata[3], m_Do_MemCardRWmng.h:39).
const CardSlots = 3

// CoHello (0x01, c->s) opens a co-op session:
//
//	[schema:u16][classHash:u64][stageTblHash:u64][worldID:8][bindNonce:8]
//	[slot:u8][clearCount:u8][nameHash:u32][fileState:u8][want:u32]   = 45 B
//
// The hub classifies the replica from it. A ClassHash or StageTblHash
// unlike the host's (a modded or randomizer build) gets features 0 and a
// NOTICE.
type CoHello struct {
	Schema       uint16 // Schema
	ClassHash    uint64 // ClassHash()
	StageTblHash uint64 // the M1 tables' identity (internal/coop/tables)
	WorldID      ID     // the tag's worldID; zero for FileUnbound
	BindNonce    ID     // the tag's bindNonce; zero for FileUnbound
	Slot         uint8  // mDataNum, 0..CardSlots-1
	ClearCount   uint8  // mClearCount (tag rule V3)
	NameHash     uint32 // FNV-1a of mPlayerName (tag rule V4)
	FileState    FileState
	Want         uint32 // feature bits the replica asks for
}

const coHelloLen = 45

func (*CoHello) Sub() Sub { return SubCoHello }

func (m *CoHello) validate() error {
	if m.Schema != Schema {
		return fmt.Errorf("%w: CO_HELLO schema %d, this build speaks %d", ErrSchema, m.Schema, Schema)
	}
	if m.Slot >= CardSlots {
		return bad("CO_HELLO slot %d (max %d)", m.Slot, CardSlots-1)
	}
	if m.FileState > FileForeign {
		return bad("CO_HELLO file state %d", m.FileState)
	}
	return nil
}

func (m *CoHello) appendBody(b []byte) []byte {
	b = binary.BigEndian.AppendUint16(b, m.Schema)
	b = binary.BigEndian.AppendUint64(b, m.ClassHash)
	b = binary.BigEndian.AppendUint64(b, m.StageTblHash)
	b = append(b, m.WorldID[:]...)
	b = append(b, m.BindNonce[:]...)
	b = append(b, m.Slot, m.ClearCount)
	b = binary.BigEndian.AppendUint32(b, m.NameHash)
	b = append(b, byte(m.FileState))
	return binary.BigEndian.AppendUint32(b, m.Want)
}

func decodeCoHello(body []byte) (*CoHello, error) {
	// The schema comes first so that a peer of another schema, whose
	// body may be any length, still gets told so.
	if len(body) >= 2 {
		if v := binary.BigEndian.Uint16(body); v != Schema {
			return nil, fmt.Errorf("%w: CO_HELLO schema %d, this build speaks %d", ErrSchema, v, Schema)
		}
	}
	if len(body) != coHelloLen {
		return nil, bad("CO_HELLO body is %d bytes, want %d", len(body), coHelloLen)
	}
	m := &CoHello{
		Schema:       binary.BigEndian.Uint16(body[0:2]),
		ClassHash:    binary.BigEndian.Uint64(body[2:10]),
		StageTblHash: binary.BigEndian.Uint64(body[10:18]),
		WorldID:      getID(body[18:26]),
		BindNonce:    getID(body[26:34]),
		Slot:         body[34],
		ClearCount:   body[35],
		NameHash:     binary.BigEndian.Uint32(body[36:40]),
		FileState:    FileState(body[40]),
		Want:         binary.BigEndian.Uint32(body[41:45]),
	}
	return validated(m)
}

// Status is the hub's verdict in CO_WELCOME. Zero is not a status, so an
// unset field can't read as OK.
type Status uint8

const (
	StatusOK      Status = 1 // the file is bound to this world: the replica may arm
	StatusUnbound Status = 2 // the session runs, but the file is not bound: nothing is applied until a bind
	StatusRefused Status = 3 // no co-op this session (Reason says why); pose sync is unaffected
)

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusUnbound:
		return "unbound"
	case StatusRefused:
		return "refused"
	}
	return fmt.Sprintf("Status(%d)", uint8(s))
}

// CoWelcome (0x02, s->c) answers a CO_HELLO:
//
//	[status:u8][reason:u8][features:u32][worldID:8][seq:u64][freshBind:u8]   = 23 B
type CoWelcome struct {
	Status    Status
	Reason    NoticeCode // 0 for none; required for StatusRefused
	Features  uint32     // the features the session runs with; 0 when refused
	WorldID   ID         // the world the hub serves
	Seq       uint64     // the world's seq
	FreshBind bool       // the replica's bindNonce is a pending fresh bind (tag rule V5)
}

const coWelcomeLen = 23

func (*CoWelcome) Sub() Sub { return SubCoWelcome }

func (m *CoWelcome) validate() error {
	if m.Status < StatusOK || m.Status > StatusRefused {
		return bad("CO_WELCOME status %d", m.Status)
	}
	if m.Reason != 0 && !m.Reason.known() {
		return bad("CO_WELCOME reason %d", m.Reason)
	}
	if m.Status == StatusRefused && (m.Reason == 0 || m.Features != 0) {
		return bad("CO_WELCOME refusal with reason %d and features 0x%X (want a reason and features 0)", m.Reason, m.Features)
	}
	return nil
}

func (m *CoWelcome) appendBody(b []byte) []byte {
	b = append(b, byte(m.Status), byte(m.Reason))
	b = binary.BigEndian.AppendUint32(b, m.Features)
	b = append(b, m.WorldID[:]...)
	b = binary.BigEndian.AppendUint64(b, m.Seq)
	return append(b, boolByte(m.FreshBind))
}

func decodeCoWelcome(body []byte) (*CoWelcome, error) {
	if len(body) != coWelcomeLen {
		return nil, bad("CO_WELCOME body is %d bytes, want %d", len(body), coWelcomeLen)
	}
	fresh, err := byteBool(body[22], "CO_WELCOME freshBind")
	if err != nil {
		return nil, err
	}
	m := &CoWelcome{
		Status:    Status(body[0]),
		Reason:    NoticeCode(body[1]),
		Features:  binary.BigEndian.Uint32(body[2:6]),
		WorldID:   getID(body[6:14]),
		Seq:       binary.BigEndian.Uint64(body[14:22]),
		FreshBind: fresh,
	}
	return validated(m)
}

func boolByte(v bool) byte {
	if v {
		return 1
	}
	return 0
}

func byteBool(b byte, what string) (bool, error) {
	if b > 1 {
		return false, bad("%s is %d, want 0 or 1", what, b)
	}
	return b == 1, nil
}

// NoticeCode says what a NOTICE (and a CO_WELCOME refusal) is about (§4.4).
type NoticeCode uint8

const (
	NoticeWorldMismatch NoticeCode = 1 // the host runs another world: the replica disarms, pose still works
	NoticeNotBound      NoticeCode = 2 // the loaded file is not bound to this world
	NoticeDivergent     NoticeCode = 3 // both sides have unique progress: binding needs --coop-merge
	NoticeClearCount    NoticeCode = 4 // mClearCount differs (second quest): refused
	NoticeSchema        NoticeCode = 5 // schema, class table or stage tables differ
	NoticeRejected      NoticeCode = 6 // a message was refused
	NoticeTombstone     NoticeCode = 7 // a proposed bit is tombstoned
	noticeCodes                    = 7
)

func (c NoticeCode) known() bool { return c >= 1 && c <= noticeCodes }

func (c NoticeCode) String() string {
	switch c {
	case NoticeWorldMismatch:
		return "WORLD_MISMATCH"
	case NoticeNotBound:
		return "NOT_BOUND"
	case NoticeDivergent:
		return "DIVERGENT"
	case NoticeClearCount:
		return "CLEARCOUNT"
	case NoticeSchema:
		return "SCHEMA"
	case NoticeRejected:
		return "REJECTED"
	case NoticeTombstone:
		return "TOMBSTONE"
	}
	return fmt.Sprintf("NoticeCode(%d)", uint8(c))
}

// MaxNoticeLen is the longest NOTICE text: its length is a u8.
const MaxNoticeLen = 0xFF

// Notice (0x07, s->c) tells the replica something it should show:
//
//	[code:u8][len:u8][utf8]
//
// The text goes to logs and the TUI, so it must be valid UTF-8 without
// control characters (the rule player names follow).
type Notice struct {
	Code NoticeCode
	Text string
}

func (*Notice) Sub() Sub { return SubNotice }

func (m *Notice) validate() error {
	if !m.Code.known() {
		return bad("NOTICE code %d", m.Code)
	}
	if len(m.Text) > MaxNoticeLen {
		return bad("NOTICE text is %d bytes (max %d)", len(m.Text), MaxNoticeLen)
	}
	if !utf8.ValidString(m.Text) {
		return bad("NOTICE text is not valid UTF-8")
	}
	for _, r := range m.Text {
		if unicode.IsControl(r) {
			return bad("NOTICE text has control character %U", r)
		}
	}
	return nil
}

func (m *Notice) appendBody(b []byte) []byte {
	b = append(b, byte(m.Code), byte(len(m.Text)))
	return append(b, m.Text...)
}

func decodeNotice(body []byte) (*Notice, error) {
	if len(body) < 2 {
		return nil, bad("NOTICE body is %d bytes, shorter than its 2-byte header", len(body))
	}
	if n := int(body[1]); len(body) != 2+n {
		return nil, bad("NOTICE text length %d, but %d bytes follow", n, len(body)-2)
	}
	m := &Notice{Code: NoticeCode(body[0]), Text: string(body[2:])}
	return validated(m)
}

// CtrlOp is a CTRL operation.
type CtrlOp uint8

const (
	CtrlResync    CtrlOp = 1 // send a fresh WORLD (either way); Arg is not interpreted here
	CtrlCoopOff   CtrlOp = 2 // the host's feature mask (Arg) for the session
	CtrlTombstone CtrlOp = 3 // the host blocks one bit for good; Arg is TombstoneArg(off, mask)
)

func (o CtrlOp) String() string {
	switch o {
	case CtrlResync:
		return "RESYNC"
	case CtrlCoopOff:
		return "COOP_OFF"
	case CtrlTombstone:
		return "TOMBSTONE"
	}
	return fmt.Sprintf("CtrlOp(%d)", uint8(o))
}

// Ctrl (0x08, both ways):
//
//	[op:u8][arg:u32]   = 5 B
//
// Which side may send which op (TOMBSTONE is the host's alone) is the
// hub's rule; the codec checks the op and a TOMBSTONE's target.
type Ctrl struct {
	Op  CtrlOp
	Arg uint32
}

const ctrlLen = 5

func (*Ctrl) Sub() Sub { return SubCtrl }

// TombstoneArg packs a TOMBSTONE target, bits mask of image byte off, into
// a CTRL arg: off<<8 | mask.
func TombstoneArg(off uint16, mask byte) uint32 { return uint32(off)<<8 | uint32(mask) }

// Tombstone unpacks a TOMBSTONE's target. ok is false for another op.
func (m *Ctrl) Tombstone() (off uint16, mask byte, ok bool) {
	if m.Op != CtrlTombstone {
		return 0, 0, false
	}
	return uint16(m.Arg >> 8), byte(m.Arg), true
}

func (m *Ctrl) validate() error {
	switch m.Op {
	case CtrlResync, CtrlCoopOff:
		return nil
	case CtrlTombstone:
		if m.Arg>>24 != 0 {
			return bad("TOMBSTONE arg 0x%08X has bits above its offset", m.Arg)
		}
		off, mask, _ := m.Tombstone()
		if mask == 0 {
			return bad("TOMBSTONE of image byte 0x%03X names no bit", off)
		}
		return checkShared("TOMBSTONE", off, mask)
	}
	return bad("CTRL op %d", m.Op)
}

func (m *Ctrl) appendBody(b []byte) []byte {
	b = append(b, byte(m.Op))
	return binary.BigEndian.AppendUint32(b, m.Arg)
}

func decodeCtrl(body []byte) (*Ctrl, error) {
	if len(body) != ctrlLen {
		return nil, bad("CTRL body is %d bytes, want %d", len(body), ctrlLen)
	}
	m := &Ctrl{Op: CtrlOp(body[0]), Arg: binary.BigEndian.Uint32(body[1:5])}
	return validated(m)
}

// checkShared reports why bits val of image byte off can't travel as a
// gain: the offset is outside the image, its class does not merge, or val
// has a bit outside the class mask.
func checkShared(what string, off uint16, val byte) error {
	if int(off) >= addr.SaveImageSize {
		return bad("%s offset 0x%03X is outside the 0x%X-byte image", what, off, addr.SaveImageSize)
	}
	r := class.Of(int(off))
	if !r.Class.Merges() {
		return bad("%s offset 0x%03X (%s) is %s, which never travels", what, off, r.Field, r.Class)
	}
	if val&^r.Mask != 0 {
		return bad("%s offset 0x%03X (%s): bits 0x%02X are outside its %s mask 0x%02X", what, off, r.Field, val&^r.Mask, r.Class, r.Mask)
	}
	return nil
}
