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

// batchCProbeRoom is M_Dai with Link in room 1 and batch C's probe cases
// placed: 0 BB setID 0 (perch 4), 1 BB setID 2 (perch 7), 2 a rider BB
// setID 5 (type 5, refused by params), 3 an unkeyed MT.
func batchCProbeRoom(t *testing.T) (*mem.FakeMem, *fakegame.Enemies) {
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
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 0, Proc: stage.ProcBB, Params: 0xFFFFFF04},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 2, Proc: stage.ProcBB, Params: 0xFFFF0507},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 5, Proc: stage.ProcBB, Params: 0xE000FF05},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: addr.ActorSetIDNone, Proc: stage.ProcMT, Params: 0xFF02FF00},
	)
	for range 4 {
		if err := g.FrameWith(es); err != nil {
			t.Fatal(err)
		}
	}
	for i, typ := range []byte{4, 7, 5} {
		bb, _ := es.Of(i)
		store(t, m, bb.Addr+addr.BbOffType, []byte{typ})
	}
	return m, es
}

// TestEnemyMatchDryBatchC: enemy-match lists the Kargarocs and the
// Magtail as rows with their live gate bytes after " pid="; the rider is
// refused by its params, a knocked-back perch by the live gate.
func TestEnemyMatchDryBatchC(t *testing.T) {
	m, es := batchCProbeRoom(t)
	var out bytes.Buffer
	if err := enemyMatch(&out, m, enemysync.AllKinds()); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{
		"ident kind=bb proc=0x00B6 setID=0x0000 ", " bb_type=4 bb_m2df=0 bb_knock=0.00 bb_live=ok\n",
		"ident kind=bb proc=0x00B6 setID=0x0002 ", " bb_type=7 ",
		"ident kind=bb proc=0x00B6 setID=0x0005 ", "params=0xE000FF05 ", "verdict=params-gate pid=", " bb_type=5 bb_m2df=0 bb_knock=0.00 bb_live=rider\n",
		"ident kind=mt proc=0x00D9 setID=0xFFFF ", " mt_mode=0 mt_appear=0 mt_death=0 mt_killed=0 mt_ice=0 mt_live=ok\n",
		"summary idents=4 taken=3 refused=1\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	bb, _ := es.Of(0)
	store(t, m, bb.Addr+addr.BbOffKnock, u32b(math.Float32bits(45)))
	set, _ := enemysync.ParseKinds("bb,mt")
	out.Reset()
	if err := enemyMatch(&out, m, set); err != nil {
		t.Fatal(err)
	}
	s = out.String()
	if !strings.Contains(s, " kinds=bb,mt walk=complete\n") || !strings.Contains(s, " bb_knock=45.00 bb_live=knock\n") ||
		!strings.Contains(s, "kind=mt proc=0x00D9 setID=0xFFFF ") || !strings.Contains(s, "verdict=unkeyed-off") {
		t.Errorf("--kinds bb,mt with a knockback:\n%s", s)
	}
}

// TestEnemyKindWatchBB: enemy-watch --kind bb prints P0-BB's walk line
// (the execute, tail_control below it, the fingerprint words and fp=ok;
// fp=miss once a word differs) and its tick fields.
func TestEnemyKindWatchBB(t *testing.T) {
	m, es := batchCProbeRoom(t)
	bb, _ := es.Of(0)
	const mtd, exec = uint32(0x80F00000), uint32(0x80F169A8)
	store(t, m, bb.Addr+addr.ActorOffSubMtd, u32b(mtd))
	store(t, m, mtd+addr.MethodOffExecute, u32b(exec))
	tc := exec - addr.BbExecToTail
	for i := range uint32(4) {
		store(t, m, exec+4*i, u32b(addr.BbFpExec[i]))
		store(t, m, tc+4*i, u32b(addr.BbFpTail[i]))
	}
	store(t, m, tc+addr.BbTailBlrOff, u32b(addr.BbFpBlr))
	store(t, m, bb.Addr+addr.BbOffTailRoot1, u32b(math.Float32bits(12.5)))
	var out bytes.Buffer
	if err := enemyKindWatch(context.Background(), &out, m, 120*time.Millisecond, enemysync.KindBB); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	s := out.String()
	for _, want := range []string{
		`kind bb proc 0x00B6 arc "Bb" bck 0x15..0x22 morf +0x2B4 ice +0xC80 gate +0x2D8`,
		"3 bb in room 1 of \"M_Dai\", 2 taken:",
		"exec 0x80F169A8 [9421FF70 7C0802A6 90010094 DBE10080] tc 0x80F10404 [9421FEB0 7C0802A6 90010154 DBE10140] blr 4E800020 fp=ok",
		"gate params-gate bb_type=5",
		"type 4 state 0 m2df 0 m2f2 0x00 dead 0 knock 0.00 ", "live ok root 12.5 0.0 0.0 tail0 0.0 0.0 0.0",
		"| at+0x5BC ", "| head+0x6E8 ", "| tg+0x814 ", "| co+0x940 ",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("bb output lacks %q:\n%s", want, s)
		}
	}
	store(t, m, tc+8, u32b(0x60000000))
	x, err := bbExecText(m, bb.Addr)
	if err != nil || !strings.HasSuffix(x, " fp=miss") {
		t.Errorf("a changed tail_control word: %q %v", x, err)
	}
}

// TestEnemyKindWatchMT: enemy-watch --kind mt prints P0-MT's fields: the
// ring index, the eye sphere beside eyePos and, per segment, its model's
// base translation beside its body sphere's centre.
func TestEnemyKindWatchMT(t *testing.T) {
	m, es := batchCProbeRoom(t)
	mt, _ := es.Of(3)
	const morf1, model1 = uint32(0x80C10000), uint32(0x80C20000)
	store(t, m, mt.Addr+addr.MtOffMorf+4, u32b(morf1))
	store(t, m, morf1+addr.McaMorfOffModel, u32b(model1))
	for k, v := range []float32{10, 20, 30} {
		store(t, m, model1+addr.J3DModelOffBaseTRMtx+0xC+0x10*uint32(k), u32b(math.Float32bits(v)))
	}
	store(t, m, mt.Addr+addr.MtOffRingIdx, u32b(0x45))
	store(t, m, mt.Addr+addr.MtOffMode, []byte{enemysync.MtModeFight, 1})
	var out bytes.Buffer
	if err := enemyKindWatch(context.Background(), &out, m, 120*time.Millisecond, enemysync.KindMT); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	s := out.String()
	for _, want := range []string{
		`kind mt proc 0x00D9 arc "Mt" bck 0x07..0x0A morf +0x2C4 ice +0x1900 gate +0x454`,
		"1 mt in room 1 of \"M_Dai\", 1 taken:", "mt_mode=1 ",
		"mode 1 action 1 ring 5 dive 0 ", "live ok eye_sph 0.0 0.0 0.0 eyePos 0.0 0.0 0.0",
		"| seg0 tr - sph ", "| seg1 tr 10.0 20.0 30.0 sph 0.0 0.0 0.0 r 0.0 anchor ", "| seg7 tr - ",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("mt output lacks %q:\n%s", want, s)
		}
	}
}
