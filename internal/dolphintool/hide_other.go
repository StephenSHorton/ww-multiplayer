//go:build !windows

package dolphintool

import "os/exec"

func hideWindow(*exec.Cmd) {}
