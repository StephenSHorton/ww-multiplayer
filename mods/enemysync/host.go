package enemysync

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// The host sampler (design "host_side", generalised by enemy-mirror-
// general.json "slots" and "identity".host_side): every tick it samples
// the mirrorable enemies of every enabled kind (Candidates over
// WW_ENEMY_MIRROR_KINDS, default DefaultMirrorKinds) in the room Link is in and
// returns the STATE to send, or a count-0 keepalive every KeepaliveEvery
// while the game is not in play or a stage change is pending. It walks
// every WalkEvery, after a validation failure and on a stage or room
// change, drops an entry whose actor changed under it (the block was
// freed and reused), and resolves each enabled kind's resource table at
// every walk so each sample's mpAnm becomes a BCK index (KindTracker). It
// never writes an actor.
//
// The STATE lists at most WireMaxSlots enemies: first the tombstones (an
// enemy last seen dying, with health <= 0 or with its ice timer running
// that vanished: sent DYING|RELEASE with WFlagTomb for TombFrames host
// frames, so a client that had not adopted it yet still kills its copy
// quietly), then the live ones by the least 3D distance to any player in
// the room (the host Link and the remote players SetPlayers gives), ties
// by Ident; under WW_ENEMY_SELECT=legacy by Ident alone (the Moblin
// prototype's lowest setIDs). Each enemy carries a handle, assigned per
// host epoch from 1 (never 0 or 0xFFFF) and kept for the epoch, which
// HIT, ACK and PHIT name it by (design D10).
const (
	WalkEvery      = 500 * time.Millisecond
	KeepaliveEvery = 250 * time.Millisecond
	// TombFrames is how many host frames (the heartbeat, spawn_trigger) a
	// tombstone is listed.
	TombFrames = 300
	// EngagedFor is how long a client HIT on an enemy, a PHIT from it or
	// (ChuChu) its At Set bit keeps the wire's WFlagEngaged on (design
	// slots.engaged_rule).
	EngagedFor = 3 * time.Second
)

// Host is the sampler's state.
type Host struct {
	m     mem.Reader
	rep   log.Reporter
	start time.Time
	epoch uint32
	seq   uint32

	scope      Scope
	cache      []Enemy
	snapNext   map[Ident]bool // Idents whose next sample snaps (just discovered)
	nextWalk   time.Time
	lastKeep   time.Time
	walkFailed bool
	// walkLogged is whether this scope's enemy count was logged: the
	// first walk of a scope logs it even at 0.
	walkLogged bool
	colliders  bool
	resWhy     [len(Kinds)]string
	resLogged  [len(Kinds)]bool

	// Kinds is the enabled set (WW_ENEMY_MIRROR_KINDS at construction),
	// Select the ordering (WW_ENEMY_SELECT), State30Hz the REL-frame gate
	// on sending (WW_ENEMY_STATE_30HZ). Tests set them.
	Kinds     KindSet
	Select    SelectMode
	State30Hz bool

	handles    map[Ident]uint16
	byHandle   map[uint16]Ident
	nextHandle uint16
	last       map[Ident]lastSeen // the last sample of each Ident sampled in this scope
	tombs      map[Ident]*tomb
	engaged    map[Ident]time.Time
	players    [][3]float32 // the remote players' positions (SetPlayers)
	listing    Listing
	relFrame   uint32
	relSeen    bool
	lastSent   time.Time

	// Tracker resolves each enabled kind's BCK table and runs the restart
	// detector.
	Tracker *KindTracker
}

// lastSeen is an Ident's last sample: its slot, and whether it was dying
// (SlotDying, health <= 0 or the ice timer running), which makes its
// disappearance a death.
type lastSeen struct {
	slot  Slot
	dying bool
}

// tomb is a vanished dying enemy, listed until beat+TombFrames.
type tomb struct {
	slot Slot
	beat uint32
}

// Listed is one enemy of the host's last STATE: its Ident, handle, wire
// slot and host pid (0 for a tombstone: its actor is gone).
type Listed struct {
	Ident  Ident
	Handle uint16
	PID    uint32
	Slot   Slot
}

// Listing is the host's last STATE as the hit paths look it up: the
// applier resolves a HIT's handle to the host pid (HitApplier.Offer), the
// remote-Link side a ring entry's attacker pid to its handle
// (RemoteHost.Tick).
type Listing struct {
	Entries []Listed
}

// ByHandle returns the entry named by handle h, or nil.
func (l *Listing) ByHandle(h uint16) *Listed {
	if l == nil || h == HandleNone || h == NoMirrorHandle {
		return nil
	}
	for i := range l.Entries {
		if l.Entries[i].Handle == h {
			return &l.Entries[i]
		}
	}
	return nil
}

// HandleOfPID returns the handle of the listed live enemy with process ID
// pid, or NoMirrorHandle (a PHIT attacker the clients have no mirror of).
func (l *Listing) HandleOfPID(pid uint32) uint16 {
	if l == nil || pid == 0 {
		return NoMirrorHandle
	}
	for i := range l.Entries {
		if l.Entries[i].PID == pid {
			return l.Entries[i].Handle
		}
	}
	return NoMirrorHandle
}

// CollidersEnabled reports whether the STATE asks the clients to keep
// their copies' colliders submitted: on unless WW_ENEMY_COLLIDERS=0.
func CollidersEnabled() bool { return os.Getenv("WW_ENEMY_COLLIDERS") != "0" }

// NewHost returns a sampler over m at epoch; start anchors host_ms. The
// colliders flag follows CollidersEnabled at construction, the kinds
// WW_ENEMY_MIRROR_KINDS, the ordering WW_ENEMY_SELECT and the send gate
// WW_ENEMY_STATE_30HZ (a value that does not parse is logged and the
// default taken).
func NewHost(m mem.Reader, rep log.Reporter, epoch uint32, start time.Time) *Host {
	h := &Host{m: m, rep: rep, epoch: epoch, start: start, colliders: CollidersEnabled(), State30Hz: State30Hz(),
		snapNext: map[Ident]bool{}, handles: map[Ident]uint16{}, byHandle: map[uint16]Ident{}, last: map[Ident]lastSeen{},
		tombs: map[Ident]*tomb{}, engaged: map[Ident]time.Time{}, Tracker: NewKindTracker()}
	var err error
	if h.Kinds, err = MirrorKinds(); err != nil {
		log.Logf(rep, log.Warn, "enemy host: %v; mirroring %s", err, h.Kinds)
	}
	if h.Select, err = SelectFromEnv(); err != nil {
		log.Logf(rep, log.Warn, "enemy host: %v; selecting by %s", err, h.Select)
	}
	return h
}

// SetColliders overrides the colliders flag (tests; the env rules
// otherwise).
func (h *Host) SetColliders(on bool) { h.colliders = on }

// SetPlayers gives the remote players' positions (the session's roster),
// which the ordering measures distance to besides the host Link's own.
func (h *Host) SetPlayers(pos [][3]float32) { h.players = append(h.players[:0], pos...) }

// Epoch is the epoch every STATE carries.
func (h *Host) Epoch() uint32 { return h.epoch }

// Scope is the scope the last Tick read.
func (h *Host) Scope() Scope { return h.scope }

// Enemies is the cached discovery, sorted by Ident.
func (h *Host) Enemies() []Enemy { return append([]Enemy(nil), h.cache...) }

// Listing is the last STATE's enemies (empty out of play).
func (h *Host) Listing() *Listing { return &h.listing }

// HostMS is the host's monotonic ms at t, the STATE's host_ms.
func (h *Host) HostMS(t time.Time) uint32 { return uint32(t.Sub(h.start).Milliseconds()) }

// HandleOf returns id's handle, assigning the next one on first sight:
// monotonic per epoch from 1, never HandleNone (0) or NoMirrorHandle
// (0xFFFF); past 0xFFFE it wraps to 1, skipping the handles still held.
func (h *Host) HandleOf(id Ident) uint16 {
	if v, ok := h.handles[id]; ok {
		return v
	}
	for {
		h.nextHandle++
		if h.nextHandle == HandleNone || h.nextHandle == NoMirrorHandle {
			h.nextHandle = 1
		}
		if _, used := h.byHandle[h.nextHandle]; !used {
			break
		}
	}
	h.handles[id], h.byHandle[h.nextHandle] = h.nextHandle, id
	return h.nextHandle
}

// NoteEngaged marks the enemy named by handle engaged at now: a client's
// HIT on it landed (the applier queued it), or a PHIT from it was sent
// (design slots.engaged_rule). An unknown handle is ignored.
func (h *Host) NoteEngaged(handle uint16, now time.Time) {
	if id, ok := h.byHandle[handle]; ok {
		h.engaged[id] = now
	}
}

// resetScope forgets what belongs to a stage and room: the cache, the last
// samples, the tombstones and the engaged marks. Handles stay (per epoch).
// The next in-play tick walks: back in the same stage and room (a void-out
// reload, a Game Over's continue) nothing else would, and until WalkEvery
// ran out every STATE would list no enemy.
func (h *Host) resetScope() {
	h.cache = nil
	h.nextWalk = time.Time{}
	h.walkLogged = false
	clear(h.last)
	clear(h.tombs)
	clear(h.engaged)
	clear(h.snapNext)
	h.listing.Entries = nil
}

// walk discovers the enemies of the scope's room and resolves each enabled
// kind's table.
func (h *Host) walk(now time.Time) error {
	h.nextWalk = now.Add(WalkEvery)
	was := h.cache
	cache, err := CandidatesSince(h.m, h.scope.Stage, h.scope.Room, h.Kinds, was)
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
			h.snapNext[cache[i].Ident] = true
		}
	}
	h.Tracker.Keep(pids)
	if len(cache) != len(was) || h.walkFailed || !h.walkLogged {
		h.walkFailed, h.walkLogged = false, true
		log.Logf(h.rep, log.Info, "enemy host: %d enemies (%s) in %s room %d", len(cache), h.Kinds, h.scope.Stage, h.scope.Room)
	}
	for i := range Kinds {
		if !h.Kinds.On[i] {
			continue
		}
		r := h.Tracker.Res[i]
		if err := r.Resolve(h.m); err != nil {
			return err
		}
		if why := r.Why(); why != h.resWhy[i] || !h.resLogged[i] {
			h.resWhy[i], h.resLogged[i] = why, true
			if why == "" {
				entry, res, _ := r.Entry()
				log.Logf(h.rep, log.Info, "enemy host: %s resource table resolved (mObjectInfo[%d], mRes 0x%08X): its animation stream is on", r.Name, entry, res)
			} else {
				log.Logf(h.rep, log.Warn, "enemy host: %s: that kind's animation stream is off until it resolves", why)
			}
		}
	}
	return nil
}

// animOK reports whether any enabled kind's table resolved: the STATE's
// StateFlagAnim (a kind whose table did not sends Bck 0xFF per slot).
func (h *Host) animOK() bool {
	for i := range Kinds {
		if h.Kinds.On[i] && h.Tracker.Res[i].OK() {
			return true
		}
	}
	return false
}

// linkPos reads the host Link's position (false when unreadable).
func (h *Host) linkPos() ([3]float32, bool) {
	var p [3]float32
	link, err := mem.ReadU32(h.m, addr.PlayerPtr)
	if err != nil || !addr.InMEM1(link) {
		return p, false
	}
	b, err := h.m.Read(link+addr.ActorOffCurPos, 12)
	if err != nil || len(b) != 12 {
		return p, false
	}
	for i := range p {
		p[i] = mem.F32BE(b[4*i:])
	}
	return p, finiteOK(p[0], p[1], p[2])
}

// dist2 is the squared 3D distance from p to the nearest of players
// (+Inf with none). It is 3D, not xz: Siren room 3's ceiling ChuChus hang
// about 1360 above Link, and an xz ranking let them hold every client
// slot while an awake ChuChu beside Link ran its own AI on the client.
func dist2(p [3]float32, players [][3]float32) float64 {
	best := math.Inf(1)
	for _, q := range players {
		dx, dy, dz := float64(p[0]-q[0]), float64(p[1]-q[1]), float64(p[2]-q[2])
		best = min(best, dx*dx+dy*dy+dz*dz)
	}
	return best
}

// Tick samples once at now. It returns the STATE to send, or nil when
// nothing is due (out of play and the keepalive not due yet; under
// State30Hz, the REL's frame not advanced and KeepaliveEvery not passed).
func (h *Host) Tick(now time.Time) (*State, error) {
	scope, err := ReadScope(h.m)
	if err != nil {
		return nil, err
	}
	changed := scope.Stage != h.scope.Stage || scope.Room != h.scope.Room
	left := h.scope.InPlay && !scope.InPlay
	h.scope = scope
	hb, err := mem.ReadU32(h.m, mailbox.SpawnTrigger)
	if err != nil {
		return nil, err
	}
	st := &State{Epoch: h.epoch, HostBeat: hb, HostMS: h.HostMS(now), Stage: scope.Stage, Room: scope.Room,
		HostKindMask: uint16(h.Kinds.Mask()), KindHash: KindHash()}
	if !scope.InPlay {
		h.resetScope()
		// The first out-of-play STATE goes at once: the in-play sends keep
		// lastKeep fresh, and a client that saw no out-of-play STATE before
		// the host came back with an empty room never held its copies.
		if !left && !h.lastKeep.IsZero() && now.Sub(h.lastKeep) < KeepaliveEvery {
			return nil, nil
		}
		h.lastKeep = now
		h.seq++
		st.Seq = h.seq
		return st, nil
	}
	if changed {
		h.resetScope()
	}
	if changed || now.After(h.nextWalk) {
		if err := h.walk(now); err != nil {
			return nil, err
		}
	}
	st.Flags |= StateFlagInScope
	if h.animOK() {
		st.Flags |= StateFlagAnim
	}
	if h.colliders {
		st.Flags |= StateFlagColliders
	}
	words, err := ReadDeadWords(h.m, scope.Room)
	if err != nil {
		return nil, err
	}
	st.SetDead(words)

	players := h.players
	if p, ok := h.linkPos(); ok {
		players = append([][3]float32{p}, h.players...)
	}
	type live struct {
		l     Listed
		dist2 float64
	}
	var lives []live
	sampled := map[Ident]bool{}
	var snapped []Ident // SlotSnap set: the one-shot flags, spent only once this STATE goes out
	rewalk := false
	for i := 0; i < len(h.cache); i++ {
		e := &h.cache[i]
		ks, err := ReadKindSample(h.m, e)
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
		if err := h.Tracker.Derive(h.m, e.PID, &ks, now); err != nil {
			return nil, err
		}
		s := h.wireSlot(e, &ks, now)
		if h.snapNext[e.Ident] {
			s.Flags |= SlotSnap
			snapped = append(snapped, e.Ident)
		}
		sampled[e.Ident] = true
		delete(h.tombs, e.Ident) // listed live again: a tombstone of this Ident is stale
		h.last[e.Ident] = lastSeen{slot: s, dying: s.Flags&SlotDying != 0 || s.Health <= 0 || ks.IceTimer != 0}
		lives = append(lives, live{l: Listed{Ident: e.Ident, Handle: s.Handle, PID: e.PID, Slot: s}, dist2: dist2(s.Pos, players)})
	}
	if rewalk {
		h.walkFailed = true
		h.nextWalk = now // walk at the next tick
	}
	// An Ident sampled before and gone now (its actor deleted, or changed
	// under the sampler) died if it was last seen dying; a Kargaroc's
	// tombstones it whatever it was last seen doing (vanishTomb).
	for id, ls := range h.last {
		if sampled[id] {
			continue
		}
		delete(h.last, id)
		if ls.dying || vanishTomb(ls.slot.Kind) {
			s := ls.slot
			s.Flags = s.Flags&^SlotSnap | SlotDying | SlotRelease
			s.WFlags = WFlagTomb
			h.tombs[id] = &tomb{slot: s, beat: hb}
		}
	}
	var tombs []Listed
	for id, tb := range h.tombs {
		if d := int32(hb - tb.beat); d < 0 || d >= TombFrames {
			delete(h.tombs, id) // expired, or the heartbeat went back (a save state)
			continue
		}
		tombs = append(tombs, Listed{Ident: id, Handle: tb.slot.Handle, Slot: tb.slot})
	}
	slices.SortFunc(tombs, func(a, b Listed) int { return compareIdent(a.Ident, b.Ident) })
	slices.SortStableFunc(lives, func(a, b live) int {
		if h.Select == SelectDistance {
			if c := cmp.Compare(a.dist2, b.dist2); c != 0 {
				return c
			}
		}
		return compareIdent(a.l.Ident, b.l.Ident)
	})
	entries := tombs
	for _, lv := range lives {
		entries = append(entries, lv.l)
	}
	if len(entries) > WireMaxSlots {
		entries = entries[:WireMaxSlots]
	}
	h.listing.Entries = entries
	for i := range entries {
		st.Slots = append(st.Slots, entries[i].Slot)
	}
	if h.State30Hz {
		rel, err := mailbox.ReadREL(h.m)
		if err != nil {
			return nil, err
		}
		if h.relSeen && rel.Frames == h.relFrame && now.Sub(h.lastSent) < KeepaliveEvery {
			return nil, nil
		}
		h.relFrame, h.relSeen = rel.Frames, true
	}
	for _, id := range snapped {
		delete(h.snapNext, id)
	}
	h.seq++
	st.Seq = h.seq
	h.lastKeep, h.lastSent = now, now
	return st, nil
}

// vanishTomb reports a kind whose enemy gets a tombstone when it vanishes
// alive (batch C go_changes BB sampler): a Kargaroc deletes itself
// without dying at the end of a path whose mArg3 is 6 (d_a_bb.cpp:825-834)
// and when the forest's andsw0 removes it (hajimarinomori_check,
// d_a_andsw0.cpp:228-243). A client copy handed back instead would fly on
// as this game's own bird; the tombstone (DYING|RELEASE, WFlagTomb) ends
// it quietly (ids >= KI always die quietly in the REL). A listed
// Kargaroc leaves the walk only so (liveSticky keeps it through a
// knockback; its type and room never change).
func vanishTomb(id KindID) bool { return id == KindBB }

// wireSlot lays a kind sample out as e's wire slot: the kind, the host
// actor's pkey, its handle and aux values (BK m48C.z and the jaw, CC
// shape_angle.x and m310; KI shape.x, shape.z, m326, m328; AM mEyeRot;
// TN dr.m48C.z; MT mode|action, the ball timer, the colour frames and
// shape.z; MO2, AM2 and BB none), a ChuChu's m316.z, a Kargaroc's
// current.angle.z and a Magtail's wiggle in the attack bytes, flags2 bit 6
// (KindF2Light) when it dies with its ice shrink timer running,
// WFlagEngaged, and WFlagDormant (dormant).
func (h *Host) wireSlot(e *Enemy, ks *KindSample, now time.Time) Slot {
	s := ks.Slot
	s.Kind, s.PKey, s.Handle, s.Aux = e.Kind, e.Ident.PKey, h.HandleOf(e.Ident), ks.Aux
	if e.Kind == KindCC || e.Kind == KindBB || e.Kind == KindMT {
		s.SetCcM316Z(ks.TiltZ) // CC m316.z; BB current.angle.z (ReadSampleBB); MT the wiggle +0x468 (ReadSampleMT)
	}
	if e.Kind == KindCC && ks.CcAtSPrm&addr.CcSPrmSet != 0 {
		h.engaged[e.Ident] = now
	}
	if s.Flags&SlotDying != 0 && ks.IceTimer != 0 {
		s.Flags2 |= KindF2Light
	}
	if t, ok := h.engaged[e.Ident]; ok && now.Sub(t) < EngagedFor {
		s.WFlags |= WFlagEngaged
	}
	if dormant(e.Kind, ks, s.Flags) {
		s.WFlags |= WFlagDormant
	}
	return s
}

// dormant is the WFlagDormant rule (design slots: a dormant copy does not
// hold its client slot against a much nearer newcomer). Per kind:
//
//   - CC: attention without LOCKON_BATTLE (a ceiling or buried ChuChu),
//     not carried.
//   - KI: the sampler's rule (allow design type_table KI): mAction WAIT
//     with mBehaviorType 0 or 1 (hanging from the ceiling), or m2D4 (it
//     waits on a switch, hidden). A Keese keeps LOCKON_BATTLE from create
//     (d_a_ki.cpp:1453), so attention is no test.
//   - AM: asleep, mAction DOUSA with mMode <= 1 on SLEEP_LOOP.
//   - AM2: attention without LOCKON_BATTLE (a statue: create and the
//     inactive return clear it, d_a_am2.cpp:1336, 1017-1022).
//   - TN: never (TN design go_changes host.go: ReadSampleTN sets no
//     dormant rule; a D_DOZOU statue is refused at adopt instead).
//   - BB, MT: never (batch C: a perched Kargaroc and a Magtail on its
//     patrol are the player's to approach).
//
// An allow kind is never dormant while carried (an Armos statue stunned
// in MAHI mode 12 gains ACTION_CARRY, not LOCKON, d_a_am2.cpp:669, and can
// be picked up) or dying (a light arrow clears attention_info,
// d_a_am.cpp:279, d_a_am2.cpp:174): the player is at it, and its copy
// keeps its slot.
func dormant(id KindID, ks *KindSample, flags uint8) bool {
	if ks.Status&addr.ActorStatusCarry != 0 {
		return false
	}
	switch id {
	case KindCC:
		return ks.Attn&addr.AttnLockonBattle == 0
	case KindKI, KindAM, KindAM2:
		return flags&SlotDying == 0 && ks.St.Dormant
	case KindTN, KindBB:
		return false
	case KindMT:
		return false
	}
	return false
}

// HitApplier is the host's hit path (design "v2"): a HIT from a client is
// validated against the host's scope and its sample, rate-limited, and
// queued; the head of the queue is written into the host's block as a
// hit_apply request (role host, with the STATE's stage and room so the
// REL's scope check passes, and the setID and pid of the host actor the
// HIT's handle names in the listing) for the host REL to apply through its At
// sphere (or, under WW_ENEMY_HIT_V1=1, as the v1 light-shrink kill); the
// REL's result word comes back as the ACK. While the REL is linked the
// block is rewritten every KeepaliveEvery so the REL's stale watchdog
// never trips between hits. A request the validation drops is
// acknowledged at once with a detail.
//
// The HIT's epoch is the host's: the client stamps the Epoch of the STATE
// it resolved the hit against (every STATE carries the host's epoch), not
// its own mirror epoch, and every ACK echoes the HIT's epoch, setID,
// handle and hit_seq so the client matches it to the hit it sent.
type HitApplier struct {
	m        mem.Mem
	rep      log.Reporter
	epoch    uint32
	lastSeq  map[uint32]uint16      // per hitKey(client, handle): the last hit_seq taken
	seqScope Scope                  // the stage and room lastSeq belongs to
	lastHit  map[uint32]time.Time   // per hitKey(client, handle): when a hit was last applied
	seq      uint32                 // hit_apply seq, ++ per request and per cancel
	queue    []*pendingHit          // queue[0] is in the block
	last     *mailbox.EnemyHitApply // with the queue empty, the block's hit_apply: a seq the REL already took, or a cancel
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
	//   is in a demo (mDemo.mDemoType != 0) is held (held, ReleaseHeld)
	//   and applied when the demo ends.
	V1, FallbackShrink, NoHitMark, DemoGate bool

	// held are the hits that came in during the host Link's demo, oldest
	// first (at most DemoHoldDepth, one per (client, handle)).
	held []heldHit

	// Attrib, when set, gives a request's extra hit_apply flags: the
	// remote-Link ATTRIB bits (RemoteHost.AttribFlags, remote-link-design.md
	// §8.6) that attribute the hit to its sender's body.
	Attrib func(h *Hit) uint8

	// Kinds is the host's enabled set, written as the block's kind_mask:
	// the REL drops a hit_apply on a kind whose bit is clear (HDKind) and
	// remote.c reads it for HF_NOT_MIRROR. NewHitApplier reads
	// WW_ENEMY_MIRROR_KINDS; the pipeline gives it the Host's.
	Kinds KindSet

	// Annex is the annex every host block carries (WW_ENEMY_ANNEX): the
	// REL's em_hit_center and em_hit_tick read its tg_hit_up and invuln
	// wait overrides from this block. HkdHi and Knobs are layout 4's hook
	// bytes for kind ids 7..14 and knob table values (ParseOverrides: the
	// host's tg_hit_up and invuln of ids 3..15). NewHitApplier reads them.
	Annex mailbox.EnemyAnnex
	HkdHi [mailbox.EnemyHkdHiKinds]uint8
	Knobs mailbox.EnemyKnobs
	knob  knobWriter
}

// heldHit is a hit held over the host Link's demo, with when it came in.
type heldHit struct {
	hit Hit
	at  time.Time
}

// A hit that lands during the host Link's demo (a chest, a door, a
// cutscene) is held instead of dropped HDDemo (coop-slice-drc.md A6): the
// client's copies stand frozen while the host's enemies do, and its
// swings would be lost. DemoHoldDepth bounds the held hits (one per
// (client, handle)); one held DemoHoldMax is dropped HDDemo.
const (
	DemoHoldDepth = 8
	DemoHoldMax   = 15 * time.Second
)

type pendingHit struct {
	hit   Hit
	pid   uint32
	setID uint16 // the host actor's (0xFFFF unkeyed), from the handle
	kind  KindID // the host slot's kind (its own STATE's, never the client's claim): hitApply's per-kind flags
	seq   uint32
	at    time.Time
	stage string
	room  int8

	// When the request became the head (its first write into the block):
	// the REL's frame counter (mailbox.ReadREL) and the wall clock, which
	// Poll's timeout counts from.
	written  bool
	relFrame uint32
	wroteAt  time.Time
}

// HitRate is the least time between two hits one client lands on one
// enemy (six host frames at 30 Hz).
const HitRate = 200 * time.Millisecond

// A hit_apply waits for the REL's ack hitAckFrames REL executes (2 s at
// 30 Hz; an open request resolves in a few dozen at most, the 8-execute
// wait included), counted on the REL's own frame counter (WWREL_FRAME,
// rel_main.c wwrel_execute), so a host whose game is paused (Dolphin
// paused, or the pause menu: the REL actor has no NOPAUSE and
// fopAc_Execute skips it, f_op_actor.cpp:175) never times out a request
// the REL could still take. hitAckBackstop is the wall-clock limit for a
// REL that never runs again. These replaced hitAckTicks (30 Go ticks,
// ~480 ms), which kept counting while the game was paused.
const (
	hitAckFrames   = 60
	hitAckBackstop = 10 * time.Second
)

// HitQueueDepth is how many requests the applier holds for the REL, one
// per (client, handle); the next is acknowledged HDBusy at once.
const HitQueueDepth = 4

// NewHitApplier returns an applier over m at epoch with the knobs read
// from the environment.
func NewHitApplier(m mem.Mem, rep log.Reporter, epoch uint32) *HitApplier {
	a := newHitApplier(m, rep, epoch)
	a.V1, a.FallbackShrink = os.Getenv("WW_ENEMY_HIT_V1") == "1", os.Getenv("WW_ENEMY_HIT_FALLBACK") == "shrink"
	a.DemoGate = os.Getenv("WW_ENEMY_HIT_DEMO") != "0"
	a.Kinds, _ = MirrorKinds()
	o, err := OverridesFromEnv()
	if err != nil {
		log.Logf(rep, log.Warn, "enemy host: %v; no annex overrides", err)
	}
	a.Annex, a.HkdHi, a.Knobs = o.Annex, o.HkdHi, o.Knobs
	return a
}

// newHitApplier is NewHitApplier with every knob off (tests set them).
// The hit_apply seq starts at epoch<<16, so a host Go restarted against a
// still-linked REL never reuses a seq the REL last acked.
func newHitApplier(m mem.Mem, rep log.Reporter, epoch uint32) *HitApplier {
	a := &HitApplier{m: m, rep: rep, epoch: epoch, seq: epoch << 16, lastSeq: map[uint32]uint16{}, lastHit: map[uint32]time.Time{}}
	a.knob.seed(uint64(epoch))
	return a
}

// hitKey is lastSeq's and lastHit's key: (client, handle) (design
// "slots"/D10: replay and HitRate keyed by handle, keyed and unkeyed
// alike). The HIT's seq is one counter per client mirror across all its
// enemies (client.go readHit), so the seqs one handle sees only grow.
// Handles are per host epoch, but lastSeq is still cleared when the
// STATE's stage or room changes (the client's slots start over).
func hitKey(client uint8, handle uint16) uint32 { return uint32(client)<<16 | uint32(handle) }

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

// Offer validates h against st (the host's latest STATE, where the HIT's
// handle names the target's slot) and listing (the host's enemies as the
// sampler last listed them, where the handle resolves to the host actor's
// pid) at now. It returns the ACK to send now (a
// drop, with its detail), or nil when the request was queued and the ACK
// follows from Poll, or held over the host Link's demo and the ACK follows
// from ReleaseHeld (or Poll). The ACK echoes the HIT's handle.
func (a *HitApplier) Offer(h *Hit, st *State, listing *Listing, now time.Time) *Ack {
	ack := hitAck(h)
	if !a.inScope(h, st) {
		return drop(ack, HDGoGate)
	}
	// A replay is only the same hit_seq again: TCP neither duplicates nor
	// reorders, and the client drains each slot's entry once (lastHits). The
	// seq is the client mirror's own counter (client.go readHit), which only
	// grows, so it never repeats for one handle when the enemy moves to
	// another block slot (the REL's per-slot hit_seq did: review 2). A seq
	// behind the last one is taken: a restarted client Go counts from 1
	// again, so a "far behind means reset" rule would drop its hits. HitRate
	// caps what a wrongly taken seq can do at one hit per enemy per client
	// per 200 ms.
	if st.Stage != a.seqScope.Stage || st.Room != a.seqScope.Room {
		clear(a.lastSeq)
		a.seqScope = Scope{Stage: st.Stage, Room: st.Room}
	}
	key := hitKey(h.Client, h.Handle)
	if last, ok := a.lastSeq[key]; ok && h.HitSeq == last {
		return drop(ack, HDGoGate) // replayed
	}
	a.lastSeq[key] = h.HitSeq
	return a.admit(h, st, listing, now, ack)
}

// hitAck is the ACK for h before a verdict: dropped, echoing the HIT.
func hitAck(h *Hit) *Ack {
	return &Ack{Client: h.Client, Result: AckDropped, Epoch: h.Epoch, SetID: h.SetID, Handle: h.Handle, HitSeq: h.HitSeq}
}

// inScope is Offer's first gate: h is of this host's epoch and of st's
// stage and room, and st is in play.
func (a *HitApplier) inScope(h *Hit, st *State) bool {
	return st != nil && h.Epoch == a.epoch && h.Stage == st.Stage && h.Room == st.Room && st.Flags&StateFlagInScope != 0
}

// admit is Offer past its scope and replay gates (and ReleaseHeld's for a
// held hit): the target, its flags, the rate, the REL, the demo (which
// holds it) and the queue.
func (a *HitApplier) admit(h *Hit, st *State, listing *Listing, now time.Time, ack *Ack) *Ack {
	key := hitKey(h.Client, h.Handle)
	// The handle names the target: its slot in st (the flags and health
	// the request is judged by) and its pid in the listing (a tombstone has
	// none: its actor is gone). The hit_apply carries that actor's own
	// setID (0xFFFF for an unkeyed one) and pid, which the REL checks with
	// its proc and room.
	var sl *Slot
	for i := range st.Slots {
		if st.Slots[i].Handle == h.Handle && h.Handle != HandleNone {
			sl = &st.Slots[i]
		}
	}
	var pid uint32
	if e := listing.ByHandle(h.Handle); e != nil {
		pid = e.PID
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
	if t, ok := a.lastHit[key]; ok && now.Sub(t) < HitRate {
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
		return a.holdHit(h, now, ack)
	}
	if len(a.queue) >= HitQueueDepth || a.queued(h) {
		return drop(ack, HDBusy)
	}
	a.seq++
	a.lastHit[key] = now
	a.queue = append(a.queue, &pendingHit{hit: *h, pid: pid, setID: sl.SetID, kind: sl.Kind, seq: a.seq, at: now, stage: st.Stage, room: st.Room})
	if len(a.queue) == 1 {
		if err := a.writeHead(now); err != nil {
			log.Logf(a.rep, log.Warn, "enemy host: hit_apply write: %v", err)
			a.queue = nil
			return drop(ack, HDAborted)
		}
	}
	return nil
}

// holdHit holds h over the host Link's demo (nil: its ACK follows from
// ReleaseHeld), or drops it HDBusy when DemoHoldDepth hits are held or
// one of the same (client, handle) is.
func (a *HitApplier) holdHit(h *Hit, now time.Time, ack *Ack) *Ack {
	if len(a.held) >= DemoHoldDepth {
		return drop(ack, HDBusy)
	}
	for _, p := range a.held {
		if p.hit.Client == h.Client && p.hit.Handle == h.Handle {
			return drop(ack, HDBusy)
		}
	}
	a.held = append(a.held, heldHit{hit: *h, at: now})
	log.Logf(a.rep, log.Info, "enemy host: hit on handle %d (client %d, hit_seq %d) held: the host Link is in a demo", h.Handle, h.Client, h.HitSeq)
	return nil
}

// Held is how many hits are held over the host Link's demo.
func (a *HitApplier) Held() int { return len(a.held) }

// ReleaseHeld runs every tick (after the HITs are offered): it returns the
// ACKs of the held hits it drops, each logged with why. One held past
// DemoHoldMax is dropped HDDemo, demo or not. Once the host Link's demo
// is over, the rest go through admit against st (the host's latest
// STATE) and listing, oldest first, as a fresh HIT would after its
// replay gate: a target gone (AckNoActor), dying or ragdolling
// (HDGoFlags), or a host out of the hit's stage and room (HDGoGate) drops
// it; one admit queues waits for Poll's ACK. A hit whose (client, handle)
// is queued, or that finds the queue full, stays held for a later tick.
func (a *HitApplier) ReleaseHeld(st *State, listing *Listing, now time.Time) []*Ack {
	if len(a.held) == 0 {
		return nil
	}
	var out []*Ack
	var keep []heldHit
	held := a.held
	a.held = nil // admit may hold again (a demo that began since the read)
	demo := a.DemoGate && a.inDemo()
	for _, p := range held {
		h := p.hit
		switch {
		case now.Sub(p.at) > DemoHoldMax:
			log.Logf(a.rep, log.Info, "enemy host: held hit on handle %d (client %d, hit_seq %d) dropped: held over %v", h.Handle, h.Client, h.HitSeq, DemoHoldMax)
			out = append(out, drop(hitAck(&h), HDDemo))
			continue
		case demo || len(a.queue) >= HitQueueDepth || a.queued(&h):
			keep = append(keep, p)
			continue
		}
		var ack *Ack
		if !a.inScope(&h, st) {
			ack = drop(hitAck(&h), HDGoGate)
		} else {
			ack = a.admit(&h, st, listing, now, hitAck(&h))
		}
		if ack != nil {
			log.Logf(a.rep, log.Info, "enemy host: held hit on handle %d (client %d, hit_seq %d) dropped after the demo: %s/%s", h.Handle, h.Client, h.HitSeq, ResultName(ack.Result), DetailName(ack.Detail))
			out = append(out, ack)
		}
	}
	a.held = append(keep, a.held...)
	return out
}

// queued reports a request of h's (client, handle) in the queue.
func (a *HitApplier) queued(h *Hit) bool {
	for _, p := range a.queue {
		if p.hit.Client == h.Client && p.hit.Handle == h.Handle {
			return true
		}
	}
	return false
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
	if err := a.checkKnobs(); err != nil {
		log.Logf(a.rep, log.Warn, "enemy host: knob table: %v", err) // tuning only: the keepalive still goes out
	}
	return a.writeBlock(now)
}

// checkKnobs reads the REL's knob echo before a keepalive (Poll checks
// it with the ack it reads anyway): the host REL validates the host's
// own table at each consume (both roles run em_consume) and its
// tg_hit_up and invuln waits come from it.
func (a *HitApplier) checkKnobs() error {
	if a.knob.gen == 0 {
		return nil
	}
	status, err := mailbox.ReadEnemyStatus(a.m)
	if err != nil {
		return err
	}
	return a.knob.check(a.m, a.rep, "enemy host", &status)
}

// writeHead writes the block for a request that just became the head and
// records the REL frame and the time Poll's timeout counts from.
func (a *HitApplier) writeHead(now time.Time) error {
	p := a.queue[0]
	rel, err := mailbox.ReadREL(a.m)
	if err != nil {
		return err
	}
	p.written, p.relFrame, p.wroteAt = true, rel.Frames, now
	return a.writeBlock(now)
}

// writeBlock writes the host's block: role host, no slots, the head of
// the queue as hit_apply, else a.last (the request the REL acked last,
// whose seq it already took, or the cancel that superseded a request Go
// timed out), so a keepalive never starts a request; the stage and room
// of the head request (its STATE's) or of the host's scope.
func (a *HitApplier) writeBlock(now time.Time) error {
	a.blockSeq++
	a.lastWr = now
	b := &mailbox.EnemyBlock{Magic: mailbox.EnemyMagic, Seq: a.blockSeq, GoWriteMS: uint32(now.UnixMilli()),
		Flags: mailbox.EnemyFlagMirror, Layout: mailbox.EnemyLayout, Epoch: a.epoch, Role: mailbox.EnemyRoleHost,
		Stage: a.scope.Stage, Room: a.scope.Room, KindMask: a.Kinds.Mask(), HkdHi: a.HkdHi,
		Annex: mailbox.EnemyAnnex{Override: a.Annex.Override, InvulnWait: a.Annex.InvulnWait, HookDisableX: a.Annex.HookDisableX, TnTgHitUp: a.Annex.TnTgHitUp}}
	gen, err := a.knob.prepare(a.m, &a.Knobs, a.epoch, a.blockSeq)
	if err != nil {
		return err
	}
	b.KnobGen = gen
	if len(a.queue) > 0 {
		p := a.queue[0]
		b.Stage, b.Room = p.stage, p.room
		b.HitApply = a.hitApply(p)
	} else if a.last != nil {
		b.HitApply = *a.last
	}
	if len(b.Stage) > mailbox.EnemyStageLen {
		b.Stage = b.Stage[:mailbox.EnemyStageLen]
	}
	return mailbox.WriteEnemyBlock(a.m, b)
}

// hitApply lays p out as the block's request with the knobs' flags, by
// the host slot's kind (p.kind):
//
//   - TN: never the remote-Link ATTRIB bits (TN design D12): an unswapped
//     consuming execute would leave the body as proc 0xA9, and an armored
//     hit would become a pure clank (d_a_tn.cpp:2916-2920, 3006-3011).
//   - MT collider code 4 (HitMtKill, batch C MT-4): never ATTRIB either.
//     It is a client custody death, not a swing; no body hit it.
//   - AM2 collider 1 with the client REL's verdict (Hit.ClientFlags
//     HitCFVerdict, R-14): mailbox.EnemyHAFVerdict, EnemyHAFBack when
//     HitCFBack, and EnemyHAFCrit when am2Crit. The host REL takes the
//     direct path on them only with its own AM2 HKD_SPECIAL clear (D10),
//     and before its ATTRIB block, so ATTRIB stays ORed here (step6's
//     remote_attrib_force never runs on a direct hit).
//
// Every other hit carries no verdict bits, whatever its ClientFlags say.
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
	if a.Attrib != nil && p.kind != KindTN && !mtKillHit(p.kind, h.Collider) {
		flags |= a.Attrib(h)
	}
	if p.kind == KindAM2 && h.Collider == HitHead && h.ClientFlags&HitCFVerdict != 0 {
		flags |= mailbox.EnemyHAFVerdict
		if h.ClientFlags&HitCFBack != 0 {
			flags |= mailbox.EnemyHAFBack
		}
		if am2Crit(h.AtType, h.CutType) {
			flags |= mailbox.EnemyHAFCrit
		}
	}
	return mailbox.EnemyHitApply{Seq: p.seq, SetID: p.setID, Collider: h.Collider, Flags: flags, AtType: h.AtType, Atp: h.Atp,
		Se: h.Se, HitMark: h.HitMark, Spl: h.Spl, Pos: h.Pos, PID: p.pid}
}

// The At types and cut types am2Crit tests (d_cc_d.h:14-38; the REL's
// EMK_AT_SWORD_CLASS and EMK_AT_SKULL_HAMMER, pinned by
// TestAm2CritPins).
const (
	// AtTypeSwordClass is week_atari_check's sword case: SWORD, MACHETE,
	// UNK800, DARKNUT_SWORD and MOBLIN_SPEAR (d_a_am2.cpp:236-240).
	AtTypeSwordClass uint32 = 0x14000C02
	// CutTypeHammerSide is daPy_py_c::CUT_TYPE_HAMMER_SIDESWING
	// (d_a_player.h:462), the hammer's m2CE 8 (d_a_am2.cpp:278-280).
	CutTypeHammerSide uint8 = 0x11
)

// am2CritCuts are the cut types week_atari_check counts as a crit for a
// sword-class hit, m2CE 1 (d_a_am2.cpp:241-260; d_a_player.h:445-476):
// CUT_EA, CUT_EB, CUT_TURN, CUT_ROLL, JUMPCUT_SWORD, JUMPCUT_STICK,
// JUMPCUT_MACHETE, BT_ROLLCUT, BT_VERTICALJUMPCUT, JUMPCUT_CLUB,
// JUMPCUT_SPEAR, CUT_EXA, CUT_EXB, CUT_EXMJ, CUT_KESA.
var am2CritCuts = [...]uint8{0x06, 0x07, 0x08, 0x09, 0x0A, 0x0C, 0x0E, 0x0F, 0x10, 0x15, 0x19, 0x1A, 0x1B, 0x1E, 0x1F}

// am2Crit reports mailbox.EnemyHAFCrit for an AM2 verdict hit (TN design
// R-15; CONTRACT.md 7: the hit_apply record has no cut-type byte, so Go
// decides and the REL's em_am2_m2ce reads one bit): a sword-class At type
// (or an unread hit object, at type 0, which the REL also takes as the
// sword) with a crit cut, which the REL turns into m2CE 1, or the Skull
// Hammer's side swing, m2CE 8. The cut type is the client Link's, read at
// the hit (client.go readHit), where vanilla reads player 0's.
func am2Crit(atType uint32, cut uint8) bool {
	switch { // em_am2_m2ce's order: the hammer first
	case atType&AtTypeSkullHammer != 0:
		return cut == CutTypeHammerSide
	case atType == 0 || atType&AtTypeSwordClass != 0:
		return slices.Contains(am2CritCuts[:], cut)
	}
	return false
}

// Poll reads the block's tail for the head request's ack. It returns the
// ACK once the REL acked it (the result word's result, detail, frames and
// Sets), or a no-actor ACK once it timed out (timedOut); the next queued
// request is then written. An ack for another seq is ignored.
//
// A request Go timed out is never applied late: Poll supersedes it with a
// cancel, a hit_apply with a new seq and nothing else (PID 0), which
// becomes a.last and is written at once when the queue is empty (else the
// next request replaces it in the block). The REL takes the cancel as a
// new request, em_hit_actor finds no actor for pid 0 (em_actor_of) and
// em_hit_apply publishes NO_ACTOR for the cancel's seq, which matches no
// queued request and is ignored. A REL that took the timed-out request
// before the cancel arrived still finishes it; that ack is ignored too.
func (a *HitApplier) Poll(st *State, now time.Time) (*Ack, error) {
	if len(a.queue) == 0 {
		return nil, nil
	}
	p := a.queue[0]
	status, err := mailbox.ReadEnemyStatus(a.m)
	if err != nil {
		return nil, err
	}
	if err := a.knob.check(a.m, a.rep, "enemy host", &status); err != nil {
		log.Logf(a.rep, log.Warn, "enemy host: knob table: %v", err)
	}
	ack := &Ack{Client: p.hit.Client, Epoch: p.hit.Epoch, SetID: p.hit.SetID, Handle: p.hit.Handle, HitSeq: p.hit.HitSeq}
	if st != nil {
		for i := range st.Slots {
			if st.Slots[i].Handle == p.hit.Handle {
				ack.Health = st.Slots[i].Health
			}
		}
	}
	cancelled := false
	if status.HitApplyAck == p.seq {
		r := status.HitResult
		ack.Result, ack.Detail, ack.Frames, ack.Sets = r.Result, r.Detail, r.Frames, r.Sets
		ha := a.hitApply(p)
		a.last = &ha
	} else {
		why, err := a.timedOut(p, now)
		if err != nil || why == "" {
			return nil, err
		}
		ack.Result, cancelled = AckNoActor, true
		a.seq++
		a.last = &mailbox.EnemyHitApply{Seq: a.seq}
		log.Logf(a.rep, log.Warn, "enemy host: the REL did not ack %v (%s); cancelled by hit_apply %d", p, why, a.seq)
	}
	a.queue = a.queue[1:]
	err = nil
	switch {
	case len(a.queue) > 0:
		err = a.writeHead(now)
	case cancelled && a.relOK:
		err = a.writeBlock(now) // the cancel, now (else the next keepalive)
	}
	if err != nil {
		log.Logf(a.rep, log.Warn, "enemy host: hit_apply write: %v", err)
	}
	return ack, nil
}

// timedOut says why the head request p has waited too long for the REL's
// ack, or "" while the ack may still come: hitAckFrames REL executes
// since p was written; the REL's frame counter behind the one at the
// write (the module relinked, and enemy_mirror_init starts acked on
// whatever seq the block holds, so p can never land; or it unlinked,
// which reads frame 0); or hitAckBackstop of wall clock.
func (a *HitApplier) timedOut(p *pendingHit, now time.Time) (string, error) {
	if !p.written {
		// writeHead failed reading the REL words: write it now, and count
		// from here.
		return "", a.writeHead(now)
	}
	rel, err := mailbox.ReadREL(a.m)
	if err != nil {
		return "", err
	}
	switch {
	case rel.Frames < p.relFrame:
		return fmt.Sprintf("the REL's frame counter went back from %d to %d", p.relFrame, rel.Frames), nil
	case rel.Frames-p.relFrame >= hitAckFrames:
		return fmt.Sprintf("%d REL frames", rel.Frames-p.relFrame), nil
	case now.Sub(p.wroteAt) >= hitAckBackstop:
		return fmt.Sprintf("%v without %d REL frames", hitAckBackstop, hitAckFrames), nil
	}
	return "", nil
}

// Shutdown writes magic 0 so the host REL forgets the request.
func (a *HitApplier) Shutdown() error {
	if a.blockSeq == 0 {
		return nil
	}
	a.knob.reset()
	return mailbox.ClearEnemyBlock(a.m)
}

func (p *pendingHit) String() string {
	return fmt.Sprintf("hit_apply %d (handle %d setID 0x%X pid %d, client %d, hit_seq %d)", p.seq, p.hit.Handle, p.setID, p.pid, p.hit.Client, p.hit.HitSeq)
}
