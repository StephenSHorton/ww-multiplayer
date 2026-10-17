// Package class assigns every byte of the 0x778-byte dSv_save_c image
// exactly one co-op class (§3): how the byte merges between players, or
// that it never does. One table drives the sender's diff, the server's
// merge and validation, the receiver's WriteGuard and the offline save
// tools, so a byte left out or claimed twice is a bug; Validate (and the
// golden test that calls it) catches both.
//
// The image is the canonical one the wire uses: the saved dSv_save_c,
// with the current table's live memBit folded into its saved slot. Offsets
// are addr.Off* offsets.
package class

import (
	"errors"
	"fmt"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/addr"
)

// Class is how one byte of the image is shared.
type Class uint8

const (
	// Unassigned is the zero Class. Only a broken table produces it, and
	// every consumer treats it as never-synced.
	Unassigned Class = iota

	// OR merges the bits in Mask by grow-only OR: a bit either player ever
	// set stays set, and clears are never sent.
	OR

	// ORMasked is OR restricted to a policy subset of Mask that later
	// milestones supply: the latching-switch table from the user's ISO
	// for memory switches (M1, §6.2), the generated SyncMask for story
	// bits (M4, §6.5). Mask is the most that subset may ever be.
	ORMasked

	// Max merges the field (byte & Mask) by MAX, so it is never lowered.
	// Bits outside Mask stay local.
	Max

	// Derived bytes are computed by each receiver from shared flags (small
	// keys, max hearts and magic, inventory slots and equipment) rather
	// than copied.
	Derived

	// PerPlayer bytes belong to one player: life, magic, rupees, ammo,
	// bottles, bags, time of day, position, names and options.
	PerPlayer

	// Never bytes are padding or unnamed: never synced.
	Never

	// Tag is the co-op binding tag in dSv_reserve_c, owned by Go per save
	// file (§5.2).
	Tag
)

func (c Class) String() string {
	switch c {
	case Unassigned:
		return "UNASSIGNED"
	case OR:
		return "OR"
	case ORMasked:
		return "OR_MASKED"
	case Max:
		return "MAX"
	case Derived:
		return "DERIVED"
	case PerPlayer:
		return "PER_PLAYER"
	case Never:
		return "NEVER"
	case Tag:
		return "TAG"
	}
	return fmt.Sprintf("Class(%d)", uint8(c))
}

// Merges reports whether bytes of class c travel between players and are
// merged by the class rule. For those, Rule.Mask is non-zero.
func (c Class) Merges() bool { return c == OR || c == ORMasked || c == Max }

// Rule is the class of one image byte.
type Rule struct {
	Class Class
	// Mask is the bits the merge rule covers: non-zero exactly when
	// Class.Merges(). Bits outside it are local to each player.
	Mask byte
	// Field names the decomp member, for diagnostics.
	Field string
}

// Span is one save.Layout span (its Off, Len and Field) with the Rule
// keyed to it.
type Span struct {
	Off, Len int
	Rule
}

var (
	rules = buildRules()
	spans = buildSpans(rules)
	table = buildTable(spans)
)

// Of returns the rule for image offset off. A gap, an overlap or an offset
// outside the image all give the zero Rule (Unassigned).
func Of(off int) Rule {
	if off < 0 || off >= len(table) {
		return Rule{}
	}
	return table[off]
}

// Spans returns the table as written, in image order.
func Spans() []Span {
	return append([]Span(nil), spans...)
}

// Validate reports every gap, overlap and inconsistent mask in the table,
// and every rule not keyed to exactly one save.Layout span.
func Validate() error { return errors.Join(validate(spans), validateRules(rules)) }

func buildTable(spans []Span) [addr.SaveImageSize]Rule {
	var t [addr.SaveImageSize]Rule
	var n [addr.SaveImageSize]int
	for _, s := range spans {
		for i := s.Off; i < s.Off+s.Len; i++ {
			if i >= 0 && i < len(t) {
				n[i]++
				t[i] = s.Rule
			}
		}
	}
	for i := range t {
		if n[i] != 1 {
			t[i] = Rule{} // fail closed: a doubtful byte is never synced
		}
	}
	return t
}

func validate(spans []Span) error {
	var errs []error
	owner := make([]int, addr.SaveImageSize) // span index + 1 of the first claimant
	for si, s := range spans {
		if s.Len <= 0 || s.Off < 0 || s.Off+s.Len > addr.SaveImageSize {
			errs = append(errs, fmt.Errorf("%s: span 0x%03X+%d is outside the 0x%X-byte image",
				s.Field, s.Off, s.Len, addr.SaveImageSize))
			continue
		}
		if s.Class == Unassigned {
			errs = append(errs, fmt.Errorf("%s: span 0x%03X+%d has no class", s.Field, s.Off, s.Len))
		}
		if s.Class.Merges() != (s.Mask != 0) {
			errs = append(errs, fmt.Errorf("%s: %s span has mask 0x%02X", s.Field, s.Class, s.Mask))
		}
		for i := s.Off; i < s.Off+s.Len; i++ {
			if owner[i] != 0 {
				errs = append(errs, fmt.Errorf("overlap at 0x%03X: %s and %s",
					i, spans[owner[i]-1].Field, s.Field))
				continue
			}
			owner[i] = si + 1
		}
	}
	for i := 0; i < len(owner); i++ {
		if owner[i] != 0 {
			continue
		}
		j := i
		for j+1 < len(owner) && owner[j+1] == 0 {
			j++
		}
		errs = append(errs, fmt.Errorf("gap at 0x%03X-0x%03X: no class", i, j))
		i = j
	}
	return errors.Join(errs...)
}
