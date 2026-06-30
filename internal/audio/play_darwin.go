//go:build darwin

package audio

import "os/exec"

// playJoin shells out to afplay with a short stock macOS system sound.
func playJoin() {
	playSound("/System/Library/Sounds/Glass.aiff")
}

// playLeave uses a different stock sound so join/leave are distinguishable
// by ear.
func playLeave() {
	playSound("/System/Library/Sounds/Funk.aiff")
}

// playSound starts afplay without waiting for it to finish, so
// PlayJoin/PlayLeave return immediately. If afplay isn't on PATH (or the
// sound file is missing), Start's error is ignored -- best-effort, never
// fatal.
func playSound(path string) {
	cmd := exec.Command("afplay", path)
	_ = cmd.Start()
}
