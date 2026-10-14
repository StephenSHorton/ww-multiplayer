package main

import "github.com/StephenSHorton/ww-multiplayer/internal/dolphin"

// Guards for puppet-sync's pose feed, which writes bytes from the network
// into the receiving Dolphin's emulated RAM.

// linkJointCount is the number of joints in Link's skeleton, one 48-byte Mtx
// each in mpNodeMtx. The C side sizes every mini-Link pose buffer for this
// rig (pose_buf_u32s = joint count * 12 in multiplayer.c), so a remote pose
// is written only when it has exactly this many joints.
const linkJointCount = 42

// mtxBytes is sizeof(Mtx): a 3x4 matrix of big-endian float32.
const mtxBytes = 48

// mem1Start and mem1End bound the GameCube's 24 MiB of MEM1 as the game
// addresses it (the cached mirror at 0x80000000).
const (
	mem1Start = 0x80000000
	mem1End   = 0x81800000
)

// linkPoseUsable reports whether a received pose is a whole Link skeleton:
// linkJointCount joints and exactly that many matrices of bytes.
func linkPoseUsable(joints, nbytes int) bool {
	return joints == linkJointCount && nbytes == linkJointCount*mtxBytes
}

// poseDstInMEM1 reports whether writing n bytes at dst, a pose_buf pointer
// read back out of the mailbox, stays inside MEM1.
func poseDstInMEM1(dst uint32, n int) bool {
	return n >= 0 && dst >= mem1Start && uint64(dst)+uint64(n) <= mem1End
}

// nextSeq returns the sequence byte that follows cur, skipping 0 on wrap.
// The C side reads a pose_seqs or face_seqs value of 0 as "nothing written
// yet" and skips the slot that frame (multiplayer.c, the pose_seqs checks in
// the mode-5 draw path), so a plain cur+1 would blank the remote Link once
// every 256 writes, about every 4 s at puppet-sync's 60 Hz.
func nextSeq(cur byte) byte {
	if cur == 0xFF {
		return 1
	}
	return cur + 1
}

// bumpSeqByte advances the mailbox sequence byte at addr with nextSeq and
// returns the value written. A failed read counts as 0, so the first value
// written is 1.
func bumpSeqByte(d *dolphin.Dolphin, addr uint32) (byte, error) {
	var cur byte
	if b, err := d.ReadAbsolute(addr, 1); err == nil && len(b) == 1 {
		cur = b[0]
	}
	next := nextSeq(cur)
	return next, d.WriteAbsolute(addr, []byte{next})
}
