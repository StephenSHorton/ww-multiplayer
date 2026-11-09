# Co-op that just works

Plan from card #1166 (2026-11-08). Status: **agreed** by the owner on
2026-11-08, with every choice in §8 answered as recommended. It is split
into the doing cards in §9. It supersedes #1163 and #1164, and it builds
on what they landed (both on main): #1163's co-op status line and
no-world GO (77bcb78), and #1164's stale-disc gate (PR #328, f31e9b6).

The target, in the owner's words: "They just want to play. We need to
rethink how we're doing multiplayer to make it just work for people
without having to jump around tapping buttons every update."

**Done when:** a fresh player who updates the app and presses Start ends
up in a co-op world, with no dialog to answer.

## 1. What went wrong on 11-08

A real two-player session ran into three walls. Each one needed a step
nobody would guess:

1. **Stale disc.** The host clicked Update (0.4.1 to 0.6.1). The disc was
   still patched by 0.4.x, and Start launched it anyway (`Bridge.Start`,
   `internal/gui/game.go:279`, never checks the disc). The game ran with
   no mod for 20 minutes. The only signal was an ERR line about 3 s after
   boot, plus a dialog that never comes back once closed. The startup
   re-patch did not run. It refuses whenever *any* Dolphin process exists
   (`Bridge.Repatch`, `internal/gui/update.go:578`), shows one toast, and
   is not tried again until the next app start (`planOnce`).
2. **Save tied to a missing world.** The host's save carried a co-op tag
   for world 0868…, which is not on this PC, and the host picked New
   world. `found()` (`mods/coop/session/app.go:401`) loops forever on a
   blocked file. GO was never sent, the lobby sat on "Starting", and the
   joiner was told nothing. The way out was a "Remove the tag" button,
   followed by an in-game save so the memory card kept the change.
3. **Dolphin's Widescreen Hack** put the name tags off to the side. That
   is being fixed on #1157 (the tag follows the hack), so it is not part
   of this plan.

## 2. What the player sees after this plan

**Host.** Clicks Update. The app restarts on the Play page (not Setup) and
shows one line: "Updating your disc for 0.6.2… 15 s". They press Host,
then Start. Dolphin launches and they load their save. One line appears
after that, for example "Co-op: continuing your world" or "Co-op: started
a new world from File 1 (Link)". There is no dialog and no in-game save
step.

**Joiner.** Joins with a code or over LAN and clicks Ready. Their disc
updates the same way, while they wait in the lobby. Their game starts at
the host's Start, and once they load a save, one line says "Co-op:
joined Stephen's world". If co-op can't run for someone, every window
says why in one line, and pose sync keeps working.

## 3. The disc is always current

| # | Change | Where |
|---|---|---|
| D1 | One full freshness check: the DOL **and** RELS.arc against this build. Today `patch.Inspect` reads the DOL only, so a REL-only release (at least 10 in the blob.stamp history) reads as current. | `internal/patch/inspect.go`; reuse `hasCurrentRELSArc` (`internal/inject/iso.go:190-222`) without writing |
| D2 | The check runs when the app opens, when the player hosts or joins, and right before any Dolphin launch. It runs again each time, not once per app run (`planOnce`). | `internal/gui/update.go` `discPlan`/`diskPlan`; `game.go` `checkDiscVersion` (PR #328) |
| D3 | A stale disc is re-patched automatically from `ISO.Vanilla`. It stays on the Play page with a progress line, never opens Setup, and never needs a toast to be read. A Start pressed meanwhile **waits for the patch and then starts**, instead of returning `errPatching`. | `update.js afterUpdate`, `Bridge.Start`, `StartPatch`/`waitPatch` |
| D4 | The re-patch writes the same path as today (`patch.DefaultOut(Vanilla)`). It does **not** rotate file names, because old discs would pile up in Dolphin's game list and a "start Dolphin myself" player could boot one. | `setup_patch.go` |
| D5 | Only one thing can stop the re-patch: a Dolphin holding the disc. The app may close a Dolphin **it launched itself** that is not in game, meaning at the title screen, with no game, or wrong/unpatched (`DolphinState`). Any other Dolphin gets a status line, "Close Dolphin to update your disc", with Start disabled until it closes. The re-patch then runs by itself. That needs a small process-close helper in the shipped exe (only `cmd/wwdev` has one today). | `wwapi/emu/launch`, `Bridge.Repatch` (narrow `len(b.procs()) > 0` to Dolphins on this user folder or ISO) |
| D6 | If the vanilla disc moved or was never saved, the app looks for a GZLE01 image that inspects as vanilla in the patched ISO's folder and in Dolphin's `ISOPath*` folders. Only if none turns up does the player get one clear line: "Choose your Wind Waker disc". That is the single unavoidable prompt, because nothing can be patched without the disc. | `internal/gui/update.go` `repatchPlan` |
| D7 | The joiner gets the same treatment. Its Dolphin launches on the wire START inside `GameRunner.run`, so the check also goes into a `Prepare` step before `DecideLaunch`. It normally finds nothing to do, because D2 already ran in the lobby. | `mods/multiplayer/engine/game.go:349` |
| D8 | The disc plan and every re-patch result go to an app log, not just a toast. On 11-08 nothing recorded whether a re-patch was even tried. | `internal/applog` |

**As built (card #1172, `internal/gui/disc.go`):** D2, D3, D5, D6 and D8 as
above, with these details:
- "Not in game" is a positive read, never Link's pointer alone, which is
  null during every stage load. Every one of three looks must read Wind
  Waker on the title stage (`sea_T`) with no stage change pending. A
  failed read, no game found (that read can't tell "none" from
  "unreadable"), a load, a cutscene or file select keeps the Dolphin open
  and shows the status line.
- "App-launched" means this run's session launched it, or its command
  line is `launch.LaunchArgs` on this player's `-u` folder and `-e` disc.
  That also covers a Dolphin an older version launched.
- The vanilla search tries the patched disc's own source name first
  (`<name>.iso/.gcm/.ciso`). Then it looks in the saved vanilla folder,
  the patched folder, and Dolphin's `ISOPath*` folders. It reads at most
  16 Wind Waker discs and gives up after 5 s. A miss is not searched
  again this run.
- Join waits for a running patch until card 3's `Prepare` step. Host
  doesn't wait.
- `ISO_PATH` (a rig override) turns the automatic update off.
- The app log is `<config>/ww-multiplayer/logs/<stamp>-app.log`. It is
  opened only when a check finds something to do.

The contract check in game stays as the backstop. It alone catches a save
state taken on an older version, which restores the old mod into RAM.
PR #328's gate stays as the last refusal if D3 to D6 all fail, and then
it says why on the Play page.

## 4. The world follows the save (host)

Co-op is **on by default** in the app, in a new "auto" mode. The host no
longer picks New world or Continue each session. Today it starts Off every
time: the server defaults to `CoopOff`, and nothing applies
`config.Coop.LastMode`. Settings keep a switch to turn co-op off, and an
advanced "play a specific world" choice. In auto mode the world is decided
by the save the host loads, at the moment `found()` sees it in game and
idle:

| Host's loaded save | What the app does | Writes to the save |
|---|---|---|
| Tied to world W, and W is on this PC | Continue W, whatever the lobby said | none |
| Tied to world X, and X is **not** on this PC (the 11-08 case) | Choice §8-B. Recommended: found X here **under the same ID** from this save, and mark the world file "adopted" so a later meeting with the original X can be told apart | none |
| Not tied (empty, damaged, or a leaked tag), first quest | Found a new world from it and confirm the host's own bind automatically. Both sides are this one save, so nothing can be lost | the tag (§6) |
| Second quest, a newer tag layout, or junk in the tag area | No co-op this session. Every window says why in one line, and pose sync runs | none |
| The host never gets in game | The status says "waiting for <host> to load a save" (#1163), with the right reason. Today an unpatched Dolphin reads as "waiting for your Dolphin to run Wind Waker" forever | none |

The one line shown afterwards names what happened: continued, adopted, or
founded. The "World founded" dialog, the host's Bind dialog and "Remove
the tag" leave the normal path. They stay reachable from the co-op panel
for anyone who wants them.

Code: `found()` returns "open W", "found under X" or "found new" instead
of blocking (`app.go:484-501`). `openWorld` (`session.go:561`) accepts
that result. `foundFrom` (`session.go:853`) takes an optional ID instead
of always calling `bind.NewWorldID()`. `world.Create` already accepts any
unused ID (`world/store.go:226`). `foundCheck` allows Bound when the tag's
world is absent. The "World founded" dialog becomes the one-line notice.

## 5. The joiner follows the host

A joiner's co-op starts from the host's world, W from GO. Every binding
decision is made automatically:

| Joiner's loaded save | What happens |
|---|---|
| Tied to W | Armed, as today |
| Not tied (or leaked), progress the same as W or behind it | Bound automatically. The app sets the bind "yes" itself (`CoopOptions.Yes` / `AutoYes`; the app never sets it today) |
| Not tied, with progress W lacks (Ahead/Divergent), or a new game against a progressed W (FarBehind) | Choice §8-C. Recommended: merge is on by default for app hosts, so the bind goes ahead and the joiner's progress joins W. Without merge, FarBehind is a dead end today (the advice says to copy the host's save in Dolphin's Memory Card Manager) |
| Tied to X, X **not** on the joiner's PC | The foreign tag is cleared automatically, then the rows above apply. Today this takes two dialogs: "Remove the tag", then Bind |
| Tied to X, and X is the joiner's own world on its PC | Choice §8-D. Recommended: follow the host anyway. X stays on disk, untouched |
| Second quest, or the card backup failed | No co-op for this joiner. One line in every window says why |

Code: the hub's `onHello` refuses a file tied to another world
(`hub/session.go:212-216`), and so does the replica's `offerBind`
(`replica.go:1760`). The automatic clear runs `clearLoadedTag` first, as
"Remove the tag" does today, so those refusals stay as they are and the
next hello is Unbound. Merge-by-default changes the app host's lobby
default (`config.Coop.Merge`, `lobby_state.go`). The bind and scrub
questions, the far-behind advice and the foreign dialog leave the normal
path, and their outcome shows as the one-line notice.

## 6. The tag stays without an in-game save

Today every tag write goes to game RAM only, and loading the file from
the card brings the old tag back. That is how the Oct 19 re-tie was lost.
There are two ways to fix it, and the owner chooses (§8-A):

**A (recommended): the app writes the tag into the memory card.**
- Binding still happens live in RAM. That is where the slot the player
  picked is known, which avoids the "which of three slots" question
  before launch.
- At the same time the app records a pending tag:
  `{card file, slot, name hash, clear count, nonce, world}`.
- It writes the pending tag into the card at the next moment no Dolphin
  holds the card: Start before launch, the "Dolphin closed" event, or
  app quit.
- Rules for the write:
  - re-read the slot first, and write only if slot, name hash and clear
    count still match and the save date is non-zero (V5);
  - take `backup.Create` first;
  - write with `File.WithSlot` and `Encode` (they exist and round-trip
    exactly), through a temp file and a rename;
  - read it back with `Verify`.
- GCI folders, Dolphin's default, are covered by existing code. A raw
  `.raw` card has no writer yet, so it falls back to the RAM path and B's
  re-bind.
- Only the 0x30 reserve bytes change. Pack and Unpack round-trip the
  card exactly, so none of the game's own save fix-ups (the `life < 12`
  one) are needed.
- This reverses `docs/08-coop-design.md` line 62 and §5.1 ("Go never
  writes memory-card files"). The doing card edits both.

**B: never write the card; re-bind every session.**
- The app remembers `{card file, slot, name hash} → world`. When an
  untied save that matches is loaded, it re-binds it in RAM to that
  world, silently.
- No save file is ever touched by the app.
- But the card's tag goes stale whenever the player doesn't save, so the
  save is not self-describing on another PC or after a reinstall.
- The key is weaker than a tag: the game leaks tags on New Game and file
  copy (`bind/tag.go:9-15`).

With either choice, the session's own tag write in RAM still happens.
Saving in game keeps working as it always has.

## 7. Never stuck, never silent

- **#1163 (on main, 77bcb78):** a per-member co-op status in the lobby state,
  so every window shows why co-op isn't running, during the wait, after
  GO and on a rejoin. A founding that stays blocked sends GO with no
  world after a short wait.
- **Late world announcement (doing card):**
  - GO goes out right at Start, and the world follows when founding
    finishes.
  - The server accepts a world after an empty GO, and the host's
    `sendGo` stops returning once the lobby is in game (`app.go:267`).
  - Existing joiner builds already accept a second GO naming a world
    (`OnGo`'s `worldID == ""` check comes before `begin()`).
  - Lobby bodies ignore unknown fields, so no protocol bump is needed.
  - This replaces #1163's timer. "Starting" then never waits on co-op,
    and a host who loads a different save later still gets co-op.
- A failed `lc.Go` is retried instead of only logged (`app.go:270`,
  `engine/lobby.go:252`).
- **Version skew:** when the members' app versions differ (`HELLO.App`
  is stored and never compared), the lobby shows one line, "Whitney is
  on 0.6.0, you're on 0.6.1: ask them to update". This is status text,
  not a refusal.
- **First session with no memory card:** today `backup.Create` returns
  `ErrNoCards`, which turns tag writes off for the whole session, so a
  truly fresh player can't bind. With no card there is nothing to back
  up, so the app allows tags and takes the backup once a card exists.

## 8. Owner decisions (2026-11-08)

Asked as separate questions on card #1166. Each was answered with the
recommended option:

- **A. Persisting the tag:** the app writes it into the memory card: a
  GCI folder, with a backup first and a read-back check. A raw card falls
  back to the RAM path (§6 A).
- **B. A save tied to a world that isn't on this PC:** found that world
  here under the same ID, from this save, and mark it adopted. Nothing is
  written to the save.
- **C. Joiners whose progress differs from the host's world:** merge is
  on by default. The joiner's unique progress joins the host's world, and
  a new-game file catches up to it.
- **D. A joiner's save tied to the joiner's own world on its PC:** follow
  the host. The joiner's world stays on disk, untouched.
- **The plan as a whole:** agreed.

Decided in the plan without a question:
- The app may close a Dolphin it launched that isn't in game. Any other
  Dolphin gets a status line.
- Co-op on by default, with an off switch in Settings.
- The vanilla disc is searched for before the player is asked.
- The CLI and TUI stay as they are.

## 9. Doing cards

Board cards: 1 = #1171, 2 = #1172, 3 = #1176, 4 = #1173, 5 = #1177,
6 = #1178, 7 = #1175, 8 = #1174, 9 = #1179. In order. Cards on separate lines can run side by side.

1. **Full disc freshness check** (D1). Normal.
   - `InspectFull`: the DOL plus RELS.arc. `discPlan`,
     `checkDiscVersion` and Setup use it.
   - Done: a REL-only stale fixture reads OtherVersion.
2. **Automatic re-patch in the app** (D2, D3, D5, D6, D8). Normal. After 1.
   - Startup stays on Play. Start queues behind the patch.
   - The process-close helper, narrowed to app-launched Dolphins that
     aren't in game. The vanilla search. App log lines.
   - Done: a test with a stale disc and a saved vanilla image shows
     Start re-patching, then launching, with no dialog. Also checked
     once on the PC, starting from an older patched disc.
3. **Joiner pre-launch disc step** (D7). Normal. After 2.
   - The lobby-phase check plus a `Prepare` seam in `GameRunner`.
4. **World follows the save, and co-op on by default** (§4). Normal.
   - The `found()` decision table, `foundFrom` with an ID (choice B),
     the adopted marker, the host's bind confirmed automatically, the
     one-line notice, co-op "auto" as the app host's default, and a
     Settings off switch.
   - Done: session tests for each table row. The 11-08 save (tied to a
     missing world) starts co-op with no click.
5. **Joiner follows the host** (§5). Normal. After 4.
   - Automatic yes for bind and scrub, the automatic foreign clear,
     merge on by default (choice C), the own-world rule (choice D).
   - Revise `docs/08` §5.3 ("Binding is always an explicit prompt").
   - Done: tests for each joiner row. A two-Dolphin run on the Mac rig
     binds a fresh joiner with no dialog.
6. **Tag persistence** (§6, choice A or B). Normal (A touches save files:
   backup, verify and a temp-file rename are required). After 4.
   - Done (A): a pending tag reaches a GCI card after Dolphin closes, the
     card verifies, and loading it in game shows the file still bound.
7. **Late world announcement** (§7). Normal. After #1163 lands.
   - GO at Start, the world later, the `lc.Go` retry.
8. **First-session and skew fixes** (§7). Light.
   - `ErrNoCards` allows tags and backs up later. The version-skew
     lobby line.
9. **Acceptance run on two PCs with real input.** Normal, needs:pc.
   After 2 to 8.
   - Install an older release, patch, update the app, Host + Join,
     Start, load saves.
   - Done: both players are in one co-op world with no dialog, with
     screenshots and logs as proof.

## 10. Not in this plan

- **Save states.** Nothing before launch can catch one, because it
  restores the old mod and the old tag into RAM. The contract check in
  game stays the backstop. Its dialog should get a banner that stays
  (#1164's leftover; fold it into card 2).
- **The Update click itself.** One button, which players expect. No
  silent auto-install.
- **The CLI and TUI** (`host`/`join --coop`): unchanged.
- **Widescreen Hack:** #1157.
- **Dolphin's own auto-updater** replacing Dolphin mid-session (11-08,
  sessions 1 and 2). A possible later card: launch with a session-only
  `-C` override that turns its update track off, once the key name is
  checked against Dolphin's config.
