package update

import (
	"os/exec"
	"syscall"
)

// noWindow keeps a console program (Smoke's `version` run) from opening a
// console window: the app's own console is hidden or gone.
func noWindow(cmd *exec.Cmd) {
	const createNoWindow = 0x08000000 // CREATE_NO_WINDOW
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
