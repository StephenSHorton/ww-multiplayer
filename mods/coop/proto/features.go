package proto

import (
	"fmt"
	"strings"
)

// Feature is one bit of the u32 feature words of CO_HELLO (want) and
// CO_WELCOME (features), and of CTRL COOP_OFF's arg (§4.4). wire carries
// the words without giving them meaning; these bits are the hub's, as the
// host decides what a session runs (§11 item 28: a host-authoritative
// feature mask), and replicas read them from here.
//
// The low byte and the third are the session features: a replica asks
// for them in CO_HELLO's want, the host allows some (hub.Config.Features,
// hub.SetFeatures), and CO_WELCOME grants the intersection. The second byte
// is what the host tells a replica about binding. The top bits are hello
// flags: they ask for something in one CO_HELLO and are never granted
// back.
//
// Two session features carry image bytes of their own, which the hub
// neither takes from a player without them nor sends it (hub.gateOf):
// FeatureSwitches the memory switches, FeatureStory the story bits. The
// others either need no bytes beyond FeatureImage's or are what the
// replica does with the image (the live-switch timing, the inventory, the
// derived counts), and the hub only passes the host's choice on.
type Feature uint32

const (
	// FeatureImage is the shared image (§3 classes OR, OR_MASKED and
	// MAX): the replica's WORLD is merged and its GAINS proposed, and it
	// gets the GAINS relay.
	FeatureImage Feature = 1 << 0
	// FeaturePuzzle is the dan, zone and room-edge switches (SCOPED DAN,
	// ZONE, ROOMEDGE; §6.3, M5): the latching ones of the host's M1
	// verdict (hub.Config.SyncedScoped, ScopeFilter).
	FeaturePuzzle Feature = 1 << 1
	// FeatureCombat is the kill union (SCOPED KILL; §7.2, M6): the
	// keyed K1 deaths of the host's M1 verdict (hub.Config.SyncedKill,
	// Filter), written into the partner's zone and light-shrinking its
	// live enemy.
	FeatureCombat Feature = 1 << 2
	// FeatureLiveSwitches is --coop-live-switches (§6.3): the current
	// table's memory switches apply when IDLE, once the room hold has
	// passed, rather than at the next room change. The hub only passes
	// the host's choice on.
	FeatureLiveSwitches Feature = 1 << 3
	// FeatureSwitches is memory switch sync (§6.2-6.3, M3): the latching
	// memory switches 0x00-0x7F of the host's M1 switch table travel in
	// the image (SwitchPolicy). Without it the hub neither takes a
	// player's memory switch bytes nor sends it any, and its replica
	// writes none.
	FeatureSwitches Feature = 1 << 4
	// FeatureStory is story sync (§6.5, M4): the story bits of
	// class.SyncMask travel in the image (DefaultPolicy), every named bit
	// of mEvent 0x00-0x41 but the LocalOnly, game-cleared and refight
	// ones, and the replica writes the world's on its next stage
	// transition, the holds' aside, with the allowlisted event registers.
	// Without it the hub neither takes a player's story bit bytes nor
	// sends it any, as for FeatureSwitches; a bind is judged on them all
	// the same, as on the memory switches, since the bound file's later
	// sessions may have it. The registers have been image bytes since M2
	// (FeatureImage); FeatureStory is the replica writing them.
	FeatureStory Feature = 1 << 5
	// FeatureInventory is the inventory (§6.6, M4): the replica writes the
	// world's get-item and collect flags, songs, Triforce shards, pearls,
	// spoils and bait flags, the item slots and equipment it derives from
	// them, and the wallet, quiver and bomb bag capacities by MAX. Those
	// flags and capacities have been image bytes since M2 (FeatureImage),
	// so the hub only passes the host's choice on.
	FeatureInventory Feature = 1 << 6
	// FeatureKeys is derived small keys (§6.4, M4): the replica sets each
	// dungeon's key count to the key sources minus the key doors its own
	// game has open. Those come from the M1 key table (TableFeatures); the
	// count itself is DERIVED and never enters the world.
	FeatureKeys Feature = 1 << 7

	// FeatureLife is derived max life (§3 "Max life", M4): 12 quarter
	// hearts plus the pieces and four per container the replica's own game
	// has collected, raised through the HUD's pending counter, never
	// lowered. The heart sources come from the M1 heart table
	// (TableFeatures).
	FeatureLife Feature = 1 << 16
	// FeatureMagic is derived max magic (§3 "Max magic", M4): from the
	// Deku Leaf's get-item flag and event 0x3180, which need no table,
	// raised through the HUD's pending counter, never lowered.
	FeatureMagic Feature = 1 << 17

	// FeatureMerge says the host runs with --coop-merge (§5.3): a file
	// with progress the world lacks (divergent) or one far behind it may
	// bind, and its progress is merged in. Set in every welcome of such a
	// host, whatever the replica wanted.
	FeatureMerge Feature = 1 << 8
	// FeatureBind, in the CO_WELCOME answering a hello with AskBind,
	// says the bind is accepted: the replica may write the tag now
	// (bind.Bind) and then send a bound CO_HELLO.
	FeatureBind Feature = 1 << 9

	// AskUnsaved, a hello flag, says the loaded file has never been
	// saved and loaded back (mDateIPL is 0, §5.2 V5). With AskBind it asks
	// the hub to record the bind's nonce as a pending fresh bind before
	// the tag is written; on a bound hello it keeps a pending fresh bind
	// pending, and a bound hello without it clears the nonce's (the file
	// has now been loaded from the card).
	AskUnsaved Feature = 1 << 30
	// AskBind, a hello flag, asks to bind the loaded, unbound file to the
	// hub's world: CO_HELLO's worldID is the hub's world and its
	// bindNonce the nonce the replica will write (bind.PlanBind). The
	// replica sends its WORLD first, so the hub can check it is a subset
	// of the world (§5.3).
	AskBind Feature = 1 << 31
)

// M4Features are M4's session features (§9 M4): story bits, inventory and
// the derived keys, max life and max magic.
const M4Features = FeatureStory | FeatureInventory | FeatureKeys | FeatureLife | FeatureMagic

// SessionFeatures is every session feature: what a replica may want and a
// host may allow.
const SessionFeatures = FeatureImage | FeaturePuzzle | FeatureCombat | FeatureLiveSwitches | FeatureSwitches | M4Features

// DefaultFeatures is what a host allows unless told otherwise (a --coop
// session): everything but the live-switch opt-in.
const DefaultFeatures = FeatureImage | FeaturePuzzle | FeatureCombat | FeatureSwitches | M4Features

// TableFeatures are the session features that read the M1 stage tables
// (wwapi/tables): which switches latch (memory switches, the live
// opt-in, the dan and zone switches of FeaturePuzzle), which setIDs name
// which enemies (FeatureCombat), and which chests, pickups and doors are
// key sources and key doors (FeatureKeys) and heart sources
// (FeatureLife). A player gets them only when its tables' identity,
// CO_HELLO's stageTblHash, is the host's and not 0 (hub.Config.StageTblHash);
// otherwise the hub grants the rest and says why in a NOTICE. The image's
// other classes (chests, pickups, visited rooms, dungeon items, ocean,
// charts and the sea map, story bits, registers, inventory and
// capacities, §3) and max magic need no table.
const TableFeatures = FeatureSwitches | FeatureLiveSwitches | FeaturePuzzle | FeatureCombat | FeatureKeys | FeatureLife

var featureNames = []struct {
	f    Feature
	name string
}{
	{FeatureImage, "image"},
	{FeaturePuzzle, "puzzle"},
	{FeatureCombat, "combat"},
	{FeatureLiveSwitches, "live-switches"},
	{FeatureSwitches, "switches"},
	{FeatureStory, "story"},
	{FeatureInventory, "inventory"},
	{FeatureKeys, "keys"},
	{FeatureLife, "life"},
	{FeatureMagic, "magic"},
	{FeatureMerge, "merge"},
	{FeatureBind, "bind"},
	{AskUnsaved, "ask-unsaved"},
	{AskBind, "ask-bind"},
}

// String lists f's bits by name ("image|puzzle"), "none" for 0, and any
// unnamed bits in hex.
func (f Feature) String() string {
	if f == 0 {
		return "none"
	}
	var parts []string
	for _, n := range featureNames {
		if f&n.f != 0 {
			parts = append(parts, n.name)
			f &^= n.f
		}
	}
	if f != 0 {
		parts = append(parts, fmt.Sprintf("0x%X", uint32(f)))
	}
	return strings.Join(parts, "|")
}
