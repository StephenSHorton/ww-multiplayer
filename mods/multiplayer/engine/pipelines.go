package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"

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

// LogSessionErr logs a host/join session's returned error unless it is a
// fatal connect error, which the session's ConnectLog already reported.
func LogSessionErr(rep log.Reporter, err error) {
	if err != nil && !errors.Is(err, network.ErrFatal) {
		rep.Log(log.Err, err.Error())
	}
}
