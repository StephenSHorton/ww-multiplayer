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

// commandTable returns the registry. The listed commands come first, in
// printHelp's order. It is a function, not a var, because help's run calls
// printHelp, which reads the table.
func commandTable() []command {
	return []command{
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
		// Co-op M1 tables from the user's own disc (coop_tables.go),
		// kept out of printHelp like the co-op diagnostics.
		{name: "coop-tables", group: groupDev, hidden: true, run: func(string, []string) { runCoopTables(os.Args[2:]) }},
		// Co-op M2 binding tag (coop_m2_cmds.go): show, clear.
		{name: "coop-tag", group: groupDev, hidden: true, run: func(string, []string) { runCoopTag(os.Args[2:]) }},
		// Co-op M2 worlds and card backups (coop_world_cmds.go):
		// list, restore, block. Out of printHelp like coop-tag.
		{name: "coop-world", group: groupDev, hidden: true, run: func(string, []string) { runCoopWorld(os.Args[2:]) }},
		// Co-op M4 offline save tools (coop_save_cmds.go), out of
		// printHelp like the co-op diagnostics.
		{name: "save-diff", group: groupDev, hidden: true, run: func(string, []string) { runSaveDiff(os.Args[2:]) }},
		{name: "save-merge", group: groupDev, hidden: true, run: func(string, []string) { runSaveMerge(os.Args[2:]) }},
	}
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
	co, rest, err := extractCoopFlags(os.Args[2:])
	if err != nil {
		exitUsage(err, "host [name] [--port N] "+coopFlagsUsage)
	}
	name, port, err := parseHostArgs(rest)
	if err != nil {
		exitUsage(err, "host [name] [--port N]")
	}
	runHost(name, port, co)
}

func cmdJoin(string, []string) {
	co, rest, err := extractCoopFlags(os.Args[2:])
	if err == nil {
		err = co.forJoin()
	}
	if err != nil {
		exitUsage(err, "join <host-ip[:port]> [name] [--port N] "+coopJoinFlagsUsage)
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

func cmdDolphin2(string, []string) {
	reset := false
	for _, a := range os.Args[2:] {
		if a == "--reset" {
			reset = true
		}
	}
	runDolphin2(reset)
}

func cmdMpLocal(string, []string) {
	co, rest, err := extractCoopFlags(os.Args[2:])
	if err != nil {
		exitUsage(err, "mp-local [nameA] [nameB] "+coopFlagsUsage)
	}
	if i := slices.Index(rest, "--"); co.on && i >= 0 {
		rest = slices.Delete(rest, i, i+1) // with --coop, "--" only ends the options
	}
	nameA, nameB := "PlayerA", "PlayerB"
	if len(rest) > 0 {
		nameA = rest[0]
	}
	if len(rest) > 1 {
		nameB = rest[1]
	}
	for _, n := range []string{nameA, nameB} {
		if err := network.ValidatePlayerName(n); err != nil {
			exitUsage(err, "mp-local [nameA] [nameB]")
		}
	}
	runMpLocal(nameA, nameB, co)
}

func cmdServer(string, []string) {
	port, err := parseServerArgs(os.Args[2:])
	if err != nil {
		exitUsage(err, "server [--port N]")
	}
	runServer(port)
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
	runFakeClient(name, addr, cx, cz)
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

func cmdScreenshot(string, []string) {
	out := ""
	if len(os.Args) > 2 {
		out = os.Args[2]
	}
	runScreenshot(out)
}

func cmdAutoRecapture(string, []string) {
	out := ""
	if len(os.Args) > 2 {
		out = os.Args[2]
	}
	runAutoRecapture(out)
}

func cmdAutoRecapturePair(string, []string) {
	out1, out2 := "", ""
	delaySecs := 5
	if len(os.Args) > 2 {
		out1 = os.Args[2]
	}
	if len(os.Args) > 3 {
		out2 = os.Args[3]
	}
	if len(os.Args) > 4 {
		if n, err := strconv.Atoi(os.Args[4]); err == nil && n > 0 {
			delaySecs = n
		}
	}
	runAutoRecapturePair(out1, out2, time.Duration(delaySecs)*time.Second)
}

func cmdMovePuppet(string, []string) {
	if len(os.Args) < 5 {
		fmt.Println("Usage: ww move-puppet <x> <y> <z> [slot=0]")
		os.Exit(1)
	}
	slot := 0
	if len(os.Args) > 5 {
		if v, err := strconv.Atoi(os.Args[5]); err == nil {
			slot = v
		}
	}
	runMovePuppet(os.Args[2], os.Args[3], os.Args[4], slot)
}

func cmdInput(string, []string) {
	// input <buttons-hex> <stickX-s8> <stickY-s8> [ms=1000]
	// Drives synthetic controller input through the C-side
	// pad_read_shim. If ms > 0, holds for that duration then
	// releases (input_enable=0). If ms == 0, sets-and-leaves —
	// caller is responsible for releasing.
	if len(os.Args) < 5 {
		fmt.Println("Usage: ww-multiplayer.exe input <buttons-hex> <stickX> <stickY> [ms=1000]")
		fmt.Println("  buttons mask: A=0x100 B=0x200 X=0x400 Y=0x800 Start=0x1000")
		fmt.Println("                Z=0x10 R=0x20 L=0x40")
		fmt.Println("                DPad L=1 R=2 D=4 U=8")
		fmt.Println("  stickX/Y: -128..+127 (0 = neutral)")
		fmt.Println("  ms: hold duration; 0 = set-and-leave (manual release)")
		os.Exit(1)
	}
	ms := 1000
	if len(os.Args) > 5 {
		if v, err := strconv.Atoi(os.Args[5]); err == nil {
			ms = v
		}
	}
	runInput(os.Args[2], os.Args[3], os.Args[4], ms)
}

func cmdInputProbe(string, []string) {
	// input-probe <buttons-hex> <stickX-s8> <stickY-s8> [ms=2000]
	// Direct write to JUTGamePad::mPadStatus[0] at 0x803ED818.
	// KNOWN NOT TO WORK: writes survive same-process readback
	// but don't propagate to game logic — either dual-mapping
	// or per-frame SI overwrite races. Kept as a diagnostic
	// for memory-write reachability against the input region.
	// For real input use the `input` subcommand instead.
	if len(os.Args) < 5 {
		fmt.Println("Usage: ww-multiplayer.exe input-probe <buttons-hex> <stickX> <stickY> [ms=2000]")
		os.Exit(1)
	}
	ms := 2000
	if len(os.Args) > 5 {
		if v, err := strconv.Atoi(os.Args[5]); err == nil {
			ms = v
		}
	}
	runInputProbe(os.Args[2], os.Args[3], os.Args[4], ms)
}

func cmdPokeU32(string, []string) {
	if len(os.Args) < 4 {
		fmt.Println("Usage: ww poke-u32 <addr-hex> <value-hex>")
		os.Exit(1)
	}
	runPokeU32(os.Args[2], os.Args[3])
}

func cmdShadowMode(string, []string) {
	if len(os.Args) < 3 {
		fmt.Println("Usage: ww shadow-mode <0|1|2|3|4|5>  (0=off 1=mirror-refresh 2=mirror-freeze 3=no-op basicMtxCalc 4=echo-ring 5=pose-feed)")
		os.Exit(1)
	}
	runShadowMode(os.Args[2])
}

func cmdEchoDelay(string, []string) {
	if len(os.Args) < 3 {
		fmt.Println("Usage: ww echo-delay <N>  (0=identity 1..59=delayed replay; requires shadow-mode 4)")
		os.Exit(1)
	}
	runEchoDelay(os.Args[2])
}

func cmdPoseTest(string, []string) {
	mode := "mirror"
	dur := 30
	if len(os.Args) > 2 {
		mode = os.Args[2]
	}
	if len(os.Args) > 3 {
		if v, err := strconv.Atoi(os.Args[3]); err == nil {
			dur = v
		}
	}
	runPoseTest(mode, dur)
}

func cmdBroadcastLink(string, []string) {
	name := "Player"
	addr := "localhost:25565"
	if len(os.Args) > 2 {
		name = os.Args[2]
	}
	if len(os.Args) > 3 {
		addr = os.Args[3]
	}
	runBroadcastLink(name, addr)
}

func cmdPoseFakeLoop(string, []string) {
	name := "FakePlayer"
	addr := "localhost:25565"
	if len(os.Args) > 2 {
		name = os.Args[2]
	}
	if len(os.Args) > 3 {
		addr = os.Args[3]
	}
	feed.PoseFakeLoop(feedEnv, name, addr)
}

func cmdTunicRecolor(string, []string) {
	if len(os.Args) < 3 {
		fmt.Println("Usage: ww-multiplayer.exe tunic-recolor <color|#RRGGBB|reset>")
		os.Exit(1)
	}
	runTunicRecolor(os.Args[2])
}

func cmdTunicSlot(string, []string) {
	if len(os.Args) < 4 {
		fmt.Println("Usage: ww-multiplayer.exe tunic-slot <0|1|local> <color|#RRGGBB|off|casual>")
		os.Exit(1)
	}
	runTunicSlot(os.Args[2], os.Args[3])
}

func cmdDisasm(string, []string) {
	addr := uint32(0x800231E4)
	count := 40
	if len(os.Args) > 2 {
		var v uint64
		v, err := strconv.ParseUint(strings.TrimPrefix(os.Args[2], "0x"), 16, 32)
		if err != nil {
			fmt.Printf("bad addr: %v\n", err)
			os.Exit(1)
		}
		addr = uint32(v)
	}
	if len(os.Args) > 3 {
		n, err := strconv.Atoi(os.Args[3])
		if err == nil {
			count = n
		}
	}
	runDisasm(addr, count)
}

func cmdPokeVec3(string, []string) {
	if len(os.Args) < 6 {
		fmt.Println("Usage: ww-multiplayer.exe poke-vec3 <addr-hex> <x> <y> <z>")
		os.Exit(1)
	}
	runPokeVec3(os.Args[2], os.Args[3], os.Args[4], os.Args[5])
}

func cmdPeek(string, []string) {
	if len(os.Args) < 3 {
		fmt.Println("Usage: ww-multiplayer.exe peek <addr-hex> [n=4]")
		os.Exit(1)
	}
	n := uint32(4)
	if len(os.Args) > 3 {
		if v, err := strconv.ParseUint(os.Args[3], 10, 32); err == nil {
			n = uint32(v)
		}
	}
	runPeek(os.Args[2], n)
}

func cmdWarpForce(string, []string) {
	if len(os.Args) < 5 {
		fmt.Println("Usage: ww-multiplayer.exe warp-force <x> <y> <z>")
		fmt.Println("  Sets warp target + warp_force flag so C re-applies the warp")
		fmt.Println("  every frame. Use `warp-force-off` to release.")
		os.Exit(1)
	}
	runWarpForce(os.Args[2], os.Args[3], os.Args[4], true)
}

func cmdEyeAnimDlDump(string, []string) {
	if len(os.Args) < 3 {
		fmt.Println("Usage: ww-multiplayer.exe eye-anim-dl-dump <model-addr-hex> [n=256]")
		os.Exit(1)
	}
	n := uint32(256)
	if len(os.Args) > 3 {
		if v, err := strconv.ParseUint(os.Args[3], 10, 32); err == nil {
			n = uint32(v)
		}
	}
	runEyeAnimDLDump(os.Args[2], n)
}

func cmdEyeAnimWatch(string, []string) {
	if len(os.Args) < 4 {
		fmt.Println("Usage: ww-multiplayer.exe eye-anim-watch <addr-hex> <size-hex> [secs=10]")
		fmt.Println("  Watches a memory range for byte-level changes over time.")
		fmt.Println("  Reports byte offsets that ever differ from the initial sample.")
		os.Exit(1)
	}
	secs := 10
	if len(os.Args) > 4 {
		if v, err := strconv.Atoi(os.Args[4]); err == nil {
			secs = v
		}
	}
	runEyeAnimWatch(os.Args[2], os.Args[3], secs)
}

func cmdEyeAnimPoke(string, []string) {
	if len(os.Args) < 3 {
		fmt.Println("Usage: ww-multiplayer.exe eye-anim-poke <model-addr-hex> [secs=10]")
		os.Exit(1)
	}
	secs := 10
	if len(os.Args) > 3 {
		if v, err := strconv.Atoi(os.Args[3]); err == nil {
			secs = v
		}
	}
	runEyeAnimPoke(os.Args[2], secs)
}

func cmdFaceSyncWatch(string, []string) {
	secs := 10
	if len(os.Args) > 2 {
		if v, err := strconv.Atoi(os.Args[2]); err == nil {
			secs = v
		}
	}
	runFaceSyncWatch(secs)
}

func cmdFaceTevHammer(string, []string) {
	secs := 5
	texHex := "0099"
	if len(os.Args) > 2 {
		if v, err := strconv.Atoi(os.Args[2]); err == nil {
			secs = v
		}
	}
	if len(os.Args) > 3 {
		texHex = os.Args[3]
	}
	runFaceTevHammer(secs, texHex)
}

func cmdFaceMatHammer(string, []string) {
	if len(os.Args) < 3 {
		fmt.Println("Usage: ww-multiplayer.exe face-mat-hammer <idx> [hex=0099] [secs=0]")
		fmt.Println("  Tight-loop hammers mat[idx]'s tev_block.texno[0..1]. Run until Ctrl+C if secs=0.")
		os.Exit(1)
	}
	idx, _ := strconv.Atoi(os.Args[2])
	texHex := "0099"
	secs := 0
	if len(os.Args) > 3 {
		texHex = strings.TrimPrefix(os.Args[3], "0x")
	}
	if len(os.Args) > 4 {
		if v, err := strconv.Atoi(os.Args[4]); err == nil {
			secs = v
		}
	}
	runFaceMatHammer(idx, texHex, secs)
}

func cmdFaceSyncTevPoll(string, []string) {
	secs := 3
	faceHex := "9988997799669955"
	if len(os.Args) > 2 {
		if v, err := strconv.Atoi(os.Args[2]); err == nil {
			secs = v
		}
	}
	if len(os.Args) > 3 {
		faceHex = strings.TrimPrefix(os.Args[3], "0x")
	}
	runFaceSyncTevPoll(secs, faceHex)
}

func cmdFaceDlLoop(string, []string) {
	secs := 0 // 0 = until Ctrl+C
	forceLsb := -1
	if len(os.Args) > 2 {
		if v, err := strconv.Atoi(os.Args[2]); err == nil {
			secs = v
		}
	}
	if len(os.Args) > 3 {
		h := strings.TrimPrefix(os.Args[3], "0x")
		if v, err := strconv.ParseUint(h, 16, 8); err == nil {
			forceLsb = int(v)
		}
	}
	runFaceDlLoop(secs, forceLsb)
}

func cmdFaceSyncFake(string, []string) {
	if len(os.Args) < 4 {
		fmt.Println("Usage: ww-multiplayer.exe face-sync-fake <slot> <hex8>")
		fmt.Println("  hex8 = 8 hex bytes for [mat1_t0:u16][mat1_t1:u16][mat4_t0:u16][mat4_t1:u16]")
		fmt.Println("  e.g. 00000027000000270 to force open eyes; 0000FFFF0000FFFF for closed")
		os.Exit(1)
	}
	slot, _ := strconv.Atoi(os.Args[2])
	runFaceSyncFake(slot, os.Args[3])
}

func cmdFaceSyncFakeLoop(string, []string) {
	if len(os.Args) < 4 {
		fmt.Println("Usage: ww-multiplayer.exe face-sync-fake-loop <slot> <hex8> [secs=0]")
		os.Exit(1)
	}
	slot, _ := strconv.Atoi(os.Args[2])
	secs := 0
	if len(os.Args) > 4 {
		if v, err := strconv.Atoi(os.Args[4]); err == nil {
			secs = v
		}
	}
	runFaceSyncFakeLoop(slot, os.Args[3], secs)
}

func cmdEyeFixMode(string, []string) {
	if len(os.Args) < 3 {
		fmt.Println("Usage: ww-multiplayer.exe eye-fix-mode <0|1|2>")
		fmt.Println("  0 = legacy (run_eye_fix + mDoExt mini-link)")
		fmt.Println("  1 = mClModel-swap one-draw (Link invisible, mini-link only)")
		fmt.Println("  2 = mClModel-swap two-draw (both visible, mini-link gets eye decals)")
		os.Exit(1)
	}
	n, err := strconv.Atoi(os.Args[2])
	if err != nil || n < 0 || n > 2 {
		fmt.Printf("mode must be 0..2, got %q\n", os.Args[2])
		os.Exit(1)
	}
	runEyeFixMode(byte(n))
}

func cmdPpcDisasm(string, []string) {
	if len(os.Args) < 3 {
		fmt.Println("Usage: ww-multiplayer.exe ppc-disasm <addr-hex> [num-words=32]")
		os.Exit(1)
	}
	addr, err := strconv.ParseUint(strings.TrimPrefix(os.Args[2], "0x"), 16, 32)
	if err != nil {
		fmt.Printf("bad addr %q: %v\n", os.Args[2], err)
		os.Exit(1)
	}
	n := 32
	if len(os.Args) > 3 {
		if v, err := strconv.Atoi(os.Args[3]); err == nil {
			n = v
		}
	}
	runPPCDisasm(uint32(addr), n)
}

func cmdEyeFixStep(string, []string) {
	if len(os.Args) < 3 {
		fmt.Println("Usage: ww-multiplayer.exe eye-fix-step <0..8>")
		fmt.Println("  Stepwise probe for the mini-Link eye-decal recipe.")
		fmt.Println("  0 = off; 1 = list swap; 2..4 bisect Pass 1 (entryOpa,")
		fmt.Println("  shape-vis, entryIn); 5..8 add Passes 2..5 cumulatively.")
		fmt.Println("  See inject/include/mailbox.h eye_fix_step comment.")
		os.Exit(1)
	}
	n, err := strconv.Atoi(os.Args[2])
	if err != nil || n < 0 || n > 8 {
		fmt.Printf("step must be an integer 0..8, got %q\n", os.Args[2])
		os.Exit(1)
	}
	runEyeFixStep(byte(n))
}

func cmdTrackPos(string, []string) {
	secs := 5
	if len(os.Args) > 2 {
		if v, err := strconv.Atoi(os.Args[2]); err == nil {
			secs = v
		}
	}
	runTrackPos(secs)
}
