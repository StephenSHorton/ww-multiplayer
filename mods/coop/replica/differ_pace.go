package replica

import (
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/story"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
)

// The sender's pace (§6.5, §6.6, M4). M2 and M3 send every rising edge on
// the tick the Differ finds it. From M4 a paced Differ (Pace) holds some
// back, so that what one game event sets reaches the world, and the
// partner, as one:
//
//   - story bits and the allowlisted event registers wait in one batch
//     (story.Batcher), released once the game has been IDLE for
//     story.BatchIdle and the batch has gained nothing for as long, so a
//     cutscene's side-effect bits travel together. A register is sent on
//     each change, an OR register's new bits or a MAX register's new value,
//     but in the batch: the receiver writes registers with the story bits
//     on its next stage transition (story.Receiver) anyway, and a
//     cutscene's register writes (a letter sent, Beedle's points, the
//     Ghost Ship cleared) are as much its side effects as its bits;
//   - a memory switch waits with the batch, and one rising with no batch
//     starts one: a stage event's trigger switch is set as its event
//     starts (d_event_manager.cpp:42-47), on a tick that can still read
//     IDLE, and should not arrive before the bits the event sets
//     (story.Batcher). That is timing only; for the start events whose
//     switch syncs, the receiver's hold makes it sure (spawnSwitch);
//   - the inventory and collection flags and the capacities wait for the
//     game to be IDLE (§3: they are "live when idle"; gate.Status.Idle:
//     no event, pause menu, minigame or boss-refight snapshot), so an
//     item's flag, granted from inside the event that shows it (a chest's
//     demo item grants it as it is deleted, daDitem_c::Delete,
//     d_a_demo_item.cpp:438-441; inferred to be before its event ends),
//     goes as that event ends, and what a Ganon's Tower
//     refight's swap of the player's items shows (dComIfGs_setPlayer-
//     RecollectionData at the refight stage's start, d_s_play.cpp:
//     1394-1400; d_com_inf_game.cpp:1440, swapped back at :1579) goes, if
//     at all, once the refight is over;
//   - everything else (stage progress, the ocean and charts) goes at once.
//
// Nothing is lost by waiting: what waits is dropped only where the
// Differ drops its baseline, and the tick after that is Rebased, so the
// replica sends its whole WORLD, which carries it.
//
// The pace's clock is the tick count unless Pace is given one: each
// observed tick is TickEvery, so the batch goes after ten IDLE ticks. The
// replica's ticker never runs faster than TickEvery and skips the ticks a
// slow one overran, so that is at least story.BatchIdle of real time.

// tickEpoch is the tick clock's zero.
var tickEpoch = time.Unix(0, 0)

// pace is a paced Differ's state.
type pace struct {
	on    bool
	clock func() time.Time // nil: the tick clock
	ticks int64

	batch story.Batcher
	inv   save.Image // inventory flags (OR) and capacities (MAX) waiting for IDLE
}

// Pace turns on M4's sender pace (see above), with clock as its clock (nil:
// one TickEvery per observed tick). A Differ is unpaced until then, as M2
// and M3's is.
func (d *Differ) Pace(clock func() time.Time) {
	d.pace = pace{on: true, clock: clock}
}

// Paced reports whether Pace turned the pace on.
func (d *Differ) Paced() bool { return d.pace.on }

// now advances the pace's clock by one tick and returns it.
func (p *pace) now() time.Time {
	p.ticks++
	if p.clock != nil {
		return p.clock()
	}
	return tickEpoch.Add(time.Duration(p.ticks) * TickEvery)
}

// drop forgets what waits, where the Differ drops its baseline. The IDLE
// run is the game's, not the batch's, so it is kept (story.Batcher.Reset).
func (p *pace) drop() {
	p.batch.Reset()
	p.inv = save.Image{}
}

// quiet takes a tick the Differ skips (Link between stages, a stage load
// half done): no edges, and not an IDLE one, so a batch waits for the new
// stage to be quiet for story.BatchIdle.
func (p *pace) quiet(now time.Time) {
	if p.on {
		p.batch.Tick(now, false, nil)
	}
}

// release passes one tick's rising edges gs (ascending, within the policy)
// through the pace and returns what goes now, in ascending offset order.
// idle is the tick's gate.Status.Idle. An unpaced Differ returns gs.
func (p *pace) release(now time.Time, idle bool, gs []world.Gain) []world.Gain {
	if !p.on {
		return gs
	}
	var rest []world.Gain
	for _, g := range gs {
		if inventoryByte(int(g.Off)) {
			world.Apply(&p.inv, []world.Gain{g})
		} else {
			rest = append(rest, g)
		}
	}
	var out save.Image
	world.Apply(&out, p.batch.Tick(now, idle, rest))
	if idle {
		world.Apply(&out, nonZero(&p.inv))
		p.inv = save.Image{}
	}
	if gs := nonZero(&out); len(gs) > 0 {
		return gs
	}
	return nil
}

// waiting lists what the pace holds back, in ascending offset order: the
// story batch with its switches, and the inventory waiting for IDLE.
func (p *pace) waiting() []world.Gain {
	var all save.Image
	world.Apply(&all, p.batch.Waiting())
	world.Apply(&all, nonZero(&p.inv))
	return nonZero(&all)
}

// Waiting returns what the pace holds back, in ascending offset order: nil
// for an unpaced Differ.
func (d *Differ) Waiting() []world.Gain { return d.pace.waiting() }

// Withhold clears from img, an image about to be sent whole (the WORLD
// after a reconnect), every bit the pace holds back, and a MAX byte's
// whole field, so what waits does not reach the world early. The world
// merges only up, so the lower value is harmless; the held edges follow
// as gains in their time (story.Batcher.Withhold). An unpaced Differ
// withholds nothing.
func (d *Differ) Withhold(img *save.Image) {
	p := &d.pace
	if !p.on {
		return
	}
	p.batch.Withhold(img)
	for off, v := range p.inv {
		if v == 0 {
			continue
		}
		if r := class.Of(off); r.Class == class.Max {
			img[off] &^= r.Mask
		} else {
			img[off] &^= v
		}
	}
}

// inventoryByte reports whether image byte off is an inventory or
// collection flag or a capacity (§3), which the pace sends when IDLE.
func inventoryByte(off int) bool {
	c := CategoryOf(off)
	return c == CatInventory || c == CatCapacity
}

// nonZero lists img's non-zero bytes as gains, in ascending offset order.
func nonZero(img *save.Image) []world.Gain {
	var gs []world.Gain
	for off, v := range img {
		if v != 0 {
			gs = append(gs, world.Gain{Off: uint16(off), Val: v})
		}
	}
	return gs
}
