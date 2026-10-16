package dolphinenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Env is what Detect and UserDir look at. Host fills it from this
// machine; a test fills it with fakes.
type Env struct {
	GOOS string
	Home string // os.UserHomeDir
	// ConfigDir is os.UserConfigDir: %APPDATA% on Windows,
	// ~/Library/Application Support on macOS.
	ConfigDir string
	Getenv    func(string) string
	// Exists says whether a file or directory exists.
	Exists func(string) bool
	// Running returns the executable paths of the running Dolphin
	// processes, oldest first; nil when none or unknown.
	Running func() []string
	// InstallLocations returns the folders Dolphin's installer recorded
	// (Windows: the uninstall registry keys' InstallLocation); nil elsewhere.
	InstallLocations func() []string
	// LookPath finds a program on PATH (exec.LookPath).
	LookPath func(string) (string, error)
}

// Host is the Env of this machine.
func Host() Env {
	cfg, _ := os.UserConfigDir()
	home, _ := os.UserHomeDir()
	return Env{
		GOOS: runtime.GOOS, Home: home, ConfigDir: cfg,
		Getenv: os.Getenv, Exists: statExists,
		Running: runningExes, InstallLocations: installLocations,
		LookPath: exec.LookPath,
	}
}

// Source says where Detect found a Dolphin.
type Source string

const (
	SourceRunning   Source = "running"   // a running Dolphin process
	SourceEnv       Source = EnvExe      // the DOLPHIN_EXE variable
	SourceInstalled Source = "installed" // a standard install location
	SourceRegistry  Source = "registry"  // the Windows installer's record
	SourcePortable  Source = "portable"  // the zip build on the Desktop (today's default)
	SourceScoop     Source = "scoop"     // a scoop install
	SourcePrepared  Source = "prepared"  // macOS: the copy PrepareMac made
	SourcePath      Source = "PATH"      // on PATH (Linux)
	SourceDefault   Source = "default"   // nothing found: the default path
	sourceNone      Source = ""
)

// Candidate is one place a Dolphin may be.
type Candidate struct {
	Exe    string
	Source Source
	// Found says the file exists (for a running process, that it runs).
	Found bool
}

// Candidates lists where Dolphin may be, in the order Detect tries them
// (docs/01-architecture.md, "Dolphin environment"):
//
//  1. a running Dolphin: its own executable, the most reliable signal;
//  2. DOLPHIN_EXE;
//  3. Windows: %ProgramFiles%\Dolphin, %ProgramFiles%\Dolphin-x64, the
//     installer's recorded InstallLocation, %USERPROFILE%\Desktop\Dolphin-x64
//     (the default dolphin2 has always used), and scoop's apps\dolphin\current;
//     macOS: ~/Applications/Dolphin.app (the prepared copy), then
//     /Applications/Dolphin.app; elsewhere dolphin-emu on PATH.
//
// Each path appears once (the first time), and Found says whether it
// exists. On macOS a running Dolphin's path is its bundle's binary.
func Candidates(e Env) []Candidate {
	var out []Candidate
	seen := map[string]bool{}
	add := func(exe string, src Source, found bool) {
		if exe == "" {
			return
		}
		k := pathKey(e.GOOS, exe)
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, Candidate{Exe: exe, Source: src, Found: found})
	}
	exists := func(p string) bool { return e.Exists != nil && e.Exists(p) }
	if e.Running != nil {
		for _, exe := range e.Running() {
			add(exe, SourceRunning, true)
		}
	}
	if e.Getenv != nil {
		if v := e.Getenv(EnvExe); v != "" {
			add(v, SourceEnv, exists(v))
		}
	}
	join := func(elem ...string) string { return joinFor(e.GOOS, elem...) }
	switch e.GOOS {
	case "windows":
		pf := ""
		if e.Getenv != nil {
			pf = e.Getenv("ProgramFiles")
		}
		if pf != "" {
			for _, d := range []string{"Dolphin", "Dolphin-x64"} {
				p := join(pf, d, "Dolphin.exe")
				add(p, SourceInstalled, exists(p))
			}
		}
		if e.InstallLocations != nil {
			for _, d := range e.InstallLocations() {
				p := join(d, "Dolphin.exe")
				add(p, SourceRegistry, exists(p))
			}
		}
		if e.Home != "" {
			p := join(e.Home, "Desktop", "Dolphin-x64", "Dolphin.exe")
			add(p, SourcePortable, exists(p))
			p = join(e.Home, "scoop", "apps", "dolphin", "current", "Dolphin.exe")
			add(p, SourceScoop, exists(p))
		}
	case "darwin":
		if e.Home != "" {
			p := join(e.Home, "Applications", "Dolphin.app", "Contents", "MacOS", "Dolphin")
			add(p, SourcePrepared, exists(p))
		}
		p := "/Applications/Dolphin.app/Contents/MacOS/Dolphin"
		add(p, SourceInstalled, exists(p))
	default:
		if e.LookPath != nil {
			if p, err := e.LookPath("dolphin-emu"); err == nil {
				add(p, SourcePath, true)
			}
		}
	}
	return out
}

// Detect returns the first Candidate found, and every candidate. When none
// is found it returns the default path (Defaults' Exe) with
// SourceDefault, so the caller can show where it looked and offer
// "Browse…".
func Detect(e Env) (Candidate, []Candidate) {
	all := Candidates(e)
	for _, c := range all {
		if c.Found {
			return c, all
		}
	}
	exists := func(p string) bool { return e.Exists != nil && e.Exists(p) }
	return Candidate{Exe: Defaults(e.GOOS, e.Home, e.ConfigDir, exists).Exe, Source: SourceDefault}, all
}

// UserDir returns the user directory the Dolphin at exe uses: <exe
// dir>/User when a portable.txt sits beside the executable (Dolphin's
// portable mode), else Dolphin's default user directory (DefaultUserDirs'
// first). portable reports which it is. (On macOS Dolphin looks for
// portable.txt beside the executable inside the bundle too (inferred from
// Dolphin's UICommon::SetUserDirectory; not tried on a Mac).)
func UserDir(e Env, exe string) (dir string, portable bool) {
	if exe != "" && e.Exists != nil {
		d := dirFor(e.GOOS, exe)
		if e.Exists(joinFor(e.GOOS, d, "portable.txt")) {
			return joinFor(e.GOOS, d, "User"), true
		}
	}
	first, _ := DefaultUserDirs(e.GOOS, e.ConfigDir)
	return first, false
}

// joinFor and dirFor are filepath.Join and filepath.Dir: a path keeps this
// OS's separators whatever goos a test asks about.
func joinFor(_ string, elem ...string) string { return filepath.Join(elem...) }

func dirFor(_ string, p string) string { return filepath.Dir(p) }

// pathKey is how two paths are compared: without case on Windows.
func pathKey(goos, p string) string {
	if goos == "windows" {
		return strings.ToLower(p)
	}
	return p
}
