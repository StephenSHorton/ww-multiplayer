package main

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player"
)

func TestYawAndStick(t *testing.T) {
	// The game's bearing: +z is 0, +x is 0x4000 (cM_atan2s(dx, dz)).
	for _, c := range []struct {
		tx, tz float64
		want   uint16
	}{{0, 10, 0x0000}, {10, 0, 0x4000}, {0, -10, 0x8000}, {-10, 0, 0xC000}} {
		if got := uint16(yawTo(0, 0, c.tx, c.tz)); got != c.want {
			t.Errorf("yawTo(%v,%v) = 0x%04X, want 0x%04X", c.tx, c.tz, got, c.want)
		}
	}
	// The stick's angle is cM_atan2s(-x, y): up 0, left 0x4000, down 0x8000,
	// right 0xC000 (measured live: m34DC).
	for _, c := range []struct {
		a      uint16
		sx, sy int8
	}{{0x0000, 0, 100}, {0x4000, -100, 0}, {0x8000, 0, -100}, {0xC000, 100, 0}} {
		x, y := stickFor(int16(c.a), 100)
		if x != c.sx || y != c.sy {
			t.Errorf("stickFor(0x%04X) = (%d,%d), want (%d,%d)", c.a, x, y, c.sx, c.sy)
		}
	}
	if angleDiff(0x0100, int16(-0x0100)) != 0x0200 || angleDiff(int16(-0x7F00), 0x7F00) != 0x0200 {
		t.Error("angleDiff does not wrap")
	}
}

// fakeLink is a Link that, while the stick is held, turns its shape angle
// a quarter of the way toward m34E8 per 33 ms frame, where m34E8 is the
// stick's angle, snapped to the nearest axis when a component is under the
// dead zone, plus a camera yaw; the light stick turns in place, a strong
// one walks.
type fakeLink struct {
	m        *mem.FakeMem
	link     uint32
	cam      int16
	held     bool
	pad      player.Pad
	backs    int
	frameAcc time.Duration
}

func (f *fakeLink) put16(off uint32, v int16) {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, uint16(v))
	_ = f.m.Store(f.link+off, b)
}

func (f *fakeLink) get16(off uint32) int16 {
	v, _ := mem.ReadU16(f.m, f.link+off)
	return int16(v)
}

func (f *fakeLink) step(d time.Duration) {
	if !f.held {
		return
	}
	f.frameAcc += d
	for ; f.frameAcc >= 33*time.Millisecond; f.frameAcc -= 33 * time.Millisecond {
		x, y := float64(f.pad.StickX), float64(f.pad.StickY)
		if math.Abs(x) < 15 {
			x = 0
		}
		if math.Abs(y) < 15 {
			y = 0
		}
		if x == 0 && y == 0 {
			continue
		}
		stick := int16(int32(math.Round(math.Atan2(-x, y) * 32768 / math.Pi)))
		move := stick + f.cam
		f.put16(faceOffStick, stick)
		f.put16(faceOffMove, move)
		shape := f.get16(faceOffShapeY)
		f.put16(faceOffShapeY, shape+angleDiff(move, shape)/4)
		if math.Hypot(x, y) > 60 { // a strong stick walks a step
			px, _ := mem.ReadF32(f.m, f.link+addr.ActorOffCurPos)
			b := make([]byte, 4)
			binary.BigEndian.PutUint32(b, math.Float32bits(px+5))
			_ = f.m.Store(f.link+addr.ActorOffCurPos, b)
		}
	}
}

func newFakeLink(t *testing.T, cam int16, shape int16) *fakeLink {
	t.Helper()
	f := &fakeLink{m: mem.NewFakeMem(), link: 0x80600000, cam: cam}
	f.put16(faceOffShapeY, shape)
	return f
}

func (f *fakeLink) env() faceEnv {
	return faceEnv{
		press:   func(p player.Pad) error { f.held, f.pad = true, p; return nil },
		release: func() error { f.held = false; f.frameAcc = 0; return nil },
		sleep:   f.step,
		steps:   80,
		back: func(p [3]float32) error {
			f.backs++
			b := make([]byte, 4)
			binary.BigEndian.PutUint32(b, math.Float32bits(p[0]))
			return f.m.Store(f.link+addr.ActorOffCurPos, b)
		},
	}
}

func TestFaceLinkTurnsWithinTol(t *testing.T) {
	for _, c := range []struct {
		name       string
		cam, shape int16
		tx, tz     float64
	}{
		{"a quarter turn, camera behind", 0x7000, 0, 1000, 0},
		{"near an axis (the dead zone case)", 0x4000, 0x2000, 80, 1000},
		{"half a turn the other way", int16(-0x3000), 0x7000, -1000, -50},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeLink(t, c.cam, c.shape)
			line, err := faceLink(f.m, f.link, c.tx, c.tz, 0x200, 100, f.env())
			if err != nil {
				t.Fatal(err)
			}
			want := yawTo(0, 0, c.tx, c.tz)
			if off := abs16(angleDiff(want, f.get16(faceOffShapeY))); off > 0x200 {
				t.Fatalf("off by 0x%X: %s", off, line)
			}
			if f.held {
				t.Error("the stick is still held")
			}
			if !strings.HasPrefix(line, "faced (") {
				t.Errorf("line %q", line)
			}
		})
	}
}

func TestFaceLinkPutsBack(t *testing.T) {
	f := newFakeLink(t, 0, 0)
	if _, err := faceLink(f.m, f.link, 1000, 300, 0x200, 100, f.env()); err != nil {
		t.Fatal(err)
	}
	if x, _ := mem.ReadF32(f.m, f.link+addr.ActorOffCurPos); x != 0 || f.backs == 0 {
		t.Errorf("not put back: x %v, backs %d", x, f.backs)
	}
}
