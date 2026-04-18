"""
Splice patched.dol into WW_Multiplayer_Patched.iso.

Assumes the ISO was already laid out by an earlier `wit copy` run with its
FST shifted past the expected DOL region. This script only overwrites the
DOL bytes in place — fast, idempotent, and safe as long as the new DOL
doesn't grow past the reserved space between DOL offset and FST offset.

If the new DOL is larger than the reserved space, it aborts rather than
clobbering the FST. At that point you'd need to regenerate the ISO with a
fresh `wit copy` + FST shift.

Also clears Dolphin's gamelist cache so the new ISO shows up.
"""

import os
import struct
import sys

# DOL_PATH resolves relative to this script so the command works from any CWD.
# ISO_PATH is outside the repo; set WW_ISO_PATH env var to override the default.
_HERE = os.path.dirname(os.path.abspath(__file__))
DOL_PATH = os.path.join(_HERE, "patched.dol")
ISO_PATH = os.environ.get(
    "WW_ISO_PATH",
    r"C:\Users\4step\Desktop\Dolphin-x64\Roms\WW_Multiplayer_Patched.iso",
)
DOLPHIN_CACHE = os.path.expandvars(
    r"%APPDATA%\Dolphin Emulator\Cache\gamelist.cache"
)


def main():
    if not os.path.exists(ISO_PATH):
        print(f"ERROR: ISO not found at {ISO_PATH}")
        sys.exit(1)
    if not os.path.exists(DOL_PATH):
        print(f"ERROR: patched.dol not found — run build.py first")
        sys.exit(1)

    dol_bytes = open(DOL_PATH, "rb").read()
    dol_size = len(dol_bytes)

    with open(ISO_PATH, "r+b") as f:
        f.seek(0x420)
        dol_off, fst_off, fst_size, _ = struct.unpack(">IIII", f.read(16))
        reserved = fst_off - dol_off
        print(f"ISO DOL offset : 0x{dol_off:08x}")
        print(f"ISO FST offset : 0x{fst_off:08x}")
        print(f"Reserved space : 0x{reserved:x} ({reserved} bytes)")
        print(f"New DOL size   : 0x{dol_size:x} ({dol_size} bytes)")

        if dol_size > reserved:
            print(
                f"ERROR: new DOL exceeds reserved space by "
                f"0x{dol_size - reserved:x} bytes — FST would be clobbered"
            )
            print("Regenerate the ISO via `wit copy` + FST shift.")
            sys.exit(2)

        f.seek(dol_off)
        f.write(dol_bytes)
        print(f"Wrote patched.dol at ISO offset 0x{dol_off:08x}")

    if os.path.exists(DOLPHIN_CACHE):
        os.remove(DOLPHIN_CACHE)
        print(f"Cleared Dolphin gamelist cache: {DOLPHIN_CACHE}")
    else:
        print("No Dolphin gamelist cache to clear (ok)")

    print()
    print("Done. Restart Dolphin fully, then boot WW_Multiplayer_Patched.iso.")


if __name__ == "__main__":
    main()
