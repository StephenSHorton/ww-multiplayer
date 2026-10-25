//go:build darwin

package launch

import (
	"os/exec"
	"strconv"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
)

// runningExes returns the executables of the running Dolphin processes
// (emu.ListPIDs, oldest first): `ps -o comm=` prints a process's full
// executable path on macOS, with no CGO and no extra rights.
func runningExes() []string {
	pids, err := emu.ListPIDs()
	if err != nil {
		return nil
	}
	var paths []string
	for _, pid := range pids {
		out, err := exec.Command("/bin/ps", "-o", "comm=", "-p", strconv.FormatUint(uint64(pid), 10)).Output()
		if err != nil {
			continue
		}
		if p := strings.TrimSpace(string(out)); p != "" {
			paths = append(paths, p)
		}
	}
	return dolphinExes(paths)
}

func installLocations() []string { return nil }

func userConfigPaths() []string { return nil }
