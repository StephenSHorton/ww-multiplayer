# Native widescreen: every 4:3 assumption and the build plan

**Status:** plan only, no code (card #1186, 2026-11-09). It builds on #1184's culling plan
(`docs/design/widescreen-culling.md`, "the culling plan" below), whose clipper hook, `WWWS` mailbox
block and `WWAP_OP_WS_SET` op card #1185 builds. Section 9 lists what this plan corrects in it.
Every address here was checked against the vanilla GZLE01 DOL and the decomp
(`~/projects/tww`) by a finder and then by an adversarial verifier, unless the row says
"unverified" or "live check". No byte count here was measured from compiled code: there is no
devkitPPC on the Mac, so the first real sizes come from the PC build of stage 1.

## 1. In short

- **The person's goal:** widescreen made by our mod, not by Dolphin's widescreen hack. The hack
  widens only the GPU projection: the HUD and menus stretch, and the game still culls a 4:3 frustum
  (the culling plan, section 3).
- **The only native route is anamorphic.** The GameCube renders into a 640-wide EFB, and Dolphin's
  EFB is 640 wide too, so the game keeps its 640x480 frame. Dolphin, with its hack **off** and its
  aspect forced to 16:9 (or Custom W:H), stretches that frame sideways by
  **F = output aspect ÷ (4/3)** (16:9 → 4/3). The mod then:
  1. widens the 3D aspect by F at its source (one data word, `0x803FA998`). The projection, the
     clipper, the particles, cutscenes and every on-screen check follow;
  2. compresses all game 2D about the screen centre by 1/F, so after Dolphin's stretch the HUD,
     menus and text have their true proportions (one hook on the four `bl J2DOrthoGraph::setOrtho`);
  3. widens what must cover the full screen (dim masks, backdrops, fades, wipes, black bars) back to
     the full frame, at screen load, by a table;
  4. moves HUD parts out to the real screen edges by S = 329.5·(F − 1) 2D units (110 at 16:9), by
     the same table.
- **Shared, cheap set-ups do most of it:** one data word for 3D, one 2D hook, one load-time screen
  hook with a table, one wrapper for 2D placed at projected 3D points (7 sites), and a few data
  words. Per-screen work is small: the picto box, the opening, THP movies, minigame HUD widgets.
- **Budget:** T2 has 0xBF8 bytes free on main. Stages 1-3 (world, 2D proportions, HUD edges) fit
  even at the high estimate (about 0x60 left); stages 4-5 fit only at the low end, with four
  levers ready (section 6). No runtime code-word rewrites, so no icache work.
- **Dolphin:** the app launches Dolphin with `-C Graphics.Settings.wideScreenHack=False -C
  Graphics.Settings.AspectRatio=1` (Force 16:9), or Custom W:H. Go puts the mod in native mode
  (mode 2) only when Dolphin's effective settings agree. The setting is *Widescreen: 16:9 / Custom
  W:H / Off*.
- **Stages,** each shippable and testable in Dolphin: (1) world and culling, with #1185, 2D still
  stretched as today; (2) 2D proportions, HUD inside the centre 4:3; (3) HUD at the edges;
  (4) menus and special screens polish, THP movies; (5) picto box proper. Releases: R1 = #1185 +
  stage 1, R2 = stages 2+3, R3 = stages 4+5. Each release is one re-patch for players.

## 2. The model and its numbers

**Frame.** `g_ntscZeldaIntDf` (`0x803716F0`, `m_Do_machine.cpp:393-418`): EFB 640x480, XFB 480
lines, VI width 660. `mDoGph_AfterOfDraw` copies `g_HIO.mFbWidth/mEfbHeight` (`0x803B9A28/2A`) into
the render mode every frame; never change them. Dolphin: `EFB_WIDTH` 640, `MAX_XFB_WIDTH` 720.

**Dolphin's stretch, hack off.** Force 16:9 draws the frame at its source aspect × (16/9)/(4/3);
Custom at × (W/H)/(4/3). Against the vanilla picture that is exactly F, so the mod never needs
Dolphin's VI aspect. Stretch and Custom (Stretch) are absolute and depend on the VI aspect (about
1.2534 for WW, computed from `VideoInterface.cpp`, live check owed): the app never uses them.

**The 2D box.** All game 2D draws through the Painter's `J2DOrthoGraph`. Source
`setOrtho(-9,-21,659,524)` is x, y, width, height, so the box is x −9..650, y −21..503 (`setPort`
adds 0.5 to the bottom). Centre x 320.5 lands on EFB px 320; half-width 329.5.

| | F | Compressed x box (320.5 ± 329.5F) | Edge shift S = 329.5(F − 1) | In EFB px, 320(1 − 1/F) |
|---|---|---|---|---|
| 16:9 | 4/3 | −118.833..759.833 (`0xC2EDAAAB`/`0x443DF555`) | 109.83 | 80 |
| 16:10 | 1.2 | −74.9..715.9 (`0xC295CCCD`/`0x4432F99A`) | 65.9 | 53.3 |
| 21:9 (7:3) | 1.75 | −256.125..897.125 | 247.13 | 137.1 |
| 64:27 (2560x1080) | 16/9 | −265.278..906.278 (`0xC384A38E`/`0x446291C7`) | 256.28 | 140 |
| 32:9 | 8/3 | −558.17..1199.17 | 549.17 | 200 |

At 16:9 the vanilla 2D frame lands on EFB 80..560, and each side band is one eighth of the output.
A HUD part moved by −S (left anchor) or +S (right anchor) keeps its vanilla edge margin. Dolphin's
own Gecko code uses a box of −123..767 (factor 1.3505, centre 322): about 1.3% too narrow. Never
copy its offsets.

## 3. Decisions

### 3.1 The 3D knob: the 4/3 literal at `0x803FA998`

`preparation()` (`d_camera.cpp:5315`) writes `view->mAspect` every frame as the 4/3 literal times
`g_HIO.mAspectRatio` (0.96): `lfs f1,-0x5368(r2)` at `0x8017BE5C`, `fmuls` at `0x8017BE6C`,
`stfs f2,0xD4(r31)` at `0x8017BE88`. The literal (`.sdata2`, DOL file offset `0x3A2D38`, vanilla
`0x3FAAAAAB`) has that one reader and no writer in the DOL or any of the 415 RELs on the disc.
Dolphin's "$16:9 Widescreen" Gecko code writes the same word.

**Mode 2 writes it; nothing writes `g_HIO.mAspectRatio`.** `g_HIO` (`0x803B9A08`) has six readers,
and three must not widen. The stage-transition fade `dOvlpFd2` redraws a capture of the whole EFB
on a ±320 quad at z = −420 under its own 60° perspective: at 1.28 it fills 103% of the width, at
1.7067 only 77%, so every transition would open on a squeezed frame with gaps. The pause cloth only
just covers a 4:3 view (inradius 2670 against corner radius 2699), so it would leave gaps. Hooking
`bl C_MTXPerspective` at `0x8017C444` instead was rejected too: it leaves the clipper, the particle
aspect and the cutscene aspect at 1.28.

**What follows the knob:** the main projection; the clipper (`ws_clip_aspect` passes the aspect
through in mode 2, so `mClipper+0x50` = `view->mAspect`; the two widenings never stack); 
`mProjViewMtx` and through it `mDoLib_project`; the 3D particle aspect (`0x8000B200`,
`0x8000B4F8`); cutscenes (`JSGGetProjectionAspect` returns `view->mAspect`). Every check that
compares a projected point with 0..640 or samples the EFB (`peekZ`, `GXPeekARGB`) becomes right by
itself, because EFB pixels are exactly the stretched screen. That fixes the culling plan's section-4
rows for the sun glare, fireflies and lantern glow, which the clipper hook alone cannot.

**What does not follow:** the fade (correct as it is), the pause cloth (still covers, weave
stretched by F: accepted), the file-select camera and cloth (section 4.1, stage 2), lock-on framing
`mWindowAspectRatio` (viewport w/h, stored at `0x8017BE84`, read only at `0x801659BC`; it stays
4:3 on purpose: targets are framed inside ±38°, within the new ±44.6°; ×F would give 50.7°), and
one unwidened frame per camera create in `init_phase2` (under the fade, harmless).

**Values.** C computes `knob = (4.0f * factor) / 3.0f` (one rounding, bit-equal to the aspect and to
the Gecko's word). `(4.0f/3.0f) * factor` is one ulp high. Modes 0 and 1 write `0x3FAAAAAB`
exactly. The handler always writes an absolute value, never a multiple of the current one.

| Output | factor | knob `0x803FA998` | `view->mAspect` = `mClipper+0x50` (`0x803A575C`) |
|---|---|---|---|
| 4:3, modes 0 and 1 | `0x3F800000` | `0x3FAAAAAB` | `0x3FA3D70A` (1.28) |
| 16:9 | `0x3FAAAAAB` | `0x3FE38E39` | `0x3FDA740D` (1.706667) |
| 16:10 | `0x3F99999A` | `0x3FCCCCCD` | `0x3FC49BA6` (1.536) |
| 21:9 (7:3) | `0x3FE00000` | `0x40155555` | `0x400F5C28` (2.24) |
| 64:27 | `0x3FE38E39` | `0x4017B426` | `0x4011A2B4` (2.275556) |
| 32:9 | `0x402AAAAB` | `0x40638E39` | `0x405A740D` (3.413333) |

(The culling plan's step 7.1 expects `0x3FDA740E` for its mode-1 clipper. That is the hook's own
`aspect × factor` product, a different computation; compute each value where it is used.)

### 3.2 2D compression: one hook on `J2DOrthoGraph::setOrtho`

`hook_branchlink` on all four `bl 0x802CDD14` (`setOrtho(const TBox2<f32>&, f32 far, f32 near)`).
No other DOL caller exists and the decomp has no REL caller (RELs were not binary-scanned for this
one; see section 10).

| Site | Caller | far / near |
|---|---|---|
| `0x8000B06C` | `mDoGph_Painter`, before 3D (`m_Do_graphic.cpp:1573`) | −1 / 1 |
| `0x8000BA30` | `mDoGph_Painter`, the main 2D phase (`:1890`) | 100000 / −100000 |
| `0x80223CF4` | `dOvlpFd2_dlst_c::draw` re-setting the shared graf from its own constants `0x803FB734..40` (`d_ovlp_fade2.cpp:103`) | −1 / 1 |
| `0x8003EADC` | `drawDvdCondition`, the disc-error screen (`f_pc_manager.cpp:201`) | −1 / 1 |

At each `bl`: r3 = graf, r4 = a stack `TBox2 {x0, y0, x1, y1}`, f1 = far, f2 = near. `setOrtho` only
stores the box (+0xBC..+0xC8), −near (+0xCC) and −far (+0xD0), so the hook is plain C in `hooks.c`:

```c
typedef struct { f32 x0, y0, x1, y1; } WsBox;
typedef void (*setortho_fn)(void* graf, const WsBox* box, f32 far_, f32 near_);
#define J2DOrthoGraph_setOrtho ((setortho_fn)0x802CDD14)

void ws_ortho_hook(void* graf, const WsBox* box, f32 far_, f32 near_) {
    WsBox b = *box;
    f32 F = ws_2d_factor();               /* section 5: the latched 2D factor, 1.0f unless compressed */
    if (F != 1.0f) {
        f32 c = 0.5f * (b.x0 + b.x1);
        b.x0 = c + (b.x0 - c) * F;
        b.x1 = c + (b.x1 - c) * F;
    }
    J2DOrthoGraph_setOrtho(graf, &b, far_, near_);
}
```

All four sites go in together: without Fd2's, its reset puts the vanilla box back on the shared
graf for every `setPort` after it in that frame. `setPort` (`0x802CDCB4`) and `scissorBounds`
(`0x802CDD84`, pane clipping) both read the hooked box, so clipping matches drawing. y is untouched.

Rejected:
- **The Gecko's data rewrite** of `0x803F7D68`/`0x803F7D6C`. `setUpRectangle` (`0x8000A530`, the
  picto box freeze-frame) reads the same words and would be squashed; it misses Fd2's private copy
  and the disc-error box (whose −9 at `0x803F8148` is shared with `messageSet`).
- **`C_MTXOrtho` inside `setPort`.** Pane clipping would stop matching, and it would also compress
  the grafs that must stay full width (`J2DScreen::draw(NULL)`, the fader).

Grafs built by the `J2DOrthoGraph` constructor (box 0..w × 0..h) are not reached: `J2DScreen::draw`
with a NULL graf, `JFWDisplay::endGX`'s fader, `JUTDbPrint`, `JUTConsole`. They stay full width,
stretched. The ~14 passes with their own projection over the full EFB (fades, blur, depth of field
and the cutscene bars, the alpha and spot passes, the pause and game-over captures, `JUTFader`,
`clearEfb`, the shadow render target) never call `setOrtho` and need nothing.

**Dolphin's heuristic** (only consulted under Auto, which the app never uses): the vanilla ortho
votes 0.942 (normal); compressed at 16:9 it votes (659F/524.5)/(4/3) = 1.256, within the 0.11 slop
of 1.333 (anamorphic).

### 3.3 J2D screens: one load-time hook and one table

Every `.blo` screen loads through `J2DScreen::set(const char* name, JKRArchive*)` (`0x802D0944`,
size 0xE8): 57 DOL callers and one REL caller (`d_a_title.rel`). Its `bl 0x802D0CE0` at
`0x802D09DC` is the only call of `set(JSURandomInputStream*)` anywhere, but the name is gone by
then: it only passes through r3 to `getGlbResource` at `0x802D0968`. So two `hook_branchlink`s, in
core T2 (the title, file select and the first `dMeter_Create` run before the REL loads):

- **Hook A, `0x802D0968`:** stash the name pointer (one 4-byte static), then call
  `JKRFileLoader::getGlbResource` (`0x802B68F0`).
- **Hook B, `0x802D09DC`:** call `0x802D0CE0`. If it returned true and the stashed name hashes to a
  table row, apply the row. Always clear the stash.

Every menu calls `set` before `fopMsgM_setPaneData`, and `fopMsgM_pane_parts_set` copies each
pane's bounds into its `Orig` fields after `set`. So a change made in hook B is what every later
`paneTrans`/`cposMove` starts from: per-frame layout code needs no other change.

**How a pane is changed (never by a bare `mBounds` write).** Only the parentless J2DScreen rebuilds
its matrix from `mBounds` at draw time (`J2DPane.cpp:180-183`); children use `mMtx`, which only
`calcMtx` builds.

- **Shift** (anchor L or R): `J2DPane::add(p, dx, 0)` (`0x802D05C8`: virtual `move`, then
  `calcMtx`). Works for static panes and for fopMsgM-managed panes (their `Orig` is copied after).
- **Widen** (GEOM, EDGE): set `mBounds.i.x`/`f.x`, then `J2DPane::setBasePosition(p,
  p->m2DBasePosition)` (`0x802D0800`, which calls `calcMtx`).
- **Scissored screens** (clip-to-parent, `J2DScreen+0xCC`: `time_swim_1`, `auction1`, MSG2): every
  pane is clipped to its parent inside the screen's 0..640 box, so a child pushed past it vanishes.
  Move the J2DScreen itself (`add(scrn, ±S, 0)`) so its clip box moves too. Never GEOM or EDGE there.
- **Placed by code** (the `ship_race0` timer's rupee icon, the enemy HP bar, `wiy1`): no row; fixed
  at the source (data words or `ws_project_2d`).

**Walk.** The anchor parent is the direct child tagged `'ROOT'` (`0x524F4F54`), or the screen if
there is none. Its children take their tag rule, else the row's kind. Shifted panes are not walked
into. GEOM (a childless `PIC1` whose absolute box covers 0..640 × 0..480) and EDGE match at any
depth, on absolute coordinates: EDGE moves only edges at or past the 4:3 box, x' = 320.5 + (x −
320.5)F. No row combines a shift with GEOM or EDGE. J2DPane fields: +0x04 magic (`'PIC1'`,
`'PAN1'`), +0x08 tag, +0x0C `mBounds {i.x, i.y, f.x, f.y}`, +0xA9 `m2DBasePosition`, +0xB0 child
tree (child link: object +0, next +0xC).

**Table** (T2 rodata; generated by a Go tool from a list of names, tags and actions, with a test
that hashes every `.blo` literal in the decomp and fails on a collision):

- Row `{u32 hash; u8 flags; u8 n; u8 first; u8 pad}` (8 bytes). `flags`: kind in bits 0-1 (NONE,
  ALL_L, ALL_R, BY_CX: centre x < 320.5 → L, else R), GEOM 0x04, SCR_L 0x08, SCR_R 0x10, EPOCH
  0x20. Rule: `u32 tag` + `u8 action` (NONE, L, R, EDGE), 5 bytes.
- Hash: `h = 5381; for each c: h = h*33 ^ (c | 0x20)`. A name not in the table gets no change.
- L, R, ALL_*, BY_CX and SCR_* act only when the latched HUD shift is non-zero (section 5);
  GEOM and EDGE act whenever 2D is compressed.

| Screen (`.blo`) | Row | Stage |
|---|---|---|
| `main_parts1` | BY_CX, EPOCH; NONE for `ben1-4 enem enm2 bey1 bey2 enek ene1-3 l r wiy1` | EPOCH 2, the rest 3 |
| `main_parts2` / `main_parts3` | ALL_R / ALL_L | 3 |
| `time_swim_1` / `time_swim_2` | SCR_R / ALL_R | 3 |
| `place_name` | `pn` L (screenshot first: the dumps did not include it) | 3 |
| `auction1` / `auction2` | SCR_L / `icon ip00-03 gd00-05` L (`met1/met2` stay centred) | 3 |
| `menu_save` | GEOM; `wp04-07` EDGE | 2 |
| `wipe_00`, `wipe_00_2` (telescope) | `wp04-07` EDGE | 2 |
| `menu_collect_01`, `menu_item_02`, `menu_explanation`, `menu_map_d`, `option`, `f_map`, `f_hikaku_1`, `g_map_01`, `t_map_01`, `DOC_map_01`, `HRT_map_1`, `HM_map_1`, `IR_map_1`, `MN_map_1`, `SENSUI_map_1`, `TERRY_map_1`, `TR_map_1`, `YG_map_1`, `YS_map_1`, `gameover`, `operate_wind`, `operate_wind_mask` | GEOM | 2 |
| `main_parts_fileselect` | EPOCH (stage 2); ALL_R (stage 3) | 2, 3 |

About 0x1B0 bytes of data and 0x1C0 of code. A global GEOM rule (every screen) would drop the 22
GEOM-only rows (−0xB0), if a stage-2 screenshot sweep shows no false hits.

**Mode changes and the latch.** A row is applied once, at load, and the game's `Orig` copies can't
be reached again. So every 2D reader uses a *latched* mode and factor that change only at boot and
when an EPOCH screen loads: `main_parts1` (every stage load, `d_stage.cpp:1496`) and
`main_parts_fileselect` (when file select starts). A `WS_SET` changes the 3D at once; 2D follows at
the next stage load or file select. Without the latch, leaving mode 2 mid-stage would push the
hearts to x = −86, off screen.

### 3.4 2D placed at a projected point: `ws_project_2d`

`mDoLib_project` (`0x80017748`, `void (Vec* src, Vec* dst)`, returns EFB px) follows the widened
aspect. Code that draws 2D at the result uses the EFB number as a 2D x, which after compression
lands 1/F toward the centre. Never hook `mDoLib_project` itself: half its callers need EFB px.

Vanilla draws at u = p (the EFB number as a 2D unit, about 3% inward of the true point). Keeping that
landing pixel under compression needs **u′ = 320.5 + (p − 320.5)·F**. (A pivot of 320 is off by
0.5(F − 1) units; the exact-point variant 320.5 + (p − 320)·F·659/640 would move reticles from
where players know them. If screenshots at 21:9 or 32:9 show reticles off target, switch to it: one
constant.)

```c
void ws_project_2d(Vec* src, Vec* dst) {
    mDoLib_project(src, dst);                        /* 0x80017748 */
    f32 F = ws_2d_factor();
    if (F != 1.0f) dst->x = 320.5f + (dst->x - 320.5f) * F;
}
```

33 `bl 0x80017748` in 16 DOL functions, four REL relocations, and our REL:

| `bl` site | Function | Point used for | Wrap | Stage |
|---|---|---|---|---|
| `0x800E1624` | `daBoomerang_sightPacket_c::setSight` | boomerang lock reticles | yes | 2 |
| `0x80129550` | `daPy_sightPacket_c::setSight` | Link's aim reticle (bow, hookshot, rope, cannon; the Gecko misses it) | yes | 2 |
| `0x801F8104` | `dMeter_zakoEnemyMove` | enemy HP bar beside a Z-locked enemy | yes | 2 |
| `0x801F9910` | `dMeter_flyGaugeMove` | magic bar flying to Link's head | yes | 2 |
| `0x8020DF5C` | `dMsg_ScreenDataValueInitTalk` | talk box's opening start point | yes | 2 |
| `0x802229D0`, `0x80222ADC` | `dMetronome_c::melodyShow` | baton-tip sparkle (wrapped before its −320) | yes | 2 |
| `0x8008C6C0` | `dKyr_lenzflare_move` | flare streak angle | optional | 4 |
| `0x800994AC`, `0x80099A6C` | `dKyr_drawStar` | moon-star exclusion (both or neither) | optional | 4 |
| `0x8019744C` | `dKy_telescope_lookin_chk` | radius-100 test round (320,240) | optional | 5 |
| `0x8020F518` | `dMsg_mesgOutPos` | only y used; x goes to `talkPosX`, never read | no | — |
| `0x8008CAA4` | `dKyr_sun_move` | bounds + `peekZ` (its falloff stays ×F: accepted) | no | — |
| `0x8009AD38`, `0x8009B0C4` | `drawVrkumo` | cloud bounds | no | — |
| `0x801657A4`, `0x8017B77C`, `0x800BC910` | `pointInSight`, `shakeCamera`, `possessedEvCamera` | viewport fractions | no | — |
| 15 sites `0x800CD1B4`..`0x800CD63C` | `dSnap_Obj::CalcArea2D` | picto area against the EFB pixel count | no | — |
| RELS.arc `d_a_ff` sec1+0x314, `d_a_kantera` +0x88C | glow | bounds + `peekZ` | no | — |
| loose `/rels/d_a_npc_ls1.rel` +0x302C, +0x31D4 | Aryll's telescope | distance from (319.5,186.5): up to F× more lenient | no (the patcher can't rewrite loose RELs) | — |

### 3.5 What covers the full screen, and how

| Thing | Handled by | Stage |
|---|---|---|
| Fades with their own projection, blur, DOF, letterbox bars, pause/game-over captures, `JUTFader` | nothing: full-EFB already | — |
| `dOvlpFd2` capture quad | nothing (3D knob leaves it at 1.28); its `setOrtho` is in 3.2 | 2 |
| Cutscene circle wipe `0x803F89B8..C4` (−9, −21, 659, 524) | data in `ws_apply`: x = 320.5 − 329.5F, y = 241 − 262F, w = 659F, h = 524F (both axes, so the iris stays round; 16:9: `C2EDAAAB C2D8AAAB 445BAAAB 442EAAAB`) | 2 |
| OVERLAP4/5 blit (`dDlst_2Dt_Sp_c`) | data: `0x803FB78C` = 320.5 − 329.5F (`0xC2EDAAAB`), `0x803FB77C` = 1.0296875F (`0x3FAFBBBC`); vanilla `C1100000`/`3F83CCCD` | 2 |
| Pause, map, save, option dims and backdrops; sea chart `cl`, `ms01/ms02`, `HK00/HK01`; game over `blak`; Wind's Requiem `mask` | GEOM rows (widened, not removed as the Gecko does) | 2 |
| Save iris bars `wp04-07`; telescope bars `wp04-07` | EDGE rows | 2 |
| Opening storybook (660-wide art, `mak1/mak2` bars, `d42` waiting at 651..1311) | compressed art + black side fills after `bl J2DScreen::draw` at `0x8023452C` (`ws_fill_sides`: `J2DFillBox` `0x802CDF3C` at [L′, −21 .. −9] and [650 .. R′]) | 2 |
| Picto viewfinder (`wipe_01_01`, `wipe_01_02`) and shutter lines | drawn uncompressed (section 3.6) | 2 |
| THP movies (title loop, staff roll) | pillarboxed by a T2 frame step (section 4.5) | 4 |

The Gecko's `li r4,0` (NULL graf) trick is not used anywhere: it draws with a 0..640 box (not the
vanilla −9..650) and leaves that projection current.

### 3.6 The picto box: recommendation and the decision it needs

The capture copies a fixed EFB rectangle (304x208 at 168..472 × 76..284, 2:1 to a 152x104 card),
the subject judge counts EFB pixels inside a fixed rectangle (320 ± 147), and the photo viewers have
private orthos. EFB copies scale only 1:1 or 2:1.

**Recommended (option A): photos are anamorphic, the viewfinder is drawn uncompressed.**
- Stage 1: in mode 2 subjects cover 1/F as many EFB px, so quest and figurine photos would get
  harder. `hook_branchlink` at `0x800CE5E8` (`ws_snap_setresult`: multiply each
  `mCapturedPixels` by F, then call `SetResult`). Ratios are untouched. Uses the live mode, not the
  2D latch.
- Stage 2: `ws_screen_draw_full` (save the graf box at +0xBC, set −9..650, `setPort`, draw, restore,
  `setPort`) at the four `bl J2DScreen::draw` sites `0x8022B510`, `0x8022B538`, `0x8022B778`,
  `0x8022B9B8`, and a second entry around the two `shutterLineDraw` calls `0x8022B524`,
  `0x8022B9CC`. The frame then matches the capture rectangle (what you see is what you get).
  `hukidashi_08` (`0x8022B54C`, `0x8022B78C`) stays compressed. This is today's look, so stage 2
  does not regress it. Also stage 2: the album's private ortho (`pictureDraw`, box `0x803FB81C..28`,
  `bl C_MTXOrtho` at `0x80227BB8`) widened about 320.5 by F, or the album slots drift by
  (x − 320)(1 − 1/F).
- Stage 5: photos taken in mode 2 hold F× the world width and look squeezed in the album (the Gecko
  look). Optionally crop in the album/freeze-frame draws with a texture matrix
  s′ = 0.5 + (s − 0.5)/F (loses a quarter of the horizontal resolution).

Alternatives for the person: **B**, true 4:3 photos by a software resample in `mCaptureProc`
(`0x8000AAC4`, ~0x150 bytes, could live in the REL) plus a compressed viewfinder; **C**, drop the
3D back to 4:3 while aiming (per-frame knob write; the world visibly narrows in the viewfinder).
Never rewrite the judge immediates at runtime (icache) or as a patch-time DOL patch (BlobHash can't
see it).

### 3.7 Our own drawing

- **Name tags** (`inject/rel/visuals.c`): one `xs` drives the tag's glyph width;
  `WWVIS_FLAGS_XSCALE` is a q16 fraction below 1 and cannot carry F. In stage 2 the REL reads the
  WWWS block with the same `ws_2d_factor` rule. When F ≠ 1 it culls `screen.x` against −80..720 (EFB
  px is the whole screen), sets `tag->x = 320.5 + (screen.x − 320.5)·F` and `tag->xs = 1`. No
  `VisTag` struct change (`tag->x` is already the position). Otherwise today's XSCALE path. Stage 2
  includes `wwws.h` in the REL (new RELBuildID; the culling plan kept it out only for #1185).
- **Lobby line** "Here: A, B" (file select): stays at x 320; `xs = 1` when F ≠ 1. Stage 2.
- **Go's sampler** (`mods/multiplayer/feed/widescreen.go` `xscaleSampler`): writes XScale 0 (= 1)
  whenever the WWWS mode is 2 (the hack is off then anyway). Stage 1.
- **Sea chart markers** (`seachart_shim` → `wwvis_seachart_draw`): read the chart panes each frame
  and draw with the chart's port, so they follow. Nothing.
- **3D visuals** (mini-Links, equipment, boats, projectile and enemy copies, the title lobby row):
  through the game's projection; nothing. Optional stage 4: scale the title lobby row's spacing by F
  (`mods/multiplayer/feed/lobby.go`).

## 4. The inventory: every 4:3 / 640x480 assumption

Kind: **shared** (one set-up covers many screens: cheap), **screen** (one screen or class),
**element** (one HUD part or call site). Anchor: where the part belongs on a wide screen.
Stage 0 means Go only; "—" means no change (with the reason). Decomp paths are under
`~/projects/tww/src`.

### 4.1 3D

| # | What assumes 4:3 | Where | DOL | Kind | Fix | Stage |
|---|---|---|---|---|---|---|
| 3D-1 | 4/3 literal in `view->mAspect` | `d_camera.cpp:5315` | data `0x803FA998`, read `0x8017BE5C` | shared | WS_SET writes (4F)/3 in mode 2, `0x3FAAAAAB` otherwise (3.1) | 1 |
| 3D-2 | GPU projection, clipper, `mProjViewMtx` | `d_camera.cpp:5463, 5347, 5492` | `bl C_MTXPerspective` `0x8017C444`; clipper `bl` `0x8017BF90` (#1185's hook) | shared | follow 3D-1; `ws_clip_aspect` passes through in mode 2 | 1 |
| 3D-3 | 3D particle aspect | `m_Do_graphic.cpp:1602, 1671` | `lfs` `0x8000B200`, `0x8000B4F8` | shared | follows | 1 |
| 3D-4 | Cutscene camera aspect | `JSGGetProjectionAspect` | — | shared | follows (a demo's own aspect is ignored) | 1 |
| 3D-5 | Checks on EFB px: sun glare + `peekZ`, clouds, fireflies, lantern, `pointInSight`, `shakeCamera`, `possessedEvCamera`, picto `CalcArea2D` | `d_kankyo_rain.cpp:602`, `d_a_ff.cpp:40-73`, `d_a_kantera.cpp:146-155`, `d_snap.cpp:1514-1609` | see 3.4's "no" rows | shared | right by themselves in mode 2; never wrap | 1 |
| 3D-6 | Distance from centre in EFB px: flare angle, moon-star exclusion, telescope look-in | `d_kankyo_rain.cpp`, `d_kankyo.cpp` | `0x8008C6C0`; `0x800994AC`+`0x80099A6C`; `0x8019744C` | element | optional `ws_project_2d` | 4, 5 |
| 3D-7 | Aryll's telescope checks | `d_a_npc_ls1.cpp` (loose REL) | sec1+0x302C, +0x31D4 | element | accepted: only more lenient | — |
| 3D-8 | Lock-on framing `mWindowAspectRatio` | `d_camera.cpp` | stored `0x8017BE84`, read `0x801659BC` | shared | stays 4:3 on purpose (3.1) | — |
| 3D-9 | `g_HIO.mAspectRatio` (0.96) | `f_ap_game.cpp:34` | `0x803B9A08`; readers `0x8017BE68 0x8017C920 0x801DF9F4 0x80223A00 0x80230034 0x80232498` | shared | never written (3.1) | — |
| 3D-10 | `dOvlpFd2` capture quad, own 60° perspective | `d_ovlp_fade2.cpp:20-102` | `bl C_MTXPerspective` `0x80223A10` | screen | none: stays 1.28 and fills the EFB | — |
| 3D-11 | Pause cloth (`dMCloth`), own 30° perspective | `d_menu_capture.inc:79-93` | `0x801DFA04` | screen | accepted: still covers, weave ×F. Optional polish: `ws_persp` here plus a uniform cloth scale (`0x803E5EC0/C4`, `0x803E5F70/74` × k(F), k(16:9) ≈ 1.218, unverified) | — (4) |
| 3D-12 | File-select camera (aspect latched at create) and file-select cloth panel | `d_s_name.cpp:195-205, 610-622, 1538-1545` | `bl C_MTXPerspective` `0x802306A0` (setView, every frame) and `0x802324A8` (cloth) | screen | `ws_persp(m,fovy,aspect,n,f)` = `C_MTXPerspective` (`0x8030DB78`) with aspect × `ws_2d_factor()`; without it the ~90% cloth panel runs to ~120% under the centred panels | 2 |
| 3D-13 | Sky, sun/moon sprites, flare billboards, focus lines, DOF, rain/snow | `d_kankyo*.cpp` | — | shared | follow; focus lines may thin in the bands (accepted) | — |

### 4.2 2D foundation, fades and EFB

| # | What | Where | DOL | Kind | Fix | Stage |
|---|---|---|---|---|---|---|
| 2D-1 | Painter graf box −9..650 (all game 2D) | `m_Do_graphic.cpp:1572-1576, 1890-1891` | `bl setOrtho` `0x8000B06C`, `0x8000BA30` | shared | `ws_ortho_hook` (3.2) | 2 |
| 2D-2 | Fd2 re-sets the shared graf to the vanilla box | `d_ovlp_fade2.cpp:103` | `bl` `0x80223CF4` | shared | same hook | 2 |
| 2D-3 | Disc-error screen box | `f_pc_manager.cpp:201` | `bl` `0x8003EADC` | shared | same hook | 2 |
| 2D-4 | Painter box words shared with `setUpRectangle` | `m_Do_graphic.cpp` | `0x803F7D60..6C`; `0x8000A530` | — | constraint: never rewrite them | — |
| 2D-5 | Cutscene wipe rect | `d_drawlist.cpp` (`mWipeDlst`) | data `0x803F89B8..C4` | element | `ws_apply` (3.5) | 2 |
| 2D-6 | OVERLAP4/5 blit maths | `d_ovlp_fade4.cpp` | data `0x803FB78C`, `0x803FB77C` | element | `ws_apply` (3.5) | 2 |
| 2D-7 | Minimap icon scissor: 2D → EFB with the vanilla box, width 116 px | `d_map.cpp` `calcScissor` | `0x8004A3A4`; its only caller `bl` `0x8004A64C`; x consts `0x803F8234/38/3C`; `li r3,116` `0x8004A428`; result X/W stored at `0x803F6AE4`/`0x803F6AE8` (verify by disassembly at build) | element | `hook_branchlink` `0x8004A64C`: call, then if F ≠ 1 recompute X = (int)((map x − L′)·640/(659F)), W = (int)(116/F + 0.5) (87 at 16:9), vanilla clamps. It follows stage 3's map move by itself | 2 |
| 2D-8 | Full-EFB passes (3.2) and the shadow render target | `m_Do_graphic.cpp`, `d_drawlist.cpp` | — | shared | none | — |
| 2D-9 | Render mode, EFB/XFB 640x480, viewport/scissor resets | `m_Do_machine.cpp:393-444` | `0x803716F0` | shared | none; never write `g_HIO+0x2C/+0x2E` | — |
| 2D-10 | 2D particles and the 2D J3D list | `m_Do_graphic.cpp:1894-1922` | 2D draw-info 4/3 `0x803F7DA4` read at `0x8000BA70` | shared | follow the hooked graf; the 4/3 only feeds projected texgen | — |
| 2D-11 | `dDlst_2Dtact_c` / `dDlst_2DCopy_c` EFB copies | `d_msg.cpp`, `d_message.cpp` | — | — | dead in USA, never drawn | — |
| 2D-12 | GX points and lines sized in px (minimap dots) | `d_map.cpp` | — | element | accepted (×F wide) | — |
| 2D-13 | Clip-to-parent screens | `J2DScreen+0xCC` | — | rule | move the screen, never widen a child (3.3) | 2, 3 |
| 2D-14 | Cinema/vista trims 52/65 (16:9 vista ≈ 2.27:1) | `d_camera.cpp` | data `0x803F90F0`, `0x803F90F4` | element | optional smaller trims in mode 2 | 4 |
| 2D-15 | Scope/picto opening: the 3D view rect follows the 2D wipe scale in EFB px | `d_scope.cpp`, `d_camera.cpp` | — | element | only if visible: scale the `setView` x args by 1/F | 5 |

### 4.3 HUD (in-game)

Anchor rule: a part whose vanilla centre is left of 320.5 moves −S, right of it +S, unless it is
centred by design (boss bar, lock-on bar, prompts) or follows something else (the wind arrow follows
the minimap). S = 329.5(F − 1) keeps each part's vanilla distance from its edge. Positions come from
the vanilla `.blo` files parsed from the disc.

| # | What | Where | DOL / data | Kind | Anchor | Fix | Stage |
|---|---|---|---|---|---|---|---|
| H-1 | Hearts `ht00-19 hk00-19 htfl`, magic `mbb* mbr* mbk*`, compass `cmb1`, D-pad `crs1 ylef yup yrig` | `d_meter.cpp`, `main_parts1.blo` | row | shared | left | `main_parts1` BY_CX | 3 |
| H-2 | Buttons A/B/X/Y/Z/R, counts, action plates, keys `kyl1/2 nm03/04/08/09 key0` | `main_parts1.blo` | row | shared | right | BY_CX (centre x > 320.5) | 3 |
| H-3 | Centred in `main_parts1`: boss bar `ben1-4 enem enm2 bey1 bey2`, lock-on bar `enek ene1-3`, pause hints `l r`, wind arrow `wiy1` | `main_parts1.blo` | rule NONE | element | centre / follows map | exceptions (`ben2` has centre x 342 and would go right) | 3 |
| H-4 | Rupee counter | `main_parts2.blo` | row | shared | right | ALL_R; its sparkle (pane centre − 320, also `rupyAlpha`'s respawns) follows | 3 |
| H-5 | Clock and its sun/moon/cloud rects | `main_parts3.blo` | row | shared | left | ALL_L; `dDlst_2Dm_c` rects come from `cmtm`'s Orig | 3 |
| H-6 | Swim/air meter `msk1 main soul kg00` (scissored) and `wk00 hk11-33` + shine | `time_swim_1/2.blo` | rows | shared | right | SCR_R / ALL_R | 3 |
| H-7 | Minimap corner, free-camera icon | `d_meter.cpp:7657-7743` | `g_meter_mapHIO` `0x803E68DC`: +0x8 (`0x803E68E4`, 35), +0x14 (`0x803E68F0`, 590) | element | left / right | `ws_apply`: 35 − S, 590 + S | 3 |
| H-8 | Minimap hide-slide target: the hidden map shows in the widened left band | `d_meter.cpp:7733-7743` | +0xC (`0x803E68E8`, −180) | element | left | `ws_apply`: −180 − 329.5(F − 1) (from the 2D factor, not the HUD flag) | 2 |
| H-9 | Minimap scissor | 2D-7 | | element | follows map | 2D-7 | 2 |
| H-10 | Place-name card `pn` | `place_name.blo` | rule | element | left | `pn` L | 3 |
| H-11 | Minigame timer's rupee/item icon at x = 32 (`setPaneInitialPos` overwrites Orig, so no row) | `d_timer.cpp:535-555, 651, 681` | `g_menuHIO+0x9A` `0x803E6B06` (s16, 0) | element | left | `ws_apply`: −S | 3 |
| H-12 | Cannon game: 10 bomb icons x = 30, 5 ship icons x = 540, angle/shot battery | `d_a_canon.cpp`, `d_2dnumber.cpp` | vtable slots `0x80389100` (`dDlst_2DObject_c::draw` `0x800C946C`), `0x80389110` (`dDlst_2DBattery_c::draw` `0x800C90F0`) | element | left / right | REL writes the slots to wrappers (add ±S to the stored x, call, restore) and restores them in `_epilog` | 3 |
| H-13 | Sploosh Kaboom score/shot numbers | `d_a_mgameboard` (stub REL) | — | element | unknown | screenshot first; same wrapper kind if edge-placed | 3 |
| H-14 | 2D at projected points: boomerang sights, aim reticle, enemy HP bar, fly gauge | 3.4 | 3.4 | element | follows point | `ws_project_2d` | 2 |
| H-15 | Telescope bars (world shows beside the scope once 2D is compressed) | `d_scope.cpp`, `wipe_00/_2.blo` | rows | screen | full | EDGE; `dScp_wipeMove` sizes `wp06/07` from `mSizeOrig`, so widened bars follow the iris. Fallback: fill outside the live `wp06/wp07` edges from a hook at `bl outFontDraw` `0x802375CC` | 2 |
| H-16 | Wind's Requiem dim mask | `operate_wind_mask.blo` | row | screen | full | GEOM (fallback `ws_screen_draw_full` at `0x8021E6EC`) | 2 |
| H-17 | Auction: rolling bid digits (scissored), rupee icon and plates top-left, gauge centred | `d_auction_screen.cpp`, `auction1/2.blo` | rows | screen | left / centre | `auction1` SCR_L; `auction2` tags L | 3 |
| H-18 | Centred by design: boss HP, choice arrows, baton beat strip, wind ring, 3-2-1-GO, timer bar, Z-target (a 3D model), item get | various | — | — | centre | none | — |

Do **not** touch `g_meterHIO+0x50` or `+0x9C` (the Gecko's button and D-pad offsets): with the
pre-shift they would shift twice. Never write shared float constants the Gecko reads for its
offsets (`0x803F92CC` 114.0 is also a camera constant; `0x803F8DAC`, `0x803F8B74`, `0x803FB334`,
`0x803F85A4` all have unrelated readers).

### 4.4 Menus and scenes

All of these draw through the Painter's graf (`dComIfGp_getCurrentGrafPort()->setPort();
scrn->draw(0,0,graf)`), so 2D-1 compresses them with no per-screen hook. "Centred 4:3 with a
full-width backdrop" is the target look.

| # | Screen | What needs work | Fix | Stage |
|---|---|---|---|---|
| M-1 | Item, Quest Status, dungeon map (`d_menu_item/collect/dmap.cpp`) | full-screen dims `msk0`, `blak`, `.msk` | GEOM rows; notes sliding in show briefly in a band (accepted) | 2 |
| M-2 | Sea chart and chart views (`d_menu_fmap.cpp`, `d_menu_fmap2.cpp`) | `cl` backdrop (slides up from Orig with x = 0), `wts1`, `ms01/ms02`, `HK00/HK01` | GEOM rows | 2 |
| M-3 | Save (`d_menu_save.cpp:1750-1797`) | `msk0` dim; iris `wp00-07` | GEOM + `wp04-07` EDGE (live look; fallback NOP `0x801DAA38`) | 2 |
| M-4 | Options | `blak` | GEOM | 2 |
| M-5 | Game over (`d_gameover.cpp`) | capture (no change), `blak` | GEOM (letters stay compressed) | 2 |
| M-6 | Pause and game-over capture blits | full-viewport 0..1 quads | none | — |
| M-7 | Pause cloth | 3D-11 | — | — |
| M-8 | File select 3D (camera and cloth) | 3D-12 | `ws_persp` | 2 |
| M-9 | File select 2D | panels centred; A/B button cluster at 465..617 × 18..118 | `main_parts_fileselect` ALL_R | 3 |
| M-10 | Name entry, memory-card errors, boot logos | centred | none | — |
| M-11 | Title (`d_a_title`, REL actor) | the ship shows earlier in the bands | accepted | — |
| M-12 | Opening storybook (`d_s_open.cpp`, `Opening.blo`) | `d42` spills into the right band; bars stop at 4:3 | side fills at `0x8023452C` (3.5) | 2 |
| M-13 | Auction | H-17 | | 3 |
| M-14 | 2D menu particles (sparkles, title smoke) | emitted at pane − (320,240) | follow the graf | — |

### 4.5 Text boxes and special screens

| # | What | Where | DOL | Kind | Fix | Stage |
|---|---|---|---|---|---|---|
| T-1 | Text boxes, item-get box, arrows, button icons, song boxes, MSG2 board (scissored through `scissorBounds`), MSG3 paper, 3-2-1 text | `d_msg.cpp`, `d_message.cpp` | — | shared | follow 2D-1; text is about 25% softer sideways (cosmetic) | 2 |
| T-2 | Talk box grows from the speaker's projected x | `d_msg.cpp:739` | `0x8020DF5C` | element | `ws_project_2d` | 2 |
| T-3 | Baton-tip sparkle | `d_metronome.cpp` | `0x802229D0`, `0x80222ADC` | element | `ws_project_2d` | 2 |
| T-4 | Picto judge: absolute EFB pixel counts | `d_snap.cpp` | `bl SetResult` `0x800CE5E8` | element | ×F (3.6) | 1 |
| T-5 | Picto viewfinder and shutter lines | `d_picture_box.cpp` | 3.6 | screen | uncompressed draw | 2 |
| T-6 | Picto album private ortho | `d_picture_box.cpp:710-781` | `0x803FB81C..28`, `bl` `0x80227BB8` | screen | widen about 320.5 by F | 2 |
| T-7 | Picto capture rect and freeze-frame | `m_Do_graphic.cpp` (`mDoGph_screenCapture`, `setUpRectangle`) | `0x8000AAC4`, `0x8000A530`, `bl C_MTXOrtho` `0x8000A6C0` | screen | option A: none (anamorphic photos); B/C in 3.6 | 5 |
| T-8 | Photo browse/get views | `d_picture_box.cpp` | `0x80227A30` | screen | option A: none; B: x += w(1 − 1/F)/2, w /= F | 5 |
| T-9 | THP movies `title_loop.thp` (title screen) and `end_st_epilogue.thp` (the staff roll), 640x480 with their own ortho | `d_a_movie_player.cpp:4206` (loose Yaz0 REL, loaded before ours) | REL `.bss` + `0xCB580` (xSize), + `0xCB59C` (DrawPosX), from the disc map, **unverified** | screen | T2 frame step: find module 415's `.bss` in the OS module list; in mode 2, if xSize == 640, write w = even(640/F) and DrawPosX = (640 − w)/2; restore 640/0 otherwise | 4 |
| T-10 | Game over, Wind's Requiem, opening | 4.4, H-16 | | | | 2 |
| T-11 | Tingle Tuner TV side, JUT debug console | | | | none | — |

### 4.6 Our own drawing

See 3.7: name tags and lobby line (REL, stage 2), the sampler (Go, stage 1), sea chart markers and
3D visuals (nothing).

### 4.7 Dolphin's "$16:9 Widescreen" Gecko code, line by line

The code (Dolphin `Data/Sys/GameSettings/GZLE01.ini:347-452`, disabled by default) is anamorphic
native widescreen too. Every line was decoded against the DOL (r2 = `0x803FFD00`). What ours does
instead:

| Gecko patch | Ours |
|---|---|
| `043FA998 3FE38E39` (ini line 450): 4/3 → 16/9 | 3D-1, as (4F)/3 for any F |
| Painter box `0x803F7D68` −9 → −123, `0x803F7D6C` 650 → 767 | 2D-1 hook (exact F; keeps `setUpRectangle` vanilla; covers Fd2) |
| Wipe box, Fd4 `0x803FB77C`/`0x803FB78C` (last line `043FB78C C2F60000`) | 2D-5, 2D-6 |
| `li r4,0` NULL graf for game over, Wind's Requiem, opening | GEOM rows; opening side fills |
| 17 NOPs on the pause dims' alpha setters | GEOM rows (widened, kept) |
| Sea chart `cl` strip (lfs patch + cave) | GEOM row |
| Boomerang sight remap (`x' = 320 + (x − 320)·4/3`) | `ws_project_2d` at all 7 sites |
| Picto box and telescope fixed black bars; picto viewport (79,0,480,480) | 3.6 and H-15 |
| Minimap scissor `li 0x99/0x20` (right only at rest), `g_meter_mapHIO` words | 2D-7, H-7, H-8 |
| HUD: hearts, magic, compass, clock, D-pad, keys, buttons, rupees, swim timer (`g_meterHIO+0x50/+0x9C`, caves at `heartScaleInit`, `setInitAlpha`, `compassDirOpen`, `clockMultiMove`, `mMtx` pokes, `rupyInit` 320 → 206) | the J2DScreen::set pre-shift, which survives every re-layout (the Gecko misses `compassDirClose`/`ValueInit` and `rupyAlpha`) |
| Place name: root `mBounds.i.x` = −123 | H-10 |

Its code caves (`0x800037E0`, `0x80004038..0x800040F7`) sit in zeroed padding in
`gTRKInterruptVectorTable` (`.init`), not in our `CanaryTRK` (`0x8040CFB0..0x80410000`), and no
repo hook site is within 0x10 of any of its sites. It must never run with mode 2 (section 7).

### 4.8 Shared versus per screen

**Shared set-ups (cheap, most of the work):** the 3D knob (one word); the `setOrtho` hook (4
sites, one function); the J2DScreen::set hook pair, its walker and table (every screen); the
`ws_apply` data table (wipe, Fd4, minimap, timer); `ws_project_2d` (7 sites, one function);
`ws_persp` (2 sites); the latch.

**Per screen (small, each one on its own):** the minimap scissor wrapper; the picto viewfinder and
album; the opening fills; THP movies; the telescope fallback if EDGE rows don't hold mid-tween.

**Per element:** the cannon and Sploosh Kaboom widgets (vtable wrappers in the REL); the optional
distance sites.

## 5. The WWWS block, extended, and the C pieces

The culling plan's block (mailbox +0xA6C, `0x8041596C`) grows from 0x10 to **0x18 bytes** before
#1185 builds it, so no stage changes its layout later. It ends at +0xA84 (`0x80415984`), inside the
free +0xA6C..+0xD00 slack; `CanaryArenaHigh` starts at +0xA84 (`wwapi/mailbox/mod.go`,
`TestModRegions`: size 0x27C). Only C writes it.

| Offset | Type | Field | Meaning |
|---|---|---|---|
| +0x00 | u32 | `magic` | `'WWWS'` (0x57575753) |
| +0x04 | u8 | `version` | 1 |
| +0x05 | u8 | `mode` | requested: 0 off, 1 clip (Dolphin hack), 2 native |
| +0x06 | u16 | pad | 0 |
| +0x08 | f32 | `factor` | F, in [1, 3] |
| +0x0C | u8 | `flags` | bit 0 `WWWS_F_2D` (compress 2D in mode 2), bit 1 `WWWS_F_HUD` (anchor HUD to the edges). Stored as `c & WWWS_FLAGS_SUPPORTED`: 0 in a stage-1 build, 1 from stage 2, 3 from stage 3 |
| +0x0D | u8 | `ui_mode` | latched: 2 when 2D is compressed, else 0 |
| +0x0E | u16 | pad | 0 |
| +0x10 | f32 | `ui_factor` | latched F for 2D; 1.0 unless `ui_mode` is 2 |
| +0x14 | f32 | `ui_shift` | latched S = 329.5(`ui_factor` − 1) when `WWWS_F_HUD` is set, else 0 |

**`WWAP_OP_WS_SET`** (16): a = mode, b = factor bits, **c = flags** (new; Go sends 0xFF for
"everything this build supports", a tester sends 0 for an instant A/B of compression). It refuses a
mode above 2 or a factor outside [1, 3]; otherwise it stores mode, factor and flags, writes the 3D
knob (3.1) and returns OK. It never touches the `ui_*` fields.

**`ws_latch()`** runs from `ws_init` (every boot and soft reset) and from hook B when an EPOCH row
loads. It sets `ui_mode` = 2 if mode is 2 and `WWWS_F_2D` is set, else 0; `ui_factor` and
`ui_shift` to match; then runs `ws_apply(ui_factor, ui_shift)`.

**`ws_apply`** walks one rodata table of `{u32 addr; u8 type; f32 a; f32 b}` and writes
a + b·F (or a + b·S), rounded for s16 entries. At F = 1 every entry gives back the vanilla word, so
leaving native mode needs no restore code. Entries: the wipe box (4), Fd4 (2), the minimap hide
target (stage 2), the minimap corner and free icon and `g_menuHIO+0x9A` (stage 3, from S).

**`ws_2d_factor()`** is the one predicate every 2D reader uses (`ws_ortho_hook`, `ws_project_2d`,
`ws_persp`, the scissor wrapper, the side fills, the walker, and the REL's name tags): `ui_factor`
if the magic matches, `ui_mode` is 2 and 1 ≤ `ui_factor` ≤ 3 (NaN fails), else 1.0. The 3D knob and
the picto judge use the live `mode`/`factor`.

**`ws_init`** writes the block (mode 1, factor 4/3 as the culling plan says, flags = supported,
`ui_*` from `ws_latch`) and the knob `0x3FAAAAAB`. It never defaults to mode 2: C can't see
Dolphin's settings, and mode 2 with the hack on widens twice at any factor but 4/3.

**Module shape:** the culling plan's `WwModuleDesc` entry `{ 'WS  ', ws_init, ... }` gains a
per-frame step from stage 4 (THP). Hook sites stay in `hooks.c` (core owns hook sites); the mod's
functions sit in `inject/src/ws/widescreen.c` and are declared in `mods.h`.

## 6. Blob budget

**Room.** T2 runs `0x80410000..0x80413408` (`internal/inject/blob.go` `T2End`) and its budget
ends at the API region at `0x80414000` (`inject/include/wwops.h`): **0xBF8 bytes free**. (The
card's "0x478" was the figure after E3, before wind#80 and wind#90.) #1185 takes about 0x80-0x100.
T2's `.bss` counts too: latched state lives in the block, saved boxes on the stack.

**What a hook costs.** `hook_branchlink` rewrites the site's `bl` to point straight at the C
function (no trampoline): one 4-byte `DOLPatches` word and 0 bytes of T2. Sites that call the same
original with the same registers share one function (`setOrtho` ×4, `mDoLib_project` ×7,
`C_MTXPerspective` ×2). Measured today: naked forwarders 0x2C-0x50 (`exec_shim` 0x2C, `frame_shim`
0x48, `seachart_shim` 0x50), small C hooks 0x38-0xB0 (`pad_read_shim` 0xB0). The estimates below
count code ×1.5 at the high end (E3 once overshot by 0x4E0); table data is exact.

| Stage | T2 bytes | Contents | Running total with #1185 |
|---|---|---|---|
| 1 | 0x60-0xC0 | knob store in WS_SET/`ws_init`, `ws_snap_setresult` | 0xE0-0x1C0 |
| 2 | 0x540-0x820 | `ws_2d_factor` + latch, `ws_ortho_hook`, `ws_apply` + 7 entries, hook A/B + walker + 27 rows, `ws_project_2d`, scissor wrapper, album ortho, `ws_screen_draw_full` (2 entries), `ws_persp`, opening fills | 0x620-0x9E0 |
| 3 | 0x138-0x1B8 | anchor kinds, 7 rows + ~16 rules (HUD, auction, file select), 3 `ws_apply` entries; +0x80-0x120 if the cannon/Sploosh wrappers stay in T2 instead of the REL | 0x758-0xB98 |
| 4 | 0x70-0xD0 | THP frame step, optional polish | 0x7C8-0xC68 |
| 5 | 0x60-0x200 | picto, by option (0x78 if a resample lives in the REL behind a forwarder) | 0x828-0xE68 |

Stages 1-3 fit at the high end with about 0x60 to spare; stages 4-5 only at the low end.

**Must be T2:** every DOL `bl` target, and everything that runs before our REL links (Link exists +
300 frames): `ws_init`, WS_SET, `ws_apply`, the `setOrtho` hook, hooks A/B with the walker,
`ws_persp`, the opening fills, the THP step (the title movie). **Can be REL:** code with no DOL
site (the cannon and Sploosh vtable wrappers; the REL writes the slots and restores them in
`_epilog`, or a slot left pointing into an unlinked module crashes) and big gameplay-only bodies
behind a 0x30-0x50 T2 forwarder (the `seachart_shim` pattern). That saves bytes, not recaptures.

**If it doesn't fit,** cheapest first:
1. A global GEOM rule instead of 22 rows: −0xB0.
2. The cannon/Sploosh wrappers and any picto resample in the REL.
3. Go writes the anchor table into the mailbox slack before WS_SET (−0x140..−0x1B0 of T2, uses up
   to 0x1B0 of the 0x27C slack); rows can then be tuned without a blob rebuild.
4. Move the API region, the mailbox and ArenaLo up 0x1000 (ArenaLo `0x80416000` → `0x80417000`;
   build.py's `addi r3,r3,0x7000` at `0x80301820`): T2's budget becomes 0x1BF8. It costs 4 KiB of
   ZeldaHeap (the archive and game heaps are fixed, `m_Do_machine.cpp:551, 593`), a new REL id and a
   sweep of about 44 files with literal addresses (wind#80's precedent). Measure ZeldaHeap headroom
   on Outset, Windfall and the open sea first.

**Rule:** after stage 2 first compiles, read `TestT2EndsBelowMailbox`'s free-bytes line. If fewer
than 0x500 bytes are free (stages 3-5 at their high end need 0x488), apply levers 1-3, and lever 4
in R2 if that is still not enough or another card has T2 work queued.

**No runtime code rewrites.** Every per-mode value is a data word or read by a hook, so nothing
needs `DCFlushRange` (`0x803030D8`) + `ICInvalidateRange` (`0x803031BC`). Never ship a
patch-time-only DOL patch for a mode-dependent value: `DOLPatches` are outside BlobHash
(`HashRanges` covers T2's read-only sections), so an old save state or disc still passes the
contract check, and it would be wrong in modes 0 and 1. (build.py's `patches` loop maps only text
section 1; `dol_file_offset()` handles any section if a constant data patch is ever needed.)

**Re-patch cost.** Any T2 or REL change gives a new BlobHash (and a new REL id when `inject/rel`
changes): every player re-patches (the app does it by itself), and the PC `saves/start.sav` and
the Mac start save are recaptured, in mode 1 (save states carry the block and the knob). The C
build is a PC job (`needs:pc`). Go-only work is free. **Releases:** R1 = #1185 + stage 1, R2 =
stages 2+3 (new REL id: the name tags), R3 = stages 4+5. Stages are built and tested one by one;
only the releases bundle them.

## 7. Dolphin and the setting

**The setting.** *Widescreen: 16:9 / Custom W:H / Off*, stored as
`config.Mods["widescreen"].Settings = {"aspect": "16:9" | "W:H" | "off"}`, shown in a "Display"
card on the app's Settings page (`gui.Bridge` setter, validated by the mod's `ParseSetting`).
Presets: 16:9, 16:10, 21:9 (= 7:3, F 1.75). Custom takes integers W:H with W/H in (4/3, 4]; at or
below 4:3 it counts as Off. "21:9" means the literal 21:9 everywhere (the culling plan's table said
64/27; a 2560x1080 player picks Custom 64:27, 3440x1440 picks 43:18).

**Default.** 16:9, as the card asks, **from the release that carries stages 2+3 (R2)**. In R1 an
unset setting means Off: stage 1 alone leaves the HUD stretched, and a 16:9 default would turn a
4:3 player's window wide with a stretched HUD. One Go constant flips it.

**Off** means the culling plan's behaviour: no graphics `-C`, never mode 2; mode 1 from C's
default (and #1185's `widescreen` command or its later automatic writer).

**Launch arguments** (`launch.WidescreenArgs(setting)`, appended through a new
`session.LaunchOptions.Widescreen` that `gui.launchOptions` fills; wwdev reads
`WW_WIDESCREEN=<setting>` for `dolphin2`/`mp-local`; `auto-recapture` never sets it):

- 16:9: `-C Graphics.Settings.wideScreenHack=False -C Graphics.Settings.AspectRatio=1` (Force
  16:9: the heuristic never runs, F is exactly 4/3).
- Custom W:H: `-C Graphics.Settings.wideScreenHack=False -C Graphics.Settings.AspectRatio=4 -C
  Graphics.Settings.CustomAspectRatioWidth=W -C Graphics.Settings.CustomAspectRatioHeight=H`.
- Off: nothing.

Dolphin's `-C` system name for GFX.ini is **`Graphics`**; `-C GFX.Settings.*` is silently dropped
(`Config.cpp`, `CommandLineParse.cpp`). `-C` lasts for the session; GFX.ini is untouched. Dolphin's
`AspectMode` enum: 0 Auto, 1 Force 16:9, 2 Force 4:3, 3 Stretch, 4 Custom, 5 Custom (Stretch), 6 Raw.

**The mode follows Dolphin, not the setting.** After a launch, and when attaching to a Dolphin the
app didn't start, Go reads the effective settings with a fixed `launch.ReadGFXAspect` (process args
and user dir). A `[Video_Settings]` block in the player's `GameSettings/GZLE01.ini` beats `-C`
(layers: CommandLine < GlobalGame < LocalGame). Go sends mode 2 only when all hold:

- the hack is off;
- the aspect is Force 16:9 or Custom (Stretch modes only after the VI-aspect live check);
- `0x803FA998` holds what C last wrote (`0x3FAAAAAB` outside mode 2), so no other widener is
  running;
- Dolphin's "$16:9 Widescreen" Gecko code is not enabled (scan `[Gecko_Enabled]` in
  `GameSettings/GZL.ini`, `GZLE01.ini`, `GZLE01r0.ini`, and `EnableCheats`; the word check above
  catches it either way).

Otherwise it sends mode 1 with the hack's factor (the culling plan), or mode 0 when the Gecko code
is on. A mismatch between what was asked and what Dolphin runs shows one line on the Play page.

**Who submits.** The engine's per-player Dolphin runner (`mods/multiplayer/engine/game.go`, which
already launches and probes Dolphin) owns one `ops.Queue` per PID; nothing else in the app submits
today (`cmd/wwdev/ops_cmd.go`'s note about "the app's Game" is stale). It starts at the first probe
where the mod is present and the queue is `Ready()` (the logo or title: `ops_frame` runs before the
Link check). Each tick it reads the mode byte (`0x80415971`) and factor (`0x80415974`) and resubmits
WS_SET on any difference: that heals soft resets (`ws_init` restores mode 1), save states taken in
another mode and a stray CLI toggle. It polls about every 100 ms until the first apply, then every
second, and moves to `Game.Ops()` once the mod runtime has a Game. #1185's CLI `widescreen` command
refuses while a session holds the mailbox (`shadow_mode` set). CLI `host`/`join` and the TUI don't
launch Dolphin and keep #1185's command and its later automatic writer.

**The boot window.** The logo draws in mode 1 for under a second. If WS_SET lands after the title
screen loaded, the title keeps the old 2D (the latch moves at file select) and the 3D widens at
once. Cosmetic; testers should not file it. If the boot-timing check (stage 2's tests) shows WS_SET
reliably lands before `title_logo_e.blo` loads, add it as an EPOCH row.

**Rejected:** a Gecko "boot request" in the player's GameSettings (needs EnableCheats, which also
runs every code they ticked; edits a file the app deliberately leaves alone; persists into launches
the app didn't make; a Gecko code already failed to install after a state load, culling plan 3.2).

**Bugs found on main (fix in stage 1, Go only):**
- `wwapi/emu/launch/widescreen.go:79-84` parses only `gfx.settings.` (lower-cased): Dolphin uses
  `Graphics.Settings.` and drops `GFX.`. Match `Graphics` exactly (section case-insensitive) and
  ignore `GFX.`; its doc comment (`:38-43`) and tests (`widescreen_test.go:34, 37, 40`) say `GFX`.
- `ReadGFXAspect` ignores `SuggestedAspectRatio` (which overrides the heuristic); add it.
- Its custom default is 16:9 (`:45`); Dolphin's is 1:1.
- `XScale` uses 4/3 for Stretch and Custom (Stretch) where Dolphin uses the VI aspect (≈1.2534).
- The culling plan's step 7.5 forces the hack with `-C GFX.Settings.*`, which Dolphin ignores
  (fixed in this PR).

## 8. Stages

Each stage is one card (or one PR) that ships on its own and is tested in Dolphin before the next
starts. The checks are CLAUDE.md's "Checks (run locally)"; every C change carries its restamped
blob in the same PR (`cd inject && python build.py && cd .. && python scripts/extract_blob.py` on
the PC). After every C stage: `go test -count=1 -run TestT2EndsBelowMailbox -v ./internal/inject`
and write the free-bytes figure on the card.

**Test rig, every stage.** The Mac rig (docs/mac-rig.md; take a `ww/dolphin-slot-<n>` lease) or the
PC two-Dolphin loop, with a start save recaptured for the stage's blob (in mode 1). Native output:
launch with `WW_WIDESCREEN=16:9` once stage 1's Go lands, or by hand with `-C
Graphics.Settings.wideScreenHack=False -C Graphics.Settings.AspectRatio=1`. Pictures:
`WW_DOLPHIN_INDEX=0 ./wwdev.exe screenshot /tmp/ws.png` (PC; it captures the stretched window, which
is what the player sees) or `scripts/mac-rig.sh shot`; if the Mac agent lacks Screen Recording,
frame dumps with `-C Dolphin.Movie.DumpFrames=True -C Dolphin.Movie.DumpFramesSilent=True -C
Graphics.Settings.DumpFramesAsImages=True` (default dump resolution uses the window's aspect; a Raw
dump is pre-stretch, useful for measuring 2D at 320 + (x − 320)/F). `-C
Graphics.Settings.OverlayProjStats=True` prints Dolphin's projection ratios (gameplay: ortho ≈ 0.94
in mode 0, ≈ 1.26 with 2D compressed at 16:9). Mode toggles: `ww-multiplayer widescreen ...`
(#1185's command; stage 1 adds a native form) or `wwdev ops` submitting op 16. Peek the block:
`./wwdev.exe peek 8041596C 24`. Screens that need items (telescope, picto box, boomerang, bow,
Deku Leaf, baton) need a save that has them; the start save may not, so note which checks were run.
Put pictures on the card, not in git.

### Stage 1: world and culling (with #1185, release R1)

Changes the C side, so it lands in **#1185's blob change**: follow the culling plan's section 8
with these differences.

1. **Header** (`inject/include/wwws.h`): the 0x18 layout of section 5, `WWWS_F_2D` 0x01,
   `WWWS_F_HUD` 0x02, `WWWS_FLAGS_SUPPORTED` 0x00, `WWWS_MODE_NATIVE` 2, `WWWS_KNOB_ADDR`
   0x803FA998, `WWWS_KNOB_VANILLA` 0x3FAAAAAB. The static asserts check `WWWS_OFF + 0x18`.
2. **C** (`inject/src/ws/widescreen.c`): WS_SET takes c = flags (stored `& WWWS_FLAGS_SUPPORTED`)
   and writes the knob: `(4.0f*factor)/3.0f` in mode 2, `WWWS_KNOB_VANILLA` otherwise. `ws_init`
   writes the knob vanilla and the `ui_*` fields to 0 / 1.0 / 0. `ws_clip_aspect` widens only in
   mode 1 (as planned).
3. **Picto judge** (T-4): `ws_snap_setresult` in hooks.c, `proj.hook_branchlink("ws_snap_setresult",
   0x800CE5E8)`: in mode 2 (live `mode`/`factor`), multiply each captured-pixel count by F
   (`dSnap_c` result fields: read `d_snap.cpp` `SetResult` and its caller for the exact layout),
   then call the original. Pin it in `internal/inject/t2_budget_test.go` like `TestSeachartShimLoadsVisWord`.
4. **Go mirrors:** `wwapi/mailbox/widescreen.go` with the 0x18 layout and `Decode`; pin test
   against `wwws.h`; `CanaryArenaHigh` from +0xA84 (`TestModRegions`, size 0x27C).
5. **Go mod and app** (Go only; can land before the blob, they do nothing until it ships):
   - the `ReadGFXAspect` fixes (section 7: `Graphics` prefix, `SuggestedAspectRatio`, custom 1:1,
     VI aspect for the Stretch modes) with their tests;
   - `launch.WidescreenArgs`, `session.LaunchOptions.Widescreen`, `gui.launchOptions`, wwdev
     `WW_WIDESCREEN` through `TestArgs`;
   - the setting (`ParseSetting` presets 16:9, 16:10, 21:9, Custom W:H, off; unset = off in R1),
     the Settings page "Display" card and its Bridge setter, `cmd/ww-multiplayer` goldens;
   - the mode policy (section 7) as one function with a table test, and the engine runner's
     reconcile loop over `ops.Queue` (test with `wwapi/ops/opstest`);
   - the Gecko-code scan and the knob-word check, with a Play-page warning line;
   - `xscaleSampler` writes XScale 0 when the block's mode is 2;
   - #1185's CLI command gains `widescreen native <aspect> [--flags N]` (mode 2, flags 0xFF by
     default; `--flags 0` is the tests' A/B), refused while a session holds the mailbox.
6. **Live checks (Dolphin):**
   1. Before the build, with today's blob: hack off, Force 16:9, `./wwdev.exe poke-u32 803FA998
      3FE38E39`. The world is round, not wide; `peek 803A575C` reads `0x3FDA740D`. Cross two stage
      loads and the pause menu, then `peek 803FA998` (still `3FE38E39`): this proves a single store
      holds under the JIT. `poke-u32 803FA998 3FAAAAAB` restores it.
   2. With the build, launched at 16:9: the block reads mode 2, factor `0x3FAAAAAB`; the knob
      `0x3FE38E39`; `mClipper+0x50` equals `view->mAspect`. The culling plan's `cull-probe`: no CULL
      actor with tanH between 0.739 and 0.985 is NODRAW.
   3. Pictures: the world is correctly proportioned, the HUD stretched exactly as with today's hack.
      Sun glare is lost exactly when the sun goes behind something at the screen edge (not early, as
      under the hack).
   4. Fades: a door with the OVERLAP2 twist (wipe type 2): the first frames fill the width, no gaps.
      Open the pause menu 5+ times (random cloth rotation): the cloth reaches both edges every time.
   5. Picto box (if the save has it): take a quest-style photo of a subject in mode 2 and in mode 1;
      the judge's verdict matches.
   6. Lock-on: Z-target an enemy with another beside it near the edge; both stay on screen.
   7. Soft reset (`./wwdev.exe input 0x1600 0 0 3500`): the mode byte drops to 1, the knob to
      `3FAAAAAB`, and the runner puts mode 2 back within a tick.
   8. Refusals: launch with the hack on (fixture GFX.ini): Go stays in mode 1. Enable the "$16:9
      Widescreen" Gecko code in a test user dir (`EnableCheats=True`): `peek 803FA998` shows
      `3FE38E39` every frame, Go picks mode 0 and warns.
   9. `-C` name: launch over `AspectRatio=1` with `-C GFX.Settings.AspectRatio=2` (window stays
      16:9), then `-C Graphics.Settings.AspectRatio=2` (window turns 4:3).
   10. Regression: the culling plan's step 7 checks in mode 1 with the hack on, unchanged.

### Stage 2: 2D proportions (release R2, with stage 3)

1. **Block and helpers.** `WWWS_FLAGS_SUPPORTED` = `WWWS_F_2D`. `ws_2d_factor`, `ws_latch`,
   `ws_apply` with its table (section 5: the wipe box ×4, Fd4 ×2, the minimap hide target).
2. **`setOrtho` hook** (3.2): `ws_ortho_hook` in hooks.c, four `hook_branchlink` lines
   (`0x8000B06C`, `0x8000BA30`, `0x80223CF4`, `0x8003EADC`), one pin test each.
3. **`ws_project_2d`** (3.4) at `0x800E1624`, `0x80129550`, `0x801F8104`, `0x801F9910`,
   `0x8020DF5C`, `0x802229D0`, `0x80222ADC`.
4. **J2DScreen hooks** (3.3): hook A `0x802D0968`, hook B `0x802D09DC`, the walker with GEOM, EDGE
   and EPOCH (no shift kinds yet), and the table: a Go generator (e.g. `tools/gen_wstable`) writes
   a C header from a Go list, with a freshness test and a hash-collision test over every `.blo`
   literal in the decomp. Stage-2 rows: `menu_save`, `wipe_00`, `wipe_00_2`, the 22 GEOM rows,
   EPOCH on `main_parts1` and `main_parts_fileselect`.
5. **Minimap scissor** (2D-7): `hook_branchlink` at `0x8004A64C`. Confirm the result addresses
   (`0x803F6AE4`/`E8`) and the map's left-x field from `calcScissor`'s disassembly first.
6. **`ws_persp`** (3D-12) at `0x802306A0` and `0x802324A8`.
7. **Picto box** (3.6 option A, stage-2 half): `ws_screen_draw_full` at `0x8022B510`,
   `0x8022B538`, `0x8022B778`, `0x8022B9B8` and its second entry at `0x8022B524`, `0x8022B9CC`;
   the album ortho wrapper at `0x80227BB8` (widen the left/right arguments of `C_MTXOrtho`,
   `0x8030DC48`, about 320.5 by F).
8. **Opening fills** at `0x8023452C` (`ws_fill_sides`, 3.5).
9. **REL** (3.7): name tags and lobby line read the block; `wwws.h` joins `REL_HEADERS`; new REL id.
10. **Go:** the runner sends flags 0xFF; status shows "2D pending until the next stage load" when
    `ui_mode` lags `mode`. Default stays off until stage 3 ships with it in R2.
11. **Live checks (16:9, then 16:10 and 21:9 by Custom):**
    1. HUD hearts and buttons are round and sit inside the centre 4:3 area (EFB 80..560 at 16:9);
       text has vanilla proportions. `ww-multiplayer widescreen native 16:9 --flags 0` then a stage load: 2D
       goes back to stretched; `--flags 1` and a stage load: compressed again.
    2. Pause menu: Items, Quest Status, Map, sea chart, a treasure chart, Options, Save: every dim
       and backdrop reaches both edges; the save iris leaves no gap in the bands; scrolling lists
       clip exactly at their frames (pane scissors agree with the drawing).
    3. Game over and Continue; the Wind's Requiem selector: the dim covers the full width, the
       letters and ring are not stretched.
    4. Transitions: OVERLAP2 door (no one-frame HUD widening at the twist), a cutscene iris wipe
       (round, covers the corners), an OVERLAP4 white fade (full width).
    5. Reticles: boomerang with 3-5 targets spread to both edges, bow/hookshot at an edge target,
       Z-lock a Bokoblin at each edge (HP bar 50 units beside it), Deku Leaf gauge flying to an
       off-centre Link, talk to an NPC at the far left (the box springs from the NPC), baton
       swung to both extremes (sparkle on the tip). Each sits where it does in a 4:3 run.
    6. Minimap: icons are clipped to the map frame; hide the map (it slides fully out of the left
       band).
    7. Telescope: open, zoom and close in both layouts; the bands stay black wherever the 4:3 area
       is, and the bars never lag the iris. If they do, use H-15's fallback.
    8. Picto box: the viewfinder looks as today and matches the photo taken; the album's photos
       sit in their frames.
    9. File select: the cloth panel keeps its side margins around the panels; the sky is round.
       New game: the opening's bands stay black during the `d4` pan.
    10. mp-local in mode 2: walk B's Link to A's screen edges; A's tag sits over B's head with
        glyphs shaped as at the centre. The lobby line on file select is centred, not stretched.
    11. Boot timing: from a cold app launch poll `peek 80415971` every 50 ms; note when ops is
        ready, when mode 2 lands and when the title appears. Decide the `title_logo_e` EPOCH row.
    12. Regression: mode 1 with the hack on and mode 0: everything identical to stage 1.
    13. A hash miss: a talk box (`hukidashi_08`) is unchanged (compressed, centred).

### Stage 3: HUD to the edges (release R2)

1. `WWWS_FLAGS_SUPPORTED` = `WWWS_F_2D | WWWS_F_HUD`; `ws_latch` sets `ui_shift`.
2. Walker: the shift kinds ALL_L, ALL_R, BY_CX, SCR_L, SCR_R and the L/R tag actions (3.3).
3. Rows: `main_parts1` (BY_CX + the NONE list), `main_parts2`, `main_parts3`, `time_swim_1`,
   `time_swim_2`, `place_name` (after a screenshot proves it left-anchored), `auction1`, `auction2`,
   `main_parts_fileselect` ALL_R.
4. `ws_apply` S entries: `0x803E68E4` (35 − S), `0x803E68F0` (590 + S), `0x803E6B06` (−S).
5. REL: the cannon game's vtable wrappers (H-12), slots restored in `_epilog`; Sploosh Kaboom only
   if its screenshot shows edge-placed numbers.
6. Go: the setting's default becomes 16:9 for the R2 release.
7. **Live checks:** hearts, magic, compass and clock keep their vanilla left margin; buttons, keys
   and rupees their right margin; the boss bar and lock-on bar stay centred (`ben2` too). Swim in
   the sea: the air meter sits at the right edge, not clipped, its shine on `wk00`. Minimap at the
   left edge, the wind arrow on it at sea, the free-camera icon at the right. Island arrival: the
   place name at the left. A minigame timer (boat race or Orca): the rupee icon at the left.
   Windfall auction: digits roll uncut, the icon and plates hug the top-left. Cannon game: bombs
   left, ships right, battery right; leave the game and peek `0x80389100`/`0x80389110` (back to
   `800C946C`/`800C90F0`). File select: the button cluster at the right. Mid-stage `WS_SET` mode 0:
   nothing goes off screen; the HUD updates at the next stage load.

### Stage 4: menus and special screens (release R3)

1. **THP movies** (T-9): first verify the module-415 `.bss` offsets live (peek after the title
   movie starts: xSize 640 at `.bss`+0xCB580). Then the T2 frame step (the WS module's per-frame
   entry). Check: the title movie and the staff roll are pillarboxed with black bands, at the right
   proportions.
2. **Optional polish**, each one judged by a screenshot first: the lens-flare and moon-star
   `ws_project_2d` sites; smaller cinema trims; the pause cloth `ws_persp` + scale; game-over
   letters; the title lobby row spacing (Go).
3. **Live checks:** the movies; an off-axis sun's flare streak points the right way; stars are
   hidden in a round area around the moon.

### Stage 5: picto box and telescope (release R3)

1. **Ask the person** (a blocker on the card) which picto option (3.6: A recommended, B, C), with a
   mode-2 album screenshot from stage 2 attached.
2. Build it: A = the optional album/freeze-frame crop (`bl C_MTXOrtho` `0x8000A6C0` and the album
   draw); B = the resample in `mCaptureProc` behind a forwarder plus rows for `wipe_01_01/_02`
   (and drop stage 2's uncompressed draws there); C = a per-frame knob write while aiming.
3. The telescope look-in radius (`0x8019744C`) through `ws_project_2d`; the scope-opening view rect
   (2D-15) if visible.
4. **Live checks:** a photo of a round object (the Windfall lighthouse, a pot) looks round in the
   album under the chosen option; a quest photo passes; at night with the moon about 110 px left of
   the scope centre the telescope's moon line does not trigger.

## 9. Corrections to `widescreen-culling.md` (made in this PR)

- §9: the mode-2 knob is the 4/3 literal `0x803FA998`, not `g_HIO.mAspectRatio`; widening the
  fade was a regression, not a benefit; the reticles also need `ws_project_2d` once 2D is
  compressed; Dolphin's hack stops widening only at exactly 16:9 and after its transition count;
  the Gecko line `043FA998 3FE38E39` is GZLE01.ini:450, not the last line; its caves are not in
  `CanaryTRK`; lock-on framing is decided (stays 4:3).
- §6.1: the block is 0x18 bytes (section 5), WS_SET's c carries flags, and "21:9" is 7:3 (F 1.75)
  with 64:27 listed apart.
- §4: the name-tag row holds for mode 1 only; mode 2 needs the REL change (3.7).
- §7, §8 step 4: `CanaryArenaHigh` starts at +0xA84 (size 0x27C).
- §8 step 7.5: `-C Graphics.Settings.*`, not `GFX.Settings.*`.
- §2: `Widescreen.cpp:84-151` (overrides at `:55-80`).

## 10. Open questions and live checks owed

**For the person:**
- The picto box option (3.6), asked at stage 5 with a picture.
- Pause dims widened (this plan) or removed (the Gecko): widened unless they say otherwise.
- Default Off in R1, 16:9 from R2 (section 7): the card asked for 16:9 by default; this plan only
  waits until the HUD is right.

**Owed live (each is in its stage's checks):** a single knob store holds under the JIT; the pause
cloth covers at every rotation; boot timing of WS_SET against the title; the save iris and telescope
EDGE bars through their tweens; `place_name.blo`, `ship_race0/1/2.blo` and `file_select.blo`
layouts (not in the `.blo` dumps); Sploosh Kaboom's number positions; the THP `.bss` offsets; the VI
aspect (≈1.2534; a default F9 dump under Force 4:3 should be about 602x480) before any Stretch mode
is allowed; whether `back_aori.bti` (the `BACK` panes of `operate_wind_mask` and `auction1/2`)
resolves to a texture that needs GEOM.

**Not scanned:** RELS.arc for relocations to `setOrtho` (`0x802CDD14`) and `mDoLib_project` from
the ~75 stub actors (the decomp source shows none). A relocation scan of RELS.arc settles it.

**Accepted cosmetic:** the file-select sky and the title's first frames after a late WS_SET; notes
sliding in through a band; the title ship visible in the bands; text about 25% softer sideways (the
2D is drawn into fewer EFB pixels); the sun's falloff and Aryll's telescope check stretched by F.
