package network

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeConnectLog is a ConnectLog on a hand-advanced clock that records the
// lines it emits, tagged "WARN" or "OK".
type fakeConnectLog struct {
	*ConnectLog
	clock time.Time
	mu    sync.Mutex
	lines []string
}

func newFakeConnectLog(addr string) *fakeConnectLog {
	f := &fakeConnectLog{clock: time.Unix(1000, 0)}
	f.ConnectLog = NewConnectLog(addr,
		func(s string) { f.add("WARN " + s) },
		func(s string) { f.add("OK " + s) })
	f.ConnectLog.now = func() time.Time { return f.clock }
	return f
}

func (f *fakeConnectLog) add(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lines = append(f.lines, s)
}

// take returns the lines logged since the last take.
func (f *fakeConnectLog) take() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.lines
	f.lines = nil
	return out
}

var errUnreachable = errors.New("dial tcp 192.0.2.1:25565: i/o timeout")

// TestConnectLogPolicy walks one outage through the policy: the first
// failure logs at once with the address and error, repeats are held to one
// line per connectLogRepeat, and the first success logs a single recovery
// line. A second outage after that starts over with an immediate line.
func TestConnectLogPolicy(t *testing.T) {
	ctx := context.Background()
	f := newFakeConnectLog("192.0.2.1:25565")

	if err := f.Observe(ctx, nil); err != nil {
		t.Fatalf("Observe(nil) = %v", err)
	}
	if got := f.take(); len(got) != 0 {
		t.Fatalf("success with no prior failure logged %q, want nothing", got)
	}

	// First failure: logged immediately, naming the address and the error.
	if err := f.Observe(ctx, errUnreachable); !errors.Is(err, errUnreachable) {
		t.Fatalf("Observe returned %v, want the attempt's error unchanged", err)
	}
	got := f.take()
	if len(got) != 1 || !strings.HasPrefix(got[0], "WARN Can't reach 192.0.2.1:25565") ||
		!strings.Contains(got[0], errUnreachable.Error()) || !strings.Contains(got[0], "Retrying") {
		t.Fatalf("first failure logged %q, want one WARN line with address, error and retry note", got)
	}

	// Repeats inside the window are silent, whatever the attempt count.
	for _, step := range []time.Duration{100 * time.Millisecond, 5 * time.Second, 10 * time.Second, 14 * time.Second} {
		f.clock = f.clock.Add(step)
		f.Observe(ctx, errUnreachable)
	}
	if got := f.take(); len(got) != 0 {
		t.Fatalf("failures within %v of the last line logged %q, want nothing", connectLogRepeat, got)
	}

	// 30 s after the first line: one "still" line with the outage length.
	f.clock = time.Unix(1000, 0).Add(connectLogRepeat)
	f.Observe(ctx, errUnreachable)
	got = f.take()
	if len(got) != 1 || !strings.HasPrefix(got[0], "WARN Still can't reach 192.0.2.1:25565 after 30s") {
		t.Fatalf("failure at +30s logged %q, want one WARN \"Still can't reach ... after 30s\" line", got)
	}

	// The window restarts from that line, not from the outage start.
	f.clock = f.clock.Add(connectLogRepeat - time.Second)
	f.Observe(ctx, errUnreachable)
	if got := f.take(); len(got) != 0 {
		t.Fatalf("failure 29s after the last line logged %q, want nothing", got)
	}
	f.clock = f.clock.Add(time.Second)
	f.Observe(ctx, errUnreachable)
	if got := f.take(); len(got) != 1 || !strings.Contains(got[0], "after 1m0s") {
		t.Fatalf("failure at +60s logged %q, want one line saying \"after 1m0s\"", got)
	}

	// Recovery: one OK line, then successes are silent again.
	f.clock = f.clock.Add(time.Second)
	f.Observe(ctx, nil)
	if got := f.take(); len(got) != 1 || got[0] != "OK Reached 192.0.2.1:25565; connected." {
		t.Fatalf("success after failures logged %q, want one OK recovery line", got)
	}
	f.Observe(ctx, nil)
	if got := f.take(); len(got) != 0 {
		t.Fatalf("second success logged %q, want nothing", got)
	}

	// A new outage (the host went away later) is reported at once again.
	f.clock = f.clock.Add(time.Second)
	f.Observe(ctx, errUnreachable)
	if got := f.take(); len(got) != 1 || !strings.HasPrefix(got[0], "WARN Can't reach") {
		t.Fatalf("first failure of a new outage logged %q, want an immediate WARN line", got)
	}
}

// TestConnectLogIgnoresShutdown: a connect that fails because ctx was
// cancelled (TUI Esc, Ctrl+C) is a shutdown, not an unreachable host. It must
// log nothing and must not start an outage.
func TestConnectLogIgnoresShutdown(t *testing.T) {
	f := newFakeConnectLog("host:1")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	if err := f.Observe(cancelled, context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Observe returned %v, want context.Canceled unchanged", err)
	}
	if got := f.take(); len(got) != 0 {
		t.Fatalf("cancelled attempt logged %q, want nothing", got)
	}
	// No outage was started, so a later success has nothing to recover from.
	f.Observe(context.Background(), nil)
	if got := f.take(); len(got) != 0 {
		t.Fatalf("success after a cancelled attempt logged %q, want nothing", got)
	}
}

// TestConnectLogSharedAcrossLoops: host/join run two reconnect loops against
// the same address and share one ConnectLog, so an outage both loops hit is
// reported once, not once per loop. Run under -race.
func TestConnectLogSharedAcrossLoops(t *testing.T) {
	f := newFakeConnectLog("host:1")
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				f.Observe(context.Background(), errUnreachable)
			}
		}()
	}
	wg.Wait()
	if got := f.take(); len(got) != 1 {
		t.Fatalf("two loops failing together logged %d lines %q, want 1", len(got), got)
	}
}

// TestConnectLogNilSinks: nil warn/ok funcs drop the lines without panicking.
func TestConnectLogNilSinks(t *testing.T) {
	l := NewConnectLog("host:1", nil, nil)
	l.Observe(context.Background(), errUnreachable)
	l.Observe(context.Background(), nil)
}

// TestConnectLogFatal: a fatal connect error (a reject or a version
// mismatch, which ReconnectLoop gives up on) logs one "can't join" line with
// the reason instead of the "retrying" wording, once across both loops.
func TestConnectLogFatal(t *testing.T) {
	f := newFakeConnectLog("192.0.2.1:25565")
	fatal := &RejectError{Code: RejectVersion, Reason: "host runs an older ww-multiplayer"}
	ctx := context.Background()

	if err := f.Observe(ctx, fatal); err != fatal {
		t.Fatalf("Observe returned %v, want the error unchanged", err)
	}
	f.Observe(ctx, fatal) // the session's other loop
	got := f.take()
	if len(got) != 1 {
		t.Fatalf("logged %q, want exactly one line", got)
	}
	if !strings.HasPrefix(got[0], "WARN Can't join 192.0.2.1:25565: ") ||
		!strings.Contains(got[0], "older ww-multiplayer") || strings.Contains(got[0], "etrying") {
		t.Fatalf("line %q: want a can't-join line with the reason and no retry claim", got[0])
	}
}

// TestConnectLogFatalSink: with Fatal set, the one can't-join line goes to
// Fatal instead of warn, and ordinary failures still go to warn.
func TestConnectLogFatalSink(t *testing.T) {
	f := newFakeConnectLog("192.0.2.1:25565")
	f.Fatal = func(s string) { f.add("FATAL " + s) }
	ctx := context.Background()

	f.Observe(ctx, errUnreachable)
	if got := f.take(); len(got) != 1 || !strings.HasPrefix(got[0], "WARN ") {
		t.Fatalf("ordinary failure logged %q, want one WARN line", got)
	}
	fatal := &RejectError{Code: RejectServerFull, Reason: "server full"}
	f.Observe(ctx, fatal)
	f.Observe(ctx, fatal)
	got := f.take()
	if len(got) != 1 || !strings.HasPrefix(got[0], "FATAL Can't join 192.0.2.1:25565: ") {
		t.Fatalf("fatal failure logged %q, want one FATAL can't-join line", got)
	}
}
