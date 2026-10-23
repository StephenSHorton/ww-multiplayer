package switches

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// TestLiveUses: the live-actor adapter (G2) names the bit a live actor
// reads, routed with its home room unless the decoder routes it otherwise:
// DRC r7's ambusher Bokoblin's room switch 0xE0 in room 7, a key door's
// lock in room -1; switch 0xFF is no use, and a proc no decoder reads has
// none.
func TestLiveUses(t *testing.T) {
	u, ok := LiveRole(stage.ProcBK, 0xFFFF3203, [3]int16{0, 0, 0x00E0}, 7, RoleDeath)
	if !ok || u.No != 0xE0 || u.Room != 7 || u.Store != addr.SwitchRoom || !u.Write || u.Op != OpSet || u.Class != Latching {
		t.Errorf("Bokoblin death switch: %+v %v", u, ok)
	}
	if _, ok := LiveRole(stage.ProcBK, 0xFFFF3203, [3]int16{0, 0, 0x00FF}, 7, RoleDeath); ok {
		t.Error("death switch 0xFF is a use")
	}
	// M_NewD2's keyshut for sw54 (TGDR 8), between rooms 14 and 3.
	u, ok = LiveRole(stage.ProcDoor10, 0x0FFFF436, [3]int16{206, 0, -193}, -1, RoleKeyLock)
	if !ok || u.No != 0x36 || u.Room != -1 || u.Store != addr.SwitchMemory {
		t.Errorf("key door lock: %+v %v", u, ok)
	}
	us, ok := LiveUses(stage.ProcDoor10, 0x0FFFF436, [3]int16{206, 0, -193}, -1)
	roles := 0
	for _, u := range us {
		if u.Role != RoleNone {
			roles++
		}
	}
	if !ok || roles != 1 {
		t.Errorf("door uses %+v: %d with a role, want 1", us, roles)
	}
	if _, ok := LiveUses(stage.Proc(0x0A9), 0, [3]int16{}, 0); ok {
		t.Error("Link decodes")
	}
}
