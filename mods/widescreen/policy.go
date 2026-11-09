package widescreen

import (
	"fmt"
	"math"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// The mode follows Dolphin, not the setting (native-widescreen.md section
// 7): Decide picks what WS_SET should hold from the app's setting, what
// Dolphin runs and what the game's memory shows.

// Painter box words: the 2D painter's left edge (-9.0), which Dolphin's
// "$16:9 Widescreen" Gecko code rewrites to -123.0 and we never write.
const (
	PainterAddr    uint32 = 0x803F7D68
	PainterVanilla uint32 = 0xC1100000
	PainterGecko   uint32 = 0xC2F60000
)

// FlagsAll is WS_SET's c from Go: everything the build supports.
const FlagsAll = 0xFF

// Dolphin is what Decide knows of Dolphin's settings.
type Dolphin struct {
	GFX              launch.GFXAspect
	RenderW, RenderH int  // the render size, for the Stretch modes; 0 when unknown
	Gecko            bool // GeckoEnabled: the "$16:9 Widescreen" code ticked and cheats on
}

// Game is what Decide reads of the game's memory.
type Game struct {
	Block   mailbox.Widescreen
	Knob    uint32 // the word at mailbox.WsKnobAddr
	Painter uint32 // the word at PainterAddr
}

// Decision is the request to hold and a one-line warning for the Play
// page ("" when Dolphin runs what was asked).
type Decision struct {
	Request
	Warn string
}

// KnobFor is the 3D knob C writes for a block: (4*factor)/3 in mode 2
// (one f32 rounding each, as widescreen.c), vanilla 4/3 otherwise.
func KnobFor(b mailbox.Widescreen) uint32 {
	if !b.Present || b.Mode != mailbox.WsModeNative || !mailbox.WsFactorValid(b.Factor) {
		return mailbox.WsKnobVanilla
	}
	return math.Float32bits(float32(float32(4*b.Factor) / 3))
}

// Decide picks the mode, factor and flags for setting s. Mode 2 only when
// s is on, the hack is off, the aspect is Force 16:9 or Custom wider than
// 4:3, the knob holds what C last wrote (no other widener) and no Gecko
// code runs; its factor is Dolphin's aspect over 4:3. Else mode 1 with the
// hack's factor (1/XScale; mode 0 when XScale is 1), or mode 0 when the
// Gecko code is on.
func Decide(s launch.WidescreenSetting, d Dolphin, g Game) Decision {
	gecko := d.Gecko || g.Painter != PainterVanilla
	if gecko {
		dec := Decision{Request: Request{Mode: mailbox.WsModeOffVal, Factor: 1, Flags: FlagsAll}}
		if !s.Off() {
			dec.Warn = `Dolphin's "$16:9 Widescreen" cheat is on, so the app leaves widescreen to it; untick it in Dolphin's Gecko codes for the app's widescreen.`
		}
		return dec
	}
	dec := Decision{Request: clipRequest(d)}
	if s.Off() {
		return dec
	}
	a := d.GFX
	target := 0.0
	switch a.Mode {
	case launch.AspectForceWide:
		target = 16.0 / 9.0
	case launch.AspectCustom:
		target = a.CustomW / a.CustomH
	}
	switch {
	case a.Hack:
		dec.Warn = "Dolphin's Widescreen Hack is on (its graphics settings or GameSettings/GZLE01.ini), so only the culling is widened; turn the hack off and start Dolphin again."
	case !(target > standard):
		dec.Warn = fmt.Sprintf("Dolphin's aspect ratio is %s, not %s: start Dolphin from the app (or set Force 16:9 or Custom) for widescreen.", aspectName(a), settingName(s))
	case g.Knob != KnobFor(g.Block):
		dec.Warn = fmt.Sprintf("Something else changes the game's camera aspect (0x%08X holds 0x%08X), maybe another widescreen cheat; the app stays out of its way.", mailbox.WsKnobAddr, g.Knob)
	default:
		f := float32(math.Min(target/standard, mailbox.WsFactorMax))
		dec.Request = Request{Mode: mailbox.WsModeNative, Factor: f, Flags: FlagsAll}
		if f != float32(s.Factor()) {
			dec.Warn = fmt.Sprintf("Dolphin draws %s, not the %s you chose; the game follows Dolphin.", aspectName(a), settingName(s))
		}
	}
	return dec
}

// clipRequest is mode 1 at the hack's factor, or mode 0 when nothing is
// widened.
func clipRequest(d Dolphin) Request {
	g := d.GFX
	if g.Hack && g.Mode == launch.AspectStretch && (d.RenderW <= 0 || d.RenderH <= 0) {
		// The window's size is unknown, so the hack's factor is too: keep
		// ws_init's default (mode 1 at 4/3) rather than drop the culling fix.
		return Request{Mode: mailbox.WsModeClip, Factor: float32(4.0 / 3), Flags: FlagsAll}
	}
	x, _ := g.XScale(d.RenderW, d.RenderH)
	if !(x > 0 && x < 1) {
		return Request{Mode: mailbox.WsModeOffVal, Factor: 1, Flags: FlagsAll}
	}
	return Request{Mode: mailbox.WsModeClip, Factor: float32(math.Min(1/x, mailbox.WsFactorMax)), Flags: FlagsAll}
}

func aspectName(a launch.GFXAspect) string {
	switch a.Mode {
	case launch.AspectAuto:
		return "Auto"
	case launch.AspectForceWide:
		return "16:9"
	case launch.AspectForceStandard:
		return "4:3"
	case launch.AspectStretch:
		return "Stretch to Window"
	case launch.AspectCustom, launch.AspectCustomStretch:
		return fmt.Sprintf("custom %g:%g", a.CustomW, a.CustomH)
	}
	return fmt.Sprintf("mode %d", a.Mode)
}

func settingName(s launch.WidescreenSetting) string {
	return fmt.Sprintf("%d:%d", s.W, s.H)
}

// Describe is a request for the log, e.g. "native widescreen (mode 2,
// factor 1.333)".
func (r Request) Describe() string {
	switch r.Mode {
	case mailbox.WsModeNative:
		return fmt.Sprintf("native widescreen (mode 2, factor %.3f)", r.Factor)
	case mailbox.WsModeClip:
		return fmt.Sprintf("culling widened for Dolphin's Widescreen Hack (mode 1, factor %.3f)", r.Factor)
	}
	return "off (mode 0)"
}
