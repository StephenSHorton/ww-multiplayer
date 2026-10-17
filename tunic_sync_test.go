package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/network"
	"github.com/StephenSHorton/ww-multiplayer/internal/playercolor"
	"github.com/StephenSHorton/ww-multiplayer/internal/tunic"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// fakeRAM is a sparse big-endian GameCube RAM for the tunic tests. It
// also plays the C side's allocation step (multiplayer_update's
// "allocate once" block) when allocOnTick is set.
type fakeRAM struct {
	mem    map[uint32]byte
	writes int
}

func newFakeRAM() *fakeRAM { return &fakeRAM{mem: map[uint32]byte{}} }

func (f *fakeRAM) Read(addr uint32, n int) ([]byte, error) {
	b := make([]byte, n)
	for i := range b {
		b[i] = f.mem[addr+uint32(i)]
	}
	return b, nil
}

func (f *fakeRAM) ReadU32(addr uint32) (uint32, error) {
	b, _ := f.Read(addr, 4)
	return binary.BigEndian.Uint32(b), nil
}

func (f *fakeRAM) Write(addr uint32, data []byte) error {
	f.writes++
	for i, v := range data {
		f.mem[addr+uint32(i)] = v
	}
	return nil
}

func (f *fakeRAM) put32(addr, v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	f.Write(addr, b[:])
}

func (f *fakeRAM) u8(addr uint32) byte { return f.mem[addr] }

const (
	fakeLink    = 0x80A00000
	fakeTexArr  = 0x80B00000 // ResTIMG[] of the shared model data
	fakeWorn    = fakeTexArr + 2*tunic.ResTIMGSize
	fakeImage   = 0x80B10000
	fakeCasual  = 0x80B20000
	fakeHeapTop = 0x80C00000
)

// testImage is an 8x8 CMPR image: two green blocks, a skin block and a
// white one.
func testImage() []byte {
	c := func(r, g, b uint8) uint16 {
		return uint16(r>>3)<<11 | uint16(g>>2)<<5 | uint16(b>>3)
	}
	var out []byte
	for _, e := range [][2]uint16{
		{c(0x3C, 0x9A, 0x2E), c(0x1E, 0x5A, 0x18)},
		{c(0x40, 0xA0, 0x30), c(0x20, 0x60, 0x18)},
		{c(0xF0, 0xC8, 0x96), c(0xC0, 0x98, 0x70)},
		{c(0xF8, 0xF8, 0xF0), c(0xE0, 0xE0, 0xD8)},
	} {
		blk := make([]byte, 8)
		binary.BigEndian.PutUint16(blk[0:], e[0])
		binary.BigEndian.PutUint16(blk[2:], e[1])
		copy(blk[4:], []byte{0x1B, 0x1B, 0xE4, 0xE4})
		out = append(out, blk...)
	}
	return out
}

func resTIMG(format byte, w, h uint16, imageOffset uint32) []byte {
	b := make([]byte, tunic.ResTIMGSize)
	b[0] = format
	binary.BigEndian.PutUint16(b[2:], w)
	binary.BigEndian.PutUint16(b[4:], h)
	binary.BigEndian.PutUint32(b[0x1C:], imageOffset)
	return b
}

// newLinkRAM sets up Link #1 in the hero's clothes (or casual), his worn
// linktexS3TC header and the hero image, plus an empty mailbox.
func newLinkRAM(casual bool) *fakeRAM {
	f := newFakeRAM()
	f.put32(addr.PlayerPtr, fakeLink)
	f.put32(fakeLink+addr.PlayerOffCurrLinktex, fakeWorn)
	f.Write(fakeImage, testImage())
	hero := resTIMG(tunic.FormatCMPR, 8, 8, fakeImage-fakeWorn)
	if casual {
		f.put32(fakeLink+addr.PlayerOffNoResetFlg1, addr.PlayerFlg1CasualClothes)
		// The worn header holds the casual texture (another format);
		// mOtherLinktex holds the hero header, offsets relative to the
		// worn header.
		f.Write(fakeWorn, resTIMG(0x09, 16, 16, fakeCasual-fakeWorn))
		f.Write(fakeLink+addr.PlayerOffOtherLinktex, hero)
	} else {
		f.Write(fakeWorn, hero)
		f.Write(fakeLink+addr.PlayerOffOtherLinktex, resTIMG(0x09, 16, 16, fakeCasual-fakeWorn))
	}
	return f
}

// cAllocate plays multiplayer_update's allocation block.
func (f *fakeRAM) cAllocate() {
	if f.u8(mailbox.Mailbox+mailbox.OffTunicState) != 0 {
		return
	}
	size, _ := f.ReadU32(mailbox.Mailbox + mailbox.OffTunicBufSize)
	if size == 0 {
		return
	}
	for s := 0; s < tunicSlots; s++ {
		f.put32(mailbox.Mailbox+mailboxTunicBuf(s), fakeHeapTop+uint32(s)*0x1000)
	}
	f.mem[mailbox.Mailbox+mailbox.OffTunicState] = 1
}

func (f *fakeRAM) frame() {
	v, _ := f.ReadU32(mailbox.Mailbox)
	f.put32(mailbox.Mailbox, v+1)
}

// run ticks the sync n game frames, the C side allocating as it would.
func run(t *tunicSync, f *fakeRAM, now *time.Time, n int) {
	for i := 0; i < n; i++ {
		t.Tick(*now)
		f.cAllocate()
		f.frame()
		*now = now.Add(16 * time.Millisecond)
	}
}

func slotImage(f *fakeRAM, s int) []byte {
	buf, _ := f.ReadU32(mailbox.Mailbox + mailboxTunicBuf(s))
	b, _ := f.Read(buf, len(testImage()))
	return b
}

func expectRecolor(c playercolor.Color) []byte {
	img := testImage()
	m, ref := tunic.Calibrate(img, tunic.DefaultMask)
	out, _ := tunic.Recolor(img, c, m, ref)
	return out
}

func TestTunicSync_AllocatesAndFillsSlots(t *testing.T) {
	f := newLinkRAM(false)
	ts := newTunicSync(f, t.Logf)
	red, blue := playercolor.Palette()[1].Color, playercolor.Palette()[2].Color
	ts.Set(0, true, red)
	ts.Set(1, true, playercolor.Vanilla())
	ts.SetLocal(blue)
	now := time.Unix(0, 0)
	run(ts, f, &now, 8)

	if got, _ := f.ReadU32(mailbox.Mailbox + mailbox.OffTunicBufSize); got != uint32(len(testImage())) {
		t.Fatalf("buf_size = %d, want %d", got, len(testImage()))
	}
	for s, want := range [][]byte{expectRecolor(red), testImage(), expectRecolor(blue)} {
		if f.u8(mailbox.Mailbox+mailbox.OffTunicEnable+uint32(s)) != 1 {
			t.Errorf("slot %d not enabled", s)
		}
		if got := slotImage(f, s); !bytes.Equal(got, want) {
			t.Errorf("slot %d image = %x, want %x", s, got, want)
		}
	}
	if bytes.Equal(expectRecolor(red), testImage()) {
		t.Fatal("test image has no green to recolor")
	}
}

func TestTunicSync_VanillaLocalIsOff(t *testing.T) {
	f := newLinkRAM(false)
	ts := newTunicSync(f, t.Logf)
	ts.SetLocal(playercolor.Vanilla())
	now := time.Unix(0, 0)
	run(ts, f, &now, 8)
	if f.u8(mailbox.Mailbox+mailbox.OffTunicEnable+tunicSlotLocal) != 0 || !ts.Settled(tunicSlotLocal) {
		t.Errorf("vanilla local slot: enable=%d settled=%v, want off", f.u8(mailbox.Mailbox+mailbox.OffTunicEnable+tunicSlotLocal), ts.Settled(tunicSlotLocal))
	}
}

// TestTunicSync_ColorChangeDrains: a color change disables the slot,
// waits tunicDrainFrames game frames, then rewrites and re-enables.
func TestTunicSync_ColorChangeDrains(t *testing.T) {
	f := newLinkRAM(false)
	ts := newTunicSync(f, t.Logf)
	red, purple := playercolor.Palette()[1].Color, playercolor.Palette()[3].Color
	ts.Set(0, true, red)
	now := time.Unix(0, 0)
	run(ts, f, &now, 8)
	ts.Set(0, true, purple)

	ts.Tick(now)
	if f.u8(mailbox.Mailbox+mailbox.OffTunicEnable) != 0 {
		t.Fatal("slot still enabled right after a color change")
	}
	if !bytes.Equal(slotImage(f, 0), expectRecolor(red)) {
		t.Fatal("image rewritten before the drain")
	}
	ts.Tick(now) // same game frame: still draining
	if f.u8(mailbox.Mailbox+mailbox.OffTunicEnable) != 0 {
		t.Fatal("re-enabled within the same frame")
	}
	for i := 0; i < tunicDrainFrames; i++ {
		f.frame()
	}
	ts.Tick(now)
	if f.u8(mailbox.Mailbox+mailbox.OffTunicEnable) != 1 || !bytes.Equal(slotImage(f, 0), expectRecolor(purple)) {
		t.Fatal("slot not rewritten to purple after the drain")
	}
}

// TestTunicSync_CasualSlot: casual is written at once (no image, no
// drain), leaving it for a color goes through off and the drain, a
// casual remote without a server color still gets the shirt, and the
// local slot never takes it.
func TestTunicSync_CasualSlot(t *testing.T) {
	f := newLinkRAM(false)
	ts := newTunicSync(f, t.Logf)
	red := playercolor.Palette()[1].Color
	ts.Set(0, true, red)
	now := time.Unix(0, 0)
	run(ts, f, &now, 8)
	ts.SetCasual(0)
	ts.SetCasual(tunicSlotLocal)
	if ts.managed[tunicSlotLocal] {
		t.Fatal("SetCasual took the local slot")
	}
	ts.Tick(now)
	if f.u8(mailbox.Mailbox+mailbox.OffTunicEnable) != tunicEnableCasual || !ts.Settled(0) {
		t.Fatalf("enable = %d settled = %v, want casual at once", f.u8(mailbox.Mailbox+mailbox.OffTunicEnable), ts.Settled(0))
	}
	ts.Set(0, true, red)
	ts.Tick(now)
	if f.u8(mailbox.Mailbox+mailbox.OffTunicEnable) != 0 {
		t.Fatal("casual -> red did not disable first")
	}
	run(ts, f, &now, tunicDrainFrames+2)
	if f.u8(mailbox.Mailbox+mailbox.OffTunicEnable) != tunicEnableOn || !ts.Settled(0) {
		t.Fatal("slot not back to red after the drain")
	}
	// Someone else (a save-state load) writing casual is noticed.
	f.mem[mailbox.Mailbox+mailbox.OffTunicEnable] = tunicEnableCasual
	run(ts, f, &now, tunicDrainFrames+3)
	if f.u8(mailbox.Mailbox+mailbox.OffTunicEnable) != tunicEnableOn {
		t.Fatal("foreign casual enable not put back to red")
	}

	casualFace := append(make([]byte, faceStateWireBytes), appearanceCasual)
	remotes := []network.RemotePlayer{{ID: 2, Name: "a", FaceState: casualFace}}
	syncTunics(ts, playercolor.Vanilla(), true, remotes, map[byte]int{2: 0}, now)
	if f.u8(mailbox.Mailbox+mailbox.OffTunicEnable) != tunicEnableCasual {
		t.Error("casual remote without a server color is not casual")
	}
	ts.Set(0, false, playercolor.Color{})
	ts.Tick(now)
	if f.u8(mailbox.Mailbox+mailbox.OffTunicEnable) != 0 || !ts.Settled(0) {
		t.Error("casual -> off not written")
	}
}

// TestTunicSync_CasualReadsOtherLinktex: in the casual clothes the hero
// header is mOtherLinktex, with offsets relative to the worn header.
func TestTunicSync_CasualReadsOtherLinktex(t *testing.T) {
	f := newLinkRAM(true)
	h, err := readHeroTexture(f, [tunicSlots]uint32{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if h.hdrAddr != fakeWorn || !bytes.Equal(h.image, testImage()) || h.hdr.Width != 8 {
		t.Errorf("hero = %+v, want the 8x8 image at 0x%08X", h.hdr, uint32(fakeImage))
	}
	b, ok := localAppearance(f)
	if !ok || b != appearanceCasual {
		t.Errorf("localAppearance = %#x %v, want casual", b, ok)
	}
}

// TestTunicSync_MidSwapReadRefused: a worn header that points into one
// of our buffers was read inside the C bracket and must not be taken as
// the hero image.
func TestTunicSync_MidSwapReadRefused(t *testing.T) {
	f := newLinkRAM(false)
	buf := uint32(fakeHeapTop)
	f.Write(buf, testImage())
	f.Write(fakeWorn, resTIMG(tunic.FormatCMPR, 8, 8, buf-fakeWorn))
	if _, err := readHeroTexture(f, [tunicSlots]uint32{buf}, len(testImage())); err != errTunicBusy {
		t.Errorf("err = %v, want errTunicBusy", err)
	}
}

func TestTunicSync_RejectsNonCMPRAndHuge(t *testing.T) {
	f := newLinkRAM(false)
	f.Write(fakeWorn, resTIMG(0x09, 8, 8, fakeImage-fakeWorn))
	if _, err := readHeroTexture(f, [tunicSlots]uint32{}, 0); err == nil {
		t.Error("a non-CMPR hero texture was accepted")
	}
	f.Write(fakeWorn, resTIMG(tunic.FormatCMPR, 1024, 1024, fakeImage-fakeWorn))
	if _, err := readHeroTexture(f, [tunicSlots]uint32{}, 0); err == nil {
		t.Error("a 512 KiB hero texture was accepted")
	}
}

// TestTunicSync_RewritesAfterOverwrite: a save-state load puts other
// bytes in a buffer; the once-a-second verify notices and rewrites.
func TestTunicSync_RewritesAfterOverwrite(t *testing.T) {
	f := newLinkRAM(false)
	ts := newTunicSync(f, t.Logf)
	red := playercolor.Palette()[1].Color
	ts.Set(0, true, red)
	now := time.Unix(0, 0)
	run(ts, f, &now, 8)
	buf, _ := f.ReadU32(mailbox.Mailbox + mailboxTunicBuf(0))
	f.Write(buf, make([]byte, len(testImage())))
	now = now.Add(2 * tunicVerifyEvery)
	run(ts, f, &now, 8)
	if !bytes.Equal(slotImage(f, 0), expectRecolor(red)) || f.u8(mailbox.Mailbox+mailbox.OffTunicEnable) != 1 {
		t.Error("overwritten slot image was not rewritten")
	}
}

// TestTunicSync_UnmanagedSlotsUntouched: tunic-slot sets one slot and
// must leave the others as they are.
func TestTunicSync_UnmanagedSlotsUntouched(t *testing.T) {
	f := newLinkRAM(false)
	f.mem[mailbox.Mailbox+mailbox.OffTunicEnable+1] = 1
	ts := newTunicSync(f, t.Logf)
	ts.Set(0, true, playercolor.Palette()[2].Color)
	now := time.Unix(0, 0)
	run(ts, f, &now, 8)
	if f.u8(mailbox.Mailbox+mailbox.OffTunicEnable+1) != 1 || f.u8(mailbox.Mailbox+mailbox.OffTunicEnable+tunicSlotLocal) != 0 {
		t.Error("an unmanaged slot's enable changed")
	}
	if !ts.Settled(0) {
		t.Error("slot 0 not settled")
	}
}

func TestTunicSync_WaitsForLink(t *testing.T) {
	f := newFakeRAM()
	ts := newTunicSync(f, nil)
	ts.SetLocal(playercolor.Palette()[1].Color)
	now := time.Unix(0, 0)
	run(ts, f, &now, 4)
	if f.writes != 4 { // only run's own frame counter writes
		t.Errorf("%d writes with no Link loaded, want only the frame counter's", f.writes)
	}
}

func TestRemoteCasual(t *testing.T) {
	face := make([]byte, faceStateWireBytes)
	if remoteCasual(face) {
		t.Error("a 16-byte trailer (pre-appearance sender) read as casual")
	}
	if !remoteCasual(append(face, appearanceCasual)) || remoteCasual(append(face, 0)) {
		t.Error("appearance byte not read")
	}
}

// TestSyncTunics_SlotsFollowRemotes: link slots take their remote's
// server color, a casual remote gets the casual shirt (enable 2), a free
// slot is off, and the
// local slot takes this client's own color.
func TestSyncTunics_SlotsFollowRemotes(t *testing.T) {
	f := newLinkRAM(false)
	ts := newTunicSync(f, t.Logf)
	red, blue := playercolor.Palette()[1].Color, playercolor.Palette()[2].Color
	casualFace := append(make([]byte, faceStateWireBytes), appearanceCasual)
	remotes := []network.RemotePlayer{
		{ID: 2, Name: "a", Color: red, HasColor: true},
		{ID: 3, Name: "b", Color: blue, HasColor: true, FaceState: casualFace},
	}
	now := time.Unix(0, 0)
	for i := 0; i < 8; i++ {
		syncTunics(ts, playercolor.Vanilla(), true, remotes, map[byte]int{2: 1, 3: 0}, now)
		f.cAllocate()
		f.frame()
	}
	if ts.want[1] != (tunicWant{on: true, color: red}) {
		t.Errorf("slot 1 want = %+v, want red", ts.want[1])
	}
	if ts.want[0] != (tunicWant{casual: true}) {
		t.Errorf("casual remote's slot want = %+v, want casual", ts.want[0])
	}
	if f.u8(mailbox.Mailbox+mailbox.OffTunicEnable) != tunicEnableCasual {
		t.Errorf("casual remote's slot enable = %d, want %d", f.u8(mailbox.Mailbox+mailbox.OffTunicEnable), tunicEnableCasual)
	}
	// This client is green, which needs no swap.
	if ts.want[tunicSlotLocal].on {
		t.Errorf("local want = %+v, want off (green)", ts.want[tunicSlotLocal])
	}
	if !bytes.Equal(slotImage(f, 1), expectRecolor(red)) {
		t.Error("slot 1 image is not red")
	}
	syncTunics(ts, playercolor.Vanilla(), true, remotes[:1], map[byte]int{2: 1}, now)
	if ts.want[0].on || ts.want[0].casual {
		t.Error("free slot 0 is on")
	}
	if f.u8(mailbox.Mailbox+mailbox.OffTunicEnable) != 0 {
		t.Error("free slot 0 still casual in RAM")
	}
}

// TestSyncTunics_SharedColorBothSlots: players may share a color, so
// two remotes in the same color each get their own slot, both on, both
// drawn in that color.
func TestSyncTunics_SharedColorBothSlots(t *testing.T) {
	f := newLinkRAM(false)
	ts := newTunicSync(f, t.Logf)
	red := playercolor.Palette()[1].Color
	remotes := []network.RemotePlayer{
		{ID: 2, Name: "a", Color: red, HasColor: true},
		{ID: 3, Name: "b", Color: red, HasColor: true},
	}
	now := time.Unix(0, 0)
	for i := 0; i < 8; i++ {
		syncTunics(ts, playercolor.Vanilla(), true, remotes, map[byte]int{2: 0, 3: 1}, now)
		f.cAllocate()
		f.frame()
	}
	for s := 0; s < 2; s++ {
		if ts.want[s] != (tunicWant{on: true, color: red}) {
			t.Errorf("slot %d want = %+v, want red", s, ts.want[s])
		}
		if !bytes.Equal(slotImage(f, s), expectRecolor(red)) {
			t.Errorf("slot %d image is not red", s)
		}
		if !ts.Settled(s) {
			t.Errorf("slot %d not settled", s)
		}
	}
}

// TestSyncTunics_NoServerColorAllOff: against a server without colors
// (or before the first player list) every slot is off, even ones a
// previous run left enabled, and a remote without a server color is not
// drawn in a by-ID guess.
func TestSyncTunics_NoServerColorAllOff(t *testing.T) {
	f := newLinkRAM(false)
	for s := 0; s < tunicSlots; s++ {
		f.mem[mailbox.Mailbox+mailbox.OffTunicEnable+uint32(s)] = 1
	}
	ts := newTunicSync(f, t.Logf)
	remotes := []network.RemotePlayer{{ID: 2, Name: "a"}, {ID: 3, Name: "b"}}
	now := time.Unix(0, 0)
	for i := 0; i < 8; i++ {
		syncTunics(ts, playercolor.Color{}, false, remotes, map[byte]int{2: 0, 3: 1}, now)
		f.cAllocate()
		f.frame()
	}
	for s := 0; s < tunicSlots; s++ {
		if f.u8(mailbox.Mailbox+mailbox.OffTunicEnable+uint32(s)) != 0 || !ts.Settled(s) {
			t.Errorf("slot %d: enable=%d settled=%v, want off", s, f.u8(mailbox.Mailbox+mailbox.OffTunicEnable+uint32(s)), ts.Settled(s))
		}
	}
	// Own color known, remotes still without one: remote slots stay off.
	red := playercolor.Palette()[1].Color
	for i := 0; i < 8; i++ {
		syncTunics(ts, red, true, remotes, map[byte]int{2: 0, 3: 1}, now)
		f.cAllocate()
		f.frame()
	}
	if ts.want[0].on || ts.want[1].on || !ts.want[tunicSlotLocal].on {
		t.Errorf("wants = %+v, want only the local slot on", ts.want)
	}
}

// TestSyncTunics_CasualSkipsColorGates: the casual shirt needs no image,
// so a casual remote gets enable 2 whatever holds the color pipeline
// back: no server color (haveMine false), Link not loaded (no hero
// texture), nothing allocated yet (state 0), a buffer size for another
// texture, or its slot's allocation failed (state 0xFD, buf 0).
func TestSyncTunics_CasualSkipsColorGates(t *testing.T) {
	casualFace := append(make([]byte, faceStateWireBytes), appearanceCasual)
	remotes := []network.RemotePlayer{{ID: 2, Name: "a", FaceState: casualFace}, {ID: 3, Name: "b"}}
	slots := map[byte]int{2: 0, 3: 1}
	red := playercolor.Palette()[1].Color
	for _, tc := range []struct {
		name     string
		haveMine bool
		setup    func(f *fakeRAM)
	}{
		{"no server color", false, func(f *fakeRAM) {}},
		{"Link not loaded", true, func(f *fakeRAM) { f.put32(addr.PlayerPtr, 0) }},
		{"not allocated", true, func(f *fakeRAM) {
			f.mem[mailbox.Mailbox+mailbox.OffTunicState] = 0
			f.put32(mailbox.Mailbox+mailbox.OffTunicBufSize, 0)
		}},
		{"size mismatch", true, func(f *fakeRAM) {
			f.put32(mailbox.Mailbox+mailbox.OffTunicBufSize, 0x1234)
			f.mem[mailbox.Mailbox+mailbox.OffTunicState] = 1
		}},
		{"alloc failed", true, func(f *fakeRAM) {
			f.put32(mailbox.Mailbox+mailbox.OffTunicBufSize, uint32(len(testImage())))
			f.put32(mailbox.Mailbox+mailboxTunicBuf(1), fakeHeapTop+0x1000)
			f.mem[mailbox.Mailbox+mailbox.OffTunicState] = 0xFD
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLinkRAM(false)
			tc.setup(f)
			ts := newTunicSync(f, t.Logf)
			now := time.Unix(0, 0)
			for i := 0; i < 4; i++ {
				syncTunics(ts, red, tc.haveMine, remotes, slots, now)
				f.frame()
			}
			if got := f.u8(mailbox.Mailbox + mailbox.OffTunicEnable); got != tunicEnableCasual || !ts.Settled(0) {
				t.Errorf("slot 0 enable = %d settled = %v, want casual", got, ts.Settled(0))
			}
			if got := f.u8(mailbox.Mailbox + mailbox.OffTunicEnable + 1); got == tunicEnableCasual {
				t.Error("non-casual slot 1 is casual")
			}
			if !tc.haveMine && (ts.want[1].on || ts.want[tunicSlotLocal].on) {
				t.Errorf("without a server color the colors are on: %+v", ts.want)
			}
		})
	}
}

// putSwapped plays the C side mid-bracket: state 1, three published
// buffers, slot s enabled, and the worn header repointed at bufs[s]
// (hdr[7] = buf - hdr) with img in that buffer.
func putSwapped(f *fakeRAM, s int, img []byte) uint32 {
	f.put32(mailbox.Mailbox+mailbox.OffTunicBufSize, uint32(len(img)))
	for i := 0; i < tunicSlots; i++ {
		f.put32(mailbox.Mailbox+mailboxTunicBuf(i), fakeHeapTop+uint32(i)*0x1000)
	}
	f.mem[mailbox.Mailbox+mailbox.OffTunicState] = 1
	f.mem[mailbox.Mailbox+mailbox.OffTunicEnable+uint32(s)] = 1
	buf := uint32(fakeHeapTop) + uint32(s)*0x1000
	f.Write(buf, img)
	f.Write(fakeWorn, resTIMG(tunic.FormatCMPR, 8, 8, buf-fakeWorn))
	return buf
}

// TestTunicSync_StaleSwapFirstTick: a fresh process (tunic-slot, a
// puppet-sync restart) whose first hero read lands inside a bracket left
// enabled by an earlier run must not calibrate on the recolored buffer.
func TestTunicSync_StaleSwapFirstTick(t *testing.T) {
	f := newLinkRAM(false)
	red := playercolor.Palette()[1].Color
	putSwapped(f, tunicSlotLocal, expectRecolor(red))
	ts := newTunicSync(f, t.Logf)
	ts.Set(0, true, playercolor.Vanilla())
	now := time.Unix(0, 0)
	ts.Tick(now)
	if ts.hero != nil {
		t.Fatal("hero captured from a swapped header")
	}
	// The bracket ends: the worn header is the hero's again.
	f.Write(fakeWorn, resTIMG(tunic.FormatCMPR, 8, 8, fakeImage-fakeWorn))
	run(ts, f, &now, 8)
	if ts.hero == nil || !bytes.Equal(ts.hero.image, testImage()) {
		t.Fatal("hero not captured once the header was restored")
	}
	if !bytes.Equal(slotImage(f, 0), testImage()) {
		t.Error("vanilla slot 0 is not the unmodified hero image")
	}
}

// TestTunicSync_RecalibrateRefusesSwapped: the recalibrate path (worn
// header moved) must refuse a swapped header too.
func TestTunicSync_RecalibrateRefusesSwapped(t *testing.T) {
	f := newLinkRAM(false)
	ts := newTunicSync(f, t.Logf)
	ts.SetLocal(playercolor.Palette()[2].Color)
	now := time.Unix(0, 0)
	run(ts, f, &now, 8)
	// A new model: the worn header moves, and Go's next read lands inside
	// the local bracket.
	const worn2 = fakeWorn + 0x100
	f.put32(fakeLink+addr.PlayerOffCurrLinktex, worn2)
	buf, _ := f.ReadU32(mailbox.Mailbox + mailboxTunicBuf(tunicSlotLocal))
	f.Write(worn2, resTIMG(tunic.FormatCMPR, 8, 8, buf-worn2))
	now = now.Add(2 * tunicVerifyEvery)
	ts.Tick(now) // verify notices the move, drops the hero
	ts.Tick(now)
	if ts.hero != nil {
		t.Fatal("recalibrated on a swapped header")
	}
}

// TestTunicSync_RewritesOtherColor: a save-state load that leaves another
// color's image behind enable=1 is caught by the verify, whatever part of
// the image the colors differ in.
func TestTunicSync_RewritesOtherColor(t *testing.T) {
	f := newLinkRAM(false)
	ts := newTunicSync(f, t.Logf)
	red, blue := playercolor.Palette()[1].Color, playercolor.Palette()[2].Color
	ts.Set(0, true, red)
	now := time.Unix(0, 0)
	run(ts, f, &now, 8)
	buf, _ := f.ReadU32(mailbox.Mailbox + mailboxTunicBuf(0))
	other := expectRecolor(blue)
	// Make the first block identical so only a full compare can tell.
	copy(other[:8], expectRecolor(red)[:8])
	f.Write(buf, other)
	now = now.Add(2 * tunicVerifyEvery)
	run(ts, f, &now, 8)
	if !bytes.Equal(slotImage(f, 0), expectRecolor(red)) || f.u8(mailbox.Mailbox+mailbox.OffTunicEnable) != 1 {
		t.Error("slot holding another color's image was not rewritten")
	}
}

// TestTunicSync_DisableAll: session teardown turns every enable off.
func TestTunicSync_DisableAll(t *testing.T) {
	f := newLinkRAM(false)
	ts := newTunicSync(f, t.Logf)
	ts.Set(0, true, playercolor.Palette()[1].Color)
	ts.SetLocal(playercolor.Palette()[2].Color)
	now := time.Unix(0, 0)
	run(ts, f, &now, 8)
	ts.DisableAll()
	for s := 0; s < tunicSlots; s++ {
		if f.u8(mailbox.Mailbox+mailbox.OffTunicEnable+uint32(s)) != 0 {
			t.Errorf("slot %d still enabled after DisableAll", s)
		}
	}
}

func TestReadNameTab_DerefsJUTNameTab(t *testing.T) {
	f := newFakeRAM()
	const obj, res = 0x80D00000, 0x80D10000
	names := []string{"linktexS3TC", "eye", "linktexS3TC"}
	f.put32(obj, 0x803A0000) // vtable
	f.put32(obj+4, res)
	f.put32(obj+8, res+4+4*uint32(len(names)))
	f.Write(obj+0xC, []byte{0, byte(len(names))})
	f.Write(res, []byte{0, byte(len(names)), 0xFF, 0xFF})
	off := 4 + 4*len(names)
	for i, n := range names {
		var e [4]byte
		binary.BigEndian.PutUint16(e[2:], uint16(off))
		f.Write(res+4+4*uint32(i), e[:])
		f.Write(res+uint32(off), append([]byte(n), 0))
		off += len(n) + 1
	}
	got := readNameTab(f, obj, len(names))
	if fmt.Sprint(got) != fmt.Sprint(names) {
		t.Fatalf("names = %q, want %q", got, names)
	}
	if spare, used, ok := tunic.FindLinktex(got); !ok || spare != 0 || used != 2 {
		t.Errorf("FindLinktex = %d %d %v", spare, used, ok)
	}
	// The old bug: reading the object itself as the ResNTAB.
	if bad := readNameTab(f, res, len(names)); bad[0] != "" {
		t.Errorf("a ResNTAB passed as a JUTNameTab gave %q", bad)
	}
}
