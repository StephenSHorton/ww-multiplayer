package fakegame

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
)

// The room puzzles M5's receiver writes into (§6.3, §9 M5): the switch
// reads the game's actors poll, the zone table a zone switch is found
// through, a zone moving to another slot, and two placed actors, one that
// polls a switch and one that writes its switch every frame. Tests use
// them to show a partner's switch opening the receiver's door, and that a
// switch the receiver's own game clears is not set again.

// IsSwitch is dSv_info_c::isSwitch (d_save.cpp:1586-1603), which an
// actor's execute calls through fopAcM_isSwitch with its room
// (f_op_actor_mng.h:374-376): switches 0x00-0x7F read the live memBit,
// 0x80-0xBF the dan bits and 0xC0-0xEF the zone of room's
// mStatus.mZoneNo. -1 and 255 are false. Anything else, a room outside
// 0..63 for a zone switch, or a room with no zone, is ErrAssert: the
// store's own isSwitch asserts the switch number (d_save.cpp:1096-1098,
// 1250-1252, 1291-1293) and the zone lookup the room and zone
// (d_save.cpp:1596-1599).
func (g *FakeGame) IsSwitch(no, room int) (bool, error) {
	store, bit, err := addr.SwitchBit(no)
	switch store {
	case addr.SwitchNone:
		return false, nil
	case addr.SwitchInvalid:
		return false, assertf("switch %d: %v", no, err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	base := addr.LiveMemBit
	switch store {
	case addr.SwitchDan:
		base = addr.Dan
	case addr.SwitchZone, addr.SwitchRoom:
		z, err := g.roomZone(room)
		if err != nil {
			return false, fmt.Errorf("switch 0x%X: %w", no, err)
		}
		base = zoneAddr(z)
	}
	return g.ld8(base+uint32(bit.Off))&bit.Mask != 0, nil
}

// StayNo is the room Link is in, dStage_roomControl_c::mStayNo
// (d_stage.h:904): -1 before a stage's first room loads.
func (g *FakeGame) StayNo() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return int(int8(g.ld8(addr.StayNo)))
}

// ZoneOf is room's zone slot, mStatus[room].mZoneNo, the slot every zone
// switch of the room is found through (getZoneNo, d_stage.h:901;
// d_save.cpp:1552-1556): -1 if the room has none.
func (g *FakeGame) ZoneOf(room int) (int, error) {
	if room < 0 || room >= addr.RoomCount {
		return 0, fmt.Errorf("fakegame: room %d out of range 0..%d", room, addr.RoomCount-1)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return int(g.zoneNo(room)), nil
}

// ZoneRoom is zone slot z's mRoomNo (dSv_zone_c::getRoomNo, d_save.h:792):
// the room it was created for, or -1 once removeZone freed it
// (d_save.h:969, :793), when its old bits still sit in it.
func (g *FakeGame) ZoneRoom(z int) (int, error) {
	if _, err := addr.ZoneAddr(z); err != nil {
		return 0, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return int(int8(g.ld8(zoneAddr(z) + addr.ZoneOffRoomNo))), nil
}

// MoveZone frees room's zone and creates it again, after a zone for room
// other, which has none, took the first free slot: the zone's bits are
// gone (dSv_zone_c::init, d_save.cpp:1338-1342) and room's zone is in
// whichever slot createZone found next (first-free, d_save.cpp:1525-1537),
// with every mStatus.mZoneNo and mRoomNo as the game leaves them. It
// returns the slot room had and the one it has now, which differ when
// room's was the first free slot. mStayNo does not move.
//
// It is a transition storm in one call. On the console it takes room
// changes (zoneCountCheck removes a zone two changes after its room was
// last loaded, d_stage.cpp:250-267; a room scene being created makes a
// zone, d_s_room.cpp:193-197), or a save state loaded between two reads of
// the tool's.
func (g *FakeGame) MoveZone(room, other int) (from, to int, err error) {
	for _, r := range []int{room, other} {
		if r < 0 || r >= addr.RoomCount {
			return 0, 0, fmt.Errorf("fakegame: room %d out of range 0..%d", r, addr.RoomCount-1)
		}
	}
	if room == other {
		return 0, 0, errors.New("fakegame: MoveZone needs another room to take the slot")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.playing {
		return 0, 0, errors.New("fakegame: no stage loaded")
	}
	from = int(g.zoneNo(room))
	if from < 0 {
		return 0, 0, fmt.Errorf("fakegame: room %d has no zone", room)
	}
	if g.zoneNo(other) >= 0 {
		return 0, 0, fmt.Errorf("fakegame: room %d already has a zone", other)
	}
	c := g.zoneCount(room)
	g.removeZone(from)
	g.setZoneNo(room, -1)
	zo := g.createZone(other)
	g.setZoneNo(other, zo)
	g.setZoneCount(other, 2)
	to = g.createZone(room)
	g.setZoneNo(room, to)
	g.setZoneCount(room, int(c))
	if to < 0 {
		return from, to, fmt.Errorf("fakegame: no free zone slot for room %d", room)
	}
	return from, to, nil
}

// curStage is the loaded stage's name, mCurStage.mName.
func (g *FakeGame) curStage() string {
	name, _, _ := strings.Cut(string(g.load(addr.CurStage, addr.StageNameLen)), "\x00")
	return name
}

// Actor is a placed actor of a stage room the fake runs each frame
// (FrameWith, RunWith): its execute, which fpcM_Management runs before
// frame_shim's heartbeat (f_ap_game.cpp:77-81). An actor exists only
// while its stage is loaded, its room has a zone and is not hidden, as a
// room's actors are created with its room scene (d_s_room.cpp:193-197)
// and deleted while the room is hidden (HideRoom); otherwise it does
// nothing.
type Actor interface {
	execute(g *FakeGame) error
}

// here reports whether an actor of room room of stage stage exists now:
// the room has a zone and is not hidden.
func (g *FakeGame) here(stage string, room int) bool {
	_, ok := g.roomScene(stage, room)
	return ok
}

// roomScene is here, with the process ID of the room's scene, which a new
// scene of the room changes (ReloadRoom).
func (g *FakeGame) roomScene(stage string, room int) (uint32, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.playing || g.curStage() != stage || room < 0 || room >= addr.RoomCount || g.zoneNo(room) < 0 {
		return 0, false
	}
	a, _ := addr.RoomStatusAddr(room)
	if g.ld8(a+roomStatusOffFlags)&roomHidden != 0 {
		return 0, false
	}
	return g.ld32(a + roomStatusOffProcID), true
}

// Fields of dStage_roomStatus_c (d_stage.h:855-869) a room's actors rest
// on, and its status flag "hidden" (checkStatusFlag(room, 0x08)).
const (
	roomStatusOffFlags  = 0x104 // u8 mFlags
	roomStatusOffProcID = 0x10C // fpc_ProcID mProcID, the room scene's
	roomHidden          = 0x08
)

// HideRoom is a door closing behind Link: closeEndCom hides the room he
// came from (onStatusFlag(mFromRoomNo, 0x08), d_door.cpp:305), and the
// room scene's next execute deletes its actors and clears its zone's room
// switches (objectSetCheck, d_s_room.cpp:79-83), which the fake does at
// once. The zone stays.
func (g *FakeGame) HideRoom(room int) error {
	return g.roomFlag(room, func(a uint32) {
		g.st8(a+roomStatusOffFlags, g.ld8(a+roomStatusOffFlags)|roomHidden)
		if z := g.zoneNo(room); z >= 0 {
			g.clearRoomSwitch(int(z))
		}
	})
}

// ShowRoom is a door opening onto room: offStatusFlag(mToRoomNo, 0x08)
// (d_door.cpp:282), after which the room scene's next execute creates its
// actors again (objectSetCheck, d_s_room.cpp:72-76): an actor of the room
// is created on its next frame.
func (g *FakeGame) ShowRoom(room int) error {
	return g.roomFlag(room, func(a uint32) {
		g.st8(a+roomStatusOffFlags, g.ld8(a+roomStatusOffFlags)&^roomHidden)
	})
}

// ReloadRoom is room's scene deleted and created again, as a room the
// game unloaded and loads once more: the delete clears its status flags
// (dScnRoom_Delete, d_s_room.cpp:120) and the new scene's phase_0 writes
// its process ID into mProcID (d_s_room.cpp:150-152). Its actors are
// created anew. The zone stays.
func (g *FakeGame) ReloadRoom(room int) error {
	return g.roomFlag(room, func(a uint32) {
		g.st8(a+roomStatusOffFlags, 0)
		g.st32(a+roomStatusOffProcID, g.ld32(a+roomStatusOffProcID)+1)
	})
}

// roomFlag runs f with room's mStatus address and the lock held.
func (g *FakeGame) roomFlag(room int, f func(a uint32)) error {
	a, err := addr.RoomStatusAddr(room)
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.playing {
		return errors.New("fakegame: no stage loaded")
	}
	f(a)
	return nil
}

// actorsMove reports whether an actor with no event part executes this
// frame: no event runs (dEvt_control_c::moveApproval gives NOMOVE,
// d_event.cpp:626-670; fopAc_Execute, f_op_actor.cpp:178-200) and the
// pause menu is not up (f_op_actor.cpp:175).
func (g *FakeGame) actorsMove() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.ld8(addr.EvtMode) == 0 && g.ld8(addr.MenuPause) == 0
}

// FrameWith runs actors' execute, in order, then a Frame. An actor's
// ErrAssert halts the game, as on the console: it is returned and the
// frame is not run.
func (g *FakeGame) FrameWith(actors ...Actor) error {
	for _, a := range actors {
		if err := a.execute(g); err != nil {
			return err
		}
	}
	g.Frame()
	return nil
}

// RunWith is Run with actors: a FrameWith every d until ctx ends (nil) or
// an actor asserts (its error).
func (g *FakeGame) RunWith(ctx context.Context, d time.Duration, actors ...Actor) error {
	if d <= 0 {
		d = time.Millisecond
	}
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := g.FrameWith(actors...); err != nil {
				return err
			}
		}
	}
}

// Poller is an actor that waits on switch No of its room, a door or gate
// that opens once it is set: its execute polls fopAcM_isSwitch every frame
// (f_op_actor_mng.h:374-376; as SWC00's enable switch is polled,
// d_a_swc00.cpp:14-15, and an AND switch's inputs, d_a_andsw0.cpp:42) and,
// once it sees the switch, stays open whatever the switch does after.
type Poller struct {
	Stage string
	Room  int
	No    int

	mu     sync.Mutex
	opened uint64 // the frame it opened on, from 1; 0 while shut
}

func (p *Poller) execute(g *FakeGame) error {
	if !g.here(p.Stage, p.Room) {
		return nil
	}
	on, err := g.IsSwitch(p.No, p.Room)
	if err != nil || !on {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.opened == 0 {
		p.opened = g.FrameCount() + 1
	}
	return nil
}

// Opened reports whether p has opened, and on which frame (counted from 1
// as FrameCount counts them).
func (p *Poller) Opened() (bool, uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.opened != 0, p.opened
}

// Swc00 is SWC00, an area switch: while its enable switch Enable is set
// (or 0xFF), its execute sets switch No when Link stands in its area and,
// for type 0, clears it again every frame he does not; another type sets
// it once and deletes itself (daSwc00_Execute, d_a_swc00.cpp:12-32). Type
// 0 is a writer that does not latch, which co-op must never sync (§6.2):
// whatever a partner's write sets, it clears on the next frame Link is
// away.
type Swc00 struct {
	Stage  string
	Room   int
	No     int
	Enable int // 0xFF: always enabled
	Type   int

	mu      sync.Mutex
	in      bool
	deleted bool
}

// SetIn puts Link in s's area, or takes him out of it.
func (s *Swc00) SetIn(in bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.in = in
}

func (s *Swc00) execute(g *FakeGame) error {
	s.mu.Lock()
	in, deleted := s.in, s.deleted
	s.mu.Unlock()
	if deleted || !g.here(s.Stage, s.Room) {
		return nil
	}
	if s.Enable != 0xFF {
		on, err := g.IsSwitch(s.Enable, s.Room)
		if err != nil || !on {
			return err
		}
	}
	switch {
	case in:
		if err := g.OnSwitch(s.No, s.Room); err != nil {
			return err
		}
		if s.Type != 0 {
			s.mu.Lock()
			s.deleted = true // fopAcM_delete, d_a_swc00.cpp:23-25
			s.mu.Unlock()
		}
	case s.Type == 0:
		return g.OffSwitch(s.No, s.Room)
	}
	return nil
}

// ShutterFrames is the frame timer a shutter's Create sets
// (mFrameTimer = 30, d_a_shutter.cpp:87): a switch that changes before
// its 31st execute is not seen (Shutter).
const ShutterFrames = 30

// Shutter is daShutter_c, the shutter of a dungeon room (Htobi1, Htobi2),
// shut until switch No of its room is set. Its Create, as its room's
// actors are created, reads the switch to stand open or shut and sets a
// 30-frame timer (d_a_shutter.cpp:71-87). Each execute counts the timer
// down, starts the opening if the switch reads otherwise than mbIsSwitch
// once the timer has run out (demo, d_a_shutter.cpp:233-243), and copies
// the switch into mbIsSwitch (_execute, d_a_shutter.cpp:133-142). So a
// switch set within its first 30 executes is never seen: the shutter
// stays shut, the switch set, until its room's actors are created again.
// It does not execute while an event runs or the pause menu is up
// (actorsMove). The fake opens it as the opening starts (the real one
// plays its open event, m_open_ev_name).
type Shutter struct {
	Stage string
	Room  int
	No    int

	mu       sync.Mutex
	live     bool
	proc     uint32 // the room scene it was created in
	timer    int    // mFrameTimer
	isSwitch bool   // mbIsSwitch
	open     bool
	opened   uint64 // the frame its opening started on, from 1; 0 if none did
	execs    int    // executes since it was created
}

func (s *Shutter) execute(g *FakeGame) error {
	proc, ok := g.roomScene(s.Stage, s.Room)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !ok {
		s.live = false // deleted with its room's actors
		return nil
	}
	if !s.live || s.proc != proc {
		on, err := g.IsSwitch(s.No, s.Room)
		if err != nil {
			return err
		}
		s.live, s.proc, s.timer, s.open, s.opened, s.execs = true, proc, ShutterFrames, on, 0, 0
		return nil
	}
	if !g.actorsMove() {
		return nil
	}
	s.execs++
	if s.timer >= 0 {
		s.timer--
	}
	on, err := g.IsSwitch(s.No, s.Room)
	if err != nil {
		return err
	}
	if on != s.isSwitch && s.timer < 0 && on != s.open {
		s.open = on
		if on {
			s.opened = g.FrameCount() + 1
		}
	}
	s.isSwitch = on
	return nil
}

// Open reports whether s stands open now.
func (s *Shutter) Open() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.open
}

// Opened reports whether s's opening started since it was last created,
// and on which frame (counted from 1 as FrameCount counts them).
func (s *Shutter) Opened() (bool, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opened != 0, s.opened
}

// Executes is how many times s executed since it was last created.
func (s *Shutter) Executes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.execs
}

// RoomAges is an actor that keeps, for each room of the loaded stage, the
// executes the room's actors have had since they were created, as a
// Shutter of the room counts its own (Executes): they exist while the room
// has a zone and is not hidden, are created anew with a new room scene
// (ReloadRoom), and execute on a frame with no event and no pause menu
// (actorsMove). Tests run it every frame to check that a switch reached
// every room's actors only after their first ShutterFrames executes, when
// a shutter sees it change (§6.1).
type RoomAges struct {
	mu    sync.Mutex
	stage string
	live  [addr.RoomCount]bool
	proc  [addr.RoomCount]uint32
	execs [addr.RoomCount]int
}

func (a *RoomAges) execute(g *FakeGame) error {
	g.mu.Lock()
	stage := g.curStage()
	g.mu.Unlock()
	moves := g.actorsMove()
	a.mu.Lock()
	defer a.mu.Unlock()
	if stage != a.stage {
		a.stage, a.live = stage, [addr.RoomCount]bool{}
	}
	for room := range addr.RoomCount {
		proc, ok := g.roomScene(stage, room)
		switch {
		case !ok:
			a.live[room] = false // deleted with its room's actors
		case !a.live[room] || a.proc[room] != proc:
			a.live[room], a.proc[room], a.execs[room] = true, proc, 0
		case moves:
			a.execs[room]++
		}
	}
	return nil
}

// Youngest returns the room whose actors exist and have had the fewest
// executes since they were created, and that count; room is -1 while no
// room's actors exist.
func (a *RoomAges) Youngest() (room, execs int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	room = -1
	for r := range addr.RoomCount {
		if a.live[r] && (room < 0 || a.execs[r] < execs) {
			room, execs = r, a.execs[r]
		}
	}
	return room, execs
}
