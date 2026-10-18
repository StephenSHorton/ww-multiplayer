package enemysync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/write"
)

// The two pipelines, in feed.PuppetSync's shape (Env, Open, a client,
// ReconnectLoop): HostPipeline samples this Dolphin's Moblins and streams
// them on a RoleEnemyHost connection; ClientPipeline receives them on a
// RoleEnemyClient connection and mirrors them into this Dolphin. Both are
// behind WW_ENEMY_SYNC=1 (Enabled) in the engine and mp-local.

// Env is what the pipelines take from the program that runs them.
type Env struct {
	// Open attaches to the Dolphin with this PID; 0 means the one the
	// environment selects (WW_DOLPHIN_INDEX / WW_DOLPHIN_PID).
	Open func(pid uint32) (*emu.Dolphin, error)
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
	defer func() {
		if err := applier.Shutdown(); err != nil {
			log.Logf(rep, log.Warn, "enemy host: clearing the block: %v", err)
		}
	}()
	hits := make(chan *Hit, 64)
	client := network.NewEnemyClient(name, network.RoleEnemyHost)
	client.OnLog = func(msg string) { rep.Log(log.Net, msg) }
	client.OnEnemy = func(data []byte) {
		if h, err := DecodeHit(data); err == nil {
			select {
			case hits <- h:
			default:
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
			applier.SetREL(gate.check(d, now))
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
					if ack := applier.Offer(h, last, host.Moblins(), now); ack != nil {
						send(EncodeAck(ack))
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
	defer func() {
		if err := mirror.Shutdown(); err != nil {
			log.Logf(rep, log.Warn, "enemy client: clearing the block: %v", err)
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
