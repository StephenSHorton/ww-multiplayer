package savemerge

import (
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/class"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/save"
)

// Change is one image byte that differs between two images.
type Change struct {
	Off  int
	A, B byte
	Rule class.Rule
}

// Diff returns every byte where a and b differ, in image order, with its
// class (class.Of).
func Diff(a, b *save.Image) []Change {
	var out []Change
	for off := range a {
		if a[off] != b[off] {
			out = append(out, Change{Off: off, A: a[off], B: b[off], Rule: class.Of(off)})
		}
	}
	return out
}

// Deriver recomputes the DERIVED bytes of img, the merged image, from its
// shared flags, in place: a is the file merged into and b the one merged
// from. It may change only DERIVED bytes (Merge refuses anything else) and
// returns notes for the user: what it derived, or why it kept a byte.
type Deriver func(img, a, b *save.Image) []string

// MergeOptions tune Merge.
type MergeOptions struct {
	// Policy is the bits of the OR, OR_MASKED and MAX bytes that merge,
	// as the hub and the replicas keep it (world.Policy): nil is
	// world.DefaultPolicy, which merges every OR and MAX byte by its
	// class mask and no OR_MASKED bit. The caller opens the memory
	// switches the M1 tables call latching (hub.SwitchPolicy) and the
	// story bits of the SyncMask (story.Policy).
	Policy *world.Policy
	// Derive recomputes the DERIVED bytes; nil keeps a's.
	Derive Deriver
}

// MergeResult is what Merge made.
type MergeResult struct {
	Image save.Image
	// Gains are the bytes the class merge raised: A is a's byte, B the
	// merged one.
	Gains []Change
	// Held is what b has that the receiver-side holds keep out of a for
	// now (§6.5), as a's replica would hold them.
	Held []replica.HeldBit
	// Derived are the DERIVED bytes Derive changed (A a's, B the
	// derived), and Notes what it said.
	Derived []Change
	Notes   []string
}

// mergeRounds bounds Merge's rounds. Each round but the last adds bits,
// and only a hold that a derived byte releases (0x2D10 waits for a Master
// Sword in mSelectEquip) makes a second one.
const mergeRounds = 4

// Merge returns a with b's shared progress in it, a's file as a's own
// replica would bring it up to a world holding b's file (§3, §4.2):
//
//   - OR and OR_MASKED bytes gain b's bits within the policy, and MAX
//     bytes take b's value where it is larger, by the class mask
//     (class.Of); nothing is ever cleared or lowered;
//   - story bits the holds name stay out while their condition is unmet
//     in a (replica.Holds): applying them would move a's resume stage,
//     Outset's or the Forsaken Fortress's layer, or lock its boat;
//   - DERIVED bytes are Derive's, or a's without one; max life and max
//     magic never drop below a's whatever Derive says;
//   - PER_PLAYER, NEVER and TAG bytes, and the bits of merging bytes
//     outside their class mask or the policy, are a's.
//
// It is the replica's own Planner and hold rules on the two images, so
// the offline merge and the live sync agree on what travels. A hold is
// judged on a with its derived bytes, as a's replica would judge it once
// its game had applied the merge, so Merge plans again until nothing
// changes.
func Merge(a, b *save.Image, opt MergeOptions) (MergeResult, error) {
	sharedB := world.Shared(b)
	planner := replica.NewPlanner(opt.Policy)
	img := *a
	var (
		held  []replica.HeldBit
		notes []string
	)
	for round := 0; ; round++ {
		local := world.Shared(&img)
		ws, h := planner.Plan(&sharedB, &img, &local, -1)
		next := replica.Planned(&img, ws)
		held, notes = h, nil
		if opt.Derive != nil {
			// Each round derives afresh from a's bytes, so the last
			// round's notes say everything the derive did.
			for off := range next {
				if class.Of(off).Class == class.Derived {
					next[off] = a[off]
				}
			}
			merged := next
			notes = opt.Derive(&next, a, b)
			for off := range next {
				if next[off] == merged[off] {
					continue
				}
				if rule := class.Of(off); rule.Class != class.Derived {
					return MergeResult{}, fmt.Errorf("savefile: the derive changed %s (image+0x%03X), a %s byte", rule.Field, off, rule.Class)
				}
			}
		}
		notes = append(notes, keepAddOnly(&next, a)...)
		if next == img || round == mergeRounds-1 {
			img = next
			break
		}
		img = next
	}

	r := MergeResult{Image: img, Held: held, Notes: notes}
	for off := range img {
		if img[off] == a[off] {
			continue
		}
		c := Change{Off: off, A: a[off], B: img[off], Rule: class.Of(off)}
		if c.Rule.Class == class.Derived {
			r.Derived = append(r.Derived, c)
		} else {
			r.Gains = append(r.Gains, c)
		}
	}
	return r, nil
}

// keepAddOnly puts back a's max life and max magic where img has less:
// both are add-only (§3 "Max life", "Max magic"), whatever a derive says.
func keepAddOnly(img, a *save.Image) []string {
	var notes []string
	if got, had := be16(img[addr.OffMaxLife:]), be16(a[addr.OffMaxLife:]); got < had {
		img[addr.OffMaxLife], img[addr.OffMaxLife+1] = a[addr.OffMaxLife], a[addr.OffMaxLife+1]
		notes = append(notes, fmt.Sprintf("max life: kept %d quarter hearts, not %d: it is never lowered", had, got))
	}
	if got, had := img[addr.OffMaxMagic], a[addr.OffMaxMagic]; got < had {
		img[addr.OffMaxMagic] = had
		notes = append(notes, fmt.Sprintf("max magic: kept %d, not %d: it is never lowered", had, got))
	}
	return notes
}
