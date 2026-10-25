# The pre-game lobby (protocol)

**Status:** built in App PR 4 (the lobby protocol, the server's lobby, the lobby client, and the controller's lobby commands); the app's Lobby screen is App PR 7, and what Start launches (each player's Dolphin and the pose pipelines, then GO) App PR 8 (docs/01-architecture.md, "Start → Dolphin → in game"). Co-op from the lobby is App PR 9 (docs/01-architecture.md, "Co-op in the app"): the host app opens or founds the world after START, sets its hub with `Server.SetCoop` and only then sends GO with the world's ID; an app joiner starts co-op only on a GO that names a world; "Allow merging" in game is the settings' `merge`. No CLI subcommand opens a lobby, so `host`, `join`, `mp-local` and the TUI behave and print exactly as before.

Design of record: the app design's §3 (lobby protocol) and its owner decisions: JSON bodies, no protocol version bump, unique names, colors locked after Start (until v1.1), shared colors allowed (2026-10-16, §4), the GUI always uses the lobby and falls back automatically for older hosts, green stays in the palette.

## 1. Shape

The lobby is a third connection role on the existing server, next to the player and co-op connections.

- **Hello.** `MsgJoin(name)` then `MsgHello [version:2 = 2][role:1 = 2][caps:2]`, the same protocol-2 role hello co-op uses. `RoleLobby = 2`. Caps bit 0, `LobbyCapJSON`, must be set (the bodies are JSON); the other bits are reserved and sent as 0. A lobby hello without it is refused with `RejectRole`.
- **Welcome.** A plain `MsgWelcome [id][epoch][version]`, as for any connection. The ID comes from a lobby ID space of its own (1..255 among live lobby connections) and means nothing to the client.
- **Frames.** `MsgLobby` = `'B'` (0x42), payload `[sub:1][body]`. The body is one JSON object (UTF-8, at most `MaxLobbyBody` = 16 KiB). Unknown fields and unknown sub-messages are ignored, so either side can add fields later. An empty body reads as `{}`. The codec is `mods/multiplayer/net/lobby_wire.go` (`EncodeLobby`, `ParseLobby`, `DecodeLobby`); `TestLobbyGoldenBytes` pins its wire form.
- **Not a player.** A lobby connection is never in `s.players`: not in the roster, `PlayerCount`, the 255-player limit, or the position and pose relays. It keeps the heartbeat both ways (the server measures its RTT from the pongs) and carries chat (§5).
- **No version bump.** A server from before the lobby (331e772 and earlier protocol-2 builds) refuses role 2 with `RejectRole` ("unknown connection role 2"); the app takes that as "join directly" (§6). Old clients never send role 2.

Taken frame types: J W P L D C M K Q H R O B E S. `S` is the status probe (docs/11-self-hosting.md "Health check"): a connection whose first frame is `S` instead of `J` gets one `S` back and is closed, so it never reaches a role.

## 2. Sub-messages

| Sub | Name | Direction | Body (JSON) |
|---|---|---|---|
| 0x01 | HELLO | c→s, first after the welcome | `{token?, app, os, color?, dolphin, modsHash?, ownerKey?}` — `token` reclaims a held slot; `color` is `#RRGGBB` or a palette name; `modsHash` is the joiner's mods hash in lowercase hex (§5a); `ownerKey` is a dedicated server's owner key (§3 "Owner key"), never logged |
| 0x02 | WELCOME | s→c | `{member, token, host, phase, features}` — `token` is 16 random bytes in hex, for reconnects only |
| 0x03 | STATE | s→c, to every member after every change | `{phase, rev, settings, members:[{id, name, color, ready, host?, dolphin?, file?, rttMs?, via, away?}]}` |
| 0x04 | SET | c→s | `{color?, ready?, dolphin?, file?}`; a missing field is left as it is |
| 0x05 | (unused) | | Was COLOR, the answer to a color request not granted as asked. Every request is granted now (§4), so nothing sends it; the value stays unused. |
| 0x06 | SETTINGS | c→s, host only | `{coop: "off" \| "new" \| <world id>, dryRun, merge, liveSwitches, worlds?: [{id, seq, inUse?}]}` |
| 0x07 | START | c→s host, then s→c all | `{settings}` |
| 0x08 | GO | c→s host, then s→c all | `{world?}` |
| 0x09 | REQUEST | c→s, then s→host | `{kind: "merge", from}`; the server fills `from` |
| 0x0A | KICK | c→s host | `{member}` |
| 0x0B | NOTICE | s→c | `{code, text, suggest?}` |

Notice codes: `name_taken` (with a suggested free name; the connection closes), `full`, `kicked` (closes), `mods_differ` (the HELLO's `modsHash` is not the host's, §5a; closes), `replaced` (a newer connection took the slot; closes), `not_host`, `wrong_phase`, `colors_locked`, `settings_locked`, `bad_message`, `no_member`, `no_host`, `hello_first`, `slow_down` (over the message rate; the excess was dropped, §3), `owner_key` (the HELLO's `ownerKey` was wrong, or the server has none; not fatal: the member joined without the host role), `no_coop` (SETTINGS asked for co-op on a dedicated server, §3 "Owner key"). `kicked` is also the answer to a HELLO from a kicked name.

STATE is the whole lobby (the member count is small). A member that says HELLO after START (a late joiner, or a reclaim during `starting` or `in_game`) also gets START, and in game GO with the host's world, right after its STATE, so it launches like the others. After a reclaim that can repeat one it already had; receivers treat START and GO as idempotent. The server keeps the GO body until the lobby empties. `rev` grows with every change; a client drops a STATE whose `rev` is not newer than the one it has, and starts over at a new WELCOME (a restarted server counts from 0 again).

## 3. The lobby's state (`mods/multiplayer/net/lobby_state.go`, `lobby.go`)

`lobbyMachine` is the state, a pure machine: each input (a HELLO, a sub-message, a dropped connection, the hold timer, a change in the player connections) returns the frames to send. `LobbyHub`, one per `Server`, serializes it and delivers the frames through a non-blocking send queue per connection. With no lobby connection the hub is inert.

- **Flow control.** STATE is merged: a connection has at most one STATE waiting, and its writer fills in the lobby's current STATE when it gets there, so a burst of changes costs a slow reader one frame. Chat relays are dropped, not queued, when a queue is full. Only a peer 64 frames behind on everything else is disconnected (its slot is held, so it reconnects into it). Each lobby connection may send 20 `MsgLobby`/`MsgChat` frames a second (bursts of 40); the excess is dropped, with one `slow_down` notice per run of drops. So one member flipping ready as fast as it can doesn't make the server flood the others.

- **Phase.** `lobby` → `starting` (the host's START) → `in_game` (the host's GO). It only moves forward; it goes back to `lobby` (with the settings reset to co-op off) only when the last member is gone.
- **Members.** At most `MaxLobbyMembers` (24). Names are unique among members without regard to case (after `ValidatePlayerName`, which the join already ran); a duplicate gets `name_taken` with "Name (2)" (or the next free number) and the connection closes. Player connections keep today's rules: one human's two pose connections share a name.
- **Host.** The first member whose connection comes from this machine (a loopback address) is the host. A remote client can never claim it (unless the server has an owner key, next). A held host slot keeps the host. When the host's slot is gone (expired), the first live member from this machine becomes host. With none, the lobby has no host (START, GO, SETTINGS and KICK get `not_host`, REQUEST `no_host`) until a member from this machine says HELLO or reclaims its slot. STATE shows the change; the controller takes its host flag from our STATE row.
- **Owner key** (`mods/multiplayer/net/owner_key.go`; ww-online review idea 7). A dedicated `server` has no member on its own machine, so without a key its lobby never has a host. `server --owner-key K` (better `WW_OWNER_KEY`, which no process list shows; both trimmed; 16 to 256 bytes; an empty `--owner-key` is an error) sets one with `Server.SetOwnerKey`; with neither, `server` makes a 128-bit key (`GenerateOwnerKey`, 32 hex digits) and prints it once at start, so a dedicated server's lobby is always keyed. Then the rules above apply with "presented the key" in place of "from this machine": the first member whose HELLO carried the key is host, the role passes to the next live keyed member, and a loopback connection counts for nothing (behind a local TCP proxy, `tailscale serve` or an SSH tunnel every client is loopback). Without a key (an app-hosted session) nothing changes. The key is compared as SHA-256 in fixed time; a key over 256 bytes is refused unhashed. Wrong keys throttle their source address (the IPv4 address, or the IPv6 /64) across connections, and never refuse a key: after n wrong keys, the next HELLO with a key from that address waits until 250 ms × 2^(n-1) (at most 8 s) after the last wrong one, outside the hub's lock, before its key is compared; then the right key is accepted as from anywhere else. A real owner who shares an address with whoever is guessing (a NAT, a Docker gateway, a local proxy) still gets in. An address with no wrong key for 10 minutes starts over. The table holds at most 1024 addresses; past that the forgotten ones go first, then those with the fewest wrong keys, so a spray of fresh addresses doesn't push out a throttle doing its job. A key is recorded only when `helloAs` admits its HELLO: one refused for `name_taken`, `full`, `kicked` or `mods_differ` counts for nothing, so the app's name_taken retry counts a mistyped key once. A wrong key, or a key sent to a server with none, gets `owner_key` and joins as a player. The throttle only slows serial guessing; the key's entropy is the defense (16 bytes minimum, 128 bits by default). The role is held under the member's token like any host slot: a reclaim with the token keeps it, and its HELLO's key is not checked or counted again. The server logs only that a member's key was accepted or not, never the key or the address. The app's join form sends it from "Advanced: dedicated server owner key" (`JoinOptions.OwnerKey`, kept in memory for Retry, never saved). A dedicated server also refuses co-op in SETTINGS (`no_coop`, `Server.DenyCoop`): the co-op hub runs in the hosting app's own server process (`SetCoop`, §7), which a `server` is not, so a keyed host plays plain multiplayer there. The key crosses the wire as the rest of the protocol does, unencrypted; docs/11-self-hosting.md leads with Tailscale for that reason.
- **Held slots.** A member whose lobby connection drops is marked `away` and held for `LobbyHoldTimeout` (60 s) under its token: a HELLO with the token and the same name gets the same member (ID, color, ready, host) back. A HELLO with the token while the old connection still looks alive takes over and closes the old one (`replaced`). An expired slot is gone; the name is free again.
- **CLI players.** Player connections with no member of their name are listed as `via: "cli"` rows, one per name (lowest player ID), at most 16, with ID `65536 + player ID`, `ready: true`, the color the server gave them, and `dolphin: "in_game"` once a pose has arrived on one of them. They never get START or GO and play at once, as today. The app's own pose connections (the member's name) are not listed twice.
- **Settings.** Host only. In `lobby` everything can change (`coop` must be `off`, `new` or a 16-hex-digit world ID; the world list is capped at 16 and kept when a SETTINGS leaves it out). After START only `merge` may change (`settings_locked` otherwise).
- **Ready, Dolphin, file.** Any member, any phase. Free text is kept printable and short (Dolphin 16 bytes, file hint 80).
- **START / GO.** Host only, in `lobby` / `starting` respectively (`not_host`, `wrong_phase`). STATE with the new phase goes out before the relayed START or GO.
- **REQUEST.** Goes to the host's connection, if it is live (`no_host` otherwise).
- **KICK.** Host only. A member: its lobby connection gets `kicked` and closes, its slot is dropped (not held), and every player connection of its name is closed. A CLI row: its player connections are closed. Either way the name (any case) stays out until the lobby empties: a lobby HELLO under it gets `kicked`, and a player or co-op hello gets `MsgReject` with `RejectBadName` ("the host removed you from this session"). A reject is fatal for the client, so a CLI `join`'s reconnect loop stops instead of coming back as a `via: "cli"` row. Old clients know that code, so the wire is unchanged.

## 4. Colors

The lobby is the single source of a member's color, through `mods/multiplayer/playercolor`. Owner decision (Stephen, 2026-10-16): "I don't care that people are the same color. Remove that code too if it auto assigns a new color." Players may wear the same color, and nothing warns about it, refuses it or changes it.

- **At HELLO:** a member gets the color it asks for (a palette name or `#RRGGBB`), whoever else wears it. Without a preference (or with one that doesn't parse), classic green, the game's own tunic.
- **SET color:** always granted (STATE), whoever else wears it. There is no refusal and no suggestion.
- **Locked after START** (v1): a color change gets `colors_locked`. A member that joins late still gets the color it asks for.
- **Rule 0 on the server** (`pickColorLocked`): a player connection whose name is a member's gets the member's color, so the app's pose connections carry the lobby color with no hello preference, and every client's player-list trailer shows it. Other players (the CLI path, `--color` / `WW_COLOR`, docs/09) get their own preference, else their name's earlier color, else green; the members' colors don't affect them.
- **Rule 0 is consistent.** A lobby HELLO or SET holds `Server.mu` while it refreshes the player list, sets the member's color and publishes it to the table rule 0 reads, so a player connection of the member's name sees the member and its color together.
- **Wire.** COLOR (0x05) is no longer sent and the client ignores it (unknown sub-messages are ignored, §1). There is no protocol version bump: both sides of a session run the same release anyway.

## 5. Chat

`MsgChat` on a lobby connection (after its HELLO) is relayed with the member's name, as a player's is, to every player connection and to every other lobby connection. A player's chat also reaches the lobby connections. So the app chats before and during the game over one connection, and a CLI player sees lobby chat as an ordinary chat line.

## 5a. Mods (`modsHash`)

Every player needs the same `client_required` mods at the same versions (docs/api/02-mods.md, "mods_hash"). The app's lobby client sends its hash as HELLO's `modsHash` (64 hex digits; the SHA-256 of the sorted `id@version` of the `client_required` mods that load). A host whose own hash differs answers with the notice `mods_differ` and closes the connection. Its text lists the host's mods; the joiner's client adds its own list, so the player reads, as is in the TUI and the app's "Different mods" dialog:

> the mods differ: the host runs coop@1.0.0, multiplayer@1.0.0; you run multiplayer@1.0.0. Both players need the same mods turned on.

The dialog's first button, "Open Mods", opens the Mods page, where a compiled-in mod is turned on or off from the next start; "Close" closes it.

`mods_differ` is fatal, like `name_taken`. A HELLO without `modsHash` (a build from before mods) joins as before, and a host from before mods ignores the field (its decoder takes unknown fields). The player connections the app opens at START carry the same hash in their hello (docs/api/02-mods.md), so a CLI `join` is checked too; a refused one gets `MsgReject` with `RejectMods` and the same text. The mods hash did not change ProtocolVersion (it stayed 2); enemy sync later raised it to 3 (§6).

## 6. Compatibility

| Client \ Server | pre-lobby (protocol 2) | this build |
|---|---|---|
| CLI `join` / old app / TUI | as before | as before; the lobby lists it as `via: "cli"`. It never sees a `'B'` frame; a `'B'` it sends on a player connection is dropped |
| Lobby client (the app) | lobby hello → `RejectRole` → `network.IsLegacyHost(err)` → the controller joins directly | full lobby |
| Mods hash (this build) | ignored: HELLO's `modsHash`, and a player hello's `CapModsHash` and hash, are unknown to it | checked: another hash is refused (`mods_differ`, `RejectMods`); none is admitted |

The table is protocol 2's. Since enemy sync, `ProtocolVersion` is 3: a protocol-2 build (v0.4.1 or older) and this one refuse each other at the hello with `RejectVersion` (the version dialog), whatever the role, and the LAN list greys the other build (`TestVersion2PeersRefused`, `TestClientsStopOnVersion2Welcome`; the LAN list: `TestLanSessionViewMasked`). The doubles below now model a protocol-3 server.

`TestLobbyMixedVersions` runs a pre-lobby player client's exact bytes (at this build's protocol) against a server with a live lobby; `TestLobbyLegacyHostFallback` runs the lobby client against a stub of the pre-lobby server (and a protocol-1 one, which stays a version error).

## 7. The co-op start order

With a lobby the server runs before the host's co-op world is open (the app opens or founds it after START and sets the hub just before GO). A co-op connection that arrived before the hub used to get `RejectRole`, which is fatal for `RunCoop`, so a CLI `join --coop` that connected early never had co-op.

- `Server.SetCoop(h)` sets the `CoopHandler` under the server's lock, before or after `Start` (setting `Server.Coop` before `Start` still works). `SetCoop(nil)` turns co-op off again.
- While the lobby expects co-op (it has members, its phase is not `in_game`, and `settings.coop` is not `off`), a co-op hello that finds no handler is **held** for `coopHoldTimeout` (10 s) waiting for `SetCoop`, then attached on the same connection. If the hold runs out the connection is closed without a reject, a transient error, so the client backs off and tries again. The hold is kept under the client's default 15 s handshake read deadline on purpose (`TestCoopHoldUnderClientReadTimeout` pins it): past it the client would time out on its own. A user who sets `WW_READ_TIMEOUT_SECS` under 10 s ends the hold on the client side instead; that is a transient error too, so the client still retries.
- `Server.Stop` ends a held hello at once, without a reject, so a client that connected early retries against the restarted server instead of waiting out the hold on a dead one.
- Outside that window (no lobby, co-op off, or in game) a co-op hello with no handler is refused with `RejectRole` as before.

`TestCoopEarlyJoinNotRefusedPermanently`, `TestCoopHelloHeldUntilSetCoop` and `TestCoopHoldReleasedByStop` cover it.

## 8. The client and the controller

- **`network.LobbyClient`** (`lobby_client.go`): a `Client` with `RoleLobby` that sends HELLO after the welcome and waits for WELCOME (`Connect`), keeps the latest STATE and WELCOME, and has `Run`, which reconnects with the player connection's backoff, reclaiming the slot with the token (and, against a restarted server, asking for the color it had). `Run` stops on a fatal error: a pre-lobby host (`IsLegacyHost`), a version mismatch, or a `*LobbyNoticeError` (`name_taken`, `full`, `kicked`, `replaced`). Commands: `SetColor`, `SetReady`, `SetDolphin`, `SetFile`, `SetSettings`, `Start`, `Go`, `Kick`, `RequestMerge`, `SendChat`. Callbacks: `OnWelcome`, `OnState`, `OnNotice`, `OnStart`, `OnGo`, `OnRequest`, `OnLink` (each connect attempt's outcome and each drop, for the app's "Can't reach the host" dialog and reconnect banner), and `Client().OnChat`. `OnNotice` runs before a fatal notice ends `Connect` or `Run`, so the controller publishes `EventLobbyNotice` (with a `name_taken` suggestion) before the session ends.
- **`session.Controller`** (`internal/session/lobby.go`): `HostLobby` and `JoinLobby` start a lobby session; `State.Phase` goes `Connecting` → `InLobby` → `Starting` → `InGame` with the lobby, and `State.Lobby` holds its view (our member ID, host flag, phase, settings, members). Commands: `SetColor`, `SetReady`, `SetDolphin`, `SetFile`, `SetCoopSettings`, `Start`, `Go`, `Kick`, `RequestMerge`, and `SendChat` (which a lobby session sends on its lobby connection). The controller refuses before the wire what the phase or role rules out (`ErrNotHost`, `ErrWrongPhase`, `ErrColorsLocked`, `ErrNoLobby`); the server checks again. Events: `EventLobbyNotice`, `EventLobbyStart`, `EventLobbyGo`, `EventLobbyRequest`, `EventLegacyHost`, and `EventState` on every lobby change.
- **Fallback.** A `JoinLobby` whose engine reports `ErrLegacyHost` logs "This host runs an older ww-multiplayer without a lobby; joining directly.", publishes `EventLegacyHost`, sets `State.Lobby.Legacy`, and runs the engine's direct `Join` in the same session (phase `InGame`) with the same `JoinOptions`. The color chosen for the lobby goes to the player connections as their hello preference (pre-lobby hosts take one), over `--color` / `WW_COLOR`. It is not a failure.
- **Engine.** `engine.Engine` implements `session.LobbyEngine` in `mods/multiplayer/engine/lobby.go`: `HostLobby` starts the server and joins its lobby on `localhost`; `JoinLobby` joins `addr` (default port added); `Lobby` sends a command.

## 9. Tests and fuzzing

- `lobby_wire_test.go`: round trips of every body, golden bytes, refusals; **`FuzzLobby`** (the `'B'` codec: never panics, and whatever decodes re-encodes to a payload that decodes to the same value).
- `mods_test.go` (§5a): the hello with a mods hash byte for byte, a new client against a stub of a host from before mods (it ignores the caps and the hash; the join succeeds), old clients against a host with mods (admitted), matching and differing hashes (`RejectMods`, the host's list against ours), the lobby's `mods_differ`, and `modsHash` on the wire (omitted when empty, ignored by an old decoder). `FuzzParseHelloRole`, `FuzzServerHello` and `FuzzLobby` have seeds with the hash. The engine's `mods_test.go` runs real engines with different `Config.Mods` against each other: a lobby join ends on `mods_differ`, a direct join on `RejectMods`, both with the two lists, and matching mods join (the hosts' servers and the game side's pipelines carry the engine's mods).
- `owner_key_test.go`: the fixed-time match and the length cap, key generation, the per-address throttle (backoff and its cap, the right key never refused after many wrong ones, decay, IPv6 /64, v4-in-v6, the address cap that keeps active throttles), the real wait with the hub's lock free, the keyed machine (the key holder is host, loopback is not, the role passes and survives a reclaim without the key, no key is unchanged, `no_coop`), and the hub over real connections (accept, wrong key's notice, a reconnect not counted again, a throttle across connections that still lets the right key host, a refused HELLO not counted, no key in the log); `status_test.go`: the status probe (§1).
- `lobby_state_test.go`: host and names, colors (two members asking for the same color both get it at HELLO and SET, no preference is green, a CLI player's color doesn't move a member), phases and locks, hold/reclaim/expiry, the host's slot passing on, the empty lobby, CLI rows and kicks, kicks that stick, START and GO replayed at HELLO, REQUEST, the member cap; **`FuzzLobbyMachine`** (random joins, with or without an owner key on the lobby and on the HELLO, sub-messages, drops, timer ticks and player lists; checks unique names, one host (on a keyed lobby, one that presented the key), forward-only phase, colors fixed after START, increasing `rev`, every frame encodable, every live member sent START in `starting` and GO in `in_game`).
- `lobby_test.go`: the loopback integration test (two lobby clients and a CLI player: join, a shared color, ready, Start, colors locked, GO, rule 0 on the app's pose connection, chat both ways, REQUEST, lobby connections outside the roster and count), reconnect and expiry, a host restart, name taken (the notice arrives before `Run` returns), kicks, kicks that stick (a reconnecting CLI join stops), START and GO replayed on reclaim and to a late joiner, a ready flood against a slow reader, a member and a CLI player sharing a color, the legacy fallback, the JSON cap, mixed versions, the co-op start order (also Stop during a hold, and the hold under the client's read deadline), and **`FuzzServerHandleLobby`** (a lobby connection's whole server path over an arbitrary stream).
- `internal/session/lobby_test.go`: the controller's phases, commands, refusals, events and the legacy fallback over a fake engine; `mods/multiplayer/engine/lobby_test.go`: the real engine through the controller against a real server, the session color over `--color`, and the host flag from STATE.
- `mods/multiplayer/net/color_test.go`: the server's join path (two players asking for one color both get it; no preference is green; a reconnect keeps its color); `mods/multiplayer/playercolor`: `TestParse`, **`FuzzParse`**.

## 10. Join codes and LAN discovery (App PR 11)

Design of record: the app design's §2.2-§2.3, decision 12 (encoded codes and LAN discovery, no service; a relay only if #15 testing calls for one) and the owner decision "hide IP addresses by default". Neither touches the lobby protocol: a code is an address, and a beacon only says where a lobby is.

### 10.1 Join codes (`internal/netutil/joincode.go`)

A join code is the host's IPv4 address and lobby port, in Crockford base32 (`0-9 A-Z` without `I L O U`), with a check symbol:

| Code | Symbols | Data bits |
|---|---|---|
| `WW-XXXX-XXXX` (port 25565) | 7 data, then 1 check | kind `0` (3 bits), IPv4 (32 bits) |
| `WW-XXXX-XXXX-XXXX` (any other port) | 11 data, then 1 check | kind `1` (3 bits), 4 zero bits, IPv4 (32 bits), port (16 bits) |

The data bits are written most significant first, 5 bits a symbol. The check symbol `c` makes `c ⊕ Σ α^(i+1)·v_i = 0` over GF(32) (modulus x⁵+x²+1, α = x), where `v_i` is data symbol `i` counted from 0. Any single mistyped symbol and any swap of two neighbouring symbols (the check symbol included) is caught. Examples: `192.168.1.42:25565` is `WW-30AG-09AX`; `192.168.1.42:25570` is `WW-460N-00JM-RZ2Q`.

Parsing (`ParseJoinCode`) ignores case and surrounding space, takes the dashes anywhere or not at all, and reads `I`/`L` as 1 and `O` as 0; `U`, any other character, a wrong length, a wrong check symbol, an unknown kind or nonzero reserved bits are refused, with a message that doesn't repeat the code. `NormalizeAddr` turns a valid code into its `ip:port` (the code's port wins over a default port or `--port`), and `ValidateAddr` refuses a code-shaped string (`WW` and 8 or 12 letters and digits: `LooksLikeJoinCode`) that doesn't parse, so a host name of exactly that shape can't be joined by name. The CLI's `join` refuses such a string with the same reason before it dials (it is never looked up as a host name), and the app joins a default-port code as the bare IP, as the share card shows it and one would type it, so a host joined both ways is one recent host. IPv6 addresses have no code. `FuzzParseJoinCode` checks that whatever parses re-encodes to a code that parses to the same address.

A code is as private as the address it holds. `redact.Addr` masks a code-shaped string whole (`WW-••••-••••`, or `WW-••••-••••-••••` for a long one), and `redact.Text` masks every code-shaped token in a log or chat line, valid or not, so the window, Copy diagnostics and the TUI show none unless the "Show IP addresses" switch is on. The CLI prints no codes; its output is unchanged.

### 10.2 The beacon (`internal/lan`)

While the app hosts a lobby it sends one UDP datagram every 2 s (`BeaconInterval`) to port 25565 (`DefaultPort`; `WW_LAN_PORT` changes it for both sides) of the directed broadcast address of each IPv4 network on every interface that is up, can broadcast and isn't loopback (`BroadcastAddrs`: a `/24` LAN's `x.y.z.255`, Radmin VPN's `26.255.255.255`; a `/31`, a `/32` or a point-to-point interface such as Tailscale's has none). Big-endian, at most 512 bytes (`MaxBeacon`):

| Offset | Size | Field |
|---|---|---|
| 0 | 4 | magic `WWLB` |
| 4 | 1 | format version (1) |
| 5 | 1 | flags: bit 0 = the host chose a co-op world; the rest 0 |
| 6 | 2 | the host's network `ProtocolVersion` |
| 8 | 2 | the lobby's TCP port (not 0) |
| 10 | 1 | players: lobby members not away |
| 11 | 1 | the member limit (`MaxLobbyMembers`, 24) |
| 12 | 1 | phase: 0 lobby, 1 starting, 2 in game |
| 13 | 8 | instance: random, made once per app run |
| 21 | 1+n | app version, n ≤ 32 bytes |
| … | 1+n | the host's player name, n ≤ 64 bytes |

Strings are UTF-8 with printable characters only. There is no address in it (the joiner takes the datagram's source address and the port field), no lobby token and nothing else secret. `TestBeaconGolden` pins the bytes.

`Decode` refuses a wrong magic (`ErrNotBeacon`), a datagram over 512 bytes or too short, format 0, port 0, a string over its limit, invalid UTF-8 or an unprintable character, and, in format 1, a phase above 2 or trailing bytes (`ErrMalformed`). A newer format is read as far as format 1 goes: what it appends is ignored and an unknown phase reads as "unknown". `FuzzDecodeBeacon` checks it never panics and that whatever decodes re-encodes to the same beacon.

### 10.3 Listening (`lan.Listen`, `lan.Table`, `Bridge.LanSessions`)

While Home is shown the page asks `Bridge.LanSessions` every second. The first ask opens a listener on the beacon port, bound with `SO_REUSEADDR` (and `SO_REUSEPORT` on macOS, Linux and the BSDs) so two apps on one machine can both listen; it closes 5 s after the last ask, and at once when a session starts. Each datagram that decodes goes into the table under its instance:

- the app's own instance is skipped (`NewTable(self)`);
- a host heard from several addresses (a LAN and Radmin VPN) is one entry; its addresses are ranked LAN, Radmin VPN, Tailscale, other, then by which was heard first, and Join uses the best one heard in the last 6 s;
- beacons merge only when they agree on the instance, the host's name, the lobby port, the app version and the protocol: a beacon that copies a host's instance but says anything else of those is its own row, so it can't change where the real row's Join goes (an exact copy from a later address merges, but the address heard first stays ahead within its rank);
- an entry is listed until 6 s (`EntryTTL`, three beacons) after its last beacon;
- the table keeps at most 64 hosts and 8 addresses per host, and one source address can have at most 4 hosts listed (`maxPerSource`; a real host sends one), so a flood of forged beacons from one machine can neither grow the table nor fill it and hide the real hosts. Beacons are not authenticated: a machine on the network can still announce a fake session under its own address, which is why a join always takes a click.

Home lists each entry under Join, "Sessions on your network": the host's name (masked as text unless the switch is on, as a peer's name always is), player count, the phase when past the lobby, a Co-op chip, the network's label with the address masked (Show reveals it until Home is left) and a Join button. A host whose `ProtocolVersion` differs from ours is greyed with its app version and can't be joined (its server would refuse us with `RejectVersion`); another app version with the same protocol shows its version but stays joinable.

**Local checks.** `WW_LISTEN_HOST=127.0.0.1` (the lobby server's own switch) also keeps discovery on the machine: the listener binds 127.0.0.1 and the beacon goes to 127.255.255.255 and 127.0.0.1 on the beacon port, so nothing is sent to or opened on the network. The loopback broadcast reaches every app listening on the machine (on Windows a unicast datagram reaches only one of the sockets sharing the port); the unicast covers systems where a socket bound to 127.0.0.1 doesn't hear the broadcast, and a listener that hears both just refreshes its entry. `localhost` and `::1` count as 127.0.0.1; any other host name or IPv6 address turns discovery off (it is IPv4 only, and never falls back to every network when a host was named); another IPv4 address binds the listener to it and broadcasts only on its network. `WW_LAN=off` turns discovery off (no beacon, no listening). The gui package's tests run with discovery on 127.0.0.1 (`lanTestHost`, set by `TestMain`).

**Not verified yet:** whether Radmin VPN's virtual network delivers a directed broadcast to `26.255.255.255` between two machines. The beacon is sent there; if it doesn't arrive, the host's Radmin join code or address still works.
