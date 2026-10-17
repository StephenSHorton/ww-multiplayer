// Package wwapi is the Wind Waker modding API: the mod runtime that
// collects compiled-in mods, resolves them and runs them under a
// supervisor.
//
// # v0 UNSTABLE
//
// This package is wwapi v0. Its API may change in any 0.x minor version,
// without a deprecation period. Nothing outside wwapi uses it yet (Phase B
// step B3 of docs/api/00-design.md); docs/api/02-mods.md describes it.
//
// # Mods
//
// A mod is a Go package compiled into the app. It embeds a mod.json
// (see [ParseManifest] and wwapi/schema/mod.schema.json) and implements
// [Mod]. The app lists its mods statically; there is no init()
// registration and no drop-in loading.
//
// [Resolve] checks a list of mods (manifest, enable filter, the api range
// against [Version], dependency ranges, cycles, single providers, needs,
// declared write sets) and puts the ones that can load in a load order.
// A [Runtime] then calls each mod's Init in that order and runs every
// Run under a supervisor: a panic or error moves only that mod to
// QUARANTINED and removes its event handlers.
package wwapi

import "errors"

// Version is the wwapi version (semver). Mods range-check it with the
// "api" field of their mod.json. 0.x minor versions may break.
const Version = "0.1.0"

// ErrFatal, returned (wrapped) from the net provider's Run, ends the
// whole session: the supervisor stops every mod and Runtime.Run returns
// that error as is, without logging it, as runPipelines does with
// network.ErrFatal (the provider has already reported it). From any other
// mod it is an ordinary error and quarantines that mod.
var ErrFatal = errors.New("wwapi: fatal session error")
