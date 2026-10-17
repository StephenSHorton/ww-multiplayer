package engine

import (
	"fmt"
	"math"
	"os"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
)

func RunServer(port int) {
	fmt.Println("=== WW Multiplayer Server ===")
	fmt.Println()

	srv := network.NewServer(port)
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

func RunFakeClient(name, addr string, centerX, centerZ float32) {
	fmt.Printf("=== Fake Client: %s -> %s  center=(%.1f, %.1f) ===\n\n", name, addr, centerX, centerZ)

	client := NewPlayerClient(name)
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
