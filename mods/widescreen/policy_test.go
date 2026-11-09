package widescreen

import (
	"math"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/emu/launch"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// TestKnobFor: the knob words of native-widescreen.md 3.1's table.
func TestKnobFor(t *testing.T) {
	for _, c := range []struct{ factor, knob uint32 }{
		{0x3FAAAAAB, 0x3FE38E39}, // 16:9
		{0x3F99999A, 0x3FCCCCCD}, // 16:10
		{0x3FE00000, 0x40155555}, // 21:9
		{0x3FE38E39, 0x4017B426}, // 64:27
		{0x402AAAAB, 0x40638E39}, // 32:9
	} {
		b := mailbox.Widescreen{Present: true, Mode: mailbox.WsModeNative, Factor: math.Float32frombits(c.factor)}
		if got := KnobFor(b); got != c.knob {
			t.Errorf("factor %08X: knob %08X, want %08X", c.factor, got, c.knob)
		}
		b.Mode = mailbox.WsModeClip
		if got := KnobFor(b); got != mailbox.WsKnobVanilla {
			t.Errorf("mode 1: knob %08X", got)
		}
	}
	if got := KnobFor(mailbox.Widescreen{}); got != mailbox.WsKnobVanilla {
		t.Errorf("no block: %08X", got)
	}
}

func TestDecide(t *testing.T) {
	f169 := math.Float32frombits(0x3FAAAAAB)
	set := func(s string) launch.WidescreenSetting {
		w, err := launch.ParseWidescreenSetting(s)
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	wide := launch.GFXAspect{Mode: launch.AspectForceWide, CustomW: 1, CustomH: 1}
	custom := func(w, h float64) launch.GFXAspect {
		return launch.GFXAspect{Mode: launch.AspectCustom, CustomW: w, CustomH: h}
	}
	hack := launch.GFXAspect{Hack: true, Mode: launch.AspectForceWide, CustomW: 1, CustomH: 1}
	clip := mailbox.Widescreen{Present: true, Mode: mailbox.WsModeClip, Factor: f169}
	native := mailbox.Widescreen{Present: true, Mode: mailbox.WsModeNative, Factor: f169}
	game := func(b mailbox.Widescreen) Game { return Game{Block: b, Knob: KnobFor(b), Painter: PainterVanilla} }
	poked := game(clip)
	poked.Knob = 0x3FE38E39
	nativePoked := game(native)
	nativePoked.Knob = 0x40155555
	gecko := game(clip)
	gecko.Painter = PainterGecko

	for _, c := range []struct {
		name    string
		s       launch.WidescreenSetting
		d       Dolphin
		g       Game
		mode    uint8
		factor  float32
		warnHas string // "" = no warning
	}{
		{"16:9, Force 16:9", set("16:9"), Dolphin{GFX: wide}, game(clip), 2, f169, ""},
		{"16:9, already native", set("16:9"), Dolphin{GFX: wide}, game(native), 2, f169, ""},
		{"21:9 custom", set("21:9"), Dolphin{GFX: custom(21, 9)}, game(clip), 2, 1.75, ""},
		{"16:9 asked, Dolphin custom 21:9", set("16:9"), Dolphin{GFX: custom(21, 9)}, game(clip), 2, 1.75, "follows Dolphin"},
		{"hack on", set("16:9"), Dolphin{GFX: hack}, game(clip), 1, f169, "Widescreen Hack is on"},
		{"Auto aspect", set("16:9"), Dolphin{GFX: launch.GFXAspect{CustomW: 1, CustomH: 1}}, game(clip), 0, 1, "Auto"},
		{"Force 4:3", set("16:9"), Dolphin{GFX: launch.GFXAspect{Mode: launch.AspectForceStandard}}, game(clip), 0, 1, "4:3"},
		{"custom 4:3", set("16:9"), Dolphin{GFX: custom(4, 3)}, game(clip), 0, 1, "custom 4:3"},
		{"stretch, hack off", set("16:9"), Dolphin{GFX: launch.GFXAspect{Mode: launch.AspectStretch}}, game(clip), 0, 1, "Stretch"},
		{"knob poked outside mode 2", set("16:9"), Dolphin{GFX: wide}, poked, 0, 1, "camera aspect"},
		{"knob poked in mode 2", set("16:9"), Dolphin{GFX: wide}, nativePoked, 0, 1, "camera aspect"},
		{"gecko by ini", set("16:9"), Dolphin{GFX: wide, Gecko: true}, game(clip), 0, 1, "$16:9 Widescreen"},
		{"gecko by painter", set("16:9"), Dolphin{GFX: wide}, gecko, 0, 1, "$16:9 Widescreen"},
		{"gecko, setting off", set("off"), Dolphin{GFX: hack}, gecko, 0, 1, ""},
		{"off, hack 16:9", set("off"), Dolphin{GFX: hack}, game(clip), 1, f169, ""},
		{"off, hack 21:9", set("off"), Dolphin{GFX: launch.GFXAspect{Hack: true, Mode: launch.AspectCustom, CustomW: 21, CustomH: 9}}, game(clip), 1, 1.75, ""},
		{"off, Force 16:9 hack off: never 2", set("off"), Dolphin{GFX: wide}, game(native), 0, 1, ""},
		{"off, stretch hack 1920x1080", set("off"), Dolphin{GFX: launch.GFXAspect{Hack: true, Mode: launch.AspectStretch}, RenderW: 1920, RenderH: 1080}, game(clip), 1, f169, ""},
		{"off, stretch hack size unknown: C default", set("off"), Dolphin{GFX: launch.GFXAspect{Hack: true, Mode: launch.AspectStretch}}, game(clip), 1, f169, ""},
	} {
		got := Decide(c.s, c.d, c.g)
		if got.Mode != c.mode || got.Factor != c.factor || got.Flags != FlagsAll {
			t.Errorf("%s: %+v, want mode %d factor %v flags 0xFF", c.name, got.Request, c.mode, c.factor)
		}
		if (c.warnHas == "") != (got.Warn == "") || !strings.Contains(got.Warn, c.warnHas) {
			t.Errorf("%s: warn %q, want one with %q", c.name, got.Warn, c.warnHas)
		}
	}
}
