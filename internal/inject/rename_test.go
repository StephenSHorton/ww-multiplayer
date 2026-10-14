package inject

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeRename fails its first n calls with err, then succeeds.
type fakeRename struct {
	n, calls int
	err      error
}

func (f *fakeRename) rename(oldpath, newpath string) error {
	f.calls++
	if f.calls <= f.n {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: f.err}
	}
	return nil
}

func recordSleeps(into *[]time.Duration) func(time.Duration) {
	return func(d time.Duration) { *into = append(*into, d) }
}

func TestRetryRenameRidesOutSharingViolation(t *testing.T) {
	fr := &fakeRename{n: 3, err: winErrorSharingViolation}
	var sleeps []time.Duration
	if err := retryRename(fr.rename, "a.tmp", "a.iso", isWindowsSharingError, recordSleeps(&sleeps)); err != nil {
		t.Fatalf("err = %v, want success on the 4th try", err)
	}
	want := []time.Duration{5 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond}
	if fr.calls != 4 || !slices.Equal(sleeps, want) {
		t.Errorf("calls = %d, sleeps = %v; want 4 calls, sleeps %v", fr.calls, sleeps, want)
	}
}

func TestRetryRenameStopsOnOtherErrors(t *testing.T) {
	fr := &fakeRename{n: 100, err: syscall.Errno(2)} // ERROR_FILE_NOT_FOUND / ENOENT
	var sleeps []time.Duration
	err := retryRename(fr.rename, "a.tmp", "a.iso", isWindowsSharingError, recordSleeps(&sleeps))
	if !errors.Is(err, syscall.Errno(2)) || fr.calls != 1 || len(sleeps) != 0 {
		t.Errorf("err = %v, calls = %d, sleeps = %v; want the error after 1 call, no sleep", err, fr.calls, sleeps)
	}
}

func TestRetryRenameGivesUpAfterBudget(t *testing.T) {
	fr := &fakeRename{n: 1 << 30, err: winErrorAccessDenied}
	var sleeps []time.Duration
	err := retryRename(fr.rename, "a.tmp", "a.iso", isWindowsSharingError, recordSleeps(&sleeps))
	if !errors.Is(err, winErrorAccessDenied) {
		t.Fatalf("err = %v, want it to wrap ERROR_ACCESS_DENIED", err)
	}
	if !strings.Contains(err.Error(), "close Dolphin") {
		t.Errorf("err = %q, want a hint to close whatever holds the file", err)
	}
	var total time.Duration
	for _, d := range sleeps {
		total += d
		if d > renameRetryMaxStep {
			t.Errorf("slept %v in one step, more than %v", d, renameRetryMaxStep)
		}
	}
	if total > renameRetryBudget || total+renameRetryMaxStep <= renameRetryBudget {
		t.Errorf("slept %v in total, want just under %v", total, renameRetryBudget)
	}
	if fr.calls != len(sleeps)+1 {
		t.Errorf("calls = %d with %d sleeps, want one more call than sleeps", fr.calls, len(sleeps))
	}
}

func TestIsWindowsSharingError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{&os.LinkError{Op: "rename", Err: winErrorAccessDenied}, true},
		{fmt.Errorf("wrapped: %w", &os.LinkError{Op: "rename", Err: winErrorSharingViolation}), true},
		{&os.LinkError{Op: "rename", Err: syscall.Errno(2)}, false},
		{errors.New("access is denied"), false},
		{nil, false},
	}
	for _, c := range cases {
		if got := isWindowsSharingError(c.err); got != c.want {
			t.Errorf("isWindowsSharingError(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

// Errno 5 and 32 are EIO and EPIPE on Unix; only Windows retries them.
func TestRenameErrIsTransientOnlyOnWindows(t *testing.T) {
	err := &os.LinkError{Op: "rename", Err: winErrorSharingViolation}
	if got, want := renameErrIsTransient(err), runtime.GOOS == "windows"; got != want {
		t.Errorf("renameErrIsTransient(errno 32) on %s = %v, want %v", runtime.GOOS, got, want)
	}
}
