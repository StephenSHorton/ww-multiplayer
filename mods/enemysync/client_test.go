package enemysync

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// clientRoom is moblinRoom booted as this build's patch (the contract
// gate) with the REL module linked; relTick advances the REL's frame
// counter and the heartbeat, as the game does, before each mirror tick.
func clientRoom(t *testing.T) (*fakegame.FakeGame, *mem.FakeMem, *fakegame.Enemies, func()) {
	t.Helper()
	g, m, es := moblinRoom(t)
	if err := fakegame.Boot(m); err != nil {
		t.Fatal(err)
	}
	rel := mailbox.REL{Linked: true, BuildID: inject.RELBuildID, Frames: 100}
	tick := func() {
		rel.Frames++
		if err := mailbox.WriteREL(gameWriter{m}, rel); err != nil {
			t.Fatal(err)
		}
		g.Frame()
	}
	tick()
	return g, m, es, tick
}

// gameWriter writes as the game does (FakeMem.Store: never recorded as
// a tool write).
type gameWriter struct{ m *mem.FakeMem }

func (w gameWriter) Write(a uint32, b []byte) error { return w.m.Store(a, b) }

// hostState is a STATE for the three keyed Moblins of M_Dai room 1.
func hostState(seq uint32, hostMS uint32) *State {
	st := &State{Flags: StateFlagInScope | StateFlagAnim, Epoch: 7, Seq: seq, HostBeat: 1000 + seq, HostMS: hostMS, Stage: "M_Dai", Room: 1}
	for id := uint16(0); id < 3; id++ {
		st.Slots = append(st.Slots, Slot{SetID: id, Proc: ProcMO2, Pos: [3]float32{float32(id) * 100, 10, float32(hostMS)}, Health: 4, MorfRate: 1})
	}
	return st
}

func pushState(t *testing.T, c *Mirror, st *State, now time.Time) {
	t.Helper()
	b, err := EncodeState(st)
	if err != nil {
		t.Fatal(err)
	}
	c.OnEnemy(b, now)
}

// blockWrites returns the recorded writes that are whole blocks, decoded.
func blockWrites(t *testing.T, m *mem.FakeMem) []mailbox.EnemyBlock {
	t.Helper()
	var out []mailbox.EnemyBlock
	for _, w := range m.Writes() {
		if w.Addr != mailbox.EnemyBlockAddr || len(w.Data) != mailbox.EnemyGoSpan {
			continue
		}
		blk, tail, err := mailbox.DecodeEnemyBlock(w.Data)
		if err != nil || tail != blk.Seq {
			t.Fatalf("block write: %v tail %d head %d", err, tail, blk.Seq)
		}
		out = append(out, blk)
	}
	return out
}

// TestMirrorWritesBlock: with every gate open, one tick writes one block
// of EnemyGoSpan bytes with seq_head == seq_tail, the host's setIDs
// matched to this game's Moblins by (room, setID) and an unmatched one
// given pid 0; the next tick's seq is one up; Shutdown writes magic 0.
func TestMirrorWritesBlock(t *testing.T) {
	_, m, es, tick := clientRoom(t)
	start := time.Unix(2000, 0)
	c := NewMirror(m, log.Discard{}, nil, false, start)
	m.Record(true)
	now := start.Add(time.Second)
	st := hostState(1, 1000)
	st.Slots = append(st.Slots, Slot{SetID: 3, Proc: ProcMO2, Health: 4}) // jailed here: no match
	pushState(t, c, st, now)
	tick()
	hits, sleep, err := c.Tick(now)
	if err != nil || sleep != 0 || len(hits) != 0 {
		t.Fatalf("tick: hits %v sleep %v err %v", hits, sleep, err)
	}
	blocks := blockWrites(t, m)
	if len(blocks) != 1 || len(m.Writes()) != 1 {
		t.Fatalf("writes: %d blocks of %d writes", len(blocks), len(m.Writes()))
	}
	b := blocks[0]
	a0, _ := es.Of(1)
	a1, _ := es.Of(2)
	a2, _ := es.Of(0)
	if b.Magic != mailbox.EnemyMagic || b.Seq != 1 || b.Role != mailbox.EnemyRoleClient || b.Flags&mailbox.EnemyFlagMirror == 0 || b.Flags&mailbox.EnemyFlagRaw != 0 ||
		b.Layout != mailbox.EnemyLayout || b.Epoch != c.Epoch() || b.Stage != "M_Dai" || b.Room != 1 || b.HostBeat != 1001 || len(b.Slots) != 4 {
		t.Fatalf("block: %+v", b)
	}
	for i, want := range []uint32{a0.PID, a1.PID, a2.PID, 0} {
		if b.Slots[i].PID != want {
			t.Errorf("slot %d pid %d, want %d", i, b.Slots[i].PID, want)
		}
	}
	if s := b.Slots[1]; s.Pos != [3]float32{100, 10, 1000} || s.Flags&mailbox.EnemySFSnap == 0 || s.SampleSeq != 1 || s.Health != 4 {
		t.Errorf("slot 1: %+v", s)
	}
	// The next tick: seq 2, no snap.
	pushState(t, c, hostState(2, 1016), now.Add(16*time.Millisecond))
	tick()
	if _, _, err := c.Tick(now.Add(16 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	blocks = blockWrites(t, m)
	if len(blocks) != 2 || blocks[1].Seq != 2 || blocks[1].Slots[0].Flags&mailbox.EnemySFSnap != 0 || blocks[1].Slots[0].PID != a0.PID {
		t.Errorf("second block: %+v", blocks[1])
	}
	m.ResetWrites()
	if err := c.Shutdown(); err != nil {
		t.Fatal(err)
	}
	w := m.Writes()
	if len(w) != 1 || w[0].Addr != mailbox.EnemyBlockAddr || len(w[0].Data) != 4 || binary.BigEndian.Uint32(w[0].Data) != 0 {
		t.Errorf("shutdown: %+v", w)
	}
}

// TestMirrorGates: each gate failure writes a count-0 block (once, when
// the mirror was armed) and asks for GateSleep; a contract failure
// writes nothing.
func TestMirrorGates(t *testing.T) {
	g, m, _, tick := clientRoom(t)
	start := time.Unix(2000, 0)
	c := NewMirror(m, log.Discard{}, nil, false, start)
	m.Record(true)
	now := start
	arm := func() {
		t.Helper()
		pushState(t, c, hostState(1, 0), now)
		tick()
		if _, sleep, err := c.Tick(now); err != nil || sleep != 0 {
			t.Fatalf("arm: sleep %v err %v", sleep, err)
		}
	}
	countOf := func() []int {
		var out []int
		for _, b := range blockWrites(t, m) {
			out = append(out, len(b.Slots))
		}
		return out
	}
	// No STATE yet: nothing written, GateSleep.
	if _, sleep, err := c.Tick(now); err != nil || sleep != GateSleep || len(m.Writes()) != 0 {
		t.Fatalf("no STATE: sleep %v err %v writes %d", sleep, err, len(m.Writes()))
	}
	arm()
	// Stale STATE (over StaleAfter without one, 32 ticks): a count-0
	// block, once.
	for i := 1; i <= 32; i++ {
		tick()
		if _, _, err := c.Tick(now.Add(time.Duration(i) * 16 * time.Millisecond)); err != nil {
			t.Fatal(err)
		}
	}
	if counts := countOf(); len(counts) != 33 || counts[31] != 3 || counts[32] != 0 {
		t.Fatalf("stale: block counts %v", counts)
	}
	now = now.Add(time.Second)
	tick()
	if _, sleep, _ := c.Tick(now); sleep != GateSleep || len(countOf()) != 33 {
		t.Errorf("a second stale tick wrote again (%d blocks) or slept %v", len(countOf()), sleep)
	}
	// Another room than the host's.
	m.ResetWrites()
	arm()
	if err := g.EnterRoom(2, 1); err != nil {
		t.Fatal(err)
	}
	tick()
	if _, sleep, _ := c.Tick(now); sleep != GateSleep {
		t.Error("room mismatch not gated")
	}
	if counts := countOf(); len(counts) != 2 || counts[1] != 0 {
		t.Errorf("room mismatch: counts %v", counts)
	}
	if err := g.EnterRoom(1, 2); err != nil {
		t.Fatal(err)
	}
	// The host not in play (a keepalive STATE).
	m.ResetWrites()
	arm()
	ka := hostState(2, 16)
	ka.Flags, ka.Slots = 0, nil
	pushState(t, c, ka, now)
	tick()
	if _, sleep, _ := c.Tick(now); sleep != GateSleep {
		t.Error("host keepalive not gated")
	}
	if counts := countOf(); len(counts) != 2 || counts[1] != 0 {
		t.Errorf("host keepalive: counts %v", counts)
	}
	// This game not in play.
	m.ResetWrites()
	arm()
	if err := g.LoadStage(fakegame.Stage{Name: addr.StageTitle, Table: addr.TableSea, NoPlayer: true}); err != nil {
		t.Fatal(err)
	}
	tick()
	if _, sleep, _ := c.Tick(now); sleep != GateSleep {
		t.Error("title screen not gated")
	}
	if counts := countOf(); len(counts) != 2 || counts[1] != 0 {
		t.Errorf("title: counts %v", counts)
	}
	if err := g.LoadStage(mDai); err != nil {
		t.Fatal(err)
	}
	if err := g.EnterRoom(1, 2); err != nil {
		t.Fatal(err)
	}
	// The REL unlinked: gated, count 0 once; relinked: a new epoch.
	m.ResetWrites()
	arm()
	epoch := c.Epoch()
	if err := mailbox.WriteREL(m, mailbox.REL{}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(RELEvery)
	if _, sleep, _ := c.Tick(now); sleep != GateSleep {
		t.Error("REL unlinked not gated")
	}
	if counts := countOf(); len(counts) != 2 || counts[1] != 0 {
		t.Errorf("REL unlinked: counts %v", counts)
	}
	now = now.Add(RELEvery)
	tick()
	tick()
	pushState(t, c, hostState(3, 32), now)
	if _, sleep, err := c.Tick(now); err != nil || sleep != 0 {
		t.Errorf("relinked: sleep %v err %v", sleep, err)
	}
	if c.Epoch() != epoch+1 {
		t.Errorf("epoch %d after a relink, want %d", c.Epoch(), epoch+1)
	}
	// The contract failing: no write at all, even when armed.
	m.ResetWrites()
	store(t, m, mailbox.Mailbox+mailbox.OffHdrMagic, []byte{0, 0, 0, 0})
	now = now.Add(ContractEvery)
	tick()
	if _, sleep, _ := c.Tick(now); sleep != GateSleep || len(m.Writes()) != 0 {
		t.Errorf("contract: sleep %v writes %d", sleep, len(m.Writes()))
	}
}

// TestMirrorEpochAndDying: a heartbeat regression starts a new epoch,
// the block carries it and the slots snap; a dead_mask bit for a Moblin
// this game still has makes its slot dying.
func TestMirrorEpochAndDying(t *testing.T) {
	_, m, es, tick := clientRoom(t)
	start := time.Unix(2000, 0)
	c := NewMirror(m, log.Discard{}, nil, false, start)
	m.Record(true)
	now := start
	pushState(t, c, hostState(1, 0), now)
	tick()
	if _, _, err := c.Tick(now); err != nil {
		t.Fatal(err)
	}
	epoch := c.Epoch()
	// The heartbeat goes backwards (a save state).
	hb, _ := mem.ReadU32(m, mailbox.SpawnTrigger)
	store(t, m, mailbox.SpawnTrigger, binary.BigEndian.AppendUint32(nil, hb-50))
	now = now.Add(16 * time.Millisecond)
	pushState(t, c, hostState(2, 16), now)
	if _, _, err := c.Tick(now); err != nil {
		t.Fatal(err)
	}
	blocks := blockWrites(t, m)
	if c.Epoch() != epoch+1 || len(blocks) != 2 || blocks[1].Epoch != epoch+1 || blocks[1].Slots[0].Flags&mailbox.EnemySFSnap == 0 {
		t.Errorf("regression: epoch %d, block %+v", c.Epoch(), blocks[1])
	}
	// A jump too far forward too.
	store(t, m, mailbox.SpawnTrigger, binary.BigEndian.AppendUint32(nil, hb+5000))
	now = now.Add(16 * time.Millisecond)
	pushState(t, c, hostState(3, 32), now)
	c.Tick(now)
	if c.Epoch() != epoch+2 {
		t.Errorf("jump: epoch %d, want %d", c.Epoch(), epoch+2)
	}
	// dead_mask bit 1 while this game's setID 1 lives: dying; and a setID
	// the host no longer lists with its bit set: a dying release slot.
	st := hostState(4, 48)
	st.DeadMask = 1<<1 | 1<<2
	st.Slots = st.Slots[:2] // setIDs 0, 1
	now = now.Add(16 * time.Millisecond)
	pushState(t, c, st, now)
	tick()
	c.Tick(now)
	blocks = blockWrites(t, m)
	b := blocks[len(blocks)-1]
	a1, _ := es.Of(2)
	a2, _ := es.Of(0)
	if len(b.Slots) != 3 || b.Slots[0].Flags&mailbox.EnemySFDying != 0 || b.Slots[1].Flags&mailbox.EnemySFDying == 0 || b.Slots[1].PID != a1.PID ||
		b.Slots[2].PID != a2.PID || b.Slots[2].Flags&(mailbox.EnemySFDying|mailbox.EnemySFRelease) != mailbox.EnemySFDying|mailbox.EnemySFRelease {
		t.Errorf("dead_mask: %+v", b.Slots)
	}
}

// TestMirrorHits: a pending hit entry the REL wrote becomes one HIT with
// the hit object's At type and the collider's centre, and its pending
// byte is cleared; the same hit_seq again is not sent twice.
func TestMirrorHits(t *testing.T) {
	_, m, es, tick := clientRoom(t)
	start := time.Unix(2000, 0)
	c := NewMirror(m, log.Discard{}, nil, false, start)
	now := start
	pushState(t, c, hostState(1, 0), now)
	tick()
	if _, _, err := c.Tick(now); err != nil {
		t.Fatal(err)
	}
	a0, _ := es.Of(1)
	// The REL's status for slot 0 and a pending head hit; the hit object.
	stOff := mailbox.EnemyBlockAddr + mailbox.EnemyStatusOff
	store(t, m, stOff+mailbox.EnemySTActorOff, binary.BigEndian.AppendUint32(nil, a0.Addr))
	store(t, m, stOff+mailbox.EnemySTPIDOff, binary.BigEndian.AppendUint32(nil, a0.PID))
	store(t, m, stOff+mailbox.EnemySTStateOff, []byte{mailbox.EnemyStateMirrored})
	hitOff := mailbox.EnemyBlockAddr + mailbox.EnemyHitsOff
	store(t, m, hitOff, []byte{0, 9, ColliderHead, 1})
	const obj = uint32(0x80D00000)
	store(t, m, a0.Addr+addr.Mo2OffHeadSph+addr.Mo2CcTgHitObjOff, binary.BigEndian.AppendUint32(nil, obj))
	store(t, m, obj+0x10, []byte{0, 0x08, 0, 0, 2, 0, 0, 0})
	store(t, m, a0.Addr+addr.Mo2OffHeadSph+addr.Mo2CcCenterOff, f32s(1, 2, 3))
	m.Record(true)
	now = now.Add(16 * time.Millisecond)
	pushState(t, c, hostState(2, 16), now)
	tick()
	hits, _, err := c.Tick(now)
	if err != nil || len(hits) != 1 {
		t.Fatalf("hits %v err %v", hits, err)
	}
	h := hits[0]
	if h.Collider != ColliderHead || h.SetID != 0 || h.HitSeq != 9 || h.AtType != 0x00080000 || h.Atp != 2 || h.Pos != [3]float32{1, 2, 3} || h.Epoch != c.Epoch() || h.Stage != "M_Dai" || h.Room != 1 {
		t.Errorf("hit: %+v", h)
	}
	cleared := false
	for _, w := range m.Writes() {
		if w.Addr == hitOff+3 && len(w.Data) == 1 && w.Data[0] == 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("the pending byte was not cleared")
	}
	// Still pending with the same seq (the clear not yet seen): no repeat.
	now = now.Add(16 * time.Millisecond)
	pushState(t, c, hostState(3, 32), now)
	tick()
	if hits, _, _ := c.Tick(now); len(hits) != 0 {
		t.Errorf("hit repeated: %v", hits)
	}
	if c.Stats.Hits != 1 {
		t.Errorf("Stats.Hits %d", c.Stats.Hits)
	}
}

// TestMirrorRaw: raw mode sets the block's raw flag and passes the newest
// sample through.
func TestMirrorRaw(t *testing.T) {
	_, m, _, tick := clientRoom(t)
	start := time.Unix(2000, 0)
	c := NewMirror(m, log.Discard{}, nil, true, start)
	m.Record(true)
	pushState(t, c, hostState(1, 0), start)
	tick()
	c.Tick(start)
	blocks := blockWrites(t, m)
	if len(blocks) != 1 || blocks[0].Flags&mailbox.EnemyFlagRaw == 0 || blocks[0].Slots[2].Pos != [3]float32{200, 10, 0} {
		t.Errorf("raw: %+v", blocks)
	}
}

// TestMirrorStickyErr: the REL's last_err is sticky, so an unresolved
// slot's error forces one walk when it appears, not a walk every tick.
func TestMirrorStickyErr(t *testing.T) {
	_, m, _, tick := clientRoom(t)
	start := time.Unix(2000, 0)
	c := NewMirror(m, log.Discard{}, nil, false, start)
	now := start
	pushState(t, c, hostState(1, 0), now)
	tick()
	if _, _, err := c.Tick(now); err != nil {
		t.Fatal(err)
	}
	first := c.matchNext
	stOff := mailbox.EnemyBlockAddr + mailbox.EnemyStatusOff
	store(t, m, stOff+mailbox.EnemySTStateOff, []byte{mailbox.EnemyStateMissing, 0, mailbox.EnemyErrPID})
	now = now.Add(16 * time.Millisecond)
	pushState(t, c, hostState(2, 16), now)
	tick()
	c.Tick(now)
	if c.matchNext == first {
		t.Fatal("a new last_err did not force a walk")
	}
	forced := c.matchNext
	for i := 3; i < 6; i++ {
		now = now.Add(16 * time.Millisecond)
		pushState(t, c, hostState(uint32(i), uint32(i)*16), now)
		tick()
		c.Tick(now)
	}
	if c.matchNext != forced {
		t.Error("the sticky last_err forced a walk again")
	}
}
