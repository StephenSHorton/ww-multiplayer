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

**Debug `fopAcM_fastCreate(PROC_PLAYER, ...)` — spawn the second Link.**

Per-frame hook is solid (heartbeat-only `multiplayer_update` is stable). Adding
the spawn block (full `multiplayer_update` with `frame_count >= 300`) triggered
the same `mDoExt_SaveCurrentHeap != 0` assertion around frame 300 (~10 sec in).
Root cause is inside the spawn block itself, not the hook mechanism.

### Things to try (in order)

- [ ] Re-enable spawn guard but increase gate to e.g. 1800 frames (60 sec) so we can rule out "game isn't fully booted yet"
- [ ] Before `fopAcM_fastCreate`, verify `PLAYER_PTR_ARRAY[0]` looks sane (non-zero, points into 0x80xxxxxx actor range, has a valid actor-struct header at known offsets)
- [ ] Drop the `PLAYER_PTR_ARRAY[0] = link;` writeback after spawn — the comment said "may overwrite" but that line may itself corrupt actor-registry state
- [ ] If still crashing: the `fopAcM_fastCreate` at `0x80024614` may need a different call context than what `fapGm_Execute`'s mid-body provides. Consider a LATER per-frame hook site (e.g., inside `fapGm_Execute`'s first called function `0x8003EC84`, after its own prologue)
- [ ] Instrument via mailbox: write a progress byte before each step (`mailbox->actor2_ptr = 0xAA` → `0xBB` → etc.) so the final value on crash reveals where we died
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
