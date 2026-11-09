//go:build windows

package host

import (
	"sync"
	"syscall"
	"unsafe"
)

var procGetClientRect = user32.NewProc("GetClientRect")

func clientSize(hwnd uintptr) (w, h int) {
	var r rect
	if ok, _, _ := procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ok == 0 {
		return 0, 0
	}
	return int(r.Right - r.Left), int(r.Bottom - r.Top)
}

// RenderSize is the size in pixels of the surface Dolphin pid draws the
// game on: the client area of its game window (FindWindowByPID), or, when
// the game renders inside the main window, its largest visible child
// window (Qt's native render widget) so the menu, tool and status bars
// don't count. ok is false when no window is found.
func RenderSize(pid uint32) (w, h int, ok bool) {
	hwnd, err := FindWindowByPID(pid)
	if err != nil {
		return 0, 0, false
	}
	w, h = clientSize(hwnd)
	if w <= 0 || h <= 0 {
		return 0, 0, false
	}
	childMu.Lock()
	childW, childH = 0, 0
	procEnumChildWindows.Call(hwnd, childCB, 0)
	bw, bh := childW, childH
	childMu.Unlock()
	// A child that fills most of the window is the render widget.
	if bw > 0 && bh > 0 && bw*bh*2 >= w*h {
		return bw, bh, true
	}
	return w, h, true
}

// RenderSize's EnumChildWindows callback, made once (see findWindowCB);
// childMu guards the largest child it found.
var (
	childMu        sync.Mutex
	childW, childH int
	childCB        = syscall.NewCallback(func(child uintptr, _ uintptr) uintptr {
		if v, _, _ := procIsWindowVisible.Call(child); v == 0 {
			return 1
		}
		if cw, ch := clientSize(child); cw*ch > childW*childH {
			childW, childH = cw, ch
		}
		return 1
	})
)

var procClientToScreen = user32.NewProc("ClientToScreen")

// ClientRectInWindow is hwnd's client area within a CaptureWindow image
// of it (whose origin is the window rect's top-left): the picture without
// the title bar and borders. ok is false when a call fails.
func ClientRectInWindow(hwnd uintptr) (x, y, w, h int, ok bool) {
	var wr rect
	if r, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&wr))); r == 0 {
		return 0, 0, 0, 0, false
	}
	w, h = clientSize(hwnd)
	if w <= 0 || h <= 0 {
		return 0, 0, 0, 0, false
	}
	var p struct{ X, Y int32 }
	if r, _, _ := procClientToScreen.Call(hwnd, uintptr(unsafe.Pointer(&p))); r == 0 {
		return 0, 0, 0, 0, false
	}
	return int(p.X - wr.Left), int(p.Y - wr.Top), w, h, true
}
