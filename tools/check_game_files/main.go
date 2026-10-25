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
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/gamefiles"
)

func main() {
	var r gamefiles.Result
	var err error
	if len(os.Args) > 1 {
		for _, p := range os.Args[1:] {
			if err = r.Tree(p); err != nil {
				break
			}
		}
	} else {
		err = gitTree(&r)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "check_game_files:", err)
		os.Exit(2)
	}
	if len(r.Findings) > 0 {
		fmt.Printf("Game files found (%d):\n", len(r.Findings))
		for _, f := range r.Findings {
			fmt.Println("  " + f.String())
		}
		fmt.Println("Never commit or ship Nintendo game files or saves: remove them (and check .gitignore).")
		os.Exit(1)
	}
	fmt.Printf("No game files: %d file(s) checked.\n", r.Checked)
}

// gitTree checks the files of the working tree git lists: tracked, and
// untracked but not ignored.
func gitTree(r *gamefiles.Result) error {
	top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
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
