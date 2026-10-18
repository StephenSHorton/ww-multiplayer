"""
Extract the injected T2 section from Freighter's patched.dol and emit a Go
source file (internal/inject/blob.go) that the standalone `ww-multiplayer.exe patch`
subcommand uses to splice the same delta into a user's vanilla Wind Waker
ISO. Run after `inject/build.py` produces a fresh patched.dol.

Outputs the T2 bytes, the blob's contract constants (Phase E1: the
mailbox address, the WWMB ABI, layout version and features word from
inject/include/mailbox_core.h, the patched __OSArenaLo, where T2 and its .text
end, and BlobHash, the CRC-32 of T2's read-only sections (HashRanges:
.rodata, .text, .init, .fini) that wwapi's contract check compares
against the same ranges read back from RAM) and DOLPatches: every
in-DOL byte run that differs
between original.dol and patched.dol (the four hook_branchlink sites and
the OSInit __OSArenaLo immediates), found by a byte-wise diff, which
internal/inject/dol.go applies. The Freighter clobbers that build.py
reverts never show up in the diff, because build.py restores the original
bytes before this script runs.

The user's vanilla DOL is the source of all "original game code" bytes;
nothing copyrighted ships in our binary.
"""

import os
import struct
import subprocess
import sys
import datetime
import re
import zlib

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
ORIG = os.path.join(ROOT, "inject", "original.dol")
PATCHED = os.path.join(ROOT, "inject", "patched.dol")
OUT = os.path.join(ROOT, "internal", "inject", "blob.go")
# Freighter's linked ELF (build.py builds with clean_up=False): its section
# table says where T2's read-only sections (and .text) start and end.
ELF = os.path.join(ROOT, "inject", "build", "temp", "WWMultiplayer.o")
MAILBOX_H = os.path.join(ROOT, "inject", "include", "mailbox_core.h")

# The patched OSInit's `lis r3, hi` / `addi r3, r3, lo` (inject/build.py).
T1_LOAD, T1_FILE = 0x800056E0, 0x2620
ARENA_LIS, ARENA_ADDI = 0x8030181C, 0x80301820

# ELF section header bits.
SHT_NOBITS = 8
SHF_WRITE, SHF_ALLOC = 0x1, 0x2


def elf_sections(path):
    """Every section of a big-endian ELF32: (name, type, flags, addr, size, file bytes)."""
    with open(path, "rb") as f:
        e = f.read()
    if e[:4] != b"\x7fELF" or e[4] != 1 or e[5] != 2:
        print(f"ERROR: {path} is not a big-endian ELF32", file=sys.stderr)
        sys.exit(1)
    shoff, = struct.unpack(">I", e[0x20:0x24])
    shentsize, shnum, shstrndx = struct.unpack(">HHH", e[0x2E:0x34])

    def sh(i):
        return struct.unpack(">10I", e[shoff + i * shentsize:shoff + i * shentsize + 40])
    stroff = sh(shstrndx)[4]
    out = []
    for i in range(shnum):
        h = sh(i)
        start = stroff + h[0]
        name = e[start:e.index(b"\x00", start)].decode()
        data = e[h[4]:h[4] + h[5]] if h[1] != SHT_NOBITS else b""
        out.append((name, h[1], h[2], h[3], h[5], data))
    return out


def header_define(name):
    """A numeric #define from mailbox_core.h."""
    with open(MAILBOX_H, encoding="utf-8") as f:
        m = re.search(r"(?m)^#define\s+" + name + r"\s+(0x[0-9A-Fa-f]+|\d+)\s*(?://.*)?$", f.read())
    if not m:
        print(f"ERROR: {MAILBOX_H}: no #define {name}", file=sys.stderr)
        sys.exit(1)
    return int(m.group(1), 0)


def arena_lo(dol):
    """The __OSArenaLo the patched OSInit computes (lis r3 / addi r3,r3)."""
    def word(a):
        off = T1_FILE + (a - T1_LOAD)
        return struct.unpack(">I", dol[off:off + 4])[0]
    lis, addi = word(ARENA_LIS), word(ARENA_ADDI)
    if lis & 0xFFFF0000 != 0x3C600000 or addi & 0xFFFF0000 != 0x38630000:
        print(f"ERROR: no lis r3 / addi r3,r3 at 0x{ARENA_LIS:08X}: 0x{lis:08X} 0x{addi:08X}", file=sys.stderr)
        sys.exit(1)
    lo = addi & 0xFFFF
    if lo & 0x8000:
        lo -= 0x10000
    return ((lis & 0xFFFF) << 16) + lo


def main():
    if not os.path.exists(ORIG):
        print(f"ERROR: {ORIG} not found", file=sys.stderr)
        sys.exit(1)
    if not os.path.exists(PATCHED):
        print(f"ERROR: {PATCHED} not found — run inject/build.py first", file=sys.stderr)
        sys.exit(1)

    with open(ORIG, "rb") as f:
        orig = f.read()
    with open(PATCHED, "rb") as f:
        patched = f.read()

    if len(patched) <= len(orig):
        print(f"ERROR: patched ({len(patched)}) not larger than original ({len(orig)})",
              file=sys.stderr)
        sys.exit(1)

    blob = patched[len(orig):]

    # Read T2 section metadata from patched DOL header.
    text_off = struct.unpack(">7I", patched[0x00:0x1C])
    text_addr = struct.unpack(">7I", patched[0x48:0x64])
    text_size = struct.unpack(">7I", patched[0x90:0xAC])

    # T2 is the slot Freighter chose. The index is hard-coded to text slot
    # 2; the check below fails loudly if Freighter ever picks another slot.
    # The Go patcher does not depend on the index: internal/inject/dol.go
    # takes the first unused text slot.
    t2_idx = 2
    if (text_off[t2_idx] != len(orig) or
            text_size[t2_idx] != len(blob)):
        print(f"ERROR: T2 section header doesn't match appended bytes:", file=sys.stderr)
        print(f"  T2 file_off=0x{text_off[t2_idx]:x} size=0x{text_size[t2_idx]:x}", file=sys.stderr)
        print(f"  appended at=0x{len(orig):x} len=0x{len(blob):x}", file=sys.stderr)
        sys.exit(1)

    t2_address = text_addr[t2_idx]
    t2_size = text_size[t2_idx]
    t2_end = t2_address + t2_size

    # The contract constants (Phase E1). BlobHash covers the read-only
    # part of the image: every allocated section without SHF_WRITE
    # (.rodata, .text, .init, .fini), whose bytes never change at run
    # time; .data, .bss, .ctors and .dtors are left out. Each hashed
    # section's bytes in the ELF must equal the same range of the blob,
    # so a stale ELF from another build fails here instead of producing
    # a wrong BlobHash.
    text_start = text_end = None
    ro = []
    for name, typ, flags, addr, size, data in elf_sections(ELF):
        if name == ".text":
            text_start, text_end = addr, addr + size
        if not flags & SHF_ALLOC or flags & SHF_WRITE or typ == SHT_NOBITS or size == 0:
            continue
        if not (t2_address <= addr and addr + size <= t2_end):
            print(f"ERROR: {name} 0x{addr:08x}..0x{addr + size:08x} is not inside T2 "
                  f"0x{t2_address:08x}..0x{t2_end:08x}: is {ELF} from this build?", file=sys.stderr)
            sys.exit(1)
        if blob[addr - t2_address:addr - t2_address + size] != data:
            print(f"ERROR: {name} in {ELF} differs from patched.dol's T2: "
                  f"the ELF is not from this build (rebuild with inject/build.py)", file=sys.stderr)
            sys.exit(1)
        ro.append((addr, addr + size))
    if text_start is None:
        print(f"ERROR: {ELF} has no .text section", file=sys.stderr)
        sys.exit(1)
    hash_ranges = []
    for a, b in sorted(ro):
        if hash_ranges and a <= hash_ranges[-1][1]:
            hash_ranges[-1][1] = max(hash_ranges[-1][1], b)
        else:
            hash_ranges.append([a, b])
    blob_hash = 0
    for a, b in hash_ranges:
        blob_hash = zlib.crc32(blob[a - t2_address:b - t2_address], blob_hash)
    blob_hash &= 0xFFFFFFFF
    mailbox_addr = header_define("MAILBOX_ADDR")
    abi = header_define("WWMB_ABI")
    layout = header_define("WWMB_LAYOUT")
    features = header_define("WW_FEATURES")
    arena = arena_lo(patched)

    # Diff orig vs patched within the original DOL's footprint to find every
    # in-DOL byte modification: the hook_branchlink sites Freighter writes,
    # the OSInit immediate patches that build.py applies post-Freighter,
    # plus any future direct edits. Excludes the appended T2 section
    # (handled separately) and the DOL header changes (the standalone
    # patcher rebuilds those from scratch).
    HEADER_END = 0x100
    patch_runs = []  # list of (file_off, bytes)
    i = HEADER_END
    n = len(orig)
    while i < n:
        if patched[i] != orig[i]:
            j = i
            while j < n and patched[j] != orig[j]:
                j += 1
            patch_runs.append((i, patched[i:j]))
            i = j
        else:
            i += 1

    print(f"Extracted T2 blob:")
    print(f"  size:    0x{t2_size:x} ({t2_size} bytes)")
    print(f"  address: 0x{t2_address:08x}")
    print(f"  end:     0x{t2_end:08x} (mailbox 0x{mailbox_addr:08x}, arena 0x{arena:08x})")
    print(f"  .text:   0x{text_start:08x}..0x{text_end:08x}")
    print(f"  hashed:  {', '.join(f'0x{a:08x}..0x{b:08x}' for a, b in hash_ranges)}, CRC-32 0x{blob_hash:08x}")
    print(f"  WWMB:    ABI {abi}, layout {layout}, features 0x{features:x}")
    print(f"In-DOL patches: {len(patch_runs)} run(s)")
    for off, b in patch_runs:
        print(f"  @ 0x{off:08x}  {len(b)}B  {b.hex()}")
    print(f"  output:  {OUT}")

    # Emit blob.go.
    os.makedirs(os.path.dirname(OUT), exist_ok=True)
    lines = []
    lines.append("// Code generated by scripts/extract_blob.py - DO NOT EDIT.")
    lines.append(f"// Source: inject/patched.dol minus inject/original.dol.")
    lines.append(f"// Built:  {datetime.datetime.now(datetime.UTC).isoformat(timespec='seconds').replace('+00:00', 'Z')}")
    lines.append("")
    lines.append("package inject")
    lines.append("")
    lines.append("// T2Address is where the appended section lives in MEM1 once the")
    lines.append("// patched DOL is loaded by Dolphin's apploader emulation. Must match")
    lines.append("// the inject_address Freighter was configured with in inject/build.py.")
    lines.append(f"const T2Address uint32 = 0x{t2_address:08X}")
    lines.append("")
    lines.append("// The blob's contract (Phase E1, docs/api/00-design.md section 6.4, step 5).")
    lines.append("// wwapi's contract check compares the WWMB header main01_init writes")
    lines.append("// with ABI and LayoutVersion, and the CRC-32 of the HashRanges it reads")
    lines.append("// back from RAM with BlobHash.")
    lines.append("const (")
    lines.append("\t// T2End is where T2 ends: T2Address + len(T2Bytes).")
    lines.append(f"\tT2End uint32 = 0x{t2_end:08X}")
    lines.append("\t// TextStart and TextEnd bound T2's .text, the code.")
    lines.append(f"\tTextStart uint32 = 0x{text_start:08X}")
    lines.append(f"\tTextEnd   uint32 = 0x{text_end:08X}")
    lines.append("\t// BlobHash is the CRC-32 (IEEE) of T2Bytes over HashRanges, in order.")
    lines.append(f"\tBlobHash uint32 = 0x{blob_hash:08X}")
    lines.append("\t// MailboxAddr is mailbox_core.h's MAILBOX_ADDR.")
    lines.append(f"\tMailboxAddr uint32 = 0x{mailbox_addr:08X}")
    lines.append("\t// ArenaLo is the __OSArenaLo the patched OSInit sets.")
    lines.append(f"\tArenaLo uint32 = 0x{arena:08X}")
    lines.append("\t// ABI, LayoutVersion and Features are what main01_init writes into")
    lines.append("\t// the WWMB header (mailbox_core.h WWMB_ABI, WWMB_LAYOUT, WW_FEATURES).")
    lines.append(f"\tABI           uint16 = {abi}")
    lines.append(f"\tLayoutVersion uint16 = {layout}")
    lines.append(f"\tFeatures      uint32 = 0x{features:02X}")
    lines.append(")")
    lines.append("")
    lines.append("// HashRanges are the [start, end) address ranges BlobHash covers: the")
    lines.append("// read-only part of T2 (.rodata, .text, .init, .fini: every allocated")
    lines.append("// section without SHF_WRITE), which never changes at run time. The")
    lines.append("// .data, .bss, .ctors and .dtors do.")
    lines.append("var HashRanges = [][2]uint32{")
    for a, b in hash_ranges:
        lines.append(f"\t{{0x{a:08X}, 0x{b:08X}}},")
    lines.append("}")
    lines.append("")
    lines.append("// T2Bytes is the full content of the new text section. Splicing it")
    lines.append("// into a user's vanilla DOL means: pick the first unused text-section")
    lines.append("// slot in the DOL header, set its file_offset = old_dol_size,")
    lines.append("// mem_addr = T2Address, size = len(T2Bytes), then append the bytes")
    lines.append("// to the end of the DOL. See internal/inject/dol.go.")
    lines.append("var T2Bytes = []byte{")

    # 16 bytes per line.
    for i in range(0, len(blob), 16):
        chunk = blob[i:i + 16]
        hex_bytes = ", ".join(f"0x{b:02X}" for b in chunk)
        lines.append(f"\t{hex_bytes},")

    lines.append("}")
    lines.append("")

    # In-DOL patch records (currently the OSInit arena-lo immediate writes
    # that point __OSArenaLo past the mailbox). Captured by diffing
    # original vs patched within the original DOL's footprint, so any
    # future direct game-code edit Freighter applies will be picked up
    # automatically without changing this script.
    lines.append("// DOLPatch is a single contiguous byte run to write into the user's")
    lines.append("// vanilla DOL at FileOff. Used by inject/dol.go.")
    lines.append("type DOLPatch struct {")
    lines.append("\tFileOff uint32")
    lines.append("\tBytes   []byte")
    lines.append("}")
    lines.append("")
    lines.append("// DOLPatches captures every in-DOL byte modification Freighter +")
    lines.append("// inject/build.py performs on top of the appended T2 section.")
    lines.append("// Today: the four OSInit immediates that bump __OSArenaLo past")
    lines.append("// our T2 + mailbox. Tomorrow: any direct game-code edit added.")
    lines.append("// Auto-extracted via byte-wise diff so this list stays correct.")
    lines.append("var DOLPatches = []DOLPatch{")
    for off, b in patch_runs:
        bs = ", ".join(f"0x{x:02X}" for x in b)
        lines.append(f"\t{{FileOff: 0x{off:08X}, Bytes: []byte{{{bs}}}}},")
    lines.append("}")
    lines.append("")

    with open(OUT, "w", newline="\n") as f:
        f.write("\n".join(lines))

    # Restamp internal/inject/blob.stamp: it binds this blob.go to the C
    # inputs it was built from, and TestBlobFresh checks it on every PR.
    r = subprocess.run(["go", "run", "./tools/blobstamp"], cwd=ROOT)
    if r.returncode != 0:
        print("ERROR: blob.go was written but `go run ./tools/blobstamp` failed; "
              "run it from the repo root before committing", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
