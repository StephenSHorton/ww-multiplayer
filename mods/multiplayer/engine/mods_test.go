package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/feed"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// engineMods are the Config.Mods of a build whose client_required mods
// are list, as package main makes them (wwapi.ModsIDOf).
func engineMods(list ...string) network.Mods {
	id := wwapi.ModsIDOf(list)
	return network.Mods{Hash: id.Hash, List: id.List, Known: true}
}

var (
	modsCoopMP = engineMods("coop@1.0.0", "multiplayer@1.0.0")
	modsMPOnly = engineMods("multiplayer@1.0.0")
)

// modsDifferText is what a player with modsMPOnly is told by a host with
// modsCoopMP: the host's list against theirs.
const modsDifferText = "the mods differ: the host runs coop@1.0.0, multiplayer@1.0.0; you run multiplayer@1.0.0. Both players need the same mods turned on."

func sameMods(a, b network.Mods) bool {
	return a.Known == b.Known && a.Hash == b.Hash && slices.Equal(a.List, b.List)
}

// TestWithMods: a session's ctx carries the engine's mods to the player
// connections it opens; the zero Mods leaves ctx as it is.
func TestWithMods(t *testing.T) {
	ctx := context.Background()
	if got := WithMods(ctx, network.Mods{}); got != ctx {
		t.Error("WithMods with the zero Mods made a new ctx")
	}
	if m := ModsFor(ctx); m.Known {
		t.Errorf("ModsFor(bare ctx) = %+v, want the zero Mods", m)
	}
	if m := ModsFor(WithMods(ctx, modsCoopMP)); !sameMods(m, modsCoopMP) {
		t.Errorf("ModsFor = %+v, want %+v", m, modsCoopMP)
	}
}

// TestEngineHostMods: a direct host session's server checks joiners
// against the engine's mods (Engine.Host's WithMods, RunHostSession's
// SetMods). The co-op start fails, so nothing listens.
func TestEngineHostMods(t *testing.T) {
	for _, m := range []network.Mods{modsCoopMP, {}} {
		coop := &fakeCoop{startErr: errors.New("stop here")}
		e := New(Config{Coop: modsCoopApp{coop}, Mods: m})
		ctx, cancel := context.WithCancel(context.Background())
		err := e.Host(ctx, cancel, session.HostOptions{Name: "Host"}, &recOutput{})
		cancel()
		if err == nil || err.Error() != "stop here" {
			t.Fatalf("Host = %v, want the co-op start's error", err)
		}
		if coop.srv == nil {
			t.Fatal("co-op never saw the server")
		}
		if got := coop.srv.Mods(); !sameMods(got, m) {
			t.Errorf("server mods %+v, want the engine's %+v", got, m)
		}
	}
}

// modsCoopApp is a CoopApp whose host and join sessions run coop.
type modsCoopApp struct{ coop Coop }

func (a modsCoopApp) Session(session.CoopOptions, Front) Coop { return a.coop }
func (modsCoopApp) Lobby(context.Context, LobbyEnv) LobbyCoop { return nil }
func (modsCoopApp) Worlds() ([]session.CoopWorld, error)      { return nil, nil }
func (modsCoopApp) Sync(context.Context, string, string, uint32, log.Reporter, CoopSide, func(uint32) bool) error {
	return nil
}

// TestEngineJoinMods: a direct join sends the engine's mods in its player
// hellos (Engine.Join's WithMods, NewPlayerClientCtx's SetMods). A host
// with other mods refuses it with RejectMods and both lists; one with the
// same mods admits it, and so does one with no hash (an older build).
func TestEngineJoinMods(t *testing.T) {
	t.Setenv("WW_TUNIC", "off") // the fake Dolphin has no RAM to swap tunics in
	srv := network.NewServer(0) // loopback only
	srv.SetMods(modsCoopMP)
	var mu sync.Mutex
	var srvLog []string
	srv.OnLog = func(msg string) {
		mu.Lock()
		srvLog = append(srvLog, msg)
		mu.Unlock()
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	addr := srv.Addr().String()
	connected := func(name string) func() bool {
		return func() bool {
			mu.Lock()
			defer mu.Unlock()
			for _, l := range srvLog {
				if strings.HasPrefix(l, name+" (ID:") && strings.HasSuffix(l, " connected") {
					return true
				}
			}
			return false
		}
	}
	// The pipelines get a Dolphin at once (one with no process: its reads
	// fail), so they connect.
	env := Env{Feed: feed.Env{
		Open:      func(uint32) (*emu.Dolphin, error) { return &emu.Dolphin{}, nil },
		NewClient: NewPlayerClientCtx,
	}}

	differ := session.New(New(Config{Env: env, Mods: modsMPOnly}), session.Options{})
	if _, err := differ.Join(context.Background(), session.JoinOptions{Name: "Differ", Addr: addr}); err != nil {
		t.Fatal(err)
	}
	st := waitSnap(t, differ, "refused", func(st session.State) bool { return st.Phase == session.Ended })
	var rej *network.RejectError
	if !st.Failed || !errors.As(st.Err, &rej) || rej.Code != network.RejectMods || rej.Reason != "host refused the connection: "+modsDifferText {
		t.Fatalf("mismatched join ended %+v (err %v), want RejectMods %q", st, st.Err, modsDifferText)
	}

	for _, c := range []struct {
		name string
		mods network.Mods
	}{{"Same", modsCoopMP}, {"Old", network.Mods{}}} {
		ctrl := session.New(New(Config{Env: env, Mods: c.mods}), session.Options{})
		if _, err := ctrl.Join(context.Background(), session.JoinOptions{Name: c.name, Addr: addr}); err != nil {
			t.Fatal(err)
		}
		if !waitForCond(connected(c.name)) {
			ctrl.Leave()
			t.Fatalf("%s never joined; server log %q", c.name, srvLog)
		}
		ctrl.Leave()
		if st := ctrl.Snapshot(); st.Failed {
			t.Errorf("%s ended failed: %v", c.name, st.Err)
		}
	}
}

// TestEngineLobbyMods: a lobby host's server checks the engine's mods
// (HostLobby's SetMods) against each HELLO's (runLobby's SetMods). A
// joiner with other mods ends on the fatal mods_differ notice with both
// lists; one with the same mods joins, and its game side's pipelines run
// with the mods (the game ctx's WithMods).
func TestEngineLobbyMods(t *testing.T) {
	fa, fb := newFakeGame(), newFakeGame()
	fb.NextPID = 200
	var mu sync.Mutex
	seen := map[string]network.Mods{}
	deps := func(f *fakeGame) *GameDeps {
		d := fakeDeps(f)
		d.Pipelines = func(ctx context.Context, cancel context.CancelFunc, name, addr string, pid uint32, rep log.Reporter) error {
			mu.Lock()
			seen[name] = ModsFor(ctx)
			mu.Unlock()
			return f.Pipelines(ctx, cancel, name, addr, pid, rep)
		}
		return d
	}
	ca := session.New(New(Config{Live: &LiveState{}, Game: deps(fa), Mods: modsCoopMP}), session.Options{})
	cm := session.New(New(Config{Live: &LiveState{}, Mods: modsMPOnly}), session.Options{})
	cb := session.New(New(Config{Live: &LiveState{}, Game: deps(fb), Mods: modsCoopMP}), session.Options{})

	port := freePort(t)
	if _, err := ca.HostLobby(context.Background(), session.HostOptions{Name: "Host", Port: port, ListenHost: "127.0.0.1", Launch: optsA}); err != nil {
		t.Fatal(err)
	}
	defer ca.Leave()
	waitSnap(t, ca, "host in its lobby", func(st session.State) bool { return st.Phase == session.InLobby && st.Lobby.Host })
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	if _, err := cm.JoinLobby(context.Background(), session.JoinOptions{Name: "Differ", Addr: addr}); err != nil {
		t.Fatal(err)
	}
	st := waitSnap(t, cm, "refused", func(st session.State) bool { return st.Phase == session.Ended })
	var ne *network.LobbyNoticeError
	if !st.Failed || !errors.As(st.Err, &ne) || ne.Notice.Code != network.NoticeModsDiffer || ne.Notice.Text != modsDifferText {
		t.Fatalf("mismatched lobby join ended %+v (err %v), want mods_differ %q", st, st.Err, modsDifferText)
	}
	if st := ca.Snapshot(); len(st.Lobby.Members) != 1 {
		t.Fatalf("the refused joiner is in the host's lobby: %+v", st.Lobby.Members)
	}

	optsB := optsA
	optsB.UserDir = userB
	if _, err := cb.JoinLobby(context.Background(), session.JoinOptions{Name: "Joiner", Addr: addr, Launch: optsB}); err != nil {
		t.Fatal(err)
	}
	defer cb.Leave()
	waitSnap(t, ca, "two members", func(st session.State) bool { return len(st.Lobby.Members) == 2 })
	if err := cb.SetReady(true); err != nil {
		t.Fatal(err)
	}
	waitSnap(t, ca, "joiner ready", func(st session.State) bool { return len(st.Lobby.NotReady()) == 0 })
	if err := ca.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "host pipelines", hasEvent(fa, "pipelines 100 start"))
	waitFor(t, "joiner pipelines", hasEvent(fb, "pipelines 200 start"))
	mu.Lock()
	defer mu.Unlock()
	for _, name := range []string{"Host", "Joiner"} {
		if m := seen[name]; !sameMods(m, modsCoopMP) {
			t.Errorf("%s's pipelines ran with mods %+v, want %+v", name, m, modsCoopMP)
		}
	}
}
