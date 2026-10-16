package dolphinenv

import (
	"path/filepath"
	"strings"
)

// dolphinExes keeps the paths whose file name is Dolphin's own executable
// (Dolphin.exe, Dolphin, dolphin-emu, any case): dolphin.ListPIDs matches
// every process with "Dolphin" in its name, DolphinTool and the updater
// included.
func dolphinExes(paths []string) []string {
	var out []string
	for _, p := range paths {
		base := p
		if i := strings.LastIndexAny(p, `\/`); i >= 0 {
			base = p[i+1:]
		}
		switch strings.ToLower(base) {
		case "dolphin.exe", "dolphin", "dolphin-emu":
			out = append(out, p)
		}
	}
	return out
}

// UninstallEntry is one Windows uninstall registry key.
type UninstallEntry struct {
	Key, DisplayName, InstallLocation string
}

// installLocationsOf returns the InstallLocation of each entry that is
// Dolphin's: its DisplayName starts with "Dolphin" (any case), and it
// names a folder.
func installLocationsOf(entries []UninstallEntry) []string {
	var out []string
	for _, e := range entries {
		name := strings.ToLower(strings.TrimSpace(e.DisplayName))
		loc := strings.Trim(strings.TrimSpace(e.InstallLocation), `"`)
		if !strings.HasPrefix(name, "dolphin") || loc == "" {
			continue
		}
		out = append(out, filepath.Clean(loc))
	}
	return out
}
