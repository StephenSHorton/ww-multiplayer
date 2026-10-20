package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// mDai stands in for M_Dai (the Earth Temple's table is ET; any table
// serves the fake).
var mDai = fakegame.Stage{Name: "M_Dai", Table: addr.TableDRC, Room: 1}

// placeMoblins is the prototype's room as the live read found it: three
// keyed Moblins in room 1 (setIDs 0 and 1 mode 1, setID 2 mode 0), a
// carried one (mode 5, refused), a runtime one (setID 0xFFFF) and one in
// room 2.
func placeMoblins() *fakegame.Enemies {
	return fakegame.PlaceEnemies(
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 2, Proc: stage.ProcMO2},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 0, Proc: stage.ProcMO2, Mode: 1},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 1, Proc: stage.ProcMO2, Mode: 1},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 3, Proc: stage.ProcMO2, Mode: addr.Mo2ModeCarried},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: addr.ActorSetIDNone, Proc: stage.ProcMO2},
		fakegame.Enemy{Stage: mDai.Name, Room: 2, SetID: 4, Proc: stage.ProcMO2},
	)
}

// moblinRoom loads M_Dai with Link in room 1 (room 2 a neighbour), runs
// the placed Moblins to their execute state and gives setID 0 a
// position, angles, a morf and an enemy_ice mode.
func moblinRoom(t *testing.T) (*fakegame.FakeGame, *mem.FakeMem, *fakegame.Enemies) {
	t.Helper()
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	if err := g.LoadStage(mDai); err != nil {
		t.Fatal(err)
	}
	if err := g.EnterRoom(1, 2); err != nil {
		t.Fatal(err)
	}
	es := placeMoblins()
	for range 4 {
		if err := g.FrameWith(es); err != nil {
			t.Fatal(err)
		}
	}
	a, ok := es.Of(1) // setID 0
	if !ok {
		t.Fatal("setID 0 is not live")
	}
	pos := make([]byte, 12)
	for i, v := range []float32{-1500.5, 250, 3000} {
		binary.BigEndian.PutUint32(pos[4*i:], math.Float32bits(v))
	}
	store(t, m, a.Addr+addr.ActorOffCurPos, pos)
	store(t, m, a.Addr+addr.ActorOffCurAngle+2, []byte{0x40, 0x00})
	store(t, m, a.Addr+addr.ActorOffShapeAngle+2, []byte{0x3F, 0xFF})
	store(t, m, a.Addr+addr.Mo2OffDRMode, []byte{0xFF, 0x9C, 0x00, 0x14}) // mode -100, action 20
	store(t, m, a.Addr+addr.Mo2OffAttackType, []byte{0, 0, 0, 2, 0, 0, 0, 1})
	store(t, m, a.Addr+addr.ActorOffPauseFlag, []byte{0x02})
	store(t, m, a.Addr+addr.Mo2OffCoCyl+addr.Mo2CcCenterOff, func() []byte {
		b := make([]byte, 12)
		for i, v := range []float32{-1500.5, 250, 3035} {
			binary.BigEndian.PutUint32(b[4*i:], math.Float32bits(v))
		}
		return b
	}())
	store(t, m, a.Addr+addr.Mo2OffIceShrink, []byte{5})
	store(t, m, a.Addr+addr.Mo2OffIce+addr.EnemyIceOffMode, []byte{2})
	store(t, m, a.Addr+addr.Mo2OffInvuln, []byte{0, 7})
	store(t, m, a.Addr+addr.Mo2OffTilt, []byte{0xFF, 0xFE})
	const morf = uint32(0x80C00000)
	store(t, m, a.Addr+addr.Mo2OffMorf, u32b(morf))
	store(t, m, morf+addr.McaMorfOffAttr, []byte{2})
	store(t, m, morf+addr.McaMorfOffRate, u32b(math.Float32bits(1.5)))
	store(t, m, morf+addr.McaMorfOffFrame, u32b(math.Float32bits(12.25)))
	return g, m, es
}

// The fake Mo2 resource table: mObjectInfo[mo2Entry] is "Mo2" with its
// mRes at mo2Res and BCK i's resource at mo2Bck(i).
const (
	mo2Entry = 28 // M_Dai's, as s2_res_probe found it (SESSION-2026-10-18)
	mo2Arch  = uint32(0x80D80000)
	mo2Res   = uint32(0x80D00000)
)

func mo2Bck(idx int) uint32 { return 0x80D10000 + 0x100*uint32(idx) }

// placeMo2Table writes the fake Mo2 entry and its 56 BCK pointers.
func placeMo2Table(t *testing.T, m *mem.FakeMem) {
	t.Helper()
	e := make([]byte, addr.ResInfoSize)
	copy(e[addr.ResInfoOffName:], addr.Mo2Arc)
	binary.BigEndian.PutUint16(e[addr.ResInfoOffCount:], 0x80)
	binary.BigEndian.PutUint32(e[addr.ResInfoOffArchive:], mo2Arch)
	binary.BigEndian.PutUint32(e[addr.ResInfoOffRes:], mo2Res)
	store(t, m, addr.MObjectInfo+mo2Entry*addr.ResInfoSize, e)
	var p []byte
	for i := addr.Mo2BckFirst; i <= addr.Mo2BckLast; i++ {
		p = binary.BigEndian.AppendUint32(p, mo2Bck(i))
	}
	store(t, m, mo2Res+4*addr.Mo2BckFirst, p)
}

// TestEnemyWatch: the probe keeps the three keyed mode-0 Moblins of Link's
// room, sorted by setID, prints every field it read and writes the CSV,
// with the host-derived animation columns (bck through the Mo2 table,
// attr, anm_seq, morf, flags2, blur) appended.
func TestEnemyWatch(t *testing.T) {
	_, m, es := moblinRoom(t)
	if err := mailbox.WriteREL(m, mailbox.REL{Linked: true, BuildID: 0xAC19DE43, Frames: 77}); err != nil {
		t.Fatal(err)
	}
	var out, csv bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// No Mo2 entry yet: the probe says so and every bck reads 0xFF.
	if err := enemyWatch(ctx, &out, nil, m, 200*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); !strings.Contains(s, "Mo2 table: NOT resolved: no loaded Mo2 entry in mObjectInfo") || !strings.Contains(s, "bck 0xFF anm_attr 2 anm_seq 0 anm_morf 0") ||
		!strings.Contains(s, "Mo2 table stats: reads ") {
		t.Errorf("without the table:\n%s", s)
	}
	out.Reset()

	// setID 0 plays WAIT (0x73, a loop) set with morf 15, its spear
	// drawn and its blur on with shape 3 at half scale.
	a, _ := es.Of(1)
	const morf = uint32(0x80C00000)
	placeMo2Table(t, m)
	store(t, m, morf+addr.McaMorfOffAnm, u32b(mo2Bck(addr.Mo2BckWait)))
	store(t, m, morf+addr.McaMorfOffCurMorf, u32b(math.Float32bits(0)))
	store(t, m, morf+addr.McaMorfOffMorfStep, u32b(math.Float32bits(1.0/15)))
	store(t, m, a.Addr+addr.Mo2OffHasWeapon, []byte{1})
	store(t, m, a.Addr+addr.Mo2OffBlurScale, u32b(math.Float32bits(0.5)))
	store(t, m, a.Addr+addr.Mo2OffBlurOn, u32b(1))
	store(t, m, a.Addr+addr.Mo2OffBlurShape, u32b(3))

	if err := enemyWatch(ctx, &out, &csv, m, 200*time.Millisecond); err != nil {
		t.Fatalf("enemyWatch: %v\n%s", err, out.String())
	}
	s := out.String()
	for _, want := range []string{
		`stage "M_Dai" room 1; REL words at 0x804130B0: magic 0x5757524C linked=true build 0xAC19DE43 frames 77`,
		"3 keyed mode-0/1 Moblins in room 1 of \"M_Dai\":",
		"setID 0 pid", "setID 1 pid", "setID 2 pid",
		"mo2_mode 1 pos -1500.5 250.0 3000.0 ang 16384 shape 16383 hp 4 dr -100/20 atk 2/1 morf 0x80C00000 attr 2 rate 1.50 frame 12.25 pause 0x02 co -1500.5 250.0 3035.0 (d 0.0 0.0 35.0)",
		"ice 5 ice_mode 2 inv 7 tilt -2 bck 0x73 anm_attr 2 anm_seq 0 anm_morf 15 flags2 0x05 blur 1/3 scale 0.50",
		"mo2_mode 0 pos 0.0",
		"Mo2 table: resolved, mObjectInfo[28] mRes 0x80D00000 (BCK 0x3E..0x75 at 0x80D13E00..0x80D17500)",
		"Mo2 table stats: reads 1 bck_miss 0 miss_reads 0",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	for _, bad := range []string{"setID 3 ", "setID 4 ", "setID 65535", "changed under"} {
		if strings.Contains(s, bad) {
			t.Errorf("output has %q:\n%s", bad, s)
		}
	}
	// The walk line lists them in setID order, whatever the queue's.
	walk := s[strings.Index(s, "Moblins in room 1"):]
	walk = walk[:strings.Index(walk, "\n")]
	if i0, i1, i2 := strings.Index(walk, "[setID 0 "), strings.Index(walk, "[setID 1 "), strings.Index(walk, "[setID 2 "); !(i0 < i1 && i1 < i2) || !strings.Contains(walk, "mode 1]") || !strings.Contains(walk, "mode 0]") {
		t.Errorf("walk line not sorted: %s", walk)
	}
	lines := strings.Split(strings.TrimSpace(csv.String()), "\n")
	if lines[0] != enemyCSVHeader || len(lines) < 1+3 {
		t.Fatalf("csv:\n%s", csv.String())
	}
	cols := strings.Split(lines[1], ",")
	if len(cols) != strings.Count(enemyCSVHeader, ",")+1 || cols[3] != "0" || cols[4] != "1" || cols[5] != "-1500.500" || cols[25] != "35.000" || cols[35] != "5" || cols[37] != "-2" || cols[38] != "1" || cols[39] != "2" {
		t.Errorf("csv row: %v", cols)
	}
	if got := strings.Join(cols[40:], ","); got != "0x73,2,0,15,0x05,1,3,0.500" {
		t.Errorf("csv anim columns %s, want 0x73,2,0,15,0x05,1,3,0.500 (row %v)", got, cols)
	}

	// The detector over time (windows sized for a loaded machine: the probe
	// samples every 16 ms, the phases need only a few samples each): a switch to KWAIT (0x5C) set with morf 10
	// is one restart; a pointer outside the table is another, reads bck
	// 0xFF, counts as a miss and re-reads the table.
	out.Reset()
	done := make(chan error, 1)
	go func() { done <- enemyWatch(ctx, &out, nil, m, 1200*time.Millisecond) }()
	time.Sleep(300 * time.Millisecond)
	store(t, m, morf+addr.McaMorfOffMorfStep, u32b(math.Float32bits(0.1)))
	store(t, m, morf+addr.McaMorfOffAnm, u32b(mo2Bck(addr.Mo2BckKWait)))
	time.Sleep(400 * time.Millisecond)
	store(t, m, morf+addr.McaMorfOffAnm, u32b(0x80D20004))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	s = out.String()
	for _, want := range []string{"bck 0x73 anm_attr 2 anm_seq 0 anm_morf 15", "bck 0x5C anm_attr 2 anm_seq 1 anm_morf 10", "bck 0xFF anm_attr 2 anm_seq 2 anm_morf 10"} {
		if !strings.Contains(s, want) {
			t.Errorf("detector run lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "anm_seq 3") || strings.Contains(s, "bck_miss 0 ") || strings.Contains(s, "miss_reads 0\n") {
		t.Errorf("detector run: a spurious restart or the wrong miss counts:\n%s", s)
	}
	store(t, m, morf+addr.McaMorfOffAnm, u32b(mo2Bck(addr.Mo2BckWait)))

	// A Moblin whose block is reused under the probe (a new pid) is
	// reported once and the walk runs again, finding the new actor.
	out.Reset()
	go func() { done <- enemyWatch(ctx, &out, nil, m, 800*time.Millisecond) }()
	time.Sleep(250 * time.Millisecond)
	store(t, m, a.Addr+addr.ActorOffBsPcID, u32b(a.PID+1000))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s := out.String(); strings.Count(s, "changed under the probe") != 1 || !strings.Contains(s, "[setID 0 pid 1002 ") {
		t.Errorf("after a pid change:\n%s", s)
	}
}

func TestSplitValueFlags(t *testing.T) {
	pos, flags, values, err := splitValueFlags([]string{"5", "--csv", "out.csv", "--yes"}, []string{"csv"}, "yes")
	if err != nil || len(pos) != 1 || pos[0] != "5" || !flags["yes"] || values["csv"] != "out.csv" {
		t.Errorf("%v %v %v %v", pos, flags, values, err)
	}
	if _, _, values, err := splitValueFlags([]string{"--csv=a b.csv"}, []string{"csv"}); err != nil || values["csv"] != "a b.csv" {
		t.Errorf("--csv=: %v %v", values, err)
	}
	if _, _, _, err := splitValueFlags([]string{"--csv"}, []string{"csv"}); err == nil {
		t.Error("--csv without a value accepted")
	}
	if _, _, _, err := splitValueFlags([]string{"--nope"}, []string{"csv"}); err == nil {
		t.Error("--nope accepted")
	}
}
