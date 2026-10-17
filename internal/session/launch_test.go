package session

import (
	"context"
	"errors"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/playercolor"
)

// TestDolphinStateOf: what a probe of a Dolphin means.
func TestDolphinStateOf(t *testing.T) {
	for _, c := range []struct {
		name      string
		p         Probe
		launching bool
		want      DolphinState
	}{
		{"gone", Probe{}, false, DolphinNone},
		{"gone while launching", Probe{}, true, DolphinNone},
		{"booting", Probe{Running: true}, true, DolphinLaunching},
		{"open, no game", Probe{Running: true}, false, DolphinNoGame},
		{"another game", Probe{Running: true, GameID: "GALE01"}, false, DolphinWrongGame},
		{"another game while launching", Probe{Running: true, GameID: "GALE01"}, true, DolphinWrongGame},
		{"unpatched", Probe{Running: true, GameID: GameID}, false, DolphinUnpatched},
		{"title", Probe{Running: true, GameID: GameID, Patched: true}, true, DolphinTitle},
		{"in game", Probe{Running: true, GameID: GameID, Patched: true, InGame: true}, false, DolphinInGame},
		{"unpatched in game", Probe{Running: true, GameID: GameID, InGame: true}, false, DolphinUnpatched},
	} {
		if got := DolphinStateOf(c.p, c.launching); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	for s, want := range map[DolphinState][2]bool{
		DolphinTitle: {true, false}, DolphinInGame: {true, false}, DolphinWrongGame: {false, true},
		DolphinUnpatched: {false, true}, DolphinNone: {false, false}, DolphinLaunching: {false, false}, DolphinNoGame: {false, false},
	} {
		if s.Playable() != want[0] || s.Bad() != want[1] {
			t.Errorf("%s: playable %v bad %v", s, s.Playable(), s.Bad())
		}
	}
}

// TestDecideLaunch: attach to a Dolphin on this player's user folder,
// never another player's; launch otherwise; "I'll start Dolphin myself"
// never launches.
func TestDecideLaunch(t *testing.T) {
	const mine, theirs = `C:\Users\p\AppData\Roaming\Dolphin Emulator`, `C:\Users\p\AppData\Roaming\Dolphin Emulator 2`
	o := LaunchOptions{Exe: `C:\Dolphin\Dolphin.exe`, UserDir: mine, ISO: `C:\Roms\ww-multiplayer.iso`}
	manual := o
	manual.Manual = true
	for _, c := range []struct {
		name    string
		o       LaunchOptions
		running []RunningDolphin
		want    PlanKind
		pid     uint32
	}{
		{"nothing runs", o, nil, PlanLaunch, 0},
		{"mine runs", o, []RunningDolphin{{PID: 7, UserDir: mine, State: DolphinTitle}}, PlanAttach, 7},
		{"mine, other case and a trailing slash", o, []RunningDolphin{{PID: 7, UserDir: `c:\users\P\appdata\roaming\dolphin emulator\`, State: DolphinNoGame}}, PlanAttach, 7},
		// Two players on one computer: B's Dolphin is never A's.
		{"only the other player's runs", o, []RunningDolphin{{PID: 8, UserDir: theirs, State: DolphinInGame}}, PlanLaunch, 0},
		{"both run", o, []RunningDolphin{{PID: 8, UserDir: theirs, State: DolphinInGame}, {PID: 9, UserDir: mine, State: DolphinTitle}}, PlanAttach, 9},
		// A folder that can't be read (macOS) may be ours.
		{"unknown folder", o, []RunningDolphin{{PID: 5, State: DolphinTitle}}, PlanAttach, 5},
		// Ours runs the patched game: it wins over an older one at its game list.
		{"patched first", o, []RunningDolphin{{PID: 3, UserDir: mine, State: DolphinNoGame}, {PID: 4, UserDir: mine, State: DolphinInGame}}, PlanAttach, 4},
		// A wrong game on our folder is attached to; the window says what to boot.
		{"wrong game on mine", o, []RunningDolphin{{PID: 6, UserDir: mine, State: DolphinWrongGame}}, PlanAttach, 6},
		{"manual, nothing runs", manual, nil, PlanWait, 0},
		// Opened without -u: its default folder, maybe not the one saved.
		{"manual, Wind Waker on a default folder", manual, []RunningDolphin{{PID: 8, UserDir: theirs, State: DolphinTitle}}, PlanAttach, 8},
		// Two players on one computer: A's app launched A's Dolphin with
		// -u; B starts Dolphin 2 himself and hasn't yet. B waits.
		{"manual, the other player's explicit -u", manual, []RunningDolphin{{PID: 8, UserDir: theirs, Explicit: true, State: DolphinTitle}}, PlanWait, 0},
		{"manual, the other player's explicit -u in game", manual, []RunningDolphin{{PID: 8, UserDir: theirs, Explicit: true, State: DolphinInGame}, {PID: 9, UserDir: theirs, State: DolphinUnpatched}}, PlanAttach, 9},
		{"manual, another session plays on it", manual, []RunningDolphin{{PID: 8, UserDir: theirs, Claimed: true, State: DolphinInGame}}, PlanWait, 0},
		{"manual, explicit -u on my folder", manual, []RunningDolphin{{PID: 8, UserDir: mine, Explicit: true, Claimed: true, State: DolphinTitle}}, PlanAttach, 8},
		{"manual, another game elsewhere", manual, []RunningDolphin{{PID: 8, UserDir: theirs, State: DolphinWrongGame}}, PlanWait, 0},
		{"manual, mine", manual, []RunningDolphin{{PID: 8, UserDir: theirs, State: DolphinTitle}, {PID: 9, UserDir: mine, State: DolphinNoGame}}, PlanAttach, 9},
	} {
		p := DecideLaunch(c.o, c.running)
		if p.Kind != c.want || p.PID != c.pid || p.Why == "" {
			t.Errorf("%s: %+v, want %s pid %d", c.name, p, c.want, c.pid)
		}
	}
}

// TestLobbyNotReady: who Start waits for.
func TestLobbyNotReady(t *testing.T) {
	l := Lobby{Member: 1, Members: []LobbyMember{
		{ID: 1, Name: "Host", Via: ViaApp},                          // ourselves: never asked
		{ID: 2, Name: "Ready", Ready: true, Via: ViaApp},            //
		{ID: 3, Name: "Slow", Via: ViaApp},                          // not ready
		{ID: 4, Name: "Gone", Ready: true, Away: true, Via: ViaApp}, // away
		{ID: 65537, Name: "Cli", Ready: true, Via: ViaCLI},          // a CLI player is always ready
	}}
	var names []string
	for _, m := range l.NotReady() {
		names = append(names, m.Name)
	}
	if len(names) != 2 || names[0] != "Slow" || names[1] != "Gone" {
		t.Fatalf("NotReady = %v", names)
	}
}

// TestStartReadiness: Start is the host's, in the lobby phase, and waits
// for everyone to be ready; StartAnyway doesn't wait.
func TestStartReadiness(t *testing.T) {
	e := newFakeLobby()
	c := New(e, Options{})
	if _, err := c.HostLobby(context.Background(), HostOptions{Name: "Me"}); err != nil {
		t.Fatal(err)
	}
	defer c.Leave()
	out := e.out(t)
	l := lobbyView(LobbyPhaseLobby, true)
	l.Members = append(l.Members, LobbyMember{ID: 2, Name: "Jo", Color: playercolor.Vanilla(), Via: ViaApp})
	out.LobbyState(l)
	if err := c.Start(); !errors.Is(err, ErrNotReady) {
		t.Fatalf("Start with Jo not ready = %v", err)
	}
	if n := len(e.commands()); n != 0 {
		t.Fatalf("%d commands sent for a refused Start", n)
	}
	if err := c.StartAnyway(); err != nil {
		t.Fatal(err)
	}
	l.Members[2].Ready = true
	out.LobbyState(l)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	if cmds := e.commands(); len(cmds) != 2 || cmds[0].Kind != LobbyStart || cmds[1].Kind != LobbyStart {
		t.Fatalf("commands %+v", cmds)
	}
	// After START: no second Start, anyway or not.
	l.Phase = LobbyPhaseStarting
	out.LobbyState(l)
	if err := c.StartAnyway(); !errors.Is(err, ErrWrongPhase) {
		t.Fatalf("StartAnyway after Start = %v", err)
	}

	// A joiner can't start.
	e2 := newFakeLobby()
	c2 := New(e2, Options{})
	if _, err := c2.JoinLobby(context.Background(), JoinOptions{Name: "Jo", Addr: "h"}); err != nil {
		t.Fatal(err)
	}
	defer c2.Leave()
	e2.out(t).LobbyState(lobbyView(LobbyPhaseLobby, false))
	for _, f := range []func() error{c2.Start, c2.StartAnyway} {
		if err := f(); !errors.Is(err, ErrNotHost) {
			t.Fatalf("joiner start = %v", err)
		}
	}
}

// TestControllerDolphin: the engine's Dolphin reports become
// State.Dolphin with an EventState; LaunchDolphin works after Start only.
func TestControllerDolphin(t *testing.T) {
	e := newFakeLobby()
	c := New(e, Options{})
	ch, unsub := c.Subscribe()
	defer unsub()
	id, err := c.JoinLobby(context.Background(), JoinOptions{Name: "Me", Addr: "h", Launch: LaunchOptions{ISO: "ww.iso"}})
	if err != nil {
		t.Fatal(err)
	}
	out := e.out(t)
	out.LobbyState(lobbyView(LobbyPhaseLobby, false))
	if err := c.LaunchDolphin(); !errors.Is(err, ErrWrongPhase) {
		t.Fatalf("LaunchDolphin before Start = %v", err)
	}
	out.LobbyState(lobbyView(LobbyPhaseStarting, false))
	collect(t, ch, func(ev Event) bool { return ev.Kind == EventState && c.Snapshot().Phase == Starting })
	out.Dolphin(DolphinInfo{State: DolphinLaunching, PID: 42, Launched: true})
	collect(t, ch, func(ev Event) bool { return ev.Kind == EventState && ev.Session == id })
	if d := c.Snapshot().Dolphin; d.State != DolphinLaunching || d.PID != 42 || !d.Launched {
		t.Fatalf("Dolphin = %+v", d)
	}
	if err := c.LaunchDolphin(); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	n := e.launches
	e.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d launches", n)
	}
	c.Leave()
	out.Dolphin(DolphinInfo{State: DolphinTitle}) // after the session: ignored
	if d := c.Snapshot().Dolphin; d.State != DolphinLaunching {
		t.Fatalf("a late report changed the ended session: %+v", d)
	}
}

// TestLaunchOptionsFn: LaunchFn is asked when Start comes, over Launch.
func TestLaunchOptionsFn(t *testing.T) {
	o := HostOptions{Launch: LaunchOptions{Exe: "a"}}
	if o.LaunchOptions().Exe != "a" {
		t.Fatal("Launch")
	}
	o.LaunchFn = func() LaunchOptions { return LaunchOptions{Exe: "b", Manual: true} }
	if got := o.LaunchOptions(); got.Exe != "b" || !got.Manual {
		t.Fatalf("LaunchFn: %+v", got)
	}
	j := JoinOptions{LaunchFn: func() LaunchOptions { return LaunchOptions{ISO: "x"} }}
	if j.LaunchOptions().ISO != "x" {
		t.Fatal("JoinOptions.LaunchFn")
	}
}
