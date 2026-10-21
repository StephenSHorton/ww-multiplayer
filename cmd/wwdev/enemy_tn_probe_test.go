package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/enemysync"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// tnProbeRoom is M_Dai with Link in room 1 and the TN batch's B0 cases
// placed: 0 TN setID 5 guarding with its sword (a BOKO), 1 an unkeyed TN,
// 2 TN setID 6 with miniboss params 0xD (refused by the params gate), 3
// BK setID 0.
func tnProbeRoom(t *testing.T) (*mem.FakeMem, *fakegame.Enemies) {
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
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 5, Proc: stage.ProcTN, Params: 0xFFFF0004, Weapon: true},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: addr.ActorSetIDNone, Proc: stage.ProcTN, Params: 0xFFFF0000},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 6, Proc: stage.ProcTN, Params: 0xFFFF000D},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 0, Proc: stage.ProcBK, Params: 0xFFFF8784},
	)
	for range 4 {
		if err := g.FrameWith(es); err != nil {
			t.Fatal(err)
		}
	}
	tn, _ := es.Of(0)
	store(t, m, tn.Addr+addr.TnOffBehavior, []byte{4})
	store(t, m, tn.Addr+addr.TnOffEquip, []byte{3})
	store(t, m, tn.Addr+addr.TnOffPieces, []byte{0x0F})
	store(t, m, tn.Addr+addr.TnOffDRAction, []byte{0, addr.TnActDefence})
	store(t, m, tn.Addr+addr.TnOffM1444, []byte{1})
	store(t, m, tn.Addr+addr.TnOffWeapon, u32b(1))
	// The method table and the execute function P0-c reads.
	const mtd, exec = uint32(0x80F00000), uint32(0x80F1AEA8)
	store(t, m, tn.Addr+addr.ActorOffSubMtd, u32b(mtd))
	store(t, m, mtd+addr.MethodOffExecute, u32b(exec))
	return m, es
}

// TestEnemyMatchDryTN: enemy-match lists the staged Darknuts beside the
// rows (sorted by Ident) with their live gate bytes after " pid=", their
// sword on a boko line, and "tn" without "+u" leaves the unkeyed one off.
func TestEnemyMatchDryTN(t *testing.T) {
	m, _ := tnProbeRoom(t)
	var out bytes.Buffer
	if err := enemyMatch(&out, m, enemysync.AllKinds(), enemysync.AllStaged()); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{
		" kinds=mo2+u,bk+u,cc+u,ki+u,am+u,am2+u walk=complete staged=tn+u\n",
		"ident kind=bk proc=0x00BE setID=0x0000 ",
		"ident kind=tn proc=0x00C0 setID=0x0005 ", " verdict=ok pid=",
		" tn_behavior=4 tn_action=10 tn_boss=0 tn_equip=3 tn_m02c1=0 tn_m1518=0 tn_m14f8=0 tn_m140c=0 tn_live=ok\n",
		"ident kind=tn proc=0x00C0 setID=0x0006 ", "params=0xFFFF000D ", "verdict=params-gate",
		"ident kind=tn proc=0x00C0 setID=0xFFFF ",
		"boko kind=tn setID=0x0005 ", " boko_state=found boko_proc=0x01D5 boko_mode=2\n",
		"summary idents=4 taken=3 refused=1\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	var procs []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "ident ") {
			procs = append(procs, strings.Fields(l)[2]+"/"+strings.Fields(l)[3])
			if head, _, _ := strings.Cut(l, " pid="); strings.Contains(head, "tn_") {
				t.Errorf("tn live bytes before the pid: %s", l)
			}
		}
	}
	if strings.Join(procs, " ") != "proc=0x00BE/setID=0x0000 proc=0x00C0/setID=0x0005 proc=0x00C0/setID=0x0006 proc=0x00C0/setID=0xFFFF" {
		t.Errorf("ident order %v", procs)
	}

	set, staged, err := splitStagedKinds("bk,tn")
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := enemyMatch(&out, m, set, staged); err != nil {
		t.Fatal(err)
	}
	s = out.String()
	if !strings.Contains(s, " kinds=bk walk=complete staged=tn\n") || !strings.Contains(s, "setID=0xFFFF ") ||
		!strings.Contains(s, "verdict=unkeyed-off pid=") || !strings.Contains(s, "tn_action=- ") {
		t.Errorf("--kinds bk,tn:\n%s", s)
	}
	if _, staged, err := splitStagedKinds("tn+u"); err != nil || !staged.TakesUnkeyed(enemysync.KindTN) {
		t.Errorf("tn+u: %v %v", staged, err)
	}
	if _, _, err := splitStagedKinds("tn,tn+u"); err == nil {
		t.Error("tn listed twice accepted")
	}
	if _, _, err := splitStagedKinds("tn,zz"); err == nil {
		t.Error("an unknown kind beside tn accepted")
	}
}

// TestEnemyKindWatchTN: enemy-watch --kind tn resolves the Tn table on
// its own ArcRes, samples through ReadSampleTN and prints P0-b's fields,
// the sword's BOKO and the P0-c execute addresses.
func TestEnemyKindWatchTN(t *testing.T) {
	m, es := tnProbeRoom(t)
	const morf = uint32(0x80C00000)
	placeKindTable(t, m, 34, addr.TnArc, 0x80EE0000, addr.TnBckFirst, addr.TnBckLast, 0x80EF0000)
	tn, _ := es.Of(0)
	store(t, m, tn.Addr+addr.TnOffMorf, u32b(morf))
	store(t, m, morf+addr.McaMorfOffAnm, u32b(0x80EF0000+0x100*0x30))
	store(t, m, tn.Addr+addr.ActorOffCurAngle+2, []byte{0x20, 0x00})

	var out bytes.Buffer
	if err := enemyKindWatch(context.Background(), &out, m, 150*time.Millisecond, enemysync.KindTN); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	s := out.String()
	for _, want := range []string{
		`kind tn proc 0x00C0 arc "Tn" bck 0x28..0x55 morf +0x2C4 ice +0x1524 gate +0x2BC`,
		"Tn table: resolved, mObjectInfo[34]",
		"3 tn in room 1 of \"M_Dai\", 2 taken:",
		"gate params-gate tn_behavior=",
		"exec 0x80F1AEA8 part_move 0x80F19E68 yoroi_break 0x80F18C5C",
		"setID 0x0005 pid ", "ang 8192 ", "action 10 ", "flags 0x08 [DEFENCE]", "flags2 0x01 [WEAPON]", "bck 0x30 AGUARD1 ",
		"equip 3 ", "pieces 0x0F [ARMOR B3 HELMET SHIELD]", "m0C34 1 ", "m1444 1 ", "live ok ",
		"sword boko pid ", "proc 0x01D5 mode 2 (enemy-carry)",
		"| co+0xC74 ", "| def+0x1000 ", "| wepon2+0x1258 ",
		"Tn table stats: reads ",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("tn output lacks %q:\n%s", want, s)
		}
	}
}
