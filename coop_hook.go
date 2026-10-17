package main

import (
	"context"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/engine"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// sessionEnv is what a host or join session (engine.RunHostSession,
// engine.RunJoinSession) takes from package main: the pose pipelines'
// feedEnv, and clearMultiplayerState when the session ends.
var sessionEnv = engine.Env{Feed: feedEnv, Clear: clearMultiplayerState}

// coopHook is the engine.Coop of a host or join session run with co-op
// (--coop): the coopSession that startCoopHost or startCoopSession opens
// with front as its front end, its coop-sync pipeline (runCoopSyncCtx)
// and its warps' pose clients (coopSession.withPoseClient).
type coopHook struct {
	co    coopFlags
	front coopFront
	cs    *coopSession // nil until started
}

// newCoopHook is the co-op side of a session with co, or nil (no co-op)
// when co is off.
func newCoopHook(co coopFlags, front coopFront) engine.Coop {
	if !co.on {
		return nil
	}
	return &coopHook{co: co, front: front}
}

// env is the co-op environment of this Dolphin with the hook's front end.
func (h *coopHook) env() coopEnv {
	env := defaultCoopEnv(0)
	env.front = h.front
	return env
}

// StartHost backs up the cards, opens or founds the co-op world and sets
// the CoopHub as srv's Coop.
func (h *coopHook) StartHost(ctx context.Context, rep log.Reporter, srv *network.Server) error {
	cs, err := startCoopHost(ctx, rep, srv.OnLog, h.co, h.env())
	if err != nil {
		return err
	}
	h.cs = cs
	srv.Coop = cs.hub
	return nil
}

// StartJoin backs up the cards and opens the joiner's co-op side.
func (h *coopHook) StartJoin(rep log.Reporter) {
	h.cs = startCoopSession(rep, h.co, h.env())
}

func (h *coopHook) WithPoseClient(name string, next func(*network.Client)) func(*network.Client) {
	return h.cs.withPoseClient(name, next)
}

// Pipelines is coop-sync, with its own Dolphin handle and co-op
// connection.
func (h *coopHook) Pipelines(name, addr string, rep log.Reporter) []engine.Pipeline {
	return []engine.Pipeline{{Name: "coop-sync", Run: func(ctx context.Context) error {
		return runCoopSyncCtx(ctx, name, addr, 0, rep, h.cs)
	}}}
}

// Stop ends the hub, which writes the world (coopSession.stop).
func (h *coopHook) Stop() { h.cs.stop() }
