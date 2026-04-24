package dolphin

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

var (
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess      = kernel32.NewProc("OpenProcess")
	procReadProcessMem   = kernel32.NewProc("ReadProcessMemory")
	procWriteProcessMem  = kernel32.NewProc("WriteProcessMemory")
	procCloseHandle      = kernel32.NewProc("CloseHandle")
	procVirtualQueryEx   = kernel32.NewProc("VirtualQueryEx")
	procCreateToolhelp   = kernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32First   = kernel32.NewProc("Process32FirstW")
	procProcess32Next    = kernel32.NewProc("Process32NextW")
)

const (
	PROCESS_VM_READ       = 0x0010
	PROCESS_VM_WRITE      = 0x0020
	PROCESS_VM_OPERATION  = 0x0008
	PROCESS_QUERY_INFO    = 0x0400
	PROCESS_ALL_ACCESS    = PROCESS_VM_READ | PROCESS_VM_WRITE | PROCESS_VM_OPERATION | PROCESS_QUERY_INFO
	TH32CS_SNAPPROCESS    = 0x00000002
	MEM_COMMIT            = 0x1000
	PAGE_READWRITE        = 0x04
	PAGE_EXECUTE_READWRITE = 0x40
)

type memoryBasicInfo struct {
	BaseAddress       uintptr
	AllocationBase    uintptr
	AllocationProtect uint32
	RegionSize        uintptr
	State             uint32
	Protect           uint32
	Type              uint32
}

type processEntry32 struct {
	Size            uint32
	CntUsage        uint32
	ProcessID       uint32
	DefaultHeapID   uintptr
	ModuleID        uint32
	CntThreads      uint32
	ParentProcessID uint32
	PriClassBase    int32
	Flags           uint32
	ExeFile         [260]uint16
}

// Dolphin represents a connection to a running Dolphin emulator process.
type Dolphin struct {
	handle    syscall.Handle
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
	PosOffset     = 0x1F8     // current.pos (3x float32)
	RotOffset     = 0x204     // current.angle (3x int16)
	RoomOffset    = 0x20A     // current.roomNo (int8)
	AnimOffset    = 0x31D8    // mCurProc (uint32)
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
	pids, err := findAllProcesses("Dolphin")
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
	return findAllProcesses("Dolphin")
}

func openByPID(pid uint32, gameID string) (*Dolphin, error) {
	handle, _, _ := procOpenProcess.Call(PROCESS_ALL_ACCESS, 0, uintptr(pid))
	if handle == 0 {
		return nil, fmt.Errorf("failed to open dolphin process (pid %d)", pid)
	}
	d := &Dolphin{
		handle: syscall.Handle(handle),
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
	if d.handle != 0 {
		procCloseHandle.Call(uintptr(d.handle))
		d.handle = 0
	}
}

// GCRamBase returns the process address where emulated GC RAM starts.
func (d *Dolphin) GCRamBase() uintptr { return d.gcRamBase }

// ReadAbsolute reads bytes from an absolute GameCube RAM address.
func (d *Dolphin) ReadAbsolute(gcAddr uint32, size int) ([]byte, error) {
	offset := uintptr(gcAddr - 0x80000000)
	addr := d.gcRamBase + offset
	buf := make([]byte, size)
	var bytesRead uintptr
	ret, _, _ := procReadProcessMem.Call(
		uintptr(d.handle), addr, uintptr(unsafe.Pointer(&buf[0])),
		uintptr(size), uintptr(unsafe.Pointer(&bytesRead)),
	)
	if ret == 0 {
		return nil, fmt.Errorf("read failed at 0x%X", gcAddr)
	}
	return buf, nil
}

// WriteAbsolute writes bytes to an absolute GameCube RAM address.
func (d *Dolphin) WriteAbsolute(gcAddr uint32, data []byte) error {
	offset := uintptr(gcAddr - 0x80000000)
	addr := d.gcRamBase + offset
	var bytesWritten uintptr
	ret, _, _ := procWriteProcessMem.Call(
		uintptr(d.handle), addr, uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)), uintptr(unsafe.Pointer(&bytesWritten)),
	)
	if ret == 0 {
		return fmt.Errorf("write failed at 0x%X", gcAddr)
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

// scanForRAM searches Dolphin's process memory for the emulated GameCube RAM.
func (d *Dolphin) scanForRAM() error {
	expected := []byte(d.gameID)
	var addr uintptr
	maxAddr := uintptr(0x7FFFFFFFFFFF)

	for addr < maxAddr {
		var mbi memoryBasicInfo
		ret, _, _ := procVirtualQueryEx.Call(
			uintptr(d.handle), addr,
			uintptr(unsafe.Pointer(&mbi)),
			unsafe.Sizeof(mbi),
		)
		if ret == 0 {
			break
		}

		regionSize := mbi.RegionSize
		regionBase := mbi.BaseAddress

		if mbi.State == MEM_COMMIT && regionSize >= 0x1800000 &&
			(mbi.Protect == PAGE_READWRITE || mbi.Protect == PAGE_EXECUTE_READWRITE) {
			header := make([]byte, len(expected))
			var bytesRead uintptr
			ret, _, _ := procReadProcessMem.Call(
				uintptr(d.handle), regionBase,
				uintptr(unsafe.Pointer(&header[0])),
				uintptr(len(expected)),
				uintptr(unsafe.Pointer(&bytesRead)),
			)
			if ret != 0 && string(header) == d.gameID {
				d.gcRamBase = regionBase
				return nil
			}
		}

		addr = regionBase + regionSize
		if addr <= regionBase {
			break
		}
	}

	// Second pass: search inside larger regions at 4KB intervals
	addr = 0
	for addr < maxAddr {
		var mbi memoryBasicInfo
		ret, _, _ := procVirtualQueryEx.Call(
			uintptr(d.handle), addr,
			uintptr(unsafe.Pointer(&mbi)),
			unsafe.Sizeof(mbi),
		)
		if ret == 0 {
			break
		}

		regionSize := mbi.RegionSize
		regionBase := mbi.BaseAddress

		if mbi.State == MEM_COMMIT && regionSize >= 0x100000 {
			scanLimit := regionSize
			if scanLimit > 0x100000 {
				scanLimit = 0x100000
			}
			for offset := uintptr(0); offset < scanLimit; offset += 0x1000 {
				header := make([]byte, len(expected))
				var bytesRead uintptr
				ret, _, _ := procReadProcessMem.Call(
					uintptr(d.handle), regionBase+offset,
					uintptr(unsafe.Pointer(&header[0])),
					uintptr(len(expected)),
					uintptr(unsafe.Pointer(&bytesRead)),
				)
				if ret != 0 && string(header) == d.gameID {
					d.gcRamBase = regionBase + offset
					return nil
				}
			}
		}

		addr = regionBase + regionSize
		if addr <= regionBase {
			break
		}
	}

	return fmt.Errorf("could not find GameCube RAM (game ID: %s)", d.gameID)
}

func findProcess(name string) (uint32, error) {
	pids, err := findAllProcesses(name)
	if err != nil {
		return 0, err
	}
	return pids[0], nil
}

// findAllProcesses returns every PID whose executable name contains `name`,
// sorted ascending. Used by Find() so WW_DOLPHIN_INDEX is stable across runs
// (lower PID = older instance = index 0).
func findAllProcesses(name string) ([]uint32, error) {
	snap, _, _ := procCreateToolhelp.Call(TH32CS_SNAPPROCESS, 0)
	if snap == 0 {
		return nil, fmt.Errorf("failed to create process snapshot")
	}
	defer procCloseHandle.Call(snap)

	var entry processEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	ret, _, _ := procProcess32First.Call(snap, uintptr(unsafe.Pointer(&entry)))
	if ret == 0 {
		return nil, fmt.Errorf("no processes found")
	}

	nameLower := strings.ToLower(name)
	var pids []uint32
	for {
		exeName := syscall.UTF16ToString(entry.ExeFile[:])
		if strings.Contains(strings.ToLower(exeName), nameLower) {
			pids = append(pids, entry.ProcessID)
		}
		entry.Size = uint32(unsafe.Sizeof(entry))
		ret, _, _ = procProcess32Next.Call(snap, uintptr(unsafe.Pointer(&entry)))
		if ret == 0 {
			break
		}
	}

	if len(pids) == 0 {
		return nil, fmt.Errorf("process %q not found", name)
	}
	sort.Slice(pids, func(i, j int) bool { return pids[i] < pids[j] })
	return pids, nil
}
