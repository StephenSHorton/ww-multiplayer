package main

import (
	"bytes"
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
)

// batchDRoom is M_Dai with Link in room 1 and es placed (far from Link)
// and executing.
func batchDRoom(t *testing.T, es ...fakegame.Enemy) (*mem.FakeMem, *fakegame.Enemies) {
	t.Helper()
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	if err := g.LoadStage(mDai); err != nil {
		t.Fatal(err)
	}
	if err := g.EnterRoom(1, 2); err != nil {
		t.Fatal(err)
	}
	for i := range es {
		es[i].Stage, es[i].Room = mDai.Name, 1
		es[i].Pos = [3]float32{5000, 0, float32(1000 * i)}
	}
	p := fakegame.PlaceEnemies(es...)
	for range 4 {
		if err := g.FrameWith(p); err != nil {
			t.Fatal(err)
		}
	}
	return m, p
}

func f32bits(v float32) []byte { return u32b(math.Float32bits(v)) }

func storeVec(t *testing.T, m *mem.FakeMem, a uint32, v [3]float32) {
	t.Helper()
	for i := range v {
		store(t, m, a+uint32(4*i), f32bits(v[i]))
	}
}

// bstRoom places Gohdan's head and hands in the APPLIED state of R-D2.
func bstRoom(t *testing.T) (*mem.FakeMem, [3]uint32) {
	t.Helper()
	m, es := batchDRoom(t,
		fakegame.Enemy{SetID: addr.ActorSetIDNone, Proc: stage.ProcBST, Params: 0},
		fakegame.Enemy{SetID: addr.ActorSetIDNone, Proc: stage.ProcBST, Params: 1},
		fakegame.Enemy{SetID: addr.ActorSetIDNone, Proc: stage.ProcBST, Params: 2},
	)
	var at [3]uint32
	// Queue order hand2, head, hand1: the probe sorts by +0x2B4.
	for i, part := range []byte{2, 0, 1} {
		a, _ := es.Of(i)
		at[part] = a.Addr
		store(t, m, a.Addr+bstOffPart, []byte{part})
	}
	store(t, m, at[0]+bstOffMorf, u32b(0x80C00000))
	store(t, m, at[0]+bstOffAct, u32b(0x00000007))
	store(t, m, at[0]+bstOffDmg, u32b(0x00000003))
	store(t, m, at[0]+bstOffM2E7C, u32b(0x0C000000))
	for _, h := range at[1:] {
		store(t, m, h+bstOffAct, u32b(0x00000006))
		store(t, m, h+bstOffTimers, u32b(0x0000012C))
	}
	return m, at
}

// TestBstProbe: bst-probe lists the three parts by +0x2B4 with the raw
// words and their decode, and answers as bk_bst would: APPLIED in the
// forced recipe, WAITING (with why) once a hand wakes, ALREADY in
// END_DEMO, NO_ACTOR without the head's morf.
func TestBstProbe(t *testing.T) {
	m, at := bstRoom(t)
	run := func() string {
		t.Helper()
		var out bytes.Buffer
		if err := bstProbe(context.Background(), &out, m, 0); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	s := run()
	for _, want := range []string{
		`stage "M_Dai" room 1; P0-BST wants SirenB 1 0`,
		"3 proc 0xF2 actors\n",
		"head  pid ", " +0x2B4 0 morf 0x80C00000 | +0x10D4 00000007 00000003 (act 7 m10D8 0 mDamage 3) +0x2E7C 0C000000 (m2E7C 12) +0x2E98 00000000 (m2E98 0 m2E9A 0)",
		"hand1 pid ", "hand2 pid ", "+0x10D4 00000006 00000000 (act 6 m10D8 0 mDamage 0)", "+0x10FC 0000012C (m10FC[0] 0 sleep 300)",
		"bk_bst would answer APPLIED 0x0107030C\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	if h, h1 := strings.Index(s, "head "), strings.Index(s, "hand1 "); h < 0 || h1 < h || strings.Index(s, "hand2 ") < h1 {
		t.Errorf("parts not sorted by +0x2B4:\n%s", s)
	}
	store(t, m, at[2]+bstOffAct, u32b(0x00000001))
	if s := run(); !strings.Contains(s, "bk_bst would answer WAITING 0x0207030C: hand2 act 1 != 6\n") {
		t.Errorf("a hand awake:\n%s", s)
	}
	store(t, m, at[0]+bstOffAct, u32b(0x00000016))
	if s := run(); !strings.Contains(s, "bk_bst would answer ALREADY 0x0316030C\n") {
		t.Errorf("END_DEMO:\n%s", s)
	}
	store(t, m, at[0]+bstOffMorf, u32b(0))
	if s := run(); !strings.Contains(s, "bk_bst would answer NO_ACTOR 0x04000000: the head's morf is NULL\n") {
		t.Errorf("no morf:\n%s", s)
	}
}

// TestBstVerdictMissing: two parts are NO_ACTOR, and the count line says
// three are wanted.
func TestBstVerdictMissing(t *testing.T) {
	ps := []bstPart{{Part: 0, Init: 2, Morf: 1}, {Part: 1, Init: 2}}
	if r, _, why := bstVerdict(ps); r != mailbox.BossRNoActor || why != "no executing hand2" {
		t.Errorf("two parts: %d %q", r, why)
	}
	if s := bstReport(ps); !strings.HasPrefix(s, "2 proc 0xF2 actors (want 3: head and two hands)\n") {
		t.Errorf("report:\n%s", s)
	}
}

// rfRoom places one of each refresh proc with the fields of a clean
// candidate and the Tbox and Saku execute fingerprints.
func rfRoom(t *testing.T) (*mem.FakeMem, map[int16]uint32) {
	t.Helper()
	m, es := batchDRoom(t,
		fakegame.Enemy{SetID: 0, Proc: stage.Proc(procStone2), Params: 0x00001100},     // sw17
		fakegame.Enemy{SetID: 1, Proc: stage.Proc(procSaku), Params: 0x00E4E301},       // sw1 227, sw2 228, two halves
		fakegame.Enemy{SetID: 2, Proc: stage.Proc(procItem), Params: 0},                // purple rupee
		fakegame.Enemy{SetID: 3, Proc: stage.Proc(procLeaves), Params: 0x0005A000},     // sw 45
		fakegame.Enemy{SetID: 4, Proc: stage.Proc(procTbox), Params: 0x00000180 | 0x3}, // funcType 3, bit 3
	)
	at := map[int16]uint32{}
	for i, p := range []int16{procStone2, procSaku, procItem, procLeaves, procTbox} {
		a, _ := es.Of(i)
		at[p] = a.Addr
	}
	store(t, m, at[procSaku]+sakuOffState, append(u32b(1), u32b(1)...))
	store(t, m, at[procSaku]+sakuOffSwitch, append(u32b(227), u32b(228)...))
	const sakuMtd, sakuExec = uint32(0x80E00000), uint32(0x80E1242C)
	store(t, m, at[procSaku]+addr.ActorOffSubMtd, u32b(sakuMtd))
	store(t, m, sakuMtd+addr.MethodOffExecute, u32b(sakuExec))
	for _, f := range sakuBrokenFp {
		store(t, m, sakuExec-sakuExecOff+sakuBrokenOff+f.off, u32b(f.word))
	}
	store(t, m, at[procItem]+itemOffBitNo, u32b(5))
	store(t, m, at[procItem]+itemOffItemNo, []byte{0x04})
	store(t, m, at[procItem]+itemOffStatus, []byte{0}) // STATUS_UNK0: lying in the world, as P0-RF read it
	store(t, m, at[procTbox]+tboxOffBgOpen, u32b(0x81000000))
	store(t, m, at[procTbox]+tboxOffBgCur, u32b(0x81000100))
	const tboxMtd, tboxExec = uint32(0x80E40000), uint32(0x80E52FB0)
	store(t, m, at[procTbox]+addr.ActorOffSubMtd, u32b(tboxMtd))
	store(t, m, tboxMtd+addr.MethodOffExecute, u32b(tboxExec))
	store(t, m, tboxExec+0x0C, u32b(tboxFpWord0C))
	store(t, m, tboxExec+0x1C, u32b(tboxFpWord1C))
	store(t, m, at[procTbox]+tboxOffActFn, u32b(tboxExec-tboxExecOff+0x2634))
	return m, at
}

// TestRfProbe: rf-probe prints each refresh proc's fields, bit and gates,
// and PASS while the static checks hold; a Tbox action word that is none
// of the derived actions, a Saku switch copy off its params or a Stone2
// m648 outside {0,1} FAILs, and a gate shows as the proc gate.
func TestRfProbe(t *testing.T) {
	m, at := rfRoom(t)
	run := func() string {
		t.Helper()
		var out bytes.Buffer
		if err := rfProbe(&out, m); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	s := run()
	for _, want := range []string{
		`stage "M_Dai" room 1; walk `, "; dEvtMode 0\n",
		"proc 0x01CD room 1 params 0x00001100 ", "stone2 sw 17 m648 0 (idle); m648-in-{0,1} PASS | generic ok | proc gate ok",
		"saku sw1 227 sw2 228 params&0xF 1 (half 2 true) +0xEF4 0 +0xEF8 1 1 +0xF00 227 +0xF04 228; +0xF00==sw1 PASS +0xF04==sw2 PASS half2-matches-params PASS",
		"exec 0x80E1242C broken 0x80E10E8C [+0x1C 48000819 +0x2C 90030EF8 +0x80 4BFFF931 +0xD0 4E800020] broken-fp(v2) PASS",
		"item +0x630 5 (bit 5) +0x63A 0x04 +0x669 0x00 +0x66B 0; | generic ok | proc gate ok",
		"leaves sw 45 itemNo 0 itemSave 0;",
		"tbox funcType 3 bit 3 +0x2BC 0x81000000 +0x2C4 0x81000100 (closed) +0x348 0x80E52634 +0x3F0 0x0000;",
		"exec 0x80E52FB0 +0x0C 4BFFFA71 +0x1C 4E800020 execute-fp PASS OpenWait 0x80E52634 SwOnWait 0x80E527C8 SwOnWait2 0x80E528A0; +0x348 is actionOpenWait closed-chest-action PASS | generic ok | proc gate ok",
		"P0-RF PASS: 5 refresh actors, 0 static check(s) failed\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	store(t, m, at[procTbox]+tboxOffActFn, u32b(0x80E52A2C))
	store(t, m, at[procSaku]+sakuOffSwitch, u32b(9))
	store(t, m, at[procStone2]+stone2OffM648, u32b(7))
	store(t, m, at[procItem]+itemOffFlag, []byte{0x40})
	store(t, m, at[procItem]+itemOffStatus, []byte{6}) // STATUS_MAIN_NORMAL: the local pickup flight
	s = run()
	for _, want := range []string{
		"+0x348 is none of them closed-chest-action FAIL | generic ok | proc gate action\n",
		"+0xF00==sw1 FAIL", "m648 7 (other); m648-in-{0,1} FAIL | generic ok | proc gate m648 != 0",
		"+0x669 0x40 +0x66B 6; | generic ok | proc gate status 6 > 1, flag & 0x48",
		"P0-RF FAIL: 5 refresh actors, 3 static check(s) failed\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("after the breaks, output lacks %q:\n%s", want, s)
		}
	}
	// still_open S3's fingerprint is information only: a miss fails no check.
	store(t, m, 0x80E1242C-sakuExecOff+sakuBrokenOff+0x1C, u32b(0x60000000))
	if s := run(); !strings.Contains(s, " broken-fp(v2) FAIL (info)") || !strings.Contains(s, "P0-RF FAIL: 5 refresh actors, 3 static check(s) failed\n") {
		t.Errorf("a Saku broken fingerprint miss:\n%s", s)
	}
	store(t, m, at[procTbox]+addr.ActorOffSubMtd, u32b(0))
	if s := run(); !strings.Contains(s, " exec - execute-fp FAIL | generic ok | proc gate FP_MISS") {
		t.Errorf("no execute:\n%s", s)
	}
}

// mtSeg is one synthetic segment: the model's translation and column 2.
type mtSeg struct{ tr, col2 [3]float32 }

// mtRoom places a Magtail whose 8 segment matrices, body spheres and eye
// obey the design's rules for f470, with the retail fingerprint words.
func mtRoom(t *testing.T, mode uint8, f470 float32) (*mem.FakeMem, uint32, []mtSeg) {
	t.Helper()
	m, es := batchDRoom(t, fakegame.Enemy{SetID: addr.ActorSetIDNone, Proc: stage.ProcMT, Params: 0xFF027FFF})
	mt, _ := es.Of(0)
	segs := make([]mtSeg, addr.MtSegs)
	for i := range segs {
		ang := 0.3 * float64(i)
		sc := float32(1)
		if i == 0 {
			sc = 0.8
		}
		segs[i] = mtSeg{
			tr:   [3]float32{900 + 25*float32(i), 4121 + 2*float32(i), 4490 + 30*float32(i)},
			col2: [3]float32{sc * float32(math.Sin(ang)), 0, sc * float32(math.Cos(ang))},
		}
		morf, model := uint32(0x80C10000+0x100*i), uint32(0x80C20000+0x100*i)
		store(t, m, mt.Addr+addr.MtOffMorf+uint32(4*i), u32b(morf))
		store(t, m, morf+addr.McaMorfOffModel, u32b(model))
		for r := range uint32(3) {
			store(t, m, model+addr.J3DModelOffBaseTRMtx+0x10*r+0x8, f32bits(segs[i].col2[r]))
			store(t, m, model+addr.J3DModelOffBaseTRMtx+0x10*r+0xC, f32bits(segs[i].tr[r]))
		}
		k := -f470
		if i == 0 {
			k = mtSphOff0
		}
		var c [3]float32
		for r := range 3 {
			c[r] = segs[i].tr[r] + k*segs[i].col2[r]
		}
		storeVec(t, m, mt.Addr+addr.MtOffBodySph+uint32(addr.MtBodySphStride*i)+mtCcCenter, c)
	}
	var eye [3]float32
	for r := range 3 {
		eye[r] = segs[0].tr[r] + mtEyeOff*segs[0].col2[r]
	}
	storeVec(t, m, mt.Addr+addr.MtOffEyeSph+mtCcCenter, eye)
	store(t, m, mt.Addr+addr.MtOffMode, []byte{mode})
	store(t, m, mt.Addr+mtOffF470, f32bits(f470))
	store(t, m, mt.Addr+mtOffWiggle, []byte{0xFA, 0x42}) // -1470
	store(t, m, mt.Addr+addr.MtOffRingIdx, u32b(0x6B))
	const mtd, exec = uint32(0x80D00000), uint32(0x80D16188)
	store(t, m, mt.Addr+addr.ActorOffSubMtd, u32b(mtd))
	store(t, m, mtd+addr.MethodOffExecute, u32b(exec))
	for i := range uint32(4) {
		store(t, m, exec+4*i, u32b(mtFpExec[i]))
		store(t, m, exec-mtExecOff+mtBc1Off+4*i, u32b(mtFpBc1[i]))
		store(t, m, exec-mtExecOff+mtBc2Off+4*i, u32b(mtFpBc2[i]))
	}
	store(t, m, exec-mtExecOff+mtBc1Off+mtBc1Blr, u32b(blrWord))
	store(t, m, exec-mtExecOff+mtBc2Off+mtBc2Blr, u32b(blrWord))
	return m, mt.Addr, segs
}

// TestMt2Probe: mt2-probe's walk line has the exec/bc1/bc2 words and
// fp=ok; each sample has mode, ring idx, f470, wiggle and per segment
// tr, col2, sph and its residual; a MOVE-only run is INCOMPLETE, and a
// sphere moved by 1 unit is OVER and FAILs.
func TestMt2Probe(t *testing.T) {
	m, at, _ := mtRoom(t, 0, -10)
	run := func() string {
		t.Helper()
		var out bytes.Buffer
		if err := mt2Probe(context.Background(), &out, m, 80*time.Millisecond); err != nil {
			t.Fatalf("%v\n%s", err, out.String())
		}
		return out.String()
	}
	s := run()
	for _, want := range []string{
		`stage "M_Dai" room 1; P0-MT2 wants M_NewD2 14 8 with MOVE and FIGHT`,
		"params 0xFF027FFF exec 0x80D16188 [9421FCC0 7C0802A6 90010344 DBE10330] bc1 0x80D11F10 [9421FF90 7C0802A6 90010074 39610070] blr 4E800020 bc2 0x80D10B28 [9421FE60 7C0802A6 900101A4 DBE10190] blr 4E800020 fp=ok\n",
		"mode 0 MOVE action 0 ring 43 dive 0 f470 -10.00 (built -10.00) wiggle -1470 hit_cool 0 bite 0 pos 5000.0 0.0 0.0",
		"| seg0 tr 900.0 4121.0 4490.0 col2 0.0000 0.0000 0.8000 sph 900.0 4121.0 4570.0 d 0.000",
		"| seg1 tr 925.0 4123.0 4520.0 col2 0.2955 0.0000 0.9553 sph ",
		"| eye 900.0 4121.0 4514.0 de 0.000 | max 0.000 slope 14.0 ok\n",
		"0 over 0.2, sloped ",
		"P0-MT2 INCOMPLETE pid ",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	if n := strings.Count(s, " MOVE action "); n != 1 {
		t.Errorf("%d sample lines for an unchanged Magtail, want 1 (repeats are not printed):\n%s", n, s)
	}
	// 1 unit up: across every col2 (y 0), so no build f470 absorbs it.
	c3 := at + addr.MtOffBodySph + 3*addr.MtBodySphStride + mtCcCenter + 4
	v, _ := mem.ReadF32(m, c3)
	store(t, m, c3, f32bits(v+1))
	s = run()
	if !strings.Contains(s, " OVER\n") || !strings.Contains(s, "P0-MT2 FAIL pid ") || !strings.Contains(s, "max residual MOVE 1.000") {
		t.Errorf("a sphere 1 unit off:\n%s", s)
	}
	store(t, m, at+addr.MtOffMorf, u32b(0))
	if s := run(); !strings.Contains(s, "| seg0 tr - sph ") || !strings.Contains(s, " skip\n") || !strings.Contains(s, "no matrix ") {
		t.Errorf("no head matrix:\n%s", s)
	}
}

// TestMt2Verdict: PASS needs a MOVE and a FIGHT sample within 0.2; a
// torn sample and a BALL sample over the bound are left out; one FIGHT
// sample over it FAILs.
func TestMt2Verdict(t *testing.T) {
	a := &mt2Acc{pid: 1, byMode: map[uint8]int{}}
	a.add(&mt2Sample{Mode: 0, AllMatrix: true, MaxResid: 0.1})
	if a.verdict() != "INCOMPLETE" {
		t.Errorf("MOVE only: %s", a.verdict())
	}
	a.add(&mt2Sample{Mode: 1, AllMatrix: true, MaxResid: 0.15})
	a.add(&mt2Sample{Mode: 2, AllMatrix: true, MaxResid: 70})
	a.add(&mt2Sample{Mode: 1, AllMatrix: true, MaxResid: 9, Torn: true})
	if a.verdict() != "PASS" || a.torn != 1 || a.byMode[2] != 1 {
		t.Errorf("MOVE + FIGHT: %s torn %d\n%s", a.verdict(), a.torn, a.summary())
	}
	if s := a.summary(); !strings.Contains(s, "4 samples (MOVE 1, FIGHT 1, BALL 1), torn 1, no matrix 0, max residual MOVE 0.100 FIGHT 0.150") {
		t.Errorf("summary:\n%s", s)
	}
	a.add(&mt2Sample{Mode: 1, AllMatrix: true, MaxResid: 0.3})
	if a.verdict() != "FAIL" {
		t.Errorf("a FIGHT sample over: %s", a.verdict())
	}
}

// TestMtExecTextMiss: one changed bc2 word is fp=miss.
func TestMtExecTextMiss(t *testing.T) {
	m, at, _ := mtRoom(t, 1, 20)
	store(t, m, 0x80D16188-mtExecOff+mtBc2Off+8, u32b(0x60000000))
	if s, err := mtExecText(m, at); err != nil || !strings.HasSuffix(s, " fp=miss") {
		t.Errorf("%q %v", s, err)
	}
}

// TestRegionProbeCoversRefresh: P0-L5's span 0x804137B0..0x804137D0
// (the refresh block, R-D4) passed, so G-D5 moved CanaryArenaHigh past it:
// the block is now mailbox.RefreshRegion, which region-probe never arms
// (canaryForbidden lists it) and no canary region covers.
func TestRegionProbeCoversRefresh(t *testing.T) {
	const start, end = uint32(0x804137B0), uint32(0x804137D0)
	if mailbox.Mailbox+0x8B0 != start || mailbox.RefreshRegion.Start != start || mailbox.RefreshRegion.End != end {
		t.Fatalf("the refresh block moved: mailbox 0x%08X, %+v", mailbox.Mailbox, mailbox.RefreshRegion)
	}
	for _, r := range canaryRegions {
		if start < r.End && r.Start < end {
			t.Errorf("%s 0x%08X..0x%08X still covers the refresh block", r.Name, r.Start, r.End)
		}
	}
	forbidden := false
	for _, f := range canaryForbidden() {
		forbidden = forbidden || (f.Start <= start && end <= f.End)
	}
	if !forbidden || canarySafe(mailbox.RefreshRegion) == nil {
		t.Error("region-probe may arm the refresh block")
	}
}

// TestMt2F470Eased: the game eases f470 after the build, so a sample
// built at -10 can read -9 (MOVE -> FIGHT) and stays within the bound;
// a build more than one step from the f470 read does not.
func TestMt2F470Eased(t *testing.T) {
	m, at, _ := mtRoom(t, 1, -10)
	store(t, m, at+mtOffF470, f32bits(-9))
	pid, _ := mem.ReadU32(m, at+addr.ActorOffBsPcID)
	a := &actors.Actor{PID: pid, Addr: at, Proc: procMt}
	s, err := readMt2(m, a)
	if err != nil {
		t.Fatal(err)
	}
	if s.Torn || math.Abs(s.F470Build+10) > 1e-3 || s.MaxResid > 1e-3 {
		t.Errorf("eased one step: torn %v built %.3f max %.3f", s.Torn, s.F470Build, s.MaxResid)
	}
	store(t, m, at+mtOffF470, f32bits(-7))
	if s, err = readMt2(m, a); err != nil {
		t.Fatal(err)
	}
	if math.Abs(s.F470Build+8) > 1e-3 || s.MaxResid < 1.9 {
		t.Errorf("three steps off: built %.3f max %.3f", s.F470Build, s.MaxResid)
	}
}

// tearMem moves a body sphere centre after every class read, as a game
// frame that ran between the probe's reads would.
type tearMem struct {
	*mem.FakeMem
	t    *testing.T
	at   uint32
	left int
}

func (r *tearMem) Read(a uint32, n int) ([]byte, error) {
	b, err := r.FakeMem.Read(a, n)
	if err == nil && a == r.at && n == addr.MtClassSize && r.left > 0 {
		r.left--
		c := r.at + addr.MtOffBodySph + 5*addr.MtBodySphStride + mtCcCenter
		v, _ := mem.ReadF32(r.FakeMem, c)
		store(r.t, r.FakeMem, c, f32bits(v+0.5))
	}
	return b, err
}

// TestMt2Torn: a FIGHT frame between the two class reads (ring idx
// unchanged) is retried, and torn after mtTornTries.
func TestMt2Torn(t *testing.T) {
	m, at, _ := mtRoom(t, 1, 20)
	pid, _ := mem.ReadU32(m, at+addr.ActorOffBsPcID)
	a := &actors.Actor{PID: pid, Addr: at, Proc: procMt}
	r := &tearMem{FakeMem: m, t: t, at: at, left: 1}
	if s, err := readMt2(r, a); err != nil || s.Torn {
		t.Errorf("one tear, retried: torn %v %v", s.Torn, err)
	}
	r.left = 2 * mtTornTries
	if s, err := readMt2(r, a); err != nil || !s.Torn {
		t.Errorf("a tear every try: torn %v %v", s.Torn, err)
	}
}
