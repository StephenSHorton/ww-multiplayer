package stagetest

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestNameHash(t *testing.T) {
	cases := map[string]uint16{
		"":    0,
		".":   0x2E,
		"..":  0x2E + 0x2E*3,
		"ab":  'b' + 'a'*3,
		"AB":  'b' + 'a'*3, // hashed lowercased, as CArcName stores it
		"abc": 'c' + ('b'+'a'*3)*3,
	}
	for name, want := range cases {
		if got := NameHash(name); got != want {
			t.Errorf("NameHash(%q) = 0x%04x, want 0x%04x", name, got, want)
		}
	}
	long := bytes.Repeat([]byte("z"), 40) // wraps 16 bits
	var want uint16
	for range long {
		want = 'z' + want*3
	}
	if got := NameHash(string(long)); got != want {
		t.Errorf("NameHash(40 z) = 0x%04x, want 0x%04x", got, want)
	}
}

func TestNodeType(t *testing.T) {
	for name, want := range map[string]string{"dzs": "DZS ", "dzr": "DZR ", "event": "EVEN", "bdl": "BDL ", "a": "A   "} {
		if got := nodeType(name); got != want {
			t.Errorf("nodeType(%q) = %q, want %q", name, got, want)
		}
	}
}

// The builder's header and info block describe the archive it wrote.
func TestRARCHeader(t *testing.T) {
	b := RARC("", File{Path: "d/f", Data: []byte("data")})
	be := binary.BigEndian
	if string(b[:4]) != "RARC" || be.Uint32(b[4:]) != uint32(len(b)) || be.Uint32(b[8:]) != 0x20 {
		t.Fatalf("header % x", b[:0x20])
	}
	info := b[0x20:0x40]
	if nodes, ents := be.Uint32(info[0:]), be.Uint32(info[8:]); nodes != 2 || ents != 6 {
		t.Fatalf("%d nodes, %d entries; want 2 (root, d) and 6 (d . .. f . ..)", nodes, ents)
	}
	if !bytes.Contains(b, []byte(".\x00..\x00archive\x00")) {
		t.Error(`string table doesn't start ".", "..", then the default root name`)
	}
	data := 0x20 + int(be.Uint32(b[0x0C:]))
	if data%0x20 != 0 || string(b[data:data+4]) != "data" {
		t.Errorf("file data at 0x%x: % x", data, b[data:min(len(b), data+4)])
	}
}
