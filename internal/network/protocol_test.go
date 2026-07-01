package network

import (
	"net"
	"testing"
	"time"
)

func TestPingPongTypesDistinct(t *testing.T) {
	// Guard against a future edit reintroducing the 'P' collision: every
	// message type must be unique.
	types := map[byte]string{
		MsgJoin: "Join", MsgWelcome: "Welcome", MsgPosition: "Position",
		MsgPlayerList: "PlayerList", MsgLeave: "Leave", MsgChat: "Chat",
		MsgPose: "Pose", MsgPing: "Ping", MsgPong: "Pong",
	}
	if len(types) != 9 {
		t.Fatalf("expected 9 distinct message types, got %d (collision?)", len(types))
	}
	if MsgPong == MsgPosition {
		t.Fatal("MsgPong collides with MsgPosition")
	}
}

func TestPingPayloadRoundTrip(t *testing.T) {
	ts := monotonicNanos()
	payload := PingPayload(ts)
	if len(payload) != 8 {
		t.Fatalf("PingPayload len = %d, want 8", len(payload))
	}
	got, ok := ParsePingTimestamp(payload)
	if !ok || got != ts {
		t.Fatalf("ParsePingTimestamp = (%d,%v), want (%d,true)", got, ok, ts)
	}
	if _, ok := ParsePingTimestamp([]byte{1, 2, 3}); ok {
		t.Error("ParsePingTimestamp on a 3-byte payload should report ok=false")
	}
}

// TestWelcomeBackwardCompat asserts the extended [id][epoch:8] welcome still
// lets an OLD 1-byte-welcome reader recover the id from Data[0], and that a
// legacy 1-byte welcome parses with epochOK=false.
func TestWelcomeBackwardCompat(t *testing.T) {
	const id byte = 7
	const epoch uint64 = 0x1122334455667788

	data := WelcomeMessage(id, epoch)
	if len(data) != 9 {
		t.Fatalf("WelcomeMessage len = %d, want 9", len(data))
	}

	// New reader gets id + epoch.
	gotID, gotEpoch, ok := ParseWelcome(data)
	if !ok || gotID != id || gotEpoch != epoch {
		t.Errorf("ParseWelcome = (%d, %#x, %v), want (%d, %#x, true)", gotID, gotEpoch, ok, id, epoch)
	}

	// OLD 1-byte reader: still recovers the id from Data[0], ignores the tail.
	if data[0] != id {
		t.Errorf("legacy Data[0] = %d, want %d", data[0], id)
	}

	// A legacy 1-byte welcome parses with epoch 0 / epochOK=false.
	legacy := []byte{id}
	lid, lep, lok := ParseWelcome(legacy)
	if lid != id || lep != 0 || lok {
		t.Errorf("ParseWelcome(legacy) = (%d, %d, %v), want (%d, 0, false)", lid, lep, lok, id)
	}
}

// TestPingPongWireEcho exercises the on-the-wire echo: a peer that receives a
// MsgPing replies with a MsgPong carrying the identical 8 bytes, and the
// original sender derives a positive RTT from the echoed timestamp.
func TestPingPongWireEcho(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()

	go func() {
		msg, err := ReadMessage(serverSide)
		if err != nil {
			return
		}
		if msg.Type == MsgPing {
			// A delay comfortably above the monotonic-clock granularity
			// (~1ms on Windows) guarantees a measurable RTT even on a fast
			// in-memory pipe.
			time.Sleep(5 * time.Millisecond)
			WriteMessage(serverSide, MsgPong, msg.Data) // echo verbatim
		}
	}()

	ts := monotonicNanos()
	if err := WriteMessage(clientSide, MsgPing, PingPayload(ts)); err != nil {
		t.Fatalf("write ping: %v", err)
	}
	msg, err := ReadMessage(clientSide)
	if err != nil {
		t.Fatalf("read pong: %v", err)
	}
	if msg.Type != MsgPong {
		t.Fatalf("got type %q, want MsgPong (%q)", msg.Type, MsgPong)
	}
	echoed, ok := ParsePingTimestamp(msg.Data)
	if !ok || echoed != ts {
		t.Fatalf("echoed timestamp = (%d,%v), want (%d,true)", echoed, ok, ts)
	}
	if rtt := monotonicNanos() - echoed; rtt <= 0 {
		t.Errorf("computed RTT = %d ns, want > 0", rtt)
	}
}
