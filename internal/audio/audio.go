// Package audio plays short, non-disruptive system sounds from the
// ww-multiplayer host/join process (NOT through Dolphin) when a remote
// player connects or disconnects, so a Link appearing in-game isn't a
// silent surprise (issue #21).
//
// Platform-specific playback lives behind playJoin()/playLeave() in
// play_windows.go, play_darwin.go, and play_other.go (build-tag split,
// mirroring internal/dolphin's screenshot_windows.go / screenshot_other.go
// and memory_darwin.go pattern). This file only adds the WW_NO_AUDIO
// opt-out and the panic/blocking safety contract shared by every
// platform.
package audio

import (
	"os"
	"strconv"
	"strings"
)

// PlayJoin plays a short "someone connected" cue. Fire-and-forget: it
// kicks off playback asynchronously and returns immediately, and it
// never panics -- any platform-level failure (missing player binary,
// no audio device, etc.) is swallowed silently. There's no good way to
// surface "your join chime didn't play" without being more disruptive
// than the missing chime itself.
func PlayJoin() {
	if disabled() {
		return
	}
	safe(playJoin)
}

// PlayLeave plays a short "someone disconnected" cue. Same
// fire-and-forget, never-panics contract as PlayJoin.
func PlayLeave() {
	if disabled() {
		return
	}
	safe(playLeave)
}

// disabled reports whether WW_NO_AUDIO opts the user out of all chimes.
// Any value strconv.ParseBool accepts as true (1, t, T, TRUE, true,
// True) disables audio; an unset/empty var leaves audio on. Any other
// non-empty value (e.g. "yes") is treated as an explicit opt-out too --
// when in doubt, stay quiet rather than surprise someone who clearly
// tried to silence it.
func disabled() bool {
	v := strings.TrimSpace(os.Getenv("WW_NO_AUDIO"))
	if v == "" {
		return false
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return true
	}
	return b
}

// safe recovers from any panic inside fn so a misbehaving platform
// backend can never take down the host/join process over a sound cue.
func safe(fn func()) {
	defer func() { _ = recover() }()
	fn()
}
