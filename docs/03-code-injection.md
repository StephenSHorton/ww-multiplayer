# Code Injection — What Works and What Doesn't

The fundamental problem: to spawn a second Link actor, we need to CALL the game's `fopAcM_fastCreate` function. Calling functions requires executing code inside the emulated CPU's context — not just writing memory.

## Current state: WORKING

DOL injection is the solved path. Build pipeline:

1. Freighter compiles `inject/src/multiplayer.c` → `patched.dol` with a new T2 section at `0x80410000`
2. `build.py` applies four DOL binary patches to `OSInit` so `__OSArenaLo = 0x80411000` (protects T2 from `ClearArena`)
3. `build.py` reverts five Freighter silent clobbers of game code (see `docs/05-known-issues.md`)
4. `wit copy` produces a plain ISO from the CISO; Python shifts FST past the new DOL end
5. Boot the ISO in Dolphin — hook fires correctly, game renders normally

The rest of this doc is historical record of dead ends.

## Approaches Tried

### ❌ Gecko C0 Codes (standalone code execution)

Wrote PPC assembly as a Gecko C0 code. C0 codes run every frame as standalone functions inserted into the Gecko codehandler area (`0x80001800+`).

**What happened:**
- Simple tests (writing `DEADBEEF` to a mailbox every frame) worked.
- Calling `fopAcM_fastCreate` via `bctrl` crashed the game (invalid read).

**Why it failed:**
- C0 code runs with a weird stack/register state.
- The function call's stack allocation and PPC ABI assumptions weren't safe in the codehandler context.

### ❌ Gecko C2 Hooks (insert ASM)

C2 codes hook into a specific game instruction, replacing it with a branch to injected code. Injected code runs inside the game's own execution context (valid stack, proper PPC state).

**What happened:**
- The hook DID get applied — `0x80006338` showed a branch instruction.
- The game did execute our injected code — we verified with PC=`0x803FD108` error when code was missing.
- But writes we made via `WriteProcessMemory` weren't visible to the injected code (dual-mapping issue).
- And the injected code couldn't read triggers we wrote externally, so spawns never fired.

**Why it partially failed:**
- Dual memory mapping: our writes went to one view, the JIT read from another for addresses in the codehandler area.

### ❌ Freighter DOL Injection

[Freighter](https://github.com/kai13xd/Freighter) compiles C code with `devkitPPC` and injects it into a DOL file as a new section.

**What worked:**
- We wrote `multiplayer.c` calling `fopAcM_fastCreate(PROC_PLAYER, ...)`.
- Freighter compiled it and added a new text section at `0x803FCF20`.
- Patched the branch at `0x80006338` to call our function.
- The patched DOL is valid and has the code at the right file offsets.

**Why it failed:**
- **BSS zeroing**: the game's BSS region (`0x803A2960`, size `0x5A648`) ends at `0x803FCFA8`, overlapping the first `0x88` bytes of our injected section.
- When Dolphin zeros BSS after loading sections, it wipes part of our code.
- Even fixing BSS size, the area past the DOL end gets cleared by the game's startup code anyway.

### ❌ CISO Rebuild

Built a CISO with the patched DOL. Requires handling block boundaries correctly (the DOL spans 2 CISO blocks).

**What happened:**
- CISO rebuild works — code is in the file at the right offsets.
- Dolphin still shows `0x00000000` at the code addresses at runtime.
- Something (BSS zeroing or game startup) clears the area past the original DOL end.

### ❌ Extracted Folder Loading

Tried `Config → Paths → Add extracted folder`. Dolphin didn't recognize it.

Tried `File → Open main.dol` directly — the DOL booted but without disc assets (invisible textures).

### ⚠️ OnFrame Patches for Code Bytes

Used Dolphin's `[OnFrame]` patch section to write 1KB of PPC code byte-by-byte every frame.

**What worked:**
- 230 OnFrame entries successfully write our code to `0x803FD000+`.
- Verified via `ReadProcessMemory` — the code is present in memory.

**What didn't work:**
- Writing the hook branch at `0x80006338` via OnFrame didn't invalidate the JIT cache.
- Even with the AR-based hook writing the correct branch byte pattern, the JIT executes its cached version of `main01` with the original `stwu` instruction.

## Key Findings

### ✅ Confirmed Working

- `WriteProcessMemory` to game data addresses (rupees, positions) → game sees changes
- `ReadProcessMemory` from any mapped address → always works
- Dolphin `[OnFrame]` patches → work for data writes
- Dolphin Gecko C2 hooks → work for code replacement (JIT is invalidated)

### ❌ Confirmed Broken

- `WriteProcessMemory` to code section addresses to patch instructions → JIT doesn't invalidate
- AR codes writing to code addresses → JIT doesn't invalidate
- OnFrame writing to code addresses → JIT doesn't invalidate
- DOL sections past BSS end → get zeroed by BSS or game startup
- Booting a raw DOL file in Dolphin → missing disc assets

### 🤷 Unknown

- Why Gecko C2 hooks properly invalidate JIT but equivalent AR/OnFrame writes don't.
- Whether the code-area write problem is specific to Dolphin 2603 or a general behavior.

## Next Approach to Try

**Proper ISO rebuild with tool like GCIT.** Instead of patching the CISO directly, use a dedicated tool that:
1. Extracts the game to a folder
2. Replaces the DOL  
3. Rebuilds the ISO with correct padding, TOC, checksums

This would eliminate our CISO patching bugs and present Dolphin with a normally-structured game image.

Alternative: test on **Dolphin 5.0 Legacy** — if the JIT cache behavior differs, Gecko codes might "just work" there.
