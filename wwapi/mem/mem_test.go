package mem

import (
	"bytes"
	"errors"
	"sync"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

func TestCheckRange(t *testing.T) {
	ok := []struct {
		a uint32
		n int
	}{
		{addr.MEM1Base, 1},
		{addr.MEM1End - 1, 1},
		{addr.MEM1End - 4, 4},
		{addr.MEM1Base, addr.MEM1Size},
		{addr.GameInfo, addr.SaveInfoSize},
	}
	for _, c := range ok {
		if err := CheckRange(c.a, c.n); err != nil {
			t.Errorf("CheckRange(0x%08X, %d) = %v, want nil", c.a, c.n, err)
		}
	}
	bad := []struct {
		a uint32
		n int
	}{
		{addr.MEM1Base - 1, 1}, // just below
		{addr.MEM1End, 1},      // just past
		{addr.MEM1End - 1, 2},  // straddles the end
		{addr.MEM1Base, addr.MEM1Size + 1},
		{0x00000000, 4},     // a null pointer read out of the game
		{0xFFFFFFFE, 4},     // would wrap a uint32
		{addr.MEM1Base, 0},  // empty
		{addr.MEM1Base, -1}, // negative
	}
	for _, c := range bad {
		err := CheckRange(c.a, c.n)
		if !errors.Is(err, ErrOutOfRange) {
			t.Errorf("CheckRange(0x%08X, %d) = %v, want ErrOutOfRange", c.a, c.n, err)
			continue
		}
		var re *RangeError
		if !errors.As(err, &re) || re.Addr != c.a || re.N != c.n {
			t.Errorf("CheckRange(0x%08X, %d) error = %#v", c.a, c.n, err)
		}
	}
}

func TestFakeMemReadWrite(t *testing.T) {
	f := NewFakeMem()

	// Untouched memory reads as zero, anywhere in MEM1.
	for _, a := range []uint32{addr.MEM1Base, addr.GameInfo, addr.MEM1End - 8} {
		b, err := f.Read(a, 8)
		if err != nil || !bytes.Equal(b, make([]byte, 8)) {
			t.Fatalf("Read(0x%08X) = %x, %v; want zeros", a, b, err)
		}
	}

	// A write straddling a page boundary reads back whole, and leaves its
	// neighbours alone.
	at := uint32(0x80001000 - 3)
	data := []byte{1, 2, 3, 4, 5, 6, 7}
	if err := f.Write(at, data); err != nil {
		t.Fatal(err)
	}
	got, err := f.Read(at-1, len(data)+2)
	if err != nil {
		t.Fatal(err)
	}
	if want := append(append([]byte{0}, data...), 0); !bytes.Equal(got, want) {
		t.Fatalf("read back %x, want %x", got, want)
	}

	// Read hands out a copy, and Write copies its input.
	got[1] = 0xEE
	data[0] = 0xEE
	if b, _ := f.Read(at, 1); b[0] != 1 {
		t.Fatalf("memory changed through a returned or passed slice: %x", b)
	}

	// A large read spanning many pages, some never allocated.
	big, err := f.Read(addr.MEM1Base, 5*fakePageSize+17)
	if err != nil || len(big) != 5*fakePageSize+17 {
		t.Fatalf("big read: %d bytes, %v", len(big), err)
	}
	if !bytes.Equal(big[at-addr.MEM1Base:at-addr.MEM1Base+3], []byte{1, 2, 3}) {
		t.Fatal("big read misses the written bytes")
	}

	// The last bytes of MEM1 are writable, like on the console.
	if err := f.Write(addr.MEM1End-2, []byte{0xAB, 0xCD}); err != nil {
		t.Fatal(err)
	}
	if v, _ := ReadU16(f, addr.MEM1End-2); v != 0xABCD {
		t.Fatalf("last u16 = %#x", v)
	}
}

func TestFakeMemRefusesOutOfRange(t *testing.T) {
	f := NewFakeMem()
	f.Record(true)
	if _, err := f.Read(addr.MEM1End-1, 2); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("straddling read: %v", err)
	}
	if _, err := f.Read(0, 4); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("null read: %v", err)
	}
	if err := f.Write(addr.MEM1End-1, []byte{1, 2}); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("straddling write: %v", err)
	}
	if err := f.Write(addr.MEM1Base, nil); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("empty write: %v", err)
	}
	if err := f.Store(addr.MEM1Base-4, []byte{1, 2, 3, 4}); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("store below MEM1: %v", err)
	}
	// A refused write changes nothing: the in-range byte of the straddling
	// write is still zero, and nothing was recorded.
	if b, _ := ReadU8(f, addr.MEM1End-1); b != 0 {
		t.Errorf("refused write landed: %#x", b)
	}
	if n := len(f.Writes()); n != 0 {
		t.Errorf("%d refused writes recorded", n)
	}
}

func TestFakeMemRecording(t *testing.T) {
	f := NewFakeMem()

	// Off by default.
	if err := f.Write(addr.Life, []byte{0, 12}); err != nil {
		t.Fatal(err)
	}
	if n := len(f.Writes()); n != 0 {
		t.Fatalf("recorded %d writes before Record(true)", n)
	}

	f.Record(true)
	in := []byte{0x00, 0x10}
	if err := f.Write(addr.ItemMaxLifeCount, in); err != nil {
		t.Fatal(err)
	}
	in[1] = 0xFF // the record keeps its own copy
	if err := f.Store(addr.LiveMemBit, []byte{0x80}); err != nil {
		t.Fatal(err) // the game's write: never recorded
	}
	if err := WriteU8(f, addr.LiveKeyNum, 2); err != nil {
		t.Fatal(err)
	}

	w := f.Writes()
	if len(w) != 2 {
		t.Fatalf("recorded %d writes, want 2: %+v", len(w), w)
	}
	if w[0].Addr != addr.ItemMaxLifeCount || !bytes.Equal(w[0].Data, []byte{0x00, 0x10}) || w[0].End() != addr.ItemMaxLifeCount+2 {
		t.Errorf("first record = %+v", w[0])
	}
	if w[1].Addr != addr.LiveKeyNum || !bytes.Equal(w[1].Data, []byte{2}) {
		t.Errorf("second record = %+v", w[1])
	}
	w[0].Data[0] = 0xEE // Writes hands out copies
	if f.Writes()[0].Data[0] != 0 {
		t.Error("Writes returned the recorder's own slice")
	}

	f.Record(false)
	_ = WriteU8(f, addr.LiveKeyNum, 3)
	if n := len(f.Writes()); n != 2 {
		t.Errorf("Record(false) kept recording: %d writes", n)
	}
	f.ResetWrites()
	if n := len(f.Writes()); n != 0 {
		t.Errorf("ResetWrites left %d writes", n)
	}
	// Stored and written bytes are both in memory.
	if b, _ := ReadU8(f, addr.LiveMemBit); b != 0x80 {
		t.Errorf("stored byte = %#x", b)
	}
	if b, _ := ReadU8(f, addr.LiveKeyNum); b != 3 {
		t.Errorf("written byte = %#x", b)
	}
}

func TestHelpersBigEndian(t *testing.T) {
	f := NewFakeMem()
	if err := WriteU32(f, mailbox.SpawnTrigger, 0x11223344); err != nil {
		t.Fatal(err)
	}
	if b, _ := f.Read(mailbox.SpawnTrigger, 4); !bytes.Equal(b, []byte{0x11, 0x22, 0x33, 0x44}) {
		t.Fatalf("WriteU32 stored %x", b)
	}
	if v, err := ReadU32(f, mailbox.SpawnTrigger); err != nil || v != 0x11223344 {
		t.Fatalf("ReadU32 = %#x, %v", v, err)
	}
	if v, err := ReadU16(f, mailbox.SpawnTrigger+2); err != nil || v != 0x3344 {
		t.Fatalf("ReadU16 = %#x, %v", v, err)
	}
	if err := WriteU16(f, addr.MaxLife, 0x000C); err != nil {
		t.Fatal(err)
	}
	if v, _ := ReadU8(f, addr.MaxLife+1); v != 0x0C {
		t.Fatalf("WriteU16 low byte = %#x", v)
	}

	// Stage names are char[8], NUL-padded, or all 8 bytes when the name
	// fills the field.
	_ = f.Store(addr.CurStageName, []byte("sea\x00\x00\x00\x00\x00"))
	if s, err := ReadString(f, addr.CurStageName, addr.StageNameLen); err != nil || s != "sea" {
		t.Errorf("ReadString = %q, %v", s, err)
	}
	_ = f.Store(addr.CurStageName, []byte("M_NewD2X"))
	if s, _ := ReadString(f, addr.CurStageName, addr.StageNameLen); s != "M_NewD2X" {
		t.Errorf("unterminated ReadString = %q", s)
	}

	// Errors pass through every helper.
	if _, err := ReadU32(f, addr.MEM1End-2); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("ReadU32 past MEM1: %v", err)
	}
	if _, err := ReadString(f, 0, 8); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("ReadString at 0: %v", err)
	}
	if err := WriteU32(f, addr.MEM1End-2, 1); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("WriteU32 past MEM1: %v", err)
	}
}

// TestDolphinMemChecksRangeFirst: the adapter refuses out-of-range access
// before it touches the Dolphin, so a nil one never gets dereferenced. On
// a live Dolphin those calls would read or write outside emulated RAM.
func TestDolphinMemChecksRangeFirst(t *testing.T) {
	m := NewDolphinMem(nil)
	if _, err := m.Read(0x00000000, 4); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("Read(0) = %v, want ErrOutOfRange", err)
	}
	if _, err := m.Read(addr.MEM1End-1, 2); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("straddling Read = %v, want ErrOutOfRange", err)
	}
	if err := m.Write(addr.MEM1End, []byte{1}); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("Write past MEM1 = %v, want ErrOutOfRange", err)
	}
	if err := m.Write(addr.MEM1Base, nil); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("empty Write = %v, want ErrOutOfRange", err)
	}
}

// TestFakeMemConcurrent runs readers, writers and the recorder at once;
// with -race it checks FakeMem's locking.
func TestFakeMemConcurrent(t *testing.T) {
	f := NewFakeMem()
	f.Record(true)
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			base := addr.GameInfo + uint32(g)*fakePageSize
			for i := 0; i < 200; i++ {
				_ = WriteU32(f, base+uint32(i%64)*4, uint32(i))
				_ = f.Store(base+0x800, []byte{byte(i)})
				if _, err := f.Read(base, fakePageSize+8); err != nil {
					t.Error(err)
					return
				}
				_ = f.Writes()
			}
		}(g)
	}
	wg.Wait()
	if n := len(f.Writes()); n != 4*200 {
		t.Errorf("recorded %d writes, want %d", n, 4*200)
	}
}

// readOnly hides FakeMem's Write: the read helpers need only a Reader.
type readOnly struct{ r Reader }

func (o readOnly) Read(a uint32, n int) ([]byte, error) { return o.r.Read(a, n) }

func TestSignedAndFloatHelpers(t *testing.T) {
	f := NewFakeMem()
	base := addr.MEM1Base + 0x100
	if err := WriteS8(f, base, -2); err != nil {
		t.Fatal(err)
	}
	if err := WriteS16(f, base+2, -0x1234); err != nil {
		t.Fatal(err)
	}
	if err := WriteF32(f, base+4, -1.5); err != nil {
		t.Fatal(err)
	}
	if b, _ := f.Read(base, 8); !bytes.Equal(b, []byte{0xFE, 0, 0xED, 0xCC, 0xBF, 0xC0, 0, 0}) {
		t.Fatalf("bytes % X", b)
	}
	r := readOnly{f}
	if v, err := ReadS8(r, base); err != nil || v != -2 {
		t.Errorf("ReadS8 = %d, %v", v, err)
	}
	if v, err := ReadS16(r, base+2); err != nil || v != -0x1234 {
		t.Errorf("ReadS16 = %d, %v", v, err)
	}
	if v, err := ReadF32(r, base+4); err != nil || v != -1.5 {
		t.Errorf("ReadF32 = %v, %v", v, err)
	}
	if _, err := ReadF32(r, addr.MEM1End-2); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("ReadF32 past MEM1: %v", err)
	}
	if err := WriteF32(f, addr.MEM1End-2, 1); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("WriteF32 past MEM1: %v", err)
	}
}

func TestSliceHelpers(t *testing.T) {
	b := make([]byte, 4)
	PutU16BE(b, 0xABCD)
	if U16BE(b) != 0xABCD || b[0] != 0xAB {
		t.Errorf("U16BE % X", b)
	}
	PutU32BE(b, 0x11223344)
	if U32BE(b) != 0x11223344 || b[0] != 0x11 {
		t.Errorf("U32BE % X", b)
	}
	PutS16BE(b, -1)
	if S16BE(b) != -1 || b[0] != 0xFF || b[1] != 0xFF {
		t.Errorf("S16BE % X", b)
	}
	PutF32BE(b, 1)
	if F32BE(b) != 1 || !bytes.Equal(b, []byte{0x3F, 0x80, 0, 0}) {
		t.Errorf("F32BE % X", b)
	}
}
