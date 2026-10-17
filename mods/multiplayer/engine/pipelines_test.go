package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// recordingReporter is a log.Reporter that keeps every line.
type recordingReporter struct {
	lines []string
}

func (r *recordingReporter) Log(_ log.Level, msg string) {
	r.lines = append(r.lines, msg)
}

func (r *recordingReporter) joined() string { return strings.Join(r.lines, "\n") }

// runPipelinesWithin runs RunPipelines on a fresh ctx and fails the test if
// it doesn't return within a second.
func runPipelinesWithin(t *testing.T, rep *recordingReporter, ps ...Pipeline) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- RunPipelines(ctx, cancel, rep, ps...) }()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		t.Fatal("RunPipelines did not return within 1s")
		return nil
	}
}

// untilCancel is a pipeline body that runs until the session is cancelled,
// then returns ret — the shape of a healthy broadcast-pose / puppet-sync.
func untilCancel(ret error) func(context.Context) error {
	return func(ctx context.Context) error {
		<-ctx.Done()
		return ret
	}
}

// TestRunPipelinesReturnsFirstFailure: when one half of the session fails,
// the other is cancelled, and the failure comes back (prefixed with the
// pipeline's name, unlogged) so host/join can exit non-zero.
func TestRunPipelinesReturnsFirstFailure(t *testing.T) {
	boom := errors.New("dolphin not running")
	rep := &recordingReporter{}
	err := runPipelinesWithin(t, rep,
		Pipeline{"broadcast-pose", func(context.Context) error { return boom }},
		Pipeline{"puppet-sync", untilCancel(nil)},
	)
	if !errors.Is(err, boom) || err.Error() != "broadcast-pose: dolphin not running" {
		t.Fatalf("RunPipelines = %v, want the broadcast-pose failure", err)
	}
	if len(rep.lines) != 0 {
		t.Errorf("RunPipelines logged %q; the returned error is the caller's to log", rep.lines)
	}
}

// TestRunPipelinesCleanShutdownIsNil: a normal Ctrl+C / Esc shutdown, where
// every pipeline returns nil or context.Canceled, is not a failure, so the
// process still exits 0.
func TestRunPipelinesCleanShutdownIsNil(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{}, 2)
	wait := func(ret error) func(context.Context) error {
		return func(ctx context.Context) error {
			started <- struct{}{}
			<-ctx.Done()
			return ret
		}
	}
	done := make(chan error, 1)
	rep := &recordingReporter{}
	go func() {
		done <- RunPipelines(ctx, cancel, rep,
			Pipeline{"broadcast-pose", wait(nil)},
			Pipeline{"puppet-sync", wait(context.Canceled)},
		)
	}()
	<-started
	<-started
	cancel() // the user's Ctrl+C
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunPipelines after a clean shutdown = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RunPipelines did not return after cancel")
	}
	if len(rep.lines) != 0 {
		t.Errorf("clean shutdown logged %q, want nothing", rep.lines)
	}
}

// TestRunPipelinesLogsLaterFailures: only one error can be returned, so a
// second pipeline failing too is logged instead of being lost.
func TestRunPipelinesLogsLaterFailures(t *testing.T) {
	rep := &recordingReporter{}
	err := runPipelinesWithin(t, rep,
		Pipeline{"broadcast-pose", func(context.Context) error { return errors.New("first") }},
		Pipeline{"puppet-sync", untilCancel(errors.New("second"))},
	)
	if err == nil || err.Error() != "broadcast-pose: first" {
		t.Fatalf("RunPipelines = %v, want the first failure", err)
	}
	if got := rep.joined(); got != "puppet-sync: second" {
		t.Errorf("logged %q, want the later failure %q", got, "puppet-sync: second")
	}
}

// TestRunPipelinesEndsWhenOneReturnsNil: a pipeline that ends on its own,
// even without an error, takes the session down with it, so a half-dead
// session never sits there looking connected.
func TestRunPipelinesEndsWhenOneReturnsNil(t *testing.T) {
	rep := &recordingReporter{}
	err := runPipelinesWithin(t, rep,
		Pipeline{"broadcast-pose", func(context.Context) error { return nil }},
		Pipeline{"puppet-sync", untilCancel(nil)},
	)
	if err != nil {
		t.Fatalf("RunPipelines = %v, want nil", err)
	}
	if strings.TrimSpace(rep.joined()) != "" {
		t.Errorf("logged %q, want nothing", rep.lines)
	}
}

// TestRunPipelinesFatalReportedOnce: a fatal connect error (the host refused
// us) hits both pipelines, and the shared ConnectLog has already printed it.
// RunPipelines returns it unprefixed and logs neither copy, and the callers'
// LogSessionErr skips it, so the reason appears exactly once.
func TestRunPipelinesFatalReportedOnce(t *testing.T) {
	reject := &network.RejectError{Code: network.RejectServerFull, Reason: "host refused the connection: server full"}
	rep := &recordingReporter{}
	err := runPipelinesWithin(t, rep,
		Pipeline{"broadcast-pose", func(context.Context) error { return reject }},
		Pipeline{"puppet-sync", func(context.Context) error { return reject }},
	)
	if err != error(reject) {
		t.Fatalf("RunPipelines = %v, want the reject unchanged", err)
	}
	LogSessionErr(rep, err)
	if len(rep.lines) != 0 {
		t.Errorf("logged %q; a fatal error is the ConnectLog's to report", rep.lines)
	}
	LogSessionErr(rep, errors.New("dolphin not running"))
	if len(rep.lines) != 1 {
		t.Errorf("LogSessionErr logged %q for an ordinary failure, want one line", rep.lines)
	}
}
