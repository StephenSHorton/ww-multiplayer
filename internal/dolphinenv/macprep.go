package dolphinenv

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// ErrNotMac is PrepareMacHere's (and MacStatusHere's) error on any OS but
// macOS.
var ErrNotMac = errors.New("preparing Dolphin is a macOS step")

// MacStep is one step of MacPlan: a line to log, then one action. Exactly
// one of RemoveIfExists, MkdirAll and Cmd is set.
type MacStep struct {
	// Say is logged as "==> <Say>" before the step runs (for
	// RemoveIfExists, only when the path exists).
	Say string
	// RemoveIfExists removes this path (an older copy) when it exists.
	RemoveIfExists string
	// MkdirAll creates this directory and its parents. It logs nothing.
	MkdirAll string
	// Cmd is a program and its arguments, run through a Runner.
	Cmd []string
	// BestEffort says a failing Cmd doesn't stop the plan (`|| true` in
	// the script).
	BestEffort bool
}

// MacPlan is what PrepareMac does to copy src (a Dolphin.app) to dst, in
// order, as scripts/setup-mac-dolphin.sh does it: remove an older copy,
// copy, strip xattrs, give the copy MacBundleID, re-sign its main
// executable ad hoc without the hardened runtime, and register it with
// LaunchServices. It is a pure function of its arguments, so a test checks
// the commands without running any; PrepareMac runs it and then verifies
// the result (MacVerify).
func MacPlan(src, dst string) []MacStep {
	plist := filepath.Join(dst, "Contents", "Info.plist")
	bin := filepath.Join(dst, "Contents", "MacOS", "Dolphin")
	return []MacStep{
		{Say: fmt.Sprintf("Removing existing %s...", dst), RemoveIfExists: dst},
		{Say: fmt.Sprintf("Copying %s -> %s...", src, dst)},
		{MkdirAll: filepath.Dir(dst)},
		{Cmd: []string{"cp", "-R", src, dst}},
		{Say: "Stripping com.apple.provenance and other locking xattrs...", Cmd: []string{"xattr", "-cr", dst}, BestEffort: true},
		{Say: fmt.Sprintf("Setting CFBundleIdentifier to %s (avoids LaunchServices tie with /Applications)...", MacBundleID),
			Cmd: []string{"plutil", "-replace", "CFBundleIdentifier", "-string", MacBundleID, plist}},
		{Say: "Re-signing main Dolphin executable without hardened runtime...", Cmd: []string{"codesign", "--force", "--sign", "-", bin}},
		{Say: "Registering with LaunchServices...", Cmd: []string{lsregister, "-f", dst}, BestEffort: true},
	}
}

// MacVerify is the commands PrepareMac reads the copy back with after
// MacPlan: its signature (whose flags must not name the runtime) and its
// bundle ID (which must be MacBundleID).
func MacVerify(dst string) (codesign, bundleID []string) {
	plist := filepath.Join(dst, "Contents", "Info.plist")
	bin := filepath.Join(dst, "Contents", "MacOS", "Dolphin")
	return []string{"codesign", "-d", "-vvv", bin}, []string{"plutil", "-extract", "CFBundleIdentifier", "raw", plist}
}

// PrepareMac runs scripts/setup-mac-dolphin.sh's steps (MacPlan), logging
// each as the script prints it, then verifies the signature and bundle ID
// (MacVerify). No step needs sudo. dst must be another path ending in
// ".app": it is removed first.
func PrepareMac(rep report.Reporter, r Runner, src, dst string) error {
	if filepath.Ext(dst) != ".app" || filepath.Clean(dst) == filepath.Clean(src) {
		return fmt.Errorf("prepare Dolphin: the copy %q must be another .app than %q", dst, src)
	}
	if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
		return fmt.Errorf("%s not found. Install Dolphin first", src)
	}
	step := func(s string) { report.Logf(rep, report.Info, "==> %s", s) }
	run := func(cmd []string) error {
		out, err := r.Run(cmd[0], cmd[1:]...)
		if err != nil {
			if s := strings.TrimSpace(string(out)); s != "" {
				return fmt.Errorf("%s: %w: %s", cmd[0], err, s)
			}
			return fmt.Errorf("%s: %w", cmd[0], err)
		}
		return nil
	}
	for _, s := range MacPlan(src, dst) {
		switch {
		case s.RemoveIfExists != "":
			if _, err := os.Stat(s.RemoveIfExists); err != nil {
				continue
			}
			step(s.Say)
			if err := os.RemoveAll(s.RemoveIfExists); err != nil {
				return err
			}
			continue
		case s.Say != "":
			step(s.Say)
		}
		switch {
		case s.MkdirAll != "":
			if err := os.MkdirAll(s.MkdirAll, 0o755); err != nil {
				return err
			}
		case len(s.Cmd) > 0:
			if err := run(s.Cmd); err != nil && !s.BestEffort {
				return err
			}
		}
	}

	step("Verifying...")
	signCmd, idCmd := MacVerify(dst)
	sig, _ := r.Run(signCmd[0], signCmd[1:]...)
	flags := CodesignFlags(string(sig))
	report.Logf(rep, report.Info, "    %s", flags)
	if HasHardenedRuntime(string(sig)) {
		return errors.New("hardened runtime still set — re-sign failed")
	}
	id, err := r.Run(idCmd[0], idCmd[1:]...)
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
// "" when it has neither. A binary plist gives "": GatherMacFacts reads
// the versions with plutil first (MacVersionCmds), which reads both, and
// keeps this as the fallback.
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
	return joinVersion(short, build)
}

// joinVersion is "short (build)", whichever of them is set, or "".
func joinVersion(short, build string) string {
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
// their versions as plutil reads them (any plist format), their Info.plist
// contents (the fallback when plutil gave nothing), and `codesign -d -vvv`
// of the copy's binary.
type MacFacts struct {
	Original, Copy               bool
	OriginalVersion, CopyVersion string
	OriginalPlist, CopyPlist     []byte
	CopyCodesign                 string
}

// version is a bundle's version: plutil's, else the XML plist's.
func version(v string, plist []byte) string {
	if v != "" {
		return v
	}
	return BundleVersion(plist)
}

// MacVersionCmds is the commands GatherMacFacts reads a bundle's
// CFBundleShortVersionString and CFBundleVersion with: plutil reads XML and
// binary plists alike.
func MacVersionCmds(app string) (short, build []string) {
	plist := filepath.Join(app, "Contents", "Info.plist")
	return []string{"plutil", "-extract", "CFBundleShortVersionString", "raw", plist},
		[]string{"plutil", "-extract", "CFBundleVersion", "raw", plist}
}

// plutilVersion reads app's version with MacVersionCmds through r, "" when
// plutil can't.
func plutilVersion(r Runner, app string) string {
	get := func(cmd []string) string {
		out, err := r.Run(cmd[0], cmd[1:]...)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	short, build := MacVersionCmds(app)
	return joinVersion(get(short), get(build))
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
		o, c := version(f.OriginalVersion, f.OriginalPlist), version(f.CopyVersion, f.CopyPlist)
		if o != "" && c != "" && o != c {
			return MacCopyOutdated
		}
	}
	return MacReady
}

// GatherMacFacts reads the MacFacts of the original bundle (MacApp) and
// home's prepared copy (MacPreparedApp) through exists, readFile and r
// (each bundle's versions with plutil, the copy's `codesign -d -vvv`), so
// MacCheck can run on fakes; MacStatusHere passes this Mac's.
func GatherMacFacts(home string, exists func(string) bool, readFile func(string) ([]byte, error), r Runner) MacFacts {
	cp := MacPreparedApp(home)
	f := MacFacts{Original: exists(MacApp), Copy: exists(cp)}
	if f.Original {
		f.OriginalVersion = plutilVersion(r, MacApp)
		f.OriginalPlist, _ = readFile(filepath.Join(MacApp, "Contents", "Info.plist"))
	}
	if f.Copy {
		f.CopyVersion = plutilVersion(r, cp)
		f.CopyPlist, _ = readFile(filepath.Join(cp, "Contents", "Info.plist"))
		signCmd, _ := MacVerify(cp)
		out, _ := r.Run(signCmd[0], signCmd[1:]...)
		f.CopyCodesign = string(out)
	}
	return f
}
