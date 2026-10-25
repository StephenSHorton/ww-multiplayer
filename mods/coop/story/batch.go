package story

import (
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
)

// BatchIdle is how long the sender's game must have been IDLE before its
// new story bits go out (§6.5), counted from the later of the start of the
// IDLE run and the batch's newest bit.
const BatchIdle = time.Second

// Batcher is the sender's side of story sync (§6.5). The Differ's rising
// edges go through it every tick:
//
//   - the story bits of the SyncMask and the allowlisted registers wait in
//     one batch, released once the game has been IDLE (gate.Status.Idle: no
//     event, pause menu, minigame or refight) for BatchIdle and the batch
//     has gained nothing for as long. A cutscene's side-effect bits are set
//     while its event runs, or as it ends, so they all travel in one batch;
//     a bit set on the very frame an event is ordered waits for that event
//     too, since the quiet second has not passed when it starts;
//   - story bits outside the SyncMask are dropped: they never sync, and the
//     hub's policy refuses them anyway;
//   - a memory switch waits with the batch, and one rising when no batch
//     waits starts one, so it too goes after the quiet second. A stage
//     event's spawn switch is set as its event starts
//     (dEvent_exception_c::setStartDemo, d_event_manager.cpp:42-47), on a
//     tick that can still read IDLE, and a receiver given it skips that
//     event (the same lines), so it should not arrive before the bits the
//     event sets (GT's sw30 went ahead of 0x3B02 live, D2(9)). A switch
//     rising while a batch waits does not put it off. This coupling is
//     timing only, so best effort: a batch goes at the first quiet IDLE
//     second, and the GT D2(9) reruns on #175 saw sw30 leave in one
//     while A's intro still played and 0x3B02, already in A's save, only
//     after it ended. The receiver's hold is the guarantee
//     (mods/coop/replica spawnSwitch: the switch waits for its bit in the
//     receiver's own game, and the stage change that writes the bit writes
//     it);
//   - every other byte that merges goes at once, and one that does not (the
//     Differ reports none) is dropped.
//
// A bit that rises on the tick the batch is due joins it, and so puts it
// off by BatchIdle: the batch goes once nothing has risen for that long.
// A batch that is waiting is not in the sender's WORLD either (Withhold).
// The zero Batcher is ready to use. It is not safe for concurrent use.
type Batcher struct {
	batch   save.Image // story bits (OR) and registers (OR bits or MAX values) waiting
	coupled save.Image // memory switch bits waiting with them
	waiting bool
	idleAt  time.Time // when the current IDLE run began; zero while the game is busy
	lastAt  time.Time // when the batch last gained a story bit or register
}

// Tick takes one sender tick: when its snapshot was read, whether the game
// was IDLE then, and the tick's rising edges (Edges.Gains: an OR byte's
// new bits, a MAX byte's new value). It returns the gains to send now, in
// ascending offset order: this tick's that need not wait and, when its
// time has come, the batch.
func (b *Batcher) Tick(now time.Time, idle bool, gains []world.Gain) []world.Gain {
	switch {
	case !idle:
		b.idleAt = time.Time{}
	case b.idleAt.IsZero():
		b.idleAt = now
	}

	// The story bits and registers first, so that a switch rising on the
	// same tick as a story bit waits with it.
	for _, g := range gains {
		off := int(g.Off)
		switch {
		case IsStoryByte(off):
			if v := g.Val & class.SyncMaskOf(off-addr.OffEvent); v != 0 && world.Apply(&b.batch, []world.Gain{{Off: g.Off, Val: v}}) > 0 {
				b.waiting, b.lastAt = true, now
			}
		case IsRegisterByte(off):
			if world.Apply(&b.batch, []world.Gain{g}) > 0 {
				b.waiting, b.lastAt = true, now
			}
		}
	}
	var out save.Image
	for _, g := range gains {
		off := int(g.Off)
		switch {
		case IsStoryByte(off), IsRegisterByte(off):
		case isSwitchByte(off):
			if world.Apply(&b.coupled, []world.Gain{g}) > 0 && !b.waiting {
				b.waiting, b.lastAt = true, now
			}
		default:
			world.Apply(&out, []world.Gain{g})
		}
	}

	if b.due(now) {
		world.Apply(&out, gainsOf(&b.batch))
		world.Apply(&out, gainsOf(&b.coupled))
		b.drop()
	}
	return gainsOf(&out)
}

// due reports whether the batch goes out at now: one waits, the game is
// IDLE, and BatchIdle has passed since both the IDLE run began and the
// batch last grew.
func (b *Batcher) due(now time.Time) bool {
	if !b.waiting || b.idleAt.IsZero() {
		return false
	}
	from := b.idleAt
	if b.lastAt.After(from) {
		from = b.lastAt
	}
	return now.Sub(from) >= BatchIdle
}

func (b *Batcher) drop() {
	b.batch, b.coupled, b.waiting, b.lastAt = save.Image{}, save.Image{}, false, time.Time{}
}

// Waiting returns what the batch holds, story bits, registers and the
// switches waiting with them, in ascending offset order; nil when no
// batch waits.
func (b *Batcher) Waiting() []world.Gain {
	if !b.waiting {
		return nil
	}
	all := b.batch
	world.Apply(&all, gainsOf(&b.coupled))
	return gainsOf(&all)
}

// Withhold clears from img, an image about to be sent whole (a WORLD after
// a reconnect or a new baseline), every bit the batch holds, and a MAX
// register's whole field, so the batch does not reach the world early.
// The world merges only up, so the lower value is harmless; the batch
// follows as gains when it is due.
func (b *Batcher) Withhold(img *save.Image) {
	if !b.waiting {
		return
	}
	for off := range img {
		if v := b.batch[off]; v != 0 {
			if r := class.Of(off); r.Class == class.Max {
				img[off] &^= r.Mask
			} else {
				img[off] &^= v
			}
		}
		img[off] &^= b.coupled[off]
	}
}

// Reset drops the batch: call it wherever the Differ drops its baseline
// (another file, a save state, a read error). The replica then sends its
// whole WORLD, which carries what the batch held. The IDLE run is the
// game's, not the batch's, so it is kept.
func (b *Batcher) Reset() { b.drop() }
