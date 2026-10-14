# Wind Waker GZLE01 Memory Addresses

All addresses are for the **NTSC-U** version (Game ID `GZLE01`). Other versions have different addresses.

## Reference Documentation

Primary source: the [zeldaret/tww decompilation](https://github.com/zeldaret/tww). Clone it locally for symbol lookups and reading game source.

The symbol file is at `config/GZLE01/symbols.txt` — contains every known function and global address.

Citations such as `d_save.h:40` are file:line in the decomp at commit `4d78c994`, and `sym:N`
is line N of `symbols.txt`. Decomp line numbers drift, so re-check them against that commit.

## Functions

| Address | Symbol | Purpose |
|---------|--------|---------|
| `0x8002451C` | `fopAcM_create(s16, ...)` | Spawn actor (async, returns process ID) |
| `0x80024614` | `fopAcM_fastCreate(s16, ...)` | Spawn actor (sync, returns actor pointer) |
| `0x80024478` | `fopAcM_delete` | Delete an actor |
| `0x800241C0` | `fopAcM_SearchByID` | Find actor by process ID |
| `0x80024230` | `fopAcM_SearchByName` | Find actor by profile name |
| `0x80006338` | `main01` | Main game loop function (we hook this) |
| `0x800F1034` | `dComIfGp_getPlayer(int idx)` | Get player pointer by index |

## Globals

| Address | Symbol | Purpose |
|---------|--------|---------|
| `0x803C4C08` | `g_dComIfG_gameInfo` | Global game info struct (size 0x1D1C8) |
| `0x803CA754` | `mpPlayerPtr[0]` | Pointer to Link's actor |
| `0x803CA758` | `mpPlayerPtr[1]` | Pointer to companion actor (Medli, Makar, etc.) |
| `0x803CA75C` | `mpPlayerPtr[2]` | Pointer to ship (King of Red Lions) |
| `0x803F6A68` | `g_fpcPf_ProfileList_p` | Pointer to actor profile list |
| `0x8038FD8C` | `g_profile_PLAYER` | Player actor profile definition |

## Actor Struct Offsets (from `fopAc_ac_c` base)

| Offset | Type | Field |
|--------|------|-------|
| `+0x1D0` | actor_place (0x14) | `home` (spawn position) |
| `+0x1E4` | actor_place (0x14) | `old` (previous position) |
| `+0x1F8` | `cXyz` (12 bytes) | `current.pos` (3× f32: x, y, z) |
| `+0x204` | `csXyz` (6 bytes) | `current.angle` (3× s16) |
| `+0x20A` | `s8` | `current.roomNo` |
| `+0x20B` | `u8` | padding |
| `+0x20C` | `csXyz` | `shape_angle` (visual rotation) |
| `+0x214` | `cXyz` | `scale` |
| `+0x220` | `cXyz` | `speed` (velocity vector) |
| `+0x24C` | `J3DModel*` | `model` (non-null for visible actors) |
| `+0x254` | `f32` | `speedF` (forward speed) |
| `+0x258` | `f32` | `gravity` |
| `+0x285` | `s8` | `health` |
| `+0x31D8` | `int` | `mCurProc` (Link only — state machine) |

## Save Data (relative to `g_dComIfG_gameInfo`)

These are offsets from `0x803C4C08`; the absolute address is `0x803C4C08 + offset`. The save
info `dSv_info_c` sits at +0 of `g_dComIfG_gameInfo` (`d_com_inf_game.h:914`), its
`dSv_save_c mSavedata` at +0 (`d_save.h:991`), the player at +0 of that (`d_save.h:908`), and
`dSv_player_status_a_c` at +0 of the player (`d_save.h:586`), so these are the
`dSv_player_status_a_c` offsets (`d_save.h:40-53`). All multi-byte values are big-endian.

| Offset from base | Absolute | Field | Source |
|------------------|----------|-------|--------|
| `+0x00` | `0x803C4C08` | `mMaxLife` (u16, quarter hearts; a new file has 12 = 3 hearts) | `d_save.h:40`; `d_save.cpp:26` |
| `+0x02` | `0x803C4C0A` | `mLife` (u16, quarter hearts) | `d_save.h:41`; `d_save.cpp:27` |
| `+0x04` | `0x803C4C0C` | `mRupee` (u16) | `d_save.h:42` |
| `+0x09` | `0x803C4C11` | `mSelectItem[5]` (u8; X, Y, Z buttons) | `d_save.h:45` |
| `+0x0E` | `0x803C4C16` | `mSelectEquip[4]` (u8; sword, shield, bracelet, ?) | `d_save.h:46` |
| `+0x12` | `0x803C4C1A` | `mWalletSize` (u8: 0=200, 1=1000, 2=5000) | `d_save.h:47`; `d_save.h:20-30` |
| `+0x13` | `0x803C4C1B` | `mMaxMagic` (u8) | `d_save.h:48` |
| `+0x14` | `0x803C4C1C` | `mMagic` (u8) | `d_save.h:49` |

**Corrected 2026-10-14 (#23).** This table used to list max and current health at `+0x01` and
`+0x03`, and said to add `0x803C4C0C` to get an absolute address. Both were wrong: the life
fields are u16 at `+0x00` and `+0x02`, and the offsets are from `0x803C4C08` itself. The HUD
caps max life at `0x50` quarter hearts, i.e. 20 hearts (`d_meter.cpp:1432-1433`).

## Co-op addresses

Used by the co-op design in [`docs/08-coop-design.md`](08-coop-design.md); its Appendix A is
the full table. Each row here was re-checked against the decomp at commit `4d78c994`, but none
has been checked on a live Dolphin yet; the day-0 `peek` probes in docs/08 §8.3 do that.
**(inferred)** marks an offset taken from a decomp comment with no `STATIC_ASSERT` behind it.

Two bases: the save info `dSv_info_c` (0x12A0 bytes) at `0x803C4C08` (sym:17472;
`d_save.h:991-1005`), and `dComIfG_play_c play` right after it at
`0x803C5EA8 = 0x803C4C08 + 0x12A0` (`d_com_inf_game.h:914-915`).

### memBit (per-stage-table progress)

A save table *t* is 0..15 (`d_save.h:888-906`): 0 sea, 3 DRC, 4 FW, 5 TotG, 6 ET, 7 WT,
0xB interiors.

| Name | Address | Size | Source |
|------|---------|------|--------|
| `mMemory[16]`, saved memBit of table *t* | `0x803C4F88 + 0x24·t` | 0x24 each | `d_save.h:909` (mSavedata +0x380) |
| live memBit of the current table | `0x803C5380` | 0x24 | `d_save.h:992` (+0x778) |

`dSv_memBit_c` layout (`d_save.h:684-690`):

| Offset | Field |
|--------|-------|
| `+0x00` | u32 `mTbox`: chests opened |
| `+0x04` | u32 `mSwitch[4]`: memory switches 0x00-0x7F; switch n is bit `n & 31` of `mSwitch[n >> 5]` (`d_save.cpp:1084-1087`) |
| `+0x14` | u32 `mItem[1]`: item pickups |
| `+0x18` | u32 `mVisitedRoom[2]` |
| `+0x20` | u8 `mKeyNum`: small keys (live `0x803C53A0`; saved `0x803C4FA8 + 0x24·t`) |
| `+0x21` | u8 `mDungeonItem`: bits 0-5 map, compass, boss key, boss defeated, heart container taken, boss demo (`d_save.h:639-646`) |

On every stage load `dStage_stagInfoInit` (`0x8004230C`, sym:1289) copies the saved slot into
the live copy (`getSave`, `d_stage.cpp:1674-1675`, `d_save.cpp:1506-1509`).
`dStage_Delete` (`0x80043464`, `d_stage.cpp:2247-2256`) copies it back (`putSave`,
`d_save.cpp:1512-1515`).

### Event bits

| Name | Address | Size | Source |
|------|---------|------|--------|
| `mEvent` (`dSv_event_c`) | `0x803C522C` | 0x100 | `d_save.h:716,911` (mSavedata +0x624) |

- An event-bit id `0xHHLL` is byte `HH`, mask `LL`: `onEventBit` does
  `mFlags[id >> 8] |= id & 0xFF` (`d_save.cpp:1187-1189`). Bit `0x2A08` is mask `0x08` of
  `0x803C5256`.
- Bytes 0x00-0x41 (`0x803C522C..0x803C526D`) hold the 487 named bits. Bytes 0x79-0xFF
  (`0x803C52A5..0x803C532B`) hold 131 multi-bit registers, which `setEventReg` clears by mask
  and then sets (`d_save.cpp:1202-1205`). Bytes 0x42-0x78 have no names. The names are in
  `include/d/d_save_event_flag.inc` (all of them made up by the decomp).

### Stage name and table

| Name | Address | Size | Source |
|------|---------|------|--------|
| `mCurStage`: name[8], point s16 +0x8, room s8 +0xA, layer s8 +0xB | `0x803C9D3C` | 0xC | `d_com_inf_game.h:723` (play +0x3E94); `d_stage.h:949-952` |
| `mNextStage`: same layout, plus `mEnable` s8 at +0xC = `0x803C9D54` (set to 1 when a stage change is queued, `d_stage.cpp:26-31`) | `0x803C9D48` | 0xE | `d_com_inf_game.h:724` (play +0x3EA0); `d_stage.h:955-968` |
| `mStageData.mpStagInfo`: pointer to the loaded stage's STAG | `0x803C9DA0` | 4 | `d_com_inf_game.h:726` (play +0x3EB0); `d_stage.h:586` (+0x48) |
| STAG `mProp` at +0x09: save table = `(mProp >> 1) & 0x7F`, key HUD on = `mProp & 1` | `*0x803C9DA0 + 0x09` | 1 | `d_stage.h:70,1023-1029` |
| `mDan.mStageNo`: s8 save table of the loaded stage, set by `initDan` at stage load | `0x803C53A4` | 1 | `d_save.h:752,993` (+0x79C); `d_save.cpp:1225-1235`; `d_stage.cpp:1676` |
| `dStage_roomControl_c::mStayNo`: current room | `0x803F6A78` | 1 | sym:18667 |

### Gates

The co-op writer only writes while these say the game is settled (docs/08 §3.2).

| Name | Address | Size | Source |
|------|---------|------|--------|
| `mpPlayerPtr[0]` (Link; 0 when there is no player actor) | `0x803CA754` | 4 | `d_com_inf_game.h:791` (play +0x48AC) |
| `mNextStage.mEnable` | `0x803C9D54` | 1 | see the stage table |
| `mEvtCtrl.mOrderCount` (s8) / `mMode` (u8, 0 = no event) **(inferred)** | `0x803C9EA0` / `0x803C9EA2` | 1 / 1 | `d_com_inf_game.h:728` (play +0x3F38); `d_event.h:152,154` (+0xC0, +0xC2) |
| `dMenu_pause` | `0x803F7097` | 1 | sym:19130 |
| `mMiniGameType` | `0x803CA8E2` | 1 | `d_com_inf_game.h:886` (play +0x4A3A) |
| `mPlayerInfoBufferStageNo` (1-4 while a Ganon's Tower boss-refight snapshot is active, else 0) | `0x803CA8C8` | 1 | `d_com_inf_game.h:882` (play +0x4A20); `d_com_inf_game.cpp:1446-1457,1580-1581` |
| mailbox `spawn_trigger` (mod heartbeat, bumped once per frame by `multiplayer_update`) | `0x80412F00` | 4 | `inject/include/mailbox.h:33,109`; `inject/src/multiplayer.c:342` |

### Pending counters

The HUD adds these `dComIfG_play_c` deltas into the save fields, then zeroes them. The
offsets are from `d_com_inf_game.h`; the behaviour is from `d_meter.cpp`, which the decomp
does not match yet, so treat the caps and the refill as inferred until the docs/08 §8.3 HUD
test confirms them.

| Name | Address | Size | Source |
|------|---------|------|--------|
| `mItemLifeCount` (f32, current-life delta) | `0x803CA764` | 4 | `d_com_inf_game.h:793`; `dMeter_LifeMove`, `d_meter.cpp:1423-1453` |
| `mItemRupeeCount` (s32, rupee delta) | `0x803CA768` | 4 | `d_com_inf_game.h:794`; `d_meter.cpp:5037-5054` |
| `mItemKeyNumCount` (s16, small-key delta; applied only while the stage's key HUD is on) | `0x803CA77C` | 2 | `d_com_inf_game.h:799`; `dMeter_keyMove`, `d_meter.cpp:5242-5257` |
| `mItemMaxLifeCount` (s16, max-life delta in quarter hearts; caps at 0x50 and refills) | `0x803CA77E` | 2 | `d_com_inf_game.h:800`; `dMeter_LifeMove`, `d_meter.cpp:1423-1439` |
| `mItemMaxMagicCount` (s16, max-magic delta; caps at 0x20) | `0x803CA784` | 2 | `d_com_inf_game.h:803`; `dMeter_magicGaugeMove`, `d_meter.cpp:4069-4075` |

## Actor Profiles (PROC IDs)

Defined in `include/d/d_procname.h` in the decomp.

| Value | Name |
|-------|------|
| `0x00A9` | `PROC_PLAYER` (Link) |
| `0x0109` | `PROC_Obj_Demo_Barrel` |
| `0x0128` | `PROC_BOMB` |
| `0x01CE` | `PROC_Obj_Barrel` |
| `0x01CF` | `PROC_Obj_Barrel2` |

## Memory Layout

```
0x80000000  ─── RAM start (cached mirror)
0x80003100      First function (.init)
0x800056E0      Main .text section
0x80338680      End of .text, start of .data
0x803A2960      Start of BSS
0x803C4C08      g_dComIfG_gameInfo
0x803F7D00      Last data section
0x803FCF20      DOL end / original mod end
0x803FCFA8      BSS end
...             Stack/heap area
0x81800000  ─── RAM end (24MB)
```

## Controller Input

WW uses libogc's `JUTGamePad`. Per-frame flow inside `JUTGamePad::read()`
(`0x802C3980`):
1. `bl PADRead` (`0x802C39A0` → `0x80315A20`) fills `mPadStatus[4]`.
2. Per port, `mPadMStick[i].update(...)` / `mPadSStick[i].update(...)` /
   `mPadButton[i].update(&mPadStatus[i], ...)` derive the processed
   state that game logic actually reads.

PPC addresses (from `tww-decomp/config/GZLE01/symbols.txt`):
| Symbol | Address | Size | Notes |
|---|---|---|---|
| `JUTGamePad::mPadStatus[4]` | `0x803ED818` | `0x30` | Raw `PADStatus` array, 12 B per port. Port 0 = active player. |
| `JUTGamePad::mPadButton[4]` | `0x803ED848` | `0xC0` | Derived button state; *not* the live read site for game logic. AR codes' `28...`/`0A...` conditions read `0x803ED84A` (mPadButton[0]+0x02) from here. |
| `JUTGamePad::mPadMStick[4]` | `0x803ED908` | `0x40` | Derived main-stick state. |

`PADStatus` layout (libogc, 12 B):
| Offset | Type | Field |
|---|---|---|
| `+0x00` | `u16` | `button` bitfield (BE on PPC) |
| `+0x02` | `s8` | `stick_x` (-128..+127, 0 = centred) |
| `+0x03` | `s8` | `stick_y` |
| `+0x04` | `s8` | `substick_x` |
| `+0x05` | `s8` | `substick_y` |
| `+0x06` | `u8` | `trigger_l` |
| `+0x07` | `u8` | `trigger_r` |
| `+0x08` | `u8` | `analog_a` |
| `+0x09` | `u8` | `analog_b` |
| `+0x0A` | `s8` | `err` (PAD_ERR_NONE = 0) |

Button bit masks (`button` u16):
- `0x0001` — DPad Left
- `0x0002` — DPad Right
- `0x0004` — DPad Down
- `0x0008` — DPad Up
- `0x0010` — Z
- `0x0020` — R
- `0x0040` — L
- `0x0100` — A
- `0x0200` — B
- `0x0400` — X
- `0x0800` — Y
- `0x1000` — Start

**Writing input from Go: don't.** External `WriteProcessMemory` writes to
`mPadStatus[0]` survive readback in the writing process but don't
propagate to the game's read path (race against per-frame PADRead and/or
Dolphin's dual memory mapping, see `docs/02`). Use the C-side
`pad_read_shim` hook instead: write the mailbox `input_*` fields and set
`input_enable=1`, then `JUTGamePad::read()`'s downstream derivations
pick up the synthetic values cleanly. See `inject/src/multiplayer.c`
and the `input` subcommand in `main.go`.
