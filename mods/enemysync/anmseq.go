package enemysync

import (
	"math"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// The host-side restart detector (design "host_side", PER-TICK
// DERIVATION): from one Moblin's raw samples it derives the slot's
// AnmSeq, a byte the host bumps whenever it must have called setAnm, and
// Morf, setAnm's own morf argument recovered from mMorfStep at that
// restart. setAnm always re-inits the frame control and calls setMorf
// (m_Do_ext.cpp:1414-1449), so a restart shows as one of:
//
//	(a) mpAnm changed;
//	(b) mAttribute changed;
//	(c) mCurMorf dropped (setMorf writes 0 when morf > 0, m_Do_ext.cpp:
//	    1456; play only chases it up, :1464-1466);
//	(d) on a non-loop attribute (not LOOP, not LOOP_REVERSE) the frame
//	    reset against the rate: rate >= 0 and frame < prev - 0.5, or
//	    rate < 0 and frame > prev + 0.5. That catches NAJAB's three
//	    phases, all on bck 0x61 (d_a_mo2.cpp:1994); a loop's wrap is never
//	    a restart, and a wait_set that re-sets the same loop costs only
//	    its blend, which (c) catches while it runs. A rate of 0 takes the
//	    previous sample's direction: a NONE anim played backwards stops
//	    at its start with rate 0 (J3DFrameCtrl::update), and that last
//	    step down (0.999 to 0, defence's BWALKFB at speed -1,
//	    d_a_mo2.cpp:2213) is play, not a reset.
//
// The client REL compares the byte to the one it last setAnm'd for and
// calls setAnm again on a change; the smoother never touches it, so a
// restart fires exactly once per host restart (the earlier REL-side bit
// fired twice on smoothed frames).

// AnmTrack is one Moblin's detector state.
type AnmTrack struct {
	have    bool
	anm     uint32
	attr    uint8
	curMorf float32
	frame   float32
	rate    float32
	seq     uint8
	morf    uint8
}

// Update takes the tick's sample and fills its AnmSeq and Morf. The
// first sample of a track starts at seq 0 with the morf the sample
// shows; a sample whose morf was unreadable is passed through with
// AnmAttr unknown and leaves the track as it was.
func (t *AnmTrack) Update(s *Sample) {
	if !s.MorfOK {
		s.AnmAttr = AnmAttrUnknown
		s.AnmSeq, s.Morf = t.seq, t.morf
		return
	}
	restart := !t.have
	if t.have {
		switch {
		case s.Anm != t.anm, s.AnmAttr != t.attr, s.CurMorf < t.curMorf:
			restart = true
		case s.AnmAttr != AnmAttrLoop && s.AnmAttr != AnmAttrLoopReverse:
			rate := s.MorfRate
			if rate == 0 {
				rate = t.rate
			}
			if (rate >= 0 && s.MorfFrame < t.frame-0.5) || (rate < 0 && s.MorfFrame > t.frame+0.5) {
				restart = true
			}
		}
	}
	if restart {
		if t.have {
			t.seq++
		}
		t.morf = morfOf(s.CurMorf, s.MorfStep)
	}
	t.have, t.anm, t.attr, t.curMorf, t.frame, t.rate = true, s.Anm, s.AnmAttr, s.CurMorf, s.MorfFrame, s.MorfRate
	s.AnmSeq, s.Morf = t.seq, t.morf
}

// morfOf recovers anm_init's morf argument at a restart: setMorf(morf)
// leaves mCurMorf 0 and mMorfStep 1/morf for morf > 0, else mCurMorf 1
// (m_Do_ext.cpp:1452-1460). A blend still running (mCurMorf < 1) with a
// positive step gives round(1/step) clamped to 1..255; anything else is 0
// (a snap: attack-phase chaining, or the blend already done).
func morfOf(curMorf, step float32) uint8 {
	if !(curMorf < 1.0) || !(step > 0) || math.IsInf(float64(step), 0) || math.IsNaN(float64(step)) {
		return 0
	}
	v := math.Round(1 / float64(step))
	if v < 1 {
		return 1
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// Tracker is what the host keeps across ticks for the animation stream:
// the Mo2 table and one AnmTrack per Moblin (by process ID).
type Tracker struct {
	Res    Mo2Res
	tracks map[uint32]*AnmTrack
}

// Derive fills s's Bck (through the table, with the miss rule), AnmSeq
// and Morf for the Moblin with process ID pid at now. A table read error
// is returned; the sample is still derived with Bck unresolved.
func (t *Tracker) Derive(m mem.Reader, pid uint32, s *Sample, now time.Time) error {
	if t.tracks == nil {
		t.tracks = map[uint32]*AnmTrack{}
	}
	tr := t.tracks[pid]
	if tr == nil {
		tr = &AnmTrack{}
		t.tracks[pid] = tr
	}
	tr.Update(s)
	s.Bck = addr.Mo2BckNone
	if !s.MorfOK {
		return nil
	}
	idx, err := t.Res.Lookup(m, s.Anm, now)
	s.Bck = idx
	return err
}

// Keep drops the tracks of every Moblin not in pids (after a walk).
func (t *Tracker) Keep(pids []uint32) {
	for pid := range t.tracks {
		found := false
		for _, p := range pids {
			if p == pid {
				found = true
				break
			}
		}
		if !found {
			delete(t.tracks, pid)
		}
	}
}
