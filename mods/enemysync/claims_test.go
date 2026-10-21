package enemysync

import "testing"

// TestClaimSet: Replace swaps the whole set, pid 0 is never claimed, and
// a nil set claims nothing (a game without co-op).
func TestClaimSet(t *testing.T) {
	var none *ClaimSet
	none.Replace([]uint32{1})
	if none.Claimed(1) {
		t.Error("a nil set claimed a pid")
	}
	c := NewClaimSet()
	c.Replace([]uint32{0, 7, 9})
	if !c.Claimed(7) || !c.Claimed(9) || c.Claimed(0) || c.Claimed(8) {
		t.Error("claims after the first Replace")
	}
	c.Replace([]uint32{8})
	if c.Claimed(7) || !c.Claimed(8) {
		t.Error("Replace kept an old pid or lost the new one")
	}
}

// TestZoneEchoGated: the client never ORs a zone bit the host's dead[]
// word lacks (enemy-mirror-general.json death_and_drops.k1 (a)): only a
// bit the host's STATE of the same stage and room has passes, in any of
// the four words.
func TestZoneEchoGated(t *testing.T) {
	var words [DeadWords]uint32
	words[0] = 1 << 3
	words[2] = 1 << 15 // setID 79 (M_Dai r2's Bokoblin, design test_plan)
	host := &State{Stage: "M_Dai", Room: 2}
	host.SetDead(words)
	for _, c := range []struct {
		name  string
		host  *State
		stage string
		room  int8
		id    uint16
		want  bool
	}{
		{"the host has it", host, "M_Dai", 2, 3, true},
		{"the host has it, word 2", host, "M_Dai", 2, 79, true},
		{"the host lacks it", host, "M_Dai", 2, 4, false},
		{"the host lacks it, word 2", host, "M_Dai", 2, 78, false},
		{"another room", host, "M_Dai", 1, 3, false},
		{"another stage", host, "M_NewD2", 2, 3, false},
		{"past the dead words", host, "M_Dai", 2, 128, false},
		{"unkeyed", host, "M_Dai", 2, 0xFFFF, false},
		{"no STATE yet", nil, "M_Dai", 2, 3, false},
	} {
		if got := ZoneEchoAllowed(c.host, c.stage, c.room, c.id); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	// A STATE with every bit clear (a v4 STATE with v3 content, as the
	// host sends until batch 5) allows no echo at all.
	empty := &State{Stage: "M_Dai", Room: 2}
	for id := range uint16(32 * DeadWords) {
		if ZoneEchoAllowed(empty, "M_Dai", 2, id) {
			t.Fatalf("setID %d echoed with the host's dead words clear", id)
		}
	}
}
