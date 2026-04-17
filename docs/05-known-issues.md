# Known Issues and Hard-Won Learnings

This doc is the graveyard of things that wasted our time and how they were finally resolved. Check here before re-treading.

## The JIT Cache Wall — SOLVED

### The problem

Dolphin translates PowerPC to x86 at runtime and caches compiled blocks. Writing to a code address via `WriteProcessMemory` / AR codes / OnFrame did NOT invalidate the cache — the JIT kept running stale x86. Only Gecko C2 hooks invalidated the JIT properly.

### The fix

Build a proper ISO (`wit copy` from CISO) with a Freighter-patched DOL. At boot, Dolphin's apploader emulation loads the patched DOL including our new T2 section, and there is no JIT cache to invalidate (the code is there from the first execution).

## The ClearArena Wall — SOLVED

### The problem

`ClearArena()` (`dolphin/os/OS.c:163`) zeros memory between `__OSArenaLo` and `__OSArenaHi` during `OSInit`. `__OSArenaLo` defaults to `0x8040EFC0` (the linker's `__ArenaLo` symbol), so any section we put above that gets wiped before `main01` or `fapGm_Execute` ever runs.

Hooking `__init_user` to bump `__OSArenaLo` doesn't work: **`__init_user` is called AFTER `OSInit` in `__start`, not before** (roadmap originally had this order wrong).

### The fix

Direct DOL binary patch of OSInit's immediate load of `__ArenaLo`:

```
# OSInit at 0x8030173C. File offsets assume T1 load=0x800056e0 file=0x2620.
0x80301818: 40820010 bne +16             ->  60000000 nop    (always use our immediate)
0x8030181C: 3c608041 lis  r3, 0x8041    ->  3c608041 lis  r3, 0x8041  (same, for clarity)
0x80301820: 3863efc0 addi r3, r3, -0x1040 ->  38631000 addi r3, r3, 0x1000   (r3 = 0x80411000)
0x80301838: 40820030 bne +0x30            ->  48000030 b    +0x30   (skip debug OSSetArenaLo override)
```

Now `ClearArena`'s memset starts at `0x80411000`, skipping our T2 code at `0x80410000-0x80410448`.

## Freighter Silently Clobbers Game Code

When you tell Freighter `inject_address=0x80410000`, it doesn't just add a new T2 section — it also **overwrites five regions of the game's own text** to relocate the stack and arena to Freighter's aspirational values (`New OSArenaLo: 0x80420560`, `Stack Moved To: 0x80410548`, etc). Those writes are NEVER logged by Freighter.

Affected regions (file offsets, with inject at 0x80410000):

- `0x00002410` (8B, RAM `0x80005410`) — injects `lis r1, 0x8042 / ori r1, r1, 0x0548` into existing padding
- `0x000e82ac` (8B, RAM `0x800eb36c`) — overwrites `addi r12, r1, 8 / bl ???` with `lis r3, 0x8042 / ori r3, r3, 0x2660` (debug OSArenaLo)
- `0x000e82e4` (8B, RAM `0x800eb3a4`) — overwrites `li r0, -1 / stw r0, 4(r3)` with `lis r3, 0x8042 / ori r3, r3, 0x0560` (OSArenaLo)
- `0x000ee7fc` (12B, RAM `0x800f18bc`) — overwrites float ops with `lis r3, 0x8042 / ori r3, r3, 0x0648 / lis r3, 0x8041`
- `0x000ee80c` (4B, RAM `0x800f18cc`) — overwrites `rlwinm` with `ori r3, r3, 0x0548` (stack end)

**Symptom:** game boots but most rendering is broken (ocean, sky, terrain, Link invisible; only simple actors like grass/flowers render). The user can move around and the UI works.

**Fix:** after Freighter produces `patched.dol`, copy those exact byte ranges back from `original.dol`. See `build.py` for the revert table.

## Dolphin's DOL Loader Refuses Some Addresses

Addresses where `inject_address` was tested:

| Address | Loads? | Notes |
|---|---|---|
| `0x803FD000` | ❌ | Just past BSS end. Dolphin silently drops the section (runtime `0x803FD000` reads all zeros). |
| `0x80410000` | ✅ | Current chosen address. ~12 KB above default `__ArenaLo`. |
| `0x80500000` | ✅ | Works but wastes ~1 MB of arena. |

We don't fully understand Dolphin's rule for rejecting sections. Possibly related to BSS proximity or apploader-emulation heuristics.

## Mailbox Location Is Not BSS Padding

`0x803F6100` **looks** like it's in BSS but is actually inside the game's data section D6 (`0x803F60E0-0x803F6820`). Writing to it corrupts real game data and (depending on what the game does with that word) can cause subtle bugs including visible rendering glitches.

Current mailbox location: `0x80410800` — orphan memory between our T2 code (ends `0x80410448`) and `__OSArenaLo` (`0x80411000`). Not part of any DOL section, not part of the arena, not zeroed by anything.

## Dolphin caches INI files at startup

Edits to `GZLE01.ini` in `User/Config/GameSettings/` are only read once at Dolphin startup. Closing and reopening the GAME does not reload the INI — the USER has to fully quit and restart Dolphin. Wasted multiple sessions on this.

## CISO Block Boundaries

Wind Waker's DOL spans 2 blocks in the CISO. A naive "just write the DOL into the file" patcher corrupts the second block. Use `wit` to convert to plain ISO first and patch that instead.

## BSS Zeroing Overlaps with T2 — BENIGN, see above

Historically the advice was that T2 at `0x803FD000` overlaps the tail of BSS zeroing. Not quite true — the BSS init (via `_bss_init_info` in the DOL's own data) only zeros the declared ranges. The real zeroing culprit was `ClearArena`, not BSS init. That's now handled via the OSInit patch.

## Dolphin Doesn't Show Extracted Folders In Game List

`Config → Paths → Add <extracted folder>` doesn't cause Dolphin to show the game. Only ways to boot:

- Double-click a disc image file (ISO, CISO, GCM)
- `File → Open → <main.dol>` (but this skips disc filesystem — assets missing)

## Per-Frame Hook (fapGm_Execute) Crashes

Hooking `fapGm_Execute` first instruction at `0x800231E4` via `hook_branchlink` immediately crashes:

`Invalid read from 0x00000014, PC = 0x802abbec` (inside `checkStreamPlaying` — audio subsystem)

Suspect Freighter's trampoline at function entry doesn't preserve something the downstream code depends on. Current workaround: use `main01` for one-shot setup, defer per-frame hook research.

## Observations Worth Remembering

- Picking up a rupee triggers a display refresh. Before the pickup, the displayed rupee count may lag behind the stored value.
- The wallet cap (200/1000/5000) applies when the game reads the rupee value, not when we write it. So writing 777 and picking up a rupee shows 200 if your wallet is size 200.
- `PlayerPtr[0]` at `0x803CA754` is null when the game is at the title/file screen. Non-null only when a save is loaded.
