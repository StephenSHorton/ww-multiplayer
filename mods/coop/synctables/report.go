package synctables

import (
	"fmt"
	"slices"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/enemies"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/hearts"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/keys"
)

// Status is an acceptance check's verdict.
type Status string

const (
	Pass Status = "PASS"
	Fail Status = "FAIL"
	// Info is a number the owner reads rather than one with a target:
	// the setID share and the duplicate keys that M6's go/no-go weighs.
	Info Status = "INFO"
)

// Check is one line of M1's owner acceptance (docs/08-coop-design.md §9
// M1, §11 item 8), as the tables measure it.
type Check struct {
	Section tables.Section
	Name    string
	Status  Status
	Detail  string
}

// Acceptance measures t against M1's owner acceptance: small keys and key
// doors DRC 4/4, FW 1/1, TotG 2/2, ET 3/3, WT 2/2 and none outside tables
// 3-7; 44 heart pieces and 6 containers; a non-empty push-block exclusion
// list; and, to read, the enemy setID share and duplicate (stage, room,
// setID) keys. The checks are on the counts the owner compares; each
// section's own verdict, which also weighs what its entries record, is
// in its report. A missing section has no checks.
func Acceptance(t *tables.Tables) []Check {
	var out []Check
	if r := t.Keys; r != nil {
		for _, d := range keys.Dungeons {
			c := Check{Section: tables.SectionKeys, Name: "keys " + d.Short, Status: Fail}
			tb, ok := r.Table(d.Table)
			if !ok {
				c.Detail = fmt.Sprintf("no table %d; vanilla %d/%d", d.Table, d.Keys, d.Keys)
				out = append(out, c)
				continue
			}
			if len(tb.Sources) == d.Keys && len(tb.Doors) == d.Keys {
				c.Status = Pass
			}
			c.Detail = fmt.Sprintf("%d/%d (sources/doors), vanilla %d/%d", len(tb.Sources), len(tb.Doors), d.Keys, d.Keys)
			if n := len(tb.Problems); n > 0 {
				c.Detail += fmt.Sprintf("; %d problem(s) in the keys section", n)
			}
			out = append(out, c)
		}
		var outside []string
		n := 0
		for _, tb := range r.Tables {
			if tb.Dungeon == "" && len(tb.Doors) > 0 {
				n += len(tb.Doors)
				outside = append(outside, fmt.Sprintf("table %d: %d", tb.SaveTable, len(tb.Doors)))
			}
		}
		c := Check{Section: tables.SectionKeys, Name: "key doors outside tables 3-7", Status: Pass, Detail: "none"}
		if n > 0 {
			c.Status, c.Detail = Fail, fmt.Sprintf("%d (%s)", n, strings.Join(outside, ", "))
		}
		out = append(out, c)
	}
	if r := t.Hearts; r != nil {
		tt := r.Totals
		c := Check{Section: tables.SectionHearts, Name: "heart pieces", Status: verdict(tt.Pieces == hearts.ExpectedPieces),
			Detail: fmt.Sprintf("%d, vanilla %d: %d in stage files, %d rewards, %d unverified rewards",
				tt.Pieces, hearts.ExpectedPieces, tt.StagePieces, tt.RewardPieces, tt.UnverifiedPieces)}
		out = append(out, c)
		c = Check{Section: tables.SectionHearts, Name: "heart containers", Status: verdict(tt.Containers == hearts.ExpectedContainers),
			Detail: fmt.Sprintf("%d, vanilla %d", tt.Containers, hearts.ExpectedContainers)}
		if n := len(r.Problems); n > 0 {
			c.Detail += fmt.Sprintf("; %d problem(s) in the hearts section", n)
		}
		out = append(out, c)
	}
	if r := t.Switches; r != nil {
		n := 0
		for _, p := range r.PushBlocks {
			n += len(p.Switches)
		}
		out = append(out, Check{Section: tables.SectionSwitches, Name: "push-block exclusion list", Status: verdict(len(r.PushBlocks) > 0),
			Detail: fmt.Sprintf("%d push block(s) storing a path in %d switch use(s), never synced", len(r.PushBlocks), n)})
	}
	if r := t.Enemies; r != nil {
		all, k1 := r.Summary.All, r.Summary.K1
		out = append(out, Check{Section: tables.SectionEnemies, Name: "enemy setID share", Status: Info,
			Detail: fmt.Sprintf("%d enemy entries: %s with a setID, %s keyed (%d of them never dead-checked); K1 classes not BOSS: %s keyed of %d",
				all.Entries, share(all.WithSetID, all.Entries), share(all.Keyed, all.Entries), all.Unchecked,
				share(k1.Keyed, k1.Entries), k1.Entries)})
		out = append(out, Check{Section: tables.SectionEnemies, Name: "duplicate (stage, room, setID) keys", Status: Info,
			Detail: duplicates(r)})
		out = append(out, Check{Section: tables.SectionEnemies, Name: "M6 kill coverage", Status: Info, Detail: r.Coverage().Line()})
	}
	return out
}

// duplicates sums up the enemy table's duplicate keys.
func duplicates(r *enemies.Table) string {
	withEnemy, shared, collide, layerOnly := 0, 0, 0, 0
	for _, d := range r.Duplicates {
		if d.Enemies > 0 {
			withEnemy++
		}
		if d.SharedKey {
			shared++
		}
		if d.Collides {
			collide++
		}
		if d.LayerOnly {
			layerOnly++
		}
	}
	return fmt.Sprintf("%d: %d with an enemy, %d with two enemies on a common layer, %d colliding in one game, %d on different layers only",
		len(r.Duplicates), withEnemy, shared, collide, layerOnly)
}

// share is "n (p%)" of d.
func share(n, d int) string {
	if d == 0 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%d (%.1f%%)", n, 100*float64(n)/float64(d))
}

func verdict(ok bool) Status {
	if ok {
		return Pass
	}
	return Fail
}

// Report is the tables as text: a header naming the disc and the binary,
// the acceptance checks of the sections asked for, then each of those
// sections' own report. With no sections, every one is reported, in
// tables.Sections order; a section t lacks is left out.
func Report(t *tables.Tables, sections ...tables.Section) string {
	if len(sections) == 0 {
		sections = tables.Sections
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Co-op tables (docs/08-coop-design.md §9 M1), schema %d, built by %s\n", t.Schema, t.Builder)
	fmt.Fprintf(&b, "  main.dol SHA-256 %s\n", t.DOL)
	fmt.Fprintf(&b, "  stage files SHA-256 %s: %d stages, %d rooms\n", t.StageFiles, t.Stages, t.Rooms)
	if len(t.Skipped) > 0 {
		skipped := make([]string, len(t.Skipped))
		for i, s := range t.Skipped {
			skipped[i] = fmt.Sprintf("%s (%s)", s.Name, s.Why)
		}
		noun := "stages"
		if len(t.Skipped) == 1 {
			noun = "stage"
		}
		fmt.Fprintf(&b, "  left out %d %s the play scene can't load: %s\n", len(t.Skipped), noun, strings.Join(skipped, ", "))
	}
	if len(t.Excluded) > 0 {
		excluded := make([]string, len(t.Excluded))
		for i, s := range t.Excluded {
			excluded[i] = s.Name
		}
		noun := "stages"
		if len(t.Excluded) == 1 {
			noun = "stage"
		}
		fmt.Fprintf(&b, "  left out %d %s on save table 15 (STAGE_TEST, developer test stages): %s\n", len(t.Excluded), noun, strings.Join(excluded, ", "))
	}

	var checks []Check
	for _, c := range Acceptance(t) {
		if slices.Contains(sections, c.Section) {
			checks = append(checks, c)
		}
	}
	if len(checks) > 0 {
		b.WriteString("\nAcceptance (owner run, docs/08-coop-design.md §9 M1):\n")
		width := 0
		for _, c := range checks {
			width = max(width, len(c.Name))
		}
		for _, c := range checks {
			fmt.Fprintf(&b, "  %s  %-*s  %s\n", c.Status, width, c.Name, c.Detail)
		}
	}

	for _, s := range tables.Sections {
		if !slices.Contains(sections, s) {
			continue
		}
		var text string
		switch s {
		case tables.SectionKeys:
			if t.Keys != nil {
				text = t.Keys.Report()
			}
		case tables.SectionHearts:
			if t.Hearts != nil {
				text = t.Hearts.Report()
			}
		case tables.SectionSwitches:
			if t.Switches != nil {
				text = t.Switches.Report()
			}
		case tables.SectionEnemies:
			if t.Enemies != nil {
				text = t.Enemies.Report()
			}
		}
		if text == "" {
			continue
		}
		fmt.Fprintf(&b, "\n== %s ==\n\n%s", s, text)
		if !strings.HasSuffix(text, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
