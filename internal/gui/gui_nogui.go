//go:build !((windows || darwin) && desktop && production)

package gui

import (
	"fmt"
	"runtime"
)

// Available reports whether this build has the desktop window.
func Available() bool { return false }

// Run returns ErrNoGUI: this build has no window (see the package comment).
func Run(Options) error {
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		return fmt.Errorf("%w on %s", ErrNoGUI, runtime.GOOS)
	}
	return fmt.Errorf("%w (build with -tags desktop,production for it)", ErrNoGUI)
}
