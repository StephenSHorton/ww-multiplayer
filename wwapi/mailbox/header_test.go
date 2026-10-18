package mailbox

import (
	"hash/crc32"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// TestHeader: zero bytes (every blob before E1) are no header, ABI 0;
// the 'WWMB' magic carries the ABI, the layout version and the features
// word at +0x1A0.
func TestHeader(t *testing.T) {
	m := mem.NewFakeMem()
	h, err := ReadHeader(m)
	if err != nil {
		t.Fatal(err)
	}
	if h != (Header{}) {
		t.Errorf("zeroed header: %+v", h)
	}
	if err := m.Store(Mailbox+OffHdrMagic, []byte{'W', 'W', 'M', 'B', 0x00, 0x02, 0x01, 0x03}); err != nil {
		t.Fatal(err)
	}
	if err := m.Store(Mailbox+OffFeatures, []byte{0, 0, 0, 0x15}); err != nil {
		t.Fatal(err)
	}
	if h, err = ReadHeader(m); err != nil || !h.Present || h.ABI != 2 || h.Layout != 0x103 || h.Features != 0x15 {
		t.Errorf("WWMB header: %+v, %v", h, err)
	}
	h, err = ParseHeader([]byte{'X', 'W', 'M', 'B', 0, 2, 0, 3})
	if err != nil || h.Present || h.ABI != 0 || h.Magic != 0x58574D42 {
		t.Errorf("other magic: %+v, %v", h, err)
	}
	if _, err := ParseHeader(make([]byte, HeaderLen-1)); err == nil {
		t.Error("short header parsed")
	}
	if HeaderLen != 8 || OffHdrMagic != 0x180 || OffFeatures != 0x1A0 || MailboxSize != 0x1B0 {
		t.Errorf("header at +0x%X, %d bytes, features at +0x%X, mailbox 0x%X bytes; design §3 says +0x180, 8, +0x1A0, 0x1B0",
			OffHdrMagic, HeaderLen, OffFeatures, MailboxSize)
	}
}

// TestWriteHeader: WriteHeader writes what ReadHeader reads back, at the
// offsets main01_init stores to.
func TestWriteHeader(t *testing.T) {
	m := mem.NewFakeMem()
	want := Header{Present: true, Magic: HeaderMagic, ABI: WwmbAbi, Layout: WwmbLayout, Features: WwFeatures}
	if err := WriteHeader(m, want); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadHeader(m); err != nil || got != want {
		t.Errorf("read back %+v, %v; want %+v", got, err, want)
	}
	b, _ := m.Read(Mailbox+0x180, 0x24)
	if string(b[:4]) != "WWMB" || b[0x23] != WwFeatures {
		t.Errorf("mailbox +0x180..+0x1A4: % X", b)
	}
}

// TestBlobHash: the CRC-32 of the ranges' bytes in order (the bytes
// between them left out), and no range or an empty one refused.
func TestBlobHash(t *testing.T) {
	m := mem.NewFakeMem()
	ro := []byte{0x3F, 0x80, 0x00, 0x00}
	code := []byte{0x4E, 0x80, 0x00, 0x20, 0x60, 0x00, 0x00, 0x00}
	if err := m.Store(T2Base, ro); err != nil {
		t.Fatal(err)
	}
	if err := m.Store(T2Base+0x40, code); err != nil {
		t.Fatal(err)
	}
	want := crc32.ChecksumIEEE(append(append([]byte{}, ro...), code...))
	ranges := [][2]uint32{{T2Base, T2Base + 4}, {T2Base + 0x40, T2Base + 0x48}}
	if h, err := BlobHash(m, ranges); err != nil || h != want {
		t.Errorf("BlobHash = 0x%08X, %v; want 0x%08X", h, err, want)
	}
	if err := m.Store(T2Base+0x10, []byte{0xFF}); err != nil {
		t.Fatal(err)
	}
	if h, _ := BlobHash(m, ranges); h != want {
		t.Error("a byte between the ranges changed the hash")
	}
	if err := m.Store(T2Base+1, []byte{0x40}); err != nil {
		t.Fatal(err)
	}
	if h, _ := BlobHash(m, ranges); h == want {
		t.Error("a byte in the first range did not change the hash")
	}
	if _, err := BlobHash(m, nil); err == nil {
		t.Error("no range hashed")
	}
	if _, err := BlobHash(m, [][2]uint32{{T2Base, T2Base}}); err == nil {
		t.Error("empty range hashed")
	}
}

// FuzzParseHeader: any bytes parse without panicking; the header is
// Present exactly when the magic is there, and only then carries an ABI
// and layout version.
func FuzzParseHeader(f *testing.F) {
	f.Add([]byte{'W', 'W', 'M', 'B', 0, 1, 0, 1})
	f.Add(make([]byte, HeaderLen))
	f.Add([]byte{'W', 'W', 'M'})
	f.Fuzz(func(t *testing.T, b []byte) {
		h, err := ParseHeader(b)
		if len(b) < HeaderLen {
			if err == nil {
				t.Fatalf("%d bytes parsed", len(b))
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		magic := string(b[:4]) == "WWMB"
		if h.Present != magic || (!h.Present && (h.ABI != 0 || h.Layout != 0)) {
			t.Fatalf("% X: %+v", b[:HeaderLen], h)
		}
	})
}
