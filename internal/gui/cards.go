package gui

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/backup"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/savefile"
)

// The card-file preview (App PR 9, design §2.5 "Which save file"): a
// binding is per loaded file, so the file is chosen in game, but before
// launch each app reads its player's memory card offline (backup.Locate
// on the player's Dolphin user folder, then savefile.Read) and shows the
// three files with their name, hearts and bind state for the session's
// world, with a hint ("Load File 2: it's bound to this world"). A short
// form of the hint goes to the lobby as this member's file hint, so the
// host sees everyone's. It is a hint only: the card may change while
// Dolphin runs, and the binding decision stays in game.

// CardFileView is one file slot of the card.
type CardFileView struct {
	Slot  int    `json:"slot"`  // 1-3, as the file select screen numbers them
	State string `json:"state"` // "empty", "corrupt" or "used"
	Name  string `json:"name"`
	// Hearts is the file's heart containers (max life in whole hearts,
	// a piece short of one counting down).
	Hearts int `json:"hearts"`
	// Bind is the file's bind state for the session's world: "this"
	// (bound to it), "other" (bound to another world), "unbound",
	// "stale" (a leaked tag, which co-op offers to scrub), "newer" (a tag
	// a newer build wrote) or "" for an empty slot; BindText says it in
	// words, World names the other world.
	Bind     string `json:"bind"`
	BindText string `json:"bindText"`
	World    string `json:"world,omitempty"`
}

// CardsView is the preview of this player's card.
type CardsView struct {
	// Card is the save file read ("" when none was found).
	Card  string         `json:"card"`
	Files []CardFileView `json:"files"`
	// Hint says which file to load; Short is its lobby form.
	Hint  string `json:"hint"`
	Short string `json:"short"`
	// Err says why the card couldn't be read ("" when it was).
	Err string `json:"err,omitempty"`
}

// cardSave is one Wind Waker save found on a card.
type cardSave struct {
	path string
	file *savefile.File
}

// findSave returns the Wind Waker save of the cards Dolphin uses in
// userDir (the ones Dolphin's settings select, else any), or why none.
func findSave(userDir string) (cardSave, error) {
	if userDir == "" {
		return cardSave{}, fmt.Errorf("no Dolphin user folder is set (Setup)")
	}
	cards, err := backup.Locate(userDir)
	if err != nil {
		return cardSave{}, err
	}
	var inUse, rest []backup.Card
	for _, c := range cards {
		if c.InUse == backup.UseYes {
			inUse = append(inUse, c)
		} else {
			rest = append(rest, c)
		}
	}
	for _, c := range append(inUse, rest...) {
		var paths []string
		switch c.Kind {
		case backup.GCIFolder:
			ents, err := os.ReadDir(c.Path)
			if err != nil {
				continue
			}
			for _, e := range ents {
				if e.Type().IsRegular() && strings.EqualFold(filepath.Ext(e.Name()), ".gci") {
					paths = append(paths, filepath.Join(c.Path, e.Name()))
				}
			}
		default:
			paths = []string{c.Path}
		}
		for _, p := range paths {
			if f, _, err := savefile.Read(p); err == nil {
				return cardSave{path: p, file: f}, nil
			}
		}
	}
	return cardSave{}, fmt.Errorf("no Wind Waker save on the memory cards in %s", userDir)
}

// slotBindFile is slot n of image img as the tag rules see the file
// loaded from it (package main's slotFile).
func slotBindFile(n int, img *world.Image) bind.File {
	f := bind.File{Slot: uint8(n), ClearCount: img[addr.OffClearCount], DateIPL: binary.BigEndian.Uint64(img[addr.OffDateIPL:])}
	copy(f.Tag[:], img[addr.OffReserve:])
	copy(f.Name[:], img[addr.OffPlayerName:])
	return f
}

// previewOf is the card preview of save s for the session's co-op
// setting coop ("new" or a world ID); host says this player founds a new
// world from its file.
func previewOf(s cardSave, coop string, host bool) CardsView {
	v := CardsView{Card: s.path, Files: []CardFileView{}}
	var wid bind.WorldID
	if coop != "new" {
		wid, _ = bind.ParseWorldID(coop)
	}
	for _, sl := range s.file.AllSlots() {
		fv := CardFileView{Slot: sl.N + 1}
		switch sl.State {
		case savefile.Corrupt:
			fv.State, fv.BindText = "corrupt", "This data is corrupted."
		case savefile.NewGame:
			fv.State, fv.BindText = "empty", "New game"
		default:
			fv.State, fv.Name = "used", strings.TrimSpace(sl.PlayerName())
			fv.Hearts = int(binary.BigEndian.Uint16(sl.Image[addr.OffMaxLife:])) / 4
			fv.Bind, fv.BindText, fv.World = bindState(slotBindFile(sl.N, &sl.Image), wid)
		}
		v.Files = append(v.Files, fv)
	}
	v.Hint, v.Short = cardHint(v.Files, coop == "new", host)
	return v
}

// bindState is file f's bind state for world wid (zero for a world not
// founded yet).
func bindState(f bind.File, wid bind.WorldID) (state, text, other string) {
	if f.ClearCount != 0 {
		return "newer", "A second quest: co-op doesn't take it.", ""
	}
	v := bind.Classify(f, bind.Expect{World: wid})
	switch v.State {
	case bind.Bound:
		if wid.IsZero() {
			return "other", "Bound to world " + v.Tag.World.String() + ".", v.Tag.World.String()
		}
		return "this", "Bound to this world.", ""
	case bind.Foreign:
		if v.HasTag {
			return "other", "Bound to another world, " + v.Tag.World.String() + ".", v.Tag.World.String()
		}
		return "newer", "Has a tag a newer version wrote.", ""
	case bind.Leaked:
		return "stale", "Carries a stale co-op tag; you'll be asked to remove it.", ""
	}
	return "unbound", "Not bound yet.", ""
}

// cardHint says which file to load, at length and in the lobby's short
// form.
func cardHint(files []CardFileView, newWorld, host bool) (hint, short string) {
	pick := func(state string) (CardFileView, bool) {
		for _, f := range files {
			if f.Bind == state {
				return f, true
			}
		}
		return CardFileView{}, false
	}
	label := func(f CardFileView) string { return fmt.Sprintf("File %d “%s”", f.Slot, f.Name) }
	// The lobby's form is plain ASCII: the lobby keeps file hints short
	// and printable.
	tag := func(f CardFileView) string { return fmt.Sprintf("File %d %q", f.Slot, f.Name) }
	if newWorld && host {
		if f, ok := pick("unbound"); ok {
			return "Load the file you want to share, " + label(f) + " say, and stand in game: the new world is founded from it.", tag(f) + ": unbound"
		}
		if f, ok := pick("other"); ok {
			return "Load the file you want to share: " + label(f) + " is bound to another world, so the app offers to remove its tag first.", tag(f) + ": bound elsewhere"
		}
		return "Load the file you want to share and stand in game: the new world is founded from it.", "founds the world"
	}
	if f, ok := pick("this"); ok {
		return "Load " + label(f) + ": it's bound to this world.", tag(f) + ": bound"
	}
	for _, st := range []string{"unbound", "stale"} {
		if f, ok := pick(st); ok {
			return label(f) + " isn't bound to this world yet: load it and you'll be asked to bind it.", tag(f) + ": unbound"
		}
	}
	if f, ok := pick("other"); ok {
		return label(f) + " is bound to another world: load it and the app offers to remove its tag so it can bind here.", tag(f) + ": bound elsewhere"
	}
	return "No file on your card can join yet: start a new game on an empty file, then load it.", "no file yet"
}

// cardCache keeps the last preview for a while: State is read every
// second, the card changes rarely.
type cardCache struct {
	mu        sync.Mutex
	key       string
	at        time.Time
	view      *CardsView
	short     string     // the file hint last sent to the lobby
	shortSess session.ID // the session short was sent in
}

const cardTTL = 3 * time.Second

// cards is this player's card preview for coop, cached.
func (b *Bridge) cards(userDir, coop string, host bool) *CardsView {
	key := fmt.Sprintf("%s|%s|%v", userDir, coop, host)
	now := b.now()
	b.cardc.mu.Lock()
	defer b.cardc.mu.Unlock()
	if b.cardc.view != nil && b.cardc.key == key && now.Sub(b.cardc.at) < cardTTL {
		v := *b.cardc.view
		return &v
	}
	var v CardsView
	if s, err := b.findSave(userDir); err != nil {
		v = CardsView{Files: []CardFileView{}, Err: err.Error(), Hint: "Your memory card couldn't be read; load your file in game and co-op says what it makes of it.", Short: "card not read"}
	} else {
		v = previewOf(s, coop, host)
	}
	b.cardc.key, b.cardc.at, b.cardc.view = key, now, &v
	out := v
	return &out
}

// noteFileHint sends short to the lobby as this member's file hint when
// it changed.
func (b *Bridge) noteFileHint(id session.ID, short string) {
	b.cardc.mu.Lock()
	if b.cardc.shortSess != id {
		b.cardc.shortSess, b.cardc.short = id, "" // a new lobby has no hint of ours yet
	}
	same := b.cardc.short == short
	b.cardc.mu.Unlock()
	if same || b.ctrl.SetFile(short) != nil {
		return
	}
	b.cardc.mu.Lock()
	b.cardc.short = short
	b.cardc.mu.Unlock()
}
