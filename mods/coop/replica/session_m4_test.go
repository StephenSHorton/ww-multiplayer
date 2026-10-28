package replica

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/proto"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
)

// TestWrittenClasses: the armed line names what the granted features
// write.
func TestWrittenClasses(t *testing.T) {
	for _, c := range []struct {
		f    proto.Feature
		want string
	}{
		{proto.FeatureImage, "the world's chests, pickups, rooms, dungeon items, ocean and charts"},
		{proto.FeatureImage | proto.FeatureSwitches | proto.FeatureStory, "the world's chests, pickups, rooms, dungeon items, ocean and charts; its latching switches (at room changes); its story bits and event registers (at stage changes)"},
		{proto.FeatureImage | proto.FeatureInventory | proto.FeatureMagic, "the world's chests, pickups, rooms, dungeon items, ocean and charts; its items, songs and capacities (when idle); the max magic this game's own flags give"},
		{proto.FeatureImage | proto.FeatureSwitches | proto.FeatureLiveSwitches, "the world's chests, pickups, rooms, dungeon items, ocean and charts; its latching switches (live: in the room you stand in, once idle)"},
		{proto.FeatureImage | proto.FeatureLiveSwitches, "the world's chests, pickups, rooms, dungeon items, ocean and charts"}, // no switches granted: none written
		{proto.DefaultFeatures, "the world's chests, pickups, rooms, dungeon items, ocean and charts; its latching switches (at room changes); its story bits and event registers (at stage changes); its items, songs and capacities (when idle); the small keys, max hearts and max magic this game's own flags give"},
	} {
		if got := writtenClasses(c.f); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.f, got, c.want)
		}
	}
}

// TestSayUngranted: a writing replica says which M4 classes a welcome
// does not grant and why, once per set; a dry run, which asks for none,
// says nothing.
func TestSayUngranted(t *testing.T) {
	all := proto.DefaultFeatures
	noTables := all &^ proto.TableFeatures
	rr := newRecRig(t, Config{Apply: true, Policy: proto.SessionPolicy(nil)}, noTables)
	const tablesWhy = "the small keys and max hearts need both sides' co-op stage tables to match (the host's notice says what is wrong)"
	if !rr.log.has("the world's small keys and max hearts are not written to this game this session: " + tablesWhy) {
		t.Fatalf("log:\n%s", rr.log)
	}
	if !rr.log.has("written to this game: the world's chests, pickups, rooms, dungeon items, ocean and charts; its story bits and event registers (at stage changes); its items, songs and capacities (when idle); the max magic this game's own flags give") {
		t.Fatalf("armed line:\n%s", rr.log)
	}
	say(t, rr.r, welcome(wire.StatusOK, uint32(noTables), false))
	if n := rr.log.count("not written to this game this session"); n != 1 {
		t.Fatalf("said %d times", n)
	}
	say(t, rr.r, welcome(wire.StatusOK, uint32(noTables&^proto.FeatureStory), false))
	if !rr.log.has("the world's story bits, small keys and max hearts are not written to this game this session: " + tablesWhy + "; the host turned the story bits off") {
		t.Fatalf("log:\n%s", rr.log)
	}
	say(t, rr.r, welcome(wire.StatusOK, uint32(all), false))
	if !rr.log.has("the host now grants the story bits, items, small keys, max hearts and max magic") {
		t.Fatalf("log:\n%s", rr.log)
	}
	if st := rr.r.Status(); st.Derive.NoKeyTable != true || st.Derive.Life {
		t.Fatalf("a replica without tables: %+v", st.Derive)
	}

	dry := newRecRigNoWelcome(t, Config{})
	say(t, dry.r, welcome(wire.StatusOK, uint32(proto.FeatureImage), false))
	if dry.log.has("not written to this game this session") {
		t.Fatalf("a dry run said:\n%s", dry.log)
	}
}
