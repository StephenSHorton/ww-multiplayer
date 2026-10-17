package hub

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/story"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// Event bits the tests name (d_save_event_flag.inc).
const (
	evMetKorl   = 0x0F80 // MET_KORL, :111
	evRodeKorl  = 0x2A08 // RODE_KORL, :322 (LocalOnly)
	ev0801      = 0x0801 // :66
	evCourtyard = 0x3804 // HYRULE_COURTYARD_CUTSCENE, :430
)

// evGain is event bit id as a gain.
func evGain(id uint16) world.Gain {
	b := addr.EventByte(id)
	return world.Gain{Off: uint16(addr.OffEvent + b.Off), Val: b.Mask}
}

// plainStory are n story bits that sync, one per event byte from the
// first, none of them a hold or a resume milestone.
func plainStory(t *testing.T, n int) []uint16 {
	t.Helper()
	skip := map[uint16]bool{}
	for _, h := range class.Holds() {
		skip[h.ID] = true
	}
	for _, id := range resumeMilestones {
		skip[id] = true
	}
	var ids []uint16
	for i := 0; i < addr.EventBitBytes && len(ids) < n; i++ {
		m := class.SyncMaskOf(i)
		for b := 7; b >= 0; b-- {
			if id := uint16(i)<<8 | 1<<b; m&(1<<b) != 0 && !skip[id] {
				ids = append(ids, id)
				break
			}
		}
	}
	if len(ids) < n {
		t.Fatalf("only %d plain story bits", len(ids))
	}
	return ids
}

// unsynced is the first bit of list, and the first that shares its byte
// with a synced bit (zero for none).
func unsynced(t *testing.T, list []class.EventBit) (first, mixed world.Gain) {
	t.Helper()
	if len(list) == 0 {
		t.Fatal("no bit in the list")
	}
	for _, e := range list {
		if g := evGain(e.ID); class.SyncMaskOf(int(g.Off)-addr.OffEvent) != 0 {
			mixed = g
			break
		}
	}
	return evGain(list[0].ID), mixed
}

// TestDefaultPolicy: the hub's default policy is world.DefaultPolicy with
// the story bytes set to exactly the SyncMask, and SessionPolicy adds the
// verdict's switches to it.
func TestDefaultPolicy(t *testing.T) {
	p, base := DefaultPolicy(), world.DefaultPolicy()
	for off := range world.ImageSize {
		want := base[off]
		if story.IsStoryByte(off) {
			want = class.SyncMaskOf(off - addr.OffEvent)
		}
		if p[off] != want {
			t.Errorf("byte 0x%03X (%s) = %02x, want %02x", off, classField(off), p[off], want)
		}
		if IsSwitchByte(off) && p[off] != 0 {
			t.Errorf("switch byte 0x%03X open by default", off)
		}
	}
	if *SessionPolicy(nil) != *p {
		t.Error("SessionPolicy(nil) is not DefaultPolicy")
	}
	if got, want := SessionPolicy(testSynced), SwitchPolicy(DefaultPolicy(), testSynced); *got != *want {
		t.Error("SessionPolicy is not DefaultPolicy with the verdict's switches")
	}
	for _, id := range []uint16{evMetKorl, ev0801, evCourtyard} {
		if !class.EventSyncs(id) {
			t.Errorf("event 0x%04X does not sync; the tests below need it to", id)
		}
	}
	if class.EventSyncs(evRodeKorl) {
		t.Error("RODE_KORL syncs; §6.5 has it LocalOnly")
	}
}

// TestDefaultPolicyClasses: what a world under the hub's default policy
// takes of each class of byte M4 adds, and why it refuses the rest.
func TestDefaultPolicyClasses(t *testing.T) {
	plain := evGain(plainStory(t, 1)[0])
	local, localMixed := unsynced(t, class.LocalOnly())
	d8, _ := unsynced(t, class.GameCleared())
	refight := evGain(class.Refight()[0].ID)
	if localMixed.Val == 0 {
		t.Fatal("no LocalOnly bit shares a byte with a synced one")
	}
	syncedOfMixed := class.SyncMaskOf(int(localMixed.Off) - addr.OffEvent)
	// An unnamed bit of a story byte, and a register outside the
	// allowlist.
	var unnamed world.Gain
	for i := range addr.EventBitBytes {
		if m := world.SharedMask(addr.OffEvent + i); m != 0xFF {
			for b := byte(1); b != 0; b <<= 1 {
				if m&b == 0 {
					unnamed = world.Gain{Off: uint16(addr.OffEvent + i), Val: b}
					break
				}
			}
			break
		}
	}
	perPlayerReg := -1
	for i := addr.EventRegFirst; i < addr.EventSize && perPlayerReg < 0; i++ {
		if class.Of(addr.OffEvent+i).Class == class.PerPlayer {
			perPlayerReg = addr.OffEvent + i
		}
	}
	if unnamed.Val == 0 || perPlayerReg < 0 {
		t.Fatal("no unnamed story bit or per-player register to test")
	}
	fig := uint16(addr.OffEvent + addr.EventByte(0x80FF).Off)
	letter := addr.EventByte(0x7A03) // LETTER_ROCK_SPIRE_SHOP_AD, field 0x03
	letterOff := uint16(addr.OffEvent + letter.Off)
	keyNum := uint16(addr.OffMemBit(addr.TableDRC) + addr.MemBitOffKeyNum)
	g := func(off int, v byte) world.Gain { return world.Gain{Off: uint16(off), Val: v} }

	cases := []struct {
		name  string
		prior []world.Gain // merged first
		g     world.Gain
		want  byte         // the world byte after
		why   world.Reason // the refusal, 0 for none
		rej   byte         // the refused bits
	}{
		{name: "a story bit of the SyncMask", g: plain, want: plain.Val},
		{name: "MET_KORL, a hold, syncs", g: evGain(evMetKorl), want: evGain(evMetKorl).Val},
		{name: "a LocalOnly bit", g: local, why: world.ReasonPolicy, rej: local.Val},
		{name: "RODE_KORL (LocalOnly)", g: evGain(evRodeKorl), want: 0, why: world.ReasonPolicy, rej: evGain(evRodeKorl).Val},
		{name: "a game-cleared bit (D8)", g: d8, why: world.ReasonPolicy, rej: d8.Val},
		{name: "a refight bit", g: refight, why: world.ReasonPolicy, rej: refight.Val},
		{name: "a byte's synced bits with a LocalOnly one", g: world.Gain{Off: localMixed.Off, Val: localMixed.Val | syncedOfMixed},
			want: syncedOfMixed, why: world.ReasonPolicy, rej: localMixed.Val},
		{name: "an unnamed bit of a story byte", g: unnamed, why: world.ReasonMask, rej: unnamed.Val},
		{name: "an unnamed event byte (0x42-0x78)", g: g(addr.OffEvent+0x50, 0x01), why: world.ReasonClass, rej: 0x01},
		{name: "a figurine bitfield, by OR", prior: []world.Gain{{Off: fig, Val: 0x01}}, g: world.Gain{Off: fig, Val: 0x04}, want: 0x05},
		{name: "a letter, by the MAX of its field", prior: []world.Gain{{Off: letterOff, Val: 2}}, g: world.Gain{Off: letterOff, Val: 3}, want: 3},
		{name: "a letter going back", prior: []world.Gain{{Off: letterOff, Val: 2}}, g: world.Gain{Off: letterOff, Val: 1}, want: 2},
		{name: "bits outside a letter's field", g: world.Gain{Off: letterOff, Val: 0x04}, why: world.ReasonMask, rej: 0x04},
		{name: "a register outside the allowlist", g: g(perPlayerReg, 0x01), why: world.ReasonClass, rej: 0x01},
		{name: "a get-item flag", g: g(addr.OffGetItem+3, 0x20), want: 0x20},
		{name: "the telescope, with a bottle content held (local)", g: g(addr.OffGetItem, 0x05), want: 0x01, why: world.ReasonMask, rej: 0x04},
		{name: "a collect flag", g: g(addr.OffCollectFlags+1, 0x02), want: 0x02},
		{name: "the Hero's Charm owned, and worn (local)", g: g(addr.OffCollectFlags+4, 0x03), want: 0x01, why: world.ReasonMask, rej: 0x02},
		{name: "a song", g: g(addr.OffTact, 0x40), want: 0x40},
		{name: "a Triforce shard", g: g(addr.OffTriforce, 0x80), want: 0x80},
		{name: "a pearl", g: g(addr.OffSymbol, 0x04), want: 0x04},
		{name: "a spoil", g: g(addr.OffBeastFlags, 0x10), want: 0x10},
		{name: "a bait flag", g: g(addr.OffBaitFlags, 0x01), want: 0x01},
		{name: "the wallet, by MAX", prior: []world.Gain{g(addr.OffWalletSize, 1)}, g: g(addr.OffWalletSize, 2), want: 2},
		{name: "a smaller wallet", prior: []world.Gain{g(addr.OffWalletSize, 2)}, g: g(addr.OffWalletSize, 1), want: 2},
		{name: "the quiver", g: g(addr.OffArrowMax, 60), want: 60},
		{name: "the bomb bag", prior: []world.Gain{g(addr.OffBombMax, 60)}, g: g(addr.OffBombMax, 30), want: 60},
		{name: "max life (DERIVED)", g: g(addr.OffMaxLife+1, 0x10), why: world.ReasonClass, rej: 0x10},
		{name: "max magic (DERIVED)", g: g(addr.OffMaxMagic, 0x20), why: world.ReasonClass, rej: 0x20},
		{name: "a small key count (DERIVED)", g: world.Gain{Off: keyNum, Val: 1}, why: world.ReasonClass, rej: 1},
		{name: "an item slot (DERIVED)", g: g(addr.OffItems, 0x20), why: world.ReasonClass, rej: 0x20},
		{name: "the equipped sword (DERIVED)", g: g(addr.OffSelectEquip, 0x38), why: world.ReasonClass, rej: 0x38},
		{name: "rupees (PER_PLAYER)", g: g(addr.OffRupee+1, 0x10), why: world.ReasonClass, rej: 0x10},
		{name: "a memory switch, without the verdict", g: switchGain(t, 3, 0x05), why: world.ReasonPolicy, rej: switchGain(t, 3, 0x05).Val},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := world.New(testWorld, time.Unix(1_800_000_000, 0))
			w.SetPolicy(DefaultPolicy())
			if r := w.ApplyGains(c.prior); len(r.Rejected) > 0 {
				t.Fatalf("prior refused: %+v", r.Rejected)
			}
			r := w.ApplyGains([]world.Gain{c.g})
			if img := w.Image(); img[c.g.Off] != c.want {
				t.Errorf("world byte 0x%03X (%s) = %02x, want %02x", c.g.Off, classField(int(c.g.Off)), img[c.g.Off], c.want)
			}
			switch {
			case c.why == 0 && len(r.Rejected) > 0:
				t.Errorf("refused %+v", r.Rejected)
			case c.why != 0 && (len(r.Rejected) != 1 || r.Rejected[0].Why != c.why || r.Rejected[0].Val != c.rej):
				t.Errorf("refusals %+v, want bits %02x for %s", r.Rejected, c.rej, c.why)
			}
		})
	}
}

// storyRig is a hub with the default policy (Config.Policy nil), alice
// with story sync and bob without.
func storyRig(t *testing.T) (r *rig, alice, bob *fakeConn) {
	t.Helper()
	r = newRig(t, Config{Features: DefaultFeatures, SyncedSwitch: testSynced})
	alice = r.join(newConn(1, "alice"))
	bob = newConn(2, "bob")
	r.attach(bob)
	m := hello(wire.FileBound, testWorld, 2)
	m.Want = uint32(DefaultFeatures &^ FeatureStory)
	if w := welcomeOf(t, r.exchange(bob, m)); w.Status != wire.StatusOK || Feature(w.Features) != DefaultFeatures&^FeatureStory {
		t.Fatalf("bob's welcome %+v", w)
	}
	return r, alice, bob
}

// TestStorySync: story bits enter the world and reach only the players
// with story sync; the registers, inventory and capacities are image
// bytes every bound player shares.
func TestStorySync(t *testing.T) {
	r, alice, bob := storyRig(t)
	ids := plainStory(t, 3)
	a, b, c := evGain(ids[0]), evGain(ids[1]), evGain(ids[2])
	local, _ := unsynced(t, class.LocalOnly())
	tbox := world.Gain{Off: offTbox(3, 0), Val: 0x01}

	// Alice: her story bit merges and is relayed to her, not to bob; her
	// LocalOnly bit is refused, quietly.
	r.send(alice, up(1, a, tbox, local))
	if got, want := gainsOf(t, alice.take(t)), sortedGains(a, tbox); !slices.Equal(got, want) {
		t.Errorf("alice's relay %v, want %v", got, want)
	}
	if got, want := gainsOf(t, bob.take(t)), sortedGains(tbox); !slices.Equal(got, want) {
		t.Errorf("bob's relay %v, want %v", got, want)
	}
	if img := r.store.Image(); img[a.Off] != a.Val || img[local.Off]&local.Val != 0 {
		t.Errorf("world story byte %02x, LocalOnly byte %02x", img[a.Off], img[local.Off])
	}
	if p := r.peer(1); p.Rejected != 1 {
		t.Errorf("alice's rejected %d, want 1 (the LocalOnly bit)", p.Rejected)
	}

	// Bob: his story bit is dropped and counted; the rest merges and
	// reaches alice.
	fig := world.Gain{Off: uint16(addr.OffEvent + addr.EventByte(0x80FF).Off), Val: 0x08}
	item := world.Gain{Off: uint16(addr.OffGetItem + 3), Val: 0x40}
	wallet := world.Gain{Off: uint16(addr.OffWalletSize), Val: 1}
	r.send(bob, up(1, b, fig, item, wallet))
	want := sortedGains(fig, item, wallet)
	if got := gainsOf(t, alice.take(t)); !slices.Equal(got, want) {
		t.Errorf("alice's relay of bob's %v, want %v", got, want)
	}
	if got := gainsOf(t, bob.take(t)); !slices.Equal(got, want) {
		t.Errorf("bob's relay %v, want %v", got, want)
	}
	if img := r.store.Image(); img[b.Off] != 0 {
		t.Errorf("bob's story bit merged: %02x", img[b.Off])
	}
	if p := r.peer(2); p.Rejected != 1 {
		t.Errorf("bob's rejected %d, want 1", p.Rejected)
	}

	// A change of story bits alone does not reach bob.
	r.send(alice, up(2, c))
	if got := gainsOf(t, alice.take(t)); !slices.Equal(got, sortedGains(c)) {
		t.Errorf("alice's relay %v", got)
	}
	if ms := bob.take(t); len(ms) != 0 {
		t.Errorf("bob got %s for a story-only change", subs(ms))
	}

	// WORLD: bob's story bytes are stripped before the merge, and his
	// view never has any; alice's RESYNC has them.
	r.send(bob, worldMsg(testWorld, r.store.Seq(), b, world.Gain{Off: offTbox(5, 1), Val: 0x02}))
	bw := pick[*wire.World](bob.take(t))
	if len(bw) != 1 {
		t.Fatal("bob got no WORLD back")
	}
	for off, v := range bw[0].Image {
		if story.IsStoryByte(off) && v != 0 {
			t.Errorf("bob's WORLD has story byte 0x%03X = %02x", off, v)
		}
	}
	if bw[0].Image[fig.Off] != fig.Val || bw[0].Image[offTbox(5, 1)] != 0x02 {
		t.Error("bob's WORLD lacks the register or his chest")
	}
	if img := r.store.Image(); img[b.Off] != 0 {
		t.Errorf("bob's WORLD merged a story bit: %02x", img[b.Off])
	}
	if got := gainsOf(t, alice.take(t)); !slices.Equal(got, []wire.Gain{{Off: offTbox(5, 1), Val: 0x02}}) {
		t.Errorf("alice's relay of bob's WORLD %v", got)
	}
	aw := pick[*wire.World](r.exchange(alice, &wire.Ctrl{Op: wire.CtrlResync}))
	if len(aw) != 1 || aw[0].Image[a.Off] != a.Val || aw[0].Image[c.Off] != c.Val {
		t.Errorf("alice's RESYNC lacks the story bits: %+v", aw)
	}

	// The host turns story sync off and on: alice's story bits are
	// refused meanwhile, and she is asked to resync after.
	if err := r.h.SetFeatures(DefaultFeatures &^ FeatureStory); err != nil {
		t.Fatal(err)
	}
	if cs := pick[*wire.Ctrl](alice.take(t)); len(cs) != 1 || cs[0].Op != wire.CtrlCoopOff || Feature(cs[0].Arg) != DefaultFeatures&^FeatureStory {
		t.Fatalf("alice after story off: %+v", cs)
	}
	d := evGain(plainStory(t, 4)[3])
	r.send(alice, up(3, d))
	if img := r.store.Image(); img[d.Off] != 0 {
		t.Error("a story bit merged with story sync off")
	}
	if err := r.h.SetFeatures(DefaultFeatures); err != nil {
		t.Fatal(err)
	}
	cs := pick[*wire.Ctrl](alice.take(t))
	if len(cs) != 2 || cs[0].Op != wire.CtrlCoopOff || Feature(cs[0].Arg) != DefaultFeatures || cs[1].Op != wire.CtrlResync {
		t.Errorf("alice after story back on: %+v, want COOP_OFF then RESYNC", cs)
	}
	if cs := pick[*wire.Ctrl](bob.take(t)); len(cs) != 2 || Feature(cs[0].Arg)&FeatureStory != 0 || cs[1].Op == wire.CtrlResync {
		t.Errorf("bob's regrants %+v: no story, and nothing new to resync", cs)
	}
}

// TestDerivedNeverInWorld: a bound player's WORLD carries its DERIVED
// bytes (the preflight reads them), but none enters the world or any
// player's WORLD back.
func TestDerivedNeverInWorld(t *testing.T) {
	r, alice, _ := storyRig(t)
	keyNum := addr.OffMemBit(addr.TableDRC) + addr.MemBitOffKeyNum
	derived := []int{addr.OffMaxLife, addr.OffMaxLife + 1, addr.OffMaxMagic, addr.OffItems, addr.OffSelectEquip, keyNum}
	var im wire.Image
	for _, off := range derived {
		if class.Of(off).Class != class.Derived {
			t.Fatalf("byte 0x%03X is %s, not DERIVED", off, class.Of(off).Class)
		}
		im[off] = 0x13
	}
	ms := r.exchange(alice, &wire.World{WorldID: wire.ID(testWorld), Image: im})
	ws := pick[*wire.World](ms)
	if len(ws) != 1 {
		t.Fatalf("got %s", subs(ms))
	}
	img := r.store.Image()
	for _, off := range derived {
		if img[off] != 0 || ws[0].Image[off] != 0 {
			t.Errorf("DERIVED byte 0x%03X: world %02x, WORLD back %02x", off, img[off], ws[0].Image[off])
		}
	}
	if r.store.Seq() != 0 {
		t.Errorf("seq %d after a WORLD of DERIVED bytes alone", r.store.Seq())
	}
}

// TestStoryBind is §5.3's bind classification with story bits in the
// world: divergent, far behind, the holds named, and what --coop-merge
// allows.
func TestStoryBind(t *testing.T) {
	ids := plainStory(t, 2)
	a, b := evGain(ids[0]), evGain(ids[1])
	cases := []struct {
		name    string
		merge   bool
		dryRun  bool         // the joiner wants the image alone, as a --coop-dry-run replica does
		world   []world.Gain // in the world first (via a bound peer)
		file    []world.Gain
		notice  []string // in the preflight's DIVERGENT; nil for none
		granted bool
		log     string // in the host's log when granted
	}{
		{name: "story subset", world: []world.Gain{a, evGain(evMetKorl)}, file: []world.Gain{a, evGain(evMetKorl)}, granted: true, log: "a subset of the world"},
		{name: "story divergent", file: []world.Gain{a},
			notice: []string{"progress the world lacks", "mEvent", "needs --coop-merge on the host"}},
		{name: "story divergent, merge", merge: true, file: []world.Gain{a},
			notice: []string{"binding it merges that into the world (--coop-merge)"}, granted: true, log: "merged under --coop-merge"},
		{name: "a LocalOnly bit is not divergent", file: []world.Gain{evGain(evRodeKorl)}, granted: true, log: "a subset"},
		{name: "far behind", world: []world.Gain{evGain(evMetKorl)},
			notice: []string{"far ahead of your file", "0x0F80", "copying the host's save in (all three files) is better than a union", "needs --coop-merge", "held until your file catches up: MET_KORL"}},
		{name: "far behind, merge", merge: true, world: []world.Gain{evGain(evMetKorl)},
			notice: []string{"the host's --coop-merge allows a union", "MET_KORL"}, granted: true, log: "far behind, merged under --coop-merge, 1 holds"},
		{name: "far behind by a later milestone", world: []world.Gain{evGain(evMetKorl), evGain(ev0801)}, file: []world.Gain{evGain(evMetKorl)},
			notice: []string{"story milestones 0x0801)"}},
		// A bind outlasts the session: the file's next one, granted story
		// sync, would take its story bits in and write the world's story
		// into it unjudged (judge).
		{name: "a dry run is far behind too", dryRun: true, world: []world.Gain{evGain(evMetKorl)},
			notice: []string{"far ahead of your file", "0x0F80", "needs --coop-merge", "MET_KORL"}},
		{name: "a dry run's story bits are divergent too", dryRun: true, file: []world.Gain{b},
			notice: []string{"progress the world lacks", "mEvent", "needs --coop-merge on the host"}},
		{name: "a dry run far behind, merge", dryRun: true, merge: true, world: []world.Gain{evGain(evMetKorl)},
			notice: []string{"the host's --coop-merge allows a union"}, granted: true, log: "far behind, merged under --coop-merge"},
		{name: "a dry run's subset", dryRun: true, world: []world.Gain{a, evGain(evMetKorl)}, file: []world.Gain{a, evGain(evMetKorl)}, granted: true, log: "a subset of the world"},
		{name: "divergent and far behind", world: []world.Gain{evGain(evMetKorl)}, file: []world.Gain{a},
			notice: []string{"progress the world lacks", "held until your file catches up: MET_KORL"}},
		{name: "the holds named", world: []world.Gain{evGain(evMetKorl), evGain(evCourtyard)}, file: []world.Gain{b},
			notice: []string{"held until your file catches up: MET_KORL, HYRULE_COURTYARD_CUTSCENE"}},
		{name: "holds without a refusal", merge: true, world: []world.Gain{evGain(evCourtyard)}, granted: true, log: "a subset of the world, 1 holds"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, Config{Features: DefaultFeatures, Merge: tc.merge})
			if len(tc.world) > 0 {
				h := r.join(newConn(9, "host"))
				r.send(h, up(1, tc.world...))
				if img := r.store.Image(); img[tc.world[0].Off]&tc.world[0].Val == 0 {
					t.Fatal("the world did not take its story bits")
				}
			}
			u := newConn(1, "joiner")
			r.attach(u)
			m := hello(wire.FileUnbound, world.ID{}, 0)
			if tc.dryRun {
				m.Want = uint32(FeatureImage)
			}
			r.send(u, m)
			u.take(t)
			ns := pick[*wire.Notice](r.exchange(u, worldMsg(world.ID{}, 0, tc.file...)))
			switch {
			case tc.notice == nil && len(ns) > 0:
				t.Errorf("preflight notices %+v, want none", ns)
			case tc.notice != nil && (len(ns) != 1 || ns[0].Code != wire.NoticeDivergent || !containsAll(ns[0].Text, tc.notice...)):
				var texts []string
				for _, n := range ns {
					texts = append(texts, n.Code.String()+": "+n.Text)
				}
				t.Errorf("preflight notices %q, want DIVERGENT with %q", texts, tc.notice)
			}
			ask := hello(wire.FileUnbound, world.ID{}, 0)
			ask.WorldID, ask.BindNonce = wire.ID(testWorld), wire.ID{0x77}
			ask.Want = m.Want | uint32(AskBind)
			ms := r.exchange(u, ask)
			w := welcomeOf(t, ms)
			granted := Feature(w.Features)&FeatureBind != 0
			if granted != tc.granted {
				t.Fatalf("bind granted %v (reason %s, %s), want %v", granted, w.Reason, subs(ms), tc.granted)
			}
			if !granted {
				ns := pick[*wire.Notice](ms)
				if w.Reason != wire.NoticeDivergent || len(ns) != 1 || !strings.Contains(ns[0].Text, "--coop-merge") {
					t.Errorf("refusal: reason %s, notices %+v", w.Reason, ns)
				}
				return
			}
			if !r.log.has(tc.log) {
				t.Errorf("the host's log lacks %q: %q", tc.log, r.log.lines)
			}
		})
	}
}

// TestBindJudgedAgain: the bind check is made again when the file asks,
// against the world as it is then.
func TestBindJudgedAgain(t *testing.T) {
	for _, merge := range []bool{false, true} {
		r := newRig(t, Config{Features: DefaultFeatures, Merge: merge})
		h := r.join(newConn(9, "host"))
		u := newConn(1, "joiner")
		r.attach(u)
		r.send(u, hello(wire.FileUnbound, world.ID{}, 0))
		u.take(t)
		if ns := pick[*wire.Notice](r.exchange(u, worldMsg(world.ID{}, 0))); len(ns) != 0 {
			t.Fatalf("an empty file against an empty world: %q %s", ns[0].Text, ns[0].Code)
		}
		// The partner rides on to Windfall meanwhile.
		r.send(h, up(1, evGain(evMetKorl)))
		ask := hello(wire.FileUnbound, world.ID{}, 0)
		ask.WorldID, ask.BindNonce = wire.ID(testWorld), wire.ID{0x78}
		ask.Want |= uint32(AskBind)
		ms := r.exchange(u, ask)
		w := welcomeOf(t, ms)
		if granted := Feature(w.Features)&FeatureBind != 0; granted != merge {
			t.Fatalf("merge %v: granted %v (%s)", merge, granted, subs(ms))
		}
		if !merge {
			ns := pick[*wire.Notice](ms)
			if len(ns) != 1 || !containsAll(ns[0].Text, "far ahead", "0x0F80", "replaces all three of your files", "MET_KORL") {
				t.Errorf("refusal notices %+v", ns)
			}
		}
	}
}

// TestHeldNoticeFits: however many holds a file has, the NOTICE names as
// many as fit and counts the rest, unclipped.
func TestHeldNoticeFits(t *testing.T) {
	var held []story.Held
	for _, h := range class.Holds() {
		held = append(held, story.Held{Hold: h})
	}
	long := "the world is far ahead of your file (story milestones 0x0F80, 0x0801, 0x0808, 0x2401); copy the host's save in (it replaces all three of your files), or bind with --coop-merge on the host"
	for _, c := range []struct {
		text string
		held []story.Held
		has  []string
	}{
		{"short", nil, nil},
		{"short", held[:2], []string{"; held until your file catches up: MET_KORL, UNK_0801"}},
		{"short", held, []string{"MET_KORL", held[len(held)-1].Hold.Name}},
		{long, held, []string{"MET_KORL", "more"}},
		{strings.Repeat("x", wire.MaxNoticeLen), held, []string{strconv.Itoa(len(held)) + " story bits"}},
	} {
		s := withHeld(c.text, c.held)
		if !strings.HasPrefix(s, c.text) || !containsAll(s, c.has...) {
			t.Errorf("withHeld(%.20q, %d holds) = %q", c.text, len(c.held), s)
		}
		if len(c.text) <= wire.MaxNoticeLen-60 && len(s) > wire.MaxNoticeLen {
			t.Errorf("withHeld(%.20q, %d holds) is %d bytes, more than a NOTICE holds", c.text, len(c.held), len(s))
		}
	}
}
