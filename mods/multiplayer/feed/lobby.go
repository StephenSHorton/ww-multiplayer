package feed

import (
	"bytes"
	"encoding/binary"
	"math"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// Lobby presence (issue #38): on the title screen the remotes' Links stand
// in a row in front of the title camera, facing it, with their name tags,
// instead of at their own world coordinates (another stage's, or a spot on
// the sea nowhere near the title's view). The title stage (sea_T) has a
// Link whose draw runs every frame, so the mini-Link draw path, the REL's
// sword, shield and name tag included, works there as in play; only the
// placement differs. The pose keeps moving as the remote moves; the whole
// body is turned to face the camera and set down on the row.
//
// The file select screen has no Link and draws no 3D models of ours; its
// list of names is the C side's (WWVIS_FLAG_LOBBY).
//
// WW_LOBBY=off (also 0, false, no, none) turns both off: on the title the
// remotes are then hidden, as they are on the file select screen.

// The row, in world units, from the title camera's eye at the reference
// field of view (lobbyRefFovy): dist ahead along the view, drop below it,
// the Links spacing apart and the row's middle side to the camera's right
// (negative: left). The defaults stand a Link's full body to the right of
// PRESS START, under the logo's right end, with its tag clear of the logo
// (live on the title's sea pass, wind#77). WW_LOBBY_{DIST,DROP,SPACING,SIDE}
// move it (dev knobs, read once at start).
type lobbyRow struct{ dist, drop, spacing, side float32 }

func lobbyRowFromEnv() lobbyRow {
	return lobbyRow{
		dist:    EnvFloat32("WW_LOBBY_DIST", 550),
		drop:    EnvFloat32("WW_LOBBY_DROP", 95),
		spacing: EnvFloat32("WW_LOBBY_SPACING", 80),
		side:    EnvFloat32("WW_LOBBY_SIDE", 150),
	}
}

// lobbyRefFovy is the vertical field of view, in degrees, the row's
// numbers are for: the title camera's on its sea pass. Its passes zoom
// (a narrower view on the opening shot), so the row moves away as the
// view narrows and keeps the Links the same size on screen.
const lobbyRefFovy = 44.39

// lobbyCam is the title camera for one Feed: ok while this game shows the
// title stage with Link loaded and no stage change pending. fovy is its
// vertical field of view in degrees.
type lobbyCam struct {
	ok          bool
	eye, center [3]float32
	fovy        float32
}

// scale is how far the row moves out at this camera's field of view.
func (c lobbyCam) scale() float64 {
	f := float64(c.fovy)
	if !(f >= 5 && f <= 120) {
		return 1
	}
	return math.Tan(lobbyRefFovy*math.Pi/360) / math.Tan(f*math.Pi/360)
}

// readLobbyCam reads the title camera. A read error, another stage or a
// camera that isn't in MEM1 gives ok false.
func (f *Feeder) readLobbyCam() lobbyCam {
	link, err := f.m.ReadU32(addr.PlayerPtr)
	if err != nil || !addr.InMEM1(link) {
		return lobbyCam{}
	}
	n := int(addr.NextStageEnable-addr.CurStageName) + 1
	b, err := f.m.Read(addr.CurStageName, n)
	if err != nil || len(b) != n || b[len(b)-1] != 0 {
		return lobbyCam{}
	}
	name := b[:addr.StageNameLen]
	if i := bytes.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	if string(name) != addr.StageTitle {
		return lobbyCam{}
	}
	cam, err := f.m.ReadU32(addr.CameraPtr)
	if err != nil || !addr.InMEM1(cam) {
		return lobbyCam{}
	}
	v, err := f.m.Read(cam+addr.ViewOffFovy, 32)
	if err != nil || len(v) != 32 {
		return lobbyCam{}
	}
	var c lobbyCam
	c.fovy = math.Float32frombits(binary.BigEndian.Uint32(v))
	v = v[addr.ViewOffEye-addr.ViewOffFovy:]
	for i := 0; i < 3; i++ {
		c.eye[i] = math.Float32frombits(binary.BigEndian.Uint32(v[4*i:]))
		c.center[i] = math.Float32frombits(binary.BigEndian.Uint32(v[12+4*i:]))
	}
	for _, x := range append(c.eye[:], c.center[:]...) {
		if math.IsNaN(float64(x)) || math.Abs(float64(x)) > 1e7 {
			return lobbyCam{}
		}
	}
	c.ok = true
	return c
}

// lobbySpot is where the remote in place i of n stands on the title, and
// the s16 yaw that faces it to the camera. The row is laid out in the
// camera's own frame (forward, up, right), so it sits at the same spot on
// the screen whatever the camera's pitch. ok is false for a camera that
// looks straight up or down.
func lobbySpot(c lobbyCam, row lobbyRow, i, n int) (x, y, z float32, yaw int16, ok bool) {
	f := [3]float64{float64(c.center[0] - c.eye[0]), float64(c.center[1] - c.eye[1]), float64(c.center[2] - c.eye[2])}
	l := math.Sqrt(f[0]*f[0] + f[1]*f[1] + f[2]*f[2])
	h := math.Hypot(f[0], f[2])
	if l < 1e-3 || h < 1e-3*l {
		return 0, 0, 0, 0, false
	}
	for k := range f {
		f[k] /= l
	}
	// right = forward x worldUp, levelled; up = right x forward.
	r := [3]float64{-f[2] * l / h, 0, f[0] * l / h}
	u := [3]float64{r[1]*f[2] - r[2]*f[1], r[2]*f[0] - r[0]*f[2], r[0]*f[1] - r[1]*f[0]}
	k := c.scale()
	side := k * (float64(row.side) + (float64(i)-float64(n-1)/2)*float64(row.spacing))
	d, drop := k*float64(row.dist), k*float64(row.drop)
	x = c.eye[0] + float32(f[0]*d+r[0]*side-u[0]*drop)
	y = c.eye[1] + float32(f[1]*d+r[1]*side-u[1]*drop)
	z = c.eye[2] + float32(f[2]*d+r[2]*side-u[2]*drop)
	// Face the eye: an actor at yaw a faces (sin a, cos a).
	yaw = int16(int32(math.Round(math.Atan2(float64(c.eye[0]-x), float64(c.eye[2]-z)) * 32768 / math.Pi)))
	return x, y, z, yaw, true
}

// poseYaw is the s16 yaw a pose's root joint faces: its local +Z axis
// (the matrix's third column) on the level plane. A Link's model faces
// its root's +Z.
func poseYaw(pose []byte) int16 {
	if len(pose) < 48 {
		return 0
	}
	m02 := math.Float32frombits(binary.BigEndian.Uint32(pose[8:]))
	m22 := math.Float32frombits(binary.BigEndian.Uint32(pose[40:]))
	return int16(int32(math.Round(math.Atan2(float64(m02), float64(m22)) * 32768 / math.Pi)))
}

// lobbyPlace is where link slot ls stands this Feed: its place in the row
// is its rank among the link slots in use. ok is false off the title or
// with WW_LOBBY off.
func (f *Feeder) lobbyPlace(ls int) (x, y, z float32, yaw int16, ok bool) {
	if !f.lobby.ok || !f.lobbyOn {
		return 0, 0, 0, 0, false
	}
	i, n := 0, 0
	for _, s := range f.remoteToLinkSlot {
		n++
		if s < ls {
			i++
		}
	}
	return lobbySpot(f.lobby, f.lobbyRow, i, n)
}
