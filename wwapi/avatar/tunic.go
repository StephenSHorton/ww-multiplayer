package avatar

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/playercolor"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gfx/texture"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// Tunic colors, render side (docs/09-player-colors.md §5.3-§5.4).
//
// The C side (inject/src/multiplayer.c tunic_swap / tunic_restore)
// repoints Link's worn "linktexS3TC" header at a per-slot image around
// each Link's draw. This file is the Go half: it reads the hero's-clothes
// image once, recolors it per slot with wwapi/gfx/texture, writes the images
// into the three GameHeap buffers the C side allocates once, and flips
// the per-slot enables.

// The mailbox's tunic block (inject/include/mailbox.h, "TUNIC COLORS";
// offsets generated into wwapi/mailbox) runs from mailbox.OffTunicBufs
// to the end of the mailbox, mailbox.MailboxSize:
//
//	tunic_bufs     u32[3], C: image per slot
//	tunic_buf_size u32, Go: bytes per image; nonzero asks C to allocate
//	tunic_enable   u8[3], Go: 1 = slot image valid, swap on; 2 = casual clothes
//	tunic_state    u8, C: 0 none, 1 allocated, 0xFD alloc failed
//	tunic_swaps    u32, C: header swaps done
const (
	TunicSlots     = mailbox.LenTunicBufs
	TunicSlotLocal = mailbox.TunicSlotLocal // TUNIC_SLOT_LOCAL: Link #1 on this Dolphin

	// Enable byte values. TunicEnableCasual (TUNIC_ENABLE_CASUAL) marks a
	// remote slot in the casual clothes: C copies the casual header in
	// around its draw and uses no image buffer (docs/09 §5.6).
	TunicEnableOn     = 1
	TunicEnableCasual = mailbox.TunicEnableCasual

	// tunicMaxImage caps the image size Go asks the C side to allocate
	// three of from GameHeap (docs/09 §6: ~245 KB free on Outset). A
	// 256x256 CMPR image is 32 KiB.
	tunicMaxImage = 64 << 10

	// tunicDrainFrames is how many game frames a slot stays disabled
	// before its image is rewritten, so no display list baked in the
	// frame before still samples the buffer while it changes (§5.4).
	tunicDrainFrames = 2

	tunicVerifyEvery = time.Second
)

// TunicMem is the Dolphin memory access the tunic code needs;
// *emu.Dolphin implements it.
type TunicMem interface {
	Read(addr uint32, n int) ([]byte, error)
	ReadU32(addr uint32) (uint32, error)
	Write(addr uint32, data []byte) error
}

// OffTunicBuf is the mailbox offset of tunic_bufs[slot].
func OffTunicBuf(slot int) uint32 { return uint32(mailbox.OffTunicBufs + 4*slot) }

// errTunicBusy means the worn header pointed at one of our buffers: the
// C bracket was mid-swap during the read. Retry on a later tick.
var errTunicBusy = errors.New("linktexS3TC header is mid-swap")

// heroTexture is Link's hero's-clothes body texture as read from RAM.
type heroTexture struct {
	hdrAddr uint32          // the worn header (mpCurrLinktex)
	hdr     texture.ResTIMG // the hero's-clothes header
	image   []byte          // the original CMPR image
}

// readHeroTexture reads the hero's-clothes linktexS3TC image. The worn
// header (daPy_lk_c+0x338) holds it in the hero's clothes; in the casual
// clothes it is the raw copy in mOtherLinktex (+0x33C), whose offsets are
// relative to the worn header (d_a_player_main.cpp:12338-12355). C never
// writes mOtherLinktex; the worn header it does, so a read that lands
// inside a bracket (image inside one of skip, the live slot buffers from
// the mailbox) is refused with errTunicBusy, and the header is read
// twice to reject a torn read. skipSize is the mailbox buf_size; the
// range checked is the larger of it and the header's own image size, so
// a swapped header (image == buf) is refused even when skipSize is 0.
func readHeroTexture(m TunicMem, skip [TunicSlots]uint32, skipSize int) (*heroTexture, error) {
	link, err := m.ReadU32(addr.PlayerPtr)
	if err != nil || !addr.InMEM1(link) {
		return nil, errors.New("Link not loaded")
	}
	worn, err := m.ReadU32(link + addr.PlayerOffCurrLinktex)
	if err != nil || !addr.InMEM1(worn) {
		return nil, fmt.Errorf("mpCurrLinktex not set (0x%08X)", worn)
	}
	flg, err := m.ReadU32(link + addr.PlayerOffNoResetFlg1)
	if err != nil {
		return nil, err
	}
	src := worn
	if flg&addr.PlayerFlg1CasualClothes != 0 {
		src = link + addr.PlayerOffOtherLinktex
	}
	a, err1 := m.Read(src, texture.ResTIMGSize)
	b, err2 := m.Read(src, texture.ResTIMGSize)
	if err1 != nil || err2 != nil || len(a) != texture.ResTIMGSize || !bytes.Equal(a, b) {
		return nil, errTunicBusy
	}
	h, _ := texture.ParseResTIMG(a)
	size, ok := h.ImageSize()
	if !ok {
		return nil, fmt.Errorf("hero texture format 0x%02X is not CMPR", h.Format)
	}
	if size <= 0 || size > tunicMaxImage {
		return nil, fmt.Errorf("hero texture is %d bytes (%dx%d), over the %d-byte cap", size, h.Width, h.Height, tunicMaxImage)
	}
	img := h.ImageAddr(worn)
	if !addr.InMEM1(img) || !addr.InMEM1(img+uint32(size)-1) {
		return nil, fmt.Errorf("hero image 0x%08X is outside MEM1", img)
	}
	span := uint32(max(skipSize, size))
	for _, s := range skip {
		if s != 0 && img >= s && img < s+span {
			return nil, errTunicBusy
		}
	}
	data, err := m.Read(img, size)
	if err != nil || len(data) != size {
		return nil, errors.New("cannot read the hero texture")
	}
	return &heroTexture{hdrAddr: worn, hdr: h, image: data}, nil
}

// TunicWant is what a slot should show: off, the hero's clothes in
// color, or (remote slots only) the casual clothes.
type TunicWant struct {
	On     bool
	Color  playercolor.Color
	Casual bool
}

// tunicSlotState is what Go last put in RAM for a slot.
type tunicSlotState struct {
	known   bool // enable byte state is known (written by us since the buffer appeared)
	enabled bool
	casual  bool              // enable byte is TunicEnableCasual
	color   playercolor.Color // image content while enabled
	drainAt uint32            // spawn_trigger when the slot was disabled
	img     []byte            // the written image (a cache entry, never mutated), for verify
}

// Tunics drives the three tunic slots of one Dolphin. (It was tunicSync
// in package main; the session side, which picks each slot's color,
// stays with the caller.)
type Tunics struct {
	mem TunicMem
	log func(format string, args ...any)

	hero  *heroTexture
	mask  texture.Mask
	ref   texture.Reference
	cache map[playercolor.Color][]byte

	bufs       [TunicSlots]uint32
	managed    [TunicSlots]bool // Tick leaves unmanaged slots alone
	want       [TunicSlots]TunicWant
	cur        [TunicSlots]tunicSlotState
	lastVerify time.Time
	lastErr    string
	stateNoted byte
}

// NewTunics returns a Tunics over m; log (nil: none) gets its status
// lines.
func NewTunics(m TunicMem, log func(format string, args ...any)) *Tunics {
	if log == nil {
		log = func(string, ...any) {}
	}
	return &Tunics{mem: m, log: log, cache: map[playercolor.Color][]byte{}}
}

// Set records what slot should show: off, or the tunic in color c. A
// vanilla c on a remote slot still swaps (to the unmodified image), so a
// green remote is drawn in hero green even while the local player wears
// the casual clothes.
func (t *Tunics) Set(slot int, on bool, c playercolor.Color) {
	if slot < 0 || slot >= TunicSlots {
		return
	}
	t.want[slot] = TunicWant{On: on, Color: c}
	t.managed[slot] = true
}

// SetCasual asks a remote slot to show the casual clothes. It needs no
// image, so a slot switches to it without draining.
func (t *Tunics) SetCasual(slot int) {
	if slot < 0 || slot >= TunicSlotLocal {
		return
	}
	t.want[slot] = TunicWant{Casual: true}
	t.managed[slot] = true
}

// SetLocal sets the local Link's color. Vanilla green needs no swap.
func (t *Tunics) SetLocal(c playercolor.Color) {
	t.Set(TunicSlotLocal, !playercolor.IsVanilla(c), c)
}

func (t *Tunics) noteErr(err error) {
	if err == nil {
		t.lastErr = ""
		return
	}
	if errors.Is(err, errTunicBusy) {
		return
	}
	if s := err.Error(); s != t.lastErr {
		t.lastErr = s
		t.log("tunic colors waiting: %s", s)
	}
}

// Tick moves RAM one step toward the wanted state. It is cheap when
// nothing changes, and safe to call every puppet-sync tick.
func (t *Tunics) Tick(now time.Time) {
	// The mailbox block comes first: the hero read must know the live
	// buffers, because enables survive a Go process (and a recalibration),
	// so the worn header may already be swapped on the very first read.
	blk, err := t.mem.Read(mailbox.Mailbox+mailbox.OffTunicBufs, mailbox.MailboxSize-mailbox.OffTunicBufs)
	if err != nil || len(blk) != mailbox.MailboxSize-mailbox.OffTunicBufs {
		return
	}
	size := binary.BigEndian.Uint32(blk[mailbox.OffTunicBufSize-mailbox.OffTunicBufs:])
	state := blk[mailbox.OffTunicState-mailbox.OffTunicBufs]
	enable := blk[mailbox.OffTunicEnable-mailbox.OffTunicBufs:]

	for s := 0; s < TunicSlots; s++ {
		if buf := binary.BigEndian.Uint32(blk[4*s:]); buf != t.bufs[s] {
			t.bufs[s] = buf
			t.cur[s] = tunicSlotState{}
		}
	}
	// The casual shirt needs no image: C copies mOtherLinktex in and
	// ignores the slot buffer (docs/09 §5.6). So a casual slot is written
	// before every gate below (hero texture, allocation state, buffer size,
	// a slot whose allocation failed), or a hero viewer would see a casual
	// remote in the uniform whenever the color pipeline is not ready.
	for s := 0; s < TunicSlotLocal; s++ {
		if t.managed[s] && t.want[s].Casual {
			t.step(s, 0, enable[s], 0, false)
		}
	}

	if t.hero == nil {
		var live [TunicSlots]uint32
		for s := range live {
			live[s] = binary.BigEndian.Uint32(blk[4*s:])
		}
		h, err := readHeroTexture(t.mem, live, int(size))
		t.noteErr(err)
		if err != nil {
			return
		}
		t.hero = h
		t.mask, t.ref = texture.Calibrate(h.image, texture.DefaultMask)
		t.cache = map[playercolor.Color][]byte{}
		t.log("tunic colors: hero texture %dx%d (%d B) at 0x%08X, green measured at hue %.0f from %d endpoints",
			h.hdr.Width, h.hdr.Height, len(h.image), h.hdr.ImageAddr(h.hdrAddr), t.ref.H, t.ref.Samples)
	}
	if state == 0 {
		if size == 0 {
			var b [4]byte
			binary.BigEndian.PutUint32(b[:], uint32(len(t.hero.image)))
			t.mem.Write(mailbox.Mailbox+mailbox.OffTunicBufSize, b[:])
		}
		return
	}
	if state != t.stateNoted {
		t.stateNoted = state
		if state == 0xFD {
			t.log("tunic colors: GameHeap allocation failed for some slots (state 0xFD)")
		}
	}
	if size != uint32(len(t.hero.image)) {
		// Allocated for another size (a texture we no longer see). The
		// buffers are kept for the session (F18), so the feature stays off.
		t.noteErr(fmt.Errorf("buffers are %d B, the texture is %d B", size, len(t.hero.image)))
		return
	}
	frame, _ := t.mem.ReadU32(mailbox.Mailbox + 0x00) // spawn_trigger, +1 per game frame

	verify := now.Sub(t.lastVerify) >= tunicVerifyEvery
	if verify {
		t.lastVerify = now
		// A different worn header means a different model: recalibrate.
		if link, err := t.mem.ReadU32(addr.PlayerPtr); err == nil && addr.InMEM1(link) {
			if worn, err := t.mem.ReadU32(link + addr.PlayerOffCurrLinktex); err == nil && addr.InMEM1(worn) && worn != t.hero.hdrAddr {
				t.hero = nil
				for s := range t.cur {
					t.cur[s] = tunicSlotState{}
				}
				return
			}
		}
	}

	for s := 0; s < TunicSlots; s++ {
		buf := t.bufs[s]
		if !t.managed[s] || t.want[s].Casual || buf == 0 || !addr.InMEM1(buf) || !addr.InMEM1(buf+size-1) {
			continue
		}
		t.step(s, buf, enable[s], frame, verify)
	}
}

// Settled reports whether slot's RAM state matches what Set asked for.
func (t *Tunics) Settled(slot int) bool {
	w, c := t.want[slot], t.cur[slot]
	if !c.known {
		return false
	}
	if w.Casual {
		return c.casual
	}
	if !w.On {
		return !c.enabled && !c.casual
	}
	return c.enabled && c.color == w.Color
}

// Want returns what slot was last asked to show (Set, SetCasual,
// SetLocal).
func (t *Tunics) Want(slot int) TunicWant { return t.want[slot] }

// Buf returns slot's image buffer as last read from the mailbox (0
// until the C side allocated it).
func (t *Tunics) Buf(slot int) uint32 { return t.bufs[slot] }

// image returns the slot image for c (cached per color).
func (t *Tunics) image(c playercolor.Color) []byte {
	if img, ok := t.cache[c]; ok {
		return img
	}
	img := t.hero.image
	if !playercolor.IsVanilla(c) {
		img, _ = texture.Recolor(t.hero.image, c, t.mask, t.ref)
	}
	t.cache[c] = img
	return img
}

// DisableAll turns every slot's swap off in RAM. puppet-sync calls it on
// teardown so the C side stops recoloring once the session ends; the
// GameHeap buffers stay allocated (F18) and are reused next session.
func (t *Tunics) DisableAll() {
	t.mem.Write(mailbox.Mailbox+mailbox.OffTunicEnable, make([]byte, TunicSlots))
	for s := range t.cur {
		t.cur[s] = tunicSlotState{}
	}
}

func (t *Tunics) setEnable(slot int, on bool) {
	v := byte(0)
	if on {
		v = TunicEnableOn
	}
	t.mem.Write(mailbox.Mailbox+mailbox.OffTunicEnable+uint32(slot), []byte{v})
}

// step is one slot's state machine: off → (drain) → write → on. The
// casual state is entered directly (C reads no image for it) and left
// through off and the drain, like a color change.
func (t *Tunics) step(s int, buf uint32, enInRAM byte, frame uint32, verify bool) {
	w, c := t.want[s], &t.cur[s]
	if c.known && (c.enabled != (enInRAM == TunicEnableOn) || c.casual != (enInRAM == TunicEnableCasual)) {
		// Someone else wrote the enable (a save-state load, tunic-slot):
		// start over from "unknown".
		*c = tunicSlotState{}
	}
	if c.known && c.enabled && verify && len(c.img) > 0 {
		// The whole image (at most tunicMaxImage per slot, once a second):
		// a prefix can be identical for every color, so only a full
		// compare catches a save-state load that left another color's
		// image behind an enable of 1.
		if got, err := t.mem.Read(buf, len(c.img)); err == nil && !bytes.Equal(got, c.img) {
			*c = tunicSlotState{} // image overwritten (a save-state load): rewrite it
		}
	}
	switch {
	case w.Casual:
		if !c.known || !c.casual {
			t.mem.Write(mailbox.Mailbox+mailbox.OffTunicEnable+uint32(s), []byte{TunicEnableCasual})
			*c = tunicSlotState{known: true, casual: true}
		}
	case !w.On:
		if !c.known || c.enabled || c.casual {
			t.setEnable(s, false)
			*c = tunicSlotState{known: true, drainAt: frame}
		}
	case c.known && c.enabled && c.color == w.Color:
		// Up to date.
	case !c.known || c.enabled || c.casual:
		t.setEnable(s, false)
		*c = tunicSlotState{known: true, drainAt: frame}
	case frame-c.drainAt >= tunicDrainFrames:
		img := t.image(w.Color)
		if err := t.mem.Write(buf, img); err != nil {
			return
		}
		t.setEnable(s, true)
		*c = tunicSlotState{known: true, enabled: true, color: w.Color, img: img}
	}
}
