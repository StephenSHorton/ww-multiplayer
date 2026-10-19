package main

// enemy-mirror-local (L2) and enemy-block-dump: the one-Dolphin loopback
// of the Moblin mirror (ww-multiplayer-handoff/design/
// host-authoritative-prototype.md, step 4) and a decoded print of the
// block. enemy-mirror-local samples the keyed Moblin setID 0 of Link's
// room the way the host sampler does and writes it, every 16 ms, as the
// target of setID 1 (the local setID 1 actor's pid), +150 units on X,
// role client, so setID 1 follows setID 0 under the REL module. Every
// second it prints go_write_ms against ack_frame, adopted_mask, the
// status entries and the canaries, and once the first write-to-consume
// delay. On stop it writes magic 0, waits a few frames and confirms the
// pause byte of setID 1 is back to the pause_prev the REL recorded.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/diag"
	"github.com/StephenSHorton/ww-multiplayer/mods/enemysync"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

const (
	enemyMirrorLocalUsage = "enemy-mirror-local --yes [secs=20, 0 = until Ctrl+C]"
	enemyBlockDumpUsage   = "enemy-block-dump"
	// mirrorLocalOffsetX is how far setID 1 is placed from setID 0.
	mirrorLocalOffsetX = 150.0
)

// runEnemyMirrorLocal is the enemy-mirror-local command.
func runEnemyMirrorLocal(cmd string, args []string) {
	pos, flags, err := diag.SplitFlags(args, "yes")
	if err != nil {
		exitUsage(err, enemyMirrorLocalUsage)
	}
	secs, err := diagIntArg(pos, 0, 20, 0, 86400, "secs")
	if err != nil {
		exitUsage(err, enemyMirrorLocalUsage)
	}
	fmt.Println("enemy-mirror-local writes the enemy-mirror block of this Dolphin every 16 ms so the REL module pauses Moblin setID 1 and moves it 150 units beside setID 0; the Moblin is released when it stops.")
	if !flags["yes"] {
		fmt.Println("Not written: re-run with --yes.")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err = diag.WithMem(openDolphin, func(m mem.Mem) error {
		return enemyMirrorLocal(ctx, os.Stdout, m, time.Duration(secs)*time.Second)
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmd, err)
		os.Exit(1)
	}
}

// enemyMirrorLocal runs the loopback over m for d (0: until ctx ends).
func enemyMirrorLocal(ctx context.Context, w io.Writer, m mem.Mem, d time.Duration) error {
	c, err := wwapi.CheckContract(m, wwapi.ContractOptions{})
	if err != nil {
		return err
	}
	if !c.WritesAllowed {
		return errors.New("this Dolphin's game is not this version's patch: " + c.Why)
	}
	if !c.REL.Linked || c.REL.BuildID != inject.RELBuildID {
		return fmt.Errorf("the REL module is not linked with this build (linked %v, build %08X, want %08X): nothing would consume the block", c.REL.Linked, c.REL.BuildID, inject.RELBuildID)
	}
	scope, err := enemysync.ReadScope(m)
	if err != nil {
		return err
	}
	if !scope.InPlay {
		return fmt.Errorf("the game is not in play (stage %q)", scope.Stage)
	}
	mos, err := enemysync.KeyedMoblins(m, scope.Room)
	if err != nil {
		return err
	}
	var src, dst *enemysync.Moblin
	for i := range mos {
		switch mos[i].SetID {
		case 0:
			src = &mos[i]
		case 1:
			dst = &mos[i]
		}
	}
	if src == nil || dst == nil {
		return fmt.Errorf("need keyed mode-0 Moblins setID 0 and 1 in %s room %d; found %d: %+v", scope.Stage, scope.Room, len(mos), mos)
	}
	pausePrev, err := mem.ReadU8(m, dst.Addr+addr.ActorOffPauseFlag)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "stage %q room %d; source setID 0 pid %d 0x%08X -> target setID 1 pid %d 0x%08X (pause byte 0x%02X); block at 0x%08X; REL build %08X frame %d\n",
		scope.Stage, scope.Room, src.PID, src.Addr, dst.PID, dst.Addr, pausePrev, mailbox.EnemyBlockAddr, c.REL.BuildID, c.REL.Frames)

	start := time.Now()
	epoch := uint32(start.Unix() & 0xFFFF)
	var deadline time.Time
	if d > 0 {
		deadline = start.Add(d)
	}
	var (
		seq        uint32
		firstWrite time.Time
		firstSeq   uint32
		consumed   bool
		nextLine   = start.Add(time.Second)
		snap       = true
	)
	defer func() {
		if err := mailbox.ClearEnemyBlock(m); err != nil {
			fmt.Fprintf(w, "clearing the block: %v\n", err)
			return
		}
		time.Sleep(200 * time.Millisecond)
		st, err := mailbox.ReadEnemyStatus(m)
		if err != nil {
			fmt.Fprintf(w, "reading the status after the clear: %v\n", err)
			return
		}
		pause, err := mem.ReadU8(m, dst.Addr+addr.ActorOffPauseFlag)
		if err != nil {
			fmt.Fprintf(w, "reading the pause byte after the clear: %v\n", err)
			return
		}
		fmt.Fprintf(w, "stopped: magic 0 written; status[0] state %d pause_prev 0x%02X; setID 1's pause byte now 0x%02X (was 0x%02X): %s\n",
			st.Status[0].State, st.Status[0].PausePrev, pause, pausePrev, map[bool]string{true: "restored", false: "NOT restored"}[pause == pausePrev])
	}()
	t := time.NewTicker(enemysync.Tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-t.C:
			if !deadline.IsZero() && now.After(deadline) {
				return nil
			}
			s, err := enemysync.ReadSample(m, src)
			if err != nil {
				if errors.Is(err, actors.ErrChanged) {
					fmt.Fprintf(w, "%v: stopping\n", err)
					return nil
				}
				return err
			}
			seq++
			b := &mailbox.EnemyBlock{Magic: mailbox.EnemyMagic, Seq: seq, GoWriteMS: uint32(now.Sub(start).Milliseconds()),
				Flags: mailbox.EnemyFlagMirror | mailbox.EnemyFlagAnim, Layout: mailbox.EnemyLayout, Epoch: epoch, Stage: scope.Stage, Room: scope.Room, Role: mailbox.EnemyRoleClient,
				Slots: []mailbox.EnemySlot{{PID: dst.PID, Pos: [3]float32{s.Pos[0] + mirrorLocalOffsetX, s.Pos[1], s.Pos[2]}, AngleY: s.AngleY, Tilt: s.Tilt,
					Action: s.Action, Mode: s.Mode, AttackType: s.AttackType, AttackPhase: s.AttackPhase, Health: s.Health,
					Flags: s.Flags &^ enemysync.SlotDying, MorfFrame: s.MorfFrame, MorfRate: s.MorfRate, SampleSeq: seq}}}
			if snap {
				b.Slots[0].Flags |= mailbox.EnemySFSnap
				snap = false
			}
			if err := mailbox.WriteEnemyBlock(m, b); err != nil {
				return err
			}
			if firstWrite.IsZero() {
				firstWrite, firstSeq = now, seq
			}
			st, err := mailbox.ReadEnemyStatus(m)
			if err != nil {
				return err
			}
			if !consumed && st.AckSeq >= firstSeq && st.AckSeq <= seq {
				consumed = true
				fmt.Fprintf(w, "first consume: seq %d acked at ack_frame %d, %d ms after the first write (seq %d); %d ms after its own write\n",
					st.AckSeq, st.AckFrame, now.Sub(firstWrite).Milliseconds(), firstSeq, now.Sub(firstWrite).Milliseconds()-int64(st.AckSeq-firstSeq)*16)
			}
			if now.After(nextLine) {
				nextLine = now.Add(time.Second)
				fmt.Fprintf(w, "[%5.1fs] seq %d go_write_ms %d ack_seq %d ack_frame %d adopted %04b epoch_seen %d status[0] actor 0x%08X pid %d state %d prev 0x%02X err %d frames %d canaries %+v src %.1f %.1f %.1f\n",
					now.Sub(start).Seconds(), seq, b.GoWriteMS, st.AckSeq, st.AckFrame, st.AdoptedMask, st.EpochSeen,
					st.Status[0].Actor, st.Status[0].PID, st.Status[0].State, st.Status[0].PausePrev, st.Status[0].LastErr, st.Status[0].Frames, st.Canaries,
					s.Pos[0], s.Pos[1], s.Pos[2])
			}
		}
	}
}

// runEnemyBlockDump is the enemy-block-dump command.
func runEnemyBlockDump(cmd string, args []string) {
	if len(args) > 0 {
		exitUsage(fmt.Errorf("%s takes no arguments", cmd), enemyBlockDumpUsage)
	}
	err := diag.WithMem(openDolphin, func(m mem.Mem) error { return enemyBlockDump(os.Stdout, m) })
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmd, err)
		os.Exit(1)
	}
}

// enemyBlockDump prints the whole block decoded.
func enemyBlockDump(w io.Writer, m mem.Reader) error {
	blk, tail, st, err := mailbox.ReadEnemyBlock(m)
	if err != nil {
		return err
	}
	rel, err := mailbox.ReadREL(m)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "enemy block at 0x%08X (0x%X bytes); REL words: magic 0x%08X linked=%v build 0x%08X frames %d\n", mailbox.EnemyBlockAddr, mailbox.EnemySize, rel.Magic, rel.Linked, rel.BuildID, rel.Frames)
	magic := "off"
	if blk.Magic == mailbox.EnemyMagic {
		magic = "'WWEN'"
	} else if blk.Magic != 0 {
		magic = "unknown"
	}
	torn := ""
	if tail != blk.Seq {
		torn = " (TORN: head != tail)"
	}
	fmt.Fprintf(w, "Go span: magic 0x%08X %s seq_head %d seq_tail %d%s host_beat %d go_write_ms %d count %d flags 0x%02X layout %d epoch %d stage %q room %d role %d\n",
		blk.Magic, magic, blk.Seq, tail, torn, blk.HostBeat, blk.GoWriteMS, len(blk.Slots), blk.Flags, blk.Layout, blk.Epoch, blk.Stage, blk.Room, blk.Role)
	for i, s := range blk.Slots {
		fmt.Fprintf(w, "  slot %d: pid %d pos %.1f %.1f %.1f angle %d tilt %d action %d mode %d attack %d/%d health %d flags 0x%02X frame %.2f rate %.2f sample %d bck 0x%02X attr %d anm_seq %d morf %d flags2 0x%02X blur %d/%.2f\n",
			i, s.PID, s.Pos[0], s.Pos[1], s.Pos[2], s.AngleY, s.Tilt, s.Action, s.Mode, s.AttackType, s.AttackPhase, s.Health, s.Flags, s.MorfFrame, s.MorfRate, s.SampleSeq,
			s.Bck, s.AnmAttr, s.AnmSeq, s.Morf, s.Flags2, s.BlurShape, s.BlurScale)
	}
	h := blk.HitApply
	fmt.Fprintf(w, "  hit_apply: seq %d setID %d collider %d flags 0x%02X atType 0x%08X atp %d se %d hit_mark %d spl %d pos %.1f %.1f %.1f pid %d\n",
		h.Seq, h.SetID, h.Collider, h.Flags, h.AtType, h.Atp, h.Se, h.HitMark, h.Spl, h.Pos[0], h.Pos[1], h.Pos[2], h.PID)
	r := st.HitResult
	fmt.Fprintf(w, "REL span: ack_seq %d ack_frame %d adopted %04b epoch_seen %d hit_apply_ack %d result %d detail %d frames %d sets %d canaries pause_reasserts %d anm_unresolved %d revalidate_failures %d watchdog_releases %d canaries2 %v\n",
		st.AckSeq, st.AckFrame, st.AdoptedMask, st.EpochSeen, st.HitApplyAck, r.Result, r.Detail, r.Frames, r.Sets,
		st.Canaries.PauseReasserts, st.Canaries.AnmUnresolved, st.Canaries.RevalidateFailures, st.Canaries.WatchdogReleases, st.Canaries2)
	for i, e := range st.Status {
		fmt.Fprintf(w, "  status %d: actor 0x%08X pid %d state %d pause_prev 0x%02X last_err %d frames %d anm_state %d bck 0x%02X anm_changes %d; hit: seq %d collider %d pending %d\n",
			i, e.Actor, e.PID, e.State, e.PausePrev, e.LastErr, e.Frames, e.AnmState, e.Bck, e.AnmChanges, st.Hits[i].HitSeq, st.Hits[i].Collider, st.Hits[i].Pending)
	}
	return nil
}
