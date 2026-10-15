package savefile

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/derive"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
)

// fuzzSeeds are well-formed saves: a fresh GCI, one with a played slot,
// and that file on a 32-block raw card.
func fuzzSeeds(f *testing.F) [][]byte {
	img := randImage(31, "Link")
	one, _ := New().WithSlot(0, &img)
	return [][]byte{New().Encode(), one.Encode(), buildCard(f, cardSpec{blocks: 32, file: one, startBlock: 5})}
}

// checkDecoded is what both fuzz targets ask of b: Decode never panics,
// and what it accepts survives the round trips the save tools make:
// Encode then Parse gives the same file, two of its slots merge, and
// writing a slot back gives a file that verifies and holds the slot's
// image.
func checkDecoded(t *testing.T, b []byte) {
	file, kind, err := Decode(b)
	if err != nil {
		return
	}
	if kind == KindGCI && !bytes.Equal(file.Encode(), b) {
		t.Fatal("Encode is not the GCI Decode read")
	}
	again, err := Parse(file.Encode())
	if err != nil || again.Data != file.Data || again.Header != file.Header {
		t.Fatalf("Parse(Encode): %v", err)
	}
	slots := file.AllSlots()
	for n, s := range slots {
		if p := Pack(&s.Image); p != s.Packed {
			t.Fatalf("slot %d: Pack(Unpack) changed the packed image", n)
		}
	}
	// The merge of two slots, with the table-free derives, never fails
	// and is idempotent.
	opt := MergeOptions{Policy: world.FullPolicy(), Derive: Derive(nil, nil)}
	m, err := Merge(&slots[0].Image, &slots[1].Image, opt)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if again, err := Merge(&m.Image, &slots[1].Image, opt); err != nil || again.Image != m.Image {
		t.Fatalf("Merge again: %v", err)
	}
	// With key and heart tables too: idempotent, small keys at the live
	// derive's target wherever the merge moved a source or door, and max
	// life never below a's.
	kr := keyTable(true)
	opt.Derive = Derive(kr, heartTable(true))
	if m, err = Merge(&slots[0].Image, &slots[1].Image, opt); err != nil {
		t.Fatalf("Merge with tables: %v", err)
	}
	if again, err := Merge(&m.Image, &slots[1].Image, opt); err != nil || again.Image != m.Image {
		t.Fatalf("Merge with tables again: %v", err)
	}
	src, doors := keyCount(&m.Image, &kr.Tables[0])
	src0, doors0 := keyCount(&slots[0].Image, &kr.Tables[0])
	keys := m.Image[addr.OffMemBit(3)+addr.MemBitOffKeyNum]
	if want := derive.KeyTarget(derive.KeyFacts{Taken: src, Open: doors}, 0); (src != src0 || doors != doors0) && int(keys) != want {
		t.Fatalf("keys %d, the live derive's target %d", keys, want)
	}
	if be16(m.Image[addr.OffMaxLife:]) < be16(slots[0].Image[addr.OffMaxLife:]) {
		t.Fatal("the merge lowered max life")
	}
	if file.Header.CheckStat() != nil {
		return
	}
	for n, s := range slots {
		out, err := file.WithSlot(n, &s.Image)
		if err != nil {
			t.Fatalf("WithSlot(%d): %v", n, err)
		}
		if err := out.Verify(n); err != nil {
			t.Fatalf("WithSlot(%d) then Verify: %v", n, err)
		}
		got, _ := out.Slot(n)
		if got.Image != s.Image || got.State == Corrupt {
			t.Fatalf("slot %d: written image not read back (%s)", n, got.State)
		}
	}
}

// FuzzDecode feeds Decode arbitrary bytes: GCI files, raw cards and
// anything else (checkDecoded).
func FuzzDecode(f *testing.F) {
	seeds := fuzzSeeds(f)
	for _, s := range seeds {
		f.Add(s)
	}
	f.Add(seeds[1][:GCISize-1])
	f.Add(seeds[1][:HeaderSize])
	f.Add(make([]byte, 8*BlockSize))
	f.Fuzz(checkDecoded)
}

// FuzzPatched overwrites part of a well-formed save with patch, at offset
// at, and checks the result (checkDecoded). The input stays small, so the
// fuzzer mutates and minimizes it quickly, where a whole GCI (0x18040
// bytes) makes FuzzDecode's minimizing slow. With fix set it first
// recomputes the checksums the patch broke, so the slot and copy
// checksums hold and the deeper paths run.
func FuzzPatched(f *testing.F) {
	seeds := fuzzSeeds(f)
	f.Add(uint8(1), uint32(HeaderSize+BlockSize+8+0x150), []byte("Tetra"), true)
	f.Add(uint8(1), uint32(HeaderSize+2*BlockSize+8), []byte{0xFF, 0xFF}, false)
	f.Add(uint8(0), uint32(0x2C), []byte{0, 0, 0, 0x40}, true)
	f.Add(uint8(2), uint32(3*BlockSize+12), []byte{0x00, 0x09}, false)
	f.Fuzz(func(t *testing.T, which uint8, at uint32, patch []byte, fix bool) {
		b := bytes.Clone(seeds[int(which)%len(seeds)])
		copy(b[int(at%uint32(len(b))):], patch)
		if fix && len(b) == GCISize {
			for blk := blockSave; blk <= blockSave2; blk++ {
				sd := b[HeaderSize+blk*BlockSize : HeaderSize+(blk+1)*BlockSize]
				for n := range Slots {
					gd := gameData(sd, n)
					binary.BigEndian.PutUint64(gd[gdOffSum:], GameDataSum(gd[:PackedSize]))
				}
				binary.BigEndian.PutUint32(sd[sdOffSum:], SaveDataSum(sd[:sdOffSum]))
			}
		}
		checkDecoded(t, b)
	})
}
