package enemysync

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// The host sampler (design "host_side"): every tick it samples the keyed
// mode-0 Moblins of the room Link is in and returns the STATE to send,
// or a count-0 keepalive every KeepaliveEvery while the game is not in
// play or a stage change is pending. It discovers the Moblins with a
// walk every WalkEvery, after a validation failure and on a stage or
// room change, caches (addr, pid, setID, home room) sorted by setID,
// drops an entry whose process ID changed under it (the block was freed
// and reused), and resolves the Mo2 resource table at every walk so each
// sample's mpAnm becomes a BCK index (Tracker). It never writes an actor.
const (
	WalkEvery      = 500 * time.Millisecond
	KeepaliveEvery = 250 * time.Millisecond
)

// Host is the sampler's state.
type Host struct {
	m     mem.Reader
	rep   log.Reporter
	start time.Time
	epoch uint32
	seq   uint32

	scope      Scope
	cache      []Moblin
	snapNext   map[uint16]bool // setIDs whose next sample snaps (just discovered)
	nextWalk   time.Time
	lastKeep   time.Time
	walkFailed bool
	colliders  bool
	resWhy     string

	// Tracker resolves the Mo2 table and runs the restart detector.
	Tracker Tracker
}

// CollidersEnabled reports whether the STATE asks the clients to keep
// their copies' colliders submitted: on unless WW_ENEMY_COLLIDERS=0.
func CollidersEnabled() bool { return os.Getenv("WW_ENEMY_COLLIDERS") != "0" }

// NewHost returns a sampler over m at epoch; start anchors host_ms. The
// colliders flag follows CollidersEnabled at construction.
func NewHost(m mem.Reader, rep log.Reporter, epoch uint32, start time.Time) *Host {
	return &Host{m: m, rep: rep, epoch: epoch, start: start, snapNext: map[uint16]bool{}, colliders: CollidersEnabled()}
}

// SetColliders overrides the colliders flag (tests; the env rules
// otherwise).
func (h *Host) SetColliders(on bool) { h.colliders = on }

// Epoch is the epoch every STATE carries.
func (h *Host) Epoch() uint32 { return h.epoch }

// Scope is the scope the last Tick read.
func (h *Host) Scope() Scope { return h.scope }

// Moblins is the cached discovery, sorted by setID.
func (h *Host) Moblins() []Moblin { return append([]Moblin(nil), h.cache...) }

// HostMS is the host's monotonic ms at t, the STATE's host_ms.
func (h *Host) HostMS(t time.Time) uint32 { return uint32(t.Sub(h.start).Milliseconds()) }

// walk discovers the Moblins of the scope's room and resolves the Mo2
// table.
func (h *Host) walk(now time.Time) error {
	h.nextWalk = now.Add(WalkEvery)
	was := h.cache
	cache, err := KeyedMoblins(h.m, h.scope.Room)
	if err != nil {
		return err
	}
	h.cache = cache
	pids := make([]uint32, len(cache))
	for i := range cache {
		pids[i] = cache[i].PID
		found := false
		for j := range was {
			if was[j].PID == cache[i].PID && was[j].Addr == cache[i].Addr {
				found = true
				break
			}
		}
		if !found {
			h.snapNext[cache[i].SetID] = true
		}
	}
	h.Tracker.Keep(pids)
	if len(cache) != len(was) || h.walkFailed {
		h.walkFailed = false
		log.Logf(h.rep, log.Info, "enemy host: %d keyed Moblin(s) in %s room %d", len(cache), h.scope.Stage, h.scope.Room)
	}
	if err := h.Tracker.Res.Resolve(h.m); err != nil {
		return err
	}
	if why := h.Tracker.Res.Why(); why != h.resWhy {
		h.resWhy = why
		if why == "" {
			entry, res := h.Tracker.Res.Entry()
			log.Logf(h.rep, log.Info, "enemy host: Mo2 resource table resolved (mObjectInfo[%d], mRes 0x%08X): the animation stream is on", entry, res)
		} else {
			log.Logf(h.rep, log.Warn, "enemy host: %s: the animation stream is off until it resolves", why)
		}
	}
	return nil
}

// Tick samples once at now. It returns the STATE to send, or nil when
// nothing is due (out of play and the keepalive not due yet).
func (h *Host) Tick(now time.Time) (*State, error) {
	scope, err := ReadScope(h.m)
	if err != nil {
		return nil, err
	}
	changed := scope.Stage != h.scope.Stage || scope.Room != h.scope.Room
	h.scope = scope
	hb, err := mem.ReadU32(h.m, mailbox.SpawnTrigger)
	if err != nil {
		return nil, err
	}
	st := &State{Epoch: h.epoch, HostBeat: hb, HostMS: h.HostMS(now), Stage: scope.Stage, Room: scope.Room}
	if !scope.InPlay {
		h.cache = nil
		if !h.lastKeep.IsZero() && now.Sub(h.lastKeep) < KeepaliveEvery {
			return nil, nil
		}
		h.lastKeep = now
		h.seq++
		st.Seq = h.seq
		return st, nil
	}
	if changed || now.After(h.nextWalk) {
		if changed {
			h.cache = nil
		}
		if err := h.walk(now); err != nil {
			return nil, err
		}
	}
	st.Flags |= StateFlagInScope
	if h.Tracker.Res.OK() {
		st.Flags |= StateFlagAnim
	}
	if h.colliders {
		st.Flags |= StateFlagColliders
	}
	if st.DeadMask, err = DeadMask(h.m, scope.Room); err != nil {
		return nil, err
	}
	rewalk := false
	for i := 0; i < len(h.cache); i++ {
		mo := &h.cache[i]
		s, err := ReadSample(h.m, mo)
		if err != nil {
			if !errors.Is(err, actors.ErrChanged) {
				return nil, err
			}
			log.Logf(h.rep, log.Info, "enemy host: %v; walking again", err)
			h.cache = append(h.cache[:i], h.cache[i+1:]...)
			i--
			rewalk = true
			continue
		}
		if err := h.Tracker.Derive(h.m, mo.PID, &s, now); err != nil {
			return nil, err
		}
		if h.snapNext[mo.SetID] {
			s.Flags |= SlotSnap
			delete(h.snapNext, mo.SetID)
		}
		st.Slots = append(st.Slots, s.Slot)
	}
	if rewalk {
		h.walkFailed = true
		h.nextWalk = now // walk at the next tick
	}
	h.seq++
	st.Seq = h.seq
	h.lastKeep = now
	return st, nil
}

// HitApplier is the host's hit path (design "v2"): a HIT from a client is
// validated against the host's scope and its sample, rate-limited, and
// queued; the head of the queue is written into the host's block as a
// hit_apply request (role host, with the STATE's stage and room so the
// REL's scope check passes, and the host's pid of that setID's Moblin
// from the sampler's cache) for the host REL to apply through its At
// sphere (or, under WW_ENEMY_HIT_V1=1, as the v1 light-shrink kill); the
// REL's result word comes back as the ACK. While the REL is linked the
// block is rewritten every KeepaliveEvery so the REL's stale watchdog
// never trips between hits. A request the validation drops is
// acknowledged at once with a detail.
type HitApplier struct {
	m        mem.Mem
	rep      log.Reporter
	epoch    uint32
	lastSeq  map[uint8]uint16     // per client: the last hit_seq taken
	lastHit  map[uint16]time.Time // per setID: when a hit was last applied
	seq      uint32               // hit_apply seq, ++ per request
	queue    []*pendingHit        // queue[0] is in the block
	relOK    bool
	warned   bool
	blockSeq uint32
	scope    Scope // the host's scope, for keepalives (SetScope)
	lastWr   time.Time
	demoWarn bool

	// The knobs (env at construction; tests set them):
	//   V1 (WW_ENEMY_HIT_V1=1): the REL runs its v1 branch (the shrink) and
	//   the four flags pre-drop here;
	//   FallbackShrink (WW_ENEMY_HIT_FALLBACK=shrink): after two Sets that
	//   crossed nothing the REL writes the shrink;
	//   NoHitMark: the At sphere carries NoHitMark (no env; a HIT's own
	//   AtSPrm bit sets it per request too);
	//   DemoGate (WW_ENEMY_HIT_DEMO=0 disables): a hit while the host Link
	//   is in a demo (mDemo.mDemoType != 0) is dropped HDDemo.
	V1, FallbackShrink, NoHitMark, DemoGate bool
}

type pendingHit struct {
	hit   Hit
	pid   uint32
	seq   uint32
	at    time.Time
	ticks int
	stage string
	room  int8
}

// HitRate is the least time between two hits applied to one Moblin (six
// host frames at 30 Hz).
const HitRate = 200 * time.Millisecond

// hitAckTicks is how many ticks a hit_apply waits for the REL's ack.
const hitAckTicks = 30

// HitQueueDepth is how many requests the applier holds for the REL, one
// per (client, setID); the next is acknowledged HDBusy at once.
const HitQueueDepth = 4

// NewHitApplier returns an applier over m at epoch with the knobs read
// from the environment.
func NewHitApplier(m mem.Mem, rep log.Reporter, epoch uint32) *HitApplier {
	return &HitApplier{m: m, rep: rep, epoch: epoch, lastSeq: map[uint8]uint16{}, lastHit: map[uint16]time.Time{},
		V1: os.Getenv("WW_ENEMY_HIT_V1") == "1", FallbackShrink: os.Getenv("WW_ENEMY_HIT_FALLBACK") == "shrink",
		DemoGate: os.Getenv("WW_ENEMY_HIT_DEMO") != "0"}
}

// SetREL tells the applier whether the host's REL module is linked with
// this build's id: without it no request is written and every HIT is
// dropped (logged once).
func (a *HitApplier) SetREL(ok bool) { a.relOK = ok }

// Pending is how many requests are queued (the head is in the block).
func (a *HitApplier) Pending() int { return len(a.queue) }

func drop(ack *Ack, detail uint8) *Ack {
	ack.Result, ack.Detail = AckDropped, detail
	return ack
}

// Offer validates h against st (the host's latest STATE) and the
// sampler's Moblins (the pid of the hit setID) at now. It returns the
// ACK to send now (a drop, with its detail), or nil when the request was
// queued and the ACK follows from Poll.
func (a *HitApplier) Offer(h *Hit, st *State, moblins []Moblin, now time.Time) *Ack {
	ack := &Ack{Client: h.Client, Result: AckDropped, Epoch: h.Epoch, SetID: h.SetID, HitSeq: h.HitSeq}
	if st == nil || h.Epoch != a.epoch || h.Stage != st.Stage || h.Room != st.Room || st.Flags&StateFlagInScope == 0 {
		return drop(ack, HDGoGate)
	}
	if last, ok := a.lastSeq[h.Client]; ok && int16(h.HitSeq-last) <= 0 {
		return drop(ack, HDGoGate) // replayed or old
	}
	a.lastSeq[h.Client] = h.HitSeq
	var sl *Slot
	for i := range st.Slots {
		if st.Slots[i].SetID == h.SetID {
			sl = &st.Slots[i]
		}
	}
	var pid uint32
	for i := range moblins {
		if moblins[i].SetID == h.SetID {
			pid = moblins[i].PID
		}
	}
	if sl == nil || pid == 0 {
		ack.Result = AckNoActor
		return ack
	}
	ack.Health = sl.Health
	pre := SlotRagdoll | SlotDying
	if a.V1 {
		pre = SlotInvuln | SlotDefence | SlotRagdoll | SlotDying
	}
	if sl.Flags&pre != 0 {
		return drop(ack, HDGoFlags)
	}
	if t, ok := a.lastHit[h.SetID]; ok && now.Sub(t) < HitRate {
		return drop(ack, HDRate)
	}
	if !a.relOK {
		if !a.warned {
			a.warned = true
			a.rep.Log(log.Warn, "enemy host: a hit arrived but this game's REL module is not linked with this build; hits are dropped.")
		}
		return drop(ack, HDGoGate)
	}
	if a.DemoGate && a.inDemo() {
		return drop(ack, HDDemo)
	}
	if len(a.queue) >= HitQueueDepth {
		return drop(ack, HDBusy)
	}
	for _, p := range a.queue {
		if p.hit.Client == h.Client && p.hit.SetID == h.SetID {
			return drop(ack, HDBusy)
		}
	}
	a.seq++
	a.lastHit[h.SetID] = now
	a.queue = append(a.queue, &pendingHit{hit: *h, pid: pid, seq: a.seq, at: now, stage: st.Stage, room: st.Room})
	if len(a.queue) == 1 {
		if err := a.writeBlock(now); err != nil {
			log.Logf(a.rep, log.Warn, "enemy host: hit_apply write: %v", err)
			a.queue = nil
			return drop(ack, HDAborted)
		}
	}
	return nil
}

// inDemo reads the host Link's mDemo.mDemoType (addr.PlayerOffDemoType):
// nonzero during a player demo (a cutscene). A read failure counts as no
// demo and is logged once.
func (a *HitApplier) inDemo() bool {
	link, err := mem.ReadU32(a.m, addr.PlayerPtr)
	if err == nil && addr.InMEM1(link) {
		var t uint16
		if t, err = mem.ReadU16(a.m, link+addr.PlayerOffDemoType); err == nil {
			return t != 0
		}
	}
	if err != nil && !a.demoWarn {
		a.demoWarn = true
		log.Logf(a.rep, log.Warn, "enemy host: demo gate: %v (hits are not gated by it)", err)
	}
	return false
}

// SetScope gives the applier the host's scope for keepalives (the block
// carries a pending request's own stage and room while one is queued).
func (a *HitApplier) SetScope(s Scope) { a.scope = s }

// Keepalive rewrites the block every KeepaliveEvery while the REL is
// linked (SetREL), with scope as the host's current stage and room, so
// the REL's stale watchdog never releases a request in flight. The
// pipeline calls it every tick after Poll.
func (a *HitApplier) Keepalive(scope Scope, now time.Time) error {
	a.scope = scope
	if !a.relOK || (!a.lastWr.IsZero() && now.Sub(a.lastWr) < KeepaliveEvery) {
		return nil
	}
	return a.writeBlock(now)
}

// writeBlock writes the host's block: role host, no slots, the head of
// the queue as hit_apply (or the last one acked), the stage and room of
// that request (its STATE's) or of the host's scope.
func (a *HitApplier) writeBlock(now time.Time) error {
	a.blockSeq++
	a.lastWr = now
	b := &mailbox.EnemyBlock{Magic: mailbox.EnemyMagic, Seq: a.blockSeq, GoWriteMS: uint32(now.UnixMilli()),
		Flags: mailbox.EnemyFlagMirror, Layout: mailbox.EnemyLayout, Epoch: a.epoch, Role: mailbox.EnemyRoleHost,
		Stage: a.scope.Stage, Room: a.scope.Room}
	if len(a.queue) > 0 {
		p := a.queue[0]
		b.Stage, b.Room = p.stage, p.room
		b.HitApply = a.hitApply(p)
	}
	if len(b.Stage) > mailbox.EnemyStageLen {
		b.Stage = b.Stage[:mailbox.EnemyStageLen]
	}
	return mailbox.WriteEnemyBlock(a.m, b)
}

// hitApply lays p out as the block's request with the knobs' flags.
func (a *HitApplier) hitApply(p *pendingHit) mailbox.EnemyHitApply {
	h := &p.hit
	var flags uint8
	if a.V1 {
		flags |= mailbox.EnemyHAFForceV1
	}
	if a.FallbackShrink {
		flags |= mailbox.EnemyHAFFallbackShrink
	}
	if a.NoHitMark || h.AtSPrm&addr.CcAtSPrmNoHitMark != 0 {
		flags |= mailbox.EnemyHAFNoHitMark
	}
	return mailbox.EnemyHitApply{Seq: p.seq, SetID: h.SetID, Collider: h.Collider, Flags: flags, AtType: h.AtType, Atp: h.Atp,
		Se: h.Se, HitMark: h.HitMark, Spl: h.Spl, Pos: h.Pos, PID: p.pid}
}

// Poll reads the block's tail for the head request's ack. It returns the
// ACK once the REL acked it (the result word's result, detail, frames and
// Sets), or a no-actor ACK after hitAckTicks ticks without one; the next
// queued request is then written. An ack for another seq is ignored.
func (a *HitApplier) Poll(st *State, now time.Time) (*Ack, error) {
	if len(a.queue) == 0 {
		return nil, nil
	}
	p := a.queue[0]
	status, err := mailbox.ReadEnemyStatus(a.m)
	if err != nil {
		return nil, err
	}
	ack := &Ack{Client: p.hit.Client, Epoch: p.hit.Epoch, SetID: p.hit.SetID, HitSeq: p.hit.HitSeq}
	if st != nil {
		for i := range st.Slots {
			if st.Slots[i].SetID == p.hit.SetID {
				ack.Health = st.Slots[i].Health
			}
		}
	}
	switch {
	case status.HitApplyAck == p.seq:
		r := status.HitResult
		ack.Result, ack.Detail, ack.Frames, ack.Sets = r.Result, r.Detail, r.Frames, r.Sets
	case p.ticks >= hitAckTicks:
		ack.Result = AckNoActor
		log.Logf(a.rep, log.Warn, "enemy host: the REL did not ack hit_apply %d within %d ticks", p.seq, hitAckTicks)
	default:
		p.ticks++
		return nil, nil
	}
	a.queue = a.queue[1:]
	if len(a.queue) > 0 {
		if err := a.writeBlock(now); err != nil {
			log.Logf(a.rep, log.Warn, "enemy host: hit_apply write: %v", err)
		}
	}
	return ack, nil
}

// Shutdown writes magic 0 so the host REL forgets the request.
func (a *HitApplier) Shutdown() error {
	if a.blockSeq == 0 {
		return nil
	}
	return mailbox.ClearEnemyBlock(a.m)
}

func (p *pendingHit) String() string {
	return fmt.Sprintf("hit_apply %d (setID %d pid %d, client %d, hit_seq %d)", p.seq, p.hit.SetID, p.pid, p.hit.Client, p.hit.HitSeq)
}
