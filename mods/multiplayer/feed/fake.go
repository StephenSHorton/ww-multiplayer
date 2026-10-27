package feed

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/boatsync"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/net"
	"github.com/StephenSHorton/ww-multiplayer/mods/multiplayer/vissync"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/avatar"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/player"
)

// PoseFakeLoop captures Link's current pose once and replays it as a separate
// "fake remote" so loopback testing can demonstrate multi-Link
// rendering without a second Dolphin. Position is sent as the live
// Link position + 1000 X offset so the fake walks alongside the real
// player, frozen in the captured pose. Receiver sees this as a remote
// distinct from broadcast-pose's player and assigns it a new link slot.
// The face trailer (eyes, appearance, equip and, unless WW_BOATS=off, the
// ship) is captured with the pose as broadcast-pose builds it and sent
// with every frame, so a capture taken aboard replays its boat under the
// fake (an aboard ship is relative to Link); the place trailer is read
// live with the position.
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
	var captured, face []byte
	for try := 0; try < 30 && captured == nil; try++ {
		linkPtr, err := player.NewLink(d).Ptr()
		if err != nil || linkPtr == 0 {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		modelPtr, _ := d.ReadU32(linkPtr + linkMpCLModelOff)
		if modelPtr < 0x80000000 || modelPtr >= 0x83000000 {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		nodePtr, _ := d.ReadU32(modelPtr + j3dModelMpNodeMtxOff)
		if nodePtr < 0x80000000 || nodePtr >= 0x83000000 {
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
		var equip vissync.EquipSampler
		face = faceTrailer(d, player.NewLink(d), [3]float32{pos.PosX, pos.PosY, pos.PosZ}, &equip, boatsync.SettingsFromEnv().On)
		fmt.Printf("captured fake pose at (%.0f, %.0f, %.0f), face trailer %d bytes\n", pos.PosX, pos.PosY, pos.PosZ, len(face))
	}
	if captured == nil {
		fmt.Println("could not capture initial pose; is Link loaded?")
		os.Exit(1)
	}

	// Stream loop: position tracks live Link with +1000 X so the fake
	// walks beside the real player. Pose and face stay frozen at the capture.
	for client.IsConnected() {
		pos, err := player.NewLink(d).Position()
		if err == nil && pos != nil {
			place, placeOK := ReadPlace(d)
			if err := fakeSend(client, pos, place, placeOK, captured, face); err != nil {
				fmt.Printf("%v\n", err)
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	fmt.Println("\nDisconnected.")
}

// fakeSender is what PoseFakeLoop sends through (*network.Client).
type fakeSender interface {
	SendPosition(pos *network.PlayerPosition) error
	SendPositionAt(pos *network.PlayerPosition, p network.Place) error
	SendPose(joints int, matrices []byte, face []byte) error
}

// fakeSend sends one PoseFakeLoop frame: live, the real Link's position,
// moved 1000 X, with the place trailer when it was read (as
// broadcast-pose does), then the captured pose and face trailer.
func fakeSend(c fakeSender, live *player.Position, place network.Place, placeOK bool, pose, face []byte) error {
	netPos := &network.PlayerPosition{
		PosX: live.PosX + 1000,
		PosY: live.PosY,
		PosZ: live.PosZ,
		RotY: live.RotY,
	}
	var err error
	if placeOK {
		err = c.SendPositionAt(netPos, place)
	} else {
		err = c.SendPosition(netPos)
	}
	if err != nil {
		return fmt.Errorf("send position: %w", err)
	}
	if err := c.SendPose(avatar.JointCount, pose, face); err != nil {
		return fmt.Errorf("send pose: %w", err)
	}
	return nil
}
