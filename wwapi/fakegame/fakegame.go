// Package fakegame is a small, deterministic model of the vanilla save
// logic the co-op design (#23) relies on, running over a mem.FakeMem at
// the real GZLE01 addresses. Tests drive it the way the console would
// (load a stage, walk between rooms, run frames, hit a switch, start a New
// Game) and point the real snapshot, gate and replica code at the same
// FakeMem.
//
// It models exactly the behaviours §3.1, §4.2 and §5.2 depend on, each
// citing the decomp function it copies (zeldaret/tww at 4d78c994, cited as
// in wwapi/addr):
//
//   - stage load: getSave copies mMemory[t] into the live memBit, then
//     initDan resets the dan switches when the table changes;
//   - stage delete: putSave copies the live memBit back to mMemory[t];
//   - room change: clearRoomSwitch zeroes every live zone's room switches
//     (0xE0-0xEF), and zones are created first-free and removed two room
//     changes after their room was last loaded;
//   - the HUD consuming the pending key, max-life and max-magic counters;
//   - New Game (dComIfGs_init), which leaves mReserve alone, the save
//     menu, card_to_memory and the second-quest ExDataSave (save.go);
//   - the mod's heartbeat, spawn_trigger++ once per frame;
//   - the LR word boot leaves above the main stack top (mailbox.BootLRSlot),
//     beside the region-probe canaries;
//   - the game's own onSwitch, offSwitch, onEventBit and onActor writers,
//     which refuse what the game would JUT_ASSERT on.
//
// Everything else (actors, events, time of day, the actual stage data) is
// left out; a test sets the gate inputs it needs with the Set* methods.
//
// Every game write goes through FakeMem.Store, so FakeMem's write recorder
// sees only the tool's writes. The game's flag setters do a word load, OR
// and store like the real ones (d_save.cpp:1084-1087), so a tool byte
// written into the same word between the load and the store is lost here
// too (§4.2.1).
package fakegame

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// Addresses the fake invents. On the console the game allocates these on
// a heap; any MEM1 address clear of the game's statics serves.
const (
	// PlayerActor is Link's actor while a stage with a player is loaded:
	// what mpPlayerPtr[0] holds.
	PlayerActor uint32 = 0x80A00000

	// StagChunk stands in for the loaded stage's STAG chunk, which
	// mStageData.mpStagInfo points at. Only its mProp byte is written.
	StagChunk uint32 = 0x80A01000
)

// oldStayNo is dStage_roomControl_c::mOldStayNo, the room before mStayNo
// (sym:18668; setStayNo, d_stage.cpp:171-180). Only zoneCountCheck reads
// it, so it lives here rather than in wwapi/addr.
const oldStayNo uint32 = 0x803F6A79

// ErrAssert is matched (errors.Is) by every call the real game would halt
// on: JUT_ASSERT is live in retail and calls OSPanic (§3.1). The call
// changes nothing.
var ErrAssert = errors.New("fakegame: the game would JUT_ASSERT and halt")

func assertf(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrAssert}, args...)...)
}

// Stage is a stage to load: what dComIfGp_setNextStage takes
// (d_com_inf_game.cpp:666-684), plus the STAG facts the fake needs.
type Stage struct {
	Name  string // at most 7 bytes: mName is char[8]
	Point int16  // start point; >= 0 (every normal entry) wipes the zones (d_stage.cpp:124-126)
	Room  int8   // the room Link starts in
	Layer int8

	Table  int  // save table: STAG mProp >> 1 (dStage_stagInfo_GetSaveTbl, d_stage.h:1027-1029)
	KeyHUD bool // the small-key counter is shown: STAG mProp & 1 (d_stage.h:1023-1025)

	// NoPlayer loads the stage without a Link actor, as the title screen
	// does with sea_T.
	NoPlayer bool
}

func (s Stage) validate() error {
	if len(s.Name) == 0 || len(s.Name) >= addr.StageNameLen {
		return fmt.Errorf("fakegame: stage name %q must be 1-%d bytes", s.Name, addr.StageNameLen-1)
	}
	if !addr.ValidTable(s.Table) {
		return assertf("getSave of table %d", s.Table) // d_save.cpp:1507
	}
	if s.Room < 0 || int(s.Room) >= addr.RoomCount {
		return fmt.Errorf("fakegame: start room %d out of range 0..%d", s.Room, addr.RoomCount-1)
	}
	return nil
}

// FakeGame is the emulated game. Its methods are safe for concurrent use;
// each runs to completion under one lock, the way game code runs on the
// one PPC thread. Tool reads and writes of the FakeMem are not serialised
// with it, just as on the console.
//
// A method panics only on a bug in the fake itself: every address it
// touches is a constant or has been validated.
type FakeGame struct {
	mu      sync.Mutex
	m       *mem.FakeMem
	playing bool             // a play scene is up: a stage is loaded and the HUD runs
	stags   map[string]Stage // the STAG facts of each stage SetNextStage has named
	frames  uint64
	// loads counts the stages loaded (ChangeStage): every one creates its
	// rooms' actors anew, which KeyDoor and Enemies follow.
	loads uint64
	card  [CardSlots][PackedSize]byte
}

// New returns the game at boot: __init_hardware's return left at
// 4(_stack_addr) (mailbox.MainStackHeader), the save as dSv_info_c::init
// leaves it (d_save.cpp:1389-1398), no stage loaded, and every room's zone
// number -1 as dStage_roomControl_c::init sets it (d_stage.cpp:120-150).
// The card is blank. m should be fresh, so the heartbeat starts at 0.
func New(m *mem.FakeMem) *FakeGame {
	g := &FakeGame{m: m, stags: make(map[string]Stage)}
	g.st32(mailbox.BootLRSlot, mailbox.BootLR)
	g.infoInit()
	g.roomControlInit(0)
	return g
}

// Mem returns the FakeMem the game runs over.
func (g *FakeGame) Mem() *mem.FakeMem { return g.m }

// Frame runs one game frame: the actors' execute, of which the fake has
// only the HUD's pending counters (hud), then frame_shim's
// multiplayer_update, whose first act is spawn_trigger++
// (multiplayer.c:341-342). fapGm_Execute runs the shim after
// fpcM_Management (f_ap_game.cpp:77-81), so the heartbeat is the frame's
// last write.
func (g *FakeGame) Frame() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.frame()
}

// Frames runs n frames.
func (g *FakeGame) Frames(n int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for range n {
		g.frame()
	}
}

func (g *FakeGame) frame() {
	if g.playing {
		g.hud()
	}
	g.st32(mailbox.SpawnTrigger, g.ld32(mailbox.SpawnTrigger)+1)
	g.frames++
}

// FrameCount is the number of frames run since New.
func (g *FakeGame) FrameCount() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.frames
}

// hud applies the pending counters the way the meter does each frame, then
// zeroes each one it applied. Go's derived keys and hearts are written
// through these counters (§6.4), so their semantics matter.
func (g *FakeGame) hud() {
	// dMeter_LifeMove (d_meter.cpp:1420-1452). A max-life count raises
	// mMaxLife, capped at 0x50 quarter hearts, and refills life.
	maxCount := int16(g.ld16(addr.ItemMaxLifeCount))
	lifeCount := math.Float32frombits(g.ld32(addr.ItemLifeCount))
	if maxCount != 0 || int16(lifeCount) != 0 {
		maxHP := int16(g.ld16(addr.MaxLife)) + maxCount
		var newHP int16
		if maxCount > 0 {
			newHP = maxHP / 4 * 4
		} else {
			newHP = int16(g.ld16(addr.Life)) + int16(lifeCount)
		}
		if maxCount != 0 {
			maxHP = min(max(maxHP, 0), 0x50)
			g.st16(addr.MaxLife, uint16(maxHP))
			g.st16(addr.ItemMaxLifeCount, 0)
		}
		newHP = min(max(newHP, 0), maxHP/4*4)
		g.st16(addr.Life, uint16(uint8(newHP)))
		g.st32(addr.ItemLifeCount, 0)
	}

	// dMeter_magicGaugeMove (d_meter.cpp:4069-4076): mMaxMagic capped at
	// 0x20, with no lower bound.
	if n := int16(g.ld16(addr.ItemMaxMagicCount)); n != 0 {
		v := min(int16(g.ld8(addr.MaxMagic))+n, 0x20)
		g.st8(addr.MaxMagic, uint8(v))
		g.st16(addr.ItemMaxMagicCount, 0)
	}

	// dMeter_keyMove (d_meter.cpp:5242-5257): only while the stage shows
	// the key counter. Elsewhere the count stays pending.
	if g.stagProp()&addr.StagPropKeyDisp != 0 {
		if n := int16(g.ld16(addr.ItemKeyNumCount)); n != 0 {
			v := min(max(int16(g.ld8(addr.LiveKeyNum))+n, 0), 99)
			g.st8(addr.LiveKeyNum, uint8(v))
			g.st16(addr.ItemKeyNumCount, 0)
		}
	}
}

// SetNextStage is dComIfGp_setNextStage: a loading zone, warp or cutscene
// asks for s. mNextStage takes it and mEnable becomes 1, unless mEnable is
// already 1, when the pending change wins (dStage_nextStage_c::set,
// d_stage.cpp:26-31). The current stage and Link stay until ChangeStage.
// The fake also remembers s's table and key HUD under its name: they are
// the stage's STAG data, which ChangeStage needs.
func (g *FakeGame) SetNextStage(s Stage) error {
	if err := s.validate(); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stags[s.Name] = s
	if g.ld8(addr.NextStageEnable) != 0 {
		return nil
	}
	g.writeStageRef(addr.NextStage, s.Name, s.Point, s.Room, s.Layer)
	g.st8(addr.NextStageEnable, 1)
	return nil
}

// ChangeStage carries out the stage change mNextStage holds, whether
// SetNextStage or a tool's write put it there (§6.7's warp), in the order
// the game does it:
//
//  1. dStage_Delete putSaves the old stage's table (d_stage.cpp:2248-2261)
//     and Link goes with the old play scene;
//  2. the new play scene copies mNextStage to mCurStage and clears mEnable
//     (d_s_play.cpp:1277-1278);
//  3. dStage_roomControl_c::init (d_stage.cpp:120-150);
//  4. dStage_stagInfoInit: getSave then initDan (d_stage.cpp:1670-1677);
//  5. Link is created and the start room loads (EnterRoom).
func (g *FakeGame) ChangeStage() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ld8(addr.NextStageEnable) == 0 {
		return errors.New("fakegame: no stage change pending")
	}
	ref := g.load(addr.NextStage, addr.StageSize)
	name, _, _ := strings.Cut(string(ref[:addr.StageNameLen]), "\x00")
	s, ok := g.stags[name]
	if !ok {
		return fmt.Errorf("fakegame: no STAG for stage %q: name it in SetNextStage first", name)
	}
	s.Point = int16(uint16(ref[addr.StageOffPoint])<<8 | uint16(ref[addr.StageOffPoint+1]))
	s.Room, s.Layer = int8(ref[addr.StageOffRoomNo]), int8(ref[addr.StageOffLayer])
	if err := s.validate(); err != nil {
		return err
	}
	g.leaveStage()

	g.writeStageRef(addr.CurStage, s.Name, s.Point, s.Room, s.Layer)
	g.st8(addr.NextStageEnable, 0)

	g.roomControlInit(s.Point)

	prop := byte(s.Table << 1)
	if s.KeyHUD {
		prop |= addr.StagPropKeyDisp
	}
	g.st8(StagChunk+addr.StagOffProp, prop)
	g.st32(addr.StagInfoPtr, StagChunk)
	g.getSave(s.Table)
	g.initDan(s.Table)

	g.playing = true
	g.loads++
	if !s.NoPlayer {
		g.st32(addr.PlayerPtr, PlayerActor)
	}
	g.enterRoom(int(s.Room), nil)
	return nil
}

// LoadStage is SetNextStage then ChangeStage. A change already pending
// wins, as in SetNextStage.
func (g *FakeGame) LoadStage(s Stage) error {
	if err := g.SetNextStage(s); err != nil {
		return err
	}
	return g.ChangeStage()
}

// EnterFileSelect leaves the stage for the file select and naming screen:
// the play scene's dStage_Delete putSave (d_stage.cpp:2248-2261), then
// dScnName_c::create sets the stage to "Name" (d_s_name.cpp:110-114).
func (g *FakeGame) EnterFileSelect() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.leaveStage()
	g.writeStageRef(addr.CurStage, addr.StageFileSelect, 0, 0, -1)
	g.writeStageRef(addr.NextStage, addr.StageFileSelect, 0, 0, -1)
	g.st8(addr.NextStageEnable, 0)
}

// leaveStage deletes the play scene, if one is up: putSave of the loaded
// stage's table, read back from its STAG as dStage_Delete does
// (d_stage.cpp:2255-2256), and Link with it.
func (g *FakeGame) leaveStage() {
	if !g.playing {
		return
	}
	g.putSave(addr.StagSaveTable(g.stagProp()))
	g.st32(addr.PlayerPtr, 0)
	g.playing = false
}

func (g *FakeGame) writeStageRef(a uint32, name string, point int16, room, layer int8) {
	var b [addr.StageSize]byte
	copy(b[:addr.StageNameLen-1], name) // dStage_startStage_c::set strcpys the name (d_stage.cpp:112-117)
	b[addr.StageOffPoint] = byte(uint16(point) >> 8)
	b[addr.StageOffPoint+1] = byte(point)
	b[addr.StageOffRoomNo] = byte(room)
	b[addr.StageOffLayer] = byte(layer)
	g.store(a, b[:])
}

func (g *FakeGame) stagProp() byte {
	p := g.ld32(addr.StagInfoPtr)
	if p == 0 || mem.CheckRange(p+addr.StagOffProp, 1) != nil {
		return 0
	}
	return g.ld8(p + addr.StagOffProp)
}

// roomControlInit is dStage_roomControl_c::init (d_stage.cpp:120-150).
// Entering at a start point >= 0 wipes every zone; a negative point keeps
// them and only clears their room switches.
func (g *FakeGame) roomControlInit(point int16) {
	g.st8(addr.StayNo, 0xFF)
	g.st8(oldStayNo, 0xFF)
	if point >= 0 {
		g.initZone()
	}
	for room := range addr.RoomCount {
		if point >= 0 {
			g.setZoneNo(room, -1)
		} else if z := g.zoneNo(room); z >= 0 {
			g.clearRoomSwitch(int(z))
		}
		// The old play scene's room scenes are gone, and a deleted room
		// scene clears its status flags (dScnRoom_Delete, d_s_room.cpp:120).
		g.setRoomFlags(room, 0)
	}
}

// setRoomFlags sets mStatus[room].mFlags (d_stage.h:855-869).
func (g *FakeGame) setRoomFlags(room int, f uint8) {
	a, _ := addr.RoomStatusAddr(room)
	g.st8(a+addr.RoomStatusOffFlags, f)
}

func (g *FakeGame) roomFlags(room int) uint8 {
	a, _ := addr.RoomStatusAddr(room)
	return g.ld8(a + addr.RoomStatusOffFlags)
}

// EnterRoom moves Link into room, with neighbours loaded alongside it, as
// dStage_RoomCheck does when his ground check lands in a new room
// (d_stage.cpp:2264-2290): zoneCountCheck when the room changed, then
// loadRoom, whose room scenes create any zone they lack.
func (g *FakeGame) EnterRoom(room int, neighbours ...int) error {
	for _, r := range append([]int{room}, neighbours...) {
		if r < 0 || r >= addr.RoomCount {
			return fmt.Errorf("fakegame: room %d out of range 0..%d", r, addr.RoomCount-1)
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.playing {
		return errors.New("fakegame: no stage loaded")
	}
	g.enterRoom(room, neighbours)
	return nil
}

func (g *FakeGame) enterRoom(room int, neighbours []int) {
	if int8(g.ld8(addr.StayNo)) != int8(room) {
		g.zoneCountCheck(room)
	}
	for _, r := range append([]int{room}, neighbours...) {
		// loadRoom gives every room it keeps loaded two more room
		// changes of zone life (d_stage.cpp:236); a room scene being
		// created makes a zone if its room has none (d_s_room.cpp:193-197).
		g.setZoneCount(r, 2)
		if g.zoneNo(r) < 0 {
			g.setZoneNo(r, g.createZone(r))
		}
		// loadRoom creates the room's scene (status 0x02, d_stage.cpp:
		// 233-239), whose phases end with the room loaded (0x01); the fake
		// takes both at once. A hidden room stays hidden.
		g.setRoomFlags(r, g.roomFlags(r)&addr.RoomFlagHidden|addr.RoomFlagLoaded)
	}
}

// zoneCountCheck is dStage_roomControl_c::zoneCountCheck
// (d_stage.cpp:250-267). Every live zone loses its room switches; unless
// Link went back to the room he came from, each counts down and is
// removed at zero. Then mStayNo moves (setStayNo, d_stage.cpp:171-180).
func (g *FakeGame) zoneCountCheck(room int) {
	old := int8(g.ld8(oldStayNo))
	for r := range addr.RoomCount {
		z, c := g.zoneNo(r), g.zoneCount(r)
		if z < 0 || c <= 0 {
			continue
		}
		g.clearRoomSwitch(int(z))
		if int8(room) != old {
			c--
			g.setZoneCount(r, int(c))
			if c == 0 {
				g.removeZone(int(z))
				g.setZoneNo(r, -1)
				g.setRoomFlags(r, 0) // its scene deleted (d_s_room.cpp:120)
			}
		}
	}
	g.st8(oldStayNo, g.ld8(addr.StayNo))
	g.st8(addr.StayNo, byte(room))
}

func (g *FakeGame) zoneNo(room int) int8 {
	a, _ := addr.ZoneNoAddr(room)
	return int8(g.ld8(a))
}

func (g *FakeGame) setZoneNo(room, z int) {
	a, _ := addr.ZoneNoAddr(room)
	g.st8(a, byte(z))
}

func (g *FakeGame) zoneCount(room int) int8 {
	a, _ := addr.RoomStatusAddr(room)
	return int8(g.ld8(a + addr.RoomStatusOffZoneCount))
}

func (g *FakeGame) setZoneCount(room, c int) {
	a, _ := addr.RoomStatusAddr(room)
	g.st8(a+addr.RoomStatusOffZoneCount, byte(c))
}

func zoneAddr(z int) uint32 {
	a, err := addr.ZoneAddr(z)
	if err != nil {
		panic(err)
	}
	return a
}

// createZone is dSv_info_c::createZone (d_save.cpp:1525-1537): the first
// zone whose mRoomNo is negative is initialised for room, or -1 if all 32
// are taken. Slots are first-free per machine, so they mean nothing across
// players (§3.1).
func (g *FakeGame) createZone(room int) int {
	for z := range addr.ZoneCount {
		if int8(g.ld8(zoneAddr(z)+addr.ZoneOffRoomNo)) < 0 {
			g.initZoneSlot(z, room)
			return z
		}
	}
	return -1
}

// initZoneSlot is dSv_zone_c::init (d_save.cpp:1338-1342): mRoomNo, then
// the zone's switches, items (dSv_zoneBit_c::init, d_save.cpp:1265-1271)
// and dead-actor bits (dSv_zoneActor_c::init, d_save.cpp:1319-1323) are
// zeroed. The pad byte at +1 is left alone.
func (g *FakeGame) initZoneSlot(z, room int) {
	a := zoneAddr(z)
	g.st8(a+addr.ZoneOffRoomNo, byte(room))
	g.store(a+addr.ZoneOffSwitch, make([]byte, addr.ZoneSize-addr.ZoneOffSwitch))
}

// removeZone is dSv_info_c::removeZone, which only marks the slot free
// (d_save.h:969, dSv_zone_c::reset at d_save.h:793). Its old bits stay
// until createZone reuses it, so a freed slot must not be read as a room's
// state.
func (g *FakeGame) removeZone(z int) {
	g.st8(zoneAddr(z)+addr.ZoneOffRoomNo, 0xFF)
}

// initZone is dSv_info_c::initZone (d_save.cpp:1518-1522).
func (g *FakeGame) initZone() {
	for z := range addr.ZoneCount {
		g.initZoneSlot(z, -1)
	}
}

// clearRoomSwitch is dSv_zoneBit_c::clearRoomSwitch (d_save.cpp:1274-1276):
// zone mSwitch[2], switches 0xE0-0xEF, is zeroed. Switches 0xC0-0xDF, the
// zone item bits and the dead actors stay.
func (g *FakeGame) clearRoomSwitch(z int) {
	g.st16(zoneAddr(z)+addr.ZoneOffSwitch+4, 0)
}

// getSave is dSv_info_c::getSave (d_save.cpp:1506-1509): the live memBit
// becomes a copy of table t's saved one.
func (g *FakeGame) getSave(t int) {
	saved, _ := addr.SavedMemBitAddr(t)
	g.store(addr.LiveMemBit, g.load(saved, addr.MemBitSize))
}

// putSave is dSv_info_c::putSave (d_save.cpp:1512-1515): table t's saved
// memBit becomes a copy of the live one.
func (g *FakeGame) putSave(t int) {
	saved, _ := addr.SavedMemBitAddr(t)
	g.store(saved, g.load(addr.LiveMemBit, addr.MemBitSize))
}

// initDan is dSv_info_c::initDan, i.e. dSv_danBit_c::init
// (d_save.cpp:1225-1235): a table other than the one mDan holds zeroes the
// dan switches and the GBA rupee count. The same table keeps them, so dan
// switches survive moving between stages that share a table.
func (g *FakeGame) initDan(t int) {
	if int8(g.ld8(addr.DanStageNo)) == int8(t) {
		return
	}
	g.store(addr.DanSwitch, make([]byte, 8))
	g.st8(addr.DanStageNo, byte(t))
	g.st8(addr.Dan+addr.DanOffGbaRupee, 0)
}

// OnSwitch is dSv_info_c::onSwitch (d_save.cpp:1540-1560). Switches
// 0x00-0x7F set the live memBit (not the saved slot; putSave carries them
// over), 0x80-0xBF the dan bits and 0xC0-0xEF the zone of room's
// mStatus.mZoneNo. -1 and 255 do nothing. Anything else, a room outside
// 0..63 for a zone switch, or a room with no zone, is ErrAssert.
func (g *FakeGame) OnSwitch(no, room int) error { return g.setSwitch(no, room, true) }

// OffSwitch is dSv_info_c::offSwitch (d_save.cpp:1563-1583), with the
// same routing and asserts as OnSwitch.
func (g *FakeGame) OffSwitch(no, room int) error { return g.setSwitch(no, room, false) }

func (g *FakeGame) setSwitch(no, room int, on bool) error {
	store, bit, err := addr.SwitchBit(no)
	switch store {
	case addr.SwitchNone:
		return nil
	case addr.SwitchInvalid:
		return assertf("switch %d: %v", no, err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	switch store {
	case addr.SwitchMemory:
		g.rmwBit(addr.LiveMemBit, 4, bit, on) // dSv_memBit_c::onSwitch, d_save.cpp:1084-1087
	case addr.SwitchDan:
		g.rmwBit(addr.Dan, 4, bit, on) // dSv_danBit_c::onSwitch, d_save.cpp:1238-1241
	default:
		z, err := g.roomZone(room)
		if err != nil {
			return fmt.Errorf("switch 0x%X: %w", no, err)
		}
		g.rmwBit(zoneAddr(z), 2, bit, on) // dSv_zoneBit_c::onSwitch, d_save.cpp:1279-1282
	}
	return nil
}

// roomZone is the zone dComIfGp_roomControl_getZoneNo gives room, with the
// two asserts every zone setter makes (d_save.cpp:1553-1556).
func (g *FakeGame) roomZone(room int) (int, error) {
	if room < 0 || room >= addr.RoomCount {
		return 0, assertf("room %d", room)
	}
	z := int(g.zoneNo(room))
	if z < 0 || z >= addr.ZoneCount {
		return 0, assertf("room %d has zone %d", room, z)
	}
	return z, nil
}

// OnActor is dSv_info_c::onActor (d_save.cpp:1671-1683): the dead-actor
// bit for setID id in room's zone, as an enemy's death sets it. id -1 or
// 0xFFFF (a runtime spawn) or room -1 do nothing.
func (g *FakeGame) OnActor(id, room int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.onActor(id, room)
}

// onActor is OnActor with g.mu held.
func (g *FakeGame) onActor(id, room int) error {
	if id == -1 || id == addr.ActorSetIDNone || room == -1 {
		return nil
	}
	bit, err := addr.ZoneActorBit(id)
	if err != nil {
		return assertf("actor id %d", id)
	}
	z, err := g.roomZone(room)
	if err != nil {
		return fmt.Errorf("actor %d: %w", id, err)
	}
	g.rmwBit(zoneAddr(z), 4, bit, true) // dSv_zoneActor_c::on, d_save.cpp:1326-1329
	return nil
}

// OnEventBit is dSv_event_c::onEventBit (d_save.cpp:1187-1189): a byte
// OR into the saved mEvent. There is no live copy of event bits.
func (g *FakeGame) OnEventBit(id uint16) { g.setEventBit(id, true) }

// OffEventBit is dSv_event_c::offEventBit (d_save.cpp:1192-1194).
func (g *FakeGame) OffEventBit(id uint16) { g.setEventBit(id, false) }

func (g *FakeGame) setEventBit(id uint16, on bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rmwBit(addr.Event, 1, addr.EventByte(id), on)
}

// rmwBit sets or clears bit (relative to the struct at base) by loading,
// changing and storing the whole size-byte word that holds it, as the
// game's word-wide |= and &= do.
func (g *FakeGame) rmwBit(base uint32, size int, bit addr.Bit, on bool) {
	at := base + uint32(bit.Off)
	word := at &^ uint32(size-1)
	b := g.load(word, size)
	if on {
		b[at-word] |= bit.Mask
	} else {
		b[at-word] &^= bit.Mask
	}
	g.store(word, b)
}

// The gate inputs (§3.2 IDLE) are set directly: the fake has no events,
// menus or minigames of its own.

// SetEvent sets mEvtCtrl.mMode and mOrderCount: 0, 0 when no event runs.
func (g *FakeGame) SetEvent(mode uint8, orderCount int8) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.st8(addr.EvtMode, mode)
	g.st8(addr.EvtOrderCount, byte(orderCount))
}

// SetPauseMenu sets dMenu_pause.
func (g *FakeGame) SetPauseMenu(open bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var v byte
	if open {
		v = 1
	}
	g.st8(addr.MenuPause, v)
}

// SetMiniGame sets mMiniGameType.
func (g *FakeGame) SetMiniGame(t uint8) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.st8(addr.MiniGameType, t)
}

// SetRefight sets mPlayerInfoBufferStageNo: 1-4 while a Ganon's Tower
// boss-refight snapshot is held (d_com_inf_game.cpp:1448-1457), 0 after.
func (g *FakeGame) SetRefight(stageNo uint8) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.st8(addr.PlayerInfoBufStageNo, stageNo)
}

// SetGBALink sets the g_mDoGaC_gbaCom fields mDoGaC_GbaLink reads
// (addr.GbaCom): linked as mDoGaC_Connect leaves a connected Tingle Tuner
// (mEnable 1 from the Tuner's upload, d_a_agb.cpp:491; connect state 3
// and the link open, m_Do_gba_com.cpp:221-224, :190; a context that
// checked out, :518; no failed probe, :236), or, unlinked, as
// mDoGaC_Initial and mDoGaC_GbaReboot leave it (:85-93, :124, :162-168).
func (g *FakeGame) SetGBALink(linked bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	f := [5]byte{0, 0, 1, 0, 0} // enable, state, closed, retries, context
	if linked {
		f = [5]byte{1, 3, 0, 0, 1}
	}
	g.st8(addr.GbaCom+addr.GbaComOffEnable, f[0])
	g.st8(addr.GbaCom+addr.GbaComOffState, f[1])
	g.st8(addr.GbaCom+addr.GbaComOffClosed, f[2])
	g.st8(addr.GbaCom+addr.GbaComOffRetries, f[3])
	g.st8(addr.GbaCom+addr.GbaComOffContext, f[4])
}

// Memory helpers. They panic on error, which only a bad address in the
// fake itself can cause.

func (g *FakeGame) load(a uint32, n int) []byte {
	b, err := g.m.Read(a, n)
	if err != nil {
		panic(err)
	}
	return b
}

func (g *FakeGame) store(a uint32, b []byte) {
	if err := g.m.Store(a, b); err != nil {
		panic(err)
	}
}

func (g *FakeGame) ld8(a uint32) uint8 { return g.load(a, 1)[0] }

func (g *FakeGame) ld16(a uint32) uint16 {
	b := g.load(a, 2)
	return uint16(b[0])<<8 | uint16(b[1])
}

func (g *FakeGame) ld32(a uint32) uint32 {
	b := g.load(a, 4)
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func (g *FakeGame) st8(a uint32, v uint8)   { g.store(a, []byte{v}) }
func (g *FakeGame) st16(a uint32, v uint16) { g.store(a, []byte{byte(v >> 8), byte(v)}) }
func (g *FakeGame) st32(a uint32, v uint32) {
	g.store(a, []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}
