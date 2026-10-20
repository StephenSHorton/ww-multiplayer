package enemysync

import (
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// ArcRes for the Bokoblin and ChuChu tables (design test_plan.unit
// TestArcResBK/CC): 43 pointers at mRes + 4*0x29 of the "Bk" entry, 15 at
// mRes + 4*0x14 of the "CC" entry, with Mo2Res's mCount, MEM1, alignment
// and distinctness rules.

const (
	arcBkEntry = 31
	arcBkRes   = uint32(0x8131_0000)
	arcCcEntry = 40
	arcCcRes   = uint32(0x8132_0000)
	arcBkBase  = uint32(0x8150_0000)
	arcCcBase  = uint32(0x8160_0000)
)

// arcPutPtrs writes ptrs as mRes[first..].
func arcPutPtrs(t *testing.T, m *mem.FakeMem, res uint32, first int, ptrs []uint32) {
	t.Helper()
	b := make([]byte, 4*len(ptrs))
	for i, p := range ptrs {
		binary.BigEndian.PutUint32(b[4*i:], p)
	}
	if err := m.Store(res+4*uint32(first), b); err != nil {
		t.Fatal(err)
	}
}

func arcPtrs(base uint32, n int) []uint32 {
	out := make([]uint32, n)
	for i := range out {
		out[i] = base + uint32(i)*0x800
	}
	return out
}

// arcFixture is the Moblin fixture's table plus a loaded "Bk" and "CC".
func arcFixture(t *testing.T) (*mem.FakeMem, []uint32, []uint32) {
	t.Helper()
	m, _ := resFixture(t)
	resPutEntry(t, m, arcBkEntry, addr.BkArc, 2, 0x8133_0000, arcBkRes)
	resPutEntry(t, m, arcCcEntry, addr.CcArc, 9, 0x8134_0000, arcCcRes)
	bk, cc := arcPtrs(arcBkBase, addr.BkBckCount), arcPtrs(arcCcBase, addr.CcBckCount)
	arcPutPtrs(t, m, arcBkRes, addr.BkBckFirst, bk)
	arcPutPtrs(t, m, arcCcRes, addr.CcBckFirst, cc)
	return m, bk, cc
}

func arcResolve(t *testing.T, m mem.Reader, k KindID) (*ArcRes, int) {
	t.Helper()
	r := NewArcRes(k.Row())
	c := &resCountMem{Reader: m}
	if err := r.Resolve(c); err != nil {
		t.Fatal(err)
	}
	return r, c.reads
}

func TestArcResBK(t *testing.T) {
	m, bk, _ := arcFixture(t)
	r, reads := arcResolve(t, m, KindBK)
	if !r.OK() || r.Why() != "" || reads != 2 {
		t.Fatalf("OK %v why %q reads %d", r.OK(), r.Why(), reads)
	}
	if k, res, n := r.Entry(); k != arcBkEntry || res != arcBkRes || n != 2 {
		t.Errorf("Entry = %d 0x%08X %d", k, res, n)
	}
	if len(r.Ptrs()) != 43 {
		t.Errorf("%d pointers, want 43", len(r.Ptrs()))
	}
	for _, c := range []struct {
		i    int
		want uint8
	}{{0, 0x29}, {addr.BkBckWait - addr.BkBckFirst, 0x51}, {42, 0x53}} {
		if got := r.Index(bk[c.i]); got != c.want {
			t.Errorf("Index(bk[%d]) = 0x%02X, want 0x%02X", c.i, got, c.want)
		}
	}
	// A Moblin's pointer is not a Bokoblin's row.
	if got := r.Index(resBckBase); got != addr.Mo2BckNone {
		t.Errorf("Index(a Mo2 pointer) = 0x%02X", got)
	}
}

func TestArcResCC(t *testing.T) {
	m, _, cc := arcFixture(t)
	r, reads := arcResolve(t, m, KindCC)
	if !r.OK() || reads != 2 || len(r.Ptrs()) != 15 {
		t.Fatalf("OK %v why %q reads %d ptrs %d", r.OK(), r.Why(), reads, len(r.Ptrs()))
	}
	if got := r.Index(cc[0]); got != 0x14 {
		t.Errorf("Index(cc[0]) = 0x%02X, want 0x14", got)
	}
	if got := r.Index(cc[14]); got != 0x22 {
		t.Errorf("Index(cc[14]) = 0x%02X, want 0x22", got)
	}
	// The read covers mRes+0x50 .. +0x8C: a word just past it is not read.
	arcPutPtrs(t, m, arcCcRes, addr.CcBckLast+1, []uint32{0})
	if r2, _ := arcResolve(t, m, KindCC); !r2.OK() {
		t.Errorf("a word past the range made the table not ok: %s", r2.Why())
	}
}

// TestArcResRefuses: the Mo2Res rules per kind (mCount 0 or the archive
// outside MEM1 skip the entry; a bad pointer refuses the table).
func TestArcResRefuses(t *testing.T) {
	for _, c := range []struct {
		name string
		mod  func(t *testing.T, m *mem.FakeMem)
		why  string
	}{
		{"mCount 0", func(t *testing.T, m *mem.FakeMem) {
			resPutEntry(t, m, arcBkEntry, addr.BkArc, 0, 0x8133_0000, arcBkRes)
		}, "no loaded Bk entry"},
		{"archive outside MEM1", func(t *testing.T, m *mem.FakeMem) {
			resPutEntry(t, m, arcBkEntry, addr.BkArc, 2, 0x9000_0000, arcBkRes)
		}, "no loaded Bk entry"},
		{"name differs in case", func(t *testing.T, m *mem.FakeMem) {
			resPutEntry(t, m, arcBkEntry, "BK", 2, 0x8133_0000, arcBkRes)
		}, "no loaded Bk entry"},
		{"duplicate pointer", func(t *testing.T, m *mem.FakeMem) {
			arcPutPtrs(t, m, arcBkRes, addr.BkBckFirst+5, []uint32{arcBkBase})
		}, "duplicate"},
		{"misaligned pointer", func(t *testing.T, m *mem.FakeMem) {
			arcPutPtrs(t, m, arcBkRes, addr.BkBckLast, []uint32{arcBkBase + 2})
		}, "misaligned"},
		{"null pointer", func(t *testing.T, m *mem.FakeMem) {
			arcPutPtrs(t, m, arcBkRes, addr.BkBckFirst, []uint32{0})
		}, "outside MEM1"},
	} {
		m, _, _ := arcFixture(t)
		c.mod(t, m)
		r, _ := arcResolve(t, m, KindBK)
		if r.OK() || !strings.Contains(r.Why(), c.why) || r.Ptrs() != nil {
			t.Errorf("%s: OK %v why %q", c.name, r.OK(), r.Why())
		}
		if r.Index(arcBkBase+0x800) != addr.Mo2BckNone {
			t.Errorf("%s: Index resolved through a refused table", c.name)
		}
	}
	// A later loaded entry is found once the earlier one has mCount 0
	// (getResInfo skips a free entry, d_resorce.cpp:625-637).
	m, _, _ := arcFixture(t)
	resPutEntry(t, m, 2, addr.BkArc, 0, 0x8133_0000, 0x8100_0000)
	if r, _ := arcResolve(t, m, KindBK); !r.OK() {
		t.Errorf("a free earlier Bk entry hid the loaded one: %s", r.Why())
	}
}

// TestArcResMO2MatchesMo2Res: the Moblin row's ArcRes resolves what
// Mo2Res resolves.
func TestArcResMO2MatchesMo2Res(t *testing.T) {
	m, ptrs := resFixture(t)
	a, _ := arcResolve(t, m, KindMO2)
	r, _ := resResolve(t, m)
	if !a.OK() || !r.OK() {
		t.Fatalf("ArcRes %v (%s), Mo2Res %v", a.OK(), a.Why(), r.OK())
	}
	for _, p := range append(ptrs[:], 0, 0x8150_0000) {
		if a.Index(p) != r.Index(p) {
			t.Errorf("Index(0x%08X): ArcRes 0x%02X Mo2Res 0x%02X", p, a.Index(p), r.Index(p))
		}
	}
}

// TestKindTrackerDerive: a Bokoblin sample resolves through the Bk table
// and a lookup miss re-reads at most once per MissReread.
func TestKindTrackerDerive(t *testing.T) {
	m, bk, cc := arcFixture(t)
	tr := NewKindTracker()
	now := time.Unix(100, 0)
	s := KindSample{Kind: KindBK}
	s.MorfOK, s.Anm = true, bk[0x28]
	if err := tr.Derive(m, 7, &s, now); err != nil || s.Bck != 0x51 {
		t.Errorf("BK Bck = 0x%02X err %v, want 0x51 (WAIT)", s.Bck, err)
	}
	c := KindSample{Kind: KindCC}
	c.MorfOK, c.Anm = true, cc[8]
	if err := tr.Derive(m, 8, &c, now); err != nil || c.Bck != 0x1C {
		t.Errorf("CC Bck = 0x%02X err %v, want 0x1C", c.Bck, err)
	}
	miss := KindSample{Kind: KindBK}
	miss.MorfOK, miss.Anm = true, 0x8170_0000
	_ = tr.Derive(m, 7, &miss, now)
	_ = tr.Derive(m, 7, &miss, now.Add(time.Millisecond))
	// The first Derive missed on the unresolved table and read it (one
	// miss, one re-read); the two misses after it fall inside MissReread.
	if st := tr.Res[KindBK].Stats; st.BckMiss != 3 || st.MissReads != 1 || st.Reads != 1 || miss.Bck != addr.Mo2BckNone {
		t.Errorf("miss stats %+v bck 0x%02X", st, miss.Bck)
	}
	tr.Keep([]uint32{8})
	if len(tr.tracks) != 1 {
		t.Errorf("Keep left %d tracks", len(tr.tracks))
	}
}
