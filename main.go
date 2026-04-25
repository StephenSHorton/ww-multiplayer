// To regenerate the Windows icon resource (ww_windows.syso) after changing
// docs/img/icon.ico, run:
//   go install github.com/akavel/rsrc@latest
//   rsrc -ico docs/img/icon.ico -o ww_windows.syso
// Go's linker auto-includes any *.syso in the main package; the _windows
// suffix is a build constraint so non-Windows builds skip it.
//go:generate rsrc -ico docs/img/icon.ico -o ww_windows.syso

package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/dolphin"
	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/internal/network"
	"github.com/StephenSHorton/ww-multiplayer/internal/report"
	"github.com/StephenSHorton/ww-multiplayer/internal/tui"
)

// version is overridden at build time by `go build -ldflags "-X main.version=..."`.
// release.yml passes the git tag (e.g. "v0.1.5"); local builds keep "dev".
var version = "dev"

// openDolphin is the multiplexer between env-driven Find() (CLI flows
// that respect WW_DOLPHIN_INDEX / WW_DOLPHIN_PID) and explicit-PID
// FindByPID (mp-local, which enumerates both Dolphins upfront so two
// in-process goroutines can address different instances without the
// env-var collision).
func openDolphin(pid uint32) (*dolphin.Dolphin, error) {
	if pid == 0 {
		return dolphin.Find("GZLE01")
	}
	return dolphin.FindByPID(pid, "GZLE01")
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "debug":
			runDebug()
		case "host":
			name := ""
			if len(os.Args) > 2 {
				name = os.Args[2]
			}
			runHost(name)
		case "join":
			if len(os.Args) < 3 {
				fmt.Println("Usage: ww join <host-ip> [name]")
				os.Exit(1)
			}
			addr := os.Args[2]
			name := ""
			if len(os.Args) > 3 {
				name = os.Args[3]
			}
			runJoin(addr, name)
		case "patch":
			if len(os.Args) < 3 {
				fmt.Println("Usage: ww patch <input.iso|input.ciso> [output.iso]")
				fmt.Println("  Default output: <input>-multiplayer.iso (next to the input)")
				os.Exit(1)
			}
			out := ""
			if len(os.Args) > 3 {
				out = os.Args[3]
			}
			runPatch(os.Args[2], out)
		case "server":
			runServer()
		case "fake-client":
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
		case "write-test":
			runWriteTest()
		case "teleport-test":
			runTeleportTest()
		case "scan-actors":
			runActorScan()
		case "scan-npcs":
			runNPCScan()
		case "hijack-test":
			runHijackTest()
		case "inject":
			runInject()
		case "check":
			runCheck()
		case "dump":
			runDump()
		case "move-puppet":
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
		case "poke-u32":
			if len(os.Args) < 4 {
				fmt.Println("Usage: ww poke-u32 <addr-hex> <value-hex>")
				os.Exit(1)
			}
			runPokeU32(os.Args[2], os.Args[3])
		case "shadow-mode":
			if len(os.Args) < 3 {
				fmt.Println("Usage: ww shadow-mode <0|1|2|3|4|5>  (0=off 1=mirror-refresh 2=mirror-freeze 3=no-op basicMtxCalc 4=echo-ring 5=pose-feed)")
				os.Exit(1)
			}
			runShadowMode(os.Args[2])
		case "echo-delay":
			if len(os.Args) < 3 {
				fmt.Println("Usage: ww echo-delay <N>  (0=identity 1..59=delayed replay; requires shadow-mode 4)")
				os.Exit(1)
			}
			runEchoDelay(os.Args[2])
		case "pose-test":
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
		case "unhide-puppet":
			runUnhidePuppet()
		case "broadcast-link":
			name := "Player"
			addr := "localhost:25565"
			if len(os.Args) > 2 {
				name = os.Args[2]
			}
			if len(os.Args) > 3 {
				addr = os.Args[3]
			}
			runBroadcastLink(name, addr)
		case "broadcast-pose":
			name := "Player"
			addr := "localhost:25565"
			if len(os.Args) > 2 {
				name = os.Args[2]
			}
			if len(os.Args) > 3 {
				addr = os.Args[3]
			}
			runBroadcastPose(name, addr)
		case "pose-fake-loop":
			name := "FakePlayer"
			addr := "localhost:25565"
			if len(os.Args) > 2 {
				name = os.Args[2]
			}
			if len(os.Args) > 3 {
				addr = os.Args[3]
			}
			runPoseFakeLoop(name, addr)
		case "puppet-sync":
			name := "Viewer"
			addr := "localhost:25565"
			if len(os.Args) > 2 {
				name = os.Args[2]
			}
			if len(os.Args) > 3 {
				addr = os.Args[3]
			}
			runPuppetSync(name, addr)
		case "inspect-materials":
			runInspectMaterials()
		case "tint-material":
			if len(os.Args) < 3 {
				fmt.Println("Usage:")
				fmt.Println("  ww-multiplayer.exe tint-material <idx> <rgba-hex>    (8 hex digits, e.g. FF0000FF)")
				fmt.Println("  ww-multiplayer.exe tint-material <idx> reset         (restore to FFFFFFFF)")
				fmt.Println("  ww-multiplayer.exe tint-material cycle [seconds=2]   (walk all 24 materials)")
				os.Exit(1)
			}
			if os.Args[2] == "cycle" {
				secs := 2
				if len(os.Args) > 3 {
					if v, err := strconv.Atoi(os.Args[3]); err == nil && v > 0 {
						secs = v
					}
				}
				runTintCycle(secs)
			} else if os.Args[2] == "pick" {
				runTintPick()
			} else if os.Args[2] == "stage" {
				if len(os.Args) < 5 {
					fmt.Println("Usage: ww-multiplayer.exe tint-material stage <mat-idx> <stage-idx>")
					os.Exit(1)
				}
				mi, err1 := strconv.Atoi(os.Args[3])
				si, err2 := strconv.Atoi(os.Args[4])
				if err1 != nil || err2 != nil || si < 0 || si > 7 {
					fmt.Println("bad mat-idx or stage-idx (stage must be 0..7)")
					os.Exit(1)
				}
				runTintStage(mi, si)
			} else {
				if len(os.Args) < 4 {
					fmt.Println("missing color arg (rgba-hex or 'reset')")
					os.Exit(1)
				}
				idx, err := strconv.Atoi(os.Args[2])
				if err != nil {
					fmt.Printf("bad index: %v\n", err)
					os.Exit(1)
				}
				runTintMaterial(idx, os.Args[3])
			}
		case "disasm":
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
		case "scan-pos":
			runScanPos()
		case "poke-vec3":
			if len(os.Args) < 6 {
				fmt.Println("Usage: ww-multiplayer.exe poke-vec3 <addr-hex> <x> <y> <z>")
				os.Exit(1)
			}
			runPokeVec3(os.Args[2], os.Args[3], os.Args[4], os.Args[5])
		case "peek":
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
		case "warp-force":
			if len(os.Args) < 5 {
				fmt.Println("Usage: ww-multiplayer.exe warp-force <x> <y> <z>")
				fmt.Println("  Sets warp target + warp_force flag so C re-applies the warp")
				fmt.Println("  every frame. Use `warp-force-off` to release.")
				os.Exit(1)
			}
			runWarpForce(os.Args[2], os.Args[3], os.Args[4], true)
		case "warp-force-off":
			runWarpForce("", "", "", false)
		case "eye-fix-probe":
			runEyeFixProbe()
		case "eye-fix-chain":
			runEyeFixChain()
		case "eye-fix-find-shape":
			runEyeFixFindShape()
		case "eye-fix-gates":
			runEyeFixGates()
		case "j3dsys-probe":
			runJ3DSysProbe()
		case "ppc-disasm":
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
		case "eye-fix-step":
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
		case "find-pos":
			runFindPos()
		case "track-pos":
			secs := 5
			if len(os.Args) > 2 {
				if v, err := strconv.Atoi(os.Args[2]); err == nil {
					secs = v
				}
			}
			runTrackPos(secs)
		case "warp":
			if len(os.Args) < 5 {
				fmt.Println("Usage: ww-multiplayer.exe warp <x> <y> <z>")
				fmt.Println("  Teleports Link in the env-selected Dolphin (WW_DOLPHIN_INDEX/PID)")
				fmt.Println("  to the given world coordinates. Useful for testing, debugging,")
				fmt.Println("  and as a programmatic primitive for other tools.")
				os.Exit(1)
			}
			runWarp(os.Args[2], os.Args[3], os.Args[4])
		case "dolphin2":
			reset := false
			for _, a := range os.Args[2:] {
				if a == "--reset" {
					reset = true
				}
			}
			runDolphin2(reset)
		case "mp-local":
			nameA, nameB := "PlayerA", "PlayerB"
			if len(os.Args) > 2 {
				nameA = os.Args[2]
			}
			if len(os.Args) > 3 {
				nameB = os.Args[3]
			}
			runMpLocal(nameA, nameB)
		case "help":
			printHelp()
		default:
			fmt.Printf("Unknown command: %s\n", os.Args[1])
			printHelp()
			os.Exit(1)
		}
		return
	}

	// No subcommand — launch the TUI. v0.0's Bubble Tea TUI was retired
	// in v0.1.2 (a35d4ec) because it never wired the connect screen to
	// the actual pose-feed pipeline, so multiplayer silently didn't
	// engage. v0.1.5 brings it back with the dashboard driving the same
	// runHostSession / runJoinSession funcs the CLI subcommands run, and
	// every Reporter log line surfacing in the dashboard's log panel.
	if err := tui.Run(version, tui.Hooks{
		HostSession: runHostSession,
		JoinSession: runJoinSession,
	}); err != nil {
		fmt.Println(err)
		fmt.Print("Press Enter to exit...")
		bufio.NewReader(os.Stdin).ReadString('\n')
		os.Exit(1)
	}
}

func printHelp() {
	const col = "  %-52s %s\n"
	fmt.Println("Wind Waker Multiplayer")
	fmt.Println()
	fmt.Println("Play multiplayer:")
	fmt.Printf(col, "ww-multiplayer.exe host [name]", "Host a session on :25565 (one process per player)")
	fmt.Printf(col, "ww-multiplayer.exe join <host-ip> [name]", "Join a host's session (host-ip is what `host` prints)")
	fmt.Println()
	fmt.Println("Patch an ISO:")
	fmt.Println("  ww-multiplayer.exe patch <vanilla.iso|.ciso> [out.iso]")
	fmt.Println("    Splice the multiplayer mod into your own legitimate copy of Wind Waker (GZLE01)")
	fmt.Println()
	fmt.Println("Local two-Dolphin testing (everything in-process):")
	fmt.Printf(col, "ww-multiplayer.exe dolphin2 [--reset]", "Bootstrap & launch a 2nd Dolphin instance")
	fmt.Printf(col, "ww-multiplayer.exe mp-local [nameA] [nameB]", "Run server + broadcast/puppet pairs for both Dolphins")
	fmt.Println()
	fmt.Println("Lower-level CLIs:")
	fmt.Printf(col, "ww-multiplayer.exe server", "Start headless server on :25565")
	fmt.Printf(col, "ww-multiplayer.exe broadcast-pose [name] [addr]", "Stream local Link pose+pos to server")
	fmt.Printf(col, "ww-multiplayer.exe puppet-sync [name] [addr]", "Receive remotes; render as Link #2 / actor puppets")
	fmt.Printf(col, "ww-multiplayer.exe fake-client [name] [addr]", "Connect a fake client that walks in circles")
	fmt.Printf(col, "ww-multiplayer.exe debug", "Test Dolphin memory access")
	fmt.Printf(col, "ww-multiplayer.exe warp <x> <y> <z>", "Teleport Link to world coords (uses C warp handler)")
	fmt.Printf(col, "ww-multiplayer.exe help", "Show this help")
}

// runPatch is the user-facing wrapper for inject.PatchISO. Picks a sensible
// default output filename (`<input>-multiplayer.iso`) when the user doesn't
// supply one, and prints a tiny status line so successful runs aren't
// silently mysterious.
func runPatch(in, out string) {
	if out == "" {
		base := in
		// Strip extension before tagging "-multiplayer.iso" so we don't
		// produce things like "wind-waker.ciso-multiplayer.iso".
		for i := len(base) - 1; i >= 0; i-- {
			if base[i] == '.' {
				base = base[:i]
				break
			}
			if base[i] == '/' || base[i] == '\\' {
				break
			}
		}
		out = base + "-multiplayer.iso"
	}
	fmt.Printf("Wind Waker Multiplayer patcher\n")
	fmt.Printf("  input : %s\n", in)
	fmt.Printf("  output: %s\n", out)
	if err := inject.PatchISO(in, out); err != nil {
		if err == inject.ErrAlreadyPatched {
			fmt.Printf("\nThis ISO already contains the multiplayer mod — nothing to do.\n")
			fmt.Printf("(Detected an existing T2 section at 0x%08X.)\n", inject.T2Address)
			os.Exit(0)
		}
		fmt.Printf("\nERROR: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\nDone. Boot %s in Dolphin to play multiplayer.\n", out)
}

func runWriteTest() {
	fmt.Println("=== Write Test: Setting rupees to 999 ===")
	fmt.Println()

	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	defer d.Close()

	// Read current rupees (big-endian u16 at 0x803C4C0C)
	before, _ := d.ReadAbsolute(0x803C4C0C, 2)
	fmt.Printf("Rupees before: %02X%02X\n", before[0], before[1])

	// Write 999 (0x03E7) as big-endian
	err = d.WriteAbsolute(0x803C4C0C, []byte{0x03, 0xE7})
	if err != nil {
		fmt.Printf("Write failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Wrote 999 rupees!")

	// Read back
	time.Sleep(100 * time.Millisecond)
	after, _ := d.ReadAbsolute(0x803C4C0C, 2)
	fmt.Printf("Rupees after:  %02X%02X\n", after[0], after[1])
	fmt.Println("\nCheck your in-game rupee count!")
}

func runTeleportTest() {
	fmt.Println("=== Teleport Test: Holding Link in the air for 5 seconds ===")
	fmt.Println("Switch to Dolphin window NOW!")
	fmt.Println()

	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	defer d.Close()

	pos, err := d.ReadPlayerPosition()
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Original: X:%.1f Y:%.1f Z:%.1f\n", pos.PosX, pos.PosY, pos.PosZ)

	linkPtr, _ := d.GetLinkPtr()
	targetY := pos.PosY + 500.0

	// Write Y position every frame for 5 seconds
	fmt.Printf("Forcing Y=%.1f for 5 seconds...\n", targetY)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		yBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(yBytes, math.Float32bits(targetY))
		d.WriteAbsolute(linkPtr+0x1FC, yBytes)
		time.Sleep(16 * time.Millisecond) // ~60fps
	}

	fmt.Println("Released! Link should fall back down.")
}

func runHijackTest() {
	fmt.Println("=== Hijack Test: Trying multiple actors ===")
	fmt.Println("Watch the game — something should move!")
	fmt.Println()

	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	defer d.Close()

	// Try these actors from the scan (various distances/types)
	targets := []uint32{
		0x80A59BA4, // dist 218
		0x80A59DE4, // dist 301
		0x80A6A40C, // dist 999
		0x80A6D524, // dist 784
		0x80A6D7E0, // dist 666
		0x80A980E4, // dist 1284, group 3
		0x80A9A93C, // dist 273
		0x80A86A74, // dist 79
	}

	linkPtr, _ := d.GetLinkPtr()

	for i, target := range targets {
		// Save original position
		origPos, _ := d.ReadAbsolute(target+0x1F8, 12)
		if origPos == nil {
			continue
		}

		fmt.Printf("[%d/%d] Trying 0x%08X — moving 1000 units above Link for 3 sec...\n",
			i+1, len(targets), target)

		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			linkPos, _ := d.ReadAbsolute(linkPtr+0x1F8, 12)
			if linkPos == nil {
				continue
			}

			px := math.Float32frombits(binary.BigEndian.Uint32(linkPos[0:4])) + 300
			py := math.Float32frombits(binary.BigEndian.Uint32(linkPos[4:8])) + 500
			pz := math.Float32frombits(binary.BigEndian.Uint32(linkPos[8:12]))

			posBytes := make([]byte, 12)
			binary.BigEndian.PutUint32(posBytes[0:4], math.Float32bits(px))
			binary.BigEndian.PutUint32(posBytes[4:8], math.Float32bits(py))
			binary.BigEndian.PutUint32(posBytes[8:12], math.Float32bits(pz))
			// Write to ALL position fields: home(0x1D0), old(0x1E4), current(0x1F8)
			d.WriteAbsolute(target+0x1D0, posBytes)
			d.WriteAbsolute(target+0x1E4, posBytes)
			d.WriteAbsolute(target+0x1F8, posBytes)
			// Zero out speed so actor doesn't fight back
			zero := make([]byte, 12)
			d.WriteAbsolute(target+0x220, zero) // speed vector
			// Zero forward speed
			d.WriteAbsolute(target+0x254, zero[:4]) // speedF

			time.Sleep(16 * time.Millisecond)
		}

		// Restore
		d.WriteAbsolute(target+0x1F8, origPos)
		fmt.Println("  Restored. Did anything move? Moving to next...")
		time.Sleep(500 * time.Millisecond)
	}

	fmt.Println("\nDone testing all actors!")
}

func runNPCScan() {
	fmt.Println("=== NPC Scan: Finding visible actors near Link ===")
	fmt.Println()

	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	defer d.Close()

	linkPtr, _ := d.GetLinkPtr()
	linkPos, _ := d.ReadPlayerPosition()
	fmt.Printf("Link at 0x%08X: X:%.0f Y:%.0f Z:%.0f\n\n", linkPtr, linkPos.PosX, linkPos.PosY, linkPos.PosZ)
	fmt.Println("Scanning for actors with 3D models within 2000 units...")

	found := 0
	for addr := uint32(0x80400000); addr < 0x80E00000; addr += 0x4 {
		if addr == linkPtr {
			continue
		}

		// Check model pointer at +0x24C
		modelPtr, _ := d.ReadU32(addr + 0x24C)
		if modelPtr < 0x80000000 || modelPtr > 0x81800000 {
			continue
		}

		// Check actor group at +0x1BE
		groupData, _ := d.ReadAbsolute(addr+0x1BE, 1)
		if groupData == nil {
			continue
		}

		// Read position at +0x1F8
		posData, _ := d.ReadAbsolute(addr+0x1F8, 12)
		if posData == nil {
			continue
		}
		px := math.Float32frombits(binary.BigEndian.Uint32(posData[0:4]))
		py := math.Float32frombits(binary.BigEndian.Uint32(posData[4:8]))
		pz := math.Float32frombits(binary.BigEndian.Uint32(posData[8:12]))

		if math.IsNaN(float64(px)) || math.IsInf(float64(px), 0) {
			continue
		}

		dx := float64(px - linkPos.PosX)
		dy := float64(py - linkPos.PosY)
		dz := float64(pz - linkPos.PosZ)
		dist := math.Sqrt(dx*dx + dy*dy + dz*dz)

		if dist < 2000 {
			fmt.Printf("  0x%08X  group=%d  model=0x%08X  X:%9.0f Y:%6.0f Z:%9.0f  dist:%5.0f\n",
				addr, groupData[0], modelPtr, px, py, pz, dist)
			found++
		}
	}
	fmt.Printf("\nFound %d visible actors near Link\n", found)
}

func runActorScan() {
	fmt.Println("=== Actor Scan: Finding actors near Link ===")
	fmt.Println()

	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	defer d.Close()

	linkPos, err := d.ReadPlayerPosition()
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Link at: X:%.1f Y:%.1f Z:%.1f\n\n", linkPos.PosX, linkPos.PosY, linkPos.PosZ)

	// Check companion slots
	fmt.Println("Player pointer slots:")
	for i := 0; i < 3; i++ {
		ptr, _ := d.ReadU32(0x803CA754 + uint32(i*4))
		label := []string{"Link", "Companion", "Ship"}[i]
		if ptr != 0 {
			pos, _ := d.ReadAbsolute(ptr+0x1F8, 12)
			if pos != nil {
				px := math.Float32frombits(binary.BigEndian.Uint32(pos[0:4]))
				py := math.Float32frombits(binary.BigEndian.Uint32(pos[4:8]))
				pz := math.Float32frombits(binary.BigEndian.Uint32(pos[8:12]))
				fmt.Printf("  [%d] %-10s ptr=0x%08X  X:%.1f Y:%.1f Z:%.1f\n", i, label, ptr, px, py, pz)
			}
		} else {
			fmt.Printf("  [%d] %-10s (empty)\n", i, label)
		}
	}

	// Scan the actor list - in TWW, fopAcTg_Queue is iterated for all actors
	// Let's scan memory for actor-like structs near Link's position
	// Each actor has position at offset 0x1F8
	// We'll scan a range of memory looking for valid-looking position data
	fmt.Println("\nScanning for actors with valid positions...")
	linkPtr, _ := d.GetLinkPtr()

	// Read the actor's "next" pointer from the process list
	// fopAc_ac_c inherits from leafdraw_class which has process links
	// The process list node is at offset 0x00 in the base process class
	// Let's try reading nearby memory for actor pointers
	found := 0
	// Scan some known actor memory regions
	for addr := uint32(0x80400000); addr < 0x80E00000 && found < 20; addr += 0x10 {
		// Quick check: read 4 bytes, see if it looks like a valid actor
		// Actors have their position at +0x1F8
		// Check if there's a float-like value at what would be +0x1F8 from this base
		posData, err := d.ReadAbsolute(addr+0x1F8, 12)
		if err != nil {
			continue
		}
		px := math.Float32frombits(binary.BigEndian.Uint32(posData[0:4]))
		py := math.Float32frombits(binary.BigEndian.Uint32(posData[4:8]))
		pz := math.Float32frombits(binary.BigEndian.Uint32(posData[8:12]))

		// Filter: position should be in a reasonable range and not zero/NaN
		if px == 0 && py == 0 && pz == 0 {
			continue
		}
		if math.IsNaN(float64(px)) || math.IsNaN(float64(py)) || math.IsNaN(float64(pz)) {
			continue
		}
		if math.IsInf(float64(px), 0) || math.IsInf(float64(py), 0) || math.IsInf(float64(pz), 0) {
			continue
		}
		// Check reasonable world bounds
		if px < -500000 || px > 500000 || py < -10000 || py > 10000 || pz < -500000 || pz > 500000 {
			continue
		}

		// Skip if this is Link
		if addr == linkPtr {
			continue
		}

		// Looks like an actor! Calculate distance to Link
		dx := float64(px - linkPos.PosX)
		dy := float64(py - linkPos.PosY)
		dz := float64(pz - linkPos.PosZ)
		dist := math.Sqrt(dx*dx + dy*dy + dz*dz)

		if dist < 50000 { // Within reasonable distance
			fmt.Printf("  0x%08X  X:%10.1f Y:%8.1f Z:%10.1f  dist:%.0f\n", addr, px, py, pz, dist)
			found++
		}
	}
	if found == 0 {
		fmt.Println("  (no actors found nearby)")
	}
	fmt.Printf("\nFound %d potential actors\n", found)
}

// classifyPPC returns a short label + operand hint for a PPC instruction.
// Used by `disasm` to help pick safe hook sites — we care most about:
//   - `bl` instructions (opcode 18 with LK=1): safest to replace, caller already
//     assumes volatile reg clobber.
//   - `stwu r1, -N(r1)` and `stw rN, M(r1)` in a function's prologue: UNSAFE to
//     replace, losing them corrupts the stack frame / non-volatile reg saves.
//   - branches that would clobber control flow if replaced.
func classifyPPC(inst uint32) string {
	primary := inst >> 26
	switch primary {
	case 0:
		if inst == 0 {
			return "(zero)"
		}
		return "(illegal)"
	case 14:
		rt := (inst >> 21) & 0x1F
		ra := (inst >> 16) & 0x1F
		si := int16(inst & 0xFFFF)
		if ra == 0 {
			return fmt.Sprintf("li    r%d, %d", rt, si)
		}
		return fmt.Sprintf("addi  r%d, r%d, %d", rt, ra, si)
	case 15:
		rt := (inst >> 21) & 0x1F
		ra := (inst >> 16) & 0x1F
		si := inst & 0xFFFF
		if ra == 0 {
			return fmt.Sprintf("lis   r%d, 0x%04X", rt, si)
		}
		return fmt.Sprintf("addis r%d, r%d, 0x%04X", rt, ra, si)
	case 16:
		if inst&1 != 0 {
			return "bcl"
		}
		return "bc"
	case 18:
		disp := inst & 0x03FFFFFC
		if disp&0x02000000 != 0 {
			disp |= 0xFC000000
		}
		if inst&1 != 0 {
			return fmt.Sprintf("bl    +0x%X", int32(disp))
		}
		return fmt.Sprintf("b     +0x%X", int32(disp))
	case 19:
		switch inst & 0x07FF {
		case 0x020:
			return "blr"
		case 0x021:
			return "blrl"
		case 0x420:
			return "bctr"
		case 0x421:
			return "bctrl"
		}
		return "branch-reg"
	case 31:
		xo := (inst >> 1) & 0x3FF
		switch xo {
		case 339:
			return "mfspr"
		case 467:
			return "mtspr"
		case 444:
			rs := (inst >> 21) & 0x1F
			ra := (inst >> 16) & 0x1F
			rb := (inst >> 11) & 0x1F
			if rs == rb {
				return fmt.Sprintf("mr    r%d, r%d", ra, rs)
			}
			return "or"
		}
		return fmt.Sprintf("X-form xo=%d", xo)
	case 32:
		rt := (inst >> 21) & 0x1F
		ra := (inst >> 16) & 0x1F
		d := int16(inst & 0xFFFF)
		return fmt.Sprintf("lwz   r%d, %d(r%d)", rt, d, ra)
	case 36:
		rs := (inst >> 21) & 0x1F
		ra := (inst >> 16) & 0x1F
		d := int16(inst & 0xFFFF)
		return fmt.Sprintf("stw   r%d, %d(r%d)", rs, d, ra)
	case 37:
		rs := (inst >> 21) & 0x1F
		ra := (inst >> 16) & 0x1F
		d := int16(inst & 0xFFFF)
		if rs == 1 && ra == 1 {
			return fmt.Sprintf("stwu  r1, %d(r1)  <PROLOGUE>", d)
		}
		return fmt.Sprintf("stwu  r%d, %d(r%d)", rs, d, ra)
	case 40:
		return "lhz"
	case 44:
		return "sth"
	case 48:
		return "lfs"
	case 52:
		return "stfs"
	}
	return fmt.Sprintf("op=%d", primary)
}

func runDisasm(addr uint32, count int) {
	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()

	data, err := d.ReadAbsolute(addr, count*4)
	if err != nil {
		fmt.Printf("read failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Disasm %d instructions from 0x%08X:\n", count, addr)
	for i := 0; i < count; i++ {
		inst := binary.BigEndian.Uint32(data[i*4:])
		fmt.Printf("  0x%08X: %08X  %s\n", addr+uint32(i*4), inst, classifyPPC(inst))
	}
}

// Write a target position into slot `slot` of the mailbox (default 0).
// The frame hook reads the slot each frame and writes into the
// corresponding puppet actor's pos. One-shot write.
func runMovePuppet(xs, ys, zs string, slot int) {
	if slot < 0 || slot >= maxPuppets {
		fmt.Printf("slot %d out of range [0, %d)\n", slot, maxPuppets)
		os.Exit(1)
	}
	parseF32 := func(s string) float32 {
		v, err := strconv.ParseFloat(s, 32)
		if err != nil {
			fmt.Printf("bad float %q: %v\n", s, err)
			os.Exit(1)
		}
		return float32(v)
	}
	x, y, z := parseF32(xs), parseF32(ys), parseF32(zs)

	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()

	buf := make([]byte, 12)
	binary.BigEndian.PutUint32(buf[0:4], math.Float32bits(x))
	binary.BigEndian.PutUint32(buf[4:8], math.Float32bits(y))
	binary.BigEndian.PutUint32(buf[8:12], math.Float32bits(z))
	if err := d.WriteAbsolute(slotAddr(slot, slotOffPosX), buf); err != nil {
		fmt.Printf("write failed: %v\n", err)
		os.Exit(1)
	}
	// Ensure the slot is marked active so the C hook syncs it.
	one := make([]byte, 4)
	binary.BigEndian.PutUint32(one, 1)
	d.WriteAbsolute(slotAddr(slot, slotOffAct), one)
	fmt.Printf("slot %d target: X=%.1f Y=%.1f Z=%.1f\n", slot, x, y, z)
}

// Reads local Link's position from Dolphin every 50ms and forwards it to the
// server. Pair with puppet-sync on a second host (or same host) to see Link's
// movements mirrored to a puppet actor, proving the full network round-trip.
func runBroadcastLink(name, addr string) {
	fmt.Printf("=== Broadcast Link: %s -> %s ===\n\n", name, addr)

	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	defer d.Close()

	client := network.NewClient(name)
	client.OnLog = func(msg string) { fmt.Printf("[net] %s\n", msg) }
	if err := client.Connect(addr); err != nil {
		fmt.Printf("connect: %v\n", err)
		os.Exit(1)
	}
	defer client.Disconnect()

	for client.IsConnected() {
		pos, err := d.ReadPlayerPosition()
		if err == nil && pos != nil {
			netPos := &network.PlayerPosition{
				PosX: pos.PosX,
				PosY: pos.PosY,
				PosZ: pos.PosZ,
				RotY: pos.RotY,
			}
			if err := client.SendPosition(netPos); err != nil {
				fmt.Printf("send: %v\n", err)
				break
			}
			fmt.Printf("  link -> X:%10.1f Y:%8.1f Z:%10.1f\r", pos.PosX, pos.PosY, pos.PosZ)
		}
		time.Sleep(50 * time.Millisecond)
	}
	fmt.Println("\nDisconnected.")
}

// Mtx layout in pose data is row-major f32[3][4] big-endian (PowerPC native):
//   bytes  0..15 = row 0 (m00, m01, m02, m03)   m03 = X translation
//   bytes 16..31 = row 1 (m10, m11, m12, m13)   m13 = Y translation
//   bytes 32..47 = row 2 (m20, m21, m22, m23)   m23 = Z translation
// Stride = 48 B per joint.
const (
	mtxStride        = 48
	mtxOffTransX     = 12
	mtxOffTransY     = 28
	mtxOffTransZ     = 44
)

func readBEFloat(b []byte, off int) float32 {
	return math.Float32frombits(binary.BigEndian.Uint32(b[off : off+4]))
}
func writeBEFloat(b []byte, off int, v float32) {
	binary.BigEndian.PutUint32(b[off:off+4], math.Float32bits(v))
}

// localizePoseInPlace subtracts (dx, dy, dz) from every joint's translation
// column, turning a world-space mpNodeMtx blob into a pose relative to the
// sender's origin. Rotation parts are untouched.
func localizePoseInPlace(buf []byte, joints int, dx, dy, dz float32) {
	for j := 0; j < joints; j++ {
		base := j * mtxStride
		writeBEFloat(buf, base+mtxOffTransX, readBEFloat(buf, base+mtxOffTransX)-dx)
		writeBEFloat(buf, base+mtxOffTransY, readBEFloat(buf, base+mtxOffTransY)-dy)
		writeBEFloat(buf, base+mtxOffTransZ, readBEFloat(buf, base+mtxOffTransZ)-dz)
	}
}

// applyPoseAt re-adds (tx, ty, tz) to every joint's translation column.
// Inverse of localizePoseInPlace; used by the receiver to land the
// localized pose at any chosen world position.
func applyPoseAt(buf []byte, joints int, tx, ty, tz float32) {
	for j := 0; j < joints; j++ {
		base := j * mtxStride
		writeBEFloat(buf, base+mtxOffTransX, readBEFloat(buf, base+mtxOffTransX)+tx)
		writeBEFloat(buf, base+mtxOffTransY, readBEFloat(buf, base+mtxOffTransY)+ty)
		writeBEFloat(buf, base+mtxOffTransZ, readBEFloat(buf, base+mtxOffTransZ)+tz)
	}
}

// lerpPoseInPlace closes `dst` toward `target` by fraction k per call,
// element-wise across every BE f32 in the joint matrix blob (12 floats
// per joint × N joints). Acts as a per-frame EMA: a steady stream of
// fresh `target` poses converges `dst` toward each one over a few
// frames, smoothing out any jitter between sender and receiver clocks.
// Rotation columns aren't strictly orthonormal during the transition
// but per-frame angular delta is small enough that visible vertex
// drift is bounded; full quaternion slerp can replace this if needed.
func lerpPoseInPlace(dst, target []byte, k float32) {
	if len(dst) != len(target) {
		return
	}
	n := len(dst) / 4
	for i := 0; i < n; i++ {
		off := i * 4
		dv := readBEFloat(dst, off)
		tv := readBEFloat(target, off)
		writeBEFloat(dst, off, dv+(tv-dv)*k)
	}
}

// Same as broadcast-link but ALSO sends Link's full skeletal pose every
// tick. The receiver's puppet-sync writes that pose into mailbox.pose_buf
// and flips shadow_mode=5 so Link #2 animates from the wire instead of
// mirroring locally. Link's J3DModel is at daPy_lk_c + 0x032C; mpNodeMtx
// is at J3DModel + 0x8C; sizeof(Mtx) = 48; Link has 42 joints.
//
// Standalone-CLI wrapper. Preserves the `ww-multiplayer.exe broadcast-pose` entry
// point that scripts/mplay2.sh relies on. Installs the same SIGINT handler
// as host/join so Ctrl+C exits cleanly (the broadcast side doesn't touch
// shadow_mode so there's nothing to clean up — the signal handler just
// gets us out of the send tick immediately instead of on the next tick).
func runBroadcastPose(name, addr string) {
	rep := report.Stdout{}
	ctx, cancel := cliMultiplayerContext(rep)
	defer cancel()
	if err := runBroadcastPoseCtx(ctx, name, addr, 0, rep); err != nil {
		rep.Log(report.Err, err.Error())
		os.Exit(1)
	}
}

// runBroadcastPoseCtx is the goroutine-friendly variant. Returns an error
// instead of os.Exit so host/join can surface failures cleanly, and honors
// ctx cancellation so SIGINT doesn't have to wait for the next 50 ms tick.
func runBroadcastPoseCtx(ctx context.Context, name, addr string, dolphinPID uint32, rep report.Reporter) error {
	report.Logf(rep, report.Info, "=== Broadcast Link + Pose: %s -> %s ===", name, addr)
	d, err := openDolphin(dolphinPID)
	if err != nil {
		return err
	}
	defer d.Close()
	client := network.NewClient(name)
	client.OnLog = func(msg string) { rep.Log(report.Net, msg) }
	if err := client.Connect(addr); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer client.Disconnect()

	// Wrap ctx in a cancellable child so the watcher goroutine below exits
	// when this function returns (prevents a leak when the parent passed
	// context.Background()).
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Break out of the IsConnected() loop on ctx cancel by closing the
	// socket — IsConnected() flips false and the main loop exits next tick.
	go func() {
		<-ctx.Done()
		client.Disconnect()
	}()

	const linkJointCount = 42
	const poseBytes = linkJointCount * 48

	posErrs, poseErrs := 0, 0
	for client.IsConnected() {
		// Position (cheap; same as broadcast-link).
		pos, err := d.ReadPlayerPosition()
		if err == nil && pos != nil {
			netPos := &network.PlayerPosition{
				PosX: pos.PosX, PosY: pos.PosY, PosZ: pos.PosZ,
				RotY: pos.RotY,
			}
			if err := client.SendPosition(netPos); err != nil {
				posErrs++
				if posErrs > 5 {
					return fmt.Errorf("send position: %w", err)
				}
			}
		}

		// Pose. Read from the sender-side publish buffer rather than
		// Link #1's live mpNodeMtx. The C draw hook memcpys mpNodeMtx
		// into this GameHeap-resident buffer once per frame AFTER
		// daPy_lk_c_draw returns, so our read can't catch mid-calc
		// torn state. Previous direct-mpNodeMtx reads at 20 Hz raced
		// the game's 60 Hz calc pass and produced visibly wrong poses
		// on the receiver when slope-IK made per-frame mpNodeMtx delta
		// large (observed v0.1.2: leg flapping 0-90° on slopes).
		//
		// Protocol: ship the raw 2016 B AFTER localizing — subtract
		// Link's world position from each joint's translation column so
		// the pose is relative to Link's origin (rotation parts
		// unchanged). Receiver re-adds the remote's world position to
		// land Link #2 at the right world coords.
		if pos != nil {
			stateBytes, _ := d.ReadAbsolute(mailboxBase+mailboxPosePublishState, 1)
			if len(stateBytes) == 1 && stateBytes[0] == 1 {
				pubPtr, _ := d.ReadU32(mailboxBase + mailboxPosePublishPtr)
				if pubPtr >= 0x80000000 && pubPtr < 0x81800000 {
					data, err := d.ReadAbsolute(pubPtr, poseBytes)
					if err == nil && data != nil {
						if os.Getenv("WW_POSE_RAW") == "" {
							localizePoseInPlace(data, linkJointCount, pos.PosX, pos.PosY, pos.PosZ)
						}
						if err := client.SendPose(linkJointCount, data); err != nil {
							poseErrs++
							if poseErrs > 5 {
								return fmt.Errorf("send pose: %w", err)
							}
						}
					}
				}
			}
		}

		// pos can be nil during a brief reload window where Link's
		// actor pointer is 0 (player on the main menu / save loading).
		// We used to print a per-tick `link+pose -> X:Y:Z\r` heartbeat
		// here; the TUI dashboard now polls Dolphin position directly
		// for that field, so the heartbeat just spammed the log panel.
		// CLI users still see the [net] connect line as proof of life.

		// 16 ms ≈ 60 Hz, matching Dolphin's render rate so the receiver
		// gets a fresh pose every game frame. ~120 KB/s of pose data —
		// trivial for LAN; fine for typical home upload over the
		// internet too. Was 50 ms before v0.1.7 — the 3-frame cadence
		// mismatch made remote Links visibly snap between samples.
		select {
		case <-ctx.Done():
			// Fall through; IsConnected() will be false next iteration
			// (watcher goroutine already called Disconnect()).
		case <-time.After(16 * time.Millisecond):
		}
	}
	rep.Log(report.Info, "Disconnected.")
	return nil
}

// Captures Link's current pose once and replays it as a separate
// "fake remote" so loopback testing can demonstrate multi-Link
// rendering without a second Dolphin. Position is sent as the live
// Link position + 1000 X offset so the fake walks alongside the real
// player, frozen in the captured pose. Receiver sees this as a remote
// distinct from broadcast-pose's player and assigns it a new link slot.
func runPoseFakeLoop(name, addr string) {
	fmt.Printf("=== Fake Pose Broadcaster: %s -> %s ===\n\n", name, addr)
	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	defer d.Close()
	client := network.NewClient(name)
	client.OnLog = func(msg string) { fmt.Printf("[net] %s\n", msg) }
	if err := client.Connect(addr); err != nil {
		fmt.Printf("connect: %v\n", err)
		os.Exit(1)
	}
	defer client.Disconnect()

	const linkMpCLModelOff = 0x032C
	const j3dModelMpNodeMtxOff = 0x8C
	const linkJointCount = 42
	const poseBytes = linkJointCount * 48

	// One-shot capture: read Link's current world pose, then localize.
	var captured []byte
	for try := 0; try < 30 && captured == nil; try++ {
		linkPtr, err := d.GetLinkPtr()
		if err != nil || linkPtr == 0 {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		modelPtr, _ := d.ReadU32(linkPtr + linkMpCLModelOff)
		if modelPtr < 0x80000000 || modelPtr >= 0x81800000 {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		nodePtr, _ := d.ReadU32(modelPtr + j3dModelMpNodeMtxOff)
		if nodePtr < 0x80000000 || nodePtr >= 0x81800000 {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		data, err := d.ReadAbsolute(nodePtr, poseBytes)
		if err != nil || data == nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		pos, err := d.ReadPlayerPosition()
		if err != nil || pos == nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		localizePoseInPlace(data, linkJointCount, pos.PosX, pos.PosY, pos.PosZ)
		captured = data
		fmt.Printf("captured fake pose at (%.0f, %.0f, %.0f)\n", pos.PosX, pos.PosY, pos.PosZ)
	}
	if captured == nil {
		fmt.Println("could not capture initial pose; is Link loaded?")
		os.Exit(1)
	}

	// Stream loop: position tracks live Link with +1000 X so the fake
	// walks beside the real player. Pose stays frozen at the capture.
	for client.IsConnected() {
		pos, err := d.ReadPlayerPosition()
		if err == nil && pos != nil {
			netPos := &network.PlayerPosition{
				PosX: pos.PosX + 1000,
				PosY: pos.PosY,
				PosZ: pos.PosZ,
				RotY: pos.RotY,
			}
			if err := client.SendPosition(netPos); err != nil {
				fmt.Printf("send position: %v\n", err)
				break
			}
			if err := client.SendPose(linkJointCount, captured); err != nil {
				fmt.Printf("send pose: %v\n", err)
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	fmt.Println("\nDisconnected.")
}

// Mailbox layout (keep in sync with inject/include/mailbox.h).
const (
	mailboxBase    = 0x80412F00
	maxPuppets     = 4
	puppetSlotBase = mailboxBase + 0x10
	puppetSlotSize = 0x20
)

// Byte offsets inside a slot.
const (
	slotOffActor = 0x00
	slotOffAct   = 0x04
	slotOffPosX  = 0x08
	slotOffPosY  = 0x0C
	slotOffPosZ  = 0x10
	slotOffRotX  = 0x14 // followed by rotY (+0x16), rotZ (+0x18)
)

func slotAddr(i int, off uint32) uint32 {
	return puppetSlotBase + uint32(i)*puppetSlotSize + off
}

// envFloat32 reads an environment variable as a float32, returning the
// fallback if unset or unparseable. Used for tunables that need to be
// changed per-launch (offsets, throttles) without a recompile.
func envFloat32(key string, fallback float32) float32 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 32)
	if err != nil {
		fmt.Printf("warn: bad %s=%q (%v); using %g\n", key, v, err, fallback)
		return fallback
	}
	return float32(f)
}

// Iterates every active puppet slot and applies the proc-specific unhide
// poke. Idempotent and safe to call whenever a puppet appears to be hidden.
func runUnhidePuppet() {
	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()

	any := false
	for i := 0; i < maxPuppets; i++ {
		active, _ := d.ReadU32(slotAddr(i, slotOffAct))
		if active != 1 {
			continue
		}
		ptr, err := d.ReadU32(slotAddr(i, slotOffActor))
		if err != nil || ptr < 0x80000000 || ptr >= 0x81800000 {
			continue // actor not yet resolved by C; skip this tick
		}
		procWord, err := d.ReadU32(ptr + 0x08)
		if err != nil {
			continue
		}
		proc := procWord >> 16
		buf := make([]byte, 4)
		switch proc {
		case 0x01CB: // TSUBO: m678 = 2
			binary.BigEndian.PutUint32(buf, 2)
			d.WriteAbsolute(ptr+0x678, buf)
			fmt.Printf("slot %d: unhid TSUBO (actor 0x%08X) m678=2\n", i, ptr)
			any = true
		case 0x00C3: // KAMOME: clear mSwitchNo at +0x2AA (u32 write at +0x2A8 zeroes 4 safe bytes)
			binary.BigEndian.PutUint32(buf, 0)
			d.WriteAbsolute(ptr+0x2A8, buf)
			fmt.Printf("slot %d: unhid KAMOME (actor 0x%08X) mSwitchNo=0\n", i, ptr)
			any = true
		default:
			fmt.Printf("slot %d: unknown proc 0x%04X at 0x%08X\n", i, proc, ptr)
		}
	}
	if !any {
		fmt.Println("No active puppet slots resolved yet — is the game past the 10s spawn gate and has puppet-sync populated a slot?")
		os.Exit(1)
	}
}

// Connects to a server, subscribes to remote position broadcasts, and writes
// the most recent remote player's position into the Dolphin mailbox every
// frame. The C-side frame hook then mirrors mailbox.p2_pos into the puppet
// actor's pos each frame. End-to-end loop:
//   remote player -> server -> puppet-sync -> mailbox -> C hook -> actor pos
//
// Standalone-CLI wrapper. Honors WW_SELF_NAME (kept for mplay2.sh); host/
// join call runPuppetSyncCtx directly with an explicit filter name. Installs
// the same SIGINT handler as host/join so mplay2.sh's Ctrl+C path resets
// the mailbox (shadow_mode=0 + pose_seqs[*]=0) instead of leaving Link #2
// frozen at the last received pose.
func runPuppetSync(name, addr string) {
	rep := report.Stdout{}
	ctx, cancel := cliMultiplayerContext(rep)
	defer cancel()
	err := runPuppetSyncCtx(ctx, name, addr, os.Getenv("WW_SELF_NAME"), 0, rep)
	clearMultiplayerState()
	if err != nil {
		rep.Log(report.Err, err.Error())
		os.Exit(1)
	}
}

// runPuppetSyncCtx is the goroutine-friendly variant. Takes the self-filter
// name as a parameter (rather than reading WW_SELF_NAME) so host/join can
// plumb the player's name through without exporting an env var.
func runPuppetSyncCtx(ctx context.Context, name, addr, selfFilter string, dolphinPID uint32, rep report.Reporter) error {
	report.Logf(rep, report.Info, "=== Puppet Sync: %s <- %s ===", name, addr)

	d, err := openDolphin(dolphinPID)
	if err != nil {
		return err
	}
	defer d.Close()
	rep.Log(report.OK, "Dolphin found.")

	client := network.NewClient(name)
	client.OnLog = func(msg string) {
		rep.Log(report.Net, msg)
	}
	if err := client.Connect(addr); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer client.Disconnect()

	// Wrap ctx so the watcher goroutine below exits cleanly on return,
	// then kick the IsConnected() loop out of its sleep on ctx cancel.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		client.Disconnect()
	}()

	// Lerp-smoothed state per slot. lerpK = 0.2 closes ~80% of the gap in
	// ~5 ticks (~83 ms at 60 Hz). Raise for snappier tracking, lower for
	// more butter. Rotation is raw passthrough — angular lerp needs
	// shortest-arc handling; punt until it matters visibly.
	const lerpK = 0.2
	type slotState struct {
		haveCur          bool
		curX, curY, curZ float32
	}
	var slots [maxPuppets]slotState
	remoteToSlot := map[byte]int{}
	zero := make([]byte, 4)
	one := make([]byte, 4)
	binary.BigEndian.PutUint32(one, 1)

	// Pose-feed state. We can drive up to maxRemoteLinks Link puppets
	// from incoming remote poses. First remote with pose claims Link
	// slot 0, second claims slot 1, etc. Beyond that, additional
	// remotes still get their actor-puppet (KAMOME / NPC / TSUBO).
	remoteToLinkSlot := map[byte]int{}
	poseBufPtrs := [maxRemoteLinks]uint32{}
	shadowModeArmed := false
	announced := map[byte]bool{} // log "link slot := player N" once

	// Per-link-slot displayed pose. Each tick we lerp this toward the
	// latest received pose for the slot's remote (poseLerpK fraction
	// per tick). Even at 60 Hz send, TCP arrival is bursty, so a hard
	// snap to the latest pose looks visibly jittery; EMA-style smoothing
	// soaks the jitter while staying responsive (k=0.5 closes ~94% of
	// the gap in 4 frames ≈ 66 ms). Allocated lazily on first pose so
	// we don't pre-commit a buffer size before knowing the joint count.
	const poseLerpK float32 = 0.5
	type linkSlotState struct {
		displayed []byte
	}
	var linkSlots [maxRemoteLinks]linkSlotState
	// Render offset added on top of the localized pose's re-application.
	// Default 0 (real multiplayer renders Link #2 at remote's actual
	// world coords). Set WW_LINK2_OFFSET_X / _Y / _Z for loopback demos
	// so Link #2 doesn't overlap your own Link.
	link2OffsetX := envFloat32("WW_LINK2_OFFSET_X", 0)
	link2OffsetY := envFloat32("WW_LINK2_OFFSET_Y", 0)
	link2OffsetZ := envFloat32("WW_LINK2_OFFSET_Z", 0)
	if link2OffsetX != 0 || link2OffsetY != 0 || link2OffsetZ != 0 {
		report.Logf(rep, report.Info, "Link #2/3 render offset: (%.0f, %.0f, %.0f)",
			link2OffsetX, link2OffsetY, link2OffsetZ)
	}
	armPoseSlot := func(slot int) bool {
		// Re-poll each call: the receiving Dolphin's mini-Link state can
		// reset when the player reloads a save (mini_link_reset_state in
		// inject/src/multiplayer.c clears mailbox.pose_buf_ptrs and
		// pose_buf_states, then the next mode-5 draw lazy-allocs a NEW
		// pose_buf at a possibly-different address). If we cached the
		// old pointer, we'd write fresh poses into freed memory while
		// the new mini-Link reads only its seed, producing a Link that
		// stays frozen at the seed pose forever. Authoritative source
		// is the mailbox; use it every tick instead of caching once.
		state, _ := d.ReadAbsolute(mailboxBase+mailboxPoseBufState(slot), 1)
		if len(state) == 1 && state[0] == 1 {
			ptr, _ := d.ReadU32(mailboxBase + mailboxPoseBufPtr(slot))
			if ptr != 0 {
				if poseBufPtrs[slot] != ptr {
					if poseBufPtrs[slot] == 0 {
						report.Logf(rep, report.OK, "pose-feed slot %d armed: pose_buf=0x%08X", slot, ptr)
					} else {
						report.Logf(rep, report.OK, "pose-feed slot %d re-armed: pose_buf=0x%08X (was 0x%08X)",
							slot, ptr, poseBufPtrs[slot])
					}
					poseBufPtrs[slot] = ptr
				}
				return true
			}
		}
		// State != 1 — C side hasn't allocated yet (first time this slot
		// is touched, or just reset). Drop our cached pointer so we don't
		// write to a stale address while waiting, then drive shadow_mode=5
		// and poll for the new pose_buf.
		if poseBufPtrs[slot] != 0 {
			report.Logf(rep, report.Warn, "pose-feed slot %d disarmed: waiting for C-side re-alloc", slot)
			poseBufPtrs[slot] = 0
		}
		if !shadowModeArmed {
			d.WriteAbsolute(mailboxBase+mailboxShadowMode, []byte{5})
			shadowModeArmed = true
		}
		for i := 0; i < 60; i++ {
			select {
			case <-ctx.Done():
				return false
			case <-time.After(50 * time.Millisecond):
			}
			state, _ := d.ReadAbsolute(mailboxBase+mailboxPoseBufState(slot), 1)
			if len(state) == 1 && state[0] == 1 {
				ptr, _ := d.ReadU32(mailboxBase + mailboxPoseBufPtr(slot))
				if ptr != 0 {
					poseBufPtrs[slot] = ptr
					report.Logf(rep, report.OK, "pose-feed slot %d armed: pose_buf=0x%08X", slot, ptr)
					return true
				}
			}
			if len(state) == 1 && (state[0] == 0xFD || state[0] == 0xFE) {
				report.Logf(rep, report.Err, "pose-feed slot %d alloc failed (state=0x%02X)", slot, state[0])
				return false
			}
		}
		return false
	}
	// Returns the link slot assigned to this remote, or -1 if no slot
	// is available (all maxRemoteLinks already taken by other remotes).
	pickLinkSlot := func(remoteID byte) int {
		if s, ok := remoteToLinkSlot[remoteID]; ok {
			return s
		}
		used := map[int]bool{}
		for _, s := range remoteToLinkSlot {
			used[s] = true
		}
		for s := 0; s < maxRemoteLinks; s++ {
			if !used[s] {
				remoteToLinkSlot[remoteID] = s
				return s
			}
		}
		return -1
	}

	// Puppet-sync only renders network poses if shadow_mode == 5 on the
	// receiving Dolphin. Old armPoseSlot only wrote shadow_mode=5 in its
	// lazy-arm path (when state != 1), so if state was already 1 from a
	// prior puppet-sync run AND shadow_mode had drifted back to 0 (e.g.
	// from a manual `./ww-multiplayer.exe shadow-mode 0`, or any future reset path
	// that clears it), the new puppet-sync would silently fail with the
	// receiver showing local-mirror Link instead of the network pose.
	// Always assert mode 5 once at startup; cheap and idempotent.
	d.WriteAbsolute(mailboxBase+mailboxShadowMode, []byte{5})
	shadowModeArmed = true

	// selfFilter lets a puppet-sync attached to the SAME Dolphin as a
	// broadcast-pose twin ignore its twin's stream. Without this, the
	// twin's pose (= our local Link's live position) gets written into
	// a puppet actor that then physics-collides with our own Link. Empty
	// (default) keeps the loopback "mirror yourself with offset" demo
	// working. mplay2.sh still works because the CLI wrapper above reads
	// WW_SELF_NAME into this arg; `ww-multiplayer.exe host/join` pass the player name
	// so users never have to know the env var exists.
	if selfFilter != "" {
		report.Logf(rep, report.Info, "Filtering self-echo: remotes named %q will be ignored.", selfFilter)
	}

	for client.IsConnected() {
		remotes := client.GetRemotePlayers()
		seen := map[byte]bool{}
		for _, rp := range remotes {
			if rp.Position == nil {
				continue
			}
			if selfFilter != "" && rp.Name == selfFilter {
				continue
			}
			seen[rp.ID] = true
			hasPose := rp.PoseMatrices != nil && rp.PoseJoints > 0 &&
				len(rp.PoseMatrices) == rp.PoseJoints*48
			idx, ok := remoteToSlot[rp.ID]
			if !ok {
				// Find a free slot. Walk 0..N; first index not already
				// mapped to a live remote wins.
				used := map[int]bool{}
				for _, v := range remoteToSlot {
					used[v] = true
				}
				for i := 0; i < maxPuppets; i++ {
					if !used[i] {
						idx = i
						break
					}
				}
				if used[idx] {
					// All slots full; drop this remote for now.
					continue
				}
				remoteToSlot[rp.ID] = idx
				// Only activate the actor-puppet (KAMOME / Rose / TSUBO)
				// for remotes without pose data. Pose-driven remotes
				// render as Link #2 directly; activating the actor too
				// would overlap an NPC on top of Link #2 at the same
				// coords, and C-side actor cleanup is best-effort (it
				// stops syncing but leaves the actor stuck at its last
				// position), so the duplicate sticks around forever.
				if !hasPose {
					d.WriteAbsolute(slotAddr(idx, slotOffAct), one)
					report.Logf(rep, report.OK, "slot %d := player %d (%s)", idx, rp.ID, rp.Name)
				}
			}

			tx, ty, tz := rp.Position.PosX, rp.Position.PosY, rp.Position.PosZ
			st := &slots[idx]
			if !st.haveCur {
				st.curX, st.curY, st.curZ = tx, ty, tz
				st.haveCur = true
			} else {
				st.curX += (tx - st.curX) * lerpK
				st.curY += (ty - st.curY) * lerpK
				st.curZ += (tz - st.curZ) * lerpK
			}

			posBuf := make([]byte, 12)
			binary.BigEndian.PutUint32(posBuf[0:4], math.Float32bits(st.curX))
			binary.BigEndian.PutUint32(posBuf[4:8], math.Float32bits(st.curY))
			binary.BigEndian.PutUint32(posBuf[8:12], math.Float32bits(st.curZ))
			d.WriteAbsolute(slotAddr(idx, slotOffPosX), posBuf)

			rotBuf := make([]byte, 6)
			binary.BigEndian.PutUint16(rotBuf[0:2], uint16(rp.Position.RotX))
			binary.BigEndian.PutUint16(rotBuf[2:4], uint16(rp.Position.RotY))
			binary.BigEndian.PutUint16(rotBuf[4:6], uint16(rp.Position.RotZ))
			d.WriteAbsolute(slotAddr(idx, slotOffRotX), rotBuf)

			// Pose feed (shadow_mode=5). First remote to deliver any pose
			// claims the Link-#2 driver slot; subsequent remotes still
			// puppet through the actor pipeline above. MAX_REMOTE_LINKS=1
			// for v0. Sender ships a LOCALIZED pose (translations
			// relative to its own world position); receiver re-adds
			// the smoothed remote position + WW_LINK2_OFFSET so Link #2
			// lands at the right world coords. The offset is normally 0
			// for two-Dolphin play; set it to e.g. 500 for loopback
			// development so Link #2 doesn't overlap your own Link.
			if rp.PoseMatrices != nil && len(rp.PoseMatrices) == rp.PoseJoints*48 {
				linkSlot := pickLinkSlot(rp.ID)
				if linkSlot >= 0 && armPoseSlot(linkSlot) {
					// First time this remote got a link slot — also
					// release their actor-puppet so we don't see both
					// a Link AND a KAMOME at the same position.
					d.WriteAbsolute(slotAddr(idx, slotOffAct), zero)
					if _, alreadyLogged := announced[rp.ID]; !alreadyLogged {
						report.Logf(rep, report.OK, "link slot %d := player %d (%s)", linkSlot, rp.ID, rp.Name)
						announced[rp.ID] = true
					}
					ls := &linkSlots[linkSlot]
					if ls.displayed == nil || len(ls.displayed) != len(rp.PoseMatrices) {
						// First pose for this slot, or joint count
						// changed — snap to the target so we don't
						// EMA from zero (which would render a degenerate
						// pose for the first few frames).
						ls.displayed = make([]byte, len(rp.PoseMatrices))
						copy(ls.displayed, rp.PoseMatrices)
					} else {
						lerpPoseInPlace(ls.displayed, rp.PoseMatrices, poseLerpK)
					}
					adjusted := make([]byte, len(ls.displayed))
					copy(adjusted, ls.displayed)
					if os.Getenv("WW_POSE_RAW") == "" {
						applyPoseAt(adjusted, rp.PoseJoints,
							st.curX+link2OffsetX, st.curY+link2OffsetY, st.curZ+link2OffsetZ)
					}
					d.WriteAbsolute(poseBufPtrs[linkSlot], adjusted)
					sq, _ := d.ReadAbsolute(mailboxBase+mailboxPoseSeq(linkSlot), 1)
					next := byte(0)
					if len(sq) == 1 {
						next = sq[0] + 1
					}
					d.WriteAbsolute(mailboxBase+mailboxPoseSeq(linkSlot), []byte{next})
				}
			}
		}

		// Release slots for remotes that have disconnected.
		for id, idx := range remoteToSlot {
			if !seen[id] {
				d.WriteAbsolute(slotAddr(idx, slotOffAct), zero)
				delete(remoteToSlot, id)
				slots[idx] = slotState{}
				report.Logf(rep, report.Info, "slot %d freed (player %d left)", idx, id)
				if linkSlot, ok := remoteToLinkSlot[id]; ok {
					delete(remoteToLinkSlot, id)
					// Clear pose_seq so the C-side stops rendering this
					// link slot. C's mode-5 path gates entry on
					// pose_seqs[slot] != 0, so without this the
					// receiving Dolphin would render Link #2 frozen
					// at the last received pose forever after the
					// remote disconnects.
					d.WriteAbsolute(mailboxBase+mailboxPoseSeq(linkSlot), []byte{0})
					// Drop the EMA buffer so the next remote to claim
					// this slot snaps to its first pose instead of
					// lerping from the previous occupant's last frame.
					linkSlots[linkSlot] = linkSlotState{}
					report.Logf(rep, report.Info, "link slot %d freed (will be reassigned on next pose)", linkSlot)
				}
				delete(announced, id)
			}
		}

		select {
		case <-ctx.Done():
			// Watcher goroutine will Disconnect(); IsConnected() will
			// flip false on the next iteration and the loop exits.
		case <-time.After(16 * time.Millisecond): // ~60 Hz
		}
	}
	rep.Log(report.Info, "Disconnected.")
	return nil
}

func runPokeU32(addrHex, valHex string) {
	parseHex := func(s string) uint32 {
		v, err := strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 32)
		if err != nil {
			fmt.Printf("bad hex %q: %v\n", s, err)
			os.Exit(1)
		}
		return uint32(v)
	}
	addr := parseHex(addrHex)
	val := parseHex(valHex)
	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, val)
	if err := d.WriteAbsolute(addr, buf); err != nil {
		fmt.Printf("write failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Wrote 0x%08X to 0x%08X\n", val, addr)
}

// mailbox.shadow_mode lives at +0x90; shadow_latched at +0x91.
// See inject/include/mailbox.h and docs/06 "Next Session Priority" step 1.
const mailboxShadowMode = 0x90
const mailboxShadowLatched = 0x91
const mailboxDbgJointNum = 0x9C
const mailboxDbgNodeMtxPtr = 0xA0
const mailboxEchoDelay = 0xA4
const mailboxEchoRingState = 0xA5
// Pose-feed slot fields are arrays sized maxRemoteLinks. Layout matches
// the structs in inject/include/mailbox.h:
//   pose_buf_ptrs    [N]u32  @ 0xA8 (4 B/slot)
//   pose_joint_counts[N]u16  @ 0xB0 (2 B/slot)
//   pose_buf_states  [N]u8   @ 0xB4 (1 B/slot)
//   pose_seqs        [N]u8   @ 0xB6 (1 B/slot)
//   dbg_pose_first_word u32  @ 0xB8 (slot 0 only — diagnostic)
//   dbg_node_mtx_first  u32  @ 0xBC (slot 0 only — diagnostic)
// Keep in sync with MAX_REMOTE_LINKS in inject/include/mailbox.h.
// Bumped to 2 on 2026-04-20 after the shared-J3DModelData blocker was
// unblocked (J3DModel create flag 0x80000 → 0, so each mini-Link gets a
// private material DL instead of sharing one with every other instance).
const maxRemoteLinks = 2

func mailboxPoseBufPtr(slot int) uint32     { return uint32(0xA8 + slot*4) }
func mailboxPoseJointCount(slot int) uint32 { return uint32(0xB0 + slot*2) }
func mailboxPoseBufState(slot int) uint32   { return uint32(0xB4 + slot) }
func mailboxPoseSeq(slot int) uint32        { return uint32(0xB6 + slot) }

const mailboxDbgPoseFirstWord = 0xB8
const mailboxDbgNodeMtxFirst = 0xBC

// v0.1.3: sender-side pose publish buffer. The C-side draw hook copies
// Link #1's mpNodeMtx into this GameHeap-resident buffer ONCE per frame
// after daPy_lk_c_draw returns, giving broadcast-pose a stable read
// target that doesn't race the game's calc pass. Keep in sync with the
// Mailbox layout in inject/include/mailbox.h.
//
//   pose_publish_ptr    u32  @ 0xC0 — GameHeap address of the 2016 B buffer
//   pose_publish_jc     u16  @ 0xC4 — joint count (42 for Link)
//   pose_publish_state  u8   @ 0xC6 — 0 unalloc, 1 ready, 0xFD alloc failed
//   pose_publish_seq    u8   @ 0xC7 — bumped every frame after copy
const mailboxPosePublishPtr = 0xC0
const mailboxPosePublishJointCount = 0xC4
const mailboxPosePublishState = 0xC6
const mailboxPosePublishSeq = 0xC7

// v0.1.8: warp request — Go bumps warp_seq with a target position; the
// C handler in multiplayer_update writes it into Link's home/old/current
// pos (so velocity-from-(current-old) reads zero) and clears mOldSpeed
// (so cached momentum doesn't snap Link back), then bumps warp_ack so
// Go can poll for completion. See inject/include/mailbox.h.
const (
	mailboxWarpSeq         = 0xC8
	mailboxWarpAck         = 0xCC
	mailboxWarpX           = 0xD0
	mailboxWarpY           = 0xD4
	mailboxWarpZ           = 0xD8
	mailboxWarpDbgLinkAddr = 0xDC
	mailboxWarpDbgPostX    = 0xE0
	mailboxWarpForce       = 0xE4
	mailboxEyeFixStep      = 0xE8
)

func runPokeVec3(addrHex, xs, ys, zs string) {
	a, _ := strconv.ParseUint(strings.TrimPrefix(addrHex, "0x"), 16, 32)
	x, _ := strconv.ParseFloat(xs, 32)
	y, _ := strconv.ParseFloat(ys, 32)
	z, _ := strconv.ParseFloat(zs, 32)
	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	buf := make([]byte, 12)
	binary.BigEndian.PutUint32(buf[0:4], math.Float32bits(float32(x)))
	binary.BigEndian.PutUint32(buf[4:8], math.Float32bits(float32(y)))
	binary.BigEndian.PutUint32(buf[8:12], math.Float32bits(float32(z)))
	if err := d.WriteAbsolute(uint32(a), buf); err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Wrote (%.2f, %.2f, %.2f) to 0x%08X\n", x, y, z, a)
}

// runScanPos scans a wide RAM range for cXyz triplets matching Link's
// current.pos — used to find globals like l_debug_keep_pos that live
// outside Link's actor block. Defaults to 0x80300000..0x80800000
// (the .data/.bss region above DOL code, below mailbox).
func runScanPos() {
	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	pos, err := d.ReadPlayerPosition()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Printf("Current pos: (%.2f, %.2f, %.2f) — scanning 0x80300000..0x80800000 for triplets:\n",
		pos.PosX, pos.PosY, pos.PosZ)
	const chunkSize = 0x10000
	for base := uint32(0x80300000); base < 0x80800000; base += chunkSize {
		buf, err := d.ReadAbsolute(base, chunkSize+8)
		if err != nil {
			continue
		}
		for off := 0; off+12 <= len(buf); off += 4 {
			x := math.Float32frombits(binary.BigEndian.Uint32(buf[off : off+4]))
			if abs32(x-pos.PosX) > 1.0 {
				continue
			}
			y := math.Float32frombits(binary.BigEndian.Uint32(buf[off+4 : off+8]))
			z := math.Float32frombits(binary.BigEndian.Uint32(buf[off+8 : off+12]))
			if abs32(y-pos.PosY) < 1.0 && abs32(z-pos.PosZ) < 1.0 {
				fmt.Printf("  0x%08X = (%.2f, %.2f, %.2f)\n", base+uint32(off), x, y, z)
			}
		}
	}
}

// runPeek reads and prints n bytes at an absolute PPC RAM address —
// in u32 BE chunks plus per-word float interpretation.
func runPeek(addrHex string, n uint32) {
	addr64, err := strconv.ParseUint(strings.TrimPrefix(addrHex, "0x"), 16, 32)
	if err != nil {
		fmt.Printf("bad addr: %v\n", err)
		os.Exit(1)
	}
	addr := uint32(addr64)
	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	buf, err := d.ReadAbsolute(addr, int(n))
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	for i := uint32(0); i+4 <= n; i += 4 {
		u := binary.BigEndian.Uint32(buf[i : i+4])
		f := math.Float32frombits(u)
		fmt.Printf("  0x%08X: 0x%08X  (f32: %.4f)\n", addr+i, u, f)
	}
}

// runWarpForce toggles the C-side force-warp mode. When on, the warp
// handler re-applies the target position every frame regardless of
// seq/ack. Used to test whether continuous writes can outpace whatever
// is reverting our one-shot warp.
func runWarpForce(xs, ys, zs string, on bool) {
	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	if on {
		x, _ := strconv.ParseFloat(xs, 32)
		y, _ := strconv.ParseFloat(ys, 32)
		z, _ := strconv.ParseFloat(zs, 32)
		posBuf := make([]byte, 12)
		binary.BigEndian.PutUint32(posBuf[0:4], math.Float32bits(float32(x)))
		binary.BigEndian.PutUint32(posBuf[4:8], math.Float32bits(float32(y)))
		binary.BigEndian.PutUint32(posBuf[8:12], math.Float32bits(float32(z)))
		d.WriteAbsolute(mailboxBase+mailboxWarpX, posBuf)
		d.WriteAbsolute(mailboxBase+mailboxWarpForce, []byte{1})
		fmt.Printf("Force-warp ON: target=(%.1f, %.1f, %.1f). Use `warp-force-off` to release.\n", x, y, z)
	} else {
		d.WriteAbsolute(mailboxBase+mailboxWarpForce, []byte{0})
		fmt.Println("Force-warp OFF.")
	}
}

// runEyeFixStep writes mailbox.eye_fix_step. See the field comment in
// inject/include/mailbox.h for what each value enables.
func runEyeFixStep(step byte) {
	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	if err := d.WriteAbsolute(mailboxBase+mailboxEyeFixStep, []byte{step}); err != nil {
		fmt.Printf("write failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("eye_fix_step = %d\n", step)
}

// runEyeFixGates reads Link #1's three gating-state fields that
// daPy_lk_c::draw consults to decide whether to run the eye-decal
// four-pass recipe (matched to the disasm at 0x80107308 against the
// decomp d_a_player_main.cpp:1775-1891):
//
//   - field_0x2b0 (this+0x2B0, float): r24 = (field_0x2b0 <= -85.0f).
//     The fcmpo+cror+rlwinm.+beq at 0x801076A0..0x801076B8 routes
//     r24=TRUE to block A (if-r24 body, legs-only). Subtle: the
//     `rlwinm.` (Rc=1) RESETS CR0[eq] = (r24 == 0), so the following
//     `beq` is taken when r24==0 (i.e. when "<=" is FALSE), routing
//     to 0x80107790 (the elseif-attention check).
//   - this+0x2A0 (u32): bit 0x800 = checkFreezeState(). The four-pass
//     body at 0x80107860 starts with `lwz r0, 0x2A0(this); rlwinm. r0,
//     r0, 0, 20, 20; bne 0x80107AC4` — if freeze set, skips four-pass
//     and runs hideHatAndBackle path.
//   - mCameraInfoIdx (this+0x356C, u32) → cameraInfo[idx] at
//     0x803CA720 + idx*0x34. Bit 0x20 = the elseif-attention gate.
//     0x80107790..0x801077A0 reads the entry, masks 0x20, and
//     `beq cr0, 0x80107860` jumps to four-pass if the bit is CLEAR.
//
// Four-pass recipe RUNS when ALL hold:
//   (field_0x2b0 > -85.0f) AND (cameraInfo & 0x20 == 0) AND (NOT freeze)
//
// Run this BEFORE and AFTER setting eye_fix_step > 0 to see which
// gate (if any) is being flipped — that's the state our run_eye_fix
// is corrupting between frames.
func runEyeFixGates() {
	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()

	linkPtr, err := d.GetLinkPtr()
	if err != nil || linkPtr == 0 {
		fmt.Printf("Link actor not loaded: %v\n", err)
		os.Exit(1)
	}
	field2b0, err := d.ReadF32(linkPtr + 0x2B0)
	if err != nil {
		fmt.Printf("read field_0x2b0: %v\n", err)
		os.Exit(1)
	}
	flags2a0, err := d.ReadU32(linkPtr + 0x2A0)
	if err != nil {
		fmt.Printf("read this+0x2a0: %v\n", err)
		os.Exit(1)
	}
	camIdx, err := d.ReadU32(linkPtr + 0x356C)
	if err != nil {
		fmt.Printf("read mCameraInfoIdx: %v\n", err)
		os.Exit(1)
	}
	camEntryAddr := uint32(0x803CA720) + camIdx*0x34
	camEntry, err := d.ReadU32(camEntryAddr)
	if err != nil {
		fmt.Printf("read cameraInfo[%d]: %v\n", camIdx, err)
		os.Exit(1)
	}
	// mNoResetFlg0 at this+0x29C — checkPlayerNoDraw() at line 1696
	// returns TRUE if (cameraInfo & 0x02) || (mNoResetFlg0 & daPyFlg0_NO_DRAW
	// = 0x08000000). If TRUE, daPy_lk_c::draw early-returns BEFORE the four-pass.
	noResetFlg0, err := d.ReadU32(linkPtr + 0x29C)
	if err != nil {
		fmt.Printf("read mNoResetFlg0: %v\n", err)
		os.Exit(1)
	}
	stepBytes, _ := d.ReadAbsolute(mailboxBase+mailboxEyeFixStep, 1)
	var step byte
	if len(stepBytes) == 1 {
		step = stepBytes[0]
	}

	r24 := field2b0 <= -85.0
	freezeBit := (flags2a0 & 0x800) != 0
	attentionBit := (camEntry & 0x20) != 0
	camAttn02 := (camEntry & 0x02) != 0
	noDrawBit := (noResetFlg0 & 0x08000000) != 0
	playerNoDraw := camAttn02 || noDrawBit

	fmt.Printf("Link actor:           0x%08X\n", linkPtr)
	fmt.Printf("eye_fix_step:         %d\n", step)
	fmt.Println()
	fmt.Printf("field_0x2b0:          %f  (r24 = (<= -85.0) = %v)\n", field2b0, r24)
	fmt.Printf("this+0x2A0:           0x%08X  (& 0x800 freeze → %v)\n", flags2a0, freezeBit)
	fmt.Printf("mCameraInfoIdx:       %d  → cameraInfo @ 0x%08X = 0x%08X\n",
		camIdx, camEntryAddr, camEntry)
	fmt.Printf("                                         (& 0x20 attention → %v)\n", attentionBit)
	fmt.Printf("                                         (& 0x02 noDraw    → %v)\n", camAttn02)
	fmt.Printf("mNoResetFlg0 (+0x29C): 0x%08X  (& 0x08000000 NO_DRAW → %v)\n", noResetFlg0, noDrawBit)
	fmt.Printf("checkPlayerNoDraw() = %v  (early-returns at draw line 1696 if true)\n", playerNoDraw)
	fmt.Println()
	if !playerNoDraw && !r24 && !attentionBit && !freezeBit {
		fmt.Println("Four-pass: RUNS  (no early-return, r24=FALSE, attention=clear, !freeze)")
	} else {
		fmt.Println("Four-pass: SKIPPED — gate(s) routing elsewhere:")
		if playerNoDraw {
			fmt.Println("  - checkPlayerNoDraw() TRUE → EARLY RETURN at line 1696 (no draw at all)")
		}
		if r24 {
			fmt.Println("  - r24=TRUE (field_0x2b0 <= -85.0) → block A (legs-only body)")
		}
		if attentionBit && !r24 {
			fmt.Println("  - attention bit 0x20 set (and r24=FALSE) → block B (elseif body)")
		}
		if freezeBit && !r24 && !attentionBit {
			fmt.Println("  - freeze bit 0x800 set → hideHatAndBackle (no eye-decal recipe)")
		}
	}
}

// runEyeFixChain dumps the OPA P0 draw buffer's bucket-0 chain so we can
// see in what order packets render, whether the chain has cycles, and
// where Link #1's face/hair matpackets sit. Reads mpOpaListP0 from
// 0x803CA92C, then J3DDrawBuffer.mpBuf[0] (offset 0x00 → first u32),
// then walks via mpNextPacket at packet offset 0x04. Stops at NULL or
// after 200 entries.
func runEyeFixChain() {
	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()

	listAddrEnv := os.Getenv("WW_DRAWLIST_PTR")
	listAddr := uint32(0x803CA92C)
	if listAddrEnv != "" {
		v, _ := strconv.ParseUint(strings.TrimPrefix(listAddrEnv, "0x"), 16, 32)
		listAddr = uint32(v)
	}
	bufPtr, err := d.ReadU32(listAddr)
	if err != nil || bufPtr == 0 {
		fmt.Printf("buf ptr NULL @ 0x%08X (got 0x%08X, err=%v)\n", listAddr, bufPtr, err)
		os.Exit(1)
	}
	fmt.Printf("J3DDrawBuffer @ 0x%08X (from drawlist 0x%08X)\n", bufPtr, listAddr)

	mpBuf, err := d.ReadU32(bufPtr) // mpBuf field at offset 0
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Printf("mpBuf array @ 0x%08X\n", mpBuf)

	bucket0, err := d.ReadU32(mpBuf) // mpBuf[0]
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Printf("bucket[0] head = 0x%08X\n", bucket0)

	// Read face + hair shape addresses via mailbox model_data → joint chain.
	var faceShape, hairShape uint32
	if md, err := d.ReadU32(mailboxBase + 0x94); err == nil && md != 0 {
		if jointArr, err := d.ReadU32(md + 0x2C); err == nil && jointArr != 0 {
			if linkRoot, err := d.ReadU32(jointArr); err == nil && linkRoot != 0 {
				if mtl, err := d.ReadU32(linkRoot + 0x60); err == nil && mtl != 0 {
					for i := 0; i < 16 && mtl != 0; i++ {
						s, _ := d.ReadU32(mtl + 0x08)
						if i == 2 {
							faceShape = s
						} else if i == 5 {
							hairShape = s
						}
						mtl, _ = d.ReadU32(mtl + 0x04)
					}
				}
			}
		}
	}

	seen := map[uint32]int{}
	cur := bucket0
	for i := 0; cur != 0 && i < 200; i++ {
		if prev, ok := seen[cur]; ok {
			fmt.Printf("[%3d] 0x%08X  *** CYCLE: same as [%d] ***\n", i, cur, prev)
			break
		}
		seen[cur] = i
		// Read mpShapePacket (matpacket+0x2C) → mpShape (shapepacket+0x24)
		shapePkt, _ := d.ReadU32(cur + 0x2C)
		var shape uint32
		if shapePkt != 0 {
			shape, _ = d.ReadU32(shapePkt + 0x24)
		}
		marker := ""
		if shape != 0 && shape == faceShape {
			marker = "  (face)"
		} else if shape != 0 && shape == hairShape {
			marker = "  (hair)"
		}
		fmt.Printf("[%3d] 0x%08X  shape=0x%08X%s\n", i, cur, shape, marker)
		next, err := d.ReadU32(cur + 0x04) // mpNextPacket
		if err != nil {
			fmt.Printf("        read next failed: %v\n", err)
			break
		}
		cur = next
	}
	if cur == 0 {
		fmt.Println("--- chain terminates at NULL ---")
	}
}

// runPPCDisasm reads num*4 bytes from the given absolute address and prints
// each word as a partially-decoded PPC instruction. Decodes the most common
// memory-access opcodes (lwz/stw/stwu/lhz/lbz/stb), float loads (lfs/lfd),
// integer/float compares (cmpwi/cmplwi/cmpw/cmplw/fcmpu), branches
// (b/bl/bla and conditional bc with beq/bne/blt/bge/bgt/ble mnemonics).
// That's enough to trace the gating conditional structure inside daPy_lk_c::draw
// (which has FP comparisons against -85.0f and integer-flag tests).
//
// Source: WW_DOL_PATH env var picks an offline DOL file (no Dolphin needed —
// preferred for static-code disassembly like daPy_lk_c::draw at 0x80107308).
// If unset, reads from the live Dolphin process.
func runPPCDisasm(addr uint32, num int) {
	var buf []byte
	if dolPath := os.Getenv("WW_DOL_PATH"); dolPath != "" {
		var err error
		buf, err = readDOLBytes(dolPath, addr, num*4)
		if err != nil {
			fmt.Printf("DOL read failed: %v\n", err)
			os.Exit(1)
		}
	} else {
		d, err := dolphin.Find("GZLE01")
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		defer d.Close()
		buf, err = d.ReadAbsolute(addr, num*4)
		if err != nil {
			fmt.Printf("read failed: %v\n", err)
			os.Exit(1)
		}
	}
	for i := 0; i+4 <= len(buf); i += 4 {
		insn := binary.BigEndian.Uint32(buf[i : i+4])
		pc := addr + uint32(i)
		fmt.Printf("  0x%08X: %08X    %s\n", pc, insn, decodePPC(insn, pc))
	}
}

// decodePPC returns a partial mnemonic for a PPC instruction at the given PC.
// Covers what we need to read daPy_lk_c::draw and similar J3D internals;
// unknown ops fall back to "op=NN".
func decodePPC(insn, pc uint32) string {
	op := (insn >> 26) & 0x3F
	rD := (insn >> 21) & 0x1F
	rA := (insn >> 16) & 0x1F
	d16 := int32(int16(insn & 0xFFFF))
	switch op {
	case 32:
		return fmt.Sprintf("lwz   r%d, %d(r%d)", rD, d16, rA)
	case 33:
		return fmt.Sprintf("lwzu  r%d, %d(r%d)", rD, d16, rA)
	case 34:
		return fmt.Sprintf("lbz   r%d, %d(r%d)", rD, d16, rA)
	case 36:
		return fmt.Sprintf("stw   r%d, %d(r%d)", rD, d16, rA)
	case 37:
		return fmt.Sprintf("stwu  r%d, %d(r%d)", rD, d16, rA)
	case 38:
		return fmt.Sprintf("stb   r%d, %d(r%d)", rD, d16, rA)
	case 40:
		return fmt.Sprintf("lhz   r%d, %d(r%d)", rD, d16, rA)
	case 44:
		return fmt.Sprintf("sth   r%d, %d(r%d)", rD, d16, rA)
	case 48:
		return fmt.Sprintf("lfs   f%d, %d(r%d)", rD, d16, rA)
	case 50:
		return fmt.Sprintf("lfd   f%d, %d(r%d)", rD, d16, rA)
	case 52:
		return fmt.Sprintf("stfs  f%d, %d(r%d)", rD, d16, rA)
	case 54:
		return fmt.Sprintf("stfd  f%d, %d(r%d)", rD, d16, rA)
	case 14:
		imm := int32(int16(insn & 0xFFFF))
		if rA == 0 {
			return fmt.Sprintf("li    r%d, %d", rD, imm)
		}
		return fmt.Sprintf("addi  r%d, r%d, %d", rD, rA, imm)
	case 15:
		imm := int32(int16(insn & 0xFFFF))
		return fmt.Sprintf("addis r%d, r%d, %d   (=0x%04X)", rD, rA, imm, uint16(imm))
	case 10:
		// cmpli (cmplwi when L=0): unsigned compare with imm.
		crfD := (insn >> 23) & 0x7
		uimm := uint32(insn & 0xFFFF)
		return fmt.Sprintf("cmplwi cr%d, r%d, 0x%X", crfD, rA, uimm)
	case 11:
		// cmpi (cmpwi when L=0): signed compare with imm.
		crfD := (insn >> 23) & 0x7
		return fmt.Sprintf("cmpwi cr%d, r%d, %d", crfD, rA, d16)
	case 16:
		// bc — conditional branch. BO field = rD, BI field = rA, displacement
		// in bottom 16 bits (sign-extended, low 2 bits 0).
		bo := rD
		bi := rA
		bd := int32(int16(insn&0xFFFC)) // already low-2-zero by encoding
		absBit := insn & 0x2
		lkBit := insn & 0x1
		target := uint32(int32(pc) + bd)
		if absBit != 0 {
			target = uint32(bd)
		}
		// Branch-if-condition: BO bit 0x10 means "decrement CTR not used"
		// In typical compiler output: BO=12 → branch if CR[BI]==1; BO=4 → branch if CR[BI]==0.
		crf := bi >> 2
		bit := bi & 0x3 // 0=lt, 1=gt, 2=eq, 3=so
		bitName := []string{"lt", "gt", "eq", "so"}[bit]
		var mnem string
		switch bo {
		case 12: // branch-if-true
			switch bit {
			case 0:
				mnem = "blt"
			case 1:
				mnem = "bgt"
			case 2:
				mnem = "beq"
			case 3:
				mnem = "bso"
			}
		case 4: // branch-if-false
			switch bit {
			case 0:
				mnem = "bge"
			case 1:
				mnem = "ble"
			case 2:
				mnem = "bne"
			case 3:
				mnem = "bns"
			}
		default:
			mnem = fmt.Sprintf("bc(BO=%d,BI=%d:%s)", bo, bi, bitName)
		}
		if lkBit != 0 {
			mnem += "l"
		}
		if absBit != 0 {
			mnem += "a"
		}
		return fmt.Sprintf("%s   cr%d, 0x%08X", mnem, crf, target)
	case 18:
		// b/bl/bla
		li := int32(insn&0x03FFFFFC) << 6 >> 6
		absBit := insn & 0x2
		lkBit := insn & 0x1
		target := uint32(int32(pc) + li)
		if absBit != 0 {
			target = uint32(li)
		}
		s := "b"
		if lkBit != 0 {
			s = "bl"
		}
		if absBit != 0 {
			s += "a"
		}
		return fmt.Sprintf("%s    0x%08X", s, target)
	case 19:
		sub := (insn >> 1) & 0x3FF
		switch sub {
		case 16:
			return "blr   "
		case 528:
			return "bctr  "
		case 257:
			return "crand "
		case 449:
			return "cror  "
		default:
			return fmt.Sprintf("ctrl-19 sub=%d", sub)
		}
	case 21:
		// rlwinm — common bitfield extract; show MB/ME for clarity.
		sh := (insn >> 11) & 0x1F
		mb := (insn >> 6) & 0x1F
		me := (insn >> 1) & 0x1F
		return fmt.Sprintf("rlwinm r%d, r%d, %d, %d, %d", rA, rD, sh, mb, me)
	case 24:
		return fmt.Sprintf("ori   r%d, r%d, 0x%X", rA, rD, uint16(insn))
	case 25:
		return fmt.Sprintf("oris  r%d, r%d, 0x%X", rA, rD, uint16(insn))
	case 28:
		return fmt.Sprintf("andi. r%d, r%d, 0x%X", rA, rD, uint16(insn))
	case 29:
		return fmt.Sprintf("andis. r%d, r%d, 0x%X", rA, rD, uint16(insn))
	case 31:
		sub := (insn >> 1) & 0x3FF
		rB := (insn >> 11) & 0x1F
		switch sub {
		case 0:
			crfD := (insn >> 23) & 0x7
			return fmt.Sprintf("cmpw  cr%d, r%d, r%d", crfD, rA, rB)
		case 32:
			crfD := (insn >> 23) & 0x7
			return fmt.Sprintf("cmplw cr%d, r%d, r%d", crfD, rA, rB)
		case 23:
			return fmt.Sprintf("lwzx  r%d, r%d, r%d", rD, rA, rB)
		case 151:
			return fmt.Sprintf("stwx  r%d, r%d, r%d", rD, rA, rB)
		case 467:
			return "mtspr "
		case 339:
			return "mfspr "
		case 444:
			return fmt.Sprintf("or    r%d, r%d, r%d", rA, rD, rB)
		case 28:
			return fmt.Sprintf("and   r%d, r%d, r%d", rA, rD, rB)
		case 24:
			return fmt.Sprintf("slw   r%d, r%d, r%d", rA, rD, rB)
		case 536:
			return fmt.Sprintf("srw   r%d, r%d, r%d", rA, rD, rB)
		case 266:
			return fmt.Sprintf("add   r%d, r%d, r%d", rD, rA, rB)
		case 40:
			return fmt.Sprintf("subf  r%d, r%d, r%d", rD, rA, rB)
		default:
			return fmt.Sprintf("ext31 sub=%d  rD=%d rA=%d rB=%d", sub, rD, rA, rB)
		}
	case 63:
		// FP extended ops. fcmpu = sub 0, fcmpo = sub 32, fmr = sub 72, fneg = sub 40.
		sub := (insn >> 1) & 0x3FF
		fA := rA
		fB := (insn >> 11) & 0x1F
		crfD := (insn >> 23) & 0x7
		switch sub {
		case 0:
			return fmt.Sprintf("fcmpu cr%d, f%d, f%d", crfD, fA, fB)
		case 32:
			return fmt.Sprintf("fcmpo cr%d, f%d, f%d", crfD, fA, fB)
		case 72:
			return fmt.Sprintf("fmr   f%d, f%d", rD, fB)
		case 40:
			return fmt.Sprintf("fneg  f%d, f%d", rD, fB)
		default:
			return fmt.Sprintf("fp63  sub=%d f%d,f%d", sub, fA, fB)
		}
	default:
		return fmt.Sprintf("op=%d (0x%02X)", op, op)
	}
}

// readDOLBytes opens a Wii/GC DOL file and returns `n` bytes starting at the
// given virtual load address (e.g. 0x80107308 for daPy_lk_c::draw). The DOL
// file format places its 0x100-byte header at file offset 0:
//   +0x00..+0x47 — file offsets[0..17]   (7 text + 11 data)
//   +0x48..+0x8F — load addrs[0..17]
//   +0x90..+0xD7 — section sizes[0..17]
// We linearly find the section containing addr and read from
// fileOffset[i] + (addr - loadAddr[i]).
func readDOLBytes(path string, addr uint32, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var hdr [0x100]byte
	if _, err := f.ReadAt(hdr[:], 0); err != nil {
		return nil, err
	}
	for i := 0; i < 18; i++ {
		off := binary.BigEndian.Uint32(hdr[i*4:])
		la := binary.BigEndian.Uint32(hdr[0x48+i*4:])
		sz := binary.BigEndian.Uint32(hdr[0x90+i*4:])
		if sz == 0 || la == 0 {
			continue
		}
		if addr >= la && addr+uint32(n) <= la+sz {
			out := make([]byte, n)
			if _, err := f.ReadAt(out, int64(off+(addr-la))); err != nil {
				return nil, err
			}
			return out, nil
		}
	}
	return nil, fmt.Errorf("addr 0x%08X (n=%d) not contained in any DOL section of %s", addr, n, path)
}

// runJ3DSysProbe dumps the first 0x128 bytes of the j3dSys global at
// 0x803EDA58 and, for every u32 in there, reports whether it matches one
// of the three known drawbuffer pointers (opa_p0/opa_p1/xlu_p1, looked up
// via the dDlst_list_c fields at 0x803CA92C/0x930/0x934). The matching
// offset is the real `mDrawBuffer` slot. game.h currently assumes
// J3D_SYS_DRAWBUFFER_OPA_OFFSET=0x48 / XLU_OFFSET=0x4C, but evidence from
// eye-fix-find-shape (mini-link body landing in opa_p0 instead of opa_p1
// after a "swap to P1" write) suggests those offsets are wrong.
func runJ3DSysProbe() {
	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()

	const j3dSysAddr = uint32(0x803EDA58)
	const j3dSysSize = 0x128

	// Resolve the three known drawbuffer pointers.
	type dl struct {
		name   string
		bufPtr uint32
	}
	var dls []dl
	for _, x := range []struct {
		name string
		addr uint32
	}{
		{"opa_p0", 0x803CA92C},
		{"opa_p1", 0x803CA930},
		{"xlu_p1", 0x803CA934},
	} {
		v, err := d.ReadU32(x.addr)
		if err == nil && v != 0 {
			dls = append(dls, dl{x.name, v})
		}
	}
	fmt.Println("Known drawbuffers:")
	for _, x := range dls {
		fmt.Printf("  %s = 0x%08X\n", x.name, x.bufPtr)
	}
	fmt.Println()

	buf, err := d.ReadAbsolute(j3dSysAddr, j3dSysSize)
	if err != nil {
		fmt.Printf("read failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("j3dSys @ 0x%08X (0x%X bytes):\n", j3dSysAddr, j3dSysSize)

	for off := 0; off < len(buf); off += 16 {
		line := fmt.Sprintf("  +0x%03X:", off)
		for k := 0; k < 16 && off+k < len(buf); k += 4 {
			w := binary.BigEndian.Uint32(buf[off+k : off+k+4])
			line += fmt.Sprintf(" %08X", w)
		}
		fmt.Println(line)
	}

	fmt.Println()
	fmt.Println("Drawbuffer-pointer matches:")
	hits := 0
	for off := 0; off+4 <= len(buf); off += 4 {
		v := binary.BigEndian.Uint32(buf[off : off+4])
		for _, x := range dls {
			if v == x.bufPtr {
				fmt.Printf("  +0x%03X = 0x%08X (%s)\n", off, v, x.name)
				hits++
			}
		}
	}
	if hits == 0 {
		fmt.Println("  (none — drawbuffer ptrs not currently held in j3dSys, sample a busier frame)")
	}
}

// runEyeFixFindShape exhaustively searches every bucket of opa_p0, opa_p1,
// xlu_p1 (the three known dDlst_list_c drawlists) for the face (mtl 2) and
// hair (mtl 5) shape pointer values. For each non-empty bucket it walks the
// chain via mpNextPacket@+0x04, and for each packet scans the first 0x40
// bytes for the target shape addresses. Prints which drawlist + bucket index
// + packet address + byte-offset-within-packet contains face/hair, plus a
// hex dump of every matching packet so we can confirm matpacket layout.
//
// Also dumps each J3DDrawBuffer's first 0x40 bytes so the layout (mpBuf
// offset, NumBuckets) can be cross-checked. Honors WW_NUM_BUCKETS to
// override the bucket count to walk (default 256).
//
// Background: eye-fix-chain only walks bucket[0] of opa_p0 and assumes
// matpacket+0x2C → shapepacket+0x24 → shape. That dump didn't show face
// or hair even at baseline (where they visibly render), so either the
// offsets are wrong or face/hair are in different buckets/lists. This
// command bypasses both assumptions by scanning packet bytes directly.
func runEyeFixFindShape() {
	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()

	// Resolve face + hair shape addrs via mailbox.dbg_model_data.
	modelData, err := d.ReadU32(mailboxBase + 0x94)
	if err != nil || modelData == 0 {
		fmt.Printf("dbg_model_data not published (got 0x%08X, err=%v)\n", modelData, err)
		os.Exit(1)
	}
	jointArr, err := d.ReadU32(modelData + 0x2C)
	if err != nil || jointArr == 0 {
		fmt.Printf("joint array NULL: %v\n", err)
		os.Exit(1)
	}
	linkRoot, err := d.ReadU32(jointArr)
	if err != nil || linkRoot == 0 {
		fmt.Printf("link_root NULL: %v\n", err)
		os.Exit(1)
	}
	mtl, err := d.ReadU32(linkRoot + 0x60)
	if err != nil || mtl == 0 {
		fmt.Printf("link_root mMesh NULL: %v\n", err)
		os.Exit(1)
	}
	var faceShape, hairShape uint32
	for i := 0; i < 16 && mtl != 0; i++ {
		s, _ := d.ReadU32(mtl + 0x08)
		if i == 2 {
			faceShape = s
		} else if i == 5 {
			hairShape = s
		}
		mtl, _ = d.ReadU32(mtl + 0x04)
	}
	fmt.Printf("Targets: faceShape=0x%08X  hairShape=0x%08X\n\n", faceShape, hairShape)

	numBuckets := 256
	if env := os.Getenv("WW_NUM_BUCKETS"); env != "" {
		if v, err := strconv.Atoi(env); err == nil && v > 0 {
			numBuckets = v
		}
	}

	// Sweep the dComIfGd_drawlist region for every u32 that looks like a
	// J3DDrawBuffer pointer. We know opa_p0 / opa_p1 / xlu_p1 live at
	// 0x803CA92C / 0x930 / 0x934, and the buffers themselves sit in a heap
	// allocation around 0x8068B8E0 (the live snapshot from this build), so
	// scan ±0x100 around the 3 known drawlist fields and accept any u32
	// pointer that falls inside MEM1 (0x80000000..0x817FFFFF).
	type drawlist struct {
		name    string
		fieldAt uint32
		bufPtr  uint32
	}
	knownNames := map[uint32]string{
		0x803CA92C: "opa_p0",
		0x803CA930: "opa_p1",
		0x803CA934: "xlu_p1",
	}
	scanLo, scanHi := uint32(0x803CA820), uint32(0x803CAA20)
	scanBuf, err := d.ReadAbsolute(scanLo, int(scanHi-scanLo))
	if err != nil {
		fmt.Printf("drawlist scan failed: %v\n", err)
		os.Exit(1)
	}
	var lists []drawlist
	for o := 0; o+4 <= len(scanBuf); o += 4 {
		v := binary.BigEndian.Uint32(scanBuf[o : o+4])
		if v < 0x80000000 || v >= 0x81800000 {
			continue
		}
		fieldAt := scanLo + uint32(o)
		name, ok := knownNames[fieldAt]
		if !ok {
			name = fmt.Sprintf("dl@0x%08X", fieldAt)
		}
		lists = append(lists, drawlist{name, fieldAt, v})
	}
	fmt.Printf("Scanned 0x%08X..0x%08X for drawlist fields: found %d MEM1-pointer candidates\n",
		scanLo, scanHi, len(lists))
	fmt.Println()

	// Dedupe: multiple drawlist fields can point to the same J3DDrawBuffer
	// (or the same memory region). Walk each unique bufPtr exactly once.
	seenBuf := map[uint32]bool{}
	for _, dl := range lists {
		if seenBuf[dl.bufPtr] {
			continue
		}
		seenBuf[dl.bufPtr] = true

		fmt.Printf("=== %s (field @ 0x%08X → bufPtr 0x%08X) ===\n", dl.name, dl.fieldAt, dl.bufPtr)

		hdr, err := d.ReadAbsolute(dl.bufPtr, 0x40)
		if err != nil {
			fmt.Printf("  header read failed: %v\n\n", err)
			continue
		}
		fmt.Printf("  header[0x00..0x40]:\n")
		for off := 0; off < len(hdr); off += 16 {
			line := fmt.Sprintf("    +0x%02X: ", off)
			for k := 0; k < 16 && off+k < len(hdr); k += 4 {
				w := binary.BigEndian.Uint32(hdr[off+k : off+k+4])
				line += fmt.Sprintf(" %08X", w)
			}
			fmt.Println(line)
		}

		mpBuf := binary.BigEndian.Uint32(hdr[0:4])
		mNumBuckets := binary.BigEndian.Uint32(hdr[4:8])
		// Reject anything that doesn't look like a real J3DDrawBuffer.
		if mpBuf < 0x80000000 || mpBuf >= 0x81800000 || mNumBuckets == 0 || mNumBuckets > 4096 {
			fmt.Printf("  not a J3DDrawBuffer (mpBuf=0x%08X, mNumBuckets=%d) — skip\n\n",
				mpBuf, mNumBuckets)
			continue
		}
		// Allow override but cap to mNumBuckets.
		walkN := int(mNumBuckets)
		if numBuckets > 0 && numBuckets < walkN {
			walkN = numBuckets
		}
		fmt.Printf("  mpBuf=0x%08X mNumBuckets=%d — walking %d buckets\n", mpBuf, mNumBuckets, walkN)

		bucketBytes, err := d.ReadAbsolute(mpBuf, walkN*4)
		if err != nil {
			fmt.Printf("  bucket array read failed: %v\n\n", err)
			continue
		}

		nonEmpty, totalPkts, hits := 0, 0, 0
		// Wider scan window: J3DShapePacket constructor is 0x60 bytes per
		// the symbol map, but the field layout is unknown — read 0x100 to
		// cover any plausible mpShape offset.
		const pktBytes = 0x100
		dumpAll := os.Getenv("WW_DUMP_ALL") != ""
		for b := 0; b < walkN; b++ {
			head := binary.BigEndian.Uint32(bucketBytes[b*4 : b*4+4])
			if head == 0 {
				continue
			}
			nonEmpty++
			seen := map[uint32]bool{}
			cur := head
			depth := 0
			for cur != 0 && depth < 1000 {
				if seen[cur] {
					fmt.Printf("  bucket[%d] CYCLE detected at 0x%08X (depth %d)\n", b, cur, depth)
					break
				}
				seen[cur] = true

				pkt, err := d.ReadAbsolute(cur, pktBytes)
				if err != nil {
					fmt.Printf("  bucket[%d] read failed @ 0x%08X: %v\n", b, cur, err)
					break
				}
				totalPkts++

				if dumpAll {
					vtable := binary.BigEndian.Uint32(pkt[0:4])
					next := binary.BigEndian.Uint32(pkt[4:8])
					f24 := binary.BigEndian.Uint32(pkt[0x24:0x28])
					f28 := binary.BigEndian.Uint32(pkt[0x28:0x2C])
					f2C := binary.BigEndian.Uint32(pkt[0x2C:0x30])
					fmt.Printf("    [%2d] pkt=0x%08X  vtbl=0x%08X next=0x%08X  +24=0x%08X +28=0x%08X +2C=0x%08X\n",
						depth, cur, vtable, next, f24, f28, f2C)
				}

				match := func(v uint32) (string, bool) {
					if faceShape != 0 && v == faceShape {
						return "FACE", true
					}
					if hairShape != 0 && v == hairShape {
						return "HAIR", true
					}
					return "", false
				}

				dumpPkt := func(addr uint32, raw []byte) {
					for off := 0; off < len(raw); off += 16 {
						line := fmt.Sprintf("        +0x%02X: ", off)
						for k := 0; k < 16 && off+k < len(raw); k += 4 {
							w := binary.BigEndian.Uint32(raw[off+k : off+k+4])
							line += fmt.Sprintf(" %08X", w)
						}
						fmt.Println(line)
					}
				}

				directHit := false
				for o := 0; o+4 <= len(pkt); o += 4 {
					v := binary.BigEndian.Uint32(pkt[o : o+4])
					if label, ok := match(v); ok {
						fmt.Printf("  *** %s direct in %s bucket[%d] packet 0x%08X at +0x%02X ***\n",
							label, dl.name, b, cur, o)
						dumpPkt(cur, pkt)
						hits++
						directHit = true
						break
					}
				}
				if directHit {
					cur = binary.BigEndian.Uint32(pkt[4:8])
					depth++
					continue
				}

				// One-level dereference: if any heap pointer in the packet's
				// first 0x40 bytes (where mpShapePacket-style fields live)
				// points at an object whose first 0x40 bytes contain face or
				// hair, report it. That catches the matpacket → shapepacket
				// → mpShape indirection where the matpacket itself doesn't
				// store the shape pointer.
				for o := 0; o+4 <= 0x40; o += 4 {
					v := binary.BigEndian.Uint32(pkt[o : o+4])
					if v < 0x80000000 || v >= 0x81800000 {
						continue
					}
					if v == cur || (cur != 0 && v == binary.BigEndian.Uint32(pkt[4:8])) {
						continue
					}
					sub, err := d.ReadAbsolute(v, 0x40)
					if err != nil {
						continue
					}
					for so := 0; so+4 <= len(sub); so += 4 {
						sv := binary.BigEndian.Uint32(sub[so : so+4])
						if label, ok := match(sv); ok {
							fmt.Printf("  *** %s indirect in %s bucket[%d]: packet 0x%08X +0x%02X → 0x%08X +0x%02X ***\n",
								label, dl.name, b, cur, o, v, so)
							fmt.Printf("      packet @ 0x%08X:\n", cur)
							dumpPkt(cur, pkt[:0x80])
							fmt.Printf("      derefd  @ 0x%08X:\n", v)
							dumpPkt(v, sub)
							hits++
							break
						}
					}
				}

				cur = binary.BigEndian.Uint32(pkt[4:8])
				depth++
			}
		}
		fmt.Printf("  summary: %d non-empty buckets, %d packets walked, %d face/hair hits\n\n",
			nonEmpty, totalPkts, hits)
	}
}

// runEyeFixProbe walks the live J3D pointer chain via mailbox.dbg_model_data
// to read link_root.mMesh[i].mShape.mFlags for every material on link_root.
// Prints whether the HIDE bit is set per material. Used to figure out
// whether our run_eye_fix is leaving face/hair (mtl indices 2 and 5) with
// the J3DShpFlag_Hide bit set, vs the visibility loss being caused by GX
// state pollution that doesn't touch mFlags.
func runEyeFixProbe() {
	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()

	modelData, err := d.ReadU32(mailboxBase + 0x94) // dbg_model_data
	if err != nil || modelData == 0 {
		fmt.Printf("dbg_model_data not published yet (got 0x%08X, err=%v)\n", modelData, err)
		os.Exit(1)
	}
	fmt.Printf("link J3DModelData @ 0x%08X\n", modelData)

	jointArr, err := d.ReadU32(modelData + 0x2C) // mJointNodePointer
	if err != nil || jointArr == 0 {
		fmt.Printf("joint array NULL (got 0x%08X, err=%v)\n", jointArr, err)
		os.Exit(1)
	}
	linkRoot, err := d.ReadU32(jointArr) // joint_arr[0]
	if err != nil || linkRoot == 0 {
		fmt.Printf("link_root NULL (got 0x%08X, err=%v)\n", linkRoot, err)
		os.Exit(1)
	}
	fmt.Printf("link_root joint @ 0x%08X\n", linkRoot)

	mtl, err := d.ReadU32(linkRoot + 0x60) // mMesh
	if err != nil || mtl == 0 {
		fmt.Printf("link_root mMesh NULL (got 0x%08X, err=%v)\n", mtl, err)
		os.Exit(1)
	}
	for i := 0; i < 16 && mtl != 0; i++ {
		shape, _ := d.ReadU32(mtl + 0x08) // mShape
		flags := uint32(0)
		if shape != 0 {
			flags, _ = d.ReadU32(shape + 0x0C) // mFlags
		}
		hidden := flags & 0x0001
		marker := "       "
		if i == 2 {
			marker = "(face) "
		} else if i == 5 {
			marker = "(hair) "
		}
		fmt.Printf("  mtl[%2d]%s shape=0x%08X mFlags=0x%08X HIDE=%d\n", i, marker, shape, flags, hidden)
		mtl, _ = d.ReadU32(mtl + 0x04) // mNext
	}
}

// runTrackPos polls current.pos + speed at 100 ms intervals for the
// given duration and prints the temporal evolution. Used to diagnose
// whether a warp reverts as a slow drift (something setting velocity
// each frame) or a clean snap (something hard-resetting current.pos).
func runTrackPos(secs int) {
	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	linkPtr, err := d.GetLinkPtr()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Printf("Tracking Link @ 0x%08X for %ds (50 ms tick)...\n", linkPtr, secs)
	fmt.Println("  t(ms)        X        Y        Z   speed.x   speed.y   speed.z   speedF")
	deadline := time.Now().Add(time.Duration(secs) * time.Second)
	start := time.Now()
	for time.Now().Before(deadline) {
		posBuf, err := d.ReadAbsolute(linkPtr+0x1F8, 12)
		if err != nil {
			break
		}
		spdBuf, err := d.ReadAbsolute(linkPtr+0x220, 12)
		if err != nil {
			break
		}
		spdFBuf, err := d.ReadAbsolute(linkPtr+0x254, 4)
		if err != nil {
			break
		}
		x := math.Float32frombits(binary.BigEndian.Uint32(posBuf[0:4]))
		y := math.Float32frombits(binary.BigEndian.Uint32(posBuf[4:8]))
		z := math.Float32frombits(binary.BigEndian.Uint32(posBuf[8:12]))
		sx := math.Float32frombits(binary.BigEndian.Uint32(spdBuf[0:4]))
		sy := math.Float32frombits(binary.BigEndian.Uint32(spdBuf[4:8]))
		sz := math.Float32frombits(binary.BigEndian.Uint32(spdBuf[8:12]))
		sf := math.Float32frombits(binary.BigEndian.Uint32(spdFBuf))
		fmt.Printf("  %4d  %9.1f  %7.1f  %9.1f  %8.2f  %8.2f  %8.2f  %7.2f\n",
			time.Since(start).Milliseconds(), x, y, z, sx, sy, sz, sf)
		time.Sleep(100 * time.Millisecond)
	}
}

// runFindPos dumps Link's actor memory and reports every 4-byte-aligned
// f32 within ±1.0 of a target X (and matching Y/Z for "triplet" tagging).
// Defaults to current.pos.X but accepts an explicit target via args —
// useful for finding hidden source-of-truth fields when force-warp is
// active and current.pos oscillates.
func runFindPos() {
	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	pos, err := d.ReadPlayerPosition()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	linkPtr, err := d.GetLinkPtr()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	tx, ty, tz := pos.PosX, pos.PosY, pos.PosZ
	if len(os.Args) >= 5 {
		if v, err := strconv.ParseFloat(os.Args[2], 32); err == nil {
			tx = float32(v)
		}
		if v, err := strconv.ParseFloat(os.Args[3], 32); err == nil {
			ty = float32(v)
		}
		if v, err := strconv.ParseFloat(os.Args[4], 32); err == nil {
			tz = float32(v)
		}
	}
	const dumpSize = 0x4000
	mem, err := d.ReadAbsolute(linkPtr, dumpSize)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Printf("Link @ 0x%08X — searching for f32 ≈ (%.1f, %.1f, %.1f)\n", linkPtr, tx, ty, tz)
	for off := 0; off+4 <= dumpSize; off += 4 {
		v := math.Float32frombits(binary.BigEndian.Uint32(mem[off : off+4]))
		if abs32(v-tx) < 1.0 {
			next4, next8 := float32(0), float32(0)
			if off+12 <= dumpSize {
				next4 = math.Float32frombits(binary.BigEndian.Uint32(mem[off+4 : off+8]))
				next8 = math.Float32frombits(binary.BigEndian.Uint32(mem[off+8 : off+12]))
			}
			marker := ""
			if abs32(next4-ty) < 1.0 && abs32(next8-tz) < 1.0 {
				marker = "  <-- triplet"
			}
			fmt.Printf("  +0x%04X = %12.2f   (+4: %12.2f, +8: %12.2f)%s\n", off, v, next4, next8, marker)
		}
	}
}

func abs32(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}

// runWarp is the CLI entry point for ad-hoc Link teleporting. Honors the
// usual env knobs for Dolphin selection (WW_DOLPHIN_INDEX / WW_DOLPHIN_PID).
func runWarp(xs, ys, zs string) {
	x, err := strconv.ParseFloat(xs, 32)
	if err != nil {
		fmt.Printf("bad X: %v\n", err)
		os.Exit(1)
	}
	y, err := strconv.ParseFloat(ys, 32)
	if err != nil {
		fmt.Printf("bad Y: %v\n", err)
		os.Exit(1)
	}
	z, err := strconv.ParseFloat(zs, 32)
	if err != nil {
		fmt.Printf("bad Z: %v\n", err)
		os.Exit(1)
	}
	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	defer d.Close()
	if err := warpLink(d, float32(x), float32(y), float32(z), 1*time.Second); err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	// Diagnostic: read what the C handler stashed about its write so we
	// can tell if the write reached memory and what actor it hit.
	dbgAddrBuf, _ := d.ReadAbsolute(mailboxBase+mailboxWarpDbgLinkAddr, 4)
	dbgPostBuf, _ := d.ReadAbsolute(mailboxBase+mailboxWarpDbgPostX, 4)
	dbgAddr := binary.BigEndian.Uint32(dbgAddrBuf)
	dbgPostX := math.Float32frombits(binary.BigEndian.Uint32(dbgPostBuf))
	linkPtr, _ := d.GetLinkPtr()
	fmt.Printf("Warped Link to (%.1f, %.1f, %.1f).\n", x, y, z)
	fmt.Printf("  C handler saw link=0x%08X (Go GetLinkPtr=0x%08X) — %s\n",
		dbgAddr, linkPtr,
		map[bool]string{true: "MATCH", false: "MISMATCH!"}[dbgAddr == linkPtr])
	fmt.Printf("  Post-write current.pos.x = %.2f (target %.2f) — %s\n",
		dbgPostX, x,
		map[bool]string{true: "write landed", false: "write was reverted within frame"}[abs32(dbgPostX-float32(x)) < 1.0])
	posAfter, _ := d.ReadPlayerPosition()
	if posAfter != nil {
		fmt.Printf("  Read-after-warp current.pos = (%.2f, %.2f, %.2f) — %s\n",
			posAfter.PosX, posAfter.PosY, posAfter.PosZ,
			map[bool]string{true: "stuck!", false: "REVERTED"}[abs32(posAfter.PosX-float32(x)) < 1.0])
	}
}

// warpLink writes the target position into the warp mailbox slot, bumps
// warp_seq, and polls warp_ack until the C side has consumed the request
// (or the timeout expires). Used by mp-local to space the two Links
// after both Dolphins boot from the same SAVE_STATE, and exposed as the
// `warp` subcommand for ad-hoc teleporting.
func warpLink(d *dolphin.Dolphin, x, y, z float32, timeout time.Duration) error {
	// Bump from whatever seq is currently set (cold start = 0).
	prevSeqBuf, err := d.ReadAbsolute(mailboxBase+mailboxWarpSeq, 4)
	if err != nil {
		return fmt.Errorf("read warp_seq: %w", err)
	}
	nextSeq := binary.BigEndian.Uint32(prevSeqBuf) + 1

	// Write coords first, THEN the seq bump — C reads seq under a single
	// frame's tick, and a torn write where seq updates before coords
	// would warp Link to whatever stale (x,y,z) was in the slot.
	posBuf := make([]byte, 12)
	binary.BigEndian.PutUint32(posBuf[0:4], math.Float32bits(x))
	binary.BigEndian.PutUint32(posBuf[4:8], math.Float32bits(y))
	binary.BigEndian.PutUint32(posBuf[8:12], math.Float32bits(z))
	if err := d.WriteAbsolute(mailboxBase+mailboxWarpX, posBuf); err != nil {
		return fmt.Errorf("write warp coords: %w", err)
	}
	seqBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(seqBuf, nextSeq)
	if err := d.WriteAbsolute(mailboxBase+mailboxWarpSeq, seqBuf); err != nil {
		return fmt.Errorf("write warp_seq: %w", err)
	}

	// Poll for ack — game runs at 60 Hz so we'd expect this within ~17 ms,
	// but be generous in case the multiplayer_update hook missed a tick.
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ackBuf, err := d.ReadAbsolute(mailboxBase+mailboxWarpAck, 4)
		if err == nil && binary.BigEndian.Uint32(ackBuf) == nextSeq {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for warp_ack to bump (timeout=%s)", timeout)
}

func runShadowMode(s string) {
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 || v > 5 {
		fmt.Println("mode must be 0 (baseline), 1 (refresh), 2 (freeze), 3 (no-op basicMtxCalc), 4 (echo-ring), or 5 (pose-feed)")
		os.Exit(1)
	}
	d, err := dolphin.Find("GZLE01")
	if err != nil { fmt.Println(err); os.Exit(1) }
	defer d.Close()
	if err := d.WriteAbsolute(mailboxBase+mailboxShadowMode, []byte{byte(v)}); err != nil {
		fmt.Printf("write failed: %v\n", err)
		os.Exit(1)
	}
	// Give the draw hook one frame to react and (in mode 2) latch.
	time.Sleep(50 * time.Millisecond)
	latched, _ := d.ReadAbsolute(mailboxBase+mailboxShadowLatched, 1)
	labels := []string{
		"off (no Link #2 rendered — default at boot)",
		"mirror-refresh (shadow daPy_lk_c, copy every frame)",
		"mirror-freeze (shadow daPy_lk_c, copy once)",
		"no-op basicMtxCalc (decouple; Link #2 freezes)",
		"echo-ring (capture + delayed replay; set echo-delay)",
		"pose-feed (mpNodeMtx from mailbox.pose_buf; run pose-test or broadcast-pose)",
	}
	latchedStr := fmt.Sprintf("%d", latched[0])
	if latched[0] == 0xFF {
		latchedStr = "0xFF (shadow_link alloc failed — falling back to mUserArea=this_)"
	}
	fmt.Printf("shadow_mode = %d  [%s]   latched=%s\n", v, labels[v], latchedStr)
	if v == 4 {
		ringState, _ := d.ReadAbsolute(mailboxBase+mailboxEchoRingState, 1)
		echoDelay, _ := d.ReadAbsolute(mailboxBase+mailboxEchoDelay, 1)
		rsMsg := fmt.Sprintf("%d", ringState[0])
		switch ringState[0] {
		case 0:
			rsMsg = "0 (unallocated — give it a frame)"
		case 1:
			rsMsg = "1 (allocated)"
		case 0xFE:
			rsMsg = "0xFE (bad jointNum — check dbg_joint_num in dump)"
		case 0xFD:
			rsMsg = "0xFD (GameHeap alloc failed)"
		}
		fmt.Printf("echo_ring_state=%s  echo_delay=%d\n", rsMsg, echoDelay[0])
	}
}

// Smoke test for shadow_mode 5 (pose-feed) without any networking.
//
//   mirror — every tick, copy the live mpNodeMtx (already populated by
//            mini-Link's first calc) into pose_buf. Result should look
//            identical to mode 0 — proves the pose_buf -> mpNodeMtx
//            overwrite + double-calc plumbing is non-destructive.
//   freeze — capture mpNodeMtx ONCE and stop writing. Link #2 should
//            freeze in that pose while Link #1 keeps animating —
//            decisive proof that pose_buf is the actual pose source.
//
// Either way the dolphin-side allocation happens lazily on the first
// frame after we set shadow_mode=5, so we poll pose_buf_state until C
// publishes pose_buf_ptr before sending any pose data.
func runPoseTest(mode string, durSec int) {
	if mode != "mirror" && mode != "freeze" {
		fmt.Println("Usage: ww pose-test [mirror|freeze] [seconds]")
		os.Exit(1)
	}
	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()

	// Flip to mode 5; C will lazy-alloc pose_buf and publish the pointer.
	if err := d.WriteAbsolute(mailboxBase+mailboxShadowMode, []byte{5}); err != nil {
		fmt.Printf("set shadow_mode failed: %v\n", err)
		os.Exit(1)
	}

	var poseBufPtr uint32
	var jointCount uint16
	for i := 0; i < 60; i++ {
		time.Sleep(50 * time.Millisecond)
		state, _ := d.ReadAbsolute(mailboxBase+mailboxPoseBufState(0), 1)
		if len(state) == 0 {
			continue
		}
		switch state[0] {
		case 1:
			poseBufPtr, _ = d.ReadU32(mailboxBase + mailboxPoseBufPtr(0))
			jcBytes, _ := d.ReadAbsolute(mailboxBase+mailboxPoseJointCount(0), 2)
			if len(jcBytes) == 2 {
				jointCount = binary.BigEndian.Uint16(jcBytes)
			}
		case 0xFE:
			fmt.Println("pose_buf_state = 0xFE (bad joint count — is mini-Link rendering?)")
			os.Exit(1)
		case 0xFD:
			fmt.Println("pose_buf_state = 0xFD (GameHeap alloc failed)")
			os.Exit(1)
		}
		if poseBufPtr != 0 {
			break
		}
	}
	if poseBufPtr == 0 {
		fmt.Println("timed out waiting for pose_buf_ptr — did mode 5 ever fire? Check ./ww-multiplayer.exe dump")
		os.Exit(1)
	}
	if jointCount == 0 || jointCount > 128 {
		fmt.Printf("bad joint count from C: %d\n", jointCount)
		os.Exit(1)
	}
	poseSize := int(jointCount) * 48
	fmt.Printf("pose_buf_ptr = 0x%08X  joint_count = %d  pose_size = %d B\n", poseBufPtr, jointCount, poseSize)

	bumpSeq := func() {
		seqBytes, _ := d.ReadAbsolute(mailboxBase+mailboxPoseSeq(0), 1)
		next := byte(0)
		if len(seqBytes) == 1 {
			next = seqBytes[0] + 1
		}
		d.WriteAbsolute(mailboxBase+mailboxPoseSeq(0), []byte{next})
	}

	// Read Link #1's mpNodeMtx, NOT mini-Link's. Mini-Link's mpNodeMtx is
	// what mode 5 overwrites every draw frame, so reading it back gives us
	// our own previously-written pose (== freeze). Link #1's mpNodeMtx is
	// the real animation source: daPy_lk_c + 0x032C = J3DModel* mpCLModel
	// (per zeldaret/tww decomp), then J3DModel + 0x8C = Mtx* mpNodeMtx.
	// This is also exactly what broadcast-pose will read on the sender.
	const linkMpCLModelOff = 0x032C
	const j3dModelMpNodeMtxOff = 0x8C
	captureFromLinkOne := func() []byte {
		linkPtr, err := d.GetLinkPtr()
		if err != nil || linkPtr == 0 {
			return nil
		}
		modelPtr, err := d.ReadU32(linkPtr + linkMpCLModelOff)
		if err != nil || modelPtr < 0x80000000 || modelPtr >= 0x81800000 {
			return nil
		}
		nodePtr, err := d.ReadU32(modelPtr + j3dModelMpNodeMtxOff)
		if err != nil || nodePtr < 0x80000000 || nodePtr >= 0x81800000 {
			return nil
		}
		data, err := d.ReadAbsolute(nodePtr, poseSize)
		if err != nil {
			return nil
		}
		return data
	}

	deadline := time.Now().Add(time.Duration(durSec) * time.Second)

	if mode == "freeze" {
		// Wait briefly for at least one calc cycle to populate mpNodeMtx,
		// capture once, write to pose_buf, then sit and watch.
		var captured []byte
		for i := 0; i < 30 && captured == nil; i++ {
			time.Sleep(50 * time.Millisecond)
			captured = captureFromLinkOne()
		}
		if captured == nil {
			fmt.Println("could not capture mpNodeMtx — is mini-Link calc running?")
			os.Exit(1)
		}
		if err := d.WriteAbsolute(poseBufPtr, captured); err != nil {
			fmt.Printf("write pose_buf failed: %v\n", err)
			os.Exit(1)
		}
		bumpSeq()
		fmt.Printf("freeze: captured %d B and wrote to pose_buf. Link #2 should hold this pose for %ds while Link #1 moves.\n", len(captured), durSec)
		for time.Now().Before(deadline) {
			time.Sleep(500 * time.Millisecond)
		}
		fmt.Println("done.")
		return
	}

	// mirror mode: continuously echo live mpNodeMtx -> pose_buf
	fmt.Printf("mirror: copying live mpNodeMtx -> pose_buf for %ds. Link #2 should look identical to mode 0.\n", durSec)
	// One-shot debug: prove the read chain works and the matrices change.
	if linkPtr, _ := d.GetLinkPtr(); linkPtr != 0 {
		modelPtr, _ := d.ReadU32(linkPtr + 0x032C)
		nodePtr, _ := d.ReadU32(modelPtr + 0x8C)
		fmt.Printf("[debug] link=0x%08X  link+0x032C=0x%08X  +0x8C=0x%08X\n", linkPtr, modelPtr, nodePtr)
		if nodePtr != 0 {
			row, _ := d.ReadAbsolute(nodePtr, 16)
			fmt.Printf("[debug] mpNodeMtx[0] row0: % X\n", row)
		}
	}
	ticks := 0
	var lastFirstRow []byte
	changes := 0
	nextProbe := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data := captureFromLinkOne()
		if data != nil {
			if err := d.WriteAbsolute(poseBufPtr, data); err == nil {
				bumpSeq()
				ticks++
				if lastFirstRow != nil {
					for i := 0; i < 16 && i < len(data); i++ {
						if data[i] != lastFirstRow[i] {
							changes++
							break
						}
					}
				}
				lastFirstRow = append(lastFirstRow[:0], data[:16]...)
			}
		}
		if time.Now().After(nextProbe) {
			nextProbe = time.Now().Add(2 * time.Second)
			wrote := uint32(0)
			if data != nil {
				wrote = binary.BigEndian.Uint32(data[:4])
			}
			cSawPose, _ := d.ReadU32(mailboxBase + mailboxDbgPoseFirstWord)
			cSawNode, _ := d.ReadU32(mailboxBase + mailboxDbgNodeMtxFirst)
			fmt.Printf("[probe] go-wrote=%08X  c-saw-pose=%08X  c-saw-node-after-calc=%08X\n",
				wrote, cSawPose, cSawNode)
		}
		time.Sleep(16 * time.Millisecond)
	}
	fmt.Printf("\ndone. %d ticks written, %d distinct first-row values seen.\n", ticks, changes)
}

func runEchoDelay(s string) {
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 || v > 59 {
		fmt.Println("delay must be 0..59 (frames; ring holds 60)")
		os.Exit(1)
	}
	d, err := dolphin.Find("GZLE01")
	if err != nil { fmt.Println(err); os.Exit(1) }
	defer d.Close()
	if err := d.WriteAbsolute(mailboxBase+mailboxEchoDelay, []byte{byte(v)}); err != nil {
		fmt.Printf("write failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("echo_delay = %d frames (~%.2fs at 60fps)\n", v, float64(v)/60.0)
}

func runDump() {
	d, err := dolphin.Find("GZLE01")
	if err != nil { fmt.Println(err); os.Exit(1) }
	defer d.Close()

	// Mailbox header (spawn_trigger, progress, pads)
	hdr, _ := d.ReadAbsolute(mailboxBase, 16)
	fmt.Printf("mailbox hdr  @ 0x%08X: ", mailboxBase)
	for j, b := range hdr {
		fmt.Printf("%02X", b)
		if j%4 == 3 { fmt.Print(" ") }
	}
	fmt.Println()

	if progress, err := d.ReadU32(mailboxBase + 0x04); err == nil {
		fmt.Printf("progress: %d (1=gate 3=link-ready 5=spawn-queued 8=spawned 9=syncing 10=actor-lost)\n", progress)
	}

	// Shadow-instance experiment state (mailbox +0x90 / +0x91)
	if sh, err := d.ReadAbsolute(mailboxBase+mailboxShadowMode, 2); err == nil {
		fmt.Printf("shadow_mode: %d   shadow_latched: %d\n", sh[0], sh[1])
	}
	// Mini-link J3DModelData pointer + current basicMtxCalc value
	if md, err := d.ReadU32(mailboxBase + 0x94); err == nil {
		if bc, err := d.ReadU32(mailboxBase + 0x98); err == nil {
			fmt.Printf("mini_link_data: 0x%08X   basicMtxCalc (@+0x24): 0x%08X\n", md, bc)
		}
	}
	// Echo-ring diagnostics (populated after mini-link exists; echo_ring_state
	// only becomes non-zero after first shadow_mode=4 frame).
	if jn, err := d.ReadAbsolute(mailboxBase+mailboxDbgJointNum, 2); err == nil {
		jointNum := binary.BigEndian.Uint16(jn)
		nmp, _ := d.ReadU32(mailboxBase + mailboxDbgNodeMtxPtr)
		rs, _ := d.ReadAbsolute(mailboxBase+mailboxEchoRingState, 1)
		ed, _ := d.ReadAbsolute(mailboxBase+mailboxEchoDelay, 1)
		fmt.Printf("echo: joint_num=%d  mpNodeMtx=0x%08X  ring_state=0x%02X  delay=%d\n",
			jointNum, nmp, rs[0], ed[0])
	}
	// Pose-feed (mode 5) diagnostics. pose_buf_state stays 0 until the
	// first mode-5 frame fires the lazy alloc.
	for slot := 0; slot < maxRemoteLinks; slot++ {
		pb, err := d.ReadU32(mailboxBase + mailboxPoseBufPtr(slot))
		if err != nil {
			continue
		}
		jc, _ := d.ReadAbsolute(mailboxBase+mailboxPoseJointCount(slot), 2)
		ps, _ := d.ReadAbsolute(mailboxBase+mailboxPoseBufState(slot), 1)
		sq, _ := d.ReadAbsolute(mailboxBase+mailboxPoseSeq(slot), 1)
		jcVal := uint16(0)
		if len(jc) == 2 {
			jcVal = binary.BigEndian.Uint16(jc)
		}
		fmt.Printf("pose[%d]: buf=0x%08X  joint_count=%d  state=0x%02X  seq=%d\n",
			slot, pb, jcVal, ps[0], sq[0])
	}

	// Per-slot state
	for i := 0; i < maxPuppets; i++ {
		data, _ := d.ReadAbsolute(slotAddr(i, 0), puppetSlotSize)
		fmt.Printf("slot %d       @ 0x%08X: ", i, slotAddr(i, 0))
		for j, b := range data {
			fmt.Printf("%02X", b)
			if j%4 == 3 { fmt.Print(" ") }
		}
		fmt.Println()
	}

	// Other useful addrs
	ptrs := []uint32{0x80006338, 0x803C4C0C, 0x803F66C0, 0x80410000}
	sizes := []int{8, 4, 4, 16}
	for i, addr := range ptrs {
		data, _ := d.ReadAbsolute(addr, sizes[i])
		fmt.Printf("             @ 0x%08X: ", addr)
		for j, b := range data {
			fmt.Printf("%02X", b)
			if j%4 == 3 { fmt.Print(" ") }
		}
		fmt.Println()
	}
}

// runInspectMaterials walks Link's shared J3DModelData and prints every
// material's index, name, matColor, and texNo[0..7]. Used to identify
// which material index corresponds to "tunic" vs "eye_tex" vs skin etc.
// for future work (per-material color tint, eye-render fix).
//
// Offsets from zeldaret/tww decomp (commit 6aa7ba91):
//   J3DModelData + 0x58 = J3DMaterialTable (inline)
//     + 0x04 = u16 mMaterialNum
//     + 0x08 = J3DMaterial**    (array of per-index ptrs)
//     + 0x0C = JUTNameTab*      (index -> name lookup)
//   J3DMaterial + 0x24 = J3DColorBlock*  (matColor[0..1] at +0x04)
//   J3DMaterial + 0x2C = J3DTevBlock*    (mTexNo[8] at +0x08, u16 each)
// JUTNameTab layout (JSystem):
//   +0x00: u16 count
//   +0x02: u16 pad
//   +0x04..: {u16 hash, u16 offset-from-NameTab-base}[count]
//   strings follow (ASCIIZ)
func runInspectMaterials() {
	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()

	// Resolve Link's live J3DModelData via PLAYER_PTR_ARRAY[0] + 0x0328.
	const daPyMpCLModelDataOff = 0x0328
	linkPtr, err := d.ReadU32(0x803CA754)
	if err != nil || linkPtr < 0x80000000 || linkPtr >= 0x81800000 {
		fmt.Println("Link not loaded (PLAYER_PTR_ARRAY[0] null). Load a save and try again.")
		os.Exit(1)
	}
	dataPtr, err := d.ReadU32(linkPtr + daPyMpCLModelDataOff)
	if err != nil || dataPtr < 0x80000000 || dataPtr >= 0x81800000 {
		fmt.Printf("Link's mpCLModelData is null (linkPtr=0x%08X).\n", linkPtr)
		os.Exit(1)
	}
	fmt.Printf("Link actor       : 0x%08X\n", linkPtr)
	fmt.Printf("Link J3DModelData: 0x%08X\n\n", dataPtr)

	matTableBase := dataPtr + 0x58
	countBytes, _ := d.ReadAbsolute(matTableBase+0x04, 2)
	if len(countBytes) != 2 {
		fmt.Println("failed to read material count")
		os.Exit(1)
	}
	count := binary.BigEndian.Uint16(countBytes)
	matArr, _ := d.ReadU32(matTableBase + 0x08)
	nameTab, _ := d.ReadU32(matTableBase + 0x0C)
	fmt.Printf("material count   : %d\n", count)
	fmt.Printf("material array   : 0x%08X\n", matArr)
	fmt.Printf("name table       : 0x%08X\n\n", nameTab)

	// Preload the name table count so we can bounds-check per-index reads.
	var tabCount uint16
	if nameTab >= 0x80000000 && nameTab < 0x81800000 {
		if cb, _ := d.ReadAbsolute(nameTab, 2); len(cb) == 2 {
			tabCount = binary.BigEndian.Uint16(cb)
		}
		// Raw dump of nameTab neighborhood for layout reverse-engineering.
		// 0x200 bytes covers header + 24 item entries + some string pool.
		const dumpSize = 0x200
		if raw, _ := d.ReadAbsolute(nameTab, dumpSize); len(raw) == dumpSize {
			fmt.Printf("raw nameTab[0..0x%x] @ 0x%08X:\n", dumpSize, nameTab)
			for row := 0; row < dumpSize/16; row++ {
				fmt.Printf("  +0x%03X: ", row*16)
				for col := 0; col < 16; col++ {
					fmt.Printf("%02X ", raw[row*16+col])
				}
				fmt.Printf(" |")
				for col := 0; col < 16; col++ {
					c := raw[row*16+col]
					if c >= 0x20 && c < 0x7F {
						fmt.Printf("%c", c)
					} else {
						fmt.Printf(".")
					}
				}
				fmt.Println("|")
			}
		}
		fmt.Printf("parsed tabCount = %d\n\n", tabCount)
	}

	resolveName := func(idx int) string {
		if nameTab == 0 || uint16(idx) >= tabCount {
			return "?"
		}
		// item at +0x04 + idx*4: {u16 hash, u16 offset}
		item, _ := d.ReadAbsolute(nameTab+4+uint32(idx)*4, 4)
		if len(item) != 4 {
			return "?"
		}
		offset := binary.BigEndian.Uint16(item[2:4])
		// ASCIIZ string at nameTab + offset. Read up to 64 bytes then trim at null.
		sbytes, _ := d.ReadAbsolute(nameTab+uint32(offset), 64)
		n := 0
		for n < len(sbytes) && sbytes[n] != 0 {
			n++
		}
		return string(sbytes[:n])
	}

	fmt.Printf("%-4s %-32s %-10s %-10s %s\n", "idx", "name", "mat_ptr", "matColor", "texNo[0..7]")
	fmt.Println(strings.Repeat("-", 110))
	for i := 0; i < int(count); i++ {
		matPtr, err := d.ReadU32(matArr + uint32(i)*4)
		if err != nil || matPtr < 0x80000000 || matPtr >= 0x81800000 {
			continue
		}
		name := resolveName(i)
		colBlock, _ := d.ReadU32(matPtr + 0x24)
		tevBlock, _ := d.ReadU32(matPtr + 0x2C)
		matColor := []byte{0, 0, 0, 0}
		if colBlock >= 0x80000000 && colBlock < 0x81800000 {
			if mc, _ := d.ReadAbsolute(colBlock+0x04, 4); len(mc) == 4 {
				matColor = mc
			}
		}
		texNos := make([]uint16, 8)
		if tevBlock >= 0x80000000 && tevBlock < 0x81800000 {
			if tn, _ := d.ReadAbsolute(tevBlock+0x08, 16); len(tn) == 16 {
				for s := 0; s < 8; s++ {
					texNos[s] = binary.BigEndian.Uint16(tn[s*2 : s*2+2])
				}
			}
		}
		// Format: only print texNo slots that look used (!= 0xFFFF sentinel).
		var texStr strings.Builder
		for s, v := range texNos {
			if v == 0xFFFF {
				continue
			}
			if texStr.Len() > 0 {
				texStr.WriteString(",")
			}
			fmt.Fprintf(&texStr, "%d:0x%04X", s, v)
		}
		fmt.Printf("[%3d] %-32s 0x%08X %02X%02X%02X%02X  %s\n",
			i, truncateName(name, 32), matPtr,
			matColor[0], matColor[1], matColor[2], matColor[3], texStr.String())
	}
}

func truncateName(s string, n int) string {
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}

// resolveMaterialColorBlock returns the colorBlock address for material
// index `idx` in Link's live J3DModelData. Shared helper used by both
// tint commands and the inspect command.
func resolveMaterialColorBlock(d *dolphin.Dolphin, idx int) (colBlock uint32, matPtr uint32, err error) {
	linkPtr, err := d.ReadU32(0x803CA754)
	if err != nil || linkPtr < 0x80000000 || linkPtr >= 0x81800000 {
		return 0, 0, fmt.Errorf("Link not loaded")
	}
	dataPtr, err := d.ReadU32(linkPtr + 0x0328)
	if err != nil || dataPtr < 0x80000000 || dataPtr >= 0x81800000 {
		return 0, 0, fmt.Errorf("mpCLModelData is null")
	}
	matTableBase := dataPtr + 0x58
	countBytes, _ := d.ReadAbsolute(matTableBase+0x04, 2)
	if len(countBytes) != 2 {
		return 0, 0, fmt.Errorf("failed to read material count")
	}
	count := binary.BigEndian.Uint16(countBytes)
	if idx < 0 || idx >= int(count) {
		return 0, 0, fmt.Errorf("material index %d out of range [0, %d)", idx, count)
	}
	matArr, _ := d.ReadU32(matTableBase + 0x08)
	matPtr, err = d.ReadU32(matArr + uint32(idx)*4)
	if err != nil || matPtr < 0x80000000 || matPtr >= 0x81800000 {
		return 0, 0, fmt.Errorf("material[%d] pointer invalid: 0x%08X", idx, matPtr)
	}
	colBlock, err = d.ReadU32(matPtr + 0x24)
	if err != nil || colBlock < 0x80000000 || colBlock >= 0x81800000 {
		return 0, matPtr, fmt.Errorf("material[%d] has no colorBlock (got 0x%08X)", idx, colBlock)
	}
	return colBlock, matPtr, nil
}

// runTintMaterial writes matColor[0] of material `idx` to the given
// 8-hex-digit RGBA value (or resets to white). J3DModelData is shared
// across all Link instances (Link #1, mini-Link) so this affects every
// render path on every Dolphin using that archive.
func runTintMaterial(idx int, colorArg string) {
	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	colBlock, matPtr, err := resolveMaterialColorBlock(d, idx)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	var rgba []byte
	if colorArg == "reset" || colorArg == "restore" {
		rgba = []byte{0xFF, 0xFF, 0xFF, 0xFF}
	} else {
		v, err := strconv.ParseUint(strings.TrimPrefix(colorArg, "0x"), 16, 32)
		if err != nil {
			fmt.Printf("bad rgba-hex %q: %v\n", colorArg, err)
			os.Exit(1)
		}
		rgba = []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
	}
	if err := d.WriteAbsolute(colBlock+0x04, rgba); err != nil {
		fmt.Printf("write failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("material[%d] mat=0x%08X colorBlock=0x%08X matColor[0] := %02X%02X%02X%02X\n",
		idx, matPtr, colBlock, rgba[0], rgba[1], rgba[2], rgba[3])
}

// runTintStage swaps a single material's texNo[stage] to 0 and waits for
// the user to press Enter before restoring. Used to probe materials
// whose stage 0 is already 0 (e.g. eye materials often leave stage 0
// unused and drive the real tex through stage 1).
func runTintStage(matIdx, stage int) {
	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	linkPtr, err := d.ReadU32(0x803CA754)
	if err != nil || linkPtr < 0x80000000 || linkPtr >= 0x81800000 {
		fmt.Println("Link not loaded")
		os.Exit(1)
	}
	dataPtr, _ := d.ReadU32(linkPtr + 0x0328)
	countBytes, _ := d.ReadAbsolute(dataPtr+0x58+0x04, 2)
	count := int(binary.BigEndian.Uint16(countBytes))
	matArr, _ := d.ReadU32(dataPtr + 0x58 + 0x08)
	if matIdx < 0 || matIdx >= count {
		fmt.Printf("mat-idx %d out of range [0, %d)\n", matIdx, count)
		os.Exit(1)
	}
	matPtr, _ := d.ReadU32(matArr + uint32(matIdx)*4)
	tb, _ := d.ReadU32(matPtr + 0x2C)
	if tb < 0x80000000 || tb >= 0x81800000 {
		fmt.Printf("material[%d] has no tevBlock\n", matIdx)
		os.Exit(1)
	}
	texAddr := tb + 0x08 + uint32(stage)*2
	orig, _ := d.ReadAbsolute(texAddr, 2)
	if len(orig) != 2 {
		fmt.Println("failed to read current texNo")
		os.Exit(1)
	}
	origVal := binary.BigEndian.Uint16(orig)
	fmt.Printf("material[%d] stage %d: texNo = 0x%04X -> 0x0000\n", matIdx, stage, origVal)
	fmt.Println("Watch Link in-game. Press Enter to restore.")

	// Signal-safe restore on Ctrl+C.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		d.WriteAbsolute(texAddr, orig)
		fmt.Println("\nInterrupted — restored.")
		os.Exit(0)
	}()

	d.WriteAbsolute(texAddr, []byte{0x00, 0x00})
	bufio.NewReader(os.Stdin).ReadString('\n')
	d.WriteAbsolute(texAddr, orig)
	fmt.Printf("Restored material[%d] stage %d texNo -> 0x%04X\n", matIdx, stage, origVal)
}

// runTintPick steps through materials interactively. Same texNo[0] swap
// as runTintCycle but waits for user keystrokes. Enter = next, 'p' =
// prev, 'j <N>' = jump to idx N, 'q' = restore all + exit.
func runTintPick() {
	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	linkPtr, err := d.ReadU32(0x803CA754)
	if err != nil || linkPtr < 0x80000000 || linkPtr >= 0x81800000 {
		fmt.Println("Link not loaded")
		os.Exit(1)
	}
	dataPtr, _ := d.ReadU32(linkPtr + 0x0328)
	countBytes, _ := d.ReadAbsolute(dataPtr+0x58+0x04, 2)
	count := int(binary.BigEndian.Uint16(countBytes))
	matArr, _ := d.ReadU32(dataPtr + 0x58 + 0x08)

	type slot struct {
		tevBlock uint32
		origTex0 uint16
	}
	slots := make([]slot, count)
	for i := 0; i < count; i++ {
		matPtr, _ := d.ReadU32(matArr + uint32(i)*4)
		if matPtr < 0x80000000 || matPtr >= 0x81800000 {
			continue
		}
		tb, _ := d.ReadU32(matPtr + 0x2C)
		if tb < 0x80000000 || tb >= 0x81800000 {
			continue
		}
		tn, _ := d.ReadAbsolute(tb+0x08, 2)
		if len(tn) != 2 {
			continue
		}
		slots[i] = slot{tevBlock: tb, origTex0: binary.BigEndian.Uint16(tn)}
	}

	apply := func(i int, tex uint16) {
		if slots[i].tevBlock == 0 {
			return
		}
		d.WriteAbsolute(slots[i].tevBlock+0x08, []byte{byte(tex >> 8), byte(tex)})
	}
	restoreAll := func() {
		for i, s := range slots {
			if s.tevBlock != 0 {
				apply(i, s.origTex0)
			}
		}
	}
	// Always restore on exit, even if the caller Ctrl+C's.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		restoreAll()
		fmt.Println("\nInterrupted — all materials restored.")
		os.Exit(0)
	}()
	defer restoreAll()

	fmt.Printf("Interactive picker — %d materials available.\n", count)
	fmt.Println("Commands: Enter = next  |  p = prev  |  j <N> = jump  |  q = quit")
	fmt.Println("Materials with texNo[0]==0 are skipped (the swap would be a no-op).")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)
	cur := -1
	advance := func(dir int) {
		for {
			cur += dir
			if cur < 0 || cur >= count {
				return
			}
			if slots[cur].tevBlock != 0 && slots[cur].origTex0 != 0 {
				return
			}
		}
	}
	advance(1) // land on first non-skipped material

	for cur >= 0 && cur < count {
		apply(cur, 0)
		fmt.Printf("[%3d] tev=0x%08X  texNo[0]: 0x%04X -> 0x0000 (watch Link) > ",
			cur, slots[cur].tevBlock, slots[cur].origTex0)
		line, err := reader.ReadString('\n')
		if err != nil {
			apply(cur, slots[cur].origTex0)
			break
		}
		apply(cur, slots[cur].origTex0) // restore before moving on
		line = strings.TrimSpace(line)
		switch {
		case line == "" || line == "n" || line == "next":
			advance(1)
		case line == "p" || line == "prev":
			advance(-1)
		case line == "q" || line == "quit":
			fmt.Println("quitting — restoring all materials.")
			return
		case strings.HasPrefix(line, "j"):
			var n int
			if _, err := fmt.Sscanf(strings.TrimPrefix(strings.TrimPrefix(line, "j"), " "), "%d", &n); err == nil {
				if n >= 0 && n < count {
					cur = n
					// If jumped to a skipped one, auto-advance forward
					if slots[cur].tevBlock == 0 || slots[cur].origTex0 == 0 {
						advance(1)
					}
					continue
				}
				fmt.Printf("index %d out of range [0,%d)\n", n, count)
			} else {
				fmt.Printf("could not parse jump index from %q\n", line)
			}
		default:
			fmt.Printf("unknown command %q — press Enter for next, 'p' prev, 'j N' jump, 'q' quit\n", line)
		}
	}
	fmt.Println("reached end of material list.")
}

// runTintCycle walks all 24 materials, swapping each one's texNo[0] to
// 0x0000 for `secs` seconds then restoring. mTexNo is known to be a
// per-frame patched field (btp animation rewrites it live for eye
// blinks), so writes show up instantly — unlike matColor which J3D bakes
// into a cached display list. The expected visual: the affected material
// renders with texture slot 0 (which is some shared default, usually
// drastically different from its normal tex), so it shows up as an
// obvious color/texture shift on Link.
func runTintCycle(secs int) {
	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer d.Close()
	linkPtr, err := d.ReadU32(0x803CA754)
	if err != nil || linkPtr < 0x80000000 || linkPtr >= 0x81800000 {
		fmt.Println("Link not loaded")
		os.Exit(1)
	}
	dataPtr, _ := d.ReadU32(linkPtr + 0x0328)
	countBytes, _ := d.ReadAbsolute(dataPtr+0x58+0x04, 2)
	count := int(binary.BigEndian.Uint16(countBytes))
	matArr, _ := d.ReadU32(dataPtr + 0x58 + 0x08)

	fmt.Printf("Cycling %d materials, %ds each. Watch Link in-game.\n", count, secs)
	fmt.Println("For each material we swap texNo[0] to 0x0000 so the mesh")
	fmt.Println("renders with a different texture — the changed patch is the hit.")
	fmt.Println("Press Ctrl+C to stop early; all materials are restored on exit.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	stopped := false

	// Resolve TevBlock addresses + record original texNo[0] for each
	// material up front. Skip materials whose texNo[0] is already 0x0000
	// (swap would be a no-op — nothing visible would change).
	type slot struct {
		tevBlock uint32
		origTex0 uint16
	}
	slots := make([]slot, count)
	for i := 0; i < count; i++ {
		matPtr, _ := d.ReadU32(matArr + uint32(i)*4)
		if matPtr < 0x80000000 || matPtr >= 0x81800000 {
			continue
		}
		tb, _ := d.ReadU32(matPtr + 0x2C)
		if tb < 0x80000000 || tb >= 0x81800000 {
			continue
		}
		tn, _ := d.ReadAbsolute(tb+0x08, 2)
		if len(tn) != 2 {
			continue
		}
		slots[i] = slot{tevBlock: tb, origTex0: binary.BigEndian.Uint16(tn)}
	}

	for i := 0; i < count && !stopped; i++ {
		s := slots[i]
		if s.tevBlock == 0 {
			fmt.Printf("[%3d] (no tevBlock, skipped)\n", i)
			continue
		}
		if s.origTex0 == 0x0000 {
			fmt.Printf("[%3d] tev=0x%08X  texNo[0]=0x0000 already (skipped)\n", i, s.tevBlock)
			continue
		}
		fmt.Printf("[%3d] tev=0x%08X  texNo[0] 0x%04X -> 0x0000\n",
			i, s.tevBlock, s.origTex0)
		d.WriteAbsolute(s.tevBlock+0x08, []byte{0x00, 0x00})
		select {
		case <-sigCh:
			stopped = true
		case <-time.After(time.Duration(secs) * time.Second):
		}
		d.WriteAbsolute(s.tevBlock+0x08, []byte{byte(s.origTex0 >> 8), byte(s.origTex0)})
	}

	// Final belt-and-suspenders: restore every material's original
	// texNo[0] in case something got stuck mid-cycle.
	for i := 0; i < count; i++ {
		s := slots[i]
		if s.tevBlock != 0 {
			d.WriteAbsolute(s.tevBlock+0x08, []byte{byte(s.origTex0 >> 8), byte(s.origTex0)})
		}
	}
	fmt.Println("Done. All materials restored to original texNo[0].")
}

func runCheck() {
	d, err := dolphin.Find("GZLE01")
	if err != nil { fmt.Println(err); os.Exit(1) }
	defer d.Close()

	hook, _ := d.ReadU32(0x80006338)
	fmt.Printf("Hook @ 0x80006338: 0x%08X", hook)
	if hook == 0x9421FFF0 {
		fmt.Println(" (original stwu - HOOK NOT APPLIED)")
	} else if hook>>26 == 18 {
		fmt.Println(" (branch - hook IS applied)")
	} else {
		fmt.Println(" (unexpected)")
	}

	// Verify injected code section is loaded (Freighter puts code at 0x803FD000)
	code, _ := d.ReadAbsolute(0x803FD000, 32)
	fmt.Printf("Code @ 0x803FD000: ")
	allZero := true
	for _, b := range code {
		fmt.Printf("%02X", b)
		if b != 0 { allZero = false }
	}
	if allZero {
		fmt.Println(" (ALL ZERO - code section NOT loaded)")
	} else {
		fmt.Println(" (code present)")
	}

	// Read BSS area around 0x803FCFC0 (static vars)
	bss, _ := d.ReadAbsolute(0x803FCFB0, 32)
	fmt.Printf("BSS  @ 0x803FCFB0: ")
	for _, b := range bss { fmt.Printf("%02X", b) }
	fmt.Println()

	// Read mailbox
	mb, _ := d.ReadAbsolute(0x803F6100, 32)
	fmt.Printf("Mail @ 0x803F6100: ")
	for _, b := range mb { fmt.Printf("%02X", b) }
	fmt.Println()

	// Player pointers
	for i := 0; i < 3; i++ {
		ptr, _ := d.ReadU32(0x803CA754 + uint32(i*4))
		fmt.Printf("PlayerPtr[%d]: 0x%08X\n", i, ptr)
	}
}

func runInject() {
	fmt.Println("=== Injecting Multiplayer Code ===")
	fmt.Println()

	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	defer d.Close()

	fmt.Println("Writing PPC code to Dolphin memory...")
	if err := d.InjectMultiplayer(); err != nil {
		fmt.Printf("Injection failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Code injected successfully!")

	// Verify the code was written
	check, _ := d.ReadAbsolute(0x803FCF20, 4)
	fmt.Printf("Verify @ 0x803FCF20: %02X%02X%02X%02X\n", check[0], check[1], check[2], check[3])

	// Check if the OnFrame hook is active
	hookInst, _ := d.ReadU32(0x80006338)
	fmt.Printf("Hook @ 0x80006338: 0x%08X ", hookInst)
	if hookInst == 0x483F6DD1 {
		fmt.Println("(BL to multiplayer_update - ACTIVE)")
	} else if hookInst == 0x9421FFF0 {
		fmt.Println("(original stwu - HOOK NOT ACTIVE)")
		fmt.Println("\nEnable 'WW Multiplayer Hook' in Dolphin game properties -> Patches")
	} else {
		fmt.Printf("(unknown: 0x%08X)\n", hookInst)
	}

	// Wait for Link to load
	fmt.Println("\nWaiting for Link to load...")
	for i := 0; i < 300; i++ {
		linkPtr, _ := d.GetLinkPtr()
		if linkPtr != 0 {
			fmt.Printf("Link found at 0x%08X!\n", linkPtr)
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Continuously write spawn trigger and check for result
	fmt.Println("Spamming spawn trigger for 15 seconds...")
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		d.TriggerSpawn()

		p2ptr, _ := d.GetPlayer2Ptr()
		if p2ptr != 0 {
			fmt.Printf("\nPLAYER 2 SPAWNED at 0x%08X!!!\n", p2ptr)
			return
		}
		fmt.Print(".")
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Println("\nNo spawn detected after 15 seconds.")
	fmt.Println("The WriteProcessMemory -> JIT mapping gap may prevent the trigger from reaching the game.")
}

func runDebug() {
	fmt.Println("=== WW Multiplayer Debug Mode ===")
	fmt.Println()

	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	defer d.Close()
	fmt.Printf("Dolphin found. GC RAM: 0x%X\n\n", d.GCRamBase())

	fmt.Println("Reading position for 5 seconds...")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		pos, err := d.ReadPlayerPosition()
		if err != nil {
			fmt.Printf("  waiting... (%v)\n", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}
		fmt.Printf("  X:%10.1f  Y:%10.1f  Z:%10.1f  RotY:%6d\n",
			pos.PosX, pos.PosY, pos.PosZ, pos.RotY)
		time.Sleep(200 * time.Millisecond)
	}
	fmt.Println("\nDone.")
}

func runServer() {
	fmt.Println("=== WW Multiplayer Server ===")
	fmt.Println()

	srv := network.NewServer(25565)
	srv.OnLog = func(msg string) {
		fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05"), msg)
	}

	if err := srv.Start(); err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}

	// Run until interrupted
	fmt.Println("Press Ctrl+C to stop")
	select {}
}

func runFakeClient(name, addr string, centerX, centerZ float32) {
	fmt.Printf("=== Fake Client: %s -> %s  center=(%.1f, %.1f) ===\n\n", name, addr, centerX, centerZ)

	client := network.NewClient(name)
	client.OnLog = func(msg string) {
		fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05"), msg)
	}

	if err := client.Connect(addr); err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	defer client.Disconnect()

	fmt.Println("Connected! Walking in circles...")
	fmt.Println()

	// Walk in a circle near the passed center. Radius and Y are fixed —
	// radius 300 keeps the path visible; Y=200 is comfortably above Outset
	// ground level (~180).
	radius := float32(300.0)
	y := float32(200.0)
	angle := float64(0)

	for client.IsConnected() {
		angle += 0.05
		pos := &network.PlayerPosition{
			PosX: centerX + radius*float32(math.Cos(angle)),
			PosY: y,
			PosZ: centerZ + radius*float32(math.Sin(angle)),
			RotY: int16(angle * 10430.0), // Convert radians to GC rotation units
		}

		if err := client.SendPosition(pos); err != nil {
			fmt.Printf("Send error: %v\n", err)
			break
		}

		// Print remote players' positions
		remotes := client.GetRemotePlayers()
		for _, rp := range remotes {
			if rp.Position != nil {
				fmt.Printf("  [%s] X:%10.1f Y:%10.1f Z:%10.1f\r",
					rp.Name, rp.Position.PosX, rp.Position.PosY, rp.Position.PosZ)
			}
		}

		time.Sleep(50 * time.Millisecond)
	}

	fmt.Println("\nDisconnected.")
}

// runHost is the single-process host entry point: binds the TCP server on
// :25565, then spins up broadcast-pose + puppet-sync goroutines pointing at
// localhost. Replaces the old "run 3 terminals" workflow. Ctrl+C cancels
// the ctx, cleanly shuts down both goroutines and the server, and resets
// the patched ISO's mailbox so the next Dolphin frame doesn't keep rendering
// a stale Link #2.
func runHost(name string) {
	rep := report.Stdout{}
	ctx, cancel := cliMultiplayerContext(rep)
	defer cancel()
	if err := runHostSession(ctx, cancel, name, rep); err != nil {
		rep.Log(report.Err, err.Error())
		os.Exit(1)
	}
}

// runHostSession contains the actual host flow without any signal-handler
// or os.Exit assumptions, so the TUI can drive it from inside its own
// context without fighting Bubble Tea's Ctrl+C handling.
func runHostSession(ctx context.Context, cancel context.CancelFunc, name string, rep report.Reporter) error {
	if name == "" {
		name = "Host"
	}
	srv := network.NewServer(25565)
	srv.OnLog = func(msg string) {
		rep.Log(report.Info, fmt.Sprintf("[srv %s] %s", time.Now().Format("15:04:05"), msg))
	}
	if err := srv.Start(); err != nil {
		return fmt.Errorf("server start: %w", err)
	}

	report.Logf(rep, report.OK, "Hosting as %q on :25565.", name)
	if ips := listHostIPs(); len(ips) > 0 {
		rep.Log(report.Info, "Share one of these IPs with your friend:")
		for _, ip := range ips {
			report.Logf(rep, report.Info, "  %s", ip)
		}
	} else {
		rep.Log(report.Warn, "(could not auto-detect a LAN IP — check your network settings)")
	}
	rep.Log(report.Info, "Ctrl+C to stop.")

	runMultiplayerGoroutines(ctx, cancel, name, "localhost:25565", rep)

	srv.Stop()
	clearMultiplayerState()
	return nil
}

// runJoin is the single-process joiner entry point: just broadcast-pose +
// puppet-sync goroutines pointed at the host's :25565. Same signal handling
// and mailbox cleanup as runHost.
func runJoin(addr, name string) {
	rep := report.Stdout{}
	ctx, cancel := cliMultiplayerContext(rep)
	defer cancel()
	runJoinSession(ctx, cancel, addr, name, rep)
}

// runJoinSession is the signal-free, exit-free version that the TUI calls.
func runJoinSession(ctx context.Context, cancel context.CancelFunc, addr, name string, rep report.Reporter) {
	if name == "" {
		name = "Player"
	}
	// Default port to :25565 if the user passed a bare IP.
	if !strings.Contains(addr, ":") {
		addr = addr + ":25565"
	}

	report.Logf(rep, report.OK, "Joining %s as %q.", addr, name)
	rep.Log(report.Info, "Ctrl+C to stop.")

	runMultiplayerGoroutines(ctx, cancel, name, addr, rep)

	clearMultiplayerState()
}

// cliMultiplayerContext returns a cancellable context wired to SIGINT/SIGTERM.
// Only the CLI entry points should use this — the TUI installs its own
// Ctrl+C handling via Bubble Tea's KeyMsg, and a competing signal.Notify
// would race with it.
func cliMultiplayerContext(rep report.Reporter) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			rep.Log(report.Info, "Shutting down...")
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

// runMultiplayerGoroutines spawns broadcast-pose + puppet-sync against the
// given server address. Player name is passed as selfFilter so the two
// in-process clients don't self-echo on the co-located broadcast/puppet
// twin (the WW_SELF_NAME workaround from mplay2.sh, but automatic). Blocks
// until either goroutine exits or ctx is cancelled, then waits for both to
// finish before returning.
func runMultiplayerGoroutines(ctx context.Context, cancel context.CancelFunc, name, addr string, rep report.Reporter) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer cancel()
		if err := runBroadcastPoseCtx(ctx, name, addr, 0, rep); err != nil {
			report.Logf(rep, report.Err, "broadcast-pose: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		defer cancel()
		if err := runPuppetSyncCtx(ctx, name, addr, name, 0, rep); err != nil {
			report.Logf(rep, report.Err, "puppet-sync: %v", err)
		}
	}()

	<-ctx.Done()
	wg.Wait()
}

// listHostIPs walks the machine's non-loopback IPv4 addresses so `ww-multiplayer.exe
// host` can print something the joiner can type. Skips IPv6 (users don't
// want to type v6 literals), loopback (can't reach from another machine),
// and link-local.
func listHostIPs() []string {
	var out []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipnet.IP.To4()
		if ip == nil {
			continue
		}
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		out = append(out, ip.String())
	}
	return out
}

// clearMultiplayerState resets the patched-ISO mailbox so the next Dolphin
// frame stops rendering Link #2. Writes shadow_mode = 0 (the explicit kill
// switch) and zeros every pose_seqs[slot] so even if shadow_mode is flipped
// back to 5 later, nothing renders until a fresh pose arrives. All writes
// are best-effort: if Dolphin closed first, WriteAbsolute fails silently
// which is the correct behavior (the mailbox is gone with the process).
func clearMultiplayerState() {
	d, err := dolphin.Find("GZLE01")
	if err != nil {
		return
	}
	defer d.Close()
	d.WriteAbsolute(mailboxBase+mailboxShadowMode, []byte{0})
	for i := 0; i < maxRemoteLinks; i++ {
		d.WriteAbsolute(mailboxBase+mailboxPoseSeq(i), []byte{0})
	}
}
