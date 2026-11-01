#!/usr/bin/env bash
# fresh-user.sh <user-N>: copy ~/ww-shared/fixtures/dolphin-<user-N> to this card's rig dir
# (never run Dolphin on the shared original). A fresh copy is also the card reset:
# the fixtures hold 28-Hyrule3 (md5 3162e4a9f32ca3d8929ce5f4795c7166 on the PC).
set -euo pipefail; . "$(dirname "$0")/env.sh"
src="$WW_SHARED/fixtures/dolphin-$1"; dst="$RIG_DIR/dolphin-$1"
[ -d "$src" ] || { echo "no $src (wait for wind#3)"; exit 2; }
rm -rf "$dst"; cp -R "$src" "$dst"
find "$dst/GC" -name "*.gci" -exec md5 {} \; 2>/dev/null; echo "$dst"
