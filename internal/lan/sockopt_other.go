//go:build !windows && !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package lan

import "syscall"

// reuseAddr does nothing here: one listener per machine.
func reuseAddr(_, _ string, _ syscall.RawConn) error { return nil }
