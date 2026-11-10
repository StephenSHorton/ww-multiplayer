package main

import (
	"math"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// The live clipper of section 3.2: fovY 60, aspect 1.28, far 20500.
var testClip = cullClip{FovY: 60, Aspect: 1.28, Near: 1, Far: 20500}

// testView looks down -z from (100, 0, 50): an identity rotation.
var testView = [12]float32{1, 0, 0, -100, 0, 1, 0, 0, 0, 0, 1, -50}

func TestCullClipTans(t *testing.T) {
	if got := testClip.tanH(); math.Abs(got-0.739) > 0.0005 {
		t.Errorf("side-plane tan %.4f, want 0.739", got)
	}
	// The band at fovY 60: 0.739..0.985 whether the clipper is vanilla
	// (1.28: the 16:9 edge) or widened by mode 1 (0x3FDA740E) or mode 2
	// (0x3FDA740D): its side plane is the visible edge.
	for _, a := range []uint32{0x3FA3D70A, 0x3FDA740E, 0x3FDA740D} {
		c := cullClip{FovY: 60, Aspect: math.Float32frombits(a)}
		if lo, hi := c.band(cullWide169); math.Abs(lo-0.739) > 0.0005 || math.Abs(hi-0.985) > 0.0005 {
			t.Errorf("aspect 0x%08X: band %.4f..%.4f, want 0.739..0.985", a, lo, hi)
		}
	}
	// fovY 55, widened (the stage-1 live run): the band ends at the side
	// plane, 0.888, so an actor at tanH -0.927 is off screen, not in it.
	c55 := cullClip{FovY: 55, Aspect: math.Float32frombits(0x3FDA740E), Near: 1, Far: 20500}
	if lo, hi := c55.band(cullWide169); math.Abs(lo-0.666) > 0.0005 || math.Abs(hi-0.888) > 0.0005 {
		t.Errorf("fovY 55: band %.4f..%.4f, want 0.666..0.888", lo, hi)
	}
	off := actors.Actor{Pos: [3]float32{-927 + 100, 0, -1000 + 50}, Status: cullStatusCull}
	if r := cullJudge(c55, cullWide169, testView, off, cullCondNoDraw); r.Band {
		t.Errorf("fovY 55: tanH %.3f is in the band", r.TanH)
	}
	// 21:9 in mode 2 (factor 1.75): the widened side plane, past 16:9's edge.
	c219 := cullClip{FovY: 60, Aspect: 1.28 * 1.75}
	if _, hi := c219.band(1.75); hi != c219.tanH() || hi < 1.29 {
		t.Errorf("21:9: band ends at %.4f, want the side plane %.4f", hi, c219.tanH())
	}
	// 21:9 in mode 0 with the hack on (factor 1.75, clipper vanilla): the
	// band ends at the 4:3 edge x 1.75, 0.739 x 1.75 = 1.293, not at 16:9's
	// 0.985, so an actor at tanH 1.1 is in it.
	if lo, hi := testClip.band(1.75); math.Abs(lo-0.739) > 0.0005 || math.Abs(hi-1.293) > 0.0005 {
		t.Errorf("mode 0, 21:9: band %.4f..%.4f, want 0.739..1.293", lo, hi)
	}
	wide := actors.Actor{Pos: [3]float32{1100 + 100, 0, -1000 + 50}, Status: cullStatusCull}
	if r := cullJudge(testClip, 1.75, testView, wide, cullCondNoDraw); !r.Band {
		t.Errorf("mode 0, 21:9: tanH %.3f is not in the band", r.TanH)
	}
	if r := cullJudge(testClip, cullWide169, testView, wide, cullCondNoDraw); r.Band {
		t.Errorf("mode 0, 16:9: tanH %.3f is in the band", r.TanH)
	}
	// cullFactor: the block's valid factor, else 16:9's.
	for _, c := range []struct {
		ws   mailbox.Widescreen
		want float64
	}{
		{mailbox.Widescreen{Present: true, Factor: 1.75}, 1.75},
		{mailbox.Widescreen{Present: true, Factor: 0}, cullWide169},
		{mailbox.Widescreen{Present: true, Factor: 4}, cullWide169},
		{mailbox.Widescreen{Factor: 1.75}, cullWide169},
	} {
		if got := cullFactor(c.ws); got != c.want {
			t.Errorf("cullFactor(%+v) = %v, want %v", c.ws, got, c.want)
		}
	}
}

func TestCullXform(t *testing.T) {
	// A 90° yaw: view x = world z, view z = -world x, plus a translation.
	m := [12]float32{0, 0, 1, 10, 0, 1, 0, 20, -1, 0, 0, 30}
	if got := cullXform(m, [3]float32{1, 2, 3}); got != [3]float64{13, 22, 29} {
		t.Errorf("got %v", got)
	}
}

func TestCullJudge(t *testing.T) {
	act := func(x, y, z float32, status uint32) actors.Actor {
		return actors.Actor{Pos: [3]float32{x + 100, y, z + 50}, Status: status}
	}
	for _, c := range []struct {
		name   string
		a      actors.Actor
		cond   uint32
		band   bool
		nodraw bool
	}{
		{"centre", act(0, 0, -1000, cullStatusCull), cullCondNoDraw, false, true},
		{"band right", act(870, 0, -1000, cullStatusCull), cullCondNoDraw, true, true},
		{"band left, drawn", act(-900, 0, -1000, cullStatusCull), 0, true, false},
		{"not CULL", act(870, 0, -1000, 0), cullCondNoDraw, false, true},
		{"past the visible edge", act(990, 0, -1000, cullStatusCull), cullCondNoDraw, false, true},
		{"above the screen", act(870, 600, -1000, cullStatusCull), cullCondNoDraw, false, true},
		{"past far", act(870*21, 0, -21000, cullStatusCull), cullCondNoDraw, false, true},
		{"behind", act(-870, 0, 1000, cullStatusCull), cullCondNoDraw, false, true},
	} {
		r := cullJudge(testClip, cullWide169, testView, c.a, c.cond)
		if r.Band != c.band || r.NoDraw != c.nodraw {
			t.Errorf("%s: band %v nodraw %v (tanH %.3f tanV %.3f z %.0f), want %v %v", c.name, r.Band, r.NoDraw, r.TanH, r.TanV, r.View[2], c.band, c.nodraw)
		}
	}
}

// TestCullProbe runs the probe over a fake game's actor queue: one band
// actor culled while visible fails it, and it prints the summary line.
func TestCullProbe(t *testing.T) {
	_, m, _ := moblinRoom(t)
	w, err := actors.Read(m, 0)
	if err != nil || len(w.Actors) < 3 {
		t.Fatalf("walk: %d actors, %v", len(w.Actors), err)
	}
	store(t, m, cullClipper+cullClipOffFovY, f32b(testClip.FovY, testClip.Aspect, testClip.Near, testClip.Far))
	store(t, m, cullViewMtx, f32b(testView[:]...))
	store(t, m, mailbox.WsKnobAddr, u32b(mailbox.WsKnobVanilla))
	put := func(a actors.Actor, x, z float32, nodraw bool) {
		store(t, m, a.Addr+addr.ActorOffCurPos, f32b(x+100, 0, z+50))
		store(t, m, a.Addr+addr.ActorOffStatus, u32b(cullStatusCull))
		var c uint32
		if nodraw {
			c = cullCondNoDraw
		}
		store(t, m, a.Addr+cullOffCondition, u32b(c))
	}
	for _, a := range w.Actors {
		put(a, 0, -1000, false) // the rest sit drawn in the middle
	}
	put(w.Actors[0], 870, -1000, false)
	put(w.Actors[1], -1760, -2000, false)

	var out strings.Builder
	bad, err := cullProbe(&out, m, 0, func() {})
	if err != nil || bad != 0 {
		t.Fatalf("all drawn: bad %d, %v\n%s", bad, err, out.String())
	}
	if !strings.Contains(out.String(), "band: 2 actors, 0 NODRAW") {
		t.Errorf("no summary:\n%s", out.String())
	}

	put(w.Actors[1], -1760, -2000, true)
	out.Reset()
	bad, err = cullProbe(&out, m, 1, func() {})
	if err != nil || bad != 2 {
		t.Fatalf("one culled, two samples: bad %d, %v\n%s", bad, err, out.String())
	}
	for _, s := range []string{"band: 2 actors, 1 NODRAW", "culled while visible", "side-plane tan 0.739", "no widescreen block"} {
		if !strings.Contains(out.String(), s) {
			t.Errorf("no %q:\n%s", s, out.String())
		}
	}
}
