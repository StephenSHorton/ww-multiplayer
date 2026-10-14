package network

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// Fuzz targets for protocol 2: the hello's role and caps, and the server's
// and client's co-op paths end to end, in the style of fuzz_test.go. To fuzz
// one, e.g.:
//
//	go test -run='^$' -fuzz=FuzzServerHandleCoop -fuzztime=20s ./internal/network/

func FuzzParseHelloRole(f *testing.F) {
	for _, seed := range [][]byte{
		HelloMessage(ProtocolVersion), HelloMessage(1), HelloRoleMessage(ProtocolVersion, RoleCoop, 0xBEEF),
		nil, {0}, {0, 2, 1}, {0, 2, 1, 0}, {0, 2, 9, 0, 0, 'x'},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		v, role, caps, err := ParseHelloRole(data)
		ok := len(data) == 2 || len(data) >= helloRoleLen
		if (err == nil) != ok {
			t.Fatalf("len %d: err = %v", len(data), err)
		}
		if err != nil {
			return
		}
		if pv, _ := ParseHello(data); pv != v {
			t.Fatalf("version %d, ParseHello says %d", v, pv)
		}
		if len(data) == 2 {
			if role != RolePlayer || caps != 0 {
				t.Fatalf("a 2-byte hello parsed as role %d caps %#x", role, caps)
			}
			return
		}
		if got := HelloRoleMessage(v, role, caps); !bytes.Equal(got, data[:helloRoleLen]) {
			t.Fatalf("round trip = %x, want %x", got, data[:helloRoleLen])
		}
	})
}

// coopFrames returns the payloads of the MsgCoop frames ReadMessage takes
// from stream before its first error, as the server's read loop would.
func coopFrames(stream []byte) [][]byte {
	var out [][]byte
	r := bytes.NewReader(stream)
	for {
		msg, err := ReadMessage(r)
		if err != nil {
			return out
		}
		if msg.Type == MsgCoop {
			out = append(out, msg.Data)
		}
	}
}

// FuzzServerHandleCoop runs a co-op connection's whole server path (hello,
// name check, co-op ID, Attach, the read loop, teardown) over a join with an
// arbitrary name followed by an arbitrary client stream. It must not panic;
// the handler must see Attach, exactly the stream's 'O' frames in order and
// one Detach, or nothing at all for a refused name; and no player or co-op
// connection may outlive the stream.
func FuzzServerHandleCoop(f *testing.F) {
	var stream []byte
	stream = append(stream, frame(MsgCoop, []byte{0x01, 0x00, 0x01})...)
	stream = append(stream, frame(MsgPosition, (&PlayerPosition{PosX: 1}).Serialize())...)
	stream = append(stream, frame(MsgChat, []byte("hi"))...)
	stream = append(stream, frame(MsgPing, PingPayload(1))...)
	stream = append(stream, frame(MsgCoop, nil)...)
	stream = append(stream, frame(MsgPose, SerializePose(1, make([]byte, 48), nil))...)
	f.Add("alice", uint16(0), stream)
	f.Add("", uint16(1), stream)
	f.Add("bad\nname", uint16(0xFFFF), stream)
	f.Add("alice", uint16(2), []byte{MsgCoop, 0xFF, 0xFF, 1})
	f.Fuzz(func(t *testing.T, name string, caps uint16, stream []byte) {
		if len(name) > MaxPayload {
			return // can't be framed as a join at all
		}
		in := append(frame(MsgJoin, []byte(name)), frame(MsgHello, HelloRoleMessage(ProtocolVersion, RoleCoop, caps))...)
		in = append(in, stream...)
		rec := newCoopRecorder()
		srv := NewServer(0)
		srv.Coop = rec
		srv.handlePlayer(newScriptConn(in))

		if srv.PlayerCount() != 0 || srv.CoopCount() != 0 {
			t.Fatalf("PlayerCount=%d CoopCount=%d after the stream ended, want 0 and 0", srv.PlayerCount(), srv.CoopCount())
		}
		ev := rec.snapshot()
		if ValidatePlayerName(name) != nil {
			if len(ev) != 0 {
				t.Fatalf("a refused name reached the handler: %q", ev)
			}
			return
		}
		frames := coopFrames(stream)
		if len(ev) != len(frames)+2 {
			t.Fatalf("handler events %q for %d 'O' frames", ev, len(frames))
		}
		if !strings.HasPrefix(ev[0], "attach 1 ") || ev[len(ev)-1] != "detach 1" {
			t.Fatalf("events %q, want attach first and detach last", ev)
		}
		for i, p := range frames {
			if want := fmt.Sprintf("frame 1 %x", p); ev[i+1] != want {
				t.Fatalf("event %d = %q, want %q", i+1, ev[i+1], want)
			}
		}
	})
}

// FuzzServerHello runs the server over an arbitrary hello payload, with
// co-op on, then an arbitrary stream. Whatever the hello says, nothing may
// panic, every Attach must have its Detach, and nothing may outlive the
// stream.
func FuzzServerHello(f *testing.F) {
	stream := frame(MsgCoop, []byte{0x08, 0x01, 0, 0, 0, 0})
	for _, hello := range [][]byte{
		HelloMessage(ProtocolVersion), HelloMessage(1), HelloRoleMessage(ProtocolVersion, RoleCoop, 0),
		HelloRoleMessage(ProtocolVersion, 2, 0), {0, 2, 1}, nil,
	} {
		f.Add(hello, stream)
	}
	f.Fuzz(func(t *testing.T, hello, stream []byte) {
		if len(hello) > MaxPayload {
			return
		}
		in := append(frame(MsgJoin, []byte("fuzz")), frame(MsgHello, hello)...)
		in = append(in, stream...)
		rec := newCoopRecorder()
		srv := NewServer(0)
		srv.Coop = rec
		srv.handlePlayer(newScriptConn(in))
		if srv.PlayerCount() != 0 || srv.CoopCount() != 0 {
			t.Fatalf("PlayerCount=%d CoopCount=%d after the stream ended", srv.PlayerCount(), srv.CoopCount())
		}
		if at, de := rec.counts(); at != de || at > 1 {
			t.Fatalf("attached %d, detached %d", at, de)
		}
		if _, role, _, err := ParseHelloRole(hello); err != nil || role != RoleCoop {
			if ev := rec.snapshot(); len(ev) != 0 {
				t.Fatalf("hello %x is not a co-op hello, but the handler saw %q", hello, ev)
			}
		}
	})
}

// FuzzCoopClientReadLoop runs a co-op client's read loop over an arbitrary
// server stream. It must never reach its panic safety net, and OnCoop must
// get exactly the stream's 'O' payloads in order.
func FuzzCoopClientReadLoop(f *testing.F) {
	f.Add(seedStream())
	f.Add(append(frame(MsgCoop, []byte{0x02, 0, 0}), frame(MsgPing, PingPayload(3))...))
	f.Add(frame(MsgCoop, nil))
	f.Fuzz(func(t *testing.T, stream []byte) {
		sc := newScriptConn(stream)
		c := NewCoopClient("me", 0)
		var recovered string
		c.OnLog = func(s string) {
			if strings.Contains(s, "internal error") {
				recovered = s
			}
		}
		var got [][]byte
		c.OnCoop = func(p []byte) { got = append(got, p) }
		c.conn = sc
		c.connected.Store(true)
		c.readLoop(sc)
		if recovered != "" {
			t.Fatalf("read loop panicked: %s", recovered)
		}
		want := coopFrames(stream)
		if len(got) != len(want) {
			t.Fatalf("OnCoop got %d payloads, want %d", len(got), len(want))
		}
		for i := range want {
			if !bytes.Equal(got[i], want[i]) {
				t.Fatalf("payload %d = %x, want %x", i, got[i], want[i])
			}
		}
	})
}
