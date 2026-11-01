#!/bin/bash
# healer.sh <pid>: keep Link's life at max every second (test rig only; kill by its PID)
. "$(dirname "$0")/lib.sh"; export WW_DOLPHIN_PID=$1
while true; do w=$(pk 0x803C4C08); [ -n "$w" ] && "$WWDEV" poke-u32 0x803C4C08 $(printf 0x%08X $(( (w & 0xFFFF0000) | (w >> 16) ))) >/dev/null 2>&1; sleep 1; done
