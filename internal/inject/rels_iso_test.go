package inject

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// testRELSArc stands in for a patched RELS.arc of n bytes: the RARC
// magic and a pattern that no file of the synthetic discs uses.
func testRELSArc(n int) []byte {
	b := make([]byte, n)
	tile(b, pattern(89, 0x3C))
	copy(b, "RARC")
	return b
}

// relsEntry returns the index and entry of RELS.arc in fst.
func relsEntry(t testing.TB, fst []byte) (uint64, fstEntry) {
	t.Helper()
	idx, e, err := fstRootFile(fst, relsArcName)
	if err != nil {
		t.Fatal(err)
	}
	return idx, e
}

// wantWithRELS is b.want with the replacement archive arc at relsOff and
// the FST's RELS.arc entry pointed at it: the whole output, byte for
// byte.
func (b builtDisc) wantWithRELS(t testing.TB, newFST uint32, arc []byte, relsOff uint32) []byte {
	t.Helper()
	exp := b.want(t, newFST)
	if end := int(relsOff) + len(arc); end > len(exp) {
		exp = append(exp, make([]byte, end-len(exp))...)
	}
	idx, _ := relsEntry(t, b.fst)
	put32(exp, int(newFST)+int(idx)*fstEntrySize+4, relsOff)
	put32(exp, int(newFST)+int(idx)*fstEntrySize+8, uint32(len(arc)))
	copy(exp[relsOff:], arc)
	return exp
}

// checkPatchedWithRELS asserts out is b patched with the FST at newFST
// and arc as RELS.arc at relsOff: the FST entry points there, the bytes
// are there, every other file (and the vanilla RELS.arc's bytes) are
// untouched, nothing overlaps, and the image is byte for byte the
// expected one.
func (b builtDisc) checkPatchedWithRELS(t *testing.T, out []byte, res Result, newFST uint32, arc []byte, relsOff uint32) {
	t.Helper()
	if res.NewFSTOffset != newFST || res.RELSArcOffset != relsOff || res.RELSArcSize != uint32(len(arc)) || !res.RELSArcReplaced() {
		t.Fatalf("result %+v, want FST at 0x%x and RELS.arc at 0x%x (%d bytes)", res, newFST, relsOff, len(arc))
	}
	if got := be32(out, isoHeaderFSTOff); got != newFST {
		t.Fatalf("header FST offset = 0x%x, want 0x%x", got, newFST)
	}
	fst := out[newFST : int(newFST)+len(b.fst)]
	idx, e := relsEntry(t, fst)
	if e.w1 != relsOff || e.w2 != uint32(len(arc)) {
		t.Fatalf("RELS.arc entry = off 0x%x size 0x%x, want off 0x%x size 0x%x", e.w1, e.w2, relsOff, len(arc))
	}
	if !bytes.Equal(out[relsOff:int(relsOff)+len(arc)], arc) {
		t.Fatal("the replacement RELS.arc isn't at its offset")
	}
	// The rest of the FST is the input's.
	origIdx, _ := relsEntry(t, b.fst)
	if origIdx != idx {
		t.Fatalf("RELS.arc moved to entry %d from %d", idx, origIdx)
	}
	masked := bytes.Clone(fst)
	copy(masked[idx*fstEntrySize+4:], b.fst[idx*fstEntrySize+4:idx*fstEntrySize+12])
	if !bytes.Equal(masked, b.fst) {
		t.Fatal("the FST changed beyond the RELS.arc entry")
	}
	// Every file's bytes, the vanilla archive's included, and no overlap
	// between the new archive and any file, the FST or the DOL.
	rels := extent{start: uint64(relsOff), end: uint64(relsOff) + uint64(len(arc))}
	for _, f := range b.files {
		if f.size == 0 {
			continue
		}
		if asciiEqualFold(f.name, relsArcName) {
			if f.off == relsOff {
				continue // the replacement took the vanilla extent
			}
			if !bytes.Equal(out[f.off:f.off+f.size], b.img[f.off:f.off+f.size]) {
				t.Fatalf("the vanilla RELS.arc [0x%x..0x%x] changed", f.off, f.off+f.size)
			}
		}
		if !bytes.Equal(out[f.off:f.off+f.size], b.img[f.off:f.off+f.size]) {
			t.Fatalf("file %q [0x%x..0x%x] changed", f.name, f.off, f.off+f.size)
		}
		if rels.overlaps(extent{start: uint64(f.off), end: uint64(f.off) + uint64(f.size)}) {
			t.Fatalf("RELS.arc at 0x%x overlaps file %q [0x%x..0x%x]", relsOff, f.name, f.off, f.off+f.size)
		}
	}
	dolEnd := uint64(b.dolOff) + uint64(len(b.dol)) + uint64(len(T2Bytes))
	if rels.overlaps(extent{start: uint64(b.dolOff), end: dolEnd}) {
		t.Fatalf("RELS.arc at 0x%x overlaps the patched DOL [0x%x..0x%x]", relsOff, b.dolOff, dolEnd)
	}
	if rels.overlaps(extent{start: uint64(newFST), end: uint64(newFST) + uint64(len(b.fst))}) {
		t.Fatalf("RELS.arc at 0x%x overlaps the FST at 0x%x", relsOff, newFST)
	}
	if rels.end > gcDiscSize {
		t.Fatalf("RELS.arc ends at 0x%x, past the disc-size cap", rels.end)
	}
	if exp := b.wantWithRELS(t, newFST, arc, relsOff); !bytes.Equal(out, exp) {
		i := 0
		for i < len(out) && i < len(exp) && out[i] == exp[i] {
			i++
		}
		t.Fatalf("output (%d bytes) differs from the expected image (%d), first at 0x%x", len(out), len(exp), i)
	}
}

// Without a replacement archive, PatchISOInMemoryWith is PatchISOInMemory.
func TestPatchISOWithNoRELSIsUnchanged(t *testing.T) {
	for _, d := range []testDisc{vanillaLike(), func() testDisc { d := vanillaLike(); d.fstOff = wwSafeFSTOff; return d }()} {
		b := d.build(t)
		plain, err := PatchISOInMemory(b.img)
		if err != nil {
			t.Fatal(err)
		}
		out, res, err := PatchISOInMemoryWith(b.img, Patch{})
		if err != nil || !bytes.Equal(out, plain) {
			t.Fatalf("PatchISOInMemoryWith(Patch{}): %v, output differs", err)
		}
		if res.RELSArcReplaced() || res.RELSArcOffset != 0 || res.RELSArcSize != 0 {
			t.Errorf("result %+v reports a RELS.arc", res)
		}
	}
}

// The vanilla shape: the FST moves to 0x500000 and the bigger RELS.arc
// goes in the lowest free 0x8000-aligned slot past the patched DOL, the
// FST's entry pointed at it, the vanilla archive's bytes left alone.
func TestPatchISORELSArcRelocated(t *testing.T) {
	b := vanillaLike().build(t)
	arc := testRELSArc(0x40000) // bigger than the 0x31000-byte vanilla entry
	out, res, err := PatchISOInMemoryWith(b.img, Patch{RELSArc: arc})
	if err != nil {
		t.Fatal(err)
	}
	dolEnd := uint64(b.dolOff) + uint64(len(b.dol)) + uint64(len(T2Bytes))
	want := uint32(alignUp64(dolEnd, 0x8000))
	b.checkPatchedWithRELS(t, out, res, wwSafeFSTOff, arc, want)
	if !res.FSTMoved() {
		t.Error("FSTMoved() = false")
	}

	// Idempotency: the output holds this build's DOL and RELS.arc.
	if _, _, err := PatchISOInMemoryWith(out, Patch{RELSArc: arc}); !errors.Is(err, ErrAlreadyPatched) {
		t.Errorf("re-patching the output: err = %v, want ErrAlreadyPatched", err)
	}
	// Without an archive, the DOL alone decides, as it always has.
	if _, err := PatchISOInMemory(out); !errors.Is(err, ErrAlreadyPatched) {
		t.Errorf("re-patching the output without an archive: err = %v, want ErrAlreadyPatched", err)
	}
	// Another build's archive (one byte differs, or another size) is
	// another version, even with this build's DOL.
	other := bytes.Clone(arc)
	other[0x1000] ^= 0xFF
	if _, _, err := PatchISOInMemoryWith(out, Patch{RELSArc: other}); !errors.Is(err, ErrPatchedByOtherVersion) || !strings.Contains(err.Error(), "RELS.arc") {
		t.Errorf("re-patching with another archive: err = %v, want ErrPatchedByOtherVersion naming RELS.arc", err)
	}
	if _, _, err := PatchISOInMemoryWith(out, Patch{RELSArc: arc[:len(arc)-1]}); !errors.Is(err, ErrPatchedByOtherVersion) {
		t.Errorf("re-patching with a shorter archive: err = %v, want ErrPatchedByOtherVersion", err)
	}
	// A vanilla disc whose RELS.arc happens to be the replacement is
	// patched (the DOL isn't), the archive placed again.
	vanilla := bytes.Clone(b.img)
	put32(vanilla, int(b.fstOff)+int(func() uint64 { i, _ := relsEntry(t, b.fst); return i }())*fstEntrySize+8, uint32(len(arc)))
	if _, _, err := PatchISOInMemoryWith(vanilla, Patch{RELSArc: arc}); err != nil {
		t.Errorf("a vanilla DOL with a current-sized RELS.arc: %v", err)
	}
	// The input wasn't touched.
	if !bytes.Equal(b.img, vanillaLike().build(t).img) {
		t.Error("the input image changed")
	}
}

// The owner's dev image (FST already at 0x500000): the FST stays, its
// RELS.arc entry is rewritten in place, and the archive goes in the gap
// past the DOL, below the FST.
func TestPatchISORELSArcFastPath(t *testing.T) {
	d := vanillaLike()
	d.fstOff = wwSafeFSTOff
	b := d.build(t)
	arc := testRELSArc(0x40000)
	out, res, err := PatchISOInMemoryWith(b.img, Patch{RELSArc: arc})
	if err != nil {
		t.Fatal(err)
	}
	dolEnd := uint64(b.dolOff) + uint64(len(b.dol)) + uint64(len(T2Bytes))
	b.checkPatchedWithRELS(t, out, res, wwSafeFSTOff, arc, uint32(alignUp64(dolEnd, 0x8000)))
	if res.FSTMoved() || len(out) != len(b.img) {
		t.Errorf("FSTMoved() = %v, output %d bytes (input %d)", res.FSTMoved(), len(out), len(b.img))
	}
	if _, _, err := PatchISOInMemoryWith(out, Patch{RELSArc: arc}); !errors.Is(err, ErrAlreadyPatched) {
		t.Errorf("re-patching the output: err = %v, want ErrAlreadyPatched", err)
	}
}

// An archive no bigger than the vanilla entry stays where the vanilla
// one is, with the entry's size updated.
func TestPatchISORELSArcFitsInPlace(t *testing.T) {
	b := vanillaLike().build(t)
	for _, n := range []int{0x1000, 0x31000} {
		arc := testRELSArc(n)
		out, res, err := PatchISOInMemoryWith(b.img, Patch{RELSArc: arc})
		if err != nil {
			t.Fatalf("%d bytes: %v", n, err)
		}
		b.checkPatchedWithRELS(t, out, res, wwSafeFSTOff, arc, 0x560000)
		if n < 0x31000 && !bytes.Equal(out[0x560000+n:0x560000+0x31000], b.img[0x560000+n:0x560000+0x31000]) {
			t.Errorf("%d bytes: the rest of the vanilla extent changed", n)
		}
	}
}

// With every 0x8000-aligned gap too small, a 0x20-aligned one is used
// before appending past the end of a trimmed image.
func TestPatchISORELSArcFallsBackTo0x20(t *testing.T) {
	d := vanillaLike()
	d.fstOff = wwSafeFSTOff
	const size = 0x40000 // bigger than the vanilla entry, so it can't stay in place
	dolEnd := uint32(alignUp64(uint64(d.dolOff)+uint64(testDOLSize)+uint64(len(T2Bytes)), 0x20))
	const gap = 0x4A0020 // 0x20-aligned, not 0x8000-aligned
	d.files = append(d.files,
		testFile{name: "fill1.bin", off: dolEnd, size: gap - dolEnd},
		testFile{name: "fill2.bin", off: gap + size + 0x20, size: wwSafeFSTOff - (gap + size + 0x20)},
		testFile{name: "fill3.bin"}, // from the FST's end once its size is known
	)
	fstSize := uint32(len(buildFST(d.files, d.fstPad)))
	fstEnd := uint32(alignUp64(uint64(wwSafeFSTOff+fstSize), 0x20))
	d.files[len(d.files)-1] = testFile{name: "fill3.bin", off: fstEnd, size: 0x540000 - fstEnd}
	b := d.build(t)
	// The remaining gaps (between the vanillaLike files) are under
	// 0x8000 past any 0x8000 boundary; the image is smaller than a disc,
	// so an EOF append would be the alternative.
	arc := testRELSArc(size)
	out, res, err := PatchISOInMemoryWith(b.img, Patch{RELSArc: arc})
	if err != nil {
		t.Fatal(err)
	}
	b.checkPatchedWithRELS(t, out, res, wwSafeFSTOff, arc, gap)
	if len(out) != len(b.img) {
		t.Errorf("output %d bytes, input %d: appended instead of using the gap", len(out), len(b.img))
	}
}

// A trimmed image with no gap at all: the archive is appended past the
// end, as the FST is.
func TestPatchISORELSArcAppendsAtEOF(t *testing.T) {
	d := vanillaLike()
	d.junk = true
	fstEnd := uint32(alignUp64(uint64(d.dolOff)+uint64(testDOLSize), 0x100)) + 0x5000
	d.files = []testFile{
		{name: "a.arc", off: fstEnd, size: 0x400000 - fstEnd},
		{name: "c.arc", off: 0x523456, size: testImageSize - 0x523456},
		{name: "RELS.arc", off: 0x400000, size: 0x123456}, // third: in the root
	}
	b := d.build(t)
	arc := testRELSArc(0x130000)
	out, res, err := PatchISOInMemoryWith(b.img, Patch{RELSArc: arc})
	if err != nil {
		t.Fatal(err)
	}
	newFST := uint32(alignUp64(uint64(len(b.img)), isoFSTAlignment))
	relsOff := uint32(alignUp64(uint64(newFST)+uint64(len(b.fst)), 0x8000))
	b.checkPatchedWithRELS(t, out, res, newFST, arc, relsOff)
}

// Past the disc-size cap the game can't read, so the archive is never
// put there.
func TestChooseRELSArcSlotStaysUnderDiscCap(t *testing.T) {
	used := []extent{{start: 0, end: gcDiscSize - 0x8000}}
	if off, err := chooseRELSArcSlot(used, 0x8000, 0x1000, gcDiscSize+0x10000000); err != nil || off != gcDiscSize-0x8000 {
		t.Errorf("0x8000 bytes: off 0x%x err %v, want 0x%x", off, err, gcDiscSize-0x8000)
	}
	if off, err := chooseRELSArcSlot(used, 0x8001, 0x1000, gcDiscSize+0x10000000); err == nil {
		t.Errorf("0x8001 bytes placed at 0x%x, past the cap", off)
	}
	// A trimmed image appends at EOF, but never past the cap either.
	if off, err := chooseRELSArcSlot(used, 0x8000, 0x1000, gcDiscSize-0x8000); err != nil || off != gcDiscSize-0x8000 {
		t.Errorf("EOF append of 0x8000 bytes: off 0x%x err %v, want 0x%x", off, err, gcDiscSize-0x8000)
	}
	if _, err := chooseRELSArcSlot(used, 0x8001, 0x1000, gcDiscSize-0x8000); err == nil || !strings.Contains(err.Error(), "no free space found for the 32769-byte RELS.arc") {
		t.Errorf("EOF append of 0x8001 bytes: err %v, want a no-free-space error", err)
	}
	// 0x20 alignment is the fallback: a slot the 0x8000 alignment can't
	// use, the 0x20 one can.
	used = []extent{{start: 0, end: gcDiscSize - 0x8020}}
	if off, err := chooseRELSArcSlot(used, 0x8010, 0x1000, gcDiscSize); err != nil || off != gcDiscSize-0x8020 {
		t.Errorf("0x8010 bytes in a 0x8020-byte gap: off 0x%x err %v, want 0x%x", off, err, gcDiscSize-0x8020)
	}
	// Below the patched DOL's end is never used, however free.
	used = []extent{{start: 0x100000, end: gcDiscSize - 0x8000}}
	if off, err := chooseRELSArcSlot(used, 0x8000, 0x180000, gcDiscSize); err != nil || off != gcDiscSize-0x8000 {
		t.Errorf("with the only gap before the DOL's end: off 0x%x err %v, want 0x%x", off, err, gcDiscSize-0x8000)
	}
}

func TestPatchISORELSArcRejects(t *testing.T) {
	b := vanillaLike().build(t)
	if _, _, err := PatchISOInMemoryWith(b.img, Patch{RELSArc: []byte("Yaz0 not an archive")}); err == nil || !strings.Contains(err.Error(), "isn't a RARC archive") {
		t.Errorf("not an archive: %v", err)
	}
	d := vanillaLike()
	d.files[2].name = "rels.bin"
	nb := d.build(t)
	if _, _, err := PatchISOInMemoryWith(nb.img, Patch{RELSArc: testRELSArc(0x1000)}); err == nil || !strings.Contains(err.Error(), `no file "RELS.arc" in the root directory`) {
		t.Errorf("no RELS.arc: %v", err)
	}
	// The name is matched the way the DVD library matches it.
	d.files[2].name = "rels.ARC"
	cb := d.build(t)
	if _, res, err := PatchISOInMemoryWith(cb.img, Patch{RELSArc: testRELSArc(0x40000)}); err != nil || !res.RELSArcReplaced() {
		t.Errorf("rels.ARC: %+v, %v", res, err)
	}
	// A RELS.arc inside a directory isn't the root's.
	td := treeDisc()
	td.files = append(td.files, testFile{name: "res/RELS.arc", off: 0x5F0000, size: 0x1000})
	tb := td.build(t)
	if _, _, err := PatchISOInMemoryWith(tb.img, Patch{RELSArc: testRELSArc(0x1000)}); err == nil || !strings.Contains(err.Error(), `no file "RELS.arc"`) {
		t.Errorf("RELS.arc in a subdirectory: %v", err)
	}
}

func TestPatchISOWithFile(t *testing.T) {
	b := vanillaLike().build(t)
	dir := t.TempDir()
	in := filepath.Join(dir, "ww.iso")
	out := filepath.Join(dir, "ww-multiplayer.iso")
	writeFile(t, in, b.img)
	arc := testRELSArc(0x40000)
	res, err := PatchISOWith(in, out, Patch{RELSArc: arc})
	if err != nil {
		t.Fatal(err)
	}
	dolEnd := uint64(b.dolOff) + uint64(len(b.dol)) + uint64(len(T2Bytes))
	b.checkPatchedWithRELS(t, readFile(t, out), res, wwSafeFSTOff, arc, uint32(alignUp64(dolEnd, 0x8000)))
	if !bytes.Equal(readFile(t, in), b.img) {
		t.Error("input file changed")
	}
	// The patched disc reads back through Disc with the new archive.
	d, err := OpenDisc(out)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if got, err := d.ReadFile("RELS.arc"); err != nil || !bytes.Equal(got, arc) {
		t.Errorf("ReadFile(RELS.arc) = %d bytes, %v", len(got), err)
	}
	if got, err := d.ReadFile("res/Stage.arc"); err != nil || !bytes.Equal(got, b.img[0x540000:0x560000]) {
		t.Errorf("ReadFile(res/Stage.arc) = %d bytes, %v", len(got), err)
	}
}
