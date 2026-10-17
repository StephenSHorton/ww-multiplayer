// Package playertest stands in for the C side of package player in tests:
// multiplayer_update's warp handler over a mem.FakeMem, and helpers to
// read and build Link positions.
package playertest

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// WarpHandler is multiplayer_update's warp handler over m: while Link
// exists and warp_seq differs from warp_ack, it puts Link's current.pos at
// warp_x/y/z and copies warp_seq into warp_ack (multiplayer.c:344-345,
// :365-421). It reports whether it moved Link.
func WarpHandler(t testing.TB, m *mem.FakeMem) bool {
	link, err := mem.ReadU32(m, addr.PlayerPtr)
	if err != nil || link == 0 {
		return false
	}
	w, err := m.Read(mailbox.Mailbox+mailbox.OffWarpSeq, 20)
	if err != nil || bytes.Equal(w[:4], w[4:8]) {
		return false
	}
	if err := m.Store(link+addr.ActorOffCurPos, w[8:20]); err != nil {
		t.Error(err)
	}
	if err := m.Store(mailbox.Mailbox+mailbox.OffWarpAck, w[:4]); err != nil {
		t.Error(err)
	}
	return true
}

// RunWarpHandler runs WarpHandler about every millisecond, as the game's
// frames would, until the test ends. It returns the number of warps made
// so far.
func RunWarpHandler(t testing.TB, m *mem.FakeMem) func() int {
	ctx, cancel := context.WithCancel(context.Background())
	var n atomic.Int32
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			if WarpHandler(t, m) {
				n.Add(1)
			}
			time.Sleep(time.Millisecond)
		}
	}()
	t.Cleanup(func() { cancel(); wg.Wait() })
	return func() int { return int(n.Load()) }
}

// LinkPos reads current.pos of the actor at actor.
func LinkPos(t testing.TB, m mem.Reader, actor uint32) [3]float32 {
	t.Helper()
	b, err := m.Read(actor+addr.ActorOffCurPos, 12)
	if err != nil {
		t.Fatal(err)
	}
	var p [3]float32
	for i := range p {
		p[i] = math.Float32frombits(binary.BigEndian.Uint32(b[4*i:]))
	}
	return p
}

// PosBytes is p as the game stores a position: three big-endian f32.
func PosBytes(p [3]float32) []byte {
	var b []byte
	for _, v := range p {
		b = binary.BigEndian.AppendUint32(b, math.Float32bits(v))
	}
	return b
}
