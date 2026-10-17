// Package stagetest builds synthetic Yaz0 streams, RARC archives, DZS/DZR
// chunk files, whole stages on an in-memory disc or a disc image, and a
// main.dol carrying an l_objectName table, so the stage readers in
// wwapi/stage, and the packages built on them, are tested without any
// Nintendo data. The builders write the layouts wwapi/stage documents and
// cites (yaz0.go, rarc.go, dzx.go, stage.go, objname.go);
// wwapi/stage's tests check that everything built here parses back.
package stagetest

import (
	"encoding/binary"
	"strings"
)

// Yaz0 compresses data as a Yaz0 stream: a greedy longest match over the
// last 0x1000 bytes wherever one of at least 3 bytes exists (the two-byte
// form for 3-17 bytes, the three-byte form for 18-273, overlapping its own
// output for runs), a literal byte elsewhere. Matches are found through
// hash chains over 3-byte prefixes, so multi-MiB fixtures encode quickly.
func Yaz0(data []byte) []byte {
	const (
		window = 0x1000
		maxLen = 0x111 // 273
		chain  = 64    // candidates tried per position
	)
	out := make([]byte, 0x10, 0x10+len(data)+len(data)/8+1)
	copy(out, "Yaz0")
	binary.BigEndian.PutUint32(out[4:], uint32(len(data)))

	key := func(i int) uint32 { return uint32(data[i])<<16 | uint32(data[i+1])<<8 | uint32(data[i+2]) }
	head := make(map[uint32]int)
	prev := make([]int, len(data))
	insert := func(i int) {
		if i+3 > len(data) {
			return
		}
		prev[i] = -1
		if h, ok := head[key(i)]; ok {
			prev[i] = h
		}
		head[key(i)] = i
	}
	match := func(i int) (dist, n int) {
		if i+3 > len(data) {
			return 0, 0
		}
		c, ok := head[key(i)]
		for tries := 0; ok && c >= 0 && i-c <= window && tries < chain; tries++ {
			l := 0
			for l < maxLen && i+l < len(data) && data[c+l] == data[i+l] {
				l++
			}
			if l > n {
				dist, n = i-c, l
				if l == maxLen {
					break
				}
			}
			c = prev[c]
		}
		return dist, n
	}

	code, bit := 0, byte(0) // the current code byte's index, and its next bit
	for i := 0; i < len(data); {
		if bit == 0 {
			code, bit = len(out), 0x80
			out = append(out, 0)
		}
		if dist, n := match(i); n >= 3 {
			d := dist - 1
			if n < 0x12 {
				out = append(out, byte(n-2)<<4|byte(d>>8), byte(d))
			} else {
				out = append(out, byte(d>>8), byte(d), byte(n-0x12))
			}
			for j := i; j < i+n; j++ {
				insert(j)
			}
			i += n
		} else {
			out[code] |= bit
			out = append(out, data[i])
			insert(i)
			i++
		}
		bit >>= 1
	}
	return out
}

// File is one file of an archive RARC builds.
type File struct {
	Path string // slash-separated, from the root: "dzs/stage.dzs"
	Data []byte
	Yaz0 bool // store Data Yaz0-compressed, with attributes 0x01|0x04|0x80
}

// RARC builds a RARC archive whose root node is named root (or "archive"
// when root is empty) holding files. Every directory a path spells becomes
// a node, typed the way the game's resource loader looks nodes up: its
// name upper-cased and space-padded or cut to four bytes, so "dzs" is
// "DZS " (the archive tools' convention, inferred; the root is "ROOT").
// Nodes are numbered breadth first. A node's entries are its files and
// subdirectories in the order they first appear in files, then "." and
// "..". File IDs are entry indices, names are hashed as CArcName hashes
// them, and the tables and each file's data are 0x20-aligned.
func RARC(root string, files ...File) []byte {
	if root == "" {
		root = "archive"
	}
	type item struct {
		name string
		file *File // nil for a directory
		dir  int   // a directory's node index
	}
	type node struct {
		name, typ string
		parent    int
		items     []item
	}
	nodes := []*node{{name: root, typ: "ROOT", parent: -1}}
	subdir := func(n int, name string) int {
		for _, it := range nodes[n].items {
			if it.file == nil && it.name == name {
				return it.dir
			}
		}
		nodes = append(nodes, &node{name: name, typ: nodeType(name), parent: n})
		k := len(nodes) - 1
		nodes[n].items = append(nodes[n].items, item{name: name, dir: k})
		return k
	}
	for i := range files {
		parts := strings.Split(files[i].Path, "/")
		n := 0
		for _, p := range parts[:len(parts)-1] {
			n = subdir(n, p)
		}
		nodes[n].items = append(nodes[n].items, item{name: parts[len(parts)-1], file: &files[i]})
	}
	// subdir appends in depth-first discovery order; renumber breadth first.
	order := []int{0}
	for q := 0; q < len(order); q++ {
		for _, it := range nodes[order[q]].items {
			if it.file == nil {
				order = append(order, it.dir)
			}
		}
	}
	index := make([]int, len(nodes))
	for newIdx, old := range order {
		index[old] = newIdx
	}

	// String table: "." and ".." first, then every other name.
	strs := []byte(".\x00..\x00")
	name := func(s string) uint32 {
		off := uint32(len(strs))
		strs = append(append(strs, s...), 0)
		return off
	}
	type entry struct {
		id, hash  uint16
		attr      byte
		nameOff   uint32
		off, size uint32
		data      []byte // a file's stored bytes
		isFile    bool
	}
	nodeTab := make([]byte, 0, len(nodes)*0x10)
	var ents []entry
	for _, old := range order {
		n := nodes[old]
		nodeTab = append(nodeTab, n.typ...)
		nodeTab = binary.BigEndian.AppendUint32(nodeTab, name(n.name))
		nodeTab = binary.BigEndian.AppendUint16(nodeTab, NameHash(n.name))
		nodeTab = binary.BigEndian.AppendUint16(nodeTab, uint16(len(n.items)+2))
		nodeTab = binary.BigEndian.AppendUint32(nodeTab, uint32(len(ents)))
		for _, it := range n.items {
			e := entry{id: 0xFFFF, hash: NameHash(it.name), nameOff: name(it.name)}
			if it.file != nil {
				e.isFile, e.attr, e.data = true, 0x01, it.file.Data
				if it.file.Yaz0 {
					e.attr, e.data = 0x01|0x04|0x80, Yaz0(it.file.Data)
				}
				e.id, e.size = uint16(len(ents)), uint32(len(e.data))
			} else {
				e.attr, e.off, e.size = 0x02, uint32(index[it.dir]), 0x10
			}
			ents = append(ents, e)
		}
		parent := uint32(0xFFFFFFFF)
		if n.parent >= 0 {
			parent = uint32(index[n.parent])
		}
		ents = append(ents,
			entry{id: 0xFFFF, hash: NameHash("."), attr: 0x02, nameOff: 0, off: uint32(index[old]), size: 0x10},
			entry{id: 0xFFFF, hash: NameHash(".."), attr: 0x02, nameOff: 2, off: parent, size: 0x10})
	}

	// Layout: header, info block, nodes, entries, strings, file data.
	const info = 0x20
	nodeOff := 0x20
	entOff := align(nodeOff+len(nodeTab), 0x20)
	strOff := align(entOff+len(ents)*0x14, 0x20)
	strLen := align(len(strs), 0x20)
	dataStart := align(info+strOff+strLen, 0x20)
	pos := 0
	for i := range ents {
		if ents[i].isFile {
			ents[i].off = uint32(pos)
			pos = align(pos+len(ents[i].data), 0x20)
		}
	}
	out := make([]byte, dataStart+pos)
	be := binary.BigEndian
	copy(out, "RARC")
	be.PutUint32(out[0x04:], uint32(len(out)))
	be.PutUint32(out[0x08:], info)
	be.PutUint32(out[0x0C:], uint32(dataStart-info))
	be.PutUint32(out[0x10:], uint32(pos))
	ib := out[info:]
	be.PutUint32(ib[0x00:], uint32(len(nodes)))
	be.PutUint32(ib[0x04:], uint32(nodeOff))
	be.PutUint32(ib[0x08:], uint32(len(ents)))
	be.PutUint32(ib[0x0C:], uint32(entOff))
	be.PutUint32(ib[0x10:], uint32(strLen))
	be.PutUint32(ib[0x14:], uint32(strOff))
	be.PutUint16(ib[0x18:], uint16(len(ents)))
	ib[0x1A] = 1 // file IDs are entry indices
	copy(ib[nodeOff:], nodeTab)
	for i, e := range ents {
		eb := ib[entOff+i*0x14:]
		be.PutUint16(eb[0x00:], e.id)
		be.PutUint16(eb[0x02:], e.hash)
		be.PutUint32(eb[0x04:], uint32(e.attr)<<24|e.nameOff)
		be.PutUint32(eb[0x08:], e.off)
		be.PutUint32(eb[0x0C:], e.size)
		if e.isFile {
			copy(out[dataStart+int(e.off):], e.data)
		}
	}
	copy(ib[strOff:], strs)
	return out
}

// NameHash is the hash RARC stores with each name: tolower(c) + hash*3
// over its bytes, in 16 bits (CArcName::store, JKRArchivePri.cpp:189-203).
func NameHash(name string) uint16 {
	var h uint16
	for i := 0; i < len(name); i++ {
		c := name[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		h = uint16(c) + h*3
	}
	return h
}

// nodeType is the four-character node type of a directory named name.
func nodeType(name string) string {
	t := strings.ToUpper(name) + "    "
	return t[:4]
}

func align(v, a int) int { return (v + a - 1) / a * a }
