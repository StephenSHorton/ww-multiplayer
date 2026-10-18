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

// The placed Moblins, setIDs out of order: three keyed mode-0 ones in
// room 1, a jailed one (mode 1), a runtime one (setID 0xFFFF) and one in
// room 2. Placed indices: 0 setID 2, 1 setID 0, 2 setID 1, 3 jailed, 4
// runtime, 5 room 2.
func placeMoblins() *fakegame.Enemies {
	return fakegame.PlaceEnemies(
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 2, Proc: stage.ProcMO2},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 0, Proc: stage.ProcMO2},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 1, Proc: stage.ProcMO2},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 3, Proc: stage.ProcMO2, Mode: 1},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: addr.ActorSetIDNone, Proc: stage.ProcMO2},
		fakegame.Enemy{Stage: mDai.Name, Room: 2, SetID: 4, Proc: stage.ProcMO2},
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
// sorted by setID, with their fields; the first sample snaps; a PID
// change under the sampler drops the entry; the title screen gives a
// count-0 keepalive every KeepaliveEvery; the ice timer, the death action
// and health 0 mark dying; the zone bit sets dead_mask.
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
	s0 := st.Slots[0]
	want := Slot{SetID: 0, Proc: ProcMO2, Pos: [3]float32{-1500.5, 250, 3000}, AngleY: 0x4000, Tilt: -2, Action: 10, Mode: 2, AttackType: 2, AttackPhase: 1,
		MorfFrame: 12.25, MorfRate: 1.5, Health: 4, Flags: SlotSnap | SlotInvuln | SlotDefence | SlotBareHanded}
	if s0 != want {
		t.Errorf("slot 0\n got %+v\nwant %+v", s0, want)
	}
	for _, s := range st.Slots[1:] {
		if s.Flags != SlotSnap || s.Health != 4 || s.Proc != ProcMO2 {
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

// TestHitApplier: a valid HIT is written as hit_apply (role host) and
// acknowledged with the REL's result; an invulnerable, replayed, stale or
// unknown one is dropped at once; a request the REL never acks times
// out as no-actor.
func TestHitApplier(t *testing.T) {
	m := mem.NewFakeMem()
	m.Record(true)
	a := NewHitApplier(m, log.Discard{}, 7)
	a.SetREL(true)
	now := time.Unix(1000, 0)
	st := &State{Flags: StateFlagInScope, Epoch: 7, Stage: "M_Dai", Room: 1, Slots: []Slot{
		{SetID: 0, Health: 4}, {SetID: 1, Health: 2, Flags: SlotInvuln},
	}}
	hit := &Hit{Client: 3, Collider: ColliderHead, Epoch: 7, Stage: "M_Dai", Room: 1, SetID: 0, HitSeq: 1, AtType: 0x80000, Atp: 2, Pos: [3]float32{1, 2, 3}}
	if ack := a.Offer(hit, st, now); ack != nil {
		t.Fatalf("a valid hit was acked at once: %+v", ack)
	}
	w := m.Writes()
	if len(w) != 1 || w[0].Addr != mailbox.EnemyBlockAddr || len(w[0].Data) != mailbox.EnemyGoSpan {
		t.Fatalf("writes: %+v", w)
	}
	blk, tail, _ := mailbox.DecodeEnemyBlock(w[0].Data)
	if blk.Magic != mailbox.EnemyMagic || blk.Role != mailbox.EnemyRoleHost || blk.Epoch != 7 || len(blk.Slots) != 0 || tail != blk.Seq ||
		blk.HitApply != (mailbox.EnemyHitApply{Seq: 1, SetID: 0, Collider: ColliderHead, AtType: 0x80000, Atp: 2, Pos: [3]float32{1, 2, 3}}) {
		t.Errorf("host block: %+v tail %d", blk, tail)
	}
	// Drops while one is pending, and for the invulnerable one.
	if ack := a.Offer(&Hit{Client: 3, Epoch: 7, Stage: "M_Dai", Room: 1, SetID: 1, HitSeq: 2}, st, now); ack == nil || ack.Result != AckDropped {
		t.Errorf("invulnerable: %+v", ack)
	}
	// No ack yet.
	if ack, err := a.Poll(st); err != nil || ack != nil {
		t.Fatalf("poll: %+v %v", ack, err)
	}
	// The REL acks with the shrink result.
	store(t, m, mailbox.EnemyBlockAddr+mailbox.EnemyHitApplyAckOff, binary.BigEndian.AppendUint32(nil, 1))
	store(t, m, mailbox.EnemyBlockAddr+mailbox.EnemyReserved2Off, []byte{0, 0, 0, AckShrink})
	ack, err := a.Poll(st)
	if err != nil || ack == nil || *ack != (Ack{Client: 3, Result: AckShrink, Epoch: 7, SetID: 0, HitSeq: 1, Health: 4}) {
		t.Fatalf("acked: %+v %v", ack, err)
	}
	// A replay, a stale epoch, another room, an unknown setID.
	for _, c := range []struct {
		name string
		hit  Hit
		want uint8
	}{
		{"replay", Hit{Client: 3, Epoch: 7, Stage: "M_Dai", Room: 1, SetID: 0, HitSeq: 1}, AckDropped},
		{"stale epoch", Hit{Client: 3, Epoch: 6, Stage: "M_Dai", Room: 1, SetID: 0, HitSeq: 5}, AckDropped},
		{"other room", Hit{Client: 3, Epoch: 7, Stage: "M_Dai", Room: 2, SetID: 0, HitSeq: 6}, AckDropped},
		{"unknown setID", Hit{Client: 3, Epoch: 7, Stage: "M_Dai", Room: 1, SetID: 9, HitSeq: 7}, AckNoActor},
	} {
		if ack := a.Offer(&c.hit, st, now.Add(time.Second)); ack == nil || ack.Result != c.want {
			t.Errorf("%s: %+v", c.name, ack)
		}
	}
	// Rate: a second hit on setID 0 within HitRate is dropped; after it,
	// taken; the REL never acks: no-actor after hitAckTicks.
	if ack := a.Offer(&Hit{Client: 4, Epoch: 7, Stage: "M_Dai", Room: 1, SetID: 0, HitSeq: 1}, st, now.Add(HitRate/2)); ack == nil || ack.Result != AckDropped {
		t.Errorf("within HitRate: %+v", ack)
	}
	if ack := a.Offer(&Hit{Client: 4, Epoch: 7, Stage: "M_Dai", Room: 1, SetID: 0, HitSeq: 2}, st, now.Add(2*HitRate)); ack != nil {
		t.Errorf("after HitRate: %+v", ack)
	}
	var got *Ack
	for i := 0; i <= hitAckTicks && got == nil; i++ {
		got, err = a.Poll(st)
		if err != nil {
			t.Fatal(err)
		}
	}
	if got == nil || got.Result != AckNoActor || got.Client != 4 || got.HitSeq != 2 {
		t.Errorf("timed out: %+v", got)
	}
	// Without the REL, every hit is dropped.
	a.SetREL(false)
	if ack := a.Offer(&Hit{Client: 5, Epoch: 7, Stage: "M_Dai", Room: 1, SetID: 0, HitSeq: 1}, st, now.Add(time.Minute)); ack == nil || ack.Result != AckDropped {
		t.Errorf("no REL: %+v", ack)
	}
	m.ResetWrites()
	if err := a.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if w := m.Writes(); len(w) != 1 || w[0].Addr != mailbox.EnemyBlockAddr || len(w[0].Data) != 4 || binary.BigEndian.Uint32(w[0].Data) != 0 {
		t.Errorf("shutdown writes: %+v", w)
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
