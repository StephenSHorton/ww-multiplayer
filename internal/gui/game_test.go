package gui

import (
	"errors"
	"github.com/StephenSHorton/ww-multiplayer/internal/seachart"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/config"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/playercolor"
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
	if m == nil || !m.SelfOK || len(m.Players) != 2 || m.Color != v.Color || m.Self.X != 100 || m.Self.Square != "D4" {
		t.Fatalf("map %+v", m)
	}
	if p := m.Players[0]; p.X != 400 || p.Z != -300 || !p.Drawn || strings.Contains(p.Name, "192.168") || p.Color == "" {
		t.Fatalf("map dot %+v", p)
	}
	if v.Dialog != nil || v.Banner != nil {
		t.Fatalf("dialog %+v banner %+v", v.Dialog, v.Banner)
	}
	// The switch on: names unmasked.
	if v := stateView(st, true, "v1", time.Now(), masker{show: true}); !strings.Contains(v.Map.Players[0].Name, "192.168.1.9") {
		t.Fatalf("switch on: %+v", v.Map.Players[0])
	}
	// No position of our own yet: no area, no self dot; the others are
	// still on the chart (their positions are absolute).
	st.LocalOK = false
	v = stateView(st, true, "v1", time.Now(), hideIPs)
	if v.Lobby.Members[1].Area != "" || v.Map.SelfOK || len(v.Map.Players) != 2 {
		t.Fatalf("no position: %+v %+v", v.Lobby.Members[1], v.Map)
	}
}

// TestMapPlaces: a Link on the sea is drawn where it is; one in a known
// dungeon or a stage the disc places is pinned to its island's centre with
// the place's name; one in a stage nobody knows is listed, not drawn.
func TestMapPlaces(t *testing.T) {
	ch := &seachart.Chart{Islands: []seachart.Island{{Square: seachart.Square{GX: 2, GZ: -2}, CX: 212300, CZ: -195500}},
		Stages: map[string]seachart.Square{"Omasao": {GX: -2, GZ: 3}}}
	st := session.State{Live: session.Live{LocalOK: true, LocalX: -200000, LocalZ: 320000,
		LocalPlace: session.Place{Stage: "sea", Room: 44, InPlay: true}, LocalHasPlace: true,
		Players: []session.Player{
			{Name: "Komali", X: 10, Y: 1, Z: 20, Place: session.Place{Stage: "M_NewD2", Room: 3}, HasPlace: true, Fresh: true},
			{Name: "Aryll", X: 10, Y: 1, Z: 20, Place: session.Place{Stage: "Omasao"}, HasPlace: true, Fresh: true},
			{Name: "Zill", X: 10, Y: 1, Z: 20, Place: session.Place{Stage: "Xyzzy"}, HasPlace: true, Fresh: true},
			{Name: "Old", X: 5000, Y: 1, Z: -7000},
		}}}
	m := mapView(st, "#3c9e2e", hideIPs, ch)
	if s := m.Self; !m.SelfOK || s.X != -200000 || s.Z != 320000 || s.Square != "B7" || s.Island != "Outset Island" || s.Pinned {
		t.Fatalf("self %+v", s)
	}
	if p := m.Players[0]; !p.Drawn || !p.Pinned || p.X != 212300 || p.Z != -195500 || p.Square != "F2" || p.Where != "Dragon Roost Cavern" {
		t.Errorf("dungeon %+v", p)
	}
	if p := m.Players[1]; !p.Drawn || p.X != -200000 || p.Z != 300000 || p.Square != "B7" || p.Where != "Outset Island" {
		t.Errorf("disc-placed interior (no tile: the square's centre) %+v", p)
	}
	if p := m.Players[2]; p.Drawn || p.Where != "Xyzzy" {
		t.Errorf("unknown stage %+v", p)
	}
	if p := m.Players[3]; !p.Drawn || p.X != 5000 || p.Square != "D4" {
		t.Errorf("no place: taken as the sea %+v", p)
	}
	// Without the disc's chart, a known dungeon is still placed.
	if p := mapView(st, "", hideIPs, nil).Players[0]; !p.Drawn || p.X != 200000 || p.Z != -200000 {
		t.Errorf("no chart %+v", p)
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

// TestMemberVitals: this player's row takes its own game's vitals, another
// row the remote's of the same name; unknown is nil (wind#55).
func TestMemberVitals(t *testing.T) {
	var st session.State
	if memberVitals(st, "Me", true) != nil || memberVitals(st, "Blake", false) != nil {
		t.Fatal("vitals before any are known")
	}
	st.LocalVitals, st.LocalVitalsOK = session.Vitals{Life: 8, MaxLife: 12, Rupees: 5}, true
	st.Players = []session.Player{
		{Name: "Sam"},
		{Name: "Blake", HasVitals: true, Vitals: session.Vitals{Life: 1, MaxLife: 20, Magic: 3, MaxMagic: 32, Rupees: 47}},
	}
	if v := memberVitals(st, "Me", true); v == nil || *v != (VitalsView{Life: 8, MaxLife: 12, Rupees: 5}) {
		t.Errorf("self: %+v", v)
	}
	if v := memberVitals(st, "Blake", false); v == nil || *v != (VitalsView{Life: 1, MaxLife: 20, Magic: 3, MaxMagic: 32, Rupees: 47}) {
		t.Errorf("Blake: %+v", v)
	}
	if v := memberVitals(st, "Sam", false); v != nil {
		t.Errorf("Sam sends none, got %+v", v)
	}
}

// TestStateViewPlaces: with places on the wire, each other row reads
// where its game is ("in"), "loading" between stages and "stale" with its
// age once quiet; the header says where we are; a partner on another stage,
// loading or stale is never "same area", however near its coordinates
// (the sea chart still places everyone); a partner whose build sends no place keeps the old
// position hint.
func TestStateViewPlaces(t *testing.T) {
	st := gameState(session.LobbyPhaseInGame, session.DolphinInfo{State: session.DolphinInGame, PID: 7, Launched: true, Pipelines: true})
	st.LocalPlace, st.LocalHasPlace = session.Place{Stage: "M_NewD2", Room: 3, InPlay: true}, true
	tetra, medli := &st.Players[0], &st.Players[1]
	tetra.Place, tetra.HasPlace, tetra.Fresh = session.Place{Stage: "M_NewD2", Room: 5, InPlay: true}, true, true
	medli.X, medli.Z = 150, 150 // near by coordinates, but on the sea
	medli.Place, medli.HasPlace, medli.Fresh = session.Place{Stage: "sea", Room: 11, InPlay: true}, true, true

	v := stateView(st, true, "v1", time.Now(), hideIPs)
	if v.Where != "Dragon Roost Cavern, room 3" {
		t.Fatalf("Where %q", v.Where)
	}
	tv, mv := v.Lobby.Members[1], v.Lobby.Members[2]
	if tv.Place != "Dragon Roost Cavern, room 5" || tv.PlaceState != "in" || tv.Area != "near" {
		t.Fatalf("Tetra %+v", tv)
	}
	if mv.Place != "Sea, B4 (Windfall Island)" || mv.PlaceState != "in" || mv.Area != "" {
		t.Fatalf("Medli on another stage %+v", mv)
	}
	// The sea chart places everyone (Tetra pinned to her dungeon's island):
	// the place gate is for the Players rows' "same area" only.
	if len(v.Map.Players) != 2 || !strings.HasPrefix(v.Map.Players[0].Name, "Tetra") || !v.Map.Players[0].Pinned {
		t.Fatalf("map %+v", v.Map.Players)
	}

	// Medli loading, then quiet for 12 s.
	medli.Place.Stage, medli.Place.InPlay = "M_NewD2", false
	v = stateView(st, true, "v1", time.Now(), hideIPs)
	if mv := v.Lobby.Members[2]; mv.PlaceState != "loading" || mv.Place != "" || mv.Area != "" {
		t.Fatalf("loading %+v map %+v", mv, v.Map.Players)
	}
	medli.Place.InPlay, medli.Fresh, medli.PlaceAge = true, false, 12*time.Second+400*time.Millisecond
	v = stateView(st, true, "v1", time.Now(), hideIPs)
	if mv := v.Lobby.Members[2]; mv.PlaceState != "stale" || mv.LastSeen != 12 || mv.Place != "Dragon Roost Cavern, room 11" || mv.Area != "" {
		t.Fatalf("stale %+v map %+v", mv, v.Map.Players)
	}

	// No place from Medli's build: the position hint as before.
	*medli = session.Player{Name: "Medli", X: 150, Z: 150}
	v = stateView(st, true, "v1", time.Now(), hideIPs)
	if mv := v.Lobby.Members[2]; mv.PlaceState != "" || mv.Area != "near" {
		t.Fatalf("no place %+v map %+v", mv, v.Map.Players)
	}

	// Our own game between stages: no header.
	st.LocalPlace.InPlay = false
	if v := stateView(st, true, "v1", time.Now(), hideIPs); v.Where != "" {
		t.Fatalf("loading header %q", v.Where)
	}
}
