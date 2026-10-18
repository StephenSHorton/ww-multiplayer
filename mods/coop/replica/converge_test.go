package replica

import (
	"bytes"
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
)

// host is the host side of a loopback session: a world store, the real
// CoopHub and a network.Server on a loopback port.
type host struct {
	store *world.Store
	hub   *hub.Hub
	addr  string
}

func startHost(t *testing.T) *host {
	t.Helper()
	st, err := world.Create(t.TempDir(), world.New(world.ID(testWorld), time.Unix(1_800_000_000, 0)), world.Options{})
	if err != nil {
		t.Fatal(err)
	}
	h, err := hub.New(hub.Config{Store: st, Features: hub.DefaultFeatures})
	if err != nil {
		t.Fatal(err)
	}
	srv := network.NewServer(0)
	srv.Coop = h
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		srv.Stop()
		if err := h.Stop(); err != nil {
			t.Errorf("hub stop: %v", err)
		}
	})
	return &host{store: st, hub: h, addr: fmt.Sprintf("127.0.0.1:%d", srv.Addr().(*net.TCPAddr).Port)}
}

// unruly sits between a replica and its network client and makes the
// connection misbehave the ways the protocol must survive: inbound
// payloads are held and handed over in a random order, some twice, and
// some outbound GAINS, WORLD and PRESENCE are sent twice.
type unruly struct {
	mu    sync.Mutex
	rng   *rand.Rand
	r     *Replica
	inbox [][]byte
}

// onCoop is the client's OnCoop: it holds the payload.
func (u *unruly) onCoop(p []byte) {
	u.mu.Lock()
	u.inbox = append(u.inbox, bytes.Clone(p))
	u.mu.Unlock()
}

// deliver hands the held payloads to the replica, shuffled, some twice.
func (u *unruly) deliver() {
	u.mu.Lock()
	in := u.inbox
	u.inbox = nil
	u.rng.Shuffle(len(in), func(i, j int) { in[i], in[j] = in[j], in[i] })
	var twice []bool
	for range in {
		twice = append(twice, u.rng.IntN(5) == 0)
	}
	u.mu.Unlock()
	for i, p := range in {
		u.r.Handle(p)
		if twice[i] {
			u.r.Handle(p)
		}
	}
}

// session is network.RunCoop's session: what the last connection left
// unread goes with it, as a closed socket's would.
func (u *unruly) session(ctx context.Context, send func([]byte) error) error {
	u.mu.Lock()
	u.inbox = nil
	u.mu.Unlock()
	dup := func(p []byte) error {
		if err := send(p); err != nil {
			return err
		}
		u.mu.Lock()
		again := u.rng.IntN(4) == 0
		u.mu.Unlock()
		switch wire.Sub(p[0]) {
		case wire.SubGains, wire.SubWorld, wire.SubPresence:
			if again {
				return send(p)
			}
		}
		return nil
	}
	return u.r.Session(ctx, dup)
}

// player is one side of the loopback session: a game, its replica and its
// co-op connection.
type player struct {
	*rig
	r      *Replica
	u      *unruly
	client *network.Client
	log    *logBuf
}

func newPlayer(t *testing.T, ctx context.Context, h *host, name string, nonce bind.Nonce, seed uint64) *player {
	t.Helper()
	p := &player{rig: newRig(t, name), log: &logBuf{}}
	p.tag(p.tagFor(testWorld, nonce))
	r, err := New(Config{Mem: p.m, Logf: p.log.logf})
	if err != nil {
		t.Fatal(err)
	}
	p.r = r
	p.u = &unruly{rng: rand.New(rand.NewPCG(seed, 99)), r: r}
	p.client = network.NewCoopClient(name, 0)
	p.client.OnCoop = p.u.onCoop
	done := make(chan struct{})
	go func() {
		defer close(done)
		network.RunCoop(ctx, p.client, h.addr, nil, p.u.session)
	}()
	t.Cleanup(func() { <-done })
	return p
}

func (p *player) step() {
	p.g.Frame()
	p.u.deliver()
	p.r.Tick()
}

// view is the replica's view of the world.
func (p *player) view() (save.Image, uint64) {
	p.r.mu.Lock()
	defer p.r.mu.Unlock()
	return p.r.view, p.r.viewSeq
}

// shareable lists the image bytes the default policy shares, OR ones and
// MAX ones apart.
func shareable() (or, mx []int) {
	pol := world.DefaultPolicy()
	for off := range addr.SaveImageSize {
		if pol[off] == 0 {
			continue
		}
		if class.Of(off).Class == class.Max {
			mx = append(mx, off)
		} else {
			or = append(or, off)
		}
	}
	return or, mx
}

// play does one random thing in p's game: progress (a bit or a raised
// MAX value somewhere in the image) or a transition.
func (p *player) play(rng *rand.Rand, or, mx []int) {
	stages := []fakegame.Stage{outset, drc, drcBoss, woods}
	switch k := rng.IntN(20); {
	case k < 12:
		off := or[rng.IntN(len(or))]
		m := world.SharedMask(off)
		for {
			b := byte(1) << rng.IntN(8)
			if m&b != 0 {
				p.set(off, b)
				return
			}
		}
	case k < 14:
		off := mx[rng.IntN(len(mx))]
		m := world.SharedMask(off)
		cur := p.read(p.byteAddr(off), 1)[0] & m
		if v := byte(rng.IntN(int(m) + 1)); v&^m == 0 && v > cur {
			p.put(off, v)
		}
	case k < 17:
		p.enter(rng.IntN(addr.RoomCount), rng.IntN(addr.RoomCount))
	default:
		p.load(stages[rng.IntN(len(stages))])
	}
}

// TestConvergeLoopback is M2's acceptance test: two dry-run replicas on
// their own fake games, each bound to the host's world, with the real hub
// behind a loopback network.Server. Both games make progress at random
// and move between stages and rooms while the connections deliver out of
// order, repeat messages and drop (reconnects). Afterwards, every gain of
// either game is in the world, each replica's view is the world, and each
// replica's planned image (its game plus the writes it would make) is the
// world: the two would converge once M3 applies the plans.
func TestConvergeLoopback(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a loopback server for a few seconds")
	}
	h := startHost(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // before the cleanups, which wait for the clients
	a := newPlayer(t, ctx, h, "Link", bind.Nonce{1}, 1)
	b := newPlayer(t, ctx, h, "Tetra", bind.Nonce{2}, 2)
	players := []*player{a, b}

	rng := rand.New(rand.NewPCG(2026, 10))
	or, mx := shareable()
	waitArmed := func(why string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			armed := true
			for _, p := range players {
				p.step()
				armed = armed && p.r.Status().State == StateArmed
			}
			if armed {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: not armed: %+v / %+v\nA:\n%s\nB:\n%s", why, a.r.Status(), b.r.Status(), a.log, b.log)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitArmed("start")

	const steps = 300
	for i := range steps {
		for _, p := range players {
			if rng.IntN(3) == 0 {
				p.play(rng, or, mx)
			}
			p.step()
		}
		switch i {
		case steps / 3:
			a.client.Disconnect()
		case 2 * steps / 3:
			b.client.Disconnect()
			a.client.Disconnect()
		}
		time.Sleep(time.Millisecond)
	}

	// Quiesce: no more play; wait until both are armed again and the
	// world, the views and the plans stop moving and agree.
	waitArmed("after the reconnects")
	deadline := time.Now().Add(15 * time.Second)
	for {
		for _, p := range players {
			p.step()
		}
		if converged(t, h, players, false) {
			break
		}
		if time.Now().After(deadline) {
			converged(t, h, players, true)
			t.Fatalf("no convergence\nA:\n%s\nB:\n%s", a.log, b.log)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// The run did what it claims: both dropped and came back, both
	// would apply the other's progress, and the hub saw repeats.
	for i, p := range players {
		if n := p.log.count("welcome to world"); n < 2 || !p.log.has("armed offline") {
			t.Errorf("player %d: %d welcomes, offline %v: the reconnect did not happen", i, n, p.log.has("armed offline"))
		}
		if st := p.r.Status(); len(st.Planned) == 0 {
			t.Errorf("player %d plans nothing: the partner's progress never reached it", i)
		}
	}
	hs, err := h.hub.Status()
	if err != nil {
		t.Fatal(err)
	}
	dups := 0
	for _, ps := range hs.Peers {
		dups += ps.Duplicates
	}
	t.Logf("world seq %d; %d repeated GAINS batches dropped since the last reconnects", hs.Seq, dups)
}

// converged reports whether the world holds every shared bit of both
// games, and each replica's view and planned image are the world. With
// report it says what differs.
func converged(t *testing.T, h *host, players []*player, report bool) bool {
	t.Helper()
	pol := world.DefaultPolicy()
	within := func(img save.Image) save.Image {
		s := world.Shared(&img)
		for off := range s {
			if class.Of(off).Class == class.Max && s[off]&^pol[off] != 0 {
				s[off] = 0
			} else {
				s[off] &= pol[off]
			}
		}
		return s
	}
	wimg := h.store.Image()
	var join save.Image
	for _, p := range players {
		img := p.image()
		local := within(img)
		join = world.Join(&join, &local)
	}
	ok := true
	fail := func(format string, args ...any) {
		ok = false
		if report {
			t.Errorf(format, args...)
		}
	}
	if gs := world.Diff(&wimg, &join); len(gs) > 0 {
		fail("the world lacks %d bytes of the games' progress: %s", len(gs), fmtGains(gs))
	}
	for i, p := range players {
		view, seq := p.view()
		if view != wimg {
			fail("player %d: view (seq %d) is not the world (seq %d): world lacks %s, view lacks %s", i, seq, h.store.Seq(),
				fmtGains(world.Diff(&wimg, &view)), fmtGains(world.Diff(&view, &wimg)))
		}
		img := p.image()
		local := world.Shared(&img)
		st := p.r.Status()
		planned := Planned(&local, st.Planned)
		planned = within(planned)
		if planned != wimg {
			fail("player %d: planned image is not the world: world lacks %s, plan lacks %s (%d writes planned)", i,
				fmtGains(world.Diff(&wimg, &planned)), fmtGains(world.Diff(&planned, &wimg)), len(st.Planned))
		}
		if st.State != StateArmed || st.Queued != 0 {
			fail("player %d: %s (%s), %d queued", i, st.State, st.Why, st.Queued)
		}
	}
	return ok
}
