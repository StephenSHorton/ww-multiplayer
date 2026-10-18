package gui

import (
	"syscall"
	"unsafe"
)

var procMessageBoxW = user32.NewProc("MessageBoxW")

// messageBox shows a native message box (an update's relaunch failing
// after the window closed, when no console shows a line).
func messageBox(title, text string) {
	t, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	m, err := syscall.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	const mbOK, mbIconWarning, mbSetForeground = 0x0, 0x30, 0x10000
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), mbOK|mbIconWarning|mbSetForeground)
}
