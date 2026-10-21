package enemysync

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/write"
)

// The client mirror (design "client_go_side", generalised by
// enemy-mirror-general.json "identity".client_side, "slots" and
// "death_and_drops"): every tick it gates (contract every ContractEvery,
// the REL module linked with this build and its frame counter advancing,
// in play with no stage change pending, the same stage and room as the
// host's STATE, a STATE fresher than StaleAfter and built from this
// build's kind table), matches the host's enemies to this game's by Ident
// (keyed: proc and setID; unkeyed: proc and pkey) over the kinds both
// sides enable, picks MaxSlots of them (selectSlots), smooths each one and
// writes the annex and the block in one write (mailbox.WriteEnemyBlock),
// then reads the REL's tail. A gate failure writes a count-0 block (the
// REL releases) and asks the caller to sleep GateSleep; Shutdown writes
// magic 0. A heartbeat regression (a save state loaded) starts a new
// epoch. Hits the REL flagged come back from Tick as HIT messages.
//
// A block slot is no longer the STATE's slot of the same index: the
// client keeps blk[j], the Ident in block slot j, and every per-slot thing
// (the REL's hit entries and status, the drained hit_seq, the pending
// clear) goes through it. A copy that dies stays in its slot from its
// first dying sample until the REL reports it dead (or gone), so a quiet
// death is never cut short by a reshuffle.
const (
	ContractEvery = 2 * time.Second
	RELEvery      = 250 * time.Millisecond
	MatchEvery    = 2 * time.Second
	StaleAfter    = 500 * time.Millisecond
	GateSleep     = 250 * time.Millisecond
	LogEvery      = time.Second
	// ackWithin is how long after arming the REL's ack_seq must follow
	// before "REL not consuming" is logged (once).
	ackWithin = 250 * time.Millisecond
)

// The selection's hysteresis (design slots.client_selectSlots): an
// adopted copy stays while its host enemy is within KeepR of this game's
// Link, engaged (WFlagEngaged) or adopted less than MinHold ago; a
// newcomer takes a free slot, or evicts a copy neither engaged nor
// younger than MinHold only when it is nearer than EvictRatio of that
// copy's distance and within EnterR (within KeepR too).
const (
	KeepR      = 1500.0
	EnterR     = 1200.0
	EvictRatio = 0.7
	MinHold    = 2 * time.Second
	// PinMax bounds a dying slot's pin: a copy whose REL status never
	// reports it dead or gone (an epoch change made the REL forget it, a
	// legacy death latched past the status) frees its slot after it.
	PinMax = 10 * time.Second
)

// Mirror is the client's state machine; Tick drives it.
type Mirror struct {
	m     mem.Mem
	rep   log.Reporter
	brk   *write.Breaker
	raw   bool
	start time.Time

	// What the read loop hands over.
	mu       sync.Mutex
	latest   *State
	latestAt time.Time
	acks     []*Ack

	epoch uint32
	seq   uint32
	// hostEpoch is the epoch of the last STATE mirrored (haveHost once
	// one was): a change is a restarted host whose host_ms began again.
	hostEpoch uint32
	haveHost  bool

	// Gates.
	contractOK, contractRan bool
	contractNext            time.Time
	contractWhy             string
	relOK                   bool
	relNext                 time.Time
	relFrames               uint32
	relWhy                  string
	hbOK                    bool
	hb                      uint32
	hbAt                    time.Time

	// Kinds is this game's enabled set (WW_ENEMY_MIRROR_KINDS), Quiet the
	// kinds whose copies die quietly (WW_ENEMY_QUIET_DEATH), Select the
	// selection (WW_ENEMY_SELECT); NewMirror reads them, tests set them.
	// CoopSwitchSync, when it reports true, means co-op syncs switches for
	// this game: a quiet death's switch then arrives through it and the
	// mirror does not write it (nil: the mirror writes it).
	Kinds          KindSet
	Quiet          KindSet
	Select         SelectMode
	CoopSwitchSync func() bool
	// Annex is the annex overrides every block carries (WW_ENEMY_ANNEX:
	// quiet frames, hook_disable; its Aux is ignored, the slots' own go in).
	Annex mailbox.EnemyAnnex
	// Claims, when set, gets every local pid the mirror claims each tick
	// (Env.Claims).
	Claims *ClaimSet

	// Matching: this game's enemies of (stage, room) by match key.
	local      map[Ident]localEnemy
	localStage string
	localRoom  int8
	matchNext  time.Time
	lastKeys   []Ident

	// Per room: the unkeyed Idents the host showed dying or as a
	// tombstone (caught up when a slot frees), the Idents whose copy the
	// REL reported dead here, and the keyed setIDs whose zone bit waits
	// for the host's dead words.
	hostDead    map[Ident]bool
	deadHere    map[Ident]bool
	zonePending map[uint16]bool
	warned      map[string]bool

	blk [MaxSlots]blkSlot
	sm  map[Ident]*Smoother

	armed    bool
	armedAt  time.Time
	ackSeen  bool
	relQuiet bool
	gateWhy  string
	logNext  time.Time
	hitSeq   uint16           // the last HIT seq sent: this mirror's own counter (readHit)
	lastHits [MaxSlots]uint16 // the last hit_seq drained per block slot
	// lastStatus is each block slot's (state, last_err) at the last tick:
	// the REL's last_err is sticky, so only a change forces a walk.
	lastStatus [MaxSlots][2]uint8
	Stats      MirrorStats

	// Remote, when set, is this joiner's remote-Link side (players.go):
	// OnEnemy hands it the PHITs and RunMirror ticks it after the mirror.
	Remote *RemoteClient
}

// localEnemy is one of this game's enemies the match walk took.
type localEnemy struct {
	Enemy
	Pos [3]float32 // current.pos at the walk
}

// blkSlot is block slot j's assignment.
type blkSlot struct {
	selSlot
	pid     uint32 // the local actor
	local   Ident  // its Ident (its own pkey, which the block carries)
	kind    KindID
	room    int8 // its home room
	addr    uint32
	quiet   bool // written with SF2_QUIET
	catchUp bool // assigned while already dying: never mirrored alive, always quiet
	sw      uint8
	swOK    bool              // sw was read (at the pin)
	last    mailbox.EnemySlot // the last slot written, which a pinned slot the host no longer lists keeps
	lastAux [4]int16          // and its annex aux
	haveLst bool
}

// MirrorStats are counters a probe or test reads.
type MirrorStats struct {
	Writes, Epochs, Hits uint64
	LastStatus           mailbox.EnemyStatus
	LastWriteMS          uint32
	// Deaths counts the copies the REL reported dead (quiet deaths),
	// ZoneBits the zone bits and Switches the death switches written.
	Deaths, ZoneBits, Switches uint64
}

// NewMirror returns a mirror over m with its breaker; raw bypasses the
// smoother and sets the block's raw flag (WW_ENEMY_RAW=1). The kinds,
// quiet kinds and selection come from WW_ENEMY_MIRROR_KINDS,
// WW_ENEMY_QUIET_DEATH and WW_ENEMY_SELECT (a value that does not parse
// is logged and the default taken).
func NewMirror(m mem.Mem, rep log.Reporter, brk *write.Breaker, raw bool, start time.Time) *Mirror {
	c := &Mirror{m: m, rep: rep, brk: brk, raw: raw, start: start, epoch: 1, local: map[Ident]localEnemy{}, sm: map[Ident]*Smoother{},
		hostDead: map[Ident]bool{}, deadHere: map[Ident]bool{}, zonePending: map[uint16]bool{}, warned: map[string]bool{}}
	var err error
	if c.Kinds, err = MirrorKinds(); err != nil {
		log.Logf(rep, log.Warn, "enemy client: %v; mirroring %s", err, c.Kinds)
	}
	if c.Quiet, err = QuietKinds(); err != nil {
		log.Logf(rep, log.Warn, "enemy client: %v; quiet deaths for %s", err, c.Quiet)
	}
	if c.Select, err = SelectFromEnv(); err != nil {
		log.Logf(rep, log.Warn, "enemy client: %v; selecting by %s", err, c.Select)
	}
	if c.Annex, err = AnnexFromEnv(); err != nil {
		log.Logf(rep, log.Warn, "enemy client: %v; no annex overrides", err)
	}
	return c
}

// Epoch is the current epoch.
func (c *Mirror) Epoch() uint32 { return c.epoch }

// OnEnemy takes one MsgEnemy payload from the read loop: a STATE becomes
// the latest; an ACK is kept for the next Tick's log.
func (c *Mirror) OnEnemy(data []byte, now time.Time) {
	sub, err := Sub(data)
	if err != nil {
		return
	}
	switch sub {
	case SubState:
		st, err := DecodeState(data)
		if err != nil {
			return
		}
		c.mu.Lock()
		c.latest, c.latestAt = st, now
		c.mu.Unlock()
	case SubAck:
		a, err := DecodeAck(data)
		if err != nil {
			return
		}
		c.mu.Lock()
		c.acks = append(c.acks, a)
		c.mu.Unlock()
	case SubPHit:
		if c.Remote == nil {
			return
		}
		if p, err := DecodePHit(data); err == nil {
			c.Remote.OnPHit(p)
		}
	}
}

// RemoteGate is what RemoteClient.Tick takes from the mirror at the last
// Tick: the contract held and the REL was linked with this build, the
// latest host STATE's epoch, the heartbeat and the mirror's own epoch.
func (c *Mirror) RemoteGate() ClientGate {
	g := ClientGate{RELOK: c.contractOK && c.relOK, Frame: c.hb, Epoch: c.epoch}
	if st, _ := c.Latest(); st != nil {
		g.HostEpoch, g.HaveHost = st.Epoch, true
	}
	return g
}

// Latest returns the latest STATE and its arrival.
func (c *Mirror) Latest() (*State, time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.latest, c.latestAt
}

// checkContract runs the contract check every ContractEvery.
func (c *Mirror) checkContract(now time.Time) bool {
	if c.contractRan && now.Before(c.contractNext) {
		return c.contractOK
	}
	c.contractNext = now.Add(ContractEvery)
	k, err := wwapi.CheckContract(c.m, wwapi.ContractOptions{})
	if err != nil {
		if !c.contractRan {
			c.contractRan = true
			c.rep.Log(log.Warn, "enemy client: cannot read the game yet ("+err.Error()+")")
		}
		return c.contractOK
	}
	was, first := c.contractOK, !c.contractRan
	c.contractRan, c.contractOK = true, k.WritesAllowed
	switch {
	case !c.contractOK && k.Why != c.contractWhy:
		c.contractWhy = k.Why
		c.rep.Log(log.Err, "enemy client: this Dolphin's game is not this version's patch: "+k.Why+". "+wwapi.RepatchAdvice)
	case c.contractOK && (!was || first):
		c.contractWhy = ""
		c.rep.Log(log.OK, "enemy client: "+wwapi.ContractLine(k))
		if !first {
			c.newEpoch("contract regained")
		}
	}
	return c.contractOK
}

// relStill is checkREL's reason while the REL actor's frame counter
// stands still.
const relStill = "the REL module's actor is not running (its frame counter is still)"

// checkREL reads the REL words every RELEvery: linked with this build's
// id and its frame counter moving since the last read.
func (c *Mirror) checkREL(now time.Time) bool {
	if now.Before(c.relNext) {
		return c.relOK
	}
	c.relNext = now.Add(RELEvery)
	r, err := mailbox.ReadREL(c.m)
	if err != nil {
		return c.relOK
	}
	why := ""
	switch {
	case !r.Linked:
		why = "the REL module is not linked"
	case r.BuildID != inject.RELBuildID:
		why = fmt.Sprintf("the linked REL module is build %08X, this build's is %08X", r.BuildID, inject.RELBuildID)
	case r.Frames == c.relFrames:
		why = relStill
	}
	// A relink is the module gone or replaced, or its counter starting
	// over (a new link counts from 0). A counter that stood still and then
	// moves on (the pause menu, an event, a hit-stop, a slow host) is the
	// same link: no new epoch, which would release and re-adopt every copy.
	relinked := c.relWhy != "" && c.relWhy != relStill || r.Frames < c.relFrames
	c.relFrames = r.Frames
	ok := why == ""
	if ok != c.relOK || (why != "" && why != c.relWhy) {
		if ok {
			c.rep.Log(log.OK, fmt.Sprintf("enemy client: REL module linked (build %08X, frame %d)", r.BuildID, r.Frames))
			if c.relWhy != "" && relinked {
				c.newEpoch("REL module relinked")
			}
		} else {
			c.rep.Log(log.Warn, "enemy client: "+why+"; the mirror waits.")
		}
	}
	c.relOK, c.relWhy = ok, why
	return ok
}

// checkHeartbeat reads spawn_trigger and starts a new epoch when it went
// backwards or jumped further than the game can have run since the last
// read (a save state was loaded; write.BeatLead's rule).
func (c *Mirror) checkHeartbeat(now time.Time) error {
	hb, err := mem.ReadU32(c.m, mailbox.SpawnTrigger)
	if err != nil {
		return err
	}
	if c.hbOK {
		d := int32(hb - c.hb)
		if d < 0 {
			c.newEpoch("the heartbeat went backwards: a save state was loaded")
		} else if uint32(d) > write.BeatLead(now.Sub(c.hbAt)) {
			c.newEpoch(fmt.Sprintf("the heartbeat jumped %d frames: a save state was loaded", d))
		}
	}
	c.hb, c.hbAt, c.hbOK = hb, now, true
	return nil
}

// newEpoch starts a new epoch: the smoothers forget, the next block
// carries it, the REL forgets its slots (em_release_all with forget: the
// dying latches too), and so do the slots here. A save state loaded in
// the same room brings back the enemies that died here (their zone bits
// are the old RAM's): the dead-here set is dropped so the host's dead
// words catch them up again, the pins with it (their REL latches are gone,
// a status naming them never comes), and the next tick walks again (every
// pid may have changed).
func (c *Mirror) newEpoch(why string) {
	c.epoch++
	c.Stats.Epochs++
	for _, s := range c.sm {
		s.Reset()
	}
	// The REL renumbers its hit_seq from 1 on every link (enemy_mirror_init
	// zeroes the state) and a save state restores an older one: a drained
	// seq from before must not hide a new entry's.
	c.lastHits = [MaxSlots]uint16{}
	c.blk = [MaxSlots]blkSlot{}
	c.lastStatus = [MaxSlots][2]uint8{}
	clear(c.deadHere)
	c.matchNext = time.Time{}
	log.Logf(c.rep, log.Info, "enemy client: epoch %d (%s)", c.epoch, why)
}

// --- Selection ----------------------------------------------------------------

// matchKey is the key two games pair an enemy by (design
// identity.client_side): a keyed one by (proc, setID), its pkey left out
// (the REL compares the local actor's own, so keyed placements on two
// layers still pair, identity.layer), an unkeyed one by (proc, pkey).
func matchKey(id Ident) Ident {
	if id.Keyed() {
		return Ident{Proc: id.Proc, SetID: id.SetID}
	}
	return id
}

// selSlot is what the selection knows of a block slot: the match key in it
// (Used), when it took the slot, and whether it is pinned dying (rule 0:
// from its first dying sample until the REL reports it dead).
type selSlot struct {
	Key       Ident
	Used      bool
	AdoptedAt time.Time
	Pinned    bool
	PinnedAt  time.Time
}

// cand is one enemy the selection may give a slot: listed by the host and
// matched here, or a catch-up (a local enemy the host's dead words or
// tombstones say is dead). Dist is the 3D distance from this game's Link
// to the host's enemy (to the local actor for a catch-up), +Inf unknown.
// Late marks a tombstone (WFlagTomb) or a catch-up: an enemy already gone
// on the host, which SelectLegacy gives only a slot left free.
type cand struct {
	Key     Ident
	Dist    float64
	Dying   bool
	Late    bool
	Engaged bool
}

// selectSlots gives the MaxSlots block slots their enemies at now (design
// slots.client_selectSlots), from the current assignment cur:
//
//  0. A pinned (dying) slot stays, listed or not, until the caller unpins
//     it (the REL reported it dead or gone); a slot whose enemy turns
//     dying is pinned.
//  1. A dying candidate without a slot takes a free one, else evicts the
//     farthest copy rule 2 does not keep, else waits (its death persists
//     through the tombstone's TTL and the zone bits).
//  2. A copy stays while its enemy is within KeepR, engaged, or adopted
//     less than MinHold ago; one whose enemy is no longer offered frees
//     its slot.
//  3. Newcomers fill free slots by distance; one may evict the farthest
//     copy that is not engaged or younger than MinHold, within KeepR or
//     not, only when nearer than EvictRatio of its distance and within
//     EnterR. (Siren room 3: four dormant ceiling ChuChus about 1400 from
//     Link held every slot under a strict KeepR while an awake one at
//     about 750 ran its own AI on the client.)
//
// A locked slot (an unacked remote-Link apply names it, players.go
// LockedSlots) keeps its enemy and, once freed, is not given to another.
// SelectLegacy instead keeps the pinned slots and the first live cands in
// their given order (the host's lowest setIDs; a listed enemy that is
// dying but still live on the host keeps its place there), then gives the
// slots left free to the Late ones without a slot (tombstones, catch-ups),
// as the Moblin prototype did, with no hysteresis.
func selectSlots(cands []cand, cur [MaxSlots]selSlot, locked [MaxSlots]bool, now time.Time, mode SelectMode) [MaxSlots]selSlot {
	by := make(map[Ident]*cand, len(cands))
	for i := range cands {
		by[cands[i].Key] = &cands[i]
	}
	next := cur
	held := map[Ident]bool{}
	for j := range next {
		s := &next[j]
		if !s.Used {
			continue
		}
		k := by[s.Key]
		switch {
		case s.Pinned:
		case k == nil:
			*s = selSlot{}
			continue
		case k.Dying:
			s.Pinned, s.PinnedAt = true, now
		}
		held[s.Key] = true
	}
	free := func() int {
		for j := range next {
			if !next[j].Used && !locked[j] {
				return j
			}
		}
		return -1
	}
	place := func(j int, k *cand) {
		next[j] = selSlot{Key: k.Key, Used: true, AdoptedAt: now, Pinned: k.Dying}
		if k.Dying {
			next[j].PinnedAt = now
		}
		held[k.Key] = true
	}

	if mode == SelectLegacy {
		room := MaxSlots
		for j := range next {
			if next[j].Used && next[j].Pinned {
				room--
			}
		}
		// The first room listed candidates in the host's order are wanted,
		// dying ones included (the prototype mirrored the lowest four the
		// host listed, a Moblin in its death too); a Late one without a slot
		// (a tombstone, listed before every live one, or a catch-up) only
		// takes a slot left free, as the prototype added its catch-ups, and
		// never evicts a copy.
		want := map[Ident]bool{}
		var order, late []*cand
		for i := range cands {
			k := &cands[i]
			if held[k.Key] && slotOf(&next, k.Key).Pinned {
				continue
			}
			if k.Late && !held[k.Key] {
				late = append(late, k)
				continue
			}
			if len(order) < room {
				want[k.Key] = true
				order = append(order, k)
			}
		}
		order = append(order, late...)
		for j := range next {
			s := &next[j]
			if s.Used && !s.Pinned && !locked[j] && !want[s.Key] {
				delete(held, s.Key)
				*s = selSlot{}
			}
		}
		for _, k := range order {
			if held[k.Key] {
				continue
			}
			if j := free(); j >= 0 {
				place(j, k)
			}
		}
		return next
	}

	// hardKept: pinned, locked, unlisted, engaged or younger than MinHold;
	// no candidate evicts it. kept adds KeepR, which only a newcomer
	// EvictRatio nearer overrides (rule 3).
	hardKept := func(j int) bool {
		s := &next[j]
		if s.Pinned || locked[j] {
			return true
		}
		k := by[s.Key]
		return k == nil || k.Engaged || now.Sub(s.AdoptedAt) < MinHold
	}
	kept := func(j int) bool { return hardKept(j) || by[next[j].Key].Dist < KeepR }
	victim := func(keep func(int) bool) int {
		best := -1
		for j := range next {
			if !next[j].Used || keep(j) {
				continue
			}
			if best < 0 || by[next[j].Key].Dist > by[next[best].Key].Dist {
				best = j
			}
		}
		return best
	}
	var dying, newcomers []*cand
	for i := range cands {
		k := &cands[i]
		if held[k.Key] {
			continue
		}
		if k.Dying {
			dying = append(dying, k)
		} else {
			newcomers = append(newcomers, k)
		}
	}
	nearest := func(a, b *cand) int { return cmp.Or(cmp.Compare(a.Dist, b.Dist), compareIdent(a.Key, b.Key)) }
	slices.SortStableFunc(dying, nearest)
	slices.SortStableFunc(newcomers, nearest)
	for _, k := range dying {
		j := free()
		if j < 0 {
			j = victim(kept)
		}
		if j >= 0 {
			place(j, k)
		}
	}
	for _, k := range newcomers {
		if j := free(); j >= 0 {
			place(j, k)
			continue
		}
		if v := victim(hardKept); v >= 0 && k.Dist < EvictRatio*by[next[v].Key].Dist && k.Dist < EnterR {
			place(v, k)
		}
	}
	return next
}

// slotOf returns the used slot holding key in s (a zero selSlot if none).
func slotOf(s *[MaxSlots]selSlot, key Ident) selSlot {
	for j := range s {
		if s[j].Used && s[j].Key == key {
			return s[j]
		}
	}
	return selSlot{}
}

// --- Matching -----------------------------------------------------------------

// resetScope forgets what belongs to a stage and room: the slots, the
// host-dead and dead-here sets, the zone bits waiting and the walk (the
// next match walks whatever the host's list).
func (c *Mirror) resetScope() {
	c.blk = [MaxSlots]blkSlot{}
	c.lastHits = [MaxSlots]uint16{}
	c.lastStatus = [MaxSlots][2]uint8{}
	clear(c.hostDead)
	clear(c.deadHere)
	clear(c.zonePending)
	clear(c.sm)
	clear(c.local)
	c.lastKeys, c.matchNext = nil, time.Time{}
}

// match walks this game's enemies of (stage, room) when the scope changed,
// the host's list changed, MatchEvery passed, or the REL reported an
// unresolved slot. It keeps the ones Classify takes (initState 2, keyed or
// unkeyed per the kind's "+u", the kind's static gates), by match key; two
// with one key are both refused (neither game can tell them apart).
func (c *Mirror) match(now time.Time, scope Scope, st *State, force bool) error {
	keys := make([]Ident, len(st.Slots))
	for i := range st.Slots {
		keys[i] = matchKey(st.Slots[i].Ident())
	}
	slices.SortFunc(keys, compareIdent) // the distance order moves; the set is what counts
	if scope.Stage != c.localStage || scope.Room != c.localRoom {
		c.resetScope()
		force = true
	}
	if !force && slices.Equal(keys, c.lastKeys) && now.Before(c.matchNext) {
		return nil
	}
	c.matchNext = now.Add(MatchEvery)
	c.localStage, c.localRoom, c.lastKeys = scope.Stage, scope.Room, keys
	w, err := actors.ReadWith(c.m, 0, KindLayouts)
	if err != nil {
		return err
	}
	es, err := ClassifyWalk(c.m, &w, scope.Room, c.Kinds)
	if err != nil {
		return err
	}
	pos := make(map[uint32][3]float32, len(w.Actors))
	for i := range w.Actors {
		pos[w.Actors[i].PID] = w.Actors[i].Pos
	}
	local := make(map[Ident]localEnemy, len(es))
	dup := map[Ident]bool{}
	for _, e := range es {
		if e.Refused != "" {
			continue
		}
		k := matchKey(e.Ident)
		if _, ok := local[k]; ok {
			dup[k] = true
		}
		local[k] = localEnemy{Enemy: e, Pos: pos[e.PID]}
	}
	for k := range dup {
		delete(local, k)
		c.warnOnce("dup "+k.String(), fmt.Sprintf("enemy client: two of this game's enemies share %s; neither is mirrored", k))
	}
	if len(local) != len(c.local) {
		log.Logf(c.rep, log.Info, "enemy client: %d enemies (%s) of ours in %s room %d", len(local), c.Kinds, scope.Stage, scope.Room)
	}
	c.local = local
	return nil
}

func (c *Mirror) warnOnce(key, msg string) {
	if !c.warned[key] {
		c.warned[key] = true
		c.rep.Log(log.Warn, msg)
	}
}

// linkPos reads this game's Link position (false when unreadable).
func (c *Mirror) linkPos() ([3]float32, bool) {
	var p [3]float32
	link, err := mem.ReadU32(c.m, addr.PlayerPtr)
	if err != nil || !addr.InMEM1(link) {
		return p, false
	}
	b, err := c.m.Read(link+addr.ActorOffCurPos, 12)
	if err != nil || len(b) != 12 {
		return p, false
	}
	for i := range p {
		p[i] = mem.F32BE(b[4*i:])
	}
	return p, finiteOK(p[0], p[1], p[2])
}

// offer is what the candidates carry beyond the selection's cand: the
// host's wire slot (nil for a catch-up) and the local enemy.
type offer struct {
	wire *Slot
	loc  localEnemy
}

// candidates lists what the selection may give a slot this tick: every
// host-listed enemy of a kind both sides enable that matches one of this
// game's (dying when the host says so: DYING, a tombstone, or a keyed
// dead word), then the catch-ups: this game's keyed enemies whose dead bit
// the host's words carry and the host-dead unkeyed Idents, not listed.
// The claimed pids (Env.Claims) are all of them.
func (c *Mirror) candidates(st *State, link [3]float32, haveLink bool) ([]cand, map[Ident]offer, []uint32) {
	on := c.Kinds.And(MaskSet(uint32(st.HostKindMask)))
	dist := func(p [3]float32) float64 {
		if !haveLink {
			return math.Inf(1)
		}
		return math.Sqrt(dist2(p, [][3]float32{link}))
	}
	var cands []cand
	offers := map[Ident]offer{}
	var claims []uint32
	seen := map[Ident]int{}
	for i := range st.Slots {
		seen[matchKey(st.Slots[i].Ident())]++
	}
	for i := range st.Slots {
		sl := &st.Slots[i]
		kind := KindOf(sl.Proc)
		if sl.Kind != kind {
			c.warnOnce(fmt.Sprintf("kind %d proc %04X", sl.Kind, sl.Proc), fmt.Sprintf("enemy client: the host sends proc 0x%04X as kind %d, this build's is %s; refused", sl.Proc, sl.Kind, kind))
			continue
		}
		k := matchKey(sl.Ident())
		if !on.Has(kind) || (!k.Keyed() && !on.TakesUnkeyed(kind)) || seen[k] > 1 || c.deadHere[k] {
			continue
		}
		dying := sl.Flags&SlotDying != 0 || sl.WFlags&WFlagTomb != 0 || (k.Keyed() && st.IsDead(sl.SetID))
		if dying && !k.Keyed() {
			c.hostDead[k] = true
		}
		loc, ok := c.local[k]
		if !ok {
			continue
		}
		cands = append(cands, cand{Key: k, Dist: dist(sl.Pos), Dying: dying, Late: sl.WFlags&WFlagTomb != 0, Engaged: sl.WFlags&WFlagEngaged != 0})
		offers[k] = offer{wire: sl, loc: loc}
		claims = append(claims, loc.PID)
	}
	var ups []cand
	for k, loc := range c.local {
		if _, listed := offers[k]; listed || seen[k] > 0 || c.deadHere[k] || !on.Has(loc.Kind) || (!k.Keyed() && !on.TakesUnkeyed(loc.Kind)) {
			continue
		}
		if k.Keyed() && !st.IsDead(k.SetID) || !k.Keyed() && !c.hostDead[k] {
			continue
		}
		ups = append(ups, cand{Key: k, Dist: dist(loc.Pos), Dying: true, Late: true})
		offers[k] = offer{loc: loc}
		claims = append(claims, loc.PID)
	}
	slices.SortFunc(ups, func(a, b cand) int { return compareIdent(a.Key, b.Key) })
	return append(cands, ups...), offers, claims
}

// --- Death --------------------------------------------------------------------

// unpin reads the REL's status for each pinned slot and frees the slot of
// a copy that is done: one the REL reported dead (a quiet death: the zone
// bit and the death switch follow), gone (MISSING: a legacy em_die's
// enemy_ice deleted it, or it vanished), or refused at adopt with
// ERR_ICE (its ice timers ran: K1 or a local death got there first, and
// this game owns that death), and one the walk no longer finds. ERR_GATE
// is not final: em_validate's BK/CC runtime gates (ragdoll, carry-drop,
// demo, CC actions 5/6/8) pass again a moment later, so that pin waits
// (PinMax) while the REL retries every frame. Each is
// then done here for the room (deadHere: never offered again, never
// claimed). A pin older than PinMax frees the slot too (to be offered
// again). A legacy death stays pinned through the REL's DYING while its
// copy shrinks (the slot keeps the pid, so the REL's latch holds) and frees
// once the walk loses the copy enemy_ice deleted; the REL then drops its
// latch as soon as the slot names another pid, and never re-adopts the
// shrinking copy (em_adopt's ERR_ICE). A quiet pin the walk loses counts
// as dead (see below).
func (c *Mirror) unpin(now time.Time, st *State, status *mailbox.EnemyStatus) {
	for j := range c.blk {
		b := &c.blk[j]
		if !b.Used || !b.Pinned {
			continue
		}
		e := &status.Status[j]
		dead, done := false, false
		if e.PID == b.pid {
			switch e.State {
			case mailbox.EnemyStateDead:
				dead, done = true, true
			case mailbox.EnemyStateMissing:
				done = true
			case mailbox.EnemyStateRefused:
				done = e.LastErr == mailbox.EnemyErrIce
			}
		}
		if loc, ok := c.local[b.Key]; !ok || loc.PID != b.pid {
			done = true
			// A quiet copy the walk no longer finds is dead: the REL deletes it
			// at the end of its shrink, and its STATE_DEAD can be missed (the
			// delete fell between the status read and the walk, or a disarm
			// outlasted the latch). The host already shows it dying, so the
			// zone bit and switch are its own. Not when the REL refused it with
			// ERR_ICE (this game's own death owns it), nor a re-created actor
			// under the key (a new pid: alive).
			if !ok && b.quiet && !(e.PID == b.pid && e.State == mailbox.EnemyStateRefused && e.LastErr == mailbox.EnemyErrIce) {
				dead = true
			}
		}
		if !done && now.Sub(b.PinnedAt) <= PinMax {
			continue
		}
		if dead {
			c.died(b, st)
		}
		if done {
			c.deadHere[b.Key] = true
			delete(c.local, b.Key)
		}
		*b = blkSlot{}
		c.lastHits[j], c.lastStatus[j] = 0, [2]uint8{}
	}
}

// died runs what a quiet death skipped on this game (design
// death_and_drops.quiet_steps): the keyed zone bit, once the host's dead
// words carry it (else it waits in zonePending), and, without co-op's
// switch sync, the Bokoblin's or Moblin's death switch in its home room.
func (c *Mirror) died(b *blkSlot, st *State) {
	c.Stats.Deaths++
	if b.Key.Keyed() {
		c.zonePending[b.Key.SetID] = true
		c.flushZone(st, b.room)
	}
	if b.kind == KindCC || !b.swOK || !DeathSwitchSet(b.sw) || (c.CoopSwitchSync != nil && c.CoopSwitchSync()) {
		return
	}
	if err := c.setSwitch(int(b.sw), int(b.room)); err != nil {
		log.Logf(c.rep, log.Warn, "enemy client: death switch 0x%02X of %s: %v", b.sw, b.Key, err)
		return
	}
	c.Stats.Switches++
	log.Logf(c.rep, log.Info, "enemy client: %s died here; its death switch 0x%02X set", b.Key, b.sw)
}

// flushZone ORs every waiting keyed zone bit the host's dead words now
// carry (ZoneEchoAllowed: the STATE of this stage and room, its Dead words
// with the bit) into room's zone.
func (c *Mirror) flushZone(st *State, room int8) {
	for id := range c.zonePending {
		if !ZoneEchoAllowed(st, c.localStage, room, id) {
			continue
		}
		delete(c.zonePending, id)
		bit, err := addr.ZoneActorBit(int(id))
		if err == nil {
			err = c.orZone(room, bit)
		}
		if err != nil {
			log.Logf(c.rep, log.Warn, "enemy client: zone bit of setID %d: %v", id, err)
			continue
		}
		c.Stats.ZoneBits++
	}
}

// zoneOf is the address of room's live zone (mStatus[room].mZoneNo, whose
// mRoomNo must be room: a freed or reused slot is not written).
func (c *Mirror) zoneOf(room int8) (uint32, error) {
	zn, err := addr.ZoneNoAddr(int(room))
	if err != nil {
		return 0, err
	}
	z, err := mem.ReadS8(c.m, zn)
	if err != nil {
		return 0, err
	}
	za, err := addr.ZoneAddr(int(z))
	if err != nil {
		return 0, fmt.Errorf("room %d has no zone (%d)", room, z)
	}
	r, err := mem.ReadS8(c.m, za+addr.ZoneOffRoomNo)
	if err != nil {
		return 0, err
	}
	if r != room {
		return 0, fmt.Errorf("room %d's zone %d belongs to room %d", room, z, r)
	}
	return za, nil
}

func (c *Mirror) orZone(room int8, bit addr.Bit) error {
	za, err := c.zoneOf(room)
	if err != nil {
		return err
	}
	return c.orByte(za+uint32(bit.Off), bit.Mask)
}

// setSwitch sets switch no as dSv_info_c::onSwitch routes it
// (d_save.cpp:1540-1560): the live memBit, the dan bits, or room's zone.
func (c *Mirror) setSwitch(no, room int) error {
	store, bit, err := addr.SwitchBit(no)
	if err != nil {
		return err
	}
	switch store {
	case addr.SwitchMemory:
		return c.orByte(addr.LiveMemBit+uint32(bit.Off), bit.Mask)
	case addr.SwitchDan:
		return c.orByte(addr.Dan+uint32(bit.Off), bit.Mask)
	}
	return c.orZone(int8(room), bit)
}

// standing reads the copy at a's current.pos and current.angle.y now
// (the walk's position may be MatchEvery old); walk is the fallback when
// either read fails or a coordinate is not finite (angle 0).
func (c *Mirror) standing(a uint32, walk [3]float32) ([3]float32, int16) {
	var p [3]float32
	for i := range p {
		v, err := mem.ReadF32(c.m, a+addr.ActorOffCurPos+uint32(4*i))
		if err != nil || math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return walk, 0
		}
		p[i] = v
	}
	ang, err := mem.ReadS16(c.m, a+addr.ActorOffCurAngle+2)
	if err != nil {
		return walk, 0
	}
	return p, ang
}

// orByte ORs mask into the byte at a (a read, then a one-byte write).
func (c *Mirror) orByte(a uint32, mask byte) error {
	if c.brk != nil && c.brk.Off() {
		return c.brk.Err()
	}
	v, err := mem.ReadU8(c.m, a)
	if err != nil {
		return err
	}
	if v&mask == mask {
		return nil
	}
	return c.m.Write(a, []byte{v | mask})
}

// --- The tick -----------------------------------------------------------------

// writeBlock writes one block with the slots, annex and kind mask given
// (nil slots: count 0).
func (c *Mirror) writeBlock(now time.Time, st *State, slots []mailbox.EnemySlot, annex mailbox.EnemyAnnex, kindMask uint32) error {
	if c.brk != nil && c.brk.Off() {
		return c.brk.Err()
	}
	c.seq++
	if c.seq == 0 {
		c.seq = 1
	}
	// Each slot's sample_seq is this block's seq: the smoother re-times
	// MorfFrame to the render time on every write, so the REL must re-anchor
	// its frame and rate on every block it consumes (em_consume takes them
	// only when sample_seq changes) and its S2.5 lead covers only the
	// write-to-consume gap (design S2.5; with the STATE's seq a STATE gap
	// froze the copy after EM_FRAME_LEAD_CAP frames, and a bck switch the
	// smoother made between STATEs reached the REL with the previous anim's
	// frame and rate). Raw mode keeps the STATE's seq: its frame is the
	// newest sample's, not re-timed, so re-anchoring on a repeat write would
	// throw away the lead the REL added since that sample.
	for i := range slots {
		if !c.raw || st == nil {
			slots[i].SampleSeq = c.seq
		} else {
			slots[i].SampleSeq = st.Seq
		}
	}
	// The block's flags follow the STATE's: the animation stream only
	// while the host resolved a table, the colliders while the host asks
	// for them (design "wire_and_block", CLIENT). The kind mask is the
	// kinds both sides enable; the annex goes in the same write, before
	// seq_head, with annex_seq = seq (mailbox.EnemyBlock.EncodeWrite).
	b := &mailbox.EnemyBlock{Magic: mailbox.EnemyMagic, Seq: c.seq, GoWriteMS: uint32(now.Sub(c.start).Milliseconds()),
		Flags: mailbox.EnemyFlagMirror, Layout: mailbox.EnemyLayout, Epoch: c.epoch, Role: mailbox.EnemyRoleClient, Slots: slots,
		KindMask: kindMask, Annex: annex}
	if c.raw {
		b.Flags |= mailbox.EnemyFlagRaw
	}
	if st != nil {
		if st.Flags&StateFlagAnim != 0 {
			b.Flags |= mailbox.EnemyFlagAnim
		}
		if st.Flags&StateFlagColliders != 0 {
			b.Flags |= mailbox.EnemyFlagColliders
		}
		b.HostBeat, b.Stage, b.Room = st.HostBeat, st.Stage, st.Room
	} else {
		b.Stage, b.Room = c.localStage, c.localRoom
	}
	if err := mailbox.WriteEnemyBlock(c.m, b); err != nil {
		if c.brk != nil {
			c.brk.Fault(write.FaultRefusal, "enemy block: "+err.Error())
		}
		return err
	}
	c.Stats.Writes++
	c.Stats.LastWriteMS = b.GoWriteMS
	if !c.armed {
		c.armed, c.armedAt, c.ackSeen = true, now, false
	}
	return nil
}

// disarm writes a count-0 block once (the REL releases its slots) when
// the mirror was armed, and claims nothing.
func (c *Mirror) disarm(now time.Time, why string) error {
	if why != c.gateWhy {
		c.gateWhy = why
		log.Logf(c.rep, log.Info, "enemy client: not mirroring: %s", why)
	}
	c.Claims.Replace(nil)
	if !c.armed {
		return nil
	}
	err := c.writeBlock(now, nil, nil, mailbox.EnemyAnnex{}, 0)
	c.armed = false
	return err
}

// Tick runs one 16 ms tick at now. It returns the HITs to send (the REL
// flagged them), how long the caller should sleep before the next tick
// (GateSleep after a gate failure, else 0 for the normal cadence), and
// a read or write error.
func (c *Mirror) Tick(now time.Time) (hits []*Hit, sleep time.Duration, err error) {
	c.mu.Lock()
	acks := c.acks
	c.acks = nil
	c.mu.Unlock()
	for _, a := range acks {
		log.Logf(c.rep, log.Info, "enemy client: ACK setID 0x%X handle %d hit %d %s/%s frames %d sets %d health %d", a.SetID, a.Handle, a.HitSeq, ResultName(a.Result), DetailName(a.Detail), a.Frames, a.Sets, a.Health)
	}
	if !c.checkContract(now) {
		return nil, GateSleep, nil
	}
	if err := c.checkHeartbeat(now); err != nil {
		return nil, 0, err
	}
	if !c.checkREL(now) {
		return nil, GateSleep, c.disarm(now, c.relWhy)
	}
	scope, err := ReadScope(c.m)
	if err != nil {
		return nil, 0, err
	}
	// What belongs to a room is this game's, so it follows this game's
	// scope before any host gate: a stage load (not in play: the reload
	// of the same room after a void-out or a Game Over too) re-creates
	// every actor, and a room this game left while the host stayed (the
	// host-room gate below disarms before match) has new pids on return.
	if !scope.InPlay {
		c.resetScope()
		return nil, GateSleep, c.disarm(now, "this game is not in play")
	}
	if scope.Stage != c.localStage || scope.Room != c.localRoom {
		c.resetScope()
		c.localStage, c.localRoom = scope.Stage, scope.Room
	}
	st, at := c.Latest()
	switch {
	case st == nil:
		return nil, GateSleep, c.disarm(now, "no STATE from the host yet")
	case now.Sub(at) > StaleAfter:
		return nil, GateSleep, c.disarm(now, "the host's STATE is stale")
	case st.Flags&StateFlagInScope == 0:
		return nil, GateSleep, c.disarm(now, "the host is not in play")
	case st.KindHash != KindHash():
		return nil, GateSleep, c.disarm(now, fmt.Sprintf("the host's enemy kind table is another build's (hash %08X, this build's %08X)", st.KindHash, KindHash()))
	case st.Stage != scope.Stage || st.Room != scope.Room:
		return nil, GateSleep, c.disarm(now, fmt.Sprintf("the host is in %s room %d, this game in %s room %d", st.Stage, st.Room, scope.Stage, scope.Room))
	}
	c.gateWhy = ""

	// The REL's tail: ack, status, hits.
	status, err := mailbox.ReadEnemyStatus(c.m)
	if err != nil {
		return nil, 0, err
	}
	// The REL's last_err is sticky (it keeps the last failure seen, not
	// the current state), so a resolve failure forces a walk only when
	// the slot's (state, last_err) pair changed since the last tick.
	force := false
	for i := range status.Status {
		e := &status.Status[i]
		key := [2]uint8{e.State, e.LastErr}
		walkErr := e.LastErr >= mailbox.EnemyErrPID && e.LastErr <= mailbox.EnemyErrSetID || e.LastErr == mailbox.EnemyErrKind || e.LastErr == mailbox.EnemyErrKey
		if key != c.lastStatus[i] && walkErr {
			force = true
		}
		c.lastStatus[i] = key
	}
	if err := c.match(now, scope, st, force); err != nil {
		return nil, 0, err
	}
	// The status again, after the walk: the game runs on while Go reads, and
	// a quiet death's last frame (fopAcM_delete, STATE_DEAD in one REL frame)
	// that fell between the first read and the walk would show the copy gone
	// beside a DYING status; unpin and the hits go by this read.
	if status, err = mailbox.ReadEnemyStatus(c.m); err != nil {
		return nil, 0, err
	}
	c.Stats.LastStatus = status
	// A new host epoch is a restarted host Go (pipelines.go: the epoch is
	// its start time) whose host_ms began again near 0: every smoother
	// re-bases, or each would drop the new samples as older than its newest
	// and hold the old host's last pose.
	if c.haveHost && st.Epoch != c.hostEpoch {
		log.Logf(c.rep, log.Info, "enemy client: the host's epoch went %d -> %d (the host restarted); the smoothers re-base", c.hostEpoch, st.Epoch)
		for _, s := range c.sm {
			s.Rebase()
		}
	}
	c.hostEpoch, c.haveHost = st.Epoch, true

	c.unpin(now, st, &status)
	c.flushZone(st, scope.Room)
	link, haveLink := c.linkPos()
	cands, offers, claims := c.candidates(st, link, haveLink)
	c.Claims.Replace(claims)

	// The selection, and the bookkeeping of every slot whose enemy changed:
	// its drained hit_seq and REL status start over.
	var cur [MaxSlots]selSlot
	for j := range c.blk {
		cur[j] = c.blk[j].selSlot
	}
	next := selectSlots(cands, cur, c.Remote.LockedSlots(), now, c.Select)
	for j := range next {
		b := &c.blk[j]
		n := next[j]
		if !n.Used {
			if b.Used {
				c.lastHits[j], c.lastStatus[j] = 0, [2]uint8{}
			}
			*b = blkSlot{}
			continue
		}
		o, offered := offers[n.Key]
		if !b.Used || b.Key != n.Key {
			*b = blkSlot{pid: o.loc.PID, local: o.loc.Ident, kind: o.loc.Kind, room: o.loc.HomeRoom, addr: o.loc.Addr,
				catchUp: n.Pinned}
			c.lastHits[j], c.lastStatus[j] = 0, [2]uint8{}
		} else if offered && !b.Pinned && o.loc.PID != b.pid {
			// The same key under a new local actor (re-created in this room: a
			// save state loaded, a room reload the scope did not see): the slot
			// follows the walk, as the prototype re-read the pid every tick. A
			// pinned slot is unpin's (a new pid there is done).
			b.pid, b.local, b.kind, b.room, b.addr = o.loc.PID, o.loc.Ident, o.loc.Kind, o.loc.HomeRoom, o.loc.Addr
			b.haveLst = false
			c.lastHits[j], c.lastStatus[j] = 0, [2]uint8{}
		}
		if n.Pinned && !b.Pinned {
			// The death switch is read now: the REL deletes the copy at its
			// quiet death, before STATE_DEAD reaches Go.
			if off := DeathSwitchOff(b.kind); off != 0 {
				if v, err := mem.ReadU8(c.m, b.addr+off); err == nil {
					b.sw, b.swOK = v, true
				}
			}
		}
		b.selSlot = n
		b.quiet = c.Quiet.Has(b.kind) || b.catchUp
	}

	// Smooth every listed enemy matched here (a newcomer then has its
	// history), and lay the slots out.
	listed := map[Ident]bool{}
	for i := range st.Slots {
		sl := &st.Slots[i]
		k := matchKey(sl.Ident())
		if _, ok := offers[k]; !ok {
			continue
		}
		listed[k] = true
		s := c.sm[k]
		if s == nil {
			s = NewSmoother(c.start, c.raw)
			c.sm[k] = s
		}
		s.Push(st.HostMS, at, *sl, c.epoch)
	}
	for k, s := range c.sm {
		if !listed[k] {
			s.Reset()
		}
	}
	on := c.Kinds.And(MaskSet(uint32(st.HostKindMask)))
	annex := mailbox.EnemyAnnex{Override: c.Annex.Override, InvulnWait: c.Annex.InvulnWait}
	n := 0
	for j := range c.blk {
		if c.blk[j].Used {
			n = j + 1
		}
	}
	slots := make([]mailbox.EnemySlot, n)
	for j := range n {
		b := &c.blk[j]
		if !b.Used {
			continue // pid 0: the REL releases the slot
		}
		var es mailbox.EnemySlot
		o := offers[b.Key]
		if o.wire != nil {
			out, ok := c.sm[b.Key].At(now)
			if !ok {
				out = *o.wire
			}
			// The discrete animation fields come from the smoother's output as
			// the other discrete fields do: the bracket sample's, so they switch
			// at its render time (smooth.go copies the whole slot).
			es = mailbox.EnemySlot{Pos: out.Pos, AngleY: out.AngleY, Tilt: out.Tilt,
				AttackType: out.AttackType, AttackPhase: out.AttackPhase, Health: out.Health, Flags: out.Flags,
				MorfFrame: out.MorfFrame, MorfRate: out.MorfRate, // SampleSeq: writeBlock
				Bck: out.Bck, AnmAttr: out.AnmAttr, AnmSeq: out.AnmSeq, Morf: out.Morf, Flags2: out.Flags2 &^ mailbox.EnemySF2Quiet,
				BlurShape: out.BlurShape, BlurScale: out.BlurScale}
			if b.Key.Keyed() && st.IsDead(b.Key.SetID) {
				es.Flags |= mailbox.EnemySFDying
			}
			if b.kind != KindMO2 {
				annex.Aux[j], b.lastAux = out.Aux, out.Aux
			}
		} else {
			// Not listed (a catch-up, or a pinned copy whose tombstone
			// expired): dying, released, from where it was last written, and
			// always quiet, the Moblin's too (design D6: the legacy em_die
			// would drop an item and run onSwitch here). A slot never written
			// (a fresh catch-up) starts from where the copy stands: a zero Pos
			// would be a valid target at the world origin (the REL also holds
			// a RELEASE|DYING copy where it stands, em_slot_frame).
			b.quiet = true
			es = mailbox.EnemySlot{Bck: mailbox.EnemyBckNone, AnmAttr: AnmAttrUnknown}
			if b.haveLst {
				es, annex.Aux[j] = b.last, b.lastAux
			} else {
				es.Pos, es.AngleY = c.standing(b.addr, o.loc.Pos)
			}
			es.Flags = mailbox.EnemySFDying | mailbox.EnemySFRelease
			es.Flags2 &^= mailbox.EnemySF2Quiet
		}
		// The block slot carries the local actor: its pid, its own pkey
		// (the REL recomputes it and compares), the proc (the REL refuses
		// another), SF2_QUIET for a quiet death.
		es.PID, es.PKey, es.Proc = b.pid, b.local.PKey, b.local.Proc
		if b.quiet {
			es.Flags2 |= mailbox.EnemySF2Quiet
		}
		b.last, b.haveLst = es, true
		slots[j] = es
	}
	if err := c.writeBlock(now, st, slots, annex, on.Mask()); err != nil {
		return nil, 0, err
	}

	// Liveness and the periodic line.
	if !c.ackSeen && status.AckSeq != 0 && status.AckSeq == c.seq-1 {
		c.ackSeen = true
	}
	if status.AckSeq == c.seq-1 || status.AckSeq == c.seq {
		c.ackSeen = true
	}
	if !c.ackSeen && !c.relQuiet && now.Sub(c.armedAt) > ackWithin {
		c.relQuiet = true
		c.rep.Log(log.Warn, "enemy client: REL not consuming: ack_seq has not followed seq since arming.")
	}
	if now.After(c.logNext) {
		c.logNext = now.Add(LogEvery)
		log.Logf(c.rep, log.Info, "enemy client: seq %d write_ms %d ack %d frame %d beat %d adopted %04b slots %s status %s canaries %+v canaries2 %+v",
			c.seq, c.Stats.LastWriteMS, status.AckSeq, status.AckFrame, st.HostBeat, status.AdoptedMask, c.slotsLine(), statusLine(&status), status.Canaries, status.Canaries2)
	}

	// Hits the REL flagged: read the hit object, send, clear pending. The
	// pending byte is cleared for every pending entry, a repeated hit_seq
	// included: the REL withholds the slot's collider until it reads 0
	// (enemy_mirror.c:737-747 em_hits), so an entry left pending would keep the
	// copy unhittable. Only the send is de-duplicated. A slot the host no
	// longer lists (a catch-up, a pinned dying copy) has no handle to name:
	// its hit is cleared and not sent.
	for i := range status.Hits {
		h := status.Hits[i]
		if h.Pending == 0 {
			continue
		}
		b := &c.blk[i]
		if o := offers[b.Key]; b.Used && o.wire != nil && h.HitSeq != c.lastHits[i] {
			c.lastHits[i] = h.HitSeq
			hit, err := c.readHit(st, o.wire, b.pid, b.kind, i, h)
			if err != nil {
				log.Logf(c.rep, log.Warn, "enemy client: hit slot %d: %v", i, err)
			} else if hit != nil {
				hits = append(hits, hit)
				c.Stats.Hits++
			}
		}
		at := mailbox.EnemyBlockAddr + mailbox.EnemyHitsOff + uint32(i)*mailbox.EnemyHitSize + 3
		if err := c.m.Write(at, []byte{0}); err != nil {
			log.Logf(c.rep, log.Warn, "enemy client: clearing hit slot %d: %v", i, err)
		}
	}
	return hits, 0, nil
}

// OwnerOf translates a PHIT's attacker handle into the remote-Link apply
// record's owner id (RemoteClient.Owner; design closed_before_build "PHIT
// owner"): a keyed enemy's setID (the REL finds its copy by it), OwnerSlotBase
// plus j for an unkeyed one in block slot j, else NoMirrorSetID (the
// stand-in). Never the raw handle.
func (c *Mirror) OwnerOf(handle uint16) uint16 {
	st, _ := c.Latest()
	if st == nil || handle == HandleNone || handle == NoMirrorHandle {
		return NoMirrorSetID
	}
	for i := range st.Slots {
		sl := &st.Slots[i]
		if sl.Handle != handle {
			continue
		}
		k := matchKey(sl.Ident())
		if k.Keyed() {
			return k.SetID
		}
		for j := range c.blk {
			if c.blk[j].Used && c.blk[j].Key == k {
				return OwnerSlotBase + uint16(j)
			}
		}
		break
	}
	return NoMirrorSetID
}

// readHit builds the HIT for block slot i, whose host enemy is wire (its
// setID and handle name the target) and local actor pid (checked against
// the REL's status), stamped with the epoch of the host STATE mirrored:
// the host's HitApplier gates on its own epoch (host.go Offer), never on
// this mirror's. The collider is the kind's for the REL's code
// (ColliderOff). It carries the hit object's At type and atp (cCcD_ObjAt
// mType at obj+0x10, mAtp at +0x14, c_cc_d.h:438-439), its dCcD_GObjAt's
// SPrm low byte, Se, HitMark and Spl (addr.CcAtOff*; one hitObjReadLen
// read from obj+0x10) and the collider's centre.
func (c *Mirror) readHit(st *State, wire *Slot, pid uint32, kind KindID, i int, h mailbox.EnemyHitEntry) (*Hit, error) {
	col := ColliderOff(kind, h.Collider)
	if col == 0 {
		return nil, fmt.Errorf("collider %d of kind %s", h.Collider, kind)
	}
	stat := c.Stats.LastStatus.Status[i]
	if stat.Actor == 0 || !addr.InMEM1(stat.Actor) || stat.PID != pid {
		return nil, errors.New("no resolved actor for the slot")
	}
	obj, err := mem.ReadU32(c.m, stat.Actor+col+addr.Mo2CcTgHitObjOff)
	if err != nil {
		return nil, err
	}
	// The HIT's seq is this mirror's own counter, never the REL's: that one
	// counts per block slot (em_hits), so an enemy that moved to another slot
	// could repeat the seq the host last took for its handle, and the host's
	// replay check (per client and handle, host.go Offer) would drop a real
	// hit. This one only grows (past 0xFFFF it skips 0).
	c.hitSeq++
	if c.hitSeq == 0 {
		c.hitSeq = 1
	}
	hit := &Hit{Collider: h.Collider, Epoch: st.Epoch, Stage: st.Stage, Room: st.Room, SetID: wire.SetID, Handle: wire.Handle, HitSeq: c.hitSeq, ClientFrame: c.hb}
	// Wire v3: the swing's cut type and count, which the host stages into
	// this player's remote body under ATTRIB (best effort: a failed read
	// sends 0, an untyped hit).
	if link, err := mem.ReadU32(c.m, addr.PlayerPtr); err == nil && addr.InMEM1(link) {
		if b, err := c.m.Read(link+addr.PlayerOffCutType, 2); err == nil && len(b) == 2 {
			hit.CutType, hit.CutCount = b[0], b[1]
		}
	}
	if addr.InMEM1(obj) {
		b, err := c.m.Read(obj+hitObjReadOff, hitObjReadLen)
		if err != nil {
			return nil, err
		}
		if len(b) == hitObjReadLen {
			at := func(off uint32) []byte { return b[off-hitObjReadOff:] }
			hit.AtType = mem.U32BE(at(hitObjOffAtType))
			hit.Atp = at(hitObjOffAtp)[0]
			hit.AtSPrm = at(addr.CcAtOffSPrm)[3] // the u32's low byte
			hit.Se = at(addr.CcAtOffSe)[0]
			hit.HitMark = at(addr.CcAtOffHitMark)[0]
			hit.Spl = at(addr.CcAtOffSpl)[0]
		}
	}
	p, err := c.m.Read(stat.Actor+col+addr.Mo2CcCenterOff, 12)
	if err != nil {
		return nil, err
	}
	if len(p) == 12 {
		for k := range 3 {
			hit.Pos[k] = mem.F32BE(p[4*k:])
		}
	}
	return hit, nil
}

// The hit object read: from cCcD_ObjAt mType (obj+0x10) through
// dCcD_GObjAt mSpl (obj+0x6F), 0x60 bytes in one read.
const (
	hitObjOffAtType = 0x10
	hitObjOffAtp    = 0x14
	hitObjReadOff   = hitObjOffAtType
	hitObjReadLen   = 0x70 - hitObjReadOff
)

// slotsLine prints each block slot's enemy (kind and setID, or pkey for an
// unkeyed one), P when pinned dying, Q when quiet.
func (c *Mirror) slotsLine() string {
	s := ""
	for j := range c.blk {
		b := &c.blk[j]
		if !b.Used {
			s += fmt.Sprintf("[%d:-]", j)
			continue
		}
		id := fmt.Sprintf("%X", b.Key.SetID)
		if !b.Key.Keyed() {
			id = fmt.Sprintf("u%08X", b.Key.PKey)
		}
		mark := ""
		if b.Pinned {
			mark += "P"
		}
		if b.quiet {
			mark += "Q"
		}
		s += fmt.Sprintf("[%d:%s %s%s]", j, b.kind, id, mark)
	}
	return s
}

// statusLine prints each slot's state, last_err, animation state, the
// bck setAnm'd on the copy and its setAnm count.
func statusLine(st *mailbox.EnemyStatus) string {
	s := ""
	for i := range st.Status {
		e := &st.Status[i]
		s += fmt.Sprintf("[%d:s%d e%d a%d bck%02X n%d]", i, e.State, e.LastErr, e.AnmState, e.Bck, e.AnmChanges)
	}
	return s
}

// Shutdown writes magic 0 when the block was ever written, so the REL
// releases every slot.
func (c *Mirror) Shutdown() error {
	c.Claims.Replace(nil)
	if c.seq == 0 {
		return nil
	}
	c.armed = false
	return mailbox.ClearEnemyBlock(c.m)
}
