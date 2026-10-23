package switches

import (
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// The live-actor adapter (design/stale-actor-refresh.md G2): the decoders
// above read a placed entry's params and home.angle, and a live actor
// keeps both as the entry gave them (fopAcM_GetParam, home.angle:
// dStage_actorCreate, d_stage.cpp:1300-1310), so the same decode names
// the bits a live actor reads and writes. Co-op's stale-actor refresh
// asks it which switch is a key door's lock (RoleKeyLock) and which is a
// K1 enemy's death switch (RoleDeath).

// Role is what a switch is to the actor that uses it, where a live
// refresh acts on it. Most uses have none.
type Role uint8

const (
	RoleNone Role = iota
	// RoleKeyLock is a key door's lock switch: keyInit sets it as a key
	// opens the lock, if it is below 0x80 (d_door.cpp:433-447), and
	// setKey reads it only from the door's actionInit (daDoor10_c::setKey,
	// d_a_door10.cpp:28-50; daDoor12_c::setKey, d_a_door12.cpp:52-73).
	// It is routed with room -1 (doorUses).
	RoleKeyLock
	// RoleDeath is a BK, MO2, ST or TN death switch, home.angle.z's low
	// byte: set when killed (enemy_ice, c_damagereaction.cpp:165-166),
	// and read only by create (d_a_bk.cpp:4771-4780; deathSwitch).
	RoleDeath
)

func (r Role) String() string {
	switch r {
	case RoleKeyLock:
		return "key lock"
	case RoleDeath:
		return "death switch"
	}
	return "none"
}

// LiveUse is one switch a live actor uses, as its proc's decoder reads
// its params and home.angle.
type LiveUse struct {
	No int
	// Room is the room the game routes it with: the actor's home room
	// unless the decoder routes it otherwise (-1 for a key door's lock).
	// isSwitch reads it only for a zone or room switch (d_save.cpp:
	// 1586-1604).
	Room  int
	Store addr.SwitchStore
	Write bool
	Op    Op
	Class Class
	Role  Role
	Why   string
}

// LiveUses decodes the switches a live actor of proc uses, from its
// params, home.angle and home room (mParameters, home.angle and
// home.roomNo: addr.ActorOffParameters, ActorOffHomeAngle and
// ActorOffHomeRoomNo; actors.Actor's Params, HomeAngle and HomeRoom). ok
// is false for a proc no decoder reads, or one whose params are not
// decoded. Uses of switch 0xFF (and -1) are dropped, as Build drops them:
// the game's setters return early for them (d_save.cpp:1544-1546); so is
// a number the game would assert on.
func LiveUses(proc stage.Proc, params uint32, angle [3]int16, room int) ([]LiveUse, bool) {
	sp, ok := specByProc[proc]
	if !ok || sp.decode == nil {
		return nil, false
	}
	e := entry{stage.Actor{Params: params, Angle: angle}}
	var out []LiveUse
	for _, u := range sp.decode(e) {
		st := addr.ClassifySwitch(u.no)
		if st == addr.SwitchNone || st == addr.SwitchInvalid {
			continue
		}
		r := room
		if u.roomSet {
			r = u.room
		}
		out = append(out, LiveUse{No: u.no, Room: r, Store: st, Write: u.write, Op: u.op, Class: u.class, Role: u.role, Why: u.why})
	}
	return out, true
}

// LiveRole returns the switch of role r a live actor of proc uses, by
// LiveUses, and false if it has none.
func LiveRole(proc stage.Proc, params uint32, angle [3]int16, room int, r Role) (LiveUse, bool) {
	us, _ := LiveUses(proc, params, angle, room)
	for _, u := range us {
		if u.Role == r {
			return u, true
		}
	}
	return LiveUse{}, false
}
