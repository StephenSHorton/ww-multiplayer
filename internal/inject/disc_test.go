package inject

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/disc"
)

// Synthetic GameCube images for the patcher and Disc tests. A real GZLE01
// image is 1.4 GB and copyrighted; these are 6 MiB stand-ins with the same
// structure: boot.bin with the game ID and DOL/FST pointers, bi2.bin, an
// apploader, a DOL big enough to hold every DOLPatches site, an FST with a
// directory (or, for the Disc tests, a nested tree) and a few files, and
// file data wherever a test wants it. PatchDOL doesn't check the original
// bytes at the hook sites, so the DOL body is an arbitrary pattern.

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
	tree   bool // file names are slash-separated paths, laid out by buildTreeFST
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
	if d.tree {
		fst = buildTreeFST(d.files, d.fstPad)
	}
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

// buildTreeFST lays files out under the directories their slash-separated
// names spell: each directory once, where its first file appears, holding
// its files and subdirectories in the order they first appear (so a
// directory's entries are contiguous, as the FST requires). pad extra
// string-table bytes set the FST's size.
func buildTreeFST(files []testFile, pad int) []byte {
	type node struct {
		name string
		file *testFile // nil for a directory
		kids []*node
	}
	root := &node{}
	for i := range files {
		parts := strings.Split(files[i].name, "/")
		n := root
		for _, p := range parts[:len(parts)-1] {
			var dir *node
			for _, k := range n.kids {
				if k.file == nil && k.name == p {
					dir = k
					break
				}
			}
			if dir == nil {
				dir = &node{name: p}
				n.kids = append(n.kids, dir)
			}
			n = dir
		}
		n.kids = append(n.kids, &node{name: parts[len(parts)-1], file: &files[i]})
	}

	ents := make([]byte, fstEntrySize) // the root, filled in last
	var names []byte
	name := func(s string) uint32 {
		off := uint32(len(names))
		names = append(append(names, s...), 0)
		return off
	}
	var emit func(dir *node, parent int)
	emit = func(dir *node, parent int) {
		for _, k := range dir.kids {
			i := len(ents) / fstEntrySize
			ents = append(ents, make([]byte, fstEntrySize)...)
			if k.file != nil {
				put32(ents, i*fstEntrySize, name(k.name))
				put32(ents, i*fstEntrySize+4, k.file.off)
				put32(ents, i*fstEntrySize+8, k.file.size)
				continue
			}
			put32(ents, i*fstEntrySize, 1<<24|name(k.name))
			put32(ents, i*fstEntrySize+4, uint32(parent))
			emit(k, i)
			put32(ents, i*fstEntrySize+8, uint32(len(ents)/fstEntrySize))
		}
	}
	emit(root, 0)
	put32(ents, 0, 1<<24)
	put32(ents, 8, uint32(len(ents)/fstEntrySize))
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

// treeDisc is vanillaLike with its files nested the way the game's res/
// tree nests them, plus an empty file.
func treeDisc() testDisc {
	d := vanillaLike()
	d.tree = true
	d.files = []testFile{
		{name: "res/Stage/M_NewD2/Stage.arc", off: 0x540000, size: 0x20000},
		{name: "res/Stage/M_NewD2/Room0.arc", off: 0x560000, size: 0x1234},
		{name: "res/Stage/sea/Stage.arc", off: 0x570000, size: 0x8000},
		{name: "res/Object/Link.arc", off: 0x580000, size: 0x1000},
		{name: "res/Object/empty.arc", off: 0, size: 0},
		{name: "opening.bnr", off: 0x5A0000, size: 0x1960},
		{name: "Audio.aw", off: 0x5B0000, size: 0x4F000},
	}
	return d
}

// discFiles is what Disc.Files should list for b.
func (b builtDisc) discFiles() []disc.DiscFile {
	var want []disc.DiscFile
	for _, f := range b.files {
		want = append(want, disc.DiscFile{Path: f.name, Offset: f.off, Size: f.size})
	}
	return want
}

// checkDisc asserts d lists and reads every file of b and b's DOL.
func (b builtDisc) checkDisc(t *testing.T, d *Disc) {
	t.Helper()
	if got, want := d.Files(), b.discFiles(); !slices.Equal(got, want) {
		t.Fatalf("Files() = %v\nwant %v", got, want)
	}
	for _, f := range b.files {
		got, err := d.ReadFile(f.name)
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", f.name, err)
		}
		if !bytes.Equal(got, b.img[f.off:f.off+f.size]) {
			t.Fatalf("ReadFile(%q) returned the wrong bytes", f.name)
		}
	}
	dol, err := d.ReadDOL()
	if err != nil || !bytes.Equal(dol, b.dol) {
		t.Fatalf("ReadDOL: %d bytes, %v; want the %d-byte DOL", len(dol), err, len(b.dol))
	}
}

func TestDiscReadsISOAndCISO(t *testing.T) {
	b := treeDisc().build(t)
	dir := t.TempDir()
	isoPath, cisoPath := filepath.Join(dir, "ww.iso"), filepath.Join(dir, "ww.ciso")
	writeFile(t, isoPath, b.img)
	ciso := makeCISO(b.img, 0x10000)
	writeFile(t, cisoPath, ciso)

	for _, c := range []struct {
		name string
		open func() (*Disc, error)
	}{
		{"ISO reader", func() (*Disc, error) { return NewDisc(bytes.NewReader(b.img), int64(len(b.img))) }},
		{"ISO file", func() (*Disc, error) { return OpenDisc(isoPath) }},
		{"CISO reader", func() (*Disc, error) { return NewDisc(bytes.NewReader(ciso), int64(len(ciso))) }},
		{"CISO file", func() (*Disc, error) { return OpenDisc(cisoPath) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, err := c.open()
			if err != nil {
				t.Fatal(err)
			}
			b.checkDisc(t, d)
			if err := d.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
		})
	}
}

// A patched image has the FST relocated and a bigger DOL; the reader
// follows the header to both.
func TestDiscReadsPatchedImage(t *testing.T) {
	b := treeDisc().build(t)
	out, err := PatchISOInMemory(b.img)
	if err != nil {
		t.Fatal(err)
	}
	if be32(out, isoHeaderFSTOff) == b.fstOff {
		t.Fatal("test setup: the FST didn't move")
	}
	d, err := NewDisc(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	patched, err := PatchDOL(b.dol)
	if err != nil {
		t.Fatal(err)
	}
	b.dol = patched
	b.checkDisc(t, d)
}

func TestDiscStat(t *testing.T) {
	b := treeDisc().build(t)
	d, err := NewDisc(bytes.NewReader(b.img), int64(len(b.img)))
	if err != nil {
		t.Fatal(err)
	}
	found := []struct{ path, canon string }{
		{"res/Stage/sea/Stage.arc", "res/Stage/sea/Stage.arc"},
		{"/res/Stage/sea/Stage.arc", "res/Stage/sea/Stage.arc"},
		{"RES/stage/SEA/stage.ARC", "res/Stage/sea/Stage.arc"}, // dvdfs.c isSame lowercases both sides
		{"res/Object/empty.arc", "res/Object/empty.arc"},
		{"audio.aw", "Audio.aw"},
	}
	for _, c := range found {
		f, err := d.Stat(c.path)
		if err != nil || f.Path != c.canon {
			t.Errorf("Stat(%q) = %+v, %v; want path %q", c.path, f, err, c.canon)
		}
	}
	missing := []string{
		"", "/", "res", "res/", "res/Stage", // directories aren't files
		"res/Stage/sea/Stage.arc/x", "opening.bnr/", // a file isn't a directory
		"res//Stage/sea/Stage.arc", "./opening.bnr", "res/Stage/../Stage/sea/Stage.arc",
		"missing.arc", "res/Stage/sea/Stage.arcx", "res/Stage/sea/Stage.ar",
		"res/Stage/M_NewD2/Link.arc", // in a sibling directory
	}
	for _, p := range missing {
		if f, err := d.Stat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(%q) = %+v, %v; want fs.ErrNotExist", p, f, err)
		}
		if _, err := d.ReadFile(p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("ReadFile(%q) err = %v, want fs.ErrNotExist", p, err)
		}
	}

	r, err := d.Open("opening.bnr")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(got, b.img[0x5A0000:0x5A0000+0x1960]) {
		t.Fatalf("Open(opening.bnr) read %d bytes, %v", len(got), err)
	}
}

// A name followed by "/" matches only a directory, while a last component
// takes the first entry of that name, file or directory, as
// DVDConvertPathToEntrynum does (dvdfs.c:139-156). So a file listed after
// a directory of the same name can't be opened, by the game or by us.
func TestDiscStatFileAndDirectoryOfOneName(t *testing.T) {
	for _, c := range []struct {
		name     string
		files    []string
		fileOpen bool // whether Stat("x") finds the file
	}{
		{"file first", []string{"x", "x/a"}, true},
		{"directory first", []string{"x/a", "x"}, false},
	} {
		img := tinyDisc(c.files...)
		d, err := NewDisc(bytes.NewReader(img), int64(len(img)))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if _, err := d.Stat("x/a"); err != nil {
			t.Errorf("%s: Stat(x/a): %v", c.name, err)
		}
		if _, err := d.Stat("x"); (err == nil) != c.fileOpen {
			t.Errorf("%s: Stat(x) err = %v, want found = %v", c.name, err, c.fileOpen)
		}
	}
}

func TestNewDiscRejects(t *testing.T) {
	b := vanillaLike().build(t)
	mutate := func(f func(img []byte) []byte) []byte { return f(bytes.Clone(b.img)) }
	cases := []struct {
		name    string
		img     []byte
		wantErr string
	}{
		{"wrong game id", mutate(func(img []byte) []byte { copy(img, "GZLP01"); return img }), `unexpected game id "GZLP01"`},
		{"too small", b.img[:isoHeaderEnd-1], "image too small"},
		{"empty", nil, "image too small"},
		{"FST past end", mutate(func(img []byte) []byte { put32(img, isoHeaderFSTOff, uint32(len(img)-4)); return img }), "runs past the end of the image"},
		{"FST over the cap", mutate(func(img []byte) []byte { put32(img, isoHeaderFSTSize, discMaxFST+1); return img }), "more than the 0x400000 cap"},
		{"root not a directory", mutate(func(img []byte) []byte { img[b.fstOff] = 0; return img }), "root entry is not a directory"},
		{"CISO payload truncated", makeCISO(b.img, 0x100000)[:cisoHeaderSize+0x100000], "payload truncated"},
		{"CISO header truncated", []byte("CISO\x00\x00\x10\x00"), "header truncated"},
	}
	for _, c := range cases {
		_, err := NewDisc(bytes.NewReader(c.img), int64(len(c.img)))
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.wantErr)
		}
	}

	var ufe *UnsupportedFormatError
	rvz := []byte("RVZ\x01\x00\x00\x00\x00")
	if _, err := NewDisc(bytes.NewReader(rvz), int64(len(rvz))); !errors.As(err, &ufe) || ufe.Format != "RVZ" {
		t.Errorf("RVZ: err = %v, want UnsupportedFormatError", err)
	}
	if _, err := OpenDisc(filepath.Join(t.TempDir(), "missing.iso")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing file: err = %v, want fs.ErrNotExist", err)
	}
}

// A file or DOL whose bytes lie past the end of the image (a truncated
// download) opens fine but fails to read, naming the problem.
func TestDiscTruncatedImage(t *testing.T) {
	b := treeDisc().build(t)
	img := b.img[:0x5A0000+0x1000] // cuts opening.bnr short and drops Audio.aw
	d, err := NewDisc(bytes.NewReader(img), int64(len(img)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ReadFile("res/Stage/sea/Stage.arc"); err != nil {
		t.Errorf("a file before the cut: %v", err)
	}
	for _, p := range []string{"opening.bnr", "Audio.aw"} {
		if _, err := d.ReadFile(p); err == nil || !strings.Contains(err.Error(), "is the image truncated?") {
			t.Errorf("ReadFile(%q) err = %v, want a truncation error", p, err)
		}
		if _, err := d.Open(p); err == nil {
			t.Errorf("Open(%q) succeeded", p)
		}
	}

	for name, f := range map[string]func(img []byte){
		"DOL header past end": func(img []byte) { put32(img, isoHeaderDOLOff, uint32(len(img)-0x80)) },
		"DOL section past end": func(img []byte) {
			put32(img, int(b.dolOff)+0x18, 0x100) // T6 as big as the image: under the RAM cap, past the image end
			put32(img, int(b.dolOff)+0xA8, uint32(len(img)))
		},
		"DOL over the RAM cap": func(img []byte) {
			put32(img, int(b.dolOff)+0x18, 0x100)
			put32(img, int(b.dolOff)+0xA8, dolMaxSize)
		},
	} {
		img := bytes.Clone(b.img)
		f(img)
		d, err := NewDisc(bytes.NewReader(img), int64(len(img)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := d.ReadDOL(); err == nil {
			t.Errorf("%s: ReadDOL succeeded", name)
		}
	}
}

// fstEntryBytes encodes one FST entry.
func fstEntryBytes(dir bool, nameOff, w1, w2 uint32) []byte {
	e := make([]byte, fstEntrySize)
	if dir {
		nameOff |= 1 << 24
	}
	put32(e, 0, nameOff)
	put32(e, 4, w1)
	put32(e, 8, w2)
	return e
}

func TestParseFSTTree(t *testing.T) {
	// 0 root, 1 a/ (to 5), 2 a/b/ (to 4), 3 a/b/c.arc, 4 a/d.arc, 5 e.arc.
	files := []testFile{{name: "a/b/c.arc", off: 0x100}, {name: "a/d.arc", off: 0x200}, {name: "e.arc", off: 0x300}}
	good := buildTreeFST(files, 0)
	ents, err := parseFSTTree(good)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 6 || !ents[1].dir || ents[1].name != "a" || ents[1].b != 5 || ents[2].b != 4 || ents[3].name != "c.arc" {
		t.Fatalf("parsed %+v", ents)
	}
	names := 6 * fstEntrySize // string table: "a\0b\0c.arc\0d.arc\0e.arc\0"

	bad := []struct {
		name    string
		mutate  func(fst []byte) []byte
		wantErr string
	}{
		{"directory ends at itself", func(f []byte) []byte { put32(f, 1*fstEntrySize+8, 1); return f }, "outside"},
		{"directory ends before itself", func(f []byte) []byte { put32(f, 2*fstEntrySize+8, 0); return f }, "outside"},
		{"directory past its parent", func(f []byte) []byte { put32(f, 2*fstEntrySize+8, 6); return f }, "outside"},
		{"directory past the root", func(f []byte) []byte { put32(f, 1*fstEntrySize+8, 7); return f }, "outside"},
		{"name past the table", func(f []byte) []byte { put32(f, 3*fstEntrySize, 0xFFFFFF); return f }, "bad name #3"},
		{"empty name", func(f []byte) []byte { put32(f, 3*fstEntrySize, 1); return f }, `bad name ""`},
		{"unterminated name", func(f []byte) []byte { return f[:len(f)-1] }, `bad name "e.arc"`},
		{"slash in a name", func(f []byte) []byte { f[names+4] = '/'; return f }, `bad name "/.arc"`},
	}
	for _, c := range bad {
		_, err := parseFSTTree(c.mutate(bytes.Clone(good)))
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.wantErr)
		}
	}

	// Paths may be discMaxPath bytes long, no longer.
	long := strings.Repeat("n", discMaxPath-4)
	if _, err := parseFSTTree(buildTreeFST([]testFile{{name: "dir/" + long}}, 0)); err != nil {
		t.Errorf("a %d-byte path: %v", discMaxPath, err)
	}
	if _, err := parseFSTTree(buildTreeFST([]testFile{{name: "dir/" + long + "n"}}, 0)); err == nil {
		t.Errorf("a %d-byte path parsed", discMaxPath+1)
	}

	// An empty directory is fine and lists nothing.
	empty := slices.Concat(fstEntryBytes(true, 0, 0, 3), fstEntryBytes(true, 0, 0, 2), fstEntryBytes(false, 2, 0x40, 1), []byte("d\x00f\x00"))
	ents, err = parseFSTTree(empty)
	if err != nil {
		t.Fatal(err)
	}
	if got := (&Disc{ents: ents}).Files(); !slices.Equal(got, []disc.DiscFile{{Path: "f", Offset: 0x40, Size: 1}}) {
		t.Errorf("Files() = %v", got)
	}
}

func TestDOLBytesAt(t *testing.T) {
	dol := testDOL()
	d0 := int(be32(dol, 0x1C))
	ok := []struct {
		addr, size uint32
		want       []byte
	}{
		{0x80003100, 4, dol[0x100:0x104]},              // start of T0
		{0x80005600 - 4, 4, dol[0x2600-4 : 0x2600]},    // end of T0
		{0x80380010, 0x20, dol[d0+0x10 : d0+0x30]},     // inside D0
		{0x80380000, 0x8000, dol[d0 : d0+0x8000]},      // all of D0
		{0x80380000 + 0x8000 - 1, 1, dol[len(dol)-1:]}, // last byte
	}
	for _, c := range ok {
		got, err := DOLBytesAt(dol, c.addr, c.size)
		if err != nil || !bytes.Equal(got, c.want) {
			t.Errorf("DOLBytesAt(0x%08x, 0x%x) = %d bytes, %v", c.addr, c.size, len(got), err)
		}
	}
	for _, c := range []struct{ addr, size uint32 }{
		{0x80380000, 0x8001},     // runs past D0
		{0x80005600 - 2, 4},      // straddles T0 and T1
		{0x803A0000, 4},          // BSS
		{0x80000000, 4},          // below every section
		{0xFFFFFFFF, 2},          // would wrap
		{0x80003100 - 1, 0x1000}, // starts before T0
	} {
		if _, err := DOLBytesAt(dol, c.addr, c.size); err == nil {
			t.Errorf("DOLBytesAt(0x%08x, 0x%x) succeeded", c.addr, c.size)
		}
	}
	if _, err := DOLBytesAt(dol[:len(dol)-1], 0x80380000, 4); err == nil || !strings.Contains(err.Error(), "runs past the end") {
		t.Errorf("truncated DOL: err = %v", err)
	}
	if _, err := DOLBytesAt(dol[:0x80], 0x80003100, 4); err == nil {
		t.Error("headerless DOL accepted")
	}

	// The mod's T2 section reads back from a patched DOL by its address.
	patched, err := PatchDOL(dol)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := DOLBytesAt(patched, T2Address, uint32(len(T2Bytes))); err != nil || !bytes.Equal(got, T2Bytes) {
		t.Errorf("T2 from the patched DOL: %d bytes, %v", len(got), err)
	}
}

// tinyDisc is a complete GZLE01 image of a few KiB for the fuzzer and the
// small FST cases: boot.bin, a 0x140-byte DOL, an FST of paths (nested by
// buildTreeFST) and a little data for each file.
func tinyDisc(paths ...string) []byte {
	const dolOff, fstOff, dataOff = 0x500, 0x700, 0x1000
	files := make([]testFile, len(paths))
	end := uint32(dataOff)
	for i, p := range paths {
		files[i] = testFile{name: p, off: end, size: 0x20 + uint32(i)}
		end += 0x40
	}
	fst := buildTreeFST(files, 0)
	if fstOff+len(fst) > dataOff {
		panic("tinyDisc: FST too big")
	}
	img := make([]byte, end)
	copy(img, isoExpectedGameID)
	put32(img, isoHeaderDOLOff, dolOff)
	put32(img, isoHeaderFSTOff, fstOff)
	put32(img, isoHeaderFSTSize, uint32(len(fst)))
	put32(img, dolOff+0x00, dolHeaderSize) // T0: file offset, address, size
	put32(img, dolOff+0x48, 0x80003100)
	put32(img, dolOff+0x90, 0x40)
	tile(img[dolOff+dolHeaderSize:dolOff+dolHeaderSize+0x40], pattern(0x40, 0x31))
	copy(img[fstOff:], fst)
	for i, f := range files {
		tile(img[f.off:f.off+f.size], pattern(int(f.size), byte(i)))
	}
	return img
}

// FuzzNewDisc feeds NewDisc arbitrary images, plain and CISO, then lists,
// stats and reads what opens. The seed corpus runs as an ordinary test; to
// fuzz:
//
//	go test -run='^$' -fuzz=FuzzNewDisc -fuzztime=30s ./internal/inject/
func FuzzNewDisc(f *testing.F) {
	iso := tinyDisc("res/Stage/sea/Stage.arc", "res/Stage/sea/Room1.arc", "res/Object/Link.arc", "opening.bnr")
	f.Add(iso)
	f.Add(makeCISO(iso, 0x400))
	f.Add(tinyDisc("x", "x/a", "X/b"))
	f.Add(iso[:isoHeaderEnd])
	f.Add(iso[:0x800])
	f.Add([]byte("CISO\x00\x04\x00\x00\x01"))
	f.Add([]byte("RVZ\x01"))
	f.Fuzz(func(t *testing.T, img []byte) {
		d, err := NewDisc(bytes.NewReader(img), int64(len(img)))
		if err != nil {
			return
		}
		for i, df := range d.Files() {
			if i == 64 {
				break
			}
			// Stat may find a same-named entry first (or a directory, which
			// isn't a file), but whatever it finds is spelled like the query.
			got, err := d.Stat(df.Path)
			if err != nil {
				if !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("Stat(%q): %v", df.Path, err)
				}
				continue
			}
			if !asciiEqualFold(got.Path, df.Path) {
				t.Fatalf("Stat(%q) found %q", df.Path, got.Path)
			}
			if b, err := d.ReadFile(df.Path); err == nil && len(b) != int(got.Size) {
				t.Fatalf("ReadFile(%q) = %d bytes, Stat says %d", df.Path, len(b), got.Size)
			}
		}
		if dol, err := d.ReadDOL(); err == nil && len(dol) < dolHeaderSize {
			t.Fatalf("ReadDOL returned %d bytes", len(dol))
		}
	})
}
