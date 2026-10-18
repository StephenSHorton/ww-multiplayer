package avatar

import (
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player"
)

// Guards for puppet-sync's pose feed, which writes bytes from the network
// into the receiving Dolphin's emulated RAM.

// JointCount is the number of joints in Link's skeleton, one 48-byte Mtx
// each in mpNodeMtx. The C side sizes every mini-Link pose buffer for this
// rig (pose_buf_u32s = joint count * 12 in inject/src/mp/minilink.c), so a remote pose
// is written only when it has exactly this many joints. It is
// player.JointCount, the joint count Link.Pose sizes its read by.
const JointCount = player.JointCount

// MtxBytes is sizeof(Mtx): a 3x4 matrix of big-endian float32.
const MtxBytes = 48

// PoseUsable reports whether a received pose is a whole Link skeleton:
// JointCount joints and exactly that many matrices of bytes.
func PoseUsable(joints, nbytes int) bool {
	return joints == JointCount && nbytes == JointCount*MtxBytes
}

// PoseDstInMEM1 reports whether writing n bytes at dst, a pose_buf pointer
// read back out of the mailbox, stays inside MEM1 (the GameCube's 24 MiB
// at the cached mirror, addr.MEM1Base to addr.MEM1End).
func PoseDstInMEM1(dst uint32, n int) bool {
	return n >= 0 && dst >= addr.MEM1Base && uint64(dst)+uint64(n) <= uint64(addr.MEM1End)
}

// NextSeq returns the sequence byte that follows cur, skipping 0 on wrap.
// The C side reads a pose_seqs or face_seqs value of 0 as "nothing written
// yet" and skips the slot that frame (inject/src/mp/minilink.c, the pose_seqs checks in
// the mode-5 draw path), so a plain cur+1 would blank the remote Link once
// every 256 writes, about every 4 s at puppet-sync's 60 Hz.
func NextSeq(cur byte) byte {
	if cur == 0xFF {
		return 1
	}
	return cur + 1
}

// BumpSeq advances the mailbox sequence byte at a with NextSeq and
// returns the value written. A failed read counts as 0, so the first value
// written is 1.
func BumpSeq(m mem.Mem, a uint32) (byte, error) {
	var cur byte
	if b, err := m.Read(a, 1); err == nil && len(b) == 1 {
		cur = b[0]
	}
	next := NextSeq(cur)
	return next, m.Write(a, []byte{next})
}
