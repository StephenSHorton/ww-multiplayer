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
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/write"
)

// The two pipelines, in feed.PuppetSync's shape (Env, Open, a client,
// ReconnectLoop): HostPipeline samples this Dolphin's enemies (the kinds
// WW_ENEMY_MIRROR_KINDS enables, DefaultMirrorKinds by default) and streams
// them on a RoleEnemyHost connection; ClientPipeline receives them on a
// RoleEnemyClient connection and mirrors them into this Dolphin. Both are
// on unless WW_ENEMY_SYNC=0 (Enabled) in the engine and mp-local.

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

// Enabled reports enemy sync on: the default, off only with
// WW_ENEMY_SYNC=0. It is the one switch for the pipelines and the claim
// sets alike (engine, mp-local), so the two never disagree.
func Enabled() bool { return os.Getenv("WW_ENEMY_SYNC") != "0" }

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
	defer func() {
		if err := applier.Shutdown(); err != nil {
			log.Logf(rep, log.Warn, "enemy host: clearing the block: %v", err)
		}
	}()
	// The remote-Link side (players.go): the WWRP block unless
	// WW_ENEMY_REMOTE turns it off; the remote players' mini-Links come
	// from this session's pose feed (posefeed.RosterFor: PuppetSync runs
	// under the same name on the same Dolphin).
	cfg := RemoteConfigFromEnv(rep)
	log.Logf(rep, log.Info, "enemy host: host-authoritative enemies on (kinds %s), remote Link bodies %s (WW_ENEMY_SYNC=0 turns enemy sync off); selecting by %s (WW_ENEMY_SELECT), STATE 30 Hz gate %v", host.Kinds, remoteStepLabel(cfg), host.Select, host.State30Hz)
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
	loop := newHostLoop(d, rep, host, applier, remote, roster.Snapshot, hits)
	session := func(ctx context.Context, send func([]byte) error) error {
		loop.last = nil // a new connection's ACKs answer this connection's STATEs
		t := time.NewTicker(Tick)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-t.C:
			}
			if err := loop.step(time.Now(), send); err != nil {
				return err
			}
		}
	}
	return network.RunEnemy(ctx, client, addr, connLog, session)
}

// hostLoop is one enemy host's tick (HostPipeline's session runs step
// every Tick): sample, send the STATE, apply the clients' hits, keep the
// block alive, run the remote-Link side. Each site that can fail on every
// tick logs through a repeatWarn: a Dolphin that died or stopped answering
// fails host.Tick 60 times a second, and that is one "paused" Warn, a
// count every RepeatEvery and one "resumed" OK at the next good tick,
// never a line per tick. The loop pauses rather than ending the enemy
// session: a stall that clears (a hitch, a debugger, a Dolphin paused)
// resumes with nothing restarted, and a Dolphin that is gone for good is
// a Dolphin change, which the app's GameRunner answers by running the
// pipelines again (docs/05 "Shared enemies").
type hostLoop struct {
	rep     log.Reporter
	m       mem.Reader // the gate's reads
	host    *Host
	applier *HitApplier
	remote  *RemoteHost
	roster  func() ([]posefeed.RemoteView, time.Time)
	hits    <-chan *Hit
	gate    hostGate
	last    *State // the last STATE sent on this connection

	tickWarn, encodeWarn, pollWarn, keepWarn, remoteWarn *repeatWarn
}

func newHostLoop(m mem.Reader, rep log.Reporter, host *Host, applier *HitApplier, remote *RemoteHost, roster func() ([]posefeed.RemoteView, time.Time), hits <-chan *Hit) *hostLoop {
	return &hostLoop{
		rep: rep, m: m, host: host, applier: applier, remote: remote, roster: roster, hits: hits,
		gate:       hostGate{rep: rep},
		tickWarn:   pausedWarn(rep, "enemy host", "no enemy STATE and no hits"),
		encodeWarn: plainWarn(rep, "enemy host"),
		pollWarn:   plainWarn(rep, "enemy host"),
		keepWarn:   plainWarn(rep, "enemy host: keepalive"),
		remoteWarn: plainWarn(rep, "enemy remote"),
	}
}

// step runs one tick at now; an error is send's (the connection is gone).
func (l *hostLoop) step(now time.Time, send func([]byte) error) error {
	relOK := l.gate.check(l.m, now)
	l.applier.SetREL(relOK)
	// The remote players' positions order the host's candidates (with the
	// host Link's own): those drawn here as a live mini-Link, the ones in
	// this stage (the roster carries no room).
	var views []posefeed.RemoteView
	var viewsAt time.Time
	if l.roster != nil {
		views, viewsAt = l.roster()
	}
	var players [][3]float32
	if !viewsAt.IsZero() && now.Sub(viewsAt) <= RosterStale {
		for _, v := range views {
			if v.LinkSlot >= 0 && v.Live {
				players = append(players, v.Pos)
			}
		}
	}
	l.host.SetPlayers(players)
	st, err := l.host.Tick(now)
	l.tickWarn.Note(err, now)
	if err != nil {
		return nil
	}
	if st != nil {
		l.last = st
		b, err := EncodeState(st)
		l.encodeWarn.Note(err, now)
		if err != nil {
			return nil
		}
		if err := send(b); err != nil {
			return fmt.Errorf("send STATE: %w", err)
		}
	}
	for drained := false; !drained; {
		select {
		case h := <-l.hits:
			if ack := l.applier.Offer(h, l.last, l.host.Listing(), now); ack != nil {
				send(EncodeAck(ack))
			} else {
				l.host.NoteEngaged(h.Handle, now) // a hit that landed and was queued
			}
		default:
			drained = true
		}
	}
	// The hits held over the host Link's demo: applied once it ends, or
	// dropped (aged out, the target gone).
	for _, ack := range l.applier.ReleaseHeld(l.last, l.host.Listing(), now) {
		send(EncodeAck(ack))
	}
	ack, err := l.applier.Poll(l.last, now)
	l.pollWarn.Note(err, now)
	if err == nil && ack != nil {
		send(EncodeAck(ack))
	}
	l.keepWarn.Note(l.applier.Keepalive(l.host.Scope(), now), now)
	phits, err := l.remote.Tick(now, l.host.Scope(), relOK, views, viewsAt, l.host.Listing())
	l.remoteWarn.Note(err, now)
	for _, p := range phits {
		l.host.NoteEngaged(p.SetID, now) // the attacker's handle
		if b, err := EncodePHit(p); err == nil {
			send(b)
		}
	}
	return nil
}

// hostGate is the host's contract and REL gate, checked every
// ContractEvery: the hit applier writes the block only when both hold.
// Each change of verdict is logged once through rep (nil: silent), in the
// client's checkREL words: a Warn with the reason while hits wait, an OK
// when the REL is linked.
type hostGate struct {
	rep  log.Reporter
	next time.Time
	ran  bool
	ok   bool
	why  string
}

func (g *hostGate) check(m mem.Reader, now time.Time) bool {
	if now.Before(g.next) {
		return g.ok
	}
	g.next = now.Add(ContractEvery)
	c, err := wwapi.CheckContract(m, wwapi.ContractOptions{})
	if err != nil {
		return g.ok
	}
	why := ""
	switch {
	case !c.WritesAllowed:
		why = "this Dolphin's game is not this version's patch (" + c.Why + ")"
	case !c.REL.Linked:
		why = "the REL module is not linked"
	case c.REL.BuildID != inject.RELBuildID:
		why = fmt.Sprintf("the linked REL module is build %08X, this build's is %08X", c.REL.BuildID, inject.RELBuildID)
	}
	ok := why == ""
	if g.rep != nil && (!g.ran || ok != g.ok || why != g.why) {
		if ok {
			log.Logf(g.rep, log.OK, "enemy host: REL module linked (build %08X); host hits apply", c.REL.BuildID)
		} else {
			g.rep.Log(log.Warn, "enemy host: "+why+"; host hits wait")
		}
	}
	g.ran, g.ok, g.why = true, ok, why
	return g.ok
}

// remoteStepLabel names cfg's remote-Link flags for the start-of-session
// lines: "off", the step preset they equal ("step6"), or the raw flags.
func remoteStepLabel(cfg RemoteConfig) string {
	if !cfg.On {
		return "off"
	}
	var steps [len(remoteSteps)]uint16
	var acc uint16
	for k, f := range remoteSteps {
		acc |= f
		steps[k] = acc
	}
	for k := len(steps) - 1; k >= 0; k-- {
		if cfg.Flags == steps[k] {
			return fmt.Sprintf("step%d", k)
		}
	}
	return fmt.Sprintf("flags 0x%04X", cfg.Flags)
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
	defer func() {
		if err := mirror.Shutdown(); err != nil {
			log.Logf(rep, log.Warn, "enemy client: clearing the block: %v", err)
		}
	}()
	// The remote-Link side: PSTATE always, the WWRP block unless
	// WW_ENEMY_REMOTE turns it off (players.go).
	cfg := RemoteConfigFromEnv(rep)
	log.Logf(rep, log.Info, "enemy client: mirroring the host's enemies (kinds %s), remote bodies %s; quiet deaths for %s (WW_ENEMY_QUIET_DEATH), selecting by %s (WW_ENEMY_SELECT)", mirror.Kinds, remoteStepLabel(cfg), mirror.Quiet, mirror.Select)
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
	// As the host's loop (hostLoop): a tick that fails on every tick (a
	// dead Dolphin, every GateSleep) is one paused Warn, a count every
	// RepeatEvery and one resumed OK, never a line per tick.
	tickWarn := pausedWarn(rep, "enemy client", "no copies moved and no hits sent")
	remoteWarn := plainWarn(rep, "enemy remote")
	session := func(ctx context.Context, send func([]byte) error) error {
		for {
			now := time.Now()
			hits, sleep, err := mirror.Tick(now)
			if err != nil && errors.Is(err, write.ErrOff) {
				rep.Log(log.Err, "enemy client: "+err.Error())
				<-ctx.Done()
				return nil
			}
			tickWarn.Note(err, now)
			if err != nil {
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
					remoteWarn.Note(rerr, now)
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
