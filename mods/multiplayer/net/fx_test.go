package network

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"
)

func sampleFx() FxEvent {
	return FxEvent{Seq: 7, Kind: FxKindArrow, Variant: 3, Room: -1, Stage: "sea", ID: 0x01020304,
		Pos: [3]float32{100, -50.5, 2e5}, SpeedF: 12, SpeedY: -3, Gravity: -2.9, Angle: [3]int16{-0x1000, 0x7FFF, 1}, Timer: 150}
}

// TestFxRoundTrip: an event encodes to FxWireSize bytes and parses back;
// the relay adds the sender's ID in front.
func TestFxRoundTrip(t *testing.T) {
	e := sampleFx()
	b := e.Bytes()
	if len(b) != FxWireSize || b[0] != FxWireVersion {
		t.Fatalf("payload %d bytes, version %d", len(b), b[0])
	}
	got, err := ParseFx(b)
	if err != nil || got != e {
		t.Fatalf("ParseFx = %+v, %v; want %+v", got, err, e)
	}
	// A later version's tail is ignored.
	if got, err := ParseFx(append(b, 1, 2, 3)); err != nil || got != e {
		t.Errorf("with a tail: %+v, %v", got, err)
	}
	from, got, err := ParseFxRelay(FxRelayMessage(9, b))
	if err != nil || from != 9 || got != e {
		t.Errorf("relay: %d %+v %v", from, got, err)
	}
	// An eight-byte stage name fills its field, unterminated.
	e.Stage = "Abesso00"
	if got, err := ParseFx(e.Bytes()); err != nil || got.Stage != "Abesso00" {
		t.Errorf("8-byte stage: %q, %v", got.Stage, err)
	}
}

// TestParseFxRefuses: every check ParseFx makes refuses with ErrFxEvent.
func TestParseFxRefuses(t *testing.T) {
	nan := float32(math.NaN())
	inf := float32(math.Inf(1))
	cases := map[string]func(e *FxEvent, b []byte) []byte{
		"short":           func(e *FxEvent, b []byte) []byte { return b[:FxWireSize-1] },
		"version":         func(e *FxEvent, b []byte) []byte { b[0] = 2; return b },
		"kind 0":          func(e *FxEvent, b []byte) []byte { e.Kind = 0; return e.Bytes() },
		"kind 7":          func(e *FxEvent, b []byte) []byte { e.Kind = FxKindMax + 1; return e.Bytes() },
		"arrow type 4":    func(e *FxEvent, b []byte) []byte { e.Variant = FxArrowMax + 1; return e.Bytes() },
		"bomb variant":    func(e *FxEvent, b []byte) []byte { e.Kind, e.Variant = 1, 1; return e.Bytes() },
		"no stage":        func(e *FxEvent, b []byte) []byte { e.Stage = ""; return e.Bytes() },
		"stage byte":      func(e *FxEvent, b []byte) []byte { b[9] = 0x01; return b },
		"stage padding":   func(e *FxEvent, b []byte) []byte { b[8+5] = 0; b[8+6] = 'x'; return b },
		"nan x":           func(e *FxEvent, b []byte) []byte { e.Pos[0] = nan; return e.Bytes() },
		"inf z":           func(e *FxEvent, b []byte) []byte { e.Pos[2] = inf; return e.Bytes() },
		"far":             func(e *FxEvent, b []byte) []byte { e.Pos[1] = FxMaxCoord; return e.Bytes() },
		"speed":           func(e *FxEvent, b []byte) []byte { e.SpeedF = -FxMaxSpeed; return e.Bytes() },
		"gravity":         func(e *FxEvent, b []byte) []byte { e.Gravity = FxMaxGravity; return e.Bytes() },
		"nan speed y":     func(e *FxEvent, b []byte) []byte { e.SpeedY = nan; return e.Bytes() },
		"empty relay":     nil,
		"relay of a junk": nil,
	}
	for name, mk := range cases {
		var err error
		switch name {
		case "empty relay":
			_, _, err = ParseFxRelay(nil)
		case "relay of a junk":
			_, _, err = ParseFxRelay([]byte{1, 2, 3})
		default:
			e := sampleFx()
			e.Stage = "M_NewD2" // 7 bytes, so the padding case has a byte to break
			_, err = ParseFx(mk(&e, e.Bytes()))
		}
		if !errors.Is(err, ErrFxEvent) {
			t.Errorf("%s: err %v, want ErrFxEvent", name, err)
		}
	}
}

// TestFxBucket: a burst of FxBurst, then FxRatePerSec per second.
func TestFxBucket(t *testing.T) {
	var b fxBucket
	now := time.Unix(1000, 0)
	for i := 0; i < FxBurst; i++ {
		if !b.take(now) {
			t.Fatalf("burst refused at %d", i)
		}
	}
	if b.take(now) {
		t.Fatal("past the burst taken")
	}
	now = now.Add(time.Second / FxRatePerSec)
	if !b.take(now) || b.take(now) {
		t.Fatal("one refill tick should allow exactly one")
	}
	now = now.Add(time.Hour)
	n := 0
	for b.take(now) {
		n++
	}
	if n != FxBurst {
		t.Errorf("after an hour %d taken, want the burst %d", n, FxBurst)
	}
}

type fxRecorder struct {
	mu  sync.Mutex
	got []FxEvent
	ids []byte
}

func (r *fxRecorder) fn() func(byte, FxEvent) {
	return func(id byte, e FxEvent) {
		r.mu.Lock()
		r.got = append(r.got, e)
		r.ids = append(r.ids, id)
		r.mu.Unlock()
	}
}

func (r *fxRecorder) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.got)
}

// TestFxRelayReachesOthers: an event from one player reaches the other
// players (with the sender's ID), never the sender; a malformed one and
// events past the rate limit are dropped by the server.
func TestFxRelayReachesOthers(t *testing.T) {
	defer restoreNetTimers(saveNetTimers())
	heartbeatInterval = 20 * time.Millisecond
	serverReadTimeout = 2 * time.Second
	clientReadTimeout = 2 * time.Second

	srv, addr := mustListen(t)
	defer srv.Stop()

	var aliceRec, bobRec fxRecorder
	alice := NewClient("alice")
	alice.OnFx = aliceRec.fn()
	if err := alice.Connect(addr); err != nil {
		t.Fatalf("alice connect: %v", err)
	}
	defer alice.Disconnect()
	bob := NewClient("bob")
	bob.OnFx = bobRec.fn()
	if err := bob.Connect(addr); err != nil {
		t.Fatalf("bob connect: %v", err)
	}
	defer bob.Disconnect()
	if !waitFor(2*time.Second, func() bool { return srv.PlayerCount() == 2 }) {
		t.Fatalf("server never saw 2 connections")
	}

	e := sampleFx()
	if err := alice.SendFx(&e); err != nil {
		t.Fatal(err)
	}
	if err := alice.send(MsgFx, []byte{FxWireVersion, 0}); err != nil { // malformed: dropped
		t.Fatal(err)
	}
	if !waitFor(2*time.Second, func() bool { return bobRec.len() >= 1 }) {
		t.Fatal("bob never received alice's event")
	}
	bobRec.mu.Lock()
	if bobRec.got[0] != e || bobRec.ids[0] != alice.MyID() {
		t.Errorf("bob got %+v from %d, want %+v from %d", bobRec.got[0], bobRec.ids[0], e, alice.MyID())
	}
	bobRec.mu.Unlock()

	// The rest of the burst, then events past it: bob gets the burst only.
	for i := 0; i < FxBurst+10; i++ {
		e.Seq++
		if err := alice.SendFx(&e); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(200 * time.Millisecond)
	if n := bobRec.len(); n < FxBurst || n > FxBurst+3 {
		t.Errorf("bob got %d events, want about the burst %d (rate %d/s)", n, FxBurst, FxRatePerSec)
	}
	if n := aliceRec.len(); n != 0 {
		t.Errorf("the sender received %d of her own events", n)
	}
}
