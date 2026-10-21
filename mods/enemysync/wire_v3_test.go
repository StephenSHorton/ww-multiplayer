package enemysync

import (
	"bytes"
	"encoding/hex"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// The wire v3 subs, byte for byte against inject/include/wwremote.h's
// header comment (the contract): PHIT 60, PACK 16, PSTATE 24, the HIT's
// new tail at [46..51]. Version 4 keeps their layouts (the version byte
// is 4; PHIT's setID field carries a handle) and grows the HIT to 56.

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestV3Sizes pins the subs and sizes to the server's and the contract's.
func TestV3Sizes(t *testing.T) {
	if SubPHit != 4 || SubPAck != 5 || SubPState != 6 {
		t.Errorf("subs %d %d %d", SubPHit, SubPAck, SubPState)
	}
	if PHitLen != 60 || PAckLen != 16 || PStateLen != 24 || HitLen != 56 {
		t.Errorf("sizes phit %d pack %d pstate %d hit %d", PHitLen, PAckLen, PStateLen, HitLen)
	}
	if network.EnemyHitPlayerIDOff != 48 || network.EnemyPStatePlayerIDOff != 19 || network.EnemyClientIDOff != 2 {
		t.Errorf("stamp offsets hit %d pstate %d client %d", network.EnemyHitPlayerIDOff, network.EnemyPStatePlayerIDOff, network.EnemyClientIDOff)
	}
	// The PHIT flags are the apply record's: Go copies them.
	if PHitHostShield != 1 || PHitHostGuard != 2 || HDNoBody != 16 {
		t.Errorf("phit flags %d %d, HDNoBody %d", PHitHostShield, PHitHostGuard, HDNoBody)
	}
	if PSGuard != 1 || PSCrouchDef != 2 || PSDamageMode != 4 || PSDead != 8 || PSInEvent != 0x10 || PSSwim != 0x20 || PSOnShip != 0x40 || PSLoading != 0x80 {
		t.Error("PS bits moved")
	}
}

// TestPHitGolden: a PHIT's bytes as the contract lays them out.
func TestPHitGolden(t *testing.T) {
	p := &PHit{Client: 7, Flags: PHitHostShield | PHitHostGuard, Epoch: 0x01020304, Stage: "M_Dai", Room: -2, Atp: 4,
		SetID: 0x0102, Seq: 0xABCD, Se: 3, HitMark: 0x0D, AtType: 0x800, Spl: 7,
		RelHit: [3]float32{1, 2, -1}, RVec: [3]float32{0, 0, 0.5}, HostFrame: 0xDEADBEEF}
	b, err := EncodePHit(p)
	if err != nil {
		t.Fatal(err)
	}
	want := unhex(t, "04 04 07 03 01020304 4D5F446169000000 FE 04 0102 ABCD 03 0D 00000800 07 000000"+
		"3F800000 40000000 BF800000 00000000 00000000 3F000000 DEADBEEF")
	if !bytes.Equal(b, want) {
		t.Fatalf("PHIT\n got % x\nwant % x", b, want)
	}
	got, err := DecodePHit(b)
	if err != nil || !reflect.DeepEqual(got, p) {
		t.Errorf("round trip %+v %v", got, err)
	}
	for _, bad := range [][]byte{nil, b[:PHitLen-1], append(bytes.Clone(b), 0),
		func() []byte { c := bytes.Clone(b); c[0] = SubPAck; return c }(),
		func() []byte { c := bytes.Clone(b); c[1] = 2; return c }(),
		func() []byte { c := bytes.Clone(b); copy(c[36:], []byte{0x7F, 0xC0, 0, 0}); return c }(),
		func() []byte { c := bytes.Clone(b); copy(c[52:], []byte{0xFF, 0x80, 0, 0}); return c }(),
	} {
		if _, err := DecodePHit(bad); err == nil {
			t.Errorf("PHIT % x decoded", bad)
		}
	}
	nan := *p
	nan.RVec[2] = float32(math.NaN())
	if _, err := EncodePHit(&nan); err == nil {
		t.Error("NaN rvec encoded")
	}
	long := *p
	long.Stage = "123456789"
	if _, err := EncodePHit(&long); err == nil {
		t.Error("9-byte stage encoded")
	}
}

// TestPAckGolden: a PACK's bytes; the life delta is signed.
func TestPAckGolden(t *testing.T) {
	a := &PAck{Client: 9, Result: PAckGuarded, Seq: 0x1234, Detail: PADIFrames, LifeBefore: 12, ClientFrame: 0xA0B0C0D0, LifeDelta: -4}
	b := EncodePAck(a)
	want := unhex(t, "05 04 09 03 1234 01 0C A0B0C0D0 FFFC 0000")
	if !bytes.Equal(b, want) {
		t.Fatalf("PACK\n got % x\nwant % x", b, want)
	}
	got, err := DecodePAck(b)
	if err != nil || !reflect.DeepEqual(got, a) {
		t.Errorf("round trip %+v %v", got, err)
	}
	for _, bad := range [][]byte{nil, b[:PAckLen-1], append(bytes.Clone(b), 0), func() []byte { c := bytes.Clone(b); c[0] = SubAck; return c }()} {
		if _, err := DecodePAck(bad); err == nil {
			t.Errorf("PACK % x decoded", bad)
		}
	}
}

// TestPStateGolden: a PSTATE's bytes, with the server's two stamps.
func TestPStateGolden(t *testing.T) {
	p := &PState{Flags: PSGuard | PSSwim, Epoch: 5, Stage: "sea", Room: 44, CutType: 8, CutCount: 2, IFrames: -1, ShieldAngleY: 0x4000}
	b, err := EncodePState(p)
	if err != nil {
		t.Fatal(err)
	}
	want := unhex(t, "06 04 00 21 00000005 7365610000000000 2C 08 02 00 FFFF 4000")
	if !bytes.Equal(b, want) {
		t.Fatalf("PSTATE\n got % x\nwant % x", b, want)
	}
	b[network.EnemyClientIDOff], b[network.EnemyPStatePlayerIDOff] = 3, 17
	got, err := DecodePState(b)
	p.Client, p.PlayerID = 3, 17
	if err != nil || !reflect.DeepEqual(got, p) {
		t.Errorf("round trip %+v %v", got, err)
	}
	for _, bad := range [][]byte{nil, b[:PStateLen-1], append(bytes.Clone(b), 0), func() []byte { c := bytes.Clone(b); c[1] = 2; return c }()} {
		if _, err := DecodePState(bad); err == nil {
			t.Errorf("PSTATE % x decoded", bad)
		}
	}
}

// TestHitV3Tail: the HIT's cut type and count at [46]/[47] and the stamped
// player id at [48]; [49..51] stay 0, and so does v4's handle at [52..53]
// when none is set.
func TestHitV3Tail(t *testing.T) {
	h := &Hit{Stage: "M_Dai", SetID: 1, CutType: 0x0B, CutCount: 3, PlayerID: 0}
	b, err := EncodeHit(h)
	if err != nil {
		t.Fatal(err)
	}
	if b[46] != 0x0B || b[47] != 3 || b[48] != 0 || !bytes.Equal(b[49:], []byte{0, 0, 0, 0, 0, 0, 0}) {
		t.Fatalf("tail % x", b[44:])
	}
	b[network.EnemyHitPlayerIDOff] = 5
	got, err := DecodeHit(b)
	if err != nil || got.CutType != 0x0B || got.CutCount != 3 || got.PlayerID != 5 {
		t.Errorf("decode %+v %v", got, err)
	}
}

// TestPAckNames: every PACK result and detail has its own name, matching
// the block's constants.
func TestPAckNames(t *testing.T) {
	results := map[uint8]string{PAckNone: "none", PAckDropped: "dropped", PAckApplied: "applied", PAckGuarded: "guarded",
		PAckNoCross: "no_cross", PAckNoMirror: "no_mirror", PAckLinkAbsent: "link_absent", PAckEarlyOut: "early_out"}
	if len(results) != int(mailbox.RemoteAREarlyOut)+1 {
		t.Fatalf("%d results", len(results))
	}
	for r, want := range results {
		if got := PAckResultName(r); got != want {
			t.Errorf("PAckResultName(%d) = %q, want %q", r, got, want)
		}
	}
	details := map[uint8]string{PADNone: "none", PADIFrames: "iframes", PADGoGate: "go_gate", PADNotInPlay: "not_in_play",
		PADStaleEpoch: "stale_epoch", PADAborted: "aborted"}
	if len(details) != int(mailbox.RemoteADAborted)+1 {
		t.Fatalf("%d details", len(details))
	}
	for d, want := range details {
		if got := PAckDetailName(d); got != want {
			t.Errorf("PAckDetailName(%d) = %q, want %q", d, got, want)
		}
	}
	if PAckResultName(8) != "result_8" || PAckDetailName(6) != "detail_6" {
		t.Error("unknown names")
	}
}

func FuzzDecodePHit(f *testing.F) {
	b, _ := EncodePHit(&PHit{Client: 1, Stage: "sea", Atp: 1, AtType: 0x800})
	f.Add(b)
	f.Add([]byte(nil))
	f.Add(make([]byte, PHitLen))
	f.Fuzz(func(t *testing.T, data []byte) {
		p, err := DecodePHit(data)
		if err != nil {
			return
		}
		enc, err := EncodePHit(p)
		if want := canonical(data, 8, 29, 30, 31); err != nil || !bytes.Equal(enc, want) {
			t.Fatalf("round trip = %x (%v), want %x", enc, err, want)
		}
	})
}

func FuzzDecodePAck(f *testing.F) {
	f.Add(EncodePAck(&PAck{Client: 2, Result: PAckApplied, LifeDelta: -8}))
	f.Add([]byte(nil))
	f.Add(make([]byte, PAckLen))
	f.Fuzz(func(t *testing.T, data []byte) {
		a, err := DecodePAck(data)
		if err != nil {
			return
		}
		if enc, want := EncodePAck(a), canonical(data, -1, 14, 15); !bytes.Equal(enc, want) {
			t.Fatalf("round trip = %x, want %x", enc, want)
		}
	})
}

func FuzzDecodePState(f *testing.F) {
	b, _ := EncodePState(&PState{Flags: PSGuard, Stage: "M_Dai", Room: 1})
	f.Add(b)
	f.Add([]byte(nil))
	f.Add(make([]byte, PStateLen))
	f.Fuzz(func(t *testing.T, data []byte) {
		p, err := DecodePState(data)
		if err != nil {
			return
		}
		// Every byte but the stage's tail past its NUL is a field.
		enc, err := EncodePState(p)
		if want := canonical(data, 8); err != nil || !bytes.Equal(enc, want) {
			t.Fatalf("round trip = %x (%v), want %x", enc, err, want)
		}
	})
}
