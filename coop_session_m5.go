package main

// M5's live room puzzles in a --coop session (docs/08-coop-design.md §6.3,
// §9 M5): a co-present partner's latching dan switches (same stage and
// save table), zone switches (same room) and room switches (as edges,
// while both are in the room) reach each game through the hub's scoped
// stores, judged by the M1 switch table on both sides: the host's hub
// keeps and relays only the switches its tables mark latching
// (hub.Config.SyncedScoped), and each writing replica writes only those
// its own mark latching (replica.Config.Tables). hub.FeaturePuzzle is a
// TableFeature: a player whose stage tables are not the host's is not
// granted it, and its replica says so on the welcome.

import (
	"fmt"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/internal/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/synctables"
	"github.com/StephenSHorton/ww-multiplayer/internal/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables/switches"
)

// scopedVerdict is the M1 verdict on the switches SCOPED carries
// (synctables.SyncedScopedSwitch), as hub.Config.SyncedScoped takes
// it: nil without tables, so the hub keeps and relays none.
func (st *coopStageTables) scopedVerdict() func(table int, stage string, room, no int) bool {
	if st == nil || st.t == nil {
		return nil
	}
	t := st.t
	return func(table int, stage string, room, no int) bool {
		return synctables.SyncedScopedSwitch(t, table, stage, room, no)
	}
}

// puzzleCount is how many dan, zone and room switches t's verdict lets
// SCOPED carry, and in how many save tables (dan) and rooms (zone and
// room).
type puzzleCount struct {
	dan, zone, room int
	tables, rooms   int
}

func countPuzzles(t *tables.Tables) puzzleCount {
	var c puzzleCount
	if t == nil || t.Switches == nil {
		return c
	}
	danTables := map[int]bool{}
	rooms := map[switches.Scope]bool{}
	for _, s := range t.Switches.Switches {
		if !synctables.SyncedScopedSwitch(t, s.Table, s.Stage, s.Room, s.No) {
			continue
		}
		switch s.Store {
		case switches.StoreDan:
			c.dan++
			danTables[s.Table] = true
		case switches.StoreZone:
			c.zone++
			rooms[switches.Scope{Table: s.Table, Stage: s.Stage, Room: s.Room}] = true
		case switches.StoreRoom:
			c.room++
			rooms[switches.Scope{Table: s.Table, Stage: s.Stage, Room: s.Room}] = true
		}
	}
	c.tables, c.rooms = len(danTables), len(rooms)
	return c
}

// logCoopPuzzles says whether this side's replicas write the room puzzles
// (§9 M5), by its own M1 tables t, and how many switches t marks
// latching. Whether the host grants them its replica says on the
// welcome.
func logCoopPuzzles(rep log.Reporter, t *tables.Tables) {
	if t == nil || t.Switches == nil {
		log.Logf(rep, log.Warn, "co-op: room puzzles are off this session: without co-op stage tables no partner's dan, zone or room switch is known to latch, so none is written.")
		return
	}
	c := countPuzzles(t)
	log.Logf(rep, log.OK, "co-op: room puzzles this session: a partner's latching dan switches (%d in %d save table(s)) sync while both are in the same stage, its zone switches (%d) while both are in the same room, and its room switches (%d) as it sets them, in %d room(s); timed and other switches that clear never travel.",
		c.dan, c.tables, c.zone, c.room, c.rooms)
}

// puzzleStatus is /status's M5 line for st: whether the room puzzles are
// written, where Link is (the scope of the stores he takes), and what the
// receiver wrote, refused and dropped.
func puzzleStatus(st replica.Status) string {
	p := st.Puzzle
	where := "Link is in " + p.Place.String()
	if p.Place != world.Nowhere {
		if p.Zone >= 0 {
			where += fmt.Sprintf(", zone slot %d", p.Zone)
		} else {
			where += ", no zone for the room yet"
		}
	}
	switch {
	case st.DryRun:
		return "room puzzles: none in a dry run, which asks the host for the shared image alone; " + where
	case !p.Granted:
		return "room puzzles: not granted by the host (its co-op stage tables are not this side's, or it turned them off); " + where
	case !p.Tables:
		return "room puzzles: granted, but this side has no co-op stage tables, so none is written; " + where
	}
	return fmt.Sprintf("room puzzles: on; %s; %d partner switch(es) set, %d refused as not latching here, %d room edge(s) dropped",
		where, p.Applied, p.Refused, p.Dropped)
}

// hubPuzzleStatus is /status's lines of the host's hub for the room
// puzzles: whether it allows them, the scoped stores it holds by kind,
// and for each peer where it is and how many of the bits its SCOPED
// words carried the host's tables refused.
func hubPuzzleStatus(hs hub.Status) []string {
	on := "allowed"
	if hs.Features&hub.FeaturePuzzle == 0 {
		on = "off"
	}
	byKind := map[world.ScopeKind]int{}
	for _, k := range hs.Scopes {
		byKind[k.Kind]++
	}
	out := []string{fmt.Sprintf("room puzzles %s; scoped stores held: %d dan, %d zone, %d kill",
		on, byKind[world.ScopeDan], byKind[world.ScopeZone], byKind[world.ScopeKill])}
	for _, p := range hs.Peers {
		puzzle := "puzzles granted"
		if p.Features&hub.FeaturePuzzle == 0 {
			puzzle = "puzzles not granted"
		}
		out = append(out, fmt.Sprintf("  %s (%s): in %s; %s; %d scoped bit(s) refused by the host's tables",
			p.Name, p.State, p.Place, puzzle, p.ScopedRefused))
	}
	return out
}

// logHubStatus logs the host's hub lines of /status (hubPuzzleStatus).
func logHubStatus(rep log.Reporter, status func() (hub.Status, error)) {
	hs, err := status()
	if err != nil {
		log.Logf(rep, log.Warn, "co-op: host: %v", err)
		return
	}
	for _, l := range append(hubPuzzleStatus(hs), hubCombatStatus(hs)...) {
		log.Logf(rep, log.Info, "co-op: host: %s", strings.TrimRight(l, " "))
	}
}
