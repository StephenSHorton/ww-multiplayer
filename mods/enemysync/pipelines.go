package enemysync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	posefeed "github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/feed"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/write"
)

// The two pipelines, in feed.PuppetSync's shape (Env, Open, a client,
// ReconnectLoop): HostPipeline samples this Dolphin's enemies (the kinds
// WW_ENEMY_MIRROR_KINDS enables, DefaultMirrorKinds by default) and streams
// them on a RoleEnemyHost connection; ClientPipeline receives them on a
// RoleEnemyClient connection and mirrors them into this Dolphin. Both are
// behind WW_ENEMY_SYNC=1 (Enabled) in the engine and mp-local.

// Env is what the pipelines take from the program that runs them.
type Env struct {
	// Open attaches to the Dolphin with this PID; 0 means the one the
	// environment selects (WW_DOLPHIN_INDEX / WW_DOLPHIN_PID).
	Open func(pid uint32) (*emu.Dolphin, error)
	// Claims, when non-nil, is the client's claim set for this game (the
	// client Replaces it every tick; co-op's K1 reads it through
	// Claims.Claimed). Nil: nothing is published (no co-op on this game).
	Claims *ClaimSet
	// CoopSwitchSync, when it reports true, says co-op syncs this game's
	// switches: a quiet death's death switch then arrives through co-op
	// and the client does not write it (design death_and_drops.quiet_steps,
	// the BK death switch). Nil: the client writes it.
	CoopSwitchSync func() bool
}

// Enabled reports WW_ENEMY_SYNC=1.
func Enabled() bool { return os.Getenv("WW_ENEMY_SYNC") == "1" }

// Raw reports WW_ENEMY_RAW=1: the client bypasses the smoother and sets
// the block's raw flag (the lag measurement).
func Raw() bool { return os.Getenv("WW_ENEMY_RAW") == "1" }

// Tick is the pipelines' cadence.
const Tick = 16 * time.Millisecond

// HostPipeline runs the host sampler on Dolphin dolphinPID against the
// server at addr as name until ctx ends, reconnecting after a drop. The
// host's Go never writes an actor; it writes the block only when this
// game's REL module is linked with this build: a client's hit to apply,
// and a keepalive every KeepaliveEvery between hits.
func HostPipeline(ctx context.Context, env Env, name, addr string, dolphinPID uint32, rep log.Reporter, connLog *network.ConnectLog) error {
	log.Logf(rep, log.Info, "=== Enemy host: %s -> %s ===", name, addr)
	d, err := env.Open(dolphinPID)
	if err != nil {
		return err
	}
	defer d.Close()
	rep.Log(log.OK, "Dolphin found.")

	start := time.Now()
	epoch := uint32(start.Unix() & 0xFFFF)
	host := NewHost(d, rep, epoch, start)
	applier := NewHitApplier(d, rep, epoch)
	applier.Kinds = host.Kinds
	log.Logf(rep, log.Info, "enemy host: mirroring %s (WW_ENEMY_MIRROR_KINDS), selecting by %s (WW_ENEMY_SELECT), STATE 30 Hz gate %v", host.Kinds, host.Select, host.State30Hz)
	defer func() {
		if err := applier.Shutdown(); err != nil {
			log.Logf(rep, log.Warn, "enemy host: clearing the block: %v", err)
		}
	}()
	// The remote-Link side (players.go): the WWRP block only under
	// WW_ENEMY_REMOTE; the remote players' mini-Links come from this
	// session's pose feed (posefeed.RosterFor: PuppetSync runs under the same
	// name on the same Dolphin).
	cfg := RemoteConfigFromEnv(rep)
	if cfg.On {
		log.Logf(rep, log.Info, "enemy host: remote-Link block on, flags 0x%04X, %d policies, FF denylist %v", cfg.Flags, len(cfg.Policy), cfg.FFDeny)
		if cfg.Flags&mailbox.RemoteFlagFullBodies != 0 {
			log.Logf(rep, log.Info, "enemy host: full bodies on: heap gate %v (game largest 0x%X free 0x%X zelda largest 0x%X archive largest 0x%X), full_max %d, flags2 0x%04X",
				cfg.HeapGate, cfg.HeapSizes.GameLargest, cfg.HeapSizes.GameFree, cfg.HeapSizes.ZeldaLargest, cfg.HeapSizes.ArchiveLargest, cfg.FullMax, cfg.Flags2)
		}
	}
	remote := NewRemoteHost(d, rep, cfg, epoch)
	remote.Kinds = host.Kinds
	roster := posefeed.RosterFor(name, dolphinPID)
	defer func() {
		if err := remote.Shutdown(); err != nil {
			log.Logf(rep, log.Warn, "enemy host: clearing the remote-Link block: %v", err)
		}
	}()
	applier.Attrib = remote.AttribFlags
	hits := make(chan *Hit, 64)
	client := network.NewEnemyClient(name, network.RoleEnemyHost)
	client.OnLog = func(msg string) { rep.Log(log.Net, msg) }
	client.OnEnemy = func(data []byte) {
		sub, err := Sub(data)
		if err != nil {
			return
		}
		switch sub {
		case SubHit:
			if h, err := DecodeHit(data); err == nil {
				remote.NoteHit(h, time.Now())
				select {
				case hits <- h:
				default:
				}
			}
		case SubPState:
			if p, err := DecodePState(data); err == nil {
				remote.OnPState(p, time.Now())
			}
		case SubPAck:
			if a, err := DecodePAck(data); err == nil {
				remote.OnPAck(a)
			}
		}
	}
	if connLog == nil {
		connLog = network.NewConnectLog(addr, func(s string) { rep.Log(log.Warn, s) }, func(s string) { rep.Log(log.OK, s) })
	}
	var gate hostGate
	session := func(ctx context.Context, send func([]byte) error) error {
		var last *State
		t := time.NewTicker(Tick)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-t.C:
			}
			now := time.Now()
			relOK := gate.check(d, now)
			applier.SetREL(relOK)
			// The remote players' positions order the host's candidates
			// (with the host Link's own): those drawn here as a live
			// mini-Link, the ones in this stage (the roster carries no room).
			views, viewsAt := roster.Snapshot()
			var players [][3]float32
			if !viewsAt.IsZero() && now.Sub(viewsAt) <= RosterStale {
				for _, v := range views {
					if v.LinkSlot >= 0 && v.Live {
						players = append(players, v.Pos)
					}
				}
			}
			host.SetPlayers(players)
			st, err := host.Tick(now)
			if err != nil {
				log.Logf(rep, log.Warn, "enemy host: %v", err)
				continue
			}
			if st != nil {
				last = st
				b, err := EncodeState(st)
				if err != nil {
					log.Logf(rep, log.Warn, "enemy host: %v", err)
					continue
				}
				if err := send(b); err != nil {
					return fmt.Errorf("send STATE: %w", err)
				}
			}
			for drained := false; !drained; {
				select {
				case h := <-hits:
					if ack := applier.Offer(h, last, host.Listing(), now); ack != nil {
						send(EncodeAck(ack))
					} else {
						host.NoteEngaged(h.Handle, now) // a hit that landed and was queued
					}
				default:
					drained = true
				}
			}
			ack, err := applier.Poll(last, now)
			if err != nil {
				log.Logf(rep, log.Warn, "enemy host: %v", err)
			} else if ack != nil {
				send(EncodeAck(ack))
			}
			if err := applier.Keepalive(host.Scope(), now); err != nil {
				log.Logf(rep, log.Warn, "enemy host: keepalive: %v", err)
			}
			phits, err := remote.Tick(now, host.Scope(), relOK, views, viewsAt, host.Listing())
			if err != nil {
				log.Logf(rep, log.Warn, "enemy remote: %v", err)
			}
			for _, p := range phits {
				host.NoteEngaged(p.SetID, now) // the attacker's handle
				if b, err := EncodePHit(p); err == nil {
					send(b)
				}
			}
		}
	}
	return network.RunEnemy(ctx, client, addr, connLog, session)
}

// hostGate is the host's contract and REL gate, checked every
// ContractEvery: the hit applier writes the block only when both hold.
type hostGate struct {
	next time.Time
	ok   bool
	why  string
}

func (g *hostGate) check(m *emu.Dolphin, now time.Time) bool {
	if now.Before(g.next) {
		return g.ok
	}
	g.next = now.Add(ContractEvery)
	c, err := wwapi.CheckContract(m, wwapi.ContractOptions{})
	if err != nil {
		return g.ok
	}
	g.ok = c.WritesAllowed && c.REL.Linked && c.REL.BuildID == inject.RELBuildID
	return g.ok
}

// ClientPipeline runs the mirror on Dolphin dolphinPID against the server
// at addr as name until ctx ends, reconnecting after a drop. The mirror's
// state (epoch, smoothers) survives reconnects; the block is cleared when
// the pipeline ends.
func ClientPipeline(ctx context.Context, env Env, name, addr string, dolphinPID uint32, rep log.Reporter, connLog *network.ConnectLog) error {
	log.Logf(rep, log.Info, "=== Enemy client: %s <- %s ===", name, addr)
	d, err := env.Open(dolphinPID)
	if err != nil {
		return err
	}
	defer d.Close()
	rep.Log(log.OK, "Dolphin found.")
	brk := write.NewBreaker(write.BreakerConfig{Logf: func(format string, args ...any) { log.Logf(rep, log.Warn, format, args...) }})
	mirror := NewMirror(d, rep, brk, Raw(), time.Now())
	mirror.Claims, mirror.CoopSwitchSync = env.Claims, env.CoopSwitchSync
	log.Logf(rep, log.Info, "enemy client: mirroring %s (WW_ENEMY_MIRROR_KINDS), quiet deaths for %s (WW_ENEMY_QUIET_DEATH), selecting by %s (WW_ENEMY_SELECT)", mirror.Kinds, mirror.Quiet, mirror.Select)
	defer func() {
		if err := mirror.Shutdown(); err != nil {
			log.Logf(rep, log.Warn, "enemy client: clearing the block: %v", err)
		}
	}()
	// The remote-Link side: PSTATE always, the WWRP block only under
	// WW_ENEMY_REMOTE (players.go).
	cfg := RemoteConfigFromEnv(rep)
	if cfg.On {
		log.Logf(rep, log.Info, "enemy client: remote-Link block on, flags 0x%04X", cfg.Flags)
	}
	mirror.Remote = NewRemoteClient(d, rep, brk, cfg, time.Now())
	mirror.Remote.Owner = mirror.OwnerOf
	defer func() {
		if err := mirror.Remote.Shutdown(); err != nil {
			log.Logf(rep, log.Warn, "enemy client: clearing the remote-Link block: %v", err)
		}
	}()
	return RunMirror(ctx, mirror, name, addr, rep, connLog)
}

// RunMirror keeps mirror fed from a RoleEnemyClient connection to addr
// and ticks it every Tick until ctx ends.
func RunMirror(ctx context.Context, mirror *Mirror, name, addr string, rep log.Reporter, connLog *network.ConnectLog) error {
	client := network.NewEnemyClient(name, network.RoleEnemyClient)
	client.OnLog = func(msg string) { rep.Log(log.Net, msg) }
	client.OnEnemy = func(data []byte) { mirror.OnEnemy(data, time.Now()) }
	if connLog == nil {
		connLog = network.NewConnectLog(addr, func(s string) { rep.Log(log.Warn, s) }, func(s string) { rep.Log(log.OK, s) })
	}
	session := func(ctx context.Context, send func([]byte) error) error {
		for {
			now := time.Now()
			hits, sleep, err := mirror.Tick(now)
			if err != nil {
				if errors.Is(err, write.ErrOff) {
					rep.Log(log.Err, "enemy client: "+err.Error())
					<-ctx.Done()
					return nil
				}
				log.Logf(rep, log.Warn, "enemy client: %v", err)
				sleep = GateSleep
			}
			for _, h := range hits {
				b, err := EncodeHit(h)
				if err == nil {
					send(b)
				}
			}
			if mirror.Remote != nil && err == nil {
				if scope, serr := ReadScope(mirror.m); serr == nil {
					out, rerr := mirror.Remote.Tick(now, scope, mirror.RemoteGate())
					if rerr != nil {
						log.Logf(rep, log.Warn, "enemy remote: %v", rerr)
					}
					for _, b := range out {
						send(b)
					}
				}
			}
			if sleep == 0 {
				sleep = Tick
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(sleep):
			}
		}
	}
	return network.RunEnemy(ctx, client, addr, connLog, session)
}

// BlockAddr is the block's address, for probes.
const BlockAddr = mailbox.EnemyBlockAddr
