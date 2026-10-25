// Package feed is the multiplayer mod's pose pipelines (docs/api
// 00-design.md §7): BroadcastPose streams this Dolphin's Link (position,
// pose, face and appearance) to the server, and PuppetSync draws every
// remote player in this Dolphin, as a mini-Link in one of the avatar.Cap
// pose-feed slots or, beyond those, as a legacy actor puppet. PoseFakeLoop
// is the loopback dev source (pose-fake-loop).
//
// They were runBroadcastPoseCtx, runPuppetSyncCtx and runPoseFakeLoop in
// package main; the CLI wrappers (broadcast-pose, puppet-sync)
// stay there, and mods/multiplayer/engine's host and join sessions call
// these.
package feed

import (
	"context"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
)

// Env is what the pipelines take from the program that runs them.
type Env struct {
	// Open attaches to the Dolphin with this PID; 0 means the one the
	// environment selects (WW_DOLPHIN_INDEX / WW_DOLPHIN_PID).
	Open func(pid uint32) (*emu.Dolphin, error)
	// NewClient makes a player connection for name, with the color
	// preference of the session ctx belongs to.
	NewClient func(ctx context.Context, name string) *network.Client
	// TestRate, when non-nil, makes BroadcastPose sample at its rate and
	// send each sample late by its latency and jitter (testrate.go). Only
	// wwdev sets it (WW_POSE_TEST_RATE); nil is the normal 16 ms stream.
	TestRate *TestRate
}
