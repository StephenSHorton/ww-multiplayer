package network

import (
	"testing"
	"time"
)

// TestNoSelfEcho: a receiver whose hello carries CapNoSelfEcho gets no
// position or pose from a connection under its own name (its
// broadcast-pose twin), and still gets everyone else's; a same-named
// receiver without the bit still gets its twin's, as before (wind#84).
func TestNoSelfEcho(t *testing.T) {
	s := NewServer(0)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	addr := s.Addr().String()
	connect := func(name string, skip bool) *Client {
		t.Helper()
		c := NewClient(name)
		if skip {
			c.SkipSelfEcho()
		}
		if err := c.Connect(addr); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(c.Disconnect)
		return c
	}
	recvSkip := connect("Ann", true) // Ann's puppet-sync, self-filtering
	recvOld := connect("Ann", false) // an Ann receiver from before the bit
	recvBob := connect("Bob", true)  // Bob's puppet-sync
	twin := connect("Ann", false)    // Ann's broadcast-pose
	time.Sleep(100 * time.Millisecond)

	for i := 0; i < 3; i++ {
		if err := twin.SendPosition(&PlayerPosition{PosX: float32(i + 1)}); err != nil {
			t.Fatal(err)
		}
		if err := twin.SendPose(1, make([]byte, 48), nil); err != nil {
			t.Fatal(err)
		}
	}
	got := func(c *Client) (pos, pose bool) {
		for _, rp := range c.GetRemotePlayers() {
			if rp.ID != twin.MyID() {
				continue
			}
			pos = rp.Position != nil && rp.Position.PosX == 3
			pose = rp.PoseMatrices != nil
		}
		return
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		bp, bm := got(recvBob)
		op, om := got(recvOld)
		if bp && bm && op && om {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Bob got pos %v pose %v, the old Ann receiver pos %v pose %v; want all", bp, bm, op, om)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if p, m := got(recvSkip); p || m {
		t.Fatalf("the self-filtering Ann receiver got its twin's pos %v pose %v", p, m)
	}
}
