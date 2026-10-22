package enemysync

import (
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// staleRig is a mirror armed over the three Moblins of clientRoom: two
// STATEs mirrored at start, the second block's writes recorded (last).
type staleRig struct {
	t    *testing.T
	c    *Mirror
	m    *mem.FakeMem
	rep  *recReporter
	tick func()
	last mailbox.EnemyBlock
}

func newStaleRig(t *testing.T, start time.Time) *staleRig {
	t.Helper()
	_, m, _, tick := clientRoom(t)
	rep := &recReporter{}
	c := NewMirror(m, rep, nil, false, start)
	c.StaleHold = StaleHoldMax
	pushState(t, c, hostState(1, 0), start)
	tick()
	if _, _, err := c.Tick(start); err != nil {
		t.Fatal(err)
	}
	m.Record(true)
	pushState(t, c, hostState(2, 16), start)
	tick()
	if _, _, err := c.Tick(start); err != nil {
		t.Fatal(err)
	}
	b := blockWrites(t, m)
	if len(b) != 1 || len(b[0].Slots) != 3 {
		t.Fatalf("arm: %+v", b)
	}
	return &staleRig{t: t, c: c, m: m, rep: rep, tick: tick, last: b[0]}
}

// step ticks the game and the mirror at now, with no new STATE.
func (r *staleRig) step(now time.Time) time.Duration {
	r.t.Helper()
	r.tick()
	hits, sleep, err := r.c.Tick(now)
	if err != nil || len(hits) != 0 {
		r.t.Fatalf("tick at %v: hits %v err %v", now, hits, err)
	}
	return sleep
}

// TestMirrorStaleHold (live 2026-10-22, a loaded machine): the host's
// STATE stops for longer than StaleAfter and comes back. The copies are
// held frozen meanwhile (each block the last one's slots, same pids and
// epoch, MorfRate 0, no SNAP), one hold, never a count-0 block, and the
// STATE back resumes the mirror; "not mirroring" is never logged.
func TestMirrorStaleHold(t *testing.T) {
	start := time.Unix(2000, 0)
	r := newStaleRig(t, start)
	c, m := r.c, r.m
	var now time.Time
	for i := 1; i <= 8; i++ { // StaleAfter + 2 s: within StaleHoldMax
		now = start.Add(StaleAfter + time.Duration(i)*GateSleep)
		if sleep := r.step(now); sleep != GateSleep {
			t.Fatalf("stall %d: sleep %v", i, sleep)
		}
	}
	blks := blockWrites(t, m)
	if len(blks) != 9 || c.Stats.Holds != 1 || c.holdSince.IsZero() {
		t.Fatalf("stall: %d blocks, %d holds", len(blks), c.Stats.Holds)
	}
	for i, b := range blks[1:] {
		if b.Seq != r.last.Seq+uint32(i)+1 || b.Epoch != r.last.Epoch || len(b.Slots) != 3 || b.Stage != "M_Dai" || b.Room != 1 || b.KindMask != r.last.KindMask {
			t.Fatalf("held block %d: %+v", i, b)
		}
		for j, s := range b.Slots {
			w := r.last.Slots[j]
			if s.PID != w.PID || s.Pos != w.Pos || s.MorfRate != 0 || s.Flags&(mailbox.EnemySFSnap|mailbox.EnemySFRelease) != 0 {
				t.Errorf("held block %d slot %d: %+v, last %+v", i, j, s, w)
			}
		}
	}
	// The STATE back: the mirror resumes, the hold over.
	now = now.Add(16 * time.Millisecond)
	pushState(t, c, hostState(3, 1000), now)
	if sleep := r.step(now); sleep != 0 {
		t.Fatalf("back: sleep %v", sleep)
	}
	blks = blockWrites(t, m)
	b := blks[len(blks)-1]
	if len(b.Slots) != 3 || b.Slots[1].Pos != [3]float32{100, 10, 1000} || b.Epoch != r.last.Epoch || !c.holdSince.IsZero() {
		t.Fatalf("back: %+v", b)
	}
	for i, b := range blks {
		if len(b.Slots) != 3 {
			t.Errorf("block %d has %d slots: the copies were released", i, len(b.Slots))
		}
	}
	log := r.rep.String()
	if !strings.Contains(log, "the host's STATE is stale (a stall?); holding the copies frozen (up to 2.5s)") || strings.Contains(log, "not mirroring") {
		t.Errorf("log: %s", log)
	}
}

// TestMirrorStaleRelease: a STATE stale past StaleAfter + StaleHold
// releases as before (a count-0 block, once, "not mirroring"), and a
// STATE back after it mirrors again; StaleHold 0 releases at StaleAfter.
func TestMirrorStaleRelease(t *testing.T) {
	start := time.Unix(2000, 0)
	r := newStaleRig(t, start)
	c, m := r.c, r.m
	r.step(start.Add(StaleAfter + 16*time.Millisecond))
	if c.Stats.Holds != 1 {
		t.Fatalf("not held: %d holds", c.Stats.Holds)
	}
	now := start.Add(StaleAfter + StaleHoldMax + time.Millisecond)
	if sleep := r.step(now); sleep != GateSleep {
		t.Fatalf("past the hold: sleep %v", sleep)
	}
	now = now.Add(GateSleep)
	r.step(now)
	blks := blockWrites(t, m)
	if len(blks) != 3 || len(blks[1].Slots) != 3 || len(blks[2].Slots) != 0 {
		t.Fatalf("past the hold: %+v", blks)
	}
	if !strings.Contains(r.rep.String(), "not mirroring: the host's STATE is stale|") {
		t.Errorf("log: %s", r.rep.String())
	}
	now = now.Add(GateSleep)
	pushState(t, c, hostState(3, 1000), now)
	if sleep := r.step(now); sleep != 0 {
		t.Fatalf("back: sleep %v", sleep)
	}
	if b := blockWrites(t, m); len(b) != 4 || len(b[3].Slots) != 3 {
		t.Fatalf("back: %+v", b)
	}

	// StaleHold 0: the old gate.
	r = newStaleRig(t, start)
	r.c.StaleHold = 0
	if sleep := r.step(start.Add(StaleAfter + 16*time.Millisecond)); sleep != GateSleep {
		t.Fatalf("hold 0: sleep %v", sleep)
	}
	if b := blockWrites(t, r.m); len(b) != 2 || len(b[1].Slots) != 0 || r.c.Stats.Holds != 0 {
		t.Fatalf("hold 0: %+v, %d holds", b, r.c.Stats.Holds)
	}
}

// TestMirrorStaleAfterOutOfPlay: a host out of play (held up to
// HostHoldMax) whose STATE then stops is released StaleHold past
// StaleAfter, not HostHoldMax: a host gone is gone.
func TestMirrorStaleAfterOutOfPlay(t *testing.T) {
	start := time.Unix(2000, 0)
	r := newStaleRig(t, start)
	c := r.c
	ka := hostState(3, 32)
	ka.Flags, ka.Slots = 0, nil
	now := start.Add(GateSleep)
	pushState(t, c, ka, now)
	r.step(now)
	r.step(now.Add(StaleAfter + time.Second))
	if c.Stats.Holds != 1 || c.holdSince.IsZero() {
		t.Fatalf("held: %d holds", c.Stats.Holds)
	}
	r.step(now.Add(StaleAfter + StaleHoldMax + time.Millisecond))
	if b := blockWrites(t, r.m); len(b[len(b)-1].Slots) != 0 {
		t.Fatalf("past the hold: %+v", b[len(b)-1])
	}
}

func TestParseStaleHold(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"", StaleHoldMax, true},
		{"2s", 2 * time.Second, true},
		{" 1500ms ", 1500 * time.Millisecond, true},
		{"3000", 3 * time.Second, true},
		{"0", 0, true},
		{"-1s", StaleHoldMax, false},
		{"soon", StaleHoldMax, false},
	} {
		got, err := ParseStaleHold(tc.in)
		if got != tc.want || (err == nil) != tc.ok {
			t.Errorf("ParseStaleHold(%q) = %v, %v; want %v, ok %v", tc.in, got, err, tc.want, tc.ok)
		}
	}
	t.Setenv("WW_ENEMY_STALE_HOLD", "1s")
	_, m, _, _ := clientRoom(t)
	if c := NewMirror(m, &recReporter{}, nil, false, time.Unix(2000, 0)); c.StaleHold != time.Second {
		t.Errorf("NewMirror: StaleHold %v", c.StaleHold)
	}
}

// TestMirrorHeartbeatHitch (live 2026-10-22): the heartbeat moving 15
// to 23 frames over a 16 ms tick is this Go stalled between the tick's
// stamp and the read, not a save state: no epoch, the copies stay (no
// SNAP, no count-0 block). A stall the clock saw (a second around the
// reads, 200 frames run) is none either; the same 200 frames with the
// clock still is a save state, and so is the heartbeat going backwards.
func TestMirrorHeartbeatHitch(t *testing.T) {
	start := time.Unix(2000, 0)
	clk := time.Unix(9000, 0)
	_, m, _, tick := clientRoom(t)
	c := NewMirror(m, &recReporter{}, nil, false, start)
	c.StaleHold = StaleHoldMax
	c.clock = func() time.Time { return clk }
	now := start
	step := func(frames uint32, real time.Duration) mailbox.EnemyBlock {
		t.Helper()
		if frames > 0 {
			hb, _ := mem.ReadU32(m, mailbox.SpawnTrigger)
			store(t, m, mailbox.SpawnTrigger, binary.BigEndian.AppendUint32(nil, hb+frames-1))
		}
		tick() // one more frame, and the REL's
		now = now.Add(16 * time.Millisecond)
		clk = clk.Add(real)
		pushState(t, c, hostState(uint32(now.Sub(start)/time.Millisecond), uint32(now.Sub(start)/time.Millisecond)), now)
		m.ResetWrites()
		if _, sleep, err := c.Tick(now); err != nil || sleep != 0 {
			t.Fatalf("tick: sleep %v err %v", sleep, err)
		}
		b := blockWrites(t, m)
		if len(b) != 1 {
			t.Fatalf("%d blocks", len(b))
		}
		return b[0]
	}
	m.Record(true)
	step(1, 16*time.Millisecond)
	step(1, 16*time.Millisecond)
	epoch := c.Epoch()
	for _, d := range []uint32{15, 23, 60} {
		b := step(d, 16*time.Millisecond)
		if c.Epoch() != epoch || b.Epoch != epoch || len(b.Slots) != 3 || b.Slots[0].Flags&mailbox.EnemySFSnap != 0 {
			t.Fatalf("a %d-frame hitch: epoch %d (was %d), block %+v", d, c.Epoch(), epoch, b)
		}
	}
	if b := step(200, time.Second); c.Epoch() != epoch || b.Slots[0].Flags&mailbox.EnemySFSnap != 0 {
		t.Fatalf("a 1 s stall the clock saw: epoch %d (was %d)", c.Epoch(), epoch)
	}
	if b := step(200, 16*time.Millisecond); c.Epoch() != epoch+1 || b.Epoch != epoch+1 {
		t.Fatalf("200 frames in 16 ms: epoch %d, want %d", c.Epoch(), epoch+1)
	}
	hb, _ := mem.ReadU32(m, mailbox.SpawnTrigger)
	store(t, m, mailbox.SpawnTrigger, binary.BigEndian.AppendUint32(nil, hb-50))
	if step(0, 16*time.Millisecond); c.Epoch() != epoch+2 {
		t.Fatalf("backwards: epoch %d, want %d", c.Epoch(), epoch+2)
	}
}

// TestMirrorStaleHitchTogether: the stale half of the live sequence, the
// client's own stall: its read loop and its ticks stop together, the
// game runs on. The first tick after it sees a STATE past StaleAfter and
// the heartbeat moved 21 frames: held, no epoch, and the STATEs that
// queued meanwhile resume the mirror with the same copies (the epoch
// half is TestMirrorHeartbeatHitch's).
func TestMirrorStaleHitchTogether(t *testing.T) {
	start := time.Unix(2000, 0)
	r := newStaleRig(t, start)
	c := r.c
	epoch := c.Epoch()
	hb, _ := mem.ReadU32(r.m, mailbox.SpawnTrigger)
	store(t, r.m, mailbox.SpawnTrigger, binary.BigEndian.AppendUint32(nil, hb+20))
	now := start.Add(700 * time.Millisecond)
	if sleep := r.step(now); sleep != GateSleep || c.Epoch() != epoch || c.Stats.Holds != 1 {
		t.Fatalf("stall: sleep %v epoch %d (was %d) holds %d", sleep, c.Epoch(), epoch, c.Stats.Holds)
	}
	now = now.Add(GateSleep)
	pushState(t, c, hostState(3, 700), now)
	if sleep := r.step(now); sleep != 0 {
		t.Fatalf("back: sleep %v", sleep)
	}
	for i, b := range blockWrites(t, r.m) {
		if len(b.Slots) != 3 || b.Epoch != epoch {
			t.Errorf("block %d: %d slots epoch %d", i, len(b.Slots), b.Epoch)
		}
	}
}
