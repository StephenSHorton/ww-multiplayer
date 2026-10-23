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
// Gohdan (enemy-mirror-batch-d.json R-D1..R-D3, "gohdan") is the third
// row: in SirenB with Tower of the Gods' table loaded, the REL gives this
// game's own Bst bom_eat_check's lethal block (d_a_bst.cpp:1219-1229),
// but only from vanilla's own pre-death state: the head in HEAD_DAMAGE
// with its mouth open for a bomb (mDamage 3, m2E7C 12, m2E9A 0) and both
// hands asleep. The receiver shoots out the eyes itself, as it breaks
// Gohma's armor; the intro, the hands' fight and the eye phase answer
// "waiting". Its demo (demo_camera case 50-57) then sets the bit, spawns
// the warp flower and drops the heart (d_a_bst.cpp:2429-2436,
// 2504-2510). The --coop-boss-kill row set (Config.BossKill) picks the
// rows the relay asks for, gohma,kalle by default: gohdan joins it once
// GD-2 and the D4 live check pass (docs/TESTING.md "Batch D").

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
}

// BossKillDefault is the --coop-boss-kill row set when none is given:
// Gohma and Kalle Demos. Gohdan joins it after GD-2 and the D4 live check
// (enemy-mirror-batch-d.json G-D1).
const BossKillDefault = "gohma,kalle"

// BossKillTokens names every --coop-boss-kill token, for the usage.
const BossKillTokens = "gohma,kalle,gohdan"

// ParseBossKill parses a --coop-boss-kill row set: bossKillRows' keys
// joined by ",", "none" for no row, "" for BossKillDefault. It returns the
// rows' procs.
func ParseBossKill(v string) (map[uint16]bool, error) {
	if v == "" {
		v = BossKillDefault
	}
	on := map[uint16]bool{}
	if strings.EqualFold(v, "none") {
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

// bossAckKey is an ack line's bossLog key. Gohdan: the result, the
// head's mActionType with the fight's actions as one (STAY and FLY, and
// the hands' attacks 10..14, which turn over all fight long), mDamage and
// m2E7C. Gohma: the result, the armor
// phase and Btd's action, with the fight's actions 0..2 as one (they turn
// over with every attack while an armored Gohma waits); mGohmaState is
// left out. Kalle Demos: the result, whether the core is out (m304) and
// Bmd's mMode, with the vines-up modes 0..2 as one; m302 is left out
// (its -1 after the intro and after eat included).
func bossAckKey(proc uint16, st mailbox.BossStatus) string {
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
		line := fmt.Sprintf("co-op: the partner killed %s, but --coop-boss-kill leaves it out (%s): this game's arena keeps its %s until it is killed here", row.name, r.cfg.bossKillSet(), row.name)
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
			r.bossLog(bossAckKey(row.proc, st), fmt.Sprintf("co-op: boss_kill %d: %s (%s)", b.seq, b.stats.Result, mailbox.BossDetail(row.proc, st)))
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
