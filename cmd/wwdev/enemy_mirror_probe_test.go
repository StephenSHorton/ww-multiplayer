package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/mods/enemysync"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// standInREL acks whatever seq is in the block every 10 ms, a little
// later than the write, as the REL would, and copies st0's non-zero bytes
// into status[0].
func standInREL(ctx context.Context, m *mem.FakeMem, st0 []byte) {
	go func() {
		t := time.NewTicker(10 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				seq, _ := mem.ReadU32(m, mailbox.EnemyBlockAddr+mailbox.EnemySeqHeadOff)
				if seq != 0 {
					m.Store(mailbox.EnemyBlockAddr+mailbox.EnemyAckSeqOff, binary.BigEndian.AppendUint32(nil, seq))
					m.Store(mailbox.EnemyBlockAddr+mailbox.EnemyAckFrameOff, binary.BigEndian.AppendUint32(nil, 500))
					m.Store(mailbox.EnemyBlockAddr+mailbox.EnemyStatusOff+mailbox.EnemySTPausePrevOff, []byte{0x04})
					for off, v := range st0 {
						if v != 0 {
							m.Store(mailbox.EnemyBlockAddr+mailbox.EnemyStatusOff+uint32(off), []byte{v})
						}
					}
				}
			}
		}
	}()
}

// mirrorLocalBlocks decodes the recorded writes: the block writes (each
// checked to name setID 1's pid at setID 0's position 150 units along X,
// role client, with SampleSeq the block seq) and the last magic written.
func mirrorLocalBlocks(t *testing.T, m *mem.FakeMem, dstPID uint32) ([]mailbox.EnemyBlock, uint32) {
	t.Helper()
	var blocks []mailbox.EnemyBlock
	var lastMagic uint32 = 1
	for _, w := range m.Writes() {
		switch {
		case w.Addr == mailbox.EnemyBlockAddr && len(w.Data) == mailbox.EnemyGoSpan:
			blk, tail, err := mailbox.DecodeEnemyBlock(w.Data)
			if err != nil || tail != blk.Seq || blk.Role != mailbox.EnemyRoleClient || len(blk.Slots) != 1 || blk.Slots[0].PID != dstPID ||
				blk.Slots[0].Pos != [3]float32{-1500.5 + mirrorLocalOffsetX, 250, 3000} || blk.Stage != "M_Dai" || blk.Room != 1 || blk.Slots[0].SampleSeq != blk.Seq {
				t.Fatalf("block: %+v tail %d %v", blk, tail, err)
			}
			blocks = append(blocks, blk)
			lastMagic = blk.Magic
		case w.Addr == mailbox.EnemyBlockAddr && len(w.Data) == 4:
			lastMagic = binary.BigEndian.Uint32(w.Data)
		default:
			t.Errorf("stray write 0x%08X+%d", w.Addr, len(w.Data))
		}
	}
	return blocks, lastMagic
}

// TestEnemyMirrorLocal: the loopback writes blocks naming setID 1's pid
// with setID 0's position 150 units along X, reports the first consume
// once the REL (played here by the test) acks, and clears the magic on
// stop, checking the pause byte. Without a Mo2 table it leaves FLAG_ANIM
// off (the copy stays on S1) and says so.
func TestEnemyMirrorLocal(t *testing.T) {
	_, m, es := moblinRoom(t)
	if err := fakegame.Boot(m); err != nil {
		t.Fatal(err)
	}
	if err := mailbox.WriteREL(m, mailbox.REL{Linked: true, BuildID: inject.RELBuildID, Frames: 5}); err != nil {
		t.Fatal(err)
	}
	dst, _ := es.Of(2) // setID 1
	store(t, m, dst.Addr+addr.ActorOffPauseFlag, []byte{0x04})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	standInREL(ctx, m, nil)
	m.Record(true)
	var out bytes.Buffer
	if err := enemyMirrorLocal(ctx, &out, m, 120*time.Millisecond, false); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	s := out.String()
	for _, want := range []string{
		"source setID 0 pid", "target setID 1 pid", "(pause byte 0x04)",
		"Mo2 table NOT resolved (no loaded Mo2 entry in mObjectInfo): FLAG_ANIM off", "colliders off",
		"first consume: seq", "ack_frame 500",
		"frame residual over 0 samples", "copy bck != source bck in 0 samples", "Mo2 table stats: reads ",
		"stopped: magic 0 written; status[0] state 0 pause_prev 0x04; setID 1's pause byte now 0x04 (was 0x04): restored",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	// Every sample missed the table, and a miss re-reads it (as the host does).
	if strings.Contains(s, "miss_reads 0") || strings.Contains(s, "bck_miss 0 ") {
		t.Errorf("no miss re-read:\n%s", s)
	}
	blocks, lastMagic := mirrorLocalBlocks(t, m, dst.PID)
	if len(blocks) < 3 || lastMagic != 0 {
		t.Fatalf("%d blocks, last magic 0x%08X", len(blocks), lastMagic)
	}
	if blocks[0].Slots[0].Flags&mailbox.EnemySFSnap == 0 || blocks[1].Slots[0].Flags&mailbox.EnemySFSnap != 0 {
		t.Error("only the first block snaps")
	}
	for _, b := range blocks {
		if b.Flags != mailbox.EnemyFlagMirror || b.Slots[0].Bck != addr.Mo2BckNone {
			t.Fatalf("no table: flags 0x%02X bck 0x%02X", b.Flags, b.Slots[0].Bck)
		}
	}

	// Without the REL linked it refuses before writing.
	if err := mailbox.WriteREL(m, mailbox.REL{}); err != nil {
		t.Fatal(err)
	}
	if err := enemyMirrorLocal(ctx, &out, m, time.Millisecond, false); err == nil || !strings.Contains(err.Error(), "REL module is not linked") {
		t.Errorf("unlinked: %v", err)
	}
}

// TestEnemyMirrorLocalS2: with the Mo2 table resolved the loopback
// streams the S2 fields the host derives (bck through the table, attr,
// anm_seq, morf from mMorfStep, flags2, blur) under FLAG_ANIM, adds
// FLAG_COLLIDERS on request, and prints the copy's bck (its mpMorf+0x54
// through the same table) and frame against the source's, the REL's
// animation status and the residual summary. A copy on another bck counts
// as a mismatch and not in the residual.
func TestEnemyMirrorLocalS2(t *testing.T) {
	old := mirrorLocalEvery
	mirrorLocalEvery = 30 * time.Millisecond
	defer func() { mirrorLocalEvery = old }()
	for _, tc := range []struct {
		name     string
		copyBck  int
		want     []string
		mismatch bool
	}{
		{"same bck", addr.Mo2BckWait, []string{"|delta| 0.25;", "max 0.250; copy bck != source bck in 0 samples"}, false},
		{"other bck", addr.Mo2BckFirst, []string{"|delta| n/a (bck differs)", "frame residual over 0 samples"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, m, es := moblinRoom(t)
			if err := fakegame.Boot(m); err != nil {
				t.Fatal(err)
			}
			if err := mailbox.WriteREL(m, mailbox.REL{Linked: true, BuildID: inject.RELBuildID, Frames: 5}); err != nil {
				t.Fatal(err)
			}
			src, _ := es.Of(1) // setID 0
			dst, _ := es.Of(2) // setID 1
			store(t, m, dst.Addr+addr.ActorOffPauseFlag, []byte{0x04})
			placeMo2Table(t, m)
			// setID 0 plays WAIT (0x73, a loop) set with morf 15 at frame
			// 12.25, its spear drawn, its blur on with shape 3 at half scale.
			const morf, copyMorf = uint32(0x80C00000), uint32(0x80C01000)
			store(t, m, morf+addr.McaMorfOffAnm, u32b(mo2Bck(addr.Mo2BckWait)))
			store(t, m, morf+addr.McaMorfOffCurMorf, u32b(math.Float32bits(0)))
			store(t, m, morf+addr.McaMorfOffMorfStep, u32b(math.Float32bits(1.0/15)))
			store(t, m, src.Addr+addr.Mo2OffHasWeapon, []byte{1})
			store(t, m, src.Addr+addr.Mo2OffBlurScale, u32b(math.Float32bits(0.5)))
			store(t, m, src.Addr+addr.Mo2OffBlurOn, u32b(1))
			store(t, m, src.Addr+addr.Mo2OffBlurShape, u32b(3))
			// The copy sits at frame 12.0 of tc.copyBck.
			store(t, m, dst.Addr+addr.Mo2OffMorf, u32b(copyMorf))
			store(t, m, copyMorf+addr.McaMorfOffAnm, u32b(mo2Bck(tc.copyBck)))
			store(t, m, copyMorf+addr.McaMorfOffAttr, []byte{2})
			store(t, m, copyMorf+addr.McaMorfOffFrame, u32b(math.Float32bits(12)))
			store(t, m, copyMorf+addr.McaMorfOffEnd, []byte{0, 60, 0, 0}) // a 60-frame loop
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			st0 := make([]byte, mailbox.EnemyStatusSize)
			st0[mailbox.EnemySTAnmStateOff] = mailbox.EnemyAnmSynced
			st0[mailbox.EnemySTBckOff] = addr.Mo2BckWait
			st0[mailbox.EnemySTAnmChangesOff] = 3
			standInREL(ctx, m, st0)
			m.Record(true)
			var out bytes.Buffer
			if err := enemyMirrorLocal(ctx, &out, m, 150*time.Millisecond, true); err != nil {
				t.Fatalf("%v\n%s", err, out.String())
			}
			s := out.String()
			for _, want := range append([]string{
				"Mo2 table resolved (mObjectInfo[28], mRes 0x80D00000): FLAG_ANIM on", "colliders on (FLAG_COLLIDERS)",
				"anm: src bck 0x73 attr 2 anm_seq 0 morf 15 flags2 0x05 frame 12.25 rate 1.50; copy anm 0x",
				fmt.Sprintf("bck 0x%02X frame 12.00", tc.copyBck),
				"rel anm_state 1 bck 0x73 anm_changes 3 canaries2 {",
				"summary: ", "rms ", "copy unreadable 0", "source anm_seq bumps 0;",
			}, tc.want...) {
				if !strings.Contains(s, want) {
					t.Errorf("output lacks %q:\n%s", want, s)
				}
			}
			if tc.mismatch && strings.Contains(s, "copy bck != source bck in 0 samples") {
				t.Errorf("a copy on another bck counted no mismatch:\n%s", s)
			}
			blocks, lastMagic := mirrorLocalBlocks(t, m, dst.PID)
			if len(blocks) < 3 || lastMagic != 0 {
				t.Fatalf("%d blocks, last magic 0x%08X", len(blocks), lastMagic)
			}
			for _, b := range blocks {
				sl := b.Slots[0]
				if b.Flags != mailbox.EnemyFlagMirror|mailbox.EnemyFlagAnim|mailbox.EnemyFlagColliders || sl.Bck != addr.Mo2BckWait || sl.AnmAttr != 2 || sl.AnmSeq != 0 ||
					sl.Morf != 15 || sl.Flags2 != 0x05 || sl.BlurShape != 3 || sl.BlurScale != 0.5 || sl.MorfFrame != 12.25 || sl.MorfRate != 1.5 {
					t.Fatalf("S2 block: flags 0x%02X slot %+v", b.Flags, sl)
				}
			}
		})
	}
}

// TestMirrorResidual: the residual takes only graced samples of a
// resolved source whose copy is on the same bck; a differing bck is a
// mismatch; a LOOP period folds a wrap the copy has not taken yet.
func TestMirrorResidual(t *testing.T) {
	var r mirrorResidual
	r.add(0x73, 0x73, 10, 13, 0, true, false)                    // in grace
	r.add(addr.Mo2BckNone, addr.Mo2BckNone, 0, 0, 0, true, true) // source unresolved
	r.add(0x73, 0x73, 10, 13, 0, false, true)                    // copy unread
	r.add(0x73, 0x61, 10, 13, 0, true, true)                     // mismatch
	r.add(0x73, 0x73, 10, 13, 0, true, true)
	r.add(0x73, 0x73, 10, 6, 0, true, true)
	if r.n != 2 || r.skipped != 3 || r.mismatch != 1 || r.max != 4 || math.Abs(r.rms()-math.Sqrt(12.5)) > 1e-9 {
		t.Errorf("residual %+v rms %v", r, r.rms())
	}
	r.add(0x73, 0x73, 0.5, 59.5, 60, true, true) // the source wrapped, the copy not yet
	if r.n != 3 || r.max != 4 {
		t.Errorf("wrap not folded: %+v", r)
	}
	if (&mirrorResidual{}).rms() != 0 {
		t.Error("empty rms")
	}
	for _, c := range []struct {
		src, cp, period float32
		want            float64
	}{
		{0.5, 59.5, 60, 1}, {59.5, 0.5, 60, 1}, {10, 40, 60, 30}, {10, 12, 60, 2}, {0.5, 59.5, 0, 59}, {1, 3, 0, 2},
	} {
		if got := mirrorFrameDelta(c.src, c.cp, c.period); math.Abs(got-c.want) > 1e-6 {
			t.Errorf("mirrorFrameDelta(%v, %v, %v) = %v, want %v", c.src, c.cp, c.period, got, c.want)
		}
	}
}

// TestMirrorGrace: a bck change and a same-bck restart (anm_seq bump,
// NAJAB's phases on 0x61) both restart the grace; bumps counts anm_seq
// changes only.
func TestMirrorGrace(t *testing.T) {
	var g mirrorGrace
	if g.graced(1) {
		t.Error("graced before any write")
	}
	g.note(1, 0x73, 0)
	g.note(2, 0x73, 0)
	if g.graced(2) {
		t.Error("graced after one more write")
	}
	g.note(3, 0x73, 0)
	if !g.graced(3) {
		t.Error("not graced after two more writes")
	}
	g.note(4, 0x61, 0) // bck change
	if g.graced(4) || g.bumps != 0 {
		t.Errorf("bck change: graced %v bumps %d", g.graced(4), g.bumps)
	}
	g.note(5, 0x61, 0)
	g.note(6, 0x61, 0)
	g.note(7, 0x61, 1) // same-bck restart
	if g.graced(7) || g.bumps != 1 {
		t.Errorf("restart: graced %v bumps %d", g.graced(7), g.bumps)
	}
	g.note(8, 0x61, 1)
	g.note(9, 0x61, 1)
	if !g.graced(9) {
		t.Error("not graced two writes after the restart")
	}
}

// TestMirrorLoopPeriod reads mEnd - mLoop of a LOOP morf and nothing for
// another attribute or a bad morf.
func TestMirrorLoopPeriod(t *testing.T) {
	m := mem.NewFakeMem()
	mo := &enemysync.Moblin{Addr: 0x80B00000}
	const morf = uint32(0x80C00000)
	store(t, m, mo.Addr+addr.Mo2OffMorf, u32b(morf))
	store(t, m, morf+addr.McaMorfOffEnd, []byte{0, 60, 0, 4}) // mEnd 60, mLoop 4
	if p := mirrorLoopPeriod(m, mo, enemysync.AnmAttrLoop); p != 56 {
		t.Errorf("LOOP period %v, want 56", p)
	}
	if p := mirrorLoopPeriod(m, mo, enemysync.AnmAttrLoopReverse); p != 0 {
		t.Errorf("LOOP_REVERSE period %v, want 0", p)
	}
	store(t, m, mo.Addr+addr.Mo2OffMorf, u32b(0))
	if p := mirrorLoopPeriod(m, mo, enemysync.AnmAttrLoop); p != 0 {
		t.Errorf("null morf period %v, want 0", p)
	}
}

// TestEnemyBlockDump prints every field of a written block and the REL's
// tail.
func TestEnemyBlockDump(t *testing.T) {
	m := mem.NewFakeMem()
	blk := &mailbox.EnemyBlock{Magic: mailbox.EnemyMagic, Seq: 42, HostBeat: 7, GoWriteMS: 99, Flags: 0x05, Layout: 2, Epoch: 3, Stage: "M_Dai", Room: 1, Role: 2,
		Slots: []mailbox.EnemySlot{{PID: 1002, Pos: [3]float32{1, 2, 3}, Health: 4, Flags: 0x03, MorfFrame: 1.5, MorfRate: 1, SampleSeq: 9,
			Bck: 0x73, AnmAttr: 2, AnmSeq: 5, Morf: 15, Flags2: 0x05, BlurShape: 3, BlurScale: 1.25}},
		HitApply: mailbox.EnemyHitApply{Seq: 0x30001, SetID: 2, Collider: 1, Flags: 0x02, AtType: 2, Atp: 2, Se: 1, HitMark: 0xD, Spl: 1, PID: 620}}
	if err := mailbox.WriteEnemyBlock(m, blk); err != nil {
		t.Fatal(err)
	}
	store(t, m, mailbox.EnemyBlockAddr+mailbox.EnemyAckSeqOff, u32b(41))
	store(t, m, mailbox.EnemyBlockAddr+mailbox.EnemyCanariesOff, []byte{1, 2, 3, 4})
	store(t, m, mailbox.EnemyBlockAddr+mailbox.EnemyCanaries2Off, []byte{5, 6, 7, 8})
	store(t, m, mailbox.EnemyBlockAddr+mailbox.EnemyHitApplyAckOff, u32b(0x30001))
	store(t, m, mailbox.EnemyBlockAddr+mailbox.EnemyHitResultOff, []byte{1, 0, 1, 1})
	store(t, m, mailbox.EnemyBlockAddr+mailbox.EnemyStatusOff+mailbox.EnemyStatusSize+mailbox.EnemySTAnmStateOff, []byte{1})
	store(t, m, mailbox.EnemyBlockAddr+mailbox.EnemyStatusOff+mailbox.EnemyStatusSize+mailbox.EnemySTBckOff, []byte{0x73, 9})
	var out bytes.Buffer
	if err := enemyBlockDump(&out, m); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"enemy block at 0x80413100 (0x198 bytes)",
		"magic 0x5757454E 'WWEN' seq_head 42 seq_tail 42 host_beat 7 go_write_ms 99 count 1 flags 0x05 layout 2 epoch 3 stage \"M_Dai\" room 1 role 2",
		"slot 0: pid 1002 pos 1.0 2.0 3.0", "health 4 flags 0x03 frame 1.50 rate 1.00 sample 9 bck 0x73 attr 2 anm_seq 5 morf 15 flags2 0x05 blur 3/1.25",
		"hit_apply: seq 196609 setID 2 collider 1 flags 0x02 atType 0x00000002 atp 2 se 1 hit_mark 13 spl 1 pos 0.0 0.0 0.0 pid 620",
		"hit_apply_ack 196609 result 1 detail 0 frames 1 sets 1", "canaries2 {5 6 7 8}",
		"status 1: actor 0x00000000 pid 0 state 0 pause_prev 0x00 last_err 0 frames 0 anm_state 1 bck 0x73 anm_changes 9;",
		"ack_seq 41", "canaries pause_reasserts 1 anm_unresolved 2 revalidate_failures 3 watchdog_releases 4",
		"status 3: actor 0x00000000",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dump lacks %q:\n%s", want, out.String())
		}
	}
	store(t, m, mailbox.EnemyBlockAddr+mailbox.EnemySeqTailOff, u32b(41))
	out.Reset()
	enemyBlockDump(&out, m)
	if !strings.Contains(out.String(), "(TORN: head != tail)") {
		t.Errorf("torn block not flagged:\n%s", out.String())
	}
}
