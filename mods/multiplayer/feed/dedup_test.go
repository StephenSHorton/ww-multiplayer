package feed

import (
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
)

// TestPoseSampleSame: a sample repeats the last one only when everything
// it would send is equal (wind#84's dedup sends only a changed one, or a
// repeat every poseRepeatEvery).
func TestPoseSampleSame(t *testing.T) {
	mk := func() *poseSample {
		return &poseSample{
			pos:   &network.PlayerPosition{PosX: 1, PosY: 2, PosZ: 3, RotY: 4},
			place: network.Place{Stage: "sea", Room: 44, InPlay: true}, placeOK: true,
			pose: []byte{1, 2, 3}, face: []byte{9},
		}
	}
	a := mk()
	if !a.same(mk()) {
		t.Fatal("equal samples differ")
	}
	for name, change := range map[string]func(s *poseSample){
		"pos":     func(s *poseSample) { s.pos.PosY = 2.5 },
		"rot":     func(s *poseSample) { s.pos.RotY = 5 },
		"room":    func(s *poseSample) { s.place.Room = 1 },
		"placeOK": func(s *poseSample) { s.placeOK = false },
		"pose":    func(s *poseSample) { s.pose[1] = 0 },
		"no pose": func(s *poseSample) { s.pose = nil },
		"face":    func(s *poseSample) { s.face = []byte{8} },
	} {
		b := mk()
		change(b)
		if a.same(b) || b.same(a) {
			t.Errorf("%s: a changed sample reads as the same", name)
		}
	}
}

// TestSendDue: a new pose frame goes out at once; a sample that differs
// only in the position or trailer waits trailerEvery; a repeat waits
// poseRepeatEvery.
func TestSendDue(t *testing.T) {
	mk := func() *poseSample {
		return &poseSample{pos: &network.PlayerPosition{PosX: 1}, pose: []byte{1, 2}, face: []byte{9}}
	}
	last := mk()
	newPose, newShip, repeat := mk(), mk(), mk()
	newPose.pose[0] = 7
	newShip.face = []byte{8}
	for _, c := range []struct {
		name  string
		s     *poseSample
		since time.Duration
		want  bool
	}{
		{"new pose at once", newPose, 0, true},
		{"trailer change too soon", newShip, trailerEvery - time.Millisecond, false},
		{"trailer change after trailerEvery", newShip, trailerEvery, true},
		{"repeat", repeat, poseRepeatEvery - time.Millisecond, false},
		{"repeat as keepalive", repeat, poseRepeatEvery, true},
	} {
		if got := sendDue(c.s, last, c.since); got != c.want {
			t.Errorf("%s: sendDue = %v, want %v", c.name, got, c.want)
		}
	}
	if !sendDue(repeat, nil, 0) {
		t.Error("the first sample must go out")
	}
}
