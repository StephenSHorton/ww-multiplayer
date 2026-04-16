package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
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
			if len(os.Args) > 2 {
				name = os.Args[2]
			}
			if len(os.Args) > 3 {
				addr = os.Args[3]
			}
			runFakeClient(name, addr)
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

func runDump() {
	d, err := dolphin.Find("GZLE01")
	if err != nil { fmt.Println(err); os.Exit(1) }
	defer d.Close()

	addrs := []uint32{0x803C4C0C, 0x80002348, 0x803FD1E8, 0x80006338, 0x80006338}
	sizes := []int{16, 64, 64, 32, 32}
	for i, addr := range addrs {
		data, _ := d.ReadAbsolute(addr, sizes[i])
		fmt.Printf("0x%08X: ", addr)
		if data != nil {
			for j, b := range data {
				fmt.Printf("%02X", b)
				if j%4 == 3 { fmt.Print(" ") }
			}
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

func runFakeClient(name, addr string) {
	fmt.Printf("=== Fake Client: %s -> %s ===\n\n", name, addr)

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

	// Walk in a circle near a common starting position
	centerX := float32(-199000.0)
	centerZ := float32(316000.0)
	radius := float32(500.0)
	y := float32(80.0)
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
