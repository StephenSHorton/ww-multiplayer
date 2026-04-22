package inject

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
)

// Gamecube disc header offsets we care about.
const (
	isoHeaderGameID   = 0x000
	isoHeaderTitle    = 0x020
	isoHeaderDOLOff   = 0x420
	isoHeaderFSTOff   = 0x424
	isoHeaderFSTSize  = 0x428
	isoHeaderFSTMax   = 0x42C
	isoHeaderEnd      = 0x440
	isoExpectedGameID = "GZLE01"
	isoFSTAlignment   = 0x10000 // align relocated FST to 64 KB

	// Preferred relocation target for the FST in vanilla Wind Waker NTSC-U.
	// Empirically, the bytes at this disc offset are zero padding between
	// files in the vanilla layout — verified live, and it's what the user's
	// own `wit copy + python FST shift` workflow lands on. Placing the FST
	// here keeps it inside the standard 1.46 GB Gamecube disc size limit
	// (0x57058000), which matters because Dolphin's runtime DVD emulation
	// rejects file reads from offsets past that limit at game runtime
	// (even though apploader-time reads ignore the cap). Putting the FST
	// past EOF "works" through boot but the game's mDoDvdThd file mounts
	// fail later — RELS.arc / f_pc_profile_lst load errors and a NULL
	// deref in the actor profile linker.
	wwSafeFSTOff = 0x500000
)

// PatchISOInMemory takes the full bytes of a vanilla (or already-patched)
// Wind Waker ISO and returns a new []byte with the multiplayer mod spliced
// in. If the input DOL has enough reserved space (i.e. someone already ran
// `wit copy` + FST shift), the FST stays put and we only splice the DOL.
// Otherwise the FST is relocated to the end of the file to make room.
//
// The output may be larger than the input (FST relocation appends to EOF).
// Caller is expected to write it to a fresh path; we never mutate `iso`
// in place.
func PatchISOInMemory(iso []byte) ([]byte, error) {
	if len(iso) < isoHeaderEnd {
		return nil, fmt.Errorf("iso: input too small (%d bytes)", len(iso))
	}
	if got := string(iso[isoHeaderGameID : isoHeaderGameID+6]); got != isoExpectedGameID {
		return nil, fmt.Errorf("iso: unexpected game id %q (expected %q — this patcher is GZLE01-only)",
			got, isoExpectedGameID)
	}

	dolOff := binary.BigEndian.Uint32(iso[isoHeaderDOLOff:])
	fstOff := binary.BigEndian.Uint32(iso[isoHeaderFSTOff:])
	fstSize := binary.BigEndian.Uint32(iso[isoHeaderFSTSize:])

	if uint64(dolOff)+uint64(dolHeaderSize) > uint64(len(iso)) {
		return nil, fmt.Errorf("iso: DOL offset 0x%x past end of file", dolOff)
	}
	if uint64(fstOff)+uint64(fstSize) > uint64(len(iso)) {
		return nil, fmt.Errorf("iso: FST [0x%x..0x%x] past end of file (%d bytes)",
			fstOff, uint64(fstOff)+uint64(fstSize), len(iso))
	}
	if dolOff >= fstOff {
		return nil, fmt.Errorf("iso: DOL offset 0x%x not before FST offset 0x%x", dolOff, fstOff)
	}

	// Vanilla DOL spans [dolOff, fstOff). It might be smaller than that range
	// if a prior `wit copy` reserved extra space for growth — the unused
	// tail is just zero padding. We feed only the actual DOL bytes (computed
	// from the DOL header) to PatchDOL so trailing padding doesn't get
	// included in the output.
	vanillaDOL, err := extractDOL(iso, dolOff, fstOff)
	if err != nil {
		return nil, err
	}
	patchedDOL, err := PatchDOL(vanillaDOL)
	if err != nil {
		return nil, err
	}

	out := make([]byte, len(iso))
	copy(out, iso)

	reserved := fstOff - dolOff
	if uint32(len(patchedDOL)) <= reserved {
		// Fast path: patched DOL fits in existing reserved region, FST
		// stays put. Just splice the new DOL.
		copy(out[dolOff:dolOff+uint32(len(patchedDOL))], patchedDOL)
		return out, nil
	}

	// Slow path: patched DOL is larger than the reserved region — FST
	// would be clobbered. Relocate FST. Prefer wwSafeFSTOff (0x500000),
	// the empirically-known zero-padded region in vanilla WW that keeps
	// the FST inside the standard 1.46 GB Gamecube disc size cap. Only
	// fall back to other strategies if 0x500000 is occupied.
	newFSTOff, err := chooseFSTRelocation(iso, fstSize, dolOff, uint32(len(patchedDOL)))
	if err != nil {
		return nil, err
	}
	fstBytes := make([]byte, fstSize)
	copy(fstBytes, iso[fstOff:fstOff+fstSize])

	// Extend `out` if the new FST location is past current EOF (only
	// happens on the EOF-append fallback path).
	if int(newFSTOff)+len(fstBytes) > len(out) {
		grown := make([]byte, int(newFSTOff)+len(fstBytes))
		copy(grown, out)
		out = grown
	}
	copy(out[newFSTOff:int(newFSTOff)+len(fstBytes)], fstBytes)

	// Now splice patched DOL — its reserved space is now (newFSTOff - dolOff).
	if uint32(len(patchedDOL)) > newFSTOff-dolOff {
		return nil, fmt.Errorf("iso: patched DOL (%d bytes) doesn't fit in (%d) reserved after relocating FST to 0x%x",
			len(patchedDOL), newFSTOff-dolOff, newFSTOff)
	}
	copy(out[dolOff:dolOff+uint32(len(patchedDOL))], patchedDOL)

	// Update header pointers for the relocated FST.
	binary.BigEndian.PutUint32(out[isoHeaderFSTOff:], newFSTOff)
	if binary.BigEndian.Uint32(out[isoHeaderFSTMax:]) < fstSize {
		binary.BigEndian.PutUint32(out[isoHeaderFSTMax:], fstSize)
	}

	return out, nil
}

// chooseFSTRelocation picks where the relocated FST should land. Strategy:
//  1. Prefer wwSafeFSTOff (0x500000) — known-zero region in vanilla WW that
//     stays inside Dolphin's runtime disc-size cap. Verify the destination
//     is actually all zero so we can't ever overwrite a file.
//  2. If 0x500000 is occupied (custom-laid-out ISO?), fall back to scanning
//     forward for any 64KB-aligned zero region of the right size.
//  3. As last resort, append to EOF — which "works" through apploader but
//     usually fails at game runtime if past 0x57058000. We log a warning
//     in this case but don't refuse, so the user has SOMETHING to work
//     with even on weird ISOs.
func chooseFSTRelocation(iso []byte, fstSize, dolOff, newDOLSize uint32) (uint32, error) {
	needed := uint32(fstSize)

	// Candidate 1: WW's known-good slot.
	if wwSafeFSTOff+needed <= uint32(len(iso)) &&
		wwSafeFSTOff > dolOff+newDOLSize &&
		isAllZero(iso[wwSafeFSTOff:wwSafeFSTOff+needed]) {
		return wwSafeFSTOff, nil
	}

	// Candidate 2: scan forward from a safe lower bound for a zero region.
	// Lower bound = max(end of new DOL, end of original FST) aligned up.
	lo := alignUp(maxU32(dolOff+newDOLSize, alignUp(0, isoFSTAlignment)), isoFSTAlignment)
	for off := lo; off+needed <= uint32(len(iso)); off += isoFSTAlignment {
		if isAllZero(iso[off : off+needed]) {
			return off, nil
		}
	}

	// Candidate 3: append to EOF. Likely to fail at runtime (past disc cap)
	// but we honor the request rather than refuse.
	return alignUp(uint32(len(iso)), isoFSTAlignment), nil
}

func isAllZero(b []byte) bool {
	// Fast-ish path: check 8 bytes at a time, then tail.
	i := 0
	for ; i+8 <= len(b); i += 8 {
		if binary.BigEndian.Uint64(b[i:i+8]) != 0 {
			return false
		}
	}
	for ; i < len(b); i++ {
		if b[i] != 0 {
			return false
		}
	}
	return true
}

func maxU32(a, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}

// extractDOL returns just the bytes of the DOL whose header lives at
// iso[dolOff], computed from the DOL header itself rather than relying on
// the gap to the FST. This protects against ISOs that were `wit copy`'d
// with extra reserved space — we don't want trailing zero padding fed
// into PatchDOL, since PatchDOL appends our T2 section to whatever it
// receives and the padding would shift our T2 to a wrong file offset.
func extractDOL(iso []byte, dolOff, capOff uint32) ([]byte, error) {
	if dolOff+dolHeaderSize > uint32(len(iso)) {
		return nil, fmt.Errorf("dol: header runs past end of iso")
	}
	hdr := iso[dolOff : dolOff+dolHeaderSize]

	// Compute the DOL's actual size: max(section_file_off + section_size)
	// across all 7 text + 11 data sections, clamped to the header's BSS
	// region (BSS isn't in-file). Largest end-of-section is the file end.
	var maxEnd uint32 = dolHeaderSize
	for i := 0; i < 7; i++ {
		off := binary.BigEndian.Uint32(hdr[0x00+4*i:])
		size := binary.BigEndian.Uint32(hdr[0x90+4*i:])
		if off != 0 && off+size > maxEnd {
			maxEnd = off + size
		}
	}
	for i := 0; i < 11; i++ {
		off := binary.BigEndian.Uint32(hdr[0x1C+4*i:])
		size := binary.BigEndian.Uint32(hdr[0xAC+4*i:])
		if off != 0 && off+size > maxEnd {
			maxEnd = off + size
		}
	}

	if dolOff+maxEnd > capOff {
		return nil, fmt.Errorf("dol: declared end (0x%x) past FST offset (0x%x)",
			dolOff+maxEnd, capOff)
	}
	if dolOff+maxEnd > uint32(len(iso)) {
		return nil, fmt.Errorf("dol: declared end (0x%x) past EOF (%d)", dolOff+maxEnd, len(iso))
	}
	out := make([]byte, maxEnd)
	copy(out, iso[dolOff:dolOff+maxEnd])
	return out, nil
}

// PatchISO is the file-on-disk wrapper around PatchISOInMemory. Reads the
// input fully into memory, patches it, writes the result to outPath.
// CISO inputs are detected by magic and decompressed transparently.
func PatchISO(inPath, outPath string) error {
	data, err := os.ReadFile(inPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", inPath, err)
	}
	if isCISO(data) {
		data, err = decompressCISO(data)
		if err != nil {
			return fmt.Errorf("decompress CISO: %w", err)
		}
	}
	patched, err := PatchISOInMemory(data)
	if err != nil {
		return err
	}
	return os.WriteFile(outPath, patched, 0o644)
}

func alignUp(v, align uint32) uint32 {
	return (v + align - 1) / align * align
}

// Detect CISO by 4-byte magic.
func isCISO(b []byte) bool {
	return len(b) >= 4 && bytes.Equal(b[:4], []byte("CISO"))
}
