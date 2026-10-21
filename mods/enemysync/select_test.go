package enemysync

import (
	"math"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// key is a keyed Moblin's match key, for the selection tests.
func key(setID uint16) Ident { return Ident{Proc: ProcMO2, SetID: setID} }

// held is the assignment's keys by block slot (setIDs; -1 free).
func held(s [MaxSlots]selSlot) [MaxSlots]int {
	var out [MaxSlots]int
	for j := range s {
		out[j] = -1
		if s[j].Used {
			out[j] = int(s[j].Key.SetID)
		}
	}
	return out
}

// adopted is a used slot holding setID since at.
func adopted(setID uint16, at time.Time) selSlot {
	return selSlot{Key: key(setID), Used: true, AdoptedAt: at}
}

// TestSelectSlots: design slots.client_selectSlots. Newcomers fill free
// slots nearest first; a held enemy keeps its slot whatever the order the
// candidates come in (the remap); a copy within KeepR (unless dormant),
// engaged or adopted less than MinHold ago is never evicted; a newcomer
// evicts the farthest copy no rule keeps only when nearer than EvictRatio
// of its distance and within EnterR; a dying candidate takes a free slot, else evicts such a
// copy, else waits; an enemy no longer offered frees its slot; a locked
// slot keeps its enemy and is not refilled; legacy mode keeps the given
// (lowest setID) order.
func TestSelectSlots(t *testing.T) {
	now := time.Unix(1000, 0)
	old := now.Add(-10 * time.Second)
	var none [MaxSlots]bool
	cands := func(ds ...float64) []cand {
		var out []cand
		for i, d := range ds {
			out = append(out, cand{Key: key(uint16(i)), Dist: d})
		}
		return out
	}

	// Fresh: the four nearest, in that order.
	got := selectSlots(cands(600, 100, 500, 200, 300, 400), [MaxSlots]selSlot{}, none, now, SelectDistance)
	if h := held(got); h != [MaxSlots]int{1, 3, 4, 5} {
		t.Errorf("fresh: %v", h)
	}
	for j := range got {
		if !got[j].AdoptedAt.Equal(now) || got[j].Pinned {
			t.Errorf("fresh slot %d: %+v", j, got[j])
		}
	}

	// The remap: reshuffled candidates, nearer newcomers but every copy
	// kept (within KeepR): nobody moves.
	cur := [MaxSlots]selSlot{adopted(3, old), adopted(1, old), adopted(5, old), adopted(4, old)}
	got = selectSlots(cands(10, 900, 20, 1000, 1100, 1400), cur, none, now, SelectDistance)
	if h := held(got); h != [MaxSlots]int{3, 1, 5, 4} {
		t.Errorf("remap: %v", h)
	}

	// Rule 2 against rule 3: setID 3 at 1600, no longer kept, is the only
	// victim. A newcomer at 100 evicts it (100 < 0.7*1600, < EnterR);
	// engaged, or adopted less than MinHold ago, it stays.
	cs := cands(100, 300, 300, 1600, 300, 300)
	cs = cs[:4]
	cs[1].Key, cs[2].Key = key(1), key(5)
	cs = append(cs, cand{Key: key(4), Dist: 300})
	cur = [MaxSlots]selSlot{adopted(3, old), adopted(1, old), adopted(5, old), adopted(4, old)}
	if h := held(selectSlots(cs, cur, none, now, SelectDistance)); h != [MaxSlots]int{0, 1, 5, 4} {
		t.Errorf("evict past KeepR: %v", h)
	}
	cs[3].Engaged = true
	if h := held(selectSlots(cs, cur, none, now, SelectDistance)); h != [MaxSlots]int{3, 1, 5, 4} {
		t.Errorf("engaged: %v", h)
	}
	cs[3].Engaged = false
	young := cur
	young[0].AdoptedAt = now.Add(-MinHold / 2)
	if h := held(selectSlots(cs, young, none, now, SelectDistance)); h != [MaxSlots]int{3, 1, 5, 4} {
		t.Errorf("min hold: %v", h)
	}
	cs[3].Dist = 1400 // within KeepR
	if h := held(selectSlots(cs, cur, none, now, SelectDistance)); h != [MaxSlots]int{3, 1, 5, 4} {
		t.Errorf("within KeepR: %v", h)
	}
	// Dormant (WFlagDormant) within KeepR: a newcomer EvictRatio nearer
	// evicts it (Siren room 3: ceiling ChuChus at about 1400, an awake one
	// at about 750), one not that near does not; MinHold still holds it.
	cs[3].Dormant = true
	if h := held(selectSlots(cs, cur, none, now, SelectDistance)); h != [MaxSlots]int{0, 1, 5, 4} {
		t.Errorf("dormant within KeepR, newcomer at 100: %v", h)
	}
	cs[0].Dist = 1000 // >= 0.7*1400
	if h := held(selectSlots(cs, cur, none, now, SelectDistance)); h != [MaxSlots]int{3, 1, 5, 4} {
		t.Errorf("dormant within KeepR, newcomer at 1000: %v", h)
	}
	cs[0].Dist = 100
	if h := held(selectSlots(cs, young, none, now, SelectDistance)); h != [MaxSlots]int{3, 1, 5, 4} {
		t.Errorf("dormant, min hold: %v", h)
	}
	cs[3].Dormant = false

	// The ratio and EnterR.
	for _, c := range []struct {
		victim, newcomer float64
		evict            bool
	}{
		{1600, 1150, false}, // 1150 >= 0.7*1600
		{1600, 1100, true},
		{3000, 1300, false}, // < 0.7*3000 but past EnterR
		{3000, 1190, true},
	} {
		cs[3].Dist, cs[0].Dist = c.victim, c.newcomer
		h := held(selectSlots(cs, cur, none, now, SelectDistance))
		if evicted := h[0] == 0; evicted != c.evict {
			t.Errorf("victim %v newcomer %v: %v, want evicted %v", c.victim, c.newcomer, h, c.evict)
		}
	}

	// A dying candidate: a free slot first; else it evicts a copy rule 2
	// does not keep (setID 3 at 1600) even from farther than the ratio
	// allows a newcomer; else it waits.
	cs = []cand{{Key: key(1), Dist: 300}, {Key: key(3), Dist: 1600}, {Key: key(4), Dist: 300}, {Key: key(5), Dist: 300}, {Key: key(9), Dist: 1500, Dying: true}}
	got = selectSlots(cs, cur, none, now, SelectDistance)
	if h := held(got); h != [MaxSlots]int{9, 1, 5, 4} || !got[0].Pinned {
		t.Errorf("dying evicts: %v %+v", h, got[0])
	}
	cs[1].Dist = 1000
	if h := held(selectSlots(cs, cur, none, now, SelectDistance)); h != [MaxSlots]int{3, 1, 5, 4} {
		t.Errorf("dying waits: %v", h)
	}
	free := cur
	free[2] = selSlot{}
	cs = append(cs[:3], cs[4]) // setID 5 is no longer offered
	if h := held(selectSlots(cs, free, none, now, SelectDistance)); h != [MaxSlots]int{3, 1, 9, 4} {
		t.Errorf("dying takes the free slot: %v", h)
	}

	// No longer offered: freed; a locked slot keeps its enemy, and once
	// freed is not refilled.
	cs = []cand{{Key: key(1), Dist: 100}, {Key: key(3), Dist: 1600}, {Key: key(7), Dist: 50}, {Key: key(8), Dist: 60}}
	locked := [MaxSlots]bool{0: true, 2: true}
	got = selectSlots(cs, cur, locked, now, SelectDistance)
	if h := held(got); h != [MaxSlots]int{3, 1, -1, 7} {
		t.Errorf("locked: %v", h)
	}

	// Legacy: the given order, no hysteresis, pinned slots kept.
	cs = cands(900, 100, 800, 200, 700, 300)
	got = selectSlots(cs, [MaxSlots]selSlot{}, none, now, SelectLegacy)
	if h := held(got); h != [MaxSlots]int{0, 1, 2, 3} {
		t.Errorf("legacy: %v", h)
	}
	pinned := got
	pinned[1].Pinned = true
	cs = append(cs[2:], cand{Key: key(9), Dist: 1})
	if h := held(selectSlots(cs, pinned, none, now, SelectLegacy)); h != [MaxSlots]int{4, 1, 2, 3} {
		t.Errorf("legacy with setID 1 pinned and 0 gone: %v", h)
	}
	// Legacy with a tombstone listed first (the host lists tombstones before
	// every live enemy) of a setID never mirrored here: it waits for a free
	// slot and evicts no live copy (review 2); with a slot free it takes it,
	// pinned, behind the live ones.
	full := [MaxSlots]selSlot{adopted(1, old), adopted(2, old), adopted(3, old), adopted(4, old)}
	cs = []cand{{Key: key(5), Dying: true, Late: true}, {Key: key(1)}, {Key: key(2)}, {Key: key(3)}, {Key: key(4)}}
	if h := held(selectSlots(cs, full, none, now, SelectLegacy)); h != [MaxSlots]int{1, 2, 3, 4} {
		t.Errorf("legacy tombstone evicted a live copy: %v", h)
	}
	cs = append(cs[:4:4], cand{Key: key(6)}) // setID 4 gone, 6 new
	got = selectSlots(cs, full, none, now, SelectLegacy)
	if h := held(got); h != [MaxSlots]int{1, 2, 3, 6} {
		t.Errorf("legacy: the live newcomer before the tombstone: %v", h)
	}
	cs = cs[:4] // setID 6 gone too: the free slot is the tombstone's
	got = selectSlots(cs, full, none, now, SelectLegacy)
	if h := held(got); h != [MaxSlots]int{1, 2, 3, 5} || !got[3].Pinned {
		t.Errorf("legacy tombstone into the free slot: %v %+v", h, got[3])
	}
	// Legacy with a listed enemy dying but still live on the host (DYING or
	// its dead word, no tombstone) and no slot here: it keeps its setID
	// place, as the prototype mirrored the lowest four listed, so it takes
	// a slot (pinned) and the fifth live copy gives one up.
	full2 := [MaxSlots]selSlot{adopted(2, old), adopted(3, old), adopted(4, old), adopted(5, old)}
	cs = []cand{{Key: key(1), Dying: true}, {Key: key(2)}, {Key: key(3)}, {Key: key(4)}, {Key: key(5)}}
	got = selectSlots(cs, full2, none, now, SelectLegacy)
	if h := held(got); h != [MaxSlots]int{2, 3, 4, 1} || !got[3].Pinned {
		t.Errorf("legacy live-dying listed enemy skipped: %v %+v", h, got[3])
	}
}

// TestSelectSlotsPinsDying: a copy that turns dying is pinned and stays in
// its slot (no eviction, no remap) while it dies, listed or not, whatever
// comes nearer, until the caller unpins it (the REL reported STATE_DEAD:
// Mirror.unpin); then the slot is free for the next.
func TestSelectSlotsPinsDying(t *testing.T) {
	now := time.Unix(1000, 0)
	old := now.Add(-10 * time.Second)
	var none [MaxSlots]bool
	cur := [MaxSlots]selSlot{adopted(0, old), adopted(1, old), adopted(2, old), adopted(3, old)}
	cs := []cand{{Key: key(0), Dist: 5000, Dying: true}, {Key: key(1), Dist: 5000}, {Key: key(2), Dist: 5000}, {Key: key(3), Dist: 5000}}
	got := selectSlots(cs, cur, none, now, SelectDistance)
	if !got[0].Pinned || !got[0].PinnedAt.Equal(now) || held(got) != [MaxSlots]int{0, 1, 2, 3} {
		t.Fatalf("turned dying: %+v", got)
	}
	// Its tombstone gone (no longer offered), and four near newcomers:
	// setIDs 1..3 (far, past KeepR) are evicted, setID 0 stays.
	later := now.Add(time.Second)
	cs = []cand{{Key: key(1), Dist: 5000}, {Key: key(2), Dist: 5000}, {Key: key(3), Dist: 5000},
		{Key: key(10), Dist: 10}, {Key: key(11), Dist: 20}, {Key: key(12), Dist: 30}, {Key: key(13), Dist: 40}}
	got = selectSlots(cs, got, none, later, SelectDistance)
	if h := held(got); h != [MaxSlots]int{0, 10, 11, 12} || !got[0].Pinned || !got[0].PinnedAt.Equal(now) {
		t.Errorf("pinned through a reshuffle: %v %+v", h, got[0])
	}
	// A pinned slot under legacy too.
	if h := held(selectSlots(cs, got, none, later, SelectLegacy)); h[0] != 0 {
		t.Errorf("legacy dropped the pinned slot: %v", h)
	}
	// Unpinned (STATE_DEAD): free, and the nearest waiting takes it.
	got[0] = selSlot{}
	if h := held(selectSlots(cs, got, none, later.Add(3*time.Second), SelectDistance)); h != [MaxSlots]int{13, 10, 11, 12} {
		t.Errorf("after the unpin: %v", h)
	}
}

// TestUnkeyedDeadSet: an unkeyed enemy the host showed as a tombstone
// joins the per-room host-dead set; when its tombstone expires while every
// slot is pinned it is still offered as a quiet catch-up, and takes the
// first slot that frees; a room change forgets the set.
func TestUnkeyedDeadSet(t *testing.T) {
	m := mem.NewFakeMem()
	c := NewMirror(m, log.Discard{}, nil, false, time.Unix(1, 0))
	c.Kinds, _ = ParseKinds("cc+u")
	u := Ident{Proc: addr.ProcCC, SetID: addr.ActorSetIDNone, PKey: 0xC0FFEE}
	c.local = map[Ident]localEnemy{u: {Enemy: Enemy{Kind: KindCC, Ident: u, PID: 77, HomeRoom: 1}}}
	st := &State{HostKindMask: 1 << KindCC, Stage: "M_Dai", Room: 1,
		Slots: []Slot{{SetID: u.SetID, Proc: u.Proc, Kind: KindCC, PKey: u.PKey, Handle: 4, Flags: SlotDying | SlotRelease, WFlags: WFlagTomb}}}
	cs, offers, claims := c.candidates(st, [3]float32{}, false)
	if len(cs) != 1 || !cs[0].Dying || offers[u].wire == nil || !c.hostDead[u] || len(claims) != 1 || claims[0] != 77 {
		t.Fatalf("tombstone: %+v %+v %v %v", cs, offers, c.hostDead, claims)
	}
	// Every slot pinned by others: it waits.
	now := time.Unix(100, 0)
	var full [MaxSlots]selSlot
	for j := range full {
		full[j] = selSlot{Key: key(uint16(j)), Used: true, Pinned: true, PinnedAt: now}
	}
	if h := held(selectSlots(cs, full, [MaxSlots]bool{}, now, SelectDistance)); h != [MaxSlots]int{0, 1, 2, 3} {
		t.Fatalf("all pinned: %v", h)
	}
	// The tombstone expired: no longer listed, still a catch-up.
	st.Slots = nil
	cs, offers, claims = c.candidates(st, [3]float32{}, false)
	if len(cs) != 1 || cs[0].Key != u || !cs[0].Dying || offers[u].wire != nil || len(claims) != 1 {
		t.Fatalf("expired: %+v %+v %v", cs, offers, claims)
	}
	full[2] = selSlot{} // the REL reported one dead
	got := selectSlots(cs, full, [MaxSlots]bool{}, now, SelectDistance)
	if !got[2].Used || got[2].Key != u || !got[2].Pinned {
		t.Errorf("not caught up when a slot freed: %+v", got)
	}
	// Without "+u" here, or with the host not enabling ChuChus, it is not
	// mirrored at all.
	c.Kinds, _ = ParseKinds("cc")
	if cs, _, _ := c.candidates(st, [3]float32{}, false); len(cs) != 0 {
		t.Errorf("keyed-only: %+v", cs)
	}
	c.Kinds, _ = ParseKinds("cc+u")
	st.HostKindMask = 1 << KindMO2
	if cs, _, _ := c.candidates(st, [3]float32{}, false); len(cs) != 0 {
		t.Errorf("host without ChuChus: %+v", cs)
	}
	c.resetScope()
	st.HostKindMask = 1 << KindCC
	if cs, _, _ := c.candidates(st, [3]float32{}, false); len(cs) != 0 || len(c.hostDead) != 0 {
		t.Errorf("after a room change: %+v %v", cs, c.hostDead)
	}
}

// TestHandles: handles are assigned on first sight from 1, monotonic,
// stable per Ident for the epoch, never 0 or 0xFFFF (the wrap skips both
// and the handles still held); the client translates a PHIT's handle to a
// keyed local setID, OwnerSlotBase|j for an unkeyed copy in block slot j,
// else NoMirrorSetID, never the handle itself.
func TestHandles(t *testing.T) {
	h := &Host{handles: map[Ident]uint16{}, byHandle: map[uint16]Ident{}}
	a, b := key(7), Ident{Proc: addr.ProcBK, SetID: addr.ActorSetIDNone, PKey: 0x1234}
	if h.HandleOf(a) != 1 || h.HandleOf(b) != 2 || h.HandleOf(a) != 1 {
		t.Fatalf("handles %d %d", h.HandleOf(a), h.HandleOf(b))
	}
	h.nextHandle = 0xFFFD
	if v := h.HandleOf(key(8)); v != 0xFFFE {
		t.Errorf("0xFFFE: %d", v)
	}
	if v := h.HandleOf(key(9)); v != 3 { // 0xFFFF, 0, 1 and 2 skipped
		t.Errorf("after the wrap: %d", v)
	}
	l := &Listing{Entries: []Listed{{Ident: a, Handle: 1, PID: 50}, {Ident: b, Handle: 2}}}
	if l.HandleOfPID(50) != 1 || l.HandleOfPID(51) != NoMirrorHandle || l.HandleOfPID(0) != NoMirrorHandle || l.ByHandle(2) == nil || l.ByHandle(0) != nil {
		t.Errorf("listing lookups")
	}

	c := NewMirror(mem.NewFakeMem(), log.Discard{}, nil, false, time.Unix(1, 0))
	st := &State{Slots: []Slot{
		{SetID: 7, Proc: ProcMO2, Kind: KindMO2, Handle: 1},
		{SetID: b.SetID, Proc: b.Proc, Kind: KindBK, PKey: b.PKey, Handle: 2},
		{SetID: addr.ActorSetIDNone, Proc: addr.ProcBK, Kind: KindBK, PKey: 0x9999, Handle: 3},
	}}
	c.mu.Lock()
	c.latest = st
	c.mu.Unlock()
	c.blk[2].selSlot = selSlot{Key: b, Used: true}
	for _, w := range []struct{ handle, owner uint16 }{{1, 7}, {2, OwnerSlotBase | 2}, {3, NoMirrorSetID}, {4, NoMirrorSetID}, {NoMirrorHandle, NoMirrorSetID}, {0, NoMirrorSetID}} {
		if got := c.OwnerOf(w.handle); got != w.owner {
			t.Errorf("handle %d: owner 0x%04X, want 0x%04X", w.handle, got, w.owner)
		}
	}
}

// TestQuietCarryGuard: the Go copy of the REL's guard on a quiet Bokoblin
// death's CARRY clear: only a BOKO it holds (m0B30), carried, in
// Mode_ENEMY_CARRY_e; a stale m1200 naming a stick in Link's hands or on
// the ground, or no actor, is left alone.
func TestQuietCarryGuard(t *testing.T) {
	stick := func(mode int32, carried bool) *actors.Actor {
		a := &actors.Actor{Proc: addr.ProcBoko, Mode: mode, ModeOK: true}
		if carried {
			a.Status = addr.ActorStatusCarry
		}
		return a
	}
	for _, c := range []struct {
		name  string
		hold  uint32
		boko  *actors.Actor
		clear bool
	}{
		{"held", 1, stick(addr.BokoModeEnemyCarry, true), true},
		{"Link holds it", 1, stick(addr.BokoModePlayerCarry, true), false},
		{"on the ground", 1, stick(addr.BokoModeEnemyCarry, false), false},
		{"other mode", 1, stick(0, true), false},
		{"no weapon held", 0, stick(addr.BokoModeEnemyCarry, true), false},
		{"no actor", 1, nil, false},
		{"not a BOKO", 1, &actors.Actor{Proc: addr.ProcMo2, Status: addr.ActorStatusCarry, Mode: addr.BokoModeEnemyCarry, ModeOK: true}, false},
		{"mode unreadable", 1, &actors.Actor{Proc: addr.ProcBoko, Status: addr.ActorStatusCarry}, false},
	} {
		why := QuietCarryGuard(c.hold, c.boko)
		if (why == "") != c.clear {
			t.Errorf("%s: %q, want clear %v", c.name, why, c.clear)
		}
	}
}

// TestColliderOffPins: the client's hit collider offsets per kind are the
// wwenemy_kinds.h rows' HEAD_SPH (code 1) and TG_CYL (code 2), which the
// REL's em_hits codes come from; the ChuChu has no head sphere.
func TestColliderOffPins(t *testing.T) {
	_, defs := kindsHeader(t)
	for id, p := range map[KindID]string{KindMO2: "EMK_MO2_", KindBK: "EMK_BK_", KindCC: "EMK_CC_"} {
		if got, want := int64(ColliderOff(id, ColliderHead)), defs[p+"HEAD_SPH"]; got != want {
			t.Errorf("%s head: 0x%X, the header's 0x%X", id, got, want)
		}
		if got, want := int64(ColliderOff(id, ColliderTgCyl)), defs[p+"TG_CYL"]; got != want {
			t.Errorf("%s tg: 0x%X, the header's 0x%X", id, got, want)
		}
		if ColliderOff(id, 0) != 0 || ColliderOff(id, 3) != 0 {
			t.Errorf("%s: an unknown code has a collider", id)
		}
	}
	if DeathSwitchOff(KindCC) != 0 || DeathSwitchOff(KindBK) != addr.BkOffDeathSwitch || DeathSwitchOff(KindMO2) != addr.Mo2OffDeathSwitch {
		t.Error("death switch offsets")
	}
	if DeathSwitchSet(0) || DeathSwitchSet(0xFF) || !DeathSwitchSet(0x10) {
		t.Error("DeathSwitchSet")
	}
}

// TestBatch5Knobs: WW_ENEMY_QUIET_DEATH (default mo2,bk,cc,ki,am,am2; "+u" refused),
// WW_ENEMY_SELECT (distance, legacy), WW_ENEMY_STATE_30HZ, and the kind
// set helpers the client narrows the host's mask with.
func TestBatch5Knobs(t *testing.T) {
	q, err := ParseQuietKinds("")
	if err != nil || q.String() != "mo2,bk,cc,ki,am,am2" {
		t.Errorf("quiet default: %v %v", q, err)
	}
	if q, err = ParseQuietKinds("bk,cc"); err != nil || q.Has(KindMO2) {
		t.Errorf("quiet without the Moblin: %v %v", q, err)
	}
	if _, err = ParseQuietKinds("bk+u"); err == nil {
		t.Error("quiet +u accepted")
	}
	if q, err = ParseQuietKinds("none"); err != nil || q.Mask() != 0 {
		t.Errorf("quiet none: %v %v", q, err)
	}
	for v, want := range map[string]SelectMode{"": SelectDistance, "distance": SelectDistance, "LEGACY": SelectLegacy} {
		if got, err := ParseSelect(v); err != nil || got != want {
			t.Errorf("select %q: %v %v", v, got, err)
		}
	}
	if _, err := ParseSelect("nearest"); err == nil {
		t.Error("select nearest accepted")
	}
	t.Setenv("WW_ENEMY_STATE_30HZ", "1")
	if !State30Hz() {
		t.Error("WW_ENEMY_STATE_30HZ=1")
	}
	t.Setenv("WW_ENEMY_MIRROR_KINDS", "xx")
	if s, err := MirrorKinds(); err == nil || s.String() != "mo2,bk+u,cc+u" {
		t.Errorf("a bad WW_ENEMY_MIRROR_KINDS: %v %v", s, err)
	}
	local, _ := ParseKinds("mo2,bk+u,cc+u")
	on := local.And(MaskSet(1<<KindBK | 1<<KindCC))
	if on.Has(KindMO2) || !on.TakesUnkeyed(KindBK) || !on.Enabled(addr.ProcCC) || on.Enabled(addr.ProcMo2) || on.Mask() != 0b110 {
		t.Errorf("host bk,cc and local mo2,bk+u,cc+u: %v", on)
	}
	keyedOnly, _ := ParseKinds("bk")
	if keyedOnly.And(MaskSet(1 << KindBK)).TakesUnkeyed(KindBK) {
		t.Error("the local keyed-only set took unkeyed")
	}
}

// TestDist2Vertical: selection distance is 3D. Siren room 3's ceiling
// ChuChus hang about 1360 above Link and must rank behind an awake one
// about 700 away on the floor (an xz ranking put them first).
func TestDist2Vertical(t *testing.T) {
	link := [][3]float32{{4143, -2078, 907}}
	ceiling := dist2([3]float32{4218, -715, 1026}, link)
	floor := dist2([3]float32{4720, -1736, 570}, link)
	if !(floor < ceiling) {
		t.Fatalf("floor %v ceiling %v", floor, ceiling)
	}
	if d := dist2([3]float32{3, 4, 12}, [][3]float32{{0, 0, 0}, {100, 0, 0}}); d != 169 {
		t.Errorf("nearest player: %v", d)
	}
	if d := dist2([3]float32{}, nil); !math.IsInf(d, 1) {
		t.Errorf("no players: %v", d)
	}
}
