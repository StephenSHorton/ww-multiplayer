package enemysync

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// flakyMem is a FakeMem whose reads and writes fail while fail is set, as
// a Dolphin that died (ReadProcessMemory on a closed process) or stalled.
type flakyMem struct {
	*mem.FakeMem
	fail atomic.Bool
}

var errDeadDolphin = errors.New("ReadProcessMemory failed")

func (f *flakyMem) Read(a uint32, n int) ([]byte, error) {
	if f.fail.Load() {
		return nil, errDeadDolphin
	}
	return f.FakeMem.Read(a, n)
}

func (f *flakyMem) Write(a uint32, b []byte) error {
	if f.fail.Load() {
		return errDeadDolphin
	}
	return f.FakeMem.Write(a, b)
}

func (l *levelLog) with(level log.Level, sub string) int {
	n := 0
	for _, x := range l.lines {
		if x.level == level && strings.Contains(x.msg, sub) {
			n++
		}
	}
	return n
}

// testHostLoop is a host loop over m as HostPipeline builds one (remote
// bodies off, no roster).
func testHostLoop(m mem.Mem, rep log.Reporter, start time.Time) *hostLoop {
	host := NewHost(m, rep, 7, start)
	applier := NewHitApplier(m, rep, 7)
	applier.Kinds = host.Kinds
	remote := NewRemoteHost(m, rep, RemoteConfig{}, 7)
	remote.Kinds = host.Kinds
	return newHostLoop(m, rep, host, applier, remote, nil, make(chan *Hit, 4))
}

// TestRepeatWarnRun: a run of one error is one Warn, a count line every
// RepeatEvery and one OK when it clears; a new error text in the run is
// one more Warn, up to repeatReasons; past that new texts only count.
func TestRepeatWarnRun(t *testing.T) {
	rep := &levelLog{}
	w := pausedWarn(rep, "enemy host", "no enemy STATE")
	start := time.Unix(1000, 0)
	now := start
	for i := 0; i < 65*60; i++ { // 65 s at 60 Hz
		w.Fail(errDeadDolphin, now)
		now = now.Add(time.Second / 60)
	}
	if got := rep.with(log.Warn, "enemy host paused: ReadProcessMemory failed"); got != 1 {
		t.Errorf("%d paused lines, want 1: %v", got, rep.lines)
	}
	if got := rep.with(log.Warn, "enemy host still paused:"); got != 2 {
		t.Errorf("%d count lines in 65 s, want 2 (30 s, 60 s): %v", got, rep.lines)
	}
	if len(rep.lines) != 3 {
		t.Errorf("%d lines for 3900 failures, want 3: %v", len(rep.lines), rep.lines)
	}
	w.OK(now)
	w.OK(now.Add(time.Second))
	if got := rep.with(log.OK, "enemy host resumed: running again after 3900 failed ticks over 1m5s"); got != 1 {
		t.Errorf("resumed: %v", rep.lines)
	}

	// A second run starts over, and its distinct texts are capped.
	rep.lines = nil
	for i := 0; i < 10; i++ {
		w.Fail(fmt.Errorf("read failed at 0x%08X", 0x80000000+i), now)
	}
	if got := rep.with(log.Warn, "paused"); got != repeatReasons {
		t.Errorf("%d Warns for 10 distinct texts, want %d: %v", got, repeatReasons, rep.lines)
	}

	// A plainWarn says nothing when its error clears.
	rep.lines = nil
	p := plainWarn(rep, "enemy host: keepalive")
	p.Fail(errDeadDolphin, now)
	p.Fail(errDeadDolphin, now)
	p.OK(now)
	if len(rep.lines) != 1 || rep.lines[0].msg != "enemy host: keepalive: ReadProcessMemory failed" {
		t.Errorf("plainWarn: %v", rep.lines)
	}
}

// TestHostLoopDeadGame: the R7b flood. A host whose Dolphin can't be read
// (every read fails) ticks 60 times a second for 137 s: that was 8,193
// "enemy host: read failed" lines; now it is one paused Warn, a count
// every RepeatEvery from each site that fails, and nothing sent. The
// session does not end (step returns nil), so it resumes when the reads
// come back.
func TestHostLoopDeadGame(t *testing.T) {
	m := &flakyMem{FakeMem: mem.NewFakeMem()}
	m.fail.Store(true)
	rep := &levelLog{}
	start := time.Unix(1000, 0)
	loop := testHostLoop(m, rep, start)
	sent := 0
	send := func([]byte) error { sent++; return nil }
	now := start
	for i := 0; i < 137*60; i++ {
		if err := loop.step(now, send); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
		now = now.Add(Tick)
	}
	if sent != 0 {
		t.Errorf("%d frames sent from a game that can't be read", sent)
	}
	if got := rep.with(log.Warn, "enemy host paused:"); got != 1 {
		t.Errorf("%d paused lines, want 1", got)
	}
	for _, l := range rep.lines {
		t.Logf("%v %s", l.level, l.msg)
	}
	if n := rep.count(log.Warn); n > 6 {
		t.Errorf("%d Warn lines in 137 s of failed reads (the flood was 8,193): %v", n, rep.lines)
	}
}

// TestHostLoopStallRecovers: reads that fail for a while and then come
// back resume the loop with no restart: one paused Warn, one resumed OK,
// and STATEs sent again (the title screen's count-0 keepalive).
func TestHostLoopStallRecovers(t *testing.T) {
	m := &flakyMem{FakeMem: mem.NewFakeMem()}
	rep := &levelLog{}
	start := time.Unix(1000, 0)
	loop := testHostLoop(m, rep, start)
	sent := 0
	send := func([]byte) error { sent++; return nil }
	now := start
	run := func(d time.Duration) {
		for end := now.Add(d); now.Before(end); now = now.Add(Tick) {
			if err := loop.step(now, send); err != nil {
				t.Fatal(err)
			}
		}
	}
	run(2 * time.Second)
	before := sent
	if before == 0 {
		t.Fatal("no STATE sent from a readable game")
	}
	m.fail.Store(true)
	run(5 * time.Second)
	if sent != before {
		t.Errorf("%d frames sent while the game couldn't be read", sent-before)
	}
	m.fail.Store(false)
	run(2 * time.Second)
	if sent == before {
		t.Error("nothing sent after the reads came back")
	}
	if p, r := rep.with(log.Warn, "enemy host paused:"), rep.with(log.OK, "enemy host resumed:"); p != 1 || r != 1 {
		t.Errorf("paused %d, resumed %d, want 1 and 1: %v", p, r, rep.lines)
	}
}
