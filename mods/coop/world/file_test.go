package world

import (
	"encoding/binary"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// sampleWorld is a world with something in every section.
func sampleWorld(t testing.TB) *World {
	t.Helper()
	w := newTestWorld()
	w.ApplyGains([]Gain{
		{offItem, 0x05},
		{addr.OffWalletSize, 2},
		{uint16(addr.OffMemBit(addr.TableDRC) + addr.MemBitOffTbox + 3), 0x81},
		{uint16(addr.OffMemBit(addr.TableDRC) + addr.MemBitOffSwitch + 2), 0x10},
		{addr.OffEvent + 0x0F, 0x80},
	})
	if _, err := w.Tombstone(addr.OffOcean+7, 0x40); err != nil {
		t.Fatal(err)
	}
	if _, err := w.AddFreshBind(ID{9, 9, 9}, testCreated.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.AddFreshBind(ID{1, 2, 3}, testCreated.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	return w
}

// worldsEqual compares everything the world file keeps.
func worldsEqual(a, b *World) bool {
	return a.id == b.id && a.seq == b.seq && a.image == b.image && a.tomb == b.tomb &&
		a.created.Equal(b.created) && slices.Equal(a.FreshBinds(), b.FreshBinds())
}

func TestFileRoundTrip(t *testing.T) {
	w := sampleWorld(t)
	b := Encode(w)
	got, stripped, err := decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if !worldsEqual(got, w) || stripped != 0 {
		t.Fatalf("round trip lost something (stripped %d)", stripped)
	}
	if string(b[:8]) != FileMagic || binary.BigEndian.Uint16(b[8:]) != FileVersion {
		t.Errorf("header %q", b[:12])
	}
	if !slices.Equal(Encode(got), b) {
		t.Error("re-encoding is not byte-identical")
	}
	// A decoded world starts fail-closed on OR_MASKED bytes.
	if *got.Policy() != *DefaultPolicy() {
		t.Error("decoded world does not have the default policy")
	}
}

// reseal fixes up the body length and CRC after a test edits b, so it
// reads as whole.
func reseal(b []byte) []byte {
	binary.BigEndian.PutUint32(b[0x1C:], uint32(len(b)-headerLen))
	binary.BigEndian.PutUint32(b[0x20:], fileCRC(b))
	return b
}

func section(tag uint16, val []byte) []byte {
	b := binary.BigEndian.AppendUint16(nil, tag)
	b = binary.BigEndian.AppendUint32(b, uint32(len(val)))
	return append(b, val...)
}

// TestFileDamage: every way a file can be cut, bent or forged is refused
// as damage (so Load falls back), except a whole file of another version,
// which is refused as such (so Load does not).
func TestFileDamage(t *testing.T) {
	good := Encode(sampleWorld(t))
	edit := func(f func(b []byte) []byte) []byte { return f(slices.Clone(good)) }
	imageAt := headerLen + secHeaderLen // the image section is first
	for _, c := range []struct {
		name string
		b    []byte
		want error
	}{
		{"empty", nil, ErrCorrupt},
		{"header cut off", good[:headerLen-1], ErrCorrupt},
		{"body cut off", good[:len(good)-1], ErrCorrupt},
		{"trailing byte", append(slices.Clone(good), 0), ErrCorrupt},
		{"bad magic", edit(func(b []byte) []byte { b[0] = 'X'; return reseal(b) }), ErrCorrupt},
		{"image bit flipped", edit(func(b []byte) []byte { b[imageAt+addr.OffGetItem] ^= 0x40; return b }), ErrCorrupt},
		{"seq bit flipped", edit(func(b []byte) []byte { b[0x14+7] ^= 1; return b }), ErrCorrupt},
		{"version bit flipped", edit(func(b []byte) []byte { b[0x09] ^= 2; return b }), ErrCorrupt},
		{"version 2", edit(func(b []byte) []byte { b[0x09] = 2; return reseal(b) }), ErrVersion},
		{"version 0", edit(func(b []byte) []byte { b[0x09] = 0; return reseal(b) }), ErrVersion},
		{"flags set", edit(func(b []byte) []byte { b[0x0B] = 1; return reseal(b) }), ErrVersion},
		{"zero world ID", edit(func(b []byte) []byte { clear(b[0x0C:0x14]); return reseal(b) }), ErrCorrupt},
		{"no image", reseal(append(slices.Clone(good[:headerLen]), section(secMeta, make([]byte, 8))...)), ErrCorrupt},
		{"short image", reseal(append(slices.Clone(good[:headerLen]), section(secImage, make([]byte, 10))...)), ErrCorrupt},
		{"image twice", reseal(append(slices.Clone(good), section(secImage, make([]byte, ImageSize))...)), ErrCorrupt},
		{"unknown section", reseal(append(slices.Clone(good), section(0x0042, nil)...)), ErrCorrupt},
		{"section overruns", reseal(append(slices.Clone(good), 0x00, 0x05, 0, 0, 0, 9, 1)), ErrCorrupt},
		{"stray bytes", reseal(append(slices.Clone(good), 0x00, 0x05)), ErrCorrupt},
		{"tombstones not ascending", reseal(append(slices.Clone(good[:headerLen]),
			append(section(secImage, make([]byte, ImageSize)), section(secTombstones, []byte{0, 9, 1, 0, 8, 1})...)...)), ErrCorrupt},
		{"tombstone mask 0", reseal(append(slices.Clone(good[:headerLen]),
			append(section(secImage, make([]byte, ImageSize)), section(secTombstones, []byte{0, 9, 0})...)...)), ErrCorrupt},
		{"fresh nonce 0", reseal(append(slices.Clone(good[:headerLen]),
			append(section(secImage, make([]byte, ImageSize)), section(secFresh, make([]byte, freshLen))...)...)), ErrCorrupt},
		{"too many fresh binds", reseal(append(slices.Clone(good[:headerLen]),
			append(section(secImage, make([]byte, ImageSize)), section(secFresh, make([]byte, freshLen*(MaxFreshBinds+1)))...)...)), ErrCorrupt},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := Decode(c.b)
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if c.want == ErrCorrupt && errors.Is(err, ErrVersion) {
				t.Fatal("damage reads as another version")
			}
		})
	}
}

// TestFileStripsAndSkips: an optional section this build doesn't know is
// skipped, and bits the class table doesn't hold (or the file's own
// tombstones refuse) are dropped and counted, not taken as damage.
func TestFileStripsAndSkips(t *testing.T) {
	var img Image
	img[addr.OffLife] = 0x0C                                // per player: 2 bits
	img[addr.OffMemBit(0)+addr.MemBitOffDungeonItem] = 0xC1 // 0xC0 outside the mask: 2 bits
	img[offItem] = 0x03                                     // bit 1 tombstoned below: 1 bit
	img[addr.OffGetItem] = 0x05                             // 0x04 is bottle history, outside the mask: 1 bit
	tomb := []byte{0, addr.OffLife, 0x01, 0, offItem, 0x02} // the first is outside every mask: 1 bit
	hdr := slices.Clone(Encode(New(testID, time.Time{}))[:headerLen])
	b := reseal(append(hdr, slices.Concat(section(secImage, img[:]), section(secTombstones, tomb), section(0x8123, []byte("future")))...))
	w, stripped, err := decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if stripped != 2+2+1+1+1 {
		t.Errorf("stripped %d bits, want 7", stripped)
	}
	got := w.Image()
	if got[addr.OffLife] != 0 || got[addr.OffMemBit(0)+addr.MemBitOffDungeonItem] != 0x01 || got[offItem] != 0x01 || got[addr.OffGetItem] != 0x01 {
		t.Errorf("image after stripping: life %02x dungeon %02x getItem[3] %02x getItem[0] %02x", got[addr.OffLife],
			got[addr.OffMemBit(0)+addr.MemBitOffDungeonItem], got[offItem], got[addr.OffGetItem])
	}
	if ts := w.Tombstones(); !slices.Equal(ts.Entries(), []Gain{{offItem, 0x02}}) {
		t.Errorf("tombstones %v", ts.Entries())
	}
}
