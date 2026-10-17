// Package clitest runs a binary's main in a child test process and
// compares what it prints with golden files: the harness of
// ww-multiplayer's CLI goldens (cli_golden_test.go at the root) and
// wwdev's (cmd/wwdev). Each test binary has its own helper test that
// calls its main (HelperArgs).
package clitest

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// helperEnv carries a child's arguments, separated by 0x1F.
const helperEnv = "WW_TEST_CLI_ARGS"

// HelperArgs returns the arguments a child process of Run is to run main
// with, and whether this process is such a child.
func HelperArgs() ([]string, bool) {
	args, ok := os.LookupEnv(helperEnv)
	if !ok {
		return nil, false
	}
	if args == "" { // Run with no arguments
		return nil, true
	}
	return strings.Split(args, "\x1f"), true
}

// Env is the environment of a child: the parent's without the variables
// the commands read, then env.
func Env(env map[string]string) []string {
	drop := map[string]bool{
		"DOLPHIN_EXE": true, "ISO_PATH": true, "USER_DIR_1": true, "USER_DIR_2": true,
		"SAVE_STATE": true, "SAVE_STATE_2": true, "APPDATA": true, "XDG_CONFIG_HOME": true,
		"HOME": true, "WW_COLOR": true, "WW_COOP_TABLES": true,
	}
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if !drop[strings.ToUpper(k)] {
			out = append(out, kv)
		}
	}
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return append(out, "GORACE="+strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
}

// Run runs the test binary's helper test (the one that calls main with
// HelperArgs) with args and env, and returns its stdout and exit code.
func Run(t *testing.T, helper string, env map[string]string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+helper+"$")
	cmd.Env = append(Env(env), helperEnv+"="+strings.Join(args, "\x1f"))
	out, err := cmd.Output()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0
	case errors.As(err, &exitErr):
		return string(out), exitErr.ExitCode()
	}
	t.Fatalf("running %q: %v", args, err)
	return "", 0
}

var seconds = regexp.MustCompile(`in [0-9]+\.[0-9] s,`)

// Normalize replaces exe (the binary as the user types it) by <exe>, root
// (a temp dir) by <TMP> in either slash form, and the co-op line's build
// time by "in N s".
func Normalize(out, root, exe string) string {
	// The binary as the user types it differs by platform (ExeCmd).
	out = strings.ReplaceAll(out, exe+" ", "<exe> ")
	// %q-quoted error texts double each backslash.
	for _, r := range []string{strings.ReplaceAll(root, `\`, `\\`), root, filepath.ToSlash(root)} {
		out = strings.ReplaceAll(out, r, "<TMP>")
	}
	out = strings.ReplaceAll(out, `\`, "/")
	return seconds.ReplaceAllString(out, "in N s,")
}

// CheckGolden compares one run with testdata/cli-golden/<name>.txt, or
// rewrites the file when update is set. When norm is not nil, both the
// run and the file pass through norm before they are compared.
func CheckGolden(t *testing.T, name, out string, code int, norm func(string) string, update bool) {
	t.Helper()
	got := fmt.Sprintf("exit %d\n%s", code, out)
	path := filepath.Join("testdata", "cli-golden", name+".txt")
	if update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (record it with -update-cli-golden)", err)
	}
	w := strings.ReplaceAll(string(want), "\r\n", "\n")
	if norm != nil {
		got, w = norm(got), norm(w)
	}
	if got != w {
		t.Errorf("%s: output changed\n--- got\n%s\n--- want\n%s", name, got, w)
	}
}

// WriteFile writes b to path, making its directory, and returns path.
func WriteFile(t *testing.T, path string, b []byte) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// ConfigEnv points every OS's user config dir at dir.
func ConfigEnv(dir string) map[string]string {
	return map[string]string{"APPDATA": dir, "XDG_CONFIG_HOME": dir, "HOME": dir}
}
