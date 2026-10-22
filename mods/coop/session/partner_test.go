package coopsession

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/feed"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
)

// TestPublishPartner: a relayed PRESENCE reaches the pose feed's partner
// place of the same player and Dolphin, after the replica's own handler;
// other messages pass through untouched.
func TestPublishPartner(t *testing.T) {
	s := &coopSync{name: "publish-partner-test", client: network.NewCoopClient("publish-partner-test", 0)}
	var handled int
	s.client.OnCoop = func([]byte) { handled++ }
	s.publishPartner(3)

	st, err := wire.MakeStage("M_NewD2")
	if err != nil {
		t.Fatal(err)
	}
	b, err := wire.Encode(&wire.Presence{Stage: st, Table: 3, Room: 1, Layer: -1, Flags: wire.PresenceInGame})
	if err != nil {
		t.Fatal(err)
	}
	s.client.OnCoop(b)
	n, err := wire.Encode(&wire.Notice{Code: wire.NoticeNotBound, Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	s.client.OnCoop(n)
	if handled != 2 {
		t.Errorf("replica handler ran %d times, want 2", handled)
	}
	stage, inGame, moving, at := feed.PartnerFor("publish-partner-test", 3).Snapshot()
	if stage != "M_NewD2" || !inGame || moving || at.IsZero() {
		t.Errorf("partner place = %q in game %v moving %v at %v", stage, inGame, moving, at)
	}
}
