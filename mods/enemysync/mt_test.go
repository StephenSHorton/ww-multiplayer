package enemysync

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// Batch D's Magtail (enemy-mirror-batch-d.json "magtail"; batch C's row
// restored), the first cut: an MT cut deletes this file with mt.go. The Go
// implementer restores the sampler, classify, hit and liveSticky tests
// from archive/enemy-batch-c/mt-row-restore.patch (CONTRACT.md "Go").

// TestMtLiveRefusal: em_mt_gate's terms at listing, one at a time; MOVE,
// FIGHT and the pinned mode pass.
func TestMtLiveRefusal(t *testing.T) {
	for _, c := range []struct {
		name string
		g    LiveGate
		want string
	}{
		{"move", LiveGate{Mode: MtModeMove}, ""},
		{"fight", LiveGate{Mode: MtModeFight}, ""},
		{"pinned", LiveGate{Mode: MtModePinned}, ""},
		{"ball", LiveGate{Mode: MtModeBall}, "mode2"},
		{"dead", LiveGate{Mode: MtModeDead}, "mode3"},
		{"appear wait", LiveGate{MtAppear: 0xE4}, "appear"},
		{"death flag", LiveGate{MtDeath: 2}, "death"},
		{"killed", LiveGate{MtKilled: 1}, "killed"},
		{"light shrink", LiveGate{MtIce: 1}, "ice"},
		{"carried", LiveGate{Carried: true}, "carried"},
	} {
		if got := MtLiveRefusal(c.g); got != c.want || KindMT.Row().LiveOK(c.g) != (c.want == "") {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// TestMtDeathSwitch: the Magtail's mDeathSwitch (+0x2B9); BB has none.
func TestMtDeathSwitch(t *testing.T) {
	if DeathSwitchOff(KindMT) != addr.MtOffDeathSwitch || DeathSwitchOff(KindBB) != 0 {
		t.Errorf("DeathSwitchOff mt 0x%X bb 0x%X", DeathSwitchOff(KindMT), DeathSwitchOff(KindBB))
	}
}
