package coopsession

// M6's kill union in a --coop session (docs/08-coop-design.md §7.2, §9
// M6.0): a partner's recorded deaths of keyed enemies reach each game
// through the hub's KILL stores, judged by the M1 enemy table on both
// sides: the host's hub keeps and relays only the deaths its tables sync
// (hub.Config.SyncedKill, synctables.SyncedKill), and each writing
// replica writes only those its own sync (replica.Config.Tables), into
// the zones of the stage's loaded rooms, and light-shrinks the live enemy.
// hub.FeatureCombat is a TableFeature: a player whose stage tables are not
// the host's is not granted it, and its replica says so on the welcome.

import (
	"fmt"
	"strings"

	"github.com/StephenSHorton/ww-multiplayer/mods/coop/combat"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/hub"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/replica"
	"github.com/StephenSHorton/ww-multiplayer/mods/coop/world"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/log"
	"github.com/StephenSHorton/ww-multiplayer/wwapi/tables"
)

// combatCount is what t's enemy table syncs: the keyed K1 entries synced,
// in how many stages, how many of them wait for a live check of their
// class, and the unkeyed ones that stay each player's own.
type combatCount struct {
	synced, stages, unverified, unkeyed int
}

func countCombat(t *tables.Tables) combatCount {
	var c combatCount
	if t == nil || t.Enemies == nil {
		return c
	}
	cov := t.Enemies.Coverage()
	c.synced, c.unverified, c.unkeyed = cov.Synced(), cov.UnverifiedTotal(), cov.Unkeyed
	for _, s := range cov.ByStage {
		if s.Synced > 0 {
			c.stages++
		}
	}
	return c
}

// logCoopCombat says whether this side's replicas write a partner's kills
// (§9 M6.0), by its own M1 tables t, how many enemies they cover, what
// stays each player's own, and what still waits for the owner's live
// checks. Whether the host grants combat its replica says on the welcome.
func logCoopCombat(rep log.Reporter, t *tables.Tables) {
	if t == nil || t.Enemies == nil {
		log.Logf(rep, log.Warn, "co-op: combat is off this session: without co-op stage tables no enemy is known to sync, so no partner's kill is written.")
		return
	}
	c := countCombat(t)
	log.Logf(rep, log.OK, "co-op: combat this session: a partner's kills of keyed enemies (%d in %d stage(s), K1 classes, no bosses) are recorded in this game and light-shrink the live enemy while both are in the same stage; %d unkeyed enemies (the sea, Savage Labyrinth, KATA_RM, caves, Hyrule and the rest) are not shared yet: each player fights their own.",
		c.synced, c.stages, c.unkeyed)
	var wait []string
	if !combat.DemoTypeVerified {
		wait = append(wait, "every light-shrink waits for the live check of Link's demo offset, so for now a partner's kill is only recorded")
	}
	if c.unverified > 0 {
		wait = append(wait, fmt.Sprintf("%d of them, of classes not yet checked live, are recorded but not light-shrunk", c.unverified))
	}
	if len(wait) > 0 {
		log.Logf(rep, log.Info, "co-op: combat: %s (docs/08 §8.2 K1).", strings.Join(wait, "; "))
	}
}

// combatStatus is /status's M6 line for st: whether a partner's kills are
// written, the kill bits held for Link's stage, and what the receiver
// wrote, light-shrank, held and refused.
func combatStatus(st replica.Status) string {
	c := st.Combat
	switch {
	case st.DryRun:
		return "combat: none in a dry run, which asks the host for the shared image alone"
	case !c.Granted:
		return "combat: not granted by the host (its co-op stage tables are not this side's, or it turned it off)"
	case !c.Tables:
		return "combat: granted, but this side has no co-op enemy table, so no kill is written"
	}
	where := "nowhere"
	if c.Place != world.Nowhere {
		where = fmt.Sprintf("%s (table %d)", c.Place.Stage, c.Place.Table)
	}
	held := fmt.Sprintf("%d held", c.Held)
	if c.Held > 0 {
		held = fmt.Sprintf("%d held (%s, link demo %d)", c.Held, c.LastHeld, c.DemoHeld)
	}
	return fmt.Sprintf("combat: on; %d partner kill bit(s) held for %s; %d written to zones, %d light-shrunk, %s, %d refused, %d verify failures",
		c.StoreBits, where, c.ZoneSet, c.Shrunk, held, c.Refused, c.VerifyFail)
}

// hubCombatStatus is /status's lines of the host's hub for combat:
// whether it runs the kill union and the kill stores it holds, and which
// peers it grants combat.
func hubCombatStatus(hs hub.Status) []string {
	on := "allowed"
	switch {
	case hs.Features&hub.FeatureCombat == 0:
		on = "off"
	case !hs.CombatAllowed:
		on = "off (the host has no co-op enemy table)"
	}
	out := []string{fmt.Sprintf("combat %s; %d kill store(s) held", on, hs.KillStores)}
	for _, p := range hs.Peers {
		g := "combat granted"
		if p.Features&hub.FeatureCombat == 0 {
			g = "combat not granted"
		}
		out = append(out, fmt.Sprintf("  %s (%s): %s", p.Name, p.State, g))
	}
	return out
}
