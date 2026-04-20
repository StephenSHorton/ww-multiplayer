package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/dolphin"
	"github.com/StephenSHorton/ww-multiplayer/internal/network"
	"github.com/StephenSHorton/ww-multiplayer/internal/tui"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "debug":
			runDebug()
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
				fmt.Println("Usage: ww shadow-mode <0|1|2|3>  (0=baseline 1=refresh 2=freeze 3=null-basicMtxCalc)")
				os.Exit(1)
			}
			runShadowMode(os.Args[2])
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
	fmt.Println("Usage:")
	fmt.Println("  ww-multiplayer              Launch TUI")
	fmt.Println("  ww-multiplayer debug        Test Dolphin memory access")
	fmt.Println("  ww-multiplayer server       Start headless server on :25565")
	fmt.Println("  ww-multiplayer fake-client [name] [addr]")
	fmt.Println("                              Connect a fake client that walks in circles")
	fmt.Println("  ww-multiplayer help          Show this help")
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

// Mailbox layout (keep in sync with inject/include/mailbox.h).
const (
	mailboxBase    = 0x80410F00
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
func runPuppetSync(name, addr string) {
	fmt.Printf("=== Puppet Sync: %s <- %s ===\n\n", name, addr)

	d, err := dolphin.Find("GZLE01")
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	defer d.Close()
	fmt.Println("Dolphin found.")

	client := network.NewClient(name)
	client.OnLog = func(msg string) {
		fmt.Printf("[net] %s\n", msg)
	}
	if err := client.Connect(addr); err != nil {
		fmt.Printf("connect: %v\n", err)
		os.Exit(1)
	}
	defer client.Disconnect()

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

	for client.IsConnected() {
		remotes := client.GetRemotePlayers()
		seen := map[byte]bool{}
		for _, rp := range remotes {
			if rp.Position == nil {
				continue
			}
			seen[rp.ID] = true
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
				d.WriteAbsolute(slotAddr(idx, slotOffAct), one)
				fmt.Printf("\nslot %d := player %d (%s)\n", idx, rp.ID, rp.Name)
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
		}

		// Release slots for remotes that have disconnected.
		for id, idx := range remoteToSlot {
			if !seen[id] {
				d.WriteAbsolute(slotAddr(idx, slotOffAct), zero)
				delete(remoteToSlot, id)
				slots[idx] = slotState{}
				fmt.Printf("\nslot %d freed (player %d left)\n", idx, id)
			}
		}

		time.Sleep(16 * time.Millisecond) // ~60 Hz
	}
	fmt.Println("\nDisconnected.")
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

func runShadowMode(s string) {
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 || v > 3 {
		fmt.Println("mode must be 0 (baseline), 1 (refresh), 2 (freeze), or 3 (null-basicMtxCalc)")
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
		"baseline (Link #1 direct)",
		"refresh (copy every frame)",
		"freeze (copy once)",
		"null basicMtxCalc around calc (probe shared pose controller)",
	}
	latchedStr := fmt.Sprintf("%d", latched[0])
	if latched[0] == 0xFF {
		latchedStr = "0xFF (alloc failed — falling back to baseline)"
	}
	fmt.Printf("shadow_mode = %d  [%s]   latched=%s\n", v, labels[v], latchedStr)
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
