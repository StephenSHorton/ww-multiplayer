package inject

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"fmt"
	"slices"
	"strconv"
)

// GameCube FST ("file system table") layout: an array of 12-byte entries
// followed by a NUL-terminated string table. Entry 0 is the root directory,
// and its last word is the total entry count (root included). Each entry:
//
//	byte  0      flags: 0 = file, anything else = directory
//	bytes 1..3   name offset into the string table
//	bytes 4..7   file: disc offset    / directory: parent entry index
//	bytes 8..11  file: length (bytes) / directory: index of the first entry past it
//
// (FSTEntry and the entryIsDir/stringOff macros, dvdfs.c:5-9,42-47 in the
// zeldaret/tww decomp.) GameCube offsets are plain byte offsets (Wii discs
// shift them by 2; not relevant here). The patcher never moves files, so
// the file extents it reads from the input FST are also the extents in the
// output.
const fstEntrySize = 12

// extent is a half-open byte range [start, end) of the disc image. It's
// 64-bit so offset+size sums of 32-bit header fields can't wrap.
type extent struct {
	start, end uint64
	what       string // for error messages: `file "RELS.arc"`, "apploader", ...
}

func (e extent) overlaps(o extent) bool {
	return e.start < o.end && o.start < e.end
}

// fstEntry is one decoded FST entry.
type fstEntry struct {
	dir     bool
	nameOff uint32 // into the string table
	w1, w2  uint32 // file: disc offset, length / directory: parent index, next index
}

// fstEntryAt decodes entry i. The caller has checked that it lies in fst.
func fstEntryAt(fst []byte, i uint64) fstEntry {
	e := fst[i*fstEntrySize : (i+1)*fstEntrySize]
	return fstEntry{
		dir:     e[0] != 0,
		nameOff: binary.BigEndian.Uint32(e[0:]) & 0x00FFFFFF,
		w1:      binary.BigEndian.Uint32(e[4:]),
		w2:      binary.BigEndian.Uint32(e[8:]),
	}
}

// fstRoot checks the root entry and returns the entry count it declares
// and the string table that follows the entries.
func fstRoot(fst []byte) (n uint64, names []byte, err error) {
	if len(fst) < fstEntrySize {
		return 0, nil, fmt.Errorf("fst: too small (%d bytes)", len(fst))
	}
	if fst[0] == 0 {
		return 0, nil, fmt.Errorf("fst: root entry is not a directory")
	}
	n = uint64(binary.BigEndian.Uint32(fst[8:]))
	if n == 0 || n*fstEntrySize > uint64(len(fst)) {
		return 0, nil, fmt.Errorf("fst: root declares %d entries, but the FST is only %d bytes", n, len(fst))
	}
	return n, fst[n*fstEntrySize:], nil
}

// fstFileExtents parses an FST and returns the disc extent of every
// non-empty file it lists. Only the structure needed to locate file data is
// validated; names are read best-effort and only feed error messages.
func fstFileExtents(fst []byte) ([]extent, error) {
	n, names, err := fstRoot(fst)
	if err != nil {
		return nil, err
	}

	var files []extent
	for i := uint64(1); i < n; i++ {
		e := fstEntryAt(fst, i)
		if e.dir {
			continue
		}
		off, size := uint64(e.w1), uint64(e.w2)
		if size == 0 {
			continue
		}
		files = append(files, extent{
			start: off,
			end:   off + size,
			what:  "file " + fstName(names, e.nameOff, i),
		})
	}
	return files, nil
}

// fstName returns the quoted name at nameOff in the string table, or the
// entry index when the offset doesn't point anywhere sensible.
func fstName(names []byte, nameOff uint32, idx uint64) string {
	s, _, ok := fstString(names, nameOff)
	if !ok {
		return fmt.Sprintf("#%d", idx)
	}
	if len(s) > 64 {
		s = s[:64]
	}
	return strconv.Quote(string(s))
}

// fstString returns the string at off in the string table: the bytes up to
// its NUL, or up to the end of the table if it has none (terminated false).
// ok is false when off is past the table.
func fstString(names []byte, off uint32) (s []byte, terminated, ok bool) {
	if uint64(off) >= uint64(len(names)) {
		return nil, false, false
	}
	s = names[off:]
	if i := bytes.IndexByte(s, 0); i >= 0 {
		return s[:i], true, true
	}
	return s, false, true
}

// findFreeSlot returns an offset in [lo, limit) where size bytes fit without
// overlapping any extent in used. prefer is tried first; otherwise the
// lowest align-aligned offset that fits wins. Free space is decided purely
// by the extents: whatever bytes currently sit there (zeros or junk
// padding) are fair game.
func findFreeSlot(used []extent, size, align, lo, limit, prefer uint64) (uint64, bool) {
	fits := func(off uint64) bool {
		if off < lo || off+size > limit {
			return false
		}
		want := extent{start: off, end: off + size}
		for _, u := range used {
			if u.overlaps(want) {
				return false
			}
		}
		return true
	}
	if fits(prefer) {
		return prefer, true
	}

	sorted := slices.Clone(used)
	slices.SortFunc(sorted, func(a, b extent) int { return cmp.Compare(a.start, b.start) })
	cursor := lo // everything below cursor is either < lo or used
	for _, u := range sorted {
		if u.start >= u.end || u.end <= cursor {
			continue
		}
		if off := alignUp64(cursor, align); off+size <= u.start && off+size <= limit {
			return off, true
		}
		cursor = u.end
	}
	if off := alignUp64(cursor, align); off+size <= limit {
		return off, true
	}
	return 0, false
}

func alignUp64(v, align uint64) uint64 {
	return (v + align - 1) / align * align
}
