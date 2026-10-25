package dolphintool

import (
	"os/exec"
	"syscall"
)

// hideWindow keeps DolphinTool (a console program) from flashing a console
// window when the app, a GUI program, runs it.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
