package inject

import (
	"bytes"
	"encoding/binary"
	"slices"
	"sync"
	"testing"
)

// Synthetic GameCube images for the patcher tests. A real GZLE01 image is
// 1.4 GB and copyrighted; these are 6 MiB stand-ins with the same
// structure: boot.bin with the game ID and DOL/FST pointers, bi2.bin, an
// apploader, a DOL big enough to hold every DOLPatches site, an FST with a
// directory and a few files, and file data wherever a test wants it.
// PatchDOL doesn't check the original bytes at the hook sites, so the DOL
// body is an arbitrary pattern.

const (
	testImageSize     = 0x600000
	testDOLOff        = 0x10000
	testApploaderSize = 0x1000
)

type testFile struct {
	name      string
	off, size uint32
	zero      bool // all-zero contents: a zero run that's still file data
}

type testDisc struct {
	size   int
	dolOff uint32
	fstOff uint32 // 0 = right after the DOL, as on a vanilla disc
	fstPad int    // extra string-table bytes, to set the FST's size
	files  []testFile
	junk   bool // fill free space with junk (raw 1:1 dump) instead of zeros
}

// vanillaLike mimics vanilla WW's shape at small scale: the FST right
// after the DOL and bigger than the mod's DOL growth, a wide free gap with
// 0x500000 in it, then files packed toward the end of the image.
func vanillaLike() testDisc {
	return testDisc{
		size:   testImageSize,
		dolOff: testDOLOff,
		fstPad: 0x4000,
		files: []testFile{
			{name: "Stage.arc", off: 0x540000, size: 0x20000},
			{name: "RELS.arc", off: 0x560000, size: 0x31000},
			{name: "empty.txt", off: 0, size: 0},
			{name: "opening.bnr", off: 0x5A0000, size: 0x1960},
			{name: "Audio.aw", off: 0x5B0000, size: 0x4F000},
		},
	}
}

type builtDisc struct {
	img    []byte
	dolOff uint32
	dol    []byte // the vanilla DOL as stored in img
	fstOff uint32
	fst    []byte
	files  []testFile
}

// testDOLSize is the smallest page-aligned DOL that holds every
// DOLPatches site (the furthest sits ~3 MiB in, as in the real DOL).
var testDOLSize = func() uint32 {
	var end uint32
	for _, p := range DOLPatches {
		end = max(end, p.FileOff+uint32(len(p.Bytes)))
	}
	return uint32(alignUp64(uint64(end), 0x1000))
}()

// testDOL is a vanilla-shaped DOL: T0 and T1 text, one data section, BSS,
// so PatchDOL picks text slot 2 for T2 just like on the real game.
var testDOL = sync.OnceValue(func() []byte {
	n := testDOLSize
	d := make([]byte, n)
	tile(d[dolHeaderSize:], pattern(251, 7))
	t1, d0 := uint32(0x2600), n-0x8000
	put32(d, 0x00, dolHeaderSize) // T0: file offset, address, size
	put32(d, 0x48, 0x80003100)
	put32(d, 0x90, t1-dolHeaderSize)
	put32(d, 0x04, t1) // T1
	put32(d, 0x4C, 0x80005600)
	put32(d, 0x94, d0-t1)
	put32(d, 0x1C, d0) // D0
	put32(d, 0x64, 0x80380000)
	put32(d, 0xAC, n-d0)
	put32(d, 0xD8, 0x803A0000) // BSS address, size
	put32(d, 0xDC, 0x60000)
	put32(d, 0xE0, 0x80003154) // entry point
	return d
})

// junk stands in for a raw dump's pseudo-random padding.
var junk = sync.OnceValue(func() []byte {
	b := make([]byte, 0x10000+13) // odd length so it doesn't line up with 64 KiB slots
	x := uint64(0x9E3779B97F4A7C15)
	for i := range b {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		b[i] = byte(x) | 1 // never zero
	}
	return b
})

func (d testDisc) build(t testing.TB) builtDisc {
	t.Helper()
	img := make([]byte, d.size)
	if d.junk {
		tile(img, junk())
	}
	clear(img[:isoApploaderOff+isoApploaderHdrSize+testApploaderSize])

	// boot.bin; bi2.bin stays zero.
	copy(img, isoExpectedGameID)
	put32(img, 0x1C, 0xC2339F3D) // GameCube disc magic
	copy(img[isoHeaderTitle:], "WW MULTIPLAYER SYNTHETIC TEST IMAGE")

	// Apploader header + body.
	copy(img[isoApploaderOff:], "2002/12/19")
	put32(img, isoApploaderOff+0x10, 0x81200000)
	put32(img, isoApploaderOff+0x14, testApploaderSize)
	put32(img, isoApploaderOff+0x18, 0)
	tile(img[isoApploaderOff+isoApploaderHdrSize:][:testApploaderSize], pattern(13, 0x4E))

	dol := testDOL()
	copy(img[d.dolOff:], dol)

	fstOff := d.fstOff
	if fstOff == 0 {
		fstOff = uint32(alignUp64(uint64(d.dolOff)+uint64(len(dol)), 0x100))
	}
	fst := buildFST(d.files, d.fstPad)
	copy(img[fstOff:], fst)
	put32(img, isoHeaderDOLOff, d.dolOff)
	put32(img, isoHeaderFSTOff, fstOff)
	put32(img, isoHeaderFSTSize, uint32(len(fst)))
	put32(img, isoHeaderFSTMax, uint32(len(fst)))

	for i, f := range d.files {
		data := img[f.off : f.off+f.size]
		if f.zero {
			clear(data)
		} else {
			tile(data, pattern(97+i, byte(0xF0+i)))
		}
	}
	return builtDisc{img: img, dolOff: d.dolOff, dol: dol, fstOff: fstOff, fst: fst, files: d.files}
}

// buildFST lays out: root, directory "res" holding the first two files,
// then the remaining files at the root.
func buildFST(files []testFile, pad int) []byte {
	k := min(2, len(files))
	n := 2 + len(files)
	ents := make([]byte, n*fstEntrySize)
	var names []byte
	name := func(s string) uint32 {
		off := uint32(len(names))
		names = append(append(names, s...), 0)
		return off
	}
	entry := func(i int, flags byte, nameOff, w1, w2 uint32) {
		e := ents[i*fstEntrySize:]
		put32(e, 0, uint32(flags)<<24|nameOff)
		put32(e, 4, w1)
		put32(e, 8, w2)
	}
	entry(0, 1, 0, 0, uint32(n))
	entry(1, 1, name("res"), 0, uint32(2+k))
	for i, f := range files {
		entry(2+i, 0, name(f.name), f.off, f.size)
	}
	names = append(names, make([]byte, pad)...)
	return append(ents, names...)
}

// want returns the image the patcher should produce from b when the FST
// ends up at newFST: the input with the patched DOL spliced in and, if the
// FST moved, the FST copied there and the header pointer updated. That's
// byte-for-byte what the pre-hardening patcher wrote for the layouts it
// got right, so out == want proves those outputs didn't change.
func (b builtDisc) want(t testing.TB, newFST uint32) []byte {
	t.Helper()
	patchedDOL, err := PatchDOL(b.dol)
	if err != nil {
		t.Fatalf("PatchDOL(vanilla test DOL): %v", err)
	}
	exp := bytes.Clone(b.img)
	if end := int(newFST) + len(b.fst); end > len(exp) {
		exp = append(exp, make([]byte, end-len(exp))...) // FST appended at EOF
	}
	if newFST != b.fstOff {
		copy(exp[newFST:], b.fst)
		put32(exp, isoHeaderFSTOff, newFST)
	}
	copy(exp[b.dolOff:], patchedDOL)
	return exp
}

// checkPatched asserts out is b correctly patched with the FST at newFST,
// with targeted failures before the whole-image comparison.
func (b builtDisc) checkPatched(t *testing.T, out []byte, newFST uint32) {
	t.Helper()
	// The image grows only when the FST is appended past its end.
	if want := max(len(b.img), int(newFST)+len(b.fst)); len(out) != want {
		t.Fatalf("output is %d bytes, want %d (input %d, FST at 0x%x)", len(out), want, len(b.img), newFST)
	}
	if got := be32(out, isoHeaderFSTOff); got != newFST {
		t.Fatalf("header FST offset = 0x%x, want 0x%x", got, newFST)
	}
	if got := be32(out, isoHeaderFSTSize); got != uint32(len(b.fst)) {
		t.Fatalf("header FST size = 0x%x, want 0x%x", got, len(b.fst))
	}
	fst := out[newFST : int(newFST)+len(b.fst)]
	if !bytes.Equal(fst, b.fst) {
		t.Fatalf("FST at 0x%x differs from the input FST", newFST)
	}
	gotFiles, err := fstFileExtents(fst)
	if err != nil {
		t.Fatalf("output FST doesn't parse: %v", err)
	}
	wantFiles, _ := fstFileExtents(b.fst)
	if !slices.Equal(gotFiles, wantFiles) {
		t.Fatalf("output FST file entries = %v, want %v", gotFiles, wantFiles)
	}
	for _, f := range b.files {
		if !bytes.Equal(out[f.off:f.off+f.size], b.img[f.off:f.off+f.size]) {
			t.Fatalf("file %q [0x%x..0x%x] changed", f.name, f.off, f.off+f.size)
		}
	}

	dol := out[b.dolOff:]
	if off, addr, size := be32(dol, 0x08), be32(dol, 0x50), be32(dol, 0x98); off != uint32(len(b.dol)) ||
		addr != T2Address || size != uint32(len(T2Bytes)) {
		t.Fatalf("text slot 2 = off 0x%x addr 0x%08x size 0x%x, want off 0x%x addr 0x%08x size 0x%x",
			off, addr, size, len(b.dol), T2Address, len(T2Bytes))
	}
	if !bytes.Equal(dol[len(b.dol):len(b.dol)+len(T2Bytes)], T2Bytes) {
		t.Fatal("T2 bytes not appended after the vanilla DOL")
	}
	for _, p := range DOLPatches {
		if !bytes.Equal(dol[p.FileOff:p.FileOff+uint32(len(p.Bytes))], p.Bytes) {
			t.Fatalf("DOL patch at 0x%x not applied", p.FileOff)
		}
	}

	if exp := b.want(t, newFST); !bytes.Equal(out, exp) {
		i := 0
		for i < len(out) && out[i] == exp[i] {
			i++
		}
		t.Fatalf("output differs from expected image, first at disc offset 0x%x", i)
	}
}

func put32(b []byte, off int, v uint32) { binary.BigEndian.PutUint32(b[off:], v) }
func be32(b []byte, off int) uint32     { return binary.BigEndian.Uint32(b[off:]) }

// pattern returns n deterministic non-zero bytes derived from seed.
func pattern(n int, seed byte) []byte {
	p := make([]byte, n)
	for i := range p {
		p[i] = seed + byte(i*31) | 0x01
	}
	return p
}

// tile fills b with repeats of pat.
func tile(b, pat []byte) {
	n := copy(b, pat)
	for n < len(b) {
		n += copy(b[n:], b[:n])
	}
}
