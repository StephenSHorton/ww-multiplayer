package inventory

import (
	"encoding/binary"
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// Context is the live state, outside the save image, a plan depends on.
type Context struct {
	// StageName is mCurStage's name, what dComIfGp_getStartStageName
	// returns (d_com_inf_game.h:283).
	StageName string
	// StageType is the loaded STAG chunk's stage type
	// (dStage_stagInfo_GetSTType, d_stage.h:1031-1033); StageTypeOK is
	// false when the chunk could not be read.
	StageType   int
	StageTypeOK bool
	// SelectItem is play.mSelectItem[0..2], the item the HUD shows on X,
	// Y and Z.
	SelectItem [Buttons]byte
	// ArrowCount and BombCount are the HUD's pending ammo counters
	// mItemArrowNumCount and mItemBombNumCount.
	ArrowCount, BombCount int16
}

// stageTypeFF1 is dStageType_FF1_e (d_stage.h:22), the Forsaken Fortress
// of the first visit.
const stageTypeFF1 = 5

// stagOffType is the byte of stage_stag_info_class whose low three bits
// are the stage type: mStageTypeAndSchbit at +0x0C (d_stage.h:72) holds
// it in bits 16-18, the low bits of its big-endian second byte.
const stagOffType = 0x0D

// stageGTower is the stage the game takes the bow away in
// (d_s_play.cpp:1376-1384).
const stageGTower = "GTower"

// event0801 is dSv_event_flag_c::UNK_0801, set by Tetra's actor
// (d_a_npc_zl1.cpp:1860) and never cleared.
var event0801 = addr.EventByte(0x0801)

// ff1 reports whether the FF1 sword rule holds for raw: event 0x0801 is
// set and the stage is FF1-type, the stage load that takes the sword away
// (d_s_play.cpp:1369-1374; the barrel that carries Link there does too,
// d_a_obj_demo_barrel.cpp:101-104). A STAG that can't be read counts as
// FF1-type: the sword waits.
func (c Context) ff1(raw *world.Image) bool {
	if raw[addr.OffEvent+event0801.Off]&event0801.Mask == 0 {
		return false
	}
	return !c.StageTypeOK || c.StageType == stageTypeFF1
}

// gtower reports whether the GTower bow rule holds: the game empties the
// bow slot and unbinds its buttons at every GTower stage load, and gives
// the bow back at the next load anywhere else (d_s_play.cpp:1376-1392).
func (c Context) gtower() bool { return c.StageName == stageGTower }

// counter returns the pending ammo counter at a.
func (c Context) counter(a uint32) int16 {
	switch a {
	case ItemArrowNumCount:
		return c.ArrowCount
	case ItemBombNumCount:
		return c.BombCount
	}
	return 0
}

// ContextOf returns the context s shows, with the loaded STAG's stage
// type typ (ok false when it could not be read), as the replica reads it.
func ContextOf(s *state.Snapshot, typ int, ok bool) Context {
	pl := func(a uint32) []byte { return s.Player[a-state.PlayerRegion.Start:] }
	c := Context{StageName: s.StageName, StageType: typ & 7, StageTypeOK: ok}
	copy(c.SelectItem[:], pl(addr.PlaySelectItem))
	c.ArrowCount = int16(binary.BigEndian.Uint16(pl(ItemArrowNumCount)))
	c.BombCount = int16(binary.BigEndian.Uint16(pl(ItemBombNumCount)))
	return c
}

// ReadContext returns the context s shows, reading the stage type from
// the STAG chunk s.StagInfo points at.
func ReadContext(m mem.Mem, s *state.Snapshot) Context {
	typ, ok := 0, false
	if s.StagInfo != 0 {
		if b, err := mem.ReadU8(m, s.StagInfo+stagOffType); err == nil {
			typ, ok = int(b&7), true
		}
	}
	return ContextOf(s, typ, ok)
}

// Rule is a context rule that holds writes back (§6.6).
type Rule uint8

const (
	// RuleFF1Sword holds the sword (mCollect[0] and mSelectEquip[0])
	// while the receiver is in an FF1-type stage with event 0x0801 set:
	// that stage load takes the sword away (d_s_play.cpp:1369-1374), and
	// Link finds it again inside.
	RuleFF1Sword Rule = iota + 1
	// RuleGTowerBow holds the bow (mGetItem[12], the bow slot and its
	// buttons) while the receiver is in GTower, where the game takes it
	// away (d_s_play.cpp:1376-1384).
	RuleGTowerBow
	// RuleAmmoBusy holds a first bow or bomb bag grant while the HUD's
	// pending ammo counter for it is not 0, so the grant's ammo fill can
	// start with it (§3.2 IDLE: a pending counter is written only while
	// it reads 0). The HUD empties the counter within a frame.
	RuleAmmoBusy
)

func (r Rule) String() string {
	switch r {
	case RuleFF1Sword:
		return "FF1 sword: the Forsaken Fortress takes the sword away (d_s_play.cpp:1369-1374)"
	case RuleGTowerBow:
		return "GTower bow: GTower takes the bow away (d_s_play.cpp:1376-1384)"
	case RuleAmmoBusy:
		return "ammo counter busy: the HUD's pending ammo count is not 0 (d_meter.cpp:2873-2905)"
	}
	return fmt.Sprintf("Rule(%d)", uint8(r))
}
