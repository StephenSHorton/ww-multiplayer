package wwapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/commands"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/events"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
	wwstate "github.com/StephenSHorton/ww-multiplayer/wwapi/state"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/write"
)

// bootedRAM is a fake game's RAM as this build's patched ISO leaves it:
// the disc ID at the start of MEM1, this build's T2 at T2Address and the
// WWMB header main01_init writes (fakegame.Boot).
func bootedRAM(t *testing.T) (*mem.FakeMem, *fakegame.FakeGame) {
	t.Helper()
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	if err := fakegame.Boot(m); err != nil {
		t.Fatal(err)
	}
	return m, g
}

func newGame(t *testing.T, m mem.Mem) *wwapi.Game {
	t.Helper()
	g, err := wwapi.NewGame(m, wwapi.GameOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Close)
	return g
}

// registerWrite registers core.test.write, a command that writes, and
// returns how many times it ran.
func registerWrite(t *testing.T, g *wwapi.Game) *int {
	t.Helper()
	ran := new(int)
	spec := commands.Spec{Writes: []write.Range{{Addr: addr.SaveInfo, Len: 1}}}
	if err := g.Commands().Register("core.test.write", spec, func(context.Context, json.RawMessage) (commands.Result, error) {
		*ran++
		return commands.Result{OK: true}, nil
	}); err != nil {
		t.Fatal(err)
	}
	return ran
}

// TestStrictAttach: this build's blob with its WWMB header attaches in
// strict mode with writes allowed, its ABI, layout and features read
// from the header and the code hash equal to blob.go's (design §6.4
// step 5, §9 E1).
func TestStrictAttach(t *testing.T) {
	m, _ := bootedRAM(t)
	g := newGame(t, m)
	c := g.Contract()
	if c.Mode != wwapi.Strict || !c.Header.Present || c.ABI != inject.ABI || c.Layout != inject.LayoutVersion {
		t.Errorf("contract %+v: want strict, header ABI %d layout %d", c, inject.ABI, inject.LayoutVersion)
	}
	if !c.WritesAllowed || c.Why != "" || c.Err() != nil {
		t.Errorf("writes refused: %q", c.Why)
	}
	if !c.BlobMatches || c.BlobHash != inject.BlobHash || c.WantHash != inject.BlobHash {
		t.Errorf("blob hash 0x%08X (match %v), want 0x%08X", c.BlobHash, c.BlobMatches, inject.BlobHash)
	}
	info := g.Info()
	if info.GameID != "GZLE01" || info.ABI != inject.ABI || info.LayoutVersion != inject.LayoutVersion || !info.BlobMatches || info.BlobHash != inject.BlobHash {
		t.Errorf("info %+v", info)
	}
	if got, want := info.Features.String(), wwapi.BlobFeatures().String(); got != want || !info.Features.Known() {
		t.Errorf("features %q, want %q", got, want)
	}
	if got := wwapi.BlobFeatures().String(); got != "avatar,eyefix,face,input,tunic,warp" {
		t.Errorf("this blob's features %q", got)
	}
	if miss := info.Features.Missing([]string{"avatar", "face", "tunic", "input"}); miss != nil {
		t.Errorf("multiplayer's needs missing: %v", miss)
	}
	if line := wwapi.ContractLine(c); !strings.Contains(line, "strict mode") || !strings.Contains(line, "writes allowed") {
		t.Errorf("contract line %q", line)
	}

	// A command that writes runs: the write gate is the contract.
	ran := registerWrite(t, g)
	if res, err := g.Commands().Execute(context.Background(), "core.test.write", nil); err != nil || !res.OK || *ran != 1 {
		t.Errorf("write command: %+v, %v, ran %d", res, err, *ran)
	}
}

// TestContractRefusals: strict mode refuses writes, with a reason a
// player can read and the re-patch advice, for another game, a game
// without a WWMB header (every blob before E1, a vanilla game, a save
// state taken on either), another ABI or layout version, and another
// blob's code.
func TestContractRefusals(t *testing.T) {
	zeroHeader := func(m *mem.FakeMem) error {
		if err := m.Store(mailbox.Mailbox+mailbox.OffHdrMagic, make([]byte, mailbox.HeaderLen)); err != nil {
			return err
		}
		return m.Store(mailbox.Mailbox+mailbox.OffFeatures, make([]byte, 4))
	}
	for _, tc := range []struct {
		name string
		edit func(m *mem.FakeMem) error // changes a booted game's RAM
		why  string
	}{
		{"another game", func(m *mem.FakeMem) error { return m.Store(addr.MEM1Base, []byte("GZLJ01")) }, `game ID "GZLJ01" is not GZLE01`},
		{"zero header", zeroHeader, "no WWMB header"},
		{"vanilla game", func(m *mem.FakeMem) error {
			if err := m.Store(inject.T2Address, make([]byte, len(inject.T2Bytes))); err != nil {
				return err
			}
			return zeroHeader(m)
		}, "no WWMB header"},
		{"another ABI", func(m *mem.FakeMem) error {
			h := fakegame.ThisHeader()
			h.ABI++
			return mailbox.WriteHeader(m, h)
		}, fmt.Sprintf("ABI %d layout %d, this version's is ABI %d", inject.ABI+1, inject.LayoutVersion, inject.ABI)},
		{"another layout", func(m *mem.FakeMem) error {
			h := fakegame.ThisHeader()
			h.Layout++
			return mailbox.WriteHeader(m, h)
		}, fmt.Sprintf("layout %d, this version's", inject.LayoutVersion+1)},
		{"another blob's code", func(m *mem.FakeMem) error {
			b, err := m.Read(inject.TextEnd-4, 1)
			if err != nil {
				return err
			}
			return m.Store(inject.TextEnd-4, []byte{b[0] ^ 0x01})
		}, "injected code in RAM is not this version's"},
		{"another blob's constants", func(m *mem.FakeMem) error {
			// A literal in .rodata (a string or a float the code loads): a
			// rebuild that changes only a constant changes the hash too.
			b, err := m.Read(inject.HashRanges[0][0], 1)
			if err != nil {
				return err
			}
			return m.Store(inject.HashRanges[0][0], []byte{b[0] ^ 0x01})
		}, "injected code in RAM is not this version's"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := bootedRAM(t)
			if err := tc.edit(m); err != nil {
				t.Fatal(err)
			}
			g := newGame(t, m)
			c := g.Contract()
			if c.WritesAllowed || c.Mode != wwapi.Strict || !errors.Is(c.Err(), wwapi.ErrContract) {
				t.Fatalf("not refused: %+v", c)
			}
			if !strings.Contains(c.Why, tc.why) || !strings.Contains(c.Err().Error(), tc.why) {
				t.Errorf("why %q, want it to say %q", c.Why, tc.why)
			}
			if line := wwapi.ContractLine(c); !strings.Contains(line, "writes refused: "+c.Why) || !strings.Contains(line, wwapi.RepatchAdvice) {
				t.Errorf("contract line %q: want the reason and the re-patch advice", line)
			}
			if !c.Header.Present && (c.ABI != 0 || !c.Features.Known() || len(c.Features.Names()) != 0) {
				t.Errorf("no header: ABI %d, features %s (known %v); want 0 and a known empty set", c.ABI, c.Features, c.Features.Known())
			}
			ran := registerWrite(t, g)
			if _, err := g.Commands().Execute(context.Background(), "core.test.write", nil); !errors.Is(err, commands.ErrWritesRefused) || !errors.Is(err, wwapi.ErrContract) || *ran != 0 {
				t.Errorf("refused write command: %v (ran %d)", err, *ran)
			}
		})
	}
}

// TestContractNeedsRepatch: the header's features word is what the mods'
// needs are checked against: a blob without "tunic" leaves a mod that
// needs it NEEDS_REPATCH, and a game without a header provides nothing.
func TestContractNeedsRepatch(t *testing.T) {
	m, _ := bootedRAM(t)
	h := fakegame.ThisHeader()
	h.Features &^= mailbox.WwFeatTunic
	if err := mailbox.WriteHeader(m, h); err != nil {
		t.Fatal(err)
	}
	c, err := wwapi.CheckContract(m, wwapi.ContractOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if miss := c.Features.Missing([]string{"avatar", "face", "tunic", "input"}); !slices.Equal(miss, []string{"tunic"}) {
		t.Errorf("missing %v, want [tunic]", miss)
	}
	if err := m.Store(mailbox.Mailbox+mailbox.OffHdrMagic, make([]byte, mailbox.HeaderLen)); err != nil {
		t.Fatal(err)
	}
	if c, err = wwapi.CheckContract(m, wwapi.ContractOptions{}); err != nil {
		t.Fatal(err)
	}
	if miss := c.Features.Missing([]string{"avatar", "face"}); !slices.Equal(miss, []string{"avatar", "face"}) {
		t.Errorf("no header: missing %v", miss)
	}
	// A refused game provides nothing even when its header sets every
	// bit: another ABI's or layout's word does not use this build's
	// names, and another blob's code is not this build's features.
	all := []string{"avatar", "eyefix", "face", "input", "tunic", "warp"}
	for _, tc := range []struct {
		name string
		edit func(m *mem.FakeMem) error
	}{
		{"another ABI", func(m *mem.FakeMem) error {
			h := fakegame.ThisHeader()
			h.ABI++
			return mailbox.WriteHeader(m, h)
		}},
		{"another layout", func(m *mem.FakeMem) error {
			h := fakegame.ThisHeader()
			h.Layout++
			return mailbox.WriteHeader(m, h)
		}},
		{"another blob's code", func(m *mem.FakeMem) error {
			b, err := m.Read(inject.TextEnd-4, 1)
			if err != nil {
				return err
			}
			return m.Store(inject.TextEnd-4, []byte{b[0] ^ 0x01})
		}},
	} {
		m, _ := bootedRAM(t)
		if err := tc.edit(m); err != nil {
			t.Fatal(err)
		}
		c, err := wwapi.CheckContract(m, wwapi.ContractOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if c.WritesAllowed || !c.Header.Present || c.Header.Features != uint32(mailbox.WwFeatures) {
			t.Fatalf("%s: setup: %+v", tc.name, c)
		}
		if miss := c.Features.Missing(all); !slices.Equal(miss, all) {
			t.Errorf("%s: missing %v, want every need", tc.name, miss)
		}
	}
	if got := wwapi.FeaturesOf(mailbox.WwFeatFace | 1<<9).String(); got != "bit9,face" {
		t.Errorf("FeaturesOf: %q", got)
	}
}

// TestContractRecheckedOnSaveState: loading a save state (the heartbeat
// goes backwards) re-runs the check, so a state taken on an old blob,
// which has no header, refuses writes from that tick on.
func TestContractRecheckedOnSaveState(t *testing.T) {
	m, fg := bootedRAM(t)
	if err := fg.LoadStage(fakegame.Stage{Name: "sea", Room: 44, Table: addr.TableSea}); err != nil {
		t.Fatal(err)
	}
	g := newGame(t, m)
	for range 5 {
		fg.Frame()
		g.Tick()
	}
	if !g.Contract().WritesAllowed {
		t.Fatalf("booted game refused: %s", g.Contract().Why)
	}
	// The old state: no header, an earlier heartbeat.
	if err := m.Store(mailbox.Mailbox+mailbox.OffHdrMagic, make([]byte, mailbox.HeaderLen)); err != nil {
		t.Fatal(err)
	}
	if err := m.Store(mailbox.SpawnTrigger, []byte{0, 0, 0, 1}); err != nil {
		t.Fatal(err)
	}
	g.Tick()
	if c := g.Contract(); c.WritesAllowed || !strings.Contains(c.Why, "no WWMB header") {
		t.Errorf("after the old save state: %+v", c)
	}
}

// TestActorCommandsUnsupported: actor.kill and actor.spawn exist and say
// they are not supported until the C op ring does.
func TestActorCommandsUnsupported(t *testing.T) {
	m, _ := bootedRAM(t)
	g := newGame(t, m)
	for _, name := range []string{wwapi.CmdActorKill, wwapi.CmdActorSpawn} {
		res, err := g.Commands().Execute(context.Background(), name, map[string]int{"pid": 1})
		if !errors.Is(err, commands.ErrUnsupported) || res.OK || res.Code != "unsupported" {
			t.Errorf("%s: %+v, %v", name, res, err)
		}
	}
	if got := g.Commands().Names(); !slices.Equal(got, []string{wwapi.CmdActorKill, wwapi.CmdActorSpawn, wwapi.CmdPlayerWarp}) {
		t.Errorf("core commands %v", got)
	}
}

// TestMemIsReadOnly: the Reader Game hands out cannot be turned back
// into a mem.Mem.
func TestMemIsReadOnly(t *testing.T) {
	m, _ := bootedRAM(t)
	g := newGame(t, m)
	if _, ok := g.Mem().(mem.Mem); ok {
		t.Error("Game.Mem() is a mem.Mem")
	}
}

// describe is an event in one line, without its Meta.
func describe(e events.Event) string {
	switch e := e.(type) {
	case events.StageChanged:
		return fmt.Sprintf("stage %q t%d -> %q t%d first=%v", e.From.Stage, e.From.Table, e.To.Stage, e.To.Table, e.First)
	case events.ItemGained:
		return fmt.Sprintf("item slot %d 0x%02X -> 0x%02X", e.Slot, e.Prev, e.Item)
	case events.SwitchSet:
		return fmt.Sprintf("switch %s 0x%02X table %d zone %d room %d", e.Kind, e.Switch, e.Table, e.Zone, e.Room)
	case events.EventBitSet:
		return fmt.Sprintf("event 0x%04X", e.ID)
	case events.HeartbeatLost:
		return "heartbeat lost: " + e.Why
	}
	return fmt.Sprintf("%T", e)
}

// TestWatcherOverFakeGame drives the fake game through a session and
// checks the core events each Game.Tick emits on the bus: StageChanged,
// ItemGained, SwitchSet (memory, zone, dan and saved), EventBitSet and
// HeartbeatLost, and nothing from a file load or a table reload.
func TestWatcherOverFakeGame(t *testing.T) {
	m, fg := bootedRAM(t)
	g := newGame(t, m)
	ch, off := events.Chan[events.Event](g.Events(), 256)
	defer off()

	tick := func(frames int, want ...string) {
		t.Helper()
		fg.Frames(frames)
		if err := g.Tick(); err != nil {
			t.Fatal(err)
		}
		var got []string
		for len(ch) > 0 {
			e := <-ch
			if e.EventMeta().Source != events.SourceGame || e.EventMeta().Origin != "core" {
				t.Errorf("%s: meta %+v", describe(e), e.EventMeta())
			}
			got = append(got, describe(e))
		}
		if !slices.Equal(got, want) {
			t.Errorf("events:\n got %q\nwant %q", got, want)
		}
	}

	tick(3, `stage "" t0 -> "" t-1 first=true`) // boot: no stage yet, still First
	if err := fg.LoadStage(fakegame.Stage{Name: "sea_T", Room: 44, NoPlayer: true}); err != nil {
		t.Fatal(err)
	}
	tick(3, `stage "" t-1 -> "sea_T" t0 first=false`)

	// File select: a new file rewrites the save. That is a load, not
	// the game giving anything.
	fg.EnterFileSelect()
	if err := fg.NewGame(0, "Link"); err != nil {
		t.Fatal(err)
	}
	if err := fg.GetItem(save.SlotTelescope, 0, save.ItemTelescope); err != nil {
		t.Fatal(err)
	}
	tick(3, `stage "sea_T" t0 -> "Name" t-1 first=false`)

	outset := fakegame.Stage{Name: "sea", Room: 44, Table: addr.TableSea}
	if err := fg.LoadStage(outset); err != nil {
		t.Fatal(err)
	}
	tick(3, fmt.Sprintf(`stage "Name" t-1 -> "sea" t%d first=false`, addr.TableSea))
	tick(3)

	before, _ := mem.ReadU8(m, addr.SaveImage+uint32(addr.OffItems+save.SlotBow))
	if err := fg.GetItem(save.SlotBow, 0, save.ItemBow); err != nil {
		t.Fatal(err)
	}
	if err := fg.OnSwitch(0x05, 44); err != nil {
		t.Fatal(err)
	}
	if err := fg.OnSwitch(0xC1, 44); err != nil {
		t.Fatal(err)
	}
	if err := fg.OnSwitch(0x81, 44); err != nil {
		t.Fatal(err)
	}
	fg.OnEventBit(0x2E01)
	tick(3,
		fmt.Sprintf("item slot %d 0x%02X -> 0x%02X", save.SlotBow, before, save.ItemBow),
		"event 0x2E01",
		fmt.Sprintf("switch memory 0x05 table %d zone -1 room -1", addr.TableSea),
		"switch dan 0x81 table -1 zone -1 room -1",
		"switch zone 0xC1 table -1 zone 0 room 44",
	)

	// A new bottle is a gain; its contents changing is not. 0x50 is an
	// empty bottle, 0x52 a red potion (dItemNo_EMPTY_BOTTLE_e,
	// dItemNo_RED_BOTTLE_e).
	bottle := addr.SaveImage + uint32(addr.OffItems+addr.SlotBottle0)
	if err := fg.GetItem(addr.SlotBottle0, 0, 0x52); err != nil {
		t.Fatal(err)
	}
	tick(3, fmt.Sprintf("item slot %d 0xFF -> 0x52", addr.SlotBottle0))
	if err := m.Store(bottle, []byte{0x50}); err != nil { // drinking the potion
		t.Fatal(err)
	}
	tick(3)
	if err := m.Store(bottle, []byte{0x52}); err != nil { // refilling it
		t.Fatal(err)
	}
	tick(3)

	// No frames: emulation paused.
	tick(0, "heartbeat lost: heartbeat stalled for 1 ticks: is emulation paused?")
	tick(0) // still lost: said once
	tick(3)

	// A stage change: putSave copies the live memBit to the saved table
	// (a saved switch edge); getSave and initDan reload the live memBit
	// and mDan for the new table, which is not reported.
	drc := fakegame.Stage{Name: "M_NewD2", Table: addr.TableDRC, KeyHUD: true}
	if err := fg.LoadStage(drc); err != nil {
		t.Fatal(err)
	}
	tick(3,
		fmt.Sprintf(`stage "sea" t%d -> "M_NewD2" t%d first=false`, addr.TableSea, addr.TableDRC),
		fmt.Sprintf("switch saved 0x05 table %d zone -1 room -1", addr.TableSea),
	)

	if s := g.Snapshot(); s == nil || s.StageName != "M_NewD2" {
		t.Errorf("Snapshot: %+v", s)
	}
	if st := g.Gate(); st.Stable || st.StableRun != 0 {
		t.Errorf("gate right after a stage change: %+v", st)
	}
	if v, ok := g.Save().View(); !ok || !v.GetItem(save.SlotBow, 0) {
		t.Error("Save().View() lacks the bow")
	}
	if img, ok := g.Save().Image(); !ok || img[addr.OffItems+save.SlotBow] != save.ItemBow {
		t.Error("Save().Image() lacks the bow")
	}
}

// TestWatcherFirstAndFailedReads: the first snapshot names where the game
// is (First), and a failed read after a moving heartbeat is a
// HeartbeatLost whose edges show on the next good read.
func TestWatcherFirstAndFailedReads(t *testing.T) {
	m, fg := bootedRAM(t)
	if err := fg.LoadStage(fakegame.Stage{Name: "sea", Room: 44, Table: addr.TableSea}); err != nil {
		t.Fatal(err)
	}
	var w wwapi.Watcher
	var tr gate.Tracker
	step := func(read bool) []string {
		t.Helper()
		fg.Frames(3)
		var s *snapshot
		if read {
			s = mustRead(t, m)
		}
		var out []string
		for _, e := range w.Step(s, tr.Observe(s)) {
			out = append(out, describe(e))
		}
		return out
	}
	if got := step(true); !slices.Equal(got, []string{fmt.Sprintf(`stage "" t0 -> "sea" t%d first=true`, addr.TableSea)}) {
		t.Errorf("first: %q", got)
	}
	if got := step(true); got != nil {
		t.Errorf("second: %q", got)
	}
	fg.OnEventBit(0x0101)
	if got := step(false); !slices.Equal(got, []string{"heartbeat lost: the game state could not be read"}) {
		t.Errorf("failed read: %q", got)
	}
	if got := step(true); !slices.Equal(got, []string{"event 0x0101"}) {
		t.Errorf("after the failed read: %q", got)
	}
}

// TestWatcherFirstAtBoot: a watcher attached before the first stage load
// still emits First (with To.Stage ""), and the first stage then comes
// as an ordinary StageChanged.
func TestWatcherFirstAtBoot(t *testing.T) {
	m, fg := bootedRAM(t)
	var w wwapi.Watcher
	var tr gate.Tracker
	step := func() []events.Event {
		t.Helper()
		fg.Frames(3)
		s := mustRead(t, m)
		return w.Step(s, tr.Observe(s))
	}
	got := step()
	if len(got) != 1 {
		t.Fatalf("boot: %d events, want 1", len(got))
	}
	if e, ok := got[0].(events.StageChanged); !ok || !e.First || e.To.Stage != "" || e.From != (events.Place{}) {
		t.Errorf("boot: %s", describe(got[0]))
	}
	if err := fg.LoadStage(fakegame.Stage{Name: "sea", Room: 44, Table: addr.TableSea}); err != nil {
		t.Fatal(err)
	}
	got = step()
	if len(got) != 1 {
		t.Fatalf("first stage: %d events, want 1", len(got))
	}
	if e, ok := got[0].(events.StageChanged); !ok || e.First || e.From.Stage != "" || e.To.Stage != "sea" {
		t.Errorf("first stage: %s", describe(got[0]))
	}
}

// TestGameStartStops: the watcher goroutine polls until Close.
//
// The first tick runs as soon as Start's goroutine does, so the test
// subscribes before Start: an events.Wait after Start could subscribe
// after StageChanged{First} was emitted and wait forever (CI hung 10
// minutes on that). The context has a deadline so a regression fails
// instead of hanging.
func TestGameStartStops(t *testing.T) {
	m, fg := bootedRAM(t)
	if err := fg.LoadStage(fakegame.Stage{Name: "sea", Room: 44, Table: addr.TableSea}); err != nil {
		t.Fatal(err)
	}
	g, err := wwapi.NewGame(m, wwapi.GameOptions{Interval: wwapi.FastInterval})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, stop := events.Chan[events.StageChanged](g.Events(), events.DefaultBuffer)
	defer stop()
	g.Start(ctx)
	g.Start(ctx) // a second Start does nothing
	select {
	case e, ok := <-ch:
		if !ok || !e.First || e.To.Stage != "sea" {
			t.Errorf("first StageChanged: %+v (open %v)", e, ok)
		}
	case <-ctx.Done():
		t.Fatal("no StageChanged{First} from the watcher")
	}
	if last, ok := events.Last[events.StageChanged](g.Events()); !ok || !last.First {
		t.Errorf("events.Last after the first tick: %+v, %v", last, ok)
	}
	// Close from several goroutines at once, as a session teardown racing
	// a signal handler would: none may panic or return before the
	// watcher has stopped.
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.Close()
		}()
	}
	wg.Wait()
	g.Close()
	if err := g.Events().Emit(probe{}); !errors.Is(err, events.ErrClosed) {
		t.Errorf("bus open after Close: %v", err)
	}
}

// TestGameCloseBeforeStart: a Game closed before Start never starts.
func TestGameCloseBeforeStart(t *testing.T) {
	m, _ := bootedRAM(t)
	g, err := wwapi.NewGame(m, wwapi.GameOptions{Interval: wwapi.FastInterval})
	if err != nil {
		t.Fatal(err)
	}
	g.Close()
	g.Start(context.Background())
	if s := g.Snapshot(); s != nil {
		t.Errorf("a closed Game ticked: %+v", s)
	}
}

type snapshot = wwstate.Snapshot

func mustRead(t *testing.T, m mem.Reader) *snapshot {
	t.Helper()
	s, err := wwstate.Read(m, wwstate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

type probe struct{ events.Base }

func (probe) EventName() string { return "core.test_probe" }
