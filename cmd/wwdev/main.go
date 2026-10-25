// Command wwdev is Wind Waker Multiplayer's developer tools: the
// two-Dolphin harness (dolphin2, mp-local), the co-op diagnostics,
// auto-recapture, the tunic tools and the research probes, moved out of
// ww-multiplayer in Phase D6 so the shipped binary carries only what
// players use. CI builds and vets it; releases do not ship it.
//
//	go build -o wwdev.exe ./cmd/wwdev
package main

import (
	"fmt"
	"os"

	"github.com/StephenSHorton/ww-multiplayer/internal/cliutil"
	coopsession "github.com/StephenSHorton/ww-multiplayer/mods/coop/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/enemysync"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/engine"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/feed"
)

// exeCmd is wwdev as the user types it on this platform (cliutil.ExeCmd).
var exeCmd = cliutil.ExeCmd("wwdev")

// feedEnv is what the pose pipelines (mods/multiplayer/feed) take from
// wwdev, as from ww-multiplayer: cliutil.OpenDolphin, and player
// connections with the session's color preference.
var feedEnv = feed.Env{Open: cliutil.OpenDolphin, NewClient: engine.NewPlayerClientCtx}

// envPoseTestRate is the pose test rate knob (main, smooth-steps).
const envPoseTestRate = "WW_POSE_TEST_RATE"

// coopEnv is what co-op (mods/coop/session) takes from wwdev (mp-local
// --coop): cliutil.OpenDolphin.
var coopEnv = coopsession.Env{Open: cliutil.OpenDolphin}

// enemyEnv is what enemy sync (mods/enemysync) takes from wwdev (mp-local
// unless WW_ENEMY_SYNC=0, enemy-mirror-local): cliutil.OpenDolphin.
var enemyEnv = enemysync.Env{Open: cliutil.OpenDolphin}

// findDolphin and openDolphin are cliutil's, under the names the moved
// commands call them by.
var (
	findDolphin = cliutil.FindDolphin
	openDolphin = cliutil.OpenDolphin
)

// exitUsage reports a command-line mistake with the command's usage line
// and exits non-zero (cliutil.ExitUsage, with wwdev's name).
func exitUsage(err error, usage string) {
	cliutil.ExitUsage(exeCmd, err, usage)
}

func main() {
	cliutil.ApplyColorPrefFromArgs() // --color / WW_COLOR, as ww-multiplayer reads them
	// WW_POSE_TEST_RATE=<hz>[:<jitter ms>[:<latency ms>]]: the broadcasts
	// this process runs (mp-local's, broadcast-pose's) send late on
	// purpose, for smooth-steps (feed.TestRate). wwdev only: the shipped
	// binary has no such knob.
	r, err := feed.ParseTestRate(os.Getenv(envPoseTestRate))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", envPoseTestRate, err)
		os.Exit(2)
	}
	feedEnv.TestRate = r
	if len(os.Args) < 2 {
		printHelp()
		return
	}
	runCommand(os.Args[1], os.Args[2:])
}
