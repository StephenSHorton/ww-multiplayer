package replica

import (
	"encoding/binary"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/gate"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/snap"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// The room hold (§6.3, M5): a partner's dan, zone or room switch, and a
// memory switch of the loaded table M3's reconciler writes, is written
// only once the actors that read it have run long enough to see it
// change.
//
// daShutter_c, the shutter of a dungeon room (Htobi1, Htobi2), reads its
// switch in Create to stand open or shut and sets mFrameTimer to 30
// (d_a_shutter.cpp:71-87). Each execute counts the timer down and then
// copies the switch into mbIsSwitch (d_a_shutter.cpp:133-142), and its
// demo starts the opening only when the switch differs from mbIsSwitch
// once the timer has run out (d_a_shutter.cpp:233-243). So a switch set
// within its first 30 executes is swallowed: the shutter stays shut while
// the switch reads set, until its room is loaded again, and a latched
// switch is not set again. It does not execute while an event runs
// (dEvt_control_c::moveApproval gives it NOMOVE, d_event.cpp:626-670;
// fopAc_Execute, f_op_actor.cpp:178-200) or the pause menu is up
// (f_op_actor.cpp:175).
//
// So the receiver keeps a clock per room, of the frames its actors have
// had to run:
//
//   - a room's actors are created with its room scene, in its phase_3
//     (objectSetCheck, d_s_room.cpp:72-76, :255), unless the room is
//     hidden (its status flag 0x08: loadRoom loads a room without
//     collision hidden, d_stage.cpp:239). A door shows the room behind it
//     as it opens (d_door.cpp:282), and the scene's next execute creates
//     the actors (d_s_room.cpp:89-101); it hides the room left as it
//     closes (d_door.cpp:305), which deletes them (d_s_room.cpp:79-83). A
//     scene deleted clears the flags (d_s_room.cpp:120), and a new one
//     takes a new process ID into mStatus[room].mProcID (phase_0,
//     d_s_room.cpp:150-152);
//   - so a room's clock starts over whenever its zone slot, its scene's
//     mProcID or its status flags 0x01-0x08 change, and every clock at a
//     change of stage or save table, and counts only the heartbeat's
//     frames between two IDLE ticks (a hidden room's not at all);
//   - a zone or room switch is written once the clock of the room Link is
//     in reaches roomSettle, and a dan switch, which every loaded room's
//     actors read, once every shown room's has (calm). So is a memory
//     switch of the loaded table (reconcile.go), which every loaded
//     room's actors read too.
//
// The clocks are read apart from the snapshot (readRoomProbe), so a
// change they miss for a tick is seen on the next: a clock only ever
// starts over late, never runs early.

const (
	// roomSettle is the IDLE frames a room's actors are given before a
	// partner's switch is written for them: two seconds at the game's 30
	// frames. The shutter needs 31; the rest is for its own create, which
	// waits for its archive (dComIfG_resLoad, d_a_shutter.cpp:103-106)
	// after the room scene asked for it (inferred).
	roomSettle = 60
	// maxRoomBeat is the most frames one tick may add to the clocks: a
	// tick's worth at maxBeatRate. A longer gap between two ticks may hide
	// an event, so it adds nothing.
	maxRoomBeat = uint32(maxBeatRate * TickEvery / time.Second)

	// Fields of dStage_roomStatus_c (d_stage.h:855-869) the clocks follow,
	// and mOldStayNo, the room before mStayNo (sym:18668; setStayNo,
	// d_stage.cpp:171-173).
	roomStatusOffFlags  = 0x104 // u8 mFlags
	roomStatusOffProcID = 0x10C // fpc_ProcID mProcID: the room scene's
	oldStayNo           = addr.StayNo + 1

	// roomFlagsSeen are the status flags whose change starts a room's
	// clock over: 0x01 loaded, 0x02 being created, 0x04 being deleted
	// (loadRoom, d_stage.cpp:214-240; dScnRoom_Execute,
	// d_s_room.cpp:89-101) and 0x08 hidden. 0x10 is the room's collision
	// (d_a_bg.cpp:254, :441), which says nothing of its actors.
	roomFlagsSeen = 0x0F
	roomHidden    = 0x08
)

// roomProbe is what a tick reads of dStage_roomControl_c besides the
// snapshot: mStayNo and mOldStayNo, and each room's status flags and room
// scene.
type roomProbe struct {
	ok            bool
	stay, oldStay int8
	flags         [addr.RoomCount]uint8
	proc          [addr.RoomCount]uint32
}

// readRoomProbe reads the probe: two bytes at mStayNo and the whole
// mStatus array, one read each. A failed read gives a probe that is not
// ok.
func readRoomProbe(m mem.Mem) roomProbe {
	var p roomProbe
	b, err := m.Read(addr.StayNo, 2)
	if err != nil {
		return p
	}
	st, err := m.Read(addr.RoomStatus, addr.RoomStatusSize)
	if err != nil {
		return p
	}
	p.ok, p.stay, p.oldStay = true, int8(b[0]), int8(b[1])
	for room := range addr.RoomCount {
		at := room * addr.RoomStatusStride
		p.flags[room] = st[at+roomStatusOffFlags]
		p.proc[room] = binary.BigEndian.Uint32(st[at+roomStatusOffProcID:])
	}
	return p
}

// roomKey is what a room's actors rest on: its zone slot, its status
// flags and its room scene.
type roomKey struct {
	slot  int8
	flags uint8
	proc  uint32
}

// roomClock is a room's clock.
type roomClock struct {
	on     bool // the room has a zone (snap.Snapshot.ZoneFor)
	key    roomKey
	frames int // IDLE frames since key was first seen, up to roomSettle
}

func (r *roomClock) hidden() bool { return r.key.flags&roomHidden != 0 }

// m5Rooms are the rooms' clocks.
type m5Rooms struct {
	have  bool       // the fields below are the last consistent tick's
	stage wire.Stage // its stage and save table
	table int8
	hb    uint32 // its heartbeat
	idle  bool   // it was IDLE
	room  [addr.RoomCount]roomClock
}

// reset stops every clock: the next consistent tick starts them over.
func (rs *m5Rooms) reset() { *rs = m5Rooms{} }

// observe follows a consistent tick's rooms: s and st are its snapshot and
// gates, stage its stage, p its probe.
func (rs *m5Rooms) observe(s *snap.Snapshot, stage wire.Stage, st gate.Status, p *roomProbe) {
	if !p.ok || st.Regressed || rs.have && (stage != rs.stage || s.Table != rs.table) {
		rs.reset()
	}
	var d int
	if n := s.Heartbeat - rs.hb; rs.have && rs.idle && st.Idle && n > 0 && n <= maxRoomBeat {
		d = int(n)
	}
	for room := range addr.RoomCount {
		r := &rs.room[room]
		z, ok := s.ZoneFor(room)
		if !ok {
			*r = roomClock{}
			continue
		}
		k := roomKey{slot: int8(z), flags: p.flags[room] & roomFlagsSeen, proc: p.proc[room]}
		switch {
		case !r.on || r.key != k:
			*r = roomClock{on: true, key: k}
		case !r.hidden():
			r.frames = min(r.frames+d, roomSettle)
		}
	}
	rs.have, rs.stage, rs.table, rs.hb, rs.idle = p.ok, stage, s.Table, s.Heartbeat, st.Idle
}

// settled reports whether room's actors have had roomSettle IDLE frames:
// it has a zone and is shown, and its clock has run.
func (rs *m5Rooms) settled(room int) bool {
	if room < 0 || room >= addr.RoomCount {
		return false
	}
	r := &rs.room[room]
	return r.on && !r.hidden() && r.frames >= roomSettle
}

// young returns a shown room whose clock has not run, or -1: a dan
// switch waits for every one.
func (rs *m5Rooms) young() int {
	for room := range rs.room {
		if r := &rs.room[room]; r.on && !r.hidden() && r.frames < roomSettle {
			return room
		}
	}
	return -1
}

// calm reports whether every shown room's clock has run, by a probe this
// tick read: what a switch that every loaded room's actors read waits
// for. That is a dan switch, and M3's memory switches of the loaded table
// (0x00-0x7F, the live memBit: dSv_info_c::isSwitch, d_save.cpp:1586-1592),
// which a shutter reads as it reads a dan or zone switch
// (fopAcM_isSwitch, f_op_actor_mng.h:374-376).
func (rs *m5Rooms) calm() bool { return rs.have && rs.young() < 0 }
