package gui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/config"
	"github.com/StephenSHorton/ww-multiplayer/internal/playercolor"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
)

// TestDolphinLabel: every Dolphin state the app reports has chip words,
// and an unknown one (a newer build's) shows as it is.
func TestDolphinLabel(t *testing.T) {
	for _, s := range []session.DolphinState{session.DolphinNone, session.DolphinLaunching, session.DolphinTitle, session.DolphinInGame,
		session.DolphinNoGame, session.DolphinWrongGame, session.DolphinUnpatched} {
		if l := dolphinLabel(string(s)); l == "" || l == string(s) {
			t.Errorf("%s: %q", s, l)
		}
	}
	if dolphinLabel("") != "" || dolphinLabel("paused") != "paused" {
		t.Fatal("empty or unknown state")
	}
	if dolphinLabel("in_game") != "In game" || dolphinLabel("title") != "Title screen" || dolphinLabel("none") != "Not running" {
		t.Fatal("chip words")
	}
}

func gameState(phase session.LobbyPhase, d session.DolphinInfo) session.State {
	red, blue := playercolor.Palette()[1].Color, playercolor.Palette()[4].Color
	return session.State{Session: 3, Phase: session.InGame, Role: session.RoleJoin, Name: "Link", Dolphin: d,
		Lobby: &session.Lobby{Member: 1, Phase: phase, Link: session.LobbyLink{State: session.LinkUp}, Members: []session.LobbyMember{
			{ID: 1, Name: "Link", Color: red, Ready: true, Via: session.ViaApp, Dolphin: "launching"},
			{ID: 2, Name: "Tetra 192.168.1.9", Color: blue, Ready: true, Host: true, Via: session.ViaApp, Dolphin: "in_game"},
			{ID: 3, Name: "Medli", Color: blue, Ready: true, Via: session.ViaApp, Dolphin: "unpatched"},
		}},
		Live: session.Live{LocalOK: true, LocalX: 100, LocalZ: 100, Players: []session.Player{
			{Name: "Tetra 192.168.1.9", X: 100 + 300, Y: 5, Z: 100 - 400, Color: blue, HasColor: true},
			{Name: "Medli", X: 9000, Z: 100},
		}}}
}

// TestStateViewInGame: the Session screen's data: our own Dolphin chip is
// our fresher report, the others' come from the lobby; each other player
// is "same area" or "far away" from the pose positions; the minimap has
// their offsets, names masked like every peer name.
func TestStateViewInGame(t *testing.T) {
	st := gameState(session.LobbyPhaseInGame, session.DolphinInfo{State: session.DolphinTitle, PID: 7, Launched: true, Pipelines: true})
	v := stateView(st, true, "v1", time.Now(), hideIPs)
	l := v.Lobby
	if l.CanStart || v.Color != playercolor.Palette()[1].Color.Hex() {
		t.Fatalf("view %+v", v)
	}
	me, tetra, medli := l.Members[0], l.Members[1], l.Members[2]
	if me.Dolphin != "title" || me.DolphinLabel != "Title screen" || me.Area != "" {
		t.Fatalf("own row %+v", me)
	}
	if tetra.DolphinLabel != "In game" || tetra.Area != "near" || tetra.Dist != 500 || strings.Contains(tetra.Name, "192.168") {
		t.Fatalf("Tetra %+v", tetra)
	}
	if medli.DolphinLabel != "Not patched" || medli.Area != "far" {
		t.Fatalf("Medli %+v", medli)
	}
	if d := v.Dolphin; d.State != "title" || d.Label != "Title screen" || d.PID != 7 || !d.Pipelines {
		t.Fatalf("Dolphin %+v", d)
	}
	m := v.Map
	if m == nil || !m.SelfOK || len(m.Players) != 2 || m.Units != sameAreaUnits || m.Color != v.Color {
		t.Fatalf("map %+v", m)
	}
	if p := m.Players[0]; p.DX != 300 || p.DZ != -400 || strings.Contains(p.Name, "192.168") || p.Color == "" {
		t.Fatalf("map dot %+v", p)
	}
	if v.Dialog != nil || v.Banner != nil {
		t.Fatalf("dialog %+v banner %+v", v.Dialog, v.Banner)
	}
	// The switch on: names unmasked.
	if v := stateView(st, true, "v1", time.Now(), masker{show: true}); !strings.Contains(v.Map.Players[0].Name, "192.168.1.9") {
		t.Fatalf("switch on: %+v", v.Map.Players[0])
	}
	// No position yet: no area, an empty map.
	st.LocalOK = false
	v = stateView(st, true, "v1", time.Now(), hideIPs)
	if v.Lobby.Members[1].Area != "" || v.Map.SelfOK || len(v.Map.Players) != 0 {
		t.Fatalf("no position: %+v %+v", v.Lobby.Members[1], v.Map)
	}
}

// TestGameDialogs: a wrong game or an unpatched disc opens a live dialog
// naming the patched disc; a failed launch one with Try again; a closed
// Dolphin the banner with Launch (Waiting, for a player who starts it).
func TestGameDialogs(t *testing.T) {
	iso := `C:\Roms\ww-multiplayer.iso`
	for _, c := range []struct {
		d      session.DolphinInfo
		dialog string
		banner string
	}{
		{session.DolphinInfo{State: session.DolphinWrongGame, PID: 4, ISO: iso}, dlgDolphin, ""},
		{session.DolphinInfo{State: session.DolphinUnpatched, PID: 4, ISO: iso}, dlgDolphin, ""},
		// After a failed launch the banner offers Try again too: closing
		// the dialog leaves a way to retry.
		{session.DolphinInfo{State: session.DolphinNone, Err: "exec: not found 10.0.0.5", LaunchAttempt: 1}, dlgLaunchFail, "Couldn't start Dolphin."},
		{session.DolphinInfo{State: session.DolphinNone}, "", "Dolphin closed."},
		{session.DolphinInfo{State: session.DolphinNone, Manual: true}, "", "Waiting for Dolphin…"},
		{session.DolphinInfo{State: session.DolphinTitle, PID: 4}, "", ""},
		{session.DolphinInfo{State: session.DolphinLaunching, PID: 4}, "", ""},
		{session.DolphinInfo{}, "", ""}, // before Start
	} {
		v := stateView(gameState(session.LobbyPhaseInGame, c.d), true, "v1", time.Now(), hideIPs)
		kind := ""
		if v.Dialog != nil {
			kind = v.Dialog.Kind
			if !v.Dialog.Live || v.Dialog.Home {
				t.Errorf("%+v: dialog %+v", c.d, v.Dialog)
			}
		}
		title := ""
		if v.Banner != nil {
			title = v.Banner.Title
		}
		if kind != c.dialog || title != c.banner {
			t.Errorf("%+v: dialog %q banner %q, want %q %q", c.d, kind, title, c.dialog, c.banner)
		}
		if kind == dlgDolphin && !strings.Contains(v.Dialog.Text, iso) {
			t.Errorf("the dialog doesn't name the disc: %q", v.Dialog.Text)
		}
		if kind == dlgLaunchFail && (strings.Contains(v.Dialog.Detail, "10.0.0.5") || v.Dialog.Actions[0].ID != actLaunch) {
			t.Errorf("launch dialog %+v", v.Dialog)
		}
		if (title == "Dolphin closed." || title == "Couldn't start Dolphin.") && v.Banner.Action != actLaunch {
			t.Errorf("banner %+v", v.Banner)
		}
	}
	// Each failed attempt is its own dialog: a closed one doesn't hide the
	// next failure.
	fail := func(n int) string {
		return gameDialog(gameState(session.LobbyPhaseInGame, session.DolphinInfo{State: session.DolphinNone, Err: "x", LaunchAttempt: n}), true, hideIPs).Key
	}
	if fail(1) == fail(2) {
		t.Fatalf("one key for two attempts: %q", fail(1))
	}
	// Not after the session, nor for a pre-lobby host.
	st := gameState(session.LobbyPhaseInGame, session.DolphinInfo{State: session.DolphinWrongGame})
	if gameDialog(st, false, hideIPs) != nil || dolphinBanner(st, false) != nil {
		t.Fatal("ended session")
	}
	st.Lobby.Legacy = true
	if gameDialog(st, true, hideIPs) != nil {
		t.Fatal("legacy")
	}
}

// TestBridgeStart: the host's Start waits for everyone (the error names
// who), Start anyway doesn't, and neither goes before this player's
// Dolphin is set up (unless they start it themselves). The setting is
// saved, and Start reads it.
func TestBridgeStart(t *testing.T) {
	b, e, path := newTestBridge(t)
	b.denv = func() launch.Env {
		return launch.Env{GOOS: "windows", ConfigDir: "cfg", Exists: func(string) bool { return false }}
	}
	var procs []launch.Process
	b.procs = func() []launch.Process { return procs }
	if err := b.Host(HostRequest{Name: "Link"}); err != nil {
		t.Fatal(err)
	}
	out := e.out(t)
	lobby := session.Lobby{Member: 1, Host: true, Phase: session.LobbyPhaseLobby, Members: []session.LobbyMember{
		{ID: 1, Name: "Link", Via: session.ViaApp, Host: true},
		{ID: 2, Name: "Aryll 10.1.2.3", Via: session.ViaApp},
	}}
	out.LobbyState(lobby)
	// No Dolphin set up, and none runs: Start would have to launch one.
	if err := b.Start(false); err == nil || !strings.Contains(err.Error(), "Setup") {
		t.Fatalf("Start without a Dolphin = %v", err)
	}
	// One runs (no user folder saved: any counts as ours): Start plays on
	// it, no setup needed; it still waits for Aryll.
	procs = []launch.Process{{PID: 6, ArgsKnown: true, Args: []string{"-u", "cfg"}}}
	if err := b.Start(false); err == nil || strings.Contains(err.Error(), "Setup") || !strings.Contains(err.Error(), "Aryll") {
		t.Fatalf("Start with a Dolphin running = %v", err)
	}
	procs = nil
	// WW_DOLPHIN_PID names one: no setup needed either.
	b.getenv = func(k string) string {
		if k == "WW_DOLPHIN_PID" {
			return "6"
		}
		return ""
	}
	if err := b.Start(false); err == nil || strings.Contains(err.Error(), "Setup") {
		t.Fatalf("Start with WW_DOLPHIN_PID = %v", err)
	}
	b.getenv = func(string) string { return "" }
	exe := filepath.Join(t.TempDir(), "Dolphin.exe")
	os.WriteFile(exe, nil, 0o644)
	b.mu.Lock()
	b.cfg.Dolphin.Exe, b.cfg.ISO.Patched = exe, `C:\Roms\ww-multiplayer.iso`
	b.mu.Unlock()
	err := b.Start(false)
	if err == nil || !strings.Contains(err.Error(), "Aryll") || strings.Contains(err.Error(), "10.1.2.3") {
		t.Fatalf("Start with Aryll not ready = %v", err)
	}
	if len(e.cmds) != 0 {
		t.Fatalf("commands %+v", e.cmds)
	}
	if err := b.Start(true); err != nil {
		t.Fatal(err)
	}
	if len(e.cmds) != 1 || e.cmds[0].Kind != session.LobbyStart {
		t.Fatalf("commands %+v", e.cmds)
	}
	// The launch options Start's engine gets: the saved Dolphin, its
	// default user folder, the patched disc.
	e.mu.Lock()
	h := e.host[0]
	e.mu.Unlock()
	o := h.LaunchOptions()
	if o.Exe != exe || o.ISO != `C:\Roms\ww-multiplayer.iso` || o.UserDir != filepath.Join("cfg", "Dolphin Emulator") || o.Manual || !o.InstallCheats {
		t.Fatalf("launch options %+v", o)
	}

	// I'll start Dolphin myself: saved, and Start's options follow.
	if err := b.SetStartDolphinMyself(true); err != nil {
		t.Fatal(err)
	}
	if o := h.LaunchOptions(); !o.Manual {
		t.Fatalf("after the switch: %+v", o)
	}
	if !b.State().StartDolphinMyself {
		t.Fatal("State doesn't show the setting")
	}
	c, _ := config.Load(path)
	if c.Dolphin.LaunchOnStart {
		t.Fatal("not saved")
	}
	// Manual: no Dolphin needed to Start.
	b.mu.Lock()
	b.cfg.Dolphin.Exe = ""
	b.mu.Unlock()
	if err := b.Start(true); err != nil {
		t.Fatal(err)
	}

	// LaunchDolphin goes to the engine after Start only.
	if err := b.LaunchDolphin(); !errors.Is(err, session.ErrWrongPhase) {
		t.Fatalf("LaunchDolphin in the lobby = %v", err)
	}
	lobby.Phase = session.LobbyPhaseStarting
	out.LobbyState(lobby)
	if err := b.LaunchDolphin(); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	n := e.launches
	e.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d launches", n)
	}
	if err := b.Start(true); !errors.Is(err, session.ErrWrongPhase) {
		t.Fatalf("Start after Start = %v", err)
	}
}
