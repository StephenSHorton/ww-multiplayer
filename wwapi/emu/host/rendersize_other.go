//go:build !windows

package host

// RenderSize is the size of Dolphin pid's render surface; unknown off
// Windows (ok false).
func RenderSize(pid uint32) (w, h int, ok bool) { return 0, 0, false }

// ClientRectInWindow is hwnd's client area within a capture of it;
// unknown off Windows (ok false).
func ClientRectInWindow(hwnd uintptr) (x, y, w, h int, ok bool) { return 0, 0, 0, 0, false }
