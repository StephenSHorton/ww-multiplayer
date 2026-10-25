package mailbox

import (
	"math"
	"testing"
)

// wwfxHeader is the C source of the projectile events block constants.
const wwfxHeader = "../../inject/include/wwfx.h"

// TestFxHeaderPins: every Go constant equals its WWFX_* define, and every
// define has a Go constant.
func TestFxHeaderPins(t *testing.T) {
	pinHeader(t, wwfxHeader, headerDefines(t, wwfxHeader), map[string]uint64{
		"WWFX_OFF": uint64(FxOff), "WWFX_SIZE": uint64(FxSize),
		"WWFX_SEND_MAGIC_OFF": FxSendMagicOff, "WWFX_RECV_MAGIC_OFF": FxRecvMagicOff,
		"WWFX_OUT_WRITE_OFF": FxOutWriteOff, "WWFX_OUT_READ_OFF": FxOutReadOff,
		"WWFX_IN_WRITE_OFF": FxInWriteOff, "WWFX_IN_READ_OFF": FxInReadOff,
		"WWFX_STAT_SPAWNED_OFF": FxStatSpawnedOff, "WWFX_STAT_REJECTED_OFF": FxStatRejectedOff,
		"WWFX_STAT_DROPPED_OFF": FxStatDroppedOff, "WWFX_STAT_LOCAL_OFF": FxStatLocalOff,
		"WWFX_STAT_SWALLOW_OFF": FxStatSwallowOff, "WWFX_STAT_CLEARED_OFF": FxStatClearedOff,
		"WWFX_OUT_RING_OFF": FxOutRingOff, "WWFX_IN_RING_OFF": FxInRingOff,
		"WWFX_MAGIC": uint64(FxMagic), "WWFX_RING": FxRing, "WWFX_EV_SIZE": FxEvSize,
		"WWFX_EV_KIND": FxEvKind, "WWFX_EV_SLOT": FxEvSlot, "WWFX_EV_VARIANT": FxEvVariant, "WWFX_EV_ROOM": FxEvRoom,
		"WWFX_EV_ID": FxEvID, "WWFX_EV_POS": FxEvPos, "WWFX_EV_SPEED_F": FxEvSpeedF, "WWFX_EV_SPEED_Y": FxEvSpeedY,
		"WWFX_EV_GRAVITY": FxEvGravity, "WWFX_EV_ANGLE": FxEvAngle, "WWFX_EV_TIMER": FxEvTimer,
		"WWFX_KIND_BOMB_THROW": FxKindBombThrow, "WWFX_KIND_BOMB_PICKUP": FxKindBombPickup,
		"WWFX_KIND_EXPLODE": FxKindExplode, "WWFX_KIND_REMOVE": FxKindRemove, "WWFX_KIND_CANNON": FxKindCannon,
		"WWFX_KIND_ARROW": FxKindArrow, "WWFX_KIND_MAX": FxKindMax,
		"WWFX_ARROW_MAX":     FxArrowMax,
		"WWFX_BOMB_PRM_COPY": uint64(FxBombPrmCopy),
	})
}

// TestFxLayout: the block is the top of the free slack (it ends at the
// arena), past the visuals block; its parts tile it; the rings hold whole
// events; the high canary ends where it starts; the copy bit is none of
// daBomb_c's params fields (d_a_bomb.h:149-160).
func TestFxLayout(t *testing.T) {
	if FxAddr != 0x80413D50 || FxOff+FxSize != RefreshSlackEnd || Mailbox+FxOff+FxSize != PatchedArenaLo || FxOff < VisOff+VisSize {
		t.Fatalf("projectile block at +0x%X (0x%08X), size 0x%X", FxOff, FxAddr, FxSize)
	}
	if FxStatClearedOff+4 != FxOutRingOff || FxOutRingOff+FxRing*FxEvSize != FxInRingOff || FxInRingOff+FxRing*FxEvSize != int(FxSize) {
		t.Error("projectile block parts do not tile it")
	}
	if FxEvTimer+2 != FxEvSize || FxEvAngle+6 != FxEvTimer || FxEvGravity+4 != FxEvAngle {
		t.Error("event fields do not tile an event")
	}
	if FxRing&(FxRing-1) != 0 {
		t.Errorf("FxRing %d is not a power of two (the u32 counters wrap)", FxRing)
	}
	// The eye packets sit between CanaryArenaHigh and the block (wave-3).
	if CanaryArenaHigh.End != EyePacketsRegion.Start || EyePacketsRegion.End != FxRegion.Start || CanaryArenaHigh.Contains(FxAddr, 4) || CanaryArenaHigh.Contains(FxRegion.End-4, 4) {
		t.Errorf("canaries cover the projectile block: high %+v, block %+v", CanaryArenaHigh, FxRegion)
	}
	if FxBombPrmCopy&0x800300FF != 0 {
		t.Errorf("copy bit 0x%08X overlaps daBomb_c's params fields", FxBombPrmCopy)
	}
	if FxOutEntry(0) != FxAddr+FxOutRingOff || FxOutEntry(FxRing+1) != FxAddr+FxOutRingOff+FxEvSize ||
		FxInEntry(FxRing-1)+FxEvSize != FxRegion.End {
		t.Error("ring entry addresses")
	}
}

// TestFxEventRoundTrip: an event encodes at the header's offsets and
// decodes back.
func TestFxEventRoundTrip(t *testing.T) {
	e := FxEvent{Kind: FxKindArrow, Slot: 7, Variant: 3, Room: -1, ID: 0xDEADBEEF, Pos: [3]float32{1, -2, 3.5},
		SpeedF: 10, SpeedY: -4, Gravity: -2.9, Angle: [3]int16{-0x1000, 0x7FFF, 5}, Timer: 150}
	b := e.Bytes()
	if len(b) != FxEvSize || b[FxEvKind] != FxKindArrow || b[FxEvSlot] != 7 || b[FxEvVariant] != 3 || b[FxEvRoom] != 0xFF {
		t.Fatalf("header bytes % x", b[:4])
	}
	if b[FxEvID] != 0xDE || b[FxEvPos] != 0x3F || b[FxEvTimer+1] != 150 {
		t.Errorf("bytes % x", b)
	}
	got, ok := ParseFxEvent(b)
	if !ok || got != e {
		t.Errorf("round trip %+v, want %+v", got, e)
	}
	if _, ok := ParseFxEvent(b[:FxEvSize-1]); ok {
		t.Error("a short entry parsed")
	}
	nan := FxEvent{Pos: [3]float32{float32(math.NaN())}}
	if g, _ := ParseFxEvent(nan.Bytes()); !math.IsNaN(float64(g.Pos[0])) {
		t.Error("NaN lost")
	}
}
