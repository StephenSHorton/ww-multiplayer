package mailbox

import (
	"encoding/binary"
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// The actor_refresh request in the mailbox slack (inject/include/
// wwrefresh.h, whose WWRF_* defines these mirror; refresh_test.go pins
// each one to the header). Co-op's replica (mods/coop/replica refresh.go,
// the --coop-refresh token set, empty by default) writes it when a bit the
// partner set has landed here and this game still runs the actor it
// placed before the bit; the REL module (inject/rel/refresh.c) re-derives
// and re-reads the bit, runs the generic and per-proc gates, and deletes
// the actor. 0x20 bytes at Mailbox+RefreshOff, right after the enemy knob
// table; P0-L5 proved no game code writes them. Design:
// ww-multiplayer-handoff/design/enemy-mirror-batch-d.json R-D4, R-D5,
// D4-D7, "actor_refresh"; research/stale-actor-refresh.md Tier 3.
//
// Go writes +0x04..+0x14, then the seq (two writes, the seq last); the
// REL writes the result, then the ack. The keeper's create consumes any
// seq the block holds (ack = seq, result RefreshRNone).
const (
	RefreshEnable = 1 // WWREFRESH_ENABLE, the REL's compile switch

	RefreshOff         uint32 = 0x8B0  // request start, from Mailbox (0x804137B0)
	RefreshSize        uint32 = 0x20   // ends at +0x8D0, where the visuals block starts (vis.go VisOff)
	RefreshSlackEnd    uint32 = 0x1100 // the free slack's end (wwremote.h)
	RefreshSeqOff             = 0x00   // u32 request seq, never 0 (Go)
	RefreshProcOff            = 0x04   // u16 the actor's proc (Go)
	RefreshTagOff             = 0x06   // u16 RefreshTag (Go)
	RefreshPIDOff             = 0x08   // u32 the actor's process id (Go)
	RefreshParamsOff          = 0x0C   // u32 its params +0xB0 (Go)
	RefreshRoomOff            = 0x10   // s8 its home room +0x1E2 (Go)
	RefreshBitKindOff         = 0x11   // u8 RefreshBit* (Go)
	RefreshBitNoOff           = 0x12   // u16 the bit's number (Go)
	RefreshAckOff             = 0x14   // u32 the seq last consumed (REL)
	RefreshResultOff          = 0x18   // u32 BE: byte +0 RefreshR*, +1 RefreshGate* (GATE only), +2..+3 the proc's state (REL)
	RefreshReservedOff        = 0x1C   // u32 0
	RefreshGoSpan             = 0x14   // bytes Go writes: seq .. bit no

	RefreshTag uint16 = 0x5246 // 'RF'

	RefreshBitSw   uint8 = 1 // a switch (isSwitch with the home room)
	RefreshBitTbox uint8 = 2 // a chest bit on the stage's memBit (isTbox)
	RefreshBitItem uint8 = 3 // an item bit (isItem with the home room)

	RefreshRNone     uint8 = 0 // consumed at the keeper's create: ask again
	RefreshRApplied  uint8 = 1 // every check passed: the delete is queued
	RefreshRNoActor  uint8 = 2 // no actor of the pid, or one still creating
	RefreshRMismatch uint8 = 3 // proc, params, room or the re-derived bit differ
	RefreshRGate     uint8 = 4 // a gate held (Gate names it): ask again later
	RefreshRNotSet   uint8 = 5 // the bit reads clear in this game
	RefreshRRefused  uint8 = 6 // bad tag, a proc off the allow-list, or a kind never deleted
	RefreshRFpMiss   uint8 = 7 // Tbox: the Execute fingerprint missed

	RefreshGateStageChange uint8 = 1  // a stage change pending
	RefreshGateBossStage   uint8 = 2  // M_DragB, kinBOSS, SirenB or an X stage
	RefreshGateInit        uint8 = 3  // mInitState not 2
	RefreshGateEventCmd    uint8 = 4  // the actor's EvtCmd set
	RefreshGateCarry       uint8 = 5  // carried
	RefreshGateMirror      uint8 = 6  // paused, or in an enemy-mirror slot
	RefreshGateLinkDemo    uint8 = 7  // Link in a demo
	RefreshGateLinkNear    uint8 = 8  // Link within RefreshLinkFar
	RefreshGateProc        uint8 = 9  // the per-proc gate
	RefreshGateEventMode   uint8 = 10 // an event runs

	RefreshProcLeaves uint16 = 0x0092 // fpcNm_Obj_Leaves_e, "Vochi"
	RefreshProcItem   uint16 = 0x0101 // fpcNm_ITEM_e
	RefreshProcTbox   uint16 = 0x0126 // fpcNm_TBOX_e
	RefreshProcSaku   uint16 = 0x0191 // fpcNm_SAKU_e, "Ksaku", "Dsaku"
	RefreshProcStone2 uint16 = 0x01CD // fpcNm_Stone2_e, "Ebrock2"

	RefreshAcProcOff     = 0x08  // s16 mProcName
	RefreshAcPauseOff    = 0x0B  // u8 mPauseFlag
	RefreshAcInitOff     = 0x0C  // s8 mInitState
	RefreshAcInitRunning = 2     // executing
	RefreshAcEvtCmdOff   = 0xF8  // u16 eventInfo mCommand
	RefreshAcPosOff      = 0x1F8 // cXyz current.pos

	RefreshSvInfo     uint32 = 0x803C4C08 // dSv_info_c, the isSwitch/isItem this
	RefreshSvMemBit   uint32 = 0x803C5380 // the stage's dSv_memBit_c, the isTbox this
	RefreshIsSwitchFn uint32 = 0x8005DE98 // isSwitch__10dSv_info_cFii
	RefreshIsTboxFn   uint32 = 0x8005C188 // isTbox__12dSv_memBit_cFi
	RefreshIsItemFn   uint32 = 0x8005E324 // isItem__10dSv_info_cFii
	RefreshZoneSwMin         = 0xC0       // zone switch bits start here (need a room >= 0)
	RefreshEvtMode    uint32 = 0x803C9EA2 // u8 the global event mode
	RefreshLinkFar           = 300        // units

	RefreshStone2SwShift = 8     // bit = (params >> 8) & 0xFF
	RefreshStone2SwMask  = 0xFF  // the switch byte
	RefreshStone2M648Off = 0x648 // s32 m648: 0 idle (the gate), 1 lifted

	RefreshLeavesSwShift = 13   // bit = (params >> 13) & 0xFF
	RefreshLeavesSwMask  = 0xFF // the switch byte

	RefreshItemBitNoOff      = 0x630 // s32 mItemBitNo
	RefreshItemBitNoMask     = 0x7F  // mItemBitNo & this
	RefreshItemBitNone       = 0x7F  // no bit: REFUSED
	RefreshItemNoOff         = 0x63A // u8 m_itemNo
	RefreshItemNoBlueJelly   = 0x4B  // REFUSED
	RefreshItemFlagOff       = 0x669 // u8 mFlag
	RefreshItemFlagBusy      = 0x48  // boomerang | hook: GATE
	RefreshItemStatusOff     = 0x66B // u8 mItemStatus
	RefreshItemStatusWaitMax = 1     // STATUS_UNK1: the gate takes 0 (at rest) and 1 (moving), execWaitMain's
	RefreshTboxFuncMask      = 0x7F  // funcType = params & this
	RefreshTboxFuncRefuseLo  = 6     // funcTypes 6..8: REFUSED
	RefreshTboxFuncRefuseHi  = 8     // (inclusive)
	RefreshTboxBitShift      = 7     // bit = (params >> 7) & 0x1F
	RefreshTboxBitMask       = 0x1F  // the chest bit
	RefreshTboxBgOpenOff     = 0x2BC // dBgW* mpBgWOpen
	RefreshTboxBgCurOff      = 0x2C4 // dBgW* mpBgWCurrent: != open while closed
	RefreshTboxActFnOff      = 0x348 // mActionFunc's function word
	RefreshTboxFlagsOff      = 0x3F0 // u16 mFlags
	RefreshTboxFlagsBusy     = 0x38  // UNK_08, OPENING, APPEARING
	RefreshTboxTextExec      = 0x2FB0
	RefreshTboxTextOpenWait  = 0x2634
	RefreshTboxTextSwWait    = 0x27C8
	RefreshTboxTextSwWait2   = 0x28A0
	RefreshTboxFpOff0        = 0x0C // exec + this
	RefreshTboxFpWord0       = 0x4BFFFA71
	RefreshTboxFpOff1        = 0x1C // exec + this: its blr
	RefreshTboxFpWord1       = 0x4E800020

	RefreshSakuSw1Shift       = 8     // sw1 = (params >> 8) & 0xFF
	RefreshSakuSw2Shift       = 16    // sw2 = (params >> 16) & 0xFF
	RefreshSakuSwMask         = 0xFF  // each half's switch byte
	RefreshSakuHalf1Mask      = 0xF   // params & this != 0: half 1 exists
	RefreshSakuBurnOff        = 0xEF4 // u8 burning
	RefreshSakuStateOff       = 0xEF8 // s32[2]
	RefreshSakuStateStanding  = 1     // a standing half: its switch must read set
	RefreshSakuStateBurning   = 2     // GATE
	RefreshSakuStateBroken    = 3     // already broken
	RefreshSakuSwOff          = 0xF00 // u32[2] the halves' switches
	refreshResultProcStateLen = 2     // bytes 1..0 of the result
)

// RefreshAddr is the request's address.
const RefreshAddr = Mailbox + RefreshOff

// RefreshRegion is the request's 0x20 bytes: Go and the REL write it, so
// the region probe never arms it (the visuals block follows it, and
// CanaryArenaHigh starts past that).
var RefreshRegion = addr.Region{Name: "actor_refresh request", Start: RefreshAddr, End: RefreshAddr + RefreshSize}

// RefreshProcName names an allow-listed proc, "proc 0x%04X" for any other
// (which the REL refuses).
func RefreshProcName(proc uint16) string {
	switch proc {
	case RefreshProcStone2:
		return "Stone2"
	case RefreshProcLeaves:
		return "Obj_Leaves"
	case RefreshProcItem:
		return "daItem"
	case RefreshProcTbox:
		return "Tbox"
	case RefreshProcSaku:
		return "Saku"
	}
	return fmt.Sprintf("proc 0x%04X", proc)
}

// RefreshResultName names a RefreshR* result.
func RefreshResultName(r uint8) string {
	switch r {
	case RefreshRNone:
		return "none (consumed at the keeper's link)"
	case RefreshRApplied:
		return "applied"
	case RefreshRNoActor:
		return "no actor"
	case RefreshRMismatch:
		return "mismatch"
	case RefreshRGate:
		return "gated"
	case RefreshRNotSet:
		return "bit not set"
	case RefreshRRefused:
		return "refused"
	case RefreshRFpMiss:
		return "fingerprint miss"
	}
	return fmt.Sprintf("result %d", r)
}

// RefreshGateName names a RefreshGate* id.
func RefreshGateName(g uint8) string {
	switch g {
	case RefreshGateStageChange:
		return "stage change"
	case RefreshGateBossStage:
		return "boss stage"
	case RefreshGateInit:
		return "not executing"
	case RefreshGateEventCmd:
		return "actor event"
	case RefreshGateCarry:
		return "carried"
	case RefreshGateMirror:
		return "mirror slot or paused"
	case RefreshGateLinkDemo:
		return "Link demo"
	case RefreshGateLinkNear:
		return "Link near"
	case RefreshGateProc:
		return "proc gate"
	case RefreshGateEventMode:
		return "event running"
	}
	return fmt.Sprintf("gate %d", g)
}

// RefreshRequest is the Go-written half of a request.
type RefreshRequest struct {
	Seq     uint32 // never 0
	Proc    uint16 // a RefreshProc*
	PID     uint32
	Params  uint32
	Room    int8
	BitKind uint8 // RefreshBit*
	BitNo   uint16
}

// Fields are the request's two writes, in order: +0x04..+0x14 (proc, tag,
// pid, params, room, bit kind, bit no), then the seq that sets it off.
func (r RefreshRequest) Fields() (body []byte, seqBytes []byte) {
	be := binary.BigEndian
	body = make([]byte, RefreshGoSpan-RefreshProcOff)
	at := func(off int) []byte { return body[off-RefreshProcOff:] }
	be.PutUint16(at(RefreshProcOff), r.Proc)
	be.PutUint16(at(RefreshTagOff), RefreshTag)
	be.PutUint32(at(RefreshPIDOff), r.PID)
	be.PutUint32(at(RefreshParamsOff), r.Params)
	at(RefreshRoomOff)[0] = byte(r.Room)
	at(RefreshBitKindOff)[0] = r.BitKind
	be.PutUint16(at(RefreshBitNoOff), r.BitNo)
	seqBytes = make([]byte, 4)
	be.PutUint32(seqBytes, r.Seq)
	return body, seqBytes
}

// RefreshStatus is the REL-written half, with the seq Go last wrote.
type RefreshStatus struct {
	Seq       uint32 // the request's seq as read
	Ack       uint32 // the seq the REL last consumed
	Result    uint8  // RefreshR*, for Ack
	Gate      uint8  // RefreshGate* with RefreshRGate, else 0
	ProcState uint16 // the proc's state as the REL found it (wwrefresh.h)
}

// ReadRefresh reads the request's words.
func ReadRefresh(m Reader) (RefreshStatus, error) {
	b, err := m.Read(RefreshAddr, int(RefreshSize))
	if err != nil {
		return RefreshStatus{}, fmt.Errorf("mailbox: actor_refresh request: %w", err)
	}
	if len(b) != int(RefreshSize) {
		return RefreshStatus{}, fmt.Errorf("mailbox: actor_refresh request: %d bytes, want %d", len(b), RefreshSize)
	}
	be := binary.BigEndian
	return RefreshStatus{
		Seq:       be.Uint32(b[RefreshSeqOff:]),
		Ack:       be.Uint32(b[RefreshAckOff:]),
		Result:    b[RefreshResultOff],
		Gate:      b[RefreshResultOff+1],
		ProcState: be.Uint16(b[RefreshResultOff+4-refreshResultProcStateLen:]),
	}, nil
}
