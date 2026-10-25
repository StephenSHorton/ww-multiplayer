// Command check_game_files fails when the repo or a release asset holds a
// Nintendo game file or a player's save (internal/gamefiles: names,
// extensions, file magic, zip entries). CI runs it on every PR and
// release.yml on the repo and on every asset before it is published:
//
//	go run ./tools/check_game_files                 # the git working tree
//	go run ./tools/check_game_files <path>...       # files and folders (zips are opened)
//
// With no path it checks every tracked file plus the untracked files git
// does not ignore, so a game file is flagged before it can be committed;
// an ignored one (inject/original.dol, saves/, a disc image) is not looked
// at until someone force-adds it. It exits 1 with one line per finding, 2
// when the scan itself fails, 0 when clean.
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/gamefiles"
)

func main() { os.Exit(run(".", os.Args[1:], os.Stdout, os.Stderr)) }

// run is the command in dir (the git tree's place when args is empty):
// its exit code, 0 clean, 1 findings, 2 a failed scan.
func run(dir string, args []string, stdout, stderr io.Writer) int {
	var r gamefiles.Result
	var err error
	if len(args) > 0 {
		for _, p := range args {
			if err = r.Tree(p); err != nil {
				break
			}
		}
	} else {
		err = gitTree(dir, &r)
	}
	if err != nil {
		fmt.Fprintln(stderr, "check_game_files:", err)
		return 2
	}
	if len(r.Findings) > 0 {
		fmt.Fprintf(stdout, "Game files found (%d):\n", len(r.Findings))
		for _, f := range r.Findings {
			fmt.Fprintln(stdout, "  "+f.String())
		}
		fmt.Fprintln(stdout, "Never commit or ship Nintendo game files or saves: remove them (and check .gitignore).")
		return 1
	}
	fmt.Fprintf(stdout, "No game files: %d file(s) checked.\n", r.Checked)
	return 0
}

// gitTree checks the files of the working tree git lists: tracked, and
// untracked but not ignored.
func gitTree(dir string, r *gamefiles.Result) error {
	top, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return fmt.Errorf("not in a git work tree (pass paths instead): %w", err)
	}
	root := strings.TrimSpace(string(top))
	out, err := exec.Command("git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		return fmt.Errorf("git ls-files: %w", err)
	}
	seen := map[string]bool{}
	for _, name := range bytes.Split(out, []byte{0}) {
		rel := string(name)
		if rel == "" || seen[rel] {
			continue
		}
		seen[rel] = true
		if err := r.File(filepath.Join(root, filepath.FromSlash(rel)), rel); err != nil {
			return err
		}
	}
	return nil
}
