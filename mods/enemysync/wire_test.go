package enemysync

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"reflect"
	"strconv"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

func sampleSlot(i int) Slot {
	return Slot{
		SetID: uint16(i), Proc: 0x00BD,
		Pos:    [3]float32{-1234.5 + float32(i), 100.25, 3e5},
		AngleY: int16(-0x4000 + i), Tilt: int16(-3), Action: 20, Mode: -100,
		AttackType: 2, AttackPhase: 1, MorfFrame: 12.5, MorfRate: 1.0,
		Health: -1, Flags: SlotSnap | SlotDying | uint8(i),
		Bck: 0x3E + uint8(i), AnmAttr: AnmAttrLoop, AnmSeq: 0xFE + uint8(i), Morf: 15,
		Flags2: SlotF2HasWeapon | SlotF2BlurOn | uint8(i)<<4, BlurShape: 2, BlurScale: 0.75 + float32(i),
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

// TestStateRoundTrip: 0..4 slots encode to the server's size (48-byte
// slots) and decode to the same State, the animation identity included;
// an empty-slot STATE decodes with nil Slots.
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
		if len(b) != StateHeaderLen+48*n {
			t.Fatalf("%d slots: %d bytes, want %d", n, len(b), StateHeaderLen+48*n)
		}
		if why := networkShape(b); why != "" {
			t.Fatalf("%d slots: the server would drop it: %s", n, why)
		}
		// The v2 tail of each slot at its design offsets (@36..@41 bytes,
		// @42 pad u16, @44 blur_scale).
		for i := range n {
			sl, want := b[StateHeaderLen+SlotLen*i:], s.Slots[i]
			if sl[36] != want.Bck || sl[37] != want.AnmAttr || sl[38] != want.AnmSeq || sl[39] != want.Morf ||
				sl[40] != want.Flags2 || sl[41] != want.BlurShape || sl[42] != 0 || sl[43] != 0 ||
				math.Float32frombits(binary.BigEndian.Uint32(sl[44:])) != want.BlurScale {
				t.Errorf("%d slots: slot %d tail % x", n, i, sl[36:48])
			}
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
		s = sampleState(3)
		s.Slots[2].BlurScale = bad
		if _, err := EncodeState(s); !errors.Is(err, ErrNotFinite) {
			t.Errorf("blur scale %v: %v", bad, err)
		}
		h := &Hit{Pos: [3]float32{bad, 0, 0}}
		if _, err := EncodeHit(h); !errors.Is(err, ErrNotFinite) {
			t.Errorf("hit pos %v: %v", bad, err)
		}
	}
}

func TestStateDecodeRefuses(t *testing.T) {
	good, _ := EncodeState(sampleState(2))
	// v1Sized is good's header over count 36-byte (version 1) slots.
	v1Sized := func(ver byte, count int) []byte {
		b := make([]byte, StateHeaderLen+36*count)
		copy(b, good[:StateHeaderLen])
		b[1], b[3] = ver, byte(count)
		return b
	}
	cases := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"one byte", []byte{SubState}},
		{"short header", good[:StateHeaderLen-1]},
		{"wrong version", func() []byte { b := bytes.Clone(good); b[1] = 3; return b }()},
		{"version 1, 48-byte slots", func() []byte { b := bytes.Clone(good); b[1] = 1; return b }()},
		// A version-1 STATE as an old host builds it.
		{"version 1, 36-byte slots", v1Sized(1, 2)},
		// The v1 slot length under the v2 version byte.
		{"36-byte slots", v1Sized(WireVersion, 2)},
		{"one 36-byte slot", v1Sized(WireVersion, 1)},
		// Four v1 slots are exactly three v2 slots' bytes: the count refuses it.
		{"four 36-byte slots", v1Sized(WireVersion, 4)},
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
		{"Inf blur scale", func() []byte {
			b := bytes.Clone(good)
			copy(b[StateHeaderLen+SlotLen+44:], []byte{0x7F, 0x80, 0, 0})
			return b
		}()},
	}
	for _, c := range cases {
		if _, err := DecodeState(c.data); err == nil {
			t.Errorf("%s: decoded", c.name)
		}
	}
}

// TestHitAckRoundTrip: a 48-byte HIT carries the sword object's
// Se/HitMark/Spl/AtSPrm at their design offsets, a 16-byte ACK a result
// with its detail, frames and sets; both refuse a version-1 or v1-sized
// payload.
func TestHitAckRoundTrip(t *testing.T) {
	h := &Hit{Client: 0, Collider: ColliderHead, Epoch: 9, Stage: "M_Dai", Room: 1, Atp: 2, SetID: 2, HitSeq: 300,
		AtType: 0x80000, Pos: [3]float32{1, -2.5, 3e4}, ClientFrame: 0xFFFFFFFF,
		Se: 0x11, HitMark: 0x22, Spl: 1, AtSPrm: 0x02}
	b, err := EncodeHit(h)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != network.EnemyHitLen || b[0] != SubHit || b[1] != WireVersion {
		t.Fatalf("hit: %d bytes, header %x", len(b), b[:4])
	}
	if b[22] != 0x11 || b[23] != 0x22 || b[44] != 1 || b[45] != 0x02 || b[46] != 0 || b[47] != 0 {
		t.Errorf("hit v2 bytes: @22 % x, @44 % x", b[22:24], b[44:48])
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
	for _, bad := range [][]byte{
		nil, b[:HitLen-1], append(bytes.Clone(b), 0), func() []byte { c := bytes.Clone(b); c[0] = SubAck; return c }(),
		b[:44], // a v1-sized HIT
		func() []byte { c := bytes.Clone(b); c[1] = 1; return c }(),
	} {
		if _, err := DecodeHit(bad); err == nil {
			t.Errorf("hit %x decoded", bad)
		}
	}

	for _, a := range []*Ack{
		{Client: 42, Result: AckShrink, Epoch: 9, SetID: 2, HitSeq: 300, Health: -128, Detail: HDFallbackShrink, Frames: 3, Sets: 2},
		{Client: 7, Result: AckParried, Epoch: 0xFFFFFFFF, SetID: 0xFFFF, HitSeq: 1, Health: 5, Detail: HDNone, Frames: 255, Sets: 1},
	} {
		ab := EncodeAck(a)
		if len(ab) != network.EnemyAckLen || ab[0] != SubAck || ab[1] != WireVersion || ab[network.EnemyClientIDOff] != a.Client || ab[3] != a.Result {
			t.Fatalf("ack: %d bytes, %x", len(ab), ab)
		}
		if ab[13] != a.Detail || ab[14] != a.Frames || ab[15] != a.Sets {
			t.Errorf("ack @13 % x, want %d %d %d", ab[13:], a.Detail, a.Frames, a.Sets)
		}
		ga, err := DecodeAck(ab)
		if err != nil || !reflect.DeepEqual(ga, a) {
			t.Errorf("ack round trip: %+v, %v", ga, err)
		}
	}
	ab := EncodeAck(&Ack{Client: 42, Result: AckParried, Detail: HDNone, Frames: 1, Sets: 1})
	for _, bad := range [][]byte{nil, ab[:AckLen-1], append(bytes.Clone(ab), 0), func() []byte { c := bytes.Clone(ab); c[1] = 1; return c }()} {
		if _, err := DecodeAck(bad); err == nil {
			t.Errorf("ack %x decoded", bad)
		}
	}
}

// TestSlotFlagsAndSizes: the wire's slot flags, flags2, ACK results and
// details are the block's (wwapi/mailbox), and the sizes are version 2's.
func TestSlotFlagsAndSizes(t *testing.T) {
	if SlotSnap != 1 || SlotDying != 2 || SlotInvuln != 4 || SlotDefence != 8 || SlotRagdoll != 0x10 || SlotBareHanded != 0x20 || SlotHidden != 0x40 || SlotRelease != 0x80 {
		t.Error("slot flags moved")
	}
	if StateHeaderLen != 32 || SlotLen != 48 || HitLen != 48 || AckLen != 16 || MaxSlots != 4 || WireVersion != 2 {
		t.Errorf("sizes: header %d slot %d hit %d ack %d max %d ver %d", StateHeaderLen, SlotLen, HitLen, AckLen, MaxSlots, WireVersion)
	}
	pins := []struct {
		name      string
		wire, blk uint8
	}{
		{"SlotSnap", SlotSnap, mailbox.EnemySFSnap},
		{"SlotDying", SlotDying, mailbox.EnemySFDying},
		{"SlotInvuln", SlotInvuln, mailbox.EnemySFInvuln},
		{"SlotDefence", SlotDefence, mailbox.EnemySFDefence},
		{"SlotRagdoll", SlotRagdoll, mailbox.EnemySFRagdoll},
		{"SlotBareHanded", SlotBareHanded, mailbox.EnemySFBareHanded},
		{"SlotHidden", SlotHidden, mailbox.EnemySFHidden},
		{"SlotRelease", SlotRelease, mailbox.EnemySFRelease},
		{"SlotF2HasWeapon", SlotF2HasWeapon, mailbox.EnemySF2HasWeapon},
		{"SlotF2PlaySkip", SlotF2PlaySkip, mailbox.EnemySF2PlaySkip},
		{"SlotF2BlurOn", SlotF2BlurOn, mailbox.EnemySF2BlurOn},
		{"SlotF2AnmGate", SlotF2AnmGate, mailbox.EnemySF2AnmGate},
		{"AckDropped", AckDropped, mailbox.EnemyHRDropped},
		{"AckAppliedAt", AckAppliedAt, mailbox.EnemyHRAppliedAt},
		{"AckNoActor", AckNoActor, mailbox.EnemyHRNoActor},
		{"AckShrink", AckShrink, mailbox.EnemyHRShrink},
		{"AckParried", AckParried, mailbox.EnemyHRParried},
		{"HDNone", HDNone, mailbox.EnemyHDNone},
		{"HDNoCross", HDNoCross, mailbox.EnemyHDNoCross},
		{"HDInvuln", HDInvuln, mailbox.EnemyHDInvuln},
		{"HDRagdoll", HDRagdoll, mailbox.EnemyHDRagdoll},
		{"HDDying", HDDying, mailbox.EnemyHDDying},
		{"HDNoLink", HDNoLink, mailbox.EnemyHDNoLink},
		{"HDNoSphere", HDNoSphere, mailbox.EnemyHDNoSphere},
		{"HDWrongObj", HDWrongObj, mailbox.EnemyHDWrongObj},
		{"HDAborted", HDAborted, mailbox.EnemyHDAborted},
		{"HDDefence", HDDefence, mailbox.EnemyHDDefence},
		{"HDFallbackShrink", HDFallbackShrink, mailbox.EnemyHDFallbackShrink},
		{"HDGoGate", HDGoGate, mailbox.EnemyHDGoGate},
		{"HDRate", HDRate, mailbox.EnemyHDRate},
		{"HDBusy", HDBusy, mailbox.EnemyHDBusy},
		{"HDGoFlags", HDGoFlags, mailbox.EnemyHDGoFlags},
		{"HDDemo", HDDemo, mailbox.EnemyHDDemo},
	}
	for _, p := range pins {
		if p.wire != p.blk {
			t.Errorf("%s = %d, the block's is %d", p.name, p.wire, p.blk)
		}
	}
	if AckParried != 4 {
		t.Errorf("AckParried = %d", AckParried)
	}
}

// TestResultDetailNames: every ACK result and detail constant has its own
// name, and an unknown one prints its number.
func TestResultDetailNames(t *testing.T) {
	results := map[uint8]string{AckDropped: "dropped", AckAppliedAt: "applied_at", AckNoActor: "no_actor", AckShrink: "shrink", AckParried: "parried"}
	for r, want := range results {
		if got := ResultName(r); got != want {
			t.Errorf("ResultName(%d) = %q, want %q", r, got, want)
		}
	}
	if got := ResultName(AckParried + 1); got != "result_5" {
		t.Errorf("ResultName(5) = %q", got)
	}
	details := map[uint8]string{
		HDNone: "none", HDNoCross: "no_cross", HDInvuln: "invuln", HDRagdoll: "ragdoll", HDDying: "dying",
		HDNoLink: "no_link", HDNoSphere: "no_sphere", HDWrongObj: "wrong_obj", HDAborted: "aborted",
		HDDefence: "defence", HDFallbackShrink: "fallback_shrink", HDGoGate: "go_gate", HDRate: "rate",
		HDBusy: "busy", HDGoFlags: "go_flags", HDDemo: "demo",
	}
	if len(details) != int(HDDemo)+1 {
		t.Fatalf("%d details listed, HDDemo is %d", len(details), HDDemo)
	}
	for d, want := range details {
		if got := DetailName(d); got != want {
			t.Errorf("DetailName(%d) = %q, want %q", d, got, want)
		}
	}
	for _, d := range []uint8{HDDemo + 1, 0xFF} {
		if got, want := DetailName(d), "detail_"+strconv.Itoa(int(d)); got != want {
			t.Errorf("DetailName(%d) = %q, want %q", d, got, want)
		}
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
		// A STATE that decodes re-encodes to the bytes it came from, less
		// the header's pad (@30) and each slot's (@42).
		enc, err := EncodeState(s)
		if err != nil {
			t.Fatalf("re-encode: %v", err)
		}
		pads := []int{30, 31}
		for i := range s.Slots {
			pads = append(pads, StateHeaderLen+SlotLen*i+42, StateHeaderLen+SlotLen*i+43)
		}
		if want := canonical(data, 20, pads...); !bytes.Equal(enc, want) {
			t.Fatalf("round trip = %x, want %x", enc, want)
		}
	})
}

func FuzzDecodeHit(f *testing.F) {
	b, _ := EncodeHit(&Hit{Stage: "sea", SetID: 1, Se: 3, HitMark: 1, Spl: 1, AtSPrm: 2})
	f.Add(b)
	f.Add([]byte(nil))
	f.Add(make([]byte, HitLen))
	f.Fuzz(func(t *testing.T, data []byte) {
		h, err := DecodeHit(data)
		if err != nil {
			return
		}
		enc, err := EncodeHit(h)
		if want := canonical(data, 8, 46, 47); err != nil || !bytes.Equal(enc, want) {
			t.Fatalf("round trip = %x (%v), want %x", enc, err, want)
		}
	})
}

func FuzzDecodeAck(f *testing.F) {
	f.Add(EncodeAck(&Ack{Client: 3, Result: 1}))
	f.Add(EncodeAck(&Ack{Client: 3, Result: AckParried, Frames: 1, Sets: 1}))
	f.Add([]byte(nil))
	f.Add(make([]byte, AckLen))
	f.Fuzz(func(t *testing.T, data []byte) {
		a, err := DecodeAck(data)
		if err != nil {
			return
		}
		// Every byte of a version-2 ACK is a field: no pads.
		if enc := EncodeAck(a); !bytes.Equal(enc, data) {
			t.Fatalf("round trip = %x, want %x", enc, data)
		}
	})
}
