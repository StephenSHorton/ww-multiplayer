package replica

import (
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/combat"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// The WriteGuard's part of M6 (§7.2): the zones' dead-actor bytes
// (TargetKills) and the light-shrink (TargetK1). Neither is judged by the
// static allowlist alone: a dead-actor byte is let through only into the
// zone of a loaded room, for setIDs the tick's plan takes, and a
// light-shrink write only for an actor the tick's walk has, as one of two
// shapes. The plan says what it takes once per tick (SetCombat), after
// SetTick opened the gates; until it does, every M6 write is refused.

// CombatTick is what a tick's M6 plan tells the WriteGuard.
type CombatTick struct {
	// Flags are each room's mStatus flags (addr.RoomStatusOffFlags) as the
	// tick's probe read them; FlagsOK says it read them.
	Flags   [addr.RoomCount]uint8
	FlagsOK bool
	// Kills reports whether setID's death in room may be written into this
	// game's zone: the partner's store has it and the M1 verdict syncs it
	// (synctables.SyncedKill). Nil takes none.
	Kills func(room, setID int) bool
	// Walk is the tick's actor walk; nil takes no light-shrink.
	Walk *actors.Walk
	// Allow reports whether carrier a may be light-shrunk: for the
	// receiver, a keyed actor whose death Kills takes; for the enemy-kill
	// diagnostic, the one actor asked for. Nil takes none.
	Allow func(a *actors.Actor) bool
	// Unverified lets an unverified class be shrunk (the enemy-kill
	// diagnostic's --unverified; never the receiver's).
	Unverified bool
	// Claimed is the game's enemy-mirror claims (Config.Claimed): a pid it
	// claims is never light-shrunk (combat.Eligible). Nil claims nothing.
	Claimed combat.ClaimFunc
	// Demo: Link's demo gate passed: its offset is verified
	// (combat.DemoTypeVerified) and it read 0 (TYPE_NONE).
	Demo bool
}

// SetCombat gives the guard the tick's M6 plan. SetTick forgets it.
func (w *WriteGuard) SetCombat(c CombatTick) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.combat, w.combatOK = c, w.have
}

// combatClaimed is the tick's claim func (CombatTick.Claimed), which the
// FrameWriter's re-read before a light-shrink (combat.Revalidate) takes
// too; nil without a plan.
func (w *WriteGuard) combatClaimed() combat.ClaimFunc {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.combatOK {
		return nil
	}
	return w.combat.Claimed
}

// noteShrunk records that carrier's timer was written and read back
// nonzero this tick: its weapon may be let go (checkActor's (W)).
func (w *WriteGuard) noteShrunk(carrier *actors.Actor) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.shrunk == nil {
		w.shrunk = map[uint32]uint32{}
	}
	w.shrunk[carrier.Addr] = carrier.PID
}

// killSetIDs lists the setIDs the bits of b set at a, a byte of zone
// slot z's dead-actor words: word i is big-endian, so its last byte holds
// setIDs 32i+0..7 (dSv_zoneActor_c::on, d_save.cpp:1326-1329).
func killSetIDs(a uint32, z int, b byte) []int {
	base, _ := addr.ZoneAddr(z)
	rel := int(a - base - addr.ZoneOffActor)
	word, at := rel/4, rel%4
	var out []int
	for bit := range 8 {
		if b&(1<<bit) != 0 {
			out = append(out, 32*word+8*(3-at)+bit)
		}
	}
	return out
}

// killClosed returns why val may not be written over old at a, a zone's
// dead-actor byte (TargetKills), this tick, or "":
//
//   - the slot is a room's by the tick's zone table: its mRoomNo names a
//     room whose mZoneNo names the slot back (§3.1), any room, not only
//     Link's: a hidden neighbour's zone is written too, so its enemies are
//     not created when a door shows it (dStage_actorInit's isActor,
//     d_stage.cpp:1693);
//   - the room is loaded and neither being created nor deleted (mStatus
//     flags 0x01 set, 0x02 and 0x04 clear, d_stage.cpp:214-240);
//   - every bit it sets is a setID the tick's plan takes (CombatTick.Kills).
//
// w.mu is held.
func (w *WriteGuard) killClosed(a uint32, old, val byte) string {
	if a < addr.Zones || a >= addr.Zones+addr.ZoneCount*addr.ZoneSize {
		return "it is no zone's dead-actor byte"
	}
	z := int(a-addr.Zones) / addr.ZoneSize
	zt := &w.tick.Zones
	room := int(zt.RoomNo[z])
	switch slot, ok := zt.SlotOf(room); {
	case !zt.Valid:
		return "no zone table was read this tick"
	case !ok || slot != z:
		return fmt.Sprintf("zone slot %d is no room's", z)
	case !w.combatOK || w.combat.Kills == nil:
		return "no M6 plan this tick"
	case !w.combat.FlagsOK:
		return "the rooms' status was not read this tick"
	}
	if f := w.combat.Flags[room]; f&addr.RoomFlagLoaded == 0 || f&(addr.RoomFlagCreating|addr.RoomFlagDeleting) != 0 {
		return fmt.Sprintf("room %d is not loaded and settled (status flags 0x%02X)", room, f)
	}
	for _, id := range killSetIDs(a, z, val&^old) {
		if !w.combat.Kills(room, id) {
			return fmt.Sprintf("setID %d of room %d is in no partner's store, or does not sync", id, room)
		}
	}
	return ""
}

// actorKind is which of the two light-shrink writes an ActorByte is.
type actorKind uint8

const (
	// actorTimer is (T): the carrier's mLightShrinkTimer, one byte, 0 to 1.
	actorTimer actorKind = iota + 1
	// actorWeapon is (W): the CARRY byte of the weapon the carrier holds,
	// CARRY cleared and nothing else.
	actorWeapon
)

func (k actorKind) String() string {
	if k == actorWeapon {
		return "weapon CARRY byte"
	}
	return "light-shrink timer"
}

// checkActor reports whether val may be written over old at a, a
// light-shrink byte of kind k for carrier c (its weapon wp for (W)), as
// combat.Revalidate read them just before. It lets through exactly:
//
//   - (T) the timer, c.TimerAddr(), from 0 to 1: c is in the tick's walk
//     with the same process ID, its class is K1 and verified (or the tick
//     allows unverified ones), every combat.Eligible gate holds, and the
//     plan allows it (CombatTick.Allow: the partner's store and the verdict
//     have its key);
//   - (W) the weapon's CARRY byte (actor_status, +0x1C6), CARRY set before
//     and cleared after with every other bit kept: the weapon is in the
//     walk, and one c holds (combat.HeldBy: a BOKO whose process ID is c's
//     weapon field, in Mode_ENEMY_CARRY_e, not Link's PLAYER_CARRY, and
//     c's own record says it holds a weapon), and c's timer was
//     written and read back nonzero this tick (noteShrunk), so a refused
//     timer never leaves a live carrier without its weapon
//     (d_a_bk.cpp:4220-4224).
//
// Both need ARMED, IDLE with the gates open, the plan of this tick, and
// Link's demo gate. A refusal is counted, logged and reported as Check's.
func (w *WriteGuard) checkActor(k actorKind, c *actors.Actor, wp *actors.Actor, a uint32, old, val byte) error {
	w.mu.Lock()
	w.checked++
	e := &RefusalError{Addr: a, Old: old, New: val, What: k.String()}
	e.Reason, e.Why = w.judgeActor(k, c, wp, a, old, val)
	if e.Reason == 0 {
		w.mu.Unlock()
		return nil
	}
	return w.refuse(e)
}

// judgeActor is checkActor's verdict. w.mu is held.
func (w *WriteGuard) judgeActor(k actorKind, c *actors.Actor, wp *actors.Actor, a uint32, old, val byte) (Refusal, string) {
	switch {
	case w.brk.Off():
		return RefuseOff, w.brk.Why()
	case w.allow.targets&TargetK1 == 0:
		return RefuseTarget, "the light-shrink is not this guard's target"
	case !w.have:
		return RefuseGate, "no tick has opened the gates yet"
	case w.closed != "":
		return RefuseGate, w.closed
	}
	if why := w.tick.opens(TargetK1, a); why != "" {
		return RefuseGate, why
	}
	cb := &w.combat
	switch {
	case !w.combatOK || cb.Walk == nil || cb.Allow == nil:
		return RefuseGate, "no M6 plan this tick"
	case !cb.Demo:
		return RefuseGate, "Link's demo gate is closed (unverified, or Link is in a demo)"
	case c == nil:
		return RefuseTarget, "no carrier"
	}
	in, ok := cb.Walk.ByAddr(c.Addr)
	switch {
	case !ok || in.PID != c.PID || in.Proc != c.Proc:
		return RefuseTarget, fmt.Sprintf("%s is not in this tick's walk", c)
	case !cb.Allow(c):
		return RefuseTarget, fmt.Sprintf("%s is no target of this tick's plan", c)
	}
	switch k {
	case actorTimer:
		if a != c.TimerAddr() || a == 0 {
			return RefuseTarget, fmt.Sprintf("0x%08X is not %s's light-shrink timer", a, c)
		}
		if old != 0 || val != 1 {
			return RefuseValue, fmt.Sprintf("the timer may only go from 0 to 1, not from %d to %d", int8(old), int8(val))
		}
		if why := combat.Eligible(c, cb.Unverified, cb.Claimed); why != "" {
			return RefuseTarget, fmt.Sprintf("%s: %s", c, why)
		}
	case actorWeapon:
		switch {
		case wp == nil:
			return RefuseTarget, "no weapon"
		case a != wp.Addr+addr.ActorOffCarryByte:
			return RefuseTarget, fmt.Sprintf("0x%08X is not the CARRY byte of %s", a, wp)
		case old&addr.ActorCarryByteMask == 0 || val != old&^addr.ActorCarryByteMask:
			return RefuseValue, fmt.Sprintf("a weapon's CARRY byte may only lose CARRY, not go from 0x%02X to 0x%02X", old, val)
		}
		inW, ok := cb.Walk.ByAddr(wp.Addr)
		switch {
		case !ok || inW.PID != wp.PID || inW.Proc != addr.ProcBoko || wp.Proc != addr.ProcBoko:
			return RefuseTarget, fmt.Sprintf("%s is no BOKO of this tick's walk", wp)
		case combat.HeldBy(c, wp) != "":
			return RefuseTarget, fmt.Sprintf("%s: %s", c, combat.HeldBy(c, wp))
		case w.shrunk[c.Addr] != c.PID || c.PID == 0:
			return RefuseTarget, fmt.Sprintf("%s's timer was not written and read back this tick", c)
		}
	default:
		return RefuseTarget, "no light-shrink write"
	}
	return 0, ""
}
