// Package heaptest lays out JKRExpHeaps in a mem.FakeMem for tests of
// wwapi/heap and its users.
package heaptest

import (
	"encoding/binary"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

func store(t testing.TB, m *mem.FakeMem, a, v uint32) {
	t.Helper()
	if err := m.Store(a, binary.BigEndian.AppendUint32(nil, v)); err != nil {
		t.Fatal(err)
	}
}

// Fake lays out a JKRExpHeap at h, spanning start..end, with the free
// blocks given as {offset from the heap's start, content size}, linked in
// order. It does not write the heap pointer (FakeAt does).
func Fake(t testing.TB, m *mem.FakeMem, h, start, end uint32, blocks [][2]uint32) {
	t.Helper()
	store(t, m, h, addr.ExpHeapVtable)
	store(t, m, h+addr.HeapOffStart, start)
	store(t, m, h+addr.HeapOffEnd, end)
	store(t, m, h+addr.HeapOffSize, end-start)
	var head, tail uint32
	for i, b := range blocks {
		at := start + b[0]
		var prev, next uint32
		if i > 0 {
			prev = start + blocks[i-1][0]
		}
		if i+1 < len(blocks) {
			next = start + blocks[i+1][0]
		}
		store(t, m, at+addr.MemBlockOffSize, b[1])
		store(t, m, at+addr.MemBlockOffPrev, prev)
		store(t, m, at+addr.MemBlockOffNext, next)
		if i == 0 {
			head = at
		}
		tail = at
	}
	store(t, m, h+addr.ExpHeapOffFreeHead, head)
	store(t, m, h+addr.ExpHeapOffFreeTail, tail)
}

// FakeAt is Fake with the heap pointer at ptrAddr set to h.
func FakeAt(t testing.TB, m *mem.FakeMem, ptrAddr, h, start, end uint32, blocks [][2]uint32) {
	t.Helper()
	store(t, m, ptrAddr, h)
	Fake(t, m, h, start, end, blocks)
}
