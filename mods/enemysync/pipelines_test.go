package enemysync

import (
	"os"
	"testing"
)

// TestEnabledDefault: enemy sync is on by default (unset, blank, 1 or any
// other value) and off only with WW_ENEMY_SYNC=0.
func TestEnabledDefault(t *testing.T) {
	t.Setenv("WW_ENEMY_SYNC", "")
	os.Unsetenv("WW_ENEMY_SYNC")
	if !Enabled() {
		t.Error("off with WW_ENEMY_SYNC unset")
	}
	for v, want := range map[string]bool{"": true, "1": true, "yes": true, "0": false} {
		t.Setenv("WW_ENEMY_SYNC", v)
		if Enabled() != want {
			t.Errorf("WW_ENEMY_SYNC=%q: Enabled %v, want %v", v, !want, want)
		}
	}
}
