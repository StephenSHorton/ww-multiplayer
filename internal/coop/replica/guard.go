package replica

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/mem"
)

// Guard is the WriteGuard (§4.2): the allowlist every game-memory write
// of the co-op pipeline must fall inside, with the bits each byte may
// change. It is computed from the class table and the address table:
//
//   - the image bytes that merge (OR, OR_MASKED, MAX: their class mask)
//     and the DERIVED bytes a receiver recomputes (mItems slots,
//     mSelectEquip, each table's mKeyNum), except mMaxLife and
//     mMaxMagic, which change only through their pending counters (§3);
//   - the live memBit, with the classes of a saved slot;
//   - the dan switch words (mDan.mSwitch) and every zone's switch and
//     dead-actor words (dSv_zone_c +0x02..+0x07 and +0x0C..+0x4B; the
//     zone's own item bits and its mRoomNo are not), which a writer
//     still validates against the live zone table (§3.1);
//   - the three pending counters, mItemKeyNumCount, mItemMaxLifeCount
//     and mItemMaxMagicCount (§6.4);
//   - play.mSelectItem, refreshed after a slot changes (§6.6);
//   - the binding tag (bind.TagRegion), the first 0x30 bytes of the
//     image's mReserve.
//
// Nothing per player is in it: not mLife (a 0 kills Link, §5.1), not
// rupees, ammo, bottles, names or options. The K1 timer bytes of M6 live
// in actors on the heap, so they are validated per write and are not
// part of this static list.
//
// M2 computes the guard and tests it but writes nothing through it; M3's
// FrameWriter checks every write with Check before making it.
type Guard struct {
	spans []guardSpan // ascending, non-overlapping
}

type guardSpan struct {
	start uint32
	mask  []byte // the bits each byte may change
	name  string
}

func (s guardSpan) end() uint32 { return s.start + uint32(len(s.mask)) }

// ErrGuard is matched (errors.Is) by every refusal Check returns.
var ErrGuard = errors.New("replica: write refused by the WriteGuard")

// NewGuard computes the allowlist.
func NewGuard() *Guard {
	g := &Guard{}
	add := func(start uint32, mask []byte, name string) {
		g.spans = append(g.spans, guardSpan{start: start, mask: mask, name: name})
	}
	fill := func(n int, v byte) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = v
		}
		return b
	}

	img := make([]byte, addr.SaveImageSize)
	for off := range img {
		img[off] = imageWriteMask(off)
	}
	// The tag is inside the image, at mReserve +0x00..+0x2F.
	tag := int(bind.TagRegion.Start - addr.SaveImage)
	copy(img[tag:], fill(int(bind.TagRegion.Size()), 0xFF))
	add(addr.SaveImage, img, "dSv_save_c")

	live := make([]byte, addr.MemBitSize)
	for i := range live {
		live[i] = imageWriteMask(addr.OffMemBit(0) + i)
	}
	add(addr.LiveMemBit, live, "live memBit")

	add(addr.DanSwitch, fill(addr.DanSize-addr.DanOffSwitch, 0xFF), "mDan.mSwitch")
	for z := range addr.ZoneCount {
		base, _ := addr.ZoneAddr(z)
		add(base+addr.ZoneOffSwitch, fill(addr.ZoneOffItem-addr.ZoneOffSwitch, 0xFF), fmt.Sprintf("mZone[%d] switches", z))
		add(base+addr.ZoneOffActor, fill(addr.ZoneSize-addr.ZoneOffActor, 0xFF), fmt.Sprintf("mZone[%d] dead actors", z))
	}

	add(addr.ItemKeyNumCount, fill(4, 0xFF), "mItemKeyNumCount, mItemMaxLifeCount") // adjacent s16s (d_com_inf_game.h:799-800)
	add(addr.ItemMaxMagicCount, fill(2, 0xFF), "mItemMaxMagicCount")
	add(addr.PlaySelectItem, fill(4, 0xFF), "play.mSelectItem")

	slices.SortFunc(g.spans, func(a, b guardSpan) int { return cmp.Compare(a.start, b.start) })
	return g
}

// imageWriteMask is the bits of image byte off a write may change.
func imageWriteMask(off int) byte {
	r := class.Of(off)
	switch {
	case r.Class.Merges():
		return r.Mask
	case r.Class != class.Derived:
		return 0
	case off >= addr.OffMaxLife && off < addr.OffMaxLife+2, off == addr.OffMaxMagic:
		return 0 // through mItemMaxLifeCount and mItemMaxMagicCount only
	}
	return 0xFF
}

// Mask returns the bits a write may change in the byte at a, 0 if a is
// outside the allowlist.
func (g *Guard) Mask(a uint32) byte {
	i, ok := slices.BinarySearchFunc(g.spans, a, func(s guardSpan, a uint32) int {
		switch {
		case a < s.start:
			return 1
		case a >= s.end():
			return -1
		}
		return 0
	})
	if !ok {
		return 0
	}
	s := g.spans[i]
	return s.mask[a-s.start]
}

// Allows reports whether every byte of [a, a+n) is in the allowlist.
func (g *Guard) Allows(a uint32, n int) bool {
	if mem.CheckRange(a, n) != nil {
		return false
	}
	for i := range n {
		if g.Mask(a+uint32(i)) == 0 {
			return false
		}
	}
	return true
}

// Check reports why writing b over old at a would be refused: a byte
// outside the allowlist, or a changed bit outside its byte's mask. old
// is what a fresh read of the bytes returned, as FrameWriter's
// read-OR-write does (§4.2).
func (g *Guard) Check(a uint32, old, b []byte) error {
	if len(old) != len(b) {
		return fmt.Errorf("%w: %d bytes over %d read", ErrGuard, len(b), len(old))
	}
	if err := mem.CheckRange(a, len(b)); err != nil {
		return fmt.Errorf("%w: %v", ErrGuard, err)
	}
	for i := range b {
		at := a + uint32(i)
		m := g.Mask(at)
		if m == 0 {
			return fmt.Errorf("%w: 0x%08X is not a co-op write target", ErrGuard, at)
		}
		if d := (old[i] ^ b[i]) &^ m; d != 0 {
			return fmt.Errorf("%w: 0x%08X bits 0x%02X are outside its mask 0x%02X", ErrGuard, at, d, m)
		}
	}
	return nil
}

// ErrDryRun is what FrameWriter returns in M2: game data is never
// written (§5.1 principle 5).
var ErrDryRun = errors.New("replica: dry run: co-op writes no game data in this version")

// FrameWriter will make the planned writes right after the heartbeat
// moves (§4.2, §4.2.1): spin-poll spawn_trigger with a 100 ms timeout,
// then for each planned byte a fresh 1-byte read, OR and 1-byte write
// through Guard.Check, verified on the next snapshot. M3 fills it in. In
// M2 Apply refuses everything without touching memory, so a caller wired
// to it early cannot write.
type FrameWriter struct {
	Mem   mem.Mem
	Guard *Guard
}

// Apply would carry out ws. In M2 it returns ErrDryRun and writes
// nothing.
func (w *FrameWriter) Apply(ws []Write) error { return ErrDryRun }
