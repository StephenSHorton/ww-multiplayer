package derive

import (
	"fmt"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
)

// Max magic (§3), from two sources:
//
//   - the Deku Leaf, recorded by its get-item flag, mItemFlags[6] bit 0:
//     item_func_deku_leaf sets it and adds 16 to mItemMaxMagicCount
//     (d_item.cpp:810-815; dInvSlot_DEKU_LEAF_e, d_com_inf_game.h:1079;
//     onItem, d_save.cpp:204-207);
//   - the magic upgrade, item 0xB2, which adds 32 (item_func_max_mp_up1,
//     d_item.cpp:1396-1398), so with the HUD's cap of 0x20
//     (d_meter.cpp:4069-4076) the meter is full size whatever came first.
//     Nothing in the decomp records it: the Great Fairy who hands it over
//     is daBigelf, whose code is not decompiled (getEventFlag,
//     d_a_bigelf.cpp:199-202), and the design's record, event bit 0x3180,
//     has one decompiled reader, a list of seven event bits in
//     daNpcPhoto_c (d_a_npc_photo.cpp:2190-2197). That 0x3180 is set as
//     her upgrade event ends is the design's and ww-online's (inferred);
//     it is in the story SyncMask, so the partner's reaches the receiver.
//
// Max magic is 0, 16 with the leaf, 32 with the upgrade. It is add-only:
// a max magic above the flags' (a second Deku Leaf pickup adds 16 again,
// d_item.cpp:813) is kept and noted, never lowered. The counter does not
// fill the meter: dMeter_magicMove only clamps mMagic to the new maximum
// (d_meter.cpp:4105-4107), and the leaf's own 16 of magic
// (mItemMagicCount, d_item.cpp:814) is not mirrored, mMagic being per
// player (§3). A receiver whose meter co-op made starts it empty.

// Max magic values.
const (
	MagicLeaf = 0x10 // the Deku Leaf's 16
	MagicCap  = 0x20 // the upgrade's, the HUD's cap
)

// The magic sources' records.
const (
	slotDekuLeaf      = 6      // dInvSlot_DEKU_LEAF_e, d_com_inf_game.h:1079
	EventMagicUpgrade = 0x3180 // UNK_3180, d_save_event_flag.inc:371 (inferred: see above)
)

// MagicFacts are the magic sources in a View.
type MagicFacts struct {
	Leaf    bool // the Deku Leaf's get-item flag
	Upgrade bool // EventMagicUpgrade
	Have    int  // mMaxMagic
}

// Derived is the max magic the flags give.
func (f MagicFacts) Derived() int {
	switch {
	case f.Upgrade:
		return MagicCap
	case f.Leaf:
		return MagicLeaf
	}
	return 0
}

// CountMagic reads the magic sources in v.
func CountMagic(v View) MagicFacts {
	return MagicFacts{Leaf: v.GetItem(slotDekuLeaf, 0), Upgrade: v.EventSet(EventMagicUpgrade), Have: v.MaxMagic()}
}

// PlanMagic plans the raise of max magic to f.Derived() through
// mItemMaxMagicCount. ok is false unless that is a raise. The caller
// checks the gates: IDLE, and that the counter reads 0.
func PlanMagic(f MagicFacts) (w Write, ok bool) {
	want := f.Derived()
	if want <= f.Have {
		return Write{}, false
	}
	why := fmt.Sprintf("max magic: %d -> %d: Deku Leaf %v, magic upgrade (event 0x%04X) %v; the meter is not filled",
		f.Have, want, f.Leaf, EventMagicUpgrade, f.Upgrade)
	return counterWrite(MagicCounter, addr.ItemMaxMagicCount, want-f.Have, why), true
}

// magicState is the Deriver's memory of max magic.
type magicState struct {
	sig     magicSig
	sigHave bool
	changed time.Time
	pend    *pending
	above   magicSig // the last max magic above its flags' that was noted
}

type magicSig struct {
	have, derived int
	counter       int16
}

func (m *magicState) reset() { *m = magicState{} }

// tick derives max magic for one tick.
func (m *magicState) tick(d *Deriver, c *tick, p *Plan) {
	now := c.in.Now
	f := CountMagic(c.v)
	counter := c.in.Snap.MaxMagicCount
	sig := magicSig{f.Have, f.Derived(), counter}
	if !m.sigHave || sig != m.sig {
		m.sig, m.sigHave, m.changed = sig, true, now
	}
	if m.pend.landed(f.Have, now) {
		m.pend = nil
	}
	if m.pend != nil || counter != 0 || !d.settled(c, m.changed) {
		return
	}
	if f.Have > f.Derived() && m.above != sig {
		m.above = sig
		p.note("max magic: %d is above the %d its flags give: kept, max magic is add-only", f.Have, f.Derived())
	}
	if !c.canWrite() || !c.in.Gates.CanWriteCounter(gate.MaxMagic) {
		return
	}
	w, ok := PlanMagic(f)
	if !ok {
		return
	}
	p.Writes = append(p.Writes, w)
	m.pend = &pending{want: f.Derived(), prev: f.Have, expires: now.Add(d.inFlight)}
}
