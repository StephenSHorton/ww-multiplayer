package main

// wwdev's command registry: the developer tools that were ww-multiplayer's
// groupLocal and hidden groupDev commands until Phase D6 (dolphin2,
// mp-local, the co-op diagnostics, auto-recapture, the tunic tools and
// the research probes). main dispatches every subcommand through
// commandTable, and printHelp is generated from it;
// testdata/cli-subcommands.txt pins the names
// (TestWWDevSubcommandsUnchanged). The command bodies (commands.go) still
// read os.Args themselves, as they did in ww-multiplayer.

import (
	"fmt"
	"os"
)

// cmdGroup is the printHelp section a command is listed under.
type cmdGroup int

const (
	groupLocal cmdGroup = iota // Local two-Dolphin testing: usage and summary
	groupDev                   // diagnostics and developer tools: names only
)

// command is one wwdev subcommand.
type command struct {
	name    string
	group   cmdGroup
	usage   string // printHelp's usage, after the binary (groupLocal only)
	summary string // printHelp's description (groupLocal only)
	// run runs the command with its name (os.Args[1]) and the arguments
	// after it (os.Args[2:]).
	run func(name string, args []string)
}

// commandTable returns the registry. It is a function, not a var,
// because help's run calls printHelp, which reads the table.
func commandTable() []command {
	return []command{
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
			name:    "help",
			group:   groupLocal,
			usage:   "help",
			summary: "Show this help",
			run:     func(string, []string) { printHelp() },
		},
		// Diagnostics and developer tools, in the order of the switch
		// ww-multiplayer's table replaced; help lists their names only.
		{name: "screenshot", group: groupDev, run: cmdScreenshot},
		{name: "auto-recapture", group: groupDev, run: cmdAutoRecapture},
		{name: "auto-recapture-pair", group: groupDev, run: cmdAutoRecapturePair},
		// Diagnostic: sends Shift+F1 via Win32 PostMessage to the
		// selected Dolphin window. Used to validate the hotkey
		// path without re-running the full auto-recapture flow.
		{name: "send-shift-f1", group: groupDev, run: func(string, []string) { runSendShiftF1() }},
		{name: "dump", group: groupDev, run: func(string, []string) { runDump() }},
		{name: "move-puppet", group: groupDev, run: cmdMovePuppet},
		{name: "input", group: groupDev, run: cmdInput},
		{name: "input-release", group: groupDev, run: func(string, []string) { runInputRelease() }},
		{name: "input-probe", group: groupDev, run: cmdInputProbe},
		{name: "poke-u32", group: groupDev, run: cmdPokeU32},
		{name: "shadow-mode", group: groupDev, run: cmdShadowMode},
		{name: "pose-test", group: groupDev, run: cmdPoseTest},
		{name: "unhide-puppet", group: groupDev, run: func(string, []string) { runUnhidePuppet() }},
		{name: "broadcast-link", group: groupDev, run: cmdBroadcastLink},
		{name: "pose-fake-loop", group: groupDev, run: cmdPoseFakeLoop},
		{name: "tunic-probe", group: groupDev, run: func(string, []string) { runTunicProbe() }},
		{name: "tunic-recolor", group: groupDev, run: cmdTunicRecolor},
		{name: "tunic-slot", group: groupDev, run: cmdTunicSlot},
		{name: "disasm", group: groupDev, run: cmdDisasm},
		{name: "scan-pos", group: groupDev, run: func(string, []string) { runScanPos() }},
		{name: "poke-vec3", group: groupDev, run: cmdPokeVec3},
		{name: "peek", group: groupDev, run: cmdPeek},
		{name: "warp-force", group: groupDev, run: cmdWarpForce},
		{name: "warp-force-off", group: groupDev, run: func(string, []string) { runWarpForce("", "", "", false) }},
		{name: "eye-fix-probe", group: groupDev, run: func(string, []string) { runEyeFixProbe() }},
		{name: "eye-fix-chain", group: groupDev, run: func(string, []string) { runEyeFixChain() }},
		{name: "eye-fix-find-shape", group: groupDev, run: func(string, []string) { runEyeFixFindShape() }},
		{name: "eye-fix-gates", group: groupDev, run: func(string, []string) { runEyeFixGates() }},
		{name: "eye-anim-find", group: groupDev, run: func(string, []string) { runEyeAnimFind() }},
		{name: "eye-anim-dl-dump", group: groupDev, run: cmdEyeAnimDlDump},
		{name: "eye-anim-watch", group: groupDev, run: cmdEyeAnimWatch},
		{name: "eye-anim-poke", group: groupDev, run: cmdEyeAnimPoke},
		{name: "face-sync-status", group: groupDev, run: func(string, []string) { runFaceSyncStatus() }},
		{name: "face-sync-watch", group: groupDev, run: cmdFaceSyncWatch},
		{name: "face-hook-arm", group: groupDev, run: func(string, []string) { runFaceHookArm(true) }},
		{name: "face-hook-disarm", group: groupDev, run: func(string, []string) { runFaceHookArm(false) }},
		{name: "face-tev-hammer", group: groupDev, run: cmdFaceTevHammer},
		{name: "face-mat-list", group: groupDev, run: func(string, []string) { runFaceMatList() }},
		{name: "face-mat-hammer", group: groupDev, run: cmdFaceMatHammer},
		{name: "face-sync-tev-poll", group: groupDev, run: cmdFaceSyncTevPoll},
		{name: "face-dl-loop", group: groupDev, run: cmdFaceDlLoop},
		{name: "face-sync-fake", group: groupDev, run: cmdFaceSyncFake},
		{name: "face-sync-fake-loop", group: groupDev, run: cmdFaceSyncFakeLoop},
		{name: "j3dsys-probe", group: groupDev, run: func(string, []string) { runJ3DSysProbe() }},
		{name: "ppc-disasm", group: groupDev, run: cmdPpcDisasm},
		{name: "find-pos", group: groupDev, run: func(string, []string) { runFindPos() }},
		{name: "track-pos", group: groupDev, run: cmdTrackPos},
		// Co-op M0 diagnostics (coop_diag.go).
		{name: "coop-probe", group: groupDev, run: runCoopDiag},
		{name: "coop-watch", group: groupDev, run: runCoopDiag},
		{name: "actors", group: groupDev, run: runCoopDiag},
		{name: "zone-dump", group: groupDev, run: runCoopDiag},
		{name: "frame-phase-probe", group: groupDev, run: runCoopDiag},
		{name: "heap-free", group: groupDev, run: runCoopDiag},
		{name: "blob-verify-live", group: groupDev, run: runCoopDiag},
		{name: "region-probe", group: groupDev, run: runCoopDiag},
		{name: "stage-hop", group: groupDev, run: runCoopDiag},
		{name: "enemy-kill", group: groupDev, run: runCoopDiag},
		{name: "peek-demo", group: groupDev, run: runCoopDiag},
		// Enemy sync, the host-authoritative Moblin prototype (enemy_probe.go).
		{name: "enemy-watch", group: groupDev, run: runEnemyWatch},
		{name: "enemy-mirror-local", group: groupDev, run: runEnemyMirrorLocal},
		{name: "enemy-block-dump", group: groupDev, run: runEnemyBlockDump},
		// The generalised mirror's L0 pairing check (enemy_kinds_probe.go).
		{name: "enemy-match", group: groupDev, run: runEnemyMatch},
		// Remote-Link, host enemies targeting every player (remote_probe.go).
		{name: "remote-watch", group: groupDev, run: runRemoteWatch},
		{name: "remote-flags", group: groupDev, run: runRemoteFlags},
		{name: "remote-slot", group: groupDev, run: runRemoteSlot},
		// Stage 4 full body: the section 3.5 globals before/after (fullbody_globals.go).
		{name: "fullbody-globals", group: groupDev, run: runFullbodyGlobals},
		// Batch D's P0 probes: Gohdan, the refresh procs, the Magtail body (enemy_batchd_probe.go).
		{name: "bst-probe", group: groupDev, run: runBatchDProbe},
		{name: "rf-probe", group: groupDev, run: runBatchDProbe},
		{name: "mt2-probe", group: groupDev, run: runBatchDProbe},
		// Batch E's boss_kill probes: Molgera, Jalhalla (enemy_batche_probe.go).
		{name: "bwd-probe", group: groupDev, run: runBatchEProbe},
		{name: "bpw-probe", group: groupDev, run: runBatchEProbe},
		// Batch F's boss_kill probe: Helmaroc King (enemy_batchf_probe.go).
		{name: "bdk-probe", group: groupDev, run: runBatchFProbe},
		// The crash JUTException caught: registers, stack, REL modules (crash_context.go).
		{name: "crash-context", group: groupDev, run: runCrashContext},
	}
}

// lookupCommand returns the command called name.
func lookupCommand(name string) (command, bool) {
	for _, c := range commandTable() {
		if c.name == name {
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

// printHelp lists the local-testing commands with their usage, then the
// diagnostics by name, wrapped; their usage is in their sources
// (commands.go, and the files they call) and in CLAUDE.md.
func printHelp() {
	fmt.Println("wwdev: Wind Waker Multiplayer developer tools (not shipped)")
	fmt.Println()
	fmt.Println("Local two-Dolphin testing (everything in-process):")
	var dev []string
	for _, c := range commandTable() {
		if c.group == groupDev {
			dev = append(dev, c.name)
			continue
		}
		fmt.Printf("  %-40s %s\n", exeCmd+" "+c.usage, c.summary)
	}
	fmt.Println()
	fmt.Println("Diagnostics and research probes (Dolphin picked by WW_DOLPHIN_INDEX / WW_DOLPHIN_PID):")
	line := " "
	for _, n := range dev {
		if len(line)+1+len(n) > 78 {
			fmt.Println(line)
			line = " "
		}
		line += " " + n
	}
	fmt.Println(line)
}
