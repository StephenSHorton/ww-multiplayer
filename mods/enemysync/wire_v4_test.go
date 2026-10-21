package enemysync

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// TestWireV4: version 4's sizes (STATE header 52, slot 64, HIT 56, ACK
// 20, up to 16 slots), each new field at its design offset
// (enemy-mirror-general.json "wire"), byte for byte; a version-3 payload
// refused; the handle carried by HIT and ACK; the dead words and their
// DeadMask kept together; a ChuChu's m316.z in the attack bytes; the slot
// kind passed through for the client to check.
func TestWireV4(t *testing.T) {
	if WireVersion != 4 || StateHeaderLen != 52 || SlotLen != 64 || HitLen != 56 || AckLen != 20 || WireMaxSlots != 16 || MaxSlots != mailbox.EnemyMaxSlots ||
		network.EnemyStateLen(16) != 52+16*64 || PHitLen != 60 {
		t.Fatalf("sizes: ver %d header %d slot %d hit %d ack %d wire max %d max %d", WireVersion, StateHeaderLen, SlotLen, HitLen, AckLen, WireMaxSlots, MaxSlots)
	}
	if OwnerSlotBase != 0xFE00 || NoMirrorHandle != 0xFFFF || HandleNone != 0 || HDKind != 17 || HDKind == HDNoBody {
		t.Error("handle, owner or detail constants moved")
	}

	cc := Slot{SetID: 0xFFFF, Proc: 0x00CF, Pos: [3]float32{1, 2, 3}, AngleY: 0x100, Tilt: -7, Action: 2, Mode: 6,
		MorfFrame: 0.5, MorfRate: 1, Health: 3, Flags: SlotInvuln, Bck: 0x1C, AnmAttr: AnmAttrLoop, AnmSeq: 9, Morf: 3,
		Flags2: CcF2Tg | CcF2Squash | KindF2Light, BlurScale: 12.5,
		WFlags: WFlagEngaged | WFlagTomb, Kind: KindCC, PKey: 0xD0264F5D, Handle: 0x0102, Aux: [4]int16{-0x4000, 1, -1, 0x7FFF}}
	cc.SetCcM316Z(-300)
	st := &State{Flags: StateFlagInScope, Epoch: 1, Seq: 2, HostBeat: 3, HostMS: 4, Stage: "M_Dai", Room: 2,
		HostKindMask: 0b101, KindHash: 0x73A3A496, Slots: []Slot{cc}}
	st.SetDead([DeadWords]uint32{0x00000105, 0x80000000, 0, 1})
	b, err := EncodeState(st)
	if err != nil {
		t.Fatal(err)
	}
	want := unhex(t, ""+
		// header: sub ver flags count, epoch, seq, beat, ms, stage, room, deadMask, hostKindMask, pad
		"01 04 04 01 00000001 00000002 00000003 00000004 4D5F446169000000 02 05 05 00"+
		// dead[4], kindHash
		"00000105 80000000 00000000 00000001 73A3A496"+
		// slot: setID proc pos angle tilt action mode, m316.z (attack/phase), frame rate health flags
		"FFFF 00CF 3F800000 40000000 40400000 0100 FFF9 0002 0006 FED4 3F000000 3F800000 03 04"+
		// bck attr anmSeq morf flags2 blurShape wflags kind blurScale pkey handle pad aux[4]
		"1C 02 09 03 45 00 03 02 41480000 D0264F5D 0102 0000 C000 0001 FFFF 7FFF")
	if !bytes.Equal(b, want) {
		t.Fatalf("STATE\n got % x\nwant % x", b, want)
	}
	got, err := DecodeState(b)
	if err != nil || !reflect.DeepEqual(got, st) {
		t.Fatalf("round trip %v\n got %+v\nwant %+v", err, got, st)
	}
	if got.Slots[0].CcM316Z() != -300 || got.Slots[0].Ident() != (Ident{Proc: 0xCF, SetID: 0xFFFF, PKey: 0xD0264F5D}) {
		t.Errorf("m316.z %d ident %v", got.Slots[0].CcM316Z(), got.Slots[0].Ident())
	}
	if st.DeadMask != 0x05 || !st.IsDead(0) || !st.IsDead(8) || st.IsDead(1) || !st.IsDead(63) || !st.IsDead(96) || st.IsDead(127) || st.IsDead(128) {
		t.Errorf("dead mask 0x%02X / IsDead wrong for %08X", st.DeadMask, st.Dead)
	}
	// The decoder passes a kind it does not know (a newer build's row):
	// the client compares Kind with KindOf(Proc).
	odd := bytes.Clone(b)
	odd[StateHeaderLen+43] = 7
	if g, err := DecodeState(odd); err != nil || g.Slots[0].Kind != 7 || KindOf(g.Slots[0].Proc) != KindCC {
		t.Errorf("an unknown kind byte: %v", err)
	}
	// Sixteen slots fit; seventeen do not; a version-3 STATE is refused.
	st.Slots = make([]Slot, WireMaxSlots)
	if b16, err := EncodeState(st); err != nil || len(b16) != 52+16*64 {
		t.Errorf("16 slots: %d bytes %v", len(b16), err)
	} else if g, err := DecodeState(b16); err != nil || len(g.Slots) != WireMaxSlots {
		t.Errorf("16 slots decode: %v", err)
	}
	v3 := bytes.Clone(b)
	v3[1] = 3
	if _, err := DecodeState(v3); err == nil {
		t.Error("a version-3 STATE decoded")
	}

	// HIT: the handle at [52]; ACK: at [16].
	h := &Hit{Collider: ColliderTgCyl, Stage: "M_Dai", Room: 2, SetID: 0xFFFF, Handle: 0xBEEF, HitSeq: 4}
	hb, err := EncodeHit(h)
	if err != nil || len(hb) != 56 || hb[1] != 4 || hb[52] != 0xBE || hb[53] != 0xEF || hb[54] != 0 || hb[55] != 0 {
		t.Fatalf("HIT % x %v", hb, err)
	}
	if g, err := DecodeHit(hb); err != nil || g.Handle != 0xBEEF || g.SetID != 0xFFFF {
		t.Errorf("HIT decode %+v %v", g, err)
	}
	ab := EncodeAck(&Ack{Client: 1, Result: AckAppliedAt, SetID: 0xFFFF, Handle: 0xBEEF, HitSeq: 4})
	if len(ab) != 20 || ab[1] != 4 || ab[16] != 0xBE || ab[17] != 0xEF || ab[18] != 0 || ab[19] != 0 {
		t.Fatalf("ACK % x", ab)
	}
	if g, err := DecodeAck(ab); err != nil || g.Handle != 0xBEEF {
		t.Errorf("ACK decode %+v %v", g, err)
	}
	for _, bad := range [][]byte{hb[:52], func() []byte { c := bytes.Clone(hb); c[1] = 3; return c }()} {
		if _, err := DecodeHit(bad); err == nil {
			t.Errorf("a v3 HIT decoded: % x", bad)
		}
	}
	for _, bad := range [][]byte{ab[:16], func() []byte { c := bytes.Clone(ab); c[1] = 3; return c }()} {
		if _, err := DecodeAck(bad); err == nil {
			t.Errorf("a v3 ACK decoded: % x", bad)
		}
	}
	// PHIT: the attacker's handle in the setID field, NoMirrorHandle for
	// none; a version-3 PHIT is refused.
	pb, err := EncodePHit(&PHit{Client: 1, Stage: "M_Dai", SetID: NoMirrorHandle})
	if err != nil || pb[1] != 4 || pb[18] != 0xFF || pb[19] != 0xFF {
		t.Fatalf("PHIT % x %v", pb, err)
	}
	pb[1] = 3
	if _, err := DecodePHit(pb); err == nil {
		t.Error("a v3 PHIT decoded")
	}
}
