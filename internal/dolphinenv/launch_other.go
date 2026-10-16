//go:build !darwin

package dolphinenv

import (
	"fmt"
	"os/exec"
)

func launch(exe string, args []string) (int, error) {
	cmd := exec.Command(exe, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	// Release disowns the child so we don't block on Wait() and the
	// process survives our exit. Without this Go would keep an OS
	// handle open until either Wait() or process termination.
	if err := cmd.Process.Release(); err != nil {
		return pid, fmt.Errorf("release: %w", err)
	}
	return pid, nil
}
