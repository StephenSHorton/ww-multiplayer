package mailbox

import (
	"bytes"
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// wwrefreshHeader is the C source of the actor_refresh constants.
const wwrefreshHeader = "../../inject/include/wwrefresh.h"

// TestRefreshHeaderPins: every Go constant equals its WWRF_* define, and
// every define has a Go constant.
func TestRefreshHeaderPins(t *testing.T) {
	pinHeader(t, wwrefreshHeader, headerDefines(t, wwrefreshHeader), map[string]uint64{
		"WWREFRESH_ENABLE":          RefreshEnable,
		"WWRF_OFF":                  uint64(RefreshOff),
		"WWRF_SIZE":                 uint64(RefreshSize),
		"WWRF_SLACK_END":            uint64(RefreshSlackEnd),
		"WWRF_SEQ_OFF":              RefreshSeqOff,
		"WWRF_PROC_OFF":             RefreshProcOff,
		"WWRF_TAG_OFF":              RefreshTagOff,
		"WWRF_PID_OFF":              RefreshPIDOff,
		"WWRF_PARAMS_OFF":           RefreshParamsOff,
		"WWRF_ROOM_OFF":             RefreshRoomOff,
		"WWRF_BIT_KIND_OFF":         RefreshBitKindOff,
		"WWRF_BIT_NO_OFF":           RefreshBitNoOff,
		"WWRF_ACK_OFF":              RefreshAckOff,
		"WWRF_RESULT_OFF":           RefreshResultOff,
		"WWRF_NEW_PID_OFF":          RefreshNewPIDOff,
		"WWRF_GO_SPAN":              RefreshGoSpan,
		"WWRF_TAG":                  uint64(RefreshTag),
		"WWRF_BIT_SW":               uint64(RefreshBitSw),
		"WWRF_BIT_TBOX":             uint64(RefreshBitTbox),
		"WWRF_BIT_ITEM":             uint64(RefreshBitItem),
		"WWRF_R_NONE":               uint64(RefreshRNone),
		"WWRF_R_APPLIED":            uint64(RefreshRApplied),
		"WWRF_R_NO_ACTOR":           uint64(RefreshRNoActor),
		"WWRF_R_MISMATCH":           uint64(RefreshRMismatch),
		"WWRF_R_GATE":               uint64(RefreshRGate),
		"WWRF_R_NOT_SET":            uint64(RefreshRNotSet),
		"WWRF_R_REFUSED":            uint64(RefreshRRefused),
		"WWRF_R_FP_MISS":            uint64(RefreshRFpMiss),
		"WWRF_R_RECREATED":          uint64(RefreshRRecreated),
		"WWRF_R_IN_STATE":           uint64(RefreshRInState),
		"WWRF_R_CREATE_FAILED":      uint64(RefreshRCreateFailed),
		"WWRF_GATE_STAGE_CHANGE":    uint64(RefreshGateStageChange),
		"WWRF_GATE_BOSS_STAGE":      uint64(RefreshGateBossStage),
		"WWRF_GATE_INIT":            uint64(RefreshGateInit),
		"WWRF_GATE_EVENT_CMD":       uint64(RefreshGateEventCmd),
		"WWRF_GATE_CARRY":           uint64(RefreshGateCarry),
		"WWRF_GATE_MIRROR":          uint64(RefreshGateMirror),
		"WWRF_GATE_LINK_DEMO":       uint64(RefreshGateLinkDemo),
		"WWRF_GATE_LINK_NEAR":       uint64(RefreshGateLinkNear),
		"WWRF_GATE_PROC":            uint64(RefreshGateProc),
		"WWRF_GATE_EVENT_MODE":      uint64(RefreshGateEventMode),
		"WWRF_GATE_EVENT_ORDER":     uint64(RefreshGateEventOrder),
		"WWRF_PROC_LEAVES":          uint64(RefreshProcLeaves),
		"WWRF_PROC_ITEM":            uint64(RefreshProcItem),
		"WWRF_PROC_TBOX":            uint64(RefreshProcTbox),
		"WWRF_PROC_SAKU":            uint64(RefreshProcSaku),
		"WWRF_PROC_STONE2":          uint64(RefreshProcStone2),
		"WWRF_PROC_VMC":             uint64(RefreshProcVmc),
		"WWRF_PROC_VFAN":            uint64(RefreshProcVfan),
		"WWRF_PROC_WARPT":           uint64(RefreshProcWarpt),
		"WWRF_PROC_MJDOOR":          uint64(RefreshProcMjdoor),
		"WWRF_PROC_MKIE":            uint64(RefreshProcMkie),
		"WWRF_PROC_MKIEK":           uint64(RefreshProcMkiek),
		"WWRF_PROC_MKNJD":           uint64(RefreshProcMknjd),
		"WWRF_PROC_MFLFT":           uint64(RefreshProcMflft),
		"WWRF_PROC_FLOOR":           uint64(RefreshProcFloor),
		"WWRF_PROC_KOKIIE":          uint64(RefreshProcKokiie),
		"WWRF_PROC_DOOR10":          uint64(RefreshProcDoor10),
		"WWRF_PROC_DOOR12":          uint64(RefreshProcDoor12),
		"WWRF_PROC_WALL":            uint64(RefreshProcWall),
		"WWRF_PROC_SWHIT0":          uint64(RefreshProcSwhit0),
		"WWRF_PROC_ICE":             uint64(RefreshProcIce),
		"WWRF_DOOR_ROWS":            RefreshDoorRows,
		"WWRF_ROW_SW_MASK":          RefreshRowSwMask,
		"WWRF_MKIE_SW_SHIFT":        RefreshMkieSwShift,
		"WWRF_VMC_SW_SHIFT":         RefreshVmcSwShift,
		"WWRF_MFLFT_SW_SHIFT":       RefreshMflftSwShift,
		"WWRF_KOKIIE_SW_SHIFT":      RefreshKokiieSwShift,
		"WWRF_MEM_SW_END":           RefreshMemSwEnd,
		"WWRF_SWHIT0_TIMER_SHIFT":   RefreshSwhit0TimerSh,
		"WWRF_WARPT_TYPE_MASK":      RefreshWarptTypeMask,
		"WWRF_WARPT_TYPE_ON":        RefreshWarptTypeOn,
		"WWRF_WARPT_TYPE_SP_LO":     RefreshWarptTypeSpLo,
		"WWRF_WARPT_TYPE_SP_HI":     RefreshWarptTypeSpHi,
		"WWRF_DOOR12_ARG1_TMP":      RefreshDoor12Arg1Tmp,
		"WWRF_AC_PROC_OFF":          RefreshAcProcOff,
		"WWRF_AC_PAUSE_OFF":         RefreshAcPauseOff,
		"WWRF_AC_INIT_OFF":          RefreshAcInitOff,
		"WWRF_AC_INIT_RUNNING":      RefreshAcInitRunning,
		"WWRF_AC_EVTCMD_OFF":        RefreshAcEvtCmdOff,
		"WWRF_AC_POS_OFF":           RefreshAcPosOff,
		"WWRF_SV_INFO":              uint64(RefreshSvInfo),
		"WWRF_SV_MEMBIT":            uint64(RefreshSvMemBit),
		"WWRF_IS_SWITCH_FN":         uint64(RefreshIsSwitchFn),
		"WWRF_IS_TBOX_FN":           uint64(RefreshIsTboxFn),
		"WWRF_IS_ITEM_FN":           uint64(RefreshIsItemFn),
		"WWRF_ZONE_SW_MIN":          RefreshZoneSwMin,
		"WWRF_EVT_MODE":             uint64(RefreshEvtMode),
		"WWRF_EVT_ORDER_COUNT":      uint64(RefreshEvtOrders),
		"WWRF_LINK_FAR":             RefreshLinkFar,
		"WWRF_STONE2_SW_SHIFT":      RefreshStone2SwShift,
		"WWRF_STONE2_SW_MASK":       RefreshStone2SwMask,
		"WWRF_STONE2_M648_OFF":      RefreshStone2M648Off,
		"WWRF_LEAVES_SW_SHIFT":      RefreshLeavesSwShift,
		"WWRF_LEAVES_SW_MASK":       RefreshLeavesSwMask,
		"WWRF_ITEM_BITNO_OFF":       RefreshItemBitNoOff,
		"WWRF_ITEM_BITNO_MASK":      RefreshItemBitNoMask,
		"WWRF_ITEM_BIT_NONE":        RefreshItemBitNone,
		"WWRF_ITEM_NO_OFF":          RefreshItemNoOff,
		"WWRF_ITEM_NO_BLUE_JELLY":   RefreshItemNoBlueJelly,
		"WWRF_ITEM_FLAG_OFF":        RefreshItemFlagOff,
		"WWRF_ITEM_FLAG_BUSY":       RefreshItemFlagBusy,
		"WWRF_ITEM_STATUS_OFF":      RefreshItemStatusOff,
		"WWRF_ITEM_STATUS_WAIT_MAX": RefreshItemStatusWaitMax,
		"WWRF_TBOX_FUNC_MASK":       RefreshTboxFuncMask,
		"WWRF_TBOX_FUNC_REFUSE_LO":  RefreshTboxFuncRefuseLo,
		"WWRF_TBOX_FUNC_REFUSE_HI":  RefreshTboxFuncRefuseHi,
		"WWRF_TBOX_BIT_SHIFT":       RefreshTboxBitShift,
		"WWRF_TBOX_BIT_MASK":        RefreshTboxBitMask,
		"WWRF_TBOX_BG_OPEN_OFF":     RefreshTboxBgOpenOff,
		"WWRF_TBOX_BG_CUR_OFF":      RefreshTboxBgCurOff,
		"WWRF_TBOX_ACT_FN_OFF":      RefreshTboxActFnOff,
		"WWRF_TBOX_FLAGS_OFF":       RefreshTboxFlagsOff,
		"WWRF_TBOX_FLAGS_BUSY":      RefreshTboxFlagsBusy,
		"WWRF_TBOX_TEXT_EXEC":       RefreshTboxTextExec,
		"WWRF_TBOX_TEXT_OPEN_WAIT":  RefreshTboxTextOpenWait,
		"WWRF_TBOX_TEXT_SW_WAIT":    RefreshTboxTextSwWait,
		"WWRF_TBOX_TEXT_SW_WAIT2":   RefreshTboxTextSwWait2,
		"WWRF_TBOX_FP_OFF0":         RefreshTboxFpOff0,
		"WWRF_TBOX_FP_WORD0":        RefreshTboxFpWord0,
		"WWRF_TBOX_FP_OFF1":         RefreshTboxFpOff1,
		"WWRF_TBOX_FP_WORD1":        RefreshTboxFpWord1,
		"WWRF_SAKU_SW1_SHIFT":       RefreshSakuSw1Shift,
		"WWRF_SAKU_SW2_SHIFT":       RefreshSakuSw2Shift,
		"WWRF_SAKU_SW_MASK":         RefreshSakuSwMask,
		"WWRF_SAKU_HALF1_MASK":      RefreshSakuHalf1Mask,
		"WWRF_SAKU_BURN_OFF":        RefreshSakuBurnOff,
		"WWRF_SAKU_STATE_OFF":       RefreshSakuStateOff,
		"WWRF_SAKU_STATE_STANDING":  RefreshSakuStateStanding,
		"WWRF_SAKU_STATE_BURNING":   RefreshSakuStateBurning,
		"WWRF_SAKU_STATE_BROKEN":    RefreshSakuStateBroken,
		"WWRF_SAKU_SW_OFF":          RefreshSakuSwOff,
	})
}

// TestRefreshPlace: the request sits right after the enemy knob table, is
// word-aligned, ends inside the free slack, and the visuals block starts at
// its end (CanaryArenaHigh did until the visuals block took the space,
// vis_test.go); the Go span ends at the ack.
func TestRefreshPlace(t *testing.T) {
	if RefreshOff != EnemyKnobOff+EnemyKnobSize || RefreshOff%4 != 0 || RefreshOff+RefreshSize > RefreshSlackEnd {
		t.Fatalf("actor_refresh +0x%X..+0x%X, want right after the knob table (+0x%X) and below +0x%X",
			RefreshOff, RefreshOff+RefreshSize, EnemyKnobOff+EnemyKnobSize, RefreshSlackEnd)
	}
	if RefreshAddr != 0x804137B0 || RefreshRegion.End != 0x804137D0 || VisRegion.Start != RefreshRegion.End {
		t.Fatalf("actor_refresh %#x..%#x, visuals block from %#x", RefreshRegion.Start, RefreshRegion.End, VisRegion.Start)
	}
	for _, c := range []struct{ s, e uint32 }{{CanaryArena.Start, CanaryArena.End}, {CanaryArenaHigh.Start, CanaryArenaHigh.End}, {CanaryTRK.Start, CanaryTRK.End}} {
		if RefreshRegion.Start < c.e && c.s < RefreshRegion.End {
			t.Fatalf("actor_refresh overlaps a canary arena %#x..%#x", c.s, c.e)
		}
	}
	if RefreshAckOff != RefreshGoSpan || RefreshResultOff+4 != RefreshNewPIDOff || RefreshNewPIDOff+4 != int(RefreshSize) {
		t.Error("the REL-written words do not follow the Go span")
	}
}

// TestRefreshFieldsAndRead: a request's writes land at the header's
// offsets, and ReadRefresh decodes the REL's half.
func TestRefreshFieldsAndRead(t *testing.T) {
	r := RefreshRequest{Seq: 7, Proc: RefreshProcStone2, PID: 0x1234, Params: 0x0000110A, Room: -1, BitKind: RefreshBitSw, BitNo: 0x11}
	body, seq := r.Fields()
	want := []byte{0x01, 0xCD, 0x52, 0x46, 0, 0, 0x12, 0x34, 0, 0, 0x11, 0x0A, 0xFF, 1, 0, 0x11}
	if !bytes.Equal(body, want) || !bytes.Equal(seq, []byte{0, 0, 0, 7}) {
		t.Fatalf("fields % X / % X", body, seq)
	}
	m := mem.NewFakeMem()
	if err := m.Write(RefreshAddr+RefreshProcOff, body); err != nil {
		t.Fatal(err)
	}
	if err := m.Write(RefreshAddr, seq); err != nil {
		t.Fatal(err)
	}
	if err := m.Write(RefreshAddr+RefreshAckOff, []byte{0, 0, 0, 7, RefreshRGate, RefreshGateLinkNear, 0, 1}); err != nil {
		t.Fatal(err)
	}
	st, err := ReadRefresh(m)
	if err != nil || st != (RefreshStatus{Seq: 7, Ack: 7, Result: RefreshRGate, Gate: RefreshGateLinkNear, ProcState: 1}) {
		t.Fatalf("ReadRefresh = %+v, %v", st, err)
	}
	if RefreshResultName(st.Result) != "gated" || RefreshGateName(st.Gate) != "Link near" || RefreshProcName(r.Proc) != "Stone2" {
		t.Error("names")
	}
	// A re-create's answer carries the new actor's pid at +0x1C.
	if err := m.Write(RefreshAddr+RefreshAckOff, []byte{0, 0, 0, 7, RefreshRRecreated, 0, 0, 0, 0, 0, 0x56, 0x78}); err != nil {
		t.Fatal(err)
	}
	if st, err = ReadRefresh(m); err != nil || st.Result != RefreshRRecreated || st.NewPID != 0x5678 || RefreshResultName(st.Result) != "re-created" {
		t.Fatalf("ReadRefresh = %+v, %v", st, err)
	}
}
