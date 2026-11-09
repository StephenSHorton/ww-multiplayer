package backup

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/savefile"
)

// FreshSaveName is the file EnsureFreshSave writes, the name Dolphin gives
// a GCI-folder save of company 01, game GZLE and file gczelda.
const FreshSaveName = "01-GZLE-gczelda.gci"

// EnsureFreshSave gives a Dolphin profile that has no Wind Waker save yet
// the empty one the game would otherwise ask to create ("Would you like to
// create a save file ... Slot A?", then "A save file has been created",
// and on No "The game cannot be saved. Continue anyway?"). It writes
// savefile.New, the file the game makes on the Yes path, into slot A's GCI
// folder (the folder Dolphin uses, the default one or Dolphin.ini's), and
// returns its path.
//
// It touches nothing that exists: it does nothing, and returns "", when
// slot A is not a GCI folder (a raw card, or no card, is the player's
// own), or the folder already holds any .gci file (a save of any game),
// or the file is there. A folder that doesn't exist yet is made.
func EnsureFreshSave(userDir string) (string, error) {
	cs, err := candidates(userDir)
	if err != nil {
		return "", err
	}
	var dir string
	for _, c := range cs {
		if c.Slot == "A" && c.Kind == GCIFolder && c.InUse == UseYes && !c.guess {
			dir = c.Path
			break
		}
	}
	if dir == "" {
		return "", nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("backup: %w", err)
	}
	for _, e := range ents {
		if strings.EqualFold(filepath.Ext(e.Name()), ".gci") {
			return "", nil
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}
	path := filepath.Join(dir, FreshSaveName)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", nil
		}
		return "", fmt.Errorf("backup: %w", err)
	}
	_, werr := f.Write(savefile.New().Encode())
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(path)
		return "", fmt.Errorf("backup: %w", werr)
	}
	return path, nil
}
