package dolphinenv

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/report"
)

// macOS: why a prepared copy (#16, scripts/setup-mac-dolphin.sh).
//
// Stock Dolphin.app is signed with the hardened runtime and the JIT
// entitlement. AMFI refuses task_for_pid against a hardened-runtime process
// unless the caller has com.apple.security.cs.debugger trusted by Apple, so
// the tool can't read Dolphin's emulated RAM. Re-signing Dolphin without the
// hardened runtime fixes that, but /Applications/Dolphin.app can't be
// re-signed in place (its com.apple.provenance xattr is a Gatekeeper lock),
// so PrepareMac copies it to ~/Applications, where xattrs are
// user-writable, and re-signs the copy. The original stays untouched and
// keeps updating.

const (
	// MacApp is where Dolphin installs; MacPreparedApp (under the home
	// directory) is the copy PrepareMac makes.
	MacApp         = "/Applications/Dolphin.app"
	macPreparedRel = "Applications/Dolphin.app"
	// MacBundleID is the prepared copy's CFBundleIdentifier. Both bundles
	// share org.dolphin-emu.dolphin upstream, so LaunchServices treats
	// them as one app and silently re-routes a launch of the copy to
	// whichever it registered as canonical (typically /Applications),
	// whose hardened binary AMFI then protects. A distinct ID breaks the
	// tie.
	MacBundleID = "org.dolphin-emu.dolphin-unhardened"

	lsregister = "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"
)

// MacPreparedApp is the prepared copy's bundle under home.
func MacPreparedApp(home string) string { return filepath.Join(home, macPreparedRel) }

// Runner runs a program and returns its combined output. PrepareMac runs
// cp, xattr, plutil, codesign and lsregister through it; a test passes a
// fake.
type Runner interface {
	Run(name string, args ...string) ([]byte, error)
}

// ExecRunner runs programs with os/exec.
type ExecRunner struct{}

func (ExecRunner) Run(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// ErrNotMac is PrepareMacHere's error on any OS but macOS.
var ErrNotMac = errors.New("preparing Dolphin is a macOS step")

// PrepareMacHere prepares ~/Applications/Dolphin.app from
// /Applications/Dolphin.app on this Mac.
func PrepareMacHere(rep report.Reporter) error {
	if runtime.GOOS != "darwin" {
		return ErrNotMac
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return PrepareMac(rep, ExecRunner{}, MacApp, MacPreparedApp(home))
}

// PrepareMac runs scripts/setup-mac-dolphin.sh's steps, logging each as
// the script prints it: copy src (a Dolphin.app) to dst (replacing an
// older copy), strip its xattrs, give it MacBundleID, re-sign its main
// executable ad hoc without the hardened runtime, register it with
// LaunchServices, and verify the signature and bundle ID. No step needs
// sudo. dst must be another path ending in ".app": it is removed first.
func PrepareMac(rep report.Reporter, r Runner, src, dst string) error {
	if filepath.Ext(dst) != ".app" || filepath.Clean(dst) == filepath.Clean(src) {
		return fmt.Errorf("prepare Dolphin: the copy %q must be another .app than %q", dst, src)
	}
	if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
		return fmt.Errorf("%s not found. Install Dolphin first", src)
	}
	step := func(format string, a ...any) { report.Logf(rep, report.Info, "==> "+format, a...) }
	run := func(name string, args ...string) error {
		out, err := r.Run(name, args...)
		if err != nil {
			if s := strings.TrimSpace(string(out)); s != "" {
				return fmt.Errorf("%s: %w: %s", name, err, s)
			}
			return fmt.Errorf("%s: %w", name, err)
		}
		return nil
	}
	if _, err := os.Stat(dst); err == nil {
		step("Removing existing %s...", dst)
		if err := os.RemoveAll(dst); err != nil {
			return err
		}
	}
	step("Copying %s -> %s...", src, dst)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := run("cp", "-R", src, dst); err != nil {
		return err
	}
	step("Stripping com.apple.provenance and other locking xattrs...")
	_ = run("xattr", "-cr", dst) // best effort, as in the script
	plist := filepath.Join(dst, "Contents", "Info.plist")
	bin := filepath.Join(dst, "Contents", "MacOS", "Dolphin")
	step("Setting CFBundleIdentifier to %s (avoids LaunchServices tie with /Applications)...", MacBundleID)
	if err := run("plutil", "-replace", "CFBundleIdentifier", "-string", MacBundleID, plist); err != nil {
		return err
	}
	step("Re-signing main Dolphin executable without hardened runtime...")
	if err := run("codesign", "--force", "--sign", "-", bin); err != nil {
		return err
	}
	step("Registering with LaunchServices...")
	_ = run(lsregister, "-f", dst) // best effort, as in the script

	step("Verifying...")
	sig, _ := r.Run("codesign", "-d", "-vvv", bin)
	flags := CodesignFlags(string(sig))
	report.Logf(rep, report.Info, "    %s", flags)
	if HasHardenedRuntime(string(sig)) {
		return errors.New("hardened runtime still set — re-sign failed")
	}
	id, err := r.Run("plutil", "-extract", "CFBundleIdentifier", "raw", plist)
	if err != nil {
		return fmt.Errorf("plutil: %w", err)
	}
	if got := strings.TrimSpace(string(id)); got != MacBundleID {
		return fmt.Errorf("bundle ID is %s, expected %s", got, MacBundleID)
	}
	report.Logf(rep, report.OK, "Done. The prepared Dolphin is %s; the original %s is unchanged.", dst, src)
	return nil
}

// CodesignFlags returns the "flags=…" line of `codesign -d -vvv` output,
// trimmed, or "".
func CodesignFlags(out string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "flags=") {
			return strings.TrimSpace(l)
		}
	}
	return ""
}

// HasHardenedRuntime says whether `codesign -d -vvv` output shows the
// hardened runtime: its flags line names "runtime", as in
// "CodeDirectory v=20500 size=… flags=0x10000(runtime) hashes=…".
func HasHardenedRuntime(codesignOut string) bool {
	return strings.Contains(CodesignFlags(codesignOut), "runtime")
}

var plistString = regexp.MustCompile(`<key>\s*(CFBundleVersion|CFBundleShortVersionString)\s*</key>\s*<string>([^<]*)</string>`)

// BundleVersion returns an XML Info.plist's CFBundleShortVersionString and
// CFBundleVersion joined as "short (build)", whichever of them it has, or
// "" when it has neither. (Dolphin ships an XML plist; a binary one gives
// "".)
func BundleVersion(plist []byte) string {
	var short, build string
	for _, m := range plistString.FindAllSubmatch(plist, -1) {
		v := strings.TrimSpace(string(m[2]))
		if string(m[1]) == "CFBundleVersion" {
			if build == "" {
				build = v
			}
		} else if short == "" {
			short = v
		}
	}
	switch {
	case short != "" && build != "" && short != build:
		return short + " (" + build + ")"
	case short != "":
		return short
	}
	return build
}

// MacState is what MacCheck finds.
type MacState int

const (
	MacReady        MacState = iota // the prepared copy is there and current
	MacNoDolphin                    // neither bundle exists
	MacNeedsCopy                    // only /Applications/Dolphin.app exists
	MacHardened                     // the copy still has the hardened runtime
	MacCopyOutdated                 // the original's version differs from the copy's: Dolphin was updated
)

func (s MacState) String() string {
	switch s {
	case MacReady:
		return "ready"
	case MacNoDolphin:
		return "no Dolphin.app"
	case MacNeedsCopy:
		return "needs a prepared copy"
	case MacHardened:
		return "the copy has the hardened runtime"
	case MacCopyOutdated:
		return "Dolphin was updated: refresh the copy"
	}
	return fmt.Sprintf("MacState(%d)", int(s))
}

// MacFacts is what MacCheck decides from: whether each bundle exists,
// their Info.plist contents, and `codesign -d -vvv` of the copy's binary.
type MacFacts struct {
	Original, Copy           bool
	OriginalPlist, CopyPlist []byte
	CopyCodesign             string
}

// MacCheck says whether the macOS prepare step is needed, and why: the
// first-run setup offers "Prepare Dolphin" for MacNeedsCopy and
// MacHardened, and "Dolphin was updated — refresh the copy" for
// MacCopyOutdated. A copy without its original is ready (the user removed
// /Applications/Dolphin.app). Versions that can't be read never call a
// copy outdated.
func MacCheck(f MacFacts) MacState {
	switch {
	case !f.Original && !f.Copy:
		return MacNoDolphin
	case !f.Copy:
		return MacNeedsCopy
	case HasHardenedRuntime(f.CopyCodesign):
		return MacHardened
	case f.Original:
		o, c := BundleVersion(f.OriginalPlist), BundleVersion(f.CopyPlist)
		if o != "" && c != "" && o != c {
			return MacCopyOutdated
		}
	}
	return MacReady
}
