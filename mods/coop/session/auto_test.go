package coopsession

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/bind/bindtest"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/engine"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/engine/enginetest"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// autoFile changes the host's loaded file before the founding looks.
type autoFile func(t *testing.T, m *mem.FakeMem)

func storeTag(t *testing.T, m *mem.FakeMem, layout uint8, w bind.WorldID, name string) {
	t.Helper()
	nm := make([]byte, bind.PlayerNameSize)
	copy(nm, name)
	tag := bind.Tag{Layout: layout, World: w, Nonce: bind.Nonce{0x1E, 0xA4}, NameHash: bind.NameHash(nm)}
	b := tag.Encode()
	if err := m.Store(addr.Reserve, b[:]); err != nil {
		t.Fatal(err)
	}
}

// TestDecideAuto: each row of docs/coop-just-works.md §4 for the host's
// loaded save, through the founding loop the app runs.
func TestDecideAuto(t *testing.T) {
	other := bind.WorldID{9, 9, 9, 9, 9, 9, 9, 9}
	for _, tc := range []struct {
		name     string
		bound    bool
		onDisk   bool // the world bindtest.World is among this PC's worlds
		edit     autoFile
		kind     autoKind
		reason   string
		wantID   bind.WorldID // autoOpen, autoAdopt
		outcome  string
		newWorld bool // autoFound: a fresh ID
	}{
		{name: "tied to a world on this PC", bound: true, onDisk: true, kind: autoOpen, wantID: bindtest.World, outcome: OutcomeContinued},
		{name: "tied to a world not on this PC (11-08)", bound: true, kind: autoAdopt, wantID: bindtest.World, outcome: OutcomeAdopted},
		{name: "untied first quest", kind: autoFound, newWorld: true, outcome: OutcomeFounded},
		{name: "a leaked tag", edit: func(t *testing.T, m *mem.FakeMem) { storeTag(t, m, bind.LayoutV1, other, "Aryll") }, kind: autoFound, newWorld: true, outcome: OutcomeFounded},
		{name: "second quest, untied", edit: func(t *testing.T, m *mem.FakeMem) { m.Store(addr.ClearCount, []byte{1}) }, kind: autoNone, reason: "second quest"},
		{name: "second quest, tied", bound: true, edit: func(t *testing.T, m *mem.FakeMem) { m.Store(addr.ClearCount, []byte{1}) }, kind: autoNone, reason: "second quest"},
		{name: "a tag of a newer layout", edit: func(t *testing.T, m *mem.FakeMem) { storeTag(t, m, 9, other, "Link") }, kind: autoNone, reason: "can't use"},
		{name: "junk in the tag area", edit: func(t *testing.T, m *mem.FakeMem) { m.Store(addr.Reserve, []byte{0xAA, 0xBB, 0xCC, 0xDD, 1, 2, 3, 4}) }, kind: autoNone, reason: "isn't a co-op tag"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			worlds := t.TempDir()
			if tc.onDisk {
				coWorld(t, worlds, world.ID(bindtest.World), time.Now(), nil)
			}
			_, m, tick := bindtest.Game(t, tc.bound)
			if tc.edit != nil {
				tc.edit(t, m)
			}
			out := &foundingOut{Out: &enginetest.Out{}}
			e := engine.New(engine.Config{Live: &engine.LiveState{}})
			deps := &appCoopDeps{
				openMem: func(uint32) (mem.Mem, func(), error) { return m, func() {}, nil },
				tick:    tick, wait: time.Millisecond, giveUp: time.Hour,
			}
			ac := newAppCoop(context.Background(), deps, e.LobbyEnv(out, "Host", nil, nil))
			ac.game = playingOn(7)
			ac.worlds = worlds
			done := make(chan struct{})
			var (
				d   autoDecision
				err error
			)
			go func() { d, err = ac.decide(context.Background(), world.DefaultPolicy()); close(done) }()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("the decision never came")
			}
			if err != nil {
				t.Fatal(err)
			}
			if d.kind != tc.kind {
				t.Fatalf("kind %v, want %v (%+v)", d.kind, tc.kind, d)
			}
			switch d.kind {
			case autoNone:
				if !strings.Contains(d.reason, tc.reason) {
					t.Fatalf("reason %q, want %q", d.reason, tc.reason)
				}
			case autoOpen:
				if d.id != world.ID(tc.wantID) {
					t.Fatalf("opens %s, want %s", d.id, tc.wantID)
				}
			case autoAdopt:
				if d.w.ID() != world.ID(tc.wantID) {
					t.Fatalf("adopts %s, want %s", d.w.ID(), tc.wantID)
				}
			case autoFound:
				if d.w.ID() == world.ID(bindtest.World) || d.w.ID() == world.ID(other) || d.w.ID().IsZero() {
					t.Fatalf("founded %s: want a new ID", d.w.ID())
				}
			}
			if d.kind != autoNone && d.outcome() != tc.outcome {
				t.Fatalf("outcome %q, want %q", d.outcome(), tc.outcome)
			}
			// Nothing is blocked or asked: the window saw no blocked founding.
			out.mu.Lock()
			defer out.mu.Unlock()
			for _, f := range out.fs {
				if f.Phase == session.FoundingBlocked {
					t.Fatalf("auto mode blocked: %+v", f)
				}
			}
		})
	}
}

// TestFoundingUnpatchedDolphin: a host whose Dolphin runs the game
// without this version's mod is told so, not "waiting for your Dolphin
// to run Wind Waker" for good, and the others read it too.
func TestFoundingUnpatchedDolphin(t *testing.T) {
	_, m, tick := bindtest.Game(t, false)
	out := &foundingOut{Out: &enginetest.Out{}}
	e := engine.New(engine.Config{Live: &engine.LiveState{}})
	deps := &appCoopDeps{
		openMem: func(uint32) (mem.Mem, func(), error) { return m, func() {}, nil },
		tick:    tick, wait: time.Millisecond, giveUp: time.Hour,
	}
	ac := newAppCoop(context.Background(), deps, e.LobbyEnv(out, "Host", nil, nil))
	ac.game = unpatchedGame{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { ac.decide(ctx, world.DefaultPolicy()); close(done) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	out.mu.Lock()
	defer out.mu.Unlock()
	if len(out.fs) == 0 || out.fs[0].Why != whyUnpatched {
		t.Fatalf("reports %+v, want the unpatched reason", out.fs)
	}
	if got := foundingStatus(out.fs[0], time.Minute); !strings.Contains(got, "without this version's mod") {
		t.Fatalf("status %q", got)
	}
	if strings.Contains(out.fs[0].Why, "waiting for your Dolphin to run Wind Waker") {
		t.Fatal("still the forever-waiting text")
	}
}

type unpatchedGame struct{}

func (unpatchedGame) PlayablePID() uint32                { return 0 }
func (unpatchedGame) SetCoop(engine.CoopSide)            {}
func (unpatchedGame) DolphinState() session.DolphinState { return session.DolphinUnpatched }

// runAutoLobby hosts and joins a lobby over the rigs, the host's co-op
// auto, edits the host's loaded file, and presses Start.
func runAutoLobby(t *testing.T, bound bool, worlds string, edit autoFile) (ca, cb *session.Controller, join *coopRig) {
	t.Helper()
	host := newCoopRig(t, bound, worlds)
	join = newCoopRig(t, false, worlds)
	if edit != nil {
		edit(t, host.m)
	}
	fa, fb := enginetest.NewGame(), enginetest.NewGame()
	fb.NextPID = 200
	da, db := gameDeps(fa), gameDeps(fb)
	da.CoopSync, db.CoopSync = coopSyncFake(fa), coopSyncFake(fb)
	ea := engine.New(engine.Config{Live: &engine.LiveState{}, Game: da, Coop: coopApp{env: testEnv, lobby: host.deps(t, "host")}})
	eb := engine.New(engine.Config{Live: &engine.LiveState{}, Game: db, Coop: coopApp{env: testEnv, lobby: join.deps(t, "joiner")}})
	ca, cb = session.New(ea, session.Options{}), session.New(eb, session.Options{})
	port := freePort(t)
	if _, err := ca.HostLobby(context.Background(), session.HostOptions{Name: "Host", Port: port, ListenHost: "127.0.0.1", Launch: optsA}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ca.Leave)
	waitSnap(t, ca, "host in its lobby", func(st session.State) bool { return st.Phase == session.InLobby && st.Lobby.Host })
	optsB := optsA
	optsB.UserDir = userB
	if _, err := cb.JoinLobby(context.Background(), session.JoinOptions{Name: "Joiner", Addr: fmt.Sprintf("127.0.0.1:%d", port), Launch: optsB}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cb.Leave)
	waitSnap(t, ca, "two members", func(st session.State) bool { return len(st.Lobby.Members) == 2 })
	if err := ca.SetCoopSettings(session.LobbySettings{Coop: session.CoopWorldAuto}); err != nil {
		t.Fatal(err)
	}
	waitSnap(t, cb, "the joiner sees co-op auto", func(st session.State) bool { return st.Lobby.Settings.Coop == "auto" })
	if err := ca.StartAnyway(); err != nil {
		t.Fatal(err)
	}
	return ca, cb, join
}

// TestLobbyCoopAutoAdopts is the 11-08 case: the host's save is tied to
// a world that is not on this PC, co-op is auto. With no click (no
// dialog, no question, no tag removal) the world is founded here under
// the same ID and marked adopted, GO carries it, and the joiner starts
// co-op in it.
func TestLobbyCoopAutoAdopts(t *testing.T) {
	worlds := t.TempDir()
	ca, cb, join := runAutoLobby(t, true, worlds, nil)
	st := waitSnap(t, ca, "adopted and in game", func(st session.State) bool {
		return st.Founding != nil && st.Founding.Phase == session.FoundingFounded && st.Phase == session.InGame
	})
	if st.Founding.Outcome != OutcomeAdopted || st.Founding.World != bindtest.World.String() {
		t.Fatalf("founding %+v, want adopted %s", st.Founding, bindtest.World)
	}
	if len(st.Questions) != 0 || len(st.Advice) != 0 {
		t.Fatalf("the host was asked something: %+v %+v", st.Questions, st.Advice)
	}
	sb := waitSnap(t, cb, "the joiner's co-op on", func(st session.State) bool { return st.Phase == session.InGame && st.Coop.On })
	if sb.Coop.World != bindtest.World.String() {
		t.Fatalf("joiner world %q", sb.Coop.World)
	}
	if got := join.starts(); len(got) != 1 {
		t.Fatalf("joiner co-op starts %v", got)
	}
	ws, err := ListWorlds(worlds)
	if err != nil || len(ws) != 1 || ws[0].id.String() != bindtest.World.String() {
		t.Fatalf("worlds %+v, %v", ws, err)
	}
	if _, err := os.Stat(filepath.Join(worlds, bindtest.World.String(), AdoptedName)); err != nil {
		t.Fatalf("the world is not marked adopted: %v", err)
	}
}

// TestLobbyCoopAutoContinues: a save tied to a world on this PC opens it
// whatever else is there, with no adopted marker.
func TestLobbyCoopAutoContinues(t *testing.T) {
	worlds := t.TempDir()
	coWorld(t, worlds, world.ID(bindtest.World), time.Now(), nil)
	ca, _, _ := runAutoLobby(t, true, worlds, nil)
	st := waitSnap(t, ca, "continued and in game", func(st session.State) bool {
		return st.Founding != nil && st.Founding.Phase == session.FoundingFounded && st.Phase == session.InGame
	})
	if st.Founding.Outcome != OutcomeContinued || st.Founding.World != bindtest.World.String() {
		t.Fatalf("founding %+v", st.Founding)
	}
	if _, err := os.Stat(filepath.Join(worlds, bindtest.World.String(), AdoptedName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a world that was already here is marked adopted: %v", err)
	}
}

// TestLobbyCoopAutoFoundsNew: an untied first-quest save founds a new
// world, and the host's own bind is confirmed with no question.
func TestLobbyCoopAutoFoundsNew(t *testing.T) {
	worlds := t.TempDir()
	ca, _, _ := runAutoLobby(t, false, worlds, nil)
	st := waitSnap(t, ca, "founded and in game", func(st session.State) bool {
		return st.Founding != nil && st.Founding.Phase == session.FoundingFounded && st.Phase == session.InGame
	})
	if st.Founding.Outcome != OutcomeFounded || len(st.Founding.World) != 16 {
		t.Fatalf("founding %+v", st.Founding)
	}
	time.Sleep(200 * time.Millisecond) // the host's replica binds its own file
	if st := ca.Snapshot(); len(st.Questions) != 0 {
		t.Fatalf("the host was asked: %+v", st.Questions)
	}
}

// TestLobbyCoopAutoNoCoop: a second quest takes no part: every window
// says why in one line, GO goes out without a world, pose sync runs.
func TestLobbyCoopAutoNoCoop(t *testing.T) {
	worlds := t.TempDir()
	ca, cb, join := runAutoLobby(t, false, worlds, func(t *testing.T, m *mem.FakeMem) {
		if err := m.Store(addr.ClearCount, []byte{1}); err != nil {
			t.Fatal(err)
		}
	})
	st := waitSnap(t, ca, "co-op off, host in game", func(st session.State) bool {
		return st.Founding != nil && st.Founding.Phase == session.FoundingFailed && st.Phase == session.InGame
	})
	if !strings.Contains(st.Founding.Err, "second quest") || st.Coop.On {
		t.Fatalf("founding %+v coop %+v", st.Founding, st.Coop)
	}
	sb := waitSnap(t, cb, "the joiner reads why", func(st session.State) bool {
		return st.Phase == session.InGame && strings.HasPrefix(hostRow(st).CoopStatus, "off this session:")
	})
	if got := hostRow(sb).CoopStatus; !strings.Contains(got, "second quest") {
		t.Fatalf("status %q", got)
	}
	time.Sleep(50 * time.Millisecond)
	if got := join.starts(); len(got) != 0 {
		t.Fatalf("the joiner started co-op: %v", got)
	}
	if ws, _ := ListWorlds(worlds); len(ws) != 0 {
		t.Fatalf("worlds %+v", ws)
	}
}
