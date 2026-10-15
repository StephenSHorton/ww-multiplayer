package inject

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"
	"testing"
)

// makeCISO packs img as a CISO with the given block size, leaving all-zero
// blocks out the way wit does.
func makeCISO(img []byte, blockSize int) []byte {
	out := make([]byte, cisoHeaderSize)
	copy(out, "CISO")
	binary.LittleEndian.PutUint32(out[4:], uint32(blockSize))
	for i := 0; i*blockSize < len(img); i++ {
		blk := img[i*blockSize : min((i+1)*blockSize, len(img))]
		if bytes.Count(blk, []byte{0}) == len(blk) {
			continue
		}
		out[cisoTableOff+i] = 1
		out = append(out, blk...)
		out = append(out, make([]byte, blockSize-len(blk))...)
	}
	return out
}

func TestDecompressCISORoundTrip(t *testing.T) {
	img := vanillaLike().build(t).img
	got, err := decompressCISO(makeCISO(img, 0x100000))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, img) {
		t.Fatal("CISO round trip changed the image")
	}

	// Trailing all-zero blocks are trimmed.
	padded := append(bytes.Clone(img), make([]byte, 0x200000)...)
	got, err = decompressCISO(makeCISO(padded, 0x100000))
	if err != nil || !bytes.Equal(got, img) {
		t.Fatalf("trailing zero blocks: len 0x%x err %v, want the unpadded image", len(got), err)
	}
}

func TestDecompressCISORejectsCrafted(t *testing.T) {
	header := func(blockSize uint32, present ...int) []byte {
		h := make([]byte, cisoHeaderSize)
		copy(h, "CISO")
		binary.LittleEndian.PutUint32(h[4:], blockSize)
		for _, i := range present {
			h[cisoTableOff+i] = 1
		}
		return h
	}
	cases := []struct {
		name    string
		ciso    []byte
		wantErr string
	}{
		// One 2 MiB block just past a full disc; a far-out index (up to
		// 0x7FF7) would ask for up to 64 GiB of zeros the same way.
		{"block past disc end", append(header(0x200000, 697), make([]byte, 0x200000)...), "more than a GameCube disc"},
		{"zero block size", header(0, 0), "implausible block size"},
		{"huge block size", header(0x20000000, 0), "implausible block size"},
		{"no blocks", header(0x200000), "no present blocks"},
		{"truncated payload", append(header(0x8000, 0, 1), make([]byte, 0x8000)...), "payload truncated"},
		{"truncated header", []byte("CISO"), "header truncated"},
	}
	for _, c := range cases {
		_, err := decompressCISO(c.ciso)
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.wantErr)
		}
	}
}

// The cap is the disc size rounded up to a whole block, not the disc size
// itself: a legitimate 2 MiB-block CISO of a full disc spans 697 blocks.
func TestCISODecodedSizeCap(t *testing.T) {
	const bs = 0x200000
	if got, err := cisoDecodedSize(bs, 697); err != nil || got != 697*bs {
		t.Errorf("697 blocks: 0x%x, %v; want 0x%x", got, err, 697*bs)
	}
	if _, err := cisoDecodedSize(bs, 698); err == nil {
		t.Error("698 blocks of 2 MiB accepted")
	}
	if _, err := cisoDecodedSize(0x10000000, 6); err != nil {
		t.Errorf("6 blocks of 256 MiB: %v", err)
	}
	if _, err := cisoDecodedSize(0x10000000, 7); err == nil {
		t.Error("7 blocks of 256 MiB accepted")
	}
}

func TestCISOReaderMatchesDecompress(t *testing.T) {
	img := treeDisc().build(t).img
	const bs = 0x8000
	ciso := makeCISO(img, bs)
	want, err := decompressCISO(ciso)
	if err != nil {
		t.Fatal(err)
	}
	c, err := newCISOReader(bytes.NewReader(ciso), int64(len(ciso)))
	if err != nil {
		t.Fatal(err)
	}
	if c.size() != int64(len(want)) {
		t.Fatalf("size = 0x%x, want 0x%x", c.size(), len(want))
	}
	if c.slot[0x400000/bs] != -1 {
		t.Fatal("test setup: the free gap at 0x400000 should be an absent block")
	}
	for _, r := range []struct{ off, n int }{
		{0, 0x440},                  // boot.bin
		{bs - 3, 7},                 // across a block boundary
		{0x400000 - 5, bs + 10},     // into, through and out of an absent block
		{0x540000, 0x20000},         // a whole file
		{len(want) - 0x10, 0x10},    // the very end
		{0, len(want)},              // everything
		{0x123457, 3*bs + 0x10_001}, // unaligned, many blocks
	} {
		got := make([]byte, r.n)
		if n, err := c.ReadAt(got, int64(r.off)); n != r.n || err != nil {
			t.Fatalf("ReadAt(0x%x, 0x%x) = %d, %v", r.off, r.n, n, err)
		}
		if !bytes.Equal(got, want[r.off:r.off+r.n]) {
			t.Fatalf("ReadAt(0x%x, 0x%x) differs from the decompressed image", r.off, r.n)
		}
	}

	// Past the end: a short read with io.EOF, as io.ReaderAt specifies.
	p := make([]byte, 0x20)
	if n, err := c.ReadAt(p, int64(len(want)-0x10)); n != 0x10 || err != io.EOF {
		t.Errorf("read across the end = %d, %v; want 16, EOF", n, err)
	}
	if n, err := c.ReadAt(p, int64(len(want))); n != 0 || err != io.EOF {
		t.Errorf("read at the end = %d, %v; want 0, EOF", n, err)
	}
	if _, err := c.ReadAt(p, -1); err == nil {
		t.Error("negative offset accepted")
	}

	// The reader checks the header exactly as decompressCISO does.
	for _, bad := range [][]byte{ciso[:len(ciso)-1], ciso[:cisoHeaderSize-1], append([]byte("CISX"), ciso[4:]...)} {
		_, errReader := newCISOReader(bytes.NewReader(bad), int64(len(bad)))
		_, errDecomp := decompressCISO(bad)
		if errReader == nil || errDecomp == nil || errReader.Error() != errDecomp.Error() {
			t.Errorf("reader err %v, decompress err %v; want the same error", errReader, errDecomp)
		}
	}
}
