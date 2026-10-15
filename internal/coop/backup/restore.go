package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/dolphin"
)

// ProcessFinder reports the running Dolphin processes. Restore asks it
// before touching a card; tests give it a fake.
type ProcessFinder interface {
	// DolphinPIDs returns the PIDs of every running Dolphin: none when
	// none runs, and an error when it can't tell.
	DolphinPIDs() ([]uint32, error)
}

// Dolphin is the ProcessFinder over internal/dolphin's process list: the
// processes whose executable name contains "Dolphin", as Find and mp-local
// count them (which takes in Dolphin's own tools too).
var Dolphin ProcessFinder = dolphinProcs{}

type dolphinProcs struct{}

func (dolphinProcs) DolphinPIDs() ([]uint32, error) {
	pids, err := dolphin.ListPIDs()
	// ListPIDs reports "none running" only as an error saying the process
	// was not found (listProcessesByName); any other error (no process
	// list on this platform, a failed snapshot) means it can't tell.
	if err != nil && strings.Contains(err.Error(), "not found") {
		return nil, nil
	}
	return pids, err
}

// ErrDolphinRunning is Restore's error while a Dolphin process runs, or
// when it can't tell whether one does.
var ErrDolphinRunning = errors.New("backup: close Dolphin before restoring a memory card")

// checkClosed returns nil only when pf says no Dolphin runs.
func checkClosed(pf ProcessFinder) error {
	pids, err := pf.DolphinPIDs()
	if err != nil {
		return fmt.Errorf("%w: can't tell whether it is running: %v", ErrDolphinRunning, err)
	}
	if len(pids) > 0 {
		return fmt.Errorf("%w: it is running (pid %v); it keeps the card in memory and would write it back over the restored file", ErrDolphinRunning, pids)
	}
	return nil
}

// RestoreReport is what Restore did.
type RestoreReport struct {
	// Restored are the files written back, from the backup.
	Restored []File
	// Safety is the backup of the cards as they were just before, which
	// undoes the restore; nil when there were none to back up.
	Safety *Backup
	// Left are Wind Waker .gci files in a restored GCI folder that the
	// backup doesn't have. They are left in place.
	Left []string
}

// Restore copies backup b back over the cards it was taken from (§5.4),
// in this order, stopping at the first failure:
//
//  1. pf must report no Dolphin running (Dolphin would overwrite the
//     restored card from its own copy in memory);
//  2. every copy in b must match its recorded hash;
//  3. every source must still be a place Dolphin keeps a card of one of
//     b's user directories, per Locate's rules and today's Dolphin.ini,
//     so a damaged or edited manifest can't aim the restore anywhere
//     else;
//  4. the cards as they are now are backed up (RestoreReport.Safety),
//     with b kept out of the rotation;
//  5. pf is asked again, and each file is written through a temporary
//     file renamed over the source.
//
// Only the files the backup holds are written. A raw card is a whole
// card: restoring it rolls back every game's saves on it.
func Restore(b *Backup, pf ProcessFinder, opt Options) (*RestoreReport, error) {
	if err := checkClosed(pf); err != nil {
		return nil, err
	}
	if err := b.Verify(); err != nil {
		return nil, err
	}
	if err := checkTargets(b); err != nil {
		return nil, err
	}
	rep := new(RestoreReport)
	safety := opt
	safety.Label = "before restoring " + b.ID
	safety.protect = b.ID
	s, err := Create(b.UserDirs, safety)
	switch {
	case errors.Is(err, ErrNoCards):
	case err != nil:
		return nil, fmt.Errorf("backup: not restoring: the cards as they are could not be backed up first: %w", err)
	default:
		rep.Safety = s
	}
	if err := checkClosed(pf); err != nil {
		return rep, err
	}
	for _, f := range b.Files {
		if err := restoreFile(b, f); err != nil {
			return rep, err
		}
		rep.Restored = append(rep.Restored, f)
	}
	rep.Left = leftBehind(b)
	return rep, nil
}

// checkTargets checks every source in b is a card location of b's user
// directories: a raw image of a candidate's family, or a .gci file
// directly inside a candidate GCI folder.
func checkTargets(b *Backup) error {
	var cands []candidate
	for _, ud := range b.UserDirs {
		cs, err := candidates(ud)
		if err != nil {
			return err
		}
		cands = append(cands, cs...)
	}
	for _, f := range b.Files {
		ok := false
		for _, c := range cands {
			switch c.Kind {
			case Raw:
				ok = c.raw.match(f.Source)
			case GCIFolder:
				ok = samePath(filepath.Dir(f.Source), c.Path) && strings.EqualFold(filepath.Ext(f.Source), ".gci")
			}
			if ok {
				break
			}
		}
		if !ok {
			return fmt.Errorf("backup: not restoring: %s is no longer where Dolphin keeps a card for %s (did Dolphin.ini change?)",
				f.Source, strings.Join(b.UserDirs, ", "))
		}
	}
	return nil
}

// restoreFile writes f's copy over f.Source: to a temporary file beside
// it, synced and checked against the recorded hash, then renamed into
// place.
func restoreFile(b *Backup, f File) error {
	dir := filepath.Dir(f.Source)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(f.Source)+".*.restore")
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	name := tmp.Name()
	tmp.Close()
	os.Remove(name) // copyFile creates it afresh, exclusively
	n, sum, err := copyFile(filepath.Join(b.Dir, filepath.FromSlash(f.Stored)), name, 0o644)
	if err != nil {
		return err
	}
	if n != f.Size || sum != f.SHA256 {
		os.Remove(name)
		return fmt.Errorf("backup: %s changed while it was restored", f.Stored)
	}
	if err := renameRetry(name, f.Source); err != nil {
		os.Remove(name)
		return fmt.Errorf("backup: %w", err)
	}
	return nil
}

// leftBehind lists the Wind Waker .gci files now in b's GCI folders that
// b doesn't hold.
func leftBehind(b *Backup) []string {
	have := map[string]bool{}
	var dirs []string
	for _, f := range b.Files {
		have[pathKey(filepath.Clean(f.Source))] = true
		if b.Cards[f.Card].Kind == GCIFolder && !slices.Contains(dirs, b.Cards[f.Card].Path) {
			dirs = append(dirs, b.Cards[f.Card].Path)
		}
	}
	var out []string
	for _, d := range dirs {
		files, _ := gciFiles(d)
		for _, p := range files {
			if !have[pathKey(filepath.Clean(p))] {
				out = append(out, p)
			}
		}
	}
	return out
}
