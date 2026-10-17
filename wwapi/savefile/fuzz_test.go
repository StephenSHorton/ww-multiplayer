package savefile

import (
	"bytes"
	"encoding/binary"
	"testing"
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
// Encode then Parse gives the same file, and writing a slot back gives a
// file that verifies and holds the slot's image. The merge of two slots
// is savemerge's FuzzMerge.
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
