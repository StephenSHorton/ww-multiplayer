//go:build !windows

package main

import (
	"runtime"
	"syscall"
)

// processUsage is this process's CPU time in seconds and its peak RSS in
// MB (getrusage: bytes on macOS, KB on Linux).
func processUsage() (cpu, rssMaxMB float64) {
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) != nil {
		return 0, 0
	}
	cpu = float64(ru.Utime.Sec+ru.Stime.Sec) + float64(ru.Utime.Usec+ru.Stime.Usec)/1e6
	rss := float64(ru.Maxrss)
	if runtime.GOOS != "darwin" {
		rss *= 1024
	}
	return cpu, mb(uint64(rss))
}
