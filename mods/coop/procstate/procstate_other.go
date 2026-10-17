//go:build !unix && !windows

package procstate

import (
	"errors"
	"runtime"
)

// Supported says State can check a process on this OS.
const Supported = false

// State can't check a process on this OS, so a session marker's owner
// is unknown (mods/coop/session), which is taken as in use.
func State(pid int) (alive bool, start string, err error) {
	return false, "", errors.New("this build can't check a process on " + runtime.GOOS)
}
