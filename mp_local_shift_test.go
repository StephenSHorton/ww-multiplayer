package main

import (
	"bytes"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/gate"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/mem"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/snap"
)

var defaultShift = [3]float32{50, 0, 50}

// shiftSideAt is a Dolphin in stage (layer, save table) and room with Link
// at pos, its gates judged over gate.StableTicks+1 snapshots with the
// heartbeat moving: all open, unless mod changes the snapshots.
func shiftSideAt(stage string, layer, table, room int8, pos [3]float32, mod func(*snap.Snapshot)) shiftSide {
	var hist []*snap.Snapshot
	for i := 0; i <= gate.StableTicks; i++ {
		s := &snap.Snapshot{StageName: stage, StageLayer: layer, Table: table, StayNo: room,
			PlayerPtr: fakegame.PlayerActor, Heartbeat: uint32(100 + 3*i)}
		if mod != nil {
			mod(s)
		}
		hist = append(hist, s)
	}
	return shiftSide{s: hist[len(hist)-1], st: gate.Evaluate(hist), pos: pos}
}

func TestPlanShift(t *testing.T) {
	here := [3]float32{-200000, 120, 300000}
	near := [3]float32{-199997, 124, 300004} // 5 across the ground, 4 up
	seaAt := func(room int8, pos [3]float32, mod func(*snap.Snapshot)) shiftSide {
		return shiftSideAt("sea", -1, addr.TableSea, room, pos, mod)
	}
	sea := func(mod func(*snap.Snapshot)) shiftSide { return seaAt(44, here, mod) }
	// menuSettling is a side that came into room 44 one tick before the
	// last, with the pause menu open: STABLE is closed (settling) as well
	// as IDLE.
	n := 0
	menuSettling := sea(func(s *snap.Snapshot) {
		if n++; n < gate.StableTicks {
			s.StayNo = 43
		}
		s.MenuPause = 1
	})
	if why := menuSettling.st.Why(); !strings.HasPrefix(why, "settling:") || menuSettling.st.Idle {
		t.Fatalf("menuSettling: %q, IDLE %v; want STABLE settling and IDLE closed", why, menuSettling.st.Idle)
	}
	cases := []struct {
		name   string
		a, b   shiftSide
		d      [3]float32
		target [3]float32 // when skip is ""
		skip   string     // a substring of the reason
		retry  bool
	}{
		{name: "overlap", a: sea(nil), b: sea(nil), d: defaultShift, target: [3]float32{-199950, 120, 300050}},
		{name: "overlap within 5", a: sea(nil), b: seaAt(44, near, nil), d: defaultShift, target: [3]float32{-199947, 124, 300054}},
		{name: "the incident", d: defaultShift, skip: "A is in M_Dai, B in sea",
			a: shiftSideAt("M_Dai", -1, addr.TableET, 0, [3]float32{120, 800, -340}, nil),
			b: seaAt(44, [3]float32{-200050, 0, 300020}, nil)},
		{name: "another stage at the same coordinates", d: defaultShift, skip: "A is in M_Dai, B in sea",
			a: shiftSideAt("M_Dai", -1, addr.TableET, 0, here, nil), b: sea(nil)},
		{name: "another room", a: sea(nil), b: seaAt(45, here, nil), d: defaultShift, skip: "A is in room 44 of sea, B in room 45"},
		{name: "another layer", d: defaultShift, skip: "A is in layer 2 of M_NewD2, B in layer 3",
			a: shiftSideAt("M_NewD2", 2, addr.TableDRC, 2, here, nil), b: shiftSideAt("M_NewD2", 3, addr.TableDRC, 2, here, nil)},
		{name: "another save table", d: defaultShift, skip: "A's save table is 3 DRC, B's 12 subdungeon",
			a: shiftSideAt("M_NewD2", 2, addr.TableDRC, 2, here, nil), b: shiftSideAt("M_NewD2", 2, addr.TableSubdungeon, 2, here, nil)},
		{name: "room not known", a: sea(nil), b: seaAt(-1, here, nil), d: defaultShift, skip: "the room is not known (mStayNo 44 on A, -1 on B)"},
		{name: "70 apart: a restart after a shift", d: defaultShift, skip: "the two Links are 71 units apart",
			a: sea(nil), b: seaAt(44, [3]float32{-199950, 120, 300050}, nil)},
		{name: "6 apart across the ground", a: sea(nil), b: seaAt(44, [3]float32{-200006, 120, 300000}, nil), d: defaultShift, skip: "6 units apart"},
		{name: "6 apart in height", a: sea(nil), b: seaAt(44, [3]float32{-200000, 126, 300000}, nil), d: defaultShift, skip: "6 units apart"},
		{name: "no position", a: sea(nil), b: seaAt(44, [3]float32{float32(math.NaN()), 120, 300000}, nil), d: defaultShift, skip: "units apart"},
		{name: "a stage change pending on A", d: defaultShift, retry: true, skip: "Dolphin A (a stage change is pending)",
			a: sea(func(s *snap.Snapshot) { s.NextEnable = 1 }), b: sea(nil)},
		{name: "a stage change pending on B", d: defaultShift, retry: true, skip: "Dolphin B (a stage change is pending)",
			a: sea(nil), b: sea(func(s *snap.Snapshot) { s.NextEnable = 1 })},
		{name: "an event on B", a: sea(nil), b: sea(func(s *snap.Snapshot) { s.EvtMode = 1 }), d: defaultShift, skip: "Dolphin B (an event is running)"},
		{name: "an event queued on B", a: sea(nil), b: sea(func(s *snap.Snapshot) { s.EvtOrderCount = 1 }), d: defaultShift, skip: "Dolphin B (an event is queued)"},
		{name: "the pause menu on B", a: sea(nil), b: sea(func(s *snap.Snapshot) { s.MenuPause = 1 }), d: defaultShift, skip: "Dolphin B (the pause menu is open)"},
		{name: "no Link on B", d: defaultShift, retry: true, skip: "Dolphin B (Link is not loaded)",
			a: sea(nil), b: sea(func(s *snap.Snapshot) { s.PlayerPtr = 0 })},
		{name: "a stalled heartbeat on B", d: defaultShift, retry: true, skip: "Dolphin B (heartbeat has not moved: is emulation paused, or the ISO not patched?)",
			a: sea(nil), b: sea(func(s *snap.Snapshot) { s.Heartbeat = 7 })},
		// The pause menu, which is why the shift gives up, is named, not
		// the stalled heartbeat or the settling, which alone would be
		// looked at again.
		{name: "a stalled heartbeat on A, the pause menu on B", d: defaultShift, skip: "Dolphin B (the pause menu is open)",
			a: sea(func(s *snap.Snapshot) { s.Heartbeat = 7 }), b: sea(func(s *snap.Snapshot) { s.MenuPause = 1 })},
		{name: "B settling with the pause menu open", a: sea(nil), b: menuSettling, d: defaultShift, skip: "Dolphin B (the pause menu is open)"},
		{name: "shift 0", a: sea(nil), b: sea(nil), skip: "MP_LOCAL_SHIFT_X/Y/Z are all 0"},
		{name: "shift NaN", a: sea(nil), b: sea(nil), d: [3]float32{50, float32(math.NaN()), 50}, skip: "must be numbers"},
		{name: "shift Inf", a: sea(nil), b: sea(nil), d: [3]float32{float32(math.Inf(1)), 0, 50}, skip: "must be numbers"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			target, skip, retry := planShift(c.a, c.b, c.d)
			switch {
			case c.skip == "" && (skip != "" || target != c.target):
				t.Errorf("target %v, skip %q; want target %v", target, skip, c.target)
			case c.skip != "" && !strings.Contains(skip, c.skip):
				t.Errorf("skip %q, want it to say %q", skip, c.skip)
			case retry != c.retry:
				t.Errorf("skip %q: retry %v, want %v", skip, retry, c.retry)
			}
		})
	}
}

// shiftRig is two FakeGames for startupShift: Dolphin A at pid 1 and B at
// pid 2, each in a stage with Link at a position.
type shiftRig struct {
	gA, gB *fakegame.FakeGame
	mA, mB *mem.FakeMem
	opened []uint32
	ticks  int
	// aFrames and bFrames are the tick after which A's and B's frames run;
	// 0 runs them on every tick.
	aFrames, bFrames int
}

func newShiftRig(t *testing.T, sA, sB fakegame.Stage, posA, posB [3]float32) *shiftRig {
	r := &shiftRig{mA: mem.NewFakeMem(), mB: mem.NewFakeMem()}
	r.gA, r.gB = fakegame.New(r.mA), fakegame.New(r.mB)
	for _, x := range []struct {
		g   *fakegame.FakeGame
		m   *mem.FakeMem
		s   fakegame.Stage
		pos [3]float32
	}{{r.gA, r.mA, sA, posA}, {r.gB, r.mB, sB, posB}} {
		if err := x.g.LoadStage(x.s); err != nil {
			t.Fatal(err)
		}
		store(t, x.m, fakegame.PlayerActor+addr.ActorOffCurPos, posBytes(x.pos))
	}
	return r
}

func (r *shiftRig) env(t *testing.T) shiftEnv {
	return shiftEnv{
		open: func(pid uint32) (mem.Mem, func(), error) {
			r.opened = append(r.opened, pid)
			switch pid {
			case 1:
				return r.mA, func() {}, nil
			case 2:
				return r.mB, func() {}, nil
			}
			t.Fatalf("opened pid %d", pid)
			return nil, nil, nil
		},
		tick: func() {
			r.ticks++
			if r.ticks > r.aFrames {
				r.gA.Frames(3)
			}
			if r.ticks > r.bFrames {
				r.gB.Frames(3)
			}
		},
		settle: time.Second,
	}
}

func (r *shiftRig) record(on bool) { r.mA.Record(on); r.mB.Record(on) }

func (r *shiftRig) noWrites(t *testing.T) {
	t.Helper()
	if ws := append(r.mA.Writes(), r.mB.Writes()...); len(ws) != 0 {
		t.Errorf("wrote %+v", ws)
	}
}

// shiftAttempt is the ticks one look at both Dolphins takes: gateSnapshots
// on each.
const shiftAttempt = 2 * gate.StableTicks

// TestStartupShiftAcrossStagesWritesNothing is the owner's incident: A in a
// dungeon, B at sea by Outset. Even with both Links at the same
// coordinates, B's warp request is left as it was.
func TestStartupShiftAcrossStagesWritesNothing(t *testing.T) {
	pos := [3]float32{120, 800, -340}
	r := newShiftRig(t, fakegame.Stage{Name: "M_Dai", Table: addr.TableET}, diagSea, pos, pos)
	warp := func() []byte {
		b, err := r.mB.Read(mailboxBase+mailboxWarpSeq, 20) // warp_seq, warp_ack, warp_x/y/z
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	before := warp()
	rep := &recordingReporter{}
	r.record(true)
	startupShift(rep, false, 1, 2, defaultShift, r.env(t))
	r.noWrites(t)
	if after := warp(); !bytes.Equal(after, before) {
		t.Errorf("B's warp request went from % X to % X", before, after)
	}
	if got := rep.joined(); !strings.Contains(got, "Not moving Dolphin B's Link at start: A is in M_Dai, B in sea.") {
		t.Errorf("log %q", got)
	}
	if r.ticks != shiftAttempt {
		t.Errorf("%d ticks, want one look (%d): another stage is no reason to look again", r.ticks, shiftAttempt)
	}
}

// TestStartupShiftOverlap: two Links on top of each other in one room are
// moved apart from B's own position, once; a restart leaves them be.
func TestStartupShiftOverlap(t *testing.T) {
	posA, posB := [3]float32{100, 200, 300}, [3]float32{102, 201, 299}
	r := newShiftRig(t, diagDRC, diagDRC, posA, posB)
	warps := runWarpHandler(t, r.mB)
	rep := &recordingReporter{}
	r.record(true)
	startupShift(rep, false, 1, 2, defaultShift, r.env(t))
	r.record(false)
	want := [3]float32{152, 201, 349}
	if got := readLinkPos(t, r.mB); got != want {
		t.Errorf("B's Link at %v, want %v (B's own position + the shift)", got, want)
	}
	if got := readLinkPos(t, r.mA); got != posA {
		t.Errorf("A's Link moved to %v", got)
	}
	if ws := r.mA.Writes(); len(ws) != 0 {
		t.Errorf("wrote A: %+v", ws)
	}
	ws := r.mB.Writes()
	if len(ws) != 2 || ws[0].Addr != mailboxBase+mailboxWarpX || !bytes.Equal(ws[0].Data, posBytes(want)) ||
		ws[1].Addr != mailboxBase+mailboxWarpSeq || !bytes.Equal(ws[1].Data, u32b(1)) {
		t.Errorf("wrote B: %+v, want the target then warp_seq = 1", ws)
	}
	if got := rep.joined(); !strings.Contains(got, "Warped Dolphin B Link by (50, 0, 50)") {
		t.Errorf("log %q", got)
	}
	if n := warps(); n != 1 {
		t.Fatalf("%d warps, want 1", n)
	}

	// mp-local restarted: the Links are 70 apart now.
	r.mB.ResetWrites()
	rep = &recordingReporter{}
	r.record(true)
	startupShift(rep, false, 1, 2, defaultShift, r.env(t))
	r.noWrites(t)
	if got := rep.joined(); !strings.Contains(got, "Not moving Dolphin B's Link at start: the two Links are 71 units apart.") {
		t.Errorf("second start: log %q", got)
	}
	if got := readLinkPos(t, r.mB); got != want || warps() != 1 {
		t.Errorf("second start moved B's Link to %v (%d warps)", got, warps())
	}
}

// TestStartupShiftRetries: the shift looks again for a while when only
// STABLE or HEARTBEAT is closed, and not when IDLE is.
func TestStartupShiftRetries(t *testing.T) {
	pos := [3]float32{100, 200, 300}
	t.Run("the heartbeat resumes", func(t *testing.T) {
		r := newShiftRig(t, diagDRC, diagDRC, pos, pos)
		r.bFrames = shiftAttempt // B is paused during the first look
		warps := runWarpHandler(t, r.mB)
		rep := &recordingReporter{}
		startupShift(rep, false, 1, 2, defaultShift, r.env(t))
		if r.ticks != 2*shiftAttempt || warps() != 1 || !strings.Contains(rep.joined(), "Warped Dolphin B Link") {
			t.Errorf("%d ticks, %d warps, log %q; want a second look and the shift", r.ticks, warps(), rep.joined())
		}
	})
	t.Run("the heartbeat stays stalled", func(t *testing.T) {
		r := newShiftRig(t, diagDRC, diagDRC, pos, pos)
		r.bFrames = math.MaxInt
		env := r.env(t)
		env.settle = 50 * time.Millisecond
		rep := &recordingReporter{}
		r.record(true)
		start := time.Now()
		startupShift(rep, false, 1, 2, defaultShift, env)
		r.noWrites(t)
		if got := rep.joined(); !strings.Contains(got, "Not moving Dolphin B's Link at start: Dolphin B (heartbeat has not moved: is emulation paused, or the ISO not patched?) (still so after 50ms).") ||
			strings.Contains(got, "?,") || strings.Contains(got, "?.") {
			t.Errorf("log %q", got)
		}
		if r.ticks < 2*shiftAttempt || time.Since(start) < env.settle {
			t.Errorf("gave up after %d ticks, %s", r.ticks, time.Since(start))
		}
	})
	t.Run("the pause menu", func(t *testing.T) {
		r := newShiftRig(t, diagDRC, diagDRC, pos, pos)
		r.gB.SetPauseMenu(true)
		rep := &recordingReporter{}
		r.record(true)
		startupShift(rep, false, 1, 2, defaultShift, r.env(t))
		r.noWrites(t)
		if got := rep.joined(); r.ticks != shiftAttempt || !strings.Contains(got, "Not moving Dolphin B's Link at start: Dolphin B (the pause menu is open).") {
			t.Errorf("%d ticks, log %q; want one look", r.ticks, got)
		}
	})
	t.Run("A paused, the pause menu on B", func(t *testing.T) {
		r := newShiftRig(t, diagDRC, diagDRC, pos, pos)
		r.aFrames = math.MaxInt
		r.gB.SetPauseMenu(true)
		rep := &recordingReporter{}
		r.record(true)
		startupShift(rep, false, 1, 2, defaultShift, r.env(t))
		r.noWrites(t)
		if got := rep.joined(); r.ticks != shiftAttempt || !strings.Contains(got, "Not moving Dolphin B's Link at start: Dolphin B (the pause menu is open).") {
			t.Errorf("%d ticks, log %q; want one look, and the menu that ended it named", r.ticks, got)
		}
	})
}

// TestStartupShiftNeverUnderCoop: under --coop (and --coop-dry-run, which
// needs it) the shift opens no Dolphin and writes nothing, whatever the
// Links' places. Its one line says MP_LOCAL_SHIFT_X/Y/Z are ignored only
// when one is set.
func TestStartupShiftNeverUnderCoop(t *testing.T) {
	pos := [3]float32{100, 200, 300}
	for _, set := range []string{"", "80"} {
		t.Run("MP_LOCAL_SHIFT_Z="+set, func(t *testing.T) {
			t.Setenv("MP_LOCAL_SHIFT_X", "")
			t.Setenv("MP_LOCAL_SHIFT_Y", "")
			t.Setenv("MP_LOCAL_SHIFT_Z", set)
			r := newShiftRig(t, diagDRC, diagDRC, pos, pos)
			rep := &recordingReporter{}
			r.record(true)
			startupShift(rep, true, 1, 2, defaultShift, r.env(t))
			r.noWrites(t)
			if len(r.opened) != 0 || r.ticks != 0 {
				t.Errorf("opened %v, %d ticks", r.opened, r.ticks)
			}
			if len(rep.lines) != 1 || !strings.Contains(rep.lines[0], "Not moving Dolphin B's Link at start under --coop") ||
				!strings.HasSuffix(rep.lines[0], "walk one a few steps away.") {
				t.Fatalf("log %q, want the one line saying the shift is off", rep.lines)
			}
			// The variables are said to be ignored only when one is set.
			if ignored := strings.Contains(rep.lines[0], "(MP_LOCAL_SHIFT_X/Y/Z ignored)"); ignored != (set != "") {
				t.Errorf("log %q: says the variables are ignored: %v, want %v", rep.lines[0], ignored, set != "")
			}
		})
	}
}
