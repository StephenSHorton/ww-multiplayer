package feed

import (
	"context"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// TestFeederResetGate: with the heartbeat moving, poses are written; it
// standing still for heartbeatStall, mDoRst's mReset set, or the reset
// block's pointer 0 (the reboot, before main sets it) stops every pose
// write, each logged once; the game running again resumes them. Puppet-
// sync holds the tunic images to Running.
func TestFeederResetGate(t *testing.T) {
	f, c, rep := newTestFeeder(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	tick := func(frame bool) byte {
		if frame {
			c.ram.Frame()
		}
		f.Feed(ctx, []network.RemotePlayer{remote(1, 100, true, now)}, now)
		now = now.Add(16 * time.Millisecond)
		return c.poseSeq(0)
	}
	// moving tells whether the pose seq moves over n ticks.
	moving := func(frame bool, n int) bool {
		was := tick(frame)
		for i := 1; i < n; i++ {
			tick(frame)
		}
		return c.poseSeq(0) != was
	}
	if !moving(true, 4) || !f.Running() {
		t.Fatal("running game: no poses written")
	}
	// The frames stop (a reboot): writes go on for heartbeatStall, then stop.
	tick(false)
	if !f.Running() {
		t.Error("closed one tick after the last frame")
	}
	for i := 0; i < int(heartbeatStall/(16*time.Millisecond)); i++ {
		tick(false)
	}
	if f.Running() || moving(false, 5) {
		t.Error("poses still written with the heartbeat stopped")
	}
	if !rep.has("frames have stopped") {
		t.Error("no pause line")
	}
	if !moving(true, 3) || !f.Running() || !rep.has("pose feed resumed") {
		t.Error("frames back: poses not written again")
	}

	// A reset asked for: closed while frames still run.
	c.ram.Put32(resetData+addr.ResetDataOffReset, 1)
	tick(true)
	if f.Running() || moving(true, 4) || !rep.has("the game is resetting") {
		t.Error("poses written with mReset set")
	}
	// Rebooted: the reset block's pointer is 0 until main sets it.
	c.ram.Put32(resetData+addr.ResetDataOffReset, 0)
	c.ram.Put32(addr.ResetDataPtr, 0)
	tick(true)
	if f.Running() || moving(true, 4) || !rep.has("the game is rebooting") {
		t.Error("poses written with no reset block")
	}
	c.ram.Put32(addr.ResetDataPtr, resetData)
	if !moving(true, 3) || !f.Running() {
		t.Error("the new boot running: poses not written again")
	}
}

// TestFeederResetGateStillCounter: a heartbeat never seen moving (a
// fake, or the first read) leaves the gate open.
func TestFeederResetGateStillCounter(t *testing.T) {
	f, c, _ := newTestFeeder(t)
	now := time.Unix(1000, 0)
	for i := 0; i < 20; i++ {
		f.Feed(context.Background(), []network.RemotePlayer{remote(1, 100, true, now)}, now)
		now = now.Add(16 * time.Millisecond)
	}
	if !f.Running() || !c.wrotePoseOf(0, 1) {
		t.Error("a still counter closed the gate")
	}
}

// TestFeederArmPollBounded: out of play (the title screen after a reset)
// three pose remotes poll nothing; in play with the C side not
// allocating, one Feed waits through at most one 60-step poll, not one
// per slot.
func TestFeederArmPollBounded(t *testing.T) {
	f, c, _ := newTestFeeder(t)
	waits := 0
	f.after = func(time.Duration) <-chan time.Time {
		waits++
		ch := make(chan time.Time, 1)
		ch <- time.Time{}
		return ch
	}
	now := time.Unix(1000, 0)
	three := []network.RemotePlayer{remote(1, 100, true, now), remote(2, 200, true, now), remote(3, 300, true, now)}
	c.stage("sea", 0, false) // no Link: the title screen
	f.Feed(context.Background(), three, now)
	if waits != 0 {
		t.Errorf("out of play: %d poll waits, want 0", waits)
	}
	c.stage("sea", 0x80A00000, false)
	now = now.Add(16 * time.Millisecond)
	f.Feed(context.Background(), three, now)
	if waits != 60 {
		t.Errorf("in play, nothing allocated: %d poll waits, want 60 (one poll)", waits)
	}
	if len(f.LinkSlots()) != 3 {
		t.Errorf("link slots %v, want three assigned", f.LinkSlots())
	}
}
