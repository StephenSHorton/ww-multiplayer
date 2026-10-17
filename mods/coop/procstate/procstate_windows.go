//go:build windows

package main

import (
	"errors"
	"fmt"
	"strconv"
	"syscall"
)

// procStateSupported says procState can check a process on this OS.
const procStateSupported = true

const (
	processQueryLimitedInfo = 0x1000            // PROCESS_QUERY_LIMITED_INFORMATION
	errorInvalidParameter   = syscall.Errno(87) // ERROR_INVALID_PARAMETER
)

// procState reports whether process pid runs on this machine, and its
// start token for a session marker (coopMarker): the time it was created
// (GetProcessTimes), in 100 ns units since 1601. A process this user may
// not open can't be checked, which the error says: after a restart, the
// pid most likely belongs to another program (a service, say), not the
// host. A pid that is not positive names no process.
func procState(pid int) (alive bool, start string, err error) {
	if pid <= 0 {
		return false, "", nil
	}
	h, err := syscall.OpenProcess(processQueryLimitedInfo|syscall.SYNCHRONIZE, false, uint32(pid))
	switch {
	case errors.Is(err, errorInvalidParameter): // no process has the pid
		return false, "", nil
	case errors.Is(err, syscall.ERROR_ACCESS_DENIED):
		return false, "", fmt.Errorf("pid %d belongs to a process this user can't open, which after a restart is most likely another program", pid)
	case err != nil:
		return false, "", fmt.Errorf("OpenProcess: %w", err)
	}
	defer syscall.CloseHandle(h)
	// A process that has exited keeps its pid while a handle to it is
	// open; its handle is then signalled.
	switch ev, err := syscall.WaitForSingleObject(h, 0); {
	case err != nil:
		return false, "", fmt.Errorf("WaitForSingleObject: %w", err)
	case ev == syscall.WAIT_OBJECT_0:
		return false, "", nil
	case ev != syscall.WAIT_TIMEOUT:
		return false, "", fmt.Errorf("WaitForSingleObject: result 0x%X", ev)
	}
	var created, exited, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return true, "", nil
	}
	return true, strconv.FormatUint(uint64(created.HighDateTime)<<32|uint64(created.LowDateTime), 10), nil
}
