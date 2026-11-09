package main

// smoke --pickup: a real pickup by pad under --coop (card #1165 check 3,
// GitHub #128). The co-op sync paths for chests, keys and heart pieces
// passed live only with the source flag POKED on A (wind#88: "every gain
// on A is a POKE"). Here the game sets it: A hops into the recipe's stage,
// its room's enemies are killed and Link is put in front of the chest
// (both logged as setup, not the thing under test), a closed-loop walk
// brings him to it by stick, and A is pressed. Nothing in this file writes
// a memBit, a key count or a life value; the only writes are the pad
// shim's input, the setup's light-shrink (enemy-kill) and one mailbox warp.
//
// Verdicts, each a smoke check:
//
//	(a) opened: A's flag went clear -> set by the game. If not, the rig
//	    failed (exit 2, "the drive did not open the chest"), not co-op.
//	(b) A once: A's live count (keys: mKeyNum plus the HUD's pending
//	    mItemKeyNumCount) ends at exactly before+1: a real pickup must
//	    not be doubled by A's own derive (wind#88: after a poke A's
//	    derive added the +1 itself).
//	(c) B flag: B (kept in another stage) has the flag in its saved
//	    memBit slot for the table, and mp-local.log has B's
//	    "co-op: set tNN <table> chest N (" line.
//	(d) B derive: B's count for the table went before -> before+1, or
//	    the log has B's derive line ("wrote saved mKeyNum of table N",
//	    "wrote max-life counter"); +2 is a double.
//	(e) B's own: B's life and rupees unchanged, and no verify failure or
//	    nonzero refusal in mp-local.log during the window.
//
// The control: `smoke --pickup drc-key3 -- --coop=new --coop-dry-run`
// must fail at (c): a dry run binds but writes nothing.

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/combat"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/diag"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/mods/enemysync"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// pickupKind is what a recipe's target flag is.
type pickupKind int

const (
	pickupTbox pickupKind = iota // a chest: dSv_memBit_c mTbox bit
	pickupItem                   // a free item: mItem bit (no drive yet)
)

func (k pickupKind) String() string {
	if k == pickupItem {
		return "item"
	}
	return "tbox"
}

// pickupDerive is the count a recipe's pickup moves.
type pickupDerive int

const (
	deriveNone pickupDerive = iota
	deriveKey               // the table's small keys (mKeyNum)
	deriveLife              // max life +1 quarter heart (a heart piece)
)

func (d pickupDerive) String() string {
	switch d {
	case deriveKey:
		return "key"
	case deriveLife:
		return "life"
	}
	return "none"
}

// pickupRecipe is one --pickup recipe: where A goes, what it takes, what
// that should move, and the setup before the drive.
type pickupRecipe struct {
	Name  string
	Stage string // A's stage-hop: stage, point, room
	Point int16
	Room  int8
	Kind  pickupKind
	Table int // save table (addr.Table*)
	No    int // chest (tbox) or item number
	// Derive is the count the pickup moves on both games.
	Derive pickupDerive
	// KillProcs are the enemy procs the setup kills in Room (enemy-kill).
	KillProcs []int16
	// Hint, if set, picks among chests sharing No (never expected: chest
	// bits are per table) and is logged as a distance; it is not trusted
	// to be the chest.
	Hint *vec
	// Note says what is not verified about the recipe.
	Note string
	// Timeouts: A idle after the hop, the walk, the text loop, B's sync.
	IdleTimeout, WalkTimeout, TextTimeout, SyncTimeout time.Duration
}

// pickupRecipes are the recipes --pickup knows.
var pickupRecipes = []pickupRecipe{
	{
		// The PC rig's save state (REL CACDFFF7, card "Abahbob" = CryZe
		// 28-Hyrule3, DRC small-key chests untaken). stage-hop M_NewD2 9 6
		// puts Link in room 6 at about (-4.6, 3500, 2306) with 3
		// Bokoblins. Max life is 80, capped: keys only on this state.
		Name: "drc-key3", Stage: "M_NewD2", Point: 9, Room: 6,
		Kind: pickupTbox, Table: addr.TableDRC, No: 3, Derive: deriveKey,
		KillProcs: []int16{0x00BE}, // bk
		Hint:      &vec{-2552.7, 2850.0, -1317.6},
		Note:      "the hint is wind82's \"DRC chest 3\" (docs/live/wind82/cond.sh:57-62), never verified to be chest 3: the chest is found by its params",
	},
	{
		// wind#88 P3 poked this one: `tbox 12:5` in coop-tables --section
		// hearts, A "in Cave01 (table 12, room 0)". Needs a card before
		// that heart piece with life below the cap (CryZe 16-AfterET).
		Name: "cave01-heart", Stage: "Cave01", Point: 0, Room: 0,
		Kind: pickupTbox, Table: addr.TableSubdungeon, No: 5, Derive: deriveLife,
		Note: "the start (Cave01 point 0 room 0) is unverified (the disc check refuses a bad one), its enemies are unknown (no kill setup), and it needs the 16-AfterET card's save state",
	},
}

// defaults fills r's zero timeouts.
func (r pickupRecipe) defaults() pickupRecipe {
	set := func(d *time.Duration, v time.Duration) {
		if *d == 0 {
			*d = v
		}
	}
	set(&r.IdleTimeout, 30*time.Second)
	set(&r.WalkTimeout, 15*time.Second)
	set(&r.TextTimeout, 30*time.Second)
	set(&r.SyncTimeout, 30*time.Second)
	return r
}

// errPickupNeedsCoop is --pickup without co-op after --: smoke refuses it
// with exit 2 (smokeUsageExit).
var errPickupNeedsCoop = errors.New("--pickup needs co-op: add -- --coop=new (or a world) for mp-local")

// parsePickupArg reads --pickup's <recipe>[,<recipe>]; "" is none.
func parsePickupArg(s string, coop bool) ([]pickupRecipe, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var out []pickupRecipe
	for _, n := range strings.Split(s, ",") {
		n = strings.TrimSpace(n)
		i := slices.IndexFunc(pickupRecipes, func(r pickupRecipe) bool { return strings.EqualFold(r.Name, n) })
		if i < 0 {
			return nil, fmt.Errorf("--pickup: no recipe %q (have %s)", n, pickupRecipeNames())
		}
		if slices.ContainsFunc(out, func(r pickupRecipe) bool { return r.Name == pickupRecipes[i].Name }) {
			return nil, fmt.Errorf("--pickup: %s twice", pickupRecipes[i].Name)
		}
		out = append(out, pickupRecipes[i].defaults())
	}
	if !coop {
		return nil, errPickupNeedsCoop
	}
	return out, nil
}

func pickupRecipeNames() string {
	var n []string
	for _, r := range pickupRecipes {
		n = append(n, r.Name)
	}
	return strings.Join(n, ", ")
}

// smokeUsageExit is smoke's usage error: exit 2 (could not run) for a
// --pickup refused for want of co-op, else exitUsage's.
func smokeUsageExit(err error) {
	if errors.Is(err, errPickupNeedsCoop) {
		fmt.Printf("ERROR: %v\n", err)
		fmt.Printf("Usage: %s %s\n", exeCmd, smokeUsage)
		os.Exit(smokeExitCannot)
	}
	exitUsage(err, smokeUsage)
}

// pickupChildEnv is mp-local's environment for o: WW_ENEMY_SYNC=0 when a
// recipe kills enemies and the smoke's own environment does not set it, as
// the wind21/82 rigs ran (the setup's kills are not co-op's enemy sync).
func pickupChildEnv(o smokeOpts, env []string, getenv func(string) string) []string {
	kills := slices.ContainsFunc(o.pickups, func(r pickupRecipe) bool { return len(r.KillProcs) > 0 })
	if !kills || getenv("WW_ENEMY_SYNC") != "" {
		return env
	}
	return append(env, "WW_ENEMY_SYNC=0")
}

// ---------------------------------------------------------------------
// What is read of a game.

// pickupVals are one game's values a recipe judges.
type pickupVals struct {
	Stage   string
	Table   int8
	Flag    bool   // the target's bit (live or saved slot, as read)
	Key     uint8  // mKeyNum of the recipe's table (live or saved)
	KeyPend int16  // mItemKeyNumCount (live reads only)
	MaxLife uint16 // quarter hearts
	LifePnd int16  // mItemMaxLifeCount
	Life    uint16
	Rupee   uint16
	Live    bool // Flag and Key are the live memBit's (the table is loaded)
}

// keyEff is the key count with the HUD's pending count.
func (v pickupVals) keyEff() int { return int(v.Key) + int(v.KeyPend) }

// lifeEff is max life with the HUD's pending count.
func (v pickupVals) lifeEff() int { return int(v.MaxLife) + int(v.LifePnd) }

// pickupBit is the recipe's target bit in a dSv_memBit_c.
func pickupBit(r pickupRecipe) (addr.Bit, error) {
	if r.Kind == pickupItem {
		return addr.MemBitItemBit(r.No)
	}
	return addr.TboxBit(r.No)
}

// readPickupVals reads m's values for r: the live memBit when m has r's
// table loaded, else its saved slot.
func readPickupVals(m mem.Reader, r pickupRecipe) (pickupVals, error) {
	var v pickupVals
	s, err := state.Read(m, state.Options{})
	if err != nil {
		return v, err
	}
	v.Stage, v.Table = s.StageName, s.Table
	v.Live = int(s.Table) == r.Table
	bit, err := pickupBit(r)
	if err != nil {
		return v, err
	}
	base := addr.LiveMemBit
	if !v.Live {
		if base, err = addr.SavedMemBitAddr(r.Table); err != nil {
			return v, err
		}
	}
	b, err := mem.ReadU8(m, base+uint32(bit.Off))
	if err != nil {
		return v, err
	}
	v.Flag = b&bit.Mask != 0
	if v.Key, err = mem.ReadU8(m, base+addr.MemBitOffKeyNum); err != nil {
		return v, err
	}
	for _, f := range []struct {
		at uint32
		to *uint16
	}{{addr.MaxLife, &v.MaxLife}, {addr.Life, &v.Life}, {addr.Rupee, &v.Rupee}} {
		if *f.to, err = mem.ReadU16(m, f.at); err != nil {
			return v, err
		}
	}
	// The pending counters are read after the counts they drain into: a
	// drain between the reads then shows before+0 for a moment, never
	// the old pending on top of the new count (a false "double").
	s2, err := state.Read(m, state.Options{})
	if err != nil {
		return v, err
	}
	if v.Live && s2.Table == s.Table {
		v.KeyPend = s2.KeyNumCount
	}
	v.LifePnd = s2.MaxLifeCount
	return v, nil
}

// ---------------------------------------------------------------------
// The chest.

// pickupChest is one TBOX actor as the drive reads it.
type pickupChest struct {
	PID, Addr uint32
	Params    uint32
	No        int
	FuncType  uint32
	Pos       vec
	Yaw       int16 // shape_angle.y: the chest faces (sin, cos) of it
}

// chestNoOf is a TBOX's chest number from its params (d_a_tbox's
// (params >> 7) & 0x1F, as replica.chestKeyOf reads it).
func chestNoOf(params uint32) int {
	return int((params >> mailbox.RefreshTboxBitShift) & mailbox.RefreshTboxBitMask)
}

// chestsOf lists w's executing TBOX actors.
func chestsOf(w *actors.Walk) []pickupChest {
	var out []pickupChest
	for _, a := range w.Actors {
		if uint16(a.Proc) != mailbox.RefreshProcTbox || a.InitState != 2 {
			continue
		}
		out = append(out, pickupChest{PID: a.PID, Addr: a.Addr, Params: a.Params, No: chestNoOf(a.Params),
			FuncType: a.Params & mailbox.RefreshTboxFuncMask, Pos: vec(a.Pos), Yaw: a.HomeAngle[1]})
	}
	return out
}

// findChest picks chest no among cs: the one there is, or with several the
// nearest to hint (none without a hint).
func findChest(cs []pickupChest, no int, hint *vec) (pickupChest, string) {
	var hits []pickupChest
	for _, c := range cs {
		if c.No == no {
			hits = append(hits, c)
		}
	}
	switch {
	case len(hits) == 1:
		return hits[0], ""
	case len(hits) == 0:
		return pickupChest{}, fmt.Sprintf("no chest %d among the %d chests loaded: %s", no, len(cs), chestList(cs))
	case hint == nil:
		return pickupChest{}, fmt.Sprintf("%d chests have number %d and the recipe has no hint: %s", len(hits), no, chestList(hits))
	}
	best := hits[0]
	for _, c := range hits[1:] {
		if vdist(c.Pos, *hint) < vdist(best.Pos, *hint) {
			best = c
		}
	}
	return best, ""
}

func chestList(cs []pickupChest) string {
	if len(cs) == 0 {
		return "none"
	}
	var s []string
	for _, c := range cs {
		s = append(s, fmt.Sprintf("chest %d (pid %d, funcType %d) at %s", c.No, c.PID, c.FuncType, c.Pos))
	}
	return strings.Join(s, "; ")
}

// chestState is a chest's live words: its action word, mFlags, and its
// d_a_tbox.rel .text base (0 when the retail fingerprint is not there).
type chestState struct {
	Same  bool // the actor at Addr is still this chest (proc and params)
	Word  uint32
	Flags uint16
	Base  uint32
}

func (s chestState) Busy() bool { return s.Flags&mailbox.RefreshTboxFlagsBusy != 0 }

func (s chestState) String() string {
	if !s.Same {
		return "the chest actor is gone or replaced"
	}
	act := fmt.Sprintf("action 0x%08X", s.Word)
	if s.Base != 0 {
		switch s.Word - s.Base {
		case mailbox.RefreshTboxTextOpenWait:
			act += " (actionOpenWait: closed, offers Open)"
		case 0x24AC:
			act += " (actionWait: open, or held by a chest claim)"
		default:
			act += fmt.Sprintf(" (.text +0x%X)", s.Word-s.Base)
		}
	}
	return fmt.Sprintf("%s, mFlags 0x%04X", act, s.Flags)
}

// readChestState reads c's live words.
func readChestState(m mem.Reader, c pickupChest) chestState {
	var s chestState
	proc, err1 := mem.ReadU16(m, c.Addr+addr.ActorOffProcName)
	params, err2 := mem.ReadU32(m, c.Addr+addr.ActorOffParameters)
	if err1 != nil || err2 != nil || proc != mailbox.RefreshProcTbox || params != c.Params {
		return s
	}
	s.Same = true
	s.Word, _ = mem.ReadU32(m, c.Addr+mailbox.RefreshTboxActFnOff)
	s.Flags, _ = mem.ReadU16(m, c.Addr+mailbox.RefreshTboxFlagsOff)
	if exec := tboxExec(m, c.Addr); exec != 0 {
		s.Base = exec - mailbox.RefreshTboxTextExec
	}
	return s
}

// tboxExec is daTbox_Execute through a's profile behind the retail
// fingerprint, or 0 (replica.chestExec's check).
func tboxExec(m mem.Reader, a uint32) uint32 {
	sm, err := mem.ReadU32(m, a+addr.ActorOffSubMtd)
	if err != nil || !addr.InMEM1(sm) {
		return 0
	}
	exec, err := mem.ReadU32(m, sm+addr.MethodOffExecute)
	if err != nil || !addr.InMEM1(exec) || exec < mailbox.RefreshTboxTextExec || !addr.InMEM1(exec+mailbox.RefreshTboxFpOff1) {
		return 0
	}
	w0, err0 := mem.ReadU32(m, exec+mailbox.RefreshTboxFpOff0)
	w1, err1 := mem.ReadU32(m, exec+mailbox.RefreshTboxFpOff1)
	if err0 != nil || err1 != nil || w0 != mailbox.RefreshTboxFpWord0 || w1 != mailbox.RefreshTboxFpWord1 {
		return 0
	}
	return exec
}

// chestFront is where the setup warp puts Link: dist in front of the
// chest along its facing (an actor at yaw a faces (sin a, cos a)), up
// lift.
func chestFront(c pickupChest, dist, lift float64) vec {
	a := float64(c.Yaw) * math.Pi / 32768
	return vec{c.Pos[0] + float32(dist*math.Sin(a)), c.Pos[1] + float32(lift), c.Pos[2] + float32(dist*math.Cos(a))}
}

// ---------------------------------------------------------------------
// The stick.

// xzDir is the levelled unit vector from a to b, ok false when they share
// a spot on the level plane.
func xzDir(a, b vec) (x, z float64, ok bool) {
	x, z = float64(b[0]-a[0]), float64(b[2]-a[2])
	l := math.Hypot(x, z)
	if l < 1e-3 {
		return 0, 0, false
	}
	return x / l, z / l, true
}

// xzDist is the level distance between a and b.
func xzDist(a, b vec) float64 { return math.Hypot(float64(a[0]-b[0]), float64(a[2]-b[2])) }

// stickToward is the main stick that walks Link from link toward target
// with the camera at eye looking at center. The game moves Link
// camera-relative: stick up (+Y) along the camera's levelled forward f =
// center - eye, stick right (+X) along its right r = f x worldUp =
// (-f.z, f.x) on the level plane (the frame lobbySpot lays the title row
// out in). So stickY = d.f and stickX = d.r for the levelled direction d
// to the target, scaled to mag (1..127). mirrorX flips X, for a game whose
// stick X runs the other way (the drive votes on it live). ok is false
// when the camera looks straight up or down or Link is on the target.
func stickToward(link, target, eye, center vec, mag float64, mirrorX bool) (sx, sy int8, ok bool) {
	fx, fz, ok1 := xzDir(eye, center)
	dx, dz, ok2 := xzDir(link, target)
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	rx, rz := -fz, fx
	x, y := dx*rx+dz*rz, dx*fx+dz*fz
	if mirrorX {
		x = -x
	}
	clamp := func(v float64) int8 { return int8(max(-127, min(127, math.Round(v*mag)))) }
	return clamp(x), clamp(y), true
}

// mirrorVote compares Link's movement (mx, mz) under a stick meant to send
// him along the levelled direction d with the camera forward f: +1 when it
// matches d reflected across f (stick X runs the other way), -1 when it
// matches d, 0 when the move is too small or d has too little sideways
// part to tell.
func mirrorVote(dx, dz, fx, fz, mx, mz float64) int {
	ml := math.Hypot(mx, mz)
	if ml < 15 {
		return 0
	}
	along := dx*fx + dz*fz
	if math.Abs(dx*(-fz)+dz*fx) < 0.3 { // sideways part of d
		return 0
	}
	// d reflected across f: 2(d.f)f - d.
	rx, rz := 2*along*fx-dx, 2*along*fz-dz
	same, refl := (mx*dx+mz*dz)/ml, (mx*rx+mz*rz)/ml
	switch {
	case refl > same+0.3:
		return 1
	case same > refl+0.3:
		return -1
	}
	return 0
}

// yawToward is the s16 yaw that faces from a to b.
func yawToward(a, b vec) int16 {
	return int16(int32(math.Round(math.Atan2(float64(b[0]-a[0]), float64(b[2]-a[2])) * 32768 / math.Pi)))
}

// yawDiffDeg is |a - b| in degrees, 0..180.
func yawDiffDeg(a, b int16) float64 {
	return math.Abs(float64(a-b)) * 180 / 32768
}

// readCamera reads the play camera's eye and center.
func readCamera(m mem.Reader) (eye, center vec, ok bool) {
	cam, err := mem.ReadU32(m, addr.CameraPtr)
	if err != nil || !addr.InMEM1(cam) {
		return eye, center, false
	}
	b, err := m.Read(cam+addr.ViewOffEye, 24)
	if err != nil || len(b) != 24 {
		return eye, center, false
	}
	for i := range 3 {
		eye[i] = mem.F32BE(b[4*i:])
		center[i] = mem.F32BE(b[12+4*i:])
	}
	for _, x := range append(eye[:], center[:]...) {
		if math.IsNaN(float64(x)) || math.Abs(float64(x)) > 1e7 {
			return eye, center, false
		}
	}
	return eye, center, true
}

// linkYaws reads Link's current.angle.y and shape_angle.y.
func linkYaws(m mem.Reader) (cur, shape int16, err error) {
	p, err := mem.ReadU32(m, addr.PlayerPtr)
	if err != nil || !addr.InMEM1(p) {
		return 0, 0, errors.New("link not loaded")
	}
	c, err := mem.ReadU16(m, p+addr.ActorOffCurAngleY)
	if err != nil {
		return 0, 0, err
	}
	s, err := mem.ReadU16(m, p+addr.ActorOffShapeAngleY)
	return int16(c), int16(s), err
}

// ---------------------------------------------------------------------
// mp-local.log.

// pickupLog is what the window of mp-local.log says.
type pickupLog struct {
	Path       string
	Available  bool // the log could be read (WW_DEV_LOGS not off)
	SetLine    string
	DeriveLine string
	Bad        []string
}

var (
	reVerifyFailed = regexp.MustCompile(`co-op: verify failed:`)
	reNonzeroBad   = regexp.MustCompile(`\b[1-9][0-9]* (?:[a-z()' -]*?)(?:refused|verify failures?)\b`)
	reRefusedWord  = regexp.MustCompile(`\brefused\b`)
	reZeroRefused  = regexp.MustCompile(`\b0 [a-z()' -]*?refused\b`)
)

// pickupSetPhrase is B's replica's line for r's flag (replica.DefaultLabel
// and logApplied: "co-op: set t03 DRC chest 3 (0x...").
func pickupSetPhrase(r pickupRecipe) string {
	what := "chest"
	if r.Kind == pickupItem {
		what = "item"
	}
	return fmt.Sprintf("co-op: set t%02d %s %s %d (", r.Table, replica.TableName(r.Table), what, r.No)
}

// pickupDerivePhrase is B's derive path's line for r ("" for none):
// replica.DeriveWrite.String after "wrote " (appliedDerived): "saved
// mKeyNum 0x803C5014 00 -> 01: ..." names the table by its saved slot.
func pickupDerivePhrase(r pickupRecipe) string {
	switch r.Derive {
	case deriveKey:
		a, err := addr.SavedMemBitAddr(r.Table)
		if err != nil {
			return ""
		}
		return fmt.Sprintf("co-op: wrote saved mKeyNum 0x%08X ", a+addr.MemBitOffKeyNum)
	case deriveLife:
		return "co-op: wrote max-life counter +1 "
	}
	return ""
}

// scanPickupLog judges lines, the window of mp-local.log, for r: B's set
// line (never the dry run's "would set"), B's derive line, and bad lines
// from either side (a verify failure, a nonzero refusal count, a refusal
// that is not a zero count).
func scanPickupLog(lines []string, r pickupRecipe) pickupLog {
	var l pickupLog
	set, der := pickupSetPhrase(r), pickupDerivePhrase(r)
	for _, ln := range lines {
		b := strings.Contains(ln, "[coop-B]")
		if b && l.SetLine == "" && strings.Contains(ln, set) {
			l.SetLine = ln
		}
		if b && der != "" && l.DeriveLine == "" && strings.Contains(ln, der) {
			l.DeriveLine = ln
		}
		if !strings.Contains(ln, "co-op") {
			continue
		}
		rest := reZeroRefused.ReplaceAllString(ln, "")
		if reVerifyFailed.MatchString(ln) || reNonzeroBad.MatchString(ln) || reRefusedWord.MatchString(rest) {
			l.Bad = append(l.Bad, ln)
		}
	}
	return l
}

// readLogFrom reads path's lines after byte off.
func readLogFrom(path string, off int64) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil, err
	}
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		out = append(out, strings.TrimRight(sc.Text(), "\r"))
	}
	return out, sc.Err()
}

// ---------------------------------------------------------------------
// Verdicts.

// pickupObs is one recipe's run, as the drive saw it.
type pickupObs struct {
	A0, A1  pickupVals // A after the hop (live) and at the end (live)
	AKeyMax int        // the highest effective key count A showed in the window
	ALifMax int        // the same for max life
	B0, B1  pickupVals // B before and at the end (saved slot)
	BKeyMax int
	BLifMax int
	BFlagAt float64 // seconds from the open to B's saved flag; -1 never
	Log     pickupLog
	Setup   string // what the setup did, for the report
}

// pickupPreSkip is the skip for a flag A already has, or "".
func pickupPreSkip(r pickupRecipe, aFlag bool) string {
	if aFlag {
		return fmt.Sprintf("A already has table %d %s %d: the pickup was taken on this save, so nothing can be proven (use a save state before it)", r.Table, r.Kind, r.No)
	}
	return ""
}

// pickupVerdicts are (a)-(e) for o. rig is set when the drive, not
// co-op, failed: the flag never rose on A.
func pickupVerdicts(r pickupRecipe, o pickupObs) (cs []smokeCheck, rig string) {
	n := "pickup " + r.Name
	if !o.A1.Flag {
		rig = fmt.Sprintf("rig: the drive did not open the chest (A's table %d %s %d still clear)", r.Table, r.Kind, r.No)
		return []smokeCheck{passIf(false, n+" opened on A", rig, map[string]any{"setup": o.Setup})}, rig
	}
	cs = append(cs, passIf(true, n+" opened on A",
		fmt.Sprintf("A's table %d %s %d went clear -> set by the game (the tool writes no memBit)", r.Table, r.Kind, r.No),
		map[string]any{"setup": o.Setup}))

	// (b) A once.
	once := func(side string, before, after, peak int, unit string) smokeCheck {
		name := fmt.Sprintf("%s %s once", n, side)
		vals := map[string]any{"before": before, "after": after, "peak": peak}
		switch {
		case after == before+1 && peak <= before+1:
			return passIf(true, name, fmt.Sprintf("%s %s %d -> %d (+1)", side, unit, before, after), vals)
		case after >= before+2 || peak >= before+2:
			return passIf(false, name, fmt.Sprintf("double: %s %s %d -> %d (peak %d): a derive added to a real pickup", side, unit, before, after, peak), vals)
		}
		return passIf(false, name, fmt.Sprintf("%s %s %d -> %d, want %d", side, unit, before, after, before+1), vals)
	}
	switch r.Derive {
	case deriveKey:
		cs = append(cs, once("A", o.A0.keyEff(), o.A1.keyEff(), max(o.AKeyMax, o.A1.keyEff()), fmt.Sprintf("table %d keys (mKeyNum + pending)", r.Table)))
	case deriveLife:
		cs = append(cs, once("A", o.A0.lifeEff(), o.A1.lifeEff(), max(o.ALifMax, o.A1.lifeEff()), "max life (quarter hearts + pending)"))
	default:
		cs = append(cs, smokeCheck{Name: n + " A once", Skipped: true, Detail: "the recipe moves no count"})
	}

	// (c) B flag.
	logNote := func(line, want string) string {
		switch {
		case !o.Log.Available:
			return "mp-local.log not read (" + o.Log.Path + ")"
		case line == "":
			return fmt.Sprintf("no %q line from B in mp-local.log", want)
		}
		return "log: " + strings.TrimSpace(line)
	}
	flagOK := o.B1.Flag && !o.B0.Flag
	logOK := !o.Log.Available || o.Log.SetLine != ""
	detail := fmt.Sprintf("B's saved table %d %s %d: %v -> %v", r.Table, r.Kind, r.No, o.B0.Flag, o.B1.Flag)
	if o.BFlagAt >= 0 {
		detail += fmt.Sprintf(" (%.1f s after the open)", o.BFlagAt)
	}
	cs = append(cs, passIf(flagOK && logOK, n+" B flag", detail+"; "+logNote(o.Log.SetLine, pickupSetPhrase(r)),
		map[string]any{"b_before": o.B0.Flag, "b_after": o.B1.Flag, "after_s": o.BFlagAt, "log": o.Log.Available}))

	// (d) B derive.
	switch r.Derive {
	case deriveKey, deriveLife:
		before, after, peak, unit := o.B0.Key, o.B1.Key, o.BKeyMax, fmt.Sprintf("saved table %d keys", r.Table)
		b0, b1, pk := int(before), int(after), max(peak, int(after))
		if r.Derive == deriveLife {
			b0, b1, pk, unit = o.B0.lifeEff(), o.B1.lifeEff(), max(o.BLifMax, o.B1.lifeEff()), "max life (quarter hearts + pending)"
		}
		name := n + " B derive"
		vals := map[string]any{"before": b0, "after": b1, "peak": pk, "line": o.Log.DeriveLine != ""}
		line := logNote(o.Log.DeriveLine, pickupDerivePhrase(r))
		switch {
		case b1 >= b0+2 || pk >= b0+2:
			cs = append(cs, passIf(false, name, fmt.Sprintf("double: B %s %d -> %d (peak %d); %s", unit, b0, b1, pk, line), vals))
		case b1 == b0+1:
			cs = append(cs, passIf(true, name, fmt.Sprintf("B %s %d -> %d; %s", unit, b0, b1, line), vals))
		default:
			cs = append(cs, passIf(false, name, fmt.Sprintf("B %s %d -> %d, want %d; %s", unit, b0, b1, b0+1, line), vals))
		}
	default:
		cs = append(cs, smokeCheck{Name: n + " B derive", Skipped: true, Detail: "the recipe moves no count"})
	}

	// (e) B's own.
	var bad []string
	if o.B1.Life != o.B0.Life {
		bad = append(bad, fmt.Sprintf("B's life %d -> %d", o.B0.Life, o.B1.Life))
	}
	if o.B1.Rupee != o.B0.Rupee {
		bad = append(bad, fmt.Sprintf("B's rupees %d -> %d", o.B0.Rupee, o.B1.Rupee))
	}
	for i, l := range o.Log.Bad {
		if i == 3 {
			bad = append(bad, fmt.Sprintf("and %d more log lines", len(o.Log.Bad)-3))
			break
		}
		bad = append(bad, "log: "+strings.TrimSpace(l))
	}
	own := fmt.Sprintf("B's life %d and rupees %d unchanged; no verify failure or refusal in the window", o.B1.Life, o.B1.Rupee)
	if !o.Log.Available {
		own += " (mp-local.log not read: the log part is unchecked)"
	}
	if len(bad) > 0 {
		own = strings.Join(bad, "; ")
	}
	cs = append(cs, passIf(len(bad) == 0, n+" B own state", own,
		map[string]any{"life": [2]uint16{o.B0.Life, o.B1.Life}, "rupee": [2]uint16{o.B0.Rupee, o.B1.Rupee}, "bad_lines": len(o.Log.Bad)}))
	return cs, ""
}

// ---------------------------------------------------------------------
// The phase.

// pickupRigFail records a check that failed for want of the rig, not of
// co-op, and makes the smoke exit 2.
func (r *smokeRun) pickupRigFail(name, why string) {
	r.add(passIf(false, name, "rig: "+why, nil))
	if r.cannot == "" {
		r.cannot = name + ": rig: " + why
	}
}

// pickups runs each --pickup recipe in turn, after the co-op bind.
func (r *smokeRun) pickups() {
	fa, errA := bind.ReadFile(r.a)
	fb, errB := bind.ReadFile(r.b)
	if err := errors.Join(errA, errB); err != nil || !allPass(bindChecks(fa, fb)) {
		// The dry-run control (--coop-dry-run binds nothing to the file)
		// still drives, so it fails where it should: at the B flag.
		log.Logf(r.rep, log.Warn, "pickup: the co-op bind checks do not all pass (%v); driving anyway, so the sync checks say what reached B", err)
	}
	if enemysync.Enabled() && slices.ContainsFunc(r.o.pickups, func(p pickupRecipe) bool { return len(p.KillProcs) > 0 }) {
		log.Logf(r.rep, log.Warn, "pickup: WW_ENEMY_SYNC is on in smoke (mp-local got WW_ENEMY_SYNC=0 unless set); the rigs that killed with enemy-kill ran without enemy sync")
	}
	judged := 0
	for _, rec := range r.o.pickups {
		if r.ctx.Err() != nil || r.cannot != "" {
			return
		}
		before := len(r.report.Checks)
		r.pickup(rec)
		for _, c := range r.report.Checks[before:] {
			if !c.Skipped {
				judged++
				break
			}
		}
	}
	// A --pickup run that judged nothing proves nothing: not a pass.
	if judged == 0 && r.cannot == "" && r.ctx.Err() == nil {
		r.cannot = "pickup: every recipe was skipped (see the SKIP lines): nothing was picked up"
	}
}

// pickup runs one recipe on A, B watching.
func (r *smokeRun) pickup(rec pickupRecipe) {
	n := "pickup " + rec.Name
	w := &reporterWriter{rep: r.rep}
	logf := func(f string, a ...any) { log.Logf(r.rep, log.Info, n+": "+f, a...) }
	if rec.Kind != pickupTbox {
		r.pickupRigFail(n, "only chest recipes have a drive")
		return
	}
	if rec.Note != "" {
		logf("note: %s", rec.Note)
	}

	// 1. Before: where both are, and the flag on A (its saved slot: A is
	// not in the table yet).
	origin, err := state.Read(r.a, state.Options{})
	if err != nil {
		r.pickupRigFail(n, "reading A: "+err.Error())
		return
	}
	if origin.StageName == rec.Stage || int(origin.Table) == rec.Table {
		r.pickupRigFail(n, fmt.Sprintf("A is already in %s: start A outside table %d so the hop is a stage change", diag.Where(origin), rec.Table))
		return
	}
	aPre, err := readPickupVals(r.a, rec)
	if err != nil {
		r.pickupRigFail(n, "reading A: "+err.Error())
		return
	}
	if why := pickupPreSkip(rec, aPre.Flag); why != "" {
		r.add(smokeCheck{Name: n, Skipped: true, Detail: why})
		return
	}
	b0, err := readPickupVals(r.b, rec)
	if err != nil {
		r.pickupRigFail(n, "reading B: "+err.Error())
		return
	}
	if b0.Live {
		r.add(smokeCheck{Name: n, Skipped: true, Detail: fmt.Sprintf("B is in %s, table %d: a chest claim (M9) holds A's chest while B shares the table; keep B in another stage", b0.Stage, rec.Table)})
		return
	}
	if b0.Flag {
		r.add(smokeCheck{Name: n, Skipped: true, Detail: fmt.Sprintf("B's saved table %d %s %d is already set: its sync cannot be seen", rec.Table, rec.Kind, rec.No)})
		return
	}
	logf("before: A in %q, saved flag %v, saved keys %d; B in %q table %d, saved flag %v, saved keys %d, max life %d, life %d, rupees %d",
		aPre.Stage, aPre.Flag, aPre.Key, b0.Stage, b0.Table, b0.Flag, b0.Key, b0.MaxLife, b0.Life, b0.Rupee)

	// 2. Hop A and wait until it is in the stage and idle.
	h := stageHop{Name: rec.Stage, Point: rec.Point, Room: rec.Room, Layer: -1}
	if err := verifyStageHop(w, launch.FromEnv().ISO, &h, true); err != nil {
		r.pickupRigFail(n, "the start: "+err.Error())
		return
	}
	// Registered before the hop: a hop that fails after A left still
	// brings it back (pickupHopBack does nothing if A never left).
	defer r.pickupHopBack(n, origin)
	if err := stageHopRun(w, r.a, h, diag.Tick); err != nil {
		r.pickupRigFail(n, fmt.Sprintf("hop A to %s %d %d: %v", h.Name, h.Point, h.Room, err))
		return
	}
	if why := r.waitIdle(r.a, rec.IdleTimeout); why != "" {
		r.pickupRigFail(n, "A never idle after the hop: "+why)
		return
	}
	a0, err := readPickupVals(r.a, rec)
	if err != nil {
		r.pickupRigFail(n, "reading A: "+err.Error())
		return
	}
	if !a0.Live {
		r.pickupRigFail(n, fmt.Sprintf("after the hop A is in %q table %d, not table %d", a0.Stage, a0.Table, rec.Table))
		return
	}
	logf("A in %q: live flag %v (saved said %v), live keys %d (saved said %d), max life %d", a0.Stage, a0.Flag, aPre.Flag, a0.Key, aPre.Key, a0.MaxLife)
	if why := pickupPreSkip(rec, a0.Flag); why != "" {
		r.add(smokeCheck{Name: n, Skipped: true, Detail: why})
		return
	}

	// 3. Setup: the room's enemies, then Link in front of the chest.
	var setup []string
	if len(rec.KillProcs) > 0 {
		s, why := r.pickupKill(rec, w)
		setup = append(setup, s)
		if why != "" {
			r.pickupRigFail(n, "setup: "+why)
			return
		}
	}
	walk, err := actors.Read(r.a, 0)
	if err != nil {
		r.pickupRigFail(n, "actor walk: "+err.Error())
		return
	}
	chest, why := findChest(chestsOf(&walk), rec.No, rec.Hint)
	if why != "" {
		r.pickupRigFail(n, why)
		return
	}
	if yaw, err := mem.ReadU16(r.a, chest.Addr+addr.ActorOffShapeAngleY); err == nil {
		chest.Yaw = int16(yaw)
	}
	cst := readChestState(r.a, chest)
	hint := ""
	if rec.Hint != nil {
		hint = fmt.Sprintf(", %.0f from the recipe's hint", vdist(chest.Pos, *rec.Hint))
	}
	logf("setup: chest %d is pid %d params 0x%08X at %s yaw 0x%04X%s; %s", chest.No, chest.PID, chest.Params, chest.Pos, uint16(chest.Yaw), hint, cst)
	front := chestFront(chest, 150, 10)
	if why := r.pickupWarp(front); why != "" {
		r.pickupRigFail(n, "setup warp: "+why)
		return
	}
	setup = append(setup, fmt.Sprintf("warped to %s, 150 in front of chest %d (pid %d)", front, chest.No, chest.PID))
	logf("setup: %s", setup[len(setup)-1])

	// The closed-loop walk is the drive, not setup.
	if why := r.pickupApproach(chest, rec.WalkTimeout, logf); why != "" {
		r.pickupRigFail(n, "the walk: "+why)
		return
	}

	// 4. Press A; the chest opens (or an event starts) within 3 s.
	logPath, logOff := r.pickupLogStart()
	var tr gate.Tracker
	var st gate.Status
	opened := false
	for try := 1; try <= 2 && !opened && r.ctx.Err() == nil; try++ {
		if try == 2 {
			// The chest's front is taken to be +Z of its shape angle (an
			// actor at yaw a faces (sin a, cos a)); unverified for TBOX, so
			// the second try comes from the other side.
			back := chestFront(chest, -150, 10)
			logf("no opening after A; setup: warping to the chest's other side %s and walking in again", back)
			if why := r.pickupWarp(back); why != "" {
				logf("setup warp: %s", why)
				break
			}
			setup = append(setup, fmt.Sprintf("second try from %s, behind the chest's +Z", back))
			if why := r.pickupApproach(chest, rec.WalkTimeout, logf); why != "" {
				logf("the walk: %s", why)
				break
			}
		}
		sleepCtx(r.ctx, 300*time.Millisecond)
		_ = player.Press(r.a, player.Pad{Buttons: 0x0100}, 200*time.Millisecond)
		t0 := time.Now()
		for time.Since(t0) < 3*time.Second && r.ctx.Err() == nil {
			s, err := state.Read(r.a, state.Options{})
			if err == nil {
				st = tr.Observe(s)
			} else {
				st = tr.Observe(nil)
			}
			cst = readChestState(r.a, chest)
			v, _ := readPickupVals(r.a, rec)
			if cst.Busy() || v.Flag || (st.Heartbeat && !st.Idle) {
				opened = true
				logf("opening: %s; gate %s", cst, gateLine(st))
				break
			}
			sleepCtx(r.ctx, diag.TickInterval)
		}
	}
	if !opened {
		lp, _ := linkPos(r.a)
		r.pickupRigFail(n, fmt.Sprintf("the drive did not open the chest: no OPENING and no event within 3 s of A (from both sides); chest %s; Link at %s, %.0f from it; gate %s",
			cst, lp, xzDist(lp, chest.Pos), gateLine(st)))
		return
	}
	tOpen := time.Now()

	// Then A every ~700 ms until A is idle for 2 s (the item-get text),
	// counted only once the event was seen: the lid's OPENING comes
	// before it, with idle ticks already counted.
	lastPress := time.Now()
	aKeyMax, aLifMax := a0.keyEff(), a0.lifeEff()
	sawEvent := st.Heartbeat && !st.Idle
	for time.Since(tOpen) < rec.TextTimeout && r.ctx.Err() == nil {
		s, err := state.Read(r.a, state.Options{})
		if err == nil {
			st = tr.Observe(s)
		} else {
			st = tr.Observe(nil)
		}
		sawEvent = sawEvent || (st.Heartbeat && !st.Idle)
		v, verr := readPickupVals(r.a, rec)
		if verr == nil {
			aKeyMax, aLifMax = max(aKeyMax, v.keyEff()), max(aLifMax, v.lifeEff())
		}
		if itemGetOver(sawEvent, st.Idle, st.IdleRun, time.Since(tOpen), verr == nil && v.Flag) {
			break
		}
		if !st.Idle && time.Since(lastPress) >= 700*time.Millisecond {
			_ = player.Press(r.a, player.Pad{Buttons: 0x0100}, 150*time.Millisecond)
			lastPress = time.Now()
		}
		sleepCtx(r.ctx, diag.TickInterval)
	}
	_ = player.Release(r.a)
	logf("item get over after %.1f s; gate %s", time.Since(tOpen).Seconds(), gateLine(st))

	// B's window: its saved flag, its derive, the log; A's count watched
	// for a late double the whole time.
	o := pickupObs{A0: a0, B0: b0, BFlagAt: -1, AKeyMax: aKeyMax, ALifMax: aLifMax,
		BKeyMax: int(b0.Key), BLifMax: b0.lifeEff(), Setup: strings.Join(setup, "; ")}
	o.Log.Path = logPath
	// B's window starts when the item get ends, so a long item get
	// doesn't use it up; BFlagAt stays timed from the open.
	tSync := time.Now()
	for r.ctx.Err() == nil {
		el := time.Since(tOpen)
		elSync := time.Since(tSync)
		if v, err := readPickupVals(r.a, rec); err == nil {
			o.AKeyMax, o.ALifMax = max(o.AKeyMax, v.keyEff()), max(o.ALifMax, v.lifeEff())
		}
		bv, err := readPickupVals(r.b, rec)
		if err == nil {
			o.BKeyMax, o.BLifMax = max(o.BKeyMax, int(bv.Key)), max(o.BLifMax, bv.lifeEff())
			if bv.Flag && o.BFlagAt < 0 {
				o.BFlagAt = round2(el.Seconds())
			}
		}
		if logPath != "" {
			if lines, err := readLogFrom(logPath, logOff); err == nil {
				o.Log.Available = true
				l := scanPickupLog(lines, rec)
				o.Log.SetLine, o.Log.DeriveLine, o.Log.Bad = l.SetLine, l.DeriveLine, l.Bad
			}
		}
		derived := rec.Derive == deriveNone || o.Log.DeriveLine != "" ||
			(err == nil && ((rec.Derive == deriveKey && int(bv.Key) > int(b0.Key)) || (rec.Derive == deriveLife && bv.lifeEff() > b0.lifeEff())))
		done := o.BFlagAt >= 0 && (!o.Log.Available || o.Log.SetLine != "") && derived
		// Done, then 5 s more for a late double on either side.
		if elSync >= rec.SyncTimeout || (done && el >= 10*time.Second && time.Since(tOpen.Add(time.Duration(o.BFlagAt*float64(time.Second)))) >= 5*time.Second) {
			break
		}
		sleepCtx(r.ctx, 250*time.Millisecond)
	}
	if o.A1, err = readPickupVals(r.a, rec); err != nil {
		r.pickupRigFail(n, "reading A at the end: "+err.Error())
		return
	}
	if o.B1, err = readPickupVals(r.b, rec); err != nil {
		r.pickupRigFail(n, "reading B at the end: "+err.Error())
		return
	}
	if logPath == "" {
		o.Log.Path = "logs are off (WW_DEV_LOGS)"
	}
	cs, rig := pickupVerdicts(rec, o)
	if rig != "" {
		cs[0].Detail += fmt.Sprintf("; chest %s; gate %s", readChestState(r.a, chest), gateLine(st))
		r.add(cs...)
		if r.cannot == "" {
			r.cannot = n + ": " + rig
		}
		return
	}
	r.add(cs...)
}

// itemGetOver says the item get is over: A idle for 2 s (20 ticks) after
// its event was seen, or, for a chest that never ran one, 6 s idle after
// the open with the flag set.
func itemGetOver(sawEvent, idle bool, idleRun int, since time.Duration, flag bool) bool {
	switch {
	case !idle || idleRun < 20:
		return false
	case sawEvent:
		return true
	}
	return flag && since >= 6*time.Second
}

// gateLine says st in a few words.
func gateLine(st gate.Status) string {
	if w := st.Why(); w != "" {
		return w
	}
	return fmt.Sprintf("open (idle %d ticks)", st.IdleRun)
}

// waitIdle waits until m's gates allow an IDLE write, up to timeout; it
// returns why not, or "".
func (r *smokeRun) waitIdle(m mem.Mem, timeout time.Duration) string {
	var tr gate.Tracker
	var st gate.Status
	t0 := time.Now()
	for time.Since(t0) < timeout && r.ctx.Err() == nil {
		s, err := state.Read(m, state.Options{})
		if err != nil {
			st = tr.Observe(nil)
		} else {
			st = tr.Observe(s)
		}
		if st.CanWriteIdle() && st.IdleRun >= 10 {
			return ""
		}
		sleepCtx(r.ctx, diag.TickInterval)
	}
	return gateLine(st)
}

// pickupKill is the setup's kills: every executing enemy of rec's procs
// homed in rec's room, by enemy-kill's function, then a walk to see them
// gone. s says what it did; why is the failure.
func (r *smokeRun) pickupKill(rec pickupRecipe, w io.Writer) (s, why string) {
	targets := func() ([]actors.Actor, error) {
		aw, err := combat.Read(r.a, 0)
		if err != nil {
			return nil, err
		}
		var out []actors.Actor
		for _, a := range aw.Actors {
			if slices.Contains(rec.KillProcs, a.Proc) && a.Group == 2 && a.InitState == 2 && a.HomeRoom == rec.Room && a.Health > 0 {
				out = append(out, a)
			}
		}
		return out, nil
	}
	ts, err := targets()
	if err != nil {
		return "", "the actor walk: " + err.Error()
	}
	log.Logf(r.rep, log.Info, "pickup %s: setup: killing %d enemies in room %d (enemy-kill)", rec.Name, len(ts), rec.Room)
	for _, a := range ts {
		log.Logf(r.rep, log.Info, "pickup %s: setup: enemy-kill %s", rec.Name, a.String())
		if err := enemyKill(r.ctx, w, r.a, a.PID, false, 4*time.Second, diag.Tick); err != nil {
			log.Logf(r.rep, log.Warn, "pickup %s: setup: enemy-kill pid %d: %v", rec.Name, a.PID, err)
		}
	}
	left, err := targets()
	if err == nil && len(left) > 0 {
		// A shrink that took the whole watch is still running.
		sleepCtx(r.ctx, 3*time.Second)
		left, err = targets()
	}
	if err != nil {
		return "", "the actor walk: " + err.Error()
	}
	s = fmt.Sprintf("killed %d of %d enemies (procs %v) in room %d", len(ts)-len(left), len(ts), rec.KillProcs, rec.Room)
	if len(left) > 0 {
		var l []string
		for _, a := range left {
			l = append(l, a.String())
		}
		return s, fmt.Sprintf("%s; still there: %s", s, strings.Join(l, "; "))
	}
	return s, ""
}

// pickupWarp puts A's Link at p: only while A is idle and no warp is in
// flight (warp_seq == warp_ack: co-op's escape warps share the slot,
// player.Warp's note), and it checks Link landed near p.
func (r *smokeRun) pickupWarp(p vec) string {
	_, st, _, err := diag.GateSnapshots(r.a, diag.Tick)
	if err != nil {
		return err.Error()
	}
	if !st.CanWriteIdle() {
		return "A is not idle: " + gateLine(st)
	}
	seq, err1 := mem.ReadU32(r.a, mailbox.Mailbox+mailbox.OffWarpSeq)
	ack, err2 := mem.ReadU32(r.a, mailbox.Mailbox+mailbox.OffWarpAck)
	if err := errors.Join(err1, err2); err != nil {
		return err.Error()
	}
	if seq != ack {
		return fmt.Sprintf("a warp is in flight (warp_seq %d, warp_ack %d): co-op may be warping", seq, ack)
	}
	if err := player.Warp(r.a, p, 2*time.Second); err != nil {
		return err.Error()
	}
	sleepCtx(r.ctx, 700*time.Millisecond)
	lp, err := linkPos(r.a)
	if err != nil {
		return err.Error()
	}
	if d := xzDist(lp, p); d > 60 {
		return fmt.Sprintf("Link is at %s, %.0f from %s after the warp", lp, d, p)
	}
	return ""
}

// Approach thresholds: done inside pickupNear facing within
// pickupFaceDeg, or stopped by the chest's body inside pickupPromptDist
// (boxCheck offers Open within 100, d_a_tbox.cpp:553-564).
const (
	pickupNear       = 70.0
	pickupPromptDist = 95.0
	pickupFaceDeg    = 35.0
)

// pickupApproach walks A's Link to chest by stick, closed loop: every
// 100 ms the stick toward the chest relative to the camera's yaw. It
// returns why it failed, or "".
func (r *smokeRun) pickupApproach(chest pickupChest, timeout time.Duration, logf func(string, ...any)) string {
	defer func() { _ = player.Release(r.a) }()
	mirror, votes := false, 0
	var lastPos vec
	var lastDir [2]float64
	var lastF [2]float64
	haveLast := false
	var progressAt time.Time
	bestDist := math.MaxFloat64
	t0 := time.Now()
	var dist, face float64
	for time.Since(t0) < timeout && r.ctx.Err() == nil {
		lp, err := linkPos(r.a)
		if err != nil {
			return err.Error()
		}
		cur, shape, err := linkYaws(r.a)
		if err != nil {
			return err.Error()
		}
		dist = xzDist(lp, chest.Pos)
		want := yawToward(lp, chest.Pos)
		face = yawDiffDeg(shape, want)
		if dist < bestDist-5 {
			bestDist, progressAt = dist, time.Now()
		}
		if dist < pickupNear && face < pickupFaceDeg {
			_ = player.Release(r.a)
			logf("at the chest: %.0f away, facing %.0f deg off (shape 0x%04X, current 0x%04X, want 0x%04X)", dist, face, uint16(shape), uint16(cur), uint16(want))
			return ""
		}
		if dist < pickupPromptDist && face < pickupFaceDeg+10 && !progressAt.IsZero() && time.Since(progressAt) > 1500*time.Millisecond {
			_ = player.Release(r.a)
			logf("stopped by the chest at %.0f, facing %.0f deg off (shape 0x%04X, current 0x%04X, want 0x%04X)", dist, face, uint16(shape), uint16(cur), uint16(want))
			return ""
		}
		eye, center, ok := readCamera(r.a)
		if !ok {
			return "no play camera to steer by"
		}
		fx, fz, _ := xzDir(eye, center)
		if haveLast {
			v := mirrorVote(lastDir[0], lastDir[1], lastF[0], lastF[1], float64(lp[0]-lastPos[0]), float64(lp[2]-lastPos[2]))
			if v != 0 {
				votes += v
			}
			if votes >= 3 && !mirror {
				mirror, votes = true, 0
				logf("Link moves as if stick X ran the other way: steering with X mirrored")
			} else if votes <= -3 {
				votes = 0
			}
		}
		mag := 100.0
		switch {
		case dist < 120:
			mag = 40
		case dist < 250:
			mag = 70
		}
		sx, sy, ok := stickToward(lp, chest.Pos, eye, center, mag, mirror)
		if !ok {
			return "Link is on the chest or the camera looks straight down"
		}
		if err := player.Override(r.a, player.Pad{StickX: sx, StickY: sy}); err != nil {
			return "input: " + err.Error()
		}
		dx, dz, _ := xzDir(lp, chest.Pos)
		lastPos, lastDir, lastF, haveLast = lp, [2]float64{dx, dz}, [2]float64{fx, fz}, true
		sleepCtx(r.ctx, 100*time.Millisecond)
	}
	return fmt.Sprintf("not at the chest after %s: %.0f away (best %.0f), facing %.0f deg off", timeout, dist, bestDist, face)
}

// pickupLogStart is mp-local.log's path and its size now ("" with the logs
// off).
func (r *smokeRun) pickupLogStart() (string, int64) {
	root := devLogsRoot(os.Getenv)
	if root == "" || !r.started {
		return "", 0
	}
	p := filepath.Join(root, "latest", "mp-local.log")
	return p, fileSize(p)
}

// pickupHopBack sends A back where it started: the end phase's pose
// checks need both Links in one stage (a remote in another stage is
// hidden, feed/partner.go).
func (r *smokeRun) pickupHopBack(n string, origin *state.Snapshot) {
	if r.ctx.Err() != nil {
		return
	}
	_ = player.Release(r.a)
	if now, err := state.Read(r.a, state.Options{}); err == nil && now.StageName == origin.StageName {
		return
	}
	w := &reporterWriter{rep: r.rep}
	back := stageHop{Name: origin.StageName, Point: origin.StagePoint, Room: origin.StageRoom, Layer: -1}
	err := verifyStageHop(w, launch.FromEnv().ISO, &back, true)
	if err == nil {
		err = stageHopRun(w, r.a, back, diag.Tick)
	}
	if err != nil {
		r.add(passIf(false, n+" hop back", fmt.Sprintf("A back to %s %d %d: %v", back.Name, back.Point, back.Room, err), nil))
		return
	}
	log.Logf(r.rep, log.Info, "%s: A back in %s point %d room %d", n, back.Name, back.Point, back.Room)
	sleepCtx(r.ctx, 3*time.Second) // the pose feed re-arms after the stage change
}
