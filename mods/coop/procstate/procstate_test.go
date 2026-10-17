package procstate

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// testChild is the test binary, run with -test.run=run and with the
// variables in env set.
func testChild(run string, env ...string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run="+run)
	// Under -race a clean exit otherwise sleeps 1s for the detector.
	cmd.Env = append(append(os.Environ(), env...), "GORACE="+strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
	return cmd
}

// deadPid returns the pid of a child process that has exited and been
// waited for, which no process has unless the OS reuses it in the
// meantime.
func deadPid(t *testing.T) int {
	t.Helper()
	cmd := testChild("^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.ProcessState.Pid()
}

// TestProcState checks State against real processes: this one, a
// child that has exited, pids no process has, and pid 1 (Unix) or 4
// (Windows), which may run as another user.
func TestProcState(t *testing.T) {
	if !Supported {
		t.Skipf("State can't check a process on %s", runtime.GOOS)
	}
	alive, start, err := State(os.Getpid())
	if err != nil || !alive {
		t.Fatalf("this process: alive %v, %v", alive, err)
	}
	if (runtime.GOOS == "linux" || runtime.GOOS == "windows") && start == "" {
		t.Errorf("this process has no start token on %s", runtime.GOOS)
	}
	if again, start2, err := State(os.Getpid()); err != nil || !again || start2 != start {
		t.Errorf("this process again: alive %v, start %q, %v; want start %q", again, start2, err, start)
	}

	dead := deadPid(t)
	if alive, start, err := State(dead); err != nil || alive {
		t.Errorf("an exited child (pid %d): alive %v, start %q, %v", dead, alive, start, err)
	}
	for _, pid := range []int{0, -1} {
		if alive, _, err := State(pid); err != nil || alive {
			t.Errorf("pid %d: alive %v, %v", pid, alive, err)
		}
	}

	// A process this user may not signal (EPERM) runs. One this user may
	// not open (ERROR_ACCESS_DENIED) can't be checked, which the error
	// says. Pid 1 is init on Unix, and 4 is System on Windows; either may
	// also be open to this user.
	other := 1
	if runtime.GOOS == "windows" {
		other = 4
	}
	alive, _, err = State(other)
	if denied := runtime.GOOS == "windows" && err != nil && strings.Contains(err.Error(), "this user can't open"); !denied && (err != nil || !alive) {
		t.Errorf("pid %d: alive %v, %v", other, alive, err)
	}
}
