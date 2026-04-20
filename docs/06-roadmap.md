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
  `m678 = 2` for TSUBO, clears `mSwitchNo` for KAMOME.
- **Multi-puppet architecture** (2026-04-18): mailbox redesigned around
  `MAX_PUPPETS = 4` slots of 0x20 B each (`inject/include/mailbox.h`).
  Each slot is `{actor_ptr, active, pos_xyz, rot_xyz}`. C loops over
  slots every frame: spawns on active+unspawned, syncs spawned, drops
  bookkeeping when `active` clears or the actor dies. Go `puppet-sync`
  maps remote player IDs -> slot indices and writes each remote's pos/rot
  to its assigned slot. Verified live: two fake-clients on one machine
  drove two separate puppets (seagull + pot) following two distinct
  circle patterns on Outset.
- **Human NPC puppet: Rose** (2026-04-18): `PROC_NPC_OB1` (0x014D) —
  one of the outdoor Outset villagers — has her archive preloaded with
  the stage. Spawns cleanly, stays alive, accepts position/rotation writes
  like any other actor. No mode_hide-style render guard — she renders
  immediately without an unhide poke. This is the first **human**
  puppet: Slot 1 in the current demo spawns Rose; a remote player's
  position makes Rose-the-NPC walk around Outset driven by TCP. Kids
  (NPC_KO1) still self-destruct because their archive isn't resident
  outdoors; fairies (NPC_FA1) self-delete after healing — Rose is the
  proven path for now. Other outdoor villagers (Abe, Mesa, Sturgeon)
  are likely resident too.
- **Per-slot proc differentiation** (2026-04-18): slot 0 spawns KAMOME,
  slot 1 spawns NPC_OB1 (Rose), slot 2 KAMOME, slot 3 TSUBO. Each slot
  is visually distinct so multiple remote players are immediately
  identifiable. Proper color tinting would need a mid-draw hook (KAMOME's
  `daKamome_Draw` rebuilds `actor.tevStr` every frame via
  `g_env_light.setLightTevColorType`, clobbering any execute-phase
  color override); mixed procs gives stronger differentiation anyway.
- **Mini-Link pipeline — plumbing proven, rendering blocked**
  (2026-04-19): `getRes("Link", 0x18, &mObjectInfo[0], 64)` returns
  valid `J3DModelData`; `mDoExt_J3DModel__create` returns non-NULL
  `J3DModel*` allocated into ArchiveHeap. Freighter draw-phase hook
  installed at `0x80108210` (the `bl daPy_lk_c::draw` inside
  `daPy_Draw`) — our C shim calls Link's real draw at `0x80107308`
  and then our per-frame matrix+submit. Mailbox moved to `0x80410F00`
  flush against `__OSArenaLo`. Fixed a major `getRes` arg-count bug
  (static member mistaken for non-static — shifted every arg by one
  register and spammed `<System.arc> getRes: res nothing !!` at ~143
  logs/sec). Two blockers remain (see "Next Session Priority" and
  docs/05 "Mini-Link render pipeline"): `mDoExt_modelEntryDL` breaks
  sky rendering regardless of phase; `J3DModel::calc()` crashes Link
  via j3dSys global pollution.
- **First independent visible Link rendering via our pipeline**
  (2026-04-19 late): Two Links visible on Outset, no crash, sky clean,
  `draw_progress=38` (full path) every frame. The missing piece was
  `J3DModel::mUserArea` (offset 0x14): Link's joint callbacks (bound
  to the shared J3DModelData via J3DJoint subclasses) recover the
  owning `daPy_lk_c*` from `model->getUserArea()` during calc, then
  read its state. With our mini-Link's mUserArea = 0, the callback
  derefed NULL at PC 0x8010C53C inside checkEquipAnime. Wiring
  `*(u32*)(mini_link_model + 0x14) = (u32)link_actor` each frame in
  the draw hook routes the callback to Link #1, calc completes, the
  bone matrices populate, modelEntryDL submits cleanly. Full 0x128
  j3dSys snapshot around calc is kept as a safety net but the
  userArea write is the actual unblock. **Bisect path that found it**:
  step 1 (5-field j3dSys save/restore) → still stuck at 34. Step 2
  (full 0x128 snapshot) → still stuck at 35. Stub calc + modelEntryDL
  → 38 stable, infrastructure proven innocent. Re-enable calc only →
  back to stuck at 35. Confirmed calc was the singular failure. Hit
  zeldaret/tww decomp for J3DModel layout: `mUserArea @ +0x14`,
  every actor with bound callbacks does
  `model->setUserArea((u32)this)`. Fix verified live — second Link
  walks/idles/swings sword identically to Link #1 (joint callback
  acts on Link #1's state). Independent animation is the next wall.
- **First visible geometry from our render pipeline — rigid model
  working end-to-end** (2026-04-19): Tsubo fragment
  (ALWAYS_BDL_MPM_TUBO=0x31) renders and tracks Link in real time,
  sky clean, no crashes. Recipe: `getRes("Always", 0x31, mObjectInfo,
  64)` → `mDoExt_J3DModel__create(data, 0x80000, 0x11000022)` in
  ArchiveHeap; each frame inside `daPy_Draw` hook (after Link's real
  draw returns): write base matrix @ J3DModel+0x24 → save
  `j3dSys.mModel`+`mCurrentMtxCalc` (offsets 0x38/0x30 in the j3dSys
  struct @ 0x803EDA58) → `J3DModel::calc` @ 0x802EE8C0 → restore →
  `mDoExt_modelEntryDL` @ 0x8000F974. **Unblocked two open blockers
  from the previous session**: (1) `modelEntryDL`'s sky breakage
  root cause was SHARED J3DModelData — entry()'s per-frame bucket
  insertion double-registered Link's material packets. Non-shared
  data submits cleanly. (2) `calc()`'s crash was confirmed as j3dSys
  global pollution; a minimal 2-field save/restore is sufficient for
  rigid models. Matrix propagation (base→node→drawMtx) also proven
  essential — without `calc()` the GX draw buffer stays uninitialized
  and renders at origin (invisible), which was silently defeating
  earlier "no calc" experiments. Skinned Link's model still crashes
  the same way under calc — its skeleton walk touches additional
  j3dSys fields beyond the 2 we save. Next session: expanded
  save/restore or separate Link archive copy. Diagnostic aid added:
  `mailbox.draw_progress` (+0x0C) so the draw hook's furthest
  execution point is observable independent of execute-phase writes.

## 🔬 Next Session Priority

**Break the Link #2 mirror — give him independent animation.** As of
2026-04-19 late, Link #2 renders end-to-end via our pipeline (see
"Done") but his pose is computed from Link #1's `daPy_lk_c` state
because that's what we hand to the joint callbacks via `mUserArea`.
For real multiplayer, Link #2's pose must come from the wire.

**Attack plan, in order of cost:**

1. **Synthesize a fake `daPy_lk_c`** with just the fields the joint
   callbacks read. Hand its address as `mUserArea` instead of Link
   #1's. Each frame, populate the fake's pose / anim-state fields
   from the network. Discovery work: figure out exactly which
   `daPy_lk_c` fields the joint callbacks touch. The +0x301C field
   that originally crashed (`lhz r0, 0x301C(r3)`) is the first
   known field — trace from there. Likely candidates: anim ctrl
   pointer, current frame, sword/shield state, equip state. Probably
   a small set; full struct (10000+ bytes) doesn't need to be faked.
2. **Per-instance joint-callback override.** If the J3DJoint vtable
   pointers can be replaced on our model's joint tree (a copy or a
   redirect), we skip Link's callbacks entirely and compose bones
   from base anim + manual matrix writes. More invasive — touching
   J3DModelData internals — and we'd then have to reproduce the
   joint-walk math ourselves.
3. **Network protocol extension.** Once Link #2 is animating from
   our fake state, wire format must carry whatever subset of fields
   the callbacks read (current anim, frame, sword state, etc).
   Existing puppet protocol carries pos+rot only; this adds an
   anim-state struct. Probably 1-2 dozen bytes per remote.

### Working skinned-Link recipe (keep this handy, it's your baseline)

Current `inject/src/multiplayer.c` state: `PROBE_ARCNAME = "Link"`,
`PROBE_BDL_IDX = LINK_BDL_CL`, uses `modelEntryDL` with `calc()`
wrapped in full 0x128-byte j3dSys snapshot AND `mUserArea` set to
Link #1's actor each frame. Renders a second Link mirroring Link #1
at `link_pos + (100, 0, 0)`. To probe a different model: flip
`PROBE_ARCNAME` / `PROBE_BDL_IDX`. To break the mirror: replace
`(u32)this_` in the userArea write with the address of a synthetic
state struct.

### Solved this session (2026-04-19 evening + late evening)

- `modelEntryDL` sky breakage root cause: shared J3DModelData, not
  the function itself.
- `calc()` crash for rigid: 2-field j3dSys save/restore (mModel +
  mCurrentMtxCalc) fully sufficient.
- `calc()` crash for skinned Link: NOT a j3dSys issue. 5-field and
  full 0x128 snapshot both still crashed. Real cause was joint
  callbacks reading `model->getUserArea()` and finding NULL. Set
  it to the live Link actor and the callbacks complete cleanly.
- Matrix propagation understood: `mBaseTransformMtx` @ +0x24 is input
  only; GX actually uploads from `mpDrawMtxBuf` @ +0x94, populated by
  `calc()` via `mpNodeMtx[]` @ +0x8C. No calc = invisible at origin.
- Bisect technique that pinned calc as the singular failure: stub
  `calc + modelEntryDL` separately, watch `draw_progress` markers.
  Cheap, decisive.

---

**Original Option B plan (below) is still the direction — these are
sub-problems within it, not a pivot.**

Path B: network-replicated pose. A second Link that coexists with Link
#1 and is driven by the remote player's animation state over the wire.
Remote Link swings sword when the remote player swings. Not a
shared-state puppet, not a cosmetic draw-twice — a real independent
Link whose state comes from the network rather than from local input.

### Why Path B (and not Path A)

`daPy_lk_c` is written as a singleton. Its construction hijacks
`PLAYER_PTR_ARRAY[0]` (`phase_1` at `0x80125CC8` calls
`dComIfGp_setPlayer(0, this) / setLinkPlayer(this)`), and its runtime
writes pepper `dComIfGp_setPlayerStatus*(0, ...)` globals from dozens
of sites across `d_a_player_*.inc`. Making the class fully re-entrant
(Path A) means rewriting dozens of call sites inside 10k lines of
player code — weeks of work. Draw-hook / render-twice (old Angle 3)
is rejected outright: remote Link would mirror local Link's animation,
which is not real multiplayer.

Path B takes the pragmatic middle: run execute() for Link #2 with its
I/O redirected (global writes filtered, input zeroed) and feed anim
state from the network. Draw runs naturally, pose tracks the wire,
Link #1 is never corrupted.

### Path B plan

~~1. **Carve a 1 MB heap region for Link #2** by bumping `__OSArenaLo`
   to `0x80511000`.~~ **TRIED AND FAILED (2026-04-18).** Patch landed
   cleanly (boot log confirmed `Arena : 0x80511000 - 0x817f2120`), but
   ZeldaHeap shrank by exactly 1 MB (`HeapSize=004f98e0` ≈ 5.1 MB, was
   ~6.1 MB), which broke Outset archive loads: dozens of `Cannot
   allocate ... ZeldaHeap` OSReports, `デモデータ読み込みエラー！！`, and
   the game fell into a demo-fallback path instead of the title screen.
   **Empirical lesson: MEM1 has no spare megabyte. ZeldaHeap consumes
   essentially all of the arena.** Reverted to `__OSArenaLo = 0x80411000`.
   See docs/05-known-issues.md "ClearArena Wall" for the reverted patch.

The rest of the original plan (intercepting the alloc, patching phase_1,
filtering global writes, zeroing input for Link #2, applying network
anim state, extending wire protocol) still holds. But step 1 — where the
704 KB for Link #2's heap comes from — needs a completely different
strategy. Three candidates, none confirmed:

### Revised step 1 — where does Link #2's 704 KB live?

**Option A: Early-allocate from GameHeap at boot.** In `main01_init`
call `mDoExt_createSolidHeap(0xB0000, mDoExt_getGameHeap(), 0x20)` once,
hold the heap pointer, hand it to the `fopAcM_entrySolidHeap` hook when
Link #2 constructs. GameHeap shrinks by 704 KB for everyone else.
**Risk — probably fatal on Outset:** docs/05 OSReport shows Outset's
GameHeap has ~245 KB free after normal actor loading today; taking
another 704 KB means Outset's own stage actors OOM by ~470 KB. Could
still work on lighter stages (Link's house interior, Grandma's house,
Windfall shop) where normal actor load is smaller.

**Option B: Custom "mini-Link" proc that shares Link's model data.**
Write a new actor class (not `daPy_lk_c`) that loads only `LINK_BDL_CL`
from the shared object archive and the minimum anim machinery — no
aura, hands, hair, sword grips, bombs, arrows, masks, etc. Estimated
footprint under 100 KB instead of 704 KB, easily fits in GameHeap.
Sidesteps the singleton problem (we write the class, it never calls
`dComIfGp_setPlayer(0, this)` and never writes slot-0 status). **Cost:**
we reimplement just enough of the Link render path (J3DModel, anim
frame ctrl, matrix calc) to drive Link's rig from the network. Real
work, probably multiple sessions, but bounded.

**Option C: Grow GameHeap by stealing from CommandHeap or ArchiveHeap.**
`m_Do_machine.cpp:466` computes `sysHeap = arenaSize - CommandHeapSize
- ArchiveHeapSize - GameHeapSize`. Those three sizes are constants in
the DOL. If CommandHeap or ArchiveHeap have slack, bump GameHeapSize at
their expense — binary patch the constant. Need to measure slack before
committing. ZeldaHeap has zero slack (proven above), but command/
archive are untested.

### Recommendation → committed: Option B

Option C was attempted (heap-slack measurement via `JKRExpHeap::do_getFreeSize`
calls sampled once per second). Even after switching from the virtual-dispatch
wrapper `JKRHeap::getFreeSize @ 0x802B0868` (which produced repeated Dolphin
MMU invalid-read warnings at PC `0x802b0874` — likely C++ this-adjust / MI
vtable-layout mismatch against raw C function-pointer calls) to the concrete
`JKRExpHeap::do_getFreeSize @ 0x802B22C4`, the game froze once in-game.
Option C abandoned. Committed to Option B.

### Option B — mini-Link rendering path (research complete 2026-04-18)

Plan: render a second Link from OUR injection code, outside the actor
system. Reuses Link #1's already-loaded `J3DModelData` (shared via the
object archive), creates a separate `J3DModel` instance with its own
base matrix + anim state, submits it each frame via `mDoExt_modelEntryDL`.
No PROC_PLAYER spawn, no phase_1 singleton hijack, no 704 KB heap.

### Key addresses (GZLE01)

**Resource lookup:**
- `dRes_control_c::getRes(arcName, index, info[], count) @ 0x8006F208` — static, 4 args
- `&g_dComIfG_gameInfo.mResControl.mObjectInfo[0] = 0x803E0BC8` (`g_dComIfG_gameInfo @ 0x803C4C08` + `mResControl` at +0x1BFC0 + `mObjectInfo` at +0x0)
- `ARRAY_SIZE(mObjectInfo) = 64`
- Link's archive name: string literal `"Link"`
- `LINK_BDL_CL = 0x18` (main body model file index)

**Model creation + draw:**
- `mDoExt_J3DModel__create(J3DModelData*, u32 modelFlag, u32 dlistFlag) @ 0x80016BB8`
- `mDoExt_modelEntryDL(J3DModel*) @ 0x8000F974` — queues to the draw phase, safe to call from our execute-phase hook
- `J3DModel::setBaseTRMtx(Mtx)` — sets the base transform; base matrix lives at J3DModel struct offset 0x24

**Matrix building** (GX standard):
- `PSMTXTrans`, `PSMTXRotRad` from `dolphin/mtx/` — build translation + Y-axis yaw

### First prototype scope

1. Once at start (after Link #1 exists so his archive is resident): call
   `getRes("Link", 0x18, (dRes_info_c*)0x803E0BC8, 64)` → J3DModelData*.
2. Create J3DModel via `mDoExt_J3DModel__create(modelData, 0x80000, 0)`. Hold
   the pointer.
3. Each frame: build a translation matrix at `link_pos + (100, 0, 0)` (offset
   so we can see it), `setBaseTRMtx(model, mtx)`, `modelEntryDL(model)`.
4. Expected outcome: a **T-posed Link** renders next to the real Link, same
   animation/none, just proving the render pipeline. T-pose because we're
   not yet driving anim state — that's step 2 of Option B.

### Unknowns remaining

- **When is Link's archive actually available?** Link #1's `daPy_createHeap`
  does the loads via `dComIfG_getObjectRes(l_arcName, ...)`. After Link #1's
  phase_2 completes, archive is resident. Gate our `getRes` call behind
  `PLAYER_PTR_ARRAY[0] != NULL` and a few seconds more to be safe.
- **Where does our new J3DModel's heap allocation go?** `mDoExt_J3DModel__create`
  allocates from the CURRENT heap (whatever that is at call time). We'd want
  to control this — allocating from ZeldaHeap (where Link #1's J3DModel also
  lives) would be natural. May need to `becomeCurrentHeap` before the call.
- **Per-joint matrix buffers.** J3DModel with skinning needs bone matrices.
  Creating a plain J3DModel might not be enough — Link has 0x2A joints and
  per-joint matrices. If `mDoExt_J3DModel__create` doesn't allocate these
  automatically, we need to track down the allocator.
- **Shared vs separate animation.** If we share `mpCLModelData`'s anim-callbacks
  with Link #1's model, their animations will clobber each other. Need to
  verify whether the per-instance J3DModel keeps its own bone state.

### Known unknowns we'll hit along the way

- `daPy_Execute` references 100+ fields in the 10k-line `daPy_lk_c`.
  "Filter globals / zero input" is probably more than one filter — we
  may need to handle HUD-icon state (`setAStatus` / `setDoStatus` /
  `setRStatus`), metronome state, and event-flag I/O.
- Camera ID: `mCameraInfoIdx = dComIfGp_getPlayerCameraID(0)` in
  execute() and playerInit(). Link #2 inherits Link #1's camera idx.
  Probably fine since Link #2's draw uses the view we're already
  rendering; worth checking once we see Link #2 alive.
- `daPy_getPlayerActorClass() == this` gates (6 sites in main).
  Flips for Link #2. Pay attention to what the guarded code does;
  some may be intentional "only the primary Link runs this."
- Once Link #2 renders, input from the Pad singleton may still reach
  Link #2 in ways we didn't anticipate — if so, patch the input
  read-site(s) per-instance.

### Rejected approaches (for the record)

- **Angle 1 naive ("just fix the 704 KB heap").** Heap fix alone
  isn't enough; the class hijacks singleton state at construction
  and corrupts it every frame in execute. Angle 1 is *subsumed*
  by Path B (steps 1-2), it just isn't sufficient on its own.
- **Angle 2 (full resource-sharing refactor of `daPy_lk_c`).**
  Intractable in any reasonable time budget; the class is 10k
  lines of tangled state.
- **Angle 3 (render Link's model twice).** Shared animation state
  makes remote Link mirror local Link. Fails the project goal of
  "each remote player has independent animations."

### History: 2026-04-18 diagnosis

The "PROC_PLAYER crashes after ~23s" behavior is **not** a singleton
`JUT_ASSERT`. Dolphin's OSReport log during the crash:

```
Error: Cannot allocate memory 721040 (0xb0090) byte ... from 81523910
FreeSize=0003d770 TotalFreeSize=0003de00 HeapType=EXPH HeapSize=002ce770 GameHeap
見積もりヒープが確保できませんでした。
```

Link #2 wants ~704 KB (`fopAcM_entrySolidHeap(this, daPy_createHeap, 0xB0000)`
at `d_a_player_main.cpp:12173`); GameHeap only has ~245 KB free on
Outset. 23-second delay = heap fragmentation tipping it over.
`OSPanic` / `PPCHalt` is the downstream effect of a null alloc, not
an invariant-assertion failure.

### Known-good baseline at start of this plan

- `fopAcM_create(...)` queues spawns; `fpcM_Management` constructs
  next frame.
- Per-frame hook + callback shim stable (docs/05).
- Multi-puppet architecture: 4 slots, mailbox, per-slot sync for
  KAMOME / TSUBO / NPC_OB1 (Rose).
- `inject/src/multiplayer.c` current code spawns non-Link procs
  per slot. It'll stay in place while we build Link #2; eventually
  slot 0 will switch to PROC_PLAYER.

## Hook + shim recipe (current working baseline)

Documented in `docs/05-known-issues.md` → "Per-Frame Hook — SOLVED". The
shim at `0x80023204` + callback pointer at `0x80410F08` (inside the
mailbox at `0x80410F00`) + `main01_init` hook at `0x80006338` is the
stable foundation for anything per-frame. A third `hook_branchlink` was
added 2026-04-19 at `0x80108210` inside `daPy_Draw` for running code in
Link's draw phase — see `docs/05-known-issues.md` → "Mini-Link render
pipeline" for the shape.
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
   - Hooks:
     - `main01 @ 0x80006338` → `main01_init` (one-shot; publishes callback pointer)
     - `fapGm_Execute bl @ 0x80023204` → `frame_shim` → `multiplayer_update` (per-frame execute)
     - `daPy_Draw bl @ 0x80108210` → `daPy_draw_hook` → `daPy_lk_c::draw` then mini-Link submit (per-frame draw)
   - Post-build patches: OSInit immediates (four writes) + revert five Freighter clobbers
3. `wit copy <ciso> <iso> --iso --trunc --overwrite` decompresses to plain ISO
4. Python snippet writes `patched.dol` at the ISO's DOL offset and shifts the FST past the DOL end (we use a 0x1000-aligned new FST offset)
5. Update the ISO header's FST offset field (at disc offset `0x424`)
6. Delete `%APPDATA%/Dolphin Emulator/Cache/gamelist.cache` (if present) and restart Dolphin
7. Boot the patched ISO — **no Gecko codes / Dolphin patches enabled** (they fight the DOL)
8. `./ww.exe dump` to verify: mailbox counter at `0x80410F00` increments, T2 code at `0x80410000` is intact, main01 hook at `0x80006338` shows `0x484XXXXX` (a `bl`)

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
