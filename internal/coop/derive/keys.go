package derive

import (
	"fmt"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/keys"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/gate"
)

// Small keys (§6.4). keys(t) = |key sources set| - |key doors set| over
// the receiver's own flags. A table holds what keys(t) gives with its
// extra added, the keys from sources the table doesn't know: every rise of
// its count, while the Deriver tracks the file, that no known source
// explains. The target is keys(t) + extra, clamped to 0..99 as the HUD
// clamps a count (d_meter.cpp:5248-5254; KeyTarget). An extra key is a
// source like the others: a door takes its key from the whole, so once
// keys(t) is 0 or below a door spends an extra key, the player's own door
// or the partner's, and the derive never gives it back. extra itself never
// falls while the file is tracked: it counts sources, not keys.
//
// On the Deriver's first tick of a file, the baseline, extra starts at 0,
// and so it does again after every reset (a failed read, a save state, the
// title or file select, a restart of the tool). A table is derived only
// while its M1 invariant passes (KeysEnabled): every source its dungeon has
// is one the table knows. A count above keys(t) at the baseline is then a
// door's key not yet taken (a partner's door whose switch reached this
// file before the decrement could, or a door an M3 session synced with no
// key derive) or a duplicate (§6.7) left in the file, and the derive takes
// it out once the table settles. Measured as extra instead, it would stay
// for good, and with keys(t) at 0 refill every door spent (Appendix C item
// 94). So a key no known source gave (a cheat's) is kept only while the
// Deriver tracks the file.
//
// A rise is the count's growth since the derive last settled the table
// less the sources taken meanwhile: a door opened (the receiver's own or
// the partner's) lowers keys(t), the receiver's own pickup raises the
// count and keys(t) together, and neither is a rise. The derive's own
// writes are part of what it expects.
//
// A rise is removed only as a duplicate: a key a known source gave twice.
// The game gives a source's key once, as its flag then stops its actor
// from being made again (a chest opens empty, d_a_tbox.cpp:447-455; a
// pickup is not created, d_a_item.cpp:227-237), but an actor made before
// co-op set the flag still gives it (§6.7). Such an actor lives at most
// until its stage is left, so a rise is a duplicate only while some
// source of the table, placed in the stage Link is in, had its flag set
// during this visit, and each such source accounts for one duplicate a
// visit. With none, the rise is kept. Both players taking a key before
// either flag reached the other is no duplicate at all: one flag, one key
// each, and keys(t) agrees.
//
// A door's switch that co-op itself wrote into the loaded table's live
// memBit is held: it is not among the doors open until Link's next room
// change after the write (Appendix C item 139). A key door reads its
// switch only in actionInit, through setKey, which takes its lock off once
// the switch is set (daDoor10_c::setKey, d_a_door10.cpp:27-50;
// daDoor12_c::setKey, d_a_door12.cpp:52-73; daKddoor_c::setKey,
// d_a_kddoor.cpp:17-25). execute goes back to actionInit only after a
// frame on which checkExecute returns 0 (d_a_door10.cpp:811-812;
// d_a_door12.cpp:830-831; d_a_kddoor.cpp:947-948), and checkExecute
// returns 0 while Link is in neither of the door's rooms or neither room
// is shown, and on the frame mStayNo moves (mRoomNo2, set at the end of
// each execute, d_a_door10.cpp:829; dDoor_info_c::checkExecute and
// adjoinPlayer, d_door.cpp:63-75, :155-171). So a door of the room Link is
// in keeps the lock it read as he came in, whatever its switch does, until
// he changes room. The memory switches of the loaded table are written
// after the room hold, about 2 s after the room change (§6.3), after every
// door there ran actionInit: a door that took its key then would still
// lock, with no key left to open it (the owner's F13). A door held is
// counted again:
//
//   - at Link's first room change seen after the write (mStayNo differs
//     from the first tick after it), when the door runs actionInit again;
//   - when Link leaves its stage or table, or the file's tracking starts
//     over, since the door is created again reading the switch;
//   - when this game spends a key the count can't otherwise explain (the
//     count falls with no door counted as open since the derive last
//     settled the table): the player opened the locked door with a key,
//     and keyInit took it (d_door.cpp:433-447). keyInit acts only from
//     the door's front room (d_door.cpp:435), and Link goes on through
//     in the door's own event, so the room change most often counts the
//     door first; this covers an opening with no room change after it
//     (a door whose two rooms are one).
//
// A key-counter write of the derive's own stays in flight while the
// counter reads nonzero, past its DefaultInFlight: a game that runs no
// frames takes it late, and the fall would read as a key spent on a held
// door.
//
// Only co-op's live writes are held. A door the game opens sets its switch
// itself, its key already spent. A switch written to a cold table's saved
// slot is read as its door is created, when the table loads (getSave,
// d_save.cpp:1506-1509), as is one in a stage of the table other than
// Link's. A switch the receiver's copy already counted (its saved slot
// had it) stays counted.

// MaxKeys is the most keys a table holds: dMeter_keyMove clamps mKeyNum to
// 0..99 (d_meter.cpp:5248-5254).
const MaxKeys = 99

// KeyFacts are one table's small keys in a View.
type KeyFacts struct {
	Table          int
	Sources, Taken int // the table's key sources, and those whose flag is set
	Doors, Open    int // its key doors, and those whose switch is set
	// Held is how many of the Open doors are held (the top of this file):
	// co-op wrote their switch into the live copy since Link's last room
	// change, so the door may still show its lock. They are not counted.
	Held int
	Have int // the mKeyNum the table holds (View.KeyNum)
}

// Derived is keys(t), sources taken less doors open, the held ones aside.
// It is below 0 when more doors are open than keys were taken: §6.4's
// known exploit (a key spent on another door while a remote door's switch
// waits) or a cheat.
func (f KeyFacts) Derived() int { return f.Taken - (f.Open - f.Held) }

// CountKeys counts t's keys in v. A source counts once its flag is set, a
// door once its memory switch is (dDoor_key2_c::keyInit sets it as the
// key is spent, d_door.cpp:433-440).
func CountKeys(v View, t *keys.Table) KeyFacts {
	f := KeyFacts{Table: t.SaveTable, Sources: len(t.Sources), Doors: len(t.Doors)}
	if !addr.ValidTable(t.SaveTable) {
		return f
	}
	for _, s := range t.Sources {
		if set, _ := v.FlagSet(s.Flag); set {
			f.Taken++
		}
	}
	for _, d := range t.Doors {
		if set, _ := v.SwitchSet(t.SaveTable, d.Switch); set {
			f.Open++
		}
	}
	f.Have = v.KeyNum(t.SaveTable)
	return f
}

// KeysEnabled reports whether t's keys may be derived, or why not: its M1
// invariant must pass (sources == doors == the vanilla count, every source
// and door recorded in a saved bit; Appendix C item 21), and every source
// flag and door switch must be one View reads.
func KeysEnabled(t *keys.Table) (bool, string) {
	switch {
	case !addr.ValidTable(t.SaveTable):
		return false, fmt.Sprintf("save table %d is past the last (0x%X)", t.SaveTable, addr.TableCount-1)
	case !t.Pass && len(t.Problems) > 0:
		return false, "its M1 key invariant fails (coop-tables): " + strings.Join(t.Problems, "; ")
	case !t.Pass:
		return false, "its M1 key invariant fails (coop-tables)"
	case t.Expected == 0:
		return false, "it is no dungeon with small keys"
	}
	for i, s := range t.Sources {
		if !Known(s.Flag) {
			return false, fmt.Sprintf("source %d: %s is no save bit the derive reads", i+1, s.Flag)
		}
	}
	for i, d := range t.Doors {
		if store, _, err := addr.SwitchBit(d.Switch); err != nil || store != addr.SwitchMemory {
			return false, fmt.Sprintf("door %d: switch 0x%02X is no memory switch", i+1, d.Switch)
		}
	}
	return true, ""
}

// KeyTarget is the count f's table should hold: keys(t) plus extra, the
// keys from sources the table doesn't know, clamped to 0..MaxKeys. The
// clamp comes last, so every door open lowers the target until it is 0,
// an extra key's door too.
func KeyTarget(f KeyFacts, extra int) int {
	return min(max(f.Derived()+extra, 0), MaxKeys)
}

// KeyContext is what PlanKeys needs beyond a table's facts.
type KeyContext struct {
	// Extra is the keys the table can't account for (see the top of this
	// file).
	Extra int
	// Loaded: the table is the one the live memBit holds (mDan.mStageNo).
	Loaded bool
	// KeyHUD: the loaded stage shows the key counter (Stag.KeyHUD).
	KeyHUD bool
}

// PlanKeys plans the write that brings f's table to KeyTarget(f,
// c.Extra), by §6.4's path for it:
//
//   - the loaded table with the key HUD on: the difference into
//     mItemKeyNumCount, which dMeter_keyMove applies and animates;
//   - the loaded table with it off: its live mKeyNum, the only copy the
//     game reads while the table is loaded (View.KeyNum). putSave copies
//     it to the saved slot as the stage goes (d_save.cpp:1512-1515), so the
//     saved byte is left alone;
//   - any other table: its saved mKeyNum, which getSave loads with the
//     table (d_save.cpp:1506-1509).
//
// ok is false when the table holds its target already, or more than
// MaxKeys, which the HUD clamps itself at the next count. The caller
// checks the gates: IDLE, and for the counter that it reads 0.
func PlanKeys(f KeyFacts, c KeyContext) (w Write, ok bool) {
	t := f.Table
	want := KeyTarget(f, c.Extra)
	if !addr.ValidTable(t) || f.Have < 0 || f.Have > MaxKeys || want == f.Have {
		return Write{}, false
	}
	why := fmt.Sprintf("small keys, table %d: %d -> %d: %d of %d source(s) taken, %d of %d door(s) open",
		t, f.Have, want, f.Taken, f.Sources, f.Open, f.Doors)
	if f.Held > 0 {
		why += fmt.Sprintf(" (%d counted after your next room change)", f.Held)
	}
	if c.Extra > 0 {
		why += fmt.Sprintf(", %d kept from sources the table doesn't know", c.Extra)
	}
	switch {
	case c.Loaded && c.KeyHUD:
		w = counterWrite(KeyCounter, addr.ItemKeyNumCount, want-f.Have, why)
		w.Table = t
		return w, true
	case c.Loaded:
		return Write{Kind: KeyLive, Table: t, Addr: addr.LiveKeyNum,
			Data: []byte{byte(want)}, Expect: []byte{byte(f.Have)}, Reason: why}, true
	}
	a, _ := addr.SavedMemBitAddr(t)
	return Write{Kind: KeySaved, Table: t, Addr: a + addr.MemBitOffKeyNum,
		Data: []byte{byte(want)}, Expect: []byte{byte(f.Have)}, Reason: why}, true
}

// tableName is what the notes call t.
func tableName(t *keys.Table) string {
	if t.Dungeon != "" {
		return fmt.Sprintf("small keys, table %d (%s)", t.SaveTable, t.Dungeon)
	}
	return fmt.Sprintf("small keys, table %d", t.SaveTable)
}

// keyState is the Deriver's memory of one enabled table.
type keyState struct {
	t    *keys.Table
	name string

	base  bool // the file's baseline has been taken
	extra int  // the keys from sources the table doesn't know (the top of this file)
	// The count, sources taken and doors counted open the derive last
	// settled, its own write included: what a rise, and a key spent on a
	// held door, are measured from.
	settledHave, settledTaken, settledOpen int

	// The last tick's count, flags and pending counter, and when they
	// last changed.
	sig     keySig
	sigHave bool
	changed time.Time

	pend *pending

	// The stage visit's: each source's flag as the visit began, and the
	// sources a duplicate was taken against.
	visitHave      bool
	visitSet, used []bool

	// The doors held (the top of this file), by index in t.Doors, and
	// what the last tick saw: whether the table was loaded, in which
	// stage, and each door's switch.
	held       []heldDoor
	lastLoaded bool
	lastStage  string
	lastOpen   []bool
}

// heldDoor is a door whose switch co-op wrote into the live copy. room is
// mStayNo on the first tick after the write (roomSet); a later tick with
// another counts the door.
type heldDoor struct {
	on, roomSet bool
	room        int8
}

type keySig struct {
	have, taken, open, held int
	counter                 int16
}

func newKeyState(t *keys.Table) *keyState {
	return &keyState{t: t, name: tableName(t), visitSet: make([]bool, len(t.Sources)), used: make([]bool, len(t.Sources)),
		held: make([]heldDoor, len(t.Doors)), lastOpen: make([]bool, len(t.Doors))}
}

func (k *keyState) reset() {
	k.base, k.extra, k.sigHave, k.pend, k.visitHave = false, 0, false, nil, false
	clear(k.held)
	k.lastLoaded, k.lastStage = false, ""
}

// heldSwitches lists the switches of the doors held as the last tick saw
// them: a door WroteLive held since is not yet among them, nor among the
// doors the last tick counted open.
func (k *keyState) heldSwitches() []int {
	var sw []int
	for i, h := range k.held {
		if h.on && h.roomSet {
			sw = append(sw, k.t.Doors[i].Switch)
		}
	}
	return sw
}

// doorPlacedIn reports whether door d has an entry in stage.
func doorPlacedIn(d keys.Door, stage string) bool {
	for _, e := range d.At {
		if e.Stage == stage {
			return true
		}
	}
	return false
}

// wroteLive holds each door of the table whose switch is among set, bits
// co-op set in image byte off's live copy, and returns a note for each:
// only while the last tick had the table loaded, in a stage where the
// door is placed, and its switch not yet set.
func (k *keyState) wroteLive(off int, set byte) []string {
	base := addr.OffMemBit(k.t.SaveTable)
	if !k.lastLoaded || off < base || off >= base+addr.MemBitSize {
		return nil
	}
	var notes []string
	for i, d := range k.t.Doors {
		_, b, err := addr.SwitchBit(d.Switch)
		if err != nil || base+b.Off != off || set&b.Mask == 0 || k.lastOpen[i] || k.held[i].on || !doorPlacedIn(d, k.lastStage) {
			continue
		}
		k.held[i] = heldDoor{on: true}
		notes = append(notes, fmt.Sprintf("%s: door 0x%02X is counted after your next room change: its switch is written, but the door shows its lock until then, and its key stays",
			k.name, d.Switch))
	}
	return notes
}

// release counts held door i again, and notes why.
func (k *keyState) release(i int, p *Plan, why string) {
	k.held[i] = heldDoor{}
	p.note("%s: door 0x%02X counted: %s", k.name, k.t.Doors[i].Switch, why)
}

// holds keeps the held doors for one tick (the top of this file) and
// returns how many stay held: each is counted again once Link's room
// changes, he leaves its stage or table, or its switch reads clear.
func (k *keyState) holds(c *tick, p *Plan, loaded, visit bool) (n int) {
	s := c.in.Snap
	for i, d := range k.t.Doors {
		set, _ := c.v.SwitchSet(k.t.SaveTable, d.Switch)
		k.lastOpen[i] = set
		h := &k.held[i]
		switch {
		case !h.on:
			continue
		case !loaded || visit:
			k.release(i, p, "you left its stage, whose doors are made again reading it")
			continue
		case !set:
			k.held[i] = heldDoor{} // the game cleared it: nothing to count
			continue
		case !h.roomSet:
			h.room, h.roomSet = s.StayNo, true
		case s.StayNo != h.room:
			k.release(i, p, "your room changed, so the door looked at its switch again and dropped its lock")
			continue
		}
		n++
	}
	k.lastLoaded, k.lastStage = loaded, s.StageName
	return n
}

// spent counts held doors again for keys the count lost that no door
// counted open since the derive last settled the table explains: this
// game spent them on held doors, whose locks keyInit took off with them
// (d_door.cpp:433-447). It returns how many it counted.
func (k *keyState) spent(f KeyFacts, p *Plan) int {
	lost := k.settledHave - f.Have - max(0, f.Open-f.Held-k.settledOpen)
	n := 0
	for i := range k.held {
		if n >= lost {
			break
		}
		if k.held[i].on {
			k.release(i, p, "this game spent a key on it")
			n++
		}
	}
	return n
}

// startVisit begins a stage visit: the flags set now are older than every
// actor the stage makes.
func (k *keyState) startVisit(v View) {
	for i, s := range k.t.Sources {
		k.visitSet[i], _ = v.FlagSet(s.Flag)
		k.used[i] = false
	}
	k.visitHave = true
}

// duplicates takes up to n duplicates against the visit's sources that
// may have given their key twice (the top of this file), and names them.
func (k *keyState) duplicates(v View, stage string, n int) (got int, from []string) {
	for i, s := range k.t.Sources {
		if got == n {
			break
		}
		set, _ := v.FlagSet(s.Flag)
		if !set || k.visitSet[i] || k.used[i] || !placedIn(s.At, stage) {
			continue
		}
		k.used[i] = true
		got++
		from = append(from, s.Flag.String())
	}
	return got, from
}

// tick derives the table's keys for one tick.
func (k *keyState) tick(d *Deriver, c *tick, p *Plan, visit bool) {
	v, now := c.v, c.in.Now
	f := CountKeys(v, k.t)
	loaded := k.t.SaveTable == v.Table()
	var counter int16
	if loaded {
		counter = c.in.Snap.KeyNumCount
	}
	if visit || !k.visitHave {
		k.startVisit(v)
	}
	f.Held = k.holds(c, p, loaded, visit)
	if sig := (keySig{f.Have, f.Taken, f.Open, f.Held, counter}); !k.sigHave || sig != k.sig {
		k.sig, k.sigHave, k.changed = sig, true, now
	}
	if k.pend != nil {
		switch {
		case f.Have == k.pend.want:
			k.pend = nil
		case loaded && counter != 0:
			// Still in flight: the HUD takes the counter only on a frame
			// dMeter_Execute runs (dMeter_keyMove, d_meter.cpp:5247-5258,
			// :7395), and a paused or stalled game runs none. Expired
			// here, a -1 landing late would read as a key this game spent
			// on a held door (spent), and the door would be counted with
			// its lock still up.
		case !now.Before(k.pend.expires):
			// Never seen to land: it is not what the count rose from.
			k.settledHave, k.pend = k.pend.prev, nil
		}
	}
	if !k.base {
		if loaded && c.in.Stag.KeyHUD && counter != 0 {
			return // a count lands this frame: measure after it
		}
		// extra starts at 0 (the top of this file): what the file holds
		// beyond keys(t) now is no rise, and is corrected once settled.
		k.base, k.extra = true, 0
		k.settledHave, k.settledTaken, k.settledOpen = f.Have, f.Taken, f.Open-f.Held
		if want := KeyTarget(f, 0); f.Have > want {
			p.note("%s: %d key(s) beyond the %d its flags give, a door's key not yet taken or a duplicate: taken out once the table settles",
				k.name, f.Have-want, want)
		}
	}
	// A count pending for the loaded table changes it at the HUD's next
	// frame (or, with the key HUD off, at a later stage's): wait for it.
	if k.pend != nil || loaded && counter != 0 || !d.settled(c, k.changed) {
		return
	}
	f.Held -= k.spent(f, p)
	rise := f.Have - k.settledHave - max(0, f.Taken-k.settledTaken)
	if over := f.Have - KeyTarget(f, k.extra); rise > 0 && over > 0 {
		rise = min(rise, over)
		n, from := k.duplicates(v, c.in.Snap.StageName, rise)
		if n > 0 {
			p.note("%s: %d duplicate key(s), from %s given again by actors older than their flag: removed",
				k.name, n, strings.Join(from, ", "))
		}
		if kept := rise - n; kept > 0 {
			k.extra += kept
			p.note("%s: %d key(s) no known source explains: kept (%d kept in all)", k.name, kept, k.extra)
		}
	}
	k.settledHave, k.settledTaken, k.settledOpen = f.Have, f.Taken, f.Open-f.Held
	if !c.canWrite() || loaded && c.in.Stag.KeyHUD && !c.in.Gates.CanWriteCounter(gate.KeyNum) {
		return
	}
	w, ok := PlanKeys(f, KeyContext{Extra: k.extra, Loaded: loaded, KeyHUD: c.in.Stag.KeyHUD})
	if !ok {
		return
	}
	p.Writes = append(p.Writes, w)
	k.pend = &pending{want: KeyTarget(f, k.extra), prev: f.Have, expires: now.Add(d.inFlight)}
	k.settledHave = k.pend.want
}
