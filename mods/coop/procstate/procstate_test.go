package main

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// coTestChild is the test binary, run with -test.run=run and with the
// variables in env set.
func coTestChild(run string, env ...string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run="+run)
	// Under -race a clean exit otherwise sleeps 1s for the detector.
	cmd.Env = append(append(os.Environ(), env...), "GORACE="+strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
	return cmd
}

// coDeadPid returns the pid of a child process that has exited and been
// waited for, which no process has unless the OS reuses it in the
// meantime.
func coDeadPid(t *testing.T) int {
	t.Helper()
	cmd := coTestChild("^$")
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

	// A process this user may not signal (EPERM) runs. One this user may
	// not open (ERROR_ACCESS_DENIED) can't be checked, which the error
	// says. Pid 1 is init on Unix, and 4 is System on Windows; either may
	// also be open to this user.
	other := 1
	if runtime.GOOS == "windows" {
		other = 4
	}
	alive, _, err = procState(other)
	if denied := runtime.GOOS == "windows" && err != nil && strings.Contains(err.Error(), "this user can't open"); !denied && (err != nil || !alive) {
		t.Errorf("pid %d: alive %v, %v", other, alive, err)
	}
}

// liveChildDir is the directory TestProcStateLiveChild's child marks.
const liveChildDir = "WW_TEST_LIVE_CHILD_DIR"

// TestProcStateLiveChild checks the real procState across processes, as
// coop-world block in one terminal checks the host in another: a marker
// a running child wrote reads in use, by its start token on Linux and
// Windows, and stale once the child has exited.
func TestProcStateLiveChild(t *testing.T) {
	if dir, ok := os.LookupEnv(liveChildDir); ok {
		// The child: mark dir, say so, and run until stdin closes.
		if _, _, err := writeCoopMarker(dir); err != nil {
			t.Fatal(err)
		}
		os.Stdout.WriteString("ready\n")
		io.Copy(io.Discard, os.Stdin)
		os.Exit(0) // keep the testing package's "PASS" off stdout
	}
	if !procStateSupported {
		t.Skipf("procState can't check a process on %s", runtime.GOOS)
	}
	dir := t.TempDir()
	cmd := coTestChild("^TestProcStateLiveChild$", liveChildDir+"="+dir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })
	out := bufio.NewReader(stdout)
	if line, err := out.ReadString('\n'); line != "ready\n" {
		rest, _ := io.ReadAll(out)
		t.Fatalf("the child said %q, %v:\n%s", line, err, rest)
	}

	m, ok := readCoopMarker(dir)
	if !ok || m.pid != cmd.Process.Pid {
		t.Fatalf("the child's marker %q (pid %d), %v; want pid %d", m, m.pid, ok, cmd.Process.Pid)
	}
	use, why := m.state()
	if runtime.GOOS == "linux" || runtime.GOOS == "windows" {
		if m.start == "" || use != markerLive || why != "" {
			t.Errorf("the running child's marker %q (start %q): %v (%q), want markerLive by its start token", m, m.start, use, why)
		}
	} else if use != markerLive {
		t.Errorf("the running child's marker %q: %v (%q), want markerLive", m, use, why)
	}

	stdin.Close()
	if rest, err := io.ReadAll(out); err != nil || len(rest) != 0 {
		t.Errorf("the child said %q, %v", rest, err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if use, why := m.state(); use != markerStale {
		t.Errorf("the exited child's marker %q (start %q): %v (%q), want markerStale", m, m.start, use, why)
	}
}
