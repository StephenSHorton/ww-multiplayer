package main

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// coDeadPid returns the pid of a child process that has exited and been
// waited for, which no process has unless the OS reuses it in the
// meantime.
func coDeadPid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	// Under -race a clean exit otherwise sleeps 1s for the detector.
	cmd.Env = append(os.Environ(), "GORACE="+strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.ProcessState.Pid()
}

// coSetProcState makes coopProcState f until t ends.
func coSetProcState(t *testing.T, f func(pid int) (bool, string, error)) {
	t.Helper()
	old := coopProcState
	coopProcState = f
	t.Cleanup(func() { coopProcState = old })
}

// coProcs is a coopProcState that sees only the processes in procs, by pid
// with their start tokens.
func coProcs(procs map[int]string) func(int) (bool, string, error) {
	return func(pid int) (bool, string, error) {
		start, ok := procs[pid]
		return ok, start, nil
	}
}

// TestProcState checks procState against real processes: this one, a
// child that has exited, pids no process has, and pid 1 (Unix) or 4
// (Windows), which may run as another user.
func TestProcState(t *testing.T) {
	if !procStateSupported {
		t.Skipf("procState can't check a process on %s", runtime.GOOS)
	}
	alive, start, err := procState(os.Getpid())
	if err != nil || !alive {
		t.Fatalf("this process: alive %v, %v", alive, err)
	}
	if (runtime.GOOS == "linux" || runtime.GOOS == "windows") && start == "" {
		t.Errorf("this process has no start token on %s", runtime.GOOS)
	}
	if again, start2, err := procState(os.Getpid()); err != nil || !again || start2 != start {
		t.Errorf("this process again: alive %v, start %q, %v; want start %q", again, start2, err, start)
	}

	dead := coDeadPid(t)
	if alive, start, err := procState(dead); err != nil || alive {
		t.Errorf("an exited child (pid %d): alive %v, start %q, %v", dead, alive, start, err)
	}
	for _, pid := range []int{0, -1} {
		if alive, _, err := procState(pid); err != nil || alive {
			t.Errorf("pid %d: alive %v, %v", pid, alive, err)
		}
	}

	// A process this user may not signal (EPERM) or open
	// (ERROR_ACCESS_DENIED) runs. Pid 1 is init on Unix, and 4 is System
	// on Windows; either may also be open to this user.
	other := 1
	if runtime.GOOS == "windows" {
		other = 4
	}
	if alive, _, err := procState(other); err != nil || !alive {
		t.Errorf("pid %d: alive %v, %v", other, alive, err)
	}
}
