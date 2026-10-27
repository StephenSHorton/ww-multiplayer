package replica

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
)

// The boss-dead relay (ww-multiplayer-handoff/design/research/
// C3-C6-gohma.md, "The relay op (B5, revised)" and A9). Each game fights
// its own Gohma. When the partner kills theirs first, the world gets DRC's
// STAGE_BOSS_ENEMY, but a receiver still in M_DragB must not get the bit:
// the arena polls it every frame (Dr2 would drop the floor under a
// running fight, d_a_dr2.cpp:561-575), so the planner holds it there
// (bossHold) and that hold stays. Instead the receiver asks its REL for
// boss_kill (wwapi/mailbox boss.go, inject/rel/bosskill.c), which gives
// its own Btd the death a lethal eye hit gives it (d_a_btd.cpp:463-475).
// Its own death demo then drops the floor, spawns the heart container,
// sets STAGE_BOSS_ENEMY and creates the warp flower, and the planner's
// miss for the bit clears on its own once the game has it (Plan takes
// view &^ local). It is symmetric: whichever game kills Gohma first is
// the other's partner here. The REL applies the death only once the
// receiver has broken Gohma's armor itself (m02E0 1, the only phase the
// lethal eye hit takes, d_a_btd.cpp:457; bosskill.c "The armor gate"):
// until then every request is answered "waiting" with the phase seen, so
// the partner's kill ends the receiver's fight at the eye phase, not
// before it.
//
// The trigger is level-triggered on the tick's plan: the file armed, this
// game in one of bossKillRows' arenas (M_DragB with DRC loaded, kinBOSS
// with Forbidden Woods' table loaded), and the plan holding that table's
// STAGE_BOSS_ENEMY under bossHold, which the planner does only while the
// world has the bit and the local save lacks it. So it never fires in the
// X refight stages (another stage name, and the REL refuses any name but
// the proc's arena too: M_DragB for Btd, kinBOSS for Bmd), and it stops
// once the local save has the bit. The REL acks every request with a
// result: "waiting" (Gohma hidden, in its intro or still armored; Kalle
// Demos in its intro, with its vines up or its flower not yet down and
// open), "no actor" (the boss not created yet), "none"
// (swallowed by a keeper relink) and a closed gate (the receiver's own demo
// makes the write wait for IDLE) are asked again every bossRetry; "applied"
// and "already" are not asked again for bossSettle, while the death demo
// runs to the bit. Nothing is asked of a REL module of another build than
// this one's (inject.RELBuildID), which may not have the op at all.
//
// Kalle Demos (research/C1-kalle-demos-relay.md) is the same relay on
// another row of bossKillRows: in kinBOSS with Forbidden Woods' table
// loaded, the REL gives this game's own Bmd core_damage_check's lethal
// block (d_a_bmd.cpp:609-617), but only at the fallen, open flower
// (mMode 3 at m302 2..5 with m304 not 0): the receiver cuts its own
// vines first, as it breaks Gohma's armor, and every other state answers
// "waiting".
//
// Gohdan (design/enemy-mirror-batch-d.json R-D1..R-D3, D2, D3, "gohdan")
// is the third row: in SirenB with Tower of the Gods' table loaded, the
// REL gives this game's own Bst bom_eat_check's lethal block
// (d_a_bst.cpp:1219-1229) with the DEAD anim, bgmStop and the clear
// stream, but only from vanilla's own pre-death state: the head in
// HEAD_DAMAGE with its mouth open for a bomb (mDamage 3, m2E7C 12,
// m2E9A 0) and both hands asleep (d_a_bst.cpp:1159-1166, 1888-1905). The
// receiver shoots out both eyes itself, as it breaks Gohma's armor; the
// intro, the hands' fight, the eye phase and the mouth opening answer
// "waiting". Its own demo (demo_camera case 50-57) then sets the bit,
// creates the warp flower and drops the heart (d_a_bst.cpp:2429-2436,
// 2504-2510); nothing is deleted. Entering SirenB after the bit leaves an
// inert Bst (:1776), and bossKillWanted stops once the local bit is set.
//
// Molgera (design/relay-molgera.md) is the fourth row: in kazeB with Wind
// Temple's table loaded, the REL hands this game's own Bwd vanilla's last
// step instead of writing the lethal block. Only once the receiver has
// pulled the tongue out with its own Hookshot (sita_hit's step 2, m18AE 3
// and m18B0 2, the tongue timer running, no demo camera) does it write the
// hit and critical-pull counters (m1BB4 = 3, m1BB5 = 3); Bwd's next
// move() then counts the fourth critical pull and runs its own lethal
// block, end(), the heart container, STAGE_BOSS_ENEMY and the warp flower
// (d_a_bwd.cpp:533-552, 1020-1050, 1700-1705, 1764-1770). The intro, the
// sand fight, the eat demo and fly answer "waiting"; end() (m18AE 0xC) and
// the inert post-kill Bwd (0xD) answer "already".
//
// Jalhalla (design/relay-jalhalla.md) is the fifth: in M_DaiB with Earth
// Temple's table loaded, the REL writes health 0 into this game's own Bpw
// body once the receiver has frozen it with light (ACTION_DAMAGE at the
// frozen, carried, thrown or spike states), before any Poe exists. The
// next spike landing then creates no Poe and splits straight into the
// death demo, which drops the heart, sets the bit and creates the warp
// flower (d_a_bpw.rel .text 0x6284-0x651C, 0x8690-0x8940). A missed throw
// fights on with health 0 until the next spike landing, and asks in
// between answer "already" (logged in its own words: an empty Hero's Charm
// gauge on a live Jalhalla is expected then). The split with live Poes
// answers "waiting": a Poe cannot be killed by a write, so the receiver
// finishes it or regathers and is frozen again.
//
// Helmaroc King (design/relay-helmaroc.md) is the sixth: in M2tower with
// Forsaken Fortress's table 2 loaded, the REL writes health 0 into this
// game's own Bdk once the receiver has broken the mask itself (m8F8 >= 4,
// a fight action 0..8, no demo camera). health is read only by
// damage_check's crest branch, so the receiver's own next sword hit on the
// crest runs vanilla's death: the pause, end_set, the death camera and the
// bit (d_a_bdk.rel .text 0x4F20, 0x491C, 0xA284). The tower climb, the
// intro, the mask fight and its demo answer "waiting"; M2tower's layers 0
// and 1 (before the rescue) place no Bdk and answer "no actor". Unlike the
// other five rows, the bits are held in every table-2 stage (bossStage is
// true wherever table FF is loaded, and Mjtower is BOSS type too), but the
// relay asks only in M2tower (the row's stage): a receiver elsewhere in the
// fortress keeps the bits held until it leaves for the sea, and one that
// comes back to M2tower layer 3 fights the tower phase and the mask
// before the relay applies.
//
// The --coop-boss-kill row set (Config.BossKill, ParseBossKill) picks the
// rows the relay asks for, each side its own: gohma,kalle,molgera,
// jalhalla,helmaroc by default (Helmaroc King since HK-1, the two-Dolphin
// check and a real hammer fight passed, docs/TESTING.md "Batch F";
// Molgera and Jalhalla since MB-2, JH-1 and the two-Dolphin checks passed
// with a real Hookshot pull and a real light freeze and spike throw,
// "Batch E"). Gohdan joins the default once GD-4 and the D4 live check
// pass (G-D1; docs/TESTING.md "Batch D"); until then it is asked for only
// when named, and a list replaces the default, so all six are named:
// --coop-boss-kill=gohma,kalle,gohdan,molgera,jalhalla,helmaroc. A row
// left out is held as before (bossHold) and logged once; the WriteGuard
// takes every row's proc whatever the set (bossRules).

// bossRow is a boss the relay can kill: its arena, the save table loaded
// there, the proc the REL takes there, and the words the log says.
type bossRow struct {
	key   string // its --coop-boss-kill token
	stage string // the arena's stage name, the only one the REL accepts for proc (bosskill.c)
	table int8   // the save table loaded in the arena, whose STAGE_BOSS_ENEMY is held
	proc  uint16 // the boss's proc (mailbox.BossProc*)
	name  string // the boss, for the log
	gate  string // when the REL applies the kill, for the log
}

// bossKillRows are the bosses the relay kills, one per arena. No X
// refight stage is in it.
var bossKillRows = []bossRow{
	{key: "gohma", stage: "M_DragB", table: addr.TableDRC, proc: mailbox.BossProcBtd, name: "Gohma", gate: "once its armor is broken"},
	{key: "kalle", stage: "kinBOSS", table: addr.TableFW, proc: mailbox.BossProcBmd, name: "Kalle Demos", gate: "once its flower is down and open"},
	{key: "gohdan", stage: "SirenB", table: addr.TableTotG, proc: mailbox.BossProcBst, name: "Gohdan", gate: "once its mouth is open for a bomb"},
	{key: "molgera", stage: "kazeB", table: addr.TableWT, proc: mailbox.BossProcBwd, name: "Molgera", gate: "once its tongue is pulled out"},
	{key: "jalhalla", stage: "M_DaiB", table: addr.TableET, proc: mailbox.BossProcBpw, name: "Jalhalla", gate: "once it is frozen by light (this game's own next throw into the spikes then ends the fight)"},
	{key: "helmaroc", stage: "M2tower", table: addr.TableFF, proc: mailbox.BossProcBdk, name: "Helmaroc King", gate: "once its mask is broken (this game's own next sword hit on the crest then ends the fight)"},
}

const (
	// BossKillDefault is the --coop-boss-kill row set when none is given:
	// Gohma, Kalle Demos, Molgera, Jalhalla and Helmaroc King
	// (docs/TESTING.md "Batch E" and "Batch F"). Gohdan joins it after
	// GD-4 and the D4 live check (enemy-mirror-batch-d.json G-D1).
	BossKillDefault = "gohma,kalle,molgera,jalhalla,helmaroc"
	// BossKillTokens names every --coop-boss-kill token, for the usage.
	BossKillTokens = "gohma,kalle,gohdan,molgera,jalhalla,helmaroc"
)

// ParseBossKill parses a --coop-boss-kill row set: bossKillRows' keys
// joined by ",", "none" for no row, "" for BossKillDefault. It returns the
// rows' procs.
func ParseBossKill(v string) (map[uint16]bool, error) {
	if strings.TrimSpace(v) == "" {
		v = BossKillDefault
	}
	on := map[uint16]bool{}
	if strings.EqualFold(strings.TrimSpace(v), "none") {
		return on, nil
	}
	for tok := range strings.SplitSeq(v, ",") {
		tok = strings.ToLower(strings.TrimSpace(tok))
		i := slices.IndexFunc(bossKillRows, func(r bossRow) bool { return r.key == tok })
		if i < 0 {
			return nil, fmt.Errorf("--coop-boss-kill: %q is no boss (say %s, or none)", tok, BossKillTokens)
		}
		on[bossKillRows[i].proc] = true
	}
	return on, nil
}

// bossKillSet is Config.BossKill as the log says it.
func (cfg Config) bossKillSet() string {
	if strings.TrimSpace(cfg.BossKill) == "" {
		return BossKillDefault
	}
	return cfg.BossKill
}

// bossRowFor is the row of the arena stage, nil for any other stage.
func bossRowFor(stage string) *bossRow {
	for i := range bossKillRows {
		if bossKillRows[i].stage == stage {
			return &bossKillRows[i]
		}
	}
	return nil
}

const (
	// bossRetry is the wait before asking again after a result that asks
	// for it, or a write that did not go.
	bossRetry = 500 * time.Millisecond
	// bossAckWait is how long a request may go unacked (the keeper runs
	// every frame) before it is given up and asked again.
	bossAckWait = 2 * time.Second
	// bossSettle is how long after "applied" or "already" the relay waits
	// for the death demo to set the bit before asking again (inferred: the
	// demo, d_a_btd.cpp:905-1100 and :2186-2190, runs well inside it).
	bossSettle = 45 * time.Second
)

// bossKillWanted reports which boss this tick should have the REL kill in
// this game, nil for none: the file armed, in game, in a row's arena with
// its table loaded, and the plan holding that table's STAGE_BOSS_ENEMY
// under bossHold, with the local save lacking it.
func bossKillWanted(o *obs, armed bool, held []HeldBit) *bossRow {
	if !armed || o == nil || o.s == nil || o.screen != screenGame || !o.boss {
		return nil
	}
	row := bossRowFor(o.s.StageName)
	if row == nil || o.s.Table != row.table {
		return nil
	}
	off := addr.OffMemBit(int(row.table)) + addr.MemBitOffDungeonItem
	const bit = byte(1 << addr.DungeonItemStageBossEnemy)
	if o.raw[off]&bit != 0 {
		return nil
	}
	for _, h := range held {
		if int(h.Off) == off && h.Mask&bit != 0 && h.Hold.Why == bossHold.Why {
			return row
		}
	}
	return nil
}

// BossKillStatus is how the boss-dead relay stands.
type BossKillStatus struct {
	Wanted   bool   // this tick's plan wants a boss killed (bossKillWanted)
	Boss     string // the boss wanted, or last wanted; "" before one
	Seq      uint32 // the last request's seq, 0 for none
	Result   string // the REL's last result, "" before one
	Requests int    // requests written
	Applied  int    // results "applied"
}

// bossKill is the relay's state (Replica.bk).
type bossKill struct {
	row     *bossRow  // the boss wanted, nil while none is
	seq     uint32    // the last request written
	pending bool      // seq is written and not acked yet
	sentAt  time.Time // when seq was written
	next    time.Time // no request before this
	logged  string    // the last line's key (bossLog), so a repeat is not
	stats   BossKillStatus
}

// bossReq is a request the tick asks the writer to make once the lock is
// released.
type bossReq struct {
	seq    uint32
	fields []Field
}

// bossLog logs line unless the last line logged had the same key. Lines
// with a seq in them key on what they say without it, so a result asked
// again every bossRetry is logged once, not once per seq.
func (r *Replica) bossLog(key, line string) {
	if r.bk.logged == key {
		return
	}
	r.bk.logged = key
	r.logf("%s", line)
}

// bossAckKey is an ack line's bossLog key. Gohma: the result, the armor
// phase and Btd's action, with the fight's actions 0..2 as one (they turn
// over with every attack while an armored Gohma waits); mGohmaState is
// left out. Kalle Demos: the result, whether the core is out (m304) and
// Bmd's mMode, with the vines-up modes 0..2 as one; m302 is left out
// (its -1 after the intro and after eat included). Gohdan: the result,
// the head's mActionType with the fight's as one (STAY and FLY 0..1, and
// 10..14, the hands' attacks, which turn over all fight long, d_a_bst.h:
// 20-34), mDamage and m2E7C. Molgera: the result, Bwd's m18AE with the
// fight's (wait, reset, eat, fly and s_fly: 1, 2, 5, 10, 11, which turn
// over all fight long) as one, the intro (0) and sita_hit (3) with its
// m18B0, so a pull waiting at step 2 logs once, and m1BB5, so each
// critical pull the receiver lands logs once. Jalhalla: the result, a
// class of mBodyAction and mActionState (bpwClass) and whether health is
// 0 or less; the health itself is left out. Helmaroc King: the result, a
// class of mAction and m8F8 (bdkClass) and whether health is 0 or less.
func bossAckKey(proc uint16, st mailbox.BossStatus) string {
	switch proc {
	case mailbox.BossProcBwd:
		act := fmt.Sprintf("0x%X", st.Act)
		switch st.Act {
		case 1, 2, 5, 10, mailbox.BossBwdActSFly:
			act = "fight"
		case mailbox.BossBwdActIntro:
			act = "intro"
		case mailbox.BossBwdActTongue:
			act = fmt.Sprintf("tongue %d", int8(st.State))
		}
		return fmt.Sprintf("ack %d bwd act %s m1BB5 %d", st.Result, act, int8(st.Phase))
	case mailbox.BossProcBpw:
		return fmt.Sprintf("ack %d bpw %s hp0 %t", st.Result, bpwClass(st.Act, st.State), int8(st.Phase) <= 0)
	case mailbox.BossProcBdk:
		return fmt.Sprintf("ack %d bdk %s hp0 %t", st.Result, bdkClass(st.Act, st.State), int8(st.Phase) <= 0)
	}
	if proc == mailbox.BossProcBst {
		act := fmt.Sprintf("0x%X", st.Act)
		if st.Act <= 1 || st.Act >= 10 && st.Act <= 14 {
			act = "fight"
		}
		return fmt.Sprintf("ack %d bst act %s mDamage %d m2E7C %d", st.Result, act, int8(st.State), int8(st.Phase))
	}
	if proc == mailbox.BossProcBmd {
		mode := fmt.Sprintf("0x%X", st.Act)
		if st.Act <= 2 {
			mode = "vines"
		}
		return fmt.Sprintf("ack %d bmd core %t mode %s", st.Result, st.Phase != 0, mode)
	}
	act := fmt.Sprintf("0x%X", st.Act)
	if st.Act <= 2 {
		act = "fight"
	}
	return fmt.Sprintf("ack %d phase %d act %s", st.Result, st.Phase, act)
}

// bpwClass names a Jalhalla result's mBodyAction and mActionState (low
// bytes) for bossAckKey: the fight (actions 0..2, and the light build-up,
// 3 at 0x50..0x51, as one with it: a player standing in a lit shaft
// flips between the two every few seconds, handoff
// archive/enemy-batch-e/live/real-items/LIVE-2.md), the gate (3 at
// 0x52..0x55, 0x64, 0x65), a wall bounce (3 at 0x5A..0x5C), the split (4
// at 0x6E..0x75), the death demo (4 at 0x78 and up), the intro (0x14),
// else the values in hex.
func bpwClass(act, st uint8) string {
	switch {
	case act <= 2, act == mailbox.BossBpwActDamage && st >= 0x50 && st <= 0x51:
		return "fight"
	case act == mailbox.BossBpwActIntro:
		return "intro"
	case act == mailbox.BossBpwActDamage && (st >= mailbox.BossBpwStFrozenLo && st <= mailbox.BossBpwStThrown || st == mailbox.BossBpwStSpike || st == mailbox.BossBpwStSpikeAir):
		return "gate"
	case act == mailbox.BossBpwActDamage && st >= 0x5A && st <= 0x5C:
		return "bounce"
	case act == mailbox.BossBpwActSeparate && st >= 0x6E && st <= 0x75:
		return "split"
	case act == mailbox.BossBpwActSeparate && st >= mailbox.BossBpwStDeath:
		return "death"
	}
	return fmt.Sprintf("0x%X/0x%X", act, st)
}

// bdkClass names a Helmaroc King result's mAction (low byte) and m8F8 for
// bossAckKey: the tower phase (0x64..0x67, the climb), the intro (0xF),
// the mask fight (0..8 with m8F8 below 4, "mask k", so each hammer hit on
// the mask logs once), the mask demo (9), the crest fight (0..8 with m8F8
// 4), the death (0xA), the inert create (0x6E), else the values in hex.
func bdkClass(act, mask uint8) string {
	switch {
	case act >= mailbox.BossBdkActTFly && act <= mailbox.BossBdkActTDown:
		return "tower"
	case act == mailbox.BossBdkActStart:
		return "intro"
	case act <= mailbox.BossBdkActFightMax && mask < mailbox.BossBdkMaskBroken:
		return fmt.Sprintf("mask %d", mask)
	case act <= mailbox.BossBdkActFightMax:
		return "crest"
	case act == mailbox.BossBdkActMaskDemo:
		return "maskdemo"
	case act == mailbox.BossBdkActEnd:
		return "end"
	case act == mailbox.BossBdkActAfter:
		return "after"
	}
	return fmt.Sprintf("0x%X/%d", act, mask)
}

// bossAckLine is the log line for an ack of seq. A Jalhalla "already"
// with health 0 outside the death demo is the zero an earlier "applied"
// left, waiting for the next spike landing, and says so; Jalhalla's
// "applied" says it too, since the Hero's Charm gauge empties at the
// write: the next throw while still frozen or carried (0x52..0x54), the
// landing already under way once thrown or in the spikes (0x55, 0x64,
// 0x65). Helmaroc King's are the same: its "applied" and an "already" at
// a fight action with health 0 (the zero not yet taken) say the
// receiver's next sword hit on the crest ends it.
func bossAckLine(seq uint32, proc uint16, st mailbox.BossStatus) string {
	const bpwGauge = " (an empty boss gauge until then is expected)"
	const bpwPending = "this game's Jalhalla falls on its next throw into the spikes" + bpwGauge
	const bdkPending = "this game's Helmaroc King falls on its next sword hit on the crest" + bpwGauge
	if proc == mailbox.BossProcBdk && st.Result == mailbox.BossRAlready && st.Act <= mailbox.BossBdkActFightMax && int8(st.Phase) <= 0 {
		return fmt.Sprintf("co-op: boss_kill %d: applied earlier; %s (mAction 0x%X, m8F8 %d)", seq, bdkPending, st.Act, st.State)
	}
	if proc == mailbox.BossProcBpw && st.Result == mailbox.BossRAlready && st.Act != mailbox.BossBpwActSeparate && int8(st.Phase) <= 0 {
		return fmt.Sprintf("co-op: boss_kill %d: applied earlier; %s (mBodyAction %d, mActionState 0x%X)", seq, bpwPending, st.Act, st.State)
	}
	line := fmt.Sprintf("co-op: boss_kill %d: %s (%s)", seq, mailbox.BossResultName(st.Result), mailbox.BossDetail(proc, st))
	if proc == mailbox.BossProcBpw && st.Result == mailbox.BossRApplied {
		if st.State >= mailbox.BossBpwStThrown {
			line += "; this game's Jalhalla falls on this landing in the spikes" + bpwGauge
		} else {
			line += "; " + bpwPending
		}
	}
	if proc == mailbox.BossProcBdk && st.Result == mailbox.BossRApplied {
		line += "; " + bdkPending
	}
	return line
}

// bossRELWhy says why the REL module cannot take a request for row's
// boss, "" when it can: linked, and of this build (the gate enemysync's
// checkREL keeps).
func bossRELWhy(m mailbox.Reader, row *bossRow) string {
	rel, err := mailbox.ReadREL(m)
	switch {
	case err != nil || !rel.Linked:
		return fmt.Sprintf("co-op: boss_kill: the REL module is not linked; the arena keeps %s until it is (or this game leaves it)", row.name)
	case rel.BuildID != inject.RELBuildID:
		return fmt.Sprintf("co-op: boss_kill: the linked REL module is build %08X, this build's is %08X; the arena keeps %s until the disc's REL matches (or this game leaves it)", rel.BuildID, inject.RELBuildID, row.name)
	}
	return ""
}

// bossTick runs the relay for one tick (under mu), after the plan: it
// reads the REL's ack, and when a request is due sets r.bossNow.
func (r *Replica) bossTick(o *obs, armed bool) {
	b := &r.bk
	now := r.now()
	row := bossKillWanted(o, armed, r.held)
	if row != nil && !r.bossOn[row.proc] {
		// A row --coop-boss-kill leaves out: the arena keeps its boss.
		line := fmt.Sprintf("co-op: the partner killed %s, but --coop-boss-kill (%s) leaves it out: this game's arena keeps its %s until it is killed here (or this game leaves it)", row.name, r.cfg.bossKillSet(), row.name)
		r.bossLog(line, line)
		row = nil
	}
	if row != b.row {
		// Another boss, or none: nothing asked of the last one is
		// waited on. A boss wanted from none is asked at once, as before.
		if b.row != nil {
			b.pending, b.next = false, time.Time{}
		}
		b.row = row
		if row != nil {
			b.stats.Boss = row.name
			line := fmt.Sprintf("co-op: the partner killed %s and this game is in its arena: asking the REL to kill this game's %s %s (boss_kill)", row.name, row.name, row.gate)
			r.bossLog(line, line)
		}
	}
	if row == nil {
		return
	}
	if r.writer.DryRun() {
		line := "co-op: dry run: boss_kill would be asked for (nothing is written)"
		r.bossLog(line, line)
		return
	}
	st, err := mailbox.ReadBoss(r.m)
	if err != nil {
		line := "co-op: boss_kill: " + err.Error()
		r.bossLog(line, line)
		return
	}
	if b.pending {
		switch {
		case st.Ack == b.seq:
			b.pending = false
			b.stats.Result = mailbox.BossResultName(st.Result)
			r.bossLog(bossAckKey(row.proc, st), bossAckLine(b.seq, row.proc, st))
			switch st.Result {
			case mailbox.BossRApplied, mailbox.BossRAlready:
				if st.Result == mailbox.BossRApplied {
					b.stats.Applied++
				}
				b.next = now.Add(bossSettle)
			default:
				b.next = now.Add(bossRetry)
			}
		case now.Sub(b.sentAt) >= bossAckWait:
			b.pending = false
			b.next = now.Add(bossRetry)
			if why := bossRELWhy(r.m, row); why != "" {
				r.bossLog(why, why)
			} else {
				r.bossLog("not acked", fmt.Sprintf("co-op: boss_kill %d: not acked after %s; asking again", b.seq, bossAckWait))
			}
		}
		return
	}
	if now.Before(b.next) {
		return
	}
	if why := bossRELWhy(r.m, row); why != "" {
		b.next = now.Add(bossRetry)
		r.bossLog(why, why)
		return
	}
	seq := max(st.Seq, st.Ack, b.seq) + 1
	if seq == 0 {
		seq = 1
	}
	procTag, seqBytes := mailbox.BossRequestFields(row.proc, seq)
	r.bossNow = &bossReq{seq: seq, fields: []Field{
		{Addr: mailbox.BossAddr + mailbox.BossProcOff, Data: procTag},
		{Addr: mailbox.BossAddr + mailbox.BossSeqOff, Data: seqBytes},
	}}
}

// bossSent records the writer's answer to q (under mu).
func (r *Replica) bossSent(q *bossReq, err error, rep *Report) {
	b := &r.bk
	now := r.now()
	rep.BossKill, rep.BossKillErr = q.seq, err
	if err != nil {
		b.next = now.Add(bossRetry)
		if !errors.Is(err, ErrClosed) {
			line := "co-op: boss_kill not written: " + err.Error()
			r.bossLog(line, line)
		}
		return
	}
	b.seq, b.pending, b.sentAt = q.seq, true, now
	b.stats.Requests++
	b.stats.Seq = q.seq
}

// applyBossKill makes the tick's boss_kill request, if any, once mu is
// released.
func (r *Replica) applyBossKill(ctx context.Context, q *bossReq, rep *Report) {
	if q == nil {
		return
	}
	_, err := r.writer.ApplyBossKill(ctx, q.fields)
	r.mu.Lock()
	r.bossSent(q, err, rep)
	r.mu.Unlock()
}

// bossKillStatus is the relay's status (under mu).
func (r *Replica) bossKillStatus() BossKillStatus {
	st := r.bk.stats
	st.Wanted = r.bk.row != nil
	return st
}
