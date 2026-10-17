package gate

import (
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// good returns a snapshot that passes every gate, at heartbeat hb.
func good(hb uint32) *state.Snapshot {
	return &state.Snapshot{
		StageName: "sea",
		StageRoom: 44,
		PlayerPtr: fakegame.PlayerActor,
		Table:     addr.TableSea,
		StayNo:    44,
		Heartbeat: hb,
	}
}

// run observes n good ticks, heartbeat 1..n, and returns the last status.
func run(t *testing.T, tr *Tracker, n int) Status {
	t.Helper()
	var st Status
	for i := 1; i <= n; i++ {
		st = tr.Observe(good(uint32(i)))
	}
	return st
}

// TestStableAfterFourUnchangedTicks: STABLE opens on the 4th tick after
// the first one that showed the stage and room, and a room change starts
// the count again (§6.3: "at least 4 ticks or about 400 ms later").
func TestStableAfterFourUnchangedTicks(t *testing.T) {
	var tr Tracker
	for i := 0; i < 8; i++ {
		st := tr.Observe(good(uint32(i)))
		if st.StableRun != i {
			t.Errorf("tick %d: StableRun = %d", i, st.StableRun)
		}
		if want := i >= StableTicks; st.Stable != want {
			t.Errorf("tick %d: Stable = %v (%s), want %v", i, st.Stable, st.StableWhy, want)
		}
		if !st.Stable && !strings.Contains(st.StableWhy, "settling") {
			t.Errorf("tick %d: StableWhy = %q", i, st.StableWhy)
		}
	}
	for i := 0; i < 6; i++ {
		s := good(uint32(100 + i))
		s.StayNo = 45 // Link walked into the next room
		st := tr.Observe(s)
		if want := i >= StableTicks; st.Stable != want || st.StableRun != i {
			t.Errorf("after the room change, tick %d: Stable %v run %d", i, st.Stable, st.StableRun)
		}
	}
}

// TestStableTruthTable closes STABLE with each of its conditions in turn.
// Each failing tick says why and restarts the count, so STABLE needs
// StableTicks more good ticks after it.
func TestStableTruthTable(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*state.Snapshot)
		why  string
	}{
		{"no Link", func(s *state.Snapshot) { s.PlayerPtr = 0 }, "Link is not loaded"},
		{"no stage", func(s *state.Snapshot) { s.StageName = "" }, "no stage"},
		{"title", func(s *state.Snapshot) { s.StageName = "sea_T" }, "title"},
		{"file select", func(s *state.Snapshot) { s.StageName = "Name" }, "file select"},
		{"no table", func(s *state.Snapshot) { s.Table = -1 }, "no save table"},
		{"bad table", func(s *state.Snapshot) { s.Table = 16 }, "not valid"},
		{"stage change pending", func(s *state.Snapshot) { s.NextEnable = 1 }, "stage change is pending"},
		{"new stage", func(s *state.Snapshot) { s.StageName = "M_NewD2" }, "settling"},
		{"new room", func(s *state.Snapshot) { s.StayNo = 3 }, "settling"},
		{"new table", func(s *state.Snapshot) { s.Table = addr.TableMisc }, "settling"},
		{"new start point", func(s *state.Snapshot) { s.StagePoint = 1 }, "settling"},
		{"new layer", func(s *state.Snapshot) { s.StageLayer = 1 }, "settling"},
	}
	for _, c := range cases {
		var tr Tracker
		if st := run(t, &tr, 6); !st.Stable {
			t.Fatalf("%s: not stable before the change: %s", c.name, st.StableWhy)
		}
		bad := good(7)
		c.mut(bad)
		st := tr.Observe(bad)
		if st.Stable || !strings.Contains(st.StableWhy, c.why) {
			t.Errorf("%s: Stable %v, why %q; want closed with %q", c.name, st.Stable, st.StableWhy, c.why)
		}
		if st.CanWrite() || st.CanWriteIdle() || st.CanWriteCounter(KeyNum) {
			t.Errorf("%s: a write is allowed while STABLE is closed", c.name)
		}
		// Recovery takes a full count after a failing tick. After a key
		// change the changed tick was the count's first.
		need := StableTicks
		settling := strings.Contains(c.why, "settling")
		if settling {
			need--
		}
		for i := 0; i <= StableTicks; i++ {
			s := good(uint32(8 + i))
			if settling {
				c.mut(s)
			}
			st = tr.Observe(s)
			if want := i >= need; st.Stable != want {
				t.Errorf("%s: recovery tick %d: Stable %v (%s)", c.name, i, st.Stable, st.StableWhy)
			}
		}
	}
}

// TestOnTransition: the first tick mNextStage is enabled after a settled
// stage is the on-transition write tick (§6.5), and no other.
func TestOnTransition(t *testing.T) {
	var tr Tracker
	run(t, &tr, 5) // StableRun 4: settled
	s := good(6)
	s.NextEnable = 1
	st := tr.Observe(s)
	if !st.OnTransition || st.Stable || !st.CanWriteOnTransition() {
		t.Fatalf("edge tick: OnTransition %v Stable %v", st.OnTransition, st.Stable)
	}
	s = good(7)
	s.NextEnable = 1
	if st = tr.Observe(s); st.OnTransition {
		t.Error("OnTransition held on the second enabled tick")
	}

	// The edge tick counts toward the unchanged ticks, as it would for
	// STABLE: 4 stable ticks then the edge is enough, 3 are not.
	for _, c := range []struct {
		before int
		want   bool
	}{{4, true}, {3, false}} {
		tr = Tracker{}
		run(t, &tr, c.before)
		s = good(uint32(c.before + 1))
		s.NextEnable = 1
		if st = tr.Observe(s); st.OnTransition != c.want {
			t.Errorf("edge after %d stable ticks: OnTransition %v", c.before, st.OnTransition)
		}
	}

	// Link gone, or the stage already changed, on the edge tick: no.
	for _, mut := range []func(*state.Snapshot){
		func(s *state.Snapshot) { s.PlayerPtr = 0 },
		func(s *state.Snapshot) { s.StageName = "M_NewD2" },
	} {
		tr = Tracker{}
		run(t, &tr, 6)
		s = good(7)
		s.NextEnable = 1
		mut(s)
		if st = tr.Observe(s); st.OnTransition {
			t.Error("OnTransition with a core condition failing")
		}
	}

	// A stalled heartbeat blocks the write, not the flag.
	tr = Tracker{}
	run(t, &tr, 6)
	s = good(6)
	s.NextEnable = 1
	if st = tr.Observe(s); !st.OnTransition || st.CanWriteOnTransition() {
		t.Errorf("stalled edge: OnTransition %v, CanWriteOnTransition %v", st.OnTransition, st.CanWriteOnTransition())
	}
}

// TestIdleTruthTable closes IDLE with each condition, and checks the
// per-counter rule.
func TestIdleTruthTable(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*state.Snapshot)
		why  string
	}{
		{"event running", func(s *state.Snapshot) { s.EvtMode = 1 }, "event is running"},
		{"event queued", func(s *state.Snapshot) { s.EvtOrderCount = 1 }, "event is queued"},
		{"pause menu", func(s *state.Snapshot) { s.MenuPause = 1 }, "pause menu"},
		{"minigame", func(s *state.Snapshot) { s.MiniGameType = 2 }, "minigame"},
		{"refight", func(s *state.Snapshot) { s.RefightStageNo = 1 }, "refight"},
	}
	for _, c := range cases {
		var tr Tracker
		if st := run(t, &tr, 6); !st.Idle || st.IdleRun != 6 || !st.CanWriteIdle() {
			t.Fatalf("%s: not idle before: %+v", c.name, st)
		}
		bad := good(7)
		c.mut(bad)
		st := tr.Observe(bad)
		if st.Idle || st.IdleRun != 0 || !strings.Contains(st.IdleWhy, c.why) {
			t.Errorf("%s: Idle %v run %d why %q", c.name, st.Idle, st.IdleRun, st.IdleWhy)
		}
		if !st.Stable || !st.CanWrite() || st.CanWriteIdle() || st.CanWriteCounter(MaxLife) {
			t.Errorf("%s: IDLE closed should leave STABLE writes only", c.name)
		}
		if st = tr.Observe(good(8)); !st.Idle || st.IdleRun != 1 {
			t.Errorf("%s: IDLE did not reopen at once: %+v", c.name, st)
		}
	}

	// A pending counter blocks only itself.
	var tr Tracker
	run(t, &tr, 6)
	s := good(7)
	s.KeyNumCount = 1
	s.MaxMagicCount = -2
	st := tr.Observe(s)
	for c, want := range map[Counter]bool{KeyNum: false, MaxLife: true, MaxMagic: false} {
		if got := st.CanWriteCounter(c); got != want {
			t.Errorf("CanWriteCounter(%s) = %v with pending %d", c, got, st.Pending(c))
		}
	}
	if st.Pending(KeyNum) != 1 || st.Pending(MaxMagic) != -2 || st.Pending(Counter(9)) != 0 {
		t.Errorf("Pending = %d %d %d", st.Pending(KeyNum), st.Pending(MaxLife), st.Pending(MaxMagic))
	}
}

// TestHeartbeat walks the heartbeat through a first tick, a vanilla ISO's
// stillness, running, a pause, a resume, a wrap and a save-state load.
func TestHeartbeat(t *testing.T) {
	var tr Tracker
	step := func(hb uint32) Status { return tr.Observe(good(hb)) }

	if st := step(100); st.Heartbeat || !strings.Contains(st.HeartbeatWhy, "second snapshot") {
		t.Errorf("first tick: %v %q", st.Heartbeat, st.HeartbeatWhy)
	}
	st := step(100)
	if st.Heartbeat || st.Stalled != 1 || !strings.Contains(st.HeartbeatWhy, "not patched") {
		t.Errorf("never moved: %v stalled %d %q", st.Heartbeat, st.Stalled, st.HeartbeatWhy)
	}
	for hb := uint32(103); hb < 120; hb += 3 {
		st = step(hb)
	}
	if !st.Heartbeat || st.Stalled != 0 || st.HeartbeatWhy != "" || !st.CanWrite() {
		t.Fatalf("running: %+v", st)
	}
	for i := 1; i <= 3; i++ {
		st = step(118)
		if st.Heartbeat || st.Stalled != i || !strings.Contains(st.HeartbeatWhy, "stalled") || st.CanWrite() {
			t.Errorf("paused tick %d: %v stalled %d %q", i, st.Heartbeat, st.Stalled, st.HeartbeatWhy)
		}
		if !st.Stable {
			t.Error("a pause closed STABLE; only writes stop")
		}
	}
	if st = step(121); !st.Heartbeat || st.Stalled != 0 || !st.CanWrite() {
		t.Errorf("resumed: %+v", st)
	}

	// The counter wraps without a stall.
	tr = Tracker{}
	tr.Observe(good(0xFFFFFFFE))
	if st = step(1); !st.Heartbeat {
		t.Errorf("wrap: %q", st.HeartbeatWhy)
	}

	// A save state rolls it back: closed, and STABLE starts over.
	tr = Tracker{}
	run(t, &tr, 10)
	st = step(5)
	if st.Heartbeat || !st.Regressed || !strings.Contains(st.HeartbeatWhy, "backwards") {
		t.Errorf("regression: %+v", st)
	}
	if st.Stable || st.StableRun != 0 {
		t.Errorf("STABLE survived a regression: run %d", st.StableRun)
	}
	if st = step(8); !st.Heartbeat || st.Regressed || st.Stable || st.StableRun != 1 {
		t.Errorf("after the regression: %+v", st)
	}
}

// TestFailedRead: a nil snapshot closes everything and forgets the
// history.
func TestFailedRead(t *testing.T) {
	var tr Tracker
	run(t, &tr, 10)
	st := tr.Observe(nil)
	if st.Stable || st.Idle || st.Heartbeat || st.OnTransition || st.CanWrite() {
		t.Fatalf("failed read: %+v", st)
	}
	if st.Why() == "" || st.StableWhy == "" || st.IdleWhy == "" {
		t.Errorf("failed read gives no reason: %+v", st)
	}
	st = tr.Observe(good(11))
	if st.Heartbeat || st.Stable || st.StableRun != 0 {
		t.Errorf("history survived a failed read: %+v", st)
	}
}

func TestWhyOrder(t *testing.T) {
	st := Status{StableWhy: "s", IdleWhy: "i", HeartbeatWhy: "h"}
	if st.Why() != "h" {
		t.Errorf("Why = %q, want the heartbeat first", st.Why())
	}
	st.HeartbeatWhy = ""
	if st.Why() != "s" {
		t.Errorf("Why = %q, want STABLE next", st.Why())
	}
	st.StableWhy = ""
	if st.Why() != "i" {
		t.Errorf("Why = %q", st.Why())
	}
	if (Status{Stable: true, Idle: true, Heartbeat: true}).Why() != "" {
		t.Error("an open status has a reason")
	}
}

func TestEvaluate(t *testing.T) {
	var hist []*state.Snapshot
	var tr Tracker
	var want Status
	for i := range 7 {
		s := good(uint32(i))
		if i == 3 {
			s.MenuPause = 1
		}
		hist = append(hist, s)
		want = tr.Observe(s)
	}
	if got := Evaluate(hist); got != want {
		t.Errorf("Evaluate = %+v, Tracker = %+v", got, want)
	}
	if got := Evaluate(nil); got.CanWrite() {
		t.Error("an empty history allows writes")
	}
}

// TestGatesOverFakeGame runs the real snapshot reader and the tracker over
// the fake game through a session: boot, title, file select, a stage, a
// room change, a stage change, the pause menu, a paused emulator and a
// pending key count.
func TestGatesOverFakeGame(t *testing.T) {
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	var tr Tracker
	tick := func(frames int) Status {
		t.Helper()
		g.Frames(frames)
		s, err := state.Read(m, state.Options{})
		if err != nil {
			t.Fatal(err)
		}
		return tr.Observe(s)
	}
	settle := func(what string) {
		t.Helper()
		for i := 0; i < StableTicks; i++ {
			if st := tick(3); st.Stable {
				t.Fatalf("%s: stable %d ticks in", what, i)
			}
		}
		if st := tick(3); !st.CanWriteIdle() {
			t.Fatalf("%s: not writable after %d ticks: %s", what, StableTicks, st.Why())
		}
	}

	if st := tick(3); st.Stable || st.StableWhy != "Link is not loaded" {
		t.Errorf("boot: %q", st.StableWhy)
	}
	if err := g.LoadStage(fakegame.Stage{Name: "sea_T", Room: 44, NoPlayer: true}); err != nil {
		t.Fatal(err)
	}
	if st := tick(3); st.Stable || st.StableWhy != "Link is not loaded" {
		t.Errorf("title: %q", st.StableWhy)
	}
	g.EnterFileSelect()
	if err := g.NewGame(0, "Link"); err != nil {
		t.Fatal(err)
	}
	if st := tick(3); st.Stable {
		t.Errorf("file select is stable")
	}

	outset := fakegame.Stage{Name: "sea", Room: 44, Table: addr.TableSea}
	if err := g.LoadStage(outset); err != nil {
		t.Fatal(err)
	}
	settle("Outset")

	if err := g.EnterRoom(45); err != nil {
		t.Fatal(err)
	}
	settle("next room")

	drc := fakegame.Stage{Name: "M_NewD2", Table: addr.TableDRC, KeyHUD: true}
	if err := g.SetNextStage(drc); err != nil {
		t.Fatal(err)
	}
	if st := tick(3); !st.CanWriteOnTransition() || st.Stable {
		t.Errorf("loading zone: OnTransition %v Stable %v", st.OnTransition, st.Stable)
	}
	if err := g.ChangeStage(); err != nil {
		t.Fatal(err)
	}
	settle("DRC")

	g.SetPauseMenu(true)
	if st := tick(3); st.Idle || !st.CanWrite() {
		t.Errorf("pause menu: Idle %v CanWrite %v", st.Idle, st.CanWrite())
	}
	g.SetPauseMenu(false)
	if st := tick(0); st.Heartbeat || st.CanWrite() {
		t.Errorf("no frames ran but the heartbeat gate is open")
	}
	if st := tick(3); !st.CanWriteIdle() {
		t.Errorf("after resuming: %s", st.Why())
	}

	// The tool asks for a key on a stage with no key counter: the count
	// stays pending, so no second key may be written on top of it.
	if err := g.LoadStage(outset); err != nil {
		t.Fatal(err)
	}
	settle("Outset again")
	if err := mem.WriteU16(m, addr.ItemKeyNumCount, 1); err != nil {
		t.Fatal(err)
	}
	st := tick(3)
	if st.CanWriteCounter(KeyNum) || !st.CanWriteCounter(MaxLife) {
		t.Errorf("pending key: key %v max-life %v", st.CanWriteCounter(KeyNum), st.CanWriteCounter(MaxLife))
	}
}
