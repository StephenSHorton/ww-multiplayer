//go:build !windows

package launch

import (
	"fmt"
	"syscall"
	"time"
)

// closeProcess sends process pid SIGTERM (Dolphin quits), then SIGKILL if
// it is still running after closeGrace.
func closeProcess(pid uint32) error {
	if err := syscall.Kill(int(pid), syscall.SIGTERM); err != nil {
		return fmt.Errorf("end dolphin pid %d: %w", pid, err)
	}
	deadline := time.Now().Add(closeGrace)
	for time.Now().Before(deadline) {
		if syscall.Kill(int(pid), 0) != nil {
			return nil // gone
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := syscall.Kill(int(pid), syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return fmt.Errorf("kill dolphin pid %d: %w", pid, err)
	}
	return nil
}
