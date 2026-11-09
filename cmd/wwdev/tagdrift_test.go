package main

import (
	"image"
	"image/color"
	"math"
	"strings"
	"testing"
)

// tdScene draws a synthetic frame: a grey-green ground, a pale wall band
// (white tag text on it differs little from it, as on Hyrule Castle's
// walls), the observer's own Link at the centre (its colour per frame, so
// the no-remote frames differ there as an idle animation does), and
// optionally the remote's body and its tag.
type tdScene struct {
	w, h      int
	selfShade uint8
	body      int // remote body centre x; 0 = none
	tag       int // tag centre x; 0 = none
	tagW      int
}

func (s tdScene) draw() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, s.w, s.h))
	for y := 0; y < s.h; y++ {
		for x := 0; x < s.w; x++ {
			c := color.RGBA{60, 110, 50, 255}
			if y < s.h/2 {
				c = color.RGBA{205, 200, 190, 255} // pale wall
			}
			img.SetRGBA(x, y, c)
		}
	}
	fill := func(x0, y0, x1, y1 int, c color.RGBA) {
		for y := max(y0, 0); y < min(y1, s.h); y++ {
			for x := max(x0, 0); x < min(x1, s.w); x++ {
				img.SetRGBA(x, y, c)
			}
		}
	}
	// The observer's Link: centre, lower half.
	fill(s.w/2-40, s.h/2-60, s.w/2+40, s.h-40, color.RGBA{s.selfShade, 160, 40, 255})
	if s.body != 0 {
		// Head at y 300, body below.
		fill(s.body-20, 300, s.body+20, 330, color.RGBA{230, 180, 140, 255})
		fill(s.body-25, 330, s.body+25, 420, color.RGBA{30, 140, 40, 255})
	}
	if s.tag != 0 {
		w := s.tagW
		if w == 0 {
			w = 80
		}
		// Glyph-like strokes: white columns with a black shadow 2 px
		// down-right, in a 14-px-tall band ending above the head.
		for x := s.tag - w/2; x < s.tag+w/2; x += 5 {
			fill(x+2, 278, x+4, 294, color.RGBA{0, 0, 0, 255})
			fill(x, 276, x+2, 292, color.RGBA{255, 255, 255, 255})
		}
	}
	return img
}

func tdFrames(w, h, body, tagF, tagO, tagOW int) (ns []*image.RGBA, f, o *image.RGBA) {
	for i := 0; i < 4; i++ {
		ns = append(ns, tdScene{w: w, h: h, selfShade: uint8(40 + 50*i)}.draw())
	}
	f = tdScene{w: w, h: h, selfShade: 90, body: body, tag: tagF}.draw()
	if tagO != 0 {
		o = tdScene{w: w, h: h, selfShade: 140, body: body, tag: tagO, tagW: tagOW}.draw()
	}
	return ns, f, o
}

func checksByName(cs []smokeCheck) map[string]smokeCheck {
	m := map[string]smokeCheck{}
	for _, c := range cs {
		m[c.Name] = c
	}
	return m
}

func TestTagDriftFixedPassesOldFails(t *testing.T) {
	const w, h, xs = 1278, 664, 0.693
	c := float64(w-1) / 2
	body := 1010
	tagO := int(math.Round(c + (float64(body)-c)/xs))
	ns, f, o := tdFrames(w, h, body, body, tagO, 115)
	m := measureTagDrift(ns, f, o, tagDriftDefaults)
	if !m.TagF.OK || !m.TagO.OK || !m.Body.OK {
		t.Fatalf("not found: tagF %+v tagO %+v body %+v", m.TagF, m.TagO, m.Body)
	}
	if math.Abs(m.Body.CX-float64(body)) > 2 || math.Abs(m.TagF.CX-float64(body)) > 3 {
		t.Fatalf("body at %.1f, tag at %.1f; want both near %d", m.Body.CX, m.TagF.CX, body)
	}
	cs, cannot := tagDriftVerdicts("x", m, xs, true, tagDriftDefaults)
	if cannot != "" {
		t.Fatal(cannot)
	}
	by := checksByName(cs)
	if !by["x tag on body"].Pass {
		t.Errorf("fixed tag failed: %s", by["x tag on body"].Detail)
	}
	if !by["x old tag drifts"].Pass {
		t.Errorf("old tag's drift not caught: %s", by["x old tag drifts"].Detail)
	}
	// The old frame judged as the fixed one fails: the check catches the
	// pre-#329 tag.
	mOld := measureTagDrift(ns, o, nil, tagDriftDefaults)
	cs, _ = tagDriftVerdicts("old", mOld, xs, false, tagDriftDefaults)
	if c := checksByName(cs)["old tag on body"]; c.Pass {
		t.Errorf("the pre-#329 tag passed: %s", c.Detail)
	}
}

func TestTagDriftLeftSideOnPaleWall(t *testing.T) {
	const w, h, xs = 1278, 664, 0.693
	c := float64(w-1) / 2
	body := 290
	tagO := int(math.Round(c + (float64(body)-c)/xs))
	ns, f, o := tdFrames(w, h, body, body, tagO, 0)
	m := measureTagDrift(ns, f, o, tagDriftDefaults)
	cs, cannot := tagDriftVerdicts("l", m, xs, true, tagDriftDefaults)
	if cannot != "" {
		t.Fatal(cannot)
	}
	for _, c := range cs {
		if !c.Pass {
			t.Errorf("%s: %s", c.Name, c.Detail)
		}
	}
}

func TestTagDriftScaleOneOldUnchanged(t *testing.T) {
	ns, f, o := tdFrames(1278, 664, 1000, 1000, 1003, 0)
	m := measureTagDrift(ns, f, o, tagDriftDefaults)
	cs, cannot := tagDriftVerdicts("one", m, 1, true, tagDriftDefaults)
	if cannot != "" {
		t.Fatal(cannot)
	}
	if c := checksByName(cs)["one old tag unchanged"]; !c.Pass {
		t.Errorf("%s", c.Detail)
	}
	// A tag the "old" run moved at scale 1 is a failure.
	ns, f, o = tdFrames(1278, 664, 1000, 1000, 1100, 0)
	m = measureTagDrift(ns, f, o, tagDriftDefaults)
	cs, _ = tagDriftVerdicts("one", m, 1, true, tagDriftDefaults)
	if c := checksByName(cs)["one old tag unchanged"]; c.Pass {
		t.Errorf("a moved tag at scale 1 passed: %s", c.Detail)
	}
}

func TestTagDriftRefusesCentralRemote(t *testing.T) {
	// V4's blind spot: near the centre any scale looks right.
	ns, f, o := tdFrames(1278, 664, 700, 700, 720, 0)
	m := measureTagDrift(ns, f, o, tagDriftDefaults)
	_, cannot := tagDriftVerdicts("c", m, 0.693, true, tagDriftDefaults)
	if !strings.Contains(cannot, "too near the picture's centre") {
		t.Fatalf("cannot = %q, want the centre refusal", cannot)
	}
}

func TestTagDriftNoTag(t *testing.T) {
	ns, f, _ := tdFrames(1278, 664, 1000, 0, 0, 0)
	m := measureTagDrift(ns, f, nil, tagDriftDefaults)
	if _, cannot := tagDriftVerdicts("n", m, 0.75, false, tagDriftDefaults); !strings.Contains(cannot, "tag was not found") {
		t.Fatalf("cannot = %q", cannot)
	}
}

func TestGameAreaPillarbox(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1278, 664))
	for y := 0; y < 664; y++ {
		for x := 84; x < 1194; x++ {
			img.SetRGBA(x, y, color.RGBA{100, 120, 90, 255})
		}
	}
	if a := gameArea(img); a != image.Rect(84, 0, 1194, 664) {
		t.Fatalf("gameArea = %v", a)
	}
}

func TestXScaleFromFlags(t *testing.T) {
	for _, tc := range []struct {
		flags uint32
		want  float64
	}{{0, 1}, {0x0000_0003, 1}, {0xC000_0001, 0.75}, {0xB158_0000, 0xB158 / 65536.0}} {
		if got := xscaleFromFlags(tc.flags); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("xscaleFromFlags(%#x) = %v, want %v", tc.flags, got, tc.want)
		}
	}
}

func TestCameraSpot(t *testing.T) {
	// Looking down -Z from (0, 100, 200): ahead is -Z, the right is +X
	// (forward x up, levelled).
	c := camView{eye: [3]float64{0, 100, 200}, center: [3]float64{0, 90, 0}}
	x, z, ok := cameraSpot(c, 400, 250)
	if !ok || math.Abs(x-(-250)) > 1e-9 && math.Abs(x-250) > 1e-9 || math.Abs(z-(-200)) > 1e-9 {
		t.Fatalf("cameraSpot = (%v, %v, %v)", x, z, ok)
	}
	if _, _, ok := cameraSpot(camView{eye: [3]float64{0, 100, 0}, center: [3]float64{0, 0, 0}}, 400, 250); ok {
		t.Fatal("a camera looking straight down gave a spot")
	}
}

func TestParseTagDriftArgs(t *testing.T) {
	o, err := parseTagDriftArgs([]string{"--side", "-250", "--case", "x"})
	if err != nil || o.side != -250 || o.label != "x" || o.nameB != "PlayerB" {
		t.Fatalf("got %+v, %v", o, err)
	}
	for _, bad := range [][]string{{"--side", "20"}, {"--dist", "50"}, {"--settle", "1s"}, {"--names", "A"}, {"extra"}} {
		if _, err := parseTagDriftArgs(bad); err == nil {
			t.Errorf("%v: no error", bad)
		}
	}
}
