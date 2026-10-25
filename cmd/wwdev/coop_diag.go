package main

// Co-op M0 diagnostics (#23; docs/08-coop-design.md §8.2-8.3 and §11
// items 3-5). Each one points the internal/coop readers at the Dolphin
// that WW_DOLPHIN_INDEX / WW_DOLPHIN_PID select, so the owner can check on
// a live game what the design infers from the decomp before any milestone
// writes game data.
//
// Everything here is read-only except two opt-in debug writers, which
// refuse to run without --yes: `region-probe arm` (canary words over the
// two gaps beside the mod) and `stage-hop` (a raw mNextStage request).
// Like the other diagnostics, none of them is listed in printHelp.
//
// Citations follow wwapi/addr: file:line is the zeldaret/tww
// decomp at 4d78c994, sym:N is line N of its config/GZLE01/symbols.txt.

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/bits"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/combat"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/diag"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/heap"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// coopDiagUsage is each co-op diagnostic's usage line. The command registry
// (commands.go) sends every name here to runCoopDiag.
var coopDiagUsage = map[string]string{
	"coop-probe":        "coop-probe",
	"coop-watch":        "coop-watch [secs=60, 0 = until Ctrl+C] [--all]",
	"actors":            "actors [max=1024]",
	"enemy-kill":        "enemy-kill <pid> [watch-secs=5] [--unverified] --yes",
	"peek-demo":         "peek-demo [watch-secs=0]",
	"zone-dump":         "zone-dump",
	"frame-phase-probe": "frame-phase-probe [secs=5]",
	"heap-free":         "heap-free",
	"blob-verify-live":  "blob-verify-live",
	"region-probe":      "region-probe arm --yes [--force] | region-probe check",
	"stage-hop":         "stage-hop <stage> [point=0] [room=first with the point] [layer=-1] [--no-check] --yes",
}

// coopDiagFlags is the --flags each diagnostic accepts.
var coopDiagFlags = map[string][]string{
	"coop-watch":   {"all"},
	"region-probe": {"yes", "force"},
	"stage-hop":    {"yes", "no-check"},
	"enemy-kill":   {"yes", "unverified"},
}

// runCoopDiag runs co-op diagnostic cmd with the arguments after it.
func runCoopDiag(cmd string, args []string) {
	usage := coopDiagUsage[cmd]
	pos, flags, err := diag.SplitFlags(args, coopDiagFlags[cmd]...)
	if err != nil {
		exitUsage(err, usage)
	}
	if !strings.ContainsAny(usage, "[<") && len(pos) > 0 {
		exitUsage(fmt.Errorf("%s takes no arguments", cmd), usage)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	switch cmd {
	case "coop-probe":
		err = diag.WithMem(openDolphin, func(m mem.Mem) error { return coopProbe(os.Stdout, m, diag.Tick) })
	case "coop-watch":
		secs, perr := diagIntArg(pos, 0, 60, 0, 86400, "secs")
		if perr != nil {
			exitUsage(perr, usage)
		}
		err = diag.WithMem(openDolphin, func(m mem.Mem) error {
			return coopWatch(ctx, os.Stdout, m, time.Duration(secs)*time.Second, flags["all"])
		})
	case "actors":
		n, perr := diagIntArg(pos, 0, actors.MaxNodes, 1, 1<<16, "max")
		if perr != nil {
			exitUsage(perr, usage)
		}
		err = diag.WithMem(openDolphin, func(m mem.Mem) error {
			w, err := combat.Read(m, n)
			if err != nil {
				return err
			}
			printActors(os.Stdout, w)
			return nil
		})
	case "enemy-kill":
		if len(pos) == 0 {
			exitUsage(errors.New("say the process ID (the actors diagnostic's pid)"), usage)
		}
		pid, perr := strconv.ParseUint(pos[0], 0, 32)
		if perr != nil || pid == 0 {
			exitUsage(fmt.Errorf("process ID %q", pos[0]), usage)
		}
		secs, perr := diagIntArg(pos, 1, 5, 0, 600, "watch-secs")
		if perr != nil {
			exitUsage(perr, usage)
		}
		fmt.Println("enemy-kill writes one byte, 1, into the enemy's light-shrink timer (and clears its weapon's CARRY): the game then runs its own light-arrow death on it, with its item drop and death switch (docs/08 §7.2).")
		if !flags["yes"] {
			fmt.Println("Not written: re-run with --yes.")
			os.Exit(1)
		}
		err = diag.WithMem(openDolphin, func(m mem.Mem) error {
			return enemyKill(ctx, os.Stdout, m, uint32(pid), flags["unverified"], time.Duration(secs)*time.Second, diag.Tick)
		})
	case "peek-demo":
		secs, perr := diagIntArg(pos, 0, 0, 0, 3600, "watch-secs")
		if perr != nil {
			exitUsage(perr, usage)
		}
		err = diag.WithMem(openDolphin, func(m mem.Mem) error { return peekDemo(ctx, os.Stdout, m, time.Duration(secs)*time.Second, diag.Tick) })
	case "zone-dump":
		err = diag.WithMem(openDolphin, func(m mem.Mem) error {
			s, err := state.Read(m, state.Options{})
			if err != nil {
				return err
			}
			zoneDump(os.Stdout, s)
			return nil
		})
	case "frame-phase-probe":
		secs, perr := diagIntArg(pos, 0, 5, 1, 600, "secs")
		if perr != nil {
			exitUsage(perr, usage)
		}
		err = diag.WithMem(openDolphin, func(m mem.Mem) error {
			return framePhaseProbe(ctx, os.Stdout, m, time.Duration(secs)*time.Second)
		})
	case "heap-free":
		err = diag.WithMem(openDolphin, func(m mem.Mem) error { return heapFree(os.Stdout, m) })
	case "blob-verify-live":
		err = diag.WithMem(openDolphin, func(m mem.Mem) error { return blobVerifyLive(os.Stdout, m) })
	case "region-probe":
		if len(pos) != 1 || (pos[0] != "arm" && pos[0] != "check") {
			exitUsage(errors.New("say arm or check"), usage)
		}
		if pos[0] == "check" {
			err = diag.WithMem(openDolphin, func(m mem.Mem) error { return regionCheck(os.Stdout, m) })
			break
		}
		fmt.Print(regionArmWarning())
		if !flags["yes"] {
			fmt.Println("Not armed: re-run with --yes to write the canaries.")
			os.Exit(1)
		}
		err = diag.WithMem(openDolphin, func(m mem.Mem) error { return regionArm(os.Stdout, m, diag.Tick, flags["force"]) })
	case "stage-hop":
		h, perr := parseStageHop(pos)
		if perr != nil {
			exitUsage(perr, usage)
		}
		// Checked before --yes is looked at, so a hop the disc says
		// would halt the game is refused either way.
		if flags["no-check"] {
			fmt.Println("Start not checked (--no-check): a start the stage lacks hangs the game.")
			defaultRoomUnchecked(os.Stdout, &h, len(pos) > 2)
		} else if cerr := verifyStageHop(os.Stdout, launch.FromEnv().ISO, &h, len(pos) > 2); cerr != nil {
			fmt.Printf("ERROR: %v\n", cerr)
			os.Exit(1)
		}
		fmt.Print(h.warning())
		if !flags["yes"] {
			fmt.Println("Not sent: re-run with --yes to write mNextStage.")
			os.Exit(1)
		}
		err = diag.WithMem(openDolphin, func(m mem.Mem) error { return stageHopRun(os.Stdout, m, h, diag.Tick) })
	default:
		err = fmt.Errorf("unknown co-op diagnostic %q", cmd)
	}
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
}

// diagIntArg parses positional argument i as an integer in lo..hi, or
// returns def when it is absent.
func diagIntArg(pos []string, i, def, lo, hi int, name string) (int, error) {
	if i >= len(pos) {
		return def, nil
	}
	if len(pos) > i+1 {
		return 0, fmt.Errorf("too many arguments: %q", pos[i+1:])
	}
	v, err := strconv.Atoi(pos[i])
	if err != nil || v < lo || v > hi {
		return 0, fmt.Errorf("%s must be a number from %d to %d, not %q", name, lo, hi, pos[i])
	}
	return v, nil
}

func be32(b []byte) uint32 { return binary.BigEndian.Uint32(b) }

// ---------------------------------------------------------------------
// coop-probe

// coopProbe is the one-shot report: the stage, save table, room and file,
// life, the HUD's pending counters, whether mReserve (the binding tag's
// home, §5.2) is clear, and the three gates with the reason any is closed.
func coopProbe(w io.Writer, m mem.Mem, tick func()) error {
	s, st, hb0, err := diag.GateSnapshots(m, tick)
	if err != nil {
		return err
	}
	line := func(label, format string, a ...any) {
		fmt.Fprintf(w, "%-11s %s\n", label, fmt.Sprintf(format, a...))
	}
	line("stage", "%q point %d room %d layer %d (mCurStage 0x%08X)",
		s.StageName, s.StagePoint, s.StageRoom, s.StageLayer, addr.CurStage)
	line("room", "%d (mStayNo, the room Link is in)", s.StayNo)

	// The STAG chunk names the stage's table too (d_stage.h:1027-1029);
	// getSave loaded mDan.mStageNo from it, so the two should agree.
	stag := "mpStagInfo is null"
	if s.StagInfo != 0 {
		if prop, err := mem.ReadU8(m, s.StagInfo+addr.StagOffProp); err != nil {
			stag = fmt.Sprintf("mpStagInfo 0x%08X unreadable: %v", s.StagInfo, err)
		} else {
			t := addr.StagSaveTable(prop)
			stag = fmt.Sprintf("STAG says table %d, key HUD %v", t, prop&addr.StagPropKeyDisp != 0)
			if t != int(s.Table) {
				stag += " (DIFFERS)"
			}
		}
	}
	line("save table", "%d %s (mDan.mStageNo); %s", s.Table, diag.TableName(int(s.Table)), stag)
	line("file", "slot %d (mDataNum)", s.DataNum)

	sv := s.Save[:]
	maxLife := binary.BigEndian.Uint16(sv[addr.OffMaxLife:])
	life := binary.BigEndian.Uint16(sv[addr.OffLife:])
	line("life", "%d of %d quarter hearts (%.2f of %.2f hearts)", life, maxLife, float64(life)/4, float64(maxLife)/4)

	pl := func(a uint32) []byte { return s.Player[a-state.PlayerRegion.Start:] }
	lifeCount := math.Float32frombits(be32(pl(addr.ItemLifeCount)))
	rupeeCount := int32(be32(pl(addr.ItemRupeeCount)))
	line("pending", "key %d, max life %d, max magic %d (life %g, rupees %d)",
		s.KeyNumCount, s.MaxLifeCount, s.MaxMagicCount, lifeCount, rupeeCount)

	res := sv[addr.OffReserve : addr.OffReserve+addr.ReserveSize]
	if i := slices.IndexFunc(res, func(b byte) bool { return b != 0 }); i < 0 {
		line("mReserve", "all 0x%X bytes zero (0x%08X): no binding tag", addr.ReserveSize, addr.Reserve)
	} else {
		line("mReserve", "NOT all zero: first non-zero byte at +0x%02X; tag bytes % X", i, res[:addr.ReserveTagBytes])
	}

	line("next stage", "mEnable %d", s.NextEnable)
	line("idle input", "event mode %d, order count %d, pause %d, minigame %d, refight %d",
		s.EvtMode, s.EvtOrderCount, s.MenuPause, s.MiniGameType, s.RefightStageNo)
	line("Link", "mpPlayerPtr[0] 0x%08X", s.PlayerPtr)
	line("heartbeat", "spawn_trigger %d, +%d over %d ticks", s.Heartbeat, s.Heartbeat-hb0, gate.StableTicks)
	line("torn", "%d sensitive bytes differed between the two save reads", s.Torn)

	gateLine := func(name string, open bool, why string) {
		if open {
			line(name, "open")
		} else {
			line(name, "closed: %s", why)
		}
	}
	gateLine("STABLE", st.Stable, st.StableWhy)
	gateLine("IDLE", st.Idle, st.IdleWhy)
	gateLine("HEARTBEAT", st.Heartbeat, st.HeartbeatWhy)
	return nil
}

// ---------------------------------------------------------------------
// coop-watch

// coopWatch polls a snapshot every tick for d (0: until ctx ends) and
// prints each labelled edge in the save region (0x803C4C08, 0x12A0
// bytes), stamped with the time since it started.
func coopWatch(ctx context.Context, w io.Writer, m mem.Mem, d time.Duration, all bool) error {
	cw := &coopWatcher{all: all}
	start := time.Now()
	t := time.NewTicker(diag.TickInterval)
	defer t.Stop()
	for {
		s, err := state.Read(m, state.Options{})
		if err != nil {
			return err
		}
		at := time.Since(start)
		for _, l := range cw.step(s) {
			fmt.Fprintf(w, "%8.1fs  %s\n", at.Seconds(), l)
		}
		if d > 0 && at >= d {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// coopWatcher turns successive snapshots into coop-watch's lines.
type coopWatcher struct {
	all  bool // also show status_b, whose time of day changes every frame
	prev *state.Snapshot
}

// step returns the lines for snapshot s: a line when the stage, room,
// table or file changed, then one per edge since the last snapshot. On a
// table change getSave and initDan reload the live memBit and mDan in
// bulk (d_stage.cpp:1675-1677), which is not the game setting anything,
// so those edges are counted instead of listed.
func (cw *coopWatcher) step(s *state.Snapshot) []string {
	prev := cw.prev
	cw.prev = s
	if prev == nil {
		return []string{fmt.Sprintf("watching dSv_info_c 0x%08X (0x%X bytes) at 10 Hz: %s",
			addr.SaveInfo, addr.SaveInfoSize, diag.Where(s))}
	}
	var out []string
	if diag.Where(prev) != diag.Where(s) {
		out = append(out, "== now "+diag.Where(s))
	}
	reload := prev.Table != s.Table
	hidden := 0
	for _, e := range diag.DiffSave(prev.Save[:], s.Save[:], int(s.Table)) {
		if !cw.all && e.Off >= addr.OffStatusB && e.Off < addr.OffReturnPlace {
			continue
		}
		if reload && e.Off >= addr.InfoOffMemory && e.Off < addr.InfoOffZone {
			hidden++
			continue
		}
		out = append(out, e.String())
	}
	if hidden > 0 {
		out = append(out, fmt.Sprintf("   (table %d -> %d: getSave and initDan reloaded the live memBit and mDan; %d edges not shown)",
			prev.Table, s.Table, hidden))
	}
	return out
}

// ---------------------------------------------------------------------
// actors

// actorGroups names fopAc_Group_e (f_op_actor.h:54-60).
var actorGroups = [...]string{"actor", "player", "enemy", "env", "npc"}

// printActors prints a walk of g_fopAcTg_Queue (combat.Read): each actor's
// process, proc, setID, group and rooms, and for an enemy its health,
// params, mInitState and BOSS, CARRY and event command; for a K1 class
// its enemyice (self-pointer, mode, timer) and whether M6 may light-shrink
// it now (combat.Eligible), so the owner can check a class's offsets live
// (docs/08 §8.2 K1).
func printActors(w io.Writer, aw actors.Walk) {
	fmt.Fprintf(w, "g_fopAcTg_Queue 0x%08X: head 0x%08X, tail 0x%08X, mSize %d; g_fopAc_type 0x%08X; next process ID %d\n",
		addr.ActorQueue, aw.Head, aw.Tail, aw.Size, aw.Type, aw.ProcCounter)
	for i := range aw.Actors {
		a := &aw.Actors[i]
		set := "-"
		if a.SetID != addr.ActorSetIDNone {
			set = fmt.Sprintf("%d", a.SetID)
		}
		group := fmt.Sprintf("group%d", a.Group)
		if int(a.Group) < len(actorGroups) {
			group = actorGroups[a.Group]
		}
		extra := ""
		if a.InitState != 2 {
			extra += fmt.Sprintf("  init %d", a.InitState)
		}
		if a.Group == 2 || a.MaxHealth != 0 { // fopAc_ENEMY_e, f_op_actor.h:57
			extra += fmt.Sprintf("  hp %d/%d  params 0x%08X", a.Health, a.MaxHealth, a.Params)
		}
		if a.Boss() {
			extra += "  BOSS"
		}
		if a.Carried() {
			extra += "  CARRY"
		}
		if a.EvtCmd != 0 {
			extra += fmt.Sprintf("  mCmd %d", a.EvtCmd)
		}
		if _, ok := combat.K1(a.Proc); ok {
			extra += "  " + iceLine(a)
		}
		fmt.Fprintf(w, "%4d  0x%08X  pid %-6d proc 0x%04X %-6s set %-5s %-6s room %3d  (%9.1f %9.1f %9.1f)%s\n",
			i, a.Addr, a.PID, uint16(a.Proc), combat.ProcName(a.Proc), set, group, a.HomeRoom,
			a.Pos[0], a.Pos[1], a.Pos[2], extra)
	}
	fmt.Fprintf(w, "%d actors", len(aw.Actors))
	if aw.Stop != "" {
		fmt.Fprintf(w, "; walk stopped: %s", aw.Stop)
	}
	fmt.Fprintln(w)
	for _, n := range aw.Notes {
		fmt.Fprintf(w, "note: %s\n", n)
	}
}

// iceLine is a K1 actor's enemyice and verdict.
func iceLine(a *actors.Actor) string {
	if !a.Ice.Read {
		return "enemyice unreadable"
	}
	s := fmt.Sprintf("mode %d, shrink timer %d", a.Ice.Mode, a.Ice.Timer)
	if a.Ice.SelfOK {
		s = "enemyice ok, " + s
	} else {
		s = fmt.Sprintf("enemyice.mpActor 0x%08X is not the actor; enemyice %s", a.Ice.Self, s)
	}
	if a.ModeOK {
		s += fmt.Sprintf(", class mode %d", a.Mode)
	}
	if a.Weapon != 0 {
		s += fmt.Sprintf(", weapon pid %d", a.Weapon)
	}
	if a.HoldOK {
		s += fmt.Sprintf(" (holding record %d)", a.Hold) // m0B30, m1DD0: 0 when the weapon field is stale
	}
	if why := combat.Eligible(a, true, nil); why != "" {
		return s + "; K1 held: " + why
	}
	if c, _ := combat.K1(a.Proc); !c.Verified {
		return s + "; K1 ok with --unverified"
	}
	return s + "; K1 ok"
}

// ---------------------------------------------------------------------
// enemy-kill and peek-demo (docs/08 §8.2 K1)

// enemyKill light-shrinks the live enemy with process ID pid, through the
// same gates, re-read and WriteGuard as the M6 receiver (combat.Eligible,
// combat.Revalidate, FrameWriter.ApplyActors, WriteGuard.checkActor), for
// the owner's live check of a class's enemyice offset: one byte, 1, at its
// timer, then its weapon's CARRY cleared. Unlike the receiver it takes any
// actor of a K1 class, keyed or not, ignores Link's demo gate, and with
// unverified takes a class not yet verified. It watches the actor for
// watch afterwards and says what became of it. tick runs between the gate
// snapshots and the watch's reads.
func enemyKill(ctx context.Context, w io.Writer, m mem.Mem, pid uint32, unverified bool, watch time.Duration, tick func()) error {
	s, st, _, err := diag.GateSnapshots(m, tick)
	if err != nil {
		return err
	}
	if !st.CanWriteIdle() {
		return fmt.Errorf("not now: %s (stand in game, out of menus and cutscenes)", closedWhyDiag(st))
	}
	aw, err := combat.Read(m, 0)
	if err != nil {
		return err
	}
	if aw.Stop != "" {
		return fmt.Errorf("the actor walk stopped: %s", aw.Stop)
	}
	a, ok := aw.ByPID(pid)
	if !ok {
		return fmt.Errorf("no actor has process ID %d (the actors diagnostic lists them)", pid)
	}
	fmt.Fprintf(w, "target: %s; %s\n", a, iceLine(a))
	if why := combat.Eligible(a, unverified, nil); why != "" {
		return fmt.Errorf("not written: %s", why)
	}
	if a.Keyed() {
		if ok, err := actors.ZoneHolds(m, int(a.HomeRoom)); err != nil || !ok {
			return fmt.Errorf("not written: room %d has no zone, and the death would halt the game in onActor (%v)", a.HomeRoom, err)
		}
	}
	wp, why := combat.WeaponOf(&aw, a, unverified)
	if why != "" {
		fmt.Fprintf(w, "its weapon is left: %s\n", why)
	}
	wg := replica.NewWriteGuard(replica.GuardConfig{Targets: replica.TargetK1})
	fw, err := replica.NewFrameWriter(replica.WriterConfig{Mem: m, Guard: wg, Logf: func(f string, args ...any) { fmt.Fprintf(w, f+"\n", args...) }})
	if err != nil {
		return err
	}
	// ARMED is the receiver's world binding; a diagnostic writes on the
	// owner's word (--yes) instead.
	fw.Observe(s, st, true)
	wg.SetCombat(replica.CombatTick{Walk: &aw, Allow: func(c *actors.Actor) bool { return c.PID == pid }, Unverified: unverified, Demo: true})
	t := replica.ActorTarget{Carrier: *a}
	if wp != nil {
		c := *wp
		t.Weapon = &c
	}
	res, err := fw.ApplyActors(ctx, []replica.ActorTarget{t}, false, unverified)
	if err != nil {
		return err
	}
	if len(res.Held) > 0 {
		return fmt.Errorf("held after the beat: %s", res.Held[0].Why)
	}
	if len(res.Done) != 1 {
		return errors.New("nothing was written")
	}
	d := res.Done[0]
	fmt.Fprintf(w, "wrote 1 at 0x%08X after heartbeat %d", d.Carrier.TimerAddr(), res.Beat)
	if d.Released {
		fmt.Fprintf(w, "; its weapon's CARRY cleared")
	}
	fmt.Fprintln(w)
	deadline := time.Now().Add(watch)
	last := int8(1)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		tick()
		now, err := combat.Read(m, 0)
		if err != nil {
			return err
		}
		b, ok := now.ByPID(pid)
		if !ok {
			fmt.Fprintf(w, "the actor is gone (the shrink ran its course)")
			if d.Carrier.Keyed() {
				if s, err := state.Read(m, state.Options{}); err == nil {
					if z, ok := s.ZoneFor(int(d.Carrier.HomeRoom)); ok {
						fmt.Fprintf(w, "; room %d's zone slot %d records setID %d dead: %v", d.Carrier.HomeRoom, z, d.Carrier.SetID,
							zoneActorSet(s, z, int(d.Carrier.SetID)))
					}
				}
			}
			fmt.Fprintln(w)
			return nil
		}
		if b.Ice.Timer != last {
			fmt.Fprintf(w, "  timer %d\n", b.Ice.Timer)
			last = b.Ice.Timer
		}
	}
	fmt.Fprintf(w, "the actor is still there after %v (timer %d): its class may not run enemy_ice there, or the game is paused\n", watch, last)
	return nil
}

// zoneActorSet reports whether s's zone slot z records setID id dead.
func zoneActorSet(s *state.Snapshot, z, id int) bool {
	bit, err := addr.ZoneActorBit(id)
	if err != nil {
		return false
	}
	return s.Save[addr.InfoOffZone+z*addr.ZoneSize+bit.Off]&bit.Mask != 0
}

// closedWhyDiag is why st's gates are closed.
func closedWhyDiag(st gate.Status) string {
	if why := st.Why(); why != "" {
		return why
	}
	return "a gate is closed"
}

// peekDemo prints Link's mDemo.mDemoType (daPy_py_c +0x304,
// d_a_player.h:499), the offset M6's demo gate reads, which the owner's
// live check K1 confirms: 0 in normal play, nonzero during a
// Floormaster's or ReDead's grab and the Forsaken Fortress Moblin's
// holdup. With watch it reads it until then and prints each change.
func peekDemo(ctx context.Context, w io.Writer, m mem.Mem, watch time.Duration, tick func()) error {
	read := func() (uint32, uint16, error) {
		p, err := mem.ReadU32(m, addr.PlayerPtr)
		if err != nil || p == 0 {
			return p, 0, err
		}
		b, err := m.Read(p+addr.PlayerOffDemoType, 2)
		if err != nil {
			return p, 0, err
		}
		return p, binary.BigEndian.Uint16(b), nil
	}
	p, t, err := read()
	if err != nil {
		return err
	}
	if p == 0 {
		return errors.New("no Link (mpPlayerPtr[0] is 0)")
	}
	fmt.Fprintf(w, "Link 0x%08X: mDemo.mDemoType (+0x%X) = %d\n", p, addr.PlayerOffDemoType, t)
	deadline := time.Now().Add(watch)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		tick()
		np, nt, err := read()
		if err != nil {
			return err
		}
		if np != p || nt != t {
			fmt.Fprintf(w, "Link 0x%08X: mDemo.mDemoType = %d\n", np, nt)
			p, t = np, nt
		}
	}
	return nil
}

// ---------------------------------------------------------------------
// zone-dump

// zoneDump prints the 32 mZone slots and the rooms' mZoneNo map from one
// snapshot (§3.1 zone writes). A slot removeZone freed keeps its bits
// (only mRoomNo is reset, d_save.h:793), so a free slot with bits is shown.
func zoneDump(w io.Writer, s *state.Snapshot) {
	fmt.Fprintf(w, "%s\n", diag.Where(s))
	var empty []int
	for z := range addr.ZoneCount {
		base := addr.InfoOffZone + z*addr.ZoneSize
		zb := s.Save[base : base+addr.ZoneSize]
		room := int8(zb[addr.ZoneOffRoomNo])
		sw, rsw := zoneSwitches(zb)
		item := binary.BigEndian.Uint16(zb[addr.ZoneOffItem:])
		dead := zoneActors(zb)
		if room == -1 && len(sw)+len(rsw)+len(dead) == 0 && item == 0 {
			empty = append(empty, z)
			continue
		}
		state := fmt.Sprintf("room %2d", room)
		if room == -1 {
			state = "free   "
		}
		fmt.Fprintf(w, "  zone %2d  %s  switches %s  room switches %s  items 0x%04X  dead actors %s\n",
			z, state, fmtNums(sw, true), fmtNums(rsw, true), item, fmtNums(dead, false))
	}
	if len(empty) > 0 {
		fmt.Fprintf(w, "  free and empty: zones %s\n", fmtNums(empty, false))
	}

	fmt.Fprintln(w, "mStatus[room].mZoneNo:")
	none := 0
	for room := range addr.RoomCount {
		z := s.ZoneNo[room]
		if z == -1 {
			none++
			continue
		}
		note := ""
		if _, ok := s.ZoneFor(room); !ok {
			if z < 0 || int(z) >= addr.ZoneCount {
				note = "  INVALID: no such zone"
			} else {
				held := int8(s.Save[addr.InfoOffZone+int(z)*addr.ZoneSize+addr.ZoneOffRoomNo])
				note = fmt.Sprintf("  INVALID: zone %d holds room %d", z, held)
			}
		}
		if int(s.StayNo) == room {
			note += "  <- Link"
		}
		fmt.Fprintf(w, "  room %2d -> zone %2d (count %d)%s\n", room, z, s.ZoneCount[room], note)
	}
	fmt.Fprintf(w, "  %d rooms have no zone\n", none)
}

// zoneSwitches lists the set zone switches (0xC0-0xDF) and room switches
// (0xE0-0xEF) of a dSv_zone_c.
func zoneSwitches(zb []byte) (zone, room []int) {
	for no := addr.SwitchZoneFirst; no < addr.SwitchEnd; no++ {
		st, b, _ := addr.SwitchBit(no)
		if zb[b.Off]&b.Mask == 0 {
			continue
		}
		if st == addr.SwitchRoom {
			room = append(room, no)
		} else {
			zone = append(zone, no)
		}
	}
	return zone, room
}

// zoneActors lists the dead-actor setIDs of a dSv_zone_c.
func zoneActors(zb []byte) []int {
	var out []int
	for id := range addr.ZoneActorCount {
		b, _ := addr.ZoneActorBit(id)
		if zb[b.Off]&b.Mask != 0 {
			out = append(out, id)
		}
	}
	return out
}

// fmtNums lists ns, at most 24 of them, in hex or decimal; "-" if none.
func fmtNums(ns []int, hex bool) string {
	if len(ns) == 0 {
		return "-"
	}
	const show = 24
	var parts []string
	for i, n := range ns {
		if i == show {
			parts = append(parts, fmt.Sprintf("... (%d in all)", len(ns)))
			break
		}
		if hex {
			parts = append(parts, fmt.Sprintf("0x%02X", n))
		} else {
			parts = append(parts, strconv.Itoa(n))
		}
	}
	return strings.Join(parts, ",")
}

// ---------------------------------------------------------------------
// frame-phase-probe

// phaseStallAfter is FrameWriter's heartbeat timeout (§4.2): a stretch
// this long without a beat counts as a stall.
const phaseStallAfter = 100 * time.Millisecond

// phaseAcc accumulates frame-phase-probe's reads of the heartbeat. Each
// observation is when a read finished (since the probe began), the value
// read, and how long the read took.
type phaseAcc struct {
	reads                     int
	readMin, readMax, readSum time.Duration
	readHist                  [40]int // reads by duration: bucket i holds those under 2^i µs

	have       bool
	lastAt     time.Duration // the last read
	lastHB     uint32
	chain      bool          // lastChange is a beat the next one can be timed against
	lastChange time.Duration // the read that saw the last beat
	quietSince time.Duration // the last beat, or the start
	stalled    bool          // the current quiet stretch was counted as a stall
	changes    int
	beats      uint64
	timedBeats uint64        // beats in timed intervals
	timedSpan  time.Duration // their total time
	steps      []time.Duration
	skipped    int
	regressed  int
	stalls     int
	detectSum  time.Duration
	detectMax  time.Duration
}

func (a *phaseAcc) observe(at time.Duration, hb uint32, took time.Duration) {
	a.reads++
	a.readSum += took
	if a.reads == 1 || took < a.readMin {
		a.readMin = took
	}
	a.readMax = max(a.readMax, took)
	a.readHist[min(bits.Len64(uint64(took/time.Microsecond)), len(a.readHist)-1)]++

	if !a.have {
		a.have, a.lastAt, a.lastHB, a.quietSince = true, at, hb, at
		return
	}
	switch d := int32(hb - a.lastHB); {
	case d == 0:
		if !a.stalled && at-a.quietSince >= phaseStallAfter {
			a.stalls++
			a.stalled = true
		}
	case d < 0:
		// A save state was loaded, or Dolphin restarted the game: start
		// timing afresh.
		a.regressed++
		a.chain = false
		a.quietSince, a.stalled = at, false
	default:
		a.changes++
		a.beats += uint64(d)
		gap := at - a.lastAt // the beat happened somewhere in here
		a.detectSum += gap
		a.detectMax = max(a.detectMax, gap)
		if d > 1 {
			a.skipped++
		}
		if a.chain {
			iv := at - a.lastChange
			a.timedSpan += iv
			a.timedBeats += uint64(d)
			if d == 1 {
				a.steps = append(a.steps, iv)
			}
		}
		a.chain, a.lastChange = true, at
		a.quietSince, a.stalled = at, false
	}
	a.lastAt, a.lastHB = at, hb
}

// phaseReport is what frame-phase-probe found.
type phaseReport struct {
	Reads                              int
	ReadMin, ReadP50, ReadP99, ReadMax time.Duration // P50 and P99 are bucket upper bounds
	Changes                            int
	Beats                              uint64
	Period                             time.Duration // mean time per beat
	Jitter                             time.Duration // standard deviation of single-beat intervals
	StepMin, StepMax                   time.Duration
	Skipped, Regressed, Stalls         int
	DetectMean, DetectMax              time.Duration
}

func (a *phaseAcc) report() phaseReport {
	r := phaseReport{
		Reads: a.reads, ReadMin: a.readMin, ReadMax: a.readMax,
		Changes: a.changes, Beats: a.beats,
		Skipped: a.skipped, Regressed: a.regressed, Stalls: a.stalls,
		DetectMax: a.detectMax,
	}
	r.ReadP50, r.ReadP99 = a.readPercentile(0.50), a.readPercentile(0.99)
	if a.changes > 0 {
		r.DetectMean = a.detectSum / time.Duration(a.changes)
	}
	if a.timedBeats > 0 {
		r.Period = a.timedSpan / time.Duration(a.timedBeats)
	}
	if n := len(a.steps); n > 0 {
		r.StepMin, r.StepMax = slices.Min(a.steps), slices.Max(a.steps)
		var mean float64
		for _, s := range a.steps {
			mean += float64(s)
		}
		mean /= float64(n)
		var v float64
		for _, s := range a.steps {
			v += (float64(s) - mean) * (float64(s) - mean)
		}
		r.Jitter = time.Duration(math.Sqrt(v / float64(n)))
	}
	return r
}

// readPercentile is the upper bound of the histogram bucket holding the
// p-th read duration.
func (a *phaseAcc) readPercentile(p float64) time.Duration {
	if a.reads == 0 {
		return 0
	}
	want := int(math.Ceil(p * float64(a.reads)))
	n := 0
	for i, c := range a.readHist {
		if n += c; n >= want {
			return time.Duration(1<<i) * time.Microsecond
		}
	}
	return a.readMax
}

// framePhaseProbe spin-reads spawn_trigger for d and reports the
// heartbeat's period and jitter as Go sees it, and how quickly Go sees a
// beat: the window a FrameWriter write lands in after one (§4.2.1).
func framePhaseProbe(ctx context.Context, w io.Writer, m mem.Mem, d time.Duration) error {
	var acc phaseAcc
	start := time.Now()
	for i := 0; ; i++ {
		t0 := time.Now()
		hb, err := mem.ReadU32(m, mailbox.SpawnTrigger)
		if err != nil {
			return err
		}
		took := time.Since(t0)
		acc.observe(t0.Sub(start)+took, hb, took)
		if time.Since(start) >= d {
			break
		}
		if i%1024 == 0 && ctx.Err() != nil {
			break
		}
	}
	printPhase(w, acc.report(), time.Since(start))
	return nil
}

func printPhase(w io.Writer, r phaseReport, ran time.Duration) {
	ms := func(d time.Duration) string { return fmt.Sprintf("%.2f ms", float64(d)/float64(time.Millisecond)) }
	us := func(d time.Duration) string { return fmt.Sprintf("%d µs", d/time.Microsecond) }
	fmt.Fprintf(w, "heartbeat  spawn_trigger 0x%08X over %.1f s: %d beats seen as %d changes\n",
		mailbox.SpawnTrigger, ran.Seconds(), r.Beats, r.Changes)
	if r.Changes == 0 {
		fmt.Fprintln(w, "           the heartbeat never moved: is emulation paused, or the ISO not patched?")
		return
	}
	hz := 0.0
	if r.Period > 0 {
		hz = float64(time.Second) / float64(r.Period)
	}
	fmt.Fprintf(w, "period     %s mean (%.1f Hz); single beats %s to %s, jitter (stddev) %s\n",
		ms(r.Period), hz, ms(r.StepMin), ms(r.StepMax), ms(r.Jitter))
	fmt.Fprintf(w, "missed     %d changes skipped a beat (Go did not poll in time), %d regressions, %d stalls of %v or more\n",
		r.Skipped, r.Regressed, r.Stalls, phaseStallAfter)
	fmt.Fprintf(w, "reads      %d, one every %s; latency min %s, p50 <= %s, p99 <= %s, max %s\n",
		r.Reads, us(ran/time.Duration(max(r.Reads, 1))), us(r.ReadMin), us(r.ReadP50), us(r.ReadP99), us(r.ReadMax))
	fmt.Fprintf(w, "detection  Go saw each beat within %s on average, %s at worst\n", us(r.DetectMean), us(r.DetectMax))
	fmt.Fprintf(w, "window     a FrameWriter read-OR-write lands about %s after a beat (detection + 2 reads), %s at worst;\n",
		us(r.DetectMean+2*r.ReadP50), us(r.DetectMax+2*r.ReadMax))
	fmt.Fprintln(w, "           that is the time in which a game store to the same byte would be lost (§4.2.1)")
}

// ---------------------------------------------------------------------
// heap-free

// heapFree reports the heap error counters, where the arena starts, and
// each JKRExpHeap's free space: the §4.5 margin an arena bump would take.
func heapFree(w io.Writer, m mem.Mem) error {
	fmt.Fprintln(w, "heap error counters (any increase means an allocation failed):")
	c, err := heap.ReadCounters(m)
	if err != nil {
		return err
	}
	var parts []string
	for i, v := range c.Values() {
		parts = append(parts, fmt.Sprintf("%s %d", heap.CounterNames[i], v))
	}
	fmt.Fprintf(w, "  %s\n", strings.Join(parts, "  "))

	codeEnd, err := mem.ReadU32(m, addr.JKRHeapCodeEnd)
	if err != nil {
		return err
	}
	arenaLo, err := mem.ReadU32(m, addr.OSArenaLo)
	if err != nil {
		return err
	}
	switch {
	case codeEnd >= mailbox.PatchedArenaLo:
		fmt.Fprintf(w, "arena: JKRHeap::mCodeEnd 0x%08X, 0x%X above the patched __OSArenaLo 0x%08X\n",
			codeEnd, codeEnd-mailbox.PatchedArenaLo, mailbox.PatchedArenaLo)
	default:
		fmt.Fprintf(w, "arena: JKRHeap::mCodeEnd 0x%08X is below the patched __OSArenaLo 0x%08X: a vanilla ISO, or not booted?\n",
			codeEnd, mailbox.PatchedArenaLo)
	}
	fmt.Fprintf(w, "       __OSArenaLo now 0x%08X (initArena moves it to the arena top, JKRHeap.cpp:94)\n", arenaLo)

	fmt.Fprintln(w, "JKRExpHeap free lists (free = getTotalFreeSize, largest = getFreeSize):")
	for _, hp := range heap.ExpHeaps {
		h, err := heap.Walk(m, hp.Name, hp.Ptr)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "  %-8s 0x%08X", h.Name, h.Ptr)
		if h.Size != 0 {
			fmt.Fprintf(w, "  0x%08X-0x%08X (%d KiB): free %d KiB in %d blocks, largest %d KiB",
				h.Start, h.End, h.Size/1024, h.Free/1024, h.Blocks, h.Largest/1024)
		}
		if h.Problem != "" {
			fmt.Fprintf(w, "  [%s]", h.Problem)
		}
		fmt.Fprintln(w)
	}
	return nil
}

// ---------------------------------------------------------------------
// blob-verify-live

// byteRun is a run of differing bytes, as offsets.
type byteRun struct{ Off, Len int }

// blobDiff compares a live read-back with the bytes that should be there.
type blobDiff struct {
	Diff    int       // bytes that differ
	Runs    []byteRun // the first runs of differing bytes
	More    int       // runs past those
	AllZero bool      // live is all zero: nothing loaded there
}

func compareBlob(live, want []byte, maxRuns int) blobDiff {
	var d blobDiff
	d.AllZero = !slices.ContainsFunc(live, func(b byte) bool { return b != 0 })
	for i := 0; i < len(want); {
		if i < len(live) && live[i] == want[i] {
			i++
			continue
		}
		j := i
		for j < len(want) && (j >= len(live) || live[j] != want[j]) {
			j++
		}
		d.Diff += j - i
		if len(d.Runs) < maxRuns {
			d.Runs = append(d.Runs, byteRun{i, j - i})
		} else {
			d.More++
		}
		i = j
	}
	return d
}

// blobVerifyLive reads T2 back from the running game and compares it with
// the blob this binary patches in (internal/inject T2Bytes): the §4.5
// option B loader check. It writes nothing.
func blobVerifyLive(w io.Writer, m mem.Mem) error {
	want := inject.T2Bytes
	live, err := m.Read(inject.T2Address, len(want))
	if err != nil {
		return err
	}
	end := inject.T2Address + uint32(len(want))
	d := compareBlob(live, want, 8)
	fmt.Fprintf(w, "T2 0x%08X-0x%08X (0x%X bytes of inject.T2Bytes): ", inject.T2Address, end, len(want))
	switch {
	case d.Diff == 0:
		fmt.Fprintln(w, "MATCH")
		return nil
	case d.AllZero:
		fmt.Fprintln(w, "all zero: the section did not load (a vanilla ISO, or Dolphin dropped it)")
		return nil
	}
	first := d.Runs[0].Off
	fmt.Fprintf(w, "0x%X bytes differ in %d runs; the first at +0x%04X (0x%08X): live %02X, want %02X\n",
		d.Diff, len(d.Runs)+d.More, first, inject.T2Address+uint32(first), live[first], want[first])
	for _, r := range d.Runs {
		fmt.Fprintf(w, "  +0x%04X..+0x%04X (%d bytes)\n", r.Off, r.Off+r.Len, r.Len)
	}
	if d.More > 0 {
		fmt.Fprintf(w, "  ... and %d more runs\n", d.More)
	}
	fmt.Fprintln(w, "The mod's static variables (inject/src) live in T2 and change as it runs, so differences")
	fmt.Fprintln(w, "inside its data are expected; differences in code mean a different build, a stale save state")
	fmt.Fprintln(w, "or a section that did not load.")
	return nil
}

// ---------------------------------------------------------------------
// region-probe

// canaryXor makes each canary word depend on its address, so a word
// copied or shifted by something else still reads as changed. The result
// for these regions (0xDA1A....) is not a MEM1 pointer.
const canaryXor uint32 = 0x5A5A5A5A

func canaryWord(a uint32) uint32 { return a ^ canaryXor }

// canaryChunk is the most region-probe writes in one call.
const canaryChunk = 0x400

// canaryRegions are the gaps beside the mod the design wants proven free
// (§4.5, §11 item 5): the TRK stack up to T2, and the slack between the
// mailbox and the patched arena in its two parts around the REL's exec-
// hook words (mailbox.RELHookRegion, which T2 branches to), the
// enemy-mirror block (mailbox.EnemyBlockRegion), the remote-Link block
// (mailbox.RemoteBlockRegion), the enemy knob table after it
// (mailbox.EnemyKnobRegion, layout 4) and batch D's actor_refresh request
// after that (mailbox.RefreshRegion), the visuals block after that
// (mailbox.VisRegion), which Go and the REL write while they run and which
// the probe therefore never arms or checks, and the mini-Links' eye
// packets at the top of the slack (mailbox.EyePacketsRegion), which T2 and
// the game's draw lists write.
var canaryRegions = []addr.Region{mailbox.CanaryTRK, mailbox.CanaryArena, mailbox.CanaryArenaHigh}

// canaryForbidden is what a canary must never cover: the main stack and
// the header above it that boot writes, T2 with its slack and the
// mailbox, and the patched arena (§4.5 layout).
func canaryForbidden() []addr.Region {
	return []addr.Region{
		mailbox.MainStack,
		mailbox.MainStackHeader,
		{Name: "T2, its slack and the mailbox", Start: inject.T2Address, End: mailbox.Mailbox + mailbox.MailboxSize},
		mailbox.RELHookRegion,
		mailbox.EnemyBlockRegion,
		mailbox.RemoteBlockRegion,
		mailbox.EnemyKnobRegion,
		mailbox.RefreshRegion,
		mailbox.VisRegion,
		mailbox.BoatRegion,
		mailbox.EyePacketsRegion,
		mailbox.FxRegion,
		{Name: "the arena", Start: mailbox.PatchedArenaLo, End: addr.MEM1End},
	}
}

// canarySafe checks that r is somewhere region-probe may write.
func canarySafe(r addr.Region) error {
	if r.Start >= r.End || r.Start%4 != 0 || r.End%4 != 0 {
		return fmt.Errorf("%s 0x%08X-0x%08X is not a word-aligned range", r.Name, r.Start, r.End)
	}
	if err := mem.CheckRange(r.Start, int(r.Size())); err != nil {
		return err
	}
	if t2End := inject.T2Address + uint32(len(inject.T2Bytes)); t2End > mailbox.Mailbox {
		return fmt.Errorf("T2 ends at 0x%08X, past the mailbox at 0x%08X: the layout changed, so the canary ranges are unproven", t2End, mailbox.Mailbox)
	}
	for _, f := range canaryForbidden() {
		if r.Start < f.End && f.Start < r.End {
			return fmt.Errorf("%s 0x%08X-0x%08X overlaps %s 0x%08X-0x%08X", r.Name, r.Start, r.End, f.Name, f.Start, f.End)
		}
	}
	return nil
}

// canaryPreflight refuses to arm unless the patched layout is running.
// On a vanilla ISO both regions are inside the arena, which is heap. The
// heartbeat must move (the mod runs) and JKRHeap::mCodeEnd, where
// initArena started the heaps (JKRHeap.cpp:77-88), must be at or above
// the patched __OSArenaLo.
func canaryPreflight(m mem.Mem, tick func()) error {
	codeEnd, err := mem.ReadU32(m, addr.JKRHeapCodeEnd)
	if err != nil {
		return err
	}
	if codeEnd < mailbox.PatchedArenaLo || codeEnd >= addr.MEM1End {
		return fmt.Errorf("JKRHeap::mCodeEnd is 0x%08X, not at or above the patched __OSArenaLo 0x%08X: is this the patched ISO, booted?",
			codeEnd, mailbox.PatchedArenaLo)
	}
	hb, err := mem.ReadU32(m, mailbox.SpawnTrigger)
	if err != nil {
		return err
	}
	tick()
	hb2, err := mem.ReadU32(m, mailbox.SpawnTrigger)
	if err != nil {
		return err
	}
	if hb2 == hb {
		return errors.New("the heartbeat is not moving: is emulation paused, or the ISO not patched?")
	}
	return nil
}

// armCanary fills r with canary words. It refuses unless r is safe, and,
// without force, when r already holds words that are neither zero nor a
// canary: memory something uses.
func armCanary(m mem.Mem, r addr.Region, force bool) error {
	if err := canarySafe(r); err != nil {
		return err
	}
	cur, err := m.Read(r.Start, int(r.Size()))
	if err != nil {
		return err
	}
	busy, first := 0, -1
	for i := 0; i < len(cur); i += 4 {
		if v := be32(cur[i:]); v != 0 && v != canaryWord(r.Start+uint32(i)) {
			if busy++; first < 0 {
				first = i
			}
		}
	}
	if busy > 0 && !force {
		return fmt.Errorf("%s: %d of %d words already hold data (first 0x%08X = 0x%08X), so something may use it; not arming (--force arms anyway)",
			r.Name, busy, len(cur)/4, r.Start+uint32(first), be32(cur[first:]))
	}
	buf := make([]byte, r.Size())
	for i := 0; i < len(buf); i += 4 {
		binary.BigEndian.PutUint32(buf[i:], canaryWord(r.Start+uint32(i)))
	}
	for off := 0; off < len(buf); off += canaryChunk {
		if err := m.Write(r.Start+uint32(off), buf[off:min(off+canaryChunk, len(buf))]); err != nil {
			return err
		}
	}
	return nil
}

// wordRun is a run [Start, End) of changed canary words.
type wordRun struct{ Start, End uint32 }

// canaryCheck is what became of one region's canaries.
type canaryCheck struct {
	Region                addr.Region
	Words, Intact, Zeroed int
	Runs                  []wordRun // the first runs of changed words
	More                  int
}

// checkCanary reads r back and finds the words that are no longer canaries.
func checkCanary(m mem.Mem, r addr.Region) (canaryCheck, error) {
	c := canaryCheck{Region: r}
	if err := canarySafe(r); err != nil {
		return c, err
	}
	cur, err := m.Read(r.Start, int(r.Size()))
	if err != nil {
		return c, err
	}
	c.Words = len(cur) / 4
	const maxRuns = 8
	open := false
	for i := 0; i < len(cur); i += 4 {
		a := r.Start + uint32(i)
		v := be32(cur[i:])
		if v == canaryWord(a) {
			c.Intact++
			open = false
			continue
		}
		if v == 0 {
			c.Zeroed++
		}
		switch {
		case open:
			c.Runs[len(c.Runs)-1].End = a + 4
		case len(c.Runs) < maxRuns:
			c.Runs = append(c.Runs, wordRun{a, a + 4})
			open = true
		default:
			c.More++
		}
	}
	return c, nil
}

func regionArmWarning() string {
	var b strings.Builder
	b.WriteString("WARNING: region-probe arm WRITES GAME RAM. It fills these ranges with canary words:\n")
	for _, r := range canaryRegions {
		fmt.Fprintf(&b, "  %-12s 0x%08X-0x%08X (0x%X bytes)\n", r.Name, r.Start, r.End, r.Size())
	}
	b.WriteString("Both are free only by inference (docs/08 §4.5). If anything does use them, the game can\n")
	b.WriteString("crash or misbehave. Nothing else is written: never T2 (0x80410000), the mailbox (0x80412F00)\n")
	b.WriteString("or the arena. A save state loaded afterwards restores the old RAM and wipes the canaries.\n")
	return b.String()
}

// regionArm arms both regions after canaryPreflight. A region that fails
// is skipped and reported; the other is still armed.
func regionArm(w io.Writer, m mem.Mem, tick func(), force bool) error {
	if err := canaryPreflight(m, tick); err != nil {
		return err
	}
	var errs []error
	for _, r := range canaryRegions {
		if err := armCanary(m, r, force); err != nil {
			errs = append(errs, err)
			fmt.Fprintf(w, "%-12s NOT armed: %v\n", r.Name, err)
			continue
		}
		fmt.Fprintf(w, "%-12s armed: %d words at 0x%08X-0x%08X\n", r.Name, r.Size()/4, r.Start, r.End)
	}
	if len(errs) == 0 {
		fmt.Fprintln(w, "Play on (boot, menus, stage changes), then run `region-probe check`.")
	}
	return errors.Join(errs...)
}

// regionCheck reports which canary words changed. It writes nothing.
func regionCheck(w io.Writer, m mem.Mem) error {
	for _, r := range canaryRegions {
		c, err := checkCanary(m, r)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "%-12s 0x%08X-0x%08X (%d words): ", r.Name, r.Start, r.End, c.Words)
		switch changed := c.Words - c.Intact; {
		case changed == 0:
			fmt.Fprintln(w, "all intact")
			continue
		case c.Intact == 0:
			fmt.Fprintf(w, "no canaries (%d words zero): never armed, or a reboot or save state restored this RAM\n", c.Zeroed)
			continue
		default:
			fmt.Fprintf(w, "%d changed (%d now zero, %d other)\n", changed, c.Zeroed, changed-c.Zeroed)
		}
		for _, run := range c.Runs {
			fmt.Fprintf(w, "  0x%08X-0x%08X (%d words)\n", run.Start, run.End, (run.End-run.Start)/4)
		}
		if c.More > 0 {
			fmt.Fprintf(w, "  ... and %d more runs\n", c.More)
		}
	}
	return nil
}

// ---------------------------------------------------------------------
// stage-hop

// stageHop is a debug stage change: what dComIfGp_setNextStage hands
// mNextStage (d_com_inf_game.cpp:667-669; dStage_nextStage_c::set,
// d_stage.cpp:26-31), without its other effects.
type stageHop struct {
	Name  string
	Point int16
	Room  int8
	Layer int8
}

// Bounds on a stage-hop request.
const (
	// stageHopLayerMax: getLayerNo returns a set layer as is
	// (d_com_inf_game.cpp:268) and it indexes the 12-entry
	// l_layerFuncTable_p unchecked (d_stage.cpp:2144-2150).
	stageHopLayerMax = 11
	// stageHopWipe is the wipe written: 0, setNextStage's default
	// (d_com_inf_game.h:2247-2249). dScnPly_Draw asserts wipe < 12
	// (d_s_play.cpp:292-308).
	stageHopWipe = 0
)

// parseStageHop parses `<stage> [point] [room] [layer]`.
func parseStageHop(pos []string) (stageHop, error) {
	h := stageHop{Layer: -1}
	if len(pos) < 1 || len(pos) > 4 {
		return h, errors.New("give a stage name and at most a point, a room and a layer")
	}
	h.Name = pos[0]
	if len(h.Name) == 0 || len(h.Name) >= addr.StageNameLen {
		return h, fmt.Errorf("stage name %q must be 1-%d characters (mName is char[8])", h.Name, addr.StageNameLen-1)
	}
	for _, c := range h.Name {
		if !(c == '_' || c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z') {
			return h, fmt.Errorf("stage name %q: only letters, digits and _", h.Name)
		}
	}
	num := func(i, lo, hi int, name string) (int, error) {
		if i >= len(pos) {
			return 0, nil
		}
		v, err := strconv.Atoi(pos[i])
		if err != nil || v < lo || v > hi {
			return 0, fmt.Errorf("%s must be a number from %d to %d, not %q", name, lo, hi, pos[i])
		}
		return v, nil
	}
	// Negative points mean restarts the raw write does not set up
	// (d_com_inf_game.cpp:2369-2403 use -1 and -3), so only >= 0; a
	// point is matched against a PLYR entry's (u8)angle.z, so at most
	// 255 (d_stage.cpp:1455-1463; write.StageRequest.Check).
	p, err := num(1, 0, math.MaxUint8, "point")
	if err != nil {
		return h, err
	}
	r, err := num(2, 0, addr.RoomCount-1, "room")
	if err != nil {
		return h, err
	}
	h.Point, h.Room = int16(p), int8(r)
	if len(pos) > 3 {
		l, err := num(3, -1, stageHopLayerMax, "layer")
		if err != nil {
			return h, err
		}
		h.Layer = int8(l)
	}
	return h, nil
}

// bytes is h laid out as dStage_startStage_c (d_stage.h:949-952).
func (h stageHop) bytes() [addr.StageSize]byte {
	var b [addr.StageSize]byte
	copy(b[addr.StageOffName:addr.StageNameLen-1], h.Name)
	binary.BigEndian.PutUint16(b[addr.StageOffPoint:], uint16(h.Point))
	b[addr.StageOffRoomNo] = byte(h.Room)
	b[addr.StageOffLayer] = byte(h.Layer)
	return b
}

func (h stageHop) warning() string {
	return fmt.Sprintf(`WARNING: stage-hop WRITES GAME RAM. It writes mNextStage (0x%08X-0x%08X) to ask for
stage %q point %d room %d layer %d. It is a raw write, so it skips what
dComIfGp_setNextStage also does (Link's last-scene speed and mode, and the restart point,
d_com_inf_game.cpp:671-690): a death or a restart after the hop can put Link somewhere odd.
Unless --no-check was given or no disc was found, the stage, room and start point were
checked against the disc above; one the stage lacks halts the game (d_stage.cpp:1463).
Debug sessions only.
`, addr.NextStage, addr.NextStage+addr.NextStageSize, h.Name, h.Point, h.Room, h.Layer)
}

// applyStageHop writes h into mNextStage if the gates allow it: STABLE
// (which includes no stage change pending), IDLE and HEARTBEAT. The name,
// point, room, layer and wipe go first and mEnable last, so the game never
// sees an enabled request half written.
func applyStageHop(m mem.Mem, h stageHop, st gate.Status) error {
	if !st.CanWriteIdle() {
		return fmt.Errorf("not now: %s", st.Why())
	}
	// The gates read mEnable a tick ago; read it again just before the
	// write so a loading zone Link just walked into wins.
	if en, err := mem.ReadU8(m, addr.NextStageEnable); err != nil {
		return err
	} else if en != 0 {
		return errors.New("not now: the game just asked for a stage change itself")
	}
	b := h.bytes()
	if err := m.Write(addr.NextStage, b[:]); err != nil {
		return err
	}
	if err := mem.WriteU8(m, addr.NextStage+addr.NextOffWipe, stageHopWipe); err != nil {
		return err
	}
	return mem.WriteU8(m, addr.NextStageEnable, 1)
}

// stageHopRun checks the gates, sends the hop, then waits up to 10 s for
// the new stage to be loaded.
func stageHopRun(w io.Writer, m mem.Mem, h stageHop, tick func()) error {
	_, st, _, err := diag.GateSnapshots(m, tick)
	if err != nil {
		return err
	}
	if err := applyStageHop(m, h, st); err != nil {
		return err
	}
	fmt.Fprintf(w, "mNextStage set: %q point %d room %d layer %d\n", h.Name, h.Point, h.Room, h.Layer)
	for range 100 {
		tick()
		s, err := state.Read(m, state.Options{})
		if err != nil {
			return err
		}
		if s.StageName == h.Name && s.NextEnable == 0 {
			fmt.Fprintf(w, "now in %s\n", diag.Where(s))
			return nil
		}
	}
	return errors.New("the new stage did not load within 10 s; check the game with coop-probe or a screenshot")
}
