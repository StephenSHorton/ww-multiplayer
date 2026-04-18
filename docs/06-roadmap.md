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
- **Queued-spawn pipeline proven end-to-end** (2026-04-18): `fopAcM_create(PROC_GRASS, 0, link_pos, room, link_angle, 0, -1, 0)` queues cleanly (pid `0x232`), `fpcM_Management` constructs the actor next frame, grass tuft renders at Link's position. Frame hook + shim + queued spawn is a stable foundation for syncing a remote player's position to any resident actor. PROC_Obj_Barrel froze (archive not on Outset); PROC_GRASS is always resident.
- **Full position-sync pipeline proven** (2026-04-18): `PROC_TSUBO` (pot, param=0 → "Always" archive) spawns with valid model pointer at `actor + 0x298` (`mpModel`, not base-class `+0x24C`). Programmatically-spawned pots idle in `mode_hide` (`m678 = 0` at actor+0x678); writing `m678 = 2` (mode_wait) makes them render and accept position writes. Verified live: Go `./ww.exe move-puppet x y z` → mailbox f32s → frame-hook copies to actor+0x1F8 → pot visibly teleports in-game. End-to-end loop from a host-side command to a visible remote actor is complete.
- **End-to-end network multiplayer working** (2026-04-18): full round-trip
  verified on a single machine via loopback. Pipeline:
  `broadcast-link` reads Link's live position → TCP → `server` relays →
  `puppet-sync` applies client-side lerp smoothing (k=0.2) → mailbox →
  C frame hook → puppet actor follows Link around Outset with ~80 ms trail.
  On two machines, swap `broadcast-link` for the remote player's instance
  and you have actual multiplayer. Three CLI commands now form the runtime:
  `server`, `broadcast-link <name> <addr>`, `puppet-sync <name> <addr>`,
  plus one-shot `unhide-puppet` (the m678=2 poke must run ~3 s after spawn
  — doing it inside the C hook from frame 1 of phase 2 corrupts TSUBO's
  construction and freezes the game).
- **Rotation sync** (2026-04-18): puppet-sync writes the remote's RotY to
  `mailbox.p2_rot_*` (s16 BE) every tick; the frame hook copies both
  `current.angle` (+0x204) and `shape_angle` (+0x20C) so the puppet faces
  the direction the remote is facing. No angular lerp yet (would need
  shortest-arc handling); raw copy looks clean at 20 Hz send / 60 Hz apply.
- **Humanoid-ish proxy working: seagull** (2026-04-18): `PROC_KAMOME`
  (0x00C3) — archive is resident on every sea-adjacent island. Spawns
  constructed-but-invisible because `daKamome_Draw` guards on
  `mSwitchNo != 0 || mbNoDraw != 0`; with param=0 the spawner leaves
  `mSwitchNo = 1` at `actor + 0x2AA`. Zeroing that byte makes the bird
  render with its glide animation, tracks Link's position over real TCP,
  and rotates to match. `unhide-puppet` now dispatches by proc: writes
  `m678 = 2` for TSUBO, clears `mSwitchNo` for KAMOME. Trying a human
  humanoid (e.g. NPC_KO1 kid) will first need a proc whose archive is
  actually resident on Outset outdoors — NPC_KO1 self-destructed during
  construction because kids' archive isn't loaded there.

## 🔬 Next Session Priority

Done as of 2026-04-18: spawn + pos-sync + network + lerp smoothing.
Remaining for a complete experience, in rough priority order:

1. **Two-machine test.** Confirms the LAN path works when it isn't loopback.
   Same three commands on each host, just point `broadcast-link` and
   `puppet-sync` at the shared server IP.
2. **Humanoid proxy.** One-line proc swap. Candidates to try (prefer ones
   whose archive is always resident on Outset): NPC_KO1/NPC_KO2 (Joel/Zill),
   NPC_OB1 (Abe/Sturgeon), or a Kamome seagull as a flying proxy. Apply the
   same mode_hide → mode_wait recipe.
3. **Rotation sync.** `broadcast-link` already puts RotY in the network
   packet; extend the frame hook to copy `mailbox.p2_rot_*` into
   `actor + 0x20C` (shape_angle). Then the puppet faces the direction the
   remote player is moving.
4. **Animation state.** Read Link's `mCurProc` (+0x31D8) on the broadcaster,
   stash in `mailbox.p2_anim`, find the same field on the puppet and set it
   per frame. Tricky if the proxy's state machine differs from PLAYER.
5. **Room transitions.** When players change stages (entering a house,
   leaving Outset), the puppet needs to despawn/respawn — our current actor
   reference goes stale on stage change.
6. **Bake `unhide-puppet` into `puppet-sync`** so a user doesn't have to
   manually unhide after connecting. Needs a safe delay detector (e.g. wait
   N seconds after first position arrives) — can't just poke immediately.

### Diagnosis history (2026-04-18)

The "PROC_PLAYER crashes after ~23s" behavior is **not** a singleton
`JUT_ASSERT`. Dolphin's OSReport log during the crash showed a GameHeap OOM:

```
Error: Cannot allocate memory 721040 (0xb0090) byte ... from 81523910
FreeSize=0003d770 TotalFreeSize=0003de00 HeapType=EXPH HeapSize=002ce770 GameHeap
見積もりヒープが確保できませんでした。
```

- Link #2 wants ~704 KB of player heap; GameHeap only has ~245 KB free after
  normal load. The 23-second delay was heap fragmentation tipping it over.
- `OSPanic` / `PPCHalt` is the *downstream* effect of a null allocation
  return, not a singleton-guard assertion.
- This kills the "chase d_a_player_main.cpp asserts" path. Spawning a *second
  full Link* in a live Outset is memory-bound on the default allocator.

### What's working

- `fopAcM_create(..., 0, link_pos, room, link_angle, 0, -1, 0)` successfully
  queues a spawn and `fpcM_Management` processes it next frame.
- Per-frame hook + callback shim is stable (docs/05).

### Current code

`inject/src/multiplayer.c` now spawns `PROC_Obj_Barrel` (0x01CE, ~2 KB)
instead of `PROC_PLAYER`. A barrel is a visual stand-in purely to isolate
"queued spawn works" from "second Link won't fit". Needs rebuild +
ISO-repatch to validate.

### Recommended approaches (next session)

- [ ] **Validate barrel spawn.** Rebuild inject, repatch ISO, boot. Expect
      a barrel to appear next to Link ~10s after load and stay stable. If it
      crashes too, our spawn context is still wrong (not just a memory issue).
- [ ] **Pick a humanoid proxy** that's already resident on the current stage
      so we pay no extra archive-load cost. Candidates to research in the
      decomp: Outset villagers (Abe, Mesa, Sturgeon, Rose, Joel, Zill),
      generic NPCs. Look for one whose archive is already loaded at runtime
      and whose actor heap is small (<100 KB).
- [ ] **If a humanoid proxy won't fit either:** allocate a dedicated
      JKRExpHeap in our orphan memory (`0x80411000+`) and pass it to
      `fopAcM_create`. Only needed if no resident NPC fits.
- [ ] **Long-term "real Link" path:** would need to grow GameHeap at init
      (before game actors allocate), or unload something large. Defer —
      proxy approach covers the visual goal.

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
2. Freighter project at `inject/` produces `patched.dol` via `python build.py`
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
