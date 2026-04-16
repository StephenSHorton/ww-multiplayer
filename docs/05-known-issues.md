# Known Issues and The JIT Cache Wall

## The Core Problem

To spawn a second Link in Wind Waker, we need the emulated PowerPC CPU to execute a call to `fopAcM_fastCreate`. This requires either:

1. **Patching an existing instruction** to branch to our custom code (requires JIT cache invalidation)
2. **Injecting a new function** and making the game call it (same requirement)

Both approaches have been blocked by Dolphin's JIT cache behavior in version 2603.

## What is the JIT Cache?

Dolphin translates PowerPC instructions to x86 instructions at runtime for speed. Compiled blocks are cached. When we modify memory containing PowerPC code that the JIT has already compiled, the JIT continues executing the old x86 translation.

To pick up our changes, the JIT must be **invalidated** — told that the memory at a given address has changed so it discards the cached translation and recompiles on the next execution.

## What We Observed

### Gecko C2 hooks DO invalidate JIT

When a Gecko C2 hook is enabled via Dolphin's Gecko Codes UI, the JIT is properly invalidated. We verified this by:
- Hooking `main01` at `0x80006338`
- Running the game with no code at the branch target
- Getting the error `Unknown instruction 0x00000000 at PC = 0x803FD108`

The error proves the emulated CPU executed the new branch and jumped to the target.

### AR codes and OnFrame patches DO NOT invalidate JIT for code sections

Writing the same branch instruction via:
- AR code: `04006338 483F6EB1`
- OnFrame: `0x80006338:dword:0x483F6EB1`
- Gecko 04: `04006338 483F6EB1`

...writes the bytes to memory (we verified via `ReadProcessMemory`), but the JIT continues executing the original instruction. The emulated CPU never takes our branch.

### WriteProcessMemory has dual mapping issues

Our external `WriteProcessMemory` writes to Dolphin's backing store. The JIT reads code from a fastmem mapping that may or may not share pages with the backing store depending on the address.

- For **game data** addresses (save area, actor structs): writes propagate both ways.
- For **code addresses** and addresses past the DOL end: writes may not be visible to the JIT.

## Things That WASTED Time

### Dolphin caches INI files at startup

I edited `GZLE01.ini` dozens of times during debugging. Each time I said "restart the game" but the user was only closing and reopening the GAME within Dolphin — not restarting Dolphin itself. Dolphin reads the INI once at startup and keeps its copy in memory.

**Rule:** When editing `GZLE01.ini`, the user must close Dolphin entirely and reopen it. A game restart is not enough.

### CISO block boundaries

Wind Waker's DOL spans 2 blocks in the CISO. Our first patcher wrote it as a contiguous block, corrupting block 2 data. Fixed by writing per-block with file offset lookups.

### BSS overlaps with injected sections

Freighter places new text sections at addresses like `0x803FCF20`. But the game's BSS starts at `0x803A2960` and extends to `0x803FCFA8` — **past our code start**. Dolphin zeros BSS after loading sections, wiping the start of our code.

Shrinking the BSS size in the DOL header didn't help either — the game's startup code clears memory past the DOL end regardless.

### Dolphin doesn't show extracted folders in the game list

`Config → Paths → Add <extracted folder>` doesn't cause Dolphin to show the game. The only ways to boot:
- Double-click a disc image file (ISO, CISO, GCM)
- `File → Open → <main.dol>` but this skips disc file system, so assets are missing

## ClearArena Wipes Our Code (the new wall)

Even after rebuilding as a proper ISO with a Freighter-patched DOL (which DOES eliminate the JIT cache wall — hooks fire correctly), our injected code at `0x803FD000` is gone by the time main01 runs.

Root cause: `dolphin/os/OS.c:163` `ClearArena()`:
```c
memset(OSGetArenaLo(), 0U, OSGetArenaHi() - OSGetArenaLo());
```

`OSArenaLo` defaults to end of BSS (`0x803FCFA8`), so it zeros our T2 section. ClearArena is called from `OSInit` in crt0, BEFORE `main()` and BEFORE the main01 thread starts. Freighter's `__OSArenaLo = ...` override happens too late (in a ctor or main).

Tried `inject_address=0x80002800` (low memory): broke the game's startup. Freighter's stack/OSArenaLo math assumes a high inject address.

Best lead: hook `__init_user` (called early in crt0 from `__start.c:112`, before OSInit) and update `__OSArenaLo` there. See `docs/06-roadmap.md` approach A.

## Current Workaround Ideas

### 1. Rebuild as a proper ISO (not patch the CISO)

Use a tool like GCIT to extract, modify the DOL, and rebuild as a fresh ISO. Dolphin would boot it as a normal game with proper disc filesystem AND loaded code sections.

### 2. Try Dolphin 5.0 Legacy

The original Windwaker-coop was built for 5.0 Legacy. The JIT cache behavior may be different there. If Gecko codes "just work" on 5.0, we could use that for testing.

### 3. Find Dolphin's fastmem base

Instead of the backing store (what our scanner finds), write to the fastmem mapping that the JIT uses directly. Requires reverse-engineering Dolphin internals.

### 4. Use Dolphin's GDB stub

Dolphin exposes a GDB remote debugging protocol. Connecting to it lets us write memory with proper JIT invalidation. Would require implementing a GDB client in Go.

### 5. Patch only via Gecko C2

Keep the C2 hook as our hook mechanism (since it works), and use OnFrame only for the code body data (doesn't need JIT invalidation since we control the target address).

## Observations Worth Remembering

- Picking up a rupee triggers a display refresh. Before the pickup, the displayed rupee count may lag behind the stored value.
- The wallet cap (200/1000/5000) applies when the game reads the rupee value, not when we write it. So writing 777 and picking up a rupee shows 200 if your wallet is size 200.
- `PlayerPtr[0]` at `0x803CA754` is null when the game is at the title/file screen. Non-null only when a save is loaded.
