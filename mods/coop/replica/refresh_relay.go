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
// enemy-mirror-batch-d.json R-D4, R-D5, G-D2..G-D5, D4-D7,
// "actor_refresh"; research/stale-actor-refresh.md Tier 3). Tiers 1 and 2
// (refresh.go) write an actor's own bytes; Tier 3 is for the actors whose
// create alone reads their bit, where no byte of the live actor would make
// it right: a boulder (Stone2), a bush (Obj_Leaves), a pickup (daItem), a
// chest (Tbox), a barricade (Saku), or one of the table rows (review idea
// 11, design (b): a bombable wall, a breakable floor, an ice block, a
// Moblin-wood door, ET's light statue and light wall, a song statue, GT's
// Phantom Ganon door, a crystal switch, a warp jar's lid, DRC's flame lift,
// FW's flower house, WT's soil mound) this game placed before the
// partner's bit landed here stays until its room reloads. The relay asks
// the REL to bring it up to date (wwapi/mailbox refresh.go,
// inject/rel/refresh.c): a class whose create refuses to come up once the
// bit is set is deleted, and a reload then never creates it, as vanilla;
// a class whose create builds it in its set state (refreshProc.recreate:
// the chest, the crystal switch, the jar, the lift, the house, the mound)
// is deleted and created again in its own layer, so it comes up open,
// lit, lidless, dropped, fallen or grown as a reload would build it. A
// re-created actor answers IN_STATE when the walk names it again, which
// ends the asking for its pid.
//
// It acts only on an actor whose bit is in the landed set (landed.go, the
// G-D2 set #169 built for Tiers 1 and 2, D11: the bits this replica's own
// verified writes set, per bit and per store, never one that merely reads
// set), so the local player's own break, cut, pickup or chest never gets
// a request. The decoder (refreshDecode, G-D3) derives each actor's bit
// from its params or fields with the REL's formulas (mailbox.Refresh*,
// pinned to inject/include/wwrefresh.h), so a candidate the Go side gets
// wrong costs a refusal, never a wrong delete: the REL re-derives and
// re-reads the bit and runs every gate again (the design's safety rule).
// Never candidates: a Tbox of funcType 6..8 (d_a_tbox.cpp:29-31), a daItem
// with no bit (0x7F) or a Blue Jelly (d_a_item.cpp:227-233), a pickup bit
// the landed set takes no write of (32..63 alias mVisitedRoom[0], 0x40..
// are a zone's; landed.go LandItem), a zone or room switch with no home
// room (isSwitch asserts, d_save.cpp:1585-1604), and a Saku unless every
// half it has (d_a_saku.cpp:709-724) names a switch that landed and reads
// set (D7: v1 deletes only the whole barricade; a half with no switch
// can never read set, so its barricade is no candidate).
//
// The Go-side gates (G-D4), each judged by the tick that plans the
// candidate: co-op armed, the hub granting FeatureCombat, not a dry run
// (the M6 receiver's m6Granted, which plans it); IDLE; not a boss stage
// (bossStage, planner.go); the bit landed at least RefreshSettle ago and
// set now (refreshTick.landedWhy); the home room loaded and settled
// (roomSettled); the actor executing, in no event, not carried, no mirror
// pause bit, not claimed by the enemy mirror, never light-shrunk by M6
// (ever); Link more than 300 units away and in no demo (linkDemo). The
// actor is read again, its params with it (actors.RereadParams), just
// before it is named. The REL checks its own (D4) besides.
//
// The key-door rows (door10, door12) are built in the REL behind
// WWRF_DOOR_ROWS 0 and refused here (refreshProc.pending) until the ST1
// key-door live check (archive/coop-batch2/live/LIVE-3-doors.md) decides
// between Tier 1's live lock write (refresh.go) and their re-create.
//
// The relay copies bossTick (bosskill.go): one request in flight, written
// body first and the seq last (mailbox.RefreshRequest.Fields) through the
// WriteGuard (TargetRefresh, pathRefresh); a refreshAckWait for the ack;
// a refreshRetry before the same actor is asked again after "gated", "no
// actor" or "none"; nothing asked of a REL module of another build
// (bossRELWhy's gate); each actor applied at most once, and given up after
// a final answer ("bit not set", "mismatch", "refused", "fingerprint
// miss", "already in its set state", "re-created", "deleted, create
// refused") until the stage visit or a save state ends; and each log line
// once per key and visit. Off by default: the --coop-refresh token set
// (Config.Refresh) is empty. Its "dry" token logs one line per candidate
// and writes nothing; a --coop-dry-run replica has no landed set (it
// writes nothing), so the token is how a dry run of Tier 3 is had.

// refreshProc is an allow-listed proc the relay may ask about.
type refreshProc struct {
	key  string // its --coop-refresh token
	proc uint16 // mailbox.RefreshProc*
	// recreate: the REL deletes the actor and creates it again (its create
	// builds the set state), else it only deletes it.
	recreate bool
	// row: a table row of refresh.c rf_rows, whose bit is a switch,
	// (params >> shift) & 0xFF, or home.angle.x & 0xFF with angleX.
	row    bool
	shift  int
	angleX bool
	// memOnly: the create reads the switch with room -1 (a door's
	// setKey), so only a memory switch; noZone: with the stay room (a warp
	// jar), so a memory or dan switch. The REL refuses any other.
	memOnly, noZone bool
	// pending, when set, is why ParseRefresh refuses the token: the REL
	// has the row compiled out (mailbox.RefreshDoorRows).
	pending string
}

// refreshPendingST1 is why the key-door rows are refused.
const refreshPendingST1 = "pending the ST1 key-door live check (Tier 1's lock write stays the door's refresh; WWRF_DOOR_ROWS 0)"

// refreshProcs is the allow-list (wwrefresh.h WWRF_PROC_*), the design's
// cut_order backwards: a cut drops the last first. The rows after tbox are
// review idea 11's (refresh.c rf_rows), each opt-in by its token.
var refreshProcs = []refreshProc{
	{key: "stone2", proc: mailbox.RefreshProcStone2},
	{key: "leaves", proc: mailbox.RefreshProcLeaves},
	{key: "item", proc: mailbox.RefreshProcItem},
	{key: "saku", proc: mailbox.RefreshProcSaku},
	{key: "tbox", proc: mailbox.RefreshProcTbox, recreate: true},
	{key: "wall", proc: mailbox.RefreshProcWall, row: true},
	{key: "floor", proc: mailbox.RefreshProcFloor, row: true},
	{key: "ice", proc: mailbox.RefreshProcIce, row: true},
	{key: "mjdoor", proc: mailbox.RefreshProcMjdoor, row: true},
	{key: "mkie", proc: mailbox.RefreshProcMkie, row: true, shift: mailbox.RefreshMkieSwShift},
	{key: "mkiek", proc: mailbox.RefreshProcMkiek, row: true},
	{key: "mknjd", proc: mailbox.RefreshProcMknjd, row: true},
	{key: "vfan", proc: mailbox.RefreshProcVfan, row: true},
	{key: "swhit0", proc: mailbox.RefreshProcSwhit0, row: true, recreate: true},
	{key: "warpt", proc: mailbox.RefreshProcWarpt, row: true, recreate: true, angleX: true, noZone: true},
	{key: "mflft", proc: mailbox.RefreshProcMflft, row: true, recreate: true, shift: mailbox.RefreshMflftSwShift},
	{key: "kokiie", proc: mailbox.RefreshProcKokiie, row: true, recreate: true, shift: mailbox.RefreshKokiieSwShift},
	{key: "vmc", proc: mailbox.RefreshProcVmc, row: true, recreate: true, shift: mailbox.RefreshVmcSwShift},
	{key: "door10", proc: mailbox.RefreshProcDoor10, row: true, recreate: true, memOnly: true, pending: refreshPendingST1},
	{key: "door12", proc: mailbox.RefreshProcDoor12, row: true, recreate: true, memOnly: true, pending: refreshPendingST1},
}

// refreshProcOf is proc's allow-list entry, nil for none.
func refreshProcOf(proc uint16) *refreshProc {
	for i := range refreshProcs {
		if refreshProcs[i].proc == proc {
			return &refreshProcs[i]
		}
	}
	return nil
}

const (
	// RefreshTokens names every --coop-refresh proc token the relay takes,
	// for the usage (refreshProcs' keys but the pending ones, in order).
	RefreshTokens = "stone2,leaves,item,tbox,saku,wall,floor,ice,mjdoor,mkie,mkiek,mknjd,vfan,swhit0,warpt,mflft,kokiie,vmc"
	// refreshDryToken is the --coop-refresh token that logs the candidates
	// and writes nothing.
	refreshDryToken = "dry"
)

// refreshSet is a parsed --coop-refresh: the procs asked about, and
// whether it only logs.
type refreshSet struct {
	procs map[uint16]bool
	dry   bool
}

// on reports whether the set asks about some proc.
func (s refreshSet) on() bool { return len(s.procs) > 0 }

// ParseRefresh parses a --coop-refresh token set: refreshProcs' keys
// joined by ",", with "dry" to log the candidates only; "" or "none" is
// none (the default). "dry" alone is an error: it names no proc, so the
// relay would stay off and log nothing, which reads as no candidates.
func ParseRefresh(v string) (refreshSet, error) {
	s := refreshSet{procs: map[uint16]bool{}}
	if t := strings.TrimSpace(v); t == "" || strings.EqualFold(t, "none") {
		return s, nil
	}
	for tok := range strings.SplitSeq(v, ",") {
		tok = strings.ToLower(strings.TrimSpace(tok))
		if tok == refreshDryToken {
			s.dry = true
			continue
		}
		i := slices.IndexFunc(refreshProcs, func(p refreshProc) bool { return p.key == tok })
		if i < 0 {
			return refreshSet{}, fmt.Errorf("--coop-refresh: %q is no refresh token (say %s, dry, or none)", tok, RefreshTokens)
		}
		if why := refreshProcs[i].pending; why != "" {
			return refreshSet{}, fmt.Errorf("--coop-refresh: %q is not taken yet: %s", tok, why)
		}
		s.procs[refreshProcs[i].proc] = true
	}
	if s.dry && !s.on() {
		return refreshSet{}, fmt.Errorf("--coop-refresh: dry names no proc: add the ones to log (%s), e.g. %s,dry", RefreshTokens, RefreshTokens)
	}
	return s, nil
}

const (
	// refreshRetry is the wait before an actor is asked again after a
	// result that asks for it, or a write that did not go: bossRetry's.
	refreshRetry = bossRetry
	// refreshGateBackoff caps the doubling of refreshRetry for an actor
	// the REL keeps answering GATE: 500 ms, 1, 2, 4, then every 8 s. A
	// gate that never opens (a Saku the local player burnt, a chest this
	// game opened after the partner's bit landed: refresh.c rf_proc_gate)
	// would otherwise be asked twice a second for the whole visit.
	refreshGateBackoff = 4
	// refreshAckWait is how long a request may go unacked (the keeper
	// runs every frame) before it is given up and asked again.
	refreshAckWait = bossAckWait
	// maxRefreshKeys bounds what the relay remembers per stage visit: the
	// actors done and waiting, and the log keys.
	maxRefreshKeys = actors.MaxNodes
	// tboxRoomMask and tboxRoomNone are a chest's mRoomNo as its create
	// takes it from home.angle.x (d_a_tbox.cpp:1241), and the value that
	// means no room (:408).
	tboxRoomMask = 0x3F
	tboxRoomNone = 0x3F
	// landedItems is how many pickup bits the landed set takes (landed.go
	// LandItem: mItem's one word; isItem reads 32..63 from mVisitedRoom[0]
	// and 0x40.. from a zone, d_save.cpp:1650-1668).
	landedItems = 32
)

// refreshCand is an actor the relay may name in a request: its bit as the
// REL derives it, and the landed bits it waits on.
type refreshCand struct {
	a       actors.Actor
	bitKind uint8  // mailbox.RefreshBit*
	bitNo   uint16 // the bit the request names (Saku: sw1, half 0's)
	keys    []LandedBit
	// room is the room that must be loaded and settled before it is
	// named (roomSettled): its home room, or for a Tbox of the stage file
	// (home room -1, as every DRC chest is: P0-RF) the room its create
	// takes, home.angle.x & 0x3F (d_a_tbox.cpp:1241, searchRoomNo
	// :276-279), -1 when that is 0x3F, none (:408): only such a chest
	// skips the gate; any other proc of room -1 is held, as before. The
	// request still names the home room: the REL compares +0x1E2.
	room int
	// doorRooms: a door of the stage file (home room -1) waits on either of
	// its rooms instead (doorRoomsWhy, as Tier 1's).
	doorRooms bool
	// recreate is its proc's refreshProc.recreate.
	recreate bool
}

func (c *refreshCand) String() string {
	return fmt.Sprintf("%s %s", mailbox.RefreshProcName(uint16(c.a.Proc)), c.a.String())
}

// verb is what the REL is asked to do with c.
func (c *refreshCand) verb() string {
	if c.recreate {
		return "re-create"
	}
	return "delete"
}

// refreshDecode derives a's bit as the REL does (wwrefresh.h, the
// mailbox.Refresh* formulas) while table is loaded: ok is false when a is
// no candidate at all (a proc off the allow-list, a kind this op never
// deletes, no bit the landed set can hold). daItem's bit and item number
// are read from the actor (mItemBitNo +0x630, m_itemNo +0x63A).
func refreshDecode(m mem.Reader, a *actors.Actor, table int8) (c refreshCand, ok bool, err error) {
	c.a, c.room = *a, int(a.HomeRoom)
	if rp := refreshProcOf(uint16(a.Proc)); rp != nil {
		c.recreate = rp.recreate
	}
	sw := func(no int) (LandedBit, bool) {
		switch st := addr.ClassifySwitch(no); {
		case st == addr.SwitchNone || st == addr.SwitchInvalid:
			return LandedBit{}, false
		case (st == addr.SwitchZone || st == addr.SwitchRoom) && (a.HomeRoom < 0 || int(a.HomeRoom) >= addr.RoomCount):
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
			// takes no write of.
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
		if a.HomeRoom < 0 {
			c.room = -1
			if r := int(a.HomeAngle[0]) & tboxRoomMask; r != tboxRoomNone && r < addr.RoomCount {
				c.room = r
			}
		}
	case mailbox.RefreshProcSaku:
		sw1 := int(p>>mailbox.RefreshSakuSw1Shift) & mailbox.RefreshSakuSwMask
		halves := []int{sw1}
		if p&mailbox.RefreshSakuHalf1Mask != 0 {
			halves = append(halves, int(p>>mailbox.RefreshSakuSw2Shift)&mailbox.RefreshSakuSwMask)
		}
		for _, no := range halves {
			k, ok := sw(no)
			if !ok {
				// A half with no switch (0xFF) never reads set, so the REL
				// would hold the whole barricade at its proc gate.
				return c, false, nil
			}
			c.keys = append(c.keys, k)
		}
		c.bitKind, c.bitNo = mailbox.RefreshBitSw, uint16(sw1)
	default:
		rp := refreshProcOf(uint16(a.Proc))
		if rp == nil || !rp.row || rp.pending != "" {
			return c, false, nil
		}
		no, ok := refreshRowBit(rp, a)
		if !ok {
			return c, false, nil
		}
		k, ok := sw(no)
		if !ok {
			return c, false, nil
		}
		c.bitKind, c.bitNo, c.keys = mailbox.RefreshBitSw, uint16(no), []LandedBit{k}
		if rp.memOnly && a.HomeRoom < 0 {
			c.doorRooms = true
		}
	}
	return c, true, nil
}

// refreshRowBit derives a table row's switch as refresh.c rf_derive_row
// does: ok is false for no switch (0xFF) and for what the REL refuses (a
// timed crystal switch, a warp jar of type 1 or 2..4, a DOOR12 of arg1 8,
// a switch out of the row's room space).
func refreshRowBit(rp *refreshProc, a *actors.Actor) (no int, ok bool) {
	src := a.Params
	if rp.angleX {
		src = uint32(uint16(a.HomeAngle[0]))
	}
	no = int(src>>rp.shift) & mailbox.RefreshRowSwMask
	p := a.Params
	switch rp.proc {
	case mailbox.RefreshProcSwhit0:
		if t := p >> mailbox.RefreshSwhit0TimerSh & 0xFF; t != 0 && t != 0xFF {
			return 0, false // actionOnTimer would clear the switch here (d_a_swhit0.cpp:362)
		}
	case mailbox.RefreshProcWarpt:
		if t := int(p & mailbox.RefreshWarptTypeMask); t == mailbox.RefreshWarptTypeOn || t >= mailbox.RefreshWarptTypeSpLo && t <= mailbox.RefreshWarptTypeSpHi {
			return 0, false
		}
	case mailbox.RefreshProcDoor12:
		if int(uint16(a.HomeAngle[2]))>>8&0xFF == mailbox.RefreshDoor12Arg1Tmp {
			return 0, false
		}
	}
	switch {
	case no == mailbox.RefreshRowSwMask:
		return 0, false
	case rp.memOnly && no >= mailbox.RefreshMemSwEnd, rp.noZone && no >= mailbox.RefreshZoneSwMin:
		return 0, false
	}
	return no, true
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

// anyRefresh reports whether some bit of a kind the set asks about landed
// for the loaded table: the M6 receiver reads the walk for it.
func (t *refreshTick) anyRefresh(on refreshSet) bool {
	if !on.on() {
		return false
	}
	p := on.procs
	for b := range t.landed.At {
		if b.Table != t.s.Table {
			continue
		}
		switch b.Kind {
		case LandSwitch:
			for proc := range p {
				if proc != mailbox.RefreshProcTbox && proc != mailbox.RefreshProcItem {
					return true // every other proc reads a switch
				}
			}
		case LandTbox:
			if p[mailbox.RefreshProcTbox] {
				return true
			}
		case LandItem:
			if p[mailbox.RefreshProcItem] {
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
	if !r.rfOn.on() {
		return
	}
	for i := range w.Actors {
		a := &w.Actors[i]
		if !r.rfOn.procs[uint16(a.Proc)] || f.done[a.PID] != "" {
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
			// partner's not yet (Saku: every half's must).
			continue
		}
		switch {
		case why != "":
		case c.doorRooms:
			why = doorRoomsWhy(t, a)
		case c.room >= 0 || uint16(a.Proc) != mailbox.RefreshProcTbox:
			why = t.roomSettled(c.room)
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
	gated   map[uint32]int       // GATE answers in a row per pid (refreshGateBackoff)
	done    map[uint32]string    // pids answered for good, with the answer
	held    map[uint32]string    // candidates held, with the last reason
	last    string               // the last hold's reason
	dry     map[uint32]bool      // pids a dry run logged
	logged  map[string]bool      // log keys said this visit
	stats   ActorRefreshStatus
}

func newActorRefresh() actorRefresh {
	return actorRefresh{next: map[uint32]time.Time{}, gated: map[uint32]int{}, done: map[uint32]string{}, held: map[uint32]string{}, dry: map[uint32]bool{}, logged: map[string]bool{}}
}

// forget drops what the relay remembers of process IDs and the visit: a
// save state rewound the counter, or another stage visit began.
func (f *actorRefresh) forget() {
	f.pending = nil
	clear(f.next)
	clear(f.gated)
	clear(f.done)
	clear(f.held)
	clear(f.dry)
	clear(f.logged)
	f.last = ""
}

// retryAfter is the wait before pid is asked again after non-final
// result r: refreshRetry, doubled for each GATE in a row up to
// refreshGateBackoff times (NONE and NO_ACTOR are races and wait the flat
// refreshRetry, leaving the count).
func (f *actorRefresh) retryAfter(pid uint32, r uint8) time.Duration {
	if r != mailbox.RefreshRGate {
		return refreshRetry
	}
	if len(f.gated) >= maxRefreshKeys {
		clear(f.gated)
	}
	n := min(f.gated[pid], refreshGateBackoff)
	f.gated[pid]++
	return refreshRetry << n
}

func (f *actorRefresh) hold(pid uint32, why string) {
	if f.held[pid] == why {
		return
	}
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
	// queued (a re-create's among them), Recreated the re-creates.
	Seq       uint32
	Result    string
	Requests  int
	Applied   int
	Recreated int
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
	case mailbox.RefreshRApplied, mailbox.RefreshRNotSet, mailbox.RefreshRMismatch, mailbox.RefreshRRefused, mailbox.RefreshRFpMiss,
		mailbox.RefreshRRecreated, mailbox.RefreshRInState, mailbox.RefreshRCreateFailed:
		return true
	}
	return false
}

// actorRefreshTick runs the relay for one tick (under mu), after the M6
// receiver planned the candidates: it reads the REL's ack, and when a
// request is due sets r.arNow.
func (r *Replica) actorRefreshTick(armed bool) {
	f := &r.ar
	if !r.rfOn.on() {
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
	if r.rfOn.dry {
		for _, c := range f.cands {
			if !f.dry[c.a.PID] && len(f.dry) < maxRefreshKeys {
				f.dry[c.a.PID] = true
				r.logf("co-op: actor_refresh dry run: would ask the REL to %s the stale %s (bit kind %d, bit 0x%02X); nothing is written", c.verb(), c.String(), c.bitKind, c.bitNo)
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
			switch st.Result {
			case mailbox.RefreshRGate:
				detail = fmt.Sprintf("%s, %s", mailbox.RefreshGateName(st.Gate), detail)
			case mailbox.RefreshRRecreated:
				detail = fmt.Sprintf("%s, new pid %d", detail, st.NewPID)
			case mailbox.RefreshRCreateFailed:
				detail += ", gone until its room reloads"
			}
			r.arLog(fmt.Sprintf("ack pid %d result %d gate %d", c.a.PID, st.Result, st.Gate),
				fmt.Sprintf("co-op: actor_refresh %d: %s for the stale %s (%s)", f.seq, f.stats.Result, c.String(), detail))
			if refreshFinal(st.Result) {
				switch st.Result {
				case mailbox.RefreshRRecreated:
					f.stats.Recreated++
					f.stats.Applied++
				case mailbox.RefreshRApplied, mailbox.RefreshRCreateFailed:
					f.stats.Applied++
				}
				if len(f.done) >= maxRefreshKeys {
					clear(f.done)
				}
				f.done[c.a.PID] = f.stats.Result
				delete(f.held, c.a.PID)
				delete(f.gated, c.a.PID)
			} else {
				f.next[c.a.PID] = now.Add(f.retryAfter(c.a.PID, st.Result))
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
	// The actor read again just before it is named: the same node, pid,
	// proc, setID, home room and params as the walk's (the REL compares
	// them once more).
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
	r.arLog(fmt.Sprintf("ask pid %d", c.a.PID), fmt.Sprintf("co-op: the partner's bit landed: asking the REL to %s the stale %s (actor_refresh %d)", c.verb(), c.String(), q.seq))
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
	if r.rfOn.on() {
		st.On, st.Dry = strings.TrimSpace(r.cfg.Refresh), r.rfOn.dry
	}
	st.Candidates, st.Held, st.LastHeld = len(r.ar.cands), len(r.ar.held), r.ar.last
	return st
}
