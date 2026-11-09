package coopsession

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/gui"
	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind/bindtest"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/engine"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/engine/enginetest"
)

// bridgeEnv is a window's environment overrides for these tests: the
// Dolphin is named (Start attaches, never launches, with no Setup), the
// lobby listens on loopback, and no LAN beacon goes out.
func bridgeEnv(k string) string {
	switch k {
	case "WW_DOLPHIN_PID":
		return "1"
	case gui.EnvListenHost:
		return "127.0.0.1"
	case gui.EnvLan:
		return "off"
	}
	return ""
}

// bridgeSide is one app as the window drives it: a gui.Bridge over a
// controller over the engine, with the co-op side over rig and the
// game side over scripted Dolphin pid (in game at once).
func bridgeSide(t *testing.T, rig *coopRig, who string, pid uint32) (*gui.Bridge, *enginetest.Game) {
	t.Helper()
	f := enginetest.NewGame()
	f.Script(pid, enginetest.InGame)
	d := gameDeps(f)
	d.EnvPID = pid
	d.CoopSync = coopSyncFake(f)
	e := engine.New(engine.Config{Live: &engine.LiveState{}, Game: d, Coop: coopApp{env: testEnv, lobby: rig.deps(t, who)}})
	b := gui.NewBridge(gui.Options{Version: "v9.9.9", Controller: session.New(e, session.Options{}),
		ConfigPath: filepath.Join(t.TempDir(), "config.json"), Getenv: bridgeEnv})
	t.Cleanup(b.Leave)
	return b, f
}

// waitView polls b's State until cond holds, for up to 10 s.
func waitView(t *testing.T, b *gui.Bridge, what string, cond func(gui.StateView) bool) gui.StateView {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		v := b.State()
		if cond(v) {
			return v
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: state phase %q lobby %+v coop %+v banner %+v dialog %+v", what, v.Phase, v.Lobby, v.Coop, v.Banner, v.Dialog)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestBridgeHostAutoAdoptsBoundSave is check 2 of card #1165, the 11-08
// session through the window's own calls: the host's loaded save is
// tied to a world that is not on the host's PC, and the host never
// touches the co-op settings (no SetCoop: a fresh config sends "auto" by
// itself). Host, Join, Start anyway: both windows reach the game, the
// world is founded on the host under the save's ID and marked adopted,
// the joiner's co-op runs in it, and the host never sees a blocked
// founding dialog. Before #1173 the founding blocked on the bound file
// and nothing was founded; before #1175 GO waited on it, so the lobby
// sat on "Starting".
func TestBridgeHostAutoAdoptsBoundSave(t *testing.T) {
	// The host's SetCoop lists the worlds of this machine (world.Dir):
	// keep that read off the developer's own.
	cfg := t.TempDir()
	t.Setenv("APPDATA", cfg)
	t.Setenv("XDG_CONFIG_HOME", cfg)

	hostWorlds, joinWorlds := t.TempDir(), t.TempDir()
	host := newCoopRig(t, true, hostWorlds) // bound to bindtest.World, absent from hostWorlds
	join := newCoopRig(t, false, joinWorlds)
	ba, _ := bridgeSide(t, host, "host", 100)
	bb, fb := bridgeSide(t, join, "joiner", 200)

	for _, b := range []*gui.Bridge{ba, bb} {
		if err := b.SetStartDolphinMyself(true); err != nil {
			t.Fatal(err)
		}
	}
	port := freePort(t)
	if err := ba.Host(gui.HostRequest{Name: "Host", Port: port}); err != nil {
		t.Fatal(err)
	}
	waitView(t, ba, "host in its lobby", func(v gui.StateView) bool { return v.Phase == "lobby" && v.Lobby != nil && v.Lobby.Host })
	if err := bb.Join(gui.JoinRequest{Addr: fmt.Sprintf("127.0.0.1:%d", port), Name: "Joiner"}); err != nil {
		t.Fatal(err)
	}
	waitView(t, ba, "two members", func(v gui.StateView) bool { return v.Lobby != nil && len(v.Lobby.Members) == 2 })
	va := waitView(t, ba, "the host's lobby on co-op auto with no SetCoop", func(v gui.StateView) bool { return v.Lobby != nil && v.Lobby.Coop == "auto" })
	if va.Coop == nil || va.Coop.Setting != "auto" {
		t.Fatalf("host coop view %+v, want setting auto", va.Coop)
	}
	waitView(t, bb, "the joiner sees co-op auto", func(v gui.StateView) bool { return v.Lobby != nil && v.Lobby.Coop == "auto" })
	if err := ba.Start(true); err != nil {
		t.Fatal(err)
	}

	va = waitView(t, ba, "the host in game", func(v gui.StateView) bool { return v.Phase == "in game" })
	vb := waitView(t, bb, "the joiner in game with co-op on in the save's world", func(v gui.StateView) bool {
		return v.Phase == "in game" && v.Coop != nil && v.Coop.On && v.Coop.World == bindtest.World.String()
	})
	if vb.Lobby == nil || vb.Lobby.Phase != "in_game" {
		t.Fatalf("joiner lobby %+v, want in_game", vb.Lobby)
	}
	if _, err := os.Stat(filepath.Join(hostWorlds, bindtest.World.String(), AdoptedName)); err != nil {
		t.Fatalf("the host's world is not marked adopted: %v", err)
	}
	va = waitView(t, ba, "the host's adopted line", func(v gui.StateView) bool {
		return v.Banner != nil && strings.Contains(v.Banner.Text, "wasn't on this computer")
	})
	if va.Coop == nil || va.Coop.World != bindtest.World.String() {
		t.Fatalf("host coop view %+v, want world %s", va.Coop, bindtest.World)
	}
	if d := va.Dialog; d != nil && d.Kind == "founding" {
		t.Fatalf("the host has a founding dialog: %+v", d)
	}
	if got := join.starts(); len(got) != 1 {
		t.Fatalf("joiner co-op starts %v, want one", got)
	}
	waitFor(t, "joiner coop-sync", hasEvent(fb, "coop-sync 200 start"))
}
