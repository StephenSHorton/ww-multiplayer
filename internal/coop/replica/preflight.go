package replica

import (
	"fmt"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/addr"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
)

// The join preflight (§5.3): before a file is bound to a world, what each
// side would gain from the other, which of the world's bits the receiver
// would hold back, and whether binding is a plain prompt or needs the
// host's --coop-merge.

// PreflightVerdict is how a file stands against a world.
type PreflightVerdict uint8

const (
	// PreflightSame: neither side has a shared bit the other lacks.
	PreflightSame PreflightVerdict = iota + 1
	// PreflightSubset: the file's shared classes are a subset of the
	// world (a fresh file, or an older copy of this world's file): bind
	// with a prompt.
	PreflightSubset
	// PreflightAhead: the file has progress the world lacks and the
	// world nothing the file lacks. Binding adds the file's progress to
	// every bound file, so it needs --coop-merge (a host founding a world
	// from its file is ahead of the empty world, and passes it).
	PreflightAhead
	// PreflightDivergent: both sides have unique progress: binding needs
	// --coop-merge.
	PreflightDivergent
	// PreflightFarBehind: the world has resume-stage milestones the file
	// lacks. Copying the host's file over (coop-world export/import) is
	// recommended over a union, which needs --coop-merge, and the holds
	// apply.
	PreflightFarBehind
	// PreflightRefused: the file can't join co-op at all (a second
	// quest, §2).
	PreflightRefused
)

func (v PreflightVerdict) String() string {
	switch v {
	case PreflightSame:
		return "same"
	case PreflightSubset:
		return "subset"
	case PreflightAhead:
		return "ahead"
	case PreflightDivergent:
		return "divergent"
	case PreflightFarBehind:
		return "far behind"
	case PreflightRefused:
		return "refused"
	}
	return fmt.Sprintf("PreflightVerdict(%d)", uint8(v))
}

// Milestones are the resume-stage story bits (§5.3 "far behind"): which
// of them a file has picks where it starts when loaded (l_checkData in
// dComIfGs_setGameStartStage, d_com_inf_game.cpp:1304-1311), so a union
// that adds one moves the file's start.
var Milestones = []uint16{
	0x2A08,    // RODE_KORL, d_save_event_flag.inc:322
	evMetKorl, // MET_KORL
	ev0801,    // UNK_0801
	ev0808,    // UNK_0808
	ev2401,    // UNK_2401
}

// Bit is one shared bit (or, for a MAX byte, value) of a preflight list.
type Bit struct {
	Off      uint16
	Val      byte // the bit's mask, or a MAX byte's value
	Max      bool
	Category Category
	Label    string
}

// PreflightReport is Preflight's result.
type PreflightReport struct {
	Verdict PreflightVerdict
	// NeedsMerge: binding needs the host's --coop-merge.
	NeedsMerge bool
	// Why is one line for the prompt.
	Why string

	// FileGains is what binding would add to the world from the file,
	// WorldGains what it would add to the file from the world: one entry
	// per OR bit or MAX byte, in image order, within the policy.
	FileGains, WorldGains []Bit
	// FileByCategory and WorldByCategory count them per Category.
	FileByCategory, WorldByCategory [numCategories]int
	// Held is the world gains the holds keep out of the file for now
	// (§6.5); they are also in WorldGains.
	Held []Bit
	// MissingMilestones are the Milestones the world has and the file
	// lacks.
	MissingMilestones []uint16
}

// PreflightOptions tune Preflight. The zero value uses
// world.DefaultPolicy and DefaultLabel.
type PreflightOptions struct {
	// Policy limits both sides to the bits that sync: an OR_MASKED bit
	// outside it is neither side's gain.
	Policy *world.Policy
	Label  func(off, bit int) string
}

// Preflight compares a file with a world: file is the file's canonical
// image (local bytes included: its clear count and equipment matter),
// worldImg the world's. It is a pure function of the two images.
func Preflight(file, worldImg *world.Image, opt PreflightOptions) PreflightReport {
	pol, label := opt.Policy, opt.Label
	if pol == nil {
		pol = world.DefaultPolicy()
	}
	if label == nil {
		label = DefaultLabel
	}
	var r PreflightReport
	if cc := file[addr.OffClearCount]; cc != 0 {
		r.Verdict = PreflightRefused
		r.Why = fmt.Sprintf("the file is a second quest (clear count %d), which co-op doesn't take", cc)
		return r
	}

	local := world.Shared(file)
	wimg := world.Shared(worldImg)
	within := func(gs []world.Gain) []world.Gain { return filterPolicy(gs, pol) }
	r.FileGains = bitList(within(world.Diff(&wimg, &local)), label, r.FileByCategory[:])
	r.WorldGains = bitList(within(world.Diff(&local, &wimg)), label, r.WorldByCategory[:])

	// Holds, judged as the receiver's planner would with no ever-had
	// history: the file plus every unheld world gain.
	p := NewPlanner(pol)
	_, held := p.Plan(&wimg, file, &local, -1)
	for _, h := range held {
		for b := range 8 {
			if m := byte(1) << b; h.Mask&m != 0 {
				r.Held = append(r.Held, Bit{Off: h.Off, Val: m, Category: CatStory, Label: label(int(h.Off), b) + " (held: " + h.Hold.Why + ")"})
			}
		}
	}

	for _, id := range Milestones {
		if eventSet(&wimg, id) && !eventSet(&local, id) {
			r.MissingMilestones = append(r.MissingMilestones, id)
		}
	}

	switch {
	case len(r.MissingMilestones) > 0:
		r.Verdict, r.NeedsMerge = PreflightFarBehind, true
		r.Why = fmt.Sprintf("the world is past %s, which this file is not: copying the host's file is better than a union (which needs --coop-merge)",
			milestoneList(r.MissingMilestones))
	case len(r.FileGains) == 0 && len(r.WorldGains) == 0:
		r.Verdict = PreflightSame
		r.Why = "the file and the world have the same shared progress"
	case len(r.FileGains) == 0:
		r.Verdict = PreflightSubset
		r.Why = fmt.Sprintf("the world adds %d to this file; the file adds nothing to the world", len(r.WorldGains))
	case len(r.WorldGains) == 0:
		r.Verdict, r.NeedsMerge = PreflightAhead, true
		r.Why = fmt.Sprintf("the file adds %d the world lacks: binding it needs --coop-merge on the host", len(r.FileGains))
	default:
		r.Verdict, r.NeedsMerge = PreflightDivergent, true
		r.Why = fmt.Sprintf("both sides have unique progress (the file adds %d, the world %d): binding needs --coop-merge on the host",
			len(r.FileGains), len(r.WorldGains))
	}
	return r
}

// bitList expands gains into one Bit per OR bit or MAX byte, counting them
// per category into count.
func bitList(gs []world.Gain, label func(off, bit int) string, count []int) []Bit {
	var out []Bit
	for _, g := range gs {
		off := int(g.Off)
		cat := CategoryOf(off)
		if class.Of(off).Class == class.Max {
			out = append(out, Bit{Off: g.Off, Val: g.Val, Max: true, Category: cat, Label: fmt.Sprintf("%s = %d", label(off, -1), g.Val)})
			count[cat]++
			continue
		}
		for b := range 8 {
			if m := byte(1) << b; g.Val&m != 0 {
				out = append(out, Bit{Off: g.Off, Val: m, Category: cat, Label: label(off, b)})
				count[cat]++
			}
		}
	}
	return out
}

func milestoneList(ids []uint16) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprintf("0x%04X", id)
	}
	return strings.Join(parts, ", ")
}

// Summary is the report in a few lines for a prompt or the TUI: the
// verdict, the per-category counts both ways and the holds.
func (r PreflightReport) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "preflight: %s: %s\n", r.Verdict, r.Why)
	if r.Verdict == PreflightRefused {
		return b.String()
	}
	counts := func(name string, c [numCategories]int) {
		var parts []string
		for _, cat := range Categories() {
			if c[cat] > 0 {
				parts = append(parts, fmt.Sprintf("%s %d", cat, c[cat]))
			}
		}
		if len(parts) == 0 {
			parts = []string{"nothing"}
		}
		fmt.Fprintf(&b, "  %s: %s\n", name, strings.Join(parts, ", "))
	}
	counts("the file adds", r.FileByCategory)
	counts("the world adds", r.WorldByCategory)
	if len(r.Held) > 0 {
		fmt.Fprintf(&b, "  held until this file catches up: %d\n", len(r.Held))
		for _, h := range r.Held {
			fmt.Fprintf(&b, "    %s\n", h.Label)
		}
	}
	return b.String()
}
