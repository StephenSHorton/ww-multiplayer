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

**Attempted in this project's Path B step 1 (2026-04-18) then REVERTED**: bumping
to `0x80511000` to carve a 1 MB Link-#2 heap region. The arena shrank by 1 MB,
which directly shrank ZeldaHeap from ~6.1 MB to ~5.1 MB. Boot log showed
`Arena : 0x80511000 - 0x817f2120` (our patch landed) followed by dozens of
`Cannot allocate ... ZeldaHeap` errors while Outset was loading its archives,
plus `デモデータ読み込みエラー！！` ("Demo data load error") — game fell into a
demo fallback path instead of the title screen. **Lesson: MEM1 has no spare
megabyte; ZeldaHeap fills essentially all of the arena.** Path B had to pivot
away from arena carve-outs.

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

The mailbox lives between our T2 code section end and `__OSArenaLo = 0x80411000`. Address bumped as the mod grew:

| Location | When | Why bumped |
|---|---|---|
| `0x80410800` | original | Worked while mod ≤ 0x748 bytes. |
| `0x80410900` | mini-Link draft | Mod grew to 0x868; old slot fell inside code. |
| `0x80410F00` | **current** | Mod grew to 0x9A8. Now flush against arena start so there's max headroom before another collision. Callback pointer at `mailbox+0x08` = `0x80410F08`; the asm shim in `frame_shim` encodes this offset as `lwz 12, 0x0F08(12)`. |

**Lesson (learned twice):** any data slot we carve out of orphan memory has to sit ABOVE the eventual mod-end address. Each time mod size crosses the slot, the game's own instructions land at that address and `main01_init`'s write corrupts real code — the symptoms were a crash at `0x80410704` (`Invalid read 0x24`) on v1 and garbage reads from what looked like mailbox slots (slot fields showing `0x7C0803A6 = mtlr r0` instruction bytes) on v2.

## Mini-Link render pipeline — RIGID WORKING, SKINNED BLOCKED (2026-04-19)

Option B plan from `docs/06-roadmap.md`: render a second model from our
own code, separate from the actor system. **Breakthrough 2026-04-19**:
rigid (non-skinned) models render end-to-end. Skinned models (Link)
still crash Link via j3dSys pollution during `calc()`.

### Working recipe for rigid models (confirmed: broken-pot fragment tracks Link in real time, no crashes, sky clean)

```c
// 1. Once: resolve shared resident J3DModelData via static getRes.
// 2. Once: switch heap to ArchiveHeap, create J3DModel, restore heap.
JKRHeap* prev = JKRHeap_becomeCurrentHeap(mDoExt_getArchiveHeap());
mini_link_data  = getRes("Always", ALWAYS_BDL_MPM_TUBO, mObjectInfo, 64);
mini_link_model = mDoExt_J3DModel__create(mini_link_data, 0x80000, 0x11000022);
JKRHeap_becomeCurrentHeap(prev);

// 3. Each frame inside daPy_Draw hook (after calling Link's real draw):
write_base_tr_mtx(mini_link_model, world_xform);    // @ model+0x24
J3DModel* saved_model = *J3D_SYS_M_MODEL;           // 0x803EDA58 + 0x38
void*     saved_calc  = *J3D_SYS_M_CURRENT_MTX_CALC; // 0x803EDA58 + 0x30
J3DModel_calc(mini_link_model);                     // 0x802EE8C0
*J3D_SYS_M_MODEL            = saved_model;
*J3D_SYS_M_CURRENT_MTX_CALC = saved_calc;
mDoExt_modelEntryDL(mini_link_model);               // 0x8000F974
```

### What we learned

- **Blocker 1 root cause (modelEntryDL breaks sky)**: SHARED J3DModelData.
  `J3DModel::entry()` registers material/shape packets into `j3dSys`
  bucket lists each call. Two J3DModels built from Link's single
  J3DModelData register overlapping packets → bucket pollution →
  material/TEV state bleed into the sky render. Using a
  NON-SHARED J3DModelData (Tsubo) with the identical `modelEntryDL`
  path leaves the sky untouched. `modelEntryDL` itself is fine; the
  hazard is sharing the ModelData with a live actor.
- **Blocker 2 partial fix (calc crashes Link)**: wrapping `J3DModel::calc`
  in save/restore of `j3dSys.mModel` + `j3dSys.mCurrentMtxCalc` makes
  it safe **for rigid models**. Verified: Tsubo calc runs, draw
  matrices get populated from `mBaseTransformMtx`, mesh renders,
  Link's subsequent `checkEquipAnime` still sees its own state.
  Attempting the same with Link's own skinned ModelData STILL crashed
  at the exact same site (PC 0x8010C53C). Hypothesis: calc on a
  skinned model walks the skeleton via `mCurrentMtxCalc` and writes
  more j3dSys fields (`mMatPacket @ +0x3C`, `mShapePacket @ +0x40`,
  `mShape @ +0x44`, possibly static members `mCurrentMtx/mCurrentS/
  mParentS`). Expanded save/restore needed before Link can run calc
  alongside Link #1.
- **Matrix propagation is not automatic.** Writing `mBaseTransformMtx`
  at J3DModel+0x24 is only the base input. The actual GX upload reads
  from `mpDrawMtxBuf` (J3DModel+0x94), which is populated by `calc()`
  via the intermediate `mpNodeMtx[]` (+0x8C). Without `calc()` the
  draw buffer stays uninitialized and the model renders at origin
  with degenerate matrices — invisible. This is why the earlier
  "no calc, just write baseMtx" experiments produced no geometry.
- **`mDoExt_J3DModel__create` is NOT sufficient to enter the draw
  bucket by itself.** It calls `entryModelData` (which sets up the
  model's own packet arrays), NOT `entry()` (which inserts into
  j3dSys bucket lists). Either use `modelEntryDL` each frame (calls
  entry + lock + viewCalc) or `modelEntry` once + `modelUpdateDL`
  each frame (entry once + update/lock/viewCalc). Both work for
  non-shared ModelData.

### Addresses added this session (GZLE01)

- `mDoExt_modelUpdateDL @ 0x8000F84C` — update variant (no re-entry)
- `mDoExt_modelEntry @ 0x8000F8F8` — one-shot entry without DL
- `J3DModel_calc @ 0x802EE8C0`
- `j3dSys @ 0x803EDA58` — +0x30 = `mCurrentMtxCalc`, +0x38 = `mModel`
- `settingTevStruct @ 0x80193028` (dScnKy_env_light_c method)
- `setLightTevColorType @ 0x80193A34` (dScnKy_env_light_c method)
- `dKy_tevstr_init @ 0x80196EB4` (params: tevStr*, roomNo, 0xFF)
- `g_env_light @ 0x803E4AB4` (size 0xC9C)
- `dKy_tevstr_c` size = 0xB0
- `ALWAYS_BDL_MPM_TUBO = 0x31` (broken-pot-fragment model — the first
  visible geometry ever rendered by our pipeline. For a whole pot use
  a different type index — real Tsubo reads the BDL from
  `data().m6C` indexed by `mType`.)

### Draw-phase diagnostic: `mailbox.draw_progress`

Mailbox +0x0C is now `draw_progress`, written only from `daPy_draw_hook`
so execute phase can't overwrite it. Stages 30-36: hook entered / Link
draw returned / model non-NULL / state == 1 / matrix written / calc+
save/restore done / modelEntryDL returned. Invaluable when the visible
output is ambiguous — tells you exactly how far each frame reached.

### Next step for Link

Expanded save/restore covering at minimum `j3dSys.mMatPacket`,
`mShapePacket`, `mShape` (maybe a full 0x128-byte snapshot of the
j3dSys struct). If that still crashes, the issue is J3DSys static
members (`mCurrentMtx/mCurrentS/mParentS`) or state inside the shared
`J3DMtxCalc` object itself. If even that fails, the fallback is to
load a SEPARATE copy of Link's archive (different arcname slot) so
we have a second independent `J3DModelData`, avoiding the shared-
state problem at the ModelData level entirely.

### Infrastructure landmarks established this session

- `dRes_control_c::getRes` is a **static** method, not a member function. The Itanium mangling (`F...` without `CCF` / `CFv`) plus the decomp's explicit `static` keyword confirm this. Earlier we typed it as a member with a `this` arg, which shifted every real argument by one register — the game treated `&mObjectInfo[0]` (which at offset 0 inside dRes_control_c has the 14-byte string `"System\0...\0"` of the first archive slot) as `arcName`, which is why broken getRes calls spammed `<System.arc> getRes: res nothing !!` at ~143 logs/sec. **Beware**: static members in the decomp headers are NOT clearly flagged in the mangled names; read the header declaration or the decomp source before building a function-pointer typedef.
- Draw-phase hooking recipe: `hook_branchlink` at `0x80108210` (the `bl` inside `daPy_Draw`). Our C function must call `daPy_lk_c::draw @ 0x80107308` via function pointer to preserve Link's rendering, then do our own work, then return Link's original result (`BOOL`/`int`) so `daPy_Draw`'s caller sees the right value. This is the generic shape for "run code inside the actor-draw iterator" and should work for any actor's draw we want to piggyback on.
- Mini-Link J3DModel lives in ArchiveHeap (switched via `mDoExt_getArchiveHeap @ 0x80011AB4` + `JKRHeap::becomeCurrentHeap @ 0x802B03F8`). Neither ZeldaHeap (`mDoExt_getZeldaHeap @ 0x800118C0`) nor GameHeap (`mDoExt_getGameHeap @ 0x800117E4`) are better — heap starvation isn't the failure mode.

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

## Per-Frame Hook — SOLVED

### The problem

`hook_branchlink` overwrites the target instruction with a `bl`. If the target
is a function's prologue (`stwu r1, -N(r1)`, `stw r31, ...`), that instruction
is LOST — the function's stack frame is never allocated or a non-volatile reg
is never saved, and the game crashes downstream. That's why hooking
`fapGm_Execute` at entry or `+0x0C` both crashed.

### The fix — callback-pointer shim + bl-replay

`fapGm_Execute` at `0x800231E4` is a tiny wrapper:

```
stwu/mflr/stw                   # prologue
li r3, 0 / lis r4, ... / addi   # args for 1st call
bl +0x1BA88   # -> 0x8003EC84 (per-frame work #1)
li r3, 0
bl +0x2217A8  # -> 0x802449AC (per-frame work #2 — heap bookkeeping)
lwz/mtlr/addi/blr               # epilogue
```

Hook at **`0x80023204`** — the SECOND `bl`. Replacing a `bl` is transparent
to the host (caller already assumes volatile-reg clobber) and past the
prologue (stack frame valid). But we also have to REPLAY the replaced call,
because `0x802449AC` is critical — dropping it trips
`Failed assertion: m_Do_ext.cpp:2755 mDoExt_SaveCurrentHeap != 0` within
~1 sec of loading a save.

Replay mechanism must preserve LR. `0x802449AC` internally does `mflr` for
heap bookkeeping, so if LR points into our shim instead of `0x80023208`
(fapGm_Execute's post-bl address), heap state corrupts and the same
assertion fires. A `bctrl` replay clobbers LR — use a TAIL-CALL `bctr`
instead so LR stays = caller's LR.

Final shim shape (see `inject/src/multiplayer.c`):

```asm
frame_shim:
    mflr  0                # save caller LR (= 0x80023208)
    stwu  1, -0x20(1)
    stw   0, 0x10(1)
    ; call multiplayer_update via callback pointer (bctrl clobbers LR, that's
    ; ok — we saved it and restore it below)
    lis   12, 0x8041
    lwz   12, 0x0700(12)
    cmpwi 12, 0
    beq-  1f
    mtctr 12
    bctrl
1:  lwz   0, 0x10(1)
    addi  1, 1, 0x20
    mtlr  0                # LR = 0x80023208 again
    ; tail-call 0x802449AC — bctr does NOT touch LR, so its blr returns
    ; directly to 0x80023208 as if the original bl had run
    li    3, 0
    lis   12, 0x8024
    ori   12, 12, 0x49AC
    mtctr 12
    bctr
```

`main01_init` (hooked at `0x80006338`) publishes `&multiplayer_update` at
`0x80410700` once at thread start; `frame_shim` reads it each frame.

### Verified

Mailbox counter at `0x80410800` increments ~30/sec (matching 29.97 FPS) while
the game runs normally.

## PROC_PLAYER Won't Fit — GameHeap OOM

### The problem

Spawning a second Link via `fopAcM_create(PROC_PLAYER, ...)` queued fine and
even rendered briefly, but the game hard-crashed ~23 seconds later via
`OSPanic` → `PPCHalt`. The obvious (wrong) hypothesis was a singleton
`JUT_ASSERT` inside `d_a_player_main.cpp`.

### The actual cause

Dolphin's OSReport log during the crash (View → Show Log → enable OSReport):

```
Error: Cannot allocate memory 721040 (0xb0090) byte ... from 81523910
FreeSize=0003d770 TotalFreeSize=0003de00 HeapType=EXPH HeapSize=002ce770 GameHeap
見積もりヒープが確保できませんでした。  ("Couldn't allocate estimation heap")
```

Link's actor heap allocation is ~704 KB. The GameHeap (JKRExpHeap, ~2.9 MB
total) only had ~245 KB free after Outset finished loading. The 23-second
delay was ongoing fragmentation eating into that free pool until a
subsequent allocation null'd and tripped an assert downstream.

### Implication

Chasing `dComIfGp`-singleton asserts is a dead end for this failure. A
second full Link instance is memory-bound, not invariant-bound.

### Path forward

Proxy actor. `multiplayer.c` now spawns `PROC_Obj_Barrel` (~2 KB) to confirm
the queued-spawn infrastructure itself is sound independent of heap size.
If that's stable, next step is to find a humanoid NPC whose archive is
already resident on Outset and use that as Player 2's visual stand-in.

### Diagnostic recipe: read OSReport from Dolphin

Any future "game dies at X seconds" investigation should enable OSReport
logging BEFORE writing injected diagnostic code:

1. Dolphin → View → Show Log, View → Show Log Configuration
2. Verbosity: Info; tick Write to Window
3. Enable OSReport (EXI) channel
4. Repro the crash; read the tail of the log

Much cheaper than hooking `OSPanic` ourselves.

## Observations Worth Remembering

- Picking up a rupee triggers a display refresh. Before the pickup, the displayed rupee count may lag behind the stored value.
- The wallet cap (200/1000/5000) applies when the game reads the rupee value, not when we write it. So writing 777 and picking up a rupee shows 200 if your wallet is size 200.
- `PlayerPtr[0]` at `0x803CA754` is null when the game is at the title/file screen. Non-null only when a save is loaded.
