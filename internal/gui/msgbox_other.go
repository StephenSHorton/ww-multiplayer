//go:build !windows

package gui

// messageBox: elsewhere the relaunch's failure goes to stderr only (macOS
// relaunches through open, which reports its own failures).
func messageBox(title, text string) {}
