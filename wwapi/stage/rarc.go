package stage

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"fmt"
	"io/fs"
	"slices"
	"strings"
)

// RARC is JSystem's archive format (JKRArchive). Stage.arc and every
// RoomN.arc are RARC archives, Yaz0-compressed on the disc. All fields are
// big-endian. The layout, from JKRArchive.h and the way JKRMemArchive::open
// finds the tables (JKRMemArchive.cpp:117-123):
//
//	SArcHeader, 0x20 bytes at 0 (JKRArchive.h:10-19)
//	  0x00  "RARC"
//	  0x04  file length
//	  0x08  header length: where the info block starts
//	  0x0C  file data offset, from the info block
//	  0x10  file data length
//	  0x14  three words this reader ignores
//	SArcDataInfo, 0x20 bytes at the header length (JKRArchive.h:21-31);
//	its offsets are from the start of the info block
//	  0x00  node count          0x04  node table offset
//	  0x08  file entry count    0x0C  file entry table offset
//	  0x10  string table length 0x14  string table offset
//	  0x18  u16 next free file ID, 0x1A u8 "file IDs are indices"
//	node (a directory), 0x10 bytes each (SDIDirEntry, JKRArchive.h:52-58)
//	  0x00  type, a four-character code: 'ROOT' for node 0
//	        (f_op_msg_mng.cpp:1453), otherwise what findResType looks up,
//	        such as 'DZS ' or 'DZR ' (d_resorce.cpp:154-184)
//	  0x04  name offset in the string table
//	  0x08  u16 name hash
//	  0x0A  u16 entry count
//	  0x0C  index of its first file entry
//	file entry, 0x14 bytes each (SDIFileEntry, JKRArchive.h:60-80; the
//	pointer at 0x10 is filled in at run time)
//	  0x00  u16 file ID
//	  0x02  u16 name hash
//	  0x04  attribute byte << 24 | name offset
//	  0x08  file: data offset from the file data start (fetchResource,
//	        JKRMemArchive.cpp:152); directory: the node index
//	        (findFsResource, JKRArchivePri.cpp:118-119)
//	  0x0C  data size
//
// Attribute bits: 0x01 file, 0x02 directory, 0x04 compressed, with 0x80
// meaning Yaz0 and its absence Yay0 (JKRArchive.h:73-78,193-206); 0x10,
// 0x20 and 0x40 say where the file is preloaded (main RAM, ARAM, or not
// at all: read from the disc), which only ArchiveEditor uses. A node's
// entries are its files and subdirectories followed by "." and ".."
// (directory entries for the node itself and its parent); the game would
// follow them by name, so the listing here skips them. Inferred from the
// archive layout, not from the decomp, which never lists a directory.
const (
	rarcHeaderSize = 0x20
	rarcInfoSize   = 0x20
	rarcNodeSize   = 0x10
	rarcEntrySize  = 0x14

	rarcAttrDir        = 0x02
	rarcAttrCompressed = 0x04
	rarcAttrMRAM       = 0x10 // preload into main RAM
	rarcAttrARAM       = 0x20 // preload into ARAM (every REL in RELS.arc)
	rarcAttrDVD        = 0x40 // load from the disc on demand
	rarcAttrYaz0       = 0x80

	// maxArchivePath caps the length of a path in an archive. The game
	// can't even match a name past 256 bytes (CArcName holds 256,
	// JKRArchive.h:112, JKRArchivePri.cpp:195-197); the cap bounds what
	// listing a crafted archive's deep directories allocates, and how far
	// a name is searched for its terminating NUL.
	maxArchivePath = 255

	// MaxArchiveSize caps the stored size of an archive. The game mounts
	// a stage or room archive whole into one heap block, expanded
	// (JKRMemArchive::open, JKRMemArchive.cpp:91-123, through
	// loadToMainRAM, JKRDvdRipper.cpp:78-84, :100), so it is at most
	// MaxYaz0Size expanded; stored as Yaz0 it is at most that much in
	// literals, a code byte per eight of them, and the header. A larger
	// file is corrupt, and Load refuses it before reading it.
	MaxArchiveSize = yaz0HeaderSize + MaxYaz0Size + MaxYaz0Size/8
)

// Archive is a parsed RARC archive. Its methods never modify it; Edit
// returns a copy that can be.
type Archive struct {
	data    []byte // the (expanded) archive
	dataOff uint64 // where file data starts
	nodes   []rarcNode
	entries []rarcEntry
	files   []ArchiveFile // every file entry, in entry order
	strs    []byte        // the string table, as stored
	nextID  uint16        // info 0x18: the next free file ID
	idsSync uint8         // info 0x1A: file IDs are entry indices
}

// rarcNode is one node (directory), decoded.
type rarcNode struct {
	typ          string
	first, count uint64 // its range of file entries
	nameOff      uint32
	hash         uint16
}

// ArchiveFile is one file in an archive.
type ArchiveFile struct {
	Path  string // from the root node, slash-separated: "dzs/stage.dzs"
	Index int    // file entry index, which the game's resource tables use (d_resorce.cpp:369)
	ID    uint16 // file ID
	Attr  uint8  // attribute byte: 0x01 file, 0x04 compressed, 0x80 Yaz0
	Type  string // its directory node's four-character type, such as "DZS "
	Size  uint32 // stored size: the compressed size of a compressed file
}

// Compressed reports whether the file is stored compressed (attribute 0x04).
func (f ArchiveFile) Compressed() bool { return f.Attr&rarcAttrCompressed != 0 }

// rarcEntry is one file entry, decoded.
type rarcEntry struct {
	id         uint16
	hash       uint16
	attr       uint8
	name       string
	nameOff    uint32
	off, size  uint32
	owner      int32 // the node listing the entry, -1 for none
	dir, isDot bool  // a directory; named "." or ".."
}

// ParseArchive parses the RARC archive b. A Yaz0-compressed archive is
// expanded first, as the game's loader does on seeing the magic
// (loadToMainRAM, JKRDvdRipper.cpp:70-84). An archive over MaxYaz0Size
// expanded is refused (see MaxArchiveSize). It checks the whole table
// structure: every table and name inside the archive, node entry ranges
// in bounds and disjoint, and the directories a tree under node 0, each
// node reached once. File data is checked when a file is read.
func ParseArchive(b []byte) (*Archive, error) {
	if IsYaz0(b) {
		var err error
		if b, err = DecodeYaz0(b); err != nil {
			return nil, fmt.Errorf("rarc: %w", err)
		}
	} else if len(b) > MaxYaz0Size {
		return nil, fmt.Errorf("rarc: %d bytes, more than the %d-byte cap", len(b), MaxYaz0Size)
	}
	if len(b) < rarcHeaderSize {
		return nil, fmt.Errorf("rarc: header truncated (%d bytes)", len(b))
	}
	if string(b[:4]) != "RARC" {
		return nil, fmt.Errorf("rarc: bad magic %q", b[:4])
	}
	be := binary.BigEndian
	if fileLen := be.Uint32(b[4:]); uint64(fileLen) > uint64(len(b)) {
		return nil, fmt.Errorf("rarc: truncated: the header says %d bytes, there are %d", fileLen, len(b))
	}
	info := uint64(be.Uint32(b[8:]))
	if info+rarcInfoSize > uint64(len(b)) {
		return nil, fmt.Errorf("rarc: info block at 0x%x runs past the end (%d bytes)", info, len(b))
	}
	ib := b[info : info+rarcInfoSize]
	a := &Archive{data: b, dataOff: info + uint64(be.Uint32(b[0x0C:]))}

	// table returns the count*size bytes at off from the info block.
	table := func(what string, off, count, size uint64) ([]byte, error) {
		start := info + off
		if start+count*size > uint64(len(b)) {
			return nil, fmt.Errorf("rarc: %d %s at 0x%x run past the end (%d bytes)", count, what, start, len(b))
		}
		return b[start : start+count*size], nil
	}
	nNodes, nEntries := uint64(be.Uint32(ib[0x00:])), uint64(be.Uint32(ib[0x08:]))
	nodes, err := table("nodes", uint64(be.Uint32(ib[0x04:])), nNodes, rarcNodeSize)
	if err != nil {
		return nil, err
	}
	ents, err := table("file entries", uint64(be.Uint32(ib[0x0C:])), nEntries, rarcEntrySize)
	if err != nil {
		return nil, err
	}
	strs, err := table("bytes of strings", uint64(be.Uint32(ib[0x14:])), uint64(be.Uint32(ib[0x10:])), 1)
	if err != nil {
		return nil, err
	}
	if nNodes == 0 {
		return nil, fmt.Errorf("rarc: no root node")
	}
	a.strs = strs
	a.nextID, a.idsSync = be.Uint16(ib[0x18:]), ib[0x1A]
	strTable := string(strs) // one copy; every name is a slice of it
	// name returns the name at off, or what is wrong with it. It looks
	// for the NUL no further than a path can be long (no path holds a
	// longer name), so entries that all name one long string cost no more
	// than short names do.
	name := func(off uint32) (string, string) {
		if uint64(off) >= uint64(len(strTable)) {
			return "", fmt.Sprintf("name offset 0x%x is past the %d-byte string table", off, len(strTable))
		}
		rest := strTable[off:]
		window := rest[:min(len(rest), maxArchivePath+1)]
		end := strings.IndexByte(window, 0)
		switch {
		case end < 0 && len(window) < len(rest):
			return "", fmt.Sprintf("name at 0x%x is longer than %d bytes", off, maxArchivePath)
		case end <= 0:
			return "", fmt.Sprintf("name at 0x%x is empty or unterminated", off)
		}
		return rest[:end], ""
	}

	a.entries = make([]rarcEntry, nEntries)
	for i := range a.entries {
		e := ents[i*rarcEntrySize:]
		w := be.Uint32(e[4:])
		n, bad := name(w & 0x00FFFFFF)
		if bad == "" && strings.IndexByte(n, '/') >= 0 { // the path lookups split on it (JKRArchivePri.cpp:112)
			bad = fmt.Sprintf("name %q has a '/' in it", n)
		}
		if bad != "" {
			return nil, fmt.Errorf("rarc: file entry %d: %s", i, bad)
		}
		attr := uint8(w >> 24)
		a.entries[i] = rarcEntry{
			id: be.Uint16(e[0:]), hash: be.Uint16(e[2:]), attr: attr, name: n, nameOff: w & 0x00FFFFFF,
			off: be.Uint32(e[8:]), size: be.Uint32(e[12:]),
			owner: -1, dir: attr&rarcAttrDir != 0, isDot: n == "." || n == "..",
		}
	}

	// Each node lists a range of entries; no entry belongs to two nodes.
	a.nodes = make([]rarcNode, nNodes)
	ns := a.nodes
	for k := range ns {
		nd := nodes[k*rarcNodeSize:]
		ns[k] = rarcNode{
			typ:     string(nd[0:4]),
			first:   uint64(be.Uint32(nd[0x0C:])),
			count:   uint64(be.Uint16(nd[0x0A:])),
			nameOff: be.Uint32(nd[4:]),
			hash:    be.Uint16(nd[8:]),
		}
		if _, bad := name(ns[k].nameOff); bad != "" {
			return nil, fmt.Errorf("rarc: node %d: %s", k, bad)
		}
		if ns[k].first+ns[k].count > nEntries {
			return nil, fmt.Errorf("rarc: node %d lists entries %d..%d, past the %d entries",
				k, ns[k].first, ns[k].first+ns[k].count, nEntries)
		}
		for i := ns[k].first; i < ns[k].first+ns[k].count; i++ {
			if o := a.entries[i].owner; o >= 0 {
				return nil, fmt.Errorf("rarc: nodes %d and %d both list entry %d", o, k, i)
			}
			a.entries[i].owner = int32(k)
		}
	}

	// Walk the directory tree from node 0, depth first, naming each file
	// by its path. A node reached twice would make a cycle or a shared
	// subtree, and one never reached would hold files with no path: both
	// are errors.
	paths := make([]string, nEntries)
	reached := make([]bool, nNodes)
	reached[0] = true
	type open struct {
		node   int
		prefix string
	}
	stack := []open{{node: 0}}
	for len(stack) > 0 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		nd := ns[top.node]
		for i := nd.first; i < nd.first+nd.count; i++ {
			e := a.entries[i]
			if e.isDot {
				continue
			}
			p := top.prefix + e.name
			if len(p) > maxArchivePath {
				return nil, fmt.Errorf("rarc: entry %d (%q) has a path longer than %d bytes", i, e.name, maxArchivePath)
			}
			if !e.dir {
				paths[i] = p
				continue
			}
			if uint64(e.off) >= nNodes || reached[e.off] {
				return nil, fmt.Errorf("rarc: directory %q (entry %d) points at node %d: out of range or already reached",
					p, i, e.off)
			}
			reached[e.off] = true
			stack = append(stack, open{node: int(e.off), prefix: p + "/"})
		}
	}
	for k, r := range reached {
		if !r {
			return nil, fmt.Errorf("rarc: node %d is not reachable from the root", k)
		}
	}

	for i, e := range a.entries {
		if paths[i] == "" {
			continue // a directory, "." or "..", or listed by no node
		}
		a.files = append(a.files, ArchiveFile{
			Path: paths[i], Index: i, ID: e.id, Attr: e.attr,
			Type: ns[e.owner].typ, Size: e.size,
		})
	}
	return a, nil
}

// Files returns every file in the archive in entry order. Entries no node
// lists are left out: the game never loads them (dRes_info_c::loadResource
// fills its table one node type at a time, d_resorce.cpp:187-372).
func (a *Archive) Files() []ArchiveFile {
	return append([]ArchiveFile(nil), a.files...)
}

// File looks up the file at path ("dzs/stage.dzs"; a leading "/" is
// allowed) the way JKRArchive::findFsResource does (JKRArchivePri.cpp:
// 110-132): one directory at a time, the first entry of each name
// winning. Names compare ASCII case-insensitively; the game lowercases the
// path and compares it to the stored name (CArcName, JKRArchivePri.cpp:
// 189-224), which matches the same names when, as in the game's archives,
// they are stored lowercase. "." and ".." are not followed.
func (a *Archive) File(path string) (ArchiveFile, bool) {
	path = strings.TrimPrefix(path, "/")
	nd := a.nodes[0]
	for {
		name, rest, more := strings.Cut(path, "/")
		if name == "" || name == "." || name == ".." {
			return ArchiveFile{}, false
		}
		found := -1
		for i := nd.first; i < nd.first+nd.count; i++ {
			if asciiEqualFold(a.entries[i].name, name) {
				found = int(i)
				break
			}
		}
		if found < 0 {
			return ArchiveFile{}, false
		}
		e := a.entries[found]
		if !more {
			if e.dir {
				return ArchiveFile{}, false
			}
			return a.fileAt(found)
		}
		if !e.dir {
			return ArchiveFile{}, false
		}
		nd, path = a.nodes[e.off], rest // checked in range by ParseArchive's walk
	}
}

// FindName returns the first file, in entry order, named exactly name: the
// lookup the game's stage code uses for "stage.dzs" and "room.dzr"
// (dRes_control_c::getRes, d_resorce.cpp:657-674, a case-sensitive strcmp).
func (a *Archive) FindName(name string) (ArchiveFile, bool) {
	for _, f := range a.files {
		if a.entries[f.Index].name == name {
			return f, true
		}
	}
	return ArchiveFile{}, false
}

// Read returns the contents of f, a file of this archive. A compressed
// file is expanded: the archive decodes by magic (JKRDecomp::decode,
// JKRDecomp.cpp:112-119), and only Yaz0 is supported; a Yay0 file is an
// error. The result is a copy.
func (a *Archive) Read(f ArchiveFile) ([]byte, error) {
	f, ok := a.fileAt(f.Index)
	if !ok {
		return nil, fmt.Errorf("rarc: entry %d is not a file of this archive", f.Index)
	}
	e := a.entries[f.Index]
	start := a.dataOff + uint64(e.off)
	if start+uint64(e.size) > uint64(len(a.data)) {
		return nil, fmt.Errorf("rarc: %q: data [0x%x..0x%x] runs past the end of the archive (%d bytes)",
			f.Path, start, start+uint64(e.size), len(a.data))
	}
	return expandStored(f.Path, e.attr, a.data[start:start+uint64(e.size)], -1)
}

// expandStored returns the contents of a file stored as data with
// attribute attr: a copy when it is not compressed, else data expanded
// (its first limit bytes when limit is not negative). Errors name path.
func expandStored(path string, attr uint8, data []byte, limit int) ([]byte, error) {
	if attr&rarcAttrCompressed == 0 {
		if limit >= 0 && limit < len(data) {
			data = data[:limit]
		}
		return bytes.Clone(data), nil
	}
	switch {
	case IsYaz0(data):
		out, err := decodeYaz0(data, limit)
		if err != nil {
			return nil, fmt.Errorf("rarc: %q: %w", path, err)
		}
		return out, nil
	case len(data) >= 4 && string(data[:4]) == "Yay0":
		return nil, fmt.Errorf("rarc: %q: Yay0 compression is not supported", path)
	default:
		return nil, fmt.Errorf("rarc: %q: marked compressed but has no Yaz0 header", path)
	}
}

// ReadFile reads the file at path (see File). A missing file, or a path
// naming a directory, is an error matching fs.ErrNotExist.
func (a *Archive) ReadFile(path string) ([]byte, error) {
	f, ok := a.File(path)
	if !ok {
		return nil, fmt.Errorf("rarc: %s: %w", path, fs.ErrNotExist)
	}
	return a.Read(f)
}

// fileAt returns the listing of file entry i.
func (a *Archive) fileAt(i int) (ArchiveFile, bool) {
	k, ok := slices.BinarySearchFunc(a.files, i, func(f ArchiveFile, i int) int { return cmp.Compare(f.Index, i) })
	if !ok {
		return ArchiveFile{Index: i}, false
	}
	return a.files[k], true
}

// asciiEqualFold reports whether a and b are equal under ASCII case
// folding, the tolower comparison the game's name lookups make. Unlike
// strings.EqualFold it never decodes UTF-8.
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
