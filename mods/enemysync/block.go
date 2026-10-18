package enemysync

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"slices"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// What the host sampler and the client matcher share: the private MO2
// walk layout, the scope reads (stage, room, in play), the room's
// dead-actor mask and one Moblin's per-tick sample.

// ProcMO2 is the Moblin's proc (fpcNm_MO2_e, f_pc_name.h:198).
const ProcMO2 = uint16(stage.ProcMO2)

// Layouts is the private walk layout: a Moblin's enemyice and its u8
// mMode. combat.Layouts() is shared and untouched.
var Layouts = actors.Layouts{int16(stage.ProcMO2): {Ice: addr.Mo2OffIce, Mode: addr.Mo2OffMode, ModeU8: true}}

// Scope is where a game is: its stage and room, and whether it is in play
// (feed.inPlay's rule: Link loaded, not the title or file select, no
// stage change pending).
type Scope struct {
	Stage  string
	Room   int8
	InPlay bool
	// Pending is mNextStage.mEnable: a stage change is under way.
	Pending bool
}

// ReadScope reads the scope from m: PlayerPtr, CurStageName..NextStageEnable
// in one read, and mStayNo.
func ReadScope(m mem.Reader) (Scope, error) {
	var s Scope
	link, err := mem.ReadU32(m, addr.PlayerPtr)
	if err != nil {
		return s, err
	}
	n := int(addr.NextStageEnable-addr.CurStageName) + 1
	b, err := m.Read(addr.CurStageName, n)
	if err != nil {
		return s, err
	}
	if len(b) != n {
		return s, fmt.Errorf("enemysync: stage read %d bytes, want %d", len(b), n)
	}
	name := b[:addr.StageNameLen]
	if i := bytes.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	s.Stage = string(name)
	s.Pending = b[n-1] != 0
	room, err := mem.ReadS8(m, addr.StayNo)
	if err != nil {
		return s, err
	}
	s.Room = room
	s.InPlay = addr.InMEM1(link) && !s.Pending && s.Stage != "" && s.Stage != addr.StageTitle && s.Stage != addr.StageFileSelect
	return s, nil
}

// DeadMask reads the room's dead-actor bits for keyed setIDs 0..7 (bit n
// = setID n), the way the co-op kill path addresses them: mStatus[room].
// mZoneNo names the zone, whose dSv_zoneActor_c words sit at ZoneOffActor
// (dSv_zoneActor_c::on, d_save.cpp:1326-1329). A room without a zone has
// no deaths recorded: mask 0.
func DeadMask(m mem.Reader, room int8) (uint8, error) {
	zn, err := addr.ZoneNoAddr(int(room))
	if err != nil {
		return 0, nil
	}
	z, err := mem.ReadS8(m, zn)
	if err != nil {
		return 0, err
	}
	za, err := addr.ZoneAddr(int(z))
	if err != nil {
		return 0, nil // -1: no zone
	}
	word, err := mem.ReadU32(m, za+addr.ZoneOffActor)
	if err != nil {
		return 0, err
	}
	return uint8(word & 0xFF), nil
}

// Moblin is a keyed, executing Moblin a walk found, with an mMode the
// mirror takes (MirrorsMode).
type Moblin struct {
	Addr, PID uint32
	SetID     uint16
	HomeRoom  int8
	Mode      uint8 // mo2 mMode (addr.Mo2OffMode)
}

// MirrorsMode reports whether a Moblin with mo2 mMode mode is one the
// mirror takes: 0 (patrol) and 1 (the lantern variant: M_Dai room 1's
// setIDs 0 and 1 carry params FF00FF01 and FF01FF01, so create's low
// byte makes them mode 1, d_a_mo2.cpp:4080, and they behave as normal
// Moblins once the player has a pearl, d_a_mo2.cpp:4134-4146). A carried
// one (addr.Mo2ModeCarried) and a statue (addr.Mo2ModeStatue) are
// refused, as the REL refuses them (WWENEMY_ERR_MODE).
func MirrorsMode(mode int32) bool { return mode == 0 || mode == 1 }

// KeyedMoblins walks the actors with Layouts and returns the keyed
// (setID < 0x200, home room 0..63), executing (initState 2) Moblins of
// a mode the mirror takes (MirrorsMode) whose home room is room, sorted
// by setID, at most MaxSlots of them (the lowest setIDs).
func KeyedMoblins(m mem.Reader, room int8) ([]Moblin, error) {
	w, err := actors.ReadWith(m, 0, Layouts)
	if err != nil {
		return nil, err
	}
	var out []Moblin
	for i := range w.Actors {
		a := &w.Actors[i]
		if a.Proc == int16(stage.ProcMO2) && a.Keyed() && a.InitState == 2 && a.ModeOK && MirrorsMode(a.Mode) && a.HomeRoom == room {
			out = append(out, Moblin{Addr: a.Addr, PID: a.PID, SetID: a.SetID, HomeRoom: a.HomeRoom, Mode: uint8(a.Mode)})
		}
	}
	slices.SortFunc(out, func(a, b Moblin) int { return int(a.SetID) - int(b.SetID) })
	if len(out) > MaxSlots {
		out = out[:MaxSlots]
	}
	return out, nil
}

// Sample is one Moblin's fields as a tick read them.
type Sample struct {
	Slot
	IceTimer int8
	Hidden   bool
}

// The per-tick reads of one Moblin, beyond its fopAc_ac_c: the spans
// below, each one Read.
const (
	readInvulnOff = addr.Mo2OffInvuln                                // s16 m05B4 .. through damagereaction mode/action
	readInvulnLen = addr.Mo2OffDRAction + 2 - addr.Mo2OffInvuln      // 0x68
	readTailOff   = addr.Mo2OffBareHanded                            // u8 m2943 .. s8 m2A1C .. ice timer
	readTailLen   = addr.Mo2OffIceShrink + 1 - addr.Mo2OffBareHanded // 0x118
)

// ReadSample reads mo's fields for one tick. It checks the actor is
// still mo (process ID, proc, initState 2, setID); a mismatch is
// actors.ErrChanged and the caller drops the entry and walks again.
func ReadSample(m mem.Reader, mo *Moblin) (Sample, error) {
	var s Sample
	b, err := m.Read(mo.Addr, addr.ActorSize)
	if err != nil {
		return s, err
	}
	if len(b) != addr.ActorSize {
		return s, fmt.Errorf("enemysync: actor 0x%08X: %d bytes read", mo.Addr, len(b))
	}
	be := binary.BigEndian
	if be.Uint32(b[addr.ActorOffBsPcID:]) != mo.PID || be.Uint16(b[addr.ActorOffProcName:]) != ProcMO2 ||
		int8(b[addr.ActorOffInitState]) != 2 || be.Uint16(b[addr.ActorOffSetID:]) != mo.SetID || int8(b[addr.ActorOffHomeRoomNo]) != mo.HomeRoom {
		return s, fmt.Errorf("%w: Moblin setID %d pid %d at 0x%08X", actors.ErrChanged, mo.SetID, mo.PID, mo.Addr)
	}
	s.SetID, s.Proc = mo.SetID, ProcMO2
	for i := range 3 {
		s.Pos[i] = math.Float32frombits(be.Uint32(b[addr.ActorOffCurPos+4*i:]))
	}
	s.AngleY = int16(be.Uint16(b[addr.ActorOffCurAngle+2:]))
	s.Health = int8(b[addr.ActorOffHealth])

	d, err := m.Read(mo.Addr+readInvulnOff, readInvulnLen)
	if err != nil {
		return s, err
	}
	if len(d) != readInvulnLen {
		return s, fmt.Errorf("enemysync: actor 0x%08X: short damagereaction read", mo.Addr)
	}
	invuln := int16(be.Uint16(d))
	s.Mode = int16(be.Uint16(d[addr.Mo2OffDRMode-readInvulnOff:]))
	s.Action = int16(be.Uint16(d[addr.Mo2OffDRAction-readInvulnOff:]))
	tilt, err := mem.ReadS16(m, mo.Addr+addr.Mo2OffTilt)
	if err != nil {
		return s, err
	}
	s.Tilt = tilt
	atk, err := m.Read(mo.Addr+addr.Mo2OffAttackType, 8)
	if err != nil {
		return s, err
	}
	if len(atk) == 8 {
		s.AttackType, s.AttackPhase = atk[3], atk[7]
	}
	morf, err := mem.ReadU32(m, mo.Addr+addr.Mo2OffMorf)
	if err != nil {
		return s, err
	}
	if at := morf + addr.McaMorfOffFrameCtrl; mem.CheckRange(at, addr.McaMorfFrameCtrlLen) == nil {
		fc, err := m.Read(at, addr.McaMorfFrameCtrlLen)
		if err != nil {
			return s, err
		}
		if len(fc) == addr.McaMorfFrameCtrlLen {
			s.MorfRate = math.Float32frombits(be.Uint32(fc[addr.McaMorfOffRate-addr.McaMorfOffFrameCtrl:]))
			s.MorfFrame = math.Float32frombits(be.Uint32(fc[addr.McaMorfOffFrame-addr.McaMorfOffFrameCtrl:]))
		}
	}
	tail, err := m.Read(mo.Addr+readTailOff, readTailLen)
	if err != nil {
		return s, err
	}
	if len(tail) != readTailLen {
		return s, fmt.Errorf("enemysync: actor 0x%08X: short tail read", mo.Addr)
	}
	bare := tail[0] != 0
	s.Hidden = tail[addr.Mo2OffHide-readTailOff] != 0
	s.IceTimer = int8(tail[addr.Mo2OffIceShrink-readTailOff])

	if s.Health <= 0 || s.Action == 20 || s.Action == 21 || s.IceTimer != 0 {
		s.Flags |= SlotDying
	}
	if invuln != 0 {
		s.Flags |= SlotInvuln
	}
	if s.Action == 10 {
		s.Flags |= SlotDefence
	}
	if s.Mode <= -100 {
		s.Flags |= SlotRagdoll
	}
	if bare {
		s.Flags |= SlotBareHanded
	}
	if s.Hidden {
		s.Flags |= SlotHidden
	}
	if !finiteOK(s.Pos[0], s.Pos[1], s.Pos[2], s.MorfFrame, s.MorfRate) {
		return s, fmt.Errorf("enemysync: Moblin setID %d: NaN or Inf read at 0x%08X", mo.SetID, mo.Addr)
	}
	return s, nil
}

func finiteOK(fs ...float32) bool { return finite(fs...) == nil }
