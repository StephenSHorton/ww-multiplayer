package replica

import (
	"context"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/story"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// batchTicks is how many ticks of the tick clock make story.BatchIdle.
const batchTicks = int(story.BatchIdle / TickEvery)

// storyIDs are n story bits that sync, one per event byte from the
// first, none of them held (class.Holds).
func storyIDs(n int) []uint16 {
	held := map[uint16]bool{}
	for _, h := range class.Holds() {
		held[h.ID] = true
	}
	var ids []uint16
	for i := 0; i < addr.EventBitBytes && len(ids) < n; i++ {
		m := class.SyncMaskOf(i)
		for b := 7; b >= 0; b-- {
			if id := uint16(i)<<8 | 1<<b; m&(1<<b) != 0 && !held[id] {
				ids = append(ids, id)
				break
			}
		}
	}
	return ids
}

// evGain is event bit id as a gain.
func evGain(id uint16) world.Gain {
	off, m := eventBit(id)
	return world.Gain{Off: uint16(off), Val: m}
}

// pacedRig is a differRig whose Differ is paced, under the story policy
// over every other class-mask bit (memory switches included).
func pacedRig(t *testing.T) *differRig {
	dr := newDifferRig(t, story.Policy(world.FullPolicy()))
	dr.d.Pace(nil)
	if e := dr.tick(); !e.Rebased {
		t.Fatalf("first tick: %+v, want the baseline", e)
	}
	return dr
}

// TestDifferPaced walks a paced Differ through the sender's rules: story
// bits in a batch released after an IDLE second, a switch coupled to a
// batch, inventory waiting for IDLE, and stage progress at once.
func TestDifferPaced(t *testing.T) {
	ids := storyIDs(4)
	a, b, c, d := evGain(ids[0]), evGain(ids[1]), evGain(ids[2]), evGain(ids[3])
	chestOff, chestMask := chest(addr.TableSea, 3)
	chestG := world.Gain{Off: uint16(chestOff), Val: chestMask}
	itemG := world.Gain{Off: uint16(addr.OffGetItem + 3), Val: 0x04}
	walletG := world.Gain{Off: uint16(addr.OffWalletSize), Val: 2}
	_, sb, err := addr.SwitchBit(0x05)
	if err != nil {
		t.Fatal(err)
	}
	swG := world.Gain{Off: uint16(addr.OffMemBit(addr.TableSea) + sb.Off), Val: sb.Mask}
	figOff := addr.OffEvent + addr.EventByte(0x80FF).Off
	figG := world.Gain{Off: uint16(figOff), Val: 0x10}
	letter := addr.EventByte(0x7A03)
	letterG := world.Gain{Off: uint16(addr.OffEvent + letter.Off), Val: 2}
	localOnly := evGain(class.LocalOnly()[0].ID)

	dr := pacedRig(t)
	// expect runs n ticks, the last of which must release want; the
	// others nothing.
	expect := func(step string, n int, want ...world.Gain) {
		t.Helper()
		want = sortGains(want)
		for i := 1; i <= n; i++ {
			e := dr.tick()
			switch {
			case i < n && len(e.Gains) > 0:
				t.Fatalf("%s: tick %d of %d released %s early", step, i, n, fmtGains(e.Gains))
			case i == n && !slices.Equal(e.Gains, want):
				t.Fatalf("%s: tick %d released %s, want %s (waiting %s)", step, i, fmtGains(e.Gains), fmtGains(want), fmtGains(e.Waiting))
			}
		}
	}

	// A story bit waits for an IDLE second with nothing new.
	dr.g.OnEventBit(ids[0])
	if e := dr.tick(); len(e.Gains) > 0 || !slices.Equal(e.Waiting, []world.Gain{a}) {
		t.Fatalf("story bit: gains %s, waiting %s", fmtGains(e.Gains), fmtGains(e.Waiting))
	}
	expect("one story bit", batchTicks, a)

	// A bit that rises while the batch waits puts it off.
	dr.g.OnEventBit(ids[1])
	dr.ticks(batchTicks - 1)
	dr.g.OnEventBit(ids[2])
	expect("a later bit", batchTicks+1, b, c)

	// An event: the chest goes at once; the story bit, the registers and
	// the switch wait for the batch, the inventory for IDLE.
	dr.g.SetEvent(1, 0)
	dr.tick()
	dr.g.OnEventBit(ids[3])
	dr.set(chestOff, chestMask)
	dr.set(int(itemG.Off), itemG.Val)
	dr.put(int(walletG.Off), walletG.Val)
	dr.onSwitch(0x05, int(outset.Room))
	dr.set(figOff, figG.Val)
	dr.put(int(letterG.Off), letterG.Val)
	dr.g.OnEventBit(class.LocalOnly()[0].ID)
	if e := dr.tick(); !slices.Equal(e.Gains, []world.Gain{chestG}) {
		t.Fatalf("in the event: gains %s, want the chest", fmtGains(e.Gains))
	}
	if e := dr.ticks(3 * batchTicks); len(e.Gains) > 0 {
		t.Fatalf("while the event runs: gains %s", fmtGains(e.Gains))
	}
	waiting := sortGains([]world.Gain{d, figG, letterG, itemG, walletG, swG})
	if got := dr.d.Waiting(); !slices.Equal(got, waiting) {
		t.Fatalf("waiting %s, want %s (no LocalOnly bit %s)", fmtGains(got), fmtGains(waiting), fmtGains([]world.Gain{localOnly}))
	}
	dr.g.SetEvent(0, 0)
	expect("the event ends: the inventory", 1, itemG, walletG)
	expect("the event's batch", batchTicks, d, figG, letterG, swG)
	if w := dr.d.Waiting(); len(w) > 0 {
		t.Fatalf("still waiting %s", fmtGains(w))
	}

	// A switch rising while IDLE with no batch waiting goes at once.
	dr.onSwitch(0x06, int(outset.Room))
	_, sb6, _ := addr.SwitchBit(0x06)
	if e := dr.tick(); !slices.Equal(e.Gains, []world.Gain{{Off: swG.Off, Val: sb6.Mask}}) {
		t.Fatalf("an IDLE switch: gains %s", fmtGains(e.Gains))
	}
	// An inventory flag rising while IDLE goes at once too.
	dr.set(int(itemG.Off), 0x08)
	if e := dr.tick(); !slices.Equal(e.Gains, []world.Gain{{Off: itemG.Off, Val: 0x08}}) {
		t.Fatalf("an IDLE item: gains %s", fmtGains(e.Gains))
	}

	// A pause menu opened before the batch is due puts it off until a
	// whole IDLE second has passed again.
	e5 := evGain(storyIDs(5)[4])
	dr.g.OnEventBit(storyIDs(5)[4])
	dr.ticks(batchTicks - 2)
	dr.g.SetPauseMenu(true)
	dr.ticks(2)
	dr.g.SetPauseMenu(false)
	expect("after the pause menu", batchTicks+1, e5)
}

// TestDifferPacedSkippedTicks: a tick the Differ skips (Link between
// stages) is not IDLE, so a batch waits for the new stage to be IDLE for
// a second.
func TestDifferPacedSkippedTicks(t *testing.T) {
	id := storyIDs(1)[0]
	dr := pacedRig(t)
	dr.g.OnEventBit(id)
	dr.ticks(batchTicks - 1)
	// A stage whose Link is not created yet (fakegame NoPlayer).
	dr.load(fakegame.Stage{Name: drc.Name, Table: drc.Table, KeyHUD: true, NoPlayer: true})
	for range 3 {
		if e := dr.tick(); len(e.Gains) > 0 || !slices.Equal(e.Waiting, []world.Gain{evGain(id)}) || e.Note != "between stages: skipped" {
			t.Fatalf("between stages: gains %s, waiting %s (note %q)", fmtGains(e.Gains), fmtGains(e.Waiting), e.Note)
		}
	}
	dr.load(drc)
	for n := 1; ; n++ {
		e := dr.tick()
		if len(e.Gains) == 0 {
			if n > batchTicks+1 {
				t.Fatalf("nothing released %d ticks into the new stage", n)
			}
			continue
		}
		if !slices.Equal(e.Gains, []world.Gain{evGain(id)}) || n != batchTicks+1 {
			t.Fatalf("released %s %d ticks into the new stage, want %s after %d", fmtGains(e.Gains), n, fmtGains([]world.Gain{evGain(id)}), batchTicks+1)
		}
		break
	}
}

// TestDifferPacedDrop: where the Differ drops its baseline, what waits is
// dropped too (the WORLD after the new baseline carries it), and nothing
// of it comes out later.
func TestDifferPacedDrop(t *testing.T) {
	id := storyIDs(1)[0]
	for _, c := range []struct {
		name string
		do   func(dr *differRig)
	}{
		{"a save state", func(dr *differRig) {
			dr.ticks(3)
			dr.store(mailbox.SpawnTrigger, []byte{0, 0, 0, 1})
			if e := dr.tickNoFrame(); !strings.Contains(e.Note, "heartbeat went backwards") {
				dr.t.Fatalf("after the save state: note %q", e.Note)
			}
		}},
		{"file select", func(dr *differRig) {
			if err := dr.g.Save(); err != nil {
				dr.t.Fatal(err)
			}
			dr.g.EnterFileSelect()
			dr.tick()
			if err := dr.g.LoadFile(0); err != nil {
				dr.t.Fatal(err)
			}
			dr.load(outset)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dr := pacedRig(t)
			dr.g.SetEvent(1, 0)
			dr.g.OnEventBit(id)
			dr.set(addr.OffGetItem, 0x01)
			dr.tick()
			if len(dr.d.Waiting()) != 2 {
				t.Fatalf("waiting %s", fmtGains(dr.d.Waiting()))
			}
			c.do(dr)
			dr.g.SetEvent(0, 0)
			rebased := false
			for range 3 * batchTicks {
				e := dr.tick()
				rebased = rebased || e.Rebased
				if len(e.Gains) > 0 {
					t.Fatalf("released %s after the drop", fmtGains(e.Gains))
				}
			}
			if !rebased || len(dr.d.Waiting()) > 0 {
				t.Fatalf("rebased %v, waiting %s", rebased, fmtGains(dr.d.Waiting()))
			}
		})
	}
}

// TestDifferWithhold: a WORLD sent while edges wait leaves them out, a MAX
// byte's whole field; an unpaced Differ withholds nothing.
func TestDifferWithhold(t *testing.T) {
	id := storyIDs(1)[0]
	ev, evMask := eventBit(id)
	_, swBit, err := addr.SwitchBit(0x00)
	if err != nil {
		t.Fatal(err)
	}
	sw, swMask := addr.OffMemBit(addr.TableSea)+swBit.Off, swBit.Mask
	dr := pacedRig(t)
	dr.g.SetEvent(1, 0)
	dr.g.OnEventBit(id)
	dr.set(addr.OffGetItem, 0x01)
	dr.put(addr.OffWalletSize, 2)
	dr.onSwitch(0x00, int(outset.Room))
	chestOff, chestMask := chest(addr.TableSea, 1)
	dr.set(chestOff, chestMask)
	if e := dr.tick(); !slices.Equal(e.Gains, []world.Gain{{Off: uint16(chestOff), Val: chestMask}}) {
		t.Fatalf("gains %s, want the chest", fmtGains(e.Gains))
	}

	img := dr.image()
	if img[ev]&evMask == 0 || img[addr.OffGetItem]&1 == 0 || img[addr.OffWalletSize] != 2 || img[sw]&swMask == 0 || img[chestOff]&chestMask == 0 {
		t.Fatal("the game image lacks what the test set")
	}
	dr.d.Withhold(&img)
	if img[ev]&evMask != 0 || img[addr.OffGetItem]&1 != 0 || img[addr.OffWalletSize] != 0 || img[sw]&swMask != 0 {
		t.Errorf("withheld image: event %02x, item %02x, wallet %02x, switch %02x",
			img[ev], img[addr.OffGetItem], img[addr.OffWalletSize], img[sw])
	}
	if img[chestOff]&chestMask == 0 {
		t.Error("the chest, sent as it rose, was withheld")
	}

	un := newDifferRig(t, story.Policy(world.FullPolicy()))
	un.tick()
	un.g.SetEvent(1, 0)
	un.g.OnEventBit(id)
	un.tick()
	before := un.image()
	after := before
	un.d.Withhold(&after)
	if after != before || un.d.Waiting() != nil {
		t.Error("an unpaced Differ withheld something")
	}
}

// FuzzDifferPaced runs a paced and an unpaced Differ over the same ticks
// and checks the pace against the edges: it never sends what did not
// rise, and loses nothing (what it sent and what waits are what rose);
// stage progress goes on the tick it rose; the inventory goes only on an
// IDLE tick and the story bits and registers only after an IDLE second;
// and once the game has been IDLE for a second with nothing new, nothing
// waits.
func FuzzDifferPaced(f *testing.F) {
	f.Add([]byte{0, 3, 1, 9, 2, 5, 3, 3, 3, 4, 0, 5, 3})
	f.Add([]byte{2, 1, 0, 0, 1, 1, 2, 1, 3, 3, 3, 3, 2, 0, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3})
	f.Add([]byte{4, 1, 0, 9, 9, 9, 1, 3, 3, 4, 0, 3})
	pol := story.Policy(world.FullPolicy())
	var ors, maxes []int
	for off := range addr.SaveImageSize {
		switch {
		case pol[off] == 0:
		case class.Of(off).Class == class.Max:
			maxes = append(maxes, off)
		default:
			ors = append(ors, off)
		}
	}
	f.Fuzz(func(t *testing.T, ops []byte) {
		if len(ops) > 400 {
			ops = ops[:400]
		}
		dr := newDifferRig(t, pol)
		dr.d.Pace(nil)
		un := NewDiffer(pol)
		var sent, rose world.Image
		stages := []fakegame.Stage{outset, drc, drcBoss, woods}
		arg := func(i int) int {
			if i < len(ops) {
				return int(ops[i])
			}
			return 0
		}
		tick := func() {
			dr.g.Frame()
			o := dr.observeNow()
			ue := un.observe(o)
			pe := dr.d.observe(o)
			world.Apply(&rose, ue.Gains)
			for _, g := range pe.Gains {
				off := int(g.Off)
				if class.Of(off).Class == class.Max && g.Val > rose[off] || class.Of(off).Class != class.Max && g.Val&^rose[off] != 0 {
					t.Fatalf("sent 0x%03X=%02x, which never rose (rose %02x)", off, g.Val, rose[off])
				}
				switch {
				case inventoryByte(off) && !o.st.Idle:
					t.Fatalf("inventory 0x%03X sent on a busy tick", off)
				case (story.IsStoryByte(off) || story.IsRegisterByte(off)) && o.st.IdleRun <= batchTicks:
					t.Fatalf("story byte 0x%03X sent %d ticks into an IDLE run", off, o.st.IdleRun)
				}
			}
			world.Apply(&sent, pe.Gains)
			// Stage progress (not story, register, inventory or switch)
			// goes as it rises.
			var now, pnow world.Image
			for _, g := range ue.Gains {
				if off := int(g.Off); !inventoryByte(off) && !story.IsStoryByte(off) && !story.IsRegisterByte(off) && !hub.IsSwitchByte(off) {
					world.Apply(&now, []world.Gain{g})
				}
			}
			for _, g := range pe.Gains {
				if off := int(g.Off); !inventoryByte(off) && !story.IsStoryByte(off) && !story.IsRegisterByte(off) && !hub.IsSwitchByte(off) {
					world.Apply(&pnow, []world.Gain{g})
				}
			}
			if now != pnow {
				t.Fatalf("stage progress: paced %s, unpaced %s", fmtGains(nonZero(&pnow)), fmtGains(nonZero(&now)))
			}
			var all world.Image = sent
			world.Apply(&all, dr.d.Waiting())
			if all != rose {
				t.Fatalf("sent and waiting %s, rose %s", fmtGains(nonZero(&all)), fmtGains(nonZero(&rose)))
			}
		}
		tick()
		for i := 0; i < len(ops); i++ {
			a := arg(i + 1)
			switch ops[i] % 5 {
			case 0:
				off := ors[(a<<8|arg(i+2))%len(ors)]
				if b := byte(1) << (arg(i+3) % 8); pol[off]&b != 0 {
					dr.set(off, b)
				}
				i += 3
			case 1:
				off := maxes[a%len(maxes)]
				m := world.SharedMask(off)
				cur := dr.read(dr.byteAddr(off), 1)[0]
				if v := byte(arg(i+2)) & m; v > cur&m {
					dr.put(off, cur&^m|v)
				}
				i += 2
			case 2:
				switch a % 4 {
				case 0:
					dr.g.SetEvent(0, 0)
				case 1:
					dr.g.SetEvent(1, 0)
				case 2:
					dr.g.SetPauseMenu(a&4 != 0)
				default:
					dr.load(stages[(a>>2)%len(stages)])
				}
				i++
			case 3:
				tick()
			case 4:
				off := ors[(a<<8|arg(i+2))%len(ors)]
				dr.clear(off, byte(1)<<(arg(i+3)%8))
				i += 3
			}
		}
		dr.g.SetEvent(0, 0)
		dr.g.SetPauseMenu(false)
		for range batchTicks + 1 {
			tick()
		}
		if w := dr.d.Waiting(); len(w) > 0 {
			t.Fatalf("after an IDLE second, still waiting %s", fmtGains(w))
		}
		if sent != rose {
			t.Fatalf("sent %s, rose %s", fmtGains(nonZero(&sent)), fmtGains(nonZero(&rose)))
		}
	})
}

// newStoryPlayer is newPlayer with story sync: the replica asks for the
// image and story bits (or only the image, story false), proposes under
// the hub's session policy, and its Differ is paced.
func newStoryPlayer(t *testing.T, ctx context.Context, h *host, name string, nonce bind.Nonce, seed uint64, storySync bool, got func(*wire.GainsDown)) *player {
	t.Helper()
	p := &player{rig: newRig(t, name), log: &logBuf{}}
	p.tag(p.tagFor(testWorld, nonce))
	want := hub.FeatureImage
	if storySync {
		want |= hub.FeatureStory
	}
	r, err := New(Config{Mem: p.m, Logf: p.log.logf, Policy: hub.SessionPolicy(nil), Want: want})
	if err != nil {
		t.Fatal(err)
	}
	r.differ.Pace(nil)
	p.r = r
	p.u = &unruly{rng: rand.New(rand.NewPCG(seed, 7)), r: r}
	p.client = network.NewCoopClient(name, 0)
	p.client.OnCoop = func(b []byte) {
		if m, err := wire.Decode(b, wire.Down); err == nil {
			if gd, ok := m.(*wire.GainsDown); ok {
				got(gd)
			}
		}
		p.u.onCoop(b)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		network.RunCoop(ctx, p.client, h.addr, nil, p.u.session)
	}()
	t.Cleanup(func() { <-done })
	return p
}

// TestStoryBatchThroughHub: what one event sets reaches the hub and the
// partner as one GAINS, after the sender's IDLE second, and a partner
// without story sync gets none of its story bits.
func TestStoryBatchThroughHub(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a loopback server")
	}
	h := startHost(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var bGot, cGot recv
	a := newStoryPlayer(t, ctx, h, "Link", bind.Nonce{1}, 1, true, func(*wire.GainsDown) {})
	b := newStoryPlayer(t, ctx, h, "Tetra", bind.Nonce{2}, 2, true, bGot.add)
	c := newStoryPlayer(t, ctx, h, "Medli", bind.Nonce{3}, 3, false, cGot.add)
	players := []*player{a, b, c}
	deadline := time.Now().Add(10 * time.Second)
	for !(a.r.Status().State == StateArmed && b.r.Status().State == StateArmed && c.r.Status().State == StateArmed) {
		for _, p := range players {
			p.step()
		}
		if time.Now().After(deadline) {
			t.Fatalf("not armed: %+v / %+v / %+v\nA:\n%s", a.r.Status(), b.r.Status(), c.r.Status(), a.log)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if f := c.r.Status().Features; f&hub.FeatureStory != 0 {
		t.Fatalf("c was granted %s", f)
	}

	ids := storyIDs(3)
	figOff := addr.OffEvent + addr.EventByte(0x80FF).Off
	chestOff, chestMask := chest(addr.TableSea, 7)
	want := sortGains([]world.Gain{evGain(ids[0]), evGain(ids[1]), evGain(ids[2]), {Off: uint16(figOff), Val: 0x02}})

	aTick := func() Report {
		a.g.Frame()
		a.u.deliver()
		return a.r.Tick()
	}
	a.g.SetEvent(1, 0)
	aTick()
	a.g.OnEventBit(ids[0])
	a.set(figOff, 0x02)
	a.set(chestOff, chestMask)
	if rep := aTick(); rep.Gains != 1 {
		t.Fatalf("the event's first tick sent %d gain bytes, want the chest's", rep.Gains)
	}
	for i := range 2 * batchTicks {
		if i == batchTicks {
			a.g.OnEventBit(ids[1])
		}
		if rep := aTick(); rep.Gains != 0 {
			t.Fatalf("tick %d of the event sent %d gain bytes", i, rep.Gains)
		}
	}
	a.g.SetEvent(0, 0)
	a.g.OnEventBit(ids[2]) // set as the event ends
	for i := 1; ; i++ {
		rep := aTick()
		if rep.Gains == 0 {
			if i > batchTicks+1 {
				t.Fatalf("no batch %d ticks after the event", i)
			}
			continue
		}
		if i < batchTicks || rep.Gains != len(want) {
			t.Fatalf("tick %d after the event sent %d gain bytes, want %d after %d", i, rep.Gains, len(want), batchTicks)
		}
		break
	}

	// The batch arrives whole: b gets it in one GAINS, c without its
	// story bits (the figurine register is image, not story).
	deadline = time.Now().Add(10 * time.Second)
	for {
		for _, p := range players[1:] {
			p.step()
		}
		img := h.store.Image()
		bv, _ := b.view()
		cv, _ := c.view()
		done := true
		for _, g := range want {
			done = done && img[g.Off]&g.Val == g.Val && bv[g.Off]&g.Val == g.Val
		}
		done = done && cv[figOff]&0x02 != 0
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the batch did not arrive: world %s", fmtGains(nonZero(&img)))
		}
		time.Sleep(5 * time.Millisecond)
	}
	withStory := 0
	for _, gd := range bGot.all() {
		var story []world.Gain
		for _, g := range gd.Gains {
			if off := int(g.Off); storyByte(off) || off == figOff {
				story = append(story, world.Gain(g))
			}
		}
		if len(story) > 0 {
			withStory++
			if !slices.Equal(story, want) {
				t.Errorf("b's GAINS carried %s, want the whole batch %s", fmtGains(story), fmtGains(want))
			}
		}
	}
	if withStory != 1 {
		t.Errorf("b got %d GAINS with story bytes, want 1", withStory)
	}
	for _, gd := range cGot.all() {
		for _, g := range gd.Gains {
			if storyByte(int(g.Off)) {
				t.Errorf("c, without story sync, got story byte 0x%03X", g.Off)
			}
		}
	}
	if cv, _ := c.view(); cv[want[0].Off] != 0 {
		t.Error("c's view has a story bit")
	}
}

func storyByte(off int) bool { return story.IsStoryByte(off) }

// recv records the GAINS a client receives.
type recv struct {
	mu sync.Mutex
	gs []*wire.GainsDown
}

func (r *recv) add(m *wire.GainsDown) {
	r.mu.Lock()
	r.gs = append(r.gs, m)
	r.mu.Unlock()
}

func (r *recv) all() []*wire.GainsDown {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.gs)
}
