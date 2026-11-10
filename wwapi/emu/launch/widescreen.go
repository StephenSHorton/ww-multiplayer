package launch

import (
	"bufio"
	"fmt"
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
// -C / --config Graphics.Settings.<key>=<value> on the Dolphin command line
// args, then the user's game settings GameSettings/GZL.ini, GZLE01.ini and
// GZLE01r0.ini [Video_Settings] over them: Dolphin's layer order (Base <
// CommandLine < GlobalGame < LocalGame). Missing files and keys keep
// Dolphin's defaults (hack off, Auto, custom 1:1). Dolphin's -C system name
// for GFX.ini is "Graphics"; GFX.Settings.* is silently dropped, so it is
// ignored here too. A SuggestedAspectRatio other than Auto (the game's own
// suggestion, which the heuristic would otherwise pick) replaces Auto.
func ReadGFXAspect(userDir string, args []string) GFXAspect {
	a := GFXAspect{Mode: AspectAuto, CustomW: 1, CustomH: 1}
	suggested := AspectAuto
	set := func(k, v string) {
		v = strings.TrimSpace(v)
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "widescreenhack":
			a.Hack = strings.EqualFold(v, "true") || v == "1"
		case "aspectratio":
			if n, err := strconv.Atoi(v); err == nil {
				a.Mode = n
			}
		case "suggestedaspectratio":
			if n, err := strconv.Atoi(v); err == nil {
				suggested = n
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
		if !strings.HasPrefix(strings.ToLower(kv), "graphics.settings.") {
			continue
		}
		if k, v, ok := strings.Cut(kv[len("graphics.settings."):], "="); ok {
			set(k, v)
		}
	}
	if userDir != "" {
		for _, name := range []string{"GZL.ini", "GZLE01.ini", "GZLE01r0.ini"} {
			iniSection(filepath.Join(userDir, "GameSettings", name), "Video_Settings", set)
		}
	}
	if a.Mode == AspectAuto && suggested != AspectAuto {
		a.Mode = suggested
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

// VIAspect is the aspect of Wind Waker's picture by its video interface
// (VI width 660 over 480 lines), which Dolphin uses for Auto/Force 4:3 and
// the Stretch modes (docs/design/native-widescreen.md section 2). Computed
// from Dolphin's VideoInterface.cpp as about 1.2534 and measured on the PC
// rig 2026-11-09 (docs/TESTING.md WS-11; Dolphin 2609b, hack off, Force 4:3):
// the dumps keep the width and round the height up to a multiple of 4, 1x
// IR 640x512 and 4x IR 2560x2044, so the VI aspect lies in
// VIAspectMin..VIAspectMax and 1.2534 holds. Recorded only: XScale's
// Stretch cases still compare against standardAspect, and a change there
// needs its own live check.
const (
	VIAspect    = 1.2534
	VIAspectMin = 2560.0 / 2044.0 // 1.2524: the 4x dump as it is
	VIAspectMax = 2560.0 / 2041.0 // 1.2543: the most its rounded-up height can hide
)

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

// WidescreenSetting is the app's "Widescreen" choice, parsed: Off, or an
// output aspect W:H wider than 4:3 (native widescreen, #1186).
type WidescreenSetting struct {
	W, H int // 0, 0 = Off
}

// Off reports whether the setting leaves Dolphin and the game alone.
func (s WidescreenSetting) Off() bool { return s.W == 0 || s.H == 0 }

// Aspect is W/H (0 when Off).
func (s WidescreenSetting) Aspect() float64 {
	if s.Off() {
		return 0
	}
	return float64(s.W) / float64(s.H)
}

// Factor is the aspect over 4:3, the WWWS block's factor (1 when Off).
func (s WidescreenSetting) Factor() float64 {
	if s.Off() {
		return 1
	}
	return s.Aspect() / standardAspect
}

// ParseWidescreenSetting reads "16:9", "16:10", "21:9", an integer "W:H"
// with W/H in (4/3, 4], or "off" (also ""). At or below 4:3 counts as Off,
// as native-widescreen.md section 7 says; no decimals, because Dolphin's
// custom size is integer.
func ParseWidescreenSetting(s string) (WidescreenSetting, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || s == "off" {
		return WidescreenSetting{}, nil
	}
	ws, hs, ok := strings.Cut(s, ":")
	if !ok {
		return WidescreenSetting{}, fmt.Errorf("widescreen %q: want off, 16:9, 16:10, 21:9 or W:H", s)
	}
	w, err1 := strconv.Atoi(ws)
	h, err2 := strconv.Atoi(hs)
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return WidescreenSetting{}, fmt.Errorf("widescreen %q: W and H must be whole numbers above 0", s)
	}
	r := float64(w) / float64(h)
	switch {
	case r > 4:
		return WidescreenSetting{}, fmt.Errorf("widescreen %q: aspect above 4:1 is not supported", s)
	case r <= standardAspect:
		return WidescreenSetting{}, nil
	}
	return WidescreenSetting{W: w, H: h}, nil
}

// WidescreenArgs is the Dolphin command line for a setting (-C lasts for
// the session; GFX.ini is untouched; the system name is Graphics). 16:9
// forces 16:9 with the hack off, any other aspect is Custom; Off adds
// nothing.
func WidescreenArgs(s WidescreenSetting) []string {
	if s.Off() {
		return nil
	}
	const p = "Graphics.Settings."
	if s.W*9 == s.H*16 {
		return []string{"-C", p + "wideScreenHack=False", "-C", p + "AspectRatio=1"}
	}
	return []string{
		"-C", p + "wideScreenHack=False", "-C", p + "AspectRatio=4",
		"-C", p + "CustomAspectRatioWidth=" + strconv.Itoa(s.W),
		"-C", p + "CustomAspectRatioHeight=" + strconv.Itoa(s.H),
	}
}
