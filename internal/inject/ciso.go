package inject

import (
	"encoding/binary"
	"fmt"
	"io"
)

// CISO ("Compact ISO") format — header is fixed 0x8000 bytes:
//
//	0x00..0x03  magic "CISO"
//	0x04..0x07  block size (u32 LE; commonly 0x200000 = 2 MB)
//	0x08..0x7FFF  block table (each byte: 1 = block present in file, 0 = absent/all-zero)
//
// After the header, present blocks are stored back-to-back in table order.
// Absent blocks are reconstructed as block_size zero bytes during decode.
//
// Block table size = 0x7FF8 entries → max addressable = 0x7FF8 × block_size,
// so a 2 MB block-size CISO can represent up to ~64 GB. Wind Waker uses
// only the first ~700 of those entries.
const (
	cisoHeaderSize = 0x8000
	cisoTableOff   = 0x08
	cisoTableSize  = cisoHeaderSize - cisoTableOff
)

// cisoLayout is a checked CISO header: the block size and which blocks the
// file stores.
type cisoLayout struct {
	blockSize uint32
	table     []byte // the block table; nonzero = block stored in the file
	blocks    int    // table entries the image spans: last stored block + 1
	stored    int    // how many blocks the file stores
}

// parseCISOHeader checks the CISO header at the start of hdr (which may be
// the whole file) and returns its layout. The decoded image is capped by
// cisoDecodedSize, so a valid layout never asks for more than a disc.
func parseCISOHeader(hdr []byte) (cisoLayout, error) {
	if len(hdr) < cisoHeaderSize {
		return cisoLayout{}, fmt.Errorf("ciso: header truncated (%d bytes, need %d)",
			len(hdr), cisoHeaderSize)
	}
	if string(hdr[:4]) != "CISO" {
		return cisoLayout{}, fmt.Errorf("ciso: bad magic %q", hdr[:4])
	}
	l := cisoLayout{blockSize: binary.LittleEndian.Uint32(hdr[4:8]), table: hdr[cisoTableOff:cisoHeaderSize]}
	if l.blockSize == 0 || l.blockSize > 0x10_000_000 {
		return cisoLayout{}, fmt.Errorf("ciso: implausible block size 0x%x", l.blockSize)
	}

	// The last stored block sizes the image.
	for i := 0; i < cisoTableSize; i++ {
		if l.table[i] != 0 {
			l.blocks = i + 1
			l.stored++
		}
	}
	if l.stored == 0 {
		return cisoLayout{}, fmt.Errorf("ciso: no present blocks (input is empty)")
	}
	if _, err := cisoDecodedSize(l.blockSize, l.blocks); err != nil {
		return cisoLayout{}, err
	}
	return l, nil
}

// imageSize is the size of the image the CISO decodes to.
func (l cisoLayout) imageSize() uint64 { return uint64(l.blocks) * uint64(l.blockSize) }

// checkFileSize returns an error if a CISO file of fileSize bytes is too
// short to hold the header and every stored block.
func (l cisoLayout) checkFileSize(fileSize uint64) error {
	expectedSize := uint64(cisoHeaderSize) + uint64(l.stored)*uint64(l.blockSize)
	if fileSize < expectedSize {
		return fmt.Errorf("ciso: payload truncated — expected %d bytes (header + %d × 0x%x), got %d",
			expectedSize, l.stored, l.blockSize, fileSize)
	}
	return nil
}

// decompressCISO inflates a CISO-format buffer back to a plain ISO. The
// resulting size is (last_present_block_index + 1) × block_size; trailing
// all-zero blocks past the last present one are dropped (the original
// `wit copy --trunc` behavior — saves disk space and Dolphin reads it
// just fine).
func decompressCISO(ciso []byte) ([]byte, error) {
	l, err := parseCISOHeader(ciso)
	if err != nil {
		return nil, err
	}
	if err := l.checkFileSize(uint64(len(ciso))); err != nil {
		return nil, err
	}

	out := make([]byte, l.imageSize())

	src := cisoHeaderSize
	blockSize := int(l.blockSize)
	for i := 0; i < l.blocks; i++ {
		if l.table[i] != 0 {
			dst := i * blockSize
			copy(out[dst:dst+blockSize], ciso[src:src+blockSize])
			src += blockSize
		}
		// else: block stays zero (out is already zero-initialized).
	}

	return out, nil
}

// cisoReader reads the image a CISO decodes to straight out of the CISO
// file, one block at a time through the block table, so a 1.4 GB image is
// never inflated in memory. Absent blocks read as zeros. It is safe for
// concurrent use when the underlying ReaderAt is (an *os.File is).
type cisoReader struct {
	r    io.ReaderAt
	l    cisoLayout
	slot []int32 // per block of the image: its index among the stored blocks, or -1
}

// newCISOReader checks the CISO header of the fileSize-byte file r and
// maps its blocks.
func newCISOReader(r io.ReaderAt, fileSize int64) (*cisoReader, error) {
	hdr := make([]byte, min(max(fileSize, 0), cisoHeaderSize))
	if err := readFullAt(r, hdr, 0); err != nil {
		return nil, fmt.Errorf("ciso: read header: %w", err)
	}
	l, err := parseCISOHeader(hdr)
	if err != nil {
		return nil, err
	}
	if err := l.checkFileSize(uint64(fileSize)); err != nil {
		return nil, err
	}
	c := &cisoReader{r: r, l: l, slot: make([]int32, l.blocks)}
	k := int32(0)
	for i := range c.slot {
		c.slot[i] = -1
		if l.table[i] != 0 {
			c.slot[i] = k
			k++
		}
	}
	return c, nil
}

// size is the size of the decoded image.
func (c *cisoReader) size() int64 { return int64(c.l.imageSize()) }

// ReadAt implements io.ReaderAt over the decoded image.
func (c *cisoReader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, fmt.Errorf("ciso: read at negative offset %d", off)
	}
	bs, size := int64(c.l.blockSize), c.size()
	n := 0
	for n < len(p) {
		pos := off + int64(n)
		if pos >= size {
			return n, io.EOF
		}
		within := pos % bs
		dst := p[n : n+int(min(int64(len(p)-n), bs-within, size-pos))]
		if s := c.slot[pos/bs]; s >= 0 {
			if err := readFullAt(c.r, dst, cisoHeaderSize+int64(s)*bs+within); err != nil {
				return n, err
			}
		} else {
			clear(dst)
		}
		n += len(dst)
	}
	return n, nil
}

// readFullAt fills p from r at off. Unlike a bare ReadAt it accepts io.EOF
// alongside a full read (which the io.ReaderAt contract allows at the end of
// the input) and turns a short read into io.ErrUnexpectedEOF.
func readFullAt(r io.ReaderAt, p []byte, off int64) error {
	n, err := r.ReadAt(p, off)
	if n == len(p) {
		return nil
	}
	if err == nil || err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return err
}

// cisoDecodedSize returns the size of the image a CISO decodes to, given
// how many table entries it spans (last present block index + 1). Absent
// blocks cost nothing in the file but decode to zeros, so a tiny crafted
// CISO marking only a far-out block present would otherwise make us
// allocate gigabytes. A GameCube disc never needs more blocks than cover
// gcDiscSize, so cap the output at gcDiscSize rounded up to a block.
func cisoDecodedSize(blockSize uint32, blocks int) (uint64, error) {
	maxBlocks := (uint64(gcDiscSize) + uint64(blockSize) - 1) / uint64(blockSize)
	if uint64(blocks) > maxBlocks {
		return 0, fmt.Errorf("ciso: block table spans %d blocks of 0x%x bytes, more than a GameCube disc (0x%x bytes) needs",
			blocks, blockSize, gcDiscSize)
	}
	return uint64(blocks) * uint64(blockSize), nil
}
