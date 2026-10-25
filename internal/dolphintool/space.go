package dolphintool

import "errors"

// errNoFreeSpace is FreeBytes' error where the system has no free-space
// query here.
var errNoFreeSpace = errors.New("free space unknown on this system")

// FreeBytes is the space free to this user on the volume holding dir.
func FreeBytes(dir string) (uint64, error) { return freeBytes(dir) }
