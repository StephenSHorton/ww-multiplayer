package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/engine/enginetest"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// fakeGame is GameDeps over scripted Dolphins (enginetest.Game): running
// lists them, probe answers each PID's script (its last answer repeats),
// launch starts a new PID, and the pipelines and reset are recorded in
// events, in order. Nothing here ever closes a Dolphin: there is no seam
// for it.
type fakeGame = enginetest.Game

func newFakeGame() *fakeGame { return enginetest.NewGame() }

// fakeDeps is the game side's seams over f, ticking fast.
func fakeDeps(f *fakeGame) *GameDeps {
	return &GameDeps{
		Running:   f.List,
		Probe:     f.Probe,
		Launch:    f.Launch,
		Pipelines: f.Pipelines,
		Reset:     f.Reset,
		Tick:      2 * time.Millisecond,
		BootGrace: time.Hour,
		Retry:     2 * time.Millisecond,
	}
}

var (
	booting   = enginetest.Booting
	title     = enginetest.Title
	inGame    = enginetest.InGame
	unpatched = enginetest.Unpatched
	otherGame = enginetest.OtherGame
	closed    = enginetest.Closed
)

// gameOut is a session.LobbyOutput that records log lines and Dolphin
// reports, safe for the runner's goroutine.
type gameOut = enginetest.Out

// lobbyRec records the states reported to the lobby.
type lobbyRec struct {
	mu     sync.Mutex
	states []string
}

func (l *lobbyRec) set(s string) {
	l.mu.Lock()
	l.states = append(l.states, s)
	l.mu.Unlock()
}

func (l *lobbyRec) list() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.states)
}

const (
	userA = enginetest.UserA
	userB = enginetest.UserB
)

var optsA = enginetest.OptsA

func newTestRunner(f *fakeGame, o session.LaunchOptions) (*GameRunner, *gameOut, *lobbyRec, *[]error) {
	out, lob := &gameOut{}, &lobbyRec{}
	var fails []error
	g := NewGameRunner(fakeDeps(f), func() session.LaunchOptions { return o }, "Link", "127.0.0.1:1", out, lob.set,
		func(err error) { fails = append(fails, err) })
	return g, out, lob, &fails
}

var (
	waitFor  = enginetest.WaitFor
	hasEvent = enginetest.HasEvent
	freePort = enginetest.FreePort

	waitSnap    = enginetest.WaitSnap
	waitForCond = enginetest.WaitForCond
)

// TestGameRunnerLaunch: nothing runs on the player's folder, so Start
// launches the configured Dolphin with the patched disc; it goes
// launching -> title screen, and the pose pipelines start on it at the
// title screen (#38). Stopping ends the pipelines, then resets that
// Dolphin's mailbox; the Dolphin is never closed.
func TestGameRunnerLaunch(t *testing.T) {
	f := newFakeGame()
	f.Running = []session.RunningDolphin{{PID: 7, UserDir: userB, State: session.DolphinTitle}} // the other player's
	g, out, lob, _ := newTestRunner(f, optsA)
	g.Start(context.Background())
	g.Start(context.Background()) // a replayed START: once is enough
	waitFor(t, "the pipelines", hasEvent(f, "pipelines 100 start"))
	if l := f.Launches(); len(l) != 1 || l[0] != optsA {
		t.Fatalf("launches %+v", l)
	}
	if d := out.Last(); d.PID != 100 || !d.Launched || d.State != session.DolphinTitle || !d.Pipelines || d.ISO != optsA.ISO {
		t.Fatalf("Dolphin %+v", d)
	}
	f.Script(100, inGame)
	waitFor(t, "in game", func() bool { return out.Last().State == session.DolphinInGame })
	if err := g.Stop(); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(f.Events(), ", "), "launch 100, pipelines 100 start, pipelines 100 stop, reset 100"; got != want {
		t.Fatalf("events %q, want %q", got, want)
	}
	if got := out.States(); !slices.Equal(got, []session.DolphinState{session.DolphinLaunching, session.DolphinTitle, session.DolphinInGame}) {
		t.Fatalf("states %v", got)
	}
	if got := lob.list(); !slices.Equal(got, []string{"launching", "title", "in_game"}) {
		t.Fatalf("lobby states %v", got)
	}
	g.Start(context.Background()) // a START after Leave starts nothing
	time.Sleep(10 * time.Millisecond)
	if n := len(f.Events()); n != 4 {
		t.Fatalf("a START after stop did something: %v", f.Events())
	}
}

// TestGameRunnerAttach: a Dolphin already running on the player's folder
// is played on; nothing is launched.
func TestGameRunnerAttach(t *testing.T) {
	f := newFakeGame()
	f.Running = []session.RunningDolphin{{PID: 8, UserDir: userB, State: session.DolphinTitle}, {PID: 9, UserDir: userA, State: session.DolphinInGame}}
	f.Script(9, inGame)
	g, out, _, _ := newTestRunner(f, optsA)
	g.Start(context.Background())
	waitFor(t, "the pipelines", hasEvent(f, "pipelines 9 start"))
	if len(f.Launches()) != 0 {
		t.Fatal("launched although one runs")
	}
	if d := out.Last(); d.PID != 9 || d.Launched || d.State != session.DolphinInGame {
		t.Fatalf("Dolphin %+v", d)
	}
	g.Stop()
	if !slices.Contains(f.Events(), "reset 9") {
		t.Fatalf("events %v", f.Events())
	}
}

// TestGameRunnerManual: "I'll start Dolphin myself" never launches; the
// session plays on the player's Dolphin once it runs Wind Waker.
func TestGameRunnerManual(t *testing.T) {
	f := newFakeGame()
	o := optsA
	o.Manual = true
	g, out, _, _ := newTestRunner(f, o)
	g.Start(context.Background())
	waitFor(t, "a report", func() bool { return out.Last().State == session.DolphinNone })
	if d := out.Last(); !d.Manual || d.PID != 0 {
		t.Fatalf("Dolphin %+v", d)
	}
	if err := g.launchAgain(); err == nil {
		t.Fatal("launchAgain with Manual")
	}
	time.Sleep(10 * time.Millisecond)
	f.Script(55, title)
	f.SetRunning([]session.RunningDolphin{{PID: 55, UserDir: `D:\Portable\User`, State: session.DolphinTitle}})
	waitFor(t, "the pipelines", hasEvent(f, "pipelines 55 start"))
	if len(f.Launches()) != 0 {
		t.Fatal("launched for a player who starts Dolphin themselves")
	}
	g.Stop()
}

// TestGameRunnerWrongDisc: a Dolphin that runs another game, then an
// unpatched Wind Waker, gets no pipelines and is reported (after a second
// look: a boot can be caught mid-way); once it runs the patched disc it is
// played on.
func TestGameRunnerWrongDisc(t *testing.T) {
	f := newFakeGame()
	f.Running = []session.RunningDolphin{{PID: 9, UserDir: userA, State: session.DolphinWrongGame}}
	f.Script(9, otherGame, otherGame, otherGame, unpatched, unpatched, unpatched, title)
	g, out, lob, _ := newTestRunner(f, optsA)
	g.Start(context.Background())
	waitFor(t, "the pipelines", hasEvent(f, "pipelines 9 start"))
	// The first look (wrong game, not yet reported) is "no game", never
	// "none": the Dolphin runs, it didn't close.
	if got := out.States(); !slices.Equal(got, []session.DolphinState{session.DolphinNoGame, session.DolphinWrongGame, session.DolphinUnpatched, session.DolphinTitle}) {
		t.Fatalf("states %v", got)
	}
	if !out.Logged("different game") || !out.Logged("without this version's mod") || out.Logged("closed") {
		t.Fatalf("logged: %v", out.Lines())
	}
	if got := lob.list(); !slices.Equal(got, []string{"no_game", "wrong_game", "unpatched", "title"}) {
		t.Fatalf("lobby %v", got)
	}
	if len(f.Launches()) != 0 {
		t.Fatal("launched over a running Dolphin")
	}
	g.Stop()

	// A single unpatched look (the boot mid-way) is not reported.
	f2 := newFakeGame()
	f2.Running = []session.RunningDolphin{{PID: 3, UserDir: userA, State: session.DolphinTitle}}
	f2.Script(3, unpatched, title)
	g2, out2, _, _ := newTestRunner(f2, optsA)
	g2.Start(context.Background())
	waitFor(t, "the pipelines", hasEvent(f2, "pipelines 3 start"))
	g2.Stop()
	if slices.Contains(out2.States(), session.DolphinUnpatched) {
		t.Fatalf("one unpatched look was reported: %v", out2.States())
	}
}

// TestGameRunnerAttachWrongGame: WW_DOLPHIN_PID names a Dolphin that
// runs another game. Its first look is no reason to call it closed (no
// "closed" log, the PID is kept), and the second reports the wrong game.
func TestGameRunnerAttachWrongGame(t *testing.T) {
	f := newFakeGame()
	f.Script(31, otherGame)
	out := &gameOut{}
	d := fakeDeps(f)
	d.EnvPID = 31
	g := NewGameRunner(d, func() session.LaunchOptions { return optsA }, "Link", "x", out, nil, nil)
	g.Start(context.Background())
	waitFor(t, "wrong game", func() bool { return out.Last().State == session.DolphinWrongGame })
	g.Stop()
	if d := out.Last(); d.PID != 31 {
		t.Fatalf("Dolphin %+v", d)
	}
	if out.Logged("closed") || slices.Contains(out.States(), session.DolphinNone) {
		t.Fatalf("called closed: %v %v", out.States(), out.Lines())
	}
}

// TestGameRunnerEmulationStopped: emulation stops in the same Dolphin
// (no game for two looks) and boots again: the pipelines, whose handle is
// the old boot's, stop and start again on the new boot. A single empty
// look doesn't stop them.
func TestGameRunnerEmulationStopped(t *testing.T) {
	f := newFakeGame()
	f.Running = []session.RunningDolphin{{PID: 9, UserDir: userA, State: session.DolphinTitle}}
	f.Script(9, title, title, booting, title, title, booting, booting, booting, title)
	g, _, _, _ := newTestRunner(f, optsA)
	g.Start(context.Background())
	waitFor(t, "the pipelines again", func() bool {
		n := 0
		for _, e := range f.Events() {
			if e == "pipelines 9 start" {
				n++
			}
		}
		return n == 2
	})
	g.Stop()
	if got, want := strings.Join(f.Events(), ", "), "pipelines 9 start, pipelines 9 stop, pipelines 9 start, pipelines 9 stop, reset 9"; got != want {
		t.Fatalf("events %q, want %q", got, want)
	}
}

// TestGameRunnerDolphinClosed: when the Dolphin closes, the pipelines
// stop and the state is "none" (the banner); launchAgain launches it
// again, and the pipelines come back. The closed Dolphin's mailbox is
// reset at the end too (best effort: it is gone).
func TestGameRunnerDolphinClosed(t *testing.T) {
	f := newFakeGame()
	g, out, _, _ := newTestRunner(f, optsA)
	if err := g.launchAgain(); !errors.Is(err, session.ErrWrongPhase) {
		t.Fatalf("launchAgain before start = %v", err)
	}
	g.Start(context.Background())
	waitFor(t, "the pipelines", hasEvent(f, "pipelines 100 start"))
	if err := g.launchAgain(); err == nil {
		t.Fatal("launchAgain while Dolphin runs")
	}
	f.Script(100, closed)
	waitFor(t, "closed", func() bool { return out.Last().State == session.DolphinNone })
	waitFor(t, "the pipelines stopped", hasEvent(f, "pipelines 100 stop"))
	if d := out.Last(); d.PID != 0 || d.Pipelines {
		t.Fatalf("Dolphin %+v", d)
	}
	if err := g.launchAgain(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the pipelines again", hasEvent(f, "pipelines 101 start"))
	g.Stop()
	ev := f.Events()
	if !slices.Contains(ev, "reset 100") || !slices.Contains(ev, "reset 101") || len(f.Launches()) != 2 {
		t.Fatalf("events %v", ev)
	}
}

// TestGameRunnerLaunchFails: a launch that fails (or no Dolphin set up)
// is reported with its reason, and nothing plays.
func TestGameRunnerLaunchFails(t *testing.T) {
	f := newFakeGame()
	f.LaunchErr = errors.New("exec: Dolphin.exe: file does not exist")
	g, out, _, _ := newTestRunner(f, optsA)
	g.Start(context.Background())
	waitFor(t, "the error", func() bool { return out.Last().Err != "" })
	if d := out.Last(); d.State != session.DolphinNone || !strings.Contains(d.Err, "does not exist") || d.LaunchAttempt != 1 {
		t.Fatalf("Dolphin %+v", d)
	}
	// Try again fails again: a new attempt (a new dialog).
	if err := g.launchAgain(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the second attempt", func() bool { return out.Last().LaunchAttempt == 2 })
	// The player starts Dolphin on their folder: attached, the old error
	// gone (it would show again once that Dolphin closes).
	f.Script(9, title)
	f.SetRunning([]session.RunningDolphin{{PID: 9, UserDir: userA, State: session.DolphinTitle}})
	waitFor(t, "the pipelines", hasEvent(f, "pipelines 9 start"))
	if d := out.Last(); d.Err != "" || d.PID != 9 {
		t.Fatalf("Dolphin %+v", d)
	}
	g.Stop()

	f2 := newFakeGame()
	g2, out2, _, _ := newTestRunner(f2, session.LaunchOptions{UserDir: userA})
	g2.Start(context.Background())
	waitFor(t, "the error", func() bool { return out2.Last().Err != "" })
	g2.Stop()
	if len(f2.Launches()) != 0 || !strings.Contains(out2.Last().Err, "Setup") {
		t.Fatalf("no Dolphin set up: %+v", out2.Last())
	}
}

// TestGameRunnerFatal: a fatal pipelines error (a version mismatch) ends
// the session: fail is called and stop returns it. A transient one only
// restarts the pipelines.
func TestGameRunnerFatal(t *testing.T) {
	f := newFakeGame()
	g, _, _, fails := newTestRunner(f, optsA)
	g.Start(context.Background())
	waitFor(t, "the pipelines", hasEvent(f, "pipelines 100 start"))
	f.PipeErr <- errors.New("broadcast-pose: send position: broken pipe")
	waitFor(t, "a restart", func() bool {
		n := 0
		for _, e := range f.Events() {
			if e == "pipelines 100 start" {
				n++
			}
		}
		return n == 2
	})
	fatal := fmt.Errorf("%w: version mismatch", network.ErrFatal)
	f.PipeErr <- fatal
	waitFor(t, "the reset", hasEvent(f, "reset 100"))
	if err := g.Stop(); !errors.Is(err, network.ErrFatal) {
		t.Fatalf("stop = %v", err)
	}
	if len(*fails) != 1 {
		t.Fatalf("fail called %d times", len(*fails))
	}
}

// TestGameRunnerEnvPID: WW_DOLPHIN_PID picks the Dolphin, whatever runs.
func TestGameRunnerEnvPID(t *testing.T) {
	f := newFakeGame()
	f.Running = []session.RunningDolphin{{PID: 9, UserDir: userA, State: session.DolphinTitle}}
	f.Script(31, title)
	out := &gameOut{}
	d := fakeDeps(f)
	d.EnvPID = 31
	g := NewGameRunner(d, func() session.LaunchOptions { return optsA }, "Link", "x", out, nil, nil)
	g.Start(context.Background())
	waitFor(t, "the pipelines", hasEvent(f, "pipelines 31 start"))
	g.Stop()
}

// bootedMem is emulated RAM as this build's patched ISO leaves it
// (fakegame.Boot: the disc ID, T2 and the WWMB header).
func bootedMem(t *testing.T) *mem.FakeMem {
	t.Helper()
	m := mem.NewFakeMem()
	if err := fakegame.Boot(m); err != nil {
		t.Fatal(err)
	}
	return m
}

// TestHasThisMod: this build's mod in RAM (its WWMB header and its
// code) says the game is patched; the mod's variables changing don't
// undo that; a save state taken on a blob before E1 (no header), an
// unpatched game's heap, another version's code, or nothing readable
// isn't (the strict contract check, Phase E1).
func TestHasThisMod(t *testing.T) {
	ram := bootedMem(t)
	if !hasThisMod(ram) {
		t.Fatal("the mod itself")
	}
	// Every byte of T2 outside the hashed read-only sections (.data,
	// .bss, .ctors, .dtors) is the mod's variables.
	for i, r := range inject.HashRanges {
		end := inject.T2End
		if i+1 < len(inject.HashRanges) {
			end = inject.HashRanges[i+1][0]
		}
		if err := ram.Store(r[1], bytes.Repeat([]byte{0x5A}, int(end-r[1]))); err != nil {
			t.Fatal(err)
		}
	}
	if !hasThisMod(ram) {
		t.Fatal("the mod's .data and .bss changed")
	}

	old := bootedMem(t)
	if err := old.Store(mailbox.Mailbox+mailbox.OffHdrMagic, make([]byte, mailbox.HeaderLen)); err != nil {
		t.Fatal(err)
	}
	if hasThisMod(old) {
		t.Fatal("a save state from before E1: no header")
	}

	heap := mem.NewFakeMem()
	junk := make([]byte, len(inject.T2Bytes))
	for i := range junk {
		junk[i] = byte(i*7 + 3)
	}
	if err := heap.Store(addr.MEM1Base, []byte(session.GameID)); err != nil {
		t.Fatal(err)
	}
	if err := heap.Store(inject.T2Address, junk); err != nil {
		t.Fatal(err)
	}
	if hasThisMod(heap) {
		t.Fatal("an unpatched game's heap")
	}

	other := bootedMem(t)
	if err := other.Store(inject.TextStart+0x40, []byte{0xFF, 0xFF, 0xFF, 0xFF}); err != nil {
		t.Fatal(err)
	}
	if hasThisMod(other) {
		t.Fatal("another version's code under this version's header")
	}
	if hasThisMod(unreadable{}) {
		t.Fatal("nothing readable")
	}
}

// unreadable is RAM that cannot be read.
type unreadable struct{}

func (unreadable) Read(uint32, int) ([]byte, error) { return nil, errors.New("unmapped") }

// mailRAM is emulated RAM for resetMailbox: this build's patched game
// (or a vanilla one), and the writes made.
type mailRAM struct {
	mod    bool
	ram    *mem.FakeMem
	writes []uint32
}

func (m *mailRAM) Read(a uint32, n int) ([]byte, error) {
	if m.ram == nil {
		m.ram = mem.NewFakeMem()
		if m.mod {
			if err := fakegame.Boot(m.ram); err != nil {
				return nil, err
			}
		}
	}
	return m.ram.Read(a, n)
}

func (m *mailRAM) Write(addr uint32, data []byte) error {
	m.writes = append(m.writes, addr)
	return nil
}

// TestResetMailbox: Leave resets the mailbox only while the Dolphin still
// runs this build's mod; one that booted the vanilla disc since has game
// heap there, and is not written.
func TestResetMailbox(t *testing.T) {
	patched := &mailRAM{mod: true}
	if !resetMailbox(patched, true) || !slices.Contains(patched.writes, mailbox.Mailbox+mailbox.OffShadowMode) ||
		!slices.Contains(patched.writes, mailbox.Mailbox+mailbox.OffTunicEnable) {
		t.Fatalf("patched: writes %x", patched.writes)
	}
	noTunic := &mailRAM{mod: true}
	if !resetMailbox(noTunic, false) || slices.Contains(noTunic.writes, mailbox.Mailbox+mailbox.OffTunicEnable) {
		t.Fatalf("WW_TUNIC=off: writes %x", noTunic.writes)
	}
	vanilla := &mailRAM{}
	if resetMailbox(vanilla, true) || len(vanilla.writes) != 0 {
		t.Fatalf("vanilla: writes %x", vanilla.writes)
	}
}

// TestLobbyStartGoInGame drives two apps' engines through their
// controllers against a real lobby on loopback, with fake Dolphins: the
// joiner must be ready before the host's Start goes; Start launches both
// players' Dolphins (each its own, on its own folder), the host sends GO
// by itself (co-op off) and both go in game with the pose pipelines on
// their Dolphins. A late joiner launches at once. Leave on the host stops
// its pipelines, then resets its Dolphin's mailbox, before the server
// stops; no Dolphin is closed.
func TestLobbyStartGoInGame(t *testing.T) {
	fa, fb, fc := newFakeGame(), newFakeGame(), newFakeGame()
	fb.NextPID, fc.NextPID = 200, 300
	ea := New(Config{Live: &LiveState{}, Game: fakeDeps(fa)})
	eb := New(Config{Live: &LiveState{}, Game: fakeDeps(fb)})
	ec := New(Config{Live: &LiveState{}, Game: fakeDeps(fc)})
	ca, cb, cc := session.New(ea, session.Options{}), session.New(eb, session.Options{}), session.New(ec, session.Options{})

	port := freePort(t)
	optsB := optsA
	optsB.UserDir = userB
	if _, err := ca.HostLobby(context.Background(), session.HostOptions{Name: "Host", Port: port, ListenHost: "127.0.0.1", Launch: optsA}); err != nil {
		t.Fatal(err)
	}
	defer ca.Leave()
	waitSnap(t, ca, "host in its lobby", func(st session.State) bool { return st.Phase == session.InLobby && st.Lobby.Host })
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	if _, err := cb.JoinLobby(context.Background(), session.JoinOptions{Name: "Joiner", Addr: addr, Launch: optsB}); err != nil {
		t.Fatal(err)
	}
	defer cb.Leave()
	waitSnap(t, ca, "two members", func(st session.State) bool { return len(st.Lobby.Members) == 2 })
	if err := cb.Start(); !errors.Is(err, session.ErrNotHost) {
		t.Fatalf("joiner Start = %v", err)
	}
	if err := ca.Start(); !errors.Is(err, session.ErrNotReady) {
		t.Fatalf("Start with the joiner not ready = %v", err)
	}
	if err := cb.SetReady(true); err != nil {
		t.Fatal(err)
	}
	waitSnap(t, ca, "joiner ready", func(st session.State) bool { return len(st.Lobby.NotReady()) == 0 })
	if err := ca.Start(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []*session.Controller{ca, cb} {
		waitSnap(t, c, "in game", func(st session.State) bool {
			return st.Phase == session.InGame && st.Dolphin.State == session.DolphinTitle && st.Dolphin.Pipelines
		})
	}
	waitFor(t, "host pipelines", hasEvent(fa, "pipelines 100 start"))
	waitFor(t, "joiner pipelines", hasEvent(fb, "pipelines 200 start"))
	if la, lb := fa.Launches(), fb.Launches(); len(la) != 1 || la[0].UserDir != userA || len(lb) != 1 || lb[0].UserDir != userB {
		t.Fatalf("launches %+v / %+v", la, lb)
	}
	// Each app's Dolphin chip reaches the others through the lobby.
	waitSnap(t, cb, "the host's chip", func(st session.State) bool {
		for _, m := range st.Lobby.Members {
			if m.Name == "Host" && m.Dolphin == string(session.DolphinTitle) {
				return true
			}
		}
		return false
	})

	// A late joiner launches at once: the lobby replays START and GO.
	if _, err := cc.JoinLobby(context.Background(), session.JoinOptions{Name: "Late", Addr: addr, Launch: optsA}); err != nil {
		t.Fatal(err)
	}
	defer cc.Leave()
	waitSnap(t, cc, "late joiner in game", func(st session.State) bool { return st.Phase == session.InGame && st.Dolphin.Pipelines })
	waitFor(t, "late pipelines", hasEvent(fc, "pipelines 300 start"))

	// Leave: pipelines stop, then the mailbox reset, and only then does
	// the host's session (and its server) end.
	ca.Leave()
	fa.Event("left")
	if got, want := strings.Join(fa.Events(), ", "), "launch 100, pipelines 100 start, pipelines 100 stop, reset 100, left"; got != want {
		t.Fatalf("host teardown %q, want %q", got, want)
	}
	if st := ca.Snapshot(); st.Phase != session.Ended || st.Failed {
		t.Fatalf("host after Leave: %+v", st)
	}
	cb.Leave()
	if ev := fb.Events(); ev[len(ev)-1] != "reset 200" {
		t.Fatalf("joiner teardown %v", ev)
	}
}
