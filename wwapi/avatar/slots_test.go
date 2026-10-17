package avatar

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// TestSlotOffsets checks the per-slot mailbox offsets against the
// generated layout (wwapi/mailbox/layout_gen.go, from
// inject/include/mailbox.h; its freshness test is wwapi/mailbox's
// TestMailboxLayout).
func TestSlotOffsets(t *testing.T) {
	for slot := 0; slot < mailbox.MailboxPoseSlotCap; slot++ {
		for _, c := range []struct {
			name      string
			got, want uint32
		}{
			{"OffPoseBufPtr", OffPoseBufPtr(slot), mailbox.OffPoseBufPtrs + 4*uint32(slot)},
			{"OffPoseJointCount", OffPoseJointCount(slot), mailbox.OffPoseJointCounts + 2*uint32(slot)},
			{"OffPoseBufState", OffPoseBufState(slot), mailbox.OffPoseBufStates + uint32(slot)},
			{"OffPoseSeq", OffPoseSeq(slot), mailbox.OffPoseSeqs + uint32(slot)},
			{"OffFaceState", OffFaceState(slot), mailbox.OffFaceState + mailbox.FaceStateSize*uint32(slot)},
			{"OffFaceSeq", OffFaceSeq(slot), mailbox.OffFaceSeqs + uint32(slot)},
		} {
			if c.got != c.want {
				t.Errorf("%s(%d) = +0x%X, want +0x%X", c.name, slot, c.got, c.want)
			}
		}
	}
	for slot := 0; slot < TunicSlots; slot++ {
		if got, want := OffTunicBuf(slot), mailbox.OffTunicBufs+4*uint32(slot); got != want {
			t.Errorf("OffTunicBuf(%d) = +0x%X, want +0x%X", slot, got, want)
		}
	}
	if OffTunicBuf(TunicSlots) != mailbox.OffTunicBufSize {
		t.Errorf("tunic_bufs holds %d slots, Go uses %d", (mailbox.OffTunicBufSize-mailbox.OffTunicBufs)/4, TunicSlots)
	}
	if Cap > mailbox.MailboxPoseSlotCap {
		t.Errorf("Cap %d > MAILBOX_POSE_SLOT_CAP %d", Cap, mailbox.MailboxPoseSlotCap)
	}
}

func u8(t *testing.T, m *mem.FakeMem, a uint32) byte {
	t.Helper()
	b, err := m.Read(a, 1)
	if err != nil {
		t.Fatal(err)
	}
	return b[0]
}

// TestClearAndRearm: Clear turns shadow_mode off and zeroes every pose
// seq; Rearm sets pose-feed mode and zeroes one slot's pose and face
// seqs, leaving the other slot alone.
func TestClearAndRearm(t *testing.T) {
	m := mem.NewFakeMem()
	mb := uint32(mailbox.Mailbox)
	m.Write(mb+mailbox.OffShadowMode, []byte{ModePoseFeed})
	for s := 0; s < Cap; s++ {
		m.Write(mb+OffPoseSeq(s), []byte{7})
		m.Write(mb+OffFaceSeq(s), []byte{9})
	}

	Rearm(m, 1)
	if got := u8(t, m, mb+mailbox.OffShadowMode); got != ModePoseFeed {
		t.Errorf("shadow_mode after Rearm = %d, want %d", got, ModePoseFeed)
	}
	if u8(t, m, mb+OffPoseSeq(1)) != 0 || u8(t, m, mb+OffFaceSeq(1)) != 0 {
		t.Error("Rearm left slot 1's seqs set")
	}
	if u8(t, m, mb+OffPoseSeq(0)) != 7 || u8(t, m, mb+OffFaceSeq(0)) != 9 {
		t.Error("Rearm(1) touched slot 0")
	}

	Clear(m)
	if got := u8(t, m, mb+mailbox.OffShadowMode); got != ModeOff {
		t.Errorf("shadow_mode after Clear = %d, want 0", got)
	}
	for s := 0; s < Cap; s++ {
		if u8(t, m, mb+OffPoseSeq(s)) != 0 {
			t.Errorf("Clear left pose_seq[%d] set", s)
		}
	}
}

// TestWritePoseAndFace: a pose goes through the slot's buffer pointer and
// a face into face_state[slot], each bumping its seq (skipping 0).
func TestWritePoseAndFace(t *testing.T) {
	m := mem.NewFakeMem()
	mb := uint32(mailbox.Mailbox)
	const buf = 0x8150_0000
	pose := bytes.Repeat([]byte{0xAB}, JointCount*MtxBytes)
	m.Write(mb+OffPoseSeq(1), []byte{0xFF})
	WritePose(m, 1, buf, pose)
	if got, _ := m.Read(buf, len(pose)); !bytes.Equal(got, pose) {
		t.Error("pose not written at the buffer")
	}
	if got := u8(t, m, mb+OffPoseSeq(1)); got != 1 {
		t.Errorf("pose_seq[1] = %d, want 1 (0xFF wraps past 0)", got)
	}

	face := bytes.Repeat([]byte{0x5A}, mailbox.FaceStateSize)
	WriteFace(m, 0, face)
	WriteFace(m, 0, face)
	if got, _ := m.Read(mb+OffFaceState(0), len(face)); !bytes.Equal(got, face) {
		t.Error("face not written")
	}
	if got := u8(t, m, mb+OffFaceSeq(0)); got != 2 {
		t.Errorf("face_seq[0] = %d, want 2", got)
	}
}

func poseWith(trans [3]float32) []byte {
	b := make([]byte, 2*MtxBytes)
	for j := 0; j < 2; j++ {
		for i := 0; i < 12; i++ {
			binary.BigEndian.PutUint32(b[j*MtxBytes+4*i:], math.Float32bits(float32(i+1)))
		}
		binary.BigEndian.PutUint32(b[j*MtxBytes+mtxOffTransX:], math.Float32bits(trans[0]))
		binary.BigEndian.PutUint32(b[j*MtxBytes+mtxOffTransY:], math.Float32bits(trans[1]))
		binary.BigEndian.PutUint32(b[j*MtxBytes+mtxOffTransZ:], math.Float32bits(trans[2]))
	}
	return b
}

// TestPoseMath: Localize and ApplyAt move only the translation column and
// undo each other; Lerp closes the gap by k and ignores a size mismatch.
func TestPoseMath(t *testing.T) {
	world := poseWith([3]float32{110, 20, -30})
	p := append([]byte(nil), world...)
	Localize(p, 2, 100, 0, -50)
	if !bytes.Equal(p, poseWith([3]float32{10, 20, 20})) {
		t.Fatal("Localize did not subtract the origin from the translations only")
	}
	ApplyAt(p, 2, 100, 0, -50)
	if !bytes.Equal(p, world) {
		t.Fatal("ApplyAt did not undo Localize")
	}

	dst := poseWith([3]float32{0, 0, 0})
	Lerp(dst, poseWith([3]float32{10, -20, 40}), 0.5)
	if !bytes.Equal(dst, poseWith([3]float32{5, -10, 20})) {
		t.Error("Lerp(0.5) did not close half the gap")
	}
	short := append([]byte(nil), dst[:MtxBytes]...)
	Lerp(short, world, 0.5)
	if !bytes.Equal(short, dst[:MtxBytes]) {
		t.Error("Lerp changed dst for a target of another size")
	}
}
