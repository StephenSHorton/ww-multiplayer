package avatar

import (
	"bytes"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar/avatartest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gfx/texture"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// The fake game RAM and C allocation step are wwapi/avatar/avatartest.

// Tunic targets: the multiplayer palette's red, blue and purple
// (mods/multiplayer/playercolor).
var (
	tRed    = texture.Color{R: 0xD0, G: 0x2C, B: 0x2C}
	tBlue   = texture.Color{R: 0x2C, G: 0x5C, B: 0xE0}
	tPurple = texture.Color{R: 0x8C, G: 0x3C, B: 0xD0}
)

// run ticks the sync n game frames, the C side allocating as it would.
func run(t *Tunics, f *avatartest.RAM, now *time.Time, n int) {
	for i := 0; i < n; i++ {
		t.Tick(*now)
		f.CAllocate()
		f.Frame()
		*now = now.Add(16 * time.Millisecond)
	}
}

func TestTunicSync_AllocatesAndFillsSlots(t *testing.T) {
	f := avatartest.NewLinkRAM(false)
	ts := NewTunics(f, t.Logf)
	red, blue := tRed, tBlue
	ts.Set(0, true, red)
	ts.Set(1, true, texture.Vanilla())
	ts.SetLocal(blue)
	now := time.Unix(0, 0)
	run(ts, f, &now, 8)

	if got, _ := f.ReadU32(mailbox.Mailbox + mailbox.OffTunicBufSize); got != uint32(len(avatartest.TestImage())) {
		t.Fatalf("buf_size = %d, want %d", got, len(avatartest.TestImage()))
	}
	for s, want := range [][]byte{avatartest.ExpectRecolor(red), avatartest.TestImage(), avatartest.ExpectRecolor(blue)} {
		if f.U8(mailbox.Mailbox+mailbox.OffTunicEnable+uint32(s)) != 1 {
			t.Errorf("slot %d not enabled", s)
		}
		if got := f.SlotImage(s); !bytes.Equal(got, want) {
			t.Errorf("slot %d image = %x, want %x", s, got, want)
		}
	}
	if bytes.Equal(avatartest.ExpectRecolor(red), avatartest.TestImage()) {
		t.Fatal("test image has no green to recolor")
	}
}

func TestTunicSync_VanillaLocalIsOff(t *testing.T) {
	f := avatartest.NewLinkRAM(false)
	ts := NewTunics(f, t.Logf)
	ts.SetLocal(texture.Vanilla())
	now := time.Unix(0, 0)
	run(ts, f, &now, 8)
	if f.U8(mailbox.Mailbox+mailbox.OffTunicEnable+TunicSlotLocal) != 0 || !ts.Settled(TunicSlotLocal) {
		t.Errorf("vanilla local slot: enable=%d settled=%v, want off", f.U8(mailbox.Mailbox+mailbox.OffTunicEnable+TunicSlotLocal), ts.Settled(TunicSlotLocal))
	}
}

// TestTunicSync_ColorChangeDrains: a color change disables the slot,
// waits tunicDrainFrames game frames, then rewrites and re-enables.
func TestTunicSync_ColorChangeDrains(t *testing.T) {
	f := avatartest.NewLinkRAM(false)
	ts := NewTunics(f, t.Logf)
	red, purple := tRed, tPurple
	ts.Set(0, true, red)
	now := time.Unix(0, 0)
	run(ts, f, &now, 8)
	ts.Set(0, true, purple)

	ts.Tick(now)
	if f.U8(mailbox.Mailbox+mailbox.OffTunicEnable) != 0 {
		t.Fatal("slot still enabled right after a color change")
	}
	if !bytes.Equal(f.SlotImage(0), avatartest.ExpectRecolor(red)) {
		t.Fatal("image rewritten before the drain")
	}
	ts.Tick(now) // same game frame: still draining
	if f.U8(mailbox.Mailbox+mailbox.OffTunicEnable) != 0 {
		t.Fatal("re-enabled within the same frame")
	}
	for i := 0; i < tunicDrainFrames; i++ {
		f.Frame()
	}
	ts.Tick(now)
	if f.U8(mailbox.Mailbox+mailbox.OffTunicEnable) != 1 || !bytes.Equal(f.SlotImage(0), avatartest.ExpectRecolor(purple)) {
		t.Fatal("slot not rewritten to purple after the drain")
	}
}

// TestTunicSync_CasualSlot: casual is written at once (no image, no
// drain), leaving it for a color goes through off and the drain, and
// the local slot never takes it.
func TestTunicSync_CasualSlot(t *testing.T) {
	f := avatartest.NewLinkRAM(false)
	ts := NewTunics(f, t.Logf)
	red := tRed
	ts.Set(0, true, red)
	now := time.Unix(0, 0)
	run(ts, f, &now, 8)
	ts.SetCasual(0)
	ts.SetCasual(TunicSlotLocal)
	if ts.managed[TunicSlotLocal] {
		t.Fatal("SetCasual took the local slot")
	}
	ts.Tick(now)
	if f.U8(mailbox.Mailbox+mailbox.OffTunicEnable) != TunicEnableCasual || !ts.Settled(0) {
		t.Fatalf("enable = %d settled = %v, want casual at once", f.U8(mailbox.Mailbox+mailbox.OffTunicEnable), ts.Settled(0))
	}
	ts.Set(0, true, red)
	ts.Tick(now)
	if f.U8(mailbox.Mailbox+mailbox.OffTunicEnable) != 0 {
		t.Fatal("casual -> red did not disable first")
	}
	run(ts, f, &now, tunicDrainFrames+2)
	if f.U8(mailbox.Mailbox+mailbox.OffTunicEnable) != TunicEnableOn || !ts.Settled(0) {
		t.Fatal("slot not back to red after the drain")
	}
	// Someone else (a save-state load) writing casual is noticed.
	f.Mem[mailbox.Mailbox+mailbox.OffTunicEnable] = TunicEnableCasual
	run(ts, f, &now, tunicDrainFrames+3)
	if f.U8(mailbox.Mailbox+mailbox.OffTunicEnable) != TunicEnableOn {
		t.Fatal("foreign casual enable not put back to red")
	}
}

// TestTunicSync_CasualReadsOtherLinktex: in the casual clothes the hero
// header is mOtherLinktex, with offsets relative to the worn header.
func TestTunicSync_CasualReadsOtherLinktex(t *testing.T) {
	f := avatartest.NewLinkRAM(true)
	h, err := readHeroTexture(f, [TunicSlots]uint32{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if h.hdrAddr != avatartest.Worn || !bytes.Equal(h.image, avatartest.TestImage()) || h.hdr.Width != 8 {
		t.Errorf("hero = %+v, want the 8x8 image at 0x%08X", h.hdr, uint32(avatartest.Image))
	}
}

// TestTunicSync_MidSwapReadRefused: a worn header that points into one
// of our buffers was read inside the C bracket and must not be taken as
// the hero image.
func TestTunicSync_MidSwapReadRefused(t *testing.T) {
	f := avatartest.NewLinkRAM(false)
	buf := uint32(avatartest.HeapTop)
	f.Write(buf, avatartest.TestImage())
	f.Write(avatartest.Worn, avatartest.ResTIMG(texture.FormatCMPR, 8, 8, buf-avatartest.Worn))
	if _, err := readHeroTexture(f, [TunicSlots]uint32{buf}, len(avatartest.TestImage())); err != errTunicBusy {
		t.Errorf("err = %v, want errTunicBusy", err)
	}
}

func TestTunicSync_RejectsNonCMPRAndHuge(t *testing.T) {
	f := avatartest.NewLinkRAM(false)
	f.Write(avatartest.Worn, avatartest.ResTIMG(0x09, 8, 8, avatartest.Image-avatartest.Worn))
	if _, err := readHeroTexture(f, [TunicSlots]uint32{}, 0); err == nil {
		t.Error("a non-CMPR hero texture was accepted")
	}
	f.Write(avatartest.Worn, avatartest.ResTIMG(texture.FormatCMPR, 1024, 1024, avatartest.Image-avatartest.Worn))
	if _, err := readHeroTexture(f, [TunicSlots]uint32{}, 0); err == nil {
		t.Error("a 512 KiB hero texture was accepted")
	}
}

// TestTunicSync_RewritesAfterOverwrite: a save-state load puts other
// bytes in a buffer; the once-a-second verify notices and rewrites.
func TestTunicSync_RewritesAfterOverwrite(t *testing.T) {
	f := avatartest.NewLinkRAM(false)
	ts := NewTunics(f, t.Logf)
	red := tRed
	ts.Set(0, true, red)
	now := time.Unix(0, 0)
	run(ts, f, &now, 8)
	buf, _ := f.ReadU32(mailbox.Mailbox + OffTunicBuf(0))
	f.Write(buf, make([]byte, len(avatartest.TestImage())))
	now = now.Add(2 * tunicVerifyEvery)
	run(ts, f, &now, 8)
	if !bytes.Equal(f.SlotImage(0), avatartest.ExpectRecolor(red)) || f.U8(mailbox.Mailbox+mailbox.OffTunicEnable) != 1 {
		t.Error("overwritten slot image was not rewritten")
	}
}

// TestTunicSync_UnmanagedSlotsUntouched: tunic-slot sets one slot and
// must leave the others as they are.
func TestTunicSync_UnmanagedSlotsUntouched(t *testing.T) {
	f := avatartest.NewLinkRAM(false)
	f.Mem[mailbox.Mailbox+mailbox.OffTunicEnable+1] = 1
	ts := NewTunics(f, t.Logf)
	ts.Set(0, true, tBlue)
	now := time.Unix(0, 0)
	run(ts, f, &now, 8)
	if f.U8(mailbox.Mailbox+mailbox.OffTunicEnable+1) != 1 || f.U8(mailbox.Mailbox+mailbox.OffTunicEnable+TunicSlotLocal) != 0 {
		t.Error("an unmanaged slot's enable changed")
	}
	if !ts.Settled(0) {
		t.Error("slot 0 not settled")
	}
}

func TestTunicSync_WaitsForLink(t *testing.T) {
	f := avatartest.NewRAM()
	ts := NewTunics(f, nil)
	ts.SetLocal(tRed)
	now := time.Unix(0, 0)
	run(ts, f, &now, 4)
	if f.Writes != 4 { // only run's own frame counter writes
		t.Errorf("%d writes with no Link loaded, want only the frame counter's", f.Writes)
	}
}

// putSwapped plays the C side mid-bracket: state 1, three published
// buffers, slot s enabled, and the worn header repointed at bufs[s]
// (hdr[7] = buf - hdr) with img in that buffer.
func putSwapped(f *avatartest.RAM, s int, img []byte) uint32 {
	f.Put32(mailbox.Mailbox+mailbox.OffTunicBufSize, uint32(len(img)))
	for i := 0; i < TunicSlots; i++ {
		f.Put32(mailbox.Mailbox+OffTunicBuf(i), avatartest.HeapTop+uint32(i)*0x1000)
	}
	f.Mem[mailbox.Mailbox+mailbox.OffTunicState] = 1
	f.Mem[mailbox.Mailbox+mailbox.OffTunicEnable+uint32(s)] = 1
	buf := uint32(avatartest.HeapTop) + uint32(s)*0x1000
	f.Write(buf, img)
	f.Write(avatartest.Worn, avatartest.ResTIMG(texture.FormatCMPR, 8, 8, buf-avatartest.Worn))
	return buf
}

// TestTunicSync_StaleSwapFirstTick: a fresh process (tunic-slot, a
// puppet-sync restart) whose first hero read lands inside a bracket left
// enabled by an earlier run must not calibrate on the recolored buffer.
func TestTunicSync_StaleSwapFirstTick(t *testing.T) {
	f := avatartest.NewLinkRAM(false)
	red := tRed
	putSwapped(f, TunicSlotLocal, avatartest.ExpectRecolor(red))
	ts := NewTunics(f, t.Logf)
	ts.Set(0, true, texture.Vanilla())
	now := time.Unix(0, 0)
	ts.Tick(now)
	if ts.hero != nil {
		t.Fatal("hero captured from a swapped header")
	}
	// The bracket ends: the worn header is the hero's again.
	f.Write(avatartest.Worn, avatartest.ResTIMG(texture.FormatCMPR, 8, 8, avatartest.Image-avatartest.Worn))
	run(ts, f, &now, 8)
	if ts.hero == nil || !bytes.Equal(ts.hero.image, avatartest.TestImage()) {
		t.Fatal("hero not captured once the header was restored")
	}
	if !bytes.Equal(f.SlotImage(0), avatartest.TestImage()) {
		t.Error("vanilla slot 0 is not the unmodified hero image")
	}
}

// TestTunicSync_RecalibrateRefusesSwapped: the recalibrate path (worn
// header moved) must refuse a swapped header too.
func TestTunicSync_RecalibrateRefusesSwapped(t *testing.T) {
	f := avatartest.NewLinkRAM(false)
	ts := NewTunics(f, t.Logf)
	ts.SetLocal(tBlue)
	now := time.Unix(0, 0)
	run(ts, f, &now, 8)
	// A new model: the worn header moves, and Go's next read lands inside
	// the local bracket.
	const worn2 = avatartest.Worn + 0x100
	f.Put32(avatartest.Link+addr.PlayerOffCurrLinktex, worn2)
	buf, _ := f.ReadU32(mailbox.Mailbox + OffTunicBuf(TunicSlotLocal))
	f.Write(worn2, avatartest.ResTIMG(texture.FormatCMPR, 8, 8, buf-worn2))
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
	f := avatartest.NewLinkRAM(false)
	ts := NewTunics(f, t.Logf)
	red, blue := tRed, tBlue
	ts.Set(0, true, red)
	now := time.Unix(0, 0)
	run(ts, f, &now, 8)
	buf, _ := f.ReadU32(mailbox.Mailbox + OffTunicBuf(0))
	other := avatartest.ExpectRecolor(blue)
	// Make the first block identical so only a full compare can tell.
	copy(other[:8], avatartest.ExpectRecolor(red)[:8])
	f.Write(buf, other)
	now = now.Add(2 * tunicVerifyEvery)
	run(ts, f, &now, 8)
	if !bytes.Equal(f.SlotImage(0), avatartest.ExpectRecolor(red)) || f.U8(mailbox.Mailbox+mailbox.OffTunicEnable) != 1 {
		t.Error("slot holding another color's image was not rewritten")
	}
}

// TestTunicSync_DisableAll: session teardown turns every enable off.
func TestTunicSync_DisableAll(t *testing.T) {
	f := avatartest.NewLinkRAM(false)
	ts := NewTunics(f, t.Logf)
	ts.Set(0, true, tRed)
	ts.SetLocal(tBlue)
	now := time.Unix(0, 0)
	run(ts, f, &now, 8)
	ts.DisableAll()
	for s := 0; s < TunicSlots; s++ {
		if f.U8(mailbox.Mailbox+mailbox.OffTunicEnable+uint32(s)) != 0 {
			t.Errorf("slot %d still enabled after DisableAll", s)
		}
	}
}
