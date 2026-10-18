# The REL module (inject/rel)

Mod code shipped as a REL, loaded by the game's own dynamic linker,
instead of as T2 bytes in the DOL (T2 has 0x478 bytes left below the
mailbox). Plan: `ww-multiplayer-handoff/design/rel-module-plan.md`.
This first module only proves the pipeline; it does nothing a player
can see.

## What it is

`rel_main.c` is an actor module. Its profile, `g_profile_WWREL`
(`.rodata`, 0x30 bytes, the decomp's `actor_process_profile_definition`),
describes proc 0xB5 with a five-method table (create, delete, execute,
is_delete, draw) and an actor of `sizeof(fopAc_ac_c)` (0x290 in GZLE01)
plus one u32 of its own:

- `_prolog` runs the module's constructors (none) and the linker's
  `ModuleProlog`, then writes the mailbox slack words (below).
- create allocates nothing and registers no model or collision, so the
  actor is invisible and untouchable; it returns `cPhs_COMPLEATE_e`.
- execute bumps the slack frame counter once per frame.
- draw, delete and is_delete return TRUE.
- `_epilog` clears the magic word, then `ModuleEpilog` and the
  destructors.

Game functions are called through absolute function-pointer casts, as
`wwapi.h` does, so those calls need no relocations. The only game
symbols the ELF references by address are the two method tables the
profile points at, `g_fpcLf_Method` and `g_fopAc_Method`, defined in
`ww_rel.ld` (addresses from the decomp's `config/GZLE01/symbols.txt`).

## The proc hijack

Proc 0xB5 is `PROC_RECTANGLE`, an unused dummy actor whose module,
`d_a_rectangle.rel` (module ID 0x58), lives in `RELS.arc`. The profile
list `g_fpcPfLst_ProfileList[]` is itself a REL in that archive,
`f_pc_profile_lst.rel`, and its slot 0xB5 is a relocation against module
0x58. The patcher (Go, `wwapi/rel`) converts `RELELF` to a REL, writes it
into `RELS.arc` as module 0x58 under the same file name (the loader opens
RELs by name), and edits that one relocation to point at
`g_profile_WWREL`. `mProcName` in the profile must stay 0xB5:
`scripts/extract_blob.py` checks the word in the ELF.

The game loads the module when the first actor of proc 0xB5 is created
(`fopAcM_create(0xB5, ...)` from T2, or `cDyl_Link(0xB5)` to keep it
resident) and unlinks it when the last one is deleted, which happens on
every stage transition. Nothing in the module's `.bss` survives that.

## The mailbox slack words

`Mailbox` (`inject/include/mailbox_core.h`) is 0x1B0 bytes at
`MAILBOX_ADDR` 0x80412F00; `__OSArenaLo` is 0x80414000, so 0xF50 bytes
past the struct belong to nobody. The module uses the first three words.
The struct and its size are unchanged; the offsets are `#define`s in
`rel_main.c`, read by `extract_blob.py` into `blob.go`.

| Offset from MAILBOX_ADDR | Address    | Word                | Writer                    |
|--------------------------|------------|---------------------|---------------------------|
| +0x1B0 `WWREL_MAGIC_OFF` | 0x804130B0 | `'WWRL'` 0x5757524C | `_prolog` sets, `_epilog` clears |
| +0x1B4 `WWREL_BUILD_ID_OFF` | 0x804130B4 | build id          | `_prolog`                 |
| +0x1B8 `WWREL_FRAME_OFF` | 0x804130B8 | frame counter       | `_prolog` zeroes, execute +1 |

The build id is a CRC-32 of `inject/rel`'s sources (`build.py`
`rel_build_id`, passed as `-DWWREL_BUILD_ID`), also exported as
`g_wwrel_build_id` in `.rodata` so `extract_blob.py` can read it from the
ELF and emit `RELBuildID`. A source hash rather than a timestamp keeps
the ELF byte-identical across rebuilds of the same source, which
`blob.stamp` relies on.

Go reads them as `inject.RELMagic`, `RELMagicOff`, `RELBuildIDOff`,
`RELFrameOff`, `RELBuildID`: magic present and build id equal means the
patched module is linked; the frame counter moving means its actor runs.

## The build

`cd inject && python build.py` builds T2 as before and then this module
(`build_rel`; `--no-rel` skips it):

```
powerpc-eabi-gcc -mcpu=750 -std=c99 -fno-inline -Og -fshort-enums
    -fno-jump-tables -ffreestanding -fno-builtin -G0
    -fno-asynchronous-unwind-tables -fno-unwind-tables -fno-ident
    -Wall -Wextra -Iinclude -DWWREL_BUILD_ID=0x... -c rel/rel_main.c
powerpc-eabi-ld --relocatable -T rel/ww_rel.ld -Map build/wwrel.map
    -o build/wwrel.elf
```

The flags are ww-online's (`WWOnline.Patcher/Tools/DevKitPpcToolchain.cs`)
plus this project's freestanding ones. `-fno-jump-tables` because a jump
table needs `R_PPC_REL32`, which the converter does not emit; `-Og` and
not `-Os` because `-Os` calls libgcc's `_restgpr_*_x`, which the DOL
lacks; `-G0` so nothing lands in `.sdata`/`.sbss`/`.sdata2`, which are
addressed off r13/r2 through `R_PPC_EMB_SDA21` relocations the game's
linker cannot apply (the T2 build uses `-G0` too); no unwind tables and
no `.comment`, so the ELF has only `.text`, `.rodata`, `.data`, `.bss`,
`.ctors`, `.dtors` and their `.rela.*`.

`build_rel` prints the section table, the relocation counts per type,
`_prolog`/`_epilog`/`_unresolved`/`g_profile_WWREL` with section and
offset, and the `.text` size, and fails on an undefined symbol (the game
would route the call to `_unresolved`), an `R_PPC_REL32`, a small-data
section, or a missing symbol. `scripts/extract_blob.py` repeats those
checks on the ELF it embeds (pure Python, no devkitPPC), verifies the
profile's `mProcName`, and emits `RELELF` plus the constants into
`internal/inject/blob.go`; without the ELF it emits an empty `RELELF`.
`blob.stamp` carries the ELF's SHA-256 (`rel` line) and `inject/rel`'s
sources among its inputs, so CI catches a REL change without a regen,
while the `blob` line stays the T2 payload alone.

Expected relocations for this module: `R_PPC_ADDR32` for the profile's
five method pointers, its method-table pointer and the two `ww_rel.ld`
symbols, and `R_PPC_ADDR16_HA`/`_LO` pairs for `&_ctors`/`&_dtors` in the
prolog and epilog.

## Who creates the actor

Nothing in the vanilla game creates proc 0xB5, so T2's `src/mp/relload.c` does:
every frame once Link exists it checks the magic word, and when the module is
not linked it creates one actor of proc 0xB5 at Link's position (invisible,
allocates nothing), at most three times per stage with a 60-frame cooldown. The
game's loader then loads and links the module and `_prolog` writes the magic;
a stage transition would delete the actor, unlink the module and clear the
magic, and the next stage would start over, but live (2026-10-18, a stage-hop
from Outset to A_mori) the actor survived: created from the frame hook, outside
any stage actor, it lives in a layer the stage load keeps (inferred: the root
layer) and the module stayed linked through the load. On a disc whose RELS.arc was
not patched the vanilla dummy is created instead and never writes the magic;
`wwapi.Info` reports the module as not linked.
