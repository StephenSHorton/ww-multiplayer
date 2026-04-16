# Wind Waker GZLE01 Memory Addresses

All addresses are for the **NTSC-U** version (Game ID `GZLE01`). Other versions have different addresses.

## Reference Documentation

Primary source: the [zeldaret/tww decompilation](https://github.com/zeldaret/tww) at `C:\Users\4step\Desktop\tww-decomp`.

The symbol file is at `config/GZLE01/symbols.txt` — contains every known function and global address.

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

These are offsets from `0x803C4C08`. For absolute addresses, add `0x803C4C0C = 0x803C4C08 + 4`.

| Offset from base | Field |
|------------------|-------|
| `+0x04` | `rupees` (u16, BE) |
| `+0x01` | `max_health` (u16) |
| `+0x03` | `current_health` (u16) |
| `+0x12` | `wallet_type` (u8: 0=200, 1=1000, 2=5000) |
| `+0x13` | `max_magic` |
| `+0x14` | `current_magic` |

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

Controller state is at `0x803ED84A` (u16, bitfield). Used by AR codes like `0A3ED84A 00000020` (if R pressed).

Bit masks:
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
