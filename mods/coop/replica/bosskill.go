package replica

import (
	"context"
	"errors"
	"fmt"
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
// game in M_DragB with DRC loaded, and the plan holding DRC's
// STAGE_BOSS_ENEMY under bossHold, which the planner does only while the
// world has the bit and the local save lacks it. So it never fires in the
// X refight stages (another stage name, and the REL refuses any name but
// M_DragB too), and it stops once the local save has the bit. The REL
// acks every request with a result: "waiting" (Gohma hidden, in its
// intro or still armored), "no actor" (Btd not created yet), "none"
// (swallowed by a keeper relink) and a closed gate (the receiver's own demo
// makes the write wait for IDLE) are asked again every bossRetry; "applied"
// and "already" are not asked again for bossSettle, while the death demo
// runs to the bit. Nothing is asked of a REL module of another build than
// this one's (inject.RELBuildID), which may not have the op at all.

// bossKillStage is the stage the relay works in, and the only one the REL
// accepts (bosskill.c).
const bossKillStage = "M_DragB"

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

// bossKillWanted reports whether this tick should have the REL kill this
// game's Gohma: the file armed, in game, in M_DragB with DRC's table
// loaded, and the plan holding DRC's STAGE_BOSS_ENEMY under bossHold, with
// the local save lacking it.
func bossKillWanted(o *obs, armed bool, held []HeldBit) bool {
	if !armed || o == nil || o.s == nil || o.screen != screenGame || !o.boss ||
		o.s.StageName != bossKillStage || o.s.Table != addr.TableDRC {
		return false
	}
	off := addr.OffMemBit(addr.TableDRC) + addr.MemBitOffDungeonItem
	const bit = byte(1 << addr.DungeonItemStageBossEnemy)
	if o.raw[off]&bit != 0 {
		return false
	}
	for _, h := range held {
		if int(h.Off) == off && h.Mask&bit != 0 && h.Hold.Why == bossHold.Why {
			return true
		}
	}
	return false
}

// BossKillStatus is how the boss-dead relay stands.
type BossKillStatus struct {
	Wanted   bool   // this tick's plan wants Gohma killed (bossKillWanted)
	Seq      uint32 // the last request's seq, 0 for none
	Result   string // the REL's last result, "" before one
	Requests int    // requests written
	Applied  int    // results "applied"
}

// bossKill is the relay's state (Replica.bk).
type bossKill struct {
	wanted  bool
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

// bossAckKey is an ack line's bossLog key: the result, the armor phase and
// Btd's action, with the fight's actions 0..2 as one (they turn over with
// every attack while an armored Gohma waits). mGohmaState is left out.
func bossAckKey(st mailbox.BossStatus) string {
	act := fmt.Sprintf("0x%X", st.Act)
	if st.Act <= 2 {
		act = "fight"
	}
	return fmt.Sprintf("ack %d phase %d act %s", st.Result, st.Phase, act)
}

// bossRELWhy says why the REL module cannot take a request, "" when it can:
// linked, and of this build (the gate enemysync's checkREL keeps).
func bossRELWhy(m mailbox.Reader) string {
	rel, err := mailbox.ReadREL(m)
	switch {
	case err != nil || !rel.Linked:
		return "co-op: boss_kill: the REL module is not linked; the arena keeps Gohma until it is (or this game leaves it)"
	case rel.BuildID != inject.RELBuildID:
		return fmt.Sprintf("co-op: boss_kill: the linked REL module is build %08X, this build's is %08X; the arena keeps Gohma until the disc's REL matches (or this game leaves it)", rel.BuildID, inject.RELBuildID)
	}
	return ""
}

// bossTick runs the relay for one tick (under mu), after the plan: it
// reads the REL's ack, and when a request is due sets r.bossNow.
func (r *Replica) bossTick(o *obs, armed bool) {
	b := &r.bk
	now := r.now()
	want := bossKillWanted(o, armed, r.held)
	if want != b.wanted {
		b.wanted = want
		if want {
			line := "co-op: the partner killed Gohma and this game is in its arena: asking the REL to kill this game's Gohma once its armor is broken (boss_kill)"
			r.bossLog(line, line)
		} else {
			b.pending, b.next = false, time.Time{}
		}
	}
	if !want {
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
			r.bossLog(bossAckKey(st), fmt.Sprintf("co-op: boss_kill %d: %s (m02E4 0x%X, state 0x%X, m02E0 %d)", b.seq, b.stats.Result, st.Act, st.State, st.Phase))
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
			if why := bossRELWhy(r.m); why != "" {
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
	if why := bossRELWhy(r.m); why != "" {
		b.next = now.Add(bossRetry)
		r.bossLog(why, why)
		return
	}
	seq := max(st.Seq, st.Ack, b.seq) + 1
	if seq == 0 {
		seq = 1
	}
	procTag, seqBytes := mailbox.BossRequestFields(mailbox.BossProcBtd, seq)
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
	st.Wanted = r.bk.wanted
	return st
}
