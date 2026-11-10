package main

// cull-probe (widescreen-culling.md sections 3.2 and 8 step 6): which
// actors the game culls at the screen's side edges. It reads mClipper, the
// 3D knob and the WWWS block, puts every actor's current.pos through
// j3dSys's view matrix and prints its tanH = x / -z with its NODRAW bit.
// A CULL actor in the side band (the strip the 4:3 clipper misses: at fovY
// 60 tanH 0.739..0.985, cullClip.band), on screen vertically and inside the far plane, must
// be drawn once the clipper hook widens mClipper; one that is NODRAW makes
// the probe exit 1.
//
//	cull-probe        one sample
//	cull-probe <secs> one sample a second for secs seconds

import (
	"fmt"
	"io"
	"math"
	"os"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

const (
	// mDoLib_clipper::mClipper, a J3DUClipper (J3DUClipper.h:26-32).
	cullClipper       uint32 = 0x803A570C
	cullClipOffFovY          = 0x4C // f32, degrees
	cullClipOffAspect        = 0x50 // f32
	cullClipOffNear          = 0x54 // f32
	cullClipOffFar           = 0x58 // f32
	// j3dSys.mViewMtx, the first 0x30 bytes: a 3x4 row-major f32 Mtx.
	cullViewMtx uint32 = 0x803EDA58

	cullOffCondition        = 0x1C8      // u32 actor_condition (f_op_actor.h:285)
	cullCondNoDraw   uint32 = 0x04       // fopAcCnd_NODRAW_e: fopAc_Draw culled it
	cullStatusCull   uint32 = 0x00000100 // fopAcStts_CULL_e (f_op_actor.h:28)

	// The vanilla clipper's aspect (4/3 x 0.96) and the 16:9 widening.
	cullAspect43 = 1.28
	cullWide169  = 4.0 / 3.0
)

// cullClip is mClipper's frustum.
type cullClip struct{ FovY, Aspect, Near, Far float32 }

// tanV is the top plane's tan: tan(fovY/2) (calcViewFrustum).
func (c cullClip) tanV() float64 { return math.Tan(float64(c.FovY) * 0.5 * math.Pi / 180) }

// tanH is the side planes' tan: tan(fovY/2) x aspect.
func (c cullClip) tanH() float64 { return c.tanV() * float64(c.Aspect) }

// band is the side band at this clipper's fovY: from the vanilla 4:3
// clipper's edge, tan(fovY/2) x 1.28, to the visible edge. That is the
// clipper's own side plane when it is widened (mode 1 under the hack, mode
// 2 by the knob; the stage-1 live run saw fovY 55, where the old fixed
// 0.985 flagged an off-screen actor), and the 4:3 edge x factor when it is
// not (mode 0 with the hack on: WS-C3's "off" half), where factor is the
// WWWS block's (cullFactor; 4/3, the 16:9 edge, without a valid one). At
// fovY 60 and 16:9 both give 0.739..0.985.
func (c cullClip) band(factor float64) (lo, hi float64) {
	lo = c.tanV() * cullAspect43
	return lo, max(c.tanH(), lo*factor)
}

// cullFactor is the aspect factor the band's visible edge follows: the
// WWWS block's when it has a valid one, else 16:9's 4/3.
func cullFactor(ws mailbox.Widescreen) float64 {
	if ws.Present && mailbox.WsFactorValid(ws.Factor) {
		return float64(ws.Factor)
	}
	return cullWide169
}

// cullXform puts p through the 3x4 row-major view matrix m.
func cullXform(m [12]float32, p [3]float32) [3]float64 {
	var v [3]float64
	for r := range 3 {
		v[r] = float64(m[4*r])*float64(p[0]) + float64(m[4*r+1])*float64(p[1]) +
			float64(m[4*r+2])*float64(p[2]) + float64(m[4*r+3])
	}
	return v
}

// cullRow is one actor as the probe judged it.
type cullRow struct {
	A          actors.Actor
	View       [3]float64 // view space: -z is ahead
	TanH, TanV float64    // x / -z, y / -z (0 behind the camera)
	Cull       bool       // fopAcStts_CULL_e
	NoDraw     bool       // fopAcCnd_NODRAW_e
	Band       bool       // a CULL actor in the side band, on screen vertically, inside the far plane
}

// cullJudge places a in view space and marks the band (factor: cullFactor). The game culls by
// a sphere or box around the actor, not its origin, so the band is judged
// by the origin only, as the 3.2 probe did.
func cullJudge(c cullClip, factor float64, m [12]float32, a actors.Actor, cond uint32) cullRow {
	r := cullRow{A: a, View: cullXform(m, a.Pos), Cull: a.Status&cullStatusCull != 0, NoDraw: cond&cullCondNoDraw != 0}
	d := -r.View[2]
	if d <= 0 {
		return r
	}
	r.TanH, r.TanV = r.View[0]/d, r.View[1]/d
	h := math.Abs(r.TanH)
	lo, hi := c.band(factor)
	r.Band = r.Cull && h >= lo && h <= hi && math.Abs(r.TanV) < c.tanV() &&
		d >= float64(c.Near) && d <= float64(c.Far)
	return r
}

// cullSample is one read of the game.
type cullSample struct {
	Clip cullClip
	Knob uint32
	WS   mailbox.Widescreen
	Mtx  [12]float32
	Walk actors.Walk
	Rows []cullRow
}

func readCullSample(m mem.Reader) (cullSample, error) {
	var s cullSample
	cb, err := m.Read(cullClipper+cullClipOffFovY, 0x10)
	if err != nil {
		return s, err
	}
	s.Clip = cullClip{
		FovY: mem.F32BE(cb[0:]), Aspect: mem.F32BE(cb[cullClipOffAspect-cullClipOffFovY:]),
		Near: mem.F32BE(cb[cullClipOffNear-cullClipOffFovY:]), Far: mem.F32BE(cb[cullClipOffFar-cullClipOffFovY:]),
	}
	if s.Knob, err = mem.ReadU32(m, mailbox.WsKnobAddr); err != nil {
		return s, err
	}
	if s.WS, err = mailbox.ReadWidescreen(m); err != nil {
		return s, err
	}
	mb, err := m.Read(cullViewMtx, 0x30)
	if err != nil {
		return s, err
	}
	for i := range s.Mtx {
		s.Mtx[i] = mem.F32BE(mb[4*i:])
	}
	if s.Walk, err = actors.Read(m, 0); err != nil {
		return s, err
	}
	for _, a := range s.Walk.Actors {
		cond, err := mem.ReadU32(m, a.Addr+cullOffCondition)
		if err != nil {
			return s, err
		}
		s.Rows = append(s.Rows, cullJudge(s.Clip, cullFactor(s.WS), s.Mtx, a, cond))
	}
	return s, nil
}

// printCullSample prints s and returns how many band actors it found and
// how many of them are NODRAW.
func printCullSample(w io.Writer, s cullSample) (band, nodraw int) {
	fmt.Fprintf(w, "mClipper: fovY %.2f aspect %.4f (0x%08X) near %.1f far %.1f; side-plane tan %.3f, top %.3f\n",
		s.Clip.FovY, s.Clip.Aspect, math.Float32bits(s.Clip.Aspect), s.Clip.Near, s.Clip.Far, s.Clip.tanH(), s.Clip.tanV())
	fmt.Fprintf(w, "knob 0x%08X: 0x%08X (vanilla 0x%08X); %s\n", mailbox.WsKnobAddr, s.Knob, mailbox.WsKnobVanilla, s.WS)
	lo, hi := s.Clip.band(cullFactor(s.WS))
	fmt.Fprintf(w, "band: |tanH| %.3f..%.3f (the 4:3 clipper's edge to the visible edge), CULL actors on screen vertically inside the far plane\n", lo, hi)
	fmt.Fprintf(w, "  %-10s %-6s %-6s %10s %7s %7s %4s %6s\n", "actor", "proc", "pid", "view z", "tanH", "tanV", "cull", "nodraw")
	for _, r := range s.Rows {
		mark := ""
		if r.Band {
			band++
			mark = "  band"
			if r.NoDraw {
				nodraw++
				mark = "  band NODRAW  <-- culled while visible"
			}
		}
		yn := func(b bool) string {
			if b {
				return "y"
			}
			return "-"
		}
		if -r.View[2] <= 0 {
			fmt.Fprintf(w, "  0x%08X 0x%04X %-6d %10.0f %7s %7s %4s %6s  behind\n", r.A.Addr, uint16(r.A.Proc), r.A.PID, r.View[2], "", "", yn(r.Cull), yn(r.NoDraw))
			continue
		}
		fmt.Fprintf(w, "  0x%08X 0x%04X %-6d %10.0f %7.3f %7.3f %4s %6s%s\n", r.A.Addr, uint16(r.A.Proc), r.A.PID, r.View[2], r.TanH, r.TanV, yn(r.Cull), yn(r.NoDraw), mark)
	}
	if s.Walk.Stop != "" {
		fmt.Fprintf(w, "walk stopped early: %s\n", s.Walk.Stop)
	}
	fmt.Fprintf(w, "band: %d actors, %d NODRAW\n", band, nodraw)
	return band, nodraw
}

// cullProbe takes samples+1 samples (one a second) and returns how many
// band actors were NODRAW across them.
func cullProbe(w io.Writer, m mem.Reader, samples int, wait func()) (int, error) {
	bad := 0
	for i := 0; i <= samples; i++ {
		if i > 0 {
			wait()
			fmt.Fprintln(w)
		}
		s, err := readCullSample(m)
		if err != nil {
			return bad, err
		}
		_, n := printCullSample(w, s)
		bad += n
	}
	return bad, nil
}

func runCullProbe(_ string, args []string) {
	const usage = "cull-probe [secs=0]"
	if len(args) > 1 {
		exitUsage(fmt.Errorf("at most one argument"), usage)
	}
	secs, err := diagIntArg(args, 0, 0, 0, 3600, "secs")
	if err != nil {
		exitUsage(err, usage)
	}
	// Exit 1 means only "culled while visible"; no game or a bad read is 2.
	d, err := findDolphin("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(2)
	}
	defer d.Close()
	bad, err := cullProbe(os.Stdout, d, secs, func() { time.Sleep(time.Second) })
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(2)
	}
	if bad > 0 {
		os.Exit(1)
	}
}
