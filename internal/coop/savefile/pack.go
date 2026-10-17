package savefile

import (
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// member is one struct memory_to_card and card_to_memory copy, in the
// order they copy them (d_save.cpp:1708-1781, 1799-1868): the packed
// image is the members back to back (dSv_save_c_PACKED, d_save.h:614-635,
// 922-930), and the image's alignment padding between them (0x08E,
// 0x0C1-0x0C3, 0x1A9-0x1AB, 0x37C-0x37F and 0x774-0x777, d_save.h:
// 593-605, 913) is on neither side. A member's own padding (status_a's
// last byte, a memBit's last two) is copied with it.
type member struct {
	off, size int // in the 0x778-byte image
}

var members = []member{
	{addr.OffStatusA, addr.StatusASize},
	{addr.OffStatusB, addr.StatusBSize},
	{addr.OffReturnPlace, addr.ReturnPlaceSize},
	{addr.OffItems, addr.ItemsSize},
	{addr.OffGetItem, addr.GetItemSize},
	{addr.OffItemRecord, addr.ItemRecordSize},
	{addr.OffItemMax, addr.ItemMaxSize},
	{addr.OffBagItem, addr.BagItemSize},
	{addr.OffGetBagItem, addr.GetBagItemSize},
	{addr.OffBagItemRecord, addr.BagItemRecordSize},
	{addr.OffCollect, addr.CollectSize},
	{addr.OffMap, addr.MapSize},
	{addr.OffInfo, addr.InfoSize},
	{addr.OffConfig, addr.ConfigSize},
	{addr.OffPriest, addr.PriestSize},
	{addr.OffStatusC, addr.StatusCCount * addr.StatusCSize},
	{addr.OffMemory, addr.TableCount * addr.MemBitSize},
	{addr.OffOcean, addr.OceanSize},
	{addr.OffEvent, addr.EventSize},
	{addr.OffReserve, addr.ReserveSize},
}

// Packed is one slot's save image as the card holds it.
type Packed [PackedSize]byte

// Unpack returns p as the 0x778-byte image, as card_to_memory lays it
// out in dSv_save_c; the padding the card does not hold reads 0.
func Unpack(p *Packed) world.Image {
	var img world.Image
	n := 0
	for _, m := range members {
		n += copy(img[m.off:m.off+m.size], p[n:])
	}
	return img
}

// Pack returns img as the card holds it, as memory_to_card copies it. The
// image's padding is dropped. Unlike memory_to_card, nothing is fixed up:
// the card copy of life is not raised to 12 (d_save.cpp:1710-1712) and
// mDateIPL is not stamped (d_save.cpp:1716-1717). Those are per-player
// bytes, and an image unpacked from a card already had both done.
func Pack(img *world.Image) Packed {
	var p Packed
	n := 0
	for _, m := range members {
		n += copy(p[n:], img[m.off:m.off+m.size])
	}
	return p
}

// PackedOff maps image offset off to its offset in the packed slot; ok
// is false for the padding the card does not hold.
func PackedOff(off int) (p int, ok bool) {
	for _, m := range members {
		if off >= m.off && off < m.off+m.size {
			return p + off - m.off, true
		}
		p += m.size
	}
	return 0, false
}
