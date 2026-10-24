package main

// Batch F's boss_kill probe (ww-multiplayer-handoff/design/relay-helmaroc.md
// "Read-only probe" and "Live test recipe"; docs/TESTING.md "Batch F"). It
// only reads game memory:
//
//   - bdk-probe [secs=0]: P0-BDK. Every proc 0xF0 actor (one in M2tower,
//     on layer 3 only) with the fields bk_bdk reads (the morf, mAction,
//     m8F8, m25A0, health) and the probe's own (max_health, m2B4, m2C4,
//     mState, m2F8, the position), each as the raw word it sits in and
//     decoded, Link's y against the climb's thresholds, and the answer
//     bk_bdk would give for the first executing one, as bk_SearchByName
//     finds it. Run it through the climb, the hammer hit (or the `down`
//     poke), the intro and the mask break before any REL write.
//
// With secs > 0 it samples every tick and prints only on change, as
// bwd-probe does. The REL's offsets are the pinned mailbox.BossBdk*
// (wwboss.h); m2B4, m2C4, mState, m2F8 and the position are this probe's.

import (
	"context"
	"fmt"
	"io"
	"math"
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

const bdkProbeUsage = "bdk-probe [secs=0, one read]"

// procBdk is Helmaroc King's proc (f_pc_name.h:252).
const procBdk = int16(stage.ProcBDK) // 0x0F0

// Helmaroc King (bdk_class, d_a_bdk.h:40-179): the word each field sits in.
const (
	bdkOffHealth = mailbox.BossBdkHealthOff - 1 // s8 max_health, s8 health (+0x285), then the pad
	bdkOffM2B4   = 0x2B4                        // u8 m2B4, the phase: 1 tower, 0 arena (d_a_bdk.h:44)
	bdkOffMorf   = mailbox.BossBdkMorfOff       // mDoExt_McaMorf* mpMorf
	bdkOffAct    = mailbox.BossBdkActOff - 2    // s16 m2C4 (the frame counter), s16 mAction (+0x2C6)
	bdkOffState  = 0x2C8                        // s16 mState, s16 m2CA
	bdkOffM2F8   = 0x2F8                        // s16 m2F8 (the hit i-frames), s16 m2FA
	bdkOffMask   = mailbox.BossBdkMaskOff       // u8 m8F8, then the pad
	bdkOffDemo   = mailbox.BossBdkDemoOff       // s16 m25A0, s8 m25A2, then a byte
)

// The climb's thresholds on Link's y: T_LASTATTACK above 9250 (d_a_bdk.cpp:
// 2164-2166) and start() at 9800 (:1784).
const (
	bdkLastAttackY = 9250
	bdkStartY      = 9800
)

// bdkActor is one proc 0xF0 actor as bdk-probe reads it.
type bdkActor struct {
	PID, Addr uint32
	Init      int8
	W284      uint32 // max_health, health, pad
	W2B4      uint32 // m2B4, then three bytes
	Morf      uint32
	W2C4      uint32 // m2C4 << 16 | mAction
	W2C8      uint32 // mState << 16 | m2CA
	W2F8      uint32 // m2F8 << 16 | m2FA
	W8F8      uint32 // m8F8, then the pad
	W25A0     uint32 // m25A0 << 16 | m25A2 << 8 | a byte
	Pos       [3]float32
}

func (b *bdkActor) maxHP() int8    { return int8(b.W284 >> 24) }
func (b *bdkActor) hp() int8       { return int8(b.W284 >> 16) }
func (b *bdkActor) phase() uint8   { return uint8(b.W2B4 >> 24) }
func (b *bdkActor) frame() int16   { return int16(b.W2C4 >> 16) }
func (b *bdkActor) act() int16     { return int16(b.W2C4) }
func (b *bdkActor) state() int16   { return int16(b.W2C8 >> 16) }
func (b *bdkActor) iframes() int16 { return int16(b.W2F8 >> 16) }
func (b *bdkActor) mask() uint8    { return uint8(b.W8F8 >> 24) }
func (b *bdkActor) demo() int16    { return int16(b.W25A0 >> 16) }
func (b *bdkActor) air() int8      { return int8(b.W25A0 >> 8) }
func (b *bdkActor) result() uint32 {
	return uint32(uint8(b.act()))<<16 | uint32(b.mask())<<8 | uint32(uint8(b.hp()))
}

// readBdk reads every proc 0xF0 actor of aw, in queue order.
func readBdk(m mem.Reader, aw *actors.Walk) ([]bdkActor, error) {
	var bs []bdkActor
	for i := range aw.Actors {
		a := &aw.Actors[i]
		if a.Proc != procBdk {
			continue
		}
		if err := checkPID(m, a); err != nil {
			return nil, err
		}
		b := bdkActor{PID: a.PID, Addr: a.Addr, Init: a.InitState}
		var pos [3]uint32
		if err := readU32s(m, a.Addr, []struct {
			off uint32
			v   *uint32
		}{{bdkOffHealth, &b.W284}, {bdkOffM2B4, &b.W2B4}, {bdkOffMorf, &b.Morf}, {bdkOffAct, &b.W2C4}, {bdkOffState, &b.W2C8},
			{bdkOffM2F8, &b.W2F8}, {bdkOffMask, &b.W8F8}, {bdkOffDemo, &b.W25A0},
			{addr.ActorOffCurPos, &pos[0]}, {addr.ActorOffCurPos + 4, &pos[1]}, {addr.ActorOffCurPos + 8, &pos[2]}}); err != nil {
			return nil, err
		}
		for j := range pos {
			b.Pos[j] = math.Float32frombits(pos[j])
		}
		bs = append(bs, b)
	}
	return bs, nil
}

// bdkVerdict is the answer bk_bdk gives b and its result word (byte +0 R,
// +1 mAction, +2 m8F8, +3 health), with why it is not APPLIED.
func bdkVerdict(b *bdkActor) (r uint8, word uint32, why string) {
	word = b.result()
	act := b.act()
	switch {
	case b.Morf == 0:
		r, why = mailbox.BossRNoActor, "the morf is NULL"
	case act == mailbox.BossBdkActEnd || act == mailbox.BossBdkActAfter:
		r = mailbox.BossRAlready
	case act < 0 || act > mailbox.BossBdkActFightMax:
		r, why = mailbox.BossRWaiting, fmt.Sprintf("mAction 0x%X not 0..8 (%s)", act, bdkActName(act))
	case b.hp() <= 0:
		r, why = mailbox.BossRAlready, "health <= 0 at a fight action: a pending zero, the next crest hit kills"
	case b.mask() >= mailbox.BossBdkMaskBroken && b.demo() == 0:
		r = mailbox.BossRApplied
	default:
		r = mailbox.BossRWaiting
		var miss []string
		if b.mask() < mailbox.BossBdkMaskBroken {
			miss = append(miss, fmt.Sprintf("m8F8 %d < 4 (the mask)", b.mask()))
		}
		if b.demo() != 0 {
			miss = append(miss, fmt.Sprintf("m25A0 0x%X != 0 (a demo camera)", b.demo()))
		}
		why = strings.Join(miss, ", ")
	}
	return r, uint32(r)<<24 | word, why
}

// bdkActName names an mAction (d_a_bdk.cpp:26-45).
func bdkActName(act int16) string {
	switch {
	case act >= 0 && act <= mailbox.BossBdkActFightMax:
		return "the arena fight"
	case act == mailbox.BossBdkActMaskDemo:
		return "KAMEN_DEMO"
	case act == mailbox.BossBdkActEnd:
		return "END"
	case act == mailbox.BossBdkActStart:
		return "START, the intro"
	case act >= mailbox.BossBdkActTFly && act <= mailbox.BossBdkActTDown:
		return "the tower phase"
	case act == mailbox.BossBdkActAfter:
		return "AFTER_FIGHT"
	}
	return "unknown"
}

// bdkReport is bdk-probe's text for one read: the count, each actor, Link's
// y (linkY, ok false when there is no Link) and the verdict for the one
// bk_SearchByName would find (the first executing).
func bdkReport(bs []bdkActor, linkY float32, linkOK bool) string {
	var b strings.Builder
	n := fmt.Sprintf("%d proc 0xF0 actors", len(bs))
	if len(bs) != 1 {
		n += " (want 1: Helmaroc King, M2tower layer 3)"
	}
	b.WriteString(n + "\n")
	var found *bdkActor
	for i := range bs {
		x := &bs[i]
		fmt.Fprintf(&b, "  bdk at 0x%08X pid %-5d init %d morf 0x%08X | +0x284 %08X (max_health %d health %d) +0x2B4 %08X (m2B4 %d) +0x2C4 %08X (m2C4 %d mAction 0x%X, %s) +0x2C8 %08X (mState %d) +0x2F8 %08X (m2F8 %d) +0x8F8 %08X (m8F8 %d) +0x25A0 %08X (m25A0 0x%X m25A2 %d) pos (%.0f, %.0f, %.0f)\n",
			x.Addr, x.PID, x.Init, x.Morf, x.W284, x.maxHP(), x.hp(), x.W2B4, x.phase(), x.W2C4, x.frame(), x.act(), bdkActName(x.act()),
			x.W2C8, x.state(), x.W2F8, x.iframes(), x.W8F8, x.mask(), x.W25A0, x.demo(), x.air(), x.Pos[0], x.Pos[1], x.Pos[2])
		if found == nil && x.Init == 2 {
			found = x
		}
	}
	if linkOK {
		where := "below 9250 (the climb)"
		switch {
		case linkY >= bdkStartY:
			where = "at or above 9800 (start() takes it)"
		case linkY > bdkLastAttackY:
			where = "above 9250 (T_LASTATTACK)"
		}
		fmt.Fprintf(&b, "  Link y %.1f: %s\n", linkY, where)
	} else {
		b.WriteString("  Link: none\n")
	}
	if found == nil {
		fmt.Fprintf(&b, "  bk_bdk would answer NO_ACTOR 0x%08X: no executing Bdk\n", uint32(mailbox.BossRNoActor)<<24)
		return b.String()
	}
	r, word, why := bdkVerdict(found)
	fmt.Fprintf(&b, "  bk_bdk would answer %s 0x%08X", bossRName(r), word)
	if why != "" {
		b.WriteString(": " + why)
	}
	b.WriteString("\n")
	// m2B4 is implied 0 by m8F8 >= 4 (the design's gate); the REL does not
	// test it, the probe flags it.
	if found.mask() >= mailbox.BossBdkMaskBroken && found.phase() != 0 {
		fmt.Fprintf(&b, "  note: m2B4 %d with m8F8 %d (expected 0 in the arena)\n", found.phase(), found.mask())
	}
	return b.String()
}

// linkPosY is Link's current.pos.y, ok false when mpPlayerPtr[0] holds no
// actor.
func linkPosY(m mem.Reader) (float32, bool, error) {
	p, err := mem.ReadU32(m, addr.PlayerPtr)
	if err != nil {
		return 0, false, err
	}
	if !addr.InMEM1(p) {
		return 0, false, nil
	}
	v, err := mem.ReadU32(m, p+addr.ActorOffCurPos+4)
	if err != nil {
		return 0, false, err
	}
	return math.Float32frombits(v), true, nil
}

// bdkProbe is bdk-probe over m.
func bdkProbe(ctx context.Context, w io.Writer, m mem.Reader, d time.Duration) error {
	return bossProbe(ctx, w, m, d, "P0-BDK wants M2tower 30 0 3", func(aw *actors.Walk) (string, error) {
		bs, err := readBdk(m, aw)
		if err != nil {
			return "", err
		}
		y, ok, err := linkPosY(m)
		if err != nil {
			return "", err
		}
		return bdkReport(bs, y, ok), nil
	})
}

// runBatchFProbe runs bdk-probe.
func runBatchFProbe(cmd string, args []string) {
	pos, _, err := diag.SplitFlags(args)
	if err != nil {
		exitUsage(err, bdkProbeUsage)
	}
	secs, perr := diagIntArg(pos, 0, 0, 0, 86400, "secs")
	if perr != nil {
		exitUsage(perr, bdkProbeUsage)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err = diag.WithMem(openDolphin, func(m mem.Mem) error { return bdkProbe(ctx, os.Stdout, m, time.Duration(secs)*time.Second) })
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmd, err)
		os.Exit(1)
	}
}
