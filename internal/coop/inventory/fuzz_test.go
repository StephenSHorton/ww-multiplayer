package inventory

import (
	"encoding/binary"
	"reflect"
	"slices"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/snap"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// fuzzOffsets are the image bytes a plan reads: the flags, capacities,
// slots, equipment, button bindings, ammo and the FF1 event byte. The
// fuzzer fills them from its input; every other byte stays as fresh()
// leaves it.
var fuzzOffsets = func() []int {
	var offs []int
	for _, f := range flagBytes {
		offs = append(offs, f.off)
	}
	for _, c := range capacities {
		offs = append(offs, c.off)
	}
	for _, s := range append(ItemSlots(), EquipSlots()...) {
		offs = append(offs, s.Off)
	}
	for i := range Buttons {
		offs = append(offs, addr.OffSelectItem+i)
	}
	return append(offs, OffArrowNum, OffBombNum, offEv0801, addr.OffItems+addr.SlotBottle0)
}()

// fuzzImage fills fresh()'s fuzzOffsets from b, a byte each; byte values
// are folded so slots, flags and capacities land on interesting values
// often.
func fuzzImage(b []byte) world.Image {
	img := fresh()
	for i, off := range fuzzOffsets {
		if i < len(b) {
			img[off] = b[i]
		}
	}
	return img
}

// interesting are the item values that matter for a slot-like byte.
var interesting = func() []byte {
	vs := []byte{ItemNone, 0x00, 0x50}
	for _, s := range append(ItemSlots(), EquipSlots()...) {
		for _, v := range s.Variants {
			vs = append(vs, v.Item)
		}
	}
	return vs
}()

// FuzzPending checks the plan's invariants on arbitrary images and
// contexts: it is deterministic; every write is to a byte the inventory
// owns (or a button or an ammo counter), reads what the plan read and
// never clears a bit, lowers a capacity or moves a slot down; no byte is
// written twice; no write a holding rule covers is made; and once the
// writes land, the plan on the result writes nothing and holds the same.
func FuzzPending(f *testing.F) {
	bow := with(fresh(), offBowFlags, 0x07, addr.OffArrowMax, 99)
	swords := with(fresh(), offSwordBits, 0x0F, addr.OffCollectFlags+1, 3)
	empty, ff1 := fresh(), with(fresh(), offEv0801, 1)
	f.Add(bow[:], empty[:], "sea", byte(7), true, []byte{0xFF, 0xFF, 0xFF}, int16(0), int16(0))
	f.Add(bow[:], ff1[:], "GTower", byte(5), true, []byte{0x27, 0x35, 0xFF}, int16(-1), int16(5))
	f.Add(swords[:], ff1[:], "MajyuE", byte(5), false, []byte{0, 1, 2}, int16(0), int16(0))
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF}, []byte{0x01, 0x02, 0x03}, "", byte(0), false, []byte{}, int16(1), int16(0))
	f.Fuzz(func(t *testing.T, vb, rb []byte, stage string, typ byte, typOK bool, sel []byte, arrows, bombs int16) {
		view, raw := fuzzImage(pick(vb)), fuzzImage(pick(rb))
		if len(vb) == world.ImageSize {
			copy(view[:], vb)
		}
		if len(rb) == world.ImageSize {
			copy(raw[:], rb)
		}
		ctx := Context{StageName: stage, StageType: int(typ & 7), StageTypeOK: typOK, ArrowCount: arrows, BombCount: bombs}
		for i := range Buttons {
			ctx.SelectItem[i] = ItemNone
			if i < len(sel) {
				ctx.SelectItem[i] = interesting[int(sel[i])%len(interesting)]
			}
		}
		p := Pending(&view, &raw, ctx)
		if q := Pending(&view, &raw, ctx); !reflect.DeepEqual(p, q) {
			t.Fatalf("not deterministic:\n%+v\n%+v", p, q)
		}
		checkPlan(t, &view, &raw, ctx, p)
		checkIdempotent(t, &view, &raw, ctx, p)

		img, _ := Apply(&raw, ctx, append(slices.Clone(p.Writes), p.Ammo...))
		for _, f := range flagBytes {
			if img[f.off]&raw[f.off] != raw[f.off] {
				t.Errorf("%s lost bits: 0x%02X -> 0x%02X", f.name, raw[f.off], img[f.off])
			}
		}
		for _, c := range capacities {
			if img[c.off] < raw[c.off] {
				t.Errorf("%s lowered: %d -> %d", c.name, raw[c.off], img[c.off])
			}
		}
	})
}

// pick folds each input byte onto the values a plan cares about, so short
// inputs reach variants and flag patterns: an even byte is kept, an odd
// one picks an interesting item.
func pick(b []byte) []byte {
	out := make([]byte, len(b))
	for i, v := range b {
		out[i] = v
		if v&1 == 1 {
			out[i] = interesting[int(v>>1)%len(interesting)]
		}
	}
	return out
}

// checkPlan checks one plan's writes against the images it was made from.
func checkPlan(t *testing.T, view, raw *world.Image, ctx Context, p Plan) {
	t.Helper()
	seen := map[uint32]bool{}
	ff1, gt := ctx.ff1(raw), ctx.gtower()
	all := append(slices.Clone(p.Writes), p.Ammo...)
	for _, h := range p.Held {
		all = append(all, h.Write)
	}
	for i, w := range all {
		made := i < len(p.Writes)+len(p.Ammo)
		if made && seen[w.Addr] {
			t.Errorf("0x%08X written twice", w.Addr)
		}
		seen[w.Addr] = true
		if w.Reason == "" {
			t.Errorf("%s: no reason", short(w))
		}
		if w.Off >= 0 && (w.Addr != addr.SaveImage+uint32(w.Off) || w.Old != raw[w.Off]) {
			t.Errorf("%s: Addr 0x%08X Old 0x%02X, image reads 0x%02X", short(w), w.Addr, w.Old, raw[w.Off])
		}
		switch w.Kind {
		case KindFlag:
			m := FlagMask(w.Off)
			if w.Op != OpOR || w.Val&w.Old != w.Old || w.Bits() == 0 || w.Bits()&^(m&view[w.Off]) != 0 {
				t.Errorf("%s: flag write outside the world's bits 0x%02X mask 0x%02X", short(w), view[w.Off], m)
			}
		case KindCapacity:
			c := slices.IndexFunc(capacities, func(c capacity) bool { return c.off == w.Off })
			if c < 0 || w.Op != OpMax || w.Val <= w.Old || w.Val > max(capacities[c].most, 30) {
				t.Errorf("%s: capacity write", short(w))
			}
		case KindSlot, KindEquip:
			slots := itemSlots
			if w.Kind == KindEquip {
				slots = equipSlots
			}
			i := slices.IndexFunc(slots, func(s Slot) bool { return s.Off == w.Off })
			if i < 0 || w.Op != OpSet {
				t.Fatalf("%s: not a derived byte", short(w))
			}
			s := slots[i]
			if s.Rank(w.Val) < 0 || w.Old != ItemNone && s.Rank(w.Old) >= s.Rank(w.Val) {
				t.Errorf("%s: %s 0x%02X -> 0x%02X is no upgrade", short(w), s.Name, w.Old, w.Val)
			}
			if w.Kind == KindEquip && w.When != AtStageEntry || w.Kind == KindSlot && w.When != Live {
				t.Errorf("%s: timing %v", short(w), w.When)
			}
		case KindSelectItem:
			btn := int(w.Addr - addr.PlaySelectItem)
			if btn < 0 || btn >= Buttons || w.Off != -1 || w.Op != OpSet || w.Old != ctx.SelectItem[btn] || w.Val == ItemNone {
				t.Errorf("%s: button write", short(w))
			}
		case KindAmmo:
			if (w.Addr != ItemArrowNumCount+1 || ctx.ArrowCount != 0) && (w.Addr != ItemBombNumCount+1 || ctx.BombCount != 0) ||
				w.Op != OpSet || w.Old != 0 || w.Val == 0 || w.Val > 30 {
				t.Errorf("%s: ammo write", short(w))
			}
		default:
			t.Errorf("%s: unknown kind", short(w))
		}
		if !made {
			continue
		}
		// Nothing a holding rule covers is made.
		bowByte := w.Off == offBowFlags || w.Off == offBowSlot ||
			w.Kind == KindSelectItem && raw[addr.OffSelectItem+int(w.Addr-addr.PlaySelectItem)] == SlotBow
		if gt && bowByte {
			t.Errorf("%s made in GTower", short(w))
		}
		if ff1 && (w.Off == offSwordBits || w.Off == offSword) {
			t.Errorf("%s made in FF1 with 0x0801", short(w))
		}
		if gt && w.Addr == ItemArrowNumCount+1 {
			t.Errorf("%s: arrows filled in GTower", short(w))
		}
	}
}

// FuzzContextOf decodes arbitrary player blocks: the buttons and counters
// come back as the bytes say.
func FuzzContextOf(f *testing.F) {
	f.Add([]byte{0x27, 0x35, 0xFF, 0x00, 0xFF, 0xFF, 0x00, 0x1E}, "GTower", 5, true)
	f.Fuzz(func(t *testing.T, b []byte, stage string, typ int, ok bool) {
		var s snap.Snapshot
		s.StageName = stage
		at := func(a uint32) int { return int(a - snap.PlayerRegion.Start) }
		for i, v := range b {
			switch {
			case i < Buttons:
				s.Player[at(addr.PlaySelectItem)+i] = v
			case i < Buttons+2:
				s.Player[at(ItemArrowNumCount)+i-Buttons] = v
			case i < Buttons+4:
				s.Player[at(ItemBombNumCount)+i-Buttons-2] = v
			}
		}
		c := ContextOf(&s, typ, ok)
		if c.StageName != stage || c.StageTypeOK != ok || c.StageType < 0 || c.StageType > 7 {
			t.Errorf("%+v", c)
		}
		if !slices.Equal(c.SelectItem[:], s.Player[at(addr.PlaySelectItem):at(addr.PlaySelectItem)+Buttons]) {
			t.Errorf("buttons %v", c.SelectItem)
		}
		if c.ArrowCount != int16(binary.BigEndian.Uint16(s.Player[at(ItemArrowNumCount):])) ||
			c.BombCount != int16(binary.BigEndian.Uint16(s.Player[at(ItemBombNumCount):])) {
			t.Errorf("counters %d %d", c.ArrowCount, c.BombCount)
		}
	})
}
