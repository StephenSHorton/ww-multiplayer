package feed

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/host"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// xscaleEvery is how often the name tags' Widescreen Hack scale is
// measured again: the player may resize the window or change the setting.
const xscaleEvery = 2 * time.Second

// xscaleProcRetry is how long a failed look-up of the Dolphin's process
// (its user folder and command line) waits before the next: listing every
// process is not for the 60 Hz loop's every tick.
const xscaleProcRetry = 30 * time.Second

// xscaleSampler measures how much Dolphin's Widescreen Hack narrows the 3D
// picture against the 2D layer the name tags are drawn in
// (launch.GFXAspect.XScale), for the visuals block (#1157). It finds the
// Dolphin's user folder and command line once, then re-reads its graphics
// settings and render size every xscaleEvery. WW_TAG_XSCALE=<f> pins the
// scale (tests; off = 1).
type xscaleSampler struct {
	pid uint32
	// proc finds pid's user folder and command line; size its render size.
	proc func(pid uint32) (userDir string, args []string, ok bool)
	size func(pid uint32) (w, h int, ok bool)

	procOK    bool
	procRetry time.Time
	userDir   string
	args      []string
	next      time.Time
	scale     float64
	why       string
}

func newXScaleSampler(pid uint32) *xscaleSampler {
	return &xscaleSampler{pid: pid, proc: dolphinProc, size: host.RenderSize, scale: 1}
}

// dolphinProc is pid's user folder (launch.ProcessUserDir) and arguments.
func dolphinProc(pid uint32) (string, []string, bool) {
	for _, p := range launch.Processes() {
		if p.PID != pid {
			continue
		}
		dir, ok := launch.ProcessUserDir(launch.Host(), p)
		return dir, p.Args, ok
	}
	return "", nil, false
}

// Scale is the current scale (1 = none), measured again when due; a
// change is logged once.
func (s *xscaleSampler) Scale(now time.Time, rep log.Reporter) float64 {
	if now.Before(s.next) {
		return s.scale
	}
	s.next = now.Add(xscaleEvery)
	scale, why := s.measure(now)
	if scale != s.scale || why != s.why {
		if scale != s.scale {
			if scale < 1 {
				log.Logf(rep, log.Info, "name tags: %s, so they are drawn at %.2f of their x to stay on the players", why, scale)
			} else if s.why != "" {
				log.Logf(rep, log.Info, "name tags: %s, drawn as they are", why)
			}
		}
		s.scale, s.why = scale, why
	}
	return s.scale
}

func (s *xscaleSampler) measure(now time.Time) (float64, string) {
	if v := strings.TrimSpace(os.Getenv("WW_TAG_XSCALE")); v != "" {
		if strings.EqualFold(v, "off") {
			return 1, "WW_TAG_XSCALE=off"
		}
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 && f <= 1 {
			return f, fmt.Sprintf("WW_TAG_XSCALE=%v", f)
		}
	}
	if !s.procOK {
		if now.Before(s.procRetry) {
			return 1, "Dolphin's settings unknown"
		}
		s.userDir, s.args, s.procOK = s.proc(s.pid)
		if !s.procOK {
			s.procRetry = now.Add(xscaleProcRetry)
			return 1, "Dolphin's settings unknown"
		}
	}
	a := launch.ReadGFXAspect(s.userDir, s.args)
	w, h := 0, 0
	if a.Hack && a.Mode == launch.AspectStretch {
		w, h, _ = s.size(s.pid)
	}
	return a.XScale(w, h)
}
