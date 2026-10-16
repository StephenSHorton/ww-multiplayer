package dolphinenv

import "strings"

// Process is one running Dolphin process, as Processes finds it.
type Process struct {
	PID uint32
	// Exe is its program ("" when it can't be read).
	Exe string
	// Args is its command line after the program; ArgsKnown says it
	// could be read (Windows only, for now).
	Args      []string
	ArgsKnown bool
}

// Processes lists the running Dolphins, oldest first (dolphin.ListPIDs'
// order). On Windows it reads each one's program and command line; on
// macOS its program; elsewhere none run (the tool reads Dolphin's memory
// on Windows and macOS only).
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
