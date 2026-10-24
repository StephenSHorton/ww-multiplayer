package replica

import (
	"encoding/binary"
	"fmt"
	"strings"
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
		{"Molgera", "kazeB", addr.TableWT, held(wtBossItems), -1, mailbox.BossProcBwd},
		{"kazeB with TotG's bit held", "kazeB", addr.TableWT, held(totgBossItems), -1, 0},
		{"kazeB with ET's table", "kazeB", addr.TableET, held(etBossItems), -1, 0},
		{"kazeB, the local bit set", "kazeB", addr.TableWT, held(wtBossItems), wtBossItems, 0},
		{"the WT dungeon", "kaze", addr.TableWT, held(wtBossItems), -1, 0},
		{"the WT miniboss room", "kazeMB", addr.TableWT, held(wtBossItems), -1, 0},
		{"an X stage on WT's table", "Xboss3", addr.TableWT, held(wtBossItems), -1, 0},
		{"Jalhalla", "M_DaiB", addr.TableET, held(etBossItems), -1, mailbox.BossProcBpw},
		{"M_DaiB with WT's bit held", "M_DaiB", addr.TableET, held(wtBossItems), -1, 0},
		{"M_DaiB with WT's table", "M_DaiB", addr.TableWT, held(wtBossItems), -1, 0},
		{"M_DaiB, the local bit set", "M_DaiB", addr.TableET, held(etBossItems), etBossItems, 0},
		{"the ET dungeon", "M_Dai", addr.TableET, held(etBossItems), -1, 0},
		{"the ET miniboss room", "M_DaiMB", addr.TableET, held(etBossItems), -1, 0},
		{"an X stage on ET's table", "Xboss2", addr.TableET, held(etBossItems), -1, 0},
		{"Helmaroc King", "M2tower", addr.TableFF, held(ffBossItems), -1, mailbox.BossProcBdk},
		{"M2tower with ET's bit held", "M2tower", addr.TableFF, held(etBossItems), -1, 0},
		{"M2tower with DRC's table", "M2tower", addr.TableDRC, held(ffBossItems), -1, 0},
		{"M2tower, the local bit set", "M2tower", addr.TableFF, held(ffBossItems), ffBossItems, 0},
		{"the FF1 tower (BOSS type, table 2)", "Mjtower", addr.TableFF, held(ffBossItems), -1, 0},
		{"MajyuE", "MajyuE", addr.TableFF, held(ffBossItems), -1, 0},
		{"majroom", "majroom", addr.TableFF, held(ffBossItems), -1, 0},
		{"ma2room", "ma2room", addr.TableFF, held(ffBossItems), -1, 0},
		{"ma3room", "ma3room", addr.TableFF, held(ffBossItems), -1, 0},
		{"M2ganon", "M2ganon", addr.TableFF, held(ffBossItems), -1, 0},
		{"the sea with FF's bit held", "sea", 0, held(ffBossItems), -1, 0},
		{"an X stage on FF's table", "Xboss0", addr.TableFF, held(ffBossItems), -1, 0},
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
	// Between Bmd (0xEC) and Bst (0xF2): BMDHAND, BMDFOOT, DK (0xEF),
	// BDKOBJ (0xF1) and the rest are no value of the proc's low byte;
	// Helmaroc King's 0xF0 is a row's (below).
	for v := 0xED; v < 0xF2; v++ {
		if v == 0xF0 {
			continue
		}
		if r := judge(procAt+1, byte(v)); r != RefuseValue {
			t.Fatalf("proc low byte 0x%02X: %v, want a value refusal", v, r)
		}
	}
	// Molgera's, Jalhalla's and Helmaroc King's: taken; Molgera's larvae
	// (BWDS 0xDB) and sand floor (BWDG 0xDC), Jalhalla's Poes (PW 0xD5)
	// and Helmaroc King's DK (0xEF) and BDKOBJ (0xF1) are no value.
	for _, p := range []uint16{mailbox.BossProcBwd, mailbox.BossProcBpw, mailbox.BossProcBdk} {
		f, _ := mailbox.BossRequestFields(p, 1)
		for i, b := range f {
			if r := judge(procAt+uint32(i), b); r != 0 {
				t.Fatalf("%s's proc/tag byte %d 0x%02X refused: %s", mailbox.BossProcName(p), i, b, r)
			}
		}
	}
	for _, v := range []byte{0xD5, 0xDB, 0xDC, 0xEF, 0xF1} {
		if r := judge(procAt+1, v); r != RefuseValue {
			t.Fatalf("proc low byte 0x%02X: %v, want a value refusal", v, r)
		}
	}
	// The boss procs' bytes are the boss path's alone: the refresh
	// request's bytes take none of the boss rows' procs (its proc is the
	// allow-list's).
	if _, br, ok := g.at(mailbox.RefreshAddr + mailbox.RefreshProcOff + 1); !ok || br.paths != pathRefresh {
		t.Fatalf("the refresh proc byte: ok %v paths %s", ok, br.paths)
	} else {
		for _, v := range []byte{0xF2, 0xDA, 0xD4, 0xF0} {
			if r, _ := judgeByte(br, 0, v); r != RefuseValue {
				t.Fatalf("0x%02X in the refresh request's proc: %v", v, r)
			}
		}
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
		{"molgera", []uint16{mailbox.BossProcBwd}, false},
		{"jalhalla", []uint16{mailbox.BossProcBpw}, false},
		{"gohma,kalle,gohdan,Molgera,jalhalla", []uint16{mailbox.BossProcBtd, mailbox.BossProcBmd, mailbox.BossProcBst, mailbox.BossProcBwd, mailbox.BossProcBpw}, false},
		{"helmaroc", []uint16{mailbox.BossProcBdk}, false},
		{"gohma,helmaroc", []uint16{mailbox.BossProcBtd, mailbox.BossProcBdk}, false},
		{BossKillTokens, []uint16{mailbox.BossProcBtd, mailbox.BossProcBmd, mailbox.BossProcBst, mailbox.BossProcBwd, mailbox.BossProcBpw, mailbox.BossProcBdk}, false},
		{"none", nil, false},
		{"gohma,ganon", nil, true},
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
	// The default leaves the opt-in rows out.
	def, _ := ParseBossKill("")
	for _, p := range []uint16{mailbox.BossProcBst, mailbox.BossProcBwd, mailbox.BossProcBpw, mailbox.BossProcBdk} {
		if def[p] {
			t.Fatalf("the default row set takes %s", mailbox.BossProcName(p))
		}
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

// wtBossItems and etBossItems are Wind Temple's and Earth Temple's
// mDungeonItem bytes in the image.
var (
	wtBossItems = addr.OffMemBit(addr.TableWT) + addr.MemBitOffDungeonItem
	etBossItems = addr.OffMemBit(addr.TableET) + addr.MemBitOffDungeonItem
)

// wtBoss and etBoss are the Molgera and Jalhalla arenas, BOSS-type stages
// of WT's and ET's tables.
var (
	wtBoss = fakegame.Stage{Name: "kazeB", Table: addr.TableWT, KeyHUD: true}
	etBoss = fakegame.Stage{Name: "M_DaiB", Table: addr.TableET, KeyHUD: true}
)

// batchERows is --coop-boss-kill with Molgera and Jalhalla named.
const batchERows = "gohma,kalle,molgera,jalhalla"

// partnerKilledAt is the world with the partner's boss of the table at
// off dead (and its demo seen), and the map and compass.
func (br *bossRig) partnerKilledAt(off int) {
	var img save.Image
	img[off] = 0x2B
	br.world(img)
}

// bossAnswers acks every answer in turn with result waiting, checking the
// relay asks again after bossRetry and not before.
func (br *bossRig) bossAnswers(t *testing.T, answers [][3]uint8) {
	t.Helper()
	for _, a := range answers {
		s, _, _ := br.request()
		br.relPhase(s, mailbox.BossRWaiting, a[0], a[1], a[2])
		br.ticks(1)
		if n, _, _ := br.request(); n != s {
			t.Fatalf("asked again (%d) before bossRetry", n)
		}
		br.clock.advance(bossRetry)
		br.ticks(1)
		if n, _, _ := br.request(); n != s+1 {
			t.Fatalf("after waiting at % X: seq %d, want %d; log:\n%s", a, n, s+1, br.log)
		}
	}
}

// TestBossKillRelayBwd: with Molgera in the row set, the partner kills it
// while this game is in kazeB. The request names Bwd's proc; the intro,
// the fight's states (one key) and a pull still at its one-shot are each
// logged once while asked again after bossRetry; the pulled tongue's
// applied is not asked again during the death camera, and the relay stops
// once the game has set WT's bit itself.
func TestBossKillRelayBwd(t *testing.T) {
	br := newBossRigSet(t, wtBoss, true, batchERows)
	br.partnerKilledAt(wtBossItems)
	br.ticks(3)
	seq, proc, tag := br.request()
	if seq == 0 || proc != mailbox.BossProcBwd || tag != mailbox.BossTag {
		t.Fatalf("request seq %d proc 0x%X tag 0x%X; log:\n%s", seq, proc, tag, br.log)
	}
	if v := br.liveByte(wtBossItems); v&bossEnemy != 0 {
		t.Fatalf("the boss bit was written live in the arena: 0x%02X", v)
	}
	if st := br.r.Status(); !st.BossKill.Wanted || st.BossKill.Boss != "Molgera" || st.BossKill.Requests != 1 {
		t.Fatalf("status %+v", st.BossKill)
	}
	if !br.log.has("the partner killed Molgera") || !br.log.has("once its tongue is pulled out") {
		t.Fatalf("no Molgera line:\n%s", br.log)
	}
	// m18AE, m18B0, m1BB5: the intro, then s_fly and the fight (wait,
	// reset, eat, fly, all one key), then a pull at its one-shot step 1.
	br.bossAnswers(t, [][3]uint8{
		{mailbox.BossBwdActIntro, 0, 0},
		{mailbox.BossBwdActSFly, 0, 0}, {1, 0, 0}, {2, 1, 0}, {5, 2, 0}, {10, 0, 0}, {1, 0xF6, 0},
		{mailbox.BossBwdActTongue, 1, 0},
	})
	if n := br.log.count(": waiting (m18AE 0x"); n != 3 {
		t.Fatalf("waiting logged %d times, want 3 (intro, fight, tongue step 1):\n%s", n, br.log)
	}
	if !br.log.has("waiting (m18AE 0x0, m18B0 0, m1BB5 0)") || !br.log.has("waiting (m18AE 0x3, m18B0 1, m1BB5 0)") {
		t.Fatalf("Bwd's fields not logged:\n%s", br.log)
	}

	// Applied with the tongue looped: not asked again during the demo.
	s, _, _ := br.request()
	br.relPhase(s, mailbox.BossRApplied, mailbox.BossBwdActTongue, mailbox.BossBwdStepPulled, 2)
	br.ticks(1)
	br.clock.advance(10 * bossRetry)
	br.ticks(3)
	if n, _, _ := br.request(); n != s {
		t.Fatalf("asked again (%d) during the demo", n)
	}
	if st := br.r.Status(); st.BossKill.Applied != 1 || st.BossKill.Result != "applied" {
		t.Fatalf("status %+v", st.BossKill)
	}
	if !br.log.has("applied (m18AE 0x3, m18B0 2, m1BB5 2)") {
		t.Fatalf("no applied line:\n%s", br.log)
	}

	// The death camera sets WT's bit: the relay ends.
	br.set(wtBossItems, bossEnemy)
	br.clock.advance(bossSettle)
	br.ticks(3)
	if n, _, _ := br.request(); n != s {
		t.Fatalf("asked again (%d) once the game has the bit", n)
	}
	if st := br.r.Status(); st.BossKill.Wanted {
		t.Fatalf("still wanted with the bit set: %+v", st.BossKill)
	}
}

// TestBossKillRelayBpw: with Jalhalla in the row set, the partner kills it
// while this game is in M_DaiB. The request names Bpw's proc; the intro,
// the fight, the light build-up, a wall bounce and the split are each
// logged once while asked again after bossRetry; applied at the freeze
// settles; a later already with health 0 off the death demo (a missed
// throw) logs its own words once and settles too; the relay stops once
// the game has set ET's bit.
func TestBossKillRelayBpw(t *testing.T) {
	br := newBossRigSet(t, etBoss, true, batchERows)
	br.partnerKilledAt(etBossItems)
	br.ticks(3)
	seq, proc, tag := br.request()
	if seq == 0 || proc != mailbox.BossProcBpw || tag != mailbox.BossTag {
		t.Fatalf("request seq %d proc 0x%X tag 0x%X; log:\n%s", seq, proc, tag, br.log)
	}
	if v := br.liveByte(etBossItems); v&bossEnemy != 0 {
		t.Fatalf("the boss bit was written live in the arena: 0x%02X", v)
	}
	if st := br.r.Status(); !st.BossKill.Wanted || st.BossKill.Boss != "Jalhalla" || st.BossKill.Requests != 1 {
		t.Fatalf("status %+v", st.BossKill)
	}
	if !br.log.has("the partner killed Jalhalla") || !br.log.has("once it is frozen by light") {
		t.Fatalf("no Jalhalla line:\n%s", br.log)
	}
	// mBodyAction, mActionState, health.
	br.bossAnswers(t, [][3]uint8{
		{mailbox.BossBpwActIntro, 0xC8, 15},
		{0, 0, 15}, {1, 3, 15}, {2, 0x28, 15},
		{mailbox.BossBpwActDamage, 0x50, 15}, {mailbox.BossBpwActDamage, 0x51, 15},
		{mailbox.BossBpwActDamage, 0x5A, 15},
		{mailbox.BossBpwActSeparate, 0x6F, 15}, {mailbox.BossBpwActSeparate, 0x72, 14},
	})
	if n := br.log.count(": waiting (mBodyAction "); n != 5 {
		t.Fatalf("waiting logged %d times, want 5 (intro, fight, light, bounce, split):\n%s", n, br.log)
	}
	if !br.log.has("waiting (mBodyAction 20, mActionState 0xC8, health 15)") || !br.log.has("waiting (mBodyAction 4, mActionState 0x6F, health 15)") {
		t.Fatalf("Bpw's fields not logged:\n%s", br.log)
	}

	// Applied while carried: not asked again for bossSettle.
	s, _, _ := br.request()
	br.relPhase(s, mailbox.BossRApplied, mailbox.BossBpwActDamage, 0x54, 15)
	br.ticks(1)
	br.clock.advance(10 * bossRetry)
	br.ticks(3)
	if n, _, _ := br.request(); n != s {
		t.Fatalf("asked again (%d) after applied", n)
	}
	if !br.log.has("applied (mBodyAction 3, mActionState 0x54, health 15); this game's Jalhalla falls on its next throw into the spikes (an empty boss gauge until then is expected)") {
		t.Fatalf("no applied line with the gauge note:\n%s", br.log)
	}

	// The throw missed: the next asks find health 0 in the fight, which
	// logs once in its own words and settles as already.
	for range 2 {
		br.clock.advance(bossSettle)
		br.ticks(1)
		s, _, _ = br.request()
		br.relPhase(s, mailbox.BossRAlready, 0, 0, 0)
		br.ticks(1)
		br.clock.advance(10 * bossRetry)
		br.ticks(3)
		if n, _, _ := br.request(); n != s {
			t.Fatalf("asked again (%d) after already", n)
		}
	}
	const pending = "applied earlier; this game's Jalhalla falls on its next throw into the spikes (an empty boss gauge until then is expected) (mBodyAction 0, mActionState 0x0)"
	if n := br.log.count(pending); n != 1 {
		t.Fatalf("the pending zero logged %d times:\n%s", n, br.log)
	}
	if br.log.has(": already dying (mBodyAction 0") {
		t.Fatalf("the pending zero read as already dying:\n%s", br.log)
	}

	// The death demo sets ET's bit: the relay ends.
	br.set(etBossItems, bossEnemy)
	br.clock.advance(bossSettle)
	br.ticks(3)
	if n, _, _ := br.request(); n != s {
		t.Fatalf("asked again (%d) once the game has the bit", n)
	}
	if st := br.r.Status(); st.BossKill.Wanted || st.BossKill.Applied != 1 {
		t.Fatalf("status with the bit set: %+v", st.BossKill)
	}
}

// TestBossKillBwdDefaultOff and TestBossKillBpwDefaultOff: with the
// default row set (gohma,kalle), kazeB and M_DaiB get no request; the log
// says once why the arena keeps its boss.
func TestBossKillBwdDefaultOff(t *testing.T) { testBossDefaultOff(t, wtBoss, wtBossItems, "Molgera") }
func TestBossKillBpwDefaultOff(t *testing.T) { testBossDefaultOff(t, etBoss, etBossItems, "Jalhalla") }

func testBossDefaultOff(t *testing.T, stage fakegame.Stage, off int, name string) {
	t.Helper()
	br := newBossRig(t, stage, true)
	br.partnerKilledAt(off)
	br.ticks(3)
	br.clock.advance(bossRetry)
	br.ticks(3)
	if seq, _, _ := br.request(); seq != 0 {
		t.Fatalf("%s: a request without the row: seq %d", name, seq)
	}
	if st := br.r.Status(); st.BossKill.Wanted || st.Writes.BossKills != 0 {
		t.Fatalf("%s: status %+v", name, st.BossKill)
	}
	if n := br.log.count("the partner killed " + name + ", but --coop-boss-kill (gohma,kalle) leaves it out"); n != 1 {
		t.Fatalf("%s: logged %d times:\n%s", name, n, br.log)
	}
}

// TestBossKillBwdNeverOutsideKazeB: with Molgera named, no other stage of
// WT's table (the temple, the miniboss room, an X refight stage) gets a
// request, and --coop-boss-kill=molgera alone leaves Gohma's arena alone.
func TestBossKillBwdNeverOutsideKazeB(t *testing.T) {
	testBossNeverOutside(t, wtBossItems, "molgera", batchERows, fakegame.Stage{Name: "kaze", Table: addr.TableWT, KeyHUD: true},
		fakegame.Stage{Name: "kazeMB", Table: addr.TableWT, KeyHUD: true}, fakegame.Stage{Name: "Xboss3", Table: addr.TableWT, KeyHUD: true})
}

// TestBossKillBpwNeverOutsideMDaiB: with Jalhalla named, no other stage of
// ET's table (the temple, the miniboss room, an X refight stage) gets a
// request, and --coop-boss-kill=jalhalla alone leaves Gohma's arena alone.
func TestBossKillBpwNeverOutsideMDaiB(t *testing.T) {
	testBossNeverOutside(t, etBossItems, "jalhalla", batchERows, fakegame.Stage{Name: "M_Dai", Table: addr.TableET, KeyHUD: true},
		fakegame.Stage{Name: "M_DaiMB", Table: addr.TableET, KeyHUD: true}, fakegame.Stage{Name: "Xboss2", Table: addr.TableET, KeyHUD: true})
}

func testBossNeverOutside(t *testing.T, off int, key, rows string, stages ...fakegame.Stage) {
	t.Helper()
	for _, st := range stages {
		br := newBossRigSet(t, st, true, rows)
		br.partnerKilledAt(off)
		br.ticks(5)
		if seq, _, _ := br.request(); seq != 0 {
			t.Fatalf("%s: a request: seq %d", st.Name, seq)
		}
	}
	br := newBossRigSet(t, drcBoss, true, key)
	br.partnerKilled()
	br.ticks(5)
	if seq, _, _ := br.request(); seq != 0 {
		t.Fatalf("Gohma asked for with --coop-boss-kill=%s: seq %d", key, seq)
	}
}

// TestBossKillBwdNoREL and TestBossKillBpwNoREL: without the REL module
// the relay writes nothing and names its boss once; a REL of another
// build likewise.
func TestBossKillBwdNoREL(t *testing.T) {
	testBossNoREL(t, wtBoss, wtBossItems, "Molgera", batchERows)
}
func TestBossKillBpwNoREL(t *testing.T) {
	testBossNoREL(t, etBoss, etBossItems, "Jalhalla", batchERows)
}

func testBossNoREL(t *testing.T, stage fakegame.Stage, off int, name, rows string) {
	t.Helper()
	br := newBossRigSet(t, stage, false, rows)
	br.partnerKilledAt(off)
	br.ticks(3)
	br.clock.advance(bossRetry)
	br.ticks(3)
	if seq, _, _ := br.request(); seq != 0 {
		t.Fatalf("%s: a request without the REL: seq %d", name, seq)
	}
	if n := br.log.count("the arena keeps " + name + " until it is"); n != 1 {
		t.Fatalf("%s: logged %d times:\n%s", name, n, br.log)
	}
	br.linkREL(inject.RELBuildID ^ 1)
	br.clock.advance(bossRetry)
	br.ticks(3)
	if seq, _, _ := br.request(); seq != 0 {
		t.Fatalf("%s: a request to another REL build: seq %d", name, seq)
	}
	if n := br.log.count(fmt.Sprintf("is build %08X, this build's is %08X; the arena keeps %s", inject.RELBuildID^1, inject.RELBuildID, name)); n != 1 {
		t.Fatalf("%s: logged %d times:\n%s", name, n, br.log)
	}
}

// TestBossAckKeyBwd: the fight's states (wait, reset, eat, fly, s_fly)
// log as one key; the intro, each sita_hit step, each critical pull and
// the death differ.
func TestBossAckKeyBwd(t *testing.T) {
	key := func(act, step, crit uint8) string {
		return bossAckKey(mailbox.BossProcBwd, mailbox.BossStatus{Result: mailbox.BossRWaiting, Act: act, State: step, Phase: crit})
	}
	fight := key(1, 0, 0)
	for _, a := range [][2]uint8{{2, 1}, {5, 3}, {10, 0}, {mailbox.BossBwdActSFly, 2}, {1, 0xF6}} {
		if k := key(a[0], a[1], 0); k != fight {
			t.Errorf("act %d step %d: %q, want %q", a[0], a[1], k, fight)
		}
	}
	seen := map[string]bool{fight: true}
	for _, k := range []string{key(0, 0, 0), key(3, 0, 0), key(3, 1, 0), key(3, 2, 0), key(3, 2, 1), key(1, 0, 2), key(0xC, 0, 4), key(0xD, 0, 0)} {
		if seen[k] {
			t.Errorf("%q repeats a key", k)
		}
		seen[k] = true
	}
}

// TestBossAckKeyBpw: each class of mBodyAction and mActionState is one
// key whatever the health; health 0 is its own key.
func TestBossAckKeyBpw(t *testing.T) {
	key := func(r, act, st, hp uint8) string {
		return bossAckKey(mailbox.BossProcBpw, mailbox.BossStatus{Result: r, Act: act, State: st, Phase: hp})
	}
	w := mailbox.BossRWaiting
	for _, c := range []struct {
		name string
		same [][2]uint8
	}{
		{"fight", [][2]uint8{{0, 0}, {1, 7}, {2, 0x28}}},
		{"light", [][2]uint8{{3, 0x50}, {3, 0x51}}},
		{"gate", [][2]uint8{{3, 0x52}, {3, 0x53}, {3, 0x54}, {3, 0x55}, {3, 0x64}, {3, 0x65}}},
		{"bounce", [][2]uint8{{3, 0x5A}, {3, 0x5B}, {3, 0x5C}}},
		{"split", [][2]uint8{{4, 0x6E}, {4, 0x6F}, {4, 0x75}}},
		{"death", [][2]uint8{{4, 0x78}, {4, 0x86}, {4, 0x88}}},
		{"intro", [][2]uint8{{0x14, 0xC8}, {0x14, 0xD0}}},
	} {
		k0 := key(w, c.same[0][0], c.same[0][1], 15)
		for _, s := range c.same {
			for _, hp := range []uint8{1, 9, 15} {
				if k := key(w, s[0], s[1], hp); k != k0 {
					t.Errorf("%s: act %d st 0x%X hp %d: %q, want %q", c.name, s[0], s[1], hp, k, k0)
				}
			}
		}
		if key(w, c.same[0][0], c.same[0][1], 0) == k0 {
			t.Errorf("%s: health 0 shares the key", c.name)
		}
	}
	if key(w, 0, 0, 15) == key(w, 3, 0x52, 15) || key(w, 3, 0x52, 15) == key(w, 3, 0x5A, 15) || key(w, 4, 0x6F, 15) == key(w, 4, 0x78, 15) {
		t.Error("two classes share a key")
	}
	if got := bpwClass(3, 0x58); got != "0x3/0x58" {
		t.Errorf("an unclassed state: %q", got)
	}
}

// TestBossAckLineBpw: Jalhalla's applied line names the next throw while
// it is frozen or carried (0x52..0x54) and the landing under way once it
// is thrown or in the spikes (0x55, 0x64, 0x65).
func TestBossAckLineBpw(t *testing.T) {
	for _, c := range []struct {
		st   uint8
		want string
	}{
		{0x52, "falls on its next throw into the spikes"},
		{0x54, "falls on its next throw into the spikes"},
		{0x55, "falls on this landing in the spikes"},
		{0x64, "falls on this landing in the spikes"},
		{0x65, "falls on this landing in the spikes"},
	} {
		line := bossAckLine(7, mailbox.BossProcBpw, mailbox.BossStatus{Result: mailbox.BossRApplied, Act: mailbox.BossBpwActDamage, State: c.st, Phase: 15})
		if !strings.Contains(line, "; this game's Jalhalla "+c.want+" (an empty boss gauge until then is expected)") {
			t.Errorf("state 0x%X: %q, want %q", c.st, line, c.want)
		}
	}
}

// ffBossItems is Forsaken Fortress's (table 2) mDungeonItem byte in the
// image, and ffBoss the Helmaroc King arena, M2tower.
var (
	ffBossItems = addr.OffMemBit(addr.TableFF) + addr.MemBitOffDungeonItem
	ffBoss      = fakegame.Stage{Name: "M2tower", Table: addr.TableFF, KeyHUD: true}
)

// batchFRows is --coop-boss-kill with Helmaroc King named.
const batchFRows = "gohma,kalle,helmaroc"

// TestBossKillRelayBdk: with Helmaroc King in the row set, the partner
// kills it while this game is in M2tower. The request names Bdk's proc;
// the tower phase, the intro, each mask hit count, the mask demo and the
// crest fight under a demo camera are each logged once while asked again
// after bossRetry; applied in the crest fight settles; a later already
// with health 0 at a fight action (the zero not yet taken) logs its own
// words once and settles too; the relay stops once the game has set
// table 2's bit.
func TestBossKillRelayBdk(t *testing.T) {
	br := newBossRigSet(t, ffBoss, true, batchFRows)
	br.partnerKilledAt(ffBossItems)
	br.ticks(3)
	seq, proc, tag := br.request()
	if seq == 0 || proc != mailbox.BossProcBdk || tag != mailbox.BossTag {
		t.Fatalf("request seq %d proc 0x%X tag 0x%X; log:\n%s", seq, proc, tag, br.log)
	}
	if v := br.liveByte(ffBossItems); v&bossEnemy != 0 {
		t.Fatalf("the boss bit was written live in the arena: 0x%02X", v)
	}
	if st := br.r.Status(); !st.BossKill.Wanted || st.BossKill.Boss != "Helmaroc King" || st.BossKill.Requests != 1 {
		t.Fatalf("status %+v", st.BossKill)
	}
	if !br.log.has("the partner killed Helmaroc King") || !br.log.has("once its mask is broken") {
		t.Fatalf("no Helmaroc King line:\n%s", br.log)
	}
	// mAction, m8F8, health: the climb (the create's zeroed health), the
	// intro, the mask fight at each hit count, the mask demo (m8F8 3, then
	// 4 from its camera's case 1), the crest fight under a demo camera.
	br.bossAnswers(t, [][3]uint8{
		{mailbox.BossBdkActTFly, 0, 0}, {0x66, 0, 0},
		{mailbox.BossBdkActStart, 0, 20},
		{0, 0, 20}, {3, 0, 20}, {7, 1, 20}, {1, 2, 20}, {8, 3, 20},
		{mailbox.BossBdkActMaskDemo, 3, 20}, {mailbox.BossBdkActMaskDemo, 4, 20},
		{5, 4, 20},
	})
	if n := br.log.count(": waiting (mAction 0x"); n != 8 {
		t.Fatalf("waiting logged %d times, want 8 (tower, intro, mask 0..3, maskdemo, crest):\n%s", n, br.log)
	}
	if !br.log.has("waiting (mAction 0x64, m8F8 0, health 0)") || !br.log.has("waiting (mAction 0x9, m8F8 3, health 20)") {
		t.Fatalf("Bdk's fields not logged:\n%s", br.log)
	}

	// Applied in the crest fight: not asked again for bossSettle.
	s, _, _ := br.request()
	br.relPhase(s, mailbox.BossRApplied, 5, mailbox.BossBdkMaskBroken, 20)
	br.ticks(1)
	br.clock.advance(10 * bossRetry)
	br.ticks(3)
	if n, _, _ := br.request(); n != s {
		t.Fatalf("asked again (%d) after applied", n)
	}
	if !br.log.has("applied (mAction 0x5, m8F8 4, health 20); this game's Helmaroc King falls on its next sword hit on the crest (an empty boss gauge until then is expected)") {
		t.Fatalf("no applied line with the gauge note:\n%s", br.log)
	}
	if st := br.r.Status(); st.BossKill.Applied != 1 || st.BossKill.Result != "applied" {
		t.Fatalf("status %+v", st.BossKill)
	}

	// No crest hit yet: the next asks find health 0 in the fight, which
	// logs once in its own words and settles as already.
	for range 2 {
		br.clock.advance(bossSettle)
		br.ticks(1)
		s, _, _ = br.request()
		br.relPhase(s, mailbox.BossRAlready, 1, mailbox.BossBdkMaskBroken, 0)
		br.ticks(1)
		br.clock.advance(10 * bossRetry)
		br.ticks(3)
		if n, _, _ := br.request(); n != s {
			t.Fatalf("asked again (%d) after already", n)
		}
	}
	const pending = "applied earlier; this game's Helmaroc King falls on its next sword hit on the crest (an empty boss gauge until then is expected) (mAction 0x1, m8F8 4)"
	if n := br.log.count(pending); n != 1 {
		t.Fatalf("the pending zero logged %d times:\n%s", n, br.log)
	}
	if br.log.has(": already dying (mAction 0x1") {
		t.Fatalf("the pending zero read as already dying:\n%s", br.log)
	}

	// The death camera's case 0x72 sets table 2's bit: the relay ends.
	br.set(ffBossItems, bossEnemy)
	br.clock.advance(bossSettle)
	br.ticks(3)
	if n, _, _ := br.request(); n != s {
		t.Fatalf("asked again (%d) once the game has the bit", n)
	}
	if st := br.r.Status(); st.BossKill.Wanted || st.BossKill.Applied != 1 {
		t.Fatalf("status with the bit set: %+v", st.BossKill)
	}
}

// TestBossKillBdkNoActor: M2tower's layers 0/1 (before the rescue) place
// no Bdk; "no actor" is asked again every bossRetry and logged once.
func TestBossKillBdkNoActor(t *testing.T) {
	br := newBossRigSet(t, ffBoss, true, batchFRows)
	br.partnerKilledAt(ffBossItems)
	br.ticks(3)
	for range 4 {
		s, _, _ := br.request()
		br.relPhase(s, mailbox.BossRNoActor, 0, 0, 0)
		br.ticks(1)
		if n, _, _ := br.request(); n != s {
			t.Fatalf("asked again (%d) before bossRetry", n)
		}
		br.clock.advance(bossRetry)
		br.ticks(1)
		if n, _, _ := br.request(); n != s+1 {
			t.Fatalf("after no actor: seq %d, want %d; log:\n%s", n, s+1, br.log)
		}
	}
	if n := br.log.count(": no actor (mAction 0x0, m8F8 0, health 0)"); n != 1 {
		t.Fatalf("no actor logged %d times:\n%s", n, br.log)
	}
}

// TestBossKillBdkDefaultOff: with the default row set, M2tower gets no
// request; the log says once why the arena keeps its boss.
func TestBossKillBdkDefaultOff(t *testing.T) {
	testBossDefaultOff(t, ffBoss, ffBossItems, "Helmaroc King")
}

// TestBossKillBdkNeverOutsideM2tower: with Helmaroc King named, no other
// stage of table 2 gets a request, though the bits are held in each
// (bossStage takes every table-2 stage) and Mjtower is BOSS type too, nor
// an X stage on table 2; --coop-boss-kill=helmaroc alone leaves Gohma's
// arena alone.
func TestBossKillBdkNeverOutsideM2tower(t *testing.T) {
	var stages []fakegame.Stage
	for _, n := range []string{"M2ganon", "ma2room", "ma3room", "majroom", "MajyuE", "Mjtower", "Xboss0"} {
		stages = append(stages, fakegame.Stage{Name: n, Table: addr.TableFF, KeyHUD: true})
	}
	testBossNeverOutside(t, ffBossItems, "helmaroc", batchFRows, stages...)
}

// TestBossKillBdkNoREL: without the REL module, or with one of another
// build, the relay writes nothing and names Helmaroc King once each.
func TestBossKillBdkNoREL(t *testing.T) {
	testBossNoREL(t, ffBoss, ffBossItems, "Helmaroc King", batchFRows)
}

// TestBossAckKeyBdk: each class of mAction and m8F8 is one key whatever
// the health; each mask hit count is its own, and health 0 is its own.
func TestBossAckKeyBdk(t *testing.T) {
	key := func(r, act, mask, hp uint8) string {
		return bossAckKey(mailbox.BossProcBdk, mailbox.BossStatus{Result: r, Act: act, State: mask, Phase: hp})
	}
	w := mailbox.BossRWaiting
	for _, c := range []struct {
		name string
		same [][2]uint8
	}{
		{"tower", [][2]uint8{{0x64, 0}, {0x65, 0}, {0x66, 0}, {0x67, 0}}},
		{"intro", [][2]uint8{{0xF, 0}, {0xF, 4}}},
		{"mask 0", [][2]uint8{{0, 0}, {4, 0}, {8, 0}}},
		{"mask 2", [][2]uint8{{1, 2}, {6, 2}}},
		{"maskdemo", [][2]uint8{{9, 3}, {9, 4}}},
		{"crest", [][2]uint8{{0, 4}, {5, 4}, {8, 4}}},
		{"end", [][2]uint8{{0xA, 4}}},
		{"after", [][2]uint8{{0x6E, 0}}},
	} {
		k0 := key(w, c.same[0][0], c.same[0][1], 20)
		for _, s := range c.same {
			for _, hp := range []uint8{1, 9, 20} {
				if k := key(w, s[0], s[1], hp); k != k0 {
					t.Errorf("%s: act 0x%X m8F8 %d hp %d: %q, want %q", c.name, s[0], s[1], hp, k, k0)
				}
			}
		}
		if key(w, c.same[0][0], c.same[0][1], 0) == k0 {
			t.Errorf("%s: health 0 shares the key", c.name)
		}
	}
	seen := map[string]bool{}
	for _, k := range []string{key(w, 0x64, 0, 20), key(w, 0xF, 0, 20), key(w, 0, 0, 20), key(w, 0, 1, 20), key(w, 0, 2, 20), key(w, 0, 3, 20),
		key(w, 9, 3, 20), key(w, 0, 4, 20), key(w, 0xA, 4, 20), key(w, 0x6E, 0, 20)} {
		if seen[k] {
			t.Errorf("%q repeats a key", k)
		}
		seen[k] = true
	}
	if got := bdkClass(0x20, 1); got != "0x20/1" {
		t.Errorf("an unclassed action: %q", got)
	}
}

// TestBossAckLineBdk: Helmaroc King's applied line and an already at a
// fight action with health 0 name the next crest hit; an already in the
// death (0xA) or the inert create (0x6E) reads plain.
func TestBossAckLineBdk(t *testing.T) {
	const note = "this game's Helmaroc King falls on its next sword hit on the crest (an empty boss gauge until then is expected)"
	line := bossAckLine(7, mailbox.BossProcBdk, mailbox.BossStatus{Result: mailbox.BossRApplied, Act: 3, State: 4, Phase: 20})
	if line != "co-op: boss_kill 7: applied (mAction 0x3, m8F8 4, health 20); "+note {
		t.Errorf("applied: %q", line)
	}
	line = bossAckLine(8, mailbox.BossProcBdk, mailbox.BossStatus{Result: mailbox.BossRAlready, Act: 2, State: 4, Phase: 0})
	if line != "co-op: boss_kill 8: applied earlier; "+note+" (mAction 0x2, m8F8 4)" {
		t.Errorf("pending: %q", line)
	}
	for _, act := range []uint8{mailbox.BossBdkActEnd, mailbox.BossBdkActAfter} {
		line = bossAckLine(9, mailbox.BossProcBdk, mailbox.BossStatus{Result: mailbox.BossRAlready, Act: act, State: 4, Phase: 0})
		if strings.Contains(line, "applied earlier") || !strings.Contains(line, ": already dying (mAction 0x") {
			t.Errorf("act 0x%X: %q", act, line)
		}
	}
	line = bossAckLine(10, mailbox.BossProcBdk, mailbox.BossStatus{Result: mailbox.BossRWaiting, Act: 0x64, State: 0, Phase: 0})
	if line != "co-op: boss_kill 10: waiting (mAction 0x64, m8F8 0, health 0)" {
		t.Errorf("tower waiting: %q", line)
	}
}
