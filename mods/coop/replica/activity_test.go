package replica

import (
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/wire"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
)

// gain sets bits val of image byte off on img (as world.Apply would) and
// returns the gain.
func gain(img *save.Image, off int, val byte) world.Gain {
	img[off] |= val
	return world.Gain{Off: uint16(off), Val: val}
}

func slotNamed(t *testing.T, name string) save.Slot {
	t.Helper()
	for _, s := range save.ItemSlots() {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no item slot %q", name)
	return save.Slot{}
}

func memBitOff(table, rel int) int { return addr.OffMemory + table*addr.MemBitSize + rel }

func texts(es []Activity) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Text()
	}
	return out
}

func TestGainWho(t *testing.T) {
	names := map[uint8]string{3: "Blake", 4: "Ana"}
	for _, c := range []struct {
		origin uint8
		want   string
	}{{3, "Blake"}, {4, "Ana"}, {0, "the host"}, {9, "a partner"}} {
		if got := GainWho(c.origin, names); got != c.want {
			t.Errorf("GainWho(%d) = %q, want %q", c.origin, got, c.want)
		}
	}
}

func TestPlaceName(t *testing.T) {
	for _, c := range []struct {
		table int
		stage string
		want  string
	}{
		{addr.TableDRC, "M_NewD2", "Dragon Roost Cavern"},
		{addr.TableWT, "kaze", "Wind Temple"},
		{addr.TableSea, "sea", "the Great Sea"},
		{addr.TableFF, "MajyuE", "the Forsaken Fortress"},
		{addr.TableMisc, "Pjavdou", "Pjavdou"}, // a many-place table: the stage
		{-1, "", ""},
	} {
		if got := PlaceName(c.table, c.stage); got != c.want {
			t.Errorf("PlaceName(%d, %q) = %q, want %q", c.table, c.stage, got, c.want)
		}
	}
}

// The card's example: a partner opens the Hookshot chest in DRC.
func TestClassifyChestItem(t *testing.T) {
	var before save.Image
	after := before
	hook := slotNamed(t, "hookshot")
	gs := []world.Gain{
		gain(&after, hook.FlagOff, 1),
		gain(&after, memBitOff(addr.TableDRC, addr.MemBitOffTbox+3), 0x04), // a chest bit
	}
	got := texts(ClassifyGains(&before, &after, gs, "Blake", "Dragon Roost Cavern", addr.TableDRC, nil))
	want := []string{"Blake got the hookshot in Dragon Roost Cavern"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestClassifyChestAlone(t *testing.T) {
	var before save.Image
	after := before
	gs := []world.Gain{gain(&after, memBitOff(addr.TableFW, addr.MemBitOffTbox), 0x80)}
	// The chest's own table names the place, wherever the origin stands.
	got := texts(ClassifyGains(&before, &after, gs, "Ana", "the Great Sea", addr.TableSea, nil))
	if len(got) != 1 || got[0] != "Ana opened a chest in Forbidden Woods" {
		t.Fatalf("got %q", got)
	}
}

func TestClassifyDungeonItems(t *testing.T) {
	var before save.Image
	after := before
	gs := []world.Gain{gain(&after, memBitOff(addr.TableET, addr.MemBitOffDungeonItem), 0x01|0x04|0x08)}
	got := strings.Join(texts(ClassifyGains(&before, &after, gs, "Blake", "Earth Temple", addr.TableET, nil)), "|")
	want := "Blake got the Dungeon Map in Earth Temple|Blake got the Big Key in Earth Temple|Blake defeated the boss in Earth Temple"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

// Switches, story bits, visited rooms and a bit the image already had are
// noise: no entry.
func TestClassifyNoise(t *testing.T) {
	var before save.Image
	before[memBitOff(addr.TableDRC, addr.MemBitOffTbox)] = 0x01
	after := before
	gs := []world.Gain{
		gain(&after, memBitOff(addr.TableDRC, addr.MemBitOffSwitch), 0x10),
		gain(&after, memBitOff(addr.TableDRC, addr.MemBitOffVisited), 0x02),
		gain(&after, addr.OffEvent+5, 0x20),
		{Off: uint16(memBitOff(addr.TableDRC, addr.MemBitOffTbox)), Val: 0x01}, // already set
	}
	if got := ClassifyGains(&before, &after, gs, "Blake", "Dragon Roost Cavern", addr.TableDRC, nil); len(got) != 0 {
		t.Fatalf("noise made entries: %q", texts(got))
	}
}

func TestClassifySongAndUpgrade(t *testing.T) {
	var before save.Image
	bow := slotNamed(t, "bow")
	before[bow.FlagOff] = 0x01 // has the bow
	after := before
	gs := []world.Gain{gain(&after, bow.FlagOff, 0x02), gain(&after, addr.OffTact, 0x03), gain(&after, addr.OffSymbol, 0x02)}
	got := strings.Join(texts(ClassifyGains(&before, &after, gs, "Ana", "", -1, nil)), "|")
	want := "Ana got the fire and ice arrows|Ana got Wind's Requiem|Ana got the Ballad of Gales|Ana got Din's Pearl"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestActivityRing(t *testing.T) {
	var a activityRing
	now := time.Unix(100, 0)
	for range ActivityCap + 5 {
		a.add(now, Activity{Who: "x", Kind: ActChest, Label: "a chest"})
	}
	s := a.snapshot()
	if len(s) != ActivityCap || s[0].ID != 6 || s[len(s)-1].ID != ActivityCap+5 {
		t.Fatalf("ring holds %d, ids %d..%d", len(s), s[0].ID, s[len(s)-1].ID)
	}
}

// notePlace: one entry per change of place, none for the first sighting
// or a player out of game.
func TestNotePlace(t *testing.T) {
	r := &Replica{now: func() time.Time { return time.Unix(1, 0) }}
	pres := func(stage string, table int8, inGame bool) *wire.PresenceDown {
		p := &wire.PresenceDown{Origin: 3, Name: "Blake"}
		copy(p.Stage[:], stage)
		p.Table = table
		if inGame {
			p.Flags = wire.PresenceInGame
		}
		return p
	}
	r.notePlace(pres("sea", addr.TableSea, true))
	r.notePlace(pres("sea", addr.TableSea, true))
	r.notePlace(pres("M_NewD2", addr.TableDRC, true))
	r.notePlace(pres("", -1, false))
	r.notePlace(pres("M_NewD2", addr.TableDRC, true))
	got := texts(r.act.snapshot())
	if len(got) != 1 || got[0] != "Blake entered Dragon Roost Cavern" {
		t.Fatalf("got %q", got)
	}
}

// TestStatusActivity: a partner's relayed gains land in Status.Activity
// named by its PRESENCE (the origin's connection), at its place; the
// replica's own world and a switch make none.
func TestStatusActivity(t *testing.T) {
	rr := armedRig(t, Config{})
	var img save.Image
	say(t, rr.r, worldOf(2, img))
	say(t, rr.r, down(t, 2, "Blake", "M_NewD2", addr.TableDRC, 3, 0, wire.PresenceInGame))
	say(t, rr.r, &wire.GainsDown{Seq: 3, Origin: 2, Gains: []wire.Gain{
		{Off: uint16(addr.OffGetItem + save.SlotHookshot), Val: 0x01},
		{Off: uint16(memBitOff(addr.TableDRC, addr.MemBitOffSwitch)), Val: 0x01},
	}})
	say(t, rr.r, &wire.GainsDown{Seq: 4, Origin: 7, Gains: []wire.Gain{
		{Off: uint16(memBitOff(addr.TableFW, addr.MemBitOffTbox)), Val: 0x01},
	}})
	got := texts(rr.r.Status().Activity)
	want := []string{"Blake got the hookshot in Dragon Roost Cavern", "a partner opened a chest in Forbidden Woods"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("activity %q, want %q", got, want)
	}
}
