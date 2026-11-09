// Package cardtag puts the co-op tag the app wrote into game memory onto
// the player's memory card, so a bind, or a cleared tag, survives without
// an in-game save (docs/coop-just-works.md §6 A; docs/08-coop-design.md
// §5.1 principle 1).
//
// The tag is still written live, in game memory (package bind): that is
// where the file the player loaded is known. Each write is recorded here
// as a pending tag (Store.Record), pinned to the exact card copy of that
// file as the card holds it then: the card file, the file slot, the
// slot's save date and a hash of its whole save image. Only the copy the
// game loaded is recorded: a file loaded from the card (mDateIPL not 0)
// whose card copy has the same name hash and clear count and the very save
// date the game loaded. An in-game save stamps a new date into the card
// copy only (d_save.cpp:1717), so a file saved since the load, or a save
// state of another playthrough of a file of the same name, is left to the
// in-game save. The pending tags wait in a small file in the app's config
// folder, since quitting the app leaves Dolphin running, and are written
// into the card when no Dolphin runs (Store.Flush): when the app starts,
// before it launches or waits for Dolphin, when the Dolphin it plays on
// closes, and when the app quits.
//
// A pending tag reaches the card only when all of these hold, else it is
// dropped and the log says why:
//
//   - no Dolphin runs (a Dolphin keeps its card in memory and writes it
//     back), and the process list can be read; checked again right before
//     the file is replaced;
//   - the card is still the GCI folder Dolphin uses for slot A, the only
//     slot the game uses (mCardSlot 0, m_Do_MemCard.cpp:34), and the file
//     is a Wind Waker card file;
//   - the slot is byte for byte the copy the record pinned: any save,
//     copy or new file in that slot since means the card no longer holds
//     the file as the game had it, and it is left alone. A tag already
//     equal to the pending one (an in-game save carried it) needs no
//     write.
//
// Then: the card is backed up (backup.Create), the new file is built from
// the card's own slot with only the 0x30 tag bytes changed
// (savefile.File.WithSlot, as the in-game save menu writes it), checked
// in memory (Verify, and every slot's image the same but for the tag),
// written through a temporary file and a rename (backup.Replace), and
// read back. A read-back that differs puts the card's old bytes back.
//
// A raw card image (.raw) has no writer: its tag waits for an in-game
// save, as before, and the log says so.
package cardtag

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/backup"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/savefile"
)

const (
	// stateName is the pending tags' file in Store.Dir.
	stateName = "card-tags.json"
	// logName is the log every Record and Flush outcome is appended to,
	// in Store.Dir's logs folder (applog's): at quit there is no session
	// log to write to.
	logName = "card-tags.log"
	// logMax is how large the log grows before it is moved to .1.
	logMax = 512 << 10
	// stateVersion is the state file layout this version writes.
	stateVersion = 2
	// maxAttempts is how many flushes a pending tag may wait through (a
	// write or backup that fails again and again) before it is dropped:
	// each try takes a backup, and Keep rotates the older ones away.
	maxAttempts = 4
	// cardSlot is the memory card slot the game uses (mCardSlot 0).
	cardSlot = "A"

	// BackupLabel is the label of the backup taken before a tag is
	// written to a card.
	BackupLabel = "before writing a co-op tag to the card"
)

// Kinds a pending tag can be.
const (
	KindBind  = "bind"  // a tag a bind wrote
	KindClear = "clear" // zeros a scrub or "Remove the tag" wrote
)

// Pending is one tag write waiting for the memory card.
type Pending struct {
	Kind string `json:"kind"`
	// UserDir is the Dolphin user folder whose card it goes to.
	UserDir string `json:"user_dir"`
	// Card is the card file: a .gci in the folder Dolphin uses for slot
	// A.
	Card string `json:"card"`
	// Slot is the file slot (mDataNum, 0..2): file Slot+1.
	Slot       int    `json:"slot"`
	Name       string `json:"name"`
	NameHash   uint32 `json:"name_hash"`
	ClearCount uint8  `json:"clear_count"`
	// DateIPL is mDateIPL in game memory at the write: the save date of
	// the card copy the game loaded the file from.
	DateIPL uint64 `json:"date_ipl"`
	// CardDate and CardSum pin the card copy the record saw: the slot's
	// save date, and the SHA-256 of its save image as the game loads it.
	CardDate uint64 `json:"card_date"`
	CardSum  string `json:"card_sum"`
	// Write is the 0x30 bytes the card's tag becomes, in hex.
	Write string `json:"write"`
	// World and Nonce are the bind's (KindBind only).
	World    string    `json:"world,omitempty"`
	Nonce    string    `json:"nonce,omitempty"`
	Recorded time.Time `json:"recorded"`
	// Attempts counts the flushes it waited through.
	Attempts int `json:"attempts,omitempty"`
}

func (p Pending) String() string {
	what := "the cleared co-op tag"
	if p.Kind == KindBind {
		what = "the co-op tag of world " + p.World
	}
	return fmt.Sprintf("%s of file %d (%q) on %s", what, p.Slot+1, p.Name, p.Card)
}

type state struct {
	Version int       `json:"version"`
	Pending []Pending `json:"pending"`
}

// Store keeps the pending tags and writes them to the cards. Its methods
// are safe for concurrent use. The zero Store is the app's: the pending
// tags under the app's config folder, the backups under backup.Dir().
type Store struct {
	// Dir holds the pending tags' file and, in its logs folder, their
	// log; "" is <UserConfigDir>/ww-multiplayer.
	Dir string
	// Backups is the backup root; "" is backup.Dir().
	Backups string
	// Procs reports the running Dolphins; nil is backup.Dolphin.
	Procs backup.ProcessFinder
	// Now is the clock; nil is time.Now.
	Now func() time.Time

	mu sync.Mutex
	// forget are files whose newest write could not be recorded or
	// forgotten (the lock was held): the next Flush drops their pending
	// tags before it writes anything.
	forget []fileKey
}

// fileKey names a file the way a pending tag does, in any user folder.
type fileKey struct {
	slot       int
	nameHash   uint32
	clearCount uint8
}

func keyOf(w bind.Written) fileKey {
	return fileKey{int(w.File.Slot), bind.NameHash(w.File.Name[:]), w.File.ClearCount}
}

func (k fileKey) matches(p Pending) bool {
	return p.Slot == k.slot && p.NameHash == k.nameHash && p.ClearCount == k.clearCount
}

func (s *Store) dir() (string, error) {
	if s.Dir != "" {
		return s.Dir, nil
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cardtag: no config folder: %w", err)
	}
	return filepath.Join(cfg, "ww-multiplayer"), nil
}

func (s *Store) procs() backup.ProcessFinder {
	if s.Procs != nil {
		return s.Procs
	}
	return backup.Dolphin
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Pending returns the tags waiting for a card, oldest first.
func (s *Store) Pending() ([]Pending, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// load reads the pending tags; a missing file is none. A file that does
// not parse is moved aside (its tags are lost: each is only a copy of a
// write game memory still has) and reads as none.
func (s *Store) load() ([]Pending, error) {
	dir, err := s.dir()
	if err != nil {
		return nil, err
	}
	p := filepath.Join(dir, stateName)
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cardtag: %w", err)
	}
	var st state
	if err := json.Unmarshal(b, &st); err != nil || st.Version != stateVersion {
		os.Rename(p, p+".bad")
		return nil, fmt.Errorf("cardtag: %s is not a pending-tags file this version reads (moved to %s.bad)", p, stateName)
	}
	return st.Pending, nil
}

// save writes ps, or removes the file when there are none.
func (s *Store) save(ps []Pending) error {
	dir, err := s.dir()
	if err != nil {
		return err
	}
	p := filepath.Join(dir, stateName)
	if len(ps) == 0 {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("cardtag: %w", err)
		}
		return nil
	}
	b, err := json.MarshalIndent(state{Version: stateVersion, Pending: ps}, "", "  ")
	if err != nil {
		return fmt.Errorf("cardtag: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cardtag: %w", err)
	}
	return backup.Replace(p, append(b, '\n'), 0o644)
}

// The lock between app processes (two players on one computer share the
// config folder): an OS lock on a file beside the pending tags (flock, or
// an open with no sharing on Windows), held across each load and save.
// The OS lets go of it when its process ends, so a crash leaves none.
const (
	lockName    = "card-tags.lock"
	lockRetries = 50 * time.Millisecond
)

// lockWait is how long a Record or Flush waits for the lock; a variable
// so a test can shorten it.
var lockWait = 5 * time.Second

// lock takes the lock and returns how to release it.
func (s *Store) lock() (func(), error) {
	dir, err := s.dir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("cardtag: %w", err)
	}
	p := filepath.Join(dir, lockName)
	deadline := time.Now().Add(lockWait)
	for {
		unlock, busy, err := tryLock(p)
		switch {
		case err != nil:
			return nil, fmt.Errorf("cardtag: %w", err)
		case !busy:
			return unlock, nil
		case time.Now().After(deadline):
			return nil, fmt.Errorf("cardtag: another ww-multiplayer holds %s", p)
		}
		time.Sleep(lockRetries)
	}
}

// logf logs to rep (nil: none) and appends the line to the store's log.
func (s *Store) logf(rep log.Reporter, level log.Level, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if rep != nil {
		rep.Log(level, msg)
	}
	dir, err := s.dir()
	if err != nil {
		return
	}
	logs := filepath.Join(dir, "logs")
	if os.MkdirAll(logs, 0o755) != nil {
		return
	}
	p := filepath.Join(logs, logName)
	if fi, err := os.Stat(p); err == nil && fi.Size() > logMax {
		os.Rename(p, p+".1")
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s %s\n", s.now().Format(time.RFC3339), levelWord(level), msg)
}

func levelWord(l log.Level) string {
	switch l {
	case log.Warn:
		return "WARN"
	case log.Err:
		return "ERROR"
	case log.OK:
		return "OK"
	}
	return "INFO"
}

// The tag fields of a slot image.
func imgTag(img *save.Image) []byte { return img[addr.OffReserve : addr.OffReserve+bind.TagSize] }
func imgDate(img *save.Image) uint64 {
	return binary.BigEndian.Uint64(img[addr.OffDateIPL:])
}
func imgNameHash(img *save.Image) uint32 {
	return bind.NameHash(img[addr.OffPlayerName : addr.OffPlayerName+bind.PlayerNameSize])
}
func imgClearCount(img *save.Image) uint8 { return img[addr.OffClearCount] }

// sameFile reports whether slot s holds the file f names: a played file
// of the same name hash and clear count.
func sameFile(s *savefile.Slot, nameHash uint32, clearCount uint8) bool {
	return s.State == savefile.InUse && imgNameHash(&s.Image) == nameHash && imgClearCount(&s.Image) == clearCount
}

// Why a write has no card to go to.
var (
	errRaw       = errors.New("Dolphin keeps this memory card as a raw card image, which the app does not write")
	errNoCard    = errors.New("Dolphin has no GCI-folder memory card in slot A")
	errNotOnCard = errors.New("the file is not on the memory card yet")
	errAmbiguous = errors.New("more than one Wind Waker card file in the memory card folder holds this file")
)

// slotACard returns the card Dolphin uses for slot A of userDir.
func slotACard(userDir string) (backup.Card, error) {
	cards, err := backup.Locate(userDir)
	if err != nil {
		return backup.Card{}, err
	}
	for _, c := range cards {
		if c.Slot == cardSlot && c.InUse == backup.UseYes {
			if c.Kind == backup.Raw {
				return c, errRaw
			}
			return c, nil
		}
	}
	return backup.Card{}, errNoCard
}

// findCard returns the card file of userDir's slot A that holds file
// slot n with this name hash and clear count, and that slot.
func findCard(userDir string, n int, nameHash uint32, clearCount uint8) (string, *savefile.Slot, error) {
	c, err := slotACard(userDir)
	if err != nil {
		return "", nil, err
	}
	files, err := backup.GCIFiles(c.Path)
	if err != nil {
		return "", nil, err
	}
	var match []string
	var slot *savefile.Slot
	for _, p := range files {
		f, kind, err := savefile.Read(p)
		if err != nil || kind != savefile.KindGCI {
			continue
		}
		if s, err := f.Slot(n); err == nil && sameFile(s, nameHash, clearCount) {
			match, slot = append(match, p), s
		}
	}
	switch len(match) {
	case 0:
		return "", nil, errNotOnCard
	case 1:
		return match[0], slot, nil
	}
	return "", nil, fmt.Errorf("%w: %s", errAmbiguous, strings.Join(match, ", "))
}

// imgSum is the SHA-256 of a slot's save image, in hex.
func imgSum(img *save.Image) string { return sha256Hex(img[:]) }

// Record records tag write w, made in the game memory of the Dolphin
// whose user folder is userDir, as a pending tag for that folder's card,
// pinned to the card copy of the file as the card holds it now. It
// replaces a pending tag of the same file (the newest write is what the
// card should hold). A write with no card copy to go to (no GCI card, a
// file the game did not load from the card, a card copy older than the
// one loaded) is logged and not kept.
func (s *Store) Record(userDir string, w bind.Written, rep log.Reporter) {
	if userDir == "" || int(w.File.Slot) >= savefile.Slots {
		return
	}
	if abs, err := filepath.Abs(userDir); err == nil {
		userDir = abs
	}
	n := int(w.File.Slot)
	nameHash := bind.NameHash(w.File.Name[:])
	p := Pending{
		UserDir: userDir, Slot: n, Name: w.File.PlayerName(), NameHash: nameHash, ClearCount: w.File.ClearCount,
		DateIPL: w.File.DateIPL, Write: hex.EncodeToString(w.Bytes[:]), Kind: KindClear, Recorded: s.now().UTC(),
	}
	if w.Kind == bind.KindBind {
		p.Kind, p.World, p.Nonce = KindBind, w.Tag.World.String(), w.Tag.Nonce.String()
	}
	var cardErr error
	if w.File.DateIPL == 0 {
		cardErr = errNotOnCard // never loaded from the card since boot: which card copy it is can't be told
	} else {
		var sl *savefile.Slot
		if p.Card, sl, cardErr = findCard(userDir, n, nameHash, w.File.ClearCount); cardErr == nil {
			p.CardDate, p.CardSum = imgDate(&sl.Image), imgSum(&sl.Image)
			if p.CardDate != w.File.DateIPL {
				// Not the copy the game loaded: saved in game since (the card
				// copy is newer), or a save state of another playthrough of a
				// file of this name. Save dates can't tell those apart.
				cardErr = errNotOnCard
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lock()
	if err != nil {
		s.forget = append(s.forget, keyOf(w))
		s.logf(rep, log.Warn, "co-op: could not keep the pending co-op tag: %v. Save in game so your memory card keeps it.", err)
		return
	}
	defer unlock()
	ps, err := s.load()
	if err != nil {
		s.logf(rep, log.Warn, "co-op: %v", err)
	}
	ps = slices.DeleteFunc(ps, func(q Pending) bool {
		return backup.SamePath(q.UserDir, userDir) && q.Slot == n && q.NameHash == nameHash && q.ClearCount == w.File.ClearCount
	})
	if cardErr == nil {
		ps = append(ps, p)
	}
	if err := s.save(ps); err != nil {
		s.logf(rep, log.Warn, "co-op: could not keep the pending co-op tag: %v. Save in game so your memory card keeps it.", err)
		return
	}
	s.unforget(keyOf(w)) // this newest write is what the card should hold now
	switch {
	case cardErr == nil && p.Kind == KindBind:
		s.logf(rep, log.Info, "co-op: the tag goes onto your memory card (file %d, %s) as soon as no Dolphin runs: no in-game save needed.", n+1, p.Card)
	case cardErr == nil:
		s.logf(rep, log.Info, "co-op: the cleared tag goes onto your memory card (file %d, %s) as soon as no Dolphin runs.", n+1, p.Card)
	case errors.Is(cardErr, errNotOnCard):
		s.logf(rep, log.Info, "co-op: file %d (%q) as loaded is not on your memory card: save in game so the card keeps the tag.", n+1, p.Name)
	default:
		s.logf(rep, log.Info, "co-op: the app can't put this tag onto your memory card (%v): save in game so the card keeps it.", cardErr)
	}
}

// Forget drops the pending tags of the file w wrote to, in every user
// folder: a newer write to it that can't be recorded (its Dolphin's user
// folder can't be told) makes an older pending tag of it wrong for the
// card. Since the folder is unknown, a file of the same slot, name and
// clear count in another folder loses its pending tag too: the safe side,
// as that file's tag then waits for its in-game save.
func (s *Store) Forget(w bind.Written, rep log.Reporter) {
	k := keyOf(w)
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lock()
	if err != nil {
		s.forget = append(s.forget, k)
		return
	}
	defer unlock()
	ps, err := s.load()
	if err != nil {
		s.logf(rep, log.Warn, "co-op: %v", err)
	}
	n := len(ps)
	if ps = slices.DeleteFunc(ps, k.matches); len(ps) != n {
		if err := s.save(ps); err != nil {
			s.forget = append(s.forget, k)
			return
		}
		s.logf(rep, log.Info, "co-op: dropped the earlier pending co-op tag of file %d: a newer change to it can't reach the card.", k.slot+1)
	}
	s.unforget(k)
}

// unforget takes k off the forget list: the store now holds what the card
// should get for that file.
func (s *Store) unforget(k fileKey) {
	s.forget = slices.DeleteFunc(s.forget, func(f fileKey) bool { return f == k })
}

// Flush writes the pending tags into their cards, if no Dolphin runs, and
// keeps only the ones that must wait (a Dolphin started meanwhile, the
// backup or the file write failed), each at most maxAttempts flushes.
// Every outcome is logged to rep (nil: the store's log alone).
func (s *Store) Flush(rep log.Reporter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lock()
	if err != nil {
		s.logf(rep, log.Warn, "co-op: the pending co-op tags wait: %v", err)
		return
	}
	defer unlock()
	ps, err := s.load()
	if err != nil {
		s.logf(rep, log.Warn, "co-op: %v", err)
	}
	if len(s.forget) > 0 {
		for _, k := range s.forget {
			ps = slices.DeleteFunc(ps, k.matches)
		}
		if err := s.save(ps); err != nil {
			s.logf(rep, log.Warn, "co-op: could not update the pending co-op tags: %v", err)
			return
		}
		s.forget = nil
	}
	if len(ps) == 0 {
		return
	}
	if err := backup.CheckClosed(s.procs()); err != nil {
		s.logf(rep, log.Info, "co-op: %d co-op tag(s) wait for Dolphin to close before they go onto the memory card (%v).", len(ps), err)
		return
	}
	fl := &flush{s: s, rep: rep, backups: map[string]*backup.Backup{}, wrote: map[string][]byte{}}
	var left []Pending
	for _, p := range ps {
		if !fl.one(p) {
			continue
		}
		if p.Attempts++; p.Attempts >= maxAttempts {
			s.logf(rep, log.Warn, "co-op: %s is dropped after %d tries. Save in game so the card keeps the tag.", p, p.Attempts)
			continue
		}
		left = append(left, p)
	}
	if err := s.save(left); err != nil {
		s.logf(rep, log.Warn, "co-op: could not update the pending co-op tags: %v", err)
	}
}

// flush is one Flush's state: the backup taken of each user folder, and
// what it wrote to each card file.
type flush struct {
	s       *Store
	rep     log.Reporter
	backups map[string]*backup.Backup
	wrote   map[string][]byte
}

// The outcome of one pending tag.
const (
	drop = false
	keep = true
)

func (fl *flush) dropf(p Pending, format string, args ...any) bool {
	fl.s.logf(fl.rep, log.Warn, "co-op: %s stays off the memory card: %s. Save in game so the card keeps the tag.", p, fmt.Sprintf(format, args...))
	return drop
}

func (fl *flush) waitf(p Pending, format string, args ...any) bool {
	fl.s.logf(fl.rep, log.Warn, "co-op: %s waits: %s. The app tries again later.", p, fmt.Sprintf(format, args...))
	return keep
}

// one writes p into its card; it reports whether p must wait.
func (fl *flush) one(p Pending) bool {
	write, err1 := hex.DecodeString(p.Write)
	if err1 != nil || len(write) != bind.TagSize || (p.Kind != KindBind && p.Kind != KindClear) || p.Slot < 0 || p.Slot >= savefile.Slots || p.CardSum == "" {
		return fl.dropf(p, "the pending record is damaged")
	}
	if p.Kind == KindBind {
		if t, err := bind.Decode(write); err != nil || t.World.String() != p.World || t.Nonce.String() != p.Nonce || t.NameHash != p.NameHash {
			return fl.dropf(p, "the pending tag does not decode as the bind it records")
		}
	} else if !bytes.Equal(write, make([]byte, bind.TagSize)) {
		return fl.dropf(p, "a cleared tag must be zeros")
	}

	// The card must still be the one Dolphin uses for slot A.
	c, err := slotACard(p.UserDir)
	switch {
	case errors.Is(err, errRaw):
		return fl.dropf(p, "Dolphin now keeps slot A as a raw card image (%s)", c.Path)
	case err != nil:
		return fl.dropf(p, "%v", err)
	case !backup.SamePath(filepath.Dir(p.Card), c.Path) || !strings.EqualFold(filepath.Ext(p.Card), ".gci"):
		return fl.dropf(p, "Dolphin's slot A card is now %s", c.Path)
	}
	fi, err := os.Lstat(p.Card)
	if err != nil || !fi.Mode().IsRegular() {
		return fl.dropf(p, "the card file is gone or not a regular file")
	}
	old, err := os.ReadFile(p.Card)
	if err != nil {
		return fl.waitf(p, "reading the card file: %v", err)
	}
	f, err := savefile.Parse(old)
	if err != nil {
		return fl.dropf(p, "%v", err)
	}

	// The slot must still be the card copy the record pinned.
	sl, err := f.Slot(p.Slot)
	if err != nil {
		return fl.dropf(p, "%v", err)
	}
	if bytes.Equal(imgTag(&sl.Image), write) && sameFile(sl, p.NameHash, p.ClearCount) {
		fl.s.logf(fl.rep, log.OK, "co-op: %s is already on the memory card (an in-game save put it there): nothing to write.", p)
		return drop
	}
	if imgSum(&sl.Image) != p.CardSum || imgDate(&sl.Image) != p.CardDate || sl.State != savefile.InUse || !sameFile(sl, p.NameHash, p.ClearCount) {
		return fl.dropf(p, "file %d on the card changed since (saved, copied over or replaced by another file): it is left as it is", p.Slot+1)
	}

	// The new file: the card's own slot with only the tag changed.
	img := sl.Image
	copy(imgTag(&img), write)
	nf, err := f.WithSlot(p.Slot, &img)
	if err != nil {
		return fl.dropf(p, "%v", err)
	}
	if err := OnlyTag(f, nf, p.Slot, write); err != nil {
		return fl.dropf(p, "the new card file failed its check: %v", err)
	}
	enc := nf.Encode()

	// A backup of the card as it is, taken once per user folder.
	bk := fl.backups[p.UserDir]
	if bk == nil {
		if bk, err = backup.Create([]string{p.UserDir}, backup.Options{Root: fl.s.Backups, Label: BackupLabel}); err != nil {
			return fl.waitf(p, "the memory card could not be backed up first: %v", err)
		}
		fl.backups[p.UserDir] = bk
	}
	if !backedUp(bk, p.Card, old) && !bytes.Equal(fl.wrote[p.Card], old) {
		return fl.waitf(p, "the card changed while it was backed up")
	}

	if err := backup.CheckClosed(fl.s.procs()); err != nil {
		return fl.waitf(p, "%v", err)
	}
	if err := replaceCard(p.Card, enc, fi.Mode().Perm()); err != nil {
		return fl.waitf(p, "writing the card file: %v", err)
	}
	if err := readBack(p.Card, enc, p.Slot); err != nil {
		if rerr := replaceCard(p.Card, old, fi.Mode().Perm()); rerr != nil {
			fl.s.logf(fl.rep, log.Err, "co-op: writing %s did not read back (%v), and putting the card back failed too (%v). With Dolphin closed, `ww-multiplayer coop-world restore %s --yes` puts the card back from backup %s.",
				p, err, rerr, bk.ID, bk.Dir)
			return drop
		}
		fl.s.logf(fl.rep, log.Err, "co-op: writing %s did not read back (%v): the card was put back as it was (backup %s).", p, err, bk.Dir)
		return drop
	}
	fl.wrote[p.Card] = enc
	fl.s.logf(fl.rep, log.OK, "co-op: wrote %s: only the tag changed, and it reads back right. The card as it was is in backup %s.", p, bk.Dir)
	return drop
}

// replaceCard writes a card file (backup.Replace); a variable so a test
// can make a write go wrong.
var replaceCard = backup.Replace

// backedUp reports whether bk holds card with exactly the bytes b.
func backedUp(bk *backup.Backup, card string, b []byte) bool {
	for _, f := range bk.Files {
		if backup.SamePath(f.Source, card) {
			return f.Size == int64(len(b)) && f.SHA256 == sha256Hex(b)
		}
	}
	return false
}

// readBack checks the card file at path holds enc, a GCI whose slot n
// the game loads without repairing anything.
func readBack(path string, enc []byte, n int) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(b, enc) {
		return errors.New("the file holds other bytes than were written")
	}
	f, err := savefile.Parse(b)
	if err != nil {
		return err
	}
	return f.Verify(n)
}

// OnlyTag checks that nf is old with slot n's tag set to tag and nothing
// else of the save changed: nf verifies for slot n (Verify), every slot
// loads as it did in old (the same image and state) but for slot n's tag
// bytes, which hold tag, and the card entry, the banner block and the
// photos are byte for byte old's. What WithSlot also changes, as the game's
// own save does, is the save count and the two save blocks' checksums.
func OnlyTag(old, nf *savefile.File, n int, tag []byte) error {
	if err := nf.Verify(n); err != nil {
		return err
	}
	if nf.Header != old.Header {
		return errors.New("the card entry changed")
	}
	const bs = savefile.BlockSize
	if !bytes.Equal(nf.Data[:bs], old.Data[:bs]) || !bytes.Equal(nf.Data[3*bs:], old.Data[3*bs:]) {
		return errors.New("the banner block or the photos changed")
	}
	was, now := old.AllSlots(), nf.AllSlots()
	for m := range savefile.Slots {
		want := was[m].Image
		if m == n {
			copy(imgTag(&want), tag)
			if !now[m].Valid() {
				return fmt.Errorf("file %d: checksum wrong", m+1)
			}
		} else if now[m].State != was[m].State {
			return fmt.Errorf("file %d: was %s, now %s", m+1, was[m].State, now[m].State)
		}
		if now[m].Image != want {
			return fmt.Errorf("file %d: more than the tag changed", m+1)
		}
	}
	return nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
