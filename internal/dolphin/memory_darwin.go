//go:build darwin

package dolphin

/*
#cgo LDFLAGS: -framework CoreFoundation
#include <stdlib.h>
#include <string.h>
#include <ctype.h>
#include <mach/mach.h>
#include <mach/mach_vm.h>
#include <libproc.h>

// task_for_pid fails on every modern macOS unless one of:
//   - the calling binary is signed with com.apple.security.cs.debugger
//   - the caller runs as root
//   - SIP is disabled (do not ask users to do this)
// On failure we surface the kern_return_t to the Go caller verbatim so
// the user sees the underlying reason ("operation not permitted",
// "(os/kern) failure", etc).
int dolphin_task_for_pid(int pid, mach_port_t *out_task) {
    return (int)task_for_pid(mach_task_self(), pid, out_task);
}

void dolphin_release_task(mach_port_t task) {
    mach_port_deallocate(mach_task_self(), task);
}

// Returns 0 on success, otherwise the kern_return_t from Mach.
int dolphin_read(mach_port_t task, unsigned long long addr, void *buf, unsigned long long len) {
    mach_vm_size_t got = 0;
    kern_return_t kr = mach_vm_read_overwrite(task,
        (mach_vm_address_t)addr,
        (mach_vm_size_t)len,
        (mach_vm_address_t)(uintptr_t)buf,
        &got);
    if (kr != KERN_SUCCESS) return (int)kr;
    if (got != (mach_vm_size_t)len) return -1;
    return 0;
}

int dolphin_write(mach_port_t task, unsigned long long addr, const void *buf, unsigned long long len) {
    kern_return_t kr = mach_vm_write(task,
        (mach_vm_address_t)addr,
        (vm_offset_t)(uintptr_t)buf,
        (mach_msg_type_number_t)len);
    return (int)kr;
}

// Iterates one region using mach_vm_region_recurse. Pass start_addr=0 on
// the first call; pass back base+size on subsequent calls. Submaps are
// skipped (Dolphin's emulated RAM is a top-level mmap, not a submap).
//
// On success returns 0 and fills out_base/out_size/out_prot.
// On end-of-iteration returns 1.
// On error returns the kern_return_t (always > 1).
int dolphin_next_region(mach_port_t task, unsigned long long start_addr,
                        unsigned long long *out_base,
                        unsigned long long *out_size,
                        int *out_prot) {
    mach_vm_address_t addr = (mach_vm_address_t)start_addr;
    mach_vm_size_t size = 0;
    natural_t depth = 0;
    vm_region_submap_info_data_64_t info;
    mach_msg_type_number_t count = VM_REGION_SUBMAP_INFO_COUNT_64;

    kern_return_t kr = mach_vm_region_recurse(task, &addr, &size, &depth,
        (vm_region_recurse_info_t)&info, &count);
    if (kr == KERN_INVALID_ADDRESS) return 1;
    if (kr != KERN_SUCCESS) return (int)kr;

    *out_base = (unsigned long long)addr;
    *out_size = (unsigned long long)size;
    *out_prot = (int)info.protection;
    return 0;
}

// Finds PIDs whose executable basename (case-insensitive) contains
// `needle`. Writes up to max_pids ascending PIDs into out_pids.
// Returns the count written, or a negative errno-ish value on error.
int dolphin_find_pids_by_name(const char *needle, int *out_pids, int max_pids) {
    int byte_size = proc_listallpids(NULL, 0);
    if (byte_size <= 0) return -1;
    // proc_listallpids quirk: passing the exact size sometimes truncates,
    // so over-allocate.
    byte_size *= 2;
    int *all = (int *)malloc((size_t)byte_size);
    if (!all) return -2;

    int got = proc_listallpids(all, byte_size);
    if (got <= 0) { free(all); return -3; }
    int n = got / (int)sizeof(int);

    char low_needle[256];
    int nlen = (int)strlen(needle);
    if (nlen >= (int)sizeof(low_needle)) nlen = (int)sizeof(low_needle) - 1;
    for (int i = 0; i < nlen; i++) low_needle[i] = (char)tolower((unsigned char)needle[i]);
    low_needle[nlen] = 0;

    int count = 0;
    char path[PROC_PIDPATHINFO_MAXSIZE];
    char low_base[256];
    for (int i = 0; i < n && count < max_pids; i++) {
        if (all[i] <= 0) continue;
        int plen = proc_pidpath(all[i], path, sizeof(path));
        if (plen <= 0) continue;
        const char *base = strrchr(path, '/');
        base = base ? base + 1 : path;
        int j = 0;
        for (; base[j] && j < (int)sizeof(low_base) - 1; j++) {
            low_base[j] = (char)tolower((unsigned char)base[j]);
        }
        low_base[j] = 0;
        if (strstr(low_base, low_needle)) {
            out_pids[count++] = all[i];
        }
    }
    free(all);
    return count;
}
*/
import "C"

import (
	"fmt"
	"sort"
	"unsafe"
)

type procHandle = C.mach_port_t

func zeroProcHandle() procHandle      { return 0 }
func procHandleZero(h procHandle) bool { return h == 0 }

const (
	vmProtRead  = 0x01
	vmProtWrite = 0x02
)

func openProc(pid uint32) (procHandle, error) {
	var task C.mach_port_t
	kr := C.dolphin_task_for_pid(C.int(pid), &task)
	if kr != 0 {
		return 0, fmt.Errorf("task_for_pid(%d) failed: kern_return=%d — needs sudo or a binary signed with com.apple.security.cs.debugger", pid, int(kr))
	}
	return task, nil
}

func closeProc(h procHandle) {
	C.dolphin_release_task(h)
}

func readProc(h procHandle, addr uintptr, buf []byte) error {
	if len(buf) == 0 {
		return nil
	}
	rc := C.dolphin_read(h,
		C.ulonglong(addr),
		unsafe.Pointer(&buf[0]),
		C.ulonglong(len(buf)))
	if rc != 0 {
		return fmt.Errorf("mach_vm_read_overwrite failed (kr=%d)", int(rc))
	}
	return nil
}

func writeProc(h procHandle, addr uintptr, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	rc := C.dolphin_write(h,
		C.ulonglong(addr),
		unsafe.Pointer(&data[0]),
		C.ulonglong(len(data)))
	if rc != 0 {
		return fmt.Errorf("mach_vm_write failed (kr=%d)", int(rc))
	}
	return nil
}

// walkRegions enumerates committed RW regions in the target by
// iterating mach_vm_region_recurse from address 0 upward. We skip
// submaps and any region without both READ and WRITE protection bits.
func walkRegions(h procHandle) ([]region, error) {
	var out []region
	var addr C.ulonglong = 0
	for {
		var base, size C.ulonglong
		var prot C.int
		rc := C.dolphin_next_region(h, addr, &base, &size, &prot)
		if rc == 1 {
			break // KERN_INVALID_ADDRESS — past end of address space
		}
		if rc != 0 {
			return nil, fmt.Errorf("mach_vm_region_recurse failed (kr=%d)", int(rc))
		}
		if (int(prot) & (vmProtRead | vmProtWrite)) == (vmProtRead | vmProtWrite) {
			out = append(out, region{base: uintptr(base), size: uintptr(size)})
		}
		next := base + size
		if next <= addr {
			break
		}
		addr = next
	}
	return out, nil
}

func listProcessesByName(name string) ([]uint32, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	const maxPIDs = 4096
	buf := make([]C.int, maxPIDs)
	got := C.dolphin_find_pids_by_name(cName, &buf[0], C.int(maxPIDs))
	if got < 0 {
		return nil, fmt.Errorf("proc_listallpids failed (rc=%d)", int(got))
	}
	if got == 0 {
		return nil, fmt.Errorf("process %q not found", name)
	}
	pids := make([]uint32, int(got))
	for i := 0; i < int(got); i++ {
		pids[i] = uint32(buf[i])
	}
	sort.Slice(pids, func(i, j int) bool { return pids[i] < pids[j] })
	return pids, nil
}
