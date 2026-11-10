package widescreen

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/host"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/ops"
)

// Reconciler keeps one game's WWWS block at what Decide wants, through
// that game's ops queue (native-widescreen.md section 7, "Who submits").
// It is the game's only ops writer while it runs. It heals reboot resets
// (ws_init puts mode 1 back), save states taken in another mode and a
// stray CLI toggle. It never compares the flags byte, so a tester's
// flags survive until the next resubmit.
type Reconciler struct {
	Q       *ops.Queue
	M       mem.Reader
	Setting launch.WidescreenSetting
	// Dolphin reads Dolphin's settings (ok false: unknown, so nothing is
	// sent); called at most once per Slow.
	Dolphin func() (Dolphin, bool)
	Rep     log.Reporter
	// Fast is the poll until the block first holds what is wanted, Slow
	// after; 100 ms and 1 s when 0.
	Fast, Slow time.Duration
	// TicketWait bounds the wait for the last ticket when stopping; 2 s
	// when 0.
	TicketWait time.Duration
}

// Run reconciles until ctx ends, then waits for its own last ticket.
func (r *Reconciler) Run(ctx context.Context) error {
	fast, slow, tw := r.Fast, r.Slow, r.TicketWait
	if fast <= 0 {
		fast = 100 * time.Millisecond
	}
	if slow <= 0 {
		slow = time.Second
	}
	if tw <= 0 {
		tw = 2 * time.Second
	}
	s := &reconcileState{}
	defer func() {
		if s.pending == nil {
			return
		}
		wctx, cancel := context.WithTimeout(context.Background(), tw)
		defer cancel()
		_, _ = r.Q.Wait(wctx, *s.pending)
	}()
	for {
		r.tick(s, slow)
		every := fast
		if s.applied {
			every = slow
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(every):
		}
	}
}

type reconcileState struct {
	pending   *ops.Ticket
	applied   bool // the block held what was wanted once: slow polls from then
	view      Dolphin
	viewOK    bool
	viewAt    time.Time
	demote    bool // a mode-2 block wanted lower once: twice submits (a torn read can't flap it)
	warned    string
	said      string
	refused   bool
	lastWant  Request
	reboot    bool // the block said mode 2 with the knob vanilla once (a reboot mid-load)
	pending2D bool // the last tick saw the 2D latch behind the live mode
	// The boot-timing marks (native-widescreen.md section 8 stage 2, check
	// 11), each logged once with its time: the first Ready() probe, the
	// first WS_SET answered OK, the first probe on the title screen.
	sawReady, sawOK, sawTitle bool
}

// mark logs a boot-timing mark once, with the wall-clock time.
func (r *Reconciler) mark(seen *bool, what string) {
	if !*seen {
		*seen = true
		log.Logf(r.Rep, log.Info, "widescreen: %s at %s", what, time.Now().Format("15:04:05.000"))
	}
}

func (r *Reconciler) tick(s *reconcileState, slow time.Duration) {
	if err := r.Q.Ready(); err != nil {
		s.pending = nil // a reboot cleared the queue
		return
	}
	r.mark(&s.sawReady, "first ready probe")
	if !s.sawTitle && atTitle(r.M) {
		r.mark(&s.sawTitle, "first probe on the title screen")
	}
	if s.pending != nil {
		res, done, err := r.Q.Result(*s.pending)
		switch {
		case err != nil:
			s.pending = nil // reset or lost: the block says what happened
		case !done:
			return // C has not run it yet
		default:
			s.pending = nil
			if res != ops.ROK {
				if !s.refused {
					log.Logf(r.Rep, log.Warn, "widescreen: the game answered %s to %s", res, s.lastWant.Describe())
				}
				s.refused, s.applied = true, true // no fast resubmits against a refusal
				return
			}
			s.refused = false
			r.mark(&s.sawOK, "first WS_SET OK")
		}
	}
	blk, err := mailbox.ReadWidescreen(r.M)
	if err != nil || !blk.Present {
		return
	}
	if p := blk.Pending2D(); p != s.pending2D {
		s.pending2D = p
		if p {
			log.Logf(r.Rep, log.Info, "widescreen: %s", mailbox.Pending2DNote)
		} else {
			log.Logf(r.Rep, log.Info, "widescreen: 2D follows the setting")
		}
	}
	knob, err1 := mem.ReadU32(r.M, mailbox.WsKnobAddr)
	painter, err2 := mem.ReadU32(r.M, PainterAddr)
	if err1 != nil || err2 != nil {
		return
	}
	if now := time.Now(); s.viewAt.IsZero() || now.Sub(s.viewAt) >= slow {
		s.view, s.viewOK = r.Dolphin()
		s.viewAt = now
	}
	if !s.viewOK {
		return
	}
	// A reboot reloads the DOL (the knob back to vanilla) before ws_init
	// rewrites the block, which still says mode 2: skip one tick so that
	// window doesn't read as another widescreen writer.
	inReboot := blk.Mode == mailbox.WsModeNative && knob == mailbox.WsKnobVanilla
	if inReboot && !s.reboot {
		s.reboot = true
		return
	}
	s.reboot = inReboot
	want := Decide(r.Setting, s.view, Game{Block: blk, Knob: knob, Painter: painter})
	if want.Warn != s.warned {
		if want.Warn != "" {
			log.Logf(r.Rep, log.Warn, "widescreen: %s", want.Warn)
		}
		s.warned = want.Warn
	}
	if blk.Mode == want.Mode && math.Float32bits(blk.Factor) == math.Float32bits(want.Factor) {
		s.applied, s.demote = true, false
		if d := want.Describe(); d != s.said {
			log.Logf(r.Rep, log.Info, "widescreen: %s", d)
			s.said = d
		}
		return
	}
	if blk.Mode == mailbox.WsModeNative && want.Mode != mailbox.WsModeNative && !s.demote {
		s.demote = true
		return
	}
	s.demote = false
	a, b, c := want.Words()
	t, err := r.Q.Submit(mailbox.OpWsSet, a, b, c)
	if err != nil {
		return
	}
	s.pending, s.lastWant, s.applied = &t, want.Request, false
}

// RunDolphin reconciles Dolphin pid's game for the app's setting (the
// "Widescreen" text; a bad one counts as off) until ctx ends: the engine's
// GameDeps.Widescreen. Warnings go to rep (the Play page).
func RunDolphin(ctx context.Context, pid uint32, setting string, rep log.Reporter) error {
	s, err := launch.ParseWidescreenSetting(setting)
	if err != nil {
		log.Logf(rep, log.Warn, "widescreen: %v (left off)", err)
	}
	d, err := emu.FindByPID(pid, "GZLE01")
	if err != nil {
		return fmt.Errorf("widescreen: %w", err)
	}
	defer d.Close()
	return (&Reconciler{Q: ops.New(d), M: d, Setting: s, Dolphin: DolphinOf(pid), Rep: rep}).Run(ctx)
}

// DolphinOf reads Dolphin pid's settings: its user folder and command line
// once (listing processes is slow: a failed look-up waits procRetry), then
// its graphics settings, render size and Gecko code at every call.
func DolphinOf(pid uint32) func() (Dolphin, bool) {
	var dir string
	var args []string
	var found bool
	var retry time.Time
	return func() (Dolphin, bool) {
		if !found && !time.Now().Before(retry) {
			if dir, args, found = dolphinProc(pid); !found {
				retry = time.Now().Add(procRetry)
			}
		}
		if !found {
			return Dolphin{}, false
		}
		v := Dolphin{GFX: launch.ReadGFXAspect(dir, args), Gecko: GeckoEnabled(dir, args)}
		if v.GFX.Mode == launch.AspectStretch || v.GFX.Mode == launch.AspectCustomStretch {
			v.RenderW, v.RenderH, _ = host.RenderSize(pid)
		}
		return v, true
	}
}

// procRetry is how long a failed process look-up waits before the next.
const procRetry = 30 * time.Second

// dolphinProc is pid's user folder and arguments.
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

// atTitle says the game is on the title screen: the current stage is
// sea_T (a read error says no).
func atTitle(m mem.Reader) bool {
	b, err := m.Read(addr.CurStageName, addr.StageNameLen)
	if err != nil || len(b) != addr.StageNameLen {
		return false
	}
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b) == addr.StageTitle
}
