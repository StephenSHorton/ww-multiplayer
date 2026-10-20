// Package heap reads the game's JKRExpHeaps and heap error counters from
// emulator memory: the free-list walk the heap-free probe prints
// (cmd/wwdev) and the samples the remote-Link full-body heap gate
// (mods/enemysync, remote-link-stage4-plan.md D4-3) decides on.
//
// # v0 UNSTABLE
//
// This package is part of wwapi v0. Its API may change in any 0.x minor
// version.
package heap

import (
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

const (
	// expHeapHeaderSize covers a JKRExpHeap through mTailUsedList
	// (JKRExpHeap.h:96-106).
	expHeapHeaderSize = addr.ExpHeapOffUsedTail + 4
	// MaxFreeBlocks bounds a walk: a longer free list is a Problem.
	MaxFreeBlocks = 1 << 16
)

// Info is one JKRExpHeap's free list.
type Info struct {
	Name                  string
	Ptr, Start, End, Size uint32
	Blocks                int
	Free                  uint64 // what do_getTotalFreeSize returns (JKRExpHeap.cpp:487-496)
	Largest               uint32 // what do_getFreeSize returns (JKRExpHeap.cpp:459-470)
	// Problem says why the walk was cut short or the list looks
	// inconsistent; the totals then cover what was walked.
	Problem string
}

// Walk reads the JKRExpHeap whose pointer is at ptrAddr and sums its
// free list (mHeadFreeList, JKRExpHeap.h:103) the way do_getTotalFreeSize
// does. The heap must carry JKRExpHeap's vtable and sane bounds; each
// block must lie inside the heap with its content (the CMemBlock header,
// JKRExpHeap.h:36-42, then size bytes), link back to the block before it
// and not repeat. Free blocks are not checked for the 'HM' magic: the
// first is initiated with it but setFreeBlock zeroes it (JKRExpHeap.cpp:
// 85,575). The error is a read error; a bad list is reported in Problem.
func Walk(m mem.Reader, name string, ptrAddr uint32) (Info, error) {
	h := Info{Name: name}
	p, err := mem.ReadU32(m, ptrAddr)
	if err != nil {
		return h, err
	}
	if h.Ptr = p; p == 0 {
		h.Problem = "not created"
		return h, nil
	}
	hdr, err := m.Read(p, expHeapHeaderSize)
	if err != nil {
		h.Problem = fmt.Sprintf("heap pointer 0x%08X: %v", p, err)
		return h, nil
	}
	if vt := mem.U32BE(hdr); vt != addr.ExpHeapVtable {
		h.Problem = fmt.Sprintf("vtable 0x%08X is not JKRExpHeap's 0x%08X", vt, addr.ExpHeapVtable)
		return h, nil
	}
	h.Start, h.End, h.Size = mem.U32BE(hdr[addr.HeapOffStart:]), mem.U32BE(hdr[addr.HeapOffEnd:]), mem.U32BE(hdr[addr.HeapOffSize:])
	if h.Start >= h.End || mem.CheckRange(h.Start, int(h.End-h.Start)) != nil || h.Size != h.End-h.Start {
		h.Problem = fmt.Sprintf("bounds 0x%08X-0x%08X size 0x%X do not fit MEM1", h.Start, h.End, h.Size)
		return h, nil
	}
	tail := mem.U32BE(hdr[addr.ExpHeapOffFreeTail:])
	seen := make(map[uint32]bool)
	var prev uint32
	for b := mem.U32BE(hdr[addr.ExpHeapOffFreeHead:]); b != 0; {
		switch {
		case h.Blocks >= MaxFreeBlocks:
			h.Problem = fmt.Sprintf("more than %d free blocks", MaxFreeBlocks)
		case b&3 != 0 || b < h.Start || b > h.End-addr.MemBlockSize:
			h.Problem = fmt.Sprintf("free block 0x%08X is outside the heap", b)
		case seen[b]:
			h.Problem = fmt.Sprintf("free block 0x%08X comes round again: the list has a cycle", b)
		}
		if h.Problem != "" {
			return h, nil
		}
		seen[b] = true
		blk, err := m.Read(b, addr.MemBlockSize)
		if err != nil {
			return h, err
		}
		if bp := mem.U32BE(blk[addr.MemBlockOffPrev:]); bp != prev {
			h.Problem = fmt.Sprintf("free block 0x%08X has prev 0x%08X, not 0x%08X: the heap changed during the walk", b, bp, prev)
			return h, nil
		}
		size := mem.U32BE(blk[addr.MemBlockOffSize:])
		if uint64(b)+addr.MemBlockSize+uint64(size) > uint64(h.End) {
			h.Problem = fmt.Sprintf("free block 0x%08X of 0x%X bytes runs past the heap's end", b, size)
			return h, nil
		}
		h.Blocks++
		h.Free += uint64(size)
		h.Largest = max(h.Largest, size)
		prev, b = b, mem.U32BE(blk[addr.MemBlockOffNext:])
	}
	if prev != tail {
		h.Problem = fmt.Sprintf("the last free block is 0x%08X but mTailFreeList is 0x%08X", prev, tail)
	}
	return h, nil
}

// ExpHeap names a JKRExpHeap pointer.
type ExpHeap struct {
	Name string
	Ptr  uint32
}

// ExpHeaps are the JKRExpHeaps the heap-free probe walks, in its order.
var ExpHeaps = []ExpHeap{
	{"zelda", addr.ZeldaHeapPtr},
	{"game", addr.GameHeapPtr},
	{"archive", addr.ArchiveHeapPtr},
	{"command", addr.CommandHeapPtr},
}

// Counters are the allocation-failure counters, in declaration order
// (m_Do_machine.cpp:35-41); the failure handler bumps one per failure by
// heap (m_Do_machine.cpp:121-135). Vanilla bumps Solid once per stage
// load (HANDOFF.md:60).
type Counters struct {
	Solid, Game, Zelda, Command, Archive, Unknown, Total uint32
}

// CounterNames name Counters' fields in order, for logs.
var CounterNames = [7]string{"solid", "game", "zelda", "command", "archive", "unknown", "total"}

// Values returns the counters in CounterNames order.
func (c Counters) Values() [7]uint32 {
	return [7]uint32{c.Solid, c.Game, c.Zelda, c.Command, c.Archive, c.Unknown, c.Total}
}

// ReadCounters reads the seven counters in one read of
// addr.HeapCountersLen bytes at addr.HeapCountersAddr.
func ReadCounters(m mem.Reader) (Counters, error) {
	b, err := m.Read(addr.HeapCountersAddr, addr.HeapCountersLen)
	if err != nil {
		return Counters{}, err
	}
	if len(b) != addr.HeapCountersLen {
		return Counters{}, fmt.Errorf("heap: counters read %d bytes, want %d", len(b), addr.HeapCountersLen)
	}
	w := func(i int) uint32 { return mem.U32BE(b[4*i:]) }
	return Counters{w(0), w(1), w(2), w(3), w(4), w(5), w(6)}, nil
}

// The full-body heap gate's thresholds (remote-link-stage4-plan.md D4-3).
// The margins (0x20000, 0x40000, 0x10000, 0x20000) are design values the
// live checks (S4-1, S4-2) measure.
const (
	// GateGameLargest: mDoExt_createSolidHeap's 0xB0000 estimate rounded
	// up with the JKRSolidHeap header and 0x20 alignment (m_Do_ext.cpp:
	// 1005-1015), 0xB0090, plus 0x20000.
	GateGameLargest uint32 = 0xB0090 + 0x20000
	// GateGameFree: the 0xCCF00 a daPy_lk_c keeps resident, plus 0x40000.
	GateGameFree uint32 = 0xCCF00 + 0x40000
	// GateZeldaLargest: the actor instance (RemoteFullSize) plus a
	// CMemBlock header, plus 0x10000.
	GateZeldaLargest uint32 = mailbox.RemoteFullSize + addr.MemBlockSize + 0x10000
	// GateArchiveLargest: the shanm table (RemoteShanmBytes) plus
	// 0x20000, checked only while the table is not yet allocated.
	GateArchiveLargest uint32 = mailbox.RemoteShanmBytes + 0x20000
)

// Thresholds are the gate's size rules; a zero field skips its rule.
type Thresholds struct {
	GameLargest, GameFree, ZeldaLargest, ArchiveLargest uint32
}

// DefaultThresholds are the Gate* constants.
func DefaultThresholds() Thresholds {
	return Thresholds{GameLargest: GateGameLargest, GameFree: GateGameFree, ZeldaLargest: GateZeldaLargest, ArchiveLargest: GateArchiveLargest}
}

// Sample is one walk of the heaps the gate reads. The ArchiveHeap is
// walked only while the shanm table is not allocated (ArchiveWalked).
type Sample struct {
	Game, Zelda, Archive Info
	ArchiveWalked        bool
}

// TakeSample walks the GameHeap and ZeldaHeap, and the ArchiveHeap when
// withArchive.
func TakeSample(m mem.Reader, withArchive bool) (Sample, error) {
	var s Sample
	var err error
	if s.Game, err = Walk(m, "game", addr.GameHeapPtr); err != nil {
		return s, err
	}
	if s.Zelda, err = Walk(m, "zelda", addr.ZeldaHeapPtr); err != nil {
		return s, err
	}
	if withArchive {
		if s.Archive, err = Walk(m, "archive", addr.ArchiveHeapPtr); err != nil {
			return s, err
		}
		s.ArchiveWalked = true
	}
	return s, nil
}

// Gate is the size verdict on s: allow, or the mailbox.RemoteHG* reason
// of the first rule that fails (a walk Problem, then GameHeap largest,
// GameHeap free, ZeldaHeap largest, ArchiveHeap largest). The reason is
// mailbox.RemoteHGOpen when allowed.
func (th Thresholds) Gate(s Sample) (allow bool, reason uint8) {
	if s.Game.Problem != "" || s.Zelda.Problem != "" || (s.ArchiveWalked && s.Archive.Problem != "") {
		return false, mailbox.RemoteHGWalkProblem
	}
	switch {
	case s.Game.Largest < th.GameLargest:
		return false, mailbox.RemoteHGGameLargest
	case s.Game.Free < uint64(th.GameFree):
		return false, mailbox.RemoteHGGameFree
	case s.Zelda.Largest < th.ZeldaLargest:
		return false, mailbox.RemoteHGZeldaLargest
	case s.ArchiveWalked && s.Archive.Largest < th.ArchiveLargest:
		return false, mailbox.RemoteHGArchiveLargest
	}
	return true, mailbox.RemoteHGOpen
}
