package main

// The CLI's command registry. main dispatches every subcommand through
// commandTable, and printHelp is generated from it. It replaced main's
// switch on os.Args[1] (docs/api/00-design.md, Phase B1) without changing
// what any subcommand prints or exits with; testdata/cli-subcommands.txt
// pins the names (TestCLISubcommandsUnchanged).
//
// No arguments, `tui` and `ui` are not commands: appFace (app_face.go)
// takes them before main reaches the registry.

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/gui"
	coopsession "github.com/StephenSHorton/ww-multiplayer/mods/coop/session"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/engine"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/feed"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
)

// cmdGroup is the printHelp section a command is listed under.
type cmdGroup int

const (
	groupPlay  cmdGroup = iota // Play multiplayer
	groupPatch                 // Patch an ISO
	groupLocal                 // Local two-Dolphin testing
	groupLower                 // Lower-level CLIs
	groupDev                   // diagnostics and developer tools (hidden)
)

// helpSections are printHelp's sections, in order. A long section prints
// each command's usage and summary on two lines; faces adds the `tui` and
// `ui` lines after the section's commands.
var helpSections = []struct {
	group cmdGroup
	title string
	long  bool
	faces bool
}{
	{groupPlay, "Play multiplayer:", false, true},
	{groupPatch, "Patch an ISO:", true, false},
	{groupLocal, "Local two-Dolphin testing (everything in-process):", false, false},
	{groupLower, "Lower-level CLIs:", false, false},
}

// command is one CLI subcommand.
type command struct {
	name    string
	aliases []string // other names that run the same command
	group   cmdGroup
	hidden  bool   // not listed by printHelp
	usage   string // printHelp's usage, after the binary (listed commands only)
	summary string // printHelp's description (listed commands only)
	// run runs the command with its name (os.Args[1]) and the arguments
	// after it (os.Args[2:]). The bodies moved here from main's switch
	// still read os.Args themselves, as they did there.
	run func(name string, args []string)
}

// commandTable returns the registry: the app's own commands, then the
// loaded mods' (modCommands; co-op's coop-tables, coop-tag, coop-world,
// save-diff and save-merge, where they sat before they moved into
// mods/coop). The listed commands come first, in printHelp's order. It is
// a function, not a var, because help's run calls printHelp, which reads
// the table.
func commandTable() []command {
	return append([]command{
		{
			name:    "host",
			group:   groupPlay,
			usage:   "host [name] [--port N]",
			summary: "Host a session (port 25565 unless --port; one process per player)",
			run:     cmdHost,
		},
		{
			name:    "join",
			group:   groupPlay,
			usage:   "join <host-ip[:port]> [name]",
			summary: "Join a host's session (type the address `host` prints)",
			run:     cmdJoin,
		},
		{
			name:    "patch",
			group:   groupPatch,
			usage:   "patch <vanilla.iso|.ciso> [out.iso]",
			summary: "Splice the multiplayer mod into your own legitimate copy of Wind Waker (GZLE01)",
			run:     cmdPatch,
		},
		{
			name:    "dolphin2",
			group:   groupLocal,
			usage:   "dolphin2 [--reset]",
			summary: "Bootstrap & launch a 2nd Dolphin instance",
			run:     cmdDolphin2,
		},
		{
			name:    "mp-local",
			group:   groupLocal,
			usage:   "mp-local [nameA] [nameB]",
			summary: "Run server + broadcast/puppet pairs for both Dolphins",
			run:     cmdMpLocal,
		},
		{
			name:    "server",
			group:   groupLower,
			usage:   "server [--port N]",
			summary: "Start headless server (port 25565 unless --port)",
			run:     cmdServer,
		},
		{
			name:    "broadcast-pose",
			group:   groupLower,
			usage:   "broadcast-pose [name] [addr]",
			summary: "Stream local Link pose+pos to server",
			run:     cmdBroadcastPose,
		},
		{
			name:    "puppet-sync",
			group:   groupLower,
			usage:   "puppet-sync [name] [addr]",
			summary: "Receive remotes; render as Link #2 / actor puppets",
			run:     cmdPuppetSync,
		},
		{
			name:    "fake-client",
			group:   groupLower,
			usage:   "fake-client [name] [addr]",
			summary: "Connect a fake client that walks in circles",
			run:     cmdFakeClient,
		},
		{
			name:    "debug",
			group:   groupLower,
			usage:   "debug",
			summary: "Test Dolphin memory access",
			run:     func(string, []string) { runDebug() },
		},
		{
			name:    "warp",
			group:   groupLower,
			usage:   "warp <x> <y> <z>",
			summary: "Teleport Link to world coords (uses C warp handler)",
			run:     cmdWarp,
		},
		{
			// The compiled-in mods and their status (mods.go, wwapi).
			name:    "mods",
			group:   groupLower,
			usage:   modsUsage,
			summary: "List the mods built into this version",
			run:     func(_ string, args []string) { runMods(args) },
		},
		{
			name:    "version",
			aliases: []string{"--version"},
			group:   groupLower,
			usage:   "version",
			summary: "Print the version (both players should match)",
			run:     func(string, []string) { fmt.Println(versionLine()) },
		},
		{
			name:    "help",
			group:   groupLower,
			usage:   "help",
			summary: "Show this help",
			run:     func(string, []string) { printHelp() },
		},
		// Not in printHelp: diagnostics and developer tools, in the order of
		// the switch this table replaced.
		{name: "screenshot", group: groupDev, hidden: true, run: cmdScreenshot},
		{name: "auto-recapture", group: groupDev, hidden: true, run: cmdAutoRecapture},
		{name: "auto-recapture-pair", group: groupDev, hidden: true, run: cmdAutoRecapturePair},
		// Diagnostic: sends Shift+F1 via Win32 PostMessage to the
		// selected Dolphin window. Used to validate the hotkey
		// path without re-running the full auto-recapture flow.
		{name: "send-shift-f1", group: groupDev, hidden: true, run: func(string, []string) { runSendShiftF1() }},
		{name: "dump", group: groupDev, hidden: true, run: func(string, []string) { runDump() }},
		{name: "move-puppet", group: groupDev, hidden: true, run: cmdMovePuppet},
		{name: "input", group: groupDev, hidden: true, run: cmdInput},
		{name: "input-release", group: groupDev, hidden: true, run: func(string, []string) { runInputRelease() }},
		{name: "input-probe", group: groupDev, hidden: true, run: cmdInputProbe},
		{name: "poke-u32", group: groupDev, hidden: true, run: cmdPokeU32},
		{name: "shadow-mode", group: groupDev, hidden: true, run: cmdShadowMode},
		{name: "echo-delay", group: groupDev, hidden: true, run: cmdEchoDelay},
		{name: "pose-test", group: groupDev, hidden: true, run: cmdPoseTest},
		{name: "unhide-puppet", group: groupDev, hidden: true, run: func(string, []string) { runUnhidePuppet() }},
		{name: "broadcast-link", group: groupDev, hidden: true, run: cmdBroadcastLink},
		{name: "pose-fake-loop", group: groupDev, hidden: true, run: cmdPoseFakeLoop},
		{name: "tunic-probe", group: groupDev, hidden: true, run: func(string, []string) { runTunicProbe() }},
		{name: "tunic-recolor", group: groupDev, hidden: true, run: cmdTunicRecolor},
		{name: "tunic-slot", group: groupDev, hidden: true, run: cmdTunicSlot},
		{name: "disasm", group: groupDev, hidden: true, run: cmdDisasm},
		{name: "scan-pos", group: groupDev, hidden: true, run: func(string, []string) { runScanPos() }},
		{name: "poke-vec3", group: groupDev, hidden: true, run: cmdPokeVec3},
		{name: "peek", group: groupDev, hidden: true, run: cmdPeek},
		{name: "warp-force", group: groupDev, hidden: true, run: cmdWarpForce},
		{name: "warp-force-off", group: groupDev, hidden: true, run: func(string, []string) { runWarpForce("", "", "", false) }},
		{name: "eye-fix-probe", group: groupDev, hidden: true, run: func(string, []string) { runEyeFixProbe() }},
		{name: "eye-fix-chain", group: groupDev, hidden: true, run: func(string, []string) { runEyeFixChain() }},
		{name: "eye-fix-find-shape", group: groupDev, hidden: true, run: func(string, []string) { runEyeFixFindShape() }},
		{name: "eye-fix-gates", group: groupDev, hidden: true, run: func(string, []string) { runEyeFixGates() }},
		{name: "eye-fix-post-chain", group: groupDev, hidden: true, run: func(string, []string) { runEyeFixPostChain() }},
		{name: "eye-anim-find", group: groupDev, hidden: true, run: func(string, []string) { runEyeAnimFind() }},
		{name: "eye-anim-dl-dump", group: groupDev, hidden: true, run: cmdEyeAnimDlDump},
		{name: "eye-anim-watch", group: groupDev, hidden: true, run: cmdEyeAnimWatch},
		{name: "eye-anim-poke", group: groupDev, hidden: true, run: cmdEyeAnimPoke},
		{name: "face-sync-status", group: groupDev, hidden: true, run: func(string, []string) { runFaceSyncStatus() }},
		{name: "face-sync-watch", group: groupDev, hidden: true, run: cmdFaceSyncWatch},
		{name: "face-hook-arm", group: groupDev, hidden: true, run: func(string, []string) { runFaceHookArm(true) }},
		{name: "face-hook-disarm", group: groupDev, hidden: true, run: func(string, []string) { runFaceHookArm(false) }},
		{name: "face-tev-hammer", group: groupDev, hidden: true, run: cmdFaceTevHammer},
		{name: "face-mat-list", group: groupDev, hidden: true, run: func(string, []string) { runFaceMatList() }},
		{name: "face-mat-hammer", group: groupDev, hidden: true, run: cmdFaceMatHammer},
		{name: "face-sync-tev-poll", group: groupDev, hidden: true, run: cmdFaceSyncTevPoll},
		{name: "face-dl-loop", group: groupDev, hidden: true, run: cmdFaceDlLoop},
		{name: "face-sync-fake", group: groupDev, hidden: true, run: cmdFaceSyncFake},
		{name: "face-sync-fake-loop", group: groupDev, hidden: true, run: cmdFaceSyncFakeLoop},
		{name: "eye-fix-mode", group: groupDev, hidden: true, run: cmdEyeFixMode},
		{name: "eye-fix-mode-probe", group: groupDev, hidden: true, run: func(string, []string) { runEyeFixModeProbe() }},
		{name: "j3dsys-probe", group: groupDev, hidden: true, run: func(string, []string) { runJ3DSysProbe() }},
		{name: "ppc-disasm", group: groupDev, hidden: true, run: cmdPpcDisasm},
		{name: "eye-fix-step", group: groupDev, hidden: true, run: cmdEyeFixStep},
		{name: "find-pos", group: groupDev, hidden: true, run: func(string, []string) { runFindPos() }},
		{name: "track-pos", group: groupDev, hidden: true, run: cmdTrackPos},
		// Co-op M0 diagnostics (coop_diag.go), kept out of printHelp
		// like the other diagnostics.
		{name: "coop-probe", group: groupDev, hidden: true, run: runCoopDiag},
		{name: "coop-watch", group: groupDev, hidden: true, run: runCoopDiag},
		{name: "actors", group: groupDev, hidden: true, run: runCoopDiag},
		{name: "zone-dump", group: groupDev, hidden: true, run: runCoopDiag},
		{name: "frame-phase-probe", group: groupDev, hidden: true, run: runCoopDiag},
		{name: "heap-free", group: groupDev, hidden: true, run: runCoopDiag},
		{name: "blob-verify-live", group: groupDev, hidden: true, run: runCoopDiag},
		{name: "region-probe", group: groupDev, hidden: true, run: runCoopDiag},
		{name: "stage-hop", group: groupDev, hidden: true, run: runCoopDiag},
		{name: "enemy-kill", group: groupDev, hidden: true, run: runCoopDiag},
		{name: "peek-demo", group: groupDev, hidden: true, run: runCoopDiag},
	}, modCommands()...)
}

// lookupCommand returns the command called name (or aliased to it).
func lookupCommand(name string) (command, bool) {
	for _, c := range commandTable() {
		if c.name == name || slices.Contains(c.aliases, name) {
			return c, true
		}
	}
	return command{}, false
}

// runCommand runs the subcommand name with args; an unknown one prints
// the help and exits 1.
func runCommand(name string, args []string) {
	c, ok := lookupCommand(name)
	if !ok {
		fmt.Printf("Unknown command: %s\n", name)
		printHelp()
		os.Exit(1)
	}
	c.run(name, args)
}

func printHelp() {
	const col = "  %-52s %s\n"
	x := exeCmd
	fmt.Println("Wind Waker Multiplayer " + version)
	table := commandTable()
	for _, s := range helpSections {
		fmt.Println()
		fmt.Println(s.title)
		for _, c := range table {
			if c.hidden || c.group != s.group {
				continue
			}
			if s.long {
				fmt.Println("  " + x + " " + c.usage)
				fmt.Println("    " + c.summary)
			} else {
				fmt.Printf(col, x+" "+c.usage, c.summary)
			}
		}
		if !s.faces {
			continue
		}
		if gui.Available() {
			fmt.Printf(col, x+" tui", "Open the terminal menu (no arguments opens the app window)")
			fmt.Printf(col, x+" "+uiUsage, "Open the app window (--browser: in your browser)")
		} else {
			fmt.Printf(col, x+" tui", "Open the terminal menu (also what no arguments opens)")
			fmt.Printf(col, x+" "+uiUsage, "Open the app in your browser (--browser; this build has no app window)")
		}
	}
}

// The command bodies below moved verbatim from main's switch.

func cmdHost(string, []string) {
	co, rest, err := coopsession.ExtractFlags(os.Args[2:])
	if err != nil {
		exitUsage(err, "host [name] [--port N] "+coopsession.FlagsUsage)
	}
	name, port, err := parseHostArgs(rest)
	if err != nil {
		exitUsage(err, "host [name] [--port N]")
	}
	runHost(name, port, co)
}

func cmdJoin(string, []string) {
	co, rest, err := coopsession.ExtractFlags(os.Args[2:])
	if err == nil {
		err = co.ForJoin()
	}
	if err != nil {
		exitUsage(err, "join <host-ip[:port]> [name] [--port N] "+coopsession.JoinFlagsUsage)
	}
	addr, name, err := parseJoinArgs(rest)
	if err != nil {
		exitUsage(err, "join <host-ip[:port]> [name] [--port N]")
	}
	runJoin(addr, name, co)
}

func cmdPatch(string, []string) {
	if len(os.Args) < 3 {
		fmt.Printf("Usage: %s patch <input.iso|input.ciso> [output.iso]\n", exeCmd)
		fmt.Println("  Default output: <input>-multiplayer.iso (next to the input)")
		os.Exit(1)
	}
	out := ""
	if len(os.Args) > 3 {
		out = os.Args[3]
	}
	runPatch(os.Args[2], out)
}

func cmdServer(string, []string) {
	port, err := parseServerArgs(os.Args[2:])
	if err != nil {
		exitUsage(err, "server [--port N]")
	}
	engine.RunServer(port)
}

func cmdBroadcastPose(string, []string) {
	name := "Player"
	addr := "localhost:25565"
	if len(os.Args) > 2 {
		name = os.Args[2]
	}
	if len(os.Args) > 3 {
		addr = os.Args[3]
	}
	runBroadcastPose(name, addr)
}

func cmdPuppetSync(string, []string) {
	name := "Viewer"
	addr := "localhost:25565"
	if len(os.Args) > 2 {
		name = os.Args[2]
	}
	if len(os.Args) > 3 {
		addr = os.Args[3]
	}
	runPuppetSync(name, addr)
}

func cmdFakeClient(string, []string) {
	name := "FakePlayer"
	addr := "localhost:25565"
	cx := float32(-200048.0)
	cz := float32(316367.0)
	if len(os.Args) > 2 {
		name = os.Args[2]
	}
	if len(os.Args) > 3 {
		addr = os.Args[3]
	}
	if len(os.Args) > 5 {
		if v, err := strconv.ParseFloat(os.Args[4], 32); err == nil {
			cx = float32(v)
		}
		if v, err := strconv.ParseFloat(os.Args[5], 32); err == nil {
			cz = float32(v)
		}
	}
	engine.RunFakeClient(name, addr, cx, cz)
}

func cmdWarp(string, []string) {
	if len(os.Args) < 5 {
		fmt.Println("Usage: ww-multiplayer.exe warp <x> <y> <z>")
		fmt.Println("  Teleports Link in the env-selected Dolphin (WW_DOLPHIN_INDEX/PID)")
		fmt.Println("  to the given world coordinates. Useful for testing, debugging,")
		fmt.Println("  and as a programmatic primitive for other tools.")
		os.Exit(1)
	}
	runWarp(os.Args[2], os.Args[3], os.Args[4])
}
