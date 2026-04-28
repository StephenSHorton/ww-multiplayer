package dolphin

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"strconv"
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

// Wind Waker GZLE01 addresses
const (
	PlayerPtrAddr = 0x803CA754 // mpPlayerPtr[0] — pointer to Link's actor
	PosOffset     = 0x1F8      // current.pos (3x float32)
	RotOffset     = 0x204      // current.angle (3x int16)
	RoomOffset    = 0x20A      // current.roomNo (int8)
	AnimOffset    = 0x31D8     // mCurProc (uint32)
)

// Find locates a Dolphin emulator process and maps its emulated RAM.
// When multiple Dolphin instances are running (two-Dolphin multiplayer),
// the WW_DOLPHIN_INDEX env var picks which one (0 = lowest PID, 1 = next,
// ...). Defaults to 0 for the single-Dolphin case.
func Find(gameID string) (*Dolphin, error) {
	idx := 0
	if v := os.Getenv("WW_DOLPHIN_INDEX"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("bad WW_DOLPHIN_INDEX=%q", v)
		}
		idx = n
	}
	if pidStr := os.Getenv("WW_DOLPHIN_PID"); pidStr != "" {
		pid64, err := strconv.ParseUint(pidStr, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("bad WW_DOLPHIN_PID=%q", pidStr)
		}
		return openByPID(uint32(pid64), gameID)
	}
	pids, err := ListPIDs()
	if err != nil {
		return nil, fmt.Errorf("dolphin not running: %w", err)
	}
	if idx >= len(pids) {
		return nil, fmt.Errorf("WW_DOLPHIN_INDEX=%d but only %d Dolphin instance(s) running", idx, len(pids))
	}
	return openByPID(pids[idx], gameID)
}

// FindByPID is the public entry point for opening a Dolphin process by
// known PID. Used by mp-local (and other multi-instance flows) that
// enumerate Dolphins upfront via ListPIDs and need to address each one
// without relying on the WW_DOLPHIN_INDEX env var (which is process-wide
// and can't differ between goroutines in one binary).
func FindByPID(pid uint32, gameID string) (*Dolphin, error) {
	return openByPID(pid, gameID)
}

// ListPIDs returns the PIDs of every running Dolphin process, sorted
// ascending. Same ordering Find() uses for WW_DOLPHIN_INDEX, so
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

// ReadAbsolute reads bytes from an absolute GameCube RAM address.
func (d *Dolphin) ReadAbsolute(gcAddr uint32, size int) ([]byte, error) {
	offset := uintptr(gcAddr - 0x80000000)
	addr := d.gcRamBase + offset
	buf := make([]byte, size)
	if err := readProc(d.handle, addr, buf); err != nil {
		return nil, fmt.Errorf("read failed at 0x%X: %w", gcAddr, err)
	}
	return buf, nil
}

// WriteAbsolute writes bytes to an absolute GameCube RAM address.
func (d *Dolphin) WriteAbsolute(gcAddr uint32, data []byte) error {
	offset := uintptr(gcAddr - 0x80000000)
	addr := d.gcRamBase + offset
	if err := writeProc(d.handle, addr, data); err != nil {
		return fmt.Errorf("write failed at 0x%X: %w", gcAddr, err)
	}
	return nil
}

// ReadU32 reads a big-endian uint32 from a GameCube address.
func (d *Dolphin) ReadU32(gcAddr uint32) (uint32, error) {
	buf, err := d.ReadAbsolute(gcAddr, 4)
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
	return d.ReadU32(PlayerPtrAddr)
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
	return ptr >= 0x80000000 && ptr < 0x81800000
}

// ReadPlayerPosition reads Link's current position and rotation.
func (d *Dolphin) ReadPlayerPosition() (*PlayerPosition, error) {
	linkPtr, err := d.GetLinkPtr()
	if err != nil || linkPtr == 0 {
		return nil, fmt.Errorf("link not loaded")
	}

	posData, err := d.ReadAbsolute(linkPtr+PosOffset, 12)
	if err != nil {
		return nil, err
	}
	rotData, err := d.ReadAbsolute(linkPtr+RotOffset, 6)
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
