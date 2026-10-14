//go:build !windows && !(darwin && cgo)

package dolphin

import (
	"errors"
	"runtime"
)

// errUnsupported is returned by every process-memory primitive on
// platforms without a backend (Linux, BSD, ...). Lets `go vet ./...` and
// `go test ./...` build on Linux CI; any code path that actually needs a
// live Dolphin fails fast with a clear message instead of a link error.
var errUnsupported = errors.New("dolphin process memory access is not implemented on " + runtime.GOOS)

type procHandle = uintptr

func zeroProcHandle() procHandle       { return 0 }
func procHandleZero(h procHandle) bool { return h == 0 }

func openProc(pid uint32) (procHandle, error)                 { return 0, errUnsupported }
func closeProc(h procHandle)                                  {}
func readProc(h procHandle, addr uintptr, buf []byte) error   { return errUnsupported }
func writeProc(h procHandle, addr uintptr, data []byte) error { return errUnsupported }
func walkRegions(h procHandle) ([]region, error)              { return nil, errUnsupported }
func listProcessesByName(name string) ([]uint32, error)       { return nil, errUnsupported }
