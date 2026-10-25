package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/feed"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// smokeGame is a fake game's liveness words, written into a FakeMem.
type smokeGame struct {
	m *mem.FakeMem
}

func newSmokeGame(t *testing.T) *smokeGame {
	t.Helper()
	g := &smokeGame{m: mem.NewFakeMem()}
	if err := mailbox.WriteREL(g.m, mailbox.REL{Linked: true, BuildID: inject.RELBuildID}); err != nil {
		t.Fatal(err)
	}
	g.u8(mailbox.Mailbox+mailbox.OffShadowMode, avatar.ModePoseFeed)
	g.u32(mailbox.VisAddr+mailbox.VisMagicOff, mailbox.VisMagic)
	return g
}

func (g *smokeGame) u32(a, v uint32) {
	b := make([]byte, 4)
	mem.PutU32BE(b, v)
	_ = g.m.Store(a, b)
}

func (g *smokeGame) u8(a uint32, v uint8) { _ = g.m.Store(a, []byte{v}) }

func (g *smokeGame) vec(a uint32, v vec) {
	b := make([]byte, 12)
	for i, f := range v {
		mem.PutF32BE(b[4*i:], f)
	}
	_ = g.m.Store(a, b)
}

// tick is one sample's worth of a running game: frames frames, the REL
// frame counter, the pose published, slot 0 fed (NextSeq: 0 skipped).
func (g *smokeGame) tick(t *testing.T, frames uint32, feed bool) gameSample {
	t.Helper()
	hb, _ := mem.ReadU32(g.m, mailbox.SpawnTrigger)
	g.u32(mailbox.SpawnTrigger, hb+frames)
	rel, _ := mailbox.ReadREL(g.m)
	rel.Frames += frames
	_ = mailbox.WriteREL(g.m, rel)
	pub, _ := mem.ReadU8(g.m, mailbox.Mailbox+mailbox.OffPosePublishSeq)
	g.u8(mailbox.Mailbox+mailbox.OffPosePublishSeq, pub+1)
	if feed {
		s, _ := mem.ReadU8(g.m, mailbox.Mailbox+avatar.OffPoseSeq(0))
		g.u8(mailbox.Mailbox+avatar.OffPoseSeq(0), avatar.NextSeq(s))
	}
	return readGameSample(g.m)
}

func checkNamed(t *testing.T, cs []smokeCheck, name string) smokeCheck {
	t.Helper()
	for _, c := range cs {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check %q in %+v", name, cs)
	return smokeCheck{}
}

var testLimits = smokeLimits{MinFPS: 10, SeqChanges: 5}

func TestSmokeLivenessPass(t *testing.T) {
	g := newSmokeGame(t)
	// Start near the u8 wrap so the seqs roll over 255 -> 1 mid-run.
	g.u8(mailbox.Mailbox+avatar.OffPoseSeq(0), 250)
	var ss []gameSample
	for range 30 {
		ss = append(ss, g.tick(t, 3, true)) // 30 frames a second at 100 ms
	}
	cs := append(livenessChecks("A", ss, 3, testLimits), poseFlowChecks("A", ss, testLimits)...)
	for _, c := range cs {
		if !c.Pass {
			t.Errorf("%s failed: %s", c.Name, c.Detail)
		}
	}
	if c := checkNamed(t, cs, "heartbeat A"); c.Values["fps"] != 29.0 {
		t.Errorf("fps %v, want 29 (87 frames over 3 s)", c.Values["fps"])
	}
}

func TestSmokeLivenessFailures(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(g *smokeGame)
		frame uint32
		feed  bool
		fails []string
	}{
		{name: "stalled heartbeat", frame: 0, feed: true, fails: []string{"heartbeat A", "rel A"}},
		{name: "slow game", frame: 1, feed: true, fails: []string{"heartbeat A"}},
		{name: "no feed", frame: 3, fails: []string{"pose feed A"}},
		{name: "REL of another build", frame: 3, feed: true, setup: func(g *smokeGame) {
			_ = mailbox.WriteREL(g.m, mailbox.REL{Linked: true, BuildID: inject.RELBuildID ^ 1})
		}, fails: []string{"rel A"}},
		{name: "REL unlinked", frame: 3, feed: true, setup: func(g *smokeGame) {
			_ = mailbox.WriteREL(g.m, mailbox.REL{})
		}, fails: []string{"rel A"}},
		{name: "visuals error", frame: 3, feed: true, setup: func(g *smokeGame) {
			g.u32(mailbox.VisAddr+mailbox.VisStatErrOff, mailbox.VisErrCreate)
		}, fails: []string{"rel last error A"}},
		{name: "shadow mode off", frame: 3, feed: true, setup: func(g *smokeGame) {
			g.u8(mailbox.Mailbox+mailbox.OffShadowMode, avatar.ModeOff)
		}, fails: []string{"pose feed A"}},
		{name: "crash", frame: 3, feed: true, setup: func(g *smokeGame) {
			ctx := uint32(0x80500000)
			g.u32(addr.ExCallbackObject+addr.ExCallbackOffContext, ctx)
		}, fails: []string{"no crash A"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := newSmokeGame(t)
			if c.setup != nil {
				c.setup(g)
			}
			var ss []gameSample
			for range 30 {
				ss = append(ss, g.tick(t, c.frame, c.feed))
			}
			cs := append(livenessChecks("A", ss, 3, testLimits), poseFlowChecks("A", ss, testLimits)...)
			for _, k := range cs {
				want := strings.Contains(strings.Join(c.fails, "|"), k.Name)
				if k.Pass == want {
					t.Errorf("%s: pass %v (%s), want fail %v", k.Name, k.Pass, k.Detail, want)
				}
			}
		})
	}
	if cs := livenessChecks("A", nil, 3, testLimits); len(cs) != 1 || cs[0].Pass {
		t.Errorf("no samples: %+v", cs)
	}
}

func TestSmokeTracking(t *testing.T) {
	// The observer's puppet slot 2 holds the mover, slot 0 a stale
	// position far off; mini-Link slot 1 is armed with its root.
	obs := newSmokeGame(t)
	obs.vec(feed.SlotAddr(0, mailbox.PuppetOffPosX), vec{9000, 0, 9000})
	obs.vec(feed.SlotAddr(2, mailbox.PuppetOffPosX), vec{310, 0, 12})
	const buf = 0x80600000
	obs.u32(mailbox.Mailbox+avatar.OffPoseBufPtr(1), buf)
	obs.u8(mailbox.Mailbox+avatar.OffPoseSeq(1), 7)
	mtx := make([]byte, avatar.MtxBytes)
	mem.PutF32BE(mtx[12:], 312)
	mem.PutF32BE(mtx[28:], 40)
	mem.PutF32BE(mtx[44:], 10)
	_ = obs.m.Store(buf, mtx)

	end := vec{300, 0, 10}
	slot, p, ok := remoteSlotPos(obs.m, end)
	if !ok || slot != 2 || p != (vec{310, 0, 12}) {
		t.Fatalf("remoteSlotPos: %d %v %v", slot, p, ok)
	}
	ps, root, ok := remotePoseRoot(obs.m, vec{300, 40, 10})
	if !ok || ps != 1 || root != (vec{312, 40, 10}) {
		t.Fatalf("remotePoseRoot: %d %v %v", ps, root, ok)
	}
	o := trackObs{MoverStart: vec{0, 0, 0}, MoverEnd: end, MoverRoot: vec{300, 40, 10}, HaveMoverRoot: true,
		Slot: slot, SlotPos: p, HaveSlot: true, PoseSlot: ps, PoseRoot: root, HavePose: true}
	if c := trackCheck("track", o, 100, 50); !c.Pass {
		t.Errorf("tracking within tolerance failed: %s", c.Detail)
	}
	if c := trackCheck("track", o, 100, 5); c.Pass {
		t.Errorf("tolerance 5 passed: %s", c.Detail)
	}
	stuck := o
	stuck.MoverEnd = vec{20, 0, 0}
	stuck.SlotPos = vec{20, 0, 0}
	if c := trackCheck("track", stuck, 100, 50); c.Pass || !strings.Contains(c.Detail, "proves nothing") {
		t.Errorf("a Link that did not move passed: %s", c.Detail)
	}
	noPose := o
	noPose.HavePose = false
	if c := trackCheck("track", noPose, 100, 50); c.Pass {
		t.Errorf("no mini-Link root passed: %s", c.Detail)
	}
	noSlot := o
	noSlot.HaveSlot = false
	if c := trackCheck("track", noSlot, 100, 50); c.Pass {
		t.Errorf("no slot passed: %s", c.Detail)
	}
	// A pose_buf pointer outside MEM1 is not followed.
	obs.u32(mailbox.Mailbox+avatar.OffPoseBufPtr(1), 0x90000000)
	if _, _, ok := remotePoseRoot(obs.m, end); ok {
		t.Error("followed a pose_buf outside MEM1")
	}
}

func TestSmokeBindChecks(t *testing.T) {
	world := bind.WorldID{1, 2, 3, 4, 5, 6, 7, 8}
	file := func(slot uint8, w bind.WorldID) bind.File {
		f := bind.File{Slot: slot, ClearCount: 0, DateIPL: 1}
		copy(f.Name[:], "Link")
		tag := bind.Tag{Layout: bind.LayoutV1, Slot: slot, World: w, Nonce: bind.Nonce{9}, NameHash: bind.NameHash(f.Name[:])}
		b := tag.Encode()
		copy(f.Tag[:], b[:])
		return f
	}
	if cs := bindChecks(file(0, world), file(1, world)); !allPass(cs) {
		t.Errorf("both bound to one world: %+v", cs)
	}
	if cs := bindChecks(file(0, world), file(1, bind.WorldID{8})); allPass(cs) || checkNamed(t, cs, "coop same world").Pass {
		t.Errorf("two worlds passed: %+v", cs)
	}
	if cs := bindChecks(file(0, world), bind.File{}); checkNamed(t, cs, "coop bind B").Pass {
		t.Errorf("an unbound file passed: %+v", cs)
	}
}

func TestParseSmokeArgs(t *testing.T) {
	o, err := parseSmokeArgs(nil)
	if err != nil || o.nameA != "PlayerA" || o.hop != "sea 0 44" || o.coopSwitch != -1 || o.coop() || o.walk != 1500*time.Millisecond {
		t.Fatalf("defaults: %+v, %v", o, err)
	}
	o, err = parseSmokeArgs([]string{"--hop", "none", "--switch", "5", "--smooth", "20:40:20", "--", "--coop=new", "--coop-live-switches"})
	if err != nil || o.hop != "" || o.coopSwitch != 5 || !o.coop() || len(o.mpArgs) != 2 || o.smooth != "20:40:20" {
		t.Fatalf("got %+v, %v", o, err)
	}
	for _, bad := range [][]string{
		{"--switch", "5"},                   // no co-op
		{"--switch", "200", "--", "--coop"}, // not a memory switch
		{"--names", "A"},                    // one name
		{"--names", "A,A"},                  // the same twice
		{"--smooth", "0"},                   // no rate
		{"--walk-ms", "50"},                 // too short
		{"stray"},                           // a positional
		{"--tol", "0"},                      // no tolerance
	} {
		if _, err := parseSmokeArgs(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

func TestSmokePIDsFrom(t *testing.T) {
	none := func(string) string { return "" }
	if _, _, err := smokePIDsFrom([]uint32{5}, none, nil, nil); err == nil {
		t.Error("one Dolphin: no error")
	}
	if a, b, err := smokePIDsFrom([]uint32{5, 7}, none, nil, nil); err != nil || a != 5 || b != 7 {
		t.Errorf("two: %d %d %v", a, b, err)
	}
	if _, _, err := smokePIDsFrom([]uint32{5, 7, 9}, none, nil, nil); err == nil {
		t.Error("three without a pin: no error")
	}
	pin := func(k string) string {
		if k == "MP_LOCAL_PIDS" {
			return "9,5"
		}
		return ""
	}
	if a, b, err := smokePIDsFrom([]uint32{5, 7, 9}, pin, nil, nil); err != nil || a != 9 || b != 5 {
		t.Errorf("pinned: %d %d %v", a, b, err)
	}
	recs := []launchRecord{{Label: "Dolphin 2", PID: 5, Start: "s5"}, {Label: "Dolphin 1", PID: 7, Start: "s7"}}
	start := func(pid uint32) string { return map[uint32]string{5: "s5", 7: "s7"}[pid] }
	if a, b, err := smokePIDsFrom([]uint32{5, 7}, none, recs, start); err != nil || a != 7 || b != 5 {
		t.Errorf("by record: %d %d %v", a, b, err)
	}
	// A record whose PID another process now has does not count.
	reused := func(pid uint32) string { return "other" }
	if a, b, _ := smokePIDsFrom([]uint32{5, 7}, none, recs, reused); a != 5 || b != 7 {
		t.Errorf("re-used PIDs: %d %d", a, b)
	}
}

func TestAttachRefusal(t *testing.T) {
	players := []string{`C:\Users\p\AppData\Roaming\Dolphin Emulator`}
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	recs := []launchRecord{{PID: 1, UserDir: `D:\fixtures\user-4`}, {PID: 2, UserDir: `D:\fixtures\user-3`}}
	if why := attachRefusal([2]uint32{1, 2}, recs, getenv, players, nil); why != "" {
		t.Errorf("recorded test dirs refused: %s", why)
	}
	recs[1].UserDir = `c:/users/P/AppData/Roaming/Dolphin Emulator/`
	if why := attachRefusal([2]uint32{1, 2}, recs, getenv, players, nil); !strings.Contains(why, "player's Dolphin profile") {
		t.Errorf("a recorded player profile: %q", why)
	}
	if why := attachRefusal([2]uint32{3, 4}, nil, getenv, players, nil); !strings.Contains(why, "USER_DIR_1 is not set") {
		t.Errorf("unrecorded, nothing declared: %q", why)
	}
	env["USER_DIR_1"], env["USER_DIR_2"] = `D:\t1`, players[0]
	if why := attachRefusal([2]uint32{3, 4}, nil, getenv, players, nil); !strings.Contains(why, "Dolphin B") {
		t.Errorf("unrecorded B declared as a player profile: %q", why)
	}
	env["USER_DIR_2"] = `D:\t2`
	if why := attachRefusal([2]uint32{3, 4}, nil, getenv, players, nil); why != "" {
		t.Errorf("declared test dirs refused: %s", why)
	}
}

func TestWatchStopFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stop")
	stopped := make(chan struct{})
	go watchStopFile(context.Background(), log.Discard{}, path, time.Millisecond, func() { close(stopped) })
	select {
	case <-stopped:
		t.Fatal("stopped before the file was there")
	case <-time.After(20 * time.Millisecond):
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("not stopped after the file appeared")
	}
	// No path: returns at once without calling stop.
	watchStopFile(context.Background(), log.Discard{}, "", time.Millisecond, func() { t.Error("stop called for no path") })
}

// TestSmokeReportExit: the outcome and exit code finish gives each kind
// of run, and smoke.json's shape.
func TestSmokeReportExit(t *testing.T) {
	root := t.TempDir()
	t.Setenv(envDevLogs, root)
	for _, c := range []struct {
		name    string
		checks  []smokeCheck
		cannot  string
		code    int
		outcome string
	}{
		{"pass", []smokeCheck{{Name: "x", Pass: true}, {Name: "y", Skipped: true}}, "", smokeExitPass, "pass"},
		{"fail", []smokeCheck{{Name: "x", Pass: true}, {Name: "y"}}, "", smokeExitFail, "fail"},
		{"cannot", nil, "no Dolphin is running", smokeExitCannot, "cannot run: no Dolphin is running"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := &smokeRun{ctx: context.Background(), rep: log.Discard{}, logBuf: &strings.Builder{}, cannot: c.cannot}
			r.report.Checks = c.checks
			if got := r.finish(); got != c.code {
				t.Errorf("exit %d, want %d", got, c.code)
			}
			b, err := os.ReadFile(filepath.Join(root, "latest", "smoke.json"))
			if err != nil {
				t.Fatal(err)
			}
			var rep smokeReport
			if err := json.Unmarshal(b, &rep); err != nil {
				t.Fatal(err)
			}
			if rep.Outcome != c.outcome || rep.ExitCode != c.code {
				t.Errorf("report says %q exit %d", rep.Outcome, rep.ExitCode)
			}
			if c.name == "fail" && (len(rep.Failed) != 1 || rep.Failed[0] != "y") {
				t.Errorf("failed list %v", rep.Failed)
			}
		})
	}
}
