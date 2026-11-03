package feed

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar"
)

func TestLobbySpotFacesTheEye(t *testing.T) {
	// The title camera read live on the rig (wind#77): eye, center.
	c := lobbyCam{ok: true, fovy: lobbyRefFovy,
		eye:    [3]float32{-200403.16, 3548.51, 318567.0},
		center: [3]float32{-196774.59, 3511.64, 321242.63}}
	row := lobbyRow{dist: 700, drop: 110, spacing: 110, side: 260}
	x, y, z, yaw, ok := lobbySpot(c, row, 0, 1)
	if !ok {
		t.Fatal("no spot")
	}
	dx, dy, dz := float64(x-c.eye[0]), float64(y-c.eye[1]), float64(z-c.eye[2])
	if d := math.Sqrt(dx*dx + dy*dy + dz*dz); math.Abs(d-math.Sqrt(700*700+110*110+260*260)) > 1 {
		t.Errorf("distance from the eye %.1f", d)
	}
	// In front of the camera.
	fx, fz := float64(c.center[0]-c.eye[0]), float64(c.center[2]-c.eye[2])
	if dx*fx+dz*fz <= 0 {
		t.Error("spot is behind the camera")
	}
	// To its right: Y up, looking along +Z, the right is -X (forward x up).
	if fx*dz-fz*dx <= 0 {
		t.Error("spot is not on the camera's right")
	}
	// Facing the eye: (sin yaw, cos yaw) points back at it.
	a := float64(yaw) * math.Pi / 32768
	if dot := (math.Sin(a)*-dx + math.Cos(a)*-dz) / math.Hypot(dx, dz); dot < 0.999 {
		t.Errorf("yaw %d faces %.3f off the eye", yaw, dot)
	}
	// A second remote stands one spacing apart.
	x2, _, z2, _, _ := lobbySpot(c, row, 1, 2)
	x1, _, z1, _, _ := lobbySpot(c, row, 0, 2)
	if d := math.Hypot(float64(x2-x1), float64(z2-z1)); math.Abs(d-110) > 0.5 {
		t.Errorf("spacing %.1f", d)
	}
}

func TestLobbySpotNarrowViewMovesOut(t *testing.T) {
	c := lobbyCam{ok: true, fovy: lobbyRefFovy, eye: [3]float32{0, 0, 0}, center: [3]float32{0, 0, 100}}
	row := lobbyRow{dist: 500, drop: 0, spacing: 0, side: 0}
	_, _, z, _, _ := lobbySpot(c, row, 0, 1)
	c.fovy = lobbyRefFovy / 2
	_, _, z2, _, _ := lobbySpot(c, row, 0, 1)
	want := 500 * math.Tan(lobbyRefFovy*math.Pi/360) / math.Tan(lobbyRefFovy*math.Pi/720)
	if math.Abs(float64(z)-500) > 0.01 || math.Abs(float64(z2)-want) > 0.5 {
		t.Errorf("z %.2f at the reference view, %.2f at half (want %.2f)", z, z2, want)
	}
	c.fovy = 0 // unread: the reference distance
	if _, _, z3, _, _ := lobbySpot(c, row, 0, 1); math.Abs(float64(z3)-500) > 0.01 {
		t.Errorf("z %.2f with no field of view", z3)
	}
}

func TestLobbySpotStraightDown(t *testing.T) {
	c := lobbyCam{ok: true, eye: [3]float32{0, 100, 0}, center: [3]float32{0, 0, 0}}
	if _, _, _, _, ok := lobbySpot(c, lobbyRowFromEnv(), 0, 1); ok {
		t.Error("a camera looking straight down has no row")
	}
}

func TestPoseYawAfterRotate(t *testing.T) {
	// One identity joint faces +Z (yaw 0); turned by 0x4000 it faces +X.
	pose := make([]byte, avatar.MtxBytes)
	for i, v := range []float32{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0} {
		binary.BigEndian.PutUint32(pose[4*i:], math.Float32bits(v))
	}
	if y := poseYaw(pose); y != 0 {
		t.Fatalf("identity yaw %d", y)
	}
	avatar.RotateY(pose, 1, 0x4000)
	if y := poseYaw(pose); y != 0x4000 {
		t.Errorf("turned yaw 0x%X, want 0x4000", y)
	}
	avatar.RotateY(pose, 1, -0x6000)
	if y := poseYaw(pose); y != -0x2000 {
		t.Errorf("turned back yaw %d, want %d", y, -0x2000)
	}
}
