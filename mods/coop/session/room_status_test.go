package coopsession

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/roomitems"
)

// TestCoopRoomOf: the replica's Room reaches the app's status, hearts
// and all; a pipeline before the world has none.
func TestCoopRoomOf(t *testing.T) {
	if cs := coopStatusOf("Link", replica.Status{}); cs.Room != nil {
		t.Fatalf("no world yet, room %+v", cs.Room)
	}
	it := roomitems.Items{
		Items:  []roomitems.Item{{Slot: "bow", Name: "the bow", Have: true}},
		Equip:  []roomitems.Item{{Slot: "sword"}},
		Songs:  []roomitems.Named{{Name: "Wind's Requiem", Have: true}},
		Shards: 3, Charts: 5, Wallet: 5000, Magic: 32,
		Hearts: &roomitems.Hearts{Containers: 6, ContainersTaken: 2, Pieces: 44, PiecesTaken: 9, Max: 29},
	}
	r := coopStatusOf("Link", replica.Status{Room: &it}).Room
	if r == nil || len(r.Items) != 1 || r.Items[0] != (session.CoopRoomItem{Slot: "bow", Name: "the bow", Have: true}) || r.Equip[0].Have || !r.Songs[0].Have || r.Shards != 3 || r.Charts != 5 || r.ChartsTotal != roomitems.ChartsTotal ||
		r.Wallet != 5000 || r.Magic != 32 || !r.Hearts || r.ContainersTaken != 2 || r.PiecesTaken != 9 || r.MaxLife != 29 {
		t.Fatalf("room %+v", r)
	}
}
