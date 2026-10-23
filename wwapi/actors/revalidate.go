package actors

import (
	"errors"
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// ErrChanged is matched (errors.Is) by every Reread refusal, and by every
// refusal of a re-read built on it (co-op's combat.Revalidate): the actor
// a write was planned for is no longer what the plan saw.
var ErrChanged = errors.New("actors: the actor changed since the walk")

// Reread reads a's node again as a walk with layouts would, just before a
// write that rests on it, and checks it is still a; typ is g_fopAc_type
// (addr.FopAcType) as read just before. Between the walk and the write
// the actor may have died and been freed, and its memory reused by another
// actor (fpcDt_Handler runs at the next frame's start,
// f_pc_manager.cpp:280), so nothing the walk read is trusted:
//
//   - the tag at a.Node is still a queued actor_tag of the actor before it
//     (mpData &g_fopAcTg_Queue, mbIsUse 1, mpTagData the actor), and the
//     actor's actor_type is g_fopAc_type;
//   - its process ID, proc, setID and home room are the walk's: a reused
//     block gets a new process ID (fpcBs_MakeOfId only counts up,
//     f_pc_base.cpp:36-40).
//
// The error is a read error or one matching ErrChanged.
func Reread(m mem.Reader, a *Actor, typ uint32, layouts Layouts) (Actor, error) {
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
	if l, ok := layouts[now.Proc]; ok {
		if err := readLayout(m, &now, l); err != nil {
			return Actor{}, err
		}
	}
	switch {
	case now.PID != a.PID || now.Proc != a.Proc:
		return now, fmt.Errorf("%w: %s is now pid %d, proc 0x%04X (freed and reused)", ErrChanged, a.String(), now.PID, uint16(now.Proc))
	case now.SetID != a.SetID || now.HomeRoom != a.HomeRoom:
		return now, fmt.Errorf("%w: %s now has setID 0x%04X in room %d", ErrChanged, a.String(), now.SetID, now.HomeRoom)
	}
	return now, nil
}

// RereadParams is Reread with the params compared too: a's base params
// (+0xB0) still the walk's. Co-op's actor_refresh relay asks it of an
// actor just before it names the actor in a request (enemy-mirror-
// batch-d.json G-D3), whose params the REL compares again (MISMATCH).
// Reread itself leaves the params out: a Boko weapon sets its own in life
// (fopAcM_SetParam, d_a_boko.cpp:946), and M6's Revalidate rides on
// Reread. The error is Reread's, or one matching ErrChanged for other
// params.
func RereadParams(m mem.Reader, a *Actor, typ uint32, layouts Layouts) (Actor, error) {
	now, err := Reread(m, a, typ, layouts)
	if err != nil {
		return now, err
	}
	if now.Params != a.Params {
		return now, fmt.Errorf("%w: %s now has params 0x%08X", ErrChanged, a.String(), now.Params)
	}
	return now, nil
}

// ZoneGone returns why room's zone does not hold, or "": mStatus[room].
// mZoneNo is 0..31 and the zone's mRoomNo names room back (§3.1). A keyed
// actor's light-shrink needs it to hold: onActor asserts the zone
// (d_save.cpp:1678-1679).
func ZoneGone(m mem.Reader, room int) (string, error) {
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
func ZoneHolds(m mem.Reader, room int) (bool, error) {
	why, err := ZoneGone(m, room)
	return why == "" && err == nil, err
}
