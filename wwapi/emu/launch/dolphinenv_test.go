package launch

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

type lines []string

func (l *lines) Log(level log.Level, msg string) {
	*l = append(*l, fmt.Sprintf("%d %s", level, msg))
}

func set(paths ...string) func(string) bool {
	m := map[string]bool{}
	for _, p := range paths {
		m[p] = true
	}
	return func(p string) bool { return m[p] }
}

func TestDefaults(t *testing.T) {
	home, cfg := filepath.Join("H"), filepath.Join("C")
	userCopy := filepath.Join(home, "Applications", "Dolphin.app", "Contents", "MacOS", "Dolphin")
	cases := []struct {
		goos   string
		exists func(string) bool
		want   Paths
	}{
		{"windows", set(), Paths{
			filepath.Join(home, "Desktop", "Dolphin-x64", "Dolphin.exe"),
			filepath.Join(home, "Desktop", "Dolphin-x64", "Roms", "WW_Multiplayer_Patched.iso"),
			filepath.Join(cfg, "Dolphin Emulator"), filepath.Join(cfg, "Dolphin Emulator 2")}},
		{"darwin", set(userCopy), Paths{userCopy, filepath.Join(home, "Roms", "WW_Multiplayer_Patched.iso"),
			filepath.Join(cfg, "Dolphin"), filepath.Join(cfg, "Dolphin 2")}},
		{"darwin", set(), Paths{"/Applications/Dolphin.app/Contents/MacOS/Dolphin", filepath.Join(home, "Roms", "WW_Multiplayer_Patched.iso"),
			filepath.Join(cfg, "Dolphin"), filepath.Join(cfg, "Dolphin 2")}},
		{"linux", set(), Paths{"dolphin-emu", filepath.Join(home, "Roms", "WW_Multiplayer_Patched.iso"),
			filepath.Join(cfg, "dolphin-emu"), filepath.Join(cfg, "dolphin-emu-2")}},
	}
	for _, c := range cases {
		if got := Defaults(c.goos, home, cfg, c.exists); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.goos, got, c.want)
		}
	}
}

func TestWithEnv(t *testing.T) {
	p := Paths{"exe", "iso", "u1", "u2"}
	env := map[string]string{"DOLPHIN_EXE": "E", "USER_DIR_2": "U2", "ISO_PATH": ""}
	if got := p.WithEnv(func(k string) string { return env[k] }); got != (Paths{"E", "iso", "u1", "U2"}) {
		t.Errorf("got %+v", got)
	}
}

func TestCandidatesWindows(t *testing.T) {
	home, pf := filepath.Join("C:", "Users", "ann"), filepath.Join("C:", "Program Files")
	running := filepath.Join("D:", "emu", "Dolphin.exe")
	regDir := filepath.Join("E:", "Apps", "Dolphin")
	desktop := filepath.Join(home, "Desktop", "Dolphin-x64", "Dolphin.exe")
	env := map[string]string{"ProgramFiles": pf, "DOLPHIN_EXE": strings.ToUpper(running)}
	e := Env{
		GOOS: "windows", Home: home, ConfigDir: filepath.Join(home, "AppData", "Roaming"),
		Getenv:           func(k string) string { return env[k] },
		Exists:           set(desktop),
		Running:          func() []string { return []string{running} },
		InstallLocations: func() []string { return []string{regDir} },
	}
	got := Candidates(e)
	want := []Candidate{
		{running, SourceRunning, true}, // DOLPHIN_EXE names it too (other case): listed once
		{filepath.Join(pf, "Dolphin", "Dolphin.exe"), SourceInstalled, false},
		{filepath.Join(pf, "Dolphin-x64", "Dolphin.exe"), SourceInstalled, false},
		{filepath.Join(regDir, "Dolphin.exe"), SourceRegistry, false},
		{desktop, SourcePortable, true},
		{filepath.Join(home, "scoop", "apps", "dolphin", "current", "Dolphin.exe"), SourceScoop, false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("candidates:\n got %+v\nwant %+v", got, want)
	}
	if best, _ := Detect(e); best != want[0] {
		t.Errorf("Detect = %+v", best)
	}
	e.Running = nil
	env["DOLPHIN_EXE"] = filepath.Join("X", "nope.exe")
	if best, all := Detect(e); best != (Candidate{desktop, SourcePortable, true}) || all[0].Source != SourceEnv || all[0].Found {
		t.Errorf("Detect without a running Dolphin = %+v (%+v)", best, all)
	}
	e.Exists = set()
	if best, _ := Detect(e); best.Source != SourceDefault || best.Exe != desktop {
		t.Errorf("nothing found: %+v", best)
	}
}

func TestCandidatesDarwinAndLinux(t *testing.T) {
	home := "/Users/ann"
	prepared := filepath.Join(home, "Applications", "Dolphin.app", "Contents", "MacOS", "Dolphin")
	e := Env{GOOS: "darwin", Home: home, Getenv: func(string) string { return "" }, Exists: set("/Applications/Dolphin.app/Contents/MacOS/Dolphin")}
	got := Candidates(e)
	want := []Candidate{{prepared, SourcePrepared, false}, {"/Applications/Dolphin.app/Contents/MacOS/Dolphin", SourceInstalled, true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("darwin: %+v", got)
	}
	l := Env{GOOS: "linux", LookPath: func(string) (string, error) { return "/usr/bin/dolphin-emu", nil }}
	if best, _ := Detect(l); best != (Candidate{"/usr/bin/dolphin-emu", SourcePath, true}) {
		t.Errorf("linux: %+v", best)
	}
	l.LookPath = func(string) (string, error) { return "", errors.New("no") }
	if best, all := Detect(l); best.Source != SourceDefault || best.Exe != "dolphin-emu" || len(all) != 0 {
		t.Errorf("linux, none: %+v %+v", best, all)
	}
	if got := Candidates(Env{GOOS: "windows"}); len(got) != 0 {
		t.Errorf("empty env: %+v", got)
	}
}

func TestUserDir(t *testing.T) {
	exe := filepath.Join("D:", "Dolphin-x64", "Dolphin.exe")
	cfg := filepath.Join("C:", "AppData")
	e := Env{GOOS: "windows", ConfigDir: cfg, Exists: set(filepath.Join("D:", "Dolphin-x64", "portable.txt"))}
	if d, portable := UserDir(e, exe); d != filepath.Join("D:", "Dolphin-x64", "User") || !portable {
		t.Errorf("portable: %s %v", d, portable)
	}
	e.Exists = set()
	if d, portable := UserDir(e, exe); d != filepath.Join(cfg, "Dolphin Emulator") || portable {
		t.Errorf("installed: %s %v", d, portable)
	}
	if d, _ := UserDir(Env{GOOS: "darwin", ConfigDir: "/S"}, ""); d != filepath.Join("/S", "Dolphin") {
		t.Errorf("darwin: %s", d)
	}
}

func TestDolphinExes(t *testing.T) {
	in := []string{`C:\D\Dolphin.exe`, `C:\D\DolphinTool.exe`, "/Applications/Dolphin.app/Contents/MacOS/Dolphin", "/usr/bin/dolphin-emu", `C:\D\Updater.exe`, "dolphin.EXE"}
	want := []string{`C:\D\Dolphin.exe`, "/Applications/Dolphin.app/Contents/MacOS/Dolphin", "/usr/bin/dolphin-emu", "dolphin.EXE"}
	if got := dolphinExes(in); !reflect.DeepEqual(got, want) {
		t.Errorf("got %q", got)
	}
}

func TestInstallLocationsOf(t *testing.T) {
	got := installLocationsOf([]UninstallEntry{
		{Key: "Dolphin", DisplayName: "Dolphin", InstallLocation: `"C:\Program Files\Dolphin\"`},
		{Key: "dolphin-x", DisplayName: "dolphin 2509", InstallLocation: " "},
		{Key: "DolphinBrowser", DisplayName: "Some Browser (Dolphin)", InstallLocation: `C:\B`},
		{Key: "{guid}", DisplayName: "Dolphin Emulator", InstallLocation: `D:\Emu`},
	})
	want := []string{filepath.Clean(`C:\Program Files\Dolphin\`), filepath.Clean(`D:\Emu`)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q", got)
	}
}

func TestLaunchArgs(t *testing.T) {
	mem := []string{"-C", "Dolphin.Core.RAMOverrideEnable=True", "-C", "Dolphin.Core.MEM1Size=0x03000000"}
	if got := LaunchArgs("U", "I.iso", ""); !reflect.DeepEqual(got, append([]string{"-u", "U", "-e", "I.iso"}, mem...)) {
		t.Errorf("%q", got)
	}
	if got := LaunchArgs("U", "I.iso", "s.sav"); !reflect.DeepEqual(got, append(append([]string{"-u", "U", "-e", "I.iso"}, mem...), "--save_state=s.sav")) {
		t.Errorf("%q", got)
	}
}

func TestAppBundle(t *testing.T) {
	if b, err := AppBundle("/Users/a/Applications/Dolphin.app/Contents/MacOS/Dolphin"); err != nil || b != "/Users/a/Applications/Dolphin.app" {
		t.Errorf("%q %v", b, err)
	}
	_, err := AppBundle("/usr/bin/dolphin-emu")
	if err == nil || err.Error() != `DOLPHIN_EXE "/usr/bin/dolphin-emu" is not an app-bundle binary; expected path ending in .app/Contents/MacOS/Dolphin` {
		t.Errorf("%v", err)
	}
}

func TestNewPID(t *testing.T) {
	if p, ok := newPID([]uint32{3, 9}, []uint32{3, 9, 12}); !ok || p != 12 {
		t.Errorf("%d %v", p, ok)
	}
	if _, ok := newPID([]uint32{3}, []uint32{3}); ok {
		t.Error("no new PID found one")
	}
}

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapUserDir(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "ud2")
	write(t, filepath.Join(src, "Config", "Dolphin.ini"), "[Core]")
	write(t, filepath.Join(src, "GC", "USA", "Card A", "01-GZLE-gczelda.gci"), "GZLE")
	write(t, filepath.Join(src, "Cache", "Shaders", "x.cache"), "locked")
	write(t, filepath.Join(src, "Config", "Cache", "kept"), "only the top-level Cache is skipped")
	if err := BootstrapUserDir(src, dst); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"Config/Dolphin.ini", "GC/USA/Card A/01-GZLE-gczelda.gci", "Config/Cache/kept"} {
		if _, err := os.Stat(filepath.Join(dst, filepath.FromSlash(p))); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "Cache")); !os.IsNotExist(err) {
		t.Errorf("Cache copied: %v", err)
	}
	if err := BootstrapUserDir(filepath.Join(src, "missing"), dst); err == nil {
		t.Error("a missing source copied")
	}
}

func TestEnsureCheatsIni(t *testing.T) {
	ud := t.TempDir()
	var l lines
	EnsureCheatsIni(&l, ud, []byte("[Gecko]\n"))
	EnsureCheatsIni(&l, ud, []byte("[Other]\n"))
	EnsureCheatsIni(&l, "", []byte("x"))
	EnsureCheatsIni(&l, ud, nil)
	target := filepath.Join(ud, "GameSettings", "GZLE01.ini")
	if b, _ := os.ReadFile(target); string(b) != "[Gecko]\n" {
		t.Errorf("file %q", b)
	}
	want := lines{
		fmt.Sprintf("%d cheats: installed bundled GZLE01.ini at %s (enable individual codes via Dolphin → game properties → Gecko Codes)", log.OK, target),
		fmt.Sprintf("%d cheats: %s already exists, leaving it untouched (merge from cheats/GZLE01.ini if you want the bundled codes)", log.Info, target),
	}
	if !reflect.DeepEqual(l, want) {
		t.Errorf("log %q", l)
	}
	// A file where GameSettings should be: a warning, nothing else.
	ud2 := t.TempDir()
	write(t, filepath.Join(ud2, "GameSettings"), "not a dir")
	l = nil
	EnsureCheatsIni(&l, ud2, []byte("x"))
	if len(l) != 1 || !strings.HasPrefix(l[0], fmt.Sprintf("%d cheats: mkdir ", log.Warn)) {
		t.Errorf("log %q", l)
	}
}

// fakeRunner records commands and answers codesign -d and plutil
// -extract from its fields.
type fakeRunner struct {
	calls     []string
	signature string
	bundleID  string
	fail      string // a program that fails
	cp        func(src, dst string)
}

func (f *fakeRunner) Run(name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, strings.Join(append([]string{filepath.Base(name)}, args...), " "))
	if filepath.Base(name) == f.fail {
		return []byte("boom"), errors.New("exit status 1")
	}
	switch {
	case name == "cp" && f.cp != nil:
		f.cp(args[1], args[2])
	case name == "codesign" && args[0] == "-d":
		return []byte(f.signature), nil
	case name == "plutil" && args[0] == "-extract":
		return []byte(f.bundleID + "\n"), nil
	}
	return nil, nil
}

func TestPrepareMac(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "Applications", "Dolphin.app")
	dst := filepath.Join(root, "home", "Applications", "Dolphin.app")
	write(t, filepath.Join(src, "Contents", "Info.plist"), "<plist/>")
	write(t, filepath.Join(dst, "old"), "an older copy")
	r := &fakeRunner{
		signature: "Executable=x\nCodeDirectory v=20400 size=1 flags=0x2(adhoc) hashes=1\n",
		bundleID:  MacBundleID,
		cp:        func(s, d string) { write(t, filepath.Join(d, "copied"), "x") },
	}
	var l lines
	if err := PrepareMac(&l, r, src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "old")); !os.IsNotExist(err) {
		t.Errorf("the old copy was not removed: %v", err)
	}
	plist := filepath.Join(dst, "Contents", "Info.plist")
	bin := filepath.Join(dst, "Contents", "MacOS", "Dolphin")
	want := []string{
		"cp -R " + src + " " + dst,
		"xattr -cr " + dst,
		"plutil -replace CFBundleIdentifier -string " + MacBundleID + " " + plist,
		"codesign --force --sign - " + bin,
		"lsregister -f " + dst,
		"codesign -d -vvv " + bin,
		"plutil -extract CFBundleIdentifier raw " + plist,
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Errorf("commands:\n%s", strings.Join(r.calls, "\n"))
	}
	if len(l) != 9 || !strings.Contains(l[0], "==> Removing existing") || !strings.Contains(l[7], "flags=0x2(adhoc)") || !strings.Contains(l[8], "Done.") {
		t.Errorf("log:\n%s", strings.Join(l, "\n"))
	}

	// xattr and lsregister failing is fine, as in the script.
	r2 := &fakeRunner{signature: r.signature, bundleID: MacBundleID, fail: "xattr"}
	if err := PrepareMac(&lines{}, r2, src, dst); err != nil {
		t.Errorf("xattr failure: %v", err)
	}
	for _, c := range []struct {
		r    *fakeRunner
		want string
	}{
		{&fakeRunner{signature: "CodeDirectory flags=0x10000(runtime)", bundleID: MacBundleID}, "hardened runtime still set"},
		{&fakeRunner{signature: r.signature, bundleID: "org.dolphin-emu.dolphin"}, "bundle ID is org.dolphin-emu.dolphin"},
		{&fakeRunner{fail: "codesign"}, "codesign: exit status 1: boom"},
		{&fakeRunner{fail: "cp"}, "cp: exit status 1"},
	} {
		if err := PrepareMac(&lines{}, c.r, src, dst); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("want %q, got %v", c.want, err)
		}
	}
	if err := PrepareMac(&lines{}, &fakeRunner{}, filepath.Join(root, "nope.app"), dst); err == nil || !strings.Contains(err.Error(), "Install Dolphin first") {
		t.Errorf("missing source: %v", err)
	}
	for _, bad := range []string{src, filepath.Join(root, "home")} {
		r := &fakeRunner{}
		if err := PrepareMac(&lines{}, r, src, bad); err == nil || len(r.calls) != 0 {
			t.Errorf("dst %s: %v, %v", bad, err, r.calls)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "home")); err != nil {
		t.Errorf("a refused dst was touched: %v", err)
	}
}

func TestBundleVersionAndMacCheck(t *testing.T) {
	plist := func(short, build string) []byte {
		var b bytes.Buffer
		b.WriteString("<plist><dict>")
		if build != "" {
			fmt.Fprintf(&b, "<key>CFBundleVersion</key>\n\t<string>%s</string>", build)
		}
		if short != "" {
			fmt.Fprintf(&b, "<key>CFBundleShortVersionString</key><string>%s</string>", short)
		}
		b.WriteString("</dict></plist>")
		return b.Bytes()
	}
	for _, c := range []struct {
		short, build, want string
	}{{"2509", "2509-123", "2509 (2509-123)"}, {"2509", "", "2509"}, {"", "77", "77"}, {"5.0", "5.0", "5.0"}, {"", "", ""}} {
		if got := BundleVersion(plist(c.short, c.build)); got != c.want {
			t.Errorf("BundleVersion(%q, %q) = %q", c.short, c.build, got)
		}
	}
	adhoc, hardened := "flags=0x2(adhoc)", "CodeDirectory v=1 flags=0x10000(runtime) hashes=2"
	for _, c := range []struct {
		f    MacFacts
		want MacState
	}{
		{MacFacts{}, MacNoDolphin},
		{MacFacts{Original: true}, MacNeedsCopy},
		{MacFacts{Original: true, Copy: true, CopyCodesign: hardened}, MacHardened},
		{MacFacts{Original: true, Copy: true, CopyCodesign: adhoc, OriginalPlist: plist("2509", ""), CopyPlist: plist("2506", "")}, MacCopyOutdated},
		{MacFacts{Original: true, Copy: true, CopyCodesign: adhoc, OriginalPlist: plist("2509", ""), CopyPlist: plist("2509", "")}, MacReady},
		{MacFacts{Original: true, Copy: true, CopyCodesign: adhoc, OriginalPlist: []byte("bplist00"), CopyPlist: plist("2509", "")}, MacReady},
		{MacFacts{Copy: true, CopyCodesign: adhoc}, MacReady},
	} {
		if got := MacCheck(c.f); got != c.want {
			t.Errorf("MacCheck(%+v) = %v, want %v", c.f, got, c.want)
		}
	}
	if HasHardenedRuntime("runtime is mentioned but no flags line") {
		t.Error("a line without flags= counted")
	}
}

func TestPrepareMacHere(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("it would prepare this Mac's Dolphin")
	}
	if err := PrepareMacHere(&lines{}); !errors.Is(err, ErrNotMac) {
		t.Errorf("%v", err)
	}
}
