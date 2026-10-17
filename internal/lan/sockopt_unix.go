//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package lan

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// reuseAddr lets several listeners share the beacons' port (two apps on
// one machine both on Home); each hears every broadcast. BSDs (macOS)
// need SO_REUSEPORT for that, Linux SO_REUSEADDR.
func reuseAddr(_, _ string, c syscall.RawConn) error {
	var serr error
	err := c.Control(func(fd uintptr) {
		serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
		if serr == nil {
			serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
		}
	})
	if err != nil {
		return err
	}
	return serr
}
