//go:build unix

package main

import (
	"bytes"
	"errors"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// procStateSupported says procState can check a process on this OS.
const procStateSupported = true

// procState reports whether process pid runs on this machine, and its
// start token for a session marker (coopMarker): on Linux the time it
// started, in clock ticks after boot; elsewhere "" (macOS and the other
// Unixes are checked by pid alone). A pid that is not positive names no
// process.
func procState(pid int) (alive bool, start string, err error) {
	if pid <= 0 {
		return false, "", nil
	}
	// Signal 0 checks the pid without signalling it. EPERM is a process
	// that runs as another user.
	switch err := syscall.Kill(pid, 0); {
	case errors.Is(err, syscall.ESRCH):
		return false, "", nil
	case err != nil && !errors.Is(err, syscall.EPERM):
		return false, "", err
	}
	if runtime.GOOS == "linux" {
		start = linuxStartTime(pid)
	}
	return true, start, nil
}

// linuxStartTime is the starttime field of /proc/<pid>/stat, or "" if it
// can't be read.
func linuxStartTime(pid int) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	return statStartTime(b)
}

// statStartTime is the starttime field (field 22, proc(5)) of b, a
// /proc/<pid>/stat, or "" if b has none. The fields are counted from the
// last ')': the command name before it may hold spaces and parentheses.
func statStartTime(b []byte) string {
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return ""
	}
	// The fields after ')' start at field 3, the state.
	f := strings.Fields(string(b[i+1:]))
	if len(f) < 20 {
		return ""
	}
	return f[19]
}
