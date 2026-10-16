package actors

import (
	"fmt"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/enemies"
)

// The K1 light-shrink (§7.2): one byte, 1, written to a live enemy's
// enemyice.mLightShrinkTimer (an s8 at enemyice +6, c_damagereaction.h:16)
// runs the generic light-arrow death on its next executed frames: about
// 90 frames of shrinking, then fopAcM_delete, fopAcM_onActor, the item
// ball and its death switch, and health -0x80 (enemy_ice,
// c_damagereaction.cpp:117-171). A u16 write would put the 1 in the pad
// byte and 0 in the timer: on the owner's game a 16-bit write did nothing
// and the one-byte write shrank and deleted three ChuChus (Appendix C
// item 141).

// DemoTypeVerified says Link's mDemo.mDemoType offset (addr.
// PlayerOffDemoType) has been read on a live game. On the owner's game on
// 2026-10-15, peek-demo read 0 at idle, 2 during a stage-entry event
// (Cave02 to M_NewD2) and 0 again after it (docs/08 §8.2 K1). The demos
// the gate exists for, a Floormaster's grab, a ReDead's grab and the
// Forsaken Fortress Moblin's holdup (d_a_mo2.cpp:2829-2830), set the same
// field and are left to the owner's K7. With it false the gate fails
// closed and no light-shrink is written; tests set it false to check that.
var DemoTypeVerified = true

// Class is a K1 class: where its enemyice is, whether it is verified, and
// what M6's gates read of it beyond fopAc_ac_c.
type Class struct {
	Proc stagedata.Proc
	Name string // enemies.Class.Name: "BK"
	// Ice is its enemyice member's offset (enemies.Class.Ice).
	Ice uint32
	// Verified: enemies.Class.K1Verified. An unverified class is
	// light-shrunk only by the enemy-kill diagnostic's --unverified.
	Verified bool
	// Weapon is the offset of the process ID of the weapon it carries
	// (a BOKO actor), 0 for none; WeaponVerified as Verified.
	Weapon         uint32
	WeaponVerified bool
	// Hold is the offset of the carrier's own record that it holds its
	// weapon, 0 for none: the weapon field keeps a stale process ID after
	// the weapon is lost, so it alone is no sign of a weapon in hand.
	// holdS8 says the record is an s8 (else an s32); holding judges it.
	Hold    uint32
	holdS8  bool
	holding func(v int32) (held, settling bool)
	// Mode is the offset of an s32 mode a gate reads (held), 0 for none.
	Mode uint32
	held func(mode int32) string
}

// The classes' weapons and modes (the plan's preludes and gates 18-19):
//
//   - Bokoblin m1200, the BOKO it holds (d_a_bk.h:112), with CARRY set
//     while it does (fopAcM_setCarryNow, d_a_bk.cpp:4220-4224). m1200 is
//     not cleared when the stick is lost (yari_off_check zeroes only
//     m0B34 and m0B30, d_a_bk.cpp:100-137; only daBk_wepon_view_check
//     rewrites it, :826-837), so the stick it names may lie on the ground
//     or be in Link's hands. int m0B30 (d_a_bk.h:74) is nonzero exactly
//     while it holds one: set with CARRY (:2821, :4223), zeroed when the
//     stick goes (:137, :4414), and the game's own light-arrow hit lets
//     the stick go only when it is nonzero (:3601-3604);
//   - Stalfos mHeldWeaponEntityId (d_a_st.h:103), whose CARRY its own
//     light-arrow hit cancels (d_a_st.cpp:1570-1577). s8 m1DD0
//     (d_a_st.h:104) is 1 from its create until its first draw sets CARRY
//     (:2898-2899, :2271-2273), 2 or 10 while it holds the mace (:2006,
//     :2051), and 0 once the mace falls (:2022-2025);
//   - Darknut mBokoPcId (d_a_tn.h:122), a header of D44J01's layout only
//     (configure.py:1858), verified live on the owner's game on 2026-10-15:
//     enemy-kill on a kaze r8 Darknut named its sword there, cleared the
//     sword's CARRY and the sword fell to the floor (docs/08 §8.2 K1);
//   - ReDead mMode (d_a_rd.h:137): MODE_ATTACK 7 holds Link in its grab,
//     and MODE_SW_WAIT 0xA returns from _execute before enemy_ice runs
//     (d_a_rd.cpp:1621-1630), so a timer written then would wait;
//   - Floormaster mMode (d_a_fm.h:130): 13-16 are its grabs of Link and
//     NPCs and 17 Link's start demo (mode_tbl, d_a_fm.cpp:2306-2420).
const (
	weaponBK = 0x1200
	weaponST = 0x1DCC
	weaponTN = 0x1410
	holdBK   = 0xB30
	holdST   = 0x1DD0
	modeRD   = 0x2AC
	modeFM   = 0x2AC
)

var classes = func() map[int16]Class {
	out := map[int16]Class{}
	for _, c := range enemies.K1Classes() {
		if c.Ice == 0 || c.Proc == stagedata.ProcKS {
			// Every placed Morth is a generator that never exists
			// (d_a_ks.cpp:1582-1610): no Morth is ever a target.
			continue
		}
		k := Class{Proc: c.Proc, Name: c.Name, Ice: c.Ice, Verified: c.K1Verified}
		switch c.Proc {
		case stagedata.ProcBK:
			k.Weapon, k.WeaponVerified = weaponBK, true
			k.Hold, k.holding = holdBK, func(v int32) (bool, bool) { return v != 0, false }
		case stagedata.ProcST:
			k.Weapon, k.WeaponVerified = weaponST, true
			k.Hold, k.holdS8, k.holding = holdST, true, func(v int32) (bool, bool) { return v >= 2, v == 1 }
		case stagedata.ProcTN:
			k.Weapon, k.WeaponVerified = weaponTN, true
		case stagedata.ProcRD:
			k.Mode, k.held = modeRD, func(m int32) string {
				switch m {
				case 7:
					return "a ReDead holding Link (MODE_ATTACK)"
				case 0xA:
					return "a ReDead waiting on its switch (MODE_SW_WAIT runs no enemy_ice)"
				}
				return ""
			}
		case stagedata.ProcFM:
			k.Mode, k.held = modeFM, func(m int32) string {
				if m >= 13 && m <= 17 {
					return fmt.Sprintf("a Floormaster in its grab or Link's demo (mode %d)", m)
				}
				return ""
			}
		}
		out[int16(c.Proc)] = k
	}
	return out
}()

// K1 returns proc's K1 class, if it is one M6 may light-shrink.
func K1(proc int16) (Class, bool) {
	c, ok := classes[proc]
	return c, ok
}

// SetVerified sets K1 class proc's Verified to v and returns its old value;
// it does nothing for a proc that is no K1 class. Every K1 class with an
// enemyice passed the live check K1 on 2026-10-15 (docs/08 §8.2), so tests
// use it to keep the path of an unverified class covered. It is not safe
// while a walk or a write is under way.
func SetVerified(proc int16, v bool) (old bool) {
	c, ok := classes[proc]
	if !ok {
		return false
	}
	old, c.Verified = c.Verified, v
	classes[proc] = c
	return old
}

// Classes returns every K1 class, in proc order.
func Classes() []Class {
	var out []Class
	for _, c := range enemies.K1Classes() {
		if k, ok := classes[int16(c.Proc)]; ok {
			out = append(out, k)
		}
	}
	return out
}

// ProcName is a short name for proc p, where the walk knows one.
func ProcName(p int16) string {
	switch p {
	case 0xA7: // fpcNm_SHIP_e, f_pc_name.h:176
		return "ship"
	case 0xA9: // fpcNm_PLAYER_e, f_pc_name.h:178
		return "player"
	case addr.ProcBoko:
		return "boko"
	}
	if c, ok := enemies.ClassOf(stagedata.Proc(p)); ok {
		return strings.ToLower(c.Name)
	}
	return ""
}

// Eligible returns "" if a may be light-shrunk now, by what a walk read of
// it, or why not: §7.2's per-actor gates, each read again by Revalidate
// before the write.
//
//   - a K1 class (not Morth, not a boss: boss procs are no K1 class), and a
//     Verified one unless unverified is true;
//   - mInitState 2: the actor executes (fpcEx_Execute, f_pc_executor.cpp:31);
//   - group 2, fopAc_ENEMY_e (f_op_actor.h:57);
//   - a setID the game takes: below 0x200 with a home room of 0..63, or
//     0xFFFF, which onActor ignores (d_save.cpp:1672-1674). 0x200-0xFFFE
//     halts the game in onActor (:1676);
//   - enemyice.mpActor is the actor (the class's create ran, and the
//     offset is right), its mMode is 1 (enemy_ice has run and the actor
//     is not frozen) and its freeze timer 0;
//   - the timer is 0 (another write, or a Light Arrow, got there first);
//   - health above 0 (a dying actor's own death is under way: a ReDead
//     calls onActor every frame of its last 300, d_a_rd.cpp:836-853);
//   - no BOSS status (Class.IsBoss; a Bokoblin of type 0xA stops its
//     mini-boss music only by its own death, d_a_bk.cpp:2639) and no CARRY
//     (Link holds it);
//   - eventInfo.mCommand 0: no event has ordered it;
//   - the class's mode is not one its gate holds (a ReDead's grab or
//     switch wait, a Floormaster's grab).
func Eligible(a *Actor, unverified bool) string {
	c, ok := K1(a.Proc)
	switch {
	case !ok:
		return "not a K1 class"
	case !c.Verified && !unverified:
		return fmt.Sprintf("class %s is not verified (docs/08 §8.2 K1)", c.Name)
	case a.InitState != 2:
		return fmt.Sprintf("mInitState %d: not executing", a.InitState)
	case a.Group != 2:
		return fmt.Sprintf("group %d, not an enemy", a.Group)
	case a.SetID != addr.ActorSetIDNone && !a.Keyed():
		return fmt.Sprintf("setID 0x%04X in room %d, which onActor asserts on", a.SetID, a.HomeRoom)
	case !a.Ice.Read:
		return "enemyice unreadable"
	case !a.Ice.SelfOK:
		return fmt.Sprintf("enemyice.mpActor 0x%08X is not the actor", a.Ice.Self)
	case a.Ice.Timer != 0:
		return fmt.Sprintf("the shrink timer is %d already", a.Ice.Timer)
	case a.Ice.Mode != 1:
		return fmt.Sprintf("enemyice mode %d (0: not run yet, 2-3: frozen)", a.Ice.Mode)
	case a.Ice.FreezeTimer != 0:
		return fmt.Sprintf("freeze timer %d", a.Ice.FreezeTimer)
	case a.Health <= 0:
		return fmt.Sprintf("health %d: dying", a.Health)
	case a.Boss():
		return "BOSS status"
	case a.Carried():
		return "CARRY status: Link holds it"
	case a.EvtCmd != 0:
		return fmt.Sprintf("an event ordered it (mCommand %d)", a.EvtCmd)
	}
	if c.held != nil {
		if !a.ModeOK {
			return "its mode is unreadable"
		}
		if why := c.held(a.Mode); why != "" {
			return why
		}
	}
	return ""
}

// HoldState reports what carrier a's own record (Class.Hold) says of its
// weapon: held, or settling (between hands: a Stalfos's mace before its
// first draw sets CARRY). ok is false for a class without the record, or
// when it was not read.
func HoldState(a *Actor) (held, settling, ok bool) {
	c, k1 := K1(a.Proc)
	if !k1 || c.Hold == 0 || !a.HoldOK {
		return false, false, false
	}
	held, settling = c.holding(a.Hold)
	return held, settling, true
}

// HeldBy returns why wp is not the weapon carrier c holds now, by what was
// read of both, or "": wp is a BOKO whose process ID is c's weapon field,
// with CARRY set and its mNowMode Mode_ENEMY_CARRY_e (Link's grab sets
// CARRY too, with Mode_PLAYER_CARRY_e, d_a_player_grab.inc:330-339), and
// c's own record, for a class that keeps one, says it holds a weapon.
func HeldBy(c, wp *Actor) string {
	k, ok := K1(c.Proc)
	switch {
	case !ok || k.Weapon == 0:
		return fmt.Sprintf("%s carries no weapon", c)
	case wp.Proc != addr.ProcBoko:
		return fmt.Sprintf("its weapon pid %d is proc 0x%04X, not BOKO", wp.PID, uint16(wp.Proc))
	case c.Weapon != wp.PID:
		return fmt.Sprintf("it holds pid %d, not the weapon %d", c.Weapon, wp.PID)
	case !wp.Carried():
		return fmt.Sprintf("its weapon %s is not carried", wp)
	case !wp.ModeOK:
		return fmt.Sprintf("its weapon %s has no readable mNowMode", wp)
	case wp.Mode == addr.BokoModePlayerCarry:
		return fmt.Sprintf("Link holds the weapon %s", wp)
	case wp.Mode != addr.BokoModeEnemyCarry:
		return fmt.Sprintf("its weapon %s is in mNowMode %d, not ENEMY_CARRY", wp, wp.Mode)
	}
	if k.Hold != 0 {
		switch held, _, ok := HoldState(c); {
		case !ok:
			return "its weapon record is unreadable"
		case !held:
			return fmt.Sprintf("its weapon record at +0x%X is %d: it holds no weapon", k.Hold, c.Hold)
		}
	}
	return ""
}

// WeaponOf returns the weapon a holds, which a light-shrink of a lets go
// (the CARRY clear its own light-arrow hit makes, d_a_st.cpp:1570-1577;
// d_a_bk.cpp:3601-3604), from w: the BOKO a's weapon field names, which
// HeldBy says a holds. It is nil when a holds none. why is not "" when a
// must wait: its class's weapon field is unverified, or its own record
// says it holds a weapon that is not (yet) one HeldBy takes, or that is
// between hands. A weapon field naming a BOKO a does not hold (a stale
// m1200: a stick on the ground, or in Link's hands) is never let go.
func WeaponOf(w *Walk, a *Actor, unverified bool) (weapon *Actor, why string) {
	c, ok := K1(a.Proc)
	if !ok || c.Weapon == 0 {
		return nil, ""
	}
	none := a.Weapon == 0 || a.Weapon == 0xFFFFFFFF
	if c.Hold != 0 {
		switch held, settling, ok := HoldState(a); {
		case !ok:
			return nil, "its weapon record is unreadable"
		case settling:
			return nil, "its weapon is between hands"
		case !held:
			return nil, "" // whatever its field names is not in its hand
		case none:
			return nil, fmt.Sprintf("its weapon record says it holds one, but its weapon field is %d", int32(a.Weapon))
		}
	} else if none {
		return nil, ""
	}
	if !c.WeaponVerified && !unverified {
		return nil, fmt.Sprintf("the %s weapon field is not verified", c.Name)
	}
	wp, ok := w.ByPID(a.Weapon)
	if !ok {
		if c.Hold != 0 {
			return nil, fmt.Sprintf("its weapon pid %d is not in the walk", a.Weapon)
		}
		return nil, "" // dropped or gone: nothing to let go
	}
	if wp.Proc != addr.ProcBoko {
		return nil, fmt.Sprintf("its weapon pid %d is proc 0x%04X, not BOKO", a.Weapon, uint16(wp.Proc))
	}
	if why := HeldBy(a, wp); why != "" {
		if c.Hold != 0 {
			return nil, why // it says it holds one: wait until both agree
		}
		return nil, "" // on the ground, flying, or in Link's hands
	}
	return wp, ""
}
