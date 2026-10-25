package inject

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
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

// Convertible says Dolphin's own DolphinTool can turn this format into a
// plain ISO the patcher reads (internal/dolphintool): RVZ, WIA and GCZ.
// NKit is not, until proven: an NKit image is a trimmed dump, and whether
// DolphinTool restores it to the full disc is untested (it is left on the
// advice path, convert with the NKit tool).
func (e *UnsupportedFormatError) Convertible() bool {
	return e.Format == "RVZ" || e.Format == "WIA" || e.Format == "GCZ"
}

// ImageFormat reads the start of the file at path and returns the
// *UnsupportedFormatError naming its format when it is an RVZ, WIA, GCZ
// or NKit image, else nil (an ISO, a CISO, or no disc at all: OpenDisc
// says which). err is for a file that can't be read.
func ImageFormat(path string) (*UnsupportedFormatError, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	head := make([]byte, nkitOffset+4)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	var u *UnsupportedFormatError
	if errors.As(detectUnsupportedFormat(head[:n]), &u) {
		return u, nil
	}
	return nil, nil
}
