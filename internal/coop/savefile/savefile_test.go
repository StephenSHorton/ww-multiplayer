package savefile

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/mem"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
)

// randImage is a random image with the padding the card does not hold
// zeroed and a player name, so it is a slot in use.
func randImage(seed uint64, name string) world.Image {
	r := rand.New(rand.NewPCG(seed, 1))
	var img world.Image
	for i := range img {
		img[i] = byte(r.Uint32())
	}
	p := Pack(&img)
	img = Unpack(&p)
	n := img[addr.OffPlayerName : addr.OffPlayerName+17]
	clear(n)
	copy(n, name)
	img[addr.OffClearCount] = 0
	return img
}

func TestChecksums(t *testing.T) {
	for _, c := range []struct {
		name string
		in   []byte
		want uint64
	}{
		{"empty", nil, 0},
		{"one zero", []byte{0}, 0xFFFFFFFF},
		// c1 adds 0xFFFFFFFF - v per byte: the u8 is promoted before ~.
		{"1, 2", []byte{1, 2}, 3<<32 | (0xFFFFFFFE+0xFFFFFFFD)&0xFFFFFFFF},
		{"0xFF", []byte{0xFF}, 0xFF<<32 | 0xFFFFFF00},
	} {
		if got := GameDataSum(c.in); got != c.want {
			t.Errorf("GameDataSum(%s) = %#x, want %#x", c.name, got, c.want)
		}
	}
	for _, c := range []struct {
		name string
		in   []byte
		want uint32
	}{
		{"empty", nil, 0},
		{"one word", []byte{0x00, 0x01}, 0x0001_FFFE},
		{"two words wrap", []byte{0xFF, 0xFF, 0x00, 0x02}, 0x0001_FFFD},
		{"odd byte ignored", []byte{0x12, 0x34, 0x56}, 0x1234_EDCB},
	} {
		if got := SaveDataSum(c.in); got != c.want {
			t.Errorf("SaveDataSum(%s) = %#x, want %#x", c.name, got, c.want)
		}
	}
	if got := PictDataSum([]byte{0xFF, 0xFF, 2}); got != 0x200 {
		t.Errorf("PictDataSum = %#x, want 0x200", got)
	}
	for _, c := range []struct {
		in       []byte
		sum, inv uint16
		desc     string
	}{
		{[]byte{0, 0}, 0, 0, "the inverse 0xFFFF is stored as 0"},
		{[]byte{0xFF, 0xFF}, 0, 0, "the sum 0xFFFF is stored as 0"},
		{[]byte{0, 1, 0, 2}, 3, 0xFFFB, "plain"},
	} {
		if s, i := cardCheckSum(c.in); s != c.sum || i != c.inv {
			t.Errorf("cardCheckSum(% X) = %#x, %#x; want %#x, %#x (%s)", c.in, s, i, c.sum, c.inv, c.desc)
		}
	}
}

// TestPackLayout checks the member table against dSv_save_c_PACKED
// (d_save.h:614-635, 922-932): the members pack to 0x768 bytes with the
// big ones where the decomp puts them, and only the image's padding is
// left out.
func TestPackLayout(t *testing.T) {
	n, end := 0, 0
	for _, m := range members {
		if m.off < end {
			t.Errorf("member at 0x%03X overlaps the one before", m.off)
		}
		n, end = n+m.size, m.off+m.size
	}
	if n != PackedSize || end != addr.OffImagePad {
		t.Errorf("members pack to 0x%X bytes ending at image 0x%03X; want 0x%X ending at 0x%03X", n, end, PackedSize, addr.OffImagePad)
	}
	for _, c := range []struct{ off, want int }{
		{addr.OffStatusA, 0x000},
		{addr.OffGetBagItem, 0x08E},
		{addr.OffBagItemRecord, 0x09A},
		{addr.OffCollect, 0x0B2},
		{addr.OffMap, 0x0BF},
		{addr.OffInfo, 0x143},
		{addr.OffConfig, 0x19F},
		{addr.OffPriest, 0x1A4},
		{addr.OffStatusC, 0x1B4},
		{addr.OffMemory, 0x374},
		{addr.OffOcean, 0x5B4},
		{addr.OffEvent, 0x618},
		{addr.OffReserve, 0x718},
		{addr.OffReserve + addr.ReserveSize - 1, 0x767},
	} {
		if p, ok := PackedOff(c.off); !ok || p != c.want {
			t.Errorf("PackedOff(0x%03X) = 0x%03X, %v; want 0x%03X", c.off, p, ok, c.want)
		}
	}
	for _, off := range []int{0x08E, 0x08F, 0x0C1, 0x0C3, 0x1A9, 0x1AB, 0x37C, 0x37F, 0x774, 0x777, addr.SaveImageSize, -1} {
		if p, ok := PackedOff(off); ok {
			t.Errorf("PackedOff(0x%03X) = 0x%03X for padding", off, p)
		}
	}
	// A member's own padding is copied with it.
	for _, off := range []int{addr.OffStatusAPad, addr.OffStatusBPad, addr.OffMapPad, addr.OffMemory + addr.MemBitOffPad} {
		if _, ok := PackedOff(off); !ok {
			t.Errorf("PackedOff(0x%03X): a member's own padding is on the card", off)
		}
	}
}

func TestPackRoundTrip(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 7))
	for range 20 {
		var p Packed
		for i := range p {
			p[i] = byte(r.Uint32())
		}
		img := Unpack(&p)
		if got := Pack(&img); got != p {
			t.Fatal("Pack(Unpack(p)) != p")
		}
		for off := range img {
			if _, ok := PackedOff(off); !ok && img[off] != 0 {
				t.Fatalf("Unpack left padding byte 0x%03X = %#x", off, img[off])
			}
			if q, ok := PackedOff(off); ok && img[off] != p[q] {
				t.Fatalf("image 0x%03X = %#x, packed 0x%03X = %#x", off, img[off], q, p[q])
			}
		}
	}
}

// TestPackMatchesFakeGame: what the fake game's memory_to_card writes
// unpacks to the fake's RAM image (bar memory_to_card's life and date
// fix-ups, d_save.cpp:1710-1717), and the fake's card_to_memory loads
// what Pack writes.
func TestPackMatchesFakeGame(t *testing.T) {
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	if err := g.NewGame(1, "Aryll"); err != nil {
		t.Fatal(err)
	}
	if err := g.LoadStage(fakegame.Stage{Name: "sea", Room: 44, Table: addr.TableSea}); err != nil {
		t.Fatal(err)
	}
	if err := g.OnSwitch(0x47, 44); err != nil {
		t.Fatal(err)
	}
	g.OnEventBit(0x0F80)
	_ = m.Store(addr.Life, []byte{0, 40})
	_ = m.Store(addr.Reserve, bytes.Repeat([]byte{0xA5}, addr.ReserveSize))
	g.Frames(3)
	if err := g.Save(); err != nil {
		t.Fatal(err)
	}
	card, err := g.Card(1)
	if err != nil {
		t.Fatal(err)
	}
	var p Packed
	copy(p[:], card)
	img := Unpack(&p)
	ram, err := m.Read(addr.SaveImage, addr.SaveImageSize)
	if err != nil {
		t.Fatal(err)
	}
	for off := range img {
		_, onCard := PackedOff(off)
		inDate := off >= addr.OffDateIPL && off < addr.OffDateIPL+8
		if onCard && !inDate && img[off] != ram[off] {
			t.Errorf("image 0x%03X: card %#x, RAM %#x", off, img[off], ram[off])
		}
	}

	other := randImage(3, "Tetra")
	op := Pack(&other)
	if err := g.SetCard(2, op[:]); err != nil {
		t.Fatal(err)
	}
	if err := g.LoadFile(2); err != nil {
		t.Fatal(err)
	}
	ram, _ = m.Read(addr.SaveImage, addr.SaveImageSize)
	for off := range other {
		if _, onCard := PackedOff(off); onCard && ram[off] != other[off] {
			t.Errorf("loaded image 0x%03X = %#x, want %#x", off, ram[off], other[off])
		}
	}
}

func TestNewAndParse(t *testing.T) {
	f := New()
	if err := f.Header.CheckStat(); err != nil {
		t.Fatal(err)
	}
	b := f.Encode()
	if len(b) != GCISize {
		t.Fatalf("GCI is %d bytes", len(b))
	}
	g, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(g.Encode(), b) {
		t.Fatal("Parse(Encode) changed the file")
	}
	if h := g.Header; h.GameName() != "GZLE" || h.Company() != "01" || h.FileName() != "gczelda" || h.Length() != 12 {
		t.Errorf("header %q %q %q %d", h.GameName(), h.Company(), h.FileName(), h.Length())
	}
	for _, s := range g.AllSlots() {
		if s.State != NewGame || s.Copy != 1 || !s.Valid() {
			t.Errorf("slot %d: %s from copy %d valid %v", s.N, s.State, s.Copy, s.Valid())
		}
	}
	if g.SaveCount() != 1 {
		t.Errorf("save count %d", g.SaveCount())
	}
	if a, b := g.Copies(); !a || !b {
		t.Errorf("copies %v %v", a, b)
	}
	if ph := g.Photos(0); ph != [3]bool{true, true, true} {
		t.Errorf("blank photos %v", ph)
	}
}

func TestParseRefuses(t *testing.T) {
	good := New().Encode()
	edit := func(f func(b []byte) []byte) []byte { return f(bytes.Clone(good)) }
	for _, c := range []struct {
		name string
		in   []byte
		want string
	}{
		{"empty", nil, "shorter than a GCI header"},
		{"header only", good[:HeaderSize], "is 0x18040 bytes, not 0x40"},
		{"truncated", good[:len(good)-1], "not 0x1803F"},
		{"trailing byte", append(bytes.Clone(good), 0), "not 0x18041"},
		{"another game", edit(func(b []byte) []byte { copy(b, "GZLP"); return b }), `game "GZLP"`},
		{"another company", edit(func(b []byte) []byte { copy(b[4:], "8P"); return b }), `company "8P"`},
		{"another file", edit(func(b []byte) []byte { copy(b[8:], "gczeldb"); return b }), `"gczeldb"`},
		{"longer name", edit(func(b []byte) []byte { copy(b[8:], "gczelda2"); return b }), `"gczelda2"`},
		{"shorter name", edit(func(b []byte) []byte { b[8+6] = 0; return b }), `"gczeld"`},
		{"length", edit(func(b []byte) []byte { binary.BigEndian.PutUint16(b[0x38:], 11); return b }), "11 blocks"},
	} {
		_, err := Parse(c.in)
		if err == nil || !errors.Is(err, ErrFormat) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err %v, want ErrFormat containing %q", c.name, err, c.want)
		}
	}
}

func TestCheckStat(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(h *Header)
		want string
	}{
		{"icon address", func(h *Header) { binary.BigEndian.PutUint32(h[hdrOffIconAddr:], 0x40) }, "icon address"},
		{"no icon", func(h *Header) { binary.BigEndian.PutUint32(h[hdrOffIconAddr:], 0xFFFFFFFF) }, "icon address"},
		{"comment", func(h *Header) { binary.BigEndian.PutUint32(h[hdrOffCommentAddr:], 0) }, "comment address"},
		{"banner", func(h *Header) { h[hdrOffBannerFormat] = 2 }, "banner format"},
		{"icon animation", func(h *Header) { h[hdrOffBannerFormat] = 1 | 4 }, "banner format"},
		{"icon formats", func(h *Header) { binary.BigEndian.PutUint16(h[hdrOffIconFormat:], 0x0005) }, "icon formats"},
		{"icon speeds", func(h *Header) { binary.BigEndian.PutUint16(h[hdrOffIconSpeed:], 0x0002) }, "icon speeds"},
	} {
		f := New()
		c.edit(&f.Header)
		if err := f.Header.CheckStat(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
		if _, err := f.WithSlot(0, &world.Image{}); err == nil {
			t.Errorf("%s: WithSlot wrote a file the game would not load", c.name)
		}
	}
	// Bits CheckCardStat does not read may be anything.
	f := New()
	f.Header[hdrOffBannerFormat] |= 0xF8
	if err := f.Header.CheckStat(); err != nil {
		t.Errorf("high banner bits: %v", err)
	}
}

func TestWithSlot(t *testing.T) {
	f := New()
	img := randImage(1, "Link")
	g, err := f.WithSlot(2, &img)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Verify(2); err != nil {
		t.Fatal(err)
	}
	if g.SaveCount() != f.SaveCount()+1 {
		t.Errorf("save count %d -> %d, want one up", f.SaveCount(), g.SaveCount())
	}
	if f.AllSlots()[2].State != NewGame {
		t.Error("WithSlot changed its receiver")
	}
	s, _ := g.Slot(2)
	if s.State != InUse || s.Image != img || s.PlayerName() != "Link" {
		t.Errorf("slot 2: %s %q, image equal %v", s.State, s.PlayerName(), s.Image == img)
	}
	for _, n := range []int{0, 1} {
		if s, _ := g.Slot(n); s.State != NewGame {
			t.Errorf("slot %d became %s", n, s.State)
		}
	}
	if !bytes.Equal(g.Data[:BlockSize], f.Data[:BlockSize]) || !bytes.Equal(g.Data[3*BlockSize:], f.Data[3*BlockSize:]) {
		t.Error("WithSlot changed the header block or the photos")
	}
	sd := g.block(blockSave)
	if v := be32(sd[sdOffDataVersion:]); v != 0 {
		t.Errorf("data_version %d", v)
	}
	if !bytes.Equal(sd[sdOffPad:sdOffSum], make([]byte, sdOffSum-sdOffPad)) {
		t.Error("unused card_savedata bytes not zero")
	}
	if _, err := f.WithSlot(3, &img); err == nil {
		t.Error("slot 3 accepted")
	}
	if _, err := f.Slot(-1); err == nil {
		t.Error("slot -1 accepted")
	}
	if err := g.Verify(3); err == nil {
		t.Error("Verify(3) accepted")
	}

	// Round trip through bytes, then a second save on top.
	h, err := Parse(g.Encode())
	if err != nil {
		t.Fatal(err)
	}
	img2 := randImage(2, "Tetra")
	h, err = h.WithSlot(0, &img2)
	if err != nil {
		t.Fatal(err)
	}
	if s0, _ := h.Slot(0); s0.Image != img2 {
		t.Error("slot 0 not written")
	}
	if s2, _ := h.Slot(2); s2.Image != img {
		t.Error("slot 2 lost")
	}
}

// TestRestore follows mDoMemCdRWm_Restore's repair (m_Do_MemCardRWmng.cpp:
// 135-146), slot 1's quirk included, and WithSlot keeping the repair.
func TestRestore(t *testing.T) {
	base := New()
	img := [Slots]world.Image{randImage(10, "A"), randImage(11, "B"), randImage(12, "C")}
	for n := range Slots {
		var err error
		if base, err = base.WithSlot(n, &img[n]); err != nil {
			t.Fatal(err)
		}
	}
	corrupt := func(f *File, copyNo, slot int) {
		gameData(f.block(copyNo), slot)[0x100] ^= 0xFF
	}
	for _, c := range []struct {
		name  string
		bad   [][2]int // (copy, slot) made invalid
		from  [Slots]int
		state [Slots]State
	}{
		{"clean", nil, [Slots]int{1, 1, 1}, [Slots]State{InUse, InUse, InUse}},
		{"slot 0 repaired", [][2]int{{1, 0}}, [Slots]int{2, 1, 1}, [Slots]State{InUse, InUse, InUse}},
		{"slot 2 repaired", [][2]int{{1, 2}}, [Slots]int{1, 1, 2}, [Slots]State{InUse, InUse, InUse}},
		{"slot 0 both bad", [][2]int{{1, 0}, {2, 0}}, [Slots]int{1, 1, 1}, [Slots]State{Corrupt, InUse, InUse}},
		{"slot 1 repaired", [][2]int{{1, 1}}, [Slots]int{1, 2, 1}, [Slots]State{InUse, InUse, InUse}},
		// The quirk: slot 1's repair tests the second copy's slot 2.
		{"slot 1 not repaired: copy 2 slot 2 bad", [][2]int{{1, 1}, {2, 2}}, [Slots]int{1, 1, 1}, [Slots]State{InUse, Corrupt, InUse}},
		{"slot 1 copied though bad", [][2]int{{1, 1}, {2, 1}}, [Slots]int{1, 2, 1}, [Slots]State{InUse, Corrupt, InUse}},
	} {
		f := base.Clone()
		for _, b := range c.bad {
			corrupt(f, b[0], b[1])
		}
		for n, s := range f.AllSlots() {
			if s.Copy != c.from[n] || s.State != c.state[n] {
				t.Errorf("%s: slot %d from copy %d %s; want copy %d %s", c.name, n, s.Copy, s.State, c.from[n], c.state[n])
			}
		}
		// Saving slot 0 writes the restored slots to both copies.
		g, err := f.WithSlot(0, &img[0])
		if err != nil {
			t.Fatal(err)
		}
		for n, s := range g.AllSlots() {
			want := c.state[n]
			if n == 0 {
				want = InUse
			}
			if s.State != want || want == InUse && s.Copy != 1 {
				t.Errorf("%s: after a save slot %d from copy %d %s; want %s, from copy 1 if in use", c.name, n, s.Copy, s.State, want)
			}
		}
		if err := g.Verify(0); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}

	// A bad card_savedata checksum alone does not stop a load, but
	// Verify reports it.
	f := base.Clone()
	f.block(blockSave2)[sdOffPad] = 1
	if _, second := f.Copies(); second {
		t.Error("damaged second copy passes")
	}
	if s, _ := f.Slot(0); s.State != InUse {
		t.Error("slot lost to the copy's own checksum")
	}
	if err := f.Verify(0); err == nil {
		t.Error("Verify passed a damaged copy")
	}
}

func TestReadDecode(t *testing.T) {
	dir := t.TempDir()
	img := randImage(5, "Link")
	f, err := New().WithSlot(0, &img)
	if err != nil {
		t.Fatal(err)
	}
	gci := filepath.Join(dir, "01-GZLE-gczelda.gci")
	if err := os.WriteFile(gci, f.Encode(), 0o644); err != nil {
		t.Fatal(err)
	}
	g, k, err := Read(gci)
	if err != nil || k != KindGCI || g.Data != f.Data {
		t.Fatalf("Read GCI: %v %s", err, k)
	}
	raw := filepath.Join(dir, "MemoryCardA.USA.raw")
	if err := os.WriteFile(raw, buildCard(t, cardSpec{blocks: 64, file: f, startBlock: 9}), 0o644); err != nil {
		t.Fatal(err)
	}
	g, k, err = Read(raw)
	if err != nil || k != KindRaw || g.Data != f.Data {
		t.Fatalf("Read raw: %v %s", err, k)
	}
	// The entry is the card's, which names where the file starts on it.
	want := f.Header
	binary.BigEndian.PutUint16(want[entryOffStartBlock:], 9)
	if g.Header != want {
		t.Errorf("raw entry\n% X\nwant\n% X", g.Header, want)
	}
	odd := filepath.Join(dir, "odd.bin")
	_ = os.WriteFile(odd, make([]byte, 100), 0o644)
	if _, _, err := Read(odd); !errors.Is(err, ErrFormat) {
		t.Errorf("odd size: %v", err)
	}
	if _, _, err := Read(filepath.Join(dir, "missing.gci")); err == nil {
		t.Error("missing file read")
	}
}
