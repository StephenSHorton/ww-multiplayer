package inject

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

	// boot.bin (0x440) + bi2.bin (0x2000) are followed by the apploader:
	// a 0x20-byte header (date, entry point, code size at +0x14, trailer
	// size at +0x18) and then its code.
	isoApploaderOff     = 0x2440
	isoApploaderHdrSize = 0x20

	// Standard GameCube disc size. Dolphin's runtime DVD emulation rejects
	// game reads past it, so the relocated FST has to end below it.
	gcDiscSize = 0x57058000

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

// Result reports where PatchISO put the FST, for the CLI's status output.
type Result struct {
	OldFSTOffset uint32 // FST disc offset in the input
	NewFSTOffset uint32 // FST disc offset in the output; == OldFSTOffset if it didn't move
	FSTSize      uint32
}

// FSTMoved reports whether the FST had to be relocated to fit the bigger DOL.
func (r Result) FSTMoved() bool { return r.NewFSTOffset != r.OldFSTOffset }

// ErrOutputIsInput is returned by PatchISO when the output path names the
// input file. Patching in place would destroy the user's vanilla ISO.
var ErrOutputIsInput = errors.New("output is the input file")

// PatchISOInMemory takes the full bytes of a vanilla (or already-patched)
// Wind Waker ISO and returns a new []byte with the multiplayer mod spliced
// in. If the input DOL has enough reserved space (i.e. someone already ran
// `wit copy` + FST shift), the FST stays put and we only splice the DOL.
// Otherwise the FST is relocated into a gap that no file uses, to make
// room. Every write is checked against the file extents listed in the FST,
// so no file data is ever overwritten; if there's no safe gap, it errors.
//
// The output is always the same size as the input. Caller is expected to
// write it to a fresh path; we never mutate `iso` in place.
func PatchISOInMemory(iso []byte) ([]byte, error) {
	out, _, err := patchImage(iso)
	return out, err
}

func patchImage(iso []byte) ([]byte, Result, error) {
	if err := detectUnsupportedFormat(iso); err != nil {
		return nil, Result{}, err
	}
	if len(iso) < isoApploaderOff+isoApploaderHdrSize {
		return nil, Result{}, fmt.Errorf("iso: input too small (%d bytes)", len(iso))
	}
	if got := string(iso[isoHeaderGameID : isoHeaderGameID+6]); got != isoExpectedGameID {
		return nil, Result{}, fmt.Errorf("iso: unexpected game id %q (expected %q — this patcher is GZLE01-only)",
			got, isoExpectedGameID)
	}

	dolOff := binary.BigEndian.Uint32(iso[isoHeaderDOLOff:])
	fstOff := binary.BigEndian.Uint32(iso[isoHeaderFSTOff:])
	fstSize := binary.BigEndian.Uint32(iso[isoHeaderFSTSize:])

	if uint64(dolOff)+uint64(dolHeaderSize) > uint64(len(iso)) {
		return nil, Result{}, fmt.Errorf("iso: DOL offset 0x%x past end of file", dolOff)
	}
	if uint64(fstOff)+uint64(fstSize) > uint64(len(iso)) {
		return nil, Result{}, fmt.Errorf("iso: FST [0x%x..0x%x] past end of file (%d bytes)",
			fstOff, uint64(fstOff)+uint64(fstSize), len(iso))
	}
	if dolOff >= fstOff {
		return nil, Result{}, fmt.Errorf("iso: DOL offset 0x%x not before FST offset 0x%x", dolOff, fstOff)
	}

	// Vanilla DOL spans [dolOff, fstOff). It might be smaller than that range
	// if a prior `wit copy` reserved extra space for growth — the unused
	// tail is just zero padding. We feed only the actual DOL bytes (computed
	// from the DOL header) to PatchDOL so trailing padding doesn't get
	// included in the output.
	vanillaDOL, err := extractDOL(iso, dolOff, fstOff)
	if err != nil {
		return nil, Result{}, err
	}
	patchedDOL, err := PatchDOL(vanillaDOL)
	if err != nil {
		return nil, Result{}, err
	}

	// Everything on the disc that isn't free space: the system area, every
	// file the FST lists, and (below) the enlarged DOL. The old FST isn't
	// included — it's either left alone or vacated by the relocation.
	fst := iso[fstOff : uint64(fstOff)+uint64(fstSize)]
	files, err := fstFileExtents(fst)
	if err != nil {
		return nil, Result{}, fmt.Errorf("iso: %w", err)
	}
	apploaderEnd := uint64(isoApploaderOff+isoApploaderHdrSize) +
		uint64(binary.BigEndian.Uint32(iso[isoApploaderOff+0x14:])) +
		uint64(binary.BigEndian.Uint32(iso[isoApploaderOff+0x18:]))
	used := append([]extent{{start: 0, end: apploaderEnd, what: "the disc header/apploader"}}, files...)

	// The bytes the enlarged DOL newly claims (past the end of the vanilla
	// DOL) must not belong to anything else.
	dolEnd := uint64(dolOff) + uint64(len(patchedDOL))
	growth := extent{start: uint64(dolOff) + uint64(len(vanillaDOL)), end: dolEnd}
	for _, u := range used {
		if growth.overlaps(u) {
			return nil, Result{}, fmt.Errorf("iso: the patched DOL [0x%x..0x%x] would overwrite %s [0x%x..0x%x]; "+
				"this disc layout isn't one the patcher knows, please report it",
				dolOff, dolEnd, u.what, u.start, u.end)
		}
	}

	res := Result{OldFSTOffset: fstOff, NewFSTOffset: fstOff, FSTSize: fstSize}
	out := make([]byte, len(iso))
	copy(out, iso)

	if dolEnd <= uint64(fstOff) {
		// Fast path: patched DOL fits in existing reserved region, FST
		// stays put. Just splice the new DOL.
		copy(out[dolOff:], patchedDOL)
		return out, res, nil
	}

	// Slow path: patched DOL is larger than the reserved region — FST
	// would be clobbered. Relocate FST, preferring wwSafeFSTOff (0x500000),
	// the known spot in vanilla WW that keeps the FST inside the standard
	// 1.46 GB Gamecube disc size cap.
	used = append(used, extent{start: uint64(dolOff), end: dolEnd, what: "the patched DOL"})
	newFSTOff, err := chooseFSTRelocation(used, fstSize, dolEnd, uint64(len(iso)))
	if err != nil {
		return nil, Result{}, err
	}
	copy(out[newFSTOff:], fst)
	copy(out[dolOff:], patchedDOL)

	// Update header pointers for the relocated FST.
	binary.BigEndian.PutUint32(out[isoHeaderFSTOff:], newFSTOff)
	if binary.BigEndian.Uint32(out[isoHeaderFSTMax:]) < fstSize {
		binary.BigEndian.PutUint32(out[isoHeaderFSTMax:], fstSize)
	}
	res.NewFSTOffset = newFSTOff
	return out, res, nil
}

// chooseFSTRelocation picks where the relocated FST should land: a 64 KB
// aligned range past the end of the patched DOL (dolEnd), inside both the
// image and the standard disc size, that overlaps nothing in used.
//  1. Prefer wwSafeFSTOff (0x500000) — the slot vanilla WW leaves free, and
//     the one the patcher has always used. Junk padding there (raw 1:1
//     dumps keep it; scrubbed images and CISOs have zeros) doesn't matter:
//     no file lives there, so nothing reads those bytes.
//  2. Otherwise the lowest free aligned gap.
//  3. No gap: refuse. Appending past EOF/the disc size cap breaks the game
//     at runtime, so there's no point producing that image.
func chooseFSTRelocation(used []extent, fstSize uint32, dolEnd, imageSize uint64) (uint32, error) {
	limit := min(imageSize, gcDiscSize)
	off, ok := findFreeSlot(used, uint64(fstSize), isoFSTAlignment, dolEnd, limit, wwSafeFSTOff)
	if !ok {
		return 0, fmt.Errorf("iso: no free space found to relocate the FST (0x%x bytes, between 0x%x and 0x%x); please report this",
			fstSize, dolEnd, limit)
	}
	return uint32(off), nil
}

// dolMaxSize bounds the DOL size a header may declare before we allocate
// for it. Every section has to load into the GameCube's 24 MiB of MEM1, so
// a real DOL is far smaller (vanilla GZLE01's is 0x3A52C0 bytes); a header
// claiming more is corrupt or crafted.
const dolMaxSize = 24 << 20

// extractDOL returns just the bytes of the DOL whose header lives at
// iso[dolOff], computed from the DOL header itself rather than relying on
// the gap to the FST. This protects against ISOs that were `wit copy`'d
// with extra reserved space — we don't want trailing zero padding fed
// into PatchDOL, since PatchDOL appends our T2 section to whatever it
// receives and the padding would shift our T2 to a wrong file offset.
func extractDOL(iso []byte, dolOff, capOff uint32) ([]byte, error) {
	if uint64(dolOff)+dolHeaderSize > uint64(len(iso)) {
		return nil, fmt.Errorf("dol: header runs past end of iso")
	}
	hdr := iso[dolOff : dolOff+dolHeaderSize]

	// Compute the DOL's actual size: max(section_file_off + section_size)
	// across all 7 text + 11 data sections, clamped to the header's BSS
	// region (BSS isn't in-file). Largest end-of-section is the file end.
	// 64-bit sums: a crafted header can't wrap them past the checks below.
	var maxEnd uint64 = dolHeaderSize
	for i := 0; i < 7; i++ {
		off := uint64(binary.BigEndian.Uint32(hdr[0x00+4*i:]))
		size := uint64(binary.BigEndian.Uint32(hdr[0x90+4*i:]))
		if off != 0 && off+size > maxEnd {
			maxEnd = off + size
		}
	}
	for i := 0; i < 11; i++ {
		off := uint64(binary.BigEndian.Uint32(hdr[0x1C+4*i:]))
		size := uint64(binary.BigEndian.Uint32(hdr[0xAC+4*i:]))
		if off != 0 && off+size > maxEnd {
			maxEnd = off + size
		}
	}

	if maxEnd > dolMaxSize {
		return nil, fmt.Errorf("dol: header declares 0x%x bytes of sections, more than fits in GameCube RAM (0x%x); corrupt DOL?",
			maxEnd, dolMaxSize)
	}
	if uint64(dolOff)+maxEnd > uint64(capOff) {
		return nil, fmt.Errorf("dol: declared end (0x%x) past FST offset (0x%x)",
			uint64(dolOff)+maxEnd, capOff)
	}
	if uint64(dolOff)+maxEnd > uint64(len(iso)) {
		return nil, fmt.Errorf("dol: declared end (0x%x) past EOF (%d)", uint64(dolOff)+maxEnd, len(iso))
	}
	out := make([]byte, maxEnd)
	copy(out, iso[dolOff:uint64(dolOff)+maxEnd])
	return out, nil
}

// PatchISO is the file-on-disk wrapper around PatchISOInMemory. Reads the
// input fully into memory, patches it, writes the result to outPath.
// CISO inputs are detected by magic and decompressed transparently.
//
// The input is never modified: an outPath naming the input file is refused
// (ErrOutputIsInput), and the output is written to a temp file next to it
// and renamed into place, so a failed or interrupted run never leaves a
// truncated ISO behind.
func PatchISO(inPath, outPath string) (Result, error) {
	if sameFile(inPath, outPath) {
		return Result{}, fmt.Errorf("%w: %s; choose a different output path (the patcher never overwrites your original)",
			ErrOutputIsInput, outPath)
	}
	data, err := os.ReadFile(inPath)
	if err != nil {
		return Result{}, fmt.Errorf("read %s: %w", inPath, err)
	}
	if isCISO(data) {
		data, err = decompressCISO(data)
		if err != nil {
			return Result{}, fmt.Errorf("decompress CISO: %w", err)
		}
	}
	patched, res, err := patchImage(data)
	if err != nil {
		return Result{}, err
	}
	if err := writeFileAtomic(outPath, patched, 0o644); err != nil {
		return Result{}, fmt.Errorf("write %s: %w", outPath, err)
	}
	return res, nil
}

// sameFile reports whether paths a and b name the same file: the same
// cleaned absolute path, or (when both exist) the same file on disk, which
// also catches case-only differences on Windows/macOS and links.
func sameFile(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA == nil && errB == nil && absA == absB {
		return true
	}
	stA, errA := os.Stat(a)
	stB, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(stA, stB)
}

// writeFileAtomic writes data to a temp file in path's directory and
// renames it over path, so path holds either its old content or all of
// data — never a partial write.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	committed := false
	defer func() {
		if !committed {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	committed = true
	return nil
}

// Detect CISO by 4-byte magic.
func isCISO(b []byte) bool {
	return len(b) >= 4 && bytes.Equal(b[:4], []byte("CISO"))
}
