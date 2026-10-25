// Package hearts builds the M1 heart table (docs/08-coop-design.md §3's
// max life row, §11 items 8 and 11): every heart piece and heart
// container the user's own stage files place (a pig's dig spots among
// them, through itemsrc), the hand-written Catalogue
// of the pieces NPCs, minigames, letters, a shop and the auction hand
// over, and the totals against the vanilla 44 pieces and 6 containers.
// Max life derives from them as 12 + pieces + 4 * containers quarter
// hearts.
//
// A piece is recorded by its placement's own save bit (itemsrc.Flag). A
// container is recorded by STAGE_LIFE: whatever places it,
// item_func_utuwa_heart sets that bit for the save table of the stage it
// is taken in, Forsaken Fortress's (2) when that is the sea
// (d_item.cpp:588-604), and nothing else marks it.
//
// Citations: file:line is the zeldaret/tww decomp at commit 4d78c994
// (include/ and src/; the file names are unique, so directories are left
// out), as in wwapi/addr.
//
// # v0 UNSTABLE
//
// This package is part of wwapi v0. Its API may change in any 0.x minor
// version.
package hearts

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/stage"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/keys/itemsrc"
)

// The vanilla totals, as data: the design's §11 item 11, which checks
// them against the HUD's cap, 12 + 44 + 4 * 6 = 0x50 quarter hearts
// (d_meter.cpp:1432). Build compares the user's stage files and the
// Catalogue with them.
const (
	ExpectedPieces     = 44
	ExpectedContainers = 6
)

// Kinds of container placements beyond itemsrc's.
const (
	// Boss is a boss whose defeat drops a container.
	Boss itemsrc.Kind = "boss"
	// BossItem is a Bitem, which makes the container again when the
	// stage's boss is dead and its STAGE_LIFE not yet set
	// (daBossItem_Create, d_a_boss_item.cpp:24-41).
	BossItem itemsrc.Kind = "boss_item"
)

// bosses are the boss classes that create a container
// (fopAcM_createItemForBoss, f_op_actor_mng.cpp:955-964, directly or
// through a disappear actor of type daDisItem_HEART_CONTAINER_e,
// d_a_disappear.cpp:27-28). Helmaroc King (stage.ProcBDK) sets
// STAGE_BOSS_ENEMY (d_a_bdk.cpp:3232, :4235) but creates no container:
// Forsaken Fortress's comes from a Bitem or a placed one.
//
// Gohma, Kalle Demos, Jalhalla and Molgera drop none in a stage whose
// name starts with 'X', Ganon's Tower's refights (Xboss0-3): there each
// tests dComIfGp_getStartStageName()[0] == 'X' (the current stage's name,
// d_com_inf_game.h:283) when beaten, sets its trials-clear event bit and
// warps out with dLib_setNextStageBySclsNum instead of reaching the death
// that makes the container (d_a_btd.cpp:906-911, d_a_bmd.cpp:520-525,
// d_a_bpw.cpp:2154-2158, d_a_bwd.cpp:1021-1026), so Build leaves the
// bosses of such a stage out (refight). Gohdan has no such test, but no X
// stage holds him, so he is left out there too on purpose.
var bosses = map[stage.Proc]string{
	stage.ProcBTD: "Gohma",       // d_a_btd.cpp:1082
	stage.ProcBMD: "Kalle Demos", // d_a_bmd.cpp:747
	stage.ProcBST: "Gohdan",      // d_a_bst.cpp:2504
	stage.ProcBPW: "Jalhalla",    // d_a_bpw.cpp:2856
	stage.ProcBWD: "Molgera",     // d_a_bwd.cpp:1704
}

const (
	tableSea = 0x0 // STAGE_SEA, d_save.h:889
	tableFF  = 0x2 // STAGE_FF, d_save.h:891
	tableMax = 0x10
)

// unreachable are the stages Build leaves out, by name, with why: each
// holds a heart chest no player can reach, which would otherwise stand in
// the count for a real reward. Hearts only: the other sections keep them,
// as their switches and enemies cost nothing (docs/08-coop-design.md
// Appendix C item 168).
//
// UNVERIFIED here: this tree reads no SCLS exits, so the reachability is
// datstarkey/ww-online's (WWOnline.Patcher/WorldData/HeartCatalog.cs:97,
// docs/hearts.md §3.4, MIT; facts, no code). It agrees with the Wind
// Waker Randomizer's logic/item_locations.txt, whose 12 heart chests name
// neither stage, and with the totals: without these two the owner's disc
// has those 12 chests, and the Catalogue's 16 rewards make 44. Their third
// stage, DmSpot0, holds no heart (item 137), so it is not listed.
var unreachable = map[string]string{
	"Cave06":  "a heart chest (save table 12, tbox 18) whose only entrance is in sea_E, the unused E3-demo sea (unverified)",
	"I_SubAN": "a heart chest (save table 12, tbox 7) in a developer sub-dungeon no location list has (unverified)",
}

// refight reports whether s is a stage where a boss warps out instead of
// dropping its container: its name starts with 'X', the bosses' own test
// (see bosses). Gohdan (d_a_bst.cpp) has no such test, and no X stage
// holds him. A Bitem or a placed container in such a stage still counts.
func refight(s *stage.Stage) bool { return strings.HasPrefix(s.Name, "X") }

// lifeTable is the save table whose STAGE_LIFE a container taken in a
// stage on save table t sets (d_item.cpp:595-602).
func lifeTable(t int) int {
	if t == tableSea {
		return tableFF
	}
	return t
}

// Totals counts the sources by kind. Pieces counts every piece source:
// StagePieces in stage files, RewardPieces in the Catalogue with the
// item seen, UnverifiedPieces in it without.
type Totals struct {
	Pieces             int            `json:"pieces"`
	StagePieces        int            `json:"stage_pieces"`
	RewardPieces       int            `json:"reward_pieces"`
	UnverifiedPieces   int            `json:"unverified_pieces"`
	PiecesByKind       map[string]int `json:"pieces_by_kind"`
	Containers         int            `json:"containers"`
	ContainersByKind   map[string]int `json:"containers_by_kind"`
	ExpectedPieces     int            `json:"expected_pieces"`
	ExpectedContainers int            `json:"expected_containers"`
}

// Result is the heart table.
type Result struct {
	Pieces     []itemsrc.Source `json:"pieces"`     // in stage files
	Containers []itemsrc.Source `json:"containers"` // one per STAGE_LIFE table
	Rewards    []Reward         `json:"rewards"`    // the Catalogue
	Totals     Totals           `json:"totals"`
	// Unreachable are the stages left out (unreachable), in order.
	Unreachable []stage.Skipped `json:"unreachable,omitempty"`
	Pass        bool            `json:"pass"`
	Problems    []string        `json:"problems,omitempty"`
}

// Build reads stages, as stage.Load returns them, with names, the
// user's l_objectName. It never fails: what it can't account for is a
// problem.
func Build(stages []*stage.Stage, names *stage.ObjectNames) *Result {
	r := &Result{Rewards: slices.Clone(Catalogue)}
	var pieces, containers []itemsrc.Placement
	isPiece := func(i uint8) bool { return i == itemsrc.ItemHeartPiece || i == itemsrc.ItemHeartPieceAlt }
	isContainer := func(i uint8) bool { return i == itemsrc.ItemHeartContainer }
	for _, s := range stages {
		if s == nil || names == nil {
			continue
		}
		if why, ok := unreachable[s.Name]; ok {
			r.Unreachable = append(r.Unreachable, stage.Skipped{Name: s.Name, Why: why})
			continue
		}
		one := []*stage.Stage{s}
		pieces = append(pieces, itemsrc.Find(one, names, isPiece)...)
		life := itemsrc.Flag{Kind: itemsrc.StageLifeFlag, Table: lifeTable(s.SaveTable)}
		for _, p := range itemsrc.Find(one, names, isContainer) {
			if p.Flag.Halts() {
				// STAGE_LIFE would record it, but the game halts on
				// the placement's own bit before it can be taken.
				r.Problems = append(r.Problems, fmt.Sprintf("%s: container placement: the game halts on its save bit, %s (a JUT_ASSERT)",
					p.Where, p.Flag))
			}
			p.Flag = life
			containers = append(containers, p)
		}
		for _, p := range s.All() {
			proc, ok := names.Proc(p.Name)
			if !ok {
				continue
			}
			kind := Boss
			if proc == stage.ProcBossItem {
				kind = BossItem
				// getStage, prm & 0xFF (d_a_boss_item.h:13), is the
				// table whose boss and STAGE_LIFE it checks.
				if t := int(p.Params & 0xFF); t >= tableMax {
					r.Problems = append(r.Problems, fmt.Sprintf("%s: Bitem checks save table %d, past the last (0x%X)",
						itemsrc.At(s, p), t, tableMax-1))
				} else if t != life.Table {
					r.Problems = append(r.Problems, fmt.Sprintf("%s: Bitem checks save table %d's STAGE_LIFE, but its container sets table %d's",
						itemsrc.At(s, p), t, life.Table))
				}
			} else if _, ok := bosses[proc]; !ok || refight(s) {
				continue
			}
			containers = append(containers, itemsrc.Placement{
				Where: itemsrc.At(s, p), Kind: kind, Proc: proc, Item: itemsrc.ItemHeartContainer, Flag: life,
			})
		}
	}
	r.Pieces = itemsrc.Group(pieces)
	r.Containers = itemsrc.Group(containers)
	r.Totals = totals(r)
	r.Problems = append(r.Problems, check(r, origins(containers))...)
	r.Pass = len(r.Problems) == 0
	return r
}

func totals(r *Result) Totals {
	t := Totals{
		PiecesByKind: make(map[string]int), ContainersByKind: make(map[string]int),
		ExpectedPieces: ExpectedPieces, ExpectedContainers: ExpectedContainers,
	}
	for _, s := range r.Pieces {
		t.StagePieces++
		t.PiecesByKind[string(s.Kind)]++
	}
	for _, rw := range r.Rewards {
		if rw.ItemSeen {
			t.RewardPieces++
		} else {
			t.UnverifiedPieces++
		}
		t.PiecesByKind[string(rw.Kind)]++
	}
	t.Pieces = t.StagePieces + t.RewardPieces + t.UnverifiedPieces
	for _, s := range r.Containers {
		t.Containers++
		t.ContainersByKind[string(s.Kind)]++
	}
	return t
}

// origins lists, for each STAGE_LIFE, the kinds of the placements that
// set it, leaving out Bitems. The placements of one container are one
// entry on several layers or in several stages, or a boss and the Bitem
// that makes its container again; two kinds are two containers that one
// bit records.
func origins(ps []itemsrc.Placement) map[itemsrc.Flag][]itemsrc.Kind {
	o := make(map[itemsrc.Flag][]itemsrc.Kind)
	for _, p := range ps {
		if p.Kind != BossItem && !slices.Contains(o[p.Flag], p.Kind) {
			o[p.Flag] = append(o[p.Flag], p.Kind)
		}
	}
	return o
}

// check lists what keeps max life from being derived from the sources.
func check(r *Result, origins map[itemsrc.Flag][]itemsrc.Kind) []string {
	var p []string
	t := r.Totals
	if t.Pieces != t.ExpectedPieces {
		p = append(p, fmt.Sprintf("%d heart pieces (%d in stage files, %d rewards, %d unverified rewards); vanilla has %d",
			t.Pieces, t.StagePieces, t.RewardPieces, t.UnverifiedPieces, t.ExpectedPieces))
	}
	if t.Containers != t.ExpectedContainers {
		p = append(p, fmt.Sprintf("%d heart containers; vanilla has %d", t.Containers, t.ExpectedContainers))
	}
	for i, s := range r.Pieces {
		switch {
		case s.Flag.Halts():
			p = append(p, fmt.Sprintf("piece %d: %s: the game halts on that save bit (a JUT_ASSERT), so the piece can't be taken", i+1, s.Flag))
		case !s.Flag.Saved():
			p = append(p, fmt.Sprintf("piece %d: %s: nothing saved records the pickup, so it can be taken again and max life can't count it", i+1, s.Flag))
		}
	}
	for i, s := range r.Containers {
		if k := origins[s.Flag]; len(k) > 1 {
			kinds := make([]string, len(k))
			for j := range k {
				kinds[j] = string(k[j])
			}
			p = append(p, fmt.Sprintf("container %d: %d placements of %d kinds (%s) all set %s, which counts once",
				i+1, len(s.At), len(k), strings.Join(kinds, ", "), s.Flag))
		}
	}
	return p
}

// Report is the table as text: the verdict and totals, then every source.
func (r *Result) Report() string {
	var b strings.Builder
	t := r.Totals
	fmt.Fprintf(&b, "hearts: %s\n", verdict(r.Pass))
	stage := make(map[string]int)
	for _, s := range r.Pieces {
		stage[string(s.Kind)]++
	}
	seen, unseen := make(map[string]int), make(map[string]int)
	for _, rw := range r.Rewards {
		if rw.ItemSeen {
			seen[string(rw.Kind)]++
		} else {
			unseen[string(rw.Kind)]++
		}
	}
	fmt.Fprintf(&b, "  heart pieces: %d, vanilla %d: %d in stage files%s, %d rewards%s, %d unverified%s\n",
		t.Pieces, t.ExpectedPieces, t.StagePieces, list(byKind(stage)), t.RewardPieces, list(byKind(seen)),
		t.UnverifiedPieces, list(byKind(unseen)))
	fmt.Fprintf(&b, "  heart containers: %d, vanilla %d%s\n", t.Containers, t.ExpectedContainers, list(byKind(t.ContainersByKind)))
	for _, u := range r.Unreachable {
		fmt.Fprintf(&b, "  left out %s: %s\n", u.Name, u.Why)
	}
	section := func(title string, srcs []itemsrc.Source) {
		fmt.Fprintf(&b, "\n%s:\n", title)
		if len(srcs) == 0 {
			b.WriteString("  (none)\n")
		}
		for i, s := range srcs {
			fmt.Fprintf(&b, "  %d: %s, item 0x%02X, %s\n", i+1, s.Kind, s.Item, s.Flag)
			for _, w := range s.At {
				fmt.Fprintf(&b, "    %s\n", w)
			}
		}
	}
	section("heart pieces in stage files", r.Pieces)
	section("heart containers", r.Containers)
	b.WriteString("\nheart piece rewards:\n")
	for _, rw := range r.Rewards {
		fmt.Fprintf(&b, "  %s: %s, %s, %s, item 0x%02X", rw.ID, rw.Giver, rw.Where, rw.Kind, rw.Item)
		if !rw.ItemSeen {
			b.WriteString(" (unverified)")
		}
		fmt.Fprintf(&b, "\n    site: %s\n", rw.Site)
		if rw.Record != "" {
			fmt.Fprintf(&b, "    record: %s\n", rw.Record)
		}
		if rw.Note != "" {
			fmt.Fprintf(&b, "    note: %s\n", rw.Note)
		}
	}
	if len(r.Problems) > 0 {
		b.WriteByte('\n')
	}
	for _, p := range r.Problems {
		fmt.Fprintf(&b, "problem: %s\n", p)
	}
	return b.String()
}

// byKind is "kind n" for each kind in m, in kind order.
func byKind(m map[string]int) []string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(m)) {
		out = append(out, fmt.Sprintf("%s %d", k, m[k]))
	}
	return out
}

func list(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return " (" + strings.Join(s, ", ") + ")"
}

func verdict(pass bool) string {
	if pass {
		return "PASS"
	}
	return "FAIL"
}
