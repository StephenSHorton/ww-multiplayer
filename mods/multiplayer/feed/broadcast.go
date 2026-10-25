package feed

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/fxsync"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/tunicsync"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/vissync"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player"
)

// BroadcastPose sends this Dolphin's Link (position, then the localized
// pose with the face and appearance trailer) to serverAddr every 16 ms
// until ctx ends, reconnecting after a drop. It returns an error
// instead of os.Exit so host/join can surface failures cleanly, and honors
// ctx cancellation so SIGINT doesn't have to wait for the next 50 ms tick.
// connLog reports failed connects to addr; nil makes one that logs through
// rep (host/join pass one shared with puppet-sync so each line shows once).
// onPos, when non-nil (the TUI path via liveStateLocalPosSetter), is invoked
// every tick this Dolphin's own position resolves — #22's minimap bridge.
// CLI callers pass nil.
func BroadcastPose(ctx context.Context, env Env, name, serverAddr string, dolphinPID uint32, rep log.Reporter, connLog *network.ConnectLog, onPos func(x, y, z float32)) error {
	log.Logf(rep, log.Info, "=== Broadcast Link + Pose: %s -> %s ===", name, serverAddr)
	d, err := env.Open(dolphinPID)
	if err != nil {
		return err
	}
	defer d.Close()
	client := env.NewClient(ctx, name)
	client.OnLog = func(msg string) { rep.Log(log.Net, msg) }
	link := player.NewLink(d)

	// Projectile events (fxsync, inject/include/wwfx.h; WW_PROJECTILES=on,
	// default off): the REL's outbox of this Link's bombs, cannonballs and
	// arrows, sent as MsgFx. Its words are written only into a game the
	// contract check allows (quietly: puppet-sync reports the verdict),
	// and the send word goes back to 0 when the broadcast ends.
	fxSettings := fxsync.SettingsFromEnv()
	var fx fxsync.Sender
	fxGate := newContractGate(d, log.Discard{})
	if fxSettings.On {
		rep.Log(log.Info, "Projectile events on (WW_PROJECTILES): this Link's bombs, cannonballs and arrows are sent.")
		defer func() {
			if fxGate.ok {
				_ = fx.Arm(d, false)
			}
		}()
	}

	// Session 14: face-state capture reads from mailbox.face_state_local,
	// which the C side publishes once per frame at the top of
	// daPy_draw_hook BEFORE any face_emit_swap_for_slot fires. Eliminates
	// the s13 race where direct tev_block reads could catch the bracket's
	// transient swapped value and ship that to the remote.

	// Auto-reconnect (#8): connect/run callbacks drive a ReconnectLoop.
	// A transient drop ends run() and ReconnectLoop re-dials with
	// exponential backoff; only a real ctx-cancel ends this function.
	// connLog turns the otherwise silent retries into log lines.
	if connLog == nil {
		connLog = NewConnectLog(serverAddr, rep)
	}
	connect := func(ctx context.Context) error {
		return connLog.Observe(ctx, client.ConnectContext(ctx, serverAddr))
	}
	// sessionLoop is one live session's 16 ms send loop. network.RunSession
	// (in run, below) wraps it with a ctx-scoped teardown watcher and WAITS
	// for that watcher before returning, so a reconnect can't overlap a
	// stale watcher's (session-agnostic) Disconnect with the next session's
	// freshly-dialed conn — the self-chaining flap. The child ctx passed
	// here also drives the select below, so a parent cancel exits promptly.
	sessionLoop := func(ctx context.Context) error {
		posErrs, poseErrs := 0, 0
		var equip vissync.EquipSampler // the shield bit, debounced (two samples)
		for client.IsConnected() {
			// Position (cheap; same as broadcast-link).
			pos, err := link.Position()
			if err == nil && pos != nil {
				netPos := &network.PlayerPosition{
					PosX: pos.PosX, PosY: pos.PosY, PosZ: pos.PosZ,
					RotY: pos.RotY,
				}
				if err := client.SendPosition(netPos); err != nil {
					posErrs++
					if posErrs > 5 {
						return fmt.Errorf("send position: %w", err)
					}
				}
				// #22 minimap: publish this Dolphin's own position into
				// liveState (TUI path only — onPos is nil on every CLI
				// entry point).
				if onPos != nil {
					onPos(pos.PosX, pos.PosY, pos.PosZ)
				}
			}

			// Pose. Read from the sender-side publish buffer rather than
			// Link #1's live mpNodeMtx. The C draw hook memcpys mpNodeMtx
			// into this GameHeap-resident buffer once per frame AFTER
			// daPy_lk_c_draw returns, so our read can't catch mid-calc
			// torn state. Previous direct-mpNodeMtx reads at 20 Hz raced
			// the game's 60 Hz calc pass and produced visibly wrong poses
			// on the receiver when slope-IK made per-frame mpNodeMtx delta
			// large (observed v0.1.2: leg flapping 0-90° on slopes).
			//
			// Protocol: ship the raw 2016 B AFTER localizing — subtract
			// Link's world position from each joint's translation column so
			// the pose is relative to Link's origin (rotation parts
			// unchanged). Receiver re-adds the remote's world position to
			// land Link #2 at the right world coords.
			if pos != nil {
				data, err := link.Pose()
				if err == nil && data != nil {
					if os.Getenv("WW_POSE_RAW") == "" {
						avatar.Localize(data, avatar.JointCount, pos.PosX, pos.PosY, pos.PosZ)
					}
					// Capture the LOCAL Link's eye texno bytes from
					// the mailbox slot the C side publishes at the
					// top of daPy_draw_hook (before any face_emit
					// swap fires). Race-free vs reading tev_block
					// directly from emu RAM. Wire layout matches
					// FaceState: 8 B = (mat1_tex0:u16 BE)
					// (mat1_tex1:u16 BE)(mat4_tex0:u16 BE)
					// (mat4_tex1:u16 BE).
					var face []byte
					if fb, err := link.Face(); err == nil && len(fb) == player.FaceBytes {
						face = fb
						// docs/09 §5.4: a 17th byte, the appearance
						// flags (bit 0 = casual clothes). Receivers
						// before it copy only the first 16 bytes.
						if app, ok := tunicsync.LocalAppearance(d); ok {
							// vissync: the sea, sword-out and
							// shield-in-hand bits, then the two
							// equip bytes (19 bytes in all).
							if extra, eq, ok := equip.Sample(d); ok {
								face = append(face, app|extra, eq[0], eq[1])
							} else {
								face = append(face, app)
							}
						}
					}
					if err := client.SendPose(avatar.JointCount, data, face); err != nil {
						poseErrs++
						if poseErrs > 5 {
							return fmt.Errorf("send pose: %w", err)
						}
					}
				}
			}

			// Projectile events: drain the REL's outbox (fxsync).
			if fxSettings.On {
				if ok, _ := fxGate.check(time.Now()); ok && fx.Arm(d, true) == nil {
					_, _ = fx.Drain(d, client.SendFx)
				}
			}

			// pos can be nil during a brief reload window where Link's
			// actor pointer is 0 (player on the main menu / save loading).
			// We used to print a per-tick `link+pose -> X:Y:Z\r` heartbeat
			// here; the TUI dashboard now polls Dolphin position directly
			// for that field, so the heartbeat just spammed the log panel.
			// CLI users still see the [net] connect line as proof of life.

			// 16 ms ≈ 60 Hz, matching Dolphin's render rate so the receiver
			// gets a fresh pose every game frame. ~120 KB/s of pose data —
			// trivial for LAN; fine for typical home upload over the
			// internet too. Was 50 ms before v0.1.7 — the 3-frame cadence
			// mismatch made remote Links visibly snap between samples.
			select {
			case <-ctx.Done():
				// Fall through; IsConnected() will be false next iteration
				// (RunSession's watcher already called Disconnect()).
			case <-time.After(16 * time.Millisecond):
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

// NewConnectLog returns a network.ConnectLog that reports connect failures
// to addr (and the recovery after them) through rep, so they reach the CLI
// output or the TUI log panel.
//
// A fatal error ("Can't join <addr>: <reason>") is logged at Err, once for
// the session; callers then skip logging the returned ErrFatal error (see
// logSessionErr), so the reason appears exactly once and becomes the TUI's
// session-ended reason.
func NewConnectLog(addr string, rep log.Reporter) *network.ConnectLog {
	l := network.NewConnectLog(addr,
		func(msg string) { rep.Log(log.Warn, msg) },
		func(msg string) { rep.Log(log.OK, msg) })
	l.Fatal = func(msg string) { rep.Log(log.Err, msg) }
	return l
}
