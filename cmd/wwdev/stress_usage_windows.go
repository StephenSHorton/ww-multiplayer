//go:build windows

package main

import "syscall"

// processUsage is this process's CPU time in seconds; peak RSS isn't
// read on Windows (0), the Go heap numbers stand in.
func processUsage() (cpu, rssMaxMB float64) {
	h, err := syscall.GetCurrentProcess()
	if err != nil {
		return 0, 0
	}
	var c, e, k, u syscall.Filetime
	if syscall.GetProcessTimes(h, &c, &e, &k, &u) != nil {
		return 0, 0
	}
	t := func(f syscall.Filetime) float64 {
		return float64(uint64(f.HighDateTime)<<32|uint64(f.LowDateTime)) / 1e7
	}
	return t(k) + t(u), 0
}
