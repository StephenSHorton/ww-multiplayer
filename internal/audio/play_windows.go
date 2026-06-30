//go:build windows

package audio

import (
	"syscall"
	"unsafe"
)

// winmm.dll PlaySoundW flags (mmsystem.h). SND_ASYNC returns immediately
// and plays on a separate system thread; SND_ALIAS resolves the name
// against the registered system-sound aliases (Control Panel > Sounds),
// the same names used by SystemAsterisk/SystemExclamation/etc.
// SND_NODEFAULT suppresses the fallback "default beep" if the user has
// that event muted, and SND_NOSTOP leaves any sound already playing
// alone instead of cutting it off.
const (
	sndAsync     = 0x0001
	sndNoDefault = 0x0002
	sndNoStop    = 0x0010
	sndAlias     = 0x00010000
)

var (
	winmm         = syscall.NewLazyDLL("winmm.dll")
	procPlaySound = winmm.NewProc("PlaySoundW")
)

// playJoin plays the SystemAsterisk alias -- a short, neutral "heads up"
// chime present on every stock Windows install.
func playJoin() {
	playAlias("SystemAsterisk")
}

// playLeave plays the SystemExclamation alias -- audibly distinct from
// the join chime without being alarming.
func playLeave() {
	playAlias("SystemExclamation")
}

// playAlias calls PlaySoundW(alias, NULL, SND_ALIAS|SND_ASYNC|...). No
// cgo: syscall + a lazily-loaded winmm.dll, matching the pattern already
// used for user32/gdi32 in internal/dolphin/screenshot_windows.go and
// kernel32 in memory_windows.go.
func playAlias(alias string) {
	name, err := syscall.UTF16PtrFromString(alias)
	if err != nil {
		return
	}
	procPlaySound.Call(
		uintptr(unsafe.Pointer(name)),
		0,
		uintptr(sndAsync|sndAlias|sndNoDefault|sndNoStop),
	)
}
