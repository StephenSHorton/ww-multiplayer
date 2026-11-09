//go:build unix

package cardtag

import (
	"errors"
	"os"
	"syscall"
)

// tryLock takes an exclusive flock on path without waiting: busy when
// another process holds it. The lock goes with the process.
func tryLock(path string) (unlock func(), busy bool, err error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, true, nil
		}
		return nil, false, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, false, nil
}
