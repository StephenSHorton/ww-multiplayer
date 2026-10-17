// Package emu attaches to a running Dolphin emulator and maps its emulated
// GameCube RAM. A *Dolphin is a mem.Mem: Read and Write are bounds-checked
// to MEM1. launch (Dolphin detection, launch, user dirs) and host (window,
// screenshot, keys) are its subpackages.
//
// # v0 UNSTABLE
//
// This package is part of wwapi v0. Its API may change in any 0.x minor
// version.
package emu

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// Dolphin represents a connection to a running Dolphin emulator process.
type Dolphin struct {
	handle    procHandle
	pid       uint32
	gcRamBase uintptr // Start of emulated GameCube RAM in Dolphin's process
	gameID    string
}

// PlayerPosition holds a player's real-time state in big-endian GameCube format.
type PlayerPosition struct {
	PosX float32
	PosY float32
	PosZ float32
	RotX int16
	RotY int16
	RotZ int16
}

// Options picks the Dolphin process Attach opens. The caller fills it:
// package main reads WW_DOLPHIN_PID and WW_DOLPHIN_INDEX from the
// environment (dolphin_env.go); package emu never reads the environment.
type Options struct {
	// PID opens that process when non-zero, and Index is ignored.
	PID int
	// Index picks among the running Dolphin instances, in ListPIDs order
	// (0 = lowest PID). It matters only when two Dolphins run at once.
	Index int
	// GameID is the disc ID at the start of emulated RAM ("GZLE01").
	GameID string
}

// Attach locates a Dolphin emulator process and maps its emulated RAM:
// the process o.PID when it is set, else the o.Index-th running Dolphin.
func Attach(o Options) (*Dolphin, error) {
	if o.PID != 0 {
		return openByPID(uint32(o.PID), o.GameID)
	}
	pids, err := ListPIDs()
	if err != nil {
		return nil, fmt.Errorf("dolphin not running: %w", err)
	}
	if o.Index < 0 || o.Index >= len(pids) {
		return nil, fmt.Errorf("WW_DOLPHIN_INDEX=%d but only %d Dolphin instance(s) running", o.Index, len(pids))
	}
	return openByPID(pids[o.Index], o.GameID)
}

// FindByPID opens a Dolphin process by known PID. Used by mp-local (and
// other multi-instance flows) that enumerate Dolphins upfront via ListPIDs
// and need to address each one without relying on the WW_DOLPHIN_INDEX
// env var (which is process-wide and can't differ between goroutines in
// one binary).
func FindByPID(pid uint32, gameID string) (*Dolphin, error) {
	return openByPID(pid, gameID)
}

// ListPIDs returns the PIDs of every running Dolphin process, sorted
// ascending. Same ordering Attach uses for WW_DOLPHIN_INDEX, so
// idx 0 → ListPIDs()[0], idx 1 → ListPIDs()[1], etc.
func ListPIDs() ([]uint32, error) {
	return listProcessesByName("Dolphin")
}

func openByPID(pid uint32, gameID string) (*Dolphin, error) {
	handle, err := openProc(pid)
	if err != nil {
		return nil, fmt.Errorf("failed to open dolphin process (pid %d): %w", pid, err)
	}
	d := &Dolphin{
		handle: handle,
		pid:    pid,
		gameID: gameID,
	}
	if err := d.scanForRAM(); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

// PID returns the underlying Dolphin process ID for diagnostics.
func (d *Dolphin) PID() uint32 { return d.pid }

// Close releases the process handle.
func (d *Dolphin) Close() {
	if !procHandleZero(d.handle) {
		closeProc(d.handle)
		d.handle = zeroProcHandle()
	}
}

// GCRamBase returns the process address where emulated GC RAM starts.
func (d *Dolphin) GCRamBase() uintptr { return d.gcRamBase }

// readAbsolute reads bytes from an absolute GameCube RAM address. It does
// no bounds check; Read (mem.go) is the checked, exported door.
func (d *Dolphin) readAbsolute(gcAddr uint32, size int) ([]byte, error) {
	offset := uintptr(gcAddr - 0x80000000)
	addr := d.gcRamBase + offset
	buf := make([]byte, size)
	if err := readProc(d.handle, addr, buf); err != nil {
		return nil, fmt.Errorf("read failed at 0x%X: %w", gcAddr, err)
	}
	return buf, nil
}

// writeAbsolute writes bytes to an absolute GameCube RAM address. It does
// no bounds check; Write (mem.go) is the checked, exported door.
func (d *Dolphin) writeAbsolute(gcAddr uint32, data []byte) error {
	offset := uintptr(gcAddr - 0x80000000)
	addr := d.gcRamBase + offset
	if err := writeProc(d.handle, addr, data); err != nil {
		return fmt.Errorf("write failed at 0x%X: %w", gcAddr, err)
	}
	return nil
}

// ReadU32 reads a big-endian uint32 from a GameCube address.
func (d *Dolphin) ReadU32(gcAddr uint32) (uint32, error) {
	buf, err := d.readAbsolute(gcAddr, 4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(buf), nil
}

// ReadF32 reads a big-endian float32 from a GameCube address.
func (d *Dolphin) ReadF32(gcAddr uint32) (float32, error) {
	val, err := d.ReadU32(gcAddr)
	if err != nil {
		return 0, err
	}
	return math.Float32frombits(val), nil
}

// GetLinkPtr reads mpPlayerPtr[0] to get Link's actor address.
func (d *Dolphin) GetLinkPtr() (uint32, error) {
	return d.ReadU32(addr.PlayerPtr)
}

// IsInGame reports whether the Dolphin instance is in a playable scene
// (Link's actor pointer is non-zero and in the GC RAM range). Returns
// false during title screen, save-file menu, world-map cutscenes, or
// the brief reload window between scenes — anything where mpPlayerPtr[0]
// is null or unmapped.
//
// Used by mp-local's "wait until both Dolphins ready" gate so the
// connect sequence doesn't kick off into a Dolphin that hasn't loaded
// a save yet (which would have broadcast-pose hammering ReadU32 on a
// zero pointer and puppet-sync writing actor poses into nothing).
func (d *Dolphin) IsInGame() bool {
	ptr, err := d.GetLinkPtr()
	if err != nil {
		return false
	}
	return addr.InMEM1(ptr)
}

// ReadPlayerPosition reads Link's current position and rotation.
func (d *Dolphin) ReadPlayerPosition() (*PlayerPosition, error) {
	linkPtr, err := d.GetLinkPtr()
	if err != nil || linkPtr == 0 {
		return nil, fmt.Errorf("link not loaded")
	}

	posData, err := d.readAbsolute(linkPtr+addr.ActorOffCurPos, 12)
	if err != nil {
		return nil, err
	}
	rotData, err := d.readAbsolute(linkPtr+addr.ActorOffCurAngle, 6)
	if err != nil {
		return nil, err
	}

	return &PlayerPosition{
		PosX: math.Float32frombits(binary.BigEndian.Uint32(posData[0:4])),
		PosY: math.Float32frombits(binary.BigEndian.Uint32(posData[4:8])),
		PosZ: math.Float32frombits(binary.BigEndian.Uint32(posData[8:12])),
		RotX: int16(binary.BigEndian.Uint16(rotData[0:2])),
		RotY: int16(binary.BigEndian.Uint16(rotData[2:4])),
		RotZ: int16(binary.BigEndian.Uint16(rotData[4:6])),
	}, nil
}

// scanForRAM searches Dolphin's process memory for the emulated GameCube
// RAM. Strategy is platform-agnostic: walk every committed,
// readable+writable region the OS reports for the target process; first
// look for a region of at least 24 MiB whose first bytes are the game
// ID, then fall back to scanning at 4 KiB intervals inside any region
// of at least 1 MiB.
func (d *Dolphin) scanForRAM() error {
	expected := []byte(d.gameID)

	regions, err := walkRegions(d.handle)
	if err != nil {
		return fmt.Errorf("enumerate regions: %w", err)
	}

	header := make([]byte, len(expected))

	for _, r := range regions {
		if r.size < 0x1800000 {
			continue
		}
		if err := readProc(d.handle, r.base, header); err != nil {
			continue
		}
		if bytes.Equal(header, expected) {
			d.gcRamBase = r.base
			return nil
		}
	}

	for _, r := range regions {
		if r.size < 0x100000 {
			continue
		}
		scanLimit := r.size
		if scanLimit > 0x100000 {
			scanLimit = 0x100000
		}
		for offset := uintptr(0); offset < scanLimit; offset += 0x1000 {
			if err := readProc(d.handle, r.base+offset, header); err != nil {
				continue
			}
			if bytes.Equal(header, expected) {
				d.gcRamBase = r.base + offset
				return nil
			}
		}
	}

	return fmt.Errorf("could not find GameCube RAM (game ID: %s)", d.gameID)
}

// region is a single committed, readable+writable memory region in the
// target process — the platform-agnostic shape returned by walkRegions.
type region struct {
	base uintptr
	size uintptr
}

// gcDiscMagic is the GameCube disc magic word at offset 0x1C of the disc
// header, which the boot copies to 0x80000000 with the game ID.
const gcDiscMagic = 0xC2339F3D

// DiscID returns the game ID of the GameCube disc Dolphin process pid
// runs ("GZLE01"), read from the start of its emulated RAM: "" when it
// runs no game (Dolphin is open at its game list, or still booting). The
// error is for a process that can't be opened (it has exited, say).
func DiscID(pid uint32) (string, error) {
	h, err := openProc(pid)
	if err != nil {
		return "", fmt.Errorf("failed to open dolphin process (pid %d): %w", pid, err)
	}
	defer closeProc(h)
	regions, err := walkRegions(h)
	if err != nil {
		return "", fmt.Errorf("enumerate regions: %w", err)
	}
	hdr := make([]byte, 0x20)
	for _, r := range regions {
		if r.size < 0x1800000 {
			continue
		}
		if err := readProc(h, r.base, hdr); err != nil {
			continue
		}
		if id, ok := discID(hdr); ok {
			return id, nil
		}
	}
	return "", nil
}

// discID reads a disc header's first 0x20 bytes: the game ID when the GC
// magic is there and the ID is six letters and digits.
func discID(hdr []byte) (string, bool) {
	if len(hdr) < 0x20 || binary.BigEndian.Uint32(hdr[0x1C:]) != gcDiscMagic {
		return "", false
	}
	for _, c := range hdr[:6] {
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return "", false
		}
	}
	return string(hdr[:6]), true
}
