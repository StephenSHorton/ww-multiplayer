// Package coopcli is co-op's CLI commands (docs/api/00-design.md §7):
// coop-tables (the M1 stage tables of the user's disc), coop-tag (the
// binding tag), coop-world (worlds and card backups) and the offline
// save tools save-diff and save-merge. They are developer tools, out of
// the help like the co-op diagnostics; the co-op mod (mods/coop) hands
// them to the app through the wwapi CLI capability.
//
// # v0 UNSTABLE
//
// Like wwapi v0, this package's Go API may change in any 0.x minor
// version of wwapi.
package coopcli

import (
	"fmt"
	"os"
	"runtime"

	coopsession "github.com/StephenSHorton/ww-multiplayer/mods/coop/session"
	"github.com/StephenSHorton/ww-multiplayer/wwapi"
)

// Commands are co-op's CLI commands, in the order the app's help-less
// developer group lists them: coop-tables, coop-tag, coop-world,
// save-diff, save-merge. coop-tag reads the Dolphin env.Open picks.
func Commands(env coopsession.Env) []wwapi.Command {
	dev := func(name string, run func(args []string)) wwapi.Command {
		return wwapi.Command{Name: name, Group: wwapi.GroupDev, Hidden: true,
			Run: func(_ string, args []string) { run(args) }}
	}
	return []wwapi.Command{
		// Co-op M1 tables from the user's own disc (coop_tables.go).
		dev("coop-tables", runCoopTables),
		// Co-op M2 binding tag (coop_m2_cmds.go): show, clear.
		dev("coop-tag", func(args []string) { runCoopTag(env, args) }),
		// Co-op M2 worlds and card backups (coop_world_cmds.go): list,
		// restore, block.
		dev("coop-world", runCoopWorld),
		// Co-op M4 offline save tools (coop_save_cmds.go).
		dev("save-diff", runSaveDiff),
		dev("save-merge", runSaveMerge),
	}
}

// exeCmd is the binary as the user types it on this platform, as package
// main's exeCmd (and the TUI's) spell it: ".\ww-multiplayer.exe" on
// Windows, "./ww-multiplayer" elsewhere.
var exeCmd = func() string {
	if runtime.GOOS == "windows" {
		return `.\ww-multiplayer.exe`
	}
	return "./ww-multiplayer"
}()

// exitUsage prints err and the command's usage and exits 1, as package
// main's exitUsage does.
func exitUsage(err error, usage string) {
	fmt.Printf("ERROR: %v\n", err)
	fmt.Printf("Usage: %s %s\n", exeCmd, usage)
	os.Exit(1)
}
