package mailbox

import (
	"encoding/binary"
	"math"
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
	if WsRegion.Start != BoatRegion.End || CanaryArenaHigh.Start != WsRegion.End || WsAddr != 0x8041596C || WsRegion.End != 0x80415984 {
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
