package main

// enemy-watch --kind bk|cc and enemy-match --dry: the L0 read-only
// tooling of the generalised enemy mirror (ww-multiplayer-handoff/design/
// enemy-mirror-general.json, implementation_batches n=1 and test_plan
// live L0). Nothing is written.
//
// enemy-watch --kind bk|cc walks the actors every 500 ms, lists every
// actor of the kind in Link's room with its identity (pid, setID,
// params, home room, home.pos raw bits, pkey) and the static gates'
// verdict, then samples each taken one every 16 ms through
// enemysync.ReadSampleBK / ReadSampleCC and the kind's resource table:
// the sampled fields, the derived SF_* flags and flags2, the BCK index
// and name (for a ChuChu next to its own m320), and for a Bokoblin the
// BOKO its m1200 names, with that BOKO's proc and mNowMode.
// --kind mo2 (and no --kind) is the Moblin probe, unchanged.
//
// enemy-watch --kind ki|am|am2 is the allow batch's probe
// (ww-multiplayer-handoff/design/enemy-mirror-allow.json,
// implementation_batches A0, A1): rows of the Kinds table since A1,
// classified by ClassifyWalk and sampled by ReadKindSample (ReadSampleKI
// / AM / AM2) like the others. Each tick it also prints what A0
// verified live: current.angle beside shape_angle (the
// AngleOff question; "ang" is shape.y), the kind's action fields, the BCK
// from the morf beside mCurrBckIdx, every collider's At/Tg/Co mSPrm words,
// centre and radius, the attention flags, the status word, the ice
// timers, the Keese's LUNGE and home.pos, the Armos's weak-sphere Tg bit,
// and the dormant rule the host would apply.
//
// enemy-match --dry lists one Dolphin's Idents for its current stage and
// room as key=value lines, sorted by Ident. Each line carries what both
// machines must agree on (the placement-derived identity and the
// verdict) before " pid=", and everything per-machine or live from
// " pid=" on: the pids, the live gate bytes (bk_type, cc_*, mo2_mode,
// ki_action, am_action, am2_action and the allow kinds' others; "-" when
// the classification did not read one) and a Bokoblin's BOKO state, which the game changes as it
// plays. Two Dolphins in the same room therefore diff clean after
// `sed 's/ pid=.*//'` on both outputs. The allow kinds (ki, am, am2)
// are rows like the others: listed by default and when --kinds names
// them, with the extra live gate bytes their LiveOK reads (ki_m2dc,
// am_mode, am2_mode, am2_abyss, am2_carried). A keyed Keese's pkey
// hashes no home.pos (enemysync.PKeyFor, design D9), so it matches across
// machines although the Keese moves its home (d_a_ki.cpp:681-682); the
// home printed still can differ.
//
// enemy-watch --kind tn is the TN batch's probe (ww-multiplayer-handoff/
// design/enemy-mirror-tn.json, test_plan P0-b; B0 read the Darknut as a
// staged row, B1 made it row 6 of Kinds): classified by ClassifyWalk
// (readTnGate's live gate, which refuses), sampled by ReadKindSample
// (ReadSampleTN) and its BCK resolved through the tracker's Tn table like
// the other rows. Each tick prints what P0-b checks: dr.mAction / mMode (action,
// mode), pieces 0x2D0, equipment 0x3C5, m1444, m03F0, m140C, the sword
// mBokoPcId resolved to its actor (a proc 0x1D5 BOKO), the would-be live
// gate (TnLiveRefusal) and every collider. Each walk line also prints the
// actor's execute function (*(*(actor+0xEC)+8)) and the two P0-c
// addresses below it, daTn_Execute-0x1040 (part_move) and -0x224C
// (yoroi_break); dump their words with ppc-disasm (docs/TESTING.md).
// enemy-match --dry lists tn (and tn+u) like any row, by default and
// when --kinds names it; without "+u" an unkeyed Darknut is unkeyed-off.
// Its live gate bytes (tn_behavior .. tn_m140c) and TnLiveRefusal
// (tn_live) come after " pid="; a refusal by the live gate is
// verdict=live-gate (the host's walk, which also keeps a listed Darknut
// through the gate's non-BOSS terms, liveSticky, has no "was" here).
//
// enemy-watch --kind bb and --kind mt are batch C's P0-BB and P0-MT
// probes (ww-multiplayer-handoff/design/enemy-mirror-batch-c.json
// test_plan; rows 7 and 8 of Kinds, off by default, which --kind and
// --kinds enable for the probe alone; batch D restored row 8, G-D6/G-D7).
// A Kargaroc's walk line prints its execute, tail_control below it (exec - addr.BbExecToTail) and the
// tail fingerprint words with fp=ok or fp=miss (the REL's em_bb_fp_ok);
// its tick prints the type, state, unk_2DF, unk_2F2, unk_C7E, the
// knockback, the attack timer, unk_366/unk_368, the would-be live gate
// (BbLiveRefusal), the tail root unk_BD4[1] beside unk_AA8[0] and every
// sphere. A Magtail's tick prints mode/action, the ring index (+0xBF4)
// beside the dive timer, the ball timer, the hit cooldown, the wiggle
// (+0x468, the slot's +0x18), the colour phase and frames, the death, eye
// HP, killed and respawn bytes, the would-be live gate (MtLiveRefusal),
// the eye sphere's centre beside eyePos, and per segment i its model's
// base translation (morf[i] -> model + 0x24), body sphere i's centre and
// anchor +0x350[i]. Batch D's P0-MT2 (mt2-probe, enemy_batchd_probe.go)
// fits the body model to the same words. enemy-match --dry lists bb
// (bb_type, bb_m2df, bb_knock, bb_live) and mt (mt_mode, mt_appear,
// mt_death, mt_killed, mt_ice, mt_live) after " pid=".

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/diag"
	"github.com/StephenSHorton/ww-multiplayer/mods/enemysync"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

const enemyMatchUsage = "enemy-match --dry [--kinds mo2,bk,cc,ki,am,am2,tn,bb,mt (default every kind, +u on each)]"

// kindOnly is the set with id alone enabled, keyed and unkeyed.
func kindOnly(id enemysync.KindID) enemysync.KindSet {
	var s enemysync.KindSet
	s.On[id], s.Unkeyed[id] = true, true
	return s
}

// identFields is an Enemy's identity as key=value fields.
func identFields(e *enemysync.Enemy) string {
	return fmt.Sprintf("kind=%s proc=0x%04X setID=0x%04X pkey=0x%08X params=0x%08X home_room=%d home=0x%08X,0x%08X,0x%08X",
		e.Kind, e.Ident.Proc, e.Ident.SetID, e.Ident.PKey, e.Params, e.HomeRoom, e.HomePos[0], e.HomePos[1], e.HomePos[2])
}

// verdict is "ok" or the refusal token.
func verdict(e *enemysync.Enemy) string {
	if e.Refused == "" {
		return "ok"
	}
	return e.Refused
}

// liveGateText is the gate bytes the classification read, "-" for each
// one it did not (an actor refused before the read).
func liveGateText(e *enemysync.Enemy) string {
	val := func(ok bool, v uint8) string {
		if !ok {
			return "-"
		}
		return fmt.Sprint(v)
	}
	mode := val(e.LiveModeOK, e.Live.Mode)
	switch e.Kind {
	case enemysync.KindMO2:
		return "mo2_mode=" + mode
	case enemysync.KindBK:
		return "bk_type=" + mode
	case enemysync.KindCC:
		return fmt.Sprintf("cc_behavior=%s cc_color=%s cc_m304=%s", mode, val(e.LiveSpanOK, e.Live.Color), val(e.LiveSpanOK, e.Live.InstaKill))
	case enemysync.KindKI:
		return fmt.Sprintf("ki_action=%s ki_m2dc=%s", mode, val(e.LiveSpanOK, e.Live.Fade))
	case enemysync.KindAM:
		return fmt.Sprintf("am_action=%s am_mode=%s", mode, val(e.LiveSpanOK, e.Live.SubMode))
	case enemysync.KindAM2:
		carried := uint8(0)
		if e.Live.Carried {
			carried = 1
		}
		return fmt.Sprintf("am2_action=%s am2_mode=%s am2_abyss=%s am2_carried=%d", mode, val(e.LiveSpanOK, e.Live.SubMode), val(e.LiveSpanOK, e.Live.Abyss), carried)
	case enemysync.KindTN:
		g := &e.Live
		if !e.LiveSpanOK {
			return fmt.Sprintf("tn_behavior=%s tn_action=- tn_boss=- tn_equip=- tn_m02c1=- tn_m1518=- tn_m14f8=- tn_m140c=- tn_live=-", mode)
		}
		boss, live := 0, enemysync.TnLiveRefusal(*g)
		if g.Boss {
			boss = 1
		}
		if live == "" {
			live = "ok"
		}
		return fmt.Sprintf("tn_behavior=%s tn_action=%d tn_boss=%d tn_equip=%d tn_m02c1=%d tn_m1518=%d tn_m14f8=%d tn_m140c=%d tn_live=%s",
			mode, g.DrAction, boss, g.Equip, g.M02C1, g.M1518, g.M14F8, g.M140C, live)
	case enemysync.KindBB:
		if !e.LiveSpanOK {
			return fmt.Sprintf("bb_type=%s bb_m2df=- bb_knock=- bb_live=-", mode)
		}
		return fmt.Sprintf("bb_type=%s bb_m2df=%d bb_knock=%.2f bb_live=%s", mode, e.Live.BbRider, e.Live.BbKnock, okText(enemysync.BbLiveRefusal(e.Live)))
	case enemysync.KindMT:
		if !e.LiveSpanOK {
			return fmt.Sprintf("mt_mode=%s mt_appear=- mt_death=- mt_killed=- mt_ice=- mt_live=-", mode)
		}
		g := &e.Live
		return fmt.Sprintf("mt_mode=%s mt_appear=%d mt_death=%d mt_killed=%d mt_ice=%d mt_live=%s", mode, g.MtAppear, g.MtDeath, g.MtKilled, g.MtIce,
			okText(enemysync.MtLiveRefusal(*g)))
	}
	return ""
}

// okText is a refusal, "ok" for none.
func okText(why string) string {
	if why == "" {
		return "ok"
	}
	return why
}

// bokoInfo is what a Bokoblin's m1200 names: the walk's actor with that
// pid, re-read for its pid, proc and mNowMode.
type bokoInfo struct {
	PID   uint32
	Found bool
	Proc  uint16
	Mode  uint8
}

func (b bokoInfo) String() string {
	switch {
	case bokoNone(b.PID):
		return "boko none"
	case !b.Found:
		return fmt.Sprintf("boko pid %d (not in the walk)", b.PID)
	}
	return fmt.Sprintf("boko pid %d proc 0x%04X mode %d (%s)", b.PID, b.Proc, b.Mode, bokoModeName(b))
}

func bokoModeName(b bokoInfo) string {
	if b.Proc != addr.ProcBoko {
		return "not a BOKO"
	}
	switch b.Mode {
	case addr.BokoModeEnemyCarry:
		return "enemy-carry"
	case addr.BokoModePlayerCarry:
		return "player-carry"
	}
	return "loose"
}

// bokoNone reports whether a Bokoblin's m1200 names no weapon:
// fpcM_ERROR_PROCESS_ID_e, or 0 before wepon_search first ran (the class
// is zeroed at create).
func bokoNone(pid uint32) bool { return pid == addr.BkBokoNone || pid == 0 }

// readBoko resolves pid through byPID (the walk's actors) and reads the
// actor's pid, proc and, for a BOKO, its mNowMode.
func readBoko(m mem.Reader, byPID map[uint32]uint32, pid uint32) (bokoInfo, error) {
	b := bokoInfo{PID: pid}
	at, ok := byPID[pid]
	if bokoNone(pid) || !ok {
		return b, nil
	}
	h, err := m.Read(at+addr.ActorOffBsPcID, addr.ActorOffProcName+2-addr.ActorOffBsPcID)
	if err != nil {
		return b, err
	}
	be := binary.BigEndian
	if len(h) < 6 || be.Uint32(h) != pid {
		return b, nil // freed or reused since the walk
	}
	b.Found, b.Proc = true, be.Uint16(h[addr.ActorOffProcName-addr.ActorOffBsPcID:])
	if b.Proc == addr.ProcBoko {
		if b.Mode, err = mem.ReadU8(m, at+addr.BokoOffNowMode); err != nil {
			return b, err
		}
	}
	return b, nil
}

// pidIndex maps each walked actor's pid to its address.
func pidIndex(w *actors.Walk) map[uint32]uint32 {
	out := make(map[uint32]uint32, len(w.Actors))
	for i := range w.Actors {
		out[w.Actors[i].PID] = w.Actors[i].Addr
	}
	return out
}

// runEnemyMatch is the enemy-match command.
func runEnemyMatch(cmd string, args []string) {
	pos, flags, values, err := splitValueFlags(args, []string{"kinds"}, "dry")
	if err == nil && len(pos) > 0 {
		err = fmt.Errorf("unexpected argument %q", pos[0])
	}
	if err == nil && !flags["dry"] {
		err = errors.New("only --dry is implemented (Batch 1 writes nothing)")
	}
	set := enemysync.AllKinds()
	if err == nil && values["kinds"] != "" {
		set, err = enemysync.ParseKinds(values["kinds"])
	}
	if err != nil {
		exitUsage(err, enemyMatchUsage)
	}
	err = diag.WithMem(openDolphin, func(m mem.Mem) error { return enemyMatch(os.Stdout, m, set) })
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmd, err)
		os.Exit(1)
	}
}

// enemyMatch prints m's Idents for its current stage and room: a scope
// line, one ident line per actor of an enabled kind (taken or refused), a
// boko line per Bokoblin and per Darknut (its sword), and a summary. On
// the ident and boko lines the per-machine and live fields come from
// " pid=" on (see the file comment).
func enemyMatch(w io.Writer, m mem.Reader, set enemysync.KindSet) error {
	stageName, room, err := enemyScope(m)
	if err != nil {
		return err
	}
	aw, err := actors.ReadWith(m, 0, enemysync.KindLayouts)
	if err != nil {
		return err
	}
	es, err := enemysync.ClassifyWalk(m, &aw, stageName, room, set)
	if err != nil {
		return err
	}
	scope := fmt.Sprintf("scope stage=%s room=%d kinds=%s walk=%s", stageName, room, set, strings.ReplaceAll(walkNote(&aw), " ", "_"))
	fmt.Fprintln(w, scope)
	byPID := pidIndex(&aw)
	taken := 0
	for i := range es {
		e := &es[i]
		if e.Refused == "" {
			taken++
		}
		fmt.Fprintf(w, "ident %s verdict=%s pid=%d %s\n", identFields(e), verdict(e), e.PID, liveGateText(e))
	}
	for i := range es {
		e := &es[i]
		pidOff := uint32(addr.BkOffBokoPID)
		switch e.Kind {
		case enemysync.KindBK:
		case enemysync.KindTN:
			pidOff = addr.TnOffBokoPID
		default:
			continue
		}
		pid, err := mem.ReadU32(m, e.Addr+pidOff)
		if err != nil {
			return err
		}
		b, err := readBoko(m, byPID, pid)
		if err != nil {
			return err
		}
		state, proc, mode := "none", uint16(0), uint8(0)
		switch {
		case bokoNone(pid):
		case !b.Found:
			state = "missing"
		default:
			state, proc, mode = "found", b.Proc, b.Mode
		}
		fmt.Fprintf(w, "boko kind=%s setID=0x%04X pkey=0x%08X pid=%d boko_pid=%d boko_state=%s boko_proc=0x%04X boko_mode=%d\n",
			e.Kind, e.Ident.SetID, e.Ident.PKey, e.PID, pid, state, proc, mode)
	}
	fmt.Fprintf(w, "summary idents=%d taken=%d refused=%d\n", len(es), taken, len(es)-taken)
	return nil
}

// enemyKindWatch is enemy-watch --kind bk|cc|ki|am|am2|tn|bb|mt over m
// for d (0: until ctx ends). An allow kind (ki, am, am2) also prints
// allowExtra's tail each tick, the Darknut (tn) tnExtra's, the Kargaroc
// (bb) bbExtra's and the Magtail (mt) mtExtra's.
func enemyKindWatch(ctx context.Context, w io.Writer, m mem.Reader, d time.Duration, id enemysync.KindID) error {
	tr := enemysync.NewKindTracker()
	k := id.Row()
	if k == nil {
		return fmt.Errorf("no kind %d", id)
	}
	res := tr.Res[id]
	allow := id >= enemysync.KindKI && id <= enemysync.KindAM2
	defer func() {
		st := res.Stats
		fmt.Fprintf(w, "%s table stats: reads %d bck_miss %d miss_reads %d\n", k.Arc, st.Reads, st.BckMiss, st.MissReads)
	}()
	stageName, room, err := enemyScope(m)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "stage %q room %d; kind %s proc 0x%04X arc %q bck 0x%02X..0x%02X morf +0x%X ice +0x%X gate +0x%X\n",
		stageName, room, k.Name, k.Proc, k.Arc, k.BckFirst, k.BckLast, k.MorfOff, k.IceOff, k.GateOff)
	start := time.Now()
	var deadline time.Time
	if d > 0 {
		deadline = start.Add(d)
	}
	var taken []enemysync.Enemy
	var byPID map[uint32]uint32
	var nextWalk time.Time
	var lastList, resWhy string
	resFirst := true
	walk := func(now time.Time) error {
		nextWalk = now.Add(enemyWalkEvery)
		stageName, room, err = enemyScope(m)
		if err != nil {
			return err
		}
		aw, err := actors.ReadWith(m, 0, enemysync.KindLayouts)
		if err != nil {
			return err
		}
		es, err := enemysync.ClassifyWalk(m, &aw, stageName, room, kindOnly(id))
		if err != nil {
			return err
		}
		byPID = pidIndex(&aw)
		taken = taken[:0]
		var pids []uint32
		var b strings.Builder
		for i := range es {
			e := &es[i]
			fmt.Fprintf(&b, "  pid %d %s gate %s %s", e.PID, identFields(e), verdict(e), liveGateText(e))
			switch id {
			case enemysync.KindTN:
				x, err := execText(m, e.Addr)
				if err != nil {
					return err
				}
				b.WriteString(" " + x)
			case enemysync.KindBB:
				x, err := bbExecText(m, e.Addr)
				if err != nil {
					return err
				}
				b.WriteString(" " + x)
			}
			b.WriteString("\n")
			if e.Refused == "" {
				taken = append(taken, *e)
				pids = append(pids, e.PID)
			}
		}
		tr.Keep(pids)
		if err := res.Resolve(m); err != nil {
			return err
		}
		if why := res.Why(); resFirst || why != resWhy {
			resWhy, resFirst = why, false
			if why != "" {
				fmt.Fprintf(w, "%s table: NOT resolved: %s (every bck reads 0xFF)\n", k.Arc, why)
			} else {
				entry, mres, count := res.Entry()
				p := res.Ptrs()
				fmt.Fprintf(w, "%s table: resolved, mObjectInfo[%d] mRes 0x%08X mCount %d (BCK 0x%02X..0x%02X at 0x%08X..0x%08X)\n",
					k.Arc, entry, mres, count, k.BckFirst, k.BckLast, p[0], p[len(p)-1])
			}
		}
		if list := b.String(); list != lastList {
			lastList = list
			fmt.Fprintf(w, "[%6.3fs] walk: %d actors (%s), %d %s in room %d of %q, %d taken:\n%s", now.Sub(start).Seconds(), len(aw.Actors), walkNote(&aw),
				len(es), k.Name, room, stageName, len(taken), list)
		}
		return nil
	}
	if err := walk(start); err != nil {
		return err
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
			if now.After(nextWalk) {
				if err := walk(now); err != nil {
					return err
				}
			}
			ms := now.Sub(start).Milliseconds()
			rewalk := false
			for i := range taken {
				e := &taken[i]
				s, err := enemysync.ReadKindSample(m, e)
				if err == nil {
					err = tr.Derive(m, e.PID, &s, now)
				}
				if err != nil {
					fmt.Fprintf(w, "[%6.3fs] %s pid %d: %v\n", now.Sub(start).Seconds(), e.Ident, e.PID, err)
					if errors.Is(err, actors.ErrChanged) {
						rewalk = true
					}
					continue
				}
				extra := ""
				if id == enemysync.KindTN {
					b, err := readBoko(m, byPID, s.Tn.BokoPID)
					if err != nil {
						return err
					}
					extra = tnExtra(e, &s, b)
				} else if allow {
					extra = allowExtra(id, &s)
				} else if id == enemysync.KindBB {
					if extra, err = bbExtra(m, e, &s); err != nil {
						return err
					}
				} else if id == enemysync.KindMT {
					if extra, err = mtExtra(m, e, &s); err != nil {
						return err
					}
				} else if id == enemysync.KindBK {
					b, err := readBoko(m, byPID, s.BokoPID)
					if err != nil {
						return err
					}
					extra = fmt.Sprintf("type %d %s attach %d demo %d dr_inv %d freeze %d", s.BkType, b, s.BokoAttach, s.DemoGate, s.DRInvincible, s.IceFreeze)
				} else {
					ccName := "-"
					if s.CcBck >= 0 && s.CcBck <= 0xFF {
						ccName = enemysync.BckName(id, uint8(s.CcBck))
					}
					extra = fmt.Sprintf("behavior %d color %d electric %d m320 0x%02X %s at/tg/co 0x%X/0x%X/0x%X freeze %d",
						s.CcBehavior, s.CcColor, s.CcElectric, s.CcBck, ccName, s.CcAtSPrm, s.CcTgSPrm, s.CcCoSPrm, s.IceFreeze)
				}
				fmt.Fprintf(w, "%7d setID 0x%04X pid %d pos %.1f %.1f %.1f ang %d hp %d mode %d action %d tilt %d/%d aux %d/%d/%d/%d ice %d flags 0x%02X [%s] flags2 0x%02X [%s] bck 0x%02X %s attr %d seq %d morf %d frame %.2f rate %.2f blur %d scale %.2f attn 0x%X %s\n",
					ms, s.SetID, e.PID, s.Pos[0], s.Pos[1], s.Pos[2], s.AngleY, s.Health, s.Mode, s.Action, s.Tilt, s.TiltZ, s.Aux[0], s.Aux[1], s.Aux[2], s.Aux[3],
					s.IceTimer, s.Flags, slotFlagNames(s.Flags), s.Flags2, kindFlag2Names(id, s.Flags2), s.Bck, enemysync.BckName(id, s.Bck),
					s.AnmAttr, s.AnmSeq, s.Morf, s.MorfFrame, s.MorfRate, s.BlurShape, s.BlurScale, s.Attn, extra)
			}
			if rewalk {
				if err := walk(now); err != nil {
					return err
				}
			}
		}
	}
}

// slotFlagNames names the SF_* bits set in f.
func slotFlagNames(f uint8) string {
	return bitNames(f, []struct {
		bit  uint8
		name string
	}{
		{enemysync.SlotDying, "DYING"}, {enemysync.SlotInvuln, "INVULN"}, {enemysync.SlotDefence, "DEFENCE"},
		{enemysync.SlotRagdoll, "RAGDOLL"}, {enemysync.SlotHidden, "HIDDEN"}, {enemysync.SlotBareHanded, "BARE"},
	})
}

// kindFlag2Names names the kind's flags2 bits set in f.
func kindFlag2Names(id enemysync.KindID, f uint8) string {
	type nb = struct {
		bit  uint8
		name string
	}
	var names []nb
	switch id {
	case enemysync.KindBK:
		names = []nb{{enemysync.BkF2HasWeapon, "WEAPON"}, {enemysync.BkF2PlaySkip, "PLAY_SKIP"}, {enemysync.BkF2BlurOn, "BLUR"}, {enemysync.BkF2Attn, "ATTN"}}
	case enemysync.KindCC:
		names = []nb{{enemysync.CcF2Tg, "TG"}, {enemysync.CcF2Co, "CO"}, {enemysync.CcF2Squash, "SQUASH"}, {enemysync.CcF2LowEye, "LOW_EYE"},
			{enemysync.CcF2NoShadow, "NO_SHADOW"}, {enemysync.CcF2Attn, "ATTN"}}
	case enemysync.KindKI:
		names = []nb{{enemysync.KiF2Attn, "ATTN"}, {enemysync.KiF2Lunge, "LUNGE"}}
	case enemysync.KindAM:
		names = []nb{{enemysync.AmF2Attn, "ATTN"}, {enemysync.AmF2PlaySkip, "PLAY_SKIP"}}
	case enemysync.KindAM2:
		names = []nb{{enemysync.Am2F2Attn, "ATTN"}, {enemysync.Am2F2WeakTg, "WEAK_TG"}, {enemysync.Am2F2BodyCo, "BODY_CO"}, {enemysync.Am2F2Carried, "CARRIED"}}
	case enemysync.KindTN:
		names = []nb{{enemysync.TnF2HasWeapon, "WEAPON"}, {enemysync.TnF2PlaySkip, "PLAY_SKIP"}, {enemysync.TnF2Attn, "ATTN"}}
	case enemysync.KindBB:
		names = []nb{{enemysync.BbF2Attn, "ATTN"}}
	case enemysync.KindMT:
		names = []nb{{1, "PHASE1"}, {2, "PHASE2"}, {enemysync.MtF2Shield, "SHIELD"}, {enemysync.MtF2Dive, "DIVE"}, {enemysync.MtF2Attn, "ATTN"}}
	}
	names = append(names, nb{enemysync.KindF2Light, "LIGHT"})
	return bitNames(f, names)
}

func bitNames(f uint8, names []struct {
	bit  uint8
	name string
}) string {
	var out []string
	for _, n := range names {
		if f&n.bit != 0 {
			out = append(out, n.name)
		}
	}
	slices.Sort(out)
	return strings.Join(out, " ")
}

// allowExtra is an allow kind's per-tick tail (A0's fields): the morf's last frame
// (mEnd; for the Armos Knight's DEAD, 0x1A, the design's
// EMK_AM_QUIET_FRAMES), current.angle beside shape_angle, the status
// word, the ice freeze timer, the dormant rule, the kind's own fields,
// and every collider.
func allowExtra(id enemysync.KindID, s *enemysync.KindSample) string {
	st := &s.St
	var b strings.Builder
	fmt.Fprintf(&b, "end %d cur %d/%d/%d shape %d/%d/%d dy %d status 0x%08X freeze %d dormant %v", s.MorfEnd,
		st.CurAngle[0], st.CurAngle[1], st.CurAngle[2], st.ShapeAngle[0], st.ShapeAngle[1], st.ShapeAngle[2],
		int16(st.CurAngle[1]-st.ShapeAngle[1]), s.Status, s.IceFreeze, st.Dormant)
	bckIdx := func() string {
		if st.BckIdx < 0 || st.BckIdx > 0xFF {
			return fmt.Sprintf("bckidx %d -", st.BckIdx)
		}
		return fmt.Sprintf("bckidx 0x%02X %s", st.BckIdx, enemysync.BckName(id, uint8(st.BckIdx)))
	}
	switch id {
	case enemysync.KindKI:
		fmt.Fprintf(&b, " beh %d m2D4 %d m2DC %d params %d fire %d m314 %d home 0x%08X,0x%08X,0x%08X",
			st.Behavior, st.DrawGate, st.ShrinkDelete, st.Params, st.DmgType, st.Invuln, st.HomePos[0], st.HomePos[1], st.HomePos[2])
	case enemysync.KindAM:
		fmt.Fprintf(&b, " %s play_skip %d body_hit %d swallow 0x%08X", bckIdx(), st.PlaySkip, st.BodyHit, st.SwallowPID)
	case enemysync.KindAM2:
		fmt.Fprintf(&b, " %s act_scale %.2f weak_hit %d not_home %d abyss %d stun %d", bckIdx(), st.Scale, st.WeakHit, st.NotHome, st.Abyss, st.Stun)
	}
	for _, c := range st.Colliders {
		fmt.Fprintf(&b, " | %s+0x%X at/tg/co 0x%X/0x%X/0x%X c %.1f %.1f %.1f r %.1f", c.Name, c.Off, c.AtSPrm, c.TgSPrm, c.CoSPrm, c.Center[0], c.Center[1], c.Center[2], c.R)
		if c.Cyl {
			fmt.Fprintf(&b, " h %.1f", c.H)
		}
	}
	return b.String()
}

// execText is an actor's execute function, *(*(actor+0xEC)+8)
// (f_op_actor.h:274, f_pc_method.h:13), and the TN design's P0-c
// addresses below it: daTn_Execute-0x1040 (part_move) and -0x224C
// (yoroi_break, rels/d_a_tn/symbols.txt:68,71,73). "exec -" when the
// method table is not in MEM1.
func execText(m mem.Reader, actor uint32) (string, error) {
	mtd, err := mem.ReadU32(m, actor+addr.ActorOffSubMtd)
	if err != nil {
		return "", err
	}
	if !addr.InMEM1(mtd) {
		return fmt.Sprintf("exec - (sub_method 0x%08X)", mtd), nil
	}
	exec, err := mem.ReadU32(m, mtd+addr.MethodOffExecute)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("exec 0x%08X part_move 0x%08X yoroi_break 0x%08X", exec, exec-addr.TnExecToPartMove, exec-addr.TnExecToYoroiBreak), nil
}

// pieceNames names a Darknut's mRemainingEquipmentPieces bits.
func pieceNames(p int8) string {
	return bitNames(uint8(p), []struct {
		bit  uint8
		name string
	}{{addr.TnPieceArmor, "ARMOR"}, {addr.TnPieceHelmet, "HELMET"}, {addr.TnPieceShield, "SHIELD"}, {addr.TnPieceUnknown, "B3"}})
}

// tnExtra is a Darknut's per-tick tail (P0-b's fields, design test_plan):
// the morf's last frame, current.angle beside shape_angle, the status
// word, the ice freeze timer, the class fields, the would-be live gate
// from this tick's bytes, the sword its mBokoPcId names, and every
// collider.
func tnExtra(e *enemysync.Enemy, s *enemysync.KindSample, sword bokoInfo) string {
	st, tn := &s.St, &s.Tn
	var b strings.Builder
	fmt.Fprintf(&b, "end %d cur %d/%d/%d shape %d/%d/%d status 0x%08X freeze %d dr_inv %d", s.MorfEnd,
		st.CurAngle[0], st.CurAngle[1], st.CurAngle[2], st.ShapeAngle[0], st.ShapeAngle[1], st.ShapeAngle[2], s.Status, s.IceFreeze, s.DRInvincible)
	fmt.Fprintf(&b, " beh %d equip %d color %d pieces 0x%02X [%s] parts %d/%d %d/%d %d/%d death_sw %d m02C1 %d m03EE %d m03F0 %d m0C34 %d m140C %d m1444 %d m14F8 %d m1518 %d m1402 %d",
		tn.Behavior, tn.Equip, tn.ArmorColor, uint8(tn.Pieces), pieceNames(tn.Pieces), tn.Parts[0][0], tn.Parts[0][1], tn.Parts[1][0], tn.Parts[1][1], tn.Parts[2][0], tn.Parts[2][1],
		tn.DeathSwitch, tn.M02C1, tn.PlaySkip, tn.Invuln, tn.Weapon, tn.M140C, tn.M1444, tn.M14F8, tn.M1518, tn.M1402)
	g := e.Live
	g.Mode, g.DrAction, g.Boss, g.Equip = tn.Behavior, s.Action, s.Status&addr.ActorStatusBoss != 0, tn.Equip
	g.M02C1, g.M1518, g.M14F8, g.M140C = tn.M02C1, uint8(tn.M1518), uint8(tn.M14F8), uint8(tn.M140C)
	live := enemysync.TnLiveRefusal(g)
	if live == "" {
		live = "ok"
	}
	fmt.Fprintf(&b, " live %s morfs 0x%08X/0x%08X def %.1f %.1f %.1f r %.1f sword %s", live, tn.ShieldMorf, tn.ArmorMorf,
		tn.DefCenter[0], tn.DefCenter[1], tn.DefCenter[2], tn.DefR, sword)
	for _, c := range st.Colliders {
		fmt.Fprintf(&b, " | %s+0x%X at/tg/co 0x%X/0x%X/0x%X c %.1f %.1f %.1f r %.1f", c.Name, c.Off, c.AtSPrm, c.TgSPrm, c.CoSPrm, c.Center[0], c.Center[1], c.Center[2], c.R)
		if c.Cyl {
			fmt.Fprintf(&b, " h %.1f", c.H)
		}
	}
	return b.String()
}

// runEnemyKindWatch runs enemy-watch --kind bk|cc|ki|am|am2|tn|bb|mt.
func runEnemyKindWatch(cmd string, secs int, id enemysync.KindID) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err := diag.WithMem(openDolphin, func(m mem.Mem) error {
		return enemyKindWatch(ctx, os.Stdout, m, time.Duration(secs)*time.Second, id)
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmd, err)
		os.Exit(1)
	}
}
