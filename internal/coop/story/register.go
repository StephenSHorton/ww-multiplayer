package story

import (
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
)

// ApplyRegister returns event register byte i (mEvent 0x79-0xFF) of the
// receiver after the world's byte w is applied to its own byte l, by §3's
// allowlist:
//
//   - a figurine bitfield (class OR, mask 0xFF): l | w;
//   - a MAX register: w's field (w & mask) where it is above l's, with the
//     bits of l outside the mask kept, as setEventReg's mask-and-set does
//     (d_save.cpp:1202-1205): a letter register's field is the byte's low
//     two bits (0xHH03), and the other six stay the receiver's own;
//   - every other byte, and an i outside the registers: l.
//
// It never clears a bit of l and never lowers a field.
func ApplyRegister(i int, l, w byte) byte {
	if i < addr.EventRegFirst || i >= addr.EventSize {
		return l
	}
	r := class.Of(addr.OffEvent + i)
	switch {
	case !r.Class.Merges():
		return l
	case r.Class == class.Max:
		if f := w & r.Mask; f > l&r.Mask {
			return l&^r.Mask | f
		}
		return l
	}
	return l | w&r.Mask
}

// ApplyRegisters applies the world's registers w to img, a receiver's
// canonical image (as at a load: a save merge, or the getSave shim of M7),
// byte by byte with ApplyRegister, and returns how many bytes changed.
// Nothing outside the allowlisted registers is touched.
func ApplyRegisters(img, w *world.Image) int {
	n := 0
	for i := addr.EventRegFirst; i < addr.EventSize; i++ {
		off := addr.OffEvent + i
		if v := ApplyRegister(i, img[off], w[off]); v != img[off] {
			img[off] = v
			n++
		}
	}
	return n
}

// ApplyPlan applies p's writes to img as the receiver's writer would, the
// story bits by OR and the registers by ApplyRegister, and returns img's
// new value: the image the receiver reaches if every write lands.
func ApplyPlan(img *world.Image, p Plan) world.Image {
	out := *img
	for _, g := range p.Bits {
		if IsStoryByte(int(g.Off)) {
			out[g.Off] |= g.Val & class.SyncMaskOf(int(g.Off)-addr.OffEvent)
		}
	}
	for _, g := range p.Regs {
		if i := int(g.Off) - addr.OffEvent; IsRegisterByte(int(g.Off)) {
			out[g.Off] = ApplyRegister(i, out[g.Off], g.Val)
		}
	}
	return out
}
