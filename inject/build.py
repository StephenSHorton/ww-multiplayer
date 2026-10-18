"""
Build script for Wind Waker multiplayer code injection.
Uses Freighter to compile C code and inject it into the DOL.

The devkitPPC folder comes from the DEVKITPPC environment variable
(default C:\\devkitpro\\devkitPPC). devkitPro's Windows installer sets it
in MSYS form (/opt/devkitpro/devkitPPC); that prefix is read as
C:\\devkitpro. Besides patched.dol, the build writes build/WWMultiplayer.map,
a Dolphin symbol map of T2 (Symbols > Load Other Map File).

After T2 it builds the REL module (rel/, see rel/README.md) into
build/wwrel.elf, a relocatable ELF that scripts/extract_blob.py embeds
and the patcher converts to a REL; `python build.py --no-rel` skips it.
"""

import importlib.util
import os
import re
import subprocess
import sys
import zlib

# The C units, in link order: the core first, then the multiplayer mod.
C_SOURCES = [
    "src/core/hooks.c",
    "src/core/services.c",
    "src/mp/puppets.c",
    "src/mp/minilink.c",
    "src/mp/face.c",
]

DEFAULT_DEVKITPRO = r"C:\devkitpro"
DEFAULT_DEVKITPPC = os.path.join(DEFAULT_DEVKITPRO, "devkitPPC")
MSYS_DEVKITPRO = "/opt/devkitpro"

# Freighter's linked ELF and the symbol map written from it.
ELF = os.path.join("build", "temp", "WWMultiplayer.o")
SYMBOL_MAP = os.path.join("build", "WWMultiplayer.map")

# The REL module (rel/README.md): its C units, the symbol script of the
# game data it references by address, and its outputs. Built apart from
# T2 (plain gcc + `ld --relocatable`, no Freighter); extract_blob.py
# embeds REL_ELF as is and the Go converter (wwapi/rel) makes the REL.
REL_SOURCES = ["rel/rel_main.c"]
REL_LD = os.path.join("rel", "ww_rel.ld")
REL_OBJ_DIR = os.path.join("build", "temp", "rel")
REL_ELF = os.path.join("build", "wwrel.elf")
REL_MAP = os.path.join("build", "wwrel.map")
# The symbols the converter and the patcher look up by name.
REL_SYMBOLS = ["_prolog", "_epilog", "_unresolved", "g_profile_WWREL"]
# Compile flags, ww-online's (WWOnline.Patcher/Tools/DevKitPpcToolchain.cs)
# plus this project's freestanding ones:
#   -fno-jump-tables   a switch's jump table would need R_PPC_REL32,
#                      which the converter does not emit;
#   -Og, -fno-inline   -Os needs libgcc's _restgpr_*_x (not in the DOL);
#   -G0                no small data: .sdata/.sbss/.sdata2 are addressed
#                      off r13/r2 (the DOL's small-data bases) through
#                      R_PPC_EMB_SDA21 relocations the game's linker
#                      cannot apply, so every object stays in
#                      .data/.bss/.rodata (checked after the link);
#   -fno-*unwind-tables, -fno-ident   no .eh_frame (its R_PPC_REL32
#                      relocations would fail the check) and no .comment.
REL_CFLAGS = [
    "-mcpu=750", "-std=c99", "-fno-inline", "-Og", "-fshort-enums",
    "-fno-jump-tables", "-ffreestanding", "-fno-builtin", "-G0",
    "-fno-asynchronous-unwind-tables", "-fno-unwind-tables", "-fno-ident",
    "-Wall", "-Wextra", "-Iinclude",
]
# Sections a REL cannot carry: small data (see -G0) and anything a
# converter would have to understand. A new section name here means a
# code change the converter must learn first.
REL_BAD_SECTIONS = {".sdata", ".sbss", ".sdata2", ".sbss2", ".eh_frame", ".eh_frame_hdr"}


def devkitppc_dir():
    """The devkitPPC folder: $DEVKITPPC, else the default."""
    env = os.environ.get("DEVKITPPC", "").strip()
    if not env:
        return DEFAULT_DEVKITPPC
    if os.name == "nt" and not os.path.isdir(env):
        # MSYS form, as devkitPro's installer sets it. Git Bash passes it
        # on as C:/Program Files/Git/opt/devkitpro/devkitPPC.
        i = env.replace("\\", "/").lower().find(MSYS_DEVKITPRO)
        if i >= 0:
            return os.path.normpath(DEFAULT_DEVKITPRO + env[i + len(MSYS_DEVKITPRO):])
    return os.path.normpath(env)


def tool(bin_dir, name):
    return os.path.join(bin_dir, name + (".exe" if os.name == "nt" else ""))


def setup_devkitppc():
    """Point the environment and Freighter at devkitPPC.

    Freighter 0.2.4 hard-codes C:/devkitPro/devkitPPC/bin/ (Windows) or
    /opt/devkitpro/devkitPPC/bin/ when freighter.constants is imported, and
    its set_devkitppc has no effect, so freighter.constants is loaded here
    with that path replaced. This runs at import time, not in main():
    Freighter compiles in child processes, which import this file again
    (as __mp_main__) before they import Freighter.
    """
    ppc = devkitppc_dir()
    bin_dir = os.path.join(ppc, "bin")
    if not os.path.isfile(tool(bin_dir, "powerpc-eabi-gcc")):
        print(f"ERROR: no powerpc-eabi-gcc in {bin_dir} "
              f"(DEVKITPPC={os.environ.get('DEVKITPPC', '')!r}); "
              f"set DEVKITPPC to the devkitPPC folder", file=sys.stderr)
        sys.exit(1)
    os.environ["DEVKITPPC"] = ppc
    os.environ["DEVKITPRO"] = os.path.dirname(ppc)
    os.environ["PATH"] = bin_dir + os.pathsep + os.environ.get("PATH", "")
    if "freighter.constants" in sys.modules:
        return bin_dir
    spec = importlib.util.find_spec("freighter")
    if spec is None or not spec.submodule_search_locations:
        print("ERROR: Freighter is not installed (pip install freighter)", file=sys.stderr)
        sys.exit(1)
    path = os.path.join(spec.submodule_search_locations[0], "constants.py")
    with open(path, encoding="utf-8") as f:
        src = f.read()
    win, unix = '"C:/devkitPro/devkitPPC/bin/"', '"/opt/devkitpro/devkitPPC/bin/"'
    if win not in src or unix not in src:
        print(f"ERROR: {path} does not hard-code the devkitPPC path this build "
              f"replaces; is Freighter 0.2.4 installed?", file=sys.stderr)
        sys.exit(1)
    want = repr(bin_dir.replace("\\", "/").rstrip("/") + "/")
    src = src.replace(win, want).replace(unix, want)
    mod_spec = importlib.util.spec_from_file_location("freighter.constants", path)
    mod = importlib.util.module_from_spec(mod_spec)
    sys.modules["freighter.constants"] = mod
    exec(compile(src, path, "exec"), mod.__dict__)
    return bin_dir


BIN_DIR = setup_devkitppc()


def write_symbol_map(elf, out):
    """Write T2's symbols as a Dolphin (CodeWarrior-style) map.

    Freighter's own map writer stays disabled (main): it merges into a
    CodeWarrior map of the game, which this project does not have. The
    symbols come from devkitPPC's nm on the linked ELF instead.
    """
    nm = subprocess.run([tool(BIN_DIR, "powerpc-eabi-nm"), "-n", "-S", "--defined-only", elf],
                        capture_output=True, text=True, check=True).stdout
    sections = {"t": ".text", "d": ".data", "r": ".data", "b": ".bss"}
    lines = {".text": [], ".data": [], ".bss": []}
    for line in nm.splitlines():
        parts = line.split()
        if len(parts) != 4:
            continue  # no size: a linker-script or absolute symbol
        addr, size, typ, name = int(parts[0], 16), int(parts[1], 16), parts[2].lower(), parts[3]
        sec = sections.get(typ)
        if sec is None or addr < 0x80000000:
            continue
        lines[sec].append(f"  {addr:08x} {size:06x} {addr:08x}  0 {name}")
    with open(out, "w", newline="\n") as f:
        for sec, syms in lines.items():
            if syms:
                f.write(f"{sec} section layout\n")
                f.write("  Starting        Virtual\n  address  Size   address\n  -----------------------\n")
                f.write("\n".join(syms) + "\n\n")
    print(f"Symbol map ({sum(len(s) for s in lines.values())} symbols) saved to {out}")


def run_tool(name, *args):
    """Run a devkitPPC tool, returning its stdout; exit on failure."""
    cmd = [tool(BIN_DIR, name), *args]
    r = subprocess.run(cmd, capture_output=True, text=True)
    if r.stderr.strip():
        print(r.stderr.rstrip(), file=sys.stderr)
    if r.returncode != 0:
        print(f"ERROR: {' '.join(cmd)} failed ({r.returncode})", file=sys.stderr)
        sys.exit(1)
    return r.stdout


def rel_build_id():
    """The module's build id: a CRC-32 of its sources and symbol script.

    Passed as -DWWREL_BUILD_ID, written into the mailbox slack by _prolog
    and read back by Go. A source hash, not a timestamp, so the same
    sources give the same ELF (blob.stamp records its hash).
    """
    crc = 0
    for path in sorted(REL_SOURCES + [REL_LD]):
        with open(path, "rb") as f:
            crc = zlib.crc32(f.read().replace(b"\r\n", b"\n"), crc)
    return crc & 0xFFFFFFFF


def build_rel():
    """Compile rel/ and link it into REL_ELF, a relocatable ELF.

    Prints the section table, the relocation counts per type, the
    symbols the converter needs and the .text size, and fails on what a
    REL cannot carry: an undefined symbol (the game would route the
    call to _unresolved), an R_PPC_REL32 relocation, a small-data
    section, or a missing REL_SYMBOLS entry.
    """
    print()
    print("Building the REL module (rel/)...")
    os.makedirs(REL_OBJ_DIR, exist_ok=True)
    build_id = rel_build_id()
    objs = []
    for src in REL_SOURCES:
        obj = os.path.join(REL_OBJ_DIR, os.path.basename(src) + ".o")
        if os.path.exists(obj):
            os.remove(obj)
        run_tool("powerpc-eabi-gcc", *REL_CFLAGS, f"-DWWREL_BUILD_ID=0x{build_id:08X}",
                 "-c", src, "-o", obj)
        objs.append(obj)
    for out in (REL_ELF, REL_MAP):
        if os.path.exists(out):
            os.remove(out)
    run_tool("powerpc-eabi-ld", "--relocatable", "-T", REL_LD, "-Map", REL_MAP,
             "-o", REL_ELF, *objs)

    sections = run_tool("powerpc-eabi-readelf", "-S", "-W", REL_ELF)
    print(sections.rstrip())
    bad = sorted(s for s in REL_BAD_SECTIONS if f" {s} " in sections or f" {s}\t" in sections)
    if bad:
        print(f"ERROR: {REL_ELF} has {', '.join(bad)}: a REL cannot carry them", file=sys.stderr)
        sys.exit(1)

    undefined = run_tool("powerpc-eabi-nm", "-u", REL_ELF).split()
    if undefined:
        print(f"ERROR: {REL_ELF} has undefined symbols (missing from {REL_LD}?): "
              f"{' '.join(undefined)}", file=sys.stderr)
        sys.exit(1)

    counts = {}
    for line in run_tool("powerpc-eabi-readelf", "-r", "-W", REL_ELF).splitlines():
        parts = line.split()
        if len(parts) >= 3 and parts[2].startswith("R_PPC_"):
            counts[parts[2]] = counts.get(parts[2], 0) + 1
    print("Relocations: " + (", ".join(f"{k} x{v}" for k, v in sorted(counts.items())) or "none"))
    if "R_PPC_REL32" in counts:
        print("ERROR: R_PPC_REL32 relocations: the converter does not emit them "
              "(a switch jump table? .eh_frame?)", file=sys.stderr)
        sys.exit(1)

    # objdump -t: `<value> <flags> <section>\t<size> <name>`.
    syms = {}
    for line in run_tool("powerpc-eabi-objdump", "-t", REL_ELF).splitlines():
        m = re.match(r"^([0-9a-f]{8}) .* (\S+)\t([0-9a-f]{8}) (\S+)$", line)
        if m:
            syms[m.group(4)] = (m.group(2), int(m.group(1), 16), int(m.group(3), 16))
    missing = [s for s in REL_SYMBOLS if s not in syms]
    if missing:
        print(f"ERROR: {REL_ELF} lacks {', '.join(missing)}", file=sys.stderr)
        sys.exit(1)
    print("Symbols:")
    for name in REL_SYMBOLS + ["g_wwrel_build_id"]:
        sec, off, size = syms[name]
        print(f"  {name:<18} {sec}+0x{off:04X}  size 0x{size:X}")
    # readelf -S -W: `  [ 1] .text  PROGBITS  <addr> <off> <size> ...`.
    m = re.search(r"\]\s+\.text\s+PROGBITS\s+[0-9a-f]+\s+[0-9a-f]+\s+([0-9a-f]+)", sections)
    text = int(m.group(1), 16) if m else 0
    print(f"  .text size 0x{text:X} ({text} bytes); build id 0x{build_id:08X}")
    print(f"REL module saved to {REL_ELF} (map: {REL_MAP})")


def main():
    # Fix encoding for Freighter's emoji output
    os.environ["PYTHONIOENCODING"] = "utf-8"
    try:
        sys.stdout.reconfigure(encoding="utf-8", errors="replace")
        sys.stderr.reconfigure(encoding="utf-8", errors="replace")
    except Exception:
        pass

    print(f"devkitPPC: {os.environ['DEVKITPPC']}")

    from freighter import Project

    # Monkey-patch to skip Freighter's map saving: it merges our symbols
    # into a CodeWarrior map of the game (it crashes without one).
    # write_symbol_map below writes T2's symbols instead.
    import freighter.devkit_tools
    freighter.devkit_tools.Project._Project__save_map = lambda self: None

    # Project setup
    proj = Project("WWMultiplayer", "GZLE01", auto_import=False)

    # Add source and include. The C side is several units (Phase E3,
    # docs/api/00-design.md §6.6): the core (its hooks and services) and
    # the multiplayer mod. Freighter names each object after its file's
    # basename (build/temp/<name>.c.o), so basenames must be unique.
    # Objects are removed first: Freighter compiles in child processes
    # and links whatever objects are on disk, so a unit that failed to
    # compile would otherwise link its previous object.
    proj.add_include_folder("include")
    for src in C_SOURCES:
        obj = os.path.join("build", "temp", os.path.basename(src) + ".o")
        if os.path.exists(obj):
            os.remove(obj)
        proj.add_c_file(src)

    # One-shot init hook on main01 (0x80006338 — proven working).
    # main01_init publishes &multiplayer_update at CALLBACK_PTR_ADDR
    # (mailbox+0x08 = 0x80412F08, src/core/hooks.c) for frame_shim.
    proj.hook_branchlink("main01_init", 0x80006338)

    # Per-frame hook. Targets the LAST bl in fapGm_Execute (0x80023204 ->
    # 0x802449AC). Chosen because: past the prologue (stack frame valid),
    # replaces a bl (caller already assumes volatile-reg clobber), return
    # value is discarded by the caller, and the PRIMARY per-frame work at
    # 0x800231FC still runs ahead of us. frame_shim runs multiplayer_update
    # and then tail-calls 0x802449AC with the caller's LR restored, so the
    # replaced call still runs exactly as before (see frame_shim in
    # src/core/hooks.c).
    proj.hook_branchlink("frame_shim", 0x80023204)

    # Draw-phase hook. `daPy_Draw` at 0x80108204 is Link #1's draw thunk;
    # at 0x80108210 it bls the real draw impl `daPy_lk_c::draw` at
    # 0x80107308. Freighter replaces that bl with a bl to our
    # `daPy_draw_hook`, which calls the original implementation and then
    # runs the mods' post-draw steps (the multiplayer mod's mini-Links),
    # so our modelEntryDL submissions
    # land in the legitimate actor-draw iteration. This is the live draw
    # path for the mini-Link puppets (shadow_mode 5 pose feed) and the
    # face/tunic swaps. The early sky-texture breakage came from sharing
    # Link's J3DModelData, not from this hook; see docs/05-known-issues.md
    # → "Mini-Link render pipeline".
    proj.hook_branchlink("daPy_draw_hook", 0x80108210)

    # Input-injection hook. JUTGamePad::read at 0x802C3980 calls
    # PADRead at 0x802C39A0; we replace that bl with a bl to
    # pad_read_shim, which calls real PADRead first then optionally
    # overrides mPadStatus[0] from the mailbox. JUTGamePad::read's
    # downstream .update() derivations then pick up our synthetic
    # values, so mPadButton[0] / mPadMStick[0] / mPadSStick[0] —
    # what the game's logic actually reads — reflect the override.
    # Disabled at idle (input_enable=0 ⇒ pass-through).
    proj.hook_branchlink("pad_read_shim", 0x802C39A0)

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
    # No small-data sections. Split into several units (Phase E3), the
    # globals one unit shares with another (mini_link_state, ...) would
    # land in .sbss, which Freighter's linker script places at T2's start
    # ahead of .rodata (Go's contract check hashes T2's read-only
    # sections from there, internal/inject TestBlobContract). Addressing
    # is unchanged: nothing here uses r13/r2-relative small data.
    proj.common_args.append("-G0")

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

    # Post-build: patch OSInit so __OSArenaLo starts at 0x80414000 (past our
    # T2 at 0x80410000 + the mailbox at 0x80412F00) instead of the
    # linker's 0x8040EFC0. Arena loses ~24 KB from the original layout.
    #
    # History: was 0x80411000 while mod fit in 0xF00 bytes; echo-ring code
    # (docs/06 track 1) pushed .text past 0x80411000, bumping to 0x80412000.
    # Eye-fix recipe (item #9 attempt 4) grew mod past 0x80411F00, bumping
    # to 0x80413000. Session 6 (mode-1 freeze diagnostics) added
    # dbg_postswap_* fields that pushed mailbox size past 0x100; bumped to
    # 0x80414000 so mailbox has 0x1000 = 4 KB of headroom for further
    # diagnostics without another arena bump. Mailbox lives at
    # 0x80412F00..0x80413nnn; MUST stay in sync with MAILBOX_ADDR in
    # inject/include/mailbox_core.h.
    #
    # Do NOT bump further without measuring ZeldaHeap headroom: the 1 MB
    # carve-out experiment (0x80511000) broke Outset archive loading. A few
    # KB is well inside noise (ZeldaHeap ~6 MB).
    T1_LOAD = 0x800056e0
    T1_FILE = 0x2620
    patches = [
        (0x80301818, b"\x60\x00\x00\x00"),  # nop (always fall through)
        (0x8030181C, b"\x3c\x60\x80\x41"),  # lis r3, 0x8041
        (0x80301820, b"\x38\x63\x40\x00"),  # addi r3, r3, 0x4000  -> r3 = 0x80414000
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

    write_symbol_map(ELF, SYMBOL_MAP)

    if "--no-rel" not in sys.argv[1:]:
        build_rel()

if __name__ == '__main__':
    main()
