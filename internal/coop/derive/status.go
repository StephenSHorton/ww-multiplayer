package derive

import (
	"fmt"
	"strings"
)

// Status is what the Deriver derives and how each derive stands, for a
// session's startup lines and its /status: every key table the M1 tables
// list, derived or not, and whether max life is. Max magic needs no table
// and is always derived.
type Status struct {
	Keys []KeyStatus // in the key table's order (save table order)
	// Life reports whether max life is derived; LifeWhy says why not.
	Life    bool
	LifeWhy string
	// NoKeyTable: the tables have no key table at all (no M1 tables).
	NoKeyTable bool
}

// KeyStatus is one save table's small keys.
type KeyStatus struct {
	Table   int
	Dungeon string // its dungeon's short name, "" for none
	// On: the table's keys are derived (KeysEnabled); Why says why not.
	On  bool
	Why string
	// Seen: the Deriver has measured the loaded file's keys of the table
	// (its baseline). The rest is the last tick's, and means nothing
	// before.
	Seen  bool
	Facts KeyFacts // over the file's own flags
	// Extra is the keys kept from sources the table doesn't know, Target
	// the count the table should hold (KeyTarget).
	Extra, Target int
	// Pending: a key write is in flight, not yet seen to land.
	Pending bool
	// Held are the switches of the doors held: open, but counted only
	// after Link's next room change (keys.go). Facts.Held is their number.
	Held []int
}

// Name is what a status line calls the table: "table 3 (DRC)".
func (k KeyStatus) Name() string {
	if k.Dungeon != "" {
		return fmt.Sprintf("table %d (%s)", k.Table, k.Dungeon)
	}
	return fmt.Sprintf("table %d", k.Table)
}

// String is the table's keys in one phrase: "table 3 (DRC): 1 key, 2 of 4
// source(s) taken, 1 of 4 door(s) open", "table 3 (DRC): not seen yet" or
// "table 9: not derived: why".
func (k KeyStatus) String() string {
	switch {
	case !k.On:
		return k.Name() + ": not derived: " + k.Why
	case !k.Seen:
		return k.Name() + ": not seen yet (it is measured on the file's first tick in game)"
	}
	f := k.Facts
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %d key(s), %d of %d source(s) taken, %d of %d door(s) open", k.Name(), f.Have, f.Taken, f.Sources, f.Open, f.Doors)
	if len(k.Held) > 0 {
		sw := make([]string, len(k.Held))
		for i, s := range k.Held {
			sw[i] = fmt.Sprintf("0x%02X", s)
		}
		fmt.Fprintf(&b, " (door %s counted after your next room change: its lock stays until then)", strings.Join(sw, ", "))
	}
	if k.Extra > 0 {
		fmt.Fprintf(&b, ", %d kept from sources the table doesn't know", k.Extra)
	}
	switch {
	case k.Pending:
		fmt.Fprintf(&b, "; %d on its way", k.Target)
	case k.Target != f.Have:
		fmt.Fprintf(&b, "; to become %d once IDLE and settled", k.Target)
	}
	return b.String()
}

// Status returns the Deriver's state: what its tables let it derive, and
// for each key table the last tick it saw of the loaded file.
func (d *Deriver) Status() Status {
	st := Status{Life: d.life != nil, LifeWhy: d.lifeWhy, NoKeyTable: d.noKeys}
	on := make(map[int]*keyState, len(d.keys))
	for _, k := range d.keys {
		on[k.t.SaveTable] = k
	}
	for _, t := range d.keyTables {
		ks := KeyStatus{Table: t.SaveTable, Dungeon: t.Dungeon}
		k := on[t.SaveTable]
		if k == nil {
			_, ks.Why = KeysEnabled(t)
			st.Keys = append(st.Keys, ks)
			continue
		}
		ks.On, ks.Seen = true, k.base && k.sigHave
		ks.Held = k.heldSwitches()
		ks.Facts = KeyFacts{Table: t.SaveTable, Sources: len(t.Sources), Taken: k.sig.taken,
			Doors: len(t.Doors), Open: k.sig.open, Held: len(ks.Held), Have: k.sig.have}
		ks.Extra, ks.Target, ks.Pending = k.extra, KeyTarget(ks.Facts, k.extra), k.pend != nil
		st.Keys = append(st.Keys, ks)
	}
	return st
}
