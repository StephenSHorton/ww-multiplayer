//go:build windows

package launch

import (
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu"
)

var procQueryFullProcessImageName = syscall.NewLazyDLL("kernel32.dll").NewProc("QueryFullProcessImageNameW")

const processQueryLimitedInformation = 0x1000

// runningExes returns the executables of the running Dolphin processes
// (emu.ListPIDs, oldest first) by QueryFullProcessImageNameW, which
// needs only limited query access.
func runningExes() []string {
	pids, err := emu.ListPIDs()
	if err != nil {
		return nil
	}
	var paths []string
	for _, pid := range pids {
		if p := imagePath(pid); p != "" {
			paths = append(paths, p)
		}
	}
	return dolphinExes(paths)
}

func imagePath(pid uint32) string {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, pid)
	if err != nil {
		return ""
	}
	defer syscall.CloseHandle(h)
	buf := make([]uint16, 1024)
	n := uint32(len(buf))
	r, _, _ := procQueryFullProcessImageName.Call(uintptr(h), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
	if r == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}

// uninstallKeys are where Windows installers record themselves, for all
// users (64- and 32-bit views) and for this user.
var uninstallKeys = []struct {
	root registry.Key
	path string
}{
	{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`},
	{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`},
	{registry.CURRENT_USER, `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`},
}

// installLocations returns the InstallLocation of every uninstall entry
// whose DisplayName names Dolphin (installLocationsOf). Dolphin's NSIS
// installer writes one (inferred from Installer/Installer.nsi; not checked
// against an installed Dolphin on the owner's machine, which runs the zip
// build).
func installLocations() []string {
	var entries []UninstallEntry
	for _, k := range uninstallKeys {
		root, err := registry.OpenKey(k.root, k.path, registry.ENUMERATE_SUB_KEYS)
		if err != nil {
			continue
		}
		names, _ := root.ReadSubKeyNames(-1)
		root.Close()
		for _, n := range names {
			if !strings.Contains(strings.ToLower(n), "dolphin") {
				continue // read only the keys that may be Dolphin's
			}
			sub, err := registry.OpenKey(k.root, k.path+`\`+n, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			name, _, _ := sub.GetStringValue("DisplayName")
			loc, _, _ := sub.GetStringValue("InstallLocation")
			sub.Close()
			entries = append(entries, UninstallEntry{Key: n, DisplayName: name, InstallLocation: loc})
		}
	}
	return installLocationsOf(entries)
}

// userConfigPaths returns HKCU\Software\Dolphin Emulator's
// UserConfigPath, where an installed Dolphin may keep its user folder
// (Dolphin's UICommon::SetUserDirectory), when it is set.
func userConfigPaths() []string {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Dolphin Emulator`, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()
	if v, _, err := k.GetStringValue("UserConfigPath"); err == nil && v != "" {
		return []string{v}
	}
	return nil
}
