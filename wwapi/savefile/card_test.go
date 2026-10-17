package savefile

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

// cardSpec is a synthetic raw card: blocks in all, file's blocks at chain
// (default: startBlock on, one after another), another game's file in
// directory entry 0 before ours, and the two directory and block-table
// copies with check codes codes (the second copy of each gets dirCode[1]
// or fatCode[1]).
type cardSpec struct {
	blocks     int
	file       *File
	startBlock int
	chain      []int
	dirCode    [2]int16
	fatCode    [2]int16
	// stale replaces the copy with the lower check code by one holding
	// no file at all, so a reader that picks the wrong copy fails.
	stale bool
}

// putSums stores __CARDCheckSum of over at b[at:].
func putSums(b []byte, at int, over []byte) {
	sum, inv := cardCheckSum(over)
	binary.BigEndian.PutUint16(b[at:], sum)
	binary.BigEndian.PutUint16(b[at+2:], inv)
}

// buildCard lays a card out as CARDFormat and CARDCreate would
// (CARDFormat.c:63-101, CARDCreate.c:24-40, CARDBlock.c:94-116).
func buildCard(t testing.TB, s cardSpec) []byte {
	t.Helper()
	b := make([]byte, s.blocks*BlockSize)
	block := func(i int) []byte { return b[i*BlockSize : (i+1)*BlockSize] }

	id := block(0)
	for i := range 12 {
		id[i] = byte(0x30 + i) // a serial: unchecked offline
	}
	binary.BigEndian.PutUint16(id[idOffSize:], uint16(s.blocks/cardBlocksPerMb))
	putSums(id, idOffSum, id[:idOffSum])

	chain := s.chain
	if chain == nil {
		for k := range FileBlocks {
			chain = append(chain, s.startBlock+k)
		}
	}
	fat := make([]byte, BlockSize)
	used := map[int]bool{}
	for k, blk := range chain {
		next := fatLast
		if k+1 < len(chain) {
			next = chain[k+1]
		}
		binary.BigEndian.PutUint16(fat[2*blk:], uint16(next))
		used[blk] = true
		copy(block(blk), s.file.block(k))
	}
	// Another game's one-block file just before ours.
	other := 5
	for used[other] {
		other++
	}
	binary.BigEndian.PutUint16(fat[2*other:], fatLast)
	used[other] = true
	free := 0
	for n := cardSystemBlocks; n < s.blocks; n++ {
		if !used[n] {
			free++
		}
	}
	binary.BigEndian.PutUint16(fat[fatOffFree:], uint16(free))

	dir := bytes.Repeat([]byte{0xFF}, BlockSize)
	var o Header
	copy(o[:], "GALE01")
	copy(o[hdrOffFileName:], "SuperSmashBros0110290334")
	binary.BigEndian.PutUint16(o[hdrOffLength:], 1)
	binary.BigEndian.PutUint16(o[entryOffStartBlock:], uint16(other))
	copy(dir, o[:])
	h := s.file.Header
	binary.BigEndian.PutUint16(h[entryOffStartBlock:], uint16(chain[0]))
	copy(dir[HeaderSize:], h[:])

	emptyDir := bytes.Repeat([]byte{0xFF}, BlockSize)
	for i, code := range s.dirCode {
		d := dir
		if s.stale && code < s.dirCode[1-i] {
			d = bytes.Clone(emptyDir)
		}
		blk := block(1 + i)
		copy(blk, d)
		binary.BigEndian.PutUint16(blk[dirOffCheckCode:], uint16(code))
		putSums(blk, dirOffSum, blk[:dirOffSum])
	}
	for i, code := range s.fatCode {
		blk := block(3 + i)
		copy(blk, fat)
		if s.stale && code < s.fatCode[1-i] {
			clear(blk)
			binary.BigEndian.PutUint16(blk[fatOffFree:], uint16(s.blocks-cardSystemBlocks))
		}
		binary.BigEndian.PutUint16(blk[fatOffCheckCode:], uint16(code))
		putSums(blk, 0, blk[fatOffCheckCode:])
	}
	return b
}

func TestParseCard(t *testing.T) {
	img := randImage(21, "Link")
	f, err := New().WithSlot(1, &img)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []cardSpec{
		{blocks: 64, file: f, startBlock: 6},
		{blocks: 64, file: f, chain: []int{40, 7, 8, 30, 31, 9, 63, 10, 11, 12, 13, 14}},
		{blocks: 2048, file: f, startBlock: 2000},
		{blocks: 64, file: f, startBlock: 6, dirCode: [2]int16{3, 4}, fatCode: [2]int16{9, 2}, stale: true},
		{blocks: 64, file: f, startBlock: 6, dirCode: [2]int16{5, 1}, fatCode: [2]int16{-1, 7}, stale: true},
	} {
		g, err := ParseCard(buildCard(t, s))
		if err != nil {
			t.Errorf("%+v: %v", s.chain, err)
			continue
		}
		if g.Data != f.Data {
			t.Errorf("chain %v start %d: data differs", s.chain, s.startBlock)
		}
		if g.Header.FileName() != FileName || g.Header.Length() != FileBlocks {
			t.Errorf("header %q %d", g.Header.FileName(), g.Header.Length())
		}
		if sl, _ := g.Slot(1); sl.Image != img {
			t.Error("slot 1 lost")
		}
	}
}

func TestParseCardRefuses(t *testing.T) {
	img := randImage(22, "Link")
	f, _ := New().WithSlot(0, &img)
	good := cardSpec{blocks: 64, file: f, startBlock: 6}
	resum := func(b []byte, blk, at, from, to int) {
		p := b[blk*BlockSize : (blk+1)*BlockSize]
		putSums(p, at, p[from:to])
	}
	for _, c := range []struct {
		name string
		mk   func() []byte
		want string
	}{
		{"not blocks", func() []byte { return make([]byte, 8*BlockSize+1) }, "whole number"},
		{"too small", func() []byte { return make([]byte, 7*BlockSize) }, "whole number"},
		{"device", func() []byte {
			b := buildCard(t, good)
			binary.BigEndian.PutUint16(b[idOffDeviceID:], 1)
			return b
		}, "device 1"},
		{"ID checksum", func() []byte { b := buildCard(t, good); b[0x100] ^= 1; return b }, "card ID checksum"},
		{"size", func() []byte {
			b := buildCard(t, good)
			binary.BigEndian.PutUint16(b[idOffSize:], 8)
			resum(b, 0, idOffSum, 0, idOffSum)
			return b
		}, "128 blocks"},
		{"directories", func() []byte {
			b := buildCard(t, good)
			b[BlockSize+5] ^= 1
			b[2*BlockSize+5] ^= 1
			return b
		}, "directory fail"},
		{"block tables", func() []byte {
			b := buildCard(t, good)
			b[3*BlockSize+100] ^= 1
			b[4*BlockSize+100] ^= 1
			return b
		}, "block table fail"},
		{"free count", func() []byte {
			b := buildCard(t, good)
			for _, blk := range []int{3, 4} {
				p := b[blk*BlockSize:]
				binary.BigEndian.PutUint16(p[fatOffFree:], 1)
				resum(b, blk, 0, fatOffCheckCode, BlockSize)
			}
			return b
		}, "block table fail"},
		{"no file", func() []byte {
			s := good
			g := f.Clone()
			copy(g.Header[hdrOffGameName:], "GZLP")
			s.file = g
			return buildCard(t, s)
		}, "holds no gczelda"},
		{"length", func() []byte {
			s := good
			g := f.Clone()
			binary.BigEndian.PutUint16(g.Header[hdrOffLength:], 3)
			s.file = g
			return buildCard(t, s)
		}, "3 blocks"},
		{"chain into system blocks", func() []byte {
			s := good
			s.chain = []int{6, 7, 8, 9, 2, 10, 11, 12, 13, 14, 15, 16}
			return buildCard(t, s)
		}, "broken at its block 4"},
		{"chain loops", func() []byte {
			b := buildCard(t, good)
			for _, blk := range []int{3, 4} {
				binary.BigEndian.PutUint16(b[blk*BlockSize+2*10:], 7)
				resum(b, blk, 0, fatOffCheckCode, BlockSize)
			}
			return b
		}, "broken at its block 5"},
		{"chain past the end", func() []byte {
			b := buildCard(t, good)
			for _, blk := range []int{3, 4} {
				binary.BigEndian.PutUint16(b[blk*BlockSize+2*17:], 40)
				resum(b, blk, 0, fatOffCheckCode, BlockSize)
			}
			return b
		}, "runs past"},
		{"chain off the card", func() []byte {
			b := buildCard(t, good)
			for _, blk := range []int{3, 4} {
				binary.BigEndian.PutUint16(b[blk*BlockSize+2*6:], 64)
				resum(b, blk, 0, fatOffCheckCode, BlockSize)
			}
			return b
		}, "broken at its block 1"},
	} {
		_, err := ParseCard(c.mk())
		if !errors.Is(err, ErrFormat) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want ErrFormat containing %q", c.name, err, c.want)
		}
	}
	// One bad copy of each is survivable.
	b := buildCard(t, good)
	b[BlockSize+5] ^= 1
	b[4*BlockSize+100] ^= 1
	if _, err := ParseCard(b); err != nil {
		t.Errorf("one bad copy each: %v", err)
	}
}
