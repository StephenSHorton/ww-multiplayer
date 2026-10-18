package enemysync

import (
	"errors"
	"fmt"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// The host sampler (design "host_side"): every tick it samples the keyed
// mode-0 Moblins of the room Link is in and returns the STATE to send,
// or a count-0 keepalive every KeepaliveEvery while the game is not in
// play or a stage change is pending. It discovers the Moblins with a
// walk every WalkEvery, after a validation failure and on a stage or
// room change, caches (addr, pid, setID, home room) sorted by setID, and
// drops an entry whose process ID changed under it (the block was freed
// and reused). It never writes an actor.
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
}

// NewHost returns a sampler over m at epoch; start anchors host_ms.
func NewHost(m mem.Reader, rep log.Reporter, epoch uint32, start time.Time) *Host {
	return &Host{m: m, rep: rep, epoch: epoch, start: start, snapNext: map[uint16]bool{}}
}

// Epoch is the epoch every STATE carries.
func (h *Host) Epoch() uint32 { return h.epoch }

// Scope is the scope the last Tick read.
func (h *Host) Scope() Scope { return h.scope }

// Moblins is the cached discovery, sorted by setID.
func (h *Host) Moblins() []Moblin { return append([]Moblin(nil), h.cache...) }

// HostMS is the host's monotonic ms at t, the STATE's host_ms.
func (h *Host) HostMS(t time.Time) uint32 { return uint32(t.Sub(h.start).Milliseconds()) }

// walk discovers the Moblins of the scope's room.
func (h *Host) walk(now time.Time) error {
	h.nextWalk = now.Add(WalkEvery)
	was := h.cache
	cache, err := KeyedMoblins(h.m, h.scope.Room)
	if err != nil {
		return err
	}
	h.cache = cache
	for i := range cache {
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
	if len(cache) != len(was) || h.walkFailed {
		h.walkFailed = false
		log.Logf(h.rep, log.Info, "enemy host: %d keyed Moblin(s) in %s room %d", len(cache), h.scope.Stage, h.scope.Room)
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
	st.Flags |= StateFlagInScope | StateFlagAnim
	if changed || now.After(h.nextWalk) {
		if changed {
			h.cache = nil
		}
		if err := h.walk(now); err != nil {
			return nil, err
		}
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

// HitApplier is the host's v1 hit path: a HIT from a client is validated
// against the host's scope and its sample, rate-limited, and written into
// the host's block as a hit_apply request (role host, with the host's
// pid of that setID's Moblin from the sampler's cache: the REL checks
// setID and room against it) for the host REL to apply as the
// light-shrink kill; the REL's ack comes back as the ACK. A request the
// validation drops is acknowledged at once.
type HitApplier struct {
	m        mem.Mem
	rep      log.Reporter
	epoch    uint32
	lastSeq  map[uint8]uint16     // per client: the last hit_seq taken
	lastHit  map[uint16]time.Time // per setID: when a hit was last applied
	seq      uint32               // hit_apply seq, ++ per request
	pending  *pendingHit
	relOK    bool
	warned   bool
	blockSeq uint32
}

type pendingHit struct {
	hit   Hit
	pid   uint32
	seq   uint32
	at    time.Time
	ticks int
}

// HitRate is the least time between two hits applied to one Moblin (six
// host frames at 30 Hz).
const HitRate = 200 * time.Millisecond

// hitAckTicks is how many ticks a hit_apply waits for the REL's ack.
const hitAckTicks = 30

// NewHitApplier returns an applier over m at epoch.
func NewHitApplier(m mem.Mem, rep log.Reporter, epoch uint32) *HitApplier {
	return &HitApplier{m: m, rep: rep, epoch: epoch, lastSeq: map[uint8]uint16{}, lastHit: map[uint16]time.Time{}}
}

// SetREL tells the applier whether the host's REL module is linked with
// this build's id: without it no request is written and every HIT is
// dropped (logged once).
func (a *HitApplier) SetREL(ok bool) { a.relOK = ok }

// Offer validates h against st (the host's latest STATE) and the
// sampler's Moblins (the pid of the hit setID) at now. It returns the
// ACK to send now (a drop), or nil when the request was written and the
// ACK follows from Poll.
func (a *HitApplier) Offer(h *Hit, st *State, moblins []Moblin, now time.Time) *Ack {
	ack := &Ack{Client: h.Client, Result: AckDropped, Epoch: h.Epoch, SetID: h.SetID, HitSeq: h.HitSeq}
	if st == nil || h.Epoch != a.epoch || h.Stage != st.Stage || h.Room != st.Room || st.Flags&StateFlagInScope == 0 {
		return ack
	}
	if last, ok := a.lastSeq[h.Client]; ok && int16(h.HitSeq-last) <= 0 {
		return ack // replayed or old
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
	if sl.Flags&(SlotInvuln|SlotDefence|SlotRagdoll|SlotDying) != 0 {
		return ack
	}
	if t, ok := a.lastHit[h.SetID]; ok && now.Sub(t) < HitRate {
		return ack
	}
	if !a.relOK {
		if !a.warned {
			a.warned = true
			a.rep.Log(log.Warn, "enemy host: a hit arrived but this game's REL module is not linked with this build; hits are dropped.")
		}
		return ack
	}
	if a.pending != nil {
		return ack // one at a time
	}
	a.seq++
	a.lastHit[h.SetID] = now
	a.pending = &pendingHit{hit: *h, pid: pid, seq: a.seq, at: now}
	if err := a.writeBlock(now); err != nil {
		log.Logf(a.rep, log.Warn, "enemy host: hit_apply write: %v", err)
		a.pending = nil
		return ack
	}
	return nil
}

// writeBlock writes the host's block: role host, no slots, the pending
// hit_apply (or the last one).
func (a *HitApplier) writeBlock(now time.Time) error {
	a.blockSeq++
	b := &mailbox.EnemyBlock{Magic: mailbox.EnemyMagic, Seq: a.blockSeq, GoWriteMS: uint32(now.UnixMilli()),
		Flags: mailbox.EnemyFlagMirror, Layout: mailbox.EnemyLayout, Epoch: a.epoch, Role: mailbox.EnemyRoleHost}
	if p := a.pending; p != nil {
		b.HitApply = mailbox.EnemyHitApply{Seq: p.seq, SetID: p.hit.SetID, Collider: p.hit.Collider, AtType: p.hit.AtType, Atp: p.hit.Atp, Pos: p.hit.Pos, PID: p.pid}
	}
	return mailbox.WriteEnemyBlock(a.m, b)
}

// Poll reads the block's tail for the pending request's ack. It returns
// the ACK once the REL acked it, or a no-actor ACK after hitAckTicks
// ticks without one.
func (a *HitApplier) Poll(st *State) (*Ack, error) {
	p := a.pending
	if p == nil {
		return nil, nil
	}
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
		ack.Result = status.LastResult
	case p.ticks >= hitAckTicks:
		ack.Result = AckNoActor
		log.Logf(a.rep, log.Warn, "enemy host: the REL did not ack hit_apply %d within %d ticks", p.seq, hitAckTicks)
	default:
		p.ticks++
		return nil, nil
	}
	a.pending = nil
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
