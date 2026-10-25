package feed

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/tunicsync"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/vissync"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// MaxPuppets is the number of actor-puppet slots in the mailbox. The
// mailbox layout (base, field offsets, Puppet slot fields) is generated
// from inject/include/mailbox_core.h into wwapi/mailbox.
const MaxPuppets = mailbox.MaxPuppets

// SlotAddr is the address of field off (a mailbox.PuppetOff* offset)
// in puppet slot i.
func SlotAddr(i int, off uint32) uint32 {
	return mailbox.Mailbox + mailbox.OffPuppets + uint32(i)*mailbox.PuppetSize + off
}

// EnvFloat32 reads an environment variable as a float32, returning the
// fallback if unset or unparseable. Used for tunables that need to be
// changed per-launch (offsets, throttles) without a recompile.
func EnvFloat32(key string, fallback float32) float32 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 32)
	if err != nil {
		fmt.Printf("warn: bad %s=%q (%v); using %g\n", key, v, err, fallback)
		return fallback
	}
	return float32(f)
}

// PuppetSync connects to addr, receives the remote players and draws
// each one in this Dolphin (a pose-feed Link slot, else an actor puppet)
// every 16 ms until ctx ends, reconnecting after a drop. It takes the
// self-filter name as a parameter (rather than reading WW_SELF_NAME) so host/join can
// plumb the player's name through without exporting an env var.
//
// connLog reports failed connects to addr, as in BroadcastPose; nil
// makes one that logs through rep.
//
// onClient, when non-nil, is invoked once with the freshly-created
// *network.Client so a caller (the TUI bridge) can register it for its status
// panel. The client survives reconnects (#8) — it's the same object across
// ReconnectLoop iterations — so a single registration is sufficient. CLI
// callers pass nil.
func PuppetSync(ctx context.Context, env Env, name, addr, selfFilter string, dolphinPID uint32, rep log.Reporter, connLog *network.ConnectLog, onClient func(*network.Client)) error {
	log.Logf(rep, log.Info, "=== Puppet Sync: %s <- %s ===", name, addr)

	d, err := env.Open(dolphinPID)
	if err != nil {
		return err
	}
	defer d.Close()
	rep.Log(log.OK, "Dolphin found.")

	// The strict contract check (Phase E1): nothing is written into a
	// game that does not run this version's mod (an unpatched disc, one
	// patched by another version, or a save state taken on one). It runs
	// now, and again every ContractEvery while the session plays.
	gate := newContractGate(d, rep)
	gate.check(time.Now())

	client := env.NewClient(ctx, name)
	client.OnLog = func(msg string) {
		rep.Log(log.Net, msg)
	}
	if onClient != nil {
		onClient(client)
	}
	// Tunic colors (docs/09 §5.4): per-slot recolored body textures.
	// WW_TUNIC=off leaves the slots to the tunic-slot dev command.
	var tunics *avatar.Tunics
	if os.Getenv("WW_TUNIC") != "off" {
		tunics = avatar.NewTunics(d, func(format string, args ...any) { log.Logf(rep, log.Info, format, args...) })
		// Session over: stop the C swaps (the enables live in RAM and
		// would keep the local Link recolored). Runs before d.Close, and
		// only into a game the contract check allows.
		defer func() {
			if gate.ok {
				tunics.DisableAll()
			}
		}()
	}

	// Slot assignment, lerp state and arming live in the Feeder; it is
	// created once here so its state SURVIVES reconnects (#8): the
	// connect/run callbacks for ReconnectLoop share it, and a transient
	// drop must not lose slot assignments.
	feeder := NewFeeder(d, selfFilter, rep)
	// The stage gate (partner.go): co-op's coop-sync of this session
	// publishes where the partner is; without co-op nothing is.
	feeder.partner = PartnerFor(name, dolphinPID)
	// The session's roster (roster.go): what this feeder draws, for the
	// enemy host pipeline's remote bodies. Emptied when the sync stops.
	roster := RosterFor(name, dolphinPID)
	defer roster.Publish(nil, time.Now())
	// Session over: disarm face sync (F20). The flag lives in RAM and
	// would keep the C bracket swapping stale face_state into the next
	// session's mini-Links. Only into a game the contract check allows.
	defer func() {
		if gate.ok {
			avatar.SetFaceSync(d, false)
		}
	}()

	// The remote players' visuals (vissync, inject/include/wwvis.h): names,
	// sword and shield, sea chart markers. WW_NAMETAGS / WW_EQUIP /
	// WW_SEACHART=off turn each off. Session over: the block goes off.
	visSettings := vissync.SettingsFromEnv()
	var vis vissync.Sync
	defer func() {
		if gate.ok {
			_ = vis.Off(d)
		}
	}()

	// connect (re)establishes the session and RE-ARMS the mailbox (#8 + D3).
	// Runs at startup and after every reconnect. A change in the welcome's
	// server epoch means the server restarted → full puppet reset.
	if connLog == nil {
		connLog = NewConnectLog(addr, rep)
	}
	connect := func(ctx context.Context) error {
		if err := connLog.Observe(ctx, client.ConnectContext(ctx, addr)); err != nil {
			return err
		}
		if gate.ok {
			feeder.Connected(client.ServerEpoch())
		}
		return nil
	}

	// selfFilter lets a puppet-sync attached to the SAME Dolphin as a
	// broadcast-pose twin ignore its twin's stream. Without this, the
	// twin's pose (= our local Link's live position) gets written into
	// a puppet actor that then physics-collides with our own Link. Empty
	// (default) keeps the loopback "mirror yourself with offset" demo
	// working. The standalone puppet-sync CLI reads WW_SELF_NAME into
	// this arg; `ww-multiplayer.exe host/join` pass the player name
	// so users never have to know the env var exists.
	if selfFilter != "" {
		log.Logf(rep, log.Info, "Filtering self-echo: remotes named %q will be ignored.", selfFilter)
	}

	// sessionLoop owns one live session: the 60 Hz receive→mailbox loop.
	// network.RunSession (in run, below) wraps it with a ctx-scoped teardown
	// watcher and WAITS for that watcher before returning, so a reconnect
	// can't overlap a stale watcher's (session-agnostic) Disconnect with the
	// next session's freshly-dialed conn — the self-chaining flap.
	sessionLoop := func(ctx context.Context) error {
		for client.IsConnected() {
			remotes := client.GetRemotePlayers()
			now := time.Now()
			if ok, regained := gate.check(now); !ok {
				// Refused: write nothing until the game runs this
				// version's mod again.
				select {
				case <-ctx.Done():
				case <-time.After(250 * time.Millisecond):
				}
				continue
			} else if regained {
				feeder.Connected(client.ServerEpoch())
			}
			feeder.Feed(ctx, remotes, now)
			roster.Publish(feeder.Views(remotes, now), now)

			if tunics != nil {
				mine, haveMine := client.MyColor()
				tunicsync.SyncTunics(tunics, mine, haveMine, remotes, feeder.LinkSlots(), now)
			}

			_ = vis.Write(d, vissync.Build(visSettings, remotes, feeder.LinkSlots(), selfFilter))

			feeder.Expire(now)

			select {
			case <-ctx.Done():
				// RunSession's watcher will Disconnect(); IsConnected() will
				// flip false on the next iteration and the loop exits.
			case <-time.After(16 * time.Millisecond): // ~60 Hz
			}
		}
		rep.Log(log.Info, "Disconnected.")
		return nil
	}
	run := func(ctx context.Context) error {
		return network.RunSession(ctx, client.Disconnect, sessionLoop)
	}

	return network.ReconnectLoop(ctx, connect, run, network.NewBackoff(), network.SleepCtx)
}
