package coopsession

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/procstate"
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

// coSetProcState makes ProcState f until t ends.
func coSetProcState(t *testing.T, f func(pid int) (bool, string, error)) {
	t.Helper()
	old := ProcState
	ProcState = f
	t.Cleanup(func() { ProcState = old })
}

// coProcs is a ProcState that sees only the processes in procs, by pid
// with their start tokens.
func coProcs(procs map[int]string) func(int) (bool, string, error) {
	return func(pid int) (bool, string, error) {
		start, ok := procs[pid]
		return ok, start, nil
	}
}

// liveChildDir is the directory TestProcStateLiveChild's child marks.
const liveChildDir = "WW_TEST_LIVE_CHILD_DIR"

// TestProcStateLiveChild checks the real procstate.State across processes, as
// coop-world block in one terminal checks the host in another: a marker
// a running child wrote reads in use, by its start token on Linux and
// Windows, and stale once the child has exited.
func TestProcStateLiveChild(t *testing.T) {
	if dir, ok := os.LookupEnv(liveChildDir); ok {
		// The child: mark dir, say so, and run until stdin closes.
		if _, _, err := WriteMarker(dir); err != nil {
			t.Fatal(err)
		}
		os.Stdout.WriteString("ready\n")
		io.Copy(io.Discard, os.Stdin)
		os.Exit(0) // keep the testing package's "PASS" off stdout
	}
	if !procstate.Supported {
		t.Skipf("procstate.State can't check a process on %s", runtime.GOOS)
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

	m, ok := ReadMarker(dir)
	if !ok || m.pid != cmd.Process.Pid {
		t.Fatalf("the child's marker %q (pid %d), %v; want pid %d", m, m.pid, ok, cmd.Process.Pid)
	}
	use, why := m.State()
	if runtime.GOOS == "linux" || runtime.GOOS == "windows" {
		if m.start == "" || use != MarkerLive || why != "" {
			t.Errorf("the running child's marker %q (start %q): %v (%q), want MarkerLive by its start token", m, m.start, use, why)
		}
	} else if use != MarkerLive {
		t.Errorf("the running child's marker %q: %v (%q), want MarkerLive", m, use, why)
	}

	stdin.Close()
	if rest, err := io.ReadAll(out); err != nil || len(rest) != 0 {
		t.Errorf("the child said %q, %v", rest, err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if use, why := m.State(); use != MarkerStale {
		t.Errorf("the exited child's marker %q (start %q): %v (%q), want MarkerStale", m, m.start, use, why)
	}
}
