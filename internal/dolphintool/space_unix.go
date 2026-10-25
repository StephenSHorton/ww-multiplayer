//go:build darwin || linux

package dolphintool

import "golang.org/x/sys/unix"

func freeBytes(dir string) (uint64, error) {
	var s unix.Statfs_t
	if err := unix.Statfs(dir, &s); err != nil {
		return 0, err
	}
	return uint64(s.Bavail) * uint64(s.Bsize), nil
}
