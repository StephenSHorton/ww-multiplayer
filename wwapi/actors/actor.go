package actors

import (
	"fmt"
	"math"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// Actor is one actor as a walk read it: the fopAc_ac_c fields
// (f_op_actor.h:270-319; base_process_class, f_pc_base.h:13-32) M6 and
// the actors diagnostic read, and, for a K1 class (K1), its enemyice, the
// class's mode the gates read and the process ID of the weapon it holds.
type Actor struct {
	Node, Addr uint32 // its actor_tag, and the actor (Node - 0xC4)
	PID        uint32 // mBsPcId
	Proc       int16  // mProcName
	InitState  int8   // mInitState: 2 while it executes
	SetID      uint16
	Group      uint8
	HomeRoom   int8
	HomePos    [3]uint32 // home.pos, the f32 bits as the entry had them
	HomeAngle  [3]int16
	Pos        [3]float32 // current.pos
	Params     uint32
	Status     uint32 // actor_status
	ParentID   uint32 // parentActorID: 0xFFFFFFFF for a placed actor
	EvtCmd     uint16 // eventInfo.mCommand: 0 outside an event's order
	Health     int8
	MaxHealth  int8
	Attn       uint32 // attention_info.flags

	// Ice is the enemyice of a K1 class; Ice.Off is 0 for any other.
	Ice IceState
	// Mode is the class's mode a gate reads (Class.Mode), if ModeOK; for
	// a BOKO, its u8 mNowMode (addr.BokoOffNowMode).
	Mode   int32
	ModeOK bool
	// Weapon is the process ID in the carrier's weapon field
	// (Class.Weapon), 0 for a class without one. It may name a weapon the
	// carrier no longer holds (HeldBy).
	Weapon uint32
	// Hold is the carrier's own record that it holds its weapon
	// (Class.Hold), sign-extended, if HoldOK.
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
	return fmt.Sprintf("actor 0x%08X (pid %d, proc 0x%04X %s, setID %s, room %d)", a.Addr, a.PID, uint16(a.Proc), ProcName(a.Proc), setIDName(a.SetID), a.HomeRoom)
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

// readClass reads what a K1 class has beyond fopAc_ac_c: its enemyice,
// its gated mode, its weapon field and its record of holding the weapon;
// and a BOKO's mNowMode. A part that lies outside MEM1 is left unread
// (Ice.Read false, ModeOK false, Weapon 0, HoldOK false); the error is a
// read error.
func readClass(m mem.Mem, a *Actor) error {
	if a.Proc == addr.ProcBoko {
		v, err := readU8In(m, a.Addr+addr.BokoOffNowMode)
		a.Mode, a.ModeOK = int32(v.v), v.ok
		return err
	}
	c, ok := classes[a.Proc]
	if !ok {
		return nil
	}
	a.Ice.Off = c.Ice
	if at := a.Addr + c.Ice; mem.CheckRange(at, addr.EnemyIceSize) == nil {
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
	if c.Mode != 0 {
		if v, err := readU32In(m, a.Addr+c.Mode); err != nil {
			return err
		} else if v.ok {
			a.Mode, a.ModeOK = int32(v.v), true
		}
	}
	if c.Weapon != 0 {
		v, err := readU32In(m, a.Addr+c.Weapon)
		if err != nil {
			return err
		}
		a.Weapon = v.v
	}
	if c.Hold != 0 {
		read := readU32In
		if c.holdS8 {
			read = readU8In
		}
		v, err := read(m, a.Addr+c.Hold)
		if err != nil {
			return err
		}
		a.Hold, a.HoldOK = int32(v.v), v.ok
		if c.holdS8 {
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
func readU32In(m mem.Mem, at uint32) (u32In, error) {
	if mem.CheckRange(at, 4) != nil {
		return u32In{}, nil
	}
	v, err := mem.ReadU32(m, at)
	return u32In{v, err == nil}, err
}

// readU8In reads the u8 at at if it lies in MEM1.
func readU8In(m mem.Mem, at uint32) (u32In, error) {
	if mem.CheckRange(at, 1) != nil {
		return u32In{}, nil
	}
	v, err := mem.ReadU8(m, at)
	return u32In{uint32(v), err == nil}, err
}
