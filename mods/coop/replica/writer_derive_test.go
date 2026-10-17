package replica

import (
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/derive"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/proto"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// newDeriveRig is a writer rig with M4's targets, in Dragon Roost Cavern
// (whose stage shows the key counter), its drain watch drainFrames long.
func newDeriveRig(t *testing.T, drainFrames uint32) *wrig {
	t.Helper()
	r := newWriterRig(t, GuardConfig{Targets: M3Targets | M4Targets, Policy: proto.SessionPolicy(nil)})
	fw, err := NewFrameWriter(WriterConfig{Mem: r.hm, Guard: r.wg, BeatTimeout: testBeat, Logf: r.log.logf, DrainFrames: drainFrames})
	if err != nil {
		t.Fatal(err)
	}
	r.fw = fw
	r.load(wDRC)
	r.settle()
	return r
}

// applyDerived runs a derive batch with the game beating on the writer's
// second heartbeat read.
func (r *wrig) applyDerived(ds ...DeriveWrite) (DeriveResult, error) {
	r.hm.beatOn(2, r.g.Frame)
	defer r.hm.beatOn(0, nil)
	return r.fw.ApplyDerived(context.Background(), ds)
}

func (r *wrig) u16At(a uint32) uint16 {
	r.t.Helper()
	return binary.BigEndian.Uint16(r.bytesAt(a, 2))
}

func (r *wrig) bytesAt(a uint32, n int) []byte {
	r.t.Helper()
	b, err := r.m.Read(a, n)
	if err != nil {
		r.t.Fatal(err)
	}
	return b
}

// stores writes b at a as the game would.
func (r *wrig) stores(a uint32, b []byte) {
	r.t.Helper()
	if err := r.m.Store(a, b); err != nil {
		r.t.Fatal(err)
	}
}

// deriveAudit checks what the recorder saw: every write a derive write's
// whole (a counter's two bytes at once), inside M4's allowlist.
func (r *wrig) deriveAudit() {
	r.t.Helper()
	g := NewGuardFor(M4Targets)
	for _, w := range r.m.Writes() {
		if !g.Allows(w.Addr, len(w.Data)) {
			r.t.Fatalf("a write at 0x%08X, outside M4's allowlist", w.Addr)
		}
		if c := counterAt(w.Addr); g.TargetOf(w.Addr) == TargetCounters && (len(w.Data) != 2 || w.Addr != counterAddr(c)) {
			r.t.Fatalf("a %d-byte write at 0x%08X: a counter is written whole", len(w.Data), w.Addr)
		}
	}
}

func counterAddr(c gate.Counter) uint32 {
	switch c {
	case gate.MaxLife:
		return addr.ItemMaxLifeCount
	case gate.MaxMagic:
		return addr.ItemMaxMagicCount
	}
	return addr.ItemKeyNumCount
}

func TestApplyDerivedCountersDrain(t *testing.T) {
	r := newDeriveRig(t, 0)
	r.m.Record(true)
	life0 := r.u16At(imageAddr(addr.OffMaxLife))
	ds := []DeriveWrite{
		counterWrite(DeriveKeyCounter, addr.ItemKeyNumCount, addr.TableDRC, 2),
		counterWrite(DeriveLifeCounter, addr.ItemMaxLifeCount, -1, 4),
		counterWrite(DeriveMagicCounter, addr.ItemMaxMagicCount, -1, derive.MagicLeaf),
	}
	res, err := r.applyDerived(ds...)
	if err != nil || res.Made() != 3 || len(res.Wrote) != 6 {
		t.Fatalf("ApplyDerived = %+v, %v; want 3 writes made, 6 bytes", res, err)
	}
	if n := len(r.m.Writes()); n != 3 {
		t.Fatalf("%d memory writes, want 3 (each counter in one)", n)
	}
	r.tick() // the HUD takes them
	r.tick()
	if st := r.fw.Stats(); st.Drained != 3 || st.Derived != 3 {
		t.Fatalf("stats %+v: want 3 made and drained", st)
	}
	if k := r.byteAt(addr.LiveKeyNum); k != 2 {
		t.Errorf("mKeyNum %d, want 2", k)
	}
	if l := r.u16At(imageAddr(addr.OffMaxLife)); l != life0+4 {
		t.Errorf("mMaxLife %d, want %d", l, life0+4)
	}
	if m := r.byteAt(imageAddr(addr.OffMaxMagic)); m != derive.MagicLeaf {
		t.Errorf("mMaxMagic %d, want %d", m, derive.MagicLeaf)
	}
	if off, _ := r.fw.Disabled(); off {
		t.Fatalf("co-op turned off:\n%s", r.log.String())
	}
	r.deriveAudit()
}

// TestApplyDerivedCounterOnlyAtZero: a counter the game has a count
// pending in is not written, whether the count shows in the tick's
// snapshot (the gate is closed) or lands between it and the beat (the
// write is stale): either way nothing is written and nothing is a fault.
func TestApplyDerivedCounterOnlyAtZero(t *testing.T) {
	r := newDeriveRig(t, 0)
	r.m.Record(true)
	w := counterWrite(DeriveKeyCounter, addr.ItemKeyNumCount, addr.TableDRC, 1)

	// A key the game counts in, seen by the snapshot.
	r.stores(addr.ItemKeyNumCount, []byte{0, 1})
	r.observe()
	res, err := r.applyDerived(w)
	if !errors.Is(err, ErrClosed) || res.Outcomes[0] != DeriveClosed || len(res.Wrote) != 0 {
		t.Fatalf("over a pending count: %+v, %v; want it closed", res, err)
	}

	// The HUD takes it; then the game counts another in after the
	// snapshot, before the beat.
	r.tick()
	if r.u16At(addr.ItemKeyNumCount) != 0 || !r.st.CanWriteCounter(gate.KeyNum) {
		t.Fatal("the HUD did not take the game's key")
	}
	r.hm.beatOn(2, func() {
		r.g.Frame()
		r.stores(addr.ItemKeyNumCount, []byte{0, 1})
	})
	res, err = r.fw.ApplyDerived(context.Background(), []DeriveWrite{w})
	r.hm.beatOn(0, nil)
	if err != nil || res.Outcomes[0] != DeriveStale || len(res.Wrote) != 0 {
		t.Fatalf("a count landing before the beat: %+v, %v; want it stale", res, err)
	}
	if n := len(r.m.Writes()); n != 0 {
		t.Fatalf("%d writes made", n)
	}
	if st := r.fw.Stats(); st.Stale != 1 || st.Refused != 0 {
		t.Fatalf("stats %+v", st)
	}
	if g := r.wg.Stats(); g.Refused != 0 {
		t.Fatalf("guard refused %+v", g)
	}
}

// TestApplyDerivedCompareAndSet: a slot, the equipment and a button are
// moved only while they still read what the plan saw.
func TestApplyDerivedCompareAndSet(t *testing.T) {
	r := newDeriveRig(t, 0)
	r.m.Record(true)
	bow := imageAddr(addr.OffItems + save.SlotBow)
	tele := imageAddr(addr.OffItems + save.SlotTelescope)
	one := func(k DeriveKind, a uint32, old, val byte) DeriveWrite {
		return DeriveWrite{Kind: k, Table: -1, Addr: a, Expect: []byte{old}, Data: []byte{val}, Reason: "test"}
	}
	// X shows the empty slot it is bound to, as dComIfGp_setSelectItem
	// leaves it (d_com_inf_game.h:2924-2938).
	r.store(addr.PlaySelectItem, save.ItemNone)
	r.tick()
	// The game hands the bow over itself between the snapshot and the
	// beat; the telescope it does not.
	r.hm.beatOn(2, func() {
		r.g.Frame()
		r.stores(bow, []byte{save.ItemBow})
	})
	res, err := r.fw.ApplyDerived(context.Background(), []DeriveWrite{
		one(DeriveSlot, bow, save.ItemNone, save.ItemBow),
		one(DeriveSlot, tele, save.ItemNone, save.ItemTelescope),
		one(DeriveButton, addr.PlaySelectItem, save.ItemNone, save.ItemTelescope),
	})
	r.hm.beatOn(0, nil)
	if err != nil || res.Outcomes[0] != DeriveStale || res.Outcomes[1] != DeriveMade || res.Outcomes[2] != DeriveMade {
		t.Fatalf("ApplyDerived = %+v, %v", res, err)
	}
	if got := r.byteAt(tele); got != save.ItemTelescope {
		t.Fatalf("telescope slot 0x%02X", got)
	}
	if rep := r.tick(); rep.Verified != 2 || rep.Failed != 0 {
		t.Fatalf("verify %+v", rep)
	}

	// A slot the game moves again before the next tick reads back wrong:
	// a verify failure.
	res, err = r.applyDerived(one(DeriveEquip, imageAddr(addr.OffSelectEquip), save.ItemNone, save.ItemSword))
	if err != nil || res.Made() != 1 {
		t.Fatalf("the sword: %+v, %v", res, err)
	}
	r.stores(imageAddr(addr.OffSelectEquip), []byte{save.ItemNone})
	if rep := r.tick(); rep.Failed != 1 {
		t.Fatalf("verify %+v: want the sword's failure", rep)
	}
	r.deriveAudit()
}

// TestApplyDerivedRefusesBeforeTheWait: a write without its kind's shape
// is refused before the heartbeat is waited for, and the rest still go.
func TestApplyDerivedRefusesBeforeTheWait(t *testing.T) {
	r := newDeriveRig(t, 0)
	r.m.Record(true)
	res, err := r.applyDerived(
		counterWrite(DeriveLifeCounter, addr.ItemMaxLifeCount, -1, -4),
		counterWrite(DeriveMagicCounter, addr.ItemMaxMagicCount, -1, derive.MagicLeaf),
	)
	if !errors.Is(err, ErrGuard) || res.Outcomes[0] != DeriveRefused || res.Outcomes[1] != DeriveMade {
		t.Fatalf("ApplyDerived = %+v, %v", res, err)
	}
	if n := len(r.m.Writes()); n != 1 {
		t.Fatalf("%d writes, want the magic counter's", n)
	}

	// Nothing but refusals: no beat is waited for.
	polls := 0
	r.hm.hooks(func(a uint32, _ int) {
		if a == mailbox.SpawnTrigger {
			polls++
		}
	}, nil)
	res, err = r.fw.ApplyDerived(context.Background(), []DeriveWrite{{Kind: DeriveSlot, Table: -1, Addr: addr.NextStageEnable, Expect: []byte{0}, Data: []byte{1}}})
	r.hm.hooks(nil, nil)
	if !errors.Is(err, ErrGuard) || polls != 0 {
		t.Fatalf("mNextStage as a slot: %+v, %v after %d heartbeat polls", res, err, polls)
	}
	if g := r.wg.Stats(); g.ByReason[RefuseValue] != 1 || g.ByReason[RefuseTarget] != 1 {
		t.Fatalf("guard refusals %+v", g.ByReason)
	}
}

// TestApplyDerivedAbortsWhenNotIdle: an event begun since the snapshot
// aborts the batch; a dry run and co-op off write nothing.
func TestApplyDerivedAbortsWhenNotIdle(t *testing.T) {
	r := newDeriveRig(t, 0)
	r.m.Record(true)
	w := counterWrite(DeriveMagicCounter, addr.ItemMaxMagicCount, -1, derive.MagicLeaf)
	r.hm.beatOn(2, func() {
		r.g.Frame()
		r.g.SetEvent(1, 0)
	})
	res, err := r.fw.ApplyDerived(context.Background(), []DeriveWrite{w})
	r.hm.beatOn(0, nil)
	if !errors.Is(err, ErrAborted) || len(res.Wrote) != 0 {
		t.Fatalf("an event since the snapshot: %+v, %v", res, err)
	}
	r.g.SetEvent(0, 0)
	r.settle()

	dry, err := NewFrameWriter(WriterConfig{Mem: r.m, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dry.ApplyDerived(context.Background(), []DeriveWrite{w}); !errors.Is(err, ErrDryRun) {
		t.Fatalf("a dry run: %v", err)
	}
	r.fw.Stop("coop off")
	if _, err := r.applyDerived(w); !errors.Is(err, ErrOff) {
		t.Fatalf("co-op off: %v", err)
	}
	if n := len(r.m.Writes()); n != 0 {
		t.Fatalf("%d writes", n)
	}
}

// TestApplyDerivedUndrainedTripsTheBreaker: a counter the HUD never takes
// (here the stage stops showing the key counter right after the write, as
// if the write had gone where the meter does not read) turns co-op off
// once the HUD had DrainFrames frames to take it.
func TestApplyDerivedUndrainedTripsTheBreaker(t *testing.T) {
	const frames = 30
	r := newDeriveRig(t, frames)
	res, err := r.applyDerived(counterWrite(DeriveKeyCounter, addr.ItemKeyNumCount, addr.TableDRC, 1))
	if err != nil || res.Made() != 1 {
		t.Fatalf("ApplyDerived = %+v, %v", res, err)
	}
	prop := r.byteAt(fakegame.StagChunk + addr.StagOffProp)
	r.stores(fakegame.StagChunk+addr.StagOffProp, []byte{prop &^ addr.StagPropKeyDisp})
	ran := 0
	for ; ran < 4*frames; ran += 3 {
		r.tick()
		if off, _ := r.fw.Disabled(); off {
			break
		}
	}
	off, why := r.fw.Disabled()
	if !off || !strings.Contains(why, "the HUD never took") || ran < frames-3 {
		t.Fatalf("after %d frames: off %v (%q)\n%s", ran, off, why, r.log.String())
	}
	if _, err := r.applyDerived(counterWrite(DeriveMagicCounter, addr.ItemMaxMagicCount, -1, derive.MagicLeaf)); !errors.Is(err, ErrOff) {
		t.Fatalf("a write after the trip: %v", err)
	}
}

// TestDrainWatchCountsTheHUDsFrames: the watch counts only the frames the
// meter would take the counter in (hudTakes): none while Orca's training
// runs for max life, none while a partner is loaded for max magic, none
// under the pause menu. The frames here move the heartbeat alone, so
// nothing drains.
func TestDrainWatchCountsTheHUDsFrames(t *testing.T) {
	const frames = 30
	cases := []struct {
		name  string
		w     DeriveWrite
		pause func(g *fakegame.FakeGame, on bool)
	}{
		{"max life, Orca's training", counterWrite(DeriveLifeCounter, addr.ItemMaxLifeCount, -1, 4), func(g *fakegame.FakeGame, on bool) {
			var t uint8
			if on {
				t = miniGameOrca
			}
			g.SetMiniGame(t)
		}},
		{"max magic, a partner", counterWrite(DeriveMagicCounter, addr.ItemMaxMagicCount, -1, derive.MagicLeaf), (*fakegame.FakeGame).SetPartner},
		{"max magic, the pause menu", counterWrite(DeriveMagicCounter, addr.ItemMaxMagicCount, -1, derive.MagicLeaf), (*fakegame.FakeGame).SetPauseMenu},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newDeriveRig(t, frames)
			if res, err := r.applyDerived(c.w); err != nil || res.Made() != 1 {
				t.Fatalf("ApplyDerived = %+v, %v", res, err)
			}
			beat := func(n int) {
				for range n {
					r.store32(mailbox.SpawnTrigger, binary.BigEndian.Uint32(r.bytesAt(mailbox.SpawnTrigger, 4))+1)
				}
				r.observe()
			}
			c.pause(r.g, true)
			for range 4 * frames / 3 {
				beat(3)
			}
			if off, why := r.fw.Disabled(); off {
				t.Fatalf("tripped while the HUD could not take it: %s", why)
			}
			c.pause(r.g, false)
			for range frames/3 + 2 {
				beat(3)
			}
			if off, why := r.fw.Disabled(); !off || !strings.Contains(why, "the HUD never took") {
				t.Fatalf("not tripped once the HUD could take it: %q", why)
			}
		})
	}
}

// TestDrainWatchDropsOnAStageChange: a stage change, or a save state,
// ends the watch without a verdict: the counter is the new stage's.
func TestDrainWatchDropsOnAStageChange(t *testing.T) {
	r := newDeriveRig(t, 6)
	if res, err := r.applyDerived(counterWrite(DeriveKeyCounter, addr.ItemKeyNumCount, addr.TableDRC, 1)); err != nil || res.Made() != 1 {
		t.Fatalf("ApplyDerived = %+v, %v", res, err)
	}
	prop := r.byteAt(fakegame.StagChunk + addr.StagOffProp)
	r.stores(fakegame.StagChunk+addr.StagOffProp, []byte{prop &^ addr.StagPropKeyDisp})
	r.load(wOutset) // a stage that hides the key counter: the count stays pending
	for range 10 {
		r.tick()
	}
	if off, why := r.fw.Disabled(); off {
		t.Fatalf("tripped after a stage change: %s", why)
	}
	if st := r.fw.Stats(); st.Unverified == 0 {
		t.Fatalf("stats %+v: the watch was not dropped", st)
	}
}

func TestHUDTakes(t *testing.T) {
	partner := func(s *state.Snapshot) {
		binary.BigEndian.PutUint32(s.Player[addr.PlayerPtr+4-state.PlayerRegion.Start:], fakegame.PartnerActor)
	}
	cases := []struct {
		name string
		set  func(s *state.Snapshot)
		c    gate.Counter
		want bool
	}{
		{"keys", nil, gate.KeyNum, true},
		{"life", nil, gate.MaxLife, true},
		{"magic", nil, gate.MaxMagic, true},
		{"life in Orca's training", func(s *state.Snapshot) { s.MiniGameType = miniGameOrca }, gate.MaxLife, false},
		{"life in another minigame", func(s *state.Snapshot) { s.MiniGameType = 2 }, gate.MaxLife, true},
		{"magic in Orca's training", func(s *state.Snapshot) { s.MiniGameType = miniGameOrca }, gate.MaxMagic, true},
		{"magic with a partner", partner, gate.MaxMagic, false},
		{"life with a partner", partner, gate.MaxLife, true},
		{"keys under the pause menu", func(s *state.Snapshot) { s.MenuPause = 1 }, gate.KeyNum, false},
	}
	for _, c := range cases {
		var s state.Snapshot
		if c.set != nil {
			c.set(&s)
		}
		if got := hudTakes(c.c, &s); got != c.want {
			t.Errorf("%s: hudTakes = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestMergeAndEscapePathsRefuseDerivedBytes: a plan or an escape that
// holds a DERIVED byte, a counter or a button is refused before the wait,
// and writes nothing.
func TestMergeAndEscapePathsRefuseDerivedBytes(t *testing.T) {
	r := newDeriveRig(t, 0)
	r.m.Record(true)
	for _, off := range []int{addr.OffItems + save.SlotBow, addr.OffSelectEquip, addr.OffMemBit(addr.TableFW) + addr.MemBitOffKeyNum} {
		res, err := r.apply(plannedOR(off, 0x01, addr.TableDRC))
		var re *RefusalError
		if !errors.As(err, &re) || re.Reason != RefusePath || len(res.Wrote) != 0 {
			t.Errorf("%s through the merge path: %+v, %v", DefaultLabel(off, -1), res, err)
		}
	}
	for _, f := range []Field{
		{Addr: addr.ItemMaxLifeCount, Data: []byte{0, 4}},
		{Addr: addr.PlaySelectItem, Data: []byte{save.ItemTelescope}},
		{Addr: imageAddr(addr.OffItems + save.SlotBow), Data: []byte{save.ItemBow}},
	} {
		r.hm.beatOn(2, r.g.Frame)
		res, err := r.fw.ApplyFields(context.Background(), []Field{f})
		r.hm.beatOn(0, nil)
		if !errors.Is(err, ErrGuard) || len(res.Wrote) != 0 {
			t.Errorf("0x%08X through an escape: %+v, %v", f.Addr, res, err)
		}
	}
	if n := len(r.m.Writes()); n != 0 {
		t.Fatalf("%d writes", n)
	}
}
