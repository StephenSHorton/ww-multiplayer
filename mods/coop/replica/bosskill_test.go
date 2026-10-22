package replica

import (
	"encoding/binary"
	"fmt"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/proto"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/fakegame"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/state"
)

// drcBossItems is DRC's mDungeonItem byte in the image, and bossEnemy its
// STAGE_BOSS_ENEMY bit.
var drcBossItems = addr.OffMemBit(addr.TableDRC) + addr.MemBitOffDungeonItem

const bossEnemy = byte(1 << addr.DungeonItemStageBossEnemy)

// TestBossKillWanted: the trigger wants the kill only with the file armed,
// in game, in M_DragB with DRC loaded, a boss stage, the plan holding DRC's
// STAGE_BOSS_ENEMY under bossHold and the local save lacking it.
func TestBossKillWanted(t *testing.T) {
	heldBoss := []HeldBit{{Off: uint16(drcBossItems), Mask: bossEnemy | 1<<addr.DungeonItemStageBossDemo, Hold: bossHold}}
	base := func() *obs {
		return &obs{s: &state.Snapshot{StageName: "M_DragB", Table: addr.TableDRC}, screen: screenGame, boss: true}
	}
	cases := []struct {
		name  string
		armed bool
		edit  func(o *obs)
		held  []HeldBit
		want  bool
	}{
		{"held in M_DragB", true, nil, heldBoss, true},
		{"not armed", false, nil, heldBoss, false},
		{"nothing held", true, nil, nil, false},
		{"only the demo bit held", true, nil, []HeldBit{{Off: uint16(drcBossItems), Mask: 1 << addr.DungeonItemStageBossDemo, Hold: bossHold}}, false},
		{"held by another hold", true, nil, []HeldBit{{Off: uint16(drcBossItems), Mask: bossEnemy, Hold: Holds[0]}}, false},
		{"an X refight stage", true, func(o *obs) { o.s.StageName = "Xboss0" }, heldBoss, false},
		{"the dungeon, not the arena", true, func(o *obs) { o.s.StageName, o.boss = "M_NewD2", false }, heldBoss, false},
		{"another table loaded", true, func(o *obs) { o.s.Table = addr.TableFW }, heldBoss, false},
		{"not a boss stage", true, func(o *obs) { o.boss = false }, heldBoss, false},
		{"off the game", true, func(o *obs) { o.screen = screenOut }, heldBoss, false},
		{"the local save has the bit", true, func(o *obs) { o.raw[drcBossItems] |= bossEnemy }, heldBoss, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := base()
			if c.edit != nil {
				c.edit(o)
			}
			if got := bossKillWanted(o, c.armed, c.held); got != c.want {
				t.Fatalf("bossKillWanted = %v, want %v", got, c.want)
			}
		})
	}
}

// TestBossGuardRules: the allowlist takes the request's seq, and its proc
// and tag only as Gohma's proc and the tag, on pathBoss alone; the REL's
// ack and result are no target.
func TestBossGuardRules(t *testing.T) {
	g := NewGuardFor(AllTargets)
	judge := func(a uint32, val byte) Refusal {
		t.Helper()
		_, br, ok := g.at(a)
		if !ok {
			return RefuseTarget
		}
		if br.target != TargetBoss || br.paths != pathBoss {
			t.Fatalf("0x%08X: target %s, paths %s", a, br.target, br.paths)
		}
		r, _ := judgeByte(br, 0, val)
		return r
	}
	procAt := mailbox.BossAddr + mailbox.BossProcOff
	pt, _ := mailbox.BossRequestFields(mailbox.BossProcBtd, 1)
	for i, b := range pt {
		if r := judge(procAt+uint32(i), b); r != 0 {
			t.Fatalf("proc/tag byte %d 0x%02X refused: %s", i, b, r)
		}
	}
	other, _ := mailbox.BossRequestFields(0x00EC, 1) // Kalle Demos' proc: not the REL's
	if r := judge(procAt+1, other[1]); r != RefuseValue {
		t.Fatalf("another proc: %s", r)
	}
	if r := judge(procAt+3, 'X'); r != RefuseValue {
		t.Fatalf("another tag: %s", r)
	}
	for i := range uint32(4) {
		if r := judge(mailbox.BossAddr+mailbox.BossSeqOff+i, 0x42); r != 0 {
			t.Fatalf("seq byte %d refused: %s", i, r)
		}
	}
	if g.Allows(mailbox.BossAddr+mailbox.BossAckOff, 8) || g.Mask(mailbox.BossAddr+mailbox.BossAckOff) != 0 {
		t.Fatal("the REL's ack and result are in the allowlist")
	}
	if NewGuardFor(M3Targets|M4Targets|M5Targets|M6Targets|TargetEscape).Allows(mailbox.BossAddr, 4) {
		t.Fatal("the boss request is in a guard without TargetBoss")
	}
}

// bossRig is a recRig in M_DragB (a BOSS-type stage of DRC) with the REL
// module of this build linked and a clock the test moves.
type bossRig struct {
	*recRig
	clock *breakerClock
}

func newBossRig(t *testing.T, stage fakegame.Stage, rel bool) *bossRig {
	t.Helper()
	clock := &breakerClock{t: time.Unix(1_700_000_000, 0)}
	rr := newRecRig(t, Config{Apply: true, Now: clock.Now}, proto.FeatureImage)
	br := &bossRig{recRig: rr, clock: clock}
	rr.load(stage)
	rr.stagType(stageTypeBoss)
	if rel {
		br.linkREL(inject.RELBuildID)
	}
	rr.ticks(gate.StableTicks + 2)
	return br
}

// linkREL writes the REL module's words for a module of build id.
func (br *bossRig) linkREL(id uint32) {
	var b [8]byte
	binary.BigEndian.PutUint32(b[:], mailbox.RELMagic)
	binary.BigEndian.PutUint32(b[mailbox.OffRELBuildID-mailbox.OffRELMagic:], id)
	br.store(mailbox.Mailbox+mailbox.OffRELMagic, b[:])
}

// request reads the request's seq, proc and tag.
func (br *bossRig) request() (seq uint32, proc, tag uint16) {
	b := br.read(mailbox.BossAddr, mailbox.BossGoSpan)
	return binary.BigEndian.Uint32(b), binary.BigEndian.Uint16(b[4:]), binary.BigEndian.Uint16(b[6:])
}

// rel acks seq with result r, as bosskill.c does, with the armor broken.
func (br *bossRig) rel(seq uint32, r, act uint8) {
	br.relPhase(seq, r, act, 0, mailbox.BossBtdPhaseBare)
}

// relPhase acks seq with result r, Btd's action, state and armor phase.
func (br *bossRig) relPhase(seq uint32, r, act, state, phase uint8) {
	var b [8]byte
	binary.BigEndian.PutUint32(b[:], seq)
	b[4], b[5], b[6], b[7] = r, act, state, phase
	br.store(mailbox.BossAddr+mailbox.BossAckOff, b[:])
}

// partnerKilled is the world with the partner's Gohma dead (and its demo
// seen), and the map and compass.
func (br *bossRig) partnerKilled() {
	var img save.Image
	img[drcBossItems] = 0x2B
	br.world(img)
}

// TestBossKillRelay: the partner kills Gohma while this game is in M_DragB.
// The boss bits stay held (the hold is unchanged); the replica asks the REL
// for boss_kill, asks again while it waits on the intro and after a
// request swallowed at a relink, not while an applied kill's demo runs,
// and stops once the game has set the bit itself.
func TestBossKillRelay(t *testing.T) {
	br := newBossRig(t, drcBoss, true)
	br.partnerKilled()
	br.ticks(3)
	seq, proc, tag := br.request()
	if seq == 0 || proc != mailbox.BossProcBtd || tag != mailbox.BossTag {
		t.Fatalf("request seq %d proc 0x%X tag 0x%X; log:\n%s", seq, proc, tag, br.log)
	}
	if v := br.liveByte(drcBossItems); v&bossEnemy != 0 {
		t.Fatalf("the boss bit was written live in the arena: 0x%02X", v)
	}
	if st := br.r.Status(); !st.BossKill.Wanted || st.BossKill.Requests != 1 || st.Writes.BossKills != 1 {
		t.Fatalf("status %+v writes %+v", st.BossKill, st.Writes)
	}

	// Gohma is in its intro: waiting, asked again after bossRetry only.
	br.rel(seq, mailbox.BossRWaiting, mailbox.BossBtdActIntro)
	br.ticks(2)
	if s, _, _ := br.request(); s != seq {
		t.Fatalf("asked again (%d) before bossRetry", s)
	}
	br.clock.advance(bossRetry)
	br.ticks(1)
	s2, _, _ := br.request()
	if s2 != seq+1 {
		t.Fatalf("after waiting: seq %d, want %d; log:\n%s", s2, seq+1, br.log)
	}

	// Swallowed at a keeper relink (result none): asked again too.
	br.rel(s2, mailbox.BossRNone, 0)
	br.ticks(1)
	br.clock.advance(bossRetry)
	br.ticks(1)
	s3, _, _ := br.request()
	if s3 != s2+1 {
		t.Fatalf("after a swallowed request: seq %d, want %d", s3, s2+1)
	}

	// Applied: not asked again while the death demo runs.
	br.rel(s3, mailbox.BossRApplied, 0)
	br.ticks(1)
	br.clock.advance(10 * bossRetry)
	br.ticks(3)
	if s, _, _ := br.request(); s != s3 {
		t.Fatalf("asked again (%d) during the death demo", s)
	}
	if st := br.r.Status(); st.BossKill.Applied != 1 || st.BossKill.Result != "applied" {
		t.Fatalf("status %+v", st.BossKill)
	}

	// The game's own death demo sets the bit: the hold and the relay end.
	br.set(drcBossItems, bossEnemy)
	br.clock.advance(bossSettle)
	br.ticks(3)
	if s, _, _ := br.request(); s != s3 {
		t.Fatalf("asked again (%d) once the game has the bit", s)
	}
	if st := br.r.Status(); st.BossKill.Wanted {
		t.Fatalf("still wanted with the bit set: %+v", st.BossKill)
	}
	if !br.log.has("boss_kill") {
		t.Fatalf("no boss_kill line logged:\n%s", br.log)
	}
}

// TestBossKillNeverInRefight: an X refight stage (Btd's 'X' branch,
// d_a_btd.cpp:905-910), a BOSS-type stage of DRC's table here, never gets
// a request, however the world stands.
func TestBossKillNeverInRefight(t *testing.T) {
	br := newBossRig(t, fakegame.Stage{Name: "Xboss0", Table: addr.TableDRC, KeyHUD: true}, true)
	br.partnerKilled()
	br.ticks(5)
	if seq, _, _ := br.request(); seq != 0 {
		t.Fatalf("a request in a refight stage: seq %d", seq)
	}
	if st := br.r.Status(); st.BossKill.Wanted || st.Writes.BossKills != 0 {
		t.Fatalf("status %+v", st.BossKill)
	}
}

// TestBossKillNotWhenLocalHasIt: a game that killed its own Gohma has the
// bit; the world's adds nothing to ask for.
func TestBossKillNotWhenLocalHasIt(t *testing.T) {
	br := newBossRig(t, drcBoss, true)
	br.set(drcBossItems, bossEnemy)
	br.ticks(2)
	br.partnerKilled()
	br.ticks(5)
	if seq, _, _ := br.request(); seq != 0 {
		t.Fatalf("a request with the bit already local: seq %d", seq)
	}
}

// TestBossKillNoREL: without the REL module linked nothing is written; the
// log says why once.
func TestBossKillNoREL(t *testing.T) {
	br := newBossRig(t, drcBoss, false)
	br.partnerKilled()
	br.ticks(3)
	br.clock.advance(bossRetry)
	br.ticks(3)
	if seq, _, _ := br.request(); seq != 0 {
		t.Fatalf("a request without the REL: seq %d", seq)
	}
	if n := br.log.count("REL module is not linked"); n != 1 {
		t.Fatalf("logged %d times:\n%s", n, br.log)
	}
}

// TestBossKillWrongREL: a linked REL module of another build gets no
// request (it may lack the op); the log names both builds once.
func TestBossKillWrongREL(t *testing.T) {
	br := newBossRig(t, drcBoss, false)
	br.linkREL(inject.RELBuildID ^ 1)
	br.partnerKilled()
	br.ticks(3)
	br.clock.advance(bossRetry)
	br.ticks(3)
	if seq, _, _ := br.request(); seq != 0 {
		t.Fatalf("a request to another REL build: seq %d", seq)
	}
	if n := br.log.count(fmt.Sprintf("is build %08X, this build's is %08X", inject.RELBuildID^1, inject.RELBuildID)); n != 1 {
		t.Fatalf("logged %d times:\n%s", n, br.log)
	}
}

// TestBossKillLogOnce: a result asked again every bossRetry (an armored
// Gohma answering waiting, with its attacks turning m02E4 and mGohmaState
// over) is logged once, not once per seq; a new phase is logged again, and
// a request left unacked is logged once however often it times out.
func TestBossKillLogOnce(t *testing.T) {
	br := newBossRig(t, drcBoss, true)
	br.partnerKilled()
	br.ticks(3)
	for i := range 4 {
		seq, _, _ := br.request()
		br.relPhase(seq, mailbox.BossRWaiting, uint8(i%3), uint8(i), 0)
		br.ticks(1)
		br.clock.advance(bossRetry)
		br.ticks(1)
	}
	if n := br.log.count(": waiting ("); n != 1 {
		t.Fatalf("armored waiting logged %d times:\n%s", n, br.log)
	}
	seq, _, _ := br.request()
	br.rel(seq, mailbox.BossRWaiting, 0)
	br.ticks(1)
	if n := br.log.count(": waiting ("); n != 2 || !br.log.has("m02E0 1)") {
		t.Fatalf("the broken armor's waiting not logged:\n%s", br.log)
	}
	br.clock.advance(bossRetry)
	br.ticks(1)
	for range 3 {
		br.clock.advance(bossAckWait)
		br.ticks(1)
		br.clock.advance(bossRetry)
		br.ticks(1)
	}
	if n := br.log.count("not acked after"); n != 1 {
		t.Fatalf("not acked logged %d times:\n%s", n, br.log)
	}
}
