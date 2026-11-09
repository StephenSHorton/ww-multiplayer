//go:build windows

package launch

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// closeProcess ends process pid with TerminateProcess and waits a little
// for it to go.
func closeProcess(pid uint32) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return fmt.Errorf("open dolphin pid %d: %w", pid, err)
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil {
		return fmt.Errorf("end dolphin pid %d: %w", pid, err)
	}
	windows.WaitForSingleObject(h, 5000)
	return nil
}
