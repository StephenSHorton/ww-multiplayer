package engine

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// Pipeline is one named, long-running half of a multiplayer session
// (broadcast-pose or puppet-sync) for RunPipelines.
type Pipeline struct {
	Name string
	Run  func(context.Context) error
}

// RunPipelines runs every pipeline concurrently under ctx. When any of them
// returns, it cancels ctx (via cancel) so the others stop too — a session
// with one dead half isn't left looking alive — and waits for all of them.
//
// It returns the first real failure, prefixed with that pipeline's name and
// NOT yet logged, so callers can log it once and exit non-zero. A pipeline
// that returns nil or context.Canceled ended because of a normal Ctrl+C or
// Esc shutdown and doesn't count. Any further failures are logged here as
// they happen, since only one error can be returned.
func RunPipelines(ctx context.Context, cancel context.CancelFunc, rep log.Reporter, ps ...Pipeline) error {
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		first error
	)
	for _, p := range ps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer cancel()
			err := p.Run(ctx)
			if err == nil || errors.Is(err, context.Canceled) {
				return
			}
			// A fatal connect error (reject, version mismatch) was already
			// printed once by the shared ConnectLog, and both pipelines hit
			// the same one, so it is returned as is and never logged here.
			fatal := errors.Is(err, network.ErrFatal)
			if !fatal {
				err = fmt.Errorf("%s: %w", p.Name, err)
			}
			mu.Lock()
			isFirst := first == nil
			if isFirst {
				first = err
			}
			mu.Unlock()
			if !isFirst && !fatal {
				rep.Log(log.Err, err.Error())
			}
		}()
	}
	wg.Wait()
	return first
}

// degradeBackoff and degradeBackoffMax are Degradable's waits before it
// runs a pipeline again after a retryable failure: the first, doubled
// per failure in a row up to the second (ReconnectLoop's shape). Tests
// shorten them.
var (
	degradeBackoff    = 2 * time.Second
	degradeBackoffMax = 30 * time.Second
)

// Degradable wraps p so that it never ends its RunPipelines group: the
// enemy-sync pipeline beside the pose pipelines (release-readiness A2).
// It runs p again after a failure retryable reports true (a Dolphin
// open/read or network error), with one Warn per reason and a backoff. On
// any other failure (ErrFatal: a reject or a version mismatch) or a panic
// it calls onDown (nil: none) once, says enemy sync is off, and waits for
// ctx to end: the pipeline stays down until the group runs again (the
// GameRunner reruns it after a pose failure or a Dolphin change, which
// gives the wrapper a fresh start). Every way out waits for ctx, since
// RunPipelines cancels the group when a pipeline returns, so the wrapped
// pipeline returns nil, and only once ctx has ended.
//
// The recover sits outside p.Run, so p's own deferred cleanup (the
// enemy pipelines' block Shutdown) runs first. A panic on another
// goroutine p started is not caught here: the enemy session's tick loop
// runs on network.RunEnemy's goroutine, which recovers it and returns a
// *network.SessionPanicError (ErrFatal), logged here with its stack, and
// the read loop's callbacks (OnEnemy) have the client's own recover.
func Degradable(p Pipeline, rep log.Reporter, retryable func(error) bool, onDown func(error)) Pipeline {
	run := func(ctx context.Context) (panicked bool, err error) {
		defer func() {
			if r := recover(); r != nil {
				panicked, err = true, fmt.Errorf("%s: panic: %v", p.Name, r)
				log.Logf(rep, log.Err, "%v\n%s", err, debug.Stack())
			}
		}()
		return false, p.Run(ctx)
	}
	return Pipeline{Name: p.Name, Run: func(ctx context.Context) error {
		warned := map[string]bool{}
		wait := degradeBackoff
		for {
			start := time.Now()
			panicked, err := run(ctx)
			if err == nil || errors.Is(err, context.Canceled) || ctx.Err() != nil {
				<-ctx.Done()
				return nil
			}
			if sp := (*network.SessionPanicError)(nil); errors.As(err, &sp) {
				panicked = true
				log.Logf(rep, log.Err, "%s: %v\n%s", p.Name, sp, sp.Stack)
			}
			if panicked || retryable == nil || !retryable(err) {
				log.Logf(rep, log.Warn, "enemy sync off for this session: %v; multiplayer is unaffected", err)
				if onDown != nil {
					onDown(err)
				}
				<-ctx.Done()
				return nil
			}
			if why := err.Error(); !warned[why] {
				warned[why] = true
				log.Logf(rep, log.Warn, "%s stopped (%v); starting it again", p.Name, err)
			}
			// A run that lasted past the longest wait was a healthy one:
			// the next failure starts the backoff over.
			if time.Since(start) > degradeBackoffMax {
				wait = degradeBackoff
			}
			if network.SleepCtx(ctx, wait) != nil {
				return nil
			}
			wait = min(2*wait, degradeBackoffMax)
		}
	}}
}

// LogSessionErr logs a host/join session's returned error unless it is a
// fatal connect error, which the session's ConnectLog already reported.
func LogSessionErr(rep log.Reporter, err error) {
	if err != nil && !errors.Is(err, network.ErrFatal) {
		rep.Log(log.Err, err.Error())
	}
}
