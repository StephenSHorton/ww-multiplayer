# The pre-game lobby (protocol)

**Status:** built in App PR 4 (the lobby protocol, the server's lobby, the lobby client, and the controller's lobby commands). The GUI that shows it (app PR 7) and what Start launches (Dolphin, the pose pipelines, co-op: PRs 8 and 9) come later. No CLI subcommand opens a lobby, so `host`, `join`, `mp-local` and the TUI behave and print exactly as before.

Design of record: the app design's §3 (lobby protocol) and its owner decisions: JSON bodies, no protocol version bump, unique names, colors locked after Start (until v1.1), the GUI always uses the lobby and falls back automatically for older hosts, green stays in the palette.

## 1. Shape

The lobby is a third connection role on the existing server, next to the player and co-op connections.

- **Hello.** `MsgJoin(name)` then `MsgHello [version:2 = 2][role:1 = 2][caps:2]`, the same protocol-2 role hello co-op uses. `RoleLobby = 2`. Caps bit 0, `LobbyCapJSON`, must be set (the bodies are JSON); the other bits are reserved and sent as 0. A lobby hello without it is refused with `RejectRole`.
- **Welcome.** A plain `MsgWelcome [id][epoch][version]`, as for any connection. The ID comes from a lobby ID space of its own (1..255 among live lobby connections) and means nothing to the client.
- **Frames.** `MsgLobby` = `'B'` (0x42), payload `[sub:1][body]`. The body is one JSON object (UTF-8, at most `MaxLobbyBody` = 16 KiB). Unknown fields and unknown sub-messages are ignored, so either side can add fields later. An empty body reads as `{}`. The codec is `internal/network/lobby_wire.go` (`EncodeLobby`, `ParseLobby`, `DecodeLobby`); `TestLobbyGoldenBytes` pins its wire form.
- **Not a player.** A lobby connection is never in `s.players`: not in the roster, `PlayerCount`, the 255-player limit, or the position and pose relays. It keeps the heartbeat both ways (the server measures its RTT from the pongs) and carries chat (§5).
- **No version bump.** A server from before the lobby (331e772 and earlier protocol-2 builds) refuses role 2 with `RejectRole` ("unknown connection role 2"); the app takes that as "join directly" (§6). Old clients never send role 2.

Taken frame types: J W P L D C M K Q H R O B.

## 2. Sub-messages

| Sub | Name | Direction | Body (JSON) |
|---|---|---|---|
| 0x01 | HELLO | c→s, first after the welcome | `{token?, app, os, color?, dolphin}` — `token` reclaims a held slot; `color` is `#RRGGBB` or a palette name |
| 0x02 | WELCOME | s→c | `{member, token, host, phase, features}` — `token` is 16 random bytes in hex, for reconnects only |
| 0x03 | STATE | s→c, to every member after every change | `{phase, rev, settings, members:[{id, name, color, ready, host?, dolphin?, file?, rttMs?, via, away?}]}` |
| 0x04 | SET | c→s | `{color?, ready?, dolphin?, file?}`; a missing field is left as it is |
| 0x05 | COLOR | s→c | `{granted: "#…" \| null, wanted, clashWith?, suggest}` |
| 0x06 | SETTINGS | c→s, host only | `{coop: "off" \| "new" \| <world id>, dryRun, merge, liveSwitches, worlds?: [{id, seq, inUse?}]}` |
| 0x07 | START | c→s host, then s→c all | `{settings}` |
| 0x08 | GO | c→s host, then s→c all | `{world?}` |
| 0x09 | REQUEST | c→s, then s→host | `{kind: "merge", from}`; the server fills `from` |
| 0x0A | KICK | c→s host | `{member}` |
| 0x0B | NOTICE | s→c | `{code, text, suggest?}` |

Notice codes: `name_taken` (with a suggested free name; the connection closes), `full`, `kicked` (closes), `replaced` (a newer connection took the slot; closes), `not_host`, `wrong_phase`, `colors_locked`, `settings_locked`, `bad_message`, `no_member`, `no_host`, `hello_first`, `slow_down` (over the message rate; the excess was dropped, §3). `kicked` is also the answer to a HELLO from a kicked name.

STATE is the whole lobby (the member count is small). A member that says HELLO after START (a late joiner, or a reclaim during `starting` or `in_game`) also gets START, and in game GO with the host's world, right after its STATE, so it launches like the others. After a reclaim that can repeat one it already had; receivers treat START and GO as idempotent. The server keeps the GO body until the lobby empties. `rev` grows with every change; a client drops a STATE whose `rev` is not newer than the one it has, and starts over at a new WELCOME (a restarted server counts from 0 again).

## 3. The lobby's state (`internal/network/lobby_state.go`, `lobby.go`)

`lobbyMachine` is the state, a pure machine: each input (a HELLO, a sub-message, a dropped connection, the hold timer, a change in the player connections) returns the frames to send. `LobbyHub`, one per `Server`, serializes it and delivers the frames through a non-blocking send queue per connection. With no lobby connection the hub is inert.

- **Flow control.** STATE is merged: a connection has at most one STATE waiting, and its writer fills in the lobby's current STATE when it gets there, so a burst of changes costs a slow reader one frame. Chat relays are dropped, not queued, when a queue is full. Only a peer 64 frames behind on everything else is disconnected (its slot is held, so it reconnects into it). Each lobby connection may send 20 `MsgLobby`/`MsgChat` frames a second (bursts of 40); the excess is dropped, with one `slow_down` notice per run of drops. So one member flipping ready as fast as it can doesn't make the server flood the others.

- **Phase.** `lobby` → `starting` (the host's START) → `in_game` (the host's GO). It only moves forward; it goes back to `lobby` (with the settings reset to co-op off) only when the last member is gone.
- **Members.** At most `MaxLobbyMembers` (24). Names are unique among members without regard to case (after `ValidatePlayerName`, which the join already ran); a duplicate gets `name_taken` with "Name (2)" (or the next free number) and the connection closes. Player connections keep today's rules: one human's two pose connections share a name.
- **Host.** The first member whose connection comes from this machine (a loopback address) is the host. A remote client can never claim it. A held host slot keeps the host. When the host's slot is gone (expired), the first live member from this machine becomes host. With none, the lobby has no host (START, GO, SETTINGS and KICK get `not_host`, REQUEST `no_host`) until a member from this machine says HELLO or reclaims its slot. STATE shows the change; the controller takes its host flag from our STATE row.
- **Held slots.** A member whose lobby connection drops is marked `away` and held for `LobbyHoldTimeout` (60 s) under its token: a HELLO with the token and the same name gets the same member (ID, color, ready, host) back. A HELLO with the token while the old connection still looks alive takes over and closes the old one (`replaced`). An expired slot is gone; the name is free again.
- **CLI players.** Player connections with no member of their name are listed as `via: "cli"` rows, one per name (lowest player ID), at most 16, with ID `65536 + player ID`, `ready: true`, the color the server gave them, and `dolphin: "in_game"` once a pose has arrived on one of them. They never get START or GO and play at once, as today. The app's own pose connections (the member's name) are not listed twice.
- **Settings.** Host only. In `lobby` everything can change (`coop` must be `off`, `new` or a 16-hex-digit world ID; the world list is capped at 16 and kept when a SETTINGS leaves it out). After START only `merge` may change (`settings_locked` otherwise).
- **Ready, Dolphin, file.** Any member, any phase. Free text is kept printable and short (Dolphin 16 bytes, file hint 80).
- **START / GO.** Host only, in `lobby` / `starting` respectively (`not_host`, `wrong_phase`). STATE with the new phase goes out before the relayed START or GO.
- **REQUEST.** Goes to the host's connection, if it is live (`no_host` otherwise).
- **KICK.** Host only. A member: its lobby connection gets `kicked` and closes, its slot is dropped (not held), and every player connection of its name is closed. A CLI row: its player connections are closed. Either way the name (any case) stays out until the lobby empties: a lobby HELLO under it gets `kicked`, and a player or co-op hello gets `MsgReject` with `RejectBadName` ("the host removed you from this session"). A reject is fatal for the client, so a CLI `join`'s reconnect loop stops instead of coming back as a `via: "cli"` row. Old clients know that code, so the wire is unchanged.

## 4. Colors

The lobby is the single source of a member's color, through `internal/playercolor`.

- **At HELLO:** a member with a preference gets `playercolor.Nearest(want, used)`: the preference when it is clear of every other member's and CLI player's color (`MinDistance`), else the clear palette color closest to it. When that is not what it asked for, a COLOR follows with `granted` set and `clashWith` naming who has it. When no palette color is clear, the preference is granted as asked and shared, as for SET below, with no COLOR. Without a preference, the first clear palette color (`Pick`). Green is a normal palette entry.
- **SET color:** granted (STATE) when clear; otherwise refused with a COLOR whose `granted` is null, `clashWith`, and `suggest` (the nearest clear color); the member keeps its color. First come, first served; the host has no override.
- **More than the palette separates:** when no palette color is clear, nobody is refused: the request is granted and colors are shared.
- **Locked after START** (v1): a color change gets `colors_locked`. A member that joins late still gets a color.
- **Rule 0 on the server** (`pickColorLocked`): a player connection whose name is a member's gets the member's color, so the app's pose connections carry the lobby color with no hello preference, and every client's player-list trailer shows it. Other players' colors (the CLI path, `--color` / `WW_COLOR`, docs/09) also stay clear of the members' colors. Without lobby members the rule is docs/09's, unchanged.
- **One pick at a time.** A lobby HELLO or SET holds `Server.mu` while it refreshes the player list, chooses the member's color and publishes it to the table rule 0 reads. A CLI player connecting at that moment either sees the member's new color or is already in the list the member's color is checked against, so the two can't take the same color at once.

## 5. Chat

`MsgChat` on a lobby connection (after its HELLO) is relayed with the member's name, as a player's is, to every player connection and to every other lobby connection. A player's chat also reaches the lobby connections. So the app chats before and during the game over one connection, and a CLI player sees lobby chat as an ordinary chat line.

## 6. Compatibility

| Client \ Server | pre-lobby (protocol 2) | this build |
|---|---|---|
| CLI `join` / old app / TUI | as before | as before; the lobby lists it as `via: "cli"`. It never sees a `'B'` frame; a `'B'` it sends on a player connection is dropped |
| Lobby client (the app) | lobby hello → `RejectRole` → `network.IsLegacyHost(err)` → the controller joins directly | full lobby |

`TestLobbyMixedVersions` runs the previous release's exact player bytes against a server with a live lobby; `TestLobbyLegacyHostFallback` runs the lobby client against a stub of the pre-lobby server (and a protocol-1 one, which stays a version error).

## 7. The co-op start order

With a lobby the server runs before the host's co-op world is open (the app opens or founds it after START and sets the hub just before GO). A co-op connection that arrived before the hub used to get `RejectRole`, which is fatal for `RunCoop`, so a CLI `join --coop` that connected early never had co-op.

- `Server.SetCoop(h)` sets the `CoopHandler` under the server's lock, before or after `Start` (setting `Server.Coop` before `Start` still works). `SetCoop(nil)` turns co-op off again.
- While the lobby expects co-op (it has members, its phase is not `in_game`, and `settings.coop` is not `off`), a co-op hello that finds no handler is **held** for `coopHoldTimeout` (10 s) waiting for `SetCoop`, then attached on the same connection. If the hold runs out the connection is closed without a reject, a transient error, so the client backs off and tries again. The hold is kept under the client's default 15 s handshake read deadline on purpose (`TestCoopHoldUnderClientReadTimeout` pins it): past it the client would time out on its own. A user who sets `WW_READ_TIMEOUT_SECS` under 10 s ends the hold on the client side instead; that is a transient error too, so the client still retries.
- `Server.Stop` ends a held hello at once, without a reject, so a client that connected early retries against the restarted server instead of waiting out the hold on a dead one.
- Outside that window (no lobby, co-op off, or in game) a co-op hello with no handler is refused with `RejectRole` as before.

`TestCoopEarlyJoinNotRefusedPermanently`, `TestCoopHelloHeldUntilSetCoop` and `TestCoopHoldReleasedByStop` cover it.

## 8. The client and the controller

- **`network.LobbyClient`** (`lobby_client.go`): a `Client` with `RoleLobby` that sends HELLO after the welcome and waits for WELCOME (`Connect`), keeps the latest STATE and WELCOME, and has `Run`, which reconnects with the player connection's backoff, reclaiming the slot with the token (and, against a restarted server, asking for the color it had). `Run` stops on a fatal error: a pre-lobby host (`IsLegacyHost`), a version mismatch, or a `*LobbyNoticeError` (`name_taken`, `full`, `kicked`, `replaced`). Commands: `SetColor`, `SetReady`, `SetDolphin`, `SetFile`, `SetSettings`, `Start`, `Go`, `Kick`, `RequestMerge`, `SendChat`. Callbacks: `OnWelcome`, `OnState`, `OnColor`, `OnNotice`, `OnStart`, `OnGo`, `OnRequest`, and `Client().OnChat`. `OnNotice` runs before a fatal notice ends `Connect` or `Run`, so the controller publishes `EventLobbyNotice` (with a `name_taken` suggestion) before the session ends.
- **`session.Controller`** (`internal/session/lobby.go`): `HostLobby` and `JoinLobby` start a lobby session; `State.Phase` goes `Connecting` → `InLobby` → `Starting` → `InGame` with the lobby, and `State.Lobby` holds its view (our member ID, host flag, phase, settings, members). Commands: `SetColor`, `SetReady`, `SetDolphin`, `SetFile`, `SetCoopSettings`, `Start`, `Go`, `Kick`, `RequestMerge`, and `SendChat` (which a lobby session sends on its lobby connection). The controller refuses before the wire what the phase or role rules out (`ErrNotHost`, `ErrWrongPhase`, `ErrColorsLocked`, `ErrNoLobby`); the server checks again. Events: `EventLobbyColor`, `EventLobbyNotice`, `EventLobbyStart`, `EventLobbyGo`, `EventLobbyRequest`, `EventLegacyHost`, and `EventState` on every lobby change.
- **Fallback.** A `JoinLobby` whose engine reports `ErrLegacyHost` logs "This host runs an older ww-multiplayer without a lobby; joining directly.", publishes `EventLegacyHost`, sets `State.Lobby.Legacy`, and runs the engine's direct `Join` in the same session (phase `InGame`) with the same `JoinOptions`. The color chosen for the lobby goes to the player connections as their hello preference (pre-lobby hosts take one), over `--color` / `WW_COLOR`. It is not a failure.
- **Engine.** `sessionEngine` implements `session.LobbyEngine` in `lobby_engine.go`: `HostLobby` starts the server and joins its lobby on `localhost`; `JoinLobby` joins `addr` (default port added); `Lobby` sends a command.

## 9. Tests and fuzzing

- `lobby_wire_test.go`: round trips of every body, golden bytes, refusals; **`FuzzLobby`** (the `'B'` codec: never panics, and whatever decodes re-encodes to a payload that decodes to the same value).
- `lobby_state_test.go`: host and names, colors (clash at HELLO and SET, CLI colors, a full palette at HELLO and SET), phases and locks, hold/reclaim/expiry, the host's slot passing on, the empty lobby, CLI rows and kicks, kicks that stick, START and GO replayed at HELLO, REQUEST, the member cap; **`FuzzLobbyMachine`** (random joins, sub-messages, drops, timer ticks and player lists; checks unique names, one host, forward-only phase, colors fixed after START, no granted clash while a palette color is free, increasing `rev`, every frame encodable, every live member sent START in `starting` and GO in `in_game`).
- `lobby_test.go`: the loopback integration test (two lobby clients and a CLI player: join, colors with a clash, ready, Start, colors locked, GO, rule 0 on the app's pose connection, chat both ways, REQUEST, lobby connections outside the roster and count), reconnect and expiry, a host restart, name taken (the notice arrives before `Run` returns), kicks, kicks that stick (a reconnecting CLI join stops), START and GO replayed on reclaim and to a late joiner, a ready flood against a slow reader, the member/CLI color race, the legacy fallback, the JSON cap, mixed versions, the co-op start order (also Stop during a hold, and the hold under the client's read deadline), and **`FuzzServerHandleLobby`** (a lobby connection's whole server path over an arbitrary stream).
- `internal/session/lobby_test.go`: the controller's phases, commands, refusals, events and the legacy fallback over a fake engine; `lobby_engine_test.go`: the real engine through the controller against a real server, the session color over `--color`, and the host flag from STATE.
- `internal/playercolor`: `TestNearest`, **`FuzzNearest`**.
