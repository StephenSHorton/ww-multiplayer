//go:build unix

package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// TestSecondCtrlCForceQuits runs a helper process that installs
// cliMultiplayerContext and then hangs in "teardown" after the first
// SIGINT. The first SIGINT must cancel the ctx and print the force-quit
// hint; the second must kill the process outright instead of being
// swallowed by the handler.
func TestSecondCtrlCForceQuits(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestCliSignalHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), "WW_TEST_SIGNAL_HELPER=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	var seen []string
	waitLine := func(want string) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					t.Fatalf("helper exited before printing %q; output %q", want, seen)
				}
				seen = append(seen, l)
				if strings.Contains(l, want) {
					return
				}
			case <-deadline:
				t.Fatalf("helper never printed %q; output %q", want, seen)
			}
		}
	}

	waitLine("READY")
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	waitLine("press Ctrl+C again to force quit")
	waitLine("CANCELLED")

	// Teardown is now "stuck"; the second Ctrl+C must end the process.
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("helper exit = %v, want death by SIGINT", err)
		}
		if ws, ok := ee.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() || ws.Signal() != syscall.SIGINT {
			t.Fatalf("helper exit = %v, want death by SIGINT", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second SIGINT was swallowed: helper still running")
	}
}

// TestCliSignalHelper is the helper process for TestSecondCtrlCForceQuits;
// it does nothing in a normal test run.
func TestCliSignalHelper(t *testing.T) {
	if os.Getenv("WW_TEST_SIGNAL_HELPER") != "1" {
		t.Skip("helper process for TestSecondCtrlCForceQuits")
	}
	ctx, cancel := cliMultiplayerContext(log.Stdout{})
	defer cancel()
	fmt.Println("READY")
	<-ctx.Done()
	fmt.Println("CANCELLED")
	time.Sleep(time.Minute) // a teardown that never finishes
}
