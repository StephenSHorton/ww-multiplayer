package dolphin

import (
	"fmt"
)

// Addresses for the injected code
const (
	InjectAddr       = 0x803FCF20 // Where the code gets written in GC RAM
	HookAddr         = 0x80006338 // main01 entry point we hook
	MailboxAddr      = 0x803F6100 // Shared mailbox for Go <-> injected code
	MultUpdateAddr   = 0x803FCFE0 // Address of multiplayer_update in injected code
)

// Mailbox offsets
const (
	MBSpawnTrigger = MailboxAddr + 0x00 // u32: write 1 to spawn
	MBActorPtr     = MailboxAddr + 0x04 // u32: actor pointer (set by injected code)
	MBPosX         = MailboxAddr + 0x08 // f32: Player 2 X
	MBPosY         = MailboxAddr + 0x0C // f32: Player 2 Y
	MBPosZ         = MailboxAddr + 0x10 // f32: Player 2 Z
	MBRotX         = MailboxAddr + 0x14 // s16: Player 2 rot X
	MBRotY         = MailboxAddr + 0x16 // s16: Player 2 rot Y
	MBRotZ         = MailboxAddr + 0x18 // s16: Player 2 rot Z
)

// InjectMultiplayer writes the compiled PPC code into Dolphin's emulated RAM.
// This must be called while the game is running but BEFORE the OnFrame hook activates.
func (d *Dolphin) InjectMultiplayer() error {
	// Write the compiled code to the injection address
	err := d.WriteAbsolute(InjectAddr, injectedCode)
	if err != nil {
		return fmt.Errorf("failed to write code: %w", err)
	}

	// Write the data section if present
	if len(injectedData) > 0 {
		dataAddr := InjectAddr + uint32(len(injectedCode))
		dataAddr = (dataAddr + 31) & ^uint32(31)
		err = d.WriteAbsolute(dataAddr, injectedData)
		if err != nil {
			return fmt.Errorf("failed to write data: %w", err)
		}
	}

	// Clear the mailbox
	zeros := make([]byte, 32)
	err = d.WriteAbsolute(MailboxAddr, zeros)
	if err != nil {
		return fmt.Errorf("failed to clear mailbox: %w", err)
	}

	// Zero the BSS area (static variables: spawned, player2, frame_count)
	// BSS is at ~0x803FCFC0, 48 bytes should cover it
	bssZeros := make([]byte, 48)
	err = d.WriteAbsolute(0x803FCFC0, bssZeros)
	if err != nil {
		return fmt.Errorf("failed to clear BSS: %w", err)
	}

	return nil
}

// TriggerSpawn writes the spawn trigger to the mailbox.
func (d *Dolphin) TriggerSpawn() error {
	return d.WriteAbsolute(MBSpawnTrigger, []byte{0x00, 0x00, 0x00, 0x01})
}

// GetPlayer2Ptr reads the spawned actor pointer from the mailbox.
func (d *Dolphin) GetPlayer2Ptr() (uint32, error) {
	return d.ReadU32(MBActorPtr)
}

// WritePlayer2Position writes remote player position to the mailbox.
func (d *Dolphin) WritePlayer2Position(pos *PlayerPosition) error {
	data := make([]byte, 18)
	putF32BE(data[0:4], pos.PosX)
	putF32BE(data[4:8], pos.PosY)
	putF32BE(data[8:12], pos.PosZ)
	putS16BE(data[12:14], pos.RotX)
	putS16BE(data[14:16], pos.RotY)
	putS16BE(data[16:18], pos.RotZ)
	return d.WriteAbsolute(MBPosX, data)
}
