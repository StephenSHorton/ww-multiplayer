// Package engine is the multiplayer session core (docs/api/00-design.md
// §7): the status bridge between a running session and the controller
// (LiveState), the host and join sessions, the pipelines they run, the
// standalone server and fake client, and the player clients with this
// player's color preference.
//
// Package main keeps the CLI parsing (--port, --color, the host/join/
// server argument parsers) and hands the parsed values in.
package engine
