package wwapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

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

// bootedRAM is a fake game's RAM as the patched ISO leaves it: the disc
// ID at the start of MEM1 and this build's T2 at T2Address. The WWMB
// header bytes are left zero, as every blob before E1 leaves them.
func bootedRAM(t *testing.T) (*mem.FakeMem, *fakegame.FakeGame) {
	t.Helper()
	m := mem.NewFakeMem()
	g := fakegame.New(m)
	if err := m.Store(addr.MEM1Base, []byte(wwapi.GameID)); err != nil {
		t.Fatal(err)
	}
	if err := m.Store(inject.T2Address, inject.T2Bytes); err != nil {
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

// TestZeroedHeaderAttachesWithWritesAllowed: today's blob has
// _reserved_hdr zeroed. In pre-E1 mode that is ABI 0 with the fixed
// features, and writes are allowed (design §6.4 step 5, decision 18).
func TestZeroedHeaderAttachesWithWritesAllowed(t *testing.T) {
	m, _ := bootedRAM(t)
	hdr, err := m.Read(mailbox.Mailbox+mailbox.OffReservedHdr, mailbox.HeaderLen)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(hdr, func(b byte) bool { return b != 0 }) {
		t.Fatalf("header not zero: % X", hdr)
	}
	g := newGame(t, m)
	c := g.Contract()
	if c.Mode != wwapi.PreE1 || c.Header.Present || c.ABI != 0 || c.Layout != 0 {
		t.Errorf("contract %+v: want pre-E1, no header, ABI 0, layout 0", c)
	}
	if !c.WritesAllowed || c.Why != "" || c.Err() != nil {
		t.Errorf("writes refused: %q", c.Why)
	}
	if !c.BlobMatches || c.BlobMatch != 1 {
		t.Errorf("blob match %v (%v)", c.BlobMatches, c.BlobMatch)
	}
	info := g.Info()
	if info.GameID != "GZLE01" || info.ABI != 0 || info.LayoutVersion != 0 || !info.BlobMatches {
		t.Errorf("info %+v", info)
	}
	if got, want := info.Features.String(), wwapi.PreE1Features().String(); got != want || !info.Features.Known() {
		t.Errorf("features %q, want %q", got, want)
	}
	if miss := info.Features.Missing([]string{"avatar", "face", "tunic", "input"}); miss != nil {
		t.Errorf("multiplayer's needs missing: %v", miss)
	}

	// A command that writes runs: the write gate is the contract.
	ran := false
	spec := commands.Spec{Writes: []write.Range{{Addr: addr.SaveInfo, Len: 1}}}
	if err := g.Commands().Register("core.test.write", spec, func(context.Context, json.RawMessage) (commands.Result, error) {
		ran = true
		return commands.Result{OK: true}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if res, err := g.Commands().Execute(context.Background(), "core.test.write", nil); err != nil || !res.OK || !ran {
		t.Errorf("write command: %+v, %v, ran %v", res, err, ran)
	}
}

// TestContractRefusals: the pre-E1 check refuses writes only for another
// game or another blob, never for a missing header; a header that is
// there is read.
func TestContractRefusals(t *testing.T) {
	m, _ := bootedRAM(t)
	if err := m.Store(mailbox.Mailbox+mailbox.OffReservedHdr, []byte{'W', 'W', 'M', 'B', 0, 1, 0, 7}); err != nil {
		t.Fatal(err)
	}
	c, err := wwapi.CheckContract(m, wwapi.ContractOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !c.Header.Present || c.ABI != 1 || c.Layout != 7 || !c.WritesAllowed {
		t.Errorf("with a header: %+v", c)
	}

	other := mem.NewFakeMem()
	if err := other.Store(addr.MEM1Base, []byte("GZLJ01")); err != nil {
		t.Fatal(err)
	}
	if err := other.Store(inject.T2Address, inject.T2Bytes); err != nil {
		t.Fatal(err)
	}
	c, err = wwapi.CheckContract(other, wwapi.ContractOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if c.WritesAllowed || !errors.Is(c.Err(), wwapi.ErrContract) || !strings.Contains(c.Why, "GZLJ01") {
		t.Errorf("another game: %+v", c)
	}

	vanilla := mem.NewFakeMem()
	fakegame.New(vanilla)
	if err := vanilla.Store(addr.MEM1Base, []byte(wwapi.GameID)); err != nil {
		t.Fatal(err)
	}
	g := newGame(t, vanilla)
	c = g.Contract()
	if c.WritesAllowed || c.BlobMatches || !strings.Contains(c.Why, "not this build's") {
		t.Errorf("no blob: %+v", c)
	}
	// Still ABI 0 with the fixed features: the header is not what refused.
	if c.ABI != 0 || c.Features.String() != wwapi.PreE1Features().String() {
		t.Errorf("no blob: ABI %d features %s", c.ABI, c.Features)
	}
	spec := commands.Spec{Writes: []write.Range{{Addr: addr.SaveInfo, Len: 1}}}
	if err := g.Commands().Register("core.test.write", spec, func(context.Context, json.RawMessage) (commands.Result, error) {
		t.Error("a refused write command ran")
		return commands.Result{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commands().Execute(context.Background(), "core.test.write", nil); !errors.Is(err, commands.ErrWritesRefused) || !errors.Is(err, wwapi.ErrContract) {
		t.Errorf("refused write command: %v", err)
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
func TestGameStartStops(t *testing.T) {
	m, fg := bootedRAM(t)
	if err := fg.LoadStage(fakegame.Stage{Name: "sea", Room: 44, Table: addr.TableSea}); err != nil {
		t.Fatal(err)
	}
	g, err := wwapi.NewGame(m, wwapi.GameOptions{Interval: wwapi.FastInterval})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g.Start(ctx)
	e, err := events.Wait(ctx, g.Events(), func(e events.StageChanged) bool { return e.First })
	if err != nil || e.To.Stage != "sea" {
		t.Errorf("first StageChanged: %+v, %v", e, err)
	}
	g.Close()
	g.Close()
	if err := g.Events().Emit(probe{}); !errors.Is(err, events.ErrClosed) {
		t.Errorf("bus open after Close: %v", err)
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
