package main

import (
	"testing"

	"github.com/StephenSHorton/ww-multiplayer/internal/tui"
)

// TestDedupePlayersByName_CollapsesSameName verifies the ~2x connection fan-out
// (each human = broadcast-pose + puppet-sync under one name) collapses to a
// single entry per person, and that the positioned entry is preferred so PR-C's
// minimap keeps real coords.
func TestDedupePlayersByName_CollapsesSameName(t *testing.T) {
	// Zelda appears twice: the puppet connection (no position) first, then the
	// broadcast connection (real coords). Dedup must keep exactly one Zelda,
	// carrying the non-zero position.
	in := []tui.PlayerView{
		{ID: 4, Name: "Zelda"},                      // puppet twin, unpositioned
		{ID: 3, Name: "Zelda", X: 10, Y: 20, Z: 30}, // broadcast twin, positioned
		{ID: 5, Name: "Ganon", X: 1, Y: 2, Z: 3},    // distinct human
	}
	out := dedupePlayersByName(in)

	if len(out) != 2 {
		t.Fatalf("dedupePlayersByName: got %d entries, want 2: %+v", len(out), out)
	}

	byName := map[string]tui.PlayerView{}
	for _, p := range out {
		if _, dup := byName[p.Name]; dup {
			t.Errorf("dedupePlayersByName left a duplicate for %q", p.Name)
		}
		byName[p.Name] = p
	}

	z, ok := byName["Zelda"]
	if !ok {
		t.Fatalf("dedupePlayersByName dropped Zelda entirely: %+v", out)
	}
	if z.X != 10 || z.Y != 20 || z.Z != 30 {
		t.Errorf("dedupePlayersByName kept the unpositioned Zelda; want coords (10,20,30), got (%v,%v,%v)", z.X, z.Y, z.Z)
	}

	if _, ok := byName["Ganon"]; !ok {
		t.Errorf("dedupePlayersByName dropped the distinct human Ganon: %+v", out)
	}
}

// TestDedupePlayersByName_PreferredWhenBroadcastFirst verifies the position
// preference is order-independent (positioned entry seen first is retained even
// if an unpositioned same-name entry follows).
func TestDedupePlayersByName_PreferredWhenBroadcastFirst(t *testing.T) {
	in := []tui.PlayerView{
		{ID: 3, Name: "Zelda", X: 10, Y: 20, Z: 30}, // positioned first
		{ID: 4, Name: "Zelda"},                      // unpositioned twin
	}
	out := dedupePlayersByName(in)
	if len(out) != 1 {
		t.Fatalf("got %d entries, want 1: %+v", len(out), out)
	}
	if out[0].X != 10 || out[0].Y != 20 || out[0].Z != 30 {
		t.Errorf("position preference not order-independent: got (%v,%v,%v)", out[0].X, out[0].Y, out[0].Z)
	}
}

// TestDedupePlayersByName_DistinctNamesPreserved verifies distinct humans are
// all kept and input order is preserved.
func TestDedupePlayersByName_DistinctNamesPreserved(t *testing.T) {
	in := []tui.PlayerView{
		{ID: 1, Name: "A"},
		{ID: 2, Name: "B"},
		{ID: 3, Name: "C"},
	}
	out := dedupePlayersByName(in)
	if len(out) != 3 {
		t.Fatalf("got %d entries, want 3: %+v", len(out), out)
	}
	for i, want := range []string{"A", "B", "C"} {
		if out[i].Name != want {
			t.Errorf("out[%d].Name = %q, want %q (order not preserved)", i, out[i].Name, want)
		}
	}
}

// TestDedupePlayersByName_EmptyNameKeyedByID verifies nameless entries (created
// before the server player-list arrives) fall back to per-ID keying so distinct
// IDs are not collapsed into one.
func TestDedupePlayersByName_EmptyNameKeyedByID(t *testing.T) {
	in := []tui.PlayerView{
		{ID: 7},
		{ID: 8},
		{ID: 7}, // same ID as the first -> collapses
	}
	out := dedupePlayersByName(in)
	if len(out) != 2 {
		t.Fatalf("got %d entries, want 2 (IDs 7 and 8): %+v", len(out), out)
	}
}

// TestDedupePlayersByName_Empty verifies the nil/empty fast path.
func TestDedupePlayersByName_Empty(t *testing.T) {
	if got := dedupePlayersByName(nil); got != nil {
		t.Errorf("dedupePlayersByName(nil) = %+v, want nil", got)
	}
	if got := dedupePlayersByName([]tui.PlayerView{}); got != nil {
		t.Errorf("dedupePlayersByName(empty) = %+v, want nil", got)
	}
}

// TestPlayerCountFromDeduped documents the truthful-count arithmetic the bridge
// relies on: N deduped OTHER humans + 1 self = N+1 total. A 2-human game (one
// other human after dedup) must read 2, not the raw 4 connections.
func TestPlayerCountFromDeduped(t *testing.T) {
	// What the puppet-sync client sees in a 2-human game AFTER self-echo is
	// filtered out by remoteViews: the other human's two same-named twins.
	otherHumanTwins := []tui.PlayerView{
		{ID: 3, Name: "Zelda", X: 10, Y: 20, Z: 30},
		{ID: 4, Name: "Zelda"},
	}
	count := len(dedupePlayersByName(otherHumanTwins)) + 1
	if count != 2 {
		t.Errorf("2-human count = %d, want 2", count)
	}
}
