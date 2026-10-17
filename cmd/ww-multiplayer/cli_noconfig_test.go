package main

import (
	"errors"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestCLIReadsNoConfig: the CLI does not read the app's saved settings
// (docs/01-architecture.md, owner decision 11). Package main must not
// import internal/config; the app's face will get it from its own package.
// Neither may wwapi or any mod (docs/api/00-design.md §6.5): a mod's
// settings reach it through wwapi.ConfigSource, which the faces fill.
// Nor may wwdev (cmd/wwdev), the developer tools moved out of the CLI.
// Package main is this directory (cmd/ww-multiplayer since Phase D7); the
// other trees are walked from the repo root.
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
	self := filepath.Join(repoRoot, "cmd", "ww-multiplayer")
	for _, dir := range []string{"wwapi", "mods", "cmd"} {
		err := filepath.WalkDir(filepath.Join(repoRoot, dir), func(path string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() && path == self {
				return filepath.SkipDir // package main: the glob above has it
			}
			if err == nil && !d.IsDir() && strings.HasSuffix(path, ".go") {
				files = append(files, path)
			}
			return err
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatal(err)
		}
	}
	if !slices.Contains(files, filepath.Join(repoRoot, "wwapi", "context.go")) {
		t.Fatal("the walk missed wwapi/context.go")
	}
	if !slices.Contains(files, filepath.Join(repoRoot, "cmd", "wwdev", "main.go")) {
		t.Fatal("the walk missed cmd/wwdev/main.go")
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
