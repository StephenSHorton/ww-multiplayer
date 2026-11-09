package launch

import (
	"fmt"
	"strings"
	"time"
)

// Process is one running Dolphin process, as Processes finds it.
type Process struct {
	PID uint32
	// Exe is its program ("" when it can't be read).
	Exe string
	// Args is its command line after the program; ArgsKnown says it
	// could be read (Windows and macOS).
	Args      []string
	ArgsKnown bool
}

// Processes lists the running Dolphins, oldest first (emu.ListPIDs'
// order). On Windows and macOS it reads each one's program and command
// line; elsewhere none run (the tool reads Dolphin's memory on Windows and
// macOS only).
func Processes() []Process { return processes() }

// UserDirArg returns the user folder a Dolphin command line names: -u
// <dir>, -u<dir>, --user <dir> or --user=<dir> (Dolphin's option), the
// last one given.
func UserDirArg(args []string) (string, bool) {
	dir, ok := "", false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-u" || a == "--user":
			if i+1 < len(args) {
				i++
				dir, ok = args[i], true
			}
		case strings.HasPrefix(a, "--user="):
			dir, ok = strings.TrimPrefix(a, "--user="), true
		case strings.HasPrefix(a, "-u") && len(a) > 2 && !strings.HasPrefix(a, "--"):
			dir, ok = a[2:], true
		}
	}
	return dir, ok
}

// ProcessUserDir returns the user folder Dolphin process p runs on: its
// -u argument, else the folder its program uses by default (UserDir: the
// portable User folder, or Dolphin's usual one). known is false when p's
// command line couldn't be read.
func ProcessUserDir(e Env, p Process) (dir string, known bool) {
	if !p.ArgsKnown {
		return "", false
	}
	if d, ok := UserDirArg(p.Args); ok && d != "" {
		return d, true
	}
	d, _ := UserDir(e, p.Exe)
	return d, true
}

// ISOArg returns the disc a Dolphin command line boots: -e <file>,
// -e<file>, --exec <file> or --exec=<file> (Dolphin's option), the last
// one given. A Dolphin started from its game list names none.
func ISOArg(args []string) (string, bool) {
	iso, ok := "", false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-e" || a == "--exec":
			if i+1 < len(args) {
				i++
				iso, ok = args[i], true
			}
		case strings.HasPrefix(a, "--exec="):
			iso, ok = strings.TrimPrefix(a, "--exec="), true
		case strings.HasPrefix(a, "-e") && len(a) > 2 && !strings.HasPrefix(a, "--"):
			iso, ok = a[2:], true
		}
	}
	return iso, ok
}

// HasLaunchArgs says a Dolphin command line is one Launch builds: an
// explicit user folder (-u) and disc (-e) and the MEM1 override, in
// any order. The app (this version or another) started that Dolphin, or
// a dev tool that launches the same way (wwdev dolphin2).
func HasLaunchArgs(args []string) bool {
	if d, ok := UserDirArg(args); !ok || d == "" {
		return false
	}
	if iso, ok := ISOArg(args); !ok || iso == "" {
		return false
	}
	for i := 0; i+1 < len(MEM1Override); i += 2 {
		found := false
		for j := 0; j+1 < len(args); j++ {
			if args[j] == MEM1Override[i] && args[j+1] == MEM1Override[i+1] {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Close ends Dolphin process pid at once, with no "stop emulation?"
// prompt: TerminateProcess on Windows; SIGTERM, then SIGKILL if it is
// still there after closeGrace, elsewhere. It refuses a pid Processes
// no longer lists, so a reused PID is never ended. The caller decides
// it may be closed (the app closes only a Dolphin it launched that isn't
// in game); WaitGone waits for it to leave.
func Close(pid uint32) error {
	listed := false
	for _, p := range Processes() {
		if p.PID == pid {
			listed = true
			break
		}
	}
	if !listed {
		return fmt.Errorf("dolphin pid %d is not running", pid)
	}
	return closeProcess(pid)
}

// closeGrace is how long Close waits after SIGTERM before SIGKILL.
const closeGrace = 3 * time.Second

// WaitGone waits up to d for Dolphin process pid to leave the process
// list (its files closed), polling; it says whether it did.
func WaitGone(pid uint32, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		gone := true
		for _, p := range Processes() {
			if p.PID == pid {
				gone = false
				break
			}
		}
		if gone {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
}
