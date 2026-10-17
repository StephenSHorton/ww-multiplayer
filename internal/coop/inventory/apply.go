package inventory

import (
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// Due returns the writes of ws that may be made this tick, in order: Live
// ones while IDLE writes are allowed, AtStageEntry ones only on the
// stage-entry window (entry, from Entry.Tick).
func Due(ws []Write, st gate.Status, entry bool) []Write {
	if !st.CanWriteIdle() {
		return nil
	}
	var due []Write
	for _, w := range ws {
		if w.When == Live || w.When == AtStageEntry && entry {
			due = append(due, w)
		}
	}
	return due
}

// Due returns the plan's writes that may be made this tick (see Due). The
// ammo fills are left out: Due(p.Ammo, st, entry) returns those.
func (p Plan) Due(st gate.Status, entry bool) []Write { return Due(p.Writes, st, entry) }

// Apply returns raw and ctx once ws land as the receiver's writer lands
// them over fresh reads: an OpOR write ORs its bits, an OpMax write raises
// the byte, an OpSet write sets it only while it still reads Old. Writes
// to bytes it does not know are ignored. It is what a test, or a dry run,
// expects the receiver to read back.
func Apply(raw *world.Image, ctx Context, ws []Write) (world.Image, Context) {
	img := *raw
	for _, w := range ws {
		var b *byte
		switch {
		case w.Off >= 0 && w.Off < world.ImageSize && w.Addr == addr.SaveImage+uint32(w.Off):
			b = &img[w.Off]
		case w.Addr >= addr.PlaySelectItem && w.Addr < addr.PlaySelectItem+Buttons:
			b = &ctx.SelectItem[w.Addr-addr.PlaySelectItem]
		}
		if b != nil {
			*b = land(w, *b)
			continue
		}
		for _, c := range []struct {
			a uint32
			v *int16
		}{{ItemArrowNumCount, &ctx.ArrowCount}, {ItemBombNumCount, &ctx.BombCount}} {
			if w.Addr == c.a+1 {
				lo := land(w, byte(*c.v))
				*c.v = int16(uint16(*c.v)&0xFF00 | uint16(lo))
			}
		}
	}
	return img, ctx
}

// land returns cur once w lands over it.
func land(w Write, cur byte) byte {
	switch w.Op {
	case OpOR:
		return cur | w.Val&^w.Old
	case OpMax:
		return max(cur, w.Val)
	case OpSet:
		if cur == w.Old {
			return w.Val
		}
	}
	return cur
}

// Owns reports whether image byte off is one a plan may write: a flag
// byte (bits FlagMask(off) only), a capacity, a derived item slot (not a
// bottle) or mSelectEquip[0..2].
func Owns(off int) bool {
	if FlagMask(off) != 0 {
		return true
	}
	for _, c := range capacities {
		if c.off == off {
			return true
		}
	}
	for _, s := range itemSlots {
		if s.Off == off {
			return true
		}
	}
	for _, e := range equipSlots {
		if e.Off == off {
			return true
		}
	}
	return false
}

// Entry finds the receiver's stage-entry window, when the equipment's
// AtStageEntry writes are made: the first tick allowing IDLE writes after
// a stage load. A load is a pending stage change (mNextStage.mEnable), no
// Link, or a new stage name or Link actor since the last tick; each one
// recreates Link with nothing in hand (d_a_player_main.cpp:12179). A room
// change within a stage does not. Until the first load the replica sees,
// there is no window: a session started mid-stage equips at the next
// load. The window comes at least StableTicks after the load (§3.2
// STABLE): a player who draws the sword within that time has it in hand
// when the equipment changes, the one case the wait does not cover.
type Entry struct {
	loaded bool // a load was seen and its window has not come yet
	seen   bool
	stage  string
	player uint32
}

// Tick takes one tick's snapshot and gate verdict and reports whether this
// tick is the window. A window whose writes could not be made is not
// opened again until the next load.
func (e *Entry) Tick(s *state.Snapshot, st gate.Status) bool {
	if s.NextEnable != 0 || s.PlayerPtr == 0 || e.seen && (s.StageName != e.stage || s.PlayerPtr != e.player) {
		e.loaded = true
	}
	e.seen, e.stage, e.player = true, s.StageName, s.PlayerPtr
	if e.loaded && st.CanWriteIdle() {
		e.loaded = false
		return true
	}
	return false
}
