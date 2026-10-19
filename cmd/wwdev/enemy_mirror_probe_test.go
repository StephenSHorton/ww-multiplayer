package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// TestEnemyMirrorLocal: the loopback writes blocks naming setID 1's pid
// with setID 0's position 150 units along X, reports the first consume
// once the REL (played here by the test) acks, and clears the magic on
// stop, checking the pause byte.
func TestEnemyMirrorLocal(t *testing.T) {
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
	// A stand-in REL: ack whatever seq is in the block, a little later.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
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
				}
			}
		}
	}()
	m.Record(true)
	var out bytes.Buffer
	if err := enemyMirrorLocal(ctx, &out, m, 120*time.Millisecond); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	s := out.String()
	for _, want := range []string{
		"source setID 0 pid", "target setID 1 pid", "(pause byte 0x04)",
		"first consume: seq", "ack_frame 500",
		"stopped: magic 0 written; status[0] state 0 pause_prev 0x04; setID 1's pause byte now 0x04 (was 0x04): restored",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	var blocks int
	var lastMagic uint32 = 1
	for _, w := range m.Writes() {
		switch {
		case w.Addr == mailbox.EnemyBlockAddr && len(w.Data) == mailbox.EnemyGoSpan:
			blocks++
			blk, tail, err := mailbox.DecodeEnemyBlock(w.Data)
			if err != nil || tail != blk.Seq || blk.Role != mailbox.EnemyRoleClient || len(blk.Slots) != 1 || blk.Slots[0].PID != dst.PID ||
				blk.Slots[0].Pos != [3]float32{-1500.5 + mirrorLocalOffsetX, 250, 3000} || blk.Stage != "M_Dai" || blk.Room != 1 {
				t.Fatalf("block: %+v tail %d %v", blk, tail, err)
			}
			if blocks == 1 && blk.Slots[0].Flags&mailbox.EnemySFSnap == 0 {
				t.Error("the first block does not snap")
			}
			lastMagic = blk.Magic
		case w.Addr == mailbox.EnemyBlockAddr && len(w.Data) == 4:
			lastMagic = binary.BigEndian.Uint32(w.Data)
		default:
			t.Errorf("stray write 0x%08X+%d", w.Addr, len(w.Data))
		}
	}
	if blocks < 3 || lastMagic != 0 {
		t.Errorf("%d blocks, last magic 0x%08X", blocks, lastMagic)
	}
	_ = src

	// Without the REL linked it refuses before writing.
	if err := mailbox.WriteREL(m, mailbox.REL{}); err != nil {
		t.Fatal(err)
	}
	if err := enemyMirrorLocal(ctx, &out, m, time.Millisecond); err == nil || !strings.Contains(err.Error(), "REL module is not linked") {
		t.Errorf("unlinked: %v", err)
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
