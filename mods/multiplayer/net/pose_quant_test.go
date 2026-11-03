package network

import (
	"encoding/binary"
	"math"
	"testing"
)

// poseClose reports whether two raw Mtx blocks are equal within the
// quantization step: rotation within 1/PoseRotScale, translation within
// 1/PoseTransScale plus float32 rounding at its magnitude. NaN and
// infinities must match exactly (they only ever travel raw).
func poseClose(a, b []byte) (i int, x, y float32, ok bool) {
	if len(a) != len(b) {
		return -1, 0, 0, false
	}
	for i = 0; i*4 < len(a); i++ {
		x = math.Float32frombits(binary.BigEndian.Uint32(a[i*4:]))
		y = math.Float32frombits(binary.BigEndian.Uint32(b[i*4:]))
		if math.Float32bits(x) == math.Float32bits(y) {
			continue
		}
		if x != x || y != y || math.IsInf(float64(x), 0) || math.IsInf(float64(y), 0) {
			return i, x, y, false
		}
		tol := 1.0 / PoseRotScale
		if i%4 == 3 {
			tol = 1.0/PoseTransScale + 4*float64(ulp32(x))
		}
		if math.Abs(float64(x)-float64(y)) > tol {
			return i, x, y, false
		}
	}
	return 0, 0, 0, true
}

func ulp32(x float32) float32 {
	return math.Nextafter32(float32(math.Abs(float64(x))), float32(math.Inf(1))) - float32(math.Abs(float64(x)))
}

// testRig builds a Link-like pose: 42 joints, each a rotation about a
// different axis, spread within ~150 units of a world position far from
// the origin (as on the Great Sea).
func testRig(world [3]float32, scale float32) []byte {
	m := make([]byte, 42*48)
	for j := 0; j < 42; j++ {
		a := float64(j) * 0.37
		c, s := float32(math.Cos(a))*scale, float32(math.Sin(a))*scale
		rows := [3][4]float32{
			{c, -s, 0, world[0] + float32(j%7)*20.3},
			{s, c, 0, world[1] + float32(j)*3.71},
			{0, 0, scale, world[2] - float32(j%5)*31.9},
		}
		if j%3 == 1 { // some joints rotate about X instead
			rows[0][0], rows[0][1], rows[1][0], rows[1][1] = scale, 0, 0, c
			rows[1][2], rows[2][1], rows[2][2] = -s, s, c
		}
		for r := 0; r < 3; r++ {
			for k := 0; k < 4; k++ {
				binary.BigEndian.PutUint32(m[j*48+(r*4+k)*4:], math.Float32bits(rows[r][k]))
			}
		}
	}
	return m
}

func TestPoseQuantizedHalvesTheFrameAndKeepsThePose(t *testing.T) {
	face := make([]byte, 17)
	for _, world := range [][3]float32{{0, 0, 0}, {12345.6, 210.25, -98765.4}, {-299999.5, 1500, 299999.9}} {
		m := testRig(world, 1)
		p := SerializePose(42, m, face)
		if p[2] != PoseInt16 {
			t.Fatalf("world %v: sent as format %d, want int16", world, p[2])
		}
		raw := 4 + 42*48 + len(face)
		if len(p) != 4+12+42*24+len(face) || len(p)*100/raw > 52 {
			t.Fatalf("world %v: %d bytes against %d raw", world, len(p), raw)
		}
		j, got, f, err := DeserializePose(p)
		if err != nil || j != 42 || string(f) != string(face) {
			t.Fatalf("world %v: joints %d face %x err %v", world, j, f, err)
		}
		if i, a, b, ok := poseClose(m, got); !ok {
			t.Fatalf("world %v: float %d (joint %d) %v became %v", world, i, i/12, a, b)
		}
		t.Logf("world %v: %d B quantized, %d B raw (%.0f%%)", world, len(p), raw, 100*float64(len(p))/float64(raw))
	}
}

func TestPoseFallsBackToRawWhenAValueDoesNotFit(t *testing.T) {
	cases := map[string]func(m []byte){
		"scale 5":       func(m []byte) { putF(m, 7, 0, 5) },
		"joint far off": func(m []byte) { putF(m, 9, 3, 2000) },
		"NaN rotation":  func(m []byte) { putF(m, 3, 5, float32(math.NaN())) },
		"inf origin":    func(m []byte) { putF(m, 0, 7, float32(math.Inf(1))) },
	}
	for name, spoil := range cases {
		m := testRig([3]float32{100, 0, 100}, 1)
		spoil(m)
		p := SerializePose(42, m, nil)
		if p[2] != PoseRaw || len(p) != 4+42*48 {
			t.Fatalf("%s: format %d, %d bytes; want raw", name, p[2], len(p))
		}
		if _, got, _, err := DeserializePose(p); err != nil || string(got) != string(m) {
			t.Fatalf("%s: raw frame did not come back byte for byte (err %v)", name, err)
		}
	}
}

func TestPoseRejectsUnknownFormat(t *testing.T) {
	p := SerializePose(1, make([]byte, 48), nil)
	p[2] = 2
	if _, _, _, err := DeserializePose(p); err == nil {
		t.Fatal("format 2 accepted")
	}
	short := SerializePose(2, testRig([3]float32{}, 1)[:96], nil)
	if _, _, _, err := DeserializePose(short[:len(short)-1]); err == nil {
		t.Fatal("truncated int16 block accepted")
	}
}

func putF(m []byte, joint, i int, v float32) {
	binary.BigEndian.PutUint32(m[joint*48+i*4:], math.Float32bits(v))
}
