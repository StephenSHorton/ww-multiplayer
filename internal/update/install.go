package update

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// FS is the file operations an install makes; OS is the real one, and a
// test's fake records them.
type FS interface {
	Rename(from, to string) error
	Remove(path string) error
	RemoveAll(path string) error
	Stat(path string) (fs.FileInfo, error)
	Chmod(path string, mode fs.FileMode) error
}

// OS is the operating system's FS.
var OS FS = osFS{}

type osFS struct{}

func (osFS) Rename(from, to string) error              { return os.Rename(from, to) }
func (osFS) Remove(path string) error                  { return os.Remove(path) }
func (osFS) RemoveAll(path string) error               { return os.RemoveAll(path) }
func (osFS) Stat(path string) (fs.FileInfo, error)     { return os.Stat(path) }
func (osFS) Chmod(path string, mode fs.FileMode) error { return os.Chmod(path, mode) }

// OldSuffix names what an install moved aside: <exe>.old on Windows,
// <bundle>.old on macOS. The next start removes it (CleanupOld).
const OldSuffix = ".old"

// SwapExe puts the verified download newExe in place of the running
// program exe: any leftover <exe>.old is removed, exe is renamed to
// <exe>.old (Windows lets a running exe be renamed, not deleted or
// written), and newExe is renamed to exe. It returns where the old exe
// went (Restore puts it back). If that last rename fails, the old exe is
// put back, so the player is never left without one. A rename another
// program (antivirus, the search indexer) blocks for a moment is retried
// (renameRetry).
func SwapExe(fsys FS, exe, newExe string) (string, error) {
	old := exe + OldSuffix
	if err := fsys.Remove(old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		// Still running from an earlier update? Use a fresh name.
		old = fmt.Sprintf("%s.%d%s", exe, os.Getpid(), OldSuffix)
	}
	if err := fsys.Chmod(newExe, 0o755); err != nil {
		return "", fmt.Errorf("update: %w", err)
	}
	if err := renameRetry(fsys, exe, old); err != nil {
		return "", fmt.Errorf("update: can't move the running app aside: %w", err)
	}
	if err := renameRetry(fsys, newExe, exe); err != nil {
		if rerr := renameRetry(fsys, old, exe); rerr != nil {
			return "", fmt.Errorf("update: can't put the new version in place (%v), nor the old one back (%v): the old one is %s", err, rerr, old)
		}
		return "", fmt.Errorf("update: can't put the new version in place (the old one is back): %w", err)
	}
	return old, nil
}

// Restore puts the exe SwapExe moved aside (old) back over exe, the new
// version that didn't run.
func Restore(fsys FS, exe, old string) error {
	if err := renameRetry(fsys, old, exe); err != nil {
		return fmt.Errorf("update: can't put the old version back (it is %s): %w", old, err)
	}
	return nil
}

// RestoreMissing puts the old exe back when exe is gone (antivirus
// removed the new version, say): the newest of <exe>.old and
// <exe>.<pid>.old. It says whether it did.
func RestoreMissing(fsys FS, exe string) bool {
	if _, err := os.Lstat(exe); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	cands := []string{exe + OldSuffix}
	matches, _ := filepath.Glob(globEscape(exe) + ".*" + OldSuffix)
	cands = append(cands, matches...)
	best, bestT := "", time.Time{}
	for _, c := range cands {
		fi, err := os.Stat(c)
		if err == nil && fi.Mode().IsRegular() && (best == "" || fi.ModTime().After(bestT)) {
			best, bestT = c, fi.ModTime()
		}
	}
	return best != "" && renameRetry(fsys, best, exe) == nil
}

// Windows error codes for a rename another process blocks by holding one
// of the files open (antivirus, the search indexer, for a moment). Raw
// Errnos so this builds everywhere, as internal/inject's rename.go.
const (
	winErrorAccessDenied     = syscall.Errno(5)  // ERROR_ACCESS_DENIED
	winErrorSharingViolation = syscall.Errno(32) // ERROR_SHARING_VIOLATION
)

// The rename retry schedule: 5 ms doubling up to 250 ms, about 2 s in all
// (Go's cmd/go robustio; internal/inject's retryRename).
const (
	renameRetryFirst   = 5 * time.Millisecond
	renameRetryMaxStep = 250 * time.Millisecond
	renameRetryBudget  = 2 * time.Second
)

// renameSleep waits between rename retries; renameTransient says a failed
// rename may work a moment later (Windows only). Tests replace both.
var (
	renameSleep     = time.Sleep
	renameTransient = func(err error) bool {
		return runtime.GOOS == "windows" && isSharingError(err)
	}
)

func isSharingError(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && (errno == winErrorAccessDenied || errno == winErrorSharingViolation)
}

// renameRetry renames from to to, retrying a transient failure until the
// budget runs out; the last error is returned.
func renameRetry(fsys FS, from, to string) error {
	delay, slept := renameRetryFirst, time.Duration(0)
	for {
		err := fsys.Rename(from, to)
		if err == nil || !renameTransient(err) || slept+delay > renameRetryBudget {
			return err
		}
		renameSleep(delay)
		slept += delay
		delay = min(delay*2, renameRetryMaxStep)
	}
}

// BundleOf returns the macOS .app bundle exe runs from
// (<bundle>.app/Contents/MacOS/<exe>), or false when it isn't in one.
func BundleOf(exe string) (string, bool) {
	macos := filepath.Dir(exe)
	contents := filepath.Dir(macos)
	bundle := filepath.Dir(contents)
	if filepath.Base(macos) != "MacOS" || filepath.Base(contents) != "Contents" || !strings.HasSuffix(bundle, ".app") {
		return "", false
	}
	return bundle, true
}

// SwapBundle puts the extracted, verified newBundle (a directory next to
// bundle, so the renames stay on one volume) in place of bundle: bundle
// is renamed to <bundle>.old, newBundle to bundle, and the old one is
// removed (a running program's files may be unlinked on macOS; what is
// left is removed at the next start). A failed second rename puts the
// old bundle back.
func SwapBundle(fsys FS, bundle, newBundle string) error {
	old := bundle + OldSuffix
	if err := fsys.RemoveAll(old); err != nil {
		return fmt.Errorf("update: can't remove the leftover %s: %w", old, err)
	}
	if err := renameRetry(fsys, bundle, old); err != nil {
		return fmt.Errorf("update: can't move the app aside: %w", err)
	}
	if err := renameRetry(fsys, newBundle, bundle); err != nil {
		if rerr := renameRetry(fsys, old, bundle); rerr != nil {
			return fmt.Errorf("update: can't put the new version in place (%v), nor the old one back (%v): the old one is %s", err, rerr, old)
		}
		return fmt.Errorf("update: can't put the new version in place (the old one is back): %w", err)
	}
	_ = fsys.RemoveAll(old)
	return nil
}

// CleanupOld removes what an earlier install moved aside next to exe:
// <exe>.old and <exe>.<pid>.old on Windows, <bundle>.old on macOS, and
// what an install that was killed mid-way left there (TempPrefix
// downloads and, on macOS, unpacked folders, once over StaleAfter old: a
// newer one may be another instance's download in progress). Best
// effort: a file the old process still holds stays until the next start.
func CleanupOld(fsys FS, goos, exe string) {
	if goos == "darwin" {
		if b, ok := BundleOf(exe); ok {
			_ = fsys.RemoveAll(b + OldSuffix)
			cleanupTemps(fsys, filepath.Dir(b), true)
		}
		return
	}
	_ = fsys.Remove(exe + OldSuffix)
	matches, _ := filepath.Glob(globEscape(exe) + ".*" + OldSuffix)
	for _, m := range matches {
		_ = fsys.Remove(m)
	}
	cleanupTemps(fsys, filepath.Dir(exe), false)
}

// TempPrefix starts the names of an install's downloads (Download, *.tmp)
// and unpacked folders (Install on macOS) next to the app.
const TempPrefix = ".ww-update-"

// StaleAfter is how old a TempPrefix leftover is before CleanupOld
// removes it.
const StaleAfter = time.Hour

// cleanupTemps removes dir's stale TempPrefix downloads, and with dirs
// its stale unpacked folders too.
func cleanupTemps(fsys FS, dir string, dirs bool) {
	matches, _ := filepath.Glob(filepath.Join(globEscape(dir), TempPrefix+"*"))
	for _, m := range matches {
		fi, err := os.Lstat(m)
		if err != nil || time.Since(fi.ModTime()) < StaleAfter {
			continue
		}
		switch {
		case fi.Mode().IsRegular() && strings.HasSuffix(m, ".tmp"):
			_ = fsys.Remove(m)
		case dirs && fi.IsDir():
			_ = fsys.RemoveAll(m)
		}
	}
}

// globEscape escapes filepath.Match's metacharacters in a literal path.
func globEscape(p string) string {
	var b strings.Builder
	for _, r := range p {
		switch r {
		case '*', '?', '[':
			b.WriteRune('[')
			b.WriteRune(r)
			b.WriteRune(']')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// RelaunchCmd is the command that starts the updated app with args (the
// same command line as this run): the new exe itself, or on macOS the
// bundle through open -n (a new instance even while this one exits).
func RelaunchCmd(goos, exe string, args []string) *exec.Cmd {
	if goos == "darwin" {
		if b, ok := BundleOf(exe); ok {
			a := []string{"-n", b}
			if len(args) > 0 {
				a = append(append(a, "--args"), args...)
			}
			return exec.Command("open", a...)
		}
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir, _ = os.Getwd()
	return cmd
}

// SmokeTimeout bounds Smoke's run of the new version.
const SmokeTimeout = 15 * time.Second

// Smoke runs the program exe as `exe version` (with EnvNoCheck=1, so it
// asks no server, and without a console window) and checks it prints
// "ww-multiplayer <tag>": the new version is there and runs. It catches a
// download antivirus quarantined or blocked once it was in place (the
// builds are unsigned) while the old app is still open and can put
// itself back.
func Smoke(ctx context.Context, exe, tag string) error {
	ctx, cancel := context.WithTimeout(ctx, SmokeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "version")
	cmd.Env = append(os.Environ(), EnvNoCheck+"=1", EnvSmoke+"=1")
	cmd.Dir = filepath.Dir(exe)
	noWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("running %s: %w", filepath.Base(exe), err)
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	line = strings.TrimSpace(line)
	if want := "ww-multiplayer " + tag; line != want {
		return fmt.Errorf("%s says %q, not %q", filepath.Base(exe), line, want)
	}
	return nil
}
