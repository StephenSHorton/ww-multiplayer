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

// Hotkey VK constants stubbed so callers compile on non-Windows.
// Values match Windows VK_* so cross-platform code referencing them
// remains source-compatible.
const (
	VKShift = 0x10
	VKCtrl  = 0x11
	VKAlt   = 0x12
	VKF1    = 0x70
	VKF2    = 0x71
	VKF3    = 0x72
)

func SendKeyChord(hwnd uintptr, modifier, key uint32) error {
	return fmt.Errorf("SendKeyChord is not implemented on %s", runtime.GOOS)
}

func SendKey(hwnd uintptr, key uint32) error {
	return fmt.Errorf("SendKey is not implemented on %s", runtime.GOOS)
}

func SendChordToFocusedWindow(modifier, key uint32) error {
	return fmt.Errorf("SendChordToFocusedWindow is not implemented on %s", runtime.GOOS)
}

func GetFocusedWindow() uintptr { return 0 }

func ForegroundWindow(hwnd uintptr) {}
