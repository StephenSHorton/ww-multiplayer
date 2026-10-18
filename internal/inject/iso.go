package inject

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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

// Result reports where PatchISO put the FST (and RELS.arc, when one was
// given), for the CLI's status output.
type Result struct {
	OldFSTOffset uint32 // FST disc offset in the input
	NewFSTOffset uint32 // FST disc offset in the output; == OldFSTOffset if it didn't move
	FSTSize      uint32

	// RELSArcOffset and RELSArcSize are where Patch.RELSArc lies on the
	// output disc, where the FST's entry for RELS.arc now points; zero
	// when no replacement archive was given.
	RELSArcOffset uint32
	RELSArcSize   uint32
}

// FSTMoved reports whether the FST had to be relocated to fit the bigger DOL.
func (r Result) FSTMoved() bool { return r.NewFSTOffset != r.OldFSTOffset }

// RELSArcReplaced reports whether a replacement RELS.arc went on the disc.
func (r Result) RELSArcReplaced() bool { return r.RELSArcSize != 0 }

// Patch is what the patcher splices into the disc besides the DOL patch
// blob.go carries. The zero value is PatchISO's: the DOL only.
type Patch struct {
	// RELSArc is a replacement for the disc's RELS.arc (the archive of
	// the game's relocatable modules, with the mod's REL in it:
	// internal/inject/rels builds one), or nil to leave the disc's. It
	// is bigger than the vanilla one, so it goes in free space below the
	// disc-size cap and the FST's entry for "RELS.arc" is pointed at it;
	// the vanilla bytes stay where they were, unreferenced.
	RELSArc []byte
}

// relsArcName is the archive's name in the FST's root directory, as the
// game opens it (DynamicLink.cpp mounts "/RELS.arc"; the DVD library
// compares names ASCII case-insensitively).
const relsArcName = "RELS.arc"

// relsArcAlignments are the alignments tried for the replacement
// RELS.arc's disc offset, in order. The DVD library needs 4-byte file
// offsets at most (and the vanilla disc's files are placed at 4), so
// 0x20 (one DVD DMA unit, the finer one tried) is already generous;
// 0x8000, a DVD sector, is preferred as the tidier layout and what
// authoring tools use, and the vanilla disc has hundreds of MB free
// past the DOL to give it.
var relsArcAlignments = []uint64{0x8000, 0x20}

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
// The output is the same size as the input, except for a trimmed image with
// no free gap inside it: there the FST is appended past its end, which grows
// the output (always staying under the disc size cap). Caller is expected to
// write it to a fresh path; we never mutate `iso` in place.
func PatchISOInMemory(iso []byte) ([]byte, error) {
	out, _, err := patchImage(iso, Patch{})
	return out, err
}

// PatchISOInMemoryWith is PatchISOInMemory with p's extras spliced in
// too (see Patch), and the result's placements. With a Patch.RELSArc,
// an image that already holds this build's DOL patch is ErrAlreadyPatched
// only when it holds that archive as well (hasCurrentRELSArc); with
// another RELS.arc it is ErrPatchedByOtherVersion.
func PatchISOInMemoryWith(iso []byte, p Patch) ([]byte, Result, error) {
	return patchImage(iso, p)
}

func patchImage(iso []byte, p Patch) ([]byte, Result, error) {
	if p.RELSArc != nil && !bytes.HasPrefix(p.RELSArc, []byte("RARC")) {
		return nil, Result{}, fmt.Errorf("iso: the replacement RELS.arc isn't a RARC archive (%d bytes)", len(p.RELSArc))
	}
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
	fst := iso[fstOff : uint64(fstOff)+uint64(fstSize)]
	patchedDOL, err := PatchDOL(vanillaDOL)
	if err != nil {
		if errors.Is(err, ErrAlreadyPatched) && p.RELSArc != nil {
			// The DOL is this build's; the disc is only when its
			// RELS.arc is this build's too.
			current, cerr := hasCurrentRELSArc(iso, fst, p.RELSArc)
			switch {
			case cerr != nil:
				return nil, Result{}, fmt.Errorf("iso: %w", cerr)
			case !current:
				return nil, Result{}, fmt.Errorf("%w (its RELS.arc isn't this build's)", ErrPatchedByOtherVersion)
			}
		}
		return nil, Result{}, err
	}

	// Everything on the disc that isn't free space: the system area, every
	// file the FST lists, and (below) the enlarged DOL. The old FST isn't
	// included — it's either left alone or vacated by the relocation.
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
	dolExtent := extent{start: uint64(dolOff), end: dolEnd, what: "the patched DOL"}

	// Fast path: the patched DOL fits in the existing reserved region and
	// the FST stays put. Otherwise the FST would be clobbered: relocate
	// it, preferring wwSafeFSTOff (0x500000), the known spot in vanilla
	// WW that keeps the FST inside the standard 1.46 GB Gamecube disc
	// size cap.
	moveFST := dolEnd > uint64(fstOff)
	newFSTOff := fstOff
	if moveFST {
		used = append(used, dolExtent)
		if newFSTOff, err = chooseFSTRelocation(used, fstSize, dolEnd, uint64(len(iso))); err != nil {
			return nil, Result{}, err
		}
		res.NewFSTOffset = newFSTOff
	}

	// The replacement RELS.arc: in the vanilla archive's own extent when
	// it fits there, else in free space (below the FST wherever it ends
	// up, the patched DOL and every file), with the FST's entry pointed
	// at it. fstOut is the FST as written: the input's, or a copy with
	// that entry rewritten.
	fstOut := fst
	if p.RELSArc != nil {
		idx, e, err := fstRootFile(fst, relsArcName)
		if err != nil {
			return nil, Result{}, fmt.Errorf("iso: %w", err)
		}
		size := uint64(len(p.RELSArc))
		relsOff := uint64(e.w1)
		if size > uint64(e.w2) {
			reserved := append(slices.Clone(used), extent{start: uint64(newFSTOff), end: uint64(newFSTOff) + uint64(fstSize), what: "the FST"})
			if !moveFST {
				reserved = append(reserved, dolExtent)
			}
			if relsOff, err = chooseRELSArcSlot(reserved, size, dolEnd, uint64(len(iso))); err != nil {
				return nil, Result{}, err
			}
		}
		fstOut = bytes.Clone(fst)
		binary.BigEndian.PutUint32(fstOut[idx*fstEntrySize+4:], uint32(relsOff))
		binary.BigEndian.PutUint32(fstOut[idx*fstEntrySize+8:], uint32(size))
		res.RELSArcOffset, res.RELSArcSize = uint32(relsOff), uint32(size)
	}

	out := make([]byte, len(iso))
	copy(out, iso)
	end := max(uint64(newFSTOff)+uint64(fstSize), uint64(res.RELSArcOffset)+uint64(res.RELSArcSize))
	if end > uint64(len(out)) {
		out = append(out, make([]byte, end-uint64(len(out)))...) // the EOF append
	}
	copy(out[dolOff:], patchedDOL)
	if moveFST || p.RELSArc != nil {
		copy(out[newFSTOff:], fstOut)
	}
	if moveFST {
		// Update header pointers for the relocated FST.
		binary.BigEndian.PutUint32(out[isoHeaderFSTOff:], newFSTOff)
		if binary.BigEndian.Uint32(out[isoHeaderFSTMax:]) < fstSize {
			binary.BigEndian.PutUint32(out[isoHeaderFSTMax:], fstSize)
		}
	}
	if p.RELSArc != nil {
		copy(out[res.RELSArcOffset:], p.RELSArc)
	}
	return out, res, nil
}

// chooseRELSArcSlot picks where the replacement RELS.arc goes: the
// lowest free slot past the patched DOL (dolEnd) that overlaps nothing
// in used and ends below the disc size cap, at the first of
// relsArcAlignments that has one; failing that, past the end of a
// trimmed image, still under the cap (the game's DVD reads past the cap
// fail, see wwSafeFSTOff); failing that, an error.
func chooseRELSArcSlot(used []extent, size, dolEnd, imageSize uint64) (uint64, error) {
	limit := min(imageSize, gcDiscSize)
	for _, align := range relsArcAlignments {
		if off, ok := findFreeSlot(used, size, align, dolEnd, limit, 0); ok {
			return off, nil
		}
	}
	if imageSize < gcDiscSize {
		for _, align := range relsArcAlignments {
			if off, ok := findFreeSlot(used, size, align, max(dolEnd, imageSize), gcDiscSize, 0); ok {
				return off, nil
			}
		}
	}
	return 0, fmt.Errorf("iso: no free space found for the %d-byte RELS.arc (between 0x%x and the disc size cap 0x%x); please report this",
		size, dolEnd, uint64(gcDiscSize))
}

// hasCurrentRELSArc reports whether the FST's RELS.arc on iso is arc,
// byte for byte: the size the entry gives and the bytes at its offset.
// What a build writes there is deterministic (the archive is rebuilt
// from the vanilla one and the same REL), so an image patched by this
// build matches and one by any other build doesn't.
func hasCurrentRELSArc(iso, fst, arc []byte) (bool, error) {
	_, e, err := fstRootFile(fst, relsArcName)
	if err != nil {
		return false, err
	}
	off, size := uint64(e.w1), uint64(e.w2)
	if size != uint64(len(arc)) || off+size > uint64(len(iso)) {
		return false, nil
	}
	return bytes.Equal(iso[off:off+size], arc), nil
}

// chooseFSTRelocation picks where the relocated FST should land: a 64 KB
// aligned range past the end of the patched DOL (dolEnd), below the standard
// disc size, that overlaps nothing in used.
//  1. Prefer wwSafeFSTOff (0x500000) — the slot vanilla WW leaves free, and
//     the one the patcher has always used. Junk padding there (raw 1:1
//     dumps keep it; scrubbed images and CISOs have zeros) doesn't matter:
//     no file lives there, so nothing reads those bytes.
//  2. Otherwise the lowest free aligned gap.
//  3. No gap inside the image, but the image is smaller than a full disc (a
//     trimmed dump): append past EOF, as the patcher always has for these.
//     The FST still ends below the disc size cap, so the game reads it.
//  4. Otherwise refuse. Past the disc size cap the game's file mounts fail
//     at runtime (see wwSafeFSTOff), so there's no point producing that image.
func chooseFSTRelocation(used []extent, fstSize uint32, dolEnd, imageSize uint64) (uint32, error) {
	limit := min(imageSize, gcDiscSize)
	off, ok := findFreeSlot(used, uint64(fstSize), isoFSTAlignment, dolEnd, limit, wwSafeFSTOff)
	if !ok && imageSize < gcDiscSize {
		off, ok = findFreeSlot(used, uint64(fstSize), isoFSTAlignment, max(dolEnd, imageSize), gcDiscSize, 0)
	}
	if !ok {
		return 0, fmt.Errorf("iso: no free space found to relocate the FST (0x%x bytes, between 0x%x and the disc size cap 0x%x); please report this",
			fstSize, dolEnd, uint64(gcDiscSize))
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
	maxEnd, err := dolFileSize(iso[dolOff : dolOff+dolHeaderSize])
	if err != nil {
		return nil, err
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

// dolFileSize returns the size of the DOL whose 0x100-byte header is hdr:
// max(section_file_off + section_size) across all 7 text + 11 data
// sections. BSS isn't in-file, so the largest end-of-section is the file
// end. 64-bit sums: a crafted header can't wrap them past the cap.
func dolFileSize(hdr []byte) (uint64, error) {
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
		return 0, fmt.Errorf("dol: header declares 0x%x bytes of sections, more than fits in GameCube RAM (0x%x); corrupt DOL?",
			maxEnd, dolMaxSize)
	}
	return maxEnd, nil
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
	return PatchISOWith(inPath, outPath, Patch{})
}

// PatchISOWith is PatchISO with p's extras spliced in too (see Patch
// and PatchISOInMemoryWith).
func PatchISOWith(inPath, outPath string, p Patch) (Result, error) {
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
	patched, res, err := patchImage(data, p)
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
// data — never a partial write. Apart from that it behaves like
// os.WriteFile: a symlink at path is followed (its target is replaced and
// the link stays a link), an existing file keeps its permission bits, and a
// new file gets perm minus the umask. A hard link to the old file is the
// one difference: it keeps the old content.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf("resolve symlink: %w", err)
		}
		path = target
	}
	f, err := createTempNear(path, perm)
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
	if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() {
		if err := os.Chmod(tmp, st.Mode().Perm()); err != nil {
			return err
		}
	}
	if err := replaceFile(tmp, path); err != nil {
		return err
	}
	committed = true
	return nil
}

// createTempNear creates a new file named <path>.<random>.tmp, so it sits
// in path's directory (and on its filesystem, for the rename). Unlike
// os.CreateTemp's fixed 0600 it is opened with perm, which the umask then
// trims exactly as it would for os.WriteFile.
func createTempNear(path string, perm os.FileMode) (*os.File, error) {
	for range 100 {
		name := path + "." + strconv.FormatUint(uint64(rand.Uint32()), 10) + ".tmp"
		f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, perm)
		if !errors.Is(err, fs.ErrExist) {
			return f, err
		}
	}
	return nil, fmt.Errorf("create temp file next to %s: too many name collisions", path)
}

// Detect CISO by 4-byte magic.
func isCISO(b []byte) bool {
	return len(b) >= 4 && bytes.Equal(b[:4], []byte("CISO"))
}
