//go:build !windows

package dolphinenv

import "github.com/StephenSHorton/ww-multiplayer/internal/dolphin"

// processes lists the running Dolphins by PID with the program
// runningExes would show; their command lines are not read here, so
// ArgsKnown is false (session.DecideLaunch then treats each as possibly this
// player's).
func processes() []Process {
	pids, err := dolphin.ListPIDs()
	if err != nil {
		return nil
	}
	out := make([]Process, 0, len(pids))
	for _, pid := range pids {
		out = append(out, Process{PID: pid})
	}
	return out
}
