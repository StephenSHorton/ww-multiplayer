package world

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"slices"
	"time"
)

// The world file, world.wwco (§4.4): a 36-byte header, then TLV sections,
// every integer big-endian.
//
//	+0x00  [8]  magic "WWCOWRLD"
//	+0x08  u16  version (FileVersion)
//	+0x0A  u16  flags, 0
//	+0x0C  [8]  worldID
//	+0x14  u64  seq
//	+0x1C  u32  body length: the bytes after the header
//	+0x20  u32  CRC-32 (IEEE) of header bytes 0x00-0x1F, then the body
//	+0x24       body: sections, each [tag:u16][len:u32][value]
//
// The header layout is the same in every version, so a reader checks the
// length and CRC before it looks at the version: a file that fails them is
// damaged (and Load falls back to a backup), while a whole file of another
// version was written by another build (and Load refuses to fall back
// past it, so a downgrade never overwrites a newer world).
//
// Sections (each at most once; IMAGE is required):
//
//	0x0001 IMAGE       the 0x778-byte image, shared bits only
//	0x0002 TOMBSTONES  n x {off:u16, mask:u8}, offsets ascending
//	0x0003 FRESH       n x {nonce:8, minted:i64 Unix ms}, nonces ascending
//	0x0004 META        created:i64 Unix ms (0 = unknown)
//
// A tag with bit 0x8000 set is optional: a reader that doesn't know it
// skips it. Any other unknown tag is damage.
//
// The scoped stores are not in the file: the game forgets them too (see
// Scopes), and every player leaves every scope when the host stops.
const (
	FileMagic   = "WWCOWRLD"
	FileVersion = 1

	headerLen = 0x24
	// maxFileSize caps what Load reads. A whole file is under 8 KiB.
	maxFileSize = 1 << 20

	secImage      = 0x0001
	secTombstones = 0x0002
	secFresh      = 0x0003
	secMeta       = 0x0004
	secOptional   = 0x8000

	secHeaderLen = 6
	tombLen      = 3
	freshLen     = 16
	metaLen      = 8
)

var (
	// ErrCorrupt is wrapped by Decode's error for a file that is damaged
	// or was never a world file.
	ErrCorrupt = errors.New("world: damaged world file")
	// ErrVersion is wrapped by Decode's error for a whole world file of a
	// version this build does not read.
	ErrVersion = errors.New("world: world file of another version")
)

func corrupt(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrCorrupt}, args...)...)
}

// Encode returns w's world file.
func Encode(w *World) []byte {
	var body []byte
	section := func(tag uint16, value []byte) {
		body = binary.BigEndian.AppendUint16(body, tag)
		body = binary.BigEndian.AppendUint32(body, uint32(len(value)))
		body = append(body, value...)
	}
	section(secImage, w.image[:])
	var tomb []byte
	for _, e := range w.tomb.Entries() {
		tomb = binary.BigEndian.AppendUint16(tomb, e.Off)
		tomb = append(tomb, e.Val)
	}
	section(secTombstones, tomb)
	var fresh []byte
	for _, f := range w.sortedFresh() {
		fresh = append(fresh, f.Nonce[:]...)
		fresh = binary.BigEndian.AppendUint64(fresh, uint64(unixMilli(f.Minted)))
	}
	section(secFresh, fresh)
	section(secMeta, binary.BigEndian.AppendUint64(nil, uint64(unixMilli(w.created))))

	b := make([]byte, headerLen, headerLen+len(body))
	copy(b, FileMagic)
	binary.BigEndian.PutUint16(b[0x08:], FileVersion)
	copy(b[0x0C:], w.id[:])
	binary.BigEndian.PutUint64(b[0x14:], w.seq)
	binary.BigEndian.PutUint32(b[0x1C:], uint32(len(body)))
	b = append(b, body...)
	binary.BigEndian.PutUint32(b[0x20:], fileCRC(b))
	return b
}

// fileCRC is the CRC of a file whose header and body are in b.
func fileCRC(b []byte) uint32 {
	c := crc32.ChecksumIEEE(b[:0x20])
	return crc32.Update(c, crc32.IEEETable, b[headerLen:])
}

// Decode parses a world file. The world it returns has the DefaultPolicy.
// Bits this build's class table does not hold are dropped, as are image
// bits the file's own tombstones refuse.
func Decode(b []byte) (*World, error) {
	w, _, err := decode(b)
	return w, err
}

// decode is Decode that also counts the bits it dropped.
func decode(b []byte) (*World, int, error) {
	if len(b) < headerLen {
		return nil, 0, corrupt("%d bytes, shorter than the %d-byte header", len(b), headerLen)
	}
	if len(b) > maxFileSize {
		return nil, 0, corrupt("%d bytes (max %d)", len(b), maxFileSize)
	}
	if !bytes.Equal(b[:8], []byte(FileMagic)) {
		return nil, 0, corrupt("no %q magic", FileMagic)
	}
	if n := binary.BigEndian.Uint32(b[0x1C:]); uint64(n) != uint64(len(b)-headerLen) {
		return nil, 0, corrupt("header says %d body bytes, %d follow (cut off, or not one file)", n, len(b)-headerLen)
	}
	if got, want := fileCRC(b), binary.BigEndian.Uint32(b[0x20:]); got != want {
		return nil, 0, corrupt("CRC 0x%08X, header says 0x%08X", got, want)
	}
	if v, flags := binary.BigEndian.Uint16(b[0x08:]), binary.BigEndian.Uint16(b[0x0A:]); v != FileVersion || flags != 0 {
		return nil, 0, fmt.Errorf("%w: version %d flags 0x%04X, this build reads version %d", ErrVersion, v, flags, FileVersion)
	}
	var id ID
	copy(id[:], b[0x0C:0x14])
	if id.IsZero() {
		return nil, 0, corrupt("zero world ID")
	}
	w := New(id, time.Time{})
	w.seq = binary.BigEndian.Uint64(b[0x14:])

	var img Image
	seen := map[uint16]bool{}
	stripped := 0
	for body := b[headerLen:]; len(body) > 0; {
		if len(body) < secHeaderLen {
			return nil, 0, corrupt("%d stray bytes after the last section", len(body))
		}
		tag, n := binary.BigEndian.Uint16(body), binary.BigEndian.Uint32(body[2:])
		if uint64(n) > uint64(len(body)-secHeaderLen) {
			return nil, 0, corrupt("section 0x%04X says %d bytes, %d follow", tag, n, len(body)-secHeaderLen)
		}
		val := body[secHeaderLen : secHeaderLen+int(n)]
		body = body[secHeaderLen+int(n):]
		if seen[tag] {
			return nil, 0, corrupt("section 0x%04X twice", tag)
		}
		seen[tag] = true
		switch tag {
		case secImage:
			if len(val) != ImageSize {
				return nil, 0, corrupt("image is %d bytes, want 0x%X", len(val), ImageSize)
			}
			copy(img[:], val)
		case secTombstones:
			if len(val)%tombLen != 0 {
				return nil, 0, corrupt("tombstones are %d bytes, not a multiple of %d", len(val), tombLen)
			}
			last := -1
			for e := val; len(e) > 0; e = e[tombLen:] {
				off, mask := int(binary.BigEndian.Uint16(e)), e[2]
				if off >= ImageSize || off <= last || mask == 0 {
					return nil, 0, corrupt("tombstone {0x%X, 0x%02X} after offset %d", off, mask, last)
				}
				last = off
				stripped += bitCount(mask &^ shared[off])
				w.tomb[off] = mask & shared[off]
			}
		case secFresh:
			if len(val)%freshLen != 0 || len(val)/freshLen > MaxFreshBinds {
				return nil, 0, corrupt("fresh binds are %d bytes (entries of %d, at most %d)", len(val), freshLen, MaxFreshBinds)
			}
			var last ID
			for e := val; len(e) > 0; e = e[freshLen:] {
				var n ID
				copy(n[:], e[:8])
				if n.IsZero() || bytes.Compare(n[:], last[:]) <= 0 {
					return nil, 0, corrupt("fresh bind %s after %s", n, last)
				}
				last = n
				w.fresh[n] = fromUnixMilli(int64(binary.BigEndian.Uint64(e[8:])))
			}
		case secMeta:
			if len(val) != metaLen {
				return nil, 0, corrupt("meta is %d bytes, want %d", len(val), metaLen)
			}
			w.created = fromUnixMilli(int64(binary.BigEndian.Uint64(val)))
		default:
			if tag&secOptional == 0 {
				return nil, 0, corrupt("unknown section 0x%04X", tag)
			}
		}
	}
	if !seen[secImage] {
		return nil, 0, corrupt("no image section")
	}
	for off := range img {
		keep := img[off] & shared[off] &^ w.tomb[off]
		stripped += bitCount(img[off] &^ keep)
		w.image[off] = keep
	}
	return w, stripped, nil
}

// sortedFresh lists the fresh binds by nonce, as the file keeps them.
func (w *World) sortedFresh() []FreshBind {
	fs := w.FreshBinds()
	slices.SortFunc(fs, func(a, b FreshBind) int { return bytes.Compare(a.Nonce[:], b.Nonce[:]) })
	return fs
}

// unixMilli is t in Unix milliseconds, with 0 for the zero time.
func unixMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// fromUnixMilli inverts unixMilli.
func fromUnixMilli(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

func bitCount(b byte) int {
	n := 0
	for ; b != 0; b &= b - 1 {
		n++
	}
	return n
}
