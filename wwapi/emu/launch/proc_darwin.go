//go:build darwin

package launch

import (
	"bytes"
	"encoding/binary"

	"golang.org/x/sys/unix"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
)

// processes lists the running Dolphins with their program and command
// line, read with sysctl kern.procargs2 (allowed for the user's own
// processes). Without the command line, session.DecideLaunch counts every
// running Dolphin as possibly this player's, so the app would attach to
// another player's Dolphin (one started with its own -u) instead of
// launching its own; seen live on a shared Mac (wind#13).
func processes() []Process {
	pids, err := emu.ListPIDs()
	if err != nil {
		return nil
	}
	out := make([]Process, 0, len(pids))
	for _, pid := range pids {
		p := Process{PID: pid}
		if buf, err := unix.SysctlRaw("kern.procargs2", int(pid)); err == nil {
			if exe, args, ok := parseProcArgs2(buf); ok {
				p.Exe, p.Args, p.ArgsKnown = exe, args, true
			}
		}
		out = append(out, p)
	}
	return out
}

// parseProcArgs2 reads a kern.procargs2 buffer: argc (int32, host order),
// the executable path, NUL padding, then argc NUL-terminated strings
// (argv[0] first) and the environment. It returns the path and argv[1:].
func parseProcArgs2(buf []byte) (exe string, args []string, ok bool) {
	if len(buf) < 4 {
		return "", nil, false
	}
	argc := int(int32(binary.LittleEndian.Uint32(buf[:4])))
	rest := buf[4:]
	i := bytes.IndexByte(rest, 0)
	if i < 0 || argc < 1 {
		return "", nil, false
	}
	exe = string(rest[:i])
	rest = rest[i:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	argv := make([]string, 0, argc)
	for len(argv) < argc {
		j := bytes.IndexByte(rest, 0)
		if j < 0 {
			return "", nil, false
		}
		argv = append(argv, string(rest[:j]))
		rest = rest[j+1:]
	}
	return exe, argv[1:], true
}
