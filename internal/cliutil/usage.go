package cliutil

import (
	"fmt"
	"os"
	"runtime"
)

// ExeCmd is the binary name as the user types it on this platform —
// `.\name.exe` on Windows (PowerShell, the default Windows Terminal
// shell, won't run a program from the current folder by bare name, and
// cmd.exe accepts the .\ form too), "./name" everywhere else (the
// convention for binaries that aren't on $PATH).
func ExeCmd(name string) string {
	return exeCmd(name, runtime.GOOS)
}

func exeCmd(name, goos string) string {
	if goos == "windows" {
		return `.\` + name + ".exe"
	}
	return "./" + name
}

// ExitUsage reports a command-line mistake with the command's usage line
// (after exe, the binary as ExeCmd spells it) and exits non-zero.
func ExitUsage(exe string, err error, usage string) {
	fmt.Printf("ERROR: %v\n", err)
	fmt.Printf("Usage: %s %s\n", exe, usage)
	os.Exit(1)
}
