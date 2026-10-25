package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/combat"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/diag"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/heap/heaptest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

var (
	diagSea = fakegame.Stage{Name: "sea", Room: 44, Table: addr.TableSea}
	diagDRC = fakegame.Stage{Name: "M_NewD2", Point: 3, Room: 2, Layer: 2, Table: addr.TableDRC, KeyHUD: true}
)

func store(t *testing.T, m *mem.FakeMem, a uint32, b []byte) {
	t.Helper()
	if err := m.Store(a, b); err != nil {
		t.Fatal(err)
	}
}

func u16b(v uint16) []byte { return binary.BigEndian.AppendUint16(nil, v) }
func u32b(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }

// watchLines reads a snapshot and returns the watcher's lines for it.
func watchLines(t *testing.T, m mem.Mem, cw *coopWatcher) []string {
	t.Helper()
	s, err := state.Read(m, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return cw.step(s)
}

func wantLines(t *testing.T, step string, got []string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("%s: no line %q in\n  %s", step, w, strings.Join(got, "\n  "))
		}
	}
}

// TestCoopWatchEdges drives the fake game's own setters and checks
// coop-watch labels each edge by store, table and room, notes whether the
// live memBit's saved slot has caught up, and folds a table change's bulk
// reload into one line.
func TestCoopWatchEdges(t *testing.T) {
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	if err := g.LoadStage(diagDRC); err != nil {
		t.Fatal(err)
	}
	cw := &coopWatcher{}
	if got := watchLines(t, m, cw); len(got) != 1 || !strings.Contains(got[0], `stage "M_NewD2" room 2, table 3 DRC`) {
		t.Fatalf("first step: %q", got)
	}
	if got := watchLines(t, m, cw); len(got) != 0 {
		t.Fatalf("nothing changed, yet: %q", got)
	}

	for _, err := range []error{
		g.OnSwitch(0x1A, 2), g.OnSwitch(0x85, 2), g.OnSwitch(0xC5, 2), g.OnSwitch(0xE3, 2),
		g.OnActor(7, 2), g.OnActor(300, 2),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	g.OnEventBit(0x2D01)
	g.OnEventBit(0x0040)
	got := watchLines(t, m, cw)
	wantLines(t, "game writes", got,
		"+ live t03 switch 0x1A (saved slot clear)",
		"+ dan switch 0x85",
		"+ zone 0 switch 0xC5 (room 2)",
		"+ zone 0 switch 0xE3 (room 2)",
		"+ zone 0 actor 7 (room 2)",
		"+ zone 0 actor 300 (room 2)",
		"+ event 0x2D01",
		"+ event 0x0040 (unnamed)",
	)
	if len(got) != 8 {
		t.Errorf("want exactly the 8 edges, got %q", got)
	}

	// The save menu's putSave brings the saved slot level with the live
	// copy; a later clear then shows the slot still set.
	if err := g.Save(); err != nil {
		t.Fatal(err)
	}
	wantLines(t, "save", watchLines(t, m, cw), "+ saved t03 switch 0x1A")
	if err := g.OffSwitch(0x1A, 2); err != nil {
		t.Fatal(err)
	}
	wantLines(t, "clear", watchLines(t, m, cw), "- live t03 switch 0x1A (saved slot set)")

	// A value byte, and status_b, which only --all shows.
	store(t, m, addr.LiveKeyNum, []byte{2})
	store(t, m, addr.SaveInfo+addr.OffStatusB+4, []byte{0x40})
	got = watchLines(t, m, cw)
	wantLines(t, "values", got, "~ live t03 small keys: 0x00 -> 0x02")
	if len(got) != 1 {
		t.Errorf("status_b shown without --all: %q", got)
	}
	cwAll := &coopWatcher{all: true, prev: cw.prev}
	store(t, m, addr.SaveInfo+addr.OffStatusB+4, []byte{0x41})
	wantLines(t, "--all", watchLines(t, m, cwAll), "~ status_b+0x4: 0x40 -> 0x41")

	// Leaving for the sea: putSave, then getSave and initDan for table 0.
	if err := g.LoadStage(diagSea); err != nil {
		t.Fatal(err)
	}
	got = watchLines(t, m, cw)
	wantLines(t, "stage change", got,
		`== now stage "sea" room 44, table 0 sea, file 0`,
		"~ saved t03 small keys: 0x00 -> 0x02",
		"   (table 3 -> 0: getSave and initDan reloaded the live memBit and mDan; 3 edges not shown)",
	)
	for _, l := range got {
		if strings.Contains(l, "live t") || strings.Contains(l, "dan switch") {
			t.Errorf("bulk reload edge shown: %q", l)
		}
	}
}

// diagBeat is the fake game over its FakeMem, whose heartbeat moves a
// frame, the enemies' included, whenever it is read twice in a row: what
// a FrameWriter's waitBeat needs to see the game run.
type diagBeat struct {
	*mem.FakeMem
	g    *fakegame.FakeGame
	es   *fakegame.Enemies
	last uint32
}

func (b *diagBeat) Read(a uint32, n int) ([]byte, error) {
	if a == mailbox.SpawnTrigger && b.last == mailbox.SpawnTrigger {
		if err := b.g.FrameWith(b.es); err != nil {
			return nil, err
		}
	}
	b.last = a
	return b.FakeMem.Read(a, n)
}

// unverifyPW makes the Poe unverified for one test: every K1 class passed
// the live check K1 (docs/08 §8.2), and the diagnostics' --unverified path
// is kept covered with it.
func unverifyPW(t *testing.T) {
	old := combat.SetVerified(int16(stage.ProcPW), false)
	t.Cleanup(func() { combat.SetVerified(int16(stage.ProcPW), old) })
}

// diagEnemies is a game in DRC's room 2 with a Bokoblin holding its
// weapon, a keyed ChuChu, an unkeyed one and a Poe, all executing.
func diagEnemies(t *testing.T) (*diagBeat, func()) {
	t.Helper()
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	es := fakegame.PlaceEnemies(
		fakegame.Enemy{Stage: diagDRC.Name, Room: 2, SetID: 1, Proc: stage.ProcBK, Weapon: true},
		fakegame.Enemy{Stage: diagDRC.Name, Room: 2, SetID: 2, Proc: stage.ProcCC},
		fakegame.Enemy{Stage: diagDRC.Name, Room: 2, SetID: addr.ActorSetIDNone, Proc: stage.ProcCC},
		fakegame.Enemy{Stage: diagDRC.Name, Room: 2, SetID: 4, Proc: stage.ProcPW},
	)
	if err := g.LoadStage(diagDRC); err != nil {
		t.Fatal(err)
	}
	b := &diagBeat{FakeMem: m, g: g, es: es}
	tick := func() {
		for range 3 {
			if err := g.FrameWith(es); err != nil {
				t.Fatal(err)
			}
		}
	}
	tick()
	return b, tick
}

func TestPrintActors(t *testing.T) {
	unverifyPW(t)
	b, _ := diagEnemies(t)
	w, err := combat.Read(b, 0)
	if err != nil || !w.Complete() || len(w.Actors) != 5 {
		t.Fatalf("walk %+v, %v", w, err)
	}
	var out bytes.Buffer
	printActors(&out, w)
	for _, s := range []string{
		"proc 0x00BE bk", "set 1", "enemy", "hp 4/4", "enemyice ok, mode 1, shrink timer 0", "weapon pid", "; K1 ok\n",
		"proc 0x01D5 boko", "CARRY", "proc 0x00D5 pw", "K1 ok with --unverified", "5 actors\n",
	} {
		if !strings.Contains(out.String(), s) {
			t.Errorf("printActors lacks %q:\n%s", s, out.String())
		}
	}
}

// TestEnemyKill: the diagnostic light-shrinks the actor asked for through
// the receiver's gates and guard, its weapon let go, and watches it go; it
// refuses an unverified class without --unverified, an actor no walk has,
// and a game that is not idle.
func TestEnemyKill(t *testing.T) {
	unverifyPW(t)
	b, tick := diagEnemies(t)
	ctx := context.Background()
	bk, _ := b.es.Of(0)
	wp, ok := b.es.WeaponOf(0)
	if !ok {
		t.Fatal("no weapon")
	}
	var out bytes.Buffer
	if err := enemyKill(ctx, &out, b, bk.PID, false, 10*time.Second, tick); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	for _, s := range []string{"target: actor", "wrote 1 at", "its weapon's CARRY cleared", "the actor is gone", "records setID 1 dead: true"} {
		if !strings.Contains(out.String(), s) {
			t.Errorf("the output lacks %q:\n%s", s, out.String())
		}
	}
	if st, err := mem.ReadU32(b, wp.Addr+addr.ActorOffStatus); err != nil || st&addr.ActorStatusCarry != 0 {
		t.Errorf("the weapon's status 0x%08X, %v: still carried", st, err)
	}

	// The unkeyed ChuChu: no zone needed, nothing recorded.
	cc, _ := b.es.Of(2)
	out.Reset()
	if err := enemyKill(ctx, &out, b, cc.PID, false, 10*time.Second, tick); err != nil || !strings.Contains(out.String(), "ran its course)\n") {
		t.Fatalf("the unkeyed ChuChu: %v\n%s", err, out.String())
	}

	pw, _ := b.es.Of(3)
	if err := enemyKill(ctx, &out, b, pw.PID, false, 0, tick); err == nil || !strings.Contains(err.Error(), "not verified") {
		t.Errorf("an unverified Poe: %v", err)
	}
	out.Reset()
	if err := enemyKill(ctx, &out, b, pw.PID, true, 0, tick); err != nil || !strings.Contains(out.String(), "wrote 1 at") {
		t.Errorf("a Poe with --unverified: %v\n%s", err, out.String())
	}
	if err := enemyKill(ctx, &out, b, 9999, false, 0, tick); err == nil || !strings.Contains(err.Error(), "no actor has process ID 9999") {
		t.Errorf("an unknown pid: %v", err)
	}
	b.g.SetPauseMenu(true)
	kc, _ := b.es.Of(1)
	if err := enemyKill(ctx, &out, b, kc.PID, false, 0, tick); err == nil || !strings.Contains(err.Error(), "not now") {
		t.Errorf("with the pause menu up: %v", err)
	}
}

func TestPeekDemo(t *testing.T) {
	b, tick := diagEnemies(t)
	b.g.SetLinkDemo(13)
	var out bytes.Buffer
	if err := peekDemo(context.Background(), &out, b, 0, tick); err != nil || !strings.Contains(out.String(), "mDemo.mDemoType (+0x304) = 13") {
		t.Errorf("%v: %s", err, out.String())
	}
}

// TestZoneDump checks the zone slots and the room map against the fake
// game's own zone allocation.
func TestZoneDump(t *testing.T) {
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	if err := g.LoadStage(diagDRC); err != nil {
		t.Fatal(err)
	}
	if err := g.EnterRoom(3, 2); err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{g.OnSwitch(0xC5, 2), g.OnSwitch(0xE1, 3), g.OnActor(7, 2), g.OnActor(300, 2)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	dump := func() string {
		s, err := state.Read(m, state.Options{})
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		zoneDump(&b, s)
		return b.String()
	}
	out := dump()
	for _, want := range []string{
		"zone  0  room  2  switches 0xC5  room switches -  items 0x0000  dead actors 7,300\n",
		"zone  1  room  3  switches -  room switches 0xE1  items 0x0000  dead actors -\n",
		"free and empty: zones 2,3,4,",
		"room  2 -> zone  0 (count 2)\n",
		"room  3 -> zone  1 (count 2)  <- Link\n",
		"62 rooms have no zone",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("zone-dump lacks %q:\n%s", want, out)
		}
	}

	// A zone slot that no longer holds its room is not valid for writes.
	store(t, m, addr.Zones+addr.ZoneSize+addr.ZoneOffRoomNo, []byte{5})
	if out := dump(); !strings.Contains(out, "room  3 -> zone  1 (count 2)  INVALID: zone 1 holds room 5  <- Link") {
		t.Errorf("stale zone not flagged:\n%s", out)
	}
}

// probeField returns what coop-probe printed after label.
func probeField(out, label string) string {
	for _, l := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(l, label+" "); ok && len(label) <= 11 {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

func TestCoopProbe(t *testing.T) {
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	if err := g.LoadStage(diagDRC); err != nil {
		t.Fatal(err)
	}
	probe := func() string {
		var b bytes.Buffer
		if err := coopProbe(&b, m, func() { g.Frames(3) }); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	out := probe()
	for label, want := range map[string]string{
		"stage":      `"M_NewD2" point 3 room 2 layer 2`,
		"save table": "3 DRC (mDan.mStageNo); STAG says table 3, key HUD true",
		"mReserve":   "all 0x50 bytes zero",
		"pending":    "key 0, max life 0, max magic 0",
		"heartbeat":  "spawn_trigger 12, +12 over 4 ticks",
		"STABLE":     "open",
		"IDLE":       "open",
		"HEARTBEAT":  "open",
	} {
		if got := probeField(out, label); !strings.HasPrefix(got, want) {
			t.Errorf("%s: %q, want %q\n%s", label, got, want, out)
		}
	}

	store(t, m, addr.Reserve+2, []byte{'W'})
	g.SetPauseMenu(true)
	out = probe()
	if got := probeField(out, "mReserve"); !strings.HasPrefix(got, "NOT all zero: first non-zero byte at +0x02") {
		t.Errorf("mReserve: %q", got)
	}
	if got := probeField(out, "IDLE"); got != "closed: the pause menu is open" {
		t.Errorf("IDLE: %q", got)
	}

	// No heartbeat: the probe says why HEARTBEAT is closed.
	g.SetPauseMenu(false)
	var b bytes.Buffer
	if err := coopProbe(&b, m, func() {}); err != nil {
		t.Fatal(err)
	}
	if got := probeField(b.String(), "HEARTBEAT"); !strings.HasPrefix(got, "closed: heartbeat has not moved") {
		t.Errorf("HEARTBEAT without frames: %q", got)
	}
}

// TestPhaseAccClean feeds a perfect 30 Hz heartbeat read every 50 µs.
func TestPhaseAccClean(t *testing.T) {
	const (
		frame = 33333 * time.Microsecond
		poll  = 50 * time.Microsecond
		took  = 5 * time.Microsecond
	)
	var a phaseAcc
	for at := time.Duration(0); at <= time.Second; at += poll {
		a.observe(at, uint32(at/frame), took)
	}
	r := a.report()
	if r.Changes != 30 || r.Beats != 30 || r.Skipped != 0 || r.Stalls != 0 || r.Regressed != 0 {
		t.Fatalf("report: %+v", r)
	}
	if d := r.Period - frame; d < -poll || d > poll {
		t.Errorf("period %v, want %v", r.Period, frame)
	}
	if r.Jitter > poll || r.StepMin < frame-poll || r.StepMax > frame+poll {
		t.Errorf("jitter %v, steps %v-%v", r.Jitter, r.StepMin, r.StepMax)
	}
	if r.DetectMax != poll || r.DetectMean != poll {
		t.Errorf("detection mean %v max %v, want %v", r.DetectMean, r.DetectMax, poll)
	}
	if r.ReadMin != took || r.ReadMax != took || r.ReadP50 != 8*time.Microsecond || r.ReadP99 != 8*time.Microsecond {
		t.Errorf("reads min %v max %v p50 %v p99 %v", r.ReadMin, r.ReadMax, r.ReadP50, r.ReadP99)
	}
}

// TestPhaseAccAnomalies: a gap in Go's polling skips a beat, a paused
// emulator stalls, and a save-state load sends the heartbeat backwards.
func TestPhaseAccAnomalies(t *testing.T) {
	const (
		frame = 33333 * time.Microsecond
		poll  = 50 * time.Microsecond
	)
	var a phaseAcc
	hb := func(at time.Duration) uint32 {
		switch {
		case at < 500*time.Millisecond:
			return uint32(at / frame)
		case at < 700*time.Millisecond:
			return uint32(500 * time.Millisecond / frame) // paused
		}
		return uint32((at - 200*time.Millisecond) / frame)
	}
	for at := time.Duration(0); at <= time.Second; at += poll {
		if at > 260*time.Millisecond && at < 310*time.Millisecond {
			continue // Go was descheduled for 50 ms, across a beat
		}
		a.observe(at, hb(at), time.Microsecond)
	}
	a.observe(time.Second+poll, 0, time.Microsecond) // a save state was loaded
	r := a.report()
	if r.Skipped != 1 || r.Stalls != 1 || r.Regressed != 1 {
		t.Errorf("skipped %d stalls %d regressed %d, want 1 each", r.Skipped, r.Stalls, r.Regressed)
	}
	if r.DetectMax < 50*time.Millisecond {
		t.Errorf("detection max %v misses the 50 ms gap", r.DetectMax)
	}
	if r.StepMax < 200*time.Millisecond {
		t.Errorf("step max %v misses the stall", r.StepMax)
	}
}

// TestHeapFree: heap-free's output over a fake heap (the walk itself is
// wwapi/heap's TestWalk).
func TestHeapFree(t *testing.T) {
	const (
		heap  uint32 = 0x80C00000
		start uint32 = 0x80C00100
		end   uint32 = 0x80C10100
	)
	blocks := [][2]uint32{{0, 0x100}, {0x1000, 0x2000}, {0x8000, 0x40}}
	setup := func() *mem.FakeMem {
		m := mem.NewFakeMem()
		heaptest.FakeAt(t, m, addr.ZeldaHeapPtr, heap, start, end, blocks)
		return m
	}

	var out bytes.Buffer
	m := setup()
	store(t, m, addr.JKRHeapCodeEnd, u32b(mailbox.PatchedArenaLo+0x20))
	store(t, m, addr.ZeldaHeapErrors, u32b(2))
	if err := heapFree(&out, m); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"zelda 2", "0x20 above the patched __OSArenaLo", "zelda    0x80C00000  0x80C00100-0x80C10100 (64 KiB): free 8 KiB in 3 blocks, largest 8 KiB", "[not created]"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("heap-free lacks %q:\n%s", want, out.String())
		}
	}
}

func TestCompareBlob(t *testing.T) {
	want := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	if d := compareBlob(slices.Clone(want), want, 8); d.Diff != 0 || len(d.Runs) != 0 || d.AllZero {
		t.Errorf("equal: %+v", d)
	}
	live := []byte{1, 0, 0, 4, 5, 6, 9, 8}
	if d := compareBlob(live, want, 8); d.Diff != 3 || !slices.Equal(d.Runs, []byteRun{{1, 2}, {6, 1}}) {
		t.Errorf("two runs: %+v", d)
	}
	if d := compareBlob(live, want, 1); len(d.Runs) != 1 || d.More != 1 {
		t.Errorf("capped runs: %+v", d)
	}
	if d := compareBlob(make([]byte, 8), want, 8); !d.AllZero || d.Diff != 8 {
		t.Errorf("all zero: %+v", d)
	}

	// The live check against the real blob, loaded and not.
	m := mem.NewFakeMem()
	var out bytes.Buffer
	if err := blobVerifyLive(&out, m); err != nil || !strings.Contains(out.String(), "all zero") {
		t.Errorf("not loaded: %q %v", out.String(), err)
	}
	store(t, m, inject.T2Address, inject.T2Bytes)
	out.Reset()
	if err := blobVerifyLive(&out, m); err != nil || !strings.HasSuffix(out.String(), "MATCH\n") {
		t.Errorf("loaded: %q %v", out.String(), err)
	}
	store(t, m, inject.T2Address+0x20, []byte{0xAA, 0xBB})
	out.Reset()
	blobVerifyLive(&out, m)
	if !strings.Contains(out.String(), "0x2 bytes differ in 1 runs; the first at +0x0020 (0x80410020)") {
		t.Errorf("mismatch: %q", out.String())
	}
}

// TestCanaryRegions pins the canary ranges to §4.5 and checks the guard
// refuses anything touching the main stack, T2, the mailbox or the arena.
func TestCanaryRegions(t *testing.T) {
	if mailbox.CanaryTRK.Start != 0x8040CFB0 || mailbox.CanaryTRK.End != 0x80410000 ||
		mailbox.CanaryArena.Start != 0x804130B0 || mailbox.CanaryArena.End != 0x804130BC ||
		mailbox.CanaryArenaHigh.Start != 0x804138E4 || mailbox.CanaryArenaHigh.End != 0x80413EB0 ||
		mailbox.EyePacketsRegion.End != 0x80414000 {
		t.Fatalf("canary ranges moved: %+v %+v %+v", mailbox.CanaryTRK, mailbox.CanaryArena, mailbox.CanaryArenaHigh)
	}
	for _, r := range canaryRegions {
		if err := canarySafe(r); err != nil {
			t.Errorf("%s: %v", r.Name, err)
		}
	}
	for _, r := range []addr.Region{
		{Name: "into T2", Start: 0x8040FF00, End: 0x80410004},
		{Name: "slack after T2", Start: 0x80412E00, End: 0x80412E10},
		{Name: "mailbox", Start: mailbox.Mailbox, End: mailbox.Mailbox + 4},
		{Name: "mailbox tail", Start: 0x8041309C, End: 0x80413100},
		{Name: "main stack", Start: 0x8040CFA4, End: 0x8040D000},
		{Name: "stack header", Start: 0x8040CFA8, End: 0x8040D000},
		{Name: "boot LR slot", Start: 0x8040CFAC, End: 0x8040CFB0},
		{Name: "arena", Start: 0x80413F00, End: 0x80414004},
		{Name: "enemy block", Start: 0x80413100, End: 0x80413104},
		{Name: "enemy block tail", Start: 0x80413294, End: 0x80413298},
		{Name: "enemy sphere image", Start: 0x804132A0, End: 0x804132A4},
		{Name: "enemy stts tail", Start: 0x8041340C, End: 0x80413410},
		{Name: "REL exec hook word", Start: 0x804130BC, End: 0x804130C0},
		{Name: "REL swap state word", Start: 0x804130C0, End: 0x804130C4},
		{Name: "remote-Link block", Start: 0x80413420, End: 0x80413424},
		{Name: "remote-Link block tail", Start: 0x8041374C, End: 0x80413750},
		{Name: "enemy knob table", Start: 0x80413750, End: 0x80413754},
		{Name: "enemy knob table tail", Start: 0x804137AC, End: 0x804137B0},
		{Name: "actor_refresh request", Start: 0x804137B0, End: 0x804137B4},
		{Name: "actor_refresh request tail", Start: 0x804137CC, End: 0x804137D0},
		{Name: "across the enemy block", Start: 0x804130B0, End: 0x80414000},
		{Name: "misaligned", Start: 0x8040CFAA, End: 0x8040D000},
		{Name: "empty", Start: 0x8040D000, End: 0x8040D000},
		{Name: "outside MEM1", Start: 0x7FFFFF00, End: 0x80000000},
	} {
		if err := canarySafe(r); err == nil {
			t.Errorf("%s 0x%08X-0x%08X passed the guard", r.Name, r.Start, r.End)
		}
	}
}

func TestArmAndCheckCanaries(t *testing.T) {
	m := mem.NewFakeMem()
	m.Record(true)
	for _, r := range canaryRegions {
		if err := armCanary(m, r, false); err != nil {
			t.Fatal(err)
		}
	}
	// Every write lies inside a canary region, and they cover both.
	covered := 0
	for _, w := range m.Writes() {
		in := false
		for _, r := range canaryRegions {
			in = in || r.Contains(w.Addr, len(w.Data))
		}
		if !in || len(w.Data) > canaryChunk {
			t.Errorf("write 0x%08X+0x%X is outside the canary regions or too big", w.Addr, len(w.Data))
		}
		covered += len(w.Data)
	}
	if want := int(mailbox.CanaryTRK.Size() + mailbox.CanaryArena.Size() + mailbox.CanaryArenaHigh.Size()); covered != want {
		t.Errorf("wrote 0x%X bytes, want 0x%X", covered, want)
	}
	for _, r := range canaryRegions {
		c, err := checkCanary(m, r)
		if err != nil || c.Intact != c.Words || c.Words != int(r.Size()/4) {
			t.Errorf("%s fresh: %+v %v", r.Name, c, err)
		}
	}

	// OSClearStack-style zeroing and one stray store.
	r := mailbox.CanaryTRK
	store(t, m, r.Start+0x100, make([]byte, 12))
	store(t, m, r.Start+0x400, u32b(0x80001234))
	c, _ := checkCanary(m, r)
	if c.Words-c.Intact != 4 || c.Zeroed != 3 ||
		!slices.Equal(c.Runs, []wordRun{{r.Start + 0x100, r.Start + 0x10C}, {r.Start + 0x400, r.Start + 0x404}}) {
		t.Errorf("after damage: %+v", c)
	}
	var out bytes.Buffer
	if err := regionCheck(&out, m); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"4 changed (3 now zero, 1 other)", "0x8040D0B0-0x8040D0BC (3 words)",
		"below arena  0x804130B0-0x804130BC (3 words): all intact",
		"below arena (past the visuals block) 0x804138E4-0x80413EB0 (371 words): all intact"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("region-probe check lacks %q:\n%s", want, out.String())
		}
	}

	// Data that is neither zero nor a canary means the memory is in use:
	// arming refuses unless forced. Re-arming over canaries is fine.
	m.ResetWrites()
	if err := armCanary(m, r, false); err == nil || !strings.Contains(err.Error(), "already hold data (first 0x8040D3B0 = 0x80001234)") {
		t.Errorf("armed over data: %v", err)
	}
	if len(m.Writes()) != 0 {
		t.Errorf("a refused arm wrote %d times", len(m.Writes()))
	}
	if err := armCanary(m, r, true); err != nil {
		t.Errorf("--force: %v", err)
	}
	if err := armCanary(m, mailbox.CanaryArena, false); err != nil {
		t.Errorf("re-arm over canaries: %v", err)
	}

	// Never armed.
	out.Reset()
	regionCheck(&out, mem.NewFakeMem())
	if !strings.Contains(out.String(), "no canaries (3092 words zero)") {
		t.Errorf("unarmed check: %s", out.String())
	}
}

// TestCanaryBesideBootLR: every booted game holds __init_hardware's
// return at 4(_stack_addr). Arming must succeed beside it, unforced, and
// leave it alone.
func TestCanaryBesideBootLR(t *testing.T) {
	m := mem.NewFakeMem()
	store(t, m, mailbox.BootLRSlot, u32b(mailbox.BootLR))
	m.Record(true)
	for _, r := range canaryRegions {
		if err := armCanary(m, r, false); err != nil {
			t.Error(err)
		}
	}
	for _, w := range m.Writes() {
		if w.Addr < mailbox.MainStackHeader.End && mailbox.MainStackHeader.Start < w.Addr+uint32(len(w.Data)) {
			t.Errorf("write 0x%08X+0x%X touches the stack header", w.Addr, len(w.Data))
		}
	}
	if v, err := mem.ReadU32(m, mailbox.BootLRSlot); err != nil || v != mailbox.BootLR {
		t.Errorf("boot LR now 0x%08X %v", v, err)
	}
	var out bytes.Buffer
	if err := regionCheck(&out, m); err != nil || strings.Count(out.String(), "all intact") != len(canaryRegions) {
		t.Errorf("region-probe check: %v\n%s", err, out.String())
	}
}

// TestCanaryPreflight: arming needs the patched layout running.
func TestCanaryPreflight(t *testing.T) {
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	tick := func() { g.Frames(1) }
	if err := canaryPreflight(m, tick); err == nil || !strings.Contains(err.Error(), "mCodeEnd") {
		t.Errorf("vanilla arena: %v", err)
	}
	store(t, m, addr.JKRHeapCodeEnd, u32b(0x8040EFE0)) // the vanilla __ArenaLo plus OSInitAlloc's table
	if err := canaryPreflight(m, tick); err == nil {
		t.Error("armed with the vanilla arena")
	}
	store(t, m, addr.JKRHeapCodeEnd, u32b(mailbox.PatchedArenaLo+0x20))
	if err := canaryPreflight(m, func() {}); err == nil || !strings.Contains(err.Error(), "heartbeat") {
		t.Errorf("paused: %v", err)
	}
	if err := canaryPreflight(m, tick); err != nil {
		t.Errorf("patched and running: %v", err)
	}
	var out bytes.Buffer
	if err := regionArm(&out, m, tick, false); err != nil || strings.Count(out.String(), "armed: ") != len(canaryRegions) {
		t.Errorf("regionArm: %v\n%s", err, out.String())
	}
}

func TestParseStageHop(t *testing.T) {
	good := []struct {
		args []string
		want stageHop
	}{
		{[]string{"M_NewD2"}, stageHop{"M_NewD2", 0, 0, -1}},
		{[]string{"sea", "3", "44"}, stageHop{"sea", 3, 44, -1}},
		{[]string{"M_NewD2", "3", "2", "2"}, stageHop{"M_NewD2", 3, 2, 2}},
		{[]string{"A_umikz", "0", "0", "11"}, stageHop{"A_umikz", 0, 0, 11}},
	}
	for _, c := range good {
		if got, err := parseStageHop(c.args); err != nil || got != c.want {
			t.Errorf("%q: %+v, %v", c.args, got, err)
		}
	}
	for _, args := range [][]string{
		nil,
		{"ABCDEFGH"},            // mName is char[8]
		{"a-b"},                 // not a stage name
		{"sea", "-1"},           // restart points
		{"sea", "256"},          // a PLYR point is a u8
		{"sea", "0", "64"},      // rooms 0..63
		{"sea", "0", "0", "12"}, // l_layerFuncTable_p has 12 entries
		{"sea", "0", "0", "-2"}, // -1 is the only negative layer
		{"sea", "x"},            // not a number
		{"sea", "0", "0", "0", "0"},
	} {
		if _, err := parseStageHop(args); err == nil {
			t.Errorf("%q parsed", args)
		}
	}
}

// TestStageHop sends a hop through the fake game: the bytes land where
// ChangeStage reads them, mEnable is written last, nothing else is
// written, and closed gates refuse.
func TestStageHop(t *testing.T) {
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	for _, s := range []fakegame.Stage{diagDRC, diagSea} { // the fake learns both STAGs
		if err := g.LoadStage(s); err != nil {
			t.Fatal(err)
		}
	}
	tick := func() { g.Frames(3) }
	_, st, _, err := diag.GateSnapshots(m, tick)
	if err != nil {
		t.Fatal(err)
	}
	m.Record(true)
	h := stageHop{Name: "M_NewD2", Point: 3, Room: 2, Layer: 2}
	if err := applyStageHop(m, h, st); err != nil {
		t.Fatal(err)
	}
	ws := m.Writes()
	next := addr.Region{Name: "mNextStage", Start: addr.NextStage, End: addr.NextStage + addr.NextStageSize}
	for _, w := range ws {
		if !next.Contains(w.Addr, len(w.Data)) {
			t.Errorf("write 0x%08X+%d is outside mNextStage", w.Addr, len(w.Data))
		}
	}
	if last := ws[len(ws)-1]; last.Addr != addr.NextStageEnable || !bytes.Equal(last.Data, []byte{1}) {
		t.Errorf("last write %+v, want mEnable = 1", last)
	}
	if err := g.ChangeStage(); err != nil {
		t.Fatal(err)
	}
	s, err := state.Read(m, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if s.StageName != "M_NewD2" || s.StagePoint != 3 || s.StageRoom != 2 || s.StageLayer != 2 || s.Table != addr.TableDRC {
		t.Errorf("after the hop: %q point %d room %d layer %d table %d", s.StageName, s.StagePoint, s.StageRoom, s.StageLayer, s.Table)
	}

	// Closed gates and a pending change refuse, writing nothing.
	m.ResetWrites()
	g.SetEvent(1, 0)
	_, st, _, _ = diag.GateSnapshots(m, tick)
	if err := applyStageHop(m, h, st); err == nil || !strings.Contains(err.Error(), "an event is running") {
		t.Errorf("during an event: %v", err)
	}
	g.SetEvent(0, 0)
	_, st, _, _ = diag.GateSnapshots(m, tick)
	if err := g.SetNextStage(diagSea); err != nil {
		t.Fatal(err)
	}
	if err := applyStageHop(m, h, st); err == nil || !strings.Contains(err.Error(), "asked for a stage change itself") {
		t.Errorf("with a change pending: %v", err)
	}
	if len(m.Writes()) != 0 {
		t.Errorf("refused hops wrote: %+v", m.Writes())
	}
	if st := (gate.Status{}); applyStageHop(m, h, st) == nil {
		t.Error("closed gates allowed a hop")
	}
}

func TestSplitDiagFlags(t *testing.T) {
	pos, flags, err := diag.SplitFlags([]string{"arm", "--yes"}, "yes", "force")
	if err != nil || !slices.Equal(pos, []string{"arm"}) || !flags["yes"] || flags["force"] {
		t.Errorf("%q %v %v", pos, flags, err)
	}
	if _, _, err := diag.SplitFlags([]string{"--yes"}); err == nil {
		t.Error("--yes accepted where no flag is")
	}
	for cmd := range coopDiagFlags {
		if _, ok := coopDiagUsage[cmd]; !ok {
			t.Errorf("%s has flags but no usage", cmd)
		}
	}
	for cmd, u := range coopDiagUsage {
		if !strings.HasPrefix(u, cmd) {
			t.Errorf("usage %q does not start with %s", u, cmd)
		}
	}
}

// TestCoopDiagDispatched checks that wwdev's command registry sends every
// coopDiagUsage name, and only those, to runCoopDiag, and that its lookup
// finds each of them. enemy-kill and peek-demo were once left out of
// ww-multiplayer's switch and printed "Unknown command".
func TestCoopDiagDispatched(t *testing.T) {
	diag := reflect.ValueOf(runCoopDiag).Pointer()
	var got []string
	for _, c := range commandTable() {
		if c.run != nil && reflect.ValueOf(c.run).Pointer() == diag {
			got = append(got, c.name)
		}
	}
	slices.Sort(got)
	want := slices.Sorted(maps.Keys(coopDiagUsage))
	if !slices.Equal(got, want) {
		t.Errorf("the registry sends %q to runCoopDiag, coopDiagUsage has %q", got, want)
	}
	for _, name := range want {
		c, ok := lookupCommand(name)
		if !ok {
			t.Errorf("%s: not a command", name)
			continue
		}
		if reflect.ValueOf(c.run).Pointer() != diag {
			t.Errorf("%s dispatches to %s, not runCoopDiag", name, c.name)
		}
	}
}

// The escapes' bytes are the ones package main's own tools write: the
// mailbox's warp fields (player.Warp) and stage-hop's mNextStage request.
func TestCoopEscapesMatchMain(t *testing.T) {
	if mailbox.WarpSeq != mailbox.Mailbox+mailbox.OffWarpSeq || replica.WarpAck != mailbox.Mailbox+mailbox.OffWarpAck ||
		replica.WarpPos != mailbox.Mailbox+mailbox.OffWarpX || mailbox.OffWarpY != mailbox.OffWarpX+4 || mailbox.OffWarpZ != mailbox.OffWarpX+8 {
		t.Errorf("the replica's warp fields (seq 0x%08X, ack 0x%08X, x/y/z 0x%08X) are not the mailbox's", mailbox.WarpSeq, replica.WarpAck, replica.WarpPos)
	}
	if replica.MaxStageLayer != stageHopLayerMax {
		t.Errorf("layer bound %d, stage-hop's %d", replica.MaxStageLayer, stageHopLayerMax)
	}
	for _, h := range []stageHop{
		{Name: "sea", Point: 0, Room: 44, Layer: -1},
		{Name: "M_NewD2", Point: 3, Room: 5, Layer: 0},
		{Name: "kindan", Point: 255, Room: 63, Layer: 11},
	} {
		r := replica.StageRequest{Name: h.Name, Point: h.Point, Room: h.Room, Layer: h.Layer}
		if err := r.Check(); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
		if r.Bytes() != h.bytes() {
			t.Errorf("%+v: % X, stage-hop's % X", r, r.Bytes(), h.bytes())
		}
		fs := r.Fields()
		if len(fs) != 3 || fs[0].Addr != addr.NextStage || fs[1].Addr != addr.NextStage+addr.NextOffWipe || fs[1].Data[0] != stageHopWipe ||
			fs[2].Addr != addr.NextStageEnable || fs[2].Data[0] != 1 {
			t.Errorf("%+v: fields %+v, want stage-hop's order: the request, the wipe, then mEnable", r, fs)
		}
	}
}
