package mailbox

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// The widescreen block in the mailbox slack (inject/include/wwws.h, whose
// WWWS_* defines these mirror; widescreen_test.go pins each one to the
// header). Only T2 (inject/src/ws/widescreen.c) writes it; Go reads it for
// status and changes it through WWAP_OP_WS_SET (OpWsSet).
const (
	WsOff  uint32 = 0xA6C // block start, from Mailbox (0x8041596C)
	WsSize uint32 = 0x18  // ends +0xA84 (0x80415984), where CanaryArenaHigh starts

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

	WsFlagsSupported = 0x00 // the flags the stage-1 blob supports

	WsKnobAddr    uint32 = 0x803FA998 // the 4/3 literal preparation() reads for view->mAspect
	WsKnobVanilla uint32 = 0x3FAAAAAB // 4/3 as an f32

	WsFactorMaxBits uint32 = 0x40400000 // 3.0f
	WsFactorDefault uint32 = 0x3FAAAAAB // 4/3 as an f32: ws_init's factor (16:9)

	WsFactorMin = 1.0 // WS_SET refuses a factor outside [WsFactorMin, WsFactorMax]
	WsFactorMax = 3.0
)

// OpWsSet is WWAP_OP_WS_SET: a = mode, b = factor f32 bits, c = flags.
const OpWsSet = OpsOpWsSet

// WsAddr is the block's absolute address (0x8041596C).
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
func ReadWidescreen(m mem.Reader) (Widescreen, error) {
	b, err := m.Read(WsAddr, int(WsSize))
	if err != nil {
		return Widescreen{}, err
	}
	return DecodeWidescreen(b), nil
}

// WsFactorValid reports whether WS_SET would accept f (NaN fails).
func WsFactorValid(f float32) bool { return f >= WsFactorMin && f <= WsFactorMax }

func (w Widescreen) String() string {
	if !w.Present {
		return "no widescreen block"
	}
	return fmt.Sprintf("mode %d factor %.4f flags 0x%02X", w.Mode, w.Factor, w.Flags)
}
