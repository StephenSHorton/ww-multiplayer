// Package derive computes what each receiver derives from its own applied
// flags (docs/08-coop-design.md §3, §6.4) and plans the writes that bring
// the game to it:
//
//   - small keys per save table, keys(t) = |key sources set| - |key doors
//     set| over the receiver's own flags (keys.go);
//   - max life, 12 + pieces + 4 * containers quarter hearts, add-only
//     (life.go);
//   - max magic, from the Deku Leaf and the magic upgrade, add-only
//     (magic.go).
//
// The flags are the receiver's own: the live memBit ORed with the saved
// slot for the loaded table, the saved slot for the others (View), never
// the world image. A remote door therefore takes its key only once its
// switch is in the receiver's copy and, when co-op wrote it into the live
// copy, once Link's room has changed since, when the door reads the
// switch again and drops its lock (§6.1; WroteLive, keys.go). So no key is
// ever owed to a door that still locks.
//
// Every planner (PlanKeys, PlanLife, PlanMagic) is a pure function of the
// facts it is given and returns at most one Write, with its reason. The
// Deriver keeps what they can't see, tick to tick: when each table last
// changed (settling), the writes in flight, the keys and quarter hearts
// the tables can't account for, and the stage visit's duplicates. The
// replica routes its writes through the WriteGuard (replica.TargetDerived
// for the key bytes, replica.TargetCounters for the counters): nothing
// here writes game memory.
//
// The HUD's pending counters, as the decomp has them:
//
//   - mItemKeyNumCount: dMeter_keyMove adds it to the loaded table's live
//     mKeyNum, clamped to 0..99, and zeroes it, but only while the stage's
//     STAG shows the key counter (d_meter.cpp:5242-5257). A negative count
//     is taken, and at 0 keys it stops at 0. With the counter hidden the
//     count stays pending until the next stage that shows it, where it
//     would land on that stage's table; only itemInit clears it
//     (d_com_inf_game.cpp:72-84), from the file select (d_s_name.cpp:1253,
//     :1374). So the counter is written only with the key HUD on, and the
//     live byte otherwise. The game itself counts a key in (+1,
//     item_func_small_key, d_item.cpp:666) and out (-1, with the door's
//     switch, dDoor_key2_c::keyInit, d_door.cpp:433-440) through it.
//   - mItemMaxLifeCount: dMeter_LifeMove adds it to mMaxLife, capped at
//     0x50, and zeroes it; a positive count also refills life to the new
//     maximum's whole hearts (d_meter.cpp:1420-1452, refill :1426-1427).
//     A heart piece adds 1, a container 4 (item_func_kakera_heart,
//     d_item.cpp:583-585; item_func_utuwa_heart, :588-590); a new file
//     starts at 12 (dSv_player_status_a_c::init, d_save.cpp:26).
//   - mItemMaxMagicCount: dMeter_magicMove adds it to mMaxMagic, capped at
//     0x20, and zeroes it (d_meter.cpp:4069-4076). It does not fill
//     mMagic: the meter only clamps mMagic to the maximum (:4105-4107).
//
// Each counter is written only while it reads 0 (gate.CanWriteCounter),
// and each Write carries the bytes it expects to overwrite, so that the
// router can make it a compare-and-set: the game adds to the same
// counters, and changes mKeyNum, between the snapshot and the write.
//
// Citations: file:line is the zeldaret/tww decomp at commit 4d78c994, as
// in internal/coop/addr.
package derive

import (
	"encoding/binary"
	"fmt"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/bind"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/gate"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/mem"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/snap"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/hearts"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/tables/keys"
)

// Kind is what a Write changes.
type Kind uint8

const (
	// KeyCounter is a delta into mItemKeyNumCount (addr.ItemKeyNumCount),
	// for the loaded table while its stage shows the key counter.
	KeyCounter Kind = iota + 1
	// KeyLive is the loaded table's live mKeyNum (addr.LiveKeyNum), while
	// its stage hides the key counter.
	KeyLive
	// KeySaved is the saved mKeyNum of a table that is not loaded
	// (addr.SavedMemBit + 0x24 * t + 0x20).
	KeySaved
	// LifeCounter is a delta into mItemMaxLifeCount
	// (addr.ItemMaxLifeCount), quarter hearts.
	LifeCounter
	// MagicCounter is a delta into mItemMaxMagicCount
	// (addr.ItemMaxMagicCount).
	MagicCounter
)

func (k Kind) String() string {
	switch k {
	case KeyCounter:
		return "key counter"
	case KeyLive:
		return "live mKeyNum"
	case KeySaved:
		return "saved mKeyNum"
	case LifeCounter:
		return "max-life counter"
	case MagicCounter:
		return "max-magic counter"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Write is one planned game write: Data at Addr, to be made only while
// the bytes at Addr still read Expect. A counter's Data is its s16 delta,
// big-endian, and its Expect is 0; a key byte's Expect is the count the
// plan saw.
type Write struct {
	Kind Kind
	// Table is the save table a key write is for; -1 for max life and
	// max magic.
	Table  int
	Addr   uint32
	Data   []byte
	Expect []byte
	Reason string
}

// Counter returns the pending counter w writes, and false for a key byte.
func (w Write) Counter() (gate.Counter, bool) {
	switch w.Kind {
	case KeyCounter:
		return gate.KeyNum, true
	case LifeCounter:
		return gate.MaxLife, true
	case MagicCounter:
		return gate.MaxMagic, true
	}
	return 0, false
}

// Delta is a counter write's count, or a key byte's new value.
func (w Write) Delta() int {
	if len(w.Data) == 2 {
		return int(int16(binary.BigEndian.Uint16(w.Data)))
	}
	if len(w.Data) == 1 {
		return int(w.Data[0])
	}
	return 0
}

func (w Write) String() string {
	if _, ok := w.Counter(); ok {
		return fmt.Sprintf("%v %+d at 0x%08X: %s", w.Kind, w.Delta(), w.Addr, w.Reason)
	}
	return fmt.Sprintf("%v of table %d to %d at 0x%08X: %s", w.Kind, w.Table, w.Delta(), w.Addr, w.Reason)
}

// counterWrite is a Write of delta into the s16 counter at a.
func counterWrite(k Kind, a uint32, delta int, why string) Write {
	d := make([]byte, 2)
	binary.BigEndian.PutUint16(d, uint16(int16(delta)))
	return Write{Kind: k, Table: -1, Addr: a, Data: d, Expect: []byte{0, 0}, Reason: why}
}

// Stag is what the loaded stage's STAG chunk says (d_stage.h:1023-1029):
// the save table its mProp names, and whether the stage shows the small-
// key counter. OK is false while mpStagInfo is null or unreadable.
type Stag struct {
	OK     bool
	Table  int
	KeyHUD bool
}

// StagOf decodes a STAG mProp byte.
func StagOf(prop byte) Stag {
	return Stag{OK: true, Table: addr.StagSaveTable(prop), KeyHUD: prop&addr.StagPropKeyDisp != 0}
}

// ReadStag reads s's STAG chunk's mProp through mStageData.mpStagInfo
// (addr.StagInfoPtr). Read it after the snapshot, as the replica's
// observe does: a stage load sets mpStagInfo before getSave and initDan
// (d_stage.cpp:1670-1677), so a tick whose STAG names another table than
// mDan's caught a stage load half done, and Tick skips it.
func ReadStag(m mem.Mem, s *snap.Snapshot) Stag {
	if s == nil || s.StagInfo == 0 {
		return Stag{}
	}
	prop, err := mem.ReadU8(m, s.StagInfo+addr.StagOffProp)
	if err != nil {
		return Stag{}
	}
	return StagOf(prop)
}

// Input is one tick: the snapshot, its gates and STAG, whether the file is
// armed in the world, and the time.
type Input struct {
	Now   time.Time
	Snap  *snap.Snapshot
	Gates gate.Status
	Stag  Stag
	// Armed: the loaded file is bound to the world and the session runs
	// (replica.Tick.Armed). An unarmed tick is observed, never written.
	Armed bool
}

// Plan is one tick's writes and what the Deriver has to say about it, for
// the log. Each note is said once, when its cause first shows.
type Plan struct {
	Writes []Write
	Notes  []string
}

// DefaultSettle is how long a table's keys, or max life or max magic,
// must be unchanged, with IDLE open throughout, before they are corrected
// (§6.4: no local key change in the last second).
const DefaultSettle = time.Second

// DefaultInFlight is how long a planned write is waited for: until it
// shows in a snapshot or this long, whichever comes first, nothing more is
// planned for its table (§6.4: no write pending for that table).
const DefaultInFlight = 2 * time.Second

// Config sets up a Deriver.
type Config struct {
	// Tables are the M1 tables of the user's own disc. Keys are derived
	// from Tables.Keys, max life from Tables.Hearts; with either nil (or
	// Tables nil) that derive is off. Max magic needs no table.
	Tables *tables.Tables
	// Settle is DefaultSettle when 0.
	Settle time.Duration
	// InFlight is DefaultInFlight when 0.
	InFlight time.Duration
}

// Disabled is a derive the tables keep off, and why.
type Disabled struct {
	What string // "small keys, table 3 (DRC)" or "max life"
	Why  string
}

// Deriver tracks one receiver's derived values over its ticks. The zero
// Deriver is not ready; use New. It is not safe for concurrent use.
//
// Tick must see every tick of a file from the first, ahead of the
// replica's writes for that tick: the quarter hearts a file holds beyond
// what its own flags give are measured on the first tick (the baseline),
// before any world flag lands in it, and the keys' rises are measured
// from it (keys.go).
type Deriver struct {
	settle, inFlight time.Duration

	keys  []*keyState
	life  *lifeState // nil: max life is not derived
	magic magicState
	off   []Disabled
	said  bool // the disabled derives were noted

	// For Status: every key table the tables list, enabled or not, in
	// their order, and why max life is not derived.
	keyTables []*keys.Table
	noKeys    bool
	lifeWhy   string

	have bool // file holds the file being tracked
	file fileKey

	stage string // the stage visit's stage, "" between visits

	idle     bool
	idleFrom time.Time // the first tick of the IDLE run

	now time.Time // the last tick's

	notes []string // WroteLive's, for the next Tick's plan
}

// fileKey is a loaded file's identity without its tag, which a bind
// changes mid-session: the replica's (replica/differ.go keyOf).
type fileKey struct {
	slot, clearCount uint8
	name             [bind.PlayerNameSize]byte
	dateIPL          uint64
}

func keyOf(f bind.File) fileKey {
	return fileKey{slot: f.Slot, clearCount: f.ClearCount, name: f.Name, dateIPL: f.DateIPL}
}

// New returns a Deriver for cfg's tables. A key table derives only while
// its M1 invariant holds (KeysEnabled), max life only while the heart
// table passes (LifeEnabled).
func New(cfg Config) *Deriver {
	d := &Deriver{settle: cfg.Settle, inFlight: cfg.InFlight}
	if d.settle <= 0 {
		d.settle = DefaultSettle
	}
	if d.inFlight <= 0 {
		d.inFlight = DefaultInFlight
	}
	var kr *keys.Result
	var hr *hearts.Result
	if cfg.Tables != nil {
		kr, hr = cfg.Tables.Keys, cfg.Tables.Hearts
	}
	if kr == nil {
		d.noKeys = true
		d.off = append(d.off, Disabled{"small keys", "no key table (coop-tables)"})
	} else {
		for i := range kr.Tables {
			t := &kr.Tables[i]
			d.keyTables = append(d.keyTables, t)
			if ok, why := KeysEnabled(t); !ok {
				d.off = append(d.off, Disabled{tableName(t), why})
				continue
			}
			d.keys = append(d.keys, newKeyState(t))
		}
	}
	if ok, why := LifeEnabled(hr); ok {
		d.life = newLifeState(hr)
	} else {
		d.lifeWhy = why
		d.off = append(d.off, Disabled{"max life", why})
	}
	return d
}

// Disabled lists the derives the tables keep off.
func (d *Deriver) Disabled() []Disabled { return append([]Disabled(nil), d.off...) }

// Tick takes one tick and returns its plan. A nil snapshot is a tick
// whose read failed: nothing is planned, and the file's tracking starts
// over, as does a loaded save state (Gates.Regressed) and a visit to the
// title or file select, where a file may be loaded again from the card.
func (d *Deriver) Tick(in Input) Plan {
	var p Plan
	d.now = in.Now
	p.Notes, d.notes = d.notes, nil
	if !d.said {
		d.said = true
		for _, o := range d.off {
			p.note("%s: not derived: %s", o.What, o.Why)
		}
	}
	s, st := in.Snap, in.Gates
	switch {
	case s == nil, st.Regressed, outOfGame(s):
		d.reset()
		return p
	}
	if st.Idle {
		if !d.idle {
			d.idle, d.idleFrom = true, in.Now
		}
	} else {
		d.idle = false
	}
	if s.PlayerPtr == 0 || s.NextEnable != 0 {
		d.stage = "" // a stage change: the next stage's actors are new
	}
	t := int(s.Table)
	if s.PlayerPtr == 0 || !addr.ValidTable(t) || !in.Stag.OK || in.Stag.Table != t {
		return p // no stage, or one loading: the save is half copied
	}
	if k := keyOf(bind.FileOf(s)); !d.have || k != d.file {
		d.reset()
		d.have, d.file = true, k
	}
	v := ViewOf(s)
	visit := s.StageName != d.stage
	d.stage = s.StageName
	c := tick{in: in, v: v, settledSince: d.idleFrom}
	for _, k := range d.keys {
		k.tick(d, &c, &p, visit)
	}
	if d.life != nil {
		d.life.tick(d, &c, &p)
	}
	d.magic.tick(d, &c, &p)
	return p
}

// Dropped tells the Deriver that w, from the last plan, was not made (a
// dry run, a refusal, or its bytes no longer read w.Expect). Its value is
// planned again once it has settled anew, a settle time from the tick w
// was planned in.
func (d *Deriver) Dropped(w Write) {
	switch w.Kind {
	case KeyCounter, KeyLive, KeySaved:
		for _, k := range d.keys {
			if k.t.SaveTable == w.Table && k.pend != nil {
				k.settledHave, k.pend, k.changed = k.pend.prev, nil, d.now
			}
		}
	case LifeCounter:
		if d.life != nil {
			d.life.pend, d.life.changed = nil, d.now
		}
	case MagicCounter:
		d.magic.pend, d.magic.changed = nil, d.now
	}
}

// WroteLive tells the Deriver that co-op set bits set of image byte off in
// the live memBit, the loaded table's copy, after its last Tick (the
// merge path's writes, replica.Result.Wrote). A key door whose switch is
// among them is held: not counted open until Link's next room change, as
// the door keeps its lock until then (keys.go). The next Tick's plan
// notes each door held.
func (d *Deriver) WroteLive(off int, set byte) {
	if !d.have || set == 0 {
		return
	}
	for _, k := range d.keys {
		d.notes = append(d.notes, k.wroteLive(off, set)...)
	}
}

// reset forgets the file: its baselines, settling and writes in flight.
func (d *Deriver) reset() {
	d.have, d.stage, d.idle = false, "", false
	for _, k := range d.keys {
		k.reset()
	}
	if d.life != nil {
		d.life.reset()
	}
	d.magic.reset()
}

// outOfGame reports whether s is off any file's play: no stage, the title
// or file select.
func outOfGame(s *snap.Snapshot) bool {
	return s.StageName == "" || s.StageName == addr.StageTitle || s.StageName == addr.StageFileSelect
}

// tick is what every derive reads of one Tick.
type tick struct {
	in Input
	v  View
	// settledSince is when IDLE last opened: a value settles only from
	// then.
	settledSince time.Time
}

// settled reports whether a value last changed at changed has settled:
// IDLE has been open, and the value unchanged, for the settle time.
func (d *Deriver) settled(c *tick, changed time.Time) bool {
	if !d.idle {
		return false
	}
	from := changed
	if c.settledSince.After(from) {
		from = c.settledSince
	}
	return c.in.Now.Sub(from) >= d.settle
}

// canWrite reports whether the tick may make an IDLE write: armed, and
// STABLE, HEARTBEAT and IDLE open.
func (c *tick) canWrite() bool { return c.in.Armed && c.in.Gates.CanWriteIdle() }

// pending is a write in flight: until the value reads want, or until
// expires, nothing more is planned for it. prev is what the value read
// when it was planned.
type pending struct {
	want, prev int
	expires    time.Time
}

// landed reports whether p is done at now with the value reading have: it
// shows, or has expired. A nil p is done.
func (p *pending) landed(have int, now time.Time) bool {
	return p == nil || have == p.want || !now.Before(p.expires)
}

func (p *Plan) note(format string, args ...any) {
	p.Notes = append(p.Notes, fmt.Sprintf(format, args...))
}
