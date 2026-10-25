package replica

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
)

// TestStatusRoom: Status's Room is the world the hub sent, read by
// roomitems, and follows the relayed gains; nil before the world came in.
func TestStatusRoom(t *testing.T) {
	rr := newReplicaRig(t, Config{})
	if rr.r.Status().Room != nil {
		t.Fatal("a Room before any world")
	}
	rr = armedRig(t, Config{})
	var img save.Image
	img[addr.OffTact] = 0x01
	say(t, rr.r, worldOf(2, img))
	st := rr.r.Status()
	if st.Room == nil || !st.Room.Songs[0].Have || st.Room.Songs[1].Have || st.Room.HeartsWhy == "" {
		t.Fatalf("room %+v, want the Requiem and no hearts (no tables)", st.Room)
	}
	say(t, rr.r, &wire.GainsDown{Seq: 3, Origin: 2, Gains: []wire.Gain{{Off: uint16(addr.OffGetItem + save.SlotHookshot), Val: 0x01}}})
	st = rr.r.Status()
	if st.Room == nil {
		t.Fatal("no room after a relay")
	}
	have := false
	for _, it := range st.Room.Items {
		have = have || it.Slot == "hookshot" && it.Have
	}
	if !have {
		t.Fatalf("room after the partner's hookshot: %+v", st.Room.Items)
	}
}
