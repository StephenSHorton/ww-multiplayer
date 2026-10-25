package engine

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
)

// ServerOptions are a dedicated server's settings (the `server` command).
type ServerOptions struct {
	Port int
	// OwnerKey is the lobby owner key (`--owner-key`, WW_OWNER_KEY, or
	// one parseServerArgs made; "" none, which `server` never passes): a
	// lobby member who joins with it becomes the host (docs/10-lobby.md
	// §3 "Owner key").
	OwnerKey string
	// KeyGenerated: OwnerKey was made for this run (none was configured);
	// RunServer prints it once.
	KeyGenerated bool
	// Version is this build's version, for the status probe's answer.
	Version string
}

// dedicatedNoCoop is the no_coop notice of a dedicated server: the co-op
// hub runs in the hosting app's own server process (Server.SetCoop),
// which a `server` has none of.
const dedicatedNoCoop = "this is a dedicated server: co-op needs the host's own app to host the session; plain multiplayer works here"

// RunServer runs a dedicated server (docs/11-self-hosting.md) until
// Ctrl+C or SIGTERM (a container stop), then stops it cleanly, which
// closes every connection so the clients reconnect to the next one. Its
// lobby takes no co-op settings (dedicatedNoCoop). A bad owner key or a
// port that can't be bound exits 1.
func RunServer(o ServerOptions) {
	fmt.Println("=== WW Multiplayer Server ===")
	fmt.Println()

	srv := network.NewServer(o.Port)
	srv.AppVersion = o.Version
	srv.OnLog = func(msg string) {
		fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05"), msg)
	}
	srv.DenyCoop(dedicatedNoCoop)
	if o.OwnerKey != "" {
		if err := srv.SetOwnerKey(o.OwnerKey); err != nil {
			fmt.Printf("ERROR: owner key: %v\n", err)
			os.Exit(1)
		}
	}

	if err := srv.Start(); err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	switch {
	case o.KeyGenerated:
		// Printed once, here only (the log lines never carry it). A
		// restart makes a new one; WW_OWNER_KEY keeps one.
		fmt.Println("Owner key (made for this run; set WW_OWNER_KEY to keep one across restarts):")
		fmt.Println("  " + o.OwnerKey)
		fmt.Println("The app player who joins with it (Join, Advanced: dedicated server owner key) becomes the lobby's host.")
	case o.OwnerKey != "":
		fmt.Println("Owner key set: the app player who joins with it becomes the lobby's host.")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Println("Press Ctrl+C to stop")
	<-ctx.Done()
	srv.Stop()
	fmt.Println("Server stopped.")
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
