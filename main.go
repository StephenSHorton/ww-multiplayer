// To regenerate the Windows icon resource (ww_windows.syso) after changing
// docs/img/icon.ico, run:
//   go install github.com/akavel/rsrc@latest
//   rsrc -ico docs/img/icon.ico -o ww_windows.syso
// Go's linker auto-includes any *.syso in the main package; the _windows
// suffix is a build constraint so non-Windows builds skip it.
//go:generate rsrc -ico docs/img/icon.ico -o ww_windows.syso

package main

import (
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
	"github.com/StephenSHorton/ww-multiplayer/internal/tui"
)

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
		case "help":
			printHelp()
		default:
			fmt.Printf("Unknown command: %s\n", os.Args[1])
			printHelp()
			os.Exit(1)
		}
		return
	}

	if err := tui.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Println("Wind Waker Multiplayer")
	fmt.Println()
	fmt.Println("Play multiplayer:")
	fmt.Println("  ww.exe host [name]                        Host a session on :25565 (one process per player)")
	fmt.Println("  ww.exe join <host-ip> [name]              Join a host's session (host-ip is what `host` prints)")
	fmt.Println()
	fmt.Println("Patch an ISO:")
	fmt.Println("  ww.exe patch <vanilla.iso|.ciso> [out.iso]")
	fmt.Println("                                            Splice the multiplayer mod into your own")
	fmt.Println("                                            legitimate copy of Wind Waker (GZLE01)")
	fmt.Println()
	fmt.Println("Lower-level CLIs (used by scripts/mplay2.sh):")
	fmt.Println("  ww.exe server                             Start headless server on :25565")
	fmt.Println("  ww.exe broadcast-pose [name] [addr]       Stream local Link pose+pos to server")
	fmt.Println("  ww.exe puppet-sync [name] [addr]          Receive remotes; render as Link #2 / actor puppets")
	fmt.Println("  ww.exe fake-client [name] [addr]          Connect a fake client that walks in circles")
	fmt.Println("  ww.exe debug                              Test Dolphin memory access")
	fmt.Println("  ww.exe help                               Show this help")
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

// Same as broadcast-link but ALSO sends Link's full skeletal pose every
// tick. The receiver's puppet-sync writes that pose into mailbox.pose_buf
// and flips shadow_mode=5 so Link #2 animates from the wire instead of
// mirroring locally. Link's J3DModel is at daPy_lk_c + 0x032C; mpNodeMtx
// is at J3DModel + 0x8C; sizeof(Mtx) = 48; Link has 42 joints.
//
// Standalone-CLI wrapper. Preserves the `ww.exe broadcast-pose` entry
// point that scripts/mplay2.sh relies on.
func runBroadcastPose(name, addr string) {
	if err := runBroadcastPoseCtx(context.Background(), name, addr); err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
}

// runBroadcastPoseCtx is the goroutine-friendly variant. Returns an error
// instead of os.Exit so host/join can surface failures cleanly, and honors
// ctx cancellation so SIGINT doesn't have to wait for the next 50 ms tick.
func runBroadcastPoseCtx(ctx context.Context, name, addr string) error {
	fmt.Printf("=== Broadcast Link + Pose: %s -> %s ===\n\n", name, addr)
	d, err := dolphin.Find("GZLE01")
	if err != nil {
		return err
	}
	defer d.Close()
	client := network.NewClient(name)
	client.OnLog = func(msg string) { fmt.Printf("[net] %s\n", msg) }
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

	const linkMpCLModelOff = 0x032C
	const j3dModelMpNodeMtxOff = 0x8C
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

		// Pose. Walk daPy_lk_c -> mpCLModel -> mpNodeMtx and ship the
		// raw 2016 B AFTER localizing — subtract Link's world position
		// from each joint's translation column so the pose is relative
		// to Link's origin (rotation parts unchanged). Receiver re-adds
		// the remote's world position to land Link #2 at the right
		// world coords. Without localization, two-Dolphin works but
		// loopback always overlaps; with localization, the receiver can
		// render Link #2 anywhere it wants.
		linkPtr, err := d.GetLinkPtr()
		if err == nil && linkPtr != 0 && pos != nil {
			modelPtr, _ := d.ReadU32(linkPtr + linkMpCLModelOff)
			if modelPtr >= 0x80000000 && modelPtr < 0x81800000 {
				nodePtr, _ := d.ReadU32(modelPtr + j3dModelMpNodeMtxOff)
				if nodePtr >= 0x80000000 && nodePtr < 0x81800000 {
					data, err := d.ReadAbsolute(nodePtr, poseBytes)
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
		// Without this guard the Printf below dereferences nil and
		// crashes broadcast-pose, which drops the TCP connection and
		// leaves the receiving Dolphin's puppet frozen at the last
		// pose it ever received. Just print a sentinel and keep
		// looping; once Link reappears we resume sending.
		if pos != nil {
			fmt.Printf("  link+pose -> X:%10.1f Y:%8.1f Z:%10.1f\r",
				pos.PosX, pos.PosY, pos.PosZ)
		} else {
			fmt.Printf("  link+pose -> [no link — reload in progress?]   \r")
		}
		// 50 ms = 20 Hz. ~40 KB/s of pose data — trivial for LAN.
		select {
		case <-ctx.Done():
			// Fall through; IsConnected() will be false next iteration
			// (watcher goroutine already called Disconnect()).
		case <-time.After(50 * time.Millisecond):
		}
	}
	fmt.Println("\nDisconnected.")
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
	mailboxBase    = 0x80411F00
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
// join call runPuppetSyncCtx directly with an explicit filter name.
func runPuppetSync(name, addr string) {
	if err := runPuppetSyncCtx(context.Background(), name, addr, os.Getenv("WW_SELF_NAME")); err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
}

// runPuppetSyncCtx is the goroutine-friendly variant. Takes the self-filter
// name as a parameter (rather than reading WW_SELF_NAME) so host/join can
// plumb the player's name through without exporting an env var.
func runPuppetSyncCtx(ctx context.Context, name, addr, selfFilter string) error {
	fmt.Printf("=== Puppet Sync: %s <- %s ===\n\n", name, addr)

	d, err := dolphin.Find("GZLE01")
	if err != nil {
		return err
	}
	defer d.Close()
	fmt.Println("Dolphin found.")

	client := network.NewClient(name)
	client.OnLog = func(msg string) {
		fmt.Printf("[net] %s\n", msg)
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
	// Render offset added on top of the localized pose's re-application.
	// Default 0 (real multiplayer renders Link #2 at remote's actual
	// world coords). Set WW_LINK2_OFFSET_X / _Y / _Z for loopback demos
	// so Link #2 doesn't overlap your own Link.
	link2OffsetX := envFloat32("WW_LINK2_OFFSET_X", 0)
	link2OffsetY := envFloat32("WW_LINK2_OFFSET_Y", 0)
	link2OffsetZ := envFloat32("WW_LINK2_OFFSET_Z", 0)
	if link2OffsetX != 0 || link2OffsetY != 0 || link2OffsetZ != 0 {
		fmt.Printf("Link #2/3 render offset: (%.0f, %.0f, %.0f)\n",
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
						fmt.Printf("\npose-feed slot %d armed: pose_buf=0x%08X\n", slot, ptr)
					} else {
						fmt.Printf("\npose-feed slot %d re-armed: pose_buf=0x%08X (was 0x%08X)\n",
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
			fmt.Printf("\npose-feed slot %d disarmed: waiting for C-side re-alloc\n", slot)
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
					fmt.Printf("\npose-feed slot %d armed: pose_buf=0x%08X\n", slot, ptr)
					return true
				}
			}
			if len(state) == 1 && (state[0] == 0xFD || state[0] == 0xFE) {
				fmt.Printf("\npose-feed slot %d alloc failed (state=0x%02X)\n", slot, state[0])
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
	// from a manual `./ww.exe shadow-mode 0`, or any future reset path
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
	// WW_SELF_NAME into this arg; `ww.exe host/join` pass the player name
	// so users never have to know the env var exists.
	if selfFilter != "" {
		fmt.Printf("Filtering self-echo: remotes named %q will be ignored.\n", selfFilter)
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
					fmt.Printf("\nslot %d := player %d (%s)\n", idx, rp.ID, rp.Name)
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
						fmt.Printf("link slot %d := player %d (%s)\n", linkSlot, rp.ID, rp.Name)
						announced[rp.ID] = true
					}
					adjusted := make([]byte, len(rp.PoseMatrices))
					copy(adjusted, rp.PoseMatrices)
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
				fmt.Printf("\nslot %d freed (player %d left)\n", idx, id)
				if linkSlot, ok := remoteToLinkSlot[id]; ok {
					delete(remoteToLinkSlot, id)
					// Clear pose_seq so the C-side stops rendering this
					// link slot. C's mode-5 path gates entry on
					// pose_seqs[slot] != 0, so without this the
					// receiving Dolphin would render Link #2 frozen
					// at the last received pose forever after the
					// remote disconnects.
					d.WriteAbsolute(mailboxBase+mailboxPoseSeq(linkSlot), []byte{0})
					fmt.Printf("link slot %d freed (will be reassigned on next pose)\n", linkSlot)
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
	fmt.Println("\nDisconnected.")
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
		fmt.Println("timed out waiting for pose_buf_ptr — did mode 5 ever fire? Check ./ww.exe dump")
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
	if name == "" {
		name = "Host"
	}
	ctx, cancel := multiplayerContext()
	defer cancel()

	srv := network.NewServer(25565)
	srv.OnLog = func(msg string) {
		fmt.Printf("[srv %s] %s\n", time.Now().Format("15:04:05"), msg)
	}
	if err := srv.Start(); err != nil {
		fmt.Printf("server start: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Hosting as %q on :25565.\n", name)
	if ips := listHostIPs(); len(ips) > 0 {
		fmt.Println("Share one of these IPs with your friend:")
		for _, ip := range ips {
			fmt.Printf("  %s\n", ip)
		}
	} else {
		fmt.Println("(could not auto-detect a LAN IP — check your network settings)")
	}
	fmt.Println("Ctrl+C to stop.")

	runMultiplayerGoroutines(ctx, cancel, name, "localhost:25565")

	srv.Stop()
	clearMultiplayerState()
}

// runJoin is the single-process joiner entry point: just broadcast-pose +
// puppet-sync goroutines pointed at the host's :25565. Same signal handling
// and mailbox cleanup as runHost.
func runJoin(addr, name string) {
	if name == "" {
		name = "Player"
	}
	// Default port to :25565 if the user passed a bare IP.
	if !strings.Contains(addr, ":") {
		addr = addr + ":25565"
	}
	ctx, cancel := multiplayerContext()
	defer cancel()

	fmt.Printf("Joining %s as %q.\n", addr, name)
	fmt.Println("Ctrl+C to stop.")

	runMultiplayerGoroutines(ctx, cancel, name, addr)

	clearMultiplayerState()
}

// multiplayerContext returns a cancellable context wired to SIGINT/SIGTERM.
// The returned cancel() is safe to call multiple times; callers should defer
// it in addition to the signal handler firing.
func multiplayerContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			fmt.Println("\nShutting down...")
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
func runMultiplayerGoroutines(ctx context.Context, cancel context.CancelFunc, name, addr string) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer cancel()
		if err := runBroadcastPoseCtx(ctx, name, addr); err != nil {
			fmt.Printf("broadcast-pose: %v\n", err)
		}
	}()
	go func() {
		defer wg.Done()
		defer cancel()
		if err := runPuppetSyncCtx(ctx, name, addr, name); err != nil {
			fmt.Printf("puppet-sync: %v\n", err)
		}
	}()

	<-ctx.Done()
	wg.Wait()
}

// listHostIPs walks the machine's non-loopback IPv4 addresses so `ww.exe
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
