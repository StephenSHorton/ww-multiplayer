//go:build !windows

package dolphin

import (
	"fmt"
	"image"
	"runtime"
)

// Window/screenshot capture is Windows-only for now — the Go side runs
// against Dolphin's emulated PPC RAM on every platform, but only Win32
// PrintWindow + DIB sections give us pixel-perfect capture without
// shelling out to platform-specific tooling. macOS would use
// CGWindowListCreateImage; Linux varies by compositor. We can fill
// those in when the demand exists.

func FindWindowByPID(pid uint32) (uintptr, error) {
	return 0, fmt.Errorf("FindWindowByPID is not implemented on %s", runtime.GOOS)
}

func WindowTitle(hwnd uintptr) string { return "" }

func CaptureWindow(hwnd uintptr) (*image.RGBA, error) {
	return nil, fmt.Errorf("CaptureWindow is not implemented on %s", runtime.GOOS)
}
