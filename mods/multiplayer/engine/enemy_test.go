package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/enemysync"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/feed"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// TestEnemyPipelineBehindEnv: no enemy pipeline without WW_ENEMY_SYNC=1;
// with it, the host's is 'enemy-host' and a joiner's 'enemy-client'.
func TestEnemyPipelineBehindEnv(t *testing.T) {
	t.Setenv("WW_ENEMY_SYNC", "")
	if _, ok := enemyPipeline(true, feed.Env{}, "Link", "127.0.0.1:1", 0, log.Discard{}, nil, nil); ok {
		t.Fatal("an enemy pipeline without WW_ENEMY_SYNC")
	}
	t.Setenv("WW_ENEMY_SYNC", "1")
	for _, c := range []struct {
		host bool
		want string
	}{{true, "enemy-host"}, {false, "enemy-client"}} {
		p, ok := enemyPipeline(c.host, feed.Env{}, "Link", "127.0.0.1:1", 0, log.Discard{}, nil, nil)
		if !ok || p.Name != c.want || p.Run == nil {
			t.Errorf("host %v: %q ok %v", c.host, p.Name, ok)
		}
	}
}

// TestGameRunnerEnemySync: with the EnemySync seam set, the runner runs
// it beside the pose pipelines on the same Dolphin with the host flag
// SetHost gave it, and stops it with them.
func TestGameRunnerEnemySync(t *testing.T) {
	f := newFakeGame()
	f.Running = []session.RunningDolphin{{PID: 7, UserDir: userB, State: session.DolphinTitle}}
	g, out, _, _ := newTestRunner(f, optsA)
	var mu sync.Mutex
	var runs []string
	g.deps.EnemySync = func(ctx context.Context, cancel context.CancelFunc, name, addr string, pid uint32, host bool, rep log.Reporter) error {
		mu.Lock()
		runs = append(runs, strings.Join([]string{name, addr, map[bool]string{true: "host", false: "client"}[host]}, " "))
		mu.Unlock()
		f.Event("enemy %d start", pid)
		defer f.Event("enemy %d stop", pid)
		<-ctx.Done()
		return nil
	}
	g.SetHost(true)
	g.Start(context.Background())
	waitFor(t, "the pipelines", hasEvent(f, "pipelines 100 start"))
	waitFor(t, "enemy sync", hasEvent(f, "enemy 100 start"))
	f.Script(100, inGame)
	waitFor(t, "in game", func() bool { return out.Last().State == session.DolphinInGame })
	if err := g.Stop(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(runs) != 1 || runs[0] != "Link 127.0.0.1:1 host" {
		t.Fatalf("enemy sync runs %q", runs)
	}
	events := strings.Join(f.Events(), ", ")
	for _, want := range []string{"enemy 100 start", "enemy 100 stop", "pipelines 100 stop", "reset 100"} {
		if !strings.Contains(events, want) {
			t.Errorf("events lack %q: %s", want, events)
		}
	}
}

// TestEnemyClaimsPerGame: each joining game gets its own enemy claim set
// (enemy-mirror-general.json D8), which its enemy client fills and its
// co-op K1 reads; the host's game and a session without enemy sync get
// none. A GameRunner's two seams on one Dolphin meet in the same set
// (gameClaims), and an enemy client that stops empties it.
func TestEnemyClaimsPerGame(t *testing.T) {
	t.Setenv("WW_ENEMY_SYNC", "")
	if enemyClaims(false) != nil || claimFunc(nil) != nil {
		t.Fatal("a claim set without WW_ENEMY_SYNC")
	}
	t.Setenv("WW_ENEMY_SYNC", "1")
	if enemyClaims(true) != nil {
		t.Error("the host's game has a claim set")
	}
	a, b := enemyClaims(false), enemyClaims(false)
	if a == nil || b == nil || a == b {
		t.Fatalf("joiners' sets %p %p", a, b)
	}
	a.Replace([]uint32{5})
	if !claimFunc(a)(5) || claimFunc(b)(5) {
		t.Error("one game's claim reached the other's")
	}

	var g gameClaims
	if g.of(1, true) != nil {
		t.Error("the host's game has a claim set")
	}
	g.of(1, false).Replace([]uint32{9})
	g.of(2, false).Replace([]uint32{10})
	if !g.claimed(1)(9) || g.claimed(1)(10) || !g.claimed(2)(10) || g.claimed(2)(9) || g.claimed(3)(9) {
		t.Error("the games' claim sets are not kept apart")
	}

	// The client's pipeline empties its set when it stops (here: no
	// Dolphin to open).
	cs := enemysync.NewClaimSet()
	cs.Replace([]uint32{7})
	fe := feed.Env{Open: func(uint32) (*emu.Dolphin, error) { return nil, errors.New("no Dolphin") }}
	p, ok := enemyPipeline(false, fe, "Link", "127.0.0.1:1", 0, log.Discard{}, nil, cs)
	if !ok {
		t.Fatal("no enemy-client pipeline")
	}
	if err := p.Run(context.Background()); err == nil {
		t.Fatal("the client ran without a Dolphin")
	}
	if cs.Claimed(7) {
		t.Error("a stopped client's claims stay")
	}
}
