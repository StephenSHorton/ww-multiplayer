package replica

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/derive"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/internal/network"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
)

// startM4Host is a host whose hub grants the default features over tb:
// its stage tables' identity and switch verdict.
func startM4Host(t *testing.T, tb *tables.Tables) *host {
	t.Helper()
	id, err := tb.Identity()
	if err != nil {
		t.Fatal(err)
	}
	st, err := world.Create(t.TempDir(), world.New(world.ID(testWorld), time.Unix(1_800_000_000, 0)), world.Options{})
	if err != nil {
		t.Fatal(err)
	}
	h, err := hub.New(hub.Config{Store: st, Features: hub.DefaultFeatures, StageTblHash: id, SyncedSwitch: memorySwitches(tb)})
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

// m4Want is what both games and the world hold once the M4 loopback has
// converged.
type m4Want struct {
	story    int // image offset of A's story bit
	storyBit byte
	reg      int // B's MAX register, raised to its class mask
}

// lacks lists what p's game lacks of w: A's story bit and B's register in
// the saved dSv_event_c, the bow, its quiver and the Deku Leaf with their
// slots, DRC's two key chests and its door, and the derived values: a key
// (two chests less one door, on both the formula and mKeyNum), max life
// 12 + 2 pieces and max magic 16, with no count left pending.
func (w m4Want) lacks(p *rig) []string {
	var out []string
	check := func(ok bool, what string, args ...any) {
		if !ok {
			out = append(out, fmt.Sprintf(what, args...))
		}
	}
	rm := class.Of(w.reg).Mask
	check(p.savedByte(w.story)&w.storyBit != 0, "the story bit")
	check(p.savedByte(w.reg)&rm == rm, "the register (0x%02X)", p.savedByte(w.reg))
	check(p.savedByte(addr.OffGetItem+save.SlotBow)&0x01 != 0, "the bow's flag")
	check(p.savedByte(addr.OffItems+save.SlotBow) == save.ItemBow, "the bow's slot")
	check(p.savedByte(addr.OffArrowMax) == 30, "the quiver (%d)", p.savedByte(addr.OffArrowMax))
	check(p.savedByte(addr.OffGetItem+save.SlotDekuLeaf)&0x01 != 0, "the Deku Leaf's flag")
	check(p.savedByte(addr.OffItems+save.SlotDekuLeaf) == save.ItemDekuLeaf, "the Deku Leaf's slot")
	if p.table() != addr.TableDRC {
		return append(out, "DRC is not loaded")
	}
	for _, no := range []int{1, 2} {
		off, m := chest(addr.TableDRC, no)
		check(p.liveByte(off)&m != 0, "DRC's key chest %d", no)
	}
	off, m := switchImageByte(addr.TableDRC, 0x10)
	check(p.liveByte(off)&m != 0, "DRC's door 0x10")
	check(p.keyFormula() == 1 && p.read(addr.LiveKeyNum, 1)[0] == 1, "one key (formula %d, mKeyNum %d)", p.keyFormula(), p.read(addr.LiveKeyNum, 1)[0])
	check(int(p.u16(addr.MaxLife)) == derive.StartLife+2, "max life %d", p.u16(addr.MaxLife))
	check(p.read(addr.MaxMagic, 1)[0] == derive.MagicLeaf, "max magic %d", p.read(addr.MaxMagic, 1)[0])
	for _, c := range []uint32{addr.ItemKeyNumCount, addr.ItemMaxLifeCount, addr.ItemMaxMagicCount} {
		check(p.u16(c) == 0, "the counter at 0x%08X still holds %d", c, int16(p.u16(c)))
	}
	return out
}

// TestConvergeM4Loopback is M4's acceptance test: two writing replicas on
// their own fake games, with the M1 tables of the same disc, the real hub
// granting every default feature behind a loopback network.Server. A
// sets a story bit, opens two of the sea's heart-piece chests, then in
// Dragon Roost Cavern takes the first key and spends it on door 0x10; B
// gets the bow, its quiver and the Deku Leaf, raises an event register,
// then takes DRC's second key. Both then move between DRC's rooms and
// reload the stage now and then, as play would. Every M4 class
// converges: the story bit and the register (each written as a stage
// change starts), the inventory, and the derived keys, max life and max
// magic, which match their formulas over each game's own flags. The
// write audit holds for both: no PER_PLAYER, NEVER or TAG write, no
// write while its gate was closed, no clear, story writes only on a
// transition tick, and the DERIVED bytes and counters only through the
// derive path.
func TestConvergeM4Loopback(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a loopback server for a few seconds")
	}
	tb := m4Tables(t)
	h := startM4Host(t, tb)
	id, err := tb.Identity()
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{StageTblHash: id, Policy: m4Policy(tb), Tables: tb}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // before the cleanups, which wait for the clients
	a := newWritingPlayer(t, ctx, h, cfg, "Link", bind.Nonce{1}, 5)
	b := newWritingPlayer(t, ctx, h, cfg, "Tetra", bind.Nonce{2}, 6)
	players := []*m3Player{a, b}
	logs := func() string { return fmt.Sprintf("A:\n%s\nB:\n%s", a.log, b.log) }
	run := func(n int) {
		for range n {
			for _, p := range players {
				p.step()
			}
			time.Sleep(time.Millisecond)
		}
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	const want = hub.M4Features | hub.FeatureSwitches
	deadline := time.Now().Add(10 * time.Second)
	for {
		run(1)
		ok := true
		for _, p := range players {
			st := p.r.Status()
			ok = ok && st.State == StateArmed && st.Features&want == want
		}
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not armed with M4's features: %+v / %+v\n%s", a.r.Status(), b.r.Status(), logs())
		}
	}

	w := m4Want{reg: registerOff(t)}
	w.story, w.storyBit = syncedStory(t)
	rm := class.Of(w.reg).Mask
	if b.savedByte(w.reg)&rm == rm {
		t.Fatalf("a new file's register 0x%03X already holds 0x%02X", w.reg, b.savedByte(w.reg))
	}
	a.set(w.story, w.storyBit)
	must(a.g.OpenPieceChest(0))
	must(a.g.OpenPieceChest(1))
	must(b.g.GetItem(save.SlotBow, 0, save.ItemBow))
	must(b.g.SetCapacity(addr.OffArrowMax, 30))
	must(b.g.GetDekuLeaf())
	b.put(w.reg, b.savedByte(w.reg)|rm)
	run(3 * gate.StableTicks)
	a.load(drcKeys)
	run(3 * gate.StableTicks)
	must(a.g.OpenKeyChest(1))
	run(3 * gate.StableTicks)
	must(a.g.UnlockKeyDoor(0x10))
	b.load(drcKeys)
	run(3 * gate.StableTicks)
	must(b.g.OpenKeyChest(2))

	deadline = time.Now().Add(30 * time.Second)
	for i := 0; ; i++ {
		for k, p := range players {
			switch (i + 23*k) % 60 {
			case 20:
				p.enter(2, 1)
			case 30:
				p.enter(1, 2)
			case 40:
				must(p.g.SetNextStage(drcKeys))
			case 44:
				must(p.g.ChangeStage())
			}
			p.step()
		}
		done := true
		for _, p := range players {
			done = done && len(w.lacks(p.rig)) == 0
		}
		if done {
			break
		}
		if time.Now().After(deadline) {
			for k, p := range players {
				t.Errorf("player %c lacks %s; %+v, off %q", 'A'+k, strings.Join(w.lacks(p.rig), ", "), p.r.Status().Writes, p.r.Status().WritesOff)
			}
			t.Fatalf("no convergence\n%s", logs())
		}
		time.Sleep(time.Millisecond)
	}

	wimg := h.store.Image()
	for _, c := range []struct {
		name string
		off  int
		m    byte
	}{
		{"A's story bit", w.story, w.storyBit},
		{"B's register", w.reg, rm},
		{"B's bow", addr.OffGetItem + save.SlotBow, 0x01},
		{"B's Deku Leaf", addr.OffGetItem + save.SlotDekuLeaf, 0x01},
	} {
		if wimg[c.off]&c.m != c.m {
			t.Errorf("the world lacks %s: 0x%02X", c.name, wimg[c.off])
		}
	}
	for k, p := range players {
		ws := p.bm.written()
		auditM4Writes(t, ws, p.reps, p.bm.placesSeen(), false, p.r.Status().Writes.Derived)
		g := NewGuardFor(M3Targets | M4Targets)
		n := map[Target]int{}
		for _, w := range ws {
			n[g.TargetOf(w.a)]++
		}
		st := p.r.Status()
		switch {
		case n[TargetStory] == 0 || n[TargetCounters] == 0 || n[TargetStage] == 0:
			t.Errorf("player %c wrote %v: want story, counters and stage progress", 'A'+k, n)
		case st.WritesOff != "" || st.Writes.Failed != 0 || st.Writes.Drained == 0:
			t.Errorf("player %c: off %q, %+v", 'A'+k, st.WritesOff, st.Writes)
		}
	}
	if n := map[Target]int{}; true {
		g := NewGuardFor(M3Targets | M4Targets)
		for _, w := range a.bm.written() {
			n[g.TargetOf(w.a)]++
		}
		if n[TargetInventory] == 0 || n[TargetDerived] == 0 {
			t.Errorf("A wrote %v: want B's inventory flags and slots", n)
		}
	}
	t.Logf("A: %+v; B: %+v", a.r.Status().Writes, b.r.Status().Writes)
}
