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
// the hit object's At type and the collider's centre, stamped with the
// host STATE's epoch (not the mirror's own), which the host's applier
// takes (no HD_GO_GATE); its pending byte is cleared; the same hit_seq
// pending again is not sent twice but its pending byte is still cleared
// (the REL withholds the collider until it is).
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
	if h.Collider != ColliderHead || h.SetID != 0 || h.HitSeq != 9 || h.AtType != 0x00080000 || h.Atp != 2 || h.Pos != [3]float32{1, 2, 3} || h.Epoch != 7 || h.Stage != "M_Dai" || h.Room != 1 {
		t.Errorf("hit: %+v (want the host STATE's epoch 7)", h)
	}
	if c.Epoch() == 7 {
		t.Fatal("the mirror's epoch equals the host's: the test proves nothing")
	}
	// Through the wire and the host's applier (epoch 7, the STATE's).
	b, err := EncodeHit(h)
	if err != nil {
		t.Fatal(err)
	}
	wh, err := DecodeHit(b)
	if err != nil {
		t.Fatal(err)
	}
	_, ap, hst, mos := hitRig(t)
	if ack := ap.Offer(wh, hst, mos, now); ack != nil && ack.Detail == HDGoGate {
		t.Errorf("the host's applier dropped the client's hit HD_GO_GATE: %+v", ack)
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
	// Pending again with the same seq (a relinked REL renumbering from the
	// seq drained last): not sent again, but cleared.
	store(t, m, hitOff, []byte{0, 9, ColliderHead, 1})
	m.ResetWrites()
	now = now.Add(16 * time.Millisecond)
	pushState(t, c, hostState(3, 32), now)
	tick()
	if hits, _, _ := c.Tick(now); len(hits) != 0 {
		t.Errorf("hit repeated: %v", hits)
	}
	if c.Stats.Hits != 1 {
		t.Errorf("Stats.Hits %d", c.Stats.Hits)
	}
	if p, _ := m.Read(hitOff+3, 1); p[0] != 0 {
		t.Error("a repeated hit_seq left the pending byte set")
	}
	// A new epoch forgets the drained seqs: seq 9 is new again.
	c.newEpoch("test")
	if c.lastHits != [MaxSlots]uint16{} {
		t.Errorf("lastHits %v after a new epoch", c.lastHits)
	}
}

// TestMirrorHitCatchUp: a pending hit on a catch-up slot (a setID the
// host no longer lists, appended after the host's slots) neither panics
// nor is sent, and its pending byte is cleared.
func TestMirrorHitCatchUp(t *testing.T) {
	_, m, es, tick := clientRoom(t)
	start := time.Unix(2000, 0)
	c := NewMirror(m, log.Discard{}, nil, false, start)
	now := start
	pushState(t, c, hostState(1, 0), now)
	tick()
	if _, _, err := c.Tick(now); err != nil {
		t.Fatal(err)
	}
	// The host drops setID 2 (dead there): it becomes slot 1, a catch-up
	// after the one listed slot, with the pid the REL resolved for it.
	a2, _ := es.Of(0)
	stOff := mailbox.EnemyBlockAddr + mailbox.EnemyStatusOff + mailbox.EnemyStatusSize
	store(t, m, stOff+mailbox.EnemySTActorOff, binary.BigEndian.AppendUint32(nil, a2.Addr))
	store(t, m, stOff+mailbox.EnemySTPIDOff, binary.BigEndian.AppendUint32(nil, a2.PID))
	store(t, m, stOff+mailbox.EnemySTStateOff, []byte{mailbox.EnemyStateMirrored})
	hitOff := mailbox.EnemyBlockAddr + mailbox.EnemyHitsOff + mailbox.EnemyHitSize
	store(t, m, hitOff, []byte{0, 1, ColliderHead, 1})
	st := hostState(2, 16)
	st.Slots = st.Slots[:1]
	st.DeadMask = 1 << 2
	now = now.Add(16 * time.Millisecond)
	pushState(t, c, st, now)
	tick()
	m.Record(true)
	hits, _, err := c.Tick(now)
	if err != nil || len(hits) != 0 {
		t.Fatalf("catch-up hit: hits %v err %v", hits, err)
	}
	blocks := blockWrites(t, m)
	if len(blocks) != 1 || len(blocks[0].Slots) != 2 || blocks[0].Slots[1].PID != a2.PID {
		t.Fatalf("blocks: %+v", blocks)
	}
	if p, _ := m.Read(hitOff+3, 1); p[0] != 0 {
		t.Error("the catch-up slot's pending byte was not cleared")
	}
}

// TestMirrorSampleSeq: each slot's sample_seq is the block's seq, so it
// changes on every write even without a new STATE (the REL re-anchors
// the re-timed frame every consume); raw mode keeps the STATE's seq.
func TestMirrorSampleSeq(t *testing.T) {
	for _, raw := range []bool{false, true} {
		_, m, _, tick := clientRoom(t)
		start := time.Unix(2000, 0)
		c := NewMirror(m, log.Discard{}, nil, raw, start)
		m.Record(true)
		now := start
		pushState(t, c, hostState(5, 0), now)
		for range 3 { // one STATE, three writes
			tick()
			if _, _, err := c.Tick(now); err != nil {
				t.Fatal(err)
			}
			now = now.Add(16 * time.Millisecond)
		}
		blocks := blockWrites(t, m)
		if len(blocks) != 3 {
			t.Fatalf("raw %v: %d blocks", raw, len(blocks))
		}
		for _, b := range blocks {
			want := b.Seq
			if raw {
				want = 5
			}
			for i, sl := range b.Slots {
				if sl.SampleSeq != want {
					t.Errorf("raw %v block %d slot %d: sample_seq %d, want %d", raw, b.Seq, i, sl.SampleSeq, want)
				}
			}
		}
	}
}

// TestMirrorHostRestart: a STATE with a new host epoch and host_ms back
// near 0 (the host Go restarted) re-bases the smoothers: the block
// follows the new host's pose at once, snapped, instead of holding the
// old host's last one.
func TestMirrorHostRestart(t *testing.T) {
	_, m, _, tick := clientRoom(t)
	start := time.Unix(2000, 0)
	c := NewMirror(m, log.Discard{}, nil, false, start)
	m.Record(true)
	now := start
	for i := range 10 {
		pushState(t, c, hostState(uint32(i+1), 500000+uint32(i)*16), now)
		tick()
		if _, _, err := c.Tick(now); err != nil {
			t.Fatal(err)
		}
		now = now.Add(16 * time.Millisecond)
	}
	st := hostState(1, 16)
	st.Epoch = 9
	pushState(t, c, st, now)
	tick()
	if _, _, err := c.Tick(now); err != nil {
		t.Fatal(err)
	}
	blocks := blockWrites(t, m)
	sl := blocks[len(blocks)-1].Slots[0]
	if sl.Pos != st.Slots[0].Pos || sl.Flags&mailbox.EnemySFSnap == 0 {
		t.Errorf("after the host restart: pos %v flags %08b, want the new sample %v snapped", sl.Pos, sl.Flags, st.Slots[0].Pos)
	}
	// The next sample of the new host is taken too.
	now = now.Add(16 * time.Millisecond)
	st2 := hostState(2, 32)
	st2.Epoch = 9
	pushState(t, c, st2, now)
	tick()
	c.Tick(now)
	if s := c.sm[0]; s.Len() != 2 {
		t.Errorf("the new host's samples held: %d, want 2", s.Len())
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

// TestMirrorAnimFields: the block's slots carry the seven animation
// fields from the smoother's output, and a catch-up (dead_mask) slot
// carries Bck and AnmAttr 0xFF.
func TestMirrorAnimFields(t *testing.T) {
	_, m, es, tick := clientRoom(t)
	start := time.Unix(2000, 0)
	c := NewMirror(m, log.Discard{}, nil, false, start)
	m.Record(true)
	now := start.Add(time.Second)
	st := hostState(1, 1000)
	st.Slots = st.Slots[:2] // setIDs 0, 1
	st.DeadMask = 1 << 2    // setID 2: a catch-up slot
	for i := range st.Slots {
		sl := &st.Slots[i]
		sl.Bck, sl.AnmAttr, sl.AnmSeq, sl.Morf = 0x45+uint8(i), AnmAttrLoop, 3+uint8(i), 10
		sl.Flags2, sl.BlurShape, sl.BlurScale = 0x05, 1+uint8(i), 1.5+float32(i)
	}
	pushState(t, c, st, now)
	tick()
	if _, _, err := c.Tick(now); err != nil {
		t.Fatal(err)
	}
	blocks := blockWrites(t, m)
	if len(blocks) != 1 || len(blocks[0].Slots) != 3 {
		t.Fatalf("blocks: %+v", blocks)
	}
	for i := range 2 {
		got, want := blocks[0].Slots[i], st.Slots[i]
		if got.Bck != want.Bck || got.AnmAttr != want.AnmAttr || got.AnmSeq != want.AnmSeq || got.Morf != want.Morf ||
			got.Flags2 != want.Flags2 || got.BlurShape != want.BlurShape || got.BlurScale != want.BlurScale {
			t.Errorf("slot %d: %+v, want the anim fields of %+v", i, got, want)
		}
	}
	a2, _ := es.Of(0)
	cu := blocks[0].Slots[2]
	if cu.PID != a2.PID || cu.Bck != mailbox.EnemyBckNone || cu.AnmAttr != 0xFF || cu.Flags&mailbox.EnemySFRelease == 0 {
		t.Errorf("catch-up slot: %+v", cu)
	}
}

// TestMirrorBlockFlags: the block's FLAG_ANIM and FLAG_COLLIDERS follow
// the STATE's StateFlagAnim and StateFlagColliders, both, neither or
// one; FLAG_MIRROR is always set.
func TestMirrorBlockFlags(t *testing.T) {
	_, m, _, tick := clientRoom(t)
	start := time.Unix(2000, 0)
	c := NewMirror(m, log.Discard{}, nil, false, start)
	m.Record(true)
	now := start
	for i, f := range []uint8{StateFlagAnim | StateFlagColliders, 0, StateFlagAnim, StateFlagColliders} {
		st := hostState(uint32(i+1), uint32(i*16))
		st.Flags = StateFlagInScope | f
		pushState(t, c, st, now)
		tick()
		if _, _, err := c.Tick(now); err != nil {
			t.Fatal(err)
		}
		blocks := blockWrites(t, m)
		if len(blocks) != i+1 {
			t.Fatalf("state flags %02b: %d blocks", f, len(blocks))
		}
		b := blocks[i]
		anim, cols := b.Flags&mailbox.EnemyFlagAnim != 0, b.Flags&mailbox.EnemyFlagColliders != 0
		if b.Flags&mailbox.EnemyFlagMirror == 0 || anim != (f&StateFlagAnim != 0) || cols != (f&StateFlagColliders != 0) {
			t.Errorf("state flags %02b: block flags %04b", f, b.Flags)
		}
		now = now.Add(16 * time.Millisecond)
	}
}

// readLogMem logs every Read (address and length) over a FakeMem.
type readLogMem struct {
	*mem.FakeMem
	reads [][2]uint32
}

func (r *readLogMem) Read(a uint32, n int) ([]byte, error) {
	r.reads = append(r.reads, [2]uint32{a, uint32(n)})
	return r.FakeMem.Read(a, n)
}

// TestMirrorReadHit: readHit fills At type and atp (obj+0x10, +0x14),
// AtSPrm (the low byte of the u32 at obj+0x50), Se, HitMark and Spl
// (obj+0x6C, +0x6E, +0x6F) from one 0x60-byte read of the hit object.
func TestMirrorReadHit(t *testing.T) {
	fm := mem.NewFakeMem()
	rm := &readLogMem{FakeMem: fm}
	start := time.Unix(2000, 0)
	c := NewMirror(rm, log.Discard{}, nil, false, start)
	const actor, pid, obj = uint32(0x80C00000), uint32(0x1234), uint32(0x80D00000)
	c.Stats.LastStatus.Status[0] = mailbox.EnemySlotStatus{Actor: actor, PID: pid, State: mailbox.EnemyStateMirrored}
	store(t, fm, actor+addr.Mo2OffTgCyl+addr.Mo2CcTgHitObjOff, binary.BigEndian.AppendUint32(nil, obj))
	store(t, fm, actor+addr.Mo2OffTgCyl+addr.Mo2CcCenterOff, f32s(4, 5, 6))
	store(t, fm, obj+0x10, []byte{0, 0x08, 0, 0, 3})
	store(t, fm, obj+addr.CcAtOffSPrm, []byte{0xAA, 0xBB, 0xCC, 0x02})
	store(t, fm, obj+addr.CcAtOffSe, []byte{0x11, 0x99, 0x03, 0x01}) // mSe, mMtrl, mHitMark, mSpl
	store(t, fm, obj+0x0C, []byte{0xEE, 0xEE, 0xEE, 0xEE})           // outside the read
	store(t, fm, obj+0x70, []byte{0xEE, 0xEE, 0xEE, 0xEE})
	st := hostState(1, 0)
	h, err := c.readHit(st, 0, pid, 0, mailbox.EnemyHitEntry{HitSeq: 5, Collider: ColliderTgCyl, Pending: 1})
	if err != nil || h == nil {
		t.Fatalf("readHit: %v %v", h, err)
	}
	if h.AtType != 0x00080000 || h.Atp != 3 || h.AtSPrm != 0x02 || h.Se != 0x11 || h.HitMark != 0x03 || h.Spl != 0x01 ||
		h.Collider != ColliderTgCyl || h.HitSeq != 5 || h.SetID != 0 || h.Pos != [3]float32{4, 5, 6} {
		t.Errorf("hit: %+v", h)
	}
	var objReads [][2]uint32
	for _, r := range rm.reads {
		if r[0] >= obj && r[0] < obj+0x1000 {
			objReads = append(objReads, r)
		}
	}
	if len(objReads) != 1 || objReads[0] != [2]uint32{obj + 0x10, 0x60} {
		t.Errorf("hit object reads %x, want one of 0x60 bytes at obj+0x10", objReads)
	}
}
