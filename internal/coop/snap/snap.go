// Package snap reads, once per tick, the game state the co-op gates and
// differ work from (§4.2): the whole dSv_info_c save, the play struct's
// stage block and player block, the current room, every room's zone
// number, the pause-menu byte, the mod's heartbeat and the GBA link's
// state (0x129 bytes of g_mDoGaC_gbaCom, read once): about 5 KiB of
// state a tick at 10 Hz (10 KiB read, as the save is read twice), as one
// Snapshot, so every decision in a tick sees the same state.
//
// The game writes the save while Go reads it (card_to_memory at file
// select, getSave and putSave at a stage change, actors every frame), so a
// read can catch a struct half copied. Read takes the save twice, first
// and last, and for every sensitive byte (the flags and MAX fields that
// merge between players, live and saved) keeps a bit only if both reads
// had it. A torn read can then hide a gain for a tick, never invent one.
package snap

import (
	"encoding/binary"
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// The three bulk regions a snapshot reads (§4.2).
var (
	// SaveRegion is g_dComIfG_gameInfo.save, the whole dSv_info_c: the
	// saved image, the live memBit, mDan, the zones, mRestart, mTmp and
	// mDataNum (d_save.h:991-1001).
	SaveRegion = addr.Region{Name: "save", Start: addr.SaveInfo, End: addr.SaveInfo + addr.SaveInfoSize}

	// StageRegion is play +0x3E94..+0x4010: mCurStage, mNextStage,
	// mStageData and mEvtCtrl up to mHindFlag (d_com_inf_game.h:723-728,
	// d_event.h:151-162).
	StageRegion = addr.Region{Name: "play stage block", Start: addr.CurStage, End: stageEnd}

	// PlayerRegion is play +0x48AC..+0x4A3C: mpPlayerPtr, the HUD's
	// pending counters, the button items, the refight byte and
	// mMiniGameType (d_com_inf_game.h:791-886).
	PlayerRegion = addr.Region{Name: "play player block", Start: addr.PlayerPtr, End: playerEnd}
)

const (
	stageEnd  uint32 = 0x803C9EB8 // play + 0x4010
	playerEnd uint32 = 0x803CA8E4 // play + 0x4A3C

	saveSize   = addr.SaveInfoSize
	stageSize  = int(stageEnd - addr.CurStage)
	playerSize = int(playerEnd - addr.PlayerPtr)
)

// Snapshot is one tick's view of the game. Read fills the raw regions and
// decodes the fields below from them; the gates read only the decoded
// fields, so a test can build a Snapshot from those alone.
type Snapshot struct {
	// Save is dSv_info_c (SaveRegion) as read, except that each sensitive
	// byte holds only what both reads agreed on (see Read).
	Save [saveSize]byte
	// Stage and Player are StageRegion and PlayerRegion as read.
	Stage  [stageSize]byte
	Player [playerSize]byte

	// Torn counts the sensitive bytes whose two reads differed. Nonzero
	// means the game was rewriting its save during the read: a stage
	// load, a file load or a flag being set.
	Torn int

	// mCurStage, the loaded stage. StageName is "" before the first
	// stage, "sea_T" on the title and "Name" on file select. StageRoom is
	// the room the stage was entered in; the room Link is in is StayNo.
	StageName  string
	StagePoint int16
	StageRoom  int8
	StageLayer int8

	// NextEnable is mNextStage.mEnable: 1 from the moment a stage change
	// is asked for until the new stage starts.
	NextEnable int8
	// StagInfo is mStageData.mpStagInfo, the loaded STAG chunk.
	StagInfo uint32
	// EvtOrderCount and EvtMode are mEvtCtrl's: 0 and 0 when no event
	// (cutscene, talk, item get) runs or is queued.
	EvtOrderCount int8
	EvtMode       uint8

	// PlayerPtr is mpPlayerPtr[0], Link's actor; 0 while no stage with a
	// player is loaded.
	PlayerPtr uint32
	// The HUD's pending counters (s16; 0 when nothing is pending).
	KeyNumCount, MaxLifeCount, MaxMagicCount int16
	// RefightStageNo is mPlayerInfoBufferStageNo: nonzero while a Ganon's
	// Tower boss-refight snapshot is held.
	RefightStageNo uint8
	MiniGameType   uint8

	// Table is mDan.mStageNo: the save table getSave last loaded into the
	// live memBit, or -1 after dSv_info_c::init until the next stage load.
	Table int8
	// DataNum is mDataNum, the file slot loaded.
	DataNum uint8

	// StayNo is mStayNo, the room Link is in (-1 at a stage start).
	StayNo int8
	// ZoneCount and ZoneNo are each room's mStatus mZoneCount and mZoneNo.
	ZoneCount [addr.RoomCount]int8
	ZoneNo    [addr.RoomCount]int8
	// MenuPause is dMenu_pause: nonzero while the pause menu is up.
	MenuPause uint8
	// Heartbeat is the mailbox spawn_trigger, bumped once per frame.
	Heartbeat uint32

	// GBA is the fields of g_mDoGaC_gbaCom that mDoGaC_GbaLink reads
	// (addr.GbaCom): whether a Game Boy Advance (the Tingle Tuner) is,
	// or may be, linked.
	GBA GBALink
}

// GBALink is what mDoGaC_GbaLink reads of g_mDoGaC_gbaCom (m_Do_gba_com.
// cpp:258-270; addr.GbaComOff*).
type GBALink struct {
	Enable  uint8 // mEnable
	State   uint8 // field_0x2, the connect state
	Closed  uint8 // field_0x4
	Retries uint8 // field_0x10e
	Context uint8 // field_0x128[0].field_0x0
}

// Linked is mDoGaC_GbaLink's own condition: the game takes a GBA as
// linked this frame.
func (g GBALink) Linked() bool {
	return g.Enable == 1 && g.State == 3 && g.Closed == 0 || g.Context == 1 && g.Retries < 3
}

// MayLink is the conservative superset co-op guards on: true whenever
// Linked is, and from the moment the Tuner turns the link on (mEnable,
// set as its upload starts, d_a_agb.cpp:491) or a GBA's context first
// checks out (field_0x128[0].field_0x0, m_Do_gba_com.cpp:518) until
// mDoGaC_GbaReboot clears both (:162, :168). Without a GBA neither is
// ever set; with one, MayLink stays true through a dropped link until the
// game reboots the link (a reset, or calling Tingle again), so a link
// that comes back between two ticks can't be missed.
func (g GBALink) MayLink() bool { return g.Enable != 0 || g.Context != 0 }

// Read takes one snapshot: the save first, then the play blocks, the room
// bytes, the pause byte and the heartbeat, then the save again, folding
// the two save reads together byte by byte:
//
//   - OR and OR_MASKED flags (the image's, the live memBit's, the dan
//     switches, every zone's switch, item and dead-actor bits): a bit
//     within the class mask only if both reads have it;
//   - MAX fields: the lower of the two values within the class mask;
//   - everything else (counts, pointers, names, per-player data): the
//     first read.
//
// Any read error fails the whole snapshot.
func Read(m mem.Mem) (*Snapshot, error) {
	s := new(Snapshot)
	first, err := m.Read(SaveRegion.Start, saveSize)
	if err != nil {
		return nil, fmt.Errorf("snap: %s: %w", SaveRegion.Name, err)
	}
	if err := readInto(m, StageRegion, s.Stage[:]); err != nil {
		return nil, err
	}
	if err := readInto(m, PlayerRegion, s.Player[:]); err != nil {
		return nil, err
	}
	stay, err := mem.ReadU8(m, addr.StayNo)
	if err != nil {
		return nil, fmt.Errorf("snap: mStayNo: %w", err)
	}
	s.StayNo = int8(stay)
	for room := range addr.RoomCount {
		// mZoneCount and mZoneNo are adjacent (d_stage.h:861-862).
		a, _ := addr.RoomStatusAddr(room)
		b, err := m.Read(a+addr.RoomStatusOffZoneCount, 2)
		if err != nil {
			return nil, fmt.Errorf("snap: room %d status: %w", room, err)
		}
		s.ZoneCount[room], s.ZoneNo[room] = int8(b[0]), int8(b[1])
	}
	if s.MenuPause, err = mem.ReadU8(m, addr.MenuPause); err != nil {
		return nil, fmt.Errorf("snap: dMenu_pause: %w", err)
	}
	if s.Heartbeat, err = mem.ReadU32(m, mailbox.SpawnTrigger); err != nil {
		return nil, fmt.Errorf("snap: heartbeat: %w", err)
	}
	gba, err := m.Read(addr.GbaCom, addr.GbaComLinkReadLen)
	if err != nil {
		return nil, fmt.Errorf("snap: g_mDoGaC_gbaCom: %w", err)
	}
	s.GBA = GBALink{
		Enable: gba[addr.GbaComOffEnable], State: gba[addr.GbaComOffState], Closed: gba[addr.GbaComOffClosed],
		Retries: gba[addr.GbaComOffRetries], Context: gba[addr.GbaComOffContext],
	}
	second, err := m.Read(SaveRegion.Start, saveSize)
	if err != nil {
		return nil, fmt.Errorf("snap: %s (second read): %w", SaveRegion.Name, err)
	}
	s.Torn = agree(s.Save[:], first, second)
	s.decode()
	return s, nil
}

func readInto(m mem.Mem, r addr.Region, dst []byte) error {
	b, err := m.Read(r.Start, int(r.Size()))
	if err != nil {
		return fmt.Errorf("snap: %s: %w", r.Name, err)
	}
	copy(dst, b)
	return nil
}

// decode fills the decoded fields from the raw regions.
func (s *Snapshot) decode() {
	st := func(a uint32) []byte { return s.Stage[a-StageRegion.Start:] }
	pl := func(a uint32) []byte { return s.Player[a-PlayerRegion.Start:] }
	sv := func(a uint32) []byte { return s.Save[a-SaveRegion.Start:] }

	name := st(addr.CurStageName)[:addr.StageNameLen]
	for i, c := range name {
		if c == 0 {
			name = name[:i]
			break
		}
	}
	s.StageName = string(name)
	s.StagePoint = int16(binary.BigEndian.Uint16(st(addr.CurStagePoint)))
	s.StageRoom = int8(st(addr.CurStageRoomNo)[0])
	s.StageLayer = int8(st(addr.CurStageLayer)[0])
	s.NextEnable = int8(st(addr.NextStageEnable)[0])
	s.StagInfo = binary.BigEndian.Uint32(st(addr.StagInfoPtr))
	s.EvtOrderCount = int8(st(addr.EvtOrderCount)[0])
	s.EvtMode = st(addr.EvtMode)[0]

	s.PlayerPtr = binary.BigEndian.Uint32(pl(addr.PlayerPtr))
	s.KeyNumCount = int16(binary.BigEndian.Uint16(pl(addr.ItemKeyNumCount)))
	s.MaxLifeCount = int16(binary.BigEndian.Uint16(pl(addr.ItemMaxLifeCount)))
	s.MaxMagicCount = int16(binary.BigEndian.Uint16(pl(addr.ItemMaxMagicCount)))
	s.RefightStageNo = pl(addr.PlayerInfoBufStageNo)[0]
	s.MiniGameType = pl(addr.MiniGameType)[0]

	s.Table = int8(sv(addr.DanStageNo)[0])
	s.DataNum = sv(addr.DataNum)[0]
}

// Image returns the canonical dSv_save_c image (§4.2 Differ): the saved
// image with the live memBit of the current table in place of that
// table's saved slot. It is what putSave would leave, so what the card
// would get if the player saved now. While Table is not a valid table
// (after dSv_info_c::init) the saved image is returned as is.
func (s *Snapshot) Image() [addr.SaveImageSize]byte {
	var img [addr.SaveImageSize]byte
	copy(img[:], s.Save[:addr.SaveImageSize])
	if t := int(s.Table); addr.ValidTable(t) {
		live := addr.InfoOffMemory
		copy(img[addr.OffMemBit(t):], s.Save[live:live+addr.MemBitSize])
	}
	return img
}

// ZoneFor returns room's zone slot if a zone write to it is valid (§3.1):
// 0 <= mStatus[room].mZoneNo < 32 and that zone's mRoomNo is room. A zone
// slot freed by removeZone keeps its old bits, so the room check matters.
func (s *Snapshot) ZoneFor(room int) (int, bool) {
	if room < 0 || room >= addr.RoomCount {
		return 0, false
	}
	z := int(s.ZoneNo[room])
	if z < 0 || z >= addr.ZoneCount {
		return 0, false
	}
	at := addr.InfoOffZone + z*addr.ZoneSize + addr.ZoneOffRoomNo
	if int(int8(s.Save[at])) != room {
		return 0, false
	}
	return z, true
}

// fold is how agree combines the two reads of one save byte.
type fold uint8

const (
	foldFirst fold = iota // keep the first read
	foldBits              // flags: a bit within mask only if both reads have it
	foldMax               // MAX field within mask: the lower read
)

type foldRule struct {
	fold fold
	mask byte
}

// folds is the fold rule of every dSv_info_c byte, built from the class
// table so the two never disagree about what is a flag.
var folds = buildFolds()

func buildFolds() [saveSize]foldRule {
	var f [saveSize]foldRule
	fromClass := func(r class.Rule) foldRule {
		switch r.Class {
		case class.OR, class.ORMasked:
			return foldRule{foldBits, r.Mask}
		case class.Max:
			return foldRule{foldMax, r.Mask}
		}
		return foldRule{}
	}
	for off := range addr.SaveImageSize {
		f[off] = fromClass(class.Of(off))
	}
	// The live memBit has the layout, and so the classes, of every saved
	// slot.
	for i := range addr.MemBitSize {
		f[addr.InfoOffMemory+i] = fromClass(class.Of(addr.OffMemBit(0) + i))
	}
	for i := range 8 {
		f[addr.InfoOffDan+addr.DanOffSwitch+i] = foldRule{foldBits, 0xFF}
	}
	for z := range addr.ZoneCount {
		base := addr.InfoOffZone + z*addr.ZoneSize
		for i := addr.ZoneOffSwitch; i < addr.ZoneSize; i++ {
			f[base+i] = foldRule{foldBits, 0xFF}
		}
	}
	return f
}

// agree writes the fold of reads a and b into dst and returns how many
// sensitive bytes differed within their mask.
func agree(dst, a, b []byte) int {
	torn := 0
	for i, r := range folds {
		x, y := a[i], b[i]
		switch r.fold {
		case foldFirst:
			dst[i] = x
			continue
		case foldBits:
			dst[i] = x&y&r.mask | x&^r.mask
		case foldMax:
			dst[i] = min(x&r.mask, y&r.mask) | x&^r.mask
		}
		if (x^y)&r.mask != 0 {
			torn++
		}
	}
	return torn
}
