package avatar

import (
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// Cap is the number of pose-feed slots (mini-Links) the runtime drives:
// MAX_REMOTE_LINKS, the first Cap of the mailbox's MAILBOX_POSE_SLOT_CAP
// per-slot arrays. Bumped to 2 on 2026-04-20 after the
// shared-J3DModelData blocker was unblocked (J3DModel create flag
// 0x80000 → 0, so each mini-Link gets a private material DL instead of
// sharing one with every other instance).
const Cap = mailbox.MaxRemoteLinks

// shadow_mode values (mailbox_core.h). ModeOff is the kill switch: nothing
// mirrors or feeds Link #2. ModePoseFeed (5) draws each armed slot's
// mini-Link from its pose_buf. The dev modes 1-4 were deleted in Phase
// E0; the C side draws nothing for them, as for ModeOff.
const (
	ModeOff      byte = 0
	ModePoseFeed byte = 5
)

// Per-slot mailbox offsets (relative to mailbox.Mailbox) of the
// pose-feed fields: pose_buf_ptrs u32, pose_joint_counts u16,
// pose_buf_states u8, pose_seqs u8, face_state FaceState, face_seqs u8.
// The offsets are generated from inject/include/mailbox_core.h into
// wwapi/mailbox/layout_gen.go.
func OffPoseBufPtr(slot int) uint32     { return uint32(mailbox.OffPoseBufPtrs + slot*4) }
func OffPoseJointCount(slot int) uint32 { return uint32(mailbox.OffPoseJointCounts + slot*2) }
func OffPoseBufState(slot int) uint32   { return uint32(mailbox.OffPoseBufStates + slot) }
func OffPoseSeq(slot int) uint32        { return uint32(mailbox.OffPoseSeqs + slot) }
func OffFaceState(slot int) uint32 {
	return uint32(mailbox.OffFaceState + slot*mailbox.FaceStateSize)
}
func OffFaceSeq(slot int) uint32 { return uint32(mailbox.OffFaceSeqs + slot) }

// Writer writes emulated RAM; *emu.Dolphin and every mem.Mem implement
// it. The slot writes below are best-effort, as puppet-sync's always
// were: a Dolphin that closed first takes its mailbox with it.
type Writer interface {
	Write(addr uint32, data []byte) error
}

// SetPoseFeedMode writes shadow_mode = ModePoseFeed.
func SetPoseFeedMode(m Writer) {
	m.Write(mailbox.Mailbox+mailbox.OffShadowMode, []byte{ModePoseFeed})
}

// ClearSlot stops the C side drawing slot: it zeroes the slot's pose_seq
// and face_seq, which C reads as "nothing written yet".
func ClearSlot(m Writer, slot int) {
	m.Write(mailbox.Mailbox+OffPoseSeq(slot), []byte{0})
	m.Write(mailbox.Mailbox+OffFaceSeq(slot), []byte{0})
}

// Rearm re-arms a single pose-feed slot after a reconnect (D3) or a
// sender resume (D4): it re-asserts shadow_mode=5 and zeroes the slot's
// pose_seq + face_seq so the C side stops rendering whatever stale pose
// it was holding. Mirrors the seq-zeroing half of Clear plus the re-arm
// intent of puppet-sync's armPoseSlot. The CALLER must also invalidate
// its cached pose_buf_ptr (and armed-epoch) for the slot so it re-polls
// — this function only touches the mailbox, not the in-process cache.
// (It was resetPuppetSyncMailbox in package main.)
func Rearm(m Writer, slot int) {
	SetPoseFeedMode(m)
	ClearSlot(m, slot)
}

// Clear resets the mailbox so the next Dolphin frame stops rendering
// the remote Links. Writes shadow_mode = 0 (the explicit kill switch)
// and zeros every pose_seqs[slot] so even if shadow_mode is flipped
// back to 5 later, nothing renders until a fresh pose arrives. All
// writes are best-effort: if Dolphin closed first, Write fails silently,
// which is the correct behavior (the mailbox is gone with the process).
// (It was the body of clearMultiplayerState in package main.)
func Clear(m Writer) {
	m.Write(mailbox.Mailbox+mailbox.OffShadowMode, []byte{ModeOff})
	for i := 0; i < Cap; i++ {
		m.Write(mailbox.Mailbox+OffPoseSeq(i), []byte{0})
	}
}

// WritePose writes pose through slot's pose_buf pointer buf and bumps
// the slot's pose_seq (NextSeq skips 0, which C reads as "no pose yet"
// and would skip this slot for a frame). The caller checks buf first
// (PoseDstInMEM1) and that the pose is a whole Link (PoseUsable).
func WritePose(m mem.Mem, slot int, buf uint32, pose []byte) {
	m.Write(buf, pose)
	BumpSeq(m, mailbox.Mailbox+OffPoseSeq(slot))
}

// WriteFace writes face (one mailbox FaceState, player.FaceBytes) into
// slot's face_state and bumps the slot's face_seq. The C-side
// J3DMatPacket vtable hook reads face_state[slot] when the mini-Link's
// eye matpackets draw, and only swaps when face_seqs[slot] != 0, so the
// caller writes fresh bytes and bumps the seq whenever it has face
// data, changed or not.
func WriteFace(m mem.Mem, slot int, face []byte) {
	m.Write(mailbox.Mailbox+OffFaceState(slot), face)
	BumpSeq(m, mailbox.Mailbox+OffFaceSeq(slot))
}
