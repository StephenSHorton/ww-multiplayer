# Native widescreen: every 4:3 assumption and the build plan

**Status:**
- **Stage 1** was built on card #1185, with the culling plan (PC build 2026-11-09, BlobHash
  0x1100504D). Its live checks passed on the PC rig: docs/TESTING.md "Widescreen stage 1".
- **Stages 2 and 3** are built on branch `widescreen-2-5` (PC, 2026-11-09). They first overflowed
  T2; lever 4 (section 6, "Lever 4, as built") moved the API region, mailbox and ArenaLo up 0x1000,
  and its ZeldaHeap gate passed live (WS2-0). Their live checks are docs/TESTING.md "Widescreen
  stage 2" and "Widescreen stage 3".
- **Stages 4 and 5** were built on branch `ws-45` (PC, 2026-11-09, over `widescreen-2-5` db6fc72;
  section 8 "Stage 4" and "Stage 5", as built) and merged into `widescreen-2-5`. Their live checks
  are docs/TESTING.md "Widescreen stage 4" and "Widescreen stage 5". The plan was card #1186
  (2026-11-09).
- The plan builds on #1184's culling plan
(`docs/design/widescreen-culling.md`, "the culling plan" below), whose clipper hook, `WWWS` mailbox
block and `WWAP_OP_WS_SET` op card #1185 builds. Section 9 lists what this plan corrects in it.
Every address here was checked against the vanilla GZLE01 DOL and the decomp
(`~/projects/tww`) by a finder and then by an adversarial verifier, unless the row says
"unverified" or "live check". No byte count here was measured from compiled code: there is no
devkitPPC on the Mac, so the first real sizes come from the PC build of stage 1 (section 6).

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
  hook pair with a table, one wrapper for 2D placed at projected 3D points (7 sites), and a few data
  words. Per-screen work is small: the picto box, the opening, THP movies, minigame HUD widgets.
- **Budget:** T2 had 0xBF8 bytes free on main. Stage 1 with #1185 measured 0x320 (the plan said
  0xE0-0x1C0), so 0x8D8 is left. Stages 2-3 at their high estimate (0x9D0) no longer fit: the
  levers in section 6 are chosen at stage 2. No runtime code-word rewrites, so no icache work.
- **Dolphin:** the app launches Dolphin with `-C Graphics.Settings.wideScreenHack=False -C
  Graphics.Settings.AspectRatio=1` (Force 16:9), or Custom W:H. Go puts the mod in native mode
  (mode 2) only when Dolphin's effective settings agree. The setting is *Widescreen: 16:9 / Custom
  W:H / Off*.
- **Stages,** each shippable and testable in Dolphin: (1) world and culling, with #1185, 2D still
  stretched as today; (2) 2D proportions, HUD inside the centre 4:3; (3) HUD at the edges;
  (4) menus and special screens polish, THP movies; (5) picto box proper and the telescope look-in. Releases: R1 = #1185 +
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

(The hex words are the nearest f32 to the exact box. `ws_ortho_hook` computes the box in f32 from the factor word and lands one or more ulps away: 16:9 `0xC2EDAAAC`/`0x443DF556`, 16:10 `0xC295CCD0`, or `0xC295CCCF` when fused. Tests recompute the same expression instead of pinning these words.)

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

**Mode 2 writes it; nothing writes `g_HIO.mAspectRatio`.** `g_HIO.mAspectRatio` (`0x803B9A08`, `g_HIO`+0xC; `g_HIO` is `0x803B99FC`) has six readers,
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
the Gecko's word). `(4.0f/3.0f) * factor` is one ulp high at 16:9, 16:10, 21:9 and 32:9 (equal at 64:27). Modes 0 and 1 write `0x3FAAAAAB`
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
No other DOL caller exists, and none of the disc's 415 RELs has a relocation to `0x802CDD14` (binary scan of RELS.arc and /rels/).

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
into. No row combines a shift with GEOM or EDGE. GEOM and EDGE match at any depth, on absolute
coordinates: a pane's absolute x is its `mBounds.i.x` plus every ancestor's up to the screen
(rotation and scale ignored), the same for y.
- **GEOM** matches a childless `PIC1` with absolute i.x ≤ 0, f.x ≥ 640, i.y ≤ 0 and f.y ≥ 480,
  and moves both x edges to x′ = 320.5 + (x − 320.5)F.
- **EDGE** moves an edge only if it is at absolute x ≤ 0 (left) or ≥ 640 (right), by the same
  formula.
- Write back the new absolute value minus the parent's absolute x into `mBounds.i.x`/`f.x`, then
  call `setBasePosition` (which also sets `mRotationAxis` to 'z', harmless for these unrotated
  panes).

J2DPane fields: +0x04 magic (`'PIC1'`,
`'PAN1'`), +0x08 tag, +0x0C `mBounds {i.x, i.y, f.x, f.y}`, +0xA9 `m2DBasePosition`, +0xB0 child
tree (`JSUTree<J2DPane>`: the first child link is the list head `*(u32*)(pane+0xB0)`; for a link L the child pane is `*(L+0)` and the next link `*(L+0xC)`, NULL ends; a pane's own link is at its +0xBC).

**Table** (T2 rodata; generated by a Go tool from a list of names, tags and actions, with a test
that hashes every `.blo` name string in the vanilla DOL and RELs and fails on a collision; the
decomp's literals miss `ship_race2.blo`, which only the DOL's `@stringBase0` holds):

- Row `{u32 hash; u8 flags; u8 n; u8 first; u8 pad}` (8 bytes). `flags`: kind in bits 0-1 (NONE,
  ALL_L, ALL_R, BY_CX: centre x < 320.5 → L, else R), GEOM 0x04, SCR_L 0x08, SCR_R 0x10, EPOCH
  0x20. Rule: `u32 tag` + `u8 action` (NONE, L, R, EDGE), 5 bytes. The tag is the C multichar value, right-justified: `'l'` = 0x0000006C, `'pn'` = 0x0000706E, `'wp04'` = 0x77703034. The generator must encode short tags the same way.
- Hash: `h = 5381; for each c: h = h*33 ^ (c | 0x20)` (u32 wrap) over the `name` argument exactly as `set` receives it, extension included (`"main_parts1.blo"`). The table below writes names without `.blo`, and the generator appends it. A name not in the table gets no change.
- L, R, ALL_*, BY_CX and SCR_* act only when the latched HUD shift is non-zero (section 5);
  GEOM and EDGE act whenever 2D is compressed.

| Screen (`.blo`) | Row | Stage |
|---|---|---|
| `main_parts1` | BY_CX, EPOCH; NONE for `ben1-4 enem enm2 bey1 bey2 enek ene1-3 l r wiy1` | EPOCH 2, the rest 3 |
| `main_parts2` / `main_parts3` | ALL_R / ALL_L | 3 |
| `time_swim_1` / `time_swim_2` | SCR_R / ALL_R | 3 |
| `place_name` | `pn` L (`place_name.blo` from the disc: `pn` (38,66) 360x40, centre x 218, a child of `ROOT`; `blc1`/`blc2` are hidden by `setScreen`) | 3 |
| `auction1` / `auction2` | SCR_L / `icon ip00-03 gd00-05` L (`met1/met2` stay centred) | 3 |
| `menu_save` | GEOM; `wp04-07` EDGE | 2 |
| `wipe_00`, `wipe_00_2` (telescope) | `wp04-07` EDGE | 2 |
| `menu_collect_01`, `menu_item_02`, `menu_explanation`, `menu_map_d`, `option`, `f_map`, `f_hikaku_1`, `g_map_01`, `t_map_01`, `DOC_map_01`, `HRT_map_1`, `HM_map_1`, `IR_map_1`, `MN_map_1`, `SENSUI_map_1`, `TERRY_map_1`, `TR_map_1`, `YG_map_1`, `YS_map_1`, `gameover`, `operate_wind`, `operate_wind_mask` | GEOM | 2 |
| `main_parts_fileselect` | EPOCH (stage 2); ALL_R (stage 3) | 2, 3 |

About 0x1D3 bytes of data (34 rows, 39 rules) and 0x1C0 of code. A global GEOM rule (every screen) would drop the 22
GEOM-only rows (−0xB0), if a stage-2 screenshot sweep shows no false hits.

**As built (stage 2): the global GEOM rule (lever 1).** A screen with no row gets GEOM; a row without
the GEOM flag turns it off for its screen. The table has 13 rows and 39 rules (0x12B bytes).

Instead of a screenshot sweep, `go run ./tools/gen_wstable -geom <disc>` parses all 63 `.blo` files
on the vanilla disc (archives inside archives included). It writes every pane the GEOM test matches
to `tools/gen_wstable/testdata/geom_hits.txt`.

- There is one visible false hit: `wipe_01_01.blo`'s album paper `b1` (note_02.bti) and strip `b2`
  (strip00.bti), which are drawn uncompressed. They get a row with no flags.
- Every other extra hit is a `BACK` pane (0..640 × 0..480): the layout tool's reference picture.
  Its texture is in no archive on the disc (back.bti, baton3.bti, shiprace.bti, bg_proro.bti,
  sky_cloth.bti, back_aori.bti, back_auction_00.bti, collect_back.bti), so it draws nothing. This
  answers section 10's open question on `back_aori.bti` and `back_auction_00.bti`: they need nothing.
- `TestGeomGlobalRule` checks the list against the plan's 22 screens and `menu_save`. Each of them
  has a hit.
- `TestRuleTagsOnDisc` (disc-gated) checks that every rule's tag exists in its screen, and that every
  L/R tag is a direct child of the anchor.

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
| `0x8019744C` | `dKy_telescope_lookin_chk` | radius test round (320,240); the radius is the caller's f1 (100 for the moon, Orion and Hokuto checks), not a constant. With a 320.5 pivot the test is off by 0.5(F − 1); a 320 pivot is exact here | optional | 5 |
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
| Cutscene circle wipe `0x803F89B8..C4` (−9, −21, 659, 524) | data in `ws_apply`: x = 320.5 − 329.5F, y = 241 − 262F, w = 659F, h = 524F (both axes, so the iris stays round; 16:9, computed in f32 from the factor word `0x3FAAAAAB`: `C2EDAAAC C2D8AAAC 445BAAAB 442EAAAB`; the exact-math words `C2EDAAAB C2D8AAAB` differ by one ulp, so tests recompute the same f32 expression instead of pinning words) | 2 |
| OVERLAP4/5 blit (`dDlst_2Dt_Sp_c`) | data: `0x803FB78C` = 320.5 − 329.5F (`0xC2EDAAAC` in f32 at 16:9; the exact value rounds to `0xC2EDAAAB`), `0x803FB77C` = 1.0296875F (`0x3FAFBBBC`); vanilla `C1100000`/`3F83CCCD` (`0x803FB77C` is also read by `cnvAddress` at `0x80224928`, whose result the USA `draw` overwrites with −9/−21, so the write is harmless) | 2 |
| Pause, map, save, option dims and backdrops; sea chart `cl`, `ms01/ms02`, `HK00/HK01`; game over `blak`; Wind's Requiem `mask` | GEOM rows (widened, not removed as the Gecko does) | 2 |
| Save iris bars `wp04-07`; telescope bars `wp04-07` | EDGE rows | 2 |
| Opening storybook (660-wide art, `mak1/mak2` bars, `d42` waiting at 651..1311) | compressed art + black side fills over x L′..−9 and x 650..R′ (y −21..503) after `bl J2DScreen::draw` at `0x8023452C` (stage 2 step 8) | 2 |
| Picto viewfinder (`wipe_01_01`, `wipe_01_02`) and shutter lines | drawn uncompressed (section 3.6) | 2 |
| THP movies (title loop, staff roll) | pillarboxed by a T2 frame step (section 4.5) | 4 |

The Gecko's `li r4,0` (NULL graf) trick is not used anywhere: it draws with a 0..640 box (not the
vanilla −9..650) and leaves that projection current.

### 3.6 The picto box: recommendation and the decision it needs

The capture copies a fixed EFB rectangle (304x208 at 168..472 × 76..284, 2:1 to a 152x104 card),
the subject judge counts EFB pixels inside a fixed rectangle (x 174..467,
`DSNAP_SHUTTER_LEFT/RIGHT`, `d_snap.cpp:1393-1394`), and the photo viewers have private orthos.
EFB copies scale only 1:1 or 2:1. The judge's alpha clear (`dSnap_packet::ClearAlphaBuffer`
`0x800CE374`, its own `C_MTXOrtho` 0..640 × 0..480, quad 174..468 × 131..329) is EFB-fixed like
the judge and needs nothing.

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
  does not regress it. The album needs nothing in stage 2: its slot frames (`pane_no`) belong to `wipe_01_01`, which is drawn uncompressed above, and `pictureDraw`'s private ortho (box `0x803FB81C..28`, `bl C_MTXOrtho` at `0x80227BB8`) is the same vanilla box. Only option B (compressed `wipe_01_01`) must widen it about 320.5 by F, or the slots drift by (x − 320)(1 − 1/F).
- Result: the picto box's own art (viewfinder, album frames) looks as it does under today's hack,
  stretched, and the photos look right: a photo holds the anamorphic EFB pixels and is drawn back
  into the same uncompressed space, so Dolphin's stretch restores it, in the freeze-frame and in the
  album alike. A photo taken in one mode and viewed in another is stretched or squeezed by F
  (accepted). Stage 5 then has nothing to build for option A.

Alternatives for the person: **B**, true 4:3 photos by a software resample in `mCaptureProc`
(`0x8000AAC4`, a 0x58-byte function; the resample would add ~0x150 bytes and could live in the REL) plus a compressed viewfinder; **C**, drop the
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
| 3D-3 | 3D particle aspect | `m_Do_graphic.cpp:1604, 1673` | `lfs` `0x8000B200`, `0x8000B4F8` | shared | follows | 1 |
| 3D-4 | Cutscene camera aspect | `JSGGetProjectionAspect` | — | shared | follows (a demo's own aspect is ignored) | 1 |
| 3D-5 | Checks on EFB px: sun glare + `peekZ`, clouds, fireflies, lantern, `pointInSight`, `shakeCamera`, `possessedEvCamera`, picto `CalcArea2D` | `d_kankyo_rain.cpp:602`, `d_a_ff.cpp:40-73`, `d_a_kantera.cpp:146-155`, `d_snap.cpp:1514-1609` | see 3.4's "no" rows | shared | right by themselves in mode 2; never wrap | 1 |
| 3D-6 | Distance from centre in EFB px: flare angle, moon-star exclusion, telescope look-in | `d_kankyo_rain.cpp`, `d_kankyo.cpp` | `0x8008C6C0`; `0x800994AC`+`0x80099A6C`; `0x8019744C` | element | optional `ws_project_2d` | 4, 5 |
| 3D-7 | Aryll's telescope checks | `d_a_npc_ls1.cpp` (loose REL) | sec1+0x302C, +0x31D4 | element | accepted: only more lenient | — |
| 3D-8 | Lock-on framing `mWindowAspectRatio` | `d_camera.cpp` | stored `0x8017BE84`, read `0x801659BC` | shared | stays 4:3 on purpose (3.1) | — |
| 3D-9 | `g_HIO.mAspectRatio` (0.96) | `f_ap_game.cpp:34` | `0x803B9A08`; readers `0x8017BE68 0x8017C920 0x801DF9F4 0x80223A00 0x80230034 0x80232498` | shared | never written (3.1) | — |
| 3D-10 | `dOvlpFd2` capture quad, own 60° perspective | `d_ovlp_fade2.cpp:20-102` | `bl C_MTXPerspective` `0x80223A10` | screen | none: stays 1.28 and fills the EFB | — |
| 3D-11 | Pause cloth (`dMCloth`), own 30° perspective | `d_menu_capture.inc:79-93` | `0x801DFA04` | screen | accepted: still covers, weave ×F. Optional polish: `ws_persp` here plus a uniform cloth scale (`0x803E5EC0/C4`, `0x803E5F70/74` × k(F), k(F) = √((1.28F)² + 1) / √(1.28² + 1), the growth of the screen's corner radius in the cloth's projection when its aspect goes from 1.28 to 1.28F; k(16:9) = 1.218. The cloth scale words were checked: they are .bss in `l_HIO` `0x803E5EB4` (d_menu_cloth), 0x58-byte children from +8, scale at child+4: DEFAULT `0x803E5EC0/C4` (1.78), CLOTH_ONLY `0x803E5F70/74` (1.7); the file-select child `0x803E5F18/1C` is left out) | — (4) |
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
| 2D-7 | Minimap icon scissor: 2D → EFB with the vanilla box, width 116 px | `d_map.cpp` `calcScissor` | `0x8004A3A4`; its only caller `bl` `0x8004A64C`; x consts `0x803F8234/38/3C`; `li r3,116` `0x8004A428`; result X/W stored at `0x803F6AE4`/`0x803F6AE8` (checked: `stw` at `0x8004A460`/`0x8004A468`; `0x8004A64C` is the only branch to it in the DOL) | element | `hook_branchlink` `0x8004A64C`: call, then if F ≠ 1 recompute X = (int)((map x − L′)·640/(659F)), W = (int)(116/F + 0.5) (87 at 16:9), vanilla clamps. It follows stage 3's map move by itself | 2 |
| 2D-8 | Full-EFB passes (3.2) and the shadow render target | `m_Do_graphic.cpp`, `d_drawlist.cpp` | — | shared | none | — |
| 2D-9 | Render mode, EFB/XFB 640x480, viewport/scissor resets | `m_Do_machine.cpp:393-444` | `0x803716F0` | shared | none; never write `g_HIO+0x2C/+0x2E` | — |
| 2D-10 | 2D particles and the 2D J3D list | `m_Do_graphic.cpp:1894-1922` | 2D draw-info 4/3 `0x803F7DA4` read at `0x8000BA70` | shared | follow the hooked graf; the 4/3 only feeds projected texgen | — |
| 2D-11 | `dDlst_2Dtact_c` / `dDlst_2DCopy_c` EFB copies | `d_msg.cpp`, `d_message.cpp` | — | — | dead in USA, never drawn | — |
| 2D-12 | GX points and lines sized in px (minimap dots) | `d_map.cpp` | — | element | accepted (×F wide) | — |
| 2D-13 | Clip-to-parent screens | `J2DScreen+0xCC` | — | rule | move the screen, never widen a child (3.3) | 2, 3 |
| 2D-14 | Cinema/vista trims 52/65 (16:9 vista ≈ 2.27:1) | `d_cam_param.cpp:327-328` (`dCamSetup_c` ctor, copied into `dCamera_c::mCamSetup` +0x60C, fields +0x64/+0x68) | data `0x803F90F0`, `0x803F90F4` (read only at `0x800AFE14`/`0x800AFE1C`, the ctor) | element | optional smaller trims in mode 2: write the live camera's `mCamSetup` fields (the .sdata2 words only reach cameras built afterwards) | 4 |
| 2D-15 | Scope/picto opening: the 3D view rect follows the 2D wipe scale in EFB px | `d_scope.cpp`, `d_camera.cpp` | — | element | only if visible, and only under option B: under option A the viewfinder art is drawn uncompressed and already matches these EFB rects. Then `hook_branchlink` the `bl dCamera_c::setView` (`0x801675E8`) at `0x80170EE8` (`subjectCamera`, `d_camera.cpp:3962`; f1 x, f2 y, f3 w, f4 h): keep the centre's offset from 320 scaled by 1/F, and w′ = w/F. That call is only the opening tween (picto aim or demo). `subjectCamera` has two more: `0x80171084` (160, 35, 320, 320), the static rect once the tween ends, which needs the same treatment or the view jumps, and `0x801710B4` (0, 0, 640, 480) | 5 |

### 4.3 HUD (in-game)

Anchor rule: a part whose vanilla centre is left of 320.5 moves −S, right of it +S, unless it is
centred by design (boss bar, prompts), placed at a projected point each frame (the enemy HP bar `enek ene1-3`), kept beside the centred pause menu (`l r`) or follows something else (the wind arrow follows
the minimap). S = 329.5(F − 1) keeps each part's vanilla distance from its edge. Positions come from
the vanilla `.blo` files parsed from the disc.

| # | What | Where | DOL / data | Kind | Anchor | Fix | Stage |
|---|---|---|---|---|---|---|---|
| H-1 | Hearts `ht00-19 hk00-19 htfl`, magic `mbb* mbr* mbk*`, compass `cmb1`, D-pad `crs1 ylef yup yrig` | `d_meter.cpp`, `main_parts1.blo` | row | shared | left | `main_parts1` BY_CX | 3 |
| H-2 | Buttons A/B/X/Y/Z/R, counts, action plates, keys `kyl1/2 nm03/04/08/09 key0` | `main_parts1.blo` | row | shared | right | BY_CX (centre x > 320.5) | 3 |
| H-3 | The NONE list in `main_parts1`: boss bar `ben1-4 enem enm2 bey1 bey2`, enemy HP bar `enek enk2 ene1-3` (placed each frame at the projected lock-on target by `dMeter_zakoEnemyMove`, H-14; Orig only gives offsets between its panes), pause hints `l r` (x 58..88 and 554..584: kept beside the centred 4:3 pause menu, slid ±20 by `dMeter_menuLRMove`), wind arrow `wiy1` (placed from the map's position each frame, d_meter.cpp:4805-4813) | `main_parts1.blo` | rule NONE | element | centre / follows map | exceptions: under BY_CX every boss-bar pane would go right (`ben1` 461.5, `ben2-4` 342, `enem`/`enm2` 481, `bey1/2` 474/488), the enemy HP bar left (`enek` 264, `ene1-3` 262), `l`/`r` to the edges, and `wiy1` would move twice (once by the row, once with the map) | 3 |
| H-4 | Rupee counter | `main_parts2.blo` | row | shared | right | ALL_R; its sparkle (pane centre − 320, also `rupyAlpha`'s respawns) follows | 3 |
| H-5 | Clock and its sun/moon/cloud rects | `main_parts3.blo` | row | shared | left | ALL_L; `dDlst_2Dm_c` rects come from `cmtm`'s Orig | 3 |
| H-6 | Swim/air meter `msk1 main soul kg00` (scissored) and `wk00 hk11-33` + shine | `time_swim_1/2.blo` | rows | shared | right | SCR_R / ALL_R | 3 |
| H-7 | Minimap corner, free-camera icon | `d_meter.cpp:7657-7743` | `g_meter_mapHIO` `0x803E68DC`: +0x8 (`0x803E68E4`, 35), +0x14 (`0x803E68F0`, 590) | element | left / right | `ws_apply`: 35 − S, 590 + S | 3 |
| H-8 | Minimap hide-slide target: the hidden map shows in the widened left band | `d_meter.cpp:7733-7743` | +0xC (`0x803E68E8`, −180) | element | left | `ws_apply`: −180 − 329.5(F − 1) (from the 2D factor, not the HUD flag) | 2 |
| H-9 | Minimap scissor | 2D-7 | | element | follows map | 2D-7 | 2 |
| H-10 | Place-name card `pn` | `place_name.blo` | rule | element | left | `pn` L | 3 |
| H-11 | Minigame timer's rupee/item icon at x = 32 (`setPaneInitialPos` overwrites Orig, so no row) | `d_timer.cpp:535-555, 651, 681` | `g_menuHIO+0x9A` `0x803E6B06` (s16, 0) | element | left | `ws_apply`: −S | 3 |
| H-12 | Cannon game: 10 bomb icons x = 30, 5 ship icons x = 540, angle/shot battery | `d_a_canon.cpp`, `d_2dnumber.cpp` | vtable slots `0x80389100` (`dDlst_2DObject_c::draw` `0x800C946C`), `0x80389110` (`dDlst_2DBattery_c::draw` `0x800C90F0`) | element | left / right | REL `_prolog` writes the slots to wrappers and `_epilog` restores them (first thing). Each wrapper reads the latched `ui_shift` S, adds it, calls the vanilla draw (absolute pointer: `0x800C946C`/`0x800C90F0`), then restores. 2DObject: `mPos.x` (+0x0C) += (x < 320.5 ? −S : +S). Battery: `mPicturePos.x` (+0x28) and `mNumPos.x` (+0x30) += S | 3 |
| H-13 | Sploosh Kaboom score/shot numbers | `d_a_mgameboard` (stub REL; `d_a_mgameboard.h`: `dDlst_2DNumber_c* mpNumber0/1`, `dDlst_2DObject_c* mpSquidIcon[3]`) | vtable slot `0x80389130` (`dDlst_2DNumber_c::draw` `0x800C8644`); the squid icons share H-12's `0x80389100` slot, so H-12's wrapper already moves them (sign from each object's stored x) | element | unknown | screenshot first; wrap `0x80389130` the same way if the numbers are edge-placed | 3 |
| H-14 | 2D at projected points: boomerang sights, aim reticle, enemy HP bar, fly gauge | 3.4 | 3.4 | element | follows point | `ws_project_2d` | 2 |
| H-15 | Telescope bars (world shows beside the scope once 2D is compressed) | `d_scope.cpp`, `wipe_00/_2.blo` | rows | screen | full | EDGE; `dScp_wipeMove` scales all eight panes by the iris rate (mSize = mSizeOrig × rate) and puts each side bar's inner edge on the iris (`wp07` = `mWpxxPanes[6]`, `wp06` = `[7]`), so widened bars follow the iris. Fallback: fill outside the live `wp06/wp07` edges from a hook at `bl outFontDraw` `0x802375CC` | 2 |
| H-16 | Wind's Requiem dim mask | `operate_wind_mask.blo` | row | screen | full | GEOM (fallback `ws_screen_draw_full` at `0x8021E6EC`) | 2 |
| H-17 | Auction: rolling bid digits (scissored), rupee icon and plates top-left, gauge centred | `d_auction_screen.cpp`, `auction1/2.blo` | rows | screen | left / centre | `auction1` SCR_L; `auction2` tags L | 3 |
| H-18 | Centred by design: boss HP, choice arrows, baton beat strip, wind ring, 3-2-1-GO, timer bar, Z-target (a 3D model), item get | various | — | — | centre | none | — |
| H-19 | Minigame result screen (`ship_race2.blo`, `dMinigame_Terminater_c`, stub; created by `d_a_goal_flag`) | `d_minigame_terminater.cpp` | `_create` 0x80206FC0.., draw 0x8020A4D8 | screen | unknown (expected centre) | screenshot first (boat race finish); a row only if it has full-cover or edge panes | 3 |

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
| T-6 | Picto album private ortho | `d_picture_box.cpp:710-781` | `0x803FB81C..28`, `bl` `0x80227BB8` | screen | option A: none (its slot panes are in `wipe_01_01`, drawn uncompressed); option B: widen about 320.5 by F | 5 |
| T-7 | Picto capture rect and freeze-frame | `m_Do_graphic.cpp` (`mDoGph_screenCapture`, `setUpRectangle`) | `mDoGph_screenCapture` `0x8000AC3C`, `mCaptureProc` `0x8000AAC4`, `setUpRectangle` `0x8000A530`, `bl C_MTXOrtho` `0x8000A6C0` | screen | option A: none (anamorphic photos); B/C in 3.6 | 5 |
| T-8 | Photo browse/get views | `d_picture_box.cpp` | `0x80227A30` (a `bl GXSetViewport` (0, 0, 640, 480), not a function entry; `0x80227CF8` restores the saved viewport) | screen | option A: none; B: x += w(1 − 1/F)/2, w /= F. Never together with T-6's widened `pictureDraw` ortho: each compresses by F, so both give F² | 5 |
| T-9 | THP movies `title_loop.thp` (title screen) and `end_st_epilogue.thp` (the staff roll), 640x480 with their own ortho | `d_a_movie_player.cpp:4206` (loose Yaz0 REL, loaded before ours) | REL `.bss` + `0xCB580` (xSize), + `0xCB59C` (DrawPosX), from the disc map and the decomp's REL symbols.txt (module id 415 from the REL header); the runtime effect is still a live check | screen | T2 `frame_any` step (stage 4 step 1): with F = `ws_2d_factor()` (latched, so the movie matches the rest of the 2D; `main_parts1` also loads with the title stage), target w = 640 if F is 1, else 2·round(320/F); if `xSize` is 640 or the last w written and differs from the target, write w and `DrawPosX` = (640 − w)/2. Never touch another value | 4 |
| T-10 | Game over, Wind's Requiem, opening | 4.4, H-16 | | | | 2 |
| T-11 | Tingle Tuner TV side, JUT debug console | | | | none | — |

### 4.6 Our own drawing

See 3.7: name tags and lobby line (REL, stage 2), the sampler (Go, stage 1), sea chart markers and
3D visuals (nothing).

### 4.7 Dolphin's "$16:9 Widescreen" Gecko code, line by line

The code (Dolphin `Data/Sys/GameSettings/GZLE01.ini:348-452`, disabled by default) is anamorphic
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

The culling plan's block (mailbox +0xA6C, `0x8041696C` since lever 4, section 6; `0x8041596C` before) grows from 0x10 to **0x18 bytes** before
#1185 builds it, so no stage changes its layout later. It ends at +0xA84 (`0x80416984`), inside the
free +0xA6C..+0xD00 slack; `CanaryArenaHigh` moves from +0xA6C (0x294 bytes today) to start at +0xA84 when #1185 adds the block (`wwapi/mailbox/mod.go`,
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

**`ws_latch()`** runs from `ws_init` (every boot, including a reboot reset; the usual reset to the title runs no init, docs/05 "Soft reset") and from hook B when an EPOCH row
loads. It sets `ui_mode` = 2 if mode is 2 with a valid factor other than 1 (`ws_live_factor() != 1`) and `WWWS_F_2D` is set, else 0 (at F = 1 compressing changes nothing; Go's `Widescreen.Pending2D` uses the same test, `Wants2D`); `ui_factor` and
`ui_shift` to match; then runs `ws_apply(ui_factor, ui_shift)`. Hook B's order: `set(stream)` returned true → look up the stashed name's row → if the row has EPOCH, call `ws_latch()` first → then apply the row's GEOM/EDGE (stage 3: shifts) with the new latch → clear the stash.

**`ws_apply`** walks one rodata table of `{u32 addr; u8 type; f32 a; f32 b}` and writes
a + b·F (or a + b·S), rounded for s16 entries (`type` bit 0 = s16, rounded half away from zero, else
f32; bit 1 = b multiplies `ui_shift`, else `ui_factor`; stage 2 step 1 lists the rows). At F = 1 every entry gives back the vanilla word, so
leaving native mode needs no restore code. Entries: the wipe box (4), Fd4 (2), the minimap hide
target (stage 2), the minimap corner and free icon and `g_menuHIO+0x9A` (stage 3, from S).

**`ws_2d_factor()`** is the one predicate every 2D reader uses (`ws_ortho_hook`, `ws_project_2d`,
`ws_persp`, the scissor wrapper, the side fills, the walker, the THP step, and the REL's name tags): `ui_factor`
if the magic matches, `ui_mode` is 2 and 1 ≤ `ui_factor` ≤ 3 (NaN fails), else 1.0. The 3D knob and
the picto judge use the live `mode`/`factor`.

**`ws_init`** writes the block (mode 1, factor 4/3 as the culling plan says, flags = supported,
`ui_*` from `ws_latch`) and the knob `0x3FAAAAAB`. It never defaults to mode 2: C can't see
Dolphin's settings, and mode 2 with the hack on widens twice at any factor but 4/3.

**Module shape:** the culling plan's `WwModuleDesc` entry `{ 'WS  ', ws_init, ... }` gains a
`frame_any` step from stage 4 (THP: the title movie plays before Link exists, and `frame` runs only with Link, past the 300-frame gate in hooks.c `multiplayer_update`). Hook sites stay in `hooks.c` (core owns hook sites); the mod's
functions sit in `inject/src/ws/widescreen.c` and are declared in `mods.h`.

## 6. Blob budget

**Room.** Before #1185, T2 ran `0x80410000..0x80413408` and its budget ends at the API region at
`0x80414000` (`inject/include/wwops.h`): 0xBF8 bytes free. **Measured after stage 1 (PC build,
2026-11-09):** T2 runs `..0x80413728` (`internal/inject/blob.go` `T2End`), **0x8D8 bytes free**. (The
card's "0x478" was the figure after E3, before wind#80 and wind#90.) #1185 was planned at about
0x80-0x100; with stage 1 it took 0x320 (the table below). T2's `.bss` counts too: latched state
lives in the block, saved boxes on the stack.

**What a hook costs.** `hook_branchlink` rewrites the site's `bl` to point straight at the C
function (no trampoline): one rewritten 4-byte instruction (a 1-4 byte run in `DOLPatches`, which stores byte diffs) and 0 bytes of T2. Sites that call the same
original with the same registers share one function (`setOrtho` ×4, `mDoLib_project` ×7,
`C_MTXPerspective` ×2). Measured today: naked forwarders 0x2C-0x50 (`exec_shim` 0x2C, `frame_shim`
0x48, `seachart_shim` 0x50), small C hooks 0x38-0xB0 (`pad_read_shim` 0xB0). The estimates below
count code ×1.5 at the high end (E3 once overshot by 0x4E0); table data is exact.

| Stage | T2 bytes | Contents | Running total with #1185 |
|---|---|---|---|
| 1 | **0x320 measured** with #1185 (planned 0xE0-0x1C0 for both) | `ws_op_set` 0xB8, `ws_init` 0x88, `ws_live_factor` 0x68, `ws_clip_aspect` 0x5C, `clip_setup_hook` 0x58, `ws_snap_setresult` 0xAC (built `-Os` for that function; `-O2` unrolled it to 0x114), 0x308 in all, plus the module table entry and alignment | 0x320 |
| 2 | 0x540-0x810 | `ws_2d_factor` + latch, `ws_ortho_hook`, `ws_apply` + 7 entries, hooks A/B + walker + 27 rows and 12 EDGE rules, `ws_project_2d`, scissor wrapper, `ws_screen_draw_full` (2 entries), `ws_persp`, opening fills | 0x860-0xB30 |
| 3 | 0x140-0x1C0 | shift kinds (code 0x50-0x88), 7 rows + 27 rules (`main_parts1`'s 15 NONE tags, `pn`, `auction2`'s 11) + 3 `ws_apply` entries (data ≈ 0xEF); +0x80-0x120 if the cannon/Sploosh wrappers stay in T2 instead of the REL | 0x9A0-0xCF0 |
| 4 | 0x70-0xD0 planned; **0x1A0 measured with stage 5** (`ws_frame_any` 0x158, `ws_project_live` 0x4C) | THP frame step, optional polish (flare, stars) | 0xA10-0xDC0 planned |
| 5 | 0x20-0x200 planned; nothing more (option A; the telescope shares `ws_project_live`) | telescope look-in; picto option B or C if chosen (0x78 if B's resample lives in the REL behind a forwarder); option A needs nothing | measured: T2 ends `0x80414788`, 0x878 free (lever 4) |

The running totals are from the measured 0x320. Stages 2-3 need 0x680-0x9D0 on top of it, and
0x8D8 is left: they fit at the low end, not the high. So the levers below are chosen at stage 2
(the rule below).

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
   to 0x1B0 of the 0x27C slack; like the WWWS block it must be carved out of `CanaryArenaHigh`: move its start past the table in `wwapi/mailbox/mod.go`, update `TestModRegions`, and add a C header + `_Static_assert` for the region, or the canary probe reports Go's own writes); rows can then be tuned without a blob rebuild.
4. Move the API region, the mailbox and ArenaLo up 0x1000 (ArenaLo `0x80416000` → `0x80417000`;
   build.py's `addi r3,r3,0x6000` at `0x80301820` becomes `0x7000`; the sweep also fixes `wwapi/mailbox/mod.go`'s stale "addi r3,r3,0x4000" comment): T2's budget becomes 0x1BF8. It costs 4 KiB of
   ZeldaHeap (the archive and game heaps are fixed, `m_Do_machine.cpp:551, 593`), a new REL id and a
   sweep of about 44 files with literal addresses (wind#80's precedent). Measure ZeldaHeap headroom
   on Outset, Windfall and the open sea first.

**Rule:** after stage 2 first compiles, read `TestT2EndsBelowMailbox`'s free-bytes line. If fewer
than 0x500 bytes are free (stages 3-5 at their high end need 0x488), apply levers 1-3, and lever 4
in R2 if that is still not enough or another card has T2 work queued.

**As built (stages 2-3, PC, 2026-11-09): lever 4 is needed.** Stage 1 left 0x8D8 free. Stages 2 and 3 take 0xEC0 (T2 now ends at `0x804145E8`, 0x5E8 past the API region), and stages 4 and 5 still need about 0x200. So the gap is 0x7E8.

**What is already applied:**
- lever 1 (the global GEOM rule, section 3.3);
- lever 2's REL half for the cannon and Sploosh wrappers (`inject/rel/ws.c`);
- every hook body built `-Os`;
- every game function called with a plain `bl`, through build.py's `GAME_SYMBOLS` and `ld --defsym`, instead of a 16-byte `lis/ori/mtctr/bctrl`.

**Where the 0xEC0 goes,** from `inject/build/WWMultiplayer.map`:

| Part | Bytes |
|---|---|
| The walker | `ws_walk` 0x264, `ws_screen_loaded` 0x1D0, `ws_name_hash` 0x5C |
| The latch | `ws_latch` + `ws_apply` 0x138, `ws_2d_factor` 0x64 |
| Hooks | `ws_full_draw` and its entries 0x138, `ws_map_scissor` 0xF8, `ws_ortho_hook` 0xAC, `ws_opening_draw` 0xAC, `ws_persp` 0x5C, `ws_project_2d` 0x44, hooks A/B 0x40 |
| Data | the table 0x12B, `ws_apply_tbl` 0x78, the box 0x10, constants |
| `-Os` save/restore helpers from libgcc | 0xFC |
| Alignment | the rest |

**What levers 2 and 3 could still recover is about 0x370-0x470, short of 0x7E8:**
- **Lever 3**, the Go-written tables: at most the table and `ws_apply_tbl` together, 0x1A3, less the loads that replace them.
- **Lever 2 extended.** The gameplay-only bodies (the scissor, the picto draws, `ws_project_2d`, 0x27C) could move to the REL behind four exec_shim-style forwarders (4 × 0x28). That saves about 0x1DC, but leaves those features vanilla for the first 300 frames after Link exists.
- **Dropping `-Os`.** This removes the 0xFC of helpers, but `-O1` measured 0xC0 larger overall.

So lever 3 was not built. Lever 4 (item 4 above) is the next step, after its ZeldaHeap headroom check on Outset, Windfall and the open sea.

**Lever 4, as built (PC, 2026-11-09, branch `widescreen-2-5`).** The API region (`WWAP_ADDR`) is now
`0x80415000`, the mailbox `0x80415F00`, ArenaLo `0x80417000` (build.py's `addi r3,r3,0x7000` at
`0x80301820`); `frame_shim`, `exec_shim` and `seachart_shim` load `0x5F08`, `0x60BC` and `0x67D4`
off `lis 12,0x8041`. Every mailbox-relative offset and `WWMB_LAYOUT` are unchanged. T2 still ends at
`0x804145E8`: **0xA18 bytes free**, enough for stages 4-5 (about 0x200). The literal addresses
were swept in the C headers, Go constants and tests, scripts and the reference docs; dated records
(docs/live, docs/acceptance, docs/05, docs/06, passed checks) keep the addresses of their day.
`scripts/extract_blob.py` now refuses a T2 that ends past `WWAP_ADDR` (blob.go not written) and
emits it as `APIRegionAddr`, and `PatchDOL` refuses such a blob too, so an over-budget build can't
reach a disc. **The ZeldaHeap gate passed** (PC rig, 2026-11-09, docs/TESTING.md WS2-0): the
error counters `zelda`, `game` and `archive` stay 0 on two Dolphins through boot, Outset, Windfall
by day and night, the open sea, DRC, M_DragB, kindan and Hyrule with mp-local running; ZeldaHeap is
6094 KiB, with at least 788 KiB free at every stop. The PC start save is recaptured
(`fixtures/start-ws2-9E6314D8.sav`, REL 0x47408CD2); **still owed before R2:** the Mac start-save recapture.

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
card on the app's Settings page (`gui.Bridge` setter, validated by the mod's `ParseAppSetting`).
Presets: 16:9, 16:10, 21:9 (= 7:3, F 1.75). Custom takes integers W:H with W/H in (4/3, 4]; at or
below 4:3 it counts as Off. "21:9" means the literal 21:9 everywhere (the culling plan's table said
64/27; a 2560x1080 player picks Custom 64:27, 3440x1440 picks 43:18).

**Default.** 16:9, as the card asks, **from the release that carries stages 2+3 (R2)**. In R1 an
unset setting means Off: stage 1 alone leaves the HUD stretched, and a 16:9 default would turn a
4:3 player's window wide with a stretched HUD. One Go constant flips it.

**Off** means the culling plan's behaviour: no graphics `-C`, never mode 2; mode 1 from C's
default (and #1185's `widescreen` command or its later automatic writer).

**Launch arguments** (`launch.WidescreenArgs(setting)`, appended through a new
`session.LaunchOptions.Widescreen` that `gui.launchOptions` fills and the engine's `launchDolphinFor` (`mods/multiplayer/engine/game.go`) passes on. `launch.Launch(exe, userDir, iso, saveState)` and `launch.LaunchArgs` take no extra arguments today, so they get an extra-args parameter; wwdev's `LaunchTest`/`TestArgs` do too; wwdev reads
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
  catches it only outside mode 2 or at F ≠ 4/3; also check the painter box word `0x803F7D68` = `0xC1100000`, which the Gecko rewrites to `0xC2F60000` and we never write, so it detects the Gecko in every mode).

Otherwise it sends mode 1 with the hack's factor (the culling plan), or mode 0 when the Gecko code
is on. A mismatch between what was asked and what Dolphin runs shows one line on the Play page.


**Who submits.** The engine's per-player Dolphin runner (`mods/multiplayer/engine/game.go`, which
already launches and probes Dolphin) owns one `ops.Queue` per PID; nothing else in the app submits
today (`cmd/wwdev/ops_cmd.go`'s note about "the app's Game" is stale). It starts at the first probe
where the mod is present and the queue is `Ready()` (the logo or title: `ops_frame` runs before the
Link check). Each tick it reads the mode byte (`0x80416971`) and factor (`0x80416974`) and resubmits
WS_SET (flags 0xFF) on any difference. It does not compare the flags byte, so a tester's
`--flags 0` survives until the next resubmit. The loop heals reboot resets (the DOL reloads and `ws_init` restores mode 1 and the knob; the usual reset to the title reloads nothing and keeps mode 2, docs/05 "Soft reset"), save states taken in
another mode and a stray CLI toggle. It polls about every 100 ms until the first apply, then every
second, and moves to `Game.Ops()` once the mod runtime has a Game, after waiting for its own last ticket and dropping its queue (never two queues submitting at once). #1185's CLI `widescreen` command
refuses while a session holds the mailbox (`shadow_mode` set). CLI `host`/`join` and the TUI don't
launch Dolphin and keep #1185's command and its later automatic writer.

**The boot window.** The logo draws in mode 1 for under a second. If WS_SET lands after the title
screen loaded, the title keeps the old 2D (the latch moves at file select) and the 3D widens at
once. `main_parts1` also loads with the title stage (USA `dStage_playerInit` creates the title and
the meter together, `d_stage.cpp:1488-1496`), so a WS_SET that lands before the title stage loads
latches there and the title's 2D is right. Cosmetic either way; testers should not file it. Stage
2's boot-timing check records which case the app hits.

**Mid-session changes.** Dolphin's Toggle Aspect Ratio hotkey (`HotkeyScheduler.cpp`,
`Config::SetCurrent`, which beats `-C`) and its Graphics window change the output aspect live, and
Go can't see either. Mode 2 then shows a wrong picture until the next launch. Accepted: the Play
page's Display line says to change the aspect in the app and relaunch. A later card may sample
the render window's shape (`host.RenderSize`) as `xscaleSampler` does.

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

**Conventions for every stage.**
- **Where code goes.** Every function named in a `hook_branchlink` line goes in `hooks.c` (core
  owns hook sites). The helpers they call (`ws_2d_factor`, `ws_live_factor`, `ws_latch`,
  `ws_apply`, the walker and its table) go in `inject/src/ws/widescreen.c`, declared in `mods.h`.
- **Pin tests.** Each new hook site gets one in `internal/inject/t2_budget_test.go`, as the culling
  plan's step 4 pins `clip_setup_hook`: build.py has `proj.hook_branchlink("<fn>", 0x<site>)`, and
  `DOLPatches` holds a `bl` (opcode 18, LK = 1) at the site's DOL file offset whose target lies in
  T2. No instruction-word pins (compiled C moves).
- **Float words.** C computes every value in f32 from the factor word, so some results differ from
  the exact-math words in this doc by an ulp (section 2's note). Tests recompute the same f32
  expression; they never pin a hex word unless this doc says it is the computed one (3.1's table is).

**Test rig, every stage.** The Mac rig (docs/mac-rig.md; take a `ww/dolphin-slot-<n>` lease) or the
PC two-Dolphin loop, with a start save recaptured for the stage's blob (in mode 1).

- **Native output.** Stage 1 makes `WW_WIDESCREEN=<setting>` work for `wwdev dolphin2` (it adds
  the `-C` pair) and for `scripts/mac-rig.sh` (its `launch_on` appends the same pair and any words in
  a new `MAC_RIG_DOLPHIN_ARGS`). Before stage 1's Go lands, set `wideScreenHack = False` and
  `AspectRatio = 1` in the rig user dir's `Config/GFX.ini [Settings]`.
- **Setting the mode.** Outside the app nothing sends WS_SET by itself. Stage 1 adds:
  `./wwdev.exe ops ws <mode> <aspect> [flags]` (submits op 16 and waits; one writer, so not while
  another runs), and an mp-local that reads `WW_WIDESCREEN` and runs the same reconcile loop as the
  app's runner (the CLI `widescreen` command refuses while mp-local holds `shadow_mode`). In the
  app, the runner does it (host a lobby, Start). Nothing else in mp-local submits to the ops queue
  today, so its loop is the only writer there.
- **Manual toggles** (a mode or flags change mid-run): launch with `WW_WIDESCREEN` on `dolphin2`
  (the `-C` pair) but run mp-local **without** it, so no loop resubmits behind you, and use
  `wwdev ops ws`, never the CLI, once mp-local holds the mailbox. Checks that need mp-local's loop
  (stage 2 check 10) are a separate run.
- **Pictures.** `WW_DOLPHIN_INDEX=0 ./wwdev.exe screenshot /tmp/ws.png` (PC; the stretched window, as
  the player sees it) or `scripts/mac-rig.sh shot <pid> /tmp/ws.png`. If the Mac agent lacks Screen
  Recording: frame dumps with `-C Dolphin.Movie.DumpFrames=True -C Dolphin.Movie.DumpFramesSilent=True
  -C Graphics.Settings.DumpFramesAsImages=True` (default dumps use the window's aspect; a Raw dump is
  pre-stretch, for measuring 2D at 320 + (x − 320)/F). In a 16:9 picture the vanilla 4:3 area spans
  1/8..7/8 of the width.
- **Readouts.** `-C Graphics.Settings.OverlayProjStats=True` prints Dolphin's projection ratios
  (gameplay ortho ≈ 0.94 uncompressed, ≈ 1.26 compressed at 16:9). `./wwdev.exe peek 8041696C 24`
  reads the block; `./wwdev.exe ops status` the queue's done/refused counters.
- **Items.** Screens that need items (telescope, picto box, boomerang, bow, Deku Leaf, baton) need a
  save that has them; the start save may not, so note which checks were run. Pictures go on the
  card, not in git.

### Stage 1: world and culling (with #1185, release R1)

Changes the C side, so it lands in **#1185's blob change**: follow the culling plan's section 8
(as corrected in section 9) with these additions.

**Built on card #1185** (PC build, 2026-11-09, BlobHash 0x1100504D): items 1-4, and from item 5
the `ReadGFXAspect` fixes, the launch args (`LaunchWith`, `LaunchTestWith`,
`session.LaunchOptions.Widescreen`, `WW_WIDESCREEN` for `dolphin2`), `ParseAppSetting`, the CLI's
`widescreen native` and `wwdev ops ws`. The rest of item 5 (the Settings card, the mode policy
and its runner, the Gecko check, the `xscaleSampler` change, `stage-hop --wipe`) is Go only and
lands apart from the blob (no re-patch). Hooks:
`clip_setup_hook` at `0x8017BF90` (DOL file offset `0x178ED0`), `ws_snap_setresult` at
`0x800CE5E8` (DOL `0xCB528`), both in `hooks.c`. The block at
`0x8041696C` since lever 4 (+0xA6C..+0xA84, mode byte `0x80416971`, factor `0x80416974`; 0x1000 lower in the #1185 build), op 16
`WWAP_OP_WS_SET`, `WW_FEAT_WIDESCREEN` 0x80 (`WW_FEATURES` 0xFF), `CanaryArenaHigh` from +0xA84.
T2 cost 0x320 (section 6). The live checks below are owed: docs/TESTING.md "Widescreen stage 1".

1. **Header** (`inject/include/wwws.h`): the 0x18 layout of section 5, `WWWS_F_2D` 0x01,
   `WWWS_F_HUD` 0x02, `WWWS_FLAGS_SUPPORTED` 0x00, `WWWS_MODE_NATIVE` 2, `WWWS_KNOB_ADDR`
   0x803FA998, `WWWS_KNOB_VANILLA` 0x3FAAAAAB; the static asserts check `WWWS_OFF + 0x18`. The
   feature bit is `WW_FEAT_WIDESCREEN` 0x80 and `WW_FEATURES` 0xFF (0x40 is `WW_FEAT_OPS`).
2. **C** (`inject/src/ws/widescreen.c`): WS_SET takes c = flags (stored `& WWWS_FLAGS_SUPPORTED`)
   and writes the knob: `(4.0f*factor)/3.0f` in mode 2, `WWWS_KNOB_VANILLA` otherwise. `ws_init`
   writes the knob vanilla and `ui_*` to 0 / 1.0 / 0. `ws_live_factor()` returns `factor` when the
   magic matches, mode is 2 and 1 ≤ factor ≤ 3, else 1.0. `ws_clip_aspect` widens only in mode 1.
3. **Picto judge** (T-4): `void ws_snap_setresult(void* pkt)`, `hook_branchlink` at `0x800CE5E8`
   (the `bl SetResult` `0x800CEB80`, word `0x48000599`, DOL file offset `0xCB528`; r3 is the
   `dSnap_packet*` from Judge's `mr r3,r31` at `0x800CE5E4`). If `ws_live_factor()` ≠ 1: for
   col < `*(s32*)(pkt+0x14)`, scale the s32 at `pkt + 0x38 + col*0x3C`
   (`m_tbl[col].m_obj.mCapturedPixels`) to `(s32)(px*F + 0.5f)`; leave `mCapturedRatio` alone.
   Then call `SetResult`.
4. **Go mirrors:** `wwapi/mailbox/widescreen.go` with the 0x18 layout and `Decode`; a pin test
   against `wwws.h`; `CanaryArenaHigh` from +0xA84 (`TestModRegions`, size 0x27C).
5. **Go** (can land before the blob; it does nothing until the blob ships):
   - `ReadGFXAspect` fixes (section 7): the `Graphics` prefix, `SuggestedAspectRatio`, custom 1:1,
     with tests. The VI aspect for the Stretch modes only after check 11 measures it.
   - Launch args: `launch.WidescreenArgs(setting)`; `launch.Launch` and `launch.LaunchArgs` take no
     extra arguments today, so add an extra-args variant. The app: `session.LaunchOptions.Widescreen`,
     filled by `gui.launchOptions`, passed by `engine.launchDolphinFor`
     (`mods/multiplayer/engine/game.go`). wwdev: `WW_WIDESCREEN`, read only by `dolphin2`
     (`cmd/wwdev/mp_local.go`), passed as a new extra-args parameter of `launch.TestArgs` /
     `LaunchTest`; `auto-recapture` passes none. `scripts/mac-rig.sh` `launch_on` as in the rig notes.
   - The setting: `ParseAppSetting` in `mods/widescreen` ("16:9", "16:10", "21:9", integer "W:H"
     with W/H in (4/3, 4], "off"; no `auto`, no decimals: Dolphin's custom size is integer),
     separate from #1185's CLI `ParseSetting`. Unset = off in R1. The Settings page "Display" card
     and its Bridge setter; the `cmd/ww-multiplayer` goldens.
   - The mode policy (section 7) as one function in `mods/widescreen` with a table test. The engine
     runner calls it through a new `GameDeps.Widescreen` seam set in
     `cmd/ww-multiplayer/engine_config.go` (as `CoopSync` is), never by importing the mod; its
     reconcile loop over `ops.Queue` is tested with `wwapi/ops/opstest`. mp-local runs the same loop
     when `WW_WIDESCREEN` is set.
   - The Gecko check: the knob word and the painter box word (section 7), plus the GameSettings and
     `-C Dolphin.Core.EnableCheats` scan, with a Play-page warning line.
   - `xscaleSampler`: give it the block's mode byte from puppet-sync's tick and write XScale 0 when
     it is 2 (only matters when the CLI forces mode 2 with the hack on).
   - #1185's CLI: `widescreen native <aspect> [--flags N]` (mode 2, flags 0xFF by default). Every
     form refuses while a session holds the mailbox (`shadow_mode` non-zero, as
     `engine.mailboxClaimed` reads it).
   - wwdev: `ops ws <mode> <aspect> [flags]`; `stage-hop --wipe N` (it always writes wipe 0 today,
     `coop_diag.go` `stageHopWipe`; 2 gives OVERLAP2, 4 OVERLAP4).
6. **Live checks (Dolphin)** (WS-1..WS-11 in docs/TESTING.md, unchecked):
   1. Before the build, with today's blob: hack off, Force 16:9, `./wwdev.exe ops ws 0 16:9` first
      (on a stage-1 blob the boot's mode 1 multiplies the poked knob into the clipper: 2.2756), then
      `./wwdev.exe poke-u32 803FA998 3FE38E39`. The world is round, not wide; `peek 803A575C` reads `0x3FDA740D`. Cross two stage
      loads and the pause menu, then `peek 803FA998` (still `3FE38E39`): a single store holds under
      the JIT. `poke-u32 803FA998 3FAAAAAB` restores it.
   2. With the build, at 16:9 (from the app, or `wwdev ops ws 2 16:9` before mp-local): the block
      reads mode 2, factor `0x3FAAAAAB`; `peek 803FA998` `3FE38E39`; `peek 803A575C` (`mClipper+0x50`)
      `0x3FDA740D` (3.1's mode-2 value, not the mode-1 `0x3FDA740E`). The culling plan's `cull-probe`:
      no CULL actor in the band is NODRAW. The band is 0.739..0.985 at fovY 60; since branch
      `ws-45` the probe takes it from the clipper (tan(fovY/2) × 1.28 up to the clipper's own side
      plane, or, when the clipper is not widened, the 4:3 edge × the WWWS block's factor, 16:9's 4/3
      without a valid one), so it follows the camera's fovY and the hack's aspect.
   3. Pictures: the world is correctly proportioned, the HUD stretched exactly as with today's hack.
      Sun glare is lost exactly when the sun goes behind something at the screen edge.
   4. Fades: `wwdev stage-hop --wipe 2 <stage> ...`: the first frames of the OVERLAP2 twist fill the
      width, no gaps. Open the pause menu 5+ times (random cloth rotation): the cloth reaches both
      edges every time.
   5. Picto box (if the save has it): a quest-style photo of the same subject in mode 2 and in mode
      1 gets the same verdict.
   6. Lock-on: Z-target an enemy with another beside it near the edge; both stay on screen.
   7. Resets: the usual soft reset in play (`./wwdev.exe input 0x1600 0 0 1500`) returns to the
      title without reloading the DOL (docs/05 "Soft reset"): the block stays mode 2, the knob
      `3FE38E39`. A reboot reset (TESTING.md SR-2's recipe): `ws_init` puts mode 1 and `3FAAAAAB`
      back, and the app's runner resubmits mode 2 within about a second.
   8. Refusals: put `wideScreenHack = True` under `[Video_Settings]` in the test user dir's
      `GameSettings/GZLE01.ini` (that layer beats the app's `-C`), or start Dolphin yourself with
      the hack on and let the app attach: Go stays in mode 1 and the Play page shows the mismatch.
      Enable the Gecko code (`$16:9 Widescreen` under `[Gecko_Enabled]` in `GameSettings/GZLE01.ini`
      and `EnableCheats = True` in `Config/Dolphin.ini [Core]`): `peek 803F7D68` shows `C2F60000`,
      Go picks mode 0 and warns.
   9. `-C` name: over `AspectRatio=1`, launch with `-C GFX.Settings.AspectRatio=2` (window stays
      16:9), then `-C Graphics.Settings.AspectRatio=2` (window turns 4:3).
   10. Regression: the culling plan's step 7 checks in mode 1 with the hack on, unchanged.
   11. VI aspect: hack off, `-C Graphics.Settings.AspectRatio=2` (Force 4:3), a default-resolution
       frame dump; width/480 is the VI aspect (expect about 1.2534, 602x480). Record it in
       `wwapi/emu/launch` before any XScale Stretch change. **Measured** (PC rig, 2026-11-09, branch
       `ws-stage1-live`): Dolphin keeps the width and rounds the height up to a multiple of 4 (1x
       640x512, 4x 2560x2044), so 1.2524..1.2543; recorded as `launch.VIAspect` 1.2534 with
       `VIAspectMin`/`VIAspectMax` (branch `ws-45`). XScale's Stretch cases are unchanged.

### Stage 2: 2D proportions (release R2, with stage 3)

**Built on branch `widescreen-2-5`** (PC, 2026-11-09; first over the T2 budget, back under it since lever 4: section 6 "As built" and "Lever 4, as built"). Items 1-10. The 23 hook sites in `hooks.c` are pinned in `t2_budget_test.go` (`TestWidescreenStage2HookSites`). Each pin checks the vanilla word's callee and that the rewritten bl lands on the named hook (`HookTargets` in blob.go, from the linked ELF); `TestHookSiteVanillaWords` reads each vanilla word from the vanilla disc's DOL; the callee addresses are pinned by `TestGameSymbols`. The `ws_apply` rows are checked at F = 1 (the vanilla words) and at 16:9 (`ws_apply_test.go`). It differs from the items below in these ways:
- `ws_apply`'s entry is `{u32 at; f32 a, b}`: the address's low 24 bits, with the type in the top byte (0x01 s16, 0x02 × S).
- The walker applies a tag's L/R rule at any depth, not only to the anchor's children. Every L/R tag on the disc is a direct child of its anchor (`TestRuleTagsOnDisc`), so the result is the same.
- `ws_screen_draw_full` draws with x = y = 0: all four sites pass `0x803FB7C4` = 0.0.
- The opening fills call `setPort(graf)` first (item 8). `J2DFillBox` builds a temporary `J2DOrthoGraph` that never sets the projection or the vertex state.
- The text-section file offset is address − `0x800030C0`. An earlier verifier note said `0x800036C0`; the offsets it listed are right.

1. **Block and helpers.** `WWWS_FLAGS_SUPPORTED` = `WWWS_F_2D`. `ws_2d_factor`, `ws_latch`,
   `ws_apply`. `ws_apply`'s entry is `{u32 addr; u8 type; f32 a; f32 b}`; `type` bit 0 = s16
   (rounded half away from zero) else f32, bit 1 = b multiplies `ui_shift` else `ui_factor`. Stage-2
   entries (all ×F): `0x803F89B8` f32 (320.5, −329.5); `0x803F89BC` f32 (241, −262); `0x803F89C0` f32
   (0, 659); `0x803F89C4` f32 (0, 524); `0x803FB78C` f32 (320.5, −329.5); `0x803FB77C` f32
   (0, 1.0296875); `0x803E68E8` s16 (149.5, −329.5). At F = 1 they give −9, −21, 659, 524, −9,
   `0x3F83CCCD`, −180.
2. **`setOrtho` hook** (3.2): `ws_ortho_hook`, four sites (`0x8000B06C`, `0x8000BA30`,
   `0x80223CF4`, `0x8003EADC`).
3. **`ws_project_2d`** (3.4) at `0x800E1624`, `0x80129550`, `0x801F8104`, `0x801F9910`,
   `0x8020DF5C`, `0x802229D0`, `0x80222ADC`.
4. **J2DScreen hooks** (3.3): hook A `0x802D0968`, hook B `0x802D09DC`. Hook B's order:
   `set(stream)` returned true → look up the stashed name's row → if it has EPOCH, call
   `ws_latch()` first → apply the row with the new latch → always clear the stash. The walker in
   this stage does GEOM, EDGE and EPOCH only:
   - Absolute x of a pane = its `mBounds.i.x` plus every ancestor's, up to the screen (rotation and
     scale ignored); the same for y.
   - GEOM matches a childless `PIC1` with absolute i.x ≤ 0, f.x ≥ 640, i.y ≤ 0, f.y ≥ 480, and
     moves both x edges to 320.5 + (x − 320.5)F. EDGE moves an edge only if it is at abs x ≤ 0
     (left) or ≥ 640 (right), by the same formula.
   - Write back the new absolute value minus the parent's absolute x, then
     `J2DPane::setBasePosition(p, p->m2DBasePosition)` (`0x802D0800`; it also sets
     `mRotationAxis` to 'z', harmless for these unrotated panes).
   - The table: a Go generator (e.g. `tools/gen_wstable`) writes a C header from a Go list, with a
     freshness test and a collision test over a committed list of every `.blo` name string in the
     vanilla DOL and RELs (`tools/gen_wstable/testdata/blo_names.txt`, made once from the disc;
     `go test ./...` must not need the disc or the decomp). Names are hashed exactly as `set`
     receives them, `.blo` included; tags are C multichar values, right-justified (`'l'` =
     0x6C, `'pn'` = 0x706E, `'wp04'` = 0x77703034).
   - Stage-2 rows: `menu_save`, `wipe_00`, `wipe_00_2`, the 22 GEOM rows, EPOCH on `main_parts1` and
     `main_parts_fileselect`.
5. **Minimap scissor** (2D-7): `void ws_map_scissor(void)` at `0x8004A64C`: call `calcScissor`
   (`0x8004A3A4`); if `ws_2d_factor()` ≠ 1, set `mScissorOrigX` (s32 `0x803F6AE4`) = (int)((
   `mDispPosLeftUpX` (s16 `0x803F6AD8`) − L′)·640/(659F)) and `mScissorWidth` (s32 `0x803F6AE8`) =
   (int)(116/F + 0.5), then redo the x clamp (X < 0: W += X, W = max(W, 0), X = 0). Y and height
   (`0x803F6AE0`, `0x803F6AEC`) stay.
6. **`ws_persp`** (3D-12) at `0x802306A0` and `0x802324A8`: `void (Mtx44 m, f32 fovy, f32 aspect,
   f32 near, f32 far)` forwarding to `C_MTXPerspective` `0x8030DB78` with aspect × `ws_2d_factor()`.
7. **Picto box** (3.6, option A): `ws_screen_draw_full`, `void (J2DScreen* s, f32 x, f32 y, void*
   graf)`, at `0x8022B510`, `0x8022B538`, `0x8022B778`, `0x8022B9B8`: if `ws_2d_factor()` ≠ 1, save
   the graf box (+0xBC..+0xC8), write −9, −21, 650, 503, call `setPort` (`0x802CDCB4`), then
   `J2DScreen::draw` (`0x802D0F2C`), then restore the box and `setPort` again; else just draw. Its
   second entry, `void (void* pb, u8 alpha)` at `0x8022B524` and `0x8022B9CC`, does the same
   around `shutterLineDraw` (`0x8022A09C`), which has no graf argument: it uses the current graf,
   `*(void**)0x803CA908`. The album needs no change (3.6).
8. **Opening fills** at `0x8023452C`: forward to `J2DScreen::draw` (`0x802D0F2C`; r3 scrn, f1, f2,
   r4 graf), then if `ws_2d_factor()` ≠ 1 call `J2DFillBox(f32 x, f32 y, f32 w, f32 h, const u32*
   rgba)` (`0x802CDF3C`; the colour by pointer, a stack u32 `0x000000FF`) at (L′, −21, −9 − L′, 524)
   and (650, −21, R′ − 650, 524), L′ = 320.5 − 329.5F, R′ = 320.5 + 329.5F. Call
   `J2DOrthoGraph::setPort(graf)` (`0x802CDCB4`) before the fills: `J2DFillBox` builds a temporary
   `J2DOrthoGraph()` and never sets the projection, so it draws with whatever state the last pane left.
9. **REL** (3.7): name tags and lobby line read the block through the REL's own copy of the
   `ws_2d_factor` rule (it can't call T2). `"include/wwws.h"` joins `REL_HEADERS` in build.py; the
   RELBuildID changes by itself.
10. **Go:** the runner sends flags 0xFF. The Play page's widescreen line and `ww-multiplayer
    widescreen` with no argument show "2D pending until the next stage load" whenever (`mode` is 2
    and `flags & WWWS_F_2D`) differs from (`ui_mode` is 2), either way. The runner logs, with
    timestamps, its first `Ready()` probe, its first WS_SET OK and the first probe where `atTitle`
    is true. The default stays off until stage 3 ships with it in R2.
11. **Live checks (16:9, then 16:10 and 21:9 by Custom):**
    1. HUD hearts and buttons are round and sit inside the centre 4:3 area; text has vanilla
       proportions. `wwdev ops ws 2 16:9 0` then a stage load: 2D goes back to stretched;
       `wwdev ops ws 2 16:9 1` and a stage load: compressed again.
    2. Pause menu: Items, Quest Status, Map, sea chart, a treasure chart, Options, Save: every dim and
       backdrop reaches both edges; the save iris leaves no gap in the bands; scrolling lists clip
       exactly at their frames.
    3. Game over and Continue; the Wind's Requiem selector: the dim covers the full width, the
       letters and ring are not stretched.
    4. Transitions: `stage-hop --wipe 2` (no one-frame HUD widening at the twist), a cutscene iris
       wipe (round, covers the corners), `stage-hop --wipe 4` (the white fade fills the width).
    5. Reticles: boomerang with 3-5 targets spread to both edges, bow/hookshot at an edge target,
       Z-lock a Bokoblin at each edge (HP bar 50 units beside it), Deku Leaf gauge flying to an
       off-centre Link, talk to an NPC at the far left (the box springs from the NPC), baton swung to
       both extremes (sparkle on the tip). Each sits where it does in a 4:3 run.
    6. Minimap: icons are clipped to the map frame; hide the map (it slides fully out of the left
       band).
    7. Telescope: open, zoom and close in both layouts (`wipe_00_2.blo` when
       `dComIfGp_getScopeType()` is 0, else `wipe_00.blo`, `d_scope.cpp:1291-1297`; note which scope
       gave which). The bands stay black wherever the 4:3 area is, and the bars never lag the iris.
       If they do, use H-15's fallback.
    8. Picto box: the viewfinder looks as today and matches the photo taken; album photos sit in
       their frames and look round where the subject is round.
    9. File select: the cloth panel keeps its side margins around the panels; the sky is round.
       New game: the opening's bands stay black during the `d4` pan.
    10. mp-local with `WW_WIDESCREEN=16:9`: walk B's Link to A's screen edges; A's tag sits over B's
        head with glyphs shaped as at the centre. The lobby line on file select is centred, not
        stretched.
    11. Boot timing: from a cold app launch, read the runner's timestamps in the `-app.log`: whether
        WS_SET lands before the title stage loads (then the title's 2D is compressed) or after.
    12. Regression: mode 1 with the hack on, and mode 0: everything identical to stage 1.
    13. Hash misses: a talk box (`hukidashi_00.blo`, `d_msg.cpp:210`) and the telescope/picto
        message box (`hukidashi_08.blo`) have no row and are unchanged (compressed, centred).
    14. Bands sweep: open and close the pause menu, switch pages with L/R, open the sea chart and a
        treasure chart, play a cutscene that hides the HUD, toggle the map. Note any pane parked or
        sliding outside 0..640 that shows in a band, and add a GEOM/EDGE rule or list it in
        section 10 as accepted.

### Stage 3: HUD to the edges (release R2)

**Built on branch `widescreen-2-5`** (PC, 2026-11-09; under the T2 budget since lever 4, section 6). Items 1-5:
- Flags are 0x03.
- The shift kinds and the S rows were built in stage 2.
- The REL's `relws_obj_draw` and `relws_battery_draw` sit in `inject/rel/ws.c`. `_prolog` links them before the exec-hook word, and `_epilog` unlinks them first (pinned by `TestRelWsVtableOrder`).
- Sploosh Kaboom's numbers: the screenshot item 5 asks for (PC rig, 2026-11-09, WS3-8) showed the shot count left inside while its bombs moved out, so `relws_num_draw` wraps the `dDlst_2DNumber_c` slot `0x80389130` (vanilla `0x800C8644`; `mPos[4]` at +0x14, `mDigitNum` at +0x41): a number whose ones digit is left of 320.5 moves by −S, and the high score's digits stay on their plate. `dDlst_2DMinigame_c` (`0x80389120`, the plate) stays unwrapped: it sits over the board, not at an edge. REL build 0x47408CD2.
- Item 6: done on branch `widescreen-2-5` (2026-11-09, BlobHash 0x4045BB2F, REL 0x47408CD2): `widescreenDefault` in `internal/gui/display.go` is `"16:9"`, so an unset setting starts in native widescreen at 16:9, after the cannon game (WS3-7), the stage 4 checks and the mode 0/1 regressions passed live (docs/TESTING.md). `TestBridgeSetWidescreen` expects 16:9 for the unset setting (it is the only test that changes with it). An explicit "off" stays off.

1. `WWWS_FLAGS_SUPPORTED` = `WWWS_F_2D | WWWS_F_HUD`; `ws_latch` sets `ui_shift`.
2. Walker: the shift kinds ALL_L, ALL_R, BY_CX, SCR_L, SCR_R and the L/R tag actions (3.3). They
   act only when `ui_shift` ≠ 0.
3. Rows: `main_parts1` (BY_CX + the 15 NONE tags), `main_parts2`, `main_parts3`, `time_swim_1`,
   `time_swim_2`, `place_name` (`pn` L), `auction1`, `auction2` (11 L tags), `main_parts_fileselect`
   ALL_R. Offline: add them to the generator's list and regenerate; a table test checks that
   `main_parts1` is BY_CX + EPOCH with the 15 NONE tags and that short tags are right-justified.
4. `ws_apply` S entries (s16, ×`ui_shift`): `{0x803E68E4, 35, −1}`, `{0x803E68F0, 590, +1}`,
   `{0x803E6B06, 0, −1}`; at 16:9 they give −75, 700, −110 (a test checks this).
5. REL (H-12): `_prolog` writes the vtable slots `0x80389100` (`dDlst_2DObject_c`, vanilla
   `0x800C946C`) and `0x80389110` (`dDlst_2DBattery_c`, vanilla `0x800C90F0`) to wrappers;
   `_epilog` restores them first thing. Each wrapper reads the latched `ui_shift` S; if 0 it just
   calls the vanilla draw. Otherwise: 2DObject `mPos.x` (+0x0C) += (x < 320.5 ? −S : +S); Battery
   `mPicturePos.x` (+0x28) and `mNumPos.x` (+0x30) += S; call the vanilla draw; restore the fields.
   The 2DObject slot also draws Sploosh Kaboom's three squid icons (`d_a_mgameboard.h:62`), so the
   x < 320.5 rule moves them too. Sploosh's numbers (slots `0x80389130` = `dDlst_2DNumber_c::draw`
   `0x800C8644`, `mPos[4]` at +0x14; `0x80389120` = `dDlst_2DMinigame_c::draw` `0x800C8944`,
   `mTitlePos` +0x10, `mScorePos` +0x20) get the same kind of wrapper only if a screenshot shows them
   at an edge.
6. Go: the setting's default becomes 16:9 for the R2 release.
7. **Live checks:** hearts, magic, compass and clock keep their vanilla left margin; buttons, keys
   and rupees their right margin; the boss bar stays centred and the enemy HP bar beside its enemy.
   Swim in the sea: the air meter sits at the right edge, not clipped, its shine on `wk00`. Minimap
   at the left edge, the wind arrow on it at sea, the free-camera icon at the right. Island arrival:
   the place name at the left. A minigame timer (boat race or Orca): the rupee icon at the left.
   Windfall auction: digits roll uncut, the icon and plates hug the top-left. Cannon game: bombs
   left, ships right, battery right. Sploosh Kaboom: squid icons and numbers where a screenshot says
   they belong. Leaving a minigame: the HUD is normal; the slots still point into the REL (the
   module stays linked across stage loads, `inject/src/mp/relload.c`) and pass through when S is
   0. `wwdev ops ws 0 16:9` mid-stage (a manual-toggle run, see the rig notes): nothing goes off screen; the HUD updates at the
   next stage load. File select: the button cluster at the right.

### Stage 4: menus and special screens (release R3)

**Built on branch `ws-45`** (PC, 2026-11-09; not run in Dolphin). Item 1 and, of item 2, the lens
flare and the moon's stars. T2 grew 0x1A0 for stages 4 and 5 together (`ws_frame_any` 0x158,
`ws_project_live` 0x4C); 0x878 bytes stay free. It differs from the items below in these ways:
- **The THP offsets were proven offline**, not live (`internal/inject/ws_thp_vanilla_test.go`,
  `TestWsTHPVanillaREL`, on the vanilla disc): `rels/d_a_movie_player.rel` is module 0x19F, its
  `.bss` (section 6) is 0xCB5C8 bytes and the only section that size; `daMP_ActivePlayer_Draw`
  (text 0x61DC) builds `&daMP_DrawPosX` from an ADDR16_HA/LO pair to `.bss`+0xCB59C and
  `&daMP_videoInfo` from one to `.bss`+0xCB580, then loads the polygon's w and h at +0 and +4;
  `daMP_ActivePlayer_Init` stores DrawPosX from that xSize off r30 = `.bss`+0x20+0xD0000. Both
  movies' THP headers say 640x480. Only `daMP_videoInfo` (the draw's polygon) is written, never
  `daMP_ActivePlayer.videoInfo` (`.bss`+0x6F0, the texture's size). The live read-back is
  docs/TESTING.md WS4-1.
- **`ws_frame_any`** (`src/ws/widescreen.c`, the WS module's `frame_any` slot) walks
  `__OSModuleList` (`0x800030C8`) every frame, at most 256 links, each checked inside MEM1 (48 MB, `MEM1_END` `0x83000000`); for id
  0x19F it takes the section-info entry `bssSection` (header +0x33, which OSLink fills) names and
  uses it only if its size is 0xCB5C8 and the whole `.bss` lies in MEM1 (`OSLink.c:250-256` sets
  `bssSection` and the entry's address at link time). It never caches the address (the module is unlinked after
  the movie). It writes only when xSize is 640 or its own last value (a static that starts at 640, at `0x8041024C`, outside `HashRanges`),
  ySize is 480 and DrawPosX is still (640 − xSize)/2, and never when xSize is already the target.
  The movie's first frame may be drawn full width before the step runs (accepted; WS4-2 notes it).
- **The lens flare and the moon's stars** share stage 5's `ws_project_live` (`src/core/hooks.c`):
  `mDoLib_project`, then, when `ws_live_factor()` ≠ 1, x′ = 320 + (x − 320)·F. All four sites
  measure an angle or distance about (320, 240) (`d_kankyo_rain.cpp:520-527`, `4725`, `4862-4864`,
  `d_kankyo.cpp:3623-3625`), so one function and one pivot serve them; the flare's 3D quad
  positions and the stars' vertices are untouched. Pinned by `TestWidescreenStage45HookSites`,
  `TestWidescreenStage45VanillaWords` and `TestWidescreenStage45Source`
  (`internal/inject/ws_stage45_test.go`).
- **Skipped from item 2:** the cinema trims (their values are "by screenshot", and the live
  camera's `mCamSetup` write needs a camera pointer and a field check nobody can make offline);
  the pause cloth (the stage-1 run, WS-4, saw it cover both edges at every rotation, so k(F) is not
  needed); the title lobby row spacing (Go, by screenshot). Each stays optional.

1. **THP movies** (T-9). First verify the module-415 (0x19F) `.bss` live: `peek 800030C8` gives
   the OS module list head; per module, `peek <m> 52`: id at +0, next at +4, the section table at
   +0x10, `bssSection` (u8) at +0x33; for id 0x19F, `peek <table + 8*bssSection> 8` gives the bss
   address (clear bit 0). (Or add `wwdev rel-modules` over `crash.ReadModules`.) After the title
   movie starts, `.bss`+0xCB580 reads 640. Then the step in the WS module's `frame_any` slot
   (`{ MOD_ID_WS, ws_init, ws_frame_any, 0, 0, 0 }`; the `frame` slot runs only with Link, past the
   boot gate, so it never sees the title or the ending), as T-9 describes. Check: the title movie is
   pillarboxed with black bands at the right proportions; the staff roll too if a finished save is
   at hand (or try `./wwdev.exe stage-hop ENDING 0 --no-check --yes`: stage `ENDING` opens the ending
   scene, `d_s_play.cpp:310`; not tried).
2. **Optional polish**, each one judged by a screenshot first, each with a pin test:
   - the lens-flare and moon-star sites `0x8008C6C0`, `0x800994AC` + `0x80099A6C`: a
     `ws_project_2d` variant using `ws_live_factor()` (they compare 3D EFB points, not 2D);
   - smaller cinema trims: the vista 52 and cinemascope 65 are copied by `dCamSetup_c`'s ctor
     (`0x800AFE14`/`0x800AFE1C`) into the live camera's `mCamSetup` (`dCamera_c`+0x60C, fields
     +0x64/+0x68); write those (the .sdata2 words reach only cameras built afterwards); values by
     screenshot;
   - the pause cloth: `ws_persp` at `bl` `0x801DFA04` plus a uniform cloth scale × k(F) (3D-11);
   - the title lobby row spacing (Go).
3. **Live checks:** the movies; an off-axis sun's flare streak points the right way; stars are
   hidden in a round area around the moon; the pause cloth covers at every rotation.

### Stage 5: picto box and telescope (release R3)

**Built on branch `ws-45`** (PC, 2026-11-09; not run in Dolphin):
- Item 1 is decided: **option A**, nothing beyond stage 2. Item 2 does not apply.
- Item 3: the telescope look-in (`bl` `0x8019744C` in `dKy_telescope_lookin_chk`) goes through
  `ws_project_live` (stage 4's as-built note). The radius is the caller's f1, so the projected
  point is moved about 320 by the live factor, which is exact for the round test about (320, 240);
  the radius is never touched.
- The scope-opening view rect (2D-15) is not built: under option A the viewfinder art is drawn
  uncompressed and already matches the EFB rects (2D-15's row).

1. **Ask the person** (a blocker on the card) whether option A, as stage 2 left it (the picto art
   stretched, photos right), is enough, or B or C (3.6), with stage-2 screenshots of the viewfinder
   and the album attached.
2. Only for B: the resample at `mCaptureProc`'s `bl encode_s3tc` (`0x8000AAE8`; it runs on the
   capture thread, 0x2000-byte stack, or inline) behind a T2 forwarder: stretch the centre 1/F of
   the RGB565, 4x4-tiled `mCaptureCaptureBuffer` (`mCaptureSizeWidth` × `Height`) to full width,
   then call `encode_s3tc`; drop stage 2's uncompressed draws for `wipe_01_01` and give it EDGE rows
   for `wp01 wp02 wp03 wp04`; keep `wipe_01_02`'s uncompressed draw (its rotated shutter blades
   can't take EDGE); widen `pictureDraw`'s ortho (`bl C_MTXOrtho` `0x80227BB8`, left/right args)
   about 320.5 by F, or the slots drift by (x − 320)(1 − 1/F); T-8's browse view as written there.
   Only for C: find the picto-aim predicate first (e.g. `dCamera_c::subjectCamera`'s picto branch)
   and write the vanilla knob only while it holds.
3. The telescope look-in radius (`0x8019744C`) through the `ws_live_factor()` variant of
   `ws_project_2d`. The scope-opening view rect (2D-15) only if visible.
4. **Live checks:** a photo of a round object (the Windfall lighthouse, a pot) looks round in the
   album; a quest photo passes; at night, with the moon about 110 vanilla units (≈165 px of a
   1280x720 16:9 window, measured on a screenshot) left of the scope centre, the telescope's moon
   line does not trigger.

## 9. Corrections to `widescreen-culling.md` (made in this PR)

- §2: `Widescreen.cpp:84-151` (overrides at `:55-80`); `ReadGFXAspect` does not resolve real `-C`
  overrides today (it matches `GFX.Settings.`, which Dolphin drops); fixed in stage 1.
- §4: the sun/firefly/lantern row: native mode fixes them, but `radiusActorInSight` stays 4:3 on
  purpose; the reticle row points at `ws_project_2d`; the name-tag row holds for mode 1 only.
- §6.1: the block is 0x18 bytes (section 5), WS_SET's c carries flags and the handler writes the
  knob, "21:9" is 7:3 (F 1.75) with 64:27 listed apart, and the usual soft reset keeps RAM and runs
  no init (only a reboot re-runs `ws_init`).
- §6.2: the CLI `widescreen` command refuses while a session holds the mailbox; the app's writer
  starts in the engine's Dolphin runner until the mod runtime has a Game (a deliberate exception);
  the app setting takes `16:9` / `W:H` / `off` and is parsed apart from the CLI's `ParseSetting`.
- §7, §8 step 4: `CanaryArenaHigh` starts at +0xA84 (size 0x27C).
- §8 steps 1 and 3: `WW_FEAT_WIDESCREEN` is 0x80 and `WW_FEATURES` 0xFF (0x40 is `WW_FEAT_OPS`).
- §8 step 7.5: `-C Graphics.Settings.*`, not `GFX.Settings.*`.
- §9: rewritten from this plan (the knob, the fade, the reticles, Dolphin at exactly 16:9, the
  Gecko line and caves, lock-on framing).

## 10. Open questions and live checks owed

**For the person:**
- The picto box option (3.6), asked at stage 5 with pictures; A is recommended and needs nothing
  more.
- Pause dims widened (this plan) or removed (the Gecko): widened unless they say otherwise.
- Default Off in R1, 16:9 from R2 (section 7): the card asked for 16:9 by default; this plan only
  waits until the HUD is right.

**Owed live (each is in its stage's checks):** the `ship_race2.blo` result screen (H-19); the THP
`.bss` offsets read back live (proven offline from the disc REL, stage 4 as built); a Stretch mode's
own check (the VI aspect is measured, 1.2524..1.2543 in WS-11); whether `back_aori.bti` (`operate_wind_mask`'s `BACK`) and `back_auction_00.bti` (the
full-screen `BACK` panes of `auction1/2`, siblings of `ROOT`; stage 3's SCR_L moves `auction1`'s
by −S with the screen) need GEOM.

**Accepted cosmetic:** the title's 2D after a late WS_SET; the file-select sky before stage 2;
notes sliding in through a band; the title ship visible in the bands; text about 25% softer
sideways (2D drawn into fewer EFB pixels); the sun's falloff and Aryll's telescope check stretched
by F; photos viewed in a different mode than they were taken in; a Dolphin aspect changed mid-session.

**Settled live (PC rig, 2026-11-09):** a single knob store holds under the JIT (WS-1); the pause
cloth covers at every rotation (WS-4); WS_SET lands about 7 s before the title stage from a cold
app launch, so the title's 2D is compressed (WS2-11); the save iris and the telescope bars leave
no gap through their tweens (WS2-2, WS2-7); Sploosh Kaboom's shot count needed the number wrapper
(WS3-8, built).

**Stage 2/3 checks not run on the PC rig (2026-11-09),** each needing an event or a minigame the
start save doesn't reach by a hop: the Wind's Requiem selector, a cutscene's own iris wipe, a
Bokoblin's HP bar at an edge, an NPC talked to at the far left (the place name was checked by
poking its request, WS3-4), a minigame timer, the Windfall auction, the
cannon game, the compass, the clock and a boss bar, the file select's lobby line, and the Play
page's 2D-pending line.

---

[← Enemy rows: next](enemy-rows-next.md) · [Docs home](../README.md) · [Widescreen culling →](widescreen-culling.md)
