package switches

import (
	"fmt"
	"slices"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
)

// dungeons are the save tables the report lists switch by switch, with
// the names addr gives them.
var dungeons = []struct {
	table int
	name  string
}{
	{addr.TableFF, "Forsaken Fortress"},
	{addr.TableDRC, "Dragon Roost Cavern"},
	{addr.TableFW, "Forbidden Woods"},
	{addr.TableTotG, "Tower of the Gods"},
	{addr.TableET, "Earth Temple"},
	{addr.TableWT, "Wind Temple"},
	{addr.TableGT, "Ganon's Tower"},
}

// Report is a plain-text summary: counts by store and class, the
// push-block exclusion list, what couldn't be decoded or routed, and the
// switches of each dungeon table with their verdicts.
func (t *Table) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Switch table: %d stages, %d switch users", t.Stages, t.Users)
	if t.Unnamed > 0 {
		fmt.Fprintf(&b, " (%d entries not in l_objectName)", t.Unnamed)
	}
	b.WriteString("\n")

	type count struct{ all, synced, unwritten, gba int }
	stores := []Store{StoreMemory, StoreDan, StoreZone, StoreRoom}
	byStore := make(map[Store]*count)
	for _, st := range stores {
		byStore[st] = &count{}
	}
	classes := []Class{Latching, Unsafe, Event, Unknown, GBA}
	byClass := make(map[Class]int)
	var total count
	writers := 0
	for _, s := range t.Switches {
		c := byStore[s.Store]
		if c == nil {
			c = &count{}
			byStore[s.Store] = c
		}
		for _, cc := range []*count{c, &total} {
			cc.all++
			if s.Synced {
				cc.synced++
			}
			if s.Synced && s.GBA {
				cc.gba++
			}
			if len(s.Writers) == 0 {
				cc.unwritten++
			}
		}
		for _, w := range s.Writers {
			byClass[w.Class]++
			writers++
		}
	}
	fmt.Fprintf(&b, "Switches: %d, %d synced, %d with no known setter\n", total.all, total.synced, total.unwritten)
	for _, st := range stores {
		c := byStore[st]
		fmt.Fprintf(&b, "  %-7s %5d  synced %5d  no setter %5d\n", st, c.all, c.synced, c.unwritten)
	}
	fmt.Fprintf(&b, "Synced only while no GBA is linked (shared with the Tingle Tuner): %d (", total.gba)
	for i, st := range stores {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s %d", st, byStore[st].gba)
	}
	b.WriteString(")\n")
	fmt.Fprintf(&b, "Writers: %d", writers)
	for _, c := range classes {
		fmt.Fprintf(&b, ", %s %d", c, byClass[c])
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "Push blocks storing a path in switches (never synced): %d\n", len(t.PushBlocks))
	for _, p := range t.PushBlocks {
		fmt.Fprintf(&b, "  %v path %d: %s\n", p.Ref, p.Path, keyList(p.Switches))
	}

	fmt.Fprintf(&b, "Undecoded switch users: %d\n", len(t.Undecoded))
	byName := make(map[string]int)
	for _, u := range t.Undecoded {
		byName[u.Name]++
	}
	for _, n := range sortedNames(byName) {
		fmt.Fprintf(&b, "  %-8s %d\n", n, byName[n])
	}
	fmt.Fprintf(&b, "Switch uses the game can't route: %d\n", len(t.Invalid))
	for _, iv := range t.Invalid {
		fmt.Fprintf(&b, "  %v: %s\n", iv.Ref, iv.Why)
	}

	for _, d := range dungeons {
		var in []*Switch
		for i := range t.Switches {
			if s := &t.Switches[i]; s.Table == d.table {
				in = append(in, s)
			}
		}
		n := 0
		for _, s := range in {
			if s.Synced {
				n++
			}
		}
		fmt.Fprintf(&b, "\n%s (table %d): %d switches, %d synced (ok*: while no GBA is linked)\n", d.name, d.table, len(in), n)
		for _, s := range in {
			mark := "  "
			if s.Synced {
				mark = "ok"
			}
			if s.Synced && s.GBA {
				mark = "ok*"
			}
			fmt.Fprintf(&b, "  %s %v  %s\n", mark, s.Key, switchLine(s))
		}
	}
	return b.String()
}

// switchLine is a switch's writers in short for a synced switch, else
// why it isn't synced.
func switchLine(s *Switch) string {
	if !s.Synced {
		if len(s.Readers) > 0 && len(s.Writers) == 0 {
			return fmt.Sprintf("%s (read by %s)", s.Reason, readerNames(s.Readers))
		}
		return s.Reason
	}
	var names, gba []string
	for _, w := range s.Writers {
		if w.Class == GBA {
			gba = append(gba, w.Name)
		} else {
			names = append(names, w.Name)
		}
	}
	line := "set by " + strings.Join(compactStrings(names), ", ")
	switch {
	case len(gba) > 0:
		line += "; held while a GBA is linked (Tingle Tuner: " + strings.Join(compactStrings(gba), ", ") + ")"
	case s.GBA:
		line += "; held while a GBA is linked (an AND input is)"
	}
	return line
}

func readerNames(rs []Reader) string {
	names := make([]string, len(rs))
	for i, r := range rs {
		names[i] = r.Name
	}
	return strings.Join(compactStrings(names), ", ")
}

func keyList(ks []Key) string {
	s := make([]string, len(ks))
	for i, k := range ks {
		s[i] = k.String()
	}
	return strings.Join(s, ", ")
}

func sortedNames(m map[string]int) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// compactStrings sorts s and drops repeats.
func compactStrings(s []string) []string {
	slices.Sort(s)
	return slices.Compact(s)
}
