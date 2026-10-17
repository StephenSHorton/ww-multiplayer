package story

import (
	"slices"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
)

// Held is a bit of the world a hold keeps out of the receiver's game.
type Held struct {
	Off  uint16 // image offset of the event byte
	Mask byte   // the bits kept back
	Hold class.Hold
}

// Plan is what the receiver's game lacks of the world's story bits and
// allowlisted event registers.
type Plan struct {
	// Bits are the story bits to set, by OR, in ascending offset order.
	Bits []world.Gain
	// Regs are the registers to raise, in ascending offset order: for an
	// OR register (a figurine bitfield) the bits to set, for a MAX one the
	// field's new value. ApplyRegister writes either.
	Regs []world.Gain
	// Held are the world's bits the holds keep back, in class.Holds order.
	Held []Held
}

// Empty reports whether the plan writes nothing. It may still hold bits.
func (p Plan) Empty() bool { return len(p.Bits) == 0 && len(p.Regs) == 0 }

// Writes returns Bits and Regs as one list, in ascending offset order (the
// story bytes come before the registers).
func (p Plan) Writes() []world.Gain {
	if p.Empty() {
		return nil
	}
	return append(slices.Clip(p.Bits), p.Regs...)
}

// Pending returns what the receiver's game lacks of view, the world as
// the receiver knows it:
//
//	story bits: view & SyncMask &^ raw &^ ever, minus the held ones
//	OR registers: view & class mask &^ raw &^ ever
//	MAX registers: view's field, where it is above both raw's and everMax's
//
// raw is the receiver's canonical image (its own bytes included: the
// holds read them), ever its ever-had set of OR bits and everMax the
// largest value each MAX byte ever had in its game (the replica Planner's
// history, §4.2: a bit the game cleared itself is not set again, and a
// register the game lowered is not raised back). A nil ever or everMax is
// empty.
//
// The holds are judged on raw, and UntilWith also on raw with the plan's
// unheld story bits in it (Released), to a fixed point: holding a bit can
// keep back what another hold waits for.
func Pending(view, raw *world.Image, ever *world.BitSet, everMax *world.Image) Plan {
	if ever == nil {
		ever = new(world.BitSet)
	}
	if everMax == nil {
		everMax = new(world.Image)
	}
	var miss world.Image
	for i := range addr.EventBitBytes {
		off := addr.OffEvent + i
		miss[off] = view[off] & class.SyncMaskOf(i) &^ raw[off] &^ ever.Mask(off)
	}

	var p Plan
	holds := class.Holds()
	holding := make([]bool, len(holds))
	for changed := true; changed; {
		changed = false
		after := *raw
		for off := addr.OffEvent; off < addr.OffEvent+addr.EventBitBytes; off++ {
			after[off] |= miss[off]
		}
		for i, h := range holds {
			if holding[i] || Released(h, raw, &after) {
				continue
			}
			holding[i], changed = true, true
			b := addr.EventByte(h.ID)
			off := addr.OffEvent + b.Off
			if c := miss[off] & b.Mask; c != 0 {
				miss[off] &^= c
				p.Held = append(p.Held, Held{Off: uint16(off), Mask: c, Hold: h})
			}
		}
	}
	slices.SortStableFunc(p.Held, func(a, b Held) int { return holdIndex(holds, a.Hold.ID) - holdIndex(holds, b.Hold.ID) })
	p.Bits = gainsOf(&miss)

	for i := addr.EventRegFirst; i < addr.EventSize; i++ {
		off := addr.OffEvent + i
		r := class.Of(off)
		if !r.Class.Merges() {
			continue
		}
		w := view[off] & r.Mask
		if r.Class == class.Max {
			if w > raw[off]&r.Mask && w > everMax[off]&r.Mask {
				p.Regs = append(p.Regs, world.Gain{Off: uint16(off), Val: w})
			}
		} else if v := w &^ raw[off] &^ ever.Mask(off); v != 0 {
			p.Regs = append(p.Regs, world.Gain{Off: uint16(off), Val: v})
		}
	}
	return p
}

func holdIndex(holds []class.Hold, id uint16) int {
	return slices.IndexFunc(holds, func(h class.Hold) bool { return h.ID == id })
}

// Released reports whether hold h is over in the receiver's game, judged
// from its own snapshot: raw is its canonical image, and after raw with
// the story bits the same write would set (nil: none). UntilLocal reads
// raw alone, UntilWith reads after, and UntilMasterSword reads the sword
// slot of raw (daPy_lk_c::checkMasterSwordEquip, d_a_player.h:600-604).
// An unknown condition holds.
func Released(h class.Hold, raw, after *world.Image) bool {
	if after == nil {
		after = raw
	}
	switch h.Until {
	case class.UntilLocal:
		return eventSet(raw, h.Arg)
	case class.UntilWith:
		return eventSet(after, h.Arg)
	case class.UntilMasterSword:
		return slices.Contains(class.MasterSwords(), raw[addr.OffSelectEquip])
	}
	return false
}

// eventSet reports whether img has every mask bit of event bit id.
func eventSet(img *world.Image, id uint16) bool {
	b := addr.EventByte(id)
	return b.Mask != 0 && b.Off < addr.EventSize && img[addr.OffEvent+b.Off]&b.Mask == b.Mask
}

// Receiver decides when the receiver's plan is written, before M7's
// getSave shim (§6.5): on the first tick where mNextStage.mEnable reads 1,
// that is the tick a stage change starts. The story bits and registers are
// written into the saved dSv_event_c then, which getSave and putSave do
// not copy (d_save.cpp:1506-1515), so they last into the next stage. Its
// rooms read them as they load: dScnPly_Draw asks for the scene change
// while mEnable is set (d_s_play.cpp:291-314), the new play scene takes
// mNextStage and clears mEnable as it is created (d_s_play.cpp:1277-1278),
// and each room's layer comes from getLayerNo (d_com_inf_game.cpp:185-269,
// which reads story bits such as MET_KORL, :211), called as the room's
// actor lists are decoded (d_stage.cpp:2150) and as its demo archive is
// chosen (d_s_room.cpp:210); the actors read theirs as they are created
// (inferred). The stage being left is fading out: a bit written into it
// changes no layer, and the event bits its actors poll each frame see it
// for that fade only.
//
// The tick is the gates' OnTransition with a moving heartbeat
// (gate.Status.CanWriteOnTransition: the stage change follows
// gate.StableTicks stable ticks), and the Receiver also wants the last
// tick it saw to have read mEnable 0, so a stage change is written once
// and a Receiver with no history writes nothing. A missed stage change is
// not made up: the plan is level-triggered, so the next one writes it.
// The zero Receiver is ready to use. It is not safe for concurrent use.
type Receiver struct {
	have   bool
	enable int8 // the last tick's mNextStage.mEnable
}

// Tick takes one receiver tick: its gates, its snapshot's
// mNextStage.mEnable, whether co-op may write story bits at all (the
// caller's: the file is armed, co-op writes are on and this is not a dry
// run), the world as the receiver knows it and the receiver's history (as
// Pending). It returns the writes to make this tick, nil unless the tick
// starts a stage change and ok is set, and the plan, for the log and the
// TUI.
func (r *Receiver) Tick(st gate.Status, enable int8, ok bool,
	view, raw *world.Image, ever *world.BitSet, everMax *world.Image) ([]world.Gain, Plan) {
	first := r.have && r.enable == 0 && enable == 1
	r.have, r.enable = true, enable
	p := Pending(view, raw, ever, everMax)
	if !first || !ok || !st.CanWriteOnTransition() {
		return nil, p
	}
	return p.Writes(), p
}

// Reset forgets the last tick: call it after a tick whose snapshot could
// not be read, as the gates start over then too.
func (r *Receiver) Reset() { *r = Receiver{} }
