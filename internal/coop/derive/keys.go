package derive

import (
	"fmt"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/gate"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/keys"
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

// MaxKeys is the most keys a table holds: dMeter_keyMove clamps mKeyNum to
// 0..99 (d_meter.cpp:5248-5254).
const MaxKeys = 99

// KeyFacts are one table's small keys in a View.
type KeyFacts struct {
	Table          int
	Sources, Taken int // the table's key sources, and those whose flag is set
	Doors, Open    int // its key doors, and those whose switch is set
	Have           int // the mKeyNum the table holds (View.KeyNum)
}

// Derived is keys(t), sources taken less doors open. It is below 0 when
// more doors are open than keys were taken: §6.4's known exploit (a key
// spent on another door while a remote door's switch waits) or a cheat.
func (f KeyFacts) Derived() int { return f.Taken - f.Open }

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
	// The count and sources taken the derive last settled, its own write
	// included: what a rise is measured from.
	settledHave, settledTaken int

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
}

type keySig struct {
	have, taken, open int
	counter           int16
}

func newKeyState(t *keys.Table) *keyState {
	return &keyState{t: t, name: tableName(t), visitSet: make([]bool, len(t.Sources)), used: make([]bool, len(t.Sources))}
}

func (k *keyState) reset() {
	k.base, k.extra, k.sigHave, k.pend, k.visitHave = false, 0, false, nil, false
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
	if sig := (keySig{f.Have, f.Taken, f.Open, counter}); !k.sigHave || sig != k.sig {
		k.sig, k.sigHave, k.changed = sig, true, now
	}
	if k.pend != nil {
		switch {
		case f.Have == k.pend.want:
			k.pend = nil
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
		k.settledHave, k.settledTaken = f.Have, f.Taken
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
	k.settledHave, k.settledTaken = f.Have, f.Taken
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
