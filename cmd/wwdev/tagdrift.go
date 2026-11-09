package main

// tagdrift: the measured name-tag check (#1165, from #1157's real
// session). The rig's V4 check judged the tag by eye with the remote near
// the screen centre, where the Widescreen Hack's drift, (1 - xscale) times
// the remote's distance from the centre, is about zero; the 11-08 player
// saw it at the side of a Stretch window. This puts the remote well off
// centre in the observer's (A's) picture and measures, in screenshots,
// the tag's x against the body's x (tagdrift_measure.go).
//
// One run is one graphics case: Dolphin caches GFX.ini at start, so the
// case is whatever A was launched with (set AspectRatio and
// wideScreenHack in A's user dir before dolphin2). It attaches to two
// running test Dolphins with mp-local NOT running, then:
//
//  1. reads A's camera and places B's mini-Link --dist ahead and --side
//     across in the camera's frame (WW_LINK2_OFFSET_*, B never moves);
//  2. captures frame N with no remote, F with mp-local as built, and O
//     with mp-local under WW_TAG_XSCALE=off, which is exactly the tag
//     before PR #329;
//  3. checks the scale Go wrote to the visuals block against the scale
//     the case should have (launch.GFXAspect.XScale) and, with the hack
//     on, against the picture's own aspect;
//  4. passes F when the tag sits over the body (ratio within 10%) and
//     requires O to fail by the predicted 1/xscale wherever the fix moves
//     the tag: the proof the check catches the 11-08 drift.
//
// Report: logs/tagdrift/<case>/tagdrift.json (+ the frames); exit 0
// pass, 1 a check failed, 2 it could not run.

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/host"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player"
)

const tagDriftUsage = "tagdrift [--case NAME] [--side 250] [--dist 400] [--names A,B] [--settle 5s] [--no-old] [--out FILE] [--ready-timeout 120s] [-v] | tagdrift --replay DIR"

type tagDriftOpts struct {
	label        string
	side, dist   float64
	nameA, nameB string
	settle       time.Duration
	noOld        bool
	out          string
	readyTimeout time.Duration
	verbose      bool
	replay       string
}

func parseTagDriftArgs(args []string) (tagDriftOpts, error) {
	o := tagDriftOpts{}
	fs := flag.NewFlagSet("tagdrift", flag.ContinueOnError)
	fs.SetOutput(new(strings.Builder))
	fs.StringVar(&o.label, "case", "", "")
	fs.Float64Var(&o.side, "side", 250, "")
	fs.Float64Var(&o.dist, "dist", 400, "")
	names := fs.String("names", "PlayerA,PlayerB", "")
	fs.DurationVar(&o.settle, "settle", 5*time.Second, "")
	fs.BoolVar(&o.noOld, "no-old", false, "")
	fs.StringVar(&o.out, "out", "", "")
	fs.DurationVar(&o.readyTimeout, "ready-timeout", 120*time.Second, "")
	fs.BoolVar(&o.verbose, "v", false, "")
	fs.StringVar(&o.replay, "replay", "", "")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() > 0 {
		return o, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	a, b, ok := strings.Cut(*names, ",")
	if !ok || a == "" || b == "" || a == b {
		return o, fmt.Errorf("--names wants two different names, A,B")
	}
	o.nameA, o.nameB = a, b
	if math.Abs(o.side) < 50 {
		return o, fmt.Errorf("--side %.0f is too near the centre to show drift", o.side)
	}
	if o.dist < 150 || o.dist > 3000 {
		return o, fmt.Errorf("--dist %.0f is outside 150..3000", o.dist)
	}
	if o.settle < 3*time.Second {
		return o, fmt.Errorf("--settle %s: give the 2 s scale sampler at least 3 s", o.settle)
	}
	o.label = strings.TrimSpace(o.label)
	return o, nil
}

// tagDriftReport is tagdrift-<case>.json.
type tagDriftReport struct {
	Started  time.Time       `json:"started"`
	Ended    time.Time       `json:"ended"`
	Case     string          `json:"case"`
	Outcome  string          `json:"outcome"`
	ExitCode int             `json:"exit_code"`
	Args     []string        `json:"args"`
	PIDs     [2]uint32       `json:"pids"`
	Graphics map[string]any  `json:"graphics"`
	Camera   map[string]any  `json:"camera,omitempty"`
	Offset   [3]float64      `json:"link2_offset"`
	Measure  tagDriftMeasure `json:"measure"`
	Checks   []smokeCheck    `json:"checks"`
	Failed   []string        `json:"failed,omitempty"`
	Shots    []string        `json:"shots,omitempty"`
}

type tagDriftRun struct {
	o      tagDriftOpts
	rep    log.Reporter
	ctx    context.Context
	exe    string
	report tagDriftReport
	a, b   *emu.Dolphin
	cannot string
	dir    string // where the report and shots go
}

func (r *tagDriftRun) add(cs ...smokeCheck) {
	for _, c := range cs {
		c.Phase = "tagdrift"
		r.report.Checks = append(r.report.Checks, c)
		if c.Pass {
			log.Logf(r.rep, log.OK, "PASS %s: %s", c.Name, c.Detail)
		} else {
			log.Logf(r.rep, log.Err, "FAIL %s: %s", c.Name, c.Detail)
		}
	}
}

// runTagDrift is `tagdrift`.
func runTagDrift(_ string, args []string) {
	o, err := parseTagDriftArgs(args)
	if err != nil {
		exitUsage(err, tagDriftUsage)
	}
	r := &tagDriftRun{o: o, rep: log.Stdout{}}
	r.report = tagDriftReport{Started: time.Now(), Args: args}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	r.ctx = ctx
	if o.replay != "" {
		r.replay(o.replay)
		r.o.out = filepath.Join(o.replay, "replay.json")
	} else {
		r.run()
	}
	os.Exit(r.finish())
}

func (r *tagDriftRun) run() {
	var err error
	if r.exe, err = os.Executable(); err != nil {
		r.cannot = err.Error()
		return
	}
	pidA, pidB, err := smokePIDs(os.Getenv)
	if err != nil {
		r.cannot = err.Error()
		return
	}
	r.report.PIDs = [2]uint32{pidA, pidB}
	paths := launch.FromEnv()
	recs := readLaunchRecords(devLogsRootOr(os.Getenv))
	if why := attachRefusal([2]uint32{pidA, pidB}, recs, os.Getenv, smokeGuardDirs(launch.Host(), paths.Exe), processStart); why != "" {
		r.cannot = why
		return
	}
	if err := waitForReady(r.ctx, r.rep, pidA, pidB, r.o.readyTimeout); err != nil {
		r.cannot = err.Error()
		return
	}
	if r.a, err = emu.FindByPID(pidA, "GZLE01"); err != nil {
		r.cannot = err.Error()
		return
	}
	defer r.a.Close()
	if r.b, err = emu.FindByPID(pidB, "GZLE01"); err != nil {
		r.cannot = err.Error()
		return
	}
	defer r.b.Close()
	for i, d := range []*emu.Dolphin{r.a, r.b} {
		c, err := wwapi.CheckContract(d, wwapi.ContractOptions{})
		if err != nil || !c.WritesAllowed {
			why := "contract check failed"
			if err != nil {
				why = err.Error()
			} else {
				why = wwapi.ContractLine(c)
			}
			r.cannot = fmt.Sprintf("Dolphin %c: %s", 'A'+i, why)
			return
		}
	}
	if s := readGameSample(r.a); s.ShadowMode == avatar.ModePoseFeed {
		r.cannot = "A's pose feed is armed: stop mp-local (tagdrift runs its own, and frame N needs no remote)"
		return
	}

	// The case: A's graphics settings, read the way puppet-sync reads
	// them (feed's xscaleSampler), and the scale they should give.
	userDir, dargs, ok := dolphinUserDirArgs(pidA)
	if !ok {
		r.cannot = fmt.Sprintf("can't read Dolphin A's (pid %d) user folder and command line", pidA)
		return
	}
	asp := launch.ReadGFXAspect(userDir, dargs)
	rw, rh, _ := host.RenderSize(pidA)
	xs, why := asp.XScale(rw, rh)
	if r.o.label == "" {
		r.o.label = caseLabel(asp)
	}
	r.report.Case = r.o.label
	r.report.Graphics = map[string]any{"user_dir": userDir, "hack": asp.Hack, "mode": asp.Mode, "render": []int{rw, rh}, "xscale": xs, "why": why}
	log.Logf(r.rep, log.Info, "Case %s: %s (scale %.3f); render surface %dx%d", r.o.label, why, xs, rw, rh)

	// Not logs/latest: each mp-local run rotates it.
	if root := devLogsRootOr(os.Getenv); root != "" {
		r.dir = filepath.Join(root, "tagdrift", r.o.label)
		_ = os.MkdirAll(r.dir, 0o755)
	}

	// Where B's mini-Link goes: --dist ahead of A's eye and --side across,
	// at A's Link's height.
	cam, err := readCamera(r.a)
	if err != nil {
		r.cannot = "A's camera: " + err.Error()
		return
	}
	pa, err := player.NewLink(r.a).Position()
	if err != nil {
		r.cannot = "A's Link: " + err.Error()
		return
	}
	pb, err := player.NewLink(r.b).Position()
	if err != nil {
		r.cannot = "B's Link: " + err.Error()
		return
	}
	tx, tz, ok := cameraSpot(cam, r.o.dist, r.o.side)
	if !ok {
		r.cannot = "A's camera looks straight up or down"
		return
	}
	off := [3]float64{tx - float64(pb.PosX), float64(pa.PosY - pb.PosY), tz - float64(pb.PosZ)}
	r.report.Offset = off
	r.report.Camera = map[string]any{"eye": cam.eye, "center": cam.center, "fovy": cam.fovy, "spot": []float64{tx, float64(pa.PosY), tz}}
	log.Logf(r.rep, log.Info, "B's mini-Link goes to (%.0f, %.0f, %.0f) on A: offset (%.0f, %.0f, %.0f)", tx, pa.PosY, tz, off[0], off[1], off[2])

	hwnd, err := host.FindWindowByPID(pidA)
	if err != nil {
		r.cannot = "A's window: " + err.Error()
		return
	}
	// Several frames with no remote: the first is the reference, the
	// rest show what moves by itself (tagdrift_measure.go noiseMask).
	var ns []*image.RGBA
	for i := 0; i < 4; i++ {
		if i > 0 {
			sleepCtx(r.ctx, 700*time.Millisecond)
		}
		n, err := r.shot(hwnd, fmt.Sprintf("N%d", i))
		if err != nil {
			r.cannot = err.Error()
			return
		}
		ns = append(ns, n)
	}
	f, err := r.remoteShot(hwnd, "F", off, "", xs)
	if err != nil {
		r.cannot = err.Error()
		return
	}
	var o *image.RGBA
	if !r.o.noOld {
		if o, err = r.remoteShot(hwnd, "O", off, "off", 1); err != nil {
			r.cannot = err.Error()
			return
		}
	}
	if cam2, err := readCamera(r.a); err != nil || cam2.moved(cam) > 2 {
		r.cannot = "A's camera moved during the run: keep A idle"
		return
	}

	m := measureTagDrift(ns, f, o, tagDriftDefaults)
	r.report.Measure = m
	r.judge(m, asp, xs, o != nil)
}

// judge adds one case's verdicts. With the hack on and Stretch to Window
// the picture is the window, so its aspect gives the hack's scale
// independently of Go's reading of the settings and window (the 11-08
// player's case). Force 16:9 draws a picture Dolphin narrows a little
// for the GameCube's pixel aspect (1110x664 in a 1278x664 window), so
// there the picture's aspect is no measure of the scale.
func (r *tagDriftRun) judge(m tagDriftMeasure, asp launch.GFXAspect, xs float64, haveOld bool) {
	if asp.Hack && asp.Mode == launch.AspectStretch && xs < 1 {
		r.add(passIf(math.Abs(m.XScaleIm-xs) <= 0.03, r.o.label+" picture aspect",
			fmt.Sprintf("the game picture is %dx%d, so the hack's scale is %.3f; the case's is %.3f", m.Area[2]-m.Area[0], m.Area[3]-m.Area[1], m.XScaleIm, xs), nil))
	}
	checks, cannot := tagDriftVerdicts(r.o.label, m, xs, haveOld, tagDriftDefaults)
	r.add(checks...)
	if cannot != "" {
		r.cannot = cannot
	}
}

// replay recomputes a saved run's verdicts from its frames and report
// (--replay DIR): for tuning the analyser without the rig.
func (r *tagDriftRun) replay(dir string) {
	var old tagDriftReport
	b, err := os.ReadFile(filepath.Join(dir, "tagdrift.json"))
	if err == nil {
		err = json.Unmarshal(b, &old)
	}
	if err != nil {
		r.cannot = "reading the saved report: " + err.Error()
		return
	}
	load := func(name string) (*image.RGBA, error) {
		f, err := os.Open(filepath.Join(dir, name+".png"))
		if err != nil {
			return nil, err
		}
		defer f.Close()
		img, err := png.Decode(f)
		if err != nil {
			return nil, err
		}
		rgba := image.NewRGBA(img.Bounds())
		draw.Draw(rgba, rgba.Bounds(), img, img.Bounds().Min, draw.Src)
		return rgba, nil
	}
	var ns []*image.RGBA
	for i := 0; ; i++ {
		n, err := load(fmt.Sprintf("N%d", i))
		if err != nil {
			break
		}
		ns = append(ns, n)
	}
	f, err := load("F")
	if len(ns) == 0 || err != nil {
		r.cannot = "the saved run has no N0 or F frame"
		return
	}
	o, _ := load("O")
	g := old.Graphics
	num := func(k string) float64 { v, _ := g[k].(float64); return v }
	asp := launch.GFXAspect{Mode: int(num("mode"))}
	asp.Hack, _ = g["hack"].(bool)
	xs := num("xscale")
	r.o.label = old.Case
	r.report.Case, r.report.Graphics, r.report.PIDs = old.Case, old.Graphics, old.PIDs
	m := measureTagDrift(ns, f, o, tagDriftDefaults)
	r.report.Measure = m
	r.judge(m, asp, xs, o != nil)
}

// remoteShot runs mp-local with B's mini-Link at off (and WW_TAG_XSCALE
// = tagXScale when set), waits for the tag and the scale, captures A,
// and stops mp-local. wantXS is the scale the visuals block must carry.
func (r *tagDriftRun) remoteShot(hwnd uintptr, label string, off [3]float64, tagXScale string, wantXS float64) (*image.RGBA, error) {
	env := map[string]string{
		"WW_LINK2_OFFSET_X": fmt.Sprintf("%.1f", off[0]),
		"WW_LINK2_OFFSET_Y": fmt.Sprintf("%.1f", off[1]),
		"WW_LINK2_OFFSET_Z": fmt.Sprintf("%.1f", off[2]),
		"MP_LOCAL_SHIFT_X":  "0", "MP_LOCAL_SHIFT_Y": "0", "MP_LOCAL_SHIFT_Z": "0",
	}
	for k, v := range env {
		os.Setenv(k, v)
	}
	if tagXScale != "" {
		os.Setenv("WW_TAG_XSCALE", tagXScale)
	} else {
		os.Unsetenv("WW_TAG_XSCALE")
	}
	defer os.Unsetenv("WW_TAG_XSCALE")

	pre := readGameSample(r.a)
	tags0, _ := mem.ReadU32(r.a, mailbox.VisAddr+mailbox.VisStatTagsOff)
	child, err := startMpLocalChild(r.ctx, r.exe, smokeOpts{nameA: r.o.nameA, nameB: r.o.nameB, verbose: r.o.verbose}, r.report.PIDs[0], r.report.PIDs[1], r.rep)
	if err != nil {
		return nil, err
	}
	defer func() {
		if !child.stop(30 * time.Second) {
			avatar.Clear(r.a)
			avatar.Clear(r.b)
		}
	}()
	deadline := time.Now().Add(90 * time.Second)
	for !poseFeedArmed(pre, readGameSample(r.a)) {
		if !child.running() {
			return nil, fmt.Errorf("frame %s: mp-local exited before arming (%s)", label, child.exitNote())
		}
		if time.Now().After(deadline) || r.ctx.Err() != nil {
			return nil, fmt.Errorf("frame %s: mp-local did not arm A within 90 s", label)
		}
		sleepCtx(r.ctx, 250*time.Millisecond)
	}
	sleepCtx(r.ctx, r.o.settle)
	tags1, _ := mem.ReadU32(r.a, mailbox.VisAddr+mailbox.VisStatTagsOff)
	if tags1 == tags0 {
		return nil, fmt.Errorf("frame %s: the REL queued no name tag (visuals counter at +0xE0 stayed %d): is the HUD hidden or the remote out of range?", label, tags0)
	}
	flags, err := mem.ReadU32(r.a, mailbox.VisAddr+mailbox.VisFlagsOff)
	if err != nil {
		return nil, fmt.Errorf("frame %s: reading the visuals flags: %w", label, err)
	}
	got := xscaleFromFlags(flags)
	r.add(passIf(math.Abs(got-wantXS) <= 0.005, fmt.Sprintf("%s scale written (%s)", r.o.label, label),
		fmt.Sprintf("the visuals block carries scale %.4f (q16 0x%04X); %.4f wanted", got, flags>>mailbox.VisFlagsXScaleShift, wantXS),
		map[string]any{"flags": flags}))
	return r.shot(hwnd, label)
}

// xscaleFromFlags decodes the visuals flags word's top half: q16, 0 = 1.
func xscaleFromFlags(flags uint32) float64 {
	q := flags >> mailbox.VisFlagsXScaleShift
	if q == 0 {
		return 1
	}
	return float64(q) / 65536
}

// shot captures A's window, crops it to the client area (the picture
// without the title bar) and saves it beside the report.
func (r *tagDriftRun) shot(hwnd uintptr, label string) (*image.RGBA, error) {
	full, err := host.CaptureWindow(hwnd)
	if err != nil {
		return nil, fmt.Errorf("frame %s: capture: %w", label, err)
	}
	x, y, w, h, ok := host.ClientRectInWindow(hwnd)
	if !ok {
		return nil, fmt.Errorf("frame %s: can't read A's client area", label)
	}
	crop := image.Rect(x, y, x+w, y+h).Intersect(full.Bounds())
	img := image.NewRGBA(image.Rect(0, 0, crop.Dx(), crop.Dy()))
	draw.Draw(img, img.Bounds(), full, crop.Min, draw.Src)
	if r.dir != "" {
		p := filepath.Join(r.dir, label+".png")
		if err := writePNG(p, img); err != nil {
			log.Logf(r.rep, log.Warn, "saving %s: %v", p, err)
		} else {
			r.report.Shots = append(r.report.Shots, p)
		}
	}
	return img, nil
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (r *tagDriftRun) finish() int {
	rep := &r.report
	rep.Ended = time.Now()
	for _, c := range rep.Checks {
		if !c.Pass && !c.Skipped {
			rep.Failed = append(rep.Failed, c.Name)
		}
	}
	switch {
	case r.ctx.Err() != nil:
		rep.Outcome, rep.ExitCode = "interrupted", smokeExitCannot
	case r.cannot != "":
		rep.Outcome, rep.ExitCode = "cannot run: "+r.cannot, smokeExitCannot
	case len(rep.Failed) > 0:
		rep.Outcome, rep.ExitCode = "fail", smokeExitFail
	default:
		rep.Outcome, rep.ExitCode = "pass", smokeExitPass
	}
	if r.cannot != "" {
		log.Logf(r.rep, log.Err, "tagdrift could not run: %s", r.cannot)
	}
	log.Logf(r.rep, log.Info, "tagdrift %s: %s (%d checks, %d failed%s)", rep.Case, rep.Outcome, len(rep.Checks), len(rep.Failed), failedList(rep.Failed))
	var paths []string
	name := "tagdrift.json"
	if r.dir != "" {
		paths = append(paths, filepath.Join(r.dir, name))
	}
	if r.o.out != "" {
		paths = append(paths, r.o.out)
	}
	if len(paths) == 0 {
		paths = []string{name}
	}
	for _, p := range paths {
		if err := writeJSON(p, rep); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: writing %s: %v\n", p, err)
		} else {
			log.Logf(r.rep, log.Info, "Report: %s", p)
		}
	}
	return rep.ExitCode
}

// caseLabel names a graphics case for the report and file names.
func caseLabel(a launch.GFXAspect) string {
	hack := "hackoff"
	if a.Hack {
		hack = "hackon"
	}
	mode := map[int]string{
		launch.AspectAuto: "auto", launch.AspectForceWide: "force169", launch.AspectForceStandard: "force43",
		launch.AspectStretch: "stretch", launch.AspectCustom: "custom", launch.AspectCustomStretch: "customstretch",
	}[a.Mode]
	if mode == "" {
		mode = fmt.Sprintf("mode%d", a.Mode)
	}
	return hack + "-" + mode
}

// dolphinUserDirArgs is pid's user folder and command line, as the feed
// finds them for its scale (feed.dolphinProc).
func dolphinUserDirArgs(pid uint32) (string, []string, bool) {
	for _, p := range launch.Processes() {
		if p.PID != pid {
			continue
		}
		dir, ok := launch.ProcessUserDir(launch.Host(), p)
		return dir, p.Args, ok
	}
	return "", nil, false
}

// camView is a play camera's eye, centre and vertical fov.
type camView struct {
	eye, center [3]float64
	fovy        float64
}

func (c camView) moved(d camView) float64 {
	m := 0.0
	for i := 0; i < 3; i++ {
		m = math.Max(m, math.Abs(c.eye[i]-d.eye[i]))
		m = math.Max(m, math.Abs(c.center[i]-d.center[i]))
	}
	return m
}

// readCamera reads the play camera (addr.CameraPtr).
func readCamera(m mem.Reader) (camView, error) {
	var c camView
	p, err := mem.ReadU32(m, addr.CameraPtr)
	if err != nil {
		return c, err
	}
	if !addr.InMEM1(p) {
		return c, errors.New("no play camera")
	}
	v, err := m.Read(p+addr.ViewOffFovy, addr.ViewOffCenter+12-addr.ViewOffFovy)
	if err != nil {
		return c, err
	}
	f := func(off uint32) float64 {
		return float64(math.Float32frombits(binary.BigEndian.Uint32(v[off-addr.ViewOffFovy:])))
	}
	c.fovy = f(addr.ViewOffFovy)
	for i := uint32(0); i < 3; i++ {
		c.eye[i] = f(addr.ViewOffEye + 4*i)
		c.center[i] = f(addr.ViewOffCenter + 4*i)
	}
	for _, x := range append(c.eye[:], c.center[:]...) {
		if math.IsNaN(x) || math.Abs(x) > 1e7 {
			return c, errors.New("camera reads garbage")
		}
	}
	return c, nil
}

// cameraSpot is the level point dist ahead of the camera's eye along its
// view (levelled) and side to its right. ok is false for a camera that
// looks straight up or down.
func cameraSpot(c camView, dist, side float64) (x, z float64, ok bool) {
	fx, fz := c.center[0]-c.eye[0], c.center[2]-c.eye[2]
	h := math.Hypot(fx, fz)
	if h < 1e-3 {
		return 0, 0, false
	}
	fx, fz = fx/h, fz/h
	// right = forward x up (y), levelled: (-fz, fx).
	rx, rz := -fz, fx
	return c.eye[0] + fx*dist + rx*side, c.eye[2] + fz*dist + rz*side, true
}
