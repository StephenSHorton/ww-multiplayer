package replica

import (
	"fmt"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/combat"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/enemies"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/switches"
)

// The stale-actor refresh (ww-multiplayer-handoff/design/
// stale-actor-refresh.md, Tiers 1 and 2): many actors read their bit only
// in create, so one created before a partner's bit landed here stays as
// it was until its room reloads. Progress is never lost; what the player
// sees is stale. The refresh acts only on an actor whose bit is in the
// landed set (landed.go), the bits this replica's own writes set and
// verified, never on one that merely reads set, so the local player's own
// play is never touched. It runs inside the M6 receiver's tick
// (reconcileM6), over the same actor walk, with its gates:
//
//   - Tier 1, the key-door lock: a DOOR10 or DOOR12 whose lock switch
//     (switches.RoleKeyLock: a small-key door, or a boss door with a swbit
//     below 0x80, d_a_door10.cpp:28-50) landed keeps its lock until Link's
//     next room change. With no key (the count is derived, and drops when
//     the partner opens the door) it offers no "Open" (:359-373), which
//     blocks play. ApplyLocks clears dDoor_key2_c.mbEnabled
//     (writer_lock.go);
//   - Tier 2, the death switch: a K1 enemy whose death switch
//     (switches.RoleDeath, home.angle.z's low byte) landed is one the
//     partner killed. It is a second allow rule of M6's light-shrink
//     (deathAllow), with every gate of combat.Eligible, Revalidate and the
//     rest of M6 unchanged. The shrink re-sets the latching switch
//     (c_damagereaction.cpp:165-166) and drops one item ball here, as M6's
//     keyed shrink does. In practice it reaches Bokoblins: a Moblin,
//     Stalfos or Darknut with a memory death switch (1..0x7F) is BOSS from
//     create (d_a_mo2.cpp:4099-4100, d_a_st.cpp:2839-2841,
//     d_a_tn.cpp:4213-4214) and Eligible refuses BOSS (Check note 3), so
//     those classes are reached only with a dan or zone death switch. An
//     unkeyed Bokoblin the enemy mirror claims stays the mirror's
//     (Eligible refuses a claimed pid). A keyed one is taken only when
//     M1's verdict syncs its key (synctables.KillMask): the shrink's
//     death records its setID in this game's zone (fopAcM_onActor,
//     c_damagereaction.cpp:159), a write M6 refuses for a Shared or
//     unsynced key.
//
// The gates every candidate passes (design "Gates (all tiers)"), each
// read again just before the write: co-op armed, the hub granting
// FeatureCombat, not a dry run (m6Granted); IDLE, no stage change pending
// (recheck); not a boss stage (bossStage, Check note 9); the bit in the
// landed set for at least the settle time (Config.RefreshSettle, 1 s by
// default) and reading set now, in its store (Check note 10); the actor's
// room loaded and settled; the actor executing, in no event, not carried,
// not a mirror copy, not claimed; Link in no demo and more than 300 units
// away; and each actor at most once (by pid, until a save state).
//
// Never touched (design "Never delete or write"): Link, the keeper and
// remote bodies, mirror copies and claimed pids, any BOSS actor, any
// door beyond its lock byte, switch logic, push blocks, lifts, Kokiie.
// Tier 1 writes only the lock byte of the two door classes; Tier 2 only
// M6's two light-shrink bytes of a K1 class.

// RefreshSettle is how long a bit must have been in the landed set before
// the refresh acts on it (design gate 8), so the replica's own verify has
// passed.
const RefreshSettle = time.Second

// refreshState is the refresh's part of the M6 receiver.
type refreshState struct {
	// This tick's lock clears, made after the light-shrinks (applyCombat).
	locks []LockTarget
	// deathPIDs are this tick's light-shrink targets taken by the death
	// switch rule alone, whose holds the refresh reports.
	deathPIDs map[uint32]bool

	lockEver map[uint32]bool   // the doors whose lock was cleared
	lockHeld map[uint32]string // doors held, with the last reason
	lockLast string
	swHeld   map[uint32]string // death-switch targets held, with the last reason
	swLast   string

	landed  int // bits in the landed set, as the last tick counted them
	visit   string
	cleared int
	shrunk  int
}

func newRefreshState() refreshState {
	return refreshState{deathPIDs: map[uint32]bool{}, lockEver: map[uint32]bool{}, lockHeld: map[uint32]string{}, swHeld: map[uint32]string{}}
}

// forget drops what the refresh remembers of process IDs: a save state
// rewound the counter.
func (f *refreshState) forget() {
	clear(f.lockEver)
	clear(f.lockHeld)
	clear(f.swHeld)
	clear(f.deathPIDs)
}

func (f *refreshState) holdLock(pid uint32, why string) {
	if len(f.lockHeld) >= maxM6Held {
		clear(f.lockHeld)
	}
	f.lockHeld[pid] = why
	f.lockLast = why
}

func (f *refreshState) holdDeath(pid uint32, why string) {
	if len(f.swHeld) >= maxM6Held {
		clear(f.swHeld)
	}
	f.swHeld[pid] = why
	f.swLast = why
}

// RefreshStatus is how the stale-actor refresh stands (Status.Refresh),
// for /status.
type RefreshStatus struct {
	// Landed counts the bits in the landed set this stage visit.
	Landed int
	// Locks counts the key door locks cleared (Tier 1); LockHeld the doors
	// held, LockLastHeld the last reason.
	Locks, LockHeld int
	LockLastHeld    string
	// Shrunk counts the light-shrinks taken by the death switch rule
	// (Tier 2); Held the targets held, LastHeld the last reason.
	Shrunk, Held int
	LastHeld     string
}

// refreshStatus is the refresh's part of r's Status. r.mu is held.
func (r *Replica) refreshStatus() RefreshStatus {
	f := &r.m6.rf
	return RefreshStatus{Landed: f.landed, Locks: f.cleared, LockHeld: len(f.lockHeld), LockLastHeld: f.lockLast,
		Shrunk: f.shrunk, Held: len(f.swHeld), LastHeld: f.swLast}
}

// refreshTick is what one tick's refresh judges its candidates by: the
// landed set and the snapshot, read once, so the rules are the same
// function of an actor at the plan and at the guard's check.
type refreshTick struct {
	landed  Landed
	settle  time.Duration
	now     time.Time
	s       *state.Snapshot
	boss    bool
	flags   [addr.RoomCount]uint8
	flagsOK bool
	link    [3]float32
	linkOK  bool
	// keys are the synced keys (synctables.KillMask) of the home rooms of
	// the walk's keyed actors, filled before the walk is judged and only
	// read after: a keyed actor of a room missing here is refused.
	keys map[int]enemies.KillSet
}

// any reports whether some switch of the loaded table landed.
func (t *refreshTick) any() bool {
	for b := range t.landed.At {
		if b.Kind == LandSwitch && b.Table == t.s.Table {
			return true
		}
	}
	return false
}

// switchWhy judges switch no as an actor of room reads it: cand is false
// when it never landed (the actor is no candidate); else why is "" when
// the refresh may act on it, or why not.
func (t *refreshTick) switchWhy(no, room int) (why string, cand bool) {
	key := SwitchKey(t.s.Table, room, no)
	at, ok := t.landed.Has(key)
	if !ok || t.landed.Stage != t.s.StageName {
		return "", false
	}
	switch set, read := switchSetIn(t.s, no, room); {
	case t.settle > 0 && t.now.Sub(at) < t.settle:
		return fmt.Sprintf("%s landed %v ago, settling for %v", key, t.now.Sub(at).Round(time.Millisecond), t.settle), true
	case !read:
		return fmt.Sprintf("%s has no store here", key), true
	case !set:
		return fmt.Sprintf("%s reads clear now", key), true
	case t.boss:
		return "a boss stage (bossStage)", true
	}
	return "", true
}

// roomSettled returns why room is not loaded and settled, or "".
func (t *refreshTick) roomSettled(room int) string {
	switch {
	case room < 0 || room >= addr.RoomCount:
		return fmt.Sprintf("room %d is no room", room)
	case !t.flagsOK:
		return "the rooms' status was not read this tick"
	}
	if f := t.flags[room]; f&addr.RoomFlagLoaded == 0 || f&(addr.RoomFlagCreating|addr.RoomFlagDeleting) != 0 {
		return fmt.Sprintf("room %d is not loaded and settled (status flags 0x%02X)", room, f)
	}
	return ""
}

// linkFar returns why Link is too near pos, or "".
func (t *refreshTick) linkFar(pos [3]float32) string {
	switch {
	case !t.linkOK:
		return "no Link"
	case !farFrom(t.link, pos):
		return fmt.Sprintf("Link within %d units", linkRefreshRange)
	}
	return ""
}

// switchByteIn returns the byte and mask of switch no, of an actor of
// room, by s's zone table; ok is false when its store has no byte here (a
// zone switch of a room with no zone).
func switchByteIn(s *state.Snapshot, no, room int) (a uint32, mask byte, ok bool) {
	st, bit, err := addr.SwitchBit(no)
	if err != nil {
		return 0, 0, false
	}
	switch st {
	case addr.SwitchMemory:
		a = addr.LiveMemBit + uint32(bit.Off)
	case addr.SwitchDan:
		a = addr.Dan + uint32(bit.Off)
	default:
		z, ok := s.ZoneFor(room)
		if !ok {
			return 0, 0, false
		}
		za, err := addr.ZoneAddr(z)
		if err != nil {
			return 0, 0, false
		}
		a = za + uint32(bit.Off)
	}
	return a, bit.Mask, true
}

// switchSetIn reads switch no, of an actor of room, in s; read is false
// when its store has no byte here (a zone switch of a room with no zone).
func switchSetIn(s *state.Snapshot, no, room int) (set, read bool) {
	a, mask, ok := switchByteIn(s, no, room)
	if !ok {
		return false, false
	}
	v, ok := snapByte(s, a)
	return ok && v&mask != 0, ok
}

// deathWhy judges a by the death switch rule (Tier 2): cand is false when
// a is no K1 enemy with a death switch that landed; else why is "" when
// it may be light-shrunk by this rule, or why not. Its death switch is
// decoded from a's own home.angle (actors.Actor.HomeAngle): a Bokoblin
// zeroes current.angle.z at create, never home.angle (d_a_bk.cpp:4772).
// The switch is read with the home room, as create reads it (d_a_bk.cpp:
// 4771-4780; the setter routes it with fopAcM_GetRoomNo, :2645, which is
// the same room unless the actor left it: Check note 10).
func (t *refreshTick) deathWhy(a *actors.Actor) (why string, cand bool) {
	if _, ok := combat.K1(a.Proc); !ok {
		return "", false
	}
	u, ok := switches.LiveRole(stage.Proc(a.Proc), a.Params, a.HomeAngle, int(a.HomeRoom), switches.RoleDeath)
	if !ok {
		return "", false
	}
	if why, cand := t.switchWhy(u.No, u.Room); !cand || why != "" {
		return why, cand
	}
	if a.Keyed() {
		if ks, ok := t.keys[int(a.HomeRoom)]; !ok || !ks.Has(int(a.SetID)) {
			return fmt.Sprintf("its key, setID %d of room %d, does not sync (synctables.KillMask): its death would record it here (fopAcM_onActor, c_damagereaction.cpp:159)", a.SetID, a.HomeRoom), true
		}
	}
	if why := t.roomSettled(int(a.HomeRoom)); why != "" {
		return why, true
	}
	return t.linkFar(a.Pos), true
}

// deathRecheck returns what ApplyActors reads again just before a's
// light-shrink by the death switch rule: its death switch's byte, as
// deathWhy judged it; nil when a has none.
func (t *refreshTick) deathRecheck(a *actors.Actor) *DeathRecheck {
	u, ok := switches.LiveRole(stage.Proc(a.Proc), a.Params, a.HomeAngle, int(a.HomeRoom), switches.RoleDeath)
	if !ok {
		return nil
	}
	b, mask, ok := switchByteIn(t.s, u.No, u.Room)
	if !ok {
		return nil
	}
	return &DeathRecheck{No: u.No, Addr: b, Mask: mask}
}

// DeathRecheck is what ApplyActors reads again just before a light-shrink
// the death switch rule alone takes (ActorTarget.Death, design "Gates (all
// tiers)": each re-checked just before the write): the death switch, at
// Addr under Mask, still set, and Link (read again) more than 300 units
// from the carrier as Revalidate read it. The guard's Allow judges by the
// tick's snapshot, so these two are read here, as ApplyLocks reads a
// door's lock switch and Link.
type DeathRecheck struct {
	No   int
	Addr uint32
	Mask byte
}

// deathAllow reports whether a passes the death switch rule.
func (t *refreshTick) deathAllow(a *actors.Actor) bool {
	why, cand := t.deathWhy(a)
	return cand && why == ""
}

// newRefreshTick reads what o's tick of the refresh judges by. r.mu is
// held.
func (r *Replica) newRefreshTick(o *obs) *refreshTick {
	t := &refreshTick{landed: r.writer.Landed(), settle: r.cfg.RefreshSettle, now: time.Now(), s: o.s, boss: o.boss, flags: o.rooms.flags, flagsOK: o.rooms.ok}
	if t.settle == 0 {
		t.settle = RefreshSettle
	}
	if pos, ok, err := readLinkPos(r.m); err == nil {
		t.link, t.linkOK = pos, ok
	}
	f := &r.m6.rf
	f.landed = len(t.landed.At)
	if v := fmt.Sprintf("%s/%d", t.landed.Stage, t.landed.Table); v != f.visit {
		// Another stage visit: what was held was held for the last one.
		f.visit = v
		clear(f.lockHeld)
		clear(f.swHeld)
		f.lockLast, f.swLast = "", ""
	}
	return t
}

// planLocks plans this tick's lock clears from walk w (Tier 1), Link's
// demo gate being demo (why demoWhy when closed), and returns the plan's
// allow func for the guard (CombatTick.Locks). r.mu is held.
func (r *Replica) planLocks(t *refreshTick, w *actors.Walk, demo bool, demoWhy string) func(d *actors.Actor) bool {
	f := &r.m6.rf
	f.locks = nil
	planned := map[uint32]uint32{} // pid -> params
	for i := range w.Actors {
		a := &w.Actors[i]
		if _, ok := lockOff(a.Proc); !ok || f.lockEver[a.PID] {
			continue
		}
		u, ok := switches.LiveRole(stage.Proc(a.Proc), a.Params, a.HomeAngle, int(a.HomeRoom), switches.RoleKeyLock)
		if !ok || addr.ClassifySwitch(u.No) != addr.SwitchMemory {
			continue
		}
		why, cand := t.switchWhy(u.No, -1)
		if !cand {
			continue
		}
		if why == "" {
			why = doorRoomsWhy(t, a)
		}
		if why == "" {
			why = lockEligible(a, r.cfg.Claimed)
		}
		if why == "" {
			lw, gone, err := lockState(r.m, a)
			switch {
			case err != nil:
				lw = "its lock is unreadable: " + err.Error()
			case gone:
				// Not stale: setKey built it open, or the game opened it.
				delete(f.lockHeld, a.PID)
				continue
			}
			why = lw
		}
		if why == "" {
			why = t.linkFar(a.Pos)
		}
		if why == "" && !demo {
			why = demoWhy
		}
		if why != "" {
			f.holdLock(a.PID, why)
			continue
		}
		delete(f.lockHeld, a.PID)
		planned[a.PID] = a.Params
		f.locks = append(f.locks, LockTarget{Door: *a, No: u.No})
	}
	if len(planned) == 0 {
		return nil
	}
	return func(d *actors.Actor) bool {
		p, ok := planned[d.PID]
		return ok && p == d.Params
	}
}

// doorRoomsWhy returns why neither of door a's rooms is loaded and
// settled, or "". A door is placed in the stage file, so its home room is
// -1 (the stage's DOOR and TGDR entries); its rooms are home.angle.x's
// front and back (getFRoomNo & 0x3F, getBRoomNo >> 6 & 0x3F,
// d_door.cpp:15-61). Room 0x3F is no room here.
func doorRoomsWhy(t *refreshTick, a *actors.Actor) string {
	x := int(uint16(a.HomeAngle[0]))
	why := ""
	for _, room := range []int{x & 0x3F, x >> 6 & 0x3F} {
		if room == 0x3F {
			continue
		}
		if w := t.roomSettled(room); w == "" {
			return ""
		} else if why == "" {
			why = w
		}
	}
	if why == "" {
		why = "neither of its rooms is one (0x3F)"
	}
	return why
}
