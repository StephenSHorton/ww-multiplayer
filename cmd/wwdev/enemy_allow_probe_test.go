package main

import (
	"bytes"
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/enemysync"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// allowProbeRoom is M_Dai with Link in room 1 and the allow batch's
// cases placed: 0 KI setID 5, 1 an unkeyed KI params 0x01, 2 an unkeyed
// KI params 0x02 (refused by the params gate), 3 AM setID 6, 4 AM2
// setID 7, 5 BK setID 0.
func allowProbeRoom(t *testing.T) (*mem.FakeMem, *fakegame.Enemies) {
	t.Helper()
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	if err := g.LoadStage(mDai); err != nil {
		t.Fatal(err)
	}
	if err := g.EnterRoom(1, 2); err != nil {
		t.Fatal(err)
	}
	es := fakegame.PlaceEnemies(
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 5, Proc: stage.ProcKI, Params: 0xFFFFFF01},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: addr.ActorSetIDNone, Proc: stage.ProcKI, Params: 0xFFFFFF01},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: addr.ActorSetIDNone, Proc: stage.ProcKI, Params: 0xFFFFFF02},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 6, Proc: stage.ProcAM, Params: 0xFFFFFFFF},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 7, Proc: stage.ProcAM2, Params: 0xFFFFFFFF},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 0, Proc: stage.ProcBK, Params: 0xFFFF8784},
	)
	for range 4 {
		if err := g.FrameWith(es); err != nil {
			t.Fatal(err)
		}
	}
	return m, es
}

// TestEnemyMatchDryAllow: enemy-match lists the allow kinds' idents
// beside the others' (sorted by Ident) with their extra live gate bytes;
// --kinds names them like any row.
func TestEnemyMatchDryAllow(t *testing.T) {
	m, _ := allowProbeRoom(t)
	var out bytes.Buffer
	if err := enemyMatch(&out, m, enemysync.AllKinds()); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{
		" kinds=mo2+u,bk+u,cc+u,ki+u,am+u,am2+u walk=complete\n",
		"ident kind=bk proc=0x00BE setID=0x0000 ",
		"ident kind=am proc=0x00CB setID=0x0006 ", " verdict=ok pid=", " am_action=0 am_mode=0\n",
		"ident kind=am2 proc=0x00CC setID=0x0007 ", " am2_action=0 am2_mode=0 am2_abyss=0 am2_carried=0\n",
		"ident kind=ki proc=0x00D8 setID=0x0005 ", " ki_action=0 ki_m2dc=0\n",
		"ident kind=ki proc=0x00D8 setID=0xFFFF ", "params=0xFFFFFF02 ", "verdict=params-gate",
		"summary idents=6 taken=5 refused=1\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	var procs []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "ident ") {
			procs = append(procs, strings.Fields(l)[2])
		}
	}
	if strings.Join(procs, " ") != "proc=0x00BE proc=0x00CB proc=0x00CC proc=0x00D8 proc=0x00D8 proc=0x00D8" {
		t.Errorf("ident order %v", procs)
	}

	// Without the allow kinds the output is the others' alone (no note).
	out.Reset()
	bk, err := enemysync.ParseKinds("mo2+u,bk+u,cc+u")
	if err != nil {
		t.Fatal(err)
	}
	if err := enemyMatch(&out, m, bk); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); strings.Contains(s, "kind=ki") || strings.Contains(s, "note ") || !strings.Contains(s, "summary idents=1 ") {
		t.Errorf("no allow kinds:\n%s", s)
	}
}

// TestEnemyKindWatchAllow: enemy-watch --kind ki and am2 resolve their
// archive tables, sample through ReadKindSample (ReadSampleKI / AM2) and
// print the A0 fields.
func TestEnemyKindWatchAllow(t *testing.T) {
	m, es := allowProbeRoom(t)
	const morf = uint32(0x80C00000)
	placeKindTable(t, m, 32, addr.KiArc, 0x80E80000, addr.KiBckFirst, addr.KiBckLast, 0x80E90000)
	placeKindTable(t, m, 33, addr.Am2Arc, 0x80EC0000, addr.Am2BckFirst, addr.Am2BckLast, 0x80ED0000)
	ki, _ := es.Of(0)
	store(t, m, ki.Addr+addr.KiOffMorf, u32b(morf))
	store(t, m, morf+addr.McaMorfOffAnm, u32b(0x80E90000+0x100*addr.KiBckWait))
	store(t, m, ki.Addr+addr.ActorOffHealth, []byte{1})
	store(t, m, ki.Addr+addr.KiOffAction, []byte{addr.KiActAttack, 0, 3})
	store(t, m, ki.Addr+addr.ActorOffShapeAngle+2, []byte{0x40, 0x00})
	store(t, m, ki.Addr+addr.KiOffTgSph+addr.CcShapeOffR, u32b(math.Float32bits(20)))

	ctx := context.Background()
	var out bytes.Buffer
	if err := enemyKindWatch(ctx, &out, m, 150*time.Millisecond, enemysync.KindKI); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	s := out.String()
	for _, want := range []string{
		`kind ki proc 0x00D8 arc "Ki" bck 0x0F..0x14 morf +0x2B4 ice +0x924 gate +0x2D1`,
		"Ki table: resolved, mObjectInfo[32]",
		"3 ki in room 1 of \"M_Dai\", 2 taken:",
		"gate params-gate ki_action=0 ki_m2dc=0",
		"setID 0x0005 pid ", "ang 16384 ", "mode 3 action 3", "flags2 0x04 [LUNGE]", "bck 0x14 WAIT1",
		"cur 0/", "shape 0/16384/0 ", "beh 3 ", "| at+0x580 ", "| tg+0x7D8 at/tg/co ", " r 20.0",
		"Ki table stats: reads ",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("ki output lacks %q:\n%s", want, s)
		}
	}

	am2, _ := es.Of(4)
	store(t, m, am2.Addr+addr.Am2OffMorf, u32b(morf))
	store(t, m, morf+addr.McaMorfOffAnm, u32b(0x80ED0000+0x100*0x0C))
	store(t, m, am2.Addr+addr.Am2OffCurrBckIdx, u32b(0x0C))
	store(t, m, am2.Addr+addr.Am2OffWeakSph+addr.CcCylOffTgSPrm, u32b(1))
	out.Reset()
	if err := enemyKindWatch(ctx, &out, m, 150*time.Millisecond, enemysync.KindAM2); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	s = out.String()
	for _, want := range []string{
		"AM2 table: resolved, mObjectInfo[33]",
		"1 am2 in room 1 of \"M_Dai\", 1 taken:",
		"bck 0x0C MAHI", "bckidx 0x0C MAHI", "WEAK_TG", "| body+0x5A0 ", " h 0.0", "| weak+0x92C at/tg/co 0x0/0x1/0x0",
		" act_scale ",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("am2 output lacks %q:\n%s", want, s)
		}
	}
	// The allow kinds' scale.x has its own key: one " scale " per tick line.
	for _, line := range strings.Split(s, "\n") {
		if n := strings.Count(line, " scale "); strings.Contains(line, " act_scale ") && n != 1 {
			t.Errorf("am2 tick line has %d \" scale \" keys: %s", n, line)
		}
	}
}
