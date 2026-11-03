#!/bin/bash
# walk.sh <pid> <phase 0-3> <secs>: walk a square (1.2 s per side), starting at side <phase>; stops when <secs> pass or ~/ww-shared/rigs/wind89/run/walk.stop exists.
W=~/ww-shared/rigs/wind89/bin/wwdev; P=$1; ph=$2; end=$((SECONDS+$3))
dirs=("0 100" "100 0" "0 -100" "-100 0")
i=$ph
while [ $SECONDS -lt $end ] && [ ! -f ~/ww-shared/rigs/wind89/run/walk.stop ]; do
  set -- ${dirs[$((i%4))]}
  WW_DOLPHIN_PID=$P $W input 0 $1 $2 ${SIDE_MS:-1200} >/dev/null 2>&1
  i=$((i+1))
done
WW_DOLPHIN_PID=$P $W input-release >/dev/null 2>&1
