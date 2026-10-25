//go:build !windows && !darwin && !linux

package dolphintool

func freeBytes(string) (uint64, error) { return 0, errNoFreeSpace }
