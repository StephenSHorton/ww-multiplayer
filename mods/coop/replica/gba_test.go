package replica

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/synctables"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/keys"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/tablestest"
)

// The Tingle Tuner guard's fixture (gba.go): the M5 fixture, plus the
// sea's latching crystals 0x05 and 0x06, of which a Tingle Tuner switch
// (agbA) also names 0x05, and in Dragon Roost Cavern's room 1 an agbMW
// naming danOK2 and zoneOK2 and an agbCSW naming roomOK2. So 0x05,
// danOK2, zoneOK2 and roomOK2 sync only while no GBA is linked; 0x06,
// danOK, zoneOK and roomOK whatever the link.
const gbaSea, plainSea = 0x05, 0x06

var gbaFixture = sync.OnceValues(func() (*tables.Tables, error) {
	stages := m5Stages()
	for i, s := range stages {
		switch s.Name {
		case "sea":
			stages[i].Rooms = append(stages[i].Rooms, tablestest.Room(2,
				tablestest.Crystal(gbaSea), tablestest.Crystal(plainSea), tablestest.TingleTuner(0, gbaSea, 0xFF)))
		case drc.Name:
			stages[i] = tablestest.Dungeon(drc.Name, keys.TableDRC, append(m5Extra(),
				tablestest.TingleTuner(7, danOK2, zoneOK2), tablestest.TingleTuner(8, 0xFF, roomOK2))...)
		}
	}
	img := tablestest.ISO(stages)
	d, err := inject.NewDisc(bytes.NewReader(img), int64(len(img)))
	if err != nil {
		return nil, err
	}
	return tables.Build(d)
})

// gbaScopedFixture is the guard fixture without the sea's Tingle Tuner
// switch: its only GBA-dependent switches are danOK2, zoneOK2 and
// roomOK2, so the guard's memory mask is empty.
var gbaScopedFixture = sync.OnceValues(func() (*tables.Tables, error) {
	stages := m5Stages()
	for i, s := range stages {
		if s.Name == drc.Name {
			stages[i] = tablestest.Dungeon(drc.Name, keys.TableDRC, append(m5Extra(),
				tablestest.TingleTuner(7, danOK2, zoneOK2), tablestest.TingleTuner(8, 0xFF, roomOK2))...)
		}
	}
	img := tablestest.ISO(stages)
	d, err := inject.NewDisc(bytes.NewReader(img), int64(len(img)))
	if err != nil {
		return nil, err
	}
	return tables.Build(d)
})

// gbaTables are the guard fixture's tables, checked to give the verdicts
// the tests rest on.
func gbaTables(t testing.TB) *tables.Tables {
	t.Helper()
	tb, err := gbaFixture()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name        string
		synced, gba bool
	}{
		{"sea 0x05", synctables.SyncedMemorySwitch(tb, addr.TableSea, gbaSea), synctables.GBAMemorySwitch(tb, addr.TableSea, gbaSea)},
		{"sea 0x06", synctables.SyncedMemorySwitch(tb, addr.TableSea, plainSea), !synctables.GBAMemorySwitch(tb, addr.TableSea, plainSea)},
		{"danOK", synctables.SyncedScopedSwitch(tb, keys.TableDRC, drc.Name, -1, danOK), !synctables.GBAScopedSwitch(tb, keys.TableDRC, drc.Name, -1, danOK)},
		{"danOK2", synctables.SyncedScopedSwitch(tb, keys.TableDRC, drc.Name, -1, danOK2), synctables.GBAScopedSwitch(tb, keys.TableDRC, drc.Name, -1, danOK2)},
		{"zoneOK", synctables.SyncedScopedSwitch(tb, keys.TableDRC, drc.Name, 1, zoneOK), !synctables.GBAScopedSwitch(tb, keys.TableDRC, drc.Name, 1, zoneOK)},
		{"zoneOK2", synctables.SyncedScopedSwitch(tb, keys.TableDRC, drc.Name, 1, zoneOK2), synctables.GBAScopedSwitch(tb, keys.TableDRC, drc.Name, 1, zoneOK2)},
		{"roomOK2", synctables.SyncedScopedSwitch(tb, keys.TableDRC, drc.Name, 1, roomOK2), synctables.GBAScopedSwitch(tb, keys.TableDRC, drc.Name, 1, roomOK2)},
	} {
		if !c.synced || !c.gba {
			t.Fatalf("the guard fixture's %s: synced %v, GBA verdict as expected %v", c.name, c.synced, c.gba)
		}
	}
	return tb
}

// TestGBAGuardMask: newGBAGuard takes the GBA-dependent memory switches
// the policy opens, and counts every GBA-dependent switch; the WriteGuard
// shuts them only on a tick that says a GBA may be linked.
func TestGBAGuardMask(t *testing.T) {
	tb := gbaTables(t)
	pol := switchPolicy(t, gbaSea, plainSea)
	g := newGBAGuard(pol, tb)
	off, m5 := switchImageByte(addr.TableSea, gbaSea)
	_, m6 := switchImageByte(addr.TableSea, plainSea)
	var want world.Policy
	want[off] = m5
	if g.mask != want {
		t.Errorf("mask has more than sea 0x05: %v", g.mask)
	}
	if g.switches != 4 { // sea 0x05, danOK2, zoneOK2, roomOK2
		t.Errorf("%d GBA-dependent switches, want 4", g.switches)
	}
	if none := newGBAGuard(pol, nil); none.mask != (world.Policy{}) || none.switches != 0 {
		t.Error("no tables, yet a mask")
	}
	// A switch the policy doesn't open is not the guard's, nor counted.
	if g := newGBAGuard(switchPolicy(t, plainSea), tb); g.mask != (world.Policy{}) || g.switches != 3 {
		t.Errorf("with sea 0x05 shut: mask %v, %d switches (want none, 3: danOK2, zoneOK2, roomOK2)", g.mask, g.switches)
	}

	wg := NewWriteGuard(GuardConfig{Policy: pol, GBA: &g.mask})
	a := addr.SaveImage + uint32(off)
	wg.SetTick(Tick{Table: addr.TableSea})
	if got := wg.Mask(a); got != m5|m6 {
		t.Errorf("no GBA: mask 0x%02X, want 0x%02X", got, m5|m6)
	}
	wg.SetTick(Tick{Table: addr.TableSea, GBA: true})
	if got := wg.Mask(a); got != m6 {
		t.Errorf("GBA: mask 0x%02X, want 0x%02X", got, m6)
	}
	if got := wg.Mask(addr.LiveMemBit + uint32(off-addr.OffMemBit(addr.TableSea))); got != m6 {
		t.Errorf("GBA: the live copy's mask 0x%02X, want 0x%02X", got, m6)
	}
	// With only the GBA-dependent switch open, the byte is refused whole.
	wg = NewWriteGuard(GuardConfig{Policy: switchPolicy(t, gbaSea), GBA: &g.mask})
	wg.SetTick(Tick{Table: addr.TableSea, GBA: true})
	if wg.Mask(a) != 0 {
		t.Error("a byte of GBA-dependent switches alone is open while a GBA may be linked")
	}
}

// TestGBAHoldsMemorySwitches: while a GBA may be linked, the world's
// switch shared with the Tingle Tuner is not written, the other is; once
// the link is gone, it is. The log says each change once, and /status
// shows the hold.
func TestGBAHoldsMemorySwitches(t *testing.T) {
	rr := newRecRig(t, Config{Apply: true, Policy: switchPolicy(t, gbaSea, plainSea), Tables: gbaTables(t)},
		hub.FeatureImage|hub.FeatureSwitches|hub.FeatureLiveSwitches)
	rr.settleRooms()
	if st := rr.r.Status().GBA; st.MayLink || st.Switches != 4 {
		t.Fatalf("status before the link: %+v", st)
	}
	rr.g.SetGBALink(true)
	rr.tick()
	if st := rr.r.Status().GBA; !st.MayLink {
		t.Fatalf("status with the link: %+v", st)
	}
	off, m5 := switchImageByte(addr.TableSea, gbaSea)
	_, m6 := switchImageByte(addr.TableSea, plainSea)
	var img world.Image
	img[off] = m5 | m6
	rr.world(img)
	rr.ticks(gate5)
	if got := rr.liveByte(off); got&m6 == 0 || got&m5 != 0 {
		t.Fatalf("with a GBA linked the live byte is 0x%02X: want 0x%02X without 0x%02X\n%s", got, m6, m5, rr.log)
	}
	for _, w := range rr.r.Status().Planned {
		if int(w.Off) == off && w.Bits&m5 != 0 {
			t.Errorf("planned %+v while a GBA may be linked", w)
		}
	}
	rr.g.SetGBALink(false)
	rr.ticks(gate5)
	if got := rr.liveByte(off); got&m5 == 0 {
		t.Fatalf("after the link the live byte is 0x%02X, lacking 0x%02X\n%s", got, m5, rr.log)
	}
	if rr.log.count("a Game Boy Advance may be linked to this game") != 1 || rr.log.count("no Game Boy Advance is linked to this game any more") != 1 ||
		!rr.log.has("the 4 switch(es) the stage tables share with Tingle Tuner switches are neither proposed nor written") {
		t.Errorf("log:\n%s", rr.log)
	}
	rr.audit(true)
}

// gate5 is enough ticks for a live switch to be planned and written once
// the gates are open.
const gate5 = 5

// TestGBAHoldsProposals: a switch shared with the Tingle Tuner that rises
// while a GBA may be linked is not proposed, in GAINS or in the WORLD; the
// other is. It is in the baseline after, so it is never proposed later
// (it may be the Tuner's).
func TestGBAHoldsProposals(t *testing.T) {
	rr := newRecRig(t, Config{Apply: true, Policy: switchPolicy(t, gbaSea, plainSea), Tables: gbaTables(t)},
		hub.FeatureImage|hub.FeatureSwitches)
	rr.settleRooms()
	rr.hub.take()
	rr.g.SetGBALink(true)
	rr.onSwitch(gbaSea, 0)
	rr.onSwitch(plainSea, 0)
	off, m5 := switchImageByte(addr.TableSea, gbaSea)
	_, m6 := switchImageByte(addr.TableSea, plainSea)
	sent := switchGains(rr, off, 3)
	if sent&m6 == 0 || sent&m5 != 0 {
		t.Fatalf("GAINS of the switch byte with a GBA linked: 0x%02X, want 0x%02X alone", sent, m6)
	}
	rr.r.mu.Lock()
	wm := rr.r.worldMsg(rr.r.last)
	rr.r.mu.Unlock()
	if wm.Image[off]&m5 != 0 || wm.Image[off]&m6 == 0 {
		t.Errorf("WORLD with a GBA linked has 0x%02X", wm.Image[off])
	}
	rr.g.SetGBALink(false)
	if sent := switchGains(rr, off, 3); sent != 0 {
		t.Errorf("after the link, GAINS 0x%02X of the switch byte", sent)
	}
	rr.r.mu.Lock()
	wm = rr.r.worldMsg(rr.r.last)
	rr.r.mu.Unlock()
	if wm.Image[off]&(m5|m6) != m5|m6 {
		t.Errorf("WORLD without a GBA has 0x%02X", wm.Image[off])
	}
}

// switchGains runs n ticks and ORs the GAINS the replica sent for image byte
// off.
func switchGains(rr *recRig, off, n int) byte {
	var sent byte
	for range n {
		rr.tick()
		for _, g := range all[*wire.GainsUp](rr.hub.take()) {
			for _, x := range g.Gains {
				if int(x.Off) == off {
					sent |= x.Val
				}
			}
		}
	}
	return sent
}

// TestGBAHoldsScopedWrites: while a GBA may be linked, the partner's dan
// and zone switches shared with the Tingle Tuner wait in their stores and
// its room edge of one is dropped, while the others are written; once the
// link is gone the stores' are written, the edge never.
func TestGBAHoldsScopedWrites(t *testing.T) {
	rr := newM5Rig(t, Config{Apply: true, Tables: gbaTables(t)}, m5Features)
	rr.g.SetGBALink(true)
	rr.tick()
	rr.send(danMsg(t, drc, danOK, danOK2), zoneMsg(t, drc, 1, zoneOK, zoneOK2), edgeMsg(t, drc, 1, roomOK, roomOK2))
	rr.settleM5()
	for _, c := range []struct {
		no   int
		want bool
	}{{danOK, true}, {danOK2, false}, {zoneOK, true}, {zoneOK2, false}, {roomOK, true}, {roomOK2, false}} {
		if rr.is(c.no, 1) != c.want {
			t.Errorf("with a GBA linked, 0x%02X set %v, want %v\n%s", c.no, !c.want, c.want, rr.log)
		}
	}
	if !rr.log.has("not written while a Game Boy Advance may be linked: dan switch 0x87") ||
		!rr.log.has("not written while a Game Boy Advance may be linked: zone switch 0xC5 in room 1") {
		t.Errorf("log:\n%s", rr.log)
	}
	if p := rr.r.Status().Puzzle; p.Refused != 0 {
		t.Errorf("held switches counted as refused: %+v", p)
	}
	rr.g.SetGBALink(false)
	rr.settleM5()
	for _, no := range []int{danOK2, zoneOK2} {
		if !rr.is(no, 1) {
			t.Errorf("after the link, 0x%02X is not set\n%s", no, rr.log)
		}
	}
	if rr.is(roomOK2, 1) {
		t.Error("a room edge dropped while a GBA was linked was written later")
	}
	rr.audit()
}

// TestGBAHoldsScopedProposals: the scoped sender leaves a switch shared
// with the Tingle Tuner out of its SCOPED while a GBA may be linked, edges
// and whole words alike, and one that rose meanwhile stays out once the
// link is gone (it may be the Tuner's): the refresh never proposes it,
// nor a room entry, while one that rises after the link is proposed. A
// drop of the baselines (the title) forgets it, as the WORLD after one
// carries a memory switch.
func TestGBAHoldsScopedProposals(t *testing.T) {
	rr := newM5Rig(t, Config{Apply: true, Tables: gbaTables(t)}, m5Features)
	rr.g.SetGBALink(true)
	rr.tick()
	rr.hub.take()
	if err := rr.g.OnSwitch(danOK, 1); err != nil {
		t.Fatal(err)
	}
	if err := rr.g.OnSwitch(danOK2, 1); err != nil {
		t.Fatal(err)
	}
	bit := func(no int) uint32 { return 1 << ((no - addr.SwitchDanFirst) % 32) }
	danSent := func(ticks int) uint32 {
		var got uint32
		for range ticks {
			rr.tick()
			for _, m := range all[*wire.Scoped](rr.hub.take()) {
				if m.Kind != wire.ScopeDan {
					continue
				}
				for _, w := range m.Words {
					if w.Word == 0 {
						got |= w.Bits
					}
				}
			}
		}
		return got
	}
	if got := danSent(2 * scopeRefresh); got&bit(danOK) == 0 || got&bit(danOK2) != 0 {
		t.Fatalf("with a GBA linked, the dan words sent 0x%08X: want danOK's bit without danOK2's", got)
	}
	rr.g.SetGBALink(false)
	if got := danSent(2 * scopeRefresh); got&bit(danOK2) != 0 || got&bit(danOK) == 0 {
		t.Errorf("after the link, the refresh sent 0x%08X: want danOK's bit without danOK2's, set while linked", got)
	}
	// A zone switch shared with the Tuner that rises after the link is
	// proposed, through a room change and back.
	if err := rr.g.OnSwitch(zoneOK2, 1); err != nil {
		t.Fatal(err)
	}
	zbit := uint32(1) << ((zoneOK2 - addr.SwitchZoneFirst) % 16)
	zoneSent := func() uint32 {
		var got uint32
		for range 2 * scopeRefresh {
			rr.tick()
			for _, m := range all[*wire.Scoped](rr.hub.take()) {
				if m.Kind == wire.ScopeZone && m.Room == 1 {
					for _, w := range m.Words {
						if int(w.Word) == (zoneOK2-addr.SwitchZoneFirst)/16 {
							got |= w.Bits
						}
					}
				}
			}
		}
		return got
	}
	if got := zoneSent(); got&zbit == 0 {
		t.Errorf("zoneOK2, set after the link, not proposed: 0x%04X", got)
	}
	if got := danSent(2 * scopeRefresh); got&bit(danOK2) != 0 {
		t.Errorf("danOK2 proposed later: 0x%08X", got)
	}
	// The title drops every baseline, and the taint with them.
	rr.r.mu.Lock()
	rr.r.differ.Drop()
	rr.r.mu.Unlock()
	if got := danSent(2 * scopeRefresh); got&bit(danOK2) == 0 {
		t.Errorf("after a drop, the refresh sent 0x%08X, without danOK2's bit", got)
	}
}

// TestGBATaintsScopeEntry: a dan or zone switch shared with the Tingle
// Tuner that a scope already has when Link enters it while a GBA may be
// linked is tainted too: it may have been set by the Tuner unseen.
func TestGBATaintsScopeEntry(t *testing.T) {
	rr := newM5Rig(t, Config{Apply: true, Tables: gbaTables(t)}, m5Features)
	rr.g.SetGBALink(true)
	rr.tick()
	rr.enter(2, 1) // Link in room 2, room 1 loaded beside it
	rr.settleM5()
	if err := rr.g.OnSwitch(zoneOK2, 1); err != nil { // room 1's zone, unseen
		t.Fatal(err)
	}
	rr.enter(1, 2)
	rr.settleM5()
	rr.g.SetGBALink(false)
	rr.hub.take()
	for range 2 * scopeRefresh {
		rr.tick()
		for _, m := range all[*wire.Scoped](rr.hub.take()) {
			if m.Kind != wire.ScopeZone || m.Room != 1 {
				continue
			}
			for _, w := range m.Words {
				if int(w.Word) == (zoneOK2-addr.SwitchZoneFirst)/16 && w.Bits&(1<<((zoneOK2-addr.SwitchZoneFirst)%16)) != 0 {
					t.Fatalf("zoneOK2 of room 1, there as Link entered with a GBA linked, proposed after the link: %+v", m)
				}
			}
		}
	}
}

// TestGBARecheck: a write batch planned on a tick that saw no GBA is
// aborted when a GBA may be linked by the time it is written.
func TestGBARecheck(t *testing.T) {
	rr := newRecRig(t, Config{Apply: true, Policy: switchPolicy(t, gbaSea, plainSea), Tables: gbaTables(t)},
		hub.FeatureImage|hub.FeatureSwitches|hub.FeatureLiveSwitches)
	rr.settleRooms()
	off, m5 := switchImageByte(addr.TableSea, gbaSea)
	var img world.Image
	img[off] = m5
	rr.world(img)
	// The link comes up between the snapshot (heartbeat read 1) and the
	// write (the writer's reads after it).
	rr.bm.onHeartbeat(func(n int) {
		if n == 2 {
			rr.g.SetGBALink(true)
		}
	})
	rep := rr.tick()
	rr.bm.onHeartbeat(nil)
	if rep.Wrote != 0 || rr.liveByte(off)&m5 != 0 || !strings.Contains(errString(rep.WriteErr), "a Game Boy Advance may be linked since the snapshot") {
		t.Fatalf("the write went through a link that came up after the snapshot: %+v\n%s", rep, rr.log)
	}
	if n := rr.ticks(gate5); n != 0 || rr.liveByte(off)&m5 != 0 {
		t.Fatalf("written while a GBA may be linked\n%s", rr.log)
	}
}

// TestGBARecheckScoped: with tables whose only switches shared with the
// Tingle Tuner are dan, zone and room switches (the guard's memory mask
// empty), an M5 batch planned on a tick that saw no GBA is still aborted
// when a GBA may be linked by the time it is written.
func TestGBARecheckScoped(t *testing.T) {
	tb, err := gbaScopedFixture()
	if err != nil {
		t.Fatal(err)
	}
	if !synctables.GBAScopedSwitch(tb, keys.TableDRC, drc.Name, -1, danOK2) || synctables.GBAMemorySwitch(tb, addr.TableSea, gbaSea) {
		t.Fatal("the scoped-only fixture's verdicts")
	}
	rr := newM5Rig(t, Config{Apply: true, Tables: tb}, m5Features)
	if rr.r.gba.mask != (world.Policy{}) || rr.r.gba.switches != 3 {
		t.Fatalf("guard: mask %v, %d switches", rr.r.gba.mask, rr.r.gba.switches)
	}
	rr.send(danMsg(t, drc, danOK2), zoneMsg(t, drc, 1, zoneOK2))
	// Each tick's snapshot sees no GBA; the link comes up before the
	// writer's reads after it.
	aborted := 0
	for range gate.StableTicks + 4 + roomSettle/m5Frames + 1 {
		rr.g.SetGBALink(false)
		rr.bm.onHeartbeat(func(n int) {
			if n == 2 {
				rr.g.SetGBALink(true)
			}
		})
		if rep := rr.tick(); strings.Contains(errString(rep.WriteErr), "a Game Boy Advance may be linked since the snapshot") {
			aborted++
		}
		rr.bm.onHeartbeat(nil)
	}
	if rr.is(danOK2, 1) || rr.is(zoneOK2, 1) {
		t.Fatalf("a dan or zone switch shared with the Tuner written through a link that came up after the snapshot\n%s", rr.log)
	}
	if aborted == 0 {
		t.Errorf("no batch aborted:\n%s", rr.log)
	}
	rr.g.SetGBALink(false)
	rr.settleM5()
	if !rr.is(danOK2, 1) || !rr.is(zoneOK2, 1) {
		t.Errorf("not written once no GBA was linked\n%s", rr.log)
	}
	rr.audit()
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
