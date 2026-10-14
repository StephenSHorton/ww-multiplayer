package network

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// connectLogRepeat is how often a reconnect loop that is still failing
// re-logs, so the user can see it is still trying without getting one line
// per attempt.
const connectLogRepeat = 30 * time.Second

// ConnectLog turns a reconnect loop's connect outcomes into a few log lines.
// ReconnectLoop retries a failed connect forever in the background (#8), so
// without this a joiner who typed a wrong or unreachable address saw nothing
// at all. The policy:
//
//   - the first failure of an outage is logged at once, with the address,
//     the error, and that the loop keeps retrying;
//   - further failures are logged at most once per connectLogRepeat;
//   - the first success after a failure logs one line saying so;
//   - a fatal error (errors.Is ErrFatal: a reject or a version mismatch,
//     which ReconnectLoop does not retry) logs one "can't join" line with
//     the reason, and never claims to be retrying.
//
// A failure while ctx is cancelled is a shutdown, not an outage, and is not
// logged. A ConnectLog is safe for concurrent use, so the two reconnect loops
// of one host/join session (broadcast-pose and puppet-sync, which dial the
// same address) can share one and the user sees each line once.
type ConnectLog struct {
	addr string
	warn func(string) // failure lines
	ok   func(string) // the recovery line
	now  func() time.Time

	mu        sync.Mutex
	failing   bool      // an outage is in progress
	firstFail time.Time // when the current outage started
	lastLog   time.Time // when its last failure line was logged
	fatal     bool      // a fatal error was already logged
}

// NewConnectLog returns a ConnectLog for connects to addr. warn receives the
// failure lines and ok the recovery line; either may be nil to drop them.
func NewConnectLog(addr string, warn, ok func(string)) *ConnectLog {
	return &ConnectLog{addr: addr, warn: warn, ok: ok, now: time.Now}
}

// Observe records the outcome err of one connect attempt made under ctx,
// logs whatever the policy calls for, and returns err unchanged, so a
// ReconnectLoop connect callback can end with
//
//	return connLog.Observe(ctx, client.ConnectContext(ctx, addr))
func (l *ConnectLog) Observe(ctx context.Context, err error) error {
	if err != nil && ctx.Err() != nil {
		return err
	}
	if line, failed := l.record(err, l.now()); line != "" {
		emit := l.ok
		if failed {
			emit = l.warn
		}
		if emit != nil {
			emit(line)
		}
	}
	return err
}

// record applies the policy to one outcome at time now and returns the line
// to log ("" for none) and whether it reports a failure.
func (l *ConnectLog) record(err error, now time.Time) (line string, failed bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if errors.Is(err, ErrFatal) {
		// Both loops of a session hit the same reject; say it once.
		if l.fatal {
			return "", true
		}
		l.fatal = true
		return fmt.Sprintf("Can't join %s: %v", l.addr, err), true
	}
	if err == nil {
		if !l.failing {
			return "", false
		}
		l.failing = false
		return fmt.Sprintf("Reached %s; connected.", l.addr), false
	}
	if !l.failing {
		l.failing = true
		l.firstFail, l.lastLog = now, now
		return fmt.Sprintf("Can't reach %s (%v). Retrying in the background; check the address and that the host is running.", l.addr, err), true
	}
	if now.Sub(l.lastLog) < connectLogRepeat {
		return "", true
	}
	l.lastLog = now
	return fmt.Sprintf("Still can't reach %s after %s (%v). Still retrying.",
		l.addr, now.Sub(l.firstFail).Round(time.Second), err), true
}
