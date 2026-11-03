package network

import (
	"bytes"
	"net"
	"strings"
	"testing"
	"time"
)

// TestFrameCounterChunks feeds a stream of frames in every chunking from
// one byte at a time to all at once: the counts must not depend on it.
func TestFrameCounterChunks(t *testing.T) {
	var stream bytes.Buffer
	WriteMessage(&stream, MsgPose, SerializePose(42, make([]byte, 42*48), make([]byte, 64)))
	WriteMessage(&stream, MsgEnemy, []byte{EnemySubState, 4, 0, 0})
	WriteMessage(&stream, MsgPing, nil)
	WriteMessage(&stream, MsgCoop, []byte{0x04, 1, 2})
	WriteMessage(&stream, MsgEnemy, []byte{EnemySubPState})
	all := stream.Bytes()
	for chunk := 1; chunk <= len(all); chunk++ {
		var f frameCounter
		got := map[[2]byte]int64{}
		for i := 0; i < len(all); i += chunk {
			end := i + chunk
			if end > len(all) {
				end = len(all)
			}
			f.feed(all[i:end], func(typ, sub byte, size int64) { got[[2]byte{typ, sub}] += size })
		}
		want := map[[2]byte]int64{
			{MsgPose, 0}:               3 + 4 + 42*48 + 64,
			{MsgEnemy, EnemySubState}:  3 + 4,
			{MsgPing, 0}:               3,
			{MsgCoop, 0x04}:            3 + 3,
			{MsgEnemy, EnemySubPState}: 3 + 1,
		}
		if len(got) != len(want) {
			t.Fatalf("chunk %d: got %v, want %v", chunk, got, want)
		}
		for k, v := range want {
			if got[k] != v {
				t.Fatalf("chunk %d: %q/%d = %d, want %d", chunk, k[0], k[1], got[k], v)
			}
		}
	}
}

// TestServerWireStats counts a real player connection's join, hello and
// positions through a Server with Wire set.
func TestServerWireStats(t *testing.T) {
	s := NewServer(0)
	s.Wire = NewWireStats()
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	c := NewClient("Ann")
	if err := c.Connect(s.Addr().String()); err != nil {
		t.Fatal(err)
	}
	defer c.Disconnect()
	for i := 0; i < 5; i++ {
		if err := c.SendPosition(&PlayerPosition{PosX: float32(i)}); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		snap := s.Wire.Snapshot()
		pos := snap[WireKey{Name: "Ann", Role: RolePlayer, Up: true, Type: MsgPosition}]
		if pos.Frames == 5 {
			line := FormatWireRates(nil, snap, "Ann", 1)
			if !strings.Contains(line, "up pos") || !strings.Contains(line, "down W") {
				t.Fatalf("rates line %q", line)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("positions counted: %+v (all: %v)", pos, snap)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

var _ net.Conn = (*wireConn)(nil)
