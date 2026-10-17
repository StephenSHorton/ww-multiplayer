package replica

import (
	"bytes"
	"slices"
	"sync"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/derive"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/story"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/synctables"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/tablestest"
)

// The M4 receiver's tests run the replica's reconciler over a FakeGame
// with the M1 tables built from tablestest's vanilla-shaped disc: Dragon
// Roost Cavern (table 3, stage M_NewD2) has four key chests, tbox bits 1-4
// in room 1, and four key doors, memory switches 0x10-0x13, which the
// switch table marks latching; the sea (table 0) has stage-file heart
// pieces on its tbox bits from 0; tables 2-7 a container each
// (mDungeonItem bit 4).

var m4Fixture = sync.OnceValues(func() (*tables.Tables, error) {
	img := tablestest.ISO(tablestest.Vanilla())
	d, err := inject.NewDisc(bytes.NewReader(img), int64(len(img)))
	if err != nil {
		return nil, err
	}
	return tables.Build(d)
})

func m4Tables(t *testing.T) *tables.Tables {
	t.Helper()
	tb, err := m4Fixture()
	if err != nil {
		t.Fatal(err)
	}
	if !tb.Keys.Pass || !tb.Hearts.Pass {
		t.Fatalf("the fixture's tables fail: keys %v, hearts %v", tb.Keys.Pass, tb.Hearts.Pass)
	}
	return tb
}

// m4Policy is a session's policy over tb's switch verdict.
func m4Policy(tb *tables.Tables) *world.Policy { return hub.SessionPolicy(memorySwitches(tb)) }

// memorySwitches is tb's verdict on the memory switches
// (synctables.SyncedMemorySwitch), as hub.Config.SyncedSwitch takes it.
func memorySwitches(tb *tables.Tables) func(table, no int) bool {
	return func(table, no int) bool { return synctables.SyncedMemorySwitch(tb, table, no) }
}

// scopedSwitches is tb's verdict on the switches SCOPED carries
// (synctables.SyncedScopedSwitch), as hub.Config.SyncedScoped takes it.
func scopedSwitches(tb *tables.Tables) func(table int, stage string, room, no int) bool {
	return func(table int, stage string, room, no int) bool {
		return synctables.SyncedScopedSwitch(tb, table, stage, room, no)
	}
}

// drcKeys is Dragon Roost Cavern in its key chests' room.
var drcKeys = fakegame.Stage{Name: "M_NewD2", Room: 1, Table: addr.TableDRC, KeyHUD: true}

// m4Settle is enough ticks for a derive to settle (a second), be made and
// drain, and for the gates to settle after it.
const m4Settle = 3 * gate.StableTicks * 4

// changeStage is a stage change to s as the game makes it: mNextStage set
// for a few ticks, the first of which is the transition tick, then the
// new stage, settled. It returns the transition tick's number.
func (rr *recRig) changeStage(s fakegame.Stage) int {
	rr.t.Helper()
	if err := rr.g.SetNextStage(s); err != nil {
		rr.t.Fatal(err)
	}
	rr.tick()
	tr := len(rr.reps) - 1
	if !rr.reps[tr].Gates.OnTransition {
		rr.t.Fatalf("tick %d is no transition tick: %s", tr, rr.reps[tr].Gates.Why())
	}
	rr.ticks(3)
	if err := rr.g.ChangeStage(); err != nil {
		rr.t.Fatal(err)
	}
	rr.ticks(gate.StableTicks + 2)
	return tr
}

// writesSince is what the replica wrote since n writes.
func (rr *recRig) writesSince(n int) []auditWrite { return rr.bm.written()[n:] }

func (rr *recRig) auditM4(liveSwitches bool) {
	rr.t.Helper()
	auditM4Writes(rr.t, rr.bm.written(), rr.reps, rr.bm.placesSeen(), liveSwitches, rr.r.Status().Writes.Derived)
}

// auditM4Writes is M4's write audit (§9 M4) over every write a replica
// made, derived being the writes its derive path reports made: each is
// inside M3's or M4's allowlist (so no PER_PLAYER, NEVER or TAG byte, and
// no DERIVED byte but the derive targets'), made on a tick whose
// HEARTBEAT was open, and by its target:
//
//   - M3's stage progress, switches and ocean as auditWrites has them;
//   - a story bit or register: one byte, on the first tick of a stage
//     change (the game read mNextStage.mEnable 1 as the tick began and 0
//     as the one before did) with OnTransition; a story bit never
//     cleared, a register field only raised, the byte's other bits kept;
//   - an inventory flag or capacity: one byte, when IDLE; a flag never
//     cleared, a capacity only raised;
//   - a pending counter: its two bytes at once, when IDLE, over 0, with
//     a count;
//   - a slot, the equipment, a button or a key byte: one byte, when
//     IDLE; an item only ever replaced by another, a key byte 0..99.
//
// The writes at the derive targets (the DERIVED bytes, the counters and
// the buttons) are exactly the derive path's: no other path wrote one.
func auditM4Writes(t *testing.T, ws []auditWrite, reps []Report, places []gamePlace, liveSwitches bool, derived int) {
	t.Helper()
	if len(places) != len(reps) {
		t.Fatalf("%d ticks reported, %d places recorded", len(reps), len(places))
	}
	g := NewGuardFor(M3Targets | M4Targets)
	var m3 []auditWrite
	viaDerive := 0
	for _, w := range ws {
		rep := reps[w.tick]
		at := func(format string, args ...any) {
			t.Helper()
			t.Fatalf("tick %d, %d-byte write at 0x%08X (% X -> % X): "+format,
				append([]any{w.tick, w.n, w.a, w.prev, w.data}, args...)...)
		}
		tg := g.TargetOf(w.a)
		switch {
		case !g.Allows(w.a, w.n):
			at("outside M4's allowlist")
		case !rep.Gates.Heartbeat:
			at("HEARTBEAT was closed")
		}
		image := w.a >= addr.SaveImage && w.a < addr.SaveImage+addr.SaveImageSize
		off := int(w.a - addr.SaveImage)
		if image {
			switch c := class.Of(off).Class; {
			case c == class.PerPlayer, c == class.Never, c == class.Tag:
				at("a %v byte", c)
			case c == class.Derived && tg != TargetDerived:
				at("a DERIVED byte outside the derive targets")
			}
		}
		idle := func() {
			t.Helper()
			if !rep.Gates.CanWriteIdle() {
				at("not IDLE: %s", rep.Gates.Why())
			}
		}
		if tg != TargetCounters && w.n != 1 {
			at("writes outside the counters are bytes")
		}
		switch tg {
		case TargetStage, TargetOcean:
			m3 = append(m3, w)
		case TargetStory:
			if first := places[w.tick].pending && (w.tick == 0 || !places[w.tick-1].pending); !first || !rep.Gates.OnTransition {
				at("a story write off a stage change's first tick")
			}
			if story.IsRegisterByte(off) {
				if story.ApplyRegister(off-addr.OffEvent, w.old, w.val) != w.val || w.old == w.val {
					at("a register write that is no raise")
				}
			} else if w.old&^w.val != 0 || w.old == w.val {
				at("a story write that clears, or sets nothing")
			}
		case TargetInventory:
			idle()
			if class.Of(off).Class == class.Max {
				if w.val <= w.old {
					at("a capacity that does not rise")
				}
			} else if w.old&^w.val != 0 || w.old == w.val {
				at("an inventory flag write that clears, or sets nothing")
			}
		case TargetCounters:
			viaDerive++
			idle()
			switch {
			case w.n != 2 || w.a != counterAddr(counterAt(w.a)):
				at("a counter is written whole")
			case !bytes.Equal(w.prev, []byte{0, 0}):
				at("a counter written over a pending count")
			case bytes.Equal(w.data, []byte{0, 0}):
				at("a counter written with no count")
			}
		case TargetDerived, TargetSelectItem:
			viaDerive++
			idle()
			isKey := w.a == addr.LiveKeyNum || image && (off-addr.OffMemory)%addr.MemBitSize == addr.MemBitOffKeyNum && off >= addr.OffMemory
			switch {
			case isKey && w.val > derive.MaxKeys:
				at("a key byte above %d", derive.MaxKeys)
			case !isKey && (w.val == save.ItemNone || w.val == w.old):
				at("an item write that empties a slot, or changes nothing")
			}
		default:
			at("target %v", tg)
		}
	}
	if viaDerive != derived {
		t.Fatalf("%d writes at the derive targets, %d made by the derive path", viaDerive, derived)
	}
	auditWrites(t, m3, reps, places, liveSwitches)
}

// TestReconcileM4StoryOnTransition: the world's story bit and register
// are not written on a settled tick, in an event, after it or at a room
// change, only on the first tick of a stage change, into the saved
// dSv_event_c, and once: a bit the game later clears is not set again.
func TestReconcileM4StoryOnTransition(t *testing.T) {
	rr := newRecRig(t, Config{Apply: true, Policy: hub.SessionPolicy(nil)}, hub.FeatureImage|hub.FeatureStory)
	off, m := syncedStory(t)
	reg := registerOff(t)
	rm := class.Of(reg).Mask
	var img world.Image
	img[off], img[reg] = m, rm
	rr.world(img)
	rr.ticks(gate.StableTicks + 2)
	rr.g.SetEvent(1, 0)
	rr.ticks(gate.StableTicks + 2)
	rr.g.SetEvent(0, 0)
	rr.ticks(gate.StableTicks + 2)
	rr.enter(5)
	rr.ticks(gate.StableTicks + 2)
	if ws := rr.bm.written(); len(ws) != 0 {
		t.Fatalf("written before a stage change: %+v", ws)
	}

	tr := rr.changeStage(drc)
	ws := rr.bm.written()
	if len(ws) != 2 {
		t.Fatalf("the stage change wrote %+v, want the story byte and the register", ws)
	}
	for _, w := range ws {
		if w.tick != tr {
			t.Fatalf("a write on tick %d, the transition is tick %d", w.tick, tr)
		}
	}
	if rr.savedByte(off)&m != m || rr.savedByte(reg)&rm != rm {
		t.Fatalf("saved story 0x%02X, register 0x%02X", rr.savedByte(off), rr.savedByte(reg))
	}
	if !rr.log.has("a stage change starts: writing 1 story bits and 1 registers") {
		t.Fatalf("log:\n%s", rr.log)
	}
	rr.noGainsUp("after the stage change")

	// Apply-once: the game clears the bit; the next stage change leaves it.
	rr.clear(off, m)
	n := len(rr.bm.written())
	rr.ticks(gate.StableTicks + 2)
	rr.changeStage(outset)
	if ws := rr.writesSince(n); len(ws) != 0 {
		t.Fatalf("rewrote %+v", ws)
	}
	rr.auditM4(false)
}

// TestReconcileM4HeldStoryBits: §6.5's holds keep their bits out of the
// game until their condition holds there: 0x0801 until the receiver has
// MET_KORL of its own, HYRULE_COURTYARD_CUTSCENE until ZELDA_AWAKENED,
// which may come in the same write, and 0x2D10 until a Master Sword is
// equipped. Each then goes at the next stage change.
func TestReconcileM4HeldStoryBits(t *testing.T) {
	rr := newRecRig(t, Config{Apply: true, Policy: hub.SessionPolicy(nil)}, hub.FeatureImage|hub.FeatureStory)
	o0801, m0801 := eventBit(ev0801)
	oCourt, mCourt := eventBit(evHyruleCourtyard)
	oZelda, mZelda := eventBit(evZeldaAwakened)
	o2D10, m2D10 := eventBit(ev2D10)
	var img world.Image
	img[o0801] |= m0801
	img[oCourt] |= mCourt
	img[o2D10] |= m2D10
	rr.world(img)
	rr.ticks(gate.StableTicks + 2)
	rr.changeStage(drc)
	if ws := rr.bm.written(); len(ws) != 0 {
		t.Fatalf("held bits written: %+v", ws)
	}
	if !rr.log.has("holding") {
		t.Fatalf("no hold logged:\n%s", rr.log)
	}

	// The receiver meets KORL itself, and the world gains ZELDA_AWAKENED.
	rr.g.OnEventBit(evMetKorl)
	img[oZelda] |= mZelda
	rr.world(img)
	rr.ticks(gate.StableTicks + 2)
	tr := rr.changeStage(outset)
	for _, c := range []struct {
		name string
		off  int
		m    byte
		want bool
	}{
		{"UNK_0801", o0801, m0801, true},
		{"HYRULE_COURTYARD_CUTSCENE", oCourt, mCourt, true},
		{"ZELDA_AWAKENED", oZelda, mZelda, true},
		{"UNK_2D10", o2D10, m2D10, false},
	} {
		if got := rr.savedByte(c.off)&c.m != 0; got != c.want {
			t.Errorf("%s set %v, want %v", c.name, got, c.want)
		}
	}
	for _, w := range rr.bm.written() {
		if w.tick != tr {
			t.Fatalf("a write on tick %d, the transition is tick %d", w.tick, tr)
		}
	}

	// A Master Sword equipped: 0x2D10 at the next stage change.
	rr.put(addr.OffSelectEquip, class.MasterSwords()[0])
	rr.ticks(gate.StableTicks + 2)
	rr.changeStage(drc)
	if rr.savedByte(o2D10)&m2D10 == 0 {
		t.Fatalf("UNK_2D10 still held:\n%s", rr.log)
	}
	rr.auditM4(false)
}

// TestReconcileM4ClassesNotGranted: a class the hub does not grant is not
// written, and the log says so once.
func TestReconcileM4ClassesNotGranted(t *testing.T) {
	off, m := syncedStory(t)
	cases := []struct {
		name  string
		f     hub.Feature
		world func(img *world.Image)
		log   string
	}{
		{"story", hub.FeatureImage | hub.FeatureInventory | hub.FeatureMagic,
			func(img *world.Image) { img[off] |= m },
			"the world's story is not written to this game: the session does not grant story"},
		{"inventory", hub.FeatureImage | hub.FeatureStory | hub.FeatureMagic,
			func(img *world.Image) { img[addr.OffGetItem+save.SlotTelescope] = 0x01 },
			"the world's inventory is not written to this game: the session does not grant inventory"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := newRecRig(t, Config{Apply: true, Policy: hub.SessionPolicy(nil)}, c.f)
			var img world.Image
			c.world(&img)
			rr.world(img)
			rr.ticks(gate.StableTicks + 2)
			rr.changeStage(drc)
			rr.ticks(gate.StableTicks + 2)
			if ws := rr.bm.written(); len(ws) != 0 {
				t.Fatalf("wrote %+v", ws)
			}
			if !rr.log.has(c.log) {
				t.Fatalf("log lacks %q:\n%s", c.log, rr.log)
			}
		})
	}

	// Max magic without FeatureMagic: the Deku Leaf is the receiver's
	// (inventory), its magic is not.
	rr := newRecRig(t, Config{Apply: true, Policy: hub.SessionPolicy(nil)}, hub.FeatureImage|hub.FeatureInventory)
	var img world.Image
	img[addr.OffGetItem+save.SlotDekuLeaf] = 0x01
	rr.world(img)
	rr.ticks(m4Settle)
	if rr.savedByte(addr.OffItems+save.SlotDekuLeaf) != save.ItemDekuLeaf || rr.read(addr.MaxMagic, 1)[0] != 0 {
		t.Fatalf("slot 0x%02X, max magic %d", rr.savedByte(addr.OffItems+save.SlotDekuLeaf), rr.read(addr.MaxMagic, 1)[0])
	}
	if !rr.log.has("the world's max magic is not written to this game: the session does not grant magic") {
		t.Fatalf("log:\n%s", rr.log)
	}
	rr.auditM4(false)
}

// TestReconcileM4Inventory: the world's bow, quiver and telescope reach
// the game when IDLE, not during an event nor while a stage change is
// pending (no transition write), each flag and capacity by the merge path
// and each slot by the derive path; a larger capacity of the receiver's
// is never lowered.
func TestReconcileM4Inventory(t *testing.T) {
	rr := newRecRig(t, Config{Apply: true, Policy: hub.SessionPolicy(nil)}, hub.FeatureImage|hub.FeatureInventory)
	bow, tele := addr.OffGetItem+save.SlotBow, addr.OffGetItem+save.SlotTelescope
	var img world.Image
	img[bow], img[addr.OffArrowMax], img[tele] = 0x01, 30, 0x01
	rr.g.SetEvent(1, 0)
	rr.world(img)
	rr.ticks(gate.StableTicks + 2)
	if ws := rr.bm.written(); len(ws) != 0 {
		t.Fatalf("written during an event: %+v", ws)
	}
	rr.g.SetEvent(0, 0)
	if rr.untilWrite(gate.StableTicks+4) < 0 {
		t.Fatalf("nothing written once IDLE:\n%s", rr.log)
	}
	rr.ticks(gate.StableTicks + 2)
	for _, c := range []struct {
		name      string
		off       int
		got, want byte
	}{
		{"the bow's flag", bow, rr.savedByte(bow), 0x01},
		{"the arrow capacity", addr.OffArrowMax, rr.savedByte(addr.OffArrowMax), 30},
		{"the bow's slot", addr.OffItems + save.SlotBow, rr.savedByte(addr.OffItems + save.SlotBow), save.ItemBow},
		{"the telescope's slot", addr.OffItems + save.SlotTelescope, rr.savedByte(addr.OffItems + save.SlotTelescope), save.ItemTelescope},
	} {
		if c.got != c.want {
			t.Errorf("%s: 0x%02X, want 0x%02X", c.name, c.got, c.want)
		}
	}
	if st := rr.r.Status(); st.Writes.Derived != 2 {
		t.Errorf("the derive path made %d writes, want the two slots", st.Writes.Derived)
	}

	// The world's wallet arrives as a stage change starts: it waits for
	// the new stage.
	n := len(rr.bm.written())
	if err := rr.g.SetNextStage(drc); err != nil {
		t.Fatal(err)
	}
	img[addr.OffWalletSize] = 1
	rr.world(img)
	rr.ticks(4)
	if ws := rr.writesSince(n); len(ws) != 0 {
		t.Fatalf("written while a stage change is pending: %+v", ws)
	}
	if err := rr.g.ChangeStage(); err != nil {
		t.Fatal(err)
	}
	rr.ticks(gate.StableTicks + 4)
	if rr.savedByte(addr.OffWalletSize) != 1 {
		t.Fatalf("wallet %d", rr.savedByte(addr.OffWalletSize))
	}

	// A larger quiver of the receiver's own stays.
	rr.put(addr.OffArrowMax, 99)
	n = len(rr.bm.written())
	rr.ticks(gate.StableTicks + 4)
	if rr.savedByte(addr.OffArrowMax) != 99 || len(rr.writesSince(n)) != 0 {
		t.Fatalf("arrow capacity %d after %+v", rr.savedByte(addr.OffArrowMax), rr.writesSince(n))
	}
	rr.auditM4(false)
}

// TestReconcileM4GrantNeverLowersACapacity: the world's bow alone (its
// grant's floor is 30 arrows) reaches a game whose quiver already holds
// 60: the flag and the slot are written, the capacity is not lowered.
func TestReconcileM4GrantNeverLowersACapacity(t *testing.T) {
	rr := newRecRig(t, Config{Apply: true, Policy: hub.SessionPolicy(nil)}, hub.FeatureImage|hub.FeatureInventory)
	rr.put(addr.OffArrowMax, 60)
	bow := addr.OffGetItem + save.SlotBow
	var img world.Image
	img[bow] = 0x01
	rr.world(img)
	rr.ticks(gate.StableTicks + 4)
	if rr.savedByte(bow) != 0x01 || rr.savedByte(addr.OffItems+save.SlotBow) != save.ItemBow {
		t.Fatalf("flag 0x%02X, slot 0x%02X", rr.savedByte(bow), rr.savedByte(addr.OffItems+save.SlotBow))
	}
	if got := rr.savedByte(addr.OffArrowMax); got != 60 {
		t.Fatalf("arrow capacity %d, want 60", got)
	}
	for _, w := range rr.bm.written() {
		if w.a == imageAddr(addr.OffArrowMax) {
			t.Fatalf("the capacity was written: %+v", w)
		}
	}
	rr.auditM4(false)
}

// TestReconcileM4MaxMagic: the world's Deku Leaf reaches the game
// (inventory), and from its flag the derive gives the 16 magic through
// mItemMaxMagicCount, which the HUD takes; the watch sees it drained.
func TestReconcileM4MaxMagic(t *testing.T) {
	rr := newRecRig(t, Config{Apply: true, Policy: hub.SessionPolicy(nil)},
		hub.FeatureImage|hub.FeatureInventory|hub.FeatureMagic)
	var img world.Image
	img[addr.OffGetItem+save.SlotDekuLeaf] = 0x01
	rr.world(img)
	rr.ticks(m4Settle)
	if got := rr.read(addr.MaxMagic, 1)[0]; got != derive.MagicLeaf {
		t.Fatalf("max magic %d, want %d:\n%s", got, derive.MagicLeaf, rr.log)
	}
	st := rr.r.Status()
	if st.Writes.Drained != 1 || st.WritesOff != "" {
		t.Fatalf("writes %+v, off %q", st.Writes, st.WritesOff)
	}
	if !rr.log.has("the derive path wrote") {
		t.Fatalf("log:\n%s", rr.log)
	}
	rr.auditM4(false)
}

// TestReconcileM4MaxLife: the partner's three sea heart pieces and DRC's
// heart container reach the receiver's flags (M3's stage progress), and
// the derive raises max life to 12 + pieces + 4 * containers quarter
// hearts.
func TestReconcileM4MaxLife(t *testing.T) {
	tb := m4Tables(t)
	rr := newRecRig(t, Config{Apply: true, Tables: tb, Policy: m4Policy(tb)}, hub.FeatureImage|hub.FeatureLife)
	var img world.Image
	for no := range 3 {
		off, m := chest(addr.TableSea, no)
		img[off] |= m
	}
	img[addr.OffMemBit(addr.TableDRC)+addr.MemBitOffDungeonItem] |= 1 << addr.DungeonItemStageLife
	rr.world(img)
	rr.ticks(m4Settle)
	if got, want := int(rr.u16(addr.MaxLife)), derive.StartLife+3+4; got != want {
		t.Fatalf("max life %d, want %d:\n%s", got, want, rr.log)
	}
	if st := rr.r.Status(); st.Writes.Drained == 0 {
		t.Fatalf("writes %+v", st.Writes)
	}
	rr.auditM4(false)
}

func (r *rig) u16(a uint32) uint16 {
	b := r.read(a, 2)
	return uint16(b[0])<<8 | uint16(b[1])
}

// keyFormula is keys(DRC) over the game's own live flags, DRC loaded: key
// chests opened less key doors open (§3).
func (r *rig) keyFormula() int {
	r.t.Helper()
	n := 0
	for no := 1; no <= 4; no++ {
		if off, m := chest(addr.TableDRC, no); r.liveByte(off)&m != 0 {
			n++
		}
	}
	for sw := 0x10; sw <= 0x13; sw++ {
		if off, m := switchImageByte(addr.TableDRC, sw); r.liveByte(off)&m != 0 {
			n--
		}
	}
	return n
}

// TestReconcileM4Keys runs §8.1's key scenarios through the reconciler:
// the partner's chests and doors reach the receiver's flags by M3's
// writes (a door's switch at the receiver's next room change), and the
// derive brings mKeyNum to the formula over them, never owing a key to a
// door that still locks.
func TestReconcileM4Keys(t *testing.T) {
	tb := m4Tables(t)
	keyRig := func(t *testing.T) *recRig {
		rr := newRecRig(t, Config{Apply: true, Tables: tb, Policy: m4Policy(tb)}, hub.FeatureImage|hub.FeatureSwitches|hub.FeatureKeys)
		rr.load(drcKeys)
		rr.ticks(m4Settle)
		return rr
	}
	keys := func(rr *recRig) int { return int(rr.read(addr.LiveKeyNum, 1)[0]) }
	want := func(rr *recRig, n int) {
		rr.t.Helper()
		if k, f := keys(rr), rr.keyFormula(); k != n || f != n {
			rr.t.Fatalf("%d keys, the formula %d; want %d\n%s", k, f, n, rr.log)
		}
	}
	world := func(rr *recRig, tboxes []int, doors []int) {
		var img world.Image
		for _, no := range tboxes {
			off, m := chest(addr.TableDRC, no)
			img[off] |= m
		}
		for _, sw := range doors {
			off, m := switchImageByte(addr.TableDRC, sw)
			img[off] |= m
		}
		rr.world(img)
	}
	open := func(rr *recRig, no int) {
		if err := rr.g.OpenKeyChest(no); err != nil {
			rr.t.Fatal(err)
		}
	}

	t.Run("a remote key chest", func(t *testing.T) {
		rr := keyRig(t)
		world(rr, []int{1}, nil)
		rr.ticks(m4Settle)
		want(rr, 1)
		rr.auditM4(false)
	})
	t.Run("the same key twice", func(t *testing.T) {
		rr := keyRig(t)
		open(rr, 2)
		rr.ticks(m4Settle)
		world(rr, []int{2}, nil) // the partner's, the same chest
		rr.ticks(m4Settle)
		want(rr, 1)
		if d := rr.r.Status().Writes.Derived; d != 0 {
			t.Fatalf("%d derive writes, want none: one flag, one key", d)
		}
		rr.auditM4(false)
	})
	t.Run("a door opened remotely, coupled to its switch", func(t *testing.T) {
		// Door 0x10, between rooms 0 and 1, reads its switch only after
		// Link's room changes (fakegame.KeyDoor, as daDoor10_c). Every
		// tick: it never shows its lock over a set switch next to Link
		// while the game holds no key, which would leave no way through
		// (the owner's F13).
		rr := keyRig(t)
		door := &fakegame.KeyDoor{Stage: drcKeys.Name, Front: 0, Back: 1, No: 0x10}
		rr.bm.setActors(door)
		step := func(n int) {
			t.Helper()
			for range n {
				rr.tick()
				stale, err := door.StaleLock(rr.g)
				if err != nil {
					t.Fatal(err)
				}
				if stale && keys(rr) == 0 && rr.u16(addr.ItemKeyNumCount) == 0 {
					t.Fatalf("door 0x10 shows its lock over its set switch next to Link, and the key is gone: no way through\n%s", rr.log)
				}
			}
		}
		open(rr, 1)
		step(m4Settle)
		// The partner used the same key on door 0x10. Its switch waits for
		// the receiver's next room change (and the room hold), and so does
		// the key: the door still locks here.
		world(rr, []int{1}, []int{0x10})
		step(m4Settle)
		want(rr, 1)
		// Link goes into room 0, the door's other room: it reads its
		// switch, still clear, and keeps its lock. The switch is written
		// after the room hold, and the lock stays until Link's next room
		// change, and so does the key.
		rr.enter(0, 1)
		step(m4Settle + rr.holdTicks())
		off, m := switchImageByte(addr.TableDRC, 0x10)
		if rr.liveByte(off)&m == 0 || !door.Locked() || !door.AtLink() || keys(rr) != 1 {
			t.Fatalf("switch %v, door locked %v next to Link %v, %d keys: want the switch written, the door locked and the key kept\n%s",
				rr.liveByte(off)&m != 0, door.Locked(), door.AtLink(), keys(rr), rr.log)
		}
		if !rr.log.has("small keys, table 3 (DRC): door 0x10 is counted after your next room change") {
			t.Errorf("no held door logged:\n%s", rr.log)
		}
		if k := rr.r.Status().Derive.Keys; !slices.ContainsFunc(k, func(k derive.KeyStatus) bool { return slices.Equal(k.Held, []int{0x10}) }) {
			t.Errorf("status %+v: want door 0x10 held", k)
		}
		rr.enter(1, 0)
		step(m4Settle)
		want(rr, 0)
		if door.Locked() {
			t.Fatal("the door kept its lock after the room change")
		}
		if !rr.log.has("door 0x10 counted: your room changed") {
			t.Errorf("no counted door logged:\n%s", rr.log)
		}
		rr.auditM4(false)
	})
	// The receiver opens the held door with the key it kept: keyInit takes
	// it, and nothing gives it back. In the game Link then goes through,
	// and the door is counted at that room change ("through it"); with no
	// room change after it (a door whose rooms are one), the key the count
	// lost counts it.
	for _, through := range []bool{false, true} {
		name, why := "a key spent on a door held", "door 0x10 counted: this game spent a key on it"
		if through {
			name, why = name+", then through it", "door 0x10 counted: your room changed"
		}
		t.Run(name, func(t *testing.T) {
			rr := keyRig(t)
			door := &fakegame.KeyDoor{Stage: drcKeys.Name, Front: 0, Back: 1, No: 0x10}
			rr.bm.setActors(door)
			open(rr, 1)
			rr.ticks(m4Settle)
			world(rr, []int{1}, []int{0x10})
			rr.enter(0, 1)
			rr.ticks(m4Settle + rr.holdTicks())
			// The door still shows its lock, and Link is in its front room.
			if err := door.Open(rr.g); err != nil {
				t.Fatal(err)
			}
			if through {
				rr.enter(1, 0)
			}
			rr.ticks(m4Settle)
			want(rr, 0)
			if d := rr.r.Status().Writes.Derived; d != 0 {
				t.Fatalf("%d derive writes, want none: the key went with the door", d)
			}
			if !rr.log.has(why) {
				t.Errorf("no %q logged:\n%s", why, rr.log)
			}
			rr.auditM4(false)
		})
	}
	t.Run("a key spent on a door the partner opened", func(t *testing.T) {
		rr := keyRig(t)
		open(rr, 1)
		rr.ticks(m4Settle)
		if err := rr.g.UnlockKeyDoor(0x10); err != nil {
			t.Fatal(err)
		}
		world(rr, []int{1}, []int{0x10})
		rr.enter(2, 1)
		rr.ticks(m4Settle)
		want(rr, 0)
		if d := rr.r.Status().Writes.Derived; d != 0 {
			t.Fatalf("%d derive writes, want none: the door counts once", d)
		}
		rr.auditM4(false)
	})
}

// TestReconcileM4DryRun: under --coop-dry-run the M4 classes are planned
// and logged, whatever the hub grants, and nothing is written.
func TestReconcileM4DryRun(t *testing.T) {
	tb := m4Tables(t)
	rr := newRecRig(t, Config{Tables: tb, Policy: m4Policy(tb)}, hub.FeatureImage)
	off, m := syncedStory(t)
	tbox, tm := chest(addr.TableDRC, 1)
	var img world.Image
	img[off], img[addr.OffGetItem+save.SlotBow], img[addr.OffArrowMax], img[tbox] = m, 0x01, 30, tm
	rr.world(img)
	rr.load(drcKeys)
	rr.ticks(gate.StableTicks + 2)
	// The receiver's own Deku Leaf, its magic not yet counted in.
	rr.set(addr.OffGetItem+save.SlotDekuLeaf, 0x01)
	rr.ticks(m4Settle)
	rr.changeStage(outset)
	rr.ticks(gate.StableTicks + 2)
	if ws := rr.bm.written(); len(ws) != 0 {
		t.Fatalf("the dry run wrote %+v", ws)
	}
	for _, rep := range rr.reps {
		if rep.Wrote != 0 || rep.WriteErr != nil {
			t.Fatalf("a dry-run tick reports writes: %+v", rep)
		}
	}
	for _, line := range []string{
		"dry-run: a stage change starts: 1 story bits and 0 registers of the world would be written now",
		"dry-run: would write slot",
		"dry-run: would write max-magic counter",
		"dry-run: would set",
	} {
		if !rr.log.has(line) {
			t.Errorf("log lacks %q", line)
		}
	}
	if t.Failed() {
		t.Logf("log:\n%s", rr.log)
	}
}
