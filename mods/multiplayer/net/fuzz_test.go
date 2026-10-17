package network

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// Native fuzz targets for every parser that reads bytes off the wire, plus
// the client read loop and the server's per-player loop end to end. The seed
// corpora run as ordinary tests under `go test`; to fuzz one, e.g.:
//
//	go test -run='^$' -fuzz=FuzzParsePlayerList -fuzztime=20s ./mods/multiplayer/net/

// frame returns one framed message as wire bytes.
func frame(msgType byte, data []byte) []byte {
	var b bytes.Buffer
	if err := WriteMessage(&b, msgType, data); err != nil {
		panic(err)
	}
	return b.Bytes()
}

// seedStream is a representative server->client stream: one frame of each
// type a client handles, including the truncated player list that used to
// panic parsePlayerList.
func seedStream() []byte {
	list, _ := PlayerListMessage([]PlayerEntry{{ID: 1, Name: "me"}, {ID: 2, Name: "other"}})
	pos := PositionMessage(2, &PlayerPosition{PosX: 1, PosY: 2, PosZ: 3, RotY: 4})
	var s []byte
	s = append(s, frame(MsgWelcome, WelcomeMessage(1, 42))...)
	s = append(s, frame(MsgPlayerList, list)...)
	s = append(s, frame(MsgPosition, pos)...)
	s = append(s, frame(MsgPose, PoseRelayMessage(2, 42, make([]byte, 42*48), make([]byte, 16)))...)
	s = append(s, frame(MsgChat, ChatRelayMessage("other", "hi"))...)
	s = append(s, frame(MsgPing, PingPayload(7))...)
	s = append(s, frame(MsgPong, PingPayload(7))...)
	s = append(s, frame(MsgLeave, []byte{2})...)
	s = append(s, frame(MsgPlayerList, []byte{1, 5})...)
	return s
}

func FuzzParsePlayerList(f *testing.F) {
	good, _ := PlayerListMessage([]PlayerEntry{{ID: 1, Name: "alice"}, {ID: 2, Name: "bob"}})
	for _, seed := range [][]byte{good, nil, {0}, {1, 5}, {1, 5, 3, 'a'}, {2, 1, 0}, {255}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		players, err := ParsePlayerList(data)
		if err != nil {
			return
		}
		// A list that parses re-encodes to the bytes it was parsed from
		// (anything after the last entry is ignored).
		enc, err := PlayerListMessage(players)
		if err != nil {
			t.Fatalf("re-encoding a parsed list failed: %v", err)
		}
		if !bytes.HasPrefix(data, enc) {
			t.Fatalf("re-encoded list %x is not a prefix of the input %x", enc, data)
		}
	})
}

func FuzzParseChatRelay(f *testing.F) {
	for _, seed := range [][]byte{ChatRelayMessage("Link", "hello"), ChatRelayMessage("", ""), nil, {5, 'a'}, {200}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		name, text, err := ParseChatRelay(data)
		if err != nil {
			return
		}
		if got := ChatRelayMessage(name, text); !bytes.Equal(got, data) {
			t.Fatalf("round trip = %x, want %x", got, data)
		}
	})
}

func FuzzDeserializePose(f *testing.F) {
	for _, seed := range [][]byte{
		SerializePose(42, make([]byte, 42*48), make([]byte, 16)),
		SerializePose(1, make([]byte, 48), nil),
		nil, {0, 0, 0, 0}, {0, 1, 0, 0}, {0xFF, 0xFF, 0, 0}, {0, MaxPoseJoints + 1, 0, 0},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		joints, matrices, face, err := DeserializePose(data)
		if err != nil {
			return
		}
		if joints < 1 || joints > MaxPoseJoints || len(matrices) != joints*48 || len(face) > MaxFaceBytes {
			t.Fatalf("accepted joints=%d matrices=%d face=%d", joints, len(matrices), len(face))
		}
		// Re-serializing reproduces the payload, except the pad bytes
		// [2:4], which a sender always zeroes.
		got := SerializePose(joints, matrices, face)
		if got == nil || !bytes.Equal(got[:2], data[:2]) || !bytes.Equal(got[4:], data[4:]) {
			t.Fatalf("round trip = %x, want %x", got, data)
		}
	})
}

func FuzzParsePoseRelayMessage(f *testing.F) {
	for _, seed := range [][]byte{
		PoseRelayMessage(3, 42, make([]byte, 42*48), make([]byte, 16)),
		nil, {3}, {3, 0, 1, 0, 0}, {3, 0xFF, 0xFF, 0, 0},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		id, joints, matrices, face, err := ParsePoseRelayMessage(data)
		if err != nil {
			return
		}
		relay := PoseRelayMessage(id, joints, matrices, face)
		if relay == nil || relay[0] != data[0] || !bytes.Equal(relay[1:3], data[1:3]) || !bytes.Equal(relay[5:], data[5:]) {
			t.Fatalf("round trip = %x, want %x", relay, data)
		}
	})
}

func FuzzParsePositionMessage(f *testing.F) {
	pos := &PlayerPosition{PosX: -1.5, PosY: 200, PosZ: 3e6, RotX: -1, RotY: 0x4000, RotZ: 7}
	for _, seed := range [][]byte{PositionMessage(9, pos), nil, {9}, make([]byte, 18), make([]byte, 40)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		id, p := ParsePositionMessage(data)
		if p == nil {
			if len(data) >= 19 {
				t.Fatalf("rejected a %d-byte position message", len(data))
			}
			return
		}
		if len(data) < 19 {
			t.Fatalf("accepted a %d-byte position message", len(data))
		}
		if got := PositionMessage(id, p); !bytes.Equal(got, data[:19]) {
			t.Fatalf("round trip = %x, want %x", got, data[:19])
		}
	})
}

// FuzzCheckWelcome covers ParseWelcome, WelcomeProtocolVersion and the
// client's handshake verdict on whatever first frame a server sends.
func FuzzCheckWelcome(f *testing.F) {
	f.Add(MsgWelcome, WelcomeMessage(3, 99))
	f.Add(MsgWelcome, WelcomeMessage(0, 99))
	f.Add(MsgWelcome, []byte{3})
	f.Add(MsgWelcome, WelcomeMessage(3, 99)[:9])
	f.Add(MsgWelcome, []byte{})
	f.Add(MsgReject, RejectMessage(RejectServerFull, "server full"))
	f.Add(MsgReject, []byte{})
	f.Add(MsgPose, []byte{1})
	f.Fuzz(func(t *testing.T, msgType byte, data []byte) {
		id, _, epochOK := ParseWelcome(data)
		version, versionOK := WelcomeProtocolVersion(data)
		if epochOK != (len(data) >= 9) || versionOK != (len(data) >= 11) {
			t.Fatalf("len %d: epochOK=%v versionOK=%v", len(data), epochOK, versionOK)
		}
		if len(data) > 0 && id != data[0] {
			t.Fatalf("id = %d, want Data[0] = %d", id, data[0])
		}
		err := checkWelcome(&Message{Type: msgType, Data: data})
		if err == nil && (msgType != MsgWelcome || !versionOK || version != ProtocolVersion || id == 0) {
			t.Fatalf("accepted type %q data %x", msgType, data)
		}
		if msgType == MsgReject && !errors.Is(err, ErrFatal) {
			t.Fatalf("a reject must be fatal, got %v", err)
		}
	})
}

func FuzzParseHello(f *testing.F) {
	for _, seed := range [][]byte{HelloMessage(ProtocolVersion), nil, {1}, {0, 1, 'x'}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		v, err := ParseHello(data)
		if (err == nil) != (len(data) >= 2) {
			t.Fatalf("len %d: err = %v", len(data), err)
		}
		if err == nil && !bytes.Equal(HelloMessage(v), data[:2]) {
			t.Fatalf("round trip = %x, want %x", HelloMessage(v), data[:2])
		}
	})
}

func FuzzParseReject(f *testing.F) {
	for _, seed := range [][]byte{RejectMessage(RejectVersion, "upgrade"), nil, {RejectServerFull}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		code, reason, err := ParseReject(data)
		if err != nil {
			if len(data) != 0 {
				t.Fatalf("rejected a %d-byte reject: %v", len(data), err)
			}
			return
		}
		if len(data) <= MaxPayload && !bytes.Equal(RejectMessage(code, reason), data) {
			t.Fatalf("round trip = %x, want %x", RejectMessage(code, reason), data)
		}
	})
}

// FuzzReadMessage feeds arbitrary bytes to the frame reader: every frame it
// returns must re-frame to exactly the bytes it consumed.
func FuzzReadMessage(f *testing.F) {
	f.Add(seedStream())
	f.Add([]byte{MsgChat, 0xFF, 0xFF, 'x'})
	f.Add([]byte{MsgChat})
	f.Fuzz(func(t *testing.T, stream []byte) {
		r := bytes.NewReader(stream)
		consumed := 0
		for {
			msg, err := ReadMessage(r)
			if err != nil {
				return
			}
			want := frame(msg.Type, msg.Data)
			if !bytes.HasPrefix(stream[consumed:], want) {
				t.Fatalf("frame at %d re-frames to %x, not the bytes it was read from", consumed, want)
			}
			consumed += len(want)
		}
	})
}

// FuzzClientReadLoop runs the client's read loop over an arbitrary server
// stream. It must never reach its panic safety net, and every remote it keeps
// must hold a pose within the protocol limits.
func FuzzClientReadLoop(f *testing.F) {
	f.Add(seedStream())
	f.Add(frame(MsgPlayerList, []byte{1, 5}))
	f.Add(frame(MsgPose, []byte{3, 0xFF, 0xFF, 0, 0}))
	f.Fuzz(func(t *testing.T, stream []byte) {
		sc := newScriptConn(stream)
		c := NewClient("me")
		var recovered string
		c.OnLog = func(s string) {
			if strings.Contains(s, "internal error") {
				recovered = s
			}
		}
		c.OnChat = func(string, string) {}
		c.conn = sc
		c.connected.Store(true)
		c.readLoop(sc)
		if recovered != "" {
			t.Fatalf("read loop panicked: %s", recovered)
		}
		for _, rp := range c.GetRemotePlayers() {
			if rp.PoseMatrices != nil && (rp.PoseJoints < 1 || rp.PoseJoints > MaxPoseJoints || len(rp.PoseMatrices) != rp.PoseJoints*48) {
				t.Fatalf("remote %d kept a bad pose: joints=%d bytes=%d", rp.ID, rp.PoseJoints, len(rp.PoseMatrices))
			}
			if len(rp.FaceState) > MaxFaceBytes {
				t.Fatalf("remote %d kept a %d-byte face trailer", rp.ID, len(rp.FaceState))
			}
		}
	})
}

// FuzzServerHandlePlayer runs the server's whole per-connection path (hello,
// name check, ID, ingress caps, relays, teardown) over a join with an
// arbitrary name followed by an arbitrary client stream. Any panic fails the
// fuzz; the player must be torn down when the stream ends.
func FuzzServerHandlePlayer(f *testing.F) {
	pose := SerializePose(42, make([]byte, 42*48), make([]byte, 16))
	var stream []byte
	stream = append(stream, frame(MsgPosition, (&PlayerPosition{PosX: 1}).Serialize())...)
	stream = append(stream, frame(MsgPose, pose)...)
	stream = append(stream, frame(MsgChat, []byte("hi"))...)
	stream = append(stream, frame(MsgPing, PingPayload(1))...)
	stream = append(stream, frame(MsgPose, []byte{0xFF, 0xFF, 0, 0})...)
	f.Add("alice", stream)
	f.Add("", stream)
	f.Add("bad\nname", stream)
	f.Add("alice", []byte{MsgPose, 0, 9, 0})
	f.Fuzz(func(t *testing.T, name string, stream []byte) {
		if len(name) > MaxPayload {
			return // can't be framed as a join at all
		}
		in := append(frame(MsgJoin, []byte(name)), frame(MsgHello, HelloMessage(ProtocolVersion))...)
		in = append(in, stream...)
		srv := NewServer(0)
		srv.handlePlayer(newScriptConn(in))
		if n := srv.PlayerCount(); n != 0 {
			t.Fatalf("PlayerCount = %d after the stream ended, want 0", n)
		}
	})
}
