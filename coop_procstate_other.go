//go:build !unix && !windows

package main

import (
	"errors"
	"runtime"
)

// procStateSupported says procState can check a process on this OS.
const procStateSupported = false

// procState can't check a process on this OS, so a session marker's owner
// is unknown (coopMarker.state), which is taken as in use.
func procState(pid int) (alive bool, start string, err error) {
	return false, "", errors.New("this build can't check a process on " + runtime.GOOS)
}
