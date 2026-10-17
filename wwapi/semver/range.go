package semver

import (
	"fmt"
	"strings"
)

// Range is a parsed version range: a list of comparator sets joined by
// "||", where a version must satisfy every comparator of at least one set.
type Range struct {
	text string
	sets [][]comparator
}

type op uint8

const (
	opEQ op = iota
	opLT
	opLE
	opGT
	opGE
)

type comparator struct {
	op op
	v  Version
}

func (c comparator) match(v Version) bool {
	d := v.Compare(c.v)
	switch c.op {
	case opEQ:
		return d == 0
	case opLT:
		return d < 0
	case opLE:
		return d <= 0
	case opGT:
		return d > 0
	case opGE:
		return d >= 0
	}
	return false
}

// ParseRange parses a range in the npm syntax subset manifests use:
//
//	any version              *  x  X
//	exactly that version     1.2.3  =1.2.3
//	X-range                  1.2  1.2.x (>=1.2.0 <1.3.0)  1  1.x (>=1.0.0 <2.0.0)
//	caret                    ^1.2.3  ^0.1  ^0.0.3: changes that keep the
//	                         left-most non-zero part (^0.1 is >=0.1.0 <0.2.0)
//	tilde                    ~1.2.3  ~1.2: patch changes; ~1 is >=1.0.0 <2.0.0
//	comparisons              >=1.2  >1.2  <2  <=1.2; a partial version is filled
//	                         in the way npm does (>1.2 is >=1.3.0, <=1.2 is <1.3.0)
//	both A and B             A B (space-separated)
//	A or B                   A || B
//
// Hyphen ranges ("1.2 - 1.4") are not supported. As in npm, a
// prerelease version satisfies a set only when one of its comparators
// names a prerelease of the same MAJOR.MINOR.PATCH.
func ParseRange(s string) (Range, error) {
	if len(s) > MaxLen {
		return Range{}, fmt.Errorf("%w: %d bytes, the limit is %d", ErrRange, len(s), MaxLen)
	}
	if strings.TrimSpace(s) == "" {
		return Range{}, fmt.Errorf("%w: empty", ErrRange)
	}
	r := Range{text: s}
	for _, alt := range strings.Split(s, "||") {
		fields := strings.Fields(alt)
		if len(fields) == 0 {
			return Range{}, fmt.Errorf("%w: %q has an empty alternative", ErrRange, s)
		}
		var set []comparator
		for _, f := range fields {
			cs, err := parseComparator(f)
			if err != nil {
				return Range{}, fmt.Errorf("%w: %q: %v", ErrRange, s, err)
			}
			set = append(set, cs...)
		}
		r.sets = append(r.sets, set)
	}
	return r, nil
}

// MustParseRange is ParseRange that panics on an error.
func MustParseRange(s string) Range {
	r, err := ParseRange(s)
	if err != nil {
		panic(err)
	}
	return r
}

// String returns the range as it was written.
func (r Range) String() string { return r.text }

// IsZero reports whether r is the zero Range (never parsed). It matches
// nothing.
func (r Range) IsZero() bool { return r.sets == nil }

// Contains reports whether v satisfies r.
func (r Range) Contains(v Version) bool {
	for _, set := range r.sets {
		if setMatch(set, v) {
			return true
		}
	}
	return false
}

func setMatch(set []comparator, v Version) bool {
	for _, c := range set {
		if !c.match(v) {
			return false
		}
	}
	if len(v.Pre) == 0 {
		return true
	}
	for _, c := range set {
		if len(c.v.Pre) > 0 && c.v.sameTuple(v) {
			return true
		}
	}
	return false
}

// MarshalText makes a Range encode as its text in JSON.
func (r Range) MarshalText() ([]byte, error) { return []byte(r.text), nil }

// UnmarshalText parses a Range from JSON text.
func (r *Range) UnmarshalText(b []byte) error {
	p, err := ParseRange(string(b))
	if err != nil {
		return err
	}
	*r = p
	return nil
}

// partial is a possibly incomplete version: n numeric parts (0..3).
type partial struct {
	v Version
	n int
}

func isWild(p string) bool { return p == "x" || p == "X" || p == "*" }

func parsePartial(s string) (partial, error) {
	if s == "" {
		return partial{}, fmt.Errorf("missing version")
	}
	// Trailing wildcards shorten the version: 1.x and 1.x.x are 1.
	core, rest := s, ""
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		core, rest = s[:i], s[i:]
	}
	parts := strings.Split(core, ".")
	n := len(parts)
	for n > 0 && isWild(parts[n-1]) {
		n--
	}
	for _, p := range parts[:n] {
		if isWild(p) {
			return partial{}, fmt.Errorf("%q has a wildcard before a number", s)
		}
	}
	if n < len(parts) && rest != "" {
		return partial{}, fmt.Errorf("%q has a wildcard and a prerelease or build", s)
	}
	if n == 0 {
		if len(parts) > 3 {
			return partial{}, fmt.Errorf("%q has more than three parts", s)
		}
		return partial{}, nil
	}
	if len(parts) > 3 {
		return partial{}, fmt.Errorf("%q has more than three parts", s)
	}
	v, got, err := parseParts(strings.Join(parts[:n], ".") + rest)
	if err != nil {
		return partial{}, err
	}
	return partial{v: v, n: got}, nil
}

// bump returns the lowest version above every version that starts with
// p's first n parts (n is 1 or 2): 1.2 -> 1.3.0, 1 -> 2.0.0.
func bump(v Version, n int) Version {
	if n == 1 {
		return Version{Major: v.Major + 1}
	}
	return Version{Major: v.Major, Minor: v.Minor + 1}
}

func floor(v Version) Version {
	return Version{Major: v.Major, Minor: v.Minor, Patch: v.Patch, Pre: v.Pre}
}

func parseComparator(f string) ([]comparator, error) {
	var opText string
	for _, o := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
		if strings.HasPrefix(f, o) {
			opText = o
			break
		}
	}
	p, err := parsePartial(f[len(opText):])
	if err != nil {
		return nil, err
	}
	anyV := []comparator{{op: opGE, v: Version{}}}
	lo := floor(p.v)
	switch opText {
	case "", "=":
		switch p.n {
		case 0:
			return anyV, nil
		case 3:
			return []comparator{{op: opEQ, v: lo}}, nil
		}
		return []comparator{{op: opGE, v: lo}, {op: opLT, v: bump(p.v, p.n)}}, nil
	case "^":
		if p.n == 0 {
			return anyV, nil
		}
		var hi Version
		switch {
		case p.v.Major > 0 || p.n == 1:
			hi = bump(p.v, 1)
		case p.v.Minor > 0 || p.n == 2:
			hi = bump(p.v, 2)
		default: // ^0.0.z
			hi = Version{Major: 0, Minor: 0, Patch: p.v.Patch + 1}
		}
		return []comparator{{op: opGE, v: lo}, {op: opLT, v: hi}}, nil
	case "~":
		switch p.n {
		case 0:
			return anyV, nil
		case 1:
			return []comparator{{op: opGE, v: lo}, {op: opLT, v: bump(p.v, 1)}}, nil
		}
		return []comparator{{op: opGE, v: lo}, {op: opLT, v: bump(p.v, 2)}}, nil
	}
	if p.n == 0 {
		if opText == ">=" {
			return anyV, nil
		}
		return nil, fmt.Errorf("%q compares with a wildcard", f)
	}
	full := p.n == 3
	switch opText {
	case ">=":
		return []comparator{{op: opGE, v: lo}}, nil
	case "<":
		return []comparator{{op: opLT, v: lo}}, nil
	case ">":
		if full {
			return []comparator{{op: opGT, v: lo}}, nil
		}
		return []comparator{{op: opGE, v: bump(p.v, p.n)}}, nil
	default: // "<="
		if full {
			return []comparator{{op: opLE, v: lo}}, nil
		}
		return []comparator{{op: opLT, v: bump(p.v, p.n)}}, nil
	}
}
