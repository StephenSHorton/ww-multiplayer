package enemies

import (
	"fmt"
	"slices"
	"strings"
)

// maxRefsPerLine bounds how many members of one duplicate the report
// lists.
const maxRefsPerLine = 8

// Report is a plain-text summary: the counts, the setID shares §7.2's
// go/no-go needs (overall, for the K1 classes, by class and by stage),
// every duplicate key, what the game ignores, and the class list.
func (t *Table) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Enemy table: %d stages, %d entries", t.Stages, t.Placed)
	if t.Unnamed > 0 {
		fmt.Fprintf(&b, " (%d not in l_objectName)", t.Unnamed)
	}
	fmt.Fprintf(&b, ", %d enemy entries of %d classes\n", len(t.Enemies), len(t.Summary.ByClass))
	byTag := make(map[string]int)
	var tags []string
	for _, e := range t.Enemies {
		if byTag[e.Tag] == 0 {
			tags = append(tags, e.Tag)
		}
		byTag[e.Tag]++
	}
	slices.Sort(tags)
	if len(tags) > 0 {
		b.WriteString("Enemy entries by chunk:")
		for i, tag := range tags {
			sep := ","
			if i == 0 {
				sep = ""
			}
			fmt.Fprintf(&b, "%s %s %d", sep, tag, byTag[tag])
		}
		b.WriteString("\n")
	}

	b.WriteString("\nsetID report (§7.2 keys a kill by stage, room and setID != 0xFFFF):\n")
	header := fmt.Sprintf("  %-22s %7s  %-15s %-15s %6s %6s %9s %6s\n",
		"", "entries", "setID set", "keyed", "stage", ">0x1FF", "unchecked", "shared")
	b.WriteString(header)
	shareLine(&b, "all enemies", t.Summary.All)
	shareLine(&b, "K1 classes, not BOSS", t.Summary.K1)
	b.WriteString("  (keyed: setID < 0x200 in a room's file; stage: in stage.dzs, ignored; >0x1FF: the game\n" +
		"  halts; unchecked: a 0x24-byte entry, created again however dead; shared: another enemy on a\n" +
		"  common layer has the key)\n")
	b.WriteString("\nBy class:\n")
	b.WriteString(header)
	for _, cs := range t.Summary.ByClass {
		c := classByName(cs.Class)
		shareLine(&b, fmt.Sprintf("%-7s %-5s %-7s", c.Name, c.Kind, c.Death), cs.Share)
	}
	b.WriteString("\nBy stage:\n")
	b.WriteString(header)
	for _, ss := range t.Summary.ByStage {
		shareLine(&b, ss.Stage, ss.Share)
	}

	layerOnly, shared, collide, withEnemy := 0, 0, 0, 0
	for _, d := range t.Duplicates {
		if d.LayerOnly {
			layerOnly++
		}
		if d.SharedKey {
			shared++
		}
		if d.Collides {
			collide++
		}
		if d.Enemies > 0 {
			withEnemy++
		}
	}
	fmt.Fprintf(&b, "\nDuplicate keys (stage, room, setID) over every created entry: %d\n", len(t.Duplicates))
	fmt.Fprintf(&b, "  %d with an enemy; %d where two enemies share a layer (a kill by the key reaches both);\n", withEnemy, shared)
	fmt.Fprintf(&b, "  %d that collide in one game (a recorded death keeps another entry from being created);\n", collide)
	fmt.Fprintf(&b, "  %d that differ only by layer (they never meet in one game, but two players' layers can differ)\n", layerOnly)
	for _, enemy := range []bool{true, false} {
		n := withEnemy
		if !enemy {
			n = len(t.Duplicates) - withEnemy
		}
		if n == 0 {
			continue
		}
		what := "with an enemy"
		if !enemy {
			what = "without one"
		}
		fmt.Fprintf(&b, "  %s:\n", what)
		for _, d := range t.Duplicates {
			if (d.Enemies > 0) == enemy {
				duplicateLine(&b, d)
			}
		}
	}

	all := t.Summary.All
	b.WriteString("\nsetIDs the game ignores or overrides:\n")
	fmt.Fprintf(&b, "  stage.dzs entries are created with room -1, which onActor and isActor ignore\n"+
		"    (d_save.cpp:1672-1674, :1686-1688); enemy entries with a setID there: %d\n", all.StageFile)
	fmt.Fprintf(&b, "  0x24-byte entries (TGSC, SCOB, DOOR, TGDR, SCOn) keep their setID but are never\n"+
		"    dead-checked (dStage_tgscInfoInit, d_stage.cpp:1712-1730); keyed enemy entries: %d\n", all.Unchecked)
	fmt.Fprintf(&b, "  setIDs 0x200-0xFFFE are past a zone's 512 bits: isActor's and onActor's asserts halt the\n"+
		"    game (d_save.cpp:1676, :1690-1692; JUTAssert.h:9); enemy entries: %d; dead-checked room\n"+
		"    entries of any kind, on which the game halts as the room loads: %d\n", all.OutOfRange, t.Halting)
	b.WriteString("  runtime spawns get 0xFFFF (fopAcM_CreateAppend, f_op_actor_mng.cpp:82), as do Gyorgs\n" +
		"    GyCtrl creates (d_a_gy_ctrl.cpp:345) and the parts bosses create\n")
	b.WriteString("  NPC event cuts find their actor by a nonzero setID (d_npc_event_cut.inc:353), so an NPC's\n" +
		"    setID needn't be a zone bit\n")
	b.WriteString("  classes whose death never records the bit (death none) or records it only on a Light\n" +
		"    Arrow or ice death (death ice: KS, BB) leave their setID unused; unknown: a NonMatching stub\n")

	b.WriteString("\nEnemy classes (profile group fopAc_ENEMY_e, plus Morth in fopAc_ENV_e):\n")
	placed := make(map[string]int)
	for _, cs := range t.Summary.ByClass {
		placed[cs.Class] = cs.Entries
	}
	for _, c := range Classes {
		ice := ""
		if c.Ice != 0 {
			ice = fmt.Sprintf("enemyice +0x%X", c.Ice)
		}
		home := ""
		if c.HomeMoves {
			home = "home moves"
		}
		fmt.Fprintf(&b, "  0x%03X %-7s %-28s %-5s %-5s death %-7s BOSS %-7s %-17s %-10s placed %d\n",
			uint16(c.Proc), c.Name, c.What, c.Group, c.Kind, c.Death, c.Boss, ice, home, placed[c.Name])
	}
	return b.String()
}

// duplicateLine writes one duplicate: its key, members and flags, then
// why it collides.
func duplicateLine(b *strings.Builder, d Duplicate) {
	var flags []string
	if d.SharedKey {
		flags = append(flags, "shared key")
	}
	if d.Collides {
		flags = append(flags, "collides")
	}
	if d.LayerOnly {
		flags = append(flags, "layer only")
	}
	refs := make([]string, 0, min(len(d.Entries), maxRefsPerLine))
	for i, r := range d.Entries {
		if i == maxRefsPerLine {
			refs = append(refs, fmt.Sprintf("+%d more", len(d.Entries)-i))
			break
		}
		refs = append(refs, r.String())
	}
	fmt.Fprintf(b, "    %s %s 0x%04X: %s", d.Stage, roomName(d.Room), d.SetID, strings.Join(refs, ", "))
	if len(flags) > 0 {
		fmt.Fprintf(b, " [%s]", strings.Join(flags, ", "))
	}
	b.WriteString("\n")
	if d.Why != "" {
		fmt.Fprintf(b, "      %s\n", d.Why)
	}
}

func shareLine(b *strings.Builder, label string, s Share) {
	fmt.Fprintf(b, "  %-22s %7d  %-15s %-15s %6d %6d %9d %6d\n",
		label, s.Entries, pct(s.WithSetID, s.Entries), pct(s.Keyed, s.Entries), s.StageFile, s.OutOfRange, s.Unchecked, s.Shared)
}

// pct is n with its share of d.
func pct(n, d int) string {
	if d == 0 {
		return fmt.Sprint(n)
	}
	return fmt.Sprintf("%d (%.1f%%)", n, 100*float64(n)/float64(d))
}

func classByName(name string) Class {
	for _, c := range Classes {
		if c.Name == name {
			return c
		}
	}
	return Class{Name: name}
}
