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
// (B5, revised)".
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
	BossResultOff = 0x0C // u32 BE: byte +0 BossR*, +1 m02E4's low byte, +2 mGohmaState's low byte, +3 m02E0 (REL)
	BossGoSpan    = 0x08 // bytes Go writes: seq, proc, tag

	BossTag     uint16 = 0x424B // 'BK'
	BossProcBtd uint16 = 0x00EB // fpcNm_BTD_e, Gohma

	BossRNone    uint8 = 0 // consumed at the keeper's create: ask again
	BossRApplied uint8 = 1 // m02E4 0..2 with the armor broken: the death written
	BossRWaiting uint8 = 2 // m02E4 0xA (the intro), the armor intact or another state: ask again
	BossRAlready uint8 = 3 // m02E4 0xB: dying or dead
	BossRNoActor uint8 = 4 // no Btd, or one still creating
	BossRGate    uint8 = 5 // a stage change pending, not M_DragB, or a bad proc or tag

	BossBtdM02E0Off  = 0x2E0 // u8 Btd's armor phase (d_a_btd.h:41)
	BossBtdM02E4Off  = 0x2E4 // s16 Btd's action (d_a_btd.h:44)
	BossBtdStateOff  = 0x2E6 // s16 mGohmaState (d_a_btd.h:45)
	BossBtdActIntro  = 0xA   // startdemo (d_a_btd.cpp:1758-1760)
	BossBtdActEnd    = 0xB   // end (d_a_btd.cpp:1762-1764)
	BossBtdStateDead = 0x32  // end's first case (d_a_btd.cpp:905)
	BossBtdM02F6Off  = 0x2F6 // u8 Dr2's rock handshake (d_a_btd.h:54)
	BossBtdPhaseBare = 1     // m02E0 with the armor broken (d_a_btd.cpp:833)
	BossBtdRockLand  = 1     // m02F6 for a landed rock not yet taken (d_a_dr2.cpp:311)
)

// BossAddr is the request's address.
const BossAddr = Mailbox + BossOff

// BossRequestRegion is the request's 16 bytes.
var BossRequestRegion = addr.Region{Name: "boss_kill request", Start: BossAddr, End: BossAddr + BossSize}

// BossProcName names a request's proc. The REL takes only BossProcBtd,
// and only in M_DragB; any other proc is refused (BossRGate).
func BossProcName(proc uint16) string {
	if proc == BossProcBtd {
		return "Gohma (Btd)"
	}
	return fmt.Sprintf("proc 0x%04X", proc)
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
// wrote.
type BossStatus struct {
	Seq    uint32 // the request's seq as read
	Ack    uint32 // the seq the REL last consumed
	Result uint8  // BossR*, for Ack
	Act    uint8  // Btd's m02E4 low byte as the REL found it
	State  uint8  // its mGohmaState low byte
	Phase  uint8  // its m02E0 (0 armored, 1 armor broken, 2 dying), 0 with no Btd
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
