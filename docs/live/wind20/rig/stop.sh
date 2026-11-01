#!/usr/bin/env bash
# stop.sh <name>...: kill by the recorded PID in $RIG_DIR/pids (never by command-line match).
# stop.sh -STOP <name> / -CONT <name> suspends / resumes (R7a).
. "$(dirname "$0")/env.sh"; sig=-TERM
case "${1:-}" in -STOP|-CONT|-KILL) sig=$1; shift;; esac
for n in "$@"; do f="$RIG_DIR/pids/$n"; [ -f "$f" ] || { echo "no pid for $n"; continue; }
  p=$(cat "$f"); kill $sig "$p" && echo "$sig $n ($p)"; [ "$sig" = -TERM ] && rm -f "$f"; done
