package inject

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// Disc-image formats Dolphin can play but the patcher can't edit. Users
// often have one of these instead of a plain ISO (RVZ is the default of
// Dolphin's Convert File dialog), so name the format and say how to get an
// ISO rather than failing the game-ID check with gibberish.
var (
	rvzMagic = []byte("RVZ\x01")
	wiaMagic = []byte("WIA\x01")
)

const (
	gczMagic   = 0xB10BC001 // little-endian u32 at offset 0
	nkitOffset = 0x200      // "NKIT" here in the disc header marks an NKit image
)

// UnsupportedFormatError reports an input in a disc-image format the
// patcher can't patch directly. Its message says how to convert it.
type UnsupportedFormatError struct {
	Format string // "RVZ", "WIA", "GCZ" or "NKit"
}

func (e *UnsupportedFormatError) Error() string {
	if e.Format == "NKit" {
		return "iso: this is an NKit image, which is not a full disc image. " +
			"Convert it back to a full ISO with the NKit tool that made it (or re-dump your disc), then patch that"
	}
	return fmt.Sprintf("iso: this is a compressed %s image, not a plain ISO. "+
		"In Dolphin, right-click the game > Convert File... > Format: ISO "+
		"(Remove Junk Data on or off is fine), then patch the .iso it writes", e.Format)
}

// detectUnsupportedFormat returns an *UnsupportedFormatError if b starts
// like an RVZ, WIA, GCZ or NKit image, else nil.
func detectUnsupportedFormat(b []byte) error {
	var format string
	switch {
	case bytes.HasPrefix(b, rvzMagic):
		format = "RVZ"
	case bytes.HasPrefix(b, wiaMagic):
		format = "WIA"
	case len(b) >= 4 && binary.LittleEndian.Uint32(b) == gczMagic:
		format = "GCZ"
	case len(b) >= nkitOffset+4 && string(b[nkitOffset:nkitOffset+4]) == "NKIT":
		format = "NKit"
	default:
		return nil
	}
	return &UnsupportedFormatError{Format: format}
}
