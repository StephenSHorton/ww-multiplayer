package replica

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/proto"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/actors"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mailbox"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
	linkpos "github.com/StephenSHorton/ww-multiplayer/wwapi/player"
)

// Chest claims (M9, docs/08 §6.7.1): the replica's half.
//
// A closed chest offers its "Open" prompt from actionOpenWait every frame
// Link stands within 100 units facing it (boxCheck, d_a_tbox.cpp:553-564,
// :985-994) and never reads its bit again there, so two players at one
// chest both loot it. While a partner is in scope (the same stage and
// table, the same room or a room of -1), the replica holds each closed
// chest the walk has: it swaps the chest's action word (mActionFunc's
// function word, +0x348) from actionOpenWait (.text +0x2634) to actionWait
// (.text +0x24AC), the open chest's state, a bare return TRUE
// (d_a_tbox.cpp:909-911). A held chest draws and collides as before but
// offers no prompt. Both words are inside d_a_tbox.rel (its base
// daTbox_Execute - 0x2FB0, behind the refresh REL's fingerprint), so the
// word never points outside the chest's own module.
//
// When Link comes within ChestClaimRange of a held chest the replica asks
// the hub (CLAIM); a GRANT restores actionOpenWait and the prompt comes
// back before Link reaches 100 units. The hub denies a chest someone else
// holds, and says CONSUMED once the world has its bit: such a chest is
// never restored (it stays closed and gives nothing until its room
// reloads, when its create finds the bit and stands it open).
//
// Gates, each read again just before the write (FrameWriter.ApplyChests):
//
//   - hold: armed, IDLE, chest claims granted, not a dry run; the word is
//     actionOpenWait and mFlags has none of UNK_08, OPENING, APPEARING;
//     the chest's bit reads clear; Link farther than ChestHoldFar (an
//     order of the chest's event cannot come within the write's sub-frame
//     gap); no event order queued (dEvt mOrderCount, 0x803C9EA0);
//   - restore: the word is actionWait AND this replica's own record holds
//     the chest (same pid, address and params) AND its bit reads clear
//     AND the hub has not said CONSUMED. A vanilla open chest sits in
//     actionWait too, and restoring one is the double loot itself.
//     Restoring needs neither ARMED nor IDLE: it is how the replica fails
//     open (no partner in scope, the hub gone, co-op turned off).
//
// Residuals (docs/08 §6.7.1): a chest Link was already within ChestHoldFar
// of when the partner came into scope is not held until he walks off; a
// held, consumed chest is skipped by the stale-actor re-create (the REL
// answers GATE for actionWait) and opens at the room's next reload; an
// auto-disable (the Breaker tripping) leaves held chests held until then.

const (
	// ChestClaimRange is how near (XZ units) Link comes to a held chest
	// before the replica asks the hub for it. At a run (about 17 units a
	// frame at 30 fps) the 300 units to boxCheck's 100 are about half a
	// second, many LAN round trips.
	ChestClaimRange = 400
	// ChestHoldFar is how far Link must be from a chest for a hold to be
	// written (refresh.go's linkRefreshRange, the same reasoning).
	ChestHoldFar = 300
	// ChestReleaseRange and ChestReleaseAfter: a granted chest Link has
	// stayed farther than this from for that long is released.
	ChestReleaseRange = 600
	ChestReleaseAfter = 2 * time.Second
	// ChestAskEvery is the CLAIM repeat (the renewal) while in range.
	ChestAskEvery = time.Second

	// chestTextWait is actionWait__8daTbox_cFv's .text offset in
	// d_a_tbox.rel (tww config/GZLE01/rels/d_a_tbox/symbols.txt:41, size
	// 0x8: li r3,1; blr).
	chestTextWait = 0x24AC
	// chestFuncMaxHeld: funcTypes 6..8 (tact, extra save info) are never
	// held, as the refresh relay refuses them.
	maxChests = 64
)

// ChestKey names a chest: its save table and chest number.
type ChestKey struct {
	Table int8
	No    uint8
}

func (k ChestKey) String() string { return fmt.Sprintf("table %d chest %d", k.Table, k.No) }

// ChestActor is one executing daTbox_c as read.
type ChestActor struct {
	PID, Addr uint32
	Params    uint32
	Key       ChestKey
	Pos       [3]float32   // current.pos
	Base      uint32       // d_a_tbox.rel's .text: daTbox_Execute - 0x2FB0
	Word      uint32       // the action word as read
	Flags     uint16       // mFlags
	A         actors.Actor // the walk's entry, for RereadParams
}

// WordAddr is the chest's action word.
func (c ChestActor) WordAddr() uint32 { return c.Addr + mailbox.RefreshTboxActFnOff }

// OpenWait and Wait are the two words a hold swaps between.
func (c ChestActor) OpenWait() uint32 { return c.Base + mailbox.RefreshTboxTextOpenWait }
func (c ChestActor) Wait() uint32     { return c.Base + chestTextWait }

// Busy reports mFlags' UNK_08, OPENING or APPEARING.
func (c ChestActor) Busy() bool { return c.Flags&mailbox.RefreshTboxFlagsBusy != 0 }

// same reports whether d is the chest c was: the same actor and params.
func (c ChestActor) same(d ChestActor) bool {
	return c.PID == d.PID && c.Addr == d.Addr && c.Params == d.Params && c.Base == d.Base
}

func (c ChestActor) String() string { return fmt.Sprintf("chest pid %d (%s)", c.PID, c.Key) }

// chestKeyOf is params' chest key in table, ok false for a funcType 6..8.
func chestKeyOf(params uint32, table int8) (ChestKey, bool) {
	ft := params & mailbox.RefreshTboxFuncMask
	if ft >= mailbox.RefreshTboxFuncRefuseLo && ft <= mailbox.RefreshTboxFuncRefuseHi {
		return ChestKey{}, false
	}
	return ChestKey{Table: table, No: uint8((params >> mailbox.RefreshTboxBitShift) & mailbox.RefreshTboxBitMask)}, true
}

// chestExec returns daTbox_Execute through a's own profile behind the
// retail fingerprint (refresh.c rf_tbox_exec), or 0.
func chestExec(m mem.Reader, a uint32) uint32 {
	sm, err := mem.ReadU32(m, a+addr.ActorOffSubMtd)
	if err != nil || !addr.InMEM1(sm) {
		return 0
	}
	exec, err := mem.ReadU32(m, sm+addr.MethodOffExecute)
	if err != nil || !addr.InMEM1(exec) || exec < mailbox.RefreshTboxTextExec || !addr.InMEM1(exec+mailbox.RefreshTboxFpOff1) {
		return 0
	}
	w0, err0 := mem.ReadU32(m, exec+mailbox.RefreshTboxFpOff0)
	w1, err1 := mem.ReadU32(m, exec+mailbox.RefreshTboxFpOff1)
	if err0 != nil || err1 != nil || w0 != mailbox.RefreshTboxFpWord0 || w1 != mailbox.RefreshTboxFpWord1 {
		return 0
	}
	return exec
}

// readChest reads a's chest fields; ok is false for a chest that is not
// one this can hold (funcType 6..8, or no retail fingerprint).
func readChest(m mem.Reader, a *actors.Actor, table int8) (ChestActor, bool, error) {
	k, ok := chestKeyOf(a.Params, table)
	if !ok {
		return ChestActor{}, false, nil
	}
	exec := chestExec(m, a.Addr)
	if exec == 0 {
		return ChestActor{}, false, nil
	}
	word, err := mem.ReadU32(m, a.Addr+mailbox.RefreshTboxActFnOff)
	if err != nil {
		return ChestActor{}, false, err
	}
	flags, err := mem.ReadU16(m, a.Addr+mailbox.RefreshTboxFlagsOff)
	if err != nil {
		return ChestActor{}, false, err
	}
	return ChestActor{PID: a.PID, Addr: a.Addr, Params: a.Params, Key: k, Pos: a.Pos,
		Base: exec - mailbox.RefreshTboxTextExec, Word: word, Flags: flags, A: *a}, true, nil
}

// readChests reads every executing chest of w that a hold may concern.
func readChests(m mem.Reader, w *actors.Walk, table int8) ([]ChestActor, error) {
	var out []ChestActor
	for i := range w.Actors {
		a := &w.Actors[i]
		if uint16(a.Proc) != mailbox.RefreshProcTbox || a.InitState != 2 {
			continue
		}
		c, ok, err := readChest(m, a, table)
		if err != nil {
			return nil, err
		}
		if ok && len(out) < maxChests {
			out = append(out, c)
		}
	}
	return out, nil
}

// chestBitSet reads chest no of the live memBit (isTbox, the loaded table).
func chestBitSet(m mem.Reader, no uint8) (bool, error) {
	v, err := mem.ReadU32(m, addr.LiveMemBit+addr.MemBitOffTbox)
	if err != nil {
		return false, err
	}
	return v&(1<<no) != 0, nil
}

// distXZ is the XZ distance from Link to p; a NaN or Inf reads as 0
// (near), so a bad read never opens a hold gate.
func distXZ(link, p [3]float32) float64 {
	dx, dz := float64(link[0]-p[0]), float64(link[2]-p[2])
	d := math.Hypot(dx, dz)
	if math.IsNaN(d) || math.IsInf(d, 0) {
		return 0
	}
	return d
}

// ChestPlan is a tick's chest writes: holds and restores.
type ChestPlan struct {
	Hold    []ChestActor
	Restore []ChestActor
	// Consumed are the keys whose restore is refused: the hub said so,
	// or the world or this game has the chest's bit.
	Consumed map[ChestKey]bool
	// Why says why each restore is made, by pid, for the log.
	Why map[uint32]string
}

func (p *ChestPlan) empty() bool { return p == nil || len(p.Hold)+len(p.Restore) == 0 }

// ChestStatus is how chest claims stand.
type ChestStatus struct {
	On       bool
	Scope    bool // a partner is in scope
	Held     int  // chests held now
	Granted  int
	Holds    int // holds written this session
	Restores int
	Asked    int
	Denied   int
	LastWhy  string
}

// chestState is the replica's part of chest claims.
type chestState struct {
	visit    string
	held     map[uint32]ChestActor // our own holds, by pid
	granted  map[ChestKey]bool
	consumed map[ChestKey]bool
	askedAt  map[ChestKey]time.Time
	farSince map[ChestKey]time.Time
	plan     *ChestPlan
	stats    ChestStatus
	told     string
}

func (s *chestState) init() {
	if s.held == nil {
		s.held, s.granted, s.consumed = map[uint32]ChestActor{}, map[ChestKey]bool{}, map[ChestKey]bool{}
		s.askedAt, s.farSince = map[ChestKey]time.Time{}, map[ChestKey]time.Time{}
	}
}

// partnerInScope reports whether the partner's PRESENCE puts it where it
// could open the same chests: in game, the same stage and table, and the
// same room, or either room -1 (a stage start).
func partnerInScope(me, partner *wire.Presence) bool {
	switch {
	case me == nil || partner == nil:
		return false
	case me.Flags&wire.PresenceInGame == 0 || partner.Flags&wire.PresenceInGame == 0:
		return false
	case me.Stage != partner.Stage || me.Table != partner.Table || me.Table < 0:
		return false
	}
	return me.Room == partner.Room || me.Room == -1 || partner.Room == -1
}

// anyPartnerInChestScope reports whether any partner could open the same
// chests as this game (partnerInScope): with three or more players a
// chest is held for a claim while any of them is there, as the scoped
// stores and placed kills ask (docs/08 Appendix C item 171).
func (r *Replica) anyPartnerInChestScope() bool {
	for _, v := range r.partners {
		if partnerInScope(r.presence, &v.p.Presence) {
			return true
		}
	}
	return false
}

// chestInput is what one tick's chest decisions rest on.
type chestInput struct {
	on      bool // armed, chest claims granted, writes possible
	scope   bool
	now     time.Time
	link    [3]float32
	linkOK  bool
	ordered bool // an event order is queued
	idle    bool // the tick's gates let a hold through (IDLE, STABLE, settled: CanWriteIdle)
	room    int8
	chests  []ChestActor
	bits    uint32 // the live memBit's mTbox
	world   uint32 // the loaded table's mTbox in the world image (r.view)
}

// Why a held chest is restored, for the log.
const (
	whyGranted  = "the host granted it"
	whyNoScope  = "no partner in scope"
	whyNotArmed = "co-op is not armed here (the host gone, co-op off, or another file)"
	whyCoopOff  = "co-op was turned off"
)

// decide plans one tick's holds, restores and CLAIM messages. s is
// updated (asks, releases, the held set).
//
// What is held is read from memory, not from a record: vanilla puts a
// chest in actionWait only with its bit set (after CreateInit's checkOpen,
// d_a_tbox.cpp:454-457, or at the end of the open demo, :913-916, whose
// OpenInit_com set the bit, :851-859), so a chest in actionWait whose live
// bit reads clear is a hold, this process's or an earlier one's. A hold
// write or a restore that did not land is so seen again next tick.
func (s *chestState) decide(in chestInput, send func(*wire.Claim)) *ChestPlan {
	s.init()
	plan := &ChestPlan{Consumed: map[ChestKey]bool{}, Why: map[uint32]string{}}
	for k := range s.consumed {
		plan.Consumed[k] = true
	}
	restore := func(c ChestActor, why string) {
		plan.Restore = append(plan.Restore, c)
		plan.Why[c.PID] = why
	}
	seen := map[uint32]bool{}
	for _, c := range in.chests {
		seen[c.PID] = true
		live := in.bits&(1<<c.Key.No) != 0
		opened := live || in.world&(1<<c.Key.No) != 0
		holding := c.Word == c.Wait() && !live
		if holding {
			s.held[c.PID] = c
		} else {
			delete(s.held, c.PID)
		}
		d := distXZ(in.link, c.Pos)
		switch {
		case opened || s.consumed[c.Key]:
			// Never restore. A chest still in actionOpenWait whose bit
			// the world or this game has is stale (its create would
			// have stood it open, CreateInit's checkOpen): hold it,
			// partner in scope or not, so its prompt can't pay the item
			// twice. The hub's CONSUMED counts the same.
			plan.Consumed[c.Key] = true
			delete(s.granted, c.Key)
			if !holding && in.on && s.canHold(c, in, d) {
				plan.Hold = append(plan.Hold, c)
			}
		case !in.on || !in.scope:
			if holding {
				if in.on {
					restore(c, whyNoScope)
				} else {
					restore(c, whyNotArmed)
				}
			}
			if s.granted[c.Key] && in.on {
				send(&wire.Claim{Op: wire.ClaimRelease, Table: c.Key.Table, No: c.Key.No, Room: in.room})
			}
			delete(s.granted, c.Key)
		case s.granted[c.Key]:
			if holding {
				restore(c, whyGranted)
			}
			if in.linkOK && d > ChestReleaseRange {
				if t, ok := s.farSince[c.Key]; !ok {
					s.farSince[c.Key] = in.now
				} else if in.now.Sub(t) >= ChestReleaseAfter {
					send(&wire.Claim{Op: wire.ClaimRelease, Table: c.Key.Table, No: c.Key.No, Room: in.room})
					delete(s.granted, c.Key)
					delete(s.farSince, c.Key)
				}
				break
			}
			delete(s.farSince, c.Key)
			s.ask(c.Key, in, d, send)
		default:
			if holding {
				s.ask(c.Key, in, d, send)
				break
			}
			if s.canHold(c, in, d) {
				plan.Hold = append(plan.Hold, c)
			}
		}
	}
	for pid := range s.held {
		if !seen[pid] {
			delete(s.held, pid) // deleted with its room or stage
		}
	}
	return plan
}

// canHold is the hold gates the tick's reads answer.
func (s *chestState) canHold(c ChestActor, in chestInput, d float64) bool {
	return in.idle && c.Word == c.OpenWait() && !c.Busy() && in.linkOK && d > ChestHoldFar && !in.ordered
}

// ask sends CLAIM for k when Link is in range, at most every ChestAskEvery.
func (s *chestState) ask(k ChestKey, in chestInput, d float64, send func(*wire.Claim)) {
	if !in.linkOK || d > ChestClaimRange {
		return
	}
	if t, ok := s.askedAt[k]; ok && in.now.Sub(t) < ChestAskEvery {
		return
	}
	s.askedAt[k] = in.now
	s.stats.Asked++
	send(&wire.Claim{Op: wire.ClaimAsk, Table: k.Table, No: k.No, Room: in.room})
}

// onClaim logs and takes the hub's verdict. r.mu is held.
func (r *Replica) onChestClaim(m *wire.Claim) {
	k := ChestKey{Table: m.Table, No: m.No}
	switch m.Op {
	case wire.ClaimGrant:
		if !r.ch.granted[k] {
			r.logf("co-op: chest claims: the host granted %s", k)
		}
	case wire.ClaimDeny:
		r.logf("co-op: chest claims: the host denied %s: the partner holds it", k)
	case wire.ClaimConsumed:
		if !r.ch.consumed[k] {
			r.logf("co-op: chest claims: the host says %s is consumed: it stays closed here until its room reloads", k)
		}
	}
	r.ch.onClaim(m)
}

// onClaim takes the hub's verdict. r.mu is held.
func (s *chestState) onClaim(m *wire.Claim) {
	s.init()
	k := ChestKey{Table: m.Table, No: m.No}
	switch m.Op {
	case wire.ClaimGrant:
		s.granted[k] = true
	case wire.ClaimDeny:
		delete(s.granted, k)
		s.stats.Denied++
	case wire.ClaimConsumed:
		delete(s.granted, k)
		s.consumed[k] = true
	}
}

// forget drops the per-visit state (a new stage or table): the chests are
// gone with it.
func (s *chestState) forget() {
	s.held, s.granted, s.consumed, s.askedAt, s.farSince = nil, nil, nil, nil, nil
	s.init()
}

// reconcileChests plans this tick's chest holds and restores and sends
// its CLAIMs; ApplyChests makes the writes after r.mu is released. r.mu
// is held.
func (r *Replica) reconcileChests(o *obs, armed bool, out *outbox) {
	cs := &r.ch
	cs.init()
	cs.plan = nil
	s := o.s
	if s == nil {
		return
	}
	if v := fmt.Sprintf("%s/%d", s.StageName, s.Table); v != cs.visit {
		cs.visit = v
		cs.forget()
	}
	off, _ := r.writer.Disabled()
	on := armed && r.features&proto.FeatureChestClaim != 0 && !r.writer.DryRun() && !off && o.consistent()
	cs.stats.On = on
	if !on && len(cs.held) == 0 {
		cs.granted = map[ChestKey]bool{}
		return
	}
	w, err := actors.Read(r.m, 0)
	if err != nil {
		r.chestTold("the actor walk failed: " + err.Error())
		return
	}
	chests, err := readChests(r.m, &w, s.Table)
	if err != nil {
		r.chestTold("reading the chests failed: " + err.Error())
		return
	}
	bits, err := mem.ReadU32(r.m, addr.LiveMemBit+addr.MemBitOffTbox)
	if err != nil {
		r.chestTold("the live chest bits are unreadable: " + err.Error())
		return
	}
	in := chestInput{on: on, now: r.now(), room: int8(s.StayNo), chests: chests, bits: bits}
	if r.haveView && r.viewWorld == r.sessionWorld && s.Table >= 0 && int(s.Table) < addr.TableCount {
		off := addr.OffMemBit(int(s.Table)) + addr.MemBitOffTbox
		in.world = binary.BigEndian.Uint32(r.view[off : off+4])
	}
	in.scope = on && r.anyPartnerInChestScope()
	// A hold is planned only when the guard will take it: a refused write
	// counts toward the Breaker (a room entry's settling ticks refused
	// every hold and turned co-op off, live 2026-11-01). Restores need no
	// gate.
	in.idle = o.st.CanWriteIdle()
	if p, err := linkpos.NewLink(r.m).Position(); err == nil {
		in.link, in.linkOK = [3]float32{p.PosX, p.PosY, p.PosZ}, true
	}
	if n, err := mem.ReadU8(r.m, addr.EvtOrderCount); err != nil || n != 0 {
		in.ordered = true
	}
	cs.stats.Scope = in.scope
	cs.plan = cs.decide(in, func(m *wire.Claim) { out.send(m) })
	cs.told = ""
}

// chestTold logs a chest-level wait once until it changes. r.mu is held.
func (r *Replica) chestTold(why string) {
	cs := &r.ch
	cs.stats.LastWhy = why
	if why != cs.told {
		cs.told = why
		r.logf("co-op: chest claims: %s", why)
	}
}

// applyChests makes the tick's chest plan once r.mu is released.
func (r *Replica) applyChests(ctx context.Context, p *ChestPlan, rep *Report) {
	if p.empty() {
		return
	}
	res, err := r.writer.ApplyChests(ctx, *p)
	r.mu.Lock()
	defer r.mu.Unlock()
	cs := &r.ch
	cs.init()
	rep.Wrote += len(res.Wrote)
	if rep.WriteErr == nil && err != nil {
		rep.WriteErr = err
	}
	for _, c := range res.Held {
		cs.held[c.PID] = c
		cs.stats.Holds++
		r.logf("co-op: chest claims: held %s: no prompt until the host grants it (or for good, once opened)", c)
	}
	for _, c := range res.Restored {
		delete(cs.held, c.PID)
		cs.stats.Restores++
		why := p.Why[c.PID]
		if why == "" {
			why = "its hold ended"
		}
		r.logf("co-op: chest claims: restored %s: %s", c, why)
	}
	for _, w := range res.Skipped {
		cs.stats.LastWhy = w
	}
}

// releaseChests restores every chest this replica holds, at once (co-op
// turned off, the session ending), waiting at most d. It takes r.mu.
func (r *Replica) releaseChests(d time.Duration) {
	r.mu.Lock()
	p := ChestPlan{Why: map[uint32]string{}}
	var world uint32
	if t := r.last; t != nil && t.s != nil && r.haveView && t.s.Table >= 0 && int(t.s.Table) < addr.TableCount {
		off := addr.OffMemBit(int(t.s.Table)) + addr.MemBitOffTbox
		world = binary.BigEndian.Uint32(r.view[off : off+4])
	}
	for _, c := range r.ch.held {
		if world&(1<<c.Key.No) != 0 {
			continue // the world has it: never restored
		}
		c.Word = c.Wait()
		p.Restore = append(p.Restore, c)
		p.Why[c.PID] = whyCoopOff
	}
	p.Consumed = map[ChestKey]bool{}
	for k := range r.ch.consumed {
		p.Consumed[k] = true
	}
	r.mu.Unlock()
	if p.empty() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	var rep Report
	r.applyChests(ctx, &p, &rep)
}

// heldChest reports whether pid is a chest this replica holds (the
// refresh relay leaves those alone). r.mu is held.
func (r *Replica) heldChest(pid uint32) bool {
	_, ok := r.ch.held[pid]
	return ok
}

// chestStatus is chest claims' part of Status. r.mu is held.
func (r *Replica) chestStatus() ChestStatus {
	st := r.ch.stats
	st.Held, st.Granted = len(r.ch.held), len(r.ch.granted)
	return st
}
