// Package write is the guarded write engine: the generic half of co-op's
// replica (docs/08-coop-design.md §4.2, §6.7). It holds what every writer
// of game memory needs whatever it writes:
//
//   - Allowlist, the static set of bytes a writer may touch, each with
//     the bits it may change and a rule the owner defines, and Range, an
//     address set's span, with Disjoint, the overlap check that keeps two
//     writers' sets apart;
//   - Breaker, the off switch: it trips on a storm of verify failures or
//     refusals, on save-state loads, on a mismatch, or when the user turns
//     writes off, and once off every write stops;
//   - FrameWriter, the per-frame timing: it waits for the mod's heartbeat
//     (spawn_trigger) to move from the snapshot's, catching a save state
//     loaded since, reads again the stage state the snapshot's gates rest
//     on (ReadStage, Stage.Since, CheckIdle), and makes an all-or-nothing
//     batch of Fields through a Guard;
//   - Field, one write of bytes, and StageRequest, a stage change as
//     mNextStage holds it, with the Fields that ask the game for it.
//
// What to write, and each byte's rule beyond its mask (a grow-only flag, a
// better item variant), is the owner's: co-op's replica keeps its Target
// enum and byte rules and builds its WriteGuard and FrameWriter on this
// package.
//
// Error and log text still say "replica" and "co-op": they are what the
// co-op session has always printed, and moving the code changed none of
// it.
//
// # v0 UNSTABLE
//
// This package is part of wwapi v0. Its API may change in any 0.x minor
// version.
package write

import (
	"context"
	"fmt"
)

// Writer makes an all-or-nothing batch of fields after the next
// heartbeat, every byte through its owner's guard. co-op's
// replica.FrameWriter is one (replica.FrameWriter.ApplyFields).
//
// wwapi has no Writer of its own yet: this package's FrameWriter takes a
// Batch (FrameWriter.WriteFields) and does not satisfy Writer. A Writer a
// mod can get from wwapi comes in D-phase.
type Writer interface {
	ApplyFields(ctx context.Context, fs []Field) (Result, error)
}

// Guard judges the bytes of a write for a FrameWriter: an Allowlist plus
// the owner's gates and rules.
type Guard interface {
	// Mask returns the bits a write may change in the byte at a, 0 if a
	// is outside the allowlist.
	Mask(a uint32) byte
	// Ready reports whether a byte the allowlist holds may be written this
	// tick as far as the gates go: nil, or an error matching ErrClosed.
	Ready(a uint32) error
	// Check reports why writing val over old at a would be refused, nil
	// if it would not.
	Check(a uint32, old, val byte) error
}

// Range is a span of game memory: Len bytes from Addr.
type Range struct {
	Addr uint32
	Len  uint32
}

// End is the first address past r.
func (r Range) End() uint64 { return uint64(r.Addr) + uint64(r.Len) }

// Overlaps reports whether r and o share a byte.
func (r Range) Overlaps(o Range) bool {
	return r.Len > 0 && o.Len > 0 && uint64(r.Addr) < o.End() && uint64(o.Addr) < r.End()
}

func (r Range) String() string {
	return fmt.Sprintf("[0x%08X, 0x%08X)", r.Addr, r.End())
}

// Disjoint reports, as an error naming the first pair found, whether any
// two of the address sets share a byte. Ranges within one set may
// overlap; only different sets are compared.
func Disjoint(sets ...[]Range) error {
	for i := range sets {
		for j := i + 1; j < len(sets); j++ {
			for _, a := range sets[i] {
				for _, b := range sets[j] {
					if a.Overlaps(b) {
						return fmt.Errorf("write: address set %d's %v overlaps set %d's %v", i, a, j, b)
					}
				}
			}
		}
	}
	return nil
}
