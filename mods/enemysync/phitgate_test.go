package enemysync

import (
	"math"
	"testing"
	"time"

	posefeed "github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/feed"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// The PHIT sanity gates (ww-online review idea 24; players.go
// PHitStaleFrames and the rest).

func TestPHitSane(t *testing.T) {
	ok := [3]float32{10, 60, -10}
	for _, c := range []struct {
		name string
		spl  uint8
		rel  [3]float32
		rvec [3]float32
		want bool
	}{
		{"a spear hit", 6, ok, [3]float32{0, 0, 9}, true},
		{"the last SPL", PHitMaxSpl, ok, [3]float32{}, true},
		{"past the last SPL", PHitMaxSpl + 1, ok, [3]float32{}, false},
		{"at the bound", 0, [3]float32{PHitMaxRel, 0, 0}, [3]float32{}, true},
		{"far hit point", 0, [3]float32{PHitMaxRel, 1, 0}, [3]float32{}, false},
		{"NaN hit point", 0, [3]float32{float32(math.NaN()), 0, 0}, [3]float32{}, false},
		{"Inf vector", 0, ok, [3]float32{0, float32(math.Inf(1)), 0}, false},
	} {
		if got := phitSane(c.spl, c.rel, c.rvec); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// phitGateHost is remoteHostGame with the Moblin enabled, one remote
// (player 4, slot 0) whose PSTATE puts it in the host's stage and room,
// and the ring's first read done.
func phitGateHost(t *testing.T) (*RemoteHost, func(now time.Time, relFrame uint32, views []posefeed.RemoteView, hits ...mailbox.RemoteHit) []*PHit) {
	t.Helper()
	m, scope, r := remoteHostGame(t, mailbox.RemoteFlagDetect)
	r.Kinds, _ = ParseKinds("mo2")
	now := time.Unix(100, 0)
	views := []posefeed.RemoteView{{ID: 4, LinkSlot: 0, Live: true}}
	r.OnPState(&PState{Client: 2, PlayerID: 4, Stage: "M_Dai", Room: 1}, now)
	var st mailbox.RemoteStatus
	if _, err := r.Tick(now, scope, true, views, now, &Listing{}); err != nil {
		t.Fatal(err)
	}
	return r, func(at time.Time, relFrame uint32, vs []posefeed.RemoteView, hits ...mailbox.RemoteHit) []*PHit {
		t.Helper()
		hostRELFrame(t, m, relFrame)
		push(t, m, &st, hits...)
		if vs == nil {
			vs = views
		}
		out, err := r.Tick(at, scope, true, vs, at, &Listing{})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
}

// TestRemoteHostPHitSanity: fields no hit has and entries older than
// PHitStaleFrames are dropped (DropBad, DropStale), never sent; one frame
// of race (an entry newer than the frame read) is not stale.
func TestRemoteHostPHitSanity(t *testing.T) {
	r, tick := phitGateHost(t)
	now := time.Unix(100, 0)
	badSpl := ringHit(0, 0, 1, 300)
	badSpl.Spl = PHitMaxSpl + 1
	far := ringHit(0, 0, 2, 300)
	far.RelHit = [3]float32{0, 0, PHitMaxRel + 1}
	nan := ringHit(0, 0, 3, 300)
	nan.RVec[1] = float32(math.NaN())
	if out := tick(now, 300, nil, badSpl, far, nan); len(out) != 0 || r.Stats.DropBad != 3 {
		t.Fatalf("bad fields: %+v %+v", out, r.Stats)
	}
	stale := ringHit(0, 0, 4, 300-PHitStaleFrames-1)
	edge := ringHit(0, 0, 5, 300-PHitStaleFrames)
	ahead := ringHit(0, 0, 6, 301)
	out := tick(now, 300, nil, stale, edge, ahead)
	if len(out) != 2 || out[0].HostFrame != 300-PHitStaleFrames || out[1].HostFrame != 301 || r.Stats.DropStale != 1 {
		t.Fatalf("stale: %+v %+v", out, r.Stats)
	}
}

// TestRemoteHostPHitStage: a hit on a body whose owner's PSTATE names
// another stage or room, is loading, or went stale is dropped
// (DropStage), the client never sees it.
func TestRemoteHostPHitStage(t *testing.T) {
	for _, c := range []struct {
		name string
		ps   PState
		age  time.Duration
	}{
		{"other room", PState{Client: 2, PlayerID: 4, Stage: "M_Dai", Room: 2}, 0},
		{"other stage", PState{Client: 2, PlayerID: 4, Stage: "sea", Room: 1}, 0},
		{"loading", PState{Client: 2, PlayerID: 4, Stage: "M_Dai", Room: 1, Flags: PSLoading}, 0},
		{"stale PSTATE", PState{Client: 2, PlayerID: 4, Stage: "M_Dai", Room: 1}, PStateStale + time.Second},
	} {
		r, tick := phitGateHost(t)
		now := time.Unix(100, 0)
		r.OnPState(&c.ps, now)
		if out := tick(now.Add(c.age), 300, nil, ringHit(0, 0, 1, 300)); len(out) != 0 || r.Stats.DropStage != 1 {
			t.Errorf("%s: %+v %+v", c.name, out, r.Stats)
		}
	}
}

// TestRemoteHostPHitRange: a hit on a body that trails its player past
// SettleDist (the player warped, voided out or is sliding a jump) is
// dropped (DropRange); a body at its player's position is not.
func TestRemoteHostPHitRange(t *testing.T) {
	r, tick := phitGateHost(t)
	now := time.Unix(100, 0)
	trailing := []posefeed.RemoteView{{ID: 4, LinkSlot: 0, Live: true, Pos: [3]float32{0, 0, 0}, Target: [3]float32{SettleDist + 1, 0, 0}}}
	if out := tick(now, 300, trailing, ringHit(0, 0, 1, 300)); len(out) != 0 || r.Stats.DropRange != 1 {
		t.Fatalf("trailing body: %+v %+v", out, r.Stats)
	}
	close := []posefeed.RemoteView{{ID: 4, LinkSlot: 0, Live: true, Pos: [3]float32{100, 0, 0}, Target: [3]float32{100 + SettleDist - 1, 0, 0}}}
	if out := tick(now, 300, close, ringHit(0, 0, 2, 300)); len(out) != 1 {
		t.Fatalf("a body within SettleDist: %+v %+v", out, r.Stats)
	}
}

// TestRemoteHostPHitBurst: past PHitBurst PHITs at once to one client
// the rest are dropped (DropBurst), each from its own attacker so the
// per-attacker rate does not decide; the bucket refills at PHitPerSec,
// and a rate-limited hit costs no token.
func TestRemoteHostPHitBurst(t *testing.T) {
	r, tick := phitGateHost(t)
	now := time.Unix(100, 0)
	var hits []mailbox.RemoteHit
	for i := range PHitBurst + 2 {
		hits = append(hits, ringHit(0, 0, uint16(10+i), 300))
	}
	// The ring holds RemoteHitRingLen entries: push them a ring at a time.
	var sent int
	for len(hits) > 0 {
		n := min(len(hits), mailbox.RemoteHitRingLen)
		sent += len(tick(now, 300, nil, hits[:n]...))
		hits = hits[n:]
	}
	if sent != PHitBurst || r.Stats.DropBurst != 2 {
		t.Fatalf("burst: %d sent, %+v", sent, r.Stats)
	}
	// One second refills PHitPerSec tokens.
	later := now.Add(time.Second)
	sent = 0
	for i := range PHitPerSec + 1 {
		sent += len(tick(later, 300, nil, ringHit(0, 0, uint16(40+i), 300)))
	}
	if sent != PHitPerSec || r.Stats.DropBurst != 3 {
		t.Fatalf("refill: %d sent, %+v", sent, r.Stats)
	}
	// A hit the per-attacker rate drops spends nothing: after another
	// second, a repeat of attacker 40 within PHitRateFrames is DropRate,
	// and PHitPerSec new attackers still go.
	evenLater := later.Add(time.Second)
	rate0 := r.Stats.DropRate
	sent = len(tick(evenLater, 301, nil, ringHit(0, 0, 40, 301)))
	for i := range PHitPerSec {
		sent += len(tick(evenLater, 301, nil, ringHit(0, 0, uint16(60+i), 301)))
	}
	if sent != PHitPerSec || r.Stats.DropRate != rate0+1 {
		t.Fatalf("rate before burst: %d sent, %+v", sent, r.Stats)
	}
}

// TestRemoteClientPHitSanity: a PHIT held past PHitClientStale before
// the Tick, or with fields no hit has, is answered PACK dropped
// (PADGoGate) at once and never reaches the apply record.
func TestRemoteClientPHitSanity(t *testing.T) {
	now := time.Unix(100, 0)
	for _, c := range []struct {
		name  string
		edit  func(*PHit)
		at    time.Time
		stale bool
	}{
		{"held too long", nil, now.Add(-PHitClientStale - time.Millisecond), true},
		{"bad SPL", func(p *PHit) { p.Spl = PHitMaxSpl + 1 }, now, false},
		{"far hit point", func(p *PHit) { p.RelHit = [3]float32{PHitMaxRel, PHitMaxRel, 0} }, now, false},
	} {
		m, scope, r, g := remoteClientGame(t, true, mailbox.RemoteFlagApply)
		p := phitFor(5)
		if c.edit != nil {
			c.edit(p)
		}
		r.OnPHitAt(p, c.at)
		out, err := r.Tick(now, scope, g)
		_, packs := splitOut(t, out)
		if err != nil || len(packs) != 1 || packs[0].Result != PAckDropped || packs[0].Detail != PADGoGate || packs[0].Seq != 5 {
			t.Errorf("%s: %+v %v", c.name, packs, err)
		}
		if c.stale && r.Stats.DroppedStale != 1 || !c.stale && r.Stats.DroppedBad != 1 {
			t.Errorf("%s: stats %+v", c.name, r.Stats)
		}
		if blk, _, _, _ := mailbox.ReadRemoteBlock(m); blk.Apply.Seq != 0 {
			t.Errorf("%s: apply record written %+v", c.name, blk.Apply)
		}
	}
	// Just inside PHitClientStale is applied as before.
	m, scope, r, g := remoteClientGame(t, true, mailbox.RemoteFlagApply)
	r.OnPHitAt(phitFor(6), now.Add(-PHitClientStale))
	if out, _ := r.Tick(now, scope, g); len(out) != 1 { // the PSTATE only
		t.Errorf("fresh enough: %d payloads", len(out))
	}
	if blk, _, _, _ := mailbox.ReadRemoteBlock(m); blk.Apply.Seq == 0 {
		t.Error("a fresh PHIT was not written")
	}
}
