package replica

import (
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

const testChestBase = 0x81200000 // d_a_tbox.rel's .text in the tests

// testChest is chest no of table 3 at x, closed (actionOpenWait).
func testChest(pid uint32, no uint8, x float32) ChestActor {
	c := ChestActor{PID: pid, Addr: 0x80800000 + pid*0x1000, Params: uint32(no) << 7, Key: ChestKey{Table: 3, No: no},
		Pos: [3]float32{x, 0, 0}, Base: testChestBase}
	c.Word = c.OpenWait()
	return c
}

func held(c ChestActor) ChestActor { c.Word = c.Wait(); return c }

// chestIn is a tick with Link at the origin, a partner in scope and
// chests cs.
func chestIn(now time.Time, cs ...ChestActor) chestInput {
	return chestInput{on: true, scope: true, now: now, linkOK: true, room: 1, chests: cs}
}

type sent []*wire.Claim

func (s *sent) send(m *wire.Claim) { *s = append(*s, m) }

func (s sent) ops() []wire.ClaimOp {
	var out []wire.ClaimOp
	for _, m := range s {
		out = append(out, m.Op)
	}
	return out
}

func pids(cs []ChestActor) []uint32 {
	var out []uint32
	for _, c := range cs {
		out = append(out, c.PID)
	}
	return out
}

// TestChestCycle: a far closed chest is held; Link coming in range asks
// for it; the GRANT restores it; walking away for ChestReleaseAfter
// releases it, and it is held again.
func TestChestCycle(t *testing.T) {
	var s chestState
	t0 := time.Unix(1_800_000_000, 0)
	c := testChest(10, 2, 1000)
	var out sent
	p := s.decide(chestIn(t0, c), out.send)
	if len(p.Hold) != 1 || len(p.Restore) != 0 || len(out) != 0 {
		t.Fatalf("far chest: plan %v/%v, sent %v; want one hold", pids(p.Hold), pids(p.Restore), out.ops())
	}
	s.held[c.PID] = held(c) // the write landed (applyChests)

	near := held(c)
	near.Pos[0] = 350
	p = s.decide(chestIn(t0.Add(time.Second), near), out.send)
	if len(p.Hold)+len(p.Restore) != 0 || len(out) != 1 || out[0].Op != wire.ClaimAsk || out[0].No != 2 || out[0].Table != 3 {
		t.Fatalf("in range: plan %v/%v, sent %+v; want one CLAIM", pids(p.Hold), pids(p.Restore), out)
	}
	// No second ask within ChestAskEvery.
	out = nil
	s.decide(chestIn(t0.Add(1500*time.Millisecond), near), out.send)
	if len(out) != 0 {
		t.Fatalf("asked again within %v: %v", ChestAskEvery, out.ops())
	}
	s.onClaim(&wire.Claim{Op: wire.ClaimGrant, Table: 3, No: 2})
	p = s.decide(chestIn(t0.Add(2*time.Second), near), out.send)
	if len(p.Restore) != 1 || p.Restore[0].PID != c.PID {
		t.Fatalf("granted: restores %v, want [%d]", pids(p.Restore), c.PID)
	}
	delete(s.held, c.PID) // restored
	// Walk away: released after ChestReleaseAfter past ChestReleaseRange.
	gone := c
	gone.Pos[0] = ChestReleaseRange + 100
	out = nil
	s.decide(chestIn(t0.Add(3*time.Second), gone), out.send)
	s.decide(chestIn(t0.Add(4*time.Second), gone), out.send)
	if len(out) != 0 {
		t.Fatalf("released too soon: %v", out.ops())
	}
	p = s.decide(chestIn(t0.Add(3*time.Second+ChestReleaseAfter), gone), out.send)
	if len(out) != 1 || out[0].Op != wire.ClaimRelease {
		t.Fatalf("after %v away: sent %v, want [RELEASE]", ChestReleaseAfter, out.ops())
	}
	p = s.decide(chestIn(t0.Add(10*time.Second), gone), out.send)
	if len(p.Hold) != 1 {
		t.Fatalf("released chest not held again: holds %v", pids(p.Hold))
	}
}

// TestChestDenyConsumed: a DENY keeps the chest held (asks repeat); a
// CONSUMED chest is never restored, even out of scope.
func TestChestDenyConsumed(t *testing.T) {
	var s chestState
	t0 := time.Unix(1_800_000_000, 0)
	c := testChest(11, 5, 200)
	s.init()
	s.held[c.PID] = held(c)
	var out sent
	s.decide(chestIn(t0, held(c)), out.send)
	s.onClaim(&wire.Claim{Op: wire.ClaimDeny, Table: 3, No: 5})
	p := s.decide(chestIn(t0.Add(2*time.Second), held(c)), out.send)
	if len(p.Restore) != 0 || len(out) != 2 {
		t.Fatalf("denied: restores %v, sent %v; want none and a second CLAIM", pids(p.Restore), out.ops())
	}
	s.onClaim(&wire.Claim{Op: wire.ClaimConsumed, Table: 3, No: 5})
	for _, in := range []chestInput{chestIn(t0.Add(3*time.Second), held(c)), {on: false, now: t0.Add(4 * time.Second), chests: []ChestActor{held(c)}}} {
		if p := s.decide(in, out.send); len(p.Restore) != 0 || !p.Consumed[c.Key] {
			t.Fatalf("consumed (on %v scope %v): restores %v consumed %v", in.on, in.scope, pids(p.Restore), p.Consumed)
		}
	}
}

// TestChestFailOpen: out of scope or off, our holds are restored; a
// vanilla open chest (actionWait, not ours) never is; a chest whose bit
// reads set is never restored.
func TestChestFailOpen(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	ours, open, opened := testChest(12, 1, 1000), held(testChest(13, 2, 1000)), testChest(14, 3, 1000)
	for _, in := range []chestInput{
		{on: true, scope: false, now: t0, linkOK: true},
		{on: false, scope: false, now: t0, linkOK: true},
	} {
		var s chestState
		s.init()
		s.held[ours.PID] = held(ours)
		s.held[opened.PID] = held(opened)
		in.chests = []ChestActor{held(ours), open, held(opened)}
		in.bits = 1 << 3
		var out sent
		p := s.decide(in, out.send)
		if len(p.Restore) != 1 || p.Restore[0].PID != ours.PID || len(p.Hold) != 0 {
			t.Errorf("on %v scope %v: restores %v holds %v, want only pid %d restored", in.on, in.scope, pids(p.Restore), pids(p.Hold), ours.PID)
		}
	}
}

// TestChestStaleHeld: a closed chest whose bit reads set (the partner
// opened it before this room loaded) is held whatever the scope.
func TestChestStaleHeld(t *testing.T) {
	var s chestState
	c := testChest(15, 4, 1000)
	in := chestIn(time.Unix(1_800_000_000, 0), c)
	in.scope, in.bits = false, 1<<4
	var out sent
	if p := s.decide(in, out.send); len(p.Hold) != 1 {
		t.Fatalf("stale chest: holds %v, want [%d]", pids(p.Hold), c.PID)
	}
}

// TestChestHoldGates: no hold with Link within ChestHoldFar, an event
// order queued, a busy chest, no Link, or a word that is not
// actionOpenWait.
func TestChestHoldGates(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	near := testChest(20, 1, ChestHoldFar-1)
	busy := testChest(21, 2, 1000)
	busy.Flags = 0x10
	opening := testChest(22, 3, 1000)
	opening.Word = opening.Base + 0x24B4 // actionDemo
	for name, in := range map[string]chestInput{
		"near":    chestIn(t0, near),
		"busy":    chestIn(t0, busy),
		"opening": chestIn(t0, opening),
		"ordered": func() chestInput { i := chestIn(t0, testChest(23, 4, 1000)); i.ordered = true; return i }(),
		"no Link": func() chestInput { i := chestIn(t0, testChest(24, 5, 1000)); i.linkOK = false; return i }(),
	} {
		var s chestState
		var out sent
		if p := s.decide(in, out.send); len(p.Hold) != 0 {
			t.Errorf("%s: held %v", name, pids(p.Hold))
		}
	}
}

func TestPartnerInScope(t *testing.T) {
	st := func(n string) wire.Stage { s, _ := wire.MakeStage(n); return s }
	p := func(stage string, table, room int8) *wire.Presence {
		return &wire.Presence{Stage: st(stage), Table: table, Room: room, Flags: wire.PresenceInGame}
	}
	for _, c := range []struct {
		name    string
		me, you *wire.Presence
		want    bool
	}{
		{"same room", p("M_NewD2", 3, 2), p("M_NewD2", 3, 2), true},
		{"partner at a stage start", p("M_NewD2", 3, 2), p("M_NewD2", 3, -1), true},
		{"another room", p("M_NewD2", 3, 2), p("M_NewD2", 3, 4), false},
		{"another stage", p("M_NewD2", 3, 2), p("sea", 0, 2), false},
		{"no partner", p("M_NewD2", 3, 2), nil, false},
		{"partner not in game", p("M_NewD2", 3, 2), &wire.Presence{Stage: st("M_NewD2"), Table: 3, Room: 2}, false},
	} {
		if got := partnerInScope(c.me, c.you); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// TestCheckChest: the guard lets through exactly a planned hold (armed
// and idle) and a planned restore (not consumed), and nothing else.
func TestCheckChest(t *testing.T) {
	r := newRig(t, "Link")
	s, err := state.Read(r.m, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	c := testChest(30, 6, 1000)
	other := testChest(31, 7, 1000)
	for _, armed := range []bool{true, false} {
		wg := NewWriteGuard(GuardConfig{Targets: TargetChestHold})
		wg.SetTick(Tick{Gates: settledGates(*s), Armed: armed, Table: s.Table})
		if err := wg.checkChest(c, true, c.WordAddr(), c.OpenWait(), c.Wait()); err == nil {
			t.Error("a hold taken with no plan")
		}
		wg.SetChests(ChestPlan{Hold: []ChestActor{c}, Restore: []ChestActor{c}, Consumed: map[ChestKey]bool{}})
		err := wg.checkChest(c, true, c.WordAddr(), c.OpenWait(), c.Wait())
		if armed != (err == nil) {
			t.Errorf("armed %v: a planned hold: %v", armed, err)
		}
		if err := wg.checkChest(c, false, c.WordAddr(), c.Wait(), c.OpenWait()); err != nil {
			t.Errorf("armed %v: a planned restore refused: %v", armed, err)
		}
		for _, bad := range []struct {
			name     string
			c        ChestActor
			hold     bool
			a        uint32
			old, val uint32
		}{
			{"another chest", other, true, other.WordAddr(), other.OpenWait(), other.Wait()},
			{"another address", c, true, c.WordAddr() + 4, c.OpenWait(), c.Wait()},
			{"a hold from another word", c, true, c.WordAddr(), c.Base + 0x24B4, c.Wait()},
			{"a hold to another word", c, true, c.WordAddr(), c.OpenWait(), c.Base + 0x24B4},
			{"a restore of an opening chest", c, false, c.WordAddr(), c.Base + 0x24B4, c.OpenWait()},
		} {
			wg.SetChests(ChestPlan{Hold: []ChestActor{c}, Restore: []ChestActor{c}, Consumed: map[ChestKey]bool{}})
			if err := wg.checkChest(bad.c, bad.hold, bad.a, bad.old, bad.val); err == nil {
				t.Errorf("armed %v: %s taken", armed, bad.name)
			}
		}
		wg.SetChests(ChestPlan{Restore: []ChestActor{c}, Consumed: map[ChestKey]bool{c.Key: true}})
		if err := wg.checkChest(c, false, c.WordAddr(), c.Wait(), c.OpenWait()); err == nil {
			t.Errorf("armed %v: a consumed chest restored", armed)
		}
	}
}
