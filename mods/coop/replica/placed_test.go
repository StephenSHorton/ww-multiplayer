package replica

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/combat"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/proto"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// placedRig is the M6 rig granted placed-kill too, its partner in room 1,
// and the ordinal of the fixture's unkeyed Bokoblin (m6Unkeyed).
type placedRig struct {
	*m6Rig
	ord int
}

func newPlacedRig(t *testing.T) *placedRig {
	t.Helper()
	return newPlacedRigWith(t, nil)
}

func newPlacedRigWith(t *testing.T, claimed combat.ClaimFunc) *placedRig {
	t.Helper()
	rr := newM6RigWith(t, claimed)
	say(t, rr.r, &wire.Ctrl{Op: wire.CtrlCoopOff, Arg: uint32(proto.FeatureImage | proto.FeatureCombat | proto.FeaturePlacedKill)})
	pr := rr.tb.Enemies.PlacedIn(drc.Name, 1)
	if ords := pr.Ordinals(); len(ords) != 1 {
		t.Fatalf("room 1's placed ordinals %v, want the unkeyed Bokoblin's alone", ords)
	}
	p := &placedRig{m6Rig: rr, ord: pr.Ordinals()[0]}
	p.partnerIn(1)
	return p
}

func (p *placedRig) partnerIn(room int) {
	p.t.Helper()
	say(p.t, p.r, partnerMsg(p.t, drc, room))
}

// pkill is a partner's SCOPED PKILL of room room with ordinals ords.
func (p *placedRig) pkill(room int, ords ...int) {
	p.t.Helper()
	m := killMsg(p.t, drc, room, ords...)
	m.Kind = wire.ScopePlaced
	say(p.t, p.r, m)
}

// sent returns the PKILLs the replica sent since the last take.
func (p *placedRig) sent() []*wire.Scoped {
	var out []*wire.Scoped
	for _, m := range p.hub.take() {
		if sc, ok := m.(*wire.Scoped); ok && sc.Kind == wire.ScopePlaced {
			out = append(out, sc)
		}
	}
	return out
}

// health sets enemy i's health.
func (p *placedRig) health(i int, hp int8) {
	p.t.Helper()
	l, ok := p.live(i)
	if !ok {
		p.t.Fatalf("enemy %d is gone", i)
	}
	p.store(l.Addr+addr.ActorOffHealth, []byte{byte(hp)})
}

// die is enemy i's own death, seen dying by a tick first when sig is
// true.
func (p *placedRig) die(i int, sig bool) {
	p.t.Helper()
	l, ok := p.live(i)
	if !ok {
		p.t.Fatalf("enemy %d is gone", i)
	}
	if sig {
		p.health(i, 0)
		p.ticks(1)
	}
	if err := p.es.Kill(p.g, l.PID); err != nil {
		p.t.Fatal(err)
	}
	p.ticks(2)
}

// TestPlacedSend: an unkeyed enemy of Link's room seen dying and then gone
// is sent as its ordinal while the partner is in the room; without a
// signature, or with the partner elsewhere, nothing is.
func TestPlacedSend(t *testing.T) {
	p := newPlacedRig(t)
	p.ticks(2)
	p.hub.take()
	p.die(m6Unkeyed, true)
	got := p.sent()
	if len(got) != 1 || got[0].Room != 1 || len(got[0].Words) != 1 || got[0].Words[0] != (wire.ScopedWord{Word: uint8(p.ord / 32), Bits: 1 << (p.ord % 32)}) {
		t.Fatalf("sent %+v, want ordinal %d of room 1", got, p.ord)
	}
	if st := p.r.Status().Combat.Placed; st.Sent != 1 || st.Tracked != 0 {
		t.Errorf("status %+v", st)
	}
	// A keyed enemy's death is M6.0's, never a PKILL.
	p.die(m6CC, true)
	if got := p.sent(); len(got) != 0 {
		t.Errorf("a keyed death sent %+v", got)
	}
}

// TestPlacedSendNoSignature: a death the walk never saw dying is not sent
// (nosig): a removal is no kill.
func TestPlacedSendNoSignature(t *testing.T) {
	p := newPlacedRig(t)
	p.ticks(2)
	p.hub.take()
	p.die(m6Unkeyed, false)
	if got := p.sent(); len(got) != 0 {
		t.Fatalf("sent %+v", got)
	}
	if st := p.r.Status().Combat.Placed; st.Unsent["nosig"] != 1 {
		t.Errorf("status %+v", st)
	}
}

// TestPlacedSendFrozen: a frozen enemy that disappears is unknown, never
// sent; a thaw clears what was latched.
func TestPlacedSendFrozen(t *testing.T) {
	p := newPlacedRig(t)
	p.ticks(2)
	p.health(m6Unkeyed, 0)
	p.setIceMode(m6Unkeyed, 2)
	p.ticks(1)
	p.hub.take()
	p.die(m6Unkeyed, false)
	if got := p.sent(); len(got) != 0 {
		t.Fatalf("a frozen enemy's disappearance sent %+v", got)
	}
	if st := p.r.Status().Combat.Placed; st.Unsent["frozen"] != 1 {
		t.Errorf("status %+v", st)
	}
}

// TestPlacedSendAlone: with the partner in another room nothing is
// tracked or sent.
func TestPlacedSendAlone(t *testing.T) {
	p := newPlacedRig(t)
	p.partnerIn(2)
	p.ticks(2)
	p.hub.take()
	p.die(m6Unkeyed, true)
	if got := p.sent(); len(got) != 0 {
		t.Fatalf("sent %+v with the partner elsewhere", got)
	}
}

// TestPlacedTake: a partner's PKILL of the room's ordinal light-shrinks
// the unkeyed enemy, once, through M6's applier, and its vanishing is not
// sent back.
func TestPlacedTake(t *testing.T) {
	p := newPlacedRig(t)
	p.ticks(2)
	p.hub.take()
	p.pkill(1, p.ord)
	p.ticks(3)
	if p.timer(m6Unkeyed) <= 0 {
		t.Fatalf("timer %d after the partner's kill\n%s", p.timer(m6Unkeyed), p.log)
	}
	if p.timer(m6CC) != 0 || p.timer(m6BK) != 0 {
		t.Error("a keyed enemy was shrunk for a PKILL")
	}
	p.ticks(40)
	if _, ok := p.live(m6Unkeyed); ok {
		t.Fatal("the unkeyed Bokoblin outlived its shrink")
	}
	if got := p.sent(); len(got) != 0 {
		t.Errorf("the shrink was echoed: %+v", got)
	}
	st := p.r.Status().Combat
	if st.Placed.Taken != 1 || st.Placed.Pending != 0 || st.Shrunk != 1 || st.Placed.Unsent["echo"] != 1 {
		t.Errorf("status %+v", st)
	}
	if !p.log.has("placed: light-shrunk the partner's kill") {
		t.Errorf("the log:\n%s", p.log)
	}
	if err := p.bm.actorErr(); err != nil {
		t.Fatal(err)
	}
}

// TestPlacedRefused: an ordinal this side does not place, a PKILL of
// another room, one with the partner gone and one never granted are not
// taken.
func TestPlacedRefused(t *testing.T) {
	p := newPlacedRig(t)
	p.ticks(2)
	p.pkill(1, p.ord+1) // not placed here
	p.pkill(2, 0)       // room 2's only entry is keyed
	p.ticks(3)
	if p.timer(m6Unkeyed) != 0 {
		t.Fatal("shrunk for an ordinal not its own")
	}
	if st := p.r.Status().Combat.Placed; st.Refused != 2 || st.Pending != 0 {
		t.Errorf("status %+v", st)
	}
	p.pkill(1, p.ord)
	p.partnerIn(2)
	p.ticks(3)
	if p.timer(m6Unkeyed) != 0 {
		t.Fatal("shrunk with the partner in another room")
	}
	if st := p.r.Status().Combat.Placed; st.Dropped != 1 {
		t.Errorf("status %+v", st)
	}
	p.partnerIn(1)
	say(t, p.r, &wire.Ctrl{Op: wire.CtrlCoopOff, Arg: uint32(proto.FeatureImage | proto.FeatureCombat)})
	p.pkill(1, p.ord)
	p.ticks(3)
	if p.timer(m6Unkeyed) != 0 {
		t.Fatal("shrunk without placed-kill granted")
	}
}

// TestPlacedSimultaneous: a PKILL for an enemy this game killed too is
// dropped as gone, never applied to anything else.
func TestPlacedSimultaneous(t *testing.T) {
	p := newPlacedRig(t)
	p.ticks(2)
	p.health(m6Unkeyed, 0)
	p.pkill(1, p.ord)
	p.ticks(1)
	l, _ := p.live(m6Unkeyed)
	if err := p.es.Kill(p.g, l.PID); err != nil {
		t.Fatal(err)
	}
	p.ticks(3)
	st := p.r.Status().Combat
	if st.Placed.Gone != 1 || st.Placed.Pending != 0 || st.Shrunk != 0 {
		t.Errorf("status %+v", st)
	}
}

// TestPlacedCutoff: an enemy created after the kill arrived is never
// taken. Here room 1 is reloaded once the kill's cutoff is read: the
// Bokoblin it was for is gone, and the same placement created again has a
// higher pid, which the kill must spare (every room load creates an
// unkeyed enemy again, d_stage.cpp:1688-1708).
func TestPlacedCutoff(t *testing.T) {
	p := newPlacedRig(t)
	p.ticks(2)
	l, _ := p.live(m6Unkeyed)
	p.store(l.Addr+addr.ActorOffInitState, []byte{1}) // still creating: seen, not taken
	p.pkill(1, p.ord)
	p.ticks(1) // the cutoff is read
	if p.timer(m6Unkeyed) != 0 {
		t.Fatal("shrunk while creating")
	}
	if err := p.g.ReloadRoom(1); err != nil {
		t.Fatal(err)
	}
	p.ticks(1)
	n, ok := p.live(m6Unkeyed)
	if !ok || n.PID <= l.PID {
		t.Fatalf("the reload made %+v (ok %v), want a new pid above %d", n, ok, l.PID)
	}
	for range p.holdTicks() + 2 {
		p.tick()
	}
	st := p.r.Status().Combat
	if p.timer(m6Unkeyed) != 0 || st.Shrunk != 0 {
		t.Fatalf("the enemy created after the kill was shrunk: %+v", st)
	}
	if st.Placed.Pending != 0 || st.Placed.Gone != 1 {
		t.Errorf("status %+v", st.Placed)
	}
}

// TestPlacedMirrorClaimed: a pid the enemy mirror claims is neither
// tracked nor taken.
func TestPlacedMirrorClaimed(t *testing.T) {
	p := newPlacedRigWith(t, func(uint32) bool { return true })
	p.ticks(2)
	p.hub.take()
	p.pkill(1, p.ord)
	p.ticks(3)
	if p.timer(m6Unkeyed) != 0 {
		t.Fatal("a claimed enemy was shrunk")
	}
	p.die(m6Unkeyed, true)
	if got := p.sent(); len(got) != 0 {
		t.Fatalf("a claimed enemy's death was sent: %+v", got)
	}
}

// TestPlacedSaveState: a save-state load forgets the tracks and planned
// pids.
func TestPlacedSaveState(t *testing.T) {
	p := newPlacedRig(t)
	p.ticks(2)
	if st := p.r.Status().Combat.Placed; st.Tracked != 1 {
		t.Fatalf("tracked %d", st.Tracked)
	}
	p.r.mu.Lock()
	p.r.m6.pl.forget()
	n := len(p.r.m6.pl.tracks)
	p.r.mu.Unlock()
	if n != 0 {
		t.Errorf("%d tracks after forget", n)
	}
}
