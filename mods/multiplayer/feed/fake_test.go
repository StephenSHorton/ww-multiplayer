package feed

import (
	"bytes"
	"errors"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/boatsync"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player"
)

// recSender records what fakeSend sends.
type recSender struct {
	pos     *network.PlayerPosition
	place   *network.Place
	joints  int
	pose    []byte
	face    []byte
	poseErr error
}

func (r *recSender) SendPosition(p *network.PlayerPosition) error { r.pos = p; return nil }
func (r *recSender) SendPositionAt(p *network.PlayerPosition, pl network.Place) error {
	r.pos, r.place = p, &pl
	return nil
}
func (r *recSender) SendPose(joints int, m, f []byte) error {
	r.joints, r.pose, r.face = joints, m, f
	return r.poseErr
}

// TestFakeSendReplaysTrailers: a fake frame is the live position moved
// 1000 X with the live place, and the captured pose with its whole face
// trailer, the ship included (an aboard ship is relative to Link, so it
// rides under the fake).
func TestFakeSendReplaysTrailers(t *testing.T) {
	ship := boatsync.Encode(boatsync.Boat{Present: true, Aboard: true, MastOn: true, SailOn: true, Room: 44, Pos: [3]float32{0, -15, 30}, Stage: "sea"}, true)
	face := append(bytes.Repeat([]byte{0x27}, player.FaceBytes), 0x00, 0x38, 0x3B)
	face = append(face, ship...)
	if len(face) != 59 {
		t.Fatalf("test face trailer is %d bytes, want 59", len(face))
	}
	pose := make([]byte, avatar.JointCount*48)
	live := &player.Position{PosX: -224530, PosY: 19, PosZ: 325017, RotY: 0x4000}
	place := network.Place{Stage: "sea", Room: 44, InPlay: true}

	r := &recSender{}
	if err := fakeSend(r, live, place, true, pose, face); err != nil {
		t.Fatal(err)
	}
	if r.pos == nil || r.pos.PosX != live.PosX+1000 || r.pos.PosY != live.PosY || r.pos.PosZ != live.PosZ || r.pos.RotY != live.RotY {
		t.Errorf("position %+v, want the live one moved 1000 X", r.pos)
	}
	if r.place == nil || *r.place != place {
		t.Errorf("place %v, want %v", r.place, place)
	}
	if r.joints != avatar.JointCount || !bytes.Equal(r.pose, pose) {
		t.Errorf("pose: %d joints, %d bytes", r.joints, len(r.pose))
	}
	if !bytes.Equal(r.face, face) {
		t.Errorf("face trailer not replayed as captured")
	}
	if b, ok := boatsync.Decode(r.face); !ok || !b.Present || !b.Aboard || b.Pos != [3]float32{0, -15, 30} {
		t.Errorf("replayed ship decodes to %+v %v", b, ok)
	}

	// No place read: the plain position, still with the pose.
	r = &recSender{}
	if err := fakeSend(r, live, network.Place{}, false, pose, face); err != nil {
		t.Fatal(err)
	}
	if r.place != nil || r.pos == nil || !bytes.Equal(r.face, face) {
		t.Errorf("without a place: place %v, pos %v", r.place, r.pos)
	}

	// A failed pose send is reported.
	r = &recSender{poseErr: errors.New("closed")}
	if err := fakeSend(r, live, place, true, pose, face); err == nil {
		t.Error("pose error not returned")
	}
}
