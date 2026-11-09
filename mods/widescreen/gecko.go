package widescreen

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// geckoName is Dolphin's own widescreen code (Sys GameSettings/GZLE01.ini,
// off by default). It widens the game itself, so it must never run with
// mode 2 (native-widescreen.md 4.7).
const geckoName = "$16:9 widescreen"

// gameInis are the user's game settings for Wind Waker USA, in Dolphin's
// layer order.
var gameInis = []string{"GZL.ini", "GZLE01.ini", "GZLE01r0.ini"}

// GeckoEnabled says Dolphin runs the "$16:9 Widescreen" Gecko code: it is
// under [Gecko_Enabled] in userDir's GameSettings (and not under a later
// [Gecko_Disabled]), and cheats are on: Config/Dolphin.ini [Core]
// EnableCheats, then -C Dolphin.Core.EnableCheats on the command line, then
// the game settings' [Core] (Dolphin's layer order). The painter box word
// is the runtime check; this is the one that holds before the code ran.
func GeckoEnabled(userDir string, args []string) bool {
	if userDir == "" {
		return false
	}
	ticked, cheats := false, false
	setCheats := func(v string) {
		v = strings.TrimSpace(v)
		cheats = strings.EqualFold(v, "true") || v == "1"
	}
	scanIni(filepath.Join(userDir, "Config", "Dolphin.ini"), func(section, line string) {
		if strings.EqualFold(section, "Core") {
			if k, v, ok := strings.Cut(line, "="); ok && strings.EqualFold(strings.TrimSpace(k), "EnableCheats") {
				setCheats(v)
			}
		}
	})
	for i := 0; i < len(args); i++ {
		var kv string
		switch {
		case (args[i] == "-C" || args[i] == "--config") && i+1 < len(args):
			i++
			kv = args[i]
		case strings.HasPrefix(args[i], "--config="):
			kv = strings.TrimPrefix(args[i], "--config=")
		default:
			continue
		}
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(strings.TrimSpace(k), "Dolphin.Core.EnableCheats") {
			setCheats(v)
		}
	}
	for _, name := range gameInis {
		scanIni(filepath.Join(userDir, "GameSettings", name), func(section, line string) {
			switch {
			case strings.EqualFold(section, "Gecko_Enabled") && isGecko(line):
				ticked = true
			case strings.EqualFold(section, "Gecko_Disabled") && isGecko(line):
				ticked = false
			case strings.EqualFold(section, "Core"):
				if k, v, ok := strings.Cut(line, "="); ok && strings.EqualFold(strings.TrimSpace(k), "EnableCheats") {
					setCheats(v)
				}
			}
		})
	}
	return ticked && cheats
}

func isGecko(line string) bool {
	return strings.HasPrefix(strings.ToLower(line), geckoName)
}

// scanIni calls fn with each non-empty, non-comment line and its section
// (nothing when the file can't be read).
func scanIni(path string, fn func(section, line string)) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	section := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = line[1 : len(line)-1]
			continue
		}
		fn(section, line)
	}
}
