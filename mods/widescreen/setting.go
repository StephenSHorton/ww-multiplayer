package widescreen

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

const standard = 4.0 / 3.0

// Setting is what the CLI asks for: Off, Auto (follow Dolphin's graphics
// settings), or an aspect.
type Setting struct {
	Auto   bool
	Off    bool
	Aspect float64 // W/H, when neither Auto nor Off
}

// ParseSetting reads the CLI forms: auto, off, 16:9, 16:10, 21:9, 32:9,
// W:H, or a decimal aspect like 2.39.
func ParseSetting(s string) (Setting, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "auto":
		return Setting{Auto: true}, nil
	case "off", "":
		return Setting{Off: true}, nil
	}
	if w, h, ok := strings.Cut(s, ":"); ok {
		fw, e1 := strconv.ParseFloat(w, 64)
		fh, e2 := strconv.ParseFloat(h, 64)
		if e1 != nil || e2 != nil || !(fw > 0) || !(fh > 0) || math.IsInf(fw, 0) || math.IsInf(fh, 0) {
			return Setting{}, fmt.Errorf("widescreen %q: want auto, off, 16:9, 16:10, 21:9, 32:9, W:H or a decimal aspect", s)
		}
		return Setting{Aspect: fw / fh}, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || !(f > 0) || math.IsInf(f, 0) {
		return Setting{}, fmt.Errorf("widescreen %q: want auto, off, 16:9, 16:10, 21:9, 32:9, W:H or a decimal aspect", s)
	}
	return Setting{Aspect: f}, nil
}

// Factor is aspect / (4/3) clamped to [1, 3]; an aspect at or below 4:3
// gives 1.
func (s Setting) Factor() float64 {
	if s.Auto || s.Off || !(s.Aspect > standard) {
		return 1
	}
	return math.Min(s.Aspect/standard, mailbox.WsFactorMax)
}

// ParseAppSetting is the app's saved choice (config.Mods["widescreen"]
// Settings.aspect): off, 16:9, 16:10, 21:9 or an integer W:H in (4/3, 4].
// No auto, no decimals; separate from ParseSetting on purpose
// (native-widescreen.md section 7). Unset is off.
func ParseAppSetting(s string) (launch.WidescreenSetting, error) {
	return launch.ParseWidescreenSetting(s)
}

// Request is the WS_SET command: mode, factor and flags.
type Request struct {
	Mode   uint8
	Factor float32
	Flags  uint8
}

// Words are the op's a, b, c.
func (r Request) Words() (a, b, c uint32) {
	return uint32(r.Mode), math.Float32bits(r.Factor), uint32(r.Flags)
}

// RequestFor is the request for a setting in mode (clip or native): mode 0
// when the factor is 1, since nothing is to be widened.
func RequestFor(s Setting, mode uint8, flags uint8) Request {
	f := float32(s.Factor())
	if f <= 1 {
		return Request{Mode: mailbox.WsModeOffVal, Factor: 1, Flags: flags}
	}
	return Request{Mode: mode, Factor: f, Flags: flags}
}
