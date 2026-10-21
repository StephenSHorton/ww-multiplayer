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

	"github.com/StephenSHorton/ww-multiplayer/mods/enemysync"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// kindProbeRoom is M_Dai with Link in room 1 and the L0 cases placed:
// placed 0 BK setID 0 torch 0xFFFF8784 holding a BOKO, 1 BK setID 1
// sleeper 0xFFFF0002, 2 CC setID 3 black 0xFF460300, 3 CC setID 4 red
// 0xFF460100, 4 an unkeyed green CC, 5 MO2 setID 2.
func kindProbeRoom(t *testing.T) (*mem.FakeMem, *fakegame.Enemies) {
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
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 0, Proc: stage.ProcBK, Params: 0xFFFF8784, Weapon: true},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 1, Proc: stage.ProcBK, Params: 0xFFFF0002},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 3, Proc: stage.ProcCC, Params: 0xFF460300},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 4, Proc: stage.ProcCC, Params: 0xFF460100},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: addr.ActorSetIDNone, Proc: stage.ProcCC, Params: 0xFF000000},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 2, Proc: stage.ProcMO2},
	)
	for range 4 {
		if err := g.FrameWith(es); err != nil {
			t.Fatal(err)
		}
	}
	at := func(i int) uint32 {
		a, ok := es.Of(i)
		if !ok {
			t.Fatalf("placed %d is not live", i)
		}
		return a.Addr
	}
	store(t, m, at(0)+addr.BkOffType, []byte{4})
	store(t, m, at(0)+addr.ActorOffHomePos, func() []byte {
		var b []byte
		for _, v := range []float32{1280, -200, 6000} {
			b = binary.BigEndian.AppendUint32(b, math.Float32bits(v))
		}
		return b
	}())
	store(t, m, at(1)+addr.BkOffType, []byte{2})
	store(t, m, at(2)+addr.CcOffBehavior, []byte{0, 3})
	store(t, m, at(3)+addr.CcOffBehavior, []byte{0, 1})
	return m, es
}

// bkPKey is placed 0's pkey: the test vector's params and home in home
// room 1.
var bkPKey = enemysync.PKey(addr.ProcBK, 0, 0xFFFF8784, 1, [3]uint32{0x44A00000, 0xC3480000, 0x45BB8000})

func TestEnemyMatchDry(t *testing.T) {
	m, es := kindProbeRoom(t)
	var out bytes.Buffer
	if err := enemyMatch(&out, m, enemysync.AllKinds()); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	bk, _ := es.Of(0)
	for _, want := range []string{
		"scope stage=M_Dai room=1 kinds=mo2+u,bk+u,cc+u,ki+u,am+u,am2+u walk=complete\n",
		// The test vector's params and home, in home room 1.
		fmt.Sprintf("ident kind=bk proc=0x00BE setID=0x0000 pkey=0x%08X params=0xFFFF8784 home_room=1 home=0x44A00000,0xC3480000,0x45BB8000 verdict=ok pid=", bkPKey),
		" bk_type=4\n",
		"ident kind=bk proc=0x00BE setID=0x0001 ",
		" bk_type=2\n",
		"ident kind=cc proc=0x00CF setID=0x0003 ",
		" cc_behavior=0 cc_color=3 cc_m304=0\n",
		" cc_behavior=0 cc_color=1 cc_m304=0\n",
		"ident kind=cc proc=0x00CF setID=0xFFFF ",
		"ident kind=mo2 proc=0x00BD setID=0x0002 ",
		fmt.Sprintf("boko kind=bk setID=0x0000 pkey=0x%08X pid=%d boko_pid=", bkPKey, bk.PID),
		" boko_state=found boko_proc=0x01D5 boko_mode=2\n",
		"boko kind=bk setID=0x0001 ",
		" boko_pid=0 boko_state=none boko_proc=0x0000 boko_mode=0\n",
		"summary idents=6 taken=4 refused=2\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	// One line per Ident, sorted (proc, setID, pkey); the verdict is the
	// last field before " pid=", and every live field comes after it, so
	// `sed 's/ pid=.*//'` leaves only what both machines must agree on.
	var idents []string
	for _, l := range strings.Split(s, "\n") {
		if !strings.HasPrefix(l, "ident ") && !strings.HasPrefix(l, "boko ") {
			continue
		}
		head, _, ok := strings.Cut(l, " pid=")
		if !ok {
			t.Errorf("no pid: %s", l)
		}
		for _, live := range []string{"bk_type", "cc_", "mo2_mode", "boko_"} {
			if strings.Contains(head, live) {
				t.Errorf("%s before the pid: %s", live, l)
			}
		}
		if strings.HasPrefix(l, "ident ") {
			idents = append(idents, l)
			if f := strings.Fields(head); !strings.HasPrefix(f[len(f)-1], "verdict=") {
				t.Errorf("verdict is not the last field before the pid: %s", l)
			}
		}
	}
	if len(idents) != 6 || !strings.Contains(idents[0], "kind=mo2") || !strings.Contains(idents[1], "setID=0x0000") || !strings.Contains(idents[5], "setID=0xFFFF") {
		t.Errorf("ident lines out of order:\n%s", strings.Join(idents, "\n"))
	}

	// --kinds without +u refuses the unkeyed ChuChu.
	out.Reset()
	set, _ := enemysync.ParseKinds("cc")
	if err := enemyMatch(&out, m, set); err != nil {
		t.Fatal(err)
	}
	// It is refused before the gate read: its colour and m304 are unread.
	if s := out.String(); !strings.Contains(s, "setID=0xFFFF") || !strings.Contains(s, "verdict=unkeyed-off") ||
		!strings.Contains(s, "cc_color=- cc_m304=-\n") || strings.Contains(s, "kind=bk") {
		t.Errorf("--kinds cc:\n%s", s)
	}
}

// placeKindTable writes a loaded archive entry k named arc with its BCK
// pointers base + 0x100*idx for idx first..last.
func placeKindTable(t *testing.T, m *mem.FakeMem, k int, arc string, res uint32, first, last int, base uint32) {
	t.Helper()
	e := make([]byte, addr.ResInfoSize)
	copy(e[addr.ResInfoOffName:], arc)
	binary.BigEndian.PutUint16(e[addr.ResInfoOffCount:], 2)
	binary.BigEndian.PutUint32(e[addr.ResInfoOffArchive:], 0x80D90000+uint32(k)*0x100)
	binary.BigEndian.PutUint32(e[addr.ResInfoOffRes:], res)
	store(t, m, addr.MObjectInfo+uint32(k*addr.ResInfoSize), e)
	var p []byte
	for i := first; i <= last; i++ {
		p = binary.BigEndian.AppendUint32(p, base+0x100*uint32(i))
	}
	store(t, m, res+4*uint32(first), p)
}

func TestEnemyKindWatch(t *testing.T) {
	m, es := kindProbeRoom(t)
	const morf = uint32(0x80C00000)
	placeKindTable(t, m, 30, addr.BkArc, 0x80E00000, addr.BkBckFirst, addr.BkBckLast, 0x80E10000)
	placeKindTable(t, m, 31, addr.CcArc, 0x80E40000, addr.CcBckFirst, addr.CcBckLast, 0x80E50000)
	bk, _ := es.Of(0)
	store(t, m, bk.Addr+addr.BkOffMorf, u32b(morf))
	store(t, m, morf+addr.McaMorfOffAnm, u32b(0x80E10000+0x100*addr.BkBckWait))
	store(t, m, morf+addr.McaMorfOffAttr, []byte{2})
	store(t, m, bk.Addr+addr.BkOffDRAction, []byte{0, 20})
	store(t, m, bk.Addr+addr.BkOffPlaySkip, []byte{0, 1})

	ctx := context.Background()
	var out bytes.Buffer
	if err := enemyKindWatch(ctx, &out, m, 150*time.Millisecond, enemysync.KindBK); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	s := out.String()
	for _, want := range []string{
		`stage "M_Dai" room 1; kind bk proc 0x00BE arc "Bk" bck 0x29..0x53 morf +0x2BC ice +0x1264 gate +0x2B4`,
		"Bk table: resolved, mObjectInfo[30] mRes 0x80E00000 mCount 2 (BCK 0x29..0x53 at 0x80E12900..0x80E15300)",
		"2 bk in room 1 of \"M_Dai\", 1 taken:",
		fmt.Sprintf("kind=bk proc=0x00BE setID=0x0000 pkey=0x%08X params=0xFFFF8784 home_room=1 home=0x44A00000,0xC3480000,0x45BB8000 gate ok bk_type=4", bkPKey),
		"setID=0x0001 pkey=", "gate params-gate bk_type=2",
		"mode 0 action 20", "flags 0x02 [DYING]", "flags2 0x03 [PLAY_SKIP WEAPON]", "bck 0x51 BK_WAIT attr 2",
		"type 4 boko pid ", "proc 0x01D5 mode 2 (enemy-carry)",
		"Bk table stats: reads ",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("bk output lacks %q:\n%s", want, s)
		}
	}

	cc, _ := es.Of(3)
	store(t, m, cc.Addr+addr.CcOffMorf, u32b(morf))
	store(t, m, morf+addr.McaMorfOffAnm, u32b(0x80E50000+0x100*0x1C))
	store(t, m, cc.Addr+addr.CcOffBckIndex, u32b(0x1C))
	store(t, m, cc.Addr+addr.CcOffCyl+addr.CcCylOffTgSPrm, u32b(0x0B))
	out.Reset()
	if err := enemyKindWatch(ctx, &out, m, 150*time.Millisecond, enemysync.KindCC); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	s = out.String()
	for _, want := range []string{
		"CC table: resolved, mObjectInfo[31]",
		"3 cc in room 1 of \"M_Dai\", 2 taken:",
		"gate params-gate cc_behavior=0 cc_color=3",
		"setID 0x0004 pid ", "bck 0x1C HUSE_WALK", "m320 0x1C HUSE_WALK", "flags2 0x04 [SQUASH]",
		// The unkeyed one's Tg is not Set in the fake: invulnerable.
		"setID 0xFFFF pid ", "flags 0x04 [INVULN] flags2 0x04 [SQUASH] bck 0xFF -",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("cc output lacks %q:\n%s", want, s)
		}
	}
}
