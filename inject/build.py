"""
Build script for Wind Waker multiplayer code injection.
Uses Freighter to compile C code and inject it into the DOL.
"""

import os
import sys

def main():
    # Fix encoding for Freighter's emoji output
    os.environ["PYTHONIOENCODING"] = "utf-8"
    try:
        sys.stdout.reconfigure(encoding="utf-8", errors="replace")
        sys.stderr.reconfigure(encoding="utf-8", errors="replace")
    except Exception:
        pass

    # Set devkitPPC environment
    os.environ["DEVKITPPC"] = r"C:\devkitpro\devkitPPC"
    os.environ["DEVKITPRO"] = r"C:\devkitpro"
    os.environ["PATH"] = r"C:\devkitpro\devkitPPC\bin;" + os.environ.get("PATH", "")

    from freighter import Project

    # Monkey-patch to skip map saving (crashes without a CW symbol map)
    import freighter.devkit_tools
    original_save_map = freighter.devkit_tools.Project._Project__save_map
    freighter.devkit_tools.Project._Project__save_map = lambda self: None

    # Project setup
    proj = Project("WWMultiplayer", "GZLE01", auto_import=False)

    # Add source and include
    proj.add_include_folder("include")
    proj.add_c_file("src/multiplayer.c")

    # One-shot init hook on main01 (0x80006338 — proven working).
    # main01_init publishes &multiplayer_update at 0x80410700 for frame_shim.
    proj.hook_branchlink("main01_init", 0x80006338)

    # Per-frame hook. Targets the LAST bl in fapGm_Execute (0x80023204 ->
    # 0x802449AC). Chosen because: past the prologue (stack frame valid),
    # replaces a bl (caller already assumes volatile-reg clobber), return
    # value is discarded by the caller, and the PRIMARY per-frame work at
    # 0x800231FC still runs ahead of us. Cost: we lose whatever 0x802449AC
    # was doing — if that breaks rendering, move the hook earlier or find
    # a caller of fapGm_Execute to hook instead.
    proj.hook_branchlink("frame_shim", 0x80023204)

    # Draw-phase hook. `daPy_Draw` at 0x80108204 is Link #1's draw thunk;
    # at 0x80108210 it bls the real draw impl `daPy_lk_c::draw` at
    # 0x80107308. Freighter replaces that bl with a bl to our
    # `daPy_draw_hook`, which calls the original implementation and then
    # runs our per-frame mini-Link work. Intent: get our modelEntryDL
    # submissions into the legitimate actor-draw iteration. In practice
    # this still breaks sky textures (same as the execute-phase attempt),
    # so the hook is parked for the next session to investigate. See
    # docs/05-known-issues.md → "Mini-Link render pipeline".
    proj.hook_branchlink("daPy_draw_hook", 0x80108210)

    # Set entry function (required for linker)
    proj.set_entry_function("main01_init")

    # Set map output path (Freighter crashes without this)
    proj.add_map_output("build/")

    # Compiler flags
    proj.common_args.append("-O2")
    proj.common_args.append("-ffreestanding")
    proj.common_args.append("-fno-builtin")
    # Strip C++ exception / unwind metadata — pure C code, Freighter emits
    # .eh_frame (~0x158) + .eh_frame_hdr (~0x34) regardless. Those ~0x18C
    # bytes of dead weight pushed the 2026-04-19 echo-ring build past the
    # ISO's reserved DOL space by 0x88 bytes. Killing them buys headroom
    # for future growth without another __OSArenaLo bump.
    proj.common_args.append("-fno-exceptions")
    proj.common_args.append("-fno-unwind-tables")
    proj.common_args.append("-fno-asynchronous-unwind-tables")

    # Build
    input_dol = "original.dol"
    output_dol = "patched.dol"

    if not os.path.exists(input_dol):
        print(f"ERROR: {input_dol} not found!")
        sys.exit(1)

    print("Building Wind Waker Multiplayer mod...")
    print(f"  Input:  {input_dol}")
    print(f"  Output: {output_dol}")
    print()

    try:
        # Inject at 0x80410000 — just past the original __ArenaLo (0x8040EFC0)
        # to minimize arena loss. Dolphin refused T2 at 0x803FD000 but works at
        # 0x80500000; probing whether 0x80410000 also loads.
        proj.build(
            dol_inpath=input_dol,
            inject_address=0x80410000,
            dol_outpath=output_dol,
            verbose=True,
            clean_up=False
        )
        print()
        print(f"Success! Patched DOL saved to {output_dol}")
    except Exception as e:
        print(f"Build failed: {e}")
        import traceback
        traceback.print_exc()
        sys.exit(1)

    # Post-build: patch OSInit so __OSArenaLo starts at 0x80412000 (past our
    # T2 at 0x80410000 + the echo-ring mailbox at 0x80411F00) instead of the
    # linker's 0x8040EFC0. Arena loses ~16 KB from the original layout.
    #
    # History: was 0x80411000 while mod fit in 0xF00 bytes; echo-ring code
    # (docs/06 "Next Session Priority" track 1) pushed .text past 0x80411000,
    # forcing both the mailbox and the arena start to shift up by 0x1000.
    # Mailbox now lives at 0x80411F00..0x80411FAC; MUST be kept in sync with
    # MAILBOX_ADDR in inject/include/mailbox.h.
    #
    # Do NOT bump further without measuring ZeldaHeap headroom: the 1 MB
    # carve-out experiment (0x80511000) broke Outset archive loading. A 4 KB
    # bump is well inside noise (ZeldaHeap ~6 MB).
    T1_LOAD = 0x800056e0
    T1_FILE = 0x2620
    patches = [
        (0x80301818, b"\x60\x00\x00\x00"),  # nop (always fall through)
        (0x8030181C, b"\x3c\x60\x80\x41"),  # lis r3, 0x8041
        (0x80301820, b"\x38\x63\x20\x00"),  # addi r3, r3, 0x2000  -> r3 = 0x80412000
        (0x80301838, b"\x48\x00\x00\x30"),  # b +0x30 (skip debug path)
    ]
    with open(output_dol, "r+b") as f:
        for addr, bytes_ in patches:
            file_off = T1_FILE + (addr - T1_LOAD)
            f.seek(file_off)
            f.write(bytes_)
            print(f"Patched @ 0x{addr:08x} (file 0x{file_off:x}): {bytes_.hex()}")

    # Revert Freighter's silent clobbers of real game code. Freighter overwrites
    # these spots to shift the game's stack/arena to make room for its own
    # stack_addr layout — but that causes rendering corruption. Restore the
    # original bytes so the game uses its own memory plan; our T2 survives
    # because of the OSInit arena-lo patch above.
    freighter_clobbers = [
        # (file_offset, byte_count)
        (0x00002410, 8),
        (0x000e82ac, 8),
        (0x000e82e4, 8),
        (0x000ee7fc, 12),
        (0x000ee80c, 4),
    ]
    with open(input_dol, "rb") as f:
        orig = f.read()
    with open(output_dol, "r+b") as f:
        for off, n in freighter_clobbers:
            f.seek(off)
            f.write(orig[off:off + n])
            print(f"Reverted Freighter clobber @ file 0x{off:08x} ({n}B)")

if __name__ == '__main__':
    main()
