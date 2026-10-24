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
			got := bossKillWanted(o, c.armed, c.held)
			if (got != nil) != c.want || (got != nil && got.proc != mailbox.BossProcBtd) {
				t.Fatalf("bossKillWanted = %+v, want %v", got, c.want)
			}
		})
	}
}

// fwBossItems is Forbidden Woods' mDungeonItem byte in the image.
var fwBossItems = addr.OffMemBit(addr.TableFW) + addr.MemBitOffDungeonItem

// fwBoss is the Kalle Demos arena, a BOSS-type stage of FW's table.
var fwBoss = fakegame.Stage{Name: "kinBOSS", Table: addr.TableFW, KeyHUD: true}

// TestBossKillWantedRows: each row fires in its own arena with its own
// table's bit held, and asks for its own proc; neither fires with the
// other's table or bit, in an X stage, or once the local save has the
// bit.
func TestBossKillWantedRows(t *testing.T) {
	held := func(off int) []HeldBit {
		return []HeldBit{{Off: uint16(off), Mask: bossEnemy, Hold: bossHold}}
	}
	cases := []struct {
		name  string
		stage string
		table int8
		held  []HeldBit
		local int // an image byte with the bit set locally, -1 for none
		want  uint16
	}{
		{"Gohma", "M_DragB", addr.TableDRC, held(drcBossItems), -1, mailbox.BossProcBtd},
		{"Kalle Demos", "kinBOSS", addr.TableFW, held(fwBossItems), -1, mailbox.BossProcBmd},
		{"kinBOSS with DRC's bit held", "kinBOSS", addr.TableFW, held(drcBossItems), -1, 0},
		{"M_DragB with FW's bit held", "M_DragB", addr.TableDRC, held(fwBossItems), -1, 0},
		{"kinBOSS with DRC's table", "kinBOSS", addr.TableDRC, held(fwBossItems), -1, 0},
		{"kinBOSS, the local bit set", "kinBOSS", addr.TableFW, held(fwBossItems), fwBossItems, 0},
		{"M_DragB, the local bit set", "M_DragB", addr.TableDRC, held(drcBossItems), drcBossItems, 0},
		{"an X stage on FW's table", "Xboss1", addr.TableFW, held(fwBossItems), -1, 0},
		{"an X stage on DRC's table", "Xboss0", addr.TableDRC, held(drcBossItems), -1, 0},
		{"the FW dungeon", "kindan", addr.TableFW, held(fwBossItems), -1, 0},
		{"the Mothula room", "kinMB", addr.TableFW, held(fwBossItems), -1, 0},
		{"Gohdan", "SirenB", addr.TableTotG, held(totgBossItems), -1, mailbox.BossProcBst},
		{"SirenB with DRC's bit held", "SirenB", addr.TableTotG, held(drcBossItems), -1, 0},
		{"SirenB with FW's table", "SirenB", addr.TableFW, held(fwBossItems), -1, 0},
		{"SirenB, the local bit set", "SirenB", addr.TableTotG, held(totgBossItems), totgBossItems, 0},
		{"the TotG dungeon", "Siren", addr.TableTotG, held(totgBossItems), -1, 0},
		{"an X stage on TotG's table", "Xboss2", addr.TableTotG, held(totgBossItems), -1, 0},
		{"M_DragB with TotG's bit held", "M_DragB", addr.TableDRC, held(totgBossItems), -1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := &obs{s: &state.Snapshot{StageName: c.stage, Table: c.table}, screen: screenGame, boss: true}
			if c.local >= 0 {
				o.raw[c.local] |= bossEnemy
			}
			got := bossKillWanted(o, true, c.held)
			switch {
			case c.want == 0 && got != nil:
				t.Fatalf("bossKillWanted = %+v, want none", got)
			case c.want != 0 && (got == nil || got.proc != c.want || got.stage != c.stage || got.table != c.table):
				t.Fatalf("bossKillWanted = %+v, want proc 0x%04X", got, c.want)
			}
		})
	}
	for _, row := range bossKillRows {
		if row.stage[0] == 'X' {
			t.Fatalf("an X refight stage in bossKillRows: %+v", row)
		}
	}
}

// TestBossGuardRules: the allowlist takes the request's seq, and its proc
// and tag only as a bossKillRows proc (Gohma's, Kalle Demos') and the tag,
// on pathBoss alone; the REL's ack and result are no target.
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
	bmd, _ := mailbox.BossRequestFields(mailbox.BossProcBmd, 1)
	for i, b := range bmd {
		if r := judge(procAt+uint32(i), b); r != 0 {
			t.Fatalf("Kalle Demos' proc/tag byte %d 0x%02X refused: %s", i, b, r)
		}
	}
	// Every proc byte value: taken exactly when a row's proc has it there.
	for i := range 2 {
		for v := range 256 {
			want := false
			for _, row := range bossKillRows {
				var p [2]byte
				binary.BigEndian.PutUint16(p[:], row.proc)
				want = want || p[i] == byte(v)
			}
			if r := judge(procAt+uint32(i), byte(v)); (r == 0) != want {
				t.Fatalf("proc byte %d value 0x%02X: refusal %v, want taken %v", i, v, r, want)
			}
		}
	}
	bst, _ := mailbox.BossRequestFields(mailbox.BossProcBst, 1)
	for i, b := range bst {
		if r := judge(procAt+uint32(i), b); r != 0 {
			t.Fatalf("Gohdan's proc/tag byte %d 0x%02X refused: %s", i, b, r)
		}
	}
	// Between Bmd (0xEC) and Bst (0xF2): BMDHAND, BMDFOOT, Helmaroc King
	// and the rest are no value of the proc's low byte.
	for v := 0xED; v < 0xF2; v++ {
		if r := judge(procAt+1, byte(v)); r != RefuseValue {
			t.Fatalf("proc low byte 0x%02X: %v, want a value refusal", v, r)
		}
	}
	// Gohdan's proc byte is the boss path's alone: the refresh request's
	// bytes take none of the boss rows' procs (its proc is the allow-list's).
	if _, br, ok := g.at(mailbox.RefreshAddr + mailbox.RefreshProcOff + 1); !ok || br.paths != pathRefresh {
		t.Fatalf("the refresh proc byte: ok %v paths %s", ok, br.paths)
	} else if r, _ := judgeByte(br, 0, 0xF2); r != RefuseValue {
		t.Fatalf("0xF2 in the refresh request's proc: %v", r)
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
	return newBossRigSet(t, stage, rel, "")
}

// newBossRigSet is newBossRig with --coop-boss-kill set to rows.
func newBossRigSet(t *testing.T, stage fakegame.Stage, rel bool, rows string) *bossRig {
	t.Helper()
	clock := &breakerClock{t: time.Unix(1_700_000_000, 0)}
	rr := newRecRig(t, Config{Apply: true, Now: clock.Now, BossKill: rows}, proto.FeatureImage)
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

// partnerKilledFW is the world with the partner's Kalle Demos dead (and
// its demo seen), and the map and compass.
func (br *bossRig) partnerKilledFW() {
	var img save.Image
	img[fwBossItems] = 0x2B
	br.world(img)
}

// TestBossKillRelayBmd: the partner kills Kalle Demos while this game is
// in kinBOSS. The request names Bmd's proc; the REL's waiting answers
// (the intro, the vines up, the flower falling) are asked again after
// bossRetry and logged with Bmd's fields, the vines-up modes once; an
// applied kill is not asked again while end() runs, and the relay stops
// once the game has set FW's bit itself.
func TestBossKillRelayBmd(t *testing.T) {
	br := newBossRig(t, fwBoss, true)
	br.partnerKilledFW()
	br.ticks(3)
	seq, proc, tag := br.request()
	if seq == 0 || proc != mailbox.BossProcBmd || tag != mailbox.BossTag {
		t.Fatalf("request seq %d proc 0x%X tag 0x%X; log:\n%s", seq, proc, tag, br.log)
	}
	if v := br.liveByte(fwBossItems); v&bossEnemy != 0 {
		t.Fatalf("the boss bit was written live in the arena: 0x%02X", v)
	}
	if st := br.r.Status(); !st.BossKill.Wanted || st.BossKill.Boss != "Kalle Demos" || st.BossKill.Requests != 1 {
		t.Fatalf("status %+v", st.BossKill)
	}
	if !br.log.has("the partner killed Kalle Demos") || !br.log.has("once its flower is down and open") {
		t.Fatalf("no Kalle Demos line:\n%s", br.log)
	}

	// The intro, then the vines up (modes 0..2 turning over), then the
	// flower falling (mode 3, m304 0): each waiting, asked again.
	answers := []struct{ mode, step, core uint8 }{
		{mailbox.BossBmdModeIntro, 0, 1},
		{0, 0xFF, 0}, {1, 2, 0}, {2, 1, 0}, {0, 3, 0},
		{mailbox.BossBmdModeFallen, 1, 0},
	}
	for _, a := range answers {
		s, _, _ := br.request()
		br.relPhase(s, mailbox.BossRWaiting, a.mode, a.step, a.core)
		br.ticks(1)
		if n, _, _ := br.request(); n != s {
			t.Fatalf("asked again (%d) before bossRetry", n)
		}
		br.clock.advance(bossRetry)
		br.ticks(1)
		if n, _, _ := br.request(); n != s+1 {
			t.Fatalf("after waiting in mode %d: seq %d, want %d; log:\n%s", a.mode, n, s+1, br.log)
		}
	}
	if n := br.log.count("waiting (mMode 0x"); n != 3 {
		t.Fatalf("waiting logged %d times, want 3 (intro, vines, falling):\n%s", n, br.log)
	}
	if !br.log.has("waiting (mMode 0xA, m302 0, m304 1)") || !br.log.has("waiting (mMode 0x3, m302 1, m304 0)") {
		t.Fatalf("Bmd's fields not logged:\n%s", br.log)
	}

	// Applied at the open flower: not asked again while end() runs.
	s, _, _ := br.request()
	br.relPhase(s, mailbox.BossRApplied, mailbox.BossBmdModeFallen, 3, 1)
	br.ticks(1)
	br.clock.advance(10 * bossRetry)
	br.ticks(3)
	if n, _, _ := br.request(); n != s {
		t.Fatalf("asked again (%d) while end() runs", n)
	}
	if st := br.r.Status(); st.BossKill.Applied != 1 || st.BossKill.Result != "applied" {
		t.Fatalf("status %+v", st.BossKill)
	}
	if !br.log.has("applied (mMode 0x3, m302 3, m304 1)") {
		t.Fatalf("no applied line:\n%s", br.log)
	}

	// end() sets FW's bit: the relay ends.
	br.set(fwBossItems, bossEnemy)
	br.clock.advance(bossSettle)
	br.ticks(3)
	if n, _, _ := br.request(); n != s {
		t.Fatalf("asked again (%d) once the game has the bit", n)
	}
	if st := br.r.Status(); st.BossKill.Wanted {
		t.Fatalf("still wanted with the bit set: %+v", st.BossKill)
	}
}

// TestBossKillBmdNeverInRefight: an X stage on FW's table never gets a
// Kalle Demos request.
func TestBossKillBmdNeverInRefight(t *testing.T) {
	br := newBossRig(t, fakegame.Stage{Name: "Xboss1", Table: addr.TableFW, KeyHUD: true}, true)
	br.partnerKilledFW()
	br.ticks(5)
	if seq, _, _ := br.request(); seq != 0 {
		t.Fatalf("a request in a refight stage: seq %d", seq)
	}
}

// TestBossKillBmdNotWhenLocalHasIt: a game that killed its own Kalle
// Demos has FW's bit; the world's adds nothing to ask for.
func TestBossKillBmdNotWhenLocalHasIt(t *testing.T) {
	br := newBossRig(t, fwBoss, true)
	br.set(fwBossItems, bossEnemy)
	br.ticks(2)
	br.partnerKilledFW()
	br.ticks(5)
	if seq, _, _ := br.request(); seq != 0 {
		t.Fatalf("a request with the bit already local: seq %d", seq)
	}
}

// TestBossKillBmdNoREL: without the REL module the Kalle Demos relay
// writes nothing and names its boss once.
func TestBossKillBmdNoREL(t *testing.T) {
	br := newBossRig(t, fwBoss, false)
	br.partnerKilledFW()
	br.ticks(3)
	br.clock.advance(bossRetry)
	br.ticks(3)
	if seq, _, _ := br.request(); seq != 0 {
		t.Fatalf("a request without the REL: seq %d", seq)
	}
	if n := br.log.count("the arena keeps Kalle Demos until it is"); n != 1 {
		t.Fatalf("logged %d times:\n%s", n, br.log)
	}
}

// totgBossItems is Tower of the Gods' mDungeonItem byte in the image.
var totgBossItems = addr.OffMemBit(addr.TableTotG) + addr.MemBitOffDungeonItem

// totgBoss is the Gohdan arena, a BOSS-type stage of TotG's table.
var totgBoss = fakegame.Stage{Name: "SirenB", Table: addr.TableTotG, KeyHUD: true}

// gohdanRows is --coop-boss-kill with Gohdan named.
const gohdanRows = "gohma,kalle,gohdan"

// partnerKilledTotG is the world with the partner's Gohdan dead (and its
// demo seen), and the map and compass.
func (br *bossRig) partnerKilledTotG() {
	var img save.Image
	img[totgBossItems] = 0x2B
	br.world(img)
}

// TestParseBossKill: the row set's tokens, the default and none.
func TestParseBossKill(t *testing.T) {
	for _, c := range []struct {
		v    string
		want []uint16
		err  bool
	}{
		{"", []uint16{mailbox.BossProcBtd, mailbox.BossProcBmd}, false},
		{BossKillDefault, []uint16{mailbox.BossProcBtd, mailbox.BossProcBmd}, false},
		{"gohma, KALLE ,gohdan", []uint16{mailbox.BossProcBtd, mailbox.BossProcBmd, mailbox.BossProcBst}, false},
		{"gohdan", []uint16{mailbox.BossProcBst}, false},
		{"none", nil, false},
		{"gohma,helmaroc", nil, true},
	} {
		on, err := ParseBossKill(c.v)
		if (err != nil) != c.err {
			t.Fatalf("%q: %v", c.v, err)
		}
		if c.err {
			continue
		}
		if len(on) != len(c.want) {
			t.Fatalf("%q: %v, want %v", c.v, on, c.want)
		}
		for _, p := range c.want {
			if !on[p] {
				t.Fatalf("%q: %v lacks 0x%04X", c.v, on, p)
			}
		}
	}
	if _, err := New(Config{Mem: newRecRig(t, Config{}, proto.FeatureImage).m, BossKill: "ganon"}); err == nil {
		t.Fatal("New took an unknown --coop-boss-kill row")
	}
}

// TestBossKillRelayBst: with Gohdan in the row set, the partner kills it
// while this game is in SirenB. The request names Bst's proc; the REL's
// waiting answers (the intro, the hands' fight with its actions turning
// over, the eye phase, the mouth opening) are asked again after bossRetry
// and logged with Bst's fields, the fight's actions once; an applied kill
// is not asked again while the demo runs, and the relay stops once the
// game has set TotG's bit itself.
func TestBossKillRelayBst(t *testing.T) {
	br := newBossRigSet(t, totgBoss, true, gohdanRows)
	br.partnerKilledTotG()
	br.ticks(3)
	seq, proc, tag := br.request()
	if seq == 0 || proc != mailbox.BossProcBst || tag != mailbox.BossTag {
		t.Fatalf("request seq %d proc 0x%X tag 0x%X; log:\n%s", seq, proc, tag, br.log)
	}
	if v := br.liveByte(totgBossItems); v&bossEnemy != 0 {
		t.Fatalf("the boss bit was written live in the arena: 0x%02X", v)
	}
	if st := br.r.Status(); !st.BossKill.Wanted || st.BossKill.Boss != "Gohdan" || st.BossKill.Requests != 1 {
		t.Fatalf("status %+v", st.BossKill)
	}
	if !br.log.has("the partner killed Gohdan") || !br.log.has("once its mouth is open for a bomb") {
		t.Fatalf("no Gohdan line:\n%s", br.log)
	}
	// act, mDamage, m2E7C: the hands' fight (STAY, FLY and the attacks
	// 10..14 as one), the eye phase (12, the head still flying), the
	// mouth opening (HEAD_DAMAGE, mDamage 2).
	answers := []struct{ act, dmg, phase uint8 }{
		{0, 0, 1}, {1, 0, 1}, {11, 0, 1}, {14, 0, 1},
		{1, 0, mailbox.BossBstM2E7CEyes},
		{mailbox.BossBstActHeadDmg, 2, mailbox.BossBstM2E7CEyes},
	}
	for _, a := range answers {
		s, _, _ := br.request()
		br.relPhase(s, mailbox.BossRWaiting, a.act, a.dmg, a.phase)
		br.ticks(1)
		if n, _, _ := br.request(); n != s {
			t.Fatalf("asked again (%d) before bossRetry", n)
		}
		br.clock.advance(bossRetry)
		br.ticks(1)
		if n, _, _ := br.request(); n != s+1 {
			t.Fatalf("after waiting at act %d: seq %d, want %d; log:\n%s", a.act, n, s+1, br.log)
		}
	}
	if n := br.log.count(": waiting (action 0x"); n != 3 {
		t.Fatalf("waiting logged %d times, want 3 (fight, eye phase, mouth opening):\n%s", n, br.log)
	}
	if !br.log.has("waiting (action 0x7, mDamage 2, m2E7C 12)") {
		t.Fatalf("Bst's fields not logged:\n%s", br.log)
	}

	// Applied at the open mouth: not asked again while the demo runs.
	s, _, _ := br.request()
	br.relPhase(s, mailbox.BossRApplied, mailbox.BossBstActHeadDmg, mailbox.BossBstDmgMouth, mailbox.BossBstM2E7CEyes)
	br.ticks(1)
	br.clock.advance(10 * bossRetry)
	br.ticks(3)
	if n, _, _ := br.request(); n != s {
		t.Fatalf("asked again (%d) during the demo", n)
	}
	if st := br.r.Status(); st.BossKill.Applied != 1 || st.BossKill.Result != "applied" {
		t.Fatalf("status %+v", st.BossKill)
	}
	if !br.log.has("applied (action 0x7, mDamage 3, m2E7C 12)") {
		t.Fatalf("no applied line:\n%s", br.log)
	}

	// The demo sets TotG's bit: the relay ends.
	br.set(totgBossItems, bossEnemy)
	br.clock.advance(bossSettle)
	br.ticks(3)
	if n, _, _ := br.request(); n != s {
		t.Fatalf("asked again (%d) once the game has the bit", n)
	}
	if st := br.r.Status(); st.BossKill.Wanted {
		t.Fatalf("still wanted with the bit set: %+v", st.BossKill)
	}
}

// TestBossKillBstDefaultOff: with the default row set (gohma,kalle),
// SirenB gets no request; the log says once why the arena keeps Gohdan.
func TestBossKillBstDefaultOff(t *testing.T) {
	br := newBossRig(t, totgBoss, true)
	br.partnerKilledTotG()
	br.ticks(3)
	br.clock.advance(bossRetry)
	br.ticks(3)
	if seq, _, _ := br.request(); seq != 0 {
		t.Fatalf("a Gohdan request without the row: seq %d", seq)
	}
	if st := br.r.Status(); st.BossKill.Wanted || st.Writes.BossKills != 0 {
		t.Fatalf("status %+v", st.BossKill)
	}
	if n := br.log.count("--coop-boss-kill (gohma,kalle) leaves it out"); n != 1 {
		t.Fatalf("logged %d times:\n%s", n, br.log)
	}
	// The other rows stay on: Gohma is still asked for.
	g := newBossRig(t, drcBoss, true)
	g.partnerKilled()
	g.ticks(3)
	if seq, proc, _ := g.request(); seq == 0 || proc != mailbox.BossProcBtd {
		t.Fatalf("Gohma not asked for by default: seq %d proc 0x%X", seq, proc)
	}
}

// TestBossKillBstNeverOutsideSirenB: with Gohdan named, no other stage of
// TotG's table (the tower, an X refight stage) gets a Bst request, and
// --coop-boss-kill=gohdan alone leaves Gohma's arena alone.
func TestBossKillBstNeverOutsideSirenB(t *testing.T) {
	for _, st := range []fakegame.Stage{
		{Name: "Siren", Table: addr.TableTotG, KeyHUD: true},
		{Name: "Xboss2", Table: addr.TableTotG, KeyHUD: true},
	} {
		br := newBossRigSet(t, st, true, gohdanRows)
		br.partnerKilledTotG()
		br.ticks(5)
		if seq, _, _ := br.request(); seq != 0 {
			t.Fatalf("%s: a request: seq %d", st.Name, seq)
		}
	}
	br := newBossRigSet(t, drcBoss, true, "gohdan")
	br.partnerKilled()
	br.ticks(5)
	if seq, _, _ := br.request(); seq != 0 {
		t.Fatalf("Gohma asked for with --coop-boss-kill=gohdan: seq %d", seq)
	}
}

// TestBossKillBstNoREL: without the REL module the Gohdan relay writes
// nothing and names its boss once; a REL of another build likewise.
func TestBossKillBstNoREL(t *testing.T) {
	br := newBossRigSet(t, totgBoss, false, gohdanRows)
	br.partnerKilledTotG()
	br.ticks(3)
	br.clock.advance(bossRetry)
	br.ticks(3)
	if seq, _, _ := br.request(); seq != 0 {
		t.Fatalf("a request without the REL: seq %d", seq)
	}
	if n := br.log.count("the arena keeps Gohdan until it is"); n != 1 {
		t.Fatalf("logged %d times:\n%s", n, br.log)
	}
	br.linkREL(inject.RELBuildID ^ 1)
	br.clock.advance(bossRetry)
	br.ticks(3)
	if seq, _, _ := br.request(); seq != 0 {
		t.Fatalf("a request to another REL build: seq %d", seq)
	}
	if n := br.log.count(fmt.Sprintf("is build %08X, this build's is %08X; the arena keeps Gohdan", inject.RELBuildID^1, inject.RELBuildID)); n != 1 {
		t.Fatalf("logged %d times:\n%s", n, br.log)
	}
}

// TestBossAckKeyBst: the fight's actions log as one key; the eye phase,
// the mouth and the death differ.
func TestBossAckKeyBst(t *testing.T) {
	key := func(act, dmg, phase uint8) string {
		return bossAckKey(mailbox.BossProcBst, mailbox.BossStatus{Result: mailbox.BossRWaiting, Act: act, State: dmg, Phase: phase})
	}
	fight := key(0, 0, 1)
	for _, act := range []uint8{1, 10, 11, 12, 13, 14} {
		if k := key(act, 0, 1); k != fight {
			t.Errorf("act %d: %q, want %q", act, k, fight)
		}
	}
	for _, k := range []string{key(5, 0, 1), key(6, 0, 1), key(7, 3, 12), key(0x16, 0, 12), key(0, 0, 12)} {
		if k == fight {
			t.Errorf("%q is the fight's key", k)
		}
	}
}
