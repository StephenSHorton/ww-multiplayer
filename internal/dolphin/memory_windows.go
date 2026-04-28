//go:build windows

package dolphin

import (
	"fmt"
	"sort"
	"strings"
	"syscall"
	"unsafe"
)

var (
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess     = kernel32.NewProc("OpenProcess")
	procReadProcessMem  = kernel32.NewProc("ReadProcessMemory")
	procWriteProcessMem = kernel32.NewProc("WriteProcessMemory")
	procCloseHandle     = kernel32.NewProc("CloseHandle")
	procVirtualQueryEx  = kernel32.NewProc("VirtualQueryEx")
	procCreateToolhelp  = kernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32First  = kernel32.NewProc("Process32FirstW")
	procProcess32Next   = kernel32.NewProc("Process32NextW")
)

const (
	processVMRead          = 0x0010
	processVMWrite         = 0x0020
	processVMOperation     = 0x0008
	processQueryInfo       = 0x0400
	processAllAccess       = processVMRead | processVMWrite | processVMOperation | processQueryInfo
	th32csSnapProcess      = 0x00000002
	memCommit              = 0x1000
	pageReadWrite          = 0x04
	pageExecuteReadWrite   = 0x40
)

type procHandle = syscall.Handle

func zeroProcHandle() procHandle    { return 0 }
func procHandleZero(h procHandle) bool { return h == 0 }

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

func openProc(pid uint32) (procHandle, error) {
	h, _, _ := procOpenProcess.Call(processAllAccess, 0, uintptr(pid))
	if h == 0 {
		return 0, fmt.Errorf("OpenProcess failed")
	}
	return procHandle(h), nil
}

func closeProc(h procHandle) {
	procCloseHandle.Call(uintptr(h))
}

func readProc(h procHandle, addr uintptr, buf []byte) error {
	if len(buf) == 0 {
		return nil
	}
	var n uintptr
	ret, _, _ := procReadProcessMem.Call(
		uintptr(h), addr, uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)), uintptr(unsafe.Pointer(&n)),
	)
	if ret == 0 {
		return fmt.Errorf("ReadProcessMemory failed")
	}
	return nil
}

func writeProc(h procHandle, addr uintptr, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	var n uintptr
	ret, _, _ := procWriteProcessMem.Call(
		uintptr(h), addr, uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)), uintptr(unsafe.Pointer(&n)),
	)
	if ret == 0 {
		return fmt.Errorf("WriteProcessMemory failed")
	}
	return nil
}

// walkRegions enumerates committed RW (or RWX) regions in the target.
// The returned slice mirrors the order VirtualQueryEx walks the process,
// matching the legacy scanForRAM's iteration order.
func walkRegions(h procHandle) ([]region, error) {
	var out []region
	var addr uintptr
	maxAddr := uintptr(0x7FFFFFFFFFFF)

	for addr < maxAddr {
		var mbi memoryBasicInfo
		ret, _, _ := procVirtualQueryEx.Call(
			uintptr(h), addr,
			uintptr(unsafe.Pointer(&mbi)),
			unsafe.Sizeof(mbi),
		)
		if ret == 0 {
			break
		}

		if mbi.State == memCommit &&
			(mbi.Protect == pageReadWrite || mbi.Protect == pageExecuteReadWrite) {
			out = append(out, region{base: mbi.BaseAddress, size: mbi.RegionSize})
		}

		next := mbi.BaseAddress + mbi.RegionSize
		if next <= addr {
			break
		}
		addr = next
	}
	return out, nil
}

// listProcessesByName returns every PID whose executable name contains
// `name` (case-insensitive), sorted ascending. Used by ListPIDs() so
// WW_DOLPHIN_INDEX is stable across runs (lower PID = older instance =
// index 0).
func listProcessesByName(name string) ([]uint32, error) {
	snap, _, _ := procCreateToolhelp.Call(th32csSnapProcess, 0)
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
