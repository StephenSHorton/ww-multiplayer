package player

import (
	"errors"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// JointCount is the number of joints in Link's model (cl.bdl), so the
// number of 3x4 matrices in a published pose.
const JointCount = 42

// PoseBytes is the size of a published pose: JointCount row-major
// big-endian f32[3][4] matrices.
const PoseBytes = JointCount * 48

// FaceBytes is the size of the local face state the C side publishes:
// one mailbox FaceState (mailbox.h).
const FaceBytes = mailbox.FaceStateSize

// Position is Link's current.pos and shape_angle as the game holds them.
type Position struct {
	PosX float32
	PosY float32
	PosZ float32
	RotX int16
	RotY int16
	RotZ int16
}

// errNotLoaded is Position's error while mpPlayerPtr[0] is 0 or unreadable.
var errNotLoaded = errors.New("link not loaded")

// Link reads the local Link through m.
type Link struct {
	m mem.Reader
}

// NewLink returns a Link that reads through m.
func NewLink(m mem.Reader) *Link { return &Link{m: m} }

// Ptr reads mpPlayerPtr[0], Link's actor address. It is 0 while there is
// no Link (title screen, file select, the reload between scenes).
func (l *Link) Ptr() (uint32, error) {
	return mem.ReadU32(l.m, addr.PlayerPtr)
}

// Ready reports whether Link exists: mpPlayerPtr[0] reads and lies in
// MEM1. It is false on the title screen, the save-file menu, world-map
// cutscenes and the brief reload window between scenes.
//
// mp-local's "wait until both Dolphins are ready" gate uses it, so the
// connect sequence does not start against a Dolphin that has not loaded a
// save yet.
func (l *Link) Ready() bool {
	ptr, err := l.Ptr()
	if err != nil {
		return false
	}
	return addr.InMEM1(ptr)
}

// Position reads Link's current position and rotation. It fails with
// "link not loaded" while mpPlayerPtr[0] is 0 or unreadable.
func (l *Link) Position() (*Position, error) {
	linkPtr, err := l.Ptr()
	if err != nil || linkPtr == 0 {
		return nil, errNotLoaded
	}
	posData, err := l.m.Read(linkPtr+addr.ActorOffCurPos, 12)
	if err != nil {
		return nil, err
	}
	rotData, err := l.m.Read(linkPtr+addr.ActorOffCurAngle, 6)
	if err != nil {
		return nil, err
	}
	return &Position{
		PosX: mem.F32BE(posData[0:4]),
		PosY: mem.F32BE(posData[4:8]),
		PosZ: mem.F32BE(posData[8:12]),
		RotX: mem.S16BE(rotData[0:2]),
		RotY: mem.S16BE(rotData[2:4]),
		RotZ: mem.S16BE(rotData[4:6]),
	}, nil
}

// DemoType reads Link's mDemo.mDemoType (daPy_py_c +0x304,
// d_a_player.h:499): 0 in normal play, nonzero while the game holds him
// (a Floormaster's or ReDead's grab, the Forsaken Fortress Moblin's
// holdup). It fails with "link not loaded" while there is no Link.
func (l *Link) DemoType() (uint16, error) {
	p, err := l.Ptr()
	if err != nil || p == 0 {
		return 0, errNotLoaded
	}
	return mem.ReadU16(l.m, p+addr.PlayerOffDemoType)
}

// Pose returns the world-space pose the C draw hook published this frame:
// PoseBytes of JointCount matrices, copied from mpNodeMtx once per frame
// after daPy_lk_c_draw returns, so a read never sees a half-calculated
// pose. It returns nil and no error while no pose is published
// (pose_publish_state is not 1, or the buffer pointer is not in MEM1).
func (l *Link) Pose() ([]byte, error) {
	st, err := l.m.Read(mailbox.Mailbox+mailbox.OffPosePublishState, 1)
	if err != nil {
		return nil, err
	}
	if st[0] != 1 {
		return nil, nil
	}
	pubPtr, err := mem.ReadU32(l.m, mailbox.Mailbox+mailbox.OffPosePublishPtr)
	if err != nil {
		return nil, err
	}
	if !addr.InMEM1(pubPtr) {
		return nil, nil
	}
	return l.m.Read(pubPtr, PoseBytes)
}

// Face returns the local Link's face state (FaceBytes), which the C side
// publishes at the top of daPy_draw_hook before any remote face swap
// runs, so it never catches a swapped value.
func (l *Link) Face() ([]byte, error) {
	return l.m.Read(mailbox.Mailbox+mailbox.OffFaceStateLocal, FaceBytes)
}
