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

// stagedProbeRoom is M_Dai with Link in room 1 and the allow batch's A0
// cases placed: 0 KI setID 5, 1 an unkeyed KI params 0x01, 2 an unkeyed
// KI params 0x02 (refused by the params gate), 3 AM setID 6, 4 AM2
// setID 7, 5 BK setID 0.
func stagedProbeRoom(t *testing.T) (*mem.FakeMem, *fakegame.Enemies) {
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

// TestEnemyMatchDryStaged: enemy-match lists the staged kinds' idents
// beside the rows' (sorted by proc), says so on the scope line, and
// notes the keyed Keese's home.pos; --kinds splits staged names off.
func TestEnemyMatchDryStaged(t *testing.T) {
	m, _ := stagedProbeRoom(t)
	var out bytes.Buffer
	if err := enemyMatch(&out, m, enemysync.AllKinds(), allStaged()...); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{
		" walk=complete staged=ki,am,am2\n",
		"ident kind=bk proc=0x00BE setID=0x0000 ",
		"ident kind=am proc=0x00CB setID=0x0006 ", " verdict=ok pid=", " am_action=0\n",
		"ident kind=am2 proc=0x00CC setID=0x0007 ", " am2_action=0\n",
		"ident kind=ki proc=0x00D8 setID=0x0005 ", " ki_action=0\n",
		"ident kind=ki proc=0x00D8 setID=0xFFFF ", "params=0xFFFFFF02 ", "verdict=params-gate",
		"note kind=ki keyed pkey hashes home.pos",
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

	// Without staged kinds the output is the rows' alone (no scope suffix,
	// no note).
	out.Reset()
	if err := enemyMatch(&out, m, enemysync.AllKinds()); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); strings.Contains(s, "staged=") || strings.Contains(s, "kind=ki") || !strings.Contains(s, "summary idents=1 ") {
		t.Errorf("rows only:\n%s", s)
	}

	set, staged, err := splitStagedKinds("bk+u, ki,am2+u")
	if err != nil || set.String() != "bk+u" || len(staged) != 2 || staged[0] != enemysync.KindKI || staged[1] != enemysync.KindAM2 {
		t.Errorf("splitStagedKinds: %v %v %v", set, staged, err)
	}
	if set, staged, err := splitStagedKinds("am"); err != nil || set.Mask() != 0 || len(staged) != 1 {
		t.Errorf("staged alone enables a row: %v %v %v", set, staged, err)
	}
	if _, _, err := splitStagedKinds("ki,ki"); err == nil {
		t.Error("ki twice accepted")
	}
	if _, _, err := splitStagedKinds("ki,xx"); err == nil {
		t.Error("an unknown kind accepted")
	}
}

// TestEnemyKindWatchStaged: enemy-watch --kind ki and am2 resolve their
// own archive tables, sample through ReadSampleKI / AM2 and print the A0
// fields.
func TestEnemyKindWatchStaged(t *testing.T) {
	m, es := stagedProbeRoom(t)
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
		"gate params-gate ki_action=0",
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
	// The staged scale.x has its own key: one " scale " per tick line.
	for _, line := range strings.Split(s, "\n") {
		if n := strings.Count(line, " scale "); strings.Contains(line, " act_scale ") && n != 1 {
			t.Errorf("am2 tick line has %d \" scale \" keys: %s", n, line)
		}
	}
}
