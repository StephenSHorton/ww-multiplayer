package enemysync

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// mDai stands in for M_Dai room 1 (any save table serves the fake).
var mDai = fakegame.Stage{Name: "M_Dai", Table: addr.TableDRC, Room: 1}

// The placed Moblins, setIDs out of order, as the live read found M_Dai
// room 1: three keyed ones (setIDs 0 and 1 mode 1, setID 2 mode 0), a
// carried one (mode 5, refused), a statue (0xF, refused), a runtime one
// (setID 0xFFFF) and one in room 2. Placed indices: 0 setID 2, 1 setID
// 0, 2 setID 1, 3 carried, 4 runtime, 5 room 2, 6 statue.
func placeMoblins() *fakegame.Enemies {
	return fakegame.PlaceEnemies(
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 2, Proc: stage.ProcMO2},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 0, Proc: stage.ProcMO2, Mode: 1},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 1, Proc: stage.ProcMO2, Mode: 1},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 3, Proc: stage.ProcMO2, Mode: addr.Mo2ModeCarried},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: addr.ActorSetIDNone, Proc: stage.ProcMO2},
		fakegame.Enemy{Stage: mDai.Name, Room: 2, SetID: 4, Proc: stage.ProcMO2},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 5, Proc: stage.ProcMO2, Mode: addr.Mo2ModeStatue},
	)
}

// moblinRoom is M_Dai with Link in room 1 (room 2 a neighbour) and the
// placed Moblins executing.
func moblinRoom(t *testing.T) (*fakegame.FakeGame, *mem.FakeMem, *fakegame.Enemies) {
	t.Helper()
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	if err := g.LoadStage(mDai); err != nil {
		t.Fatal(err)
	}
	if err := g.EnterRoom(1, 2); err != nil {
		t.Fatal(err)
	}
	es := placeMoblins()
	frames(t, g, es, 4)
	return g, m, es
}

func frames(t *testing.T, g *fakegame.FakeGame, es *fakegame.Enemies, n int) {
	t.Helper()
	for range n {
		if err := g.FrameWith(es); err != nil {
			t.Fatal(err)
		}
	}
}

func store(t *testing.T, m *mem.FakeMem, a uint32, b []byte) {
	t.Helper()
	if err := m.Store(a, b); err != nil {
		t.Fatal(err)
	}
}

func f32s(vs ...float32) []byte {
	b := make([]byte, 4*len(vs))
	for i, v := range vs {
		binary.BigEndian.PutUint32(b[4*i:], math.Float32bits(v))
	}
	return b
}

func setIDs(st *State) []uint16 {
	var out []uint16
	for _, s := range st.Slots {
		out = append(out, s.SetID)
	}
	return out
}

func eq16(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestHostSampler: exactly the three keyed mode-0 Moblins of Link's room,
// sorted by setID, with their fields; the first sample snaps; without a
// Mo2 resource table the STATE has no anim flag and every Bck is
// unresolved (a readable morf still gives its attribute, an unset one
// 0xFF); a PID change under the sampler drops the entry; the title
// screen gives a count-0 keepalive every KeepaliveEvery; the ice timer,
// the death action and health 0 mark dying; the zone bit sets dead_mask.
func TestHostSampler(t *testing.T) {
	g, m, es := moblinRoom(t)
	a0, _ := es.Of(1) // setID 0
	store(t, m, a0.Addr+addr.ActorOffCurPos, f32s(-1500.5, 250, 3000))
	store(t, m, a0.Addr+addr.ActorOffCurAngle+2, []byte{0x40, 0x00})
	store(t, m, a0.Addr+addr.Mo2OffDRMode, []byte{0, 2, 0, 10}) // mode 2, action 10 (defence)
	store(t, m, a0.Addr+addr.Mo2OffAttackType, []byte{0, 0, 0, 2, 0, 0, 0, 1})
	store(t, m, a0.Addr+addr.Mo2OffInvuln, []byte{0, 7})
	store(t, m, a0.Addr+addr.Mo2OffTilt, []byte{0xFF, 0xFE})
	store(t, m, a0.Addr+addr.Mo2OffBareHanded, []byte{1})
	const morf = uint32(0x80C00000)
	store(t, m, a0.Addr+addr.Mo2OffMorf, binary.BigEndian.AppendUint32(nil, morf))
	store(t, m, morf+addr.McaMorfOffRate, f32s(1.5))
	store(t, m, morf+addr.McaMorfOffFrame, f32s(12.25))

	start := time.Unix(1000, 0)
	h := NewHost(m, log.Discard{}, 7, start)
	st, err := h.Tick(start)
	if err != nil {
		t.Fatal(err)
	}
	if st == nil || !eq16(setIDs(st), []uint16{0, 1, 2}) || st.Flags&StateFlagInScope == 0 || st.Epoch != 7 || st.Seq != 1 || st.Stage != "M_Dai" || st.Room != 1 || st.DeadMask != 0 {
		t.Fatalf("first tick: %+v", st)
	}
	if st.Flags&StateFlagAnim != 0 {
		t.Errorf("the anim flag without a Mo2 table: %02X", st.Flags)
	}
	if mos := h.Moblins(); len(mos) != 3 || mos[0].Mode != 1 || mos[1].Mode != 1 || mos[2].Mode != 0 || mos[0].PID != a0.PID {
		t.Fatalf("cache: %+v", mos)
	}
	s0 := st.Slots[0]
	want := Slot{SetID: 0, Proc: ProcMO2, Pos: [3]float32{-1500.5, 250, 3000}, AngleY: 0x4000, Tilt: -2, Action: 10, Mode: 2, AttackType: 2, AttackPhase: 1,
		MorfFrame: 12.25, MorfRate: 1.5, Health: 4, Flags: SlotSnap | SlotInvuln | SlotDefence | SlotBareHanded,
		Bck: addr.Mo2BckNone, AnmAttr: AnmAttrNone} // the morf is zeroed but readable; no table, no Bck
	if s0 != want {
		t.Errorf("slot 0\n got %+v\nwant %+v", s0, want)
	}
	for _, s := range st.Slots[1:] {
		if s.Flags != SlotSnap || s.Health != 4 || s.Proc != ProcMO2 || s.Bck != addr.Mo2BckNone || s.AnmAttr != AnmAttrUnknown {
			t.Errorf("slot %d: %+v", s.SetID, s)
		}
	}
	// The second tick: no snap, seq 2, host_ms moved.
	st, err = h.Tick(start.Add(16 * time.Millisecond))
	if err != nil || st == nil || st.Seq != 2 || st.HostMS != 16 {
		t.Fatalf("second tick: %+v %v", st, err)
	}
	for _, s := range st.Slots {
		if s.Flags&SlotSnap != 0 {
			t.Errorf("slot %d snaps again", s.SetID)
		}
	}

	// Dying: the ice timer on setID 2, the death action on setID 1,
	// health 0 on setID 0; and setID 2's zone bit.
	a1, _ := es.Of(2)
	a2, _ := es.Of(0)
	store(t, m, a2.Addr+addr.Mo2OffIceShrink, []byte{1})
	store(t, m, a1.Addr+addr.Mo2OffDRAction, []byte{0, 20})
	store(t, m, a0.Addr+addr.ActorOffHealth, []byte{0})
	if err := g.OnActor(2, 1); err != nil {
		t.Fatal(err)
	}
	st, _ = h.Tick(start.Add(32 * time.Millisecond))
	for i, s := range st.Slots {
		if s.Flags&SlotDying == 0 {
			t.Errorf("slot %d (setID %d) not dying: %+v", i, s.SetID, s)
		}
	}
	if st.DeadMask != 1<<2 {
		t.Errorf("dead_mask %08b, want bit 2", st.DeadMask)
	}

	// The block of setID 1 freed and reused by a runtime Moblin: the
	// entry is dropped at the tick that sees it, and the walk after it
	// does not take the new actor (setID 0xFFFF).
	if _, err := es.FreeAndReuse(g, a1.PID, stage.ProcMO2); err != nil {
		t.Fatal(err)
	}
	st, err = h.Tick(start.Add(48 * time.Millisecond))
	if err != nil || !eq16(setIDs(st), []uint16{0, 2}) {
		t.Fatalf("after the reuse: %v %v", setIDs(st), err)
	}
	st, _ = h.Tick(start.Add(64 * time.Millisecond))
	if !eq16(setIDs(st), []uint16{0, 2}) || len(h.Moblins()) != 2 {
		t.Errorf("after the walk: %v, cache %v", setIDs(st), h.Moblins())
	}

	// The title screen: a count-0 keepalive, then nothing for
	// KeepaliveEvery, then another.
	if err := g.LoadStage(fakegame.Stage{Name: addr.StageTitle, Table: addr.TableSea, NoPlayer: true}); err != nil {
		t.Fatal(err)
	}
	st, err = h.Tick(start.Add(time.Second))
	if err != nil || st == nil || len(st.Slots) != 0 || st.Flags&StateFlagInScope != 0 || st.Stage != addr.StageTitle {
		t.Fatalf("title: %+v %v", st, err)
	}
	if st, _ = h.Tick(start.Add(time.Second + 100*time.Millisecond)); st != nil {
		t.Errorf("a keepalive before KeepaliveEvery: %+v", st)
	}
	if st, _ = h.Tick(start.Add(time.Second + KeepaliveEvery)); st == nil || len(st.Slots) != 0 {
		t.Errorf("no keepalive at KeepaliveEvery: %+v", st)
	}
	if n := len(h.Moblins()); n != 0 {
		t.Errorf("cache kept %d Moblins off play", n)
	}
}

// TestHostSamplerAnim: the Mo2 table resolves at a walk, not per tick;
// once it has, the STATE carries the anim flag and a Moblin's mpAnm
// becomes its BCK index, with setAnm's morf and a restart counter that
// moves when the animation changes; a Moblin without a morf stays
// unresolved.
func TestHostSamplerAnim(t *testing.T) {
	g, m, es := moblinRoom(t)
	a0, _ := es.Of(1) // setID 0
	if err := es.SetMo2Anm(g, a0.PID, addr.Mo2BckWait, AnmAttrLoop, 3, 1, 15); err != nil {
		t.Fatal(err)
	}
	start := time.Unix(1000, 0)
	h := NewHost(m, log.Discard{}, 7, start)
	st, err := h.Tick(start)
	if err != nil || st.Flags&StateFlagAnim != 0 || st.Slots[0].Bck != addr.Mo2BckNone || st.Slots[0].AnmAttr != AnmAttrLoop || st.Slots[0].Morf != 15 {
		t.Fatalf("no table: flags %02X slot %+v %v", st.Flags, st.Slots[0], err)
	}
	// The table loads; the sampler sees it at its next walk only.
	g.LoadMo2Res()
	if st, _ = h.Tick(start.Add(16 * time.Millisecond)); st.Flags&StateFlagAnim != 0 || st.Slots[0].Bck != addr.Mo2BckNone {
		t.Errorf("between walks: flags %02X bck %02X", st.Flags, st.Slots[0].Bck)
	}
	now := start.Add(WalkEvery + 16*time.Millisecond)
	st, err = h.Tick(now)
	if err != nil || st.Flags&StateFlagAnim == 0 {
		t.Fatalf("after the walk: flags %02X %v (%s)", st.Flags, err, h.Tracker.Res.Why())
	}
	if s := st.Slots[0]; s.Bck != addr.Mo2BckWait || s.AnmAttr != AnmAttrLoop || s.AnmSeq != 0 || s.Morf != 15 || s.MorfFrame != 3 || s.MorfRate != 1 {
		t.Errorf("WAIT: %+v", s)
	}
	for _, s := range st.Slots[1:] {
		if s.Bck != addr.Mo2BckNone || s.AnmAttr != AnmAttrUnknown {
			t.Errorf("setID %d without a morf: bck %02X attr %02X", s.SetID, s.Bck, s.AnmAttr)
		}
	}
	// ATATE (0x45), a once-anim with morf 5: a restart.
	if err := es.SetMo2Anm(g, a0.PID, 0x45, AnmAttrNone, 0, 1, 5); err != nil {
		t.Fatal(err)
	}
	st, _ = h.Tick(now.Add(16 * time.Millisecond))
	if s := st.Slots[0]; s.Bck != 0x45 || s.AnmAttr != AnmAttrNone || s.AnmSeq != 1 || s.Morf != 5 {
		t.Errorf("ATATE: %+v", s)
	}
}

// TestHostColliders: the STATE asks for colliders by default, not under
// WW_ENEMY_COLLIDERS=0, and SetColliders overrides either.
func TestHostColliders(t *testing.T) {
	_, m, _ := moblinRoom(t)
	start := time.Unix(1000, 0)
	t.Setenv("WW_ENEMY_COLLIDERS", "")
	h := NewHost(m, log.Discard{}, 7, start)
	if st, err := h.Tick(start); err != nil || st.Flags&StateFlagColliders == 0 {
		t.Fatalf("default: %+v %v", st, err)
	}
	h.SetColliders(false)
	if st, _ := h.Tick(start.Add(16 * time.Millisecond)); st.Flags&StateFlagColliders != 0 {
		t.Errorf("SetColliders(false): flags %02X", st.Flags)
	}
	t.Setenv("WW_ENEMY_COLLIDERS", "0")
	h = NewHost(m, log.Discard{}, 7, start)
	if st, _ := h.Tick(start); st.Flags&StateFlagColliders != 0 {
		t.Errorf("WW_ENEMY_COLLIDERS=0: flags %02X", st.Flags)
	}
	h.SetColliders(true)
	if st, _ := h.Tick(start.Add(16 * time.Millisecond)); st.Flags&StateFlagColliders == 0 {
		t.Errorf("SetColliders(true): flags %02X", st.Flags)
	}
}

// TestHostSamplerOutOfRoom: a Moblin of another room is never sampled,
// and a room change walks again.
func TestHostSamplerOutOfRoom(t *testing.T) {
	g, m, es := moblinRoom(t)
	h := NewHost(m, log.Discard{}, 1, time.Unix(0, 0))
	st, err := h.Tick(time.Unix(1, 0))
	if err != nil || !eq16(setIDs(st), []uint16{0, 1, 2}) {
		t.Fatalf("room 1: %v %v", setIDs(st), err)
	}
	if err := g.EnterRoom(2, 1); err != nil {
		t.Fatal(err)
	}
	frames(t, g, es, 2)
	st, err = h.Tick(time.Unix(1, int64(16*time.Millisecond)))
	if err != nil || !eq16(setIDs(st), []uint16{4}) || st.Room != 2 {
		t.Fatalf("room 2: %v room %d %v", setIDs(st), st.Room, err)
	}
}

// hitRig is a host applier at epoch 7 with the REL linked (every knob
// off), recording writes, with the sampler's five Moblins of M_Dai room 1
// (setIDs 0..4, pids 420..424) and their STATE: setID 1 invulnerable,
// 2 in defence, 3 ragdolling, 4 dying.
func hitRig(t *testing.T) (*mem.FakeMem, *HitApplier, *State, []Moblin) {
	t.Helper()
	m := mem.NewFakeMem()
	m.Record(true)
	a := newHitApplier(m, log.Discard{}, 7)
	a.SetREL(true)
	st := &State{Flags: StateFlagInScope, Epoch: 7, Stage: "M_Dai", Room: 1}
	var mos []Moblin
	for i, f := range []uint8{0, SlotInvuln, SlotDefence, SlotRagdoll, SlotDying} {
		mos = append(mos, Moblin{Addr: 0x80AA0000 + 0x3000*uint32(i), PID: 420 + uint32(i), SetID: uint16(i), HomeRoom: 1, Mode: 1})
		st.Slots = append(st.Slots, Slot{SetID: uint16(i), Health: int8(4 - i%2), Flags: f})
	}
	return m, a, st, mos
}

// hostHit is client's hit_seq seq on setID in M_Dai room 1 at epoch 7.
func hostHit(client uint8, setID, seq uint16) *Hit {
	return &Hit{Client: client, Collider: ColliderHead, Epoch: 7, Stage: "M_Dai", Room: 1, SetID: setID, HitSeq: seq}
}

// hostBlocks decodes every block write m recorded since the last reset.
func hostBlocks(t *testing.T, m *mem.FakeMem) []mailbox.EnemyBlock {
	t.Helper()
	var out []mailbox.EnemyBlock
	for _, w := range m.Writes() {
		if w.Addr != mailbox.EnemyBlockAddr || len(w.Data) != mailbox.EnemyGoSpan {
			t.Fatalf("a write that is not the host block: 0x%08X %d B", w.Addr, len(w.Data))
		}
		blk, tail, err := mailbox.DecodeEnemyBlock(w.Data)
		if err != nil || tail != blk.Seq {
			t.Fatalf("block: %+v tail %d %v", blk, tail, err)
		}
		out = append(out, blk)
	}
	return out
}

// hostAckREL stores the REL's result word and then its ack of seq.
func hostAckREL(t *testing.T, m *mem.FakeMem, seq uint32, r mailbox.EnemyHitResult) {
	t.Helper()
	store(t, m, mailbox.EnemyBlockAddr+mailbox.EnemyHitResultOff, []byte{r.Result, r.Detail, r.Frames, r.Sets})
	store(t, m, mailbox.EnemyBlockAddr+mailbox.EnemyHitApplyAckOff, binary.BigEndian.AppendUint32(nil, seq))
}

// TestHitApplier: a valid HIT is written as hit_apply (role host) with the
// STATE's stage and room, whatever the host's scope (the scope defect:
// without them the REL refused every host block), and its seq starts at
// epoch<<16+1; Poll ignores an ack for another seq and maps the REL's
// result word into the ACK; a keepalive after it carries the acked
// request, never seq 0; a replayed, stale, out-of-scope or unknown HIT is
// acknowledged at once with its detail; HitRate holds per Moblin; a
// request the REL never acks is no-actor after hitAckTicks polls.
func TestHitApplier(t *testing.T) {
	m, a, st, mos := hitRig(t)
	now := time.Unix(1000, 0)
	a.SetScope(Scope{Stage: "sea", Room: 5, InPlay: true})
	hit := &Hit{Client: 3, Collider: ColliderHead, Epoch: 7, Stage: "M_Dai", Room: 1, SetID: 0, HitSeq: 1, AtType: 0x80000, Atp: 2,
		Pos: [3]float32{1, 2, 3}, Se: 1, HitMark: 0xF, Spl: 1}
	if ack := a.Offer(hit, st, mos, now); ack != nil {
		t.Fatalf("a valid hit was acked at once: %+v", ack)
	}
	const seq1 = 7<<16 + 1
	blks := hostBlocks(t, m)
	if len(blks) != 1 {
		t.Fatalf("%d block writes, want 1", len(blks))
	}
	b := blks[0]
	if b.Magic != mailbox.EnemyMagic || b.Role != mailbox.EnemyRoleHost || b.Epoch != 7 || b.Layout != mailbox.EnemyLayout ||
		b.Flags != mailbox.EnemyFlagMirror || len(b.Slots) != 0 {
		t.Errorf("host block: %+v", b)
	}
	if b.Stage != st.Stage || b.Room != st.Room {
		t.Errorf("the request's block is in %s room %d, its STATE in %s room %d", b.Stage, b.Room, st.Stage, st.Room)
	}
	want := mailbox.EnemyHitApply{Seq: seq1, SetID: 0, Collider: ColliderHead, AtType: 0x80000, Atp: 2, Se: 1, HitMark: 0xF, Spl: 1,
		Pos: [3]float32{1, 2, 3}, PID: 420}
	if b.HitApply != want {
		t.Errorf("hit_apply\n got %+v\nwant %+v", b.HitApply, want)
	}
	if a.Pending() != 1 {
		t.Errorf("pending %d", a.Pending())
	}

	// No ack yet, then an ack word for another seq: both ignored.
	if ack, err := a.Poll(st, now); err != nil || ack != nil {
		t.Fatalf("poll: %+v %v", ack, err)
	}
	hostAckREL(t, m, seq1-1, mailbox.EnemyHitResult{Result: AckShrink})
	if ack, err := a.Poll(st, now); err != nil || ack != nil {
		t.Fatalf("an ack for another seq: %+v %v", ack, err)
	}
	// The REL's result word: the fallback shrink after five executes and
	// two Sets (four distinct bytes pin their order).
	hostAckREL(t, m, seq1, mailbox.EnemyHitResult{Result: AckShrink, Detail: HDFallbackShrink, Frames: 5, Sets: 2})
	ack, err := a.Poll(st, now)
	if err != nil || ack == nil || *ack != (Ack{Client: 3, Result: AckShrink, Epoch: 7, SetID: 0, HitSeq: 1, Health: 4, Detail: HDFallbackShrink, Frames: 5, Sets: 2}) {
		t.Fatalf("acked: %+v %v", ack, err)
	}
	if a.Pending() != 0 {
		t.Errorf("pending %d after the ack", a.Pending())
	}
	// The keepalive after it: the host's scope, the acked request.
	m.ResetWrites()
	if err := a.Keepalive(Scope{Stage: "sea", Room: 5, InPlay: true}, now.Add(KeepaliveEvery)); err != nil {
		t.Fatal(err)
	}
	if blks := hostBlocks(t, m); len(blks) != 1 || blks[0].Stage != "sea" || blks[0].Room != 5 || blks[0].HitApply != want {
		t.Errorf("keepalive after the ack: %+v", blks)
	}

	for _, c := range []struct {
		name   string
		hit    *Hit
		result uint8
		detail uint8
	}{
		{"replay", hostHit(3, 0, 1), AckDropped, HDGoGate},
		{"stale epoch", &Hit{Client: 3, Epoch: 6, Stage: "M_Dai", Room: 1, SetID: 0, HitSeq: 5}, AckDropped, HDGoGate},
		{"other stage", &Hit{Client: 3, Epoch: 7, Stage: "sea", Room: 1, SetID: 0, HitSeq: 6}, AckDropped, HDGoGate},
		{"other room", &Hit{Client: 3, Epoch: 7, Stage: "M_Dai", Room: 2, SetID: 0, HitSeq: 7}, AckDropped, HDGoGate},
		{"unknown setID", hostHit(3, 9, 8), AckNoActor, HDNone},
	} {
		ack := a.Offer(c.hit, st, mos, now.Add(time.Second))
		if ack == nil || ack.Result != c.result || ack.Detail != c.detail || ack.Client != c.hit.Client || ack.HitSeq != c.hit.HitSeq {
			t.Errorf("%s: %+v", c.name, ack)
		}
	}
	if ack := a.Offer(hostHit(3, 0, 9), nil, mos, now.Add(time.Second)); ack == nil || ack.Detail != HDGoGate {
		t.Errorf("no STATE: %+v", ack)
	}
	out := *st
	out.Flags = 0
	if ack := a.Offer(hostHit(3, 0, 10), &out, mos, now.Add(time.Second)); ack == nil || ack.Detail != HDGoGate {
		t.Errorf("out of scope: %+v", ack)
	}

	// Rate: a second hit on setID 0 within HitRate is dropped; after it,
	// taken; the REL never acks: no-actor after hitAckTicks.
	if ack := a.Offer(hostHit(4, 0, 1), st, mos, now.Add(HitRate/2)); ack == nil || ack.Result != AckDropped || ack.Detail != HDRate {
		t.Errorf("within HitRate: %+v", ack)
	}
	if ack := a.Offer(hostHit(4, 0, 2), st, mos, now.Add(2*HitRate)); ack != nil {
		t.Errorf("after HitRate: %+v", ack)
	}
	for i := range hitAckTicks {
		if ack, err := a.Poll(st, now); err != nil || ack != nil {
			t.Fatalf("poll %d: %+v %v", i, ack, err)
		}
	}
	got, err := a.Poll(st, now)
	if err != nil || got == nil || got.Result != AckNoActor || got.Client != 4 || got.HitSeq != 2 {
		t.Errorf("timed out: %+v %v", got, err)
	}
	m.ResetWrites()
	if err := a.Keepalive(Scope{Stage: "M_Dai", Room: 1, InPlay: true}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if blks := hostBlocks(t, m); len(blks) != 1 || blks[0].HitApply.Seq != seq1+1 {
		t.Errorf("keepalive after the timeout: %+v", blks)
	}
	// Without the REL, every hit is dropped.
	a.SetREL(false)
	if ack := a.Offer(hostHit(5, 0, 1), st, mos, now.Add(time.Minute)); ack == nil || ack.Result != AckDropped || ack.Detail != HDGoGate {
		t.Errorf("no REL: %+v", ack)
	}
	// A setID the sampler has no pid for is no-actor.
	a.SetREL(true)
	if ack := a.Offer(hostHit(6, 0, 1), st, nil, now.Add(2*time.Minute)); ack == nil || ack.Result != AckNoActor {
		t.Errorf("no pid: %+v", ack)
	}
	m.ResetWrites()
	if err := a.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if w := m.Writes(); len(w) != 1 || w[0].Addr != mailbox.EnemyBlockAddr || len(w[0].Data) != 4 || binary.BigEndian.Uint32(w[0].Data) != 0 {
		t.Errorf("shutdown writes: %+v", w)
	}
}

// TestHitApplierSeqEpoch: the hit_apply seq starts at epoch<<16+1, so a
// host restarted with a new epoch never reuses one a linked REL acked.
func TestHitApplierSeqEpoch(t *testing.T) {
	for _, epoch := range []uint32{1, 7, 0xFFFF} {
		m := mem.NewFakeMem()
		m.Record(true)
		a := newHitApplier(m, log.Discard{}, epoch)
		a.SetREL(true)
		_, _, st, mos := hitRig(t)
		st.Epoch = epoch
		h := hostHit(1, 0, 1)
		h.Epoch = epoch
		if ack := a.Offer(h, st, mos, time.Unix(1000, 0)); ack != nil {
			t.Fatalf("epoch %d: %+v", epoch, ack)
		}
		if blks := hostBlocks(t, m); len(blks) != 1 || blks[0].HitApply.Seq != epoch<<16+1 || blks[0].Epoch != epoch {
			t.Errorf("epoch %d: %+v", epoch, blks)
		}
	}
}

// TestHitApplierPreDrop: v2 pre-drops only a ragdolling or dying Moblin
// (an invulnerable one waits in the REL, a defending one parries); V1
// pre-drops all four flags and asks the REL for the v1 branch.
func TestHitApplierPreDrop(t *testing.T) {
	now := time.Unix(1000, 0)
	m, a, st, mos := hitRig(t)
	for setID, want := range []uint8{0, 0, 0, HDGoFlags, HDGoFlags} {
		ack := a.Offer(hostHit(1, uint16(setID), uint16(setID+1)), st, mos, now)
		switch {
		case want == 0 && ack != nil:
			t.Errorf("v2 setID %d (flags %02X) dropped: %+v", setID, st.Slots[setID].Flags, ack)
		case want != 0 && (ack == nil || ack.Result != AckDropped || ack.Detail != want || ack.Health != st.Slots[setID].Health):
			t.Errorf("v2 setID %d (flags %02X): %+v", setID, st.Slots[setID].Flags, ack)
		}
	}
	if a.Pending() != 3 {
		t.Errorf("v2 pending %d, want 3", a.Pending())
	}
	if blks := hostBlocks(t, m); len(blks) != 1 || blks[0].HitApply.Flags != 0 {
		t.Errorf("v2 block: %+v", blks)
	}

	m, a, st, mos = hitRig(t)
	a.V1 = true
	for setID := 1; setID <= 4; setID++ {
		if ack := a.Offer(hostHit(1, uint16(setID), uint16(setID)), st, mos, now); ack == nil || ack.Result != AckDropped || ack.Detail != HDGoFlags {
			t.Errorf("V1 setID %d (flags %02X): %+v", setID, st.Slots[setID].Flags, ack)
		}
	}
	if ack := a.Offer(hostHit(1, 0, 5), st, mos, now); ack != nil {
		t.Fatalf("V1 clean: %+v", ack)
	}
	if blks := hostBlocks(t, m); len(blks) != 1 || blks[0].HitApply.Flags != mailbox.EnemyHAFForceV1 {
		t.Errorf("V1 block: %+v", blks)
	}
}

// TestHitApplierQueue: up to HitQueueDepth requests wait, one per
// (client, setID), and only the head is in the block; the next one, and
// a second one for a (client, setID) already queued, is HDBusy; popping
// the head writes the next request with its own stage and room.
func TestHitApplierQueue(t *testing.T) {
	m, a, st, mos := hitRig(t)
	now := time.Unix(1000, 0)
	for i := range st.Slots {
		st.Slots[i].Flags = 0 // none pre-dropped
	}
	for i := range HitQueueDepth {
		if ack := a.Offer(hostHit(uint8(i+1), uint16(i), 1), st, mos, now); ack != nil {
			t.Fatalf("request %d: %+v", i, ack)
		}
	}
	if a.Pending() != HitQueueDepth {
		t.Fatalf("pending %d", a.Pending())
	}
	if blks := hostBlocks(t, m); len(blks) != 1 || blks[0].HitApply.Seq != 7<<16+1 {
		t.Errorf("the queue's writes: %+v", blks)
	}
	if ack := a.Offer(hostHit(9, 4, 1), st, mos, now); ack == nil || ack.Result != AckDropped || ack.Detail != HDBusy {
		t.Errorf("a full queue: %+v", ack)
	}
	// The REL acks the head; the next request goes into the block. A
	// later STATE in another room does not move it: it keeps its own.
	m.ResetWrites()
	hostAckREL(t, m, 7<<16+1, mailbox.EnemyHitResult{Result: AckAppliedAt, Frames: 1, Sets: 1})
	later := *st
	later.Room = 2
	ack, err := a.Poll(&later, now)
	if err != nil || ack == nil || ack.Client != 1 || ack.Result != AckAppliedAt || ack.Frames != 1 || ack.Sets != 1 {
		t.Fatalf("head ack: %+v %v", ack, err)
	}
	blks := hostBlocks(t, m)
	if len(blks) != 1 || blks[0].HitApply.Seq != 7<<16+2 || blks[0].HitApply.SetID != 1 || blks[0].HitApply.PID != 421 || blks[0].Stage != "M_Dai" || blks[0].Room != 1 {
		t.Errorf("the next request: %+v", blks)
	}
	// Client 2's setID 1 is still queued: busy, even past HitRate; another
	// client on setID 1 is taken.
	if ack := a.Offer(hostHit(2, 1, 2), st, mos, now.Add(HitRate)); ack == nil || ack.Result != AckDropped || ack.Detail != HDBusy {
		t.Errorf("a duplicate (client, setID): %+v", ack)
	}
	if ack := a.Offer(hostHit(9, 1, 2), st, mos, now.Add(HitRate)); ack != nil {
		t.Errorf("another client on that setID: %+v", ack)
	}
	if a.Pending() != HitQueueDepth {
		t.Errorf("pending %d", a.Pending())
	}
}

// TestHitApplierKeepalive: while the REL is linked the block is
// rewritten every KeepaliveEvery after the last write of any kind, with
// the host's scope, or a queued request's own; without the REL nothing is
// written.
func TestHitApplierKeepalive(t *testing.T) {
	m, a, st, mos := hitRig(t)
	t0 := time.Unix(1000, 0)
	sea := Scope{Stage: "sea", Room: 5, InPlay: true}
	a.SetREL(false)
	for i := range 40 {
		if err := a.Keepalive(sea, t0.Add(time.Duration(i)*50*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
	}
	if w := m.Writes(); len(w) != 0 {
		t.Fatalf("keepalives without the REL: %d writes", len(w))
	}
	a.SetREL(true)
	t0 = t0.Add(time.Minute)
	for _, s := range []struct {
		at    time.Duration
		offer bool
		want  int // writes so far
	}{
		{0, false, 1}, // the first keepalive is due at once
		{100, false, 1},
		{249, false, 1},
		{250, false, 2},
		{300, true, 3}, // the request's own write
		{400, false, 3},
		{549, false, 3},
		{550, false, 4},
		{800, false, 5},
	} {
		now := t0.Add(s.at * time.Millisecond)
		if s.offer {
			if ack := a.Offer(hostHit(1, 0, 1), st, mos, now); ack != nil {
				t.Fatalf("offer: %+v", ack)
			}
		} else if err := a.Keepalive(sea, now); err != nil {
			t.Fatal(err)
		}
		if n := len(m.Writes()); n != s.want {
			t.Errorf("at %d ms: %d writes, want %d", s.at, n, s.want)
		}
	}
	blks := hostBlocks(t, m)
	if b := blks[1]; b.Stage != "sea" || b.Room != 5 || b.HitApply != (mailbox.EnemyHitApply{}) {
		t.Errorf("an idle keepalive: %+v", b)
	}
	for _, b := range blks[2:] {
		if b.Stage != "M_Dai" || b.Room != 1 || b.HitApply.Seq != 7<<16+1 || b.HitApply.PID != 420 {
			t.Errorf("a keepalive with a request queued: %+v", b)
		}
	}
	for i := 1; i < len(blks); i++ {
		if blks[i].Seq != blks[i-1].Seq+1 {
			t.Errorf("block seq %d after %d", blks[i].Seq, blks[i-1].Seq)
		}
	}
}

// TestHitApplierFlags: the knobs and a HIT's own AtSPrm set the
// request's HAF flags.
func TestHitApplierFlags(t *testing.T) {
	for _, c := range []struct {
		name     string
		fallback bool
		noMark   bool
		atSPrm   uint8
		want     uint8
	}{
		{"none", false, false, 0, 0},
		{"fallback shrink", true, false, 0, mailbox.EnemyHAFFallbackShrink},
		{"no hit mark", false, true, 0, mailbox.EnemyHAFNoHitMark},
		{"the hit's NoHitMark", false, false, addr.CcAtSPrmNoHitMark | 0x01, mailbox.EnemyHAFNoHitMark},
		{"another AtSPrm bit", false, false, 0x01, 0},
		{"both knobs", true, true, 0, mailbox.EnemyHAFFallbackShrink | mailbox.EnemyHAFNoHitMark},
	} {
		m, a, st, mos := hitRig(t)
		a.FallbackShrink, a.NoHitMark = c.fallback, c.noMark
		h := hostHit(1, 0, 1)
		h.AtSPrm = c.atSPrm
		if ack := a.Offer(h, st, mos, time.Unix(1000, 0)); ack != nil {
			t.Fatalf("%s: %+v", c.name, ack)
		}
		if blks := hostBlocks(t, m); len(blks) != 1 || blks[0].HitApply.Flags != c.want {
			t.Errorf("%s: %+v, want flags %02X", c.name, blks, c.want)
		}
	}
}

// TestHitApplierDemo: with the gate on, a hit while the host Link's
// mDemo.mDemoType is nonzero is dropped HDDemo; without a demo, with the
// gate off, or with no Link (the title screen), it is queued.
func TestHitApplierDemo(t *testing.T) {
	const link = uint32(0x80A00000)
	for _, c := range []struct {
		name string
		gate bool
		link uint32
		demo uint16
		drop bool
	}{
		{"demo, gate on", true, link, 1, true},
		{"no demo, gate on", true, link, 0, false},
		{"demo, gate off", false, link, 1, false},
		{"no Link, gate on", true, 0, 1, false},
	} {
		m, a, st, mos := hitRig(t)
		a.DemoGate = c.gate
		store(t, m, addr.PlayerPtr, binary.BigEndian.AppendUint32(nil, c.link))
		store(t, m, link+addr.PlayerOffDemoType, binary.BigEndian.AppendUint16(nil, c.demo))
		ack := a.Offer(hostHit(1, 0, 1), st, mos, time.Unix(1000, 0))
		if c.drop != (ack != nil) || (c.drop && (ack.Result != AckDropped || ack.Detail != HDDemo)) {
			t.Errorf("%s: %+v", c.name, ack)
		}
		if n := len(m.Writes()); c.drop != (n == 0) {
			t.Errorf("%s: %d writes", c.name, n)
		}
	}
}

// TestDeadMaskAndScope: DeadMask follows the room's zone bits and
// ReadScope the stage, room and play state.
func TestDeadMaskAndScope(t *testing.T) {
	g, m, _ := moblinRoom(t)
	sc, err := ReadScope(m)
	if err != nil || sc != (Scope{Stage: "M_Dai", Room: 1, InPlay: true}) {
		t.Fatalf("scope: %+v %v", sc, err)
	}
	for _, id := range []int{0, 2, 7} {
		if err := g.OnActor(id, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.OnActor(3, 2); err != nil { // room 2's zone, not room 1's
		t.Fatal(err)
	}
	mask, err := DeadMask(m, 1)
	if err != nil || mask != 1<<0|1<<2|1<<7 {
		t.Errorf("dead_mask %08b %v", mask, err)
	}
	if mask, _ := DeadMask(m, 2); mask != 1<<3 {
		t.Errorf("room 2 dead_mask %08b", mask)
	}
	if mask, err := DeadMask(m, 40); err != nil || mask != 0 {
		t.Errorf("a room without a zone: %08b %v", mask, err)
	}
	if err := g.SetNextStage(fakegame.Stage{Name: "sea", Table: addr.TableSea}); err != nil {
		t.Fatal(err)
	}
	if sc, _ := ReadScope(m); sc.InPlay || !sc.Pending {
		t.Errorf("with a stage change pending: %+v", sc)
	}
}
