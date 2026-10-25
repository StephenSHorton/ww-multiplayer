package gui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/internal/patch"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
)

// The setup's patch (design §2.1 step 2): patch.Run in a goroutine, its
// lines turned into a progress view (applyPatchLine) the frontend gets as
// "patch" events and from PatchStatus. Cancelling stops it before the disc
// is read; once inject.PatchISO runs it can't be interrupted, so a
// cancelled patch finishes writing and then its new output is removed,
// unless it was written over an earlier <name>-multiplayer.iso: that one
// is already replaced, so the new (complete) disc is kept and the view says
// so (PatchView.Stopped). The co-op tables step is skipped once cancelled.
// PatchISO writes a temp file and renames it, and closing the app waits for
// the patch to end with no time limit (Bridge.close, Bridge.beforeClose), so
// no half-written file is left behind.
//
// An RVZ, WIA or GCZ disc is first converted to a temporary ISO by the
// DolphinTool beside the player's Dolphin (patch.Converter, review idea
// 16): the saved Dolphin, else the one the Dolphin step would detect,
// since that step comes after this one. That is the "convert" stage, with
// its own creep; cancelling then kills DolphinTool and removes the
// temporary ISO, and nothing is left ("nothing").

// PatchRequest starts a patch.
type PatchRequest struct {
	Path string `json:"path"`
	// Patched is "I already have a patched ISO": a disc this build
	// patched is taken as it is, and anything else is refused rather than
	// patched.
	Patched bool `json:"patched"`
}

// CoopRowView is the co-op stage tables check row.
type CoopRowView struct {
	State string `json:"state"` // "" (not yet), "pending", "ok", "warn"
	Text  string `json:"text"`
}

// PatchView is a patch's progress.
type PatchView struct {
	ID int `json:"id"`
	// Seq grows with every change to the view, so the frontend can drop
	// a snapshot older than one it already shows (events and PatchStatus
	// replies may arrive out of order).
	Seq int `json:"seq"`
	// Stage is "idle", "start", "convert", "patch", "coop", "done",
	// "failed", "canceling" or "canceled".
	Stage string `json:"stage"`
	// Converted is the input's format ("RVZ", "WIA", "GCZ") once a
	// conversion to ISO started, else "".
	Converted      string      `json:"converted"`
	Running        bool        `json:"running"`
	Percent        int         `json:"percent"`
	In             string      `json:"in"`
	Out            string      `json:"out"`
	Image          string      `json:"image"` // the disc to boot, once done
	AlreadyPatched bool        `json:"alreadyPatched"`
	Coop           CoopRowView `json:"coop"`
	Lines          []string    `json:"lines"`
	Error          string      `json:"error"`
	// Stopped says what a cancelled patch left: "nothing" (stopped
	// before anything was written), "removed" (the new disc was finished,
	// then removed), "replaced" (it was written over an earlier patched
	// disc at Out, which is gone, so the new one is kept) or "kept" (it
	// couldn't be removed). "" unless Stage is "canceled".
	Stopped string `json:"stopped"`
}

// maxPatchLines caps PatchView.Lines.
const maxPatchLines = 200

// patchCreep is how fast the bar fills while the disc is read, patched
// and written, which says nothing until it is done: it nears 85% with this
// time constant (a 1.4 GB disc takes 5-20 s on an SSD).
const patchCreep = 8 * time.Second

// applyPatchLine moves v on by one line patch.Run wrote. The lines are
// the CLI's own (internal/patch, stagetables.CacheForPatch), unchanged, so
// this reads their stable prefixes.
func applyPatchLine(v *PatchView, line string) {
	line = strings.TrimRight(line, "\r")
	if strings.TrimSpace(line) != "" {
		v.Lines = append(v.Lines, line)
		if len(v.Lines) > maxPatchLines {
			v.Lines = v.Lines[len(v.Lines)-maxPatchLines:]
		}
	}
	bump := func(p int) {
		if p > v.Percent {
			v.Percent = p
		}
	}
	switch {
	case strings.HasPrefix(line, "Wind Waker Multiplayer patcher"):
		v.Stage = "start"
		bump(2)
	case strings.HasPrefix(line, "  input : "):
		bump(3)
	case strings.HasPrefix(line, "  output: "):
		v.Stage = "patch"
		bump(5)
	case strings.HasPrefix(line, "  convert: done"):
		v.Stage = "patch"
		bump(convertTo)
	case strings.HasPrefix(line, "  convert: "):
		// "  convert: RVZ image -> plain ISO with <tool>" starts it.
		if f, _, ok := strings.Cut(strings.TrimPrefix(line, "  convert: "), " image -> "); ok {
			v.Stage, v.Converted = "convert", f
		}
	case strings.HasPrefix(line, "  FST   : "):
		bump(85)
	case strings.HasPrefix(line, "  REL   : "):
		bump(86)
	case strings.HasPrefix(line, "  co-op : "):
		text := strings.TrimPrefix(line, "  co-op : ")
		if w, ok := strings.CutPrefix(text, "WARNING: "); ok {
			v.Coop = CoopRowView{State: "warn", Text: w}
		} else {
			v.Coop = CoopRowView{State: "ok", Text: text}
		}
		bump(97)
	case strings.HasPrefix(line, "This ISO already contains this version"):
		v.AlreadyPatched = true
		bump(85)
	case strings.HasPrefix(line, "ERROR: "):
		v.Error = strings.TrimPrefix(line, "ERROR: ")
	case strings.HasPrefix(line, "Done. Boot "):
		bump(100)
	}
}

// creep is the bar while the disc is being patched: from 5% toward 85%.
func creep(elapsed time.Duration) int {
	return creepFrom(5, 85, elapsed, patchCreep)
}

// convertCreep is the bar's time constant while DolphinTool converts the
// disc (an RVZ decompresses at a few hundred MB/s, inferred: untimed here),
// from 5% toward convertTo; the patch then creeps on from there.
const (
	convertCreep = 15 * time.Second
	convertTo    = 45
)

// creepFrom nears to from from with time constant tc.
func creepFrom(from, to int, elapsed, tc time.Duration) int {
	return from + int(float64(to-from)*(1-math.Exp(-float64(elapsed)/float64(tc))))
}

// lineWriter calls fn with each line written to it.
type lineWriter struct {
	mu  sync.Mutex
	buf []byte
	fn  func(string)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := strings.IndexByte(string(w.buf), '\n')
		if i < 0 {
			break
		}
		line := string(w.buf[:i])
		w.buf = w.buf[i+1:]
		w.fn(line)
	}
	return len(p), nil
}

// flush hands on a last line without a newline.
func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.fn(string(w.buf))
		w.buf = nil
	}
}

// patchJob is one running (or the last) patch.
type patchJob struct {
	cancel context.CancelFunc
	done   chan struct{}

	mu   sync.Mutex
	view PatchView
}

func (j *patchJob) snapshot() PatchView {
	j.mu.Lock()
	defer j.mu.Unlock()
	v := j.view
	v.Lines = append([]string{}, v.Lines...)
	return v
}

// PatchStatus returns the running patch's progress, or the last one's
// this run ({stage: "idle"} before any).
func (b *Bridge) PatchStatus() PatchView {
	b.jobMu.Lock()
	j := b.job
	b.jobMu.Unlock()
	if j == nil {
		return PatchView{Stage: "idle", Lines: []string{}}
	}
	return j.snapshot()
}

// StartPatch patches the disc image at r.Path into <name>-multiplayer.iso
// next to it, in the background; PatchStatus and the "patch" events show
// how far it got. A disc this build already patched is taken as it is. On
// success the config's patched disc (and, for a disc patched now, the
// vanilla one) is saved.
func (b *Bridge) StartPatch(r PatchRequest) (err error) {
	defer b.maskErr(&err)
	if b.updating() {
		return errUpdating
	}
	in := cleanPath(r.Path)
	if in == "" {
		return errors.New("choose your Wind Waker disc image first")
	}
	fi, err := os.Stat(in)
	if err != nil {
		return fmt.Errorf("can't open %s: %w", in, errors.Unwrap(err))
	}
	if fi.IsDir() {
		return fmt.Errorf("%s is a folder; choose the disc image (.iso, .ciso or .rvz) in it", in)
	}
	if r.Patched {
		st, err := b.inspect(in)
		var u *inject.UnsupportedFormatError
		if errors.As(err, &u) && u.Convertible() {
			return fmt.Errorf("that is a compressed %s image: choose it with “Choose your disc” and the app converts and patches it", u.Format)
		}
		if err != nil {
			return fmt.Errorf("that isn't a Wind Waker disc image this app can read (%v)", err)
		}
		switch st {
		case patch.Vanilla:
			return errors.New("that disc isn't patched yet: choose it with “Choose your disc” and the app patches it")
		case patch.OtherVersion:
			return errors.New("that disc was patched by a different ww-multiplayer version: patch your vanilla disc with this one instead")
		}
	}
	out := patch.DefaultOut(in)
	_, statErr := os.Stat(out)
	existed := statErr == nil

	b.jobMu.Lock()
	if b.job != nil && b.job.snapshot().Running {
		b.jobMu.Unlock()
		return errors.New("a disc is already being patched")
	}
	b.jobSeq++
	ctx, cancel := context.WithCancel(b.ctx)
	j := &patchJob{cancel: cancel, done: make(chan struct{}),
		view: PatchView{ID: b.jobSeq, Seq: 1, Stage: "start", Running: true, In: in, Out: out, Lines: []string{}}}
	b.job = j
	b.jobMu.Unlock()

	go b.runPatch(ctx, j, in, out, existed)
	return nil
}

// CancelPatch stops the running patch (see the file comment for what that
// means once the disc is being written).
func (b *Bridge) CancelPatch() {
	b.jobMu.Lock()
	j := b.job
	b.jobMu.Unlock()
	if j == nil {
		return
	}
	j.mu.Lock()
	running := j.view.Running
	if running {
		j.view.Stage = "canceling"
		j.view.Seq++
	}
	j.mu.Unlock()
	if running {
		j.cancel()
		b.emitPatch(j)
	}
}

// patching says whether a patch is running (closing the window asks).
func (b *Bridge) patching() bool { return b.PatchStatus().Running }

// waitPatch waits for a running patch to end. There is no time limit:
// the patch is bounded (one disc read and written) and can't be
// interrupted once writing, and a process that exited mid-write would
// leave PatchISO's temp file behind.
func (b *Bridge) waitPatch() {
	b.jobMu.Lock()
	j := b.job
	b.jobMu.Unlock()
	if j != nil {
		<-j.done
	}
}

func (b *Bridge) emitPatch(j *patchJob) {
	v := j.snapshot()
	b.emit(EventView{Kind: "patch", Time: b.now().UTC().Format(time.RFC3339Nano), Patch: &v})
}

func (b *Bridge) runPatch(ctx context.Context, j *patchJob, in, out string, existed bool) {
	defer b.restartIfReady() // last: an installed update waited for this patch
	defer close(j.done)
	defer j.cancel()
	update := func(f func(*PatchView)) {
		j.mu.Lock()
		f(&j.view)
		j.view.Seq++
		j.mu.Unlock()
		b.emitPatch(j)
	}
	w := &lineWriter{fn: func(l string) { update(func(v *PatchView) { applyPatchLine(v, l) }) }}

	// The bar creeps while the disc is converted, and while it is read,
	// patched and written: after a conversion the patch's creep starts
	// when it does, from convertTo.
	stop := make(chan struct{})
	start := time.Now()
	go func() {
		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		stage, since := "", start
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				update(func(v *PatchView) {
					if v.Stage != stage {
						stage, since = v.Stage, time.Now()
					}
					p := 0
					switch {
					case v.Stage == "convert":
						p = creepFrom(5, convertTo, time.Since(since), convertCreep)
					case v.Stage == "patch" && v.Converted != "":
						p = creepFrom(convertTo, 85, time.Since(since), patchCreep)
					case v.Stage == "patch":
						p = creep(time.Since(start))
					}
					if p > v.Percent {
						v.Percent = p
					}
				})
			}
		}
	}()

	// Keep an optional REL fix (`patch --fix-sea-heap`) the disc being
	// replaced has: the one at out, or the last patched disc. The app has
	// no switch for it, so its re-patch after an update must not drop it.
	b.mu.Lock()
	last := b.cfg.ISO.Patched
	b.mu.Unlock()
	opts := patch.Options{KeepFixes: true, KeepFrom: []string{last}}
	if patch.NeedsConvert(in) {
		opts.Converter = b.patchConverter() // looks for Dolphin only when needed
	}
	for _, step := range b.postPatch {
		opts.PostPatch = append(opts.PostPatch, func(cw io.Writer, image string) {
			if ctx.Err() != nil {
				// Cancelled: don't build (or record as the last patched
				// disc) tables for a disc that is about to be removed.
				return
			}
			update(func(v *PatchView) {
				if v.Stage != "canceling" {
					v.Stage = "coop"
				}
				v.Coop = CoopRowView{State: "pending"}
				if v.Percent < 88 {
					v.Percent = 88
				}
			})
			step(cw, image)
		})
	}
	res, err := b.patchRun(ctx, in, out, w, opts)
	w.flush()
	close(stop)

	cancelled := ctx.Err() != nil
	stopped := ""
	if cancelled {
		switch {
		case err != nil || (res.AlreadyPatched && res.ConvertedFrom == ""):
			stopped = "nothing" // stopped before the disc was read, or nothing to write
			// (A converted disc that was already patched has its converted
			// ISO kept at res.Out: that is a written disc, handled below.)
		case existed:
			// PatchISO has already renamed the new disc over the earlier
			// one: removing it would leave the player with neither.
			stopped = "replaced"
		default:
			// The user cancelled (or the window closed) while the disc was
			// being written: it is complete, but they didn't want it.
			stopped = "removed"
			if os.Remove(res.Out) != nil {
				stopped = "kept"
			}
		}
	}
	if err == nil && !cancelled {
		b.mu.Lock()
		b.cfg.ISO.Patched = res.Image()
		if !res.AlreadyPatched {
			b.cfg.ISO.Vanilla = in
		}
		b.mu.Unlock()
		err = b.saveErr()
		b.upd.mu.Lock()
		b.upd.plan = "" // the disc is this build's now
		b.upd.mu.Unlock()
	}
	update(func(v *PatchView) {
		v.Running = false
		switch {
		case cancelled:
			v.Stage, v.Error, v.Stopped = "canceled", "", stopped
		case err != nil:
			v.Stage = "failed"
			v.Error = patchError(err)
		default:
			v.Stage, v.Percent = "done", 100
			v.Image, v.AlreadyPatched = res.Image(), res.AlreadyPatched
		}
	})
}

// patchConverter is the setup's DolphinTool (patch.Converter): beside the
// saved Dolphin first, then beside each Dolphin the Dolphin step would
// detect (that step comes after the disc), in its order; the first usable
// one converts.
func (b *Bridge) patchConverter() *patch.Converter {
	c, _ := b.effective()
	var exes []string
	if exe := c.Dolphin.Exe; exe != "" && isFile(exe) {
		exes = append(exes, exe)
	}
	for _, d := range launch.Candidates(b.denv()) {
		if d.Found {
			exes = append(exes, d.Exe)
		}
	}
	return patch.ConverterFor(exes...)
}

// patchError is a failed patch in the setup's words.
func patchError(err error) string {
	var nt *patch.NoDolphinToolError
	if errors.As(err, &nt) {
		where := "no Dolphin was found yet"
		if looked := nt.Looked(); len(looked) > 0 {
			where = "there is none beside " + strings.Join(looked, " or ")
		}
		if len(nt.Unusable) > 0 {
			why := make([]string, len(nt.Unusable))
			for i, e := range nt.Unusable {
				why[i] = e.Error()
			}
			where = "the one it found can't be used (" + strings.Join(why, "; ") + ")"
		}
		return fmt.Sprintf("Your disc is a compressed %s image. The app turns those into an ISO with the DolphinTool that comes with Dolphin, but %s. "+
			"Set up Dolphin in the next step and patch again, or in Dolphin right-click the game > Convert File... > Format: ISO and choose the .iso it writes.",
			nt.Format.Format, where)
	}
	switch {
	case errors.Is(err, patch.ErrLowSpace):
		detail := strings.TrimPrefix(err.Error(), patch.ErrLowSpace.Error()+": ")
		return "There isn't enough free space next to your disc to convert it (" + detail + "). Free some space there, or move the disc image to a drive with more, and try again."
	case errors.Is(err, inject.ErrPatchedByOtherVersion):
		return "This disc was patched by a different ww-multiplayer version. Patch your vanilla (unmodified) Wind Waker disc with this version instead."
	case errors.Is(err, inject.ErrOutputIsInput):
		return "The patched disc would overwrite your original; rename your disc image and try again."
	}
	return err.Error()
}
