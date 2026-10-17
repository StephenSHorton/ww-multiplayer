package main

// Equivalence of App PR 3's move of the Dolphin environment code into
// internal/dolphinenv (wwapi/emu/launch since C10). The legacy* functions are the code as it was in
// mp_local.go at c16e9ad, verbatim but for their names; the paths and
// arguments dolphin2, auto-recapture and auto-recapture-pair use must not
// change. (Their user-visible output is in cli_golden_test.go;
// auto-recapture has no golden, since it first ends every running Dolphin.)

import (
	"errors"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
)

func legacyDolphin2Defaults() (exe, iso, userDir1, userDir2 string) {
	cfg, _ := os.UserConfigDir()
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		exe = filepath.Join(home, "Applications", "Dolphin.app", "Contents", "MacOS", "Dolphin")
		if _, err := os.Stat(exe); err != nil {
			exe = "/Applications/Dolphin.app/Contents/MacOS/Dolphin"
		}
		iso = filepath.Join(home, "Roms", "WW_Multiplayer_Patched.iso")
		userDir1 = filepath.Join(cfg, "Dolphin")
		userDir2 = filepath.Join(cfg, "Dolphin 2")
	case "windows":
		exe = filepath.Join(home, "Desktop", "Dolphin-x64", "Dolphin.exe")
		iso = filepath.Join(home, "Desktop", "Dolphin-x64", "Roms", "WW_Multiplayer_Patched.iso")
		userDir1 = filepath.Join(cfg, "Dolphin Emulator")
		userDir2 = filepath.Join(cfg, "Dolphin Emulator 2")
	default:
		exe = "dolphin-emu"
		iso = filepath.Join(home, "Roms", "WW_Multiplayer_Patched.iso")
		userDir1 = filepath.Join(cfg, "dolphin-emu")
		userDir2 = filepath.Join(cfg, "dolphin-emu-2")
	}
	return
}

func legacyEnvOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// legacyLaunchArgs is launchDolphinDetached's argument list.
func legacyLaunchArgs(userDir, iso, saveState string) []string {
	args := []string{"-u", userDir, "-e", iso}
	if saveState != "" {
		args = append(args, "--save_state="+saveState)
	}
	return args
}

// TestDolphinEnvMatchesLegacy: for each environment (unset, set, set
// empty, and on macOS with and without the prepared copy), the paths
// dolphin2 and auto-recapture take are the legacy ones.
func TestDolphinEnvMatchesLegacy(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, "cfg")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", cfg)
	t.Setenv("XDG_CONFIG_HOME", cfg)
	envs := []map[string]string{
		{},
		{"DOLPHIN_EXE": "/x/Dolphin.exe", "ISO_PATH": "/x/p.iso", "USER_DIR_1": "/u1", "USER_DIR_2": "/u2"},
		{"DOLPHIN_EXE": "", "ISO_PATH": "/only.iso", "USER_DIR_2": ""},
	}
	for round := 0; round < 2; round++ {
		if round == 1 { // macOS: the prepared copy now exists
			exe := filepath.Join(home, "Applications", "Dolphin.app", "Contents", "MacOS", "Dolphin")
			if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(exe, nil, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		for _, env := range envs {
			for _, k := range []string{"DOLPHIN_EXE", "ISO_PATH", "USER_DIR_1", "USER_DIR_2"} {
				t.Setenv(k, env[k])
			}
			e, i, u1, u2 := legacyDolphin2Defaults()
			if ge, gi, gu1, gu2 := dolphin2Defaults(); [4]string{ge, gi, gu1, gu2} != [4]string{e, i, u1, u2} {
				t.Errorf("dolphin2Defaults = %q, legacy %q", [4]string{ge, gi, gu1, gu2}, [4]string{e, i, u1, u2})
			}
			want := launch.Paths{
				Exe: legacyEnvOrDefault("DOLPHIN_EXE", e), ISO: legacyEnvOrDefault("ISO_PATH", i),
				UserDir1: legacyEnvOrDefault("USER_DIR_1", u1), UserDir2: legacyEnvOrDefault("USER_DIR_2", u2),
			}
			if got := launch.FromEnv(); got != want {
				t.Errorf("env %v: FromEnv = %+v, legacy %+v", env, got, want)
			}
		}
	}
	for _, c := range [][3]string{{"u", "i.iso", ""}, {"u", "i.iso", "s.sav"}, {"", "", ""}} {
		if got, want := launch.LaunchArgs(c[0], c[1], c[2]), legacyLaunchArgs(c[0], c[1], c[2]); !reflect.DeepEqual(got, want) {
			t.Errorf("LaunchArgs%q = %q, legacy %q", c, got, want)
		}
	}
}

// TestCLIReadsNoConfig: the CLI does not read the app's saved settings
// (docs/01-architecture.md, owner decision 11). Package main must not
// import internal/config; the app's face will get it from its own package.
// Neither may wwapi or any mod (docs/api/00-design.md §6.5): a mod's
// settings reach it through wwapi.ConfigSource, which the faces fill.
func TestCLIReadsNoConfig(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	// The CLI's entry point and its command registry must be among them,
	// or a move out of this directory would leave the check empty.
	for _, want := range []string{"main.go", "commands.go"} {
		if !slices.Contains(files, want) {
			t.Fatalf("%s is not in package main's directory (have %d .go files)", want, len(files))
		}
	}
	for _, dir := range []string{"wwapi", "mods"} {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(path, ".go") {
				files = append(files, path)
			}
			return err
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatal(err)
		}
	}
	if !slices.Contains(files, filepath.Join("wwapi", "context.go")) {
		t.Fatal("the walk missed wwapi/context.go")
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") && filepath.Dir(name) == "." {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			if strings.HasSuffix(strings.Trim(imp.Path.Value, `"`), "/internal/config") {
				t.Errorf("%s imports internal/config", name)
			}
		}
	}
}
