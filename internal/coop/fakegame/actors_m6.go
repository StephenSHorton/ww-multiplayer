package fakegame

import (
	"errors"
	"fmt"
	"sync"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/enemies"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// The enemies M6's kill sync acts on (§7.2): the actor queue the tool
// walks, placed enemies created with their room's actors and deleted with
// them, the zone actor bits that keep a dead keyed enemy from being created
// again, and enemy_ice's light-arrow death, which the tool's one-byte
// timer write starts. Its rules each copy the decomp:
//
//   - g_fopAcTg_Queue (sym:14653) is a node_list_class {head, tail, size}
//     of each actor's actor_tag at actor +0xC4; a queued tag's mpData is
//     the queue (cLs_Addition, c_list.cpp:40), its mbIsUse 1 (cTg_Addition,
//     c_tag.cpp:74-80) and its mpTagData the actor (cTg_Create,
//     c_tag.cpp:89-92). An actor joins the queue as its create starts,
//     before its class's own create (fopAc_Create, f_op_actor.cpp:259-263),
//     and leaves it when deleted;
//   - each process takes the next process ID, a counter that starts at 1
//     and only counts up (fpcBs_MakeOfId, f_pc_base.cpp:36-40), and an
//     actor's actor_type is g_fopAc_type, which the first actor's create
//     gives a value (f_pc_base.cpp:28-33);
//   - mInitState goes from 0 to 2 as the actor reaches the execute queues
//     (fpcEx_ToLineQ, f_pc_executor.cpp:49) over the fake's two frames; a
//     create that fails (cPhs_ERROR, as a Kargaroc's can, d_a_bb.cpp:
//     2700-2702) leaves the queue without ever executing;
//   - the class's create points enemyice.mpActor at the actor (for example
//     d_a_bk.cpp:5270), and enemy_ice's first call moves its mMode from 0
//     to 1 (c_damagereaction.cpp:178-198);
//   - dStage_actorInit creates a room's placed entries as its actors are
//     created, skipping a keyed one whose zone bit is set
//     (dComIfGs_isActor, d_stage.cpp:1688-1708); a room hidden deletes
//     them without recording deaths (d_s_room.cpp:61-84);
//   - an executed frame of an actor whose shrink timer is nonzero counts
//     it up, and past 90 deletes the actor, records it dead (fopAcM_onActor)
//     and sets health -0x80 (enemy_ice, c_damagereaction.cpp:117-171). It
//     executes only on a frame with no event and no pause menu
//     (actorsMove), so the shrink waits through them;
//   - an onActor that would assert (a keyed actor whose room has no zone,
//     d_save.cpp:1676-1679) halts the game: the error is the frame's.

// EnemyHeap is where the fake allocates its actors: on the console the
// game takes them from a heap; any MEM1 range clear of the game's statics
// and the fake's other inventions serves. enemyBlock is each actor's
// block, room for the largest K1 member read (Moblin's enemyice at
// 0x2A54, d_a_mo2.h:175).
const (
	EnemyHeap  uint32 = 0x80B00000
	enemyBlock        = 0x3000
	enemySlots        = 128
	// FakeActorType is the g_fopAc_type the fake's actors carry.
	FakeActorType uint32 = 0x09130001
)

// The fields of the K1 classes the fake keeps besides enemyice: the
// weapon a carrier holds (Bokoblin m1200, d_a_bk.h:112; Stalfos
// mHeldWeaponEntityId, d_a_st.h:103; Darknut mBokoPcId, d_a_tn.h:122) and
// the mode a ReDead and a Floormaster keep (mMode, d_a_rd.h:137,
// d_a_fm.h:130).
//
// A carrier's own record that it holds the weapon (Bokoblin int m0B30,
// d_a_bk.h:74; Stalfos s8 m1DD0, d_a_st.h:104) is kept as the game keeps
// it while held (1 and 10), and the BOKO's u8 mNowMode
// (addr.BokoOffNowMode) as ENEMY_CARRY, PLAYER_CARRY or MOVE.
var (
	weaponField = map[stagedata.Proc]uint32{stagedata.ProcBK: 0x1200, stagedata.ProcST: 0x1DCC, stagedata.ProcTN: 0x1410}
	modeField   = map[stagedata.Proc]uint32{stagedata.ProcRD: 0x2AC, stagedata.ProcFM: 0x2AC}
	holdField   = map[stagedata.Proc]struct {
		off  uint32
		s8   bool
		held uint32
	}{stagedata.ProcBK: {0xB30, false, 1}, stagedata.ProcST: {0x1DD0, true, 10}}
)

// procBoko is fpcNm_BOKO_e (f_pc_name.h:482), a carried weapon.
const procBoko = 0x1D5

// Enemy is a placed enemy: an ACTR entry of a room of a stage.
type Enemy struct {
	Stage  string
	Room   int
	SetID  uint16 // 0xFFFF: unkeyed
	Proc   stagedata.Proc
	Params uint32
	Health int8 // at creation; 0 is 4
	// Boss gives it fopAcStts_BOSS_e as its create does for some params
	// (enemies.Class.IsBoss).
	Boss bool
	// Weapon creates a BOKO it holds, with CARRY set, for a class that has
	// a weapon field (fopAcM_setCarryNow, d_a_bk.cpp:4220-4224).
	Weapon bool
	// Mode is a ReDead's or Floormaster's mMode as it is created.
	Mode int32
	// FailCreate makes its create fail: it is queued, then gone without
	// having executed.
	FailCreate bool
}

// liveActor is an actor in the fake's queue.
type liveActor struct {
	at      uint32
	pid     uint32
	placed  int // index into Enemies.placed; -1 for a weapon or a stand-in
	proc    stagedata.Proc
	ice     uint32 // enemyice offset, 0 for none
	room    int
	initSt  int8
	dying   int // frames of a ReDead's death left (Kill), 0 if not dying
	carrier uint32
}

// Enemies is the placed enemies of the stages a test loads, run as a
// FakeGame Actor (FrameWith, RunWith): its execute creates and deletes
// them with their rooms, runs their enemy_ice and keeps the queue.
type Enemies struct {
	mu     sync.Mutex
	placed []Enemy
	live   []*liveActor // queue order
	free   []uint32     // freed blocks, reused first (FreeAndReuse)
	next   uint32       // the next fresh block
	stage  string
	loads  uint64                    // the FakeGame's stage loads as the enemies were created
	scene  [addr.RoomCount]roomScene // the room scene each room's enemies were created in
	shrunk []uint32                  // pids the shrink deleted
	killed []uint32                  // pids their own death deleted (Kill)
	born   map[uint32]int            // every placed enemy's pid, ever: its index in placed
	// carriers maps every weapon's pid, ever, to its carrier's.
	carriers map[uint32]uint32
}

// PlaceEnemies returns es as the placed enemies of their stages' rooms.
func PlaceEnemies(es ...Enemy) *Enemies {
	return &Enemies{placed: append([]Enemy(nil), es...), next: EnemyHeap, born: map[uint32]int{}, carriers: map[uint32]uint32{}}
}

// CarrierOf returns the pid of the carrier weapon pid was created for,
// live or gone.
func (e *Enemies) CarrierOf(pid uint32) (uint32, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.carriers[pid]
	return c, ok
}

// setHold writes a carrier's record that it holds its weapon.
func (g *FakeGame) setHold(at, off uint32, s8 bool, v uint32) {
	if s8 {
		g.st8(at+off, byte(v))
	} else {
		g.st32(at+off, v)
	}
}

// weaponOf finds carrier pid's weapon. e.mu is held.
func (e *Enemies) weaponOf(pid uint32) (carrier, weapon *liveActor) {
	for _, a := range e.live {
		if a.pid == pid {
			carrier = a
		}
	}
	if carrier == nil {
		return nil, nil
	}
	for _, a := range e.live {
		if a.carrier == pid {
			weapon = a
		}
	}
	return carrier, weapon
}

// DropWeapon is carrier pid losing its weapon to a hit, as yari_off_check
// does (d_a_bk.cpp:100-137): the weapon's CARRY is cancelled and it flies
// (Mode_MOVE_e), the carrier's record that it holds one is zeroed, and its
// weapon field keeps the weapon's pid.
func (e *Enemies) DropWeapon(g *FakeGame, pid uint32) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	c, w := e.weaponOf(pid)
	if c == nil || w == nil {
		return ErrNoActor
	}
	g.st32(w.at+addr.ActorOffStatus, g.ld32(w.at+addr.ActorOffStatus)&^addr.ActorStatusCarry)
	g.st8(w.at+addr.BokoOffNowMode, 1) // Mode_MOVE_e (d_a_boko.h:40)
	if h, ok := holdField[c.proc]; ok {
		g.setHold(c.at, h.off, h.s8, 0)
	}
	return nil
}

// GrabWeapon is Link picking up carrier pid's dropped weapon: CARRY set
// and Mode_PLAYER_CARRY_e (d_a_player_grab.inc:330-339).
func (e *Enemies) GrabWeapon(g *FakeGame, pid uint32) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	_, w := e.weaponOf(pid)
	if w == nil {
		return ErrNoActor
	}
	g.st32(w.at+addr.ActorOffStatus, g.ld32(w.at+addr.ActorOffStatus)|addr.ActorStatusCarry)
	g.st8(w.at+addr.BokoOffNowMode, addr.BokoModePlayerCarry)
	return nil
}

// TakeWeapon is carrier pid picking its dropped weapon up again
// (wepon_search, d_a_bk.cpp:2821-2833): its record set, the weapon's
// CARRY set, and the weapon in Mode_ENEMY_CARRY_e.
func (e *Enemies) TakeWeapon(g *FakeGame, pid uint32) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	c, w := e.weaponOf(pid)
	if c == nil || w == nil {
		return ErrNoActor
	}
	g.st32(w.at+addr.ActorOffStatus, g.ld32(w.at+addr.ActorOffStatus)|addr.ActorStatusCarry)
	g.st8(w.at+addr.BokoOffNowMode, addr.BokoModeEnemyCarry)
	if h, ok := holdField[c.proc]; ok {
		g.setHold(c.at, h.off, h.s8, 2)
	}
	return nil
}

// Born returns the placed enemy whose actor had process ID pid, live or
// gone, and its index in PlaceEnemies' list.
func (e *Enemies) Born(pid uint32) (Enemy, int, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	i, ok := e.born[pid]
	if !ok {
		return Enemy{}, -1, false
	}
	return e.placed[i], i, true
}

// LiveEnemy is a live actor of the fake's queue, as a test looks it up.
type LiveEnemy struct {
	Addr, PID uint32
	Placed    int // index of its Enemy in PlaceEnemies' list; -1 for a weapon
	Proc      stagedata.Proc
	Room      int
	InitState int8
	Carrier   uint32 // a weapon's carrier's pid
}

// Live lists the actors in the queue now.
func (e *Enemies) Live() []LiveEnemy {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]LiveEnemy, 0, len(e.live))
	for _, a := range e.live {
		out = append(out, LiveEnemy{Addr: a.at, PID: a.pid, Placed: a.placed, Proc: a.proc, Room: a.room, InitState: a.initSt, Carrier: a.carrier})
	}
	return out
}

// Of returns the live actor of placed enemy i, if it is in the queue.
func (e *Enemies) Of(i int) (LiveEnemy, bool) {
	for _, l := range e.Live() {
		if l.Placed == i {
			return l, true
		}
	}
	return LiveEnemy{}, false
}

// WeaponOf returns the weapon placed enemy i's carrier holds or held, if
// it is in the queue.
func (e *Enemies) WeaponOf(i int) (LiveEnemy, bool) {
	c, ok := e.Of(i)
	if !ok {
		return LiveEnemy{}, false
	}
	for _, l := range e.Live() {
		if l.Carrier == c.PID {
			return l, true
		}
	}
	return LiveEnemy{}, false
}

// Shrunk lists the pids the light-arrow death deleted, in order.
func (e *Enemies) Shrunk() []uint32 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]uint32(nil), e.shrunk...)
}

// Killed lists the pids their own death (Kill) deleted, in order.
func (e *Enemies) Killed() []uint32 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]uint32(nil), e.killed...)
}

// ErrNoActor is Kill's and FreeAndReuse's error for a pid not in the
// queue.
var ErrNoActor = errors.New("fakegame: no such actor")

// Kill is pid's own death, as its class's death code makes it: a ReDead
// goes to its death mode and records its death on every frame for a while
// before it is deleted (d_a_rd.cpp:836-853), a Kargaroc is deleted without
// recording it (d_a_bb.cpp:1969-1972), any other is recorded and deleted at
// once. An onActor that asserts is returned, the game halted.
func (e *Enemies) Kill(g *FakeGame, pid uint32) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	i := e.find(pid)
	if i < 0 || e.live[i].placed < 0 {
		return ErrNoActor
	}
	a := e.live[i]
	g.st8(a.at+addr.ActorOffHealth, 0)
	switch a.proc {
	case stagedata.ProcRD:
		g.st32(a.at+modeField[a.proc], 3) // MODE_DEATH (d_a_rd.h:27)
		a.dying = 30
		return nil
	case stagedata.ProcBB:
	default:
		if err := e.record(g, a); err != nil {
			return err
		}
	}
	e.killed = append(e.killed, a.pid)
	e.remove(g, i)
	e.writeQueue(g)
	return nil
}

// FreeAndReuse deletes pid and creates in its block, at once and in its
// place in the queue, an actor of proc with a new process ID and its own
// fields: what the heap does when the game frees an actor and the next
// create takes the same block. It returns the new pid.
func (e *Enemies) FreeAndReuse(g *FakeGame, pid uint32, proc stagedata.Proc) (uint32, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	i := e.find(pid)
	if i < 0 {
		return 0, ErrNoActor
	}
	old := e.live[i]
	g.store(old.at, make([]byte, enemyBlock))
	a := &liveActor{at: old.at, pid: g.newPID(), placed: -1, proc: proc, room: old.room, initSt: 2}
	e.live[i] = a
	g.layActor(a, Enemy{Stage: e.stage, Room: old.room, SetID: addr.ActorSetIDNone, Proc: proc, Health: 1}, 0)
	e.writeQueue(g)
	return a.pid, nil
}

// SetLinkDemo sets Link's mDemo.mDemoType (daPy_py_c +0x304,
// d_a_player.h:499): nonzero while a player demo runs.
func (g *FakeGame) SetLinkDemo(t uint16) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.st16(PlayerActor+addr.PlayerOffDemoType, t)
}

func (e *Enemies) execute(g *FakeGame) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	stage := ""
	if g.playing {
		stage = g.curStage()
	}
	changed := false
	if stage != e.stage || g.loads != e.loads {
		// Another stage, or the same one loaded again: the play scene's
		// rooms, and their actors, are all new.
		for len(e.live) > 0 {
			e.remove(g, 0)
		}
		e.stage, e.loads, e.scene, changed = stage, g.loads, [addr.RoomCount]roomScene{}, true
	}
	for room := range addr.RoomCount {
		proc, shown := g.sceneOf(room)
		switch {
		case stage == "" || !shown:
			if e.scene[room].on {
				changed = e.dropRoom(g, room) || changed
				e.scene[room] = roomScene{}
			}
		case e.scene[room] != (roomScene{true, proc}):
			e.dropRoom(g, room)
			e.scene[room] = roomScene{true, proc}
			for i, p := range e.placed {
				if p.Stage == stage && p.Room == room && !g.isActor(int(p.SetID), room) {
					e.create(g, i)
				}
			}
			changed = true
		}
	}
	moves := g.ld8(addr.EvtMode) == 0 && g.ld8(addr.MenuPause) == 0
	for i := 0; i < len(e.live); i++ {
		a := e.live[i]
		if a.initSt < 2 {
			// fopAc_Create's class create, then the execute queues.
			if a.initSt++; a.initSt == 2 {
				if a.placed >= 0 && e.placed[a.placed].FailCreate {
					e.remove(g, i)
					i--
					changed = true
					continue
				}
				if a.ice != 0 {
					g.st32(a.at+a.ice, a.at) // enemyice.mpActor
				}
			}
			g.st8(a.at+addr.ActorOffInitState, byte(a.initSt))
			continue
		}
		if !moves || a.ice == 0 {
			continue
		}
		if a.dying > 0 {
			if err := e.record(g, a); err != nil {
				return err
			}
			if a.dying--; a.dying == 0 {
				e.killed = append(e.killed, a.pid)
				e.remove(g, i)
				i--
				changed = true
			}
			continue
		}
		if m, ok := modeField[a.proc]; ok && a.proc == stagedata.ProcRD && int32(g.ld32(a.at+m)) == 0xA {
			continue // MODE_SW_WAIT returns before enemy_ice (d_a_rd.cpp:1621-1630)
		}
		ice := a.at + a.ice
		switch timer := int8(g.ld8(ice + addr.EnemyIceOffTimer)); {
		case timer != 0:
			timer++
			g.st8(ice+addr.EnemyIceOffTimer, byte(timer))
			if timer > 90 {
				g.st8(a.at+addr.ActorOffHealth, 0x80)
				if err := e.record(g, a); err != nil {
					return err
				}
				e.shrunk = append(e.shrunk, a.pid)
				e.remove(g, i)
				i--
				changed = true
			}
		case g.ld8(ice+addr.EnemyIceOffMode) == 0:
			g.st8(ice+addr.EnemyIceOffMode, 1)
		}
	}
	if changed {
		e.writeQueue(g)
	}
	return nil
}

// record is fopAcM_onActor for a: its setID in its home room's zone.
func (e *Enemies) record(g *FakeGame, a *liveActor) error {
	p := e.placed[a.placed]
	if err := g.onActor(int(p.SetID), p.Room); err != nil {
		return fmt.Errorf("the death of %s r%d setID 0x%04X (pid %d): %w", p.Stage, p.Room, p.SetID, a.pid, err)
	}
	return nil
}

// dropRoom deletes room's actors, as hiding or unloading it does, without
// recording any death.
func (e *Enemies) dropRoom(g *FakeGame, room int) bool {
	dropped := false
	for i := 0; i < len(e.live); i++ {
		if e.live[i].room == room {
			e.remove(g, i)
			i--
			dropped = true
		}
	}
	return dropped
}

// create queues placed enemy i, and its weapon.
func (e *Enemies) create(g *FakeGame, i int) {
	p := e.placed[i]
	c, _ := enemies.ClassOf(p.Proc)
	a := &liveActor{at: e.alloc(), pid: g.newPID(), placed: i, proc: p.Proc, ice: c.Ice, room: p.Room}
	e.born[a.pid] = i
	g.store(a.at, make([]byte, enemyBlock)) // the class's create starts from a cleared block (inferred)
	e.live = append(e.live, a)
	var weapon uint32
	if off, ok := weaponField[p.Proc]; ok && p.Weapon {
		w := &liveActor{at: e.alloc(), pid: g.newPID(), placed: -1, proc: procBoko, room: p.Room, initSt: 2, carrier: a.pid}
		g.store(w.at, make([]byte, enemyBlock))
		e.live = append(e.live, w)
		g.layActor(w, Enemy{Stage: p.Stage, Room: p.Room, SetID: addr.ActorSetIDNone, Proc: procBoko, Health: 1}, addr.ActorStatusCarry)
		g.st8(w.at+addr.BokoOffNowMode, addr.BokoModeEnemyCarry)
		weapon = w.pid
		e.carriers[w.pid] = a.pid
		defer g.st32(a.at+off, weapon)
		if h, ok := holdField[p.Proc]; ok {
			defer g.setHold(a.at, h.off, h.s8, h.held)
		}
	}
	var status uint32
	if p.Boss {
		status |= addr.ActorStatusBoss
	}
	g.layActor(a, p, status)
	if off, ok := modeField[p.Proc]; ok {
		g.st32(a.at+off, uint32(p.Mode))
	}
}

// layActor writes a's fopAc_ac_c fields for placed entry p.
func (g *FakeGame) layActor(a *liveActor, p Enemy, status uint32) {
	if g.ld32(addr.FopAcType) == 0 {
		g.st32(addr.FopAcType, FakeActorType)
	}
	hp := p.Health
	if hp == 0 {
		hp = 4
	}
	g.st32(a.at+addr.ActorOffBsPcID, a.pid)
	g.st16(a.at+addr.ActorOffProcName, uint16(a.proc))
	g.st8(a.at+addr.ActorOffInitState, byte(a.initSt))
	g.st32(a.at+addr.ActorOffParameters, p.Params)
	g.st32(a.at+addr.ActorOffActorType, g.ld32(addr.FopAcType))
	g.st16(a.at+addr.ActorOffSetID, p.SetID)
	group := byte(2) // fopAc_ENEMY_e
	if a.proc == procBoko {
		group = 0
	}
	g.st8(a.at+addr.ActorOffGroup, group)
	g.st32(a.at+addr.ActorOffStatus, status)
	g.st32(a.at+addr.ActorOffParentID, 0xFFFFFFFF)
	g.st8(a.at+addr.ActorOffHomeRoomNo, byte(p.Room))
	g.st8(a.at+addr.ActorOffMaxHealth, byte(hp))
	g.st8(a.at+addr.ActorOffHealth, byte(hp))
}

// alloc takes a block: a freed one first.
func (e *Enemies) alloc() uint32 {
	if n := len(e.free); n > 0 {
		at := e.free[n-1]
		e.free = e.free[:n-1]
		return at
	}
	at := e.next
	if at >= EnemyHeap+enemySlots*enemyBlock {
		panic("fakegame: out of actor blocks")
	}
	e.next += enemyBlock
	return at
}

// remove takes live actor i out of the queue and frees its block, its
// bytes left as they were (the heap does not clear a freed block).
func (e *Enemies) remove(g *FakeGame, i int) {
	a := e.live[i]
	g.st8(a.at+addr.ActorOffActorTag+addr.TagOffUse, 0)
	g.st32(a.at+addr.ActorOffActorTag+addr.TagOffData, 0)
	g.st8(a.at+addr.ActorOffInitState, 3) // leaving the queues (f_pc_deletor.cpp:89)
	e.live = append(e.live[:i], e.live[i+1:]...)
	e.free = append(e.free, a.at)
}

func (e *Enemies) find(pid uint32) int {
	for i, a := range e.live {
		if a.pid == pid {
			return i
		}
	}
	return -1
}

// writeQueue links the live actors into g_fopAcTg_Queue in order.
func (e *Enemies) writeQueue(g *FakeGame) {
	var head, tail uint32
	for i, a := range e.live {
		node := a.at + addr.ActorOffActorTag
		var prev, next uint32
		if i > 0 {
			prev = e.live[i-1].at + addr.ActorOffActorTag
		}
		if i+1 < len(e.live) {
			next = e.live[i+1].at + addr.ActorOffActorTag
		}
		g.st32(node+addr.TagOffPrev, prev)
		g.st32(node+addr.TagOffData, addr.ActorQueue)
		g.st32(node+addr.TagOffNext, next)
		g.st32(node+addr.TagOffTagData, a.at)
		g.st8(node+addr.TagOffUse, 1)
		if i == 0 {
			head = node
		}
		tail = node
	}
	g.st32(addr.ActorQueue+addr.ActorQueueOffHead, head)
	g.st32(addr.ActorQueue+addr.ActorQueueOffTail, tail)
	g.st32(addr.ActorQueue+addr.ActorQueueOffSize, uint32(len(e.live)))
}

// newPID is fpcBs_MakeOfId: the counter's value, then the counter one up.
// g.mu is held.
func (g *FakeGame) newPID() uint32 {
	pid := g.ld32(addr.ProcessIDCounter)
	if pid == 0 {
		pid = 1
	}
	g.st32(addr.ProcessIDCounter, pid+1)
	return pid
}

// sceneOf is roomScene with g.mu held.
func (g *FakeGame) sceneOf(room int) (uint32, bool) {
	if !g.playing || g.zoneNo(room) < 0 {
		return 0, false
	}
	a, _ := addr.RoomStatusAddr(room)
	if g.ld8(a+roomStatusOffFlags)&roomHidden != 0 {
		return 0, false
	}
	return g.ld32(a + roomStatusOffProcID), true
}

// isActor is dSv_info_c::isActor (d_save.cpp:1684-1701) for a room whose
// zone exists, with g.mu held.
func (g *FakeGame) isActor(id, room int) bool {
	bit, err := addr.ZoneActorBit(id)
	if err != nil || g.zoneNo(room) < 0 {
		return false
	}
	return g.ld8(zoneAddr(int(g.zoneNo(room)))+uint32(bit.Off))&bit.Mask != 0
}

// roomScene is a room's scene as its enemies were created: shown, with
// its process ID (mStatus[room].mProcID), which a reload changes.
type roomScene struct {
	on   bool
	proc uint32
}
