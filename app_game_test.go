package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/engine"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// fakeGame is gameDeps over scripted Dolphins: running lists them, probe
// answers each PID's script (its last answer repeats), launch starts a
// new PID, and the pipelines and reset are recorded in events, in order.
// Nothing here ever closes a Dolphin: there is no seam for it.
type fakeGame struct {
	mu        sync.Mutex
	events    []string
	running   []session.RunningDolphin
	probes    map[uint32][]session.Probe
	nextPID   uint32
	launchErr error
	launched  []session.LaunchOptions
	pipeErr   chan error // a pipelines run returns what arrives here
}

func newFakeGame() *fakeGame {
	return &fakeGame{probes: map[uint32][]session.Probe{}, nextPID: 100, pipeErr: make(chan error, 1)}
}

func (f *fakeGame) deps() *gameDeps {
	return &gameDeps{
		running:   f.list,
		probe:     f.probe,
		launch:    f.launch,
		pipelines: f.pipelines,
		reset:     func(pid uint32) { f.event("reset %d", pid) },
		tick:      2 * time.Millisecond,
		bootGrace: time.Hour,
		retry:     2 * time.Millisecond,
	}
}

func (f *fakeGame) event(format string, args ...any) {
	f.mu.Lock()
	f.events = append(f.events, fmt.Sprintf(format, args...))
	f.mu.Unlock()
}

func (f *fakeGame) list() []session.RunningDolphin {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.running)
}

func (f *fakeGame) probe(pid uint32) session.Probe {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.probes[pid]
	if len(s) == 0 {
		return session.Probe{}
	}
	p := s[0]
	if len(s) > 1 {
		f.probes[pid] = s[1:]
	}
	return p
}

// script sets PID pid's probe answers.
func (f *fakeGame) script(pid uint32, ps ...session.Probe) {
	f.mu.Lock()
	f.probes[pid] = ps
	f.mu.Unlock()
}

func (f *fakeGame) launch(o session.LaunchOptions, _ log.Reporter) (uint32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.launchErr != nil {
		return 0, f.launchErr
	}
	f.launched = append(f.launched, o)
	pid := f.nextPID
	f.nextPID++
	f.events = append(f.events, fmt.Sprintf("launch %d", pid))
	if _, ok := f.probes[pid]; !ok {
		f.probes[pid] = []session.Probe{booting, booting, title}
	}
	return pid, nil
}

func (f *fakeGame) pipelines(ctx context.Context, cancel context.CancelFunc, name, addr string, pid uint32, _ log.Reporter) error {
	f.event("pipelines %d start", pid)
	defer f.event("pipelines %d stop", pid)
	select {
	case <-ctx.Done():
		return nil
	case err := <-f.pipeErr:
		cancel()
		return err
	}
}

func (f *fakeGame) eventList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.events)
}

func (f *fakeGame) launches() []session.LaunchOptions {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.launched)
}

var (
	booting   = session.Probe{Running: true}
	title     = session.Probe{Running: true, GameID: session.GameID, Patched: true}
	inGame    = session.Probe{Running: true, GameID: session.GameID, Patched: true, InGame: true}
	unpatched = session.Probe{Running: true, GameID: session.GameID}
	otherGame = session.Probe{Running: true, GameID: "GALE01"}
	closed    = session.Probe{}
)

// gameOut is a session.LobbyOutput that records log lines and Dolphin
// reports, safe for the runner's goroutine.
type gameOut struct {
	mu      sync.Mutex
	lines   []string
	dolphin []session.DolphinInfo
}

func (o *gameOut) Log(_ log.Level, msg string) {
	o.mu.Lock()
	o.lines = append(o.lines, msg)
	o.mu.Unlock()
}
func (o *gameOut) Chat(string, string)                        {}
func (o *gameOut) Ask(context.Context, session.Question) bool { return false }
func (o *gameOut) Advise(session.Advice)                      {}
func (o *gameOut) LobbyState(session.Lobby)                   {}
func (o *gameOut) LobbyNotice(session.Notice)                 {}
func (o *gameOut) LobbyStart(session.LobbySettings)           {}
func (o *gameOut) LobbyGo(string)                             {}
func (o *gameOut) LobbyRequest(string, string)                {}
func (o *gameOut) LobbyLink(session.LobbyLink)                {}
func (o *gameOut) Dolphin(d session.DolphinInfo) {
	o.mu.Lock()
	o.dolphin = append(o.dolphin, d)
	o.mu.Unlock()
}

func (o *gameOut) last() session.DolphinInfo {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.dolphin) == 0 {
		return session.DolphinInfo{}
	}
	return o.dolphin[len(o.dolphin)-1]
}

// states is the Dolphin states reported, repeats dropped.
func (o *gameOut) states() []session.DolphinState {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []session.DolphinState
	for _, d := range o.dolphin {
		if len(out) == 0 || out[len(out)-1] != d.State {
			out = append(out, d.State)
		}
	}
	return out
}

func (o *gameOut) logged(sub string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, l := range o.lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

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
	userA = `C:\Users\p\AppData\Roaming\Dolphin Emulator`
	userB = `C:\Users\p\AppData\Roaming\Dolphin Emulator 2`
)

var optsA = session.LaunchOptions{Exe: `C:\Dolphin\Dolphin.exe`, UserDir: userA, ISO: `C:\Roms\ww-multiplayer.iso`, InstallCheats: true}

func newTestRunner(f *fakeGame, o session.LaunchOptions) (*gameRunner, *gameOut, *lobbyRec, *[]error) {
	out, lob := &gameOut{}, &lobbyRec{}
	var fails []error
	g := newGameRunner(f.deps(), func() session.LaunchOptions { return o }, "Link", "127.0.0.1:1", out, lob.set,
		func(err error) { fails = append(fails, err) })
	return g, out, lob, &fails
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	if !waitForCond(cond) {
		t.Fatalf("timed out waiting for %s", what)
	}
}

func hasEvent(f *fakeGame, ev string) func() bool {
	return func() bool { return slices.Contains(f.eventList(), ev) }
}

// TestGameRunnerLaunch: nothing runs on the player's folder, so Start
// launches the configured Dolphin with the patched disc; it goes
// launching -> title screen, and the pose pipelines start on it at the
// title screen (#38). Stopping ends the pipelines, then resets that
// Dolphin's mailbox; the Dolphin is never closed.
func TestGameRunnerLaunch(t *testing.T) {
	f := newFakeGame()
	f.running = []session.RunningDolphin{{PID: 7, UserDir: userB, State: session.DolphinTitle}} // the other player's
	g, out, lob, _ := newTestRunner(f, optsA)
	g.start(context.Background())
	g.start(context.Background()) // a replayed START: once is enough
	waitFor(t, "the pipelines", hasEvent(f, "pipelines 100 start"))
	if l := f.launches(); len(l) != 1 || l[0] != optsA {
		t.Fatalf("launches %+v", l)
	}
	if d := out.last(); d.PID != 100 || !d.Launched || d.State != session.DolphinTitle || !d.Pipelines || d.ISO != optsA.ISO {
		t.Fatalf("Dolphin %+v", d)
	}
	f.script(100, inGame)
	waitFor(t, "in game", func() bool { return out.last().State == session.DolphinInGame })
	if err := g.stop(); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(f.eventList(), ", "), "launch 100, pipelines 100 start, pipelines 100 stop, reset 100"; got != want {
		t.Fatalf("events %q, want %q", got, want)
	}
	if got := out.states(); !slices.Equal(got, []session.DolphinState{session.DolphinLaunching, session.DolphinTitle, session.DolphinInGame}) {
		t.Fatalf("states %v", got)
	}
	if got := lob.list(); !slices.Equal(got, []string{"launching", "title", "in_game"}) {
		t.Fatalf("lobby states %v", got)
	}
	g.start(context.Background()) // a START after Leave starts nothing
	time.Sleep(10 * time.Millisecond)
	if n := len(f.eventList()); n != 4 {
		t.Fatalf("a START after stop did something: %v", f.eventList())
	}
}

// TestGameRunnerAttach: a Dolphin already running on the player's folder
// is played on; nothing is launched.
func TestGameRunnerAttach(t *testing.T) {
	f := newFakeGame()
	f.running = []session.RunningDolphin{{PID: 8, UserDir: userB, State: session.DolphinTitle}, {PID: 9, UserDir: userA, State: session.DolphinInGame}}
	f.script(9, inGame)
	g, out, _, _ := newTestRunner(f, optsA)
	g.start(context.Background())
	waitFor(t, "the pipelines", hasEvent(f, "pipelines 9 start"))
	if len(f.launches()) != 0 {
		t.Fatal("launched although one runs")
	}
	if d := out.last(); d.PID != 9 || d.Launched || d.State != session.DolphinInGame {
		t.Fatalf("Dolphin %+v", d)
	}
	g.stop()
	if !slices.Contains(f.eventList(), "reset 9") {
		t.Fatalf("events %v", f.eventList())
	}
}

// TestGameRunnerManual: "I'll start Dolphin myself" never launches; the
// session plays on the player's Dolphin once it runs Wind Waker.
func TestGameRunnerManual(t *testing.T) {
	f := newFakeGame()
	o := optsA
	o.Manual = true
	g, out, _, _ := newTestRunner(f, o)
	g.start(context.Background())
	waitFor(t, "a report", func() bool { return out.last().State == session.DolphinNone })
	if d := out.last(); !d.Manual || d.PID != 0 {
		t.Fatalf("Dolphin %+v", d)
	}
	if err := g.launchAgain(); err == nil {
		t.Fatal("launchAgain with Manual")
	}
	time.Sleep(10 * time.Millisecond)
	f.script(55, title)
	f.mu.Lock()
	f.running = []session.RunningDolphin{{PID: 55, UserDir: `D:\Portable\User`, State: session.DolphinTitle}}
	f.mu.Unlock()
	waitFor(t, "the pipelines", hasEvent(f, "pipelines 55 start"))
	if len(f.launches()) != 0 {
		t.Fatal("launched for a player who starts Dolphin themselves")
	}
	g.stop()
}

// TestGameRunnerWrongDisc: a Dolphin that runs another game, then an
// unpatched Wind Waker, gets no pipelines and is reported (after a second
// look: a boot can be caught mid-way); once it runs the patched disc it is
// played on.
func TestGameRunnerWrongDisc(t *testing.T) {
	f := newFakeGame()
	f.running = []session.RunningDolphin{{PID: 9, UserDir: userA, State: session.DolphinWrongGame}}
	f.script(9, otherGame, otherGame, otherGame, unpatched, unpatched, unpatched, title)
	g, out, lob, _ := newTestRunner(f, optsA)
	g.start(context.Background())
	waitFor(t, "the pipelines", hasEvent(f, "pipelines 9 start"))
	// The first look (wrong game, not yet reported) is "no game", never
	// "none": the Dolphin runs, it didn't close.
	if got := out.states(); !slices.Equal(got, []session.DolphinState{session.DolphinNoGame, session.DolphinWrongGame, session.DolphinUnpatched, session.DolphinTitle}) {
		t.Fatalf("states %v", got)
	}
	if !out.logged("different game") || !out.logged("without this version's mod") || out.logged("closed") {
		t.Fatalf("logged: %v", out.lines)
	}
	if got := lob.list(); !slices.Equal(got, []string{"no_game", "wrong_game", "unpatched", "title"}) {
		t.Fatalf("lobby %v", got)
	}
	if len(f.launches()) != 0 {
		t.Fatal("launched over a running Dolphin")
	}
	g.stop()

	// A single unpatched look (the boot mid-way) is not reported.
	f2 := newFakeGame()
	f2.running = []session.RunningDolphin{{PID: 3, UserDir: userA, State: session.DolphinTitle}}
	f2.script(3, unpatched, title)
	g2, out2, _, _ := newTestRunner(f2, optsA)
	g2.start(context.Background())
	waitFor(t, "the pipelines", hasEvent(f2, "pipelines 3 start"))
	g2.stop()
	if slices.Contains(out2.states(), session.DolphinUnpatched) {
		t.Fatalf("one unpatched look was reported: %v", out2.states())
	}
}

// TestGameRunnerAttachWrongGame: WW_DOLPHIN_PID names a Dolphin that
// runs another game. Its first look is no reason to call it closed (no
// "closed" log, the PID is kept), and the second reports the wrong game.
func TestGameRunnerAttachWrongGame(t *testing.T) {
	f := newFakeGame()
	f.script(31, otherGame)
	out := &gameOut{}
	d := f.deps()
	d.envPID = 31
	g := newGameRunner(d, func() session.LaunchOptions { return optsA }, "Link", "x", out, nil, nil)
	g.start(context.Background())
	waitFor(t, "wrong game", func() bool { return out.last().State == session.DolphinWrongGame })
	g.stop()
	if d := out.last(); d.PID != 31 {
		t.Fatalf("Dolphin %+v", d)
	}
	if out.logged("closed") || slices.Contains(out.states(), session.DolphinNone) {
		t.Fatalf("called closed: %v %v", out.states(), out.lines)
	}
}

// TestGameRunnerEmulationStopped: emulation stops in the same Dolphin
// (no game for two looks) and boots again: the pipelines, whose handle is
// the old boot's, stop and start again on the new boot. A single empty
// look doesn't stop them.
func TestGameRunnerEmulationStopped(t *testing.T) {
	f := newFakeGame()
	f.running = []session.RunningDolphin{{PID: 9, UserDir: userA, State: session.DolphinTitle}}
	f.script(9, title, title, booting, title, title, booting, booting, booting, title)
	g, _, _, _ := newTestRunner(f, optsA)
	g.start(context.Background())
	waitFor(t, "the pipelines again", func() bool {
		n := 0
		for _, e := range f.eventList() {
			if e == "pipelines 9 start" {
				n++
			}
		}
		return n == 2
	})
	g.stop()
	if got, want := strings.Join(f.eventList(), ", "), "pipelines 9 start, pipelines 9 stop, pipelines 9 start, pipelines 9 stop, reset 9"; got != want {
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
	g.start(context.Background())
	waitFor(t, "the pipelines", hasEvent(f, "pipelines 100 start"))
	if err := g.launchAgain(); err == nil {
		t.Fatal("launchAgain while Dolphin runs")
	}
	f.script(100, closed)
	waitFor(t, "closed", func() bool { return out.last().State == session.DolphinNone })
	waitFor(t, "the pipelines stopped", hasEvent(f, "pipelines 100 stop"))
	if d := out.last(); d.PID != 0 || d.Pipelines {
		t.Fatalf("Dolphin %+v", d)
	}
	if err := g.launchAgain(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the pipelines again", hasEvent(f, "pipelines 101 start"))
	g.stop()
	ev := f.eventList()
	if !slices.Contains(ev, "reset 100") || !slices.Contains(ev, "reset 101") || len(f.launches()) != 2 {
		t.Fatalf("events %v", ev)
	}
}

// TestGameRunnerLaunchFails: a launch that fails (or no Dolphin set up)
// is reported with its reason, and nothing plays.
func TestGameRunnerLaunchFails(t *testing.T) {
	f := newFakeGame()
	f.launchErr = errors.New("exec: Dolphin.exe: file does not exist")
	g, out, _, _ := newTestRunner(f, optsA)
	g.start(context.Background())
	waitFor(t, "the error", func() bool { return out.last().Err != "" })
	if d := out.last(); d.State != session.DolphinNone || !strings.Contains(d.Err, "does not exist") || d.LaunchAttempt != 1 {
		t.Fatalf("Dolphin %+v", d)
	}
	// Try again fails again: a new attempt (a new dialog).
	if err := g.launchAgain(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the second attempt", func() bool { return out.last().LaunchAttempt == 2 })
	// The player starts Dolphin on their folder: attached, the old error
	// gone (it would show again once that Dolphin closes).
	f.script(9, title)
	f.mu.Lock()
	f.running = []session.RunningDolphin{{PID: 9, UserDir: userA, State: session.DolphinTitle}}
	f.mu.Unlock()
	waitFor(t, "the pipelines", hasEvent(f, "pipelines 9 start"))
	if d := out.last(); d.Err != "" || d.PID != 9 {
		t.Fatalf("Dolphin %+v", d)
	}
	g.stop()

	f2 := newFakeGame()
	g2, out2, _, _ := newTestRunner(f2, session.LaunchOptions{UserDir: userA})
	g2.start(context.Background())
	waitFor(t, "the error", func() bool { return out2.last().Err != "" })
	g2.stop()
	if len(f2.launches()) != 0 || !strings.Contains(out2.last().Err, "Setup") {
		t.Fatalf("no Dolphin set up: %+v", out2.last())
	}
}

// TestGameRunnerFatal: a fatal pipelines error (a version mismatch) ends
// the session: fail is called and stop returns it. A transient one only
// restarts the pipelines.
func TestGameRunnerFatal(t *testing.T) {
	f := newFakeGame()
	g, _, _, fails := newTestRunner(f, optsA)
	g.start(context.Background())
	waitFor(t, "the pipelines", hasEvent(f, "pipelines 100 start"))
	f.pipeErr <- errors.New("broadcast-pose: send position: broken pipe")
	waitFor(t, "a restart", func() bool {
		n := 0
		for _, e := range f.eventList() {
			if e == "pipelines 100 start" {
				n++
			}
		}
		return n == 2
	})
	fatal := fmt.Errorf("%w: version mismatch", network.ErrFatal)
	f.pipeErr <- fatal
	waitFor(t, "the reset", hasEvent(f, "reset 100"))
	if err := g.stop(); !errors.Is(err, network.ErrFatal) {
		t.Fatalf("stop = %v", err)
	}
	if len(*fails) != 1 {
		t.Fatalf("fail called %d times", len(*fails))
	}
}

// TestGameRunnerEnvPID: WW_DOLPHIN_PID picks the Dolphin, whatever runs.
func TestGameRunnerEnvPID(t *testing.T) {
	f := newFakeGame()
	f.running = []session.RunningDolphin{{PID: 9, UserDir: userA, State: session.DolphinTitle}}
	f.script(31, title)
	out := &gameOut{}
	d := f.deps()
	d.envPID = 31
	g := newGameRunner(d, func() session.LaunchOptions { return optsA }, "Link", "x", out, nil, nil)
	g.start(context.Background())
	waitFor(t, "the pipelines", hasEvent(f, "pipelines 31 start"))
	g.stop()
}

// fakeMem is emulated RAM for hasThisMod.
type fakeMem []byte

func (m fakeMem) Read(addr uint32, n int) ([]byte, error) {
	if addr != inject.T2Address || n > len(m) {
		return nil, errors.New("unmapped")
	}
	return m[:n], nil
}

// TestHasThisMod: this build's injected code in RAM says the game is
// patched; a few changed variables don't undo that; the heap of an
// unpatched game, or nothing readable, isn't.
func TestHasThisMod(t *testing.T) {
	ram := fakeMem(slices.Clone(inject.T2Bytes))
	if !hasThisMod(ram) {
		t.Fatal("the mod itself")
	}
	for i := 0; i < 64; i++ { // the mod's variables change as it runs
		ram[i] ^= 0x5A
	}
	if !hasThisMod(ram) {
		t.Fatal("a few changed bytes")
	}
	heap := make(fakeMem, len(inject.T2Bytes))
	for i := range heap {
		heap[i] = byte(i*7 + 3)
	}
	if hasThisMod(heap) {
		t.Fatal("an unpatched game's heap")
	}
	if hasThisMod(fakeMem(nil)) {
		t.Fatal("nothing readable")
	}
	if modMatch(nil, []byte{0, 0}) != 0 {
		t.Fatal("an all-zero mod")
	}
}

// mailRAM is emulated RAM for resetMailbox: the mod's code (or not) at
// inject.T2Address, and the writes made.
type mailRAM struct {
	mod    bool
	writes []uint32
}

func (m *mailRAM) Read(addr uint32, n int) ([]byte, error) {
	if addr == inject.T2Address && m.mod {
		return slices.Clone(inject.T2Bytes[:n]), nil
	}
	return make([]byte, n), nil
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

// freePort is a free loopback port.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
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
	fb.nextPID, fc.nextPID = 200, 300
	ea := &sessionEngine{ls: &engine.LiveState{}, game: fa.deps()}
	eb := &sessionEngine{ls: &engine.LiveState{}, game: fb.deps()}
	ec := &sessionEngine{ls: &engine.LiveState{}, game: fc.deps()}
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
	if la, lb := fa.launches(), fb.launches(); len(la) != 1 || la[0].UserDir != userA || len(lb) != 1 || lb[0].UserDir != userB {
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
	fa.event("left")
	if got, want := strings.Join(fa.eventList(), ", "), "launch 100, pipelines 100 start, pipelines 100 stop, reset 100, left"; got != want {
		t.Fatalf("host teardown %q, want %q", got, want)
	}
	if st := ca.Snapshot(); st.Phase != session.Ended || st.Failed {
		t.Fatalf("host after Leave: %+v", st)
	}
	cb.Leave()
	if ev := fb.eventList(); ev[len(ev)-1] != "reset 200" {
		t.Fatalf("joiner teardown %v", ev)
	}
}
