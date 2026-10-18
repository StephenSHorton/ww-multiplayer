package enemysync

import (
	"bytes"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
)

func sampleSlot(i int) Slot {
	return Slot{
		SetID: uint16(i), Proc: 0x00BD,
		Pos:    [3]float32{-1234.5 + float32(i), 100.25, 3e5},
		AngleY: int16(-0x4000 + i), Tilt: int16(-3), Action: 20, Mode: -100,
		AttackType: 2, AttackPhase: 1, MorfFrame: 12.5, MorfRate: 1.0,
		Health: -1, Flags: SlotSnap | SlotDying | uint8(i),
	}
}

func sampleState(n int) *State {
	s := &State{Flags: StateFlagInScope | StateFlagAnim, Epoch: 7, Seq: 0xDEADBEEF, HostBeat: 123456, HostMS: 98765,
		Stage: "M_Dai", Room: 1, DeadMask: 0x05}
	for i := range n {
		s.Slots = append(s.Slots, sampleSlot(i))
	}
	return s
}

// TestStateRoundTrip: 0..4 slots encode to the server's size and decode
// to the same State; an empty-slot STATE decodes with nil Slots.
func TestStateRoundTrip(t *testing.T) {
	for n := 0; n <= MaxSlots; n++ {
		s := sampleState(n)
		b, err := EncodeState(s)
		if err != nil {
			t.Fatalf("%d slots: %v", n, err)
		}
		if len(b) != network.EnemyStateLen(n) || b[0] != SubState || b[1] != WireVersion || int(b[3]) != n {
			t.Fatalf("%d slots: %d bytes, header %x", n, len(b), b[:4])
		}
		if why := networkShape(b); why != "" {
			t.Fatalf("%d slots: the server would drop it: %s", n, why)
		}
		got, err := DecodeState(b)
		if err != nil {
			t.Fatalf("%d slots: decode: %v", n, err)
		}
		if !reflect.DeepEqual(got, s) {
			t.Errorf("%d slots: round trip\n got %+v\nwant %+v", n, got, s)
		}
		b2, _ := EncodeState(got)
		if !bytes.Equal(b, b2) {
			t.Errorf("%d slots: re-encode differs", n)
		}
	}
}

// networkShape is the server's ingress check for a host's frame.
func networkShape(b []byte) string {
	// The server's rule is private to package network; mirror it here
	// from its exported sizes.
	if len(b) < network.EnemyStateHeaderLen || int(b[3]) > network.EnemyMaxSlots || len(b) != network.EnemyStateLen(int(b[3])) {
		return "bad STATE shape"
	}
	return ""
}

func TestStateEncodeRefuses(t *testing.T) {
	s := sampleState(5)
	if _, err := EncodeState(s); err == nil {
		t.Error("5 slots encoded")
	}
	s = sampleState(1)
	s.Stage = "toolongname"
	if _, err := EncodeState(s); err == nil {
		t.Error("a 11-byte stage name encoded")
	}
	s = sampleState(1)
	s.Stage = "12345678" // exactly StageLen: allowed, decodes whole
	b, err := EncodeState(s)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := DecodeState(b); got.Stage != "12345678" {
		t.Errorf("8-byte stage = %q", got.Stage)
	}
	for _, bad := range []float32{float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))} {
		s = sampleState(2)
		s.Slots[1].Pos[2] = bad
		if _, err := EncodeState(s); !errors.Is(err, ErrNotFinite) {
			t.Errorf("pos %v: %v", bad, err)
		}
		s = sampleState(1)
		s.Slots[0].MorfRate = bad
		if _, err := EncodeState(s); !errors.Is(err, ErrNotFinite) {
			t.Errorf("rate %v: %v", bad, err)
		}
		h := &Hit{Pos: [3]float32{bad, 0, 0}}
		if _, err := EncodeHit(h); !errors.Is(err, ErrNotFinite) {
			t.Errorf("hit pos %v: %v", bad, err)
		}
	}
}

func TestStateDecodeRefuses(t *testing.T) {
	good, _ := EncodeState(sampleState(2))
	cases := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"one byte", []byte{SubState}},
		{"short header", good[:StateHeaderLen-1]},
		{"wrong version", func() []byte { b := bytes.Clone(good); b[1] = 2; return b }()},
		{"wrong sub", func() []byte { b := bytes.Clone(good); b[0] = SubHit; return b }()},
		{"count over max", func() []byte { b := bytes.Clone(good); b[3] = 5; return b }()},
		{"count under length", func() []byte { b := bytes.Clone(good); b[3] = 1; return b }()},
		{"count over length", func() []byte { b := bytes.Clone(good); b[3] = 3; return b }()},
		{"truncated slot", good[:len(good)-1]},
		{"trailing byte", append(bytes.Clone(good), 0)},
		{"NaN pos", func() []byte {
			b := bytes.Clone(good)
			copy(b[StateHeaderLen+4:], []byte{0x7F, 0xC0, 0, 0})
			return b
		}()},
	}
	for _, c := range cases {
		if _, err := DecodeState(c.data); err == nil {
			t.Errorf("%s: decoded", c.name)
		}
	}
}

func TestHitAckRoundTrip(t *testing.T) {
	h := &Hit{Client: 0, Collider: ColliderHead, Epoch: 9, Stage: "M_Dai", Room: 1, Atp: 2, SetID: 2, HitSeq: 300,
		AtType: 0x80000, Pos: [3]float32{1, -2.5, 3e4}, ClientFrame: 0xFFFFFFFF}
	b, err := EncodeHit(h)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != network.EnemyHitLen || b[0] != SubHit || b[1] != WireVersion {
		t.Fatalf("hit: %d bytes, header %x", len(b), b[:4])
	}
	// The server stamps the client ID.
	b[network.EnemyClientIDOff] = 42
	got, err := DecodeHit(b)
	if err != nil {
		t.Fatal(err)
	}
	h.Client = 42
	if !reflect.DeepEqual(got, h) {
		t.Errorf("hit round trip\n got %+v\nwant %+v", got, h)
	}
	for _, bad := range [][]byte{nil, b[:HitLen-1], append(bytes.Clone(b), 0), func() []byte { c := bytes.Clone(b); c[0] = SubAck; return c }()} {
		if _, err := DecodeHit(bad); err == nil {
			t.Errorf("hit %x decoded", bad)
		}
	}

	a := &Ack{Client: 42, Result: AckShrink, Epoch: 9, SetID: 2, HitSeq: 300, Health: -128}
	ab := EncodeAck(a)
	if len(ab) != network.EnemyAckLen || ab[0] != SubAck || ab[network.EnemyClientIDOff] != 42 {
		t.Fatalf("ack: %d bytes, %x", len(ab), ab)
	}
	ga, err := DecodeAck(ab)
	if err != nil || !reflect.DeepEqual(ga, a) {
		t.Errorf("ack round trip: %+v, %v", ga, err)
	}
	for _, bad := range [][]byte{nil, ab[:AckLen-1], append(bytes.Clone(ab), 0), func() []byte { c := bytes.Clone(ab); c[1] = 0; return c }()} {
		if _, err := DecodeAck(bad); err == nil {
			t.Errorf("ack %x decoded", bad)
		}
	}
}

// TestSlotFlagsMatchBlock: the wire's slot flags are the block's.
func TestSlotFlagsAndSizes(t *testing.T) {
	if SlotSnap != 1 || SlotDying != 2 || SlotInvuln != 4 || SlotDefence != 8 || SlotRagdoll != 0x10 || SlotBareHanded != 0x20 || SlotHidden != 0x40 || SlotRelease != 0x80 {
		t.Error("slot flags moved")
	}
	if StateHeaderLen != 32 || SlotLen != 36 || HitLen != 44 || AckLen != 16 || MaxSlots != 4 {
		t.Errorf("sizes: header %d slot %d hit %d ack %d max %d", StateHeaderLen, SlotLen, HitLen, AckLen, MaxSlots)
	}
}

// --- fuzz, as network's FuzzParsePoseRelayMessage ---------------------------

// canonical zeroes what a decoder ignores (pad bytes, the stage name past
// its NUL), so a decoded payload re-encodes to it.
func canonical(data []byte, stageOff int, pads ...int) []byte {
	b := bytes.Clone(data)
	if stageOff >= 0 && len(b) >= stageOff+StageLen {
		seen := false
		for i := stageOff; i < stageOff+StageLen; i++ {
			if b[i] == 0 {
				seen = true
			}
			if seen {
				b[i] = 0
			}
		}
	}
	for _, p := range pads {
		if p < len(b) {
			b[p] = 0
		}
	}
	return b
}

func FuzzDecodeState(f *testing.F) {
	for n := 0; n <= MaxSlots; n++ {
		b, _ := EncodeState(sampleState(n))
		f.Add(b)
	}
	f.Add([]byte(nil))
	f.Add([]byte{SubState, WireVersion, 0, 9})
	f.Add(make([]byte, StateHeaderLen))
	f.Fuzz(func(t *testing.T, data []byte) {
		s, err := DecodeState(data)
		if err != nil {
			return
		}
		// A STATE that decodes re-encodes to the bytes it came from.
		enc, err := EncodeState(s)
		if err != nil {
			t.Fatalf("re-encode: %v", err)
		}
		if want := canonical(data, 20, 30, 31); !bytes.Equal(enc, want) {
			t.Fatalf("round trip = %x, want %x", enc, want)
		}
	})
}

func FuzzDecodeHit(f *testing.F) {
	b, _ := EncodeHit(&Hit{Stage: "sea", SetID: 1})
	f.Add(b)
	f.Add([]byte(nil))
	f.Add(make([]byte, HitLen))
	f.Fuzz(func(t *testing.T, data []byte) {
		h, err := DecodeHit(data)
		if err != nil {
			return
		}
		enc, err := EncodeHit(h)
		if want := canonical(data, 8, 22, 23); err != nil || !bytes.Equal(enc, want) {
			t.Fatalf("round trip = %x (%v), want %x", enc, err, want)
		}
	})
}

func FuzzDecodeAck(f *testing.F) {
	f.Add(EncodeAck(&Ack{Client: 3, Result: 1}))
	f.Add([]byte(nil))
	f.Add(make([]byte, AckLen))
	f.Fuzz(func(t *testing.T, data []byte) {
		a, err := DecodeAck(data)
		if err != nil {
			return
		}
		if enc, want := EncodeAck(a), canonical(data, -1, 13, 14, 15); !bytes.Equal(enc, want) {
			t.Fatalf("round trip = %x, want %x", enc, want)
		}
	})
}
