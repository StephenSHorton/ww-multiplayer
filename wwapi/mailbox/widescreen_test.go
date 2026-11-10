package mailbox

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

const wwwsHeader = "../../inject/include/wwws.h"

// TestWidescreenHeaderPins: every Go constant equals its WWWS_* define, and
// every define has a Go constant.
func TestWidescreenHeaderPins(t *testing.T) {
	pinHeader(t, wwwsHeader, headerDefines(t, wwwsHeader), map[string]uint64{
		"WWWS_OFF": uint64(WsOff), "WWWS_SIZE": uint64(WsSize), "WWWS_MAGIC": uint64(WsMagic), "WWWS_VERSION": WsVersion,
		"WWWS_MAGIC_OFF": WsMagicOff, "WWWS_VERSION_OFF": WsVersionOff, "WWWS_MODE_OFF": WsModeOff,
		"WWWS_FACTOR_OFF": WsFactorOff, "WWWS_FLAGS_OFF": WsFlagsOff, "WWWS_UI_MODE_OFF": WsUIModeOff,
		"WWWS_UI_FACTOR_OFF": WsUIFactorOff, "WWWS_UI_SHIFT_OFF": WsUIShiftOff,
		"WWWS_MODE_OFF_VAL": WsModeOffVal, "WWWS_MODE_CLIP": WsModeClip, "WWWS_MODE_NATIVE": WsModeNative,
		"WWWS_F_2D": WsF2D, "WWWS_F_HUD": WsFHUD, "WWWS_FLAGS_SUPPORTED": WsFlagsSupported,
		"WWWS_KNOB_ADDR": uint64(WsKnobAddr), "WWWS_KNOB_VANILLA": uint64(WsKnobVanilla),
		"WWWS_FACTOR_MAX_BITS": uint64(WsFactorMaxBits), "WWWS_FACTOR_DEFAULT": uint64(WsFactorDefault),
	})
	if float64(math.Float32frombits(WsFactorMaxBits)) != WsFactorMax || math.Float32frombits(WsFactorDefault) != float32(4.0/3.0) ||
		math.Float32frombits(WsKnobVanilla) != float32(4.0/3.0) {
		t.Error("factor bit constants disagree with their float meaning")
	}
}

// TestWidescreenLayout: the block follows the boats block, and the high
// canary starts at its end.
func TestWidescreenLayout(t *testing.T) {
	if WsRegion.Start != BoatRegion.End || CanaryArenaHigh.Start != WsRegion.End || WsAddr != 0x8041696C || WsRegion.End != 0x80416984 {
		t.Errorf("block 0x%08X-0x%08X, boats end 0x%08X, high canary 0x%08X", WsRegion.Start, WsRegion.End, BoatRegion.End, CanaryArenaHigh.Start)
	}
	if WsFlagsOff+1 > WsUIFactorOff || WsUIShiftOff+4 != int(WsSize) || OpWsSet != 16 {
		t.Error("field offsets do not tile the block, or the op moved")
	}
	if WsRegion.End > MpEyePacketsOff+Mailbox {
		t.Error("block runs into the eye packets")
	}
}

func TestDecodeWidescreen(t *testing.T) {
	b := make([]byte, WsSize)
	if w := DecodeWidescreen(b); w.Present || w.String() != "no widescreen block" {
		t.Errorf("zero block: %+v", w)
	}
	binary.BigEndian.PutUint32(b[WsMagicOff:], WsMagic)
	b[WsVersionOff], b[WsModeOff] = 1, WsModeClip
	binary.BigEndian.PutUint32(b[WsFactorOff:], WsFactorDefault)
	binary.BigEndian.PutUint32(b[WsUIFactorOff:], math.Float32bits(1))
	w := DecodeWidescreen(b)
	if !w.Present || w.Mode != WsModeClip || w.Factor != float32(4.0/3.0) || w.UIFactor != 1 || w.Version != 1 {
		t.Errorf("decoded %+v", w)
	}
	m := mem.NewFakeMem()
	if err := m.Store(WsAddr, b); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadWidescreen(m); err != nil || got != w {
		t.Errorf("ReadWidescreen %+v, %v", got, err)
	}
	if !WsFactorValid(1) || !WsFactorValid(3) || WsFactorValid(0.99) || WsFactorValid(3.01) || WsFactorValid(float32(math.NaN())) {
		t.Error("WsFactorValid bounds")
	}
}

// TestPending2D: Pending2D is "the next latch would differ from the
// current one", with ws_latch's test (mode 2, WsF2D, a valid factor other
// than 1), so Go never reports a pending 2D change C will not make.
func TestPending2D(t *testing.T) {
	const f169 = float32(16.0 / 9.0 * 3.0 / 4.0)
	for _, c := range []struct {
		name string
		w    Widescreen
		want bool
	}{
		{"asked, not latched", Widescreen{Present: true, Mode: WsModeNative, Factor: f169, Flags: WsF2D}, true},
		{"asked and latched", Widescreen{Present: true, Mode: WsModeNative, Factor: f169, Flags: WsF2D, UIMode: WsModeNative, UIFactor: f169}, false},
		{"HUD asked and latched", Widescreen{Present: true, Mode: WsModeNative, Factor: f169, Flags: WsF2D | WsFHUD, UIMode: WsModeNative, UIFactor: f169, UIShift: 329.5 * (f169 - 1)}, false},
		{"HUD asked, latched without it", Widescreen{Present: true, Mode: WsModeNative, Factor: f169, Flags: WsF2D | WsFHUD, UIMode: WsModeNative, UIFactor: f169}, true},
		{"HUD latched, no longer asked", Widescreen{Present: true, Mode: WsModeNative, Factor: f169, Flags: WsF2D, UIMode: WsModeNative, UIFactor: f169, UIShift: 329.5 * (f169 - 1)}, true},
		{"latched at another factor", Widescreen{Present: true, Mode: WsModeNative, Factor: 1.75, Flags: WsF2D, UIMode: WsModeNative, UIFactor: f169}, true},
		{"latched, mode 0 now", Widescreen{Present: true, Mode: WsModeOffVal, Factor: 1, UIMode: WsModeNative}, true},
		{"latched, mode 1 now", Widescreen{Present: true, Mode: WsModeClip, Factor: f169, Flags: WsF2D, UIMode: WsModeNative}, true},
		{"latched, flags 0 now", Widescreen{Present: true, Mode: WsModeNative, Factor: f169, UIMode: WsModeNative}, true},
		{"mode 2 flags 0, not latched", Widescreen{Present: true, Mode: WsModeNative, Factor: f169}, false},
		{"mode 2 factor 1, not latched", Widescreen{Present: true, Mode: WsModeNative, Factor: 1, Flags: WsF2D}, false},
		{"mode 2 factor 1, latched", Widescreen{Present: true, Mode: WsModeNative, Factor: 1, Flags: WsF2D, UIMode: WsModeNative}, true},
		{"mode 2 bad factor", Widescreen{Present: true, Mode: WsModeNative, Factor: 5, Flags: WsF2D}, false},
		{"mode 2 NaN factor", Widescreen{Present: true, Mode: WsModeNative, Factor: float32(math.NaN()), Flags: WsF2D}, false},
		{"off, not latched", Widescreen{Present: true, Mode: WsModeOffVal, Factor: 1}, false},
		{"no block", Widescreen{Mode: WsModeNative, Factor: f169, Flags: WsF2D}, false},
	} {
		if got := c.w.Pending2D(); got != c.want {
			t.Errorf("%s: Pending2D %v, want %v", c.name, got, c.want)
		}
		if s := c.w.String(); strings.Contains(s, Pending2DNote) != c.want {
			t.Errorf("%s: String %q", c.name, s)
		}
	}
}
