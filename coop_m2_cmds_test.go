package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/backup"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/gate"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/snap"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

var m2World = bind.WorldID{0xC0, 0x0B, 1, 2, 3, 4, 5, 6}

// m2Game is a FakeGame playing a saved and loaded file "Link" in slot 0,
// bound to m2World when bound is set.
func m2Game(t *testing.T, bound bool) (*fakegame.FakeGame, *mem.FakeMem, func()) {
	t.Helper()
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(g.NewGame(0, "Link"))
	must(g.LoadStage(diagSea))
	must(g.Save())
	g.EnterFileSelect()
	must(g.LoadFile(0))
	must(g.LoadStage(diagSea))
	tick := func() { g.Frames(3) }
	if bound {
		var tr gate.Tracker
		var s *snap.Snapshot
		var st gate.Status
		for range gate.StableTicks + 1 {
			tick()
			var err error
			s, err = snap.Read(m)
			must(err)
			st = tr.Observe(s)
		}
		p, err := bind.PlanBind(s, st, bind.BindRequest{World: m2World, WorldSeq: 3})
		must(err)
		p.Confirm()
		_, err = bind.Bind(m, p, s, st)
		must(err)
	}
	return g, m, tick
}

func TestCoopTagShow(t *testing.T) {
	_, m, tick := m2Game(t, true)
	var b bytes.Buffer
	if err := coopTagShow(&b, m, tick, bind.WorldID{}, ""); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		`file slot 0, "Link", clear count 0, saved and loaded`,
		"57 57 43 4F 01 00 00 00 C0 0B",
		"BOUND: bound to world c00b010203040506 (no session world",
		"world seq 3",
		"V2 ok    tag slot 0, loaded file slot 0",
		"V6 --    not checked: give the session's world ID",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("coop-tag show lacks %q:\n%s", want, out)
		}
	}

	b.Reset()
	other := bind.WorldID{9, 9, 9, 9, 9, 9, 9, 9}
	if err := coopTagShow(&b, m, tick, other, ""); err != nil {
		t.Fatal(err)
	}
	if out := b.String(); !strings.Contains(out, "FOREIGN") || !strings.Contains(out, "V6 FAIL") || !strings.Contains(out, "coop-tag clear unbinds it") {
		t.Errorf("against another world:\n%s", out)
	}
}

// TestCoopTagShowLeak: a New Game after a bound file shows the leak and
// the rules that catch it, and writes nothing.
func TestCoopTagShowLeak(t *testing.T) {
	g, m, tick := m2Game(t, true)
	g.EnterFileSelect()
	if err := g.NewGame(1, "Tetra"); err != nil {
		t.Fatal(err)
	}
	if err := g.LoadStage(diagSea); err != nil {
		t.Fatal(err)
	}
	m.Record(true)
	var b bytes.Buffer
	if err := coopTagShow(&b, m, tick, m2World, ""); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"LEAKED", "V2 FAIL", "V3 ok", "V4 FAIL", "V5 FAIL", "V6 --    not checked", "co-op scrubs this tag"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if ws := m.Writes(); len(ws) != 0 {
		t.Errorf("coop-tag show wrote: %+v", ws)
	}
}

// TestCoopTagShowFreshBind: a New Game bound before its first save and
// reload keeps mDateIPL 0 in RAM after an in-game save (status_b init
// zeroes it, d_save.cpp:51-56; memory_to_card stamps only the card buffer,
// d_save.cpp:1715-1717), so V5 holds only through the world file's
// pending fresh bind. coop-tag show must not call it a leak: with the
// world file it is bound, and without it (a joiner's machine) it says the
// tag may be a binding rather than advise a clear, as clear warns too.
// Once loaded from the card, it passes V5 through mDateIPL on any machine.
func TestCoopTagShowFreshBind(t *testing.T) {
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(g.NewGame(0, "Link"))
	must(g.LoadStage(diagSea))
	tick := func() { g.Frames(3) }
	s, st, _, err := gateSnapshots(m, tick)
	must(err)
	p, err := bind.PlanBind(s, st, bind.BindRequest{World: m2World, WorldSeq: 1})
	must(err)
	if !p.Fresh {
		t.Fatalf("a never-saved New Game's bind is not fresh: %+v", p)
	}
	p.Confirm()
	_, err = bind.Bind(m, p, s, st)
	must(err)
	must(g.Save())
	host := t.TempDir()
	ws, err := world.Create(host, world.New(world.ID(m2World), time.Now()), world.Options{})
	must(err)
	_, err = ws.AddFreshBind(world.ID(p.Tag.Nonce))
	must(err)
	must(ws.Stop())
	m.Record(true)

	show := func(root string) string {
		var b bytes.Buffer
		must(coopTagShow(&b, m, tick, bind.WorldID{}, root))
		return b.String()
	}
	out := show(host)
	for _, want := range []string{"BOUND", "V5 ok", "V5 held only through a pending fresh bind"} {
		if !strings.Contains(out, want) {
			t.Errorf("with the world file: lacks %q:\n%s", want, out)
		}
	}
	out = show(t.TempDir())
	for _, want := range []string{"A PENDING FRESH BIND", "V5 FAIL", "not here", "load it from the card"} {
		if !strings.Contains(out, want) {
			t.Errorf("without the world file: lacks %q:\n%s", want, out)
		}
	}
	for _, root := range []string{host, t.TempDir(), ""} {
		if out := show(root); strings.Contains(out, "coop-tag clear does it now") || strings.Contains(out, "co-op scrubs this tag") || strings.Contains(out, "LEAKED:") {
			t.Errorf("worlds %q: advises a clear of a binding:\n%s", root, out)
		}
	}

	var b bytes.Buffer
	if err := coopTagClear(&b, m, tick, false, t.TempDir()); !errors.Is(err, errNotDone) || !strings.Contains(b.String(), "may be a binding, not a leak") {
		t.Errorf("clear without the world file: %v\n%s", err, b.String())
	}
	b.Reset()
	if err := coopTagClear(&b, m, tick, false, host); !errors.Is(err, errNotDone) || strings.Contains(b.String(), "Careful") || !strings.Contains(b.String(), "bound)?") {
		t.Errorf("clear with the world file: %v\n%s", err, b.String())
	}

	// Loaded from the card, the file passes V5 through mDateIPL, with the
	// world file and without it: §8.2's M2 check reads this V5 line.
	g.EnterFileSelect()
	must(g.LoadFile(0))
	must(g.LoadStage(diagSea))
	for _, root := range []string{host, t.TempDir()} {
		out := show(root)
		if !strings.Contains(out, "state     BOUND:") || !strings.Contains(out, "the file has been saved and loaded (mDateIPL 0x") || strings.Contains(out, "V5 held only through a pending fresh bind") {
			t.Errorf("worlds %q, after a load from the card: not bound through mDateIPL:\n%s", root, out)
		}
	}
	if w := m.Writes(); len(w) != 0 {
		t.Errorf("wrote %+v", w)
	}
}

func TestCoopTagClear(t *testing.T) {
	g, m, tick := m2Game(t, true)
	tag := func() []byte {
		b, err := m.Read(addr.Reserve, bind.TagSize)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	before := tag()
	m.Record(true)

	// Without --yes: the prompt, and nothing written.
	var b bytes.Buffer
	if err := coopTagClear(&b, m, tick, false, ""); !errors.Is(err, errNotDone) {
		t.Errorf("clear without --yes: %v", err)
	}
	if !strings.Contains(b.String(), "Clear it?") || !strings.Contains(b.String(), "re-run with --yes") {
		t.Errorf("output:\n%s", b.String())
	}
	if !bytes.Equal(tag(), before) || len(m.Writes()) != 0 {
		t.Fatal("clear without --yes wrote")
	}

	// With --yes, but the pause menu open: the gates refuse.
	g.SetPauseMenu(true)
	b.Reset()
	if err := coopTagClear(&b, m, tick, true, ""); !errors.Is(err, bind.ErrGate) {
		t.Errorf("clear in the pause menu: %v", err)
	}
	g.SetPauseMenu(false)
	if !bytes.Equal(tag(), before) {
		t.Fatal("clear wrote with the gates closed")
	}

	b.Reset()
	if err := coopTagClear(&b, m, tick, true, ""); err != nil {
		t.Fatal(err)
	}
	if got := tag(); !bytes.Equal(got, make([]byte, bind.TagSize)) {
		t.Errorf("tag after clear: % X", got)
	}
	for _, w := range m.Writes() {
		if !bind.TagRegion.Contains(w.Addr, len(w.Data)) {
			t.Errorf("write of %d bytes at 0x%08X outside the tag", len(w.Data), w.Addr)
		}
	}
	b.Reset()
	if err := coopTagClear(&b, m, tick, true, ""); err != nil || !strings.Contains(b.String(), "nothing to clear") {
		t.Errorf("clearing again: %v\n%s", err, b.String())
	}
}

// m2Procs is a fake backup.ProcessFinder.
type m2Procs []uint32

func (p m2Procs) DolphinPIDs() ([]uint32, error) { return p, nil }

func TestCoopWorldRestore(t *testing.T) {
	ud := filepath.Join(t.TempDir(), "Dolphin Emulator")
	card := filepath.Join(ud, "GC", "MemoryCardA.USA.raw")
	if err := os.MkdirAll(filepath.Dir(card), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(card, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()

	var b bytes.Buffer
	if err := coopWorldRestore(&b, root, "", false, m2Procs{}); err != nil || !strings.Contains(b.String(), "No memory-card backups") {
		t.Errorf("empty list: %v\n%s", err, b.String())
	}
	bk, err := backup.Create([]string{ud}, backup.Options{Root: root, Label: "session", Now: func() time.Time { return time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(card, []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
	b.Reset()
	if err := coopWorldRestore(&b, root, "", false, m2Procs{}); err != nil || !strings.Contains(b.String(), bk.ID) || !strings.Contains(b.String(), "session") {
		t.Errorf("list: %v\n%s", err, b.String())
	}

	b.Reset()
	if err := coopWorldRestore(&b, root, bk.ID, false, m2Procs{}); !errors.Is(err, errNotDone) || !strings.Contains(b.String(), card) {
		t.Errorf("restore without --yes: %v\n%s", err, b.String())
	}
	if err := coopWorldRestore(&b, root, bk.ID, true, m2Procs{1234}); !errors.Is(err, backup.ErrDolphinRunning) {
		t.Errorf("restore with Dolphin running: %v", err)
	}
	if got, _ := os.ReadFile(card); string(got) != "after" {
		t.Fatalf("the card was written: %q", got)
	}
	b.Reset()
	if err := coopWorldRestore(&b, root, bk.ID, true, m2Procs{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(card); string(got) != "before" {
		t.Errorf("the card after the restore: %q", got)
	}
	if !strings.Contains(b.String(), "To undo:") || !strings.Contains(b.String(), "Restored "+card) {
		t.Errorf("output:\n%s", b.String())
	}
	if err := coopWorldRestore(&b, root, "nope", true, m2Procs{}); err == nil {
		t.Error("restoring an unknown backup succeeded")
	}
}

// TestBackupUserDirsMatchDolphin2: the backup package finds the cards in
// the same user directories dolphin2 launches Dolphin with.
func TestBackupUserDirsMatchDolphin2(t *testing.T) {
	_, _, ud1, ud2 := dolphin2Defaults()
	if a, b := backup.DefaultUserDirs(); a != ud1 || b != ud2 {
		t.Errorf("backup.DefaultUserDirs() = %q, %q; dolphin2 uses %q, %q", a, b, ud1, ud2)
	}
}
