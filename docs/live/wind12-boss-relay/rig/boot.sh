#!/usr/bin/env bash
# boot.sh <A|B> [fixture=dolphin-user-4 for A, dolphin-user-3 for B]
# Copies the fixture user dir into $RUN/ud-<A|B> (fresh each time), launches the re-signed Dolphin
# through LaunchServices (`open -n -a`, as wwapi/emu/launch/launch_darwin.go does: a direct exec of
# Contents/MacOS/Dolphin loads the hardened binary and task_for_pid fails), and records the new PID
# in $RUN/<A|B>.pid. Take a ww/dolphin-slot-N lease for the pair first.
set -euo pipefail; . "$(dirname "$0")/env.sh"
side=${1:?A or B}; fx=${2:-$([ "$side" = A ] && echo dolphin-user-4 || echo dolphin-user-3)}
[ -f "$ISO" ] || { echo "no patched disc at $ISO (wind#3 makes it)"; exit 2; }
ud="$RUN/ud-$side"; rm -rf "$ud"; cp -R "$FIXTURES/$fx" "$ud"
args=(-u "$ud" -e "$ISO" -C Dolphin.Core.RAMOverrideEnable=True -C Dolphin.Core.MEM1Size=0x03000000)
[ -n "$START_SAV" ] && args+=(-s "$START_SAV")
before=$(pgrep -x Dolphin | sort || true)
/usr/bin/open -n -a "$DOLPHIN_APP" --args "${args[@]}"
for _ in $(seq 1 100); do
  new=$(comm -13 <(echo "$before") <(pgrep -x Dolphin | sort) | head -1)
  [ -n "$new" ] && { echo "$new" > "$RUN/$side.pid"; echo "$new"; exit 0; }; sleep 0.1
done
echo "Dolphin launched but no new PID within 10 s"; exit 1
