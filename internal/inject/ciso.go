package inject

import (
	"encoding/binary"
	"fmt"
)

// CISO ("Compact ISO") format — header is fixed 0x8000 bytes:
//   0x00..0x03  magic "CISO"
//   0x04..0x07  block size (u32 LE; commonly 0x200000 = 2 MB)
//   0x08..0x7FFF  block table (each byte: 1 = block present in file, 0 = absent/all-zero)
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

// decompressCISO inflates a CISO-format buffer back to a plain ISO. The
// resulting size is (last_present_block_index + 1) × block_size; trailing
// all-zero blocks past the last present one are dropped (the original
// `wit copy --trunc` behavior — saves disk space and Dolphin reads it
// just fine).
func decompressCISO(ciso []byte) ([]byte, error) {
	if len(ciso) < cisoHeaderSize {
		return nil, fmt.Errorf("ciso: header truncated (%d bytes, need %d)",
			len(ciso), cisoHeaderSize)
	}
	if string(ciso[:4]) != "CISO" {
		return nil, fmt.Errorf("ciso: bad magic %q", ciso[:4])
	}
	blockSize := binary.LittleEndian.Uint32(ciso[4:8])
	if blockSize == 0 || blockSize > 0x10_000_000 {
		return nil, fmt.Errorf("ciso: implausible block size 0x%x", blockSize)
	}
	table := ciso[cisoTableOff:cisoHeaderSize]

	// Find the last present block to size the output. Counts present blocks
	// in the same pass for the data offset check below.
	lastPresent := -1
	presentCount := 0
	for i := 0; i < cisoTableSize; i++ {
		if table[i] != 0 {
			lastPresent = i
			presentCount++
		}
	}
	if lastPresent < 0 {
		return nil, fmt.Errorf("ciso: no present blocks (input is empty)")
	}

	expectedSize := cisoHeaderSize + presentCount*int(blockSize)
	if len(ciso) < expectedSize {
		return nil, fmt.Errorf("ciso: payload truncated — expected %d bytes (header + %d × 0x%x), got %d",
			expectedSize, presentCount, blockSize, len(ciso))
	}

	outSize := (lastPresent + 1) * int(blockSize)
	out := make([]byte, outSize)

	src := cisoHeaderSize
	for i := 0; i <= lastPresent; i++ {
		if table[i] != 0 {
			dst := i * int(blockSize)
			copy(out[dst:dst+int(blockSize)], ciso[src:src+int(blockSize)])
			src += int(blockSize)
		}
		// else: block stays zero (out is already zero-initialized).
	}

	return out, nil
}
