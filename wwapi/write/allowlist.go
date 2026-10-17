package write

import (
	"fmt"
	"slices"

	"github.com/StephenSHorton/ww-multiplayer/wwapi/mem"
)

// Span is a run of bytes in an Allowlist: from Start, the bits each byte
// may change (Mask) and the owner's rule for it (Rules), Name naming the
// span for logs.
type Span[R any] struct {
	Start uint32
	Mask  []byte
	Rules []R
	Name  string
}

// End is the first address past s.
func (s Span[R]) End() uint32 { return s.Start + uint32(len(s.Mask)) }

// Allowlist is a static set of the bytes a writer may write: spans in
// address order, none overlapping, each byte with the bits a write may
// change and the owner's rule R (co-op's is its byteRule: the rule kind,
// its Target and the paths that may write it). A byte with mask 0 is not
// in it. An Allowlist is built once with Add and then read; it is safe
// for concurrent reads.
type Allowlist[R any] struct {
	spans []Span[R]
}

// Add adds s. A span whose every mask byte is 0 adds nothing. Mask and
// Rules must be the same length, and s may not overlap a span already
// added.
func (l *Allowlist[R]) Add(s Span[R]) error {
	if len(s.Mask) != len(s.Rules) {
		return fmt.Errorf("write: span %s: %d mask bytes, %d rules", s.Name, len(s.Mask), len(s.Rules))
	}
	if !slices.ContainsFunc(s.Mask, func(b byte) bool { return b != 0 }) {
		return nil
	}
	if uint64(s.Start)+uint64(len(s.Mask)) > 1<<32 {
		return fmt.Errorf("write: span %s at 0x%08X: %d bytes run past the address space", s.Name, s.Start, len(s.Mask))
	}
	i, _ := slices.BinarySearchFunc(l.spans, s.Start, func(t Span[R], a uint32) int {
		switch {
		case t.Start < a:
			return -1
		case t.Start > a:
			return 1
		}
		return 0
	})
	if i > 0 && l.spans[i-1].End() > s.Start {
		p := l.spans[i-1]
		return fmt.Errorf("write: span %s at 0x%08X overlaps %s [0x%08X, 0x%08X)", s.Name, s.Start, p.Name, p.Start, p.End())
	}
	if i < len(l.spans) && s.End() > l.spans[i].Start {
		n := l.spans[i]
		return fmt.Errorf("write: span %s [0x%08X, 0x%08X) overlaps %s at 0x%08X", s.Name, s.Start, s.End(), n.Name, n.Start)
	}
	l.spans = slices.Insert(l.spans, i, s)
	return nil
}

// Spans returns the spans in address order. The caller must not change
// them.
func (l *Allowlist[R]) Spans() []Span[R] { return l.spans }

// Ranges returns the address set the allowlist spans, one Range per span.
func (l *Allowlist[R]) Ranges() []Range {
	rs := make([]Range, len(l.spans))
	for i, s := range l.spans {
		rs[i] = Range{Addr: s.Start, Len: uint32(len(s.Mask))}
	}
	return rs
}

// At returns the span name and the rule of the byte at a; ok is false
// when a is outside the allowlist (no span, or a mask of 0), and name and
// r are zero when no span holds a.
func (l *Allowlist[R]) At(a uint32) (name string, r R, ok bool) {
	i, found := slices.BinarySearchFunc(l.spans, a, func(s Span[R], a uint32) int {
		switch {
		case a < s.Start:
			return 1
		case a >= s.End():
			return -1
		}
		return 0
	})
	if !found {
		return "", r, false
	}
	s := l.spans[i]
	return s.Name, s.Rules[a-s.Start], s.Mask[a-s.Start] != 0
}

// Mask returns the bits a write may change in the byte at a, 0 if a is
// outside the allowlist.
func (l *Allowlist[R]) Mask(a uint32) byte {
	i, found := slices.BinarySearchFunc(l.spans, a, func(s Span[R], a uint32) int {
		switch {
		case a < s.Start:
			return 1
		case a >= s.End():
			return -1
		}
		return 0
	})
	if !found {
		return 0
	}
	return l.spans[i].Mask[a-l.spans[i].Start]
}

// Allows reports whether every byte of [a, a+n) is in the allowlist.
func (l *Allowlist[R]) Allows(a uint32, n int) bool {
	if mem.CheckRange(a, n) != nil {
		return false
	}
	for i := range n {
		if l.Mask(a+uint32(i)) == 0 {
			return false
		}
	}
	return true
}
