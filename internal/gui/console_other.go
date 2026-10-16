//go:build !windows

package gui

// Only Windows gives a double-clicked console program a console window.

func hideConsole() bool { return false }
func showConsole()      {}
func freeConsole()      {}
