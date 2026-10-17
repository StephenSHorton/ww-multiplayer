package story

import (
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// TestPolicy: the story policy opens exactly the SyncMask of the event bit
// bytes and leaves every other byte as its base has it.
func TestPolicy(t *testing.T) {
	for _, c := range []struct {
		name string
		base *world.Policy
	}{{"default", nil}, {"full", world.FullPolicy()}} {
		p := Policy(c.base)
		base := c.base
		if base == nil {
			base = world.DefaultPolicy()
		}
		for off := range p {
			want := base[off]
			if IsStoryByte(off) {
				want = class.SyncMaskOf(off - addr.OffEvent)
			}
			if p[off] != want {
				t.Errorf("%s: byte 0x%X is 0x%02X, want 0x%02X", c.name, off, p[off], want)
			}
		}
		if c.base != nil && c.base == p {
			t.Errorf("%s: Policy returned its base", c.name)
		}
	}

	// A world with it takes a story bit that syncs and refuses the rest.
	w := world.New(world.ID{1}, time.Unix(0, 0))
	w.SetPolicy(Policy(nil))
	r := w.ApplyGains([]world.Gain{ev(ev0520), ev(evRodeKorl), reg(regLetter, 2)})
	img := w.Image()
	if !eventSet(&img, ev0520) || eventSet(&img, evRodeKorl) || img[addr.OffEvent+regLetter] != 2 {
		t.Errorf("world after the gains: delta %v rejects %v", r.Delta, r.Rejected)
	}
	for _, e := range append(append(class.LocalOnly(), class.GameCleared()...), class.Refight()...) {
		r := w.ApplyGains([]world.Gain{ev(e.ID)})
		if r.Changed() {
			t.Errorf("the world took 0x%04X (%s)", e.ID, e.Name)
		}
	}
}

// TestByteKinds: the story bytes, registers and switches are where addr
// says, and apart.
func TestByteKinds(t *testing.T) {
	story, regs, sws := 0, 0, 0
	for off := range world.ImageSize {
		k := 0
		if IsStoryByte(off) {
			story++
			k++
		}
		if IsRegisterByte(off) {
			regs++
			k++
		}
		if isSwitchByte(off) {
			sws++
			k++
		}
		if k > 1 {
			t.Errorf("byte 0x%X is of %d kinds", off, k)
		}
	}
	if story != addr.EventBitBytes || regs != 17+14 || sws != addr.TableCount*16 {
		t.Errorf("story %d registers %d switches %d", story, regs, sws)
	}
	for _, off := range []int{-1, world.ImageSize, world.ImageSize + 100} {
		if IsStoryByte(off) || IsRegisterByte(off) || isSwitchByte(off) {
			t.Errorf("offset %d has a kind", off)
		}
	}
}

// FuzzPending: whatever the world, the receiver and its history, the plan
// sets only SyncMask bits and allowlisted registers the receiver lacks and
// never had, keeps the held bits out, never clears or lowers anything,
// and holds nothing it releases.
func FuzzPending(f *testing.F) {
	full := make([]byte, 4*addr.EventSize)
	for i := range full {
		full[i] = 0xFF
	}
	f.Add(full, byte(0))
	f.Add(make([]byte, 4*addr.EventSize), byte(masterSword1))
	f.Add([]byte{0x0F, 0x80, 0x08, 0x09}, byte(heroSword))
	f.Fuzz(func(t *testing.T, data []byte, swordItem byte) {
		// data fills mEvent of view, raw, the ever-had set and everMax in
		// turn.
		var view, raw, everMax world.Image
		var ever world.BitSet
		for i, b := range data {
			if i >= 4*addr.EventSize {
				break
			}
			off := addr.OffEvent + i%addr.EventSize
			switch i / addr.EventSize {
			case 0:
				view[off] = b
			case 1:
				raw[off] = b
			case 2:
				ever[off] = b
			case 3:
				everMax[off] = b
			}
		}
		raw[addr.OffSelectEquip] = swordItem
		p := Pending(&view, &raw, &ever, &everMax)

		var held world.Image
		for _, h := range p.Held {
			held[h.Off] |= h.Mask
			if h.Mask&^addr.EventByte(h.Hold.ID).Mask != 0 || int(h.Off) != addr.OffEvent+addr.EventByte(h.Hold.ID).Off {
				t.Fatalf("held %v is not its hold's bit", h)
			}
			if raw[h.Off]&h.Mask != 0 || view[h.Off]&h.Mask != h.Mask {
				t.Fatalf("held %v the receiver has, or the world lacks", h)
			}
		}
		for _, g := range p.Bits {
			off, i := int(g.Off), int(g.Off)-addr.OffEvent
			if !IsStoryByte(off) || g.Val == 0 || g.Val&^class.SyncMaskOf(i) != 0 ||
				g.Val&^view[off] != 0 || g.Val&raw[off] != 0 || g.Val&ever[off] != 0 || g.Val&held[off] != 0 {
				t.Fatalf("planned story bits %v (view 0x%02X raw 0x%02X ever 0x%02X held 0x%02X)",
					g, view[off], raw[off], ever[off], held[off])
			}
		}
		for _, g := range p.Regs {
			off := int(g.Off)
			r := class.Of(off)
			if !IsRegisterByte(off) || g.Val == 0 || g.Val&^r.Mask != 0 {
				t.Fatalf("planned register %v", g)
			}
			if r.Class == class.Max {
				if g.Val != view[off]&r.Mask || g.Val <= raw[off]&r.Mask || g.Val <= everMax[off]&r.Mask {
					t.Fatalf("planned MAX %v (view 0x%02X raw 0x%02X everMax 0x%02X)", g, view[off], raw[off], everMax[off])
				}
			} else if g.Val&^view[off] != 0 || g.Val&raw[off] != 0 || g.Val&ever[off] != 0 {
				t.Fatalf("planned OR register %v", g)
			}
		}

		after := ApplyPlan(&raw, p)
		for off := range after {
			r := class.Of(off)
			switch {
			case after[off] == raw[off]:
			case IsStoryByte(off) || r.Class == class.OR:
				if after[off]&raw[off] != raw[off] {
					t.Fatalf("byte 0x%X cleared: 0x%02X -> 0x%02X", off, raw[off], after[off])
				}
			case r.Class == class.Max:
				if after[off]&^r.Mask != raw[off]&^r.Mask || after[off]&r.Mask < raw[off]&r.Mask {
					t.Fatalf("MAX byte 0x%X: 0x%02X -> 0x%02X", off, raw[off], after[off])
				}
			default:
				t.Fatalf("byte 0x%X outside the plan changed", off)
			}
		}
		// Every hold whose bit the plan sets is released by the plan's
		// result; every hold that kept a bit back is not.
		for _, h := range class.Holds() {
			b := addr.EventByte(h.ID)
			off := addr.OffEvent + b.Off
			sets := after[off]&b.Mask != 0 && raw[off]&b.Mask == 0
			if sets && !Released(h, &raw, &after) {
				t.Fatalf("0x%04X set while its hold is unmet", h.ID)
			}
			if held[off]&b.Mask != 0 && Released(h, &raw, &after) {
				t.Fatalf("0x%04X held although the plan releases it", h.ID)
			}
		}
		// Planned again on the result, nothing is left but what is held.
		if again := Pending(&view, &after, &ever, &everMax); !again.Empty() {
			t.Fatalf("planned again: %v", again.Writes())
		}
	})
}
