//go:build windows

package launch

import (
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
)

func processes() []Process {
	pids, err := emu.ListPIDs()
	if err != nil {
		return nil
	}
	var out []Process
	for _, pid := range pids {
		exe := imagePath(pid)
		if exe == "" || len(dolphinExes([]string{exe})) == 0 {
			continue // DolphinTool, the updater, or gone already
		}
		p := Process{PID: pid, Exe: exe}
		if line, ok := commandLine(pid); ok {
			if args, err := windows.DecomposeCommandLine(line); err == nil {
				if len(args) > 0 {
					args = args[1:]
				}
				p.Args, p.ArgsKnown = args, true
			}
		}
		out = append(out, p)
	}
	return out
}

// commandLine reads process pid's command line with
// NtQueryInformationProcess(ProcessCommandLineInformation) (Windows 8.1
// and later), which needs only limited query access.
func commandLine(pid uint32) (string, bool) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", false
	}
	defer windows.CloseHandle(h)
	buf := make([]byte, 4096)
	for range 4 {
		var n uint32
		err := windows.NtQueryInformationProcess(h, windows.ProcessCommandLineInformation, unsafe.Pointer(&buf[0]), uint32(len(buf)), &n)
		if err == nil {
			us := (*windows.NTUnicodeString)(unsafe.Pointer(&buf[0]))
			if us.Buffer == nil || us.Length == 0 {
				return "", true
			}
			return utf16N(us.Buffer, int(us.Length/2)), true
		}
		if n <= uint32(len(buf)) {
			return "", false
		}
		buf = make([]byte, n)
	}
	return "", false
}

// utf16N decodes n UTF-16 units at p (the string is not NUL-terminated).
func utf16N(p *uint16, n int) string {
	return windows.UTF16ToString(unsafe.Slice(p, n))
}
