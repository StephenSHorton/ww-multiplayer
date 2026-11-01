package main

// boss-end (wind#58): end a boss fight from its own actor's fields, so a
// live test reaches the game's own ending (its event bit, its stage
// change) without fighting the boss out. Design: the gt/design.md of
// wind#58 and the per-boss findings it was built from (each checked by a
// skeptic; the checked, corrected recipe is the one here).
//
//	boss-end <boss> [--step S] [--variant natural|parry] [--dry-run] [--watch secs]
//
// The pipeline is the same for every boss:
//
//  1. a snapshot (wwapi/state) refuses the wrong stage or room, a stage
//     change already pending, an event running or queued, the pause menu
//     and no Link;
//  2. the boss's actor is found in the actor queue by proc (plus its
//     params, mType or clone byte) and must be the only one and executing;
//  3. its fields are checked against the recipe's preconditions, and a
//     miss names the field that blocks;
//  4. ALREADY when the ending's bit is already in the save or the actor
//     already reads as ending;
//  5. the planned writes: aligned u32 words, each merged (mask, value)
//     into a fresh read just before its write, all back to back in this
//     one process. --dry-run prints them and stops;
//  6. the writes, then a read-back that catches the game racing them
//     (RACE, exit 2, with the recovery);
//  7. a watch (--watch secs, default 20; 0 = none) that prints each phase
//     change: the actor's state, the bit, NextEnable with the decoded
//     mNextStage, the stage change, and the save view after the load.
//     bossEndPhase, its judge, is a pure function.
//
// What it writes: only words inside the boss actor (and, for ganondorf
// --variant parry, one word of Link's actor). Never the save, the event,
// switch or tmp bits, nor the REL's boss_kill request (mailbox.
// BossRequestRegion): checkBossPlan refuses a plan that would, before any
// write. It never goes through wwapi/gate, whose idleClosed refuses a
// refight, and never posts a REL request, which answers R_GATE on an X
// stage (inject/rel/bosskill.c:296, :624-627) and has no row for Fganon,
// BGN or GND (bk_proc_ok, :601-615). No pause or frame step exists in
// wwapi/emu or wwdev, so the writes are not frame-atomic: their order and
// the read-back carry the safety.
//
// Exit: 0 the ending was seen (or --dry-run, or --watch 0 after a clean
// read-back), 1 refused (ALREADY included) or an error, 2 RACE or a watch
// failure (the game went another way), 3 the watch ran out before the
// ending was seen.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/diag"
)

const bossEndUsage = "boss-end <gohma|kalle|jalhalla|molgera|pg-maze|pg3|puppet|ganondorf> [--step flower|door|form1|form2|form3] [--variant natural|parry] [--dry-run] [--watch secs=20]"

// bossEndDefaultWatch is --watch's default.
const bossEndDefaultWatch = 20 * time.Second

// The ending event bits (d_save_event_flag.inc; no Go constant names them
// elsewhere) and the tmp bit the trials' X branches set with them.
const (
	evGohmaTrials    uint16 = 0x3240 // GOHMA_TRIALS_CLEAR (d_save_event_flag.inc:378)
	evKalleTrials    uint16 = 0x3220 // KALLE_DEMOS_TRIALS_CLEAR (:379)
	evJalhallaTrials uint16 = 0x3210 // JALHALLA_TRIALS_CLEAR (:380)
	evMolgeraTrials  uint16 = 0x3208 // MOLGERA_TRIALS_CLEAR (:381)
	evPuppetGanon    uint16 = 0x3F10 // Puppet Ganon beaten (d_a_bgn.cpp:3347-3351)
	evVfanDoor       uint16 = 0x3A08 // UNK_3A08, the Vfan door broken (d_a_obj_vfan.cpp:192)
	evEndhr          uint16 = 0x3F40 // set by the endhr STB after Ganondorf (docs/08-coop-design.md:578)
	tmpTrials        uint16 = 0x0480 // dSv_event_tmp_flag_c UNK_0480 (d_save_event_tmp_flag.inc:33)
)

// Procs boss-end reads that wwapi/stage does not name.
const (
	procFgmahou  = int16(0x0F4) // fpcNm_FGMAHOU_e (f_pc_name.h:256): Phantom Ganon's orbs
	procDisapp   = int16(0x193) // fpcNm_DISAPPEAR_e (f_pc_name.h:415): fopAcM_createDisappear, Kalle's heart container
	procBossItem = int16(stage.ProcBossItem)
	procNpcCb1   = int16(0x150) // fpcNm_NPC_CB1_e (f_pc_name.h:348): Makar
)

// The struct sizes (each header's "Size:" line), the span a boss's writes
// must stay inside.
const (
	sizeBtd    = 0x6E94  // d_a_btd.h:143
	sizeBmd    = 0xBE4   // d_a_bmd.h:102
	sizeBpw    = 0xD7C   // d_a_bpw.h has no Size line: its last member, mInvisibleModel at 0xD78, plus 4 at least
	sizeBwd    = 0x3C54  // d_a_bwd.h:162
	sizeFganon = 0xB8C   // d_a_fganon.h: mB8B at 0xB8B is the last member
	sizeVfan   = 0x44C   // d_a_obj_vfan.h: mBreakTimer at 0x448 is the last member
	sizeBgn    = 0xCC94  // d_a_bgn.h:203
	sizeBgn2   = 0x30DC  // d_a_bgn2.h:74
	sizeBgn3   = 0x100B4 // d_a_bgn3.h:97
	sizeGnd    = 0x15E4  // d_a_gnd.h:16 (u8 m13D2[0x15E4 - 0x13D2])
)

// Gohma (btd_class, d_a_btd.h:29-54).
const (
	btdOffAct  = mailbox.BossBtdM02E4Off // s16 m02E4, s16 mGohmaState
	btdOffRock = mailbox.BossBtdM02F6Off // u8 m02F6
)

// Kalle Demos (bmd_class, d_a_bmd.h:31-36, :85-100).
const (
	bmdOffMode = mailbox.BossBmdModeOff // s16 mMode, s16 m302
	bmdOffCore = mailbox.BossBmdM304Off // s16 m304, s16 m306
	bmdOffM310 = mailbox.BossBmdM310Off // s16 m310, s16 m312
	bmdOffB74  = mailbox.BossBmdMB74Off // s16 mB74, s16 mB76
	bmdOffBDC  = mailbox.BossBmdMBDCOff // f32 mBDC
	bmdOffBE0  = mailbox.BossBmdMBE0Off // u8 mBE0, then padding
)

// Phantom Ganon (fganon_class, d_a_fganon.h:25-109).
const (
	fgOffAct   = 0x384 // s16 m384 (frame counter), s16 mAction (+0x386)
	fgOffMode  = 0x388 // s16 mMode, s16 m38A
	fgOffTimer = 0x3A4 // s16 m3A4[0], m3A4[1] (+0x3A6, the fly timer)
	fgOffBall  = 0x408 // s8 m408: an energy ball out
	fgOffDark  = 0x671 // s8 m671: a dark ball out
	fgOffShown = 0x68D // s8 mbIsMaterialized
	fgOffClone = 0x68F // u8 m68F: 0 for the master
	fgOffB54   = 0xB54 // s16 mB54, the demo camera's state

	fgParamsMaze  = 0xFFFFFA01 // GanonJ r13: m2BC 1, range 0xFA (tables.json)
	fgParamsThird = 0xFF006402 // GanonM r0: m2BC 2, sw 0

	vfanOffState = 0x440 // int mState (d_a_obj_vfan.h:41)
	vfanSwSave   = 24    // the GanonM door's swSave: params & 0xFF (PRM_SWSAVE_W 8, d_a_obj_vfan.h:16-23)
)

// Puppet Ganon (bgn_class d_a_bgn.h, bgn2_class d_a_bgn2.h, bgn3_class
// d_a_bgn3.h; retail layout, VERSION > DEMO).
const (
	bgnOffForm   = 0x2B4  // u8 m02B4, s8 m02B5 (the form: 0, 1, 2)
	bgnOffAct    = 0xC748 // s16 mC748 (action_main's action), s16 mC74A
	bgnOffHead   = 0xC778 // s8 mC778, s8 mC779
	bgnOffCS     = 0xCA60 // s16 mCA60, s8 mCSMode (+0xCA62)
	bgn2OffAct   = 0x310  // s16 m0310, s16 m0312 (the action)
	bgn2OffM314  = 0x314  // s16 m0314, s16 m0316
	bgn3OffAct   = 0xFD88 // s16 m0FD88, s16 m0FD8A (the action)
	bgn3OffM0FD8 = 0xFD8C // s16 m0FD8C, s16 m0FD8E
	bgn3OffShut  = 0xFDB4 // s16 m0FDB4 (damage_check shut while nonzero), s16 m0FDB6
)

// Ganondorf (gnd_class): the decomp has only stubs, so these come from the
// retail d_a_gnd.rel disassembly (wind#58's gnd.s): gnd_move switches on
// +0x2CE (0x50F4), finish starts the demo at sub 0 (0x42D0-0x4314),
// demo_camera runs on +0x155E with its frame at +0x1560 (0x54F0), j_demo
// tests +0x1C0 (0x254), Execute forces action 0x14 while +0x2B4 is 0
// (0x6D54-0x6D64), attack_last takes Link's LAST_COMBO_WAIT (0x3870-0x3894).
const (
	gndOffDemoID = 0x1C0  // u8, the demo actor ID: j_demo while nonzero
	gndOffLive   = 0x2B4  // u8, 0 forces action 0x14 every frame
	gndOffAct    = 0x2CC  // s16 counter (camera sway only), s16 action (+0x2CE)
	gndOffSub    = 0x2D0  // s16 sub, s16 +0x2D2
	gndOffDemo   = 0x155C // s16, s16 demo mode (+0x155E)
	gndOffFrame  = 0x1560 // s16 demo frame, s16

	gndActFinish = 0x1E
	gndActLast   = 5

	linkOffNoReset1 = 0x2A0      // u32 mNoResetFlg1 (d_a_player.h:486)
	linkLastCombo   = 0x20000000 // daPyFlg1_LAST_COMBO_WAIT (d_a_player.h:226)
)

// bossEndOpts is one run's arguments.
type bossEndOpts struct {
	Boss, Step, Variant string
	DryRun              bool
	Watch               time.Duration // 0: no watch
	// Tick is the read-back's and the watch's sample period
	// (enemyWatchTick); tests shorten it.
	Tick time.Duration
}

// bossOutcome is how a run ended; exitCode maps it.
type bossOutcome int

const (
	bossOK      bossOutcome = iota // written and seen (or no watch)
	bossDryRun                     // the plan printed, nothing written
	bossRefused                    // refused before any write
	bossAlready                    // the ending already happened or runs
	bossRace                       // the read-back saw the game race the writes
	bossFail                       // the watch saw the game go another way
	bossTimeout                    // the watch ran out first
	bossError                      // a read or write failed
)

func (o bossOutcome) exitCode() int {
	switch o {
	case bossOK, bossDryRun:
		return 0
	case bossRace, bossFail:
		return 2
	case bossTimeout:
		return 3
	}
	return 1
}

func (o bossOutcome) String() string {
	return [...]string{"OK", "DRY-RUN", "REFUSED", "ALREADY", "RACE", "FAIL", "TIMEOUT", "ERROR"}[o]
}

// bossStop is a refusal (bossRefused or bossAlready) before any write.
type bossStop struct {
	out bossOutcome
	msg string
}

func (e *bossStop) Error() string { return e.out.String() + ": " + e.msg }

func refuse(format string, a ...any) error {
	return &bossStop{bossRefused, fmt.Sprintf(format, a...)}
}

func already(format string, a ...any) error {
	return &bossStop{bossAlready, fmt.Sprintf(format, a...)}
}

// pokeWord is one planned write: the aligned u32 at Base+Off becomes
// (fresh &^ Mask) | Value, the fresh word read just before.
type pokeWord struct {
	Who   string // "Btd", "Link", ...
	Base  uint32
	Off   uint32
	Mask  uint32
	Value uint32 // within Mask
	What  string
	// Guard, when set, judges the fresh word (and anything else it reads)
	// just before the write: false skips it, with why.
	Guard     func(m mem.Reader, word uint32) (bool, string)
	GuardNote string // the guard, for the plan's text
}

func (p pokeWord) Addr() uint32 { return p.Base + p.Off }

// memSpan is a range writes may touch.
type memSpan struct {
	Name       string
	Start, End uint32
}

func (s memSpan) holds(a, n uint32) bool { return a >= s.Start && a+n <= s.End }

// raceState is one read-back's verdict.
type raceState int

const (
	raceOK      raceState = iota
	racePending           // not yet: read again until the deadline
	raceBad
)

// bossPlan is what a boss's planner decided.
type bossPlan struct {
	Key      string // bossEndPhase's key: the boss, "/step", "/natural"
	Title    string // "Gohma (Btd pid 12 at 0x80B00000)"
	Target   actors.Actor
	Spans    []memSpan
	Writes   []pokeWord
	Notes    []string // soft warnings, printed before the plan
	Race     func(m mem.Reader) (raceState, string)
	RaceWait time.Duration // how long a pending read-back may stay pending
	// PendingRace makes a read-back still pending at RaceWait a RACE;
	// otherwise it is a warning and the watch goes on.
	PendingRace bool
	RaceHint    string // the recovery printed with RACE
	// Observe fills the actor's part of a watch sample (State, A, B);
	// the pid check (Gone) is the loop's.
	Observe func(m mem.Reader, o *bossObs) error
	// Bit is the ending's event bit (0: none); Sw reads the boss's
	// switch, if it has one.
	Bit    uint16
	Sw     func(s *state.Snapshot) bool
	SwName string
	// Scan counts what a queue walk shows (Poes, Kalle's heart and
	// Makar).
	Scan    func(aw *actors.Walk) (poes int, extras string)
	Warning string // printed after the writes (Ganondorf's session end)
	// Start is the stage the run began in (the planning snapshot's), which
	// the watch's Left compares with.
	Start stageRef
}

// bossEnv is what a planner reads.
type bossEnv struct {
	m    mem.Mem
	snap *state.Snapshot
	view save.View
	walk actors.Walk
	o    bossEndOpts
}

// bossDef is one boss: where it is fought, what it takes.
type bossDef struct {
	Name     string
	Stage    string
	Room     int // mStayNo it needs; -1 any
	Steps    []string
	Variants []string
	NeedStep bool
	Plan     func(e *bossEnv) (*bossPlan, error)
}

var bossDefs = map[string]bossDef{
	"gohma":     {Name: "Gohma", Stage: "Xboss0", Room: -1, Plan: planGohma},
	"kalle":     {Name: "Kalle Demos", Stage: "Xboss1", Room: -1, Steps: []string{"flower"}, Variants: []string{"natural"}, Plan: planKalle},
	"jalhalla":  {Name: "Jalhalla", Stage: "Xboss2", Room: -1, Variants: []string{"natural"}, Plan: planJalhalla},
	"molgera":   {Name: "Molgera", Stage: "Xboss3", Room: -1, Variants: []string{"natural"}, Plan: planMolgera},
	"pg-maze":   {Name: "Phantom Ganon (maze)", Stage: "GanonJ", Room: 13, Variants: []string{"natural"}, Plan: planPGMaze},
	"pg3":       {Name: "Phantom Ganon (third fight)", Stage: "GanonM", Room: 0, Steps: []string{"door"}, Plan: planPG3},
	"puppet":    {Name: "Puppet Ganon", Stage: "GanonK", Room: 0, Steps: []string{"form1", "form2", "form3"}, NeedStep: true, Variants: []string{"natural"}, Plan: planPuppet},
	"ganondorf": {Name: "Ganondorf", Stage: "GTower", Room: -1, Variants: []string{"parry"}, Plan: planGanondorf},
}

// parseBossEndArgs reads boss-end's arguments: the boss, then --step S,
// --variant V, --dry-run and --watch N (each also as --name=value).
func parseBossEndArgs(args []string) (bossEndOpts, error) {
	o := bossEndOpts{Watch: bossEndDefaultWatch, Tick: enemyWatchTick}
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, ok := strings.CutPrefix(a, "--")
		if !ok {
			pos = append(pos, a)
			continue
		}
		name, val, hasVal := strings.Cut(name, "=")
		if name == "dry-run" {
			if hasVal {
				return o, fmt.Errorf("--dry-run takes no value")
			}
			o.DryRun = true
			continue
		}
		if name != "step" && name != "variant" && name != "watch" {
			return o, fmt.Errorf("unknown flag %s", a)
		}
		if !hasVal {
			if i+1 >= len(args) {
				return o, fmt.Errorf("--%s needs a value", name)
			}
			i++
			val = args[i]
		}
		switch name {
		case "step":
			o.Step = val
		case "variant":
			o.Variant = val
		case "watch":
			n, err := strconv.Atoi(val)
			if err != nil || n < 0 || n > 3600 {
				return o, fmt.Errorf("--watch must be seconds from 0 to 3600, not %q", val)
			}
			o.Watch = time.Duration(n) * time.Second
		}
	}
	if len(pos) != 1 {
		return o, fmt.Errorf("want one boss, got %q", pos)
	}
	o.Boss = pos[0]
	d, ok := bossDefs[o.Boss]
	if !ok {
		return o, fmt.Errorf("unknown boss %q", o.Boss)
	}
	if o.Step != "" && !contains(d.Steps, o.Step) {
		return o, fmt.Errorf("%s has no --step %s (steps: %s)", o.Boss, o.Step, orNone(d.Steps))
	}
	if d.NeedStep && o.Step == "" {
		return o, fmt.Errorf("%s needs --step %s", o.Boss, strings.Join(d.Steps, "|"))
	}
	if o.Variant != "" && !contains(d.Variants, o.Variant) {
		return o, fmt.Errorf("%s has no --variant %s (variants: %s)", o.Boss, o.Variant, orNone(d.Variants))
	}
	if (o.Step == "door" || o.Step == "flower") && o.Variant != "" {
		return o, fmt.Errorf("--step %s has no variant", o.Step)
	}
	return o, nil
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}

func orNone(xs []string) string {
	if len(xs) == 0 {
		return "none"
	}
	return strings.Join(xs, ", ")
}

// runBossEnd is the boss-end command.
func runBossEnd(_ string, args []string) {
	o, err := parseBossEndArgs(args)
	if err != nil {
		exitUsage(err, bossEndUsage)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	out := bossError
	err = diag.WithMem(openDolphin, func(m mem.Mem) error {
		out = bossEnd(ctx, os.Stdout, m, o)
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "boss-end: %v\n", err)
		os.Exit(1)
	}
	os.Exit(out.exitCode())
}

// bossEnd runs the pipeline over m and prints to w.
func bossEnd(ctx context.Context, w io.Writer, m mem.Mem, o bossEndOpts) bossOutcome {
	if o.Tick <= 0 {
		o.Tick = enemyWatchTick
	}
	p, err := planBossEnd(m, o)
	if err != nil {
		var st *bossStop
		if errors.As(err, &st) {
			fmt.Fprintf(w, "%s: %s\n", st.out, st.msg)
			return st.out
		}
		fmt.Fprintf(w, "ERROR: %v\n", err)
		return bossError
	}
	fmt.Fprintf(w, "boss-end %s: %s\n", o.Boss, p.Title)
	for _, n := range p.Notes {
		fmt.Fprintf(w, "  note: %s\n", n)
	}
	fmt.Fprintf(w, "plan (%d words, in order; each merged into a fresh read just before its write):\n", len(p.Writes))
	for i, pw := range p.Writes {
		fmt.Fprintf(w, "  %d. 0x%08X %s+0x%X mask %08X value %08X  %s", i+1, pw.Addr(), pw.Who, pw.Off, pw.Mask, pw.Value, pw.What)
		if pw.GuardNote != "" {
			fmt.Fprintf(w, " (only %s)", pw.GuardNote)
		}
		fmt.Fprintln(w)
	}
	if o.DryRun {
		fmt.Fprintln(w, "DRY-RUN: nothing written")
		return bossDryRun
	}
	for i, pw := range p.Writes {
		old, err := mem.ReadU32(m, pw.Addr())
		if err != nil {
			fmt.Fprintf(w, "ERROR: write %d: %v\n", i+1, err)
			return bossError
		}
		if pw.Guard != nil {
			ok, why := pw.Guard(m, old)
			if !ok {
				fmt.Fprintf(w, "  %d. skipped: %s\n", i+1, why)
				continue
			}
		}
		v := old&^pw.Mask | pw.Value&pw.Mask
		if err := mem.WriteU32(m, pw.Addr(), v); err != nil {
			fmt.Fprintf(w, "ERROR: write %d: %v\n", i+1, err)
			return bossError
		}
		fmt.Fprintf(w, "  %d. wrote 0x%08X: %08X -> %08X\n", i+1, pw.Addr(), old, v)
	}
	if out := bossReadBack(ctx, w, m, p, o.Tick); out != bossOK {
		return out
	}
	if p.Warning != "" {
		fmt.Fprintf(w, "warning: %s\n", p.Warning)
	}
	if o.Watch <= 0 {
		return bossOK
	}
	return bossWatch(ctx, w, m, p, o)
}

// planBossEnd is steps 1-5: the snapshot, the actor, the preconditions,
// ALREADY and the plan, checked against the spans.
func planBossEnd(m mem.Mem, o bossEndOpts) (*bossPlan, error) {
	d, ok := bossDefs[o.Boss]
	if !ok {
		return nil, fmt.Errorf("unknown boss %q", o.Boss)
	}
	snap, err := state.Read(m, state.Options{})
	if err != nil {
		return nil, err
	}
	if err := bossSnapRefusal(d, snap); err != nil {
		return nil, err
	}
	aw, err := actors.Read(m, actors.MaxNodes)
	if err != nil {
		return nil, err
	}
	e := &bossEnv{m: m, snap: snap, view: save.ViewOf(snap), walk: aw, o: o}
	p, err := d.Plan(e)
	if err != nil {
		return nil, err
	}
	if err := checkBossPlan(p); err != nil {
		return nil, err
	}
	p.Start = stageRef{snap.StageName, snap.StagePoint, snap.StageRoom, snap.StageLayer}
	return p, nil
}

// bossSnapRefusal is step 1.
func bossSnapRefusal(d bossDef, s *state.Snapshot) error {
	switch {
	case s.StageName != d.Stage:
		return refuse("stage %q, not %s's %s", s.StageName, d.Name, d.Stage)
	case d.Room >= 0 && int(s.StayNo) != d.Room:
		return refuse("Link is in room %d of %s, not room %d", s.StayNo, d.Stage, d.Room)
	case s.NextEnable != 0:
		return refuse("a stage change is pending (NextEnable %d, next %q)", s.NextEnable, s.NextStageName)
	case s.EvtOrderCount != 0 || s.EvtMode != 0:
		return refuse("an event is running or queued (mOrderCount %d, mMode %d)", s.EvtOrderCount, s.EvtMode)
	case s.MenuPause != 0:
		return refuse("the pause menu is up (dMenu_pause %d)", s.MenuPause)
	case s.PlayerPtr == 0:
		return refuse("no Link (mpPlayerPtr[0] is 0)")
	}
	return nil
}

// checkBossPlan refuses a plan with a write that is not an aligned word
// inside one of its spans, or that touches the save or the REL's
// boss_kill request.
func checkBossPlan(p *bossPlan) error {
	forbidden := []memSpan{
		{"the save (dSv_info_c)", state.SaveRegion.Start, state.SaveRegion.End},
		{"the boss_kill request", mailbox.BossRequestRegion.Start, mailbox.BossRequestRegion.End},
	}
	for i, pw := range p.Writes {
		a := pw.Addr()
		if a%4 != 0 {
			return fmt.Errorf("plan write %d at 0x%08X is not word-aligned", i+1, a)
		}
		in := false
		for _, s := range p.Spans {
			if s.holds(a, 4) {
				in = true
			}
		}
		if !in {
			return fmt.Errorf("plan write %d at 0x%08X is outside %s", i+1, a, spanNames(p.Spans))
		}
		for _, f := range forbidden {
			if a < f.End && a+4 > f.Start {
				return fmt.Errorf("plan write %d at 0x%08X touches %s", i+1, a, f.Name)
			}
		}
	}
	return nil
}

func spanNames(ss []memSpan) string {
	var n []string
	for _, s := range ss {
		n = append(n, fmt.Sprintf("%s 0x%08X..0x%08X", s.Name, s.Start, s.End))
	}
	return strings.Join(n, ", ")
}

// actorSpan is the struct of a at its size.
func actorSpan(who string, a *actors.Actor, size uint32) memSpan {
	return memSpan{fmt.Sprintf("%s pid %d", who, a.PID), a.Addr, a.Addr + size}
}

// findBoss returns the one executing actor of proc that match accepts
// (nil: any), or a refusal: none, more than one, or still creating.
func (e *bossEnv) findBoss(who string, proc int16, match func(a *actors.Actor) (bool, error)) (*actors.Actor, error) {
	var hits []*actors.Actor
	for i := range e.walk.Actors {
		a := &e.walk.Actors[i]
		if a.Proc != proc {
			continue
		}
		if match != nil {
			ok, err := match(a)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		hits = append(hits, a)
	}
	switch {
	case len(hits) == 0:
		return nil, refuse("no actor: no %s (proc 0x%03X) in the actor queue", who, uint16(proc))
	case len(hits) > 1:
		return nil, refuse("ambiguous: %d %s actors (proc 0x%03X)", len(hits), who, uint16(proc))
	}
	a := hits[0]
	if a.InitState != 2 {
		return nil, refuse("creating: %s pid %d has mInitState %d, not 2 (executing)", who, a.PID, a.InitState)
	}
	if err := checkPID(e.m, a); err != nil {
		return nil, err
	}
	return a, nil
}

// count is how many executing actors of proc the walk holds.
func (e *bossEnv) count(proc int16) int {
	return countProc(&e.walk, proc)
}

func countProc(aw *actors.Walk, proc int16) int {
	n := 0
	for i := range aw.Actors {
		if aw.Actors[i].Proc == proc && aw.Actors[i].InitState != 3 {
			n++
		}
	}
	return n
}

// fields reads the words of one actor; the first error sticks.
type fields struct {
	m    mem.Reader
	base uint32
	err  error
}

func (f *fields) u32(off uint32) uint32 {
	if f.err != nil {
		return 0
	}
	v, err := mem.ReadU32(f.m, f.base+off)
	f.err = err
	return v
}

func (f *fields) u8(off uint32) uint8 {
	if f.err != nil {
		return 0
	}
	v, err := mem.ReadU8(f.m, f.base+off)
	f.err = err
	return v
}

func hi16(w uint32) int16 { return int16(w >> 16) }
func lo16(w uint32) int16 { return int16(w) }

// healthWord is the max_health/health word; health is the s8 at +0x285
// (f_op_actor.h:306-307).
const (
	healthOff  = addr.ActorOffMaxHealth // u32 at +0x284: max_health, health, then two more bytes
	healthMask = 0x00FF0000
)

func hpOf(w uint32) int8 { return int8(w >> 16) }

// healthTo is the planned write of health = hp, keeping max_health.
func healthTo(who string, a *actors.Actor, hp int8) pokeWord {
	return pokeWord{Who: who, Base: a.Addr, Off: healthOff, Mask: healthMask, Value: uint32(uint8(hp)) << 16, What: fmt.Sprintf("health (+0x285) = %d, max_health kept", hp)}
}

// misses joins the precondition failures into one refusal.
func misses(who string, a *actors.Actor, ms []string) error {
	return refuse("%s pid %d at 0x%08X: %s", who, a.PID, a.Addr, strings.Join(ms, "; "))
}

// pollField is a race check that reads one word.
func pollField(base, off uint32, judge func(w uint32) (raceState, string)) func(m mem.Reader) (raceState, string) {
	return func(m mem.Reader) (raceState, string) {
		w, err := mem.ReadU32(m, base+off)
		if err != nil {
			return raceBad, err.Error()
		}
		return judge(w)
	}
}

// ---------------------------------------------------------------------
// Gohma (Btd, proc 0xEB) in Xboss0.
//
// One whole word: +0x2E4 = 000B0032 (m02E4 0xB, mGohmaState 0x32), the
// pair damage_check writes (d_a_btd.cpp:470-471). move() (:1731, switch
// at :1743) runs case 0xB end() (:1762-1764) the next frame; end() sets
// mActionState 10 (:903) and case 0x32 with the start stage 'X'
// (:905-911) asks scls 0, plays the boss warp, sets GOHMA_TRIALS_CLEAR
// 0x3240 and tmp 0x0480 and stops the BGM. No health or armor poke: the
// X branch reads neither. m02F6 == 1 (a landed rock, d_a_dr2.cpp:311) is
// only a soft check: end() runs before damage_check (:1770) in the same
// move(), so the bit and the stage request fire even if damage_check
// then rewrites m02E4 (:445-451).

func planGohma(e *bossEnv) (*bossPlan, error) {
	if e.view.EventSet(evGohmaTrials) {
		return nil, already("event 0x%04X (GOHMA_TRIALS_CLEAR) is already set", evGohmaTrials)
	}
	a, err := e.findBoss("Btd", int16(stage.ProcBTD), nil)
	if err != nil {
		return nil, err
	}
	f := fields{m: e.m, base: a.Addr}
	act, rock := f.u32(btdOffAct), f.u8(btdOffRock)
	if f.err != nil {
		return nil, f.err
	}
	if hi16(act) == mailbox.BossBtdActEnd {
		return nil, already("Btd pid %d: m02E4 is already 0xB (end), mGohmaState 0x%X", a.PID, lo16(act))
	}
	if s := hi16(act); s < 0 || s > 2 {
		return nil, misses("Btd", a, []string{fmt.Sprintf("m02E4 0x%X is not 0, 1 or 2 (the fight)", s)})
	}
	p := &bossPlan{
		Key: "gohma", Target: *a, Bit: evGohmaTrials,
		Title: fmt.Sprintf("Gohma (Btd pid %d at 0x%08X): m02E4 %d, mGohmaState 0x%X, m02F6 %d", a.PID, a.Addr, hi16(act), lo16(act), rock),
		Spans: []memSpan{actorSpan("Btd", a, sizeBtd)},
		Writes: []pokeWord{
			{Who: "Btd", Base: a.Addr, Off: btdOffAct, Mask: 0xFFFFFFFF, Value: 0x000B0032, What: "m02E4 0xB (end), mGohmaState 0x32"},
		},
		Race: pollField(a.Addr, btdOffAct, func(w uint32) (raceState, string) {
			if hi16(w) == mailbox.BossBtdActEnd {
				return raceOK, fmt.Sprintf("m02E4 0xB, mGohmaState 0x%X", lo16(w))
			}
			return raceBad, fmt.Sprintf("m02E4 0x%X, not 0xB", hi16(w))
		}),
		RaceHint: "the fight rewrote m02E4 between the write and the read-back; run boss-end gohma again",
		Observe: func(m mem.Reader, o *bossObs) error {
			w, err := mem.ReadU32(m, a.Addr+btdOffAct)
			o.A, o.B = int(hi16(w)), int(lo16(w))
			o.State = fmt.Sprintf("m02E4 0x%X mGohmaState 0x%X", o.A, o.B)
			return err
		},
	}
	if rock == mailbox.BossBtdRockLand {
		p.Notes = append(p.Notes, "m02F6 is 1 (a landed rock): damage_check may rewrite m02E4 after end() this frame; the bit and the stage request still fire (d_a_btd.cpp:1762-1770)")
	}
	return p, nil
}

// ---------------------------------------------------------------------
// Kalle Demos (Bmd, proc 0xEC) in Xboss1.
//
// The lethal block of core_damage_check (d_a_bmd.cpp:609-617), each word
// read-mask-written, mMode last: m306 0x64, m310 20000 (m312 kept), mB74
// 100 (mB76 0), mBDC 1.0f, mBE0 2, then mMode 0xB, m302 0. move() case
// 0xB (:1082) runs end(), whose X branch (:519-525) reads only m302 and
// the stage name: scls 0, the boss warp, KALLE_DEMOS_TRIALS_CLEAR 0x3220
// and tmp 0x0480. The gate is the open fallen flower bk_bmd uses: mMode
// 3, m302 2..5, m304 != 0, m310 0, mB74 0 (damage(), :150-263).
// --step flower first makes force.sh's kalle gate pokes (docs/live/
// wind12-boss-relay/rig/force.sh:17-22): mMode 3 m302 2, m304 1, m310 0,
// mB74 0. --variant natural writes only health 1 (+0x285); one real sword
// hit on the core then runs core_damage_check itself (d_cc_uty.cpp:223,
// :226; d_a_bmd.cpp:591-617). Open risk: neither the head-death heart
// container (fopAcM_createDisappear, :747) nor Makar (NPC_CB1, demo_camera
// case 102, :1488, through core_move :749-753) has an X test, so the wipe
// must beat them: the watch reports DISAPPEAR, BOSSITEM and NPC_CB1.

func planKalle(e *bossEnv) (*bossPlan, error) {
	if e.view.EventSet(evKalleTrials) {
		return nil, already("event 0x%04X (KALLE_DEMOS_TRIALS_CLEAR) is already set", evKalleTrials)
	}
	a, err := e.findBoss("Bmd", int16(stage.ProcBMD), nil)
	if err != nil {
		return nil, err
	}
	f := fields{m: e.m, base: a.Addr}
	mode, core, m310, b74, hp := f.u32(bmdOffMode), f.u32(bmdOffCore), f.u32(bmdOffM310), f.u32(bmdOffB74), f.u32(healthOff)
	if f.err != nil {
		return nil, f.err
	}
	if hi16(mode) == mailbox.BossBmdModeEnd {
		return nil, already("Bmd pid %d: mMode is already 0xB (end)", a.PID)
	}
	var ms []string
	if e.o.Step == "flower" {
		if hi16(mode) == mailbox.BossBmdModeIntro {
			ms = append(ms, "mMode 10 (the intro)")
		}
	} else {
		if hi16(mode) != mailbox.BossBmdModeFallen || lo16(mode) < 2 || lo16(mode) > 5 {
			ms = append(ms, fmt.Sprintf("mMode %d m302 %d, not the open fallen flower (mMode 3, m302 2..5; --step flower pokes it)", hi16(mode), lo16(mode)))
		}
		if hi16(core) == 0 {
			ms = append(ms, "m304 0 (the core is in)")
		}
		if hi16(m310) != 0 {
			ms = append(ms, fmt.Sprintf("m310 %d != 0 (core_damage_check shut)", hi16(m310)))
		}
		if hi16(b74) != 0 {
			ms = append(ms, fmt.Sprintf("mB74 %d != 0 (a demo camera)", hi16(b74)))
		}
	}
	if len(ms) > 0 {
		return nil, misses("Bmd", a, ms)
	}
	p := &bossPlan{
		Key: "kalle", Target: *a, Bit: evKalleTrials,
		Title: fmt.Sprintf("Kalle Demos (Bmd pid %d at 0x%08X): mMode %d m302 %d, m304 %d, m310 %d, mB74 %d, health %d", a.PID, a.Addr, hi16(mode), lo16(mode), hi16(core), hi16(m310), hi16(b74), hpOf(hp)),
		Spans: []memSpan{actorSpan("Bmd", a, sizeBmd)},
		Observe: func(m mem.Reader, o *bossObs) error {
			f := fields{m: m, base: a.Addr}
			mode, core, b74 := f.u32(bmdOffMode), f.u32(bmdOffCore), f.u32(bmdOffB74)
			o.A, o.B = int(hi16(mode)), int(lo16(mode))
			o.State = fmt.Sprintf("mMode 0x%X m302 %d m306 %d mB74 %d", o.A, o.B, lo16(core), hi16(b74))
			return f.err
		},
		Scan: func(aw *actors.Walk) (int, string) {
			var seen []string
			for _, x := range []struct {
				p int16
				n string
			}{{procDisapp, "DISAPPEAR (the heart container's)"}, {procBossItem, "BOSSITEM (a heart container)"}, {procNpcCb1, "NPC_CB1 (Makar)"}} {
				if countProc(aw, x.p) > 0 {
					seen = append(seen, x.n)
				}
			}
			return 0, strings.Join(seen, ", ")
		},
	}
	if e.o.Variant == "natural" {
		p.Key = "kalle/natural"
		p.Writes = []pokeWord{healthTo("Bmd", a, 1)}
		p.Race = pollField(a.Addr, healthOff, func(w uint32) (raceState, string) {
			if hpOf(w) <= 1 {
				return raceOK, fmt.Sprintf("health %d", hpOf(w))
			}
			return raceBad, fmt.Sprintf("health %d, not 1", hpOf(w))
		})
		p.RaceHint = "health went back up; run boss-end kalle --variant natural again"
		p.Notes = append(p.Notes, "natural: land one real sword hit on the core while it is out")
		return p, nil
	}
	if e.o.Step == "flower" {
		p.Key = "kalle"
		p.Writes = append(p.Writes,
			pokeWord{Who: "Bmd", Base: a.Addr, Off: bmdOffMode, Mask: 0xFFFFFFFF, Value: 0x00030002, What: "flower: mMode 3, m302 2"},
			pokeWord{Who: "Bmd", Base: a.Addr, Off: bmdOffCore, Mask: 0xFFFF0000, Value: 0x00010000, What: "flower: m304 1, m306 kept"},
			pokeWord{Who: "Bmd", Base: a.Addr, Off: bmdOffM310, Mask: 0xFFFF0000, Value: 0, What: "flower: m310 0, m312 kept"},
			pokeWord{Who: "Bmd", Base: a.Addr, Off: bmdOffB74, Mask: 0xFFFF0000, Value: 0, What: "flower: mB74 0, mB76 kept"},
		)
	}
	p.Writes = append(p.Writes,
		pokeWord{Who: "Bmd", Base: a.Addr, Off: bmdOffCore, Mask: 0x0000FFFF, Value: mailbox.BossBmdM306Dead, What: "m306 0x64 (the head death), m304 kept"},
		pokeWord{Who: "Bmd", Base: a.Addr, Off: bmdOffM310, Mask: 0xFFFF0000, Value: mailbox.BossBmdM310Dead << 16, What: "m310 20000 (core_damage_check shut), m312 kept"},
		pokeWord{Who: "Bmd", Base: a.Addr, Off: bmdOffB74, Mask: 0xFFFFFFFF, Value: mailbox.BossBmdMB74Death << 16, What: "mB74 100 (the death camera), mB76 0"},
		pokeWord{Who: "Bmd", Base: a.Addr, Off: bmdOffBDC, Mask: 0xFFFFFFFF, Value: 0x3F800000, What: "mBDC 1.0f"},
		pokeWord{Who: "Bmd", Base: a.Addr, Off: bmdOffBE0, Mask: 0xFF000000, Value: mailbox.BossBmdMBE0Dead << 24, What: "mBE0 2, the padding kept"},
		pokeWord{Who: "Bmd", Base: a.Addr, Off: bmdOffMode, Mask: 0xFFFFFFFF, Value: mailbox.BossBmdModeEnd << 16, What: "LAST: mMode 0xB (end), m302 0"},
	)
	p.Race = pollField(a.Addr, bmdOffMode, func(w uint32) (raceState, string) {
		if hi16(w) == mailbox.BossBmdModeEnd {
			return raceOK, fmt.Sprintf("mMode 0xB m302 %d", lo16(w))
		}
		return raceBad, fmt.Sprintf("mMode 0x%X, not 0xB", hi16(w))
	})
	p.RaceHint = "the flower moved mMode on between the writes and the read-back; run boss-end kalle again (with --step flower if the flower closed)"
	return p, nil
}

// ---------------------------------------------------------------------
// Jalhalla (Bpw body, proc 0xD4, mType 0) in Xboss2.
//
// Health first (the s8 at +0x285 to 0, max_health kept), then +0x444 =
// 00030064 (mBodyAction 3 ACTION_DAMAGE, mActionState 0x64 the spike
// hit; JH-1 / P0-BPW-A). Case 100 (d_a_bpw.cpp:1994-2002) throws it, 0x65
// lands and makes `health` Poes (the loop at :2044, so none at 0) and
// sets 4/0x6E (:2057-2058); 0x6E falls into 0x6F, whose health <= 0 test
// takes the X branch (:2153-2158): JALHALLA_TRIALS_CLEAR 0x3210, tmp
// 0x0480, scls 0, the boss warp. Health must be 0 before the landing,
// else the landing makes Poes and the split must be fought out. The body
// is the first proc 0xD4 with mType 0 executing, as bk_bpw_judge takes it
// (bpwVerdict). --variant natural writes only the health; a real Mirror
// Shield freeze, carry and spike throw then reach 0x64 themselves.

func planJalhalla(e *bossEnv) (*bossPlan, error) {
	if e.view.EventSet(evJalhallaTrials) {
		return nil, already("event 0x%04X (JALHALLA_TRIALS_CLEAR) is already set", evJalhallaTrials)
	}
	a, err := e.findBoss("Bpw body", int16(stage.ProcBPW), func(a *actors.Actor) (bool, error) {
		t, err := mem.ReadU8(e.m, a.Addr+mailbox.BossBpwTypeOff)
		return t == mailbox.BossBpwTypeBody, err
	})
	if err != nil {
		return nil, err
	}
	f := fields{m: e.m, base: a.Addr}
	act, hp := f.u32(mailbox.BossBpwActionOff), f.u32(healthOff)
	if f.err != nil {
		return nil, f.err
	}
	b := bpwActor{W444: act, W284: hp}
	if b.hp() <= 0 || b.act() == mailbox.BossBpwActSeparate && b.st() >= mailbox.BossBpwStDeath {
		return nil, already("Bpw body pid %d: health %d, mBodyAction %d mActionState 0x%X (dying)", a.PID, b.hp(), b.act(), b.st())
	}
	var ms []string
	if b.act() < 0 || b.act() > 2 {
		ms = append(ms, fmt.Sprintf("mBodyAction %d is not 0..2 (the move actions)", b.act()))
	}
	if n := e.count(int16(stage.ProcPW)); n > 0 {
		ms = append(ms, fmt.Sprintf("%d Poes (proc 0xD5) alive", n))
	}
	if len(ms) > 0 {
		return nil, misses("Bpw body", a, ms)
	}
	p := &bossPlan{
		Key: "jalhalla", Target: *a, Bit: evJalhallaTrials,
		Title: fmt.Sprintf("Jalhalla (Bpw body pid %d at 0x%08X): mBodyAction %d mActionState 0x%X, health %d/%d", a.PID, a.Addr, b.act(), b.st(), b.hp(), b.maxHP()),
		Spans: []memSpan{actorSpan("Bpw", a, sizeBpw)},
		Writes: []pokeWord{
			healthTo("Bpw", a, 0),
		},
		Observe: func(m mem.Reader, o *bossObs) error {
			f := fields{m: m, base: a.Addr}
			b := bpwActor{W444: f.u32(mailbox.BossBpwActionOff), W284: f.u32(healthOff)}
			o.A, o.B = int(b.act()), int(b.st())
			o.State = fmt.Sprintf("mBodyAction %d mActionState 0x%X health %d", o.A, o.B, b.hp())
			return f.err
		},
		Scan: func(aw *actors.Walk) (int, string) { return countProc(aw, int16(stage.ProcPW)), "" },
	}
	if e.o.Variant == "natural" {
		p.Key = "jalhalla/natural"
		p.Notes = append(p.Notes, "natural: freeze it with the Mirror Shield, carry it and throw it on the spikes")
		p.Race = pollField(a.Addr, healthOff, func(w uint32) (raceState, string) {
			if hpOf(w) <= 0 {
				return raceOK, fmt.Sprintf("health %d", hpOf(w))
			}
			return raceBad, fmt.Sprintf("health %d, not 0", hpOf(w))
		})
		p.RaceHint = "run boss-end jalhalla --variant natural again"
		return p, nil
	}
	p.Writes = append(p.Writes, pokeWord{Who: "Bpw", Base: a.Addr, Off: mailbox.BossBpwActionOff, Mask: 0xFFFFFFFF, Value: 0x00030064, What: "mBodyAction 3 (ACTION_DAMAGE), mActionState 0x64 (the spike hit)"})
	p.Race = func(m mem.Reader) (raceState, string) {
		f := fields{m: m, base: a.Addr}
		b := bpwActor{W444: f.u32(mailbox.BossBpwActionOff), W284: f.u32(healthOff)}
		if f.err != nil {
			return raceBad, f.err.Error()
		}
		hit := b.act() == mailbox.BossBpwActDamage && (b.st() == mailbox.BossBpwStSpike || b.st() == mailbox.BossBpwStSpikeAir) ||
			b.act() == mailbox.BossBpwActSeparate && (b.st() == 0x6E || b.st() == 0x6F)
		if b.hp() <= 0 && hit {
			return raceOK, fmt.Sprintf("health %d, mBodyAction %d mActionState 0x%X", b.hp(), b.act(), b.st())
		}
		return raceBad, fmt.Sprintf("health %d, mBodyAction %d mActionState 0x%X (want health 0 at 3/0x64)", b.hp(), b.act(), b.st())
	}
	p.RaceHint = "if a Poe appears the split must be fought out; otherwise run boss-end jalhalla again"
	return p, nil
}

// ---------------------------------------------------------------------
// Molgera (Bwd, proc 0xDA) in Xboss3.
//
// The checked order (the first one raced): m1BB4 3 / m1BB5 3, then m1BB0
// 0 / m1BB1 0x78 (both harmless in wait: m1BB5 is never reset and m1BB4
// only at d_a_bwd.cpp:562), then m18AE 3 (sita_hit) BEFORE m18B0 2. The
// other order lets wait() case 2 (:451-463) switch to eat_attack (Link
// within 700) or reset in the gap. sita_hit case 2 (:526-563) then sees
// m1BB1 != 0 and m1BB4 >= 3, raises m1BB5 to 4 and runs the lethal block
// (:543-555: m18AE 0xC); end()'s X branch (:1021-1027) asks scls 0, plays
// the boss warp, sets MOLGERA_TRIALS_CLEAR 0x3208 and tmp 0x0480. The gate
// is wait (m18AE 1) with Link landed (m18B0 != -10, :431-438) and no demo
// camera (m3C1E 0 at the USA offset mailbox.BossBwdDemoOff; the header's
// labels past 0x1BBC are USA only). --variant natural is bk_bwd's write
// alone (m1BB4 3, m1BB5 3) during a real Hookshot pull (m18AE 3, m18B0 2,
// m1BB1 > 0).

func planMolgera(e *bossEnv) (*bossPlan, error) {
	if e.view.EventSet(evMolgeraTrials) {
		return nil, already("event 0x%04X (MOLGERA_TRIALS_CLEAR) is already set", evMolgeraTrials)
	}
	a, err := e.findBoss("Bwd", procBwd, nil)
	if err != nil {
		return nil, err
	}
	bs, err := readBwd(e.m, &actors.Walk{Actors: []actors.Actor{*a}})
	if err != nil {
		return nil, err
	}
	b := bs[0]
	if b.act() == mailbox.BossBwdActEnd || b.act() == mailbox.BossBwdActInert {
		return nil, already("Bwd pid %d: m18AE is already 0x%X (end)", a.PID, b.act())
	}
	var ms []string
	if e.o.Variant == "natural" {
		if b.act() != mailbox.BossBwdActTongue || b.step() != mailbox.BossBwdStepPulled || b.tongue() <= 0 {
			ms = append(ms, fmt.Sprintf("m18AE %d m18B0 %d m1BB1 %d, not a real pull (m18AE 3, m18B0 2, m1BB1 > 0)", b.act(), b.step(), b.tongue()))
		}
	} else {
		if b.act() != 1 {
			ms = append(ms, fmt.Sprintf("m18AE %d != 1 (wait)", b.act()))
		}
		if b.step() == -10 {
			ms = append(ms, "m18B0 -10 (Link has not landed)")
		}
	}
	if b.demo() != 0 {
		ms = append(ms, fmt.Sprintf("m3C1E %d != 0 (a demo camera)", b.demo()))
	}
	if len(ms) > 0 {
		return nil, misses("Bwd", a, ms)
	}
	p := &bossPlan{
		Key: "molgera", Target: *a, Bit: evMolgeraTrials,
		Title: fmt.Sprintf("Molgera (Bwd pid %d at 0x%08X): m18AE %d m18B0 %d m1BB1 %d m1BB4 %d m1BB5 %d m3C1E %d", a.PID, a.Addr, b.act(), b.step(), b.tongue(), b.hits(), b.crit(), b.demo()),
		Spans: []memSpan{actorSpan("Bwd", a, sizeBwd)},
		Writes: []pokeWord{
			{Who: "Bwd", Base: a.Addr, Off: bwdOffHits, Mask: 0xFFFF0000, Value: 0x03030000, What: "m1BB4 3 (hits), m1BB5 3 (critical pulls), m1BB6/7 kept"},
		},
		Race: pollField(a.Addr, bwdOffAct, func(w uint32) (raceState, string) {
			switch lo16(w) {
			case mailbox.BossBwdActTongue, mailbox.BossBwdActEnd, mailbox.BossBwdActInert:
				return raceOK, fmt.Sprintf("m18AE 0x%X", lo16(w))
			}
			return raceBad, fmt.Sprintf("m18AE 0x%X, not 3 or 0xC", lo16(w))
		}),
		RaceHint: "Molgera left wait between the writes (m18AE 5 is eat_attack, holding Link): wait for bwd-probe to show m18AE 1 again, then run boss-end molgera again",
		Observe: func(m mem.Reader, o *bossObs) error {
			bs, err := readBwd(m, &actors.Walk{Actors: []actors.Actor{*a}})
			if err != nil {
				return err
			}
			b := bs[0]
			o.A, o.B = int(b.act()), int(b.crit())
			o.State = fmt.Sprintf("m18AE 0x%X m18B0 %d m1BB5 %d", b.act(), b.step(), b.crit())
			return nil
		},
	}
	if e.o.Variant == "natural" {
		p.Key = "molgera/natural"
		return p, nil
	}
	p.Writes = append(p.Writes,
		pokeWord{Who: "Bwd", Base: a.Addr, Off: bwdOffM1BB, Mask: 0xFFFFFFFF, Value: 0x00780000, What: "m1BB0 0, m1BB1 0x78 (the tongue out), m1BB2 0, m1BB3 0"},
		pokeWord{Who: "Bwd", Base: a.Addr, Off: bwdOffAct, Mask: 0x0000FFFF, Value: mailbox.BossBwdActTongue, What: "m18AE 3 (sita_hit) BEFORE m18B0, m18AC kept"},
		pokeWord{Who: "Bwd", Base: a.Addr, Off: bwdOffStep, Mask: 0xFFFF0000, Value: mailbox.BossBwdStepPulled << 16, What: "m18B0 2 (NUKARERU_LOOP), the pad kept"},
	)
	return p, nil
}

// ---------------------------------------------------------------------
// Phantom Ganon, the maze (Fganon type 1, proc 0xF3, params 0xFFFFFA01)
// in GanonJ room 13.
//
// fail() is the type-1 death (d_a_fganon.cpp:941-1057): mAction 3 first
// (the low half of +0x384, m384 kept), then mMode 0 (+0x388 = 0, m38A has
// no reader), so the last write restarts fail() case 0; entered at mode 1
// with a looping anim it would stick. fail sets m3AE every frame, so
// damage_check returns early (:1328). The gate: mAction 5 or 9 (fly,
// fly2; not 6 shot, 11 shot2 with its orbs, 10 spinattack2 with clones),
// m408 0 (no live energy ball: energy_ball_move :2336-2340 would set
// mAction 5 mid-death), no FGMAHOU orb alive, and no Light Arrow in the
// save (standby parks a type 1 at y -20000 for good, :1065-1071;
// dComIfGs_checkGetItem's default branch scans the items,
// d_com_inf_game.cpp:997-1001). After the delete, tbox actionGenocide
// (d_a_tbox.cpp:1028-1048, 65 frames at :487) needs Link in room 13 with
// no event, then sets room 13's zone switch 232. --variant natural is
// down (mAction 8) with mMode 0; one real Master Sword hit then runs the
// type-1 kill (:1505-1509) before down's timer sends it to kieru.

func planPGMaze(e *bossEnv) (*bossPlan, error) {
	sw := func(s *state.Snapshot) bool { return zoneSwitchSet(s, 13, 232) }
	if sw(e.snap) {
		return nil, already("room 13's switch 232 (the Light Arrow chest) is already set")
	}
	if lightArrowHeld(e.snap) {
		return nil, refuse("the save holds the Light Arrow: the maze Phantom Ganon stays parked at y -20000 (d_a_fganon.cpp:1067-1071)")
	}
	a, err := e.fganonMaster(fgParamsMaze)
	if err != nil {
		return nil, err
	}
	f := fields{m: e.m, base: a.Addr}
	act, mode, ball := f.u32(fgOffAct), f.u32(fgOffMode), f.u8(fgOffBall)
	if f.err != nil {
		return nil, f.err
	}
	if lo16(act) == 3 {
		return nil, already("Fganon pid %d: mAction is already 3 (fail), mMode %d", a.PID, hi16(mode))
	}
	var ms []string
	if s := lo16(act); s != 5 && s != 9 {
		ms = append(ms, fmt.Sprintf("mAction %d is not 5 or 9 (fly, fly2)", s))
	}
	if ball != 0 {
		ms = append(ms, fmt.Sprintf("m408 %d != 0 (an energy ball is out)", int8(ball)))
	}
	if n := e.count(procFgmahou); n > 0 {
		ms = append(ms, fmt.Sprintf("%d FGMAHOU orbs alive", n))
	}
	if len(ms) > 0 {
		return nil, misses("Fganon", a, ms)
	}
	want := int16(3)
	p := &bossPlan{
		Key: "pg-maze", Target: *a, Sw: sw, SwName: "room 13 switch 232",
		Title: fmt.Sprintf("Phantom Ganon, maze (Fganon pid %d at 0x%08X): mAction %d mMode %d m408 %d", a.PID, a.Addr, lo16(act), hi16(mode), int8(ball)),
		Spans: []memSpan{actorSpan("Fganon", a, sizeFganon)},
		Writes: []pokeWord{
			{Who: "Fganon", Base: a.Addr, Off: fgOffAct, Mask: 0x0000FFFF, Value: 3, What: "mAction 3 (fail) first, m384 kept"},
			{Who: "Fganon", Base: a.Addr, Off: fgOffMode, Mask: 0xFFFFFFFF, Value: 0, What: "then mMode 0, m38A 0 (no reader)"},
		},
		RaceHint: fmt.Sprintf("Phantom Ganon left fail(); if mAction is still 3 with mMode stuck at 1, rewrite mMode with `wwdev poke-u32 %08X 00000000`, else run boss-end pg-maze again", a.Addr+fgOffMode),
		Observe:  fganonObserve(a.Addr),
	}
	p.Notes = append(p.Notes, "keep Link in room 13 with no event: the chest appears 65 frames after the delete; opening it is the Light Arrow get")
	if e.o.Variant == "natural" {
		want = 8
		p.Key = "pg-maze/natural"
		p.Writes[0] = pokeWord{Who: "Fganon", Base: a.Addr, Off: fgOffAct, Mask: 0x0000FFFF, Value: 8, What: "mAction 8 (down) first, m384 kept"}
		p.Notes = append(p.Notes, "natural: land one real Master Sword hit once m3AE (+0x3AE) is 0, before down's timer runs out")
		p.RaceHint = "run boss-end pg-maze --variant natural again once Phantom Ganon flies (mAction 5 or 9)"
	}
	p.Race = pollField(a.Addr, fgOffAct, func(w uint32) (raceState, string) {
		if lo16(w) == want {
			return raceOK, fmt.Sprintf("mAction %d", want)
		}
		return raceBad, fmt.Sprintf("mAction %d, not %d", lo16(w), want)
	})
	return p, nil
}

// fganonMaster is the one Fganon master (m68F 0) with params.
func (e *bossEnv) fganonMaster(params uint32) (*actors.Actor, error) {
	return e.findBoss("Fganon", int16(stage.ProcFganon), func(a *actors.Actor) (bool, error) {
		if a.Params != params {
			return false, nil
		}
		c, err := mem.ReadU8(e.m, a.Addr+fgOffClone)
		return c == 0, err
	})
}

func fganonObserve(at uint32) func(m mem.Reader, o *bossObs) error {
	return func(m mem.Reader, o *bossObs) error {
		f := fields{m: m, base: at}
		act, mode, b54 := f.u32(fgOffAct), f.u32(fgOffMode), f.u32(fgOffB54)
		o.A, o.B, o.C = int(lo16(act)), int(hi16(mode)), int(hi16(b54))
		o.State = fmt.Sprintf("mAction %d mMode %d mB54 %d", o.A, o.B, o.C)
		return f.err
	}
}

// zoneSwitchSet reads zone (or room) switch sw of room's zone; false
// when the room has no valid zone.
func zoneSwitchSet(s *state.Snapshot, room, sw int) bool {
	z, ok := s.ZoneFor(room)
	if !ok {
		return false
	}
	st, b, err := addr.SwitchBit(sw)
	if err != nil || (st != addr.SwitchZone && st != addr.SwitchRoom) {
		return false
	}
	return s.Save[addr.InfoOffZone+z*addr.ZoneSize+b.Off]&b.Mask != 0
}

// lightArrowHeld is dComIfGs_checkGetItem(LIGHT_ARROW)'s default branch
// over the inventory slots (d_com_inf_game.cpp:997-1001): any mItems slot
// holding it.
func lightArrowHeld(s *state.Snapshot) bool {
	for i := range 21 {
		if s.Save[addr.InfoOffSavedata+addr.OffItems+i] == save.ItemLightArrow {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------
// Phantom Ganon, the third fight (Fganon type 2, proc 0xF3, params
// 0xFF006402) in GanonM room 0, and the Vfan door (proc 0x038, swSave 24).
//
// The checked order (the first one raced into the first fight's intro
// demo): mAction 22 (last_end) FIRST, from fly or fly2 at mMode 1 with the
// fly timer m3A4[1] >= 15, so last_end mode 1 (d_a_fganon.cpp:1238-1244)
// only waits; THEN mMode 0, so last_end mode 0 (:1229-1236) sets mB54 100,
// m3A4[1] 150 and m3A4[0] 25, and the vanilla ending runs: demo_camera
// case 100, the shrink, the PG sword (:1271-1287), case 103 deletes PG
// and sets table-8 switch 0 (:1982-2013), case 150 the event reset. The
// read-back must see mB54 100 or 101: 1, 2 or 3 means last_end ran mode 1
// first and mB54++ walked into the first fight's intro (cases 1/2 at
// :1662-1690 teleport Link): load a save state. Gate: mB54 0, materialized,
// m408 0 and m671 0 (no energy or dark ball).
//
// --step door: Vfan's Execute (d_a_obj_vfan.cpp:151-196) state 1 orders
// and plays its own "Vfan" event; state 2 sets swSave 24 and 0x3A08 at the
// event's end (:189-193), and the PG sword (Type_PGANON_SWORD) deletes
// itself once 0x3A08 is set (d_a_boko.cpp:797-803). The BgW release
// (:156-160) is skipped, so its collision stays until the delete.

func planPG3(e *bossEnv) (*bossPlan, error) {
	if e.o.Step == "door" {
		return planVfan(e)
	}
	sw := func(s *state.Snapshot) bool { set, _ := save.ViewOf(s).SwitchSet(addr.TableGT, 0); return set }
	if sw(e.snap) {
		return nil, already("table 8 switch 0 (the third Phantom Ganon beaten) is already set")
	}
	a, err := e.fganonMaster(fgParamsThird)
	if err != nil {
		return nil, err
	}
	f := fields{m: e.m, base: a.Addr}
	act, mode, tm, ball, dark, shown, b54 := f.u32(fgOffAct), f.u32(fgOffMode), f.u32(fgOffTimer), f.u8(fgOffBall), f.u8(fgOffDark), f.u8(fgOffShown), f.u32(fgOffB54)
	if f.err != nil {
		return nil, f.err
	}
	if lo16(act) == 22 {
		return nil, already("Fganon pid %d: mAction is already 22 (last_end), mB54 %d", a.PID, hi16(b54))
	}
	var ms []string
	if hi16(b54) != 0 {
		ms = append(ms, fmt.Sprintf("mB54 %d != 0 (a demo runs)", hi16(b54)))
	}
	if int8(shown) != 1 {
		ms = append(ms, fmt.Sprintf("mbIsMaterialized %d != 1", int8(shown)))
	}
	if s := lo16(act); s != 5 && s != 9 {
		ms = append(ms, fmt.Sprintf("mAction %d is not 5 or 9 (fly, fly2)", s))
	}
	if hi16(mode) != 1 {
		ms = append(ms, fmt.Sprintf("mMode %d != 1", hi16(mode)))
	}
	if ball != 0 {
		ms = append(ms, fmt.Sprintf("m408 %d != 0 (an energy ball is out)", int8(ball)))
	}
	if dark != 0 {
		ms = append(ms, fmt.Sprintf("m671 %d != 0 (a dark ball is out)", int8(dark)))
	}
	if t := lo16(tm); t < 15 {
		ms = append(ms, fmt.Sprintf("m3A4[1] %d < 15 (the fly timer could run out between the writes)", t))
	}
	if len(ms) > 0 {
		return nil, misses("Fganon", a, ms)
	}
	return &bossPlan{
		Key: "pg3", Target: *a, Sw: sw, SwName: "table 8 switch 0",
		Title: fmt.Sprintf("Phantom Ganon, third fight (Fganon pid %d at 0x%08X): mAction %d mMode %d m3A4[1] %d mB54 %d", a.PID, a.Addr, lo16(act), hi16(mode), lo16(tm), hi16(b54)),
		Spans: []memSpan{actorSpan("Fganon", a, sizeFganon)},
		Writes: []pokeWord{
			{Who: "Fganon", Base: a.Addr, Off: fgOffAct, Mask: 0x0000FFFF, Value: 22, What: "mAction 22 (last_end) FIRST, m384 kept"},
			{Who: "Fganon", Base: a.Addr, Off: fgOffMode, Mask: 0xFFFF0000, Value: 0, What: "THEN mMode 0, m38A kept"},
		},
		Race: pollField(a.Addr, fgOffB54, func(w uint32) (raceState, string) {
			switch b := hi16(w); {
			case b == 100 || b == 101:
				return raceOK, fmt.Sprintf("mB54 %d", b)
			case b == 0:
				return racePending, "mB54 still 0"
			default:
				return raceBad, fmt.Sprintf("mB54 %d, not 100 or 101: last_end ran mode 1 first and walked into the first fight's intro demo", b)
			}
		}),
		RaceWait:    500 * time.Millisecond,
		PendingRace: true,
		RaceHint:    "load a save state from before the fight and run boss-end pg3 again",
		Observe:     fganonObserve(a.Addr),
	}, nil
}

func planVfan(e *bossEnv) (*bossPlan, error) {
	view := e.view
	sw := func(s *state.Snapshot) bool { set, _ := save.ViewOf(s).SwitchSet(addr.TableGT, vfanSwSave); return set }
	if view.EventSet(evVfanDoor) || sw(e.snap) {
		return nil, already("event 0x%04X or table 8 switch %d (the Vfan door) is already set", evVfanDoor, vfanSwSave)
	}
	for i := range e.walk.Actors {
		x := &e.walk.Actors[i]
		if x.Proc == int16(stage.ProcFganon) && x.Params == fgParamsThird {
			return nil, refuse("the third Phantom Ganon (pid %d) is still in the queue: end it first (boss-end pg3)", x.PID)
		}
	}
	a, err := e.findBoss("Obj_Vfan", int16(stage.ProcVfan), func(a *actors.Actor) (bool, error) {
		return a.Params&0xFF == vfanSwSave, nil
	})
	if err != nil {
		return nil, err
	}
	st, err := mem.ReadU32(e.m, a.Addr+vfanOffState)
	if err != nil {
		return nil, err
	}
	if st != 0 {
		return nil, already("Obj_Vfan pid %d: mState %d (its event is already ordered or running)", a.PID, int32(st))
	}
	return &bossPlan{
		Key: "pg3/door", Target: *a, Bit: evVfanDoor, Sw: sw, SwName: fmt.Sprintf("table 8 switch %d", vfanSwSave),
		Title: fmt.Sprintf("the Vfan door (Obj_Vfan pid %d at 0x%08X, params 0x%08X): mState 0", a.PID, a.Addr, a.Params),
		Spans: []memSpan{actorSpan("Vfan", a, sizeVfan)},
		Writes: []pokeWord{
			{Who: "Vfan", Base: a.Addr, Off: vfanOffState, Mask: 0xFFFFFFFF, Value: 1, What: "mState 1 (order its Vfan event)"},
		},
		Observe: func(m mem.Reader, o *bossObs) error {
			st, err := mem.ReadU32(m, a.Addr+vfanOffState)
			o.A = int(int32(st))
			o.State = fmt.Sprintf("mState %d", o.A)
			return err
		},
	}, nil
}

// ---------------------------------------------------------------------
// Puppet Ganon (BGN 0xF5, BGN2 0xF6, BGN3 0xF7) in GanonK room 0.
//
// Each form's own lethal action; bgn's m02B5 is never written (Execute
// rewrites it from l_HIO.m024 every frame, d_a_bgn.cpp:3346). Gate: bgn
// mCSMode (+0xCA62) 0 (no demo) and m02B5 the form's.
//   - form1, bgn: health 0, mC778/mC779 0 (what damage_check's hit path
//     zeroes, :1449-1455), then mC748 6 (hensin) / mC74A 0; hensin case 0
//     sets mCSMode 1 (:2493-2600).
//   - form2, bgn2: health 0, m0314 0, then m0312 6 (hensin, the move
//     dispatch d_a_bgn2.cpp:622-643; the lethal block :735-752); hensin
//     sets bgn->mCSMode 10, then mCC90 1 (:543, :589-591).
//   - form3, bgn3 (m0FD8A 0..2): m0FDB4 10000 first (damage_check shut,
//     d_a_bgn3.cpp:591), health 0, m0FD8C 0, then LAST m0FD8A 3 (end;
//     the lethal block :656-676). end() case 0 sets bgn->mCSMode 20
//     (:511-518); demo_camera 20-22 (d_a_bgn.cpp:3170-3227), then
//     Execute's mCC90 2 setNextStage("GanonK", 4, 0, 9) and 0x3F10
//     (:3347-3351).
// --variant natural: health 1 on the form's actor; one real Light Arrow
// into its core (AT_TYPE_LIGHT_ARROW only: d_a_bgn.cpp:3556,
// d_a_bgn2.cpp:1190/:1239, d_a_bgn3.cpp:1268-1310) then runs it.

func planPuppet(e *bossEnv) (*bossPlan, error) {
	if e.view.EventSet(evPuppetGanon) {
		return nil, already("event 0x%04X (Puppet Ganon beaten) is already set", evPuppetGanon)
	}
	form := map[string]int{"form1": 0, "form2": 1, "form3": 2}[e.o.Step]
	bgn, err := e.findBoss("Bgn", int16(stage.ProcBGN), nil)
	if err != nil {
		return nil, err
	}
	f := fields{m: e.m, base: bgn.Addr}
	fw, cs := f.u32(bgnOffForm), f.u32(bgnOffCS)
	if f.err != nil {
		return nil, f.err
	}
	cur, csMode := int8(fw>>16), int8(cs>>8)
	if uint8(fw>>24) == 0xFF {
		return nil, already("Bgn pid %d: m02B4 0xFF (the inert post-fight create)", bgn.PID)
	}
	var ms []string
	if csMode != 0 {
		ms = append(ms, fmt.Sprintf("bgn mCSMode %d != 0 (a demo runs)", csMode))
	}
	if int(cur) != form {
		ms = append(ms, fmt.Sprintf("bgn m02B5 %d: the fight is in form %d, not --step %s", cur, int(cur)+1, e.o.Step))
	}
	who, proc, size := "Bgn", int16(stage.ProcBGN), uint32(sizeBgn)
	switch form {
	case 1:
		who, proc, size = "Bgn2", int16(stage.ProcBGN2), sizeBgn2
	case 2:
		who, proc, size = "Bgn3", int16(stage.ProcBGN3), sizeBgn3
	}
	a := bgn
	if form != 0 {
		if a, err = e.findBoss(who, proc, nil); err != nil {
			return nil, err
		}
	}
	if form == 2 {
		w, err := mem.ReadU32(e.m, a.Addr+bgn3OffAct)
		if err != nil {
			return nil, err
		}
		if s := lo16(w); s < 0 || s > 2 {
			ms = append(ms, fmt.Sprintf("bgn3 m0FD8A %d is not 0..2", s))
		}
	}
	if len(ms) > 0 {
		return nil, misses(who, a, ms)
	}
	wantCS := []int{1, 10, 20}[form]
	p := &bossPlan{
		Key: "puppet/" + e.o.Step, Target: *a, Bit: evPuppetGanon,
		Title: fmt.Sprintf("Puppet Ganon %s (%s pid %d at 0x%08X; bgn pid %d m02B5 %d mCSMode %d)", e.o.Step, who, a.PID, a.Addr, bgn.PID, cur, csMode),
		Spans: []memSpan{actorSpan(who, a, size)},
		Observe: func(m mem.Reader, o *bossObs) error {
			f := fields{m: m, base: bgn.Addr}
			fw, cs := f.u32(bgnOffForm), f.u32(bgnOffCS)
			o.A, o.B = int(int8(cs>>8)), int(int8(fw>>16))
			o.State = fmt.Sprintf("bgn mCSMode %d m02B5 %d", o.A, o.B)
			return f.err
		},
		RaceWait: 500 * time.Millisecond,
		RaceHint: fmt.Sprintf("run boss-end puppet --step %s again once bgn mCSMode is 0", e.o.Step),
	}
	if e.o.Variant == "natural" {
		p.Key += "/natural"
		p.Writes = []pokeWord{healthTo(who, a, 1)}
		p.Notes = append(p.Notes, "natural: fire one real Light Arrow into this form's core")
		p.Race = pollField(a.Addr, healthOff, func(w uint32) (raceState, string) {
			if hpOf(w) <= 1 {
				return raceOK, fmt.Sprintf("health %d", hpOf(w))
			}
			return raceBad, fmt.Sprintf("health %d, not 1", hpOf(w))
		})
		p.RaceWait = 0
		return p, nil
	}
	switch form {
	case 0:
		p.Writes = []pokeWord{
			healthTo(who, a, 0),
			{Who: who, Base: a.Addr, Off: bgnOffHead, Mask: 0xFFFF0000, Value: 0, What: "mC778 0, mC779 0 (as damage_check's hit), the rest kept"},
			{Who: who, Base: a.Addr, Off: bgnOffAct, Mask: 0xFFFFFFFF, Value: 0x00060000, What: "mC748 6 (hensin), mC74A 0"},
		}
	case 1:
		p.Writes = []pokeWord{
			healthTo(who, a, 0),
			{Who: who, Base: a.Addr, Off: bgn2OffM314, Mask: 0xFFFF0000, Value: 0, What: "m0314 0, m0316 kept"},
			{Who: who, Base: a.Addr, Off: bgn2OffAct, Mask: 0x0000FFFF, Value: 6, What: "m0312 6 (hensin), m0310 kept"},
		}
	case 2:
		p.Writes = []pokeWord{
			{Who: who, Base: a.Addr, Off: bgn3OffShut, Mask: 0xFFFF0000, Value: 0x27100000, What: "m0FDB4 10000 first (damage_check shut), m0FDB6 kept"},
			healthTo(who, a, 0),
			{Who: who, Base: a.Addr, Off: bgn3OffM0FD8, Mask: 0xFFFF0000, Value: 0, What: "m0FD8C 0, m0FD8E kept"},
			{Who: who, Base: a.Addr, Off: bgn3OffAct, Mask: 0x0000FFFF, Value: 3, What: "LAST: m0FD8A 3 (end), m0FD88 kept"},
		}
	}
	p.Race = pollField(bgn.Addr, bgnOffCS, func(w uint32) (raceState, string) {
		switch c := int(int8(w >> 8)); {
		case c == 0:
			return racePending, "bgn mCSMode still 0"
		case c >= wantCS:
			return raceOK, fmt.Sprintf("bgn mCSMode %d", c)
		default:
			return raceBad, fmt.Sprintf("bgn mCSMode %d, not %d", c, wantCS)
		}
	})
	return p, nil
}

// ---------------------------------------------------------------------
// Ganondorf (Gnd, proc 0xF8) in GTower, with Zelda (PZ, proc 0xD3).
//
// Offsets from the retail REL (see the const block). Option A, the
// default, writes Gnd only: park sub (+0x2D0) at 1 if it reads 0, so
// finish (0x42D0) cannot start the demo on its own; set +0x2CC 0000001E
// (counter 0, action 0x1E finish); then clear sub once, only while
// +0x155E is still 0, so finish starts demo_camera exactly once (sub 0:
// +0x155E 0x64, sub 1, 0x4304-0x4314). demo_camera (0x54F0) orders event
// 2, puts Link in demo 0x46, fades and at frame 0x7C asks
// setNextStage("GTower", 2, 0, 9, wipe 7) (0x6944); the endhr STB then
// sets 0x3F40. --variant parry, option B: action 5 (attack_last), sub 0,
// then LAST_COMBO_WAIT into Link's mNoResetFlg1; attack_last (0x3870-
// 0x3894) reads it through g_dComIfG_gameInfo+0x5B44 (addr.
// PlayerInfo0Ptr) and switches to finish itself. demo_camera stores
// through Zelda's cached pointer (z_s_sub 0x900-0x948, stores 0x619C,
// 0x61EC-0x6218), so no PZ, no run. The REL layout has never run live.

func planGanondorf(e *bossEnv) (*bossPlan, error) {
	if e.view.EventSet(evEndhr) {
		return nil, already("event 0x%04X (endhr) is already set", evEndhr)
	}
	a, err := e.findBoss("Gnd", int16(stage.ProcGND), nil)
	if err != nil {
		return nil, err
	}
	f := fields{m: e.m, base: a.Addr}
	demoID, live, act, sub, demo := f.u8(gndOffDemoID), f.u8(gndOffLive), f.u32(gndOffAct), f.u32(gndOffSub), f.u32(gndOffDemo)
	if f.err != nil {
		return nil, f.err
	}
	if lo16(act) == gndActFinish || lo16(demo) != 0 && lo16(demo) >= 0x64 && lo16(demo) <= 0x66 {
		return nil, already("Gnd pid %d: action 0x%X, demo mode 0x%X (the finish already runs)", a.PID, lo16(act), lo16(demo))
	}
	var ms []string
	if demoID != 0 {
		ms = append(ms, fmt.Sprintf("+0x1C0 %d != 0 (the intro's STB drives it)", demoID))
	}
	if live == 0 {
		ms = append(ms, "+0x2B4 0 (Execute forces action 0x14)")
	}
	if lo16(demo) != 0 {
		ms = append(ms, fmt.Sprintf("+0x155E 0x%X != 0 (a Gnd demo runs)", lo16(demo)))
	}
	switch lo16(act) {
	case 0, 1, 2, 3, 4, 5, 0xA, 0xB, 0x14, 0x15:
	default:
		ms = append(ms, fmt.Sprintf("action +0x2CE 0x%X is no action gnd_move knows (the REL layout may differ)", lo16(act)))
	}
	if e.count(int16(stage.ProcPZ)) == 0 {
		ms = append(ms, "no Zelda (PZ, proc 0xD3): the ending stores through her pointer")
	}
	var link uint32
	if e.o.Variant == "parry" {
		var err error
		if link, err = mem.ReadU32(e.m, addr.PlayerInfo0Ptr); err != nil {
			return nil, err
		}
		if link == 0 || link != e.snap.PlayerPtr {
			ms = append(ms, fmt.Sprintf("mPlayerInfo[0] 0x%08X is not mpPlayerPtr[0] 0x%08X (Link is not the controlled player)", link, e.snap.PlayerPtr))
		}
	}
	if len(ms) > 0 {
		return nil, misses("Gnd", a, ms)
	}
	p := &bossPlan{
		Key: "ganondorf", Target: *a, Bit: evEndhr,
		Title: fmt.Sprintf("Ganondorf (Gnd pid %d at 0x%08X): action 0x%X sub %d demo 0x%X", a.PID, a.Addr, lo16(act), hi16(sub), lo16(demo)),
		Spans: []memSpan{actorSpan("Gnd", a, sizeGnd)},
		Observe: func(m mem.Reader, o *bossObs) error {
			f := fields{m: m, base: a.Addr}
			act, sub, demo, fr := f.u32(gndOffAct), f.u32(gndOffSub), f.u32(gndOffDemo), f.u32(gndOffFrame)
			o.A, o.B, o.C = int(lo16(demo)), int(hi16(fr)), int(lo16(act))
			o.State = fmt.Sprintf("action 0x%X sub %d demo 0x%X", lo16(act), hi16(sub), lo16(demo))
			return f.err
		},
		Race: pollField(a.Addr, gndOffDemo, func(w uint32) (raceState, string) {
			switch d := lo16(w); {
			case d >= 0x64 && d <= 0x66:
				return raceOK, fmt.Sprintf("+0x155E 0x%X", d)
			case d == 0:
				return racePending, "+0x155E still 0"
			default:
				return raceBad, fmt.Sprintf("+0x155E 0x%X, not 0x64..0x66", d)
			}
		}),
		RaceWait: time.Second,
		RaceHint: "check peek of Gnd +0x2CC/+0x2D0/+0x155C; the REL layout has never run live",
		Warning:  "the ending (endhr, then ENDING) ends this game's co-op session",
	}
	if e.o.Variant == "parry" {
		p.Key = "ganondorf/parry"
		p.Spans = append(p.Spans, memSpan{"Link's mNoResetFlg1", link + linkOffNoReset1, link + linkOffNoReset1 + 4})
		p.Writes = []pokeWord{
			{Who: "Gnd", Base: a.Addr, Off: gndOffAct, Mask: 0xFFFFFFFF, Value: gndActLast, What: "counter 0, action 5 (attack_last)"},
			{Who: "Gnd", Base: a.Addr, Off: gndOffSub, Mask: 0xFFFF0000, Value: 0, What: "sub 0, +0x2D2 kept"},
			{Who: "Link", Base: link, Off: linkOffNoReset1, Mask: linkLastCombo, Value: linkLastCombo, What: "mNoResetFlg1 |= LAST_COMBO_WAIT (the parry's flag)"},
		}
		return p, nil
	}
	p.Writes = []pokeWord{
		{Who: "Gnd", Base: a.Addr, Off: gndOffSub, Mask: 0xFFFF0000, Value: 0x00010000, What: "park sub at 1, +0x2D2 kept",
			GuardNote: "if sub reads 0",
			Guard: func(_ mem.Reader, w uint32) (bool, string) {
				if hi16(w) == 0 {
					return true, ""
				}
				return false, fmt.Sprintf("sub is %d, not 0: already parked", hi16(w))
			}},
		{Who: "Gnd", Base: a.Addr, Off: gndOffAct, Mask: 0xFFFFFFFF, Value: gndActFinish, What: "counter 0, action 0x1E (finish)"},
		{Who: "Gnd", Base: a.Addr, Off: gndOffSub, Mask: 0xFFFF0000, Value: 0, What: "sub 0 once: finish starts the demo",
			GuardNote: "while +0x155E is still 0",
			Guard: func(m mem.Reader, w uint32) (bool, string) {
				d, err := mem.ReadU32(m, a.Addr+gndOffDemo)
				if err != nil {
					return false, err.Error()
				}
				if lo16(d) != 0 {
					return false, fmt.Sprintf("+0x155E is 0x%X: the demo already started", lo16(d))
				}
				if hi16(w) == 0 {
					return false, "sub is already 0"
				}
				return true, ""
			}},
	}
	return p, nil
}

// bossReadBack is step 6: the race detector, read again every tick while
// pending until RaceWait.
func bossReadBack(ctx context.Context, w io.Writer, m mem.Reader, p *bossPlan, tick time.Duration) bossOutcome {
	if p.Race == nil {
		return bossOK
	}
	deadline := time.Now().Add(p.RaceWait)
	for {
		st, msg := p.Race(m)
		switch st {
		case raceOK:
			fmt.Fprintf(w, "read-back: %s\n", msg)
			return bossOK
		case raceBad:
			fmt.Fprintf(w, "RACE: %s\nrecovery: %s\n", msg, p.RaceHint)
			return bossRace
		}
		if !time.Now().Before(deadline) {
			if p.PendingRace {
				fmt.Fprintf(w, "RACE: %s after %v\nrecovery: %s\n", msg, p.RaceWait, p.RaceHint)
				return bossRace
			}
			fmt.Fprintf(w, "read-back: %s after %v (not yet; watching)\n", msg, p.RaceWait)
			return bossOK
		}
		select {
		case <-ctx.Done():
			return bossOK
		case <-time.After(tick):
		}
	}
}

// ---------------------------------------------------------------------
// The watch.

// stageRef is a dStage_startStage_c: name, point, room, layer.
type stageRef struct {
	Name  string
	Point int16
	Room  int8
	Layer int8
}

func (s stageRef) String() string {
	return fmt.Sprintf("%q point %d room %d layer %d", s.Name, s.Point, s.Room, s.Layer)
}

// bossObs is one watch sample. The loop fills the common part; the
// plan's Observe fills State, A, B and C.
type bossObs struct {
	Stage      stageRef
	NextEnable int8
	Next       stageRef
	Player     bool // mpPlayerPtr[0] != 0
	// Left is sticky: the stage differs from the one the run began in, or
	// a pending stage change came and went.
	Left    bool
	BitID   uint16
	Bit     bool // the ending's event bit (BitID)
	Tmp     bool // tmp 0x0480
	SwName  string
	Sw      bool
	Gone    bool // the target's pid left its address
	State   string
	A, B, C int
	Since   int // samples since State last changed
	Poes    int
	Extras  string
}

// bossStuckSamples is how long pg-maze may sit at mAction 3 mMode 1
// (about a second of 16 ms samples).
const bossStuckSamples = 60

var (
	gndNext    = stageRef{"GTower", 2, 0, 9}
	puppetNext = stageRef{"GanonK", 4, 0, 9}
)

// bossEndPhase judges one watch step: the phase lines to print (joined by
// "; "), whether the ending is complete, and why it failed. It reads only
// prev and cur.
func bossEndPhase(key string, prev, cur bossObs) (phase string, done bool, fail string) {
	var ps []string
	if cur.State != prev.State && !cur.Gone {
		ps = append(ps, cur.State)
	}
	if cur.Gone && !prev.Gone {
		ps = append(ps, "the actor is gone")
	}
	if cur.Bit && !prev.Bit {
		ps = append(ps, fmt.Sprintf("event 0x%04X set", cur.BitID))
	}
	if cur.Tmp && !prev.Tmp {
		ps = append(ps, fmt.Sprintf("tmp 0x%04X set", tmpTrials))
	}
	if cur.Sw && !prev.Sw {
		ps = append(ps, cur.SwName+" set")
	}
	if cur.NextEnable != 0 && prev.NextEnable == 0 {
		ps = append(ps, "NextEnable 1: next "+cur.Next.String())
	}
	if cur.Left && !prev.Left {
		ps = append(ps, "stage changed: now "+cur.Stage.String())
	}
	if cur.Left && cur.Player && !prev.Player {
		ps = append(ps, "Link loaded")
	}
	if cur.Extras != prev.Extras && cur.Extras != "" {
		ps = append(ps, "seen: "+cur.Extras)
	}
	if cur.Poes != prev.Poes {
		ps = append(ps, fmt.Sprintf("%d Poes", cur.Poes))
	}
	boss, rest, _ := strings.Cut(key, "/")
	if boss == "ganondorf" {
		if prev.B < 0x7C && cur.B >= 0x7C && !cur.Gone {
			ps = append(ps, "demo frame 0x7C")
		}
	}
	phase = strings.Join(ps, "; ")

	pending := cur.NextEnable != 0 && !cur.Left
	loaded := cur.Left && cur.Player
	trial := func() (bool, string) {
		if !loaded {
			return false, ""
		}
		if cur.Bit {
			return true, ""
		}
		return false, fmt.Sprintf("the stage changed without event 0x%04X", cur.BitID)
	}
	switch boss {
	case "gohma", "kalle", "molgera":
		if boss == "molgera" && !cur.Left && !cur.Gone && cur.A == 1 && cur.B == 3 {
			return phase, false, "m18AE fell back to 1 with m1BB5 3 (the tongue timer ran out): re-run boss-end molgera"
		}
		done, fail = trial()
	case "jalhalla":
		if cur.Poes > 0 && !cur.Left {
			return phase, false, fmt.Sprintf("%d Poes appeared: health was not 0 at the landing; the split must be fought out", cur.Poes)
		}
		done, fail = trial()
	case "pg-maze":
		want := 3
		if rest == "natural" {
			want = 8
		}
		if !cur.Gone && !cur.Left {
			if cur.A != want && cur.A != 3 {
				return phase, false, fmt.Sprintf("mAction %d: Phantom Ganon left %s; run boss-end pg-maze again", cur.A, map[int]string{3: "fail()", 8: "down"}[want])
			}
			if cur.A == 3 && cur.B == 1 && cur.Since >= bossStuckSamples {
				return phase, false, "mAction 3 stuck at mMode 1: rewrite mMode (+0x388) to 0"
			}
		}
		done = cur.Sw
	case "pg3":
		if rest == "door" {
			done = cur.Bit && cur.Sw
			break
		}
		if !cur.Gone && cur.C >= 1 && cur.C <= 3 {
			return phase, false, fmt.Sprintf("mB54 %d: the first fight's intro demo; load a save state", cur.C)
		}
		done = cur.Gone && cur.Sw
	case "puppet":
		step, _, _ := strings.Cut(rest, "/")
		switch step {
		case "form1", "form2":
			want := map[string]int{"form1": 1, "form2": 2}[step]
			done = !cur.Gone && cur.B >= want && cur.A == 0
		default:
			if pending && cur.Next != puppetNext {
				return phase, false, fmt.Sprintf("NextStage %s, not %s", cur.Next, puppetNext)
			}
			done, fail = trial()
		}
	case "ganondorf":
		if pending && cur.Next.Name != "ENDING" && cur.Next != gndNext {
			return phase, false, fmt.Sprintf("NextStage %s, not %s", cur.Next, gndNext)
		}
		done = cur.Bit || cur.NextEnable != 0 && cur.Next.Name == "ENDING"
	}
	return phase, done, fail
}

// bossWatch is step 7.
func bossWatch(ctx context.Context, w io.Writer, m mem.Reader, p *bossPlan, o bossEndOpts) bossOutcome {
	start := time.Now()
	startStage := p.Start
	var prev bossObs
	var aw actors.Walk
	var nextWalk time.Time
	sawNext, left := false, false
	sample := func(now time.Time, first bool) (bossObs, error) {
		s, err := state.Read(m, state.Options{})
		if err != nil {
			return bossObs{}, err
		}
		cur := bossObs{
			Stage: stageRef{s.StageName, s.StagePoint, s.StageRoom, s.StageLayer}, NextEnable: s.NextEnable,
			Player: s.PlayerPtr != 0, BitID: p.Bit, SwName: p.SwName,
		}
		cur.Next, err = readStageRef(m, addr.NextStage)
		if err != nil {
			return bossObs{}, err
		}
		if cur.NextEnable != 0 {
			sawNext = true
		}
		if cur.Stage != startStage || sawNext && cur.NextEnable == 0 {
			left = true
		}
		cur.Left = left
		view := save.ViewOf(s)
		if p.Bit != 0 {
			cur.Bit = view.EventSet(p.Bit)
		}
		tb := addr.EventByte(tmpTrials)
		cur.Tmp = s.Save[addr.InfoOffTmp+tb.Off]&tb.Mask != 0
		if p.Sw != nil {
			cur.Sw = p.Sw(s)
		}
		pid, err := mem.ReadU32(m, p.Target.Addr+addr.ActorOffBsPcID)
		cur.Gone = err != nil || pid != p.Target.PID || left
		cur.State, cur.A, cur.B, cur.C = prev.State, prev.A, prev.B, prev.C
		if !cur.Gone && p.Observe != nil {
			if err := p.Observe(m, &cur); err != nil {
				return bossObs{}, err
			}
		}
		if cur.State == prev.State && !first {
			cur.Since = prev.Since + 1
		}
		cur.Poes, cur.Extras = prev.Poes, prev.Extras
		if p.Scan != nil && !now.Before(nextWalk) {
			nextWalk = now.Add(250 * time.Millisecond)
			if aw, err = actors.Read(m, actors.MaxNodes); err == nil {
				cur.Poes, cur.Extras = p.Scan(&aw)
			}
		}
		return cur, nil
	}
	first, err := sample(start, true)
	if err != nil {
		fmt.Fprintf(w, "ERROR: watch: %v\n", err)
		return bossError
	}
	fmt.Fprintf(w, "watch (%v): %s\n", o.Watch, first.State)
	prev = first
	if _, done, fail := bossEndPhase(p.Key, first, first); fail != "" || done {
		return bossWatchEnd(w, 0, done, fail, first)
	}
	deadline := start.Add(o.Watch)
	t := time.NewTicker(o.Tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(w, "watch interrupted")
			return bossTimeout
		case now := <-t.C:
			cur, err := sample(now, false)
			if err != nil {
				fmt.Fprintf(w, "ERROR: watch: %v\n", err)
				return bossError
			}
			phase, done, fail := bossEndPhase(p.Key, prev, cur)
			if phase != "" {
				fmt.Fprintf(w, "[%6.3fs] %s\n", now.Sub(start).Seconds(), phase)
			}
			if done || fail != "" {
				return bossWatchEnd(w, now.Sub(start), done, fail, cur)
			}
			prev = cur
			if now.After(deadline) {
				fmt.Fprintf(w, "TIMEOUT: the watch ran %v without seeing the ending (last: %s; stage %s, NextEnable %d)\n", o.Watch, cur.State, cur.Stage, cur.NextEnable)
				return bossTimeout
			}
		}
	}
}

func bossWatchEnd(w io.Writer, at time.Duration, done bool, fail string, cur bossObs) bossOutcome {
	if fail != "" {
		fmt.Fprintf(w, "FAIL: %s\n", fail)
		return bossFail
	}
	fmt.Fprintf(w, "DONE after %.3fs: stage %s", at.Seconds(), cur.Stage)
	if cur.BitID != 0 {
		fmt.Fprintf(w, ", event 0x%04X %s", cur.BitID, map[bool]string{true: "set", false: "clear"}[cur.Bit])
	}
	if cur.SwName != "" {
		fmt.Fprintf(w, ", %s %s", cur.SwName, map[bool]string{true: "set", false: "clear"}[cur.Sw])
	}
	fmt.Fprintln(w)
	return bossOK
}

// readStageRef reads a dStage_startStage_c at a.
func readStageRef(m mem.Reader, a uint32) (stageRef, error) {
	b, err := m.Read(a, addr.StageSize)
	if err != nil {
		return stageRef{}, err
	}
	n := b[:addr.StageNameLen]
	if i := strings.IndexByte(string(n), 0); i >= 0 {
		n = n[:i]
	}
	return stageRef{string(n), int16(mem.U16BE(b[addr.StageOffPoint:])), int8(b[addr.StageOffRoomNo]), int8(b[addr.StageOffLayer])}, nil
}
