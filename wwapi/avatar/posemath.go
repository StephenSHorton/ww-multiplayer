package avatar

import (
	"encoding/binary"
	"math"
)

// Pose math. A pose is mpNodeMtx: one row-major 3x4 big-endian f32 Mtx
// per joint.
//
//	bytes  0..15 = row 0 (m00, m01, m02, m03)   m03 = X translation
//	bytes 16..31 = row 1 (m10, m11, m12, m13)   m13 = Y translation
//	bytes 32..47 = row 2 (m20, m21, m22, m23)   m23 = Z translation
//
// Stride = MtxBytes per joint.
const (
	mtxOffTransX = 12
	mtxOffTransY = 28
	mtxOffTransZ = 44
)

func readBEFloat(b []byte, off int) float32 {
	return math.Float32frombits(binary.BigEndian.Uint32(b[off : off+4]))
}
func writeBEFloat(b []byte, off int, v float32) {
	binary.BigEndian.PutUint32(b[off:off+4], math.Float32bits(v))
}

// Localize subtracts (dx, dy, dz) from every joint's translation
// column, turning a world-space mpNodeMtx blob into a pose relative to the
// sender's origin. Rotation parts are untouched.
func Localize(buf []byte, joints int, dx, dy, dz float32) {
	for j := 0; j < joints; j++ {
		base := j * MtxBytes
		writeBEFloat(buf, base+mtxOffTransX, readBEFloat(buf, base+mtxOffTransX)-dx)
		writeBEFloat(buf, base+mtxOffTransY, readBEFloat(buf, base+mtxOffTransY)-dy)
		writeBEFloat(buf, base+mtxOffTransZ, readBEFloat(buf, base+mtxOffTransZ)-dz)
	}
}

// ApplyAt re-adds (tx, ty, tz) to every joint's translation column.
// Inverse of Localize; used by the receiver to land the
// localized pose at any chosen world position.
func ApplyAt(buf []byte, joints int, tx, ty, tz float32) {
	for j := 0; j < joints; j++ {
		base := j * MtxBytes
		writeBEFloat(buf, base+mtxOffTransX, readBEFloat(buf, base+mtxOffTransX)+tx)
		writeBEFloat(buf, base+mtxOffTransY, readBEFloat(buf, base+mtxOffTransY)+ty)
		writeBEFloat(buf, base+mtxOffTransZ, readBEFloat(buf, base+mtxOffTransZ)+tz)
	}
}

// Lerp closes `dst` toward `target` by fraction k per call,
// element-wise across every BE f32 in the joint matrix blob (12 floats
// per joint × N joints). Acts as a per-frame EMA: a steady stream of
// fresh `target` poses converges `dst` toward each one over a few
// frames, smoothing out any jitter between sender and receiver clocks.
// Rotation columns aren't strictly orthonormal during the transition
// but per-frame angular delta is small enough that visible vertex
// drift is bounded; full quaternion slerp can replace this if needed.
func Lerp(dst, target []byte, k float32) {
	if len(dst) != len(target) {
		return
	}
	n := len(dst) / 4
	for i := 0; i < n; i++ {
		off := i * 4
		dv := readBEFloat(dst, off)
		tv := readBEFloat(target, off)
		writeBEFloat(dst, off, dv+(tv-dv)*k)
	}
}
