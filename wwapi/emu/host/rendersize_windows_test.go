//go:build windows

package host

import "testing"

// TestRenderSizeManyCalls: RenderSize and FindWindowByPID reuse their
// enum callbacks; a NewCallback per call panics past ~2000 (puppet-sync
// asks every few seconds for a whole session).
func TestRenderSizeManyCalls(t *testing.T) {
	for i := 0; i < 2500; i++ {
		if _, _, ok := RenderSize(0xFFFFFFF0); ok {
			t.Fatal("a window for a pid that can't exist")
		}
	}
}
