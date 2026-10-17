package world

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// The scoped stores (§4.4 CoopHub, §6.3): puzzle and kill state that only
// matters while a player is in the stage or room it belongs to. Unlike the
// image it is never saved (the game itself forgets it, see below), so it
// is not in the world file; it lives while someone occupies its scope and
// is dropped when the last occupant leaves. Presence (PRESENCE, §4.4)
// drives it.
//
// Each kind follows the game's own lifetime for the state, and §6.3's
// rule that a partner's switches reach a player only while both are in
// the same scope. A store lives while either player is in its scope, so
// the receiver holds the rest of that rule: it writes a store only while
// the partner's PRESENCE is in its scope too, and keeps it no longer than
// it is there itself (replica's partnerIn and pruneScoped):
//
//	kind      stored per            relayed to, sent on entry    dropped when nobody is in
//	DAN       stage, table          the stage (and table)        the stage (and table)
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
// its stages, in each player's game. The store is keyed by stage and
// table all the same, and lives while someone is in that stage and table,
// because a partner's dan switches are applied only while both players
// are in the same stage name and table (§6.3, kept as apply policy by
// Appendix C item 25): a store kept for a stage nobody is in could only
// reach a player whose partner is elsewhere. What a player's game carries
// from one stage of a table to the next, its sender proposes again on
// entering it (replica.Differ.ShareScopes), so nothing is lost when the
// store goes.
//
// (M2 kept a DAN store while anyone was in its table, Appendix C item 55.
// That never held for a lone player anyway: between two stages Link is
// gone, the old play scene's delete clears mpPlayerPtr (dScnPly_Delete
// ends in dComIfGp_init, d_s_play.cpp:1136; d_com_inf_game.cpp:60-69),
// and the replica's PRESENCE says nowhere, which dropped the table's
// stores.)
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
// Bounds. A player reports only for a scope its presence is in (Apply),
// and a store lives only while a player is in its scope, so the stores are
// at most one DAN and 64 KILL stores per occupied stage and one ZONE store
// per occupied room: a peer naming stage after stage drops each one's
// stores as it leaves. A presence change looks only at the stores it can
// enter or leave (they are indexed by stage), never at all of them.
//
// The assert hazard (§3.1). The game's switch setters halt on a switch
// number of 0xF0-0xFE, a room outside 0..63 or a zone the room lacks
// (JUT_ASSERT, live in GZLE01, d_save.cpp:1541-1556). A key's room and
// table are checked (Key.check), a word's number range is its kind's
// (ScopeKind.words), and SwitchFilter keeps only bits whose switch number
// is in the store the kind names, so nothing past 0xEF is ever kept or
// relayed. The zone the receiver's own game has for the room is the
// receiver's to check, at every write.

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

// reaches reports whether a player at p is in k's scope: it gets k's
// relays and its content on entry, and keeps store k alive. That is the
// same stage and table, and for ZONE and ROOMEDGE the same room.
func (k Key) reaches(p Place) bool {
	if !p.inStage() || p.Stage != k.Stage || p.Table != k.Table {
		return false
	}
	return (k.Kind != ScopeZone && k.Kind != ScopeRoomEdge) || p.Room == k.Room
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
)

// Scopes holds the scoped stores and every player's place. The zero value
// is not usable; call NewScopes.
type Scopes struct {
	// Filter, if set, returns the bits of word w that store k may hold
	// or relay (SwitchFilter: the latching switches of the M1 table,
	// §6.2). Bits it leaves out are refused. Nil accepts every bit.
	Filter func(k Key, w int) uint32

	at      map[Player]Place
	entries map[Key]*[maxWords]uint32
	// The keys of entries by the stage and table they are in.
	byStage map[stageTable]map[Key]struct{}
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

func (s *Scopes) store(k Key, e *[maxWords]uint32) {
	s.entries[k] = e
	g := stageTable{k.Stage, k.Table}
	if s.byStage[g] == nil {
		s.byStage[g] = map[Key]struct{}{}
	}
	s.byStage[g][k] = struct{}{}
}

func (s *Scopes) drop(k Key) {
	delete(s.entries, k)
	g := stageTable{k.Stage, k.Table}
	if ix := s.byStage[g]; ix != nil {
		delete(ix, k)
		if len(ix) == 0 {
			delete(s.byStage, g)
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
// Enter is sent once per entry: a place that did not move reaches nothing
// new.
func (s *Scopes) SetPresence(p Player, place Place) Moved {
	old, had := s.at[p]
	if !had {
		old = Nowhere
	}
	s.at[p] = place
	stage := place.inStage() && (!old.inStage() || old.Stage != place.Stage || old.Table != place.Table)
	room := place.inRoom() && (!old.inRoom() || old.Stage != place.Stage || old.Table != place.Table || old.Room != place.Room)
	return Moved{Enter: s.reached(place, stage, room), Dropped: s.vacate(old, place)}
}

// Reached returns all the stored content p's place reaches now, as a
// presence change would send it on entering that stage and room: for a
// player that could not take it when it entered (the hub sends a store
// only to a player granted its kind's feature) and can now.
func (s *Scopes) Reached(p Player) []Entry {
	place, ok := s.at[p]
	if !ok {
		return nil
	}
	return s.reached(place, place.inStage(), place.inRoom())
}

// reached is the stored content a player at place gets: with stage, the
// DAN store and every room's KILL store of its stage; with room, the ZONE
// store of its room.
func (s *Scopes) reached(place Place, stage, room bool) []Entry {
	var out []Entry
	if stage {
		dan := Key{Kind: ScopeDan, Stage: place.Stage, Table: place.Table, Room: -1}
		if e, ok := s.entry(dan); ok {
			out = append(out, e)
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
			out = append(out, e)
		}
	}
	if room {
		zone := Key{Kind: ScopeZone, Stage: place.Stage, Table: place.Table, Room: place.Room}
		if e, ok := s.entry(zone); ok {
			out = append(out, e)
		}
	}
	return out
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
// (ErrScope), or origin is not where k is (ErrNotPresent): only a player
// in a scope reports for it.
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

// vacate drops the stores a player moving from old to now no longer
// reaches and nobody else does, and returns their keys, sorted. Only the
// stores old reached can lose their last occupant: those of its stage.
func (s *Scopes) vacate(old, now Place) []Key {
	if !old.inStage() {
		return nil
	}
	var dropped []Key
	for k := range s.byStage[stageTable{old.Stage, old.Table}] {
		if k.reaches(now) || !k.reaches(old) {
			continue
		}
		alive := false
		for _, at := range s.at {
			if k.reaches(at) {
				alive = true
				break
			}
		}
		if !alive {
			dropped = append(dropped, k)
		}
	}
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
// is synctables.SyncedScopedSwitch(t, k.Table, k.Stage, k.Room, no) over tables t.
//
// Whatever synced says, a bit is kept only if its switch number is one
// the game's setter routes to the kind's own store (addr.ClassifySwitch,
// the routing of dSv_info_c::onSwitch, d_save.cpp:1540-1560): dan words
// hold 0x80-0xBF, zone words 0xC0-0xDF and the room-edge word 0xE0-0xEF.
// So a word outside its kind (dan word 2 would be 0xC0-0xDF, zone word 2
// the room switches, room-edge word 3 the 0xF0-0xFF the setters assert
// on, d_save.cpp:1541-1542), a zone or room switch of a room outside
// 0..63 (d_save.cpp:1553) and a key of a save table outside 0..15 keep
// nothing, and synced is not asked about them.
func SwitchFilter(synced func(k Key, no int) bool) func(Key, int) uint32 {
	return func(k Key, w int) uint32 {
		var base, bits int
		var store addr.SwitchStore
		switch k.Kind {
		case ScopeDan:
			base, bits, store = addr.SwitchDanFirst+32*w, 32, addr.SwitchDan
		case ScopeZone:
			base, bits, store = addr.SwitchZoneFirst+16*w, 16, addr.SwitchZone
		case ScopeRoomEdge:
			base, bits, store = addr.SwitchZoneFirst+16*w, 16, addr.SwitchRoom
		default:
			return 0xFFFFFFFF
		}
		if w < 0 || !addr.ValidTable(k.Table) || k.Kind != ScopeDan && !validRoom(k.Room) {
			return 0
		}
		var keep uint32
		for b := 0; b < bits; b++ {
			if no := base + b; addr.ClassifySwitch(no) == store && synced(k, no) {
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
