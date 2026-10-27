package enemysync

import (
	"fmt"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// RepeatEvery is how often a run of failures that repeats an error
// already logged says how many more there were (repeatWarn). A var:
// tests shorten it.
var RepeatEvery = 30 * time.Second

// repeatReasons caps the distinct error texts one run of failures logs in
// full: past it, new texts only count (an error that carries a changing
// number must not flood either).
const repeatReasons = 4

// repeatWarn keeps an error that a tick loop meets on every tick (a
// Dolphin that died or stopped answering: 60 failed reads a second) from
// flooding the log, in the pose feed's paused/resumed shape
// (feed.Feeder.running): the first failure of a run is one Warn, each new
// error text in it one more (up to repeatReasons), the repeats only a
// count every RepeatEvery, and the first success after a run one OK line
// when resumed is set. The loop itself keeps ticking: a stall that
// clears resumes it with nothing restarted. Not safe for concurrent use
// (one per loop site).
type repeatWarn struct {
	rep log.Reporter
	// first is the Warn for a new error text in a run.
	first func(err error) string
	// again prefixes the count line: "<again>: N more in the last 30s (<err>)".
	again string
	// resumed is the OK line after a run (n failed ticks over d); nil: none.
	resumed func(n int, d time.Duration) string

	n       int // failures in this run (0: none)
	start   time.Time
	reasons map[string]bool
	counted time.Time // the last count line (or the run's start)
	since   int       // failures not yet in a line
	lastErr string
}

// Fail notes a failed tick at now.
func (w *repeatWarn) Fail(err error, now time.Time) {
	if w.n == 0 {
		w.start, w.counted, w.since = now, now, 0
		w.reasons = map[string]bool{}
	}
	w.n++
	why := err.Error()
	w.lastErr = why
	if !w.reasons[why] && len(w.reasons) < repeatReasons {
		w.reasons[why] = true
		log.Logf(w.rep, log.Warn, "%s", w.first(err))
		return
	}
	w.since++
	if now.Sub(w.counted) >= RepeatEvery {
		log.Logf(w.rep, log.Warn, "%s: %d more in the last %s (%s)", w.again, w.since, now.Sub(w.counted).Round(time.Second), w.lastErr)
		w.counted, w.since = now, 0
	}
}

// OK notes a tick at now that did not fail: it ends a run.
func (w *repeatWarn) OK(now time.Time) {
	if w.n == 0 {
		return
	}
	if w.resumed != nil {
		log.Logf(w.rep, log.OK, "%s", w.resumed(w.n, now.Sub(w.start).Round(100*time.Millisecond)))
	}
	w.n = 0
}

// Note is Fail for a non-nil err, else OK.
func (w *repeatWarn) Note(err error, now time.Time) {
	if err != nil {
		w.Fail(err, now)
	} else {
		w.OK(now)
	}
}

// plainWarn is a repeatWarn whose first line is prefix + ": " + the error
// and which says nothing when the error clears.
func plainWarn(rep log.Reporter, prefix string) *repeatWarn {
	return &repeatWarn{rep: rep, again: prefix + ": the same error", first: func(err error) string { return fmt.Sprintf("%s: %v", prefix, err) }}
}

// pausedWarn is the repeatWarn of a loop whose tick fails (most often its
// game can't be read: a Dolphin that died or stalled): who paused, with
// what it holds back, and resumed at the next tick that succeeds, in the
// pose feed's words ("pose feed paused: ...; ... until it runs again").
func pausedWarn(rep log.Reporter, who, holds string) *repeatWarn {
	return &repeatWarn{
		rep:   rep,
		again: who + " still paused",
		first: func(err error) string {
			return fmt.Sprintf("%s paused: %v; %s until it runs again", who, err, holds)
		},
		resumed: func(n int, d time.Duration) string {
			return fmt.Sprintf("%s resumed: running again after %d failed ticks over %s", who, n, d)
		},
	}
}
