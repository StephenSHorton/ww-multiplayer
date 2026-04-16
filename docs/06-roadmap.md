# Roadmap

## ✅ Done

- Go project scaffold with Bubble Tea TUI
- Dolphin memory scanner (works on any Dolphin version)
- Real-time position reading at 50ms
- Proven writes to game data (rupees, Link's position/teleport)
- TCP networking with server and fake clients
- Freighter setup for C code compilation
- OnFrame-based code byte injection (230 entries)
- C2 Gecko hook that branches to our function
- **Built proper ISO from CISO + patched DOL using `wit` + manual FST shift** (eliminates JIT cache wall)
- **Confirmed `main01` at 0x80006338 is a one-shot init function (not per-frame)**; per-frame is `fapGm_Execute` at 0x800231E4
- **Identified the new blocker: `ClearArena()` in `dolphin/os/OS.c:163` zeros memory between OSArenaLo and OSArenaHi during `OSInit`** — runs in crt0 BEFORE main01 thread starts, wipes any code we put past the BSS end

## 🔬 Next Session Priority

**Get the second Link to actually spawn.**

The JIT cache problem is solved. The new wall is `ClearArena()` zeroing our injected code at `0x803FD000+` before any hook can fire.

### Current state of the build pipeline

1. CISO source: `Dolphin-x64/Roms/Legend of Zelda, The - The Wind Waker (USA, Canada).ciso`
2. Freighter project at `C:/Users/4step/Desktop/ww-inject/` produces `patched.dol`
3. `wit copy <ciso> <iso> --iso --trunc` decompresses to plain ISO
4. Python snippet (in conversation history) writes `patched.dol` at the disc's DOL offset and shifts FST by ~1 KB to make room
5. Output: `Dolphin-x64/Roms/WW_Multiplayer_Patched.iso`
6. Delete `%APPDATA%/Dolphin Emulator/Cache/gamelist.cache` and restart Dolphin to force a fresh scan
7. Boot ISO (no patches/Gecko codes enabled — they would fight the DOL)

### The ClearArena problem in detail

```c
// src/dolphin/os/OS.c:163 in zeldaret/tww decomp
static void ClearArena(void) {
    if (OSGetResetCode() != 0x80000000) {
        memset(OSGetArenaLo(), 0U, (u32)OSGetArenaHi() - (u32)OSGetArenaLo());
        return;
    }
    // ... saved-region logic only fires on soft reset
}
```

- `OSArenaLo` defaults to end of BSS (~0x803FCFA8), so any T2 section at 0x803FD000 is wiped.
- Freighter's `New OSArenaLo: ...` override happens too late (in a ctor or main, after ClearArena).
- Putting `inject_address=0x80002800` (low memory, below game DOL) crashed the game — Freighter's stack/arena math assumes the inject address is high.

### Approaches ranked by viability

#### A. Hook `__init_user` to bump `__OSArenaLo` before ClearArena reads it (best)

`__init_user` is called early in crt0 from `__start.c:112`, BEFORE OSInit and ClearArena. Hooking it lets us write the new OSArenaLo value while ClearArena still sees the bumped one.

**Steps:**
- [ ] Add a second Freighter hook on `__init_user` that does `__OSArenaLo = 0x803FD500` (past our T2)
- [ ] Verify ClearArena's memset now skips our region (read 0x803FD000 after boot)
- [ ] Move hook target back to `fapGm_Execute` for per-frame execution
- [ ] Confirm rupee heartbeat (777 every frame)
- [ ] Test `fopAcM_fastCreate(PROC_PLAYER, ...)`

#### B. Patch ClearArena directly

Overwrite the call to `memset` (or its size argument) so it skips 0x803FD000-0x803FD500.

#### C. Test on Dolphin 5.0 Legacy

If 5.0's JIT or DOL loader behaves differently, the OnFrame approach might just work.

**Steps:**
- [ ] Download Dolphin 5.0 Legacy
- [ ] Run rupee heartbeat
- [ ] If it works, establish 5.0 as the supported version for distribution

#### D. GDB stub for JIT-aware writes

Dolphin supports the GDB Remote Serial Protocol. Writing memory via GDB properly invalidates the JIT cache.

**Steps:**
- [ ] Research Dolphin's GDB stub setup (enable in Config)
- [ ] Implement minimal GDB client in Go (just needs `M` memory write packet)
- [ ] Replace `WriteProcessMemory` calls with GDB writes
- [ ] Test instruction-level patching

## 🎮 After Spawn Works

Once the second Link spawns and we can control its position:

- [ ] Wire up network → actor position pipeline (Player A's position → server → Player B's mailbox → Player B's Link #2 renders)
- [ ] Add animation state sync (`mCurProc` at actor + `0x31D8`)
- [ ] Add rotation sync (`shape_angle` at `0x20C`)
- [ ] Color-differentiate Player 2 (modify TEV palette data)
- [ ] Handle room/stage transitions (despawn/respawn Player 2 when players change rooms)
- [ ] Implement presence indicator (show "Player 2 is on Outset Island" when out of view)
- [ ] Handle Player 2 disconnection gracefully

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
