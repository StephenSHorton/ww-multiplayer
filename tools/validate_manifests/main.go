// Command validate_manifests checks every mod.json under mods/ against
// wwapi/schema/mod.schema.json and wwapi.ParseManifest (wwapi v0
// UNSTABLE). CI runs it on every PR:
//
//	go run ./tools/validate_manifests          # checks ./mods
//	go run ./tools/validate_manifests <dir>    # checks another tree
//
// Rules:
//   - a mod.json must parse and its id must equal its folder name;
//   - test fixtures live under mods/_fixtures, and only their ids start
//     with "_";
//   - a folder with an invalid.txt holds a mod.json that must be refused,
//     with an error that contains invalid.txt's text (trimmed).
//
// It prints one line per manifest and exits 1 on the first run with a
// problem.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/wwapi"
)

func main() {
	root := "mods"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	if len(os.Args) > 2 {
		fmt.Fprintln(os.Stderr, "usage: validate_manifests [mods-dir]")
		os.Exit(2)
	}
	if err := run(os.DirFS(root), root, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "validate_manifests:", err)
		os.Exit(1)
	}
}

// fixturesDir is the folder (under the mods root) that holds test mods.
const fixturesDir = "_fixtures"

// run checks every mod.json in fsys (shown as name in messages) and
// reports to w. It returns an error when any manifest has a problem.
func run(fsys fs.FS, name string, w io.Writer) error {
	var paths []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == "mod.json" {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(paths)
	var problems, invalid int
	for _, p := range paths {
		show := filepath.ToSlash(filepath.Join(name, p))
		wantErr, why := check(fsys, p)
		switch {
		case why != "":
			problems++
			fmt.Fprintf(w, "FAIL %s: %s\n", show, why)
		case wantErr:
			invalid++
			fmt.Fprintf(w, "ok   %s (refused, as invalid.txt expects)\n", show)
		default:
			fmt.Fprintf(w, "ok   %s\n", show)
		}
	}
	fmt.Fprintf(w, "%d manifests, %d refused on purpose, %d problems\n", len(paths), invalid, problems)
	if len(paths) == 0 {
		return errors.New("no mod.json found under " + name)
	}
	if problems > 0 {
		return fmt.Errorf("%d problems", problems)
	}
	return nil
}

// check validates one mod.json. wantErr says the folder expects a
// refusal; why is non-empty on a problem.
func check(fsys fs.FS, p string) (wantErr bool, why string) {
	dir := path.Dir(p) // fs.FS paths always use /
	folder := path.Base(dir)
	underFixtures := strings.HasPrefix(dir, fixturesDir+"/")
	data, err := fs.ReadFile(fsys, p)
	if err != nil {
		return false, err.Error()
	}
	expect, err := fs.ReadFile(fsys, path.Join(dir, "invalid.txt"))
	switch {
	case err == nil:
		wantErr = true
	case !errors.Is(err, fs.ErrNotExist):
		return false, err.Error()
	}
	if dir == "." {
		return wantErr, "mod.json must sit in a folder named after its id"
	}
	m, perr := wwapi.ParseManifest(data)
	if wantErr {
		want := string(bytes.TrimSpace(expect))
		switch {
		case !underFixtures:
			return true, "invalid.txt is only for fixtures under " + fixturesDir
		case want == "":
			return true, "invalid.txt is empty"
		case perr == nil:
			return true, "was accepted, but invalid.txt expects an error containing " + fmt.Sprintf("%q", want)
		case !strings.Contains(perr.Error(), want):
			return true, fmt.Sprintf("error %q does not contain %q", perr, want)
		}
		if id := wwapi.ManifestFrom(data).ID; id != "" && id != folder {
			return true, fmt.Sprintf("id %q does not equal its folder name %q", id, folder)
		}
		return true, ""
	}
	if perr != nil {
		return false, perr.Error()
	}
	if m.ID != folder {
		return false, fmt.Sprintf("id %q does not equal its folder name %q", m.ID, folder)
	}
	if underFixtures != m.Fixture() {
		return false, fmt.Sprintf("id %q: ids that start with _ (test fixtures) belong under %s, and only those", m.ID, fixturesDir)
	}
	return false, ""
}
