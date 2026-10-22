package mailbox

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// wwbossHeader is the C source of the boss_kill constants.
const wwbossHeader = "../../inject/include/wwboss.h"

// TestBossHeaderPins: every Go constant equals its WWBOSS_* define, and
// every define has a Go constant.
func TestBossHeaderPins(t *testing.T) {
	pinHeader(t, wwbossHeader, headerDefines(t, wwbossHeader), map[string]uint64{
		"WWBOSS_OFF":             uint64(BossOff),
		"WWBOSS_SIZE":            uint64(BossSize),
		"WWBOSS_SEQ_OFF":         BossSeqOff,
		"WWBOSS_PROC_OFF":        BossProcOff,
		"WWBOSS_TAG_OFF":         BossTagOff,
		"WWBOSS_ACK_OFF":         BossAckOff,
		"WWBOSS_RESULT_OFF":      BossResultOff,
		"WWBOSS_GO_SPAN":         BossGoSpan,
		"WWBOSS_TAG":             uint64(BossTag),
		"WWBOSS_PROC_BTD":        uint64(BossProcBtd),
		"WWBOSS_R_NONE":          uint64(BossRNone),
		"WWBOSS_R_APPLIED":       uint64(BossRApplied),
		"WWBOSS_R_WAITING":       uint64(BossRWaiting),
		"WWBOSS_R_ALREADY":       uint64(BossRAlready),
		"WWBOSS_R_NO_ACTOR":      uint64(BossRNoActor),
		"WWBOSS_R_GATE":          uint64(BossRGate),
		"WWBOSS_BTD_M02E4_OFF":   BossBtdM02E4Off,
		"WWBOSS_BTD_STATE_OFF":   BossBtdStateOff,
		"WWBOSS_BTD_ACT_INTRO":   BossBtdActIntro,
		"WWBOSS_BTD_ACT_END":     BossBtdActEnd,
		"WWBOSS_BTD_STATE_DEAD":  BossBtdStateDead,
		"WWBOSS_BTD_M02E0_OFF":   BossBtdM02E0Off,
		"WWBOSS_BTD_M02F6_OFF":   BossBtdM02F6Off,
		"WWBOSS_BTD_PHASE_BARE":  BossBtdPhaseBare,
		"WWBOSS_BTD_ROCK_LANDED": BossBtdRockLand,
	})
}

// TestBossRequestPlace: the request fills the gap between the enemy region
// and the remote-Link block, inside RemoteBlockRegion (which the canary
// probe skips) and outside both canary arenas.
func TestBossRequestPlace(t *testing.T) {
	if BossOff != EnemyRegionEnd || BossOff+BossSize != RemoteOff || BossOff < RemoteRegionStart {
		t.Fatalf("boss_kill request +0x%X..+0x%X, want the gap +0x%X..+0x%X", BossOff, BossOff+BossSize, EnemyRegionEnd, RemoteOff)
	}
	r := BossRequestRegion
	if r.Start < RemoteBlockRegion.Start || r.End > RemoteBlockRegion.End {
		t.Fatalf("boss_kill request %#x..%#x is outside %+v", r.Start, r.End, RemoteBlockRegion)
	}
	for _, c := range []struct{ s, e uint32 }{{CanaryArena.Start, CanaryArena.End}, {CanaryArenaHigh.Start, CanaryArenaHigh.End}, {CanaryTRK.Start, CanaryTRK.End}} {
		if r.Start < c.e && c.s < r.End {
			t.Fatalf("boss_kill request %#x..%#x overlaps a canary arena %#x..%#x", r.Start, r.End, c.s, c.e)
		}
	}
	if BossAckOff != BossGoSpan || BossResultOff+4 != int(BossSize) {
		t.Fatal("the Go span must end where the REL's ack starts, and the result end the request")
	}
}

// TestBossReadAndFields: ReadBoss decodes the REL's half; the request
// fields are the proc and tag, then the seq.
func TestBossReadAndFields(t *testing.T) {
	m := mem.NewFakeMem()
	pt, sq := BossRequestFields(BossProcBtd, 7)
	if err := m.Write(BossAddr+BossProcOff, pt); err != nil {
		t.Fatal(err)
	}
	if err := m.Write(BossAddr+BossSeqOff, sq); err != nil {
		t.Fatal(err)
	}
	if err := m.Write(BossAddr+BossAckOff, []byte{0, 0, 0, 7, BossRWaiting, BossBtdActIntro, 0x01, BossBtdPhaseBare}); err != nil {
		t.Fatal(err)
	}
	st, err := ReadBoss(m)
	if err != nil {
		t.Fatal(err)
	}
	if st != (BossStatus{Seq: 7, Ack: 7, Result: BossRWaiting, Act: BossBtdActIntro, State: 1, Phase: BossBtdPhaseBare}) {
		t.Fatalf("ReadBoss = %+v", st)
	}
	if b, _ := m.Read(BossAddr+BossProcOff, 4); b[0] != 0x00 || b[1] != 0xEB || b[2] != 'B' || b[3] != 'K' {
		t.Fatalf("proc and tag bytes % X", b)
	}
}
