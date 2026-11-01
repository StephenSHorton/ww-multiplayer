package replica

import (
	"encoding/binary"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/proto"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
)

// Co-op for three or more (partners.go; docs/08 Appendix C item 171).

// down is a relayed PRESENCE from partner name on connection id.
func down(t *testing.T, id uint8, name, stage string, table, room int8, point int16, f wire.PresenceFlags) *wire.PresenceDown {
	t.Helper()
	var st wire.Stage
	if stage != "" {
		var err error
		if st, err = wire.MakeStage(stage); err != nil {
			t.Fatal(err)
		}
	}
	return &wire.PresenceDown{Presence: wire.Presence{Stage: st, Table: table, Room: room, Layer: -1, Point: point, Flags: f}, Origin: id, Name: name}
}

// goneOf is the hub's PRESENCE of nowhere for a player who left.
func goneOf(id uint8, name string) *wire.PresenceDown {
	return &wire.PresenceDown{Presence: wire.Presence{Table: -1, Room: -1, Layer: -1}, Origin: id, Name: name}
}

// TestPartnersInScope: a store is in scope while any partner is there
// (the hub keeps it while anyone is); one partner leaving drops only its
// own place, and the last one leaving takes the scope with it.
func TestPartnersInScope(t *testing.T) {
	rr := newRecRig(t, Config{}, proto.FeatureImage)
	st, _ := wire.MakeStage("M_NewD2")
	dan := scopeKey{kind: wire.ScopeDan, stage: st, table: 3, room: -1}
	zone3 := scopeKey{kind: wire.ScopeZone, stage: st, table: 3, room: 3}
	zone5 := scopeKey{kind: wire.ScopeZone, stage: st, table: 3, room: 5}
	in := func() (bool, bool, bool) {
		rr.r.mu.Lock()
		defer rr.r.mu.Unlock()
		return rr.r.partnerIn(dan), rr.r.partnerIn(zone3), rr.r.partnerIn(zone5)
	}
	say(t, rr.r, down(t, 2, "Tetra", "M_NewD2", 3, 3, 0, inGame), down(t, 3, "Medli", "sea", 0, 44, 0, inGame))
	if d, z3, z5 := in(); !d || !z3 || z5 {
		t.Fatalf("Tetra in DRC room 3, Medli at sea: dan %v zone3 %v zone5 %v", d, z3, z5)
	}
	say(t, rr.r, down(t, 3, "Medli", "M_NewD2", 3, 5, 0, inGame))
	if d, z3, z5 := in(); !d || !z3 || !z5 {
		t.Fatalf("both in DRC (rooms 3 and 5): dan %v zone3 %v zone5 %v", d, z3, z5)
	}
	say(t, rr.r, goneOf(2, "Tetra"))
	if d, z3, z5 := in(); !d || z3 || !z5 {
		t.Fatalf("Tetra gone, Medli in room 5: dan %v zone3 %v zone5 %v", d, z3, z5)
	}
	if st := rr.r.Status(); len(st.Partners) != 1 || st.Partners[0].Name != "Medli" || st.Partner == nil || st.Partner.Room != 5 {
		t.Fatalf("status after Tetra left: %+v, partner %+v", st.Partners, st.Partner)
	}
	say(t, rr.r, down(t, 3, "Medli", "sea", 0, 44, 0, inGame))
	if d, z3, z5 := in(); d || z3 || z5 {
		t.Fatalf("nobody in DRC: dan %v zone3 %v zone5 %v", d, z3, z5)
	}
}

// TestPartnersReconnect: a player who reconnects has a new connection;
// the old one's PRESENCE of nowhere, coming after the new one's first,
// drops only the old.
func TestPartnersReconnect(t *testing.T) {
	rr := newRecRig(t, Config{}, proto.FeatureImage)
	say(t, rr.r, down(t, 2, "Tetra", "M_NewD2", 3, 3, 0, inGame))
	say(t, rr.r, down(t, 4, "Tetra", "M_NewD2", 3, 3, 0, inGame))
	say(t, rr.r, goneOf(2, "Tetra"))
	st := rr.r.Status()
	if len(st.Partners) != 1 || st.Partners[0].Name != "Tetra" || st.Partner == nil || st.Partner.Room != 3 {
		t.Fatalf("after the old connection's goodbye: %+v", st.Partners)
	}
}

// TestPartnersStatus: with two partners Status.Partner is nil (no one
// partner to show) and Partners lists both by name.
func TestPartnersStatus(t *testing.T) {
	rr := newRecRig(t, Config{}, proto.FeatureImage)
	say(t, rr.r, down(t, 3, "Medli", "sea", 0, 44, 0, inGame), down(t, 2, "Tetra", "M_NewD2", 3, 3, 0, inGame))
	st := rr.r.Status()
	if st.Partner != nil || len(st.Partners) != 2 || st.Partners[0].Name != "Medli" || st.Partners[1].Name != "Tetra" {
		t.Fatalf("partner %+v, partners %+v", st.Partner, st.Partners)
	}
}

// namedPos is a PartnerPos with a position per partner.
type namedPos struct {
	mu sync.Mutex
	at map[string]Pos
}

func (np *namedPos) get(name string) (Pos, bool) {
	np.mu.Lock()
	defer np.mu.Unlock()
	p, ok := np.at[name]
	return p, ok
}

// TestWarpToNamedPartner: with two partners on Link's stage, a warp must
// name one (WarpTo), and goes onto that one's position; a bare warp and an
// unknown name are refused with the names to pick from.
func TestWarpToNamedPartner(t *testing.T) {
	np := &namedPos{at: map[string]Pos{"Tetra": {X: 100, Y: 5, Z: -200}, "Medli": {X: -50, Y: 0, Z: 75}}}
	clk := &breakerClock{t: time.Unix(1_800_000_000, 0)}
	rr := newRecRig(t, Config{Apply: true, PartnerPos: np.get, Now: clk.Now}, proto.FeatureImage)
	er := &escRig{recRig: rr, clk: clk}
	say(t, rr.r, down(t, 2, "Tetra", "sea", 0, 44, 0, inGame), down(t, 3, "Medli", "sea", 0, 44, 0, inGame))
	er.loadRoom(44, true)
	er.settled()

	if res := er.escape(EscapeWarp); res.Err == nil || !strings.Contains(res.Err.Error(), "say which partner to warp to: Medli, Tetra") {
		t.Fatalf("a bare warp with two partners: %+v", res)
	}
	if res := er.warpTo("Komali"); res.Err == nil || !strings.Contains(res.Err.Error(), "no partner Komali here (partners: Medli, Tetra)") {
		t.Fatalf("a warp to nobody: %+v", res)
	}
	if seq, _ := er.warpSeq(); seq != 0 {
		t.Fatalf("a refused warp wrote warp_seq %d", seq)
	}
	res := er.warpTo("Medli")
	if res.Err != nil {
		t.Fatalf("warp to Medli: %v\n%s", res.Err, er.log)
	}
	if !strings.Contains(res.Did, "warping to Medli at (-50, 0, 75)") {
		t.Errorf("did %q", res.Did)
	}
	b := er.read(WarpPos, 12)
	if x := math.Float32frombits(binary.BigEndian.Uint32(b)); x != -50 {
		t.Errorf("warp_x %v, want Medli's -50", x)
	}
	// Tetra leaves: one partner again, and a bare warp goes to Medli.
	say(t, rr.r, goneOf(2, "Tetra"))
	er.store(WarpAck, []byte{0, 0, 0, 1})
	if res := er.escape(EscapeWarp); res.Err != nil || !strings.Contains(res.Did, "Medli") {
		t.Fatalf("a bare warp with one partner left: %+v", res)
	}
}

// warpTo is escape for WarpTo(name).
func (er *escRig) warpTo(name string) EscapeResult {
	er.t.Helper()
	ch := er.r.WarpTo(name)
	for range 5 {
		select {
		case res := <-ch:
			return res
		default:
		}
		er.tick()
	}
	select {
	case res := <-ch:
		return res
	default:
	}
	er.t.Fatalf("WarpTo(%s) never answered", name)
	return EscapeResult{}
}

// TestPlacedKillsAnyPartner: M6.1's "a partner in the room" (the sender's
// tracking, the receiver's pending kills) is any partner, as the scoped
// stores' is.
func TestPlacedKillsAnyPartner(t *testing.T) {
	rr := newRecRig(t, Config{}, proto.FeatureImage)
	st, _ := wire.MakeStage("M_NewD2")
	in := func() bool {
		rr.r.mu.Lock()
		defer rr.r.mu.Unlock()
		return rr.r.partnerInRoom(st, 3, 5)
	}
	say(t, rr.r, down(t, 2, "Tetra", "sea", 0, 44, 0, inGame), down(t, 3, "Medli", "M_NewD2", 3, 5, 0, inGame))
	if !in() {
		t.Fatal("Medli in DRC room 5, Tetra at sea: no partner in the room")
	}
	say(t, rr.r, down(t, 3, "Medli", "M_NewD2", 3, 4, 0, inGame))
	if in() {
		t.Fatal("nobody in room 5: a partner in the room")
	}
	say(t, rr.r, down(t, 2, "Tetra", "M_NewD2", 3, 5, 0, inGame))
	if !in() {
		t.Fatal("Tetra came to room 5: no partner in the room")
	}
}
