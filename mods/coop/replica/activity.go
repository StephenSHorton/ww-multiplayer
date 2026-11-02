package replica

import (
	"fmt"
	"strings"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/roomitems"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/hearts"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/keys"
)

// The app's Activity tab (wind#57): who got what where, from the world's
// own changes. It is read-only: the entries come from what the replica
// already applies (a partner's GAINS s->c, diffed by roomitems and the
// memBit layout) and the partners' PRESENCE, never from a new message.

// ActivityKind is what an Activity entry says happened.
type ActivityKind string

const (
	ActItem    ActivityKind = "item"    // an item, upgrade, song, pearl, shard, chart, capacity or heart
	ActChest   ActivityKind = "chest"   // a chest opened with no named item in the same batch
	ActDungeon ActivityKind = "dungeon" // a dungeon map, compass or boss key
	ActBoss    ActivityKind = "boss"    // a dungeon's boss defeated
	ActPlace   ActivityKind = "place"   // a partner entered another place
)

// Activity is one entry: Who did Kind (Label) in Where at At. ID rises by
// one per entry this replica makes, so a reader can tell new ones.
type Activity struct {
	ID    uint64
	At    time.Time
	Who   string
	Kind  ActivityKind
	Label string // "the Hookshot", "a chest", "the Dungeon Map", ...
	Where string // "Dragon Roost Cavern", "" when unknown
}

// Text is the entry as a line: "Blake got the Hookshot in Dragon Roost
// Cavern".
func (a Activity) Text() string {
	in := ""
	if a.Where != "" {
		in = " in " + a.Where
	}
	switch a.Kind {
	case ActChest:
		return fmt.Sprintf("%s opened %s%s", a.Who, a.Label, in)
	case ActBoss:
		return fmt.Sprintf("%s defeated %s%s", a.Who, a.Label, in)
	case ActPlace:
		return fmt.Sprintf("%s entered %s", a.Who, a.Where)
	}
	return fmt.Sprintf("%s got %s%s", a.Who, a.Label, in)
}

// ActivityCap is how many entries the ring keeps.
const ActivityCap = 200

// activityRing is the bounded ring of entries, oldest first.
type activityRing struct {
	next    uint64
	entries []Activity
	// place is each partner's last place, by connection, for "entered".
	place map[uint8]string
	// self is this replica's own co-op connection once learned (0 until
	// then): the hub answers every GAINS c->s with the same delta as
	// GAINS s->c to its origin too (hub.relay toOrigin), so an origin
	// that is no partner and only brings bytes this replica proposed is
	// itself. sent is those bytes, by image offset (bounded).
	self uint8
	sent map[uint16]bool
}

// noteSent remembers the offsets of a GAINS c->s (under mu).
func (a *activityRing) noteSent(gs []world.Gain) {
	if a.self != 0 {
		return
	}
	if a.sent == nil || len(a.sent) > 4096 {
		a.sent = map[uint16]bool{}
	}
	for _, g := range gs {
		a.sent[g.Off] = true
	}
}

// isSelf says whether origin is this replica's own connection, learning
// it from a first echo (see self).
func (a *activityRing) isSelf(origin uint8, partner bool, gs []world.Gain) bool {
	if origin == 0 || partner {
		return false
	}
	if a.self != 0 {
		return origin == a.self
	}
	if len(gs) == 0 || a.sent == nil {
		return false
	}
	for _, g := range gs {
		if !a.sent[g.Off] {
			return false
		}
	}
	a.self, a.sent = origin, nil
	return true
}

func (a *activityRing) add(now time.Time, es ...Activity) {
	for _, e := range es {
		a.next++
		e.ID, e.At = a.next, now
		a.entries = append(a.entries, e)
	}
	if n := len(a.entries) - ActivityCap; n > 0 {
		a.entries = append([]Activity(nil), a.entries[n:]...)
	}
}

func (a *activityRing) snapshot() []Activity {
	return append([]Activity(nil), a.entries...)
}

// tablePlaces names the save tables a player can be said to be "in"
// (dSv_save_c::SaveStageTbl, d_save.h:888-904); "" for the tables that
// hold many places (misc, the caves, test), named by stage instead.
var tablePlaces = [addr.TableCount]string{
	addr.TableSea: "the Great Sea", addr.TableSea2: "the Great Sea", addr.TableFF: "the Forsaken Fortress",
	addr.TableGT: "Ganon's Tower", addr.TableHyrule: "Hyrule Castle", addr.TableShip: "the Ghost Ship",
}

// PlaceName names where a save table and stage are: the dungeon's name
// for a key dungeon (keys.Dungeons), the table's place for the sea,
// Forsaken Fortress, Ganon's Tower, Hyrule and the Ghost Ship, else the
// stage's name ("" for none).
func PlaceName(table int, stage string) string {
	for _, d := range keys.Dungeons {
		if d.Table == table {
			return d.Name
		}
	}
	if addr.ValidTable(table) && tablePlaces[table] != "" {
		return tablePlaces[table]
	}
	return stage
}

// GainWho names a GAINS s->c origin: the partner on that co-op
// connection by its last PRESENCE, "the host" for the hub itself (origin
// 0: a merge), else "a partner".
func GainWho(origin uint8, names map[uint8]string) string {
	if origin == 0 {
		return "the host"
	}
	if n := names[origin]; n != "" {
		return n
	}
	return "a partner"
}

// ClassifyGains turns one applied batch into Activity entries: what the
// world's items (roomitems.Of, before and after) gained, the chests and
// dungeon items its memBit bytes gained, by who. where is the origin's
// place (PlaceName) and table its save table (-1 unknown): the
// inventory is said to be got there, and a memBit byte names the place
// of its own table, or the origin's when the table is the origin's own
// many-place one (misc, a cave). Switches, story bits, event registers,
// visited rooms, pickups and sea grids are noise and make no entry. A
// chest makes its own entry only when the batch named no item: the item
// is what it held.
func ClassifyGains(before, after *save.Image, gs []world.Gain, who, where string, table int, h *hearts.Result) []Activity {
	var out []Activity
	got := func(label string) { out = append(out, Activity{Who: who, Kind: ActItem, Label: label, Where: where}) }
	b, a := roomitems.Of(before, h), roomitems.Of(after, h)
	slots := func(bs, as []roomitems.Item) {
		for i := range as {
			if i < len(bs) && as[i].Have && as[i].Name != bs[i].Name {
				got(the(as[i].Name))
			}
		}
	}
	slots(b.Items, a.Items)
	slots(b.Equip, a.Equip)
	named := func(bs, as []roomitems.Named) {
		for i := range as {
			if i < len(bs) && as[i].Have && !bs[i].Have {
				got(the(as[i].Name))
			}
		}
	}
	named(b.Songs, a.Songs)
	named(b.Pearls, a.Pearls)
	if a.Shards > b.Shards {
		got("a Triforce Shard")
	}
	if a.TriforceCharts > b.TriforceCharts {
		got("a Triforce Chart deciphered")
	}
	if a.Charts > b.Charts {
		got("a Treasure Chart")
	}
	if a.Wallet > b.Wallet {
		got(fmt.Sprintf("a bigger wallet (%d rupees)", a.Wallet))
	}
	if a.Quiver > b.Quiver && b.Quiver > 0 {
		got(fmt.Sprintf("a bigger quiver (%d arrows)", a.Quiver))
	}
	if a.BombBag > b.BombBag && b.BombBag > 0 {
		got(fmt.Sprintf("a bigger bomb bag (%d bombs)", a.BombBag))
	}
	if a.Magic > b.Magic {
		got("more magic")
	}
	if a.Hearts != nil && b.Hearts != nil {
		if a.Hearts.ContainersTaken > b.Hearts.ContainersTaken {
			got("a Heart Container")
		}
		if a.Hearts.PiecesTaken > b.Hearts.PiecesTaken {
			got("a Piece of Heart")
		}
	}
	items := len(out)
	placeOf := func(t int) string {
		if p := PlaceName(t, ""); p != "" {
			return p
		}
		if t == table {
			return where
		}
		return ""
	}
	chest, chestIn := false, ""
	for _, g := range gs {
		off := int(g.Off)
		t, rel, ok := memBitTable(off)
		if !ok {
			continue
		}
		gain := g.Val &^ before[off]
		if gain == 0 {
			continue
		}
		place := placeOf(t)
		switch {
		case rel < addr.MemBitOffSwitch:
			if !chest {
				chest, chestIn = true, place
			}
		case rel == addr.MemBitOffDungeonItem:
			for bit := range len(dungeonItems) {
				if gain&(1<<bit) == 0 {
					continue
				}
				switch bit {
				case 0:
					out = append(out, Activity{Who: who, Kind: ActDungeon, Label: "the Dungeon Map", Where: place})
				case 1:
					out = append(out, Activity{Who: who, Kind: ActDungeon, Label: "the Compass", Where: place})
				case 2:
					out = append(out, Activity{Who: who, Kind: ActDungeon, Label: "the Big Key", Where: place})
				case 3:
					out = append(out, Activity{Who: who, Kind: ActBoss, Label: "the boss", Where: place})
				case 4:
					if a.Hearts == nil { // with a heart table the Containers count said it
						out = append(out, Activity{Who: who, Kind: ActItem, Label: "the Heart Container", Where: place})
					}
				}
			}
		}
	}
	switch {
	case chest && items == 0:
		out = append(out, Activity{Who: who, Kind: ActChest, Label: "a chest", Where: chestIn})
	case chest:
		for i := range items {
			if out[i].Where == "" {
				out[i].Where = chestIn
			}
		}
	}
	return out
}

// the puts "the" before a name that has no article: "the Hookshot";
// "a Treasure Chart" and a possessive ("Din's Pearl", "Wind's
// Requiem") stay.
func the(name string) string {
	if strings.Contains(name, "'s ") {
		return name
	}
	for _, p := range []string{"the ", "The ", "a ", "an ", "A ", "An "} {
		if len(name) > len(p) && name[:len(p)] == p {
			return name
		}
	}
	return "the " + name
}

// noteGains records a partner's applied batch (under mu): before is the
// view as it was.
func (r *Replica) noteGains(m *wire.GainsDown, before *save.Image, gs []world.Gain) {
	names := map[uint8]string{}
	for o, v := range r.partners {
		names[o] = v.p.Name
	}
	who, where, table := GainWho(m.Origin, names), "", -1
	v := r.partners[m.Origin]
	if r.act.isSelf(m.Origin, v != nil, gs) {
		// This game's own gains, as the hub answered them.
		who, v = "You", nil
		if o := r.last; o != nil && o.s != nil && o.s.StageName != "" {
			table = o.stagTable
			where = PlaceName(table, o.s.StageName)
		}
	}
	if v != nil && !v.p.Stage.IsEmpty() {
		table = int(v.p.Table)
		where = PlaceName(table, v.p.Stage.String())
	}
	var h *hearts.Result
	if r.cfg.Tables != nil {
		h = r.cfg.Tables.Hearts
	}
	if es := ClassifyGains(before, &r.view, gs, who, where, table, h); len(es) > 0 {
		r.act.add(r.now(), es...)
	}
}

// notePlace records a partner's move to another place (under mu), from
// its PRESENCE: one entry per change of PlaceName, none for the first
// place a partner is seen in, out of game or at the credits.
func (r *Replica) notePlace(m *wire.PresenceDown) {
	if r.act.place == nil {
		r.act.place = map[uint8]string{}
	}
	if m.Flags&wire.PresenceInGame == 0 || m.Stage.IsEmpty() {
		return
	}
	p := PlaceName(int(m.Table), m.Stage.String())
	last, seen := r.act.place[m.Origin]
	r.act.place[m.Origin] = p
	if seen && p != last && p != "" {
		r.act.add(r.now(), Activity{Who: m.Name, Kind: ActPlace, Where: p})
	}
}
