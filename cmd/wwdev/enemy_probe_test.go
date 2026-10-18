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

// placeMoblins is the prototype's room: three keyed mode-0 Moblins in
// room 1, a jailed one (mode 1), a runtime one (setID 0xFFFF) and one in
// room 2.
func placeMoblins() *fakegame.Enemies {
	return fakegame.PlaceEnemies(
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 2, Proc: stage.ProcMO2},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 0, Proc: stage.ProcMO2},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 1, Proc: stage.ProcMO2},
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: 3, Proc: stage.ProcMO2}, // jailed below
		fakegame.Enemy{Stage: mDai.Name, Room: 1, SetID: addr.ActorSetIDNone, Proc: stage.ProcMO2},
		fakegame.Enemy{Stage: mDai.Name, Room: 2, SetID: 4, Proc: stage.ProcMO2},
	)
}

// moblinRoom loads M_Dai with Link in room 1 (room 2 a neighbour), runs
// the placed Moblins to their execute state, jails placed entry 3 and
// gives setID 0 a position, angles and a morf.
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
	jail, ok := es.Of(3)
	if !ok {
		t.Fatal("the jailed Moblin is not live")
	}
	store(t, m, jail.Addr+addr.Mo2OffMode, []byte{1})
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
	store(t, m, a.Addr+addr.Mo2OffInvuln, []byte{0, 7})
	store(t, m, a.Addr+addr.Mo2OffTilt, []byte{0xFF, 0xFE})
	const morf = uint32(0x80C00000)
	store(t, m, a.Addr+addr.Mo2OffMorf, u32b(morf))
	store(t, m, morf+addr.McaMorfOffAttr, []byte{2})
	store(t, m, morf+addr.McaMorfOffRate, u32b(math.Float32bits(1.5)))
	store(t, m, morf+addr.McaMorfOffFrame, u32b(math.Float32bits(12.25)))
	return g, m, es
}

// TestEnemyWatch: the probe keeps the three keyed mode-0 Moblins of Link's
// room, sorted by setID, prints every field it read and writes the CSV.
func TestEnemyWatch(t *testing.T) {
	_, m, es := moblinRoom(t)
	if err := mailbox.WriteREL(m, mailbox.REL{Linked: true, BuildID: 0xAC19DE43, Frames: 77}); err != nil {
		t.Fatal(err)
	}
	var out, csv bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := enemyWatch(ctx, &out, &csv, m, 40*time.Millisecond); err != nil {
		t.Fatalf("enemyWatch: %v\n%s", err, out.String())
	}
	s := out.String()
	for _, want := range []string{
		`stage "M_Dai" room 1; REL words at 0x804130B0: magic 0x5757524C linked=true build 0xAC19DE43 frames 77`,
		"3 keyed mode-0 Moblins in room 1 of \"M_Dai\":",
		"setID 0 pid", "setID 1 pid", "setID 2 pid",
		"pos -1500.5 250.0 3000.0 ang 16384 shape 16383 hp 4 dr -100/20 atk 2/1 morf 0x80C00000 attr 2 rate 1.50 frame 12.25 pause 0x02 co -1500.5 250.0 3035.0 (d 0.0 0.0 35.0)",
		"ice 5 inv 7 tilt -2",
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
	if i0, i1, i2 := strings.Index(walk, "[setID 0 "), strings.Index(walk, "[setID 1 "), strings.Index(walk, "[setID 2 "); !(i0 < i1 && i1 < i2) {
		t.Errorf("walk line not sorted: %s", walk)
	}
	lines := strings.Split(strings.TrimSpace(csv.String()), "\n")
	if lines[0] != enemyCSVHeader || len(lines) < 1+3 {
		t.Fatalf("csv:\n%s", csv.String())
	}
	cols := strings.Split(lines[1], ",")
	if len(cols) != strings.Count(enemyCSVHeader, ",")+1 || cols[3] != "0" || cols[4] != "1" || cols[5] != "-1500.500" || cols[25] != "35.000" || cols[35] != "5" || cols[37] != "-2" {
		t.Errorf("csv row: %v", cols)
	}

	// A Moblin whose block is reused under the probe (a new pid) is
	// reported once and the walk runs again, finding the new actor.
	a, _ := es.Of(1)
	out.Reset()
	done := make(chan error, 1)
	go func() { done <- enemyWatch(ctx, &out, nil, m, 200*time.Millisecond) }()
	time.Sleep(60 * time.Millisecond)
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
