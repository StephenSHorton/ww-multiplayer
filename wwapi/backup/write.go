package backup

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// The helpers a card writer needs (mods/coop/cardtag, which puts the
// co-op tag onto a GCI card while no Dolphin runs): the Wind Waker files
// of a GCI folder, the closed-Dolphin check Restore makes, and the
// temporary-file write Restore uses.

// GCIFiles lists the Wind Waker saves in a GCI folder: the regular .gci
// files whose header starts with the game code GZLE, sorted. A missing
// folder has none.
func GCIFiles(dir string) ([]string, error) { return gciFiles(dir) }

// CheckClosed returns nil only when pf says no Dolphin runs: a Dolphin
// keeps its memory card in memory and writes it back over the file, and
// a process list that can't be read counts as one running
// (ErrDolphinRunning).
func CheckClosed(pf ProcessFinder) error { return checkClosed(pf) }

// SamePath compares two paths as Locate does: ignoring case on Windows,
// exactly elsewhere.
func SamePath(a, b string) bool { return samePath(a, b) }

// Replace writes b over path with permissions perm: to a temporary file
// beside it, named so no Dolphin would take it for a card (it ends in
// neither .gci nor .raw), synced and closed, then renamed over path, with
// Windows sharing errors retried, and the folder synced where the OS
// allows it. On any failure path is untouched and the temporary file is
// removed.
func Replace(path string, b []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.write")
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	tmp := f.Name()
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, perm)
	}
	if err == nil {
		err = replaceFile(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("backup: %w", err)
	}
	if d, err := os.Open(dir); err == nil { // Windows can't open a folder for it; its renames are journaled
		d.Sync()
		d.Close()
	}
	return nil
}

// replaceFile is the rename Replace makes; a variable so a test can make
// it fail.
var replaceFile = renameRetry
