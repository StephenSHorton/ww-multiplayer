package hub

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
// The low byte is the session features: a replica asks for them in
// CO_HELLO's want, the host allows some (Config.Features, SetFeatures),
// and CO_WELCOME grants the intersection. The next byte is what the host
// tells a replica about binding. The top bits are hello flags: they ask
// for something in one CO_HELLO and are never granted back.
type Feature uint32

const (
	// FeatureImage is the shared image (§3 classes OR, OR_MASKED and
	// MAX): the replica's WORLD is merged and its GAINS proposed, and it
	// gets the GAINS relay.
	FeatureImage Feature = 1 << 0
	// FeaturePuzzle is the dan, zone and room-edge switches (SCOPED DAN,
	// ZONE, ROOMEDGE; §6.3, M5).
	FeaturePuzzle Feature = 1 << 1
	// FeatureCombat is the kill union (SCOPED KILL; §7.2, M6).
	FeatureCombat Feature = 1 << 2
	// FeatureLiveSwitches is --coop-live-switches (§6.3): the current
	// table's memory switches apply when IDLE rather than at the next
	// room change. The hub only passes the host's choice on.
	FeatureLiveSwitches Feature = 1 << 3

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

// SessionFeatures is every session feature: what a replica may want and a
// host may allow.
const SessionFeatures = FeatureImage | FeaturePuzzle | FeatureCombat | FeatureLiveSwitches

// DefaultFeatures is what a host allows unless told otherwise: everything
// but the live-switch opt-in.
const DefaultFeatures = FeatureImage | FeaturePuzzle | FeatureCombat

var featureNames = []struct {
	f    Feature
	name string
}{
	{FeatureImage, "image"},
	{FeaturePuzzle, "puzzle"},
	{FeatureCombat, "combat"},
	{FeatureLiveSwitches, "live-switches"},
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
