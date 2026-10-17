package feed

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player"
)

// PoseFakeLoop captures Link's current pose once and replays it as a separate
// "fake remote" so loopback testing can demonstrate multi-Link
// rendering without a second Dolphin. Position is sent as the live
// Link position + 1000 X offset so the fake walks alongside the real
// player, frozen in the captured pose. Receiver sees this as a remote
// distinct from broadcast-pose's player and assigns it a new link slot.
func PoseFakeLoop(env Env, name, addr string) {
	fmt.Printf("=== Fake Pose Broadcaster: %s -> %s ===\n\n", name, addr)
	d, err := env.Open(0)
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	defer d.Close()
	client := env.NewClient(context.Background(), name)
	client.OnLog = func(msg string) { fmt.Printf("[net] %s\n", msg) }
	if err := client.Connect(addr); err != nil {
		fmt.Printf("connect: %v\n", err)
		os.Exit(1)
	}
	defer client.Disconnect()

	const linkMpCLModelOff = 0x032C
	const j3dModelMpNodeMtxOff = 0x8C
	const poseBytes = avatar.JointCount * 48

	// One-shot capture: read Link's current world pose, then localize.
	var captured []byte
	for try := 0; try < 30 && captured == nil; try++ {
		linkPtr, err := player.NewLink(d).Ptr()
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
		data, err := d.Read(nodePtr, poseBytes)
		if err != nil || data == nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		pos, err := player.NewLink(d).Position()
		if err != nil || pos == nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		avatar.Localize(data, avatar.JointCount, pos.PosX, pos.PosY, pos.PosZ)
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
		pos, err := player.NewLink(d).Position()
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
			if err := client.SendPose(avatar.JointCount, captured, nil); err != nil {
				fmt.Printf("send pose: %v\n", err)
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	fmt.Println("\nDisconnected.")
}
