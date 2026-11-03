package network

import (
	"testing"
	"time"
)

// TestRecentRing: positions append numbered samples up to RecentCap, a
// pose lands on the newest, and a snapshot taken before is never changed.
func TestRecentRing(t *testing.T) {
	var rp RemotePlayer
	rp.addPose(42, []byte{1}, nil) // no position yet: dropped
	if len(rp.Recent) != 0 {
		t.Fatalf("a pose before any position made a sample")
	}
	t0 := time.Unix(0, 0)
	for i := 0; i < RecentCap+5; i++ {
		rp.addSample(&PlayerPosition{PosX: float32(i)}, t0.Add(time.Duration(i)*time.Millisecond))
	}
	snap := rp.Recent
	if len(snap) != RecentCap || snap[0].Seq != 6 || snap[len(snap)-1].Seq != RecentCap+5 {
		t.Fatalf("ring holds %d samples, seq %d..%d", len(snap), snap[0].Seq, snap[len(snap)-1].Seq)
	}
	rp.addPose(42, []byte{9}, []byte{1})
	rp.addSample(&PlayerPosition{PosX: 99}, t0)
	if snap[len(snap)-1].PoseMatrices != nil || snap[0].Seq != 6 {
		t.Errorf("a later message changed an earlier snapshot")
	}
	if got := rp.Recent[len(rp.Recent)-2]; got.PoseJoints != 42 || got.PoseMatrices[0] != 9 {
		t.Errorf("the pose didn't land on the newest sample: %+v", got)
	}
}
