package enemysync

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"time"

	posefeed "github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/feed"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
)

// TestScopeOffsetsPinned: the STATE and PSTATE fields the server's relay
// scoping reads by offset (network enemy.go "Relay scoping") are where
// EncodeState and EncodePState put them, and the flag bits it tests are
// this package's.
func TestScopeOffsetsPinned(t *testing.T) {
	if network.EnemyStateFlagInScope != StateFlagInScope || network.EnemyPSLoading != PSLoading || network.EnemyStageLen != StageLen {
		t.Fatal("scoping flag bits or the stage length moved")
	}
	if StateFlagThin&(StateFlagAnim|StateFlagColliders|StateFlagInScope) != 0 {
		t.Fatal("the server's thin bit collides with a host STATE flag")
	}
	if network.EnemyPStateStale != PStateStale {
		t.Errorf("server PSTATE stale %v, host's %v", network.EnemyPStateStale, PStateStale)
	}
	st := hostState(1, 0)
	st.Stage, st.Room = "M_NewD2", -3
	b, err := EncodeState(st)
	if err != nil {
		t.Fatal(err)
	}
	if b[network.EnemyStateFlagsOff] != st.Flags || int(b[network.EnemyStateCountOff]) != len(st.Slots) ||
		!bytes.Equal(b[network.EnemyStateStageOff:network.EnemyStateStageOff+StageLen], []byte("M_NewD2\x00")) ||
		int8(b[network.EnemyStateRoomOff]) != -3 {
		t.Errorf("STATE header % x", b[:StateHeaderLen])
	}
	p, err := EncodePState(&PState{Flags: PSLoading, Stage: "sea", Room: 26})
	if err != nil {
		t.Fatal(err)
	}
	if p[network.EnemyPStateFlagsOff] != PSLoading ||
		!bytes.Equal(p[network.EnemyPStateStageOff:network.EnemyPStateStageOff+StageLen], []byte("sea\x00\x00\x00\x00\x00")) ||
		p[network.EnemyPStateRoomOff] != 26 {
		t.Errorf("PSTATE % x", p)
	}
}

// thinState is st as the server sends it to a client in another place:
// the header alone, count 0, the thin bit set.
func thinState(t *testing.T, st *State) []byte {
	t.Helper()
	b, err := EncodeState(st)
	if err != nil {
		t.Fatal(err)
	}
	b = append([]byte(nil), b[:StateHeaderLen]...)
	b[network.EnemyStateCountOff] = 0
	b[network.EnemyStateFlagsOff] |= StateFlagThin
	return b
}

// TestMirrorWaitsOnThinState: a thinned STATE decodes (the header is a
// valid count-0 STATE). One naming this game's own stage and room (its
// PSTATE lagged its arrival) neither arms the mirror on an empty list
// nor disarms it: the tick writes nothing and sleeps the normal cadence;
// the whole STATE after it arms as usual. One naming another room
// disarms with the host-room reason, as the whole STATE always did.
func TestMirrorWaitsOnThinState(t *testing.T) {
	start := time.Unix(3000, 0)
	_, m, _, tick := clientRoom(t)
	rep := &recReporter{}
	c := NewMirror(m, rep, nil, false, start)

	thin := thinState(t, hostState(1, 0))
	if st, err := DecodeState(thin); err != nil || len(st.Slots) != 0 || st.Flags&StateFlagThin == 0 {
		t.Fatalf("thin STATE decodes as %+v, %v", st, err)
	}
	m.Record(true)
	c.OnEnemy(thin, start)
	tick()
	hits, sleep, err := c.Tick(start)
	if err != nil || len(hits) != 0 || sleep != 0 {
		t.Fatalf("thin in-place tick: hits %v sleep %v err %v", hits, sleep, err)
	}
	if b := blockWrites(t, m); len(b) != 0 || c.mirroring {
		t.Fatalf("a thin STATE armed the mirror: %d blocks, mirroring %v", len(b), c.mirroring)
	}

	pushState(t, c, hostState(2, 16), start)
	tick()
	if _, _, err := c.Tick(start); err != nil {
		t.Fatal(err)
	}
	pushState(t, c, hostState(3, 32), start)
	tick()
	if _, _, err := c.Tick(start); err != nil {
		t.Fatal(err)
	}
	b := blockWrites(t, m)
	if len(b) == 0 || len(b[len(b)-1].Slots) != 3 {
		t.Fatalf("the whole STATE after the thin one did not arm: %+v", b)
	}

	other := hostState(4, 48)
	other.Room = 2
	c.OnEnemy(thinState(t, other), start)
	tick()
	if _, sleep, err := c.Tick(start); err != nil || sleep != GateSleep {
		t.Fatalf("thin other-room tick: sleep %v err %v", sleep, err)
	}
	if !strings.Contains(rep.String(), "the host is in M_Dai room 2") {
		t.Errorf("no host-room disarm logged:\n%s", rep.String())
	}
}

// TestReadPlaceMatchesScope: feed.ReadPlace (broadcast-pose's place
// trailer) and ReadScope (the enemy client's PSTATE) read the same stage,
// room and in-play state through a room in play, a load and a stage
// change pending, so the server scopes a player's poses and its enemy
// STATEs by one place.
func TestReadPlaceMatchesScope(t *testing.T) {
	g, m, _ := moblinRoom(t)
	check := func(what string) {
		t.Helper()
		sc, err := ReadScope(m)
		p, ok := posefeed.ReadPlace(m)
		if err != nil || !ok || p.Stage != sc.Stage || p.Room != sc.Room || p.InPlay != sc.InPlay {
			t.Errorf("%s: place %+v %v, scope %+v %v", what, p, ok, sc, err)
		}
	}
	check("in play")
	store(t, m, addr.OverlapReq, binary.BigEndian.AppendUint32(nil, addr.OverlapRequest))
	check("loading")
	store(t, m, addr.OverlapReq, binary.BigEndian.AppendUint32(nil, 0))
	if err := g.SetNextStage(fakegame.Stage{Name: "sea", Table: addr.TableSea}); err != nil {
		t.Fatal(err)
	}
	check("stage change pending")
}
