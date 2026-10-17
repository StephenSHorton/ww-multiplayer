package derive_test

import (
	"bytes"
	"encoding/binary"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/derive"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/tablestest"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// The §8.1 scenarios run the real snapshot, gates and WriteGuard over a
// FakeGame, whose HUD consumes the pending counters as the meter does,
// with the M1 tables built from tablestest's vanilla-shaped disc: Dragon
// Roost Cavern (table 3, stage M_NewD2) has four key chests, tbox bits
// 1-4 in room 1, and four key doors, switches 0x10-0x13; Forbidden Woods
// (table 4, kindan) one of each; the sea (table 0) the stage-file heart
// pieces, tbox bits 0-28 and a pig's dig spot on item bit 2; tables 2-7
// a container each.

var fixture = sync.OnceValues(func() (*tables.Tables, error) {
	img := tablestest.ISO(tablestest.Vanilla())
	d, err := inject.NewDisc(bytes.NewReader(img), int64(len(img)))
	if err != nil {
		return nil, err
	}
	return tables.Build(d)
})

func vanillaTables(t *testing.T) *tables.Tables {
	t.Helper()
	tb, err := fixture()
	if err != nil {
		t.Fatal(err)
	}
	if !tb.Keys.Pass || !tb.Hearts.Pass {
		t.Fatalf("the fixture's tables fail: keys %v, hearts %v", tb.Keys.Pass, tb.Hearts.Pass)
	}
	return tb
}

// Stages the receiver loads.
var (
	drc      = fakegame.Stage{Name: "M_NewD2", Room: 1, Table: addr.TableDRC, KeyHUD: true}
	drcBoss  = fakegame.Stage{Name: "M_DragB", Table: addr.TableDRC} // no key HUD
	woods    = fakegame.Stage{Name: "kindan", Room: 1, Table: addr.TableFW, KeyHUD: true}
	outerSea = fakegame.Stage{Name: "sea", Room: 1, Table: addr.TableSea}
)

const tickEvery = 100 * time.Millisecond // the replica's 10 Hz

// rig is the receiver: a FakeGame, and the Deriver fed by snapshots of
// it, whose writes are routed the way the replica will route them: a
// compare-and-set against Expect, every byte through a WriteGuard for the
// derive's targets, then a tool write. The other side's flags land with
// FakeMem.Store, as the M3 reconciler's writes would, so the recorder
// sees the derive's writes alone.
type rig struct {
	t     *testing.T
	m     *mem.FakeMem
	g     *fakegame.FakeGame
	tr    gate.Tracker
	d     *derive.Deriver
	guard *replica.WriteGuard
	now   time.Time
	armed bool

	// dry drops every write, as --coop-dry-run; race runs between the
	// snapshot and the routing, as the game does; stall runs no frames, as
	// a paused or stalled game, while the ticks go on.
	dry   bool
	race  func()
	stall bool

	made    []derive.Write
	dropped []derive.Write
	notes   []string
}

func newRig(t *testing.T, tb *tables.Tables, at fakegame.Stage, setup ...func(*rig)) *rig {
	t.Helper()
	m := mem.NewFakeMem()
	r := &rig{t: t, m: m, g: fakegame.New(m), armed: true,
		d:     derive.New(derive.Config{Tables: tb}),
		guard: replica.NewWriteGuard(replica.GuardConfig{Targets: replica.TargetDerived | replica.TargetCounters}),
		now:   time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC),
	}
	if err := r.g.NewGame(0, "Link"); err != nil {
		t.Fatal(err)
	}
	r.load(at)
	for _, f := range setup {
		f(r)
	}
	m.Record(true)
	t.Cleanup(r.audit)
	return r
}

func (r *rig) load(s fakegame.Stage) {
	r.t.Helper()
	if err := r.g.LoadStage(s); err != nil {
		r.t.Fatalf("LoadStage(%s): %v", s.Name, err)
	}
}

// step runs one tick: six frames, a snapshot, the Deriver, the routing.
func (r *rig) step() derive.Plan {
	r.t.Helper()
	if !r.stall {
		r.g.Frames(6)
	}
	s, err := state.Read(r.m, state.Options{})
	if err != nil {
		r.t.Fatal(err)
	}
	st := r.tr.Observe(s)
	p := r.d.Tick(derive.Input{Now: r.now, Snap: s, Gates: st, Stag: derive.ReadStag(r.m, s), Armed: r.armed})
	r.guard.SetTick(replica.Tick{Gates: st, Armed: r.armed, Table: s.Table})
	for _, w := range p.Writes {
		r.route(w)
	}
	for _, n := range p.Notes {
		r.t.Logf("note: %s", n)
	}
	r.notes = append(r.notes, p.Notes...)
	r.now = r.now.Add(tickEvery)
	return p
}

// run steps for d of ticks.
func (r *rig) run(d time.Duration) {
	r.t.Helper()
	for range int(d / tickEvery) {
		r.step()
	}
}

// settle runs long enough for any correction to be planned and land.
func (r *rig) settle() { r.t.Helper(); r.run(3 * time.Second) }

func (r *rig) route(w derive.Write) {
	r.t.Helper()
	if r.race != nil {
		r.race()
	}
	cur, err := r.m.Read(w.Addr, len(w.Data))
	if err != nil {
		r.t.Fatal(err)
	}
	if r.dry || !bytes.Equal(cur, w.Expect) {
		r.dropped = append(r.dropped, w)
		r.d.Dropped(w)
		return
	}
	for i := range w.Data {
		if err := r.guard.Check(w.Addr+uint32(i), cur[i], w.Data[i]); err != nil {
			r.t.Errorf("the WriteGuard refused %v: %v", w, err)
			return
		}
	}
	if err := r.m.Write(w.Addr, w.Data); err != nil {
		r.t.Fatal(err)
	}
	r.made = append(r.made, w)
}

// audit checks every recorded write: only the derive's own bytes, none
// refused.
func (r *rig) audit() {
	allowed := map[uint32]bool{
		addr.ItemKeyNumCount: true, addr.ItemKeyNumCount + 1: true,
		addr.ItemMaxLifeCount: true, addr.ItemMaxLifeCount + 1: true,
		addr.ItemMaxMagicCount: true, addr.ItemMaxMagicCount + 1: true,
		addr.LiveKeyNum: true,
	}
	for t := range addr.TableCount {
		a, _ := addr.SavedMemBitAddr(t)
		allowed[a+addr.MemBitOffKeyNum] = true
	}
	for _, w := range r.m.Writes() {
		for a := w.Addr; a < w.End(); a++ {
			if !allowed[a] {
				r.t.Errorf("write at 0x%08X is no derived byte", a)
			}
		}
	}
	if st := r.guard.Stats(); st.Refused != 0 {
		r.t.Errorf("the WriteGuard refused %d write(s): %v", st.Refused, st.ByReason)
	}
	for _, w := range r.made {
		if _, isCounter := w.Counter(); w.Kind == derive.LifeCounter || w.Kind == derive.MagicCounter {
			if !isCounter || w.Delta() <= 0 {
				r.t.Errorf("%v lowers max life or magic", w)
			}
		}
	}
}

func (r *rig) u8(a uint32) int {
	r.t.Helper()
	v, err := mem.ReadU8(r.m, a)
	if err != nil {
		r.t.Fatal(err)
	}
	return int(v)
}

func (r *rig) u16(a uint32) int {
	r.t.Helper()
	v, err := mem.ReadU16(r.m, a)
	if err != nil {
		r.t.Fatal(err)
	}
	return int(v)
}

func (r *rig) keys() int { return r.u8(addr.LiveKeyNum) }

func (r *rig) savedKeys(t int) int {
	a, _ := addr.SavedMemBitAddr(t)
	return r.u8(a + addr.MemBitOffKeyNum)
}

// or sets mask in the byte at a with a store, the game's write or a
// landing flag.
func (r *rig) or(a uint32, mask byte) {
	b, _ := r.m.Read(a, 1)
	if err := r.m.Store(a, []byte{b[0] | mask}); err != nil {
		r.t.Fatal(err)
	}
}

// count adds n to the s16 counter at a, as the game's setItem*Count does
// (d_com_inf_game.h:460).
func (r *rig) count(a uint32, n int) {
	v := r.u16(a)
	nv := uint16(int16(v) + int16(n))
	if err := r.m.Store(a, []byte{byte(nv >> 8), byte(nv)}); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) loaded() int { return int(int8(r.u8(addr.DanStageNo))) }

// land puts memBit bit b of table t where the M3 reconciler writes it: the
// live copy and the saved slot of the loaded table, the saved slot of any
// other. As the replica does, it tells the Deriver of a bit it set in the
// live copy (Deriver.WroteLive).
func (r *rig) land(t int, b addr.Bit) {
	saved, _ := addr.SavedMemBitAddr(t)
	r.or(saved+uint32(b.Off), b.Mask)
	if t == r.loaded() {
		live := addr.LiveMemBit + uint32(b.Off)
		old := byte(r.u8(live))
		r.or(live, b.Mask)
		r.d.WroteLive(addr.OffMemBit(t)+b.Off, b.Mask&^old)
	}
}

// roomChange is Link going into room, then back to the stage's room 1, a
// tick each: the doors of room 1 run actionInit again, and the Deriver
// counts the doors it held (keys.go).
func (r *rig) roomChange() {
	r.t.Helper()
	for _, room := range []int{2, 1} {
		if err := r.g.EnterRoom(room); err != nil {
			r.t.Fatal(err)
		}
		r.step()
	}
}

func tbox(no int) addr.Bit {
	b, err := addr.TboxBit(no)
	if err != nil {
		panic(err)
	}
	return b
}

func sw(no int) addr.Bit {
	_, b, err := addr.SwitchBit(no)
	if err != nil {
		panic(err)
	}
	return b
}

var stageLife = addr.Bit{Off: addr.MemBitOffDungeonItem, Mask: 1 << addr.DungeonItemStageLife}

// takeKey is the receiver opening key chest no of the loaded table: the
// chest's bit (onTbox, d_save.cpp:1072-1075, live) and the key's count
// (item_func_small_key, d_item.cpp:666).
func (r *rig) takeKey(no int) {
	b := tbox(no)
	r.or(addr.LiveMemBit+uint32(b.Off), b.Mask)
	r.count(addr.ItemKeyNumCount, 1)
}

// spendKey is the receiver unlocking key door sw: keyInit sets the switch
// and counts the key out (d_door.cpp:433-440).
func (r *rig) spendKey(no int) {
	if err := r.g.OnSwitch(no, -1); err != nil {
		r.t.Fatal(err)
	}
	r.count(addr.ItemKeyNumCount, -1)
}

func (r *rig) kinds() []derive.Kind {
	var k []derive.Kind
	for _, w := range r.made {
		k = append(k, w.Kind)
	}
	return k
}

func (r *rig) noted(sub string) bool {
	return slices.ContainsFunc(r.notes, func(n string) bool { return strings.Contains(n, sub) })
}

func (r *rig) wantKeys(want int) {
	r.t.Helper()
	if got := r.keys(); got != want {
		r.t.Fatalf("live mKeyNum = %d, want %d (writes %v)", got, want, r.made)
	}
}

func TestRemoteKeyChest(t *testing.T) {
	r := newRig(t, vanillaTables(t), drc)
	r.run(time.Second)
	r.land(addr.TableDRC, tbox(1)) // the partner opened chest 1
	// Not before the table has been unchanged for a second.
	r.run(900 * time.Millisecond)
	if len(r.made) != 0 {
		t.Fatalf("written before the table settled: %v", r.made)
	}
	r.settle()
	r.wantKeys(1)
	if k := r.kinds(); !slices.Equal(k, []derive.Kind{derive.KeyCounter}) {
		t.Errorf("writes %v, want one key-counter write", k)
	}
	if !strings.Contains(r.made[0].Reason, "0 -> 1: 1 of 4 source(s) taken, 0 of 4 door(s) open") {
		t.Errorf("reason %q", r.made[0].Reason)
	}
}

func TestSameKeyTwice(t *testing.T) {
	t.Run("both take it before either flag lands", func(t *testing.T) {
		r := newRig(t, vanillaTables(t), drc)
		r.run(time.Second)
		r.takeKey(2)
		r.run(500 * time.Millisecond)
		r.land(addr.TableDRC, tbox(2)) // the partner's, already set here
		r.settle()
		r.wantKeys(1)
		if len(r.made) != 0 {
			t.Errorf("writes %v, want none: one flag, one key each", r.made)
		}
	})
	t.Run("the receiver's stale chest gives it again", func(t *testing.T) {
		r := newRig(t, vanillaTables(t), drc)
		r.run(time.Second)
		r.land(addr.TableDRC, tbox(1))
		r.settle()
		r.wantKeys(1)
		// The chest was made before the flag landed, so it still opens
		// (§6.7): a second key, which the derive takes back.
		r.takeKey(1)
		r.run(200 * time.Millisecond)
		r.wantKeys(2)
		r.settle()
		r.wantKeys(1)
		if !r.noted("1 duplicate key(s), from tbox 3:1") {
			t.Errorf("no duplicate noted: %q", r.notes)
		}
		if k := r.kinds(); !slices.Equal(k, []derive.Kind{derive.KeyCounter, derive.KeyCounter}) || r.made[1].Delta() != -1 {
			t.Errorf("writes %v, want +1 then -1", r.made)
		}
	})
	t.Run("after the stage reloads a rise is kept", func(t *testing.T) {
		r := newRig(t, vanillaTables(t), drc)
		r.run(time.Second)
		r.land(addr.TableDRC, tbox(1))
		r.settle()
		// The stage reloads: its chest is made again, open.
		if err := r.g.SetNextStage(drc); err != nil {
			t.Fatal(err)
		}
		r.run(500 * time.Millisecond)
		if err := r.g.ChangeStage(); err != nil {
			t.Fatal(err)
		}
		r.run(time.Second)
		r.count(addr.ItemKeyNumCount, 1) // a key no source explains
		r.settle()
		r.wantKeys(2)
		if !r.noted("1 key(s) no known source explains: kept") {
			t.Errorf("no kept key noted: %q", r.notes)
		}
	})
}

func TestDoorOpenedRemotely(t *testing.T) {
	t.Run("loaded table", func(t *testing.T) {
		r := newRig(t, vanillaTables(t), drc)
		r.run(time.Second)
		r.takeKey(1)
		r.settle()
		r.wantKeys(1)
		// The partner used their copy of that key on door 0x10; the
		// reconciler writes its switch after the receiver's room change
		// (and the room hold). The door keeps the lock it read as Link
		// came in, so the key goes only at his next room change.
		r.land(addr.TableDRC, sw(0x10))
		r.settle()
		r.wantKeys(1)
		r.roomChange()
		r.settle()
		r.wantKeys(0)
		if len(r.made) != 1 || r.made[0].Kind != derive.KeyCounter || r.made[0].Delta() != -1 {
			t.Errorf("writes %v, want one -1 on the counter", r.made)
		}
	})
	t.Run("another table", func(t *testing.T) {
		r := newRig(t, vanillaTables(t), outerSea, func(r *rig) {
			r.land(addr.TableFW, tbox(1)) // this file took Forbidden Woods' key
			a, _ := addr.SavedMemBitAddr(addr.TableFW)
			r.m.Store(a+addr.MemBitOffKeyNum, []byte{1})
		})
		r.run(time.Second)
		r.land(addr.TableFW, sw(0x10))
		r.settle()
		if got := r.savedKeys(addr.TableFW); got != 0 {
			t.Fatalf("Forbidden Woods' saved mKeyNum = %d, want 0", got)
		}
		if len(r.made) != 1 || r.made[0].Kind != derive.KeySaved || r.made[0].Table != addr.TableFW {
			t.Errorf("writes %v, want one saved-byte write for table 4", r.made)
		}
		// getSave carries it into the stage.
		r.load(woods)
		r.settle()
		r.wantKeys(0)
		if len(r.made) != 1 {
			t.Errorf("writes after the load: %v", r.made[1:])
		}
	})
}

func TestPhantomFreeCoupling(t *testing.T) {
	r := newRig(t, vanillaTables(t), drc)
	r.run(time.Second)
	r.takeKey(1)
	r.settle()
	// The partner opened door 0x10 with the same key. Until its switch is
	// in the receiver's copy the door still locks here, so the key stays:
	// a decrement now would leave a lock and no key.
	r.run(10 * time.Second)
	r.wantKeys(1)
	if len(r.made) != 0 {
		t.Fatalf("writes before the door's switch landed: %v", r.made)
	}
	// Once it is, the door still shows the lock it read as Link came into
	// its room (daDoor10_c reads the switch in actionInit only, after a
	// room change): the key stays until his next one.
	r.land(addr.TableDRC, sw(0x10))
	r.run(10 * time.Second)
	r.wantKeys(1)
	if !r.noted("door 0x10 is counted after your next room change") {
		t.Errorf("no held door noted: %q", r.notes)
	}
	r.roomChange()
	r.settle()
	r.wantKeys(0)
	if !r.noted("door 0x10 counted: your room changed") {
		t.Errorf("notes %q", r.notes)
	}
}

// TestHeldDoor: a door whose switch co-op wrote into the live copy is
// counted at Link's next room change, or as he leaves its stage or the
// Deriver starts over, or once this game spends a key on it. A switch the
// game sets itself, one written to a cold table, one in another stage of
// the loaded table, and one the saved slot already had, are counted at
// once.
func TestHeldDoor(t *testing.T) {
	tb := vanillaTables(t)
	held := func(r *rig) {
		r.t.Helper()
		r.run(time.Second)
		r.takeKey(1)
		r.settle()
		r.land(addr.TableDRC, sw(0x10))
		r.settle()
		r.wantKeys(1)
	}
	t.Run("a key spent on it", func(t *testing.T) {
		r := newRig(t, tb, drc)
		held(r)
		// The lock still shows, and the receiver opens it with its key:
		// keyInit sets the switch again and counts the key out.
		r.spendKey(0x10)
		r.settle()
		r.wantKeys(0)
		if len(r.made) != 0 {
			t.Errorf("writes %v, want none: the key went with the door", r.made)
		}
		if !r.noted("door 0x10 counted: this game spent a key on it") {
			t.Errorf("notes %q", r.notes)
		}
		r.roomChange()
		r.settle()
		r.wantKeys(0)
	})
	t.Run("a late counter", func(t *testing.T) {
		r := newRig(t, tb, drc)
		held(r)
		// Chest 2's flag lands, and its key; then the receiver's chest 2,
		// made before the flag, gives it again: the derive takes the
		// duplicate out with a -1, which the game, stalled, takes only
		// after the write's DefaultInFlight. Landing then, it is the
		// derive's own key, not one spent on the held door.
		r.land(addr.TableDRC, tbox(2))
		r.settle()
		r.wantKeys(2)
		r.race = func() { r.stall, r.race = true, nil }
		r.takeKey(2)
		for i := 0; !r.stall && i < 30; i++ {
			r.step()
		}
		if !r.stall {
			t.Fatalf("no write for the duplicate: %v", r.made)
		}
		r.run(2*derive.DefaultInFlight + time.Second)
		r.stall = false
		r.settle()
		r.wantKeys(2)
		if r.noted("this game spent a key on it") || len(r.made) != 2 {
			t.Errorf("writes %v, notes %q: want the +1 and the -1 alone, door 0x10 held", r.made, r.notes)
		}
		if k := r.d.Status().Keys; !slices.ContainsFunc(k, func(k derive.KeyStatus) bool { return slices.Equal(k.Held, []int{0x10}) }) {
			t.Errorf("status %+v, want door 0x10 held", k)
		}
		r.roomChange()
		r.settle()
		r.wantKeys(1)
	})
	t.Run("another door's key", func(t *testing.T) {
		r := newRig(t, tb, drc)
		r.run(time.Second)
		r.takeKey(1)
		r.takeKey(2)
		r.settle()
		r.land(addr.TableDRC, sw(0x10))
		r.settle()
		r.wantKeys(2)
		// A door the receiver opens itself is counted at once, and leaves
		// the held one held.
		r.spendKey(0x11)
		r.settle()
		r.wantKeys(1)
		if k := r.d.Status().Keys; !slices.ContainsFunc(k, func(k derive.KeyStatus) bool { return slices.Equal(k.Held, []int{0x10}) }) {
			t.Errorf("status %+v, want door 0x10 held", k)
		}
		r.roomChange()
		r.settle()
		r.wantKeys(0)
	})
	t.Run("a stage change", func(t *testing.T) {
		r := newRig(t, tb, drc)
		held(r)
		r.load(drcBoss)
		r.settle()
		r.wantKeys(0)
	})
	t.Run("a failed read", func(t *testing.T) {
		r := newRig(t, tb, drc)
		held(r)
		r.failRead()
		r.settle()
		r.wantKeys(0)
	})
	t.Run("the game's own door", func(t *testing.T) {
		r := newRig(t, tb, drc)
		r.run(time.Second)
		r.takeKey(1)
		r.takeKey(2)
		r.settle()
		r.spendKey(0x10)
		r.land(addr.TableDRC, sw(0x10)) // the partner's too: nothing new to write
		r.settle()
		r.wantKeys(1)
		if len(r.made) != 0 || r.noted("is counted after your next room change") {
			t.Errorf("writes %v, notes %q", r.made, r.notes)
		}
	})
	t.Run("in the saved slot already", func(t *testing.T) {
		r := newRig(t, tb, drc, func(r *rig) {
			a, _ := addr.SavedMemBitAddr(addr.TableDRC)
			r.or(a+uint32(sw(0x10).Off), sw(0x10).Mask)
		})
		r.run(time.Second)
		r.takeKey(1)
		r.takeKey(2)
		r.settle()
		r.wantKeys(1) // the saved switch's door took the second key
		n := len(r.made)
		r.land(addr.TableDRC, sw(0x10)) // the live copy only gains it
		r.settle()
		r.wantKeys(1)
		if len(r.made) != n || r.noted("is counted after your next room change") {
			t.Errorf("writes %v, want none: the door counted all along", r.made[n:])
		}
	})
	t.Run("another stage of the table", func(t *testing.T) {
		r := newRig(t, tb, drcBoss)
		r.run(time.Second)
		r.land(addr.TableDRC, tbox(1))
		r.settle()
		r.wantKeys(1)
		r.land(addr.TableDRC, sw(0x10)) // its door is in M_NewD2, made as Link goes in
		r.settle()
		r.wantKeys(0)
	})
	// A cold table's door: TestDoorOpenedRemotely's "another table".
}

func TestKeySpentOnAnOpenDoor(t *testing.T) {
	r := newRig(t, vanillaTables(t), drc)
	r.run(time.Second)
	r.takeKey(1)
	r.settle()
	// The partner opened door 0x10 too; before its switch lands, the
	// receiver spends its own key on it.
	r.spendKey(0x10)
	r.run(500 * time.Millisecond)
	r.land(addr.TableDRC, sw(0x10))
	r.settle()
	r.wantKeys(0)
	if len(r.made) != 0 {
		t.Errorf("writes %v, want none: the door counts once", r.made)
	}
}

func TestKeyLiveWithoutHUD(t *testing.T) {
	r := newRig(t, vanillaTables(t), drcBoss)
	r.run(time.Second)
	r.land(addr.TableDRC, tbox(3))
	r.settle()
	r.wantKeys(1)
	if k := r.kinds(); !slices.Equal(k, []derive.Kind{derive.KeyLive}) {
		t.Errorf("writes %v, want the live mKeyNum: the counter would wait for a stage with the key HUD", r.made)
	}
	if got := r.u16(addr.ItemKeyNumCount); got != 0 {
		t.Errorf("mItemKeyNumCount = %d, want 0", got)
	}
}

func TestInvariantFailedTableDisabled(t *testing.T) {
	tb := *vanillaTables(t)
	kr := *tb.Keys
	kr.Tables = slices.Clone(kr.Tables)
	for i := range kr.Tables {
		if kr.Tables[i].SaveTable == addr.TableDRC {
			kr.Tables[i].Pass = false
			kr.Tables[i].Problems = []string{"5 small-key source(s) but 4 key door(s)"}
		}
	}
	tb.Keys = &kr
	r := newRig(t, &tb, drc)
	var off []string
	for _, o := range r.d.Disabled() {
		off = append(off, o.What)
	}
	if !slices.Equal(off, []string{"small keys, table 3 (DRC)"}) {
		t.Fatalf("Disabled = %q, want DRC's keys alone", off)
	}
	r.run(time.Second)
	r.land(addr.TableDRC, tbox(1))
	r.land(addr.TableDRC, tbox(2))
	r.settle()
	r.wantKeys(0)
	if len(r.made) != 0 {
		t.Errorf("writes %v for a table whose invariant fails", r.made)
	}
	if !r.noted("small keys, table 3 (DRC): not derived: its M1 key invariant fails (coop-tables): 5 small-key source(s)") {
		t.Errorf("notes %q", r.notes)
	}
	// Other tables still derive.
	r.land(addr.TableFW, tbox(1))
	r.settle()
	if got := r.savedKeys(addr.TableFW); got != 1 {
		t.Errorf("Forbidden Woods' saved mKeyNum = %d, want 1", got)
	}
}

// TestUnknownKey: a key no source explains is kept while the Deriver
// tracks the file, and is a source like the others: a door takes it once
// keys(t) is 0 or below, and the derive never gives it back.
func TestUnknownKey(t *testing.T) {
	r := newRig(t, vanillaTables(t), drc)
	r.run(time.Second)
	r.count(addr.ItemKeyNumCount, 1) // no source of the table set this visit
	r.settle()
	r.wantKeys(1)
	if !r.noted("1 key(s) no known source explains: kept (1 kept in all)") {
		t.Fatalf("notes %q", r.notes)
	}
	r.land(addr.TableDRC, tbox(1))
	r.settle()
	r.wantKeys(2)
	r.land(addr.TableDRC, sw(0x10)) // the partner's door takes the chest's key
	r.roomChange()                  // once the door here has looked at it again
	r.settle()
	r.wantKeys(1)
	// keys(t) is 0: the receiver's door spends the kept key, and keys(t)
	// of -1 is no debt the extra key pays twice.
	r.spendKey(0x11)
	r.settle()
	r.wantKeys(0)
	r.land(addr.TableDRC, sw(0x12))
	r.roomChange()
	r.settle()
	r.wantKeys(0)
	// keys(t) is 3 - 3 = 0, and the kept key is spent: 1.
	r.land(addr.TableDRC, tbox(2))
	r.land(addr.TableDRC, tbox(3))
	r.settle()
	r.wantKeys(1)
	var deltas []int
	for _, w := range r.made {
		deltas = append(deltas, w.Delta())
	}
	if want := []int{1, -1, 1}; !slices.Equal(deltas, want) {
		t.Errorf("counter writes %v (%v), want %v: no door's key is given back", deltas, r.made, want)
	}
}

// failRead is a tick whose snapshot read failed, as the replica passes it
// on (a Tick with no snapshot).
func (r *rig) failRead() {
	r.d.Tick(derive.Input{Now: r.now})
	r.now = r.now.Add(tickEvery)
}

// TestResetTakesTheDoorsKey: the Deriver starts over between a partner's
// door switch landing and its key going (a read failing, the file
// select, a save state, a restart of the tool), and the key still goes:
// its new baseline is no key from an unknown source.
func TestResetTakesTheDoorsKey(t *testing.T) {
	tb := vanillaTables(t)
	for _, c := range []struct {
		name  string
		reset func(r *rig)
	}{
		{"a read fails", func(r *rig) { r.failRead() }},
		{"the file select and back", func(r *rig) {
			r.g.EnterFileSelect() // putSave: the saved slot holds the key and the switch
			r.run(time.Second)
			r.load(drc)
		}},
		{"a save state", func(r *rig) {
			hb, _ := mem.ReadU32(r.m, mailbox.SpawnTrigger)
			r.m.Store(mailbox.SpawnTrigger, binary.BigEndian.AppendUint32(nil, hb/2))
		}},
		{"a restart of the tool", func(r *rig) { r.d = derive.New(derive.Config{Tables: tb}) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, tb, drc)
			r.run(time.Second)
			r.takeKey(1)
			r.settle()
			r.wantKeys(1)
			// The partner opened door 0x10 with the same key; its switch
			// lands while the pause menu keeps the decrement back.
			r.g.SetPauseMenu(true)
			r.land(addr.TableDRC, sw(0x10))
			r.run(time.Second)
			r.wantKeys(1)
			r.g.SetPauseMenu(false)
			c.reset(r)
			r.settle()
			r.wantKeys(0)
			if !r.noted("1 key(s) beyond the 0 its flags give, a door's key not yet taken or a duplicate") {
				t.Errorf("notes %q", r.notes)
			}
			// Nothing to spend: a door opened now is no key given back.
			r.spendKey(0x11)
			r.settle()
			r.wantKeys(0)
			if len(r.made) != 1 || r.made[0].Delta() != -1 {
				t.Errorf("writes %v, want one -1", r.made)
			}
		})
	}
}

// TestM3FileDoorKey: a file an M3 session left, whose partner's door
// switch synced with no key derive, loses that key on its first M4 tick.
func TestM3FileDoorKey(t *testing.T) {
	r := newRig(t, vanillaTables(t), drc, func(r *rig) {
		r.or(addr.LiveMemBit+uint32(tbox(1).Off), tbox(1).Mask)
		r.or(addr.LiveMemBit+uint32(sw(0x10).Off), sw(0x10).Mask)
		r.m.Store(addr.LiveKeyNum, []byte{1})
	})
	r.settle()
	r.wantKeys(0)
}

func TestKeyGates(t *testing.T) {
	tb := vanillaTables(t)
	for _, tt := range []struct {
		name  string
		close func(*rig)
		open  func(*rig)
	}{
		{"event", func(r *rig) { r.g.SetEvent(1, 0) }, func(r *rig) { r.g.SetEvent(0, 0) }},
		{"pause menu", func(r *rig) { r.g.SetPauseMenu(true) }, func(r *rig) { r.g.SetPauseMenu(false) }},
		{"unarmed", func(r *rig) { r.armed = false }, func(r *rig) { r.armed = true }},
		{"stage change pending", func(r *rig) {
			if err := r.g.SetNextStage(drc); err != nil {
				r.t.Fatal(err)
			}
		}, func(r *rig) {
			if err := r.g.ChangeStage(); err != nil {
				r.t.Fatal(err)
			}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, tb, drc)
			r.run(time.Second)
			tt.close(r)
			r.land(addr.TableDRC, tbox(1))
			r.run(5 * time.Second)
			if len(r.made) != 0 {
				t.Fatalf("written while closed: %v", r.made)
			}
			tt.open(r)
			// IDLE must stay open for the settle time before a write.
			r.run(500 * time.Millisecond)
			if len(r.made) != 0 && tt.name != "unarmed" && tt.name != "stage change pending" {
				t.Fatalf("written %v within a second of IDLE opening", r.made)
			}
			r.settle()
			r.wantKeys(1)
		})
	}
	t.Run("a count pending", func(t *testing.T) {
		r := newRig(t, tb, drcBoss) // no key HUD: a count stays pending
		r.run(time.Second)
		r.count(addr.ItemKeyNumCount, 1)
		r.land(addr.TableDRC, tbox(1))
		r.run(5 * time.Second)
		if len(r.made) != 0 {
			t.Fatalf("written with a key count pending: %v", r.made)
		}
	})
}

func TestDroppedWriteRetried(t *testing.T) {
	t.Run("dry run", func(t *testing.T) {
		r := newRig(t, vanillaTables(t), drc)
		r.dry = true
		r.run(time.Second)
		r.land(addr.TableDRC, tbox(1))
		for i := 0; len(r.dropped) == 0 && i < 30; i++ {
			r.step()
		}
		if len(r.dropped) != 1 {
			t.Fatalf("dropped %v, want one", r.dropped)
		}
		// Planned again only once settled anew, not every tick.
		r.run(900 * time.Millisecond)
		if len(r.dropped) != 1 {
			t.Fatalf("dropped %d plans within a second", len(r.dropped))
		}
		r.dry = false
		r.settle()
		r.wantKeys(1)
	})
	t.Run("the game counts first", func(t *testing.T) {
		r := newRig(t, vanillaTables(t), drc)
		r.run(time.Second)
		r.land(addr.TableDRC, tbox(1))
		// Between the snapshot and the write the receiver takes chest 2:
		// the counter no longer reads 0, so the write is not made.
		r.race = func() { r.takeKey(2); r.race = nil }
		r.settle()
		if len(r.dropped) != 1 {
			t.Fatalf("dropped %v, want one", r.dropped)
		}
		r.wantKeys(2)
		if len(r.made) != 1 || r.made[0].Delta() != 1 {
			t.Errorf("writes %v, want one +1", r.made)
		}
	})
}

func TestMaxLifeAddOnly(t *testing.T) {
	r := newRig(t, vanillaTables(t), outerSea)
	r.run(time.Second)
	life := func() int { return r.u16(addr.MaxLife) }
	if life() != derive.StartLife {
		t.Fatalf("a new file's max life = %d", life())
	}
	r.land(addr.TableSea, tbox(0)) // a partner's stage-file piece
	r.settle()
	if life() != 13 {
		t.Fatalf("max life = %d, want 13", life())
	}
	r.land(addr.TableDRC, stageLife) // Gohma's container, another table
	r.or(addr.Event+0x13, 0x40)      // Ivan's piece (event bit 0x1340)
	r.or(addr.Event+0xAC, 0x03)      // Baito's mother's letter, read
	r.settle()
	if life() != 13+4+2 {
		t.Fatalf("max life = %d, want 19", life())
	}
	// The HUD refills life to whole hearts of the new maximum.
	if got := r.u16(addr.Life); got != 16 {
		t.Errorf("life = %d, want 16", got)
	}
	for _, w := range r.made {
		if w.Kind != derive.LifeCounter || w.Delta() <= 0 {
			t.Errorf("write %v", w)
		}
	}

	// A duplicate pickup: the game counts a piece no flag explains. It is
	// kept and noted, never taken back.
	n := len(r.made)
	r.count(addr.ItemMaxLifeCount, 1)
	r.settle()
	if life() != 20 || len(r.made) != n {
		t.Fatalf("max life %d after a duplicate, writes %v", life(), r.made[n:])
	}
	if !r.noted("max life: 20 is 1 quarter heart(s) above what its flags give (19, with 0 kept before): a duplicate pickup") {
		t.Errorf("notes %q", r.notes)
	}
	// The partner's next piece is still credited past it.
	r.land(addr.TableSea, tbox(1))
	r.settle()
	if life() != 21 {
		t.Fatalf("max life = %d, want 21", life())
	}
}

func TestMaxLifeBaseline(t *testing.T) {
	// A file with a minigame's piece, which no save bit records.
	r := newRig(t, vanillaTables(t), outerSea, func(r *rig) {
		r.m.Store(addr.MaxLife, []byte{0, 13})
	})
	r.run(time.Second)
	if !r.noted("max life: keeping 1 quarter heart(s) beyond the 12 its flags give") {
		t.Errorf("notes %q", r.notes)
	}
	r.land(addr.TableSea, tbox(5))
	r.settle()
	if got := r.u16(addr.MaxLife); got != 14 {
		t.Errorf("max life = %d, want 14", got)
	}
}

func TestMaxLifeDisabled(t *testing.T) {
	tb := *vanillaTables(t)
	h := *tb.Hearts
	h.Pass, h.Problems = false, []string{"45 heart pieces"}
	tb.Hearts = &h
	r := newRig(t, &tb, outerSea)
	r.run(time.Second)
	r.land(addr.TableSea, tbox(0))
	r.settle()
	if got := r.u16(addr.MaxLife); got != derive.StartLife || len(r.made) != 0 {
		t.Errorf("max life %d, writes %v, with the heart table failing", got, r.made)
	}
	if !r.noted("max life: not derived: the M1 heart table fails its check") {
		t.Errorf("notes %q", r.notes)
	}
}

func TestMaxMagic(t *testing.T) {
	r := newRig(t, vanillaTables(t), outerSea)
	r.run(time.Second)
	r.or(addr.GetItem+6, 0x01) // the Deku Leaf's flag (mItemFlags[6] bit 0)
	r.settle()
	if got := r.u8(addr.MaxMagic); got != derive.MagicLeaf {
		t.Fatalf("max magic = %d, want 16", got)
	}
	if got := r.u8(addr.Magic); got != 0 {
		t.Errorf("magic = %d: the counter must not fill the meter", got)
	}
	r.or(addr.Event+0x31, 0x80) // the magic upgrade (event 0x3180)
	r.settle()
	if got := r.u8(addr.MaxMagic); got != derive.MagicCap {
		t.Fatalf("max magic = %d, want 32", got)
	}
	if len(r.made) != 2 || r.made[0].Delta() != 16 || r.made[1].Delta() != 16 {
		t.Errorf("writes %v", r.made)
	}
}

func TestMaxMagicAboveFlagsKept(t *testing.T) {
	// A second Deku Leaf pickup added 16 again (d_item.cpp:813).
	r := newRig(t, vanillaTables(t), outerSea, func(r *rig) {
		r.or(addr.GetItem+6, 0x01)
		r.m.Store(addr.MaxMagic, []byte{derive.MagicCap})
	})
	r.settle()
	if got := r.u8(addr.MaxMagic); got != derive.MagicCap || len(r.made) != 0 {
		t.Errorf("max magic %d, writes %v", got, r.made)
	}
	if !r.noted("max magic: 32 is above the 16 its flags give: kept") {
		t.Errorf("notes %q", r.notes)
	}
}

func TestFileChangeTakesNewBaseline(t *testing.T) {
	tb := vanillaTables(t)
	r := newRig(t, tb, drc)
	r.run(time.Second)
	r.count(addr.ItemKeyNumCount, 1) // a key this file keeps
	r.settle()
	r.wantKeys(1)
	// Back to file select, and a new file whose one key its flags give:
	// the first file's kept key is not the new file's.
	r.g.EnterFileSelect()
	r.run(time.Second)
	if err := r.g.NewGame(1, "Tetra"); err != nil {
		t.Fatal(err)
	}
	r.load(drc)
	r.or(addr.LiveMemBit+uint32(tbox(2).Off), tbox(2).Mask)
	r.m.Store(addr.LiveKeyNum, []byte{1})
	r.settle()
	r.wantKeys(1)
	if len(r.made) != 0 {
		t.Errorf("writes %v", r.made)
	}
	// Its own kept key is measured afresh too.
	r.count(addr.ItemKeyNumCount, 1)
	r.settle()
	r.wantKeys(2)
	if !r.noted("(1 kept in all)") || r.noted("(2 kept in all)") {
		t.Errorf("notes %q", r.notes)
	}
}

// The derive counts what the receiver's own flags hold, so the partner's
// sources reach it only through records that sync: the story SyncMask for
// the event bits (§6.5), the MAX register allowlist for the letters.
func TestRecordsSync(t *testing.T) {
	tb := vanillaTables(t)
	ids := []uint16{derive.EventMagicUpgrade}
	for _, r := range tb.Hearts.Rewards {
		switch rec := tables.RecordOf(r); rec.Kind {
		case tables.RecordEventBit:
			ids = append(ids, rec.ID)
		case tables.RecordLetterRead:
			b := addr.EventByte(rec.ID)
			if c := class.Of(addr.OffEvent + b.Off); c.Class != class.Max || c.Mask&b.Mask != b.Mask {
				t.Errorf("%s: letter register 0x%04X is %v mask 0x%02X, not a MAX register", r.ID, rec.ID, c.Class, c.Mask)
			}
		}
	}
	for _, id := range ids {
		if !class.EventSyncs(id) {
			t.Errorf("event bit 0x%04X does not sync", id)
		}
	}
}
