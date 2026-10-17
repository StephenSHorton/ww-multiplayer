// Package enginetest has fakes for the tests of the multiplayer engine
// (mods/multiplayer/engine) and of what plugs into it (co-op in package
// main): scripted Dolphins for its game side (Game), a LobbyOutput that
// records (Out), and waits on a controller's state.
//
// It does not import the engine, so the engine's own tests can use it:
// each side builds its engine.GameDeps from a Game's methods.
package enginetest

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// Game is a game side's Dolphins, scripted: List lists Running, Probe
// answers each PID's script (its last answer repeats), Launch starts a
// new PID, and the pipelines and resets are recorded in its events, in
// order. Nothing here ever closes a Dolphin: there is no seam for it.
type Game struct {
	mu        sync.Mutex
	events    []string
	probes    map[uint32][]session.Probe
	launched  []session.LaunchOptions
	Running   []session.RunningDolphin
	NextPID   uint32
	LaunchErr error
	PipeErr   chan error // a Pipelines run returns what arrives here
}

// NewGame is a Game with nothing running, whose first launch is PID 100.
func NewGame() *Game {
	return &Game{probes: map[uint32][]session.Probe{}, NextPID: 100, PipeErr: make(chan error, 1)}
}

// Event records one event.
func (f *Game) Event(format string, args ...any) {
	f.mu.Lock()
	f.events = append(f.events, fmt.Sprintf(format, args...))
	f.mu.Unlock()
}

// List is the running Dolphins (GameDeps.Running).
func (f *Game) List() []session.RunningDolphin {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.Running)
}

// Probe is pid's next scripted answer (GameDeps.Probe).
func (f *Game) Probe(pid uint32) session.Probe {
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

// Script sets PID pid's probe answers.
func (f *Game) Script(pid uint32, ps ...session.Probe) {
	f.mu.Lock()
	f.probes[pid] = ps
	f.mu.Unlock()
}

// Launch starts PID NextPID, booting to the title screen, or fails with
// LaunchErr (GameDeps.Launch).
func (f *Game) Launch(o session.LaunchOptions, _ log.Reporter) (uint32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.LaunchErr != nil {
		return 0, f.LaunchErr
	}
	f.launched = append(f.launched, o)
	pid := f.NextPID
	f.NextPID++
	f.events = append(f.events, fmt.Sprintf("launch %d", pid))
	if _, ok := f.probes[pid]; !ok {
		f.probes[pid] = []session.Probe{Booting, Booting, Title}
	}
	return pid, nil
}

// Pipelines runs until ctx ends or an error arrives on PipeErr
// (GameDeps.Pipelines).
func (f *Game) Pipelines(ctx context.Context, cancel context.CancelFunc, name, addr string, pid uint32, _ log.Reporter) error {
	f.Event("pipelines %d start", pid)
	defer f.Event("pipelines %d stop", pid)
	select {
	case <-ctx.Done():
		return nil
	case err := <-f.PipeErr:
		cancel()
		return err
	}
}

// Reset records a mailbox reset (GameDeps.Reset).
func (f *Game) Reset(pid uint32) { f.Event("reset %d", pid) }

// Events is the events so far.
func (f *Game) Events() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.events)
}

// Launches is the launch options of every launch so far.
func (f *Game) Launches() []session.LaunchOptions {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.launched)
}

// HasEvent says ev has happened in f.
func HasEvent(f *Game, ev string) func() bool {
	return func() bool { return slices.Contains(f.Events(), ev) }
}

// What a probe finds.
var (
	Booting   = session.Probe{Running: true}
	Title     = session.Probe{Running: true, GameID: session.GameID, Patched: true}
	InGame    = session.Probe{Running: true, GameID: session.GameID, Patched: true, InGame: true}
	Unpatched = session.Probe{Running: true, GameID: session.GameID}
	OtherGame = session.Probe{Running: true, GameID: "GALE01"}
	Closed    = session.Probe{}
)

// Two players' Dolphin user folders, and the first one's launch options.
const (
	UserA = `C:\Users\p\AppData\Roaming\Dolphin Emulator`
	UserB = `C:\Users\p\AppData\Roaming\Dolphin Emulator 2`
)

// OptsA is a player's launch options on UserA.
var OptsA = session.LaunchOptions{Exe: `C:\Dolphin\Dolphin.exe`, UserDir: UserA, ISO: `C:\Roms\ww-multiplayer.iso`, InstallCheats: true}

// Out is a session.LobbyOutput that records log lines and Dolphin
// reports, safe for the game side's goroutine.
type Out struct {
	mu      sync.Mutex
	lines   []string
	dolphin []session.DolphinInfo
}

func (o *Out) Log(_ log.Level, msg string) {
	o.mu.Lock()
	o.lines = append(o.lines, msg)
	o.mu.Unlock()
}
func (o *Out) Chat(string, string)                        {}
func (o *Out) Ask(context.Context, session.Question) bool { return false }
func (o *Out) Advise(session.Advice)                      {}
func (o *Out) LobbyState(session.Lobby)                   {}
func (o *Out) LobbyNotice(session.Notice)                 {}
func (o *Out) LobbyStart(session.LobbySettings)           {}
func (o *Out) LobbyGo(string)                             {}
func (o *Out) LobbyRequest(string, string)                {}
func (o *Out) LobbyLink(session.LobbyLink)                {}
func (o *Out) Dolphin(d session.DolphinInfo) {
	o.mu.Lock()
	o.dolphin = append(o.dolphin, d)
	o.mu.Unlock()
}

// Lines is the log lines so far.
func (o *Out) Lines() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.lines)
}

// Last is the last Dolphin report, zero before one.
func (o *Out) Last() session.DolphinInfo {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.dolphin) == 0 {
		return session.DolphinInfo{}
	}
	return o.dolphin[len(o.dolphin)-1]
}

// States is the Dolphin states reported, repeats dropped.
func (o *Out) States() []session.DolphinState {
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

// Logged says a log line contains sub.
func (o *Out) Logged(sub string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, l := range o.lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

// WaitSnap polls the controller until cond holds.
func WaitSnap(t *testing.T, c *session.Controller, what string, cond func(session.State) bool) session.State {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := c.Snapshot()
		if cond(st) {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: state %+v (lobby %+v)", what, st, st.Lobby)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// WaitForCond polls cond for up to 5 s and says whether it held.
func WaitForCond(cond func() bool) bool {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

// WaitFor fails t when cond does not hold within 5 s.
func WaitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	if !WaitForCond(cond) {
		t.Fatalf("timed out waiting for %s", what)
	}
}

// FreePort is a free loopback port.
func FreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// SetRunning sets Running while the game side runs.
func (f *Game) SetRunning(r []session.RunningDolphin) {
	f.mu.Lock()
	f.Running = r
	f.mu.Unlock()
}
