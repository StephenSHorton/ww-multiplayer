package class

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// TestStateDefaultSensitive pins wwapi/state's default fold mask, byte for
// byte, to the one state derived from this table before it stopped
// importing class: OR and OR_MASKED bytes fold as flags within the class
// mask, MAX bytes as MAX within it, and every other byte keeps the first
// read. A class change that moves a merging byte must move
// state.DefaultSensitive with it.
func TestStateDefaultSensitive(t *testing.T) {
	type rule struct {
		fold state.Fold
		mask byte
	}
	var got [addr.SaveImageSize]rule
	var claims [addr.SaveImageSize]int
	for _, sp := range state.DefaultSensitive() {
		if sp.Off < 0 || sp.Len <= 0 || sp.Off+sp.Len > addr.SaveImageSize {
			t.Fatalf("span %+v is outside the image", sp)
		}
		for i := sp.Off; i < sp.Off+sp.Len; i++ {
			claims[i]++
			got[i] = rule{sp.Fold, sp.Mask}
			if sp.Fold == state.FoldNone {
				got[i] = rule{}
			}
		}
	}
	for off := range addr.SaveImageSize {
		r := Of(off)
		var want rule
		switch r.Class {
		case OR, ORMasked:
			want = rule{state.FoldBits, r.Mask}
		case Max:
			want = rule{state.FoldMax, r.Mask}
		}
		if got[off] != want {
			t.Errorf("image 0x%03X (%s %s mask %#02x): state folds %+v, want %+v",
				off, r.Field, r.Class, r.Mask, got[off], want)
		}
		if claims[off] > 1 {
			t.Errorf("image 0x%03X (%s): in %d default spans", off, r.Field, claims[off])
		}
	}
}
