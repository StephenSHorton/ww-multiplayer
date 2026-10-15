package world

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
)

// The scoped stores (§4.4 CoopHub, §6.3): puzzle and kill state that only
// matters while a player is in the stage or room it belongs to. Unlike the
// image it is never saved (the game itself forgets it, see below), so it
// is not in the world file; it lives while someone occupies its scope and
// is dropped when the last occupant leaves. Presence (PRESENCE, §4.4)
// drives it.
//
// Each kind follows the game's own lifetime for the state:
//
//	kind      stored per            relayed to, sent on entry    dropped when nobody is in
//	DAN       stage, table          the stage (and table)        the table
//	ZONE      stage, table, room    the room                     the room
//	KILL      stage, table, room    the stage                    the stage
//	ROOMEDGE  never stored          the room                     -
//
// DAN: the game keeps the dungeon switches 0x80-0xBF per save table, not
// per stage. dStage_stagInfoInit calls initDan with the STAG save table at
// every stage load (d_stage.cpp:1668-1677), and dSv_danBit_c::init clears
// the switches only when that table differs from the last one
// (d_save.cpp:1225-1235); dSv_info_c::init (d_save.cpp:1392) and starting
// the game from file select (dComIfGs_resetDan, d_s_name.cpp:1489) force
// the next load to clear. So a dungeon's switches survive moving between
// its stages, and the store lives as long as anyone is in the table. Its
// content stays keyed by stage as well, because a partner's dan switches
// are applied only when the stage name and table both match (§6.3, kept
// as apply policy by Appendix C item 25): within one table, two stages'
// switches are separate entries.
//
// ZONE: a room's switches 0xC0-0xDF live in the zone the room was given
// (dSv_zoneBit_c, d_save.cpp:1279-1282), keyed by (stage, room) because
// zone slots are allocated first-free per machine (createZone,
// d_save.cpp:1525-1537). They sync only between players in the same room
// (§3), and the store is kept while that room is occupied.
//
// KILL: a zone's 512 dead-actor bits by setID (dSv_zoneActor_c::on,
// d_save.cpp:1326-1329). The game clears them when a zone is created
// (dSv_zone_c::init, d_save.cpp:1337-1341), wipes every zone on a stage
// entry at a start point >= 0 (d_stage.cpp:124-126) and removes a zone two
// room changes away (d_stage.cpp:236, 250-266), so enemies respawn when
// the stage is entered again. The store is per (stage, room) like the
// zones, and kept while anyone is in the stage (§7.2): a killer reports
// kills for every loaded room, not only the one it stands in.
//
// ROOMEDGE: the one-shot switches 0xE0-0xEF that clearRoomSwitch zeroes on
// every room change (d_save.cpp:1273-1276; d_stage.cpp:254). They are
// relayed to the players in that room at that moment and never kept
// (Appendix C item 2).
//
// Bounds. A player reports only for a scope its presence is in (Apply), so
// the ZONE stores are at most one per occupied room and the KILL stores
// 64 per occupied stage. DAN alone outlives its stage, and a player could
// name stage after stage within one table, so each table holds at most
// MaxDanStores of them. A presence change looks only at the stores it can
// enter or leave (they are indexed by stage and by table), never at all of
// them.

// Player is a co-op connection: network.CoopConn.ID, 1..255.
type Player uint8

// Place is where a player is, from their PRESENCE.
type Place struct {
	Stage string // mCurStage.mName; "" when no stage is loaded
	Table int    // mDan.mStageNo, the loaded save table; -1 when none
	Room  int    // mStayNo, the room Link is in; -1 at a stage start
}

// Nowhere is the Place of a player not in a stage: on the title or file
// select, between stages, or not in game at all.
var Nowhere = Place{Table: -1, Room: -1}

// inStage reports whether p occupies a stage: a valid stage name (1-7
// printable characters, d_resorce.cpp:707-708) that is not the title's
// "sea_T" or file select's "Name" (§3.2 STABLE), and a valid table.
func (p Place) inStage() bool {
	return validStage(p.Stage) && p.Stage != "sea_T" && p.Stage != "Name" && addr.ValidTable(p.Table)
}

// inRoom reports whether p occupies a room of its stage.
func (p Place) inRoom() bool { return p.inStage() && validRoom(p.Room) }

func (p Place) String() string {
	if !p.inStage() {
		return "nowhere"
	}
	return fmt.Sprintf("%s (table %d, room %d)", p.Stage, p.Table, p.Room)
}

// validStage reports whether s can be a stage name: char[8] with its NUL
// (dStage_startStage_c::set, d_stage.cpp:113), printable ASCII without
// spaces, like every stage name the game uses.
func validStage(s string) bool {
	if len(s) == 0 || len(s) >= addr.StageNameLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < '!' || c > '~' {
			return false
		}
	}
	return true
}

// validRoom reports whether r is a room the setters accept: 0..63
// (d_save.cpp:1553).
func validRoom(r int) bool { return r >= 0 && r < addr.RoomCount }

// ScopeKind is what a scoped store holds. The values are SCOPED's kind
// byte (§4.4; wire.ScopeKind).
type ScopeKind uint8

const (
	// ScopeDan: mDan.mSwitch, u32[2], switches 0x80-0xBF
	// (dSv_danBit_c::onSwitch, d_save.cpp:1238-1241).
	ScopeDan ScopeKind = 1
	// ScopeZone: a zone's mZoneBit.mSwitch[0..1], u16 words, switches
	// 0xC0-0xDF (dSv_zoneBit_c::onSwitch, d_save.cpp:1279-1282).
	ScopeZone ScopeKind = 2
	// ScopeRoomEdge: mZoneBit.mSwitch[2], word 2 only, switches 0xE0-0xEF.
	ScopeRoomEdge ScopeKind = 3
	// ScopeKill: a zone's mActorFlags, u32[16], dead actors by setID
	// (dSv_zoneActor_c::on, d_save.cpp:1326-1329).
	ScopeKill ScopeKind = 4
)

func (k ScopeKind) String() string {
	switch k {
	case ScopeDan:
		return "DAN"
	case ScopeZone:
		return "ZONE"
	case ScopeRoomEdge:
		return "ROOMEDGE"
	case ScopeKill:
		return "KILL"
	}
	return fmt.Sprintf("ScopeKind(%d)", uint8(k))
}

// maxWords is the most words a kind has: KILL's 16.
const maxWords = addr.ZoneActorCount / 32

// words returns the word range [first, last] of kind k and the bits a word
// holds; ok is false for an unknown kind.
func (k ScopeKind) words() (first, last int, bits uint32, ok bool) {
	switch k {
	case ScopeDan:
		return 0, addr.DanSwitchCount/32 - 1, 0xFFFFFFFF, true
	case ScopeZone:
		return 0, 1, 0xFFFF, true
	case ScopeRoomEdge:
		return 2, 2, 0xFFFF, true
	case ScopeKill:
		return 0, maxWords - 1, 0xFFFFFFFF, true
	}
	return 0, 0, 0, false
}

// Key names one scoped store.
type Key struct {
	Kind  ScopeKind
	Stage string
	Table int
	Room  int // -1 for ScopeDan, whose store has no room
}

func (k Key) String() string {
	if k.Kind == ScopeDan {
		return fmt.Sprintf("%s %s (table %d)", k.Kind, k.Stage, k.Table)
	}
	return fmt.Sprintf("%s %s (table %d, room %d)", k.Kind, k.Stage, k.Table, k.Room)
}

func compareKeys(a, b Key) int {
	return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Stage, b.Stage),
		cmp.Compare(a.Table, b.Table), cmp.Compare(a.Room, b.Room))
}

// check reports why k names no store.
func (k Key) check() error {
	if _, _, _, ok := k.Kind.words(); !ok {
		return fmt.Errorf("%w: kind %d", ErrScope, uint8(k.Kind))
	}
	if !validStage(k.Stage) {
		return fmt.Errorf("%w: %s stage %q", ErrScope, k.Kind, k.Stage)
	}
	if !addr.ValidTable(k.Table) {
		return fmt.Errorf("%w: %s table %d (want 0..%d)", ErrScope, k.Kind, k.Table, addr.TableCount-1)
	}
	if k.Kind == ScopeDan {
		if k.Room != -1 {
			return fmt.Errorf("%w: DAN room %d (a dan store has no room: -1)", ErrScope, k.Room)
		}
	} else if !validRoom(k.Room) {
		return fmt.Errorf("%w: %s room %d (want 0..%d)", ErrScope, k.Kind, k.Room, addr.RoomCount-1)
	}
	return nil
}

// reaches reports whether a player at p gets k's relays and its content
// on entry: same stage and table, and for ZONE and ROOMEDGE the same room.
func (k Key) reaches(p Place) bool {
	if !p.inStage() || p.Stage != k.Stage || p.Table != k.Table {
		return false
	}
	return (k.Kind != ScopeZone && k.Kind != ScopeRoomEdge) || p.Room == k.Room
}

// keeps reports whether a player at p keeps store k alive: anyone in the
// table for DAN, else whoever k reaches.
func (k Key) keeps(p Place) bool {
	if k.Kind == ScopeDan {
		return p.inStage() && p.Table == k.Table
	}
	return k.reaches(p)
}

// Word is one word of a scoped store: element Word of the game array its
// kind names, and the bits set in it.
type Word struct {
	Word uint8
	Bits uint32
}

// Entry is one scoped store's content.
type Entry struct {
	Key   Key
	Words []Word // the non-zero words, ascending
}

var (
	// ErrScope is wrapped by Apply's error for a malformed key or word.
	ErrScope = errors.New("world: bad scoped store")
	// ErrNotPresent is wrapped by Apply's error when the origin's
	// presence is not in the scope it reports for.
	ErrNotPresent = errors.New("world: player is not in that scope")
	// ErrScopesFull is wrapped by Apply's error when a new DAN store
	// would make its table hold more than MaxDanStores.
	ErrScopesFull = errors.New("world: too many scoped stores")
)

// MaxDanStores is the most DAN stores one save table holds: about twice
// the stages the game has (the audio's stage list,
// JAIZelBasic::spot_dir_name, JAIZelScene.cpp:281-406, names 115), so
// only a peer naming stages that don't exist reaches it.
const MaxDanStores = 256

// Scopes holds the scoped stores and every player's place. The zero value
// is not usable; call NewScopes.
type Scopes struct {
	// Filter, if set, returns the bits of word w that store k may hold
	// or relay (SwitchFilter: the latching switches of the M1 table,
	// §6.2). Bits it leaves out are refused. Nil accepts every bit.
	Filter func(k Key, w int) uint32

	at      map[Player]Place
	entries map[Key]*[maxWords]uint32
	// The keys of entries by where they are: each stage's ZONE and KILL
	// stores, each table's DAN stores.
	byStage map[stageTable]map[Key]struct{}
	byTable [addr.TableCount]map[Key]struct{}
}

// stageTable is a stage in a save table.
type stageTable struct {
	stage string
	table int
}

// NewScopes returns empty stores with nobody present.
func NewScopes() *Scopes {
	return &Scopes{at: map[Player]Place{}, entries: map[Key]*[maxWords]uint32{}, byStage: map[stageTable]map[Key]struct{}{}}
}

// index returns the set k is kept in: its table's for DAN, its stage's
// otherwise. With add it makes the set if there is none.
func (s *Scopes) index(k Key, add bool) map[Key]struct{} {
	if k.Kind == ScopeDan {
		if s.byTable[k.Table] == nil && add {
			s.byTable[k.Table] = map[Key]struct{}{}
		}
		return s.byTable[k.Table]
	}
	g := stageTable{k.Stage, k.Table}
	if s.byStage[g] == nil && add {
		s.byStage[g] = map[Key]struct{}{}
	}
	return s.byStage[g]
}

func (s *Scopes) store(k Key, e *[maxWords]uint32) {
	s.entries[k] = e
	s.index(k, true)[k] = struct{}{}
}

func (s *Scopes) drop(k Key) {
	delete(s.entries, k)
	if ix := s.index(k, false); ix != nil {
		delete(ix, k)
		if len(ix) == 0 {
			if k.Kind == ScopeDan {
				s.byTable[k.Table] = nil
			} else {
				delete(s.byStage, stageTable{k.Stage, k.Table})
			}
		}
	}
}

// Moved is what a presence change did.
type Moved struct {
	// Enter is the stored content the player newly reaches, to send them:
	// the DAN store of a stage they entered, the ZONE store of a room
	// they entered, every room's KILL store of a stage they entered.
	Enter []Entry
	// Dropped is the stores nobody keeps alive any more.
	Dropped []Key
}

// SetPresence records that p is at place and returns what that changes.
func (s *Scopes) SetPresence(p Player, place Place) Moved {
	old, had := s.at[p]
	if !had {
		old = Nowhere
	}
	s.at[p] = place
	var m Moved
	if place.inStage() && (!old.inStage() || old.Stage != place.Stage || old.Table != place.Table) {
		dan := Key{Kind: ScopeDan, Stage: place.Stage, Table: place.Table, Room: -1}
		if e, ok := s.entry(dan); ok {
			m.Enter = append(m.Enter, e)
		}
		var kills []Key
		for k := range s.byStage[stageTable{place.Stage, place.Table}] {
			if k.Kind == ScopeKill {
				kills = append(kills, k)
			}
		}
		slices.SortFunc(kills, compareKeys)
		for _, k := range kills {
			e, _ := s.entry(k)
			m.Enter = append(m.Enter, e)
		}
	}
	if place.inRoom() && (!old.inRoom() || old.Stage != place.Stage || old.Table != place.Table || old.Room != place.Room) {
		zone := Key{Kind: ScopeZone, Stage: place.Stage, Table: place.Table, Room: place.Room}
		if e, ok := s.entry(zone); ok {
			m.Enter = append(m.Enter, e)
		}
	}
	m.Dropped = s.vacate(old, place)
	return m
}

// Leave forgets p (its connection ended) and returns the stores nobody
// keeps alive any more.
func (s *Scopes) Leave(p Player) []Key {
	old, had := s.at[p]
	delete(s.at, p)
	if !had {
		return nil
	}
	return s.vacate(old, Nowhere)
}

// PlaceOf returns where p is, and whether p is known.
func (s *Scopes) PlaceOf(p Player) (Place, bool) {
	at, ok := s.at[p]
	return at, ok
}

// Scoped is what one Apply did.
type Scoped struct {
	// Delta is the bits new to the store (for ROOMEDGE, every bit given),
	// to relay; empty if nothing changed.
	Delta []Word
	// To is the players to relay Delta to: every other player the store
	// reaches, ascending. Nil when Delta is empty.
	To []Player
	// Filtered is the bits Filter refused.
	Filtered []Word
}

// Apply ORs origin's words into store k (or, for ROOMEDGE, only relays
// them) and returns the effective delta and who gets it. Words may come in
// any order, with duplicates. It fails if k or a word is malformed
// (ErrScope), origin is not where k is (ErrNotPresent): only a player in
// a scope reports for it, or k would be a DAN store past its table's
// MaxDanStores (ErrScopesFull).
func (s *Scopes) Apply(origin Player, k Key, words []Word) (Scoped, error) {
	if err := k.check(); err != nil {
		return Scoped{}, err
	}
	first, last, width, _ := k.Kind.words()
	var in [maxWords]uint32
	for _, w := range words {
		if int(w.Word) < first || int(w.Word) > last {
			return Scoped{}, fmt.Errorf("%w: %s word %d (want %d..%d)", ErrScope, k.Kind, w.Word, first, last)
		}
		if w.Bits&^width != 0 {
			return Scoped{}, fmt.Errorf("%w: %s word %d bits 0x%08X exceed the word's 0x%X", ErrScope, k.Kind, w.Word, w.Bits, width)
		}
		in[w.Word] |= w.Bits
	}
	if at := s.at[origin]; !k.reaches(at) {
		return Scoped{}, fmt.Errorf("%w: %s reports for %s from %s", ErrNotPresent, playerName(origin), k, at)
	}

	var r Scoped
	if s.Filter != nil {
		for w := first; w <= last; w++ {
			if out := in[w] &^ s.Filter(k, w); out != 0 {
				r.Filtered = append(r.Filtered, Word{Word: uint8(w), Bits: out})
				in[w] &^= out
			}
		}
	}
	if k.Kind == ScopeRoomEdge {
		r.Delta = nonZero(&in)
	} else {
		e := s.entries[k]
		if e == nil {
			if in == ([maxWords]uint32{}) {
				return r, nil // nothing to keep
			}
			if n := len(s.byTable[k.Table]); k.Kind == ScopeDan && n >= MaxDanStores {
				return r, fmt.Errorf("%w: table %d holds %d DAN stores already, so %s is refused", ErrScopesFull, k.Table, n, k)
			}
			e = new([maxWords]uint32)
			s.store(k, e)
		}
		var gained [maxWords]uint32
		for w := first; w <= last; w++ {
			gained[w] = in[w] &^ e[w]
			e[w] |= in[w]
		}
		r.Delta = nonZero(&gained)
	}
	if len(r.Delta) > 0 {
		for p, at := range s.at {
			if p != origin && k.reaches(at) {
				r.To = append(r.To, p)
			}
		}
		slices.Sort(r.To)
	}
	return r, nil
}

// Get returns store k's non-zero words, or nil if it holds nothing.
func (s *Scopes) Get(k Key) []Word {
	e, _ := s.entry(k)
	return e.Words
}

// Keys lists the stores held, sorted by kind, stage, table and room.
func (s *Scopes) Keys() []Key {
	ks := make([]Key, 0, len(s.entries))
	for k := range s.entries {
		ks = append(ks, k)
	}
	slices.SortFunc(ks, compareKeys)
	return ks
}

func (s *Scopes) entry(k Key) (Entry, bool) {
	e := s.entries[k]
	if e == nil {
		return Entry{Key: k}, false
	}
	return Entry{Key: k, Words: nonZero(e)}, true
}

// vacate drops the stores a player moving from old to now no longer keeps
// alive and nobody else does, and returns their keys, sorted. Only the
// stores old kept can lose their last keeper: its table's DAN stores and
// its stage's ZONE and KILL stores.
func (s *Scopes) vacate(old, now Place) []Key {
	if !old.inStage() {
		return nil
	}
	var dropped []Key
	check := func(ix map[Key]struct{}) {
		for k := range ix {
			if k.keeps(now) || !k.keeps(old) {
				continue
			}
			alive := false
			for _, at := range s.at {
				if k.keeps(at) {
					alive = true
					break
				}
			}
			if !alive {
				dropped = append(dropped, k)
			}
		}
	}
	check(s.byTable[old.Table])
	check(s.byStage[stageTable{old.Stage, old.Table}])
	for _, k := range dropped {
		s.drop(k)
	}
	slices.SortFunc(dropped, compareKeys)
	return dropped
}

// SwitchFilter returns a Scopes.Filter that keeps the dan, zone and
// room-edge switches synced reports as synced, and every kill bit. synced
// gets the store's key and the switch number a bit is (0x80-0xEF): bit b
// of dan word w is 0x80 + 32w + b (dSv_danBit_c::onSwitch,
// d_save.cpp:1238-1241), bit b of zone word w is 0xC0 + 16w + b
// (dSv_zoneBit_c::onSwitch, d_save.cpp:1279-1282). With the M1 table that
// is switches.KeyFor(k.Table, k.Stage, k.Room, no) and Table.Synced.
func SwitchFilter(synced func(k Key, no int) bool) func(Key, int) uint32 {
	return func(k Key, w int) uint32 {
		var base, bits int
		switch k.Kind {
		case ScopeDan:
			base, bits = addr.SwitchDanFirst+32*w, 32
		case ScopeZone, ScopeRoomEdge:
			base, bits = addr.SwitchZoneFirst+16*w, 16
		default:
			return 0xFFFFFFFF
		}
		var keep uint32
		for b := 0; b < bits; b++ {
			if synced(k, base+b) {
				keep |= 1 << b
			}
		}
		return keep
	}
}

func nonZero(ws *[maxWords]uint32) []Word {
	var out []Word
	for i, b := range ws {
		if b != 0 {
			out = append(out, Word{Word: uint8(i), Bits: b})
		}
	}
	return out
}

func playerName(p Player) string { return fmt.Sprintf("co-op %d", p) }
