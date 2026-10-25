package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/feed"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// TestAttachRefusalNeedsDevLogProof: a dolphins.json record vouches for
// a Dolphin only as a WW_DOLPHIN_DEVLOG launch of the same process (its
// start token matches); a plain dolphin2 launch on dolphin2's second
// default (a player's bootstrapped profile) is never driven.
func TestAttachRefusalNeedsDevLogProof(t *testing.T) {
	const cfg = `C:\Users\p\AppData\Roaming`
	guard := smokeGuardDirs(launch.Env{GOOS: "windows", ConfigDir: cfg}, "")
	second := filepath.Join(cfg, "Dolphin Emulator 2")
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	start := func(pid uint32) string { return map[uint32]string{1: "t1", 2: "t2"}[pid] }
	test := func(pid int, dir string, devLog bool, tok string) launchRecord {
		return launchRecord{PID: pid, UserDir: dir, DevLog: devLog, Start: tok}
	}
	for _, c := range []struct {
		name string
		recs []launchRecord
		env  map[string]string
		want string // "" passes, else a part of the refusal
	}{
		{"devlog records of these processes", []launchRecord{test(1, `D:\t1`, true, "t1"), test(2, `D:\t2`, true, "t2")}, nil, ""},
		{"plain dolphin2 on the second default", []launchRecord{test(1, `D:\t1`, true, "t1"), test(2, second, false, "t2")}, nil, "not as a WW_DOLPHIN_DEVLOG launch"},
		{"plain record, second default declared", []launchRecord{test(1, `D:\t1`, true, "t1"), test(2, second, false, "t2")}, map[string]string{"USER_DIR_2": second}, "player's Dolphin profile"},
		{"plain record, test dir declared", []launchRecord{test(1, `D:\t1`, true, "t1"), test(2, `D:\t2`, false, "t2")}, map[string]string{"USER_DIR_2": `d:/T2/`}, ""},
		{"plain record, another dir declared", []launchRecord{test(1, `D:\t1`, true, "t1"), test(2, second, false, "t2")}, map[string]string{"USER_DIR_2": `D:\t2`}, "not on USER_DIR_2"},
		{"devlog record without a start token", []launchRecord{test(1, `D:\t1`, true, ""), test(2, `D:\t2`, true, "t2")}, nil, "Dolphin A"},
		{"devlog record of an older process", []launchRecord{test(1, `D:\t1`, true, "old"), test(2, `D:\t2`, true, "t2")}, nil, "Dolphin A (pid 1) was not started by dolphin2"},
		{"devlog record on the second default", []launchRecord{test(1, `D:\t1`, true, "t1"), test(2, second, true, "t2")}, nil, "player's Dolphin profile"},
	} {
		t.Run(c.name, func(t *testing.T) {
			env = c.env
			if env == nil {
				env = map[string]string{}
			}
			why := attachRefusal([2]uint32{1, 2}, c.recs, getenv, guard, start)
			switch {
			case c.want == "" && why != "":
				t.Errorf("refused: %s", why)
			case c.want != "" && !strings.Contains(why, c.want):
				t.Errorf("got %q, want it to say %q", why, c.want)
			}
		})
	}
	// A token that can't be read now proves nothing either.
	recs := []launchRecord{test(1, `D:\t1`, true, "t1"), test(2, `D:\t2`, true, "t2")}
	if why := attachRefusal([2]uint32{1, 2}, recs, func(string) string { return "" }, guard, func(uint32) string { return "" }); why == "" {
		t.Error("devlog records vouched with no live start token")
	}
}

func TestBootRefusalSecondDefault(t *testing.T) {
	const cfg = `C:\Users\p\AppData\Roaming`
	sav := filepath.Join(t.TempDir(), "s.sav")
	if err := os.WriteFile(sav, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(cfg, "Dolphin Emulator 2")
	env := map[string]string{"SAVE_STATE": sav, "USER_DIR_1": `D:\t1`, "USER_DIR_2": second}
	getenv := func(k string) string { return env[k] }
	e := launch.Env{GOOS: "windows", ConfigDir: cfg, Getenv: getenv}
	p := launch.Paths{UserDir1: `D:\t1`, UserDir2: second}
	if why := bootRefusal(e, getenv, p); !strings.Contains(why, "USER_DIR_2") {
		t.Errorf("booting on the second default: %q", why)
	}
	env["USER_DIR_2"], p.UserDir2 = `D:\t2`, `D:\t2`
	if why := bootRefusal(e, getenv, p); why != "" {
		t.Errorf("test dirs refused: %s", why)
	}
}

// TestVisErrBaseline: an error word left from before the run is
// reported, not failed; a new one fails.
func TestVisErrBaseline(t *testing.T) {
	g := newSmokeGame(t)
	g.u32(mailbox.VisAddr+mailbox.VisStatErrOff, mailbox.VisErrFont)
	var ss []gameSample
	for range 30 {
		ss = append(ss, g.tick(t, 3, true))
	}
	lim := testLimits
	lim.VisErrBase = mailbox.VisErrFont
	if c := checkNamed(t, livenessChecks("A", ss, 3, lim), "rel last error A"); !c.Pass || !strings.Contains(c.Detail, "before the run") {
		t.Errorf("an inherited error: %+v", c)
	}
	if c := checkNamed(t, livenessChecks("A", ss, 3, testLimits), "rel last error A"); c.Pass {
		t.Errorf("an error with no baseline passed: %+v", c)
	}
	g.u32(mailbox.VisAddr+mailbox.VisStatErrOff, mailbox.VisErrCreate)
	ss = append(ss, g.tick(t, 3, true))
	if c := checkNamed(t, livenessChecks("A", ss, 3, lim), "rel last error A"); c.Pass {
		t.Errorf("a new error passed: %+v", c)
	}
}

// TestPoseFeedArmed: leftovers from a killed session don't arm.
func TestPoseFeedArmed(t *testing.T) {
	var pre gameSample
	pre.ShadowMode = avatar.ModePoseFeed
	pre.PoseSeqs[0] = 9
	if poseFeedArmed(pre, pre) {
		t.Error("an unchanged leftover feed armed")
	}
	now := pre
	now.PoseSeqs[0] = 10
	if !poseFeedArmed(pre, now) {
		t.Error("a moved seq did not arm")
	}
	now.ShadowMode = avatar.ModeOff
	if poseFeedArmed(pre, now) {
		t.Error("armed with shadow_mode off")
	}
	now = pre
	now.PoseSeqs[0], now.PoseSeqs[1] = 0, 0
	if poseFeedArmed(pre, now) {
		t.Error("a zeroed seq armed")
	}
}

func TestParseSmokeNamesTrimmed(t *testing.T) {
	for _, bad := range []string{"A, A", " ,B", "A, "} {
		if _, err := parseSmokeArgs([]string{"--names", bad}); err == nil {
			t.Errorf("--names %q: no error", bad)
		}
	}
	o, err := parseSmokeArgs([]string{"--names", " Ann , Bo "})
	if err != nil || o.nameA != "Ann" || o.nameB != "Bo" {
		t.Errorf("trimmed names: %q %q %v", o.nameA, o.nameB, err)
	}
}

// TestFramedStepsBigGap: a save state that moves the frame counter
// millions forward is a discontinuity, not millions of steps.
func TestFramedStepsBigGap(t *testing.T) {
	ss := []stepSample{{Frame: 100, P: vec{0, 0, 0}}, {Frame: 101, P: vec{10, 0, 0}},
		{Frame: 50_000_000, P: vec{20, 0, 0}}, {Frame: 50_000_002, P: vec{40, 0, 0}}}
	steps, missed := framedSteps(ss)
	if len(steps) != 3 || missed != 1 {
		t.Errorf("steps %v, missed %d: want 3 steps, 1 missed", steps, missed)
	}
}

func TestMpLocalTestRate(t *testing.T) {
	root := t.TempDir()
	if r, from := mpLocalTestRate(""); r != "" || !strings.Contains(from, "off") {
		t.Errorf("logs off: %q %q", r, from)
	}
	if r, from := mpLocalTestRate(root); r != "" || !strings.HasPrefix(from, "unknown") {
		t.Errorf("no session: %q %q", r, from)
	}
	latest := filepath.Join(root, "latest")
	if err := os.MkdirAll(latest, 0o755); err != nil {
		t.Fatal(err)
	}
	m := sessionManifest{Started: time.Now(), Env: map[string]string{"ww_pose_test_rate": "20:40:20"}}
	if err := m.write(latest); err != nil {
		t.Fatal(err)
	}
	if r, from := mpLocalTestRate(root); r != "20:40:20" || !strings.Contains(from, "session.json") {
		t.Errorf("running session: %q %q", r, from)
	}
	now := time.Now()
	m.Ended = &now
	if err := m.write(latest); err != nil {
		t.Fatal(err)
	}
	if r, from := mpLocalTestRate(root); r != "" || !strings.Contains(from, "ended") {
		t.Errorf("ended session: %q %q", r, from)
	}
}

// TestMeasureSmoothingInterrupt: a cancel mid-walk (Ctrl+C) releases
// the stick and reports the run as interrupted.
func TestMeasureSmoothingInterrupt(t *testing.T) {
	w, o := mem.NewFakeMem(), mem.NewFakeMem()
	const link = 0x80400000
	b := make([]byte, 4)
	mem.PutU32BE(b, link)
	_ = w.Store(addr.PlayerPtr, b)
	p := make([]byte, 12)
	mem.PutF32BE(p, 100)
	_ = w.Store(link+addr.ActorOffCurPos, p)
	_ = o.Store(feed.SlotAddr(0, mailbox.PuppetOffPosX), p)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// Cancel once the stick is held.
		for ctx.Err() == nil {
			if en, _ := mem.ReadU8(w, mailbox.Mailbox+mailbox.OffInputEnable); en == 1 {
				cancel()
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	t0 := time.Now()
	_, err := measureSmoothing(ctx, w, o, 30*time.Second)
	cancel()
	if err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Errorf("err %v, want interrupted", err)
	}
	if time.Since(t0) > 10*time.Second {
		t.Errorf("the walk ran on after the cancel (%s)", time.Since(t0))
	}
	if en, _ := mem.ReadU8(w, mailbox.Mailbox+mailbox.OffInputEnable); en != 0 {
		t.Errorf("input_enable %d after the cancel: the stick is still held", en)
	}
}
