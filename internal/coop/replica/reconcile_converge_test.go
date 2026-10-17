package replica

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
)

// m3TblHash is the stage tables' identity the M3 loopback's host and
// players share, so the hub grants switch sync (hub.TableFeatures).
const m3TblHash = 0x6D33_7461_626C_6573

// m3Synced is the loopback's M1 verdict: switch 0x05 of the sea and DRC
// tables latches, and so does switch 0x05 of TotG and ET, the hand-off
// switches (m3HandOff).
func m3Synced(t, no int) bool {
	switch t {
	case addr.TableSea, addr.TableDRC, addr.TableTotG, addr.TableET:
		return no == 0x05
	}
	return false
}

// m3HandOff is the switch each loopback player sets alone, after the
// random play: A's in TotG, B's in ET. No stage the loopback loads uses
// either table, so the partner always writes it, to the saved slot. The
// random play alone can't show that each side writes a switch: both games
// set both of its switches themselves (B sets the DRC one two ticks after
// A does), so whether the partner's bit is written first depends on how
// fast the loopback delivers it.
var m3HandOff = [2]int{addr.TableTotG, addr.TableET}

func startM3Host(t *testing.T) *host {
	t.Helper()
	st, err := world.Create(t.TempDir(), world.New(world.ID(testWorld), time.Unix(1_800_000_000, 0)), world.Options{})
	if err != nil {
		t.Fatal(err)
	}
	h, err := hub.New(hub.Config{Store: st, Features: hub.DefaultFeatures, StageTblHash: m3TblHash, SyncedSwitch: m3Synced})
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

// m3Player is a loopback player whose replica writes (Config.Apply).
type m3Player struct {
	*player
	bm   *beatMem
	reps []Report
}

func newM3Player(t *testing.T, ctx context.Context, h *host, pol *world.Policy, name string, nonce bind.Nonce, seed uint64) *m3Player {
	t.Helper()
	return newWritingPlayer(t, ctx, h, Config{StageTblHash: m3TblHash, Policy: pol}, name, nonce, seed)
}

// newWritingPlayer is a loopback player whose replica writes, with cfg's
// policy, tables and stage tables' identity.
func newWritingPlayer(t *testing.T, ctx context.Context, h *host, cfg Config, name string, nonce bind.Nonce, seed uint64) *m3Player {
	t.Helper()
	p := &player{rig: newRig(t, name), log: &logBuf{}}
	p.tag(p.tagFor(testWorld, nonce))
	bm := &beatMem{FakeMem: p.m, game: p.g}
	cfg.Mem, cfg.Logf, cfg.Apply, cfg.BeatTimeout = bm, p.log.logf, true, time.Second
	r, err := New(cfg)
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
	return &m3Player{player: p, bm: bm}
}

func (p *m3Player) step() {
	p.bm.frame()
	p.u.deliver()
	p.bm.setTick(len(p.reps))
	p.reps = append(p.reps, p.r.Tick())
}

// m3Bytes lists the image bytes M3 writes that pol shares.
func m3Bytes(pol *world.Policy) []int {
	var out []int
	for off := range addr.SaveImageSize {
		if m3Category(CategoryOf(off)) && pol[off]&world.SharedMask(off) != 0 {
			out = append(out, off)
		}
	}
	return out
}

// m3HandOffByte is the image byte of player i's hand-off switch.
func m3HandOffByte(i int) int {
	off, _ := switchImageByte(m3HandOff[i], 0x05)
	return off
}

// m3Image is img's M3 bytes within pol, everything else zero.
func m3Image(img *world.Image, pol *world.Policy) world.Image {
	var out world.Image
	for _, off := range m3Bytes(pol) {
		out[off] = img[off] & world.SharedMask(off) & pol[off]
	}
	return out
}

// writtenCategories counts ws's writes by the category of the image byte
// each wrote (a live memBit write by its table's).
func writtenCategories(ws []auditWrite) map[Category]int {
	n := map[Category]int{}
	for _, w := range ws {
		switch {
		case w.a >= addr.SaveImage && w.a < addr.SaveImage+addr.SaveImageSize:
			n[CategoryOf(int(w.a-addr.SaveImage))]++
		case w.a >= addr.LiveMemBit && w.a < addr.LiveMemBit+addr.MemBitSize:
			n[CategoryOf(addr.OffMemBit(int(w.table))+int(w.a-addr.LiveMemBit))]++
		}
	}
	return n
}

// play does one random thing in p's game: an M3 bit, set as the game
// would, or a room or stage change.
func (p *m3Player) play(rng *rand.Rand, offs []int, pol *world.Policy) {
	stages := []fakegame.Stage{outset, drc, drcBoss, woods}
	switch k := rng.IntN(20); {
	case k < 2:
		// A latching switch: of the loaded table, or set in another's
		// saved slot (the partner's game, as far as the world can tell).
		off, m := switchImageByte([]int{addr.TableSea, addr.TableDRC}[rng.IntN(2)], 0x05)
		p.set(off, m)
	case k < 12:
		off := offs[rng.IntN(len(offs))]
		m := world.SharedMask(off) & pol[off]
		for {
			if b := byte(1) << rng.IntN(8); m&b != 0 {
				p.set(off, b)
				return
			}
		}
	case k < 16:
		p.enter(rng.IntN(addr.RoomCount), rng.IntN(addr.RoomCount))
	default:
		p.load(stages[rng.IntN(len(stages))])
	}
}

// TestConvergeM3Loopback is M3's acceptance test: two writing replicas on
// their own fake games, bound to the host's world, the real hub (with
// switch sync) behind a loopback network.Server. Both games make M3
// progress at random and move between stages and rooms while the
// connections deliver out of order, repeat messages and drop. Then each
// sets a switch its partner never sets (m3HandOff). Afterwards (and a
// last room change, for what §6.3 defers) both games hold the world's M3
// bytes, each has written a switch, and the write audit holds: no byte outside M3's
// allowlist (so no PER_PLAYER, NEVER, DERIVED or TAG byte), no clear, no
// write while a gate was closed, no bit set twice, and the loaded table's
// switches only at a room change. "coop off" then stops a player's
// writes at once.
func TestConvergeM3Loopback(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a loopback server for a few seconds")
	}
	h := startM3Host(t)
	pol := hub.SwitchPolicy(nil, m3Synced)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // before the cleanups, which wait for the clients
	a := newM3Player(t, ctx, h, pol, "Link", bind.Nonce{1}, 3)
	b := newM3Player(t, ctx, h, pol, "Tetra", bind.Nonce{2}, 4)
	players := []*m3Player{a, b}

	rng := rand.New(rand.NewPCG(2026, 14))
	// The random play never sets a hand-off switch.
	var offs []int
	for _, off := range m3Bytes(pol) {
		if off != m3HandOffByte(0) && off != m3HandOffByte(1) {
			offs = append(offs, off)
		}
	}
	waitArmed := func(why string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			armed := true
			for _, p := range players {
				p.step()
				st := p.r.Status()
				armed = armed && st.State == StateArmed && st.Features&hub.FeatureSwitches != 0
			}
			if armed {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: not armed with switches: %+v / %+v\nA:\n%s\nB:\n%s", why, a.r.Status(), b.r.Status(), a.log, b.log)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitArmed("start")

	const steps = 300
	for i := range steps {
		for _, p := range players {
			if rng.IntN(3) == 0 {
				p.play(rng, offs, pol)
			}
			p.step()
		}
		if i == steps/2 {
			a.client.Disconnect()
		}
		time.Sleep(time.Millisecond)
	}

	// The hand-offs: each player sets one switch its partner never sets.
	for i, p := range players {
		off, m := switchImageByte(m3HandOff[i], 0x05)
		p.set(off, m)
	}

	// Quiesce: no more play but room changes, until both games hold the
	// world.
	waitArmed("after the reconnect")
	converged := func(report bool) bool {
		wimg := h.store.Image()
		want := m3Image(&wimg, pol)
		ok := true
		var join world.Image
		for i, p := range players {
			img := p.image()
			got := m3Image(&img, pol)
			join = world.Join(&join, &got)
			if got != want {
				ok = false
				if report {
					view, seq := p.view()
					v := m3Image(&view, pol)
					st := p.r.Status()
					t.Errorf("player %d lacks %s; the world lacks %s; its view (seq %d of %d) lacks %s; %d planned, %+v, off %q",
						i, fmtGains(world.Diff(&got, &want)), fmtGains(world.Diff(&want, &got)), seq, h.store.Seq(),
						fmtGains(world.Diff(&v, &want)), len(st.Planned), st.Writes, st.WritesOff)
				}
			}
		}
		if gs := world.Diff(&want, &join); len(gs) > 0 {
			ok = false
			if report {
				t.Errorf("the world lacks the games' %s", fmtGains(gs))
			}
		}
		return ok
	}
	deadline := time.Now().Add(15 * time.Second)
	for i := 0; ; i++ {
		for _, p := range players {
			if i%20 == 19 {
				p.enter((i/20)%addr.RoomCount, 0)
			}
			p.step()
		}
		if converged(false) {
			break
		}
		if time.Now().After(deadline) {
			converged(true)
			t.Fatalf("no convergence\nA:\n%s\nB:\n%s", a.log, b.log)
		}
		time.Sleep(time.Millisecond)
	}

	for i, p := range players {
		auditWrites(t, p.bm.written(), p.reps, p.bm.placesSeen(), false)
		if cats := writtenCategories(p.bm.written()); cats[CatStage] == 0 || cats[CatSwitch] == 0 || cats[CatOcean] == 0 {
			t.Errorf("player %d wrote %v: want stage progress, switches and the ocean", i, cats)
		}
		st := p.r.Status()
		if st.WritesOff != "" || st.Writes.Written == 0 {
			t.Errorf("player %d: writes off %q, %+v: the partner's progress was never written", i, st.WritesOff, st.Writes)
		}
		if st.Writes.Failed != 0 {
			t.Errorf("player %d: %d verify failures", i, st.Writes.Failed)
		}
	}

	// "coop off": A writes nothing more, while B's progress still
	// reaches A's view.
	a.r.TurnOff("coop off")
	n := len(a.bm.written())
	c, m := chest(addr.TableFW, 30)
	b.set(c, m)
	deadline = time.Now().Add(10 * time.Second)
	for {
		a.step()
		b.step()
		if v, _ := a.view(); v[c]&m != 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("B's chest never reached A's view")
		}
		time.Sleep(time.Millisecond)
	}
	for range 10 {
		a.step()
	}
	if got := len(a.bm.written()); got != n {
		t.Fatalf("A wrote %d bytes after coop off", got-n)
	}
	if img := a.image(); img[c]&m != 0 {
		t.Fatal("B's chest reached A's game after coop off")
	}
	t.Logf("A wrote %d bytes, B %d", a.r.Status().Writes.Written, b.r.Status().Writes.Written)
}
