package mailbox

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// The widescreen block in the mailbox slack (inject/include/wwws.h, whose
// WWWS_* defines these mirror; widescreen_test.go pins each one to the
// header). Only T2 (inject/src/ws/widescreen.c) writes it; Go reads it for
// status and changes it through WWAP_OP_WS_SET (OpWsSet).
const (
	WsOff  uint32 = 0xA6C // block start, from Mailbox (0x8041696C)
	WsSize uint32 = 0x18  // ends +0xA84 (0x80416984), where CanaryArenaHigh starts

	WsMagic   uint32 = 0x57575753 // 'WWWS'
	WsVersion        = 1

	WsMagicOff    = 0x00 // u32
	WsVersionOff  = 0x04 // u8
	WsModeOff     = 0x05 // u8
	WsFactorOff   = 0x08 // f32
	WsFlagsOff    = 0x0C // u8
	WsUIModeOff   = 0x0D // u8
	WsUIFactorOff = 0x10 // f32
	WsUIShiftOff  = 0x14 // f32

	WsModeOffVal = 0 // nothing widened
	WsModeClip   = 1 // the clipper only (Dolphin's hack widens the picture)
	WsModeNative = 2 // the camera's aspect, through the 3D knob

	WsF2D  = 0x01 // flags: compress 2D in mode 2 (stage 2)
	WsFHUD = 0x02 // flags: anchor the HUD to the edges (stage 3)

	WsFlagsSupported = 0x03 // the flags the blob supports (stage 3: WsF2D | WsFHUD)

	WsKnobAddr    uint32 = 0x803FA998 // the 4/3 literal preparation() reads for view->mAspect
	WsKnobVanilla uint32 = 0x3FAAAAAB // 4/3 as an f32

	WsFactorMaxBits uint32 = 0x40400000 // 3.0f
	WsFactorDefault uint32 = 0x3FAAAAAB // 4/3 as an f32: ws_init's factor (16:9)

	WsFactorMin = 1.0 // WS_SET refuses a factor outside [WsFactorMin, WsFactorMax]
	WsFactorMax = 3.0
)

// OpWsSet is WWAP_OP_WS_SET: a = mode, b = factor f32 bits, c = flags.
const OpWsSet = OpsOpWsSet

// WsAddr is the block's absolute address (0x8041696C).
const WsAddr = Mailbox + WsOff

// WsRegion is the widescreen block, which the region probe must not arm.
var WsRegion = addr.Region{Name: "widescreen block", Start: WsAddr, End: WsAddr + WsSize}

// Widescreen is a decoded WWWS block.
type Widescreen struct {
	Present  bool // the magic matches
	Version  uint8
	Mode     uint8
	Factor   float32
	Flags    uint8
	UIMode   uint8
	UIFactor float32
	UIShift  float32
}

// DecodeWidescreen decodes the WsSize bytes of the block.
func DecodeWidescreen(b []byte) Widescreen {
	if len(b) < int(WsSize) || binary.BigEndian.Uint32(b[WsMagicOff:]) != WsMagic {
		return Widescreen{}
	}
	f := func(o int) float32 { return math.Float32frombits(binary.BigEndian.Uint32(b[o:])) }
	return Widescreen{
		Present: true, Version: b[WsVersionOff], Mode: b[WsModeOff], Factor: f(WsFactorOff),
		Flags: b[WsFlagsOff], UIMode: b[WsUIModeOff], UIFactor: f(WsUIFactorOff), UIShift: f(WsUIShiftOff),
	}
}

// ReadWidescreen reads and decodes the block (Present false when the blob
// has none or has not run main01_init).
func ReadWidescreen(m interface {
	Read(addr uint32, n int) ([]byte, error)
}) (Widescreen, error) {
	b, err := m.Read(WsAddr, int(WsSize))
	if err != nil {
		return Widescreen{}, err
	}
	return DecodeWidescreen(b), nil
}

// WsFactorValid reports whether WS_SET would accept f (NaN fails).
func WsFactorValid(f float32) bool { return f >= WsFactorMin && f <= WsFactorMax }

// Pending2D reports that the 2D does not yet follow the live mode: the
// next latch would differ from the current one. 2D compression is asked
// for and not latched, or latched and no longer asked for, or latched with
// another factor or HUD shift (a mid-stage aspect or WsFHUD change). The
// 2D latch moves only when a stage or the file select loads
// (native-widescreen.md sections 3.3 and 5).
func (w Widescreen) Pending2D() bool {
	if !w.Present {
		return false
	}
	if !w.Wants2D() {
		return w.UIMode == WsModeNative
	}
	return w.UIMode != WsModeNative || w.UIFactor != w.Factor || w.UIShift != w.wantShift()
}

// wantShift is ws_latch's ui_shift for a block that Wants2D:
// 329.5*(F-1) in f32 with WsFHUD, else 0.
func (w Widescreen) wantShift() float32 {
	if w.Flags&WsFHUD == 0 {
		return 0
	}
	return float32(329.5) * (w.Factor - 1)
}

// Wants2D is ws_latch's test (widescreen.c): the next latch compresses the
// 2D when the block is mode 2 with WsF2D and a valid factor other than 1
// (ws_live_factor() != 1). At factor 1 compression would change nothing,
// so C latches ui_mode 0 there.
func (w Widescreen) Wants2D() bool {
	return w.Mode == WsModeNative && w.Flags&WsF2D != 0 && WsFactorValid(w.Factor) && w.Factor != 1
}

// Pending2DNote is what the Play page and the CLI show while Pending2D.
const Pending2DNote = "2D pending until the next stage load"

func (w Widescreen) String() string {
	if !w.Present {
		return "no widescreen block"
	}
	s := fmt.Sprintf("mode %d factor %.4f flags 0x%02X; 2D mode %d factor %.4f shift %.2f",
		w.Mode, w.Factor, w.Flags, w.UIMode, w.UIFactor, w.UIShift)
	if w.Pending2D() {
		s += " (" + Pending2DNote + ")"
	}
	return s
}
