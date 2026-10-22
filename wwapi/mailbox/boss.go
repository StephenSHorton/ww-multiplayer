package mailbox

import (
	"encoding/binary"
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// The boss_kill request in the mailbox slack (inject/include/wwboss.h,
// whose WWBOSS_* defines these mirror; boss_test.go pins each one to the
// header). Co-op's replica (mods/coop/replica bosskill.go) writes it when
// the partner has killed this game's boss; the REL module (inject/rel/
// bosskill.c) gives the boss its own lethal-hit death and acks. 16 bytes
// at Mailbox+BossOff, the alignment gap at the head of RemoteBlockRegion
// that remote-Link never writes, so the canary probe skips it too. Design:
// ww-multiplayer-handoff/design/research/C3-C6-gohma.md, "The relay op
// (B5, revised)"; Kalle Demos: research/C1-kalle-demos-relay.md.
//
// Go writes the proc and tag, then the seq (two writes, the seq last); the
// REL writes the result, then the ack. The keeper's create consumes any
// seq the slack holds (ack = seq, result BossRNone), so a stale request is
// never run and one that raced a relink comes back as BossRNone.
const (
	BossOff  uint32 = 0x510 // request start, from Mailbox (0x80413410)
	BossSize uint32 = 0x10  // ends at RemoteOff

	BossSeqOff    = 0x00 // u32 request seq, never 0 (Go)
	BossProcOff   = 0x04 // u16 the boss's proc (Go)
	BossTagOff    = 0x06 // u16 BossTag (Go)
	BossAckOff    = 0x08 // u32 the seq last consumed (REL)
	BossResultOff = 0x0C // u32 BE: byte +0 BossR*, +1..+3 per proc: Btd m02E4, mGohmaState, m02E0; Bmd mMode, m302, m304 (REL)
	BossGoSpan    = 0x08 // bytes Go writes: seq, proc, tag

	BossTag     uint16 = 0x424B // 'BK'
	BossProcBtd uint16 = 0x00EB // fpcNm_BTD_e, Gohma, only in M_DragB
	BossProcBmd uint16 = 0x00EC // fpcNm_BMD_e, Kalle Demos, only in kinBOSS

	BossRNone    uint8 = 0 // consumed at the keeper's create: ask again
	BossRApplied uint8 = 1 // Btd: m02E4 0..2 with the armor broken; Bmd: the open flower; the death written
	BossRWaiting uint8 = 2 // Btd: the intro, the armor intact or another state; Bmd: not the open flower; ask again
	BossRAlready uint8 = 3 // m02E4 (Btd) or mMode (Bmd) 0xB: dying or dead
	BossRNoActor uint8 = 4 // no actor of the proc, or one still creating
	BossRGate    uint8 = 5 // a stage change pending, not the proc's stage, or a bad proc or tag

	BossBtdM02E0Off  = 0x2E0 // u8 Btd's armor phase (d_a_btd.h:41)
	BossBtdM02E4Off  = 0x2E4 // s16 Btd's action (d_a_btd.h:44)
	BossBtdStateOff  = 0x2E6 // s16 mGohmaState (d_a_btd.h:45)
	BossBtdActIntro  = 0xA   // startdemo (d_a_btd.cpp:1758-1760)
	BossBtdActEnd    = 0xB   // end (d_a_btd.cpp:1762-1764)
	BossBtdStateDead = 0x32  // end's first case (d_a_btd.cpp:905)
	BossBtdM02F6Off  = 0x2F6 // u8 Dr2's rock handshake (d_a_btd.h:54)
	BossBtdPhaseBare = 1     // m02E0 with the armor broken (d_a_btd.cpp:833)
	BossBtdRockLand  = 1     // m02F6 for a landed rock not yet taken (d_a_dr2.cpp:311)

	// Bmd (d_a_bmd.h), Kalle Demos: the gate is mMode 3 at m302 2..5
	// with m304 not 0, m310 0 and mB74 0; the applied writes are core_damage_check's
	// lethal block (d_a_bmd.cpp:609-617).
	BossBmdModeOff    = 0x300 // s16 mMode
	BossBmdM302Off    = 0x302 // s16 m302, the mode's step
	BossBmdM304Off    = 0x304 // s16 m304, nonzero with the core out
	BossBmdM306Off    = 0x306 // s16 m306, the head state
	BossBmdM310Off    = 0x310 // s16 m310, core_damage_check shut while nonzero
	BossBmdMB74Off    = 0xB74 // s16 mB74, the demo camera
	BossBmdMBDCOff    = 0xBDC // f32 mBDC, the light fade (written 1.0f)
	BossBmdMBE0Off    = 0xBE0 // u8 mBE0, the light fade's state
	BossBmdModeFallen = 3     // damage(): the flower down
	BossBmdStepLanded = 2     // damage()'s first m302 after the landing
	BossBmdModeIntro  = 10    // start(), the intro
	BossBmdModeEnd    = 0xB   // end()
	BossBmdM306Dead   = 100   // the head death
	BossBmdMB74Death  = 100   // the death camera's event
	BossBmdM310Dead   = 20000 // core_damage_check shut
	BossBmdMBE0Dead   = 2     // the light fade's death case

	// mDoAud_bgmStop(30), which the REL calls after a Bmd apply.
	BossBgmStopFn   uint32 = 0x802A4658 // bgmStop__11JAIZelBasicFUll
	BossZelBasic    uint32 = 0x803F7710 // zel_basic__11JAIZelBasic
	BossBgmStopFade        = 30
)

// BossAddr is the request's address.
const BossAddr = Mailbox + BossOff

// BossRequestRegion is the request's 16 bytes.
var BossRequestRegion = addr.Region{Name: "boss_kill request", Start: BossAddr, End: BossAddr + BossSize}

// BossProcName names a request's proc. The REL takes BossProcBtd only in
// M_DragB and BossProcBmd only in kinBOSS; any other proc is refused
// (BossRGate).
func BossProcName(proc uint16) string {
	switch proc {
	case BossProcBtd:
		return "Gohma (Btd)"
	case BossProcBmd:
		return "Kalle Demos (Bmd)"
	}
	return fmt.Sprintf("proc 0x%04X", proc)
}

// BossDetail is a result's bytes after BossR* for a request of proc, as
// the REL found them: Btd's m02E4, mGohmaState and m02E0, Bmd's mMode,
// m302 and m304 (each its low byte).
func BossDetail(proc uint16, st BossStatus) string {
	if proc == BossProcBmd {
		return fmt.Sprintf("mMode 0x%X, m302 %d, m304 %d", st.Act, int8(st.State), int8(st.Phase))
	}
	return fmt.Sprintf("m02E4 0x%X, state 0x%X, m02E0 %d", st.Act, st.State, st.Phase)
}

// BossResultName names a BossR* result.
func BossResultName(r uint8) string {
	switch r {
	case BossRNone:
		return "none (consumed at the keeper's link)"
	case BossRApplied:
		return "applied"
	case BossRWaiting:
		return "waiting"
	case BossRAlready:
		return "already dying"
	case BossRNoActor:
		return "no actor"
	case BossRGate:
		return "gated"
	}
	return fmt.Sprintf("result %d", r)
}

// BossStatus is the REL-written half of the request, with the seq Go last
// wrote. Act, State and Phase are per proc (BossDetail): Btd's m02E4,
// mGohmaState and m02E0, Bmd's mMode, m302 and m304.
type BossStatus struct {
	Seq    uint32 // the request's seq as read
	Ack    uint32 // the seq the REL last consumed
	Result uint8  // BossR*, for Ack
	Act    uint8  // Btd's m02E4 or Bmd's mMode, low byte, as the REL found it
	State  uint8  // Btd's mGohmaState or Bmd's m302, low byte
	Phase  uint8  // Btd's m02E0 (0 armored, 1 armor broken, 2 dying) or Bmd's m304, 0 with no actor
}

// ReadBoss reads the request's words.
func ReadBoss(m Reader) (BossStatus, error) {
	b, err := m.Read(BossAddr, int(BossSize))
	if err != nil {
		return BossStatus{}, fmt.Errorf("mailbox: boss_kill request: %w", err)
	}
	if len(b) != int(BossSize) {
		return BossStatus{}, fmt.Errorf("mailbox: boss_kill request: %d bytes, want %d", len(b), BossSize)
	}
	be := binary.BigEndian
	return BossStatus{
		Seq:    be.Uint32(b[BossSeqOff:]),
		Ack:    be.Uint32(b[BossAckOff:]),
		Result: b[BossResultOff],
		Act:    b[BossResultOff+1],
		State:  b[BossResultOff+2],
		Phase:  b[BossResultOff+3],
	}, nil
}

// BossRequestFields are a request's two writes, in order: the proc and
// tag, then the seq that sets it off.
func BossRequestFields(proc uint16, seq uint32) (procTag []byte, seqBytes []byte) {
	procTag = make([]byte, 4)
	binary.BigEndian.PutUint16(procTag, proc)
	binary.BigEndian.PutUint16(procTag[2:], BossTag)
	seqBytes = make([]byte, 4)
	binary.BigEndian.PutUint32(seqBytes, seq)
	return procTag, seqBytes
}
