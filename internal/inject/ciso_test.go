package inject

import (
	"bytes"
	"encoding/binary"
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
