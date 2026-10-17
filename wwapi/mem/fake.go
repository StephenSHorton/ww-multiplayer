package mem

import (
	"slices"
	"sync"
)

// fakePageSize is FakeMem's allocation unit. Pages are created on first
// write, so a FakeMem costs a few KiB however much of MEM1 it covers.
const fakePageSize = 0x1000

// FakeMem is a Mem over all of MEM1 (0x80000000-0x81800000) held in Go
// memory: zero until written, and refusing out-of-range access with the
// same error a live *emu.Dolphin gives. It is safe for concurrent use.
//
// FakeMem has two write paths. Write, the Mem method, is a tool write: the
// kind the co-op audits care about, and recorded while Record(true) is in
// effect. Store is the emulated game's own write (a fake game's onSwitch,
// a test fixture) and is never recorded.
type FakeMem struct {
	mu        sync.Mutex
	pages     map[uint32]*[fakePageSize]byte // keyed by page base address
	recording bool
	writes    []WriteRecord
}

var _ Mem = (*FakeMem)(nil)

// WriteRecord is one recorded Write.
type WriteRecord struct {
	Addr uint32
	Data []byte
}

// End is the first address past the write.
func (w WriteRecord) End() uint32 { return w.Addr + uint32(len(w.Data)) }

// NewFakeMem returns an all-zero MEM1.
func NewFakeMem() *FakeMem {
	return &FakeMem{pages: make(map[uint32]*[fakePageSize]byte)}
}

// Read implements Mem.
func (f *FakeMem) Read(a uint32, n int) ([]byte, error) {
	if err := CheckRange(a, n); err != nil {
		return nil, err
	}
	out := make([]byte, n)
	f.mu.Lock()
	defer f.mu.Unlock()
	for done := 0; done < n; {
		at := a + uint32(done)
		base, off := at&^(fakePageSize-1), int(at&(fakePageSize-1))
		k := min(fakePageSize-off, n-done)
		if p := f.pages[base]; p != nil {
			copy(out[done:done+k], p[off:off+k])
		}
		done += k
	}
	return out, nil
}

// Write implements Mem. While recording, it keeps a copy of b.
func (f *FakeMem) Write(a uint32, b []byte) error {
	return f.write(a, b, true)
}

// Store writes b at a as the game would: bounds-checked like Write, never
// recorded.
func (f *FakeMem) Store(a uint32, b []byte) error {
	return f.write(a, b, false)
}

func (f *FakeMem) write(a uint32, b []byte, tool bool) error {
	if err := CheckRange(a, len(b)); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for done := 0; done < len(b); {
		at := a + uint32(done)
		base, off := at&^(fakePageSize-1), int(at&(fakePageSize-1))
		k := min(fakePageSize-off, len(b)-done)
		p := f.pages[base]
		if p == nil {
			p = new([fakePageSize]byte)
			f.pages[base] = p
		}
		copy(p[off:off+k], b[done:done+k])
		done += k
	}
	if tool && f.recording {
		f.writes = append(f.writes, WriteRecord{Addr: a, Data: slices.Clone(b)})
	}
	return nil
}

// Record turns write recording on or off. Turning it off keeps what was
// recorded so far.
func (f *FakeMem) Record(on bool) {
	f.mu.Lock()
	f.recording = on
	f.mu.Unlock()
}

// Writes returns the recorded writes, oldest first.
func (f *FakeMem) Writes() []WriteRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]WriteRecord, len(f.writes))
	for i, w := range f.writes {
		out[i] = WriteRecord{Addr: w.Addr, Data: slices.Clone(w.Data)}
	}
	return out
}

// ResetWrites discards the recorded writes.
func (f *FakeMem) ResetWrites() {
	f.mu.Lock()
	f.writes = nil
	f.mu.Unlock()
}
