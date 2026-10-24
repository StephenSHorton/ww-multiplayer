package main

// Batch D's P0 probes (ww-multiplayer-handoff/design/enemy-mirror-batch-d.json
// "p0"; docs/TESTING.md "Batch D P0"). All three only read game memory:
//
//   - bst-probe [secs=0]: P0-BST. The three proc 0xF2 actors (Gohdan's head
//     and hands) sorted by part (+0x2B4), each with the raw words the
//     design peeks (+0x10D4 8, +0x2E7C 4, +0x2E98 4, +0x10FC 4) decoded,
//     and the answer the batch's bk_bst would give (R-D2). With secs > 0
//     it samples every tick and prints a line only when a part changes,
//     so m2E7C 1 -> 10 -> 11 -> 12 and mDamage 0 -> 3 show as they happen.
//   - rf-probe: P0-RF. Every Stone2 (Ebrock2), Saku (Dsaku/Ksaku), daItem,
//     Obj_Leaves (Vochi) and Tbox in the queue with the fields the refresh
//     op reads (actor_refresh.procs), its bit as the REL re-derives it, the
//     generic gates the walk can see, the per-proc gate, and PASS/FAIL on
//     what is static: the Tbox and Saku fingerprints read from the live
//     profile execute, Tbox +0x348 against the derived action addresses,
//     Saku's switch copies against its params, and Stone2 m648 in {0, 1}.
//   - mt2-probe [secs=10]: P0-MT2. Each Magtail sampled every tick: mode,
//     ring idx (from the same block read as the sphere centres, bracketed
//     by a second block read after the segment matrices: a moved idx,
//     f470, pos, wiggle, morf or sphere centre is a torn sample, retried,
//     then counted and left out; the idx alone cannot show a tear in FIGHT
//     or a dive, where it does not advance), f470, the wiggle,
//     per segment the model's base translation and column 2 (morf[i] +0x50
//     -> J3DModel +0x24), the body sphere centres, the eye sphere and the
//     residuals the design's PASS rule names. The walk line carries the
//     exec/bc1/bc2 words and fp=ok|miss against the retail d_a_mt.rel
//     (R-D6). A summary per Magtail ends the run with PASS (MOVE and FIGHT
//     samples, max residual <= 0.2), INCOMPLETE (no FIGHT sample) or FAIL.
//
// The offsets are the design's (re-read against tww include/d/actor
// d_a_bst.h, d_a_saku.h, d_a_tbox.h, d_a_stone2.h, d_a_item.h,
// d_a_itembase.h and config/GZLE01/rels/*/symbols.txt). They are local to
// this probe until the batch's REL and Go code pins them.

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"os/signal"
	"slices"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/diag"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

const (
	bstProbeUsage = "bst-probe [secs=0, one read]"
	rfProbeUsage  = "rf-probe"
	mt2ProbeUsage = "mt2-probe [secs=10, 0 = until Ctrl+C]"
)

// The procs P0 reads (f_pc_name.h).
const (
	procBst    = int16(stage.ProcBST) // 0x0F2
	procMt     = int16(stage.ProcMT)  // 0x0D9
	procItem   = int16(stage.ProcItem)
	procTbox   = int16(stage.ProcTbox)
	procLeaves = int16(0x092) // fpcNm_Obj_Leaves_e, f_pc_name.h:155 ("Vochi", d_stage.cpp:1123)
	procSaku   = int16(0x191) // fpcNm_SAKU_e, f_pc_name.h:413 ("Ksaku", "Dsaku")
	procStone2 = int16(0x1CD) // fpcNm_Stone2_e, f_pc_name.h:473 ("Ebrock2", d_stage.cpp:656)
)

// Gohdan (bst_class, d_a_bst.h). What bk_bst reads is the pinned
// mailbox.BossBst* (wwboss.h); the probe reads the word each sits in.
const (
	bstOffPart   = mailbox.BossBstPartOff      // u8 mBstPartType: 0 head, 1 and 2 the hands
	bstOffMorf   = mailbox.BossBstMorfOff      // mDoExt_McaMorf* m02B8
	bstOffAct    = mailbox.BossBstActOff - 2   // s16 m10D4, s16 mActionType (+0x10D6)
	bstOffDmg    = mailbox.BossBstDmgOff - 2   // s16 m10D8, s16 mDamage (+0x10DA)
	bstOffTimers = 0x10FC                      // s16 m10FC[0] (target timer), [1] (sleep timer)
	bstOffM2E7C  = mailbox.BossBstM2E7COff     // s8 m2E7C, then padding
	bstOffM2E98  = mailbox.BossBstM2E9AOff - 2 // s16 m2E98, s8 m2E9A (+0x2E9A)

	bstActSleep   = mailbox.BossBstActSleep   // ACTION_SLEEP_e
	bstActHeadDmg = mailbox.BossBstActHeadDmg // ACTION_HEAD_DAMAGE_e
	bstActEndDemo = mailbox.BossBstActEndDemo // ACTION_END_DEMO_e
	bstDmgMouth   = mailbox.BossBstDmgMouth   // KUTI_OPEN_LOOP (d_a_bst.cpp:1159-1166)
	bstM2E7CEyes  = mailbox.BossBstM2E7CEyes  // both hands asleep, eyes open (d_a_bst.cpp:1888-1905)
	bstM2E7CDone  = mailbox.BossBstM2E7CDone  // d_a_bst.cpp:2534
	bstM2E9ADeath = mailbox.BossBstM2E9ADeath // d_a_bst.cpp:1220
)

// bstPart is one proc 0xF2 actor as bst-probe reads it: the raw words
// the design peeks.
type bstPart struct {
	PID, Addr uint32
	Init      int8
	Part      uint8
	Morf      uint32
	W10D4     uint32 // m10D4 << 16 | mActionType
	W10D8     uint32 // m10D8 << 16 | mDamage
	W10FC     uint32 // m10FC[0] << 16 | m10FC[1]
	W2E7C     uint32 // m2E7C in the top byte
	W2E98     uint32 // m2E98 << 16 | m2E9A << 8
}

func (p *bstPart) act() int16   { return int16(p.W10D4) }
func (p *bstPart) dmg() int16   { return int16(p.W10D8) }
func (p *bstPart) m2E7C() int8  { return int8(p.W2E7C >> 24) }
func (p *bstPart) m2E98() int16 { return int16(p.W2E98 >> 16) }
func (p *bstPart) m2E9A() int8  { return int8(p.W2E98 >> 8) }
func (p *bstPart) sleep() int16 { return int16(p.W10FC) }

// bstPartName is part's name.
func bstPartName(part uint8) string {
	switch part {
	case 0:
		return "head"
	case 1:
		return "hand1"
	case 2:
		return "hand2"
	}
	return fmt.Sprintf("part%d", part)
}

// readBstParts reads every proc 0xF2 actor of aw, sorted by part.
func readBstParts(m mem.Reader, aw *actors.Walk) ([]bstPart, error) {
	var ps []bstPart
	for i := range aw.Actors {
		a := &aw.Actors[i]
		if a.Proc != procBst {
			continue
		}
		p := bstPart{PID: a.PID, Addr: a.Addr, Init: a.InitState}
		pid, err := mem.ReadU32(m, a.Addr+addr.ActorOffBsPcID)
		if err != nil {
			return nil, err
		}
		if pid != a.PID {
			return nil, fmt.Errorf("%w: %s changed under the probe", actors.ErrChanged, a)
		}
		if p.Part, err = mem.ReadU8(m, a.Addr+bstOffPart); err != nil {
			return nil, err
		}
		for _, f := range []struct {
			off uint32
			v   *uint32
		}{{bstOffMorf, &p.Morf}, {bstOffAct, &p.W10D4}, {bstOffDmg, &p.W10D8}, {bstOffTimers, &p.W10FC}, {bstOffM2E7C, &p.W2E7C}, {bstOffM2E98, &p.W2E98}} {
			if *f.v, err = mem.ReadU32(m, a.Addr+f.off); err != nil {
				return nil, err
			}
		}
		ps = append(ps, p)
	}
	slices.SortStableFunc(ps, func(a, b bstPart) int { return int(a.Part) - int(b.Part) })
	return ps, nil
}

// bstPartLine is one part's line: the raw words, then their decode.
func bstPartLine(p *bstPart) string {
	return fmt.Sprintf("%-5s pid %-5d 0x%08X init %d +0x2B4 %d morf 0x%08X | +0x10D4 %08X %08X (act %d m10D8 %d mDamage %d) +0x2E7C %08X (m2E7C %d) +0x2E98 %08X (m2E98 %d m2E9A %d) +0x10FC %08X (m10FC[0] %d sleep %d)",
		bstPartName(p.Part), p.PID, p.Addr, p.Init, p.Part, p.Morf, p.W10D4, p.W10D8, p.act(), int16(p.W10D8>>16), p.dmg(),
		p.W2E7C, p.m2E7C(), p.W2E98, p.m2E98(), p.m2E9A(), p.W10FC, int16(p.W10FC>>16), p.sleep())
}

// bstVerdict is the answer the batch's bk_bst gives the parts (R-D2) and
// its result word (byte +0 R, +1 head act, +2 mDamage, +3 m2E7C), with
// why it is not APPLIED.
func bstVerdict(ps []bstPart) (r uint8, word uint32, why string) {
	var by [3]*bstPart
	for i := range ps {
		p := &ps[i]
		if p.Part > 2 || p.Init != 2 {
			continue
		}
		if by[p.Part] != nil {
			return mailbox.BossRNoActor, uint32(mailbox.BossRNoActor) << 24, fmt.Sprintf("two executing actors of part %d", p.Part)
		}
		by[p.Part] = p
	}
	for i, p := range by {
		if p == nil {
			return mailbox.BossRNoActor, uint32(mailbox.BossRNoActor) << 24, fmt.Sprintf("no executing %s", bstPartName(uint8(i)))
		}
	}
	h := by[0]
	if h.Morf == 0 {
		return mailbox.BossRNoActor, uint32(mailbox.BossRNoActor) << 24, "the head's morf is NULL"
	}
	word = uint32(uint8(h.act()))<<16 | uint32(uint8(h.dmg()))<<8 | uint32(uint8(h.m2E7C()))
	switch {
	case h.act() == bstActEndDemo || h.m2E9A() >= bstM2E9ADeath || h.m2E7C() == bstM2E7CDone:
		r = mailbox.BossRAlready
	case h.act() == bstActHeadDmg && h.dmg() == bstDmgMouth && h.m2E7C() == bstM2E7CEyes && h.m2E9A() == 0 &&
		by[1].act() == bstActSleep && by[2].act() == bstActSleep:
		r = mailbox.BossRApplied
	default:
		r = mailbox.BossRWaiting
		var miss []string
		if h.act() != bstActHeadDmg {
			miss = append(miss, fmt.Sprintf("head act %d != 7", h.act()))
		}
		if h.dmg() != bstDmgMouth {
			miss = append(miss, fmt.Sprintf("mDamage %d != 3", h.dmg()))
		}
		if h.m2E7C() != bstM2E7CEyes {
			miss = append(miss, fmt.Sprintf("m2E7C %d != 12", h.m2E7C()))
		}
		if h.m2E9A() != 0 {
			miss = append(miss, fmt.Sprintf("m2E9A %d != 0", h.m2E9A()))
		}
		for i := 1; i <= 2; i++ {
			if by[i].act() != bstActSleep {
				miss = append(miss, fmt.Sprintf("%s act %d != 6", bstPartName(uint8(i)), by[i].act()))
			}
		}
		why = strings.Join(miss, ", ")
	}
	return r, uint32(r)<<24 | word, why
}

// bossRName is a BossR* result's name.
func bossRName(r uint8) string {
	switch r {
	case mailbox.BossRNone:
		return "NONE"
	case mailbox.BossRApplied:
		return "APPLIED"
	case mailbox.BossRWaiting:
		return "WAITING"
	case mailbox.BossRAlready:
		return "ALREADY"
	case mailbox.BossRNoActor:
		return "NO_ACTOR"
	case mailbox.BossRGate:
		return "GATE"
	}
	return fmt.Sprintf("R%d", r)
}

// bstReport is bst-probe's text for one read: the count, each part and
// the verdict.
func bstReport(ps []bstPart) string {
	var b strings.Builder
	n := fmt.Sprintf("%d proc 0xF2 actors", len(ps))
	if len(ps) != 3 {
		n += " (want 3: head and two hands)"
	}
	b.WriteString(n + "\n")
	for i := range ps {
		b.WriteString("  " + bstPartLine(&ps[i]) + "\n")
	}
	r, word, why := bstVerdict(ps)
	fmt.Fprintf(&b, "  bk_bst would answer %s 0x%08X", bossRName(r), word)
	if why != "" {
		b.WriteString(": " + why)
	}
	b.WriteString("\n")
	return b.String()
}

// bstProbe is bst-probe over m: one read, or, for d > 0, a read every
// tick for d (a negative d: until ctx ends) printed only when it changes.
// The queue is walked once a second, or again when a part's pid moved.
func bstProbe(ctx context.Context, w io.Writer, m mem.Reader, d time.Duration) error {
	stageName, room, err := enemyScope(m)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "stage %q room %d; P0-BST wants SirenB 1 0\n", stageName, room)
	start := time.Now()
	last := ""
	var aw actors.Walk
	var nextWalk time.Time
	read := func(now time.Time) error {
		if !now.Before(nextWalk) {
			if aw, err = actors.Read(m, actors.MaxNodes); err != nil {
				return err
			}
			nextWalk = now.Add(time.Second)
		}
		ps, err := readBstParts(m, &aw)
		if errors.Is(err, actors.ErrChanged) {
			nextWalk = time.Time{}
			return nil
		}
		if err != nil {
			return err
		}
		if rep := bstReport(ps); rep != last {
			last = rep
			fmt.Fprintf(w, "[%6.3fs] walk %s: %s", now.Sub(start).Seconds(), walkNote(&aw), rep)
		}
		return nil
	}
	if err := read(start); err != nil || d == 0 {
		return err
	}
	var deadline time.Time
	if d > 0 {
		deadline = start.Add(d)
	}
	t := time.NewTicker(enemyWatchTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-t.C:
			if !deadline.IsZero() && now.After(deadline) {
				return nil
			}
			if err := read(now); err != nil {
				return err
			}
		}
	}
}

// The refresh procs' fields (actor_refresh.procs): the pinned
// mailbox.Refresh* (wwrefresh.h) refresh.c reads, and the Saku v2 words.
const (
	stone2OffM648 = mailbox.RefreshStone2M648Off // s32 m648: 0 mode_wait (idle), 1 lifted (d_a_stone2.h:138)

	sakuOffBurn   = mailbox.RefreshSakuBurnOff  // u8 field_0xEF4: 1 once burning (d_a_saku.cpp:345-377)
	sakuOffState  = mailbox.RefreshSakuStateOff // s32 field_0xEF8[2]: 0 none, 1 standing, 2 burning, 3 broken
	sakuOffSwitch = mailbox.RefreshSakuSwOff    // u32 mBottomHalfDestroyedSwitch, mTopHalfDestroyedSwitch (+0xF04)

	itemOffBitNo             = mailbox.RefreshItemBitNoOff      // s32 mItemBitNo (d_a_itembase.h:91)
	itemOffItemNo            = mailbox.RefreshItemNoOff         // u8 m_itemNo (d_a_itembase.h:94)
	itemOffFlag              = mailbox.RefreshItemFlagOff       // u8 mFlag (d_a_item.h:121)
	itemOffStatus            = mailbox.RefreshItemStatusOff     // u8 mItemStatus (d_a_item.h:123)
	itemStatusWaitMax        = mailbox.RefreshItemStatusWaitMax // STATUS_UNK1: 0 at rest, 1 moving (execWaitMain)
	itemFlagBusy             = mailbox.RefreshItemFlagBusy      // FLAG_BOOMERANG | FLAG_HOOK
	itemBitNone              = mailbox.RefreshItemBitNone
	itemNoBlueJelly          = mailbox.RefreshItemNoBlueJelly
	tboxOffBgOpen            = mailbox.RefreshTboxBgOpenOff // dBgW* mpBgWOpen
	tboxOffBgCur             = mailbox.RefreshTboxBgCurOff  // dBgW* mpBgWCurrent
	tboxOffActFn             = mailbox.RefreshTboxActFnOff  // ActionFunc mActionFunc (+0x340), its function word
	tboxOffFlags             = mailbox.RefreshTboxFlagsOff  // u16 mFlags
	tboxFlagsBusy            = mailbox.RefreshTboxFlagsBusy // UNK_08, OPENING, APPEARING
	tboxExecOff              = mailbox.RefreshTboxTextExec  // daTbox_Execute (rels/d_a_tbox/symbols.txt:51)
	tboxFpWord0C      uint32 = mailbox.RefreshTboxFpWord0
	tboxFpWord1C      uint32 = mailbox.RefreshTboxFpWord1
	sakuExecOff              = 0x242C // daSaku_Execute (rels/d_a_saku/symbols.txt:44)
	sakuBrokenOff            = 0xE8C  // broken__8daSaku_cFi (:19)
)

// tboxActions are the Tbox actions the refresh accepts, at their .text
// offsets (rels/d_a_tbox/symbols.txt:44-46).
var tboxActions = []struct {
	name string
	off  uint32
}{{"actionOpenWait", mailbox.RefreshTboxTextOpenWait}, {"actionSwOnWait", mailbox.RefreshTboxTextSwWait}, {"actionSwOnWait2", mailbox.RefreshTboxTextSwWait2}}

// sakuBrokenFp is still_open S3's fingerprint of daSaku_c::broken (v2):
// the word at each offset from broken.
var sakuBrokenFp = []struct{ off, word uint32 }{{0x1C, 0x48000819}, {0x2C, 0x90030EF8}, {0x80, 0x4BFFF931}, {0xD0, 0x4E800020}}

// The Magtail's retail d_a_mt.rel (SHA-1 9d7de862; R-D6): the pinned
// addr.Mt* (wwenemy_kinds.h EMK_MT_*) em_mt_fp_ok checks.
const (
	mtExecOff = addr.MtTextExec // daMt_Execute (rels/d_a_mt/symbols.txt:46)
	mtBc1Off  = addr.MtTextBc1  // body_control1 (:30), size 0x3C8
	mtBc2Off  = addr.MtTextBc2  // body_control2 (:18), size 0xBF4
	mtBc1Blr  = addr.MtBc1Blr
	mtBc2Blr  = addr.MtBc2Blr
	blrWord   = addr.MtFpBlr
)

var (
	mtFpExec = addr.MtFpExec
	mtFpBc1  = addr.MtFpBc1
	mtFpBc2  = addr.MtFpBc2
)

// profileExec is actor's execute, *(*(actor+0xEC)+8); ok is false when
// the method table or the execute is not in MEM1.
func profileExec(m mem.Reader, actor uint32) (exec uint32, ok bool, err error) {
	mtd, err := mem.ReadU32(m, actor+addr.ActorOffSubMtd)
	if err != nil || !addr.InMEM1(mtd) {
		return 0, false, err
	}
	exec, err = mem.ReadU32(m, mtd+addr.MethodOffExecute)
	if err != nil {
		return 0, false, err
	}
	return exec, addr.InMEM1(exec), nil
}

// words4 reads the four words at at.
func words4(m mem.Reader, at uint32) ([4]uint32, error) {
	var w [4]uint32
	for i := range w {
		v, err := mem.ReadU32(m, at+uint32(4*i))
		if err != nil {
			return w, err
		}
		w[i] = v
	}
	return w, nil
}

// passText is PASS or FAIL.
func passText(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

// rfEnv is what every refresh candidate is gated against.
type rfEnv struct {
	linkOK  bool
	link    [3]float32
	evtMode uint8
}

// readRfEnv reads Link's position and the global event mode.
func readRfEnv(m mem.Reader) (rfEnv, error) {
	var e rfEnv
	var err error
	if e.evtMode, err = mem.ReadU8(m, addr.EvtMode); err != nil {
		return e, err
	}
	p, err := mem.ReadU32(m, addr.PlayerPtr)
	if err != nil || !addr.InMEM1(p) {
		return e, err
	}
	if e.link, err = readVec(m, p+addr.ActorOffCurPos); err != nil {
		return e, err
	}
	e.linkOK = true
	return e, nil
}

// genericGates is the R-D5 step 8 gates the walk can see, "" when all
// hold (the mirror slots and Link's demo are not read here).
func genericGates(a *actors.Actor, env rfEnv) string {
	var g []string
	if a.InitState != 2 {
		g = append(g, fmt.Sprintf("init %d", a.InitState))
	}
	if a.EvtCmd != 0 {
		g = append(g, fmt.Sprintf("evtcmd %d", a.EvtCmd))
	}
	if a.Carried() {
		g = append(g, "CARRY")
	}
	if a.Pause != 0 {
		g = append(g, fmt.Sprintf("pause 0x%02X", a.Pause))
	}
	if env.evtMode != 0 {
		g = append(g, fmt.Sprintf("evt mode %d", env.evtMode))
	}
	if env.linkOK {
		dx, dy, dz := float64(env.link[0]-a.Pos[0]), float64(env.link[1]-a.Pos[1]), float64(env.link[2]-a.Pos[2])
		if d := math.Sqrt(dx*dx + dy*dy + dz*dz); d <= 300 {
			g = append(g, fmt.Sprintf("Link %.0f units", d))
		}
	}
	return strings.Join(g, ", ")
}

// rfLine is one refresh candidate's line, or "" for another proc, and
// the number of static checks it failed.
func rfLine(m mem.Reader, a *actors.Actor, env rfEnv) (string, int, error) {
	var b strings.Builder
	fails := 0
	check := func(name string, ok bool) {
		if !ok {
			fails++
		}
		fmt.Fprintf(&b, " %s %s", name, passText(ok))
	}
	// info is a check rf-probe prints but never counts.
	info := func(name string, ok bool) {
		fmt.Fprintf(&b, " %s %s (info)", name, passText(ok))
	}
	gate := ""
	prm := a.Params
	switch a.Proc {
	case procStone2:
		v, err := mem.ReadU32(m, a.Addr+stone2OffM648)
		if err != nil {
			return "", 0, err
		}
		m648 := int32(v)
		state := map[int32]string{0: "idle", 1: "lifted"}[m648]
		if state == "" {
			state = "other"
		}
		if m648 != 0 {
			gate = "m648 != 0"
		}
		fmt.Fprintf(&b, "stone2 sw %d m648 %d (%s);", (prm>>8)&0xFF, m648, state)
		check("m648-in-{0,1}", m648 == 0 || m648 == 1)
	case procLeaves:
		fmt.Fprintf(&b, "leaves sw %d itemNo %d itemSave %d;", (prm>>13)&0xFF, prm&0x3F, (prm>>6)&0x7F)
	case procSaku:
		burn, err := mem.ReadU8(m, a.Addr+sakuOffBurn)
		if err != nil {
			return "", 0, err
		}
		var st [2]int32
		var sw [2]uint32
		for i := range uint32(2) {
			v, err := mem.ReadU32(m, a.Addr+sakuOffState+4*i)
			if err != nil {
				return "", 0, err
			}
			st[i] = int32(v)
			if sw[i], err = mem.ReadU32(m, a.Addr+sakuOffSwitch+4*i); err != nil {
				return "", 0, err
			}
		}
		sw1, sw2, two := (prm>>8)&0xFF, (prm>>16)&0xFF, prm&0xF != 0
		fmt.Fprintf(&b, "saku sw1 %d sw2 %d params&0xF %d (half 2 %v) +0xEF4 %d +0xEF8 %d %d +0xF00 %d +0xF04 %d;",
			sw1, sw2, prm&0xF, two, burn, st[0], st[1], sw[0], sw[1])
		check("+0xF00==sw1", sw[0] == sw1)
		check("+0xF04==sw2", sw[1] == sw2)
		check("half2-matches-params", (st[1] != 0) == two)
		var g []string
		if burn != 0 {
			g = append(g, "burning")
		}
		for i, s := range st {
			if s == 2 {
				g = append(g, fmt.Sprintf("half %d state 2", i+1))
			} else if s != 0 && s != 1 && s != 3 {
				g = append(g, fmt.Sprintf("half %d state %d", i+1, s))
			}
		}
		gate = strings.Join(g, ", ")
		exec, ok, err := profileExec(m, a.Addr)
		if err != nil {
			return "", 0, err
		}
		if ok {
			br := exec - sakuExecOff + sakuBrokenOff
			fp := true
			fmt.Fprintf(&b, " exec 0x%08X broken 0x%08X [", exec, br)
			for i, f := range sakuBrokenFp {
				v, err := mem.ReadU32(m, br+f.off)
				if err != nil {
					return "", 0, err
				}
				if i > 0 {
					b.WriteString(" ")
				}
				fmt.Fprintf(&b, "+0x%X %08X", f.off, v)
				fp = fp && v == f.word
			}
			b.WriteString("]")
			info("broken-fp(v2)", fp)
		} else {
			b.WriteString(" exec -")
			info("broken-fp(v2)", false)
		}
	case procItem:
		bit, err := mem.ReadU32(m, a.Addr+itemOffBitNo)
		if err != nil {
			return "", 0, err
		}
		var no, flag, status uint8
		for _, f := range []struct {
			off uint32
			v   *uint8
		}{{itemOffItemNo, &no}, {itemOffFlag, &flag}, {itemOffStatus, &status}} {
			if *f.v, err = mem.ReadU8(m, a.Addr+f.off); err != nil {
				return "", 0, err
			}
		}
		fmt.Fprintf(&b, "item +0x630 %d (bit %d) +0x63A 0x%02X +0x669 0x%02X +0x66B %d;", int32(bit), bit&0x7F, no, flag, status)
		var g []string
		if bit&0x7F == itemBitNone || no == itemNoBlueJelly {
			g = append(g, "REFUSED")
		}
		if status > itemStatusWaitMax {
			g = append(g, fmt.Sprintf("status %d > %d", status, itemStatusWaitMax))
		}
		if flag&itemFlagBusy != 0 {
			g = append(g, "flag & 0x48")
		}
		gate = strings.Join(g, ", ")
	case procTbox:
		var bgOpen, bgCur, fn uint32
		for _, f := range []struct {
			off uint32
			v   *uint32
		}{{tboxOffBgOpen, &bgOpen}, {tboxOffBgCur, &bgCur}, {tboxOffActFn, &fn}} {
			v, err := mem.ReadU32(m, a.Addr+f.off)
			if err != nil {
				return "", 0, err
			}
			*f.v = v
		}
		flags, err := mem.ReadU16(m, a.Addr+tboxOffFlags)
		if err != nil {
			return "", 0, err
		}
		ft := prm & 0x7F
		fmt.Fprintf(&b, "tbox funcType %d bit %d +0x2BC 0x%08X +0x2C4 0x%08X (%s) +0x348 0x%08X +0x3F0 0x%04X;",
			ft, (prm>>7)&0x1F, bgOpen, bgCur, map[bool]string{true: "closed", false: "open"}[bgCur != bgOpen], fn, flags)
		var g []string
		if ft == 6 || ft == 7 || ft == 8 {
			g = append(g, "REFUSED funcType")
		}
		if bgCur == bgOpen {
			g = append(g, "BG open")
		}
		if flags&tboxFlagsBusy != 0 {
			g = append(g, "flags & 0x38")
		}
		exec, ok, err := profileExec(m, a.Addr)
		if err != nil {
			return "", 0, err
		}
		if !ok {
			b.WriteString(" exec -")
			check("execute-fp", false)
			g = append(g, "FP_MISS")
		} else {
			w0C, err := mem.ReadU32(m, exec+0x0C)
			if err != nil {
				return "", 0, err
			}
			w1C, err := mem.ReadU32(m, exec+0x1C)
			if err != nil {
				return "", 0, err
			}
			fmt.Fprintf(&b, " exec 0x%08X +0x0C %08X +0x1C %08X", exec, w0C, w1C)
			fp := w0C == tboxFpWord0C && w1C == tboxFpWord1C
			check("execute-fp", fp)
			if !fp {
				g = append(g, "FP_MISS")
			}
			match := ""
			for _, ac := range tboxActions {
				at := exec - tboxExecOff + ac.off
				fmt.Fprintf(&b, " %s 0x%08X", strings.TrimPrefix(ac.name, "action"), at)
				if fn == at {
					match = ac.name
				}
			}
			if match == "" {
				fmt.Fprintf(&b, "; +0x348 is none of them")
				g = append(g, "action")
			} else {
				fmt.Fprintf(&b, "; +0x348 is %s", match)
			}
			if bgCur != bgOpen && flags&tboxFlagsBusy == 0 {
				check("closed-chest-action", match != "")
			}
		}
		gate = strings.Join(g, ", ")
	default:
		return "", 0, nil
	}
	head := fmt.Sprintf("pid %-5d 0x%08X proc 0x%04X room %d params 0x%08X pos (%.1f %.1f %.1f) ", a.PID, a.Addr, uint16(a.Proc), a.HomeRoom, prm, a.Pos[0], a.Pos[1], a.Pos[2])
	tail := fmt.Sprintf(" | generic %s | proc gate %s", okText(genericGates(a, env)), okText(gate))
	return head + b.String() + tail, fails, nil
}

// rfProbe is rf-probe over m: every refresh proc's line and a summary of
// the static checks.
func rfProbe(w io.Writer, m mem.Reader) error {
	stageName, room, err := enemyScope(m)
	if err != nil {
		return err
	}
	env, err := readRfEnv(m)
	if err != nil {
		return err
	}
	aw, err := actors.Read(m, actors.MaxNodes)
	if err != nil {
		return err
	}
	link := "-"
	if env.linkOK {
		link = fmt.Sprintf("%.1f %.1f %.1f", env.link[0], env.link[1], env.link[2])
	}
	fmt.Fprintf(w, "stage %q room %d; walk %d actors (%s); Link %s; dEvtMode %d\n", stageName, room, len(aw.Actors), walkNote(&aw), link, env.evtMode)
	n, fails := 0, 0
	for i := range aw.Actors {
		line, f, err := rfLine(m, &aw.Actors[i], env)
		if err != nil {
			return err
		}
		if line == "" {
			continue
		}
		n++
		fails += f
		fmt.Fprintln(w, "  "+line)
	}
	verdict := passText(fails == 0)
	if n == 0 {
		verdict = "NONE"
	}
	fmt.Fprintf(w, "P0-RF %s: %d refresh actors, %d static check(s) failed\n", verdict, n, fails)
	return nil
}

// mtExecText is a Magtail's execute, body_control1 and body_control2
// below it, their first four words and blr words, and fp=ok|miss
// against the retail d_a_mt.rel (R-D6).
func mtExecText(m mem.Reader, actor uint32) (string, error) {
	exec, ok, err := profileExec(m, actor)
	if err != nil {
		return "", err
	}
	if !ok {
		return "exec - fp=miss", nil
	}
	bc1, bc2 := exec-mtExecOff+mtBc1Off, exec-mtExecOff+mtBc2Off
	if !addr.InMEM1(bc1) || !addr.InMEM1(bc2+mtBc2Blr) {
		return fmt.Sprintf("exec 0x%08X fp=miss", exec), nil
	}
	ew, err := words4(m, exec)
	if err != nil {
		return "", err
	}
	w1, err := words4(m, bc1)
	if err != nil {
		return "", err
	}
	w2, err := words4(m, bc2)
	if err != nil {
		return "", err
	}
	b1, err := mem.ReadU32(m, bc1+mtBc1Blr)
	if err != nil {
		return "", err
	}
	b2, err := mem.ReadU32(m, bc2+mtBc2Blr)
	if err != nil {
		return "", err
	}
	fp := "miss"
	if ew == mtFpExec && w1 == mtFpBc1 && w2 == mtFpBc2 && b1 == blrWord && b2 == blrWord {
		fp = "ok"
	}
	return fmt.Sprintf("exec 0x%08X [%08X %08X %08X %08X] bc1 0x%08X [%08X %08X %08X %08X] blr %08X bc2 0x%08X [%08X %08X %08X %08X] blr %08X fp=%s",
		exec, ew[0], ew[1], ew[2], ew[3], bc1, w1[0], w1[1], w1[2], w1[3], b1, bc2, w2[0], w2[1], w2[2], w2[3], b2, fp), nil
}

// The Magtail fields P0-MT2 reads (C1b-magtail-redo.md; the REL's, pinned
// in addr/mt.go since batch D), and the fit's limits.
const (
	mtOffWiggle  = addr.MtOffWiggle // s16 head yaw wiggle
	mtOffF470    = addr.MtOffF470   // f32 stretch: eases to -10 in MOVE, +20 in FIGHT
	mtOffBite    = addr.MtOffBite   // u8 the bite window
	mtCcCenter   = 0x118            // dCcD_Sph's centre (addr.Mo2CcCenterOff)
	mtPassResid  = 0.2
	mtSphOff0    = 100 // sph[0] = M_0 * (0,0,100)
	mtEyeOff     = 30  // eyePos = M_0 * (0,0,30)
	mtTornTries  = 3
	mtSlopeLimit = 0.5 // a sample whose segment heights spread more is sloped
	// mtF470Step is cLib_addCalc2(&f470, target, 1.0, 1.0)'s largest step
	// (c_lib.cpp:56-66). body_control1 and body_control2 ease f470 after
	// they build the matrices and sphere centres (C1b-magtail-redo.md
	// sections 2 and 4: 0x22AC, 0x15A4), so the f470 a sample reads is up
	// to one step past the one its matrices were built with.
	mtF470Step = 1.0
)

// mt2Sample is one Magtail sample.
type mt2Sample struct {
	PID, Addr uint32
	Mode      uint8
	Action    uint8
	Idx       uint32
	Dive      int16
	HitCool   int16
	Wiggle    int16
	Bite      uint8
	F470      float32
	Pos       [3]float32
	Tr, Col2  [addr.MtSegs][3]float32
	TrOK      [addr.MtSegs]bool
	Sph       [addr.MtSegs][3]float32
	Eye       [3]float32
	Torn      bool
	F470Build float64              // the f470 segments 1..7 fit, within mtF470Step of F470
	Resid     [addr.MtSegs]float64 // segment i's residual (i 0: the sph_0 rule)
	EyeResid  float64
	MaxResid  float64
	AllMatrix bool
	SlopeSpan float64
}

// readMtMatrix reads segment i's model base matrix translation and
// column 2 (row-major Mtx: [r][3] translation, [r][2] the z axis).
func readMtMatrix(m mem.Reader, morf uint32) (tr, col2 [3]float32, ok bool, err error) {
	if !addr.InMEM1(morf) {
		return tr, col2, false, nil
	}
	model, err := mem.ReadU32(m, morf+addr.McaMorfOffModel)
	if err != nil || !addr.InMEM1(model) {
		return tr, col2, false, err
	}
	b, err := m.Read(model+addr.J3DModelOffBaseTRMtx, 0x30)
	if err != nil {
		return tr, col2, false, err
	}
	if len(b) != 0x30 {
		return tr, col2, false, fmt.Errorf("0x%08X: short read", model)
	}
	for r := range 3 {
		col2[r] = mem.F32BE(b[0x10*r+0x8:])
		tr[r] = mem.F32BE(b[0x10*r+0xC:])
	}
	return tr, col2, true, nil
}

// mtClassRead is one read of the Magtail a's class.
func mtClassRead(m mem.Reader, a *actors.Actor) ([]byte, error) {
	b, err := m.Read(a.Addr, addr.MtClassSize)
	if err != nil {
		return nil, err
	}
	if len(b) != addr.MtClassSize {
		return nil, fmt.Errorf("actor 0x%08X: %d bytes read, want %d", a.Addr, len(b), addr.MtClassSize)
	}
	be := binary.BigEndian
	if be.Uint32(b[addr.ActorOffBsPcID:]) != a.PID || int16(be.Uint16(b[addr.ActorOffProcName:])) != procMt {
		return nil, fmt.Errorf("%w: %s changed under the probe", actors.ErrChanged, a)
	}
	return b, nil
}

// mtFrameKey is what one frame of the Magtail's Execute moves in a class
// read: mode and action, ring idx, f470, wiggle, pos, the morfs, the eye
// and body sphere centres. Two reads with one key bracket one frame.
func mtFrameKey(b []byte) []byte {
	var k []byte
	for _, f := range []struct{ off, n int }{
		{addr.MtOffMode, 2}, {addr.MtOffRingIdx, 4}, {mtOffF470, 4}, {mtOffWiggle, 2},
		{addr.ActorOffCurPos, 12}, {addr.MtOffMorf, 4 * addr.MtSegs}, {addr.MtOffEyeSph + mtCcCenter, 12},
	} {
		k = append(k, b[f.off:f.off+f.n]...)
	}
	for i := range addr.MtSegs {
		off := addr.MtOffBodySph + addr.MtBodySphStride*i + mtCcCenter
		k = append(k, b[off:off+12]...)
	}
	return k
}

// readMt2 reads one sample of the Magtail a: the class in one read, the
// eight matrices, then the class again. A sample whose frame key
// (mtFrameKey) moved between the two class reads is retried, and torn
// after mtTornTries.
func readMt2(m mem.Reader, a *actors.Actor) (mt2Sample, error) {
	var s mt2Sample
	for try := 0; try < mtTornTries; try++ {
		b, err := mtClassRead(m, a)
		if err != nil {
			return s, err
		}
		be := binary.BigEndian
		vec := func(off int) (v [3]float32) {
			for i := range 3 {
				v[i] = mem.F32BE(b[off+4*i:])
			}
			return v
		}
		s = mt2Sample{PID: a.PID, Addr: a.Addr, Mode: b[addr.MtOffMode], Action: b[addr.MtOffAction], Idx: be.Uint32(b[addr.MtOffRingIdx:]),
			Dive: int16(be.Uint16(b[addr.MtOffDive:])), HitCool: int16(be.Uint16(b[addr.MtOffHitCool:])), Wiggle: int16(be.Uint16(b[mtOffWiggle:])),
			Bite: b[mtOffBite], F470: mem.F32BE(b[mtOffF470:]), Pos: vec(addr.ActorOffCurPos), Eye: vec(addr.MtOffEyeSph + mtCcCenter)}
		for i := range addr.MtSegs {
			s.Sph[i] = vec(addr.MtOffBodySph + addr.MtBodySphStride*i + mtCcCenter)
			morf := be.Uint32(b[addr.MtOffMorf+4*i:])
			if s.Tr[i], s.Col2[i], s.TrOK[i], err = readMtMatrix(m, morf); err != nil {
				return s, err
			}
		}
		b2, err := mtClassRead(m, a)
		if err != nil {
			return s, err
		}
		s.Torn = !bytes.Equal(mtFrameKey(b), mtFrameKey(b2))
		if !s.Torn {
			break
		}
	}
	s.residuals()
	return s, nil
}

// dist is |a - (b + k*c)|.
func dist(a, b, c [3]float32, k float64) float64 {
	var sum float64
	for i := range 3 {
		d := float64(a[i]) - (float64(b[i]) + k*float64(c[i]))
		sum += d * d
	}
	return math.Sqrt(sum)
}

// buildF470 is the f470 the matrices of segments 1..7 were built with:
// the least-squares k of sph_i = tr_i - k*col2_i over them, clamped to
// within mtF470Step of the f470 read (the game eases f470 after the
// build). While f470 is settled it is f470 itself.
func (s *mt2Sample) buildF470() float64 {
	var num, den float64
	for i := 1; i < addr.MtSegs; i++ {
		if !s.TrOK[i] {
			continue
		}
		for r := range 3 {
			c := float64(s.Col2[i][r])
			num += (float64(s.Tr[i][r]) - float64(s.Sph[i][r])) * c
			den += c * c
		}
	}
	f := float64(s.F470)
	if den == 0 {
		return f
	}
	return min(max(num/den, f-mtF470Step), f+mtF470Step)
}

// residuals fills the design's PASS residuals: sph_i - (tr_i -
// f470*col2_i) for i >= 1 with the build f470 (buildF470), sph_0 -
// (tr_0 + 100*col2_0), eye - (tr_0 + 30*col2_0); and the height spread
// of the translations.
func (s *mt2Sample) residuals() {
	s.AllMatrix = true
	s.MaxResid = 0
	s.F470Build = s.buildF470()
	lo, hi := math.Inf(1), math.Inf(-1)
	for i := range addr.MtSegs {
		if !s.TrOK[i] {
			s.AllMatrix = false
			s.Resid[i] = math.NaN()
			continue
		}
		if i == 0 {
			s.Resid[0] = dist(s.Sph[0], s.Tr[0], s.Col2[0], mtSphOff0)
		} else {
			s.Resid[i] = dist(s.Sph[i], s.Tr[i], s.Col2[i], -s.F470Build)
		}
		s.MaxResid = max(s.MaxResid, s.Resid[i])
		lo, hi = min(lo, float64(s.Tr[i][1])), max(hi, float64(s.Tr[i][1]))
	}
	s.EyeResid = math.NaN()
	if s.TrOK[0] {
		s.EyeResid = dist(s.Eye, s.Tr[0], s.Col2[0], mtEyeOff)
		s.MaxResid = max(s.MaxResid, s.EyeResid)
	}
	s.SlopeSpan = 0
	if hi >= lo {
		s.SlopeSpan = hi - lo
	}
}

// mtModeName is a Magtail mode's name.
func mtModeName(mode uint8) string {
	switch mode {
	case 0:
		return "MOVE"
	case 1:
		return "FIGHT"
	case 2:
		return "BALL"
	case 3:
		return "DEAD"
	case 6:
		return "PINNED"
	}
	return fmt.Sprintf("mode%d", mode)
}

// line is the sample's text after its timestamp.
func (s *mt2Sample) line() string {
	var b strings.Builder
	fmt.Fprintf(&b, "pid %d mode %d %s action %d ring %d dive %d f470 %.2f (built %.2f) wiggle %d hit_cool %d bite %d pos %.1f %.1f %.1f",
		s.PID, s.Mode, mtModeName(s.Mode), s.Action, s.Idx&(addr.MtRingLen-1), s.Dive, s.F470, s.F470Build, s.Wiggle, s.HitCool, s.Bite, s.Pos[0], s.Pos[1], s.Pos[2])
	if s.Torn {
		b.WriteString(" TORN")
	}
	for i := range addr.MtSegs {
		c := s.Sph[i]
		if !s.TrOK[i] {
			fmt.Fprintf(&b, " | seg%d tr - sph %.1f %.1f %.1f", i, c[0], c[1], c[2])
			continue
		}
		t, z := s.Tr[i], s.Col2[i]
		fmt.Fprintf(&b, " | seg%d tr %.1f %.1f %.1f col2 %.4f %.4f %.4f sph %.1f %.1f %.1f d %.3f", i, t[0], t[1], t[2], z[0], z[1], z[2], c[0], c[1], c[2], s.Resid[i])
	}
	fmt.Fprintf(&b, " | eye %.1f %.1f %.1f de %.3f | max %.3f slope %.1f", s.Eye[0], s.Eye[1], s.Eye[2], s.EyeResid, s.MaxResid, s.SlopeSpan)
	switch {
	case s.Torn || !s.AllMatrix:
		b.WriteString(" skip")
	case s.MaxResid <= mtPassResid:
		b.WriteString(" ok")
	default:
		b.WriteString(" OVER")
	}
	return b.String()
}

// mt2Acc is one Magtail's run.
type mt2Acc struct {
	pid                     uint32
	samples, torn, noMatrix int
	byMode                  map[uint8]int
	maxMove, maxFight       float64
	over                    int
	sloped                  int
	maxSlope                float64
	fpLast                  string
}

// add counts s.
func (a *mt2Acc) add(s *mt2Sample) {
	a.samples++
	switch {
	case s.Torn:
		a.torn++
		return
	case !s.AllMatrix:
		a.noMatrix++
		return
	}
	a.byMode[s.Mode]++
	if s.SlopeSpan > mtSlopeLimit {
		a.sloped++
	}
	a.maxSlope = max(a.maxSlope, s.SlopeSpan)
	switch s.Mode {
	case 0:
		a.maxMove = max(a.maxMove, s.MaxResid)
	case 1:
		a.maxFight = max(a.maxFight, s.MaxResid)
	default:
		return
	}
	if s.MaxResid > mtPassResid {
		a.over++
	}
}

// verdict is the run's P0-MT2 verdict: FAIL on any MOVE or FIGHT sample
// over the bound, INCOMPLETE without both a MOVE and a FIGHT sample,
// else PASS.
func (a *mt2Acc) verdict() string {
	switch {
	case a.over > 0:
		return "FAIL"
	case a.byMode[0] == 0 || a.byMode[1] == 0:
		return "INCOMPLETE"
	}
	return "PASS"
}

// summary is the run's last lines for the Magtail.
func (a *mt2Acc) summary() string {
	var modes []string
	for _, k := range slices.Sorted(maps.Keys(a.byMode)) {
		modes = append(modes, fmt.Sprintf("%s %d", mtModeName(k), a.byMode[k]))
	}
	return fmt.Sprintf("summary pid %d: %d samples (%s), torn %d, no matrix %d, max residual MOVE %.3f FIGHT %.3f, %d over %.1f, sloped %d (max height spread %.1f)\nP0-MT2 %s pid %d (max residual <= %.1f over MOVE and FIGHT samples, both needed)\n",
		a.pid, a.samples, strings.Join(modes, ", "), a.torn, a.noMatrix, a.maxMove, a.maxFight, a.over, mtPassResid, a.sloped, a.maxSlope,
		a.verdict(), a.pid, mtPassResid)
}

// mt2Probe is mt2-probe over m for d (0: until ctx ends).
func mt2Probe(ctx context.Context, w io.Writer, m mem.Reader, d time.Duration) error {
	stageName, room, err := enemyScope(m)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "stage %q room %d; P0-MT2 wants M_NewD2 14 8 with MOVE and FIGHT\n", stageName, room)
	start := time.Now()
	var deadline time.Time
	if d > 0 {
		deadline = start.Add(d)
	}
	accs := map[uint32]*mt2Acc{}
	var order []uint32
	var mts []actors.Actor
	nextWalk := time.Time{}
	lastLine := map[uint32]string{}
	walk := func(now time.Time) error {
		aw, err := actors.Read(m, actors.MaxNodes)
		if err != nil {
			return err
		}
		mts = mts[:0]
		for i := range aw.Actors {
			a := &aw.Actors[i]
			if a.Proc != procMt || a.InitState != 2 {
				continue
			}
			mts = append(mts, *a)
			acc, ok := accs[a.PID]
			if !ok {
				acc = &mt2Acc{pid: a.PID, byMode: map[uint8]int{}}
				accs[a.PID] = acc
				order = append(order, a.PID)
			}
			fp, err := mtExecText(m, a.Addr)
			if err != nil {
				return err
			}
			if fp != acc.fpLast {
				acc.fpLast = fp
				fmt.Fprintf(w, "[%6.3fs] walk %s: pid %d 0x%08X room %d params 0x%08X %s\n", now.Sub(start).Seconds(), walkNote(&aw), a.PID, a.Addr, a.HomeRoom, a.Params, fp)
			}
		}
		nextWalk = now.Add(time.Second)
		return nil
	}
	sample := func(now time.Time) error {
		if now.After(nextWalk) {
			if err := walk(now); err != nil {
				return err
			}
		}
		for i := range mts {
			s, err := readMt2(m, &mts[i])
			if err != nil {
				if errors.Is(err, actors.ErrChanged) {
					nextWalk = time.Time{}
					continue
				}
				return err
			}
			accs[s.PID].add(&s)
			if l := s.line(); l != lastLine[s.PID] {
				lastLine[s.PID] = l
				fmt.Fprintf(w, "[%6.3fs] %s\n", now.Sub(start).Seconds(), l)
			}
		}
		return nil
	}
	finish := func() error {
		if len(order) == 0 {
			fmt.Fprintln(w, "P0-MT2 NONE: no executing Magtail (proc 0x00D9) was seen")
			return nil
		}
		for _, pid := range order {
			fmt.Fprint(w, accs[pid].summary())
		}
		return nil
	}
	if err := sample(start); err != nil {
		return err
	}
	t := time.NewTicker(enemyWatchTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return finish()
		case now := <-t.C:
			if !deadline.IsZero() && now.After(deadline) {
				return finish()
			}
			if err := sample(now); err != nil {
				return err
			}
		}
	}
}

// runBatchDProbe runs bst-probe, rf-probe and mt2-probe.
func runBatchDProbe(cmd string, args []string) {
	usage := map[string]string{"bst-probe": bstProbeUsage, "rf-probe": rfProbeUsage, "mt2-probe": mt2ProbeUsage}[cmd]
	pos, _, err := diag.SplitFlags(args)
	if err != nil {
		exitUsage(err, usage)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	switch cmd {
	case "bst-probe":
		secs, perr := diagIntArg(pos, 0, 0, 0, 86400, "secs")
		if perr != nil {
			exitUsage(perr, usage)
		}
		err = diag.WithMem(openDolphin, func(m mem.Mem) error { return bstProbe(ctx, os.Stdout, m, time.Duration(secs)*time.Second) })
	case "rf-probe":
		if len(pos) > 0 {
			exitUsage(errors.New("rf-probe takes no arguments"), usage)
		}
		err = diag.WithMem(openDolphin, func(m mem.Mem) error { return rfProbe(os.Stdout, m) })
	case "mt2-probe":
		secs, perr := diagIntArg(pos, 0, 10, 0, 86400, "secs")
		if perr != nil {
			exitUsage(perr, usage)
		}
		err = diag.WithMem(openDolphin, func(m mem.Mem) error { return mt2Probe(ctx, os.Stdout, m, time.Duration(secs)*time.Second) })
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmd, err)
		os.Exit(1)
	}
}
