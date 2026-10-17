package fakegame

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// The memory card as the fake keeps it: one packed save image per file
// slot. Checksums, the header block and the mirror copy are left out.
const (
	// CardSlots is the number of files on a card (card_savedata,
	// m_Do_MemCardRWmng.h:35-42).
	CardSlots = 3

	// PackedSize is one file's image on the card: dSv_save_c without its
	// alignment padding (dSv_save_c_PACKED, STATIC_ASSERT 0x768 at
	// d_save.h:932).
	PackedSize = 0x768
)

// member is one struct memory_to_card and card_to_memory copy, in the
// order they copy them (d_save.cpp:1703-1791, 1794-1867). The packed image
// is the members back to back; the RAM image's padding bytes are never
// copied either way. init marks the members dSv_save_c::init resets
// (d_save.cpp:1494-1503, with dSv_player_c::init at d_save.cpp:1036-1051):
// it skips mPriest, mStatusC and mReserve.
type member struct {
	name      string
	off, size int // in the 0x778-byte RAM image
	init      bool
}

var members = []member{
	{"status_a", addr.OffStatusA, addr.StatusASize, true},
	{"status_b", addr.OffStatusB, addr.StatusBSize, true},
	{"return_place", addr.OffReturnPlace, addr.ReturnPlaceSize, true},
	{"item", addr.OffItems, addr.ItemsSize, true},
	{"get_item", addr.OffGetItem, addr.GetItemSize, true},
	{"item_record", addr.OffItemRecord, addr.ItemRecordSize, true},
	{"item_max", addr.OffItemMax, addr.ItemMaxSize, true},
	{"bag_item", addr.OffBagItem, addr.BagItemSize, true},
	{"get_bag_item", addr.OffGetBagItem, addr.GetBagItemSize, true},
	{"bag_item_record", addr.OffBagItemRecord, addr.BagItemRecordSize, true},
	{"collect", addr.OffCollect, addr.CollectSize, true},
	{"map", addr.OffMap, addr.MapSize, true},
	{"info", addr.OffInfo, addr.InfoSize, true},
	{"config", addr.OffConfig, addr.ConfigSize, true},
	{"priest", addr.OffPriest, addr.PriestSize, false},
	{"status_c", addr.OffStatusC, addr.StatusCCount * addr.StatusCSize, false},
	{"memory", addr.OffMemory, addr.TableCount * addr.MemBitSize, true},
	{"ocean", addr.OffOcean, addr.OceanSize, true},
	{"event", addr.OffEvent, addr.EventSize, true},
	{"reserve", addr.OffReserve, addr.ReserveSize, false},
}

// packedOff maps a RAM image offset to its offset in the packed card
// image; ok is false for padding, which the card does not hold.
func packedOff(off int) (p int, ok bool) {
	for _, m := range members {
		if off >= m.off && off < m.off+m.size {
			return p + off - m.off, true
		}
		p += m.size
	}
	return 0, false
}

// osTimeBase stands in for OSGetTime at frame 0. The fake clock is
// osTimeBase + frames: deterministic, and never the 0 that marks a
// never-loaded file (§5.2 rule V5).
const osTimeBase = 0x0000_0123_4567_0000

// infoInit is dSv_info_c::init (d_save.cpp:1389-1398), which New Game
// runs as dComIfGs_init: the save image (minus mPriest, mStatusC and
// mReserve), the live memBit, mDan (init(-1)), every zone and mTmp. The
// member inits are modelled as zeroing plus the defaults the co-op code
// reads; their other defaults (time of day, wind, options, return place)
// stay zero in the fake.
func (g *FakeGame) infoInit() {
	for _, m := range members {
		if m.init {
			g.store(addr.SaveImage+uint32(m.off), make([]byte, m.size))
		}
	}
	// dSv_player_status_a_c::init (d_save.cpp:25-48): 3 hearts, and no
	// item on any button. mDateIPL stays 0 (status_b init, d_save.cpp:56).
	g.st16(addr.MaxLife, 12)
	g.st16(addr.Life, 12)
	g.store(addr.SelectItem, []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	g.store(addr.SelectEquip, []byte{0xFF, 0xFF, 0xFF, 0xFF})
	// dSv_player_item_c::init (d_save.cpp:79-83): every slot empty.
	g.store(addr.Items, bytesOf(0xFF, addr.ItemsSize))
	// dSv_player_info_c::init (d_save.cpp:966-990): the default name.
	g.store(addr.PlayerName, append([]byte("Link"), 0))
	// dSv_event_c::init's setInitEventBit (d_save.cpp:1178-1184,
	// d_save_init.cpp:12-15): register 0x7EFF = 0xE. 0xBEFF takes the
	// Kg1 high score, 0 here.
	b := addr.EventByte(0x7EFF)
	g.st8(addr.Event+uint32(b.Off), 0x0E)

	g.store(addr.LiveMemBit, make([]byte, addr.MemBitSize)) // dSv_memory_c::init, d_save.cpp:1220-1222
	g.initDan(-1)
	g.initZone()
	g.store(addr.Tmp, make([]byte, addr.TmpSize))
}

func bytesOf(v byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = v
	}
	return b
}

func checkSlot(slot int) error {
	if slot < 0 || slot >= CardSlots {
		return fmt.Errorf("fakegame: file slot %d out of range 0..%d", slot, CardSlots-1)
	}
	return nil
}

// NewGame starts a new file in slot as the naming screen does: file select
// sets mDataNum (d_file_select.cpp:811), then dComIfGs_init
// (d_s_name.cpp:1373) and the chosen name (d_s_name.cpp:1377). mReserve,
// and any binding tag in it, survives into the new file (§5.2).
func (g *FakeGame) NewGame(slot int, name string) error {
	if err := checkSlot(slot); err != nil {
		return err
	}
	if len(name) == 0 || len(name) > 16 {
		return fmt.Errorf("fakegame: player name %q must be 1-16 bytes", name) // mPlayerName is char[17]
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.st8(addr.DataNum, byte(slot))
	g.infoInit()
	g.store(addr.PlayerName, append([]byte(name), 0)) // setPlayerName strcpys (d_save.h:446)
	return nil
}

// LoadFile loads slot from the card as file select does: mDataNum
// (d_file_select.cpp:811), then card_to_memory (d_s_name.cpp:1213).
func (g *FakeGame) LoadFile(slot int) error {
	if err := checkSlot(slot); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.st8(addr.DataNum, byte(slot))
	g.cardToMemory(slot)
	return nil
}

// Save is the in-game save menu: putSave of the loaded stage's table
// (d_menu_save.cpp:935), then memory_to_card into mDataNum's slot
// (dataWrite, d_menu_save.cpp:1012-1013).
func (g *FakeGame) Save() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.playing {
		return errors.New("fakegame: the save menu needs a loaded stage")
	}
	slot := int(g.ld8(addr.DataNum))
	if err := checkSlot(slot); err != nil {
		return err
	}
	g.putSave(addr.StagSaveTable(g.stagProp()))
	g.memoryToCard(slot)
	return nil
}

// SecondQuestSave is what file select does after the credits: the NAMEEX
// scene sets mClearCount to 1 (d_s_name.cpp:169), then ExDataSave runs
// reinit and memory_to_card for slot and sets mDataNum
// (d_file_select.cpp:3626-3646). The player never saves, yet the card gets
// mReserve, stale tag and all (§5.2). reinit is modelled as init keeping
// the clear count and name (d_save.cpp:1435-1471); its other carry-overs
// and grants are left out.
func (g *FakeGame) SecondQuestSave(slot int) error {
	if err := checkSlot(slot); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.st8(addr.ClearCount, 1)
	name := g.load(addr.PlayerName, 17)
	g.infoInit()
	g.st8(addr.ClearCount, 1)
	g.store(addr.PlayerName, name)
	g.memoryToCard(slot)
	g.st8(addr.DataNum, byte(slot))
	return nil
}

// memoryToCard is dSv_info_c::memory_to_card (d_save.cpp:1703-1791). The
// card copy gets life raised to 3 hearts if lower (:1710-1712) and
// mDateIPL stamped with the time (:1715-1717); RAM keeps both as they
// were, so a New Game's mDateIPL stays 0 until the file is loaded back.
func (g *FakeGame) memoryToCard(slot int) {
	img := g.load(addr.SaveImage, addr.SaveImageSize)
	buf := g.card[slot][:]
	p := 0
	for _, m := range members {
		p += copy(buf[p:], img[m.off:m.off+m.size])
	}
	life, _ := packedOff(addr.OffLife)
	if binary.BigEndian.Uint16(buf[life:]) < 12 {
		binary.BigEndian.PutUint16(buf[life:], 12)
	}
	date, _ := packedOff(addr.OffDateIPL)
	binary.BigEndian.PutUint64(buf[date:], osTimeBase+g.frames)
}

// cardToMemory is dSv_info_c::card_to_memory (d_save.cpp:1794-1867): every
// member, mReserve included, overwritten from the card. The sound-mode
// fix-up after mConfig is left out.
func (g *FakeGame) cardToMemory(slot int) {
	buf := g.card[slot][:]
	p := 0
	for _, m := range members {
		g.store(addr.SaveImage+uint32(m.off), buf[p:p+m.size])
		p += m.size
	}
}

// Card returns a copy of slot's packed image.
func (g *FakeGame) Card(slot int) ([]byte, error) {
	if err := checkSlot(slot); err != nil {
		return nil, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]byte(nil), g.card[slot][:]...), nil
}

// SetCard replaces slot's packed image, which must be PackedSize bytes.
func (g *FakeGame) SetCard(slot int, b []byte) error {
	if err := checkSlot(slot); err != nil {
		return err
	}
	if len(b) != PackedSize {
		return fmt.Errorf("fakegame: card image is %d bytes, want 0x%X", len(b), PackedSize)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	copy(g.card[slot][:], b)
	return nil
}
