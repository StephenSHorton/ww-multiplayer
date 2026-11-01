#!/usr/bin/env bash
# healer.sh <pid>...: keep Link's life at max every second (test rig only). Runs in the
# background; its own PID goes to $RIG_DIR/pids/healer. Stop with stop.sh healer.
. "$(dirname "$0")/env.sh"; cd "$WW_REPO"
( while true; do
    for p in "$@"; do
      w=$(WW_DOLPHIN_PID=$p ./wwdev peek 803C4C08 4 2>/dev/null | grep -oE "0x[0-9A-F]{8} " | head -1)
      [ -n "$w" ] && WW_DOLPHIN_PID=$p ./wwdev poke-u32 803C4C08 $(printf "%X" $(( (w & 0xFFFF0000) | (w >> 16) ))) >/dev/null 2>&1
    done; sleep 1
  done ) >/dev/null 2>&1 &
echo $! > "$RIG_DIR/pids/healer"; echo $!
