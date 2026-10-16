package gui

import (
	"syscall"
	"unsafe"
)

// The exe stays a console-subsystem program (design §4.3, owner decision),
// so the CLI keeps printing and reading stdin in a terminal. Double-clicked
// from Explorer, Windows gives it a console window of its own; the GUI hides
// that window while the app window starts and frees the console once the
// window is up. From a terminal the console is shared with the shell
// (GetConsoleProcessList counts more than one process) and is left alone.

var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	user32                    = syscall.NewLazyDLL("user32.dll")
	procGetConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
	procGetConsoleWindow      = kernel32.NewProc("GetConsoleWindow")
	procFreeConsole           = kernel32.NewProc("FreeConsole")
	procShowWindow            = user32.NewProc("ShowWindow")
)

const (
	swHide = 0
	swShow = 5
)

// ownConsole reports whether this process is the only one on its console,
// i.e. Windows made the console for it (a double-click).
func ownConsole() bool {
	var pids [2]uint32
	n, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	return n == 1
}

func consoleWindow() uintptr {
	h, _, _ := procGetConsoleWindow.Call()
	return h
}

// hideConsole hides the console window when the process owns it, and
// returns whether it did.
func hideConsole() bool {
	if !ownConsole() {
		return false
	}
	h := consoleWindow()
	if h == 0 {
		return false
	}
	procShowWindow.Call(h, swHide)
	return true
}

// showConsole shows the console window again (the window couldn't open
// and the TUI takes over in the console).
func showConsole() {
	if h := consoleWindow(); h != 0 {
		procShowWindow.Call(h, swShow)
	}
}

// freeConsole detaches from the console; Windows closes the console window
// once its last process has left.
func freeConsole() { procFreeConsole.Call() }
