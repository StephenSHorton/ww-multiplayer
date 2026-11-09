# Documentation

Everything about Wind Waker Multiplayer, grouped by what you are here to do. New? Start with [Install and first run](install.md).

## Play

Install it, play with friends, keep it up to date.

- [Install and first run](install.md): Download, open, Setup, Host or Join (Windows and macOS)
- [Playing](playing.md): Setup, lobby, starting the game, shared enemies, co-op worlds, bug reports
- [The terminal menu and command line](command-line.md): host / join / patch from a terminal, headless and debug commands
- [Updates](updates.md): How the in-app updater works
- [What works and known limits](status.md): The feature list and what is not done yet
- [Known issues](05-known-issues.md): Pitfalls and workarounds

## Host

Run a session for your friends.

- [Playing over the internet](online.md): Tailscale or a port forward
- [Self-hosting a dedicated server](11-self-hosting.md): `ww-multiplayer server` on a machine of its own
- [The lobby](10-lobby.md): How sessions, join codes and LAN discovery work
- [Server stress test](stress-test.md): 4 and 8 synthetic players

## Build

Work on the code, build it, ship a release.

- [Setup on a new machine](../SETUP.md): Go, Dolphin, devkitPPC, Freighter and the C injection build
- [Building from source](building.md): Requirements and build commands for Windows and macOS
- [Releases, code signing and the website](releasing.md): Tags, the release workflow, signing secrets, GitHub Pages
- [Developing on macOS](mac-dev.md): Build, check and test on a Mac
- [The macOS test rig](mac-rig.md): One Dolphin or two, driven by agents or people
- [Testing](TESTING.md): The checks to run before pushing, and the live checks
- [Writing a mod](api/03-writing-a-mod.md): A walk through `mods/example`

## Internals

How it works and why it is built this way.

- [Architecture](01-architecture.md): How the pieces fit together
- [Dolphin memory access](02-dolphin-memory.md): Reading and writing the emulated GameCube
- [Code injection](03-code-injection.md): What works and what does not
- [Wind Waker memory addresses](04-ww-addresses.md): GZLE01 address table
- [Project history](06-history.md): Session-by-session log of how the pipeline was built
- [BetterSunshine research](07-bettersunshine-research.md): What was learned from BetterSunshineModule
- [Co-op design](08-coop-design.md): Shared progress between saves
- [Co-op that just works](coop-just-works.md): The zero-decision start plan
- [What is left for full co-op](coop-remaining.md): The remaining-work list
- [Player tunic colors](09-player-colors.md): How each player's color reaches everyone's game
- [UDP pose](12-udp-pose.md): The UDP pose path
- [Modding API: design](api/00-design.md): What was decided for `wwapi`
- [Modding API: events](api/01-events.md): The event bus
- [Modding API: mods](api/02-mods.md): Manifests and the supervisor
- [Enemy rows: next](design/enemy-rows-next.md): Design note
- [Native widescreen](design/native-widescreen.md): Design note
- [Widescreen culling](design/widescreen-culling.md): Design note

## Elsewhere

- [GitHub issues](https://github.com/StephenSHorton/ww-multiplayer/issues): what is next, grouped by epic
- [Release notes](release-notes/README.md): how a release body is written; one `v<version>.md` per release
- [Live runs](live/), [acceptance records](acceptance/), [stress runs](stress/): dated evidence from real test sessions
- [CLAUDE.md](../CLAUDE.md): context for AI sessions

[Back to the project README](../README.md) · Next: [Install and first run](install.md)
