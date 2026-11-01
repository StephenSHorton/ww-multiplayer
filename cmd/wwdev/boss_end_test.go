package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// bossFix is a fake game in a boss's stage with its actors placed and
// executing.
type bossFix struct {
	m  *mem.FakeMem
	g  *fakegame.FakeGame
	es *fakegame.Enemies
	at []uint32 // each placed actor's address, in placement order
}

// bossRoom loads stage name (table tbl) with Link in room and places es
// there, in order: the fake gives each a 0x3000 block, so a boss struct
// larger than that reaches into the next blocks; the fixtures place the
// biggest last and touch no offset that another placed actor holds.
func bossRoom(t *testing.T, name string, room, tbl int, es ...fakegame.Enemy) *bossFix {
	t.Helper()
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	if err := g.LoadStage(fakegame.Stage{Name: name, Table: tbl, Room: int8(room)}); err != nil {
		t.Fatal(err)
	}
	for i := range es {
		es[i].Stage, es[i].Room = name, room
		es[i].Pos = [3]float32{5000, 0, float32(1000 * i)}
		if es[i].SetID == 0 {
			es[i].SetID = addr.ActorSetIDNone
		}
	}
	p := fakegame.PlaceEnemies(es...)
	for range 4 {
		if err := g.FrameWith(p); err != nil {
			t.Fatal(err)
		}
	}
	f := &bossFix{m: m, g: g, es: p}
	for i := range es {
		l, ok := p.Of(i)
		if !ok || l.InitState != 2 {
			t.Fatalf("placed actor %d: %+v %v", i, l, ok)
		}
		f.at = append(f.at, l.Addr)
	}
	return f
}

func (f *bossFix) u32(t *testing.T, a, v uint32) { t.Helper(); store(t, f.m, a, u32b(v)) }

func (f *bossFix) u8(t *testing.T, a uint32, v byte) { t.Helper(); store(t, f.m, a, []byte{v}) }

// run runs boss-end with o's boss and flags over m (no watch unless set),
// recording the writes.
func runBossEndOn(t *testing.T, m mem.Mem, fm *mem.FakeMem, o bossEndOpts) (bossOutcome, string, []mem.WriteRecord) {
	t.Helper()
	if o.Tick == 0 {
		o.Tick = time.Millisecond
	}
	fm.ResetWrites()
	fm.Record(true)
	defer fm.Record(false)
	var out bytes.Buffer
	r := bossEnd(context.Background(), &out, m, o)
	return r, out.String(), fm.Writes()
}

func (f *bossFix) run(t *testing.T, o bossEndOpts) (bossOutcome, string, []mem.WriteRecord) {
	t.Helper()
	return runBossEndOn(t, f.m, f.m, o)
}

// raceMem is the game answering a write: after the nth tool write it runs
// f (which Stores what the game would leave).
type raceMem struct {
	*mem.FakeMem
	n, after int
	f        func()
}

func (r *raceMem) Write(a uint32, b []byte) error {
	err := r.FakeMem.Write(a, b)
	r.n++
	if r.n == r.after && r.f != nil {
		r.f()
	}
	return err
}

type wr struct {
	a uint32
	v uint32
}

// wantWrites checks the exact write list: address, 4 bytes, order.
func wantWrites(t *testing.T, got []mem.WriteRecord, want ...wr) {
	t.Helper()
	var g, w []string
	for _, x := range got {
		g = append(g, fmt.Sprintf("%08X=% X", x.Addr, x.Data))
	}
	for _, x := range want {
		w = append(w, fmt.Sprintf("%08X=% X", x.a, u32b(x.v)))
	}
	if strings.Join(g, "\n") != strings.Join(w, "\n") {
		t.Errorf("writes:\n%s\nwant:\n%s", strings.Join(g, "\n"), strings.Join(w, "\n"))
	}
}

// wantInside checks every write lies in one of spans and never in the
// save or the boss_kill request.
func wantInside(t *testing.T, got []mem.WriteRecord, spans ...memSpan) {
	t.Helper()
	for _, x := range got {
		in := false
		for _, s := range spans {
			if s.holds(x.Addr, uint32(len(x.Data))) {
				in = true
			}
		}
		if !in {
			t.Errorf("write at 0x%08X outside %s", x.Addr, spanNames(spans))
		}
		if x.Addr < state.SaveRegion.End && x.End() > state.SaveRegion.Start {
			t.Errorf("write at 0x%08X is in the save", x.Addr)
		}
		if x.Addr < mailbox.BossRequestRegion.End && x.End() > mailbox.BossRequestRegion.Start {
			t.Errorf("write at 0x%08X is in the boss_kill request", x.Addr)
		}
	}
}

func span(a, size uint32) memSpan { return memSpan{"actor", a, a + size} }

func wantOut(t *testing.T, r bossOutcome, out string, want bossOutcome, subs ...string) {
	t.Helper()
	if r != want {
		t.Errorf("outcome %v, want %v:\n%s", r, want, out)
	}
	for _, s := range subs {
		if !strings.Contains(out, s) {
			t.Errorf("output lacks %q:\n%s", s, out)
		}
	}
}

// ---- Gohma

func gohmaFix(t *testing.T) *bossFix {
	f := bossRoom(t, "Xboss0", 0, addr.TableGT, fakegame.Enemy{Proc: stage.ProcBTD})
	f.u32(t, f.at[0]+btdOffAct, 0x00010005)
	return f
}

func TestBossEndGohma(t *testing.T) {
	f := gohmaFix(t)
	r, out, ws := f.run(t, bossEndOpts{Boss: "gohma"})
	wantOut(t, r, out, bossOK, "read-back: m02E4 0xB, mGohmaState 0x32")
	wantWrites(t, ws, wr{f.at[0] + 0x2E4, 0x000B0032})
	wantInside(t, ws, span(f.at[0], sizeBtd))
	if r.exitCode() != 0 {
		t.Errorf("exit %d", r.exitCode())
	}
	// A second run: the action already reads end.
	r, out, ws = f.run(t, bossEndOpts{Boss: "gohma"})
	wantOut(t, r, out, bossAlready, "ALREADY: Btd pid")
	wantWrites(t, ws)
}

func TestBossEndGohmaRockNote(t *testing.T) {
	f := gohmaFix(t)
	f.u8(t, f.at[0]+btdOffRock, 1)
	r, out, _ := f.run(t, bossEndOpts{Boss: "gohma"})
	wantOut(t, r, out, bossOK, "note: m02F6 is 1")
}

func TestBossEndGohmaRace(t *testing.T) {
	f := gohmaFix(t)
	rm := &raceMem{FakeMem: f.m, after: 1, f: func() { f.u32(t, f.at[0]+btdOffAct, 0x00020014) }}
	r, out, _ := runBossEndOn(t, rm, f.m, bossEndOpts{Boss: "gohma"})
	wantOut(t, r, out, bossRace, "RACE: m02E4 0x2, not 0xB", "recovery: ")
	if r.exitCode() != 2 {
		t.Errorf("exit %d", r.exitCode())
	}
}

// The common refusals: each with zero writes.
func TestBossEndRefusals(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(t *testing.T, f *bossFix)
		out  bossOutcome
		want string
	}{
		{"NextEnable", func(t *testing.T, f *bossFix) { f.u8(t, addr.NextStageEnable, 1) }, bossRefused, "a stage change is pending"},
		{"event", func(t *testing.T, f *bossFix) { f.u8(t, addr.EvtMode, 1) }, bossRefused, "an event is running or queued"},
		{"order", func(t *testing.T, f *bossFix) { f.u8(t, addr.EvtOrderCount, 1) }, bossRefused, "an event is running or queued"},
		{"menu", func(t *testing.T, f *bossFix) { f.u8(t, addr.MenuPause, 1) }, bossRefused, "the pause menu is up"},
		{"no Link", func(t *testing.T, f *bossFix) { f.u32(t, addr.PlayerPtr, 0) }, bossRefused, "no Link"},
		{"creating", func(t *testing.T, f *bossFix) { f.u8(t, f.at[0]+addr.ActorOffInitState, 1) }, bossRefused, "creating: Btd pid"},
		{"intro", func(t *testing.T, f *bossFix) { f.u32(t, f.at[0]+btdOffAct, 0x000A0000) }, bossRefused, "m02E4 0xA is not 0, 1 or 2"},
		{"bit", func(t *testing.T, f *bossFix) { f.g.OnEventBit(evGohmaTrials) }, bossAlready, "ALREADY: event 0x3240"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := gohmaFix(t)
			c.set(t, f)
			r, out, ws := f.run(t, bossEndOpts{Boss: "gohma"})
			wantOut(t, r, out, c.out, c.want)
			wantWrites(t, ws)
			if r.exitCode() != 1 {
				t.Errorf("exit %d", r.exitCode())
			}
		})
	}
}

func TestBossEndWrongStage(t *testing.T) {
	f := bossRoom(t, "M_DragB", 0, addr.TableDRC, fakegame.Enemy{Proc: stage.ProcBTD})
	r, out, ws := f.run(t, bossEndOpts{Boss: "gohma"})
	wantOut(t, r, out, bossRefused, `stage "M_DragB", not Gohma's Xboss0`)
	wantWrites(t, ws)
}

func TestBossEndNoActorAmbiguous(t *testing.T) {
	f := bossRoom(t, "Xboss0", 0, addr.TableGT, fakegame.Enemy{Proc: stage.ProcBMD})
	r, out, ws := f.run(t, bossEndOpts{Boss: "gohma"})
	wantOut(t, r, out, bossRefused, "no actor: no Btd (proc 0x0EB)")
	wantWrites(t, ws)
	f = bossRoom(t, "Xboss0", 0, addr.TableGT, fakegame.Enemy{Proc: stage.ProcBTD}, fakegame.Enemy{Proc: stage.ProcBTD})
	r, out, ws = f.run(t, bossEndOpts{Boss: "gohma"})
	wantOut(t, r, out, bossRefused, "ambiguous: 2 Btd actors")
	wantWrites(t, ws)
}

func TestBossEndDryRun(t *testing.T) {
	f := gohmaFix(t)
	r, out, ws := f.run(t, bossEndOpts{Boss: "gohma", DryRun: true})
	wantOut(t, r, out, bossDryRun, fmt.Sprintf("1. 0x%08X Btd+0x2E4 mask FFFFFFFF value 000B0032", f.at[0]+0x2E4), "DRY-RUN: nothing written")
	wantWrites(t, ws)
	if r.exitCode() != 0 {
		t.Errorf("exit %d", r.exitCode())
	}
}

// TestBossEndWatchGohma: the game sets the bit and changes stage right
// after the write; the watch sees the load and the bit and is done.
func TestBossEndWatchGohma(t *testing.T) {
	f := gohmaFix(t)
	rm := &raceMem{FakeMem: f.m, after: 1, f: func() {
		f.g.OnEventBit(evGohmaTrials)
		if err := f.g.LoadStage(fakegame.Stage{Name: "GanonA", Table: addr.TableGT}); err != nil {
			t.Fatal(err)
		}
	}}
	// The read-back sees the new stage's memory: the old Btd block keeps
	// its write, since the fake does not clear freed blocks until reuse.
	r, out, _ := runBossEndOn(t, rm, f.m, bossEndOpts{Boss: "gohma", Watch: time.Second})
	wantOut(t, r, out, bossOK, "DONE after", `stage "GanonA"`, "event 0x3240 set")
}

func TestBossEndWatchTimeout(t *testing.T) {
	f := gohmaFix(t)
	r, out, _ := f.run(t, bossEndOpts{Boss: "gohma", Watch: 30 * time.Millisecond})
	wantOut(t, r, out, bossTimeout, "TIMEOUT: the watch ran")
	if r.exitCode() != 3 {
		t.Errorf("exit %d", r.exitCode())
	}
}

// ---- Kalle Demos

func kalleFix(t *testing.T) *bossFix {
	f := bossRoom(t, "Xboss1", 0, addr.TableGT, fakegame.Enemy{Proc: stage.ProcBMD, Health: 15})
	a := f.at[0]
	f.u32(t, a+0x300, 0x00030002)
	f.u32(t, a+0x304, 0x0001AAAA)
	f.u32(t, a+0x310, 0x0000BEEF) // m312 sentinel
	f.u32(t, a+0xB74, 0x00001234)
	f.u32(t, a+0xBE0, 0x00ABCDEF)
	return f
}

func TestBossEndKalle(t *testing.T) {
	f := kalleFix(t)
	a := f.at[0]
	r, out, ws := f.run(t, bossEndOpts{Boss: "kalle"})
	wantOut(t, r, out, bossOK, "read-back: mMode 0xB m302 0")
	wantWrites(t, ws,
		wr{a + 0x304, 0x00010064},
		wr{a + 0x310, 0x4E20BEEF},
		wr{a + 0xB74, 0x00640000},
		wr{a + 0xBDC, 0x3F800000},
		wr{a + 0xBE0, 0x02ABCDEF},
		wr{a + 0x300, 0x000B0000}, // mMode last
	)
	wantInside(t, ws, span(a, sizeBmd))
}

func TestBossEndKalleFlower(t *testing.T) {
	f := kalleFix(t)
	a := f.at[0]
	f.u32(t, a+0x300, 0x00010000) // not fallen
	f.u32(t, a+0x304, 0x0000AAAA)
	r, out, ws := f.run(t, bossEndOpts{Boss: "kalle"})
	wantOut(t, r, out, bossRefused, "mMode 1 m302 0, not the open fallen flower", "m304 0 (the core is in)")
	wantWrites(t, ws)
	r, out, ws = f.run(t, bossEndOpts{Boss: "kalle", Step: "flower"})
	wantOut(t, r, out, bossOK)
	wantWrites(t, ws,
		wr{a + 0x300, 0x00030002},
		wr{a + 0x304, 0x0001AAAA},
		wr{a + 0x310, 0x0000BEEF},
		wr{a + 0xB74, 0x00001234},
		wr{a + 0x304, 0x00010064},
		wr{a + 0x310, 0x4E20BEEF},
		wr{a + 0xB74, 0x00640000},
		wr{a + 0xBDC, 0x3F800000},
		wr{a + 0xBE0, 0x02ABCDEF},
		wr{a + 0x300, 0x000B0000},
	)
}

func TestBossEndKalleNatural(t *testing.T) {
	f := kalleFix(t)
	a := f.at[0]
	f.u32(t, a+0x284, 0x0F0F1122)
	r, out, ws := f.run(t, bossEndOpts{Boss: "kalle", Variant: "natural"})
	wantOut(t, r, out, bossOK, "read-back: health 1")
	wantWrites(t, ws, wr{a + 0x284, 0x0F011122}) // max_health and the pad kept
}

// ---- Jalhalla

func jalhallaFix(t *testing.T) *bossFix {
	f := bossRoom(t, "Xboss2", 0, addr.TableGT, fakegame.Enemy{Proc: stage.ProcBPW, Health: 15})
	f.u8(t, f.at[0]+mailbox.BossBpwTypeOff, 0)
	f.u32(t, f.at[0]+0x284, 0x0F0F1122)
	return f
}

func TestBossEndJalhalla(t *testing.T) {
	f := jalhallaFix(t)
	a := f.at[0]
	r, out, ws := f.run(t, bossEndOpts{Boss: "jalhalla"})
	wantOut(t, r, out, bossOK, "read-back: health 0, mBodyAction 3 mActionState 0x64")
	wantWrites(t, ws, wr{a + 0x284, 0x0F001122}, wr{a + 0x444, 0x00030064}) // health first
	wantInside(t, ws, span(a, sizeBpw))
}

func TestBossEndJalhallaPoe(t *testing.T) {
	f := bossRoom(t, "Xboss2", 0, addr.TableGT, fakegame.Enemy{Proc: stage.ProcBPW, Health: 15}, fakegame.Enemy{Proc: stage.ProcPW})
	r, out, ws := f.run(t, bossEndOpts{Boss: "jalhalla"})
	wantOut(t, r, out, bossRefused, "1 Poes (proc 0xD5) alive")
	wantWrites(t, ws)
}

func TestBossEndJalhallaNatural(t *testing.T) {
	f := jalhallaFix(t)
	r, out, ws := f.run(t, bossEndOpts{Boss: "jalhalla", Variant: "natural"})
	wantOut(t, r, out, bossOK, "read-back: health 0")
	wantWrites(t, ws, wr{f.at[0] + 0x284, 0x0F001122})
}

// ---- Molgera

func molgeraFix(t *testing.T) *bossFix {
	f := bossRoom(t, "Xboss3", 0, addr.TableGT, fakegame.Enemy{Proc: stage.ProcBWD})
	a := f.at[0]
	f.u32(t, a+0x2C0, 0x80C00000)
	f.u32(t, a+0x18AC, 0x12340001) // m18AC sentinel, m18AE 1
	f.u32(t, a+0x18B0, 0x0000ABCD) // m18B0 0, pad sentinel
	f.u32(t, a+0x1BB0, 0x01020304)
	f.u32(t, a+0x1BB4, 0x00005566)
	return f
}

func TestBossEndMolgera(t *testing.T) {
	f := molgeraFix(t)
	a := f.at[0]
	r, out, ws := f.run(t, bossEndOpts{Boss: "molgera"})
	wantOut(t, r, out, bossOK, "read-back: m18AE 0x3")
	wantWrites(t, ws,
		wr{a + 0x1BB4, 0x03035566},
		wr{a + 0x1BB0, 0x00780000},
		wr{a + 0x18AC, 0x12340003}, // m18AE before m18B0
		wr{a + 0x18B0, 0x0002ABCD},
	)
	wantInside(t, ws, span(a, sizeBwd))
}

func TestBossEndMolgeraRefusals(t *testing.T) {
	for _, c := range []struct {
		w18AC, w18B0 uint32
		want         string
	}{
		{0x12340005, 0, "m18AE 5 != 1 (wait)"},
		{0x12340001, 0xFFF60000, "m18B0 -10 (Link has not landed)"},
	} {
		f := molgeraFix(t)
		f.u32(t, f.at[0]+0x18AC, c.w18AC)
		f.u32(t, f.at[0]+0x18B0, c.w18B0)
		r, out, ws := f.run(t, bossEndOpts{Boss: "molgera"})
		wantOut(t, r, out, bossRefused, c.want)
		wantWrites(t, ws)
	}
}

func TestBossEndMolgeraRace(t *testing.T) {
	f := molgeraFix(t)
	a := f.at[0]
	rm := &raceMem{FakeMem: f.m, after: 4, f: func() { f.u32(t, a+0x18AC, 0x12340005) }}
	r, out, _ := runBossEndOn(t, rm, f.m, bossEndOpts{Boss: "molgera"})
	wantOut(t, r, out, bossRace, "RACE: m18AE 0x5, not 3 or 0xC", "eat_attack")
}

func TestBossEndMolgeraNatural(t *testing.T) {
	f := molgeraFix(t)
	a := f.at[0]
	f.u32(t, a+0x18AC, 0x12340003)
	f.u32(t, a+0x18B0, 0x0002ABCD)
	f.u32(t, a+0x1BB0, 0x00500000)
	r, out, ws := f.run(t, bossEndOpts{Boss: "molgera", Variant: "natural"})
	wantOut(t, r, out, bossOK)
	wantWrites(t, ws, wr{a + 0x1BB4, 0x03035566})
}

// ---- Phantom Ganon, the maze

func pgMazeFix(t *testing.T) *bossFix {
	f := bossRoom(t, "GanonJ", 13, addr.TableGT, fakegame.Enemy{Proc: stage.ProcFganon, Params: fgParamsMaze, Health: 100})
	a := f.at[0]
	f.u32(t, a+fgOffAct, 0x00420005)
	f.u32(t, a+fgOffMode, 0x00017777)
	return f
}

func TestBossEndPGMaze(t *testing.T) {
	f := pgMazeFix(t)
	a := f.at[0]
	r, out, ws := f.run(t, bossEndOpts{Boss: "pg-maze"})
	wantOut(t, r, out, bossOK, "read-back: mAction 3")
	wantWrites(t, ws, wr{a + 0x384, 0x00420003}, wr{a + 0x388, 0})
	wantInside(t, ws, span(a, sizeFganon))
}

func TestBossEndPGMazeRefusals(t *testing.T) {
	f := pgMazeFix(t)
	store(t, f.m, addr.SaveInfo+addr.InfoOffSavedata+addr.OffItems+addr.SlotBow, []byte{save.ItemLightArrow})
	r, out, ws := f.run(t, bossEndOpts{Boss: "pg-maze"})
	wantOut(t, r, out, bossRefused, "holds the Light Arrow")
	wantWrites(t, ws)

	for _, act := range []uint32{6, 11} {
		f = pgMazeFix(t)
		f.u32(t, f.at[0]+fgOffAct, 0x00420000|act)
		r, out, ws = f.run(t, bossEndOpts{Boss: "pg-maze"})
		wantOut(t, r, out, bossRefused, fmt.Sprintf("mAction %d is not 5 or 9", act))
		wantWrites(t, ws)
	}

	f = pgMazeFix(t)
	if err := f.g.OnSwitch(232, 13); err != nil {
		t.Fatal(err)
	}
	r, out, ws = f.run(t, bossEndOpts{Boss: "pg-maze"})
	wantOut(t, r, out, bossAlready, "switch 232")
	wantWrites(t, ws)

	f = bossRoom(t, "GanonJ", 12, addr.TableGT, fakegame.Enemy{Proc: stage.ProcFganon, Params: fgParamsMaze})
	r, out, _ = f.run(t, bossEndOpts{Boss: "pg-maze"})
	wantOut(t, r, out, bossRefused, "Link is in room 12 of GanonJ, not room 13")
}

func TestBossEndPGMazeNatural(t *testing.T) {
	f := pgMazeFix(t)
	a := f.at[0]
	r, out, ws := f.run(t, bossEndOpts{Boss: "pg-maze", Variant: "natural"})
	wantOut(t, r, out, bossOK, "read-back: mAction 8")
	wantWrites(t, ws, wr{a + 0x384, 0x00420008}, wr{a + 0x388, 0})
}

// ---- Phantom Ganon, the third fight

func pg3Fix(t *testing.T) *bossFix {
	f := bossRoom(t, "GanonM", 0, addr.TableGT, fakegame.Enemy{Proc: stage.ProcFganon, Params: fgParamsThird, Health: 100})
	a := f.at[0]
	f.u32(t, a+fgOffAct, 0x00420009)
	f.u32(t, a+fgOffMode, 0x00018888) // mMode 1, m38A sentinel
	f.u32(t, a+fgOffTimer, 0x00050020)
	f.u8(t, a+fgOffShown, 1)
	return f
}

func TestBossEndPG3(t *testing.T) {
	f := pg3Fix(t)
	a := f.at[0]
	rm := &raceMem{FakeMem: f.m, after: 2, f: func() { f.u32(t, a+fgOffB54, 100<<16) }}
	r, out, ws := runBossEndOn(t, rm, f.m, bossEndOpts{Boss: "pg3"})
	wantOut(t, r, out, bossOK, "read-back: mB54 100")
	wantWrites(t, ws, wr{a + 0x384, 0x00420016}, wr{a + 0x388, 0x00008888}) // mAction 22 before mMode 0
	wantInside(t, ws, span(a, sizeFganon))
}

func TestBossEndPG3Race(t *testing.T) {
	f := pg3Fix(t)
	a := f.at[0]
	rm := &raceMem{FakeMem: f.m, after: 2, f: func() { f.u32(t, a+fgOffB54, 1<<16) }}
	r, out, _ := runBossEndOn(t, rm, f.m, bossEndOpts{Boss: "pg3"})
	wantOut(t, r, out, bossRace, "RACE: mB54 1, not 100 or 101", "recovery: load a save state")
	// Nothing moves mB54: still 0 at the deadline is a RACE too.
	f = pg3Fix(t)
	r, out, _ = f.run(t, bossEndOpts{Boss: "pg3"})
	wantOut(t, r, out, bossRace, "RACE: mB54 still 0 after 500ms")
}

func TestBossEndPG3Refusals(t *testing.T) {
	for _, c := range []struct {
		off, v uint32
		want   string
	}{
		{fgOffMode, 0x00008888, "mMode 0 != 1"},
		{fgOffB54, 0x00030000, "mB54 3 != 0"},
		{fgOffTimer, 0x0005000A, "m3A4[1] 10 < 15"},
		{fgOffAct, 0x00420006, "mAction 6 is not 5 or 9"},
	} {
		f := pg3Fix(t)
		f.u32(t, f.at[0]+c.off, c.v)
		r, out, ws := f.run(t, bossEndOpts{Boss: "pg3"})
		wantOut(t, r, out, bossRefused, c.want)
		wantWrites(t, ws)
	}
	// A clone (m68F != 0) is not the master.
	f := pg3Fix(t)
	f.u8(t, f.at[0]+fgOffClone, 2)
	r, out, ws := f.run(t, bossEndOpts{Boss: "pg3"})
	wantOut(t, r, out, bossRefused, "no actor: no Fganon")
	wantWrites(t, ws)
}

func TestBossEndPG3Door(t *testing.T) {
	f := bossRoom(t, "GanonM", 0, addr.TableGT, fakegame.Enemy{Proc: stage.ProcVfan, Params: 0xFFFFFF18})
	a := f.at[0]
	r, out, ws := f.run(t, bossEndOpts{Boss: "pg3", Step: "door"})
	wantOut(t, r, out, bossOK)
	wantWrites(t, ws, wr{a + 0x440, 1})
	wantInside(t, ws, span(a, sizeVfan))

	f = bossRoom(t, "GanonM", 0, addr.TableGT, fakegame.Enemy{Proc: stage.ProcVfan, Params: 0xFFFFFF18}, fakegame.Enemy{Proc: stage.ProcFganon, Params: fgParamsThird})
	r, out, ws = f.run(t, bossEndOpts{Boss: "pg3", Step: "door"})
	wantOut(t, r, out, bossRefused, "still in the queue")
	wantWrites(t, ws)
}

// ---- Puppet Ganon

func puppetFix(t *testing.T, form int) *bossFix {
	f := bossRoom(t, "GanonK", 0, addr.TableGT,
		fakegame.Enemy{Proc: stage.ProcBGN, Health: 3},
		fakegame.Enemy{Proc: stage.ProcBGN2, Health: 3},
		fakegame.Enemy{Proc: stage.ProcBGN3, Health: 3})
	f.u32(t, f.at[0]+bgnOffForm, 0x01000000|uint32(form)<<16)
	for _, a := range f.at {
		f.u32(t, a+0x284, 0x03031122)
	}
	return f
}

func TestBossEndPuppetForms(t *testing.T) {
	// form 1: bgn
	f := puppetFix(t, 0)
	bgn := f.at[0]
	f.u32(t, bgn+bgnOffHead, 0x0102ABCD)
	rm := &raceMem{FakeMem: f.m, after: 3, f: func() { f.u8(t, bgn+0xCA62, 1) }}
	r, out, ws := runBossEndOn(t, rm, f.m, bossEndOpts{Boss: "puppet", Step: "form1"})
	wantOut(t, r, out, bossOK, "read-back: bgn mCSMode 1")
	wantWrites(t, ws, wr{bgn + 0x284, 0x03001122}, wr{bgn + 0xC778, 0x0000ABCD}, wr{bgn + 0xC748, 0x00060000})
	wantInside(t, ws, span(bgn, sizeBgn))

	// form 2: bgn2
	f = puppetFix(t, 1)
	bgn, b2 := f.at[0], f.at[1]
	f.u32(t, b2+0x310, 0x00070002)
	f.u32(t, b2+0x314, 0x0009BEEF)
	rm = &raceMem{FakeMem: f.m, after: 3, f: func() { f.u8(t, bgn+0xCA62, 10) }}
	r, out, ws = runBossEndOn(t, rm, f.m, bossEndOpts{Boss: "puppet", Step: "form2"})
	wantOut(t, r, out, bossOK, "read-back: bgn mCSMode 10")
	wantWrites(t, ws, wr{b2 + 0x284, 0x03001122}, wr{b2 + 0x314, 0x0000BEEF}, wr{b2 + 0x310, 0x00070006})
	wantInside(t, ws, span(b2, sizeBgn2))

	// form 3: bgn3, m0FD8A last
	f = puppetFix(t, 2)
	bgn, b3 := f.at[0], f.at[2]
	f.u32(t, b3+0xFD88, 0x5A5A0001)
	f.u32(t, b3+0xFD8C, 0x0004C0DE)
	f.u32(t, b3+0xFDB4, 0x0000BEEF)
	rm = &raceMem{FakeMem: f.m, after: 4, f: func() { f.u8(t, bgn+0xCA62, 20) }}
	r, out, ws = runBossEndOn(t, rm, f.m, bossEndOpts{Boss: "puppet", Step: "form3"})
	wantOut(t, r, out, bossOK, "read-back: bgn mCSMode 20")
	wantWrites(t, ws,
		wr{b3 + 0xFDB4, 0x2710BEEF},
		wr{b3 + 0x284, 0x03001122},
		wr{b3 + 0xFD8C, 0x0000C0DE},
		wr{b3 + 0xFD88, 0x5A5A0003}, // last
	)
	wantInside(t, ws, span(b3, sizeBgn3))
}

func TestBossEndPuppetRefusals(t *testing.T) {
	f := puppetFix(t, 1)
	r, out, ws := f.run(t, bossEndOpts{Boss: "puppet", Step: "form1"})
	wantOut(t, r, out, bossRefused, "bgn m02B5 1: the fight is in form 2, not --step form1")
	wantWrites(t, ws)

	f = puppetFix(t, 0)
	f.u8(t, f.at[0]+0xCA62, 1)
	r, out, ws = f.run(t, bossEndOpts{Boss: "puppet", Step: "form1"})
	wantOut(t, r, out, bossRefused, "bgn mCSMode 1 != 0")
	wantWrites(t, ws)

	f = puppetFix(t, 2)
	f.u32(t, f.at[2]+0xFD88, 0x0000000A)
	r, out, ws = f.run(t, bossEndOpts{Boss: "puppet", Step: "form3"})
	wantOut(t, r, out, bossRefused, "bgn3 m0FD8A 10 is not 0..2")
	wantWrites(t, ws)

	f = puppetFix(t, 2)
	f.g.OnEventBit(evPuppetGanon)
	r, out, ws = f.run(t, bossEndOpts{Boss: "puppet", Step: "form3"})
	wantOut(t, r, out, bossAlready, "event 0x3F10")
	wantWrites(t, ws)
}

func TestBossEndPuppetNatural(t *testing.T) {
	f := puppetFix(t, 2)
	r, out, ws := f.run(t, bossEndOpts{Boss: "puppet", Step: "form3", Variant: "natural"})
	wantOut(t, r, out, bossOK, "read-back: health 1")
	wantWrites(t, ws, wr{f.at[2] + 0x284, 0x03011122})
}

// ---- Ganondorf

func gndFix(t *testing.T, withPZ bool) *bossFix {
	es := []fakegame.Enemy{{Proc: stage.ProcGND, Health: 0x30}}
	if withPZ {
		es = append(es, fakegame.Enemy{Proc: stage.ProcPZ})
	}
	f := bossRoom(t, "GTower", 0, addr.TableGT, es...)
	a := f.at[0]
	f.u8(t, a+gndOffLive, 1)
	f.u32(t, a+gndOffAct, 0x00070000)
	f.u32(t, a+gndOffSub, 0x0000ABCD)
	f.u32(t, addr.PlayerInfo0Ptr, fakegame.PlayerActor)
	return f
}

func TestBossEndGanondorf(t *testing.T) {
	f := gndFix(t, true)
	a := f.at[0]
	rm := &raceMem{FakeMem: f.m, after: 3, f: func() { f.u32(t, a+gndOffDemo, 0x64) }}
	r, out, ws := runBossEndOn(t, rm, f.m, bossEndOpts{Boss: "ganondorf"})
	wantOut(t, r, out, bossOK, "read-back: +0x155E 0x64", "warning: the ending")
	wantWrites(t, ws,
		wr{a + 0x2D0, 0x0001ABCD}, // sub parked before the action
		wr{a + 0x2CC, 0x0000001E},
		wr{a + 0x2D0, 0x0000ABCD},
	)
	wantInside(t, ws, span(a, sizeGnd))
}

func TestBossEndGanondorfSubSet(t *testing.T) {
	// sub already 2: no park; the clear still runs once.
	f := gndFix(t, true)
	a := f.at[0]
	f.u32(t, a+gndOffSub, 0x0002ABCD)
	rm := &raceMem{FakeMem: f.m, after: 2, f: func() { f.u32(t, a+gndOffDemo, 0x64) }}
	r, out, ws := runBossEndOn(t, rm, f.m, bossEndOpts{Boss: "ganondorf"})
	wantOut(t, r, out, bossOK, "1. skipped: sub is 2")
	wantWrites(t, ws, wr{a + 0x2CC, 0x0000001E}, wr{a + 0x2D0, 0x0000ABCD})

	// The demo started between the action and the clear: no clear.
	f = gndFix(t, true)
	a = f.at[0]
	rm = &raceMem{FakeMem: f.m, after: 2, f: func() { f.u32(t, a+gndOffDemo, 0x64); f.u32(t, a+gndOffSub, 0x0001ABCD) }}
	r, out, ws = runBossEndOn(t, rm, f.m, bossEndOpts{Boss: "ganondorf"})
	wantOut(t, r, out, bossOK, "3. skipped: +0x155E is 0x64")
	wantWrites(t, ws, wr{a + 0x2D0, 0x0001ABCD}, wr{a + 0x2CC, 0x0000001E})
}

func TestBossEndGanondorfParry(t *testing.T) {
	f := gndFix(t, true)
	a, link := f.at[0], fakegame.PlayerActor
	f.u32(t, link+0x2A0, 0x00000001)
	rm := &raceMem{FakeMem: f.m, after: 3, f: func() { f.u32(t, a+gndOffDemo, 0x64) }}
	r, out, ws := runBossEndOn(t, rm, f.m, bossEndOpts{Boss: "ganondorf", Variant: "parry"})
	wantOut(t, r, out, bossOK)
	wantWrites(t, ws, wr{a + 0x2CC, 0x00000005}, wr{a + 0x2D0, 0x0000ABCD}, wr{link + 0x2A0, 0x20000001})
	wantInside(t, ws, span(a, sizeGnd), memSpan{"Link", link + 0x2A0, link + 0x2A4})
}

func TestBossEndGanondorfRefusals(t *testing.T) {
	f := gndFix(t, false)
	r, out, ws := f.run(t, bossEndOpts{Boss: "ganondorf"})
	wantOut(t, r, out, bossRefused, "no Zelda (PZ, proc 0xD3)")
	wantWrites(t, ws)

	f = gndFix(t, true)
	f.u8(t, f.at[0]+gndOffDemoID, 3)
	r, out, ws = f.run(t, bossEndOpts{Boss: "ganondorf"})
	wantOut(t, r, out, bossRefused, "+0x1C0 3 != 0")
	wantWrites(t, ws)

	f = gndFix(t, true)
	f.u32(t, addr.PlayerInfo0Ptr, 0x80A10000)
	r, out, ws = f.run(t, bossEndOpts{Boss: "ganondorf", Variant: "parry"})
	wantOut(t, r, out, bossRefused, "is not mpPlayerPtr[0]")
	wantWrites(t, ws)
}

// ---- the plan guard

func TestCheckBossPlan(t *testing.T) {
	a := uint32(0x80B00000)
	ok := &bossPlan{Spans: []memSpan{span(a, 0x100)}, Writes: []pokeWord{{Base: a, Off: 0xFC}}}
	if err := checkBossPlan(ok); err != nil {
		t.Error(err)
	}
	for name, p := range map[string]*bossPlan{
		"unaligned": {Spans: []memSpan{span(a, 0x100)}, Writes: []pokeWord{{Base: a, Off: 2}}},
		"outside":   {Spans: []memSpan{span(a, 0x100)}, Writes: []pokeWord{{Base: a, Off: 0x100}}},
		"save":      {Spans: []memSpan{span(addr.SaveInfo, 0x100)}, Writes: []pokeWord{{Base: addr.SaveInfo, Off: 0}}},
		"request":   {Spans: []memSpan{span(mailbox.BossAddr, 0x10)}, Writes: []pokeWord{{Base: mailbox.BossAddr, Off: 4}}},
	} {
		if err := checkBossPlan(p); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// ---- arguments

func TestParseBossEndArgs(t *testing.T) {
	o, err := parseBossEndArgs([]string{"pg3", "--step", "door", "--watch=5", "--dry-run"})
	if err != nil || o.Boss != "pg3" || o.Step != "door" || o.Watch != 5*time.Second || !o.DryRun {
		t.Errorf("%+v %v", o, err)
	}
	if o, err := parseBossEndArgs([]string{"gohma"}); err != nil || o.Watch != bossEndDefaultWatch {
		t.Errorf("%+v %v", o, err)
	}
	for _, args := range [][]string{
		{}, {"gohdan"}, {"gohma", "x"}, {"gohma", "--step", "door"}, {"puppet"},
		{"gohma", "--variant", "natural"}, {"ganondorf", "--variant", "natural"},
		{"gohma", "--watch", "-1"}, {"gohma", "--step"}, {"gohma", "--bogus"},
		{"pg3", "--step", "door", "--variant", "natural"},
		{"kalle", "--step", "flower", "--variant", "natural"},
	} {
		if _, err := parseBossEndArgs(args); err == nil {
			t.Errorf("%q accepted", args)
		}
	}
}

// ---- the watch judge

func TestBossEndPhase(t *testing.T) {
	x0 := stageRef{"Xboss0", 0, 0, 0}
	hub := stageRef{"GanonA", 1, 0, 0}
	base := bossObs{Stage: x0, Player: true, BitID: evGohmaTrials, State: "m02E4 0xB mGohmaState 0x32"}
	step := func(o bossObs, f func(*bossObs)) bossObs { f(&o); return o }

	// The trials: bit, NextEnable, the stage change, Link loaded: done.
	s1 := step(base, func(o *bossObs) { o.Bit, o.Tmp = true, true })
	ph, done, fail := bossEndPhase("gohma", base, s1)
	if ph != "event 0x3240 set; tmp 0x0480 set" || done || fail != "" {
		t.Errorf("bit: %q %v %q", ph, done, fail)
	}
	s2 := step(s1, func(o *bossObs) { o.NextEnable, o.Next = 1, hub })
	if ph, _, _ := bossEndPhase("gohma", s1, s2); !strings.Contains(ph, `NextEnable 1: next "GanonA" point 1`) {
		t.Errorf("next: %q", ph)
	}
	s3 := step(s2, func(o *bossObs) { o.NextEnable, o.Stage, o.Left, o.Player, o.Gone = 0, hub, true, false, true })
	if ph, done, _ := bossEndPhase("gohma", s2, s3); done || !strings.Contains(ph, "stage changed") {
		t.Errorf("left: %q %v", ph, done)
	}
	s4 := step(s3, func(o *bossObs) { o.Player = true })
	if ph, done, fail := bossEndPhase("gohma", s3, s4); !done || fail != "" || ph != "Link loaded" {
		t.Errorf("loaded: %q %v %q", ph, done, fail)
	}
	if _, done, fail := bossEndPhase("kalle", s3, step(s4, func(o *bossObs) { o.Bit = false })); done || !strings.Contains(fail, "without event") {
		t.Errorf("no bit: %v %q", done, fail)
	}

	// Jalhalla: a Poe at the landing fails.
	if _, _, fail := bossEndPhase("jalhalla", base, step(base, func(o *bossObs) { o.Poes = 2 })); !strings.Contains(fail, "2 Poes appeared") {
		t.Errorf("poe: %q", fail)
	}
	// Molgera: back to wait with m1BB5 3.
	if _, _, fail := bossEndPhase("molgera", base, step(base, func(o *bossObs) { o.A, o.B = 1, 3 })); !strings.Contains(fail, "re-run") {
		t.Errorf("molgera: %q", fail)
	}
	// Kalle: the heart container shows.
	if ph, _, _ := bossEndPhase("kalle", base, step(base, func(o *bossObs) { o.Extras = "NPC_CB1 (Makar)" })); ph != "seen: NPC_CB1 (Makar)" {
		t.Errorf("kalle extras: %q", ph)
	}

	// pg-maze: deleted, then waiting on switch 232; stuck at 3/1.
	pg := bossObs{Stage: stageRef{"GanonJ", 0, 13, 0}, Player: true, SwName: "room 13 switch 232", A: 3, B: 2}
	gone := step(pg, func(o *bossObs) { o.Gone = true })
	if ph, done, _ := bossEndPhase("pg-maze", pg, gone); done || ph != "the actor is gone" {
		t.Errorf("pg gone: %q %v", ph, done)
	}
	if ph, done, _ := bossEndPhase("pg-maze", gone, step(gone, func(o *bossObs) { o.Sw = true })); !done || ph != "room 13 switch 232 set" {
		t.Errorf("pg sw: %q %v", ph, done)
	}
	if _, _, fail := bossEndPhase("pg-maze", pg, step(pg, func(o *bossObs) { o.B, o.Since = 1, bossStuckSamples })); !strings.Contains(fail, "stuck") {
		t.Errorf("pg stuck: %q", fail)
	}
	if _, _, fail := bossEndPhase("pg-maze", pg, step(pg, func(o *bossObs) { o.A = 5 })); !strings.Contains(fail, "mAction 5") {
		t.Errorf("pg fell back: %q", fail)
	}
	if _, _, fail := bossEndPhase("pg-maze/natural", pg, step(pg, func(o *bossObs) { o.A = 8 })); fail != "" {
		t.Errorf("pg natural down: %q", fail)
	}

	// pg3: the first fight's intro fails; gone + switch is done.
	if _, _, fail := bossEndPhase("pg3", pg, step(pg, func(o *bossObs) { o.C = 2 })); !strings.Contains(fail, "load a save state") {
		t.Errorf("pg3: %q", fail)
	}
	if _, done, _ := bossEndPhase("pg3", pg, step(pg, func(o *bossObs) { o.Gone, o.Sw = true, true })); !done {
		t.Error("pg3 not done")
	}
	if _, done, _ := bossEndPhase("pg3/door", pg, step(pg, func(o *bossObs) { o.Bit, o.Sw = true, true })); !done {
		t.Error("door not done")
	}

	// Ganondorf: frame 0x7C, a wrong NextStage fails, the right one and
	// then the bit pass.
	g := bossObs{Stage: stageRef{"GTower", 0, 0, 0}, Player: true, BitID: evEndhr, A: 0x66, B: 0x70}
	if ph, _, _ := bossEndPhase("ganondorf", g, step(g, func(o *bossObs) { o.B = 0x7C })); ph != "demo frame 0x7C" {
		t.Errorf("frame: %q", ph)
	}
	if _, _, fail := bossEndPhase("ganondorf", g, step(g, func(o *bossObs) { o.NextEnable, o.Next = 1, stageRef{"GTower", 1, 0, 0} })); !strings.Contains(fail, "NextStage") {
		t.Errorf("gnd mismatch: %q", fail)
	}
	gn := step(g, func(o *bossObs) { o.NextEnable, o.Next = 1, gndNext })
	if _, done, fail := bossEndPhase("ganondorf", g, gn); done || fail != "" {
		t.Errorf("gnd next: %v %q", done, fail)
	}
	if _, done, _ := bossEndPhase("ganondorf", gn, step(gn, func(o *bossObs) { o.Bit = true })); !done {
		t.Error("gnd not done on 0x3F40")
	}

	// Puppet: form 2 up is done for form1; form3 wants GanonK 4/9.
	p := bossObs{Stage: stageRef{"GanonK", 0, 0, 0}, Player: true, BitID: evPuppetGanon}
	if _, done, _ := bossEndPhase("puppet/form1", p, step(p, func(o *bossObs) { o.A, o.B = 0, 1 })); !done {
		t.Error("form1 not done")
	}
	if _, done, _ := bossEndPhase("puppet/form1", p, step(p, func(o *bossObs) { o.A, o.B = 1, 0 })); done {
		t.Error("form1 done during hensin")
	}
	if _, _, fail := bossEndPhase("puppet/form3", p, step(p, func(o *bossObs) { o.NextEnable, o.Next = 1, stageRef{"GanonK", 0, 0, 0} })); !strings.Contains(fail, "NextStage") {
		t.Errorf("form3 mismatch: %q", fail)
	}
}
