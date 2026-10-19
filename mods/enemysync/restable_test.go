package enemysync

import (
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// The fake resource table: the Mo2 entry where the live probe found it
// in M_Dai room 1 (info[28], SESSION-2026-10-18.md:82-85), its archive
// and mRes array in the heap, and 56 BCK pointers 0x1A40 apart.
const (
	resMo2Entry = 28
	resMo2Arc   = uint32(0x8123_4560)
	resMo2Res   = uint32(0x8130_0000)
	resMo2Count = uint16(3) // a reference count: one per Moblin using the archive
	resBckBase  = uint32(0x8140_0000)
	resBckStep  = uint32(0x1A40)
)

// resCountMem counts the Reads a Resolve makes.
type resCountMem struct {
	mem.Reader
	reads int
}

func (c *resCountMem) Read(a uint32, n int) ([]byte, error) {
	c.reads++
	return c.Reader.Read(a, n)
}

// resPutEntry writes mObjectInfo entry k: its name (NUL-padded to 14),
// mCount, mpArchive and mRes.
func resPutEntry(t *testing.T, m *mem.FakeMem, k int, name string, count uint16, arc, res uint32) {
	t.Helper()
	e := make([]byte, addr.ResInfoSize)
	copy(e[addr.ResInfoOffName:addr.ResInfoOffName+addr.ResInfoNameLen], name)
	be := binary.BigEndian
	be.PutUint16(e[addr.ResInfoOffCount:], count)
	be.PutUint32(e[addr.ResInfoOffArchive:], arc)
	be.PutUint32(e[addr.ResInfoOffRes:], res)
	if err := m.Store(addr.MObjectInfo+uint32(k*addr.ResInfoSize), e); err != nil {
		t.Fatal(err)
	}
}

// resPutPtrs writes ptrs as the Mo2 entry's mRes[0x3E..0x75].
func resPutPtrs(t *testing.T, m *mem.FakeMem, res uint32, ptrs [addr.Mo2BckCount]uint32) {
	t.Helper()
	b := make([]byte, 4*addr.Mo2BckCount)
	for i, p := range ptrs {
		binary.BigEndian.PutUint32(b[4*i:], p)
	}
	if err := m.Store(res+4*addr.Mo2BckFirst, b); err != nil {
		t.Fatal(err)
	}
}

// resFixture is a loaded table: a few other archives around a valid Mo2
// entry at resMo2Entry, and the 56 pointers it holds.
func resFixture(t *testing.T) (*mem.FakeMem, [addr.Mo2BckCount]uint32) {
	t.Helper()
	m := mem.NewFakeMem()
	resPutEntry(t, m, 0, "Always", 0x40, 0x8110_0000, 0x8110_8000)
	resPutEntry(t, m, 3, "Link", 0x9A, 0x8111_0000, 0x8111_8000)
	resPutEntry(t, m, 27, "Mo", 0x10, 0x8112_0000, 0x8112_8000)
	resPutEntry(t, m, resMo2Entry, addr.Mo2Arc, resMo2Count, resMo2Arc, resMo2Res)
	var ptrs [addr.Mo2BckCount]uint32
	for i := range ptrs {
		ptrs[i] = resBckBase + uint32(i)*resBckStep
	}
	resPutPtrs(t, m, resMo2Res, ptrs)
	return m, ptrs
}

func resResolve(t *testing.T, m mem.Reader) (*Mo2Res, int) {
	t.Helper()
	var r Mo2Res
	c := &resCountMem{Reader: m}
	if err := r.Resolve(c); err != nil {
		t.Fatal(err)
	}
	return &r, c.reads
}

func TestMo2ResResolve(t *testing.T) {
	m, ptrs := resFixture(t)
	r, reads := resResolve(t, m)
	if !r.OK() || r.Why() != "" {
		t.Fatalf("OK %v why %q, want ok", r.OK(), r.Why())
	}
	if reads != 2 {
		t.Errorf("Resolve made %d reads, want 2 (mObjectInfo, then the 56 words)", reads)
	}
	if k, res := r.Entry(); k != resMo2Entry || res != resMo2Res {
		t.Errorf("Entry = %d, 0x%08X, want %d, 0x%08X", k, res, resMo2Entry, resMo2Res)
	}
	if r.Ptrs() != ptrs {
		t.Error("Ptrs differ from the table")
	}
	if got := r.Index(ptrs[7]); got != 0x45 {
		t.Errorf("Index(ptrs[7]) = 0x%02X, want 0x45 (ATATE1)", got)
	}
	for _, c := range []struct {
		i    int
		want uint8
	}{{0, 0x3E}, {addr.Mo2BckWait - addr.Mo2BckFirst, addr.Mo2BckWait}, {addr.Mo2BckCount - 1, 0x75}} {
		if got := r.Index(ptrs[c.i]); got != c.want {
			t.Errorf("Index(ptrs[%d]) = 0x%02X, want 0x%02X", c.i, got, c.want)
		}
	}
	for _, p := range []uint32{0, ptrs[7] + 4, resBckBase - resBckStep, 0x8150_0000} {
		if got := r.Index(p); got != addr.Mo2BckNone {
			t.Errorf("Index(0x%08X) = 0x%02X, want 0xFF", p, got)
		}
	}
	if r.Stats.Reads != 1 {
		t.Errorf("Stats.Reads = %d, want 1", r.Stats.Reads)
	}
}

// TestMo2ResNames: getResInfo's name rule, NUL-terminated equality, and
// its skip of an entry with mCount 0 or no archive.
func TestMo2ResNames(t *testing.T) {
	cases := []struct {
		name    string
		entries func(t *testing.T, m *mem.FakeMem)
		ok      bool
		entry   int
	}{
		{"Mo2x is not Mo2", func(t *testing.T, m *mem.FakeMem) {
			resPutEntry(t, m, resMo2Entry, "Mo2x", resMo2Count, resMo2Arc, resMo2Res)
		}, false, -1},
		{"Mo is not Mo2", func(t *testing.T, m *mem.FakeMem) {
			resPutEntry(t, m, resMo2Entry, "Mo", resMo2Count, resMo2Arc, resMo2Res)
		}, false, -1},
		{"bytes past the NUL are ignored", func(t *testing.T, m *mem.FakeMem) {
			resPutEntry(t, m, resMo2Entry, "Mo2\x00junk", resMo2Count, resMo2Arc, resMo2Res)
		}, true, resMo2Entry},
		{"a 14-byte name with no NUL", func(t *testing.T, m *mem.FakeMem) {
			resPutEntry(t, m, resMo2Entry, "Mo2"+strings.Repeat("_", 11), resMo2Count, resMo2Arc, resMo2Res)
		}, false, -1},
		{"an unloaded Mo2 before the loaded one", func(t *testing.T, m *mem.FakeMem) {
			resPutEntry(t, m, 5, addr.Mo2Arc, 0, 0, 0)
		}, true, resMo2Entry},
		{"a Mo2 with no archive before the loaded one", func(t *testing.T, m *mem.FakeMem) {
			resPutEntry(t, m, 6, addr.Mo2Arc, resMo2Count, 0, 0x8131_0000)
		}, true, resMo2Entry},
		{"the last entry", func(t *testing.T, m *mem.FakeMem) {
			resPutEntry(t, m, resMo2Entry, "", 0, 0, 0)
			resPutEntry(t, m, addr.ObjectInfoCount-1, addr.Mo2Arc, resMo2Count, resMo2Arc, resMo2Res)
		}, true, addr.ObjectInfoCount - 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, _ := resFixture(t)
			c.entries(t, m)
			r, _ := resResolve(t, m)
			if r.OK() != c.ok {
				t.Fatalf("OK = %v (why %q), want %v", r.OK(), r.Why(), c.ok)
			}
			if k, _ := r.Entry(); k != c.entry {
				t.Errorf("Entry = %d, want %d", k, c.entry)
			}
			if !c.ok && r.Why() == "" {
				t.Error("not ok with no Why")
			}
		})
	}
}

// TestMo2ResRefuses: every table the host must not stream from is not
// ok, says why, and Index answers 0xFF for every pointer.
func TestMo2ResRefuses(t *testing.T) {
	cases := []struct {
		name  string
		spoil func(t *testing.T, m *mem.FakeMem, ptrs [addr.Mo2BckCount]uint32)
		reads int
	}{
		{"no Mo2 entry", func(t *testing.T, m *mem.FakeMem, _ [addr.Mo2BckCount]uint32) {
			resPutEntry(t, m, resMo2Entry, "", 0, 0, 0)
		}, 1},
		{"mCount 0", func(t *testing.T, m *mem.FakeMem, _ [addr.Mo2BckCount]uint32) {
			resPutEntry(t, m, resMo2Entry, addr.Mo2Arc, 0, resMo2Arc, resMo2Res)
		}, 1},
		{"archive outside MEM1", func(t *testing.T, m *mem.FakeMem, _ [addr.Mo2BckCount]uint32) {
			resPutEntry(t, m, resMo2Entry, addr.Mo2Arc, resMo2Count, 0x9123_4560, resMo2Res)
		}, 1},
		{"mRes 0", func(t *testing.T, m *mem.FakeMem, _ [addr.Mo2BckCount]uint32) {
			resPutEntry(t, m, resMo2Entry, addr.Mo2Arc, resMo2Count, resMo2Arc, 0)
		}, 1},
		{"a duplicate pointer", func(t *testing.T, m *mem.FakeMem, ptrs [addr.Mo2BckCount]uint32) {
			ptrs[40] = ptrs[12]
			resPutPtrs(t, m, resMo2Res, ptrs)
		}, 2},
		{"a misaligned pointer", func(t *testing.T, m *mem.FakeMem, ptrs [addr.Mo2BckCount]uint32) {
			ptrs[3] += 2
			resPutPtrs(t, m, resMo2Res, ptrs)
		}, 2},
		{"a NULL pointer", func(t *testing.T, m *mem.FakeMem, ptrs [addr.Mo2BckCount]uint32) {
			ptrs[55] = 0
			resPutPtrs(t, m, resMo2Res, ptrs)
		}, 2},
		{"a pointer outside MEM1", func(t *testing.T, m *mem.FakeMem, ptrs [addr.Mo2BckCount]uint32) {
			ptrs[0] = 0x8300_0000
			resPutPtrs(t, m, resMo2Res, ptrs)
		}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, ptrs := resFixture(t)
			c.spoil(t, m, ptrs)
			r, reads := resResolve(t, m)
			if r.OK() {
				t.Fatal("OK true")
			}
			if r.Why() == "" {
				t.Error("no Why")
			}
			if reads != c.reads {
				t.Errorf("%d reads, want %d", reads, c.reads)
			}
			if got := r.Index(ptrs[7]); got != addr.Mo2BckNone {
				t.Errorf("Index = 0x%02X on a refused table", got)
			}
			if r.Ptrs() != [addr.Mo2BckCount]uint32{} {
				t.Error("a refused table kept pointers")
			}
		})
	}
	// mCount is a reference count (three Moblins in M_Dai room 1 read 3
	// live), not the number of resources: any nonzero count resolves.
	for _, n := range []uint16{1, 3} {
		m, _ := resFixture(t)
		resPutEntry(t, m, resMo2Entry, addr.Mo2Arc, n, resMo2Arc, resMo2Res)
		if r, _ := resResolve(t, m); !r.OK() {
			t.Errorf("mCount %d: not ok (%s)", n, r.Why())
		}
	}
}

// TestMo2ResLookupMiss: a stray pointer is 0xFF and re-reads the table at
// once, then not again until MissReread has passed.
func TestMo2ResLookupMiss(t *testing.T) {
	m, ptrs := resFixture(t)
	var r Mo2Res
	c := &resCountMem{Reader: m}
	if err := r.Resolve(c); err != nil {
		t.Fatal(err)
	}
	t0 := time.Unix(1_700_000_000, 0)
	look := func(anm uint32, at time.Duration, want uint8, wantReads int) {
		t.Helper()
		c.reads = 0
		got, err := r.Lookup(c, anm, t0.Add(at))
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("+%v Lookup(0x%08X) = 0x%02X, want 0x%02X", at, anm, got, want)
		}
		if c.reads != wantReads {
			t.Errorf("+%v Lookup(0x%08X) made %d reads, want %d", at, anm, c.reads, wantReads)
		}
	}
	stray := uint32(0x8150_0040)
	look(ptrs[addr.Mo2BckWait-addr.Mo2BckFirst], 0, addr.Mo2BckWait, 0) // a hit reads nothing
	look(stray, 0, addr.Mo2BckNone, 2)                                  // the first miss re-reads
	look(stray, 16*time.Millisecond, addr.Mo2BckNone, 0)
	look(stray, 99*time.Millisecond, addr.Mo2BckNone, 0)
	look(ptrs[7], 99*time.Millisecond, 0x45, 0) // hits never wait on the limit
	look(stray, MissReread, addr.Mo2BckNone, 2)
	if r.Stats.BckMiss != 4 || r.Stats.MissReads != 2 || r.Stats.Reads != 3 {
		t.Errorf("Stats = %+v, want 4 misses, 2 miss reads, 3 reads", r.Stats)
	}

	// The archive reloads under the sampler: the stray pointer is now
	// WALK's, and the next allowed miss finds it.
	ptrs[addr.Mo2BckLast-addr.Mo2BckFirst] = stray
	resPutPtrs(t, m, resMo2Res, ptrs)
	look(stray, MissReread+50*time.Millisecond, addr.Mo2BckNone, 0)
	look(stray, 2*MissReread, addr.Mo2BckLast, 2)
	look(stray, 2*MissReread+16*time.Millisecond, addr.Mo2BckLast, 0)
}

// TestMo2ResLookupRecovers: a table that was not ok at the walk (the
// Moblin's archive still loading) is picked up by a miss, not left until
// the next walk.
func TestMo2ResLookupRecovers(t *testing.T) {
	m, ptrs := resFixture(t)
	resPutEntry(t, m, resMo2Entry, addr.Mo2Arc, 0, 0, 0)
	var r Mo2Res
	if err := r.Resolve(m); err != nil {
		t.Fatal(err)
	}
	if r.OK() {
		t.Fatal("ok with mCount 0")
	}
	t0 := time.Unix(1_700_000_000, 0)
	if got, _ := r.Lookup(m, ptrs[addr.Mo2BckWait-addr.Mo2BckFirst], t0); got != addr.Mo2BckNone {
		t.Errorf("Lookup on an unloaded table = 0x%02X", got)
	}
	resPutEntry(t, m, resMo2Entry, addr.Mo2Arc, resMo2Count, resMo2Arc, resMo2Res)
	if got, _ := r.Lookup(m, ptrs[addr.Mo2BckWait-addr.Mo2BckFirst], t0.Add(50*time.Millisecond)); got != addr.Mo2BckNone {
		t.Errorf("Lookup inside MissReread = 0x%02X, want 0xFF", got)
	}
	if got, _ := r.Lookup(m, ptrs[addr.Mo2BckWait-addr.Mo2BckFirst], t0.Add(MissReread)); got != addr.Mo2BckWait || !r.OK() {
		t.Errorf("Lookup after the load = 0x%02X ok %v, want 0x%02X ok", got, r.OK(), addr.Mo2BckWait)
	}
}
