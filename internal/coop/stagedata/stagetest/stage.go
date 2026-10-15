package stagetest

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io/fs"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
)

// Stage is a synthetic stage directory, for Disc.AddStage: a Stage.arc
// holding dzs/stage.dzs and a Room<N>.arc holding dzr/room.dzr per room,
// under res/Stage/<Name>/ (the paths stagedata's stage.go cites).
type Stage struct {
	Name  string
	DZS   []byte // stage.dzs; nil leaves it out of Stage.arc
	Rooms []Room
	Yaz0  bool   // Yaz0-compress the archives, as the disc stores them
	Extra []File // more files for Stage.arc
}

// Room is one room of a Stage.
type Room struct {
	No    int
	DZR   []byte // room.dzr; nil leaves it out of the archive
	Extra []File // more files for the archive
}

// StageArc builds s's Stage.arc.
func (s Stage) StageArc() []byte {
	files := append([]File(nil), s.Extra...)
	if s.DZS != nil {
		files = append([]File{{Path: "dzs/stage.dzs", Data: s.DZS}}, files...)
	}
	return pack(RARC("stage", files...), s.Yaz0)
}

// RoomArc builds the archive of room r of s.
func (s Stage) RoomArc(r Room) []byte {
	files := append([]File(nil), r.Extra...)
	if r.DZR != nil {
		files = append([]File{{Path: "dzr/room.dzr", Data: r.DZR}}, files...)
	}
	return pack(RARC(fmt.Sprintf("room%d", r.No), files...), s.Yaz0)
}

func pack(arc []byte, yaz0 bool) []byte {
	if yaz0 {
		return Yaz0(arc)
	}
	return arc
}

// Disc is an in-memory disc: it lists and reads files the way
// *inject.Disc does, so it satisfies stagedata.FileSource. Lookups go one
// path component at a time, ASCII case-insensitively, the first match in
// the order files were added winning (as DVDConvertPathToEntrynum does
// in disc order). The zero value is an empty disc.
type Disc struct {
	files []inject.DiscFile
	data  [][]byte
	end   uint32
}

// Add adds a file at path ("res/Stage/sea/Stage.arc").
func (d *Disc) Add(path string, data []byte) {
	d.files = append(d.files, inject.DiscFile{Path: path, Offset: d.end, Size: uint32(len(data))})
	d.data = append(d.data, data)
	d.end += uint32(len(data))
}

// AddStage adds s's archives: res/Stage/<Name>/Stage.arc, then each
// room's Room<N>.arc.
func (d *Disc) AddStage(s Stage) {
	dir := "res/Stage/" + s.Name + "/"
	d.Add(dir+"Stage.arc", s.StageArc())
	for _, r := range s.Rooms {
		d.Add(fmt.Sprintf("%sRoom%d.arc", dir, r.No), s.RoomArc(r))
	}
}

// Files lists every file in the order added.
func (d *Disc) Files() []inject.DiscFile { return append([]inject.DiscFile(nil), d.files...) }

// Stat returns the entry ReadFile reads for path, spelled as added, or an
// error matching fs.ErrNotExist.
func (d *Disc) Stat(path string) (inject.DiscFile, error) {
	i, err := d.lookup(path)
	if err != nil {
		return inject.DiscFile{}, err
	}
	return d.files[i], nil
}

// ReadFile returns a copy of the file at path, or an error matching
// fs.ErrNotExist.
func (d *Disc) ReadFile(path string) ([]byte, error) {
	i, err := d.lookup(path)
	if err != nil {
		return nil, err
	}
	return bytes.Clone(d.data[i]), nil
}

// lookup returns the index of the file at path.
func (d *Disc) lookup(path string) (int, error) {
	want := strings.Split(strings.TrimPrefix(path, "/"), "/")
	prefix := "" // the directories resolved so far, spelled as added
	for k, name := range want {
		last := k == len(want)-1
		found := ""
		for _, f := range d.files {
			rest, ok := strings.CutPrefix(f.Path, prefix)
			if !ok {
				continue
			}
			comp, _, more := strings.Cut(rest, "/")
			if more != last && foldEqual(comp, name) {
				found = comp
				break
			}
		}
		if found == "" {
			return 0, fmt.Errorf("stagetest: %s: %w", path, fs.ErrNotExist)
		}
		prefix += found
		if !last {
			prefix += "/"
		}
	}
	for i, f := range d.files {
		if f.Path == prefix {
			return i, nil
		}
	}
	return 0, fmt.Errorf("stagetest: %s: %w", path, fs.ErrNotExist)
}

// ISO builds a plain GZLE01 disc image of d's files with dol as its
// main.dol, which inject.NewDisc opens: boot.bin's game ID at 0 and the
// DOL offset, FST offset, FST size and maximum FST size at 0x420-0x42F
// (the fields inject's iso.go cites), then the DOL, the FST and the file
// data, 0x20-aligned. The FST lists each directory's files and
// subdirectories in the order they were first added: entries of 0xC
// bytes (flags << 24 | name offset; a file's offset and size, or a
// directory's parent and next-entry indices), the root first, depth
// first, then the string table.
func (d *Disc) ISO(dol []byte) []byte {
	type node struct {
		name     string
		file     int // index into d.files; -1 for a directory
		children []*node
	}
	root := &node{file: -1}
	for i, f := range d.files {
		parts := strings.Split(f.Path, "/")
		n := root
		for _, p := range parts[:len(parts)-1] {
			var sub *node
			for _, c := range n.children {
				if c.file < 0 && c.name == p {
					sub = c
					break
				}
			}
			if sub == nil {
				sub = &node{name: p, file: -1}
				n.children = append(n.children, sub)
			}
			n = sub
		}
		n.children = append(n.children, &node{name: parts[len(parts)-1], file: i})
	}

	const dolOff = 0x3000
	fstOff := uint32(dolOff + align(len(dol), 0x20))
	var count func(n *node) int
	count = func(n *node) int {
		c := 1
		for _, ch := range n.children {
			c += count(ch)
		}
		return c
	}
	total := count(root)
	strs := []byte{0} // the root's (empty) name
	ents := make([]byte, 0, total*0xC)
	var fileOffs []uint32 // per d.files index, patched in below
	type pending struct{ ent, file int }
	var files []pending
	var emit func(n *node, parent int)
	emit = func(n *node, parent int) {
		be := binary.BigEndian
		idx := len(ents) / 0xC
		nameOff := uint32(0)
		if n != root {
			nameOff = uint32(len(strs))
			strs = append(append(strs, n.name...), 0)
		}
		if n.file >= 0 {
			ents = be.AppendUint32(ents, nameOff)
			ents = be.AppendUint32(ents, 0) // offset, patched below
			ents = be.AppendUint32(ents, uint32(len(d.data[n.file])))
			files = append(files, pending{ent: idx, file: n.file})
			return
		}
		ents = be.AppendUint32(ents, 1<<24|nameOff)
		ents = be.AppendUint32(ents, uint32(parent))
		ents = be.AppendUint32(ents, uint32(idx+count(n)))
		for _, ch := range n.children {
			emit(ch, idx)
		}
	}
	emit(root, 0)
	fst := append(ents, strs...)
	pos := int(fstOff) + align(len(fst), 0x20)
	fileOffs = make([]uint32, len(d.files))
	for _, p := range files {
		fileOffs[p.file] = uint32(pos)
		binary.BigEndian.PutUint32(fst[p.ent*0xC+4:], uint32(pos))
		pos = align(pos+len(d.data[p.file]), 0x20)
	}

	img := make([]byte, pos)
	copy(img, "GZLE01")
	be := binary.BigEndian
	be.PutUint32(img[0x420:], dolOff)
	be.PutUint32(img[0x424:], fstOff)
	be.PutUint32(img[0x428:], uint32(len(fst)))
	be.PutUint32(img[0x42C:], uint32(len(fst)))
	copy(img[dolOff:], dol)
	copy(img[fstOff:], fst)
	for i, data := range d.data {
		copy(img[fileOffs[i]:], data)
	}
	return img
}

func foldEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

// l_objectName as GZLE01 has it (stagedata's objname.go cites these):
// 825 entries of 0xC bytes at .data 0x80372818, the last "EndCode" with
// proc 7, fpcNm_PLAY_SCENE_e.
const (
	ObjectNameAddr  = 0x80372818
	ObjectNameCount = 825
	objectNameEntry = 0xC
)

// ObjectName is one l_objectName entry.
type ObjectName struct {
	Name    string // name[8]: shorter is NUL-padded, longer is cut
	Proc    int16
	Subtype int8 // -1 for none, as the table's 255
	GBAName int8
}

// ObjectNameTable builds a whole GZLE01-sized l_objectName table: names,
// then filler entries "Pad000", "Pad001", ... (proc 0, no subtype), then
// "EndCode". It panics on more than ObjectNameCount-1 names.
func ObjectNameTable(names ...ObjectName) []byte {
	if len(names) > ObjectNameCount-1 {
		panic(fmt.Sprintf("stagetest: %d object names, at most %d fit", len(names), ObjectNameCount-1))
	}
	all := append([]ObjectName(nil), names...)
	for i := 0; len(all) < ObjectNameCount-1; i++ {
		all = append(all, ObjectName{Name: fmt.Sprintf("Pad%03d", i), Subtype: -1})
	}
	all = append(all, ObjectName{Name: "EndCode", Proc: 7, Subtype: -1})
	out := make([]byte, len(all)*objectNameEntry)
	for i, n := range all {
		e := out[i*objectNameEntry:]
		copy(e[:8], n.Name)
		binary.BigEndian.PutUint16(e[8:], uint16(n.Proc))
		e[10], e[11] = byte(n.Subtype), byte(n.GBAName)
	}
	return out
}

// MainDOL builds a main.dol carrying ObjectNameTable(names...) at
// 0x80372818, in a data section that starts 0x18 bytes before the table
// and ends 0x24 bytes after it, so finding it takes the address mapping.
func MainDOL(names ...ObjectName) []byte {
	const before, after = 0x18, 0x24
	data := make([]byte, before, before+ObjectNameCount*objectNameEntry+after)
	data = append(data, ObjectNameTable(names...)...)
	data = append(data, make([]byte, after)...)
	return DOL(ObjectNameAddr-before, data)
}

// DOL builds a minimal DOL: one text section of a single blr at
// 0x80003100 (the entry point) and one data section holding data at
// dataAddr. The header is the 0x100-byte DOL header the patcher edits:
// text slots' file offsets at 0x00, addresses at 0x48, sizes at 0x90;
// data slots' at 0x1C, 0x64 and 0xAC; BSS address and size at 0xD8 and
// 0xDC; the entry point at 0xE0.
func DOL(dataAddr uint32, data []byte) []byte {
	const textAddr, textOff, textSize = 0x80003100, 0x100, 0x20
	dataOff := uint32(textOff + textSize)
	out := make([]byte, int(dataOff)+align(len(data), 0x20))
	be := binary.BigEndian
	be.PutUint32(out[0x00:], textOff)
	be.PutUint32(out[0x48:], textAddr)
	be.PutUint32(out[0x90:], textSize)
	be.PutUint32(out[0x1C:], dataOff)
	be.PutUint32(out[0x64:], dataAddr)
	be.PutUint32(out[0xAC:], uint32(len(data)))
	be.PutUint32(out[0xE0:], textAddr)
	be.PutUint32(out[textOff:], 0x4E800020) // blr
	copy(out[dataOff:], data)
	return out
}
