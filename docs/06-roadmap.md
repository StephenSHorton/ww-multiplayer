# Roadmap

## ✅ Done

- Go project scaffold with Bubble Tea TUI
- Dolphin memory scanner (works on any Dolphin version)
- Real-time position reading at 50ms
- Proven writes to game data (rupees, Link's position/teleport)
- TCP networking with server and fake clients
- Freighter setup for C code compilation
- **Built proper ISO from CISO + patched DOL using `wit` + FST shift**
- **Confirmed `main01` at 0x80006338 is a one-shot init function**; per-frame is `fapGm_Execute` at 0x800231E4
- **Cracked the `ClearArena` wall** by patching OSInit directly (`lis r3, 0x8041 / addi r3, r3, 0x1000` at 0x8030181C-0x80301820) to set `__OSArenaLo = 0x80411000` before `ClearArena`'s memset runs
- **Discovered Freighter silently clobbers game code** to relocate stack/arena to its own aspirational values — found 5 regions in T0/T1 that must be reverted post-build to keep game rendering correct
- **Found workable inject address** (`0x80410000`) — Dolphin's DOL loader refuses `0x803FDxxx` (just past BSS) but accepts addresses further up; still unknown exactly what Dolphin checks
- **Relocated mailbox** from `0x803F6100` (actually inside game data section D6, corrupting real data) to `0x80410800` (orphan memory between T2 end and `__OSArenaLo`)
- **End-to-end code injection verified**: main01 hook fires, our C code runs, mailbox counter increments, game continues rendering correctly
- **Per-frame hook working**: callback-pointer shim at `0x80023204` inside `fapGm_Execute`, with bl-replay via LR-preserving `bctr` tail-call to `0x802449AC`. Mailbox counter ticks at 30Hz in-game. See `docs/05-known-issues.md` → "Per-Frame Hook — SOLVED" for the shim recipe.

## 🔬 Next Session Priority

**Debug the downstream crash after queued Link spawn.**

### What's working

- `fopAcM_create(PROC_PLAYER, 0, link_pos, room, link_angle, 0, -1, 0)` at
  `0x8002451C` successfully QUEUES the spawn. Returns a valid `fpc_ProcID`
  (seen `0x233` in live testing).
- `fpcM_Management` processes the queue next frame; the game renders Outset
  Island normally, HUD intact.
- Game runs for ~23 seconds post-spawn before crashing.

### What's breaking

- Eventually `OSPanic` fires (write to sentinel `0x01234567` from `0x80006D64`,
  which is inside `OSPanic` at `0x80006C4C`). Game halts via `PPCHalt`.
- `d_a_player_main.cpp` has 54 `JUT_ASSERT`s — the crash is almost certainly
  one of them, tripped by Link-the-second's construction or its first execute
  tick touching singleton/global state (dComIfGp camera, player manager, save
  slot, etc.).
- Assertion text isn't visible on screen — probably killed before the next
  render pass.

### What we tried and ruled out

- `fopAcM_fastCreate` at `0x80024614` (synchronous construction) — trips
  `mDoExt_restoreCurrentHeap: mDoExt_SaveCurrentHeap != NULL` because the
  sync construction path runs heap save/restore mid-frame from our shim
  context, where the heap state isn't NULL-balanced. Switching to the queued
  `fopAcM_create` gets past this.
- The 9th `fastCreate` argument (`createFuncData`) — our typedef was missing
  it; fixed, but didn't help (the synchronous-context heap issue was the
  root cause, not a garbage-arg issue).

### Recommended approaches (next session)

- [ ] **Isolate: spawn a simpler actor** (rupee, grass, a debug actor) with
      the same queued mechanism. If it's stable, the crash is PROC_PLAYER-
      specific — a singleton guard somewhere. If it also crashes, something
      about our queued-spawn context is still wrong.
- [ ] Read the `OSPanic` message buffer from memory — the game likely stores
      the format string + args before calling `PPCHalt`. Finding that reveals
      the exact assertion.
- [ ] Scan `d_a_player_main.cpp` for asserts on `this == dComIfGp_getPlayer(0)`,
      camera IDs, player count, etc. — anything with a singleton assumption.
- [ ] As a workaround: if second Link is unspawnable, pivot to a different
      proxy actor for Player 2 (e.g., a tunic-wearing NPC) and sync its
      position to the remote player's coords.

## Hook + shim recipe (current working baseline)

Documented in `docs/05-known-issues.md` → "Per-Frame Hook — SOLVED". The
shim at `0x80023204` + callback pointer at `0x80410700` + main01_init hook
at `0x80006338` is the stable foundation for anything per-frame.
- [ ] Wire up network → actor position pipeline (Player A's pos → server → Player B's mailbox → Player B's Link #2 renders)
- [ ] Add animation state sync (`mCurProc` at actor + `0x31D8`)
- [ ] Add rotation sync (`shape_angle` at `0x20C`)
- [ ] Color-differentiate Player 2 (modify TEV palette data)
- [ ] Handle room/stage transitions (despawn/respawn Player 2 when players change rooms)
- [ ] Implement presence indicator (show "Player 2 is on Outset Island" when out of view)
- [ ] Handle Player 2 disconnection gracefully

## 🏗️ Build Pipeline

The full loop, current as of this session:

1. CISO source: `Dolphin-x64/Roms/Legend of Zelda, The - The Wind Waker (USA, Canada).ciso`
2. Freighter project at `C:/Users/4step/Desktop/ww-inject/` produces `patched.dol` via `python build.py`
   - Inject address: `0x80410000`
   - Hooks: main01 (0x80006338) → `multiplayer_update`
   - Post-build patches: OSInit immediates (four writes) + revert five Freighter clobbers
3. `wit copy <ciso> <iso> --iso --trunc --overwrite` decompresses to plain ISO
4. Python snippet writes `patched.dol` at the ISO's DOL offset and shifts the FST past the DOL end (we use a 0x1000-aligned new FST offset)
5. Update the ISO header's FST offset field (at disc offset `0x424`)
6. Delete `%APPDATA%/Dolphin Emulator/Cache/gamelist.cache` (if present) and restart Dolphin
7. Boot the patched ISO — **no Gecko codes / Dolphin patches enabled** (they fight the DOL)
8. `./ww.exe dump` to verify: mailbox counter at `0x80410800` increments, T2 code at `0x80410000` is intact, main01 hook at `0x80006338` shows `0x484XXXXX` (a `bl`)

## 🚀 Polish

- [ ] Better TUI dashboard: show remote players' positions on a mini-map
- [ ] Chat system (already have protocol support, need UI)
- [ ] Audio notifications when players join/leave
- [ ] Configurable port / multiplayer settings
- [ ] Installer / distribution: bundle Go binary + DOL patcher tool

## 🤔 Long-Term Questions

- How do we handle save files? Two players have separate saves with their own progress.
- What about combat? If one player attacks an enemy, does the other see it die?
- Puzzle rooms: one player solves a puzzle, does the door open for both?
- This starts to sound like a genuine co-op mod, not just a multiplayer viewer. Scope carefully.

## Known Untested

- Multiple clients connecting at once (server was tested with 2 fake clients, not stress-tested)
- IPv6 support
- Network reliability over the internet (tested on LAN only)
- Firewall / NAT traversal for non-LAN play
