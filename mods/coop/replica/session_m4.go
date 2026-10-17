package replica

import (
	"fmt"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
)

// What a writing replica says of its session's classes: the armed line
// names what is written, by the features granted, and a welcome that
// grants fewer of M4's than the replica asked for says which are off and
// why. The derives the replica's own tables keep off (a table without a
// key table, one whose M1 invariant fails) the Deriver says on its first
// tick (derive.Plan.Notes).

// writtenClasses lists what a writing replica granted f writes into its
// game, for the armed line.
func writtenClasses(f hub.Feature) string {
	parts := []string{"the world's chests, pickups, rooms, dungeon items, ocean and charts"}
	if f&hub.FeatureSwitches != 0 {
		parts = append(parts, "its latching switches (at room changes)")
	}
	if f&hub.FeatureStory != 0 {
		parts = append(parts, "its story bits and event registers (at stage changes)")
	}
	if f&hub.FeatureInventory != 0 {
		parts = append(parts, "its items, songs and capacities (when idle)")
	}
	if d := m4Words(f & (hub.FeatureKeys | hub.FeatureLife | hub.FeatureMagic)); d != "" {
		parts = append(parts, "the "+d+" this game's own flags give")
	}
	return strings.Join(parts, "; ")
}

// m4Names name M4's classes, in the order the lines list them.
var m4Names = []struct {
	f    hub.Feature
	name string
}{
	{hub.FeatureStory, "story bits"},
	{hub.FeatureInventory, "items"},
	{hub.FeatureKeys, "small keys"},
	{hub.FeatureLife, "max hearts"},
	{hub.FeatureMagic, "max magic"},
}

// m4Words names f's M4 classes: "small keys and max hearts".
func m4Words(f hub.Feature) string {
	var s []string
	for _, n := range m4Names {
		if f&n.f != 0 {
			s = append(s, n.name)
		}
	}
	return andList(s)
}

func andList(s []string) string {
	if len(s) < 2 {
		return strings.Join(s, "")
	}
	return strings.Join(s[:len(s)-1], ", ") + " and " + s[len(s)-1]
}

// sayUngranted follows a welcome m (its features are r.features): when a
// writing replica asked for M4 classes the host does not grant, it says
// which, and why, once for each set. The TableFeatures read the M1 stage
// tables, so the hub withholds them when the two sides' differ or one has
// none, and says why in a NOTICE (hub.noteTables); the rest only the
// host turns off.
func (r *Replica) sayUngranted(m *wire.CoWelcome) {
	if r.writer.DryRun() || m.Status == wire.StatusRefused {
		return
	}
	missing := r.want() & hub.M4Features &^ r.features
	if missing == r.toldMissing {
		return
	}
	r.toldMissing = missing
	if missing == 0 {
		r.logf("co-op: the host now grants the %s", m4Words(r.want()&hub.M4Features))
		return
	}
	var why []string
	if t := missing & hub.TableFeatures; t != 0 {
		why = append(why, fmt.Sprintf("the %s need both sides' co-op stage tables to match (the host's notice says what is wrong)", m4Words(t)))
	}
	if o := missing &^ hub.TableFeatures; o != 0 {
		why = append(why, fmt.Sprintf("the host turned the %s off", m4Words(o)))
	}
	r.logf("co-op: the world's %s are not written to this game this session: %s", m4Words(missing), strings.Join(why, "; "))
}
