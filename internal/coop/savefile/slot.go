package savefile

import (
	"encoding/binary"
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
)

// State is how the file select screen shows a slot.
type State uint8

const (
	// Corrupt: the slot's checksum fails, "This data is corrupted."
	// (d_file_select.cpp:3121, :3177-3194).
	Corrupt State = 1 + iota
	// NewGame: the checksum holds and the player name is empty, "New
	// Game" (d_file_select.cpp:3133-3158): a slot initdata_to_card made
	// (d_save.cpp:1959-1962 zeroes mInfo) and no one has played.
	NewGame
	// InUse: a played file, shown by its name.
	InUse
)

func (s State) String() string {
	switch s {
	case Corrupt:
		return "corrupted"
	case NewGame:
		return "new game"
	case InUse:
		return "in use"
	}
	return fmt.Sprintf("State(%d)", uint8(s))
}

// Slot is one file slot as the game loads it.
type Slot struct {
	N int // 0..2, mDataNum; file N+1 on the file select screen
	// Copy is the card_savedata copy the game takes the slot from: 1
	// (block 1) or 2 (block 2), after Restore's per-slot repair.
	Copy int
	// Sum is the checksum stored with the slot, Want the one its packed
	// image gives (GameDataSum). The slot is valid when they agree.
	Sum, Want uint64
	State     State
	Packed    Packed
	Image     world.Image // Unpack(&Packed)
}

// Valid reports whether the slot's checksum holds
// (mDoMemCdRWm_TestCheckSumGameData, m_Do_MemCardRWmng.cpp:348-352).
func (s *Slot) Valid() bool { return s.Sum == s.Want }

// PlayerName is the slot's mPlayerName, up to its NUL.
func (s *Slot) PlayerName() string {
	n := s.Image[addr.OffPlayerName : addr.OffPlayerName+17]
	for i, c := range n {
		if c == 0 {
			return string(n[:i])
		}
	}
	return string(n)
}

// gameData returns card_gamedata n of a card_savedata block.
func gameData(sd []byte, n int) []byte {
	off := sdOffGameData + n*GameDataSize
	return sd[off : off+GameDataSize]
}

// gameDataValid is mDoMemCdRWm_TestCheckSumGameData.
func gameDataValid(gd []byte) bool { return be64(gd[gdOffSum:]) == GameDataSum(gd[:PackedSize]) }

// restored returns card_savedata as mDoMemCdRWm_Restore leaves it in the
// game's memory (m_Do_MemCardRWmng.cpp:123-162), and which copy each slot
// came from. It starts from block 1; a slot whose checksum fails there is
// taken from block 2 when block 2's is right. Slot 1 is the exception the
// decomp marks with a TODO: its repair tests block 2's slot 2, not its
// slot 1 (:139-142), and copies block 2's slot 1 whether or not that one
// is valid.
func (f *File) restored() (sd [BlockSize]byte, from [Slots]int) {
	copy(sd[:], f.block(blockSave))
	second := f.block(blockSave2)
	for n := range Slots {
		from[n] = 1
		test := n
		if n == 1 {
			test = 2
		}
		if !gameDataValid(gameData(sd[:], n)) && gameDataValid(gameData(second, test)) {
			copy(gameData(sd[:], n), gameData(second, n))
			from[n] = 2
		}
	}
	return sd, from
}

// Slot returns slot n (0..2) as the game would load it.
func (f *File) Slot(n int) (*Slot, error) {
	if n < 0 || n >= Slots {
		return nil, fmt.Errorf("savefile: slot %d out of range 0..%d", n, Slots-1)
	}
	sd, from := f.restored()
	return slotOf(sd[:], n, from[n]), nil
}

// AllSlots returns the three slots as the game would load them.
func (f *File) AllSlots() [Slots]*Slot {
	sd, from := f.restored()
	var out [Slots]*Slot
	for n := range out {
		out[n] = slotOf(sd[:], n, from[n])
	}
	return out
}

func slotOf(sd []byte, n, copyNo int) *Slot {
	gd := gameData(sd, n)
	s := &Slot{N: n, Copy: copyNo, Sum: be64(gd[gdOffSum:]), Want: GameDataSum(gd[:PackedSize])}
	copy(s.Packed[:], gd)
	s.Image = Unpack(&s.Packed)
	switch {
	case !s.Valid():
		s.State = Corrupt
	case s.Image[addr.OffPlayerName] == 0:
		s.State = NewGame
	default:
		s.State = InUse
	}
	return s
}

// SaveCount is block 1's save_count: how many times this card file has
// been saved since it was made (m_Do_MemCardRWmng.cpp:52; NTSC-U).
func (f *File) SaveCount() uint32 { return be32(f.block(blockSave)[sdOffSaveCount:]) }

// Copies says whether each card_savedata copy's own checksum holds
// (mDoMemCdRWm_CalcCheckSum against csum, m_Do_MemCardRWmng.cpp:56-69).
// Store checks it once it has written each copy; Restore never does, so
// a bad one alone does not stop the game loading the file.
func (f *File) Copies() (first, second bool) {
	ok := func(b []byte) bool { return be32(b[sdOffSum:]) == SaveDataSum(b[:sdOffSum]) }
	return ok(f.block(blockSave)), ok(f.block(blockSave2))
}

// Photos says whether each of slot n's three picture box photos has a
// right checksum (mDoMemCdRWm_TestCheckSumPictData, m_Do_MemCardRWmng.cpp:
// 323-327). An all-zero block, a photo never taken, passes.
func (f *File) Photos(n int) [3]bool {
	var out [3]bool
	if n < 0 || n >= Slots {
		return out
	}
	for i := range out {
		b := f.block(blockPict + 3*n + i)
		out[i] = be16(b[BlockSize-2:]) == PictDataSum(b[:BlockSize-2])
	}
	return out
}

// WithSlot returns a copy of f with slot n holding img, written as the
// in-game save menu writes the card after the game loaded it: the game
// memory holds the restored card_savedata (restored), memory_to_card puts
// the slot's packed image into its card_gamedata and
// mDoMemCdRWm_SetCheckSumGameData its checksum (dataWrite,
// d_menu_save.cpp:1011-1017), then mDoMemCdRWm_Store writes card_savedata
// with save_count one up, data_version 0, the unused bytes zero and its
// own checksum, to block 1 and block 2 alike (m_Do_MemCardRWmng.cpp:
// 47-69). The other slots are as the game loaded them, so a slot the game
// would have repaired from block 2 is repaired. The header block and the
// photos are kept: Store rebuilds the header's banner and the date in its
// comment, cosmetic only, and the photos are the player's own.
//
// It fails when f's entry would make Store wipe the copies first
// (CheckStat), since the game would not have loaded the file either.
func (f *File) WithSlot(n int, img *world.Image) (*File, error) {
	if n < 0 || n >= Slots {
		return nil, fmt.Errorf("savefile: slot %d out of range 0..%d", n, Slots-1)
	}
	if err := f.Header.CheckStat(); err != nil {
		return nil, err
	}
	mem, _ := f.restored()
	gd := gameData(mem[:], n)
	p := Pack(img)
	copy(gd, p[:])
	binary.BigEndian.PutUint64(gd[gdOffSum:], GameDataSum(p[:]))

	var sd [BlockSize]byte
	binary.BigEndian.PutUint32(sd[sdOffSaveCount:], be32(mem[sdOffSaveCount:])+1)
	binary.BigEndian.PutUint32(sd[sdOffDataVersion:], 0)
	copy(sd[sdOffGameData:sdOffPad], mem[sdOffGameData:sdOffPad])
	binary.BigEndian.PutUint32(sd[sdOffSum:], SaveDataSum(sd[:sdOffSum]))

	out := f.Clone()
	copy(out.block(blockSave), sd[:])
	copy(out.block(blockSave2), sd[:])
	return out, nil
}

// Verify checks f as a file the game loads slot n of without repairing
// anything: the entry passes CheckStat, both card_savedata copies'
// checksums hold, the copies are equal, and slot n's checksum holds. A
// file WithSlot wrote for slot n passes.
func (f *File) Verify(n int) error {
	if n < 0 || n >= Slots {
		return fmt.Errorf("savefile: slot %d out of range 0..%d", n, Slots-1)
	}
	if err := f.Header.CheckStat(); err != nil {
		return err
	}
	first, second := f.Copies()
	if !first || !second {
		return formatErr("card_savedata checksum: block 1 %s, block 2 %s", okWord(first), okWord(second))
	}
	if string(f.block(blockSave)) != string(f.block(blockSave2)) {
		return formatErr("the two card_savedata copies differ")
	}
	if !gameDataValid(gameData(f.block(blockSave), n)) {
		return formatErr("slot %d (file %d): checksum wrong", n, n+1)
	}
	return nil
}

func okWord(ok bool) string {
	if ok {
		return "right"
	}
	return "wrong"
}

// New returns a fresh gczelda file as the game makes one on a card that
// has none (memCardMakeGameFileSel, d_menu_save.cpp:837-849, then the
// first Store and SetCardStat): an entry that passes CheckStat, save
// count 1 and three "New Game" slots with their checksums. The slots are
// zero images rather than initdata_to_card's defaults (d_save.cpp:
// 1881-2021), and the header block and photos are blank. For tests and
// tools that build a card from images: WithSlot fills the slots.
func New() *File {
	f := &File{}
	h := &f.Header
	copy(h[hdrOffGameName:], GameCode)
	copy(h[hdrOffCompany:], Company)
	h[hdrOffCompany+2] = 0xFF // CARDDir._padding0: CARDFormat fills the directory with 0xFF (CARDFormat.c:93), CARDCreate leaves it
	h[hdrOffBannerFormat] = 1
	copy(h[hdrOffFileName:], FileName)
	binary.BigEndian.PutUint32(h[hdrOffIconAddr:], 0)
	binary.BigEndian.PutUint16(h[hdrOffIconFormat:], 0x0001)
	binary.BigEndian.PutUint16(h[hdrOffIconSpeed:], 0x0003)
	h[hdrOffPermission] = 4 // CARD_ATTR_PUBLIC, as CARDCreate sets it (CARDCreate.c:26)
	binary.BigEndian.PutUint16(h[hdrOffLength:], FileBlocks)
	binary.BigEndian.PutUint16(h[hdrOffLength+2:], 0xFFFF) // CARDDir._padding1
	binary.BigEndian.PutUint32(h[hdrOffCommentAddr:], commentAddr)

	var sd [BlockSize]byte
	binary.BigEndian.PutUint32(sd[sdOffSaveCount:], 1)
	var zero Packed
	for n := range Slots {
		gd := gameData(sd[:], n)
		binary.BigEndian.PutUint64(gd[gdOffSum:], GameDataSum(zero[:]))
	}
	binary.BigEndian.PutUint32(sd[sdOffSum:], SaveDataSum(sd[:sdOffSum]))
	copy(f.block(blockSave), sd[:])
	copy(f.block(blockSave2), sd[:])
	return f // a zero photo block holds its checksum, 0
}
