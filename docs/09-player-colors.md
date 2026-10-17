# Player tunic colors (v1.0 / v1.1)

**Status:** built, 2026-10-16, on branch `feat/player-tunic-colors`, rebased onto `origin/main` a301890 (F18 heap fix #64, hat fix #66, session layer #65). The Go side and the C side are both in, and the blob is regenerated. None of it has run on Dolphin yet. §7 is the owner's live check, and §7.0 says how to force the hero's clothes for it.

**Request (Stephen):** "each player in the session should be assigned a different color and the green Link clothes should have their color swapped out to the player's assigned color."

**Conventions**
- `tww/` means the zeldaret/tww decomp at commit 4d78c994. References use `file:line`.
- `multiplayer.c` means `inject/src/multiplayer.c` on this branch. Line numbers shift, so search for the named function.
- All addresses are absolute GZLE01 (NTSC-U) addresses.
- **(inferred)** marks a claim read from code but never observed on a live Dolphin.

---

## 1. Summary

**Recommendation: recolor the texture, not the material.** Link's green is painted into a texture, `linktexS3TC`. For each player, Go builds a recolored copy of that texture. A small C bracket repoints the texture header at the right copy while each Link is drawn: local Link #1 and each mini-Link slot. This is approach (c), and it uses the same mechanism as approach (b).

- **Where the green comes from.** Link's tunic green is not a material or TEV color. It is painted into `linktexS3TC`, a CMPR (S3TC) texture in `cl.bdl`. The game's own casual-clothes swap replaces that texture's `ResTIMG` header (§2).
- **Why one bracket can give each Link its own color.** Every Link on screen (Link #1 and the mini-Links) shares one `J3DModelData`, and so one texture table. Each Link's per-frame differed display list is baked when that Link is entered. At that moment `loadTexNo` reads `(u8*)resTIMG + resTIMG->imageOffset` (§3). Swapping the header's image pointer around one Link's entry therefore recolors only that Link. Face sync already ships this exact bracket for the eye texNos, and it is visually proven (§3).
- **How the recolor is done.** Because the texture is CMPR, a recolor only moves the two RGB565 endpoint colors of each green 4x4 block. There is no decode or re-encode. Go does it at join time (`wwapi/gfx/texture`, was `internal/tunic`; implemented here).
- **Who picks the colors.** Each player picks their own, from an 8-color palette (green, red, blue, purple, then orange, cyan, pink, black) or any `#RRGGBB`, with `--color`, `WW_COLOR` or the app's lobby. Players may wear the same color; a player who picks none wears classic green. The server records each player *name*'s color and sends it to everyone. The colors travel in a backward-compatible trailer on `MsgPlayerList`, so there is **no protocol version bump** (§5.1). This part is implemented and tested here.
- **What must be checked live first.** One Go-only check runs before any blob change: the `tunic-probe` and `tunic-recolor` diagnostics (implemented here). They prove the texture facts and the recolor quality on a live Dolphin (§7.1).
- **The C part (built).** It is `tunic_swap` / `tunic_restore` plus a one-time allocation, measured at **0x120 bytes** of T2 with devkitPPC 16.2 (§6). The casual shirt for remotes (§5.6) added 0x20, so T2 now ends at 0x80412E28, **0xD8 bytes** below the mailbox. The blob is regenerated; it still needs one Shift+F1 recapture and a user re-patch.
- **Owner decisions (2026-10-16).**
  - Green stays in the palette: the vanilla tunic is a valid color, and it is what a player with no preference wears.
  - Colors will later be chosen in a Wails desktop lobby, so selection stays in Go packages the app can call: `mods/multiplayer/playercolor` (palette, `Parse`), `network.Client.SetColorPreference`, and `session.Player.Color` for display.
  - **Shared colors are fine (Stephen, 2026-10-16):** "I don't care that people are the same color. Remove that code too if it auto assigns a new color." Nothing warns about, refuses or changes a color another player wears. This replaces the earlier rule that moved a clashing preference to the first clear palette color.

## 2. How Link's tunic is colored (Q1)

**The green is in the texture.**
- `daPy_lk_c::playerInit` (0x80124C98) walks `mpCLModelData->getTexture()` for the name `"linktexS3TC"` (`tww/src/d/actor/d_a_player_main.cpp:12319-12357`). cl.bdl's TEX1 section has two headers by that name. The first has alpha enabled and is skipped. The second "is the one that is actually used" (`:12331-12336`).
- The game keeps a pointer to the used header (`mpCurrLinktex`, daPy_lk_c+0x338) and a copy of the hero's-clothes header (`mOtherLinktex`, +0x33C; `tww/include/d/actor/d_a_player_main.h:1996-1997`). It then overwrites the used header with the casual texture `LINK_BTI_LINKTEXBCI4` through `J3DTexture::setResTIMG` (`:12343`).
- `setResTIMG` copies the 0x20-byte header and rebases `imageOffset` and `paletteOffset` to the new data (`tww/include/JSystem/J3DGraphBase/J3DTexture.h:47-52`).
- Hero's clothes swap the headers back (`:12352-12355`). So do the clothes-change demo (`setDemoData`, `:10329-10339`) and teardown (`playerDelete`, `:11770-11772`).
- **So "change the clothes color" is already a texture-header swap in vanilla code.** That is the mechanism this design reuses.
- The casual-clothes flag is `mNoResetFlg1 & daPyFlg1_CASUAL_CLOTHES` (daPy_py_c+0x2A0, bit 0x8; `tww/include/d/actor/d_a_player.h:202,486`). It is set when event bit 0x2A80 is clear or the clear count is nonzero (`:12345-12347`).
- The name says S3TC, and a CMPR (format 0x0E) header is what the recolor needs. **(inferred: `tunic-probe` prints the format.)**

**The materials only light it.**
- The project's material survey found the tunic set to be materials 0, 16 and 19: upper body, sleeves and tunic bottom, legs and boots (`docs/06-history.md:577-583`). Material 18 is the hat. So the tunic materials also draw the pants and boots, and a per-material tint would recolor those too.
- Each frame, `daPy_lk_c::draw` calls `g_env_light.setLightTevColorType(mpCLModel, &tevStr)` (`d_a_player_main.cpp:1744`). That writes the area lighting into **every** material's TEV color 0 and konst color 0 (`tww/src/d/d_kankyo.cpp:1764-1830,1856-1872`). Writing C0 or K0 yourself is overwritten by the next frame's lighting.
- The toon TEV stages multiply the texture by these lighting colors **(inferred: the BDL's TEV stage table was not decoded)**. A multiplicative tint cannot turn a green texel red, because the texel has almost no red to scale.

**Material colors are baked; TEV registers and texNos are re-emitted per instance.**
- Link's models use the differed flag 0x37221222 (`d_a_player_main.cpp:11902`). The instance-level diff runs `J3DMaterial::diff` (`tww/src/JSystem/J3DGraphBase/J3DMaterial.cpp:244-258`).
- `J3DTevBlock::diff` (`J3DMatBlock.cpp:1111-1123`) emits these into the per-instance differed DL:
  - texNos, if `(flag>>16)&0xF` is nonzero (here 2);
  - TEV stages, if `(flag>>20)&0xF` is nonzero (here 2);
  - TEV color and konst registers, if `flag & 0x01000000` (set here).
- Material color is emitted only if `flag & 0x1` (`J3DColorBlockLightOn::diff`, `J3DMatBlock.cpp:455-460`). That bit is **clear** in 0x37221222. So `matColor` lives only in the shared DL built once by `makeDisplayList` (`J3DMaterial.cpp:189-210`).
- This is why the old `tint-material` diagnostic, which writes matColor, showed nothing (the comment on its since-removed `runTintCycle` said: "unlike matColor which J3D bakes into a cached display list").

**Can it be recolored at runtime from RAM?**
- **The texture can.** The header array is `J3DModelData+0x70` → `J3DTexture{u16 mNum @+0; ResTIMG* mpRes @+4}` (`J3DMaterialAttach.h:61`, `J3DTexture.h:35-36`).
- **Names.** `J3DModelData+0x74` is `mTextureName`, a `JUTNameTab*`. That is a C++ object, not the name resource:
  - `+0x00` vtable, `+0x04 const ResNTAB* mNameTable`, `+0x08 mpStrData`, `+0x0C u16 mNameNum` (`tww/include/JSystem/JUtility/JUTNameTab.h:20-37`).
  - The `ResNTAB` it points at is `u16 count, u16 pad, {u16 keyCode, u16 offset}[count]`. Name *i* is at `ResNTAB + offset` (`ResNTAB::getName`, `JUTNameTab.h:13-15`: `mEntries + offs - 4`, with `mEntries` at +4).
  - The first `tunic-probe` read the object as if it were the `ResNTAB`, so on the live run it printed garbage for all 41 of Link's textures. `readNameTab` now follows `+0x04` and checks the count at `+0x0C` (`TestReadNameTab_DerefsJUTNameTab`).
- A recolored image placed anywhere in MEM1, and pointed at by a header's `imageOffset` (+0x1C), is what `loadTexNo` loads.
- Writing the image bytes in place also works in principle (the `tunic-recolor` diagnostic does it). Dolphin re-decodes when the texture's hash changes **(inferred; checked in §7.1)**.
- **Material colors cannot**, per instance: see §4.

## 3. How the remote Link is drawn, and why a per-instance swap works (Q2)

**Mini-Link shares Link #1's model data.**
- `multiplayer.c:534-537` resolves `dRes_getRes_byIdx("Link", LINK_BDL_CL)`, which is Link #1's loaded `J3DModelData`.
- It then creates `MAX_REMOTE_LINKS` (2) `J3DModel`s on it with `mDoExt_J3DModel__create(mini_link_data, 0, 0x37221222)` (`:595-643`).
- Materials, TEV blocks and the texture table are therefore all shared. The same comment block notes that `mDoExt_J3DModel__create` forces the shared material-DL flag for this model type (`:606-612`).

**The submission path.**
- `daPy_draw_hook` (hooked at 0x80108210) first calls Link's real draw, `daPy_lk_c::draw` at 0x80107308 (`multiplayer.c:1487-1490`).
- Then, per active slot, it runs the following sequence (`:2001-2033`):
  1. `face_emit_swap_for_slot`
  2. `run_eye_fix`
  3. `mDoExt_skip_recipe_joints`, which is `mDoExt_modelEntryDL`
  4. `face_emit_restore`
- `mDoExt_modelEntryDL` (0x8000F974) calls `mDoExt_modelDiff` for this model type (`tww/src/m_Do/m_Do_ext.cpp:563-577`). That is `calcMaterial(); diff(); entry();` (`:509-513`).
- `J3DModel::diff` calls `material->diff(mDiffFlag)` on every material with that instance's matpacket current (`J3DModel.cpp:432-437`).
- That reaches `loadTexNo` (0x802EC388). It reads `j3dSys.getTexture()->getResTIMG(texNo)` and writes `TX_SETIMAGE` with `(u8*)resTIMG + resTIMG->imageOffset` into the instance's DL **at that moment** (`tww/src/JSystem/J3DGraphBase/J3DTevs.cpp:138-152`).
- Link #1's own diff runs the same way inside his draw (`d_a_player_main.cpp:1883-1887`).

**Why a bracket gives each Link its own texture.** Swap the used `linktexS3TC` header's `imageOffset` before a Link's diff and restore it after. That Link's DL then points at its own texture, and the others keep theirs. This is the same channel, timing and save/swap/restore pattern as face sync:
- Face sync swaps the shared TEV block's texNo, which feeds the same `loadTexNo` call, around the same window (`multiplayer.c:865-946`).
- It is visually proven to change only the mini-Link: "the bracket carries face_state values to the rendered pixel … Local Link in the same window kept blinking normally" (`docs/06-history.md:1327-1336`).
- Narrower brackets around `J3DMatPacket::draw` did **not** work (`docs/06-history.md:1228-1235`), which is why the bracket must wrap the whole entry.

**Cross-process race rule.** Go must never read the *used* header while C may be inside a bracket. Face sync hit exactly this race (`docs/06-history.md:1338-1362`). Go reads the **spare** (first) `linktexS3TC` header, which nothing swaps, or a value C publishes.

## 4. Approaches compared (Q5)

| | (a) Go writes material colors | (b) C sets TEV colors around each draw | (c) recolored texture, C bracket (**recommended**) |
|---|---|---|---|
| Per-instance? | No. matColor is baked in the shared DL (flag bit 0x1 clear). TEV C0/K0 are rewritten from `tevStr` each frame. C1-C2/K1-K3 are shared by every instance and re-baked into each instance's DL at its diff, so a Go write hits all Links on that Dolphin and races the frame. | Yes. The diff flag has 0x01000000 (diffTevReg), so a swap of C1/K1-K3 around an entry bakes per instance (same bracket as (c)). | Yes, proven channel (§3). |
| Color quality | Multiplicative on green: muddy or dark, never a clean red or blue. | Same multiplicative limit. It also tints pants, boots and sleeves, because materials 0, 16 and 19 contain them (`docs/06-history.md:579`). It also depends on an undecoded TEV stage table. | Only green texels move. Hue, saturation and value are mapped so shading survives. Skin, white pants, brown boots and the belt are untouched (hue mask). |
| JIT / DL caching | Defeated by the shared-DL bake. | Fine. | Fine: a different image address is a different Dolphin texture-cache entry. Contents are written once per color change. |
| C size | 0 | ~0x150 (inferred) | ~0x1F0-0x210 (inferred, §6) |
| Memory | 0 | 0 | One CMPR image per drawn Link (≤3) in GameHeap. Size is measured by `tunic-probe` (32 KiB each if 256x256, inferred). |

**Verdict:**
- (a) cannot meet "each player a different color" on one screen. It can only ever tint every Link on a Dolphin the same.
- (b) works mechanically but looks wrong: tinted pants and boots, no real reds or blues.
- (c) gives clean colors and is the lowest-risk path to a correct result, because its one risky element, the bracket, is the already-shipped face-sync bracket applied to a sibling field.
- A Go-only, in-place texture rewrite is kept as the **feasibility diagnostic** (§7.1), not the product. It recolors Link #1 and every mini-Link on that Dolphin alike. It also needs an original-bytes backup, and it depends on Dolphin's texture hash noticing the change.

## 5. Design

### 5.1 Color assignment and protocol (Q3) — implemented on this branch

**Palette** (`mods/multiplayer/playercolor`):
- The palette, in display order, is green (vanilla, no recolor), red, blue, purple, orange, cyan, pink, black. The first four are the Four Swords colors.
- A test checks that every pair is at least 120 apart (redmean RGB, `Distance`), so the choices are easy to tell apart. Players may still pick the same one.
- `Parse` accepts a palette name or `#RRGGBB`.

**Assignment** (`network/server.go`, `pickColorLocked`), under `s.mu` at join:
0. (App PR 4, docs/10-lobby.md §4.) A connection whose name is a lobby member's gets the member's color, which the member chose.
1. A connection whose name matches a live player gets that player's color. Each human runs two connections, broadcast-pose and puppet-sync, under one name (see `dedupePlayersByName` in `main.go`).
2. Otherwise, the hello's preference, whoever else wears it.
3. Otherwise, the color this name had earlier in the server's life. A reconnect keeps its color.
4. Otherwise, classic green.

No rule looks at the other players' colors: two players may wear the same one (owner decision, 2026-10-16).

The name memory is capped at 256 entries.

**Wire format.** Neither change alters the protocol version, because protocol-2 peers ignore both.
- *Hello:* a player with no mods hash and a preference sends the role form `[version:2][role=0][caps=0]` plus `[1][r][g][b]` (`HelloPlayerMessage`, `ParseHelloColor`). A pre-color server parses it as a plain player hello, because `ParseHelloRole` ignores bytes after caps (`handshake.go`). A player with neither sends the 2-byte hello. Since D5 every CLI and app build has a mods hash, so its player hello is always the role form with caps `CapModsHash`, the colour block (`[0][0][0][0]` when it has no preference) and the 32-byte hash (`HelloPlayerModsMessage`; docs/api/02-mods.md, "`mods_hash`"). The forms without the hash are what older clients send.
- *Player list:* after the entries, `[0xC0][count][id r g b]*`. It is sent only when every entry has a color, with IDs in entry order, and it is accepted only in exactly that form. Old clients already ignore bytes after the last entry (`ParsePlayerList` doc comment). The existing `FuzzParsePlayerList` invariant (re-encoding a parse is a prefix of the input) still holds; fuzzed 15 s.

**Client side:**
- `RemotePlayer.Color`/`HasColor`. `TunicColor()` (a `playercolor.ForID` fallback) still exists but the render and the TUI no longer use it: each human has two connections and so two IDs, so a by-ID guess would not match between clients. Without a server color, nothing is recolored and the TUI shows no swatch.
- `Client.SetColorPreference` and `Client.MyColor()`.

**Options:**
- `--color <name|#RRGGBB>` anywhere before a bare `--`, or the `WW_COLOR` env. `--color` wins.
- Both are stripped from `os.Args` before the subcommand switch (`playercolor_flag.go`). Every player client is created through `newPlayerClient`.
- `mp-local` gives both of its players the same preference, and both wear it (shared colors are allowed, §1 owner decisions). Without `WW_COLOR` both wear green, so a two-color mp-local check needs a per-player `MP_LOCAL_COLOR_A/B`, which is not built yet.

**TUI:** the status panel's "In game" row shows `■ name (color)` in the player's color (`renderStatus`). The color reaches the TUI as `session.Player.Color` (§5.4). Showing the local player's own color is left to the lobby work.

### 5.2 Recolor (`wwapi/gfx/texture`, was `internal/tunic`) — implemented on this branch

**CMPR layout.** CMPR is 8x8 tiles of four DXT1 blocks: 8 bytes each, holding two big-endian RGB565 endpoints and sixteen 2-bit indices. The decoder picks 4-color mode when `c0 > c1`, else 3 colors plus transparent.

**Recolor(data, target, mask, ref)** maps each endpoint:
- **Hue:** `target.h + (h − ref.h)`.
- **Saturation and value:** `s·target.s/ref.s` and `v·target.v/ref.v`.
- **Blend:** by a hue/saturation/value **mask weight**, with feathered edges so blocks on the tunic/skin border don't step.
- **Mode preservation:**
  - A 4-color block whose endpoints swap order is reordered, with indices XOR 0x55.
  - A 4-color block whose endpoints become equal gets all indices set to 0. Otherwise index 3 would turn transparent.
  - A 3-color block that would flip order is reordered with indices 0↔1.
- **Tests:** every pixel keeps its opacity and its color grouping for every palette target, plus white and black.

**Calibrate** measures the tunic's own green from the texture: the median hue, saturation and value of the endpoints inside the default window. The palette's "green" then maps exactly to the vanilla shade, and no hand-tuned reference color is needed. The default window is hue 105 ± 30° (feather 15°), saturation ≥ 0.30, value ≥ 0.10 **(inferred: tune from the §7.1 screenshots if eye or other green details move)**.

**Other helpers:** `ParseResTIMG`, `ResTIMG.ImageAddr` (wrapping u32 offset, as `loadTexNo`) and `ImageSize`, and `FindLinktex` (spare = first, used = second).

### 5.3 C bracket (built)

**Mailbox block** (`inject/include/mailbox.h`, "TUNIC COLORS").
- +0x180..+0x187 stay reserved for the docs/08 §4.5 option A header.
- The mailbox grows from 0x180 to 0x1A0 bytes. 0x80413080..0x80414000 was unused.
- The Go offsets are generated from `mailbox.h` (`tools/gen_mailbox` → `wwapi/mailbox/layout_gen.go`, since Phase C step C9). `TestMailboxLayout` (wwapi/mailbox) fails when that file is stale, and the generator refuses a header whose own `+0xNN` comments disagree with the struct.

| Off | Field | Writer |
|---|---|---|
| +0x188 | `u32 tunic_bufs[3]`: the image of slot 0, slot 1 (the mini-Link slots) and slot 2 (`TUNIC_SLOT_LOCAL`, Link #1); 32-byte aligned, in GameHeap | C |
| +0x194 | `u32 tunic_buf_size`: bytes per image; nonzero asks C to allocate | Go |
| +0x198 | `u8 tunic_enable[3]`: 1 = the image holds this slot's texture, swap on | Go |
| +0x19B | `u8 tunic_state`: 0 not allocated, 1 all allocated, 0xFD an allocation failed | C |
| +0x19C | `u32 tunic_swaps`: header swaps done (diagnostic) | C |

The design's `tunic_used_idx` / `tunic_spare_idx` fields were dropped. C finds the worn header through Link #1's own `mpCurrLinktex`, so it needs no texture indices and no name table.

**Allocation** (`multiplayer_update`, after the 300-frame gate).
- When `tunic_state == 0` and `tunic_buf_size != 0`, C allocates each null slot with `JKRHeap_alloc(size, 0x20, mDoExt_getGameHeap())`. This is the allocator and alignment `pose_bufs` uses.
- It then sets the state to 1, or to 0xFD if any allocation failed. The ones that worked stay published.
- Nothing sets the state back to 0, and `mini_link_reset_state` does not touch the block. The buffers are allocated **once per boot** and never dropped.
- That is the F18 lifetime rule in `multiplayer.c`'s "Lifetimes" comment: GameHeap blocks stay valid for the session, and a dropped pointer is a leak.

**Swap.** `tunic_swap(link, slot)` and `tunic_restore()` are marked `noinline, optimize("Os")` to stay small:

```c
u32 buf = mailbox->tunic_bufs[slot];
u32* hdr = *(u32**)(link + 0x338);                 // mpCurrLinktex: the worn header
if (tunic_hdr || !buf || !hdr || !mailbox->tunic_enable[slot]) return;
u32* hero = hdr;
if (*(u32*)(link + 0x2A0) & 8) {                   // Link #1 in the casual clothes
    if (slot == TUNIC_SLOT_LOCAL) return;          //   his own draw stays casual
    hero = (u32*)(link + 0x33C);                   //   mOtherLinktex = the hero header
}
if ((hero[0] >> 24) != 0x0E) return;               // CMPR only
for (k = 0; k < 8; k++) { tunic_saved[k] = hdr[k]; hdr[k] = hero[k]; }
hdr[7] = buf - (u32)hdr;                           // imageOffset; wraps like loadTexNo
tunic_hdr = hdr; mailbox->tunic_swaps++;
```

(This is the colors-only version. §5.6 adds enable value 2, the casual shirt, which copies `mOtherLinktex` in unchanged.)

`tunic_restore` writes the 8 saved words back. It differs from the design sketch in two ways:
- **The hero header comes from Link #1, not from the spare `linktexS3TC` entry.**
  - In the hero's clothes it is the worn header itself.
  - In the casual clothes it is `mOtherLinktex` (daPy_lk_c+0x33C). `playerInit` copies it raw from the worn header, so its offsets are relative to the worn header (`d_a_player_main.cpp:12338-12355`).
  - This avoids copying the spare entry's `alphaEnabled`, which is where the two entries differ (`:12331-12334`), and needs no index lookup.
- **Link #1 in the casual clothes is never swapped.** C checks this every frame, so the swap never lags the clothes-change demo.

**Call sites** (`daPy_draw_hook`):
- **Local:** `tunic_swap(this_, TUNIC_SLOT_LOCAL); result = daPy_lk_c_draw(this_); tunic_restore();`.
  - Link #1's diff runs inside his draw (`mDoExt_modelEntryDL(mpCLModel)`, `d_a_player_main.cpp:1886`), so his differed DL bakes the swapped image.
  - The eye-fix mode 1/2 experiment draws are not wrapped.
- **Remote:** inside each slot's face bracket, around `run_eye_fix` and `mDoExt_skip_recipe_joints` / `mini_link_entry_dl`, in the order `face_emit_swap_for_slot; tunic_swap(this_, slot); … ; tunic_restore(); face_emit_restore();`.

**Why the texture in RAM stays pristine.** Every swap is restored inside the same draw hook call, and C never writes the image. Go reads the original image outside the bracket (§5.4), so no backup file is needed.

### 5.4 Go wiring (built: `wwapi/avatar/tunic.go`, was `tunic_sync.go`; the session side is `mods/multiplayer/tunicsync`)

**Reading the hero image** (`readHeroTexture`). Go reads Link #1, then `mpCurrLinktex` (the worn header). The hero header is that worn header in the hero's clothes, and `mOtherLinktex` in the casual clothes. Either way the image is at `worn + imageOffset`. Race rules:
- C rewrites the worn header only inside a bracket. A header whose image lies inside one of the three buffers was read mid-swap, so it is refused (`errTunicBusy`) and retried on the next tick.
  - `Tick` reads the mailbox tunic block *before* the hero read and checks against the live `tunic_bufs`, over the larger of `tunic_buf_size` and the header's own image size. This matters because enables live in RAM and outlive a Go process: a fresh `tunic-slot` run, a puppet-sync restart or a recalibration can make its first read while a slot is already swapping. A swapped header points exactly at a buffer (`imageOffset = buf - hdr`), so it is always refused (`TestTunicSync_StaleSwapFirstTick`, `TestTunicSync_RecalibrateRefusesSwapped`).
- The header is read twice and both reads must match, which rejects a torn read.
- C never writes `mOtherLinktex` or the image.
- Only CMPR is accepted, at most 64 KiB per image (three are allocated).

**`avatar.Tunics`** (was `tunicSync`). There is one per puppet-sync, so one per Dolphin, and it survives reconnects. It works in these steps:
1. Read and `Calibrate` the hero image once.
2. Write `tunic_buf_size` while `tunic_state` is 0. C then allocates.
3. Run a small state machine per managed slot, timed by `spawn_trigger` (mailbox+0x00, +1 per game frame):
   - **Off:** write `enable = 0`.
   - **Color change:** write `enable = 0` and wait `tunicDrainFrames` (2) game frames. Then write `Recolor(orig, color)` (the original bytes for green) and set `enable = 1`. Recolors are cached per color.
4. Once a second, check two things. Either mismatch starts the slot over.
   - The whole image in each enabled buffer (at most 64 KiB per slot per second): a save-state load can leave another color's image behind an enable of 1, and a prefix can be identical for every color, so only a full compare catches it (`TestTunicSync_RewritesOtherColor`).
   - The worn-header address: a new model would need a new calibration.
5. A buffer pointer that changes (a save state from another boot) resets that slot.

**What each slot shows** (`tunicsync.SyncTunics`, called every puppet-sync tick):
- **Only server-assigned colors are drawn.** Until `client.MyColor()` is known (a server from before colors, or the moment before the first player list) no slot gets a color, so there is no by-ID color and no color flash at join (`TestSyncTunics_NoServerColorAllOff`). A casual remote still gets the casual shirt, which is not a color (§5.6).
- **Mini-Link slot *s*:** the remote that `remoteToLinkSlot` maps to that slot, in `rp.Color`. A remote without a server color is off.
  - Green remotes are swapped too, to the original image, so they show hero green even while the local player wears the casual clothes.
  - A remote whose appearance byte says casual gets `enable = 2` (`tunicEnableCasual`, §5.6): C draws it in the casual shirt, whatever its color, whether or not it or this client has a server color, and whether or not its slot has a buffer. Colors apply to the hero's clothes only. Switching to casual needs no image, so it is written at once; leaving it for a color goes through off and the drain like a color change.
  - A free slot is off.
- **Local slot:** `client.MyColor()`. Green needs no swap, so it is off.
- **Teardown.** When puppet-sync ends (host, join, mp-local, puppet-sync), `DisableAll` writes 0 to all three enables, so the C side stops recoloring after the session. The GameHeap buffers stay allocated (F18) and are reused by the next session. `WW_TUNIC=off` sessions do not touch the enables (they belong to `tunic-slot` then).

**Appearance byte.** broadcast-pose appends a 17th byte to the 16-byte face trailer: bit 0 = Link #1 wears the casual clothes (daPy_lk_c+0x2A0 bit 0x8). This is not a protocol change (`TestPoseAppearanceByteRoundTrips`):
- receivers before it copy only the first 16 bytes (`rp.FaceState[:faceStateWireBytes]`);
- `MaxFaceBytes` is 64.

**Dev and diagnostics:**
- `WW_TUNIC=off` turns the wiring off in puppet-sync, mp-local, host and join.
- `tunic-slot <0|1|local> <color|#RRGGBB|off|casual>` sets one slot (`casual`: slots 0 and 1 only) on the selected Dolphin and waits until it is in RAM. It touches no other slot. Run the pipeline with `WW_TUNIC=off` while using it, or puppet-sync puts the session colors back.
- `dump` prints the tunic block: state, size, swaps, and each slot's buffer and enable.

**TUI and app.**
- The color travels as `session.Player.Color` / `HasColor` in internal/session, the controller/snapshot API that the TUI and the future Wails app share.
- The status panel shows `■ name (color)`.
- Showing the local player's own color, and picking a color before connecting, are left to the lobby work.

**Help text.** `--color` is still not in `printHelp`. Add it when colors are confirmed live (§7.3).

### 5.5 Known limitations (by design, v1)
- **A casual remote is never recolored.** It is drawn in the casual shirt (§5.6), which has no color. Its hair is the hero's-clothes head with the hat-cap from #66, drawn in the casual texture; the casual look's own hair model (katsura) is not drawn.
- **The hero's second set of clothes.** On a second quest (clear count ≠ 0) the game forces the casual clothes (`:12345`), so there is no green to recolor.
- **Hands are not recolored.** `hands.bdl` is a separate model (`d_a_player_main.cpp:11903`). It holds skin only (inferred).

### 5.6 Casual shirt for remote players (built, 2026-10-16)

**Owner request (2026-10-16):** a player who hasn't got the Hero's Clothes from Grandma yet must not appear in a uniform in other players' games. Before this, a casual remote's slot was off, so it drew with the viewer's worn body texture: hero green (or the viewer's own state) on a uniformed viewer.

**Live probe** (`tunic-probe` and `peek`, both Dolphins in the casual clothes on Outset, 2026-10-16):
- The worn header (`mpCurrLinktex`, texture 34 `linktexS3TC`, 0x80CC70E0) holds `linktexbci4`: format **0x08 (C4, 4-bit indexed)**, 160x96, one mip, `indexTexture` 1, TLUT format 1 (RGB565), 16 colors. Image 7680 bytes at worn + 0x0010F6C0; palette at worn + 0x001114C0. Raw words: `080000A0 00600000 01010010 001114C0 00000000 01010000 01000000 0010F6C0`.
- `mOtherLinktex` (daPy_lk_c+0x33C) then holds the hero header: CMPR, 160x96, imageOffset 0x0001FA00 relative to the worn header, so its image is the spare `linktexS3TC` image (0x80CE6AE0).
- In the hero's clothes the two swap (`d_a_player_main.cpp:12350-12355`): the worn header is the CMPR one and `mOtherLinktex` holds the casual header above, word for word, offsets still relative to the worn header (read live on a forced-hero Dolphin).
- So a remote's casual header is always reachable from the viewer's own Link: the worn header while the viewer is casual, `mOtherLinktex` while the viewer wears the hero's clothes. Both offsets in it are relative to the worn header, so copying it into the worn header unchanged makes both resolve.
- `loadTexNo` loads the TLUT from the header too when `indexTexture` is set (`J3DTevs.cpp:138-152`), and the differed buffer is sized for it (0x37 bytes per texNo, `J3DPacket.h:43-45`), so the per-instance bracket carries the format, image and palette.

**C** (`tunic_swap`): enable byte value 2 (`TUNIC_ENABLE_CASUAL`, mailbox.h) on a remote slot means "this remote wears the casual clothes":
- viewer casual: nothing to swap (the worn header already is the casual one);
- viewer in hero clothes: copy the 8 words of `mOtherLinktex` into the worn header unchanged (no image buffer, no CMPR check) and restore after the slot's entry, exactly like the hero swap.
- Enable 1 behaves as before, so a hero remote seen by a casual viewer still gets the hero header from `mOtherLinktex` with its colored image. The local slot never takes 2.
- No mailbox field was added; T2 grew by 0x20 bytes (§6).

**Go:** `tunicsync.SyncTunics` calls `SetCasual(slot)` for a remote whose pose-trailer appearance byte has bit 0 set (`RemoteCasual`), so the protocol is unchanged. `step` writes 2 directly; a later color goes through off and the drain (`TestTunicSync_CasualSlot`, `TestSyncTunics_SlotsFollowRemotes`).

The casual shirt does not depend on the color pipeline, since C reads no image buffer for it. `Tick` writes a casual slot's 2 before every color gate: no hero texture read yet, nothing allocated (state 0), a buffer size for another texture, and a slot whose GameHeap allocation failed (state 0xFD, buf 0). `SyncTunics` also sets casual remotes when this client has no server color yet (a server from before colors), while every color stays off. Both are covered by `TestSyncTunics_CasualSkipsColorGates`. `tunic-slot <0|1> casual` therefore settles on a slot without a buffer too.

**Live check** (two Dolphins, host red / join blue on 127.0.0.1, Outset pier; hero clothes forced on one by event bit 0x2A80; screenshots in the PR's `casual-screenshots`):

| Case | Viewer | Remote | Seen |
|---|---|---|---|
| A | hero (red) | casual | remote in the casual shirt (blue lobster shirt, orange trousers). Before the fix the same remote was drawn in hero green. |
| B | casual | hero (red) | remote in the red hero tunic |
| C | hero (red) / hero (blue) | hero | each sees the other in their color, as before |
| D | casual | casual | both in the casual shirt, as before |

`gameHeapErrors` (0x803F68F8) stayed 0 on both. A first build copied the worn header onto itself in case A (the source pointer was only switched for a casual viewer); the live check caught it.

## 6. Code space, memory, save-state coupling (Q4)

**Code space** (measured, devkitPPC 16.2, `-O2`):

| Build | T2 size | T2 end | Free below the mailbox (0x80412F00) |
|---|---|---|---|
| `origin/main` a301890 | 0x2CE8 | 0x80412CE8 | 0x218 |
| first build of this C at `-O2` | 0x2F88 | 0x80412F88 | **−0x88 (overlaps the mailbox)** |
| as built (`noinline, optimize("Os")` on the two helpers) | 0x2E08 | 0x80412E08 | 0xF8 |
| plus the casual shirt (§5.6) | **0x2E28** | **0x80412E28** | **0xD8** |

- **Why the first build overflowed.** At `-O2`, gcc unrolled both 8-word copy loops and inlined `tunic_restore` at its three call sites. `tunic_swap` alone was 0xF0 bytes.
- **As built:** `tunic_swap` is 0xC8 bytes (0xE4 with §5.6's casual case), `tunic_restore` 0x60, and the statics (`tunic_hdr`, `tunic_saved[8]`) 0x24. The allocation block and the call sites are the rest.
- **The check.** `TestT2EndsBelowMailbox` (`internal/inject/t2_budget_test.go`) checks the T2 end against `blob.go` at test time and logs the free bytes (0xD8 now).
- **0xD8 is the last room in T2.** M7 needs about 0x700 (docs/08 §4.5). The next C feature therefore needs finish-plan D2 (drop the dead shadow modes 1-4 and the echo ring) or the option-A arena move first.

**Memory.**
- 3 × the image size from GameHeap, allocated once. If `linktexS3TC` is 256x256 with no mips, that is 3 × 32 KiB = 96 KiB **(inferred; `tunic-probe` prints the size)**. Go refuses images over 64 KiB.
- docs/05 measured about 245 KiB GameHeap free on Outset (`docs/05-known-issues.md:370,662-663`). The echo ring (120 KiB) is mode-4 only.
- If GameHeap proves too tight live, the fallbacks are ArchiveHeap (where mini-Link's models live) or allocating only the slots in use.
- Gate: the heap error counters (`gameHeapErrors` 0x803F68F8, docs/08 §4.5) stay 0.

**Save-state coupling.** This branch changes `multiplayer.c` and `mailbox.h`, so:
1. Rebuild: `cd inject && rm -f build/temp/multiplayer.c.o && python build.py && python patch_iso.py && cd .. && python scripts/extract_blob.py && go build`. `blob.go` is already regenerated on the branch; `patch_iso.py` is the owner's step.
2. Run `./wwdev.exe auto-recapture`, then the owner presses **Shift+F1** once.
3. Users re-patch their ISO.

`saves/start.sav` is the Outset prologue in the casual clothes, so on its own nothing recolors on Link #1. §7.0 forces the hero's clothes in RAM for the test.

## 7. Owner live-check plan

### 7.0 Forcing the hero's clothes in RAM (no hero-clothes save needed)

**How the game picks the clothes.** There is no `checkCasualWearFlg` and no `mCollect` bit for this. `daPy_lk_c::playerInit` decides once per Link actor, that is, on every stage load (`tww/src/d/actor/d_a_player_main.cpp:12345-12347`):

```c
if (!dComIfGs_isEventBit(dSv_event_flag_c::UNK_2A80) || dComIfGs_getClearCount() != 0)
    onNoResetFlg1(daPyFlg1_CASUAL_CLOTHES);
```

- The hero's clothes need event bit **0x2A80** set and a clear count of **0**. The clothes-change cutscene sets that bit (`d_a_demo00.cpp:654`).
- The flag this drives is `mNoResetFlg1` bit 0x8 at daPy_lk_c+0x2A0.
- Nothing copies the flag from one Link actor to the next (it is only ever or-ed or cleared, never assigned), so the next stage load recomputes it.

**Option A: the event bit (recommended).**
- Event bit `0xHHLL` is byte `HH` of `mEvent` at 0x803C522C, mask `LL` (docs/04 "Event bits"). So 0x2A80 is **byte 0x803C5256, mask 0x80**.
- `poke-u32` needs 4-byte alignment, so go through the word at 0x803C5254. 0x803C5256 is its third byte, so the word mask is **0x00008000**.
- The clear count `mClearCount` at **0x803C4DA8** (u8) must read 0. A first-quest file does. `peek` prints whole words only, so read 4 bytes: the first (high) byte of the word is `mClearCount` and must be `00`.

```bash
WW_DOLPHIN_INDEX=0 ./wwdev.exe peek 0x803C5254 4               # note the word, call it W
WW_DOLPHIN_INDEX=0 ./wwdev.exe peek 0x803C4DA8 4               # first (high) byte = mClearCount, must be 00
WW_DOLPHIN_INDEX=0 ./wwdev.exe poke-u32 0x803C5254 <W | 0x00008000>
# then make playerInit run again: walk through any loading zone or door
# (Outset: into Link's house and back out).
```

- **Reversal:** `poke-u32 0x803C5254 W` (the original word), then another loading zone. Or reload `saves/start.sav`, which restores all of RAM.
- **Do not save the game** while the bit is set: it is save data.
- The bit also changes some Outset dialogue (Aryll, `d_a_npc_ls1.cpp:1432-1443`; Grandma, `d_a_npc_ba1.cpp:1637`). That does not matter for a screenshot.
- Do it on **both** Dolphins for the two-color test.

**Option B: swap in place, with no stage load.** This mimics the clothes-change demo (`setDemoData`, `d_a_player_main.cpp:10329-10339`). Let L = `peek 0x803CA754 4` (Link #1) and H = `peek L+0x338 4` (the worn header).
1. Swap the 0x20 bytes at H with the 0x20 bytes at L+0x33C. Take two `peek … 32` dumps, then write eight `poke-u32` each way.
2. Clear bit 0x8 of the word at L+0x2A0.
3. To reverse, do the same swap and set the bit again.

Some state is read only at `playerInit`; the hat's visibility, for one, follows the flag every frame, but anything cached at init does not. So prefer Option A, and keep Option B for a quick check without moving Link.

### 7.1 Texture probe (Go only; also runs on the old blob)

Setup: run `SAVE_STATE=… ./wwdev.exe dolphin2`, then `WW_TUNIC=off ./wwdev.exe mp-local`, and force the hero's clothes on Dolphin 0 (§7.0).

1. **Probe.** Run `WW_DOLPHIN_INDEX=0 ./wwdev.exe tunic-probe` and record:
   - (0) the name column now shows real names (`linktexS3TC`, the eye and mouth textures …), not garbage;
   - (a) the two `linktexS3TC` rows: format **0x0E**, size, mips, bytes;
   - (b) "spare image == used image: true";
   - (c) the materials sampling it, and on which stage. Expect 0, 16, 19 and 18 (hat). A stage ≥ 2 is a red flag for the diff-buffer sizing (TexNoNum = 2), so report it;
   - (d) the measured green (hue near 100-110) and "changes N of M blocks". A sane N is about 20-40 % of M; near 100 % means the mask is catching skin.
2. **Recolor pair.** Take `screenshot ww-A0.png`, run `tunic-recolor red`, wait 1 s, take `screenshot ww-A1.png`. Expected on Dolphin 0:
   - Link #1 **and** the mini-Link (the other player) both have a red tunic and hat. They share the texture; per-instance colors come from the C bracket (§7.2).
   - Skin, hair, eyes, white pants, brown boots and the belt are unchanged.
   - No blocky seams at the collar, sleeves or hat brim.
   - Dolphin 1 is unchanged.
3. **Repeat** with `blue`, `purple`, `black` and `#FFFFFF`, screenshotting each. Then `tunic-recolor reset`: green is back, byte for byte from the backup file.
4. **If nothing changes:**
   - Set Dolphin Graphics > Advanced > Texture Cache Accuracy to Safe and retry. If it then works, Dolphin's texture hash sampled no changed texel. The C bracket's separate buffers are unaffected (new addresses), but note it in the PR.
   - If it still fails, run `peek` on the image address after the write. A readback that matches the recolor while the render does not points at a draw path that bypasses `loadTexNo`, which would block the design.
5. **Pass:** clean recolor on 5 colors, no seams, and reset restores it. That gates §7.2.

### 7.2 C bracket (new blob; Shift+F1 recapture)

**Setup.**
1. Re-patch the ISO with this blob and run `auto-recapture`.
2. Run `SAVE_STATE=$(pwd)/saves/start.sav ./wwdev.exe dolphin2`.
3. Run `WW_TUNIC=off ./wwdev.exe mp-local` in the background.
4. Force the hero's clothes on both Dolphins (§7.0, Option A, then walk through a door on each).

**Steps.**
1. `WW_DOLPHIN_INDEX=0 ./wwdev.exe dump` shows `tunic: state=0x00 buf_size=0` (nothing asked yet). On a re-run (state already 0x01), check that every `enable=` is 0 first; if not, run `tunic-slot local off`, `tunic-slot 0 off` and `tunic-slot 1 off` on that Dolphin, because an earlier `tunic-slot` run leaves its slot enabled.
2. Take `WW_DOLPHIN_INDEX=0 ./wwdev.exe screenshot ww-A0.png`, run `WW_DOLPHIN_INDEX=0 ./wwdev.exe tunic-slot local red`, then take `ww-A1.png`.
   - The command prints the hero texture line (size, measured hue), then `tunic slot local is red (buf 0x…)`.
   - `dump` shows `state=0x01`, three nonzero buffers, `local enable=1`, and `swaps` climbing by about 30 per second per enabled slot (one swap per Link draw at 30 fps; face sync's ~60 Hz counter counts two sites per frame).
3. Check the pair:
   - only Link #1 is red (tunic, sleeves and hat), the mini-Link stays green, and Dolphin 1 is unchanged;
   - skin, hair, eyes, white pants, brown boots and the belt are unchanged;
   - there are no blocky seams at the collar, sleeves or hat brim.
4. Run `tunic-slot 0 blue`: only the mini-Link (the other player) turns blue. Then run `tunic-slot local off` and `tunic-slot 0 off`: both are green again.
5. `peek` checks:
   - `peek <buf0> 32` differs from `peek <hero image> 32` (the hero image address is in `tunic-probe`'s `used` row);
   - the hero image itself is byte-identical before and after (C never writes it).
6. **Regressions,** each with a screenshot:
   - the face-sync hammer (`face-sync-fake-loop`) still changes pupils only on the mini-Link;
   - the sky is intact, and the eye decals are intact;
   - the heap error counters at 0x803F68F8 are 0;
   - loading a save state mid-session: no crash. A save-state load restores the mailbox with the rest of RAM, so `dump` then shows what was saved (for start.sav, `state=0x00 buf_size=0` and zero buffers); that is correct. Once `tunic-slot` or the wired pipeline runs again, C allocates again (GameHeap was restored too, so probably the same addresses, but that is not guaranteed), and with the wiring on the colors are back within about 1 s. An in-game save reload (no save state) keeps state 0x01 and the same buffers;
   - a 20-transition soak.
7. **Casual check.** Step 4 turned both slots off, so:
   1. Reverse §7.0 Option A on Dolphin 0 (poke W back, then go through a door). Do not reload start.sav for this: a save-state load clears the mailbox, so every slot would have to be set again.
   2. Run `tunic-slot local red` again. Link #1 stays in the blue casual shirt; `dump` shows `local enable=1`, but `swaps` does not grow (C skips the local slot while Link #1 is casual).
   3. Run `tunic-slot 0 red`. The mini-Link draws in a red **hero** tunic and hat while Link #1 is casual, and `swaps` now grows by about 30 per second.
   4. `tunic-slot local off` and `tunic-slot 0 off` before §7.3.

### 7.3 Wiring (two players, two colors, then one shared color)

1. Two players need two colors, and mp-local gives both the same one (§5.1, Options), so until `MP_LOCAL_COLOR_A/B` exists run host and join in two shells instead, without `WW_TUNIC`: A (Dolphin 0) with no `--color` (green), B (Dolphin 1) with `--color red`. Put both Dolphins in the hero's clothes (§7.0).
2. Take one screenshot per Dolphin:
   - Dolphin 0: Link #1 green, the mini-Link (B) **red**;
   - Dolphin 1: Link #1 **red**, the mini-Link (A) green.
3. Run `dump` on each Dolphin:
   - Dolphin 1: `local enable=1` and slot 0 `enable=1`;
   - Dolphin 0: `local enable=0` (green needs no swap) and slot 0 `enable=1`.
4. Restart only B's join without `--color`, leaving A's host running: B keeps red, because the server remembers each name's color for as long as it runs. (Restarting the host restarts the server too, and the memory goes with it.)
5. Stop host and join, then run `WW_COLOR=purple ./wwdev.exe mp-local`. Both players ask for purple, and both get it. Take a screenshot pair: on each Dolphin, Link #1 and the mini-Link are both purple.
6. Put Dolphin 1 back in the casual clothes (§7.0 reversal, then a door). On Dolphin 0, B's mini-Link stops being swapped (appearance byte) and shows Dolphin 0's own texture.
7. The TUI (`./ww-multiplayer.exe` host and join, in two shells) shows `■ name (color)` for the other player.

## 8. Implementation plan (PRs)

1. **Colors in the session (Go only): built.** `mods/multiplayer/playercolor`, the hello preference and player-list trailer, the server's per-name assignment, client accessors, `--color`/`WW_COLOR`, the TUI swatch (now on `session.Player`), `TestT2EndsBelowMailbox`.
2. **Recolor library and diagnostics (Go only): built.** `internal/tunic` (now `wwapi/gfx/texture`), `tunic-probe` (name table fixed), `tunic-recolor`.
3. **C bracket: built.** The mailbox block +0x188..+0x1A0, `tunic_swap`/`tunic_restore`/allocation, `tunic-slot`, the `dump` fields, `TestMailboxLayout`. Needs one blob regen (done), Shift+F1 and a re-patch.
4. **Wiring: built.** `tunicSync` (now `avatar.Tunics`) in puppet-sync for the remote slots and the local slot, the appearance byte, `WW_TUNIC=off`.
5. **Left:**
   - `--color` in the help text and README, once §7.3 passes;
   - the lobby color picker (Wails);
   - the casual-shirt recolor (§5.5);
   - per-player `mp-local` colors.

## 9. Risks and open questions

**Risks:**
- **CMPR or not (high impact, cheap check).** If `linktexS3TC` is not CMPR, `wwapi/gfx/texture` needs another codec. RGB565 and RGB5A3 are straightforward, CI is easier (a palette edit). §7.1 step 1 answers this.
- **The mask catches other greens (medium).** For example, eye highlights, if any share the atlas. Tune `DefaultMask` from the §7.1 screenshots, or ship a hand-made block mask. A mask is our own data, not Nintendo's.
- **Diff buffer and texmap stage (low).** The differed buffer is sized for TexNoNum = 2 (`J3DPacket.h:39-49`). Face sync proves texmap stages 0-1. A tunic texmap ≥ 2 needs a look at the matpacket's differed sizing (check `tunic-probe` (c) before §7.2).
- **GameHeap headroom (medium).** See §6.
- **Code space (known).** 0xD8 bytes are left in T2 (§6). The next C feature needs D2 or M7 first.
- **Multiple Dolphins and IDs (low).** Colors key on names, so two humans with the same name share a color. That is the existing dedup's assumption too.

**Decided (owner, 2026-10-16):** green stays in the palette; the vanilla tunic is a valid color.

## Appendix: what this branch contains

**New:**
- `mods/multiplayer/playercolor/{playercolor,playercolor_test}.go`
- `internal/tunic/{cmpr,cmpr_test,restimg,restimg_test}.go` (since C12: `wwapi/gfx/texture`)
- `mods/multiplayer/net/color_test.go`
- `internal/inject/t2_budget_test.go`
- `playercolor_flag{,_test}.go`
- `tunic_diag.go` (`tunic-probe`, `tunic-recolor`)
- `tunic_sync.go`, `tunic_sync_test.go` (wiring, `tunic-slot`, the `dump` block; since C12: the slots in `wwapi/avatar/tunic.go`, the session side, `tunic-slot` and the `dump` block in `tunic_feed.go`; since D1b: the session side and the `dump` block in `mods/multiplayer/tunicsync`, `tunic-slot` in `tunic_diag.go`)
- `mailbox_layout_test.go` (since C9: `wwapi/mailbox/layout_gen.go` and `layout_test.go`)
- this document

**Changed:**
- `inject/src/multiplayer.c`, `inject/include/{mailbox,game}.h`: the C bracket and allocation. `internal/inject/blob.go` is regenerated.
- `mods/multiplayer/net/{protocol,handshake,server,client}.go`: trailer, hello, assignment, accessors
- `mods/multiplayer/net/coop_test.go`: the new `writeJoinRole` argument
- `internal/session/session.go`: `Player.Color` / `HasColor`
- `internal/tui/{dashboard,dashboard_test,controller_test}.go`: the swatch
- `main.go`: `applyColorPrefFromArgs`, `newPlayerClient`, the `tunic-*` cases, colors in `remoteViews`, the appearance byte in broadcast-pose, `syncTunics` in puppet-sync, the `dump` block

**Checks:** the check-matrix run is recorded in `TUNIC-SUMMARY.md` (untracked). `main.go` was already not gofmt-clean at the base and was left that way.
