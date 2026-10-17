//go:build !windows && !darwin

package launch

// runningExes: the tool reads Dolphin's memory on Windows and macOS only
// (wwapi/emu), so elsewhere no running Dolphin is offered.
func runningExes() []string { return nil }

func installLocations() []string { return nil }
