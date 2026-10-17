package wire

import (
	"encoding/binary"
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// PresenceFlags are PRESENCE's flag bits.
type PresenceFlags uint8

const (
	PresenceInGame        PresenceFlags = 1 << iota // a stage with Link is loaded
	PresenceInEvent                                 // an event (cutscene, talk, item get) runs
	PresenceTransitioning                           // a stage change is pending
	PresencePaused                                  // the pause menu is up, or the heartbeat stalled
	PresenceArmed                                   // the replica is armed (§3.2 ARMED)
	presenceFlagsKnown    = 1<<iota - 1
)

// MaxLayer is the highest stage layer: ACT0..ACTb (d_stage.cpp:2144-2148;
// stagedata.NumLayers). mLayer is -1 when the game picks the layer itself
// by time of day (dComIfG_play_c::getLayerNo, d_com_inf_game.cpp:185-190).
const MaxLayer = 11

// Presence (0x05, c->s and relayed s->c) says where a player is. A replica
// sends it on every change and at 1 Hz; it scopes the hub's dan, zone and
// kill stores (§4.4):
//
//	[stage:8][table:s8][room:s8][layer:s8][point:s16][flags:u8]   = 14 B
//
// The relayed copy is the same bytes and names no sender, which is enough
// for the two players v1.0 supports.
type Presence struct {
	Stage Stage // mCurStage.mName; empty before the first stage
	Table int8  // mDan.mStageNo: the loaded save table, or -1
	Room  int8  // mStayNo, the room Link is in, or -1 at a stage start
	Layer int8  // mCurStage.mLayer: 0..MaxLayer, or -1
	Point int16 // mCurStage.mPoint, the spawn point
	Flags PresenceFlags
}

const presenceLen = 14

func (*Presence) Sub() Sub { return SubPresence }

func (m *Presence) validate() error {
	if err := m.Stage.check("PRESENCE stage"); err != nil {
		return err
	}
	if m.Table != -1 && !validTable(m.Table) {
		return bad("PRESENCE table %d (want -1 or 0..%d)", m.Table, addr.TableCount-1)
	}
	if m.Room != -1 && !validRoom(m.Room) {
		return bad("PRESENCE room %d (want -1 or 0..%d)", m.Room, addr.RoomCount-1)
	}
	if m.Layer < -1 || m.Layer > MaxLayer {
		return bad("PRESENCE layer %d (want -1 or 0..%d)", m.Layer, MaxLayer)
	}
	if m.Flags&^presenceFlagsKnown != 0 {
		return bad("PRESENCE flags 0x%02X has unknown bits", uint8(m.Flags))
	}
	return nil
}

func (m *Presence) appendBody(b []byte) []byte {
	b = append(b, m.Stage[:]...)
	b = append(b, byte(m.Table), byte(m.Room), byte(m.Layer))
	b = binary.BigEndian.AppendUint16(b, uint16(m.Point))
	return append(b, byte(m.Flags))
}

func decodePresence(body []byte) (*Presence, error) {
	if len(body) != presenceLen {
		return nil, bad("PRESENCE body is %d bytes, want %d", len(body), presenceLen)
	}
	m := &Presence{
		Table: int8(body[8]),
		Room:  int8(body[9]),
		Layer: int8(body[10]),
		Point: int16(binary.BigEndian.Uint16(body[11:13])),
		Flags: PresenceFlags(body[13]),
	}
	copy(m.Stage[:], body[0:8])
	return validated(m)
}

// ScopeKind is what a SCOPED message's words are. Word w of a kind is
// element w of the game array named below, and bit b of it is the bit the
// game's setter sets with array[w] |= 1 << b.
type ScopeKind uint8

const (
	// ScopeDan: mDan.mSwitch, u32[2], dungeon switches 0x80-0xBF
	// (dSv_danBit_c, d_save.h:752-754; onSwitch, d_save.cpp:1237-1240).
	// Stored per stage and table by the hub.
	ScopeDan ScopeKind = 1
	// ScopeZone: a zone's mZoneBit.mSwitch[0..1], u16 words, room
	// switches 0xC0-0xDF (dSv_zoneBit_c, d_save.h:770; onSwitch,
	// d_save.cpp:1279-1282). Stored per stage and room.
	ScopeZone ScopeKind = 2
	// ScopeRoomEdge: mZoneBit.mSwitch[2], the one-shot switches
	// 0xE0-0xEF that clearRoomSwitch zeroes on every room change
	// (d_save.cpp:1273-1276). Word 2 only; relayed, never stored.
	ScopeRoomEdge ScopeKind = 3
	// ScopeKill: a zone's mActorFlags, u32[16], the 512 dead-actor bits
	// by setID (dSv_zoneActor_c, d_save.h:782; on, d_save.cpp:1325-1328).
	// Stored per stage and room.
	ScopeKill ScopeKind = 4
)

func (k ScopeKind) String() string {
	switch k {
	case ScopeDan:
		return "DAN"
	case ScopeZone:
		return "ZONE"
	case ScopeRoomEdge:
		return "ROOMEDGE"
	case ScopeKill:
		return "KILL"
	}
	return fmt.Sprintf("ScopeKind(%d)", uint8(k))
}

// words returns the word range [first, last] of kind k and the bits a word
// can hold. ok is false for an unknown kind.
func (k ScopeKind) words() (first, last int, bits uint32, ok bool) {
	switch k {
	case ScopeDan:
		return 0, addr.DanSwitchCount/32 - 1, 0xFFFFFFFF, true
	case ScopeZone:
		return 0, 1, 0xFFFF, true
	case ScopeRoomEdge:
		return 2, 2, 0xFFFF, true
	case ScopeKill:
		return 0, addr.ZoneActorCount/32 - 1, 0xFFFFFFFF, true
	}
	return 0, 0, 0, false
}

// ScopedWord is one word of a SCOPED message.
type ScopedWord struct {
	Word uint8
	Bits uint32
}

// scopedWordLen is one ScopedWord on the wire: {word:u8, bits:u32}.
const scopedWordLen = 5

// Scoped (0x06, both ways) carries puzzle and kill state that only matters
// to players in the same stage or room (§4.4):
//
//	[kind:u8][stage:8][table:s8][room:s8][n:u8] n x {word:u8, bits:u32}   = 12 + 5n B
//
// Words are strictly ascending and in range for the kind. Table is 0..15
// (a scoped write needs a loaded table, §3.2 STABLE). Room is 0..63, or -1
// for ScopeDan, whose store has no room.
type Scoped struct {
	Kind  ScopeKind
	Stage Stage
	Table int8
	Room  int8
	Words []ScopedWord
}

const scopedHeaderLen = 12

func (*Scoped) Sub() Sub { return SubScoped }

func (m *Scoped) validate() error {
	first, last, bits, ok := m.Kind.words()
	if !ok {
		return bad("SCOPED kind %d", m.Kind)
	}
	if err := m.Stage.check("SCOPED " + m.Kind.String() + " stage"); err != nil {
		return err
	}
	if m.Stage.IsEmpty() {
		return bad("SCOPED %s has no stage", m.Kind)
	}
	if !validTable(m.Table) {
		return bad("SCOPED %s table %d (want 0..%d)", m.Kind, m.Table, addr.TableCount-1)
	}
	if !validRoom(m.Room) && !(m.Kind == ScopeDan && m.Room == -1) {
		return bad("SCOPED %s room %d (want 0..%d)", m.Kind, m.Room, addr.RoomCount-1)
	}
	if len(m.Words) > last-first+1 {
		return bad("SCOPED %s has %d words (max %d)", m.Kind, len(m.Words), last-first+1)
	}
	for i, w := range m.Words {
		if int(w.Word) < first || int(w.Word) > last {
			return bad("SCOPED %s word %d (want %d..%d)", m.Kind, w.Word, first, last)
		}
		if w.Bits&^bits != 0 {
			return bad("SCOPED %s word %d bits 0x%08X exceed the word's 0x%X", m.Kind, w.Word, w.Bits, bits)
		}
		if i > 0 && w.Word <= m.Words[i-1].Word {
			return bad("SCOPED %s words %d then %d are not strictly ascending", m.Kind, m.Words[i-1].Word, w.Word)
		}
	}
	return nil
}

func (m *Scoped) appendBody(b []byte) []byte {
	b = append(b, byte(m.Kind))
	b = append(b, m.Stage[:]...)
	b = append(b, byte(m.Table), byte(m.Room), byte(len(m.Words)))
	for _, w := range m.Words {
		b = append(b, w.Word)
		b = binary.BigEndian.AppendUint32(b, w.Bits)
	}
	return b
}

func decodeScoped(body []byte) (*Scoped, error) {
	if len(body) < scopedHeaderLen {
		return nil, bad("SCOPED body is %d bytes, shorter than its %d-byte header", len(body), scopedHeaderLen)
	}
	n := int(body[11])
	if len(body)-scopedHeaderLen != n*scopedWordLen {
		return nil, bad("SCOPED count %d needs %d bytes, %d follow", n, n*scopedWordLen, len(body)-scopedHeaderLen)
	}
	m := &Scoped{Kind: ScopeKind(body[0]), Table: int8(body[9]), Room: int8(body[10])}
	copy(m.Stage[:], body[1:9])
	if n > 0 {
		m.Words = make([]ScopedWord, n)
	}
	for i := range m.Words {
		e := body[scopedHeaderLen+i*scopedWordLen:]
		m.Words[i] = ScopedWord{Word: e[0], Bits: binary.BigEndian.Uint32(e[1:5])}
	}
	return validated(m)
}
