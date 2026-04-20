#!/usr/bin/env bash
# Launch a SECOND Dolphin instance pointing at the patched ISO with its
# own User dir. Used for two-Dolphin multiplayer development on a single
# machine without juggling configs.
#
# First run: bootstraps "Dolphin Emulator 2" by copying the user's existing
# Dolphin user dir (so Wind Waker save data, controller mappings, etc.
# carry over). Subsequent runs reuse the same user dir.
#
# Usage:
#   scripts/dolphin2.sh           # launch second Dolphin
#   scripts/dolphin2.sh --reset   # delete second user dir before launching
#                                 # (useful if config got corrupted)

set -euo pipefail

DOLPHIN_EXE="${DOLPHIN_EXE:-/c/Users/4step/Desktop/Dolphin-x64/Dolphin.exe}"
ISO_PATH="${ISO_PATH:-/c/Users/4step/Desktop/Dolphin-x64/Roms/WW_Multiplayer_Patched.iso}"
USER_DIR_1="${APPDATA}/Dolphin Emulator"
USER_DIR_2="${APPDATA}/Dolphin Emulator 2"

if [[ "${1:-}" == "--reset" ]]; then
    echo "Removing $USER_DIR_2 ..."
    rm -rf "$USER_DIR_2"
fi

if [[ ! -f "$DOLPHIN_EXE" ]]; then
    echo "ERROR: Dolphin not found at $DOLPHIN_EXE"
    echo "Set DOLPHIN_EXE env var to override."
    exit 1
fi
if [[ ! -f "$ISO_PATH" ]]; then
    echo "ERROR: Patched ISO not found at $ISO_PATH"
    echo "Run 'cd inject && python build.py && python patch_iso.py' first."
    exit 1
fi
if [[ ! -d "$USER_DIR_1" ]]; then
    echo "ERROR: Primary Dolphin user dir not found at $USER_DIR_1"
    exit 1
fi

if [[ ! -d "$USER_DIR_2" ]]; then
    echo "Bootstrapping $USER_DIR_2 from $USER_DIR_1 ..."
    # cp -r preserves the entire tree including saves (GC/USA/Card.raw),
    # configs, controller mappings. Cache/ is intentionally not skipped —
    # gamelist cache regenerates on demand and saves time on first boot.
    cp -r "$USER_DIR_1" "$USER_DIR_2"
    echo "Done. ($(du -sh "$USER_DIR_2" | cut -f1))"
fi

echo "Launching second Dolphin:"
echo "  exe : $DOLPHIN_EXE"
echo "  user: $USER_DIR_2"
echo "  iso : $ISO_PATH"
echo
# -u sets the user dir; -e auto-boots the ISO. Backgrounded so the
# shell returns immediately. Output suppressed (Dolphin spams stderr).
"$DOLPHIN_EXE" -u "$USER_DIR_2" -e "$ISO_PATH" >/dev/null 2>&1 &
PID=$!
echo "Launched (host PID $PID). Use 'WW_DOLPHIN_INDEX=1' to address this"
echo "instance from broadcast-pose / puppet-sync (the original is index 0)."
