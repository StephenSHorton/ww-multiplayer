package player

import (
	"bytes"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player/playertest"
)

const testActor = 0x80600000

func TestLinkNotLoaded(t *testing.T) {
	l := NewLink(mem.NewFakeMem())
	if p, err := l.Ptr(); err != nil || p != 0 {
		t.Errorf("Ptr = %#x, %v; want 0, nil", p, err)
	}
	if l.Ready() {
		t.Error("Ready with mpPlayerPtr[0] = 0")
	}
	if _, err := l.Position(); err == nil || err.Error() != "link not loaded" {
		t.Errorf("Position error %v, want link not loaded", err)
	}
	if _, err := l.DemoType(); err == nil || err.Error() != "link not loaded" {
		t.Errorf("DemoType error %v, want link not loaded", err)
	}
}

func TestLinkReadyNeedsMEM1(t *testing.T) {
	m := mem.NewFakeMem()
	store(t, m, addr.PlayerPtr, u32b(0x7FFFFFF0))
	if NewLink(m).Ready() {
		t.Error("Ready with mpPlayerPtr[0] below MEM1")
	}
	store(t, m, addr.PlayerPtr, u32b(testActor))
	if !NewLink(m).Ready() {
		t.Error("not Ready with mpPlayerPtr[0] in MEM1")
	}
}

func TestLinkPositionAndDemo(t *testing.T) {
	m := mem.NewFakeMem()
	store(t, m, addr.PlayerPtr, u32b(testActor))
	store(t, m, testActor+addr.ActorOffCurPos, playertest.PosBytes([3]float32{-200050, 12.5, 300020}))
	store(t, m, testActor+addr.ActorOffCurAngle, []byte{0xFF, 0xFE, 0x40, 0x00, 0x00, 0x01})
	store(t, m, testActor+addr.PlayerOffDemoType, []byte{0x00, 0x2A})
	l := NewLink(m)
	got, err := l.Position()
	if err != nil {
		t.Fatal(err)
	}
	want := Position{PosX: -200050, PosY: 12.5, PosZ: 300020, RotX: -2, RotY: 0x4000, RotZ: 1}
	if *got != want {
		t.Errorf("Position = %+v, want %+v", *got, want)
	}
	if d, err := l.DemoType(); err != nil || d != 42 {
		t.Errorf("DemoType = %d, %v; want 42", d, err)
	}
}

func TestLinkPose(t *testing.T) {
	m := mem.NewFakeMem()
	l := NewLink(m)
	if p, err := l.Pose(); p != nil || err != nil {
		t.Errorf("unpublished Pose = %d bytes, %v; want nil, nil", len(p), err)
	}
	const buf = 0x80700000
	pose := make([]byte, PoseBytes)
	for i := range pose {
		pose[i] = byte(i)
	}
	store(t, m, buf, pose)
	store(t, m, mailbox.Mailbox+mailbox.OffPosePublishPtr, u32b(buf))
	if p, err := l.Pose(); p != nil || err != nil {
		t.Errorf("Pose with state 0 = %d bytes, %v; want nil, nil", len(p), err)
	}
	store(t, m, mailbox.Mailbox+mailbox.OffPosePublishState, []byte{1})
	p, err := l.Pose()
	if err != nil || !bytes.Equal(p, pose) {
		t.Errorf("Pose = %d bytes, %v; want the %d published bytes", len(p), err, PoseBytes)
	}
	store(t, m, mailbox.Mailbox+mailbox.OffPosePublishPtr, u32b(0))
	if p, err := l.Pose(); p != nil || err != nil {
		t.Errorf("Pose with a null buffer = %d bytes, %v; want nil, nil", len(p), err)
	}
}

func TestLinkFace(t *testing.T) {
	m := mem.NewFakeMem()
	face := make([]byte, FaceBytes)
	for i := range face {
		face[i] = byte(0xA0 + i)
	}
	store(t, m, mailbox.Mailbox+mailbox.OffFaceStateLocal, face)
	got, err := NewLink(m).Face()
	if err != nil || !bytes.Equal(got, face) {
		t.Errorf("Face = % X, %v; want % X", got, err, face)
	}
	if FaceBytes != 16 || PoseBytes != 2016 {
		t.Errorf("FaceBytes %d, PoseBytes %d; want 16 and 2016 (the wire sizes)", FaceBytes, PoseBytes)
	}
}
