package main

// wwdev stress: the server and the co-op hub under 4-8 synthetic players
// (issues #13 and #11, wind#81; docs/stress-test.md). No Dolphin.
//
// `stress-host` is a host's server with its co-op hub over a fresh world
// in a temporary directory, as the app runs it, and prints its own CPU,
// memory and relay counters every few seconds. `stress` starts one as a
// child process (so its numbers are the server's alone) unless --addr
// names one, and runs N players against it. Each player has the three
// connections a real one has:
//
//   - pose: broadcast-pose's, a 42-joint pose frame at 60 Hz;
//   - sync: puppet-sync's, a position with its place at 20 Hz, receiving
//     the others' poses (relay latency: each frame carries its send time);
//   - coop: the co-op pipeline's, bound to the world, with PRESENCE on
//     every stage hop, a KILL a second (relay latency to the players in
//     the same room) and chest CLAIMs (asks, renewals, releases, and now
//     and then the chest bit as a GAIN).
//
// Players hop stage and room every 20-60 s. A connection that drops is
// counted and dialled again. The report (--out) holds every number.

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"os/signal"
	"runtime"

	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// The stress world and tables: any fixed values the host and the
// players share.
var stressWorld = world.ID{0x57, 0x57, 0x53, 0x54, 0x52, 0x45, 0x53, 0x53}

const stressTables uint64 = 0x5354_5245_5353_0001

// stressStages are the stages players hop between; a stage's save table
// is its index plus the 5-minute epoch (so the kill bits never run out).
var stressStages = []string{"sea", "M_NewD2", "Asoko", "kindan"}

const (
	stressJoints   = 42
	stressPoseHz   = 60
	stressPosHz    = 20
	stressRooms    = 4
	stressEpoch    = 5 * time.Minute
	stressHostTick = 5 * time.Second
)

// ---- the host ----------------------------------------------------------------

// hostSample is one line of stress-host's stdout (after its "addr" line).
type hostSample struct {
	T          float64            `json:"t"`
	CPUSeconds float64            `json:"cpu_s"`
	RSSMaxMB   float64            `json:"rss_max_mb"`
	HeapMB     float64            `json:"heap_mb"`
	SysMB      float64            `json:"sys_mb"`
	Goroutines int                `json:"goroutines"`
	Players    int                `json:"players"`
	Scope      network.ScopeStats `json:"scope"`
	Hub        *hubSample         `json:"hub,omitempty"`
}

type hubSample struct {
	Peers int `json:"peers"`
}

func cmdStressHost(_ string, args []string) {
	fs := flag.NewFlagSet("stress-host", flag.ExitOnError)
	port := fs.Int("port", 0, "port (0: any free one, loopback only)")
	fs.Parse(args)

	dir, err := os.MkdirTemp("", "ww-stress-world-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "stress-host:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)
	st, err := world.Create(dir, world.New(stressWorld, time.Now()), world.Options{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "stress-host:", err)
		os.Exit(1)
	}
	yes := func(int, string, int, int) bool { return true }
	h, err := hub.New(hub.Config{
		Store: st, StageTblHash: stressTables, Features: hub.DefaultFeatures,
		Policy: world.FullPolicy(), SyncedScoped: yes, SyncedKill: yes,
		Logf: func(f string, a ...any) { fmt.Fprintf(os.Stderr, "hub: "+f+"\n", a...) },
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "stress-host:", err)
		os.Exit(1)
	}
	srv := network.NewServer(*port)
	srv.Coop = h
	srv.OnLog = func(msg string) { fmt.Fprintln(os.Stderr, "server:", msg) }
	if err := srv.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "stress-host:", err)
		os.Exit(1)
	}
	fmt.Printf("addr %s\n", srv.Addr())

	// Stop on a signal or when stdin closes (the parent going away; the
	// one way that works the same on Windows).
	done := make(chan struct{})
	go func() { io.Copy(io.Discard, os.Stdin); close(done) }()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	start := time.Now()
	enc := json.NewEncoder(os.Stdout)
	tick := time.NewTicker(stressHostTick)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			cpu, rss := processUsage()
			s := hostSample{
				T: time.Since(start).Seconds(), CPUSeconds: cpu, RSSMaxMB: rss,
				HeapMB: mb(ms.HeapInuse), SysMB: mb(ms.Sys), Goroutines: runtime.NumGoroutine(),
				Players: srv.PlayerCount(), Scope: srv.ScopeStats(),
			}
			if hs, err := h.Status(); err == nil {
				s.Hub = &hubSample{Peers: len(hs.Peers)}
			}
			enc.Encode(s)
		case <-done:
			srv.Stop()
			h.Stop()
			return
		case <-sig:
			srv.Stop()
			h.Stop()
			return
		}
	}
}

func mb(b uint64) float64 { return math.Round(float64(b)/(1<<20)*10) / 10 }

// ---- latency histograms -----------------------------------------------------

// hist is a latency histogram in 0.1 ms buckets up to 2 s (the last
// bucket takes everything above).
type hist struct {
	mu sync.Mutex
	b  [20001]uint32
	n  uint64
	mx float64
}

func (h *hist) add(d time.Duration) {
	ms := float64(d) / float64(time.Millisecond)
	i := int(ms * 10)
	if i < 0 {
		i = 0
	}
	if i >= len(h.b) {
		i = len(h.b) - 1
	}
	h.mu.Lock()
	h.b[i]++
	h.n++
	if ms > h.mx {
		h.mx = ms
	}
	h.mu.Unlock()
}

type summary struct {
	N   uint64  `json:"n"`
	P50 float64 `json:"p50_ms"`
	P90 float64 `json:"p90_ms"`
	P99 float64 `json:"p99_ms"`
	Max float64 `json:"max_ms"`
}

func (h *hist) summary() summary {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := summary{N: h.n, Max: math.Round(h.mx*10) / 10}
	if h.n == 0 {
		return s
	}
	q := func(p float64) float64 {
		want := uint64(math.Ceil(p * float64(h.n)))
		var c uint64
		for i, v := range h.b {
			c += uint64(v)
			if c >= want {
				return math.Min(float64(i+1)/10, s.Max)
			}
		}
		return s.Max
	}
	s.P50, s.P90, s.P99 = q(0.50), q(0.90), q(0.99)
	return s
}

// reset returns the summary and empties the histogram (the per-interval
// progress lines).
func (h *hist) reset() summary {
	s := h.summary()
	h.mu.Lock()
	h.b = [20001]uint32{}
	h.n, h.mx = 0, 0
	h.mu.Unlock()
	return s
}

// ---- the run ----------------------------------------------------------------

type stressRun struct {
	start    time.Time
	players  int
	coop     bool
	addr     string
	poseLat  hist // pose relay: sender's SendPose to a receiver's read
	killLat  hist // KILL relay: sender's SendCoop to a same-room peer's read
	claimRTT hist // CLAIM ask to its verdict
	posRTT   hist // the player connections' heartbeat RTT
	window   struct{ pose, kill, claim hist }

	// kills: "stage/table/room/word/bit" -> send time.
	killsMu sync.Mutex
	kills   map[string]time.Time

	c counters
}

type counters struct {
	PosesSent, PosesRecv, PositionsSent  atomic.Int64
	BytesUp, BytesDownPose               atomic.Int64
	KillsSent, KillsRelayed, KillsReplay atomic.Int64
	KillsSkipped                         atomic.Int64
	ClaimAsks, ClaimGrant, ClaimDeny     atomic.Int64
	ClaimConsumed, ChestsOpened          atomic.Int64
	Hops, Chats, Notices                 atomic.Int64
	Drops, Reconnects, SendErrors        atomic.Int64
	CoopRefused                          atomic.Int64
}

type stressPlayer struct {
	run  *stressRun
	i    int
	name string
	rng  *rand.Rand

	mu        sync.Mutex
	stage     int
	table     int8
	room      int8
	enteredAt time.Time
	killNext  map[string]int // per room key: the next of this player's bits

	pose, sync, coop *network.Client
	welcomes         chan *wire.CoWelcome

	claimNo    int // -1: none
	claimAsked time.Time
	claimSince time.Time
	claimWait  bool
	batch      uint32
}

func cmdStress(_ string, args []string) {
	fs := flag.NewFlagSet("stress", flag.ExitOnError)
	n := fs.Int("players", 4, "synthetic players (each: pose, sync and co-op connections)")
	dur := fs.Duration("duration", 30*time.Minute, "how long to run")
	addrFlag := fs.String("addr", "", "a running server to load instead of a stress-host child (no co-op unless it hosts one)")
	out := fs.String("out", "", "write the JSON report here")
	every := fs.Duration("every", 30*time.Second, "progress line interval")
	noCoop := fs.Bool("no-coop", false, "player connections only")
	fs.Parse(args)
	if *n < 1 || *n > 60 {
		fmt.Fprintln(os.Stderr, "stress: --players 1..60")
		os.Exit(2)
	}

	r := &stressRun{start: time.Now(), players: *n, coop: !*noCoop, addr: *addrFlag, kills: map[string]time.Time{}}
	var host *exec.Cmd
	var hostIn io.WriteCloser
	var samples []hostSample
	var samplesMu sync.Mutex
	var hostLog strings.Builder
	var hostLogMu sync.Mutex
	hostExited := make(chan error, 1)
	if r.addr == "" {
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintln(os.Stderr, "stress:", err)
			os.Exit(1)
		}
		host = exec.Command(exe, "stress-host")
		hostIn, _ = host.StdinPipe()
		stdout, _ := host.StdoutPipe()
		stderr, _ := host.StderrPipe()
		if err := host.Start(); err != nil {
			fmt.Fprintln(os.Stderr, "stress: starting the host:", err)
			os.Exit(1)
		}
		go func() {
			sc := bufio.NewScanner(stderr)
			for sc.Scan() {
				hostLogMu.Lock()
				if hostLog.Len() < 4<<20 {
					hostLog.WriteString(sc.Text() + "\n")
				}
				hostLogMu.Unlock()
			}
		}()
		sc := bufio.NewScanner(stdout)
		if !sc.Scan() || !strings.HasPrefix(sc.Text(), "addr ") {
			fmt.Fprintln(os.Stderr, "stress: the host printed no address")
			host.Process.Kill()
			os.Exit(1)
		}
		r.addr = strings.TrimPrefix(sc.Text(), "addr ")
		go func() {
			for sc.Scan() {
				var s hostSample
				if json.Unmarshal(sc.Bytes(), &s) == nil {
					samplesMu.Lock()
					samples = append(samples, s)
					samplesMu.Unlock()
				}
			}
		}()
		go func() { hostExited <- host.Wait() }()
		fmt.Fprintf(os.Stderr, "stress: host pid %d on %s\n", host.Process.Pid, r.addr)
	}

	players := make([]*stressPlayer, *n)
	for i := range players {
		players[i] = newPlayer(r, i)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for _, p := range players {
		wg.Add(1)
		go func() { defer wg.Done(); p.runLoop(stop) }()
		time.Sleep(100 * time.Millisecond)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	end := time.After(*dur)
	tick := time.NewTicker(*every)
	var hostDied error
	hostGone := false
loop:
	for {
		select {
		case <-tick.C:
			samplesMu.Lock()
			var last *hostSample
			if len(samples) > 0 {
				last = &samples[len(samples)-1]
			}
			samplesMu.Unlock()
			pl, kl, cl := r.window.pose.reset(), r.window.kill.reset(), r.window.claim.reset()
			line := fmt.Sprintf("%5.0fs pose p50 %.1f p99 %.1f max %.1f (n %d) | kill p99 %.1f (n %d) | claim p99 %.1f (n %d) | drops %d",
				time.Since(r.start).Seconds(), pl.P50, pl.P99, pl.Max, pl.N, kl.P99, kl.N, cl.P99, cl.N, r.c.Drops.Load())
			if last != nil {
				line += fmt.Sprintf(" | host cpu %.1fs heap %.1f MB rss-max %.1f MB gor %d players %d", last.CPUSeconds, last.HeapMB, last.RSSMaxMB, last.Goroutines, last.Players)
			}
			fmt.Fprintln(os.Stderr, line)
		case <-end:
			break loop
		case <-sig:
			break loop
		case err := <-hostExited:
			hostDied, hostGone = err, true
			fmt.Fprintf(os.Stderr, "stress: the host exited early: %v\n", err)
			break loop
		}
	}
	close(stop)
	wg.Wait()
	if host != nil && !hostGone {
		hostIn.Close()
		select {
		case hostDied = <-hostExited:
		case <-time.After(10 * time.Second):
			host.Process.Kill()
			hostDied = fmt.Errorf("killed: did not stop within 10 s")
		}
	}

	hostLogMu.Lock()
	logText := hostLog.String()
	hostLogMu.Unlock()
	samplesMu.Lock()
	rep := r.report(*n, *dur, samples, logText, hostDied, host != nil)
	samplesMu.Unlock()
	b, _ := json.MarshalIndent(rep, "", "  ")
	if *out != "" {
		if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "stress:", err)
		}
	}
	fmt.Println(string(b))
	if rep.Panics > 0 || rep.Races > 0 || (host != nil && hostDied != nil) {
		os.Exit(1)
	}
}

type stressReport struct {
	Players        int              `json:"players"`
	Connections    int              `json:"connections"`
	Seconds        float64          `json:"seconds"`
	Coop           bool             `json:"coop"`
	PoseRelay      summary          `json:"pose_relay"`
	KillRelay      summary          `json:"kill_relay"`
	ClaimRTT       summary          `json:"claim_rtt"`
	HeartbeatRTT   summary          `json:"heartbeat_rtt"`
	Counts         map[string]int64 `json:"counts"`
	UpKBsPerPlayer float64          `json:"up_kb_s_per_player"`
	DownKBsPose    float64          `json:"down_pose_kb_s_per_player"`
	Host           *hostReport      `json:"host,omitempty"`
	Panics         int              `json:"panics"`
	Races          int              `json:"data_races"` // a -race build's host
	HostExit       string           `json:"host_exit,omitempty"`
	HostLogLines   int              `json:"host_log_lines"`
	HostLogSample  []string         `json:"host_log_sample,omitempty"`
}

type hostReport struct {
	CPUCores      float64            `json:"cpu_cores_avg"`
	CPUSeconds    float64            `json:"cpu_s"`
	HeapMBFirst   float64            `json:"heap_mb_first"`
	HeapMBLast    float64            `json:"heap_mb_last"`
	HeapMBMax     float64            `json:"heap_mb_max"`
	RSSMaxMB      float64            `json:"rss_max_mb"`
	GoroutinesMax int                `json:"goroutines_max"`
	GoroutinesEnd int                `json:"goroutines_last"`
	Scope         network.ScopeStats `json:"scope"`
	Samples       int                `json:"samples"`
}

func (r *stressRun) report(n int, dur time.Duration, samples []hostSample, logText string, hostErr error, child bool) stressReport {
	secs := time.Since(r.start).Seconds()
	c := &r.c
	rep := stressReport{
		Players: n, Seconds: math.Round(secs), Coop: r.coop,
		PoseRelay: r.poseLat.summary(), KillRelay: r.killLat.summary(),
		ClaimRTT: r.claimRTT.summary(), HeartbeatRTT: r.posRTT.summary(),
		Counts: map[string]int64{
			"poses_sent": c.PosesSent.Load(), "poses_received": c.PosesRecv.Load(), "positions_sent": c.PositionsSent.Load(),
			"kills_sent": c.KillsSent.Load(), "kills_relayed": c.KillsRelayed.Load(), "kills_replayed_on_entry": c.KillsReplay.Load(),
			"kills_skipped_no_bits": c.KillsSkipped.Load(),
			"claim_asks":            c.ClaimAsks.Load(), "claim_grants": c.ClaimGrant.Load(), "claim_denies": c.ClaimDeny.Load(),
			"claim_consumed": c.ClaimConsumed.Load(), "chests_opened": c.ChestsOpened.Load(),
			"hops": c.Hops.Load(), "chats": c.Chats.Load(), "notices": c.Notices.Load(), "coop_refused": c.CoopRefused.Load(),
			"drops": c.Drops.Load(), "reconnects": c.Reconnects.Load(), "send_errors": c.SendErrors.Load(),
		},
		UpKBsPerPlayer: math.Round(float64(c.BytesUp.Load())/secs/float64(n)/1024*10) / 10,
		DownKBsPose:    math.Round(float64(c.BytesDownPose.Load())/secs/float64(n)/1024*10) / 10,
	}
	rep.Connections = n * 2
	if r.coop {
		rep.Connections = n * 3
	}
	if len(samples) > 0 {
		first, last := samples[0], samples[len(samples)-1]
		h := &hostReport{CPUSeconds: last.CPUSeconds, HeapMBFirst: first.HeapMB, HeapMBLast: last.HeapMB,
			RSSMaxMB: last.RSSMaxMB, GoroutinesEnd: last.Goroutines, Scope: last.Scope, Samples: len(samples)}
		if last.T > 0 {
			h.CPUCores = math.Round(last.CPUSeconds/last.T*1000) / 1000
		}
		for _, s := range samples {
			h.HeapMBMax = max(h.HeapMBMax, s.HeapMB)
			h.GoroutinesMax = max(h.GoroutinesMax, s.Goroutines)
		}
		rep.Host = h
	}
	lines := strings.Split(strings.TrimSpace(logText), "\n")
	if logText == "" {
		lines = nil
	}
	rep.HostLogLines = len(lines)
	seen := map[string]bool{}
	for _, l := range lines {
		if strings.Contains(l, "panic") || strings.HasPrefix(l, "goroutine ") {
			rep.Panics++
		}
		if strings.Contains(l, "WARNING: DATA RACE") {
			rep.Races++
		}
		// A sample of the distinct kinds of log line (digits dropped).
		k := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return -1
			}
			return r
		}, l)
		if len(k) > 60 {
			k = k[:60]
		}
		if !seen[k] && len(rep.HostLogSample) < 30 {
			seen[k] = true
			rep.HostLogSample = append(rep.HostLogSample, l)
		}
	}
	if child && hostErr != nil {
		rep.HostExit = hostErr.Error()
	}
	return rep
}

// ---- a player -----------------------------------------------------------------

func newPlayer(r *stressRun, i int) *stressPlayer {
	p := &stressPlayer{run: r, i: i, name: fmt.Sprintf("stress-%d", i+1),
		rng: rand.New(rand.NewPCG(uint64(i)+1, 0x5747)), killNext: map[string]int{}, claimNo: -1,
		welcomes: make(chan *wire.CoWelcome, 4)}
	p.hopLocked(time.Now())
	return p
}

func (p *stressPlayer) place() network.Place {
	p.mu.Lock()
	defer p.mu.Unlock()
	return network.Place{Stage: stressStages[p.stage], Room: p.room, InPlay: true}
}

// hopLocked moves the player to a random stage and room (p.mu held or
// not yet shared).
func (p *stressPlayer) hopLocked(now time.Time) {
	p.stage = p.rng.IntN(len(stressStages))
	epoch := int(now.Sub(p.run.start) / stressEpoch)
	p.table = int8((p.stage*4 + epoch) % 16)
	p.room = int8(p.rng.IntN(stressRooms))
	p.enteredAt = now
}

func (p *stressPlayer) connectPlayer(pose bool) *network.Client {
	c := network.NewClient(p.name)
	if !pose {
		c.OnPose = p.onPose
		c.OnChat = func(string, string) {}
	}
	c.OnLog = func(string) {}
	for {
		if err := c.Connect(p.run.addr); err == nil {
			return c
		}
		time.Sleep(time.Second)
	}
}

func (p *stressPlayer) onPose(_ byte, m []byte) {
	if len(m) < 8 {
		return
	}
	sent := int64(binary.BigEndian.Uint64(m))
	d := time.Duration(time.Now().UnixNano() - sent)
	p.run.poseLat.add(d)
	p.run.window.pose.add(d)
	p.run.c.PosesRecv.Add(1)
	p.run.c.BytesDownPose.Add(int64(len(m) + 6))
}

func (p *stressPlayer) connectCoop() *network.Client {
	for {
		c := network.NewCoopClient(p.name, 0)
		c.OnLog = func(string) {}
		c.OnCoop = p.onCoop
		if err := c.Connect(p.run.addr); err != nil {
			time.Sleep(time.Second)
			continue
		}
		for len(p.welcomes) > 0 {
			<-p.welcomes
		}
		h := fnv.New32a()
		h.Write([]byte(p.name))
		hello := &wire.CoHello{Schema: wire.Schema, ClassHash: wire.ClassHash(), StageTblHash: stressTables,
			FileState: wire.FileBound, NameHash: h.Sum32(), Want: uint32(hub.DefaultFeatures),
			WorldID: wire.ID(stressWorld), BindNonce: wire.ID{0xAA, byte(p.i)}}
		if !p.sendCoop(c, hello) {
			c.Disconnect()
			continue
		}
		select {
		case w := <-p.welcomes:
			if w.Status != wire.StatusOK {
				p.run.c.CoopRefused.Add(1)
				fmt.Fprintf(os.Stderr, "%s: co-op welcome %+v\n", p.name, w)
				c.Disconnect()
				time.Sleep(5 * time.Second)
				continue
			}
		case <-time.After(5 * time.Second):
			c.Disconnect()
			continue
		}
		p.sendCoop(c, &wire.World{WorldID: wire.ID(stressWorld)})
		p.sendPresence(c)
		return c
	}
}

func (p *stressPlayer) sendCoop(c *network.Client, m wire.Msg) bool {
	b, err := wire.Encode(m)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: encoding %s: %v\n", p.name, m.Sub(), err)
		return false
	}
	if c.SendCoop(b) != nil {
		p.run.c.SendErrors.Add(1)
		return false
	}
	p.run.c.BytesUp.Add(int64(len(b) + 3))
	return true
}

func (p *stressPlayer) sendPresence(c *network.Client) {
	p.mu.Lock()
	st, _ := wire.MakeStage(stressStages[p.stage])
	m := &wire.Presence{Stage: st, Table: p.table, Room: p.room, Layer: -1,
		Flags: wire.PresenceInGame | wire.PresenceArmed}
	p.mu.Unlock()
	p.sendCoop(c, m)
}

func killKey(stage string, table, room int8, word uint8, bit int) string {
	return fmt.Sprintf("%s/%d/%d/%d/%d", stage, table, room, word, bit)
}

func (p *stressPlayer) onCoop(b []byte) {
	msg, err := wire.Decode(b, wire.Down)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: undecodable co-op message: %v\n", p.name, err)
		return
	}
	now := time.Now()
	switch m := msg.(type) {
	case *wire.CoWelcome:
		select {
		case p.welcomes <- m:
		default:
		}
	case *wire.Scoped:
		if m.Kind != wire.ScopeKill {
			return
		}
		p.mu.Lock()
		entered := p.enteredAt
		p.mu.Unlock()
		for _, w := range m.Words {
			for bit := 0; bit < 32; bit++ {
				if w.Bits&(1<<bit) == 0 {
					continue
				}
				k := killKey(m.Stage.String(), m.Table, m.Room, w.Word, bit)
				p.run.killsMu.Lock()
				sent, ok := p.run.kills[k]
				p.run.killsMu.Unlock()
				switch {
				case !ok:
				case sent.Before(entered):
					p.run.c.KillsReplay.Add(1) // the room's stored kills, sent on entry
				default:
					p.run.c.KillsRelayed.Add(1)
					p.run.killLat.add(now.Sub(sent))
					p.run.window.kill.add(now.Sub(sent))
				}
			}
		}
	case *wire.Claim:
		p.mu.Lock()
		if p.claimWait && int(m.No) == p.claimNo {
			p.claimWait = false
			p.run.claimRTT.add(now.Sub(p.claimAsked))
			p.run.window.claim.add(now.Sub(p.claimAsked))
		}
		p.mu.Unlock()
		switch m.Op {
		case wire.ClaimGrant:
			p.run.c.ClaimGrant.Add(1)
		case wire.ClaimDeny:
			p.run.c.ClaimDeny.Add(1)
		case wire.ClaimConsumed:
			p.run.c.ClaimConsumed.Add(1)
		}
	case *wire.Notice:
		if p.run.c.Notices.Add(1) <= 20 {
			fmt.Fprintf(os.Stderr, "%s: NOTICE %s: %s\n", p.name, m.Code, m.Text)
		}
	}
}

// runLoop drives the player until stop closes.
func (p *stressPlayer) runLoop(stop chan struct{}) {
	p.pose = p.connectPlayer(true)
	p.sync = p.connectPlayer(false)
	if p.run.coop {
		p.coop = p.connectCoop()
	}
	defer func() {
		p.pose.Disconnect()
		p.sync.Disconnect()
		if p.coop != nil {
			p.coop.Disconnect()
		}
	}()

	mat := make([]byte, stressJoints*48)
	face := make([]byte, 17)
	poseT := time.NewTicker(time.Second / stressPoseHz)
	posT := time.NewTicker(time.Second / stressPosHz)
	secT := time.NewTicker(time.Second)
	defer poseT.Stop()
	defer posT.Stop()
	defer secT.Stop()
	nextHop := time.Now().Add(time.Duration(20+p.rng.IntN(40)) * time.Second)
	nextChat := time.Now().Add(time.Duration(10+p.rng.IntN(30)) * time.Second)
	angle := 0.0
	for {
		select {
		case <-stop:
			return
		case <-poseT.C:
			binary.BigEndian.PutUint64(mat, uint64(time.Now().UnixNano()))
			if err := p.pose.SendPose(stressJoints, mat, face); err != nil {
				p.run.c.SendErrors.Add(1)
			} else {
				p.run.c.PosesSent.Add(1)
				p.run.c.BytesUp.Add(int64(len(mat) + len(face) + 5))
			}
		case <-posT.C:
			angle += 0.05
			pos := &network.PlayerPosition{PosX: float32(300 * math.Cos(angle)), PosY: 200,
				PosZ: float32(300 * math.Sin(angle)), RotY: int16(angle * 10430)}
			if err := p.sync.SendPositionAt(pos, p.place()); err != nil {
				p.run.c.SendErrors.Add(1)
			} else {
				p.run.c.PositionsSent.Add(1)
				p.run.c.BytesUp.Add(40)
			}
		case now := <-secT.C:
			p.checkLinks()
			if rtt := p.sync.LastRTT(); rtt > 0 {
				p.run.posRTT.add(rtt)
			}
			if now.After(nextHop) {
				nextHop = now.Add(time.Duration(20+p.rng.IntN(40)) * time.Second)
				p.hop(now)
			}
			if now.After(nextChat) {
				nextChat = now.Add(30 * time.Second)
				if p.sync.SendChat(fmt.Sprintf("%s at %s", p.name, now.Format("15:04:05"))) == nil {
					p.run.c.Chats.Add(1)
				}
			}
			if p.coop != nil {
				p.kill(now)
				p.chest(now)
			}
		}
	}
}

// checkLinks dials again any connection that dropped.
func (p *stressPlayer) checkLinks() {
	if !p.pose.IsConnected() {
		p.run.c.Drops.Add(1)
		p.pose.Disconnect()
		p.pose = p.connectPlayer(true)
		p.run.c.Reconnects.Add(1)
	}
	if !p.sync.IsConnected() {
		p.run.c.Drops.Add(1)
		p.sync.Disconnect()
		p.sync = p.connectPlayer(false)
		p.run.c.Reconnects.Add(1)
	}
	if p.coop != nil && !p.coop.IsConnected() {
		p.run.c.Drops.Add(1)
		p.coop.Disconnect()
		p.mu.Lock()
		p.claimNo, p.claimWait = -1, false
		p.mu.Unlock()
		p.coop = p.connectCoop()
		p.run.c.Reconnects.Add(1)
	}
}

func (p *stressPlayer) hop(now time.Time) {
	p.releaseChest()
	p.mu.Lock()
	p.hopLocked(now)
	p.mu.Unlock()
	p.run.c.Hops.Add(1)
	if p.coop != nil {
		p.sendPresence(p.coop)
	}
}

// kill sends one KILL bit of this player's share of the room's 512
// (bits i, i+n, ... for n players; skipped once they run out).
func (p *stressPlayer) kill(now time.Time) {
	p.mu.Lock()
	stage, table, room := stressStages[p.stage], p.table, p.room
	rk := fmt.Sprintf("%s/%d/%d", stage, table, room)
	k := p.killNext[rk]
	p.killNext[rk] = k + 1
	p.mu.Unlock()
	setID := p.i + k*p.run.players
	if setID >= 512 {
		p.run.c.KillsSkipped.Add(1)
		return
	}
	word, bit := uint8(setID/32), setID%32
	st, _ := wire.MakeStage(stage)
	p.run.killsMu.Lock()
	p.run.kills[killKey(stage, table, room, word, bit)] = now
	p.run.killsMu.Unlock()
	if p.sendCoop(p.coop, &wire.Scoped{Kind: wire.ScopeKill, Stage: st, Table: table, Room: room,
		Words: []wire.ScopedWord{{Word: word, Bits: 1 << bit}}}) {
		p.run.c.KillsSent.Add(1)
	}
}

// chest runs the chest claims: ask for a chest (one of 6 per table, so
// players in a room contend), renew at 1 Hz, and after 3 s either open it
// (its GAIN, a quarter of the time) or release it.
func (p *stressPlayer) chest(now time.Time) {
	p.mu.Lock()
	no, table, room := p.claimNo, p.table, p.room
	since := p.claimSince
	p.mu.Unlock()
	if no < 0 {
		if p.rng.Float64() >= 0.3 {
			return
		}
		no = p.rng.IntN(6)
		p.mu.Lock()
		p.claimNo, p.claimSince = no, now
		p.mu.Unlock()
		since = now
	}
	if now.Sub(since) >= 3*time.Second {
		if p.rng.Float64() < 0.25 {
			off := addr.OffMemBit(int(table)) + addr.MemBitOffTbox + 3 - no/8
			p.mu.Lock()
			p.batch++
			b := p.batch
			p.mu.Unlock()
			if p.sendCoop(p.coop, &wire.GainsUp{Batch: b, Gains: []wire.Gain{{Off: uint16(off), Val: 1 << (no % 8)}}}) {
				p.run.c.ChestsOpened.Add(1)
			}
		}
		p.releaseChest()
		return
	}
	p.mu.Lock()
	if !p.claimWait {
		p.claimWait, p.claimAsked = true, now
	}
	p.mu.Unlock()
	if p.sendCoop(p.coop, &wire.Claim{Op: wire.ClaimAsk, Table: table, No: uint8(no), Room: room}) {
		p.run.c.ClaimAsks.Add(1)
	}
}

func (p *stressPlayer) releaseChest() {
	p.mu.Lock()
	no, table, room := p.claimNo, p.table, p.room
	p.claimNo, p.claimWait = -1, false
	p.mu.Unlock()
	if no >= 0 && p.coop != nil {
		p.sendCoop(p.coop, &wire.Claim{Op: wire.ClaimRelease, Table: table, No: uint8(no), Room: room})
	}
}
