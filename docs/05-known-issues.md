# Known Issues and Hard-Won Learnings

This doc is the graveyard of things that wasted our time and how they were finally resolved. Check here before re-treading.

## The JIT Cache Wall — SOLVED

### The problem

Dolphin translates PowerPC to x86 at runtime and caches compiled blocks. Writing to a code address via `WriteProcessMemory` / AR codes / OnFrame did NOT invalidate the cache — the JIT kept running stale x86. Only Gecko C2 hooks invalidated the JIT properly.

### The fix

Build a proper ISO (then: `wit copy` from CISO; today `ww-multiplayer patch` reads the CISO itself) with a Freighter-patched DOL. At boot, Dolphin's apploader emulation loads the patched DOL including our new T2 section, and there is no JIT cache to invalidate (the code is there from the first execution).

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

Now `ClearArena`'s memset starts at `0x80411000`, skipping our T2 code at `0x80410000-0x80410448`. (Since bumped to `0x80414000` as the mod grew; `inject/build.py` has the current immediates.)

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
| `0x803FD000` | ❌ | Just past BSS end, inside the main stack (see the correction below). Dolphin silently drops the section (runtime `0x803FD000` reads all zeros). |
| `0x80410000` | ✅ | Current chosen address. ~4 KB (0x1040) above the default `__ArenaLo` (`0x8040EFC0`), ~12 KB above the main stack top. |
| `0x80500000` | ✅ | Works but wastes ~1 MB of arena. |

**Correction (2026-10-14, #23): `0x803FD000` is probably not a Dolphin loader rule.** We used to
say we didn't understand why Dolphin rejects it, perhaps because of BSS proximity or an
apploader heuristic. The address is inside the game's main stack, which is the likelier cause
(inferred; not re-tested):

- BSS ends at `0x803FCFA8` (tww `config/GZLE01/splits.txt:3328`, the last `.sbss2` range).
  The 64 KB main stack sits right above it and grows down from `0x8040CFA8`; above that are
  the 8 KB debug stack and the default `__ArenaLo` of `0x8040EFC0`. The stack range comes
  from the standard linker layout, not from a symbol: BSS end + 64 KB + 8 KB, rounded up to
  32 bytes, gives exactly that `__ArenaLo`. Boot writes one word just above the stack top:
  `__init_hardware`'s callees save LR at `0x8040CFAC`, which keeps `0x8000336C` (tww
  `src/dolphin/os/__start.c:173-183`, `OS.c:623-624`).
- At boot, `__OSThreadInit` sets the main thread's `stack_end` to `_stack_end` and calls
  `OSClearStack(0)` (tww `src/dolphin/os/OSThread.c:164-169`). That zeroes every word from
  `stack_end + 4` up to the current stack pointer (`OSThread.c:924-933`), so a section at
  `0x803FD000` reads back as zeros. Even if it survived, the main thread's stack would later
  grow into it.

`0x80410000` is above the stack top, which fits the table. docs/08 §4.5 (option B) considers
`0x8040D000` (above the stack top, inside the debug stack) only after a live read-back proves
the section survives boot.

## Mailbox Location Is Not BSS Padding

`0x803F6100` **looks** like it's in BSS but is actually inside the game's data section D6 (`0x803F60E0-0x803F6820`). Writing to it corrupts real game data and (depending on what the game does with that word) can cause subtle bugs including visible rendering glitches.

The mailbox lives between our T2 code section end and `__OSArenaLo` (today `0x80414000`). Address bumped as the mod grew:

| Location | When | Why bumped |
|---|---|---|
| `0x80410800` | original | Worked while mod ≤ 0x748 bytes. |
| `0x80410900` | mini-Link draft | Mod grew to 0x868; old slot fell inside code. |
| `0x80410F00` | superseded | Mod grew to 0x9A8. Flush against the arena start (then `0x80411000`). Callback pointer at `mailbox+0x08` = `0x80410F08`; `frame_shim` encoded it as `lwz 12, 0x0F08(12)`. |
| `0x80411F00` | echo ring | Mod grew to 0x11C8; `__OSArenaLo` bumped to `0x80412000`. |
| `0x80412F00` | **current** (eye-fix recipe onward) | Mod grew to 0x1FC8; `__OSArenaLo` bumped to `0x80413000`, later `0x80414000` to leave headroom after the mailbox. Callback pointer at `mailbox+0x08` = `0x80412F08`; `frame_shim` encodes it as `lwz 12, 0x2F08(12)`. Source of truth: `MAILBOX_ADDR` in `inject/include/mailbox_core.h`. |

**Lesson (learned twice):** any data slot we carve out of orphan memory has to sit ABOVE the eventual mod-end address. Each time mod size crosses the slot, the game's own instructions land at that address and `main01_init`'s write corrupts real code — the symptoms were a crash at `0x80410704` (`Invalid read 0x24`) on v1 and garbage reads from what looked like mailbox slots (slot fields showing `0x7C0803A6 = mtlr r0` instruction bytes) on v2.

## Mini-Link render pipeline — SKINNED LINK WORKING (2026-04-19)

Option B plan from `docs/06-history.md`: render a second model from our
own code, separate from the actor system. **Breakthrough 2026-04-19
(late)**: skinned Link renders end-to-end alongside Link #1, no crash,
sky clean. Mirrors Link #1's pose for now (joint callbacks act on
Link #1's state) — independent animation comes next.

### The mUserArea key

Link's joint callbacks are bound to the shared `J3DModelData` via
`J3DJoint` subclasses. During `calc()` they recover the owning
`daPy_lk_c*` from `J3DModel::mUserArea` (offset **0x14**) and access
its state. With our mini-Link's `mUserArea = 0`, the callback
dereferenced NULL at PC `0x8010C53C` (`lhz r0, 0x301C(r3)` inside
`checkEquipAnime`). Setting `*(u32*)(model+0x14) = (u32)link_this`
each frame routes the callback to the live Link #1 instance — calc
runs cleanly. (F19, 2026-10-18: those callbacks also *write* into
Link #1, which is why the pose feed no longer runs Link's real walker
on a mini-Link at all; see "Local Link's feet squished on slopes"
below. `mUserArea` is still set for the draw-time entry.) Pattern
verified against the public TWW decomp: every
actor with bound callbacks does `model->setUserArea((u32)this)`
right after creation. Without this, calc on shared skinned ModelData
is unreachable from outside the actor system.

### Working recipe for SKINNED Link (verified live: two Links visible, second mirrors first)

```c
// Once: resolve shared resident J3DModelData via static getRes.
// Once: switch heap to ArchiveHeap, create J3DModel, restore heap.
JKRHeap* prev = JKRHeap_becomeCurrentHeap(mDoExt_getArchiveHeap());
mini_link_data  = getRes("Link", LINK_BDL_CL, mObjectInfo, 64);
mini_link_model = mDoExt_J3DModel__create(mini_link_data, 0x80000, 0x11000022);
JKRHeap_becomeCurrentHeap(prev);

// Each frame inside daPy_Draw hook (after calling Link's real draw):
write_base_tr_mtx(mini_link_model, world_xform);     // @ model+0x24
*(u32*)(mini_link_model + 0x14) = (u32)link_actor;   // mUserArea — THE KEY
// Full 0x128-byte j3dSys snapshot around calc() so post-draw code
// (checkEquipAnime etc) sees Link #1's state, not our scratch.
memcpy(snapshot, j3dSys, 0x128);
J3DModel_calc(mini_link_model);                      // 0x802EE8C0
memcpy(j3dSys, snapshot, 0x128);
mDoExt_modelEntryDL(mini_link_model);                // 0x8000F974
```

### Working recipe for rigid models (kept for reference: broken-pot fragment tracks Link in real time, no crashes, sky clean)

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
- `J3DModel::calcAnmMtx @ 0x802EE5D8` (size 0xA4) — null-deref site
  for unbound basicMtxCalc is `+0x64` = `0x802EE63C`
- `J3DModelData + 0x24` = `mJointTree.mBasicMtxCalc` (J3DMtxCalc*) —
  the shared pose-walker pointer. Swap to control Link #2's pose.
- `JKRHeap::alloc(size, align, heap) @ 0x802B0434` — static allocator
  used for the shadow_link buffer (~19 KB from GameHeap).
- `j3dSys @ 0x803EDA58` — +0x30 = `mCurrentMtxCalc`, +0x38 = `mModel`
- `settingTevStruct @ 0x80193028` (dScnKy_env_light_c method)
- `setLightTevColorType @ 0x80193A34` (dScnKy_env_light_c method)
- `dKy_tevstr_init @ 0x80196EB4` (params: tevStr*, roomNo, 0xFF)
- `g_env_light @ 0x803E4AB4` (size 0xC9C)
- `dKy_tevstr_c` size = 0xB0
- `sizeof(daPy_lk_c) = 0x4C28` (from decomp d_a_player_main.h). Big
  enough to force runtime allocation (BSS at 19 KB collides with the
  mailbox at `0x80410F00`).
- `ALWAYS_BDL_MPM_TUBO = 0x31` (broken-pot-fragment model — the first
  visible geometry ever rendered by our pipeline. For a whole pot use
  a different type index — real Tsubo reads the BDL from
  `data().m6C` indexed by `mType`.)

### Draw-phase diagnostic: `mailbox.draw_progress`

(Historical: `draw_progress` and the `dbg_*` diagnostics were deleted in Phase E0; their bytes are reserved since E2.)

Mailbox +0x0C is now `draw_progress`, written only from `daPy_draw_hook`
so execute phase can't overwrite it. Stages 30-36: hook entered / Link
draw returned / model non-NULL / state == 1 / matrix written / calc+
save/restore done / modelEntryDL returned. Invaluable when the visible
output is ambiguous — tells you exactly how far each frame reached.

### Solved this session (late 2026-04-19)

- **Step 1 (5-field j3dSys save/restore: mMatPacket, mShapePacket,
  mShape on top of mModel + mCurrentMtxCalc)**: insufficient.
  draw_progress stuck at 34 = "calc didn't return".
- **Step 2 (full 0x128-byte j3dSys snapshot via memcpy)**: also
  insufficient. draw_progress stuck at 35 = "calc didn't return"
  even with the entire struct snapshot. Confirmed pollution lives
  outside j3dSys.
- **Bisect (calc + modelEntryDL stubbed → re-enable calc only)**:
  isolated calc as the sole crash trigger. modelEntryDL alone is
  fine; with calc on, draw_progress stuck at 35.
- **mUserArea = 0 was the root cause.** Joint callbacks inside
  `J3DModelData` use `model->getUserArea()` to find the owning actor.
  Wiring it to Link #1's `daPy_lk_c*` each frame fixed everything.
  Full 0x128 snapshot is still kept around calc as a safety net.

### MIRROR BROKEN — pose source identified (2026-04-19 late-late)

The userArea-shadow hypothesis was wrong. `mUserArea` is read only by
peripheral callbacks (equip/anim-status checks, `checkEquipAnime @
0x8010C53C`, the +0x301C field). **The skeletal walker does NOT look
at mUserArea.** Pose flows through `J3DModelData::mJointTree.mBasicMtxCalc`
(offset `+0x24`), which is shared between Link #1's model and our
mini-Link because they share `J3DModelData`.

Decisive evidence chain (all ran on Outset, save mid-session):
- **shadow_mode 1/2** (memcpy this_ → shadow, swap mUserArea): Link #2
  still mirrors Link #1. Confirms userArea is not pose-relevant.
- **shadow_mode 3a** (null out `mBasicMtxCalc` via `*(mini_link_data +
  0x24) = 0` around calc): Dolphin logs `Invalid read from 0x00000000,
  PC = 0x802ee63c`. `calcAnmMtx @ 0x802EE5D8` crashes exactly where
  `j3dSys.getCurrentMtxCalc()->init(...)` dereferences the null. Proves
  our write lands on the right field.
- **shadow_mode 3b** (swap `mBasicMtxCalc` to a NO-OP J3DMtxCalc whose
  vtable slots all point at a `blr` stub): calc completes cleanly,
  draw_progress 38, spawn_trigger advancing, sky clean. Link #2 **freezes
  in his last pose while Link #1 keeps animating**. DECOUPLED.

Why freeze instead of T-pose: `mpNodeMtx` on our J3DModel holds the
last frame's calc output. Skipping the walk (via no-op mtxcalc) leaves
that buffer untouched, so GX re-uses the previous matrices.

### Control surface unlocked

(Shadow modes 1-4 and `echo-delay` were deleted in Phase E0; mode 5 is the only
driven mode, and every other `shadow_mode` value draws nothing. The mailbox fields
stay as unused bytes.)

We now own Link #2's pose via ONE pointer: `J3DModelData + 0x24`.
Three useful modes fall out of this:
(Historical: today `shadow_mode = 0` means off and draws nothing, and mode 3 was deleted in Phase E0.)
- **Mirror** (the baseline then): leave `mBasicMtxCalc` alone. Cheap,
  visible, useful for debugging. `shadow_mode = 0` keeps this.
- **Freeze**: swap to no-op calc. `shadow_mode = 3` gives us this.
  Useful as a kill-switch and as a way to test whether downstream
  rendering tolerates a frozen pose.
- **Driven**: swap to a CUSTOM J3DMtxCalc whose `recursiveCalc` we
  write, and that reads a pose from wherever we want (network packet,
  stored animation, programmatic motion). This is the actual goal.

### Next step — driven mode

Write a `recursiveCalc` that accepts a pose buffer and writes joint
transforms into `mpNodeMtx` for our model. Smallest useful form:
- Read a "pose" array (one `J3DTransformInfo` per joint, or one Mtx
  per joint) from a fixed location Go populates.
- Walk the joint tree, composing matrices via the standard transform
  chain (scale × rot × trans, relative to parent).
- End up with `mpNodeMtx[0..jointNum-1]` populated.

Two directions to explore:
1. **Cheat with Link's own anim**: Before our no-op calc, manually
   call Link's `J3DMtxCalcAnm::calc(jnt_no)` for each joint but with
   a different animation frame/id. Harder — requires understanding
   how Link's anim state is composed and finding the injection point.
2. **Write matrices directly**: Skip the J3DMtxCalc abstraction
   entirely. Our basicMtxCalc remains no-op, but before modelEntryDL
   we `memcpy()` our desired matrices into `mpNodeMtx`. Simpler —
   avoids reimplementing the walker.

Either way, we're no longer blocked on the "decouple" problem. That's
solved.

### Echo-Link DONE — Link #2 drives its own pose (2026-04-19 late-late-late)

(Shadow modes 1-4 and `echo-delay` were deleted in Phase E0; mode 5 is the only
driven mode. The `echo_delay` / `echo_ring_state` mailbox fields stay as unused bytes,
but their C code and the CLI command are gone.)

Track 1 from docs/06 "Next Session Priority" complete: `shadow_mode = 4`
+ `echo-delay <N frames>` makes Link #2 animate cleanly `N/60` seconds
behind Link #1. 60-frame ring buffer in GameHeap holds `mpNodeMtx`
snapshots; each draw frame captures the current pose and replays a
delayed slot. Live verified on Outset at delay=30 (0.5 s).

### Working recipe (`shadow_mode = 4`, live verified: no stretch, no crash)

```c
// Inside daPy_draw_hook, after Link's real draw returns. Wrapped in a
// full 0x128-byte j3dSys snapshot/restore around BOTH calc() calls.
J3DModel_calc(mini_link_model);                   // 1st calc: real walker → mpNodeMtx + mpDrawMtxBuf = current pose
memcpy(ring[write_idx], mpNodeMtx, 42 * 48);      // capture current
memcpy(mpNodeMtx, ring[(write_idx - delay) % N], 42 * 48);  // replay delayed
*(basicMtxCalc_slot) = &noop_mtxcalc;             // swap walker to no-op
J3DModel_calc(mini_link_model);                   // 2nd calc: walker stubbed (mpNodeMtx unchanged)
                                                  // envelope/draw pass rebuilds mpDrawMtxBuf from delayed mpNodeMtx
*(basicMtxCalc_slot) = saved_real_mtxcalc;        // restore for Link #1
mDoExt_modelEntryDL(mini_link_model);
```

### Why a SECOND calc — the rubber-band diagnosis

Single-calc + post-calc `mpNodeMtx` overwrite alone was attempted first.
Result: **rubber-banding**. Rigid joint terminals (hands, feet, head,
belt-buckle, hat, sword, shield) tracked the delayed pose; skin-
enveloped vertices (torso, upper limbs) stayed on current pose; the
model stretched between the two. Cause: `J3DModel::calc` populates
**both** `mpNodeMtx` (+0x8C, per-joint world) AND `mpDrawMtxBuf` (+0x94,
per-envelope for skinning) in one call. A post-calc write to mpNodeMtx
doesn't propagate — envelopes already baked from current-pose mpNodeMtx.

The double-calc sidesteps reverse-engineering `mpDrawMtxBuf`'s layout
(entry-count is NOT joint-count; it's unique-envelope-combination count
indirected through shape packets). Instead, swap basicMtxCalc to a no-op
so the 2nd calc's walker does nothing, while its envelope/draw-matrix
pass re-reads our delayed mpNodeMtx and rebuilds mpDrawMtxBuf. ~1.5×
the per-frame cost of a single calc; no visible frame-rate drop.

### Addresses / offsets added this session

- `J3DJointTree + 0x18 = u16 mJointNum` (access as `J3DModelData + 0x28`).
  Link's mJointNum = **42** verified live via `mailbox.dbg_joint_num`.
- `J3DModel + 0x8C = Mtx* mpNodeMtx` — per-joint world matrices.
- `J3DModel + 0x94 = Mtx** mpDrawMtxBuf` — per-envelope draw matrices
  (computed by calc's envelope pass from mpNodeMtx).
- Stride: `Mtx = f32[3][4] = 48 bytes`. Ring per frame = 42 × 48 = 2016 B.
  60-frame ring ≈ 120 KB from GameHeap (~245 KB free on Outset).

## Pose Sync — DONE (2026-04-19 latest)

Track 2 of docs/06 "Next Session Priority" complete. Player A's Link
animations travel through TCP and render on Player B's Link #2 at
~50 ms lag. End-to-end: `daPy_lk_c::mpCLModel.mpNodeMtx` → wire →
mailbox.pose_buf → `J3DModel::calc` (no-op walker) → mpDrawMtxBuf →
GX submit. Verified live via single-Dolphin loopback (server +
broadcast-pose + puppet-sync on the same instance, Link #2 mirrored
Link #1 over TCP).

### Wire format

```
client -> server:  MsgPose='M' [joints:u16][_pad:u16][Mtx[joints]:48*N]
server -> client:  MsgPose='M' [playerID:1][joints:u16][_pad:u16][Mtx[joints]:48*N]
```

For Link: 4 + 2016 = 2020 B sender-side, +1 = 2021 B relayed. At 20 Hz
that's ~40 KB/s — trivial for LAN. Joint count is shipped explicitly so
the protocol survives non-Link rigs (children, fairies, etc.). Matrices
are raw GameCube big-endian Mtx layout (`f32[3][4]`), which means the
receiver does **zero byteswap** — `WriteAbsolute(pose_buf, matrices)`
goes straight into `mpNodeMtx`.

### Receiver recipe (`shadow_mode = 5`)

Same shape as mode 4's double-calc but with the pose source swapped from
the local echo ring to a Go-populated heap buffer:

```c
// Lazy-alloc on first mode-5 frame. Crucially, seed pose_buf with the
// current mpNodeMtx so the first frame's overwrite is identity — without
// this, copying uninitialized JKRHeap bytes into mpNodeMtx makes the
// second calc rebuild mpDrawMtxBuf from garbage matrices, killing
// Link #2 (degenerate vertices) AND the sky (TEV bucket pollution).
pose_buf = JKRHeap_alloc(joint_num * 48, 0x20, GameHeap);
memcpy(pose_buf, *(mini_link_model + 0x8C), joint_num * 48);  // seed
mailbox.pose_buf_ptr = (u32)pose_buf;

// Each subsequent draw frame, after the first calc:
memcpy(mpNodeMtx, pose_buf, joint_num * 48);     // Go's pose -> mpNodeMtx
*basicMtxCalc_slot = &noop_mtxcalc;              // disable walker
J3DModel_calc(mini_link_model);                  // 2nd calc rebuilds mpDrawMtxBuf
*basicMtxCalc_slot = saved_real_calc;
```

### Sender recipe (`daPy_lk_c + 0x032C → +0x8C`)

Per zeldaret/tww `include/d/actor/d_a_player_main.h`:
- `daPy_lk_c + 0x0328 = J3DModelData* mpCLModelData`
- `daPy_lk_c + 0x032C = J3DModel* mpCLModel`

Verified live by `getModelJointMtx(idx)` returning `mpCLModel->getAnmMtx(idx)`
— this is the skinned rig, not a side-model. Sender per tick:

```go
linkPtr := PLAYER_PTR_ARRAY[0]
modelPtr := *(linkPtr + 0x032C)              // J3DModel*
nodePtr := *(modelPtr + 0x8C)                // Mtx* mpNodeMtx
matrices := ReadAbsolute(nodePtr, 42*48)     // 2016 B raw
client.SendPose(42, matrices)
```

### The loopback overlap "bug"

Single-Dolphin loopback test (broadcaster + receiver on the same
instance) makes Link #2 appear to *not animate* — actually he's rendering
correctly but ON TOP of Link #1. Cause: `mpNodeMtx[0]` is the root joint
in WORLD space (`base_matrix × root_local`), so when we copy Link #1's
pose into Link #2's `mpNodeMtx`, Link #2 inherits Link #1's world
coords. The base matrix we set for Link #2 (`link_pos + 100`) is ignored
because the overwrite happens AFTER the first calc applied it.

Decisive proof: `pose-test freeze` — capture Link's pose once, walk
Link #1 away, a frozen Link #2 stays at the capture location. So mode 5
*is* rendering at the pose's encoded position; loopback just makes that
position coincide with Link #1.

For real multiplayer this is the CORRECT behavior — Player A's pose
encodes Player A's world coords, so Link #2 on Player B's screen appears
at Player A's actual location on the map.

### Diagnostic probes added this session

(Deleted in Phase E0; reserved bytes since E2.)

Mailbox bytes for verifying the C side actually sees Go's writes:
- `dbg_pose_first_word` (+0xB0): `*(u32*)pose_buf` published every
  mode-5 frame BEFORE the copy. Should track Go's writes.
- `dbg_node_mtx_first` (+0xB4): `mpNodeMtx[0]` published AFTER the
  second calc. Should match `dbg_pose_first_word` (i.e. calc didn't
  revert our overwrite). If they diverge, the second calc is doing
  more than just rebuilding mpDrawMtxBuf.

Both probes verified working: each tick `c-saw-pose == c-saw-node-after-calc`
== latest `go-wrote` value, with small drift from race timing.

### Mailbox bytes added

Mailbox grew from 0xA8 to 0xB8:
- `pose_buf_ptr` (+0xA8 u32) — C publishes after JKRHeap_alloc
- `pose_joint_count` (+0xAC u16) — Link = 42
- `pose_buf_state` (+0xAE u8) — 0 unalloc, 1 ready, 0xFE bad joint count, 0xFD alloc failed
- `pose_seq` (+0xAF u8) — Go bumps each write
- `dbg_pose_first_word` (+0xB0 u32), `dbg_node_mtx_first` (+0xB4 u32)

Still flush against `__OSArenaLo = 0x80412000` with 0x48 B headroom.

### ISO reserved-DOL space bumped

Mod grew to 0x12A8 — over the previous 0x3A6400 reserved DOL region by
~200 B. Instead of a fresh `wit copy` (4 GB regen), shifted the existing
ISO's FST from `0x3C5000` to `0x500000` in place. Now reserved DOL
space is `0x4E1400` (~5 MB), comfortable for several more sessions of
mod growth before another shift is needed.

(Historical: patch_iso.py was retired in Phase E4, and `ww-multiplayer patch`
now moves the FST past the new DOL end on every patch, `internal/inject/iso.go`.
The recipe below is kept as history.)

The shift is one-time and not in any source file — if regenerating the
ISO from scratch, the new patch_iso.py target is the same; the FST just
needs to live further out. Recipe:

```python
# Read FST data, write at new offset (must be all-zero before write),
# update ISO header at 0x424 with new fst_off, update fst_max at 0x42C.
```

### Mailbox + __OSArenaLo shift (mod grew to 0x1048)

Echo-ring code pushed `.text` end to `0x80410FC0` and mod end to
`0x80411048`. Two layout fixes applied:

- **Mailbox moved**: `0x80410F00` → `0x80411F00`. The old address fell
  inside `.text` once mod grew past 0xF00 — mailbox writes would have
  corrupted executing code.
- **__OSArenaLo bumped**: `0x80411000` → `0x80412000` (OSInit patch in
  build.py: `addi r3, r3, 0x1000` → `addi r3, r3, 0x2000`). Needed
  because mod end crossed 0x80411000 (ClearArena would have wiped
  .dtors + tail of .fini). 4 KB ZeldaHeap shrink — noise vs the 1 MB
  bump that broke Outset archive loads.

Must keep in sync across:
- `inject/include/mailbox_core.h` MAILBOX_ADDR
- `inject/src/core/hooks.c` CALLBACK_PTR_ADDR + `frame_shim` asm offset
- `inject/build.py` OSInit patch immediate
- `wwapi/mailbox/layout_gen.go` `Mailbox` (generated: `go generate ./wwapi/mailbox`)

### C++ exception metadata strip

Freighter's g++ emits `.eh_frame` (~0x158 B) + `.eh_frame_hdr` (~0x34 B)
even for plain C. Added `-fno-exceptions -fno-unwind-tables
-fno-asynchronous-unwind-tables` to `proj.common_args`. Saves ~0x18C B
per build — enough to keep mod under the ISO's reserved DOL space
(which otherwise requires a fresh `wit copy` instead of the fast
patch_iso.py splice; patch_iso.py was retired in Phase E4, and the Go
patcher now relocates the FST itself).

### Infrastructure landmarks established this session

- `dRes_control_c::getRes` is a **static** method, not a member function. The Itanium mangling (`F...` without `CCF` / `CFv`) plus the decomp's explicit `static` keyword confirm this. Earlier we typed it as a member with a `this` arg, which shifted every real argument by one register — the game treated `&mObjectInfo[0]` (which at offset 0 inside dRes_control_c has the 14-byte string `"System\0...\0"` of the first archive slot) as `arcName`, which is why broken getRes calls spammed `<System.arc> getRes: res nothing !!` at ~143 logs/sec. **Beware**: static members in the decomp headers are NOT clearly flagged in the mangled names; read the header declaration or the decomp source before building a function-pointer typedef.
- Draw-phase hooking recipe: `hook_branchlink` at `0x80108210` (the `bl` inside `daPy_Draw`). Our C function must call `daPy_lk_c::draw @ 0x80107308` via function pointer to preserve Link's rendering, then do our own work, then return Link's original result (`BOOL`/`int`) so `daPy_Draw`'s caller sees the right value. This is the generic shape for "run code inside the actor-draw iterator" and should work for any actor's draw we want to piggyback on.
- Mini-Link J3DModel lives in ArchiveHeap (switched via `mDoExt_getArchiveHeap @ 0x80011AB4` + `JKRHeap::becomeCurrentHeap @ 0x802B03F8`). Neither ZeldaHeap (`mDoExt_getZeldaHeap @ 0x800118C0`) nor GameHeap (`mDoExt_getGameHeap @ 0x800117E4`) are better — heap starvation isn't the failure mode.

## Mini-Link J3DModels leaked on every stage load — FIXED (2026-10-16, F18)

### The symptom

After about 20 stage loads the game halted on `JUT_ASSERT(buffer)` in
`JKRAramArchive.cpp:688` while loading a REL. On the owner's run the ArchiveHeap
(`0x80AD2590-0x81523900`) went from 807 KiB free in 223 blocks to 57 KiB in 1637
blocks. Counting J3DModels in the ArchiveHeap (vtable `0x8039EA50`) whose `+4`
(`mModelData`) equals Link's `mpCLModelData` gave 12, then 14, then 16 across stage
loads. It should stay 2 (`MAX_REMOTE_LINKS` then; 3 since WWMB_LAYOUT 3).

### The cause

`multiplayer_update` creates the mini-Link models with `mDoExt_J3DModel__create` with
the ArchiveHeap as the current heap. It treated `mpCLModelData == 0` (`daPy_lk_c +0x328`)
as "Link was rebuilt" and called `mini_link_reset_state()`, which nulled the model
pointers without freeing them. The `mini_link_state == 0` block then built two new
models in the same frame.

On every stage load the new Link actor is published in `phase_1`
(tww `d_a_player_main.cpp:12361`). Its memory is zeroed (`f_pc_base.cpp:98`), and
`playerInit` only sets `mpCLModelData` in `phase_2`, once the camera exists
(`:12374-12386`). So `+0x328` reads 0 for several frames, and every one of those frames
leaked two models. `getRes` still succeeded, because Link.arc stays resident
(`d_s_logo.cpp:876`). The draw hook also re-allocated the 2016-byte `pose_publish`
buffer from GameHeap after each reset.

The comments said the ArchiveHeap is "reset wholesale" on stage unload, so dropping
the pointers was safe. That is wrong: nothing in the decomp calls `freeAll` on the
Archive or Game heap. The 2026-04-21 note that the game tears down Link's
J3DModelData on stage unload is wrong too. `initModel` takes it from Link.arc
(`d_a_player_main.cpp:12059`), so it is the same pointer for every Link actor.

### The fix (`inject/src/mp/minilink.c`)

- `mpCLModelData == 0` means Link is not ready yet. `multiplayer_update` skips mini-Link
  work that frame, and the draw hook skips its submission. Neither resets anything.
- A new Link actor (`PLAYER_PTR_ARRAY[0]` changed) or a not-ready frame runs
  `mini_link_stage_reset()`. It unlatches the mode-2 `shadow_link` snapshot (a copy of
  the old actor, whose pointers point into its freed heap) and empties the mode-4 echo
  ring history. Allocations, Go-owned `pose_seqs` / `face_seqs` and the tev_block cache
  are kept.
- The models are rebuilt only when `mpCLModelData` is non-NULL, differs from ours, and
  equals what `getRes("Link", LINK_BDL_CL)` returns now, which means Link.arc really
  was re-mounted. That never happens while Link.arc stays resident. Any other mismatch,
  such as a stale actor read mid-teardown, is treated as "not ready". The old models are
  not freed: `J3DModel::~J3DModel` is empty (`J3DModel.cpp:26`), and the dozen-plus
  sub-allocations from `entryModelData` and the packet setup are only ever released
  when the game destroys a whole heap. The leak is bounded to one set per real
  re-mount.
- `pose_publish`, `pose_bufs`, `echo_ring` and `shadow_link` are allocated once per
  session and never dropped.
- Phase E0 note: shadow modes 2 and 4 and their per-stage reset were deleted, so
  `mini_link_stage_reset()`, `shadow_link` and the echo ring no longer exist. Only
  `pose_publish` and `pose_bufs` remain as once-per-session allocations.

T2 shrank from 0x2C88 to 0x2BE8 bytes (devkitPPC 16.2). It ends at `0x80412BE8`, which
is 0x318 bytes below the mailbox.

## Local Link's feet squished on slopes — FIXED (2026-10-18, F19)

### The symptom

On slopes the local player's *own* Link deformed: feet and lower legs squished or
bent, worse the steeper the ground, and nothing on flat ground. It needed no remote
player; a session with nobody else in it did it. The 2026-04-22 slope fix
(docs/06 "Slope-IK leg flap") removed torn reads on the *remote* puppet; this one was
in the player's own game.

### The cause

`mp_draw_post` (`inject/src/mp/minilink.c`) ran a first `J3DModel_calc` on every
mini-Link with Link's **real** walker before copying Go's pose over `mpNodeMtx` for
the no-op-walker calc. Every mini-Link shares Link's `J3DModelData`, whose joints
carry Link's callbacks, and the first calc set `mUserArea = Link #1` so they could run
(the 2026-04-19 crash fix above). Those callbacks do not write to the model being
calc'd. They write into Link #1 (zeldaret/tww `d_a_player_main.cpp`):

- `daPy_lk_c::jointCB1` (the `CL_JNT_RTOE` callback, `:488-545`) builds the leg IK
  rotation from `mFootData[].field_0x008` and `revConcat`s it onto Link #1's own
  `mpCLModel` leg matrices (`LLEGA..LTOE`, `RLEGA..RTOE`). In our calc that applies
  the IK rotation a second time, onto legs Link's own calc had already solved.
- `daPy_lk_c::jointAfterCB` (`:390-413`, the blend-calc after-callback, whose
  `mUserArea` is Link #1 directly via `m_pbCalc`) copies Link #1's leg matrices into
  `mFootData[].field_0x088`, the "animation-only, pre-IK" matrices that
  `footBgCheck` / `setLegAngle` solve from. After our calc they hold post-IK matrices.
- `mDoExt_MtxCalcAnmBlendTblOld::calc` decrements the old-frame morph counter once
  more per extra walk (`m_Do_ext.cpp:1227-1229`), so blend transitions ran faster.

Next frame `daPy_lk_c::execute` consumes that state before Link's own calc rewrites
it: `posMove` → `posMoveFromFootPos` (`:2352-2483`) reads the foot matrices for toe
and heel positions, and `footBgCheck` (`:8689-8830`) probes the ground under them,
lowers the body and runs `setLegAngle` on `field_0x088`, chasing the new angles into
`field_0x008`. The loop compounds every frame. `setLegAngle` returns early when the
ground delta is under 0.1 (`:8597`), which is why flat ground hid it. The walk ran for
every created model *before* the `pose_seqs` gate, so `shadow_mode = 5` alone, which
the feeder sets the moment a session connects, was enough. The remote's view of this
Link stayed clean because `pose_publish` copies `mpNodeMtx` before `mp_draw_post`
runs (`src/core/hooks.c`).

The handoff's first guess, `basicMtxCalc` left swapped to the no-op, was wrong: the
swap and restore around the second calc are straight-line with no exit between them.

### How it was verified (Outset pier ramp, patched ISO, no network)

With `wwdev peek`, Link #1's live `LFOOT` matrix (`mpCLModel + 0x8C`, joint `0x22`,
48 bytes) was compared with `pose_publish`'s copy of the same joint
(`mailbox.pose_publish_ptr + 0x22 * 48`), i.e. before and after `mp_draw_post`:

- `shadow_mode 0`: the two agree (idle-animation noise only).
- `wwdev shadow-mode 5`, no server, no pose written: the live matrix differs from the
  published one by a large rotation and sits about 7 units higher; the screenshot shows
  the left leg bent and the foot raised on the ramp.
- `wwdev shadow-mode 0`: they agree again and Link stands straight.

### The fix (`inject/src/mp/minilink.c`)

The first calc is gone. Go's pose fills every joint of `mpNodeMtx`, so the walk was
wasted work as well as harmful. The pose buffer is seeded, on its one-time
allocation, by copying Link #1's own `mpNodeMtx` (same rig, computed by Link's draw
this frame), a copy with no calc; nothing renders from the seed anyway, since both the
copy and the submit loop wait for `pose_seqs[slot] != 0`. The no-op-walker calc still
runs as before, and `mUserArea` is still set to Link #1 for the draw-time entry.

T2 shrank from 0x2A88 to 0x29C8 bytes (devkitPPC 16.2). It ends at `0x804129C8`,
0x538 bytes below the mailbox. The mailbox layout is unchanged (comment-only edit to
`mailbox_core.h`, `layout_gen.go` identical), so `WWMB_LAYOUT` stays 2; the blob hash
changed, so `saves/start.sav` was recaptured and players must re-patch.

## Remote faces were the local player's — FIXED (2026-10-18, F20)

### The symptom

Every remote Link blinked, frowned and moved its mouth exactly with the local Link, in
the app and in the CLI `host`/`join`. Issue #5's 2026-05-24 probe had already found the
mouth mirrored even under `wwdev mp-local` with diverged save states, and reframed the
issue as "face sync needs to actually transfer state".

### Two causes

**1. The arm never shipped.** The C bracket does nothing unless `face_hook_enable`
(mailbox +0x155) is set, and the only writers were `wwdev mp-local` (the session-13
auto-arm) and the `face-hook-arm` probe. The feeder that the app's Start and the CLI run
wrote `face_state` and `face_seqs` every tick and never the flag. The D1b live check's
"Face PASS: the remote's eyes move" was the mirroring, which looks the same from one
save state.

**2. The swap sat in the wrong window.** Where the texno is read (zeldaret/tww): on
Link's model data (type 1, never locked) `mDoExt_modelEntryDL` is `mDoExt_modelDiff`
plus `viewCalc`, i.e. `J3DModel::calcMaterial`, `diff`, `entry`, `viewCalc`
(`m_Do_ext.cpp:509-513, 563-576`). `calcMaterial` runs every material's
`J3DMaterialAnm`, which writes the **local** btp texno into the **shared** `J3DTevBlock`
(`J3DMaterialAnm.cpp:51-62`, `setTexNo`). `diff` then bakes each tev block into **this
model's** double-buffered differed display list (`J3DMaterial::diff` →
`J3DTevBlockPatched::diffTexNo` → `loadTexNo`, `J3DMatBlock.cpp:1126`), and nothing
reads the tev block at draw time: `J3DMatPacket::draw` only replays display lists
(`J3DPacket.cpp:220-240`). The bracket swapped the tev blocks *before*
`mDoExt_modelEntryDL`, so `calcMaterial` put the local face back a few instructions
before the bake. The pupils seemed to work under the 250 Hz hammer only because the
hammer also wrote the texture stage btp does not drive (`mat1_tex1`); the blink and
expression stage (`mat1_tex0`) was the local one all along, like the mouth.

Checked live before the fix, two Dolphins on the Outset pier ramp with both Links
facing the camera and the pipeline running: in frames where A's `face_state_local`
mouth was `0x17` and the slot's (B's) was `0x1D`, A's puppet showed A's closed mouth.
Feeding the slot `mat14 = 0xFFFF` left the puppet's mouth as A's; feeding
`mat1_tex1 = 0xFFFF` whited the puppet's pupils and did so at a constant value too, so
the "rate" theory in issue #5 was wrong: the window was.

### The fix

- `inject/src/mp/minilink.c`: `mini_link_entry_dl` runs the four steps itself
  (`J3DModel_calcMaterial`, `face_swap_for_slot`, `J3DModel_diff`, `face_restore`,
  `J3DModel_entry`, `J3DModel_viewCalc`; addresses in `wwapi.h` from
  `config/GZLE01/symbols.txt`), so the slot's face is what the bake reads. The eye-fix
  recipe's own `entryIn` calls bake nothing (the mini-Link's packets are locked), so
  they stay ahead of the swap.
- Go: `avatar.SetFaceSync`; the feeder arms it in `Connected` and in the shadow_mode
  re-assert after a save-state load; `avatar.Clear` disarms it and zeroes the face seqs;
  puppet-sync disarms on exit; `mp-local` and `face-hook-arm` use the helper.

### Verified after the fix (2026-10-18)

Same setup on the fixed blob, two Dolphins from a fresh recapture, pipeline running,
both Links facing the camera on the Outset pier ramp, a burst of screenshots each paired
with a read of A's `face_state_local` and `face_state[0]` taken before and after it:

- local mouth `0x17` (closed), slot `0x1D` (open): the puppet shows the open mouth;
- local eyes `0x00` (open), slot `0x09` (half-closed): the puppet shows the half-closed
  eyes while A's own Link keeps the open ones;
- local eyes `0x00` and mouth `0x19` (talking), slot eyes `0x07` (shut) and mouth
  `0x1F` (frown): the puppet shows shut eyes and the frown.

Screenshot pairs (before: `d4-*`, after: `f2-*`) are in
`ww-multiplayer-handoff\archive\f20-face\`. Both games ran on for the rest of the
session with no freeze, and the pupils, eye outline and brows still draw as before.

T2 grew from 0x29C8 to 0x2A88 bytes (devkitPPC 16.2). It ends at `0x80412A88`, 0x478
bytes below the mailbox. The mailbox layout is unchanged; the blob hash changed, so
`saves/start.sav` was recaptured and players must re-patch.

## Actor puppets outlived their slot and froze the next stage — FIXED (2026-10-17)

### The symptom

With the app, both games froze while loading the save's stage (Earth Temple, `M_Dai`) after
file select: a black screen, `spawn_trigger` stopped, and Dolphin reported "Invalid read from
0x1a0d0013, PC = 0x80f24230" (inside `d_a_kamome`'s REL). Not every run froze.

### The cause

The app starts the pose pipelines as soon as Dolphin runs the patched game, title screen
included, and the title stage (`sea_T`) has a Link (`mpPlayerPtr[0]` non-zero). A remote's
position arrives before its first pose, so the Feeder activated an actor-puppet slot for it;
300 frames later `multiplayer_update` spawned slot 0's KAMOME next to the title's Link
(actor `0x80A8B478` every run). The pose then took a link slot, Go cleared the slot, and the
C side only dropped its pid: nothing ever deleted the actor. That KAMOME took path 0 of its
spawn room (parameter 0, `dPath_GetRoomPath`, `d_a_kamome.cpp:1496`) and still ran during the
next stage load, reading the freed path through `mpPath` (+0x304, `kamome_path_move`,
`:482`).

Reproduced with the C side alone on origin/main's blob (`move-puppet 0 0 0 0` on the title,
then `poke-u32 80412F14 0`, then the menus into the save): the KAMOME stayed in the actor
list after the slot went inactive, and the load into `M_Dai` froze. A KAMOME left behind
on Outset (`stage-hop sea 0 44`) was gone after a `stage-hop` to `M_Dai` and the game ran
on, so an ordinary stage change can delete it; the title -> file select -> stage path
did not.

### The fix

- **C (`inject/src/mp/puppets.c`, `puppet_reap`).** A slot's actor is deleted
  (`fopAcM_delete` by id, 0x800244B8) when Go clears the slot, and every actor puppet is
  deleted when a stage change starts (`mNextStage.mEnable`, 0x803C9D54), when Link is gone
  (`mpPlayerPtr[0]` 0) or when Link's actor changes. A delete that can't happen yet (the
  actor is still being created, `fpcM_IsCreating`) is retried every frame; the slot is
  forgotten only once it went through. No actor is spawned while a stage change is pending,
  and phase 2 no longer forgets a pid that is still being created (that would have leaked the
  actor too). An active slot respawns its actor in the new stage.
- **Go (`mods/multiplayer/feed`, `actorSlot`).** Every new actor-puppet slot waits
  `actorGrace` (2 s) before its actor comes up. A remote that sends a usable pose and holds,
  or can still get, a link slot is drawn as a Link and never gets an actor; one beyond
  `avatar.Cap` or without a usable pose gets it when the wait ends. The wait counts only
  time this game spends in a play stage (`inPlay`: not the title screen or file select, Link
  loaded, no stage change pending), so a remote first seen on the title still waits 2 s after
  this game enters play. An actor that is up goes back down (active 0, so C deletes it) when
  this game leaves play, and comes back through the same wait. A reconnect restarts a pending
  wait. The mailbox contract for `Puppet.active` (`inject/include/mailbox_mp.h`) says the same.

Live (new blob, temp ISO, one Dolphin): the title KAMOME is deleted the moment its slot goes
inactive; with the slot left active, the menus into `M_Dai` load and the game runs on; the
same with the slot active across `stage-hop` Outset -> `M_Dai` -> Outset, and a deactivation
on Outset deletes the actor. Two app instances (host + join, Start on the title, both into
the save): no actor slot was activated, both games ran on in `M_Dai` with the other's Link.

Live, second round (app built from this change, `ui --browser` twice, Start launching both
Dolphins on the title, user folders 3 and 4):

- The original repro: the pipelines ran from the title, then the menus took both games into
  the save. No actor slot was activated on the title (the in-play check), no KAMOME or
  NPC_OB1 was in `actors` after the load, and each game drew the other's Link.
- The control on origin/main (`bc0226b`, same steps): slot 0's KAMOME (`0x80A8B478`) came up
  on the title 300 frames in, the slot went back to 0 when the pose took a link slot, the
  actor stayed, and both games froze loading `M_Dai` (frame counter stuck, black screen,
  Warning popup, the KAMOME still the only actor in the list).
- A position-only third remote (`broadcast-link`) added to the session: its NPC_OB1 puppet
  came up in both games 2 s after it appeared. Both games then did `stage-hop` `M_Dai` ->
  Outset -> `M_Dai` -> Outset. Right after every hop no puppet was in `actors`; the puppet came
  back 2 s into the new stage, and nothing froze. Stopping that remote released the slot and
  the actor was gone.
- A KAMOME forced into the unused slot 2 (`move-puppet`, `poke-u32 80412F54 1`) on Outset:
  it stayed put there, was gone right after a `stage-hop` to `M_Dai`, came back in the new
  stage and again on Outset, and was deleted the moment the slot went to 0.
- CLI `host` / `join` on the same two games: both Links drawn, red and blue.

Not changed: in `M_Dai` a KAMOME puppet is gone again a few frames after it is created (the
actor list shows it being deleted; why was not traced), so an active slot there respawns it
every few frames, as before. NPC_OB1 does the same on Outset and in `M_Dai`: it stays in its
create phase and is then deleted, about every half second, so it shows only now and then. T2 grew from 0x2E28 to 0x2EE8 bytes; 0x18 bytes are left below the mailbox
(docs/09 §6).

## A third remote froze both games — FIXED (cleared live 2026-10-25 on cea51fe, F1)

### The symptom

With two players in a session (two Dolphins, `mp-local`), a third remote freezes both
Dolphins: `spawn_trigger` stops changing and the screenshots of both games show the same
frame from then on. It does not matter how the third remote arrives:

- a position-only `fake-client` gets actor slot 1 (NPC_OB1); its actor resolves
  (`actor_ptr 0x80AAA8DC`) and then both games stop;
- a `pose-fake-loop` remote gets link slot 1 (the second mini-Link) and freezes them the same way.

Reproduced four times during D1b: twice with the `phaseD/d1b` exe and twice with origin/main
`6678cee` built in a temp dir, with blob `2f31005` and `start-2f31005.sav`. Both exes write the
same mailbox bytes, so this is not a D1b regression. Caveat: the Dolphins ran a fresh default
config (temp user dirs), not the owner's.

### Status

Fixed for the `pose-fake-loop` case, cleared live on `cea51fe` (PR #194; handoff
`archive/ww-online-review/live/LIVE-1.md`, V1). The cause was the mini-Link eye fix: it
entryImm'd the same four static packet copies once per drawn slot, so with two mini-Links each
packet entered `opa_p1` twice in one frame, the cycle the copies exist to avoid
(`inject/src/mp/minilink.c`, the comment above `MY_PACKET_SIZE`). The lead came from the
datstarkey/ww-online review (their rule: each puppet owns its packets). Each slot now owns its
own four copies. On that build `pose-fake-loop Fake3` into a two-Dolphin `mp-local` session drew
B and Fake3 together on A, and both heartbeats climbed for over 5 minutes through two stage hops
of both games; before it, the same run froze both.

The position-only `fake-client` variant (actor slot 1, NPC_OB1) passed on the same build too
(about 3.5 minutes through two hops). The eye packets cannot explain that variant, so its D1b
freeze was most likely the actor-puppet lifetime bug fixed in "Actor puppets outlived their
slot" above; one pass is all the evidence.

After the fix `MAX_REMOTE_LINKS` went to 3 (WWMB_LAYOUT 3, 2026-10-25): three remotes drawn as
mini-Links, four players in all. The layout change and what it cost are in "Three remote Links"
below. docs/TESTING.md "Three remotes and the soft reset" is the live check for a third mini-Link:
R3-1 passed in part on 0x154B9670 (B and two pose-fake remotes drawn, no freeze over a minute and a
stage hop; handoff `archive/ww-online-review/live/LIVE-2.md`).

Evidence (owner machine): logs in `C:\Users\4step\.claude\jobs\10f6fa34\tmp\pd\d1b\logs`
(`base-fake3.log`, `base-fake4.log`, `base-mplocal.log`, `fake3.log`, `fake4.log`), screenshots in
`...\d1b\shots` (`base-A-fake3.png`, `base-A-fake4.png`).

## Three remote Links (WWMB_LAYOUT 3, 2026-10-25)

`MAX_REMOTE_LINKS` and `MAILBOX_POSE_SLOT_CAP` are 3 (`inject/include/mailbox_mp.h`). That is a
layout revision, not a constant edit: `WWMB_LAYOUT` 2 -> 3, so Go's contract check refuses a
game patched by an older build, and every save state taken on one (`saves/start.sav` needs a
recapture).

### What moved

- **Mailbox** (still 0x1B0 bytes): the pose arrays fill +0xA8..+0xBF (the retired
  `_reserved2` went to them: `pose_joint_counts` +0xB4, `pose_buf_states` +0xBA, `pose_seqs`
  +0xBD); `face_state[3]` starts at +0x120 (the tail of `_reserved4`); the tunic block holds
  four images (`TUNIC_SLOTS`; slots 0-2 the remotes, `TUNIC_SLOT_LOCAL` 3 Link #1) at
  +0x188..+0x19F, `features` stays at +0x1A0, `tunic_state` is at +0x1A4 and `tunic_swaps` at
  +0x1A8.
- **Visuals block** (`wwvis.h`): 0xC0 -> 0xF0 bytes, +0x8D0..+0x9C0. Names, equip and models
  are per slot, so everything from the equip entries on moved. The wave-3 in-game batch then
  appended the second model table (mirror shield, Master Sword scabbard, katsura) for three
  slots at +0xF0, so the block is 0x114 bytes, +0x8D0..+0x9E4.
- **Eye packets**: three slots' copies are 0x150 bytes, more than T2 had free, so they moved
  out of T2's .bss into the mailbox slack (`MP_EYE_PACKETS_OFF`): first the top,
  +0xFB0..+0x1100; in the wave-3 in-game batch the projectile events block (`wwfx.h`) took the
  top (+0xE50..+0x1100) and the packets moved to +0xD00..+0xE50, right below it.
  T2 shrank by 0xE0 (0x2DE8 -> 0x2D08 bytes; 0x1F8 free below the mailbox, was 0x118).
  `CanaryArenaHigh` was then +0x9E4..+0xD00 (0x804138E4..0x80413C00). The peer boats block
  ("Peer boats" below) took +0x9E4..+0xA6C, so it is now +0xA6C..+0xD00 (0x8041396C..0x80413C00).
- **REL**: the keeper's swap state holds three body records (0x17C -> 0x194 bytes), so the
  profile's `mSize` (`WWREMOTE_ACTOR_SIZE`) went 0x600 -> 0x620 for the keeper and each lite
  body (ZeldaHeap). The WWRP block already had 4 slots. The third body's hits cannot be
  attributed ("Shared enemies", Limits).

### Heap per remote

- **GameHeap:** a 2,016-byte pose buffer (42 joints x 48) and one tunic image (the hero
  texture, 32 KiB at 256x256 CMPR, capped at 64 KiB) per slot. Both are allocated once per
  boot. The 48 MB build's GameHeap is 0x6CE800 ("48 MB MEM1" below).
- **ArchiveHeap:** one `J3DModel` per slot with private material display lists (F18's
  create), made for every slot when Link first exists, whether a remote is there or not. Not
  measured: F18's leak lost about 750 KiB of ArchiveHeap to some 14 extra models, so about
  54 KiB per model is an upper bound (fragmentation inflates it). Add the REL's five sword and
  shield models per slot (`visuals.c`), made on first draw. The 48 MB build's ArchiveHeap is
  0xE51400, 4 MiB more than vanilla. docs/TESTING.md "Three remotes" measures it with
  `wwdev heap-free`.
- **Measured (R3-2, 2026-10-27, main 3de6e6b, REL 0xE280C6F0):** A at the Windfall pier,
  `heap-free` with 1, 2 and 3 remotes: GameHeap free 4813, 4809 and 4809 KiB (largest 4768
  each time), ArchiveHeap free 7018 KiB (largest 6832) each time, the heap counters
  unchanged. The third slot costs nothing measurable here, because every slot's model and
  pose buffer is made at session start. After five sea -> M_NewD2 -> sea trips with three
  remotes: GameHeap 4813/4813, ArchiveHeap 7018/6832, all three slots live. The `solid`
  counter rose by one per trip (5 -> 10) and the other six stayed 0. The vanilla disc does the
  same (0 -> 5 over five trips, +1 per M_NewD2 load), so it is the game's own (TESTING BT-6).
  Handoff `archive/ww-online-review/live/LIVE-4.md`.
- **ZeldaHeap:** a lite body is 0x620 bytes. A full body (0x4C80, plus about 0xCCF00 resident
  in GameHeap) stays limited to `FullMax` 1 (`mods/enemysync/players.go`); the heap gate falls
  the others back to lite bodies.

### Players past four (design decision)

Three remotes is where we stop for now. Going past it needs, in order (handoff
`archive/ww-online-review/DESIGN-big-items.md` (c)):

1. **Relay scoping first.** Built in #197 (ca06f77, `mods/multiplayer/net/place.go`): the
   positions carry a place trailer, the server relays a pose only within the sender's stage,
   and the place gate hides a remote in another stage whatever the remote count. Before it a
   pose carried no stage, and with two or three remotes one in another stage was drawn at its
   raw coordinates in this one. Its live checks (docs/TESTING.md "Relay scoping", RS1-RS7) have
   not run.
2. **Another layout revision.** The pose arrays fill +0xA8..+0xBF exactly; a fourth slot
   needs them moved (and `face_state`, the tunic block, the visuals block and the eye packets
   grow again). `remote.c` pins `MAX_REMOTE_LINKS == 3`.
3. **ATTRIB's body field.** The hit-apply flags byte has no free bit, so its two ATTRIB bits
   are one field: 0x08 body 0, 0x18 body 1, 0x10 body 2 (`WWREMOTE_HAF_ATTRIB_MASK`,
   `wwremote.h`; `mailbox.RemoteHAFAttrib`). It names three bodies and no more (`remote.c`
   asserts `MAX_REMOTE_LINKS <= 3`): a fourth needs a byte with room (the PHIT / hit-apply
   record).
4. **Co-op for three or more** is a separate protocol change (PRESENCE names no sender, one
   partner per replica, a two-player warp).

Until then the fifth player and up still fall back to the actor puppets (`MAX_PUPPETS` 4), as
players 3 and 4 did before.

## Soft reset (2026-10-25)

The game has two kinds of reset, and only one reboots (tww decomp):

- **Back to the title, no reboot (the usual one).** A reset press (`mDoRst_resetCallBack`,
  `m_Do_Reset.cpp:86-119`; Dolphin's Reset takes this same title path as the controller
  reset: checked live as SR-1b on 2026-10-27, mReset 1 for about 2 s, no `OSReboot`, the pose
  buffers at the same addresses at the title; handoff
  `archive/ww-online-review/live/LIVE-4.md`), a game over's or the save menu's quit
  (`d_gameover.cpp:116,132`, `d_menu_save.cpp:1269`) sets `mDoRst`'s `mReset`. Play is torn
  down to the opening scene (`dComIfG_resetToOpening`, `d_com_inf_game.cpp:510-521`), whose
  `phase_00` clears the flag (`d_s_play.cpp:1197-1213`, `:1444`). No DOL reload: T2's statics,
  the mailbox and every block the mod holds in GameHeap stay as they are, as across a stage
  hop. Nothing new is needed; the mini-Link path already rebuilds when Link's model data comes
  back (`mini_link_reset_state`, `inject/src/mp/minilink.c`). One thing is needed: the
  opening scene's heap check (below) has to pass, see "The title's heap check" at the end of
  this section.
- **A reboot.** `mDoRst_reset` -> `OSResetSystem` restart (reset 0 is `OS_RESET_RESTART`, which
  calls `__OSReboot`, `OSReset.c:171-174`) reloads the DOL and boots again through `__start`
  -> `main` -> `main01` (`m_Do_main.cpp:599`), which builds new heaps in `mDoMch_Create`
  (`:407`). It runs when a reset reaches the logo scene (`d_s_logo.cpp:501-503`), when the
  opening scene's heap check fails (`d_s_play.cpp:1197-1198`: any heap under 70% free or
  fragmented), on a DVD error (`f_pc_manager.cpp:250-256`) and on a reset press with the disc
  cover open (`m_Do_Reset.cpp:111`). RAM is not cleared. So:

- T2's statics come back zero (its .bss is part of the DOL image: `mini_link_models`,
  `pose_bufs` and `my_packets_initialized` sit inside T2's 0x80410000 section, build map).
- The mailbox and the slack past it keep the old boot's bytes. Until 2026-10-25 that broke
  three C-published GameHeap pointers: `pose_publish_ptr` (state 1, so `pose_publish` copied
  Link's 2 KB pose into it every frame), `tunic_bufs` (state 1, so `tunic_alloc` never ran
  again and Go wrote images into them) and `pose_buf_ptrs` (state 1 until T2 republished).
  After a reset they named blocks of the new heap that belonged to someone else. The REL's
  own long-lived pointers (`WWREL_SHANM_OFF`, the visuals block's models and function words,
  the exec hook) were already zeroed by `main01_init`.
- **Fix:** `main01_init` also zeroes the pose arrays and `pose_publish_*` (+0xA8..+0xC7), the
  tunic block up to `features`, and `tunic_state` (`inject/src/core/hooks.c`). It is main01's
  first instruction (0x80006338, `inject/build.py`), before `mDoMch_Create`, so the words are
  zero before any block of the new heap exists. Go re-reads the states every tick (the pose
  feeder's `armPoseSlot`, `Tunics.Tick`, the pose publish read), so it disarms and follows the
  new blocks (`TestTunicSync_SoftReset`); a Link that comes back at another address makes
  `Tunics` recalibrate within `tunicVerifyEvery` (`TestTunicSync_SoftResetMovesLink`).
- **The reboot window: Go's reset gate.** From the reset until `main01_init` the states still
  read 1, so Go would keep writing poses (2,016 B per slot per tick) and tunic images through
  the old boot's pointers while the reboot loads the apploader and the DOL. Where those land
  is not known (pose buffers on record sat at 0x816B31E0..0x81BF7140, above the apploader's
  0x81200000, but nothing pins that). So the pose feeder stops every pose, face and tunic
  write while any of these holds (`mods/multiplayer/feed/running.go`, `TestFeederResetGate`):
  - `mReset` is set: `mDoRst::mResetData` (0x803F6968, `symbols.txt`; `getResetData` in
    `original.dol` is `lwz r3,-0x7778(r13)`, r13 0x803FE0E0) points at the flag block, `mReset`
    at +0 (`m_Do_Reset.h:12`). It goes 1 when the reset is asked for, frames before the
    reboot, and stays 1 into the next boot (a restart keeps the block, `m_Do_Reset.cpp:74`,
    `m_Do_main.cpp:569-574`) until the opening scene clears it;
  - the pointer is 0 or outside MEM1: the new boot before `main` has set it (its .sbss is
    cleared);
  - `spawn_trigger` has stood still for 100 ms after it was seen moving: the frames have
    stopped (a reboot that skipped the flag, the disc-cover path, or emulation paused).

  It logs `pose feed paused: ...` once (a stall only after 1 s, so a hitch logs nothing) and
  `pose feed resumed` when the game runs again. The
  frames of the reboot itself before the flag is set (the disc-cover path) are covered only
  after those 100 ms.
- **Why not ww-online's `__OSStartTime` stamp** (`GameMod/CLAUDE.md:68`, MIT): their block
  pointers sit in scratch words, their REL allocates late (after the heaps exist) and their
  C# writes on its own clock, so they need a per-boot stamp (`__OSStartTime`, 0x803F79B8,
  `symbols.txt`; set once per boot by `OSInit`, `OS.c:230`) to tell a live block from an old
  boot's. Ours are zeroed by T2 code that runs every boot before any heap exists, so the zero
  is the stamp, and the reset gate covers the writes a stamp could not (before `OSInit`).
- The armed-slot poll no longer blocks out of play (the title screen after a reset), and one
  `Feed` waits through at most one timed-out poll whatever the slot count
  (`TestFeederArmPollBounded`; before, up to 3 s per unarmed slot, 9 s with three).
- `puppets[].actor_ptr` is left as it was: only `wwdev` reads it, and `puppets.c`'s own state
  is T2 statics.
- **The title's heap check (2026-10-27).** Live, every reset on the patched disc rebooted,
  with or without `mp-local`, while vanilla went back to the title. The opening scene's
  `heapSizeCheck` (`d_s_play.cpp:1143-1177`) failed on GameHeap contiguity: right after the
  reset freed the stage, the largest free block was 4516 KiB of 6658 KiB free (0.678; 0.682 in
  a session), under its 0.7. T2's own GameHeap blocks came from the head of the heap, among the
  stage's: `pose_publish`'s 2 KB buffer (allocated on the first in-game frame, session or not)
  and, in a session, `pose_bufs` and `tunic_bufs`. Once the stage was freed they sat mid-heap
  and split it. The archive and Zelda heaps passed, so the REL's models on the archive heap
  are not a factor. **Fix:** the three allocations pass alignment -0x20, which JKRExpHeap takes
  from the tail (`JKRExpHeap.cpp:110-115`). Live after the fix: 6658/6658 (1.00) without
  `mp-local` and 6622/6622 with it, no reboot, the title, and after the file loaded both
  mini-Links back and 5 minutes with no heap errors; a second reset straight after also went
  to the title. So the first reset no longer reboots, and the reboot path is reached only from
  a cold boot's logo (or the disc-cover and DVD-error paths).
- SR-1 with a third remote and SR-1b (Dolphin's Reset) passed on 2026-10-27 (main 3de6e6b,
  REL 0xE280C6F0; handoff `archive/ww-online-review/live/LIVE-4.md`). Not run live: SR-2 (a
  reset at a cold boot's logo; the rig cannot land one).

## Shared enemies

Enemy sync (`mods/enemysync`) and the remote players' bodies are on by default, with no env
vars: the host's Moblins, Bokoblins, ChuChus and Keese (`DefaultMirrorKinds`, `mo2,bk+u,cc+u,ki+u`) are
the real ones, every other game mirrors them, and remote bodies run at step6 with
`DefaultPolicy`, which arms only those kinds' procs. Each start logs what it runs:
`enemy host: host-authoritative enemies on (kinds ...), remote Link bodies step6 ...` and
`enemy client: mirroring the host's enemies (kinds ...), remote bodies step6 ...`.

### Turning it off

- `WW_ENEMY_SYNC=0` turns the whole enemy layer off: no enemy connections, and co-op's kill
  sync claims nothing for the mirror.
- `WW_ENEMY_REMOTE=off` (or `none`, `false`, `0`) keeps the mirrored enemies and drops the
  bodies.
- Both players set the same value: the two sides do not negotiate it.

### Limits

- Other enemy types (Armos Knights, Armos, Darknuts, Kargarocs, Magtails and the rest) still run
  separately on each player's game, and they ignore the other players' bodies.
- Only the first three other players get bodies (`MAX_REMOTE_LINKS` 3 since WWMB_LAYOUT 3; 2
  before). With ATTRIB on, each one's hits are credited to its own body: the hit-apply flags'
  two ATTRIB bits name body 0, 1 or 2 (0x08, 0x18, 0x10; `AttribFlags`,
  `mods/enemysync/players.go`; `remote_attrib_body`, `inject/rel/remote.c`). The third
  body's code is new with this build and has not run live (docs/TESTING.md R3-3).
  `FullMax` stays 1, so at most one of the three is a full `daPy_lk_c` body; the rest are lite.
- Each player mirrors at most 4 enemies at once (the block's 4 slots); the host lists by
  player distance.
- Shared enemies have not been tested over the internet. Nearly every live pass on record ran
  under `wwdev mp-local` (one process, two Dolphins); the one exception is a CLI `host` and
  `join 127.0.0.1` loopback on one machine before v0.5.0. docs/TESTING.md "Release defaults"
  is the checklist that covers the app, two machines and WAN.

### When it fails

The enemy pipeline runs isolated from pose sync (engine `Degradable`). A Dolphin or network
error is retried after 2 s, doubling up to 30 s, with one Warn per reason. A fatal error (a
refusal such as `RejectRole`, `ErrFatal`) or a panic (in setup, or in the tick loop, which
`network.RunEnemy` recovers as a fatal `SessionPanicError`) logs once:

```
enemy sync off for this session: <reason>; multiplayer is unaffected
```

Pose sync, co-op and the lobby keep running. "For this session" means until the pipelines next
start (a pose failure or a Dolphin change), which gives enemies a fresh try. A host that
reconnects under the same name takes the enemy-host slot over from its stale connection
instead of being refused. While the host's REL module is not linked (or is another build),
host hits wait and the host logs one Warn naming the reason, then one OK line once it is.

## Shared shape hide flags: mini-Link's head under casual clothes

mini-Link is built from Link #1's own `J3DModelData`, so the two
models share every `J3DShape` hide flag, and `J3DJoint::entryIn`
drops a hidden shape at entry time (`J3DJoint.cpp:235`). Whatever
Link #1 leaves hidden when `daPy_lk_c::draw` returns is missing from
mini-Link too, since `daPy_draw_hook` enters mini-Link right after.

The case that showed (issue #43): in casual clothes `drawShadow`, the
last step of `draw` (`d_a_player_main.cpp:2024`), hides link_root
material 4, the hat `"ear(3)"` (`:1566-1573`), after the body restore
at `:1888-1892`. The hat stays hidden until the next frame's draw
shows it (`:1749-1755`). The CL head has no back without the hat;
Link covers it with the separate `mpKatsuraModel` hair (`:1897-1903`),
which mini-Link does not draw. So mini-Link's head was open at the
back whenever the *local* Link wore casual clothes, whatever the
remote wore.

Fix: `mini_link_entry_dl` (`inject/src/mp/minilink.c`) shows every hidden
link_root material around mini-Link's `mDoExt_modelEntryDL` and hides
them again right after, so Link's real shadow, drawn later in the
frame, still sees its hat hidden. Only link_root: other joints carry
state Link sets on purpose and mini-Link keeps mirroring it, for
example the scabbard `CL_PODA`, hidden with no sword (`:1869-1879`),
and the `CL_LHANDA`/`CL_RHANDA` hands, which stay hidden when
`drawShadow` is skipped on the boat (`:1746-1747`, `:2023`).

Known gaps: a casual mini-Link showed the hat in the casual texture
(`linktexS3TC` is swapped in the shared model data,
`d_a_player_main.cpp:12340-12357`), not the katsura hair model
(`mpKatsuraModel`, `:1897-1903`). Since the visuals block's second model
table (gems3, live check V9 in docs/TESTING.md) the REL draws a katsura
per slot at the head joint for a remote whose appearance byte says
casual, and hides that slot's hat and belt buckle the way
`hideHatAndBackle` does (`:1510-1531`). The hide does not use the shared
shape bit this fix shows again: it sets the `J3DShapePacket` Hidden bit
(0x10) of the mini-Link's own packets, per model, so Link #1 and the
other slot never see it (`inject/rel/visuals.c`). The hide is only set
when the katsura will be drawn (its model made, the node matrices
there), and `wwvis_unlink` clears every hide it set, so a slot is never
bald while the REL is unlinked or its hair failed to load. The remote's own
clothes are drawn since 2026-10-16: the pose trailer's appearance byte
carries the remote's casual bit and `tunic_swap` puts the casual header
in for that slot (`TUNIC_ENABLE_CASUAL`, docs/09 §5.6, live-checked in
four viewer/remote cases). The note here that it "follows the local
outfit" predates that and was out of date.

T2 grew by 0x100 to 0x2CE8 bytes (devkitPPC 16.2). It ended at
`0x80412CE8`, 0x218 bytes below the mailbox at the time. The tunic
swap (docs/09 §6) later grew T2 to 0x2E08, leaving 0xF8.

## Peer boats — BUILT, BT-1..BT-3 and BT-5..BT-9 passed, some in part (2026-10-25, ww-online review)

The other players' King of Red Lions is drawn in your game: under a remote
that sails it, and where a remote left it (on the sea, and in Tower of the
Gods). The sail is in the remote's tunic colour, and the cannon or the
salvage crane shows when the remote has it out. It shipped in the wave-3
in-game batch (PR #199). On its REL 0xE95241C5 BT-1..BT-3 passed in part (a
sailing remote seated with its sail in its colour, a parked boat drawn where
it was left; handoff `archive/ww-online-review/live/LIVE-2.md`). On main
3de6e6b (REL 0xE280C6F0) BT-5 and BT-6 passed and BT-7, BT-8 and BT-9 passed in part
(`archive/ww-online-review/live/LIVE-4.md`). BT-4 and BT-10 (docs/TESTING.md "Peer boats")
are owed. BT-4 is open: in Tower of the Gods a remote's parked ship reads about 460 units
below the water (y about -2110 against -1650), so A draws it out of sight; whether the game
itself lowers the parked ship there or boatsync reads the wrong field is not known yet.
ww-online draws the same (`GameMod/src/puppet_link/puppet_boat.c`, MIT);
`inject/rel/boats.c` is adapted from it and carries their notice.

**The hard rule (theirs, and ours).** Never create a second `daShip_c`:
its create calls `dComIfGp_setShipActor(this)` (`d_a_ship.cpp:4746`),
which takes over `mpPlayerPtr[2]` (0x803CA75C, `addr.PlayerPtr + 8`), its
execute writes this player's sail status and audio, and it spawns its own
Grid, WindArrow and encounter actors. A boat is models the REL keeper
makes, poses and draws, never an actor.

**Sender (Go, `mods/multiplayer/boatsync`, broadcast-pose).** `Local` reads
this game's ship every tick: `mpPlayerPtr[2]`, in MEM1, with process name
`fpcNm_SHIP_e` (0xA7), no stage change under way. Fields are from the
decomp's `d_a_ship.h`. `d_a_ship.rel` is not in main.dol, so only the two
fields ho_move reads through `l_ship` (+0x358 at 0x800E9F80, +0x364 at
0x800E9CF8) are checked in `inject/original.dol`. The fields:
`current.pos` (+0x1F8), the room (+0x20A), `shape_angle` (+0x20C),
`mpBodyAnm`/`mpHeadAnm` (+0x298/+0x29C, their frame at morf +0x68), `mPart`
(+0x34E), `mStateFlag` (+0x358, SAIL_ON 0x200), `mSailAngle` (+0x364), the
tiller `m0366`, the mast bck `m0392`, the cannon `m0394`/`m0396`, the crane
`m0398 + m039C`, the head bck `m03B4` and the mast scale `m03E8`. Aboard is
`daPyStts0_SHIP_RIDE_e` (0x10000) in `mPlayerStatus[0][0]` (0x803CA8D0).
The ship's position is sent relative to Link's `current.pos` while aboard
and absolute while parked. It is a 40-byte run appended to the pose trailer
after the equip bytes (offset 19), with the stage name, so a receiver knows
where a parked boat lies. The trailer is then 59 bytes, under
`network.MaxFaceBytes` (64), so there is no protocol bump. The run is sent
(all zero with no ship) whenever the equip bytes are.

**Receiver (Go, puppet-sync).** `boatsync.Tracker` builds the boats block
each tick.
- An aboard boat goes at the remote's mini-Link (the feeder's smoothed
  `RemoteView.Pos`) plus the streamed offset, so Link and hull move as one
  with no smoothing of their own. It shows while the remote's pose is live
  and on this game's stage.
- A parked boat is held where it was left, on its stage, until the remote
  boards again, parks elsewhere or leaves the session. Its poses stop when
  it walks into another stage, and the boat stays.
- On the sea a boat shows in any room (the rooms are grid squares);
  elsewhere only in the room this game stands in.
- At most three boats, in player id order.
- `WW_BOATS=off` draws none; `WW_BOATS=nosail` or `noparts` drops the sail
  cloth or the cannon and crane. The default is on: the request asked for
  an off switch, not an opt-in, which changes this section's earlier plan
  (off until checked). `WW_BOATS=off` is the fallback while BT-4, BT-7 and BT-10 are open.

**The keeper's double draw froze both games (fixed, a0605c5).** On the batch's
first build both games froze on the first frame a peer boat was drawn. The
keeper is created from T2's frame hook and lands in the root layer, so its
draw ran twice a frame (the root layer's draw walk and the play scene's
draw queue). The second `mDoExt_modelEntryDL` of the hull and head put
their packets into bucket 0 again and closed its chain into a loop, so
`drawHead` never returned and the GX FIFO filled. The keeper's draw now
runs `boats_draw` at most once per heartbeat (`wwrel_draw`,
`inject/rel/rel_main.c`). A model drawn from the keeper must be entered
once per frame.

**Block (`inject/include/wwboat.h`, `wwapi/mailbox/boat.go`).**
`MAILBOX_ADDR + 0x9E4` (0x804138E4), 0x88 bytes, right after the visuals
block: magic `WWBT` and flags (Go), a drawn counter and a last-error word
(REL, 0x804138EC and 0x804138F0), then three 0x28-byte entries (Go).
`CanaryArenaHigh` now starts at its end, +0xA6C (0x8041396C, 165 words up to
the eye packets), and `canaryForbidden` lists `mailbox.BoatRegion`.

**REL (`inject/rel/boats.c`).** It runs from the keeper: `boats_frame`
after `fx_frame` in execute, `boats_draw` from the keeper's draw (its
profile has no `fopAcStts_CULL_e`, so it runs every frame outside the
menus), and the heaps are freed in the keeper's delete and in `_epilog`.
- Models exist only while this game's "Ship" archive is resident (the sea
  and the stages with a Ship actor), checked every frame. There is no
  `dComIfG_resLoad` of our own: a parked boat is only ever on such a stage.
- Each entry has one solid heap from GameHeap
  (`mDoExt_createSolidHeapFromGame`, 0x20000, then adjusted). It is
  destroyed when the entry empties, the archive goes or changes, a stage
  change starts, the magic drops, the keeper is deleted or the module
  unlinks. Never the archive heap: Ship.arc unmounts on every
  sea -> island -> sea trip, so keep-forever models would leak once per
  trip (F18).
- The hull (`fn_body`, Ship 0x11) and the head (`fn_head_h`, 0x12) are
  `mDoExt_McaMorf`s made as `daShip_c::createHeap` makes them. The DOL
  confirms new(0xB4) and the ctor's complete-object flag in r4
  (0x800E639C..0x800E63F0, 0x800126A0). The hull has its own
  `J3DSkinDeform`. The mast bck (`FN_MAST_ON2`/`OFF2`) and the head bck play
  at the remote's streamed frames.
- The base matrix is `T(pos) * ZXYrot(shape_angle)` from the remote's own
  values, not this game's sea: the mini-Link stands at the remote's
  streamed height, so a hull on our waves would float it or sink it.
- Around every calc, each joint's callback becomes ours (or none) and each
  `mMtxCalc` becomes 0; both are put back after (`bt_calc`). daShip_c's
  callbacks on the shared joints read `getUserArea()`, which is 0 for ours
  and unchecked in the head, cannon and crane callbacks, and a morf's calc
  leaves itself in joint 0's `mMtxCalc`. Ours repeat `bodyJointCallBack`
  (tiller, sail yard, the parts' bases on the mast, the 0.001 mast scale
  with a part out), `cannonJointCallBack` and `craneJointCallBack`.
- The sail is a zeroed `daGrid_c` (0x221C bytes) in the heap, with
  `daHo_packet_c`'s ctor fields and `_create`'s amplitude table (computed
  from the DOL's `l_pos`, 0x8038B1D8). The game's `ho_move` (0x800E9C0C)
  moves it, with `l_ship` (0x803F6EC0 in `.sbss`: r13 0x803FE0E0 - 0x7220)
  pointed for that call at a stub holding the remote's mStateFlag and
  mSailAngle. The game's `daHo_packet_c::draw` draws it through our vtable
  (the game's {0, 0, isSame, entry, draw, dtor} at 0x8038BBE0, with our
  draw).
- The tunic colour is a recoloured copy of `new_ho1`'s C8 palette (the
  canvas only; the teal emblem and the orange corners stay),
  double-buffered. The header's palette offset points at it only around our
  packet's draw.
- Each boat is lit with its own `dKy_tevstr_c` for the room this game
  stands in (`settingTevStruct(TEV_TYPE_ACTOR)`) and entered in the P1
  lists, as `daShip_c::draw` does.

**Left out, and why.**
- The head's look angles (`headJointCallBack1`) and its talk mouth:
  trailer bytes and code for little.
- The hull recolour: not asked.
- The crane's rope and grappling hook: `setRopePos`'s per-segment physics
  and a 3D line packet. The arm alone is drawn.
- The wake, the shadow, the hull's water-effect texture matrix (shared
  material state the local ship sets) and any collision.
- Loading Ship.arc ourselves for a stage without a ship: no parked boat
  needs it.

**Budget.**
- REL `.text` 0x15354 -> 0x16F64 (+0x1C10).
- RELS.arc growth +53,536 -> +58,304 of the 65,536 guard (the patcher
  golden fixture).
- No T2 code: T2 still ends at 0x80412D08, 0x1F8 free.
- 0x88 bytes of mailbox slack.
- At run time, per boat: one GameHeap solid heap with the models and the
  0x2640-byte sail block.

## Dolphin caches INI files at startup

Edits to `GZLE01.ini` in `User/Config/GameSettings/` are only read once at Dolphin startup. Closing and reopening the GAME does not reload the INI — the USER has to fully quit and restart Dolphin. Wasted multiple sessions on this.

## CISO Block Boundaries

Wind Waker's DOL spans 2 blocks in the CISO. A naive "just write the DOL into the file" patcher corrupts the second block. `ww-multiplayer patch` handles this: it decodes the CISO block table itself (`internal/inject/ciso.go`) and writes a plain ISO, so `wit` is no longer needed. The warning stands only for hand-rolled patchers.

## BSS Zeroing Overlaps with T2 — BENIGN, see above

Historically the advice was that T2 at `0x803FD000` overlaps the tail of BSS zeroing. Not quite true — the BSS init (via `_bss_init_info` in the DOL's own data) only zeros the declared ranges. The real zeroing culprit was `ClearArena`, not BSS init. That's now handled via the OSInit patch.

**Correction (2026-10-14):** `ClearArena` explains the zeroing above `__ArenaLo`, not at
`0x803FD000`, which is below `__ArenaLo` and inside the main stack. There the likely cause is
`OSClearStack`; see "Dolphin's DOL Loader Refuses Some Addresses" above.

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
bl +0x1BA88   # -> 0x8003EC84 fpcM_Management(0, fapGm_After): the whole frame
li r3, 0
bl +0x2217A8  # -> 0x802449AC cCt_Counter(0): frame counters
lwz/mtlr/addi/blr               # epilogue
```

(tww `src/f_ap/f_ap_game.cpp:77-81`; `fpcM_Management` is sym:1154 and `cCt_Counter` is
sym:8240 in `config/GZLE01/symbols.txt`.)

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

**Correction (2026-10-14, #23): `0x802449AC` is `cCt_Counter`, not heap bookkeeping.**
The decomp has it as a 0x48-byte leaf that bumps the two global frame counters in
`g_Counter` and calls nothing (tww `src/SSystem/SComponent/c_counter.cpp:11-20`, a matching
object). It never reads LR, so the "does `mflr` for heap bookkeeping" explanation above is
wrong. The observations still stand: dropping the call, and the `bctrl` replay, both tripped
the assertion, which comes from `mDoExt_restoreCurrentHeap` (tww
`src/m_Do/m_Do_ext.cpp:1108-1116`; 2755 is the USA line number). Neither failure was
root-caused. The tail call is still the right shape, because it does exactly what the
original `bl` did: `cCt_Counter` runs every frame, right after our `multiplayer_update`,
and returns straight to `0x80023208`.

Final shim shape (see `frame_shim` in `inject/src/core/hooks.c`):

```asm
frame_shim:
    mflr  0                # save caller LR (= 0x80023208)
    stwu  1, -0x20(1)
    stw   0, 0x10(1)
    ; call multiplayer_update via callback pointer (bctrl clobbers LR, that's
    ; ok — we saved it and restore it below)
    lis   12, 0x8041
    lwz   12, 0x2F08(12)       # callback pointer at mailbox+0x08 = 0x80412F08
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
`0x80412F08` (`CALLBACK_PTR_ADDR`, mailbox+0x08) once at thread start; `frame_shim` reads
it each frame. The listing above matches `frame_shim` in `inject/src/core/hooks.c`
today. When this section was first written the slot was at `0x80410700`
(`lwz 12, 0x0700(12)`); it has moved several times since and now lives in the mailbox, and
the hard-coded `lwz` offset has to move with it.

### Verified

Mailbox counter (`spawn_trigger`, then at `0x80410800`, now at `0x80412F00`) increments
~30/sec (matching 29.97 FPS) while the game runs normally.

## 48 MB MEM1 (2026-10-18)

Stephen accepted an emulator-only requirement: the patched disc runs with 48 MB of
MEM1 instead of the GameCube's 24 MB, the way ww-online does (its
`use_extra_memory.asm`, MIT, "based on LagoLunatic's use_extra_dolphin_memory.asm").
This buys the heap room a real remote Link actor needs (a second `daPy_lk_c` is
about 704 KB against ~245 KB free in the stock GameHeap, "PROC_PLAYER Won't Fit"
below) and the room the host-authoritative work will take.

### What the patcher writes

- `bi2.bin` + 0x04 (ISO offset 0x444): `0x03000000`. The apploader copies it to
  `0x800000F0`, `OSGetConsoleSimulatedMemSize` reads it there
  (`__OSInitMemoryProtection` picks the 48 MB BAT setup), and Dolphin 2609+ sizes
  MEM1 from it (`VolumeDisc::GetSimulatedMemorySize`).
- Four instruction sites in `mDoMch_Create` (`m_Do_machine.cpp:500-612`), from
  `inject/build.py`'s patch list so `extract_blob.py` carries them into `blob.go`:

| DOL addr | vanilla | patched | meaning |
|---|---|---|---|
| 0x8000C7A4 | `lis r0,0x8180` | `lis r0,0x8300` | arena-hi trim threshold |
| 0x8000C844 | `addis r3,r31,-0xD2; addi r0,r3,-0xC00` | `addis r3,r31,-0x152; addi r0,r3,-0xC00` | arena -= 0x1520C00 (was 0xD20C00) |
| 0x8000C9DC | `lis r3,0xA5; addi r3,r3,0x1400` | `lis r3,0xE5; addi r3,r3,0x1400` | archive heap 0xE51400 (+4 MiB) |
| 0x8000C9EC | `lis r3,0x2D; addi r3,r3,-0x1800` | `lis r3,0x6D; addi r3,r3,-0x1800` | game heap 0x6CE800 (+4 MiB) |

The command heap (0x8000C9D0, `li r3,0x1000`) is unchanged. The system heap keeps
its vanilla size: the game itself subtracts 16 MiB when the simulated size is 48 MB
(`0x8000C7DC-0x8000C7E8`, `m_Do_machine.cpp:526-528`), and the 8 MiB the two heaps
grew is taken out of the arena subtract. Nopping that 16 MiB subtract (file 0x09728
`3FFFFF00` -> `60000000`) would hand it to the system heap; untested by anyone, left
for a measured experiment with `wwdev heap-free`.

### Dolphin

`launch.LaunchArgs` adds `-C Dolphin.Core.RAMOverrideEnable=True -C
Dolphin.Core.MEM1Size=0x03000000` to every launch (session-only, nothing written to
the user's INI). Dolphin 2609 and newer would size MEM1 from bi2.bin on their own;
the override covers older builds (it exists since 5.0-11969) and wins when both are
present. A vanilla disc launched the same way trims its arena back to 24 MB itself.
Save states refuse to load across memory sizes ("State is incompatible with current
memory settings"), so `saves/start.sav` was recaptured. Netplay propagates the
host's override to clients. Real hardware: no.

### Bounds in the mod

`addr.MEM1End` and `wwapi.MEM1End` are 0x83000000; `wwapi.h` `MEM1_END` for the C
pointer checks (the archive heap, where Link.arc and the mini-Link models live,
now reaches past 0x81800000). `emu.scanForRAM` still accepts any region of 24 MiB
or more, so a 24 MB game is found and simply refuses reads past its end.

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

Superseded (kept as history): 48 MB MEM1 (above) made room, and the remote
players are now the REL's lite bodies and, behind the heap gate, the Stage 4
full daPy_lk_c bodies ("Three remote Links", "Heap per remote"). The plan
at the time:

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

The dev tools do this from the command line now: `WW_DOLPHIN_DEVLOG=1` on
`wwdev dolphin2` / `auto-recapture` logs OSReport, MI and MASTER to
`<user dir>/Logs/dolphin.log` with the panic popups off (session-only `-C`,
no ini written; docs/TESTING.md "Dev tooling"), `mp-local` copies each
Dolphin's log into `logs/latest/`, and after a JUTException screen
`wwdev crash-context` prints the faulting context and stack.

## macOS: User-Copy Dolphin Silently Replaced by /Applications/ — SOLVED

### The symptom

`task_for_pid(pid) failed: kern_return=5` against a freshly-launched
Dolphin, even though:

- `ww-multiplayer` was ad-hoc signed with `com.apple.security.cs.debugger`
- `~/Applications/Dolphin.app/Contents/MacOS/Dolphin` was re-signed without
  the hardened runtime by `scripts/setup-mac-dolphin.sh`
- `dolphin2` was passed the path to the user copy

### The diagnosis

`lsof -p <pid> | awk '$4=="txt"'` showed the running Dolphin's text
segment was mapped from `/Applications/Dolphin.app/Contents/MacOS/Dolphin`
(still hardened-runtime), not the user copy at `~/Applications/...`.
Direct shell exec of the user-copy binary loaded the user copy correctly;
only Go's `exec.Command` (and only Go's — even with `</dev/null` and
detached process group) was getting silently re-routed.

### The root cause

Two-part:

1. Both `Dolphin.app` bundles shipped with the same `CFBundleIdentifier`
   (`org.dolphin-emu.dolphin`). LaunchServices treats them as a single
   logical app and picks one as canonical (defaults to `/Applications`).
2. Go's `exec.Command` of `<bundle>/Contents/MacOS/Dolphin` ends up
   passing through LaunchServices on macOS, which substitutes the
   canonical-app binary at exec time. The shell does not.

The result: `dolphin2` thought it launched the user copy, but every
running Dolphin process was actually the hardened-runtime binary at
`/Applications/`, against which AMFI denies `task_for_pid` regardless
of the caller's `cs.debugger` entitlement.

### The fix

Two changes, both required:

1. `scripts/setup-mac-dolphin.sh` now sets
   `CFBundleIdentifier=org.dolphin-emu.dolphin-unhardened` on the user
   copy and runs `lsregister -f` so LaunchServices treats it as a
   separate app from the canonical `/Applications/Dolphin.app`.
2. `dolphinenv.Launch` (`internal/dolphinenv/launch_darwin.go`, now `wwapi/emu/launch`; `mp_local.go`'s `launchDolphinDetached` before App PR 3) routes through
   `/usr/bin/open -n -a <bundle> --args ...` on darwin instead of
   `exec.Command` on the inner binary. `open` drives LaunchServices
   directly using bundle ID, so the distinct ID actually takes effect.
   PID is recovered by snapshotting `emu.ListPIDs()` before the
   call and polling for the new entry.

### Diagnostic recipe

If `task_for_pid` ever starts failing on macOS again, before chasing
entitlements or hardened-runtime configuration:

```bash
lsof -p <dolphin-pid> | awk '$4=="txt"' | grep Dolphin
```

If the path is `/Applications/...` instead of `~/Applications/...`,
LaunchServices is re-routing — the entitlement chain is fine, the
process is just the wrong binary.

## Observations Worth Remembering

- Picking up a rupee triggers a display refresh. Before the pickup, the displayed rupee count may lag behind the stored value.
- The wallet cap (200/1000/5000) applies when the game reads the rupee value, not when we write it. So writing 777 and picking up a rupee shows 200 if your wallet is size 200.
- `PlayerPtr[0]` at `0x803CA754` is null during file select and stage loads, but non-null on the title stage (`sea_T` has a Link). So "player loaded" does not mean "in play" (see the feed's inPlay gate).
