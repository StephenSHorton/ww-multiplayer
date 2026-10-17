package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/netutil"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/feed"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// Env is what a host or join session takes from its caller (package
// main): where the pose pipelines find the Dolphin and their player
// clients, and how the Dolphin's mailbox is reset when the session ends.
type Env struct {
	// Feed is the pose pipelines' Env.
	Feed feed.Env
	// Clear resets the Dolphin's mailbox so it stops drawing the remote
	// Links (best-effort). nil skips it.
	Clear func()
}

// clear runs e.Clear when it is set.
func (e Env) clear() {
	if e.Clear != nil {
		e.Clear()
	}
}

// Coop is a session's co-op side: the seam to co-op, which is its own
// mod requiring multiplayer (docs/api/00-design.md §7). A host or join
// session runs without it when nil. mods/coop/session's coopHook
// implements it over its Session (startCoopHost, startCoopSession,
// RunSync).
type Coop interface {
	// StartHost opens co-op on the host before srv starts and sets
	// srv.Coop. An error ends the session before the server starts.
	StartHost(ctx context.Context, rep log.Reporter, srv *network.Server) error
	// StartJoin opens co-op on a joiner.
	StartJoin(rep log.Reporter)
	// WithPoseClient returns the onClient for name's puppet-sync
	// pipeline: it hears the client (where a warp finds the partner),
	// then calls next if set.
	WithPoseClient(name string, next func(*network.Client)) func(*network.Client)
	// Pipelines are the pipelines co-op runs next to the pose pipelines
	// against addr (coop-sync).
	Pipelines(name, addr string, rep log.Reporter) []Pipeline
	// Stop ends co-op. The host stops it after the server.
	Stop()
}

// RunHostSession contains the actual host flow without any signal-handler
// or os.Exit assumptions, so the TUI can drive it from inside its own
// context without fighting Bubble Tea's Ctrl+C handling.
// ls, when non-nil (the TUI path), is the shared status bridge: this session
// registers its server, host IPs, and puppet-sync client into it for the #19
// status panel, and clears it on return. CLI callers pass nil.
// coop, when non-nil (--coop), starts before the server (it backs up the
// cards, opens or founds the co-op world and sets the CoopHub as the
// server's Coop), adds its pipelines, and stops after the server.
// Returns a server start failure or the first pipeline failure (not yet
// logged), or nil when the session ended by ctx cancel.
func RunHostSession(ctx context.Context, cancel context.CancelFunc, env Env, name string, port int, rep log.Reporter, ls *LiveState, coop Coop) error {
	if name == "" {
		name = "Host"
	}
	srv := network.NewServer(port)
	srv.SetMods(ModsFor(ctx))
	srv.OnLog = func(msg string) {
		rep.Log(log.Info, fmt.Sprintf("[srv %s] %s", time.Now().Format("15:04:05"), msg))
	}
	if coop != nil {
		if err := coop.StartHost(ctx, rep, srv); err != nil {
			if ctx.Err() != nil {
				return nil // Ctrl+C while founding waited for the game
			}
			return err
		}
	}
	if err := srv.Start(); err != nil {
		if coop != nil {
			coop.Stop()
		}
		return fmt.Errorf("server start: %w", err)
	}

	ips := ShareAddrs(ListHostIPs(), port)
	if ls != nil {
		ls.Begin("Host", name, ips)
		ls.SetServer(srv)
		defer ls.Reset()
	}

	log.Logf(rep, log.OK, "Hosting as %q on :%d.", name, port)
	if len(ips) > 0 {
		if port == DefaultPort {
			rep.Log(log.Info, "Share one of these IPs with your friend:")
		} else {
			rep.Log(log.Info, "Share one of these addresses with your friend (the :port is needed):")
		}
		for _, ip := range ips {
			log.Logf(rep, log.Info, "  %s", ip)
		}
	} else {
		rep.Log(log.Warn, "(could not auto-detect a LAN IP — check your network settings)")
	}
	rep.Log(log.Info, "Ctrl+C to stop.")

	err := runMultiplayerGoroutines(ctx, cancel, env.Feed, name, fmt.Sprintf("localhost:%d", port), rep, LiveStateClientSetter(ls), LiveStateLocalPosSetter(ls), coop)

	srv.Stop()
	if coop != nil {
		coop.Stop()
	}
	env.clear()
	return err
}

// RunJoinSession is the signal-free, exit-free version that the TUI calls.
// ls, when non-nil (the TUI path), is the shared status bridge: the join role
// registers only its puppet-sync client (no server, no host IPs) and clears the
// bridge on return. CLI callers pass nil. coop, when non-nil (--coop),
// starts before the pipelines (it backs up the cards) and adds its
// pipelines.
// Returns the first pipeline failure (not yet logged), or nil when the
// session ended by ctx cancel.
func RunJoinSession(ctx context.Context, cancel context.CancelFunc, env Env, addr, name string, rep log.Reporter, ls *LiveState, coop Coop) error {
	if name == "" {
		name = "Player"
	}
	// Default port to DefaultPort if the user passed a bare host/IP.
	// netutil.NormalizeAddr is IPv6-aware (bracketed and bare literals),
	// unlike a naive strings.Contains(addr, ":") check, which would
	// double-append a port onto bare IPv6 addresses like "::1".
	addr = netutil.NormalizeAddr(addr, DefaultPort)

	if ls != nil {
		ls.Begin("Join", name, nil)
		defer ls.Reset()
	}

	log.Logf(rep, log.OK, "Joining %s as %q.", addr, name)
	if coop != nil {
		coop.StartJoin(rep)
	}
	rep.Log(log.Info, "Ctrl+C to stop.")

	err := runMultiplayerGoroutines(ctx, cancel, env.Feed, name, addr, rep, LiveStateClientSetter(ls), LiveStateLocalPosSetter(ls), coop)

	env.clear()
	return err
}

// runMultiplayerGoroutines spawns broadcast-pose + puppet-sync against the
// given server address. Player name is passed as selfFilter so the two
// in-process clients don't self-echo on the co-located broadcast/puppet
// twin (the WW_SELF_NAME workaround from mplay2.sh, but automatic). Blocks
// until either goroutine exits or ctx is cancelled, then waits for both to
// finish before returning.
// onClient, when non-nil, is forwarded to the puppet-sync goroutine so its
// *network.Client can be registered with the TUI status bridge. onPos, when
// non-nil, is forwarded to the broadcast-pose goroutine so this Dolphin's own
// position can be registered with the TUI's #22 minimap bridge. CLI callers
// pass nil for both.
//
// Both goroutines dial the same addr, so they share one ConnectLog and an
// unreachable host is reported once rather than once per connection.
// coop, when non-nil (--coop), adds its pipelines (coop-sync, with its
// own Dolphin handle and co-op connection), and hears the puppet-sync
// client for its warps (Coop.WithPoseClient); nil runs the two pose
// pipelines alone, as before co-op.
// Returns RunPipelines' result: the first real failure, or nil on a normal
// ctx-cancel shutdown.
func runMultiplayerGoroutines(ctx context.Context, cancel context.CancelFunc, fe feed.Env, name, addr string, rep log.Reporter, onClient func(*network.Client), onPos func(x, y, z float32), coop Coop) error {
	connLog := feed.NewConnectLog(addr, rep)
	// A warp to the partner takes its position from pose sync (without
	// co-op, onClient is as given).
	if coop != nil {
		onClient = coop.WithPoseClient(name, onClient)
	}
	ps := []Pipeline{
		{Name: "broadcast-pose", Run: func(ctx context.Context) error {
			return feed.BroadcastPose(ctx, fe, name, addr, 0, rep, connLog, onPos)
		}},
		{Name: "puppet-sync", Run: func(ctx context.Context) error {
			return feed.PuppetSync(ctx, fe, name, addr, name, 0, rep, connLog, onClient)
		}},
	}
	if coop != nil {
		ps = append(ps, coop.Pipelines(name, addr, rep)...)
	}
	return RunPipelines(ctx, cancel, rep, ps...)
}
