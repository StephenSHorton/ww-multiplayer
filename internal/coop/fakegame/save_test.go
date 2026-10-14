package fakegame

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/mem"
)

// tag is a stand-in binding tag (§5.2): the bytes only need to be
// recognisable.
var tag = append([]byte("WWCO\x01\x00\x00\x00world-id"), bytes.Repeat([]byte{0x5A}, addr.ReserveTagBytes-16)...)

// TestPackedLayout checks the member table against dSv_save_c_PACKED
// (d_save.h:922-932): the members tile the RAM image apart from its
// padding, and pack to 0x768 bytes with the big members where the decomp
// puts them.
func TestPackedLayout(t *testing.T) {
	at := map[string]int{}
	p, prevEnd := 0, 0
	for _, m := range members {
		if m.off < prevEnd {
			t.Errorf("%s at 0x%03X overlaps the member before it", m.name, m.off)
		}
		prevEnd = m.off + m.size
		at[m.name] = p
		p += m.size
	}
	if p != PackedSize {
		t.Errorf("members pack to 0x%X bytes, want 0x%X", p, PackedSize)
	}
	for name, want := range map[string]int{
		"status_a": 0x000, "memory": 0x374, "ocean": 0x5B4, "event": 0x618, "reserve": 0x718,
	} {
		if at[name] != want {
			t.Errorf("packed %s at 0x%03X, want 0x%03X", name, at[name], want)
		}
	}
	// packedOff agrees, and refuses padding.
	for off, want := range map[int]int{addr.OffMemory: 0x374, addr.OffReserve + 3: 0x71B, addr.OffDateIPL: 0x18} {
		if p, ok := packedOff(off); !ok || p != want {
			t.Errorf("packedOff(0x%03X) = 0x%03X, %v; want 0x%03X", off, p, ok, want)
		}
	}
	for _, off := range []int{
		addr.OffBagItem + addr.BagItemSize, addr.OffCollect + addr.CollectSize,
		addr.OffConfigPad, addr.OffPlayerPad, addr.OffImagePad, addr.SaveImageSize,
	} {
		if p, ok := packedOff(off); ok {
			t.Errorf("packedOff(0x%03X) = 0x%03X for padding", off, p)
		}
	}
	if prevEnd != addr.OffImagePad {
		t.Errorf("members end at 0x%03X, want the image padding at 0x%03X", prevEnd, addr.OffImagePad)
	}
}

// TestNewGameKeepsReserve: dComIfGs_init resets the file but not mReserve
// (or mPriest), so the previous file's tag rides into the new one (§5.2).
func TestNewGameKeepsReserve(t *testing.T) {
	g, m := newGame(t)
	load(t, g, drc)
	onSwitch(t, g, 0x03, 0)
	onSwitch(t, g, 0x80, 0)
	g.OnEventBit(0x2A08)
	_ = m.Store(addr.Reserve, tag)
	priest := []byte{1, 2, 3, 4}
	_ = m.Store(addr.SaveImage+addr.OffPriest, priest)
	_ = m.Store(addr.DateIPL, []byte{0, 0, 0, 1, 2, 3, 4, 5})
	_ = m.Store(addr.DataNum, []byte{0})
	_ = m.Store(addr.Tmp+0x10, []byte{0xFF})

	g.EnterFileSelect()
	if err := g.NewGame(1, "Tetra"); err != nil {
		t.Fatal(err)
	}

	if b := read(t, m, addr.Reserve, addr.ReserveTagBytes); !bytes.Equal(b, tag) {
		t.Errorf("mReserve after New Game = %x, want the old tag", b)
	}
	if b := read(t, m, addr.SaveImage+addr.OffPriest, 4); !bytes.Equal(b, priest) {
		t.Errorf("mPriest after New Game = %x; dSv_player_c::init does not reset it", b)
	}
	checks := []struct {
		name      string
		got, want uint64
	}{
		{"mDataNum", uint64(u8(t, m, addr.DataNum)), 1},
		{"mDateIPL", binary.BigEndian.Uint64(read(t, m, addr.DateIPL, 8)), 0},
		{"mMaxLife", uint64(u16(t, m, addr.MaxLife)), 12},
		{"mEvent[0x2A]", uint64(u8(t, m, addr.Event+0x2A)), 0},
		{"event register 0x7E", uint64(u8(t, m, addr.Event+0x7E)), 0x0E},
		{"mDan.mStageNo", uint64(u8(t, m, addr.DanStageNo)), addr.DanStageNoInvalid},
		{"mDan switches", uint64(u32(t, m, addr.DanSwitch+4)), 0},
		{"mTmp[0x10]", uint64(u8(t, m, addr.Tmp+0x10)), 0},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %#x, want %#x", c.name, c.got, c.want)
		}
	}
	saved := read(t, m, addr.SavedMemBit, addr.TableCount*addr.MemBitSize)
	live := read(t, m, addr.LiveMemBit, addr.MemBitSize)
	if !bytes.Equal(saved, make([]byte, len(saved))) || !bytes.Equal(live, make([]byte, len(live))) {
		t.Error("memBits not reset by New Game")
	}
	for z := range addr.ZoneCount {
		if r := zoneRoom(t, m, z); r != -1 {
			t.Errorf("zone %d still belongs to room %d", z, r)
		}
	}
	if name, _ := mem.ReadString(m, addr.PlayerName, 17); name != "Tetra" {
		t.Errorf("player name = %q", name)
	}
}

// TestCardRoundTrip: memory_to_card fixes up the card copy only, and
// card_to_memory restores every member (mReserve included) but never the
// RAM image's padding.
func TestCardRoundTrip(t *testing.T) {
	g, m := newGame(t)
	if err := g.NewGame(0, "Link"); err != nil {
		t.Fatal(err)
	}
	load(t, g, outset)
	onSwitch(t, g, 0x47, 44)
	_ = m.Store(addr.Life, []byte{0, 4}) // below 3 hearts
	_ = m.Store(addr.Reserve, tag)
	g.Frames(100)
	if err := g.Save(); err != nil {
		t.Fatal(err)
	}

	card, err := g.Card(0)
	if err != nil {
		t.Fatal(err)
	}
	life, _ := packedOff(addr.OffLife)
	date, _ := packedOff(addr.OffDateIPL)
	reserve, _ := packedOff(addr.OffReserve)
	if v := binary.BigEndian.Uint16(card[life:]); v != 12 {
		t.Errorf("card life = %d, want the 12 memory_to_card raises it to", v)
	}
	if v := u16(t, m, addr.Life); v != 4 {
		t.Errorf("RAM life = %d, want it untouched", v)
	}
	if v := binary.BigEndian.Uint64(card[date:]); v != osTimeBase+100 {
		t.Errorf("card mDateIPL = %#x, want the save time %#x", v, osTimeBase+100)
	}
	if v := binary.BigEndian.Uint64(read(t, m, addr.DateIPL, 8)); v != 0 {
		t.Errorf("RAM mDateIPL = %#x after a save, want 0 until the file is loaded", v)
	}
	if !bytes.Equal(card[reserve:reserve+len(tag)], tag) {
		t.Error("the card did not get mReserve")
	}
	_, bit, _ := addr.SwitchBit(0x47)
	if p, _ := packedOff(addr.OffMemBit(addr.TableSea) + bit.Off); card[p]&bit.Mask == 0 {
		t.Error("the card's sea table lacks the switch putSave carried over")
	}

	// Scribble over the whole image, padding included, and load it back.
	before := read(t, m, addr.SaveImage, addr.SaveImageSize)
	_ = m.Store(addr.SaveImage, bytes.Repeat([]byte{0xAA}, addr.SaveImageSize))
	g.EnterFileSelect()
	if err := g.LoadFile(0); err != nil {
		t.Fatal(err)
	}
	after := read(t, m, addr.SaveImage, addr.SaveImageSize)
	pads := 0
	for i := range after {
		_, onCard := packedOff(i)
		if !onCard {
			pads++
		}
		switch {
		case !onCard:
			if after[i] != 0xAA {
				t.Errorf("padding byte 0x%03X = %#x: card_to_memory wrote it", i, after[i])
			}
		case i >= addr.OffDateIPL && i < addr.OffDateIPL+8, i == addr.OffLife+1:
			// The card's fixed-up copies, checked below.
		case after[i] != before[i]:
			t.Errorf("byte 0x%03X = %#x after the load, saved %#x", i, after[i], before[i])
		}
	}
	if v := binary.BigEndian.Uint64(read(t, m, addr.DateIPL, 8)); v == 0 {
		t.Error("a loaded file still has mDateIPL 0")
	}
	if v := u16(t, m, addr.Life); v != 12 {
		t.Errorf("loaded life = %d, want the card's 12", v)
	}
	if v := u8(t, m, addr.DataNum); v != 0 {
		t.Errorf("mDataNum = %d", v)
	}
	if pads != addr.SaveImageSize-PackedSize {
		t.Errorf("%d padding bytes, want 0x778 - 0x768 = %d", pads, addr.SaveImageSize-PackedSize)
	}

	// SetCard and the slot checks.
	if err := g.SetCard(2, card); err != nil {
		t.Fatal(err)
	}
	if c2, _ := g.Card(2); !bytes.Equal(c2, card) {
		t.Error("SetCard did not take")
	}
	if err := g.SetCard(1, card[:10]); err == nil {
		t.Error("SetCard took a short image")
	}
	if _, err := g.Card(3); err == nil {
		t.Error("Card(3) succeeded")
	}
	if err := g.LoadFile(-1); err == nil {
		t.Error("LoadFile(-1) succeeded")
	}
}

// TestSecondQuestSave: ExDataSave writes the stale tag to the card with
// the new clear count, though the player never saved (§5.2 rule V3).
func TestSecondQuestSave(t *testing.T) {
	g, m := newGame(t)
	if err := g.NewGame(0, "Link"); err != nil {
		t.Fatal(err)
	}
	_ = m.Store(addr.Reserve, tag)
	g.OnEventBit(0x2A08)
	g.EnterFileSelect()
	if err := g.SecondQuestSave(2); err != nil {
		t.Fatal(err)
	}
	card, _ := g.Card(2)
	reserve, _ := packedOff(addr.OffReserve)
	if !bytes.Equal(card[reserve:reserve+len(tag)], tag) {
		t.Error("ExDataSave did not write the stale tag")
	}
	clear, _ := packedOff(addr.OffClearCount)
	if card[clear] != 1 || u8(t, m, addr.ClearCount) != 1 {
		t.Errorf("clear count card %d RAM %d, want 1 1", card[clear], u8(t, m, addr.ClearCount))
	}
	if name, _ := mem.ReadString(m, addr.PlayerName, 17); name != "Link" {
		t.Errorf("name = %q after reinit", name)
	}
	if v := u8(t, m, addr.Event+0x2A); v != 0 {
		t.Errorf("event bits survived reinit: %#x", v)
	}
	if v := u8(t, m, addr.DataNum); v != 2 {
		t.Errorf("mDataNum = %d", v)
	}
	if err := g.Save(); err == nil {
		t.Error("Save on file select succeeded")
	}
}
