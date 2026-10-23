package replica

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/inject"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// The actor_refresh relay (ww-multiplayer-handoff/design/
// enemy-mirror-batch-d.json R-D4, R-D5, G-D2..G-D5, "actor_refresh";
// research/stale-actor-refresh.md Tier 3). Tiers 1 and 2 (refresh.go)
// write an actor's own bytes; Tier 3 is for the actors whose create alone
// reads their bit, where no byte of the live actor would make it right: a
// boulder (Stone2), a bush (Obj_Leaves), a pickup (daItem), a chest (Tbox)
// and a barricade (Saku) this game placed before the partner's bit landed
// here stays until its room reloads. The relay asks the REL to delete it
// (wwapi/mailbox refresh.go, inject/rel/refresh.c), and a reload then
// never creates it, as vanilla.
//
// It acts only on an actor whose bit is in the landed set (landed.go: the
// bits this replica's own verified writes set, never one that merely
// reads set), so the local player's own break, cut, pickup or chest never
// gets a request. The decoder (refreshDecode) derives each actor's bit
// from its params or fields with the REL's formulas (mailbox.Refresh*,
// pinned to inject/include/wwrefresh.h), so a candidate the Go side gets
// wrong costs a refusal, never a wrong delete: the REL re-derives and
// re-reads the bit and runs every gate again (the design's safety rule).
// Never candidates: a Tbox of funcType 6..8, a daItem with no bit (0x7F)
// or a Blue Jelly, a zone or room switch with no home room, a pickup bit
// the landed set cannot hold (32..63 alias mVisitedRoom[0], 0x40.. are a
// zone's), and a Saku unless every half with a switch landed and reads
// set (D7: v1 deletes only the whole barricade).
//
// The Go-side gates (G-D4), each judged by the tick that plans the
// candidate: co-op armed, the hub granting FeatureCombat, not a dry run
// (the M6 receiver's m6Granted, which plans it); IDLE; not a boss stage
// (bossStage); the bit landed at least RefreshSettle ago and set now; the
// home room loaded and settled; the actor executing, in no event, not
// carried, no mirror pause bit, not claimed by the enemy mirror, never
// light-shrunk by M6 (ever); Link more than 300 units away and in no
// demo. The REL checks its own (D4) besides.
//
// The relay copies bossTick (bosskill.go): one request in flight, written
// body first and the seq last (mailbox.RefreshRequest.Fields) through the
// WriteGuard (TargetRefresh, pathRefresh); a refreshAckWait for the ack;
// a refreshRetry before the same actor is asked again after "gated", "no
// actor" or "none"; nothing asked of a REL module of another build; each
// actor applied at most once, and given up after a final answer ("not
// set", "mismatch", "refused", "fingerprint miss") until the stage visit
// or a save state ends; and each log line once per key. Off by default:
// the --coop-refresh token set (Config.Refresh) is empty. Its "dry" token
// logs one line per candidate and writes nothing.

// refreshProc is an allow-listed proc the relay may ask about.
type refreshProc struct {
	key  string // its --coop-refresh token
	proc uint16 // mailbox.RefreshProc*
}

// refreshProcs is the allow-list (wwrefresh.h WWRF_PROC_*), in the order
// of the design's cut_order backwards: a cut drops the last first.
var refreshProcs = []refreshProc{
	{"stone2", mailbox.RefreshProcStone2},
	{"leaves", mailbox.RefreshProcLeaves},
	{"item", mailbox.RefreshProcItem},
	{"saku", mailbox.RefreshProcSaku},
	{"tbox", mailbox.RefreshProcTbox},
}

// RefreshTokens names every --coop-refresh token, for the usage.
const RefreshTokens = "stone2,leaves,item,tbox,saku"

// refreshDry is Config.Refresh's dry-run token: candidates are logged, no
// request is written.
const refreshDry = "dry"

// refreshDryProc is ParseRefresh's key for refreshDry: no proc is 0.
const refreshDryProc uint16 = 0

// ParseRefresh parses a --coop-refresh token set: refreshProcs' keys
// joined by ",", with "dry" to log the candidates only; "" or "none" is
// none (the default). It returns the procs (and refreshDryProc for dry).
func ParseRefresh(v string) (map[uint16]bool, error) {
	on := map[uint16]bool{}
	if v == "" || strings.EqualFold(v, "none") {
		return on, nil
	}
	for tok := range strings.SplitSeq(v, ",") {
		tok = strings.ToLower(strings.TrimSpace(tok))
		if tok == refreshDry {
			on[refreshDryProc] = true
			continue
		}
		i := slices.IndexFunc(refreshProcs, func(p refreshProc) bool { return p.key == tok })
		if i < 0 {
			return nil, fmt.Errorf("--coop-refresh: %q is no refresh token (say %s, dry, or none)", tok, RefreshTokens)
		}
		on[refreshProcs[i].proc] = true
	}
	return on, nil
}

const (
	// refreshRetry is the wait before an actor is asked again after a
	// result that asks for it, or a write that did not go: bossRetry's.
	refreshRetry = bossRetry
	// refreshAckWait is how long a request may go unacked (the keeper
	// runs every frame) before it is given up and asked again.
	refreshAckWait = bossAckWait
	// maxRefreshKeys bounds what the relay remembers per stage visit: the
	// actors done and waiting, and the log keys.
	maxRefreshKeys = actors.MaxNodes
	// landedItems is how many pickup bits the landed set takes (landed.go:
	// mItem's one word; isItem reads 32..63 from mVisitedRoom[0] and 0x40..
	// from a zone, d_save.cpp:1650-1668).
	landedItems = 32
)

// refreshCand is an actor the relay may name in a request: its bit as the
// REL derives it, and the landed bits it waits on.
type refreshCand struct {
	a       actors.Actor
	bitKind uint8  // mailbox.RefreshBit*
	bitNo   uint16 // the bit the request names (Saku: sw1)
	keys    []LandedBit
}

func (c *refreshCand) String() string {
	return fmt.Sprintf("%s %s", mailbox.RefreshProcName(uint16(c.a.Proc)), c.a.String())
}

// refreshDecode derives a's bit as the REL does (wwrefresh.h, the
// mailbox.Refresh* formulas) while table is loaded: ok is false when a is
// no candidate at all (a proc off the allow-list, a kind this op never
// deletes, no bit the landed set can hold). daItem's bit and item number
// are read from the actor.
func refreshDecode(m mem.Reader, a *actors.Actor, table int8) (c refreshCand, ok bool, err error) {
	c.a = *a
	sw := func(no int) (LandedBit, bool) {
		switch st := addr.ClassifySwitch(no); {
		case st == addr.SwitchNone || st == addr.SwitchInvalid:
			return LandedBit{}, false
		case (st == addr.SwitchZone || st == addr.SwitchRoom) && a.HomeRoom < 0:
			return LandedBit{}, false // the REL refuses a zone bit with no room
		}
		return SwitchKey(table, int(a.HomeRoom), no), true
	}
	p := a.Params
	switch uint16(a.Proc) {
	case mailbox.RefreshProcStone2, mailbox.RefreshProcLeaves:
		no := int(p>>mailbox.RefreshStone2SwShift) & mailbox.RefreshStone2SwMask
		if uint16(a.Proc) == mailbox.RefreshProcLeaves {
			no = int(p>>mailbox.RefreshLeavesSwShift) & mailbox.RefreshLeavesSwMask
		}
		k, ok := sw(no)
		if !ok {
			return c, false, nil
		}
		c.bitKind, c.bitNo, c.keys = mailbox.RefreshBitSw, uint16(no), []LandedBit{k}
	case mailbox.RefreshProcItem:
		bitNo, err := mem.ReadU32(m, a.Addr+mailbox.RefreshItemBitNoOff)
		if err != nil {
			return c, false, err
		}
		itemNo, err := mem.ReadU8(m, a.Addr+mailbox.RefreshItemNoOff)
		if err != nil {
			return c, false, err
		}
		no := int(bitNo) & mailbox.RefreshItemBitNoMask
		if no == mailbox.RefreshItemBitNone || itemNo == mailbox.RefreshItemNoBlueJelly || no >= landedItems {
			// No bit, a Blue Jelly (REFUSED), or a bit the landed set
			// takes no write of (landed.go: 32..63, and a zone's 0x40..).
			return c, false, nil
		}
		c.bitKind, c.bitNo = mailbox.RefreshBitItem, uint16(no)
		c.keys = []LandedBit{{Kind: LandItem, Table: table, Room: -1, No: no}}
	case mailbox.RefreshProcTbox:
		ft := int(p & mailbox.RefreshTboxFuncMask)
		if ft >= mailbox.RefreshTboxFuncRefuseLo && ft <= mailbox.RefreshTboxFuncRefuseHi {
			return c, false, nil
		}
		no := int(p>>mailbox.RefreshTboxBitShift) & mailbox.RefreshTboxBitMask
		c.bitKind, c.bitNo = mailbox.RefreshBitTbox, uint16(no)
		c.keys = []LandedBit{{Kind: LandTbox, Table: table, Room: -1, No: no}}
	case mailbox.RefreshProcSaku:
		sw1 := int(p>>mailbox.RefreshSakuSw1Shift) & mailbox.RefreshSakuSwMask
		halves := []int{sw1}
		if p&mailbox.RefreshSakuHalf1Mask != 0 {
			halves = append(halves, int(p>>mailbox.RefreshSakuSw2Shift)&mailbox.RefreshSakuSwMask)
		}
		for _, no := range halves {
			if addr.ClassifySwitch(no) == addr.SwitchNone {
				continue // a half with no switch of its own
			}
			k, ok := sw(no)
			if !ok {
				return c, false, nil
			}
			c.keys = append(c.keys, k)
		}
		if len(c.keys) == 0 {
			return c, false, nil
		}
		c.bitKind, c.bitNo = mailbox.RefreshBitSw, uint16(sw1)
	default:
		return c, false, nil
	}
	return c, true, nil
}

// bitSetIn reads landed bit b in t's snapshot, as the actor of room reads
// it: read is false when its store has no byte here.
func (t *refreshTick) bitSetIn(b LandedBit, room int) (set, read bool) {
	var bit addr.Bit
	var err error
	switch b.Kind {
	case LandSwitch:
		return switchSetIn(t.s, b.No, room)
	case LandTbox:
		bit, err = addr.TboxBit(b.No)
	case LandItem:
		bit, err = addr.MemBitItemBit(b.No)
	default:
		return false, false
	}
	if err != nil {
		return false, false
	}
	v, ok := snapByte(t.s, addr.LiveMemBit+uint32(bit.Off))
	return ok && v&bit.Mask != 0, ok
}

// landedWhy judges landed bit b as an actor of room reads it, as
// switchWhy judges a switch: cand is false when it never landed this
// visit; else why is "" when the relay may ask about it, or why not.
func (t *refreshTick) landedWhy(b LandedBit, room int) (why string, cand bool) {
	at, ok := t.landed.Has(b)
	if !ok || t.landed.Stage != t.s.StageName {
		return "", false
	}
	switch set, read := t.bitSetIn(b, room); {
	case t.settle > 0 && t.now.Sub(at) < t.settle:
		return fmt.Sprintf("%s landed %v ago, settling for %v", b, t.now.Sub(at).Round(time.Millisecond), t.settle), true
	case !read:
		return fmt.Sprintf("%s has no store here", b), true
	case !set:
		return fmt.Sprintf("%s reads clear now", b), true
	case t.boss:
		return "a boss stage (bossStage)", true
	}
	return "", true
}

// anyRefresh reports whether some bit of a kind the procs on ask about
// landed for the loaded table: the M6 receiver reads the walk for it.
func (t *refreshTick) anyRefresh(on map[uint16]bool) bool {
	if len(on) == 0 || len(on) == 1 && on[refreshDryProc] {
		return false
	}
	for b := range t.landed.At {
		if b.Table != t.s.Table {
			continue
		}
		switch b.Kind {
		case LandSwitch:
			if on[mailbox.RefreshProcStone2] || on[mailbox.RefreshProcLeaves] || on[mailbox.RefreshProcSaku] {
				return true
			}
		case LandTbox:
			if on[mailbox.RefreshProcTbox] {
				return true
			}
		case LandItem:
			if on[mailbox.RefreshProcItem] {
				return true
			}
		}
	}
	return false
}

// refreshEligible returns "" if a may be named in a request, by what was
// read of it, or why not: a class the refresh takes is no K1 enemy, so
// combat.Eligible is not asked (lockEligible's terms for an allow-listed
// proc, and never one M6 light-shrank).
func refreshEligible(a *actors.Actor, claimed func(pid uint32) bool, ever map[uint32]bool) string {
	switch {
	case a.InitState != mailbox.RefreshAcInitRunning:
		return fmt.Sprintf("mInitState %d: not executing", a.InitState)
	case a.EvtCmd != 0:
		return fmt.Sprintf("an event ordered it (mCommand %d)", a.EvtCmd)
	case a.Carried():
		return "CARRY status"
	case a.Pause != 0:
		return fmt.Sprintf("paused (mPauseFlag 0x%02X)", a.Pause)
	case claimed != nil && claimed(a.PID):
		return "claimed by the enemy mirror"
	case ever[a.PID]:
		return "light-shrunk by M6"
	}
	return ""
}

// planRefresh plans this tick's actor_refresh candidates from walk w,
// Link's demo gate being demo (why demoWhy when closed), into r.ar.cands,
// sorted by pid. r.mu is held.
func (r *Replica) planRefresh(t *refreshTick, w *actors.Walk, demo bool, demoWhy string) {
	f := &r.ar
	f.cands = f.cands[:0]
	if len(r.rfOn) == 0 {
		return
	}
	for i := range w.Actors {
		a := &w.Actors[i]
		if !r.rfOn[uint16(a.Proc)] || f.done[a.PID] != "" {
			continue
		}
		c, ok, err := refreshDecode(r.m, a, t.s.Table)
		if err != nil {
			f.hold(a.PID, "its fields are unreadable: "+err.Error())
			continue
		}
		if !ok {
			continue
		}
		why, cand := "", true
		for _, k := range c.keys {
			kw, kc := t.landedWhy(k, int(a.HomeRoom))
			if !kc {
				cand = false
				break
			}
			if why == "" {
				why = kw
			}
		}
		if !cand {
			// Some bit never landed: the local player's own, or the
			// partner's not yet (Saku: every half must).
			continue
		}
		if why == "" {
			why = t.roomSettled(int(a.HomeRoom))
		}
		if why == "" {
			why = refreshEligible(a, r.cfg.Claimed, r.m6.ever)
		}
		if why == "" {
			why = t.linkFar(a.Pos)
		}
		if why == "" && !demo {
			why = demoWhy
		}
		if why != "" {
			f.hold(a.PID, why)
			continue
		}
		delete(f.held, a.PID)
		f.cands = append(f.cands, c)
	}
	slices.SortFunc(f.cands, func(x, y refreshCand) int { return int(x.a.PID) - int(y.a.PID) })
}

// actorRefresh is the relay's state (Replica.ar).
type actorRefresh struct {
	cands []refreshCand // this tick's candidates (planRefresh)

	visit   string               // the landed set's visit the rest is of
	pending *refreshCand         // the request in flight, nil for none
	seq     uint32               // the last request written
	sentAt  time.Time            // when seq was written
	next    map[uint32]time.Time // no request for the pid before this
	done    map[uint32]string    // pids answered for good, with the answer
	held    map[uint32]string    // candidates held, with the last reason
	last    string               // the last hold's reason
	dry     map[uint32]bool      // pids a dry run logged
	logged  map[string]bool      // log keys said this visit
	stats   ActorRefreshStatus
}

func newActorRefresh() actorRefresh {
	return actorRefresh{next: map[uint32]time.Time{}, done: map[uint32]string{}, held: map[uint32]string{}, dry: map[uint32]bool{}, logged: map[string]bool{}}
}

// forget drops what the relay remembers of process IDs and the visit: a
// save state rewound the counter, or another stage visit began.
func (f *actorRefresh) forget() {
	f.pending = nil
	clear(f.next)
	clear(f.done)
	clear(f.held)
	clear(f.dry)
	clear(f.logged)
	f.last = ""
}

func (f *actorRefresh) hold(pid uint32, why string) {
	if len(f.held) >= maxRefreshKeys {
		clear(f.held)
	}
	f.held[pid] = why
	f.last = why
}

// ActorRefreshStatus is how the actor_refresh relay stands (Status.Refresh
// .Actors), for /status.
type ActorRefreshStatus struct {
	// On is the --coop-refresh token set, "" while it is off; Dry when it
	// only logs.
	On  string
	Dry bool
	// Candidates are this tick's; Held the candidates held, LastHeld the
	// last reason.
	Candidates, Held int
	LastHeld         string
	// Seq is the last request's, Result the REL's last answer ("" before
	// one), Requests the requests written, Applied the deletes the REL
	// queued.
	Seq      uint32
	Result   string
	Requests int
	Applied  int
}

// refreshReq is a request the tick asks the writer to make once the lock
// is released.
type refreshReq struct {
	seq    uint32
	cand   refreshCand
	fields []Field
}

// arLog logs line once per stage visit for key.
func (r *Replica) arLog(key, line string) {
	f := &r.ar
	if f.logged[key] {
		return
	}
	if len(f.logged) >= maxRefreshKeys {
		clear(f.logged)
	}
	f.logged[key] = true
	r.logf("%s", line)
}

// refreshRELWhy says why the REL module cannot take an actor_refresh
// request, "" when it can: linked, and of this build (bossRELWhy's gate).
func refreshRELWhy(m mailbox.Reader) string {
	rel, err := mailbox.ReadREL(m)
	switch {
	case err != nil || !rel.Linked:
		return "co-op: actor_refresh: the REL module is not linked; stale actors stay until their room reloads"
	case rel.BuildID != inject.RELBuildID:
		return fmt.Sprintf("co-op: actor_refresh: the linked REL module is build %08X, this build's is %08X; stale actors stay until their room reloads", rel.BuildID, inject.RELBuildID)
	}
	return ""
}

// refreshFinal reports whether result r ends the asking for its actor.
func refreshFinal(r uint8) bool {
	switch r {
	case mailbox.RefreshRApplied, mailbox.RefreshRNotSet, mailbox.RefreshRMismatch, mailbox.RefreshRRefused, mailbox.RefreshRFpMiss:
		return true
	}
	return false
}

// refreshTick runs the relay for one tick (under mu), after the M6
// receiver planned the candidates: it reads the REL's ack, and when a
// request is due sets r.arNow.
func (r *Replica) refreshTick(o *obs, armed bool) {
	f := &r.ar
	if len(r.rfOn) == 0 {
		return
	}
	now := r.now()
	l := r.writer.Landed()
	if v := fmt.Sprintf("%s/%d", l.Stage, l.Table); v != f.visit {
		if f.pending != nil {
			r.logf("co-op: actor_refresh %d: the stage visit ended before its ack; dropped", f.seq)
		}
		f.visit = v
		f.forget()
	}
	if f.pending == nil && len(f.cands) == 0 {
		return
	}
	if r.rfOn[refreshDryProc] {
		for _, c := range f.cands {
			if !f.dry[c.a.PID] && len(f.dry) < maxRefreshKeys {
				f.dry[c.a.PID] = true
				r.logf("co-op: actor_refresh dry run: would ask the REL to delete the stale %s (bit kind %d, bit 0x%02X); nothing is written", c.String(), c.bitKind, c.bitNo)
			}
		}
		return
	}
	if !armed || r.writer.DryRun() {
		f.pending = nil
		return
	}
	st, err := mailbox.ReadRefresh(r.m)
	if err != nil {
		r.arLog("read", "co-op: actor_refresh: "+err.Error())
		return
	}
	if c := f.pending; c != nil {
		switch {
		case st.Ack == f.seq:
			f.pending = nil
			f.stats.Result = mailbox.RefreshResultName(st.Result)
			detail := fmt.Sprintf("state 0x%04X", st.ProcState)
			if st.Result == mailbox.RefreshRGate {
				detail = fmt.Sprintf("%s, %s", mailbox.RefreshGateName(st.Gate), detail)
			}
			r.arLog(fmt.Sprintf("ack pid %d result %d gate %d", c.a.PID, st.Result, st.Gate),
				fmt.Sprintf("co-op: actor_refresh %d: %s for the stale %s (%s)", f.seq, f.stats.Result, c.String(), detail))
			switch {
			case refreshFinal(st.Result):
				if st.Result == mailbox.RefreshRApplied {
					f.stats.Applied++
				}
				if len(f.done) >= maxRefreshKeys {
					clear(f.done)
				}
				f.done[c.a.PID] = f.stats.Result
				delete(f.held, c.a.PID)
			default:
				f.next[c.a.PID] = now.Add(refreshRetry)
			}
		case now.Sub(f.sentAt) >= refreshAckWait:
			f.pending = nil
			f.next[c.a.PID] = now.Add(refreshRetry)
			if why := refreshRELWhy(r.m); why != "" {
				r.arLog(why, why)
			} else {
				r.arLog("not acked", fmt.Sprintf("co-op: actor_refresh %d: not acked after %s; asking again", f.seq, refreshAckWait))
			}
		}
		return
	}
	var pick *refreshCand
	for i := range f.cands {
		c := &f.cands[i]
		if f.done[c.a.PID] == "" && !now.Before(f.next[c.a.PID]) {
			pick = c
			break
		}
	}
	if pick == nil {
		return
	}
	if why := refreshRELWhy(r.m); why != "" {
		f.next[pick.a.PID] = now.Add(refreshRetry)
		r.arLog(why, why)
		return
	}
	// The actor read again just before it is named: the same pid, proc,
	// setID, home room and params as the walk's (the REL compares them
	// once more).
	typ, err := mem.ReadU32(r.m, addr.FopAcType)
	if err != nil {
		return
	}
	if _, err := actors.RereadParams(r.m, &pick.a, typ, nil); err != nil {
		f.next[pick.a.PID] = now.Add(refreshRetry)
		f.hold(pick.a.PID, err.Error())
		return
	}
	seq := max(st.Seq, st.Ack, f.seq) + 1
	if seq == 0 {
		seq = 1
	}
	req := mailbox.RefreshRequest{Seq: seq, Proc: uint16(pick.a.Proc), PID: pick.a.PID, Params: pick.a.Params, Room: pick.a.HomeRoom,
		BitKind: pick.bitKind, BitNo: pick.bitNo}
	body, seqBytes := req.Fields()
	r.arNow = &refreshReq{seq: seq, cand: *pick, fields: []Field{
		{Addr: mailbox.RefreshAddr + mailbox.RefreshProcOff, Data: body},
		{Addr: mailbox.RefreshAddr + mailbox.RefreshSeqOff, Data: seqBytes},
	}}
}

// refreshSent records the writer's answer to q (under mu).
func (r *Replica) refreshSent(q *refreshReq, err error, rep *Report) {
	f := &r.ar
	now := r.now()
	rep.Refresh, rep.RefreshErr = q.seq, err
	if err != nil {
		f.next[q.cand.a.PID] = now.Add(refreshRetry)
		if !errors.Is(err, ErrClosed) {
			line := "co-op: actor_refresh not written: " + err.Error()
			r.arLog(line, line)
		}
		return
	}
	c := q.cand
	f.seq, f.pending, f.sentAt = q.seq, &c, now
	f.stats.Requests++
	f.stats.Seq = q.seq
	r.arLog(fmt.Sprintf("ask pid %d", c.a.PID), fmt.Sprintf("co-op: the partner's bit landed: asking the REL to delete the stale %s (actor_refresh %d)", c.String(), q.seq))
}

// applyRefresh makes the tick's actor_refresh request, if any, once mu is
// released.
func (r *Replica) applyRefresh(ctx context.Context, q *refreshReq, rep *Report) {
	if q == nil {
		return
	}
	_, err := r.writer.ApplyRefresh(ctx, q.fields)
	r.mu.Lock()
	r.refreshSent(q, err, rep)
	r.mu.Unlock()
}

// actorRefreshStatus is the relay's status (under mu).
func (r *Replica) actorRefreshStatus() ActorRefreshStatus {
	st := r.ar.stats
	if len(r.rfOn) > 0 {
		st.On, st.Dry = r.cfg.Refresh, r.rfOn[refreshDryProc]
	}
	st.Candidates, st.Held, st.LastHeld = len(r.ar.cands), len(r.ar.held), r.ar.last
	return st
}
