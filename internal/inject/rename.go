package inject

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"time"
)

// Windows error codes for a rename blocked by another process holding one
// of the files open. Antivirus (Defender) and the search indexer routinely
// open a freshly written file for a moment, which is why Go's own toolchain
// retries renames on these (cmd/go/internal/robustio). Spelled as raw
// Errnos so this file builds, and its tests run, on every platform.
const (
	winErrorAccessDenied     = syscall.Errno(5)  // ERROR_ACCESS_DENIED
	winErrorSharingViolation = syscall.Errno(32) // ERROR_SHARING_VIOLATION
)

// Rename retry schedule: the delay starts at renameRetryFirst and doubles
// up to renameRetryMaxStep, until the next delay would take the total past
// renameRetryBudget (about 2 s, like robustio).
const (
	renameRetryFirst   = 5 * time.Millisecond
	renameRetryMaxStep = 250 * time.Millisecond
	renameRetryBudget  = 2 * time.Second
)

// replaceFile renames the finished temp file over the output. It and
// renameSleep are variables so tests can make that last step fail, or have
// a real failure skip the waits.
var (
	replaceFile = func(oldpath, newpath string) error {
		return retryRename(os.Rename, oldpath, newpath, renameErrIsTransient, renameSleep)
	}
	renameSleep = time.Sleep
)

// renameErrIsTransient reports whether a failed rename may succeed if
// retried. Only Windows has this failure mode.
func renameErrIsTransient(err error) bool {
	return runtime.GOOS == "windows" && isWindowsSharingError(err)
}

// isWindowsSharingError reports whether err carries one of the Windows
// error codes for a file another process has open.
func isWindowsSharingError(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) &&
		(errno == winErrorAccessDenied || errno == winErrorSharingViolation)
}

// retryRename calls rename until it succeeds, fails with an error that
// transient rejects, or the retry budget runs out. Waits go through sleep
// so tests don't block.
func retryRename(rename func(oldpath, newpath string) error, oldpath, newpath string,
	transient func(error) bool, sleep func(time.Duration)) error {
	delay, slept := renameRetryFirst, time.Duration(0)
	for {
		err := rename(oldpath, newpath)
		if err == nil || !transient(err) {
			return err
		}
		if slept+delay > renameRetryBudget {
			return fmt.Errorf("%w (still in use after retrying; close Dolphin or any other program using %s and patch again)",
				err, newpath)
		}
		sleep(delay)
		slept += delay
		delay = min(delay*2, renameRetryMaxStep)
	}
}
