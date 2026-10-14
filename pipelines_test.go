package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// runPipelinesWithin runs runPipelines on a fresh ctx and fails the test if
// it doesn't return within a second.
func runPipelinesWithin(t *testing.T, rep *recordingReporter, ps ...pipeline) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runPipelines(ctx, cancel, rep, ps...) }()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		t.Fatal("runPipelines did not return within 1s")
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
		pipeline{"broadcast-pose", func(context.Context) error { return boom }},
		pipeline{"puppet-sync", untilCancel(nil)},
	)
	if !errors.Is(err, boom) || err.Error() != "broadcast-pose: dolphin not running" {
		t.Fatalf("runPipelines = %v, want the broadcast-pose failure", err)
	}
	if len(rep.lines) != 0 {
		t.Errorf("runPipelines logged %q; the returned error is the caller's to log", rep.lines)
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
		done <- runPipelines(ctx, cancel, rep,
			pipeline{"broadcast-pose", wait(nil)},
			pipeline{"puppet-sync", wait(context.Canceled)},
		)
	}()
	<-started
	<-started
	cancel() // the user's Ctrl+C
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runPipelines after a clean shutdown = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runPipelines did not return after cancel")
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
		pipeline{"broadcast-pose", func(context.Context) error { return errors.New("first") }},
		pipeline{"puppet-sync", untilCancel(errors.New("second"))},
	)
	if err == nil || err.Error() != "broadcast-pose: first" {
		t.Fatalf("runPipelines = %v, want the first failure", err)
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
		pipeline{"broadcast-pose", func(context.Context) error { return nil }},
		pipeline{"puppet-sync", untilCancel(nil)},
	)
	if err != nil {
		t.Fatalf("runPipelines = %v, want nil", err)
	}
	if strings.TrimSpace(rep.joined()) != "" {
		t.Errorf("logged %q, want nothing", rep.lines)
	}
}
