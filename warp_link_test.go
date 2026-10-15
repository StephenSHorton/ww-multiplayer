package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/mem"
)

// fakeWarpHandler is multiplayer_update's warp handler over m: while Link
// exists and warp_seq differs from warp_ack, it puts Link's current.pos at
// warp_x/y/z and copies warp_seq into warp_ack (multiplayer.c:344-345,
// :365-421). It reports whether it moved Link.
func fakeWarpHandler(t *testing.T, m *mem.FakeMem) bool {
	link, err := mem.ReadU32(m, addr.PlayerPtr)
	if err != nil || link == 0 {
		return false
	}
	w, err := m.Read(mailboxBase+mailboxWarpSeq, 20)
	if err != nil || bytes.Equal(w[:4], w[4:8]) {
		return false
	}
	if err := m.Store(link+actorOffCurPos, w[8:20]); err != nil {
		t.Error(err)
	}
	if err := m.Store(mailboxBase+mailboxWarpAck, w[:4]); err != nil {
		t.Error(err)
	}
	return true
}

// runWarpHandler runs fakeWarpHandler about every millisecond, as the
// game's frames would, until the test ends. It returns the number of
// warps made so far.
func runWarpHandler(t *testing.T, m *mem.FakeMem) func() int {
	ctx, cancel := context.WithCancel(context.Background())
	var n atomic.Int32
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			if fakeWarpHandler(t, m) {
				n.Add(1)
			}
			time.Sleep(time.Millisecond)
		}
	}()
	t.Cleanup(func() { cancel(); wg.Wait() })
	return func() int { return int(n.Load()) }
}

func readU32(t *testing.T, m mem.Mem, a uint32) uint32 {
	t.Helper()
	v, err := mem.ReadU32(m, a)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func readLinkPos(t *testing.T, m mem.Mem) [3]float32 {
	t.Helper()
	b, err := m.Read(fakegame.PlayerActor+actorOffCurPos, 12)
	if err != nil {
		t.Fatal(err)
	}
	var p [3]float32
	for i := range p {
		p[i] = math.Float32frombits(binary.BigEndian.Uint32(b[4*i:]))
	}
	return p
}

func posBytes(p [3]float32) []byte {
	var b []byte
	for _, v := range p {
		b = binary.BigEndian.AppendUint32(b, math.Float32bits(v))
	}
	return b
}

// TestWarpLinkWithdrawsOnTimeout: with nothing taking the request (Link
// gone, emulation paused), warpLinkMem withdraws it, so it does not land
// once Link is back.
func TestWarpLinkWithdrawsOnTimeout(t *testing.T) {
	m := mem.NewFakeMem()
	store(t, m, mailboxBase+mailboxWarpSeq, u32b(6))
	store(t, m, mailboxBase+mailboxWarpAck, u32b(6))
	err := warpLinkMem(m, [3]float32{1, 2, 3}, 20*time.Millisecond)
	const want = "warp not acknowledged within 20ms (Link gone or emulation paused); withdrawn"
	if err == nil || err.Error() != want {
		t.Errorf("got %v, want %q", err, want)
	}
	if seq, ack := readU32(t, m, mailboxBase+mailboxWarpSeq), readU32(t, m, mailboxBase+mailboxWarpAck); seq != 7 || ack != 7 {
		t.Errorf("warp_seq %d, warp_ack %d after the withdraw, want both 7", seq, ack)
	}
	// Link comes back (the stage loaded, emulation resumed): the handler
	// finds nothing to do.
	store(t, m, addr.PlayerPtr, u32b(fakegame.PlayerActor))
	if fakeWarpHandler(t, m) {
		t.Errorf("the withdrawn warp landed later, at %v", readLinkPos(t, m))
	}
}

// TestWarpLinkAcked: a warp the game takes returns nil, and the request
// is written coordinates first, warp_seq last.
func TestWarpLinkAcked(t *testing.T) {
	m := mem.NewFakeMem()
	store(t, m, addr.PlayerPtr, u32b(fakegame.PlayerActor))
	warps := runWarpHandler(t, m)
	m.Record(true)
	p := [3]float32{-200050, 12.5, 300020}
	if err := warpLinkMem(m, p, time.Second); err != nil {
		t.Fatal(err)
	}
	m.Record(false)
	if got := readLinkPos(t, m); got != p {
		t.Errorf("Link at %v, want %v", got, p)
	}
	ws := m.Writes()
	if len(ws) != 2 ||
		ws[0].Addr != mailboxBase+mailboxWarpX || !bytes.Equal(ws[0].Data, posBytes(p)) ||
		ws[1].Addr != mailboxBase+mailboxWarpSeq || !bytes.Equal(ws[1].Data, []byte{0, 0, 0, 1}) {
		t.Errorf("writes %+v, want the 12 coordinate bytes then warp_seq = 1", ws)
	}
	if n := warps(); n != 1 {
		t.Errorf("%d warps, want 1", n)
	}
}

// raceMem runs before just ahead of a write to warp_ack: the game taking
// the request between warpLinkMem's last read of warp_ack and its
// withdraw.
type raceMem struct {
	*mem.FakeMem
	before func()
}

func (r raceMem) Write(a uint32, b []byte) error {
	if a == mailboxBase+mailboxWarpAck {
		r.before()
	}
	return r.FakeMem.Write(a, b)
}

// TestWarpLinkAckRace: when the game takes the request just as it is
// withdrawn, Link is moved exactly once, and warp_seq == warp_ack after.
func TestWarpLinkAckRace(t *testing.T) {
	fm := mem.NewFakeMem()
	p := [3]float32{10, 20, 30}
	warps, withdraws := 0, 0
	m := raceMem{FakeMem: fm, before: func() {
		withdraws++
		store(t, fm, addr.PlayerPtr, u32b(fakegame.PlayerActor)) // Link is back
		if fakeWarpHandler(t, fm) {
			warps++
		}
	}}
	err := warpLinkMem(m, p, 20*time.Millisecond)
	if withdraws != 1 || err == nil || !strings.HasSuffix(err.Error(), "; withdrawn") {
		t.Fatalf("%d writes to warp_ack, error %v; want one withdraw, which can't see that the game took the request just before it", withdraws, err)
	}
	for range 5 { // later frames
		if fakeWarpHandler(t, fm) {
			warps++
		}
	}
	if warps != 1 {
		t.Errorf("%d warps, want exactly 1", warps)
	}
	if got := readLinkPos(t, fm); got != p {
		t.Errorf("Link at %v, want %v", got, p)
	}
	if seq, ack := readU32(t, fm, mailboxBase+mailboxWarpSeq), readU32(t, fm, mailboxBase+mailboxWarpAck); seq != 1 || ack != 1 {
		t.Errorf("warp_seq %d, warp_ack %d, want both 1", seq, ack)
	}
}
