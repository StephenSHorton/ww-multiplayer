package main

// Batch E's boss_kill probes (ww-multiplayer-handoff/design/relay-molgera.md
// "Live test recipe", relay-jalhalla.md "Probe"; docs/TESTING.md "Batch
// E"). Both only read game memory:
//
//   - bwd-probe [secs=0]: Molgera's optional P0. Every proc 0xDA actor
//     (one in kazeB r0) with the fields bk_bwd reads (the head morf,
//     m18AE, m18B0, m1BB1, m1BB4, m1BB5, m3C1E), each as the raw word it
//     sits in and decoded, and the answer bk_bwd would give. Run it
//     through an intro, a wait and one Hookshot pull (m18B0 2 with m1BB1
//     counting down from 120) before any REL write.
//   - bpw-probe [secs=0]: P0-BPW. Every proc 0xD4 actor (the body, the
//     kantera, the damage balls) with mType, mInitState, mBodyAction,
//     mActionState and health, then every live Poe (proc 0xD5) with its
//     health, m344, m345 and its parent pid +0x36C, and the answer bk_bpw
//     would give: the first proc 0xD4 actor in the queue with mType 0 and
//     mInitState 2, as bk_bpw_judge takes it through fopAcIt_Judge.
//
// With secs > 0 each samples every tick and prints only on change, as
// bst-probe does. The REL's offsets are the pinned mailbox.BossBwd* and
// mailbox.BossBpw* (wwboss.h); the Poe fields are this probe's own.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
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
	bwdProbeUsage = "bwd-probe [secs=0, one read]"
	bpwProbeUsage = "bpw-probe [secs=0, one read]"
)

// The procs batch E reads (f_pc_name.h).
const (
	procBwd = int16(stage.ProcBWD) // 0x0DA
	procBpw = int16(stage.ProcBPW) // 0x0D4
	procPw  = int16(stage.ProcPW)  // 0x0D5, Jalhalla's Poes
)

// Molgera (bwd_class, d_a_bwd.h): the word each field bk_bwd reads sits in.
const (
	bwdOffMorf = mailbox.BossBwdMorfOff       // mDoExt_McaMorf* mpHeadMorf
	bwdOffAct  = mailbox.BossBwdActOff - 2    // s16 m18AC, s16 m18AE (+0x18AE)
	bwdOffStep = mailbox.BossBwdStepOff       // s16 m18B0, s16 m18B2
	bwdOffM1BB = mailbox.BossBwdTongueOff - 1 // s8 m1BB0, m1BB1 (tongue), m1BB2, m1BB3
	bwdOffHits = mailbox.BossBwdHitsOff       // s8 m1BB4 (hits), m1BB5 (crit), m1BB6, m1BB7
	bwdOffDemo = mailbox.BossBwdDemoOff - 2   // s16 m3C1C, s16 m3C1E (+0x3C1E)
)

// bwdActor is one proc 0xDA actor as bwd-probe reads it.
type bwdActor struct {
	PID, Addr uint32
	Init      int8
	Morf      uint32
	W18AC     uint32 // m18AC << 16 | m18AE
	W18B0     uint32 // m18B0 << 16 | m18B2
	W1BB0     uint32 // m1BB0..m1BB3, m1BB1 the tongue timer
	W1BB4     uint32 // m1BB4..m1BB7, m1BB4 the hits, m1BB5 the critical pulls
	W3C1C     uint32 // m3C1C << 16 | m3C1E
}

func (b *bwdActor) act() int16   { return int16(b.W18AC) }
func (b *bwdActor) step() int16  { return int16(b.W18B0 >> 16) }
func (b *bwdActor) tongue() int8 { return int8(b.W1BB0 >> 16) }
func (b *bwdActor) hits() int8   { return int8(b.W1BB4 >> 24) }
func (b *bwdActor) crit() int8   { return int8(b.W1BB4 >> 16) }
func (b *bwdActor) demo() int16  { return int16(b.W3C1C) }
func (b *bwdActor) result() uint32 {
	return uint32(uint8(b.act()))<<16 | uint32(uint8(b.step()))<<8 | uint32(uint8(b.crit()))
}

// readU32s reads each word at base + off into its field.
func readU32s(m mem.Reader, base uint32, fs []struct {
	off uint32
	v   *uint32
}) error {
	for _, f := range fs {
		var err error
		if *f.v, err = mem.ReadU32(m, base+f.off); err != nil {
			return err
		}
	}
	return nil
}

// checkPID is actors.ErrChanged when a's pid moved since the walk.
func checkPID(m mem.Reader, a *actors.Actor) error {
	pid, err := mem.ReadU32(m, a.Addr+addr.ActorOffBsPcID)
	if err != nil {
		return err
	}
	if pid != a.PID {
		return fmt.Errorf("%w: %s changed under the probe", actors.ErrChanged, a)
	}
	return nil
}

// readBwd reads every proc 0xDA actor of aw, in queue order.
func readBwd(m mem.Reader, aw *actors.Walk) ([]bwdActor, error) {
	var bs []bwdActor
	for i := range aw.Actors {
		a := &aw.Actors[i]
		if a.Proc != procBwd {
			continue
		}
		if err := checkPID(m, a); err != nil {
			return nil, err
		}
		b := bwdActor{PID: a.PID, Addr: a.Addr, Init: a.InitState}
		if err := readU32s(m, a.Addr, []struct {
			off uint32
			v   *uint32
		}{{bwdOffMorf, &b.Morf}, {bwdOffAct, &b.W18AC}, {bwdOffStep, &b.W18B0}, {bwdOffM1BB, &b.W1BB0}, {bwdOffHits, &b.W1BB4}, {bwdOffDemo, &b.W3C1C}}); err != nil {
			return nil, err
		}
		bs = append(bs, b)
	}
	return bs, nil
}

// bwdVerdict is the answer bk_bwd gives b and its result word (byte +0 R,
// +1 m18AE, +2 m18B0, +3 m1BB5), with why it is not APPLIED.
func bwdVerdict(b *bwdActor) (r uint8, word uint32, why string) {
	word = b.result()
	switch {
	case b.Morf == 0:
		r, why = mailbox.BossRNoActor, "the head morf is NULL"
	case b.act() == mailbox.BossBwdActEnd || b.act() == mailbox.BossBwdActInert:
		r = mailbox.BossRAlready
	case b.act() == mailbox.BossBwdActTongue && b.step() == mailbox.BossBwdStepPulled && b.tongue() != 0 && b.demo() == 0:
		r = mailbox.BossRApplied
	default:
		r = mailbox.BossRWaiting
		var miss []string
		if b.act() != mailbox.BossBwdActTongue {
			miss = append(miss, fmt.Sprintf("m18AE %d != 3", b.act()))
		}
		if b.step() != mailbox.BossBwdStepPulled {
			miss = append(miss, fmt.Sprintf("m18B0 %d != 2", b.step()))
		}
		if b.tongue() == 0 {
			miss = append(miss, "m1BB1 0")
		}
		if b.demo() != 0 {
			miss = append(miss, fmt.Sprintf("m3C1E %d != 0", b.demo()))
		}
		why = strings.Join(miss, ", ")
	}
	return r, uint32(r)<<24 | word, why
}

// bwdReport is bwd-probe's text for one read: the count, each actor and
// the verdict for the one bk_SearchByName would find (the first
// executing).
func bwdReport(bs []bwdActor) string {
	var b strings.Builder
	n := fmt.Sprintf("%d proc 0xDA actors", len(bs))
	if len(bs) != 1 {
		n += " (want 1: Molgera)"
	}
	b.WriteString(n + "\n")
	var found *bwdActor
	for i := range bs {
		x := &bs[i]
		fmt.Fprintf(&b, "  pid %-5d 0x%08X init %d morf 0x%08X | +0x18AC %08X (m18AE %d) +0x18B0 %08X (m18B0 %d) +0x1BB0 %08X (m1BB1 %d) +0x1BB4 %08X (m1BB4 %d m1BB5 %d) +0x3C1C %08X (m3C1E %d)\n",
			x.PID, x.Addr, x.Init, x.Morf, x.W18AC, x.act(), x.W18B0, x.step(), x.W1BB0, x.tongue(), x.W1BB4, x.hits(), x.crit(), x.W3C1C, x.demo())
		if found == nil && x.Init == 2 {
			found = x
		}
	}
	if found == nil {
		fmt.Fprintf(&b, "  bk_bwd would answer NO_ACTOR 0x%08X: no executing Bwd\n", uint32(mailbox.BossRNoActor)<<24)
		return b.String()
	}
	r, word, why := bwdVerdict(found)
	fmt.Fprintf(&b, "  bk_bwd would answer %s 0x%08X", bossRName(r), word)
	if why != "" {
		b.WriteString(": " + why)
	}
	b.WriteString("\n")
	return b.String()
}

// The Poe (pw_class, d_a_pw.h): relay-jalhalla.md's field map.
const (
	pwOffM344   = 0x344 // u8 m344 "the last Poe", m345 "counted"
	pwOffParent = 0x36C // u32 mJalhallaID, the body's pid
)

// bpwActor is one proc 0xD4 actor as bpw-probe reads it.
type bpwActor struct {
	PID, Addr uint32
	Init      int8
	Type      uint8
	W444      uint32 // mBodyAction << 16 | mActionState
	W284      uint32 // max_health, health, then the pad
}

func (b *bpwActor) act() int16 { return int16(b.W444 >> 16) }
func (b *bpwActor) st() int16  { return int16(b.W444) }
func (b *bpwActor) hp() int8   { return int8(b.W284 >> 16) }
func (b *bpwActor) maxHP() int8 {
	return int8(b.W284 >> 24)
}

// pwActor is one live Poe.
type pwActor struct {
	PID, Addr uint32
	Init      int8
	W284      uint32 // max_health, health
	W344      uint32 // m344, m345, then the next two bytes
	Parent    uint32
}

// readBpw reads every proc 0xD4 and proc 0xD5 actor of aw, in queue order.
func readBpw(m mem.Reader, aw *actors.Walk) ([]bpwActor, []pwActor, error) {
	var bs []bpwActor
	var ps []pwActor
	for i := range aw.Actors {
		a := &aw.Actors[i]
		if a.Proc != procBpw && a.Proc != procPw {
			continue
		}
		if err := checkPID(m, a); err != nil {
			return nil, nil, err
		}
		if a.Proc == procPw {
			p := pwActor{PID: a.PID, Addr: a.Addr, Init: a.InitState}
			if err := readU32s(m, a.Addr, []struct {
				off uint32
				v   *uint32
			}{{addr.ActorOffMaxHealth, &p.W284}, {pwOffM344, &p.W344}, {pwOffParent, &p.Parent}}); err != nil {
				return nil, nil, err
			}
			ps = append(ps, p)
			continue
		}
		b := bpwActor{PID: a.PID, Addr: a.Addr, Init: a.InitState}
		var err error
		if b.Type, err = mem.ReadU8(m, a.Addr+mailbox.BossBpwTypeOff); err != nil {
			return nil, nil, err
		}
		if err := readU32s(m, a.Addr, []struct {
			off uint32
			v   *uint32
		}{{mailbox.BossBpwActionOff, &b.W444}, {addr.ActorOffMaxHealth, &b.W284}}); err != nil {
			return nil, nil, err
		}
		bs = append(bs, b)
	}
	return bs, ps, nil
}

// bpwTypeName is mType's name.
func bpwTypeName(t uint8) string {
	switch t {
	case 0:
		return "body"
	case 1:
		return "kantera"
	case 2:
		return "ball"
	case 3:
		return "torituki"
	}
	return fmt.Sprintf("type%d", t)
}

// bpwVerdict is the answer bk_bpw gives the actors and its result word
// (byte +0 R, +1 mBodyAction, +2 mActionState, +3 health), with why it is
// not APPLIED: the body is the first in queue order bk_bpw_judge takes.
func bpwVerdict(bs []bpwActor) (r uint8, word uint32, why string) {
	var b *bpwActor
	for i := range bs {
		if bs[i].Type == mailbox.BossBpwTypeBody && bs[i].Init == 2 {
			b = &bs[i]
			break
		}
	}
	if b == nil {
		return mailbox.BossRNoActor, uint32(mailbox.BossRNoActor) << 24, "no executing body (mType 0, init 2)"
	}
	act, st, hp := b.act(), b.st(), b.hp()
	word = uint32(uint8(act))<<16 | uint32(uint8(st))<<8 | uint32(uint8(hp))
	gate := st >= mailbox.BossBpwStFrozenLo && st <= mailbox.BossBpwStThrown || st == mailbox.BossBpwStSpike || st == mailbox.BossBpwStSpikeAir
	switch {
	case hp <= 0 || act == mailbox.BossBpwActSeparate && st >= mailbox.BossBpwStDeath:
		r = mailbox.BossRAlready
	case act == mailbox.BossBpwActDamage && gate:
		r = mailbox.BossRApplied
	default:
		r = mailbox.BossRWaiting
		var miss []string
		if act != mailbox.BossBpwActDamage {
			miss = append(miss, fmt.Sprintf("mBodyAction %d != 3", act))
		}
		if !gate {
			miss = append(miss, fmt.Sprintf("mActionState 0x%X not 0x52..0x55, 0x64 or 0x65", st))
		}
		why = strings.Join(miss, ", ")
	}
	return r, uint32(r)<<24 | word, why
}

// bpwReport is bpw-probe's text for one read: each proc 0xD4 actor, the
// live Poes and the verdict.
func bpwReport(bs []bpwActor, ps []pwActor) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d proc 0xD4 actors\n", len(bs))
	for i := range bs {
		x := &bs[i]
		fmt.Fprintf(&b, "  %-8s pid %-5d 0x%08X init %d +0x3DC %d | +0x444 %08X (mBodyAction %d mActionState 0x%X) +0x284 %08X (max_health %d health %d)\n",
			bpwTypeName(x.Type), x.PID, x.Addr, x.Init, x.Type, x.W444, x.act(), x.st(), x.W284, x.maxHP(), x.hp())
	}
	fmt.Fprintf(&b, "%d proc 0xD5 Poes\n", len(ps))
	for i := range ps {
		p := &ps[i]
		fmt.Fprintf(&b, "  poe pid %-5d 0x%08X init %d health %d m344 %d m345 %d +0x36C %d\n",
			p.PID, p.Addr, p.Init, int8(p.W284>>16), uint8(p.W344>>24), uint8(p.W344>>16), p.Parent)
	}
	r, word, why := bpwVerdict(bs)
	fmt.Fprintf(&b, "  bk_bpw would answer %s 0x%08X", bossRName(r), word)
	if why != "" {
		b.WriteString(": " + why)
	}
	b.WriteString("\n")
	return b.String()
}

// bossProbe is bwd-probe or bpw-probe over m: one read, or, for d > 0, a
// read every tick for d (a negative d: until ctx ends) printed only when
// it changes. The queue is walked once a second, or again when a pid
// moved. report reads the walk and returns the text.
func bossProbe(ctx context.Context, w io.Writer, m mem.Reader, d time.Duration, want string, report func(*actors.Walk) (string, error)) error {
	stageName, room, err := enemyScope(m)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "stage %q room %d; %s\n", stageName, room, want)
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
		rep, err := report(&aw)
		if errors.Is(err, actors.ErrChanged) {
			nextWalk = time.Time{}
			return nil
		}
		if err != nil {
			return err
		}
		if rep != last {
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

// bwdProbe is bwd-probe over m.
func bwdProbe(ctx context.Context, w io.Writer, m mem.Reader, d time.Duration) error {
	return bossProbe(ctx, w, m, d, "bwd-probe wants kazeB (0 0 or 1 0)", func(aw *actors.Walk) (string, error) {
		bs, err := readBwd(m, aw)
		if err != nil {
			return "", err
		}
		return bwdReport(bs), nil
	})
}

// bpwProbe is bpw-probe over m.
func bpwProbe(ctx context.Context, w io.Writer, m mem.Reader, d time.Duration) error {
	return bossProbe(ctx, w, m, d, "P0-BPW wants M_DaiB 0 0", func(aw *actors.Walk) (string, error) {
		bs, ps, err := readBpw(m, aw)
		if err != nil {
			return "", err
		}
		return bpwReport(bs, ps), nil
	})
}

// runBatchEProbe runs bwd-probe and bpw-probe.
func runBatchEProbe(cmd string, args []string) {
	usage := map[string]string{"bwd-probe": bwdProbeUsage, "bpw-probe": bpwProbeUsage}[cmd]
	pos, _, err := diag.SplitFlags(args)
	if err != nil {
		exitUsage(err, usage)
	}
	secs, perr := diagIntArg(pos, 0, 0, 0, 86400, "secs")
	if perr != nil {
		exitUsage(perr, usage)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	probe := bwdProbe
	if cmd == "bpw-probe" {
		probe = bpwProbe
	}
	err = diag.WithMem(openDolphin, func(m mem.Mem) error { return probe(ctx, os.Stdout, m, time.Duration(secs)*time.Second) })
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmd, err)
		os.Exit(1)
	}
}
