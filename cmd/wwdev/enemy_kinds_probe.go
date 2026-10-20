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
// enemy-match --dry lists one Dolphin's Idents for its current stage and
// room as key=value lines, sorted by Ident. Each line carries what both
// machines must agree on (the placement-derived identity and the
// verdict) before " pid=", and everything per-machine or live from
// " pid=" on: the pids, the live gate bytes (bk_type, cc_*, mo2_mode;
// "-" when the classification did not read one) and a Bokoblin's BOKO
// state, which the game changes as it plays. Two Dolphins in the same
// room therefore diff clean after `sed 's/ pid=.*//'` on both outputs.

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

const enemyMatchUsage = "enemy-match --dry [--kinds mo2,bk,cc (default every kind, +u on each)]"

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
	}
	return ""
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
// line, one ident line per actor of an enabled kind (taken or refused),
// a boko line per Bokoblin, and a summary. On the ident and boko lines
// the per-machine and live fields come from " pid=" on (see the file
// comment).
func enemyMatch(w io.Writer, m mem.Reader, set enemysync.KindSet) error {
	stageName, room, err := enemyScope(m)
	if err != nil {
		return err
	}
	aw, err := actors.ReadWith(m, 0, enemysync.KindLayouts)
	if err != nil {
		return err
	}
	es, err := enemysync.ClassifyWalk(m, &aw, room, set)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "scope stage=%s room=%d kinds=%s walk=%s\n", stageName, room, set, strings.ReplaceAll(walkNote(&aw), " ", "_"))
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
		if e.Kind != enemysync.KindBK {
			continue
		}
		pid, err := mem.ReadU32(m, e.Addr+addr.BkOffBokoPID)
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
		fmt.Fprintf(w, "boko kind=bk setID=0x%04X pkey=0x%08X pid=%d boko_pid=%d boko_state=%s boko_proc=0x%04X boko_mode=%d\n",
			e.Ident.SetID, e.Ident.PKey, e.PID, pid, state, proc, mode)
	}
	fmt.Fprintf(w, "summary idents=%d taken=%d refused=%d\n", len(es), taken, len(es)-taken)
	return nil
}

// enemyKindWatch is enemy-watch --kind bk|cc over m for d (0: until ctx
// ends).
func enemyKindWatch(ctx context.Context, w io.Writer, m mem.Reader, d time.Duration, id enemysync.KindID) error {
	k := id.Row()
	tr := enemysync.NewKindTracker()
	res := tr.Res[id]
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
		es, err := enemysync.ClassifyWalk(m, &aw, room, kindOnly(id))
		if err != nil {
			return err
		}
		byPID = pidIndex(&aw)
		taken = taken[:0]
		var pids []uint32
		var b strings.Builder
		for i := range es {
			e := &es[i]
			fmt.Fprintf(&b, "  pid %d %s gate %s %s\n", e.PID, identFields(e), verdict(e), liveGateText(e))
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
				if id == enemysync.KindBK {
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

// runEnemyKindWatch runs enemy-watch --kind bk|cc.
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
