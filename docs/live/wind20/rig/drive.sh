#!/usr/bin/env bash
# drive.sh <pid>: press A until Link is in play (as the PC's driveg.sh).
. "$(dirname "$0")/env.sh"; cd "$WW_REPO"; P=$1
for i in $(seq 1 30); do
  t=$(WW_DOLPHIN_PID=$P ./ww-multiplayer coop-tag show 2>&1 | head -1)
  echo "$t" | grep -qE '"(Name|sea_T)"|"" |^$' || { echo "$P in play: $t"; exit 0; }
  WW_DOLPHIN_PID=$P ./wwdev input 0x0100 0 0 100 >/dev/null; sleep 2
done; echo "$P not in play after 30 tries"; exit 1
