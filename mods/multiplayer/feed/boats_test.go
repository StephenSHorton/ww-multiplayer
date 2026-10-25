package feed

import "testing"

// TestBoatAnchors: each remote's anchor is its mini-Link's smoothed
// position, not its raw target.
func TestBoatAnchors(t *testing.T) {
	got := BoatAnchors([]RemoteView{{ID: 3, Pos: [3]float32{1, 2, 3}, Target: [3]float32{9, 9, 9}}, {ID: 5, Pos: [3]float32{4, 5, 6}}})
	if len(got) != 2 || got[3] != [3]float32{1, 2, 3} || got[5] != [3]float32{4, 5, 6} {
		t.Errorf("BoatAnchors = %v", got)
	}
	if len(BoatAnchors(nil)) != 0 {
		t.Error("anchors without views")
	}
}
