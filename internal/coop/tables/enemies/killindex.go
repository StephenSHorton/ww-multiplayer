package enemies

import (
	"fmt"
	"strings"
	"sync"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/stagedata"
)

// What M6's kill union reads of the enemy table (docs/08-coop-design.md
// §7.2, M6.0): which (stage, room, setID) keys a partner's death may be
// written into this game's zone actor bits for, and light-shrink a live
// instance of, and how much of the disc that covers.
//
// A key is synced when one of its entries is a K1 class's (§7.2's
// applier can kill it) and it is:
//
//   - Keyed: setID < 0x200 in a room's file, which onActor records and
//     isActor checks (d_save.cpp:1671-1701). setID 0xFFFF is never
//     recorded (:1672-1674), so an unkeyed enemy stays each player's own
//     until M6.1;
//   - not BOSS: the game gives the actor fopAcStts_BOSS_e on creation
//     (Class.IsBoss), as a Bokoblin of type 0xA, whose mini-boss music only
//     its own death stops (d_a_bk.cpp:2639);
//   - not Morth: every placed KS is a generator, which spawns children with
//     setID 0xFFFF and never exists itself (d_a_ks.cpp:1582-1610, :1219-1283);
//   - not Shared: no other enemy on a common layer has the key, so one
//     death can't stand for two.
//
// The index is built on first use and kept out of the JSON the cache
// stores, so the table's schema does not change.

// KillSet is a room's synced keys: bit b of word w is setID 32w+b, as the
// zone actor field holds them (dSv_zoneActor_c::on, d_save.cpp:1326-1329).
type KillSet [MaxSetID / 32]uint32

// Has reports whether setID is in s.
func (s *KillSet) Has(setID int) bool {
	return setID >= 0 && setID < MaxSetID && s[setID/32]&(1<<(setID%32)) != 0
}

func (s *KillSet) add(setID int) { s[setID/32] |= 1 << (setID % 32) }

// KillCoverage is how much of the disc M6.0's kill union reaches: the K1
// classes' entries without BOSS status, by what becomes of their deaths.
type KillCoverage struct {
	// Entries counts the K1 entries without BOSS status; Keyed those of
	// them that are Keyed, Unkeyed the rest.
	Entries, Keyed, Unkeyed int
	// The Keyed ones, each in one class:
	//   - Sync: synced, and light-shrunk now;
	//   - Unverified: synced, but light-shrunk only once the owner's live
	//     check verifies their class (Class.K1Verified), by class;
	//   - IceOnly: synced, but a Kargaroc records its death only when it
	//     dies on ice or by a Light Arrow (enemy_ice, c_damagereaction.cpp:
	//     159, :369), never by its own death (d_a_bb.cpp:1969-1972);
	//   - NeverLive: a Morth generator, never synced;
	//   - Shared: a key another enemy on a common layer has, never synced.
	Sync, IceOnly, NeverLive, Shared int
	Unverified                       []ClassCount
	// Rooms counts the (stage, room)s with K1 entries: AllKeyed those
	// whose every K1 entry is Keyed, Mixed those with both, NoneKeyed
	// those with none. AllKeyed looks at K1 entries without BOSS status
	// only: Clearable counts those of them whose every enemy-group entry,
	// of any class and on every layer, is one M6.0 removes on B by
	// itself (synced, of a verified class, not BOSS, and not a Kargaroc,
	// whose own death is never recorded), so that B's room-clear code
	// (fopAcM_myRoomSearchEnemy) finds the room empty once A's kills
	// arrive. ENV-group entries (Morth) are not counted by it
	// (enemySearchJugge, f_op_actor_mng.cpp:1275) and do not block.
	Rooms, AllKeyed, Mixed, NoneKeyed, Clearable int
	// ByClass and ByStage count each class's and stage's K1 entries, how
	// many are Keyed and how many synced, in Classes and table order.
	ByClass []KillShare
	ByStage []KillShare
}

// ClassCount is a count for one class.
type ClassCount struct {
	Class string
	N     int
}

// KillShare counts K1 entries without BOSS status.
type KillShare struct {
	Name                   string // the class or stage
	Entries, Keyed, Synced int
}

// UnverifiedTotal sums Unverified.
func (c *KillCoverage) UnverifiedTotal() int {
	n := 0
	for _, u := range c.Unverified {
		n += u.N
	}
	return n
}

// Synced is how many Keyed entries are synced: Sync, Unverified and
// IceOnly.
func (c *KillCoverage) Synced() int { return c.Sync + c.UnverifiedTotal() + c.IceOnly }

type killRoom struct {
	stage string
	room  int
}

type killKey struct {
	killRoom
	setID uint16
}

type killIndex struct {
	masks map[killRoom]*KillSet
	cov   KillCoverage
}

// killCache holds the index; the zero value has none yet.
type killCache struct {
	once sync.Once
	idx  *killIndex
}

func (t *Table) kills() *killIndex {
	t.kill.once.Do(func() { t.kill.idx = buildKillIndex(t.Enemies) })
	return t.kill.idx
}

// KillMask returns the synced keys of (stage, room): what M6 writes into
// the room's zone actor bits, and light-shrinks. A room with none, or
// outside 0..63, has an empty set.
func (t *Table) KillMask(stage string, room int) KillSet {
	if s := t.kills().masks[killRoom{stage, room}]; s != nil {
		return *s
	}
	return KillSet{}
}

// KeyedK1 reports whether (stage, room, setID) is a synced key.
func (t *Table) KeyedK1(stage string, room, setID int) bool {
	s := t.kills().masks[killRoom{stage, room}]
	return s != nil && s.Has(setID)
}

// Coverage returns how much of the table M6.0's kill union reaches.
func (t *Table) Coverage() KillCoverage { return t.kills().cov }

// countsAsK1 reports whether e counts in the coverage: a K1 class's
// entry without BOSS status.
func countsAsK1(e Entry) (Class, bool) {
	c, ok := ClassOf(e.Proc)
	return c, ok && c.Kind == K1 && !e.Boss
}

func buildKillIndex(es []Entry) *killIndex {
	idx := &killIndex{masks: map[killRoom]*KillSet{}}
	cov := &idx.cov
	type roomCount struct{ keyed, unkeyed int }
	rooms := map[killRoom]*roomCount{}
	var roomOrder []killRoom
	byClass := map[string]*KillShare{}
	byStage := map[string]*KillShare{}
	var stageOrder []string
	unverified := map[string]int{}
	// A key with a K1 entry that is excluded (BOSS, Morth, shared) is not
	// synced, whatever its other entries are: one bit stands for all of
	// them (§7.2).
	refused := map[killKey]bool{}
	for _, e := range es {
		c, k1 := ClassOf(e.Proc)
		if e.Key != Keyed || !k1 || c.Kind != K1 {
			continue
		}
		if e.Boss || e.Proc == stagedata.ProcKS || e.Shared {
			refused[killKey{killRoom{e.Stage, e.Room}, e.SetID}] = true
		}
	}
	for _, e := range es {
		c, ok := countsAsK1(e)
		if !ok {
			continue
		}
		cov.Entries++
		r := killRoom{e.Stage, e.Room}
		rc := rooms[r]
		if rc == nil {
			rc = &roomCount{}
			rooms[r] = rc
			roomOrder = append(roomOrder, r)
		}
		cs := byClass[c.Name]
		if cs == nil {
			cs = &KillShare{Name: c.Name}
			byClass[c.Name] = cs
		}
		ss := byStage[e.Stage]
		if ss == nil {
			ss = &KillShare{Name: e.Stage}
			byStage[e.Stage] = ss
			stageOrder = append(stageOrder, e.Stage)
		}
		cs.Entries++
		ss.Entries++
		if e.Key != Keyed {
			cov.Unkeyed++
			rc.unkeyed++
			continue
		}
		cov.Keyed++
		rc.keyed++
		cs.Keyed++
		ss.Keyed++
		switch {
		case e.Proc == stagedata.ProcKS:
			cov.NeverLive++
			continue
		case e.Shared:
			cov.Shared++
			continue
		case refused[killKey{r, e.SetID}]:
			// Another K1 entry of the key is BOSS: counted as shared.
			cov.Shared++
			continue
		case e.Proc == stagedata.ProcBB:
			cov.IceOnly++
		case !c.K1Verified:
			unverified[c.Name]++
		default:
			cov.Sync++
		}
		cs.Synced++
		ss.Synced++
		m := idx.masks[r]
		if m == nil {
			m = new(KillSet)
			idx.masks[r] = m
		}
		m.add(int(e.SetID))
	}
	for _, c := range Classes {
		if n := unverified[c.Name]; n > 0 {
			cov.Unverified = append(cov.Unverified, ClassCount{c.Name, n})
		}
		if cs := byClass[c.Name]; cs != nil {
			cov.ByClass = append(cov.ByClass, *cs)
		}
	}
	for _, n := range stageOrder {
		cov.ByStage = append(cov.ByStage, *byStage[n])
	}
	// A room's enemies M6.0 leaves on B: any enemy-group entry that is
	// not a synced key of a verified class whose death it records.
	blocked := map[killRoom]bool{}
	for _, e := range es {
		c, ok := ClassOf(e.Proc)
		if !ok || c.Group != GroupEnemy {
			continue
		}
		r := killRoom{e.Stage, e.Room}
		m := idx.masks[r]
		clears := c.Kind == K1 && !e.Boss && e.Key == Keyed && e.Proc != stagedata.ProcBB && c.K1Verified &&
			m != nil && m.Has(int(e.SetID))
		if !clears {
			blocked[r] = true
		}
	}
	for _, r := range roomOrder {
		rc := rooms[r]
		cov.Rooms++
		switch {
		case rc.unkeyed == 0:
			cov.AllKeyed++
			if !blocked[r] {
				cov.Clearable++
			}
		case rc.keyed == 0:
			cov.NoneKeyed++
		default:
			cov.Mixed++
		}
	}
	return idx
}

// Line is the coverage in one line, for coop-tables' acceptance block.
func (c KillCoverage) Line() string {
	var b strings.Builder
	fmt.Fprintf(&b, "K1 classes not BOSS: %d entries; %s keyed: %d sync", c.Entries, pct(c.Keyed, c.Entries), c.Sync)
	if n := c.UnverifiedTotal(); n > 0 {
		var by []string
		for _, u := range c.Unverified {
			by = append(by, fmt.Sprintf("%s %d", u.Class, u.N))
		}
		fmt.Fprintf(&b, ", %d after live checks (%s)", n, strings.Join(by, ", "))
	}
	fmt.Fprintf(&b, ", %d Kargaroc only by ice or light arrow, %d Morth never live", c.IceOnly, c.NeverLive)
	if c.Shared > 0 {
		fmt.Fprintf(&b, ", %d sharing a key, never synced", c.Shared)
	}
	fmt.Fprintf(&b, "; %s unkeyed stay per player until M6.1; rooms: %d with K1 enemies, %d all keyed (%d clear on B), %d mixed, %d none",
		pct(c.Unkeyed, c.Entries), c.Rooms, c.AllKeyed, c.Clearable, c.Mixed, c.NoneKeyed)
	return b.String()
}

// report writes the coverage section of the enemy report: the line,
// then each class's and each stage's keyed and synced entries.
func (c KillCoverage) report(b *strings.Builder) {
	b.WriteString("\nM6 kill coverage (§7.2: a partner's death of a keyed K1 enemy, not BOSS, is written into this\n" +
		"game's zone and light-shrinks a live one; unkeyed enemies stay each player's own until M6.1):\n")
	fmt.Fprintf(b, "  %s\n", c.Line())
	b.WriteString("  (\"all keyed\" counts K1 entries only; \"clear on B\" counts the rooms whose every enemy-group\n" +
		"  entry, of any class, is a synced key of a verified class that records its own death, so B's\n" +
		"  room-clear code finds the room empty once A's kills arrive)\n")
	if c.UnverifiedTotal() > 0 {
		b.WriteString("  (after live checks: their zone bits sync, but the light-shrink waits for the owner's live check\n" +
			"  of the class's enemyice offset, docs/08 §8.2 K1)\n")
	}
	header := fmt.Sprintf("  %-10s %7s  %-15s %-15s\n", "", "entries", "keyed", "synced")
	line := func(s KillShare) {
		fmt.Fprintf(b, "  %-10s %7d  %-15s %-15s\n", s.Name, s.Entries, pct(s.Keyed, s.Entries), pct(s.Synced, s.Entries))
	}
	b.WriteString("  By class:\n")
	b.WriteString(header)
	for _, s := range c.ByClass {
		line(s)
	}
	b.WriteString("  By stage (stages with a synced entry):\n")
	b.WriteString(header)
	none := 0
	for _, s := range c.ByStage {
		if s.Synced == 0 {
			none++
			continue
		}
		line(s)
	}
	fmt.Fprintf(b, "  %d other stage(s) with K1 entries have none synced\n", none)
}
