//go:build !windows && !darwin

package dolphinenv

// runningExes: the tool reads Dolphin's memory on Windows and macOS only
// (internal/dolphin), so elsewhere no running Dolphin is offered.
func runningExes() []string { return nil }

func installLocations() []string { return nil }
