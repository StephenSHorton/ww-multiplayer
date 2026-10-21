package main

// enemy-mirror-local (L2) and enemy-block-dump: the one-Dolphin loopback
// of the Moblin mirror (ww-multiplayer-handoff/design/
// host-authoritative-prototype.md, step 4) and a decoded print of the
// block. enemy-mirror-local samples the keyed Moblin setID 0 of Link's
// room the way the host sampler does and writes it, every 16 ms, as the
// target of setID 1 (the local setID 1 actor's pid), +150 units on X,
// role client, so setID 1 follows setID 0 under the REL module, with the
// S2 animation fields the host derives (Tracker over the Mo2 table) so
// the LC2 live check runs on one Dolphin. Every second it prints
// go_write_ms against ack_frame, adopted_mask, the status entries and the
// canaries, then the source's and the copy's bck and frame (the copy's
// mpMorf+0x54 through the same Mo2 table), their |delta| and the REL's
// anm_state/bck/anm_changes and canaries2; once, the first
// write-to-consume delay. On stop it prints the frame residual summary,
// writes magic 0, waits a few frames and confirms the pause byte of
// setID 1 is back to the pause_prev the REL recorded.

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
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
	enemyMirrorLocalUsage = "enemy-mirror-local --yes [--colliders] [secs=20, 0 = until Ctrl+C]"
	enemyBlockDumpUsage   = "enemy-block-dump"
	// mirrorLocalOffsetX is how far setID 1 is placed from setID 0.
	mirrorLocalOffsetX = 150.0
)

// runEnemyMirrorLocal is the enemy-mirror-local command.
func runEnemyMirrorLocal(cmd string, args []string) {
	pos, flags, err := diag.SplitFlags(args, "yes", "colliders")
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
		return enemyMirrorLocal(ctx, os.Stdout, m, time.Duration(secs)*time.Second, flags["colliders"])
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmd, err)
		os.Exit(1)
	}
}

// mirrorLocalEvery is how often enemy-mirror-local prints its line (a
// var so the test can see lines inside a short run).
var mirrorLocalEvery = time.Second

// mirrorLocalGrace is how many block writes (16 ms each) of a source bck
// or restart the copy gets before it is compared: the copy is read right
// after the write, before the REL has consumed it, so the first compare
// is of the write before; the design's L2b allows the copy "one block
// write of each host change" (moblin-s2-v2-final.txt, L2b), and the
// second write is that one.
const mirrorLocalGrace = 2

// mirrorGrace tracks the source's bck and anm_seq across writes: since is
// the first write that carried the current pair, bumps counts anm_seq
// changes (to hold against the REL's anm_changes, design L2b).
type mirrorGrace struct {
	have   bool
	bck    uint8
	anmSeq uint8
	since  uint32
	bumps  int
}

// note takes write seq's source bck and anm_seq.
func (g *mirrorGrace) note(seq uint32, bck, anmSeq uint8) {
	if g.have && anmSeq != g.anmSeq {
		g.bumps++
	}
	if !g.have || bck != g.bck || anmSeq != g.anmSeq {
		g.have, g.bck, g.anmSeq, g.since = true, bck, anmSeq, seq
	}
}

// graced reports whether the copy read after write seq has had
// mirrorLocalGrace writes of the current pair.
func (g *mirrorGrace) graced(seq uint32) bool { return g.have && seq-g.since >= mirrorLocalGrace }

// mirrorResidual accumulates LC2's frame residual (design L2b: |copy
// frame - host frame| <= 2 frames, RMS): per tick, the copy's frame
// against the source's, counted only once the copy has had
// mirrorLocalGrace writes of the source's current bck and anm_seq (a
// same-bck restart, NAJAB's phases on 0x61, resets the grace as a bck
// change does). A sample whose copy bck differs counts as a mismatch and
// not in the residual (the frames of two animations do not compare); a
// source bck unresolved or a copy whose morf was unreadable counts in
// neither. On a LOOP attribute the delta is folded over the loop's
// period, so a wrap the copy takes a write after the source does not
// count as a whole loop (mirrorFrameDelta).
type mirrorResidual struct {
	n        int
	sumSq    float64
	max      float64
	mismatch int
	skipped  int // in grace, source unresolved or copy unread
}

// add takes one tick's pair; period is the copy's LOOP period (0: none)
// and graced is whether the grace has run out.
func (r *mirrorResidual) add(srcBck, copyBck uint8, srcFrame, copyFrame, period float32, copyOK, graced bool) {
	if !graced || !copyOK || srcBck == addr.Mo2BckNone {
		r.skipped++
		return
	}
	if copyBck != srcBck {
		r.mismatch++
		return
	}
	d := mirrorFrameDelta(srcFrame, copyFrame, period)
	r.n++
	r.sumSq += d * d
	r.max = max(r.max, d)
}

// mirrorFrameDelta is |copy - source| frames, folded over period when it
// is > 0: J3DFrameCtrl::update's LOOP wraps mFrame by mEnd - mLoop
// (J3DAnimation.cpp:171-184), so 59.5 against 0.5 on a 60-frame loop is
// 1 frame apart, not 59. LOOP_REVERSE turns at its ends without a jump
// (:198-208) and is not folded.
func mirrorFrameDelta(srcFrame, copyFrame, period float32) float64 {
	d := math.Abs(float64(copyFrame) - float64(srcFrame))
	if p := float64(period); p > 0 {
		d = math.Mod(d, p)
		d = min(d, p-d)
	}
	return d
}

// mirrorLoopPeriod reads the LOOP period (mEnd - mLoop) of the morf at
// mo's mpMorf, or 0 when attr is not LOOP or the read fails.
func mirrorLoopPeriod(m mem.Reader, mo *enemysync.Moblin, attr uint8) float32 {
	if attr != enemysync.AnmAttrLoop {
		return 0
	}
	morf, err := mem.ReadU32(m, mo.Addr+addr.Mo2OffMorf)
	if err != nil || !addr.InMEM1(morf) {
		return 0
	}
	b, err := m.Read(morf+addr.McaMorfOffEnd, 4) // s16 mEnd, s16 mLoop
	if err != nil || len(b) != 4 {
		return 0
	}
	end, loop := int16(binary.BigEndian.Uint16(b)), int16(binary.BigEndian.Uint16(b[2:]))
	if end <= loop {
		return 0
	}
	return float32(end - loop)
}

// rms is the root mean square of the residuals taken (0 with none).
func (r *mirrorResidual) rms() float64 {
	if r.n == 0 {
		return 0
	}
	return math.Sqrt(r.sumSq / float64(r.n))
}

// enemyMirrorLocal runs the loopback over m for d (0: until ctx ends).
// The source is sampled the way the host sampler does (design
// "host_side"): ReadSample, then an enemysync.Tracker derives Bck through
// the Mo2 table (resolved here at start; a lookup miss re-reads it at most
// once per enemysync.MissReread, Mo2Res.Lookup) and AnmSeq/Morf through
// the AnmTrack restart detector, and the slot carries every S2 field
// (Bck, AnmAttr, AnmSeq, Morf, Flags2, BlurShape, BlurScale, MorfFrame,
// MorfRate). EnemyFlagAnim is set only while the table is resolved, as
// the client follows the host's StateFlagAnim: under FLAG_ANIM the REL
// writes the weapon, blur and blur scale from the slot every frame
// (enemy_mirror.c:691-695), so a slot without them would hide the copy's
// spear. colliders sets EnemyFlagColliders (L4-style checks).
func enemyMirrorLocal(ctx context.Context, w io.Writer, m mem.Mem, d time.Duration, colliders bool) error {
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
	// Layout 3: the slot carries dst's own placement key, which the REL
	// recomputes from dst and compares (ERR_KEY on a mismatch), as the
	// client writes the local actor's.
	mo2, _ := enemysync.ParseKinds("mo2")
	ens, err := enemysync.Candidates(m, scope.Stage, scope.Room, mo2)
	if err != nil {
		return err
	}
	var dstKey uint32
	for _, e := range ens {
		if e.PID == dst.PID {
			dstKey = e.Ident.PKey
		}
	}
	if dstKey == 0 {
		return fmt.Errorf("setID 1 (pid %d) is no Moblin candidate: no placement key for it", dst.PID)
	}
	pausePrev, err := mem.ReadU8(m, dst.Addr+addr.ActorOffPauseFlag)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "stage %q room %d; source setID 0 pid %d 0x%08X -> target setID 1 pid %d 0x%08X (pause byte 0x%02X); block at 0x%08X; REL build %08X frame %d\n",
		scope.Stage, scope.Room, src.PID, src.Addr, dst.PID, dst.Addr, pausePrev, mailbox.EnemyBlockAddr, c.REL.BuildID, c.REL.Frames)
	var tr enemysync.Tracker
	if err := tr.Res.Resolve(m); err != nil {
		return err
	}
	animOK := tr.Res.OK()
	fmt.Fprintf(w, "%s; colliders %s\n", mirrorLocalAnimLine(&tr.Res), map[bool]string{true: "on (FLAG_COLLIDERS)", false: "off"}[colliders])

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
		nextLine   = start.Add(mirrorLocalEvery)
		snap       = true
		res        mirrorResidual
		grace      mirrorGrace
		copyErrs   int
	)
	defer func() {
		fmt.Fprintf(w, "summary: %d writes; frame residual over %d samples (copy bck = source bck, after a %d-write grace): rms %.3f max %.3f; copy bck != source bck in %d samples; %d not compared; copy unreadable %d; source anm_seq bumps %d; Mo2 table stats: reads %d bck_miss %d miss_reads %d\n",
			seq, res.n, mirrorLocalGrace, res.rms(), res.max, res.mismatch, res.skipped, copyErrs, grace.bumps, tr.Res.Stats.Reads, tr.Res.Stats.BckMiss, tr.Res.Stats.MissReads)
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
			if err := tr.Derive(m, src.PID, &s, now); err != nil {
				return err
			}
			if ok := tr.Res.OK(); ok != animOK {
				animOK = ok
				fmt.Fprintf(w, "[%5.1fs] %s\n", now.Sub(start).Seconds(), mirrorLocalAnimLine(&tr.Res))
			}
			seq++
			flags := mailbox.EnemyFlagMirror
			if animOK {
				flags |= mailbox.EnemyFlagAnim
			}
			if colliders {
				flags |= mailbox.EnemyFlagColliders
			}
			// SampleSeq is the block seq: one new sample per write, so the
			// REL's per-sample work (the canaries, the S2.5 frame) runs on
			// every write as it does under the client.
			// Layout 3: the slot's proc and dst's own pkey, and the block's
			// kind_mask (the Moblin row only).
			b := &mailbox.EnemyBlock{Magic: mailbox.EnemyMagic, Seq: seq, GoWriteMS: uint32(now.Sub(start).Milliseconds()),
				Flags: flags, Layout: mailbox.EnemyLayout, Epoch: epoch, Stage: scope.Stage, Room: scope.Room, Role: mailbox.EnemyRoleClient,
				KindMask: 1 << enemysync.KindMO2,
				Slots: []mailbox.EnemySlot{{PID: dst.PID, PKey: dstKey, Pos: [3]float32{s.Pos[0] + mirrorLocalOffsetX, s.Pos[1], s.Pos[2]}, AngleY: s.AngleY, Tilt: s.Tilt,
					Proc: s.Proc, AttackType: s.AttackType, AttackPhase: s.AttackPhase, Health: s.Health,
					Flags: s.Flags &^ enemysync.SlotDying, MorfFrame: s.MorfFrame, MorfRate: s.MorfRate, SampleSeq: seq,
					Bck: s.Bck, AnmAttr: s.AnmAttr, AnmSeq: s.AnmSeq, Morf: s.Morf, Flags2: s.Flags2, BlurShape: s.BlurShape, BlurScale: s.BlurScale}}}
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
			grace.note(seq, s.Bck, s.AnmSeq)
			// The copy as the REL left it: its mpMorf+0x54 (mpAnm) through
			// the same table, and its frame.
			cs, cerr := enemysync.ReadSample(m, dst)
			copyOK := cerr == nil && cs.MorfOK
			copyBck := uint8(addr.Mo2BckNone)
			if copyOK {
				copyBck = tr.Res.Index(cs.Anm)
			}
			if cerr != nil {
				copyErrs++
			}
			var period float32
			if copyOK && copyBck == s.Bck {
				period = mirrorLoopPeriod(m, dst, cs.AnmAttr)
			}
			res.add(s.Bck, copyBck, s.MorfFrame, cs.MorfFrame, period, copyOK, grace.graced(seq))
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
				nextLine = now.Add(mirrorLocalEvery)
				fmt.Fprintf(w, "[%5.1fs] seq %d go_write_ms %d ack_seq %d ack_frame %d adopted %04b epoch_seen %d status[0] actor 0x%08X pid %d state %d prev 0x%02X err %d frames %d canaries %+v src %.1f %.1f %.1f\n",
					now.Sub(start).Seconds(), seq, b.GoWriteMS, st.AckSeq, st.AckFrame, st.AdoptedMask, st.EpochSeen,
					st.Status[0].Actor, st.Status[0].PID, st.Status[0].State, st.Status[0].PausePrev, st.Status[0].LastErr, st.Status[0].Frames, st.Canaries,
					s.Pos[0], s.Pos[1], s.Pos[2])
				copyAt := "unread"
				switch {
				case cerr != nil:
					copyAt = "unread (" + cerr.Error() + ")"
				case copyOK:
					copyAt = fmt.Sprintf("anm 0x%08X bck 0x%02X frame %.2f", cs.Anm, copyBck, cs.MorfFrame)
				}
				fmt.Fprintf(w, "        anm: src bck 0x%02X attr %d anm_seq %d morf %d flags2 0x%02X frame %.2f rate %.2f; copy %s; |delta| %s; rel anm_state %d bck 0x%02X anm_changes %d canaries2 %+v; residual rms %.3f max %.3f n %d mismatch %d\n",
					s.Bck, s.AnmAttr, s.AnmSeq, s.Morf, s.Flags2, s.MorfFrame, s.MorfRate, copyAt, mirrorLocalDelta(s.Bck, copyBck, s.MorfFrame, cs.MorfFrame, period, copyOK),
					st.Status[0].AnmState, st.Status[0].Bck, st.Status[0].AnmChanges, st.Canaries2, res.rms(), res.max, res.n, res.mismatch)
			}
		}
	}
}

// mirrorLocalAnimLine says whether the animation stream is on.
func mirrorLocalAnimLine(r *enemysync.Mo2Res) string {
	if r.OK() {
		entry, res := r.Entry()
		return fmt.Sprintf("Mo2 table resolved (mObjectInfo[%d], mRes 0x%08X): FLAG_ANIM on, the S2 fields are streamed", entry, res)
	}
	return fmt.Sprintf("Mo2 table NOT resolved (%s): FLAG_ANIM off, the copy stays on S1 (bck 0xFF still sent)", r.Why())
}

// mirrorLocalDelta prints the copy's |frame - source frame| (folded over
// a LOOP period), or why it does not compare.
func mirrorLocalDelta(srcBck, copyBck uint8, srcFrame, copyFrame, period float32, copyOK bool) string {
	switch {
	case !copyOK:
		return "n/a (copy unread)"
	case srcBck == addr.Mo2BckNone:
		return "n/a (source bck unresolved)"
	case copyBck != srcBck:
		return "n/a (bck differs)"
	}
	return fmt.Sprintf("%.2f", mirrorFrameDelta(srcFrame, copyFrame, period))
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
	fmt.Fprintf(w, "Go span: magic 0x%08X %s seq_head %d seq_tail %d%s host_beat %d go_write_ms %d count %d flags 0x%02X layout %d epoch %d stage %q room %d role %d kind_mask 0x%X\n",
		blk.Magic, magic, blk.Seq, tail, torn, blk.HostBeat, blk.GoWriteMS, len(blk.Slots), blk.Flags, blk.Layout, blk.Epoch, blk.Stage, blk.Room, blk.Role, blk.KindMask)
	for i, s := range blk.Slots {
		fmt.Fprintf(w, "  slot %d: pid %d proc 0x%04X pkey 0x%08X pos %.1f %.1f %.1f angle %d tilt %d attack %d/%d health %d flags 0x%02X frame %.2f rate %.2f sample %d bck 0x%02X attr %d anm_seq %d morf %d flags2 0x%02X blur %d/%.2f\n",
			i, s.PID, s.Proc, s.PKey, s.Pos[0], s.Pos[1], s.Pos[2], s.AngleY, s.Tilt, s.AttackType, s.AttackPhase, s.Health, s.Flags, s.MorfFrame, s.MorfRate, s.SampleSeq,
			s.Bck, s.AnmAttr, s.AnmSeq, s.Morf, s.Flags2, s.BlurShape, s.BlurScale)
	}
	ax, axSeq, err := mailbox.ReadEnemyAnnex(m)
	if err != nil {
		return err
	}
	stale := ""
	if axSeq != blk.Seq {
		stale = " (STALE: != seq_head; the REL ignores it)"
	}
	fmt.Fprintf(w, "annex at 0x%08X: annex_seq %d%s aux %v override %+v invuln_wait %v\n", mailbox.EnemyAnnexAddr, axSeq, stale, ax.Aux, ax.Override, ax.InvulnWait)
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
