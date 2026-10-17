// Package player reads and drives the local Link: his actor pointer,
// position and angle, the pose and face the C draw hook publishes, the
// mailbox warp, and the mailbox controller override.
//
// Every function takes a mem.Reader or mem.Mem, so it runs the same on a
// live *emu.Dolphin and on a mem.FakeMem.
//
// # v0 UNSTABLE
//
// This package is part of wwapi v0. Its API may change in any 0.x minor
// version.
package player

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// Warp asks the C-side warp handler to put Link at p: it writes p into
// the warp mailbox slot, bumps warp_seq, and waits up to timeout for
// warp_ack to reach the new warp_seq. mp-local uses it to space the two
// Links after both Dolphins boot from the same SAVE_STATE (startupShift),
// and the `warp` subcommand for ad-hoc teleporting.
//
// A request the game has not taken by then is withdrawn. Link is gone (a
// stage change or a reload) or emulation is paused, and the handler runs
// on the first frame Link exists whenever warp_seq != warp_ack
// (multiplayer.c:344-345, :365). Left armed, the request would land later
// in whatever stage the game is in by then, where p means another place,
// and the co-op /warp would refuse while it waits. The withdraw writes
// warp_ack = the new warp_seq, which is what the handler itself writes
// (multiplayer.c:421). The handler only ever copies warp_seq into
// warp_ack, so if it takes the request between the last read below and
// the write, both store the same value: Link is moved at most once,
// warp_seq never goes backwards, and a later warp_seq from another writer
// still differs from warp_ack and is carried out. In that race the error
// says withdrawn although Link was moved.
//
// The withdraw is skipped when warp_seq reads as anything but the new
// value. A save state loaded meanwhile restores the mailbox with the rest
// of MEM1, so warp_seq = warp_ack again and warp_x/y/z are the state's:
// writing warp_ack = the new warp_seq then would arm a warp to those
// coordinates. The mailbox is then left as it is, and so it is when
// another writer has bumped warp_seq since. A read of warp_seq that fails
// does not stop the withdraw, so a load in that case is missed, as is a
// load between that read and the write, a window microseconds wide.
//
// warp_ack is no WriteGuard target (§6.7, docs/08 item 75). Warp runs
// from mp-local's startup shift, which never runs under --coop, from the
// `warp` dev CLI (docs/08 item 131), and from wwapi.Game's
// core.player.warp command. Any mod, and anything holding
// Game.Commands(), can run core.player.warp: nothing checks the mod's
// manifest or whether co-op is on, only the contract's write gate. Today
// only mods/example calls it and the app loads no mods yet. It shares
// warp_seq with co-op's replica escape warps, so it must not run beside
// co-op until D4 adds a guard (docs/api/00-design.md, D4).
func Warp(m mem.Mem, p [3]float32, timeout time.Duration) error {
	const seqAddr, ackAddr = mailbox.Mailbox + mailbox.OffWarpSeq, mailbox.Mailbox + mailbox.OffWarpAck

	// Bump from whatever seq is currently set (cold start = 0).
	prevSeq, err := mem.ReadU32(m, seqAddr)
	if err != nil {
		return fmt.Errorf("read warp_seq: %w", err)
	}
	nextSeq := prevSeq + 1

	// Write coords first, THEN the seq bump — C reads seq under a single
	// frame's tick, and a torn write where seq updates before coords
	// would warp Link to whatever stale (x,y,z) was in the slot.
	posBuf := make([]byte, 12)
	for i, v := range p {
		binary.BigEndian.PutUint32(posBuf[4*i:], math.Float32bits(v))
	}
	if err := m.Write(mailbox.Mailbox+mailbox.OffWarpX, posBuf); err != nil {
		return fmt.Errorf("write warp coords: %w", err)
	}
	if err := mem.WriteU32(m, seqAddr, nextSeq); err != nil {
		return fmt.Errorf("write warp_seq: %w", err)
	}

	// Poll for ack — game runs at 60 Hz so we'd expect this within ~17 ms,
	// but be generous in case the multiplayer_update hook missed a tick.
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ack, err := mem.ReadU32(m, ackAddr); err == nil && ack == nextSeq {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Withdraw, unless the game took it after the last poll, or warp_seq
	// is no longer ours.
	if ack, err := mem.ReadU32(m, ackAddr); err == nil && ack == nextSeq {
		return nil
	}
	if seq, err := mem.ReadU32(m, seqAddr); err == nil && seq != nextSeq {
		return fmt.Errorf("warp not acknowledged within %s, and warp_seq is now %d, not %d (a save state loaded?); left as it is", timeout, seq, nextSeq)
	}
	if err := mem.WriteU32(m, ackAddr, nextSeq); err != nil {
		return fmt.Errorf("warp not acknowledged within %s, and withdrawing it failed: %w", timeout, err)
	}
	return fmt.Errorf("warp not acknowledged within %s (Link gone or emulation paused); withdrawn", timeout)
}

// WarpForce turns on the C-side force-warp mode with p as the target:
// while warp_force is set, the warp handler re-applies p every frame
// regardless of warp_seq/warp_ack. It writes the coordinates first and
// warp_force last. It is a research aid (the `warp-force` subcommand),
// used to test whether continuous writes outpace whatever reverts a
// one-shot warp.
func WarpForce(m mem.Mem, p [3]float32) error {
	posBuf := make([]byte, 12)
	for i, v := range p {
		binary.BigEndian.PutUint32(posBuf[4*i:], math.Float32bits(v))
	}
	if err := m.Write(mailbox.Mailbox+mailbox.OffWarpX, posBuf); err != nil {
		return err
	}
	return m.Write(mailbox.Mailbox+mailbox.OffWarpForce, []byte{1})
}

// WarpForceOff clears warp_force, ending WarpForce.
func WarpForceOff(m mem.Mem) error {
	return m.Write(mailbox.Mailbox+mailbox.OffWarpForce, []byte{0})
}
