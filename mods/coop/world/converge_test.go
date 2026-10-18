package world

import (
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
)

// A loopback model of two dry-run replicas and the hub's world, at the
// level of the merge: each replica's game sets (and sometimes clears) bits,
// its differ proposes rising edges, the network delivers in any order with
// duplicates, connections drop and come back (WORLD both ways), and the
// hub relays every effective delta. Whatever the schedule, once everything
// is delivered every replica's view of the world is the world, the world
// holds every bit a replica still has, and nothing a replica never had.

type replica struct {
	local    save.Image // what its game holds now
	baseline save.Image // what its differ last looked at
	view     save.Image // the world as the hub told it
	online   bool
	epoch    int // bumped on every reconnect; older frames are gone with their connection
}

type upFrame struct {
	from, epoch int
	gains       []Gain
	world       *save.Image
}

type downFrame struct {
	to, epoch int
	gains     []Gain
	world     *save.Image
}

type loop struct {
	r    *rand.Rand
	w    *World
	reps []*replica
	up   []upFrame
	down []downFrame
	sent save.Image // the join of every bit any replica ever proposed
}

// play is one step of a replica's game: new flags, raised counters, and
// now and then a cleared flag (the game resets bits: getSave, initDan,
// story clears), which must never travel.
func (l *loop) play(rp *replica) {
	for n := l.r.IntN(4); n >= 0; n-- {
		off := l.r.IntN(ImageSize)
		if isMax[off] {
			rp.local[off] = max(rp.local[off], byte(l.r.IntN(int(shared[off])+1)))
		} else {
			rp.local[off] |= byte(l.r.Uint32()) & byte(l.r.Uint32())
		}
	}
	if l.r.IntN(5) == 0 {
		off := l.r.IntN(ImageSize)
		if isMax[off] {
			rp.local[off] = 0
		} else {
			rp.local[off] &^= byte(l.r.Uint32())
		}
	}
}

// diff sends the replica's rising edges since its last look.
func (l *loop) diff(i int) {
	rp := l.reps[i]
	gs := Diff(&rp.baseline, &rp.local)
	rp.baseline = rp.local
	if !rp.online || len(gs) == 0 {
		return // offline: a reconnect's WORLD carries them
	}
	l.propose(upFrame{from: i, epoch: rp.epoch, gains: gs})
}

func (l *loop) propose(f upFrame) {
	var img save.Image
	if f.world != nil {
		img = *f.world
	} else {
		Apply(&img, f.gains)
	}
	l.sent = Join(&l.sent, &img)
	l.up = append(l.up, f)
}

// relay sends a delta to every online replica (the origin too: its view
// is the world's, whatever its game did since).
func (l *loop) relay(gs []Gain) {
	if len(gs) == 0 {
		return
	}
	for i, rp := range l.reps {
		if rp.online {
			l.down = append(l.down, downFrame{to: i, epoch: rp.epoch, gains: gs})
		}
	}
}

// deliverUp hands one queued proposal to the hub, sometimes keeping it
// queued so it arrives twice.
func (l *loop) deliverUp(keep bool) {
	if len(l.up) == 0 {
		return
	}
	k := l.r.IntN(len(l.up))
	f := l.up[k]
	if !keep {
		l.up = slices.Delete(l.up, k, k+1)
	}
	if rp := l.reps[f.from]; !rp.online || rp.epoch != f.epoch {
		return // its connection is gone, and so is the frame
	}
	if f.world != nil {
		res := l.w.Merge(f.world)
		img := l.w.Image()
		l.down = append(l.down, downFrame{to: f.from, epoch: f.epoch, world: &img})
		l.relay(res.Delta)
		return
	}
	l.relay(l.w.ApplyGains(f.gains).Delta)
}

func (l *loop) deliverDown(keep bool) {
	if len(l.down) == 0 {
		return
	}
	k := l.r.IntN(len(l.down))
	f := l.down[k]
	if !keep {
		l.down = slices.Delete(l.down, k, k+1)
	}
	rp := l.reps[f.to]
	if !rp.online || rp.epoch != f.epoch {
		return
	}
	if f.world != nil {
		rp.view = Join(&rp.view, f.world) // a late WORLD reply never takes back a newer relay
	} else {
		Apply(&rp.view, f.gains)
	}
}

func (l *loop) disconnect(i int) {
	l.reps[i].online = false
}

// reconnect is CO_HELLO then WORLD both ways (§5.5): the replica proposes
// its whole image, and the hub answers with the merged one.
func (l *loop) reconnect(i int) {
	rp := l.reps[i]
	rp.online = true
	rp.epoch++
	img := rp.local
	rp.baseline = rp.local
	l.propose(upFrame{from: i, epoch: rp.epoch, world: &img})
}

func TestConvergeUnderRandomSchedules(t *testing.T) {
	for seed := uint64(1); seed <= 40; seed++ {
		l := &loop{r: rand.New(rand.NewPCG(seed, 2026)), w: New(testID, testCreated)}
		l.w.SetPolicy(FullPolicy())
		for range 2 {
			l.reps = append(l.reps, &replica{})
		}
		for i := range l.reps {
			l.reconnect(i)
		}
		for step := 0; step < 1500; step++ {
			i := l.r.IntN(len(l.reps))
			switch p := l.r.IntN(100); {
			case p < 30:
				l.play(l.reps[i])
			case p < 50:
				l.diff(i)
			case p < 75:
				l.deliverUp(l.r.IntN(5) == 0)
			case p < 95:
				l.deliverDown(l.r.IntN(5) == 0)
			case p < 98:
				l.disconnect(i)
			default:
				if !l.reps[i].online {
					l.reconnect(i)
				}
			}
		}
		// Settle: everyone back online with a last look, everything
		// delivered.
		for i, rp := range l.reps {
			if !rp.online {
				l.reconnect(i)
			}
			l.diff(i)
		}
		for len(l.up) > 0 || len(l.down) > 0 {
			l.deliverUp(false)
			l.deliverDown(false)
		}

		world := l.w.Image()
		for i, rp := range l.reps {
			if rp.view != world {
				d := Diff(&rp.view, &world)
				t.Fatalf("seed %d: replica %d's view lacks %d bytes of the world: %v", seed, i, len(d), d)
			}
			if d := Diff(&world, &rp.local); len(d) > 0 {
				t.Fatalf("seed %d: the world lacks replica %d's %v", seed, i, d)
			}
		}
		if d := Diff(&l.sent, &world); len(d) > 0 {
			t.Fatalf("seed %d: the world holds bits nobody proposed: %v", seed, d)
		}
	}
}
