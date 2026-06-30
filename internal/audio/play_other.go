//go:build !windows && !darwin

package audio

import (
	"fmt"
	"os"
	"os/exec"
)

// playJoin tries the freedesktop "information" event sound.
func playJoin() {
	playSound("/usr/share/sounds/freedesktop/stereo/dialog-information.oga")
}

// playLeave tries the freedesktop "warning" event sound -- distinguishable
// from the join cue by ear.
func playLeave() {
	playSound("/usr/share/sounds/freedesktop/stereo/dialog-warning.oga")
}

// playSound is best-effort across the Linux/BSD landscape, where there's
// no single guaranteed audio CLI: try paplay, then canberra-gtk-play,
// then fall back to a terminal bell, then give up silently. Player
// processes are started (not waited on) so this never blocks the caller.
func playSound(path string) {
	if _, err := os.Stat(path); err == nil {
		if p, lookErr := exec.LookPath("paplay"); lookErr == nil {
			if exec.Command(p, path).Start() == nil {
				return
			}
		}
		if p, lookErr := exec.LookPath("canberra-gtk-play"); lookErr == nil {
			if exec.Command(p, "-f", path).Start() == nil {
				return
			}
		}
	}
	// No known player (or sound file) found -- a terminal bell is the
	// last resort before going fully silent.
	fmt.Fprint(os.Stderr, "\a")
}
