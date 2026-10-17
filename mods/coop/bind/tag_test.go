package bind

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

var (
	worldA = WorldID{0xA1, 0xA2, 0xA3, 0xA4, 0xA5, 0xA6, 0xA7, 0xA8}
	worldB = WorldID{0xB1, 0xB2, 0xB3, 0xB4, 0xB5, 0xB6, 0xB7, 0xB8}
	nonce1 = Nonce{1, 2, 3, 4, 5, 6, 7, 8}
)

func sampleTag() Tag {
	return Tag{
		Layout: LayoutV1, Slot: 2, ClearCount: 0,
		World: worldA, Nonce: nonce1,
		WorldSeq: 0x0102030405060708, NameHash: NameHash([]byte("Link")),
	}
}

// TestTagLayout pins every field at the offset §5.2 gives it, and the
// tag's place in dSv_save_c.
func TestTagLayout(t *testing.T) {
	if TagRegion.Start != 0x803C532C || TagRegion.Size() != 0x30 || TagRegion.End > addr.Reserve+addr.ReserveSize {
		t.Fatalf("the tag is at 0x%08X-0x%08X", TagRegion.Start, TagRegion.End)
	}
	tg := sampleTag()
	b := tg.Encode()
	want := []struct {
		name string
		off  int
		data []byte
	}{
		{"magic", 0x00, []byte("WWCO")},
		{"layout, flags, slot, clear count", 0x04, []byte{1, 0, 2, 0}},
		{"worldID", 0x08, worldA[:]},
		{"bindNonce", 0x10, nonce1[:]},
		{"worldSeq", 0x18, []byte{1, 2, 3, 4, 5, 6, 7, 8}},
		{"nameHash", 0x20, binary.BigEndian.AppendUint32(nil, NameHash([]byte("Link")))},
		{"reserved u32", 0x24, []byte{0, 0, 0, 0}},
		{"CRC32", 0x28, binary.BigEndian.AppendUint32(nil, crc32.ChecksumIEEE(b[:0x28]))},
		{"reserved, kept zero", 0x2C, []byte{0, 0, 0, 0}},
	}
	for _, w := range want {
		if got := b[w.off : w.off+len(w.data)]; !bytes.Equal(got, w.data) {
			t.Errorf("%s at +0x%02X = % X, want % X", w.name, w.off, got, w.data)
		}
	}
	got, err := Decode(b[:])
	if err != nil || got != tg {
		t.Errorf("Decode(Encode(t)) = %+v, %v; want %+v", got, err, tg)
	}
	// Decode takes the whole 0x50-byte mReserve, ignoring its tail.
	whole := append(b[:], bytes.Repeat([]byte{0xEE}, addr.ReserveSize-TagSize)...)
	if got, err := Decode(whole); err != nil || got != tg {
		t.Errorf("Decode(mReserve) = %+v, %v", got, err)
	}
}

// TestDecodeRejects: every way the bytes can fail rule V1, and which
// error says so.
func TestDecodeRejects(t *testing.T) {
	good := sampleTag().Encode()
	// mut changes a copy of good; recrc recomputes the CRC after it, as a
	// deliberate writer would.
	mk := func(recrc bool, mut func(b []byte)) []byte {
		b := good
		mut(b[:])
		if recrc {
			binary.BigEndian.PutUint32(b[offCRC:], crc32.ChecksumIEEE(b[:offCRC]))
		}
		return b[:]
	}
	cases := []struct {
		name string
		b    []byte
		want error
	}{
		{"all zero", make([]byte, TagSize), ErrEmpty},
		{"short", good[:TagSize-1], ErrDamaged},
		{"another tool's bytes", mk(false, func(b []byte) { copy(b, "AP\x00\x01") }), ErrMagic},
		{"one stray byte", mk(false, func(b []byte) { clear(b); b[offNonce] = 1 }), ErrMagic},
		{"layout 0", mk(true, func(b []byte) { b[offLayout] = 0 }), ErrDamaged},
		{"newer layout", mk(false, func(b []byte) { b[offLayout] = 2 }), ErrLayout},
		{"flipped bit", mk(false, func(b []byte) { b[offWorld+3] ^= 0x10 }), ErrDamaged},
		{"CRC flipped", mk(false, func(b []byte) { b[offCRC] ^= 1 }), ErrDamaged},
		{"flags set", mk(true, func(b []byte) { b[offFlags] = 1 }), ErrLayout},
		{"reserved word set", mk(true, func(b []byte) { b[offReserved+3] = 1 }), ErrLayout},
		{"pad set", mk(false, func(b []byte) { b[offPad+1] = 1 }), ErrDamaged},
		{"zero world", mk(true, func(b []byte) { clear(b[offWorld : offWorld+8]) }), ErrDamaged},
		{"zero nonce", mk(true, func(b []byte) { clear(b[offNonce : offNonce+8]) }), ErrDamaged},
	}
	for _, c := range cases {
		_, err := Decode(c.b)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: Decode error %v, want %v", c.name, err, c.want)
		}
	}
}

// TestNameHash: FNV-1a over the name before its NUL, so the stale tail
// strcpy leaves behind a shorter name doesn't matter.
func TestNameHash(t *testing.T) {
	cases := []struct {
		in   string
		want uint32
	}{
		{"", 0x811C9DC5}, // the FNV-1a 32-bit offset basis
		{"a", 0xE40C292C},
		{"Link", NameHash([]byte("Link\x00\x00\x00"))},
		{"Link\x00ra\x00", NameHash([]byte("Link"))},
		{"ABCDEFGHIJKLMNOPQ", NameHash([]byte("ABCDEFGHIJKLMNOPQRS"))}, // 17 bytes, no NUL: all hashed, nothing past
	}
	for _, c := range cases {
		if got := NameHash([]byte(c.in)); got != c.want {
			t.Errorf("NameHash(%q) = %08x, want %08x", c.in, got, c.want)
		}
	}
	if NameHash([]byte("Link")) == NameHash([]byte("Tetra")) {
		t.Error("two names hash alike")
	}
	if NameHash([]byte("ABCDEFGHIJKLMNOP")) == NameHash([]byte("ABCDEFGHIJKLMNOPQ")) {
		t.Error("the 17th byte is not hashed")
	}
}

func TestWorldIDs(t *testing.T) {
	w, err := ParseWorldID(worldA.String())
	if err != nil || w != worldA {
		t.Errorf("ParseWorldID(%s) = %v, %v", worldA, w, err)
	}
	if worldA.String() != "a1a2a3a4a5a6a7a8" || nonce1.String() != "0102030405060708" {
		t.Errorf("String() = %s, %s", worldA, nonce1)
	}
	for _, bad := range []string{"", "a1a2", "a1a2a3a4a5a6a7a8a9", "zza2a3a4a5a6a7a8", "0000000000000000"} {
		if _, err := ParseWorldID(bad); err == nil {
			t.Errorf("ParseWorldID(%q) succeeded", bad)
		}
	}

	seen := map[WorldID]bool{}
	for range 64 {
		w, err := NewWorldID()
		if err != nil || w.IsZero() || seen[w] {
			t.Fatalf("NewWorldID = %v, %v (seen %v)", w, err, seen[w])
		}
		seen[w] = true
		if n, err := NewNonce(); err != nil || n.IsZero() {
			t.Fatalf("NewNonce = %v, %v", n, err)
		}
	}

	defer func(r func([]byte) (int, error)) { randRead = r }(randRead)
	randRead = func(b []byte) (int, error) { clear(b); return len(b), nil }
	if _, err := NewNonce(); err == nil {
		t.Error("NewNonce returned a zero nonce")
	}
	randRead = func(b []byte) (int, error) { return 0, errors.New("no entropy") }
	if _, err := NewWorldID(); err == nil {
		t.Error("NewWorldID ignored a failing random source")
	}
}
