package replica

import (
	"context"
	"errors"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// m5Zones is a zone table with Link in room 1, whose zone is slot 2; room
// 5's is slot 0; slot 3 is free; slot 4 still names room 1 (a zone of it
// removed and not reused: removeZone only marks a slot free when it frees
// it, d_save.h:969, so this is one a save state left) but room 1's mZoneNo
// is 2; room 7's mZoneNo is 40, past the 32 zones, and slot 6 names it.
func m5Zones() ZoneTable {
	zt := ZoneTable{Valid: true, Stay: 1}
	for i := range zt.ZoneNo {
		zt.ZoneNo[i] = -1
	}
	for i := range zt.RoomNo {
		zt.RoomNo[i] = -1
	}
	zt.ZoneNo[1], zt.RoomNo[2] = 2, 1
	zt.ZoneNo[5], zt.RoomNo[0] = 0, 5
	zt.RoomNo[4] = 1
	zt.ZoneNo[7], zt.RoomNo[6] = 40, 7
	return zt
}

// TestWriteGuardScopedZones: the guard itself lets a zone switch byte
// through only into the zone of the room Link is in, by the tick's zone
// table, and a dan switch byte only with a save table loaded; never the
// zone's other bytes, never a clear, never outside IDLE or unarmed.
func TestWriteGuardScopedZones(t *testing.T) {
	inGame := state.Snapshot{PlayerPtr: 0x80A00000, StageName: drc.Name, Table: addr.TableDRC}
	idle := settledGates(inGame)
	busy := inGame
	busy.EvtMode = 1
	sw := uint32(addr.ZoneOffSwitch)
	cases := []struct {
		name     string
		zones    func(zt *ZoneTable)
		table    int8
		busy     bool
		unarmed  bool
		a        uint32
		old, val byte
		ok       bool
	}{
		{name: "the zone of Link's room", a: zoneByteAddr(2, sw+1), val: 0x08, ok: true},
		{name: "its room switches", a: zoneByteAddr(2, sw+5), val: 0x02, ok: true},
		{name: "another room's zone", a: zoneByteAddr(0, sw+1), val: 0x08},
		{name: "a free slot", a: zoneByteAddr(3, sw+1), val: 0x08},
		{name: "a stale slot naming Link's room", a: zoneByteAddr(4, sw+1), val: 0x08},
		// mStayNo's mZoneNo still names slot 2, but the slot is no longer
		// room 1's: freed (removeZone, d_save.h:969), or made for another
		// room since (createZone, d_save.cpp:1525-1537).
		{name: "Link's zone, freed", zones: func(zt *ZoneTable) { zt.RoomNo[2] = -1 }, a: zoneByteAddr(2, sw+1), val: 0x08},
		{name: "Link's zone, now another room's", zones: func(zt *ZoneTable) { zt.RoomNo[2] = 5 }, a: zoneByteAddr(2, sw+1), val: 0x08},
		{name: "a room whose mZoneNo is past the zones", zones: func(zt *ZoneTable) { zt.Stay = 7 }, a: zoneByteAddr(6, sw+1), val: 0x08},
		{name: "no room loaded", zones: func(zt *ZoneTable) { zt.Stay = -1 }, a: zoneByteAddr(2, sw+1), val: 0x08},
		{name: "no zone table", zones: func(zt *ZoneTable) { *zt = ZoneTable{} }, a: zoneByteAddr(2, sw+1), val: 0x08},
		{name: "Link in another room", zones: func(zt *ZoneTable) { zt.Stay = 5 }, a: zoneByteAddr(2, sw+1), val: 0x08},
		{name: "the zone's mRoomNo", a: zoneByteAddr(2, addr.ZoneOffRoomNo), old: 1, val: 3},
		{name: "the zone's items", a: zoneByteAddr(2, addr.ZoneOffItem), val: 1},
		{name: "a zone switch clear", a: zoneByteAddr(2, sw+1), old: 0x08, val: 0},
		{name: "a zone switch in an event", busy: true, a: zoneByteAddr(2, sw+1), val: 0x08},
		{name: "a zone switch unarmed", unarmed: true, a: zoneByteAddr(2, sw+1), val: 0x08},
		{name: "a dan switch", a: addr.DanSwitch + 3, val: 0x20, ok: true},
		{name: "a dan switch with no table", table: -1, a: addr.DanSwitch + 3, val: 0x20},
		{name: "mDan.mStageNo", a: addr.DanStageNo, old: 3, val: 7},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			zt := m5Zones()
			if c.zones != nil {
				c.zones(&zt)
			}
			table := int8(addr.TableDRC)
			if c.table != 0 {
				table = c.table
			}
			gates := idle
			if c.busy {
				gates = settledGates(busy)
			}
			wg := NewWriteGuard(GuardConfig{Targets: M3Targets | M4Targets | M5Targets | TargetEscape, Policy: world.FullPolicy()})
			wg.SetTick(Tick{Gates: gates, Armed: !c.unarmed, Table: table, Zones: zt})
			err := wg.Check(c.a, c.old, c.val)
			if (err == nil) != c.ok {
				t.Fatalf("Check(0x%08X, 0x%02X -> 0x%02X) = %v, want ok %v", c.a, c.old, c.val, err, c.ok)
			}
			if !c.ok && !errors.Is(err, ErrGuard) {
				t.Fatalf("Check = %v, not ErrGuard", err)
			}
		})
	}
}

// applyScoped runs a scoped batch with the game beating on the writer's
// second heartbeat read.
func (r *wrig) applyScoped(ws ...ScopedWrite) (Result, error) {
	r.hm.beatOn(2, r.g.Frame)
	defer r.hm.beatOn(0, nil)
	return r.fw.ApplyScoped(context.Background(), ws)
}

// TestFrameWriterScoped: the scoped path writes a byte its plan found the
// zone of Link's room for, and verifies it, and refuses before the wait a
// byte that is no switch byte of its kind's store, or names a slot the
// tick's zone table does not give its room; nothing it refuses is
// written. A zone table that changes while the batch waits aborts it.
func TestFrameWriterScoped(t *testing.T) {
	newRig := func(t *testing.T) *wrig {
		r := newWriterRig(t, GuardConfig{Targets: M3Targets | M4Targets | M5Targets | TargetEscape})
		r.load(drcKeys)
		if err := r.g.EnterRoom(1, 2); err != nil { // room 1: slot 0; room 2: slot 1
			t.Fatal(err)
		}
		r.settle()
		return r
	}
	zone := func(t *testing.T, no, room, slot int) ScopedWrite {
		w, err := scopedWriteOf(wire.ScopeZone, no, room, slot)
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	edge := func(t *testing.T, no, room, slot int) ScopedWrite {
		w, err := scopedWriteOf(wire.ScopeRoomEdge, no, room, slot)
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	dan := func(t *testing.T, no int) ScopedWrite {
		w, err := scopedWriteOf(wire.ScopeDan, no, -1, -1)
		if err != nil {
			t.Fatal(err)
		}
		return w
	}

	t.Run("written and verified", func(t *testing.T) {
		r := newRig(t)
		res, err := r.applyScoped(zone(t, zoneOK, 1, 0), edge(t, roomOK, 1, 0), dan(t, danOK), dan(t, danOK2))
		if err != nil || len(res.Wrote) != 3 || res.Beat == 0 {
			t.Fatalf("ApplyScoped = %+v, %v; want 3 bytes after a beat", res, err)
		}
		for _, no := range []int{zoneOK, roomOK, danOK, danOK2} {
			if on, err := r.g.IsSwitch(no, 1); err != nil || !on {
				t.Fatalf("switch 0x%02X not set (%v)", no, err)
			}
		}
		if v := r.tick(); v.Verified != 3 || v.Failed != 0 {
			t.Fatalf("verify: %+v", v)
		}
		r.audit2()
	})
	t.Run("a byte already set is left", func(t *testing.T) {
		r := newRig(t)
		if err := r.g.OnSwitch(zoneOK, 1); err != nil {
			t.Fatal(err)
		}
		r.tick()
		res, err := r.applyScoped(zone(t, zoneOK, 1, 0))
		if err != nil || len(res.Wrote) != 0 || res.Already != 1 {
			t.Fatalf("ApplyScoped = %+v, %v", res, err)
		}
	})

	for _, c := range []struct {
		name   string
		w      func(t *testing.T) ScopedWrite
		reason Refusal
	}{
		{"another room's slot", func(t *testing.T) ScopedWrite { return zone(t, zoneOK, 1, 1) }, RefuseTarget},
		{"a room with no zone", func(t *testing.T) ScopedWrite { return zone(t, zoneOK, 3, 3) }, RefuseTarget},
		{"a free slot", func(t *testing.T) ScopedWrite { return zone(t, zoneOK, 1, 5) }, RefuseTarget},
		{"the zone's mRoomNo", func(t *testing.T) ScopedWrite {
			w := zone(t, zoneOK, 1, 0)
			w.Addr = zoneByteAddr(0, addr.ZoneOffRoomNo)
			return w
		}, RefuseTarget},
		{"the zone's items", func(t *testing.T) ScopedWrite {
			w := zone(t, zoneOK, 1, 0)
			w.Addr = zoneByteAddr(0, addr.ZoneOffItem)
			return w
		}, RefuseTarget},
		{"a zone write into the room switches", func(t *testing.T) ScopedWrite {
			w := zone(t, zoneOK, 1, 0)
			w.Addr = zoneByteAddr(0, addr.ZoneOffSwitch+5)
			return w
		}, RefuseTarget},
		{"a room edge into the zone switches", func(t *testing.T) ScopedWrite {
			w := edge(t, roomOK, 1, 0)
			w.Addr = zoneByteAddr(0, addr.ZoneOffSwitch+1)
			return w
		}, RefuseTarget},
		{"a dan byte naming a zone", func(t *testing.T) ScopedWrite {
			w := dan(t, danOK)
			w.Room, w.Slot = 1, 0
			return w
		}, RefuseTarget},
		{"mDan.mStageNo", func(t *testing.T) ScopedWrite {
			w := dan(t, danOK)
			w.Addr = addr.DanStageNo
			return w
		}, RefuseTarget},
		{"a kill byte", func(t *testing.T) ScopedWrite {
			w := zone(t, zoneOK, 1, 0)
			w.Kind = wire.ScopeKill
			return w
		}, RefuseTarget},
		{"no bits", func(t *testing.T) ScopedWrite {
			w := dan(t, danOK)
			w.Bits = 0
			return w
		}, RefuseValue},
	} {
		t.Run("refused: "+c.name, func(t *testing.T) {
			r := newRig(t)
			res, err := r.applyScoped(c.w(t))
			var re *RefusalError
			if !errors.Is(err, ErrGuard) || !errors.As(err, &re) || re.Reason != c.reason || res.Refused != 1 || len(res.Wrote) != 0 || res.Beat != 0 {
				t.Fatalf("ApplyScoped = %+v, %v; want a %v refusal before the wait", res, err, c.reason)
			}
			if n := len(r.m.Writes()); n != 0 {
				t.Fatalf("%d writes", n)
			}
		})
	}

	for _, c := range []struct {
		name  string
		storm func(r *wrig) error
	}{
		{"the zone moves to another slot", func(r *wrig) error {
			_, _, err := r.g.MoveZone(1, 5)
			return err
		}},
		{"Link enters another room", func(r *wrig) error { return r.g.EnterRoom(2, 1) }},
		// The slot's mRoomNo alone moves, mStayNo and every mZoneNo as the
		// plan read them: the zone freed, or made for another room.
		{"the zone of Link's room is freed", func(r *wrig) error {
			za, _ := addr.ZoneAddr(0)
			return r.m.Store(za+addr.ZoneOffRoomNo, []byte{0xFF})
		}},
		{"the zone's slot is another room's", func(r *wrig) error {
			za, _ := addr.ZoneAddr(0)
			return r.m.Store(za+addr.ZoneOffRoomNo, []byte{5})
		}},
		// A room change and back, which leaves mStayNo and the slot as
		// they were: mOldStayNo moved (setStayNo, d_stage.cpp:171-173).
		{"mOldStayNo moves", func(r *wrig) error {
			b, err := r.m.Read(addr.StayNo+1, 1)
			if err != nil {
				return err
			}
			return r.m.Store(addr.StayNo+1, []byte{b[0] + 1})
		}},
		// The game leaves IDLE as the batch waits (recheckIdle).
		{"an event starts", func(r *wrig) error { r.g.SetEvent(1, 0); return nil }},
		{"the pause menu opens", func(r *wrig) error { r.g.SetPauseMenu(true); return nil }},
	} {
		t.Run("aborted: "+c.name, func(t *testing.T) {
			r := newRig(t)
			var stormErr error
			r.hm.beatOn(2, func() {
				stormErr = c.storm(r)
				r.g.Frame()
			})
			res, err := r.fw.ApplyScoped(context.Background(), []ScopedWrite{zone(t, zoneOK, 1, 0), dan(t, danOK)})
			r.hm.beatOn(0, nil)
			if stormErr != nil {
				t.Fatal(stormErr)
			}
			if !errors.Is(err, ErrAborted) || len(res.Wrote) != 0 {
				t.Fatalf("ApplyScoped = %+v, %v; want ErrAborted", res, err)
			}
			if n := len(r.m.Writes()); n != 0 {
				t.Fatalf("%d writes", n)
			}
		})
	}
	t.Run("dry run", func(t *testing.T) {
		fw, err := NewFrameWriter(WriterConfig{Mem: newRig(t).m, DryRun: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.ApplyScoped(context.Background(), []ScopedWrite{dan(t, danOK)}); !errors.Is(err, ErrDryRun) {
			t.Fatalf("a dry run's ApplyScoped = %v", err)
		}
	})
}

// audit2 is the writer rig's audit for scoped writes: every write one
// byte, a dan or zone switch byte, never a clear.
func (r *wrig) audit2() {
	r.t.Helper()
	g := NewGuardFor(M5Targets)
	for _, w := range r.m.Writes() {
		if len(w.Data) != 1 || g.TargetOf(w.Addr) != TargetScoped {
			r.t.Fatalf("a %d-byte write at 0x%08X outside the scoped bytes", len(w.Data), w.Addr)
		}
	}
}

// TestScopedWriteSwitches: a scoped byte names the switches its bits are,
// and none for a byte outside its store.
func TestScopedWriteSwitches(t *testing.T) {
	for _, c := range []struct {
		kind  wire.ScopeKind
		no    int
		room  int
		slot  int
		valid bool
	}{
		{wire.ScopeDan, 0x80, -1, -1, true},
		{wire.ScopeDan, 0x9F, -1, -1, true},
		{wire.ScopeDan, 0xA0, -1, -1, true},
		{wire.ScopeDan, 0xBF, -1, -1, true},
		{wire.ScopeZone, 0xC0, 1, 0, true},
		{wire.ScopeZone, 0xCF, 1, 31, true},
		{wire.ScopeZone, 0xD0, 63, 4, true},
		{wire.ScopeZone, 0xDF, 0, 2, true},
		{wire.ScopeRoomEdge, 0xE0, 1, 0, true},
		{wire.ScopeRoomEdge, 0xEF, 1, 0, true},
		{wire.ScopeDan, 0xC0, -1, -1, false},
		{wire.ScopeZone, 0xE0, 1, 0, false},
		{wire.ScopeRoomEdge, 0xD0, 1, 0, false},
		{wire.ScopeZone, 0xF0, 1, 0, false},
		{wire.ScopeZone, 0xC0, 64, 0, false},
		{wire.ScopeZone, 0xC0, 1, 32, false},
		{wire.ScopeZone, 0xC0, 1, -1, false},
		{wire.ScopeKill, 0xC0, 1, 0, false},
		{wire.ScopeDan, 0x7F, -1, -1, false},
		{wire.ScopeDan, 0xFF, -1, -1, false},
	} {
		w, err := scopedWriteOf(c.kind, c.no, c.room, c.slot)
		if (err == nil) != c.valid {
			t.Errorf("scopedWriteOf(%v, 0x%02X, %d, %d) = %v, want valid %v", c.kind, c.no, c.room, c.slot, err, c.valid)
			continue
		}
		if !c.valid {
			continue
		}
		if got := w.Switches(); len(got) != 1 || got[0] != c.no {
			t.Errorf("scopedWriteOf(%v, 0x%02X) = %v, which names %v", c.kind, c.no, w, got)
		}
		w.Bits = 0xFF
		if got := w.Switches(); len(got) != 8 {
			t.Errorf("a full byte of %v names %d switches", w, len(got))
		}
	}
	if got := (ScopedWrite{Kind: wire.ScopeDan, Addr: addr.DanStageNo, Bits: 1, Room: -1, Slot: -1}).Switches(); got != nil {
		t.Errorf("mDan.mStageNo names switches %v", got)
	}
	if got := (ScopedWrite{Kind: wire.ScopeZone, Addr: zoneByteAddr(3, addr.ZoneOffItem), Bits: 1, Room: 1, Slot: 3}).Switches(); got != nil {
		t.Errorf("a zone's item byte names switches %v", got)
	}
}
