package stage

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io/fs"
)

// ArchiveEditor is an Archive whose files' contents can be replaced and
// which can be written back out. Only contents change: the directory
// tree, every name and hash, the entry order and the file IDs stay as
// parsed, so the file indices the game's resource tables hold
// (d_resorce.cpp:369) stay valid. Bytes lays the archive out the way
// wwrando's rarc.py does (the layout the WW Online patcher's C# port
// writes): the info block at 0x20, the node table at 0x40, then the
// entry table, the string table and the file data, each 0x20-aligned
// and zero-padded, with the data grouped by preload attribute (0x10
// main RAM, then 0x20 ARAM, then 0x40 DVD) and every file 0x20-aligned.
// The vanilla RELS.arc is laid out that way already, so an unchanged
// archive saves byte for byte.
//
// Differences from rarc.py, each keeping more of the archive as it was:
// the string table is written as parsed, not regenerated (rarc.py orders
// it nodes first, the vanilla archives one directory at a time); entries
// stay in their parsed order (rarc.py re-walks the tree); hashes are kept
// (rarc.py recomputes them without the game's tolower); compression
// follows an entry's attribute, not its first bytes; and a file with no
// preload bit is placed after the DVD group, where rarc.py refuses to
// save.
type ArchiveEditor struct {
	a     *Archive
	blobs [][]byte // per entry: a file's stored bytes (compressed when the entry is), nil for a directory
}

// Edit returns an editor over a copy of a's state. It fails when a file's
// data runs past the end of the archive, which Read would report when
// the file is read; an editor has to save every file.
func (a *Archive) Edit() (*ArchiveEditor, error) {
	e := &ArchiveEditor{a: a, blobs: make([][]byte, len(a.entries))}
	for _, f := range a.files {
		en := a.entries[f.Index]
		start := a.dataOff + uint64(en.off)
		if start+uint64(en.size) > uint64(len(a.data)) {
			return nil, fmt.Errorf("rarc: %q: data [0x%x..0x%x] runs past the end of the archive (%d bytes)",
				f.Path, start, start+uint64(en.size), len(a.data))
		}
		e.blobs[f.Index] = a.data[start : start+uint64(en.size)]
	}
	return e, nil
}

// Files returns every file in entry order, as Archive.Files does, with
// the stored size and the ID each will be saved with.
func (e *ArchiveEditor) Files() []ArchiveFile {
	files := e.a.Files()
	for i := range files {
		files[i].Size = uint32(len(e.blobs[files[i].Index]))
		files[i].ID = e.fileID(files[i].Index)
	}
	return files
}

// fileID is the ID the file at entry i is saved with: its index when
// the archive says IDs are indices (and the game's archives do), else
// the ID it was parsed with.
func (e *ArchiveEditor) fileID(i int) uint16 {
	if e.a.idsSync != 0 {
		return uint16(i)
	}
	return e.a.entries[i].id
}

// Read returns the contents of f as they are now, expanded when the
// entry is compressed (see Archive.Read).
func (e *ArchiveEditor) Read(f ArchiveFile) ([]byte, error) {
	f, ok := e.a.fileAt(f.Index)
	if !ok {
		return nil, fmt.Errorf("rarc: entry %d is not a file of this archive", f.Index)
	}
	return expandStored(f.Path, e.a.entries[f.Index].attr, e.blobs[f.Index], -1)
}

// Replace makes data the contents of f. It is stored Yaz0-compressed
// when the entry is compressed (attribute 0x04 with 0x80) and as given
// otherwise; a Yay0 entry (0x04 without 0x80) can't be replaced. data is
// copied.
func (e *ArchiveEditor) Replace(f ArchiveFile, data []byte) error {
	f, ok := e.a.fileAt(f.Index)
	if !ok {
		return fmt.Errorf("rarc: entry %d is not a file of this archive", f.Index)
	}
	attr := e.a.entries[f.Index].attr
	switch {
	case attr&rarcAttrCompressed == 0:
		e.blobs[f.Index] = bytes.Clone(data)
	case attr&rarcAttrYaz0 == 0:
		return fmt.Errorf("rarc: %q: Yay0 compression is not supported", f.Path)
	default:
		e.blobs[f.Index] = EncodeYaz0(data)
	}
	return nil
}

// ReplaceFile replaces the contents of the file at path (see
// Archive.File). A missing file is an error matching fs.ErrNotExist.
func (e *ArchiveEditor) ReplaceFile(path string, data []byte) error {
	f, ok := e.a.File(path)
	if !ok {
		return fmt.Errorf("rarc: %s: %w", path, fs.ErrNotExist)
	}
	return e.Replace(f, data)
}

// ReplaceByFirstWord replaces the contents of the first file, in entry
// order, whose contents start with the big-endian u32 word, and returns
// it: the way to replace a REL in RELS.arc by its module ID, the first
// word of a REL. Each file is looked at through its first four bytes
// only, so the others stay stored as they are. No such file is an error
// matching fs.ErrNotExist; a file that can't be expanded is an error.
func (e *ArchiveEditor) ReplaceByFirstWord(word uint32, data []byte) (ArchiveFile, error) {
	for _, f := range e.a.files {
		head, err := expandStored(f.Path, e.a.entries[f.Index].attr, e.blobs[f.Index], 4)
		if err != nil {
			return ArchiveFile{}, err
		}
		if len(head) < 4 || binary.BigEndian.Uint32(head) != word {
			continue
		}
		if err := e.Replace(f, data); err != nil {
			return ArchiveFile{}, err
		}
		f.Size = uint32(len(e.blobs[f.Index]))
		return f, nil
	}
	return ArchiveFile{}, fmt.Errorf("rarc: no file starts with the word 0x%08x: %w", word, fs.ErrNotExist)
}

// Growth is how many bytes longer than the archive Edit was given (as
// expanded) the archive Bytes writes is: negative when it shrank.
func (e *ArchiveEditor) Growth() int {
	return int(e.plan().size) - len(e.a.data)
}

// Bytes writes the archive out (see ArchiveEditor). It refuses an
// archive over MaxYaz0Size, which ParseArchive would refuse and the game
// couldn't mount.
func (e *ArchiveEditor) Bytes() ([]byte, error) {
	p := e.plan()
	if p.size > MaxYaz0Size {
		return nil, fmt.Errorf("rarc: %d bytes, more than the %d-byte cap", p.size, MaxYaz0Size)
	}
	a := e.a
	out := make([]byte, p.size)
	be := binary.BigEndian
	copy(out, "RARC")
	be.PutUint32(out[0x04:], uint32(p.size))
	be.PutUint32(out[0x08:], rarcHeaderSize)
	be.PutUint32(out[0x0C:], uint32(p.data-rarcHeaderSize))
	be.PutUint32(out[0x10:], p.total)
	be.PutUint32(out[0x14:], p.mram)
	be.PutUint32(out[0x18:], p.aram)

	ib := out[rarcHeaderSize:]
	be.PutUint32(ib[0x00:], uint32(len(a.nodes)))
	be.PutUint32(ib[0x04:], rarcInfoSize)
	be.PutUint32(ib[0x08:], uint32(len(a.entries)))
	be.PutUint32(ib[0x0C:], uint32(p.entries-rarcHeaderSize))
	be.PutUint32(ib[0x10:], uint32(p.data-p.strs))
	be.PutUint32(ib[0x14:], uint32(p.strs-rarcHeaderSize))
	nextID := a.nextID
	if a.idsSync != 0 {
		nextID = uint16(len(a.entries))
	}
	be.PutUint16(ib[0x18:], nextID)
	ib[0x1A] = a.idsSync

	for k, nd := range a.nodes {
		b := out[rarcHeaderSize+rarcInfoSize+k*rarcNodeSize:]
		copy(b, nd.typ)
		be.PutUint32(b[0x04:], nd.nameOff)
		be.PutUint16(b[0x08:], nd.hash)
		be.PutUint16(b[0x0A:], uint16(nd.count))
		be.PutUint32(b[0x0C:], uint32(nd.first))
	}
	for i, en := range a.entries {
		b := out[p.entries+i*rarcEntrySize:]
		id, off, size := en.id, en.off, en.size
		if !en.dir {
			id = e.fileID(i)
			off, size = p.offs[i], uint32(len(e.blobs[i]))
			copy(out[p.data+int(off):], e.blobs[i])
		}
		be.PutUint16(b[0x00:], id)
		be.PutUint16(b[0x02:], en.hash)
		be.PutUint32(b[0x04:], uint32(en.attr)<<24|en.nameOff)
		be.PutUint32(b[0x08:], off)
		be.PutUint32(b[0x0C:], size)
	}
	copy(out[p.strs:], a.strs)
	return out, nil
}

// rarcPlan is where Bytes puts everything: absolute offsets of the
// tables and the file data, each file's offset from the data start, the
// main-RAM, ARAM and total data sizes, and the archive's size.
type rarcPlan struct {
	entries, strs, data int
	offs                []uint32
	mram, aram, total   uint32
	size                uint64
}

func (e *ArchiveEditor) plan() rarcPlan {
	a := e.a
	var p rarcPlan
	p.entries = alignUp(rarcHeaderSize+rarcInfoSize+len(a.nodes)*rarcNodeSize, 0x20)
	p.strs = alignUp(p.entries+len(a.entries)*rarcEntrySize, 0x20)
	p.data = alignUp(p.strs+len(a.strs), 0x20)
	p.offs = make([]uint32, len(a.entries))
	var pos uint64
	for g := 0; g < 4; g++ {
		for i, en := range a.entries {
			if en.dir || preloadGroup(en.attr) != g {
				continue
			}
			p.offs[i] = uint32(pos)
			pos = uint64(alignUp(int(pos)+len(e.blobs[i]), 0x20))
		}
		switch g {
		case 0:
			p.mram = uint32(pos)
		case 1:
			p.aram = uint32(pos) - p.mram
		}
	}
	p.total = uint32(pos)
	p.size = uint64(p.data) + pos
	return p
}

// preloadGroup orders a file's data by its preload attribute the way the
// game's archive tools do: main RAM, ARAM, DVD, then files marked for
// none of them.
func preloadGroup(attr uint8) int {
	switch {
	case attr&rarcAttrMRAM != 0:
		return 0
	case attr&rarcAttrARAM != 0:
		return 1
	case attr&rarcAttrDVD != 0:
		return 2
	}
	return 3
}

func alignUp(v, a int) int { return (v + a - 1) / a * a }
