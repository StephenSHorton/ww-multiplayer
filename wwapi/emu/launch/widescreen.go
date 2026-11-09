package launch

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Dolphin's Widescreen Hack (GFX.ini [Settings] wideScreenHack) widens the
// game's 3D projection on the GPU so it fills a wider picture, but leaves
// the 2D layers (the HUD, and the name tags the REL places with the game's
// own 4:3 projection) at 4:3, stretched to that picture. On screen a 3D
// point at x lands at centre + (x - centre) * XScale while the 2D layer
// keeps x, so a name tag drifts outward from its remote and its text is
// 1/XScale too wide (#1157). The REL undoes both when Go tells it XScale
// (wwvis.h WWVIS_FLAGS_XSCALE_*).

// Dolphin's AspectMode (Config/GraphicsSettings.h), GFX.ini AspectRatio.
const (
	AspectAuto          = 0
	AspectForceWide     = 1 // "Force 16:9"
	AspectForceStandard = 2 // "Force 4:3"
	AspectStretch       = 3 // "Stretch to Window"
	AspectCustom        = 4
	AspectCustomStretch = 5
)

// GFXAspect is what Dolphin's graphics settings say about the picture's
// shape, for the game GZLE01.
type GFXAspect struct {
	Hack             bool
	Mode             int
	CustomW, CustomH float64
}

// ReadGFXAspect reads userDir's Config/GFX.ini [Settings], then any
// -C / --config GFX.Settings.<key>=<value> on the Dolphin command line
// args, then the user's game settings GameSettings/GZL.ini, GZLE01.ini and
// GZLE01r0.ini [Video_Settings] over them: Dolphin's layer order (Base <
// CommandLine < GlobalGame < LocalGame). Missing files and keys keep
// Dolphin's defaults (hack off, Auto, custom 16:9).
func ReadGFXAspect(userDir string, args []string) GFXAspect {
	a := GFXAspect{Mode: AspectAuto, CustomW: 16, CustomH: 9}
	set := func(k, v string) {
		v = strings.TrimSpace(v)
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "widescreenhack":
			a.Hack = strings.EqualFold(v, "true") || v == "1"
		case "aspectratio":
			if n, err := strconv.Atoi(v); err == nil {
				a.Mode = n
			}
		case "customaspectratiowidth":
			if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
				a.CustomW = f
			}
		case "customaspectratioheight":
			if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
				a.CustomH = f
			}
		}
	}
	if userDir != "" {
		iniSection(filepath.Join(userDir, "Config", "GFX.ini"), "Settings", set)
	}
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
		if !strings.HasPrefix(strings.ToLower(kv), "gfx.settings.") {
			continue
		}
		if k, v, ok := strings.Cut(kv[len("gfx.settings."):], "="); ok {
			set(k, v)
		}
	}
	if userDir != "" {
		for _, name := range []string{"GZL.ini", "GZLE01.ini", "GZLE01r0.ini"} {
			iniSection(filepath.Join(userDir, "GameSettings", name), "Video_Settings", set)
		}
	}
	return a
}

// iniSection calls set for each key = value line in [section] of an ini
// file (keys as written; nothing when the file can't be read).
func iniSection(path, section string, set func(k, v string)) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	in := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			in = strings.EqualFold(line[1:len(line)-1], section)
			continue
		}
		if !in || line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			set(k, v)
		}
	}
}

// standardAspect is the game's picture: Wind Waker draws 4:3.
const standardAspect = 4.0 / 3.0

// XScale is how much the Widescreen Hack narrows the 3D picture's x
// against the 2D layer: the game's 4:3 over the aspect Dolphin draws at
// (VertexShaderManager: the projection's x times source / target when the
// target is wider). renderW x renderH is the render surface, which only
// Stretch modes use; 0 when unknown. 1 means no change: hack off, a 4:3
// or narrower target, Auto (Dolphin keeps Wind Waker at 4:3 there), or a
// Stretch window of unknown size. why names the case for the log.
func (a GFXAspect) XScale(renderW, renderH int) (scale float64, why string) {
	if !a.Hack {
		return 1, "Widescreen Hack off"
	}
	var target float64
	switch a.Mode {
	case AspectForceWide:
		target, why = 16.0/9.0, "Widescreen Hack, Force 16:9"
	case AspectCustom, AspectCustomStretch:
		target, why = a.CustomW/a.CustomH, "Widescreen Hack, custom aspect"
	case AspectStretch:
		if renderW <= 0 || renderH <= 0 {
			return 1, "Widescreen Hack, Stretch to Window, window size unknown"
		}
		target = float64(renderW) / float64(renderH)
		why = "Widescreen Hack, Stretch to Window " + strconv.Itoa(renderW) + "x" + strconv.Itoa(renderH)
	default:
		return 1, "Widescreen Hack with a 4:3 picture"
	}
	if !(target > standardAspect) {
		return 1, why
	}
	return standardAspect / target, why
}
