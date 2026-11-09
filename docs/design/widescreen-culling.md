# Widescreen culling: the cause and the plan

**Status:** plan only, no code yet (card #1184, 2026-11-09). The build is card #1185. Card #1186
plans native widescreen (no Dolphin hack, HUD and menus) and reuses the hook and the setting
designed here; section 9 says what it inherits.

## 1. In short

- **The person's setup:** Dolphin's own widescreen hack. Every Dolphin user dir on the Mac and both
  PC fixture dirs (`~/ww-shared/fixtures/dolphin-user-3/-4`) have `GFX.ini [Settings]
  wideScreenHack = True` and `AspectRatio = 1`. Nothing in this repo turns widescreen on: not
  `cheats/GZLE01.ini`, and not the patch.
- **The cause (confirmed live):** the game culls against one software frustum,
  `mDoLib_clipper::mClipper`. Its aspect is the camera's 1.28 (4/3 × 0.96). Dolphin's hack widens
  only the GPU projection, by exactly 4/3, so the picture reaches tan 0.985 horizontally while the
  game culls at tan 0.739. The outer 1/8 of the screen on each side is drawn by the GPU, but the
  game has already dropped what is there. Actors, room geometry pieces, grass, trees, bushes,
  flowers, real shadows and dust puffs vanish there. NPCs with `NOCULLEXEC` also freeze there.
- **The fix:** one T2 hook on the only call to `mDoLib_clipper::setup` (`bl` at `0x8017BF90`)
  multiplies the aspect argument by a factor from a small mailbox block, default 4/3. That widens
  every frustum user in the game at once. The camera's `mAspect`, and with it the picture, is left
  alone.
- **Cost:** roughly 0x80-0x100 bytes of T2 (0xBF8 free on main), one 0x18-byte mailbox block in
  free slack, one op number, a new BlobHash (every player re-patches, start saves are recaptured),
  and the C build needs devkitPPC (the PC today).
- **Checked against main at `735c8d7`**, which includes co-op M7's `WwModuleDesc` table and Go → C
  command queue (wind#90) and the name tags' widescreen scale (#1157). The C mod follows that
  pattern.

## 2. The person's Dolphin setup

| Setting | Value | Meaning (Dolphin 2603 source) |
|---|---|---|
| `GFX.ini [Settings] wideScreenHack` | `True` | `bWidescreenHack` |
| `GFX.ini [Settings] AspectRatio` | `1` | `AspectMode::ForceWide` (enum in `VideoConfig.h`: 0 Auto, 1 ForceWide, 2 ForceStandard, 3 Stretch, 4 Custom, 5 CustomStretch, 6 Raw) |

What the hack does, from `~/ww-shared/tools/dolphin-src` (tag 2603; the Mac runs 2603-342 and the
PC 2609a, assumed to behave the same):

- `Presenter::UpdateDrawRectangle` (`Source/Core/VideoCommon/Present.cpp:659-687`) sets
  `fAspectRatioHackW = source_ar / draw_ar`. With ForceWide, `draw_ar = vi_ar × (16/9)/(4/3)`, so
  W = 0.75 exactly, whatever the VI timing is.
- `VertexShaderManager::LoadProjectionMatrix` (`VertexShaderManager.cpp:52-54`) multiplies the x
  terms of **perspective** projections by W. Orthographic (2D, HUD) projections are untouched,
  which is why the HUD looks stretched.
- Nothing in that path writes emulated RAM, so the game cannot know the picture got wider.
- With the hack on, Dolphin's widescreen heuristic (`Widescreen.cpp:84-151`, overrides in `GetWidescreenOverride` at `:55-80`) compares the
  projection's aspect with the viewport's. WW's ratio is 1.28 / 1.333 = 0.96, which reads as a
  normal 4:3 game, so the hack applies. **If anything widened the camera's `mAspect` itself, the
  heuristic would read the game as anamorphic and switch the hack off.** That is why this mod must
  widen only the clipper, and why #1186 must widen `mAspect` (section 9).

**Main already reads these settings.** `launch.ReadGFXAspect(userDir, args)` and
`GFXAspect.XScale(renderW, renderH)` (`wwapi/emu/launch/widescreen.go`, added for the name tags
in #1157) resolve GFX.ini, `-C` overrides and the GameSettings `[Video_Settings]` layers in
Dolphin's order (but see `native-widescreen.md` section 7: today it matches `-C GFX.Settings.*`,
which Dolphin drops; the real system name is `Graphics`, fixed in #1186's stage 1), and return how much the hack narrows the 3D picture's x. **The culling factor
this mod needs is exactly `1 / XScale`** (XScale 0.75 → factor 4/3; XScale 1 → no widening).

For other settings the factor is `draw_ar / source_ar`: Custom gives `custom / (4/3)`. Stretch
gives `window_ar / vi_ar`, where `vi_ar` is only close to 4/3, so a few percent of margin is needed.
When the window is narrower than 4:3 (`adjust > 1`), Dolphin scales the vertical axis instead and
the 4:3 clipper already covers the picture: the factor is never below 1.

## 3. The cause, confirmed

### 3.1 The code path

- `view_setup` (`d_camera.cpp:5347`, function `0x8017BEB0`) loads `f1 = view->mFovy` (+0xD0),
  `f2 = view->mAspect` (+0xD4), `f3 = view->mNear` (+0xC8), `f4 = far`, and at `0x8017BF90` calls
  `bl 0x800176BC`, `mDoLib_clipper::setup`. A scan of the vanilla DOL finds no other caller.
- `setup` stores them in `mClipper` (`J3DUClipper` at `0x803A570C`: fovY +0x4C, aspect +0x50,
  near +0x54, far +0x58; the aspect store is `stfs f2,0x50(r3)` at `0x800176E0`) and calls
  `J3DUClipper::calcViewFrustum` (`0x802566CC`). Its left and right planes use
  `nearX = mAspect × near × tan(fovY/2)` (`J3DUClipper.cpp:34`). Top and bottom don't depend on
  the aspect.
- `changeFar` and `resetFar` (`m_Do_lib.h:12-15, 32-35`) rewrite only `mFar` and rebuild the
  planes from the **stored** aspect. The many per-frame far changes (actors with `cullSizeFar`,
  room BG, grass, trees, shadows) therefore keep whatever aspect `setup` stored. A one-off poke of
  `0x803A575C` doesn't stick, because `view_setup` stores it again every frame.
- `view->mAspect` is written every frame by `preparation()` (`d_camera.cpp:5315`, store at
  `0x8017BE88`): `(4/3 at 0x803FA998) × g_HIO.mAspectRatio (0.96 at 0x803B9A08) = 1.28`. The same
  value also builds the GPU projection (`C_MTXPerspective` at `d_camera.cpp:5463`, `bl` at
  `0x8017C444`) and the particle draw aspect (`m_Do_graphic.cpp:1604, 1673`).

### 3.2 Live evidence (Mac rig, 2026-11-09)

Rig `c1184`, Dolphin 2603-342 with the fixture's `wideScreenHack = True, AspectRatio = 1`, the
rig's default disc `WW-mp.iso` and start save `start-mac-3D7FEC97-A37EC8DB.sav` (BlobHash
0xA37EC8DB; Hyrule Castle courtyard). The culling code is vanilla, so the build doesn't affect the
result. A Gecko C2 code in the rig's `GameSettings/GZLE01.ini` was tried first, but Dolphin's
code handler never installed after the state load (not chased further). To change only the clipper, a
test-only routine was poked into the mailbox slack at `0x804150D0`, with a branch to it in place of
`stfs f2,0x50(r3)` at `0x800176E0`. The routine multiplies f2 by the float at `0x804150C8` when
that word is non-zero. Dolphin ran on the interpreter core (`-C Dolphin.Core.CPUCore=0`), because
the JIT ignores code pokes (docs/02). None of this is committed. A throwaway probe walked the actor
queue, put each actor through `j3dSys`'s view matrix, and printed `tanH = x / -z` and the
`fopAcCnd_NODRAW` bit (`actor+0x1C8 & 4`), which `fopAc_Draw` sets when the cull check fails.

- Clipper read live: fovY 60.0, **aspect 1.2800** (`0x3FA3D70A`). The side planes sit at
  tan(30°) × 1.28 = **0.739**.
- The visible edge, from Dolphin's source (W = 0.75 exactly): 0.739 × 4/3 = **0.985**. A rough
  cross-check from a dumped frame agrees: the red boat, assumed to be actor 0x0A7 at tan −0.325,
  sits about a third of the way from the centre to the edge, which puts the edge near tan 0.98.
- **Factor sweep** (same frame, only the clipper's aspect changed): each actor's NODRAW bit flips
  where the side plane crosses it.

  | actor (proc) | view z | tanH | culled at side-plane tan | drawn at |
  |---|---|---|---|---|
  | 0x0BD | −18262 | 0.415 | 0.370 | 0.443 |
  | 0x0BD | −19741 | 0.340 | 0.296 | 0.370 |
  | 0x059 | −19320 | 0.305 | 0.222 | 0.296 |

  Actors past about 20,500 units stayed culled at every factor: that is the far plane (the stage
  cull point), a separate limit. In the dumped frames, the grass tufts and tree leaves at both
  edges vanish at factor 0.3 and come back at 1.0, so the packet renderers use this clipper too.
  The frames are on card #1184; game frames are not committed.
- **Side band A/B:** with the camera turned so that actors sat in the band (0.739 < tanH < 0.985):

  | actor (proc) | view z | tanH | clipper 1.28 | clipper 1.7067 (×4/3) | back to 1.28 |
  |---|---|---|---|---|---|
  | 0x1D5 | −18648 | 0.878 | culled | **drawn** | culled |
  | 0x0C0 | −18739 | 0.873 | culled | **drawn** | culled |
  | 0x096 | −18991 | 0.881 | culled | **drawn** | culled |
  | 0x096 | −19606 | 0.912 | culled | **drawn** | culled |
  | 0x0D8 | −19637 | 0.915 | culled | **drawn** | culled |
  | 0x096 | −19717 | 0.912 | culled | **drawn** | culled |
  | 0x0D8 | −19748 | 0.904 | culled | **drawn** | culled |
  | 0x096 | −20774 | 0.915 | culled | culled (past far) | culled |

So: under the hack, everything in the outer 1/8 of each side is culled while visible, and
multiplying only the clipper's aspect by 4/3 brings it back.

## 4. Every culling path, and what the hook covers

The decomp has exactly one `J3DUClipper` (`m_Do_lib.cpp:34`). A DOL scan finds all 38 references
to `0x803A570C` and all 19 calls to `calcViewFrustum` in the functions below.

| Path | Where | Effect in the side bands today | With the clipper hook |
|---|---|---|---|
| Actor draw cull (`fopAcStts_CULL`, ~263 profiles) | `fopAc_Draw` `f_op_actor.cpp:122-160`, `fopAcM_cullingCheck` `f_op_actor_mng.cpp:589-648` | actor not drawn | fixed |
| `NOCULLEXEC` freeze (~98 profiles: Windfall/Outset NPCs, `npc_people`, `ob1`, `ko1`, warp jars, buoys, …) | `fopAc_Execute` `f_op_actor.cpp:186-191` | NPC invisible **and frozen**, then pops in mid-stride | fixed (they tick near the edges, as they would on a 4:3 screen) |
| CULL-only enemies (`bk`, `mo2`, …) | profile status | invisible but still attacking | fixed |
| Props that gate their own execute or colliders (`stone`, `stone2`, `tsubo`, `barrel`, `barrel2`, `mkie`) | e.g. `d_a_stone.cpp:917-951` | pots and rocks in the band can't be hit or targeted | fixed |
| `fopAcM_checkCullingBox` (torches `d_a_ep`, `oship`, `ikada`, `daObj_Search`) | `f_op_actor_mng.cpp:543-552` | torch flames and wakes missing | fixed |
| Room BG per-joint hide (`daBg::draw` → `clipByBox`) | `d_a_bg.cpp:257-283` | chunks of buildings and terrain vanish | fixed |
| Island LOD (`daLodbg::draw` → `clipByBox`) | `d_a_lod_bg.cpp:336-381` | distant island pieces vanish | fixed |
| Grass / trees / flowers / bushes | `d_grass.cpp:384-413`, `d_tree.cpp:562-600`, `d_flower.cpp:466`, `d_wood.cpp:834-852` | clumps pop (their 260/100-unit spheres hide it near the edge, as seen live) | fixed |
| Real shadows (`realPolygonCheck`) | `d_drawlist.cpp:1230-1280` | shadows of things in the band missing | fixed |
| Simple particles (dust, splashes) | `d_particle.cpp:577-600` | never spawned in the band | fixed |
| Bombs, water marks, lava bubbles | `d_a_bomb3.inc:180`, `d_water_mark.cpp:12`, `d_a_yougan.cpp:61` | model or effect skipped | fixed |
| Far plane, `cullSizeFar`, event cull rate, `mSystemFar`, `mFovyRate` | `view_setup`, `f_op_actor_mng.cpp:601-645` | distance only | unchanged (correct) |
| Room load/unload | `d_stage.cpp:2263-2290` | zone-based, not view | unchanged (correct) |
| J3D model level, JParticle, Dolphin's CPU Cull | `J3DJoint::entryIn`, `JPAEmitterManager.cpp:101-112`, `CPUCull.cpp` | no frustum culling of their own (JPA's aspect only builds projected-texture matrices) | n/a |
| Weather, sea sparkles, clouds, sea mesh | `d_kankyo_rain.cpp`, `d_a_sea.cpp:504-646` | volumes around the eye, not frustum. Rain or sparkles may look a little thinner in the bands | unchanged (separate spawn-radius tweak if anyone notices) |
| **Screen-space 0..640 checks:** sun glare and lens flare (`dKyr_sun_move`), firefly glow (`d_a_ff`), lantern glow (`d_a_kantera`), lock-on framing (`pointInSight`, `radiusActorInSight`) | `d_kankyo_rain.cpp:602`, `d_a_ff.cpp:44-46`, `d_a_kantera.cpp:150-152`, `d_camera.cpp:1658-1760` | the glow fades in the bands, and `peekZ` samples the wrong pixel (the GPU picture is squeezed ×0.75 relative to the game's own projection) | **not fixed** by this hook. Cosmetic. Native widescreen (#1186) fixes the sun, firefly, lantern and `pointInSight` checks at once, because `mDoLib_project` follows `view->mAspect`; `radiusActorInSight` reads `mWindowAspectRatio` and stays 4:3 on purpose (native-widescreen.md 3.1). Not worth hooks under the Dolphin hack |
| HUD and reticles placed with `mDoLib_project` (boomerang sight, enemy HP bar, text box anchor) | `d_a_boomerang.cpp:247`, `d_meter.cpp:3728`, `d_msg.cpp:739` | drift outward from their targets under the hack | not culling; #1186 (`ws_project_2d`, native-widescreen.md 3.4) |
| Our own name tags over remote Links (REL, `WWVIS` names) | `inject/rel/visuals.c` | projected with `mDoLib_project` and drawn as 2D, so they would drift outward under the hack | **already handled on main** (#1157): puppet-sync measures the hack's scale (`mods/multiplayer/feed/widescreen.go`) and the REL corrects the tags (`WWVIS_FLAGS_XSCALE_*`). Nothing to do here in mode 1. Native mode (#1186 stage 2) needs a REL change: the q16 XScale cannot carry F > 1, so the REL reads the WWWS block (native-widescreen.md 3.7) |

Not traced: REL actors whose decomp is still stub-only (`/* Nonmatching */`). Any clipper call they
make goes through relocations to the same `mClipper`, so the hook covers it too.

Our own actors: the REL's keeper and remote bodies and Link (so the mini-Links) carry no CULL
status and were never culled. Actor puppets reuse vanilla procs (`ko1`, `ob1`, `fa1`, …) and get
the fix for free.

## 5. The hook

**Site:** `0x8017BF90`, view_setup's `bl mDoLib_clipper::setup` (DOL file offset `0x178ED0`; not
in `DOLPatches` yet). This is the repo's usual kind of site: a `bl` replaced by Freighter's
`hook_branchlink`, never a prologue (`hooks.c:23-28`).

```text
0x8017BF84  C03D00D0  lfs f1,0xD0(r29)   view->mFovy
0x8017BF88  C05D00D4  lfs f2,0xD4(r29)   view->mAspect
0x8017BF8C  C07D00C8  lfs f3,0xC8(r29)   view->mNear      (f4 = far, set at 0x8017BF48 or 0x8017BF80)
0x8017BF90  4BE9B72D  bl 0x800176BC      mDoLib_clipper::setup   <- hook
0x8017BF94  39610020  addi r11,r1,32     (r3 result unused)
```

**Shape:** plain C in `pad_read_shim`'s style. No naked asm is needed, because `setup` has a normal
prologue and the hook receives f1..f4 under the EABI:

```c
// hooks.c (core owns hook sites)
typedef void (*clip_setup_fn)(f32 fovy, f32 aspect, f32 near_, f32 far_);
#define mDoLib_clipper_setup ((clip_setup_fn)0x800176BC)

void clip_setup_hook(f32 fovy, f32 aspect, f32 near_, f32 far_) {
    mDoLib_clipper_setup(fovy, ws_clip_aspect(aspect), near_, far_);
}
```

The hook also covers the very first clipper setup: `init_phase2` (camera create) calls
`view_setup` (`bl 0x8017BEB0` at `0x8017C958`), which goes through the same `bl` at `0x8017BF90`.
A store scan of the DOL finds no other write to `mClipper+0x50`. RELs were not scanned; none in
the decomp source writes it.

`ws_clip_aspect` (the widescreen mod's C entry point, declared in `mods.h`) reads the mailbox
block (section 6) and returns `aspect × factor` only when the block is valid and in mode 1, else
`aspect`. The mod's other entry points use main's `WwModuleDesc` table (`mods.h`, `ww_modules[]`
in hooks.c): one entry `{ 'WS  ', ws_init, 0, 0, 0, 0 }`. It has no per-frame or draw step; the
hook site itself belongs to the core, as every hook site does. It widens only `mClipper.mAspect`. `view->mAspect`, the GPU projection, the particle
aspect and Dolphin's heuristic stay exactly as they are.

Alternatives considered and rejected:

- The store at `0x800176E0` inside `setup`: equivalent, since `setup` has one caller, but it is
  not a `bl`, so it would need a hand-written branch and naked asm.
- `calcViewFrustum`'s aspect load (`0x80256710`): runs many times a frame, and `mClipper+0x50`
  would then no longer hold the effective value.
- `view->mAspect`, `g_HIO.mAspectRatio` or the 4/3 at `0x803FA998`: they widen the picture too.
  Under the Dolphin hack that widens twice, or flips Dolphin's heuristic and turns the hack off.
  That is #1186's knob, not this one.
- Turning culling off (`li r3,0` over `bl fopAcM_cullingCheck` at `0x800235A0`): it doesn't
  cover BG, grass, shadows or the self-gated props, and it costs frame rate everywhere.

## 6. The setting

### 6.1 The mailbox block

A self-describing block in free slack, as `wwboat.h`, `wwvis.h` and `wwrefresh.h` were added (no
`WWMB_LAYOUT` bump). It goes at mailbox **+0xA6C** (`0x8041596C`), right after the boats block,
at the start of the free `CanaryArenaHigh` region (+0xA6C..+0xD00, 0x294 bytes).

| Offset | Type | Field | Meaning |
|---|---|---|---|
| +0x00 | u32 | `magic` | `'WWWS'` (0x57575753) |
| +0x04 | u8 | `version` | 1 |
| +0x05 | u8 | `mode` | 0 = off; 1 = clip (Dolphin hack on: widen the clipper only); 2 = native, #1186 (widen `view->mAspect` through the 4/3 literal `0x803FA998`; the clipper then follows and this hook does nothing) |
| +0x06 | u16 | `pad` | 0 |
| +0x08 | f32 | `factor` | output aspect ÷ (4/3). 16:9 → 1.3333, 16:10 → 1.2, 21:9 (7:3) → 1.75, 64:27 (2560x1080) → 1.7778, 32:9 → 2.6667 |
| +0x0C | u8 | `flags` | #1186: bit 0 compress 2D, bit 1 anchor HUD (WS_SET's c, masked by what the build supports; 0 in #1185's build) |
| +0x0D | u8 | `ui_mode` | #1186: latched 2D mode (0 or 2) |
| +0x0E | u16 | pad | 0 |
| +0x10 | f32 | `ui_factor` | #1186: latched 2D factor (1.0 unless `ui_mode` is 2) |
| +0x14 | f32 | `ui_shift` | #1186: latched HUD edge shift |

Size 0x18 (grown from 0x10 by #1186's plan before #1185 builds it, so no later stage changes the
layout; see `native-widescreen.md` section 5), so the block ends at +0xA84. WS_SET's c carries the
flags byte, and in mode 2 the handler also writes the 3D knob (`native-widescreen.md` 3.1); in
#1185's build it writes the knob back to `0x3FAAAAAB` in modes 0 and 1.

**Rules in C:**

- The mod's `ws_init` (its `WwModuleDesc` init, which main01_init runs after `ops_init` on every
  boot) writes the block: magic, version 1, **mode 1, factor 4/3**, flags = what the build supports,
  `ui_*` to 0 / 1.0 / 0, and the 3D knob `0x803FA998` back to vanilla `0x3FAAAAAB`. That covers the person's setup
  with no Go process at all, and it survives a soft reset (the usual reset to the title keeps RAM as it is and runs no init; a
  reboot reset re-runs main01_init, which writes the block again). The slack is
  orphan memory nothing else zeroes (hooks.c's main01_init comments explain why each block must be
  set at boot).
- `ws_init` also registers one op, `WWAP_OP_WS_SET` = 16 (the first mod op, `WWAP_OP_MOD_FIRST`;
  take the next free number if another mod has registered 16 by then), with
  `ww_cmd_register`. Its handler takes `a` = mode, `b` = the factor's f32 bits and `c` = flags (stored
  `& WWWS_FLAGS_SUPPORTED`, 0 in this build). It refuses (`WWAP_R_REFUSED`) a mode above 2 or a
  factor outside [1.0, 3.0], else writes mode, factor and flags, writes the knob (`(4·factor)/3`
  in mode 2, `0x3FAAAAAB` otherwise; `native-widescreen.md` 3.1) and returns `WWAP_R_OK`. It never
  touches the `ui_*` fields. **Only C writes the block**; Go reads it for status and changes it through
  the op.
- `ws_clip_aspect` applies the factor only when the magic matches, `mode == 1` and
  `1.0 <= factor <= 3.0`. Anything else passes the aspect through unchanged, so a torn or garbage
  block can never shrink or zero the frustum.
- **No stacking with #1186:** mode 2 is the only mode in which the camera's aspect is widened, and
  in mode 2 `ws_clip_aspect` returns the aspect unchanged. The clipper gets the wide aspect through
  `view->mAspect`. One field selects one widening point, so the two can never multiply.

**Default on, for everyone:** a 4:3 player without the hack also gets the wider clipper. The GPU
clips the extra objects anyway, so nothing visible changes. The costs: CPU work for objects just
off-screen; `NOCULLEXEC` actors (about 100 profiles, mostly NPCs) ticking about 1/8 of a screen
beyond each side, so their wake-up and freeze edges move away from vanilla; and more contenders
for the fixed pools (8 real-shadow slots handed out in actor-list order, 128 simple shadows, 150
particle emitters). Multiplayer and co-op don't care: every mirrored enemy kind is CULL-only (it
runs whether or not it is culled) and the REL's copies use their own draw gates. The default is on
because the person this is for always plays with the hack and the CLI can't persist a setting
yet (6.2). Step 7 measures the cost. If a 4:3 player loses more than 1 fps in a busy scene, or
NPC behaviour visibly changes, make mode 0 the C default and let Go turn it on (`auto` below).

### 6.2 Who writes it from Go

Go changes the setting through main's command queue (`wwapi/ops`: `ops.New(mem).Ready()`, then
`Submit(op, mode, factorBits, 0)` and `Wait`), never by writing the block. The queue has **one
writer per game** (its package doc). A one-shot CLI submit while a session's pipelines also
submit to the same Dolphin could collide. So the CLI command below refuses while a session holds the mailbox (`shadow_mode` set,
as `engine.mailboxClaimed` reads it), and the
later in-process writer must share the session's Queue. The app's mod runtime still has
`Game = nil`, and only the multiplayer pipelines attach to Dolphin, during a session
(docs/api/03-writing-a-mod.md; `app_face.go`). So the Go side starts small:

1. **A CLI command from the mod**, `ww-multiplayer widescreen [auto|off|16:9|16:10|21:9|32:9|<aspect>]`
   (a `wwapi.CLI` command, as co-op's `mods/coop/cli` registers its commands). `auto` reuses
   main's reader: find the attached Dolphin's user folder and arguments as `feed/widescreen.go`'s
   `dolphinProc` does (`launch.Processes`, `launch.ProcessUserDir`), call `launch.ReadGFXAspect`,
   then `XScale` (with `host.RenderSize` for Stretch), and write factor `1 / XScale`, or mode 0
   when XScale is 1. A toggle in Dolphin's Graphics window isn't on disk until Dolphin saves, so
   `auto` can be stale; the explicit values are the override. With no argument it
   prints the block. With one it attaches to the running Dolphin (`cliutil.OpenDolphin`, honouring
   `WW_DOLPHIN_PID` / `WW_DOLPHIN_INDEX`), checks the contract (`wwapi.CheckContract`, as
   `mods/multiplayer/feed/contract.go` does), submits `WWAP_OP_WS_SET` and waits for the result.
   It lasts until the next boot or soft reset, then the C default is back. That is enough for
   testing and for anyone not on 16:9.
2. **Later, a writer that follows the setting by itself.** puppet-sync already samples XScale
   every 2 s during a session (`feed/widescreen.go`'s `xscaleSampler`, feeding the name tags). The
   natural follow-up is to move that sampler somewhere both can use (e.g. next to
   `launch.ReadGFXAspect`), and have the widescreen mod write `1 / XScale` whenever a Dolphin is
   attached. A user override can come with it (#1186 defines the app setting:
   `config.Mods["widescreen"].Settings.aspect` = `16:9` / `W:H` / `off`, parsed by its own
   `ParseAppSetting`, not this command's `ParseSetting`). Both need the app to give the mod runtime
   a `Game` once a Dolphin is attached, which #1186's toggle needs too. #1186 starts the app's writer in the engine's Dolphin runner
   (`mods/multiplayer/engine/game.go`) until the mod runtime has a Game, a deliberate exception;
   see `native-widescreen.md` section 7. Make it its own card when #1186 lands, or earlier if a player
   not on 16:9 asks.

## 7. Blob budget and what a C change costs

- **T2 space:** on main at `735c8d7`, `blob.go` `T2End = 0x80413408`, and T2's budget now ends at
  the API region, `0x80414000` (`wwops.h`, `wwapi/mailbox/ops.go` `OpsAddr`):
  **0xBF8 bytes free**. The card's "0x478" is from before wind#80, and the 0x21F8 measured at
  `a9dbc82` is from before wind#90's API region. The hook, `ws_clip_aspect`, `ws_init`, the op
  handler and one `WwModuleDesc` entry come to roughly 0x80-0x100 bytes. Putting the mod in its
  own C unit costs a little more than inlining it in hooks.c (E3 measured split costs, 00-design.md
  §6.6), but it keeps the mod separate. Either fits. #1186 shares the same 0xBF8, so it should
  measure before it plans kilobytes of HUD code there (the REL is the room for bulk code).
- **Mailbox:** the 0x18-byte block above. No `WWMB_LAYOUT` bump, no change to `layout_gen.go`'s
  struct. `CanaryArenaHigh` shrinks by 0x18.
- **The REL** is not a better home. It can't patch DOL code, it loads only after Link exists and
  the 300-frame boot gate, and every REL feature on a DOL call site still keeps a T2 shim. That
  shim is as big as this whole hook.
- **Cost of the C change** (the usual one, CLAUDE.md "Build pipeline"): build.py and
  extract_blob.py on a machine with devkitPPC. **This Mac has none, so the C part is a PC job
  (`needs:pc`)** unless someone installs devkitPro here (it needs an admin password, docs/mac-dev.md).
  Then a new `blob.go` and `blob.stamp`, a new BlobHash: the strict contract check refuses old
  patched discs and save states. Every player re-patches (the app does it by itself after an
  update). `saves/start.sav` is recaptured on the PC (`wwdev auto-recapture`), and the Mac start
  save (`start-mac-3D7FEC97-<new BlobHash>.sav`) under the `ww/rel-blob` lease with
  `scripts/mac-rig.sh recapture`. The REL id stays unless a REL header changes: keep `wwws.h` out
  of `REL_HEADERS`. Release notes tell players to re-patch.
- **Bundling:** #1185 now comes after #1186's plan. If #1186's first stage (world and culling)
  also changes the C side, land both in **one** blob change, so players re-patch and the saves are
  recaptured once.

## 8. Build steps (card #1185)

**Stage 1 of `native-widescreen.md` lands in this same blob change** (its section 8, stage 1:
the 0x18 block, flags in WS_SET's c, the knob write, `WW_FEAT_WIDESCREEN` 0x80, the
`ws_snap_setresult` hook and its Go list), so players re-patch and the saves are recaptured once.

Each step is small enough to commit on its own. The checks are CLAUDE.md's "Checks (run locally)".

1. **Header.** Add `inject/include/wwws.h`: `WWWS_OFF 0xA6C`, `WWWS_SIZE 0x18` (the layout in 6.1), `WWWS_MAGIC
   0x57575753`, `WWWS_VERSION 1`, `WWWS_MODE_OFF/CLIP/NATIVE` (0/1/2), the field offsets above, and
   a `_Static_assert` that `WWWS_OFF + WWWS_SIZE <= MP_EYE_PACKETS_OFF` and `WWWS_OFF >= WWBOAT_OFF +
   WWBOAT_SIZE` (copy minilink.c's assert for the boats block). Add a feature bit in
   `mailbox_core.h`: `WW_FEAT_WIDESCREEN 0x80`, `WW_FEATURES 0xFF` (0x40 is `WW_FEAT_OPS` since wind#90).
2. **C mod.** Add `inject/src/ws/widescreen.c` (a unique basename) with `void ws_init(void)`
   (writes the default block and registers the op), the op handler, and
   `f32 ws_clip_aspect(f32 aspect)` (the rules in 6.1). Add `WWAP_OP_WS_SET 16` to `wwops.h`'s op
   list (one line, the file's `#define NAME value // comment` form) and its Go mirror to
   `wwapi/mailbox/ops.go` (`TestOpsHeaderPins`). Declare `ws_init` and `ws_clip_aspect` in
   `inject/src/core/mods.h`. In `hooks.c`, add the entry `{ MOD_ID_WS, ws_init, 0, 0, 0, 0 }` to
   `ww_modules[]` (after the multiplayer mod; `MOD_ID_WS` 0x57532020, 'WS  ') and
   `clip_setup_hook` (section 5). Add the unit to `C_SOURCES` in `inject/build.py`, and next to
   the other hooks `proj.hook_branchlink("clip_setup_hook", 0x8017BF90)` with a comment naming
   `view_setup` and this doc.
3. **Rebuild (devkitPPC machine):** `cd inject && python build.py && cd .. && python
   scripts/extract_blob.py`. Check that blob.go's `DOLPatches` gained a `bl` at file offset
   `0x178ED0`, that `T2End` stays below `MailboxAddr`, and that `Features` is `0xFF`.
4. **Go mirror and pins.** `go generate ./wwapi/mailbox` (the feature constant). Add
   `wwapi/mailbox/widescreen.go` (the offsets, `WidescreenAddr`, `Decode` of the block for status),
   with `widescreen_test.go` pinning them to `wwws.h` the way `boat_test.go` pins `wwboat.h`. Move
   `CanaryArenaHigh`'s start to `+0xA84` in `wwapi/mailbox/mod.go` and update `TestModRegions`
   (size 0x27C). Add `{mailbox.WwFeatWidescreen, "widescreen"}` to `featureBits` in
   `wwapi/contract.go`. In `internal/inject/t2_budget_test.go`, add a pin test modelled on
   `TestSeachartShimLoadsVisWord`: build.py has the `hook_branchlink` line, and `DOLPatches`
   holds a `bl` at `0x178ED0` that lands in T2.
5. **Go mod.** `mods/widescreen/`: `mod.json` (`id` widescreen, `multiplayer` `local_only`,
   `needs` `["widescreen"]`, `enabled_by_default` true; validate with `go run
   ./tools/validate_manifests`), `mod.go` (embedded manifest, `wwapi.NoRun`, the CLI command),
   `setting.go` (`ParseSetting`: `auto`, `off`, `16:9`, `16:10`, `21:9`, `32:9`, a decimal aspect
   like `2.39`, or `W:H`; factor = aspect ÷ 4/3, clamped to [1, 3]; an aspect at or below 4/3 means
   factor 1; `auto` through `launch.ReadGFXAspect` as in 6.2, with a fake process/size seam for
   tests as `feed/widescreen.go` has), and tests: the parse table, the submitted command's words
   (over `wwapi/ops/opstest` or `wwapi/mem.FakeMem`), the block decode, and a refused submit when
   the contract fails. List it in `compiledMods()` (`cmd/ww-multiplayer/mods.go`). Update the CLI
   and help goldens (`cmd/ww-multiplayer/testdata/cli-golden`, the `mods` listing) with
   `-update` as docs/TESTING.md says.
6. **Dev probe.** Port the throwaway probe from 3.2 into wwdev as `cull-probe [secs]`
   (`cmd/wwdev`, group dev): read `mClipper`'s fovY and aspect, walk the actors (`wwapi/actors`),
   transform by `j3dSys`'s view matrix (`0x803EDA58`, the first 0x30 bytes), and print each CULL
   actor's tanH, view z and NODRAW bit, marking the side band against the 16:9 visible edge. It
   makes step 7 a two-command check and stays useful for #1186.
7. **Live check on the Mac rig** (take a `ww/dolphin-slot-<n>` lease, rig per docs/mac-rig.md, the
   fixture's hack settings, the new start save). With the real hook, the JIT is fine, so no
   interpreter is needed:
   1. `wwdev peek 0x8041596C` shows the magic. `wwdev peek 0x803A575C` shows `0x3FDA740E` (1.7067).
   2. Turn the camera so actors sit in the band (`wwdev input 0x0000 -60 0 3000`, repeated). Run
      `wwdev cull-probe`: no actor with tanH between 0.739 and 0.985 inside the far plane may be NODRAW.
   3. `ww-multiplayer widescreen off`, then `cull-probe`: the same actors are NODRAW again, and
      the clipper aspect is back to 1.28. `widescreen 16:9` restores it.
   4. Pictures: if `mac-rig.sh shot` fails (no Screen Recording permission for the agent), launch
      with `-C Dolphin.Movie.DumpFrames=True -C Dolphin.Movie.DumpFramesSilent=True -C
      Graphics.Settings.DumpFramesAsImages=True` and use `<user dir>/Dump/Frames/`. Put the
      before/after pair on the card, not in git.
   5. Frame rate and pools: `scripts/mac-rig.sh fps <pid>` in a busy outdoor scene (Windfall at
      night, or the sea near an island) with `widescreen off` and `16:9`, hack on and off. Also
      count the real-shadow slots in use (`dDlst_shadowControl_c` `mReal[8]`, `mState != 0`) and
      watch for missing shadows or effects near the centre. Report the numbers. If a 4:3 player
      loses more than 1 fps, make mode 0 the C default (6.1). The hack can be forced on a test rig
      with `-C Graphics.Settings.wideScreenHack=True -C Graphics.Settings.AspectRatio=1` (Dolphin's
      `-C` system name is `Graphics`; `GFX.*` is silently dropped).
   6. A two-Dolphin `mp-local` run with the hook (`scripts/mac-rig.sh pair` + `mp-local`): remote
      Links, puppets and enemy copies still show, and nothing new crashes (`wwdev crash-context`
      if a JUTException appears).
8. **Docs.** RAM map lines in `docs/api/00-design.md` (T2 end, free bytes, the hook list, the
   +0xA6C block), `mailbox_core.h`'s owner map, `docs/04-ww-addresses.md` (`mClipper`,
   `view_setup`'s call site, `g_HIO.mAspectRatio`), a players' note in the next
   `docs/release-notes/v*.md` (re-patch; "objects no longer pop in at the screen edges with
   Dolphin's widescreen hack"), and the mod in docs/api if the mod list is written down there.
9. **Land it** with the restamped blob in the same PR (CLAUDE.md: a PR touching `inject/` carries
   its own restamp), then recapture both start saves.

## 9. What #1186 (native widescreen) reuses

#1186's plan is `docs/design/native-widescreen.md`. It settled the points this section left open;
the short version:

- **The block and the mode field.** Mode 2 means "native": #1186 widens the camera's aspect at its
  source and leaves the clipper hook as a pass-through. One field, so the two widenings can't
  stack. The block grows to 0x18 bytes (6.1) before #1185 builds it.
- **The knob for mode 2 is the 4/3 literal at `0x803FA998`**, read only by `preparation`
  (`0x8017BE5C`). C writes `(4·factor)/3` in mode 2 and `0x3FAAAAAB` in modes 0 and 1.
  `g_HIO.mAspectRatio` (`0x803B9A08`) is not written: of its six readers, the stage-transition fade
  (`d_ovlp_fade2.cpp:54`) would shrink to 77% of the width with gaps at the sides, and the pause
  cloth would stop covering the screen. A hook on camera_draw's `bl C_MTXPerspective` at
  `0x8017C444` is rejected too: it leaves the clipper, the particle aspect and the cutscene aspect
  at 1.28. The literal knob widens `view->mAspect` (the projection, the clipper through
  `view_setup`, the particle aspect, cutscenes) and `mDoLib_project`, which fixes the sun, firefly,
  lantern and `pointInSight` cases from section 4. 2D drawn at a projected point (reticles, the
  enemy HP bar, the talk-box start, the baton sparkle, our name tags) also needs #1186's
  `ws_project_2d` once 2D is compressed.
- **Dolphin side:** the app runs mode 2 only with the hack off and the aspect forced (Force 16:9 or
  Custom). With the hack left on, the heuristic reads the widened game as anamorphic only at
  exactly 16:9, and only after its transition count (frames can pop); at any other factor the hack
  and the mode widen twice.
- **Prior art:** Dolphin ships a "$16:9 Widescreen" Gecko code for GZLE01
  (`Data/Sys/GameSettings/GZLE01.ini:348-452`, disabled by default). It is native widescreen in
  Gecko form: its line `043FA998 3FE38E39` (`:450`; the last line is `043FB78C C2F60000`) rewrites
  `preparation`'s 4/3 to 16/9, and the rest patches HUD and menu functions. Its code caves at
  `0x800037E0` and `0x80004038..0x800040F7` sit in zeroed padding in `gTRKInterruptVectorTable`,
  **not** in `CanaryTRK` (`0x8040CFB0..0x80410000`), and no repo hook site is near any of its
  sites. It must not run together with mode 2: Go checks the knob word and the user's GameSettings.
- **Camera framing:** `dCamera_c::mWindowAspectRatio` (viewport w/h, set in `preparation` at
  `0x8017BE84`, read only at `0x801659BC`) stays 4:3 in every mode: targets are framed inside
  ±38°, within the native ±44.6°; multiplying by F would give 50.7°.
- **The `cull-probe` command** and the live recipe in step 7 carry over unchanged.

## 10. Open questions and risks

- **Performance** of the wider frustum in dense scenes (Windfall at night, Forsaken Fortress, the
  sea with many LOD islands) is not measured yet, nor are the real-shadow slots (8) and the
  emitter pool (150): step 7.5.
- **Stub-only REL actors:** about 75 actor RELs are still `/* Nonmatching */` in the decomp
  (`npc_ko1`, `bj1`, `kk1`, `ob1`, `obj_tapestry`, `bigelf`, `obj_flame`, …), and no RELS.arc on
  disk was scanned. Any clipper call they make is fixed by the hook. A private `mDoLib_project`
  screen check in one of them would not be, but it would be cosmetic like section 4's.
- **Which pop-in the person notices most** (room BG joints, actors, or grass and trees) was not
  checked on their own scenes. The hook fixes all three, so it doesn't change the plan.
- **Main moves fast.** Addresses, the free T2 bytes, the +0xA6C slack and op 16 were checked
  against main at `735c8d7`. Re-check them when the build starts: `blob.go` `T2End`,
  `wwapi/mailbox/mod.go` `CanaryArenaHigh`, `wwops.h`'s op list, build.py's hook list.
- **Default on for 4:3 players:** decided in 6.1, revisited by the step 7.5 numbers.
- **Stretch and Custom users** need the CLI setting until the persistent app setting exists (6.2).
- **The screen-space gates** (sun glare, fireflies, lantern glow, picto-box ratio, reticles) stay
  4:3 under the hack by design. #1186's native mode fixes them (`native-widescreen.md`).
- **Dolphin versions:** the hack's maths was read in tag 2603. The PC's 2609a is assumed to be the
  same. The live check above was on the Mac's 2603-342.
