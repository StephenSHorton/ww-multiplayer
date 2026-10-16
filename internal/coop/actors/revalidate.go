package actors

import (
	"errors"
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/mem"
)

// ErrChanged is matched (errors.Is) by every Revalidate refusal: the actor
// a write was planned for is no longer what the plan saw.
var ErrChanged = errors.New("actors: the actor changed since the walk")

// Revalidate reads carrier a again, and its weapon w if not nil, just
// before a light-shrink write, and returns them as they are now, or an
// error matching ErrChanged with why they may not be written. Between the
// walk and the write the actor may have died and been freed, and its
// memory reused by another actor (fpcDt_Handler runs at the next frame's
// start, f_pc_manager.cpp:280), so nothing the walk read is trusted:
//
//   - the tag at a.Node is still a queued actor_tag of the actor before it
//     (mpData &g_fopAcTg_Queue, mbIsUse 1, mpTagData the actor), and the
//     actor's actor_type is g_fopAc_type;
//   - its process ID, proc, setID and home room are the walk's: a reused
//     block gets a new process ID (fpcBs_MakeOfId only counts up,
//     f_pc_base.cpp:36-40);
//   - every gate of Eligible holds on the fresh read;
//   - a keyed actor's home room still has its zone: mStatus[room].mZoneNo
//     is 0..31 and that zone's mRoomNo is the room. onActor asserts the
//     zone (d_save.cpp:1678-1679), so a light-shrink of an actor whose
//     room's zone went would halt the game when the shrink ends;
//   - the weapon, if any, is still queued, and HeldBy says the carrier
//     holds it now: a BOKO, the PID the carrier's field holds, with CARRY
//     set and in Mode_ENEMY_CARRY_e, and the carrier's own record (m0B30,
//     m1DD0) says it holds a weapon;
//   - with no weapon planned, the carrier has not taken one up since the
//     walk: its own record still says it holds none (a Bokoblin picks up
//     the stick its m1200 already names, d_a_bk.cpp:2821-2833,
//     :4219-4224), or, for a class without that record, its weapon field
//     is unchanged. A timer written then would leave the new weapon CARRY
//     on a deleted carrier.
//
// unverified is Eligible's. The error is a read error or one matching
// ErrChanged.
func Revalidate(m mem.Mem, a, w *Actor, unverified bool) (Actor, *Actor, error) {
	typ, err := mem.ReadU32(m, addr.FopAcType)
	if err != nil {
		return Actor{}, nil, err
	}
	now, err := reread(m, a, typ)
	if err != nil {
		return Actor{}, nil, err
	}
	if why := Eligible(&now, unverified); why != "" {
		return now, nil, fmt.Errorf("%w: %s: %s", ErrChanged, now.String(), why)
	}
	if now.SetID != addr.ActorSetIDNone {
		if why, err := zoneGone(m, int(now.HomeRoom)); err != nil {
			return now, nil, err
		} else if why != "" {
			return now, nil, fmt.Errorf("%w: %s: %s", ErrChanged, now.String(), why)
		}
	}
	if w == nil {
		if c, _ := K1(now.Proc); c.Weapon != 0 {
			if c.Hold != 0 {
				if held, settling, ok := HoldState(&now); !ok || held || settling {
					return now, nil, fmt.Errorf("%w: %s took up a weapon since the walk (its record at +0x%X is %d)", ErrChanged, now.String(), c.Hold, now.Hold)
				}
			} else if now.Weapon != a.Weapon {
				return now, nil, fmt.Errorf("%w: %s's weapon field is pid %d, not %d as the walk read", ErrChanged, now.String(), now.Weapon, a.Weapon)
			}
		}
		return now, nil, nil
	}
	wn, err := reread(m, w, typ)
	if err != nil {
		return now, nil, err
	}
	if why := HeldBy(&now, &wn); why != "" {
		return now, nil, fmt.Errorf("%w: %s: %s", ErrChanged, now.String(), why)
	}
	return now, &wn, nil
}

// reread reads a's node again as a walk would, and checks it is still a:
// typ is g_fopAc_type.
func reread(m mem.Mem, a *Actor, typ uint32) (Actor, error) {
	if why := NodeBad(a.Node); why != "" || a.Addr != a.Node-addr.ActorOffActorTag {
		return Actor{}, fmt.Errorf("%w: node 0x%08X %s", ErrChanged, a.Node, why)
	}
	b, tag, err := readNode(m, a.Node)
	if err != nil {
		return Actor{}, err
	}
	if why := tagBad(a.Node, tag); why != "" {
		return Actor{}, fmt.Errorf("%w: %s: its node %s", ErrChanged, a.String(), why)
	}
	if t := be32(b[addr.ActorOffActorType:]); typ == 0 || t != typ {
		return Actor{}, fmt.Errorf("%w: %s: actor_type 0x%08X, not g_fopAc_type 0x%08X", ErrChanged, a.String(), t, typ)
	}
	now := decode(a.Node, b)
	if err := readClass(m, &now); err != nil {
		return Actor{}, err
	}
	switch {
	case now.PID != a.PID || now.Proc != a.Proc:
		return now, fmt.Errorf("%w: %s is now pid %d, proc 0x%04X (freed and reused)", ErrChanged, a.String(), now.PID, uint16(now.Proc))
	case now.SetID != a.SetID || now.HomeRoom != a.HomeRoom:
		return now, fmt.Errorf("%w: %s now has setID 0x%04X in room %d", ErrChanged, a.String(), now.SetID, now.HomeRoom)
	}
	return now, nil
}

// zoneGone returns why room's zone does not hold, or "": mStatus[room].
// mZoneNo is 0..31 and the zone's mRoomNo names room back (§3.1).
func zoneGone(m mem.Mem, room int) (string, error) {
	zn, err := addr.ZoneNoAddr(room)
	if err != nil {
		return fmt.Sprintf("home room %d has no zone (onActor asserts, d_save.cpp:1676)", room), nil
	}
	b, err := mem.ReadU8(m, zn)
	if err != nil {
		return "", err
	}
	z := int(int8(b))
	za, err := addr.ZoneAddr(z)
	if err != nil {
		return fmt.Sprintf("home room %d has zone %d (onActor asserts, d_save.cpp:1678-1679)", room, z), nil
	}
	r, err := mem.ReadU8(m, za+addr.ZoneOffRoomNo)
	if err != nil {
		return "", err
	}
	if int(int8(r)) != room {
		return fmt.Sprintf("home room %d's zone %d is room %d's", room, z, int8(r)), nil
	}
	return "", nil
}

// ZoneHolds reports whether room's zone exists in m: what a keyed actor's
// light-shrink needs, as onActor looks it up (d_save.cpp:1678-1679).
func ZoneHolds(m mem.Mem, room int) (bool, error) {
	why, err := zoneGone(m, room)
	return why == "" && err == nil, err
}
