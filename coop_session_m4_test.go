package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/derive"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/story"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/keys"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/keys/itemsrc"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/tablestest"
)

// M4 through the session plumbing (§9 M4): shared progression between
// two writing replicas of a --coop session, the story bits, the
// inventory and the derived small keys, max life and max magic, with the
// write audit over every byte the pipelines wrote.
//
// The fixture is tablestest's vanilla-shaped disc: Dragon Roost Cavern
// (save table 3, stage M_NewD2) has four key chests, tbox bits 1-4 in
// room 1, and four key doors, memory switches 0x10-0x13; the sea (table
// 0) has its heart pieces in chests on its tbox bits from 0.

// coDRCKeys is Dragon Roost Cavern in its key chests' room.
var coDRCKeys = fakegame.Stage{Name: "M_NewD2", Room: 1, Table: addr.TableDRC, KeyHUD: true}

// The fixture's key chests and doors of DRC.
var (
	coKeyChests = []int{1, 2, 3, 4}
	coKeyDoors  = []int{0x10, 0x11, 0x12, 0x13}
)

// coDRCDoors are DRC's key doors as a game's actors (fakegame.KeyDoor),
// door i between rooms i and i+1 as tablestest.Dungeon places them.
func coDRCDoors() []*fakegame.KeyDoor {
	var ds []*fakegame.KeyDoor
	for i, sw := range coKeyDoors {
		ds = append(ds, &fakegame.KeyDoor{Stage: coDRCKeys.Name, Front: i, Back: i + 1, No: sw})
	}
	return ds
}

// door is p's key door on switch sw.
func (p *coPlayer) door(sw int) *fakegame.KeyDoor {
	p.t.Helper()
	for _, d := range p.beat.doors {
		if d.No == sw {
			return d
		}
	}
	p.t.Fatalf("%s has no key door 0x%02X", p.name, sw)
	return nil
}

// heldDoors are the doors p's derive holds: open, counted only after its
// next room change (derive.KeyStatus.Held).
func (p *coPlayer) heldDoors() []int {
	var sw []int
	for _, k := range p.s.r.Status().Derive.Keys {
		sw = append(sw, k.Held...)
	}
	return sw
}

// coStuck is how many ticks a key door may stay shut to its player before
// TestCoopSessionM4 fails: a little over the derive's in-flight time, so
// that a key on its way (a chest's, counted in once settled) is no
// failure.
const coStuck = 30

// stuckAt says how a key door of p's is shut to it, or "": next to Link
// it shows the lock it read before its switch was set, while the game
// holds no key and none is pending, so no key opens it and only a room
// change would (the owner's F13).
func (p *coPlayer) stuckAt() string {
	if p.keys() != 0 || !p.counters() {
		return ""
	}
	for _, d := range p.beat.doors {
		stale, err := d.StaleLock(p.g)
		if err != nil {
			p.t.Fatal(err)
		}
		if stale {
			return fmt.Sprintf("door 0x%02X shows its lock over its set switch next to Link, with no key to open it", d.No)
		}
	}
	return ""
}

// The holds' event bits the test plays (class.Holds): MET_KORL is never
// written into a game without it, UNK_0801 only once the game has
// MET_KORL of its own.
const (
	coMetKorl = 0x0F80
	co0801    = 0x0801
)

// coDeriveTargets are the WriteGuard targets only the derive path writes.
const coDeriveTargets = replica.TargetDerived | replica.TargetCounters | replica.TargetSelectItem

// coAuditM4Write checks write w at M4 target tg by M4's rules (§9 M4),
// against the tick's report rep and where the game was as each tick
// began, and returns the OR bits it set that must not be set again at the
// same place (story bits and inventory flags; 0 for the rest):
//
//   - no PER_PLAYER, NEVER or TAG byte, and a DERIVED byte only at the
//     derive targets; ARMED, with HEARTBEAT open;
//   - a story bit or register: one byte, on the first tick of a stage
//     change (mNextStage.mEnable read 1 as the tick began, 0 as the one
//     before did) with OnTransition; a story bit never cleared, a
//     register only raised;
//   - an inventory flag or capacity: one byte, when IDLE; a flag never
//     cleared, a capacity only raised;
//   - a pending counter: both bytes at once, when IDLE, over 0, with a
//     count;
//   - a slot, the equipment, a button or a key byte: one byte, when
//     IDLE; an item never emptied, a key byte 0..99.
func coAuditM4Write(w coWrite, tg replica.Target, rep replica.Report, places []coPlace, bad func(format string, args ...any)) (set byte) {
	image := w.a >= addr.SaveImage && w.a < addr.SaveImage+addr.SaveImageSize
	off := int(w.a - addr.SaveImage)
	if image {
		switch c := class.Of(off).Class; {
		case c == class.PerPlayer, c == class.Never, c == class.Tag:
			bad("a %v byte", c)
		case c == class.Derived && tg&coDeriveTargets == 0:
			bad("a DERIVED byte off the derive path's targets")
		}
	}
	switch {
	case rep.State != replica.StateArmed:
		bad("M4 while %s", rep.State)
	case !rep.Gates.Heartbeat:
		bad("M4 with HEARTBEAT closed")
	}
	if tg != replica.TargetCounters && len(w.val) != 1 {
		bad("%d bytes: only a pending counter is written whole", len(w.val))
		return 0
	}
	idle := func() {
		if !rep.Gates.CanWriteIdle() {
			bad("not IDLE: %s", rep.Gates.Why())
		}
	}
	switch tg {
	case replica.TargetStory:
		if first := places[w.tick].pending && (w.tick == 0 || !places[w.tick-1].pending); !first || !rep.Gates.OnTransition {
			bad("a story write off a stage change's first tick")
		}
		old, val := w.old[0], w.val[0]
		if story.IsRegisterByte(off) {
			if story.ApplyRegister(off-addr.OffEvent, old, val) != val || old == val {
				bad("a register write that is no raise")
			}
			if class.Of(off).Class == class.Max {
				return 0
			}
		} else if old&^val != 0 || old == val {
			bad("a story write that clears, or sets nothing")
		}
		return val &^ old
	case replica.TargetInventory:
		idle()
		old, val := w.old[0], w.val[0]
		if r := class.Of(off); r.Class == class.Max {
			if val&r.Mask <= old&r.Mask || val&^r.Mask != old&^r.Mask {
				bad("a capacity that does not rise, or other bits changed")
			}
			return 0
		}
		if old&^val != 0 || old == val {
			bad("an inventory flag write that clears, or sets nothing")
		}
		return val &^ old
	case replica.TargetCounters:
		idle()
		switch {
		case len(w.val) != 2 || !coCounter(w.a):
			bad("a counter is written whole")
		case !bytes.Equal(w.old, []byte{0, 0}):
			bad("a counter written over a pending count")
		case bytes.Equal(w.val, []byte{0, 0}):
			bad("a counter written with no count")
		}
	case replica.TargetDerived, replica.TargetSelectItem:
		idle()
		isKey := w.a == addr.LiveKeyNum || image && off >= addr.OffMemory && off < addr.OffOcean &&
			(off-addr.OffMemory)%addr.MemBitSize == addr.MemBitOffKeyNum
		switch {
		case isKey && w.val[0] > derive.MaxKeys:
			bad("a key byte above %d", derive.MaxKeys)
		case !isKey && (w.val[0] == save.ItemNone || w.val[0] == w.old[0]):
			bad("an item write that empties a slot, or changes nothing")
		}
	default:
		bad("M4 target %v", tg)
	}
	return 0
}

// coCounter reports whether a is a pending counter's first byte.
func coCounter(a uint32) bool {
	return a == addr.ItemKeyNumCount || a == addr.ItemMaxLifeCount || a == addr.ItemMaxMagicCount
}

// coFlagBit is where source flag f is saved in the image, for the flags
// the fixture's key and heart sources use.
func coFlagBit(f itemsrc.Flag) (off int, mask byte, ok bool) {
	if !addr.ValidTable(f.Table) {
		return 0, 0, false
	}
	var b addr.Bit
	var err error
	switch f.Kind {
	case itemsrc.TboxFlag:
		b, err = addr.TboxBit(f.Bit)
	case itemsrc.ItemFlag:
		b, err = addr.MemBitItemBit(f.Bit)
	case itemsrc.StageLifeFlag:
		b, err = addr.DungeonItemBit(addr.DungeonItemStageLife)
	default:
		return 0, 0, false
	}
	if err != nil {
		return 0, 0, false
	}
	return addr.OffMemBit(f.Table) + b.Off, b.Mask, true
}

// coM4 is what the M4 test plays with, from the session's tables and
// policy.
type coM4 struct {
	tb     *tables.Tables
	pol    *world.Policy
	story  []uint16 // story bits of the SyncMask no hold names, by event ID
	regs   []int    // register bytes the policy opens
	pieces []int    // the sea's heart-piece chests, by tbox number
	// derived is the key sources' and doors' bits and the heart
	// sources': the random M3 play leaves them to the M4 play, which
	// sets them as the game does (with the key or the piece).
	derived world.BitSet
}

func newCoM4(t *testing.T, tb *tables.Tables, pol *world.Policy) *coM4 {
	t.Helper()
	m := &coM4{tb: tb, pol: pol}
	held := map[uint16]bool{}
	for _, h := range class.Holds() {
		held[h.ID] = true
	}
	for i := range addr.EventBitBytes {
		sm := class.SyncMaskOf(i) & pol[addr.OffEvent+i]
		for b := range 8 {
			if id := uint16(i)<<8 | 1<<b; sm&(1<<b) != 0 && !held[id] {
				m.story = append(m.story, id)
			}
		}
	}
	for off := addr.OffEvent + addr.EventRegFirst; off < addr.OffEvent+addr.EventSize; off++ {
		if story.IsRegisterByte(off) && pol[off]&class.Of(off).Mask != 0 {
			m.regs = append(m.regs, off)
		}
	}
	mark := func(f itemsrc.Flag) {
		if off, mask, ok := coFlagBit(f); ok {
			m.derived[off] |= mask
		}
	}
	for _, kt := range tb.Keys.Tables {
		for _, s := range kt.Sources {
			mark(s.Flag)
		}
		for _, d := range kt.Doors {
			if _, b, err := addr.SwitchBit(d.Switch); err == nil {
				m.derived[addr.OffMemBit(kt.SaveTable)+b.Off] |= b.Mask
			}
		}
	}
	for _, s := range append(append([]itemsrc.Source(nil), tb.Hearts.Pieces...), tb.Hearts.Containers...) {
		mark(s.Flag)
		if s.Flag.Kind == itemsrc.TboxFlag && s.Flag.Table == addr.TableSea {
			m.pieces = append(m.pieces, s.Flag.Bit)
		}
	}
	switch {
	case len(m.story) < 8, len(m.regs) == 0, len(m.pieces) < 4:
		t.Fatalf("the fixture gives %d story bits, %d registers, %d sea heart pieces: too few to play", len(m.story), len(m.regs), len(m.pieces))
	}
	return m
}

// without takes bits out of what M3's random play sets.
func (m *coM3) without(bits *world.BitSet) {
	var offs, sw []int
	for _, off := range m.offs {
		if m.mask[off] &^= bits[off]; m.mask[off] != 0 {
			offs = append(offs, off)
		}
	}
	for _, off := range m.sw {
		if m.mask[off] != 0 {
			sw = append(sw, off)
		}
	}
	m.offs, m.sw = offs, sw
}

// The game's facts the checks read, by the game's own memory.

func (cg *coGame) u16(a uint32) int { return int(binary.BigEndian.Uint16(cg.read(a, 2))) }

func (cg *coGame) table() int { return int(int8(cg.read(addr.DanStageNo, 1)[0])) }

// keys is the loaded table's mKeyNum, the HUD's.
func (cg *coGame) keys() int { return int(cg.read(addr.LiveKeyNum, 1)[0]) }

// bit reports whether image bit mask of byte off is set in the game (the
// live memBit for the loaded table).
func (cg *coGame) bit(off int, mask byte) bool { return cg.get(off)&mask == mask }

func coTboxOff(table, no int) (int, byte) {
	b, err := addr.TboxBit(no)
	if err != nil {
		panic(err)
	}
	return addr.OffMemBit(table) + b.Off, b.Mask
}

func coSwitchOff(table, no int) (int, byte) {
	_, b, err := addr.SwitchBit(no)
	if err != nil {
		panic(err)
	}
	return addr.OffMemBit(table) + b.Off, b.Mask
}

// drcKeys is keys(DRC) over the game's own flags: key chests taken less
// key doors open (§3), clamped at 0 (derive.KeyTarget).
func (cg *coGame) drcKeys() (taken, open int) {
	for _, no := range coKeyChests {
		if cg.bit(coTboxOff(addr.TableDRC, no)) {
			taken++
		}
	}
	for _, sw := range coKeyDoors {
		if cg.bit(coSwitchOff(addr.TableDRC, sw)) {
			open++
		}
	}
	return taken, open
}

func (cg *coGame) event(id uint16) bool {
	b := addr.EventByte(id)
	return cg.read(addr.SaveImage+uint32(addr.OffEvent+b.Off), 1)[0]&b.Mask == b.Mask
}

// counters reports whether no derive count is pending.
func (cg *coGame) counters() bool {
	return cg.u16(addr.ItemKeyNumCount) == 0 && cg.u16(addr.ItemMaxLifeCount) == 0 && cg.u16(addr.ItemMaxMagicCount) == 0
}

// view is the game's snapshot as the Deriver reads it.
func (cg *coGame) view() save.View {
	cg.t.Helper()
	s, err := state.Read(cg.m, state.Options{})
	if err != nil {
		cg.t.Fatal(err)
	}
	return save.ViewOf(s)
}

// coM4Lacks lists what game cg lacks of world w by M4's classes, and what
// the world lacks of it, within m's policy: the story bits (less the
// holds' for a game that never had MET_KORL), the registers, the
// inventory flags and the capacities in the saved image, the bow's and
// the Deku Leaf's slots once their flags are in, and the derived values
// over the game's own flags: DRC's keys when it is loaded, max life by
// the heart table, max magic, with no count left pending.
func (m *coM4) lacks(cg *coGame, w *world.Image) []string {
	var out []string
	saved := func(off int) byte { return cg.read(addr.SaveImage+uint32(off), 1)[0] }
	for off := range addr.SaveImageSize {
		c := replica.CategoryOf(off)
		switch c {
		case replica.CatStory, replica.CatRegister, replica.CatInventory, replica.CatCapacity:
		default:
			continue
		}
		r := class.Of(off)
		mask := m.pol[off] & world.SharedMask(off)
		if mask == 0 {
			continue
		}
		g, v := saved(off), w[off]
		if c == replica.CatStory && !cg.event(coMetKorl) {
			for _, id := range []uint16{coMetKorl, co0801} {
				if b := addr.EventByte(id); addr.OffEvent+b.Off == off {
					v &^= b.Mask
				}
			}
		}
		if r.Class == class.Max {
			if g&r.Mask != v&r.Mask {
				out = append(out, fmt.Sprintf("%s: game %d, world %d", replica.DefaultLabel(off, -1), g&r.Mask, v&r.Mask))
			}
			continue
		}
		if d := (v &^ g) & mask; d != 0 {
			out = append(out, fmt.Sprintf("%s: the game lacks 0x%02X", replica.DefaultLabel(off, -1), d))
		}
		if d := (g &^ w[off]) & mask; d != 0 {
			out = append(out, fmt.Sprintf("%s: the world lacks 0x%02X", replica.DefaultLabel(off, -1), d))
		}
	}
	if saved(addr.OffGetItem+save.SlotBow)&1 != 0 && saved(addr.OffItems+save.SlotBow) != save.ItemBow {
		out = append(out, "the bow's slot")
	}
	if saved(addr.OffGetItem+save.SlotDekuLeaf)&1 != 0 && saved(addr.OffItems+save.SlotDekuLeaf) != save.ItemDekuLeaf {
		out = append(out, "the Deku Leaf's slot")
	}
	if !cg.counters() {
		out = append(out, "a count pending")
	}
	if cg.table() == addr.TableDRC {
		taken, open := cg.drcKeys()
		if want := max(taken-open, 0); cg.keys() != want {
			out = append(out, fmt.Sprintf("DRC's keys: %d, the formula %d (%d taken, %d open)", cg.keys(), want, taken, open))
		}
	}
	v := cg.view()
	if f := derive.CountLife(v, m.tb.Hearts); v.MaxLife() != derive.LifeTarget(f, 0) {
		out = append(out, fmt.Sprintf("max life %d, the formula %d", v.MaxLife(), derive.LifeTarget(f, 0)))
	}
	if f := derive.CountMagic(v); v.MaxMagic() != f.Derived() {
		out = append(out, fmt.Sprintf("max magic %d, the formula %d", v.MaxMagic(), f.Derived()))
	}
	return out
}

// coNever is the game's values M4 only ever raises: the capacities, max
// life and max magic.
type coNever struct {
	wallet, arrows, bombs, life, magic int
}

func coNeverOf(cg *coGame) coNever {
	saved := func(off int) int { return int(cg.read(addr.SaveImage+uint32(off), 1)[0]) }
	return coNever{saved(addr.OffWalletSize), saved(addr.OffArrowMax), saved(addr.OffBombMax), cg.u16(addr.MaxLife), int(cg.read(addr.MaxMagic, 1)[0])}
}

func (n coNever) below(prev coNever) string {
	for _, c := range []struct {
		name     string
		now, was int
	}{{"the wallet", n.wallet, prev.wallet}, {"the quiver", n.arrows, prev.arrows}, {"the bomb bag", n.bombs, prev.bombs}, {"max life", n.life, prev.life}, {"max magic", n.magic, prev.magic}} {
		if c.now < c.was {
			return fmt.Sprintf("%s fell from %d to %d", c.name, c.was, c.now)
		}
	}
	return ""
}

// TestCoopSessionM4 is M4's acceptance test through the --coop session
// plumbing: the host founds a world from A's file and runs A's pipeline
// in its own session; B joins from a session of its own; both take their
// stage tables from --coop-iso (the vanilla fixture) and write. Scripted
// play first:
//
//   - on the sea, both open the same heart-piece chest and A another; A
//     gets the Deku Leaf, B the bow and its quiver and raises a register;
//   - A, in a cutscene, sets two story bits, MET_KORL and UNK_0801: the
//     bits leave A together once the event is over, and reach B at its
//     next stage change, the holds' aside;
//   - in DRC, both take the same key; A takes another, which B's derive
//     counts in; A opens a door, whose switch reaches B at its next room
//     change, and only then does B's key go (no phantom); both spend a
//     key on the same door, which counts once.
//
// Then both play at random, M3 progress and M4's (story bits, registers,
// key chests and doors, heart pieces, the wallet), and move between
// stages and rooms, while the connections deliver out of order, repeat
// messages and drop, and the host restarts from its world file. Once
// both reload the stage now and then, as play would:
//
//   - every M4 class converges: both games and the world hold the same
//     story bits (but MET_KORL and UNK_0801 in B, which never had
//     MET_KORL), registers, inventory flags and capacities, and each
//     game's keys, max life and max magic are the formulas over its own
//     flags, with no count pending; the M3 bytes too;
//   - no capacity, max life or max magic of either game ever fell;
//   - /status shows B's held story bits and its key tables;
//   - the write audit (coAuditM3 with coAuditM4Write) holds over every
//     byte either pipeline wrote: story bits only on a transition tick,
//     the derive targets only through the derive path; and no PER_PLAYER,
//     NEVER or TAG byte crossed the wire.
func TestCoopSessionM4(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a loopback server for a few seconds")
	}
	useTablesDir(t, t.TempDir())
	iso := writeImage(t, "ww.iso", tablestest.ISO(tablestest.Vanilla()))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // before the cleanups, which wait for the clients

	ga := newCoGame(t, "Link", coMine{life: 0x0051, rupee: 0x0123, magic: 0x11})
	gb := newCoGame(t, "Tetra", coMine{life: 0x0062, rupee: 0x0456, magic: 0x22})
	h := &coHost{t: t, ctx: ctx, rep: &coLog{}, env: coopEnv{
		worlds:   t.TempDir(),
		backups:  t.TempDir(),
		userDirs: []string{coUserDir(t)},
		openMem:  func() (mem.Mem, func(), error) { return ga.m, func() {}, nil },
		tick:     func() { ga.g.Frames(3) },
		input:    strings.NewReader(""),
		tables:   loadCoopStageTables,
	}}
	h.start(coopFlags{on: true, world: coopWorldNew, yes: true, iso: iso})
	defer h.stop()
	cs := h.cs
	tb := cs.m1()
	if tb == nil || tb.Keys == nil || tb.Hearts == nil {
		t.Fatalf("the host has no stage tables:\n%s", h.rep)
	}
	pol := cs.policy()
	m4 := newCoM4(t, tb, pol)
	m3 := newCoM3(pol)
	m3.without(&m4.derived)

	joinIn, joinKeys := coInput(t)
	jlog := &coLog{}
	jcs := startCoopSession(jlog, coopFlags{on: true, yes: true, iso: iso}, coopEnv{backups: t.TempDir(), userDirs: []string{coUserDir(t)}, input: joinIn, tables: loadCoopStageTables})
	for _, l := range []*coLog{h.rep, jlog} {
		if !l.has("co-op: shared progression this session: story bits and event registers (written at stage changes), items, songs and capacities (when idle) and max magic; derived from the stage tables: the small keys of DRC") ||
			!l.has(" and max hearts.") || l.has("not derived") {
			t.Errorf("the session did not say what of M4 it writes:\n%s", l)
		}
	}
	audit := &coAudit{}
	ba, bb := newCoBeat(ga), newCoBeat(gb)
	ba.doors, bb.doors = coDRCDoors(), coDRCDoors()
	a := newCoPlayerOver(t, ctx, h, cs, ga, ba, "Link", &coLog{}, audit, 3)
	b := newCoPlayerOver(t, ctx, h, jcs, gb, bb, "Tetra", &coLog{}, audit, 4)
	players := []*coPlayer{a, b}
	logs := func() string { return fmt.Sprintf("host:\n%s\nA:\n%s\nB:\n%s", h.rep, a.rep, b.rep) }
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	// Every step: no capacity, max life or max magic falls, nor the
	// world; and no key door stays shut to its player (coStuck).
	never := map[*coPlayer]coNever{}
	stuck := map[*coPlayer]int{}
	prevWorld := h.world()
	stepAll := func() {
		t.Helper()
		for _, p := range players {
			p.step()
			n := coNeverOf(p.coGame)
			if why := n.below(never[p]); why != "" {
				t.Fatalf("%s: %s\n%s", p.name, why, logs())
			}
			never[p] = n
			if why := p.stuckAt(); why == "" {
				stuck[p] = 0
			} else if stuck[p]++; stuck[p] > coStuck {
				t.Fatalf("%s: %s for %d ticks\n%s", p.name, why, stuck[p], logs())
			}
		}
		cur := h.world()
		if why := coMonotone(&prevWorld, &cur); why != "" {
			t.Fatalf("the world went backwards: %s", why)
		}
		prevWorld = cur
	}
	for _, p := range players {
		never[p] = coNeverOf(p.coGame)
	}
	run := func(n int) {
		t.Helper()
		for range n {
			stepAll()
			time.Sleep(time.Millisecond)
		}
	}
	until := func(why string, done func() bool) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for !done() {
			if time.Now().After(deadline) {
				t.Fatalf("%s\n%s", why, logs())
			}
			stepAll()
			time.Sleep(2 * time.Millisecond)
		}
	}
	welcomes := func(p *coPlayer) int { return p.rep.count("welcome to world") }
	need := map[*coPlayer]int{}
	const want = hub.M4Features | hub.FeatureSwitches
	waitArmed := func(why string) {
		t.Helper()
		until(why+": not armed with M4's features", func() bool {
			for _, p := range players {
				if st := p.s.r.Status(); st.State != replica.StateArmed || st.Features&want != want || welcomes(p) < need[p] {
					return false
				}
			}
			return true
		})
	}
	// transit is a stage change of p's game to s, as the game makes it:
	// mNextStage set for a few ticks, then the new stage.
	transit := func(p *coPlayer, s fakegame.Stage) {
		t.Helper()
		run(gate.StableTicks + 1)
		must(p.g.SetNextStage(s))
		run(3)
		must(p.g.ChangeStage())
	}
	waitArmed("binding")
	for _, p := range players {
		if !p.rep.has("written to this game: the world's chests, pickups, rooms, dungeon items, ocean and charts; its latching switches (at room changes); its story bits and event registers (at stage changes); its items, songs and capacities (when idle); the small keys, max hearts and max magic this game's own flags give") {
			t.Errorf("%s's armed line does not name M4's classes:\n%s", p.name, p.rep)
		}
	}

	// Hearts, the Deku Leaf, the bow and a register, on the sea: the same
	// piece twice counts once.
	p0, p1 := m4.pieces[0], m4.pieces[1]
	must(a.g.OpenPieceChest(p0))
	must(b.g.OpenPieceChest(p0))
	must(a.g.OpenPieceChest(p1))
	must(a.g.GetDekuLeaf())
	must(b.g.GetItem(save.SlotBow, 0, save.ItemBow))
	must(b.g.SetCapacity(addr.OffArrowMax, 30))
	reg := m4.regs[0]
	rm := class.Of(reg).Mask
	b.put(reg, b.get(reg)|rm)
	until("the hearts, the Deku Leaf and the bow never converged", func() bool {
		for _, p := range players {
			saved := func(off int) byte { return p.read(addr.SaveImage+uint32(off), 1)[0] }
			if p.u16(addr.MaxLife) != derive.StartLife+2 || int(p.read(addr.MaxMagic, 1)[0]) != derive.MagicLeaf || !p.counters() ||
				saved(addr.OffItems+save.SlotBow) != save.ItemBow || saved(addr.OffArrowMax) != 30 ||
				saved(addr.OffItems+save.SlotDekuLeaf) != save.ItemDekuLeaf {
				return false
			}
		}
		return true
	})
	run(30)
	for _, p := range players {
		if l := p.u16(addr.MaxLife); l != derive.StartLife+2 {
			t.Errorf("%s: max life %d after the same piece twice, want %d", p.name, l, derive.StartLife+2)
		}
	}

	// A cutscene's story bits leave A together, once it is over, and
	// reach B at its next stage change; the holds keep theirs out.
	s1, s2 := m4.story[0], m4.story[len(m4.story)/2]
	a.g.SetEvent(1, 0)
	run(3)
	for _, id := range []uint16{s1, s2, coMetKorl, co0801} {
		a.g.OnEventBit(id)
	}
	worldHas := func(id uint16) bool {
		w, e := h.world(), addr.EventByte(id)
		return w[addr.OffEvent+e.Off]&e.Mask == e.Mask
	}
	run(20)
	if worldHas(s1) || worldHas(s2) {
		t.Fatal("story bits reached the world during their cutscene")
	}
	a.g.SetEvent(0, 0)
	until("the cutscene's bits never reached the world", func() bool {
		if worldHas(s1) != worldHas(s2) {
			t.Fatalf("the cutscene's bits reached the world apart")
		}
		return worldHas(s1)
	})
	run(30)
	if b.event(s1) || b.event(s2) {
		t.Fatal("B got story bits before a stage change")
	}
	if b.get(reg)&rm == rm && a.get(reg)&rm == rm {
		t.Fatal("A got B's register before a stage change") // a.get(reg) reads the saved event register
	}
	transit(b, coSea)
	transit(a, coSea)
	until("the story bits and the register never reached the other game", func() bool {
		return b.event(s1) && b.event(s2) && a.get(reg)&rm == rm
	})
	run(30)
	if b.event(coMetKorl) || b.event(co0801) {
		t.Fatal("B got a held story bit: MET_KORL or UNK_0801")
	}

	// DRC's keys.
	must(a.g.LoadStage(coDRCKeys))
	must(b.g.LoadStage(coDRCKeys))
	run(gate.StableTicks + 2)
	keysAre := func(ka, kb int) func() bool {
		return func() bool { return a.keys() == ka && b.keys() == kb && a.counters() && b.counters() }
	}
	// The same key twice: one flag, one key each.
	must(a.g.OpenKeyChest(1))
	must(b.g.OpenKeyChest(1))
	until("the same key twice", keysAre(1, 1))
	run(40)
	if a.keys() != 1 || b.keys() != 1 {
		t.Fatalf("the same key twice: A %d, B %d keys", a.keys(), b.keys())
	}
	// A opens door 0x10 (rooms 0 and 1, next to B in room 1) with that
	// key, from its front room 0, as keyInit spends a key only there
	// (d_door.cpp:435), then goes through: its switch waits in B for a
	// room change and the room hold. First B's stage entry's own window
	// closes, once its rooms have run the room hold.
	run(150)
	must(a.g.EnterRoom(0, 1))
	run(5)
	must(a.door(0x10).Open(a.g))
	must(a.g.EnterRoom(1, 0, 2))
	d10, d10m := coSwitchOff(addr.TableDRC, 0x10)
	until("A's door never reached the world", func() bool { return h.world()[d10]&d10m != 0 && a.keys() == 0 && a.counters() })
	run(40)
	if b.bit(d10, d10m) || b.keys() != 1 {
		t.Fatalf("before B's room change: door %v, %d keys; want the door shut and the key", b.bit(d10, d10m), b.keys())
	}
	must(b.g.EnterRoom(2, 1))
	run(5)
	must(b.g.EnterRoom(1, 2))
	// The switch lands about 2 s after B's room change. B's door read its
	// lock as B came in and keeps it until B's next room change, and B
	// keeps the key until then: the door would be shut to B with no key
	// (the owner's F13).
	until("A's door never reached B", func() bool { return b.bit(d10, d10m) })
	run(2 * coStuck)
	if !b.door(0x10).Locked() || b.keys() != 1 {
		t.Fatalf("after B's room change: door 0x10 locked %v, %d keys; want the lock up and the key kept", b.door(0x10).Locked(), b.keys())
	}
	must(b.g.EnterRoom(2, 1))
	run(5)
	must(b.g.EnterRoom(1, 2))
	until("A's door never took B's key", func() bool { return b.keys() == 0 && b.counters() && !b.door(0x10).Locked() })
	// A's chest reaches B, and B's derive counts its key in.
	must(a.g.OpenKeyChest(2))
	until("A's key never reached B", keysAre(1, 1))
	// Both spend a key on the same door: it counts once.
	must(a.door(0x11).Open(a.g))
	must(b.door(0x11).Open(b.g))
	until("a key spent on an open door", keysAre(0, 0))
	run(40)
	for _, p := range players {
		if taken, open := p.drcKeys(); p.keys() != 0 || taken != 2 || open != 2 {
			t.Fatalf("%s: %d keys, %d taken, %d open", p.name, p.keys(), taken, open)
		}
	}

	// /status shows B's held story bits and its key tables.
	coType(t, joinKeys, "/status")
	until("/status said nothing of M4", func() bool { return b.rep.has("Tetra:   small keys of table 3 (DRC)") })
	for _, s := range []string{
		"Tetra:   progression granted: story|inventory|keys|life|magic",
		"Tetra:   story bits held back until this game catches up: ",
		"MET_KORL",
		"Tetra:   small keys of table 3 (DRC): 0 key(s), 2 of 4 source(s) taken, 2 of 4 door(s) open",
		"Tetra:   max hearts: derived",
	} {
		if !b.rep.has(s) {
			t.Errorf("/status lacks %q:\n%s", s, b.rep)
		}
	}

	// Random play, with drops and a host restart.
	rng := rand.New(rand.NewPCG(2026, 1016))
	pend := map[*coPlayer]int{} // ticks to a pending stage change's end
	busy := map[*coPlayer]int{} // ticks to a cutscene's end
	stages := []fakegame.Stage{coSea, coDRCKeys, coDRCBoss, coWoods}
	free := func(off int, mask byte) bool { return h.world()[off]&mask == 0 }
	play := func(p *coPlayer) {
		t.Helper()
		if pend[p] > 0 {
			return
		}
		switch k := rng.IntN(24); {
		case k < 6:
			if k := rng.IntN(20); k >= 16 {
				pend[p] = 4
				must(p.g.SetNextStage(stages[rng.IntN(len(stages))]))
				return
			}
			m3.play(p, rng)
		case k < 9:
			p.g.OnEventBit(m4.story[rng.IntN(len(m4.story))])
		case k < 10:
			off := m4.regs[rng.IntN(len(m4.regs))]
			r := class.Of(off)
			if r.Class == class.Max {
				if v := byte(rng.IntN(int(r.Mask)+1)) & r.Mask; v > p.get(off)&r.Mask {
					p.put(off, p.get(off)&^r.Mask|v)
				}
			} else if bit := byte(1) << rng.IntN(8); bit&r.Mask&pol[off] != 0 {
				p.put(off, p.get(off)|bit)
			}
		case k < 11:
			if busy[p] == 0 {
				busy[p] = 5 + rng.IntN(10)
				p.g.SetEvent(1, 0)
			}
		case k < 12:
			if w := int(p.read(addr.SaveImage+addr.OffWalletSize, 1)[0]); w < 2 {
				must(p.g.SetCapacity(addr.OffWalletSize, byte(w+1)))
			}
		case k < 18 && p.table() == addr.TableDRC:
			// A key chest or door, as the game makes them: a chest
			// nobody has opened, a door while the game holds a key no
			// door of the world's has taken (§6.4's known exploit, one
			// key on two doors, is left out).
			if rng.IntN(2) == 0 {
				no := coKeyChests[2+rng.IntN(2)]
				if off, m := coTboxOff(addr.TableDRC, no); free(off, m) && !p.bit(off, m) {
					must(p.g.OpenKeyChest(no))
				}
				return
			}
			sw := coKeyDoors[2+rng.IntN(2)]
			off, m := coSwitchOff(addr.TableDRC, sw)
			// A door of the world's still waits while its switch is not
			// in the game, or the game's derive holds it (its lock may
			// still show, and its key is kept until a room change).
			waiting := len(p.heldDoors()) > 0
			for _, d := range coKeyDoors {
				do, dm := coSwitchOff(addr.TableDRC, d)
				waiting = waiting || h.world()[do]&dm != 0 && !p.bit(do, dm)
			}
			if !waiting && p.keys() > 0 && p.counters() && free(off, m) && !p.bit(off, m) {
				// Through the door when it is next to Link (from its
				// back room keyInit spends nothing, and the door stays
				// shut); a door elsewhere reads its switch as Link
				// comes to it.
				if d := p.door(sw); d.AtLink() {
					must(d.Open(p.g))
				} else {
					must(p.g.UnlockKeyDoor(sw))
				}
			}
		case k < 18 && p.table() == addr.TableSea:
			no := m4.pieces[2+rng.IntN(len(m4.pieces)-2)]
			if off, m := coTboxOff(addr.TableSea, no); free(off, m) && !p.bit(off, m) {
				must(p.g.OpenPieceChest(no))
			}
		}
	}
	tickDown := func(p *coPlayer) {
		if pend[p] > 0 {
			if pend[p]--; pend[p] == 0 {
				must(p.g.ChangeStage())
			}
		}
		if busy[p] > 0 {
			if busy[p]--; busy[p] == 0 {
				p.g.SetEvent(0, 0)
			}
		}
	}
	drop := func(ps ...*coPlayer) {
		for _, p := range ps {
			need[p] = welcomes(p) + 1
		}
	}
	const steps = 400
	for i := range steps {
		for _, p := range players {
			if rng.IntN(3) == 0 {
				play(p)
			}
			tickDown(p)
		}
		stepAll()
		switch i {
		case steps / 4:
			drop(a)
			a.s.client.Disconnect()
		case steps / 2:
			drop(a, b)
			h.restart()
		case 3 * steps / 4:
			drop(b)
			b.s.client.Disconnect()
		}
		time.Sleep(time.Millisecond)
	}
	for _, p := range players {
		for pend[p] > 0 || busy[p] > 0 {
			tickDown(p)
			stepAll()
		}
	}

	// Quiesce: both in DRC, reloading it and changing rooms now and then
	// (the story bits wait for a stage change, the switches for a room
	// change), until every M4 class has converged.
	waitArmed("after the play")
	for _, p := range players {
		must(p.g.LoadStage(coDRCKeys))
	}
	deadline := time.Now().Add(40 * time.Second)
	for i := 0; ; i++ {
		for k, p := range players {
			switch (i + 37*k) % 80 {
			case 20:
				must(p.g.EnterRoom(2, 1))
			case 30:
				must(p.g.EnterRoom(1, 2))
			case 50:
				must(p.g.SetNextStage(coDRCKeys))
			case 54:
				must(p.g.ChangeStage())
			}
		}
		stepAll()
		w := h.world()
		ok := true
		for _, p := range players {
			got := m3.of(p.image())
			ok = ok && len(m4.lacks(p.coGame, &w)) == 0 && got == m3.of(w)
		}
		if ok {
			break
		}
		if time.Now().After(deadline) {
			for _, p := range players {
				got, mw := m3.of(p.image()), m3.of(w)
				t.Errorf("%s lacks %s; M3 %s; %+v, off %q", p.name, strings.Join(m4.lacks(p.coGame, &w), "; "), coDiff(&got, &mw), p.s.r.Status().Writes, p.s.r.Status().WritesOff)
			}
			t.Fatalf("no convergence\n%s", logs())
		}
		time.Sleep(time.Millisecond)
	}
	if b.event(coMetKorl) || b.event(co0801) {
		t.Error("B got a held story bit: MET_KORL or UNK_0801")
	}
	if !worldHas(coMetKorl) || !worldHas(co0801) {
		t.Error("the world lacks A's MET_KORL or UNK_0801")
	}

	// The audits, over everything either pipeline wrote and every
	// payload either sent or got.
	for _, p := range players {
		st := p.s.r.Status()
		if st.WritesOff != "" || st.Writes.Failed != 0 {
			t.Errorf("%s: writes off %q, %+v", p.name, st.WritesOff, st.Writes)
		}
		n := coAuditM3(t, p)
		if n.tags != 1 {
			t.Errorf("%s: %d tag writes, want the bind", p.name, n.tags)
		}
		for _, tg := range []replica.Target{replica.TargetStory, replica.TargetInventory, replica.TargetDerived, replica.TargetCounters} {
			if n.m4[tg] == 0 {
				t.Errorf("%s wrote %v: none at %v", p.name, n.m4, tg)
			}
		}
		if welcomes(p) < 2 || !p.rep.has("armed offline") {
			t.Errorf("%s: %d welcomes, offline %v: the reconnects did not happen", p.name, welcomes(p), p.rep.has("armed offline"))
		}
		t.Logf("%s: M4 writes %v (%d by the derive path), M3 %v", p.name, n.m4, n.derived, n.cats)
	}
	audit.mu.Lock()
	defer audit.mu.Unlock()
	for _, s := range audit.bad {
		t.Error(s)
	}
	if audit.worlds == 0 || audit.gains == 0 {
		t.Errorf("the audit saw %d WORLD and %d GAINS entries: the wire was not watched", audit.worlds, audit.gains)
	}
	t.Logf("world seq %d; %d WORLD images and %d gains audited", h.cs.store.Seq(), audit.worlds, audit.gains)
}

// TestFoundFromSessionPolicy: a world founded from a file keeps the
// file's bits the session's policy shares, its story bits and latching
// memory switches among them, so the file binds to it as a subset (§5.3).
// The world's default policy alone would leave them out, and the file
// would look ahead of the world it was founded from.
func TestFoundFromSessionPolicy(t *testing.T) {
	cg := newCoGame(t, "Link", coMine{life: 12})
	id := uint16(0)
	for i := range addr.EventBitBytes {
		if sm := class.SyncMaskOf(i); sm != 0 {
			id = uint16(i)<<8 | uint16(sm&-sm)
			break
		}
	}
	cg.g.OnEventBit(id)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(cg.g.OnSwitch(0x10, -1))
	s, err := state.Read(cg.m, state.Options{})
	must(err)
	sw, swm := coSwitchOff(addr.TableSea, 0x10)
	e := addr.EventByte(id)
	for _, c := range []struct {
		name string
		pol  *world.Policy
		has  bool
	}{
		{"session", hub.SessionPolicy(func(t, no int) bool { return t == addr.TableSea && no == 0x10 }), true},
		{"world default", world.DefaultPolicy(), false},
	} {
		w, err := foundFrom(&coLog{}, s, c.pol)
		must(err)
		img := w.Image()
		if got := img[addr.OffEvent+e.Off]&e.Mask != 0 && img[sw]&swm != 0; got != c.has {
			t.Errorf("%s policy: the founded world has the story bit 0x%04X and switch 0x10: %v, want %v", c.name, id, got, c.has)
		}
	}
}

// TestLogCoopProgression: a writing session says what of M4 its replicas
// write, by its own tables: every key table that passes, and each that
// fails or the whole table set missing, with why.
func TestLogCoopProgression(t *testing.T) {
	useTablesDir(t, t.TempDir())
	st, err := loadCoopStageTables(writeImage(t, "ww.iso", tablestest.ISO(tablestest.Vanilla())))
	if err != nil {
		t.Fatal(err)
	}
	vanilla := st.t
	fw := *vanilla
	kr := *fw.Keys
	kr.Tables = append([]keys.Table(nil), kr.Tables...)
	for i := range kr.Tables {
		if kr.Tables[i].SaveTable == addr.TableFW {
			kr.Tables[i].Pass, kr.Tables[i].Problems = false, []string{"2 small-key source(s) but 1 key door(s)"}
		}
	}
	fw.Keys = &kr
	noHearts := *vanilla
	h := *noHearts.Hearts
	h.Pass, h.Problems = false, []string{"43 pieces"}
	noHearts.Hearts = &h
	for _, c := range []struct {
		name   string
		t      *tables.Tables
		has    []string
		hasNot []string
		warns  int
	}{
		{"vanilla", vanilla, []string{"; derived from the stage tables: the small keys of DRC, FW, TotG, ET, WT and max hearts."}, []string{"not derived"}, 0},
		{"none", nil, []string{"and max magic; without co-op stage tables no small keys or max hearts are derived."}, []string{"derived from"}, 0},
		{"FW fails", &fw, []string{"the small keys of DRC, TotG, ", "co-op: the small keys of table 4 (FW) are not derived: its M1 key invariant fails (coop-tables): 2 small-key source(s) but 1 key door(s)."}, []string{"DRC, FW"}, 1},
		{"hearts fail", &noHearts, []string{"; derived from the stage tables: the small keys of DRC, FW, TotG, ET, WT.", "co-op: max hearts are not derived: the M1 heart table fails its check (coop-tables): 1 problem(s)."}, []string{"and max hearts"}, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			l := &coLog{}
			logCoopProgression(l, c.t)
			for _, s := range c.has {
				if !l.has(s) {
					t.Errorf("no %q in:\n%s", s, l)
				}
			}
			for _, s := range c.hasNot {
				if l.has(s) {
					t.Errorf("%q in:\n%s", s, l)
				}
			}
			if n := l.count("are not derived"); n != c.warns {
				t.Errorf("%d warnings, want %d:\n%s", n, c.warns, l)
			}
		})
	}
}
