//go:build !windows

package update

import "os/exec"

// noWindow: only Windows opens a console window for a console program.
func noWindow(*exec.Cmd) {}
