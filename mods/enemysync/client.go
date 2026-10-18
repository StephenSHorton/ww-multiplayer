package enemysync

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/write"
)

// The client mirror (design "client_go_side"): every tick it gates
// (contract every ContractEvery, the REL module linked with this build
// and its frame counter advancing, in play with no stage change pending,
// the same stage and room as the host's STATE, a STATE fresher than
// StaleAfter), matches the host's setIDs to this game's keyed mode-0
// Moblins by (room, setID), smooths each one and writes one block
// (mailbox.EnemyGoSpan bytes, seq_head == seq_tail) through m.Write,
// then reads the REL's tail. A gate failure writes a count-0 block (the
// REL releases) and asks the caller to sleep GateSleep; Shutdown writes
// magic 0. A heartbeat regression (a save state loaded) starts a new
// epoch. Hits the REL flagged come back from Tick as HIT messages.
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

	// Matching: this game's Moblins by setID for (stage, room).
	local      map[uint16]Moblin
	localStage string
	localRoom  int8
	matchNext  time.Time
	lastSetIDs []uint16

	sm map[uint16]*Smoother

	armed    bool
	armedAt  time.Time
	ackSeen  bool
	relQuiet bool
	gateWhy  string
	logNext  time.Time
	hitSeq   uint16
	lastHits [MaxSlots]uint16 // the last hit_seq drained per slot
	Stats    MirrorStats
}

// MirrorStats are counters a probe or test reads.
type MirrorStats struct {
	Writes, Epochs, Hits uint64
	LastStatus           mailbox.EnemyStatus
	LastWriteMS          uint32
}

// NewMirror returns a mirror over m with its breaker; raw bypasses the
// smoother and sets the block's raw flag (WW_ENEMY_RAW=1).
func NewMirror(m mem.Mem, rep log.Reporter, brk *write.Breaker, raw bool, start time.Time) *Mirror {
	return &Mirror{m: m, rep: rep, brk: brk, raw: raw, start: start, epoch: 1, local: map[uint16]Moblin{}, sm: map[uint16]*Smoother{}}
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
	}
}

// Latest returns the latest STATE and its arrival.
func (c *Mirror) Latest() (*State, time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.latest, c.latestAt
}

// newEpoch starts a new epoch: the smoothers forget, the next block
// carries it, the REL forgets its slots.
func (c *Mirror) newEpoch(why string) {
	c.epoch++
	c.Stats.Epochs++
	for _, s := range c.sm {
		s.Reset()
	}
	log.Logf(c.rep, log.Info, "enemy client: epoch %d (%s)", c.epoch, why)
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
		why = "the REL module's actor is not running (its frame counter is still)"
	}
	c.relFrames = r.Frames
	ok := why == ""
	if ok != c.relOK || (why != "" && why != c.relWhy) {
		if ok {
			c.rep.Log(log.OK, fmt.Sprintf("enemy client: REL module linked (build %08X, frame %d)", r.BuildID, r.Frames))
			if c.relWhy != "" {
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

// match walks this game's Moblins of (stage, room) when the scope
// changed, the host's setIDs changed, MatchEvery passed, or the REL
// reported an unresolved slot.
func (c *Mirror) match(now time.Time, scope Scope, st *State, force bool) error {
	ids := make([]uint16, len(st.Slots))
	for i := range st.Slots {
		ids[i] = st.Slots[i].SetID
	}
	if !force && scope.Stage == c.localStage && scope.Room == c.localRoom && slices.Equal(ids, c.lastSetIDs) && now.Before(c.matchNext) {
		return nil
	}
	c.matchNext = now.Add(MatchEvery)
	c.localStage, c.localRoom, c.lastSetIDs = scope.Stage, scope.Room, ids
	mos, err := KeyedMoblins(c.m, scope.Room)
	if err != nil {
		return err
	}
	local := make(map[uint16]Moblin, len(mos))
	for _, mo := range mos {
		local[mo.SetID] = mo
	}
	if len(local) != len(c.local) {
		log.Logf(c.rep, log.Info, "enemy client: %d keyed Moblin(s) of ours in %s room %d", len(local), scope.Stage, scope.Room)
	}
	c.local = local
	return nil
}

// writeBlock writes one block with the slots given (nil: count 0).
func (c *Mirror) writeBlock(now time.Time, st *State, slots []mailbox.EnemySlot) error {
	if c.brk != nil && c.brk.Off() {
		return c.brk.Err()
	}
	c.seq++
	if c.seq == 0 {
		c.seq = 1
	}
	b := &mailbox.EnemyBlock{Magic: mailbox.EnemyMagic, Seq: c.seq, GoWriteMS: uint32(now.Sub(c.start).Milliseconds()),
		Flags: mailbox.EnemyFlagMirror | mailbox.EnemyFlagAnim, Layout: mailbox.EnemyLayout, Epoch: c.epoch, Role: mailbox.EnemyRoleClient, Slots: slots}
	if c.raw {
		b.Flags |= mailbox.EnemyFlagRaw
	}
	if st != nil {
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
// the mirror was armed.
func (c *Mirror) disarm(now time.Time, why string) error {
	if why != c.gateWhy {
		c.gateWhy = why
		log.Logf(c.rep, log.Info, "enemy client: not mirroring: %s", why)
	}
	if !c.armed {
		return nil
	}
	err := c.writeBlock(now, nil, nil)
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
		log.Logf(c.rep, log.Info, "enemy client: ACK setID %d hit %d result %d health %d", a.SetID, a.HitSeq, a.Result, a.Health)
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
	if !scope.InPlay {
		return nil, GateSleep, c.disarm(now, "this game is not in play")
	}
	st, at := c.Latest()
	switch {
	case st == nil:
		return nil, GateSleep, c.disarm(now, "no STATE from the host yet")
	case now.Sub(at) > StaleAfter:
		return nil, GateSleep, c.disarm(now, "the host's STATE is stale")
	case st.Flags&StateFlagInScope == 0:
		return nil, GateSleep, c.disarm(now, "the host is not in play")
	case st.Stage != scope.Stage || st.Room != scope.Room:
		return nil, GateSleep, c.disarm(now, fmt.Sprintf("the host is in %s room %d, this game in %s room %d", st.Stage, st.Room, scope.Stage, scope.Room))
	}
	c.gateWhy = ""

	// The REL's tail: ack, status, hits.
	status, err := mailbox.ReadEnemyStatus(c.m)
	if err != nil {
		return nil, 0, err
	}
	c.Stats.LastStatus = status
	force := false
	for i := range status.Status {
		if e := status.Status[i].LastErr; e >= mailbox.EnemyErrPID && e <= mailbox.EnemyErrSetID {
			force = true
		}
	}
	if err := c.match(now, scope, st, force); err != nil {
		return nil, 0, err
	}

	// Smooth and lay the slots out.
	slots := make([]mailbox.EnemySlot, 0, len(st.Slots))
	seen := map[uint16]bool{}
	for i := range st.Slots {
		sl := &st.Slots[i]
		seen[sl.SetID] = true
		s := c.sm[sl.SetID]
		if s == nil {
			s = NewSmoother(c.start, c.raw)
			c.sm[sl.SetID] = s
		}
		s.Push(st.HostMS, at, *sl, c.epoch)
		out, ok := s.At(now)
		if !ok {
			out = *sl
		}
		es := mailbox.EnemySlot{Pos: out.Pos, AngleY: out.AngleY, Tilt: out.Tilt, Action: out.Action, Mode: out.Mode,
			AttackType: out.AttackType, AttackPhase: out.AttackPhase, Health: out.Health, Flags: out.Flags,
			MorfFrame: out.MorfFrame, MorfRate: out.MorfRate, SampleSeq: st.Seq}
		if mo, ok := c.local[sl.SetID]; ok {
			es.PID = mo.PID
		}
		if st.DeadMask&(1<<sl.SetID) != 0 && sl.SetID < 8 {
			es.Flags |= mailbox.EnemySFDying
		}
		slots = append(slots, es)
	}
	// A setID the host no longer lists but whose zone bit is set, and
	// which this game still has alive: dying (a late joiner's catch-up).
	for id, mo := range c.local {
		if seen[id] || id >= 8 || st.DeadMask&(1<<id) == 0 || len(slots) >= MaxSlots {
			continue
		}
		slots = append(slots, mailbox.EnemySlot{PID: mo.PID, Flags: mailbox.EnemySFDying | mailbox.EnemySFRelease, SampleSeq: st.Seq})
	}
	for id, s := range c.sm {
		if !seen[id] {
			s.Reset()
		}
	}
	if err := c.writeBlock(now, st, slots); err != nil {
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
		log.Logf(c.rep, log.Info, "enemy client: seq %d write_ms %d ack %d frame %d beat %d adopted %04b status %s canaries %+v",
			c.seq, c.Stats.LastWriteMS, status.AckSeq, status.AckFrame, st.HostBeat, status.AdoptedMask, statusLine(&status), status.Canaries)
	}

	// Hits the REL flagged: read the hit object, send, clear pending.
	for i := range status.Hits {
		h := status.Hits[i]
		if h.Pending == 0 || i >= len(slots) || h.HitSeq == c.lastHits[i] {
			continue
		}
		c.lastHits[i] = h.HitSeq
		hit, err := c.readHit(st, slots[i].PID, i, h)
		if err != nil {
			log.Logf(c.rep, log.Warn, "enemy client: hit slot %d: %v", i, err)
		} else if hit != nil {
			hits = append(hits, hit)
			c.Stats.Hits++
		}
		at := mailbox.EnemyBlockAddr + mailbox.EnemyHitsOff + uint32(i)*mailbox.EnemyHitSize + 3
		if err := c.m.Write(at, []byte{0}); err != nil {
			log.Logf(c.rep, log.Warn, "enemy client: clearing hit slot %d: %v", i, err)
		}
	}
	return hits, 0, nil
}

// readHit builds the HIT for slot i: the hit object's At type and atp
// (cCcD_ObjAt mType at obj+0x10, mAtp at +0x14, c_cc_d.h:438-439) and the
// collider's centre.
func (c *Mirror) readHit(st *State, pid uint32, i int, h mailbox.EnemyHitEntry) (*Hit, error) {
	var col uint32
	switch h.Collider {
	case ColliderHead:
		col = addr.Mo2OffHeadSph
	case ColliderTgCyl:
		col = addr.Mo2OffTgCyl
	default:
		return nil, fmt.Errorf("collider %d", h.Collider)
	}
	stat := c.Stats.LastStatus.Status[i]
	if stat.Actor == 0 || !addr.InMEM1(stat.Actor) || stat.PID != pid {
		return nil, errors.New("no resolved actor for the slot")
	}
	obj, err := mem.ReadU32(c.m, stat.Actor+col+addr.Mo2CcTgHitObjOff)
	if err != nil {
		return nil, err
	}
	hit := &Hit{Collider: h.Collider, Epoch: c.epoch, Stage: st.Stage, Room: st.Room, SetID: st.Slots[i].SetID, HitSeq: h.HitSeq, ClientFrame: c.hb}
	if addr.InMEM1(obj) {
		b, err := c.m.Read(obj+0x10, 8)
		if err != nil {
			return nil, err
		}
		if len(b) == 8 {
			hit.AtType = mem.U32BE(b)
			hit.Atp = b[4]
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
	c.hitSeq++
	return hit, nil
}

func statusLine(st *mailbox.EnemyStatus) string {
	s := ""
	for i := range st.Status {
		e := &st.Status[i]
		s += fmt.Sprintf("[%d:s%d e%d]", i, e.State, e.LastErr)
	}
	return s
}

// Shutdown writes magic 0 when the block was ever written, so the REL
// releases every slot.
func (c *Mirror) Shutdown() error {
	if c.seq == 0 {
		return nil
	}
	c.armed = false
	return mailbox.ClearEnemyBlock(c.m)
}
