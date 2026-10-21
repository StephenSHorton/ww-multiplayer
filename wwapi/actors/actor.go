package actors

import (
	"fmt"
	"math"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// Actor is one actor as a walk read it: the fopAc_ac_c fields
// (f_op_actor.h:270-319; base_process_class, f_pc_base.h:13-32) M6 and
// the actors diagnostic read, and, for a proc the walk has a Layout for
// (ReadWith), its enemyice, the class's mode and the process ID of the
// weapon it holds.
type Actor struct {
	Node, Addr uint32 // its actor_tag, and the actor (Node - 0xC4)
	PID        uint32 // mBsPcId
	Proc       int16  // mProcName
	InitState  int8   // mInitState: 2 while it executes
	// Pause is mPauseFlag (f_pc_base.h:18): the enemy mirror's REL sets
	// addr.ActorPauseMirrorBit on a copy it adopts, which co-op's K1
	// refuses (enemy-mirror-general.json D8).
	Pause     uint8
	SetID     uint16
	Group     uint8
	HomeRoom  int8
	HomePos   [3]uint32 // home.pos, the f32 bits as the entry had them
	HomeAngle [3]int16
	Pos       [3]float32 // current.pos
	Params    uint32
	Status    uint32 // actor_status
	ParentID  uint32 // parentActorID: 0xFFFFFFFF for a placed actor
	EvtCmd    uint16 // eventInfo.mCommand: 0 outside an event's order
	Health    int8
	MaxHealth int8
	Attn      uint32 // attention_info.flags

	// Ice is the enemyice at Layout.Ice; Ice.Off is 0 without one.
	Ice IceState
	// Mode is the class's mode at Layout.Mode, if ModeOK; for a BOKO in
	// co-op's layouts, its u8 mNowMode (addr.BokoOffNowMode).
	Mode   int32
	ModeOK bool
	// Weapon is the process ID in the carrier's weapon field
	// (Layout.Weapon), 0 for a class without one. It may name a weapon the
	// carrier no longer holds (co-op's combat.HeldBy).
	Weapon uint32
	// Hold is the carrier's own record that it holds its weapon
	// (Layout.Hold), sign-extended, if HoldOK.
	Hold   int32
	HoldOK bool
}

// IceState is an actor's enemyice (c_damagereaction.h:12-46), at Off.
type IceState struct {
	Off uint32
	// Read: the struct lies in MEM1 and was read.
	Read bool
	// Self is mpActor, which the class's create points at the actor
	// (for example d_a_bk.cpp:5270); SelfOK when it does.
	Self   uint32
	SelfOK bool
	// Timer is mLightShrinkTimer: nonzero runs the light-arrow death
	// (enemy_ice, c_damagereaction.cpp:117-171).
	Timer int8
	// Mode is mMode: 0 before enemy_ice's first call, 1 idle, 2-3 frozen
	// (c_damagereaction.cpp:178-260).
	Mode        int8
	FreezeTimer int16
}

// Layout says what a walk reads of a proc beyond fopAc_ac_c, each an
// offset from the actor, 0 for none: the enemyice (Ice), a mode (Mode, an
// s32, or a u8 if ModeU8), the process ID of the weapon it carries
// (Weapon) and its own record of holding that weapon (Hold, an s32, or an
// s8 if HoldS8). A part that lies outside MEM1 is left unread.
type Layout struct {
	Ice    uint32
	Mode   uint32
	ModeU8 bool
	Weapon uint32
	Hold   uint32
	HoldS8 bool
}

// Layouts maps a proc (mProcName) to its Layout.
type Layouts map[int16]Layout

// Names, if not nil, gives the short name String prints for a proc ("" for
// none). It is optional: without it String prints no name. Co-op's combat
// package sets it, when it is initialised, to its ProcName. Set it only
// before any walk.
var Names func(proc int16) string

// TimerAddr is the address of a's light-shrink timer, 0 without an
// enemyice.
func (a *Actor) TimerAddr() uint32 {
	if a.Ice.Off == 0 {
		return 0
	}
	return a.Addr + a.Ice.Off + addr.EnemyIceOffTimer
}

// Boss reports whether a has BOSS status (fopAcStts_BOSS_e).
func (a *Actor) Boss() bool { return a.Status&addr.ActorStatusBoss != 0 }

// Carried reports whether a has CARRY status (fopAcStts_CARRY_e): a
// weapon held, or an actor Link carries.
func (a *Actor) Carried() bool { return a.Status&addr.ActorStatusCarry != 0 }

// Keyed reports whether the game records a's death in a zone: a setID
// below 0x200 and a home room of 0..63 (dSv_info_c::onActor,
// d_save.cpp:1671-1682).
func (a *Actor) Keyed() bool {
	return a.SetID < addr.ZoneActorCount && a.HomeRoom >= 0 && int(a.HomeRoom) < addr.RoomCount
}

func (a *Actor) String() string {
	var name string
	if Names != nil {
		name = Names(a.Proc)
	}
	return fmt.Sprintf("actor 0x%08X (pid %d, proc 0x%04X %s, setID %s, room %d)", a.Addr, a.PID, uint16(a.Proc), name, setIDName(a.SetID), a.HomeRoom)
}

func setIDName(id uint16) string {
	if id == addr.ActorSetIDNone {
		return "none"
	}
	return fmt.Sprintf("%d", id)
}

// decode reads the fopAc_ac_c fields of b, the actor whose tag is node.
func decode(node uint32, b []byte) Actor {
	a := Actor{
		Node:      node,
		Addr:      node - addr.ActorOffActorTag,
		PID:       be32(b[addr.ActorOffBsPcID:]),
		Proc:      int16(be16(b[addr.ActorOffProcName:])),
		InitState: int8(b[addr.ActorOffInitState]),
		Pause:     b[addr.ActorOffPauseFlag],
		SetID:     be16(b[addr.ActorOffSetID:]),
		Group:     b[addr.ActorOffGroup],
		HomeRoom:  int8(b[addr.ActorOffHomeRoomNo]),
		Params:    be32(b[addr.ActorOffParameters:]),
		Status:    be32(b[addr.ActorOffStatus:]),
		ParentID:  be32(b[addr.ActorOffParentID:]),
		EvtCmd:    be16(b[addr.ActorOffEventCommand:]),
		Health:    int8(b[addr.ActorOffHealth]),
		MaxHealth: int8(b[addr.ActorOffMaxHealth]),
		Attn:      be32(b[addr.ActorOffAttnFlags:]),
	}
	for i := range 3 {
		a.HomePos[i] = be32(b[addr.ActorOffHomePos+4*i:])
		a.HomeAngle[i] = int16(be16(b[addr.ActorOffHomeAngle+2*i:]))
		a.Pos[i] = math.Float32frombits(be32(b[addr.ActorOffCurPos+4*i:]))
	}
	return a
}

// readLayout reads what l says a has beyond fopAc_ac_c: its enemyice, its
// mode, its weapon field and its record of holding the weapon. A part that
// lies outside MEM1 is left unread (Ice.Read false, ModeOK false, Weapon 0,
// HoldOK false); the error is a read error.
func readLayout(m mem.Reader, a *Actor, l Layout) error {
	if l.Ice != 0 {
		a.Ice.Off = l.Ice
		if at := a.Addr + l.Ice; mem.CheckRange(at, addr.EnemyIceSize) == nil {
			b, err := m.Read(at, addr.EnemyIceSize)
			if err != nil {
				return err
			}
			a.Ice.Read = true
			a.Ice.Self = be32(b[addr.EnemyIceOffSelf:])
			a.Ice.SelfOK = a.Ice.Self == a.Addr
			a.Ice.Timer = int8(b[addr.EnemyIceOffTimer])
			a.Ice.Mode = int8(b[addr.EnemyIceOffMode])
			a.Ice.FreezeTimer = int16(be16(b[addr.EnemyIceOffFreezeTimer:]))
		}
	}
	if l.Mode != 0 {
		read := readU32In
		if l.ModeU8 {
			read = readU8In
		}
		if v, err := read(m, a.Addr+l.Mode); err != nil {
			return err
		} else if v.ok {
			a.Mode, a.ModeOK = int32(v.v), true
		}
	}
	if l.Weapon != 0 {
		v, err := readU32In(m, a.Addr+l.Weapon)
		if err != nil {
			return err
		}
		a.Weapon = v.v
	}
	if l.Hold != 0 {
		read := readU32In
		if l.HoldS8 {
			read = readU8In
		}
		v, err := read(m, a.Addr+l.Hold)
		if err != nil {
			return err
		}
		a.Hold, a.HoldOK = int32(v.v), v.ok
		if l.HoldS8 {
			a.Hold = int32(int8(v.v))
		}
	}
	return nil
}

type u32In struct {
	v  uint32
	ok bool
}

// readU32In reads the u32 at at if it lies in MEM1.
func readU32In(m mem.Reader, at uint32) (u32In, error) {
	if mem.CheckRange(at, 4) != nil {
		return u32In{}, nil
	}
	v, err := mem.ReadU32(m, at)
	return u32In{v, err == nil}, err
}

// readU8In reads the u8 at at if it lies in MEM1.
func readU8In(m mem.Reader, at uint32) (u32In, error) {
	if mem.CheckRange(at, 1) != nil {
		return u32In{}, nil
	}
	v, err := mem.ReadU8(m, at)
	return u32In{uint32(v), err == nil}, err
}
