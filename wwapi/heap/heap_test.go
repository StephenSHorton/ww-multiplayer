package heap_test

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/heap"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/heap/heaptest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

func u32b(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }

func TestWalk(t *testing.T) {
	const (
		h     uint32 = 0x80C00000
		start uint32 = 0x80C00100
		end   uint32 = 0x80C10100
	)
	blocks := [][2]uint32{{0, 0x100}, {0x1000, 0x2000}, {0x8000, 0x40}}
	setup := func() *mem.FakeMem {
		m := mem.NewFakeMem()
		heaptest.FakeAt(t, m, addr.ZeldaHeapPtr, h, start, end, blocks)
		return m
	}
	got, err := heap.Walk(setup(), "zelda", addr.ZeldaHeapPtr)
	if err != nil || got.Problem != "" || got.Blocks != 3 || got.Free != 0x2140 || got.Largest != 0x2000 || got.Size != 0x10000 || got.Ptr != h {
		t.Fatalf("walk: %+v, %v", got, err)
	}

	cases := []struct {
		name string
		mut  func(m *mem.FakeMem)
		want string
	}{
		{"not created", func(m *mem.FakeMem) { m.Store(addr.ZeldaHeapPtr, u32b(0)) }, "not created"},
		{"pointer outside MEM1", func(m *mem.FakeMem) { m.Store(addr.ZeldaHeapPtr, u32b(0x7FFFFFF0)) }, "outside MEM1"},
		{"not an ExpHeap", func(m *mem.FakeMem) { m.Store(h, u32b(0x8039CCC0)) }, "is not JKRExpHeap's"},
		{"bounds", func(m *mem.FakeMem) { m.Store(h+addr.HeapOffSize, u32b(1)) }, "do not fit"},
		{"cycle", func(m *mem.FakeMem) { m.Store(start+0x8000+addr.MemBlockOffNext, u32b(start)) }, "has a cycle"},
		{"outside the heap", func(m *mem.FakeMem) { m.Store(start+0x1000+addr.MemBlockOffNext, u32b(end)) }, "outside the heap"},
		{"overrun", func(m *mem.FakeMem) { m.Store(start+0x8000+addr.MemBlockOffSize, u32b(0x10000)) }, "runs past"},
		{"prev", func(m *mem.FakeMem) { m.Store(start+0x1000+addr.MemBlockOffPrev, u32b(0)) }, "changed during the walk"},
		{"tail", func(m *mem.FakeMem) { m.Store(h+addr.ExpHeapOffFreeTail, u32b(start)) }, "mTailFreeList"},
	}
	for _, c := range cases {
		m := setup()
		c.mut(m)
		got, err := heap.Walk(m, "zelda", addr.ZeldaHeapPtr)
		if err != nil || !strings.Contains(got.Problem, c.want) {
			t.Errorf("%s: problem %q, err %v; want %q", c.name, got.Problem, err, c.want)
		}
	}
}

func TestReadCounters(t *testing.T) {
	m := mem.NewFakeMem()
	for i := range 7 {
		m.Store(addr.HeapCountersAddr+uint32(4*i), u32b(uint32(10+i)))
	}
	c, err := heap.ReadCounters(m)
	if err != nil {
		t.Fatal(err)
	}
	want := heap.Counters{Solid: 10, Game: 11, Zelda: 12, Command: 13, Archive: 14, Unknown: 15, Total: 16}
	if c != want {
		t.Errorf("counters %+v, want %+v", c, want)
	}
	// Each field is the counter addr names.
	for i, a := range []uint32{addr.SolidHeapErrors, addr.GameHeapErrors, addr.ZeldaHeapErrors, addr.CommandHeapErrors,
		addr.ArchiveHeapErrors, addr.UnknownHeapErrors, addr.HeapErrorsTotal} {
		if v, _ := mem.ReadU32(m, a); v != c.Values()[i] {
			t.Errorf("%s: 0x%08X holds %d, Values says %d", heap.CounterNames[i], a, v, c.Values()[i])
		}
	}
}

func TestGateConstants(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want uint32
	}{
		{"GateGameLargest", heap.GateGameLargest, 0xD0090},
		{"GateGameFree", heap.GateGameFree, 0x10CF00},
		{"GateZeldaLargest", heap.GateZeldaLargest, 0x14C90},
		{"GateArchiveLargest", heap.GateArchiveLargest, 0x24760},
	} {
		if c.got != c.want {
			t.Errorf("%s = 0x%X, want 0x%X", c.name, c.got, c.want)
		}
	}
}

func TestGate(t *testing.T) {
	th := heap.DefaultThresholds()
	ok := heap.Sample{
		Game:  heap.Info{Largest: heap.GateGameLargest, Free: uint64(heap.GateGameFree)},
		Zelda: heap.Info{Largest: heap.GateZeldaLargest},
	}
	if allow, r := th.Gate(ok); !allow || r != mailbox.RemoteHGOpen {
		t.Errorf("at the thresholds: %v %d", allow, r)
	}
	for _, c := range []struct {
		name string
		mut  func(s *heap.Sample)
		want uint8
	}{
		{"game problem", func(s *heap.Sample) { s.Game.Problem = "x" }, mailbox.RemoteHGWalkProblem},
		{"zelda problem", func(s *heap.Sample) { s.Zelda.Problem = "x" }, mailbox.RemoteHGWalkProblem},
		{"archive problem", func(s *heap.Sample) { s.ArchiveWalked, s.Archive.Problem = true, "x" }, mailbox.RemoteHGWalkProblem},
		{"game largest", func(s *heap.Sample) { s.Game.Largest-- }, mailbox.RemoteHGGameLargest},
		{"game free", func(s *heap.Sample) { s.Game.Free-- }, mailbox.RemoteHGGameFree},
		{"zelda largest", func(s *heap.Sample) { s.Zelda.Largest-- }, mailbox.RemoteHGZeldaLargest},
		{"archive largest", func(s *heap.Sample) { s.ArchiveWalked, s.Archive.Largest = true, heap.GateArchiveLargest-1 }, mailbox.RemoteHGArchiveLargest},
	} {
		s := ok
		c.mut(&s)
		if allow, r := th.Gate(s); allow || r != c.want {
			t.Errorf("%s: %v %d, want shut %d", c.name, allow, r, c.want)
		}
	}
	// An unwalked archive is not judged; zero thresholds skip their rule.
	s := ok
	s.Archive.Problem = "not walked"
	if allow, _ := th.Gate(s); !allow {
		t.Error("an unwalked archive shut the gate")
	}
	if allow, _ := (heap.Thresholds{}).Gate(heap.Sample{}); !allow {
		t.Error("zero thresholds shut the gate")
	}
}

func TestTakeSample(t *testing.T) {
	m := mem.NewFakeMem()
	heaptest.FakeAt(t, m, addr.GameHeapPtr, 0x80C00000, 0x80C00100, 0x80E00100, [][2]uint32{{0, 0x100000}})
	heaptest.FakeAt(t, m, addr.ZeldaHeapPtr, 0x80B00000, 0x80B00100, 0x80B40100, [][2]uint32{{0, 0x20000}})
	s, err := heap.TakeSample(m, false)
	if err != nil || s.ArchiveWalked || s.Game.Largest != 0x100000 || s.Zelda.Largest != 0x20000 {
		t.Fatalf("sample %+v, %v", s, err)
	}
	s, err = heap.TakeSample(m, true)
	if err != nil || !s.ArchiveWalked || s.Archive.Problem != "not created" {
		t.Fatalf("with archive: %+v, %v", s, err)
	}
}
