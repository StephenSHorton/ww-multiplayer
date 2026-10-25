package proto

import "testing"

// TestParseShares: the rules a host may change while its hub runs, by
// name; never the World, a hello flag, an unknown name or one twice.
func TestParseShares(t *testing.T) {
	f, err := ParseShares([]string{"story", " keys ", "", "live-switches"})
	if err != nil || f != FeatureStory|FeatureKeys|FeatureLiveSwitches {
		t.Fatalf("ParseShares = %v, %v", f, err)
	}
	if f, err := ParseShares(nil); err != nil || f != 0 {
		t.Fatalf("none: %v, %v", f, err)
	}
	for _, bad := range [][]string{{"image"}, {"merge"}, {"ask-bind"}, {"teleport"}, {"story", "story"}} {
		if _, err := ParseShares(bad); err == nil {
			t.Errorf("ParseShares(%q) took it", bad)
		}
	}
	if Shareable&FeatureImage != 0 || Shareable|FeatureImage != SessionFeatures {
		t.Errorf("Shareable = %v: want every session feature but the image", Shareable)
	}
}
