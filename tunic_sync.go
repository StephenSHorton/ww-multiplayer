package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/dolphin"
	"github.com/StephenSHorton/ww-multiplayer/internal/network"
	"github.com/StephenSHorton/ww-multiplayer/internal/playercolor"
	"github.com/StephenSHorton/ww-multiplayer/internal/tunic"
)

// Tunic colors, render side (docs/09-player-colors.md §5.3-§5.4).
//
// The C side (inject/src/multiplayer.c tunic_swap / tunic_restore)
// repoints Link's worn "linktexS3TC" header at a per-slot image around
// each Link's draw. This file is the Go half: it reads the hero's-clothes
// image once, recolors it per slot with internal/tunic, writes the images
// into the three GameHeap buffers the C side allocates once, and flips
// the per-slot enables.

// Mailbox offsets (inject/include/mailbox.h, "TUNIC COLORS").
const (
	mailboxTunicBufs    = 0x188 // u32[3], C: image per slot
	mailboxTunicBufSize = 0x194 // u32, Go: bytes per image; nonzero asks C to allocate
	mailboxTunicEnable  = 0x198 // u8[3], Go: 1 = slot image valid, swap on
	mailboxTunicState   = 0x19B // u8, C: 0 none, 1 allocated, 0xFD alloc failed
	mailboxTunicSwaps   = 0x19C // u32, C: header swaps done
	mailboxTunicEnd     = 0x1A0 // first byte past the tunic block (Mailbox size)

	tunicSlots     = 3
	tunicSlotLocal = 2 // TUNIC_SLOT_LOCAL: Link #1 on this Dolphin

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

// tunicMem is the Dolphin memory access the tunic code needs;
// *dolphin.Dolphin implements it.
type tunicMem interface {
	ReadAbsolute(addr uint32, n int) ([]byte, error)
	ReadU32(addr uint32) (uint32, error)
	WriteAbsolute(addr uint32, data []byte) error
}

func mailboxTunicBuf(slot int) uint32 { return uint32(mailboxTunicBufs + 4*slot) }

// errTunicBusy means the worn header pointed at one of our buffers: the
// C bracket was mid-swap during the read. Retry on a later tick.
var errTunicBusy = errors.New("linktexS3TC header is mid-swap")

// heroTexture is Link's hero's-clothes body texture as read from RAM.
type heroTexture struct {
	hdrAddr uint32        // the worn header (mpCurrLinktex)
	hdr     tunic.ResTIMG // the hero's-clothes header
	image   []byte        // the original CMPR image
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
func readHeroTexture(m tunicMem, skip [tunicSlots]uint32, skipSize int) (*heroTexture, error) {
	link, err := m.ReadU32(playerPtrArray0)
	if err != nil || !inMEM1(link) {
		return nil, errors.New("Link not loaded")
	}
	worn, err := m.ReadU32(link + daPyMpCurrLinktexOff)
	if err != nil || !inMEM1(worn) {
		return nil, fmt.Errorf("mpCurrLinktex not set (0x%08X)", worn)
	}
	flg, err := m.ReadU32(link + daPyNoResetFlg1Off)
	if err != nil {
		return nil, err
	}
	src := worn
	if flg&daPyFlg1Casual != 0 {
		src = link + daPyOtherLinktexOff
	}
	a, err1 := m.ReadAbsolute(src, tunic.ResTIMGSize)
	b, err2 := m.ReadAbsolute(src, tunic.ResTIMGSize)
	if err1 != nil || err2 != nil || len(a) != tunic.ResTIMGSize || !bytes.Equal(a, b) {
		return nil, errTunicBusy
	}
	h, _ := tunic.ParseResTIMG(a)
	size, ok := h.ImageSize()
	if !ok {
		return nil, fmt.Errorf("hero texture format 0x%02X is not CMPR", h.Format)
	}
	if size <= 0 || size > tunicMaxImage {
		return nil, fmt.Errorf("hero texture is %d bytes (%dx%d), over the %d-byte cap", size, h.Width, h.Height, tunicMaxImage)
	}
	img := h.ImageAddr(worn)
	if !inMEM1(img) || !inMEM1(img+uint32(size)-1) {
		return nil, fmt.Errorf("hero image 0x%08X is outside MEM1", img)
	}
	span := uint32(max(skipSize, size))
	for _, s := range skip {
		if s != 0 && img >= s && img < s+span {
			return nil, errTunicBusy
		}
	}
	data, err := m.ReadAbsolute(img, size)
	if err != nil || len(data) != size {
		return nil, errors.New("cannot read the hero texture")
	}
	return &heroTexture{hdrAddr: worn, hdr: h, image: data}, nil
}

// tunicWant is what a slot should show.
type tunicWant struct {
	on    bool
	color playercolor.Color
}

// tunicSlotState is what Go last put in RAM for a slot.
type tunicSlotState struct {
	known   bool // enable byte state is known (written by us since the buffer appeared)
	enabled bool
	color   playercolor.Color // image content while enabled
	drainAt uint32            // spawn_trigger when the slot was disabled
	img     []byte            // the written image (a cache entry, never mutated), for verify
}

// tunicSync drives the three tunic slots of one Dolphin.
type tunicSync struct {
	mem tunicMem
	log func(format string, args ...any)

	hero  *heroTexture
	mask  tunic.Mask
	ref   tunic.Reference
	cache map[playercolor.Color][]byte

	bufs       [tunicSlots]uint32
	managed    [tunicSlots]bool // Tick leaves unmanaged slots alone
	want       [tunicSlots]tunicWant
	cur        [tunicSlots]tunicSlotState
	lastVerify time.Time
	lastErr    string
	stateNoted byte
}

func newTunicSync(m tunicMem, log func(format string, args ...any)) *tunicSync {
	if log == nil {
		log = func(string, ...any) {}
	}
	return &tunicSync{mem: m, log: log, cache: map[playercolor.Color][]byte{}}
}

// Set records what slot should show: off, or the tunic in color c. A
// vanilla c on a remote slot still swaps (to the unmodified image), so a
// green remote is drawn in hero green even while the local player wears
// the casual clothes.
func (t *tunicSync) Set(slot int, on bool, c playercolor.Color) {
	if slot < 0 || slot >= tunicSlots {
		return
	}
	t.want[slot] = tunicWant{on: on, color: c}
	t.managed[slot] = true
}

// SetLocal sets the local Link's color. Vanilla green needs no swap.
func (t *tunicSync) SetLocal(c playercolor.Color) {
	t.Set(tunicSlotLocal, !playercolor.IsVanilla(c), c)
}

func (t *tunicSync) noteErr(err error) {
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
func (t *tunicSync) Tick(now time.Time) {
	// The mailbox block comes first: the hero read must know the live
	// buffers, because enables survive a Go process (and a recalibration),
	// so the worn header may already be swapped on the very first read.
	blk, err := t.mem.ReadAbsolute(mailboxBase+mailboxTunicBufs, mailboxTunicEnd-mailboxTunicBufs)
	if err != nil || len(blk) != mailboxTunicEnd-mailboxTunicBufs {
		return
	}
	size := binary.BigEndian.Uint32(blk[mailboxTunicBufSize-mailboxTunicBufs:])
	state := blk[mailboxTunicState-mailboxTunicBufs]

	if t.hero == nil {
		var live [tunicSlots]uint32
		for s := range live {
			live[s] = binary.BigEndian.Uint32(blk[4*s:])
		}
		h, err := readHeroTexture(t.mem, live, int(size))
		t.noteErr(err)
		if err != nil {
			return
		}
		t.hero = h
		t.mask, t.ref = tunic.Calibrate(h.image, tunic.DefaultMask)
		t.cache = map[playercolor.Color][]byte{}
		t.log("tunic colors: hero texture %dx%d (%d B) at 0x%08X, green measured at hue %.0f from %d endpoints",
			h.hdr.Width, h.hdr.Height, len(h.image), h.hdr.ImageAddr(h.hdrAddr), t.ref.H, t.ref.Samples)
	}
	if state == 0 {
		if size == 0 {
			var b [4]byte
			binary.BigEndian.PutUint32(b[:], uint32(len(t.hero.image)))
			t.mem.WriteAbsolute(mailboxBase+mailboxTunicBufSize, b[:])
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
	frame, _ := t.mem.ReadU32(mailboxBase + 0x00) // spawn_trigger, +1 per game frame

	verify := now.Sub(t.lastVerify) >= tunicVerifyEvery
	if verify {
		t.lastVerify = now
		// A different worn header means a different model: recalibrate.
		if link, err := t.mem.ReadU32(playerPtrArray0); err == nil && inMEM1(link) {
			if worn, err := t.mem.ReadU32(link + daPyMpCurrLinktexOff); err == nil && inMEM1(worn) && worn != t.hero.hdrAddr {
				t.hero = nil
				for s := range t.cur {
					t.cur[s] = tunicSlotState{}
				}
				return
			}
		}
	}

	for s := 0; s < tunicSlots; s++ {
		buf := binary.BigEndian.Uint32(blk[4*s:])
		if buf != t.bufs[s] {
			t.bufs[s] = buf
			t.cur[s] = tunicSlotState{}
		}
		if !t.managed[s] || buf == 0 || !inMEM1(buf) || !inMEM1(buf+size-1) {
			continue
		}
		enabled := blk[mailboxTunicEnable-mailboxTunicBufs+s] != 0
		t.step(s, buf, enabled, frame, verify)
	}
}

// Settled reports whether slot's RAM state matches what Set asked for.
func (t *tunicSync) Settled(slot int) bool {
	w, c := t.want[slot], t.cur[slot]
	if !c.known {
		return false
	}
	if !w.on {
		return !c.enabled
	}
	return c.enabled && c.color == w.color
}

// image returns the slot image for c (cached per color).
func (t *tunicSync) image(c playercolor.Color) []byte {
	if img, ok := t.cache[c]; ok {
		return img
	}
	img := t.hero.image
	if !playercolor.IsVanilla(c) {
		img, _ = tunic.Recolor(t.hero.image, c, t.mask, t.ref)
	}
	t.cache[c] = img
	return img
}

// DisableAll turns every slot's swap off in RAM. puppet-sync calls it on
// teardown so the C side stops recoloring once the session ends; the
// GameHeap buffers stay allocated (F18) and are reused next session.
func (t *tunicSync) DisableAll() {
	t.mem.WriteAbsolute(mailboxBase+mailboxTunicEnable, make([]byte, tunicSlots))
	for s := range t.cur {
		t.cur[s] = tunicSlotState{}
	}
}

func (t *tunicSync) setEnable(slot int, on bool) {
	v := byte(0)
	if on {
		v = 1
	}
	t.mem.WriteAbsolute(mailboxBase+mailboxTunicEnable+uint32(slot), []byte{v})
}

// step is one slot's state machine: off → (drain) → write → on.
func (t *tunicSync) step(s int, buf uint32, enabledInRAM bool, frame uint32, verify bool) {
	w, c := t.want[s], &t.cur[s]
	if c.known && c.enabled != enabledInRAM {
		// Someone else wrote the enable (a save-state load, tunic-slot):
		// start over from "unknown".
		*c = tunicSlotState{}
	}
	if c.known && c.enabled && verify && len(c.img) > 0 {
		// The whole image (at most tunicMaxImage per slot, once a second):
		// a prefix can be identical for every color, so only a full
		// compare catches a save-state load that left another color's
		// image behind an enable of 1.
		if got, err := t.mem.ReadAbsolute(buf, len(c.img)); err == nil && !bytes.Equal(got, c.img) {
			*c = tunicSlotState{} // image overwritten (a save-state load): rewrite it
		}
	}
	switch {
	case !w.on:
		if !c.known || c.enabled {
			t.setEnable(s, false)
			*c = tunicSlotState{known: true, drainAt: frame}
		}
	case c.known && c.enabled && c.color == w.color:
		// Up to date.
	case !c.known || c.enabled:
		t.setEnable(s, false)
		*c = tunicSlotState{known: true, drainAt: frame}
	case frame-c.drainAt >= tunicDrainFrames:
		img := t.image(w.color)
		if err := t.mem.WriteAbsolute(buf, img); err != nil {
			return
		}
		t.setEnable(s, true)
		*c = tunicSlotState{known: true, enabled: true, color: w.color, img: img}
	}
}

// remoteCasual reports whether a remote's pose trailer says it wears the
// casual clothes: byte faceStateWireBytes (the 17th) is the appearance
// byte, bit 0 = casual (docs/09 §5.4). Senders before it send 16 bytes.
func remoteCasual(face []byte) bool {
	return len(face) > faceStateWireBytes && face[faceStateWireBytes]&appearanceCasual != 0
}

const appearanceCasual = 0x01

// localAppearance is the appearance byte broadcast-pose appends to the
// face trailer: bit 0 = Link #1 wears the casual clothes. ok is false
// while Link isn't loaded.
func localAppearance(m tunicMem) (b byte, ok bool) {
	link, err := m.ReadU32(playerPtrArray0)
	if err != nil || !inMEM1(link) {
		return 0, false
	}
	flg, err := m.ReadU32(link + daPyNoResetFlg1Off)
	if err != nil {
		return 0, false
	}
	if flg&daPyFlg1Casual != 0 {
		b |= appearanceCasual
	}
	return b, true
}

// printTunicMailbox is the dump command's tunic block.
func printTunicMailbox(m tunicMem) {
	blk, err := m.ReadAbsolute(mailboxBase+mailboxTunicBufs, mailboxTunicEnd-mailboxTunicBufs)
	if err != nil || len(blk) != mailboxTunicEnd-mailboxTunicBufs {
		return
	}
	at := func(off int) []byte { return blk[off-mailboxTunicBufs:] }
	fmt.Printf("tunic: state=0x%02X  buf_size=%d  swaps=%d\n",
		at(mailboxTunicState)[0], binary.BigEndian.Uint32(at(mailboxTunicBufSize)), binary.BigEndian.Uint32(at(mailboxTunicSwaps)))
	for s := 0; s < tunicSlots; s++ {
		name := fmt.Sprintf("slot %d", s)
		if s == tunicSlotLocal {
			name = "local "
		}
		fmt.Printf("tunic[%s]: buf=0x%08X  enable=%d\n", name, binary.BigEndian.Uint32(at(mailboxTunicBufs+4*s)), at(mailboxTunicEnable)[s])
	}
}

// syncTunics sets every slot's wanted color from the session and steps
// RAM toward it. Link slots follow remoteToLinkSlot; a remote in the
// casual clothes is left unswapped (v1 recolors the hero's clothes only).
// Only server-assigned colors are drawn: until this client's own color
// arrives (a server from before colors, or the moment before the first
// player list) every slot stays off, so everyone keeps the vanilla look
// instead of a by-ID guess that other clients would not agree on (each
// human has two connections, so two IDs).
//
// mine/haveMine are network.Client.MyColor().
func syncTunics(t *tunicSync, mine playercolor.Color, haveMine bool, remotes []network.RemotePlayer, linkSlot map[byte]int, now time.Time) {
	if !haveMine {
		for s := 0; s < tunicSlots; s++ {
			t.Set(s, false, playercolor.Color{})
		}
		t.Tick(now)
		return
	}
	t.SetLocal(mine)
	var set [tunicSlotLocal]bool
	for i := range remotes {
		rp := &remotes[i]
		s, ok := linkSlot[rp.ID]
		if !ok || s < 0 || s >= tunicSlotLocal {
			continue
		}
		t.Set(s, rp.HasColor && !remoteCasual(rp.FaceState), rp.Color)
		set[s] = true
	}
	for s, ok := range set {
		if !ok {
			t.Set(s, false, playercolor.Color{})
		}
	}
	t.Tick(now)
}

// runTunicSlot is the tunic-slot dev command (docs/09 §7.2): put one
// slot's tunic in a color, or turn it off, on the selected Dolphin. Run
// puppet-sync / mp-local with WW_TUNIC=off alongside it, or puppet-sync
// puts the session's colors back.
func runTunicSlot(slotArg, colorArg string) {
	slot := -1
	switch slotArg {
	case "0", "1":
		slot = int(slotArg[0] - '0')
	case "local", "2":
		slot = tunicSlotLocal
	}
	if slot < 0 {
		fmt.Println("slot must be 0, 1 or local")
		os.Exit(1)
	}
	on, c := false, playercolor.Color{}
	if colorArg != "off" {
		var err error
		if c, err = playercolor.Parse(colorArg); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		on = true
	}
	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	t := newTunicSync(d, func(format string, args ...any) { fmt.Printf(format+"\n", args...) })
	t.Set(slot, on, c)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		t.Tick(time.Now())
		if t.Settled(slot) {
			if on {
				fmt.Printf("tunic slot %s is %s (buf 0x%08X)\n", slotArg, c, t.bufs[slot])
			} else {
				fmt.Printf("tunic slot %s is off\n", slotArg)
			}
			return
		}
		time.Sleep(16 * time.Millisecond)
	}
	fmt.Println("timed out; run dump: tunic state must be 1 (the C side allocates after ~5 s in game)")
	os.Exit(1)
}
