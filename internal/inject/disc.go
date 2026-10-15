package inject

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
)

// Disc is a read-only view of the user's own GZLE01 disc image: its
// main.dol and the files its FST lists. It reads through an io.ReaderAt,
// so opening a 1.4 GB image costs one boot.bin and one FST read, and a CISO
// is read through its block table rather than inflated. This (with the
// patcher) is the one place that knows the disc layout; the co-op stage
// readers get bytes from it. A Disc is safe for concurrent use when its
// ReaderAt is (an *os.File is).
type Disc struct {
	r      io.ReaderAt // the plain image (a CISO is mapped by cisoReader)
	size   int64       // image size in bytes
	closer io.Closer   // the file OpenDisc opened; nil for NewDisc
	dolOff uint32
	ents   []discEntry // the FST; entry 0 is the root
}

// DiscFile is one file the FST lists.
type DiscFile struct {
	Path   string // from the disc root, slash-separated: "res/Stage/sea/Stage.arc"
	Offset uint32 // disc offset of its first byte
	Size   uint32
}

// discEntry is one FST entry, decoded and checked.
type discEntry struct {
	name string // a slice of the one copy of the string table
	dir  bool
	a, b uint32 // file: disc offset, length / directory: parent index, next index
}

const (
	// discMaxFST caps the FST size a header may declare before we read it.
	// The whole FST is loaded into MEM1 (dvd.c:116-117), where the DVD
	// library walks it in place (__DVDFSInit, dvdfs.c:32-39), so a real one
	// is a small fraction of the 24 MiB; a header claiming more is corrupt.
	discMaxFST = 4 << 20

	// discMaxPath caps the length of a path in the FST. No GameCube path
	// comes near it; it bounds what Files allocates for a crafted FST that
	// nests directories deep under long shared names.
	discMaxPath = 255
)

// OpenDisc opens the ISO or CISO image at path. Close it when done.
func OpenDisc(path string) (*Disc, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	d, err := NewDisc(f, st.Size())
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	d.closer = f
	return d, nil
}

// NewDisc reads the size-byte ISO or CISO image r (detected by magic). It
// checks that the image is a GZLE01 disc and that its FST is well formed;
// the formats Dolphin can play but we can't read (RVZ, WIA, GCZ, NKit) are
// reported as an *UnsupportedFormatError saying how to convert them.
func NewDisc(r io.ReaderAt, size int64) (*Disc, error) {
	head := make([]byte, min(max(size, 0), isoHeaderEnd))
	if err := readFullAt(r, head, 0); err != nil {
		return nil, fmt.Errorf("disc: read header: %w", err)
	}
	if err := detectUnsupportedFormat(head); err != nil {
		return nil, err
	}
	if isCISO(head) {
		c, err := newCISOReader(r, size)
		if err != nil {
			return nil, err
		}
		r, size = c, c.size()
	}

	// boot.bin: DVDDiskID, whose first six bytes are the game ID, at 0
	// (dvd.h:45-53), and DVDBB2 at 0x420 (read there by dvd.c:453 and
	// fstload.c:22): the DOL's disc offset, then the FST's offset and
	// length (dvd.h:100-104).
	if size < isoHeaderEnd {
		return nil, fmt.Errorf("disc: image too small (%d bytes)", size)
	}
	d := &Disc{r: r, size: size}
	boot, err := d.readAt(0, isoHeaderEnd)
	if err != nil {
		return nil, fmt.Errorf("disc: read boot.bin: %w", err)
	}
	if got := string(boot[isoHeaderGameID : isoHeaderGameID+6]); got != isoExpectedGameID {
		return nil, fmt.Errorf("disc: unexpected game id %q (expected %q, Wind Waker NTSC-U)", got, isoExpectedGameID)
	}
	d.dolOff = binary.BigEndian.Uint32(boot[isoHeaderDOLOff:])
	fstOff := binary.BigEndian.Uint32(boot[isoHeaderFSTOff:])
	fstSize := binary.BigEndian.Uint32(boot[isoHeaderFSTSize:])
	if fstSize > discMaxFST {
		return nil, fmt.Errorf("disc: header declares a 0x%x-byte FST, more than the 0x%x cap; corrupt image?", fstSize, discMaxFST)
	}
	fst, err := d.readAt(uint64(fstOff), uint64(fstSize))
	if err != nil {
		return nil, fmt.Errorf("disc: FST: %w", err)
	}
	if d.ents, err = parseFSTTree(fst); err != nil {
		return nil, fmt.Errorf("disc: %w", err)
	}
	return d, nil
}

// Close closes the file OpenDisc opened. It is a no-op for NewDisc.
func (d *Disc) Close() error {
	if d.closer == nil {
		return nil
	}
	return d.closer.Close()
}

// Files returns every file on the disc in FST order (each directory's
// entries in the order the disc stores them).
func (d *Disc) Files() []DiscFile {
	type open struct {
		end    uint32
		prefix string
	}
	var files []DiscFile
	stack := []open{{end: d.ents[0].b}}
	for i := 1; i < len(d.ents); i++ {
		for uint32(i) >= stack[len(stack)-1].end {
			stack = stack[:len(stack)-1] // the root's end is len(d.ents), so it stays
		}
		e := d.ents[i]
		p := stack[len(stack)-1].prefix + e.name
		if e.dir {
			stack = append(stack, open{end: e.b, prefix: p + "/"})
			continue
		}
		files = append(files, DiscFile{Path: p, Offset: e.a, Size: e.b})
	}
	return files
}

// Stat looks up the file at path ("res/Stage/sea/Stage.arc"; a leading
// "/" is allowed) the way the game's DVDConvertPathToEntrynum does
// (dvdfs.c:63-161): one component at a time, names compared ASCII
// case-insensitively, the first match in disc order winning, and a
// component followed by "/" matching only a directory. The returned Path
// is spelled as on the disc. A missing file, or a path naming a
// directory, is an error matching fs.ErrNotExist.
func (d *Disc) Stat(path string) (DiscFile, error) {
	i, canon, ok := d.lookup(path)
	if !ok {
		return DiscFile{}, fmt.Errorf("disc: %s: %w", path, fs.ErrNotExist)
	}
	e := d.ents[i]
	return DiscFile{Path: canon, Offset: e.a, Size: e.b}, nil
}

// Open returns a reader over the file at path (see Stat). A file whose
// extent runs past the end of the image is an error.
func (d *Disc) Open(path string) (*io.SectionReader, error) {
	f, err := d.Stat(path)
	if err != nil {
		return nil, err
	}
	if err := d.checkExtent(uint64(f.Offset), uint64(f.Size)); err != nil {
		return nil, fmt.Errorf("disc: %s: %w", f.Path, err)
	}
	return io.NewSectionReader(d.r, int64(f.Offset), int64(f.Size)), nil
}

// ReadFile reads the whole file at path (see Stat).
func (d *Disc) ReadFile(path string) ([]byte, error) {
	f, err := d.Stat(path)
	if err != nil {
		return nil, err
	}
	b, err := d.readAt(uint64(f.Offset), uint64(f.Size))
	if err != nil {
		return nil, fmt.Errorf("disc: %s: %w", f.Path, err)
	}
	return b, nil
}

// ReadDOL reads main.dol: from the offset boot.bin gives, as many bytes as
// its own header's sections span (dolFileSize), so the padding a reserved
// DOL area may carry isn't included. On a patched image this is the
// patched DOL.
func (d *Disc) ReadDOL() ([]byte, error) {
	hdr, err := d.readAt(uint64(d.dolOff), dolHeaderSize)
	if err != nil {
		return nil, fmt.Errorf("disc: main.dol header: %w", err)
	}
	size, err := dolFileSize(hdr)
	if err != nil {
		return nil, fmt.Errorf("disc: main.dol: %w", err)
	}
	dol, err := d.readAt(uint64(d.dolOff), size)
	if err != nil {
		return nil, fmt.Errorf("disc: main.dol: %w", err)
	}
	return dol, nil
}

// DOLBytesAt returns the size bytes a DOL loads at addr: the slice of dol
// holding them, which must lie inside one text or data section (BSS has
// no file bytes). Text sections are header slots 0-6 (file offset at
// 0x00, address at 0x48, size at 0x90) and data sections slots 0-10 (0x1C,
// 0x64, 0xAC). This is how a table such as d_stage.cpp's l_objectName
// (.data 0x80372818, 0x26AC bytes; sym:14693 of the decomp's
// config/GZLE01/symbols.txt) is read out of the user's own main.dol.
func DOLBytesAt(dol []byte, addr, size uint32) ([]byte, error) {
	if len(dol) < dolHeaderSize {
		return nil, fmt.Errorf("dol: too small (%d bytes)", len(dol))
	}
	want := extent{start: uint64(addr), end: uint64(addr) + uint64(size)}
	slots := []struct{ off, n int }{{0x00, dolNumText}, {0x1C, 11}}
	for _, s := range slots {
		for i := 0; i < s.n; i++ {
			off := uint64(readU32BE(dol, s.off+4*i))
			at := uint64(readU32BE(dol, s.off+0x48+4*i))
			n := uint64(readU32BE(dol, s.off+0x90+4*i))
			if off == 0 || n == 0 || want.start < at || want.end > at+n {
				continue
			}
			start := off + (want.start - at)
			if off+n > uint64(len(dol)) {
				return nil, fmt.Errorf("dol: section at 0x%08x (file 0x%x, 0x%x bytes) runs past the end of the DOL (0x%x bytes)",
					at, off, n, len(dol))
			}
			return dol[start : start+uint64(size)], nil
		}
	}
	return nil, fmt.Errorf("dol: 0x%x bytes at 0x%08x are not inside one text or data section", size, addr)
}

// lookup returns the index of the file at path and its path as spelled on
// the disc (see Stat).
func (d *Disc) lookup(path string) (int, string, bool) {
	path = strings.TrimPrefix(path, "/")
	var canon strings.Builder
	dir := 0
	for {
		name, rest, more := strings.Cut(path, "/")
		if name == "" {
			return 0, "", false
		}
		found := -1
		// dvdfs.c:139-149: step over a subdirectory's whole range, skip
		// files while a directory is wanted.
		for i := dir + 1; i < int(d.ents[dir].b); {
			e := d.ents[i]
			if (e.dir || !more) && asciiEqualFold(e.name, name) {
				found = i
				break
			}
			if e.dir {
				i = int(e.b)
			} else {
				i++
			}
		}
		if found < 0 {
			return 0, "", false
		}
		canon.WriteString(d.ents[found].name)
		if !more {
			if d.ents[found].dir {
				return 0, "", false // DVDOpen refuses a directory (dvdfs.c:197-200)
			}
			return found, canon.String(), true
		}
		canon.WriteByte('/')
		dir, path = found, rest
	}
}

// readAt reads n bytes of the image at off.
func (d *Disc) readAt(off, n uint64) ([]byte, error) {
	if err := d.checkExtent(off, n); err != nil {
		return nil, err
	}
	b := make([]byte, n)
	if err := readFullAt(d.r, b, int64(off)); err != nil {
		return nil, err
	}
	return b, nil
}

// checkExtent returns an error unless [off, off+n) lies inside the image.
func (d *Disc) checkExtent(off, n uint64) error {
	if off+n > uint64(d.size) {
		return fmt.Errorf("[0x%x..0x%x] runs past the end of the image (0x%x bytes); is the image truncated?",
			off, off+n, d.size)
	}
	return nil
}

// parseFSTTree decodes an FST and checks the structure the DVD library's
// walk relies on (dvdfs.c:139): every directory's next index lies past it
// and inside its parent's range; every name is a non-empty, NUL-terminated
// string inside the string table with no "/" in it; and no path is longer
// than discMaxPath. Parent indices are kept but not checked, as only ".."
// lookups (which we don't make) use them.
func parseFSTTree(fst []byte) ([]discEntry, error) {
	n, names, err := fstRoot(fst)
	if err != nil {
		return nil, err
	}
	table := string(names) // one copy; every name is a slice of it
	ents := make([]discEntry, n)
	ents[0] = discEntry{dir: true, b: uint32(n)}

	type open struct {
		end     uint64
		pathLen int // length of the directory's path plus its "/"
	}
	stack := []open{{end: n}}
	for i := uint64(1); i < n; i++ {
		for i >= stack[len(stack)-1].end {
			stack = stack[:len(stack)-1] // the root's end is n, so it stays
		}
		top := stack[len(stack)-1]
		e := fstEntryAt(fst, i)
		s, terminated, ok := fstString(names, e.nameOff)
		if !ok || !terminated || len(s) == 0 || bytes.IndexByte(s, '/') >= 0 {
			return nil, fmt.Errorf("fst: entry %d has a bad name %s", i, fstName(names, e.nameOff, i))
		}
		name := table[e.nameOff : int(e.nameOff)+len(s)]
		pathLen := top.pathLen + len(name)
		if pathLen > discMaxPath {
			return nil, fmt.Errorf("fst: entry %d (%q) has a path longer than %d bytes", i, name, discMaxPath)
		}
		ents[i] = discEntry{name: name, dir: e.dir, a: e.w1, b: e.w2}
		if e.dir {
			if next := uint64(e.w2); next <= i || next > top.end {
				return nil, fmt.Errorf("fst: directory %q (entry %d) ends at entry %d, outside (%d, %d]",
					name, i, next, i, top.end)
			}
			stack = append(stack, open{end: uint64(e.w2), pathLen: pathLen + 1})
		}
	}
	return ents, nil
}

// asciiEqualFold reports whether a and b are equal under ASCII case
// folding, the comparison the DVD library's isSame makes with tolower
// (dvdfs.c:49-61). Unlike strings.EqualFold it never decodes UTF-8, so two
// different invalid bytes don't compare equal.
func asciiEqualFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if asciiLower(a[i]) != asciiLower(b[i]) {
			return false
		}
	}
	return true
}

func asciiLower(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}
