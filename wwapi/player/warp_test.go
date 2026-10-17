package player

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player/playertest"
)

func readU32(t *testing.T, m mem.Mem, a uint32) uint32 {
	t.Helper()
	v, err := mem.ReadU32(m, a)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestWarpWithdrawsOnTimeout: with nothing taking the request (Link
// gone, emulation paused), Warp withdraws it, so it does not land
// once Link is back.
func TestWarpWithdrawsOnTimeout(t *testing.T) {
	m := mem.NewFakeMem()
	store(t, m, mailbox.Mailbox+mailbox.OffWarpSeq, u32b(6))
	store(t, m, mailbox.Mailbox+mailbox.OffWarpAck, u32b(6))
	err := Warp(m, [3]float32{1, 2, 3}, 20*time.Millisecond)
	const want = "warp not acknowledged within 20ms (Link gone or emulation paused); withdrawn"
	if err == nil || err.Error() != want {
		t.Errorf("got %v, want %q", err, want)
	}
	if seq, ack := readU32(t, m, mailbox.Mailbox+mailbox.OffWarpSeq), readU32(t, m, mailbox.Mailbox+mailbox.OffWarpAck); seq != 7 || ack != 7 {
		t.Errorf("warp_seq %d, warp_ack %d after the withdraw, want both 7", seq, ack)
	}
	// Link comes back (the stage loaded, emulation resumed): the handler
	// finds nothing to do.
	store(t, m, addr.PlayerPtr, u32b(fakegame.PlayerActor))
	if playertest.WarpHandler(t, m) {
		t.Errorf("the withdrawn warp landed later, at %v", playertest.LinkPos(t, m, fakegame.PlayerActor))
	}
}

// TestWarpAcked: a warp the game takes returns nil, and the request
// is written coordinates first, warp_seq last.
func TestWarpAcked(t *testing.T) {
	m := mem.NewFakeMem()
	store(t, m, addr.PlayerPtr, u32b(fakegame.PlayerActor))
	warps := playertest.RunWarpHandler(t, m)
	m.Record(true)
	p := [3]float32{-200050, 12.5, 300020}
	if err := Warp(m, p, time.Second); err != nil {
		t.Fatal(err)
	}
	m.Record(false)
	if got := playertest.LinkPos(t, m, fakegame.PlayerActor); got != p {
		t.Errorf("Link at %v, want %v", got, p)
	}
	ws := m.Writes()
	if len(ws) != 2 ||
		ws[0].Addr != mailbox.Mailbox+mailbox.OffWarpX || !bytes.Equal(ws[0].Data, playertest.PosBytes(p)) ||
		ws[1].Addr != mailbox.Mailbox+mailbox.OffWarpSeq || !bytes.Equal(ws[1].Data, []byte{0, 0, 0, 1}) {
		t.Errorf("writes %+v, want the 12 coordinate bytes then warp_seq = 1", ws)
	}
	if n := warps(); n != 1 {
		t.Errorf("%d warps, want 1", n)
	}
}

// raceMem runs before just ahead of a write to warp_ack: the game taking
// the request between Warp's last read of warp_ack and its
// withdraw.
type raceMem struct {
	*mem.FakeMem
	before func()
}

func (r raceMem) Write(a uint32, b []byte) error {
	if a == mailbox.Mailbox+mailbox.OffWarpAck {
		r.before()
	}
	return r.FakeMem.Write(a, b)
}

// TestWarpAckRace: when the game takes the request just as it is
// withdrawn, Link is moved exactly once, and warp_seq == warp_ack after.
func TestWarpAckRace(t *testing.T) {
	fm := mem.NewFakeMem()
	p := [3]float32{10, 20, 30}
	warps, withdraws := 0, 0
	m := raceMem{FakeMem: fm, before: func() {
		withdraws++
		store(t, fm, addr.PlayerPtr, u32b(fakegame.PlayerActor)) // Link is back
		if playertest.WarpHandler(t, fm) {
			warps++
		}
	}}
	err := Warp(m, p, 20*time.Millisecond)
	if withdraws != 1 || err == nil || !strings.HasSuffix(err.Error(), "; withdrawn") {
		t.Fatalf("%d writes to warp_ack, error %v; want one withdraw, which can't see that the game took the request just before it", withdraws, err)
	}
	for range 5 { // later frames
		if playertest.WarpHandler(t, fm) {
			warps++
		}
	}
	if warps != 1 {
		t.Errorf("%d warps, want exactly 1", warps)
	}
	if got := playertest.LinkPos(t, fm, fakegame.PlayerActor); got != p {
		t.Errorf("Link at %v, want %v", got, p)
	}
	if seq, ack := readU32(t, fm, mailbox.Mailbox+mailbox.OffWarpSeq), readU32(t, fm, mailbox.Mailbox+mailbox.OffWarpAck); seq != 1 || ack != 1 {
		t.Errorf("warp_seq %d, warp_ack %d, want both 1", seq, ack)
	}
}

// loadMem stands in for a save state loaded just as Warp bumps
// warp_seq: in place of that write, the mailbox goes back to the state's,
// with warp_seq = warp_ack = 0 and warp_x/y/z = 0, as in a state captured
// from a cold boot.
type loadMem struct {
	*mem.FakeMem
	t *testing.T
}

func (l loadMem) Write(a uint32, b []byte) error {
	if a == mailbox.Mailbox+mailbox.OffWarpSeq {
		store(l.t, l.FakeMem, mailbox.Mailbox+mailbox.OffWarpSeq, make([]byte, 20)) // warp_seq, warp_ack, warp_x/y/z
		return nil
	}
	return l.FakeMem.Write(a, b)
}

// TestWarpSaveStateLoaded: a save state loaded under the request
// takes it away, and Warp then leaves the mailbox as the state
// left it. A withdraw would write warp_ack = 1 over the state's warp_seq
// of 0, and the next frame would warp Link to the state's (0, 0, 0).
func TestWarpSaveStateLoaded(t *testing.T) {
	fm := mem.NewFakeMem()
	start := [3]float32{-200000, 120, 300000}
	store(t, fm, addr.PlayerPtr, u32b(fakegame.PlayerActor))
	store(t, fm, fakegame.PlayerActor+addr.ActorOffCurPos, playertest.PosBytes(start))
	warps := playertest.RunWarpHandler(t, fm)
	fm.Record(true)
	err := Warp(loadMem{FakeMem: fm, t: t}, [3]float32{10, 20, 30}, 50*time.Millisecond)
	fm.Record(false)
	const want = "warp not acknowledged within 50ms, and warp_seq is now 0, not 1 (a save state loaded?); left as it is"
	if err == nil || err.Error() != want {
		t.Errorf("got %v, want %q", err, want)
	}
	for _, w := range fm.Writes() {
		if w.Addr != mailbox.Mailbox+mailbox.OffWarpX {
			t.Errorf("wrote % X at %#x; want only the coordinates, which the load overwrote", w.Data, w.Addr)
		}
	}
	for range 5 { // later frames
		if playertest.WarpHandler(t, fm) {
			t.Errorf("a later frame warped Link to %v", playertest.LinkPos(t, fm, fakegame.PlayerActor))
		}
	}
	if n := warps(); n != 0 {
		t.Errorf("%d warps, want 0", n)
	}
	if got := playertest.LinkPos(t, fm, fakegame.PlayerActor); got != start {
		t.Errorf("Link at %v, want %v", got, start)
	}
}

// TestWarpForce: coordinates first, warp_force last; WarpForceOff clears
// only warp_force.
func TestWarpForce(t *testing.T) {
	m := mem.NewFakeMem()
	m.Record(true)
	p := [3]float32{1.5, -2, 300000}
	if err := WarpForce(m, p); err != nil {
		t.Fatal(err)
	}
	if err := WarpForceOff(m); err != nil {
		t.Fatal(err)
	}
	checkWrites(t, m.Writes(), []mem.WriteRecord{
		{Addr: mailbox.Mailbox + mailbox.OffWarpX, Data: playertest.PosBytes(p)},
		{Addr: mailbox.Mailbox + mailbox.OffWarpForce, Data: []byte{1}},
		{Addr: mailbox.Mailbox + mailbox.OffWarpForce, Data: []byte{0}},
	})
}
