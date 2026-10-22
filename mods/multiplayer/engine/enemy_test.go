package engine

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/enemysync"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/feed"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
)

// TestEnemyPipelineDefaultOn: enemy sync is on by default (unset, blank
// or 1): the host's pipeline is 'enemy-host' and a joiner's
// 'enemy-client'; WW_ENEMY_SYNC=0 runs none.
func TestEnemyPipelineDefaultOn(t *testing.T) {
	t.Setenv("WW_ENEMY_SYNC", "0")
	if _, ok := enemyPipeline(true, feed.Env{}, "Link", "127.0.0.1:1", 0, log.Discard{}, nil, nil); ok {
		t.Fatal("an enemy pipeline with WW_ENEMY_SYNC=0")
	}
	for _, v := range []string{"unset", "", "1"} {
		if v == "unset" {
			unsetEnv(t, "WW_ENEMY_SYNC")
		} else {
			t.Setenv("WW_ENEMY_SYNC", v)
		}
		for _, c := range []struct {
			host bool
			want string
		}{{true, "enemy-host"}, {false, "enemy-client"}} {
			p, ok := enemyPipeline(c.host, feed.Env{}, "Link", "127.0.0.1:1", 0, log.Discard{}, nil, nil)
			if !ok || p.Name != c.want || p.Run == nil {
				t.Errorf("WW_ENEMY_SYNC %q, host %v: %q ok %v", v, c.host, p.Name, ok)
			}
		}
	}
}

// unsetEnv unsets key for the rest of the test (t.Setenv restores it).
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	os.Unsetenv(key)
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
	g.deps.EnemySync = func(ctx context.Context, name, addr string, pid uint32, host bool, rep log.Reporter) error {
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
	t.Setenv("WW_ENEMY_SYNC", "0")
	if enemyClaims(false) != nil || claimFunc(nil) != nil {
		t.Fatal("a claim set with WW_ENEMY_SYNC=0")
	}
	var off gameClaims
	if off.of(1, false) != nil {
		t.Fatal("a game's claim set with WW_ENEMY_SYNC=0")
	}
	unsetEnv(t, "WW_ENEMY_SYNC") // the default: on
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

// TestGameRunnerEnemyFatalKeepsPose: an enemy-sync pipeline refused by
// the server (a RejectError, ErrFatal) turns enemy sync off and nothing
// else: it is not run again, the pose pipelines keep running, fail is
// never called and Stop returns nil (release-readiness A2).
func TestGameRunnerEnemyFatalKeepsPose(t *testing.T) {
	f := newFakeGame()
	g, out, _, fails := newTestRunner(f, optsA)
	var mu sync.Mutex
	runs := 0
	g.deps.EnemySync = func(ctx context.Context, name, addr string, pid uint32, host bool, rep log.Reporter) error {
		mu.Lock()
		runs++
		mu.Unlock()
		return &network.RejectError{Code: network.RejectRole, Reason: "another player is already the enemy host"}
	}
	g.SetHost(true)
	g.Start(context.Background())
	waitFor(t, "the pipelines", hasEvent(f, "pipelines 100 start"))
	waitFor(t, "enemy sync off", func() bool { return out.Logged("enemy sync off for this session") })
	time.Sleep(50 * time.Millisecond) // many ticks: the pose pipelines would have stopped by now
	if hasEvent(f, "pipelines 100 stop")() {
		t.Fatalf("the pose pipelines stopped: %s", strings.Join(f.Events(), ", "))
	}
	if d := out.Last(); !d.Pipelines {
		t.Fatalf("Dolphin %+v", d)
	}
	if err := g.Stop(); err != nil {
		t.Fatalf("stop = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if runs != 1 || len(*fails) != 0 {
		t.Fatalf("enemy runs %d, fail called %d times", runs, len(*fails))
	}
}
