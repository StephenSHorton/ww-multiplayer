package main

// fullbody-globals: the S4-3 / S4-4 live check of the Stage 4 full body
// (ww-multiplayer-handoff/design/remote-link-stage4-plan.md, section 3.5
// and the section 6 table). It reads every game global the full-body
// create and delete windows save and put back (inject/rel/fullbody.c
// l_fb_snap, the heap words around playerInit, the host's linktex), plus
// the host Link observables of the pass rows, many times over about a
// second, so a value the game itself rewrites every frame is told apart
// from a missing restore.
//
// It only reads: it never writes game RAM. Memory comes through
// diag.WithMem(openDolphin, ...), so WW_DOLPHIN_PID / WW_DOLPHIN_INDEX
// pick the Dolphin, as for remote-watch.
//
// Why a command and not peek: runPeek steps whole words (a 1- or 2-byte
// peek prints nothing); the host Link rewrites l_debug_keep_pos and the
// two l_debug_*_angle every execute (d_a_player_main.cpp:11699-11703) and
// runs decMorfFrame/decMabaTimer every execute (:11285-11286), so a flat
// before/after diff cannot pass on a correct REL; and twenty one-shot
// peeks span seconds while the host drifts. Each region here is one
// contiguous read per sample, the pointers (Link, zel_basic, the ship,
// the linktex) are resolved once per sample, and the volatility class of
// each region decides how it is compared.
//
//   - snap <out.json> takes --samples reads --every apart and writes them
//     with each region's first value, change mask and distinct count;
//     --wait full|empty first waits for the body of --slot to be created
//     or deleted (body_status and the shanm WINDOWS count), then --settle
//     REL frames.
//   - diff <a.json> <b.json> [--base <noise.json>] compares two snaps
//     offline and prints a verdict per region; the roles (before / after
//     the create, after the delete) come from each snap's body_status.
//   - watch samples every enemysync.Tick, takes the first --baseline
//     seconds as the noise base, and compares the second before and after
//     each create and delete of the body in --slot as it happens.
//
// Exit codes: 0 every verdict PASS (or MASKED), 1 any FAIL, 2 the capture
// is INVALID (stage load, REL not linked, host not idle, timed out wait).
// A FAIL on a STATIC region is a missing restore: that address joins
// l_fb_snap (plan section 7, risk 2).

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/diag"
	"github.com/StephenSHorton/ww-multiplayer/mods/enemysync"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

const fullbodyGlobalsUsage = "fullbody-globals snap <out.json> [--samples 60] [--every 16ms] [--wait none|full|empty] [--settle 60] [--timeout 30s] [--slot 0] [--track name=expr ...]\n" +
	"       fullbody-globals diff <a.json> <b.json> [--base <noise.json>]\n" +
	"       fullbody-globals watch [secs=60, 0 = until Ctrl+C] [--slot 0] [--baseline 3] [--csv <path>] [--track name=expr ...]\n" +
	"  expr: 0xADDR[*][+0xOFF[*]]...:len (hex; * derefs a u32), e.g. brk=0x803CA754*+0x2F34*+0x04:8\n" +
	"  exit: 0 PASS, 1 FAIL, 2 INVALID capture"

// fgClass is how a region is compared (the diff rules in the header).
type fgClass string

const (
	fgStatic    fgClass = "STATIC"    // must not move, in a dump or between dumps
	fgIdle      fgClass = "IDLE"      // compared only where no dump saw it move
	fgTracks    fgClass = "TRACKS"    // equals a host Link field every execute
	fgVolatile  fgClass = "VOLATILE"  // runs every execute (the blink timer)
	fgNoise     fgClass = "NOISE"     // never equal-compared; must not point into the body
	fgTolerance fgClass = "TOLERANCE" // the ship: XZ within 5.0
	fgCounters  fgClass = "COUNTERS"  // the heap error counters
	fgInfo      fgClass = "INFO"      // printed, never judged (init$4884, --track)
)

// Field offsets the table and the body reads use.
const (
	fgActorOffHeap  = 0x0F0 // JKRSolidHeap* heap (inject/rel/dapy.h DAPY_HEAP, f_op_actor.h:275)
	fgLinkProcName  = 0xA9  // PROC_PLAYER: *(s16*)(L+8) of the host Link
	fgShipTolerance = 5.0   // ship_pos: |dx|, |dz| that still PASS (the KoRL bobs)
	fgShipDropped   = 50.0  // ship_pos: XZ distance that FAILs
	fgTracksMin     = 0.90  // TRACKS: share of samples that must equal the Link field
	fgDefaultSlot   = 0
	fgPlayerPtrExpr = "0x803CA754" // play.mpPlayerPtr[0] (dapy.h DAPY_G_PLAYER_PTR0; addr.PlayerPtr)
)

// fgRegion is one row of the table: a name, an address expression with
// its length, a class and where it comes from.
type fgRegion struct {
	Name  string
	Expr  string // fgExpr syntax, length included
	Class fgClass
	Cite  string
	// TRACKS only: the host Link field the region must equal, and the
	// offset of the same field in the body actor (it must never equal it).
	Pair    string
	BodyOff uint32
	F32     int    // leading f32 fields, printed as floats too
	Absent  string // the note when the pointer does not resolve (not a FAIL)

	e, pair fgExpr
}

// fgTable is every region, in print order. Plain addresses come from
// inject/rel/dapy.h (checked against fullbody.c l_fb_snap, FB_SNAP_BYTES
// 59, plus the gear); "L" below is *(u32*)0x803CA754, the host Link.
var fgTable = []fgRegion{
	// Plan section 3.5, exactly as l_fb_snap snapshots them.
	{Name: "evt_exception", Expr: "0x803C9EF8:0xC", Class: fgStatic, Cite: "play.mEvtManager.mException (dapy.h:37)"},
	{Name: "player_status", Expr: "0x803CA8D0:8", Class: fgIdle, Cite: "play.mPlayerStatus[0][0..1] (dapy.h:38); BOW_AIM set/cleared every execute (DPM:11690-11694)"},
	{Name: "direction", Expr: "0x803CA7E9:1", Class: fgStatic, Cite: "play.mDirection (dapy.h:39)"},
	{Name: "metronome", Expr: "0x803CA7EF:1", Class: fgStatic, Cite: "play.mMetronome (dapy.h:40)"},
	{Name: "last_mode", Expr: "0x803C5D5C:4", Class: fgStatic, Cite: "save.mRestart.mLastMode (dapy.h:41; d_save.h:837); upper bits 0x8000 shield, 0x4000 soup, tinkle<<16 (d_com_inf_game.cpp:673-684)"},
	{Name: "debug_keep_pos", Expr: "0x803E440C:0xC", Class: fgTracks, Cite: "l_debug_keep_pos == L+0x1F8 (dapy.h:42; DPM:11700)",
		Pair: fgPlayerPtrExpr + "*+0x1F8:0xC", BodyOff: 0x1F8, F32: 3},
	{Name: "debug_cur_angle", Expr: "0x803F6F10:6", Class: fgTracks, Cite: "l_debug_current_angle == L+0x204 (dapy.h:43; DPM:11702)",
		Pair: fgPlayerPtrExpr + "*+0x204:6", BodyOff: 0x204},
	{Name: "debug_shape_angle", Expr: "0x803F6F18:6", Class: fgTracks, Cite: "l_debug_shape_angle == L+0x20C (dapy.h:44; DPM:11701)",
		Pair: fgPlayerPtrExpr + "*+0x20C:6", BodyOff: 0x20C},
	{Name: "matanm", Expr: "0x803F6F1E:4", Class: fgVolatile, Cite: "m_maba_timer, m_maba_flg, m_eye_move_flg, m_morf_frame (dapy.h:45; DPM:11285-11286)"},
	{Name: "init4884", Expr: "0x803F6F22:1", Class: fgInfo, Cite: "init$4884, deliberately outside l_fb_snap (dapy.h:45)"},
	{Name: "dmecb_timer", Expr: "0x803F6F24:2", Class: fgStatic, Cite: "daPy_dmEcallBack_c::m_timer (dapy.h:46)"},
	{Name: "dmecb_type", Expr: "0x803F62B8:2", Class: fgStatic, Cite: "daPy_dmEcallBack_c::m_type (dapy.h:47)"},
	{Name: "pa_status", Expr: "0x803F6C44:1", Class: fgIdle, Cite: "dPa_control_c::mStatus (dapy.h:48)"},
	{Name: "zel_gear", Expr: "0x803F7710*+0x41:6", Class: fgStatic, Cite: "JAIZelBasic::zel_basic +0x41..+0x46 (dapy.h:49-51)",
		Absent: "no gear (fb_save gear_addr 0)"},
	// Saved and put back outside l_fb_snap (fullbody.c:273-290 create,
	// :463-474 delete).
	{Name: "cur_heap", Expr: "0x803F772C:4", Class: fgNoise, Cite: "JKRHeap::sCurrentHeap (dapy.h:54); restored around playerInit only"},
	{Name: "save_cur_heap", Expr: "0x803F6940:4", Class: fgNoise, Cite: "mDoExt_SaveCurrentHeap (dapy.h:55); restored around playerInit only"},
	{Name: "host_linktex", Expr: fgPlayerPtrExpr + "*+0x338*:0x20", Class: fgStatic, Cite: "*(L+0x338) ResTIMG header (WWREMOTE_FULL_LINKTEX_OFF; fb_tex_copy)"},
	{Name: "heap_counters", Expr: "0x803F68F4:0x1C", Class: fgCounters, Cite: "solid, game, zelda, command, archive, unknown, total (dapy.h:52-53; addr.HeapCountersAddr)"},
	// Host observables of the S4-3 and S4-4 pass rows.
	{Name: "restart", Expr: "0x803C5D30:0x30", Class: fgStatic, Cite: "dSv_restart_c, mLastMode at +0x2C: setRestartRoom did not run"},
	{Name: "player_ptrs", Expr: fgPlayerPtrExpr + ":0xC", Class: fgStatic, Cite: "mpPlayerPtr[0..2] Link, partner, ship (addr.go PlayOffPlayerPtr 0x48AC)"},
	{Name: "link_pos_angle", Expr: fgPlayerPtrExpr + "*+0x1F8:0x14", Class: fgIdle, Cite: "L current.pos + angle", F32: 3},
	{Name: "link_old_pos", Expr: fgPlayerPtrExpr + "*+0x1E4:0xC", Class: fgIdle, Cite: "L old.pos (dapy.h DAPY_OLD_POS)", F32: 3},
	{Name: "link_cur_proc", Expr: fgPlayerPtrExpr + "*+0x31D8:4", Class: fgIdle, Cite: "L mCurProc (wwremote.h:494); a change to a fall/land/LargeDamageUp proc = dropped"},
	{Name: "link_demo_type", Expr: fgPlayerPtrExpr + "*+0x304:2", Class: fgStatic, Cite: "L mDemo.mDemoType (dapy.h DAPY_DEMO_TYPE)"},
	{Name: "link_demo_mode", Expr: fgPlayerPtrExpr + "*+0x314:4", Class: fgStatic, Cite: "L mDemo.mDemoMode (dapy.h DAPY_DEMO_MODE)"},
	{Name: "link_mode_flg", Expr: fgPlayerPtrExpr + "*+0x3618:4", Class: fgIdle, Cite: "L mModeFlg (wwremote.h:496)"},
	{Name: "ship_pos", Expr: "0x803CA75C*+0x1F8:0xC", Class: fgTolerance, Cite: "mpPlayerPtr[2] current.pos; XZ within 5.0, y ignored", F32: 3,
		Absent: "no ship"},
	{Name: "event_bits", Expr: "0x803C522C:0x100", Class: fgStatic, Cite: "save event bits (addr.go SaveInfo+OffEvent 0x624)"},
	{Name: "tmp_event", Expr: "0x803C5D60:0x100", Class: fgIdle, Cite: "tmp event bits (InfoOffTmp 0x1158)"},
}

// fgBlocks are spans read whole once per sample; a region inside one is
// sliced from it, so the l_debug_* angles, matanm and the dmEcallBack
// timer come from one read (0x803F6F10..0x803F6F27).
var fgBlocks = []struct {
	Addr uint32
	Len  int
}{{0x803F6F10, 0x18}}

// fgRegions returns the table (parsed) plus the --track regions.
func fgRegions(tracks []fgRegion) ([]fgRegion, error) {
	out := make([]fgRegion, 0, len(fgTable)+len(tracks))
	for _, r := range append(slices.Clone(fgTable), tracks...) {
		var err error
		if r.e, err = parseFGExpr(r.Expr); err != nil {
			return nil, fmt.Errorf("%s: %v", r.Name, err)
		}
		if r.Pair != "" {
			if r.pair, err = parseFGExpr(r.Pair); err != nil {
				return nil, fmt.Errorf("%s pair: %v", r.Name, err)
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// fgOp is one step of an address expression: deref a u32, or add.
type fgOp struct {
	Deref bool
	Add   uint32
}

// fgExpr is `0xADDR[*][+0xOFF[*]]...:len`.
type fgExpr struct {
	Text string
	Base uint32
	Ops  []fgOp
	Len  int
}

func parseFGHex(s string) (uint32, error) {
	t := strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if t == "" {
		return 0, fmt.Errorf("empty number in %q", s)
	}
	v, err := strconv.ParseUint(t, 16, 32)
	if err != nil {
		return 0, fmt.Errorf("bad hex %q", s)
	}
	return uint32(v), nil
}

// parseFGExpr parses an address expression. Addresses and offsets are
// hex (0x optional); the length is 0x-hex or decimal.
func parseFGExpr(s string) (fgExpr, error) {
	e := fgExpr{Text: s}
	i := strings.LastIndexByte(s, ':')
	if i < 0 {
		return e, fmt.Errorf("expr %q: needs :len", s)
	}
	n, err := strconv.ParseUint(s[i+1:], 0, 32)
	if err != nil || n == 0 || n > 0x1000 {
		return e, fmt.Errorf("expr %q: length must be 1..0x1000", s)
	}
	e.Len = int(n)
	rest := s[:i]
	j := strings.IndexAny(rest, "*+")
	if j < 0 {
		j = len(rest)
	}
	if e.Base, err = parseFGHex(rest[:j]); err != nil {
		return e, fmt.Errorf("expr %q: %v", s, err)
	}
	for rest = rest[j:]; rest != ""; {
		switch rest[0] {
		case '*':
			e.Ops = append(e.Ops, fgOp{Deref: true})
			rest = rest[1:]
		case '+':
			k := strings.IndexAny(rest[1:], "*+")
			if k < 0 {
				k = len(rest) - 1
			}
			off, err := parseFGHex(rest[1 : 1+k])
			if err != nil {
				return e, fmt.Errorf("expr %q: %v", s, err)
			}
			e.Ops = append(e.Ops, fgOp{Add: off})
			rest = rest[1+k:]
		default:
			return e, fmt.Errorf("expr %q: unexpected %q", s, rest[:1])
		}
	}
	return e, nil
}

// pointer reports whether the expression derefs anything.
func (e *fgExpr) pointer() bool {
	return slices.ContainsFunc(e.Ops, func(o fgOp) bool { return o.Deref })
}

// resolve walks the expression; cache holds the u32s already read this
// sample, so each pointer is read once per sample. It returns the address
// or why it is unresolved.
func (e *fgExpr) resolve(m mem.Reader, cache map[uint32]uint32) (uint32, string) {
	a := e.Base
	for _, op := range e.Ops {
		if !op.Deref {
			a += op.Add
			continue
		}
		v, ok := cache[a]
		if !ok {
			if mem.CheckRange(a, 4) != nil {
				return 0, fmt.Sprintf("unresolved: 0x%08X not MEM1", a)
			}
			var err error
			if v, err = mem.ReadU32(m, a); err != nil {
				return 0, "unresolved: " + err.Error()
			}
			cache[a] = v
		}
		if !addr.InMEM1(v) {
			return 0, fmt.Sprintf("unresolved: *0x%08X = 0x%08X", a, v)
		}
		a = v
	}
	if mem.CheckRange(a, e.Len) != nil {
		return 0, fmt.Sprintf("unresolved: 0x%08X+0x%X not MEM1", a, e.Len)
	}
	return a, ""
}

// fgHex is bytes written as hex in the JSON; empty means unresolved.
type fgHex []byte

func (h fgHex) MarshalText() ([]byte, error) { return []byte(hex.EncodeToString(h)), nil }
func (h *fgHex) UnmarshalText(t []byte) error {
	b, err := hex.DecodeString(string(t))
	*h = b
	return err
}

// fgU32 is a u32 written as 0x%08X in the JSON.
type fgU32 uint32

func (v fgU32) MarshalText() ([]byte, error) { return fmt.Appendf(nil, "0x%08X", uint32(v)), nil }
func (v *fgU32) UnmarshalText(t []byte) error {
	x, err := strconv.ParseUint(string(t), 0, 32)
	*v = fgU32(x)
	return err
}

// fgBodyStatus is body_status[i] (WWREMOTE_BODY_STATUS_OFF 0x1F8 of the
// block at 0x80413420, 0x10 each).
type fgBodyStatus struct {
	Actor      fgU32  `json:"actor"`
	PID        uint32 `json:"pid"`
	State      uint8  `json:"state"`
	Err        uint8  `json:"err"`
	FramesLive uint16 `json:"frames_live"`
	Tries      uint8  `json:"tries"`
	Kind       uint8  `json:"kind"`
}

// liveFull: a FULL body LIVE or PARKED.
func (b fgBodyStatus) liveFull() bool {
	return b.Kind == mailbox.RemoteKindFull && (b.State == mailbox.RemoteBodyLive || b.State == mailbox.RemoteBodyParked)
}

func (b fgBodyStatus) text() string {
	return fmt.Sprintf("kind %s state %s err %s tries %d actor 0x%08X pid %d live %d",
		enumName(remoteKindNames, b.Kind), enumName(remoteBodyStateNames, b.State), enumName(remoteBErrNames, b.Err), b.Tries, uint32(b.Actor), b.PID, b.FramesLive)
}

// fgBodyRange is a body actor and its solid heap [HeapStart, HeapEnd).
type fgBodyRange struct {
	Actor     fgU32 `json:"actor"`
	HeapStart fgU32 `json:"heap_start"`
	HeapEnd   fgU32 `json:"heap_end"`
}

// contains: v points into the body actor or its solid heap.
func (r fgBodyRange) contains(v uint32) bool {
	a := uint32(r.Actor)
	if a != 0 && v >= a && v < a+mailbox.RemoteFullSize {
		return true
	}
	return r.HeapEnd > r.HeapStart && v >= uint32(r.HeapStart) && v < uint32(r.HeapEnd)
}

// fgShanm is the shanm header (the u32 at WWREL_SHANM_OFF 0x1C4).
type fgShanm struct {
	Word       fgU32  `json:"word"`
	Err        string `json:"err,omitempty"`
	Words      uint32 `json:"words"`
	CreateDiff uint32 `json:"create_diff"`
	DeleteDiff uint32 `json:"delete_diff"`
	DeleteScan uint32 `json:"delete_scan"`
	Windows    uint32 `json:"windows"`
	JointDiff  uint32 `json:"joint_diff"`
}

// fgMeta is printed per dump, never diffed.
type fgMeta struct {
	TMs        int64                                `json:"t_ms"`
	RELLinked  bool                                 `json:"rel_linked"`
	RELBuild   fgU32                                `json:"rel_build"`
	RELFrames  uint32                               `json:"rel_frames"`
	Stage      string                               `json:"stage"`
	Room       int8                                 `json:"room"`
	NextEnable uint8                                `json:"next_enable"`
	Link       fgU32                                `json:"link"`
	LinkProc   int16                                `json:"link_proc"`
	Bodies     [mailbox.RemoteMaxSlots]fgBodyStatus `json:"bodies"`
	Body       fgBodyRange                          `json:"body"` // the --slot body's actor and solid heap
	Shanm      fgShanm                              `json:"shanm"`
	DMC27      fgU32                                `json:"dmc_27"`
	DMCB5      fgU32                                `json:"dmc_b5"`
}

// linkOK: L is in MEM1 and is PROC_PLAYER.
func (mt *fgMeta) linkOK() bool { return addr.InMEM1(uint32(mt.Link)) && mt.LinkProc == fgLinkProcName }

// fgReadMeta reads one sample's meta for slot.
func fgReadMeta(m mem.Reader, slot int) (fgMeta, error) {
	var mt fgMeta
	rel, err := mailbox.ReadREL(m)
	if err != nil {
		return mt, err
	}
	mt.RELLinked, mt.RELBuild, mt.RELFrames = rel.Linked, fgU32(rel.BuildID), rel.Frames
	if mt.Stage, mt.Room, err = enemyScope(m); err != nil {
		return mt, err
	}
	if mt.NextEnable, err = mem.ReadU8(m, addr.NextStageEnable); err != nil {
		return mt, err
	}
	l, err := mem.ReadU32(m, addr.PlayerPtr)
	if err != nil {
		return mt, err
	}
	mt.Link = fgU32(l)
	if mem.CheckRange(l, 0x10) == nil {
		mt.LinkProc, _ = mem.ReadS16(m, l+addr.ActorOffProcName)
	}
	st, err := mailbox.ReadRemoteStatus(m)
	if err != nil {
		return mt, err
	}
	for i, b := range st.Bodies {
		mt.Bodies[i] = fgBodyStatus{Actor: fgU32(b.Actor), PID: b.PID, State: b.State, Err: b.Err, FramesLive: b.FramesLive, Tries: b.Tries, Kind: b.Kind}
	}
	mt.Body = fgReadBodyRange(m, st.Bodies[slot].Actor)
	sh, err := readShanm(m)
	if err != nil {
		return mt, err
	}
	mt.Shanm = fgShanm{Word: fgU32(sh.Word), Err: sh.Err, Words: sh.Words, CreateDiff: sh.CreateDiff, DeleteDiff: sh.DeleteDiff,
		DeleteScan: sh.DeleteScan, Windows: sh.Windows, JointDiff: sh.JointDiff}
	d27, err := mem.ReadU32(m, mailbox.RemoteDMCFullAddr)
	if err != nil {
		return mt, err
	}
	dB5, err := mem.ReadU32(m, mailbox.RemoteDMCRELAddr)
	if err != nil {
		return mt, err
	}
	mt.DMC27, mt.DMCB5 = fgU32(d27), fgU32(dB5)
	return mt, nil
}

// fgReadBodyRange reads actor's solid heap range (actor+0xF0, then
// [heap+0x30, heap+0x34)); a zero or bad actor gives what it can.
func fgReadBodyRange(m mem.Reader, actor uint32) fgBodyRange {
	r := fgBodyRange{Actor: fgU32(actor)}
	if mem.CheckRange(actor, fgActorOffHeap+4) != nil {
		return r
	}
	h, err := mem.ReadU32(m, actor+fgActorOffHeap)
	if err != nil || mem.CheckRange(h, addr.HeapOffEnd+4) != nil {
		return r
	}
	if b, err := m.Read(h+addr.HeapOffStart, 8); err == nil {
		r.HeapStart, r.HeapEnd = fgU32(mem.U32BE(b)), fgU32(mem.U32BE(b[4:]))
	}
	return r
}

// fgSample is one read of every region.
type fgSample struct {
	Meta             fgMeta
	Data, Pair, Body [][]byte // per region; nil: unresolved / none
	Addr             []uint32
	Why              []string
}

// fgReadOne reads n bytes at a, from a block read this sample when it
// lies inside one.
func fgReadOne(m mem.Reader, blocks [][]byte, a uint32, n int) ([]byte, error) {
	for i, b := range fgBlocks {
		if blocks[i] != nil && a >= b.Addr && uint64(a)+uint64(n) <= uint64(b.Addr)+uint64(b.Len) {
			o := int(a - b.Addr)
			return slices.Clone(blocks[i][o : o+n]), nil
		}
	}
	return m.Read(a, n)
}

// fgReadSample reads the meta, then every region, its TRACKS pair and the
// body's own field. t0 sets TMs.
func fgReadSample(m mem.Reader, regs []fgRegion, slot int, t0 time.Time) (fgSample, error) {
	s := fgSample{Data: make([][]byte, len(regs)), Pair: make([][]byte, len(regs)), Body: make([][]byte, len(regs)),
		Addr: make([]uint32, len(regs)), Why: make([]string, len(regs))}
	var err error
	if s.Meta, err = fgReadMeta(m, slot); err != nil {
		return s, err
	}
	s.Meta.TMs = time.Since(t0).Milliseconds()
	blocks := make([][]byte, len(fgBlocks))
	for i, b := range fgBlocks {
		if blocks[i], err = m.Read(b.Addr, b.Len); err != nil {
			return s, err
		}
	}
	cache := map[uint32]uint32{}
	body := uint32(s.Meta.Bodies[slot].Actor)
	for i := range regs {
		r := &regs[i]
		a, why := r.e.resolve(m, cache)
		if why != "" {
			s.Why[i] = why
			continue
		}
		b, err := fgReadOne(m, blocks, a, r.e.Len)
		if err != nil {
			s.Why[i] = "read: " + err.Error()
			continue
		}
		s.Data[i], s.Addr[i] = b, a
		if r.Class != fgTracks {
			continue
		}
		if pa, why := r.pair.resolve(m, cache); why == "" {
			s.Pair[i], _ = m.Read(pa, r.pair.Len)
		}
		if mem.CheckRange(body+r.BodyOff, r.e.Len) == nil && body != 0 {
			s.Body[i], _ = m.Read(body+r.BodyOff, r.e.Len)
		}
	}
	return s, nil
}

// fgDump is one region over a capture's samples.
type fgDump struct {
	Name       string  `json:"name"`
	Expr       string  `json:"expr"`
	Addr       fgU32   `json:"addr"` // of the first resolved sample
	Size       int     `json:"size"`
	Class      fgClass `json:"class"`
	Cite       string  `json:"cite"`
	F32        int     `json:"f32,omitempty"`
	Absent     string  `json:"absent,omitempty"`
	First      fgHex   `json:"first"`
	Mask       fgHex   `json:"mask"` // OR of first XOR sample_i
	Distinct   int     `json:"distinct"`
	Unresolved int     `json:"unresolved"`
	Why        string  `json:"why,omitempty"`
	Samples    []fgHex `json:"samples"`
	Addrs      []fgU32 `json:"addrs,omitempty"` // per sample, pointer expressions only
	Pair       []fgHex `json:"pair,omitempty"`  // TRACKS: the Link field, same sample
	Body       []fgHex `json:"body,omitempty"`  // TRACKS: the body's own field, same sample
}

// stats recomputes First, Mask, Distinct and Unresolved from Samples.
func (d *fgDump) stats() {
	d.First, d.Mask, d.Distinct, d.Unresolved = nil, nil, 0, 0
	seen := map[string]bool{}
	for i, s := range d.Samples {
		if len(s) == 0 {
			d.Unresolved++
			continue
		}
		if d.First == nil {
			d.First = slices.Clone(s)
			d.Mask = make(fgHex, len(s))
			if i < len(d.Addrs) {
				d.Addr = d.Addrs[i]
			}
		}
		for k := range min(len(s), len(d.First)) {
			d.Mask[k] |= d.First[k] ^ s[k]
		}
		seen[string(s)] = true
	}
	d.Distinct = len(seen)
}

// resolved: at least one sample was read.
func (d *fgDump) resolved() bool { return d.First != nil }

// fgCapture is what snap writes and diff reads.
type fgCapture struct {
	Tool     string      `json:"tool"`
	Taken    string      `json:"taken"`
	Slot     int         `json:"slot"`
	Wait     string      `json:"wait"`
	EveryMs  float64     `json:"every_ms"`
	Settle   int         `json:"settle"`
	LastBody fgBodyRange `json:"last_body"` // the last body seen while waiting (--wait empty)
	Notes    []string    `json:"notes,omitempty"`
	Regions  []fgDump    `json:"regions"`
	Meta     []fgMeta    `json:"meta"`
}

// fgBuild turns samples into a capture.
func fgBuild(samples []fgSample, regs []fgRegion, slot int) *fgCapture {
	c := &fgCapture{Tool: "wwdev fullbody-globals", Taken: time.Now().Format(time.RFC3339), Slot: slot}
	for _, s := range samples {
		c.Meta = append(c.Meta, s.Meta)
	}
	for i, r := range regs {
		d := fgDump{Name: r.Name, Expr: r.Expr, Size: r.e.Len, Class: r.Class, Cite: r.Cite, F32: r.F32, Absent: r.Absent}
		for _, s := range samples {
			d.Samples = append(d.Samples, s.Data[i])
			if r.e.pointer() {
				d.Addrs = append(d.Addrs, fgU32(s.Addr[i]))
			} else {
				d.Addr = fgU32(r.e.Base)
			}
			if r.Class == fgTracks {
				d.Pair = append(d.Pair, s.Pair[i])
				d.Body = append(d.Body, s.Body[i])
			}
			if s.Why[i] != "" && d.Why == "" {
				d.Why = s.Why[i]
			}
		}
		d.stats()
		c.Regions = append(c.Regions, d)
	}
	return c
}

// region returns the dump called name, or nil.
func (c *fgCapture) region(name string) *fgDump {
	for i := range c.Regions {
		if c.Regions[i].Name == name {
			return &c.Regions[i]
		}
	}
	return nil
}

// body is body_status[Slot] of the first sample.
func (c *fgCapture) body() fgBodyStatus {
	if len(c.Meta) == 0 || c.Slot < 0 || c.Slot >= mailbox.RemoteMaxSlots {
		return fgBodyStatus{}
	}
	return c.Meta[0].Bodies[c.Slot]
}

// bodyRanges are the body ranges this capture saw (its samples and the
// body it watched go while waiting).
func (c *fgCapture) bodyRanges() []fgBodyRange {
	var out []fgBodyRange
	for _, mt := range c.Meta {
		if mt.Body.Actor != 0 && !slices.Contains(out, mt.Body) {
			out = append(out, mt.Body)
		}
	}
	if c.LastBody.Actor != 0 && !slices.Contains(out, c.LastBody) {
		out = append(out, c.LastBody)
	}
	return out
}

// windows is the shanm WINDOWS count of the last sample.
func (c *fgCapture) windows() uint32 {
	if len(c.Meta) == 0 {
		return 0
	}
	return c.Meta[len(c.Meta)-1].Shanm.Windows
}

// fgRole is what a dump is in the create / delete cycle.
type fgRole string

const (
	fgBefore fgRole = "before" // no body, before the create
	fgAfter  fgRole = "after"  // the FULL body LIVE or PARKED
	fgDel    fgRole = "del"    // no body, after the delete (or a failed create)
)

// fgRoles infers the roles of a and b from body_status and the shanm
// windows: none -> FULL is a create, FULL -> none a delete, none -> none
// with windows risen a whole cycle or a failed create.
func fgRoles(a, b *fgCapture) (fgRole, fgRole) {
	pa, pb := a.body().liveFull(), b.body().liveFull()
	switch {
	case !pa && pb:
		return fgBefore, fgAfter
	case pa && !pb:
		return fgAfter, fgDel
	case pa && pb:
		return fgAfter, fgAfter
	case b.windows() > a.windows():
		return fgBefore, fgDel
	}
	return fgBefore, fgBefore
}

// Verdicts.
const (
	fgPass    = "PASS"
	fgFail    = "FAIL"
	fgMasked  = "MASKED"
	fgInvalid = "INVALID"
	fgInfoV   = "INFO"
)

// fgResult is one printed row.
type fgResult struct {
	Name, Addr, Size, Class, AFirst, BFirst, Masks, Verdict, Note string
}

// fgCounts tallies the verdicts.
type fgCounts struct{ Pass, Fail, Masked, Invalid int }

func (n *fgCounts) add(v string) {
	switch v {
	case fgPass:
		n.Pass++
	case fgFail:
		n.Fail++
	case fgMasked:
		n.Masked++
	case fgInvalid:
		n.Invalid++
	}
}

// code is the exit code: 2 INVALID, 1 FAIL, else 0.
func (n fgCounts) code() int {
	switch {
	case n.Invalid > 0:
		return 2
	case n.Fail > 0:
		return 1
	}
	return 0
}

// fgCaptureProblems are the reasons a capture cannot be judged at all.
func fgCaptureProblems(c *fgCapture) []string {
	if len(c.Meta) == 0 {
		return []string{"no samples"}
	}
	var why []string
	add := func(s string) {
		if !slices.Contains(why, s) {
			why = append(why, s)
		}
	}
	f := c.Meta[0]
	for _, mt := range c.Meta {
		if !mt.RELLinked {
			add("REL not linked")
		}
		if !mt.linkOK() {
			add(fmt.Sprintf("host Link 0x%08X proc 0x%X is not PROC_PLAYER 0xA9", uint32(mt.Link), uint16(mt.LinkProc)))
		}
		if mt.Stage != f.Stage || mt.Room != f.Room {
			add("stage or room changed during the capture")
		}
		if mt.NextEnable != 0 {
			add("NextStage enabled (a stage change is pending)")
		}
		if c.Slot >= 0 && c.Slot < mailbox.RemoteMaxSlots {
			b0, b := f.Bodies[c.Slot], mt.Bodies[c.Slot]
			// LIVE <-> PARKED is the same body (a pose feed hiccup parks it).
			if (b.State != b0.State && !(b.liveFull() && b0.liveFull())) || b.Kind != b0.Kind || b.Actor != b0.Actor {
				add("the body changed during the capture")
			}
		}
	}
	return why
}

// fgCompare compares a (role ra) with b (role rb); base, when set, adds
// its masks to the IDLE rule and its range to the VOLATILE notes.
func fgCompare(a, b, base *fgCapture, ra, rb fgRole) []fgResult {
	var out []fgResult
	for _, c := range []struct {
		n string
		c *fgCapture
	}{{"a", a}, {"b", b}} {
		for _, why := range fgCaptureProblems(c.c) {
			out = append(out, fgResult{Name: "capture_" + c.n, Verdict: fgInvalid, Note: why})
		}
	}
	if len(a.Meta) > 0 && len(b.Meta) > 0 && (a.Meta[0].Stage != b.Meta[0].Stage || a.Meta[0].Room != b.Meta[0].Room) {
		out = append(out, fgResult{Name: "capture", Verdict: fgInvalid,
			Note: fmt.Sprintf("stage %q room %d vs %q room %d", a.Meta[0].Stage, a.Meta[0].Room, b.Meta[0].Stage, b.Meta[0].Room)})
	}
	for i := range a.Regions {
		da := &a.Regions[i]
		db := b.region(da.Name)
		if db == nil {
			out = append(out, fgMissingRow(da, "b"))
			continue
		}
		var dbase *fgDump
		if base != nil {
			dbase = base.region(da.Name)
		}
		r := fgResult{Name: da.Name, Addr: fgAddrText(da), Size: fmt.Sprintf("0x%X", da.Size), Class: string(da.Class),
			AFirst: fgShort(da.First), BFirst: fgShort(db.First), Masks: fgMaskText(da.Mask) + "/" + fgMaskText(db.Mask)}
		r.Verdict, r.Note = fgJudge(da, db, dbase, a, b, ra, rb)
		if da.F32 > 0 && (da.resolved() || db.resolved()) {
			r.Note = strings.TrimSpace(r.Note + " a=" + fgFloats(da.First, da.F32) + " b=" + fgFloats(db.First, db.F32))
		}
		out = append(out, r)
	}
	for i := range b.Regions {
		if a.region(b.Regions[i].Name) == nil {
			out = append(out, fgMissingRow(&b.Regions[i], "a"))
		}
	}
	return out
}

// fgMissingRow is the row of a region one capture lacks (a capture from
// another table, or an edited file): a judged region cannot pass unseen,
// so it is INVALID; a --track (INFO) region is only noted.
func fgMissingRow(d *fgDump, in string) fgResult {
	r := fgResult{Name: d.Name, Addr: fgAddrText(d), Size: fmt.Sprintf("0x%X", d.Size), Class: string(d.Class), Verdict: fgInvalid,
		Note: "missing in " + in + ": re-snap both with the same table and --track list"}
	if d.Class == fgInfo {
		r.Verdict, r.Note = fgInfoV, "only in one capture (missing in "+in+")"
	}
	return r
}

// fgJudge applies the class rule to one region.
func fgJudge(da, db, dbase *fgDump, a, b *fgCapture, ra, rb fgRole) (string, string) {
	if da.Class == fgInfo {
		if bytes.Equal(da.First, db.First) && !nonzero(da.Mask) && !nonzero(db.Mask) {
			return fgInfoV, "unchanged"
		}
		return fgInfoV, "changed" + fgOffs(da.First, db.First, nil)
	}
	// Unresolved pointers.
	switch {
	case !da.resolved() && !db.resolved():
		if da.Absent != "" {
			return fgPass, da.Absent
		}
		return fgInvalid, "unresolved in both: " + da.Why
	case !da.resolved() || !db.resolved():
		if da.Class == fgTolerance {
			return fgFail, "ship pointer changed (present in one dump only)"
		}
		return fgFail, "resolved in one dump only: " + da.Why + db.Why
	}
	var notes []string
	if da.Unresolved > 0 || db.Unresolved > 0 {
		notes = append(notes, fmt.Sprintf("unresolved in %d/%d samples", da.Unresolved, db.Unresolved))
	}
	note := func(v string, s ...string) (string, string) {
		return v, strings.Join(append(s, notes...), "; ")
	}
	if p := fgAddrsMoved(da, db); p != "" {
		if da.Class == fgTolerance {
			return note(fgFail, "ship pointer changed "+p)
		}
		if da.Class != fgNoise {
			return note(fgFail, "pointer moved "+p)
		}
	}
	switch da.Class {
	case fgStatic:
		switch {
		case nonzero(da.Mask):
			return note(fgFail, "moved while idle in a"+fgOffs(nil, nil, da.Mask))
		case nonzero(db.Mask):
			return note(fgFail, "moved while idle in b"+fgOffs(nil, nil, db.Mask))
		case !bytes.Equal(da.First, db.First):
			return note(fgFail, "changed"+fgOffs(da.First, db.First, nil)+": missing restore, joins l_fb_snap (plan section 7 risk 2)")
		}
		return note(fgPass)
	case fgIdle:
		if ra == fgBefore && nonzero(da.Mask) {
			return note(fgInvalid, "host not idle, redo (a moved"+fgOffs(nil, nil, da.Mask)+")")
		}
		comb := slices.Clone(da.Mask)
		for _, m := range []fgHex{db.Mask, maskOf(dbase)} {
			for k := range min(len(comb), len(m)) {
				comb[k] |= m[k]
			}
		}
		if offs := fgOffs(da.First, db.First, comb); offs != "" {
			extra := ""
			if da.Name == "link_cur_proc" {
				extra = fmt.Sprintf(": mCurProc 0x%X -> 0x%X (host dropped?)", mem.U32BE(da.First), mem.U32BE(db.First))
			}
			return note(fgFail, "changed"+offs+extra)
		}
		if n := countNonzero(comb); n > 0 {
			return note(fgMasked, fmt.Sprintf("%d byte(s) moved in a sample, not compared", n))
		}
		return note(fgPass)
	case fgTracks:
		for _, d := range []struct {
			n string
			d *fgDump
		}{{"a", da}, {"b", db}} {
			match, n, hit := fgTracksShare(d.d)
			if hit >= 0 {
				return note(fgFail, fmt.Sprintf("%s sample %d equals the body's own field (leak)", d.n, hit))
			}
			if n == 0 {
				return note(fgInvalid, d.n+": no sample with the Link field")
			}
			if float64(match) < fgTracksMin*float64(n) {
				return note(fgFail, fmt.Sprintf("%s tracks Link in %d/%d samples (< 90%%)", d.n, match, n))
			}
			notes = append(notes, fmt.Sprintf("%s %d/%d", d.n, match, n))
		}
		return note(fgPass, "tracks the Link field (a one-frame leak is not visible after the fact)")
	case fgVolatile:
		ra0, rb0 := fgByteDistinct(da, 0), fgByteDistinct(db, 0)
		other := fmt.Sprintf("maba_timer distinct %d/%d; flg/eye/morf a=%s b=%s", ra0, rb0, fgShort(da.First[1:]), fgShort(db.First[1:]))
		if lo, hi, ok := fgByteRange(dbase, 0); ok {
			alo, ahi, _ := fgByteRange(db, 0)
			if alo < lo || ahi > hi {
				other += fmt.Sprintf("; b maba_timer %d..%d outside the baseline %d..%d", alo, ahi, lo, hi)
			}
		}
		for _, d := range []struct {
			r fgRole
			n int
			s string
		}{{ra, ra0, "a"}, {rb, rb0, "b"}} {
			if d.n >= 2 {
				continue
			}
			if d.r == fgAfter || d.r == fgDel {
				return note(fgFail, "blink frozen in "+d.s+" ("+string(d.r)+"); "+other)
			}
			return note(fgMasked, "blink not running in "+d.s+" ("+string(d.r)+"); "+other)
		}
		return note(fgPass, "blink still running; "+other)
	case fgNoise:
		check := func(d *fgDump, rs []fgBodyRange, what string) string {
			for i, s := range d.Samples {
				if len(s) < 4 {
					continue
				}
				v := mem.U32BE(s)
				for _, r := range rs {
					if r.contains(v) {
						return fmt.Sprintf("%s sample %d = 0x%08X inside the %s body 0x%08X (heap 0x%08X-0x%08X)", what, i, v, what, uint32(r.Actor), uint32(r.HeapStart), uint32(r.HeapEnd))
					}
				}
			}
			return ""
		}
		after := map[fgRole][]fgBodyRange{}
		for _, x := range []struct {
			r fgRole
			c *fgCapture
		}{{ra, a}, {rb, b}} {
			if x.r == fgAfter {
				after[fgAfter] = append(after[fgAfter], x.c.bodyRanges()...)
			}
		}
		checked := false
		for _, x := range []struct {
			r fgRole
			d *fgDump
			c *fgCapture
		}{{ra, da, a}, {rb, db, b}} {
			var rs []fgBodyRange
			switch x.r {
			case fgAfter:
				rs = x.c.bodyRanges()
			case fgDel:
				rs = append(slices.Clone(after[fgAfter]), x.c.bodyRanges()...)
				rs = append(rs, a.bodyRanges()...)
			}
			if len(rs) == 0 {
				continue
			}
			checked = true
			if why := check(x.d, rs, string(x.r)); why != "" {
				return note(fgFail, why)
			}
		}
		if !checked {
			return note(fgPass, "no body range to check against")
		}
		return note(fgPass, "never inside the body")
	case fgTolerance:
		ax, az := fgF32(da.First, 0), fgF32(da.First, 2)
		maxd, far := 0.0, 0.0
		for _, d := range []*fgDump{da, db} {
			for _, s := range d.Samples {
				if len(s) < 12 {
					continue
				}
				dx, dz := float64(fgF32(s, 0)-ax), float64(fgF32(s, 2)-az)
				maxd = max(maxd, math.Abs(dx), math.Abs(dz))
				far = max(far, math.Hypot(dx, dz))
			}
		}
		switch {
		case far > fgShipDropped:
			return note(fgFail, fmt.Sprintf("ship moved %.1f in XZ", far))
		case maxd > fgShipTolerance:
			return note(fgMasked, fmt.Sprintf("ship moved %.1f (5..50) in X or Z", maxd))
		}
		return note(fgPass, fmt.Sprintf("XZ within %.2f", maxd))
	case fgCounters:
		names := []string{"solid", "game", "zelda", "command", "archive", "unknown", "total"}
		last := db.First
		for _, s := range db.Samples {
			if len(s) > 0 {
				last = s
			}
		}
		var rose, changed []string
		solid := false
		for k, n := range names {
			if 4*k+4 > len(da.First) || 4*k+4 > len(last) {
				break
			}
			x, y := mem.U32BE(da.First[4*k:]), mem.U32BE(last[4*k:])
			if x == y {
				continue
			}
			s := fmt.Sprintf("%s %d->%d", n, x, y)
			switch {
			case n == "solid" && y > x:
				solid = true
				changed = append(changed, s)
			case (n == "game" || n == "zelda") && y > x:
				rose = append(rose, s)
			default:
				changed = append(changed, s)
			}
		}
		switch {
		case solid:
			return note(fgInvalid, "a stage load happened between the dumps: "+strings.Join(append(rose, changed...), ", "))
		case len(rose) > 0:
			return note(fgFail, "heap errors rose: "+strings.Join(append(rose, changed...), ", "))
		case len(changed) > 0:
			return note(fgFail, "counters changed: "+strings.Join(changed, ", "))
		}
		return note(fgPass)
	}
	return note(fgInvalid, fmt.Sprintf("unknown class %q (a missing or edited key)", da.Class))
}

// fgAddrsMoved names a pointer region whose resolved address differs
// between or inside the dumps.
func fgAddrsMoved(da, db *fgDump) string {
	var all []fgU32
	for _, d := range []*fgDump{da, db} {
		for i, a := range d.Addrs {
			if i < len(d.Samples) && len(d.Samples[i]) > 0 && !slices.Contains(all, a) {
				all = append(all, a)
			}
		}
	}
	if len(all) < 2 {
		return ""
	}
	var s []string
	for _, a := range all {
		s = append(s, fmt.Sprintf("0x%08X", uint32(a)))
	}
	return strings.Join(s, " -> ")
}

// fgTracksShare counts the samples equal to the Link field (of those
// that have one) and returns the first sample equal to the body's own
// field where that differs from the Link's (-1: none).
func fgTracksShare(d *fgDump) (match, n, bodyHit int) {
	bodyHit = -1
	for i, s := range d.Samples {
		if len(s) == 0 || i >= len(d.Pair) || len(d.Pair[i]) == 0 {
			continue
		}
		n++
		if bytes.Equal(s, d.Pair[i]) {
			match++
		}
		if bodyHit < 0 && i < len(d.Body) && len(d.Body[i]) > 0 && bytes.Equal(s, d.Body[i]) && !bytes.Equal(d.Body[i], d.Pair[i]) {
			bodyHit = i
		}
	}
	return match, n, bodyHit
}

// fgByteDistinct counts the distinct values of byte k over d's samples.
func fgByteDistinct(d *fgDump, k int) int {
	seen := map[byte]bool{}
	for _, s := range d.Samples {
		if k < len(s) {
			seen[s[k]] = true
		}
	}
	return len(seen)
}

// fgByteRange is byte k's min and max over d's samples.
func fgByteRange(d *fgDump, k int) (lo, hi byte, ok bool) {
	if d == nil {
		return 0, 0, false
	}
	lo = 0xFF
	for _, s := range d.Samples {
		if k < len(s) {
			lo, hi, ok = min(lo, s[k]), max(hi, s[k]), true
		}
	}
	return lo, hi, ok
}

func maskOf(d *fgDump) fgHex {
	if d == nil {
		return nil
	}
	return d.Mask
}

func nonzero(b []byte) bool { return slices.ContainsFunc(b, func(x byte) bool { return x != 0 }) }

func countNonzero(b []byte) int {
	n := 0
	for _, x := range b {
		if x != 0 {
			n++
		}
	}
	return n
}

// fgOffs lists the byte offsets where a and b differ and mask is 0 (or,
// with a and b nil, where mask is set), as " at +0x0,+0x4": at most 8.
func fgOffs(a, b, mask []byte) string {
	var offs []string
	n := max(len(a), len(b), len(mask))
	more := 0
	for k := range n {
		var hit bool
		if a == nil && b == nil {
			hit = mask[k] != 0
		} else {
			hit = k < len(a) && k < len(b) && a[k] != b[k] && (k >= len(mask) || mask[k] == 0)
		}
		if !hit {
			continue
		}
		if len(offs) == 8 {
			more++
			continue
		}
		offs = append(offs, fmt.Sprintf("+0x%X", k))
	}
	if len(offs) == 0 {
		return ""
	}
	s := " at " + strings.Join(offs, ",")
	if more > 0 {
		s += fmt.Sprintf(" (+%d more)", more)
	}
	return s
}

// fgShort is b as hex, the first 12 bytes of a longer one.
func fgShort(b []byte) string {
	switch {
	case b == nil:
		return "unresolved"
	case len(b) > 12:
		return hex.EncodeToString(b[:12]) + ".."
	}
	return hex.EncodeToString(b)
}

// fgMaskText is a mask as 0, its hex, or the count of moving bytes.
func fgMaskText(b []byte) string {
	switch {
	case !nonzero(b):
		return "0"
	case len(b) > 8:
		return fmt.Sprintf("%db", countNonzero(b))
	}
	return hex.EncodeToString(b)
}

func fgAddrText(d *fgDump) string {
	if !d.resolved() {
		return "unresolved"
	}
	return fmt.Sprintf("0x%08X", uint32(d.Addr))
}

func fgF32(b []byte, i int) float32 {
	if 4*i+4 > len(b) {
		return float32(math.NaN())
	}
	return mem.F32BE(b[4*i:])
}

// fgFloats prints the first n f32 of b.
func fgFloats(b []byte, n int) string {
	if b == nil {
		return "-"
	}
	var s []string
	for i := range n {
		s = append(s, strconv.FormatFloat(float64(fgF32(b, i)), 'f', 2, 32))
	}
	return "(" + strings.Join(s, ",") + ")"
}

// fgPrintRows prints the rows and the tally and returns the tally.
func fgPrintRows(w io.Writer, rows []fgResult) fgCounts {
	var n fgCounts
	fmt.Fprintf(w, "%-18s %-10s %-5s %-9s %-26s %-26s %-9s %-7s %s\n", "NAME", "ADDR", "SIZE", "CLASS", "A_FIRST", "B_FIRST", "MASKS", "VERDICT", "note")
	for _, r := range rows {
		n.add(r.Verdict)
		fmt.Fprintf(w, "%-18s %-10s %-5s %-9s %-26s %-26s %-9s %-7s %s\n", r.Name, r.Addr, r.Size, r.Class, r.AFirst, r.BFirst, r.Masks, r.Verdict, r.Note)
	}
	fmt.Fprintf(w, "PASS %d FAIL %d MASKED %d INVALID %d\n", n.Pass, n.Fail, n.Masked, n.Invalid)
	return n
}

// fgMetaText is a capture's meta in one line per dump.
func fgMetaText(name string, c *fgCapture, role fgRole) string {
	if len(c.Meta) == 0 {
		return name + ": no samples"
	}
	f, l := c.Meta[0], c.Meta[len(c.Meta)-1]
	s := fmt.Sprintf("%s (%s): stage %q room %d next %d | REL linked %v build 0x%08X frames %d..%d | slot %d %s | shanm windows %d create_diff %d delete_diff %d delete_scan %d joint_diff %d | DMC27 0x%08X DMCB5 0x%08X | %d samples over %d ms",
		name, role, f.Stage, f.Room, f.NextEnable, f.RELLinked, uint32(f.RELBuild), f.RELFrames, l.RELFrames, c.Slot, c.body().text(),
		l.Shanm.Windows, l.Shanm.CreateDiff, l.Shanm.DeleteDiff, l.Shanm.DeleteScan, l.Shanm.JointDiff, uint32(f.DMC27), uint32(f.DMCB5),
		len(c.Meta), l.TMs-f.TMs)
	if f.Body.Actor != 0 {
		s += fmt.Sprintf(" | body heap 0x%08X-0x%08X", uint32(f.Body.HeapStart), uint32(f.Body.HeapEnd))
	}
	if c.LastBody.Actor != 0 {
		s += fmt.Sprintf(" | last body 0x%08X heap 0x%08X-0x%08X", uint32(c.LastBody.Actor), uint32(c.LastBody.HeapStart), uint32(c.LastBody.HeapEnd))
	}
	if c.Wait != "" {
		s += " | wait " + c.Wait
	}
	return s
}

// ---------------------------------------------------------------------
// The command.

// runFullbodyGlobals is the fullbody-globals command.
func runFullbodyGlobals(cmd string, args []string) {
	if len(args) == 0 {
		exitUsage(errors.New("needs snap, diff or watch"), fullbodyGlobalsUsage)
	}
	var code int
	var err error
	switch args[0] {
	case "snap":
		code, err = cmdFGSnap(args[1:])
	case "diff":
		code, err = cmdFGDiff(os.Stdout, args[1:])
	case "watch":
		code, err = cmdFGWatch(args[1:])
	default:
		exitUsage(fmt.Errorf("unknown mode %q", args[0]), fullbodyGlobalsUsage)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmd, err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// fgSplitTracks pulls every --track name=expr (or --track=name=expr) out
// of args, since splitValueFlags keeps only the last of a flag.
func fgSplitTracks(args []string) (rest []string, tracks []fgRegion, err error) {
	for i := 0; i < len(args); i++ {
		v, eq := strings.CutPrefix(args[i], "--track=")
		switch {
		case args[i] == "--track":
			if i+1 >= len(args) {
				return nil, nil, errors.New("--track needs name=expr")
			}
			v = args[i+1]
			i++
		case !eq:
			rest = append(rest, args[i])
			continue
		}
		name, expr, ok := strings.Cut(v, "=")
		if !ok || name == "" {
			return nil, nil, fmt.Errorf("--track %q: want name=expr", v)
		}
		if _, err := parseFGExpr(expr); err != nil {
			return nil, nil, err
		}
		tracks = append(tracks, fgRegion{Name: name, Expr: expr, Class: fgInfo, Cite: "--track"})
	}
	return rest, tracks, nil
}

// fgSlotArg parses --slot.
func fgSlotArg(values map[string]string) (int, error) {
	s, ok := values["slot"]
	if !ok {
		return fgDefaultSlot, nil
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 || v >= mailbox.RemoteMaxSlots {
		return 0, fmt.Errorf("--slot must be 0..%d, not %q", mailbox.RemoteMaxSlots-1, s)
	}
	return v, nil
}

// fgWindow is how long watch's before and after windows are (a var so
// the test runs short ones).
var fgWindow = time.Second

// fgSnapOpts are snap's options.
type fgSnapOpts struct {
	Samples int
	Every   time.Duration
	Wait    string // none, full, empty
	Settle  int
	Timeout time.Duration
	Slot    int
}

func cmdFGSnap(args []string) (int, error) {
	rest, tracks, err := fgSplitTracks(args)
	if err != nil {
		exitUsage(err, fullbodyGlobalsUsage)
	}
	pos, _, values, err := splitValueFlags(rest, []string{"samples", "every", "wait", "settle", "timeout", "slot"})
	if err != nil {
		exitUsage(err, fullbodyGlobalsUsage)
	}
	if len(pos) != 1 {
		exitUsage(errors.New("snap needs one <out.json>"), fullbodyGlobalsUsage)
	}
	o := fgSnapOpts{Samples: 60, Every: enemysync.Tick, Wait: "none", Settle: 60, Timeout: 30 * time.Second}
	intv := func(k string, lo, hi int, p *int) {
		if s, ok := values[k]; ok {
			v, err := strconv.Atoi(s)
			if err != nil || v < lo || v > hi {
				exitUsage(fmt.Errorf("--%s must be %d..%d, not %q", k, lo, hi, s), fullbodyGlobalsUsage)
			}
			*p = v
		}
	}
	durv := func(k string, p *time.Duration) {
		if s, ok := values[k]; ok {
			v, err := time.ParseDuration(s)
			if err != nil || v < 0 {
				exitUsage(fmt.Errorf("--%s: bad duration %q", k, s), fullbodyGlobalsUsage)
			}
			*p = v
		}
	}
	intv("samples", 1, 10000, &o.Samples)
	intv("settle", 0, 100000, &o.Settle)
	durv("every", &o.Every)
	durv("timeout", &o.Timeout)
	if w, ok := values["wait"]; ok {
		if !slices.Contains([]string{"none", "full", "empty"}, w) {
			exitUsage(fmt.Errorf("--wait must be none, full or empty, not %q", w), fullbodyGlobalsUsage)
		}
		o.Wait = w
	}
	if o.Slot, err = fgSlotArg(values); err != nil {
		exitUsage(err, fullbodyGlobalsUsage)
	}
	regs, err := fgRegions(tracks)
	if err != nil {
		exitUsage(err, fullbodyGlobalsUsage)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var c *fgCapture
	err = diag.WithMem(openDolphin, func(m mem.Mem) error {
		var err error
		c, err = fgSnap(ctx, os.Stdout, m, regs, o)
		return err
	})
	if err != nil {
		if errors.Is(err, errFGTimeout) {
			return 2, err
		}
		return 1, err
	}
	b, err := json.MarshalIndent(c, "", " ")
	if err != nil {
		return 1, err
	}
	if err := os.WriteFile(pos[0], append(b, '\n'), 0o644); err != nil {
		return 1, err
	}
	fmt.Printf("wrote %s\n", pos[0])
	if len(fgCaptureProblems(c)) > 0 {
		return 2, nil
	}
	return 0, nil
}

var errFGTimeout = errors.New("wait timed out (INVALID capture)")

// fgSleep waits d or until ctx ends.
func fgSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// fgWait waits for the body of o.Slot (full: FULL LIVE/PARKED with the
// shanm WINDOWS risen by >= 2, one makeBgWait window per INIT retry,
// fullbody.c fb_phase3; empty: EMPTY with actor 0 and WINDOWS risen by
// >= 1, the delete window), then o.Settle REL frames. It returns the last
// body it saw (for --wait empty's NOISE check).
func fgWait(ctx context.Context, w io.Writer, m mem.Reader, o fgSnapOpts) (fgBodyRange, error) {
	var last fgBodyRange
	if o.Wait == "none" {
		return last, nil
	}
	deadline := time.Now().Add(o.Timeout)
	sh, err := readShanm(m)
	if err != nil {
		return last, err
	}
	w0 := sh.Windows
	fmt.Fprintf(w, "waiting for slot %d %s (shanm windows %d)\n", o.Slot, o.Wait, w0)
	for {
		st, err := mailbox.ReadRemoteStatus(m)
		if err != nil {
			return last, err
		}
		if sh, err = readShanm(m); err != nil {
			return last, err
		}
		b := st.Bodies[o.Slot]
		if b.Actor != 0 {
			last = fgReadBodyRange(m, b.Actor)
		}
		rose := sh.Windows - w0
		fb := fgBodyStatus{Actor: fgU32(b.Actor), State: b.State, Kind: b.Kind}
		if (o.Wait == "full" && fb.liveFull() && rose >= 2) || (o.Wait == "empty" && b.State == mailbox.RemoteBodyEmpty && b.Actor == 0 && rose >= 1) {
			fmt.Fprintf(w, "slot %d %s after %d windows; settling %d REL frames\n", o.Slot, o.Wait, rose, o.Settle)
			break
		}
		if time.Now().After(deadline) {
			return last, fmt.Errorf("%w: slot %d state %s kind %s, windows +%d", errFGTimeout, o.Slot,
				enumName(remoteBodyStateNames, b.State), enumName(remoteKindNames, b.Kind), rose)
		}
		if err := fgSleep(ctx, enemysync.Tick); err != nil {
			return last, err
		}
	}
	r0, err := mailbox.ReadREL(m)
	if err != nil {
		return last, err
	}
	for {
		r, err := mailbox.ReadREL(m)
		if err != nil {
			return last, err
		}
		if r.Frames-r0.Frames >= uint32(o.Settle) {
			return last, nil
		}
		if time.Now().After(deadline) {
			return last, fmt.Errorf("%w: REL frames %d of %d settled", errFGTimeout, r.Frames-r0.Frames, o.Settle)
		}
		if err := fgSleep(ctx, enemysync.Tick); err != nil {
			return last, err
		}
	}
}

// fgSnap waits as o says, takes o.Samples samples o.Every apart and
// prints a summary per region.
func fgSnap(ctx context.Context, w io.Writer, m mem.Reader, regs []fgRegion, o fgSnapOpts) (*fgCapture, error) {
	last, err := fgWait(ctx, w, m, o)
	if err != nil {
		return nil, err
	}
	t0 := time.Now()
	var samples []fgSample
	for i := range o.Samples {
		if i > 0 {
			if err := fgSleep(ctx, o.Every); err != nil {
				return nil, err
			}
		}
		s, err := fgReadSample(m, regs, o.Slot, t0)
		if err != nil {
			return nil, err
		}
		samples = append(samples, s)
	}
	c := fgBuild(samples, regs, o.Slot)
	c.Wait, c.EveryMs, c.Settle, c.LastBody = o.Wait, float64(o.Every)/float64(time.Millisecond), o.Settle, last
	c.Notes = fgCaptureProblems(c)
	fmt.Fprintf(w, "%-18s %-10s %-5s %-9s %-26s %-9s %s\n", "NAME", "ADDR", "SIZE", "CLASS", "FIRST", "MASK", "DISTINCT")
	for i := range c.Regions {
		d := &c.Regions[i]
		extra := ""
		if d.F32 > 0 {
			extra = " " + fgFloats(d.First, d.F32)
		}
		if !d.resolved() {
			extra = " " + d.Why
			if d.Absent != "" {
				extra = " " + d.Absent
			}
		}
		fmt.Fprintf(w, "%-18s %-10s %-5s %-9s %-26s %-9s %d%s\n", d.Name, fgAddrText(d), fmt.Sprintf("0x%X", d.Size), d.Class, fgShort(d.First), fgMaskText(d.Mask), d.Distinct, extra)
	}
	fmt.Fprintln(w, fgMetaText("snap", c, ""))
	for _, n := range c.Notes {
		fmt.Fprintf(w, "INVALID capture: %s\n", n)
	}
	return c, nil
}

// fgLoad reads a capture file.
func fgLoad(path string) (*fgCapture, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c fgCapture
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	for i := range c.Regions {
		c.Regions[i].stats()
	}
	return &c, nil
}

func cmdFGDiff(w io.Writer, args []string) (int, error) {
	pos, _, values, err := splitValueFlags(args, []string{"base"})
	if err != nil {
		exitUsage(err, fullbodyGlobalsUsage)
	}
	if len(pos) != 2 {
		exitUsage(errors.New("diff needs <a.json> <b.json>"), fullbodyGlobalsUsage)
	}
	a, err := fgLoad(pos[0])
	if err != nil {
		return 1, err
	}
	b, err := fgLoad(pos[1])
	if err != nil {
		return 1, err
	}
	var base *fgCapture
	if p := values["base"]; p != "" {
		if base, err = fgLoad(p); err != nil {
			return 1, err
		}
	}
	return fgDiff(w, a, b, base), nil
}

// fgDiff prints the comparison of a and b and returns the exit code.
func fgDiff(w io.Writer, a, b, base *fgCapture) int {
	ra, rb := fgRoles(a, b)
	n := fgPrintRows(w, fgCompare(a, b, base, ra, rb))
	fmt.Fprintln(w, fgMetaText("a", a, ra))
	fmt.Fprintln(w, fgMetaText("b", b, rb))
	if base != nil {
		fmt.Fprintln(w, fgMetaText("base", base, fgBefore))
	}
	return n.code()
}

func cmdFGWatch(args []string) (int, error) {
	rest, tracks, err := fgSplitTracks(args)
	if err != nil {
		exitUsage(err, fullbodyGlobalsUsage)
	}
	pos, _, values, err := splitValueFlags(rest, []string{"slot", "baseline", "csv"})
	if err != nil {
		exitUsage(err, fullbodyGlobalsUsage)
	}
	secs, err := diagIntArg(pos, 0, 60, 0, 86400, "secs")
	if err != nil {
		exitUsage(err, fullbodyGlobalsUsage)
	}
	slot, err := fgSlotArg(values)
	if err != nil {
		exitUsage(err, fullbodyGlobalsUsage)
	}
	baseline := 3
	if s, ok := values["baseline"]; ok {
		if baseline, err = strconv.Atoi(s); err != nil || baseline < 0 || baseline > 600 {
			exitUsage(fmt.Errorf("--baseline must be 0..600 seconds, not %q", s), fullbodyGlobalsUsage)
		}
	}
	regs, err := fgRegions(tracks)
	if err != nil {
		exitUsage(err, fullbodyGlobalsUsage)
	}
	var csv io.Writer
	if p := values["csv"]; p != "" {
		f, err := os.Create(p)
		if err != nil {
			exitUsage(err, fullbodyGlobalsUsage)
		}
		defer f.Close()
		csv = f
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	code := 0
	err = diag.WithMem(openDolphin, func(m mem.Mem) error {
		var err error
		code, err = fgWatch(ctx, os.Stdout, csv, m, regs, slot, time.Duration(secs)*time.Second, time.Duration(baseline)*time.Second, enemysync.Tick)
		return err
	})
	return code, err
}

// fgEvent is a create or delete being captured.
type fgEvent struct {
	kind       string // create, delete, failed-create
	pre, post  []fgSample
	collecting bool
	tMs        int64
	bodyKind   uint8
	err        uint8
}

// fgTailRun returns the newest run of ring samples that satisfy ok, at
// most fgWindow long, oldest first.
func fgTailRun(ring []fgSample, slot int, ok func(fgBodyStatus) bool) []fgSample {
	i := len(ring)
	for i > 0 && ok(ring[i-1].Meta.Bodies[slot]) && ring[len(ring)-1].Meta.TMs-ring[i-1].Meta.TMs <= fgWindow.Milliseconds() {
		i--
	}
	return slices.Clone(ring[i:])
}

// fgWatch samples every tick and compares the second before and after
// each create and delete of the body in slot (d 0: until ctx ends).
func fgWatch(ctx context.Context, w, csv io.Writer, m mem.Reader, regs []fgRegion, slot int, d, baseline, tick time.Duration) (int, error) {
	start := time.Now()
	fmt.Fprintf(w, "fullbody-globals watch: slot %d, %d regions, every %v, baseline %v\n", slot, len(regs), tick, baseline)
	if csv != nil {
		h := []string{"t_ms", "state", "kind", "err"}
		for _, r := range regs {
			h = append(h, r.Name)
		}
		fmt.Fprintln(csv, strings.Join(h, ","))
	}
	var (
		ring     []fgSample
		baseS    []fgSample
		base     *fgCapture
		ev       *fgEvent
		prev     fgBodyStatus
		havePrev bool
		lastBody fgBodyRange
		total    fgCounts
	)
	live := func(b fgBodyStatus) bool {
		return b.State == mailbox.RemoteBodyLive || b.State == mailbox.RemoteBodyParked
	}
	finish := func() {
		var ra, rb fgRole
		switch ev.kind {
		case "create":
			ra, rb = fgBefore, fgAfter
		case "delete":
			ra, rb = fgAfter, fgDel
		default:
			ra, rb = fgBefore, fgDel
		}
		a, b := fgBuild(ev.pre, regs, slot), fgBuild(ev.post, regs, slot)
		b.LastBody = lastBody
		fmt.Fprintf(w, "EVENT %s slot %d kind %s err %s t=%dms pre=%d post=%d (+1s=%d)\n", ev.kind, slot, enumName(remoteKindNames, ev.bodyKind),
			enumName(remoteBErrNames, ev.err), ev.tMs, ev.pre[len(ev.pre)-1].Meta.RELFrames, ev.post[0].Meta.RELFrames, ev.post[len(ev.post)-1].Meta.RELFrames)
		rows := fgCompare(a, b, base, ra, rb)
		n := fgPrintRows(w, rows)
		var bad []string
		for _, r := range rows {
			if r.Verdict == fgFail || r.Verdict == fgInvalid {
				bad = append(bad, r.Name)
			}
		}
		switch {
		case n.Invalid > 0:
			fmt.Fprintf(w, "verdict %s: INVALID: %s\n", ev.kind, strings.Join(bad, ","))
		case n.Fail > 0:
			fmt.Fprintf(w, "verdict %s: FAIL: %s\n", ev.kind, strings.Join(bad, ","))
		default:
			fmt.Fprintf(w, "verdict %s: PASS\n", ev.kind)
		}
		fmt.Fprintln(w, fgMetaText("pre", a, ra))
		fmt.Fprintln(w, fgMetaText("post", b, rb))
		total.Pass += n.Pass
		total.Fail += n.Fail
		total.Masked += n.Masked
		total.Invalid += n.Invalid
		ev = nil
	}
	for {
		s, err := fgReadSample(m, regs, slot, start)
		if err != nil {
			return total.code(), err
		}
		cur := s.Meta.Bodies[slot]
		if csv != nil {
			row := []string{strconv.FormatInt(s.Meta.TMs, 10), enumName(remoteBodyStateNames, cur.State), enumName(remoteKindNames, cur.Kind), strconv.Itoa(int(cur.Err))}
			for _, b := range s.Data {
				row = append(row, hex.EncodeToString(b))
			}
			fmt.Fprintln(csv, strings.Join(row, ","))
		}
		if base == nil {
			baseS = append(baseS, s)
			if time.Since(start) >= baseline {
				base = fgBuild(baseS, regs, slot)
				var moving []string
				for _, r := range base.Regions {
					if nonzero(r.Mask) {
						moving = append(moving, r.Name)
					}
				}
				fmt.Fprintf(w, "baseline: %d samples, moving while idle: %s\n", len(baseS), strings.Join(moving, ","))
				for _, p := range fgCaptureProblems(base) {
					fmt.Fprintf(w, "baseline INVALID: %s\n", p)
				}
			}
		}
		if cur.Actor != 0 {
			lastBody = s.Meta.Body
		}
		ring = append(ring, s)
		if ev != nil && ev.collecting {
			ev.post = append(ev.post, s)
			if s.Meta.TMs-ev.post[0].Meta.TMs >= fgWindow.Milliseconds() {
				finish()
			}
		} else if havePrev {
			switch {
			case ev == nil && prev.State == mailbox.RemoteBodyEmpty && cur.State == mailbox.RemoteBodyQueued:
				pre := fgTailRun(ring[:len(ring)-1], slot, func(b fgBodyStatus) bool { return b.State == mailbox.RemoteBodyEmpty })
				if len(pre) > 0 {
					ev = &fgEvent{kind: "create", pre: pre, tMs: s.Meta.TMs}
				}
			case ev == nil && live(prev) && (cur.State == mailbox.RemoteBodyDying || cur.Actor == 0):
				pre := fgTailRun(ring[:len(ring)-1], slot, live)
				if len(pre) > 0 {
					ev = &fgEvent{kind: "delete", pre: pre, tMs: s.Meta.TMs, bodyKind: prev.Kind}
				}
			case ev != nil && ev.kind == "create" && cur.liveFull():
				ev.collecting, ev.post, ev.bodyKind = true, []fgSample{s}, cur.Kind
			case ev != nil && ev.kind == "create" && live(cur):
				fmt.Fprintf(w, "EVENT create slot %d kind %s: not a FULL body, skipped\n", slot, enumName(remoteKindNames, cur.Kind))
				ev = nil
			case ev != nil && ev.kind == "create" && cur.State == mailbox.RemoteBodyEmpty:
				// QUEUED -> EMPTY: a failed create (err 11-15 for a FULL
				// one), compared like a delete.
				ev.kind, ev.collecting, ev.post, ev.err, ev.bodyKind = "failed-create", true, []fgSample{s}, cur.Err, cur.Kind
			case ev != nil && ev.kind == "delete" && cur.State == mailbox.RemoteBodyEmpty:
				ev.collecting, ev.post, ev.err = true, []fgSample{s}, cur.Err
			}
		}
		prev, havePrev = cur, true
		if keep := 2 * int(fgWindow/max(tick, time.Millisecond)); len(ring) > keep {
			ring = slices.Delete(ring, 0, len(ring)-keep)
		}
		if d > 0 && time.Since(start) >= d {
			break
		}
		if err := fgSleep(ctx, tick); err != nil {
			break
		}
	}
	if ev != nil {
		fmt.Fprintf(w, "EVENT %s slot %d: unfinished when the watch ended\n", ev.kind, slot)
	}
	fmt.Fprintf(w, "watch total: PASS %d FAIL %d MASKED %d INVALID %d\n", total.Pass, total.Fail, total.Masked, total.Invalid)
	return total.code(), nil
}
