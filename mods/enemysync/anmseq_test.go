package enemysync

import (
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// The BCKs the vectors use (Mo2.h:64-119) and their pointers in
// resFixture's table.
const (
	anmBckAtate2 = 0x46 // ATATE2, a once-anim (NONE)
	anmBckNajab  = 0x61 // NAJAB, all three bare-handed punch phases
	anmBckKWait  = addr.Mo2BckKWait
	anmBckNBWait = addr.Mo2BckNBWait
	anmBckWait   = addr.Mo2BckWait
	anmBckWalk   = addr.Mo2BckLast // WALK
)

func anmPtr(bck uint8) uint32 { return resBckBase + uint32(bck-addr.Mo2BckFirst)*resBckStep }

// anmStep is one 16 ms sample of a Moblin's morf and the AnmSeq and Morf
// the detector must give it.
type anmStep struct {
	bck            uint8
	attr           uint8
	frame, rate    float32
	curMorf, step  float32
	wantSeq, wantM uint8
}

func (a anmStep) sample() Sample {
	return Sample{
		Slot:     Slot{AnmAttr: a.attr, MorfFrame: a.frame, MorfRate: a.rate},
		MorfOK:   true,
		Anm:      anmPtr(a.bck),
		CurMorf:  a.curMorf,
		MorfStep: a.step,
	}
}

// anmRun feeds steps to tr in order and checks each sample's AnmSeq and
// Morf.
func anmRun(t *testing.T, tr *AnmTrack, steps []anmStep) {
	t.Helper()
	for i, st := range steps {
		s := st.sample()
		tr.Update(&s)
		if s.AnmSeq != st.wantSeq || s.Morf != st.wantM {
			t.Errorf("step %d (bck 0x%02X attr %d frame %g rate %g curMorf %g): seq %d morf %d, want seq %d morf %d",
				i, st.bck, st.attr, st.frame, st.rate, st.curMorf, s.AnmSeq, s.Morf, st.wantSeq, st.wantM)
		}
		if s.AnmAttr != st.attr {
			t.Errorf("step %d: AnmAttr %d changed to %d", i, st.attr, s.AnmAttr)
		}
	}
}

// Morf steps as setMorf leaves them (1/morf, m_Do_ext.cpp:1452-1460) for
// anm_init's arguments: 15 WAIT, 10 WALK, 5 attacks.
var (
	anmStep15 = float32(1) / 15
	anmStep10 = float32(1) / 10
	anmStep5  = float32(1) / 5
)

const (
	anmNone = AnmAttrNone
	anmLoop = AnmAttrLoop
)

// TestAnmSeqPointerChange: WAIT looping, then wait->walk (anm_init WALK,
// morf 10). The first sample after setAnm already has one play() in it,
// so mCurMorf is one step up from 0; the blend then rises (each game
// frame sampled twice at 60 Hz) and Morf holds 10 throughout.
func TestAnmSeqPointerChange(t *testing.T) {
	s15 := anmStep15
	var tr AnmTrack
	anmRun(t, &tr, []anmStep{
		{anmBckWait, anmLoop, 12, 1, 1, s15, 0, 0},
		{anmBckWait, anmLoop, 12, 1, 1, s15, 0, 0},
		{anmBckWait, anmLoop, 13, 1, 1, s15, 0, 0},
		{anmBckWalk, anmLoop, 1, 1, 0.1, anmStep10, 1, 10},
		{anmBckWalk, anmLoop, 1, 1, 0.1, anmStep10, 1, 10},
		{anmBckWalk, anmLoop, 2, 1, 0.2, anmStep10, 1, 10},
		{anmBckWalk, anmLoop, 2, 1, 0.2, anmStep10, 1, 10},
		{anmBckWalk, anmLoop, 3, 1, 0.3, anmStep10, 1, 10},
		{anmBckWalk, anmLoop, 11, 1, 1, anmStep10, 1, 10},
		{anmBckWalk, anmLoop, 11, 1, 1, anmStep10, 1, 10},
		// walk->wait (morf 15): the step at the restart is what Morf
		// reports, then it holds after the blend completes.
		{anmBckWait, anmLoop, 1, 1, s15, s15, 2, 15},
		{anmBckWait, anmLoop, 2, 1, 2 * s15, s15, 2, 15},
		{anmBckWait, anmLoop, 16, 1, 1, s15, 2, 15},
		{anmBckWait, anmLoop, 17, 1, 1, s15, 2, 15},
	})
}

// TestAnmSeqAttrChange: the same bck switching from LOOP to NONE (what
// enemy_ice's setPlayMode(EMode_NONE) + setPlaySpeed(3) does,
// d_a_mo2.cpp:3380-3388) is a restart, with no blend.
func TestAnmSeqAttrChange(t *testing.T) {
	var tr AnmTrack
	anmRun(t, &tr, []anmStep{
		{anmBckKWait, anmLoop, 20, 1, 1, anmStep15, 0, 0},
		{anmBckKWait, anmLoop, 21, 1, 1, anmStep15, 0, 0},
		{anmBckKWait, anmNone, 24, 3, 1, anmStep15, 1, 0},
		{anmBckKWait, anmNone, 27, 3, 1, anmStep15, 1, 0},
		{anmBckKWait, anmLoop, 28, 1, 1, anmStep15, 2, 0},
	})
}

// TestAnmSeqCurMorfDrop: wait_set re-setting the WAIT loop it is already
// on (morf 15) shows only as mCurMorf dropping; the frame may be anywhere
// and the loop never counts a frame reset. A repeated mCurMorf (the same
// game frame sampled twice) is not a drop.
func TestAnmSeqCurMorfDrop(t *testing.T) {
	s15 := anmStep15
	var tr AnmTrack
	anmRun(t, &tr, []anmStep{
		{anmBckWait, anmLoop, 30, 1, 1, s15, 0, 0},
		{anmBckWait, anmLoop, 1, 1, s15, s15, 1, 15},
		{anmBckWait, anmLoop, 1, 1, s15, s15, 1, 15},
		{anmBckWait, anmLoop, 2, 1, 2 * s15, s15, 1, 15},
		// re-set again mid-blend: mCurMorf falls from 2/15 to 1/15.
		{anmBckWait, anmLoop, 1, 1, s15, s15, 2, 15},
		{anmBckWait, anmLoop, 2, 1, 2 * s15, s15, 2, 15},
	})
}

// TestAnmSeqNoneEnd: a NONE once-anim runs out (J3DFrameCtrl::update
// leaves frame end-0.001 and rate 0, facts anim-table), stays stopped,
// and is re-set on the same bck as a snap: only the frame reset shows it.
func TestAnmSeqNoneEnd(t *testing.T) {
	var tr AnmTrack
	anmRun(t, &tr, []anmStep{
		{anmBckAtate2, anmNone, 17, 1, 1, anmStep5, 0, 0},
		{anmBckAtate2, anmNone, 18, 1, 1, anmStep5, 0, 0},
		{anmBckAtate2, anmNone, 18, 1, 1, anmStep5, 0, 0},
		{anmBckAtate2, anmNone, 18.999, 0, 1, anmStep5, 0, 0},
		{anmBckAtate2, anmNone, 18.999, 0, 1, anmStep5, 0, 0},
		{anmBckAtate2, anmNone, 18.999, 0, 1, anmStep5, 0, 0},
		{anmBckAtate2, anmNone, 1, 1, 1, anmStep5, 1, 0},
		{anmBckAtate2, anmNone, 2, 1, 1, anmStep5, 1, 0},
		// a frame step under 0.5 back is not a reset.
		{anmBckAtate2, anmNone, 1.6, 1, 1, anmStep5, 1, 0},
	})
}

// TestAnmSeqParry: a parry turns the attack around on the same bck with
// rate -1. In place (setPlaySpeed only) the frame keeps falling from
// where it was and nothing restarts, down to the stop at the start.
// Started from the end (setAnm with a negative speed sets the frame to
// mEnd) it is one restart, and none while it descends.
func TestAnmSeqParry(t *testing.T) {
	t.Run("in place", func(t *testing.T) {
		var tr AnmTrack
		anmRun(t, &tr, []anmStep{
			{anmBckAtate2, anmNone, 6, 1, 1, anmStep5, 0, 0},
			{anmBckAtate2, anmNone, 6, 1, 1, anmStep5, 0, 0},
			{anmBckAtate2, anmNone, 7, 1, 1, anmStep5, 0, 0},
			{anmBckAtate2, anmNone, 6, -1, 1, anmStep5, 0, 0},
			{anmBckAtate2, anmNone, 6, -1, 1, anmStep5, 0, 0},
			{anmBckAtate2, anmNone, 5, -1, 1, anmStep5, 0, 0},
			{anmBckAtate2, anmNone, 5, -1, 1, anmStep5, 0, 0},
			{anmBckAtate2, anmNone, 1, -1, 1, anmStep5, 0, 0},
			{anmBckAtate2, anmNone, 0, 0, 1, anmStep5, 0, 0},
			{anmBckAtate2, anmNone, 0, 0, 1, anmStep5, 0, 0},
		})
	})
	t.Run("from the end", func(t *testing.T) {
		var tr AnmTrack
		anmRun(t, &tr, []anmStep{
			{anmBckAtate2, anmNone, 7, 1, 1, anmStep5, 0, 0},
			{anmBckAtate2, anmNone, 8, 1, 1, anmStep5, 0, 0},
			{anmBckAtate2, anmNone, 18, -1, 1, anmStep5, 1, 0},
			{anmBckAtate2, anmNone, 18, -1, 1, anmStep5, 1, 0},
			{anmBckAtate2, anmNone, 17, -1, 1, anmStep5, 1, 0},
			{anmBckAtate2, anmNone, 17, -1, 1, anmStep5, 1, 0},
			{anmBckAtate2, anmNone, 16, -1, 1, anmStep5, 1, 0},
			{anmBckAtate2, anmNone, 2, -1, 1, anmStep5, 1, 0},
			{anmBckAtate2, anmNone, 0, 0, 1, anmStep5, 1, 0},
			{anmBckAtate2, anmNone, 0, 0, 1, anmStep5, 1, 0},
		})
	})
	t.Run("defence to the stop", func(t *testing.T) {
		// BWALKFB at speed -1 from the end (d_a_mo2.cpp:2213): the last
		// step clamps to the start with rate 0, which is play, not a
		// reset; a speed-0 re-set back to the start still is one.
		var tr AnmTrack
		anmRun(t, &tr, []anmStep{
			{anmBckAtate2, anmNone, 2.999, -1, 1, anmStep5, 0, 0},
			{anmBckAtate2, anmNone, 1.999, -1, 1, anmStep5, 0, 0},
			{anmBckAtate2, anmNone, 0.999, -1, 1, anmStep5, 0, 0},
			{anmBckAtate2, anmNone, 0, 0, 1, anmStep5, 0, 0},
			{anmBckAtate2, anmNone, 0, 0, 1, anmStep5, 0, 0},
			{anmBckAtate2, anmNone, 12, 1, 1, anmStep5, 0, 0},
			{anmBckAtate2, anmNone, 0, 0, 1, anmStep5, 1, 0},
		})
	})
}

// TestAnmSeqNajab: NAJAB's three phases are one bck (d_a_mo2.cpp:1994):
// the first comes in from NBWAIT with morf 5, the next two chain as
// snaps (morf 0, mCurMorf stays 1) whose only trace is the frame reset.
// One restart per phase, Morf 5, 0, 0.
func TestAnmSeqNajab(t *testing.T) {
	s5 := anmStep5
	var tr AnmTrack
	anmRun(t, &tr, []anmStep{
		{anmBckNBWait, anmLoop, 9, 1, 1, anmStep15, 0, 0},
		{anmBckNajab, anmNone, 1, 1, s5, s5, 1, 5},
		{anmBckNajab, anmNone, 1, 1, s5, s5, 1, 5},
		{anmBckNajab, anmNone, 2, 1, 2 * s5, s5, 1, 5},
		{anmBckNajab, anmNone, 6, 1, 1, s5, 1, 5},
		{anmBckNajab, anmNone, 9, 1, 1, s5, 1, 5},
		{anmBckNajab, anmNone, 9, 1, 1, s5, 1, 5},
		{anmBckNajab, anmNone, 1, 1, 1, s5, 2, 0},
		{anmBckNajab, anmNone, 1, 1, 1, s5, 2, 0},
		{anmBckNajab, anmNone, 2, 1, 1, s5, 2, 0},
		{anmBckNajab, anmNone, 8, 1, 1, s5, 2, 0},
		{anmBckNajab, anmNone, 1, 1, 1, s5, 3, 0},
		{anmBckNajab, anmNone, 2, 1, 1, s5, 3, 0},
		{anmBckNajab, anmNone, 14, 1, 1, s5, 3, 0},
		{anmBckNajab, anmNone, 14.999, 0, 1, s5, 3, 0},
		{anmBckNBWait, anmLoop, 1, 1, anmStep15, anmStep15, 4, 15},
	})
}

// TestAnmSeqLoopWrap: WALK (frames 0..29, LOOP) sampled at 60 Hz on a
// 30 Hz game: every frame shows twice and the wrap from 29 to 0 is not a
// restart, nor a wrap the other way at rate -1.
func TestAnmSeqLoopWrap(t *testing.T) {
	var steps []anmStep
	for _, f := range []float32{26, 26, 27, 27, 28, 28, 29, 29, 0, 0, 1, 1, 2, 28.5, 29.25, 0.75} {
		steps = append(steps, anmStep{anmBckWalk, anmLoop, f, 1, 1, anmStep10, 0, 0})
	}
	for _, f := range []float32{2, 2, 1, 1, 0, 0, 29, 29, 28} {
		steps = append(steps, anmStep{anmBckWalk, anmLoop, f, -1, 1, anmStep10, 0, 0})
	}
	var tr AnmTrack
	anmRun(t, &tr, steps)
}

// TestAnmSeqMorfOf: Morf is anm_init's argument, round(1/step), only
// while the blend runs; a snap (mCurMorf 1) is 0 whatever the stale step.
func TestAnmSeqMorfOf(t *testing.T) {
	cases := []struct {
		curMorf, step float32
		want          uint8
	}{
		{anmStep15, anmStep15, 15},
		{0, anmStep15, 15},
		{0.1, anmStep10, 10},
		{0.2, anmStep5, 5},
		{0, float32(1) / 3, 3},
		{1, anmStep15, 0},
		{1, 1, 0},
		{0.5, 0, 0},
		{0.5, -0.1, 0},
		{0.5, 2, 1},
		{0.001, 0.001, 255},
	}
	for _, c := range cases {
		if got := morfOf(c.curMorf, c.step); got != c.want {
			t.Errorf("morfOf(%g, %g) = %d, want %d", c.curMorf, c.step, got, c.want)
		}
	}
}

// TestAnmSeqMorfUnreadable: a sample whose morf span could not be read is
// AnmAttrUnknown with the held seq and morf, and does not move the track:
// the next good sample continuing the last good one is no restart.
func TestAnmSeqMorfUnreadable(t *testing.T) {
	var tr AnmTrack
	anmRun(t, &tr, []anmStep{
		{anmBckWait, anmLoop, 5, 1, 1, anmStep15, 0, 0},
		{anmBckWalk, anmLoop, 1, 1, 0.1, anmStep10, 1, 10},
	})
	s := Sample{Slot: Slot{AnmAttr: anmLoop, MorfFrame: 0}}
	tr.Update(&s)
	if s.AnmAttr != AnmAttrUnknown || s.AnmSeq != 1 || s.Morf != 10 {
		t.Errorf("unreadable: attr 0x%02X seq %d morf %d, want 0xFF 1 10", s.AnmAttr, s.AnmSeq, s.Morf)
	}
	anmRun(t, &tr, []anmStep{
		{anmBckWalk, anmLoop, 2, 1, 0.3, anmStep10, 1, 10},
	})
}

// TestAnmSeqWraps: AnmSeq is a byte and wraps.
func TestAnmSeqWraps(t *testing.T) {
	var tr AnmTrack
	for i := range 257 {
		bck := uint8(anmBckWait)
		if i%2 == 1 {
			bck = anmBckWalk
		}
		s := anmStep{bck, anmLoop, 1, 1, 1, anmStep15, 0, 0}.sample()
		tr.Update(&s)
		if want := uint8(i); s.AnmSeq != want {
			t.Fatalf("restart %d: seq %d, want %d", i, s.AnmSeq, want)
		}
	}
}

// TestTrackerDerive: Bck through the table, a track per Moblin, and Keep
// dropping the tracks of Moblins the walk no longer found (a pid that
// comes back starts over at seq 0).
func TestTrackerDerive(t *testing.T) {
	m, _ := resFixture(t)
	var tk Tracker
	if err := tk.Res.Resolve(m); err != nil {
		t.Fatal(err)
	}
	t0 := time.Unix(1_700_000_000, 0)
	derive := func(pid uint32, st anmStep, at time.Duration, wantBck uint8) Sample {
		t.Helper()
		s := st.sample()
		if err := tk.Derive(m, pid, &s, t0.Add(at)); err != nil {
			t.Fatal(err)
		}
		if s.Bck != wantBck || s.AnmSeq != st.wantSeq || s.Morf != st.wantM {
			t.Errorf("pid %d +%v: bck 0x%02X seq %d morf %d, want 0x%02X %d %d",
				pid, at, s.Bck, s.AnmSeq, s.Morf, wantBck, st.wantSeq, st.wantM)
		}
		return s
	}
	const a, b = 0x1F3, 0x1F4
	derive(a, anmStep{anmBckWait, anmLoop, 3, 1, 1, anmStep15, 0, 0}, 0, anmBckWait)
	derive(b, anmStep{anmBckKWait, anmLoop, 8, 1, 1, anmStep15, 0, 0}, 0, anmBckKWait)
	derive(a, anmStep{anmBckWalk, anmLoop, 1, 1, 0.1, anmStep10, 1, 10}, 16*time.Millisecond, anmBckWalk)
	derive(b, anmStep{anmBckKWait, anmLoop, 9, 1, 1, anmStep15, 0, 0}, 16*time.Millisecond, anmBckKWait)
	derive(a, anmStep{anmBckAtate2, anmNone, 1, 1, 0.2, anmStep5, 2, 5}, 33*time.Millisecond, anmBckAtate2)

	// An unreadable morf: no Bck, no table lookup.
	s := Sample{Slot: Slot{AnmAttr: anmLoop}, Anm: 0x8150_0040}
	if err := tk.Derive(m, b, &s, t0.Add(50*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if s.Bck != addr.Mo2BckNone || s.AnmAttr != AnmAttrUnknown || s.AnmSeq != 0 || tk.Res.Stats.BckMiss != 0 {
		t.Errorf("unreadable: bck 0x%02X attr 0x%02X seq %d misses %d", s.Bck, s.AnmAttr, s.AnmSeq, tk.Res.Stats.BckMiss)
	}

	// A stray mpAnm: 0xFF, counted, the restart detector still runs.
	stray := anmStep{anmBckWait, anmLoop, 4, 1, 1, anmStep15, 1, 0}.sample()
	stray.Anm = 0x8150_0040
	if err := tk.Derive(m, b, &stray, t0.Add(66*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if stray.Bck != addr.Mo2BckNone || stray.AnmSeq != 1 || tk.Res.Stats.BckMiss != 1 || tk.Res.Stats.MissReads != 1 {
		t.Errorf("stray: bck 0x%02X seq %d stats %+v", stray.Bck, stray.AnmSeq, tk.Res.Stats)
	}

	tk.Keep([]uint32{a, 0x1F9})
	if len(tk.tracks) != 1 || tk.tracks[a] == nil {
		t.Fatalf("after Keep: %d tracks", len(tk.tracks))
	}
	derive(a, anmStep{anmBckAtate2, anmNone, 2, 1, 0.4, anmStep5, 2, 5}, 83*time.Millisecond, anmBckAtate2)
	derive(b, anmStep{anmBckWait, anmLoop, 5, 1, 1, anmStep15, 0, 0}, 83*time.Millisecond, anmBckWait)
	tk.Keep(nil)
	if len(tk.tracks) != 0 {
		t.Errorf("Keep(nil) left %d tracks", len(tk.tracks))
	}
}
